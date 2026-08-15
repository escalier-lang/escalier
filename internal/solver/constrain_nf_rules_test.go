package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// --- The rules constrain routes through the normal-form layer ---
//
// constrain_nf_test.go states the conformance corpus, the verdicts the layer is
// measured against. The tests here cover the layer's own rules: how a complement
// is decided, what a complement records on a variable, what an intersection fuses
// to before it is compared, and that a constraint recursing through a complement
// closes on the coinductive cache.

// TestConstrainNegation covers the rule that routes a complement on either side
// through the normal-form layer. Every row is settled by moving the complement
// across the `<:`, so the decision is an ordinary meet or join of atoms.
//
// A complement has no annotation syntax yet, so each row builds its operands as
// solver types rather than parsing them.
func TestConstrainNegation(t *testing.T) {
	negate := func(t soltype.Type) soltype.Type { return &soltype.NegationType{Inner: t} }

	tests := []struct {
		name       string
		sub, super soltype.Type
		wantErrs   []string
	}{
		{
			// `5 ∩ string` holds no value, so nothing has to be checked. A literal and a
			// primitive of another family are disjoint, which is the fact that settles it.
			name:  "a literal against the complement of another family",
			sub:   numLit(5),
			super: negate(str()),
		},
		{
			// `5 ∩ number` is `5`, and the supertype side is empty, which reads as `never`.
			name:     "a literal against the complement of its own primitive",
			sub:      numLit(5),
			super:    negate(num()),
			wantErrs: []string{"cannot constrain 5 <: ¬number"},
		},
		{
			name:  "a primitive against the complement of another primitive",
			sub:   num(),
			super: negate(str()),
		},
		{
			// The complement moves to the supertype side, which leaves an empty meet on the
			// subtype side. An empty meet is `unknown`, and the diagnostic names it.
			name:     "the complement of a primitive against that primitive's sibling",
			sub:      negate(num()),
			super:    str(),
			wantErrs: []string{"cannot constrain unknown <: string"},
		},
		{
			// De Morgan: `¬(number | string)` normalizes to the two goals `boolean ∩ number`
			// and `boolean ∩ string`, and neither meet is inhabited.
			name:  "a primitive against the complement of a union",
			sub:   boolT(),
			super: negate(&soltype.UnionType{Types: []soltype.Type{num(), str()}}),
		},
		{
			// No value is both a number and a string, so the complement of that meet admits
			// every value.
			name:  "any type against the complement of an uninhabited intersection",
			sub:   num(),
			super: negate(&soltype.IntersectionType{Types: []soltype.Type{num(), str()}}),
		},
		{
			name:  "a literal against a double complement of its primitive",
			sub:   numLit(5),
			super: negate(negate(num())),
		},
		{
			// The union super is `number ∪ ¬number`, which every value inhabits. The
			// complement moves to the subtype side, so the goal becomes `string ∩ number <:
			// number` and the meet is uninhabited.
			name:  "a primitive against a union of a type and its complement",
			sub:   str(),
			super: &soltype.UnionType{Types: []soltype.Type{num(), negate(num())}},
		},
		{
			// No object is a number, so a types-as-values reading answers "holds" here.
			// The solver rejects it instead. Two atoms are known to meet at `never` only
			// when both are drawn from the primitives, the literals, `null`, or
			// `undefined`, so an object met with `number` keeps both atoms and the
			// subtype side reads as inhabited. Widening that disjointness is #1063's
			// simplifier.
			name:     "an object against the complement of a primitive",
			sub:      exactObj(propElem("x", num())),
			super:    negate(num()),
			wantErrs: []string{"cannot constrain object <: ¬number"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Context{}
			require.Equal(t, tt.wantErrs, Messages(c.Constrain(tt.sub, tt.super)))
		})
	}
}

// TestConstrainNegationIntoVarRecordsBound pins what a complement in supertype
// position does to a variable. The variable arms record it whole rather than
// routing it through the normal-form layer, so the complement survives on the
// bound list and coalescing renders it.
func TestConstrainNegationIntoVarRecordsBound(t *testing.T) {
	c := &Context{}
	a := c.freshVar(0)

	require.Empty(t, Messages(c.Constrain(a, &soltype.NegationType{Inner: str()})))
	require.Len(t, a.UpperBounds, 1)
	require.Empty(t, a.LowerBounds)
	require.Equal(t, "¬string", soltype.Print(coalesce(a, soltype.Negative)))
}

