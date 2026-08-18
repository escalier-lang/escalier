package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// Exactness derives from an operator's operands rather than being declared on the result
// (exact-types §7). Each case names an operand through an alias and asserts the stored `Result`
// stays symbolic. Reducing it — the expansion constrain performs to check a constraint — yields a
// type whose trailing `...` marker came from the operand's. The exact and inexact spellings of one
// operand sit next to each other so the derivation is visible as a pair.
func TestInferOperatorExactnessPropagates(t *testing.T) {
	tests := []struct {
		name         string
		src          string
		wantSymbolic string
		wantExpanded string
	}{
		{
			// An exact tuple's index set is closed: it has exactly the positions it lists.
			name: "KeyofExactTuple",
			src: `
				type Tup = [number, string]
				type Result = keyof Tup
			`,
			wantSymbolic: "keyof Tup",
			wantExpanded: "0 | 1",
		},
		{
			// An inexact tuple has unknown trailing positions, so its index set is open and carries
			// the indices those positions occupy in a trailing `...`. Those indices are positions,
			// so the tail is bounded by `number`.
			name: "KeyofInexactTuple",
			src: `
				type Tup = [number, string, ...]
				type Result = keyof Tup
			`,
			wantSymbolic: "keyof Tup",
			wantExpanded: "0 | 1 | ...number",
		},
		{
			// `T[keyof T]` over an exact object reads every key the object declares, and those are
			// all the keys it has, so the value union is closed.
			name: "IndexEveryKeyOfExactObject",
			src: `
				type Obj = {a: number, b: string}
				type Result = Obj[keyof Obj]
			`,
			wantSymbolic: "Obj[keyof Obj]",
			wantExpanded: "number | string",
		},
		{
			// `keyof` over an inexact object yields an open key set, and each key the access could
			// not enumerate holds a value the union does not list, so the value union is open too.
			name: "IndexEveryKeyOfInexactObject",
			src: `
				type Obj = {a: number, b: string, ...}
				type Result = Obj[keyof Obj]
			`,
			wantSymbolic: "Obj[keyof Obj]",
			wantExpanded: "number | string | ...",
		},
		{
			// The tuple twin: an inexact tuple's open index set carries into the element union.
			name: "IndexEveryIndexOfInexactTuple",
			src: `
				type Tup = [number, string, ...]
				type Result = Tup[keyof Tup]
			`,
			wantSymbolic: "Tup[keyof Tup]",
			wantExpanded: "number | string | ...",
		},
		{
			// An exact target union lists every member, so reading K off each reads every value K
			// can hold.
			name: "IndexExactUnionTarget",
			src: `
				type U = {k: number} | {k: string}
				type Result = U["k"]
			`,
			wantSymbolic: `U["k"]`,
			wantExpanded: "number | string",
		},
		{
			// An inexact target union has unlisted members, and the value each holds at K is not
			// among the ones the access read.
			name: "IndexInexactUnionTarget",
			src: `
				type U = {k: number} | {k: string} | ...
				type Result = U["k"]
			`,
			wantSymbolic: `U["k"]`,
			wantExpanded: "number | string | ...",
		},
		{
			// A conditional over an exact union decides every member, so the branches those members
			// selected are the only results. Each member wraps itself, so the two results differ and
			// neither absorbs the other.
			name: "CondDistributeExactUnion",
			src: `
				type Wrap<T> = if T : string { [T] } else { boolean }
				type Result = Wrap<"a" | "b">
			`,
			wantSymbolic: `Wrap<"a" | "b">`,
			wantExpanded: `["a"] | ["b"]`,
		},
		{
			// An inexact union's tail is unknown-typed, and `unknown : string` is undecidable, so
			// which branch those members select cannot be worked out and the result stays open.
			name: "CondDistributeInexactUnion",
			src: `
				type Wrap<T> = if T : string { [T] } else { boolean }
				type Result = Wrap<"a" | "b" | ...>
			`,
			wantSymbolic: `Wrap<"a" | "b" | ...>`,
			wantExpanded: `["a"] | ["b"] | ...`,
		},
		{
			// Every interpolation names a closed set of choices, so the strings the template
			// produces are a closed set too.
			name: "TemplateLitExactInterp",
			src: `
				type Side = "left" | "right"
				type Result = ` + "`pad-${Side}`" + `
			`,
			wantSymbolic: "`pad-${Side}`",
			wantExpanded: `"pad-left" | "pad-right"`,
		},
		{
			// An interpolation naming an open set of choices produces an open set of strings.
			// The tail is bounded by `string` rather than left open, because a template
			// produces a string whatever it interpolates. An open tail would accept a `5`.
			name: "TemplateLitInexactInterp",
			src: `
				type Side = "left" | "right" | ...
				type Result = ` + "`pad-${Side}`" + `
			`,
			wantSymbolic: "`pad-${Side}`",
			wantExpanded: `"pad-left" | "pad-right" | ...string`,
		},
		{
			// A string intrinsic maps each member of a closed operand union, and that is every
			// string the operand names.
			name: "StringIntrinsicExactOperand",
			src: `
				type Names = "a" | "b"
				type Result = Uppercase<Names>
			`,
			wantSymbolic: "Uppercase<Names>",
			wantExpanded: `"A" | "B"`,
		},
		{
			// An open operand union names strings the transform never sees, so the result is open.
			name: "StringIntrinsicInexactOperand",
			src: `
				type Names = "a" | "b" | ...
				type Result = Uppercase<Names>
			`,
			wantSymbolic: "Uppercase<Names>",
			wantExpanded: `"A" | "B" | ...`,
		},
		{
			// An intersection carries both operands' members, so its key sets union. An inexact
			// operand's open key set leaves the whole union open, bounded by `string` because the
			// keys it did not list are still property names.
			name: "KeyofIntersectionWithInexactMember",
			src: `
				type I = {a: number} & {b: string, ...}
				type Result = keyof I
			`,
			wantSymbolic: "keyof I",
			wantExpanded: `"a" | "b" | ...string`,
		},
		{
			// `Exact` and `Inexact` are the two operators that run the other way. Every case above
			// reads a marker off its operand and carries it to the result; these two write the
			// marker the operator names onto whatever their operand carried. A function's marker is
			// its trailing `...`, so widening an exact function adds one.
			name: "InexactOverExactFunc",
			src: `
				type ExactF = fn (a: number) -> string
				type Result = Inexact<ExactF>
			`,
			wantSymbolic: "Inexact<ExactF>",
			wantExpanded: "fn (a: number, ...) -> string",
		},
		{
			// The dual, tightening an inexact function by clearing that marker.
			name: "ExactOverInexactFunc",
			src: `
				type InexactF = fn (a: number, ...) -> string
				type Result = Exact<InexactF>
			`,
			wantSymbolic: "Exact<InexactF>",
			wantExpanded: "fn (a: number) -> string",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, ctx, errs := inferTypeNodes(t, tt.src)
			require.Empty(t, errs)
			result := nodes["Result"]
			require.Equal(t, tt.wantSymbolic, soltype.Print(result))
			require.Equal(t, tt.wantExpanded, soltype.Print(expandAliasResidual(ctx, result)))
		})
	}
}

