package parsers

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hamedfathi/TreeSitterMCP/internal/catalog"
	"github.com/klauspost/compress/zstd"
)

func init() {
	retryBackoff = []time.Duration{10 * time.Millisecond, 10 * time.Millisecond}
	lockPoll = 10 * time.Millisecond
}

type member struct {
	name string
	body []byte
	kind byte
}

func bundle(t *testing.T, members []member) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(zw)
	for _, m := range members {
		kind := m.kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		h := &tar.Header{Name: m.name, Mode: 0600, Size: int64(len(m.body)), Typeflag: kind}
		if kind != tar.TypeReg {
			h.Size = 0
			h.Linkname = "x"
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if kind == tar.TypeReg {
			if _, err := tw.Write(m.body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func goodMembers(n int) []member {
	out := []member{}
	for i := 0; i < n; i++ {
		out = append(out, member{name: fmt.Sprintf("./tree_sitter_l%02d.dll", i), body: bytes.Repeat([]byte{byte(i + 1)}, 100+i)})
	}
	return out
}

func asset(data []byte, url string) catalog.Asset {
	sum := sha256.Sum256(data)
	return catalog.Asset{URL: url, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(data))}
}

// server serves data; fail(n) reports whether request n (1-based) should fail.
func server(t *testing.T, data []byte, fail func(n int64) bool) (*httptest.Server, *atomic.Int64) {
	var hits atomic.Int64
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if fail != nil && fail(n) {
			http.Error(w, "boom", http.StatusBadGateway)
			return
		}
		w.Write(data)
	}))
	t.Cleanup(s.Close)
	return s, &hits
}

func newInstaller(t *testing.T, dir string, a catalog.Asset, n int) *Installer {
	return &Installer{Asset: a, Version: "1.0.0", Platform: "test-os", Expected: n, Libs: filepath.Join(dir, "v1.0.0", "libs"), Manifest: []byte(`{}`)}
}

