package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Distortions81/goPine/internal/uifont"
)

// Guard fixed text and numeric displays against future repertoire trimming.
// Catch omissions at their call site instead of silently drawing a space.
func TestRegularTextCoversLiteralUIStrings(t *testing.T) {
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			large := false
			for _, arg := range call.Args {
				if ptr, ok := arg.(*ast.UnaryExpr); ok && ptr.Op == token.AND {
					if sel, ok := ptr.X.(*ast.SelectorExpr); ok && sel.Sel.Name == "Regular18" {
						large = true
					}
				}
			}
			if large {
				for _, arg := range call.Args {
					if lit, ok := arg.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						label, err := strconv.Unquote(lit.Value)
						if err != nil {
							t.Fatal(err)
						}
						for _, r := range label {
							if uifont.Regular18.GetGlyph(r).Info().Rune != r {
								t.Errorf("%s: Regular18 lacks %q; update scripts/generate-fonts.py", fset.Position(lit.Pos()), r)
							}
						}
					}
				}
			}
			return true
		})
	}
	for _, label := range []string{"0123456789: .%", "AM PM", "ALARM", "TIMER DONE"} {
		for _, r := range label {
			if uifont.Regular18.GetGlyph(r).Info().Rune != r {
				t.Errorf("dynamic labels require %q", r)
			}
		}
	}
}
