# Pinned source provenance

The local Go language-pack binding comes from the official module:

`github.com/xberg-io/tree-sitter-language-pack/packages/go@v1.25.0`

Source archive: https://proxy.golang.org/github.com/xberg-io/tree-sitter-language-pack/packages/go/@v/v1.25.0.zip

Source archive SHA-256: `ee0f52f489ccd1ff104d4eeff0066a9986e1194e2b830556999259673879d392`

Binding source, header files, and its MIT license are retained. Upstream platform
dynamic FFI libraries are included in complete release assets and omitted from
ordinary source commits. Unused static .a/.lib archives and upstream tests are
omitted. No grammar is relabeled as another language.

The parser release assets and their SHA-256 values are pinned in
internal/catalog/parsers.json. Native-library hashes and the matching module
archive are pinned in internal/catalog/native.json. Source checkouts restore
ignored native assets through cmd/native-setup and cmd/packctl.

Grammar repository/revision provenance and license texts accompany the parsers in
GRAMMAR_NOTICES.md and grammar-provenance.json. Every third-party component
retains its own license. The server's MIT license does not replace those licenses.
