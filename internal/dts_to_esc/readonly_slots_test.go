package dts_to_esc

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dts_parser"
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
		// `Readonly<T>` fixes only the top-level slots of `x`. When `T` is
		// instantiated with a deep object such as `{a: {b: number}}`, `x.a.b`
		// stays writable, so the borrow has to be `&mut`.
		"TypeParam": {
			src: `declare function f<T>(x: Readonly<T>): void;`,
			want: []string{
				`@js("f")
export declare fn f<T>(x: &mut Readonly<T>) -> undefined`,
			},
		},
		"Optional": {
			src: `declare function f(x?: Readonly<{a: string}>): void;`,
			want: []string{
				`@js("f")
export declare fn f(x?: &Readonly<{
    a: string
}>) -> undefined`,
			},
		},
		"ReadonlyArraySlot": {
			src: `declare function f(x: Readonly<{a: readonly number[]}>): void;`,
			want: []string{
				`@js("f")
export declare fn f(x: &Readonly<{
    a: ReadonlyArray<number>
}>) -> undefined`,
			},
		},
		"WritableArraySlot": {
			src: `declare function f(x: Readonly<{a: number[]}>): void;`,
			want: []string{
				`@js("f")
export declare fn f(x: &mut Readonly<{
    a: Array<number>
}>) -> undefined`,
			},
		},
		"MethodParam": {
			src: widgetLib(`getA(x: Readonly<{a: number}>): number;`),
			want: []string{
				`Widget.getA(&self, x: &Readonly<{
    a: number
}>) -> number`,
			},
		},
		"ReadonlyOfArrayRef": {
			src: `declare function f(x: Readonly<Array<number>>): void;`,
			want: []string{
				`@js("f")
export declare fn f(x: &Readonly<Array<number>>) -> undefined`,
			},
		},
		"RestParam": {
			src: `declare function f(...xs: Readonly<number[]>): void;`,
			want: []string{
				`@js("f")
export declare fn f(...xs: Readonly<Array<number>>) -> undefined`,
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

// TestReadonlyParamBorrowByTarget covers which `T` in a `Readonly<T>`
// parameter give `&` and which give `&mut`.
func TestReadonlyParamBorrowByTarget(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		target string
		mut    bool
	}{
		"Parenthesized":         {target: "({a: number})", mut: false},
		"OptionalTupleElem":     {target: "[number, string?]", mut: false},
		"RestTupleElem":         {target: "[number, ...string[]]", mut: false},
		"RestTupleWritableElem": {target: "[number, ...{a: number}[]]", mut: true},
		"TupleWritableElem":     {target: "[{a: number}]", mut: true},
		"Any":                   {target: "{a: any}", mut: true},
		"Primitive":             {target: "{a: string}", mut: false},
		"Literal":               {target: "{a: \"x\"}", mut: false},
		"TemplateLiteral":       {target: "{a: `x${string}`}", mut: false},
		"KeyOf":                 {target: "{a: keyof Foo}", mut: false},
		"Function":              {target: "{a: () => void}", mut: false},
		"Constructor":           {target: "{a: new () => Foo}", mut: false},
		"Union":                 {target: "{a: number} | {b: number[]}", mut: true},
		"Intersection":          {target: "{a: number} & {b: string}", mut: false},
		"ReadonlyIndex":         {target: "{[k: string]: number}", mut: false},
		"WritableIndexValue":    {target: "{[k: string]: number[]}", mut: true},
		"NestedWritableIndex":   {target: "{a: {[k: string]: number}}", mut: true},
		"Method":                {target: "{m(): void}", mut: false},
		"Getter":                {target: "{get a(): number[]}", mut: true},
		"Setter":                {target: "{set a(v: number)}", mut: false},
		"CallSignature":         {target: "{(): void}", mut: false},
		"ConstructSignature":    {target: "{new (): Foo}", mut: false},
		"NestedReadonly":        {target: "{a: Readonly<{b: number}>}", mut: false},
		"ReadonlySet":           {target: "{a: ReadonlySet<number>}", mut: false},
		"ReadonlyMap":           {target: "{a: ReadonlyMap<string, number[]>}", mut: true},
		"InterfaceRef":          {target: "{a: Foo}", mut: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			lib := parseLib(t, "lib.interop.d.ts",
				"declare function f(x: Readonly<"+tc.target+">): void;")
			fn, ok := lib.Module.Statements[0].(*dts_parser.FuncDecl)
			require.True(t, ok)
			target, ok := readonlyTarget(fn.Params[0].Type)
			require.True(t, ok)
			require.Equal(t, tc.mut, reachesWritableSlot(target, true))
		})
	}
}
