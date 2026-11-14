// packctl installs and verifies every canonical parser in the pinned release.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/hamedfathi/TreeSitterMCP/internal/catalog"
	"github.com/hamedfathi/TreeSitterMCP/internal/parsers"
	sitter "github.com/tree-sitter/go-tree-sitter"
	pack "github.com/xberg-io/tree-sitter-language-pack/packages/go"
	"os"
	"path/filepath"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	cache := flag.String("cache", "", "parser cache base directory")
	all := flag.Bool("download-all", false, "install every parser from an offline bundle or the pinned release")
	verify := flag.Bool("verify-all", false, "load every canonical parser and parse empty input")
	bundles := flag.String("bundle-dir", "", "offline parser archive directory")
	force := flag.Bool("force", false, "with --download-all, reinstall even when the cache verifies")
	flag.Parse()
	if *cache != "" {
		p, err := filepath.Abs(*cache)
		if err != nil {
			return err
		}
		if err = pack.Configure(pack.PackConfig{CacheDir: &p}); err != nil {
			return err
		}
	}
	if *all {
		if *bundles == "" {
			exe, _ := os.Executable()
			for _, base := range []string{filepath.Dir(exe), filepath.Dir(filepath.Dir(exe)), "."} {
				p := filepath.Join(base, "runtime", "bundles")
				if st, err := os.Stat(p); err == nil && st.IsDir() {
					*bundles = p
					break
				}
			}
		}
		in, err := parsers.Default(*bundles, os.Stderr)
		if err != nil {
			return err
		}
		did, err := in.Ensure(context.Background(), *force)
		if err != nil {
			return err
		}
		if !did {
			fmt.Fprintf(os.Stderr, "All %d parsers already installed for %s\n", in.Expected, parsers.Platform())
		}
	}
	names := catalog.Names()
	installed := map[string]bool{}
	for _, name := range pack.DownloadedLanguages() {
		installed[name] = true
	}
	failures := map[string]string{}
	count := 0
	for _, name := range names {
		if installed[name] {
			count++
		}
		if !*verify {
			continue
		}
		if !installed[name] {
			failures[name] = "parser is not installed"
			continue
		}
		lang, err := pack.GetLanguage(name)
		if err != nil {
			failures[name] = err.Error()
			continue
		}
		p := sitter.NewParser()
		if err = p.SetLanguage(lang); err != nil {
			failures[name] = err.Error()
			p.Close()
			continue
		}
		tree := p.Parse([]byte{}, nil)
		if tree == nil {
			failures[name] = "empty parse returned nil"
		} else {
			tree.Close()
		}
		p.Close()
	}
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"pack_version": catalog.Version, "platform": parsers.Platform(), "catalog_count": len(names), "installed_count": count, "languages": names, "verified": *verify, "failures": failures, "apl_supported": false}); err != nil {
		return err
	}
	if len(failures) > 0 {
		return fmt.Errorf("%d parser verification failures", len(failures))
	}
	return nil
}
