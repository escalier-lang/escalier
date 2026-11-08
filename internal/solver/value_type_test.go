package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// ValueType is the union of the types whose values are copied rather than shared. Every
// such type fills it, a literal included, and an object does not.
func TestValueTypeAdmitsTheValueTypes(t *testing.T) {
	tests := map[string]struct {
		decl     string
		expected []string
	}{
		"ANumber":    {decl: `declare val v: number`},
		"ALiteral":   {decl: `declare val v: 5`},
		"AString":    {decl: `declare val v: string`},
		"ABoolean":   {decl: `declare val v: boolean`},
		"ABigint":    {decl: `declare val v: bigint`},
		"ASymbol":    {decl: `declare val v: symbol`},
		"Null":       {decl: `declare val v: null`},
		"Undefined":  {decl: `declare val v: undefined`},
		"AFunction":  {decl: `declare val v: fn (x: number) -> number`},
		"AClass":     {decl: "class Box { value: number }\nval v = Box"},
		"AUnion":     {decl: `declare val v: number | string | undefined`},
		"AnObject":   {decl: `declare val v: {x: number}`, expected: []string{"cannot constrain object <: ValueType"}},
		"ATuple":     {decl: `declare val v: [number, string]`, expected: []string{"cannot constrain tuple <: ValueType"}},
		"AnInstance": {decl: "class Box { value: number }\ndeclare val v: Box", expected: []string{"cannot constrain Box <: ValueType"}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tc.decl+`
				val w: ValueType = v
			`)
			if tc.expected == nil {
				require.Empty(t, errorMessagesOf(errs))
				return
			}
			require.Equal(t, tc.expected, errorMessagesOf(errs))
		})
	}
}

// A borrow of a type parameter bounded by ValueType fills an owned slot of that parameter by
// copying, so each body below infers without an error.
func TestAValueTypeBorrowFillsAnOwnedSlot(t *testing.T) {
	tests := map[string]struct {
		src      string
		name     string
		expected string
	}{
		"ABorrowedParameter": {
			src:      `fn steal<T: ValueType>(p: &T) -> T { return p }`,
			name:     "steal",
			expected: "fn <T: ValueType>(p: &T) -> T",
		},
		"AnElementOfABorrowedArray": {
			src:      `fn first<T: ValueType>(xs: &Array<T>) -> T | undefined { return xs[0] }`,
			name:     "first",
			expected: "fn <T: ValueType>(xs: &Array<T>) -> T | undefined",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			values, _, errs := inferSource(t, tc.src)
			require.Empty(t, errorMessagesOf(errs))
			require.Equal(t, tc.expected, values[tc.name])
		})
	}
}

// A call checks its argument against the ValueType bound, and the failure names the bound.
func TestAValueTypeBoundChecksTheArgument(t *testing.T) {
	const decl = `declare fn id<T: ValueType>(v: T) -> T`
	t.Run("AValueIsAccepted", func(t *testing.T) {
		values, _, errs := inferSource(t, decl+`
			val r = id(5)
		`)
		require.Empty(t, errorMessagesOf(errs))
		require.Equal(t, "5", values["r"])
	})

	t.Run("AnObjectIsRejected", func(t *testing.T) {
		_, _, errs := inferSource(t, decl+`
			val r = id({x: 1})
		`)
		require.Equal(t, []string{"cannot constrain object <: ValueType"}, errorMessagesOf(errs))
	})

	t.Run("ABorrowedObjectIsRejected", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			declare fn steal<T: ValueType>(p: &T) -> T
			val r = steal(&{x: 1})
		`)
		require.Equal(t, []string{"cannot constrain object <: ValueType"}, errorMessagesOf(errs))
	})
}

// A type parameter bounded by a union is a subtype of that union, although no single member
// admits the parameter on its own.
func TestABoundedParameterFillsItsUnionBound(t *testing.T) {
	tests := map[string]struct {
		src      string
		expected string
	}{
		"TheSameUnion": {
			src:      `fn f<T: number | string>(x: T) -> number | string { return x }`,
			expected: "fn <T: number | string>(x: T) -> number | string",
		},
		"AWiderUnion": {
			src:      `fn f<T: number | string>(x: T) -> number | string | boolean { return x }`,
			expected: "fn <T: number | string>(x: T) -> number | string | boolean",
		},
		"AnAliasedUnion": {
			src:      "type V = number | string\nfn f<T: V>(x: T) -> V { return x }",
			expected: "fn <T: V>(x: T) -> V",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			values, _, errs := inferSource(t, tc.src)
			require.Empty(t, errorMessagesOf(errs))
			require.Equal(t, tc.expected, values["f"])
		})
	}

	t.Run("ANarrowerUnionIsRejected", func(t *testing.T) {
		_, _, errs := inferSource(t, `fn f<T: number | string>(x: T) -> number | boolean { return x }`)
		require.Equal(t, []string{"cannot constrain T <: number | boolean"}, errorMessagesOf(errs))
	})
}

// A namespace that declares its own ValueType resolves its references to that declaration
// rather than to the built-in.
func TestANamespaceAliasShadowsValueType(t *testing.T) {
	_, _, errs := inferSource(t, `
		namespace wasm {
			interface G<T: ValueType> { value: T }
			type ValueType = "a" | "b"
		}
		declare val g: wasm.G<5>
	`)
	require.Equal(t, []string{`cannot constrain 5 <: "a" | "b"`}, errorMessagesOf(errs))
}
