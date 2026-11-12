package tui

// These bound candidates before deduplication as well as their display spans.
// One budget is shared by every body and every representation of a search.
const (
	maxFindCandidates = 2000
	maxFindSpans      = 8000
)

type findBudget struct {
	candidates int
	spans      int
	limited    bool
}

func newFindBudget() findBudget {
	return findBudget{candidates: maxFindCandidates, spans: maxFindSpans}
}
