package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/hamedfathi/TreeSitterMCP/internal/catalog"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	pack "github.com/xberg-io/tree-sitter-language-pack/packages/go"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

var samples = map[string]struct{ ext, source string }{
	"go":         {"go", "package demo\nimport \"fmt\"\nfunc Add(a, b int) int { fmt.Println(a); return a+b }\n"},
	"python":     {"py", "import os\ndef add(a, b):\n    return a + b\n"},
	"javascript": {"js", "import { x } from './dep.js';\nfunction add(a, b) { return a+b; }\n"},
	"typescript": {"ts", "import { x } from './dep';\nfunction add(a: number, b: number): number { return a+b; }\n"},
	"rust":       {"rs", "use std::fmt;\npub fn add(a: i32, b: i32) -> i32 { a+b }\n"},
	"c":          {"c", "#include <stdio.h>\nint add(int a, int b) { return a+b; }\n"},
	"cpp":        {"cpp", "#include <vector>\nclass Box { public: int add(int a, int b) { return a+b; } };\n"},
	"csharp":     {"cs", "using System;\nclass Box { public int Add(int a, int b) { return a+b; } }\n"},
	"swift":      {"swift", "import Foundation\nfunc add(a: Int, b: Int) -> Int { return a+b }\n"},
	"java":       {"java", "import java.util.List;\nclass Box { int add(int a, int b) { return a+b; } }\n"},
	"kotlin":     {"kt", "import kotlin.math.abs\nfun add(a: Int, b: Int): Int { return a+b }\n"},
	"dart":       {"dart", "import 'dart:math';\nint add(int a, int b) { return a+b; }\n"},
	"julia":      {"jl", "using LinearAlgebra\nfunction add(a,b)\n    a+b\nend\n"},
}

func TestMain(m *testing.M) {
	base, err := filepath.Abs("../runtime/parsers")
	if err != nil {
		panic(err)
	}
	if v := os.Getenv("TREESITTERMCP_TEST_CACHE"); v != "" {
		base = v
	}
	if err = pack.Configure(pack.PackConfig{CacheDir: &base}); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}
func newEngine(t *testing.T) (*Engine, string, string) {
	t.Helper()
	root, state := t.TempDir(), t.TempDir()
	e, err := New(Options{AllowRoots: []string{root}, StateDir: state, EnableLanguageSpecific: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Close() })
	must(t, e, "register_project", Input{Project: "demo", Root: root})
	return e, root, state
}
func must(t *testing.T, e *Engine, tool string, in Input) any {
	t.Helper()
	v, err := e.Execute(context.Background(), tool, in)
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	return v
}
func write(t *testing.T, root, name, text string) {
	t.Helper()
	p := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}
