# Verification report - 1.0.0

Date: 2026-11-14. Execution environment: Linux x86-64 with glibc, Go 1.27.4,
GCC 14.2.0, Python 3.12.14, official Python MCP SDK 1.29.0.

| Check | Actual recorded result |
| --- | --- |
| Named Go tests including subtests | 820 passed; no failures |
| Complete canonical grammar contract | 371/371 passed with nonempty input |
| Native grammar ABI/load checks | 371/371 loaded and parsed |
| Additional curated valid source fixtures | 13/13 parsed without root errors |
| Race detector | go test -race ./... passed |
| Static analysis | go vet ./... passed |
| Server statement coverage | 81.5% |
| Independent projecttree statement coverage | 87.5% |
| Official Python MCP SDK smoke test | 25/25 core tools and 4/4 prompts; 42 tool calls |
| All-grammar real stdio integration | 371/371; 5,567 tool calls; 1,484 prompt calls |
| MCP protocol | 2025-06-18 negotiated with official clients |
| Registration/snapshot restart persistence | Passed across separate server processes |
| Cache policy | File/project/global invalidation, TTL, LRU and disk caps checked |
| Stable node identifiers | Eviction and process restart preserve IDs; source edits reject old IDs |
| Fresh source-checkout simulation | Ignored native assets restored and verified; binaries rebuilt; full smoke test passed |
| Fresh offline parser installation | 371/371 installed and verified from bundled Linux archive |
| Release bundle integrity | All six platform archives match pinned SHA-256 values |
| Source language | No Persian/Arabic-script text in maintained source or documentation |

## Uniform contract

The same AST mechanics were exercised on every pinned grammar: native vocabulary
and field introspection (including complete paginated enumeration), file metadata,
position lookup, parent/child/sibling/ancestor/subtree navigation, diagnostics,
metrics, native query captures, multi-file queries, enhanced search, snapshots,
comparison, structural similarity, and cache operations. Runtime MCP integration
also exercised these AST operations and all four prompts on every grammar.
File/project hierarchy operations are grammar-independent and have separate
filesystem fixtures. No core operation assumes function/class/import node names.

The per-language evidence in verification/language-contracts.json records source
hashes, byte counts, node counts, diagnostics and vocabulary counts. The separate
verification/all-languages-stdio.json records actual transport results for every
canonical grammar. Raw Go JSON events, race/vet logs, coverage and installation
reports are retained in verification/. Relocation evidence is added after testing
the actual extracted ZIP in a directory containing spaces.

## Source fixture scope

All grammars receive nonempty source. Thirteen programming languages use curated
valid snippets: Python, JavaScript, TypeScript, Go, Rust, C, C++, C#, Swift, Java,
Kotlin, Dart and Julia. Other engine cases use the synthetic source
`sample = 1` followed by `sample = 2`. The all-grammar transport test uses that
shared recovery probe for every grammar. These probes intentionally test that
core capabilities remain available when parsing malformed input. They are not
misrepresented as valid syntax in every language. ERROR/MISSING output can be
empty on permissive grammars; this is a valid result.

Focused regressions check Unicode/byte positions, unknown query node kinds,
invalid predicates, regexes, path globs, word boundaries, context lines, changed
multi-file cursors, leaf renames in AST diffs, same-shape function comparisons,
cross-grammar rejection, cache eviction, TTL, bounded empty-directory cleanup,
JSON budgets, syntax diagnostics, persistence, concurrency, cancellation, and
root/link/credential boundaries. Legacy symbol/import tests run with explicit
opt-in and are excluded from the universal feature guarantee.

## Reproduce

```sh
./scripts/install.sh
go test -json -coverprofile=coverage.out ./...
go test -race ./...
go vet ./...
python3 -m pip install -r requirements-test.txt
python3 scripts/test_stdio.py
python3 scripts/test_all_languages_stdio.py
```

## Limits of verification

- Tests are regression evidence, not proof of exhaustive syntax correctness or a
  formal security audit of native parsers.
- Uniformity means the same generic API mechanics. Native grammar vocabularies,
  accepted queries and source structures differ. Semantic symbol/import analysis
  remains optional and language-dependent.
- Structural diffs are ordered child-address comparisons, not minimal edit scripts.
  Whitespace edits can leave structure unchanged. Similarity is structural and
  its histogram approximation can have hash collisions.
- APL is absent from the pinned pack and unsupported; aliases are not additional
  canonical languages.
- macOS/Windows assets and scripts are present but were not executed here.
- The Claude Desktop GUI and hosted GitHub CI were not run. Real stdio protocol
  interoperability was verified with official Go and Python SDK clients.
- Native grammar/tree memory is additional to bounded source caches; cancellation
  is cooperative and native parser code runs in-process.
