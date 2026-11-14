package projecttree

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"
)

func fixture() fstest.MapFS {
	return fstest.MapFS{
		"README.md": {Data: []byte("abc")}, "main.go": {Data: []byte("code")}, "src/app.py": {Data: []byte("hello")}, "src/empty": {Mode: fs.ModeDir | 0755}, "docs/guide.md": {Data: []byte("manual")}, "empty": {Mode: fs.ModeDir | 0755}, ".github/workflows/ci.yml": {Data: []byte("testing")}, ".env": {Data: []byte("secret")}, ".git/config": {Data: []byte("secret")}, "node_modules/dep.js": {Data: []byte("ignored")}, "link": {Mode: fs.ModeSymlink | 0777, Data: []byte("/outside")},
	}
}
func snapshot(t *testing.T, opts Options) Snapshot {
	t.Helper()
	s, err := Scan(context.Background(), fixture(), opts)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func TestHierarchyAndStats(t *testing.T) {
	s := snapshot(t, Options{})
	if s.Stats != (Stats{Files: 4, Directories: 4, Bytes: 18, Complete: true}) {
		t.Fatalf("stats: %+v", s.Stats)
	}
	if s.SkippedSymlinks != 1 {
		t.Fatal("symlink not reported")
	}
	paths := []string{}
	for _, e := range s.Entries {
		paths = append(paths, e.Path)
		if e.Path == "empty" || e.Path == "src/empty" {
			if !e.Empty {
				t.Fatal("empty folder lost")
			}
		}
	}
	if strings.Join(paths, ",") != ".,docs,docs/guide.md,empty,src,src/empty,src/app.py,README.md,main.go" {
		t.Fatal(paths)
	}
	if len(s.Select(1, false, nil)) != 6 {
		t.Fatal("depth limit is wrong")
	}
	if len(s.Select(32, true, nil)) != 5 {
		t.Fatal("folders-only view is wrong")
	}
	filtered := s.Select(32, false, []string{".py"})
	if len(filtered) != 6 {
		t.Fatal("extension filter lost directories")
	}
	if s.Stats.Files != 4 {
		t.Fatal("presentation filters changed aggregate stats")
	}
}
func TestSubtreeAndHidden(t *testing.T) {
	s := snapshot(t, Options{Root: "src"})
	if s.Entries[0].Path != "src" || s.Entries[0].Depth != 0 || s.Stats.Files != 1 || s.Stats.Directories != 1 {
		t.Fatalf("wrong subtree: %+v", s)
	}
	s = snapshot(t, Options{IncludeHidden: true})
	if s.Stats.Files != 5 || s.Stats.Directories != 6 {
		t.Fatalf("hidden stats: %+v", s.Stats)
	}
	for _, e := range s.Entries {
		if DefaultExclude(e.Path) {
			t.Fatal("credential exclusion overridden")
		}
	}
}
func TestFingerprint(t *testing.T) {
	f := fixture()
	a, err := Scan(context.Background(), f, Options{})
	if err != nil {
		t.Fatal(err)
	}
	b := snapshot(t, Options{})
	if a.Fingerprint != b.Fingerprint {
		t.Fatal("scan is nondeterministic")
	}
	f["new.txt"] = &fstest.MapFile{Data: []byte("new")}
	b, err = Scan(context.Background(), f, Options{})
	if err != nil || a.Fingerprint == b.Fingerprint {
		t.Fatal("changed tree retained fingerprint")
	}
}
func TestScanLimits(t *testing.T) {
	for _, opts := range []Options{{MaxEntries: 3}, {MaxFiles: 1}} {
		s := snapshot(t, opts)
		if !s.Incomplete || s.Stats.Complete {
			t.Fatal("limit did not mark lower-bound counts")
		}
		for _, e := range s.Entries {
			if e.Empty {
				t.Fatal("incomplete directory claimed to be empty")
			}
		}
	}
}
func TestCancellationAndValidation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Scan(ctx, fixture(), Options{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, p := range []string{"../outside", "/outside", "a\\b", ".git", ".env", "main.go", "link"} {
		if _, err := Scan(context.Background(), fixture(), Options{Root: p}); err == nil {
			t.Fatalf("invalid root accepted: %s", p)
		}
	}
}
func TestAdditionalExclusions(t *testing.T) {
	s := snapshot(t, Options{Exclude: func(p string) bool { return p == "docs" }})
	if s.Stats.Files != 3 || s.Stats.Directories != 3 {
		t.Fatalf("custom exclusion ignored: %+v", s.Stats)
	}
}
func TestUnreadable(t *testing.T) {
	s, err := Scan(context.Background(), failureFS{fixture()}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if s.Unreadable != 1 || !s.Incomplete || s.Stats.Complete {
		t.Fatalf("unreadable metadata not reported: %+v", s)
	}
}

type failureFS struct{ fstest.MapFS }

func (f failureFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if name == "docs" {
		return nil, fs.ErrPermission
	}
	return f.MapFS.ReadDir(name)
}
func TestMarkdownEscaping(t *testing.T) {
	s := snapshot(t, Options{})
	view := Markdown(s.Select(1, false, nil))
	if !strings.Contains(view, "- **project/**") || !strings.Contains(view, "  - **src/**") {
		t.Fatal(view)
	}
	view = Markdown([]Entry{{Path: "bad\n*name[<x>].go", Name: "bad", Kind: "file", Depth: 1}})
	if strings.Contains(view, "\n*") || strings.Contains(view, "<x>") {
		t.Fatal("unsafe Markdown name")
	}
}
func TestGlobMatching(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{{"*.go", "src/main.go", true}, {"**/*.go", "main.go", true}, {"**/*.go", "src/deep/main.go", true}, {"src/**/app.py", "src/app.py", true}, {"src/**/app.py", "src/deep/app.py", true}, {"src/*.py", "src/deep/app.py", false}, {"docs/**", "docs", true}, {"*.py", "main.go", false}, {"[ab].go", "a.go", true}}
	for _, c := range cases {
		t.Run(c.pattern+":"+c.name, func(t *testing.T) {
			got, err := Match(c.pattern, c.name)
			if err != nil || got != c.want {
				t.Fatalf("%v %v", got, err)
			}
		})
	}
	for _, p := range []string{"", "[", strings.Repeat("a/", 65) + "x"} {
		if _, err := Match(p, "x"); err == nil {
			t.Fatal("invalid glob accepted")
		}
	}
}

func TestSubtreePrefixRejectsLinks(t *testing.T) {
	for _, p := range []string{"link/sub", "link"} {
		if _, err := Scan(context.Background(), fixture(), Options{Root: p}); err == nil {
			t.Fatal("symbolic-link prefix accepted")
		}
	}
}
func TestInvalidUTF8Paths(t *testing.T) {
	f := fixture()
	f[string([]byte{'b', 0xff})] = &fstest.MapFile{Data: []byte("x")}
	s, err := Scan(context.Background(), f, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !s.Incomplete || s.Unreadable != 1 {
		t.Fatal("invalid UTF-8 path was not reported")
	}
}

func TestRealSubtreeAncestorSymlink(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "real", "sub"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "real", "sub", "file.txt"), []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, "real", filepath.Join(root, "alias"))
	if _, err := Scan(context.Background(), os.DirFS(root), Options{Root: "alias/sub"}); err == nil {
		t.Fatal("real symlink ancestor was followed")
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
