package dts_to_esc

import (
	"context"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/escalier-lang/escalier/internal/printer"
	"github.com/stretchr/testify/require"
)

func parseSource(t *testing.T, src string) *ast.Module {
	t.Helper()
	module, errs := parser.ParseLibFiles(context.Background(), []*ast.Source{{
		ID: 1, Path: "pkg.esc", Contents: src,
	}})
	require.Empty(t, errs, "expected no parse errors")
	return module
}

// A reference is collected wherever it is written. The type-parameter and
// `throws` slots are the ones `Accept` does not reach, so each is named here.
func TestTypeRefNamesReachesEverySlot(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"AFieldAnnotation", `export declare class C { f: Target }`, "Target"},
		{"AGenericArgument", `export declare class C { f: Box<Target> }`, "Target"},
		{"AnExtendsClause", `export declare class C extends Target {}`, "Target"},
		{"AMethodReturn", `export declare class C { m(self) -> Target }`, "Target"},
		{"AnAliasBody", `export type A = Target`, "Target"},
		{"AFunctionParameter", `export declare fn f(x: Target) -> number`, "Target"},
		{"AQualifiedHead", `export type A = Target.Inner`, "Target"},

		// The four slots the walk skips.
		{"AClassTypeParamConstraint", `export declare class C<T: Target> {}`, "Target"},
		{"AClassTypeParamDefault", `export declare class C<T = Target> {}`, "Target"},
		{"AnAliasTypeParamConstraint", `export type A<T: Target> = T`, "Target"},
		{"AFunctionThrows", `export declare fn f() -> number throws Target`, "Target"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			refs := TypeRefNames(parseSource(t, tt.src))
			require.True(t, refs.Contains(tt.want),
				"%s did not record %s; recorded %v", tt.src, tt.want, refs.ToSlice())
		})
	}
}

// A qualified reference contributes both ends. The head is normally the name an
// import brings into scope. The last segment matters where namespace flattening
// left the head naming nothing, which is what happened to `Intl`: `std:intl`
// declares `Collator` at the top level and declares no `Intl`.
func TestTypeRefNamesRecordsBothEndsOfAQualifiedName(t *testing.T) {
	refs := TypeRefNames(parseSource(t, `export type A = Intl.Collator`))
	require.True(t, refs.Contains("Intl"))
	require.True(t, refs.Contains("Collator"))
}

func TestDeclaredNamesCoversEveryDeclarationKind(t *testing.T) {
	refs := DeclaredNames(parseSource(t, `
		export declare class Cls {}
		export declare fn func() -> number
		export type Alias = number
		export declare val value: number
		export declare var variable: number
		export interface Iface {}
		export namespace Ns {
			export declare val inner: number
		}
	`))
	for _, name := range []string{"Cls", "func", "Alias", "value", "variable", "Iface", "Ns"} {
		require.True(t, refs.Contains(name), "%s was not recorded; recorded %v", name, refs.ToSlice())
	}
	// A namespace member is read off the namespace and is not a top-level name.
	require.False(t, refs.Contains("inner"))
}

// A type parameter is not a name the package has to import, whichever `<…>`
// list binds it. The four cases are the four kinds of list the generated tree
// writes: a declaration's own, a class member's, an interface member's, and a
// standalone function type's.
func TestTypeRefNamesLeavesOutABoundTypeParameter(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{"ADeclarationsOwn", `export declare class C<T> { f: T }`},
		{"AClassMembers", "export declare class C {\n    m<U>(self, x: U) -> U\n}"},
		{"AnInterfaceMembers", "export declare interface I {\n    m<U>(x: U) -> U\n}"},
		{"AFunctionTypes", `export type A = fn <U>(x: U) -> U`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			refs := TypeRefNames(parseSource(t, tt.src)).ToSlice()
			require.Empty(t, refs, "%s recorded %v", tt.src, refs)
		})
	}
}

// A member's own binder shadows only its own signature. A reference written
// after it, under the same name, is the declaration the package imports.
func TestTypeRefNamesRecordsANameAMemberBinderShadowedEarlier(t *testing.T) {
	refs := TypeRefNames(parseSource(t, "export declare class C {\n"+
		"    m<U>(self, x: U) -> U,\n"+
		"    f: U\n"+
		"}"))
	require.Equal(t, []string{"U"}, refs.ToSlice())
}

// A `<…>` binder shadows a name another package declares, so the reference to
// it takes no qualifier and forces no import.
//
// `std:math` exports a top-level `E`, from `Math.E`, and `Element.closest` in
// lib.dom.d.ts is `closest<E extends Element = Element>(selectors: string): E |
// null`. Qualifying that `E` would return `math.E` to every caller that asked
// for a subtype, and would make `web:dom` import `std:math` for a reference
// that names nothing there.
func TestAddImportHeadersLeavesABoundTypeParameterAlone(t *testing.T) {
	const element = "export declare class Element {\n" +
		"    closest<E: Element = Element>(mut self, selectors: string) -> E | null\n" +
		"}"
	mods := map[string]*StandaloneModule{
		"std:math": {Module: parseSource(t, `export declare val E: number`)},
		"web:dom":  {Module: parseSource(t, element)},
	}
	require.NoError(t, AddImportHeaders(mods))

	dom := mods["web:dom"].Module
	require.Empty(t, dom.Files[0].Imports, "a bound type parameter forces no import")

	var decls []ast.Decl
	dom.Namespaces.Scan(func(_ string, ns *ast.Namespace) bool {
		decls = append(decls, ns.Decls...)
		return true
	})
	require.Len(t, decls, 1)
	printed, err := printer.Print(decls[0], printer.DefaultOptions())
	require.NoError(t, err)
	require.Equal(t, element, printed)
}

// A destructuring binding contributes every leaf it binds. The generated tree
// writes a bare name, so this guards a hand-authored package rather than the
// converter's own output.
func TestDeclaredNamesReadsEveryLeafOfABinding(t *testing.T) {
	refs := DeclaredNames(parseSource(t, `
		export val {a, b: renamed, ...rest} = source
		export val [first, ...others] = tuple
	`))

	for _, name := range []string{"a", "renamed", "rest", "first", "others"} {
		require.True(t, refs.Contains(name), "%s was not recorded; recorded %v", name, refs.ToSlice())
	}
	// `b` is the key read off the object, not the name the pattern binds.
	require.False(t, refs.Contains("b"))
}
