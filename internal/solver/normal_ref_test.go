package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// The borrow half of the normal-form module: the ref-atom merge that splits its
// work between the type sort and the lifetime sort, and the `¬Ref` exclusion
// invariant that keeps a borrow out of every complement.
//
// A case builds its borrows directly rather than parsing an annotation, because
// parseType does not accept a named lifetime. It renders the result with
// PrintAsScheme, which names each surviving lifetime variable 'a, 'b, … by first
// appearance, so a fused borrow reads `&'a mut {x: number}` and an unfused pair
// reads `&'a mut {x: number} | &'b mut {x: number}`.

// borrow builds a borrow atom. inner is stated as a soltype.RefInner so a case
// reads as the type it writes.
func borrow(mut bool, lt soltype.Lifetime, inner soltype.RefInner) *soltype.RefType {
	return &soltype.RefType{Mut: mut, Lt: lt, Inner: inner}
}

// refObj parses an annotation into a pointee a borrow may wrap, failing the test
// when the annotation names a type that is not a RefInner.
func refObj(t *testing.T, src string) soltype.RefInner {
	t.Helper()
	obj, ok := parseType(t, src).(soltype.RefInner)
	require.True(t, ok, "%s is not borrowable", src)
	return obj
}

// normScheme renders a normal form the way a signature carrying it would read, so
// two distinct lifetimes show as two names rather than as two bare `&`s.
func normScheme(c *Context, ty soltype.Type) string {
	return soltype.PrintAsScheme(c.mkDNF(ty, soltype.Positive).toType())
}

