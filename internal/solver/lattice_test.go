package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// inexactTuple builds the tail-tolerant tuple form `[…, ...]`. The mutual-
// subsumption canonicalization tests read at a glance which arm they
// exercise.
func inexactTuple(elems ...soltype.Type) *soltype.TupleType {
	return &soltype.TupleType{Elems: elems, Inexact: true}
}

func exactTuple(elems ...soltype.Type) *soltype.TupleType {
	return &soltype.TupleType{Elems: elems}
}

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
			parts: []soltype.Type{&soltype.UnionType{Types: []soltype.Type{num(), str()}}, boolT()},
			// PrimType members sort by the Prim enum order NumPrim, StrPrim,
			// BoolPrim.
			want: &soltype.UnionType{Types: []soltype.Type{num(), str(), boolT()}},
		},
		{
			name:    "inexact nested member makes outer inexact",
			parts:   []soltype.Type{&soltype.UnionType{Types: []soltype.Type{num()}, Inexact: true}, str()},
			inexact: false,
			want:    &soltype.UnionType{Types: []soltype.Type{num(), str()}, Inexact: true},
		},
		{
			name:  "never drops from union",
			parts: []soltype.Type{num(), &soltype.NeverType{}},
			want:  num(),
		},
		{
			name:  "all-never collapses to never",
			parts: []soltype.Type{&soltype.NeverType{}, &soltype.NeverType{}},
			want:  &soltype.NeverType{},
		},
		{
			name:  "error drops from union with other members",
			parts: []soltype.Type{num(), &soltype.ErrorType{}},
			want:  num(),
		},
		{
			name:  "error retained as sole member",
			parts: []soltype.Type{&soltype.ErrorType{}},
			want:  &soltype.ErrorType{},
		},
		{
			name:  "error retained when other members are all lattice identities",
			parts: []soltype.Type{&soltype.ErrorType{}, &soltype.NeverType{}},
			want:  &soltype.ErrorType{},
		},
		{
			name:  "structural dedup",
			parts: []soltype.Type{num(), num(), str()},
			want:  &soltype.UnionType{Types: []soltype.Type{num(), str()}},
		},
		{
			name:  "empty union collapses to never",
			parts: nil,
			want:  &soltype.NeverType{},
		},
		{
			name:  "single exact member collapses to that member",
			parts: []soltype.Type{num()},
			want:  num(),
		},
		{
			name:    "inexact single member keeps the wrapper",
			parts:   []soltype.Type{num()},
			inexact: true,
			want:    &soltype.UnionType{Types: []soltype.Type{num()}, Inexact: true},
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
			parts: []soltype.Type{&soltype.IntersectionType{Types: []soltype.Type{num(), exactObj(propElem("a", num()))}}, exactObj(propElem("b", str()))},
			want:  &soltype.IntersectionType{Types: []soltype.Type{num(), exactObj(propElem("a", num())), exactObj(propElem("b", str()))}},
		},
		{
			name:  "unknown drops from intersection",
			parts: []soltype.Type{num(), &soltype.UnknownType{}},
			want:  num(),
		},
		{
			name:  "all-unknown collapses to unknown",
			parts: []soltype.Type{&soltype.UnknownType{}, &soltype.UnknownType{}},
			want:  &soltype.UnknownType{},
		},
		{
			name:  "error drops from intersection with other members",
			parts: []soltype.Type{num(), &soltype.ErrorType{}},
			want:  num(),
		},
		{
			name:  "error retained as sole member",
			parts: []soltype.Type{&soltype.ErrorType{}},
			want:  &soltype.ErrorType{},
		},
		{
			name:  "error retained when other members are all lattice identities",
			parts: []soltype.Type{&soltype.ErrorType{}, &soltype.UnknownType{}},
			want:  &soltype.ErrorType{},
		},
		{
			name:  "structural dedup",
			parts: []soltype.Type{num(), str(), num()},
			want:  &soltype.IntersectionType{Types: []soltype.Type{num(), str()}},
		},
		{
			name:  "empty intersection collapses to unknown",
			parts: nil,
			want:  &soltype.UnknownType{},
		},
		{
			name:  "single member collapses to that member",
			parts: []soltype.Type{num()},
			want:  num(),
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
	a := newUnion(nil, []soltype.Type{num(), str()}, false)
	b := newUnion(nil, []soltype.Type{str(), num()}, false)
	require.True(t, equalType(a, b), "expected canonical order to equate both shuffles")
	require.Equal(t, soltype.Print(a), soltype.Print(b))
}