// TestConstrainRecordsWeakestBoundOnVarCandidate pins what a goal the normal-form
// layer settles on a supertype-side variable records under that variable. The goal asks
// only for what the other supertype candidates leave uncovered:
//
//	⋂subCands <: p₁ ∪ … ∪ pₙ ∪ v    is    ⋂subCands ∩ ¬p₁ ∩ … ∩ ¬pₙ <: v
//
// weakestBound in constrain_nf.go builds the left-hand side, and only where `⋂subCands`
// is `unknown`. Everywhere else the variable records the whole meet, which is sound and
// pins it further than the goal asks. weakestBound says why the subtraction stops there,
// and constrainImplied's third bullet records what it leaves behind.
//
// wantBounds is the bound list as stored, and wantSurface what the display simplifier
// makes of it.
func TestConstrainRecordsWeakestBoundOnVarCandidate(t *testing.T) {
	tests := []struct {
		name string
		// goal builds the constraint to decide, the variable under test, and the
		// variables the goal should leave unbounded. It mints every variable through
		// c.freshVar so no two share an id.
		goal        func(c *Context) (sub, super soltype.Type, v *soltype.TypeVarType, untouched []*soltype.TypeVarType)
		wantBounds  []string
		wantSurface string
	}{
		{
			// `"hi" <: (T | number)`. The number candidate is trialled first and fails, so the
			// goal settles on T. The subtype side carries a positive part, so T takes the meet
			// rather than the `"hi" ∩ ¬number` the goal asks for. The two admit the same values
			// here, since `"hi"` and `number` share none.
			name: "a subtype side with a positive part records the meet",
			goal: func(c *Context) (soltype.Type, soltype.Type, *soltype.TypeVarType, []*soltype.TypeVarType) {
				v := c.freshVar(0)
				return strLit("hi"), newUnion(nil, []soltype.Type{v, num()}, false), v, nil
			},
			wantBounds:  []string{`"hi"`},
			wantSurface: `"hi"`,
		},
		{
			// `¬T <: number`. The layer moves both complements across the `<:`, which reads
			// `unknown <: number ∪ T`. Recording `unknown` would force `T = unknown` and so
			// collapse `¬T` to `never`, which the goal never asks for.
			name: "a top meet subtracts its concrete siblings",
			goal: func(c *Context) (soltype.Type, soltype.Type, *soltype.TypeVarType, []*soltype.TypeVarType) {
				v := c.freshVar(0)
				return negT(v), num(), v, nil
			},
			wantBounds:  []string{"¬number"},
			wantSurface: "¬number",
		},
		{
			// `5 <: (T | number)`. The number candidate holds, so the trial commits it and
			// never reaches T. A candidate that decides a shape records no bound at all.
			name: "a concrete candidate that holds leaves the variable unbounded",
			goal: func(c *Context) (soltype.Type, soltype.Type, *soltype.TypeVarType, []*soltype.TypeVarType) {
				v := c.freshVar(0)
				return numLit(5), newUnion(nil, []soltype.Type{v, num()}, false), v, nil
			},
			wantBounds:  []string{},
			wantSurface: "never",
		},
		{
			// `¬T <: (number | U)`. The negated variable crosses the `<:` as a positive one,
			// which empties the subtype side and makes this a top meet. The goal reads
			// `unknown <: number ∪ U ∪ T`, and U is settled on first, since two variables
			// keep their list order.
			//
			// T is dropped twice over. weakestBound skips it on concreteMember's gate, so U
			// takes `¬number` rather than the weakest `¬number ∩ ¬T`. U's pair then discharges
			// the goal on its own, so T's own pair is never trialled and T ends unbounded.
			// That is the right answer here, since `number ∪ ¬number` already admits every
			// value `¬T` could hold, whatever T turns out to be.
			name: "a top meet skips a variable sibling",
			goal: func(c *Context) (soltype.Type, soltype.Type, *soltype.TypeVarType, []*soltype.TypeVarType) {
				other, v := c.freshVar(0), c.freshVar(0)
				return negT(other), newUnion(nil, []soltype.Type{num(), v}, false), v, []*soltype.TypeVarType{other}
			},
			wantBounds:  []string{"¬number"},
			wantSurface: "¬number",
		},
		{
			// `¬T <: (U | V)`, which reads `unknown <: U ∪ V ∪ T`. The super has to be the
			// union rather than a bare variable, since a variable operand falls through to the
			// variable arm and never reaches this layer. U is settled on first. Every sibling
			// is a variable, so every one is skipped and the meet is empty. newIntersection
			// returns `unknown` for it, the bound the goal records with no subtraction at all.
			name: "a top meet whose siblings are all variables records unknown",
			goal: func(c *Context) (soltype.Type, soltype.Type, *soltype.TypeVarType, []*soltype.TypeVarType) {
				other, v, third := c.freshVar(0), c.freshVar(0), c.freshVar(0)
				return negT(other), newUnion(nil, []soltype.Type{v, third}, false), v, []*soltype.TypeVarType{other, third}
			},
			wantBounds:  []string{"unknown"},
			wantSurface: "unknown",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Context{}
			sub, super, v, untouched := tt.goal(c)
			require.False(t, hasHardError(c.Constrain(sub, super)))
			require.Equal(t, tt.wantBounds, printedBounds(v.LowerBounds))
			surface := coalesce(v, soltype.Positive).Accept(&finalSubsumer{ctx: c}, soltype.Positive)
			require.Equal(t, tt.wantSurface, soltype.Print(surface))
			// A candidate weakestBound skipped, or one the committed pair made unnecessary,
			// takes no bound of its own. The goal is discharged without it.
			for i, u := range untouched {
				require.Empty(t, printedBounds(u.LowerBounds), "untouched[%d] lower bounds", i)
				require.Empty(t, printedBounds(u.UpperBounds), "untouched[%d] upper bounds", i)
			}
		})
	}
}

