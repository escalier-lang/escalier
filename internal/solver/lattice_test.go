package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// TestNewUnionNormalization is the table-driven sweep over newUnion's
// Context-free steps. Each row exercises one of flatten, lattice identities,
// ErrorType elision, dedup, canonical order, or collapse.
func TestNewUnionNormalization(t *testing.T) {
	tests := []struct {
		name    string
		parts   []soltype.Type
		inexact bool
		want    soltype.Type
	}{
		{
			name:  "nested union splices in, canonical order",
			parts: []soltype.Type{parseType(t, "number | string"), parseType(t, "boolean")},
			// PrimType members sort by the Prim enum order NumPrim, StrPrim,
			// BoolPrim.
			want: parseType(t, "number | string | boolean"),
		},
		{
			name: "inexact nested member makes outer inexact",
			// A nested inexact UnionType carries the inexact flag out to the
			// outer mint. parseType cannot author an inexact union literal
			// today (PR4 lands the surface marker), so the nested member is
			// built from a parsed exact union with Inexact flipped.
			parts:   []soltype.Type{&soltype.UnionType{Types: []soltype.Type{num()}, Inexact: true}, parseType(t, "string")},
			inexact: false,
			want:    &soltype.UnionType{Types: parseTypes(t, "number", "string"), Inexact: true},
		},
		{
			name: "doubly-nested union splices fully",
			// `((number | string) | boolean)` collapses to
			// `number | string | boolean`. The recursive splice walks the
			// inner UnionType the outer member holds rather than stopping at
			// one level.
			parts: []soltype.Type{&soltype.UnionType{Types: []soltype.Type{
				parseType(t, "number | string"),
				parseType(t, "boolean"),
			}}},
			want: parseType(t, "number | string | boolean"),
		},
		{
			name: "inexact propagates from a deeply nested member",
			// `number | ((string | ...))` — the inexact tail lives two levels
			// down and still makes the outer union inexact.
			parts: []soltype.Type{
				parseType(t, "number"),
				&soltype.UnionType{Types: []soltype.Type{
					&soltype.UnionType{Types: parseTypes(t, "string"), Inexact: true},
				}},
			},
			want: &soltype.UnionType{Types: parseTypes(t, "number", "string"), Inexact: true},
		},
		{
			name:  "never drops from union",
			parts: parseTypes(t, "number", "never"),
			want:  parseType(t, "number"),
		},
		{
			name:  "all-never collapses to never",
			parts: parseTypes(t, "never", "never"),
			want:  parseType(t, "never"),
		},
		{
			name:  "error drops from union with other members",
			parts: []soltype.Type{parseType(t, "number"), &soltype.ErrorType{}},
			want:  parseType(t, "number"),
		},
		{
			name:  "error retained as sole member",
			parts: []soltype.Type{&soltype.ErrorType{}},
			want:  &soltype.ErrorType{},
		},
		{
			name:  "error retained when other members are all lattice identities",
			parts: []soltype.Type{&soltype.ErrorType{}, parseType(t, "never")},
			want:  &soltype.ErrorType{},
		},
		{
			name:  "structural dedup",
			parts: parseTypes(t, "number", "number", "string"),
			want:  parseType(t, "number | string"),
		},
		{
			name:  "empty union collapses to never",
			parts: nil,
			want:  parseType(t, "never"),
		},
		{
			name:  "single exact member collapses to that member",
			parts: parseTypes(t, "number"),
			want:  parseType(t, "number"),
		},
		{
			name:    "inexact single member keeps the wrapper",
			parts:   parseTypes(t, "number"),
			inexact: true,
			want:    &soltype.UnionType{Types: parseTypes(t, "number"), Inexact: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := newUnion(nil, tt.parts, tt.inexact)
			require.True(t, equalType(tt.want, got), "want %s, got %s", soltype.Print(tt.want), soltype.Print(got))
		})
	}
}

