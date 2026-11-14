package server

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/hamedfathi/TreeSitterMCP/internal/catalog"
	"github.com/hamedfathi/TreeSitterMCP/internal/parsers"
	sitter "github.com/tree-sitter/go-tree-sitter"
	pack "github.com/xberg-io/tree-sitter-language-pack/packages/go"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (e *Engine) parse(ctx context.Context, in Input, b []byte) (*parsed, error) {
	name := in.Language
	if name == "" {
		if n := pack.DetectLanguageFromPath(in.Path); n != nil {
			name = *n
		} else if n = pack.DetectLanguageFromContent(string(b)); n != nil {
			name = *n
		}
	}
	name = catalog.Canonical(name)
	if name == "" {
		return nil, errors.New("language could not be detected; supply language explicitly")
	}
	if len(name) > 64 || strings.ContainsAny(name, "/\\\x00") {
		return nil, errors.New("invalid language")
	}
	key := in.Project + "\x00" + in.Path + "\x00" + name
	d := digest(b)
	e.clock++
	if p, ok := e.trees[key]; ok && p.digest == d {
		p.touched = time.Now()
		p.used = e.clock
		e.hits++
		return p, nil
	}
	lang, err := parsers.Load(ctx, name)
	if err != nil {
		return nil, err
	}
	parser := sitter.NewParser()
	defer parser.Close()
	if err = parser.SetLanguage(lang); err != nil {
		return nil, err
	}
	tree := parser.ParseWithOptions(func(offset int, _ sitter.Point) []byte {
		if offset >= len(b) {
			return nil
		}
		return b[offset:]
	}, nil, &sitter.ParseOptions{ProgressCallback: func(sitter.ParseState) bool { return ctx.Err() != nil }})
	if tree == nil {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		return nil, errors.New("parser returned no tree")
	}
	if tree.RootNode().DescendantCount() > uint(e.opts.MaxNodes) {
		tree.Close()
		return nil, errors.New("syntax tree exceeds max_nodes")
	}
	if old := e.trees[key]; old != nil {
		old.tree.Close()
		e.cachedBytes -= int64(len(old.source))
		delete(e.trees, key)
	}
	for len(e.trees) >= e.opts.MaxCacheEntries || e.cachedBytes+int64(len(b)) > e.opts.MaxCacheBytes {
		oldest := ""
		stamp := ^uint64(0)
		for k, p := range e.trees {
			if p.used < stamp {
				oldest = k
				stamp = p.used
			}
		}
		if oldest == "" {
			break
		}
		p := e.trees[oldest]
		e.cachedBytes -= int64(len(p.source))
		p.tree.Close()
		delete(e.trees, oldest)
	}
	out := &parsed{source: b, tree: tree, language: lang, name: name, digest: d, used: e.clock, touched: time.Now()}
	e.trees[key] = out
	e.cachedBytes += int64(len(b))
	e.misses++
	return out, nil
}
func (e *Engine) walk(ctx context.Context, root *sitter.Node, fn func(*sitter.Node, int) bool) error {
	c := root.Walk()
	defer c.Close()
	count := 0
	for {
		count++
		if count > e.opts.MaxNodes {
			return errors.New("traversal exceeds max_nodes")
		}
		if count%256 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		descend := fn(c.Node(), int(c.Depth()))
		if descend && c.GotoFirstChild() {
			continue
		}
		for !c.GotoNextSibling() {
			if !c.GotoParent() {
				return ctx.Err()
			}
		}
	}
}
func span(n *sitter.Node) Span {
	a, b := n.StartPosition(), n.EndPosition()
	return Span{n.StartByte(), n.EndByte(), a.Row + 1, b.Row + 1, a.Column, b.Column}
}
func snippet(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	for n > 0 && b[n]&0xc0 == 0x80 {
		n--
	}
	return string(b[:n]) + "..."
}
func symbolKind(k string) string {
	switch {
	case strings.Contains(k, "function") && !strings.Contains(k, "call") && !strings.Contains(k, "type") && !strings.Contains(k, "body") && !strings.Contains(k, "parameters"):
		return "function"
	case strings.Contains(k, "method") && !strings.Contains(k, "invocation") && !strings.Contains(k, "call") && !strings.Contains(k, "body"):
		return "method"
	case k == "class_definition" || k == "class_declaration" || k == "class_specifier":
		return "class"
	case k == "interface_declaration" || k == "interface_definition":
		return "interface"
	case k == "struct_item" || k == "struct_specifier" || k == "struct_declaration":
		return "struct"
	case k == "enum_item" || k == "enum_declaration" || k == "enum_specifier":
		return "enum"
	case k == "type_spec" || k == "type_alias_declaration" || k == "type_definition":
		return "type"
	case k == "module_definition" || k == "module_declaration":
		return "module"
	}
	return ""
}
func nodeName(n *sitter.Node, b []byte) string {
	if n.Kind() == "function_definition" {
		for i := uint(0); i < n.NamedChildCount(); i++ {
			x := n.NamedChild(i)
			if x.Kind() == "signature" && x.NamedChildCount() > 0 {
				x = x.NamedChild(0)
				if x.Kind() == "call_expression" && x.NamedChildCount() > 0 {
					return snippet([]byte(x.NamedChild(0).Utf8Text(b)), 120)
				}
			}
		}
	}
	for _, field := range []string{"name", "declarator", "left"} {
		if x := n.ChildByFieldName(field); x != nil {
			for i := 0; i < 8; i++ {
				inner := x.ChildByFieldName("declarator")
				if inner == nil {
					break
				}
				x = inner
			}
			return snippet([]byte(x.Utf8Text(b)), 120)
		}
	}
	for i := uint(0); i < n.NamedChildCount() && i < 12; i++ {
		x := n.NamedChild(i)
		switch x.Kind() {
		case "identifier", "simple_identifier", "type_identifier", "constant", "operator", "name":
			return snippet([]byte(x.Utf8Text(b)), 120)
		}
	}
	return ""
}

