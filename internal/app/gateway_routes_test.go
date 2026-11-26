package app

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"slices"
	"strconv"
	"testing"
)

func TestGatewayRouteManifestMatchesRouter(t *testing.T) {
	// Include registrations behind dependency checks for passkeys and developer keys.
	router, err := parser.ParseFile(token.NewFileSet(), "router.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var registered []string
	ast.Inspect(router, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		method, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || (method.Sel.Name != "HandleFunc" && method.Sel.Name != "Handle") {
			return true
		}
		receiver, ok := method.X.(*ast.Ident)
		if !ok || receiver.Name != "mux" {
			return true
		}
		pattern, ok := call.Args[0].(*ast.BasicLit)
		if !ok || pattern.Kind != token.STRING {
			t.Fatal("gateway route coverage requires a literal mux pattern")
		}
		route, err := strconv.Unquote(pattern.Value)
		if err != nil {
			t.Fatal(err)
		}
		registered = append(registered, route)
		return true
	})

	data, err := os.ReadFile("../../infra/api-routes.json")
	if err != nil {
		t.Fatal(err)
	}
	var routes []string
	if err := json.Unmarshal(data, &routes); err != nil {
		t.Fatal(err)
	}
	slices.Sort(registered)
	if !slices.Equal(routes, registered) {
		t.Errorf("update infra/api-routes.json to match the Go router\nmanifest: %q\nrouter:   %q", routes, registered)
	}
}
