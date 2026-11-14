package server

import (
	"context"
	"encoding/json"
	"github.com/hamedfathi/TreeSitterMCP/internal/catalog"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func allPages(t *testing.T, e *Engine, tool string, in Input) []any {
	t.Helper()
	out := []any{}
	seen := map[string]bool{}
	for {
		pg := page(t, must(t, e, tool, in))
		out = append(out, pg.Items...)
		if pg.NextCursor == "" {
			break
		}
		if seen[pg.NextCursor] {
			t.Fatal("pagination loop")
		}
		seen[pg.NextCursor] = true
		in.Cursor = pg.NextCursor
	}
	return out
}

// This contract deliberately includes malformed nonempty input: every grammar
// must offer the same core mechanics even when Tree-sitter uses error recovery.
// It is not a claim that this synthetic probe is valid syntax in every language.
func TestEveryGrammarContract(t *testing.T) {
	for _, language := range catalog.Names() {
		t.Run(language, func(t *testing.T) {
			e, root, _ := newEngine(t)
			e.opts.EnableLanguageSpecific = false
			e.opts.MaxCacheEntries = 2
			source := "sample = 1\nsample = 2\n"
			if s, ok := samples[language]; ok {
				source = s.source
			}
			write(t, root, "fixture.txt", source)
			write(t, root, "copy.txt", source)
			in := Input{Project: "demo", Path: "fixture.txt", Language: language}
			metadata := must(t, e, "get_file_metadata", in).(map[string]any)
			if metadata["sha256"] != digest([]byte(source)) || metadata["bytes"] != len(source) {
				t.Fatal("metadata mismatch")
			}
			info := must(t, e, "get_language_info", Input{Language: language}).(map[string]any)
			if info["language"] != language {
				t.Fatal(info)
			}
			vocab := allPages(t, e, "get_language_info", Input{Language: language, Section: "node_types", Limit: 200})
			if len(vocab) != int(info["node_kind_count"].(uint32)) {
				t.Fatal("node vocabulary incomplete")
			}
			fields := allPages(t, e, "get_language_info", Input{Language: language, Section: "fields"})
			if len(fields) != int(info["field_count"].(uint32)) {
				t.Fatal("fields incomplete")
			}
			ast := page(t, must(t, e, "get_ast", in))
			if len(ast.Items) == 0 {
				t.Fatal("missing AST root")
			}
			rootID := ast.Items[0].(map[string]any)["node_id"].(string)
			nodes := allPages(t, e, "navigate_ast", Input{Project: "demo", Path: in.Path, Language: language, Relation: "subtree", Depth: 32})
			if len(nodes) == 0 || nodes[0].(map[string]any)["node_id"] != rootID {
				t.Fatal("root identity mismatch")
			}
			rootInfo := page(t, must(t, e, "navigate_ast", in)).Items[0].(map[string]any)
			children := allPages(t, e, "navigate_ast", Input{Project: "demo", Path: in.Path, Language: language, Relation: "children"})
			if len(children) != int(rootInfo["child_count"].(uint)) {
				t.Fatal("children mismatch")
			}
			if len(children) > 0 {
				child := children[0].(map[string]any)["node_id"].(string)
				ni := Input{Project: "demo", Path: in.Path, Language: language, NodeID: child, Relation: "parent"}
				parent := page(t, must(t, e, "navigate_ast", ni))
				if len(parent.Items) != 1 || parent.Items[0].(map[string]any)["node_id"] != rootID {
					t.Fatal("parent navigation")
				}
				ni.Relation = "siblings"
				if len(allPages(t, e, "navigate_ast", ni)) != len(children)-1 {
					t.Fatal("siblings")
				}
				ni.Relation = "ancestors"
				if len(allPages(t, e, "navigate_ast", ni)) != 1 {
					t.Fatal("ancestors")
				}
			}
			positionIn := in
			positionIn.StartByte = rootInfo["span"].(Span).StartByte
			pos := must(t, e, "get_node_at_position", positionIn).(map[string]any)
			if pos["node_id"] == "" {
				t.Fatal("position missing node")
			}
			lineIn := in
			lineIn.Line = int(rootInfo["span"].(Span).StartLine)
			lineIn.Column = int(rootInfo["span"].(Span).StartColumn)
			if must(t, e, "get_node_at_position", lineIn).(map[string]any)["node_id"] != pos["node_id"] {
				t.Fatal("line-byte position disagreement")
			}
			metrics := must(t, e, "get_ast_metrics", in).(map[string]any)
			total := 0
			for _, row := range metrics["histogram"].(Page).Items {
				total += row.(map[string]any)["count"].(int)
			}
			if total != metrics["nodes"].(int) || metrics["nodes"].(int) < 1 {
				t.Fatal("histogram mismatch")
			}
			diagnostics := allPages(t, e, "get_parse_diagnostics", in)
			if len(diagnostics) != metrics["errors"].(int)+metrics["missing"].(int) {
				t.Fatal("diagnostic counts")
			}
			qi := in
			qi.Query = "(_) @node\n(ERROR) @error"
			captures := allPages(t, e, "query_ast", qi)
			if len(captures) == 0 {
				t.Fatal("wildcard has no captures")
			}
			multi := allPages(t, e, "query_project", Input{Project: "demo", Language: language, Query: "(_) @node\n(ERROR) @error", PathGlob: "fixture.txt"})
			if len(multi) != len(captures) {
				t.Fatal("multi-file query mismatch")
			}
			for _, item := range multi {
				if item.(map[string]any)["status"] != "capture" {
					t.Fatal(item)
				}
			}
			search := allPages(t, e, "search_text", Input{Project: "demo", Pattern: "SAMPLE", IgnoreCase: true, WholeWord: true, PathGlob: "fixture.txt", ContextLines: 1})
			if _, ok := samples[language]; !ok && len(search) != 2 {
				t.Fatal("text search did not find probe")
			}
			snapshot := must(t, e, "create_ast_snapshot", in).(map[string]any)["snapshot"].(string)
			same := allPages(t, e, "compare_ast_snapshots", Input{Project: "demo", Snapshot: snapshot, OtherSnapshot: snapshot})
			if len(same) != 0 {
				t.Fatal("unchanged snapshot differs")
			}
			similar := allPages(t, e, "find_similar_subtrees", Input{Project: "demo", Path: in.Path, Language: language, OtherPath: "copy.txt", MinScore: 1})
			found := false
			for _, item := range similar {
				m := item.(map[string]any)
				if m["exact_shape"].(bool) && m["score"].(float64) == 1 {
					found = true
				}
			}
			if !found {
				t.Fatal("identical source lacks exact subtree match")
			}
			write(t, root, in.Path, source+"changed_probe\n")
			changed := must(t, e, "create_ast_snapshot", in).(map[string]any)["snapshot"].(string)
			if changed == snapshot {
				t.Fatal("source change did not change snapshot ID")
			}
			must(t, e, "compare_ast_snapshots", Input{Project: "demo", Snapshot: snapshot, OtherSnapshot: changed})
			stale := in
			stale.NodeID = rootID
			if _, err := e.Execute(context.Background(), "navigate_ast", stale); err == nil {
				t.Fatal("stale node ID accepted")
			}
			must(t, e, "clear_cache", Input{Project: "demo", Path: in.Path})
			if _, err := e.Execute(context.Background(), "compare_ast_snapshots", Input{Project: "demo", Snapshot: snapshot, OtherSnapshot: snapshot}); err == nil {
				t.Fatal("file snapshots not cleared")
			}
			must(t, e, "cache_stats", Input{})
			_, curated := samples[language]
			evidence, _ := json.Marshal(map[string]any{"language": language, "source_sha256": digest([]byte(source)), "source_bytes": len(source), "curated_valid_fixture": curated, "nodes": metrics["nodes"], "syntax_errors": metrics["errors"], "missing_nodes": metrics["missing"], "vocabulary_entries": len(vocab), "field_entries": len(fields), "status": "passed"})
			t.Logf("CONTRACT %s", evidence)
		})
	}
}

func TestNavigationDiagnosticsAndDiff(t *testing.T) {
	e, root, _ := newEngine(t)
	source := "def alpha(x):\n    return x + 1\n\ndef beta(x):\n    return x + 1\n"
	write(t, root, "a.py", source)
	in := Input{Project: "demo", Path: "a.py"}
	metrics := must(t, e, "get_ast_metrics", in).(map[string]any)
	if metrics["errors"] != 0 || metrics["nodes"].(int) < 10 {
		t.Fatal(metrics)
	}
	nodes := allPages(t, e, "navigate_ast", Input{Project: "demo", Path: "a.py", Relation: "subtree", Depth: 32})
	var target string
	for _, row := range nodes {
		m := row.(map[string]any)
		if m["type"] == "function_definition" {
			target = m["node_id"].(string)
			break
		}
	}
	if target == "" {
		t.Fatal("fixture failed")
	}
	similar := allPages(t, e, "find_similar_subtrees", Input{Project: "demo", Path: "a.py", NodeID: target, MinScore: 1})
	if len(similar) != 1 || !similar[0].(map[string]any)["exact_shape"].(bool) {
		t.Fatal(similar)
	}
	before := must(t, e, "create_ast_snapshot", in).(map[string]any)["snapshot"].(string)
	write(t, root, "a.py", strings.ReplaceAll(source, "alpha", "gamma"))
	after := must(t, e, "create_ast_snapshot", in).(map[string]any)["snapshot"].(string)
	if len(allPages(t, e, "compare_ast_snapshots", Input{Project: "demo", Snapshot: before, OtherSnapshot: after})) == 0 {
		t.Fatal("leaf rename invisible in diff")
	}
	write(t, root, "a.py", "def broken(:\n return (\n")
	if len(allPages(t, e, "get_parse_diagnostics", in)) == 0 {
		t.Fatal("syntax errors absent")
	}
	write(t, root, "b.go", "package demo\n")
	if _, err := e.Execute(context.Background(), "find_similar_subtrees", Input{Project: "demo", Path: "a.py", OtherPath: "b.go"}); err == nil {
		t.Fatal("cross grammar similarity admitted")
	}
}
func TestSearchAndMultiQuery(t *testing.T) {
	e, root, _ := newEngine(t)
	write(t, root, "src/a.py", "# before\nvalue = 'Needle needles NEEDLE _needle needle9'\n# after\n")
	write(t, root, "b.go", "package main\n")
	write(t, root, "bad.bin", string([]byte{0, 1}))
	search := allPages(t, e, "search_text", Input{Project: "demo", Pattern: "needle", IgnoreCase: true, WholeWord: true, PathGlob: "src/**/*.py", ContextLines: 1})
	if len(search) != 2 {
		t.Fatal(search)
	}
	if len(search[0].(map[string]any)["context"].([]any)) != 3 {
		t.Fatal("missing context")
	}
	for _, in := range []Input{{Project: "demo", Pattern: "[", Regex: true}, {Project: "demo", Pattern: "x", PathGlob: "["}, {Project: "demo", Pattern: "x", ContextLines: 6}} {
		if _, err := e.Execute(context.Background(), "search_text", in); err == nil {
			t.Fatal("invalid search admitted")
		}
	}
	rows := allPages(t, e, "query_project", Input{Project: "demo", Query: "(function_definition) @f"})
	errors, noMatch := 0, 0
	for _, row := range rows {
		switch row.(map[string]any)["status"] {
		case "error":
			errors++
		case "no_matches":
			noMatch++
		}
	}
	if errors != 2 || noMatch != 1 {
		t.Fatal(rows)
	}
	in := Input{Project: "demo", Query: "(_) @n", PathGlob: "src/*", Limit: 1}
	pg := page(t, must(t, e, "query_project", in))
	if pg.NextCursor == "" {
		t.Fatal("pagination missing")
	}
	write(t, root, "src/a.py", "x=2\n")
	in.Cursor = pg.NextCursor
	if _, err := e.Execute(context.Background(), "query_project", in); err == nil {
		t.Fatal("stale multi query cursor")
	}
}
func TestCacheScopesTTLAndPersistence(t *testing.T) {
	e, root, state := newEngine(t)
	write(t, root, "a.py", "x=1\n")
	write(t, root, "b.py", "y=2\n")
	a := Input{Project: "demo", Path: "a.py"}
	b := Input{Project: "demo", Path: "b.py"}
	must(t, e, "get_symbols", a)
	must(t, e, "get_symbols", b)
	id := must(t, e, "create_ast_snapshot", a).(map[string]any)["snapshot"].(string)
	e.Close()
	restored, err := New(Options{AllowRoots: []string{root}, StateDir: state})
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	must(t, restored, "compare_ast_snapshots", Input{Project: "demo", Snapshot: id, OtherSnapshot: id})
	if _, err := restored.Execute(context.Background(), "get_symbols", a); err == nil {
		t.Fatal("nonuniform extension enabled by default")
	}
	must(t, restored, "get_ast", a)
	must(t, restored, "get_ast", b)
	must(t, restored, "clear_cache", a)
	if len(restored.trees) != 1 || restored.cacheStats()["derived_entries"] != 1 {
		t.Fatal("file clearing affects other file")
	}
	for _, p := range restored.trees {
		p.touched = time.Now().Add(-time.Hour)
	}
	filepath.WalkDir(filepath.Join(state, "derived"), func(path string, d os.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			old := time.Now().Add(-time.Hour)
			os.Chtimes(path, old, old)
		}
		return nil
	})
	stats := must(t, restored, "cache_stats", Input{}).(map[string]any)
	if stats["entries"] != 0 || stats["derived_entries"] != 0 {
		t.Fatal("TTL did not expire caches")
	}
	if _, err := restored.Execute(context.Background(), "clear_cache", Input{Path: "a.py"}); err == nil {
		t.Fatal("path without project")
	}
	must(t, restored, "clear_cache", Input{Project: "demo"})
	if len(restored.projects) != 1 {
		t.Fatal("cache removed registration")
	}
}
func TestCoreMCPAndPrompts(t *testing.T) {
	e, root, _ := newEngine(t)
	e.opts.EnableLanguageSpecific = false
	write(t, root, "a.py", "x=1\n")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	a, b := mcp.NewInMemoryTransports()
	ss, err := NewMCP(e).Connect(ctx, a, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "contract-test", Version: "1"}, nil).Connect(ctx, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	tools, err := cs.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 25 {
		t.Fatal(tools, err)
	}
	prompts, err := cs.ListPrompts(ctx, nil)
	if err != nil || len(prompts.Prompts) != 4 {
		t.Fatal(prompts, err)
	}
	node := page(t, must(t, e, "navigate_ast", Input{Project: "demo", Path: "a.py"})).Items[0].(map[string]any)["node_id"].(string)
	for _, p := range prompts.Prompts {
		args := map[string]string{"project": "demo"}
		if p.Name != "explore_project" {
			args["path"] = "a.py"
		}
		if p.Name == "explain_query" {
			args["query"] = "(_) @n"
		}
		if p.Name == "explain_subtree" {
			args["node_id"] = node
		}
		result, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: p.Name, Arguments: args})
		if err != nil || len(result.Messages) != 1 {
			t.Fatal(p.Name, result, err)
		}
	}
	result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "get_language_info", Arguments: map[string]any{"language": "python", "section": "node_types", "limit": 1}})
	if err != nil || result.IsError {
		t.Fatal(result, err)
	}
	var pg Page
	if err := json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &pg); err != nil || len(pg.Items) != 1 || pg.NextCursor == "" {
		t.Fatal(pg, err)
	}
}