// TestConstrainVarCandidateOverAMultiAtomMeet pins what a supertype-side variable
// records when the subtype side holds several atoms that no fusion merged into one. Two
// atoms fuse only when a single atom denotes their meet exactly, so a record met with an
// arrow stays two atoms, and so do two arrows differing in both domain and codomain.
//
// The variable takes ONE pair whose subtype side is the whole meet, so every atom is
// recorded. Pairing a single atom instead would drop the rest, and the trial could not
// notice: a pair against a free variable always holds, so whichever atom came first
// would win on canonical order alone. orderedPairs builds the pair.
//
// later is a constraint run against the variable once the goal is decided. Most rows
// choose one that holds for the whole meet and fails for any single atom, which is what
// makes the recorded meet observable rather than merely stored. The breadcrumb row
// chooses one that fails, so the message it produces can be read.
func TestConstrainVarCandidateOverAMultiAtomMeet(t *testing.T) {
	rec := func() *soltype.ObjectType { return inexactObj(propElem("a", numLit(1))) }
	numToStr := func() *soltype.FuncType { return exactFn(str(), identParam("x", num())) }
	strToNum := func() *soltype.FuncType { return exactFn(num(), identParam("x", str())) }

	tests := []struct {
		name string
		goal func(c *Context) (sub, super soltype.Type, v *soltype.TypeVarType)
		// later runs against the variable once the goal is decided. nil skips it.
		later      func() soltype.Type
		wantBounds []string
		wantLater  []string
	}{
		{
			// `{a: 1, ...} & ((x: number) -> string) <: (string | T)`
			//
			// Neither atom is a subtype of string, so the trial falls through to T and T takes
			// both. later asks `T <: (x: number) -> string`, which holds only because the arrow
			// was recorded alongside the record.
			name: "a record and an arrow record the whole meet",
			goal: func(c *Context) (soltype.Type, soltype.Type, *soltype.TypeVarType) {
				v := c.freshVar(0)
				sub := newIntersection(nil, []soltype.Type{rec(), numToStr()})
				return sub, newUnion(nil, []soltype.Type{str(), v}, false), v
			},
			later:      func() soltype.Type { return numToStr() },
			wantBounds: []string{"{a: 1, ...} & (fn (x: number) -> string)"},
			wantLater:  nil,
		},
		{
			// `((x: number) -> string) & ((x: string) -> number) <: (boolean | T)`
			//
			// The two arrows differ in domain AND codomain, so `(x: number | string) -> ?` names
			// no single codomain and the meet stays two atoms. Neither is a subtype of boolean,
			// so the trial falls through to T. later asks `T <: (x: string) -> number`. The
			// candidate order puts the other arrow first, so this holds exactly when the whole
			// meet was recorded rather than one atom.
			name: "two unfused arrows record both",
			goal: func(c *Context) (soltype.Type, soltype.Type, *soltype.TypeVarType) {
				v := c.freshVar(0)
				sub := newIntersection(nil, []soltype.Type{numToStr(), strToNum()})
				return sub, newUnion(nil, []soltype.Type{boolT(), v}, false), v
			},
			later:      func() soltype.Type { return strToNum() },
			wantBounds: []string{"(fn (x: number) -> string) & (fn (x: string) -> number)"},
			wantLater:  nil,
		},
		{
			// `(T & {a: 1, ...}) <: (string | T)`, decided with `5 <: T` already recorded.
			//
			// T stands on BOTH sides, so the goal holds by reflexivity and asks nothing of T.
			// constrainImplied discharges it before any pair is built, so the string candidate
			// is never trialled and nothing is recorded.
			//
			// The `5` in the expectation is that pre-existing lower bound, unchanged. The goal
			// does not put it there. It is seeded so the assertion tells "the goal added
			// nothing" apart from "T never had a bound at all", which an empty list would leave
			// ambiguous.
			name: "the target standing in the meet records nothing",
			goal: func(c *Context) (soltype.Type, soltype.Type, *soltype.TypeVarType) {
				v := c.freshVar(0)
				v.LowerBounds = []soltype.Type{numLit(5)}
				sub := newIntersection(nil, []soltype.Type{v, rec()})
				return sub, newUnion(nil, []soltype.Type{str(), v}, false), v
			},
			wantBounds: []string{"5"},
		},
		{
			// `(U & {a: 1, ...}) <: (T | U)`
			//
			// U stands on both sides and discharges the goal. T does not, and sorts first among
			// the two variables, so it is the candidate a per-candidate reflexivity check would
			// settle on. That check would give T the whole meet, `U & {a: 1, ...}`, which the
			// goal never asked of it. Checking across the goal leaves T untouched.
			name: "a sibling variable does not record when another discharges the goal",
			goal: func(c *Context) (soltype.Type, soltype.Type, *soltype.TypeVarType) {
				tv, u := c.freshVar(0), c.freshVar(0)
				sub := newIntersection(nil, []soltype.Type{u, rec()})
				return sub, newUnion(nil, []soltype.Type{tv, u}, false), tv
			},
			wantBounds: []string{},
		},
		{
			// `(T & {a: 1, ...}) <: (string | T)` with `5 <: T` already recorded, the goal two
			// rows above, followed by `T <: string`.
			//
			// The discharge chose no candidate, so it reports no commit. Reporting one would tag
			// T as pinned by a branch of `string | T`, and the later failure would then be
			// blamed on a choice that had recorded no bound.
			//
			// The seeded `5` does double duty. It is the unchanged bound wantBounds names, and
			// it is what makes `T <: string` fail, since that constraint propagates T's lower
			// bounds against string. The message names `5 <: string` and nothing more.
			name: "a reflexive discharge leaves no union-commit breadcrumb",
			goal: func(c *Context) (soltype.Type, soltype.Type, *soltype.TypeVarType) {
				v := c.freshVar(0)
				v.LowerBounds = []soltype.Type{numLit(5)}
				sub := newIntersection(nil, []soltype.Type{v, rec()})
				return sub, newUnion(nil, []soltype.Type{str(), v}, false), v
			},
			later:      func() soltype.Type { return str() },
			wantBounds: []string{"5"},
			wantLater:  []string{"cannot constrain 5 <: string"},
		},
		{
			// `{a: 1, ...} & ((x: number) -> string) <: (string | T)`, decided with
			// `T <: (x: number) -> string` already recorded.
			//
			// The upper bound is satisfied by the arrow alone, not by the record. Recording the
			// meet propagates it against that bound and it holds, since the meet is a subtype
			// of every atom it holds. Recording a single atom would pick the record first and
			// fail. This is why taking the meet forgoes no better candidate: whatever one atom
			// satisfies, the meet satisfies too.
			name: "the meet satisfies an upper bound only one atom satisfies",
			goal: func(c *Context) (soltype.Type, soltype.Type, *soltype.TypeVarType) {
				v := c.freshVar(0)
				v.UpperBounds = []soltype.Type{numToStr()}
				sub := newIntersection(nil, []soltype.Type{rec(), numToStr()})
				return sub, newUnion(nil, []soltype.Type{str(), v}, false), v
			},
			wantBounds: []string{"{a: 1, ...} & (fn (x: number) -> string)"},
		},
		{
			// `{a: 1, ...} & ((x: number) -> U) <: (string | T)`, with U one level deeper
			// than T.
			//
			// LevelOf reads an intersection as the max over its members, so recording the meet
			// routes through extrude where a single atom at T's own level would not. U is
			// replaced by a proxy minted at T's level, which is what keeps U from being
			// generalized at the wrong one. The proxy renders as `t2`, the third variable this
			// row mints.
			name: "a meet spanning two levels extrudes the deeper one",
			goal: func(c *Context) (soltype.Type, soltype.Type, *soltype.TypeVarType) {
				outer := c.freshVar(0)
				deep := c.freshVar(1)
				sub := newIntersection(nil, []soltype.Type{rec(), exactFn(deep, identParam("x", num()))})
				return sub, newUnion(nil, []soltype.Type{str(), outer}, false), outer
			},
			wantBounds: []string{"{a: 1, ...} & (fn (x: number) -> t2)"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Context{}
			sub, super, v := tt.goal(c)
			require.False(t, hasHardError(c.Constrain(sub, super)))
			require.Equal(t, tt.wantBounds, printedBounds(v.LowerBounds))
			if tt.later == nil {
				return
			}
			require.Equal(t, tt.wantLater, Messages(c.Constrain(v, tt.later())))
		})
	}
}

