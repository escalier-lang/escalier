package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// The normal-forms tests are isolated from constraint solving. Every case here
// pushes a type into DNF or CNF and reads the result back, so nothing in this file
// calls Constrain or records a bound. That keeps the module's behavior pinned
// independently of the solver PR5 (#1062) grafts it into.
//
// A case states its input as an Escalier type annotation and its expected normal
// form as the annotation the normalized type renders under, which is how the rest
// of the solver's tests are written. Negation has no surface syntax, so the two
// helpers below build one.

// not is `¬t`, the complement no annotation can spell.
func not(t soltype.Type) soltype.Type {
	return &soltype.NegationType{Inner: t}
}

// notSrc parses an annotation and complements it, so a case can write `¬number`
// as notSrc(t, "number").
func notSrc(t *testing.T, src string) soltype.Type {
	t.Helper()
	return not(parseType(t, src))
}

// normDNF renders the disjunctive normal form of ty, which is what a round-trip
// case asserts on.
func normDNF(c *Context, ty soltype.Type) string {
	return soltype.Print(c.mkDNF(ty, soltype.Positive).toType())
}

// normCNF renders the conjunctive normal form of ty.
func normCNF(c *Context, ty soltype.Type) string {
	return soltype.Print(c.mkCNF(ty, soltype.Negative).toType())
}

// TestDNFRoundTrip pushes an annotation into DNF and back, and asserts the
// annotation the result renders under. A row whose want repeats its in is a
// round-trip: normalization left the type alone. A row whose want differs records
// a normalization the module performs.
func TestDNFRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "a primitive is one atom", in: "number", want: "number"},
		{name: "a literal is one atom", in: `"a"`, want: `"a"`},
		{name: "the bottom of the lattice", in: "never", want: "never"},
		{name: "the top of the lattice", in: "unknown", want: "unknown"},
		{name: "a record is one atom", in: "{x: number}", want: "{x: number}"},
		{name: "an inexact record keeps its open marker", in: "{x: number, ...}", want: "{x: number, ...}"},
		{name: "a tuple is one atom", in: "[number, string]", want: "[number, string]"},
		{name: "a function is one atom", in: "fn (x: number) -> string", want: "fn (x: number) -> string"},
		{name: "a borrow is an opaque atom", in: "mut {x: number}", want: "mut {x: number}"},
		{name: "a union of two primitives keeps both", in: "number | string", want: "number | string"},
		{
			name: "two exact records keep both atoms",
			in:   "{x: number} & {y: number}",
			want: "{x: number} & {y: number}",
		},
		{
			name: "two inexact records keep both atoms",
			in:   "{x: number, ...} & {y: number, ...}",
			want: "{x: number, ...} & {y: number, ...}",
		},
		{
			name: "two primitives keep both atoms",
			in:   "number & string",
			want: "number & string",
		},
		{
			name: "a repeated member of an intersection dedups",
			in:   "{x: number} & {x: number}",
			want: "{x: number}",
		},
		{
			name: "a repeated member of a union dedups",
			in:   "{x: number} | {x: number}",
			want: "{x: number}",
		},
		{
			name: "an intersection distributes over a union",
			in:   "({x: number} | {y: number}) & {z: number}",
			want: "{x: number} & {z: number} | {y: number} & {z: number}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Context{}
			require.Equal(t, tt.want, normDNF(c, parseType(t, tt.in)))
		})
	}
}

// TestCNFRoundTrip is the conjunctive twin of TestDNFRoundTrip. The two forms
// denote the same type, so a row here states the same annotation the DNF row
// states, rendered as an intersection of joins rather than a union of meets.
func TestCNFRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "a primitive is one atom", in: "number", want: "number"},
		{name: "the bottom of the lattice", in: "never", want: "never"},
		{name: "the top of the lattice", in: "unknown", want: "unknown"},
		{name: "a union is one disjunct", in: "number | string", want: "number | string"},
		{name: "an intersection is two disjuncts", in: "{x: number} & {y: number}", want: "{x: number} & {y: number}"},
		{
			name: "a union distributes over an intersection",
			in:   "({x: number} & {y: number}) | {z: number}",
			want: "({x: number} | {z: number}) & ({y: number} | {z: number})",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Context{}
			require.Equal(t, tt.want, normCNF(c, parseType(t, tt.in)))
		})
	}
}

