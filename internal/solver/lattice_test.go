package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// TestNewUnionFlatten exercises step 1 of newUnion's normalization: a nested
// UnionType is spliced into the outer union, and an inexact member contributes
// to the result's inexactness.
func TestNewUnionFlatten(t *testing.T) {
	t.Run("nested union splices in", func(t *testing.T) {
		// (number | string) | boolean ⇒ boolean | number | string (canonical print
		// order within the PrimType kind: alphabetical).
		got := newUnion(nil, []soltype.Type{
			&soltype.UnionType{Types: []soltype.Type{num(), str()}},
			boolT(),
		}, false)
		want := &soltype.UnionType{Types: []soltype.Type{boolT(), num(), str()}}
		require.True(t, equalType(want, got), "got %s", soltype.Print(got))
	})

	t.Run("inexact nested member makes outer inexact", func(t *testing.T) {
		got := newUnion(nil, []soltype.Type{
			&soltype.UnionType{Types: []soltype.Type{num()}, Inexact: true},
			str(),
		}, false)
		want := &soltype.UnionType{Types: []soltype.Type{num(), str()}, Inexact: true}
		require.True(t, equalType(want, got), "got %s", soltype.Print(got))
	})
}

// TestNewIntersectionFlatten is the meet twin of TestNewUnionFlatten.
func TestNewIntersectionFlatten(t *testing.T) {
	// (number & {a}) & {b} ⇒ number & {a} & {b}, canonical order.
	got := newIntersection(nil, []soltype.Type{
		&soltype.IntersectionType{Types: []soltype.Type{num(), exactObj(propElem("a", num()))}},
		exactObj(propElem("b", str())),
	})
	want := &soltype.IntersectionType{Types: []soltype.Type{
		num(),
		exactObj(propElem("a", num())),
		exactObj(propElem("b", str())),
	}}
	require.True(t, equalType(want, got), "got %s", soltype.Print(got))
}

// TestNewUnionLatticeIdentity covers step 2: never (⊥) drops out of a union.
func TestNewUnionLatticeIdentity(t *testing.T) {
	t.Run("never drops from union", func(t *testing.T) {
		got := newUnion(nil, []soltype.Type{num(), &soltype.NeverType{}}, false)
		require.True(t, equalType(num(), got), "got %s", soltype.Print(got))
	})

	t.Run("all-never collapses to never", func(t *testing.T) {
		got := newUnion(nil, []soltype.Type{&soltype.NeverType{}, &soltype.NeverType{}}, false)
		require.IsType(t, &soltype.NeverType{}, got)
	})
}

// TestNewIntersectionLatticeIdentity covers step 2 on the meet side: unknown
// (⊤) drops out of an intersection.
func TestNewIntersectionLatticeIdentity(t *testing.T) {
	t.Run("unknown drops from intersection", func(t *testing.T) {
		got := newIntersection(nil, []soltype.Type{num(), &soltype.UnknownType{}})
		require.True(t, equalType(num(), got), "got %s", soltype.Print(got))
	})

	t.Run("all-unknown collapses to unknown", func(t *testing.T) {
		got := newIntersection(nil, []soltype.Type{&soltype.UnknownType{}, &soltype.UnknownType{}})
		require.IsType(t, &soltype.UnknownType{}, got)
	})
}

// TestNewUnionErrorElision covers step 3: ErrorType drops from both forms,
// unless it ends up the SOLE survivor.
func TestNewUnionErrorElision(t *testing.T) {
	t.Run("error drops from union with other members", func(t *testing.T) {
		got := newUnion(nil, []soltype.Type{num(), &soltype.ErrorType{}}, false)
		require.True(t, equalType(num(), got), "got %s", soltype.Print(got))
	})

	t.Run("error retained as sole member", func(t *testing.T) {
		got := newUnion(nil, []soltype.Type{&soltype.ErrorType{}}, false)
		require.IsType(t, &soltype.ErrorType{}, got)
	})

	t.Run("error retained when other members are all lattice identities", func(t *testing.T) {
		got := newUnion(nil, []soltype.Type{&soltype.ErrorType{}, &soltype.NeverType{}}, false)
		require.IsType(t, &soltype.ErrorType{}, got)
	})
}

