package server

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/hamedfathi/TreeSitterMCP/projecttree"
	pack "github.com/xberg-io/tree-sitter-language-pack/packages/go"
	"path/filepath"
	"regexp"
)

type TreeResult struct {
	Project         string              `json:"project"`
	Root            string              `json:"root"`
	Stats           projecttree.Stats   `json:"stats"`
	Directory       *projecttree.Entry  `json:"directory,omitempty"`
	Languages       map[string]int      `json:"languages,omitempty"`
	Extensions      map[string]int      `json:"extensions,omitempty"`
	Entries         []projecttree.Entry `json:"entries"`
	Markdown        string              `json:"markdown,omitempty"`
	NextCursor      string              `json:"next_cursor,omitempty"`
	Truncated       bool                `json:"truncated"`
	Incomplete      bool                `json:"incomplete"`
	SkippedSymlinks int                 `json:"skipped_symlinks"`
	Unreadable      int                 `json:"unreadable"`
	Note            string              `json:"note"`
}

func (e *Engine) scan(ctx context.Context, in Input, includeHidden bool) (projecttree.Snapshot, Project, error) {
	r, p, err := e.openProject(in.Project)
	if err != nil {
		return projecttree.Snapshot{}, p, err
	}
	root := in.Path
	if root == "" {
		root = "."
	}
	if !projecttree.ValidRoot(root) {
		return projecttree.Snapshot{}, p, errors.New("invalid project-relative directory")
	}
	if err = verifyPath(r, root); err != nil {
		return projecttree.Snapshot{}, p, err
	}
	snapshot, err := projecttree.Scan(ctx, r.FS(), projecttree.Options{Root: root, IncludeHidden: includeHidden, MaxEntries: e.opts.MaxFiles * 4, MaxFiles: e.opts.MaxFiles, Exclude: func(path string) bool {
		return inside(e.opts.StateDir, filepath.Join(p.Root, filepath.FromSlash(path)))
	}, DetectLanguage: func(path string) string {
		if n := pack.DetectLanguageFromPath(path); n != nil {
			return *n
		}
		return ""
	}})
	if len(snapshot.Entries) > 0 && root == "." {
		snapshot.Entries[0].Name = p.Name
	}
	return snapshot, p, err
}
func (e *Engine) treeOperation(ctx context.Context, tool string, in Input) (any, error) {
	depth := in.Depth
	if depth == 0 {
		depth = 3
		if tool == "find_paths" {
			depth = 32
		}
	}
	if depth < 1 || depth > 32 {
		return nil, errors.New("depth must be 1-32")
	}
	var expression *regexp.Regexp
	if tool == "find_paths" {
		if in.Regex {
			if len(in.Pattern) == 0 || len(in.Pattern) > 4096 {
				return nil, errors.New("pattern must contain 1-4096 bytes")
			}
			var err error
			expression, err = regexp.Compile(in.Pattern)
			if err != nil {
				return nil, err
			}
		} else {
			if _, err := projecttree.Match(in.Pattern, "probe"); err != nil {
				return nil, err
			}
		}
	}
	snapshot, p, err := e.scan(ctx, in, in.IncludeHidden)
	if err != nil {
		return nil, err
	}
	entries := snapshot.Select(depth, in.FoldersOnly, in.Extensions)
	if tool == "get_directory_info" {
		entries = []projecttree.Entry{}
		for _, entry := range snapshot.Select(1, in.FoldersOnly, in.Extensions) {
			if entry.Depth == 1 {
				entries = append(entries, entry)
			}
		}
	}
	if tool == "find_paths" {
		matches := []projecttree.Entry{}
		for _, entry := range entries {
			if err = ctx.Err(); err != nil {
				return nil, err
			}
			matched := false
			if expression != nil {
				matched = expression.MatchString(entry.Path)
			} else {
				matched, err = projecttree.Match(in.Pattern, entry.Path)
				if err != nil {
					return nil, err
				}
			}
			if matched {
				matches = append(matches, entry)
			}
		}
		entries = matches
	}
	fp := binding(tool, in) + snapshot.Fingerprint
	start, err := cursorOffset(in.Cursor, fp)
	if err != nil {
		return nil, err
	}
	if start > len(entries) {
		return nil, errors.New("cursor offset is out of range")
	}
	limit := in.Limit
	if limit == 0 {
		limit = 50
	}
	out := TreeResult{Project: p.Name, Root: snapshot.Root, Stats: snapshot.Stats, Entries: []projecttree.Entry{}, Incomplete: snapshot.Incomplete, SkippedSymlinks: snapshot.SkippedSymlinks, Unreadable: snapshot.Unreadable, Note: "Folders-first depth-first view. Stats cover the admitted subtree before display filters; incomplete stats are lower bounds. Empty means no admitted children. Symlinks are never followed."}
	if tool == "get_directory_info" {
		out.Directory = &snapshot.Entries[0]
		out.Languages = snapshot.Languages
		out.Extensions = snapshot.Extensions
	}
	next := start
	for next < len(entries) && len(out.Entries) < limit {
		out.Entries = append(out.Entries, entries[next])
		out.Truncated = next+1 < len(entries)
		if out.Truncated {
			out.NextCursor = nextToken(next+1, fp)
		} else {
			out.NextCursor = ""
		}
		if tool == "get_project_tree" {
			out.Markdown = projecttree.Markdown(out.Entries)
		}
		b, _ := json.Marshal(out)
		if len(b) > e.budget(in) {
			out.Entries = out.Entries[:len(out.Entries)-1]
			if len(out.Entries) == 0 {
				return nil, errors.New("directory metadata or one entry exceeds budget; increase max_bytes or choose a smaller subtree")
			}
			break
		}
		next++
	}
	out.Truncated = next < len(entries)
	if out.Truncated {
		out.NextCursor = nextToken(next, fp)
	} else {
		out.NextCursor = ""
	}
	if tool == "get_project_tree" {
		out.Markdown = projecttree.Markdown(out.Entries)
	}
	b, _ := json.Marshal(out)
	if len(b) > e.budget(in) {
		return nil, errors.New("directory metadata exceeds response budget; choose a smaller subtree")
	}
	return out, nil
}
