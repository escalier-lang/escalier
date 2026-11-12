package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestReturnLifetimeElision covers the lifetime a written return type takes when it leaves
// one out. A returned borrow takes the one input lifetime, and a returned function type is
// tied to every input. Each case's source declares `type Flat = {x: number}` ahead of it,
// and binding names the function whose rendering expected holds, or "" for none.
func TestReturnLifetimeElision(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		binding  string
		expected string
		errs     []string
	}{
		{
			name: "ReturnedBorrowOfTwoInputsReports",
			src:  `fn pick(a: &Flat, b: &Flat) -> &Flat { return a }`,
			errs: []string{
				"2:32-2:37: the return type borrows from 'a' or 'b', and elision cannot tell which; write a lifetime, " +
					"as in `fn pick<'x>(a: &'x Flat, b: &Flat) -> &'x Flat`",
			},
		},
		{
			name: "ReturnedBorrowOfThreeInputsReports",
			src:  `fn pick(a: &Flat, b: &Flat, c: &Flat) -> &Flat { return a }`,
			errs: []string{
				"2:42-2:47: the return type borrows from 'a', 'b' or 'c', and elision cannot tell which; write a lifetime, " +
					"as in `fn pick<'x>(a: &'x Flat, b: &Flat, c: &Flat) -> &'x Flat`",
			},
		},
		{
			name: "OneParamHoldingTwoLifetimesReports",
			src:  `fn inner(p: &{r: &Flat}) -> &Flat { return p.r }`,
			errs: []string{
				"2:29-2:34: the return type borrows, and the signature's inputs hold several lifetimes, " +
					"so elision cannot tell which it takes; write a lifetime",
			},
		},
		{
			name:     "ReturnedBorrowOfTheOneInput",
			src:      `fn first(a: &Flat) -> &Flat { return a }`,
			binding:  "first",
			expected: "fn <'a>(a: &'a Flat) -> &'a Flat",
		},
		{
			name:     "StaticInputIsNotCounted",
			src:      `fn first(a: &Flat, s: &'static Flat) -> &Flat { return a }`,
			binding:  "first",
			expected: "fn <'a>(a: &'a Flat, s: &'static Flat) -> &'a Flat",
		},
		{
			// The elided lifetime ties the result to a, though the body returns a borrow of a
			// local, which L1 accepts.
			name:     "BodyBorrowsLessThanElisionTies",
			src:      "fn fresh(a: &Flat) -> &Flat {\n\tval p = {x: a.x}\n\treturn &p\n}",
			binding:  "fresh",
			expected: "fn <'a>(a: &'a Flat) -> &'a Flat",
		},
		{
			name: "NoInputLifetime",
			src:  "fn make() -> &Flat {\n\tval p = {x: 1}\n\treturn &p\n}",
		},
		{
			name:     "ReturnedFunctionTiedToEveryInput",
			src:      `fn adder(a: &Flat, b: &Flat) -> fn () -> number { return fn () { return a.x + b.x } }`,
			binding:  "adder",
			expected: "fn <'a>(a: &'a Flat, b: &'a Flat) -> 'a fn () -> number",
		},
		{
			name:     "ReturnedFunctionReadingOneInput",
			src:      `fn only(a: &Flat, b: &Flat) -> fn () -> number { return fn () { return a.x } }`,
			binding:  "only",
			expected: "fn <'a>(a: &'a Flat, b: &'a Flat) -> 'a fn () -> number",
		},
		{
			name:     "ReturnedFunctionReadingNoInput",
			src:      `fn none(a: &Flat) -> fn () -> number { return fn () { return 1 } }`,
			binding:  "none",
			expected: "fn <'a>(a: &'a Flat) -> 'a fn () -> number",
		},
		{
			name:     "NamedInputOutlivesTheReturnedFunction",
			src:      `fn named<'p>(a: &'p Flat, b: &Flat) -> fn () -> number { return fn () { return a.x + b.x } }`,
			binding:  "named",
			expected: "fn <'a: 'b, 'b>(a: &'a Flat, b: &'b Flat) -> 'b fn () -> number",
		},
		{
			name:     "ReturnTypeHoldingBothKinds",
			src:      `fn both(a: &Flat) -> {f: fn () -> number, r: &Flat} { return {f: fn () { return a.x }, r: a} }`,
			binding:  "both",
			expected: "fn <'a>(a: &'a Flat) -> {f: 'a fn () -> number, r: &'a Flat}",
		},
		{
			// 's' holds only 'static, which elision never gives a returned borrow.
			name: "StaticOnlyParamIsNotAnOwner",
			src:  `fn pick(a: &Flat, s: &'static Flat, b: &Flat) -> &Flat { return a }`,
			errs: []string{
				"2:50-2:55: the return type borrows from 'a' or 'b', and elision cannot tell which; write a lifetime, " +
					"as in `fn pick<'x>(a: &'x Flat, s: &'static Flat, b: &Flat) -> &'x Flat`",
			},
		},
		{
			name: "DeclaredFunctionReports",
			src:  `declare fn pick(a: &Flat, b: &Flat) -> &Flat`,
			errs: []string{
				"2:40-2:45: the return type borrows from 'a' or 'b', and elision cannot tell which; write a lifetime, " +
					"as in `fn pick<'x>(a: &'x Flat, b: &Flat) -> &'x Flat`",
			},
		},
		{
			// A bound the signature writes still has to be established by the body, as D4
			// decides.
			name: "WrittenBoundTheBodyDoesNotEstablish",
			src:  `fn bad<'a, 'b: 'a>(p: &'a Flat, q: &'b Flat) -> &'a Flat { return p }`,
			errs: []string{"2:12-2:18: declared lifetime bound 'b: 'a is not satisfied; the body does not make 'b outlive 'a"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, "type Flat = {x: number}\n"+tt.src)
			require.Equal(t, tt.errs, messagesWithSpan(t, errs))
			if tt.binding != "" {
				require.Equal(t, tt.expected, values[tt.binding])
			}
		})
	}
}