// TestNewIntersectionNormalization is the meet twin of TestNewUnionNormalization.
func TestNewIntersectionNormalization(t *testing.T) {
	tests := []struct {
		name  string
		parts []soltype.Type
		want  soltype.Type
	}{
		{
			name:  "nested intersection splices in, canonical order",
			parts: []soltype.Type{parseType(t, "number & {a: number}"), parseType(t, "{b: string}")},
			want:  parseType(t, "number & {a: number} & {b: string}"),
		},
		{
			name: "doubly-nested intersection splices fully",
			// `(({a} & {b}) & number)` collapses to `number & {a} & {b}`.
			// The recursive splice walks the inner IntersectionType the
			// outer member holds.
			parts: []soltype.Type{&soltype.IntersectionType{Types: []soltype.Type{
				parseType(t, "{a: number} & {b: string}"),
				parseType(t, "number"),
			}}},
			want: parseType(t, "number & {a: number} & {b: string}"),
		},
		{
			name:  "unknown drops from intersection",
			parts: parseTypes(t, "number", "unknown"),
			want:  parseType(t, "number"),
		},
		{
			name:  "all-unknown collapses to unknown",
			parts: parseTypes(t, "unknown", "unknown"),
			want:  parseType(t, "unknown"),
		},
		{
			name:  "error drops from intersection with other members",
			parts: []soltype.Type{parseType(t, "number"), &soltype.ErrorType{}},
			want:  parseType(t, "number"),
		},
		{
			name:  "error retained as sole member",
			parts: []soltype.Type{&soltype.ErrorType{}},
			want:  &soltype.ErrorType{},
		},
		{
			name:  "error retained when other members are all lattice identities",
			parts: []soltype.Type{&soltype.ErrorType{}, parseType(t, "unknown")},
			want:  &soltype.ErrorType{},
		},
		{
			name:  "structural dedup",
			parts: parseTypes(t, "number", "string", "number"),
			want:  parseType(t, "number & string"),
		},
		{
			name:  "empty intersection collapses to unknown",
			parts: nil,
			want:  parseType(t, "unknown"),
		},
		{
			name:  "single member collapses to that member",
			parts: parseTypes(t, "number"),
			want:  parseType(t, "number"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := newIntersection(nil, tt.parts)
			require.True(t, equalType(tt.want, got), "want %s, got %s", soltype.Print(tt.want), soltype.Print(got))
		})
	}
}

// TestNewUnionCanonicalOrder is the M6 PR1 gate against speculative-pinning
// drift. A union of a member list and of its shuffle must render identically
// and compare equalType-equal.
func TestNewUnionCanonicalOrder(t *testing.T) {
	a := newUnion(nil, parseTypes(t, "number", "string"), false)
	b := newUnion(nil, parseTypes(t, "string", "number"), false)
	require.True(t, equalType(a, b), "expected canonical order to equate both shuffles")
	require.Equal(t, soltype.Print(a), soltype.Print(b))
}

func TestNewIntersectionCanonicalOrder(t *testing.T) {
	a := newIntersection(nil, parseTypes(t, "number", "string"))
	b := newIntersection(nil, parseTypes(t, "string", "number"))
	require.True(t, equalType(a, b), "expected canonical order to equate both shuffles")
	require.Equal(t, soltype.Print(a), soltype.Print(b))
}

// TestNewUnionInexactPrintRoundTrip pins the printer's trailing `...` rendering
// for an inexact union, so the flag round-trips to surface syntax.
func TestNewUnionInexactPrintRoundTrip(t *testing.T) {
	u := newUnion(nil, parseTypes(t, "number", "string"), true)
	require.Equal(t, "number | string | ...", soltype.Print(u))
}

// TestNewUnionSubsumeWithContext covers the Context-gated subsumption step.
// A member that is a subtype of another member is dropped, so `number | 1`
// reduces to `number`.
func TestNewUnionSubsumeWithContext(t *testing.T) {
	c := &Context{}
	got := newUnion(c, parseTypes(t, "number", "1"), false)
	require.True(t, equalType(parseType(t, "number"), got), "got %s", soltype.Print(got))
}

