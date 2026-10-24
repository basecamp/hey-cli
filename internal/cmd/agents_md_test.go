package cmd

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Codex reads at most project_doc_max_bytes of AGENTS.md, 32 KiB by default, and
// silently drops the rest. Past that, a Codex session loses whatever comes last.
const codexProjectDocMaxBytes = 32 * 1024

var markdownLinkTarget = regexp.MustCompile(`\]\(([^)\s]+)\)`)

func TestAgentsMDFitsCodexProjectDocLimit(t *testing.T) {
	data, err := os.ReadFile("../../AGENTS.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > codexProjectDocMaxBytes {
		t.Fatalf("AGENTS.md is %d bytes; Codex reads only the first %d. Move detail into docs/architecture/ and leave a pointer.",
			len(data), codexProjectDocMaxBytes)
	}
}

func TestAgentsMDAndArchitectureDocLinksResolve(t *testing.T) {
	docs, err := filepath.Glob("../../docs/architecture/*.md")
	if err != nil {
		t.Fatal(err)
	}
	docs = append(docs, "../../AGENTS.md")

	for _, doc := range docs {
		data, err := os.ReadFile(doc)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range markdownLinkTarget.FindAllSubmatch(data, -1) {
			target, _, _ := strings.Cut(string(match[1]), "#")
			if target == "" || strings.Contains(target, "://") {
				continue
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(doc), target)); err != nil {
				t.Errorf("%s links to %s, which does not exist", filepath.Base(doc), target)
			}
		}
	}
}
