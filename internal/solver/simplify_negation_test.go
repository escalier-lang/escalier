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

// A type operator that rebuilds a union has to carry the tail's bound through, or the result
// lands back at the top of the lattice the bound took it off. Each case runs an operator over
// `keyof {a: number, b: string, ...}`, whose tail is bounded by `string`.
func TestOperatorsCarryTheTailBound(t *testing.T) {
	tests := []struct {
		name string
		// decl is a type declaration appended to the shared Obj alias, binding Result.
		decl string
		want string
	}{
		{
			// The key set the operator starts from.
			name: "Keyof",
			decl: `type Result = keyof Obj`,
			want: `"a" | "b" | ...string`,
		},
		{
			// A filtering conditional splits the bound rather than keeping or dropping it
			// whole, since `"a"` is one of the strings the bound admits. The tail comes back
			// cut by the same exclusion the named members took, which is the answer
			// reduceDifference computes for this set through the `∩ ¬` form.
			name: "DistributiveConditionalSplitsTheBound",
			decl: `
				type Drop<T, U> = if T : U { never } else { T }
				type Result = Drop<keyof Obj, "a">
			`,
			want: `"b" | ...(string & ¬"a")`,
		},
		{
			// A check every value of the bound satisfies takes the same branch for every tail
			// member, so the conditional runs on the bound the way it runs on a named member.
			// Every member and the bound alike select `"y"`, and a tail drawn from a type the
			// union already names adds nothing, so the whole result closes to `"y"`.
			name: "DistributiveConditionalUniformOverTheBound",
			decl: `
				type Yes<T> = if T : string { "y" } else { "n" }
				type Result = Yes<keyof Obj>
			`,
			want: `"y"`,
		},
		{
			// `Inexact` over an already-open union is the identity, so the bound has to
			// survive the rewrite rather than being reset to unbounded.
			name: "InexactIsTheIdentityOnAnOpenUnion",
			decl: `type Result = Inexact<keyof Obj>`,
			want: `"a" | "b" | ...string`,
		},
		{
			// `Exact` closes the union, and a closed union has no tail to bound.
			name: "ExactDropsTheTail",
			decl: `type Result = Exact<keyof Obj>`,
			want: `"a" | "b"`,
		},
		{
			// A string intrinsic transforms the bound alongside the named members. Only a
			// literal has a case to change, so the bound comes back symbolic, but it still
			// says the tail holds strings.
			name: "StringIntrinsic",
			decl: `type Result = Uppercase<keyof Obj>`,
			want: `"A" | "B" | ...Uppercase<string>`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, ctx, errs := inferTypeNodes(t, `
				type Obj = {a: number, b: string, ...}
			`+tt.decl)
			require.Empty(t, errs)
			require.Equal(t, tt.want, soltype.Print(expandAliasResidual(ctx, nodes["Result"])))
		})
	}
}

// Excluding the one key an inexact object names leaves a key set with no named member, so what
// the tail admits is the whole answer. The bound is where the exclusion has to land. A tail that
// kept `string` whole would take back the very key the exclusion removed.
func TestExcludingEveryNamedKeyStillRejectsIt(t *testing.T) {
	nodes, ctx, errs := inferTypeNodes(t, `
		type Obj = {a: number, ...}
		type Drop<T, U> = if T : U { never } else { T }
		type Result = Drop<keyof Obj, "a">
	`)
	require.Empty(t, errs)
	rest := expandAliasResidual(ctx, nodes["Result"])
	require.Equal(t, `...(string & ¬"a")`, soltype.Print(rest))

	c := newChecker()
	require.False(t, subtypeHolds(c.ctx, strLit("a"), rest), "the excluded key is not a member")
	require.True(t, subtypeHolds(c.ctx, strLit("b"), rest), "another key may be one of the unlisted members")
	require.False(t, subtypeHolds(c.ctx, numLit(5), rest), "the bound still rules out a non-string")
}

