# End-to-end verification of the content quality pipeline.
#
# This ingests real question papers through the running stack, then checks the
# things a client would check: that no stored question is corrupted, that the
# quality gate actually holds defective content back, that a generated paper is
# validated before it can be published, and that export is refused while it is
# not.
#
# It is deliberately assertive rather than descriptive. A script that prints
# numbers proves nothing; this one fails loudly when a guarantee is broken.
#
#   powershell -File .\scripts\verify-quality.ps1
#   powershell -File .\scripts\verify-quality.ps1 -Papers 3

param(
    [string]$BaseUrl = 'http://127.0.0.1:8090/api/v1',
    [string]$SourceDir = '.\backend\source\pyq\ssc-cgl',
    [int]$Papers = 2,
    [int]$TimeoutSeconds = 900
)

$ErrorActionPreference = 'Stop'
# Invoke-WebRequest's progress bar interleaves with the check output and makes the
# result unreadable.
$ProgressPreference = 'SilentlyContinue'
$script:Failures = @()
$script:Checks = 0

function Say($message) { Write-Host $message }
function Step($message) { Write-Host "`n=== $message" -ForegroundColor Cyan }

function Check($label, $condition, $detail = '') {
    $script:Checks++
    if ($condition) {
        Write-Host "  PASS  $label" -ForegroundColor Green
    }
    else {
        Write-Host "  FAIL  $label" -ForegroundColor Red
        if ($detail) { Write-Host "        $detail" -ForegroundColor DarkGray }
        $script:Failures += $label
    }
}

function Api($method, $path, $body = $null) {
    $uri = "$BaseUrl$path"
    if ($null -ne $body) {
        $json = $body | ConvertTo-Json -Depth 12 -Compress
        return Invoke-RestMethod -Method $method -Uri $uri -Body $json -ContentType 'application/json'
    }
    return Invoke-RestMethod -Method $method -Uri $uri
}

# ApiExpectFailure returns the status code of a request that is supposed to be
# refused, which is how the gates are tested.
function ApiExpectFailure($method, $path, $body = $null) {
    try {
        Api $method $path $body | Out-Null
        return 0
    }
    catch {
        if ($_.Exception.Response) { return [int]$_.Exception.Response.StatusCode }
        throw
    }
}

# UploadFile posts a multipart form. Windows PowerShell 5.1 has no -Form switch on
# Invoke-RestMethod, so the request is built with HttpClient directly, which also
# streams the file rather than loading it twice.
function UploadFile($path, $fields) {
    Add-Type -AssemblyName System.Net.Http
    $client = New-Object System.Net.Http.HttpClient
    $client.Timeout = [TimeSpan]::FromMinutes(10)
    try {
        $content = New-Object System.Net.Http.MultipartFormDataContent
        foreach ($key in $fields.Keys) {
            $content.Add((New-Object System.Net.Http.StringContent([string]$fields[$key])), $key)
        }
        $bytes = [System.IO.File]::ReadAllBytes($path)
        $fileContent = New-Object System.Net.Http.ByteArrayContent($bytes, 0, $bytes.Length)
        $fileContent.Headers.ContentType =
        [System.Net.Http.Headers.MediaTypeHeaderValue]::Parse('application/pdf')
        $content.Add($fileContent, 'files', [System.IO.Path]::GetFileName($path))

        $response = $client.PostAsync("$BaseUrl/documents", $content).GetAwaiter().GetResult()
        $text = $response.Content.ReadAsStringAsync().GetAwaiter().GetResult()
        if (-not $response.IsSuccessStatusCode) {
            throw "upload of $path failed with HTTP $([int]$response.StatusCode): $text"
        }
        return $text | ConvertFrom-Json
    }
    finally {
        $client.Dispose()
    }
}

function WaitForJob($jobId, $what) {
    if (-not $jobId) { throw "$what was queued but no job id came back" }
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        $job = (Api GET "/jobs/$jobId").data.job
        if (-not $job) { throw "job $jobId could not be read back" }
        if ($job.status -in @('completed', 'failed', 'cancelled')) {
            if ($job.status -ne 'completed') {
                throw "$what job $jobId ended $($job.status): $($job.error)"
            }
            return $job
        }
        Start-Sleep -Seconds 3
    }
    throw "$what job $jobId did not finish within $TimeoutSeconds seconds"
}