// TestConstrainIntersectionFusesBeforeComparing covers the subtype-side win the
// normal-form layer brings. Two inexact objects meet to one object carrying both
// fields, so the intersection satisfies a target naming both. Comparing the
// written members one at a time rejects it, since neither member alone carries
// both fields.
func TestConstrainIntersectionFusesBeforeComparing(t *testing.T) {
	inexactObj := func(elems ...soltype.ObjTypeElem) *soltype.ObjectType {
		return &soltype.ObjectType{Elems: elems, Inexact: true}
	}
	c := &Context{}
	sub := &soltype.IntersectionType{Types: []soltype.Type{
		inexactObj(propElem("x", num())),
		inexactObj(propElem("y", str())),
	}}

	require.Empty(t, Messages(c.Constrain(sub, inexactObj(propElem("x", num()), propElem("y", str())))))
}

// TestConstrainArrowDecompositionRestrictions pins where the arrow decomposition
// stops. It weighs one-parameter arrows only, so the same shape written with two
// parameters is decided by the weaker rule that checks one arm at a time and is
// rejected. A multi-parameter arrow's domain is a product of positions, and
// covering a product needs the union of the arms' whole domains rather than a
// union per position.
//
// The one-parameter form of this shape is the corpus row "distinct domains,
// distinct codomains: the target unions both codomains", which holds.
func TestConstrainArrowDecompositionRestrictions(t *testing.T) {
	c := &Context{}
	env := map[string]soltype.Type{}
	sub := parseTypeIn(t, env,
		"(fn (x: number, y: number) -> boolean) & (fn (x: string, y: string) -> null)")
	super := parseTypeIn(t, env,
		"fn (x: number | string, y: number | string) -> boolean | null")

	// Both diagnostics come from the last arm the pair trial tried, one per parameter
	// position it could not accept.
	require.Equal(t, []string{
		"cannot constrain number <: string",
		"cannot constrain number <: string",
	}, Messages(c.Constrain(sub, super)))
}