// TestNewIntersectionSubsumeWithContext is the meet twin. An intersection
// member that is a supertype of another is dropped. Width subtyping on
// inexact objects makes the wider object a subtype of the narrower one, so
// `{x, ...} & {x, y, ...}` reduces to `{x, y, ...}`.
func TestNewIntersectionSubsumeWithContext(t *testing.T) {
	c := &Context{}
	got := newIntersection(c, parseTypes(t,
		"{x: number, ...}",
		"{x: number, y: string, ...}",
	))
	require.True(t, equalType(parseType(t, "{x: number, y: string, ...}"), got), "got %s", soltype.Print(got))
}

// TestSubsumeMutualPicksCanonicalSurvivor pins the M6 PR1 canonicalization
// fix. When two members mutually subsume but are not equalType-equal, the
// survivor must be deterministic across input shuffles.
//
// Function callback subtyping is the case that triggers mutual subsumption
// today. A typed-rest function `(...xs: T[]) -> R` and an inexact
// zero-param function `(...) -> R` are not equalType, since they differ in
// Inexact and in declared param count, but they share an accept-set of
// [0, ∞) and the same return, so each is a subtype of the other under the
// callback rule. Without the pre-sort, the loop would drop whichever was
// reached first as `i`, and a shuffled input would drop the other. With
// the pre-sort, both input orders pick the same survivor.
//
// parseType cannot author FuncTypeAnn yet, so the test builds the two
// functions directly.
func TestSubsumeMutualPicksCanonicalSurvivor(t *testing.T) {
	c := &Context{}
	restFn := &soltype.FuncType{
		Params: []*soltype.FuncParam{
			{Pattern: &soltype.IdentPat{Name: "xs"}, Type: &soltype.UnknownType{}, Rest: true},
		},
		Ret: num(),
	}
	inexactFn := &soltype.FuncType{Ret: num(), Inexact: true}
	require.False(t, equalType(restFn, inexactFn), "precondition: structurally distinct")
	require.Empty(t, c.Constrain(restFn, inexactFn), "precondition: restFn <: inexactFn")
	require.Empty(t, c.Constrain(inexactFn, restFn), "precondition: inexactFn <: restFn")

	t.Run("union order-independent", func(t *testing.T) {
		forward := newUnion(c, []soltype.Type{restFn, inexactFn}, false)
		reverse := newUnion(c, []soltype.Type{inexactFn, restFn}, false)
		require.True(t, equalType(forward, reverse), "forward %s, reverse %s", soltype.Print(forward), soltype.Print(reverse))
	})
	t.Run("intersection order-independent", func(t *testing.T) {
		forward := newIntersection(c, []soltype.Type{restFn, inexactFn})
		reverse := newIntersection(c, []soltype.Type{inexactFn, restFn})
		require.True(t, equalType(forward, reverse), "forward %s, reverse %s", soltype.Print(forward), soltype.Print(reverse))
	})
}

// TestNewUnionNoSubsumeWithoutContext is the negative case. Without a
// Context, the constructor leaves non-equal subsumable members in place.
// This is the `combine` posture, where coalesced output is deduped and
// lattice-pruned but not subsumed. PR8 closes this gap at the finalization
// boundaries.
func TestNewUnionNoSubsumeWithoutContext(t *testing.T) {
	got := newUnion(nil, parseTypes(t, "number", "1"), false)
	// PrimType ranks before LitType in the kind table, so number leads.
	want := parseType(t, "number | 1")
	require.True(t, equalType(want, got), "got %s", soltype.Print(got))
}

func TestNewIntersectionNoSubsumeWithoutContext(t *testing.T) {
	got := newIntersection(nil, parseTypes(t, "{x: number, ...}", "{x: number, y: string, ...}"))
	require.IsType(t, &soltype.IntersectionType{}, got)
	it := got.(*soltype.IntersectionType)
	require.Len(t, it.Types, 2)
}

// TestNewUnionSubsumptionSkipsVar pins the concrete gate: a member that still
// carries a free type variable is left alone, even with a Context, to avoid
// speculatively pinning that variable mid-walk. The free var has no surface
// form parseType can produce, so the test builds it directly.
func TestNewUnionSubsumptionSkipsVar(t *testing.T) {
	c := &Context{}
	v := c.freshVar(0)
	got := newUnion(c, []soltype.Type{parseType(t, "number"), v}, false)
	require.IsType(t, &soltype.UnionType{}, got)
	u := got.(*soltype.UnionType)
	require.Len(t, u.Types, 2)
}

