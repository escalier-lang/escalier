package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// TestWeakModuleBinding covers a module-level binding whose initializer is not a syntactic
// value. Such a binding does not generalize, so every function that uses it constrains the
// same variables, and the module reports a binding whose variables no use decided.
func TestWeakModuleBinding(t *testing.T) {
	tests := []struct {
		name string
		src  string
		// values maps a top-level binding to its rendered type. Bindings the map leaves
		// out are not checked.
		values map[string]string
		// want lists every diagnostic the program reports, each with its span.
		want []string
	}{
		{
			name: "WritesFromTwoFunctionsReachOneMap",
			src: `
val mut seen = Map()
fn a() { seen.set("k", 1) }
fn b() { seen.set("k", "s") }
`,
			values: map[string]string{"seen": `Map<"k", 1 | "s">`},
		},
		{
			name: "AReadSeesTheWritesOfALaterFunction",
			src: `
val mut seen = Map()
fn get() { return seen.get("k") }
fn put() { seen.set("k", 1) }
`,
			values: map[string]string{
				"seen": `Map<"k", 1>`,
				"get":  "fn () -> 1 | undefined",
			},
		},
		{
			name: "AnOwnedValueAndABorrowInOneMap",
			src: `
val mut seen = Map()
val x: {a: number} = {a: 1}
fn a() { seen.set("k", &x) }
fn b() { seen.set("k", {a: 2}) }
`,
			want: []string{
				"2:5-2:13: a union or intersection mixes owned and borrowed members. Make ownership uniform first. Clone the borrowed member to own it, or borrow the owned member.",
			},
		},
		{
			name: "AStoreOfAParameterLeavesTheValueTypeUndecided",
			src: `
val mut seen = Map()
fn remember(value) { seen.set("last", value) }
`,
			want: []string{
				"2:5-2:13: cannot infer the type of 'seen'; annotate it, as in `val mut seen: Map<\"last\", V> = Map()`",
			},
		},
		{
			name: "AMutableContainerNothingWrites",
			src: `
val mut seen = Map()
`,
			want: []string{
				"2:5-2:13: cannot infer the type of 'seen'; annotate it, as in `val mut seen: Map<K, V> = Map()`",
			},
		},
		{
			name: "AnImmutableContainerNothingCanWrite",
			src: `
val seen = Map()
`,
			values: map[string]string{"seen": "Map<never, never>"},
		},
		{
			name: "AnExportedContainer",
			src: `
export val seen = Map()
`,
			want: []string{
				"2:12-2:16: cannot infer the type of 'seen'; annotate it, as in `val seen: Map<K, V> = Map()`",
			},
		},
		{
			name: "AnExportedMutableContainerItsFunctionsWrite",
			src: `
export val mut seen = Map()
fn a() { seen.set("k", 1) }
`,
			want: []string{
				"2:12-2:20: cannot infer the type of 'seen'; annotate it, as in `val mut seen: Map<\"k\", 1> = Map()`",
			},
		},
		{
			name: "AnExportedValueACallDecides",
			src: `
fn id(x) { return x }
export val five = id(5)
`,
			values: map[string]string{"five": "5"},
		},
		{
			name: "AnAnnotatedContainer",
			src: `
val seen: Map<string, number> = Map()
`,
			values: map[string]string{"seen": "Map<string, number>"},
		},
		{
			name: "AFunctionExpressionStaysPolymorphic",
			src: `
val f = fn (x) { return x }
val a = f(1)
val b = f("s")
`,
			values: map[string]string{
				"f": "fn <T0>(x: T0) -> T0",
				"a": "1",
				"b": `"s"`,
			},
		},
		{
			name: "AFunctionACallReturnsIsSettledByItsUse",
			src: `
fn makeId() { return fn (x) { return x } }
val g = makeId()
fn use() { return g(1) }
`,
			values: map[string]string{
				"g":   "fn (x: unknown) -> 1",
				"use": "fn () -> 1",
			},
		},
		{
			name: "AFunctionACallReturnsThatNothingCalls",
			src: `
fn makeId() { return fn (x) { return x } }
val g = makeId()
`,
			want: []string{
				"3:5-3:6: cannot infer the type of 'g'; annotate it, as in `val g: fn (x: T0) -> T0 = makeId()`",
			},
		},
		{
			name: "ALocalContainerGeneralizesWithItsFunction",
			src: `
fn make() {
	val mut m = Map()
	m.set("k", 1)
	m.set("k", "s")
	return m
}
`,
			values: map[string]string{"make": `fn () -> Map<"k", 1 | "s">`},
		},
		{
			name: "AGenericFunctionKeepsItsOwnTypeParameter",
			src: `
var id = fn <T>(x: T) -> T { return x }
declare fn mk() -> fn <T>(x: T) -> T
val made = mk()
`,
			values: map[string]string{
				"id":   "fn <T>(x: T) -> T",
				"made": "fn <T>(x: T) -> T",
			},
		},
		{
			name: "AnExportedFunctionACallSettles",
			src: `
declare fn twice<T>(f: fn (x: T) -> T) -> fn (x: T) -> T
export val inc2 = twice(fn (x: number) -> number { return x + 1 })
`,
			values: map[string]string{"inc2": "fn (x: number) -> number"},
		},
		{
			name: "AnExportedFunctionNothingSettlesPrintsOnOneLine",
			src: `
declare fn wrap<T>(f: fn (x: T) -> T) -> fn (x: T) -> T
export val same = wrap(fn (x) { return x })
`,
			want: []string{
				"3:12-3:16: cannot infer the type of 'same'; annotate it, as in `val same: fn (x: T0) -> T0 = …`",
			},
		},
		{
			name: "APatternTestLeavesASharedElementAlone",
			src: `
val mk = fn (v) { return [v] }
val o = mk(5)
fn f() { return match o[0] { 6 => "six", _ => "other" } }
`,
			values: map[string]string{"o": "[5]"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, errs := inferAgainstTreeWithSpans(t, tt.src)
			require.Equal(t, tt.want, errs)
			for name, want := range tt.values {
				require.Contains(t, values, name)
				require.Equal(t, want, values[name], name)
			}
		})
	}
}