// A union that names no member and bounds its tail is a shape only the type operators mint.
// Every rule that runs over a union's members has to notice that the members it can see are
// not all there are. Reading the empty list as "nothing" answers `never` or `{}`, and both
// claim the union is empty when it is only unenumerated.
//
// Each case here reduces such a union through a different operator. `Drop<keyof Obj, "a">` is
// `...(string & ¬"a")`, the string keys of an inexact one-key object other than the one it names.
func TestOperatorsOverAMemberlessBoundedUnion(t *testing.T) {
	tests := []struct {
		name string
		decl string
		want string
	}{
		{
			// A mapped type has no key to emit a field for, so the member stays the index
			// signature it was written as. Expanding over no key would give `{}`, and the
			// inexactness marker alone would give `{...}`, which accepts a field of any type.
			name: "MappedTypeStaysAnIndexSignature",
			decl: `type Result = {[K]: boolean for K in Drop<keyof Obj, "a">}`,
			want: `{[K: ...(string & ¬"a")]: boolean}`,
		},
		{
			// A template literal has no choice to fold into its segments, so it stays
			// symbolic. The cartesian product over no choice is empty, which would answer
			// `never`.
			name: "TemplateLiteralStaysSymbolic",
			decl: "type Result = `on${Drop<keyof Obj, \"a\">}`",
			want: "`on${Drop<keyof Obj, \"a\">}`",
		},
		{
			// A second exclusion over the same key set has no named member to filter and no
			// filter answer for the bound, so it stays a meet rather than collapsing.
			name: "SecondExclusionStaysAMeet",
			decl: `type Result = Drop<Drop<keyof Obj, "a">, "b">`,
			want: `¬"b" & Drop<keyof Obj, "a">`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, ctx, errs := inferTypeNodes(t, `
				type Obj = {a: number, ...}
				type Drop<T, U> = if T : U { never } else { T }
			`+tt.decl)
			require.Empty(t, errs)
			require.Equal(t, tt.want, soltype.Print(expandAliasResidual(ctx, nodes["Result"])))
		})
	}

	// A conditional over such a union runs entirely on the bound, since the bound is the only
	// thing the union says. Whether it has an answer decides whether the conditional reduces
	// at all, so both outcomes are stated here as solver types rather than as source, which
	// has no syntax for a bounded tail to write the operand with.
	t.Run("a check the bound answers uniformly reduces", func(t *testing.T) {
		// `if (...string) : string { 1 } else { 2 }`. Every value the tail could hold is a
		// string, so every member takes Then and the result's tail is bounded by `1`.
		cond := &soltype.CondType{
			Check:   newBoundedUnion(nil, nil, str()),
			Extends: str(), Then: numLit(1), Else: numLit(2), Distribute: true,
		}
		require.Equal(t, "...1", soltype.Print(reduceType(cond)))
	})

	t.Run("a check that splits the bound with no difference form stays symbolic", func(t *testing.T) {
		// `if (...string) : "b" { 1 } else { 2 }`. Some strings are "b" and some are not, so
		// the members do not agree on a branch. The conditional keeps neither branch and does
		// not denote a set difference, so there is no answer to give and it stays as written.
		// Unbounding the tail would leave a union with neither member nor bound, which
		// collapses to `never`.
		cond := &soltype.CondType{
			Check:   newBoundedUnion(nil, nil, str()),
			Extends: strLit("b"), Then: numLit(1), Else: numLit(2), Distribute: true,
		}
		require.Equal(t, `if ...string : "b" { 1 } else { 2 }`, soltype.Print(reduceType(cond)))
	})

	// A key set that names keys is still enumerable, so the tail changes nothing about how a
	// mapped type over it expands. This is the case the rules above must not catch.
	t.Run("a named key still gets a field", func(t *testing.T) {
		nodes, ctx, errs := inferTypeNodes(t, `
			type Obj = {a: number, ...}
			type Result = {[K]: boolean for K in keyof Obj}
		`)
		require.Empty(t, errs)
		require.Equal(t, "{a: boolean, ...}", soltype.Print(expandAliasResidual(ctx, nodes["Result"])))
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

// peelBorrows strips the borrow wrapper off every member a union has, and the tail's bound is
// one of them. A dropped bound would leave the peeled union unbounded, which is top.
func TestPeelBorrowsReachesTheTailBound(t *testing.T) {
	inner := &soltype.ObjectType{Elems: []soltype.ObjTypeElem{propElem("x", num())}}
	peeled := peelBorrows(newBoundedUnion(nil, []soltype.Type{num()}, &soltype.RefType{Inner: inner}))
	require.Equal(t, "number | ...{x: number}", soltype.Print(peeled))
}

// Narrowing a union to the members a pattern can destructure weighs the tail's bound too, but
// only where the bound answers for every member drawn from it. The `keep` predicate is a shape
// test over one member, and a bound is the set its members come from, so the two coincide for
// an atomic bound and part ways for a structured one.
func TestNarrowUnionMembersWeighsTheTailBound(t *testing.T) {
	objA := &soltype.ObjectType{Elems: []soltype.ObjTypeElem{propElem("a", num())}}
	objB := &soltype.ObjectType{Elems: []soltype.ObjTypeElem{propElem("b", str())}}
	// keepA stands for an object pattern `{a}`: it accepts a member carrying the key "a".
	keepA := func(m soltype.Type) bool { return objectMemberHasKeys(m, []string{"a"}) }

	t.Run("an atomic bound no member of which fits is dropped", func(t *testing.T) {
		// `{a: number} | {b: string} | ...string`. Every value the tail holds is a string, and
		// no string carries the key "a", so the tail contributes nothing to this branch.
		u := newBoundedUnion(nil, []soltype.Type{objA, objB}, str())
		narrowed, ok := narrowUnionMembers(u, keepA)
		require.True(t, ok)
		require.Equal(t, "{a: number}", soltype.Print(narrowed))
	})

	t.Run("a structured bound is kept", func(t *testing.T) {
		// `{a: number} | {b: string} | ...{c: boolean}`. The bound itself carries no "a", but a
		// member drawn from it may, so deciding the tail needs a disjointness question
		// narrowUnionMembers cannot ask. It keeps the tail, which is the wider answer.
		//
		// The bound names a shape no member of the union does. A bound equal to a named member
		// is subsumed by it and drops at construction, which would leave nothing to narrow.
		objC := &soltype.ObjectType{Elems: []soltype.ObjTypeElem{propElem("c", boolT())}}
		u := newBoundedUnion(nil, []soltype.Type{objA, objB}, objC)
		narrowed, ok := narrowUnionMembers(u, keepA)
		require.True(t, ok)
		require.Equal(t, "{a: number} | ...{c: boolean}", soltype.Print(narrowed))
	})

	t.Run("an unbounded tail is kept", func(t *testing.T) {
		// Nothing says what the tail holds, so it survives every narrowing, which is the rule
		// the field-read path already depends on.
		u := newUnion(nil, []soltype.Type{objA, objB}, true)
		narrowed, ok := narrowUnionMembers(u, keepA)
		require.True(t, ok)
		require.Equal(t, "{a: number} | ...", soltype.Print(narrowed))
	})
}

// An indexed access over a key set or a target that names no member of its own. Both shapes
// arrive from a set difference that excluded every named member, and neither may be read as
// `never`, which would claim the union is empty when it is only unenumerated.
func TestIndexOverAMemberlessBoundedUnion(t *testing.T) {
	obj := &soltype.ObjectType{Elems: []soltype.ObjTypeElem{propElem("x", num())}, Inexact: true}

	t.Run("a member-less key set leaves the access symbolic", func(t *testing.T) {
		// There is no key to read the target at, since the bound says the keys are strings
		// without saying which.
		idx := &soltype.IndexType{Target: obj, Index: newBoundedUnion(nil, nil, str())}
		require.Equal(t, `{x: number, ...}[...string]`, soltype.Print(reduceType(idx)))
	})

	t.Run("a member-less target reads the key off its bound", func(t *testing.T) {
		// The bound names every member the target has, so reading "x" off it reads "x" off
		// all of them at once.
		idx := &soltype.IndexType{Target: newBoundedUnion(nil, nil, obj), Index: strLit("x")}
		require.Equal(t, "...number", soltype.Print(reduceType(idx)))
	})
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