// TestVoidAndNullSortLast pins the convention that the absence markers
// NullType and Void appear after data members in canonical order, with
// NullType before Void. A mixed union such as `number | null | void`
// surfaces the data first and the absence markers last.
func TestVoidAndNullSortLast(t *testing.T) {
	tests := []struct {
		name  string
		parts []soltype.Type
		want  string
	}{
		{
			name:  "void sorts after a data member",
			parts: parseTypes(t, "void", "number"),
			want:  "number | void",
		},
		{
			name:  "null sorts after a data member",
			parts: parseTypes(t, "null", "number"),
			want:  "number | null",
		},
		{
			name:  "null sorts before void",
			parts: parseTypes(t, "void", "null", "number"),
			want:  "number | null | void",
		},
		{
			name:  "null before void independent of input order",
			parts: parseTypes(t, "void", "null"),
			want:  "null | void",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := newUnion(nil, tt.parts, false)
			require.Equal(t, tt.want, soltype.Print(got))
		})
	}
}

// TestCompareTypeConsistentWithEqual pins compareType's consistency
// contract. Two equalType-equal types must compare equal. Without that,
// canonicalization would be unstable and dedup unreliable.
func TestCompareTypeConsistentWithEqual(t *testing.T) {
	tests := []struct {
		name string
		a, b soltype.Type
	}{
		{"primitives", parseType(t, "number"), parseType(t, "number")},
		{"literals", parseType(t, "5"), parseType(t, "5")},
		{"never", parseType(t, "never"), parseType(t, "never")},
		{"unknown", parseType(t, "unknown"), parseType(t, "unknown")},
		// equalType treats objects as equal up to property order; the
		// comparator must agree.
		{"objects equal up to order", parseType(t, "{a: number, b: string}"), parseType(t, "{b: string, a: number}")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.True(t, equalType(tt.a, tt.b), "precondition: equalType")
			require.Equal(t, 0, compareType(tt.a, tt.b), "compareType must return 0 for equalType-equal types")
		})
	}
}

// TestCompareTypeKindOrder pins the kind ranking that orders dissimilar
// members so a union of mixed kinds renders deterministically. TypeVarType
// leads, then PrimType, then LitType, so a quantified parameter shows up
// before the primitive or literal it is constrained against.
func TestCompareTypeKindOrder(t *testing.T) {
	c := &Context{}
	v := c.freshVar(0)
	require.Less(t, compareType(v, parseType(t, "number")), 0, "TypeVarType < PrimType")
	require.Less(t, compareType(parseType(t, "number"), parseType(t, "1")), 0, "PrimType < LitType")
	require.Less(t, compareType(parseType(t, "never"), v), 0, "NeverType < TypeVarType")
	require.Less(t, compareType(parseType(t, "unknown"), v), 0, "UnknownType < TypeVarType")
}

// TestCompareTypeDistinctRefsWithUnnamedLifetimes pins the structural
// comparator fix for borrows. Two RefTypes that differ only in distinct,
// unnamed LifetimeVars print identically when the top-level Print supplies
// no name map, so a Print-based tie-break would collapse them. The
// structural comparator orders them by LifetimeVar.ID and keeps them
// strictly distinct. The parseType helper does not author borrows with
// LifetimeVars, so the test builds the RefTypes directly.
func TestCompareTypeDistinctRefsWithUnnamedLifetimes(t *testing.T) {
	c := &Context{}
	lt1 := c.freshLifetime(0)
	lt2 := c.freshLifetime(0)
	r1 := &soltype.RefType{Mut: true, Lt: lt1, Inner: exactObj(propElem("x", num()))}
	r2 := &soltype.RefType{Mut: true, Lt: lt2, Inner: exactObj(propElem("x", num()))}
	require.False(t, equalType(r1, r2), "precondition: distinct LifetimeVars are not equalType")
	require.NotEqual(t, 0, compareType(r1, r2), "distinct unnamed-lifetime borrows must compare unequal")
}
