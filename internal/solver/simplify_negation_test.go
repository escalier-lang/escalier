package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// --- MLstruct PR6: the disjointness-aware negation simplifier ---

// negT wraps a type in a complement without the newNegation collapses, so a test can
// hand the simplifier the literal form a bound carries.
func negT(inner soltype.Type) soltype.Type { return &soltype.NegationType{Inner: inner} }

// unionT and interT mint the two lattice nodes the way coalesced output does, with no
// Context, so the input to the simplifier is normalized exactly as coalescing leaves it.
func unionT(parts ...soltype.Type) soltype.Type { return newUnion(nil, parts, false) }
func interT(parts ...soltype.Type) soltype.Type { return newIntersection(nil, parts) }

// A complement in a coalesced intersection collapses against the disjointness facts the
// solver already decides. Each case is the type a bound carrying a negation coalesces to,
// finalized through subsumeFinal, which is where the pass runs.
func TestSimplifyNegationsDropsDisjointComplements(t *testing.T) {
	tests := []struct {
		name string
		// build returns the coalesced intersection, given a checker whose Context the
		// case may seed with class declarations.
		build func(c *checker) soltype.Type
		want  string
	}{
		{
			// One guard over a two-member union. `string` cannot survive `¬string`, and
			// what is left is disjoint from `string`, so the complement goes too.
			name:  "one guard over a union",
			build: func(*checker) soltype.Type { return interT(unionT(str(), num()), negT(str())) },
			want:  "number",
		},
		{
			// Three chained guards, the accumulation caveat 2 names. Each complement
			// narrows what the previous one left.
			name: "three-guard chain",
			build: func(*checker) soltype.Type {
				return interT(unionT(str(), num(), boolT()), negT(str()), negT(num()))
			},
			want: "boolean",
		},
		{
			// A complement over a primitive excludes the literals of that family, since
			// `"a" <: string`.
			name:  "literal arm excluded by its primitive",
			build: func(*checker) soltype.Type { return interT(unionT(numLit(5), strLit("a")), negT(str())) },
			want:  "5",
		},
		{
			// Nothing was excluded, and the remaining member is disjoint from the
			// operand, so only the complement drops.
			name:  "already-disjoint positive drops the complement",
			build: func(*checker) soltype.Type { return interT(num(), negT(str())) },
			want:  "number",
		},
		{
			// The positive part is entirely excluded, so the meet admits no value.
			name:  "complement excludes the whole positive part",
			build: func(*checker) soltype.Type { return interT(str(), negT(str())) },
			want:  "never",
		},
		{
			// Two class tags neither of which is below the other are disjoint, which is
			// the M5 fact glbClass decides. `(Dog | Cat) & ¬Dog` is `Cat`.
			name: "class tags",
			build: func(c *checker) soltype.Type {
				c.ctx.registerClass("Animal", &ClassDef{})
				c.ctx.registerClass("Dog", &ClassDef{Supers: []*soltype.ClassType{cls("Animal", false)}})
				c.ctx.registerClass("Cat", &ClassDef{Supers: []*soltype.ClassType{cls("Animal", false)}})
				return interT(unionT(cls("Dog", false), cls("Cat", false)), negT(cls("Dog", false)))
			},
			want: "Cat",
		},
		{
			// A subclass is below the excluded tag, so it goes with it.
			name: "subclass excluded with its parent",
			build: func(c *checker) soltype.Type {
				c.ctx.registerClass("Animal", &ClassDef{})
				c.ctx.registerClass("Dog", &ClassDef{Supers: []*soltype.ClassType{cls("Animal", false)}})
				c.ctx.registerClass("Puppy", &ClassDef{Supers: []*soltype.ClassType{cls("Dog", false)}})
				c.ctx.registerClass("Cat", &ClassDef{Supers: []*soltype.ClassType{cls("Animal", false)}})
				return interT(unionT(cls("Puppy", false), cls("Cat", false)), negT(cls("Dog", false)))
			},
			want: "Cat",
		},
		{
			// A complement standing on its own carries the whole meaning of the type, so
			// it is left as written.
			name:  "bare complement is left alone",
			build: func(*checker) soltype.Type { return negT(str()) },
			want:  "¬string",
		},
		{
			// The operand is rewritten before the complement over it, so an operand this
			// pass empties is folded rather than left as the meaningless `¬never`.
			name:  "complement over an emptied operand",
			build: func(*checker) soltype.Type { return negT(interT(str(), negT(str()))) },
			want:  "unknown",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newChecker()
			require.Equal(t, tt.want, soltype.Print(c.subsumeFinal(tt.build(c))))
		})
	}
}

