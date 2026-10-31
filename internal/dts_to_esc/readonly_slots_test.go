package dts_to_esc

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/printer"
	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/require"
)

// TestBorrowReadonlyParam covers the borrow the converter writes around a
// parameter typed `Readonly<T>`. It is `&` when no slot reachable through `T`
// is writable and `&mut` otherwise.
func TestBorrowReadonlyParam(t *testing.T) {
	t.Parallel()

	runInteropCases(t, map[string]interopCase{
		"TypeParam": {
			src: `declare function f<T>(x: Readonly<T>): void;`,
			want: []string{
				"@js(\"f\")\nexport declare fn f<T>(x: &mut Readonly<T>) -> undefined",
			},
		},
		"Optional": {
			src: `declare function f(x?: Readonly<{a: string}>): void;`,
			want: []string{
				"@js(\"f\")\nexport declare fn f(x?: &Readonly<{\n    a: string\n}>) -> undefined",
			},
		},
		"ReadonlyArraySlot": {
			src: `declare function f(x: Readonly<{a: readonly number[]}>): void;`,
			want: []string{
				"@js(\"f\")\nexport declare fn f(x: &Readonly<{\n    a: ReadonlyArray<number>\n}>) -> undefined",
			},
		},
		"WritableArraySlot": {
			src: `declare function f(x: Readonly<{a: number[]}>): void;`,
			want: []string{
				"@js(\"f\")\nexport declare fn f(x: &mut Readonly<{\n    a: Array<number>\n}>) -> undefined",
			},
		},
		"MethodParam": {
			src: widgetLib(`getA(x: Readonly<{a: number}>): number;`),
			want: []string{
				"Widget.getA(&self, x: &Readonly<{\n    a: number\n}>) -> number",
			},
		},
		"ReadonlyOfArrayRef": {
			src: `declare function f(x: Readonly<Array<number>>): void;`,
			want: []string{
				"@js(\"f\")\nexport declare fn f(x: &Readonly<Array<number>>) -> undefined",
			},
		},
		"RestParam": {
			src: `declare function f(...xs: Readonly<number[]>): void;`,
			want: []string{
				"@js(\"f\")\nexport declare fn f(...xs: Readonly<Array<number>>) -> undefined",
			},
		},
	})
}

// TestStoredReadonlyParamTakesNoBorrow asserts that a constructor or setter
// parameter typed `Readonly<T>` keeps its type as written with no borrow,
// since the instance keeps the argument.
func TestStoredReadonlyParamTakesNoBorrow(t *testing.T) {
	t.Parallel()

	lib := parseLib(t, "lib.interop.d.ts", `declare class Store {
    constructor(opts: Readonly<{a: number}>);
    set cfg(v: Readonly<{a: number}>);
}`)
	mod, err := ConvertBucket(mergeDecls(lib.Module.Statements), nil)
	require.NoError(t, err)
	rootNS, ok := mod.Module.Namespaces.Get("")
	require.True(t, ok)
	require.Len(t, rootNS.Decls, 1)
	class, ok := rootNS.Decls[0].(*ast.ClassDecl)
	require.True(t, ok)
	printed, err := printer.Print(class, printer.DefaultOptions())
	require.NoError(t, err)
	snaps.MatchInlineSnapshot(t, printed, snaps.Inline(`@js("Store")
export declare class Store {
    constructor(&mut self, opts: Readonly<{
        a: number
    }>),
    set cfg(&mut self, v: Readonly<{
        a: number
    }>)
}`))
}
