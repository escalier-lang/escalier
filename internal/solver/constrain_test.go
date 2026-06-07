package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// Messages adapts a slice of SolverError to a slice of their rendered messages
// so table-driven tests read naturally.
func Messages(errs []SolverError) []string {
	if len(errs) == 0 {
		return nil
	}
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Message()
	}
	return msgs
}

func num() *soltype.PrimType   { return &soltype.PrimType{Prim: soltype.NumPrim} }
func str() *soltype.PrimType   { return &soltype.PrimType{Prim: soltype.StrPrim} }
func boolT() *soltype.PrimType { return &soltype.PrimType{Prim: soltype.BoolPrim} }

func numLit(v float64) *soltype.LitType { return &soltype.LitType{Lit: &soltype.NumLit{Value: v}} }
func strLit(v string) *soltype.LitType  { return &soltype.LitType{Lit: &soltype.StrLit{Value: v}} }

func identParam(name string, t soltype.Type) *soltype.FuncParam {
	return &soltype.FuncParam{Pattern: &soltype.IdentPat{Name: name}, Type: t}
}

func optParam(name string, t soltype.Type) *soltype.FuncParam {
	return &soltype.FuncParam{Pattern: &soltype.IdentPat{Name: name}, Type: t, Optional: true}
}

// exactFn / inexactFn build function types so the accept-set tests read at a glance
// which arm they exercise. Exact is the zero value of Inexact, so exactFn sets no
// flag; inexactFn sets Inexact.
func exactFn(ret soltype.Type, params ...*soltype.FuncParam) *soltype.FuncType {
	return &soltype.FuncType{Params: params, Ret: ret}
}

func inexactFn(ret soltype.Type, params ...*soltype.FuncParam) *soltype.FuncType {
	return &soltype.FuncType{Params: params, Ret: ret, Inexact: true}
}

func TestConstrainPrim(t *testing.T) {
	tests := []struct {
		name string
		lhs  soltype.Type
		rhs  soltype.Type
		want []string
	}{
		{"number <: number", num(), num(), nil},
		{"string <: string", str(), str(), nil},
		{"number <: string", num(), str(), []string{"cannot constrain number <: string"}},
		{"boolean <: number", boolT(), num(), []string{"cannot constrain boolean <: number"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Context{}
			errs := c.Constrain(tt.lhs, tt.rhs)
			require.Equal(t, tt.want, Messages(errs))
		})
	}
}

func TestConstrainPrimMismatchStructure(t *testing.T) {
	c := &Context{}
	lhs, rhs := num(), str()
	errs := c.Constrain(lhs, rhs)
	require.Len(t, errs, 1)
	cc, ok := errs[0].(*CannotConstrainError)
	require.True(t, ok)
	require.Same(t, soltype.Type(lhs), cc.LHS)
	require.Same(t, soltype.Type(rhs), cc.RHS)
}

func TestConstrainLiteral(t *testing.T) {
	tests := []struct {
		name string
		lhs  soltype.Type
		rhs  soltype.Type
		want []string
	}{
		{"5 <: number", numLit(5), num(), nil},
		{`"hello" <: string`, strLit("hello"), str(), nil},
		{"5 <: 5", numLit(5), numLit(5), nil},
		{"5 <: string", numLit(5), str(), []string{"cannot constrain 5 <: string"}},
		{"5 <: 6", numLit(5), numLit(6), []string{"cannot constrain 5 <: 6"}},
		// Regression: float64 literals must render at 64-bit precision, not
		// float32 (which would garble these to 0.12345679 / 16777216).
		{"high-precision decimal", numLit(0.123456789), str(),
			[]string{"cannot constrain 0.123456789 <: string"}},
		{"large integer past float32 mantissa", numLit(16777217), str(),
			[]string{"cannot constrain 16777217 <: string"}},
		{`"a" <: "b"`, strLit("a"), strLit("b"), []string{`cannot constrain "a" <: "b"`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Context{}
			errs := c.Constrain(tt.lhs, tt.rhs)
			require.Equal(t, tt.want, Messages(errs))
		})
	}
}