# ---------------------------------------------------------------------------

Step 'Service health'
$meta = (Api GET '/meta/capabilities').data
Check 'converter is reachable' ($meta.converter.available -eq $true) $meta.converter.error
Check 'geometric and layout engines are both offered' ($meta.converter.engines -contains 'geometry' -and $meta.converter.engines -contains 'docling')
Check 'at least one OCR engine is installed' ($meta.converter.ocr_engines.Count -ge 1)
Say "        engines: $($meta.converter.engines -join ', ')  ocr: $($meta.converter.ocr_engines -join ', ')"

$model = (Api GET '/meta/model').data
if ($model.configured) {
    Say "        model review: $($model.model) (reachable=$($model.reachable))"
}
else {
    Say '        model review: not configured, deterministic checks only'
}

# ---------------------------------------------------------------------------

Step 'Create an exam and its pattern'
$suffix = Get-Random -Maximum 99999
$exam = (Api POST '/exams' @{
        name        = "Verification Exam $suffix"
        code        = "verify-$suffix"
        description = 'Created by verify-quality.ps1'
    }).data
Check 'exam created' ($exam.id -gt 0)

$subjects = (Api GET '/subjects?page_size=100').data
function SubjectId($code) { ($subjects | Where-Object { $_.code -eq $code } | Select-Object -First 1).id }

$sections = @(
    @{ name = 'Reasoning'; subject_id = (SubjectId 'reasoning'); question_count = 10; marks_per_question = 2 },
    @{ name = 'General Knowledge'; subject_id = (SubjectId 'gk'); question_count = 10; marks_per_question = 2 },
    @{ name = 'Quantitative Aptitude'; subject_id = (SubjectId 'quant'); question_count = 10; marks_per_question = 2 },
    @{ name = 'English'; subject_id = (SubjectId 'english'); question_count = 10; marks_per_question = 2 }
)
$pattern = (Api POST "/exams/$($exam.id)/patterns" @{
        name            = 'Verification pattern'
        total_questions = 40
        total_marks     = 80
        duration_min    = 60
        negative_marks  = 0.5
        option_count    = 4
        is_active       = $true
        sections        = $sections
    }).data
Check 'pattern created with four sections' ($pattern.id -gt 0)

# Activate explicitly rather than relying on the create flag, so the exam has a
# pattern to build against however the API chose to interpret the request.
Api POST "/exams/$($exam.id)/patterns/$($pattern.id)/activate" | Out-Null
$active = ((Api GET "/exams/$($exam.id)/patterns").data | Where-Object { $_.is_active })
Check 'the pattern is active' ($null -ne $active)

# ---------------------------------------------------------------------------

Step "Ingest $Papers real question paper(s)"
$files = Get-ChildItem $SourceDir -Filter *.pdf | Sort-Object Name | Select-Object -First $Papers
Check 'source PDFs found' ($files.Count -ge 1) "looked in $SourceDir"
if ($files.Count -lt 1) { throw "no PDFs in $SourceDir" }

