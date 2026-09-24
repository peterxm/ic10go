package ic10

import (
	"os"
	"path/filepath"

	"ic10go/internal/ast"
	"ic10go/internal/diag"
	"ic10go/internal/lexer"
	"ic10go/internal/parser"
	"ic10go/internal/source"
)

// expandImports replaces every `import "path"` declaration in tree with the
// declarations of the imported file, recursively. Each imported node keeps the
// position of the file it came from, so diagnostics stay accurate. Files are
// merged once: a diamond import or a cycle that reaches an already-merged file
// is skipped.
func expandImports(tree *ast.File, mainPath string, libs []string, diags *diag.Bag) {
	l := &importLoader{libs: libs, done: map[string]bool{}, diags: diags}
	if abs, err := filepath.Abs(mainPath); err == nil {
		l.done[abs] = true
	}
	tree.Decls = l.expand(tree.Decls, filepath.Dir(mainPath))
}

type importLoader struct {
	libs  []string
	done  map[string]bool // absolute paths already merged
	diags *diag.Bag
}

func (l *importLoader) expand(decls []ast.Decl, dir string) []ast.Decl {
	out := make([]ast.Decl, 0, len(decls))
	for _, d := range decls {
		imp, ok := d.(*ast.ImportDecl)
		if !ok {
			out = append(out, d)
			continue
		}
		path, ok := l.resolve(imp.Path.Value, dir)
		if !ok {
			l.diags.Errorf(imp.Path.Pos(), "cannot find import %q", imp.Path.Value)
			continue
		}
		abs, _ := filepath.Abs(path)
		if l.done[abs] {
			continue
		}
		l.done[abs] = true

		src, err := os.ReadFile(path)
		if err != nil {
			l.diags.Errorf(imp.Path.Pos(), "cannot read import %q: %v", imp.Path.Value, err)
			continue
		}
		f := source.NewFile(path, src)
		toks := lexer.Tokenize(f, l.diags)
		sub := parser.Parse(f, toks, l.diags)

		for _, sd := range sub.Decls {
			switch x := sd.(type) {
			case *ast.ChipDecl, *ast.BusDecl, *ast.UseDecl:
				l.diags.Errorf(sd.Pos(), "imported file %s may only declare const, data and func", path)
			case *ast.FuncDecl:
				if x.Name.Name == "main" {
					l.diags.Errorf(x.Name.Pos(), "imported file %s must not declare main", path)
				}
			}
		}
		out = append(out, l.expand(sub.Decls, filepath.Dir(path))...)
	}
	return out
}

// resolve finds an import: absolute as given, then relative to the importing
// file, then in the library directories. A name without an extension also
// tries ".icg".
func (l *importLoader) resolve(name, dir string) (string, bool) {
	candidates := []string{name}
	if filepath.Ext(name) == "" {
		candidates = append(candidates, name+".icg")
	}
	try := func(p string) (string, bool) {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p, true
		}
		return "", false
	}
	roots := []string{}
	if filepath.IsAbs(name) {
		roots = append(roots, "")
	} else if dir != "" {
		roots = append(roots, dir)
	}
	roots = append(roots, l.libs...)

	for _, root := range roots {
		for _, c := range candidates {
			p := c
			if root != "" {
				p = filepath.Join(root, c)
			}
			if resolved, ok := try(p); ok {
				return resolved, true
			}
		}
	}
	return "", false
}
