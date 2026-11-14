# Uniform core support policy

The default server exposes 25 tools and four prompts. Every installed canonical
grammar uses the same core implementations; this release pins all 371 grammars.
The guarantee covers mechanics, syntax ranges and bounded results, not language
semantics or the same node/query vocabulary.

| Requested addition | Implementation | Cross-grammar evidence |
| --- | --- | --- |
| Grammar information | get_language_info: summary, node_types, fields | Complete native vocabulary/field enumeration on 371 grammars |
| File metadata | get_file_metadata | Nonempty source size/hash verified per grammar |
| Node at position | get_node_at_position | Root-covered byte and equivalent line/column |
| AST navigation | navigate_ast plus stable IDs in get_ast | All-node hierarchy, stale IDs, eviction/restart |
| Parse diagnostics | get_parse_diagnostics | ERROR/MISSING totals agree with AST metrics |
| Generic metrics | get_ast_metrics | Histogram sums, named/anonymous counts, depth, leaves |
| Multi-file queries | query_project | Independent native validation and captures, failures, stale cursors |
| Advanced text search | search_text controls | Case folding, Unicode word boundaries, path glob, context |
| Snapshot comparison | create_ast_snapshot, compare_ast_snapshots | Identical snapshots, edits, persistence, grammar checks |
| Structural similarity | find_similar_subtrees | Identical cross-file shape and focused duplicate structures |
| Full cache management | cache_stats, clear_cache, cache-ttl option | Scope, expiry, native/disk limits, cleanup |
| Generic MCP prompts | Four prompts | All four retrieved on all 371 grammars over actual stdio |

A grammar with no fields returns an empty fields page. A valid file can have no
diagnostics or query matches. A position in grammar-ignored text can have no
covering node. A grammar-specific invalid query is an explicit error. These are
normal grammar-dependent data outcomes, not missing core implementations.

Legacy get_symbols/get_dependencies are disabled by default, since universal
semantic coverage is not justified. Explicit opt-in exposes their documented
language-dependent behavior. No successful empty result is used to disguise an
unsupported core operation.

See TEST_REPORT.md and verification/ for precise executed scope and limitations.
