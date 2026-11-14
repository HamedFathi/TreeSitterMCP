package server

import (
	sitter "github.com/tree-sitter/go-tree-sitter"
	"os"
	"path"
	"strconv"
	"strings"
)

func literal(s string) string {
	if u, err := strconv.Unquote(s); err == nil {
		return u
	}
	return strings.Trim(s, "\"'`<>")
}
func extractDependencies(file, lang string, n *sitter.Node, b []byte) []Dependency {
	targets := []string{}
	kind := "import"
	text := strings.TrimSpace(n.Utf8Text(b))
	k := n.Kind()
	addField := func(name string) {
		if x := n.ChildByFieldName(name); x != nil {
			targets = append(targets, literal(x.Utf8Text(b)))
		}
	}
	switch {
	case lang == "go" && k == "import_spec":
		addField("path")
	case (lang == "javascript" || lang == "typescript" || lang == "tsx") && (k == "import_statement" || k == "export_statement"):
		addField("source")
	case (lang == "javascript" || lang == "typescript" || lang == "tsx") && k == "call_expression":
		f, args := n.ChildByFieldName("function"), n.ChildByFieldName("arguments")
		if f != nil && f.Utf8Text(b) == "require" && args != nil && args.NamedChildCount() > 0 {
			if x := args.NamedChild(0); x.Kind() == "string" {
				targets = append(targets, literal(x.Utf8Text(b)))
				kind = "require"
			}
		}
	case lang == "python" && k == "import_from_statement":
		addField("module_name")
	case lang == "python" && k == "import_statement":
		for i := uint(0); i < n.NamedChildCount(); i++ {
			x := n.NamedChild(i)
			if x.Kind() == "aliased_import" {
				if y := x.ChildByFieldName("name"); y != nil {
					x = y
				}
			}
			if x.Kind() == "dotted_name" {
				targets = append(targets, x.Utf8Text(b))
			}
		}
	case (lang == "c" || lang == "cpp") && k == "preproc_include":
		addField("path")
		kind = "include"
	case lang == "rust" && k == "use_declaration":
		targets = append(targets, strings.TrimSuffix(strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(text, "pub "), "use ")), ";"))
	case lang == "csharp" && k == "using_directive":
		t := strings.TrimSuffix(strings.TrimSpace(strings.TrimPrefix(text, "using ")), ";")
		t = strings.TrimPrefix(t, "static ")
		if i := strings.Index(t, "="); i >= 0 {
			t = strings.TrimSpace(t[i+1:])
		}
		targets = append(targets, t)
	case lang == "java" && k == "import_declaration":
		targets = append(targets, strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(strings.TrimPrefix(text, "import ")), "static "), ";"))
	case lang == "kotlin" && k == "import_header":
		f := strings.Fields(strings.TrimPrefix(text, "import "))
		if len(f) > 0 {
			targets = append(targets, f[0])
		}
	case lang == "swift" && k == "import_declaration":
		f := strings.Fields(text)
		if len(f) > 1 {
			targets = append(targets, f[len(f)-1])
		}
	case lang == "dart" && (k == "import_specification" || k == "export_specification"):
		for i := uint(0); i < n.NamedChildCount(); i++ {
			x := n.NamedChild(i)
			for (x.Kind() == "configurable_uri" || x.Kind() == "uri") && x.NamedChildCount() > 0 {
				x = x.NamedChild(0)
			}
			if strings.Contains(x.Kind(), "string") {
				targets = append(targets, literal(x.Utf8Text(b)))
				break
			}
		}
	case lang == "julia" && (k == "import_statement" || k == "using_statement"):
		t := strings.TrimPrefix(strings.TrimPrefix(text, "using "), "import ")
		if i := strings.Index(t, ":"); i >= 0 {
			t = t[:i]
		}
		for _, v := range strings.Split(t, ",") {
			targets = append(targets, strings.TrimSpace(v))
		}
	}
	out := []Dependency{}
	for _, t := range targets {
		if t != "" {
			out = append(out, Dependency{Source: file, Target: snippet([]byte(t), 256), Kind: kind, Span: span(n)})
		}
	}
	return out
}
func (e *Engine) resolveDependency(r *os.Root, p Project, file, lang, target string) string {
	candidates := []string{}
	base := path.Dir(file)
	switch lang {
	case "javascript", "typescript", "tsx":
		if strings.HasPrefix(target, ".") {
			v := path.Join(base, target)
			candidates = append(candidates, v)
			for _, ext := range []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".json"} {
				candidates = append(candidates, v+ext, path.Join(v, "index"+ext))
			}
		}
	case "python":
		dots := len(target) - len(strings.TrimLeft(target, "."))
		v := strings.ReplaceAll(target[dots:], ".", "/")
		if dots > 0 {
			for i := 1; i < dots; i++ {
				base = path.Dir(base)
			}
			v = path.Join(base, v)
		}
		candidates = append(candidates, v+".py", path.Join(v, "__init__.py"))
	case "c", "cpp":
		candidates = append(candidates, path.Join(base, target), path.Join("include", target), target)
	case "go":
		if b, err := e.read(r, p, "go.mod"); err == nil {
			for _, line := range strings.Split(string(b), "\n") {
				f := strings.Fields(line)
				if len(f) == 2 && f[0] == "module" && strings.HasPrefix(target, f[1]+"/") {
					v := strings.TrimPrefix(target, f[1]+"/")
					if !blocked(v) && verifyPath(r, v) == nil {
						if st, err := r.Stat(v); err == nil && st.IsDir() {
							return v
						}
					}
				}
			}
		}
	case "rust":
		if strings.HasPrefix(target, "crate::") {
			v := strings.ReplaceAll(strings.TrimPrefix(target, "crate::"), "::", "/")
			candidates = append(candidates, path.Join("src", v+".rs"), path.Join("src", v, "mod.rs"))
		}
	}
	for _, c := range candidates {
		if _, err := cleanPath(c); err != nil {
			continue
		}
		if verifyPath(r, c) != nil {
			continue
		}
		if st, err := r.Stat(c); err == nil && st.Mode().IsRegular() {
			return c
		}
	}
	return ""
}