// `Exact<T>` clears a type's trailing `...` marker and `Inexact<T>` sets it (exact-types §6.1,
// §6.2). Each case stores the operator unreduced, so `Result` renders the way the source wrote it,
// and reduces it to assert the marker the operator wrote.
func TestInferExactnessIntrinsics(t *testing.T) {
	tests := []struct {
		name         string
		src          string
		wantSymbolic string
		wantExpanded string
	}{
		{
			// The object case: `Inexact` opens a closed member list.
			name: "InexactObject",
			src: `
				type Point = {x: number, y: number}
				type Result = Inexact<Point>
			`,
			wantSymbolic: "Inexact<Point>",
			wantExpanded: "{x: number, y: number, ...}",
		},
		{
			// The dual: `Exact` closes an open member list.
			name: "ExactObject",
			src: `
				type Headers = {"content-type": string, ...}
				type Result = Exact<Headers>
			`,
			wantSymbolic: "Exact<Headers>",
			wantExpanded: `{"content-type": string}`,
		},
		{
			// Applying an operator to a type that already carries its marker changes nothing.
			name: "ExactOfExactObject",
			src: `
				type Point = {x: number}
				type Result = Exact<Point>
			`,
			wantSymbolic: "Exact<Point>",
			wantExpanded: "{x: number}",
		},
		{
			// The two operators round-trip, so wrapping and unwrapping recovers the operand.
			name: "RoundTrip",
			src: `
				type Point = {x: number, y: number}
				type Result = Exact<Inexact<Point>>
			`,
			wantSymbolic: "Exact<Inexact<Point>>",
			wantExpanded: "{x: number, y: number}",
		},
		{
			name: "InexactTuple",
			src: `
				type Pair = [string, number]
				type Result = Inexact<Pair>
			`,
			wantSymbolic: "Inexact<Pair>",
			wantExpanded: "[string, number, ...]",
		},
		{
			name: "ExactTuple",
			src: `
				type Pair = [string, number, ...]
				type Result = Exact<Pair>
			`,
			wantSymbolic: "Exact<Pair>",
			wantExpanded: "[string, number]",
		},
		{
			// An inexact function tolerates extra arguments. Subtyping cannot widen an exact
			// function into one, so `Inexact<F>` is how a program asks for that widening.
			name: "InexactFunc",
			src: `
				type F = fn (a: number) -> string
				type Result = Inexact<F>
			`,
			wantSymbolic: "Inexact<F>",
			wantExpanded: "fn (a: number, ...) -> string",
		},
		{
			name: "ExactFunc",
			src: `
				type F = fn (a: number, ...) -> string
				type Result = Exact<F>
			`,
			wantSymbolic: "Exact<F>",
			wantExpanded: "fn (a: number) -> string",
		},
		{
			// A generic function's `<T>` binder is part of its type, so rewriting the marker leaves
			// the binder and every position naming it in place.
			name: "InexactGenericFunc",
			src: `
				type G = fn <T>(x: T) -> T
				type Result = Inexact<G>
			`,
			wantSymbolic: "Inexact<G>",
			wantExpanded: "fn <T>(x: T, ...) -> T",
		},
		{
			// The round trip holds for a function too, so widening an exact function and tightening
			// the result recovers the operand.
			name: "RoundTripFuncFromExact",
			src: `
				type F = fn (a: number) -> string
				type Result = Exact<Inexact<F>>
			`,
			wantSymbolic: "Exact<Inexact<F>>",
			wantExpanded: "fn (a: number) -> string",
		},
		{
			// And from the other end, tightening an inexact function and widening the result.
			name: "RoundTripFuncFromInexact",
			src: `
				type F = fn (a: number, ...) -> string
				type Result = Inexact<Exact<F>>
			`,
			wantSymbolic: "Inexact<Exact<F>>",
			wantExpanded: "fn (a: number, ...) -> string",
		},
		{
			name: "InexactUnion",
			src: `
				type Color = "red" | "green" | "blue"
				type Result = Inexact<Color>
			`,
			wantSymbolic: "Inexact<Color>",
			wantExpanded: `"blue" | "green" | "red" | ...`,
		},
		{
			name: "ExactUnion",
			src: `
				type Color = "red" | "green" | "blue" | ...
				type Result = Exact<Color>
			`,
			wantSymbolic: "Exact<Color>",
			wantExpanded: `"blue" | "green" | "red"`,
		},
		{
			// Closing a one-member union collapses it to that member, since the wrapper only ever
			// carried the open tail.
			name: "ExactSingleMemberUnion",
			src: `
				type Obj = {only: number, ...}
				type Result = Exact<keyof Obj>
			`,
			wantSymbolic: "Exact<keyof Obj>",
			wantExpanded: `"only"`,
		},
		{
			// A primitive names one kind of value with no member list to close, so neither
			// operator has anything to change.
			name: "ExactPrimitive",
			src: `
				type Result = Exact<number>
			`,
			wantSymbolic: "Exact<number>",
			wantExpanded: "number",
		},
		{
			// Mutability is orthogonal to exactness, so the operator reaches the pointee.
			name: "InexactThroughMut",
			src: `
				type Point = {x: number}
				type Result = Inexact<mut Point>
			`,
			wantSymbolic: "Inexact<mut Point>",
			wantExpanded: "mut {x: number, ...}",
		},
		{
			// An intersection's exactness is its members', so the operator reaches each of them.
			name: "InexactThroughIntersection",
			src: `
				type I = {a: number} & {b: string}
				type Result = Inexact<I>
			`,
			wantSymbolic: "Inexact<I>",
			wantExpanded: "{a: number, ...} & {b: string, ...}",
		},
		{
			// A final class has no subclasses to widen it, so its instance type is already exact.
			name: "ExactFinalClass",
			src: `
				final class Point {
					x: number,
				}
				type Result = Exact<Point>
			`,
			wantSymbolic: "Exact<Point>",
			wantExpanded: "Point",
		},
		{
			// A class's exactness is fixed where the class is declared, so `Inexact` has no use-site
			// form to open a final class's instance type with and leaves the class unchanged.
			name: "InexactFinalClass",
			src: `
				final class Point {
					x: number,
				}
				type Result = Inexact<Point>
			`,
			wantSymbolic: "Inexact<Point>",
			wantExpanded: "Point",
		},
		{
			// A non-final class instance type is already open, so `Inexact` has nothing to change.
			// The `Exact` direction over the same class is the one that is rejected, in
			// TestInferExactOnNonFinalClassErrors.
			name: "InexactNonFinalClass",
			src: `
				class Point {
					x: number,
				}
				type Result = Inexact<Point>
			`,
			wantSymbolic: "Inexact<Point>",
			wantExpanded: "Point",
		},
		{
			// The operator composes with the rest of the suite: closing an object's key set closes
			// the key union `keyof` projects from it.
			name: "KeyofOverExactOperand",
			src: `
				type Obj = {a: number, b: string, ...}
				type Result = keyof Exact<Obj>
			`,
			wantSymbolic: "keyof Exact<Obj>",
			wantExpanded: `"a" | "b"`,
		},
		{
			// The dual composition: opening an object's key set opens the key union.
			name: "KeyofOverInexactOperand",
			src: `
				type Obj = {a: number, b: string}
				type Result = keyof Inexact<Obj>
			`,
			wantSymbolic: "keyof Inexact<Obj>",
			wantExpanded: `"a" | "b" | ...string`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, ctx, errs := inferTypeNodes(t, tt.src)
			require.Empty(t, errs)
			result := nodes["Result"]
			require.Equal(t, tt.wantSymbolic, soltype.Print(result))
			require.Equal(t, tt.wantExpanded, soltype.Print(expandAliasResidual(ctx, result)))
		})
	}
}