// TestConstrainRecursiveListThroughNormalForm covers a recursive type whose body
// is a union, the shape a list builder with a base case infers. See
// recursive_test.go for the source that produces it.
//
//	μX.({head: number, tail: undefined} | {head: number, tail: X})
//
// Deciding two of them terminates only if each unfolding reaches a pair of knots
// the solver is already comparing, so the comparison closes on the knots rather
// than on what a fusion rebuilt around them.
func TestConstrainRecursiveListThroughNormalForm(t *testing.T) {
	list := func(id int, name string) soltype.Type {
		return muKnot(id, name, func(ref *soltype.RecursiveVarType) soltype.Type {
			return newUnion(nil, []soltype.Type{
				exactObj(propElem("head", num()), propElem("tail", &soltype.UndefinedType{})),
				exactObj(propElem("head", num()), propElem("tail", ref)),
			}, false)
		})
	}
	require.Empty(t, Messages((&Context{}).Constrain(list(0, "X0"), list(4, "X1"))))
	require.Empty(t, Messages((&Context{}).Constrain(list(4, "X1"), list(0, "X0"))))
}

// TestConstrainInexactUnionSuperWeighsNamedMembers pins that an inexact union's
// named members are weighed before its open tail absorbs anything. The tail has
// no atom to stand for it, so such a union is one atom and the members would
// otherwise never be reached, leaving the constraint accepted through the tail
// with nothing recorded.
//
// A member that matches has to bind what it binds. Each row's matching member is
// `{x: T}` for a fresh T, so a row that weighed it records the subtype's property
// as T's lower bound and T coalesces to `5`.
func TestConstrainInexactUnionSuperWeighsNamedMembers(t *testing.T) {
	tests := []struct {
		name string
		// rest names the members beside `{x: T}`, and open says whether the union
		// itself carries the tail.
		rest []soltype.Type
		open bool
	}{
		{
			name: "beside another member",
			rest: []soltype.Type{str()},
			open: true,
		},
		{
			// The union collapses to the one member, which is the member to weigh.
			name: "the only member",
			open: true,
		},
		{
			// The tail rides on a member rather than on the union. Splicing the members
			// out of the nested union is what keeps the outer one from inheriting it.
			name: "the tail rides a nested union member",
			rest: []soltype.Type{&soltype.UnionType{Types: []soltype.Type{str(), boolT()}, Inexact: true}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Context{}
			prop := c.freshVar(0)
			members := append([]soltype.Type{exactObj(propElem("x", prop))}, tt.rest...)
			super := &soltype.UnionType{Types: members, Inexact: tt.open}

			require.Empty(t, Messages(c.Constrain(exactObj(propElem("x", numLit(5))), super)))
			require.Len(t, prop.LowerBounds, 1)
			require.Equal(t, "5", soltype.Print(coalesce(prop, soltype.Positive)))
		})
	}
}

