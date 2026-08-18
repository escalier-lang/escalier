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
		// A union whose open tail carries no bound accepts every value, so its complement
		// accepts none.
		{"open union", newUnion(nil, []soltype.Type{boolT(), str()}, true), "never"},
		{"primitive", str(), "¬string"},
		// A closed union bounds its members, so its complement is a real type.
		{"closed union", unionT(str(), num()), "¬(number | string)"},
		// So does a bounded tail. `¬("a" | ...string)` admits `5`, which is exactly what the
		// fold above would have thrown away.
		{
			"bounded open union",
			newBoundedUnion(nil, []soltype.Type{strLit("a")}, str()),
			`¬("a" | ...string)`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, soltype.Print(newNegation(tt.inner)))
		})
	}
}

// A union whose open tail carries no bound is the top of the subtype lattice, and two
// passes depend on that. newNegation folds `¬(A | B | ...)` to `never`, and
// simplifyNegations collapses a meet carrying such a complement. Both rest on the probes
// below, so they are pinned here rather than left to a comment.
//
// TestBoundedTailIsNotTop is the counterpart. Writing a bound on the tail is what takes
// the union out of the top position these probes put it in.
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

// Writing a bound on the open tail takes the union out of the top position
// TestOpenUnionIsTopForSubtypingOnly pins. `"a" | ...string` names "a" and draws every
// other member it has from `string`, so a value outside `string` is outside the union.
//
// Each probe below has a counterpart in that test that answers the other way, which is
// what makes the bound the thing that changed rather than some difference in the shapes.
func TestBoundedTailIsNotTop(t *testing.T) {
	c := newChecker()
	bounded := newBoundedUnion(nil, []soltype.Type{strLit("a")}, str()) // "a" | ...string

	t.Run("the bound decides what is a subtype of the union", func(t *testing.T) {
		probes := []struct {
			name     string
			sub, sup soltype.Type
			want     bool
		}{
			// A value the bound admits may be one of the tail's members.
			{"a named member is a subtype of it", strLit("a"), bounded, true},
			{"another string is a subtype of it", strLit("z"), bounded, true},
			{"the bound itself is a subtype of it", str(), bounded, true},
			// A value the bound rejects cannot be, which is where the unbounded tail
			// answered true for every sub.
			{"a number literal is not a subtype of it", numLit(5), bounded, false},
			{"a number is not a subtype of it", num(), bounded, false},
			{"unknown is not a subtype of it", &soltype.UnknownType{}, bounded, false},
		}
		for _, p := range probes {
			require.Equal(t, p.want, subtypeHolds(c.ctx, p.sub, p.sup), p.name)
		}
	})

	// The complement is inhabited, so newNegation has something to wrap. `5` is not a key of
	// the object, so it satisfies `¬keyof {a: X, ...}`, while a string that could be one does
	// not. An unbounded tail folds the whole complement to `never`, which nothing satisfies.
	t.Run("its complement is inhabited", func(t *testing.T) {
		require.True(t, subtypeHolds(c.ctx, numLit(5), negT(bounded)))
		require.False(t, subtypeHolds(c.ctx, strLit("z"), negT(bounded)))
	})

	// The bound also decides what the union is a subtype of, which is the direction that
	// carries the tail into a supertype rather than out of one. An unbounded tail is a
	// subtype of nothing but `unknown`, since no closed type absorbs a tail that may hold
	// anything.
	t.Run("the bound decides what the union is a subtype of", func(t *testing.T) {
		probes := []struct {
			name     string
			sub, sup soltype.Type
			want     bool
		}{
			// Every member is a string, the named one included, so `string` absorbs the
			// whole union.
			{"a bounded tail is a subtype of its own bound", bounded, str(), true},
			// The sub's tail may hold "zz", which the super's `number` tail rejects.
			{
				"a string tail is not a subtype of a number tail",
				bounded, newBoundedUnion(nil, []soltype.Type{strLit("a")}, num()), false,
			},
			// The super's tail admits every string, so it absorbs the sub's whole tail.
			{
				"a narrower bound is a subtype of a wider one",
				newBoundedUnion(nil, []soltype.Type{strLit("a")}, strLit("z")), bounded, true,
			},
			{"a bounded tail is not a subtype of a lone member", bounded, strLit("a"), false},
			// The unbounded sub is top, so no bounded super absorbs it. This is the one
			// pair where both operands carry a tail and only the sub's is unbounded.
			{
				"an unbounded tail is not a subtype of a bounded one",
				newUnion(nil, []soltype.Type{strLit("a")}, true), bounded, false,
			},
			{
				"a bounded tail is a subtype of an unbounded one",
				bounded, newUnion(nil, []soltype.Type{strLit("a")}, true), true,
			},
		}
		for _, p := range probes {
			require.Equal(t, p.want, subtypeHolds(c.ctx, p.sub, p.sup), p.name)
		}
	})
}