func TestConstrainFunctionVariance(t *testing.T) {
	// (number) -> number  <:  (number) -> number
	t.Run("same shape", func(t *testing.T) {
		c := &Context{}
		f1 := exactFn(num(), identParam("x", num()))
		f2 := exactFn(num(), identParam("x", num()))
		require.Empty(t, c.Constrain(f1, f2))
	})

	// Contravariant params: (number) -> number <: (5) -> number requires
	// 5 <: number on the param (rhs param <: lhs param), which holds.
	t.Run("contravariant params (ok)", func(t *testing.T) {
		c := &Context{}
		f1 := exactFn(num(), identParam("x", num()))
		f2 := exactFn(num(), identParam("x", numLit(5)))
		require.Empty(t, c.Constrain(f1, f2))
	})

	// Contravariant params: (5) -> number <: (number) -> number requires
	// number <: 5, which fails.
	t.Run("contravariant params (fail)", func(t *testing.T) {
		c := &Context{}
		f1 := exactFn(num(), identParam("x", numLit(5)))
		f2 := exactFn(num(), identParam("x", num()))
		require.Equal(t, []string{"cannot constrain number <: 5"}, Messages(c.Constrain(f1, f2)))
	})

	// Covariant return: (number) -> 5 <: (number) -> number requires 5 <: number.
	t.Run("covariant return (ok)", func(t *testing.T) {
		c := &Context{}
		f1 := exactFn(numLit(5), identParam("x", num()))
		f2 := exactFn(num(), identParam("x", num()))
		require.Empty(t, c.Constrain(f1, f2))
	})
}

// TestConstrainFunctionAcceptSet exercises the PR4 accept-set rule (#677 §4.2.1):
// G <: F iff accept(G) ⊇ accept(F), with params contravariant and return covariant.
// Read F (the RHS) as the callback slot.
func TestConstrainFunctionAcceptSet(t *testing.T) {
	// Two EXACT functions relate by the old same-arity rule: accept is [n, n] on both
	// sides, so ⊇ forces equal arity.
	t.Run("exact same arity ok", func(t *testing.T) {
		c := &Context{}
		require.Empty(t, c.Constrain(
			exactFn(num(), identParam("x", num())),
			exactFn(num(), identParam("x", num())),
		))
	})

	// An exact supplier cannot fill a WIDER exact slot: accept [1,1] does not contain
	// the slot's count 2 (upper-bound failure, hiG < hiF).
	t.Run("exact fn(x) rejected by fn(x, y) slot", func(t *testing.T) {
		c := &Context{}
		g := exactFn(num(), identParam("x", num()))
		f := exactFn(num(), identParam("x", num()), identParam("y", num()))
		errs := c.Constrain(g, f)
		require.Equal(t, []string{"cannot constrain function of arity 1 <: function of arity 2"}, Messages(errs))
		fa, ok := errs[0].(*FuncArityMismatchError)
		require.True(t, ok)
		require.Same(t, g, fa.LHS)
		require.Same(t, f, fa.RHS)
	})

	// The #677 callback matrix for an exact fn(x, y) slot: fn(x, y), fn(x, ...), and
	// fn(...) are all accepted; exact fn(x) (too narrow upper bound) and a 3-param
	// function (demands more than the slot supplies) are rejected.
	slot := func() *soltype.FuncType { return exactFn(num(), identParam("x", num()), identParam("y", num())) }
	t.Run("exact fn(x, y) fills fn(x, y) slot", func(t *testing.T) {
		c := &Context{}
		require.Empty(t, c.Constrain(exactFn(num(), identParam("x", num()), identParam("y", num())), slot()))
	})
	t.Run("inexact fn(x, ...) fills fn(x, y) slot", func(t *testing.T) {
		c := &Context{}
		// accept(G) = [1, ∞) ⊇ accept(slot) = [2, 2]; the slot's arg 2 is tolerated,
		// the single shared param checked contravariantly.
		require.Empty(t, c.Constrain(inexactFn(num(), identParam("x", num())), slot()))
	})
	t.Run("inexact fn(...) fills fn(x, y) slot", func(t *testing.T) {
		c := &Context{}
		// accept(G) = [0, ∞) ⊇ [2, 2]: a zero-param inexact function fills any exact
		// slot whose required count it meets. This is the case exactness exists to permit.
		require.Empty(t, c.Constrain(inexactFn(num()), slot()))
	})
	t.Run("exact fn(x) rejected by fn(x, y) slot (matrix)", func(t *testing.T) {
		c := &Context{}
		require.Equal(t,
			[]string{"cannot constrain function of arity 1 <: function of arity 2"},
			Messages(c.Constrain(exactFn(num(), identParam("x", num())), slot())))
	})
	t.Run("3-param fn rejected by fn(x, y) slot", func(t *testing.T) {
		c := &Context{}
		g := exactFn(num(), identParam("x", num()), identParam("y", num()), identParam("z", num()))
		// accept(G) = [3, 3]; the slot supplies 2, below G's required 3 → lower-bound
		// failure (loG > loF).
		require.Equal(t,
			[]string{"cannot constrain function of arity 3 <: function of arity 2"},
			Messages(c.Constrain(g, slot())))
	})

	// Exact </: inexact: an exact function's finite upper bound cannot cover an
	// inexact slot's ∞ (the asymmetry §4.2.1.1 explains).
	t.Run("exact fn(x) rejected by inexact fn(x, ...) slot", func(t *testing.T) {
		c := &Context{}
		require.Equal(t,
			[]string{"cannot constrain function of arity 1 <: function of arity 1"},
			Messages(c.Constrain(exactFn(num(), identParam("x", num())), inexactFn(num(), identParam("x", num())))))
	})

	// Inexact <: inexact: the classic "fewer params is okay" rule — both upper bounds
	// are ∞, the supplier just needs to handle the consumer's required params.
	t.Run("inexact fn(x, ...) fills inexact fn(x, y, ...) slot", func(t *testing.T) {
		c := &Context{}
		require.Empty(t, c.Constrain(
			inexactFn(num(), identParam("x", num())),
			inexactFn(num(), identParam("x", num()), identParam("y", num())),
		))
	})

	// Optional params lower `required` without changing arity: exact fn(a, b?)
	// (accept [1, 2]) fills the narrower exact fn(a) slot (accept [1, 1]) — the slot
	// never supplies the optional argument.
	t.Run("exact fn(a, b?) fills fn(a) slot", func(t *testing.T) {
		c := &Context{}
		require.Empty(t, c.Constrain(
			exactFn(num(), identParam("a", num()), optParam("b", num())),
			exactFn(num(), identParam("a", num())),
		))
	})
}

