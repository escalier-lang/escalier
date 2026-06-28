package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// --- M6 PR5: the unknown (⊤) and never (⊥) lattice-bound rules ---

// TestConstrainTopRule covers `sub <: unknown`. unknown is the top of the
// subtype lattice, so every sub succeeds. A variable sub records no upper bound,
// since unknown is the meet identity and would add nothing.
func TestConstrainTopRule(t *testing.T) {
	t.Run("a primitive is a subtype of unknown", func(t *testing.T) {
		c := &Context{}
		require.Empty(t, c.Constrain(num(), &soltype.UnknownType{}))
	})

	t.Run("a borrow is a subtype of unknown", func(t *testing.T) {
		c := &Context{}
		borrow := &soltype.RefType{Mut: false, Inner: exactObj(propElem("x", num()))}
		require.Empty(t, c.Constrain(borrow, &soltype.UnknownType{}))
	})

	t.Run("a union is a subtype of unknown", func(t *testing.T) {
		c := &Context{}
		sub := newUnion(nil, parseTypes(t, "number", "string"), false)
		require.Empty(t, c.Constrain(sub, &soltype.UnknownType{}))
	})

	t.Run("a variable sub records no upper bound", func(t *testing.T) {
		c := &Context{}
		a := c.freshVar(0)
		require.Empty(t, c.Constrain(a, &soltype.UnknownType{}))
		require.Empty(t, a.UpperBounds)
	})
}

// TestConstrainBottomRule covers `never <: super`. never is the bottom of the
// subtype lattice, so it is a subtype of every super. A variable super records no
// lower bound, since never is the join identity and would add nothing.
func TestConstrainBottomRule(t *testing.T) {
	t.Run("never is a subtype of a primitive", func(t *testing.T) {
		c := &Context{}
		require.Empty(t, c.Constrain(&soltype.NeverType{}, num()))
	})

	t.Run("never is a subtype of a union", func(t *testing.T) {
		c := &Context{}
		super := newUnion(nil, parseTypes(t, "number", "string"), false)
		require.Empty(t, c.Constrain(&soltype.NeverType{}, super))
	})

	t.Run("a variable super records no lower bound", func(t *testing.T) {
		c := &Context{}
		a := c.freshVar(0)
		require.Empty(t, c.Constrain(&soltype.NeverType{}, a))
		require.Empty(t, a.LowerBounds)
	})
}

// TestConstrainFuncVariationB covers the function-arm Variation-B check on the
// hand-built FuncType. When the super is inexact and the sub declares more params
// than the super, the super's `...` tail may pass an arg of any type at the sub's
// extra position, so soundness demands `unknown <: sub.Params[i].Type` there
// (exact-types §4.2.1.2). The extra param's required count must stay low enough to
// pass the arity gate, so it is optional here.
func TestConstrainFuncVariationB(t *testing.T) {
	// super: fn(a: number, ...) — one named param, open tail.
	super := func() *soltype.FuncType {
		return inexactFn(num(), identParam("a", num()))
	}

	t.Run("a concrete extra param is rejected by the open tail", func(t *testing.T) {
		// sub: fn(a: number, b?: number, ...). The extra param b is number, which
		// cannot accept the tail's arbitrarily-typed arg, so unknown <: number fails.
		c := &Context{}
		sub := inexactFn(num(), identParam("a", num()), optParam("b", num()))
		require.Equal(t,
			[]string{"cannot constrain unknown <: number"},
			Messages(c.Constrain(sub, super())))
	})

	t.Run("an unknown extra param accepts the open tail", func(t *testing.T) {
		// sub: fn(a: number, b?: unknown, ...). The extra param is unknown, so
		// unknown <: unknown holds and the fill is accepted.
		c := &Context{}
		sub := inexactFn(num(), identParam("a", num()), optParam("b", &soltype.UnknownType{}))
		require.Empty(t, c.Constrain(sub, super()))
	})
}
