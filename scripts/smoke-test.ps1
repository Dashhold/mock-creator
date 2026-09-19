# End-to-end check of the API against a running stack.
#
# Exercises the parts that only fail at runtime: migrations, the aggregate
# queries, exam-association link materialisation, the job worker, blueprint
# selection and paper export. Creates its own data and deletes it again, so it is
# safe to run against a database you care about.
#
# Usage, with the stack up:
#   .\scripts\smoke-test.ps1
#   .\scripts\smoke-test.ps1 -BaseUrl http://localhost:8090 -ApiKey secret
#
# Document conversion is not covered here: that needs a real file, and the
# Documents page is the better place to try it.

param(
    [string]$BaseUrl = 'http://localhost:8090',
    [string]$ApiKey = ''
)

$ErrorActionPreference = 'Stop'
$base = "$($BaseUrl.TrimEnd('/'))/api/v1"
$fails = 0

# An API key is only required when the server has one configured.
$PSDefaultParameterValues = @{}
if ($ApiKey) {
    $PSDefaultParameterValues['Invoke-RestMethod:Headers'] = @{ 'X-API-Key' = $ApiKey }
    $PSDefaultParameterValues['Invoke-WebRequest:Headers'] = @{ 'X-API-Key' = $ApiKey }
}

function Step($name, $block) {
    try {
        $result = & $block
        Write-Host "PASS  $name" -ForegroundColor Green
        return $result
    } catch {
        $script:fails++
        Write-Host "FAIL  $name" -ForegroundColor Red
        Write-Host "      $($_.Exception.Message)" -ForegroundColor Red
        if ($_.ErrorDetails.Message) { Write-Host "      $($_.ErrorDetails.Message)" -ForegroundColor DarkRed }
        return $null
    }
}