// TestNegationRoundTrip pins how a complement normalizes. The `¬` cases a reader
// should never see — a double negation and a complement of a lattice bound — are
// gone by the time the form is read back.
func TestNegationRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		in   func(t *testing.T) soltype.Type
		want string
	}{
		{
			name: "a complemented primitive",
			in:   func(t *testing.T) soltype.Type { return notSrc(t, "number") },
			want: "¬number",
		},
		{
			name: "a double complement cancels",
			in:   func(t *testing.T) soltype.Type { return not(notSrc(t, "number")) },
			want: "number",
		},
		{
			name: "the complement of the bottom of the lattice is the top",
			in:   func(t *testing.T) soltype.Type { return notSrc(t, "never") },
			want: "unknown",
		},
		{
			name: "the complement of the top of the lattice is the bottom",
			in:   func(t *testing.T) soltype.Type { return notSrc(t, "unknown") },
			want: "never",
		},
		{
			name: "a complemented union stays one negated atom list",
			in:   func(t *testing.T) soltype.Type { return notSrc(t, "number | string") },
			want: "¬(number | string)",
		},
		{
			name: "a complemented record",
			in:   func(t *testing.T) soltype.Type { return notSrc(t, "{x: number}") },
			want: "¬{x: number}",
		},
		{
			name: "a complement beside a positive atom",
			in: func(t *testing.T) soltype.Type {
				return newIntersection(nil, []soltype.Type{parseType(t, "{x: number, ...}"), notSrc(t, "{y: number}")})
			},
			want: "{x: number, ...} & ¬{y: number}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Context{}
			require.Equal(t, tt.want, normDNF(c, tt.in(t)))
		})
	}
}

// TestCNFNegationRoundTrip covers the negated structural part of a disjunct, the
// slot Conjunct.neg moves a conjunct's positive part into.
func TestCNFNegationRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		in   func(t *testing.T) soltype.Type
		want string
	}{
		{
			name: "a complemented record",
			in:   func(t *testing.T) soltype.Type { return notSrc(t, "{x: number}") },
			want: "¬{x: number}",
		},
		{
			name: "a complemented intersection is one disjunct negating both atoms",
			in: func(t *testing.T) soltype.Type {
				return notSrc(t, "{x: number} & {y: number}")
			},
			want: "¬({x: number} & {y: number})",
		},
		{
			name: "a complement beside a positive atom",
			in: func(t *testing.T) soltype.Type {
				return newUnion(nil, []soltype.Type{parseType(t, "number"), notSrc(t, "{x: number}")}, false)
			},
			want: "number | ¬{x: number}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Context{}
			require.Equal(t, tt.want, normCNF(c, tt.in(t)))
		})
	}
}

// TestDeMorgan asserts that complementing a meet gives the join of the
// complements, and complementing a join gives the meet of them, after both sides
// are normalized. The two sides reach their normal forms by different routes —
// one through Conjunct.neg and one through the ordinary construction — so
// agreeing is a statement about the module, not about the printer.
func TestDeMorgan(t *testing.T) {
	tests := []struct {
		name string
		// operands returns the two types the row complements. It takes a Context so a
		// row can mint type variables that both sides share.
		operands func(t *testing.T, c *Context) (soltype.Type, soltype.Type)
	}{
		{
			name: "two type variables, which normalization cannot look inside",
			operands: func(t *testing.T, c *Context) (soltype.Type, soltype.Type) {
				return c.freshVar(0), c.freshVar(0)
			},
		},
		{
			name: "two records",
			operands: func(t *testing.T, c *Context) (soltype.Type, soltype.Type) {
				return parseType(t, "{x: number}"), parseType(t, "{y: number}")
			},
		},
		{
			name: "two primitives",
			operands: func(t *testing.T, c *Context) (soltype.Type, soltype.Type) {
				return parseType(t, "number"), parseType(t, "string")
			},
		},
		{
			name: "a record and a function",
			operands: func(t *testing.T, c *Context) (soltype.Type, soltype.Type) {
				return parseType(t, "{x: number}"), parseType(t, "fn (x: number) -> string")
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Context{}
			a, b := tt.operands(t, c)

			meet := newIntersection(nil, []soltype.Type{a, b})
			join := newUnion(nil, []soltype.Type{a, b}, false)
			negA, negB := not(a), not(b)

			require.Equal(t,
				normDNF(c, newUnion(nil, []soltype.Type{negA, negB}, false)),
				normDNF(c, not(meet)),
				"¬(A & B) and ¬A | ¬B normalize alike")
			require.Equal(t,
				normDNF(c, newIntersection(nil, []soltype.Type{negA, negB})),
				normDNF(c, not(join)),
				"¬(A | B) and ¬A & ¬B normalize alike")
		})
	}
}

