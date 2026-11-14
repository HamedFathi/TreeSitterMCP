package server

import (
	"context"
	"errors"
	"fmt"
	"github.com/hamedfathi/TreeSitterMCP/projecttree"
	"path"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

type fileEntry struct {
	Path     string `json:"path"`
	Bytes    int64  `json:"bytes"`
	Language string `json:"language,omitempty"`
	Modified int64  `json:"modified_ns"`
}

// binaryExtensions are formats that are never UTF-8 source text.
var binaryExtensions = map[string]bool{}

func init() {
	for _, ext := range strings.Fields(`.dll .exe .pdb .so .dylib .a .lib .o .obj .class .jar .war .nupkg .snk .pyc .pyo .wasm .bin .dat .db .sqlite .sqlite3 .mdb .png .jpg .jpeg .gif .bmp .ico .webp .tif .tiff .psd .zip .gz .tgz .bz2 .xz .zst .7z .rar .tar .pdf .doc .docx .xls .xlsx .ppt .pptx .woff .woff2 .ttf .otf .eot .mp3 .mp4 .wav .flac .ogg .avi .mov .mkv .webm`) {
		binaryExtensions[ext] = true
	}
}

func binaryExtension(p string) bool {
	return binaryExtensions[strings.ToLower(path.Ext(p))]
}

func (e *Engine) projectOperation(ctx context.Context, tool string, in Input) (any, error) {
	r, p, err := e.openProject(in.Project)
	if err != nil {
		return nil, err
	}
	snapshot, _, err := e.scan(ctx, in, true)
	if err != nil {
		return nil, err
	}
	files := []fileEntry{}
	for _, entry := range snapshot.Entries {
		if entry.Kind == "file" {
			files = append(files, fileEntry{entry.Path, entry.Bytes, entry.Language, entry.ModifiedNS})
		}
	}
	switch tool {
	case "list_files":
		items := []any{}
		for _, f := range files {
			if in.Language == "" || in.Language == f.Language {
				items = append(items, f)
			}
		}
		return e.page(items, in, binding(tool, in)+snapshot.Fingerprint, snapshot.Incomplete, "")
	case "project_overview":
		counts := map[string]int{}
		for _, f := range files {
			lang := f.Language
			if lang == "" {
				lang = "unknown"
			}
			counts[lang]++
		}
		return map[string]any{"project": p.Name, "file_count": len(files), "directory_count": snapshot.Stats.Directories, "source_bytes": snapshot.Stats.Bytes, "languages": counts, "incomplete": snapshot.Incomplete, "note": "Metadata only. Excluded directories, secrets, and symlinks are omitted."}, nil
	case "search_text":
		if len(in.Pattern) == 0 || len(in.Pattern) > 4096 {
			return nil, errors.New("pattern must contain 1-4096 bytes")
		}
		if in.ContextLines < 0 || in.ContextLines > 5 {
			return nil, errors.New("context_lines must be 0-5")
		}
		if in.PathGlob != "" {
			if _, err := projecttree.Match(in.PathGlob, ""); err != nil {
				return nil, err
			}
		}
		expression := in.Pattern
		if !in.Regex {
			expression = regexp.QuoteMeta(expression)
		}
		if in.IgnoreCase {
			expression = "(?i:" + expression + ")"
		}
		re, err := regexp.Compile(expression)
		if err != nil {
			return nil, err
		}
		items := []any{}
		skipped, scanned := 0, 0
		fingerprints := []string{}
		incomplete := snapshot.Incomplete
	scan:
		for _, f := range files {
			if err = ctx.Err(); err != nil {
				return nil, err
			}
			if in.Language != "" && f.Language != in.Language {
				continue
			}
			if in.PathGlob != "" {
				ok, _ := projecttree.Match(in.PathGlob, f.Path)
				if !ok {
					continue
				}
			}
			// Binary and oversized files can never match as UTF-8 text; skip
			// them without opening, which on Windows also avoids an
			// antivirus scan per file (build output can hold thousands).
			if binaryExtension(f.Path) || f.Bytes > e.opts.MaxFileBytes {
				skipped++
				continue
			}
			b, err := e.read(r, p, f.Path)
			if err != nil {
				skipped++
				continue
			}
			scanned++
			fingerprints = append(fingerprints, f.Path+":"+digest(b))
			offset := 0
			lines := strings.Split(string(b), "\n")
			for line, text := range lines {
				indices := [][2]int{}
				matches := re.FindAllStringIndex(text, 201)
				if len(matches) >= 201 {
					incomplete = true
				}
				for _, m := range matches {
					if in.WholeWord && !wordBoundary(text, m[0], m[1]) {
						continue
					}
					indices = append(indices, [2]int{m[0], m[1]})
				}
				for _, m := range indices {
					preview := m[0] - 120
					if preview < 0 {
						preview = 0
					}
					for preview > 0 && !utf8.RuneStart(text[preview]) {
						preview--
					}
					item := map[string]any{"path": f.Path, "line": line + 1, "column": m[0], "start_byte": offset + m[0], "end_byte": offset + m[1], "preview": snippet([]byte(text[preview:]), 320)}
					if in.ContextLines > 0 {
						from := line - in.ContextLines
						if from < 0 {
							from = 0
						}
						to := line + in.ContextLines + 1
						if to > len(lines) {
							to = len(lines)
						}
						context := []any{}
						for k := from; k < to; k++ {
							context = append(context, map[string]any{"line": k + 1, "text": snippet([]byte(lines[k]), 320)})
						}
						item["context"] = context
					}
					items = append(items, item)
					if len(items) >= 10000 {
						incomplete = true
						break scan
					}
				}
				offset += len(text) + 1
			}
		}
		return e.page(items, in, binding(tool, in)+hashJSON(fingerprints), incomplete, fmt.Sprintf("Scanned %d files; skipped %d non-text, oversized, or unreadable files. Columns use UTF-8 bytes.", scanned, skipped))
	}
	return nil, errors.New("unknown project operation")
}

func wordBoundary(text string, start, end int) bool {
	word := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }
	if start > 0 {
		r, _ := utf8.DecodeLastRuneInString(text[:start])
		if word(r) {
			return false
		}
	}
	if end < len(text) {
		r, _ := utf8.DecodeRuneInString(text[end:])
		if word(r) {
			return false
		}
	}
	return true
}