func TestFirstRunInstallsSecondRunSkips(t *testing.T) {
	data := bundle(t, goodMembers(5))
	s, hits := server(t, data, nil)
	dir := t.TempDir()
	in := newInstaller(t, dir, asset(data, s.URL), 5)
	did, err := in.Ensure(context.Background(), false)
	if err != nil || !did {
		t.Fatalf("first run: did=%v err=%v", did, err)
	}
	if err := in.Healthy(); err != nil {
		t.Fatal(err)
	}
	// A new process (fresh installer) must not download again.
	for i := 0; i < 3; i++ {
		did, err = newInstaller(t, dir, asset(data, s.URL), 5).Ensure(context.Background(), false)
		if err != nil || did {
			t.Fatalf("later run %d: did=%v err=%v", i, did, err)
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("downloads = %d; want 1", hits.Load())
	}
	b, _ := os.ReadFile(filepath.Join(in.Libs, "tree_sitter_l03.dll"))
	if len(b) != 103 || b[0] != 4 {
		t.Fatal("library content wrong")
	}
	if _, err := os.Stat(filepath.Join(in.base(), lockFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("lock not released")
	}
	leftovers, _ := filepath.Glob(filepath.Join(in.base(), "*-*"))
	if len(leftovers) != 0 {
		t.Fatalf("temporary files left: %v", leftovers)
	}
}

func TestForceReinstalls(t *testing.T) {
	data := bundle(t, goodMembers(3))
	s, hits := server(t, data, nil)
	in := newInstaller(t, t.TempDir(), asset(data, s.URL), 3)
	if _, err := in.Ensure(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if did, err := in.Ensure(context.Background(), true); err != nil || !did {
		t.Fatalf("force: did=%v err=%v", did, err)
	}
	if hits.Load() != 2 {
		t.Fatalf("downloads = %d; want 2", hits.Load())
	}
}

func TestRepairsMissingAndDamagedFiles(t *testing.T) {
	data := bundle(t, goodMembers(4))
	s, hits := server(t, data, nil)
	in := newInstaller(t, t.TempDir(), asset(data, s.URL), 4)
	if _, err := in.Ensure(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	damage := []func(){
		func() { os.Remove(filepath.Join(in.Libs, "tree_sitter_l01.dll")) },
		func() { os.WriteFile(filepath.Join(in.Libs, "tree_sitter_l02.dll"), []byte("truncated"), 0600) },
		func() { os.WriteFile(filepath.Join(in.base(), recordFile), []byte("{not json"), 0600) },
		func() { os.Remove(filepath.Join(in.base(), recordFile)) },
		func() { os.RemoveAll(in.Libs) },
	}
	for i, d := range damage {
		d()
		if in.Healthy() == nil {
			t.Fatalf("damage %d not detected", i)
		}
		did, err := in.Ensure(context.Background(), false)
		if err != nil || !did {
			t.Fatalf("repair %d: did=%v err=%v", i, did, err)
		}
		if err := in.Healthy(); err != nil {
			t.Fatalf("repair %d: %v", i, err)
		}
	}
	if hits.Load() != int64(1+len(damage)) {
		t.Fatalf("downloads = %d", hits.Load())
	}
}

func TestRecordForOtherVersionReinstalls(t *testing.T) {
	data := bundle(t, goodMembers(2))
	s, hits := server(t, data, nil)
	dir := t.TempDir()
	in := newInstaller(t, dir, asset(data, s.URL), 2)
	if _, err := in.Ensure(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	other := newInstaller(t, dir, asset(data, s.URL), 2)
	other.Platform = "other-os"
	if did, err := other.Ensure(context.Background(), false); err != nil || !did {
		t.Fatalf("did=%v err=%v", did, err)
	}
	if hits.Load() != 2 {
		t.Fatal("platform change did not reinstall")
	}
}

func TestRejectsBadBundles(t *testing.T) {
	good := bundle(t, goodMembers(2))
	cases := map[string]struct {
		data     []byte
		mutate   func(*catalog.Asset)
		expected int
	}{
		"sha mismatch":    {good, func(a *catalog.Asset) { a.SHA256 = strings.Repeat("0", 64) }, 2},
		"size mismatch":   {good, func(a *catalog.Asset) { a.Size++ }, 2},
		"count mismatch":  {good, nil, 3},
		"path traversal":  {bundle(t, []member{{name: "../evil.dll", body: []byte("x")}, {name: "a.dll", body: []byte("y")}}), nil, 2},
		"nested path":     {bundle(t, []member{{name: "sub/a.dll", body: []byte("x")}, {name: "b.dll", body: []byte("y")}}), nil, 2},
		"symlink member":  {bundle(t, []member{{name: "a.dll", kind: tar.TypeSymlink}, {name: "b.dll", body: []byte("y")}}), nil, 2},
		"duplicate":       {bundle(t, []member{{name: "a.dll", body: []byte("x")}, {name: "./a.dll", body: []byte("y")}}), nil, 2},
		"not zstd at all": {[]byte("this is not an archive"), nil, 2},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			s, _ := server(t, c.data, nil)
			a := asset(c.data, s.URL)
			if c.mutate != nil {
				c.mutate(&a)
			}
			in := newInstaller(t, t.TempDir(), a, c.expected)
			if _, err := in.Ensure(context.Background(), false); err == nil {
				t.Fatal("bad bundle accepted")
			}
			if in.Healthy() == nil {
				t.Fatal("bad bundle left a healthy record")
			}
			if _, err := os.Stat(filepath.Join(in.base(), "..", "evil.dll")); err == nil {
				t.Fatal("traversal wrote outside the cache")
			}
			if _, err := os.Stat(filepath.Join(in.base(), lockFile)); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("lock not released after failure")
			}
		})
	}
}

func TestRetriesTransientDownloadFailures(t *testing.T) {
	data := bundle(t, goodMembers(2))
	s, hits := server(t, data, func(n int64) bool { return n < 3 })
	in := newInstaller(t, t.TempDir(), asset(data, s.URL), 2)
	if _, err := in.Ensure(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 3 {
		t.Fatalf("attempts = %d; want 3", hits.Load())
	}
}

func TestGivesUpAfterPersistentFailure(t *testing.T) {
	data := bundle(t, goodMembers(2))
	s, hits := server(t, data, func(int64) bool { return true })
	in := newInstaller(t, t.TempDir(), asset(data, s.URL), 2)
	if _, err := in.Ensure(context.Background(), false); err == nil {
		t.Fatal("expected failure")
	}
	if hits.Load() != int64(1+len(retryBackoff)) {
		t.Fatalf("attempts = %d", hits.Load())
	}
}

func TestOfflineBundlePreferredAndBadOneFallsBack(t *testing.T) {
	data := bundle(t, goodMembers(3))
	s, hits := server(t, data, nil)
	offline := t.TempDir()
	os.WriteFile(filepath.Join(offline, "parsers-test-os.tar.zst"), data, 0600)
	in := newInstaller(t, t.TempDir(), asset(data, s.URL), 3)
	in.Bundles = offline
	if _, err := in.Ensure(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 0 {
		t.Fatal("downloaded despite a valid offline bundle")
	}
	if _, err := os.Stat(filepath.Join(offline, "parsers-test-os.tar.zst")); err != nil {
		t.Fatal("offline bundle was removed")
	}
	os.WriteFile(filepath.Join(offline, "parsers-test-os.tar.zst"), []byte("corrupt"), 0600)
	in2 := newInstaller(t, t.TempDir(), asset(data, s.URL), 3)
	in2.Bundles = offline
	if _, err := in2.Ensure(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 {
		t.Fatal("corrupt offline bundle did not fall back to download")
	}
}

func TestConcurrentProcessesConverge(t *testing.T) {
	data := bundle(t, goodMembers(6))
	s, hits := server(t, data, nil)
	dir := t.TempDir()
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := newInstaller(t, dir, asset(data, s.URL), 6).Ensure(context.Background(), false)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("downloads = %d; the lock should serialize installs", hits.Load())
	}
	if err := newInstaller(t, dir, asset(data, s.URL), 6).Healthy(); err != nil {
		t.Fatal(err)
	}
}

func TestStaleLockIsRecovered(t *testing.T) {
	data := bundle(t, goodMembers(2))
	s, _ := server(t, data, nil)
	in := newInstaller(t, t.TempDir(), asset(data, s.URL), 2)
	os.MkdirAll(in.base(), 0700)
	p := filepath.Join(in.base(), lockFile)
	os.WriteFile(p, []byte("12345 dead"), 0600)
	old := time.Now().Add(-time.Hour)
	os.Chtimes(p, old, old)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := in.Ensure(ctx, false); err != nil {
		t.Fatal(err)
	}
}

func TestHeldLockWaitsThenSkipsWhenPeerInstalled(t *testing.T) {
	data := bundle(t, goodMembers(2))
	s, hits := server(t, data, nil)
	dir := t.TempDir()
	peer := newInstaller(t, dir, asset(data, s.URL), 2)
	unlock, err := peer.lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := newInstaller(t, dir, asset(data, s.URL), 2).Ensure(context.Background(), false)
		done <- err
	}()
	time.Sleep(150 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("did not wait for the held lock")
	default:
	}
	// The peer completes its install, then releases.
	if err := peer.install(context.Background()); err != nil {
		t.Fatal(err)
	}
	unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 {
		t.Fatalf("downloads = %d; waiter should reuse the peer's install", hits.Load())
	}
}

func TestLockWaitHonorsContext(t *testing.T) {
	in := newInstaller(t, t.TempDir(), catalog.Asset{}, 1)
	unlock, err := in.lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := in.lock(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
}

func TestStaleTempFilesCleaned(t *testing.T) {
	data := bundle(t, goodMembers(2))
	s, _ := server(t, data, nil)
	in := newInstaller(t, t.TempDir(), asset(data, s.URL), 2)
	os.MkdirAll(filepath.Join(in.base(), "parser-stage-old"), 0700)
	os.WriteFile(filepath.Join(in.base(), "bundle-old.tar.zst"), []byte("x"), 0600)
	os.WriteFile(filepath.Join(in.base(), "bundle-fresh.tar.zst"), []byte("x"), 0600)
	old := time.Now().Add(-2 * time.Hour)
	os.Chtimes(filepath.Join(in.base(), "parser-stage-old"), old, old)
	os.Chtimes(filepath.Join(in.base(), "bundle-old.tar.zst"), old, old)
	if _, err := in.Ensure(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"parser-stage-old", "bundle-old.tar.zst"} {
		if _, err := os.Stat(filepath.Join(in.base(), n)); err == nil {
			t.Fatalf("%s not cleaned", n)
		}
	}
	if _, err := os.Stat(filepath.Join(in.base(), "bundle-fresh.tar.zst")); err != nil {
		t.Fatal("fresh temp of a possibly concurrent install was removed")
	}
}

func TestAdoptsCompleteLegacyInstall(t *testing.T) {
	in := newInstaller(t, t.TempDir(), catalog.Asset{}, 3)
	os.MkdirAll(in.Libs, 0700)
	for i := 0; i < 3; i++ {
		os.WriteFile(filepath.Join(in.Libs, fmt.Sprintf("l%d.dll", i)), []byte("lib"), 0600)
	}
	in.Missing = func() []string { return nil }
	if did, err := in.Ensure(context.Background(), false); err != nil || did {
		t.Fatalf("did=%v err=%v; legacy install should be adopted without network", did, err)
	}
	if err := in.Healthy(); err != nil {
		t.Fatal(err)
	}
}

func TestLoaderMissingForcesInstall(t *testing.T) {
	data := bundle(t, goodMembers(2))
	s, hits := server(t, data, nil)
	in := newInstaller(t, t.TempDir(), asset(data, s.URL), 2)
	if _, err := in.Ensure(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	in.Missing = func() []string { return []string{"csharp"} }
	if in.Healthy() == nil {
		t.Fatal("loader-missing grammar not detected")
	}
	in.Missing = nil
	if hits.Load() != 1 {
		t.Fatal("unexpected download")
	}
}

func TestManagerRetriesAfterFailure(t *testing.T) {
	retryAfter = 50 * time.Millisecond
	defer func() { retryAfter = 30 * time.Second }()
	data := bundle(t, goodMembers(2))
	var down atomic.Bool
	down.Store(true)
	s, _ := server(t, data, func(int64) bool { return down.Load() })
	m := NewManager(newInstaller(t, t.TempDir(), asset(data, s.URL), 2))
	if err := m.Wait(context.Background()); err == nil {
		t.Fatal("expected failure while offline")
	}
	// Within the backoff window the failure is reported without hammering the network.
	if err := m.Wait(context.Background()); err == nil {
		t.Fatal("expected cached failure")
	}
	down.Store(false)
	time.Sleep(60 * time.Millisecond)
	if err := m.Wait(context.Background()); err != nil {
		t.Fatalf("did not recover once online: %v", err)
	}
	if err := m.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestManagerConcurrentWaitersShareOneInstall(t *testing.T) {
	data := bundle(t, goodMembers(3))
	s, hits := server(t, data, nil)
	m := NewManager(newInstaller(t, t.TempDir(), asset(data, s.URL), 3))
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := m.Wait(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if hits.Load() != 1 {
		t.Fatalf("downloads = %d", hits.Load())
	}
}

func TestManagerWaitHonorsContext(t *testing.T) {
	block := make(chan struct{})
	data := bundle(t, goodMembers(2))
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
		w.Write(data)
	}))
	defer s.Close()
	m := NewManager(newInstaller(t, t.TempDir(), asset(data, s.URL), 2))
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := m.Wait(ctx); err == nil || !strings.Contains(err.Error(), "still being installed") {
		t.Fatalf("err = %v", err)
	}
	// The install keeps going in the background and later callers get it.
	close(block)
	if err := m.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
}