// TestWeakBindingInfoShowsTheSettledType asserts that Info records the type the module's
// functions settled a weak binding and a function that reads it on. Both components close
// before `put` writes the map, so a type recorded then would show the map holding nothing.
func TestWeakBindingInfoShowsTheSettledType(t *testing.T) {
	module := parseModule(t, `
val mut seen = Map()
fn get() { return seen.get("k") }
fn put() { seen.set("k", 1) }
`)
	res := InferModuleAgainstStdlib(module, benchTree, Options{})
	require.Empty(t, res.Errors)
	ns, ok := module.Namespaces.Get("")
	require.True(t, ok)
	seen, ok := ns.Decls[0].(*ast.VarDecl)
	require.True(t, ok)
	get, ok := ns.Decls[1].(*ast.FuncDecl)
	require.True(t, ok)
	require.Equal(t, `Map<"k", 1>`, soltype.Print(res.Info.DisplayTypeOf(seen.Pattern)))
	require.Equal(t, "fn () -> 1 | undefined", soltype.Print(res.Info.DisplayTypeOf(get.Name)))
}

// TestIsSyntacticValue covers which initializers generalizes treats as syntactic values.
func TestIsSyntacticValue(t *testing.T) {
	tests := []struct {
		init string
		want bool
	}{
		{init: "fn (x) { return f(x) }", want: true},
		{init: "5", want: true},
		{init: `"s"`, want: true},
		{init: "`a${n}`", want: true},
		{init: "`a${f()}`", want: false},
		{init: "n", want: true},
		{init: "o.p", want: true},
		{init: "o.p[0]", want: true},
		{init: "{a: 1, m: fn () { return f() }, ...o}", want: true},
		{init: "{a: f()}", want: false},
		{init: "[1, n, ...xs]", want: true},
		{init: "[f()]", want: false},
		{init: "-n + 1", want: true},
		{init: "&o", want: true},
		{init: "(n:number)", want: true},
		{init: "f()", want: false},
		{init: "o.m()", want: false},
		{init: "await p", want: false},
		{init: "tag`a`", want: false},
		{init: "if c { 1 } else { 2 }", want: false},
		{init: "match n { _ => 1 }", want: false},
		{init: "do { 1 }", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.init, func(t *testing.T) {
			module := parseModule(t, "val x = "+tt.init)
			ns, ok := module.Namespaces.Get("")
			require.True(t, ok)
			vd, ok := ns.Decls[0].(*ast.VarDecl)
			require.True(t, ok)
			require.Equal(t, tt.want, isSyntacticValue(vd.Init))
		})
	}
}
