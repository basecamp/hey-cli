package cmd

import "context"

// pageResult is one page a pageReader answered with. Cursor is whatever the next read
// needs to carry on from here, and is empty once the list has nothing more to give.
type pageResult[T any] struct {
	Items  []T
	Cursor string
	Total  int
}

// pageReader reads the page that begins at cursor.
type pageReader[T any] func(ctx context.Context, cursor string) (pageResult[T], error)

// pageRequest is how much of a list one command invocation wants. MaxPages counts the
// page the caller already read.
type pageRequest struct {
	Limit    int
	All      bool
	MaxPages int
	// FollowEmptyPages reads on past an empty page while it carries a cursor not read
	// before. Only for a list whose cursor only moves forward: HEY's Sent numbers its
	// pages and drops threads that are not active after taking each one, so a page can
	// be empty with more after it.
	FollowEmptyPages bool
	// StartCursor is the cursor the caller's first page was read from, so following
	// empty pages never reads it again.
	StartCursor string
}

// collectedPages is everything collectPages read, and where it stopped.
type collectedPages[T any] struct {
	Items     []T
	Cursor    string
	Total     int
	Read      int
	Truncated bool
}

// collectPages reads pages after the one the caller already has, until the request is
// satisfied, the list runs out, or the page cap is reached. An empty page ends the list
// whatever cursor came with it, unless the request follows empty pages.
func collectPages[T any](ctx context.Context, first pageResult[T], request pageRequest, read pageReader[T]) (collectedPages[T], error) {
	collected := collectedPages[T]{
		Items:  first.Items,
		Cursor: first.Cursor,
		Total:  max(first.Total, len(first.Items)),
		Read:   1,
	}
	requested := map[string]bool{"": true, request.StartCursor: true}

	for collected.Read < request.MaxPages && collected.Cursor != "" && wantsMorePages(len(collected.Items), request) {
		if request.FollowEmptyPages {
			// A cursor already read would go round again.
			if requested[collected.Cursor] {
				break
			}
			requested[collected.Cursor] = true
		}
		next, err := read(ctx, collected.Cursor)
		if err != nil {
			return collectedPages[T]{Read: collected.Read}, err
		}

		collected.Cursor = next.Cursor
		collected.Total = max(collected.Total, next.Total)
		collected.Read++
		if len(next.Items) == 0 {
			if request.FollowEmptyPages {
				continue
			}
			return collected, nil
		}
		collected.Items = append(collected.Items, next.Items...)
		collected.Total = max(collected.Total, len(collected.Items))
	}

	if request.FollowEmptyPages && requested[collected.Cursor] {
		// The list went round to a page already read: there is nothing more to give.
		collected.Cursor = ""
	}
	collected.Truncated = collected.Cursor != "" && collected.Read >= request.MaxPages
	return collected, nil
}

func wantsMorePages(collected int, request pageRequest) bool {
	return request.All || (request.Limit > 0 && collected < request.Limit)
}
