// Package extract находит в исходниках все строки, которые должны переводиться.
package extract

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Msg — переводимая строка и место, где она встретилась.
type Msg struct {
	ID  string
	Pos string
}

var flagFuncs = map[string]bool{"StringVar": true, "BoolVar": true, "IntVar": true, "Float64Var": true, "Var": true}

// Messages обходит все .go файлы модуля (кроме тестов и каталогов перевода).
func Messages(root string) ([]Msg, error) {
	seen := map[string]Msg{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && path != root && (d.Name() == "testdata" || strings.HasPrefix(d.Name(), ".")) {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			var idx []int
			switch name := funcName(call.Fun); name {
			case "T", "i18n.T", "i18n.Errorf", "i18n.N", "N":
				idx = []int{0}
			case "newFlags":
				idx = []int{1, 2}
			default:
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && flagFuncs[sel.Sel.Name] {
					idx = []int{len(call.Args) - 1}
				}
			}
			for _, i := range idx {
				if i < len(call.Args) {
					if s, ok := literal(call.Args[i]); ok && s != "" {
						if _, dup := seen[s]; !dup {
							seen[s] = Msg{ID: s, Pos: fset.Position(call.Pos()).String()}
						}
					}
				}
			}
			return true
		})
		return nil
	})
	var out []Msg
	for _, m := range seen {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, err
}

func funcName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		if id, ok := x.X.(*ast.Ident); ok {
			return id.Name + "." + x.Sel.Name
		}
	}
	return ""
}

func literal(e ast.Expr) (string, bool) {
	bl, ok := e.(*ast.BasicLit)
	if !ok || bl.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(bl.Value)
	return s, err == nil
}

var verbRe = regexp.MustCompile(`%(\[\d+\])?[-+# 0]*\d*(\.\d+)?[vTtbcdoOqxXUeEfFgGsqpw%]`)

// Verbs — последовательность плейсхолдеров fmt в строке (без %%).
func Verbs(s string) []string {
	var out []string
	for _, v := range verbRe.FindAllString(s, -1) {
		if v != "%%" {
			out = append(out, v)
		}
	}
	return out
}

// CyrillicLiterals — строковые литералы с кириллицей вне каталогов перевода.
func CyrillicLiterals(root string) ([]Msg, error) {
	var out []Msg
	fset := token.NewFileSet()
	cyr := regexp.MustCompile(`\p{Cyrillic}`)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && path != root && (d.Name() == "testdata" || strings.HasPrefix(d.Name(), ".")) {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if strings.Contains(path, "internal/i18n/") && !strings.Contains(path, "internal/i18n/extract") && !strings.HasSuffix(path, "i18n.go") {
			return nil // каталоги
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if s, ok := n.(ast.Expr); ok {
				if v, ok := literal(s); ok && cyr.MatchString(v) {
					out = append(out, Msg{ID: v, Pos: fset.Position(n.Pos()).String()})
				}
			}
			return true
		})
		return nil
	})
	return out, err
}