// TestMethodReturnLifetimeElision covers a method's elided return lifetime, which takes the
// borrowed receiver's lifetime whatever the other parameters hold.
func TestMethodReturnLifetimeElision(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		expected string
	}{
		{
			name: "ReturnedBorrowTakesTheReceiverLifetime",
			src: `class Box {
				inner: {x: number},
				peek(&self, other: &{x: number}) -> &{x: number} { return &self.inner },
			}`,
			expected: "{inner: {x: number}, peek<'a>(&'a self, other: &{x: number}) -> &'a {x: number}}",
		},
		{
			name: "ReturnedBorrowOfAMutableReceiver",
			src: `class Box {
				inner: {x: number},
				grab(&mut self, a: &{x: number}, b: &{x: number}) -> &mut {x: number} { return &mut self.inner },
			}`,
			expected: "{inner: {x: number}, grab<'a>(&'a mut self, a: &{x: number}, b: &{x: number}) -> &'a mut {x: number}}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := InferModuleWithSource(parseModule(t, tt.src), testStdlibSource())
			require.Empty(t, errorMessagesOf(res.Errors))
			def, ok := res.checker.ctx.classDef("Box")
			require.True(t, ok)
			require.Equal(t, tt.expected, printClassBody(def))
		})
	}
}

// TestMethodAmbiguousReturnLifetime asserts that the signature suggested for a method that
// consumes its receiver keeps the receiver.
func TestMethodAmbiguousReturnLifetime(t *testing.T) {
	src := `class Box {
		inner: {x: number},
		n(self, a: &{x: number}, b: &{x: number}) -> &{x: number} { return a },
	}`
	res := InferModuleWithSource(parseModule(t, src), testStdlibSource())
	require.Equal(t, []string{
		"the return type borrows from 'a' or 'b', and elision cannot tell which; write a lifetime, " +
			"as in `n<'x>(self, a: &'x { x: number }, b: &{ x: number }) -> &'x { x: number }`",
	}, errorMessagesOf(res.Errors))
}
