// Package parsers installs the pinned parser bundle into the language-pack
// cache exactly once and keeps every catalog grammar available.
//
// The install is crash-safe and idempotent:
//   - A completed install writes installed.json (the commit record) last, so an
//     interrupted install is detected and redone on the next start.
//   - Later starts verify the record against file sizes only; no network.
//   - Concurrent processes serialize on a heartbeat lock file; if two installs
//     ever overlap they still converge, because every step is atomic and both
//     place byte-identical, checksum-verified files.
package parsers

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hamedfathi/TreeSitterMCP/internal/catalog"
	"github.com/klauspost/compress/zstd"
)

const (
	recordFile = "installed.json"
	lockFile   = ".install.lock"
)

// Tunables; variables so tests can shorten them.
var (
	staleLock    = 2 * time.Minute  // a lock untouched this long belongs to a dead process
	heartbeat    = 20 * time.Second // lock refresh interval while installing
	lockWait     = 15 * time.Minute // longest wait for another process's install
	lockPoll     = 250 * time.Millisecond
	staleTemp    = time.Hour // leftover download/stage files older than this are removed
	retryBackoff = []time.Duration{time.Second, 3 * time.Second}
)

// Platform returns the language-pack platform key for this process.
func Platform() string {
	name, arch := runtime.GOOS, runtime.GOARCH
	if name == "darwin" {
		name = "macos"
	}
	if arch == "amd64" {
		arch = "x86_64"
	}
	if arch == "arm64" && name != "macos" {
		arch = "aarch64"
	}
	return name + "-" + arch
}

type record struct {
	Version  string           `json:"version"`
	Platform string           `json:"platform"`
	Files    map[string]int64 `json:"files"`
}

var errNoRecord = errors.New("no install record")

// Installer installs one bundle into one cache directory.
type Installer struct {
	Asset    catalog.Asset
	Version  string
	Platform string
	Expected int    // parser libraries in the bundle
	Libs     string // final library directory
	Bundles  string // optional directory holding an offline parsers-<platform>.tar.zst
	Manifest []byte // optional catalog copy written next to the libraries
	Client   *http.Client
	Log      io.Writer
	// Missing reports catalog grammars the loader cannot see; nil skips the check.
	Missing func() []string
}

func (in *Installer) base() string { return filepath.Dir(in.Libs) }

func (in *Installer) logf(format string, args ...any) {
	if in.Log != nil {
		fmt.Fprintf(in.Log, "TreeSitterMCP: "+format+"\n", args...)
	}
}

// Healthy reports nil when a complete install is present. It only stats
// files, so it is cheap enough to run on every start.
func (in *Installer) Healthy() error {
	b, err := os.ReadFile(filepath.Join(in.base(), recordFile))
	if errors.Is(err, os.ErrNotExist) {
		return errNoRecord
	}
	if err != nil {
		return err
	}
	var r record
	if err = json.Unmarshal(b, &r); err != nil {
		return fmt.Errorf("install record is corrupt: %w", err)
	}
	if r.Version != in.Version || r.Platform != in.Platform {
		return fmt.Errorf("install record is for %s %s", r.Version, r.Platform)
	}
	if len(r.Files) != in.Expected {
		return fmt.Errorf("install record lists %d parsers; expected %d", len(r.Files), in.Expected)
	}
	for name, size := range r.Files {
		if name != filepath.Base(name) || strings.ContainsAny(name, "/\\") {
			return errors.New("install record has an unsafe name")
		}
		st, err := os.Stat(filepath.Join(in.Libs, name))
		if err != nil {
			return fmt.Errorf("parser %s is missing", name)
		}
		if !st.Mode().IsRegular() || st.Size() != size {
			return fmt.Errorf("parser %s is damaged", name)
		}
	}
	if in.Missing != nil {
		if m := in.Missing(); len(m) > 0 {
			return fmt.Errorf("%d parsers are not loadable (first: %s)", len(m), m[0])
		}
	}
	return nil
}

// adopt records an install made before install records existed (for example
// by an older packctl) when the loader already sees every grammar.
func (in *Installer) adopt() error {
	if in.Missing == nil || len(in.Missing()) > 0 {
		return errors.New("cannot adopt an incomplete install")
	}
	entries, err := os.ReadDir(in.Libs)
	if err != nil {
		return err
	}
	files := map[string]int64{}
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		files[e.Name()] = info.Size()
	}
	if len(files) != in.Expected {
		return fmt.Errorf("found %d libraries; expected %d", len(files), in.Expected)
	}
	return in.writeRecord(files)
}