// An operator over a type parameter has no operand to read a marker off, so it stays symbolic and
// renders the way the source wrote it. That is what lets it sit unreduced in a signature until a
// call grounds it. The reflexive `Inexact<T> <: Inexact<T>` the `return x` raises succeeds inertly
// by structural identity, with neither side reduced.
func TestInferExactnessIntrinsicStaysSymbolic(t *testing.T) {
	values, _, errs := inferSource(t, `fn f<T>(x: Inexact<T>) -> Inexact<T> { return x }`)
	require.Empty(t, errs)
	require.Equal(t, "fn <T>(x: Inexact<T>) -> Inexact<T>", values["f"])
}

// constrain reduces an exactness residual when it checks a constraint against one, so the marker the
// operator wrote is what the check enforces. Each case pairs a value against an annotation the
// operator built, since the marker only shows up as an accept or a reject.
func TestInferExactnessIntrinsicChecksConstraints(t *testing.T) {
	t.Run("inexact parameter accepts an extra property", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			type Point = {x: number}
			fn f(p: Inexact<Point>) -> number { return 1 }
			val r = f({x: 1, y: 2})
		`)
		require.Empty(t, errs)
	})

	t.Run("exact parameter rejects an extra property", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			type Open = {x: number, ...}
			fn f(p: Exact<Open>) -> number { return 1 }
			val r = f({x: 1, y: 2})
		`)
		require.Len(t, errs, 1)
		require.Equal(t, "4:24-4:25: object has extra property: y", msgWithSpan(errs[0]))
	})

	t.Run("exact parameter accepts an exactly-matching argument", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			type Open = {x: number, ...}
			fn f(p: Exact<Open>) -> number { return 1 }
			val r = f({x: 1})
		`)
		require.Empty(t, errs)
	})

	// The converted function type is usable as the form the operator named, not merely rendered as
	// it. Each case passes the argument only the converted form admits, so the check would fail
	// against the operand the source wrote.
	t.Run("tightened parameter accepts an exact function", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			type LooseF = fn (a: number, ...) -> string
			fn exactFn(a: number) -> string { return "x" }
			fn take(cb: Exact<LooseF>) -> number { return 1 }
			val r = take(exactFn)
		`)
		require.Empty(t, errs)
	})

	t.Run("widened parameter accepts an inexact function", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			type ExactF = fn (a: number) -> string
			declare fn looseFn(a: number, ...) -> string
			fn take(cb: Inexact<ExactF>) -> number { return 1 }
			val r = take(looseFn)
		`)
		require.Empty(t, errs)
	})
}

// Function exactness inverts the asymmetry objects have. An object grows toward its inexact form, so
// exact `<:` inexact. A function's accept-set shrinks, so an exact function tolerates fewer calls
// than its inexact counterpart and inexact `<:` exact instead (exact-types §4.2.1.1). A conditional
// decides its branch with the same subtype check, so the operators move a function type across that
// boundary in the direction the marker names.
//
// This is the behavior `Inexact<F>` exists for. Subtyping alone never widens an exact function into
// an inexact one, so the operator is the only way to write that step (§6.2).
func TestInferExactnessIntrinsicOnFuncSubtyping(t *testing.T) {
	tests := []struct {
		name string
		cond string
		want string
	}{
		{
			// An exact function does not satisfy an inexact one: its accept-set refuses the extra
			// arguments the inexact type's callers may pass.
			name: "ExactAgainstInexactFails",
			cond: "if ExactF : LooseF { \"yes\" } else { \"no\" }",
			want: `"no"`,
		},
		{
			// The reverse holds. An inexact function already tolerates every call the exact type
			// admits, so it satisfies the narrower one.
			name: "InexactAgainstExactHolds",
			cond: "if LooseF : ExactF { \"yes\" } else { \"no\" }",
			want: `"yes"`,
		},
		{
			// `Inexact` moves the exact operand across that boundary, flipping the first case. This
			// is the widening subtyping cannot give you.
			name: "InexactOperandFlipsTheCheck",
			cond: "if Inexact<ExactF> : LooseF { \"yes\" } else { \"no\" }",
			want: `"yes"`,
		},
		{
			// `Exact` moves the inexact operand the other way, so it stops satisfying the inexact
			// pattern that the bare `LooseF` satisfied.
			name: "ExactOperandFlipsTheCheck",
			cond: "if Exact<LooseF> : LooseF { \"yes\" } else { \"no\" }",
			want: `"no"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, ctx, errs := inferTypeNodes(t, `
				type ExactF = fn (a: number) -> string
				type LooseF = fn (a: number, ...) -> string
				type Result = `+tt.cond+`
			`)
			require.Empty(t, errs)
			require.Equal(t, tt.want, soltype.Print(expandAliasResidual(ctx, nodes["Result"])))
		})
	}
}

