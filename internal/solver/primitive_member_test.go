package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPrimitiveMemberReadsWrapper covers a member read off a primitive, which resolves
// through the class the standard library declares for that primitive's values.
func TestPrimitiveMemberReadsWrapper(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want map[string]string
		// locals are the types of closures the source binds inside a function body, which
		// keep their inferred return types.
		locals map[string]string
	}{
		{
			name: "NumberLiteral",
			src:  `val n = (1.5).toFixed(2)`,
			want: map[string]string{"n": "string"},
		},
		{
			name: "StringLiteralProperty",
			src:  `val n = "abc".length`,
			want: map[string]string{"n": "number"},
		},
		{
			name: "DeclaredString",
			src: `
				declare val s: string
				val up = s.toUpperCase()
			`,
			want: map[string]string{"up": "string"},
		},
		{
			name: "AnnotatedParameter",
			src:  `fn format(x: number) -> string { return x.toFixed(1) }`,
			want: map[string]string{"format": "fn (x: number) -> string"},
		},
		{
			name: "Boolean",
			src: `
				declare val b: boolean
				val v = b.valueOf()
			`,
			want: map[string]string{"v": "boolean"},
		},
		{
			name: "LiteralUnion",
			src: `
				declare val x: 1 | 2
				val s = x.toFixed(1)
			`,
			want: map[string]string{"s": "string"},
		},
		{
			name: "MethodReadAsValue",
			src: `
				declare val x: number
				val f = x.toFixed
			`,
			want: map[string]string{"f": "fn (fractionDigits?: number) -> string"},
		},
		{
			// The wrapper comes from the package that declares it, so a module's own
			// `Number` does not change what a number's members are.
			name: "ModuleClassDoesNotReplaceWrapper",
			src: `
				class Number { x: number }
				val n = (1.5).toFixed(2)
			`,
			want: map[string]string{"n": "string"},
		},
		{
			// `apply`'s parameter types `n` as `number` before the body is checked, so the
			// read finds `toFixed` through `Number`.
			name: "CallbackReceiverSolvedAfterRead",
			src: `
				declare fn apply(f: fn (x: number) -> string) -> string
				val r = apply(fn (n) { return n.toFixed(2) })
			`,
			want: map[string]string{"r": "string"},
		},
		{
			name: "CallbackReceiverSolvedToLiteral",
			src: `
				declare fn applyFive(f: fn (x: 5) -> string) -> string
				val r = applyFive(fn (n) { return n.toString() })
			`,
			want: map[string]string{"r": "string"},
		},
		{
			name: "CallbackReadsPropertyOffWrapper",
			src: `
				declare fn measure(f: fn (s: string) -> number) -> number
				val r = measure(fn (s) { return s.length })
			`,
			want: map[string]string{"r": "number"},
		},
		{
			name: "CallbackDestructuresWrapperProperty",
			src: `
				declare fn measure(f: fn (s: string) -> number) -> number
				val r = measure(fn ({length}) { return length })
			`,
			want: map[string]string{"r": "number"},
		},
		{
			// A `&self` method reads off a receiver solved after the read, the same way
			// a wrapper's method does.
			name: "CallbackReadsImmutSelfMethod",
			src: `
				class Counter {
					count: number,
					get(&self) -> number { return self.count },
				}
				declare fn withCounter(f: fn (c: &Counter) -> number) -> number
				val r = withCounter(fn (c) { return c.get() })
			`,
			want: map[string]string{"r": "number"},
		},
		{
			// Each member reads `toString` off its own wrapper, `Number` for `number`
			// and `String` for `string`, and the two results join.
			name: "MixedPrimitiveUnion",
			src: `
				fn main() -> undefined {
					val g = fn (x: number | string) { return x.toString() }
				}
			`,
			locals: map[string]string{"g": "fn (x: number | string) -> string"},
		},
		{
			name: "PrimitiveAndClassInstanceUnion",
			src: `
				fn main() -> undefined {
					val len = fn (x: string | &Array<number>) { return x.length }
				}
			`,
			locals: map[string]string{"len": "fn (x: string | &Array<number>) -> number"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values, errs := inferAgainstTreeWithSpans(t, test.src)
			require.Empty(t, errs)
			for name, want := range test.locals {
				got, _ := inferLocalAgainstTree(t, test.src, name)
				require.Equal(t, want, got, "local binding %q", name)
			}
			for name, want := range test.want {
				require.Equal(t, want, values[name], "value binding %q", name)
			}
		})
	}
}

