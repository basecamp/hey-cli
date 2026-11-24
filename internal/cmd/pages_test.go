package cmd

import (
	"context"
	"fmt"
	"strconv"
	"testing"
)

// numberedPages serves `count` pages of one item each and then keeps answering empty
// pages, the way a page-numbered list does.
func numberedPages(count int, read *[]string) pageReader[int] {
	return func(_ context.Context, cursor string) (pageResult[int], error) {
		*read = append(*read, cursor)
		page, err := strconv.Atoi(cursor)
		if err != nil {
			return pageResult[int]{}, err
		}
		result := pageResult[int]{Cursor: strconv.Itoa(page + 1)}
		if page <= count {
			result.Items = []int{page}
		}
		return result, nil
	}
}

func TestCollectPagesReadsNothingMoreThanAsked(t *testing.T) {
	var read []string
	first := pageResult[int]{Items: []int{1, 2}, Cursor: "cursor-2", Total: 9}

	collected, err := collectPages(t.Context(), first, pageRequest{MaxPages: 10}, numberedPages(5, &read))
	if err != nil {
		t.Fatal(err)
	}
	if len(read) != 0 {
		t.Errorf("read %v, want nothing beyond the page it was given", read)
	}
	if collected.Read != 1 || collected.Cursor != "cursor-2" || collected.Total != 9 || collected.Truncated {
		t.Errorf("collected = %+v", collected)
	}
}

func TestCollectPagesGrowsUntilTheLimitIsMet(t *testing.T) {
	var read []string
	first := pageResult[int]{Items: []int{0}, Cursor: "1"}

	collected, err := collectPages(t.Context(), first, pageRequest{Limit: 3, MaxPages: 10}, numberedPages(9, &read))
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(read); got != "[1 2]" {
		t.Errorf("read %s, want the two pages the limit needed", got)
	}
	if len(collected.Items) != 3 || collected.Read != 3 || collected.Cursor != "3" {
		t.Errorf("collected = %+v", collected)
	}
}

// An empty page ends the list whatever cursor came with it, and counts as a page read.
func TestCollectPagesStopsAtAnEmptyPage(t *testing.T) {
	var read []string
	first := pageResult[int]{Items: []int{0}, Cursor: "1"}

	collected, err := collectPages(t.Context(), first, pageRequest{All: true, MaxPages: 10}, numberedPages(2, &read))
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(read); got != "[1 2 3]" {
		t.Errorf("read %s", got)
	}
	if len(collected.Items) != 3 || collected.Read != 4 || collected.Truncated {
		t.Errorf("collected = %+v", collected)
	}
	if collected.Cursor != "4" {
		t.Errorf("cursor = %q, want the one the empty page carried", collected.Cursor)
	}
}

func TestCollectPagesReportsTheCap(t *testing.T) {
	var read []string
	first := pageResult[int]{Items: []int{0}, Cursor: "1"}

	collected, err := collectPages(t.Context(), first, pageRequest{All: true, MaxPages: 4}, numberedPages(100, &read))
	if err != nil {
		t.Fatal(err)
	}
	if len(read) != 3 || collected.Read != 4 || !collected.Truncated {
		t.Errorf("read %v, collected = %+v", read, collected)
	}
}

