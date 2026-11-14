package parsers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/hamedfathi/TreeSitterMCP/internal/catalog"
	sitter "github.com/tree-sitter/go-tree-sitter"
	pack "github.com/xberg-io/tree-sitter-language-pack/packages/go"
)

// retryAfter is how long a failed install waits before a tool call retries it.
var retryAfter = 30 * time.Second

// Default returns the installer for the configured language-pack cache.
func Default(bundles string, log io.Writer) (*Installer, error) {
	manifest := catalog.Read()
	asset, ok := manifest.Platforms[Platform()]
	if !ok {
		return nil, fmt.Errorf("unsupported platform %s", Platform())
	}
	libs, err := pack.CacheDir()
	if err != nil {
		return nil, err
	}
	return &Installer{Asset: asset, Version: catalog.Version, Platform: Platform(), Expected: len(manifest.Languages), Libs: libs, Bundles: bundles, Manifest: catalog.ManifestJSON, Log: log, Missing: Missing}, nil
}

// Installed reports whether the loader sees the named canonical grammar.
func Installed(name string) bool {
	for _, n := range pack.DownloadedLanguages() {
		if n == name {
			return true
		}
	}
	return false
}

// Missing lists catalog grammars the loader cannot see.
func Missing() []string {
	have := map[string]bool{}
	for _, n := range pack.DownloadedLanguages() {
		have[n] = true
	}
	out := []string{}
	for _, n := range catalog.Names() {
		if !have[n] {
			out = append(out, n)
		}
	}
	return out
}

// Manager runs at most one install at a time for this process and lets
// callers wait for it.
type Manager struct {
	in       *Installer
	mu       sync.Mutex
	running  chan struct{}
	healthy  bool
	lastErr  error
	lastTry  time.Time
	repaired map[string]bool
}

func NewManager(in *Installer) *Manager { return &Manager{in: in, repaired: map[string]bool{}} }

var closedCh = func() chan struct{} { c := make(chan struct{}); close(c); return c }()

func (m *Manager) kick(force bool) <-chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running != nil {
		return m.running
	}
	if !force && m.healthy {
		return closedCh
	}
	if !force && m.lastErr != nil && time.Since(m.lastTry) < retryAfter {
		return closedCh
	}
	ch := make(chan struct{})
	m.running = ch
	m.lastTry = time.Now()
	go func() {
		_, err := m.in.Ensure(context.Background(), force)
		if err != nil {
			m.in.logf("parser install failed: %v", err)
		}
		m.mu.Lock()
		m.healthy = err == nil
		m.lastErr = err
		m.running = nil
		m.mu.Unlock()
		close(ch)
	}()
	return ch
}

// Wait blocks until the parsers are verified, an install attempt fails, or
// ctx ends. A failed install is retried once retryAfter has passed.
func (m *Manager) Wait(ctx context.Context) error {
	ch := m.kick(false)
	select {
	case <-ch:
	case <-ctx.Done():
		return errors.New("parsers are still being installed; retry shortly")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.healthy {
		return nil
	}
	if m.lastErr != nil {
		return m.lastErr
	}
	return errors.New("parsers are not installed")
}

var (
	stdMu sync.Mutex
	std   *Manager
)

// Start verifies the cache in the background and installs the pinned bundle
// when anything is missing or damaged. Healthy caches are never reinstalled.
func Start(bundles string, log io.Writer) error {
	in, err := Default(bundles, log)
	if err != nil {
		return err
	}
	m := NewManager(in)
	stdMu.Lock()
	std = m
	stdMu.Unlock()
	m.kick(false)
	return nil
}

func current() *Manager {
	stdMu.Lock()
	defer stdMu.Unlock()
	return std
}

// Wait blocks until the process-wide install is ready; it is a no-op when
// Start was never called.
func Wait(ctx context.Context) error {
	if m := current(); m != nil {
		return m.Wait(ctx)
	}
	return nil
}

// Require reports nil when the named grammar can be loaded, waiting for any
// install in progress.
func Require(ctx context.Context, name string) error {
	if Installed(name) {
		return nil
	}
	if err := Wait(ctx); err != nil {
		return fmt.Errorf("parser %q is not available: %w", name, err)
	}
	if Installed(name) {
		return nil
	}
	return fmt.Errorf("parser %q is not installed; run packctl --download-all", name)
}

// Load returns the named grammar. A grammar whose library is present but
// fails to load is treated as cache damage and repaired once per process.
func Load(ctx context.Context, name string) (*sitter.Language, error) {
	if err := Require(ctx, name); err != nil {
		return nil, err
	}
	lang, err := pack.GetLanguage(name)
	if err == nil {
		return lang, nil
	}
	m := current()
	if m == nil {
		return nil, err
	}
	m.mu.Lock()
	tried := m.repaired[name]
	m.repaired[name] = true
	m.mu.Unlock()
	if tried {
		return nil, err
	}
	m.in.logf("parser %s failed to load (%v); repairing cache", name, err)
	select {
	case <-m.kick(true):
	case <-ctx.Done():
		return nil, fmt.Errorf("parser %q is being repaired; retry shortly", name)
	}
	if lang, rerr := pack.GetLanguage(name); rerr == nil {
		return lang, nil
	}
	return nil, err
}
