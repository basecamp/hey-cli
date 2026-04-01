package cmd

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	"github.com/spf13/cobra"
)

func TestValidateBoxArgs(t *testing.T) {
	command := &cobra.Command{Use: "box"}
	command.SetArgs([]string{})

	tests := []struct {
		name        string
		args        []string
		wantErr     bool
		errContains string
	}{
		{
			name:        "missing arg",
			args:        nil,
			wantErr:     true,
			errContains: "Usage:",
		},
		{
			name:    "one arg",
			args:    []string{"imbox"},
			wantErr: false,
		},
		{
			name:        "too many args",
			args:        []string{"imbox", "extra"},
			wantErr:     true,
			errContains: "expected 1 mailbox argument",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateBoxArgs(command, tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Fatalf("error = %q, want substring %q", err.Error(), tt.errContains)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// makePostings creates n test postings with sequential IDs starting at offset+1.
func makePostings(n, offset int) []generated.Posting {
	postings := make([]generated.Posting, n)
	for i := range postings {
		postings[i] = generated.Posting{Id: int64(offset + i + 1)}
	}
	return postings
}

// mockFetcher returns a pageFetcher that serves a predefined sequence of pages.
// Each call returns the next page; after all pages are exhausted it returns an error.
func mockFetcher(pages []generated.BoxShowResponse) pageFetcher {
	idx := 0
	return func(_ context.Context, url string) (*generated.BoxShowResponse, error) {
		if idx >= len(pages) {
			return nil, fmt.Errorf("unexpected fetch beyond %d pages", len(pages))
		}
		page := pages[idx]
		idx++
		return &page, nil
	}
}

func TestPaginateBoxPostings_NoFlagsSinglePage(t *testing.T) {
	first := &generated.BoxShowResponse{
		Postings:       makePostings(30, 0),
		NextHistoryUrl: "https://app.hey.com/page2",
	}
	postings, hasMore, err := paginateBoxPostings(context.Background(), first, 0, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(postings) != 30 {
		t.Errorf("expected 30 postings, got %d", len(postings))
	}
	if !hasMore {
		t.Error("expected hasMore=true when next_history_url is present")
	}
}

func TestPaginateBoxPostings_AllFlag(t *testing.T) {
	first := &generated.BoxShowResponse{
		Postings:       makePostings(30, 0),
		NextHistoryUrl: "https://app.hey.com/page2",
	}
	pages := []generated.BoxShowResponse{
		{Postings: makePostings(30, 30), NextHistoryUrl: "https://app.hey.com/page3"},
		{Postings: makePostings(15, 60)},
	}

	postings, hasMore, err := paginateBoxPostings(context.Background(), first, 0, true, mockFetcher(pages))
	if err != nil {
		t.Fatal(err)
	}
	if len(postings) != 75 {
		t.Errorf("expected 75 postings, got %d", len(postings))
	}
	if hasMore {
		t.Error("expected hasMore=false when last page has no next URL")
	}
}

func TestPaginateBoxPostings_LimitExceedsFirstPage(t *testing.T) {
	first := &generated.BoxShowResponse{
		Postings:       makePostings(30, 0),
		NextHistoryUrl: "https://app.hey.com/page2",
	}
	pages := []generated.BoxShowResponse{
		{Postings: makePostings(30, 30), NextHistoryUrl: "https://app.hey.com/page3"},
	}

	postings, hasMore, err := paginateBoxPostings(context.Background(), first, 50, false, mockFetcher(pages))
	if err != nil {
		t.Fatal(err)
	}
	if len(postings) != 60 {
		t.Errorf("expected 60 postings, got %d", len(postings))
	}
	if !hasMore {
		t.Error("expected hasMore=true when stopped by limit with more pages available")
	}
}

func TestPaginateBoxPostings_LimitSatisfiedByFirstPage(t *testing.T) {
	first := &generated.BoxShowResponse{
		Postings:       makePostings(30, 0),
		NextHistoryUrl: "https://app.hey.com/page2",
	}

	postings, hasMore, err := paginateBoxPostings(context.Background(), first, 10, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(postings) != 30 {
		t.Errorf("expected 30 postings (full first page), got %d", len(postings))
	}
	if !hasMore {
		t.Error("expected hasMore=true")
	}
}

func TestPaginateBoxPostings_NoNextURL(t *testing.T) {
	first := &generated.BoxShowResponse{
		Postings: makePostings(10, 0),
	}

	postings, hasMore, err := paginateBoxPostings(context.Background(), first, 0, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(postings) != 10 {
		t.Errorf("expected 10 postings, got %d", len(postings))
	}
	if hasMore {
		t.Error("expected hasMore=false when no next URL")
	}
}

func TestPaginateBoxPostings_EmptyPageStopsPagination(t *testing.T) {
	first := &generated.BoxShowResponse{
		Postings:       makePostings(30, 0),
		NextHistoryUrl: "https://app.hey.com/page2",
	}
	pages := []generated.BoxShowResponse{
		{Postings: nil},
	}

	postings, hasMore, err := paginateBoxPostings(context.Background(), first, 0, true, mockFetcher(pages))
	if err != nil {
		t.Fatal(err)
	}
	if len(postings) != 30 {
		t.Errorf("expected 30 postings, got %d", len(postings))
	}
	if hasMore {
		t.Error("expected hasMore=false after empty page")
	}
}

func TestBoxTruncationNotice(t *testing.T) {
	tests := []struct {
		name    string
		shown   int
		fetched int
		hasMore bool
		want    string
	}{
		{"client truncated", 10, 30, false, "Showing 10 of 30 results. Use --all to see everything."},
		{"more pages available", 30, 30, true, "Showing 30 results. More available; use --all to fetch all."},
		{"all shown no more", 30, 30, false, ""},
		{"truncated with more", 10, 30, true, "Showing 10 of 30 results. Use --all to see everything."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := boxTruncationNotice(tt.shown, tt.fetched, tt.hasMore)
			if got != tt.want {
				t.Errorf("boxTruncationNotice(%d, %d, %v) = %q, want %q", tt.shown, tt.fetched, tt.hasMore, got, tt.want)
			}
		})
	}
}
