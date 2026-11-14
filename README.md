<div align="center">

# TreeSitterMCP

**Give your AI assistant a real understanding of your code, without flooding its context.**

A fast, local [Model Context Protocol](https://modelcontextprotocol.io) server
written in Go that lets Claude and other MCP clients explore project folders and
analyze source code in **371 languages** using [Tree-sitter](https://tree-sitter.github.io/).

[![CI](https://github.com/hamedfathi/TreeSitterMCP/actions/workflows/ci.yml/badge.svg)](https://github.com/hamedfathi/TreeSitterMCP/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/hamedfathi/TreeSitterMCP)](https://github.com/hamedfathi/TreeSitterMCP/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)
![Go](https://img.shields.io/badge/Go-1.26%2B-00ADD8?logo=go)
![Languages](https://img.shields.io/badge/grammars-371-orange)
![MCP](https://img.shields.io/badge/MCP-stdio-purple)

[Quick start](#-quick-start) •
[Connect a client](#-connect-your-ai-client) •
[Examples](#-what-you-can-ask) •
[Tools](#-tool-reference) •
[Configuration](#-configuration) •
[Troubleshooting](#-troubleshooting)

</div>

---

## Why TreeSitterMCP?

When an AI assistant reads your project file by file, it wastes context on
irrelevant code and still misses the structure. TreeSitterMCP gives it targeted,
**bounded** answers instead:

- 🌳 **See the shape first.** Project trees, directory summaries, and path search
  without reading a single file.
- 🔍 **Ask the syntax tree.** Parse files into ASTs, jump to the node at a
  position, navigate parents/children, and run native Tree-sitter queries across a
  whole project.
- 🌍 **371 languages, one contract.** Every core tool works on every bundled
  grammar, from Python and Rust to Terraform, SQL, and Markdown.
- 📏 **Context-friendly by design.** Every response has a byte budget and pages
  with continuation cursors, so huge repositories never blow up the conversation.
- 🔒 **Safe and local.** Runs on your machine over stdio. Only folders you
  explicitly allow are visible, secrets and dependency folders are excluded, and
  your project code is **never executed**.
- ⚡ **Fast.** Native parsers, an LRU tree cache, content-hash invalidation, and
  persistent snapshots.
- 🧩 **Embeddable.** Use it as an MCP server or as a Go library.

---

## 📋 Contents

- [Quick start](#-quick-start)
- [Connect your AI client](#-connect-your-ai-client)
- [What you can ask](#-what-you-can-ask)
- [Core concepts](#-core-concepts)
- [Tool reference](#-tool-reference)
- [Tool call examples](#-tool-call-examples)
- [Configuration](#-configuration)
- [Security model](#-security-model)
- [Use as a Go library](#-use-as-a-go-library)
- [Development and testing](#-development-and-testing)
- [Troubleshooting](#-troubleshooting)
- [FAQ](#-faq)
- [Limitations](#-limitations)
- [Contributing](#-contributing)
- [License](#-license)

---

## 🚀 Quick start

### Option A: download a release (recommended)

Prebuilt archives are published for six platforms on the
[**Releases**](https://github.com/hamedfathi/TreeSitterMCP/releases) page. Each
one has the server, the native library, and all 371 parsers ready to use.

| Platform | File | Minimum OS |
| --- | --- | --- |
| Linux x86-64 | `TreeSitterMCP-<version>-linux-x86_64.tar.gz` | glibc 2.34 (Ubuntu 22.04, Debian 12, RHEL 9) |
| Linux ARM64 | `TreeSitterMCP-<version>-linux-arm64.tar.gz` | glibc 2.34 (Ubuntu 22.04, Debian 12, RHEL 9) |
| macOS Intel | `TreeSitterMCP-<version>-macos-x86_64.tar.gz` | macOS 15 |
| macOS Apple Silicon | `TreeSitterMCP-<version>-macos-arm64.tar.gz` | macOS 15 |
| Windows x86-64 | `TreeSitterMCP-<version>-windows-x86_64.zip` | Windows 10 |
| Windows ARM64 | `TreeSitterMCP-<version>-windows-arm64.zip` | Windows 11 |

The minimums come from the upstream native library: its Linux builds need
glibc 2.34 (musl/Alpine is not supported) and its macOS builds target macOS 15.

1. Download the file for your platform and extract it somewhere permanent, such
   as `~/tools/TreeSitterMCP` or `C:\tools\TreeSitterMCP`. Keep the folder
   structure **intact**.
2. Check that it runs:

   ```sh
   # Linux / macOS
   ./bin/TreeSitterMCP --version

   # Windows (PowerShell)
   .\bin\TreeSitterMCP.exe --version
   ```

   Expected output: `TreeSitterMCP 1.0.0; language-pack 1.25.0`

3. [Connect your AI client](#-connect-your-ai-client).

> 🍎 **macOS:** the binaries are not notarized. If macOS blocks them, run
> `xattr -dr com.apple.quarantine ~/tools/TreeSitterMCP` once.
>
> 🔐 Verify your download against `SHA256SUMS.txt` from the same release:
> `sha256sum -c SHA256SUMS.txt --ignore-missing` (Linux), `shasum -a 256 <file>` (macOS),
> or `Get-FileHash <file>` (Windows).

### Option B: build from source (Linux, macOS, Windows)

**Requirements**

| Requirement | Notes |
| --- | --- |
| [Go](https://go.dev/dl/) **1.26+** | `go version` must work in your terminal |
| A C compiler (CGO) | Linux: `gcc` or `clang` · macOS: `xcode-select --install` · Windows x64: MinGW-w64 GCC from [MSYS2](https://www.msys2.org/) (`pacman -S mingw-w64-ucrt-x86_64-gcc`) · Windows ARM64: MSYS2 Clang (`pacman -S mingw-w64-clang-aarch64-clang`, then set `$env:CC = "clang"`) |
| Internet (first install only) | Downloads Go modules, the native FFI library, and parsers. Everything is checksum-verified. |

**Linux / macOS**

```sh
git clone https://github.com/hamedfathi/TreeSitterMCP.git
cd TreeSitterMCP
./scripts/install.sh
./bin/TreeSitterMCP --version
```

**Windows (PowerShell)**

```powershell
git clone https://github.com/hamedfathi/TreeSitterMCP.git
cd TreeSitterMCP
$env:CGO_ENABLED = "1"
./scripts/install.ps1
.\bin\TreeSitterMCP.exe --version
```

`install` prepares the native library, builds `bin/TreeSitterMCP` and
`bin/packctl`, places the native FFI library next to them in `bin/`, installs
all 371 parsers into `runtime/parsers`, and checks that each one loads.

> 💡 **Parsers install themselves.** If you only run `./scripts/build.sh` (or
> `build.ps1`), the server installs all 371 parsers on its first start: it
> downloads the pinned bundle in the background, verifies its SHA-256, and
> records the result in `installed.json`. Tool calls that need a grammar wait
> for it; tree and path tools work right away. Later starts only check file
> sizes against that record, so nothing is downloaded again. A missing or
> damaged parser is repaired automatically on the next start, an interrupted
> install is redone, and concurrent servers share one install through a lock
> file. Pass `--auto-install=false` to disable this (for example, offline
> machines that ship `runtime/parsers`).

> ℹ️ **No launcher script is needed.** Start `bin/TreeSitterMCP` (or
> `bin\TreeSitterMCP.exe`) directly. It loads the native FFI library from its own
> folder and finds `runtime/parsers` next to `bin/`. If you move it, move the
> whole folder (`bin/` and `runtime/` together), or pass `--parser-cache`.

### Run it

```sh
./bin/TreeSitterMCP --allow-root /home/me/code
```

The server reads MCP messages on stdin and writes them to stdout, so it **waits
silently**. That is expected: your AI client starts it and talks to it. Press
`Ctrl+C` to stop it.

---

## 🔌 Connect your AI client

Use absolute paths everywhere. `--allow-root` is the folder (or folders) the
assistant is allowed to see. Repeat it to allow several.

### Claude Desktop

Open the config file (**Settings → Developer → Edit Config**):

- macOS: `~/Library/Application Support/Claude/claude_desktop_config.json`
- Windows: `%APPDATA%\Claude\claude_desktop_config.json`
- Linux: `~/.config/Claude/claude_desktop_config.json`

**macOS / Linux**

```json
{
  "mcpServers": {
    "TreeSitterMCP": {
      "command": "/absolute/path/to/TreeSitterMCP/bin/TreeSitterMCP",
      "args": ["--allow-root", "/absolute/path/to/code"]
    }
  }
}
```

**Windows**

```json
{
  "mcpServers": {
    "TreeSitterMCP": {
      "command": "C:\\tools\\TreeSitterMCP\\bin\\TreeSitterMCP.exe",
      "args": ["--allow-root", "C:\\code"]
    }
  }
}
```

Restart Claude Desktop completely. TreeSitterMCP should then appear in the
tools menu. Ready-made files are in [`examples/`](examples).

### Claude Code

```sh
# macOS / Linux
claude mcp add TreeSitterMCP --scope user -- \
  /absolute/path/to/TreeSitterMCP/bin/TreeSitterMCP --allow-root /absolute/path/to/code

# Windows
claude mcp add TreeSitterMCP --scope user -- C:\tools\TreeSitterMCP\bin\TreeSitterMCP.exe --allow-root C:\code
```

Run `claude mcp list` to confirm the connection.

### Cursor, Windsurf, VS Code, and other MCP clients

Any client that supports **stdio** MCP servers works. Use the same `command` and
`args` as the Claude Desktop example above, in the client's MCP config:

| Client | Config location | Top-level key |
| --- | --- | --- |
| Cursor | `~/.cursor/mcp.json` or `.cursor/mcp.json` | `mcpServers` |
| Windsurf | `~/.codeium/windsurf/mcp_config.json` | `mcpServers` |
| VS Code (Copilot) | `.vscode/mcp.json` | `servers` (add `"type": "stdio"`) |

---

## 💬 What you can ask

You don't call the tools yourself. You ask the assistant in plain language and it
chooses the tools. Start each session by telling it which project to use:

> Register `/home/me/code/my-app` as project **my-app** with TreeSitterMCP.

Then try:

| You ask | Tools it typically uses |
| --- | --- |
| "Give me an overview of my-app: structure, languages, size." | `project_overview`, `get_project_tree` |
| "Show only the folder structure, 4 levels deep." | `get_project_tree` (`folders_only`) |
| "Find every test file." | `find_paths` (`**/*_test.go`, `**/test_*.py`, ...) |
| "Explain what `src/server.py` does." | `explain_file` prompt, `get_ast` |
| "List all function names in `main.py`." | `query_ast` |
| "Where is `parse_config` used?" | `search_text` (`whole_word`) |
| "Find every `TODO` in Go files, with 2 lines of context." | `search_text` (`path_glob`, `context_lines`) |
| "Which files have syntax errors?" | `query_project`, `get_parse_diagnostics` |
| "What is the AST node at line 42, column 8? Show its parent." | `get_node_at_position`, `navigate_ast` |
| "Find code that is structurally similar to this function." | `find_similar_subtrees` |
| "Snapshot `app.ts`. I'll edit it, then tell me what changed structurally." | `create_ast_snapshot`, `compare_ast_snapshots` |
| "How big and deep is the syntax tree of this file?" | `get_ast_metrics` |

> 💡 Ask for the tree or the overview **before** asking the assistant to read
> files. It will then read only what matters.

---

## 🧠 Core concepts

| Concept | What it means |
| --- | --- |
| **Allow root** | A folder you permit at startup with `--allow-root`. Nothing outside it is ever visible, and tool calls cannot widen it. |
| **Project** | A name registered (by the assistant) for a folder inside an allow root. Registrations persist across restarts. Nothing is registered automatically. |
| **Paths** | Always relative to the project root, with forward slashes (`src/main.go`). |
| **Positions** | Lines are 1-based. Columns and byte offsets are 0-based **UTF-8 bytes** (not UTF-16/LSP). Byte spans are half-open. |
| **Pagination** | Responses contain at most `limit` entries (1-200, default 50) and fit `max_bytes`. If `next_cursor` is present, repeat the same call with `cursor` set to get the next page. |
| **`truncated` / `incomplete`** | `truncated` means more pages exist. `incomplete` means a safety limit stopped the scan, so counts are lower bounds. Narrow `path` to get a complete result. |
| **Node IDs** | Stable for identical file content, even after cache eviction or restart. Editing the file invalidates them. |
| **State directory** | Private storage for registrations, snapshots, and caches. Defaults to your user config folder (`.../TreeSitterMCP`). Keep it outside your projects. |

---

## 🧰 Tool reference

25 core tools are enabled by default. They work the same way on every grammar.

### Projects

| Tool | Required | Purpose |
| --- | --- | --- |
| `register_project` | `project`, `root` | Name a folder inside an allow root |
| `list_projects` | none | List registered projects |
| `unregister_project` | `project` | Remove a registration (files are untouched) |

### Exploring folders (no parsing needed)

| Tool | Required | Purpose |
| --- | --- | --- |
| `get_project_tree` | `project` | Folders-first tree, Markdown view, recursive stats |
| `get_directory_info` | `project` | Directory summary plus paginated children |
| `find_paths` | `project`, `pattern` | Glob or regex search over file and folder paths |
| `project_overview` | `project` | File/folder counts, languages, total size |
| `list_files` | `project` | Regular files, optionally filtered by `path` and `language` |

### Reading and searching source

| Tool | Required | Purpose |
| --- | --- | --- |
| `read_file` | `project`, `path` | Bounded line ranges (default 200 lines) |
| `get_file_metadata` | `project`, `path` | Size, lines, modification time, SHA-256, language |
| `search_text` | `project`, `pattern` | Literal/RE2 search, case folding, whole word, globs, context |

### Syntax trees

| Tool | Required | Purpose |
| --- | --- | --- |
| `get_ast` | `project`, `path` | Depth-limited named-node AST, optional byte-range focus |
| `get_node_at_position` | `project`, `path` | Deepest node at `line`+`column` or `start_byte` |
| `navigate_ast` | `project`, `path` | `self`, `parent`, `children`, `siblings`, `ancestors`, `subtree` from a `node_id` |
| `query_ast` | `project`, `path`, `query` | Native Tree-sitter query captures on one file |
| `query_project` | `project`, `query` | Same query across many files, with per-file status |
| `get_parse_diagnostics` | `project`, `path` | ERROR and MISSING syntax-recovery ranges |
| `get_ast_metrics` | `project`, `path` | Node/leaf/depth/error counts and node-type histogram |

### Snapshots and similarity

| Tool | Required | Purpose |
| --- | --- | --- |
| `create_ast_snapshot` | `project`, `path` | Save a structural snapshot of a file |
| `compare_ast_snapshots` | `project`, `snapshot`, `other_snapshot` | Ordered structural diff between two snapshots |
| `find_similar_subtrees` | `project`, `path` | Rank structurally similar subtrees for a `node_id` |

### Grammars and cache

| Tool | Required | Purpose |
| --- | --- | --- |
| `list_languages` | none | All 371 grammar names |
| `get_language_info` | `language` | ABI and counts, or paginated `node_types` / `fields` |
| `cache_stats` | none | Cache counters, sizes, limits, TTL |
| `clear_cache` | none | Clear everything, one `project`, or one `project` + `path` |

### Prompts

| Prompt | Required | Gives the assistant |
| --- | --- | --- |
| `explain_file` | `project`, `path` | A bounded AST of the file |
| `explain_subtree` | `project`, `path`, `node_id` | The selected subtree |
| `explain_query` | `project`, `path`, `query` | Real, validated query captures |
| `explore_project` | `project` | The project tree and statistics |

### Optional language-specific tools

Start the server with `--enable-language-specific` to add two more tools (27 total):

| Tool | Purpose |
| --- | --- |
| `get_symbols` | Definition outline (functions, classes, ...) using common grammar shapes |
| `get_dependencies` | Import/include edges for Go, Python, JS/TS, Rust, C/C++, C#, Java, Kotlin, Swift, Dart, Julia |

These rely on per-language heuristics, so they are opt-in and outside the uniform
guarantee.

> The MCP schema each tool advertises is the source of truth for its arguments.
> Unknown arguments and `null` values are rejected.

---

## 🧪 Tool call examples

These are `tools/call` parameter objects, useful when you write your own client
or debug one. The fixture in [`examples/project`](examples/project) works with
all of them. Register it first:

```json
{"name": "register_project", "arguments": {"project": "demo", "root": "/absolute/path/to/TreeSitterMCP/examples/project"}}
```

<details>
<summary><b>Project tree</b></summary>

```json
{"name": "get_project_tree", "arguments": {"project": "demo", "depth": 3, "limit": 50}}
```

Rendered `markdown` field:

- **demo/**
  - **docs/**
    - docs/guide.md
  - **empty/**
  - **src/**
    - src/helper.go
  - README.md
  - main.py

Options: `path`, `depth` (1-32, root is 0), `folders_only`, `include_hidden`,
`extensions` (for example `["go", ".py"]`), `limit`, `max_bytes`, `cursor`.
Directory `stats` describe the whole admitted subtree, before display filters and
depth limits.
</details>

<details>
<summary><b>Directory info</b></summary>

```json
{"name": "get_directory_info", "arguments": {"project": "demo", "path": "src", "limit": 20}}
```

Returns recursive file/folder counts, total file bytes, language and extension
counts, and a page of immediate children.
</details>

<details>
<summary><b>Find paths (glob and regex)</b></summary>

```json
{"name": "find_paths", "arguments": {"project": "demo", "pattern": "**/*.go"}}
{"name": "find_paths", "arguments": {"project": "demo", "pattern": "^src/.*[.]py$", "regex": true}}
```

`*.go` (no slash) matches basenames anywhere. `**` matches zero or more folders,
so `**/*.go` also matches root-level files. Regexes match the project-relative path.
</details>

<details>
<summary><b>Read a range and search text</b></summary>

```json
{"name": "read_file", "arguments": {"project": "demo", "path": "main.py", "start_line": 1, "end_line": 20}}
{"name": "search_text", "arguments": {"project": "demo", "pattern": "def\\s+add", "regex": true}}
{"name": "search_text", "arguments": {"project": "demo", "pattern": "add", "ignore_case": true, "whole_word": true, "path_glob": "**/*.py", "context_lines": 2}}
```

Search is line-based. `context_lines` is 0-5.
</details>

<details>
<summary><b>AST, position lookup, and navigation</b></summary>

```json
{"name": "get_ast", "arguments": {"project": "demo", "path": "main.py", "depth": 3}}
{"name": "get_node_at_position", "arguments": {"project": "demo", "path": "main.py", "line": 2, "column": 0}}
{"name": "navigate_ast", "arguments": {"project": "demo", "path": "main.py", "node_id": "<node-id>", "relation": "parent"}}
```

Omit `node_id` in `navigate_ast` to start at the root. `subtree` accepts depth 1-32.
</details>

<details>
<summary><b>Tree-sitter queries</b></summary>

```json
{"name": "query_ast", "arguments": {"project": "demo", "path": "main.py", "query": "(function_definition name: (identifier) @name)"}}
{"name": "query_project", "arguments": {"project": "demo", "path_glob": "**/*.py", "query": "(function_definition name: (identifier) @name)"}}
```

Queries use the target grammar's node names. Use
`get_language_info` with `"section": "node_types"` to discover them. `#eq?` and
`#match?` predicates are supported; unknown predicates are rejected rather than
silently ignored. To hunt for broken code, use `(_) @node (ERROR) @error`.
</details>

<details>
<summary><b>Diagnostics and metrics</b></summary>

```json
{"name": "get_parse_diagnostics", "arguments": {"project": "demo", "path": "main.py"}}
{"name": "get_ast_metrics", "arguments": {"project": "demo", "path": "main.py", "limit": 20}}
```

These are syntax diagnostics and metrics. They are not compiler warnings, type
errors, or cyclomatic complexity.
</details>

<details>
<summary><b>Snapshots and structural similarity</b></summary>

```json
{"name": "create_ast_snapshot", "arguments": {"project": "demo", "path": "main.py"}}
{"name": "compare_ast_snapshots", "arguments": {"project": "demo", "snapshot": "<before-id>", "other_snapshot": "<after-id>"}}
{"name": "find_similar_subtrees", "arguments": {"project": "demo", "path": "main.py", "node_id": "<node-id>", "min_score": 0.8}}
```

Create a snapshot, edit the file, create another, then compare them. Snapshots
store compact structure and hashes, not your source text. Similarity is
structural (shape and node kinds), within one grammar, with `min_score` 0-1
(default 0.7).
</details>

<details>
<summary><b>Cache control</b></summary>

```json
{"name": "cache_stats", "arguments": {}}
{"name": "clear_cache", "arguments": {"project": "demo", "path": "main.py"}}
```

Omit `path` to clear a whole project, or omit both to clear everything.
Registrations and installed parsers are kept.
</details>

---

## ⚙️ Configuration

Pass these flags to `bin/TreeSitterMCP` (or in your client's `args`):

| Flag | Default | Description |
| --- | --- | --- |
| `--allow-root <dir>` | **required** | Folder the assistant may access. Repeat to allow several. |
| `--state-dir <dir>` | user config dir `/TreeSitterMCP` | Registrations, snapshots, derived caches |
| `--parser-cache <dir>` | `runtime/parsers` next to `bin/` (auto-detected), else the user cache | Installed grammar location |
| `--auto-install` | `true` | Install the pinned parser bundle in the background when any grammar is missing or damaged |
| `--max-file-bytes` | `2097152` (2 MiB) | Largest source file that will be parsed or read |
| `--max-cache-bytes` | `33554432` (32 MiB) | Retained source and derived-cache bytes |
| `--max-cache-entries` | `64` | Native parse trees kept in memory |
| `--max-response-bytes` | `24000` | Maximum JSON analysis payload per response |
| `--max-files` | `20000` | Files admitted per project scan |
| `--max-nodes` | `200000` | AST nodes per file |
| `--cache-ttl` | `15m` | Cache and snapshot lifetime |
| `--timeout` | `10s` | Maximum duration of one tool call, including queue time |
| `--enable-language-specific` | off | Enable `get_symbols` and `get_dependencies` |
| `--version` | | Print version and exit |

Example with several roots and a larger budget:

```sh
./bin/TreeSitterMCP \
  --allow-root /home/me/work \
  --allow-root /home/me/oss \
  --max-response-bytes 48000 \
  --timeout 30s
```

---

## 🔒 Security model

TreeSitterMCP is built to be safe to point at real code:

- **Explicit access only.** Only `--allow-root` folders are reachable, through Go's
  anchored `os.Root` handles. Tool calls cannot widen access.
- **No code execution.** Source is parsed, never run.
- **Sensitive paths are excluded:** `.git`, `.hg`, `.svn`, `node_modules`,
  virtual environments, build folders, `.env*`, credential files and folders,
  private keys, and the server's own state directory. `include_hidden` cannot
  override these.
- **Symlinks are not followed** during scans, and reads reject symlinked paths.
- **Bounded everything:** file size, response size, AST size, scan size, query
  size, and call duration all have limits.
- **Prompt-injection aware:** file names, source, and tool output are labeled as
  untrusted data, not instructions.

Notes: built-in exclusions are rules, not a full `.gitignore` interpreter or a
secret scanner. Native parser libraries are trusted code. Run the server as a
normal user. See [SECURITY.md](SECURITY.md) to report a vulnerability.

---

## 📦 Use as a Go library

Embed the engine without MCP:

```go
import "github.com/hamedfathi/TreeSitterMCP/server"

engine, err := server.New(server.Options{
    AllowRoots: []string{"/absolute/path/to/code"},
    StateDir:   "/absolute/path/to/private/state",
})
if err != nil {
    return err
}
defer engine.Close()

result, err := engine.Execute(ctx, "get_project_tree", server.Input{
    Project:     "demo", // register it first with "register_project"
    Depth:       3,
    FoldersOnly: true,
})
```

`server.NewMCP(engine)` returns an official MCP Go SDK server if you want your own
transport. See [`examples/embedded`](examples/embedded).

The `projecttree` package has **no parser or CGO dependency** and scans any `fs.FS`:

```go
root, err := os.OpenRoot("/absolute/path/to/code/demo")
if err != nil {
    return err
}
defer root.Close()

snap, err := projecttree.Scan(ctx, root.FS(), projecttree.Options{Root: ".", MaxEntries: 80000, MaxFiles: 20000})
if err != nil {
    return err
}
fmt.Println(projecttree.Markdown(snap.Select(3, true, nil)))
```

---

## 🛠 Development and testing

```sh
./scripts/install.sh            # once: native lib, binaries, parsers
go test ./...                   # unit and integration tests
go test -race ./...             # race detector
go vet ./...
go test ./projecttree           # no grammars or CGO required

# Optional: cross-SDK stdio tests with the official Python MCP client
python3 -m pip install -r requirements-test.txt
python3 scripts/test_stdio.py
python3 scripts/test_all_languages_stdio.py
```

Set `TREESITTERMCP_TEST_CACHE` to use a different prepared parser cache in tests.

### Repository layout

```text
cmd/TreeSitterMCP/   MCP server entry point
cmd/packctl/         parser install/verify utility
cmd/native-setup/    native FFI library preparation
server/              analysis engine and MCP adapter
projecttree/         standalone folder-scanning package
internal/catalog/    pinned grammar and native-library manifests
third_party/         pinned Go binding, grammar licenses, provenance
scripts/             install, build, packaging, and stdio test scripts
examples/            client configs, embedding example, demo project
docs/                language list, uniformity policy, test report
```

### Continuous integration and releases

| Workflow | Trigger | What it does |
| --- | --- | --- |
| [`ci.yml`](.github/workflows/ci.yml) | Push to `main`, pull requests, manual | gofmt, `go mod tidy`, then on **all six platforms**: install, `go vet`, `go test`, race detector, and MCP stdio tests on every grammar |
| [`release.yml`](.github/workflows/release.yml) | Tag `v*.*.*`, manual dry run | Checks the tag matches `server.Version`, runs the full CI, builds six archives, tests each **extracted** archive with a real MCP client, and publishes a GitHub release with `SHA256SUMS.txt` and notes from `CHANGELOG.md` |

Platforms: Linux x86-64 / ARM64, macOS Intel / Apple Silicon, Windows x86-64 / ARM64.

**To publish a release:**

1. Set `Version` in [`server/types.go`](server/types.go) and the Python test
   expectations, and add a `## <version> - <date>` section to `CHANGELOG.md`.
2. Commit, then tag and push:

   ```sh
   git tag v1.0.0
   git push origin v1.0.0
   ```

3. Watch the **Release** workflow. The release appears when all six builds pass.

To try the release pipeline without publishing, run **Release** manually from the
Actions tab. The archives are attached to the run as artifacts.

More detail: [language catalog](docs/LANGUAGES.md) ·
[uniform support policy](docs/UNIFORMITY.md) ·
[verification report](docs/TEST_REPORT.md).

---

## 🩺 Troubleshooting

| Symptom | Fix |
| --- | --- |
| Client says the server failed to start | Run the exact `command` + `args` from your config in a terminal and read stderr. Use absolute paths. |
| Client reports invalid JSON | Something is printing to stdout. Point the client at the binary directly, not a wrapper script that echoes text or loads a noisy shell profile. |
| `at least one --allow-root is required` | Add `--allow-root /path` to the args. |
| Project root rejected | The folder must be inside a configured `--allow-root`. |
| Native FFI library not found / DLL error | Run `install` or `build` again. The `ts_pack_core_ffi` library must sit next to the executable in `bin/`; if you copy the binary elsewhere, copy that file with it. |
| `cgo: C compiler "gcc" not found` | Install a C toolchain (see [Requirements](#option-b-build-from-source-linux-macos-windows)) and set `CGO_ENABLED=1`. |
| Parser not installed | The server installs parsers automatically on first start (needs network once, or `runtime/bundles/parsers-<platform>.tar.zst`). Check its stderr log for the cause; you can also run `bin/packctl --download-all --verify-all` (add `--force` to reinstall). |
| Windows blocks `install.ps1` | Run `powershell -ExecutionPolicy Bypass -File .\scripts\install.ps1`, or `Unblock-File` on the scripts. |
| Tree looks empty | Check `include_hidden`, `extensions`, the selected `path`, built-in exclusions, and the `incomplete` flag. |
| "Invalid or stale cursor" | The files changed or the arguments differ. Restart from the first page with the same filters. |
| "Exceeds budget" | Raise `max_bytes` (up to `--max-response-bytes`) or narrow `path`. |
| File rejected | It must be a regular, non-symlinked UTF-8 file under `--max-file-bytes`. |
| `get_symbols` not found | Start with `--enable-language-specific`, or use `query_ast`. |
| Query returns nothing | Node names differ per grammar. Check them with `get_language_info` (`section: node_types`). |
| `go` is not recognized | Install Go 1.26+ and make sure `PATH` points to it. |

---

## ❓ FAQ

**Does my code leave my machine?**
No. TreeSitterMCP runs locally over stdio and makes no network calls at runtime.
Your MCP client decides what it sends to its model.

**Which languages are supported?**
371 Tree-sitter grammars. See [docs/LANGUAGES.md](docs/LANGUAGES.md). APL is not
in the pinned upstream pack and is not supported.

**Is this a language server (LSP)?**
No. It works at the syntax level: structure, queries, positions, and diagnostics.
It does not resolve types, follow calls across files, or run compilers.

**Can it modify my files?**
No. Every tool is read-only for your projects. The server writes only to its own
state directory.

**Do I need Python or Rust?**
No. Python is needed only for the optional interoperability tests.

---

## ⚠️ Limitations

- CI builds and tests all six platforms, but day-to-day use has mostly been on
  Linux. Platform-specific reports are welcome.
- Analysis is syntactic. There is no type resolution or cross-file call graph.
- Similarity and diffs are structural heuristics, not semantic equivalence.
- Built-in exclusions are not a full `.gitignore` implementation or secret
  detector, and they are not a sandbox around native parser code.
- Folder statistics are point-in-time scans and can go stale if files change.
- The bundled cmake parser (language pack 1.25.0) has an upstream scanner bug:
  it reads uninitialized memory while recovering from syntax errors, so
  malformed CMake can occasionally produce different trees for identical
  input. Valid CMake parses consistently.

---

## 🤝 Contributing

Contributions are welcome: bug reports, docs, tests, and features.

1. Read [CONTRIBUTING.md](CONTRIBUTING.md).
2. Fork, create a branch, and make focused changes formatted with `gofmt`.
3. Run `go test ./...` and `go vet ./...`.
4. Open a pull request describing the problem, the change, and how you tested it.

If you find TreeSitterMCP useful, please ⭐ **star the repository**. It helps
other people find it.

---

## 📄 License

TreeSitterMCP is free and open source under the [MIT License](LICENSE).

Third-party code and grammars keep their own licenses. See [NOTICE](NOTICE),
[`third_party/GRAMMAR_NOTICES.md`](third_party/GRAMMAR_NOTICES.md),
[`third_party/grammar-provenance.json`](third_party/grammar-provenance.json), and
[`third_party/SOURCE.md`](third_party/SOURCE.md).

Built on:

- [Official MCP Go SDK v1.1.0](https://github.com/modelcontextprotocol/go-sdk/tree/v1.1.0)
- [Tree-sitter Go binding v0.25.0](https://github.com/tree-sitter/go-tree-sitter/tree/v0.25.0)
- [Tree-sitter language pack v1.25.0](https://github.com/xberg-io/tree-sitter-language-pack/releases/tag/v1.25.0)