// Ensure makes the install complete. It returns installed=true when it had
// to install, and never reinstalls a healthy cache unless force is set.
func (in *Installer) Ensure(ctx context.Context, force bool) (installed bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			installed, err = false, fmt.Errorf("parser install panicked: %v", r)
		}
	}()
	if !force {
		herr := in.Healthy()
		if herr == nil {
			return false, nil
		}
		if errors.Is(herr, errNoRecord) && in.adopt() == nil && in.Healthy() == nil {
			return false, nil
		}
	}
	unlock, err := in.lock(ctx)
	if err != nil {
		return false, err
	}
	defer unlock()
	// Another process may have finished the install while we waited.
	if !force && in.Healthy() == nil {
		return false, nil
	}
	in.logf("installing %d parsers for %s into %s", in.Expected, in.Platform, in.Libs)
	if err = in.install(ctx); err != nil {
		return false, err
	}
	if err = in.Healthy(); err != nil {
		return true, fmt.Errorf("parser install did not verify: %w", err)
	}
	in.logf("installed %d parsers", in.Expected)
	return true, nil
}

func (in *Installer) lock(ctx context.Context) (func(), error) {
	if err := os.MkdirAll(in.base(), 0700); err != nil {
		return nil, err
	}
	p := filepath.Join(in.base(), lockFile)
	deadline := time.Now().Add(lockWait)
	waiting := false
	for {
		f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err == nil {
			fmt.Fprintf(f, "%d %s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339))
			f.Close()
			stop := make(chan struct{})
			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				t := time.NewTicker(heartbeat)
				defer t.Stop()
				for {
					select {
					case <-stop:
						return
					case <-t.C:
						now := time.Now()
						_ = os.Chtimes(p, now, now)
					}
				}
			}()
			var once sync.Once
			return func() {
				once.Do(func() {
					close(stop)
					wg.Wait()
					_ = os.Remove(p)
				})
			}, nil
		}
		// Windows reports access denied for a lock that is being deleted.
		if !errors.Is(err, os.ErrExist) && !errors.Is(err, os.ErrPermission) {
			return nil, err
		}
		if st, serr := os.Stat(p); serr == nil && time.Since(st.ModTime()) > staleLock {
			in.logf("removing stale parser install lock")
			_ = os.Remove(p)
			continue
		}
		if !waiting {
			in.logf("waiting for another process to finish installing parsers")
			waiting = true
		}
		if time.Now().After(deadline) {
			return nil, errors.New("timed out waiting for another parser install")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(lockPoll):
		}
	}
}

func (in *Installer) install(ctx context.Context) error {
	if err := os.MkdirAll(in.base(), 0700); err != nil {
		return err
	}
	in.cleanTemps()
	archive, cleanup, err := in.obtain(ctx)
	if err != nil {
		return err
	}
	defer cleanup()
	stage, err := os.MkdirTemp(in.base(), "parser-stage-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	files, err := extract(archive, stage, in.Asset, in.Expected)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(in.Libs, 0700); err != nil {
		return err
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err = place(filepath.Join(stage, name), filepath.Join(in.Libs, name), files[name]); err != nil {
			return fmt.Errorf("placing %s: %w", name, err)
		}
	}
	if len(in.Manifest) > 0 {
		if err = atomicWrite(filepath.Join(in.base(), "parsers.json"), in.Manifest); err != nil {
			return err
		}
	}
	return in.writeRecord(files)
}

