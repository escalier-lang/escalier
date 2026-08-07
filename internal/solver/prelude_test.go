package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

func TestPreludeOperatorBindings(t *testing.T) {
	s := NewPrelude()
	tests := []struct {
		op   string
		want string
	}{
		{"+", "fn (a: number, b: number) -> number"},
		{"-", "fn (a: number, b: number) -> number"},
		{"*", "fn (a: number, b: number) -> number"},
		{"/", "fn (a: number, b: number) -> number"},
		{"<", "fn (a: number, b: number) -> boolean"},
		{">", "fn (a: number, b: number) -> boolean"},
		{"<=", "fn (a: number, b: number) -> boolean"},
		{">=", "fn (a: number, b: number) -> boolean"},
		{"==", "fn (a: unknown, b: unknown) -> boolean"},
		{"!=", "fn (a: unknown, b: unknown) -> boolean"},
		{"&&", "fn (a: boolean, b: boolean) -> boolean"},
		{"||", "fn (a: boolean, b: boolean) -> boolean"},
		{"!", "fn (a: boolean) -> boolean"},
		{"++", "fn (a: string, b: string) -> string"},
	}
	for _, tt := range tests {
		t.Run(tt.op, func(t *testing.T) {
			b, ok := s.GetValue(tt.op)
			require.True(t, ok, "operator %q should be bound in the prelude", tt.op)
			require.Equal(t, tt.want, renderBinding(b))
		})
	}
}

func TestPreludeStdlibTypePlaceholders(t *testing.T) {
	s := NewPrelude()
	for _, name := range []string{
		"Promise", "Iterable", "AsyncIterable",
		"Generator", "AsyncGenerator",
	} {
		t.Run(name, func(t *testing.T) {
			b, ok := s.GetType(name)
			require.True(t, ok, "stdlib type %q should resolve to a placeholder", name)
			require.IsType(t, &soltype.UnknownType{}, b.Type)
		})
	}
}

// The three iterator-result names bind to real aliases rather than opaque placeholders, so a
// reference resolves to a handle the alias registry expands. The prelude scope holds only the
// handle; newChecker seeds the bodies on each run's Context.
//
// Each case expands a reference at concrete arguments rather than printing the stored body,
// since that is the form every consumer sees. A body printed directly renders its parameters as
// the raw `t{ID}` debug form, the parameters being inference vars that expansion substitutes
// away.
func TestPreludeIteratorResultAliases(t *testing.T) {
	s := NewPrelude()
	ctx := &Context{}
	registerIteratorResultAliases(ctx)
	tests := []struct {
		name string
		args []soltype.Type
		want string
	}{
		{
			name: "IteratorYieldResult",
			args: []soltype.Type{&soltype.PrimType{Prim: soltype.NumPrim}},
			want: `{done?: false, value: number}`,
		},
		{
			name: "IteratorReturnResult",
			args: []soltype.Type{&soltype.PrimType{Prim: soltype.StrPrim}},
			want: `{done: true, value: string}`,
		},
		{
			name: "IteratorResult",
			args: []soltype.Type{
				&soltype.PrimType{Prim: soltype.NumPrim},
				&soltype.PrimType{Prim: soltype.StrPrim},
			},
			want: `IteratorYieldResult<number> | IteratorReturnResult<string>`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			b, ok := s.GetType(test.name)
			require.True(t, ok, "iterator-result type %q should resolve", test.name)
			ref, isAlias := b.Type.(*soltype.AliasType)
			require.True(t, isAlias, "%q should bind to an alias handle", test.name)
			require.Equal(t, test.name, ref.Name)
			require.Empty(t, ref.TypeArgs, "the prelude handle carries no arguments")

			def, registered := ctx.aliasDef(test.name)
			require.True(t, registered, "%q should be registered on the Context", test.name)
			require.Len(t, def.TypeParams, len(test.args))

			expanded := ctx.expandAlias(&soltype.AliasType{Name: test.name, TypeArgs: test.args})
			require.Equal(t, test.want, soltype.Print(expanded))
		})
	}
}

// A stdlib type name lives in the type sort, not the value sort: looking it up
// as a value must miss (so a value-position reference would error, not silently
// resolve to the placeholder).
func TestPreludeStdlibNamesAreTypesNotValues(t *testing.T) {
	s := NewPrelude()
	_, ok := s.GetValue("Promise")
	require.False(t, ok)
}
