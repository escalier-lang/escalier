package simplesub

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// test helpers for building SimpleTypes directly
func num() *Primitive     { return &Primitive{name: "number"} }
func str() *Primitive     { return &Primitive{name: "string"} }
func boolean() *Primitive { return &Primitive{name: "boolean"} }

func fn1(param, ret SimpleType) *Function {
	return &Function{params: []SimpleType{param}, ret: ret}
}

// TestInferIdentity is the M0 acceptance case: the identity function infers to
// the generalized fn <T0>(x: T0) -> T0, rendered by the production printer.
func TestInferIdentity(t *testing.T) {
	id := &Lam{Param: "x", Body: &Var{Name: "x"}}
	got, errs := Render(id)
	require.Empty(t, errs)
	require.Equal(t, "fn <T0>(x: T0) -> T0", got)
}

// TestConstrain exercises the constrain primitive directly (no coalescing), so
// it does not depend on the M1 simplification pass.
func TestConstrain(t *testing.T) {
	tests := []struct {
		name     string
		lhs, rhs SimpleType
		wantErr  bool
	}{
		{"prim equal", boolean(), boolean(), false},
		{"prim mismatch", boolean(), num(), true},
		{"func equal", fn1(num(), num()), fn1(num(), num()), false},
		// parameters are contravariant: (number)->number <: (string)->number
		// requires string <: number, which fails.
		{"func param contravariant fail", fn1(num(), num()), fn1(str(), num()), true},
		// return is covariant: (number)->number <: (number)->string requires
		// number <: string, which fails.
		{"func return covariant fail", fn1(num(), num()), fn1(num(), str()), true},
		{"func arity mismatch", fn1(num(), num()),
			&Function{params: []SimpleType{num(), num()}, ret: num()}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := NewInferer()
			errs := in.Constrain(tt.lhs, tt.rhs)
			if tt.wantErr {
				require.NotEmpty(t, errs)
			} else {
				require.Empty(t, errs)
			}
		})
	}
}

// TestConstrainVariablePropagation checks that a bound recorded on a variable is
// enforced against bounds added later (the core of bound propagation): once
// v <: number, asserting boolean <: v must fail via boolean <: number.
func TestConstrainVariablePropagation(t *testing.T) {
	in := NewInferer()
	v := in.freshVar()
	require.Empty(t, in.Constrain(v, num())) // v <: number  (number is an upper bound of v)
	require.NotEmpty(t, in.Constrain(boolean(), v))
}

// TestApplyIdentityRawUnsimplified documents a known M0 limitation: applying the
// identity to a boolean yields a result variable whose single lower bound is
// boolean. Without the M1 simplification pass, coalescing renders the variable
// alongside its bound as a union. M1 will reduce this to `boolean`.
func TestApplyIdentityRawUnsimplified(t *testing.T) {
	app := &App{
		Fn:  &Lam{Param: "x", Body: &Var{Name: "x"}},
		Arg: &Lit{Prim: "boolean"},
	}
	got, errs := Render(app)
	require.Empty(t, errs)
	require.Equal(t, "T0 | boolean", got)
}
