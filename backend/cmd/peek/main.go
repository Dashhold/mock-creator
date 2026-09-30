// Command peek parses a converted document and reports what the extractor made
// of it, without touching the database.
//
// This is the tool for answering "why did this paper come out wrong?". Point it
// at the markdown the converter produced and it prints the questions, the
// options, the answers it found and every issue it recorded, so a bad paper can
// be diagnosed from the text rather than from the database after the fact.
//
//	go run ./cmd/peek -file converted.md
//	go run ./cmd/peek -file converted.md -q 68 -v
//	go run ./cmd/peek -file converted.md -issues
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"mockcreator/internal/extract"
	"mockcreator/internal/models"
	"mockcreator/internal/quality"
)

func main() {
	var (
		file       = flag.String("file", "", "converted markdown file to parse (required)")
		only       = flag.Int("q", 0, "show only this question number")
		issuesOnly = flag.Bool("issues", false, "show only questions with a parse issue")
		verbose    = flag.Bool("v", false, "show context, explanation and provenance")
		asJSON     = flag.Bool("json", false, "emit the full parse result as JSON")
		maxOptions = flag.Int("max-options", 0, "cap options per question (0 = auto)")
		runQuality = flag.Bool("quality", false, "run the deterministic quality checks and show the verdicts")
		subjects   = flag.String("subjects", "", "JSON file of subjects with aliases, so section detection behaves as it does in the pipeline")
	)
	flag.Parse()

	if *file == "" {
		fmt.Fprintln(os.Stderr, "peek: -file is required")
		flag.Usage()
		os.Exit(2)
	}

	raw, err := os.ReadFile(*file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "peek: %v\n", err)
		os.Exit(1)
	}

	opts := extract.Options{MaxOptions: *maxOptions}

	// Section detection changes the parse, so a faithful reproduction of what the
	// pipeline did needs the same subject aliases the pipeline had. Without this
	// flag peek parses with no subjects at all, which is a different run.
	if *subjects != "" {
		loaded, err := loadSubjects(*subjects)
		if err != nil {
			fmt.Fprintf(os.Stderr, "peek: %v\n", err)
			os.Exit(1)
		}
		opts.Subjects = extract.NewSubjectMatcher(loaded)
		fmt.Printf("subjects       %d loaded from %s\n", len(loaded), *subjects)
	}

	result := extract.Parse(string(raw), opts)

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(result); err != nil {
			fmt.Fprintf(os.Stderr, "peek: %v\n", err)
			os.Exit(1)
		}
		return
	}

	printSummary(result)
	printIssueTally(result)
	printSections(result)
	if *runQuality {
		printQuality(result, *only, *verbose)
		return
	}
	printQuestions(result, *only, *issuesOnly, *verbose)
}

// printQuality runs the deterministic content checks over every parsed question
// and reports the gate each one lands in. Duplicate detection is skipped because
// it needs the warehouse; everything else is exercised exactly as the pipeline
// runs it.
func printQuality(r extract.ParseResult, only int, verbose bool) {
	opts := quality.Options{ExpectedOptions: r.OptionCount}

	tally := map[models.QualityStatus]int{}
	codes := map[string]int{}
	worst := make([]string, 0, 16)

	for _, q := range r.Questions {
		issues := quality.ValidateQuestion(toQualityInput(q), opts)
		status := issues.Worst()
		tally[status]++
		for _, issue := range issues {
			codes[string(issue.Severity)+" "+issue.Code]++
		}
		if only > 0 && q.Number != only {
			continue
		}
		if status == models.QualityPass && only == 0 && !verbose {
			continue
		}
		worst = append(worst, formatVerdict(q, status, issues))
	}

	fmt.Println("quality gate")
	for _, status := range models.AllQualityStatuses() {
		fmt.Printf("  %-10s %d\n", status, tally[status])
	}
	fmt.Println()

	if len(codes) > 0 {
		keys := make([]string, 0, len(codes))
		for k := range codes {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return codes[keys[i]] > codes[keys[j]] })
		fmt.Println("findings")
		for _, k := range keys {
			fmt.Printf("  %-44s %d\n", k, codes[k])
		}
		fmt.Println()
	}

	for _, block := range worst {
		fmt.Print(block)
	}
}

func formatVerdict(q extract.ParsedQuestion, status models.QualityStatus, issues models.QualityIssues) string {
	var b strings.Builder
	fmt.Fprintf(&b, "--- q%d  %s  score=%.2f\n", q.Number, strings.ToUpper(string(status)),
		quality.Score(issues, q.Confidence))
	fmt.Fprintf(&b, "    stem: %s\n", truncate(oneLine(q.Text), 150))
	for _, issue := range issues {
		fmt.Fprintf(&b, "    [%s] %-26s %s", issue.Severity, issue.Code, issue.Message)
		if issue.Field != "" {
			fmt.Fprintf(&b, " (%s)", issue.Field)
		}
		fmt.Fprintln(&b)
		if issue.Evidence != "" {
			fmt.Fprintf(&b, "        > %s\n", truncate(issue.Evidence, 130))
		}
	}
	b.WriteString("\n")
	return b.String()
}

