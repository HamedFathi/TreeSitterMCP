// Package projecttree scans project folders without reading source contents or
// following symbolic links. It is independent of MCP and Tree-sitter.
package projecttree

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"path"
	"sort"
	"strings"
	"unicode/utf8"
)

// Options are scan policy. Counts apply to the complete admitted subtree, even
// when a later presentation limits depth, filters files, or returns one page.
type Options struct {
	Root           string
	IncludeHidden  bool
	MaxEntries     int
	MaxFiles       int
	Exclude        func(string) bool
	DetectLanguage func(string) string
}
type Stats struct {
	Files       int   `json:"files"`
	Directories int   `json:"directories"`
	Bytes       int64 `json:"bytes"`
	Complete    bool  `json:"complete"`
}
type Entry struct {
	Path       string `json:"path"`
	Name       string `json:"name"`
	Parent     string `json:"parent,omitempty"`
	Kind       string `json:"kind"`
	Depth      int    `json:"depth"`
	Bytes      int64  `json:"bytes,omitempty"`
	Language   string `json:"language,omitempty"`
	ModifiedNS int64  `json:"modified_ns"`
	Children   int    `json:"children,omitempty"`
	Empty      bool   `json:"empty,omitempty"`
	Stats      *Stats `json:"stats,omitempty"`
}
type Snapshot struct {
	Root            string         `json:"root"`
	Entries         []Entry        `json:"entries"`
	Stats           Stats          `json:"stats"`
	Languages       map[string]int `json:"languages"`
	Extensions      map[string]int `json:"extensions"`
	Incomplete      bool           `json:"incomplete"`
	SkippedSymlinks int            `json:"skipped_symlinks"`
	Unreadable      int            `json:"unreadable"`
	Fingerprint     string         `json:"fingerprint"`
}

func ValidRoot(p string) bool { return p == "." || fs.ValidPath(p) && !strings.Contains(p, "\\") }

// DefaultExclude applies conservative exclusions independently of hidden-file
// visibility. IncludeHidden never overrides credential or dependency exclusions.
func DefaultExclude(p string) bool {
	for _, part := range strings.Split(p, "/") {
		low := strings.ToLower(part)
		switch low {
		case ".git", ".hg", ".svn", "node_modules", "vendor", ".venv", "venv", "__pycache__", "dist", "build", "target", ".ssh", ".aws", ".gnupg", ".npmrc", ".pypirc", ".netrc", "id_rsa", "id_ed25519", "credentials":
			return true
		}
		if strings.HasPrefix(low, ".env") || strings.HasSuffix(low, ".pem") || strings.HasSuffix(low, ".key") {
			return true
		}
	}
	return false
}

// validatePrefix rejects links in an explicitly selected subtree, including
// ancestor components that fs.WalkDir would otherwise resolve before walking.
func validatePrefix(ctx context.Context, filesystem fs.FS, root string) error {
	if root == "." {
		return nil
	}
	parent := "."
	for _, part := range strings.Split(root, "/") {
		if err := ctx.Err(); err != nil {
			return err
		}
		prefix := path.Join(parent, part)
		if statter, ok := filesystem.(interface {
			Lstat(string) (fs.FileInfo, error)
		}); ok {
			info, err := statter.Lstat(prefix)
			if err != nil {
				return err
			}
			if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
				return errors.New("subtree prefixes must be real directories")
			}
		} else {
			entries, err := fs.ReadDir(filesystem, parent)
			if err != nil {
				return err
			}
			found := false
			for _, entry := range entries {
				if entry.Name() == part {
					found = true
					if entry.Type()&fs.ModeSymlink != 0 || !entry.IsDir() {
						return errors.New("subtree prefixes must be real directories")
					}
					break
				}
			}
			if !found {
				return fs.ErrNotExist
			}
		}
		parent = prefix
	}
	return nil
}