type derived struct {
	Version      int          `json:"version"`
	Symbols      []Symbol     `json:"symbols"`
	Dependencies []Dependency `json:"dependencies"`
}

func (e *Engine) derive(ctx context.Context, in Input, p *parsed) (derived, error) {
	key := digest([]byte("v1:" + p.name + ":" + p.digest))
	dir := e.derivedDir(in)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return derived{}, err
	}
	file := filepath.Join(dir, key+".json")
	if st, err := os.Stat(file); err == nil && st.Size() <= 8<<20 && time.Since(st.ModTime()) < e.opts.CacheTTL {
		if b, err := os.ReadFile(file); err == nil {
			var d derived
			if json.Unmarshal(b, &d) == nil && d.Version == 1 {
				return d, nil
			}
		}
	}
	out := derived{Version: 1, Symbols: []Symbol{}, Dependencies: []Dependency{}}
	err := e.walk(ctx, p.tree.RootNode(), func(n *sitter.Node, depth int) bool {
		if !n.IsNamed() {
			return false
		}
		if kind := symbolKind(n.Kind()); kind != "" {
			if name := nodeName(n, p.source); name != "" {
				end := n.EndByte()
				if body := n.ChildByFieldName("body"); body != nil {
					end = body.StartByte()
				}
				sig := strings.TrimSpace(string(p.source[n.StartByte():end]))
				out.Symbols = append(out.Symbols, Symbol{name, kind, n.Kind(), span(n), snippet([]byte(sig), 256)})
			}
		}
		out.Dependencies = append(out.Dependencies, extractDependencies(in.Path, p.name, n, p.source)...)
		return true
	})
	if err != nil {
		return out, err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return out, err
	}
	b, err := json.Marshal(out)
	if err != nil {
		return out, err
	}
	if err = atomicWrite(file, b); err != nil {
		return out, err
	}
	e.pruneDisk("derived")
	return out, nil
}