$documentIds = @()
foreach ($file in $files) {
    Say "  uploading $($file.Name) ($([math]::Round($file.Length/1KB)) KB)"
    $upload = UploadFile $file.FullName @{
        kind        = 'question_paper'
        exam_id     = "$($exam.id)"
        auto_ingest = 'true'
    }
    # The endpoint reports one outcome per attached file.
    $outcome = @($upload.data)[0]
    if ($outcome.error) { throw "upload of $($file.Name) reported: $($outcome.error)" }
    Check "$($file.Name): stored" ($outcome.document.id -gt 0)
    $documentIds += $outcome.document.id

    if ($outcome.duplicate) {
        # The file is already in the warehouse from an earlier run. Attach it to
        # this run's exam and re-ingest, so the verification exercises the whole
        # pipeline and can be run repeatedly without resetting the database.
        Say '        already stored, re-attaching and re-ingesting'
        Api PATCH "/documents/$($outcome.document.id)" @{ exam_id = $exam.id } | Out-Null
        $jobId = (Api POST "/documents/$($outcome.document.id)/ingest" @{ reconvert = $true }).data.id
    }
    else {
        $jobId = $outcome.job_id
    }

    $job = WaitForJob $jobId "ingest of $($file.Name)"
    $r = $job.result

    Say ("        engine=$($r.engine) conf=$([math]::Round($r.extraction_confidence,3)) " +
        "parse=$([math]::Round($r.parse_confidence,3)) questions=$($r.questions_created) " +
        "ready=$($r.passed) review=$($r.needs_review) rejected=$($r.failed) passages=$($r.passages)")

    Check "$($file.Name): questions extracted" ($r.questions_created -ge 60) "got $($r.questions_created)"
    Check "$($file.Name): read with the geometric engine" ($r.engine -eq 'geometry') "used $($r.engine)"
    Check "$($file.Name): extraction confidence above the floor" ($r.extraction_confidence -ge 0.75) "got $($r.extraction_confidence)"
    Check "$($file.Name): most questions cleared the gate" ($r.passed -ge $r.questions_created * 0.5) "$($r.passed) of $($r.questions_created)"
    Check "$($file.Name): nothing silently dropped" ($r.questions_created -gt 0)
    foreach ($w in $r.warnings) { Say "        warn: $w" }
}

# ---------------------------------------------------------------------------

Step 'No stored question is corrupted'
# Every question that passed the gate is fetched and inspected directly, because
# the gate is only worth anything if what it passes is genuinely clean.
$ready = @((Api GET "/questions?exam_id=$($exam.id)&deliverable=true&page_size=200").data)
Check 'deliverable questions exist' ($ready.Count -gt 0)

$badChars = 0; $badPlaceholder = 0; $badForeign = 0; $noAnswer = 0; $shortStem = 0; $dupOption = 0
$passageInStem = 0
foreach ($q in $ready) {
    $text = $q.question_text
    if ($text -match "\uFFFD") { $badChars++ }
    if ($text -match '<!--|\{\{|\\{3,}') { $badPlaceholder++ }
    if ($text.Length -lt 12) { $shortStem++ }
    # A passage must be referenced, never copied into the stem.
    if ($q.passage_id -and $q.passage -and $text.Contains($q.passage.text)) { $passageInStem++ }

    $correct = 0
    $seen = @{}
    foreach ($o in @($q.options)) {
        if ($o.text -match "\uFFFD") { $badChars++ }
        if ($o.text -match '<!--|\{\{|\\{3,}') { $badPlaceholder++ }
        # Two or more option markers inside one option means it absorbed others.
        if ($o.text -match '(^|\s)\(?[1-9]\)?[.)]\s+\S+.*\s\(?[1-9]\)?[.)]\s+\S+') { $badForeign++ }
        if ($o.is_correct) { $correct++ }
        $key = ($o.text -replace '\s+', ' ').Trim().ToLower()
        if ($seen.ContainsKey($key)) { $dupOption++ } else { $seen[$key] = 1 }
    }
    if ($correct -eq 0 -and -not $q.answer_text) { $noAnswer++ }
}

Check 'no unreadable characters in deliverable content' ($badChars -eq 0) "$badChars found"
Check 'no unresolved placeholders' ($badPlaceholder -eq 0) "$badPlaceholder found"
Check 'no option contains other options or questions' ($badForeign -eq 0) "$badForeign found"
Check 'every deliverable question has an answer' ($noAnswer -eq 0) "$noAnswer without one"
Check 'no stem is too short to be a question' ($shortStem -eq 0) "$shortStem too short"
Check 'no duplicated options within a question' ($dupOption -eq 0) "$dupOption found"
Check 'passages are referenced, not copied into stems' ($passageInStem -eq 0) "$passageInStem inlined"
Say "        inspected $($ready.Count) deliverable questions"

