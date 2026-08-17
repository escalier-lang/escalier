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

// Splicing a nested union carries its tail out to the outer union, and the two tails
// merge. The bound says what the tail's unnamed members may be, so a union that swallows
// another inherits whatever the swallowed one could hold.
func TestFlattenUnionMergesTailBounds(t *testing.T) {
	strTail := func(parts ...soltype.Type) soltype.Type { return newBoundedUnion(nil, parts, str()) }
	numTail := func(parts ...soltype.Type) soltype.Type { return newBoundedUnion(nil, parts, num()) }

	tests := []struct {
		name  string
		parts []soltype.Type
		want  string
	}{
		{
			// A nested bounded tail with no other tail to meet comes through as written.
			name:  "a lone bounded tail carries out",
			parts: []soltype.Type{numLit(1), strTail(strLit("a"))},
			want:  `1 | "a" | ...string`,
		},
		{
			// Two bounded tails join their bounds, since a member of either could be in the
			// result.
			name:  "two bounded tails join their bounds",
			parts: []soltype.Type{strTail(strLit("a")), numTail(numLit(1))},
			want:  `1 | "a" | ...(number | string)`,
		},
		{
			// Nothing says what an unbounded tail holds, so meeting one loses the bound.
			name:  "an unbounded tail absorbs a bounded one",
			parts: []soltype.Type{strTail(strLit("a")), newUnion(nil, []soltype.Type{numLit(1)}, true)},
			want:  `1 | "a" | ...`,
		},
		{
			// An exact nested union brings no tail, so the outer one is untouched.
			name:  "an exact nested union leaves the tail alone",
			parts: []soltype.Type{strTail(strLit("a")), unionT(numLit(1), numLit(2))},
			want:  `1 | 2 | "a" | ...string`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, soltype.Print(newUnion(nil, tt.parts, false)))
		})
	}
}

// A tail whose bound the union already names as a member contributes no value that member
// does not, so it drops and the union closes. This is subsumption applied to the tail rather
// than to a member, and equality decides it, so no Context is needed.
func TestCollapseUnionDropsASubsumedTail(t *testing.T) {
	tests := []struct {
		name  string
		build func() soltype.Type
		want  string
	}{
		{
			// `"y" | ..."y"`. Every value the tail could hold is a `"y"`, which the named
			// member already admits, so the union is exactly `"y"` and collapses to it. This
			// is what a distributive conditional leaves when every member and the bound
			// select the same branch.
			name:  "a bound equal to the only member closes the union",
			build: func() soltype.Type { return newBoundedUnion(nil, []soltype.Type{strLit("y")}, strLit("y")) },
			want:  `"y"`,
		},
		{
			// `string | ...string`, the same rule over a primitive.
			name:  "a bound equal to a primitive member closes the union",
			build: func() soltype.Type { return newBoundedUnion(nil, []soltype.Type{str()}, str()) },
			want:  "string",
		},
		{
			// The tail drops but other members remain, so the union stays a union and only
			// loses its `...`.
			name: "the union keeps its other members",
			build: func() soltype.Type {
				return newBoundedUnion(nil, []soltype.Type{strLit("y"), numLit(1)}, strLit("y"))
			},
			want: `1 | "y"`,
		},
		{
			// A bound naming values no member does is not subsumed, so the tail stays.
			name:  "an unrelated bound is kept",
			build: func() soltype.Type { return newBoundedUnion(nil, []soltype.Type{strLit("y")}, str()) },
			want:  `"y" | ...string`,
		},
		{
			// Subsumption needs a member to be subsumed by. A tail with none stays whole.
			name:  "a member-less tail is kept",
			build: func() soltype.Type { return newBoundedUnion(nil, nil, str()) },
			want:  "...string",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, soltype.Print(tt.build()))
		})
	}
}