func TestStableIDsAndMetricPagination(t *testing.T) {
	e, root, state := newEngine(t)
	write(t, root, "a.py", samples["python"].source)
	in := Input{Project: "demo", Path: "a.py"}
	id := page(t, must(t, e, "navigate_ast", in)).Items[0].(map[string]any)["node_id"].(string)
	must(t, e, "clear_cache", Input{})
	in.NodeID = id
	if len(page(t, must(t, e, "navigate_ast", in)).Items) != 1 {
		t.Fatal("ID did not survive eviction")
	}
	e.Close()
	next, err := New(Options{AllowRoots: []string{root}, StateDir: state})
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if page(t, must(t, next, "navigate_ast", in)).Items[0].(map[string]any)["node_id"] != id {
		t.Fatal("ID changed after restart")
	}
	in.MaxBytes = 1024
	in.Limit = 1
	total := 0
	pages := 0
	for {
		m := must(t, next, "get_ast_metrics", in).(map[string]any)
		encoded, _ := json.Marshal(m)
		if len(encoded) > 1024 {
			t.Fatal("metrics budget exceeded")
		}
		pg := m["histogram"].(Page)
		for _, row := range pg.Items {
			total += row.(map[string]any)["count"].(int)
		}
		pages++
		if pg.NextCursor == "" {
			if total != m["nodes"] {
				t.Fatal("paginated histogram incomplete")
			}
			break
		}
		in.Cursor = pg.NextCursor
	}
	if pages < 2 {
		t.Fatal("fixture did not exercise pagination")
	}
}
func TestDiskCacheCapsAndEmptyDirectoryCleanup(t *testing.T) {
	e, root, state := newEngine(t)
	e.opts.MaxCacheEntries = 2
	for i := 0; i < 8; i++ {
		name := string(rune('a'+i)) + ".py"
		write(t, root, name, "x=1\n")
		in := Input{Project: "demo", Path: name}
		must(t, e, "get_symbols", in)
		must(t, e, "create_ast_snapshot", in)
	}
	stats := must(t, e, "cache_stats", Input{}).(map[string]any)
	if stats["derived_entries"] != 2 || stats["snapshots_entries"] != 2 {
		t.Fatal("disk entry cap", stats)
	}
	dirs := 0
	for _, kind := range []string{"derived", "snapshots"} {
		filepath.WalkDir(filepath.Join(state, kind), func(path string, d os.DirEntry, err error) error {
			if err == nil {
				if d.IsDir() {
					dirs++
				} else {
					old := time.Now().Add(-time.Hour)
					os.Chtimes(path, old, old)
				}
			}
			return nil
		})
	}
	if dirs > 8 {
		t.Fatal("evicted entries left unbounded empty directories", dirs)
	}
	stats = must(t, e, "cache_stats", Input{}).(map[string]any)
	if stats["derived_entries"] != 0 || stats["snapshots_entries"] != 0 {
		t.Fatal("age expiry", stats)
	}
	for _, kind := range []string{"derived", "snapshots"} {
		entries, err := os.ReadDir(filepath.Join(state, kind))
		if err != nil || len(entries) != 0 {
			t.Fatal("expired entries left hash directories", entries, err)
		}
	}
}
