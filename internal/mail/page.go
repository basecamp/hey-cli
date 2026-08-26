package mail

import (
	"context"
	"fmt"
	"net/url"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
)

// Page is one page of a source's postings. Cursor is whatever the next read needs to
// carry on where this page stopped — a geared_pagination cursor for a label or a
// collection, HEY's own next_history_url for a box — and is empty at the end of the
// list. Total is HEY's count of everything in the source, zero where it does not say.
type Page struct {
	Postings []generated.Posting
	Cursor   string
	Total    int
}

// ReadPage reads the page of a source that begins at cursor. An empty cursor reads the
// first page.
func ReadPage(ctx context.Context, client *hey.Client, source Source, cursor string) (Page, error) {
	switch source.Kind {
	case KindBox:
		return readBoxPage(ctx, client, source, cursor)
	case KindFolder:
		return readFolderPage(ctx, client, source, cursor)
	case KindCollection:
		return readCollectionPage(ctx, client, source, cursor)
	default:
		return Page{}, fmt.Errorf("mail: source %d has no readable kind %q", source.ID, source.Kind)
	}
}

// readBoxPage follows the box's own next_history_url, which HEY builds against the route
// the box is served by. That route matters: the Feed, the Paper Trail and Bubbled Up
// order and page their postings differently from /boxes/{id}, so a cursor from one is
// meaningless to the other. The URL itself is never fetched — only the page cursor
// inside it, handed back through the typed operation for this box's kind.
func readBoxPage(ctx context.Context, client *hey.Client, source Source, cursor string) (Page, error) {
	page := ""
	if cursor != "" {
		historyPage, err := historyPageCursor(cursor)
		if err != nil {
			return Page{}, err
		}
		page = historyPage
	}

	box, err := readBox(ctx, client, source, page)
	if err != nil {
		return Page{}, err
	}
	if box == nil {
		return Page{}, fmt.Errorf("mail: box %d answered no page", source.ID)
	}
	return Page{Postings: box.Postings, Cursor: box.NextHistoryUrl}, nil
}

func readBox(ctx context.Context, client *hey.Client, source Source, page string) (*generated.BoxShowResponse, error) {
	var cursor *string
	if page != "" {
		cursor = &page
	}

	switch source.BoxKind {
	case hey.BoxKindImbox:
		return client.Boxes().GetImbox(ctx, &generated.GetImboxParams{Page: cursor})
	case hey.BoxKindFeed:
		return client.Boxes().GetFeedbox(ctx, &generated.GetFeedboxParams{Page: cursor})
	case hey.BoxKindTrail:
		return client.Boxes().GetTrailbox(ctx, &generated.GetTrailboxParams{Page: cursor})
	case hey.BoxKindSetAside:
		return client.Boxes().GetAsidebox(ctx, &generated.GetAsideboxParams{Page: cursor})
	case hey.BoxKindLater:
		return client.Boxes().GetLaterbox(ctx, &generated.GetLaterboxParams{Page: cursor})
	case hey.BoxKindBubbleUp:
		return client.Boxes().GetBubblebox(ctx, &generated.GetBubbleboxParams{Page: cursor})
	default:
		return client.Boxes().Get(ctx, source.ID, &generated.GetBoxParams{Page: cursor})
	}
}

// ReadSeenPage reads a page of the Imbox's Previously Seen postings, which HEY serves on
// their own route ordered by when they were seen — the Imbox's own pages order seen
// postings last, which is why the box cannot stand in for this. There is no Source
// parameter: the route is account-scoped and names the Imbox itself. An empty cursor
// reads the first page.
func ReadSeenPage(ctx context.Context, client *hey.Client, cursor string) (Page, error) {
	var page *string
	if cursor != "" {
		historyPage, err := historyPageCursor(cursor)
		if err != nil {
			return Page{}, err
		}
		page = &historyPage
	}

	box, err := client.Boxes().GetImboxSeen(ctx, &generated.GetImboxSeenParams{Page: page})
	if err != nil {
		return Page{}, err
	}
	if box == nil {
		return Page{}, fmt.Errorf("mail: the Imbox's seen postings answered no page")
	}
	return Page{Postings: box.Postings, Cursor: box.NextHistoryUrl}, nil
}

func historyPageCursor(nextHistoryURL string) (string, error) {
	parsed, err := url.Parse(nextHistoryURL)
	if err != nil {
		return "", fmt.Errorf("mail: unreadable next_history_url %q: %w", nextHistoryURL, err)
	}
	page := parsed.Query().Get("page")
	if page == "" {
		return "", fmt.Errorf("mail: next_history_url %q carries no page cursor", nextHistoryURL)
	}
	return page, nil
}

func readFolderPage(ctx context.Context, client *hey.Client, source Source, cursor string) (Page, error) {
	var params *generated.GetFolderParams
	if cursor != "" {
		params = &generated.GetFolderParams{Page: &cursor}
	}

	result, err := client.Folders().GetPage(ctx, source.ID, params)
	if err != nil {
		return Page{}, err
	}
	if result == nil || result.Folder == nil {
		return Page{}, fmt.Errorf("mail: label %d answered no page at cursor %q", source.ID, cursor)
	}
	return Page{Postings: result.Folder.Postings, Cursor: result.NextPage, Total: result.TotalCount}, nil
}

func readCollectionPage(ctx context.Context, client *hey.Client, source Source, cursor string) (Page, error) {
	var params *generated.GetCollectionParams
	if cursor != "" {
		params = &generated.GetCollectionParams{Page: &cursor}
	}

	result, err := client.Collections().GetPage(ctx, source.ID, params)
	if err != nil {
		return Page{}, err
	}
	if result == nil || result.Collection == nil {
		return Page{}, fmt.Errorf("mail: collection %d answered no page at cursor %q", source.ID, cursor)
	}
	return Page{Postings: result.Collection.Postings, Cursor: result.NextPage, Total: result.TotalCount}, nil
}
