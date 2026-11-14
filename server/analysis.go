package server

import (
	"context"
	"encoding/json"
	"errors"
	sitter "github.com/tree-sitter/go-tree-sitter"
	"strings"
	"unicode/utf8"
)

func (e *Engine) fileOperation(ctx context.Context, tool string, in Input) (any, error) {
	r, project, err := e.openProject(in.Project)
	if err != nil {
		return nil, err
	}
	b, err := e.read(r, project, in.Path)
	if err != nil {
		return nil, err
	}
	if tool == "read_file" {
		return e.readPage(in, b)
	}
	p, err := e.parse(ctx, in, b)
	if err != nil {
		return nil, err
	}
	fp := binding(tool, in) + p.digest + p.name
	switch tool {
	case "get_symbols":
		d, err := e.derive(ctx, in, p)
		if err != nil {
			return nil, err
		}
		items := []any{}
		for _, s := range d.Symbols {
			if in.Name == "" || strings.Contains(s.Name, in.Name) {
				items = append(items, s)
			}
		}
		return e.page(items, in, fp, false, "AST definitions; coverage varies by grammar. Use query_ast for grammar-specific extraction.")
	case "get_dependencies":
		d, err := e.derive(ctx, in, p)
		if err != nil {
			return nil, err
		}
		items := []any{}
		for _, dep := range d.Dependencies {
			dep.ResolvedPath = e.resolveDependency(r, project, in.Path, p.name, dep.Target)
			items = append(items, dep)
		}
		return e.page(items, in, fp, false, "Syntactic imports/includes, not a type-resolved call graph. Missing resolved_path means external or unresolved.")
	case "get_ast":
		idx, err := e.index(ctx, in, p)
		if err != nil {
			return nil, err
		}
		nodeIDs := map[uintptr]string{}
		for i, n := range idx.nodes {
			nodeIDs[n.Id()] = idx.id(i)
		}
		depth := in.Depth
		if depth == 0 {
			depth = 3
		}
		if depth < 1 || depth > 32 {
			return nil, errors.New("depth must be 1-32")
		}
		root := p.tree.RootNode()
		if in.StartByte > uint(len(b)) || in.EndByte > uint(len(b)) || in.EndByte > 0 && in.EndByte < in.StartByte {
			return nil, errors.New("invalid byte range")
		}
		if in.StartByte > 0 || in.EndByte > 0 {
			end := in.EndByte
			if end == 0 {
				end = uint(len(b))
			}
			root = root.NamedDescendantForByteRange(in.StartByte, end)
			if root == nil {
				return nil, errors.New("no node at byte range")
			}
		}
		items := []any{}
		err = e.walk(ctx, root, func(n *sitter.Node, d int) bool {
			if !n.IsNamed() {
				return false
			}
			if d <= depth {
				item := map[string]any{"node_id": nodeIDs[n.Id()], "type": n.Kind(), "depth": d, "span": span(n), "has_error": n.HasError(), "missing": n.IsMissing()}
				if n.NamedChildCount() == 0 {
					item["text"] = snippet([]byte(n.Utf8Text(b)), 120)
				}
				items = append(items, item)
			}
			return d < depth
		})
		if err != nil {
			return nil, err
		}
		return e.page(items, in, fp, false, "Flat depth-first AST. Lines are one-based; columns and offsets use zero-based UTF-8 bytes.")
	case "query_ast":
		return e.query(ctx, in, p, fp)
	}
	return nil, errors.New("unknown file operation")
}
func (e *Engine) readPage(in Input, b []byte) (any, error) {
	if in.StartLine < 0 || in.EndLine < 0 {
		return nil, errors.New("line numbers must be positive")
	}
	starts := []int{0}
	for i, c := range b {
		if c == '\n' && i+1 < len(b) {
			starts = append(starts, i+1)
		}
	}
	start := in.StartLine
	if start == 0 {
		start = 1
	}
	if start > len(starts) {
		return nil, errors.New("start_line is beyond the file")
	}
	end := in.EndLine
	if end == 0 {
		end = start + 199
	}
	if end < start {
		return nil, errors.New("end_line precedes start_line")
	}
	if end > len(starts) {
		end = len(starts)
	}
	from, to := starts[start-1], len(b)
	if end < len(starts) {
		to = starts[end]
	}
	fp := binding("read_file", in) + digest(b)
	offset, err := cursorOffset(in.Cursor, fp)
	if err != nil {
		return nil, err
	}
	from += offset
	if from > to {
		return nil, errors.New("invalid cursor offset")
	}
	stop := from + e.budget(in) - 700
	if stop > to {
		stop = to
	}
	for stop > from && stop < len(b) && !utf8.RuneStart(b[stop]) {
		stop--
	}
	result := map[string]any{"path": in.Path, "start_byte": from, "total_lines": len(starts)}
	for {
		result["end_byte"] = stop
		result["content"] = string(b[from:stop])
		result["truncated"] = stop < to
		if stop < to {
			result["next_cursor"] = nextToken(stop-starts[start-1], fp)
		} else {
			delete(result, "next_cursor")
		}
		encoded, _ := json.Marshal(result)
		if len(encoded) <= e.budget(in) {
			return result, nil
		}
		stop = from + (stop-from)*3/4
		for stop > from && stop < len(b) && !utf8.RuneStart(b[stop]) {
			stop--
		}
		if stop == from {
			return nil, errors.New("response budget is too small")
		}
	}
}
func (e *Engine) query(ctx context.Context, in Input, p *parsed, fp string) (any, error) {
	if in.Query == "" || len(in.Query) > 8192 {
		return nil, errors.New("query must contain 1-8192 bytes")
	}
	q, qe := sitter.NewQuery(p.language, in.Query)
	if qe != nil {
		return nil, qe
	}
	defer q.Close()
	if q.PatternCount() > 256 || len(q.CaptureNames()) > 128 {
		return nil, errors.New("query exceeds complexity limits")
	}
	for i := uint(0); i < q.PatternCount(); i++ {
		if len(q.GeneralPredicates(i)) > 0 {
			return nil, errors.New("unsupported query predicate")
		}
	}
	c := sitter.NewQueryCursor()
	defer c.Close()
	c.SetMatchLimit(1024)
	// Do not use MatchesWithOptions: go-tree-sitter v0.25.0 hands C a pointer
	// to a Go-allocated TSQueryCursorOptions that ts_query_cursor_exec_with_options
	// retains and dereferences on every later next_match call. Once the Go
	// object moves or is collected the progress callback becomes a wild
	// function pointer (SIGBUS/SIGSEGV, seen on linux-arm64). Cancellation is
	// checked between matches instead, and the match limit bounds the work.
	matches := c.Matches(q, p.tree.RootNode(), p.source)
	items := []any{}
	incomplete := false
	matchIndex := 0
outer:
	for {
		m := matches.Next()
		if m == nil {
			break
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for _, capture := range m.Captures {
			n := capture.Node
			items = append(items, map[string]any{"match": matchIndex, "pattern": m.PatternIndex, "capture": q.CaptureNames()[capture.Index], "type": n.Kind(), "span": span(&n), "text": snippet([]byte(n.Utf8Text(p.source)), 512)})
			if len(items) >= 10000 {
				incomplete = true
				break outer
			}
		}
		matchIndex++
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.DidExceedMatchLimit() {
		incomplete = true
	}
	return e.page(items, in, fp, incomplete, "Captures are capped at 10,000; text previews at 512 bytes. Narrow the query if incomplete.")
}
