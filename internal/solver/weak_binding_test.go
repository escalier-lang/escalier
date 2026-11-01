package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// TestWeakModuleBinding covers a module-level binding whose initializer is not a syntactic
// value. Such a binding does not generalize, so every function that uses it constrains the
// same variables. Its type settles once the module is inferred, with the literals in each
// slot the binding can write widened.
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
fn b() { seen.set("j", "s") }
`,
			values: map[string]string{"seen": "Map<string, number | string>"},
		},
		{
			// #2348 widens the weak variable in the checked type as well as in seen's display,
			// so get becomes `fn () -> number | undefined`.
			name: "AReadSeesTheWritesOfALaterFunction",
			src: `
val mut seen = Map()
fn get() { return seen.get("k") }
fn put() { seen.set("k", 1) }
`,
			values: map[string]string{
				"seen": "Map<string, number>",
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
			// Nothing calls `remember`, so nothing decides what `value` holds. remember does not
			// quantify `value`, since it flows into the map's value type, and both settle at
			// `never` with no error.
			name: "AStoreOfAParameterNothingCalls",
			src: `
val mut seen = Map()
fn remember(value) { seen.set("last", value) }
`,
			values: map[string]string{
				"seen":     "Map<string, never>",
				"remember": "fn (value: never) -> undefined",
			},
		},
		{
			// #2305 warns on the `mut` of seen, since nothing writes it.
			name: "AMutableContainerNothingWrites",
			src: `
val mut seen = Map()
`,
			values: map[string]string{"seen": "Map<never, never>"},
		},
		{
			name: "AnImmutableContainerNothingCanWrite",
			src: `
val seen = Map()
`,
			values: map[string]string{"seen": "Map<never, never>"},
		},
		{
			// No use decides the map's types, and an importer sees them as `never`. #2354
			// decides whether this reports an error or a warning asking for an annotation.
			name: "AnExportedContainer",
			src: `
export val seen = Map()
`,
			values: map[string]string{"seen": "Map<never, never>"},
		},
		{
			name: "AnExportedMutableContainerItsFunctionsWrite",
			src: `
export val mut seen = Map()
fn a() { seen.set("k", 1) }
`,
			values: map[string]string{"seen": "Map<string, number>"},
		},
		{
			// #2305 warns on the `mut` of b, since nothing in the module writes it, unless it
			// exempts an exported binding an importer can write.
			name: "AnExportedMutableClassInstance",
			src: `
class Box<T> { value: T }
export val mut b = Box(5)
`,
			values: map[string]string{"b": "Box<number>"},
		},
		{
			// An immutable binding keeps its literals, so `five` is `5` the way
			// `val five = 5` is.
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
			// #2349 quantifies the closure makeId returns on the closure itself, so g
			// becomes `fn <T0>(x: T0) -> T0`.
			name: "AFunctionACallReturnsIsSettledByItsUse",
			src: `
fn makeId() { return fn (x) { return x } }
val g = makeId()
fn use() { return g(1) }
`,
			values: map[string]string{
				"g":   "fn (x: 1) -> 1",
				"use": "fn () -> 1",
			},
		},
		{
			// #2349 quantifies the closure makeId returns on the closure itself, so g
			// becomes `fn <T0>(x: T0) -> T0`.
			name: "AFunctionACallReturnsThatNothingCalls",
			src: `
fn makeId() { return fn (x) { return x } }
val g = makeId()
`,
			values: map[string]string{"g": "fn (x: never) -> never"},
		},
		{
			// An importer sees the same type, so it cannot call g with a value the module
			// never settled g on. #2349 quantifies the closure makeId returns on the closure
			// itself, so g becomes `fn <T0>(x: T0) -> T0`.
			name: "AnExportedFunctionACallReturnsThatNothingCalls",
			src: `
fn makeId() { return fn (x) { return x } }
export val g = makeId()
`,
			values: map[string]string{"g": "fn (x: never) -> never"},
		},
		{
			// The parameter and the result are one variable, so both show every value the
			// module passed. #2349 quantifies the closure makeId returns on the closure itself,
			// so g becomes `fn <T0>(x: T0) -> T0`.
			name: "AnExportedFunctionACallReturnsCalledTwice",
			src: `