// TestDeMorganOverAnInexactUnion pins the one shape the law above does not cover.
//
// An inexact union `A | B | ...` denotes A, B, and an open tail of unknown
// content, so complementing it excludes that tail too:
//
//	¬(A | B | ...)  is  ¬A ∩ ¬B ∩ ¬tail
//
// which is strictly narrower than `¬A & ¬B`. The two are therefore different
// types, and no flag on the intersection would make them agree. Inexactness on a
// union WIDENS it by an unknown member, and the complement of that is a
// NARROWING by an unknown set — something an intersection has no slot for.
// IntersectionType carries no exactness marker at all, since exactness is a
// property of the result rather than of the meet.
//
// So the complement keeps the whole union as one negated atom, tail and all,
// rather than distributing over its members and silently dropping the tail.
// Threading the marker through normalization is PR7's remit (#1064).
func TestDeMorganOverAnInexactUnion(t *testing.T) {
	c := &Context{}
	a, b := parseType(t, "{x: number}"), parseType(t, "{y: number}")
	meetOfComplements := normDNF(c, newIntersection(nil, []soltype.Type{not(a), not(b)}))
	require.Equal(t, "¬({x: number} | {y: number})", meetOfComplements)

	// The exact union obeys the law, which is what TestDeMorgan states over its
	// record row. Repeating it here is what makes the rows below a statement about
	// the marker rather than about the members.
	closed := newUnion(nil, []soltype.Type{a, b}, false)
	require.Equal(t, meetOfComplements, normDNF(c, not(closed)))

	// The inexact union does not, and its complement still carries the tail.
	open := newUnion(nil, []soltype.Type{a, b}, true)
	require.Equal(t, "¬({x: number} | {y: number} | ...)", normDNF(c, not(open)))
	require.NotEqual(t, meetOfComplements, normDNF(c, not(open)))
}

// TestUnionKeepsUnmergeableRecords is the caveat-4 regression guard. Two records
// with different field names have no single record that denotes their union, so
// both normal forms keep two members rather than widening. MLscript's RhsNf holds
// one record slot and answers `unknown` for the supertype case, which makes every
// subtype pass against it — planning/ml_struct/06-open-items.md finding 1.
func TestUnionKeepsUnmergeableRecords(t *testing.T) {
	c := &Context{}
	ty := parseType(t, "{x: number} | {y: number}")

	// Subtype position. The union is a two-conjunct DNF, one conjunct per record.
	d := c.mkDNF(ty, soltype.Positive)
	require.Len(t, d.Conjuncts, 2)
	require.Len(t, d.Conjuncts[0].Lnf.Atoms, 1)
	require.Len(t, d.Conjuncts[1].Lnf.Atoms, 1)
	require.Equal(t, "{x: number} | {y: number}", soltype.Print(d.toType()))

	// Supertype position, the one MLscript widens. The union is a single disjunct
	// holding BOTH records, not a disjunct that gave up and became `unknown`.
	n := c.mkCNF(ty, soltype.Negative)
	require.Len(t, n.Disjuncts, 1)
	require.Len(t, n.Disjuncts[0].Rnf.Atoms, 2)
	require.Equal(t, "{x: number} | {y: number}", soltype.Print(n.toType()))
}