// A bound is part of a union's identity, so two unions that differ only in what their
// tails admit are neither equal nor deduped into one, and they hold a stable place in
// canonical order. compareType is what sortTypes consults, so an unstable answer there
// would let one member list render two ways.
func TestBoundedTailsCompareByTheirBound(t *testing.T) {
	strBound := newBoundedUnion(nil, []soltype.Type{strLit("a")}, str())
	numBound := newBoundedUnion(nil, []soltype.Type{strLit("a")}, num())
	unbounded := newUnion(nil, []soltype.Type{strLit("a")}, true)

	t.Run("equality reads the bound", func(t *testing.T) {
		require.True(t, equalType(strBound, newBoundedUnion(nil, []soltype.Type{strLit("a")}, str())))
		require.False(t, equalType(strBound, numBound))
		require.False(t, equalType(strBound, unbounded))
		require.False(t, equalType(strBound, unionT(strLit("a"))))
	})

	t.Run("canonical order reads the bound", func(t *testing.T) {
		// An unbounded tail sorts before a bounded one, so the two never compare equal and
		// whichever way a caller hands them over they come back in one order.
		require.Equal(t, -1, compareType(unbounded, strBound))
		require.Equal(t, 1, compareType(strBound, unbounded))
		// Two bounded tails order by their bounds, `number` before `string`.
		require.Equal(t, 1, compareType(strBound, numBound))
		require.Equal(t, -1, compareType(numBound, strBound))
		// Identical bounds tie, which is what lets dedup drop one of them.
		require.Equal(t, 0, compareType(strBound, newBoundedUnion(nil, []soltype.Type{strLit("a")}, str())))
	})
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

// TestNewUnionSubsumptionSkipsLifetimeVar pins the lifetime half of the concrete
// gate: two mut borrows differing only in lifetime variable both survive.
func TestNewUnionSubsumptionSkipsLifetimeVar(t *testing.T) {
	c := &Context{}
	a := &soltype.RefType{Mut: true, Lt: &soltype.LifetimeVar{ID: 0}, Inner: exactObj(propElem("x", num()))}
	b := &soltype.RefType{Mut: true, Lt: &soltype.LifetimeVar{ID: 1}, Inner: exactObj(propElem("x", num()))}
	require.Empty(t, c.Constrain(a, b), "precondition: a <: b")
	require.Empty(t, c.Constrain(b, a), "precondition: b <: a")
	got := newUnion(c, []soltype.Type{a, b}, false)
	require.IsType(t, &soltype.UnionType{}, got, "got %s", soltype.Print(got))
	require.Len(t, got.(*soltype.UnionType).Types, 2)
}

// TestUndefinedAndNullSortLast pins the convention that the absence markers
// NullType and UndefinedType appear after data members in canonical order, with
// NullType before UndefinedType. A mixed union such as `number | null | undefined`
// surfaces the data first and the absence markers last.
func TestUndefinedAndNullSortLast(t *testing.T) {
	tests := []struct {
		name  string
		parts []soltype.Type
		want  string
	}{
		{
			name:  "undefined sorts after a data member",
			parts: parseTypes(t, "undefined", "number"),
			want:  "number | undefined",
		},
		{
			name:  "null sorts after a data member",
			parts: parseTypes(t, "null", "number"),
			want:  "number | null",
		},
		{
			name:  "null sorts before undefined",
			parts: parseTypes(t, "undefined", "null", "number"),
			want:  "number | null | undefined",
		},
		{
			name:  "null before undefined independent of input order",
			parts: parseTypes(t, "undefined", "null"),
			want:  "null | undefined",
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
		// A complement has no surface syntax to parse, so its operands are built directly.
		{"negations over one operand", &soltype.NegationType{Inner: num()}, &soltype.NegationType{Inner: num()}},
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

// TestCompareTypeNegation pins the canonical order over complements. Two complements order
// by their operands, and a complement occupies a kind slot of its own beside the union and
// intersection forms. A complement has no surface syntax, so parseType cannot author one and
// the operands are built directly.
func TestCompareTypeNegation(t *testing.T) {
	negNum := &soltype.NegationType{Inner: num()}
	negStr := &soltype.NegationType{Inner: str()}
	inter := &soltype.IntersectionType{Types: []soltype.Type{num(), str()}}

	// Two complements order by their operands, so the order over negated members mirrors
	// the order over the members themselves.
	require.Less(t, compareType(num(), str()), 0, "precondition: number < string")
	require.Less(t, compareType(negNum, negStr), 0, "¬number < ¬string")

	// The kind slot sits after the union and intersection forms and before the absence
	// markers, so a mixed list renders its data members before `null` and `undefined`.
	require.Less(t, compareType(inter, negNum), 0, "IntersectionType < NegationType")
	require.Less(t, compareType(negNum, parseType(t, "null")), 0, "NegationType < NullType")
	require.Less(t, compareType(parseType(t, "null"), parseType(t, "undefined")), 0,
		"NullType < UndefinedType still holds with the negation slot inserted")

	// The order is total over a mixed list: two different starting permutations sort to
	// the same sequence.
	forward := []soltype.Type{num(), negStr, inter, negNum, parseType(t, "null")}
	reverse := []soltype.Type{parseType(t, "null"), negNum, inter, negStr, num()}
	sortTypes(forward)
	sortTypes(reverse)
	require.Equal(t, len(forward), len(reverse))
	for i := range forward {
		require.True(t, equalType(forward[i], reverse[i]),
			"position %d: %s vs %s", i, soltype.Print(forward[i]), soltype.Print(reverse[i]))
	}
	require.True(t, equalType(negNum, forward[2]), "¬number sorts after the intersection")
	require.True(t, equalType(negStr, forward[3]), "¬string sorts after ¬number")
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
