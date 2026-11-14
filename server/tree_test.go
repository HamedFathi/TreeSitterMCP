package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func treeFixture(t *testing.T) (*Engine, string) {
	e, root, _ := newEngine(t)
	write(t, root, "main.go", samples["go"].source)
	write(t, root, "src/app.py", samples["python"].source)
	write(t, root, "docs/guide.md", "manual")
	write(t, root, ".github/ci.yml", "test")
	write(t, root, ".env", "secret")
	write(t, root, "node_modules/dep.js", "ignored")
	for _, p := range []string{"empty", "src/empty"} {
		if err := os.MkdirAll(filepath.Join(root, p), 0700); err != nil {
			t.Fatal(err)
		}
	}
	return e, root
}
func TestProjectTreeAndDirectoryInfo(t *testing.T) {
	e, _ := treeFixture(t)
	tree := must(t, e, "get_project_tree", Input{Project: "demo", Depth: 1}).(TreeResult)
	if tree.Stats.Files != 3 || tree.Stats.Directories != 4 || tree.Incomplete {
		t.Fatalf("wrong stats: %+v", tree.Stats)
	}
	if tree.Entries[0].Depth != 0 || tree.Entries[0].Name != "demo" || !strings.Contains(tree.Markdown, "**demo/**") {
		t.Fatal("missing root")
	}
	foundEmpty := false
	for _, entry := range tree.Entries {
		if entry.Depth > 1 {
			t.Fatal("depth exceeded")
		}
		if entry.Path == "empty" {
			foundEmpty = entry.Empty
		}
	}
	if !foundEmpty {
		t.Fatal("empty directory missing")
	}
	info := must(t, e, "get_directory_info", Input{Project: "demo", Path: "src"}).(TreeResult)
	if info.Directory.Path != "src" || info.Stats.Files != 1 || info.Stats.Directories != 1 || info.Languages["python"] != 1 || info.Extensions[".py"] != 1 {
		t.Fatalf("wrong directory summary: %+v", info)
	}
	for _, entry := range info.Entries {
		if entry.Depth != 1 {
			t.Fatal("non-immediate child")
		}
	}
}
func TestProjectTreeFilters(t *testing.T) {
	e, _ := treeFixture(t)
	for _, tool := range []string{"get_project_tree", "get_directory_info"} {
		t.Run(tool, func(t *testing.T) {
			out := must(t, e, tool, Input{Project: "demo", FoldersOnly: true, IncludeHidden: true}).(TreeResult)
			if out.Stats.Files != 4 {
				t.Fatal("hidden visibility not applied")
			}
			for _, entry := range out.Entries {
				if entry.Kind != "directory" || blocked(entry.Path) {
					t.Fatalf("filter leaked entry: %+v", entry)
				}
			}
		})
	}
	out := must(t, e, "get_project_tree", Input{Project: "demo", Depth: 32, Extensions: []string{"py"}}).(TreeResult)
	files := 0
	for _, entry := range out.Entries {
		if entry.Kind == "file" {
			files++
			if entry.Language != "python" {
				t.Fatal("extension filter failed")
			}
		}
	}
	if files != 1 || out.Stats.Files != 3 {
		t.Fatal("display filter changed counts")
	}
}
func TestFindPaths(t *testing.T) {
	e, _ := treeFixture(t)
	for _, c := range []struct {
		pattern string
		regex   bool
		want    int
	}{{"*.py", false, 1}, {"**/*.go", false, 1}, {"src/**", false, 3}, {"^src/.*[.]py$", true, 1}, {"not-found", false, 0}} {
		t.Run(c.pattern, func(t *testing.T) {
			r := must(t, e, "find_paths", Input{Project: "demo", Pattern: c.pattern, Regex: c.regex}).(TreeResult)
			if len(r.Entries) != c.want {
				t.Fatalf("got %d want %d", len(r.Entries), c.want)
			}
		})
	}
	for _, in := range []Input{{Project: "demo", Pattern: "["}, {Project: "demo", Pattern: "[", Regex: true}, {Project: "demo"}} {
		if _, err := e.Execute(context.Background(), "find_paths", in); err == nil {
			t.Fatal("invalid pattern accepted")
		}
	}
}
func TestTreePaginationAndBudget(t *testing.T) {
	e, root := treeFixture(t)
	for i := 0; i < 30; i++ {
		write(t, root, fmt.Sprintf("items/file-%02d.go", i), "package items\n")
	}
	in := Input{Project: "demo", Depth: 32, Limit: 3, MaxBytes: 2048}
	seen := map[string]bool{}
	first := ""
	for {
		r := must(t, e, "get_project_tree", in).(TreeResult)
		b, _ := json.Marshal(r)
		if len(b) > 2048 {
			t.Fatal("response budget exceeded")
		}
		for _, entry := range r.Entries {
			if seen[entry.Path] {
				t.Fatal("duplicate paginated path")
			}
			seen[entry.Path] = true
		}
		if !r.Truncated {
			break
		}
		if first == "" {
			first = r.NextCursor
		}
		if r.NextCursor == in.Cursor {
			t.Fatal("cursor did not advance")
		}
		in.Cursor = r.NextCursor
	}
	if len(seen) != 39 {
		t.Fatalf("expected 39 entries; got %d", len(seen))
	}
	in.Cursor = first
	in.FoldersOnly = true
	if _, err := e.Execute(context.Background(), "get_project_tree", in); err == nil {
		t.Fatal("cursor crossed filters")
	}
	in.FoldersOnly = false
	write(t, root, "new.txt", "new")
	if _, err := e.Execute(context.Background(), "get_project_tree", in); err == nil {
		t.Fatal("stale tree cursor accepted")
	}
}
func TestTreeSecurityAndLimits(t *testing.T) {
	e, root := treeFixture(t)
	outside := t.TempDir()
	write(t, outside, "secret.txt", "secret")
	symlinkOrSkip(t, outside, filepath.Join(root, "escape"))
	for _, p := range []string{"../outside", "/etc", "escape", "escape/sub", ".git", ".env", "main.go"} {
		t.Run(p, func(t *testing.T) {
			if _, err := e.Execute(context.Background(), "get_project_tree", Input{Project: "demo", Path: p, IncludeHidden: true}); err == nil {
				t.Fatal("unsafe subtree accepted")
			}
		})
	}
	e.opts.MaxFiles = 1
	r := must(t, e, "get_project_tree", Input{Project: "demo"}).(TreeResult)
	if !r.Incomplete || r.Stats.Complete {
		t.Fatal("incomplete scan not flagged")
	}
	for _, entry := range r.Entries {
		if entry.Empty {
			t.Fatal("incomplete folder marked empty")
		}
	}
}

func TestSearchTextSkipsBinaryFiles(t *testing.T) {
	e, root := treeFixture(t)
	write(t, root, "lib/needle.dll", "needle in a library")
	write(t, root, "lib/blob.txt", "needle\x00binary")
	write(t, root, "lib/real.txt", "a needle in text")
	r := must(t, e, "search_text", Input{Project: "demo", Pattern: "needle"})
	b, _ := json.Marshal(r)
	if !strings.Contains(string(b), "lib/real.txt") || strings.Contains(string(b), "needle.dll") || strings.Contains(string(b), "blob.txt") {
		t.Fatalf("binary files must be skipped and text files searched: %s", b)
	}
}
