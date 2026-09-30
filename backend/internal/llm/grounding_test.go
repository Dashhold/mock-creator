package llm

import "testing"

// The grounding check is the mechanism that stops a model inventing exam content.
// These cases are the contract: cosmetic differences must pass, invented words
// must fail. If this test is ever relaxed, a model can write questions.
func TestGroundingAcceptsCosmeticDifferences(t *testing.T) {
	source := NewSource(
		"Select the letter-cluster from among the given options that can replace the question mark (?) in the following series: WZWT, WVOH, WRGV, WNYJ, ?",
		"W J Q X",
		"Arvind started a business by investing \u20b980,000.",
	)

	cases := []struct {
		name     string
		fragment string
	}{
		{"verbatim", "Select the letter-cluster from among the given options"},
		{"different case", "SELECT THE LETTER-CLUSTER FROM AMONG THE GIVEN OPTIONS"},
		{"hyphen joined differently", "letter cluster from among the given options"},
		{"punctuation dropped", "in the following series WZWT WVOH WRGV WNYJ"},
		{"extra spacing", "Select   the  letter-cluster   from among"},
		{"short option text", "W J Q X"},
		{"currency stripped", "investing 80,000"},
		{"skips one stray word", "Select the letter-cluster from the given options"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !source.Contains(tc.fragment) {
				t.Errorf("fragment should be grounded but was rejected: %q", tc.fragment)
			}
		})
	}
}

func TestGroundingRejectsInventedContent(t *testing.T) {
	source := NewSource(
		"Select the letter-cluster from among the given options that can replace the question mark in the following series",
		"W J Q X",
	)

	cases := []struct {
		name     string
		fragment string
	}{
		{"invented sentence", "The correct approach is to add two to every letter in sequence"},
		{"plausible but absent", "Select the number-cluster from among the printed alternatives shown"},
		{"completed missing text", "in the following series: MIN, NJM, OKL, PLK"},
		{"hallucinated explanation", "Because each consonant advances by three places the answer follows"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if source.Contains(tc.fragment) {
				t.Errorf("fragment is not in the source but was accepted: %q", tc.fragment)
			}
		})
	}
}

func TestCheckReportsEveryUngroundedFragment(t *testing.T) {
	source := NewSource("The capital of France is Paris and the river is the Seine")

	result := source.Check(
		"the capital of France is Paris",
		"the capital of Germany is Berlin entirely elsewhere",
		"",
		"another invented claim about something completely different here",
	)

	if result.Verified {
		t.Fatal("expected verification to fail")
	}
	if len(result.Ungrounded) != 2 {
		t.Fatalf("expected 2 ungrounded fragments, got %d: %v", len(result.Ungrounded), result.Ungrounded)
	}
}

func TestEmptySourceGroundsNothing(t *testing.T) {
	source := NewSource()
	if source.Contains("any claim at all about anything") {
		t.Error("an empty source must not ground a multi-word claim")
	}
	if !source.Contains("") {
		t.Error("an empty fragment is vacuously grounded")
	}
}

// extractJSONObject has to cope with the several ways models wrap their replies,
// because a model that adds a sentence of preamble is not a reason to skip a
// quality check.
func TestExtractJSONObject(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"bare", `{"ok":true}`, `{"ok":true}`},
		{"fenced", "```json\n{\"ok\":true}\n```", `{"ok":true}`},
		{"fenced no language", "```\n{\"ok\":true}\n```", `{"ok":true}`},
		{"prose prefix", `Here is the result: {"ok":true}`, `{"ok":true}`},
		{"nested", `{"a":{"b":1},"c":2}`, `{"a":{"b":1},"c":2}`},
		{"brace inside string", `{"msg":"a } brace","ok":true}`, `{"msg":"a } brace","ok":true}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := extractJSONObject(tc.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}

	for _, bad := range []string{"", "no json here", `{"unterminated": `} {
		if _, err := extractJSONObject(bad); err == nil {
			t.Errorf("expected an error for %q", bad)
		}
	}
}

// A model must not be able to downgrade a defect that is critical by definition.
func TestSeverityFloor(t *testing.T) {
	if got := severityFrom("minor", CodeNotAnswerable); got != "critical" {
		t.Errorf("an unanswerable question must stay critical, got %q", got)
	}
	if got := severityFrom("minor", CodeOptionsNotParallel); got != "minor" {
		t.Errorf("a style defect should honour the model's grading, got %q", got)
	}
	if got := severityFrom("", CodeAmbiguous); got != "major" {
		t.Errorf("an unspecified severity should default to major, got %q", got)
	}
}
