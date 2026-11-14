package server

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func fits(v any, n int) bool { b, err := json.Marshal(v); return err == nil && len(b) <= n }
func (e *Engine) derivedDir(in Input) string {
	return filepath.Join(e.opts.StateDir, "derived", digest([]byte(in.Project)), digest([]byte(in.Path)))
}
func (e *Engine) snapshotDir(project string) string {
	return filepath.Join(e.opts.StateDir, "snapshots", digest([]byte(project)))
}
func (e *Engine) expireCache() {
	for k, p := range e.trees {
		if time.Since(p.touched) >= e.opts.CacheTTL {
			p.tree.Close()
			e.cachedBytes -= int64(len(p.source))
			delete(e.trees, k)
		}
	}
	for _, kind := range []string{"derived", "snapshots"} {
		e.pruneDisk(kind)
	}
}

// Disk caches are private server state. Both age and aggregate size are bounded;
// native trees are never serialized. Errors other than missing paths surface on writes.
func (e *Engine) pruneDisk(kind string) {
	type entry struct {
		path  string
		size  int64
		mtime time.Time
	}
	items := []entry{}
	var total int64
	filepath.WalkDir(filepath.Join(e.opts.StateDir, kind), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type().IsRegular() {
			if st, err := d.Info(); err == nil {
				if time.Since(st.ModTime()) >= e.opts.CacheTTL {
					removeCacheEntry(path, filepath.Join(e.opts.StateDir, kind))
				} else {
					items = append(items, entry{path, st.Size(), st.ModTime()})
					total += st.Size()
				}
			}
		}
		return nil
	})
	sort.Slice(items, func(i, j int) bool {
		if items[i].mtime.Equal(items[j].mtime) {
			return items[i].path < items[j].path
		}
		return items[i].mtime.Before(items[j].mtime)
	})
	for len(items) > e.opts.MaxCacheEntries || total > e.opts.MaxCacheBytes {
		f := items[0]
		removeCacheEntry(f.path, filepath.Join(e.opts.StateDir, kind))
		total -= f.size
		items = items[1:]
	}
}
func (e *Engine) cacheStats() map[string]any {
	result := map[string]any{"entries": len(e.trees), "source_bytes": e.cachedBytes, "hits": e.hits, "misses": e.misses, "ttl_seconds": e.opts.CacheTTL.Seconds(), "max_entries": e.opts.MaxCacheEntries, "max_source_bytes": e.opts.MaxCacheBytes, "native_trees_persisted": false, "persistent_derived_cache": true, "snapshot_persistence": true}
	for _, kind := range []string{"derived", "snapshots"} {
		count := 0
		var bytes int64
		filepath.WalkDir(filepath.Join(e.opts.StateDir, kind), func(_ string, d fs.DirEntry, err error) error {
			if err == nil && d.Type().IsRegular() {
				if st, er := d.Info(); er == nil {
					count++
					bytes += st.Size()
				}
			}
			return nil
		})
		result[kind+"_entries"] = count
		result[kind+"_bytes"] = bytes
	}
	return result
}
func (e *Engine) clearCache(in Input) (any, error) {
	if in.Path != "" && in.Project == "" {
		return nil, errors.New("path requires project")
	}
	if in.Project != "" {
		if _, _, err := e.openProject(in.Project); err != nil {
			return nil, err
		}
	}
	if in.Path != "" {
		if _, err := cleanPath(in.Path); err != nil {
			return nil, err
		}
	}
	for k, p := range e.trees {
		if in.Project == "" || strings.HasPrefix(k, in.Project+"\x00") && (in.Path == "" || strings.HasPrefix(k, in.Project+"\x00"+in.Path+"\x00")) {
			p.tree.Close()
			e.cachedBytes -= int64(len(p.source))
			delete(e.trees, k)
		}
	}
	for _, kind := range []string{"derived", "snapshots"} {
		dir := filepath.Join(e.opts.StateDir, kind)
		if in.Project != "" {
			dir = filepath.Join(dir, digest([]byte(in.Project)))
		}
		if in.Path != "" {
			dir = filepath.Join(dir, digest([]byte(in.Path)))
		}
		if err := os.RemoveAll(dir); err != nil {
			return nil, err
		}
	}
	return map[string]any{"cleared": true, "project": in.Project, "path": in.Path}, nil
}

type snapshotNode struct {
	Address string `json:"address"`
	Type    string `json:"type"`
	Hash    string `json:"hash"`
	Shape   string `json:"shape"`
	Span    Span   `json:"span"`
}
type astSnapshot struct {
	Version    int            `json:"version"`
	Project    string         `json:"project"`
	Path       string         `json:"path"`
	Language   string         `json:"language"`
	SourceHash string         `json:"source_hash"`
	ID         string         `json:"id"`
	Created    time.Time      `json:"created"`
	Nodes      []snapshotNode `json:"nodes"`
}

