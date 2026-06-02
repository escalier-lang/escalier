package simplesub

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// outer = (x) => { let inner = (y) => x(y); let a = inner(1); let b = inner("hi"); return [a, b] }
func TestMyExample(t *testing.T) {
	outer := &Lam{Params: []string{"x"}, Body: &Let{
		Name: "inner", Rhs: lam("y", &App{Fn: vr("x"), Arg: vr("y")}),
		Body: &Let{
			Name: "a", Rhs: &App{Fn: vr("inner"), Arg: litNum(1)},
			Body: &Let{
				Name: "b", Rhs: &App{Fn: vr("inner"), Arg: litStr("hi")},
				Body: &TupleExpr{Elems: []Term{vr("a"), vr("b")}},
			},
		},
	}}
	got, errs := Render(outer)
	require.Empty(t, errs)
	require.Equal(t, `fn <T0>(x: fn (x0: 1 | "hi") -> T0) -> [T0, T0]`, got)
}