func TestConstrainTuple(t *testing.T) {
	// [number, string] <: [number, string]
	t.Run("same length covariant", func(t *testing.T) {
		c := &Context{}
		t1 := &soltype.TupleType{Elems: []soltype.Type{num(), str()}}
		t2 := &soltype.TupleType{Elems: []soltype.Type{num(), str()}}
		require.Empty(t, c.Constrain(t1, t2))
	})

	// [5, "a"] <: [number, string] (element-wise covariant, literals widen)
	t.Run("covariant elements", func(t *testing.T) {
		c := &Context{}
		t1 := &soltype.TupleType{Elems: []soltype.Type{numLit(5), strLit("a")}}
		t2 := &soltype.TupleType{Elems: []soltype.Type{num(), str()}}
		require.Empty(t, c.Constrain(t1, t2))
	})

	t.Run("element mismatch", func(t *testing.T) {
		c := &Context{}
		t1 := &soltype.TupleType{Elems: []soltype.Type{num(), num()}}
		t2 := &soltype.TupleType{Elems: []soltype.Type{num(), str()}}
		require.Equal(t, []string{"cannot constrain number <: string"}, Messages(c.Constrain(t1, t2)))
	})

	t.Run("length mismatch", func(t *testing.T) {
		c := &Context{}
		t1 := &soltype.TupleType{Elems: []soltype.Type{num(), str()}}
		t2 := &soltype.TupleType{Elems: []soltype.Type{num()}}
		errs := c.Constrain(t1, t2)
		require.Equal(t, []string{"cannot constrain tuple of length 2 <: tuple of length 1"}, Messages(errs))
		tl, ok := errs[0].(*TupleLengthMismatchError)
		require.True(t, ok)
		require.Same(t, t1, tl.LHS)
		require.Same(t, t2, tl.RHS)
	})
}

