package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestClosureParamTypedByContext covers the contexts that type a parameter a closure leaves
// unannotated before the closure's body is checked. xType is the type recorded for the
// parameter `x`, and expected lists every diagnostic the source reports. A move out of a
// borrowed `x` reports, blamed where the borrow's type is written, as it is for a parameter
// that writes the type itself.
func TestClosureParamTypedByContext(t *testing.T) {
	const box = "class Box { v: number }\n"
	tests := []struct {
		name     string
		src      string
		xType    string
		expected []string
	}{
		{
			name: "CallArgument",
			src: box + "declare fn each(f: fn (x: &Box) -> undefined) -> undefined\n" +
				"fn g() -> undefined { each(fn (x) { val b: Box = x\n return undefined }) }",
			xType:    "<'a> &'a Box",
			expected: []string{"3:50-3:51: cannot use borrowed &Box as owned Box"},
		},
		{
			name: "AnnotatedBinding",
			src: box + "fn g() -> undefined {\n" +
				"\tval h: fn (x: &Box) -> undefined = fn (x) { val b: Box = x\n return undefined }\n}",
			xType:    "<'a> &'a Box",
			expected: []string{"3:16-3:20: cannot use borrowed &Box as owned Box"},
		},
		{
			name: "StoreIntoTypedField",
			src: box + "fn g(o: &mut {f: fn (x: &Box) -> undefined}) -> undefined {\n" +
				"\to.f = fn (x) { val b: Box = x\n return undefined }\n}",
			xType:    "<'a> &'a Box",
			expected: []string{"2:25-2:29: cannot use borrowed &Box as owned Box"},
		},
		{
			name: "ReturnOfWrittenReturnType",
			src: box + "fn g() -> fn (x: &Box) -> undefined {\n" +
				"\treturn fn (x) { val b: Box = x\n return undefined }\n}",
			xType:    "<'a> &'a Box",
			expected: []string{"2:18-2:22: cannot use borrowed &Box as owned Box"},
		},
		{
			// Every arm that accepts one argument declares `fn (x: &Box) -> undefined`.
			name: "OverloadArmsAgree",
			src: box + "declare fn on(f: fn (x: &Box) -> undefined) -> undefined\n" +
				"declare fn on(f: fn (x: &Box) -> undefined, n?: number) -> number\n" +
				"fn g() -> undefined { on(fn (x) { val b: Box = x\n return undefined }) }",
			xType:    "<'a> &'a Box",
			expected: []string{"4:48-4:49: cannot use borrowed &Box as owned Box"},
		},
		{
			// The arms differ on the callback's parameter, so `x` keeps a variable, and the
			// arm the call selects constrains it.
			name: "OverloadArmsDisagree",
			src: box + "declare fn on(f: fn (x: &Box) -> undefined) -> undefined\n" +
				"declare fn on(f: fn (x: string) -> undefined) -> number\n" +
				"fn g() -> undefined { on(fn (x) { return undefined }) }",
			xType: "<T0> T0",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			xType, errs := inferLocal(t, tt.src, "x")
			require.Equal(t, tt.xType, xType)
			require.Equal(t, tt.expected, messagesWithSpan(t, errs))
		})
	}
}

// TestClosureCallsAMutSelfMethodThroughItsContext covers a closure parameter typed by the call
// it is passed to, through which the body calls a `&mut self` method. A `&mut` context lets the
// call run, and a `&` context rejects it.
func TestClosureCallsAMutSelfMethodThroughItsContext(t *testing.T) {
	const counter = "class Counter {\n\tcount: number,\n\tinc(&mut self) -> number { return self.count },\n}\n"
	tests := []struct {
		name     string
		src      string
		expected []string
	}{
		{
			name: "MutableBorrow",
			src: counter + "declare fn withCounter(f: fn (c: &mut Counter) -> number) -> number\n" +
				"val r = withCounter(fn (c) { return c.inc() })",
		},
		{
			name: "ImmutableBorrow",
			src: counter + "declare fn withCounter(f: fn (c: &Counter) -> number) -> number\n" +
				"val r = withCounter(fn (c) { return c.inc() })",
			expected: []string{"6:37-6:42: cannot constrain immutable Counter <: mutable Counter"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Equal(t, tt.expected, messagesWithSpan(t, errs))
		})
	}
}

// TestClosureParamWithoutAContext covers assignments and calls that give a closure parameter no
// type before the body is checked. The parameter reports as untyped unless checking the closure
// against the target types it afterwards.
func TestClosureParamWithoutAContext(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		expected []string
	}{
		{
			// The target's receiver is itself a member read, which is not read without
			// inferring it. Checking the closure against the field's type still types `x`.
			name: "NestedFieldTarget",
			src: "fn g(o: &mut {i: {f: fn (x: number) -> number}}) -> undefined {\n" +
				"\to.i.f = fn (x) { return x }\n}",
		},
		{
			name: "UndeclaredField",
			src: "fn g(o: &mut {f: fn (x: number) -> number}) -> undefined {\n" +
				"\to.h = fn (x) { return x }\n}",
			expected: []string{
				"2:8-2:27: object is missing property: h",
				"2:12-2:13: parameter 'x' has no type, and nothing the closure flows into supplies one; write its type",
			},
		},
		{
			// A generic function binding has no single type to read the parameter's off.
			name: "PolymorphicBinding",
			src: "fn id<T>(x: T) -> T { return x }\n" +
				"fn g() -> undefined {\n\tid = fn (x) { return x }\n}",
			expected: []string{
				"3:2-3:26: Cannot assign to immutable binding: id",
				"3:11-3:12: parameter 'x' has no type, and nothing the closure flows into supplies one; write its type",
			},
		},
		{
			// A generic `F` gives the closure no parameter types, so the `open` parameter keeps its
			// variable and reports.
			name: "OpenParamInAGenericContext",
			src:  "fn apply<F>(f: F) -> F { return f }\nval g = apply(fn (open x) { return x.a })",
			expected: []string{
				"2:24-2:25: parameter 'x' has no type, and nothing the closure flows into supplies one; write its type",
			},
		},
		{
			name: "UnknownTarget",
			src:  "fn g() -> undefined {\n\tnope = fn (x) { return x }\n}",
			expected: []string{
				"2:2-2:6: Unknown identifier: nope",
				"2:13-2:14: parameter 'x' has no type, and nothing the closure flows into supplies one; write its type",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Equal(t, tt.expected, messagesWithSpan(t, errs))
		})
	}
}
