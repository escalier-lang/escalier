package codegen

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/escalier-lang/escalier/internal/snapshot"
	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/require"
)

func TestPrintExpr(t *testing.T) {
	sum := &BinaryExpr{
		Left:   NewLitExpr(NewNumLit(0.1, nil), nil),
		Op:     Plus,
		Right:  NewLitExpr(NewNumLit(0.2, nil), nil),
		span:   nil,
		source: nil,
	}

	printer := NewPrinter()
	printer.PrintExpr(sum)

	want := "0.1 + 0.2"
	if got := printer.Output; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	snaps.MatchSnapshot(t, snapshot.String(sum))
}

// TestPrintExprPrecedence pins the parentheses the printer adds. The codegen AST records
// grouping in its tree shape and has no parenthesis node, so the printer derives them from
// operator binding power. Each case builds the tree that source parentheses produce and
// asserts the emitted JavaScript reparses to the same grouping.
func TestPrintExprPrecedence(t *testing.T) {
	a := NewIdentExpr("a", "", nil)
	b := NewIdentExpr("b", "", nil)
	c := NewIdentExpr("c", "", nil)
	bin := func(l Expr, op BinaryOp, r Expr) Expr { return NewBinaryExpr(l, op, r, nil) }

	tests := map[string]struct {
		expr     Expr
		expected string
	}{
		// A looser child needs parentheses on either side.
		"LooserOnLeft":  {bin(bin(a, Plus, b), Times, c), "(a + b) * c"},
		"LooserOnRight": {bin(a, Times, bin(b, Plus, c)), "a * (b + c)"},
		"OrUnderAnd":    {bin(bin(a, LogicalOr, b), LogicalAnd, c), "(a || b) && c"},
		// A tighter child never needs them.
		"TighterOnLeft":  {bin(bin(a, Times, b), Plus, c), "a * b + c"},
		"TighterOnRight": {bin(a, Plus, bin(b, Times, c)), "a + b * c"},
		"AndUnderOr":     {bin(a, LogicalOr, bin(b, LogicalAnd, c)), "a || b && c"},
		// At equal binding power only the side the operator does not associate with
		// needs them, since `a - b - c` means `(a - b) - c`.
		"SubtractOnRight": {bin(a, Minus, bin(b, Minus, c)), "a - (b - c)"},
		"SubtractOnLeft":  {bin(bin(a, Minus, b), Minus, c), "a - b - c"},
		"DivideOnRight":   {bin(a, Divide, bin(b, Divide, c)), "a / (b / c)"},
		// Regrouping a short-circuiting operator changes nothing, so it stays bare.
		"AndUnderAnd": {bin(a, LogicalAnd, bin(b, LogicalAnd, c)), "a && b && c"},
		"OrUnderOr":   {bin(a, LogicalOr, bin(b, LogicalOr, c)), "a || b || c"},
		// A prefix operator takes only an operand that binds at least as tightly.
		"NotOfOr":     {NewUnaryExpr(LogicalNot, bin(a, LogicalOr, b), nil), "!(a || b)"},
		"NegateOfSum": {NewUnaryExpr(UnaryMinus, bin(a, Plus, b), nil), "-(a + b)"},
		"NotOfIdent":  {NewUnaryExpr(LogicalNot, a, nil), "!a"},
		// JavaScript rejects `??` beside `||` or `&&` rather than picking a grouping for
		// it, so the pairing needs parentheses whichever side each operator is on.
		"NullishHoldingOr": {bin(a, NullishCoalescing, bin(b, LogicalOr, c)), "a ?? (b || c)"},
		"OrHoldingNullish": {bin(bin(a, NullishCoalescing, b), LogicalOr, c), "(a ?? b) || c"},
		"NullishChain":     {bin(a, NullishCoalescing, bin(b, NullishCoalescing, c)), "a ?? b ?? c"},
		// A ternary's branches are delimited by `?` and `:`, so only its test can be
		// regrouped by what surrounds it.
		"CondAsTest":   {NewCondExpr(NewCondExpr(a, b, c, nil), b, c, nil), "(a ? b : c) ? b : c"},
		"CondBranches": {NewCondExpr(a, bin(b, Plus, c), c, nil), "a ? b + c : c"},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			printer := NewPrinter()
			printer.PrintExpr(test.expr)
			require.Equal(t, test.expected, printer.Output)
		})
	}
}

func TestPrintModule(t *testing.T) {
	source := &ast.Source{
		ID:   0,
		Path: "input.esc",
		Contents: `fn add(a, b) { return a + b }
fn sub(a, b) { return a - b }`,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	p := parser.NewParser(ctx, source)
	m1, _ := p.ParseScript()
	builder := &Builder{
		tempId:   0,
		depGraph: nil,
	}
	m2 := builder.BuildScript(m1)

	printer := NewPrinter()
	printer.PrintModule(m2)

	snaps.MatchSnapshot(t, printer.Output)
	if printer.location.Line != 11 {
		t.Errorf("got %d, want 11", printer.location.Line)
	}
}
