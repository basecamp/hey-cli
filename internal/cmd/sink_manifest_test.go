package cmd

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

// TestSinksAreSanitized walks the CLI and TUI sources and fails on a write that hands a
// field the server filled in — a sender's name, a subject, a filename — straight to a
// terminal without one of the sanitizers in between.
//
// What it checks is syntactic: a call to a listed sink whose argument mentions a listed
// field outside a listed sanitizer call. It sees `fmt.Fprintf(w, "%s", e.Creator.Name)`
// and not the same value passed through a local variable first, so it is an inventory
// of the direct pattern and a guard against the honest mistake of adding another, not a
// taint analysis. The manifest in testdata/sink_manifest.txt lists the fields, the
// sanitizers, the sinks, and the exemptions with their reasons.
func TestSinksAreSanitized(t *testing.T) {
	manifest := readSinkManifest(t, filepath.Join("testdata", "sink_manifest.txt"))

	violations := slices.Concat(
		sinkViolations(t, ".", manifest),
		sinkViolations(t, filepath.Join("..", "tui"), manifest),
	)
	sort.Strings(violations)
	for _, violation := range violations {
		t.Error(violation)
	}
}

type sinkManifest struct {
	fields     map[string]bool
	sanitizers map[string]bool
	sinks      map[string]bool
	exempt     map[string]string
}

// isSink matches a call by its full name or by the bare function or method name, so
// "table.addRow" is addRow. isSanitizer is stricter: a sanitizer listed with its
// package, like markdown.Render, matches only that qualified call — a lipgloss style's
// Render is a sink, not a sanitizer, and must not pass for one by sharing the name.
func (m sinkManifest) isSink(name string) bool {
	return m.sinks[name] || m.sinks[shortName(name)]
}

func (m sinkManifest) isSanitizer(name string) bool {
	if m.sanitizers[name] {
		return true
	}
	short := shortName(name)
	return short == name && m.sanitizers[short]
}

func readSinkManifest(t *testing.T, path string) sinkManifest {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open manifest: %v", err)
	}
	defer file.Close()

	manifest := sinkManifest{
		fields:     map[string]bool{},
		sanitizers: map[string]bool{},
		sinks:      map[string]bool{},
		exempt:     map[string]string{},
	}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		kind, rest, _ := strings.Cut(line, " ")
		name, reason, _ := strings.Cut(strings.TrimSpace(rest), " ")
		switch kind {
		case "field":
			manifest.fields[name] = true
		case "sanitizer":
			manifest.sanitizers[name] = true
		case "sink":
			manifest.sinks[name] = true
		case "exempt":
			if strings.TrimSpace(reason) == "" {
				t.Fatalf("manifest: exemption %s needs a reason", name)
			}
			manifest.exempt[name] = strings.TrimSpace(reason)
		default:
			t.Fatalf("manifest: unknown line %q", line)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	return manifest
}

func sinkViolations(t *testing.T, dir string, manifest sinkManifest) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	pkg := "cmd"
	if dir != "." {
		pkg = filepath.Base(dir)
	}

	fset := token.NewFileSet()
	var violations []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range file.Decls {
			function, ok := decl.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			key := "internal/" + pkg + "/" + name + ":" + function.Name.Name
			if _, exempt := manifest.exempt[key]; exempt {
				continue
			}
			ast.Inspect(function.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || !manifest.isSink(callName(call)) || manifest.isSanitizer(callName(call)) {
					return true
				}
				for _, arg := range call.Args {
					if field := unsanitizedField(arg, manifest); field != "" {
						position := fset.Position(arg.Pos())
						violations = append(violations, fmt.Sprintf("%s:%d: %s writes %s without a sanitizer (exempt %s in the manifest with a reason if it is safe)",
							filepath.ToSlash(position.Filename), position.Line, callName(call), field, key))
					}
				}
				return true
			})
		}
	}
	return violations
}

// callName is the name a call is made by: "fmt.Fprintf" for a package function,
// "WriteString" for a method, "markdownSafeText" for a function in the package.
func callName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		if pkg, ok := fn.X.(*ast.Ident); ok {
			return pkg.Name + "." + fn.Sel.Name
		}
		return fn.Sel.Name
	default:
		return ""
	}
}

// unsanitizedField names the first listed field an expression reaches for outside a
// sanitizer call, or "" when there is none. A call's own name is not a field —
// cmd.Name() is a method — so only its arguments are looked at.
func unsanitizedField(expr ast.Expr, manifest sinkManifest) string {
	switch e := expr.(type) {
	case *ast.CallExpr:
		if manifest.isSanitizer(callName(e)) {
			return ""
		}
		for _, arg := range e.Args {
			if field := unsanitizedField(arg, manifest); field != "" {
				return field
			}
		}
		return ""
	case *ast.SelectorExpr:
		if manifest.fields[e.Sel.Name] {
			return exprString(e)
		}
		return unsanitizedField(e.X, manifest)
	}

	found := ""
	ast.Inspect(expr, func(n ast.Node) bool {
		if found != "" || n == expr {
			return found == ""
		}
		switch n := n.(type) {
		case *ast.CallExpr, *ast.SelectorExpr:
			found = unsanitizedField(n.(ast.Expr), manifest)
			return false
		}
		return true
	})
	return found
}

func shortName(name string) string {
	_, short, found := strings.Cut(name, ".")
	if found {
		return short
	}
	return name
}

func exprString(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return exprString(e.X) + "." + e.Sel.Name
	case *ast.CallExpr:
		return exprString(e.Fun) + "(…)"
	case *ast.IndexExpr:
		return exprString(e.X) + "[…]"
	case *ast.StarExpr:
		return "*" + exprString(e.X)
	default:
		return "…"
	}
}

// The checker itself is exercised on a package written for it: it has to see the
// direct write, and it has to accept the same write through a sanitizer.
func TestSinkCheckerSeesADirectWrite(t *testing.T) {
	manifest := readSinkManifest(t, filepath.Join("testdata", "sink_manifest.txt"))
	dir := t.TempDir()
	source := `package probe

import (
	"fmt"
	"io"

	"github.com/basecamp/hey-cli/internal/terminal"
)

type entry struct{ Name, Summary string }

func direct(w io.Writer, e entry) {
	fmt.Fprintf(w, "%s\n", e.Name)
	fmt.Fprintln(w, truncate(e.Summary, 10))
}

func sanitized(w io.Writer, e entry) {
	fmt.Fprintf(w, "%s\n", terminal.SanitizeLine(e.Name))
	fmt.Fprintln(w, truncate(terminal.SanitizeLine(e.Summary), 10))
}

type style struct{}

func (style) Render(s string) string { return s }

func styled(w io.Writer, e entry, title style) {
	fmt.Fprintln(w, title.Render(e.Name))
}

func truncate(s string, n int) string { return s }
`
	if err := os.WriteFile(filepath.Join(dir, "probe.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}

	// The styled write is seen twice: once by Fprintln and once by the Render inside it.
	violations := sinkViolations(t, dir, manifest)
	if len(violations) != 4 {
		t.Fatalf("violations = %q, want the two direct writes and the styled one twice", violations)
	}
	for _, violation := range violations {
		if !strings.Contains(violation, ":direct in the manifest") && !strings.Contains(violation, ":styled in the manifest") {
			t.Errorf("violation %q names the wrong function", violation)
		}
	}
}
