package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/basecamp/hey-cli/internal/threadload"
)

// --markdown writes one document: a heading per entry, the body as Markdown, a rule
// between entries, the metadata escaped for a Markdown reader.
func TestPrintThreadMarkdownShapesOneDocument(t *testing.T) {
	var out bytes.Buffer
	err := writeThreadMarkdown(&out, 7, []threadEntry{
		{ID: 11, CreatedAt: "2026-04-12T09:30", Creator: threadContact{Name: "Rick *Sanchez*"}, Body: "See the [plan](https://example.com/plan).", BodyState: "hydrated"},
		{ID: 12, CreatedAt: "2026-04-13T09:30", Creator: threadContact{Name: "Morty Smith"}, Summary: "A _preview_", BodyState: "bodyless"},
		{ID: 13, CreatedAt: "2026-04-14T09:30", Creator: threadContact{Name: "Summer Smith"}, Summary: "Never shown", BodyState: string(threadload.StateFailed)},
		{ID: 14, CreatedAt: "2026-04-15T09:30", Creator: threadContact{Name: "Beth Smith"}, Summary: "Never shown", BodyState: string(threadload.StateHydrated)},
	}, "1 of 3 bodies could not be read (failed)")
	if err != nil {
		t.Fatal(err)
	}

	want := "# Thread 7\n" +
		"\n## From: Rick \\*Sanchez\\* — 2026-04-12T09:30 (#11)\n\nSee the [plan](https://example.com/plan).\n\n---\n" +
		"\n## From: Morty Smith — 2026-04-13T09:30 (#12)\n\nA \\_preview\\_\n\n---\n" +
		"\n## From: Summer Smith — 2026-04-14T09:30 (#13)\n\n*(body not read: failed)*\n\n---\n" +
		"\n## From: Beth Smith — 2026-04-15T09:30 (#14)\n\n*(empty body)*\n\n---\n" +
		"\n**Notice:** 1 of 3 bodies could not be read \\(failed\\)\n"
	if out.String() != want {
		t.Errorf("document =\n%s\nwant\n%s", out.String(), want)
	}
}

func TestThreadMarkdownReportsAWriteError(t *testing.T) {
	err := writeThreadMarkdown(failingWriter{}, 7, []threadEntry{{ID: 1, Body: "x", BodyState: "hydrated"}}, "")
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Errorf("error = %v, want the write failure", err)
	}
}

func TestThreadsMarkdownThroughTheCommand(t *testing.T) {
	server, _ := partialThreadServer(t, [][]int64{{12, 11}})
	stdoutTerminal(t, false)

	stdout, _, err := runCLIRaw(t, server, "--markdown", "threads", "7")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"# Thread 7", "## From: Rick Sanchez — ", "(#11)", "(#12)", "body 11", "body 12", "\n---\n"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, want %q in it", stdout, want)
		}
	}
	if strings.Contains(stdout, "<p>") || strings.Contains(stdout, "Notice") {
		t.Errorf("stdout = %q, want Markdown without HTML and without a notice for a complete thread", stdout)
	}
}

// A link in the body survives into --json as Markdown a reader can follow.
func TestThreadsJSONKeepsLinks(t *testing.T) {
	server, _ := threadEntriesServer(t, [][]int64{{11}}, map[int64]string{11: `<p>See the <a href="https://example.com/plan?a=1&amp;b=2">plan</a>.</p>`})
	stdoutTerminal(t, false)

	stdout, _, err := runCLIRaw(t, server, "--json", "threads", "7")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	response := decodeThread(t, stdout)
	if body := response.Data[0].Body; body != "See the [plan](https://example.com/plan?a=1&b=2)." {
		t.Errorf("body = %q", body)
	}
}