func TestNewIntersectionErrorElision(t *testing.T) {
	t.Run("error drops from intersection with other members", func(t *testing.T) {
		got := newIntersection(nil, []soltype.Type{num(), &soltype.ErrorType{}})
		require.True(t, equalType(num(), got), "got %s", soltype.Print(got))
	})

	t.Run("error retained as sole member", func(t *testing.T) {
		got := newIntersection(nil, []soltype.Type{&soltype.ErrorType{}})
		require.IsType(t, &soltype.ErrorType{}, got)
	})

	t.Run("error retained when other members are all lattice identities", func(t *testing.T) {
		got := newIntersection(nil, []soltype.Type{&soltype.ErrorType{}, &soltype.UnknownType{}})
		require.IsType(t, &soltype.ErrorType{}, got)
	})
}

// TestNewUnionDedup covers step 4: structurally-equal members collapse.
func TestNewUnionDedup(t *testing.T) {
	t.Run("union dedup", func(t *testing.T) {
		got := newUnion(nil, []soltype.Type{num(), num(), str()}, false)
		// Canonical order falls back to print string within the PrimType kind:
		// "number" < "string", so num() before str().
		want := &soltype.UnionType{Types: []soltype.Type{num(), str()}}
		require.True(t, equalType(want, got), "got %s", soltype.Print(got))
	})

	t.Run("intersection dedup", func(t *testing.T) {
		got := newIntersection(nil, []soltype.Type{num(), str(), num()})
		want := &soltype.IntersectionType{Types: []soltype.Type{num(), str()}}
		require.True(t, equalType(want, got), "got %s", soltype.Print(got))
	})
}

// TestNewUnionCanonicalOrder is the M6 PR1 gate against speculative-pinning
// drift: a union of a member list and of its shuffle render identically and
// equalType-match. The canonical order makes equalTypeSlice correct without a
// rewrite.
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

// TestNewUnionCollapse covers step 7: an empty union ⇒ never, a single member ⇒
// that member, an empty intersection ⇒ unknown.
func TestNewUnionCollapse(t *testing.T) {
	t.Run("empty union ⇒ never", func(t *testing.T) {
		got := newUnion(nil, nil, false)
		require.IsType(t, &soltype.NeverType{}, got)
	})

	t.Run("single member ⇒ member directly", func(t *testing.T) {
		got := newUnion(nil, []soltype.Type{num()}, false)
		require.True(t, equalType(num(), got))
	})

	t.Run("empty intersection ⇒ unknown", func(t *testing.T) {
		got := newIntersection(nil, nil)
		require.IsType(t, &soltype.UnknownType{}, got)
	})

	t.Run("single intersection member ⇒ member directly", func(t *testing.T) {
		got := newIntersection(nil, []soltype.Type{num()})
		require.True(t, equalType(num(), got))
	})

	t.Run("inexact single-member union keeps the wrapper", func(t *testing.T) {
		// A `... | T` tail makes the union strictly weaker than the bare T, so
		// the inexact single-member union does NOT collapse.
		got := newUnion(nil, []soltype.Type{num()}, true)
		want := &soltype.UnionType{Types: []soltype.Type{num()}, Inexact: true}
		require.True(t, equalType(want, got), "got %s", soltype.Print(got))
	})
}

// TestNewUnionInexactPrintRoundTrip pins the printer's trailing `...` rendering
// for an inexact union, so the flag round-trips to surface syntax.
func TestNewUnionInexactPrintRoundTrip(t *testing.T) {
	u := newUnion(nil, []soltype.Type{num(), str()}, true)
	// Canonical order: number < string (alphabetical print fallback within the
	// PrimType kind).
	require.Equal(t, "number | string | ...", soltype.Print(u))
}