// `Inexact<F>` rewrites a type, not a value. An exact function value still fails to fill a slot the
// operator widened, since the reduced annotation is an ordinary inexact function type and the
// exact-does-not-satisfy-inexact rule applies to it unchanged. Converting a value is what
// exact-types §6.6's separate `exact<T>(v)` operator is for, which this milestone does not add.
func TestInferInexactFuncAnnotationDoesNotWidenAValue(t *testing.T) {
	t.Run("exact function fills the exact slot", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			type F = fn (a: number) -> string
			fn exactFn(a: number) -> string { return "x" }
			fn take(cb: F) -> number { return 1 }
			val r = take(exactFn)
		`)
		require.Empty(t, errs)
	})

	t.Run("the same value fails the widened slot", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			type F = fn (a: number) -> string
			fn exactFn(a: number) -> string { return "x" }
			fn take(cb: Inexact<F>) -> number { return 1 }
			val r = take(exactFn)
		`)
		require.Len(t, errs, 1)
		require.Equal(t,
			"3:4-3:50: cannot constrain function of arity 1 <: function of arity 1 or more",
			msgWithSpan(errs[0]))
	})
}

// A user-defined type named `Exact` shadows the intrinsic, since the type scope resolves first. The
// reference then names that alias rather than the operator, so it expands to the alias body.
func TestInferExactnessIntrinsicShadowedByUserType(t *testing.T) {
	nodes, ctx, errs := inferTypeNodes(t, `
		type Exact<T> = [T]
		type Result = Exact<number>
	`)
	require.Empty(t, errs)
	result := nodes["Result"]
	require.Equal(t, "Exact<number>", soltype.Print(result))
	require.Equal(t, "[number]", soltype.Print(expandAliasResidual(ctx, result)))
}

