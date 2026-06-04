package soltype

import (
	"testing"

	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/require"
)

func numP() *PrimType  { return &PrimType{Prim: NumPrim} }
func strP() *PrimType  { return &PrimType{Prim: StrPrim} }
func boolP() *PrimType { return &PrimType{Prim: BoolPrim} }

func identP(name string, t Type) *FuncParam {
	return &FuncParam{Pattern: &IdentPat{Name: name}, Type: t}
}

// TestPrintRoundTrips covers the short, stable round-trips for the M1 coalesced
// type set: primitives, literals, the lattice bounds, tuples, multi-arg
// functions, and multi-element unions/intersections. Per CLAUDE.md these are the
// short stable strings, so they use require.Equal; the richer nested shapes
// (which exercise precedence parenthesization) use MatchInlineSnapshot below.
func TestPrintRoundTrips(t *testing.T) {
	tests := []struct {
		name string
		in   Type
		want string
	}{
		// Primitives.
		{"number", numP(), "number"},
		{"string", strP(), "string"},
		{"boolean", boolP(), "boolean"},

		// Literals.
		{"num literal", &LitType{Lit: &NumLit{Value: 5}}, "5"},
		{"str literal", &LitType{Lit: &StrLit{Value: "hello"}}, `"hello"`},
		{"bool literal", &LitType{Lit: &BoolLit{Value: true}}, "true"},

		// Lattice bounds and void.
		{"never", &NeverType{}, "never"},
		{"unknown", &UnknownType{}, "unknown"},
		{"void", &Void{}, "void"},

		// Tuples.
		{"empty tuple", &TupleType{}, "[]"},
		{"pair tuple", &TupleType{Elems: []Type{numP(), strP()}}, "[number, string]"},

		// Records.
		{"empty record", &RecordType{}, "{}"},
		{
			"two-field record",
			&RecordType{Fields: []*RecordField{{Name: "a", Type: numP()}, {Name: "b", Type: strP()}}},
			"{a: number, b: string}",
		},

		// Functions.
		{"nullary fn", &FuncType{Ret: numP()}, "fn () -> number"},
		{"unary fn", &FuncType{Params: []*FuncParam{identP("x", numP())}, Ret: strP()}, "fn (x: number) -> string"},
		{
			"multi-arg fn",
			&FuncType{Params: []*FuncParam{identP("a", numP()), identP("b", strP())}, Ret: boolP()},
			"fn (a: number, b: string) -> boolean",
		},

		// Unions and intersections.
		{"union pair", &UnionType{Types: []Type{numP(), strP()}}, "number | string"},
		{"union triple", &UnionType{Types: []Type{numP(), strP(), boolP()}}, "number | string | boolean"},
		{"intersection pair", &IntersectionType{Types: []Type{numP(), strP()}}, "number & string"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, Print(tt.in))
		})
	}
}

// TestPrintNestedPrecedence covers the shapes where operator precedence forces
// parenthesization, mirroring type_system/print_type.go's behavior. These use
// inline snapshots per CLAUDE.md since they're the richer rendered forms.
func TestPrintNestedPrecedence(t *testing.T) {
	// A function inside a union: precFunc < precUnion, so the function is parenthesized.
	t.Run("function in union", func(t *testing.T) {
		ty := &UnionType{Types: []Type{&FuncType{Ret: numP()}, strP()}}
		snaps.MatchInlineSnapshot(t, Print(ty), snaps.Inline(`(fn () -> number) | string`))
	})

	// A union inside an intersection: precUnion < precIntersection, so the union
	// is parenthesized.
	t.Run("union in intersection", func(t *testing.T) {
		ty := &IntersectionType{Types: []Type{&UnionType{Types: []Type{numP(), strP()}}, boolP()}}
		snaps.MatchInlineSnapshot(t, Print(ty), snaps.Inline(`(number | string) & boolean`))
	})

	// A function inside a tuple is delimited by brackets, so it needs no parens.
	t.Run("function in tuple", func(t *testing.T) {
		ty := &TupleType{Elems: []Type{
			&FuncType{Params: []*FuncParam{identP("x", numP())}, Ret: strP()},
			boolP(),
		}}
		snaps.MatchInlineSnapshot(t, Print(ty), snaps.Inline(`[fn (x: number) -> string, boolean]`))
	})

	// A record is brace-delimited (an atom), so a record nested in a union needs
	// no parens, and a function as a field value is delimited by the field's `:`.
	t.Run("record in union", func(t *testing.T) {
		ty := &UnionType{Types: []Type{
			&RecordType{Fields: []*RecordField{{Name: "f", Type: &FuncType{Ret: numP()}}}},
			strP(),
		}}
		snaps.MatchInlineSnapshot(t, Print(ty), snaps.Inline(`{f: fn () -> number} | string`))
	})
}

// TestPrintRawTypeVar verifies that Print tolerates a raw, un-coalesced
// TypeVarType (rendering it as `t{ID}`) instead of panicking — the M2 walk
// records var-carrying types in Info and only coalesces at binding boundaries,
// so a consumer can hand Print a live variable, standalone or nested in a
// function. See print.go's printType TypeVarType arm.
func TestPrintRawTypeVar(t *testing.T) {
	t.Run("bare variable", func(t *testing.T) {
		require.Equal(t, "t7", Print(&TypeVarType{ID: 7}))
	})

	t.Run("variable nested in a function", func(t *testing.T) {
		fn := &FuncType{
			Params: []*FuncParam{identP("x", &TypeVarType{ID: 0})},
			Ret:    &TypeVarType{ID: 0},
		}
		require.Equal(t, "fn (x: t0) -> t0", Print(fn))
	})
}

// TestPrintUnnamedParamFallback verifies that a parameter with no IdentPat
// pattern falls back to a positional name (arg0, arg1, ...), numbered by param
// index. This path isn't reachable in M1 (params are always IdentPat), but the
// fallback exists for nil/unknown patterns, so it's covered directly here.
func TestPrintUnnamedParamFallback(t *testing.T) {
	fn := &FuncType{
		Params: []*FuncParam{
			{Pattern: nil, Type: numP()},
			{Pattern: nil, Type: strP()},
		},
		Ret: boolP(),
	}
	require.Equal(t, "fn (arg0: number, arg1: string) -> boolean", Print(fn))
}
