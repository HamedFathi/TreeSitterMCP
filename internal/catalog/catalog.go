// Package catalog owns the exact upstream parser catalog used by this release.
package catalog

import (
	_ "embed"
	"encoding/json"
	"sort"
	"strings"
)

const Version = "1.25.0"

//go:embed parsers.json
var ManifestJSON []byte

type Asset struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}
type Manifest struct {
	Version   string                     `json:"version"`
	Platforms map[string]Asset           `json:"platforms"`
	Languages map[string]json.RawMessage `json:"languages"`
}

func Read() Manifest {
	var m Manifest
	if err := json.Unmarshal(ManifestJSON, &m); err != nil {
		panic(err)
	}
	return m
}
func Names() []string {
	out := []string{}
	for name := range Read().Languages {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
func Canonical(s string) string {
	s = strings.ToLower(s)
	if v, ok := map[string]string{"shell": "bash", "lisp": "commonlisp", "makefile": "make", "ignorefile": "gitignore", "gradle": "groovy", "bazel": "starlark", "c#": "csharp", "c_sharp": "csharp", "c++": "cpp", "js": "javascript", "ts": "typescript", "py": "python", "golang": "go"}[s]; ok {
		return v
	}
	return s
}