// The normalization layer reads a bounded tail as one more disjunct, so a decision about
// which values the union admits sees the bound alongside the named members. The two probes
// below are the two directions that reading is consulted from.
func TestNormalizationReadsTheTailBound(t *testing.T) {
	c := newChecker()
	bounded := newBoundedUnion(nil, []soltype.Type{strLit("a")}, str())

	// `"a"` is one of the strings the bound admits, so the two fuse and the whole union
	// normalizes to `string`. An unbounded tail has no atom to fuse and stays whole.
	t.Run("a bounded tail joins the disjuncts", func(t *testing.T) {
		require.Equal(t, "string", soltype.Print(c.ctx.mkCNF(bounded, soltype.Positive).toType()))
		require.Equal(t, "string", soltype.Print(c.ctx.mkDNF(bounded, soltype.Positive).toType()))
	})

	t.Run("an unbounded tail stays one atom", func(t *testing.T) {
		// mkDNF and mkCNF each carry their own unbounded-tail guard, so both are probed.
		open := newUnion(nil, []soltype.Type{strLit("a")}, true)
		require.Equal(t, `"a" | ...`, soltype.Print(c.ctx.mkCNF(open, soltype.Positive).toType()))
		require.Equal(t, `"a" | ...`, soltype.Print(c.ctx.mkDNF(open, soltype.Positive).toType()))
	})
}

// simplifyNegations weighs a bounded tail the way it weighs a named arm, so a complement that
// rules out everything the bound admits empties the meet. An unbounded tail is top and no
// complement can empty it, which TestSimplifyNegationsRefusesToCollapse pins on the other side.
func TestSimplifyNegationsEmptiesABoundedTail(t *testing.T) {
	c := newChecker()
	bounded := newBoundedUnion(nil, []soltype.Type{strLit("a")}, str())

	// `("a" | ...string) & ¬string`. Both the named member and every value the tail could
	// hold is a string, so nothing survives.
	kept, changed, provedEmpty := simplifyNegations(c.ctx, []soltype.Type{bounded, negT(str())})
	require.True(t, provedEmpty)
	require.True(t, changed)
	require.Empty(t, kept)
}

// keyof over an inexact object or tuple is the one site that mints a bounded tail today,
// and the bound is decided by what kind of key the operand has. An object's unlisted keys
// are property names and a tuple's are positions, so the two take different bounds.
func TestKeyofBoundsItsOpenTail(t *testing.T) {
	c := newChecker()
	keysOf := func(t *testing.T, src string) soltype.Type {
		t.Helper()
		nodes, ctx, errs := inferTypeNodes(t, src)
		require.Empty(t, errs)
		return expandAliasResidual(ctx, nodes["Result"])
	}

	t.Run("an object's tail is bounded by string", func(t *testing.T) {
		keys := keysOf(t, `
			type Obj = {a: number, ...}
			type Result = keyof Obj
		`)
		require.Equal(t, `"a" | ...string`, soltype.Print(keys))
		// `5` is plainly not a key of that object, which an unbounded tail could not say.
		require.False(t, subtypeHolds(c.ctx, numLit(5), keys))
		require.True(t, subtypeHolds(c.ctx, strLit("b"), keys))
	})

	t.Run("a tuple's tail is bounded by number", func(t *testing.T) {
		keys := keysOf(t, `
			type Tup = [number, ...]
			type Result = keyof Tup
		`)
		require.Equal(t, "0 | ...number", soltype.Print(keys))
		require.False(t, subtypeHolds(c.ctx, strLit("a"), keys))
		require.True(t, subtypeHolds(c.ctx, numLit(1), keys))
	})
}

