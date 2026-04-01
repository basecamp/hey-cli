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
	return func(_ context.Context, _ string) (*generated.BoxShowResponse, error) {
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
	postings, nextURL, err := paginateBoxPostings(context.Background(), first, 0, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(postings) != 30 {
		t.Errorf("expected 30 postings, got %d", len(postings))
	}
	if nextURL == "" {
		t.Error("expected non-empty nextURL when next_history_url is present")
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

	postings, nextURL, err := paginateBoxPostings(context.Background(), first, 0, true, mockFetcher(pages))
	if err != nil {
		t.Fatal(err)
	}
	if len(postings) != 75 {
		t.Errorf("expected 75 postings, got %d", len(postings))
	}
	if nextURL != "" {
		t.Errorf("expected empty nextURL when last page has no next URL, got %q", nextURL)
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

	postings, nextURL, err := paginateBoxPostings(context.Background(), first, 50, false, mockFetcher(pages))
	if err != nil {
		t.Fatal(err)
	}
	if len(postings) != 60 {
		t.Errorf("expected 60 postings, got %d", len(postings))
	}
	if nextURL == "" {
		t.Error("expected non-empty nextURL when stopped by limit with more pages available")
	}
}

func TestPaginateBoxPostings_LimitSatisfiedByFirstPage(t *testing.T) {
	first := &generated.BoxShowResponse{
		Postings:       makePostings(30, 0),
		NextHistoryUrl: "https://app.hey.com/page2",
	}

	postings, nextURL, err := paginateBoxPostings(context.Background(), first, 10, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(postings) != 30 {
		t.Errorf("expected 30 postings (full first page), got %d", len(postings))
	}
	if nextURL == "" {
		t.Error("expected non-empty nextURL")
	}
}

func TestPaginateBoxPostings_NoNextURL(t *testing.T) {
	first := &generated.BoxShowResponse{
		Postings: makePostings(10, 0),
	}

	postings, nextURL, err := paginateBoxPostings(context.Background(), first, 0, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(postings) != 10 {
		t.Errorf("expected 10 postings, got %d", len(postings))
	}
	if nextURL != "" {
		t.Errorf("expected empty nextURL when no next URL, got %q", nextURL)
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

	postings, nextURL, err := paginateBoxPostings(context.Background(), first, 0, true, mockFetcher(pages))
	if err != nil {
		t.Fatal(err)
	}
	if len(postings) != 30 {
		t.Errorf("expected 30 postings, got %d", len(postings))
	}
	if nextURL != "" {
		t.Errorf("expected empty nextURL after empty page, got %q", nextURL)
	}
}

func TestPaginateBoxPostings_NilFetchReturnsError(t *testing.T) {
	first := &generated.BoxShowResponse{
		Postings:       makePostings(30, 0),
		NextHistoryUrl: "https://app.hey.com/page2",
	}
	_, _, err := paginateBoxPostings(context.Background(), first, 0, true, nil)
	if err == nil {
		t.Fatal("expected error when fetch is nil and pagination is required")
	}
}

func TestValidateSameOrigin(t *testing.T) {
	tests := []struct {
		name    string
		base    string
		target  string
		wantErr bool
	}{
		{"same origin", "https://app.hey.com", "https://app.hey.com/page2", false},
		{"different host", "https://app.hey.com", "https://evil.com/page2", true},
		{"different scheme", "https://app.hey.com", "http://app.hey.com/page2", true},
		{"with port match", "https://app.hey.com:443", "https://app.hey.com:443/page2", false},
		{"port mismatch", "https://app.hey.com:443", "https://app.hey.com:8080/page2", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSameOrigin(tt.base, tt.target)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateSameOrigin(%q, %q) error = %v, wantErr %v", tt.base, tt.target, err, tt.wantErr)
			}
		})
	}
}

func TestBoxTruncationNotice(t *testing.T) {
	tests := []struct {
		name    string
		shown   int
		fetched int
		hasMore bool
		all     bool
		want    string
	}{
		{"client truncated", 10, 30, false, false, "Showing 10 of 30 results. Use --all to see everything."},
		{"more pages available", 30, 30, true, false, "Showing 30 results. More available; use --all to fetch all."},
		{"all shown no more", 30, 30, false, false, ""},
		{"truncated with more", 10, 30, true, false, "Showing 10 of 30 results. Use --all to see everything."},
		{"all flag pagination capped", 30, 30, true, true, "Showing 30 results. Pagination limit reached; not all results could be fetched."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := boxTruncationNotice(tt.shown, tt.fetched, tt.hasMore, tt.all)
			if got != tt.want {
				t.Errorf("boxTruncationNotice(%d, %d, %v, %v) = %q, want %q", tt.shown, tt.fetched, tt.hasMore, tt.all, got, tt.want)
			}
		})
	}
}
