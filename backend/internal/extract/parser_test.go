package extract

import (
	"encoding/json"
	"testing"

	"mockcreator/internal/models"
)

func matcher() *SubjectMatcher {
	aliases := func(v ...string) []byte {
		b, _ := json.Marshal(v)
		return b
	}
	return NewSubjectMatcher([]models.Subject{
		{Base: models.Base{ID: 1}, Name: "Quantitative Aptitude", Code: "quant",
			Aliases: aliases("quantitative aptitude", "mathematics", "numerical ability")},
		{Base: models.Base{ID: 2}, Name: "Reasoning", Code: "reasoning",
			Aliases: aliases("reasoning", "general intelligence and reasoning", "logical reasoning")},
		{Base: models.Base{ID: 3}, Name: "General Knowledge", Code: "gk",
			Aliases: aliases("general awareness", "general knowledge")},
	})
}

// Style A: numbered questions, parenthesised lowercase options, bracketed key.
const docAlphaOptions = `
# Model Test Paper

## General Intelligence and Reasoning

1. Find the odd one out.
(a) Dog
(b) Cat
(c) Table
(d) Horse

2. Complete the series: 2, 4, 8, ...
(a) 10
(b) 16
(c) 12
(d) 14

## Quantitative Aptitude

3. What is 15% of 200?
(a) 25
(b) 30
(c) 35
(d) 40

4. Solve: 12 x 12
(a) 124
(b) 144
(c) 134
(d) 154

Answer Key

1. (c)  2. (b)  3. (b)  4. (b)
`

// Style B: the hard case. Options are numbered 1..4, exactly like questions.
const docNumericOptions = `
General Awareness

1. Who wrote the national anthem?
1. Tagore
2. Gandhi
3. Nehru
4. Bose

2. Capital of Japan?
1. Osaka
2. Tokyo
3. Kyoto
4. Nagoya

3. Largest planet?
1. Earth
2. Mars
3. Jupiter
4. Venus

Answer Key

| 1 | 1 | 2 | 2 | 3 | 3 |
|---|---|---|---|---|---|
`

// Style C: Q-prefixed markers, options packed inline, per-question solutions.
const docInlineOptions = `
Reasoning

Q1. Which number comes next: 3, 6, 9, ?
(A) 10 (B) 11 (C) 12 (D) 13
Ans. (C)
Sol. The series increases by three each time.

Q2. Pointing to a photo, X said "he is my brother". Who is he?
(A) Uncle (B) Brother (C) Cousin (D) Father
Ans. (B)
Sol. The statement is direct.
`

// Style D: comprehension directions shared across a range of questions.
const docDirections = `
English

Directions for questions 1 to 2: Read the passage and answer the questions.

The rainfall this season was unusually heavy across the region, and farmers
reported both record yields and unexpected losses.

1. What was unusual this season?
(a) Drought
(b) Rainfall
(c) Frost
(d) Wind

2. What did farmers report?
(a) Only losses
(b) Only yields
(c) Both yields and losses
(d) Nothing

Answers
1. (b)
2. (c)
`

func TestParseAlphaOptions(t *testing.T) {
	res := Parse(docAlphaOptions, Options{Subjects: matcher()})
	if len(res.Questions) != 4 {
		t.Fatalf("want 4 questions, got %d (style=%s opt=%s warnings=%v)",
			len(res.Questions), res.QuestionStyle, res.OptionStyle, res.Warnings)
	}
	if res.OptionCount != 4 {
		t.Errorf("want option count 4, got %d", res.OptionCount)
	}
	if !res.AnswerKeyFound {
		t.Error("answer key not found")
	}
	if res.AnsweredCount != 4 {
		t.Errorf("want 4 answered, got %d", res.AnsweredCount)
	}
	q1 := res.Questions[0]
	if q1.Text != "Find the odd one out." {
		t.Errorf("q1 text = %q", q1.Text)
	}
	if len(q1.Options) != 4 || q1.Options[2].Text != "Table" {
		t.Errorf("q1 options = %+v", q1.Options)
	}
	if !q1.Options[2].IsCorrect {
		t.Errorf("q1 correct option not marked: %+v", q1.Options)
	}
	if q1.Subject == nil || q1.Subject.Code != "reasoning" {
		t.Errorf("q1 subject = %+v", q1.Subject)
	}
	q3 := res.Questions[2]
	if q3.Subject == nil || q3.Subject.Code != "quant" {
		t.Errorf("q3 subject = %+v", q3.Subject)
	}
	if len(res.Sections) != 2 {
		t.Errorf("want 2 sections, got %d: %+v", len(res.Sections), res.Sections)
	}
}

