package tui

import (
	"regexp"
	"strings"
	"testing"
)

func TestBodyFindSharesItsBudgetAcrossMessages(t *testing.T) {
	find := bodyFind{query: "e"}
	message := findDocument{lines: []string{strings.Repeat("e", maxFindCandidates/2+1)}}
	find.scan(message, message, message)
	if !find.limited || len(find.matches) > maxFindCandidates {
		t.Fatalf("matches=%d limited=%v, want bounded results and a limit notice", len(find.matches), find.limited)
	}
	spans := 0
	for _, match := range find.matches {
		spans += len(match.parts)
	}
	if spans > maxFindSpans {
		t.Errorf("stored %d spans, limit is %d", spans, maxFindSpans)
	}
	if footer := find.footer(20); !strings.Contains(footer, "limited") {
		t.Errorf("short footer hides the limit notice: %q", footer)
	}
	find.query = "newsletter"
	find.scan(findDocument{lines: []string{"The monthly newsletter."}})
	if find.limited || len(find.matches) != 1 {
		t.Error("a new small search should clear the previous limit notice")
	}
}

func TestBodyFindRejectsAnIncompleteMatchAtTheSpanLimit(t *testing.T) {
	run := findRun{}
	for line, text := range []string{"monthly", "budget", "forecast"} {
		run.append(text, bodySpan{line: line, end: len(text)}, " ")
	}
	budget := findBudget{candidates: 10, spans: 2}
	matches := run.matches(regexp.MustCompile("monthly budget forecast"), &budget)
	if !budget.limited || len(matches) != 0 || budget.spans != 2 {
		t.Errorf("partial match retained: matches=%v budget=%+v", matches, budget)
	}
}

func TestBodyFindBoundsCandidateGenerationBeforeDeduplication(t *testing.T) {
	run := findRun{}
	text := strings.Repeat("e", 1<<20)
	run.append(text, bodySpan{end: len(text)}, "")
	budget := findBudget{candidates: 3, spans: 3}
	matches := run.matches(regexp.MustCompile("e"), &budget)
	if len(matches) != 3 || !budget.limited || budget.candidates != 0 || budget.spans != 0 {
		t.Errorf("dense candidate generation exceeded its budget: matches=%d budget=%+v", len(matches), budget)
	}
}

func BenchmarkBodyFindDenseScan(b *testing.B) {
	lines := strings.Split(strings.Repeat("The monthly newsletter covers our quarterly budget.\n", 20000), "\n")
	find := bodyFind{query: "e"}
	document := findDocument{lines: lines}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		find.scan(document)
	}
}