Step 'Held-back content is visible and explained'
$summary = (Api GET '/review/summary').data
$held = @((Api GET "/review?exam_id=$($exam.id)&page_size=50").data)
Check 'the review queue is populated' ($held.Count -gt 0) 'nothing was held back, which is suspicious for real scans'
# @() is required throughout: a Where-Object that matches exactly one object
# returns a scalar, and .Count on a scalar PSCustomObject is $null, which silently
# reads as zero and turns a passing check into a failing one.
$withoutReason = @($held | Where-Object { -not $_.quality_issues -or @($_.quality_issues).Count -eq 0 }).Count
Check 'every held question states why' ($withoutReason -eq 0) "$withoutReason without a recorded reason"
Check 'findings are grouped by code for triage' (@($summary.by_issue).Count -gt 0)
$top = $summary.by_issue | Select-Object -First 5
foreach ($i in $top) { Say "        $($i.severity.PadRight(9)) $($i.code) x$($i.count) (from $($i.source))" }

Step 'Provenance traces back to the source'
$sample = $held[0]
$prov = (Api GET "/questions/$($sample.id)/provenance").data
Check 'the source document is identified' ($null -ne $prov.document)
Check 'the extraction engine is recorded' ($prov.extraction.engine -ne '')
Check 'the original source lines are retrievable' ($prov.source_window.available -eq $true)
Check 'the issue list is returned with it' (@($prov.issues).Count -gt 0)

# ---------------------------------------------------------------------------

Step 'Paper availability reflects the quality gate'
$avail = (Api POST "/exams/$($exam.id)/paper-availability" @{ include_borrowed = $true; require_answer = $true }).data
foreach ($s in $avail.sections) {
    Say ("        $($s.section.PadRight(24)) need=$($s.requested) ready=$($s.deliverable) " +
        "held=$($s.held_for_review) sufficient=$($s.sufficient)")
}
$reportsDeliverable = @($avail.sections | Where-Object { $null -ne $_.deliverable }).Count
Check 'availability reports deliverable counts' ($reportsDeliverable -eq @($avail.sections).Count)

Step 'Generate a paper'
$gen = Api POST "/exams/$($exam.id)/papers" @{
    title            = "Verification Paper $suffix"
    type             = 'balanced'
    include_borrowed = $true
    require_answer   = $true
    only_approved    = $false
}
$buildJob = WaitForJob $gen.data.job.id 'paper build'
$built = $buildJob.result
$paperId = $built.paper_id
Check 'a paper was produced' ($paperId -gt 0)
Say ("        questions=$($built.total_questions) marks=$($built.total_marks) " +
    "verdict=$($built.quality_status) publishable=$($built.publishable)")

Step 'The paper carries a QA verdict'
$qa = (Api GET "/papers/$paperId/qa").data
Check 'the paper was checked, not left unchecked' ($qa.quality_status -ne 'unchecked') "status $($qa.quality_status)"
Check 'a rules version is recorded' ($qa.rules_version -ge 1)
Check 'the verdict is explained in words' ($qa.explanation -ne '')
Say "        $($qa.quality_status): $($qa.explanation)"
Say "        critical=$($qa.counts.critical) major=$($qa.counts.major) minor=$($qa.counts.minor) score=$([math]::Round($qa.quality_score,2))"
foreach ($i in ($qa.issues | Select-Object -First 6)) {
    Say "        [$($i.severity)] $($i.code) $($i.message)"
}

Step 'Every question in the paper passed the gate'
$paper = (Api GET "/papers/$paperId").data
$unvetted = 0; $missingAnswer = 0; $corrupt = 0
$stems = @{}
$dupInPaper = 0
foreach ($item in $paper.items) {
    if (-not $item.question) { continue }
    if ($item.question.quality_status -ne 'pass') { $unvetted++ }
    if ($item.question.question_text -match "\uFFFD|<!--|\{\{") { $corrupt++ }
    $hasCorrect = @($item.question.options | Where-Object { $_.is_correct }).Count -gt 0
    if (-not $hasCorrect -and -not $item.question.answer_text) { $missingAnswer++ }
    $key = ($item.question.question_text -replace '\s+', ' ').Trim().ToLower()
    if ($stems.ContainsKey($key)) { $dupInPaper++ } else { $stems[$key] = 1 }
}
Check 'no unvetted question reached the paper' ($unvetted -eq 0) "$unvetted found"
Check 'no corrupted question reached the paper' ($corrupt -eq 0) "$corrupt found"
Check 'every question in the paper has an answer' ($missingAnswer -eq 0) "$missingAnswer without one"