func TestParseNumericOptions(t *testing.T) {
	res := Parse(docNumericOptions, Options{Subjects: matcher()})
	if len(res.Questions) != 3 {
		t.Fatalf("want 3 questions, got %d (style=%s opt=%s warnings=%v)",
			len(res.Questions), res.QuestionStyle, res.OptionStyle, res.Warnings)
	}
	for i, q := range res.Questions {
		if len(q.Options) != 4 {
			t.Errorf("q%d has %d options: %+v", i+1, len(q.Options), q.Options)
		}
	}
	if res.Questions[0].Text != "Who wrote the national anthem?" {
		t.Errorf("q1 text = %q", res.Questions[0].Text)
	}
	if res.Questions[1].Options[1].Text != "Tokyo" {
		t.Errorf("q2 options = %+v", res.Questions[1].Options)
	}
	if !res.AnswerKeyFound {
		t.Error("answer key table not read")
	}
	if !res.Questions[2].Options[2].IsCorrect {
		t.Errorf("q3 answer wrong: %+v", res.Questions[2].Options)
	}
	if res.Questions[0].Subject == nil || res.Questions[0].Subject.Code != "gk" {
		t.Errorf("subject = %+v", res.Questions[0].Subject)
	}
}

func TestParseInlineOptions(t *testing.T) {
	res := Parse(docInlineOptions, Options{Subjects: matcher()})
	if len(res.Questions) != 2 {
		t.Fatalf("want 2 questions, got %d (style=%s opt=%s warnings=%v)",
			len(res.Questions), res.QuestionStyle, res.OptionStyle, res.Warnings)
	}
	q1 := res.Questions[0]
	if len(q1.Options) != 4 {
		t.Fatalf("q1 options = %+v", q1.Options)
	}
	if q1.Options[2].Text != "12" || !q1.Options[2].IsCorrect {
		t.Errorf("q1 options = %+v", q1.Options)
	}
	if q1.Explanation == "" {
		t.Errorf("q1 explanation missing")
	}
	if res.AnsweredCount != 2 {
		t.Errorf("want 2 answered, got %d", res.AnsweredCount)
	}
}

func TestParseDirections(t *testing.T) {
	res := Parse(docDirections, Options{Subjects: matcher()})
	if len(res.Questions) != 2 {
		t.Fatalf("want 2 questions, got %d (warnings=%v)", len(res.Questions), res.Warnings)
	}
	if res.Questions[0].Context == "" {
		t.Errorf("shared passage not attached: %+v", res.Questions[0])
	}
	if res.Questions[0].Type != models.TypeComprehension {
		t.Errorf("want comprehension type, got %s", res.Questions[0].Type)
	}
	if !res.Questions[1].Options[2].IsCorrect {
		t.Errorf("q2 answer wrong: %+v", res.Questions[1].Options)
	}
}

func TestContentHashOrderIndependent(t *testing.T) {
	a := ContentHash("What is 2+2?", []string{"3", "4", "5", "6"})
	b := ContentHash("what is 2 + 2 ?", []string{"6", "5", "4", "3"})
	if a != b {
		t.Errorf("hash should ignore punctuation and option order:\n%s\n%s", a, b)
	}
	c := ContentHash("What is 3+3?", []string{"3", "4", "5", "6"})
	if a == c {
		t.Error("different stems must hash differently")
	}
}
