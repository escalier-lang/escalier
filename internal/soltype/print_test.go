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

func optP(name string, t Type) *FuncParam {
	return &FuncParam{Pattern: &IdentPat{Name: name}, Type: t, Optional: true}
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
		{
			// A field name that isn't a valid identifier (e.g. from a string-literal
			// key) is quoted so the rendered record stays parseable.
			"non-identifier field name is quoted",
			&RecordType{Fields: []*RecordField{{Name: "a-b", Type: numP()}}},
			`{"a-b": number}`,
		},

		// Functions. A bare (exact) function renders with no trailing marker; an
		// inexact one renders a trailing `...`, and an optional param renders `x?: T`.
		{"nullary fn", &FuncType{Ret: numP(), Exact: true}, "fn () -> number"},
		{"unary fn", &FuncType{Params: []*FuncParam{identP("x", numP())}, Ret: strP(), Exact: true}, "fn (x: number) -> string"},
		{
			"multi-arg fn",
			&FuncType{Params: []*FuncParam{identP("a", numP()), identP("b", strP())}, Ret: boolP(), Exact: true},
			"fn (a: number, b: string) -> boolean",
		},
		{"inexact nullary fn", &FuncType{Ret: numP()}, "fn (...) -> number"},
		{
			"inexact fn with params",
			&FuncType{Params: []*FuncParam{identP("x", numP())}, Ret: strP()},
			"fn (x: number, ...) -> string",
		},
		{
			"optional param renders with ?",
			&FuncType{Params: []*FuncParam{identP("a", numP()), optP("b", strP())}, Ret: boolP(), Exact: true},
			"fn (a: number, b?: string) -> boolean",
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

func TestIsIdent(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"a", true},
		{"_x", true},
		{"a1", true},
		{"camelCase_9", true},
		{"café", true},  // unicode letter (continue)
		{"naïve", true}, // unicode letter (continue)
		{"数値", true},    // non-Latin letters
		{"Ωmega", true}, // unicode letter (leading)
		{"x٢", true},    // unicode digit (Arabic-Indic) after letter
		{"", false},
		{"1a", false},  // leading digit
		{"٢x", false},  // leading unicode digit
		{"a-b", false}, // hyphen
		{"a b", false}, // space
		{"a.b", false}, // dot
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isIdent(tt.name))
		})
	}
}

// TestPrintNestedPrecedence covers the shapes where operator precedence forces
// parenthesization, mirroring type_system/print_type.go's behavior. These use
// inline snapshots per CLAUDE.md since they're the richer rendered forms.
func TestPrintNestedPrecedence(t *testing.T) {
	// A function inside a union: precFunc < precUnion, so the function is parenthesized.
	t.Run("function in union", func(t *testing.T) {
		ty := &UnionType{Types: []Type{&FuncType{Ret: numP(), Exact: true}, strP()}}
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
			&FuncType{Params: []*FuncParam{identP("x", numP())}, Ret: strP(), Exact: true},
			boolP(),
		}}
		snaps.MatchInlineSnapshot(t, Print(ty), snaps.Inline(`[fn (x: number) -> string, boolean]`))
	})

	// A record is brace-delimited (an atom), so a record nested in a union needs
	// no parens, and a function as a field value is delimited by the field's `:`.
	t.Run("record in union", func(t *testing.T) {
		ty := &UnionType{Types: []Type{
			&RecordType{Fields: []*RecordField{{Name: "f", Type: &FuncType{Ret: numP(), Exact: true}}}},
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
			Exact:  true,
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
		Ret:   boolP(),
		Exact: true,
	}
	require.Equal(t, "fn (arg0: number, arg1: string) -> boolean", Print(fn))
}

// TestPrintScheme covers the M3 quantifier-prefix rendering: a generalized type's
// free variables are collected into a <T0, T1, …> prefix (named by first
// appearance in print order) and rendered under those names, while a variable-free
// type renders exactly as Print would.
func TestPrintScheme(t *testing.T) {
	t.Run("no free vars renders as Print", func(t *testing.T) {
		ty := &FuncType{Params: []*FuncParam{identP("x", numP())}, Ret: strP(), Exact: true}
		require.Equal(t, "fn (x: number) -> string", PrintAsScheme(ty))
	})

	t.Run("identity gets one type parameter", func(t *testing.T) {
		a := &TypeVarType{ID: 7, Level: 1}
		ty := &FuncType{Params: []*FuncParam{identP("x", a)}, Ret: a, Exact: true}
		require.Equal(t, "fn <T0>(x: T0) -> T0", PrintAsScheme(ty))
	})

	t.Run("distinct vars are named by first appearance", func(t *testing.T) {
		a := &TypeVarType{ID: 1, Level: 1}
		b := &TypeVarType{ID: 2, Level: 1}
		// fn (x: a, y: b) -> [b, a]: a appears first (param x), then b (param y).
		ty := &FuncType{
			Params: []*FuncParam{identP("x", a), identP("y", b)},
			Ret:    &TupleType{Elems: []Type{b, a}},
			Exact:  true,
		}
		require.Equal(t, "fn <T0, T1>(x: T0, y: T1) -> [T1, T0]", PrintAsScheme(ty))
	})

	t.Run("a free var keeps one name across positions", func(t *testing.T) {
		a := &TypeVarType{ID: 3, Level: 1}
		ty := &TupleType{Elems: []Type{a, a}}
		require.Equal(t, "<T0> [T0, T0]", PrintAsScheme(ty))
	})
}

// PrintAsSchemeWith names ONLY the variables the predicate accepts as quantified
// type parameters; a variable it rejects (one coalescing should have inlined)
// renders as the raw t{ID} debug anchor instead of being masked as a <Tn>
// parameter. This preserves the leak signal that plain PrintAsScheme (which names
// every free var) would hide.
func TestPrintSchemeParamsLeakAnchor(t *testing.T) {
	param := &TypeVarType{ID: 1, Level: 2}   // a genuine type parameter (Level > 1)
	leaked := &TypeVarType{ID: 99, Level: 0} // a var that should have been inlined
	ty := &FuncType{
		Params: []*FuncParam{identP("x", param)},
		Ret:    &TupleType{Elems: []Type{param, leaked}},
		Exact:  true,
	}
	got := PrintAsSchemeWith(ty, func(v *TypeVarType) bool { return v.Level > 1 })
	require.Equal(t, "fn <T0>(x: T0) -> [T0, t99]", got)
}