fn makeId() { return fn (x) { return x } }
export val g = makeId()
val n = g(5)
val s = g("a")
`,
			values: map[string]string{"g": `fn (x: 5 | "a") -> 5 | "a"`},
		},
		{
			// y flows into g's parameter, which is weak, so h does not quantify y. Nothing
			// calls h or g, so both show `never` in every position.
			name: "AFunctionPassingItsParameterToAWeakFunction",
			src: `
fn makeId() { return fn (x) { return x } }
val g = makeId()
fn h(y) { return g(y) }
`,
			values: map[string]string{
				"g": "fn (x: never) -> never",
				"h": "fn (y: never) -> never",
			},
		},
		{
			// Each call of h passes its argument on to g, so g and h settle together on both
			// arguments.
			name: "AFunctionPassingItsParameterToAWeakFunctionCalledTwice",
			src: `
fn makeId() { return fn (x) { return x } }
val g = makeId()
fn h(y) { return g(y) }
val n = h(5)
val s = h("a")
`,
			values: map[string]string{
				"g": `fn (x: 5 | "a") -> 5 | "a"`,
				"h": `fn (y: 5 | "a") -> 5 | "a"`,
				"n": `5 | "a"`,
				"s": `5 | "a"`,
			},
		},
		{
			// h and g name each other, so they share a component, and h is finalized before
			// g. h flows into pick's `f` and y flows into g's parameter, so h quantifies
			// nothing. Its parameter shows the arguments of every call, as in the case
			// above where h and g are in separate components.
			name: "AFunctionInAWeakBindingsComponentStaysMonomorphic",
			src: `
fn pick(f) { return fn (x) { return x } }
fn h(y) { return g(y) }
val g = pick(h)
val n = h(5)
val s = h("a")
`,
			values: map[string]string{
				"g": `fn (x: 5 | "a") -> 5 | "a"`,
				"h": `fn (y: 5 | "a") -> 5 | "a"`,
				"n": `5 | "a"`,
				"s": `5 | "a"`,
			},
		},
		{
			// z flows only into the result, never into g, so h still quantifies it.
			name: "AParameterThatSkipsTheWeakFunctionStaysGeneric",
			src: `
fn makeId() { return fn (x) { return x } }
val g = makeId()
fn h(y, z) {
	g(y)
	return z
}
`,
			values: map[string]string{"h": "fn <T0>(y: never, z: T0) -> T0"},
		},
		{
			// y and z both flow into the result, but only y flows into g. Every call shares
			// y, so each result holds both values passed for y, and only its own z.
			name: "AParameterJoinedWithAWeakInputStaysGeneric",
			src: `
fn makeId() { return fn (x) { return x } }
val g = makeId()
fn h(y, z, c: boolean) {
	g(y)
	return if c { y } else { z }
}
val a = h(1, "a", true)
val b = h(2, true, true)
`,
			values: map[string]string{
				"h": "fn <T0>(y: 1 | 2, z: T0, c: boolean) -> T0 | 1 | 2",
				"a": `1 | "a" | 2`,
				"b": "1 | 2 | true",
			},
		},
		{
			// No value but `never` flows into T, so its type is what its uses require. Each
			// call needs a different object, and g settles to their intersection.
			name: "AWeakVariableOnlyUsesDecideIsTheirIntersection",
			src: `
declare fn mk<T>() -> fn (x: T) -> T
val g = mk()
declare val z: never
fn u() {
	val a: {x: number} = g(z)
	val b: {y: string} = g(z)
}
`,
			values: map[string]string{"g": "fn (x: {x: number} & {y: string}) -> {x: number} & {y: string}"},
		},
		{
			// T appears only where h produces a value, so coalescing drops it before its uses
			// are read. #2351 keeps it, so h becomes `fn () -> {x: number} & {y: string}`.
			name: "AWeakVariableInOnePositionOnlyUsesDecide",
			src: `