// TestInexactUnionStaysOneAtom covers the open tail an `A | B | ...` carries. The
// tail has no atom to stand for it, so the union is not taken apart and the DNF
// holds one conjunct rather than one per written member.
func TestInexactUnionStaysOneAtom(t *testing.T) {
	c := &Context{}
	open := newUnion(nil, parseTypes(t, "number", "string"), true)

	d := c.mkDNF(open, soltype.Positive)
	require.Len(t, d.Conjuncts, 1)
	require.Len(t, d.Conjuncts[0].Lnf.Atoms, 1)
	require.Equal(t, "number | string | ...", soltype.Print(d.toType()))

	n := c.mkCNF(open, soltype.Negative)
	require.Len(t, n.Disjuncts, 1)
	require.Len(t, n.Disjuncts[0].Rnf.Atoms, 1)
	require.Equal(t, "number | string | ...", soltype.Print(n.toType()))

	// The exact union of the same members IS taken apart, which is what makes the
	// two rows above a statement about the marker rather than about the members.
	closed := newUnion(nil, parseTypes(t, "number", "string"), false)
	require.Len(t, c.mkDNF(closed, soltype.Positive).Conjuncts, 2)
}

// TestVariablesStayInTheirSlots checks that a type variable is recorded as a
// variable rather than as a structural atom, positively or negatively, and that
// holding one in both slots makes the conjunct uninhabited.
func TestVariablesStayInTheirSlots(t *testing.T) {
	c := &Context{}
	v := c.freshVar(0)

	positive := c.mkDNF(v, soltype.Positive)
	require.Len(t, positive.Conjuncts, 1)
	require.True(t, positive.Conjuncts[0].Vars.Contains(v))
	require.Empty(t, positive.Conjuncts[0].Lnf.Atoms)

	negative := c.mkDNF(not(v), soltype.Positive)
	require.Len(t, negative.Conjuncts, 1)
	require.True(t, negative.Conjuncts[0].NVars.Contains(v))
	require.Empty(t, negative.Conjuncts[0].Rnf.Atoms)

	// `v ∩ ¬v` admits no value, so the conjunct is dropped and the DNF is empty.
	both := newIntersection(nil, []soltype.Type{v, not(v)})
	require.Empty(t, c.mkDNF(both, soltype.Positive).Conjuncts)
	require.Equal(t, "never", normDNF(c, both))

	// `v ∪ ¬v` admits every value, so the disjunct is dropped and the CNF is empty.
	either := newUnion(nil, []soltype.Type{v, not(v)}, false)
	require.Empty(t, c.mkCNF(either, soltype.Negative).Disjuncts)
	require.Equal(t, "unknown", normCNF(c, either))
}

// TestBaseReadsTheSingleClassTag covers the slot PR4 (#1061) wires the nominal
// meet to. One tag in a conjunct is the base; two unrelated tags are both kept, so
// the conjunct has no single base until that PR lands.
func TestBaseReadsTheSingleClassTag(t *testing.T) {
	c := &Context{}
	point := classTag("Point")
	line := classTag("Line")

	one := c.mkDNF(point, soltype.Positive)
	require.Len(t, one.Conjuncts, 1)
	base, ok := one.Conjuncts[0].Lnf.Base()
	require.True(t, ok)
	require.Same(t, point, base)

	// Two tags naming one class are equal, so they dedup and the conjunct still has
	// a single base.
	same := c.mkDNF(newIntersection(nil, []soltype.Type{point, classTag("Point")}), soltype.Positive)
	require.Len(t, same.Conjuncts, 1)
	require.Len(t, same.Conjuncts[0].Lnf.Atoms, 1)

	// TODO(#1061): once PR4 supplies the nominal glb, two unrelated tags collapse
	// the conjunct to `never` and this DNF is empty.
	unrelated := c.mkDNF(newIntersection(nil, []soltype.Type{point, line}), soltype.Positive)
	require.Len(t, unrelated.Conjuncts, 1)
	require.Len(t, unrelated.Conjuncts[0].Lnf.Atoms, 2)
	_, ok = unrelated.Conjuncts[0].Lnf.Base()
	require.False(t, ok)
}

