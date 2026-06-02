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
		f1 := &soltype.FuncType{Params: []*soltype.FuncParam{identParam("x", num())}, Ret: num()}
		f2 := &soltype.FuncType{Params: []*soltype.FuncParam{identParam("x", num())}, Ret: num()}
		require.Empty(t, c.Constrain(f1, f2))
	})

	// Contravariant params: (number) -> number <: (5) -> number requires
	// 5 <: number on the param (rhs param <: lhs param), which holds.
	t.Run("contravariant params (ok)", func(t *testing.T) {
		c := &Context{}
		f1 := &soltype.FuncType{Params: []*soltype.FuncParam{identParam("x", num())}, Ret: num()}
		f2 := &soltype.FuncType{Params: []*soltype.FuncParam{identParam("x", numLit(5))}, Ret: num()}
		require.Empty(t, c.Constrain(f1, f2))
	})

	// Contravariant params: (5) -> number <: (number) -> number requires
	// number <: 5, which fails.
	t.Run("contravariant params (fail)", func(t *testing.T) {
		c := &Context{}
		f1 := &soltype.FuncType{Params: []*soltype.FuncParam{identParam("x", numLit(5))}, Ret: num()}
		f2 := &soltype.FuncType{Params: []*soltype.FuncParam{identParam("x", num())}, Ret: num()}
		require.Equal(t, []string{"cannot constrain number <: 5"}, Messages(c.Constrain(f1, f2)))
	})

	// Covariant return: (number) -> 5 <: (number) -> number requires 5 <: number.
	t.Run("covariant return (ok)", func(t *testing.T) {
		c := &Context{}
		f1 := &soltype.FuncType{Params: []*soltype.FuncParam{identParam("x", num())}, Ret: numLit(5)}
		f2 := &soltype.FuncType{Params: []*soltype.FuncParam{identParam("x", num())}, Ret: num()}
		require.Empty(t, c.Constrain(f1, f2))
	})
}

func TestConstrainFunctionArity(t *testing.T) {
	// Fewer-params-is-subtype: arity 1 <: arity 2 is OK.
	t.Run("fewer params is subtype", func(t *testing.T) {
		c := &Context{}
		f1 := &soltype.FuncType{Params: []*soltype.FuncParam{identParam("x", num())}, Ret: num()}
		f2 := &soltype.FuncType{
			Params: []*soltype.FuncParam{identParam("x", num()), identParam("y", num())},
			Ret:    num(),
		}
		require.Empty(t, c.Constrain(f1, f2))
	})

	// More params is NOT a subtype: arity 2 <: arity 1 fails.
	t.Run("more params is not subtype", func(t *testing.T) {
		c := &Context{}
		f1 := &soltype.FuncType{
			Params: []*soltype.FuncParam{identParam("x", num()), identParam("y", num())},
			Ret:    num(),
		}
		f2 := &soltype.FuncType{Params: []*soltype.FuncParam{identParam("x", num())}, Ret: num()}
		errs := c.Constrain(f1, f2)
		require.Equal(t, []string{"cannot constrain function of arity 2 <: function of arity 1"}, Messages(errs))
		fa, ok := errs[0].(*FuncArityMismatchError)
		require.True(t, ok)
		require.Same(t, f1, fa.LHS)
		require.Same(t, f2, fa.RHS)
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
	require.Empty(t, c.Constrain(numLit(5), a))
	require.Len(t, a.LowerBounds, 1)
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