declare fn mk<T>() -> fn () -> T
val h = mk()
fn u() {
	val a: {x: number} = h()
	val b: {y: string} = h()
}
`,
			values: map[string]string{"h": "fn () -> never"},
		},
		{
			// make's type comes from the bounds the checker uses for m, which keep the literals.
			// #2348 widens those bounds, so make becomes `fn () -> Map<string, number | string>`.
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
			// The `6` the pattern tests is not part of `o`'s type, which keeps the `5` it
			// was built from.
			// #2355 reports `6` as a pattern that cannot match `5`, so this case then needs
			// `val mut o`, which makes the element `number`.
			name: "APatternTestLeavesASharedElementAlone",
			src: `
val mk = fn (v) { return [v] }
val o = mk(5)
fn f() { return match o[0] { 6 => "six", _ => "other" } }
`,
			values: map[string]string{"o": "[5]"},
		},
		{
			// An immutable binding keeps its literals whether its initializer is a call or
			// the literal itself.
			name: "AnImmutableCallResultKeepsItsLiterals",
			src: `
declare fn id<T>(x: T) -> T
val a = [5, 10]
val c = id([5, 10])
`,
			values: map[string]string{"a": "[5, 10]", "c": "[5, 10]"},
		},
		{
			// Nothing can write the value inside a promise, so a `mut` binding keeps its
			// literal. A `var` binding can be rebound to any promise, so it widens. #2304 warns
			// on the `mut` of p, since a promise has no writable slot.
			name: "APromiseWidensOnlyWhenItsBindingIsReassignable",
			src: `
async fn fetchNum() { return 5 }
val mut p = fetchNum()
var q = fetchNum()
`,
			values: map[string]string{"p": "Promise<5>", "q": "Promise<number>"},
		},
		{
			// A `mut` binding can write `Box`'s field, so `T` widens. `Frozen`'s field is
			// `readonly`, so its literal stays unless the binding can be rebound. #2304 warns on
			// the `mut` of `f`, since `f` has no writable slot. #2305 warns on the `mut` of b,
			// since nothing writes it.
			name: "AClassArgumentWidensWhenAMutableOwnerCanWriteIt",
			src: `
class Box<T> { value: T }
class Frozen<T> { readonly value: T }
val mut b = Box(5)
val mut f = Frozen(5)
var g = Frozen(5)
`,
			values: map[string]string{"b": "Box<number>", "f": "Frozen<5>", "g": "Frozen<number>"},
		},
		{
			// A write to the element or field replaces the promise in it, so the promise's
			// argument widens too. #2305 warns on the `mut` of a and o, since nothing writes them.
			name: "APromiseInsideAWritableSlotWidens",
			src: `
declare fn id<T>(x: T) -> T
async fn fetchNum() { return 5 }
val mut a = id([fetchNum()])
val mut o = id({x: 1, y: fetchNum()})
`,
			values: map[string]string{"a": "[Promise<number>]", "o": "{x: number, y: Promise<number>}"},
		},
		{
			// `T` appears only in a parameter of `put`, so it is contravariant, and widening
			// it would claim `s` accepts every number. #2304 warns on the `mut` of s, since
			// `Sink` has no writable slot.
			name: "AContravariantClassArgumentKeepsItsLiteral",
			src: `
class Sink<T> { put(&self, x: T) -> undefined { return undefined } }
declare fn mkSink<T>(x: T) -> Sink<T>
val mut s = mkSink(5)
`,
			values: map[string]string{"s": "Sink<5>"},
		},
		{
			// An immutable binding of a borrow keeps its literals.
			name: "AnImmutableBorrowKeepsItsLiterals",
			src: `
val p = {x: 1}
fn borrow(q: &{x: 1}) -> &{x: 1} { return q }
val r = borrow(&p)
`,
			values: map[string]string{"r": "&{x: 1}"},
		},
		{
			// The literal belongs to p, so widening r to `&mut {x: number}` would let r write
			// a value the type of p rejects.
			name: "AnImmutableMutableBorrowKeepsItsLiterals",
			src: `
val mut p: {x: 1} = {x: 1}
fn borrowMut(q: &mut {x: 1}) -> &mut {x: 1} { return q }
val r = borrowMut(&mut p)
`,
			values: map[string]string{"r": "&mut {x: 1}"},
		},
		{
			// The map's values are writable slots, so a write can store a borrow of any
			// `{x: number}`, and `&` accepts it.
			name: "AnImmutableBorrowInAWritableSlotWidens",
			src: `