// The two routes a complement can take past a variable, which the section comment above
// simplifyNegations describes. They go opposite ways, and only the first leaves a
// complement behind for that pass to find.
func TestNegatedBoundReachesAVariable(t *testing.T) {
	// A variable on the subtype side of a complement stores it whole. Constrain sends a
	// goal to the normal-form layer only when neither operand is a variable, so this one
	// falls through to the variable arm.
	t.Run("a variable below a complement stores it", func(t *testing.T) {
		c := &Context{}
		a := c.freshVar(0)
		require.Empty(t, Messages(c.Constrain(a, negT(str()))))
		require.Equal(t, []string{"¬string"}, printedBounds(a.UpperBounds))
		require.Empty(t, a.LowerBounds)
	})

	// The normal-form layer moves a negated part to the far side of the `<:` as a
	// POSITIVE one, so `α ∩ ¬string <: number` becomes `α <: number | string`. A union in
	// supertype position is an exists rule, so ONE member is kept, never both and never a
	// complement. Recording both would read as the meet `number & string`, which is
	// stronger than the goal.
	//
	// Which member survives depends on α's other bounds, so both outcomes are pinned. A
	// `"hi"` lower bound fails the `number` trial and pushes the choice to `string`.
	t.Run("a negated part crosses over positive", func(t *testing.T) {
		cases := map[string]struct {
			lower []soltype.Type
			want  []string
		}{
			"unconstrained commits the first member": {want: []string{"number"}},
			"a string lower bound commits string":    {lower: []soltype.Type{strLit("hi")}, want: []string{"string"}},
		}
		for name, tt := range cases {
			t.Run(name, func(t *testing.T) {
				c := &Context{}
				a := c.freshVar(0)
				a.LowerBounds = tt.lower
				sub := newIntersection(nil, []soltype.Type{a, negT(str())})
				require.Empty(t, Messages(c.Constrain(sub, num())))
				require.Equal(t, tt.want, printedBounds(a.UpperBounds))
			})
		}
	})

	// The meet reading of the upper-bound list is what rules out recording both members.
	t.Run("upper bounds are read as a meet", func(t *testing.T) {
		c := &Context{}
		a := c.freshVar(0)
		a.UpperBounds = []soltype.Type{num(), str()}
		require.Equal(t, "number & string", soltype.Print(coalesce(a, soltype.Negative)))
	})
}

// printedBounds renders a variable's bound list for comparison against Escalier type
// syntax, which is what makes a bound assertion readable.
func printedBounds(bounds []soltype.Type) []string {
	out := make([]string, len(bounds))
	for i, b := range bounds {
		out[i] = soltype.Print(b)
	}
	return out
}

// subsumeFinal rewrites a union's tail bound before the union itself, so a bound that only
// becomes droppable on the way up still has to be noticed. Neither case below drops a member,
// which is what makes them the cases a member count alone would miss.
func TestFinalSubsumptionRereadsARewrittenTailBound(t *testing.T) {
	tests := []struct {
		name  string
		parts []soltype.Type
		bound soltype.Type
		want  string
	}{
		{
			// `¬string` admits nothing a `string` admits, so the meet is empty and the tail
			// is drawn from `never`. A tail holding nothing leaves the union its named side,
			// and there is none, so the answer is `never` rather than `...never`.
			name:  "a bound that folds to never empties the union",
			bound: newIntersection(nil, []soltype.Type{str(), not(str())}),
			want:  "never",
		},
		{
			// The bound folds to `"y"`, which the union already names. The tail then draws
			// only members the union lists, so it contributes nothing and drops.
			name:  "a bound that folds to a named member drops the tail",
			parts: []soltype.Type{strLit("y")},
			bound: newIntersection(nil, []soltype.Type{strLit("y"), not(strLit("n"))}),
			want:  `"y"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newChecker()
			in := &soltype.UnionType{Types: tt.parts, Inexact: true, TailBound: tt.bound}
			require.Equal(t, tt.want, soltype.Print(c.subsumeFinal(in)))
		})
	}
}
