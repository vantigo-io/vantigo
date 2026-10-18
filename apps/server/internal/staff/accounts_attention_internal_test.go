package staff

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

// Every reason the accounts worker writes as a link's blocked_reason — each
// reason* constant of accounts_decide.go and accounts_worker.go, walked from
// the source so a reason added later cannot be forgotten — maps to an
// attention item, or is named as judged by the person query instead.
func TestAccountAttention_EveryWorkerReasonIsMapped(t *testing.T) {
	fset := token.NewFileSet()
	seen := 0
	for _, file := range []string{"accounts_decide.go", "accounts_worker.go"} {
		f, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			spec, ok := n.(*ast.ValueSpec)
			if !ok {
				return true
			}
			for i, name := range spec.Names {
				if !strings.HasPrefix(name.Name, "reason") || i >= len(spec.Values) {
					continue
				}
				lit, ok := spec.Values[i].(*ast.BasicLit)
				if !ok {
					t.Errorf("%s: %s is not a string literal", file, name.Name)
					continue
				}
				reason, _ := strconv.Unquote(lit.Value)
				seen++
				if _, mapped := accountReasonItems[reason]; !mapped {
					t.Errorf("%s: %s (%q) maps to no attention item", file, name.Name, reason)
				}
			}
			return true
		})
	}
	if seen < 19 {
		t.Errorf("walked %d reasons, want every one (at least nineteen)", seen)
	}
}