val p = {x: 1}
val mut seen = Map()
fn f() { seen.set("k", &p) }
`,
			values: map[string]string{"seen": "Map<string, &{x: number}>"},
		},
		{
			// A `var` binding can be rebound to a borrow of any `{x: number}`. A `val` binding
			// of the same borrow cannot, so it keeps the literal that belongs to p.
			name: "AnImmutableBorrowInAReassignableBindingWidens",
			src: `
val p = {x: 1}
fn borrow(q: &{x: 1}) -> &{x: 1} { return q }
var r = borrow(&p)
val s = borrow(&p)
`,
			values: map[string]string{"r": "&{x: number}", "s": "&{x: 1}"},
		},
		{
			// The map holds `&mut p`, and a write through a `&mut` lands in p. p is declared
			// `{x: 1}`, so the referent has to keep that type exactly. `&mut` is invariant, so
			// the map can only hold `&mut {x: 1}`. Widening it to `&mut {x: number}` would let
			// anything that reads the map write `x = 2` into p. The literals therefore stay
			// even though the map's values are writable slots. Only the borrow in the slot
			// could be replaced, and only by another `&mut {x: 1}`.
			name: "AMutableBorrowInAWritableSlotKeepsItsLiterals",
			src: `
val mut p: {x: 1} = {x: 1}
val mut seen = Map()
fn f() { seen.set("k", &mut p) }
`,
			values: map[string]string{"seen": "Map<string, &mut {x: 1}>"},
		},
		{
			// setX can write any number to x. Passing it the borrow the map holds would write
			// through to p, whose x is declared 1, so the call is rejected.
			name: "AMutableBorrowFromAWritableSlotRejectsAWiderWrite",
			src: `
val mut p: {x: 1} = {x: 1}
val mut seen = Map()
fn f() { seen.set("k", &mut p) }
fn setX(r: &mut {x: number} | undefined) {}
fn g() { setX(seen.get("k")) }
`,
			values: map[string]string{"seen": "Map<string, &mut {x: 1}>"},
			want: []string{
				"6:15-6:28: cannot constrain mut object <: mut object | undefined",
			},
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
	require.Equal(t, "Map<string, number>", soltype.Print(res.Info.DisplayTypeOf(seen.Pattern)))
	// #2348 widens the weak variable in the checked type as well, so get becomes
	// `fn () -> number | undefined`.
	require.Equal(t, "fn () -> 1 | undefined", soltype.Print(res.Info.DisplayTypeOf(get.Name)))
}

// TestWeakLocalInfoShowsTheSettledType covers the type Info records for a body-level
// binding that did not generalize. The type settles once the module is walked, so it shows
// writes that come after the declaration and writes to a module-level binding it reads.
func TestWeakLocalInfoShowsTheSettledType(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "WritesAfterTheDeclaration",
			src: `
fn f() -> number {
	val mut m = Map()
	m.set("k", 1)
	return m.size
}
`,
			want: "Map<string, number>",
		},
		{
			name: "AWeakModuleBindingALaterFunctionWrites",
			src: `
val mut seen = Map()
fn f() -> number {
	val mut m = Map()
	m.set("a", seen.get("k"))
	return m.size
}
fn put() { seen.set("k", 1) }
`,
			want: "Map<string, number | undefined>",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			module := parseModule(t, tt.src)
			res := InferModuleAgainstStdlib(module, benchTree, Options{})
			require.Empty(t, res.Errors)
			ns, ok := module.Namespaces.Get("")
			require.True(t, ok)
			var f *ast.FuncDecl
			for _, d := range ns.Decls {
				if fd, isFunc := d.(*ast.FuncDecl); isFunc && fd.Name.Name == "f" {
					f = fd
				}
			}
			require.NotNil(t, f)
			decl, ok := f.Body.Stmts[0].(*ast.DeclStmt)
			require.True(t, ok)
			m, ok := decl.Decl.(*ast.VarDecl)
			require.True(t, ok)
			require.Equal(t, tt.want, soltype.Print(res.Info.DisplayTypeOf(m.Pattern)))
		})
	}
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