func TestConstrainVoid(t *testing.T) {
	c := &Context{}
	require.Empty(t, c.Constrain(&soltype.Void{}, &soltype.Void{}))
	require.Equal(t, []string{"cannot constrain void <: number"},
		Messages(c.Constrain(&soltype.Void{}, num())))
}

func TestConstrainVariableBinding(t *testing.T) {
	// α <: number records number as an upper bound of α.
	c := &Context{}
	a := c.freshVar(0)
	n := num()
	require.Empty(t, c.Constrain(a, n))
	require.Len(t, a.UpperBounds, 1)
	require.Same(t, soltype.Type(n), a.UpperBounds[0])

	// 5 <: α records 5 as a lower bound of α.
	five := numLit(5)
	require.Empty(t, c.Constrain(five, a))
	require.Len(t, a.LowerBounds, 1)
	require.Same(t, soltype.Type(five), a.LowerBounds[0])
}

func TestConstrainTransitivePropagation(t *testing.T) {
	// Constrain α <: number, then 5 <: α; the second constraint must propagate
	// the existing upper bound (number), checking 5 <: number — which holds.
	c := &Context{}
	a := c.freshVar(0)
	require.Empty(t, c.Constrain(a, num()))
	require.Empty(t, c.Constrain(numLit(5), a))

	// Now make the propagation fail: β <: number, then "hello" <: β must
	// propagate "hello" <: number and fail.
	c2 := &Context{}
	b := c2.freshVar(0)
	require.Empty(t, c2.Constrain(b, num()))
	require.Equal(t, []string{`cannot constrain "hello" <: number`},
		Messages(c2.Constrain(strLit("hello"), b)))
}

func TestConstrainExtrusion(t *testing.T) {
	// When a lower-level variable is constrained against a higher-level type,
	// that type must be extruded down to the variable's level: the lower var's
	// bound list must capture a FRESH level-0 variable, not the original
	// higher-level variable (no cross-level leakage).
	c := &Context{}
	low := c.freshVar(0)  // level 0
	high := c.freshVar(1) // level 1

	// low <: high. low (level 0) is the lhs variable; high (level 1) lives above
	// low's level, so it is extruded down to a fresh level-0 variable before
	// being recorded as low's upper bound.
	require.Empty(t, c.Constrain(low, high))

	require.Len(t, low.UpperBounds, 1)
	bound, ok := low.UpperBounds[0].(*soltype.TypeVarType)
	require.True(t, ok)
	require.NotSame(t, high, bound)       // the original high var did not leak in
	require.Equal(t, 0, bound.Level)      // it was copied down to low's level
	require.Greater(t, bound.ID, high.ID) // and it is a freshly-allocated var

	// The extruded var is wired back to the original through high's bound list.
	require.Len(t, high.LowerBounds, 1)
	require.Same(t, soltype.Type(bound), high.LowerBounds[0])
}

func TestConstrainExtrusionBothPolarities(t *testing.T) {
	// Extruding a function that mentions the same higher-level variable in both
	// a contravariant (param) and a covariant (return) position must produce
	// TWO distinct fresh vars — one per polarity, with opposite bound wiring.
	// A cache keyed by var ID alone would reuse the first-seen polarity's copy
	// in the other position; the (id, polarity) key keeps them distinct.
	c := &Context{}
	low := c.freshVar(0) // level 0
	a := c.freshVar(1)   // level 1, used in both positions of fn
	fn := &soltype.FuncType{
		Params: []*soltype.FuncParam{identParam("x", a)},
		Ret:    a,
	}

	// low <: fn. fn lives above low's level, so it is extruded down; `a` is
	// reached at Positive (return) and Negative (param) polarity.
	require.Empty(t, c.Constrain(low, fn))

	require.Len(t, low.UpperBounds, 1)
	extruded, ok := low.UpperBounds[0].(*soltype.FuncType)
	require.True(t, ok)

	paramVar, ok := extruded.Params[0].Type.(*soltype.TypeVarType)
	require.True(t, ok)
	retVar, ok := extruded.Ret.(*soltype.TypeVarType)
	require.True(t, ok)

	// Distinct fresh vars, both copied down to low's level.
	require.NotSame(t, paramVar, retVar)
	require.Equal(t, 0, paramVar.Level)
	require.Equal(t, 0, retVar.Level)
}