func TestNewIntersectionCanonicalOrder(t *testing.T) {
	a := newIntersection(nil, []soltype.Type{num(), str()})
	b := newIntersection(nil, []soltype.Type{str(), num()})
	require.True(t, equalType(a, b), "expected canonical order to equate both shuffles")
	require.Equal(t, soltype.Print(a), soltype.Print(b))
}

// TestNewUnionInexactPrintRoundTrip pins the printer's trailing `...` rendering
// for an inexact union, so the flag round-trips to surface syntax.
func TestNewUnionInexactPrintRoundTrip(t *testing.T) {
	u := newUnion(nil, []soltype.Type{num(), str()}, true)
	require.Equal(t, "number | string | ...", soltype.Print(u))
}

// TestNewUnionSubsumeWithContext covers the Context-gated subsumption step.
// A member that is a subtype of another member is dropped, so `number | 1`
// reduces to `number`.
func TestNewUnionSubsumeWithContext(t *testing.T) {
	c := &Context{}
	got := newUnion(c, []soltype.Type{num(), numLit(1)}, false)
	require.True(t, equalType(num(), got), "got %s", soltype.Print(got))
}

// TestNewIntersectionSubsumeWithContext is the meet twin. An intersection
// member that is a supertype of another is dropped. Width subtyping on
// inexact objects makes the wider object a subtype of the narrower one, so
// `{x, ...} & {x, y, ...}` reduces to `{x, y, ...}`.
func TestNewIntersectionSubsumeWithContext(t *testing.T) {
	c := &Context{}
	got := newIntersection(c, []soltype.Type{
		inexactObj(propElem("x", num())),
		inexactObj(propElem("x", num()), propElem("y", str())),
	})
	want := inexactObj(propElem("x", num()), propElem("y", str()))
	require.True(t, equalType(want, got), "got %s", soltype.Print(got))
}

// TestSubsumeMutualPicksCanonicalSurvivor pins the M6 PR1 canonicalization
// fix. When two members mutually subsume but are not equalType-equal, the
// survivor must be deterministic across input shuffles. The tuple constrain
// rule admits `[T] <: [T, ...]` and `[T, ...] <: [T]` at equal length, so
// the two tuples are mutual subtypes, but their Inexact flags distinguish
// them structurally. Without the pre-sort, [A, B] and [B, A] would drop
// different members. With it, both produce the same survivor.
func TestSubsumeMutualPicksCanonicalSurvivor(t *testing.T) {
	c := &Context{}
	exact := exactTuple(num())
	inexact := inexactTuple(num())
	t.Run("union forward order", func(t *testing.T) {
		got := newUnion(c, []soltype.Type{exact, inexact}, false)
		require.IsType(t, &soltype.TupleType{}, got)
	})
	t.Run("union reverse order", func(t *testing.T) {
		forward := newUnion(c, []soltype.Type{exact, inexact}, false)
		reverse := newUnion(c, []soltype.Type{inexact, exact}, false)
		require.True(t, equalType(forward, reverse), "forward %s, reverse %s", soltype.Print(forward), soltype.Print(reverse))
	})
	t.Run("intersection forward order", func(t *testing.T) {
		got := newIntersection(c, []soltype.Type{exact, inexact})
		require.IsType(t, &soltype.TupleType{}, got)
	})
	t.Run("intersection reverse order", func(t *testing.T) {
		forward := newIntersection(c, []soltype.Type{exact, inexact})
		reverse := newIntersection(c, []soltype.Type{inexact, exact})
		require.True(t, equalType(forward, reverse), "forward %s, reverse %s", soltype.Print(forward), soltype.Print(reverse))
	})
}

