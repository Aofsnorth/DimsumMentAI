// Command testexport produces the worklist for relocating white-box tests from
// internal/ to tests/.
//
// A test in tests/ is a separate package, so every package-level identifier it
// names must be exported. This walks the AST to find those references, and
// deliberately reports the ones it cannot decide on its own — unexported struct
// fields and unexported methods — because renaming those changes production
// behaviour in ways a mechanical export does not.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type member struct {
	file string
	line int
}

func main() {
	root := "internal"
	dirs := map[string]bool{}
	filepath.Walk(root, func(p string, i os.FileInfo, err error) error {
		if err != nil || i.IsDir() {
			return nil
		}
		if strings.HasSuffix(p, "_test.go") {
			dirs[filepath.Dir(p)] = true
		}
		return nil
	})

	dirList := []string{}
	for d := range dirs {
		dirList = append(dirList, d)
	}
	sort.Strings(dirList)

	nFiles, nIdents, nFields := 0, 0, 0
	for _, dir := range dirList {
		// Unexported top-level identifiers of the package.
		topLevel := map[string]bool{}
		// Unexported struct fields and unexported methods, by receiver type.
		fields := map[string]map[string]bool{}
		methods := map[string]bool{}

		fset := token.NewFileSet()
		pkgs, _ := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
			return !strings.HasSuffix(fi.Name(), "_test.go")
		}, 0)
		for _, pkg := range pkgs {
			for fname, f := range pkg.Files {
				for _, d := range f.Decls {
					switch decl := d.(type) {
					case *ast.FuncDecl:
						if !decl.Name.IsExported() {
							if decl.Recv != nil && len(decl.Recv.List) > 0 {
								methods[decl.Name.Name] = true
							} else {
								topLevel[decl.Name.Name] = true
							}
						}
					case *ast.GenDecl:
						for _, s := range decl.Specs {
							switch spec := s.(type) {
							case *ast.TypeSpec:
								if !spec.Name.IsExported() {
									topLevel[spec.Name.Name] = true
								}
								if st, ok := spec.Type.(*ast.StructType); ok {
									for _, fl := range st.Fields.List {
										for _, nm := range fl.Names {
											if !nm.IsExported() {
												if fields[spec.Name.Name] == nil {
													fields[spec.Name.Name] = map[string]bool{}
												}
												fields[spec.Name.Name][nm.Name] = true
											}
										}
									}
								}
							case *ast.ValueSpec:
								for _, nm := range spec.Names {
									if !nm.IsExported() {
										topLevel[nm.Name] = true
									}
								}
							}
						}
					}
				}
				_ = fname
			}
		}
		if len(topLevel) == 0 && len(fields) == 0 && len(methods) == 0 {
			continue
		}

		files, _ := filepath.Glob(filepath.Join(dir, "*_test.go"))
		for _, tf := range files {
			tset := token.NewFileSet()
			f, err := parser.ParseFile(tset, tf, nil, 0)
			if err != nil {
				continue
			}
			usedTop := map[string]bool{}
			usedField := map[string]bool{}
			usedMethod := map[string]bool{}

			ast.Inspect(f, func(n ast.Node) bool {
				switch node := n.(type) {
				case *ast.SelectorExpr:
					// x.field / x.method — only counts when x is a bare ident
					// that is not a known package qualifier.
					if id, ok := node.X.(*ast.Ident); ok && !token.IsExported(id.Name) {
						if fields[id.Name] != nil && fields[id.Name][node.Sel.Name] {
							usedField[id.Name+"."+node.Sel.Name] = true
						}
						if methods[node.Sel.Name] {
							usedMethod[node.Sel.Name] = true
						}
					}
				case *ast.Ident:
					if topLevel[node.Name] {
						usedTop[node.Name] = true
					}
				}
				return true
			})

			if len(usedTop) == 0 && len(usedField) == 0 && len(usedMethod) == 0 {
				continue
			}
			nFiles++
			nIdents += len(usedTop)
			nFields += len(usedField) + len(usedMethod)

			fmt.Printf("FILE %s\n", filepath.ToSlash(tf))
			if len(usedTop) > 0 {
				fmt.Printf("  TOP     %s\n", strings.Join(sorted(usedTop), " "))
			}
			if len(usedMethod) > 0 {
				fmt.Printf("  METHOD  %s\n", strings.Join(sorted(usedMethod), " "))
			}
			if len(usedField) > 0 {
				fmt.Printf("  FIELD   %s\n", strings.Join(sorted(usedField), " "))
			}
		}
	}
	fmt.Fprintf(os.Stderr, "\nFILES=%d TOPLEVEL=%d MEMBERS=%d\n", nFiles, nIdents, nFields)
}

func sorted(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