// TestRefAtomMerge pins which pairs of borrow atoms fuse and what the fusion
// carries. Each row unions or intersects two borrows and asserts the normal form.
func TestRefAtomMerge(t *testing.T) {
	tests := []struct {
		name string
		// build returns the two borrow atoms, given the context the case runs in, so
		// a row can mint the lifetimes it needs.
		build func(t *testing.T, c *Context) (soltype.Type, soltype.Type)
		union string // the DNF of `a | b`
		inter string // the DNF of `a & b`
	}{
		{
			// Two distinct lifetime variables have no lifetime that already names their
			// meet or their join, so both borrows stay. A single borrow over the join of
			// the two lifetimes would admit a borrow valid for that join alone, which
			// neither member admits — see the comment above meetRefs.
			name: "borrows differing only in lifetime",
			build: func(t *testing.T, c *Context) (soltype.Type, soltype.Type) {
				obj := refObj(t, "{x: number}")
				return borrow(true, c.freshLifetime(0), obj), borrow(true, c.freshLifetime(0), obj)
			},
			union: "<'a, 'b> &'a mut {x: number} | &'b mut {x: number}",
			inter: "<'a, 'b> &'a mut {x: number} & &'b mut {x: number}",
		},
		{
			// One shared lifetime combines to itself, so two borrows over one pointee
			// and one lifetime are the same atom and collapse.
			name: "borrows sharing one lifetime and one pointee",
			build: func(t *testing.T, c *Context) (soltype.Type, soltype.Type) {
				obj := refObj(t, "{x: number}")
				lt := c.freshLifetime(0)
				return borrow(true, lt, obj), borrow(true, lt, obj)
			},
			union: "<'a> &'a mut {x: number}",
			inter: "<'a> &'a mut {x: number}",
		},
		{
			// A mutable borrow's pointee is invariant, so two borrows over different
			// pointees stay separate rather than fusing to a carrier that would accept a
			// string written into the cell holding the number.
			name: "mutable borrows over different pointees",
			build: func(t *testing.T, c *Context) (soltype.Type, soltype.Type) {
				return borrow(true, c.freshLifetime(0), refObj(t, "{x: number}")),
					borrow(true, c.freshLifetime(0), refObj(t, "{x: string}"))
			},
			union: "<'a, 'b> &'a mut {x: number} | &'b mut {x: string}",
			inter: "<'a, 'b> &'a mut {x: number} & &'b mut {x: string}",
		},
		{
			// An immutable borrow is read-only, so its pointee combines covariantly and
			// the union fuses through joinObjects. Both borrows carry one lifetime, so
			// the fusion asks the lifetime sort for nothing.
			name: "immutable borrows over different pointees",
			build: func(t *testing.T, c *Context) (soltype.Type, soltype.Type) {
				lt := c.freshLifetime(0)
				return borrow(false, lt, refObj(t, "{x: number}")), borrow(false, lt, refObj(t, "{x: string}"))
			},
			union: "<'a> &'a {x: number | string}",
			// Two exact records naming one field meet field by field, which is what
			// meetObjects does whether or not a borrow wraps them.
			inter: "<'a> &'a {x: never}",
		},
		{
			// 'static is the bottom of the outlives lattice, so it drops out of the join
			// and absorbs the meet.
			name: "a 'static borrow beside a variable one",
			build: func(t *testing.T, c *Context) (soltype.Type, soltype.Type) {
				obj := refObj(t, "{x: number}")
				return borrow(true, soltype.Static, obj), borrow(true, c.freshLifetime(0), obj)
			},
			union: "<'a> &'a mut {x: number}",
			inter: "&'static mut {x: number}",
		},
		{
			// An exact record names its whole key set, so nothing is both exactly
			// `{x: number}` and exactly `{y: string}` and the pointees meet at `never`.
			// A borrow of an uninhabited pointee points at nothing, so the whole meet is
			// `never`. Their join has no single record that denotes it, so the union
			// keeps both borrows even though they share a lifetime.
			name: "immutable borrows over records naming different fields",
			build: func(t *testing.T, c *Context) (soltype.Type, soltype.Type) {
				lt := c.freshLifetime(0)
				return borrow(false, lt, refObj(t, "{x: number}")), borrow(false, lt, refObj(t, "{y: string}"))
			},
			union: "<'a> &'a {x: number} | &'a {y: string}",
			inter: "never",
		},
		{
			// A pointee still under inference is opaque to the atom merges, which have
			// no arm for a type variable, so neither the meet nor the join combines the
			// two and both borrows stay.
			name: "immutable borrows over distinct type variables",
			build: func(t *testing.T, c *Context) (soltype.Type, soltype.Type) {
				lt := c.freshLifetime(0)
				return borrow(false, lt, c.freshVar(0)), borrow(false, lt, c.freshVar(0))
			},
			union: "<T0, T1, 'a> &'a T0 | &'a T1",
			inter: "<T0, T1, 'a> &'a T0 & &'a T1",
		},
		{
			// Two unrelated class tags are disjoint, so their meet is `never`. No value
			// inhabits the pointee, and a borrow of an uninhabited pointee points at
			// nothing, so the whole meet is `never` rather than a borrow of one.
			name: "immutable borrows over disjoint class tags",
			build: func(t *testing.T, c *Context) (soltype.Type, soltype.Type) {
				c.registerClass("Point", &ClassDef{})
				c.registerClass("Line", &ClassDef{})
				lt := c.freshLifetime(0)
				return borrow(false, lt, &soltype.ClassType{Name: "Point"}),
					borrow(false, lt, &soltype.ClassType{Name: "Line"})
			},
			// A union of two class tags has no single tag that denotes it, so joinAtoms
			// leaves the pointees alone and both borrows stay.
			union: "<'a> &'a Line | &'a Point",
			inter: "never",
		},
		{
			// A union does not distribute over the lifetime and the pointee at once, so
			// two borrows that differ in BOTH stay two atoms. Fusing them would give
			// `&'a {x: number | string}`, which admits an `&'a {x: number}` that neither
			// member admits: the number-holding member demands 'static. The intersection
			// has no such restriction and fuses both sorts.
			name: "borrows differing in lifetime and pointee at once",
			build: func(t *testing.T, c *Context) (soltype.Type, soltype.Type) {
				return borrow(false, soltype.Static, refObj(t, "{x: number}")),
					borrow(false, c.freshLifetime(0), refObj(t, "{x: string}"))
			},
			union: "<'a> &'static {x: number} | &'a {x: string}",
			inter: "&'static {x: never}",
		},
		{
			// An owned cell carries no lifetime, so it is not a borrow and has nothing
			// to combine with one. 'static does not absorb it.
			name: "an owned mutable cell beside a 'static borrow",
			build: func(t *testing.T, c *Context) (soltype.Type, soltype.Type) {
				obj := refObj(t, "{x: number}")
				return borrow(true, nil, obj), borrow(true, soltype.Static, obj)
			},
			union: "mut {x: number} | &'static mut {x: number}",
			inter: "mut {x: number} & &'static mut {x: number}",
		},
		{
			// A mutable borrow and an immutable one over one pointee are related by
			// mut-decay rather than by a fused wrapper, so both stay.
			name: "borrows of differing mutability",
			build: func(t *testing.T, c *Context) (soltype.Type, soltype.Type) {
				obj := refObj(t, "{x: number}")
				lt := c.freshLifetime(0)
				return borrow(true, lt, obj), borrow(false, lt, obj)
			},
			union: "<'a> &'a {x: number} | &'a mut {x: number}",
			inter: "<'a> &'a {x: number} & &'a mut {x: number}",
		},
		{
			// One shared lifetime needs no join variable, so the fusion combines the two
			// pointees alone. The literal `5` is narrower than `number`, so the union
			// widens the field to `number` and the meet narrows it to `5`.
			name: "immutable borrows sharing one lifetime",
			build: func(t *testing.T, c *Context) (soltype.Type, soltype.Type) {
				lt := c.freshLifetime(0)
				return borrow(false, lt, refObj(t, "{x: number}")), borrow(false, lt, refObj(t, "{x: 5}"))
			},
			union: "<'a> &'a {x: number}",
			inter: "<'a> &'a {x: 5}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Context{}
			a, b := tt.build(t, c)
			require.Equal(t, tt.union, normScheme(c, newUnion(nil, []soltype.Type{a, b}, false)))

			c = &Context{}
			a, b = tt.build(t, c)
			require.Equal(t, tt.inter, normScheme(c, newIntersection(nil, []soltype.Type{a, b})))
		})
	}
}