// TestNewUnionNoSubsumeWithoutContext is the negative case. Without a
// Context, the constructor leaves non-equal subsumable members in place.
// This is the `combine` posture, where coalesced output is deduped and
// lattice-pruned but not subsumed. PR8 closes this gap at the finalization
// boundaries.
func TestNewUnionNoSubsumeWithoutContext(t *testing.T) {
	got := newUnion(nil, []soltype.Type{num(), numLit(1)}, false)
	// PrimType ranks before LitType in the kind table, so num leads.
	want := &soltype.UnionType{Types: []soltype.Type{num(), numLit(1)}}
	require.True(t, equalType(want, got), "got %s", soltype.Print(got))
}

func TestNewIntersectionNoSubsumeWithoutContext(t *testing.T) {
	got := newIntersection(nil, []soltype.Type{
		inexactObj(propElem("x", num())),
		inexactObj(propElem("x", num()), propElem("y", str())),
	})
	require.IsType(t, &soltype.IntersectionType{}, got)
	it := got.(*soltype.IntersectionType)
	require.Len(t, it.Types, 2)
}

// TestNewUnionSubsumptionSkipsVar pins the concrete gate: a member that still
// carries a free type variable is left alone, even with a Context, to avoid
// speculatively pinning that variable mid-walk.
func TestNewUnionSubsumptionSkipsVar(t *testing.T) {
	c := &Context{}
	v := c.freshVar(0)
	got := newUnion(c, []soltype.Type{num(), v}, false)
	require.IsType(t, &soltype.UnionType{}, got)
	u := got.(*soltype.UnionType)
	require.Len(t, u.Types, 2)
}

// TestCompareTypeConsistentWithEqual pins compareType's consistency
// contract. Two equalType-equal types must compare equal. Without that,
// canonicalization would be unstable and dedup unreliable.
func TestCompareTypeConsistentWithEqual(t *testing.T) {
	tests := []struct {
		name string
		a, b soltype.Type
	}{
		{"primitives", num(), num()},
		{"literals", numLit(5), numLit(5)},
		{"never", &soltype.NeverType{}, &soltype.NeverType{}},
		{"unknown", &soltype.UnknownType{}, &soltype.UnknownType{}},
		{"objects equal up to order", exactObj(propElem("a", num()), propElem("b", str())), exactObj(propElem("b", str()), propElem("a", num()))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.True(t, equalType(tt.a, tt.b), "precondition: equalType")
			require.Equal(t, 0, compareType(tt.a, tt.b), "compareType must return 0 for equalType-equal types")
		})
	}
}

// TestCompareTypeKindOrder pins the kind ranking that orders dissimilar
// members so a union of mixed kinds renders deterministically.
func TestCompareTypeKindOrder(t *testing.T) {
	c := &Context{}
	v := c.freshVar(0)
	require.Less(t, compareType(num(), numLit(1)), 0, "PrimType < LitType")
	require.Less(t, compareType(numLit(1), v), 0, "LitType < TypeVarType")
	require.Less(t, compareType(&soltype.NeverType{}, num()), 0, "NeverType < PrimType")
	require.Less(t, compareType(&soltype.UnknownType{}, num()), 0, "UnknownType < PrimType")
}

// TestCompareTypeDistinctRefsWithUnnamedLifetimes pins the structural
// comparator fix for borrows. Two RefTypes that differ only in distinct,
// unnamed LifetimeVars print identically when the top-level Print supplies
// no name map, so a Print-based tie-break would collapse them. The
// structural comparator orders them by LifetimeVar.ID and keeps them
// strictly distinct.
func TestCompareTypeDistinctRefsWithUnnamedLifetimes(t *testing.T) {
	c := &Context{}
	lt1 := c.freshLifetime(0)
	lt2 := c.freshLifetime(0)
	r1 := &soltype.RefType{Mut: true, Lt: lt1, Inner: exactObj(propElem("x", num()))}
	r2 := &soltype.RefType{Mut: true, Lt: lt2, Inner: exactObj(propElem("x", num()))}
	require.False(t, equalType(r1, r2), "precondition: distinct LifetimeVars are not equalType")
	require.NotEqual(t, 0, compareType(r1, r2), "distinct unnamed-lifetime borrows must compare unequal")
}
