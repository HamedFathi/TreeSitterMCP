module github.com/hamedfathi/TreeSitterMCP

go 1.26

require (
	github.com/klauspost/compress v1.18.0
	github.com/modelcontextprotocol/go-sdk v1.1.0
	github.com/tree-sitter/go-tree-sitter v0.25.0
	github.com/xberg-io/tree-sitter-language-pack/packages/go v1.25.0
)

require (
	github.com/google/jsonschema-go v0.3.0 // indirect
	github.com/mattn/go-pointer v0.0.1 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/oauth2 v0.30.0 // indirect
)

replace github.com/xberg-io/tree-sitter-language-pack/packages/go => ./third_party/languagepack
