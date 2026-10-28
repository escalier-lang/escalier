package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestBindingMutabilityChecks covers the checks a binding written `mut`, as `val mut p: T` or
// `mut p: T`, takes part in: writes through it, moves into it, and the readonly fields a value
// moved into it may not carry. want lists every diagnostic src reports.
func TestBindingMutabilityChecks(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "WriteThroughValMut",
			src: `fn g() -> number {
	val mut p: {a: {b: number}} = {a: {b: 0}}
	p.a.b = 1
	return p.a.b
}`,
		},
		{
			name: "WriteArrayElement",
			src: `fn g() -> number {
	val mut xs: Array<{x: number}> = [{x: 1}]
	xs[0].x = 2
	xs.push({x: 3})
	return xs[0].x
}`,
		},
		{
			name: "UseAfterMoveIntoValMut",
			src: `fn g(q: {x: number}) -> number {
	val mut p: {x: number} = q
	p.x = 1
	return q.x
}`,
			want: []string{"4:9-4:12: use of moved value 'q'"},
		},
		{
			name: "ReadonlyFieldIntoValMut",
			src: `fn g() {
	val q: {readonly a: number} = {a: 1}
	val mut p: {a: number} = q
}`,
			want: []string{"3:27-3:28: readonly field 'a' cannot satisfy a writable field requirement"},
		},
		{
			name: "WriteThroughMutParam",
			src: `fn f(mut p: {x: number}) -> number {
	p.x = 2
	return p.x
}
fn g() -> number {
	val q = {x: 1}
	return f(q)
}`,
		},
		{
			name: "PushOntoMutParam",
			src:  `fn f(mut xs: Array<number>) { xs.push(1) }`,
		},
		{
			name: "UseAfterMoveIntoMutParam",
			src: `fn f(mut p: {x: number}) -> number {
	p.x = 2
	return p.x
}
fn g(q: {x: number}) -> number {
	val n = f(q)
	return q.x
}`,
			want: []string{"7:9-7:12: use of moved value 'q'"},
		},
		{
			name: "MoveIntoMutParamWhileBorrowed",
			src: `fn f(mut p: {x: number}) -> number {
	p.x = 2
	return p.x
}
fn g() -> number {
	val q = {x: 1}
	val r = &q
	return f(q) + r.x
}`,
			want: []string{"8:11-8:12: cannot move 'q' while it is borrowed"},
		},
		{
			name: "WriteThroughDeclareVarMut",
			src: `declare var mut sink: {x: number}
fn f() { sink.x = 1 }`,
		},
		{
			name: "ReassignMutParam",
			src:  `fn f(mut p: {x: number}) { p = {x: 2} }`,
			want: []string{"1:28-1:38: Cannot assign to immutable binding: p"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, errs := inferAgainstTreeWithSpans(t, tt.src)
			require.Equal(t, tt.want, errs)
		})
	}
}

// TestMutParamSignature asserts that a `mut` parameter leaves the function's signature
// with the plain type, so a caller moves any owned value of that type into it. want is
// the type of `f`, and errs lists every diagnostic the program reports.
func TestMutParamSignature(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
		errs []string
	}{
		{
			name: "Write",
			src:  `fn f(mut p: {x: number}) { p.x = 1 }`,
			want: "fn (p: {x: number}) -> undefined",
		},
		// An unannotated `mut` parameter is mutable, and its type comes from its uses.
		{
			name: "UnannotatedWrite",
			src:  `fn f(mut p) { p.x = 1 }`,
			want: "fn (p: {x: number}) -> undefined",
		},
		// Binding mutability is never inferred, so a write through an unannotated
		// parameter without `mut` is rejected. The parameter's shape still comes from its
		// uses. The write could not tell whether `p` is owned, so it asked only that the
		// field exist, and the field's type is left open.
		{
			name: "UnannotatedWriteWithoutMut",
			src:  `fn f(p) { p.x = 1 }`,
			want: "fn (p: {x: unknown}) -> undefined",
			errs: []string{"1:11-1:18: cannot mutate 'p' because its binding is immutable; declare it as `mut p`"},
		},
		// A call does not move its argument into a union parameter, so the `mut` has no
		// effect and the write is rejected.
		{
			name: "UnionWrite",
			src:  `fn f(mut p: {x: number} | {x: string}) { p.x = 1 }`,
			want: "fn (p: {x: number} | {x: string}) -> undefined",
			errs: []string{"1:42-1:49: cannot mutate 'p': a call moves its argument only into a parameter typed as an object, tuple, or class, so `mut` has no effect on this one; annotate it as `mut p: T` with such a type, or as `p: &mut T`"},
		},
		{
			name: "RestPush",
			src:  `fn f(...mut rest: Array<number>) { rest.push(1) }`,
			want: "fn (...rest: Array<number>) -> undefined",
		},
		// A borrow cannot fill the owned parameter. The solver blames the borrow's
		// annotation on `q`.
		{
			name: "BorrowedArgument",
			src: `fn f(mut p: {x: number}) { p.x = 1 }
fn g(q: &{x: number}) { f(q) }`,
			want: "fn (p: {x: number}) -> undefined",
			errs: []string{"2:9-2:21: cannot use borrowed &object as owned object"},
		},
		// A borrow carries its own mutability, so `mut` on the binding does not make the
		// referent writable. The solver blames the parameter's annotation.
		{
			name: "BorrowParamKeepsItsMutability",
			src:  `fn f(mut p: &{x: number}) { p.x = 1 }`,
			want: "fn (p: &{x: number}) -> undefined",
			errs: []string{"1:13-1:25: cannot constrain immutable object <: mutable object"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, errs := inferAgainstTreeWithSpans(t, tt.src)
			require.Equal(t, tt.errs, errs)
			require.Equal(t, tt.want, values["f"])
		})
	}
}

