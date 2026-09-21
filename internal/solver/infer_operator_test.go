package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// --- Binary operator application ---
//
// An operator application resolves the operator's prelude binding, constrains each
// operand against the parameter in its position, and yields the signature's return
// type. The assignment form `a = expr` is not an application. Its tests live in
// infer_assign_test.go.

// Every operator addOperatorBindings seeds yields the return type its signature
// declares.
func TestInferBinaryYieldsDeclaredReturn(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{name: "Add", src: `val x = 1 + 2`, want: "number"},
		{name: "Subtract", src: `val x = 1 - 2`, want: "number"},
		{name: "Multiply", src: `val x = 1 * 2`, want: "number"},
		{name: "Divide", src: `val x = 1 / 2`, want: "number"},
		{name: "LessThan", src: `val x = 1 < 2`, want: "boolean"},
		{name: "LessThanEqual", src: `val x = 1 <= 2`, want: "boolean"},
		{name: "GreaterThan", src: `val x = 1 > 2`, want: "boolean"},
		{name: "GreaterThanEqual", src: `val x = 1 >= 2`, want: "boolean"},
		{name: "Equal", src: `val x = 1 == 2`, want: "boolean"},
		{name: "NotEqual", src: `val x = 1 != 2`, want: "boolean"},
		{name: "LogicalAnd", src: `val x = true && false`, want: "boolean"},
		{name: "LogicalOr", src: `val x = true || false`, want: "boolean"},
		{name: "Concatenation", src: `val x = "a" ++ "b"`, want: "string"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values, _, errs := inferSource(t, test.src)
			require.Empty(t, errs)
			require.Equal(t, test.want, values["x"])
		})
	}
}

// The equality operators take `unknown` parameters, which every type is a subtype
// of, so comparing operands of different types checks. Neither operand constrains
// the other, and the result is `boolean` either way.
func TestInferBinaryEqualityAcceptsAnyOperands(t *testing.T) {
	values, _, errs := inferSource(t, `val x = 1 == "a"
val y = true != 2`)
	require.Empty(t, errs)
	require.Equal(t, "boolean", values["x"])
	require.Equal(t, "boolean", values["y"])
}

// An operand is an ordinary expression, so a nested application and an identifier
// both flow through. `1 + 2 * 3` parses as `1 + (2 * 3)`, so the inner `number`
// result fills the outer `+`'s right parameter.
func TestInferBinaryOperandExpressions(t *testing.T) {
	t.Run("nested application", func(t *testing.T) {
		values, _, errs := inferSource(t, `val x = 1 + 2 * 3`)
		require.Empty(t, errs)
		require.Equal(t, "number", values["x"])
	})
	t.Run("identifier operand", func(t *testing.T) {
		values, _, errs := inferSource(t, `val n = 5
val x = n + 1`)
		require.Empty(t, errs)
		require.Equal(t, "number", values["x"])
	})
}

// Constraining an un-annotated parameter against an operator's parameter gives the
// parameter its type, so the function's signature is inferred from the operator
// alone.
func TestInferBinaryConstrainsUnannotatedParam(t *testing.T) {
	values, _, errs := inferSource(t, `fn f(a) { return a + 1 }`)
	require.Empty(t, errs)
	require.Equal(t, "fn (a: number) -> number", values["f"])
}

// A rejected operand blames that operand, not the whole expression. The operator's
// parameter type comes from the prelude, which records no AST node, so the error
// has no related span.
func TestInferBinaryRejectedOperandBlamesOperand(t *testing.T) {
	t.Run("left operand", func(t *testing.T) {
		src := `val x = "a" * 2`
		_, _, errs := inferSource(t, src)
		requireBlame(t, src, errs, `1:9-1:12: cannot constrain "a" <: number`, `"a"`)
	})
	t.Run("right operand", func(t *testing.T) {
		src := `val x = 2 * "a"`
		_, _, errs := inferSource(t, src)
		requireBlame(t, src, errs, `1:13-1:16: cannot constrain "a" <: number`, `"a"`)
	})
	t.Run("logical operand", func(t *testing.T) {
		src := `val x = 1 && true`
		_, _, errs := inferSource(t, src)
		requireBlame(t, src, errs, "1:9-1:10: cannot constrain 1 <: boolean", "1")
	})
}

// Each operand is constrained on its own, so two rejected operands report two
// errors rather than collapsing into one about the expression.
func TestInferBinaryReportsBothRejectedOperands(t *testing.T) {
	_, _, errs := inferSource(t, `val x = "a" * "b"`)
	require.Len(t, errs, 2)
	require.Equal(t, `cannot constrain "a" <: number`, errs[0].Message())
	require.Equal(t, `cannot constrain "b" <: number`, errs[1].Message())
}

// ast.Modulo and ast.NullishCoalescing have no prelude binding. The parser emits
// neither, so this is reachable only from a hand-built AST. The report names the
// operator rather than the node kind. Both operands are still typed first, so the
// unknown identifier in the left operand is reported alongside it.
func TestInferBinaryUnboundOperator(t *testing.T) {
	c := newTestChecker()
	e := ast.NewBinary(identExpr("nope"), numExpr(2), ast.Modulo, testSpan())

	got := c.inferExpr(NewScope(), 0, e)
	require.IsType(t, &soltype.ErrorType{}, got)
	require.Len(t, c.errs, 2)
	require.Equal(t, "Unknown identifier: nope", c.errs[0].Message())
	require.Equal(t, "Unsupported: operator %", c.errs[1].Message())
	require.Equal(t, testSpan(), c.errs[1].Span())
}

// A node with a nil operand is hand-built only. The real parser substitutes
// ast.NewError for an operand it could not read. Typing one must not panic, and it
// blames the whole expression because there is no operand to blame.
func TestInferBinaryNilOperandDoesNotPanic(t *testing.T) {
	c := newTestChecker()
	e := ast.NewBinary(numExpr(1), nil, ast.Plus, testSpan())
	require.NotPanics(t, func() {
		c.inferExpr(NewScope(), 0, e)
		for _, err := range c.errs { // force lazy Span()/Message()
			_ = err.Span()
			_ = err.Message()
		}
	})
	require.Len(t, c.errs, 1)
	require.Equal(t, "Unsupported: BinaryExpr", c.errs[0].Message())
}