// entryInfo returns metadata for p. On Windows a directory listing reports a
// subdirectory's timestamp from the parent's index, which NTFS updates
// lazily, so it can differ between two scans of an unchanged tree and make
// the snapshot fingerprint (and every pagination cursor bound to it)
// unstable. Directories are therefore stat'ed directly. Files keep the
// listing data: a direct stat opens each file, which is ~16x slower and, on a
// cold cache, triggers an antivirus scan of every file (tens of seconds on
// projects with build output).
func entryInfo(filesystem fs.FS, p string, d fs.DirEntry) (fs.FileInfo, error) {
	if d.IsDir() {
		if statter, ok := filesystem.(interface {
			Lstat(string) (fs.FileInfo, error)
		}); ok {
			return statter.Lstat(p)
		}
	}
	return d.Info()
}

// Scan returns deterministic folders-first depth-first entries, including empty
// directories and the requested root. It does not follow directory or file links.
func Scan(ctx context.Context, filesystem fs.FS, opts Options) (Snapshot, error) {
	if opts.Root == "" {
		opts.Root = "."
	}
	if !ValidRoot(opts.Root) {
		return Snapshot{}, errors.New("invalid project-relative directory")
	}
	if opts.MaxEntries == 0 {
		opts.MaxEntries = 80000
	}
	if opts.MaxFiles == 0 {
		opts.MaxFiles = 20000
	}
	if opts.MaxEntries < 1 || opts.MaxFiles < 1 {
		return Snapshot{}, errors.New("scan limits must be positive")
	}
	excluded := func(p string) bool { return DefaultExclude(p) || opts.Exclude != nil && opts.Exclude(p) }
	if opts.Root != "." && excluded(opts.Root) {
		return Snapshot{}, errors.New("directory is excluded by policy")
	}
	if err := validatePrefix(ctx, filesystem, opts.Root); err != nil {
		return Snapshot{}, err
	}
	out := Snapshot{Root: opts.Root, Entries: []Entry{}, Stats: Stats{Complete: true}, Languages: map[string]int{}, Extensions: map[string]int{}}
	byPath := map[string]*Entry{}
	children := map[string][]string{}
	seen, files := 0, 0
	err := fs.WalkDir(filesystem, opts.Root, func(p string, d fs.DirEntry, err error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err != nil {
			if p == opts.Root {
				return err
			}
			out.Unreadable++
			out.Incomplete = true
			return nil
		}
		if !utf8.ValidString(p) {
			out.Unreadable++
			out.Incomplete = true
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		seen++
		if seen > opts.MaxEntries {
			out.Incomplete = true
			return fs.SkipAll
		}
		if p != opts.Root && (excluded(p) || !opts.IncludeHidden && strings.HasPrefix(d.Name(), ".")) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			out.SkippedSymlinks++
			if p == opts.Root {
				return errors.New("symbolic-link roots are not allowed")
			}
			return nil
		}
		info, err := entryInfo(filesystem, p, d)
		if err != nil {
			out.Unreadable++
			out.Incomplete = true
			return nil
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			// Replaced by a link after the directory listing; never follow it.
			out.SkippedSymlinks++
			if p == opts.Root {
				return errors.New("symbolic-link roots are not allowed")
			}
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if p == opts.Root && !info.IsDir() {
			return errors.New("path must name a directory")
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return nil
		}
		depth := 0
		if p != opts.Root {
			relative := strings.TrimPrefix(p, opts.Root+"/")
			if opts.Root == "." {
				relative = p
			}
			depth = strings.Count(relative, "/") + 1
		}
		e := &Entry{Path: p, Name: d.Name(), Parent: path.Dir(p), Depth: depth, ModifiedNS: info.ModTime().UnixNano()}
		if p == opts.Root {
			e.Parent = ""
			if e.Name == "." {
				e.Name = "project"
			}
		}
		if info.IsDir() {
			e.Kind = "directory"
			e.Stats = &Stats{Complete: true}
		} else {
			files++
			if files > opts.MaxFiles {
				out.Incomplete = true
				return fs.SkipAll
			}
			e.Kind = "file"
			e.Bytes = info.Size()
			if opts.DetectLanguage != nil {
				e.Language = opts.DetectLanguage(p)
			}
			if e.Language != "" {
				out.Languages[e.Language]++
			}
			ext := strings.ToLower(path.Ext(p))
			if ext == "" {
				ext = "[none]"
			}
			out.Extensions[ext]++
		}
		byPath[p] = e
		if p != opts.Root {
			children[e.Parent] = append(children[e.Parent], p)
		}
		return nil
	})
	if err != nil {
		return out, err
	}
	if byPath[opts.Root] == nil {
		return out, errors.New("directory could not be scanned")
	}
	// Iterative traversal also handles unusually deep folder structures safely.
	stack := []string{opts.Root}
	order := []string{}
	for len(stack) > 0 {
		p := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		order = append(order, p)
		siblings := children[p]
		sort.Slice(siblings, func(i, j int) bool {
			a, b := byPath[siblings[i]], byPath[siblings[j]]
			if a.Kind != b.Kind {
				return a.Kind == "directory"
			}
			return a.Name < b.Name
		})
		for i := len(siblings) - 1; i >= 0; i-- {
			stack = append(stack, siblings[i])
		}
	}
	for i := len(order) - 1; i >= 0; i-- {
		e := byPath[order[i]]
		if e.Kind == "directory" {
			e.Children = len(children[e.Path])
			e.Empty = e.Children == 0 && !out.Incomplete
			e.Stats.Complete = !out.Incomplete
		}
		if e.Parent != "" {
			if parent := byPath[e.Parent]; parent != nil && parent.Stats != nil {
				if e.Kind == "directory" {
					parent.Stats.Directories += 1 + e.Stats.Directories
					parent.Stats.Files += e.Stats.Files
					parent.Stats.Bytes += e.Stats.Bytes
				} else {
					parent.Stats.Files++
					parent.Stats.Bytes += e.Bytes
				}
			}
		}
	}
	for _, p := range order {
		out.Entries = append(out.Entries, *byPath[p])
	}
	out.Stats = *byPath[opts.Root].Stats
	b, _ := json.Marshal(out)
	sum := sha256.Sum256(b)
	out.Fingerprint = hex.EncodeToString(sum[:])
	return out, nil
}

// Select limits displayed depth while preserving the full scan's statistics.
// Depth counts edges from the selected root; zero selects only that root.
func (s Snapshot) Select(depth int, foldersOnly bool, extensions []string) []Entry {
	allowed := map[string]bool{}
	for _, e := range extensions {
		allowed[strings.ToLower(strings.TrimPrefix(e, "."))] = true
	}
	out := []Entry{}
	for _, e := range s.Entries {
		if e.Depth > depth {
			continue
		}
		if e.Kind == "file" {
			if foldersOnly {
				continue
			}
			if len(allowed) > 0 && !allowed[strings.TrimPrefix(strings.ToLower(path.Ext(e.Name)), ".")] {
				continue
			}
		}
		out = append(out, e)
	}
	return out
}

// Markdown renders a paginated path view as nested Markdown lists. Explicit paths
// keep a page understandable when its parent appears on an earlier page.
func Markdown(entries []Entry) string {
	var b strings.Builder
	escape := strings.NewReplacer("\\", "\\\\", "*", "\\*", "_", "\\_", "`", "\\`", "[", "\\[", "]", "\\]", "<", "&lt;", ">", "&gt;", "\n", "\\n", "\r", "\\r", "\t", "\\t")
	for _, e := range entries {
		label := e.Path
		if e.Depth == 0 {
			label = e.Name
		}
		label = escape.Replace(label)
		b.WriteString(strings.Repeat("  ", e.Depth))
		b.WriteString("- ")
		if e.Kind == "directory" {
			b.WriteString("**" + label + "/**")
		} else {
			b.WriteString(label)
		}
		b.WriteByte('\n')
	}
	return b.String()
}
