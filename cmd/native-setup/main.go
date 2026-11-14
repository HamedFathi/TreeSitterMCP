// native-setup prepares a verified platform FFI library for a source checkout.
package main

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/hamedfathi/TreeSitterMCP/internal/catalog"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

type nativeAsset struct {
	FileName string `json:"file_name"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
}
type nativeManifest struct {
	Version   string                 `json:"version"`
	URL       string                 `json:"module_zip_url"`
	SHA256    string                 `json:"module_zip_sha256"`
	Platforms map[string]nativeAsset `json:"platforms"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	root := flag.String("root", ".", "repository root")
	flag.Parse()
	var m nativeManifest
	if err := json.Unmarshal(catalog.NativeJSON, &m); err != nil {
		return err
	}
	name := runtime.GOOS
	arch := runtime.GOARCH
	if name == "darwin" {
		name = "macos"
	}
	if arch == "amd64" {
		arch = "x86_64"
	}
	if arch == "arm64" && name != "macos" {
		arch = "aarch64"
	}
	platform := name + "-" + arch
	a, ok := m.Platforms[platform]
	if !ok {
		return fmt.Errorf("unsupported platform %s", platform)
	}
	dest := filepath.Join(*root, "third_party", "languagepack", ".lib", platform, a.FileName)
	if b, err := os.ReadFile(dest); err == nil {
		h := sha256.Sum256(b)
		if hex.EncodeToString(h[:]) == a.SHA256 {
			return nil
		}
	}
	temp, err := os.CreateTemp("", "languagepack-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.URL, nil)
	if err != nil {
		temp.Close()
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		temp.Close()
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		temp.Close()
		return fmt.Errorf("native download: HTTP %d", resp.StatusCode)
	}
	hash := sha256.New()
	_, err = io.Copy(io.MultiWriter(temp, hash), io.LimitReader(resp.Body, (160<<20)+1))
	cerr := temp.Close()
	if err != nil {
		return err
	}
	if cerr != nil {
		return cerr
	}
	if hex.EncodeToString(hash.Sum(nil)) != m.SHA256 {
		return fmt.Errorf("native module archive SHA-256 mismatch")
	}
	archive, err := zip.OpenReader(temp.Name())
	if err != nil {
		return err
	}
	defer archive.Close()
	expected := "github.com/xberg-io/tree-sitter-language-pack/packages/go@v" + m.Version + "/.lib/" + platform + "/" + a.FileName
	for _, f := range archive.File {
		if f.Name != expected {
			continue
		}
		if f.UncompressedSize64 != uint64(a.Size) {
			return fmt.Errorf("native library size mismatch")
		}
		src, err := f.Open()
		if err != nil {
			return err
		}
		b, err := io.ReadAll(io.LimitReader(src, a.Size+1))
		src.Close()
		if err != nil {
			return err
		}
		h := sha256.Sum256(b)
		if int64(len(b)) != a.Size || hex.EncodeToString(h[:]) != a.SHA256 {
			return fmt.Errorf("native library SHA-256 mismatch")
		}
		if err = os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return err
		}
		stage, err := os.CreateTemp(filepath.Dir(dest), "native-*.tmp")
		if err != nil {
			return err
		}
		defer os.Remove(stage.Name())
		if err = stage.Chmod(0644); err == nil {
			_, err = stage.Write(b)
		}
		cerr = stage.Close()
		if err != nil {
			return err
		}
		if cerr != nil {
			return cerr
		}
		return os.Rename(stage.Name(), dest)
	}
	return fmt.Errorf("native library not present in pinned archive")
}
