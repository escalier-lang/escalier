package dts_to_esc

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/printer"
	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/require"
)

// borrowDefaultsTypes declares the types the TestApplyBorrowDefaults cases name.
// `Flat` holds only value types and `Nested` holds an object. `Callback` is a
// function type written as an interface, `Mode` an alias to a union of
// literals, and `Pair` an alias to an object type.
const borrowDefaultsTypes = `
interface Flat { a: number; b?: string; }
interface Nested { inner: Flat; }
interface Callback { (x: number): void; }
type Mode = "a" | "b";
type Pair = { left: Nested; right: Nested };
`

// TestApplyBorrowDefaults covers which parameters and returns applyBorrowDefaults
// rewrites in a declared function. A parameter of an object type becomes `&mut`, and
// every other parameter keeps the type it converts to. A return of an object type
// borrows each borrowed parameter it may share structure with.
func TestApplyBorrowDefaults(t *testing.T) {
	t.Parallel()

	tests := map[string]interopCase{
		"ValueTypeParams": {
			src: `declare function f(a: string, b: (x: number) => void, c: Promise<Flat>, d: Callback, e: Mode): void;`,
			want: []string{
				"@js(\"f\")\nexport declare fn f(a: string, b: fn (x: number) -> unknown, c: Promise<Flat>, d: Callback, e: Mode) -> undefined",
			},
		},
		"UncertainParamsStayOwned": {
			src: `declare function f<T>(a: T, b: unknown, c: Flat | string, ...rest: Flat[]): void;`,
			want: []string{
				"@js(\"f\")\nexport declare fn f<T>(a: T, b: unknown, c: Flat | string, ...rest: Array<Flat>) -> undefined",
			},
		},
		"ObjectParams": {
			src: `declare function f(a: Flat, b: {x: number}, c: [number, string], d: Pair, e?: Nested): void;`,
			want: []string{
				"@js(\"f\")\nexport declare fn f(a: &mut Flat, b: &mut {\n    x: number\n}, c: &mut [number, string], d: &mut Pair, e?: &mut Nested) -> undefined",
			},
		},
		"FlatParamDoesNotShareWithReturn": {
			src: `declare function f(a: Flat): Nested;`,
			want: []string{
				"@js(\"f\")\nexport declare fn f(a: &mut Flat) -> Nested",
			},
		},
		"FlatParamOfReturnTypeSharesWithReturn": {
			src: `declare function f(a: Flat): Flat;`,
			want: []string{
				"@js(\"f\")\nexport declare fn f<'a>(a: &'a mut Flat) -> &'a mut Flat",
			},
		},
		"OnlyParamsThatMayShareTakeTheLifetime": {
			src: `declare function f(a: Flat, b: Nested): Pair;`,
			want: []string{
				"@js(\"f\")\nexport declare fn f<'a>(a: &mut Flat, b: &'a mut Nested) -> &'a mut Pair",
			},
		},
		"NullableReturnBorrowsItsObjectType": {
			src: `declare function f(a: Nested): Nested | null;`,
			want: []string{
				"@js(\"f\")\nexport declare fn f<'a>(a: &'a mut Nested) -> &'a mut Nested | null",
			},
		},
		"NamespaceQualifiedParam": {
			src: `declare namespace NS { interface Opts { a: number; } }
declare function f(o: NS.Opts): void;`,
			want: []string{
				"@js(\"f\")\nexport declare fn f(o: &mut NS.Opts) -> undefined",
			},
		},
		"ValueTypeReturnStaysOwned": {
			src: `declare function f(a: Nested): Promise<Nested>;`,
			want: []string{
				"@js(\"f\")\nexport declare fn f(a: &mut Nested) -> Promise<Nested>",
			},
		},
	}
	for name, test := range tests {
		test.src = borrowDefaultsTypes + test.src
		tests[name] = test
	}
	runInteropCases(t, tests)
}

// TestApplyBorrowDefaultsClassMembers covers the class members applyBorrowDefaults
// rewrites. An instance method and a static method take the same defaults as a
// function. A constructor and a setter keep their parameters owned.
func TestApplyBorrowDefaultsClassMembers(t *testing.T) {
	t.Parallel()

	lib := parseLib(t, "lib.widget.d.ts", borrowDefaultsTypes+`
interface Widget {
    take(n: Nested): Nested;
    set value(v: Nested);
}
interface WidgetConstructor {
    new (n: Nested): Widget;
    readonly prototype: Widget;
    from(n: Nested): Widget;
}
declare var Widget: WidgetConstructor;
`)
	mod, err := ConvertBucket(mergeDecls(lib.Module.Statements), nil)
	require.NoError(t, err)

	rootNS, ok := mod.Module.Namespaces.Get("")
	require.True(t, ok)
	var class *ast.ClassDecl
	for _, decl := range rootNS.Decls {
		if d, ok := decl.(*ast.ClassDecl); ok && d.Name.Name == "Widget" {
			class = d
		}
	}
	require.NotNil(t, class)
	printed, err := printer.Print(class, printer.DefaultOptions())
	require.NoError(t, err)
	snaps.MatchInlineSnapshot(t, printed, snaps.Inline(`@js("Widget")
export declare class Widget {
    take<'a>(&mut self, n: &'a mut Nested) -> &'a mut Nested,
    set value(&mut self, v: Nested),
    constructor(&mut self, n: Nested),
    static readonly prototype: Widget,
    static from<'a>(n: &'a mut Nested) -> &'a mut Widget
}`))
}
