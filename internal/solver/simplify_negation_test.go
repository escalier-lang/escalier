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
// solver already decides. Each case builds the unsimplified type a bound carrying a
// negation coalesces to, then finalizes it through subsumeFinal, which is where the pass
// runs.
//
// unsimplified is asserted alongside want, so the before-and-after pair below is the real
// one rather than a claim that can drift from what build returns.
func TestSimplifyNegationsDropsDisjointComplements(t *testing.T) {
	tests := []struct {
		name string
		// build returns the intersection before the pass runs, given a checker whose
		// Context the case may seed with class declarations.
		build func(c *checker) soltype.Type
		// unsimplified is what build returns, rendered.
		unsimplified string
		// want is what the pass leaves.
		want string
	}{
		{
			// One guard over a two-member union. `string` cannot survive `¬string`, and
			// what is left is disjoint from `string`, so the complement goes too.
			name:         "one guard over a union",
			build:        func(*checker) soltype.Type { return interT(unionT(str(), num()), negT(str())) },
			unsimplified: "(number | string) & ¬string",
			want:         "number",
		},
		{
			// Two complements over a three-member union. Each excludes one arm, so the
			// third is the only member standing.
			name: "two complements over one union",
			build: func(*checker) soltype.Type {
				return interT(unionT(str(), num(), boolT()), negT(str()), negT(num()))
			},
			unsimplified: "(number | string | boolean) & ¬number & ¬string",
			want:         "boolean",
		},
		{
			// A complement over a primitive excludes the literals of that family, since
			// `"a" <: string`.
			name:         "literal arm excluded by its primitive",
			build:        func(*checker) soltype.Type { return interT(unionT(numLit(5), strLit("a")), negT(str())) },
			unsimplified: `(5 | "a") & ¬string`,
			want:         "5",
		},
		{
			// Nothing was excluded, and the remaining member is disjoint from the
			// operand, so only the complement drops.
			name:         "already-disjoint positive drops the complement",
			build:        func(*checker) soltype.Type { return interT(num(), negT(str())) },
			unsimplified: "number & ¬string",
			want:         "number",
		},
		{
			// The positive part is entirely excluded, so the meet admits no value.
			name:         "complement excludes the whole positive part",
			build:        func(*checker) soltype.Type { return interT(str(), negT(str())) },
			unsimplified: "string & ¬string",
			want:         "never",
		},
		{
			// Two class tags neither of which is below the other are disjoint, which is
			// what glbClass decides.
			name: "class tags",
			build: func(c *checker) soltype.Type {
				c.ctx.registerClass("Animal", &ClassDef{})
				c.ctx.registerClass("Dog", &ClassDef{Supers: []*soltype.ClassType{cls("Animal", false)}})
				c.ctx.registerClass("Cat", &ClassDef{Supers: []*soltype.ClassType{cls("Animal", false)}})
				return interT(unionT(cls("Dog", false), cls("Cat", false)), negT(cls("Dog", false)))
			},
			unsimplified: "(Dog | Cat) & ¬Dog",
			want:         "Cat",
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
			unsimplified: "(Puppy | Cat) & ¬Dog",
			want:         "Cat",
		},
		{
			// A complement standing on its own carries the whole meaning of the type, so
			// it is left as written.
			name:         "bare complement is left alone",
			build:        func(*checker) soltype.Type { return negT(str()) },
			unsimplified: "¬string",
			want:         "¬string",
		},
		{
			// The operand is rewritten before the complement over it, so an operand this
			// pass empties is folded rather than left as the meaningless `¬never`.
			name:         "complement over an emptied operand",
			build:        func(*checker) soltype.Type { return negT(interT(str(), negT(str()))) },
			unsimplified: "¬(string & ¬string)",
			want:         "unknown",
		},
		{
			// An inexact union is the top of the subtype lattice, so nothing satisfies
			// its complement and the meet is empty. Rendering `never` says that, where
			// leaving the complement standing would read as a live constraint on a type
			// no value inhabits.
			name: "complement over an open union empties the meet",
			build: func(*checker) soltype.Type {
				return interT(num(), negT(newUnion(nil, []soltype.Type{boolT(), str()}, true)))
			},
			unsimplified: "number & ¬(string | boolean | ...)",
			want:         "never",
		},
		{
			// The same holds with the open union on both sides. Here the complement folds
			// to `never` on its own, before the meet is weighed at all.
			name: "an open union meets its own complement",
			build: func(*checker) soltype.Type {
				open := func() soltype.Type { return newUnion(nil, []soltype.Type{str(), num()}, true) }
				return interT(open(), negT(open()))
			},
			unsimplified: "(number | string | ...) & ¬(number | string | ...)",
			want:         "never",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newChecker()
			in := tt.build(c)
			require.Equal(t, tt.unsimplified, soltype.Print(in), "input to the pass")
			require.Equal(t, tt.want, soltype.Print(c.subsumeFinal(in)))
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
	// The unsimplified body is `fn (x: T) -> T & ¬Tag`. T occurs in both polarities, so
	// it is retained as a type parameter rather than inlined to its bounds, and the
	// return keeps both members because nothing proves T disjoint from Tag.
	body := &soltype.FuncType{
		Params: []*soltype.FuncParam{fparam("x", param)},
		Ret:    interT(param, negT(cls("Tag", false))),
	}
	require.Equal(t, "fn <T0>(x: T0) -> T0 & ¬Tag", renderScheme(c.generalize(body, 0)))
}

// The cases the pass answers through its return values rather than through the type it
// produces. subsumeFinal runs subsumeMembers over whatever comes back, so calling the pass
// directly is what isolates its own decision.
//
// Each case asserts provedEmpty, which reports what the pass DERIVED rather than whether
// the meet has an inhabitant. A false means the pass reached no derivation, which for a
// meet the concreteness gate keeps it away from says nothing either way.
func TestSimplifyNegationsMemberRewrites(t *testing.T) {
	c := newChecker()

	// `string & ¬string` admits no value and the pass derives it. The empty meet is
	// reported rather than returned as a `never` member, so the caller decides what an
	// empty intersection renders as.
	t.Run("excluded positive part is proved empty", func(t *testing.T) {
		kept, changed, provedEmpty := simplifyNegations(c.ctx, []soltype.Type{str(), negT(str())})
		require.True(t, provedEmpty)
		require.True(t, changed)
		require.Empty(t, kept)
	})

	// `number & string` carries no complement, so it is handed back untouched and the
	// finalizer keeps the node it already had.
	t.Run("nothing to simplify", func(t *testing.T) {
		members := []soltype.Type{num(), str()}
		kept, changed, provedEmpty := simplifyNegations(c.ctx, members)
		require.False(t, provedEmpty)
		require.False(t, changed)
		require.Equal(t, members, kept)
	})

	// `(string | number | ...) & ¬string`. An open tail admits values no arm names, so no
	// arm is dropped and the complement stays as the only statement about what the tail
	// excludes.
	t.Run("inexact union keeps its arms", func(t *testing.T) {
		open := newUnion(nil, []soltype.Type{str(), num()}, true)
		members := []soltype.Type{open, negT(str())}
		kept, changed, provedEmpty := simplifyNegations(c.ctx, members)
		require.False(t, provedEmpty)
		require.False(t, changed)
		require.Equal(t, members, kept)
	})

	// `number & ¬(boolean | ...)`. An inexact union is the top of the subtype lattice, so
	// nothing satisfies its complement and the meet has no inhabitant. The two subtype
	// facts the derivation rests on are asserted first, so a change to either shows up
	// here as well as in TestOpenUnionIsTopForSubtypingOnly.
	t.Run("inexact union operand collapses the meet", func(t *testing.T) {
		open := newUnion(nil, []soltype.Type{boolT()}, true)
		require.True(t, subtypeHolds(c.ctx, num(), open), "every type is below an inexact union")
		require.False(t, subtypeHolds(c.ctx, num(), negT(open)), "so nothing is below its complement")
		kept, changed, provedEmpty := simplifyNegations(c.ctx, []soltype.Type{num(), negT(open)})
		require.True(t, provedEmpty)
		require.True(t, changed)
		require.Empty(t, kept)
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
		// An inexact union accepts every value, so its complement accepts none.
		{"open union", newUnion(nil, []soltype.Type{boolT(), str()}, true), "never"},
		{"primitive", str(), "¬string"},
		// A closed union bounds its members, so its complement is a real type.
		{"closed union", unionT(str(), num()), "¬(number | string)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, soltype.Print(newNegation(tt.inner)))
		})
	}
}

// An inexact union is the top of the subtype lattice, and two passes now depend on that.
// newNegation folds `¬(A | B | ...)` to `never`, and simplifyNegations collapses a meet
// carrying such a complement. Both rest on the probes below, so they are pinned here
// rather than left to a comment.
//
// The second subtest is the step from top-ness to the fold: if `boolean | ...` accepts
// every value then nothing satisfies `¬(boolean | ...)`.
//
// The third records where an open union and `unknown` part ways. A property read answers
// from the open union's named members and is rejected outright on `unknown`. That is the
// one place the named members still carry weight, and it is why an open union is top for
// SUBTYPING rather than identical to `unknown` outright.
func TestOpenUnionIsTopForSubtypingOnly(t *testing.T) {
	c := newChecker()
	open := newUnion(nil, []soltype.Type{boolT()}, true) // boolean | ...
	unknownT := func() soltype.Type { return &soltype.UnknownType{} }

	// For subtyping the two are interchangeable in both directions.
	t.Run("indistinguishable from unknown", func(t *testing.T) {
		probes := []struct {
			name     string
			sub, sup soltype.Type
			want     bool
		}{
			{"every type is below an open union", num(), open, true},
			{"every type is below unknown", num(), unknownT(), true},
			{"an open union is not below a primitive", open, num(), false},
			{"unknown is not below a primitive", unknownT(), num(), false},
			{"an open union is not below its own member", open, boolT(), false},
			{"unknown is not below boolean", unknownT(), boolT(), false},
			{"unknown is below an open union", unknownT(), open, true},
			{"an open union is below unknown", open, unknownT(), true},
		}
		for _, p := range probes {
			require.Equal(t, p.want, subtypeHolds(c.ctx, p.sub, p.sup), p.name)
		}
	})

	// Nothing inhabits the complement of a type every value is below, which is what makes
	// the refused collapse consistent with the rules above.
	t.Run("nothing is below an open union's complement", func(t *testing.T) {
		require.False(t, subtypeHolds(c.ctx, num(), negT(open)))
		require.False(t, subtypeHolds(c.ctx, numLit(5), negT(open)))
	})

	// A property read is where the two part ways. The open union answers from its named
	// members, widened by the tail; unknown has no members to answer from at all.
	t.Run("a property read tells them apart", func(t *testing.T) {
		values, _, errs := inferSource(t, `fn f(u: {x: number} | {x: string} | ...) { return u.x }`)
		require.Empty(t, errs)
		require.Equal(t, "fn (u: {x: number} | {x: string} | ...) -> unknown", values["f"])

		_, _, errs = inferSource(t, `fn g(u: unknown) { return u.x }`)
		require.Equal(t, []string{"1:27-1:30: cannot constrain unknown <: object"}, messagesWithSpan(errs))
	})
}
