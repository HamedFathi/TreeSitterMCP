# Changelog

All notable changes to TreeSitterMCP are documented here. The project follows
[Semantic Versioning](https://semver.org/).

## Unreleased

### Fixed

- Parsers are installed automatically on first start. Previously a build
  without `install` left the parser cache empty and every AST tool failed with
  "parser is not installed". The install runs in the background, is
  checksum-verified, retried on network errors, and recorded in
  `installed.json`, so later starts never download again. Missing, truncated,
  or unloadable parsers and interrupted installs are repaired automatically;
  concurrent servers share one install through a heartbeat lock. Tool calls
  wait for a first-run install instead of failing. New `--auto-install` flag
  (default `true`); `packctl --download-all` is now idempotent, with `--force`.
- Pagination cursors from `get_project_tree`, `get_directory_info`, and
  `find_paths` no longer go stale spuriously on Windows: entry metadata now
  comes from a direct `Lstat` instead of the lazily updated NTFS directory
  index.
- Project scans no longer time out on Windows projects with build output.
  Only directories are stat'ed directly (files use directory-listing data, so
  they are not opened and antivirus-scanned one by one), and `search_text`
  skips binary extensions such as `.dll`/`.pdb` without opening them and
  rejects other binary files from their first 8 KiB. On a 7,400-file .NET
  project the first `get_project_tree` went from 51 s to 0.14 s and
  `search_text` from 7.9 s to 1.4 s.
- `scripts/build.ps1` works while the server is running (in-use binaries are
  moved aside), honors `CC` (clang on Windows ARM64), and finds MSYS2/MinGW
  `gcc`/`clang` when it is not on `PATH`.
- Release builds fail if a Linux binary would require newer than glibc 2.34.
  The README lists the minimum OS for each platform.

## 1.0.0 - 2026-11-14

First public release.

### Project exploration

- `get_project_tree`: folders-first hierarchy with depth limits, empty visible
  directories, structured entries, Markdown view, and recursive statistics.
- `get_directory_info`: recursive counts, language/extension summaries,
  aggregate file bytes, and paginated immediate children.
- `find_paths`: basename globs, recursive `**` path globs, and RE2 regexes.
- Folders-only, extension, and hidden-file presentation controls that preserve
  credential, dependency, build, state, and symlink boundaries.
- Standalone, parser-independent `projecttree` Go package.

### Source analysis (371 grammars)

- 25 uniform core tools and 4 MCP prompts that work on every pinned grammar,
  including syntax-recovery trees.
- AST exploration with deterministic, snapshot-bound node IDs; position lookup
  and navigation; native Tree-sitter queries per file or across a project.
- Grammar metadata, file metadata, syntax diagnostics, and AST metrics.
- Text search with RE2, Unicode case folding, word boundaries, path globs, and
  bounded context lines.
- Persistent AST snapshots with structural diffs, and same-grammar structural
  similarity search.
- Optional, opt-in `get_symbols` and `get_dependencies` language-specific tools.

### Runtime

- Native-tree LRU cache, content-hash invalidation, TTL, scoped invalidation,
  disk snapshots, and cache statistics.
- Bounded JSON payloads, content/snapshot-bound pagination cursors, and explicit
  incomplete-scan flags.
- Operator-configured allow roots, anchored filesystem handles, symlink
  controls, and built-in credential/dependency exclusions. Project code is
  never executed.
- Verified native-library and offline parser installation for source checkouts.