func (in *Installer) writeRecord(files map[string]int64) error {
	b, err := json.MarshalIndent(record{Version: in.Version, Platform: in.Platform, Files: files}, "", " ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(in.base(), recordFile), b)
}

// cleanTemps removes downloads and stage directories left by killed installs.
func (in *Installer) cleanTemps() {
	entries, err := os.ReadDir(in.base())
	if err != nil {
		return
	}
	for _, e := range entries {
		n := e.Name()
		if !strings.HasPrefix(n, "bundle-") && !strings.HasPrefix(n, "parser-stage-") && !strings.HasPrefix(n, ".tmp-") {
			continue
		}
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > staleTemp {
			_ = os.RemoveAll(filepath.Join(in.base(), n))
		}
	}
}

// obtain returns a checksum-verified bundle: the offline copy when valid,
// otherwise a fresh download with retries.
func (in *Installer) obtain(ctx context.Context) (string, func(), error) {
	noop := func() {}
	if in.Bundles != "" {
		p := filepath.Join(in.Bundles, "parsers-"+in.Platform+".tar.zst")
		if _, err := os.Stat(p); err == nil {
			if err = verifyFile(p, in.Asset); err == nil {
				return p, noop, nil
			}
			in.logf("offline bundle %s rejected (%v); downloading instead", p, err)
		}
	}
	var last error
	for attempt := 0; attempt <= len(retryBackoff); attempt++ {
		if attempt > 0 {
			in.logf("parser download failed (%v); retrying", last)
			select {
			case <-ctx.Done():
				return "", noop, ctx.Err()
			case <-time.After(retryBackoff[attempt-1]):
			}
		}
		p, err := in.download(ctx)
		if err == nil {
			return p, func() { _ = os.Remove(p) }, nil
		}
		last = err
		if ctx.Err() != nil {
			return "", noop, ctx.Err()
		}
	}
	return "", noop, fmt.Errorf("parser bundle download failed: %w", last)
}

func (in *Installer) download(ctx context.Context) (string, error) {
	if in.Asset.URL == "" {
		return "", errors.New("no download URL for this platform")
	}
	f, err := os.CreateTemp(in.base(), "bundle-*.tar.zst")
	if err != nil {
		return "", err
	}
	name := f.Name()
	fail := func(err error) (string, error) {
		f.Close()
		_ = os.Remove(name)
		return "", err
	}
	dctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(dctx, http.MethodGet, in.Asset.URL, nil)
	if err != nil {
		return fail(err)
	}
	client := in.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fail(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fail(fmt.Errorf("HTTP %d", resp.StatusCode))
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, in.Asset.Size+1))
	if err != nil {
		return fail(err)
	}
	if n != in.Asset.Size {
		return fail(fmt.Errorf("bundle size %d; expected %d", n, in.Asset.Size))
	}
	if hex.EncodeToString(h.Sum(nil)) != in.Asset.SHA256 {
		return fail(errors.New("bundle SHA-256 mismatch"))
	}
	if err = f.Sync(); err != nil {
		return fail(err)
	}
	if err = f.Close(); err != nil {
		_ = os.Remove(name)
		return "", err
	}
	return name, nil
}

func verifyFile(p string, asset catalog.Asset) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, asset.Size+1))
	if err != nil {
		return err
	}
	if n != asset.Size {
		return errors.New("size mismatch")
	}
	if hex.EncodeToString(h.Sum(nil)) != asset.SHA256 {
		return errors.New("SHA-256 mismatch")
	}
	return nil
}

// extract unpacks a verified bundle into stage and returns file sizes.
func extract(archive, stage string, asset catalog.Asset, expected int) (map[string]int64, error) {
	if err := verifyFile(archive, asset); err != nil {
		return nil, fmt.Errorf("parser bundle: %w", err)
	}
	f, err := os.Open(archive)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	decoder, err := zstd.NewReader(f, zstd.WithDecoderMaxMemory(512<<20))
	if err != nil {
		return nil, err
	}
	defer decoder.Close()
	tr := tar.NewReader(decoder)
	files := map[string]int64{}
	var total int64
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if header.Typeflag == tar.TypeDir {
			continue
		}
		if header.Typeflag != tar.TypeReg {
			return nil, errors.New("unexpected non-regular archive member")
		}
		name := strings.TrimPrefix(header.Name, "./")
		if name == "" || name != filepath.Base(name) || strings.ContainsAny(name, "/\\:") || name == "." || name == ".." || header.Size > 128<<20 || header.Size < 0 {
			return nil, errors.New("unsafe parser archive member")
		}
		if _, dup := files[name]; dup {
			return nil, errors.New("duplicate parser archive member")
		}
		total += header.Size
		if total > 1<<30 {
			return nil, errors.New("expanded bundle exceeds 1 GiB")
		}
		dest, err := os.OpenFile(filepath.Join(stage, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return nil, err
		}
		_, err = io.CopyN(dest, tr, header.Size)
		if err == nil {
			err = dest.Sync()
		}
		cerr := dest.Close()
		if err != nil {
			return nil, err
		}
		if cerr != nil {
			return nil, cerr
		}
		files[name] = header.Size
	}
	if len(files) != expected {
		return nil, fmt.Errorf("bundle contains %d parsers; expected %d", len(files), expected)
	}
	return files, nil
}

// place moves one verified library into the cache. On Windows a library
// another process has loaded cannot be replaced; it came from the same
// verified bundle, so an identical-size copy already in place is accepted.
// Antivirus and indexers can briefly lock new files, so transient failures
// are retried.
func place(src, dst string, size int64) error {
	var err error
	for i := 0; i < 6; i++ {
		if err = os.Rename(src, dst); err == nil {
			return nil
		}
		if st, serr := os.Stat(dst); serr == nil && st.Mode().IsRegular() && st.Size() == size {
			return nil
		}
		time.Sleep(time.Duration(i+1) * 100 * time.Millisecond)
	}
	return err
}

func atomicWrite(p string, b []byte) error {
	f, err := os.CreateTemp(filepath.Dir(p), ".tmp-")
	if err != nil {
		return err
	}
	name := f.Name()
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = place(name, p, int64(len(b)))
	}
	if err != nil {
		_ = os.Remove(name)
	}
	return err
}