// TestValMutCallResultWidens asserts that a `val mut` binding widens the literals of the
// owned value a call returns, since every owned layer of the value is writable.
func TestValMutCallResultWidens(t *testing.T) {
	values, errs := inferAgainstTreeWithSpans(t, `
		fn mk() { return {x: 1} }
		val mut d = mk()
		val n = d.x
	`)
	require.Empty(t, errs)
	require.Equal(t, "{x: number}", values["d"])
	require.Equal(t, "number", values["n"])
}

// TestDynamicIndexPlace covers how a dynamic index off a binding names the whole binding.
// A write through `xs[1]` is a write to `xs`, so it conflicts with a live borrow of
// another element. A read of one element moves nothing the next read needs.
func TestDynamicIndexPlace(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "WriteWhileElementBorrowed",
			src: `fn g() -> number {
	val mut xs: Array<{x: number}> = [{x: 1}, {x: 2}]
	val r = &xs[0]
	xs[1].x = 3
	return r.x
}`,
			want: []string{"4:2-4:9: cannot assign to 'xs[1].x' while it is borrowed as immutable"},
		},
		{
			name: "ReadTwoElements",
			src: `fn g() {
	val xs: Array<{x: number}> = [{x: 1}, {x: 2}]
	val a = xs[0]
	val b = xs[1]
}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, errs := inferAgainstTreeWithSpans(t, tt.src)
			require.Equal(t, tt.want, errs)
		})
	}
}

// TestValMutBorrowedInitializer asserts that a borrow cannot fill an owned `val mut p: T`
// binding, and that the error names the borrow rather than its mutability.
func TestValMutBorrowedInitializer(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			// The solver blames the borrow's annotation on `q`.
			name: "BorrowedParameter",
			src:  `fn g(q: &{x: number}) { val mut p: {x: number} = q }`,
			want: []string{"1:9-1:21: cannot use borrowed &object as owned object"},
		},
		{
			name: "BorrowReturnedByCall",
			src: `fn h(a: &{x: number}) -> &{x: number} { return a }
fn g() {
	val o = {x: 1}
	val mut p: {x: number} = h(&o)
}`,
			want: []string{"4:13-4:24: cannot use borrowed &object as owned object"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, errs := inferAgainstTreeWithSpans(t, tt.src)
			require.Equal(t, tt.want, errs)
		})
	}
}

// TestValMutUnionWithBorrow asserts that `val mut` makes no owned layer writable when the
// annotation is a union with a borrow member, since the borrow carries its own
// mutability. The write through `p` is rejected.
func TestValMutUnionWithBorrow(t *testing.T) {
	_, errs := inferAgainstTreeWithSpans(t, `fn g() {
	val mut p: {x: number} | &{x: number} = {x: 1}
	p.x = 2
}`)
	require.Equal(t, []string{"3:2-3:9: cannot constrain immutable object <: mutable object"}, errs)
}

// TestAwaitedCallResultOwned covers an `await` of a call as the initializer of a `mut`
// binding. The promise is a temporary, so an owned value it settles to belongs to the binding
// alone. want lists every diagnostic the program reports.
func TestAwaitedCallResultOwned(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "ValMut",
			src: `declare fn load() -> Promise<Array<number>>
async fn g() {
	val mut r = await load()
	r.push(1)
}`,
		},
		{
			name: "AnnotatedMut",
			src: `declare fn load() -> Promise<Array<number>>
async fn g() {
	val mut r: Array<number> = await load()
	r.push(1)
}`,
		},
		// A borrow the promise settles to is still owned by someone else, so the binding
		// gets no mutable access to it.
		{
			name: "SettlesToBorrow",
			src: `declare fn shared() -> Promise<&'static Array<number>>
async fn g() {
	val mut r = await shared()
	r.push(1)
}`,
			want: []string{"4:2-4:8: cannot constrain immutable Array<number> <: mutable Array<number>"},
		},
		// A promise held in a variable can be awaited again, so the value it settles to is
		// not the binding's alone.
		{
			name: "AwaitedVariable",
			src: `declare fn load() -> Promise<Array<number>>
async fn g() {
	val p = load()
	val mut r = await p
	r.push(1)
}`,
			want: []string{"4:14-4:21: cannot bind the value 'p' settles to with `mut`, since awaiting 'p' again yields the same value; await a call that makes a new promise instead"},
		},
		// A `mut` leaf of a destructuring pattern owns its value the way a `val mut` binding
		// does, so it may not share one with another await either.
		{
			name: "AwaitedVariableIntoMutLeaf",
			src: `declare fn load() -> Promise<{a: Array<number>}>
async fn g() {
	val p = load()
	val {mut a} = await p
	a.push(1)
}`,
			want: []string{"4:16-4:23: cannot bind the value 'p' settles to with `mut`, since awaiting 'p' again yields the same value; await a call that makes a new promise instead"},
		},
		// A primitive has no layer to write, so a second holder of it observes nothing.
		{
			name: "AwaitedVariableSettlesToPrimitive",
			src: `declare fn count() -> Promise<number>
async fn g() {
	val p = count()
	val mut n = await p
}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, errs := inferAgainstTreeWithSpans(t, tt.src)
			require.Equal(t, tt.want, errs)
		})
	}
}
