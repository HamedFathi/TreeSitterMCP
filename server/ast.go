package server

import (
	"context"
	"errors"
	"fmt"
	"github.com/hamedfathi/TreeSitterMCP/internal/catalog"
	"github.com/hamedfathi/TreeSitterMCP/internal/parsers"
	sitter "github.com/tree-sitter/go-tree-sitter"
	pack "github.com/xberg-io/tree-sitter-language-pack/packages/go"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

func installedLanguage(ctx context.Context, name string) (*sitter.Language, error) {
	name = catalog.Canonical(name)
	if name == "" {
		return nil, errors.New("unknown language")
	}
	return parsers.Load(ctx, name)
}
func (e *Engine) languageInfo(ctx context.Context, in Input) (any, error) {
	name := catalog.Canonical(in.Language)
	l, err := installedLanguage(ctx, name)
	if err != nil {
		return nil, err
	}
	summary := map[string]any{"language": name, "pack_version": PackVersion, "abi": l.AbiVersion(), "metadata": l.Metadata(), "node_kind_count": l.NodeKindCount(), "field_count": l.FieldCount(), "parse_states": l.ParseStateCount(), "core_features": "all", "language_specific_extensions": e.opts.EnableLanguageSpecific}
	if in.Section == "" || in.Section == "summary" {
		return summary, nil
	}
	items := []any{}
	switch in.Section {
	case "node_types":
		for i := uint32(0); i < l.NodeKindCount(); i++ {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			id := uint16(i)
			items = append(items, map[string]any{"id": i, "type": l.NodeKindForId(id), "named": l.NodeKindIsNamed(id), "visible": l.NodeKindIsVisible(id), "supertype": l.NodeKindIsSupertype(id)})
		}
	case "fields":
		for i := uint32(1); i <= l.FieldCount(); i++ {
			items = append(items, map[string]any{"id": i, "name": l.FieldNameForId(uint16(i))})
		}
	default:
		return nil, errors.New("section must be summary, node_types, or fields")
	}
	return e.page(items, in, binding("get_language_info", in)+PackVersion, false, "")
}

// Node identifiers contain the exact grammar, file identity, source digest and
// deterministic all-node preorder ordinal. They never expose native pointers.
type nodeIndex struct {
	nodes   []*sitter.Node
	depths  []int
	parents []int
	ends    []int
	fields  []string
	prefix  string
}

func (e *Engine) index(ctx context.Context, in Input, p *parsed) (*nodeIndex, error) {
	x := &nodeIndex{prefix: hashJSON([]string{PackVersion, in.Project, in.Path, p.name, p.digest})}
	stack := []int{}
	err := e.walk(ctx, p.tree.RootNode(), func(n *sitter.Node, d int) bool {
		for len(stack) > d {
			j := stack[len(stack)-1]
			x.ends[j] = len(x.nodes)
			stack = stack[:len(stack)-1]
		}
		parent := -1
		field := ""
		if len(stack) > 0 {
			parent = stack[len(stack)-1]
		}
		// The cursor provides linear traversal; derive fields in one later pass.
		x.nodes = append(x.nodes, n)
		x.depths = append(x.depths, d)
		x.parents = append(x.parents, parent)
		x.ends = append(x.ends, 0)
		x.fields = append(x.fields, field)
		stack = append(stack, len(x.nodes)-1)
		return true
	})
	if err != nil {
		return nil, err
	}
	for _, i := range stack {
		x.ends[i] = len(x.nodes)
	}
	for i, n := range x.nodes {
		if i%256 == 0 && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		j := i + 1
		for c := uint(0); c < n.ChildCount() && j < x.ends[i]; c++ {
			x.fields[j] = n.FieldNameForChild(uint32(c))
			j = x.ends[j]
		}
	}
	return x, nil
}
func (x *nodeIndex) id(i int) string { return x.prefix + ":" + strconv.Itoa(i) }
func (x *nodeIndex) find(id string) (int, error) {
	if id == "" {
		return 0, nil
	}
	parts := strings.Split(id, ":")
	if len(parts) != 2 || parts[0] != x.prefix {
		return 0, errors.New("node_id is stale or belongs to another file/grammar")
	}
	i, err := strconv.Atoi(parts[1])
	if err != nil || i < 0 || i >= len(x.nodes) {
		return 0, errors.New("invalid node_id")
	}
	return i, nil
}
func (x *nodeIndex) info(i int, b []byte) map[string]any {
	n := x.nodes[i]
	m := map[string]any{"node_id": x.id(i), "type": n.Kind(), "named": n.IsNamed(), "extra": n.IsExtra(), "span": span(n), "child_count": n.ChildCount(), "has_error": n.HasError(), "missing": n.IsMissing(), "field": x.fields[i]}
	if x.parents[i] >= 0 {
		m["parent_id"] = x.id(x.parents[i])
	}
	if n.ChildCount() == 0 {
		m["text"] = snippet([]byte(n.Utf8Text(b)), 120)
	}
	return m
}
func (e *Engine) astOperation(ctx context.Context, tool string, in Input) (any, error) {
	r, project, err := e.openProject(in.Project)
	if err != nil {
		return nil, err
	}
	b, err := e.read(r, project, in.Path)
	if err != nil {
		return nil, err
	}
	if tool == "get_file_metadata" {
		st, err := r.Stat(in.Path)
		if err != nil {
			return nil, err
		}
		lines := 0
		if len(b) > 0 {
			lines = 1 + strings.Count(string(b), "\n")
			if b[len(b)-1] == '\n' {
				lines--
			}
		}
		lang := ""
		if l := pack.DetectLanguageFromPath(in.Path); l != nil {
			lang = catalog.Canonical(*l)
		}
		return map[string]any{"path": in.Path, "bytes": len(b), "lines": lines, "modified_ns": st.ModTime().UnixNano(), "sha256": digest(b), "language": lang, "encoding": "UTF-8", "line_ending_crlf": strings.Contains(string(b), "\r\n")}, nil
	}
	p, err := e.parse(ctx, in, b)
	if err != nil {
		return nil, err
	}
	x, err := e.index(ctx, in, p)
	if err != nil {
		return nil, err
	}
	fp := binding(tool, in) + x.prefix
	switch tool {
	case "get_node_at_position":
		offset := in.StartByte
		if in.Line < 0 || in.Column < 0 {
			return nil, errors.New("invalid line or column")
		}
		if in.Line > 0 {
			if in.StartByte != 0 {
				return nil, errors.New("use either line/column or start_byte")
			}
			line := 1
			start := 0
			for line < in.Line && start < len(b) {
				j := strings.IndexByte(string(b[start:]), '\n')
				if j < 0 {
					break
				}
				start += j + 1
				line++
			}
			if line != in.Line {
				return nil, errors.New("line outside source")
			}
			end := start
			for end < len(b) && b[end] != '\n' {
				end++
			}
			if in.Column > end-start {
				return nil, errors.New("column outside line")
			}
			offset = uint(start + in.Column)
		} else if in.Column != 0 {
			return nil, errors.New("column requires line")
		}
		if offset > uint(len(b)) || offset < uint(len(b)) && !utf8.RuneStart(b[offset]) {
			return nil, errors.New("position is outside source or inside a UTF-8 code point")
		}
		found := -1
		for i, n := range x.nodes {
			contains := n.StartByte() <= offset && offset < n.EndByte()
			if offset == uint(len(b)) {
				contains = n.StartByte() <= offset && n.EndByte() == offset
			}
			if contains && (found < 0 || x.depths[i] > x.depths[found]) {
				found = i
			}
		}
		if found < 0 {
			return nil, errors.New("no syntax node at position")
		}
		return x.info(found, b), nil
	case "navigate_ast":
		i, err := x.find(in.NodeID)
		if err != nil {
			return nil, err
		}
		rel := in.Relation
		if rel == "" {
			rel = "self"
		}
		ids := []int{}
		depth := in.Depth
		if depth == 0 {
			depth = 3
		}
		if depth < 1 || depth > 32 {
			return nil, errors.New("depth must be 1-32")
		}
		switch rel {
		case "self":
			ids = append(ids, i)
		case "parent":
			if x.parents[i] >= 0 {
				ids = append(ids, x.parents[i])
			}
		case "children":
			for j := i + 1; j < x.ends[i]; j = x.ends[j] {
				ids = append(ids, j)
			}
		case "siblings":
			if par := x.parents[i]; par >= 0 {
				for j := par + 1; j < x.ends[par]; j = x.ends[j] {
					if j != i {
						ids = append(ids, j)
					}
				}
			}
		case "ancestors":
			for j := x.parents[i]; j >= 0; j = x.parents[j] {
				ids = append(ids, j)
			}
		case "subtree":
			for j := i; j < x.ends[i]; j++ {
				if x.depths[j]-x.depths[i] <= depth {
					ids = append(ids, j)
				}
			}
		default:
			return nil, errors.New("unknown relation")
		}
		items := []any{}
		for _, j := range ids {
			m := x.info(j, b)
			m["depth"] = x.depths[j] - x.depths[i]
			items = append(items, m)
		}
		return e.page(items, in, fp, false, "")
	case "get_parse_diagnostics":
		items := []any{}
		for i, n := range x.nodes {
			if n.IsError() || n.IsMissing() {
				m := x.info(i, b)
				if n.IsMissing() {
					m["diagnostic"] = "missing"
				} else {
					m["diagnostic"] = "error"
				}
				items = append(items, m)
			}
		}
		return e.page(items, in, fp, false, "Syntax recovery diagnostics only; no type checking or code execution.")
	case "get_ast_metrics":
		histogram := map[string]int{}
		named, leaves, depth, errs, missing := 0, 0, 0, 0, 0
		for i, n := range x.nodes {
			histogram[n.Kind()]++
			if n.IsNamed() {
				named++
			}
			if n.ChildCount() == 0 {
				leaves++
			}
			if x.depths[i] > depth {
				depth = x.depths[i]
			}
			if n.IsError() {
				errs++
			}
			if n.IsMissing() {
				missing++
			}
		}
		keys := []string{}
		for k := range histogram {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		items := []any{}
		for _, k := range keys {
			items = append(items, map[string]any{"type": k, "count": histogram[k]})
		}
		histInput := in
		histInput.MaxBytes = e.budget(in) - 320
		pg, err := e.page(items, histInput, fp, false, "")
		if err != nil {
			return nil, err
		}
		out := map[string]any{"nodes": len(x.nodes), "named_nodes": named, "anonymous_nodes": len(x.nodes) - named, "leaves": leaves, "max_depth": depth, "errors": errs, "missing": missing, "histogram": pg}
		if !fits(out, e.budget(in)) {
			return nil, errors.New("metrics exceed budget; reduce limit")
		}
		return out, nil
	case "create_ast_snapshot":
		return e.createSnapshot(ctx, in, p, x)
	case "find_similar_subtrees":
		return e.similar(ctx, in, p, x)
	}
	return nil, fmt.Errorf("unknown AST operation %s", tool)
}