// A complement over an abstract type parameter is irreducible: nothing proves T disjoint
// from the excluded tag, so the pass must render it faithfully rather than drop it. The
// scheme goes through generalize, the path a real binding takes to its display type.
func TestSimplifyNegationsKeepsIrreducibleComplement(t *testing.T) {
	c := newChecker()
	c.ctx.registerClass("Tag", &ClassDef{})
	param := c.freshAt(1)
	// `fn (x: T) -> T & ¬Tag` — T occurs in both polarities, so it is retained as a
	// type parameter rather than inlined to its bounds.
	body := &soltype.FuncType{
		Params: []*soltype.FuncParam{fparam("x", param)},
		Ret:    interT(param, negT(cls("Tag", false))),
	}
	require.Equal(t, "fn <T0>(x: T0) -> T0 & ¬Tag", renderScheme(c.generalize(body, 0)))
}

// The cases the pass answers through its return values rather than through the type it
// produces. subsumeFinal runs subsumeMembers over whatever comes back, so calling the pass
// directly is what isolates its own decision.
func TestSimplifyNegationsMemberRewrites(t *testing.T) {
	c := newChecker()

	// An empty meet is reported rather than returned as a `never` member, so the caller
	// decides what an uninhabited intersection renders as.
	t.Run("excluded positive part is uninhabited", func(t *testing.T) {
		kept, changed, uninhabited := simplifyNegations(c.ctx, []soltype.Type{str(), negT(str())})
		require.True(t, uninhabited)
		require.True(t, changed)
		require.Empty(t, kept)
	})

	// A meet with no complement is handed back untouched, so the finalizer keeps the node
	// it already had.
	t.Run("nothing to simplify", func(t *testing.T) {
		members := []soltype.Type{num(), str()}
		kept, changed, uninhabited := simplifyNegations(c.ctx, members)
		require.False(t, uninhabited)
		require.False(t, changed)
		require.Equal(t, members, kept)
	})

	// An open tail admits values no arm names, so no arm is dropped and the complement
	// stays as the only statement about what the tail excludes.
	t.Run("inexact union keeps its arms", func(t *testing.T) {
		open := newUnion(nil, []soltype.Type{str(), num()}, true)
		members := []soltype.Type{open, negT(str())}
		kept, changed, uninhabited := simplifyNegations(c.ctx, members)
		require.False(t, uninhabited)
		require.False(t, changed)
		require.Equal(t, members, kept)
	})

	// Every type is a subtype of an inexact union under the open-tail rule, so a
	// complement over one would exclude whatever it is met with. Such an operand is
	// refused, leaving `number & ¬(boolean | ...)` with both members.
	t.Run("inexact union operand is refused", func(t *testing.T) {
		open := newUnion(nil, []soltype.Type{boolT()}, true)
		require.True(t, subtypeHolds(c.ctx, num(), open))
		members := []soltype.Type{num(), negT(open)}
		kept, changed, uninhabited := simplifyNegations(c.ctx, members)
		require.False(t, uninhabited)
		require.False(t, changed)
		require.Equal(t, members, kept)
	})
}

// newNegation collapses the three operands whose complement the surface type set already
// spells, and wraps everything else.
func TestNewNegation(t *testing.T) {
	tests := []struct {
		name  string
		inner soltype.Type
		want  string
	}{
		{"never", &soltype.NeverType{}, "unknown"},
		{"unknown", &soltype.UnknownType{}, "never"},
		{"double complement", negT(str()), "string"},
		{"primitive", str(), "¬string"},
		{"union", unionT(str(), num()), "¬(number | string)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, soltype.Print(newNegation(tt.inner)))
		})
	}
}
