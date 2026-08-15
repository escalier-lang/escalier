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
// is `unknown`. Everywhere else the trial records one subtype candidate, which is sound
// and pins the variable further than the goal asks. weakestBound says why the
// subtraction stops there, and constrainImplied's third bullet records what it leaves
// behind.
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
			// goal settles on T. The subtype side carries a positive part, so T takes that
			// candidate rather than the `"hi" ∩ ¬number` the goal asks for. The two admit the
			// same values here, since `"hi"` and `number` share none.
			name: "a subtype side with a positive part records the candidate",
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
// records when the subtype side holds several atoms that no fusion merged into one.
// Two atoms of different kinds never fuse, since no single record denotes the meet of a
// record and an arrow, so a cross-kind meet is the shape that reaches this.
//
// The trial pairs ONE atom with the variable and the pair holds at once, since
// constraining into a free variable only appends a bound. So the variable records that
// atom and the rest of the meet is dropped. constrainImplied's third bullet states it.
//
// later is a constraint run after the goal, chosen so it holds for the whole meet and
// fails for the single atom. It is what makes the dropped atom observable rather than
// merely unrecorded.
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
			// `{a: 1, ...} & ((x: number) -> string) <: (string | T)`. Neither atom is a
			// subtype of string, so the trial falls through to T and commits the first atom
			// paired with it. sortAtoms ranks a record before an arrow, so T takes the record.
			name: "a record and an arrow record only the record",
			goal: func(c *Context) (soltype.Type, soltype.Type, *soltype.TypeVarType) {
				v := c.freshVar(0)
				sub := newIntersection(nil, []soltype.Type{rec(), numToStr()})
				return sub, newUnion(nil, []soltype.Type{str(), v}, false), v
			},
			later:      func() soltype.Type { return numToStr() },
			wantBounds: []string{"{a: 1, ...}"},
			wantLater: []string{
				"cannot constrain object <: function; t0 was committed to a branch of " +
					"t0 | string by an earlier match, so it cannot also satisfy function",
			},
		},
		{
			// Two arrows with different domains AND different codomains. `(x: number | string)
			// -> ?` names no single codomain, so they do not fuse and the meet stays two atoms.
			name: "two unfused arrows record only the first",
			goal: func(c *Context) (soltype.Type, soltype.Type, *soltype.TypeVarType) {
				v := c.freshVar(0)
				sub := newIntersection(nil, []soltype.Type{numToStr(), strToNum()})
				return sub, newUnion(nil, []soltype.Type{boolT(), v}, false), v
			},
			later:      func() soltype.Type { return strToNum() },
			wantBounds: []string{"fn (x: number) -> string"},
			wantLater: []string{
				"cannot constrain string <: number; t0 was committed to a branch of " +
					"t0 | boolean by an earlier match, so it cannot also satisfy number",
				"cannot constrain string <: number; t0 was committed to a branch of " +
					"t0 | boolean by an earlier match, so it cannot also satisfy number",
			},
		},
		{
			// `(T & {a: 1, ...}) <: (string | T)`, with T already bounded below by 5 so the
			// string candidate fails on both `{a: 1, ...} <: string` and `T <: string`. T then
			// stands on BOTH sides of the goal, which holds by reflexivity and asks nothing of
			// T. The trial records the record anyway, since it pairs `{a: 1, ...}` with T
			// before it reaches the reflexive `T <: T`.
			name: "the target standing in the meet still records another atom",
			goal: func(c *Context) (soltype.Type, soltype.Type, *soltype.TypeVarType) {
				v := c.freshVar(0)
				v.LowerBounds = []soltype.Type{numLit(5)}
				sub := newIntersection(nil, []soltype.Type{v, rec()})
				return sub, newUnion(nil, []soltype.Type{str(), v}, false), v
			},
			wantBounds: []string{"5", "{a: 1, ...}"},
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