func page(t *testing.T, v any) Page {
	t.Helper()
	p, ok := v.(Page)
	if !ok {
		t.Fatalf("not a page: %#v", v)
	}
	return p
}
func TestCompleteCatalog(t *testing.T) {
	names := catalog.Names()
	if len(names) != 371 {
		t.Fatal(len(names))
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			e, root, _ := newEngine(t)
			write(t, root, "empty.txt", "")
			p := page(t, must(t, e, "get_ast", Input{Project: "demo", Path: "empty.txt", Language: name, Depth: 1}))
			if len(p.Items) == 0 {
				t.Fatal("missing root node")
			}
		})
	}
}
func TestLanguageSamples(t *testing.T) {
	e, root, _ := newEngine(t)
	for lang, s := range samples {
		t.Run(lang, func(t *testing.T) {
			name := "sample." + s.ext
			write(t, root, name, s.source)
			in := Input{Project: "demo", Path: name, Language: lang}
			p := page(t, must(t, e, "get_ast", in))
			if p.Items[0].(map[string]any)["has_error"] == true {
				t.Fatal("sample has parse error")
			}
			if len(page(t, must(t, e, "get_symbols", in)).Items) == 0 {
				t.Fatal("missing symbols")
			}
			if len(page(t, must(t, e, "get_dependencies", in)).Items) == 0 {
				t.Fatal("missing dependencies")
			}
			in.Query = "(_) @node"
			in.Limit = 5
			if len(page(t, must(t, e, "query_ast", in)).Items) == 0 {
				t.Fatal("missing native captures")
			}
		})
	}
}
func TestSecurityBoundaries(t *testing.T) {
	e, root, _ := newEngine(t)
	outside := t.TempDir()
	write(t, outside, "secret.txt", "secret")
	write(t, root, ".env", "TOKEN=secret")
	write(t, root, "safe.go", samples["go"].source)
	for name, target := range map[string]string{"escape": filepath.Join(outside, "secret.txt"), "escape-dir": outside, "internal-link": filepath.Join(root, ".env")} {
		symlinkOrSkip(t, target, filepath.Join(root, name))
	}
	for _, p := range []string{"../secret.txt", "/etc/passwd", "escape", "escape-dir/secret.txt", "internal-link", ".env", "x/../../secret.txt", "a\\b", "."} {
		t.Run(p, func(t *testing.T) {
			if _, err := e.Execute(context.Background(), "read_file", Input{Project: "demo", Path: p}); err == nil {
				t.Fatal("unsafe read accepted")
			}
		})
	}
	if _, err := e.Execute(context.Background(), "register_project", Input{Project: "outside", Root: outside}); err == nil {
		t.Fatal("outside project accepted")
	}
	p := page(t, must(t, e, "list_files", Input{Project: "demo"}))
	if len(p.Items) != 1 || p.Items[0].(fileEntry).Path != "safe.go" {
		t.Fatal(p)
	}
	write(t, root, "big.txt", strings.Repeat("x", int(e.opts.MaxFileBytes)+1))
	if _, err := e.Execute(context.Background(), "read_file", Input{Project: "demo", Path: "big.txt"}); err == nil {
		t.Fatal("oversized source accepted")
	}
	write(t, root, "binary.txt", string([]byte{0xff, 0}))
	if _, err := e.Execute(context.Background(), "read_file", Input{Project: "demo", Path: "binary.txt"}); err == nil {
		t.Fatal("binary accepted")
	}
	if _, err := New(Options{StateDir: t.TempDir()}); err == nil {
		t.Fatal("missing allow root accepted")
	}
}
func TestPaginationBudgetsAndInvalidation(t *testing.T) {
	e, root, _ := newEngine(t)
	source := "package demo\n"
	for i := 0; i < 100; i++ {
		source += fmt.Sprintf("func Function%d() int {return %d}\n", i, i)
	}
	write(t, root, "many.go", source)
	in := Input{Project: "demo", Path: "many.go", Limit: 7, MaxBytes: 2048}
	count := 0
	first := ""
	for {
		p := page(t, must(t, e, "get_symbols", in))
		b, _ := json.Marshal(p)
		if len(b) > 2048 {
			t.Fatal("budget exceeded")
		}
		count += len(p.Items)
		if !p.Truncated {
			break
		}
		if first == "" {
			first = p.NextCursor
		}
		if p.NextCursor == in.Cursor {
			t.Fatal("cursor did not advance")
		}
		in.Cursor = p.NextCursor
	}
	if count != 100 {
		t.Fatal(count)
	}
	in.Cursor = first
	in.Name = "Function"
	if _, err := e.Execute(context.Background(), "get_symbols", in); err == nil {
		t.Fatal("cursor crossed filters")
	}
	in.Name = ""
	write(t, root, "many.go", source+"func Changed(){}\n")
	if _, err := e.Execute(context.Background(), "get_symbols", in); err == nil {
		t.Fatal("stale cursor accepted")
	}
	write(t, root, "long.txt", strings.Repeat("\"hello\" \u2603 ", 800))
	read := Input{Project: "demo", Path: "long.txt", MaxBytes: 1024}
	var restored strings.Builder
	for {
		r := must(t, e, "read_file", read).(map[string]any)
		b, _ := json.Marshal(r)
		if len(b) > 1024 {
			t.Fatal("read budget exceeded")
		}
		restored.WriteString(r["content"].(string))
		if r["truncated"] == false {
			break
		}
		read.Cursor = r["next_cursor"].(string)
	}
	expected, _ := os.ReadFile(filepath.Join(root, "long.txt"))
	if restored.String() != string(expected) {
		t.Fatal("UTF-8 source did not round trip")
	}
}
func TestCacheAndPersistence(t *testing.T) {
	e, root, state := newEngine(t)
	write(t, root, "a.py", samples["python"].source)
	in := Input{Project: "demo", Path: "a.py"}
	must(t, e, "get_symbols", in)
	must(t, e, "get_symbols", in)
	if e.hits != 1 || e.misses != 1 {
		t.Fatal("cache counters incorrect")
	}
	write(t, root, "a.py", strings.ReplaceAll(samples["python"].source, "add", "sum"))
	if page(t, must(t, e, "get_symbols", in)).Items[0].(Symbol).Name != "sum" {
		t.Fatal("stale native tree")
	}
	e.Close()
	e2, err := New(Options{AllowRoots: []string{root}, StateDir: state, EnableLanguageSpecific: true})
	if err != nil {
		t.Fatal(err)
	}
	defer e2.Close()
	if len(e2.projects) != 1 {
		t.Fatal("registration did not persist")
	}
	if page(t, must(t, e2, "get_symbols", in)).Items[0].(Symbol).Name != "sum" {
		t.Fatal("derived cache did not restore")
	}
	must(t, e2, "clear_cache", Input{})
	if _, err = os.Stat(filepath.Join(state, "derived")); !os.IsNotExist(err) {
		t.Fatal("derived cache not cleared")
	}
	must(t, e2, "unregister_project", Input{Project: "demo"})
	if len(e2.projects) != 0 {
		t.Fatal("registration not removed")
	}
}
func TestQueriesAndDependencies(t *testing.T) {
	e, root, _ := newEngine(t)
	write(t, root, "main.js", samples["javascript"].source)
	write(t, root, "dep.js", "export const x=1;\n")
	p := page(t, must(t, e, "get_dependencies", Input{Project: "demo", Path: "main.js"}))
	if p.Items[0].(Dependency).ResolvedPath != "dep.js" {
		t.Fatal("local edge not resolved")
	}
	in := Input{Project: "demo", Path: "main.js", Query: `((identifier) @id (#eq? @id "add"))`}
	p = page(t, must(t, e, "query_ast", in))
	if len(p.Items) != 1 || p.Items[0].(map[string]any)["text"] != "add" {
		t.Fatal("predicate not applied")
	}
	for _, q := range []string{"(missing_node) @x", "(", `((identifier) @id (#custom? @id))`} {
		in.Query = q
		if _, err := e.Execute(context.Background(), "query_ast", in); err == nil {
			t.Fatal("invalid query accepted")
		}
	}
	if len(page(t, must(t, e, "search_text", Input{Project: "demo", Pattern: "a.d", Regex: true})).Items) == 0 {
		t.Fatal("regex search failed")
	}
	if _, err := e.Execute(context.Background(), "search_text", Input{Project: "demo", Pattern: "[", Regex: true}); err == nil {
		t.Fatal("invalid regex accepted")
	}
	if _, err := e.Execute(context.Background(), "get_ast", Input{Project: "demo", Path: "main.js", Language: "apl"}); err == nil {
		t.Fatal("APL incorrectly claimed")
	}
}
func TestConcurrencyCancellationAndEviction(t *testing.T) {
	e, root, _ := newEngine(t)
	e.opts.MaxCacheEntries = 2
	for i := 0; i < 4; i++ {
		write(t, root, fmt.Sprintf("%d.py", i), samples["python"].source)
	}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := e.Execute(context.Background(), "get_symbols", Input{Project: "demo", Path: fmt.Sprintf("%d.py", i%4)}); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if len(e.trees) > 2 {
		t.Fatal("LRU exceeded")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.Execute(ctx, "get_symbols", Input{Project: "demo", Path: "0.py"}); err == nil {
		t.Fatal("cancelled call succeeded")
	}
}
func TestMCPIntegration(t *testing.T) {
	e, root, _ := newEngine(t)
	write(t, root, "main.go", samples["go"].source)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a, b := mcp.NewInMemoryTransports()
	ss, err := NewMCP(e).Connect(ctx, a, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, nil)
	cs, err := client.Connect(ctx, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	tools, err := cs.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != len(specs) {
		t.Fatal("tool discovery failed")
	}
	result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "get_symbols", Arguments: map[string]any{"project": "demo", "path": "main.go"}})
	if err != nil || result.IsError {
		t.Fatalf("call: %v %v", result, err)
	}
	for _, name := range []string{"get_project_tree", "get_directory_info", "find_paths"} {
		args := map[string]any{"project": "demo"}
		if name == "find_paths" {
			args["pattern"] = "**/*.go"
		}
		r, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil || r.IsError {
			t.Fatalf("%s: %v %v", name, r, err)
		}
	}
	for _, args := range []any{map[string]any{"project": "demo", "path": "main.go", "bogus": true}, map[string]any{"project": "demo"}, map[string]any{"project": "demo", "path": "main.go", "limit": 0}, map[string]any{"project": "demo", "path": "main.go", "max_bytes": 1}} {
		r, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "get_symbols", Arguments: args})
		if err != nil || !r.IsError {
			t.Fatal("input validation failed")
		}
	}
}
func TestFrameReader(t *testing.T) {
	r := &FrameReader{Reader: strings.NewReader("1234\n1234\n"), MaxBytes: 4}
	b, err := io.ReadAll(r)
	if err != nil || string(b) != "1234\n1234\n" {
		t.Fatal("valid frames rejected")
	}
	r = &FrameReader{Reader: bytes.NewBufferString(strings.Repeat("x", 100)), MaxBytes: 10}
	if _, err = io.ReadAll(r); err == nil {
		t.Fatal("oversized frame accepted")
	}
}

// symlinkOrSkip creates a symlink, skipping the test only when the OS refuses
// the privilege (Windows without Developer Mode or elevation).
func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		if runtime.GOOS == "windows" && strings.Contains(err.Error(), "privilege") {
			t.Skip("symlink creation needs Developer Mode or elevation on Windows: ", err)
		}
		t.Fatal(err)
	}
}
