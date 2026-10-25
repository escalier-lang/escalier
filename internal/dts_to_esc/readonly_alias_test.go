package dts_to_esc

import (
	"strings"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dts_parser"
	"github.com/escalier-lang/escalier/internal/printer"
	"github.com/stretchr/testify/require"
)

// convertAndPrintFuncs converts src as a package's `.d.ts` and returns each function
// declaration it produces, printed and prefixed with its namespace path.
func convertAndPrintFuncs(t *testing.T, src string) []string {
	t.Helper()
	p := dts_parser.NewDtsParser(&ast.Source{ID: 0, Path: "index.d.ts", Contents: src})
	parsed, errs := p.ParseModule()
	require.Empty(t, errs)
	mod, err := ConvertModuleWithOverrides(parsed, nil, "dep")
	require.NoError(t, err)
	var out []string
	var walk func(nsPath string, decls []ast.Decl)
	walk = func(nsPath string, decls []ast.Decl) {
		for _, decl := range decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				printed, err := printer.Print(d, printer.DefaultOptions())
				require.NoError(t, err)
				out = append(out, nsPath+": "+strings.TrimSpace(printed))
			case *ast.NamespaceDecl:
				walk(qualifyNamespace(nsPath, d.Name.Name), d.Decls)
			}
		}
	}
	mod.Namespaces.Scan(func(nsPath string, ns *ast.Namespace) bool {
		walk(nsPath, ns.Decls)
		return true
	})
	return out
}

// TestReadonlyAliasReturnsResolveByNamespace asserts that a return type naming a type
// alias is borrowed exactly when the alias it resolves to is a readonly collection. A
// reference resolves from its own namespace outward, so a class declared nearer
// hides a readonly alias of the same name further out.
func TestReadonlyAliasReturnsResolveByNamespace(t *testing.T) {
	t.Parallel()

	got := convertAndPrintFuncs(t, `export type RO = readonly number[];
export declare namespace outer {
	type List = readonly number[];
	namespace inner {
		function make(): List;
		function makeRO(): RO;
	}
}
export declare namespace classShadow {
	class RO { x: number }
	function make(): RO;
}`)
	require.Equal(t, []string{
		"classShadow: export declare fn make() -> RO",
		"outer.inner: export declare fn make() -> &'static List",
		"outer.inner: export declare fn makeRO() -> &'static RO",
	}, got)
}
