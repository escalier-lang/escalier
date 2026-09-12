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
	for _, name := range []string{"Generator", "AsyncGenerator"} {
		t.Run(name, func(t *testing.T) {
			b, ok := s.GetType(name)
			require.True(t, ok, "stdlib type %q should resolve to a placeholder", name)
			require.IsType(t, &soltype.UnknownType{}, b.Type)
		})
	}
}

// The names no rule resolves itself get no placeholder. `Promise` is read through the
// class the prelude declares, and `Iterable` and `AsyncIterable` through the protocol
// member on the operand, so a stub for any of the three would stand between a missing
// declaration and the report naming it.
func TestPreludeSeedsNoStubForAResolvedName(t *testing.T) {
	s := NewPrelude()
	for _, name := range []string{"Promise", "Iterable", "AsyncIterable"} {
		t.Run(name, func(t *testing.T) {
			_, ok := s.GetType(name)
			require.False(t, ok, "stdlib type %q should have no placeholder", name)
		})
	}
}

// Both spellings of a `Promise` reference report the same missing declaration when the
// run's tree supplies no prelude. The stub used to answer the argument-less one with
// `unknown`, which reached the async return check as a type that is not a promise and
// reported that instead of the name it could not find.
func TestAPromiseReferenceWithNoPreludeIsAnUnboundName(t *testing.T) {
	for _, src := range []string{
		`declare fn f() -> Promise`,
		`declare fn f() -> Promise<number>`,
	} {
		t.Run(src, func(t *testing.T) {
			res := inferAgainstExactStdlib(t, src, map[string]string{})
			// The run also reports the classes its tree does not declare, which is what
			// leaves the reference itself an ordinary unbound name.
			require.Contains(t, errorMessagesOf(res.Errors), "cannot find type `Promise`")
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
			want: `IteratorReturnResult<string> | IteratorYieldResult<number>`,
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

// A program numbers its own unique symbols from zero however many the prelude declares,
// the same way it numbers its type variables from `t0`. `SymbolConstructor` alone declares
// seventeen, and a diagnostic about code that never names one should not start at
// `unique symbol#17`.
func TestPreludeSymbolsDoNotAdvanceTheProgramsNumbering(t *testing.T) {
	const src = `
		declare class C { readonly a: unique symbol, readonly b: unique symbol }
		declare fn takeA(s: C["a"]) -> number
		declare val c: C
		val n = takeA(c.b)
	`
	tests := []struct {
		name  string
		files map[string]string
	}{
		{
			// The shape the committed tree writes, so the fixture cannot drift from it.
			name: "APreludeDeclaringSymbolsOfItsOwn",
			files: map[string]string{"std/prelude.esc": `
				export declare interface SymbolConstructor {
					readonly iterator: unique symbol,
					readonly asyncIterator: unique symbol,
				}
				export declare var Symbol: SymbolConstructor
			`},
		},
		{
			name:  "APreludeDeclaringNone",
			files: map[string]string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := inferAgainstStdlib(t, src, tt.files)
			require.Equal(t,
				[]string{"cannot constrain unique symbol#1 <: unique symbol#0"},
				errorMessagesOf(res.Errors))
		})
	}
}