// classTag is a bare nominal handle, the smallest ClassType a normal form can
// carry as an atom.
func classTag(name string) *soltype.ClassType {
	return &soltype.ClassType{
		Name: name, TypeArgs: nil, LifetimeArgs: nil, Lt: nil, Final: false, Variant: false,
	}
}

// TestNominalAtomsStayDistinct guards the kinds compareType has no structural arm
// for. Those atoms all compare equal to one another under it, so a normal form
// that treated a zero from the comparator as equality would delete every class tag
// but one, and would collapse two conjuncts that negate different tags.
func TestNominalAtomsStayDistinct(t *testing.T) {
	point, line, arc := classTag("Point"), classTag("Line"), classTag("Arc")

	t.Run("a union of three tags keeps all three, in one order", func(t *testing.T) {
		for _, order := range permutations([]string{"Point", "Line", "Arc"}) {
			c := &Context{}
			members := make([]soltype.Type, len(order))
			for i, name := range order {
				members[i] = classTag(name)
			}
			d := c.mkDNF(&soltype.UnionType{Types: members, Inexact: false}, soltype.Positive)
			require.Len(t, d.Conjuncts, 3, "order %v", order)
			require.Equal(t, "Arc | Line | Point", soltype.Print(d.toType()), "order %v", order)
		}
	})

	t.Run("two conjuncts negating different tags both survive", func(t *testing.T) {
		c := &Context{}
		shape := parseType(t, "{a: number, ...}")
		withoutPoint := newIntersection(nil, []soltype.Type{shape, not(point)})
		withoutLine := newIntersection(nil, []soltype.Type{shape, not(line)})

		d := c.mkDNF(newUnion(nil, []soltype.Type{withoutPoint, withoutLine}, false), soltype.Positive)
		require.Len(t, d.Conjuncts, 2)
		require.Equal(t,
			"{a: number, ...} & ¬Line | {a: number, ...} & ¬Point",
			soltype.Print(d.toType()))
	})

	t.Run("an intersection of two tags keeps both, in one order", func(t *testing.T) {
		for _, members := range [][]soltype.Type{{point, arc}, {arc, point}} {
			c := &Context{}
			d := c.mkDNF(&soltype.IntersectionType{Types: members}, soltype.Positive)
			require.Len(t, d.Conjuncts, 1)
			require.Equal(t, "Arc & Point", soltype.Print(d.toType()))
		}
	})
}

// TestCanonicalOrderIsPermutationStable builds one type from its members in every
// order and asserts each order reaches the same normal form. The members are
// assembled into raw lattice nodes rather than through newUnion and
// newIntersection, so the smart constructors' own sorting cannot be what makes the
// orders agree.
func TestCanonicalOrderIsPermutationStable(t *testing.T) {
	tests := []struct {
		name string
		// members are the annotations the row combines.
		members []string
		// union says whether to combine them with `|` rather than `&`.
		union bool
		want  string
	}{
		{
			name:    "a union of records",
			members: []string{"{y: number}", "{x: number}", "{z: number}"},
			union:   true,
			want:    "{x: number} | {y: number} | {z: number}",
		},
		{
			name:    "an intersection of records",
			members: []string{"{y: number}", "{x: number}", "{z: number}"},
			want:    "{x: number} & {y: number} & {z: number}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, order := range permutations(tt.members) {
				c := &Context{}
				parts := parseTypes(t, order...)
				var raw soltype.Type
				if tt.union {
					raw = &soltype.UnionType{Types: parts, Inexact: false}
				} else {
					raw = &soltype.IntersectionType{Types: parts}
				}
				require.Equal(t, tt.want, normDNF(c, raw), "order %v", order)
			}
		})
	}
}

// permutations returns every ordering of items. The member lists are three long,
// so the six orderings are cheap to run in full.
func permutations(items []string) [][]string {
	if len(items) <= 1 {
		return [][]string{items}
	}
	var out [][]string
	for i := range items {
		rest := make([]string, 0, len(items)-1)
		rest = append(rest, items[:i]...)
		rest = append(rest, items[i+1:]...)
		for _, tail := range permutations(rest) {
			order := make([]string, 0, len(items))
			order = append(order, items[i])
			order = append(order, tail...)
			out = append(out, order)
		}
	}
	return out
}
