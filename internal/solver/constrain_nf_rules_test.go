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
			// An object really is disjoint from a primitive, so the sound answer holds. The
			// meet of two atoms is only known uninhabited for the primitives, the literals,
			// `null`, and `undefined`, so this pair keeps both atoms and the goal is
			// rejected. Widening that disjointness is #1063's simplifier.
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
// no atom to stand for it, so the union is one atom and the members would
// otherwise never be reached. A member that matches has to bind what it binds:
// here the matching member's property type is an inference variable, and it picks
// up the subtype's property as a lower bound.
func TestConstrainInexactUnionSuperWeighsNamedMembers(t *testing.T) {
	c := &Context{}
	prop := c.freshVar(0)
	super := &soltype.UnionType{
		Types:   []soltype.Type{exactObj(propElem("x", prop)), str()},
		Inexact: true,
	}

	require.Empty(t, Messages(c.Constrain(exactObj(propElem("x", numLit(5))), super)))
	require.Len(t, prop.LowerBounds, 1)
	require.Equal(t, "5", soltype.Print(coalesce(prop, soltype.Positive)))
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
