package pattern

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

// scheme is the timing and marking rules read off a document's front matter.
type scheme struct {
	durationMin      int
	totalMarks       float64
	marksPerQuestion float64
	negativeMarks    float64
}

// Most papers state their own rules on the first page: "Time Allowed: 60
// Minutes", "Maximum Marks: 200", "0.50 marks will be deducted for each wrong
// answer". Reading them beats making the user retype what the document says.
var (
	timeLineRe = regexp.MustCompile(`(?i)\b(?:time|duration)\b`)
	hoursRe    = regexp.MustCompile(`(?i)([0-9]{1,2})\s*(?:hours?|hrs?\.?|h\b)`)
	minutesRe  = regexp.MustCompile(`(?i)([0-9]{1,3})\s*(?:minutes?|mins?\.?|m\b)`)
	bareNumRe  = regexp.MustCompile(`([0-9]{1,3})`)

	totalMarksRe = regexp.MustCompile(`(?i)(?:maximum|max\.?|total|full)\s*marks?\s*[:\-–—=]?\s*([0-9]{1,5}(?:\.[0-9]+)?)`)

	marksPerQRe = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(?:each|every)\s+(?:question|correct\s+answer|right\s+answer|correct\s+response)` +
			`[^0-9]{0,40}([0-9]+(?:\.[0-9]+)?)\s*marks?`),
		regexp.MustCompile(`(?i)([0-9]+(?:\.[0-9]+)?)\s*marks?\s*(?:will\s*be\s*awarded\s*)?(?:for|per)\s*` +
			`(?:each\s*)?(?:correct\s*)?(?:answer|question|response)`),
		regexp.MustCompile(`(?i)(?:question|answer)\s*(?:carries|carry|is\s*of|worth)\s*([0-9]+(?:\.[0-9]+)?)\s*marks?`),
	}

	negativeLineRe = regexp.MustCompile(`(?i)\b(?:negative\s*mark(?:ing|s)?|penalt(?:y|ies)|deduct(?:ed|ion|s)?)\b`)
	noNegativeRe   = regexp.MustCompile(`(?i)\b(?:no|nil|without|there\s+is\s+no)\b[^.]{0,30}\b(?:negative\s*mark(?:ing|s)?|penalt(?:y|ies)|deduction)\b`)
	fractionRe     = regexp.MustCompile(`([0-9]{1,2})\s*/\s*([0-9]{1,2})`)
	decimalRe      = regexp.MustCompile(`([0-9]*\.?[0-9]+)`)
)

// headerLines returns the front matter of a document, where the rules live.
func headerLines(markdown string, limit int) []string {
	if markdown == "" {
		return nil
	}
	out := make([]string, 0, limit)
	for _, line := range strings.Split(markdown, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out = append(out, line)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// detectScheme reads the marking rules from every document and keeps the value
// the documents most agree on, so a single misprint cannot set the pattern.
func detectScheme(observations []Observation) scheme {
	var (
		durations []int
		totals    []float64
		perQ      []float64
		negatives []float64
	)

	for _, o := range observations {
		lines := headerLines(o.Markdown, 120)
		if len(lines) == 0 {
			continue
		}
		if d := detectDuration(lines); d > 0 {
			durations = append(durations, d)
		}
		if t := detectTotalMarks(lines); t > 0 {
			totals = append(totals, t)
		}
		if p := detectMarksPerQuestion(lines); p > 0 {
			perQ = append(perQ, p)
		}
		if n, found := detectNegative(lines); found {
			negatives = append(negatives, n)
		}
	}

	return scheme{
		durationMin:      modeInt(durations),
		totalMarks:       modeFloat(totals),
		marksPerQuestion: modeFloat(perQ),
		negativeMarks:    modeFloat(negatives),
	}
}

// detectDuration reads a time allowance in minutes.
func detectDuration(lines []string) int {
	for _, line := range lines {
		if !timeLineRe.MatchString(line) {
			continue
		}
		minutes := 0
		if m := hoursRe.FindStringSubmatch(line); m != nil {
			if h, err := strconv.Atoi(m[1]); err == nil && h > 0 && h <= 12 {
				minutes += h * 60
			}
		}
		if m := minutesRe.FindStringSubmatch(line); m != nil {
			if mm, err := strconv.Atoi(m[1]); err == nil && mm > 0 && mm < 600 {
				// A single-digit "2 m" next to "2 hours" is the hour repeated, not
				// two minutes; only take minutes that make sense on their own.
				if minutes == 0 || mm >= 5 {
					minutes += mm
				}
			}
		}
		if minutes == 0 {
			// "Time: 60" with the unit implied.
			if m := bareNumRe.FindStringSubmatch(line); m != nil {
				if n, err := strconv.Atoi(m[1]); err == nil && n >= 10 && n <= 600 {
					minutes = n
				}
			}
		}
		if minutes > 0 && minutes <= 600 {
			return minutes
		}
	}
	return 0
}

func detectTotalMarks(lines []string) float64 {
	for _, line := range lines {
		if m := totalMarksRe.FindStringSubmatch(line); m != nil {
			if v, err := strconv.ParseFloat(m[1], 64); err == nil && v > 0 && v <= 10000 {
				return v
			}
		}
	}
	return 0
}

func detectMarksPerQuestion(lines []string) float64 {
	for _, line := range lines {
		for _, re := range marksPerQRe {
			m := re.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			if v, err := strconv.ParseFloat(m[1], 64); err == nil && v > 0 && v <= 100 {
				return v
			}
		}
	}
	return 0
}

// detectNegative reads the penalty per wrong answer. It reports found=true for
// an explicit "no negative marking" so that zero is recorded as a decision
// rather than as a missing value.
func detectNegative(lines []string) (float64, bool) {
	for _, line := range lines {
		if noNegativeRe.MatchString(line) {
			return 0, true
		}
	}
	for _, line := range lines {
		if !negativeLineRe.MatchString(line) {
			continue
		}
		// Fractions are the common form: "1/4 mark", "1/3rd of the marks".
		if m := fractionRe.FindStringSubmatch(line); m != nil {
			num, err1 := strconv.ParseFloat(m[1], 64)
			den, err2 := strconv.ParseFloat(m[2], 64)
			if err1 == nil && err2 == nil && den > 0 {
				if v := num / den; v > 0 && v <= 10 {
					return round2(v), true
				}
			}
		}
		for _, m := range decimalRe.FindAllStringSubmatch(line, -1) {
			v, err := strconv.ParseFloat(m[1], 64)
			if err != nil || v <= 0 || v > 10 {
				continue
			}
			return round2(v), true
		}
	}
	return 0, false
}

func modeFloat(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	counts := map[float64]int{}
	best, bestCount := 0.0, 0
	for _, v := range values {
		key := math.Round(v*100) / 100
		counts[key]++
		if counts[key] > bestCount || (counts[key] == bestCount && key > best) {
			best, bestCount = key, counts[key]
		}
	}
	return best
}