// TestRefLifetimeCombination pins the lifetime half of the merge on its own, one
// pair of lifetimes at a time. The atom merges reach these two functions only in
// the order sortAtoms puts the borrows in, so a table over the pairs is what
// states the rule in both argument orders.
func TestRefLifetimeCombination(t *testing.T) {
	c := &Context{}
	lt := c.freshLifetime(0)
	other := c.freshLifetime(0)
	// A second 'static instance, since 'static is a lattice bound compared by value
	// rather than an origin compared by identity.
	otherStatic := &soltype.StaticLifetime{}

	tests := []struct {
		name     string
		a, b     soltype.Lifetime
		meet     soltype.Lifetime
		join     soltype.Lifetime
		combines bool
	}{
		{name: "two owned cells", a: nil, b: nil, meet: nil, join: nil, combines: true},
		{name: "an owned cell and a borrow", a: nil, b: lt, combines: false},
		{name: "a borrow and an owned cell", a: lt, b: nil, combines: false},
		{name: "one lifetime with itself", a: lt, b: lt, meet: lt, join: lt, combines: true},
		{name: "two 'static instances", a: soltype.Static, b: otherStatic, meet: soltype.Static, join: soltype.Static, combines: true},
		{name: "'static then a variable", a: soltype.Static, b: lt, meet: soltype.Static, join: lt, combines: true},
		{name: "a variable then 'static", a: lt, b: soltype.Static, meet: soltype.Static, join: lt, combines: true},
		{name: "two distinct variables", a: lt, b: other, combines: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			met, metOK := meetRefLifetimes(tt.a, tt.b)
			require.Equal(t, tt.combines, metOK)
			joined, joinedOK := joinRefLifetimes(tt.a, tt.b)
			require.Equal(t, tt.combines, joinedOK)
			if !tt.combines {
				return
			}
			require.Equal(t, tt.meet, met)
			require.Equal(t, tt.join, joined)
		})
	}
}

// TestRefMergeRecordsNoLifetimeBound holds the line normalization draws around the
// lifetime sort: a merge READS the two lifetimes and never constrains them.
//
// Two things rest on that. constrainNF normalizes both operands before opening the
// probe its derivation runs under, so a bound recorded during a merge would
// outlive a failed constraint. And naming the join of two lifetime variables would
// take a fresh variable bounded below both, which is a bound, so a merge cannot
// name one.
func TestRefMergeRecordsNoLifetimeBound(t *testing.T) {
	c := &Context{}
	obj := refObj(t, "{x: number}")
	first, second := c.freshLifetime(0), c.freshLifetime(0)
	union := newUnion(nil, []soltype.Type{borrow(true, first, obj), borrow(true, second, obj)}, false)

	c.mkDNF(union, soltype.Positive)
	for _, lt := range []*soltype.LifetimeVar{first, second} {
		require.Empty(t, lt.LowerBounds)
		require.Empty(t, lt.UpperBounds)
	}
	require.Equal(t, 2, c.lifetimeCounter, "no merge minted a lifetime")
}