// toQualityInput adapts a parsed question to the quality engine's input.
func toQualityInput(q extract.ParsedQuestion) quality.Input {
	options := make([]quality.OptionInput, 0, len(q.Options))
	for _, o := range q.Options {
		options = append(options, quality.OptionInput{Label: o.Label, Text: o.Text, IsCorrect: o.IsCorrect})
	}
	subject := ""
	if q.Subject != nil {
		subject = q.Subject.Code
	}
	return quality.Input{
		Type:                 q.Type,
		Stem:                 q.Text,
		Options:              options,
		AnswerText:           q.AnswerText,
		Explanation:          q.Explanation,
		PassageText:          q.Context,
		TrailingText:         q.Trailing,
		SubjectCode:          subject,
		ExtractIssues:        q.Issues,
		ExtractionConfidence: q.Confidence,
	}
}

func printSummary(r extract.ParseResult) {
	fmt.Printf("questions      %d (%d answered, %d flagged, %d dropped)\n",
		len(r.Questions), r.AnsweredCount, r.FlaggedCount, r.DroppedCount)
	fmt.Printf("confidence     %.3f\n", r.Confidence)
	fmt.Printf("question style %s\n", r.QuestionStyle)
	fmt.Printf("option style   %s (%d per question, e.g. %q)\n",
		r.OptionStyle, r.OptionCount, r.OptionExample)
	fmt.Printf("answer key     found=%t  explanations=%t\n", r.AnswerKeyFound, r.ExplanationsFound)
	fmt.Printf("pages          %d\n", r.PageCount)
	for _, w := range r.Warnings {
		fmt.Printf("  ! %s\n", w)
	}
	fmt.Println()
}

func printIssueTally(r extract.ParseResult) {
	tally := map[string]int{}
	for _, q := range r.Questions {
		for _, issue := range q.Issues {
			tally[issue.Code]++
		}
	}
	if len(tally) == 0 {
		fmt.Println("issues         none")
		fmt.Println()
		return
	}
	codes := make([]string, 0, len(tally))
	for code := range tally {
		codes = append(codes, code)
	}
	sort.Slice(codes, func(i, j int) bool { return tally[codes[i]] > tally[codes[j]] })
	fmt.Println("issues")
	for _, code := range codes {
		fmt.Printf("  %-22s %d\n", code, tally[code])
	}
	fmt.Println()
}

func printSections(r extract.ParseResult) {
	if len(r.Sections) == 0 {
		return
	}
	fmt.Println("sections")
	for _, s := range r.Sections {
		subject := "-"
		if s.Subject != nil {
			subject = s.Subject.Code
		}
		fmt.Printf("  %-44s subject=%-12s q%d-%d (%d)\n",
			truncate(s.Label, 44), subject, s.FirstQuestion, s.LastQuestion, s.QuestionCount)
	}
	fmt.Println()
}

func printQuestions(r extract.ParseResult, only int, issuesOnly, verbose bool) {
	for _, q := range r.Questions {
		if only > 0 && q.Number != only {
			continue
		}
		if issuesOnly && len(q.Issues) == 0 {
			continue
		}

		answer := "none"
		for _, o := range q.Options {
			if o.IsCorrect {
				answer = o.Label
				break
			}
		}
		if answer == "none" && q.AnswerText != "" {
			answer = q.AnswerText
		}

		fmt.Printf("--- q%d  page=%d  type=%s  conf=%.2f  answer=%s  subject=%s\n",
			q.Number, q.PageNo, q.Type, q.Confidence, answer, subjectCode(q))
		for _, issue := range q.Issues {
			fmt.Printf("    ISSUE %s: %s\n", issue.Code, issue.Detail)
		}
		fmt.Printf("    stem: %s\n", oneLine(q.Text))
		for _, o := range q.Options {
			mark := " "
			if o.IsCorrect {
				mark = "*"
			}
			fmt.Printf("     (%s)%s %s\n", o.Label, mark, oneLine(o.Text))
		}
		if q.Trailing != "" {
			fmt.Printf("    trailing: %s\n", oneLine(q.Trailing))
		}
		if verbose {
			if q.Context != "" {
				fmt.Printf("    context[%s]: %s\n", shortKey(q.ContextKey), oneLine(truncate(q.Context, 160)))
			}
			if q.Explanation != "" {
				fmt.Printf("    explanation: %s\n", oneLine(truncate(q.Explanation, 200)))
			}
			fmt.Printf("    source lines %d-%d\n", q.SourceFirstLine, q.SourceLastLine)
		}
		fmt.Println()
	}
}

func subjectCode(q extract.ParsedQuestion) string {
	if q.Subject == nil {
		return "-"
	}
	return q.Subject.Code
}

// loadSubjects reads the subject catalogue from a JSON array, which is what
// `psql -c "select json_agg(...) from subjects"` produces.
func loadSubjects(path string) ([]models.Subject, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// Windows tooling writes a byte-order mark, which is not valid JSON.
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))

	var subjects []models.Subject
	if err := json.Unmarshal(raw, &subjects); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return subjects, nil
}

func shortKey(key string) string {
	if len(key) <= 8 {
		return key
	}
	return key[:8]
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-1]) + "\u2026"
}