func fingerprints(ctx context.Context, x *nodeIndex, b []byte) ([]snapshotNode, error) {
	out := make([]snapshotNode, len(x.nodes))
	childOrd := make([]int, len(x.nodes))
	for i, n := range x.nodes {
		if i%256 == 0 && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		parent := "root"
		ord := 0
		if par := x.parents[i]; par >= 0 {
			parent = out[par].Address
			ord = childOrd[par]
			childOrd[par]++
		}
		out[i] = snapshotNode{Address: hashJSON([]any{parent, ord}), Type: n.Kind(), Span: span(n)}
	}
	for i := len(x.nodes) - 1; i >= 0; i-- {
		if i%256 == 0 && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		n := x.nodes[i]
		shapes := []string{n.Kind()}
		hashes := []string{n.Kind()}
		if n.IsNamed() {
			shapes = append(shapes, "named")
			hashes = append(hashes, "named")
		}
		if n.IsMissing() {
			shapes = append(shapes, "missing")
			hashes = append(hashes, "missing")
		}
		if n.ChildCount() == 0 {
			hashes = append(hashes, digest(b[n.StartByte():n.EndByte()]))
		}
		for j := i + 1; j < x.ends[i]; j = x.ends[j] {
			shapes = append(shapes, x.fields[j], out[j].Shape)
			hashes = append(hashes, x.fields[j], out[j].Hash)
		}
		out[i].Hash = hashJSON(hashes)
		out[i].Shape = hashJSON(shapes)
	}
	return out, nil
}
func (e *Engine) createSnapshot(ctx context.Context, in Input, p *parsed, x *nodeIndex) (any, error) {
	nodes, err := fingerprints(ctx, x, p.source)
	if err != nil {
		return nil, err
	}
	s := astSnapshot{Version: 2, Project: in.Project, Path: in.Path, Language: p.name, SourceHash: p.digest, ID: x.prefix, Created: time.Now().UTC(), Nodes: nodes}
	b, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > e.opts.MaxCacheBytes {
		return nil, errors.New("snapshot exceeds disk cache byte budget")
	}
	dir := filepath.Join(e.snapshotDir(in.Project), digest([]byte(in.Path)))
	if err = os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err = atomicWrite(filepath.Join(dir, s.ID+".json"), b); err != nil {
		return nil, err
	}
	e.pruneDisk("snapshots")
	return map[string]any{"snapshot": s.ID, "source_hash": p.digest, "language": p.name, "nodes": len(nodes), "ttl_seconds": e.opts.CacheTTL.Seconds()}, nil
}
func validHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}
func (e *Engine) loadSnapshot(project, id string) (astSnapshot, error) {
	var out astSnapshot
	if !validHash(id) {
		return out, errors.New("invalid snapshot ID")
	}
	if _, _, err := e.openProject(project); err != nil {
		return out, err
	}
	found := ""
	err := filepath.WalkDir(e.snapshotDir(project), func(path string, d fs.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if d.Type().IsRegular() && d.Name() == id+".json" {
			found = path
		}
		return nil
	})
	if err != nil {
		return out, err
	}
	if found == "" {
		return out, errors.New("snapshot not retained, expired, or belongs to another project")
	}
	st, err := os.Stat(found)
	if err != nil {
		return out, err
	}
	if st.Size() > e.opts.MaxCacheBytes {
		return out, errors.New("snapshot exceeds budget")
	}
	b, err := os.ReadFile(found)
	if err != nil {
		return out, err
	}
	if err = json.Unmarshal(b, &out); err != nil {
		return out, err
	}
	if out.Version != 2 || out.ID != id || out.Project != project {
		return out, errors.New("invalid snapshot metadata")
	}
	return out, nil
}
func (e *Engine) compareSnapshots(ctx context.Context, in Input) (any, error) {
	a, err := e.loadSnapshot(in.Project, in.Snapshot)
	if err != nil {
		return nil, err
	}
	b, err := e.loadSnapshot(in.Project, in.OtherSnapshot)
	if err != nil {
		return nil, err
	}
	if a.Language != b.Language {
		return nil, errors.New("snapshots must use the same grammar")
	}
	before := map[string]snapshotNode{}
	for _, n := range a.Nodes {
		before[n.Address] = n
	}
	items := []any{}
	for i, n := range b.Nodes {
		if i%256 == 0 && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		old, ok := before[n.Address]
		if !ok {
			items = append(items, map[string]any{"change": "added", "after": n})
		} else if old.Hash != n.Hash {
			items = append(items, map[string]any{"change": "changed", "before": old, "after": n})
		}
		delete(before, n.Address)
	}
	for _, n := range a.Nodes {
		if _, ok := before[n.Address]; ok {
			items = append(items, map[string]any{"change": "removed", "before": n})
		}
	}
	return e.page(items, in, binding("compare_ast_snapshots", in)+a.ID+b.ID, false, "Ordered child-address comparison; ancestor changes are included. Insertions may shift sibling addresses. Not a semantic or minimal edit diff.")
}

// Remove empty hash directories as entries expire, so cache metadata itself
// remains bounded after visiting many different project files.
func removeCacheEntry(path, base string) {
	if os.Remove(path) != nil {
		return
	}
	for dir := filepath.Dir(path); dir != base && inside(base, dir); dir = filepath.Dir(dir) {
		if os.Remove(dir) != nil {
			break
		}
	}
}
