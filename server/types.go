// Package server exposes a bounded code analysis engine and a stdio MCP adapter.
package server

import "time"

const Version = "1.0.0"
const PackVersion = "1.25.0"

type Options struct {
	AllowRoots             []string
	StateDir               string
	MaxFileBytes           int64
	MaxCacheBytes          int64
	MaxCacheEntries        int
	MaxResponseBytes       int
	MaxFiles               int
	MaxNodes               int
	OperationTimeout       time.Duration
	CacheTTL               time.Duration
	EnableLanguageSpecific bool
}

func (o *Options) defaults() {
	if o.MaxFileBytes == 0 {
		o.MaxFileBytes = 2 << 20
	}
	if o.MaxCacheBytes == 0 {
		o.MaxCacheBytes = 32 << 20
	}
	if o.MaxCacheEntries == 0 {
		o.MaxCacheEntries = 64
	}
	if o.MaxResponseBytes == 0 {
		o.MaxResponseBytes = 24000
	}
	if o.MaxFiles == 0 {
		o.MaxFiles = 20000
	}
	if o.MaxNodes == 0 {
		o.MaxNodes = 200000
	}
	if o.CacheTTL == 0 {
		o.CacheTTL = 15 * time.Minute
	}
	if o.OperationTimeout == 0 {
		o.OperationTimeout = 10 * time.Second
	}
}

type Input struct {
	NodeID        string  `json:"node_id,omitempty"`
	Relation      string  `json:"relation,omitempty"`
	Section       string  `json:"section,omitempty"`
	Line          int     `json:"line,omitempty"`
	Column        int     `json:"column,omitempty"`
	IgnoreCase    bool    `json:"ignore_case,omitempty"`
	WholeWord     bool    `json:"whole_word,omitempty"`
	PathGlob      string  `json:"path_glob,omitempty"`
	ContextLines  int     `json:"context_lines,omitempty"`
	Snapshot      string  `json:"snapshot,omitempty"`
	OtherSnapshot string  `json:"other_snapshot,omitempty"`
	OtherPath     string  `json:"other_path,omitempty"`
	MinScore      float64 `json:"min_score,omitempty"`

	Project       string   `json:"project,omitempty"`
	Root          string   `json:"root,omitempty"`
	Path          string   `json:"path,omitempty"`
	Language      string   `json:"language,omitempty"`
	Cursor        string   `json:"cursor,omitempty"`
	Limit         int      `json:"limit,omitempty"`
	MaxBytes      int      `json:"max_bytes,omitempty"`
	StartLine     int      `json:"start_line,omitempty"`
	EndLine       int      `json:"end_line,omitempty"`
	Depth         int      `json:"depth,omitempty"`
	StartByte     uint     `json:"start_byte,omitempty"`
	EndByte       uint     `json:"end_byte,omitempty"`
	Pattern       string   `json:"pattern,omitempty"`
	Regex         bool     `json:"regex,omitempty"`
	Query         string   `json:"query,omitempty"`
	Name          string   `json:"name,omitempty"`
	FoldersOnly   bool     `json:"folders_only,omitempty"`
	IncludeHidden bool     `json:"include_hidden,omitempty"`
	Extensions    []string `json:"extensions,omitempty"`
}
type Project struct {
	Name         string    `json:"name"`
	Root         string    `json:"root"`
	RegisteredAt time.Time `json:"registered_at"`
}
type Page struct {
	Items      []any  `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
	Truncated  bool   `json:"truncated"`
	Incomplete bool   `json:"incomplete,omitempty"`
	Note       string `json:"note,omitempty"`
}
type Span struct {
	StartByte   uint `json:"start_byte"`
	EndByte     uint `json:"end_byte"`
	StartLine   uint `json:"start_line"`
	EndLine     uint `json:"end_line"`
	StartColumn uint `json:"start_column"`
	EndColumn   uint `json:"end_column"`
}
type Symbol struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	NodeType  string `json:"node_type"`
	Span      Span   `json:"span"`
	Signature string `json:"signature"`
}
type Dependency struct {
	Source       string `json:"source"`
	Target       string `json:"target"`
	Kind         string `json:"kind"`
	ResolvedPath string `json:"resolved_path,omitempty"`
	Span         Span   `json:"span"`
}