// A non-final class can be subclassed, so a subclass instance carries members the class does not
// declare. Escalier fixes a class's exactness where the class is declared, so there is no use-site
// form that closes an open instance type and `Exact<C>` is rejected.
func TestInferExactOnNonFinalClassErrors(t *testing.T) {
	nodes, ctx, errs := inferTypeNodes(t, `
		class Point {
			x: number,
		}
		type Result = Exact<Point>
	`)
	require.Empty(t, errs)
	e := newTypeEvaluator(ctx, newSeenPairs())
	require.Equal(t, "error", soltype.Print(e.reduce(nodes["Result"])))
	require.Len(t, e.errs, 1)
	require.Equal(t,
		"class Point is not final, so a subclass instance may carry members Point does not declare "+
			"and Exact<Point> cannot close it; declare Point as `final` instead",
		e.errs[0].Message())
}

// A borrow of an errored pointee is errored too, so the diagnostic the pointee's reduction reported
// is the one a constraint site names. Reducing the borrow to the error sentinel is what carries it
// there. A reduction that ends on a residual reports no diagnostic at all, so the site would
// otherwise blame that residual and never say which class is not final.
//
// The bare and borrowed spellings therefore report the same message. Each case pairs a parameter the
// operator annotates with an argument, since the reduction only surfaces at a constraint site.
func TestInferExactOnBorrowedNonFinalClassErrors(t *testing.T) {
	tests := []struct {
		name string
		ann  string
	}{
		{"Bare", "Exact<Point>"},
		{"OwnedMut", "Exact<mut Point>"},
		{"MutBorrow", "Exact<&mut Point>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, `
				class Point {
					x: number,
				}
				fn f(p: `+tt.ann+`) -> number { return 1 }
				fn g(q: Point) -> number { return f(q) }
			`)
			require.Len(t, errs, 1)
			require.Contains(t, errs[0].Message(),
				"class Point is not final, so a subclass instance may carry members Point does not "+
					"declare and Exact<Point> cannot close it; declare Point as `final` instead")
		})
	}
}
