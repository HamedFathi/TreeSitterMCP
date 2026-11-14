# Contributing

## Development setup

Use Go 1.26 or newer and a C toolchain. Run `./scripts/install.sh` on Linux/macOS
or `./scripts/install.ps1` on Windows. Native libraries and parser bundles are
pinned by committed checksums; do not replace them with untracked local builds
when reporting reproducible results.

Run the focused tests for your change, then:

```sh
go test ./...
go test -race ./...
go vet ./...
python3 -m pip install -r requirements-test.txt
python3 scripts/test_stdio.py
python3 scripts/test_all_languages_stdio.py
```

`go test ./projecttree` is independent of parser libraries. Source-analysis tests
need the prepared full grammar cache. Tests must use temporary project/state
directories and must never read contributor credentials or execute fixture code.

## Pull requests

Describe the concrete problem, resulting behavior, validation, and limitations.
Keep changes focused, format Go code with gofmt, and update the README/tool schemas
and changelog when behavior changes. Add meaningful regression cases for bugs,
especially path confinement, budgets, cancellation, stale cursors, metadata scan
limits, and native tree ownership.

All maintained source, comments, examples, and documentation are in English.
Avoid generated binaries, parser caches, build output, and machine-specific paths
in source commits. Preserve upstream source/attribution and all third-party
licenses. Use synthetic fixtures rather than proprietary code.

## Grammars and native dependencies

Changing the pack version requires updating both parser and FFI manifests,
verifying every platform archive checksum, retaining the matching redistribution
notices/provenance, loading every canonical parser, and exercising nonempty
fixtures and the complete TestEveryGrammarContract matrix for every language. Alias names must not inflate the language count.
A successful empty parse is an ABI/load check, not evidence of complete grammar
correctness.

Do not silently ignore unsupported predicates, claim unsupported languages, or
weaken filesystem/response boundaries to make a fixture pass. The default core must use grammar-independent AST mechanics. Optional symbol
and dependency extensions must remain opt-in and document grammar-specific
limits. Do not turn failed analyses into empty successful responses.

## Reports

Bug reports should include OS/architecture, Go/server/pack versions, exact tool
arguments, expected and actual behavior, and a minimal synthetic reproduction.
Remove secrets and private source before posting logs. Report security-sensitive
issues using the guidance in SECURITY.md rather than a public reproduction.