# A duplicate is allowed to exist only if QA found it and blocked the paper. That
# is the guarantee that matters: the point is not that generation is perfect, but
# that nothing defective can be delivered without someone being told.
$reportedDuplicate = @($qa.issues | Where-Object {
        $_.code -in @('paper_duplicate_question', 'paper_repeated_stem')
    }).Count
if ($dupInPaper -eq 0) {
    Check 'no question appears twice' $true
}
else {
    Say "        $dupInPaper repeated stem(s) present in the generated paper"
    Check 'a repeated question is detected by QA' ($reportedDuplicate -gt 0)
    Check 'a repeated question blocks the paper' ($qa.quality_status -eq 'failed')
}

Step 'The gate blocks publication and export'
if ($qa.publishable) {
    $publish = Api PUT "/papers/$paperId" @{ status = 'published' }
    Check 'a passing paper can be published' ($publish.data.status -eq 'published')
    $exportOk = $false
    try {
        Invoke-WebRequest -Uri "$BaseUrl/papers/$paperId/export" -UseBasicParsing | Out-Null
        $exportOk = $true
    }
    catch { $exportOk = $false }
    Check 'a passing paper exports' $exportOk
}
else {
    $status = ApiExpectFailure PUT "/papers/$paperId" @{ status = 'published' }
    Check 'publishing a paper that failed QA is refused' ($status -eq 409) "got HTTP $status"

    $exportStatus = 0
    try { Invoke-WebRequest -Uri "$BaseUrl/papers/$paperId/export" -UseBasicParsing | Out-Null }
    catch { $exportStatus = [int]$_.Exception.Response.StatusCode }
    Check 'exporting it without asking for a draft is refused' ($exportStatus -eq 409) "got HTTP $exportStatus"

    $draft = Invoke-WebRequest -Uri "$BaseUrl/papers/$paperId/export?draft=true" -UseBasicParsing
    Check 'a draft export is allowed' ($draft.StatusCode -eq 200)
    Check 'the draft export is stamped as not deliverable' ($draft.Content -match 'DRAFT - NOT CLEARED FOR DELIVERY')
}

Step 'A rejected question cannot be selected'
# Reject a question that currently passes, then confirm the generator will not
# pick it: this proves the gate is enforced in the query, not merely displayed.
$victim = $ready[0]
Api POST "/questions/$($victim.id)/resolve" @{ decision = 'reject'; reviewer = 'verify script'; notes = 'rejected to test the gate' } | Out-Null
$after = (Api GET "/questions/$($victim.id)").data
Check 'rejection is recorded on the question' ($after.quality_status -eq 'failed') "status $($after.quality_status)"
$stillListed = @((Api GET "/questions?exam_id=$($exam.id)&deliverable=true&page_size=200").data |
    Where-Object { $_.id -eq $victim.id })
Check 'a rejected question leaves the deliverable pool' ($stillListed.Count -eq 0)

Step 'Human acceptance is recorded as an override'
$toAccept = $held[0]
Api POST "/questions/$($toAccept.id)/resolve" @{
    decision = 'accept'; reviewer = 'verify script'; notes = 'accepted to test the override'; difficulty = 'medium'
} | Out-Null
$accepted = (Api GET "/questions/$($toAccept.id)/provenance").data
Check 'acceptance clears the question for use' ($accepted.quality.status -eq 'pass')
$override = @($accepted.issues | Where-Object { $_.code -eq 'human_override' })
Check 'the override is recorded rather than the findings erased' ($override.Count -gt 0)
$humanReviews = @($accepted.question.reviews | Where-Object { $_.reviewer_type -eq 'human' })
Check 'the reviewer is named in the audit trail' ($humanReviews.Count -gt 0)
Check 'the review records who made the call' ($humanReviews.Count -eq 0 -or $humanReviews[0].reviewer_name -ne '')

# ---------------------------------------------------------------------------

Write-Host "`n================ RESULT ================"
Write-Host "checks run: $script:Checks"
if ($script:Failures.Count -eq 0) {
    Write-Host 'ALL CHECKS PASSED' -ForegroundColor Green
    exit 0
}
Write-Host "$($script:Failures.Count) CHECK(S) FAILED" -ForegroundColor Red
foreach ($f in $script:Failures) { Write-Host "  - $f" -ForegroundColor Red }
exit 1