// TestNegatedBorrowPanics pins the `¬Ref` exclusion invariant. A complement of a
// borrow has no sound lifetime, so it is rejected at construction and again where
// normalization would take it apart, rather than being given a reading.
func TestNegatedBorrowPanics(t *testing.T) {
	c := &Context{}
	ref := borrow(true, c.freshLifetime(0), refObj(t, "{x: number}"))
	message := "AssertNegatable: forbidden complement of the borrow &mut {x: number}"

	t.Run("the smart constructor rejects it", func(t *testing.T) {
		require.PanicsWithValue(t, message, func() { soltype.NewNegation(ref) })
	})
	t.Run("pushing it into DNF rejects it", func(t *testing.T) {
		require.PanicsWithValue(t, message, func() { c.mkDNF(not(ref), soltype.Positive) })
	})
	t.Run("pushing it into CNF rejects it", func(t *testing.T) {
		require.PanicsWithValue(t, message, func() { c.mkCNF(not(ref), soltype.Negative) })
	})
	// De Morgan's law turns the complement of a join into a meet of complements, so
	// a borrow the source wrote as a union member still reaches a negated part.
	t.Run("a borrow inside a negated union is rejected", func(t *testing.T) {
		joined := newUnion(nil, []soltype.Type{parseType(t, "{a: number}"), ref}, false)
		require.PanicsWithValue(t, message, func() { c.mkDNF(not(joined), soltype.Positive) })
		require.PanicsWithValue(t, message, func() { c.mkCNF(not(joined), soltype.Negative) })
	})
}

// TestNegationInsideBorrowNormalizes pins the case the invariant leaves alone. A
// complement INSIDE a borrow sits in the pure type sort, which RefInner already
// admits through UnionType and IntersectionType, so it normalizes by the ordinary
// rules while the wrapper stays opaque.
func TestNegationInsideBorrowNormalizes(t *testing.T) {
	c := &Context{}
	// `&'a ({a: number} | ¬¬{x: number})`. Complementing twice returns the original
	// set, so the pointee normalizes to `{a: number} | {x: number}` while the borrow
	// stands as written. The complement sits under a union, which is the only way one
	// sits inside a borrow. RefInner admits UnionType, and NegationType is not a
	// RefInner.
	doubled := not(not(parseType(t, "{x: number}")))
	inner, ok := newUnion(nil, []soltype.Type{parseType(t, "{a: number}"), doubled}, false).(soltype.RefInner)
	require.True(t, ok)
	ref := borrow(false, c.freshLifetime(0), inner)

	require.Equal(t,
		"<'a> &'a ({a: number} | {x: number})",
		soltype.PrintAsScheme(c.normalizeDeep(ref, soltype.Positive)))
}

// TestBorrowNarrowingKeepsBorrowsWhole pins the rule the `¬Ref` invariant rests
// on: normalization never takes a borrow wrapper apart. A borrow reaches the
// normal-form layer as ONE atom and is handed straight back to the structural
// rules, which is what keeps the RefType arm of constrain the only code that reads
// a borrow's mutability and lifetime.
//
// The two operands are the ones `if val r2: mut {x: number} = r` produces over a
// borrow union: the owned-mutable annotation on the subtype side and the union of
// the two borrows on the supertype side. TestInferIfVal's "if-val narrows borrow
// union for write" row runs that program end to end.
//
// The atoms are compared structurally rather than by identity, because
// deepNormalizer rebuilds each borrow around its normalized pointee. What the
// comparison states is that the rebuild carries the same mutability, lifetime, and
// pointee, so nothing about the wrapper was taken apart or dropped.
func TestBorrowNarrowingKeepsBorrowsWhole(t *testing.T) {
	c := &Context{}
	first := borrow(true, c.freshLifetime(0), refObj(t, "{x: number}"))
	second := borrow(true, c.freshLifetime(0), refObj(t, "{x: string}"))
	scrutinee := newUnion(nil, []soltype.Type{first, second}, false)
	// The narrowing annotation `mut {x: number}` is an owned mutable cell, a borrow
	// wrapper with no lifetime.
	annotation := borrow(true, nil, refObj(t, "{x: number}"))

	super := c.mkDeepCNF(scrutinee, soltype.Negative)
	require.Len(t, super.Disjuncts, 1)
	require.Equal(t, []soltype.Type{first, second}, super.Disjuncts[0].Rnf.Atoms)
	require.Empty(t, super.Disjuncts[0].Lnf.Atoms, "no borrow reaches a negated part")

	sub := c.mkDeepDNF(annotation, soltype.Positive)
	require.Len(t, sub.Conjuncts, 1)
	require.Equal(t, []soltype.Type{soltype.Type(annotation)}, sub.Conjuncts[0].Lnf.Atoms)
	require.Empty(t, sub.Conjuncts[0].Rnf.Atoms, "no borrow reaches a negated part")
}

// TestNegationIsNotBorrowable pins the other half of the invariant: no borrow can
// point AT a complement either, so `&¬T` cannot be built in the first place. A
// complement names no allocated value, so there is nothing there to borrow.
func TestNegationIsNotBorrowable(t *testing.T) {
	var complement soltype.Type = &soltype.NegationType{Inner: parseType(t, "{x: number}")}
	_, borrowable := complement.(soltype.RefInner)
	require.False(t, borrowable, "NegationType must not be a RefInner")
	require.False(t, soltype.BorrowableType(complement))
}