function Post($path, $body) {
    Invoke-RestMethod -Method Post -Uri "$base$path" -ContentType 'application/json' `
        -Body ($body | ConvertTo-Json -Depth 8)
}
function Get1($path) { Invoke-RestMethod -Method Get -Uri "$base$path" }

Write-Host "`n--- reads ---" -ForegroundColor Cyan
Step 'health' { Invoke-RestMethod "$($BaseUrl.TrimEnd('/'))/health" } | Out-Null
$overview = Step 'meta/overview (FILTER aggregates)' { Get1 '/meta/overview' }
Step 'meta/capabilities' { Get1 '/meta/capabilities' } | Out-Null
$subjects = Step 'subjects with counts' { Get1 '/subjects' }
Step 'warehouse rollups' { Get1 '/warehouse' } | Out-Null
Step 'jobs list' { Get1 '/jobs' } | Out-Null
Step 'questions list' { Get1 '/questions' } | Out-Null
Step 'papers list' { Get1 '/papers' } | Out-Null

Write-Host "seeded subjects: $($subjects.data.Count)"
$quant = $subjects.data | Where-Object { $_.code -eq 'quant' } | Select-Object -First 1
$reasoning = $subjects.data | Where-Object { $_.code -eq 'reasoning' } | Select-Object -First 1

Write-Host "`n--- exams and pattern ---" -ForegroundColor Cyan
$examA = Step 'create exam A' { Post '/exams' @{ name = 'Smoke Exam A'; code = 'smoke-a' } }
$examB = Step 'create exam B' { Post '/exams' @{ name = 'Smoke Exam B'; code = 'smoke-b' } }
$aId = $examA.data.id
$bId = $examB.data.id
Write-Host "exam A=$aId  exam B=$bId"

$pattern = Step 'create manual pattern' {
    Post "/exams/$aId/patterns" @{
        name = 'Smoke pattern'; duration_min = 60; marks_per_question = 2; negative_marks = 0.5
        option_count = 4; activate = $true
        sections = @(
            @{ subject_id = $quant.id; name = 'Quantitative Aptitude'; question_count = 2; order_index = 0 }
            @{ subject_id = $reasoning.id; name = 'Reasoning'; question_count = 1; order_index = 1 }
        )
    }
}
Write-Host "pattern total=$($pattern.data.total_questions) marks=$($pattern.data.total_marks) sections=$($pattern.data.sections.Count)"

Step 'list patterns' { Get1 "/exams/$aId/patterns" } | Out-Null
Step 'get exam A (preloads)' { Get1 "/exams/$aId" } | Out-Null

Write-Host "`n--- questions and links ---" -ForegroundColor Cyan
function NewQuestion($subjectId, $stem, $correct) {
    Post '/questions' @{
        subject_id = $subjectId; origin_exam_id = $aId; question_text = $stem
        difficulty = 'medium'; status = 'approved'; year = 2024
        options = @(
            @{ label = 'A'; text = "$stem opt A"; is_correct = ($correct -eq 'A') }
            @{ label = 'B'; text = "$stem opt B"; is_correct = ($correct -eq 'B') }
            @{ label = 'C'; text = "$stem opt C"; is_correct = ($correct -eq 'C') }
            @{ label = 'D'; text = "$stem opt D"; is_correct = ($correct -eq 'D') }
        )
    }
}
$q1 = Step 'create question 1 (quant)' { NewQuestion $quant.id 'What is 12 x 12?' 'B' }
$q2 = Step 'create question 2 (quant)' { NewQuestion $quant.id 'What is 15% of 200?' 'C' }
$q3 = Step 'create question 3 (reasoning)' { NewQuestion $reasoning.id 'Find the odd one out.' 'A' }

$tagged = Step 'questions carry exam tags' { Get1 "/questions?exam_id=$aId" }
$firstTags = $tagged.data[0].exam_tags
Write-Host "question tags: $(($firstTags | ForEach-Object { "$($_.name)/$($_.link_type)" }) -join ', ')"
if ($tagged.meta.total -ne 3) { Write-Host "      expected 3 questions for exam A, got $($tagged.meta.total)" -ForegroundColor Yellow; $fails++ }

Write-Host "`n--- association (borrowed links) ---" -ForegroundColor Cyan
Step 'associate B -> A' {
    Post "/exams/$bId/associations" @{ source_exam_id = $aId; similarity = 0.9; note = 'smoke test' }
} | Out-Null

$borrowed = Step 'exam B sees borrowed questions' { Get1 "/questions?exam_id=$bId" }
Write-Host "exam B reaches $($borrowed.meta.total) question(s)"
if ($borrowed.meta.total -ne 3) { Write-Host "      expected 3 borrowed, got $($borrowed.meta.total)" -ForegroundColor Yellow; $fails++ }
$bTag = $borrowed.data[0].exam_tags | Where-Object { $_.exam_id -eq $bId }
if ($bTag.link_type -ne 'associated') { Write-Host "      expected an 'associated' tag, got '$($bTag.link_type)'" -ForegroundColor Yellow; $fails++ }

Write-Host "`n--- paper generation ---" -ForegroundColor Cyan
$avail = Step 'paper availability' {
    Post "/exams/$aId/paper-availability" @{ require_answer = $true; include_borrowed = $true }
}
Write-Host "buildable=$($avail.data.buildable) requested=$($avail.data.total_requested) available=$($avail.data.total_available)"
foreach ($s in $avail.data.sections) {
    Write-Host ("  {0}: need {1}, own {2}, answered {3}, ok={4}" -f $s.section, $s.requested, $s.own, $s.answered, $s.sufficient)
}

$gen = Step 'queue paper generation' {
    Post "/exams/$aId/papers" @{ title = 'Smoke Paper'; type = 'balanced'; require_answer = $true }
}
$jobId = $gen.data.job.id
Write-Host "job id=$jobId"

$paperId = $null
for ($i = 0; $i -lt 30; $i++) {
    Start-Sleep -Milliseconds 700
    $job = Get1 "/jobs/$jobId"
    if ($job.data.job.status -eq 'completed') {
        $paperId = $job.data.job.result.paper_id
        Write-Host "PASS  worker completed the job" -ForegroundColor Green
        Write-Host "      questions=$($job.data.job.result.total_questions) marks=$($job.data.job.result.total_marks) seed=$($job.data.job.result.seed)"
        if ($job.data.job.result.warnings) {
            $job.data.job.result.warnings | ForEach-Object { Write-Host "      warning: $_" -ForegroundColor DarkYellow }
        }
        break
    }
    if ($job.data.job.status -in @('failed', 'cancelled')) {
        $fails++
        Write-Host "FAIL  job $($job.data.job.status): $($job.data.job.error)" -ForegroundColor Red
        break
    }
}
if (-not $paperId) { $fails++; Write-Host 'FAIL  no paper was produced' -ForegroundColor Red }

if ($paperId) {
    $paper = Step 'get paper with items' { Get1 "/papers/$paperId" }
    Write-Host "paper '$($paper.data.title)' has $($paper.data.items.Count) item(s)"
    $sectionNames = ($paper.data.items | ForEach-Object { $_.section_name } | Select-Object -Unique) -join ', '
    Write-Host "sections: $sectionNames"
    Step 'export paper as markdown' {
        $md = Invoke-WebRequest -Uri "$base/papers/$paperId/export" -UseBasicParsing
        if ($md.Content -notmatch 'Answer Key') { throw 'export is missing the answer key' }
        Write-Host "      export is $($md.Content.Length) bytes"
    } | Out-Null
}

Write-Host "`n--- bulk and review ---" -ForegroundColor Cyan
Step 'review a question' {
    Post "/questions/$($q3.data.id)/review" @{ verdict = 'approved'; is_factual = $true; set_difficulty = 'hard' }
} | Out-Null
Step 'bulk update' {
    Invoke-RestMethod -Method Patch -Uri "$base/questions" -ContentType 'application/json' `
        -Body (@{ question_ids = @($q1.data.id, $q2.data.id); difficulty = 'easy' } | ConvertTo-Json)
} | Out-Null
$after = Step 'warehouse reflects the changes' { Get1 '/warehouse' }
foreach ($s in $after.data.subjects) {
    Write-Host ("  {0}: total {1}, answered {2}, easy {3}, hard {4}" -f $s.name, $s.total, $s.answered, $s.easy, $s.hard)
}

Write-Host "`n--- error handling ---" -ForegroundColor Cyan
Step 'duplicate exam code is rejected with 409' {
    try {
        Post '/exams' @{ name = 'Dup'; code = 'smoke-a' } | Out-Null
        throw 'the duplicate was accepted'
    } catch {
        if ($_.Exception.Response.StatusCode.value__ -ne 409) { throw "expected 409, got $($_.Exception.Response.StatusCode.value__)" }
    }
} | Out-Null
Step 'unknown route returns 404' {
    try {
        Get1 '/nope' | Out-Null
        throw 'the unknown route was accepted'
    } catch {
        if ($_.Exception.Response.StatusCode.value__ -ne 404) { throw "expected 404, got $($_.Exception.Response.StatusCode.value__)" }
    }
} | Out-Null

Write-Host "`n--- cleanup ---" -ForegroundColor Cyan
if ($paperId) { Step 'delete paper' { Invoke-RestMethod -Method Delete -Uri "$base/papers/$paperId" } | Out-Null }
Step 'delete exam B' { Invoke-RestMethod -Method Delete -Uri "$base/exams/$bId" } | Out-Null
Step 'delete exam A' { Invoke-RestMethod -Method Delete -Uri "$base/exams/$aId" } | Out-Null
foreach ($q in @($q1, $q2, $q3)) {
    if ($q) { Step "delete question $($q.data.id)" { Invoke-RestMethod -Method Delete -Uri "$base/questions/$($q.data.id)?force=true" } | Out-Null }
}

Write-Host ''
if ($fails -eq 0) {
    Write-Host 'SMOKE TEST PASSED' -ForegroundColor Green
    exit 0
}
Write-Host "SMOKE TEST FAILED: $fails problem(s)" -ForegroundColor Red
exit 1
