package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/hamedfathi/TreeSitterMCP/internal/parsers"
	"github.com/hamedfathi/TreeSitterMCP/server"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	pack "github.com/xberg-io/tree-sitter-language-pack/packages/go"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type roots []string

func (r *roots) String() string     { return strings.Join(*r, ",") }
func (r *roots) Set(s string) error { *r = append(*r, s); return nil }
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "TreeSitterMCP:", err)
		os.Exit(1)
	}
}
func run() error {
	var allow roots
	flag.Var(&allow, "allow-root", "permitted project parent directory; repeat for several roots")
	state := flag.String("state-dir", "", "private registry and derived-cache directory")
	cache := flag.String("parser-cache", "", "parser cache base directory; bundled runtime/parsers is detected")
	maxFile := flag.Int64("max-file-bytes", 2<<20, "maximum source size")
	maxCache := flag.Int64("max-cache-bytes", 32<<20, "maximum retained source bytes and derived-cache bytes")
	maxEntries := flag.Int("max-cache-entries", 64, "maximum native parse trees")
	maxResponse := flag.Int("max-response-bytes", 24000, "maximum JSON analysis payload")
	maxFiles := flag.Int("max-files", 20000, "maximum admitted files per project scan")
	maxNodes := flag.Int("max-nodes", 200000, "maximum AST nodes per file")
	ttl := flag.Duration("cache-ttl", 15*time.Minute, "native, derived and snapshot cache lifetime")
	legacy := flag.Bool("enable-language-specific", false, "enable optional non-uniform symbol/import extensions")
	timeout := flag.Duration("timeout", 10*time.Second, "maximum tool-call duration, including queue time")
	autoInstall := flag.Bool("auto-install", true, "install the pinned parser bundle in the background when any grammar is missing")
	version := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if flag.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	if *version {
		fmt.Printf("TreeSitterMCP %s; language-pack %s\n", server.Version, server.PackVersion)
		return nil
	}
	if len(allow) == 0 {
		return fmt.Errorf("at least one --allow-root is required")
	}
	if *state == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return err
		}
		*state = filepath.Join(dir, "TreeSitterMCP")
	}
	if *cache == "" {
		exe, _ := os.Executable()
		for _, base := range []string{filepath.Dir(exe), filepath.Dir(filepath.Dir(exe))} {
			p := filepath.Join(base, "runtime", "parsers")
			if st, err := os.Stat(p); err == nil && st.IsDir() {
				*cache = p
				break
			}
		}
	}
	if *cache != "" {
		p, err := filepath.Abs(*cache)
		if err != nil {
			return err
		}
		if err = pack.Configure(pack.PackConfig{CacheDir: &p}); err != nil {
			return err
		}
	}
	if *autoInstall {
		bundles := ""
		exe, _ := os.Executable()
		for _, base := range []string{filepath.Dir(exe), filepath.Dir(filepath.Dir(exe))} {
			p := filepath.Join(base, "runtime", "bundles")
			if st, err := os.Stat(p); err == nil && st.IsDir() {
				bundles = p
				break
			}
		}
		if err := parsers.Start(bundles, os.Stderr); err != nil {
			// Exploration tools still work; AST tools report the cause.
			fmt.Fprintln(os.Stderr, "TreeSitterMCP: parser auto-install unavailable:", err)
		}
	}
	// Folder exploration remains available even when no source parsers are installed.
	engine, err := server.New(server.Options{AllowRoots: allow, StateDir: *state, MaxFileBytes: *maxFile, MaxCacheBytes: *maxCache, MaxCacheEntries: *maxEntries, MaxResponseBytes: *maxResponse, MaxFiles: *maxFiles, MaxNodes: *maxNodes, OperationTimeout: *timeout, CacheTTL: *ttl, EnableLanguageSpecific: *legacy})
	if err != nil {
		return err
	}
	defer engine.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return server.NewMCP(engine).Run(ctx, &mcp.IOTransport{Reader: &server.FrameReader{Reader: os.Stdin, MaxBytes: 1 << 20}, Writer: os.Stdout})
}
