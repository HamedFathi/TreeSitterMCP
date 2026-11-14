package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/hamedfathi/TreeSitterMCP/internal/catalog"
	"github.com/hamedfathi/TreeSitterMCP/internal/parsers"
	"github.com/hamedfathi/TreeSitterMCP/projecttree"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

type Engine struct {
	mu                  sync.Mutex
	gate                chan struct{}
	opts                Options
	projects            map[string]Project
	roots               map[string]*os.Root
	allow               []*os.Root
	trees               map[string]*parsed
	clock, hits, misses uint64
	cachedBytes         int64
	closed              bool
}
type parsed struct {
	source       []byte
	tree         *sitter.Tree
	language     *sitter.Language
	name, digest string
	used         uint64
	touched      time.Time
}
type diskState struct {
	Version  int                `json:"version"`
	Projects map[string]Project `json:"projects"`
}

var projectName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func New(opts Options) (*Engine, error) {
	opts.defaults()
	if len(opts.AllowRoots) == 0 {
		return nil, errors.New("at least one allow root is required")
	}
	if opts.StateDir == "" || opts.MaxFileBytes < 1 || opts.MaxCacheBytes < opts.MaxFileBytes || opts.MaxCacheEntries < 1 || opts.MaxResponseBytes < 2048 || opts.MaxFiles < 1 || opts.MaxNodes < 100 || opts.CacheTTL < time.Millisecond || opts.OperationTimeout < time.Millisecond {
		return nil, errors.New("invalid state directory or resource limits")
	}
	opts.AllowRoots = append([]string{}, opts.AllowRoots...)
	for i, p := range opts.AllowRoots {
		if p == "" {
			return nil, errors.New("allow roots must not be empty")
		}
		r, err := canonicalDir(p)
		if err != nil {
			return nil, err
		}
		opts.AllowRoots[i] = r
	}
	state, err := filepath.Abs(opts.StateDir)
	if err != nil {
		return nil, err
	}
	opts.StateDir = state
	if err = os.MkdirAll(state, 0700); err != nil {
		return nil, err
	}
	st, err := os.Lstat(state)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("state directory must be a real directory")
	}
	if err = os.Chmod(state, 0700); err != nil {
		return nil, err
	}
	e := &Engine{opts: opts, gate: make(chan struct{}, 1), projects: map[string]Project{}, roots: map[string]*os.Root{}, trees: map[string]*parsed{}}
	success := false
	defer func() {
		if !success {
			e.Close()
		}
	}()
	for _, p := range opts.AllowRoots {
		r, err := os.OpenRoot(p)
		if err != nil {
			return nil, err
		}
		e.allow = append(e.allow, r)
	}
	file, err := os.Open(filepath.Join(state, "projects.json"))
	if err == nil {
		b, readErr := io.ReadAll(io.LimitReader(file, (1<<20)+1))
		file.Close()
		if readErr != nil {
			return nil, readErr
		}
		if len(b) > 1<<20 {
			return nil, errors.New("registry is too large")
		}
		var d diskState
		if err = json.Unmarshal(b, &d); err != nil {
			return nil, fmt.Errorf("invalid registry: %w", err)
		}
		if d.Version != 1 {
			return nil, errors.New("unsupported registry version")
		}
		for name, p := range d.Projects {
			if !projectName.MatchString(name) || name != p.Name {
				return nil, errors.New("invalid registry project")
			}
			if !e.allowed(p.Root) {
				continue
			}
			r, err := e.anchor(p.Root)
			if err != nil {
				return nil, err
			}
			e.projects[name] = p
			e.roots[name] = r
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	success = true
	return e, nil
}
func canonicalDir(p string) (string, error) {
	a, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	a, err = filepath.EvalSymlinks(a)
	if err != nil {
		return "", err
	}
	st, err := os.Stat(a)
	if err != nil {
		return "", err
	}
	if !st.IsDir() {
		return "", errors.New("path must be a directory")
	}
	return filepath.Clean(a), nil
}
func inside(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
func (e *Engine) allowed(p string) bool {
	if !filepath.IsAbs(p) {
		return false
	}
	for _, a := range e.opts.AllowRoots {
		if inside(a, p) {
			return true
		}
	}
	return false
}
func (e *Engine) anchor(p string) (*os.Root, error) {
	for i, a := range e.opts.AllowRoots {
		if inside(a, p) {
			rel, err := filepath.Rel(a, p)
			if err != nil {
				return nil, err
			}
			return e.allow[i].OpenRoot(rel)
		}
	}
	return nil, errors.New("project is outside allow roots")
}
func (e *Engine) openProject(name string) (*os.Root, Project, error) {
	p, ok := e.projects[name]
	if !ok {
		return nil, p, errors.New("unknown project")
	}
	return e.roots[name], p, nil
}
func (e *Engine) save() error {
	b, err := json.MarshalIndent(diskState{1, e.projects}, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(e.opts.StateDir, "projects.json"), b)
}
func atomicWrite(path string, b []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), "write-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	cerr := f.Close()
	if err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
func (e *Engine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil
	}
	e.closed = true
	for _, p := range e.trees {
		p.tree.Close()
	}
	for _, r := range e.roots {
		r.Close()
	}
	for _, r := range e.allow {
		r.Close()
	}
	return nil
}

// parserFree lists tools that never load a grammar, so they never wait for a
// parser install.
var parserFree = map[string]bool{"list_languages": true, "register_project": true, "list_projects": true, "unregister_project": true, "list_files": true, "read_file": true, "search_text": true, "cache_stats": true, "clear_cache": true, "get_project_tree": true, "get_directory_info": true, "find_paths": true, "project_overview": true}

// installWait bounds how long a tool call waits for a first-run parser install.
const installWait = 10 * time.Minute

func (e *Engine) Execute(ctx context.Context, tool string, in Input) (any, error) {
	if !parserFree[tool] {
		// Wait outside the operation timeout and engine lock so a first-run
		// install never fails a call or blocks unrelated tools. Errors are
		// reported per grammar by parsers.Load.
		wctx, wcancel := context.WithTimeout(ctx, installWait)
		_ = parsers.Wait(wctx)
		wcancel()
	}
	ctx, cancel := context.WithTimeout(ctx, e.opts.OperationTimeout)
	defer cancel()
	select {
	case e.gate <- struct{}{}:
		defer func() { <-e.gate }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, errors.New("engine is closed")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if in.Limit < 0 || in.Limit > 200 {
		return nil, errors.New("limit must be 1-200 or omitted")
	}
	if in.MaxBytes < 0 || in.MaxBytes > e.opts.MaxResponseBytes || in.MaxBytes > 0 && in.MaxBytes < 1024 {
		return nil, errors.New("max_bytes must be 1024 through the configured response limit")
	}
	if len(in.Extensions) > 32 {
		return nil, errors.New("at most 32 extension filters are allowed")
	}
	for _, ext := range in.Extensions {
		if len(ext) > 32 || strings.ContainsAny(ext, "/\\\x00") {
			return nil, errors.New("invalid extension filter")
		}
	}
	e.expireCache()
	switch tool {
	case "get_language_info":
		return e.languageInfo(ctx, in)
	case "query_project":
		return e.queryProject(ctx, in)
	case "compare_ast_snapshots":
		return e.compareSnapshots(ctx, in)
	case "get_file_metadata", "get_node_at_position", "navigate_ast", "get_parse_diagnostics", "get_ast_metrics", "create_ast_snapshot", "find_similar_subtrees":
		return e.astOperation(ctx, tool, in)
	case "list_languages":
		items := []any{}
		for _, n := range catalog.Names() {
			items = append(items, n)
		}
		return e.page(items, in, "languages:"+PackVersion, false, "")
	case "register_project":
		if !projectName.MatchString(in.Project) || in.Root == "" {
			return nil, errors.New("valid project and root are required")
		}
		if _, exists := e.projects[in.Project]; exists {
			return nil, errors.New("project already exists; unregister it first")
		}
		root, err := canonicalDir(in.Root)
		if err != nil {
			return nil, err
		}
		r, err := e.anchor(root)
		if err != nil {
			return nil, err
		}
		p := Project{in.Project, root, time.Now().UTC()}
		e.projects[in.Project] = p
		if err = e.save(); err != nil {
			delete(e.projects, in.Project)
			r.Close()
			return nil, err
		}
		e.roots[in.Project] = r
		return p, nil
	case "list_projects":
		keys := []string{}
		for k := range e.projects {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		items := []any{}
		for _, k := range keys {
			items = append(items, e.projects[k])
		}
		return e.page(items, in, "projects:"+hashJSON(items), false, "")
	case "unregister_project":
		p, ok := e.projects[in.Project]
		if !ok {
			return nil, errors.New("unknown project")
		}
		delete(e.projects, in.Project)
		if err := e.save(); err != nil {
			e.projects[in.Project] = p
			return nil, err
		}
		e.roots[in.Project].Close()
		delete(e.roots, in.Project)
		e.clearProject(in.Project)
		for _, kind := range []string{"derived", "snapshots"} {
			if err := os.RemoveAll(filepath.Join(e.opts.StateDir, kind, digest([]byte(in.Project)))); err != nil {
				return nil, err
			}
		}
		return map[string]any{"removed": in.Project}, nil
	case "cache_stats":
		return e.cacheStats(), nil
	case "clear_cache":
		return e.clearCache(in)
	case "get_project_tree", "get_directory_info", "find_paths":
		return e.treeOperation(ctx, tool, in)
	case "list_files", "project_overview", "search_text":
		return e.projectOperation(ctx, tool, in)
	case "read_file", "get_ast", "get_symbols", "query_ast", "get_dependencies":
		if (tool == "get_symbols" || tool == "get_dependencies") && !e.opts.EnableLanguageSpecific {
			return nil, errors.New("language-specific extensions are disabled; use query_ast with the grammar vocabulary")
		}
		return e.fileOperation(ctx, tool, in)
	default:
		return nil, errors.New("unknown tool")
	}
}
func blocked(p string) bool { return projecttree.DefaultExclude(p) }
func cleanPath(p string) (string, error) {
	if p == "" || !fs.ValidPath(p) || p == "." || strings.ContainsAny(p, "\\\x00") {
		return "", errors.New("path must be project-relative and slash-separated")
	}
	if blocked(p) {
		return "", errors.New("path is excluded by policy")
	}
	return p, nil
}
func verifyPath(r *os.Root, p string) error {
	prefix := ""
	for _, part := range strings.Split(p, "/") {
		if part == "." {
			continue
		}
		if prefix != "" {
			prefix += "/"
		}
		prefix += part
		st, err := r.Lstat(prefix)
		if err != nil {
			return err
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return errors.New("symbolic links are excluded")
		}
	}
	return nil
}
func (e *Engine) read(r *os.Root, p Project, name string) ([]byte, error) {
	name, err := cleanPath(name)
	if err != nil {
		return nil, err
	}
	if inside(e.opts.StateDir, filepath.Join(p.Root, filepath.FromSlash(name))) {
		return nil, errors.New("state files are excluded")
	}
	if err = verifyPath(r, name); err != nil {
		return nil, err
	}
	st, err := r.Stat(name)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > e.opts.MaxFileBytes {
		return nil, errors.New("file is not regular or exceeds size limit")
	}
	f, err := openSource(r, name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err = f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, errors.New("file is not regular")
	}
	// Reject binary content from the first block instead of reading the
	// whole file first.
	head := make([]byte, 8192)
	n, err := io.ReadFull(f, head)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, err
	}
	head = head[:n]
	if bytes.IndexByte(head, 0) >= 0 {
		return nil, errors.New("file is not UTF-8 text")
	}
	rest, err := io.ReadAll(io.LimitReader(f, e.opts.MaxFileBytes+1-int64(n)))
	if err != nil {
		return nil, err
	}
	b := append(head, rest...)
	if int64(len(b)) > e.opts.MaxFileBytes {
		return nil, errors.New("file exceeds size limit")
	}
	if !utf8.Valid(b) || strings.IndexByte(string(b), 0) >= 0 {
		return nil, errors.New("file is not UTF-8 text")
	}
	return b, nil
}
func digest(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
func hashJSON(v any) string  { b, _ := json.Marshal(v); return digest(b) }
func (e *Engine) clearProject(name string) {
	for k, p := range e.trees {
		if strings.HasPrefix(k, name+"\x00") {
			p.tree.Close()
			e.cachedBytes -= int64(len(p.source))
			delete(e.trees, k)
		}
	}
}