// TestPrimitiveMemberRejected covers member reads and annotations that a primitive's
// wrapper class does not satisfy.
func TestPrimitiveMemberRejected(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "MissingMember",
			src:  `val n = (1.5).nope`,
			want: []string{"1:10-1:19: object is missing property: nope"},
		},
		{
			// `y` holds `1` or the object `x` borrows. `1` reads `toFixed` off `Number`
			// and the object has no `toFixed`, so `y.toFixed` may be `undefined` and the
			// call rejects it.
			name: "MixedReceiver",
			src: `
				val f = fn (c: boolean, x: &{a: number}) -> string {
					val y = if c { 1 } else { x }
					return y.toFixed(1)
				}
			`,
			want: []string{"4:13-4:25: cannot constrain undefined <: function"},
		},
		{
			name: "CallbackMissingMember",
			src: `
				declare fn apply(f: fn (x: number) -> string) -> string
				val r = apply(fn (n) { return n.nope() })
			`,
			want: []string{"3:35-3:41: object is missing property: nope"},
		},
		{
			// `withCounter`'s parameter types `c` as `&mut Counter` before the body is checked,
			// so the body may call a `&mut self` method through it.
			name: "CallbackMutSelfMethod",
			src: `
				class Counter {
					count: number,
					inc(&mut self) -> number { return self.count },
				}
				declare fn withCounter(f: fn (c: &mut Counter) -> number) -> number
				val r = withCounter(fn (c) { return c.inc() })
			`,
		},
		{
			name: "CallbackConsumingSelfMethod",
			src: `
				class Counter {
					count: number,
					take(self) -> number { return self.count },
				}
				declare fn withCounter(f: fn (c: &Counter) -> number) -> number
				val r = withCounter(fn (c) { return c.take() })
			`,
			want: []string{"7:41-7:47: 'take' takes its receiver by value, so it moves the instance and cannot be reached through a borrow."},
		},
		{
			// `undefined` has no members to read, so the union read falls back to the
			// rule that every member must carry the property. `number` carries
			// `toString` through `Number`, so only `undefined` is reported.
			name: "UnionWithUndefined",
			src:  `val g = fn (x: number | undefined) -> string { return x.toString() }`,
			want: []string{"1:55-1:65: cannot constrain undefined <: object"},
		},
		{
			name: "ObjectAnnotationWithMethodType",
			src:  `val x: {toFixed: fn () -> string} = 5`,
			want: []string{"1:37-1:38: cannot constrain 5 <: object"},
		},
		{
			name: "InexactObjectAnnotationWithMethodType",
			src:  `val x: {toFixed: fn () -> string, ...} = 5`,
			want: []string{"1:42-1:43: cannot constrain 5 <: object"},
		},
		{
			name: "EmptyInexactObjectAnnotation",
			src: `
				declare val n: number
				val x: {...} = n
			`,
			want: []string{"3:20-3:21: cannot constrain number <: object"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, errs := inferAgainstTreeWithSpans(t, test.src)
			require.Equal(t, test.want, errs)
		})
	}
}

// TestPrimitiveMemberMissingRelatedSpan covers the related span of a member missing from a
// primitive whose members are read through its wrapper class. The span names the primitive,
// which a source node produced, rather than the wrapper's body, which none did.
func TestPrimitiveMemberMissingRelatedSpan(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		want    string
		related []string
	}{
		{
			// `n` takes the parameter type `apply` declares for its callback, as the call
			// instantiates it. That copy records no source node, so the error names none.
			name: "CallbackReceiverTypedByCallee",
			src: `
				declare fn apply(f: fn (x: number) -> string) -> string
				val r = apply(fn (n) { return n.nope() })
			`,
			want:    "3:35-3:41: object is missing property: nope",
			related: []string{},
		},
		{
			name:    "MixedPrimitiveUnion",
			src:     `fn g(x: number | string) { return x.nope }`,
			want:    "1:37-1:41: object is missing property: nope",
			related: []string{"number"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			res := InferModuleAgainstStdlib(parseModule(t, test.src), benchTree, Options{})
			require.Len(t, res.Errors, 1)
			require.Equal(t, test.want, msgWithSpan(t, res.Errors[0]))
			related := []string{}
			for _, span := range res.Errors[0].Related() {
				related = append(related, spanText(test.src, span))
			}
			require.Equal(t, test.related, related)
		})
	}
}
