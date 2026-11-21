package telemetry

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// defaultClientAllowed are the non-test files that may use http.DefaultClient:
// the healthcheck command, which probes this process on localhost, and test
// fakes that live in non-test files of a *test package.
var defaultClientAllowed = map[string]bool{
	"internal/health/health.go": true,
}

// TestEveryHTTPClientIsTraced is the lint KR20 names (Kubernetes readiness):
// every http.Client built in non-test server code sets Transport to
// telemetry.OutboundTransport(…), so every outbound call is a client span and
// none forwards our trace IDs; and nothing outside the allow-list reaches for
// http.DefaultClient or the package-level http.Get/Post/Head, which bypass
// both.
func TestEveryHTTPClientIsTraced(t *testing.T) {
	root := filepath.Join("..", "..")
	var problems []string
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			httpName := importName(file, "net/http")
			if httpName == "" {
				return nil
			}
			testPackage := strings.HasSuffix(file.Name.Name, "test")
			ast.Inspect(file, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.CompositeLit:
					if isSelector(n.Type, httpName, "Client") && !tracedClient(n, file.Name.Name) {
						problems = append(problems, fset.Position(n.Pos()).String()+": http.Client without Transport: telemetry.OutboundTransport(…)")
					}
				case *ast.SelectorExpr:
					if testPackage || defaultClientAllowed[rel] {
						return true
					}
					for _, name := range []string{"DefaultClient", "Get", "Post", "PostForm", "Head"} {
						if isSelector(n, httpName, name) {
							problems = append(problems, fset.Position(n.Pos()).String()+": http."+name+" is not traced; build a client over telemetry.OutboundTransport")
						}
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range problems {
		t.Error(p)
	}
}

func importName(f *ast.File, path string) string {
	for _, imp := range f.Imports {
		if strings.Trim(imp.Path.Value, `"`) == path {
			if imp.Name != nil {
				return imp.Name.Name
			}
			return filepath.Base(path)
		}
	}
	return ""
}

func isSelector(e ast.Expr, pkg, name string) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == pkg && sel.Sel.Name == name
}

func tracedClient(lit *ast.CompositeLit, pkg string) bool {
	for _, el := range lit.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if key, ok := kv.Key.(*ast.Ident); !ok || key.Name != "Transport" {
			continue
		}
		call, ok := kv.Value.(*ast.CallExpr)
		if !ok {
			return false
		}
		if pkg == "telemetry" {
			if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "OutboundTransport" {
				return true
			}
		}
		return isSelector(call.Fun, "telemetry", "OutboundTransport")
	}
	return false
}