// The list runs out on its own when a page answers without a cursor.
func TestCollectPagesStopsWhenTheCursorRunsOut(t *testing.T) {
	first := pageResult[int]{Items: []int{1}, Cursor: "cursor-2"}
	collected, err := collectPages(t.Context(), first, pageRequest{All: true, MaxPages: 10},
		func(context.Context, string) (pageResult[int], error) {
			return pageResult[int]{Items: []int{2}}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(collected.Items) != 2 || collected.Cursor != "" || collected.Read != 2 || collected.Truncated {
		t.Errorf("collected = %+v", collected)
	}
}

// HEY's own count wins over what has been read, and what has been read wins over a count
// HEY did not give.
func TestCollectPagesKeepsTheLargestTotal(t *testing.T) {
	first := pageResult[int]{Items: []int{1, 2}}
	collected, err := collectPages(t.Context(), first, pageRequest{All: true, MaxPages: 10},
		func(context.Context, string) (pageResult[int], error) {
			return pageResult[int]{}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if collected.Total != 2 {
		t.Errorf("total = %d, want what was read", collected.Total)
	}

	counted, err := collectPages(t.Context(), pageResult[int]{Items: []int{1}, Cursor: "1", Total: 40},
		pageRequest{All: true, MaxPages: 10},
		func(context.Context, string) (pageResult[int], error) {
			return pageResult[int]{Items: []int{2}, Total: 41}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if counted.Total != 41 {
		t.Errorf("total = %d, want HEY's count", counted.Total)
	}
}

func TestCollectPagesReturnsAFailedRead(t *testing.T) {
	want := fmt.Errorf("page 2 failed")
	first := pageResult[int]{Items: []int{1}, Cursor: "1"}

	collected, err := collectPages(t.Context(), first, pageRequest{All: true, MaxPages: 10},
		func(context.Context, string) (pageResult[int], error) {
			return pageResult[int]{}, want
		})
	if err != want {
		t.Fatalf("error = %v, want %v", err, want)
	}
	if collected.Items != nil {
		t.Errorf("collected = %+v, want nothing", collected)
	}
}

// A list that follows empty pages reads on past one while it carries a new cursor, and
// ends when the cursor does.
func TestCollectPagesFollowsEmptyPagesWhenAsked(t *testing.T) {
	var read []string
	pages := map[string]pageResult[int]{
		"2": {Cursor: "3"},
		"3": {Items: []int{3}, Cursor: "4"},
		"4": {},
	}
	first := pageResult[int]{Items: []int{1}, Cursor: "2"}

	collected, err := collectPages(t.Context(), first, pageRequest{All: true, MaxPages: 10, FollowEmptyPages: true},
		func(_ context.Context, cursor string) (pageResult[int], error) {
			read = append(read, cursor)
			return pages[cursor], nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(read); got != "[2 3 4]" {
		t.Errorf("read %s, want every page up to the one without a cursor", got)
	}
	if fmt.Sprint(collected.Items) != "[1 3]" || collected.Read != 4 || collected.Cursor != "" || collected.Truncated {
		t.Errorf("collected = %+v", collected)
	}
}

// Following empty pages never reads a cursor twice, so an empty page whose cursor points
// back at one already read ends the list rather than going round it.
func TestCollectPagesFollowingEmptyPagesStopsOnARepeatedCursor(t *testing.T) {
	var read []string
	pages := map[string]pageResult[int]{
		"2": {Items: []int{2}, Cursor: "3"},
		"3": {Cursor: "2"},
	}
	first := pageResult[int]{Items: []int{1}, Cursor: "2"}

	collected, err := collectPages(t.Context(), first, pageRequest{All: true, MaxPages: 10, FollowEmptyPages: true},
		func(_ context.Context, cursor string) (pageResult[int], error) {
			read = append(read, cursor)
			return pages[cursor], nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(read); got != "[2 3]" {
		t.Errorf("read %s, want each cursor once", got)
	}
	if fmt.Sprint(collected.Items) != "[1 2]" {
		t.Errorf("items = %v, want each page's items once", collected.Items)
	}
}

// Following empty pages still stops at the page cap.
func TestCollectPagesFollowingEmptyPagesStopsAtTheCap(t *testing.T) {
	var read []string
	first := pageResult[int]{Items: []int{0}, Cursor: "1"}

	collected, err := collectPages(t.Context(), first, pageRequest{All: true, MaxPages: 4, FollowEmptyPages: true}, numberedPages(0, &read))
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(read); got != "[1 2 3]" {
		t.Errorf("read %s", got)
	}
	if collected.Read != 4 || !collected.Truncated || collected.Cursor != "4" {
		t.Errorf("collected = %+v, want the cap reached with the next cursor kept", collected)
	}
}
