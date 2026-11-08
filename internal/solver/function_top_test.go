package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// `function` is the supertype of every value that can be called. Each case binds an
// annotated value and checks that it fills a `function` slot.
func TestEveryCallableFillsFunction(t *testing.T) {
	tests := map[string]struct {
		decl string
	}{
		"AFunctionType":         {decl: `declare val f: fn (x: number) -> number`},
		"ANullaryFunction":      {decl: `declare val f: fn () -> undefined`},
		"AGenericFunction":      {decl: `declare val f: fn <T>(x: T) -> T`},
		"AThrowingFunction":     {decl: `declare val f: fn (x: string) -> number throws string`},
		"AnInexactFunction":     {decl: `declare val f: fn (x: number, ...) -> number`},
		"ACallableObject":       {decl: `declare val f: {(n: number) -> string, tag: string}`},
		"AConstructorSignature": {decl: `declare val f: {new (n: number) -> {n: number}}`},
		"AClassValue":           {decl: "class Box<T> { value: T }\nval f = Box"},
		"AFunctionExpression":   {decl: `val f = fn (x: number) { return x }`},
		"FunctionItself":        {decl: `declare val f: function`},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tc.decl+`
				val g: function = f
			`)
			require.Empty(t, errorMessagesOf(errs))
		})
	}
}

// A value that cannot be called is not a `function`.
func TestANonCallableDoesNotFillFunction(t *testing.T) {
	tests := map[string]struct {
		decl     string
		expected string
	}{
		"ANumber":        {decl: `declare val f: number`, expected: "cannot constrain number <: function"},
		"AString":        {decl: `declare val f: "hi"`, expected: `cannot constrain "hi" <: function`},
		"AnObject":       {decl: `declare val f: {x: number}`, expected: "cannot constrain object <: function"},
		"AClassInstance": {decl: "class Box { value: number }\ndeclare val f: Box", expected: "cannot constrain Box <: function"},
		"Null":           {decl: `declare val f: null`, expected: "cannot constrain null <: function"},
		"Unknown":        {decl: `declare val f: unknown`, expected: "cannot constrain unknown <: function"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tc.decl+`
				val g: function = f
			`)
			require.Equal(t, []string{tc.expected}, errorMessagesOf(errs))
		})
	}
}

// `function` is not a subtype of any particular function type, since a value of it may take
// any parameters. The raw message describes any function type by its kind, so both sides read
// `function`.
func TestFunctionDoesNotFillAFunctionType(t *testing.T) {
	_, _, errs := inferSource(t, `
		declare val f: function
		val g: fn (x: number) -> number = f
	`)
	require.Equal(t, []string{"cannot constrain function <: function"}, errorMessagesOf(errs))
}

// A type parameter bounded by `function` accepts every callable value and rejects anything
// else.
func TestAFunctionBound(t *testing.T) {
	const decl = `
		class Box<T> { value: T }
		declare fn describe<T: function>(f: T) -> string
	`
	t.Run("ItRenders", func(t *testing.T) {
		values, _, errs := inferSource(t, decl)
		require.Empty(t, errorMessagesOf(errs))
		require.Equal(t, "fn <T: function>(f: T) -> string", values["describe"])
	})

	t.Run("CallableArgumentsAreAccepted", func(t *testing.T) {
		values, _, errs := inferSource(t, decl+`
			declare val callable: {(n: number) -> string}
			val a = describe(fn (x: number) { return x })
			val b = describe(Box)
			val c = describe(callable)
		`)
		require.Empty(t, errorMessagesOf(errs))
		require.Equal(t, "string", values["a"])
		require.Equal(t, "string", values["b"])
		require.Equal(t, "string", values["c"])
	})

	t.Run("ANonCallableArgumentIsRejected", func(t *testing.T) {
		_, _, errs := inferSource(t, decl+`
			val a = describe(5)
		`)
		require.Equal(t, []string{"cannot constrain 5 <: function"}, errorMessagesOf(errs))
	})
}

// An array of `function` holds functions of every shape side by side.
func TestAnArrayOfFunction(t *testing.T) {
	values, _, errs := inferSource(t, `
		class Box<T> { value: T }
		declare val callable: {(n: number) -> string}
		val fs: Array<function> = [fn (x: number) { return x }, fn () {}, Box, callable]
		val first = fs[0]
	`)
	require.Empty(t, errorMessagesOf(errs))
	require.Equal(t, "Array<function>", values["fs"])
	require.Equal(t, "function", values["first"])
}

// Calling a `function` is rejected, since nothing is known about its parameters. A type
// parameter bounded by `function` is rejected the same way.
func TestCallingFunctionIsRejected(t *testing.T) {
	const expected = "cannot call a value of type function, since its parameters are unknown; " +
		"narrow it to a function type first"
	tests := map[string]string{
		"AValue":         "declare val f: function\nval r = f(1)",
		"AnAlias":        "type F = function\ndeclare val f: F\nval r = f(1)",
		"ABoundedParam":  "fn call<T: function>(f: T) { return f(1) }",
		"ANullaryCall":   "declare val f: function\nval r = f()",
		"AnArrayElement": "declare val fs: Array<function>\nval r = fs[0]()",
	}
	for name, src := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, src)
			require.Equal(t, []string{expected}, errorMessagesOf(errs))
		})
	}
}

// An annotation pattern narrows to `function`, and the arms after it see what is left.
func TestMatchNarrowsToFunction(t *testing.T) {
	values, _, errs := inferSource(t, `
		declare val u: number | fn (x: number) -> number
		val r = match u {
			f: function => "callable",
			n: number => n,
		}
	`)
	require.Empty(t, errorMessagesOf(errs))
	require.Equal(t, `"callable" | number`, values["r"])
}

// A borrow of a value read out of a parameter fills a `function` slot by copying, as it fills
// a function type's.
func TestABorrowCopiesIntoFunction(t *testing.T) {
	_, _, errs := inferSource(t, `
		declare fn pick<T>(p: &{f: T}) -> &T
		declare val o: {f: fn () -> number}
		val g: function = pick(&o)
	`)
	require.Empty(t, errorMessagesOf(errs))
}