// TestNewUnionSubsumeWithContext covers step 5: when a Context is supplied, a
// member that is a subtype of another member is dropped. `number | 1` ⇒ `number`.
func TestNewUnionSubsumeWithContext(t *testing.T) {
	c := &Context{}
	got := newUnion(c, []soltype.Type{num(), numLit(1)}, false)
	require.True(t, equalType(num(), got), "got %s", soltype.Print(got))
}

// TestNewIntersectionSubsumeWithContext is the meet twin: an intersection
// member that is a SUPERTYPE of another is dropped. Width subtyping holds on
// inexact objects (the wider object is a subtype of the narrower), so
// `{x, ...} & {x, y, ...}` ⇒ `{x, y, ...}`.
func TestNewIntersectionSubsumeWithContext(t *testing.T) {
	c := &Context{}
	got := newIntersection(c, []soltype.Type{
		inexactObj(propElem("x", num())),
		inexactObj(propElem("x", num()), propElem("y", str())),
	})
	want := inexactObj(propElem("x", num()), propElem("y", str()))
	require.True(t, equalType(want, got), "got %s", soltype.Print(got))
}

// TestNewUnionNoSubsumeWithoutContext is the negative case: without a Context,
// the constructor leaves non-equal subsumable members in place. This is the
// `combine` posture — a coalesced output is dedup'd and lattice-pruned but not
// subsumed, since subsumption needs a Context to run constrain under a probe.
// PR8 closes this gap at the finalization boundaries.
func TestNewUnionNoSubsumeWithoutContext(t *testing.T) {
	got := newUnion(nil, []soltype.Type{num(), numLit(1)}, false)
	// Both members survive; canonical order is numLit(1) before num() by kind
	// (LitType ranks before PrimType is false — actually PrimType=4 < LitType=5,
	// so num() first).
	want := &soltype.UnionType{Types: []soltype.Type{num(), numLit(1)}}
	require.True(t, equalType(want, got), "got %s", soltype.Print(got))
}

func TestNewIntersectionNoSubsumeWithoutContext(t *testing.T) {
	got := newIntersection(nil, []soltype.Type{
		inexactObj(propElem("x", num())),
		inexactObj(propElem("x", num()), propElem("y", str())),
	})
	// Both members survive — the wider `{x, ...}` is not dropped without a Context.
	require.IsType(t, &soltype.IntersectionType{}, got)
	it := got.(*soltype.IntersectionType)
	require.Len(t, it.Types, 2)
}

// TestNewUnionSubsumptionSkipsVar pins the concrete gate: a member that still
// carries a free type variable is left alone, even with a Context, to avoid
// speculatively pinning that variable mid-walk. Both members survive.
func TestNewUnionSubsumptionSkipsVar(t *testing.T) {
	c := &Context{}
	v := c.freshVar(0)
	got := newUnion(c, []soltype.Type{num(), v}, false)
	// number | T (a free var), neither member is dropped.
	require.IsType(t, &soltype.UnionType{}, got)
	u := got.(*soltype.UnionType)
	require.Len(t, u.Types, 2)
}

// TestCompareTypeConsistentWithEqual pins compareType's consistency contract:
// two equalType-equal types compare equal. Without this canonicalization would
// be unstable and dedup unreliable.
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
	// PrimType (4) ranks before LitType (5) ranks before TypeVarType (6).
	c := &Context{}
	v := c.freshVar(0)
	require.Less(t, compareType(num(), numLit(1)), 0, "PrimType < LitType")
	require.Less(t, compareType(numLit(1), v), 0, "LitType < TypeVarType")
	require.Less(t, compareType(&soltype.NeverType{}, num()), 0, "NeverType < PrimType")
	require.Less(t, compareType(&soltype.UnknownType{}, num()), 0, "UnknownType < PrimType")
}