// TestConstrainReportsOneDiagnosticPerFailure pins that a failure is reported
// once. The subtype below normalizes to two conjuncts, `{a: number, c: number}`
// and `{b: number, c: number}`, and each fails against `string` the same way. The
// constraint is settled by the first goal that fails, so the diagnostic does not
// repeat per goal.
func TestConstrainReportsOneDiagnosticPerFailure(t *testing.T) {
	c := &Context{}
	sub := &soltype.IntersectionType{Types: []soltype.Type{
		newUnion(nil, []soltype.Type{
			inexactObj(propElem("a", num())),
			inexactObj(propElem("b", num())),
		}, false),
		inexactObj(propElem("c", num())),
	}}

	require.Equal(t, []string{"cannot constrain object <: string"}, Messages(c.Constrain(sub, str())))
}

// TestConstrainUnionCommitOnAFusedAtom covers the ambiguity warning when the
// decision commits to an atom several members fused into. The two object members
// below fuse to `{x: number | string}`, so no member was picked over the other and
// neither is an alternative to the atom it helped build. The bare variable member
// is one, since it would also match by binding, so the warning names it.
func TestConstrainUnionCommitOnAFusedAtom(t *testing.T) {
	c := &Context{}
	catchAll := c.freshVar(0)
	super := newUnion(nil, []soltype.Type{
		exactObj(propElem("x", num())),
		exactObj(propElem("x", str())),
		catchAll,
	}, false).(*soltype.UnionType)

	errs := c.Constrain(exactObj(propElem("x", numLit(5))), super)
	require.Equal(t, []string{
		"ambiguous match against t0 | object | object: committed object, " +
			"but t0 would also match; annotate to disambiguate",
	}, Messages(errs))
	require.False(t, hasHardError(errs))
}

// TestConstrainNegationThroughRecursiveUnion covers termination. The knot below
// carries a complement inside a union, so comparing two of them re-asks the same
// pair on every unfolding. The coinductive cache closes that pair, which is what
// keeps the comparison finite.
//
//	μX.{next: X, tag: string | ¬number}
//
// The two knots number their binders differently, so a comparison that only
// succeeded on identical operands would not settle either direction.
func TestConstrainNegationThroughRecursiveUnion(t *testing.T) {
	knot := func(id int, name string) soltype.Type {
		return muKnot(id, name, func(ref *soltype.RecursiveVarType) soltype.Type {
			return exactObj(
				propElem("next", ref),
				propElem("tag", &soltype.UnionType{
					Types: []soltype.Type{str(), &soltype.NegationType{Inner: num()}},
				}),
			)
		})
	}
	require.Empty(t, Messages((&Context{}).Constrain(knot(0, "X0"), knot(4, "X1"))))
	require.Empty(t, Messages((&Context{}).Constrain(knot(4, "X1"), knot(0, "X0"))))
}
