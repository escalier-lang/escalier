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
		// A receiver binds tighter than every operator, so an operator expression that a
		// call, an index, or a member access reads from needs parentheses.
		"SumAsMemberObject": {
			NewMemberExpr(bin(a, Plus, b), NewIdentifier("c", nil), false, nil),
			"(a + b).c",
		},
		"SumAsIndexObject": {
			NewIndexExpr(bin(a, Plus, b), c, false, nil),
			"(a + b)[c]",
		},
		"SumAsCallee": {
			NewCallExpr(bin(a, Plus, b), []Expr{c}, false, nil),
			"(a + b)(c)",
		},
		"NegationAsMemberObject": {
			NewMemberExpr(NewUnaryExpr(UnaryMinus, a, nil), NewIdentifier("c", nil), false, nil),
			"(-a).c",
		},
		"AwaitAsMemberObject": {
			NewMemberExpr(NewAwaitExpr(a, nil), NewIdentifier("c", nil), false, nil),
			"(await a).c",
		},
		"CondAsCallee": {
			NewCallExpr(NewCondExpr(a, b, c, nil), []Expr{}, false, nil),
			"(a ? b : c)()",
		},
		// A receiver that carries no operator of its own is already grouped.
		"MemberChain": {
			NewMemberExpr(NewMemberExpr(a, NewIdentifier("b", nil), false, nil), NewIdentifier("c", nil), false, nil),
			"a.b.c",
		},
		"CallOfMember": {
			NewCallExpr(NewMemberExpr(a, NewIdentifier("b", nil), false, nil), []Expr{c}, false, nil),
			"a.b(c)",
		},
		// An argument and an index are delimited by their own brackets, so an operator
		// expression in either position stays bare.
		"SumAsArgument": {
			NewCallExpr(a, []Expr{bin(b, Plus, c)}, false, nil),
			"a(b + c)",
		},
		"SumAsIndex": {
			NewIndexExpr(a, bin(b, Plus, c), false, nil),
			"a[b + c]",
		},
		"SumAsNewCallee": {
			NewNewExpr(bin(a, Plus, b), []Expr{c}, nil),
			"new (a + b)(c)",
		},
		"SumAsTemplateTag": {
			NewTaggedTemplateLitExpr(bin(a, Plus, b), []string{"x"}, []Expr{}, nil),
			"(a + b)`x`",
		},
		// A number literal takes the following `.` as its own decimal point, so a bare
		// `5.toFixed(2)` would be a JavaScript syntax error.
		"NumberAsMemberObject": {
			NewMemberExpr(NewLitExpr(NewNumLit(5, nil), nil), NewIdentifier("toFixed", nil), false, nil),
			"(5).toFixed",
		},
		// A string literal has no such reading, so it stays bare.
		"StringAsMemberObject": {
			NewMemberExpr(NewLitExpr(NewStrLit("a", nil), nil), NewIdentifier("length", nil), false, nil),
			`"a".length`,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			printer := NewPrinter()
			printer.PrintExpr(test.expr)
			require.Equal(t, test.expected, printer.Output)
		})
	}
}

// TestPrintExprStmtAmbiguousStart pins the parentheses an expression statement adds. A
// statement beginning with `function` starts a function declaration and one beginning with
// `{` starts a block, so either emitted bare is a JavaScript syntax error rather than the
// expression the tree holds. Only the leftmost token decides, which is why an expression
// that merely contains one of those forms stays bare.
func TestPrintExprStmtAmbiguousStart(t *testing.T) {
	a := NewIdentExpr("a", "", nil)
	fn := NewFuncExpr(nil, nil, FuncExprOptions{}, nil)
	obj := NewObjectExpr([]ObjExprElem{}, nil)
	member := func(recv Expr, name string) Expr {
		return NewMemberExpr(recv, NewIdentifier(name, nil), false, nil)
	}

	tests := map[string]struct {
		expr     Expr
		expected string
	}{
		"ImmediatelyInvokedFunction": {
			NewCallExpr(fn, []Expr{}, false, nil),
			"(function () {\n}());",
		},
		"MemberOnFunction": {
			member(fn, "call"),
			"(function () {\n}.call);",
		},
		"MemberOnObjectLiteral": {
			member(obj, "x"),
			"({}.x);",
		},
		// The leftmost token is what JavaScript reads, so an ambiguous form reached
		// through a chain of receivers still opens the statement.
		"ObjectLiteralLeftmostInSum": {
			NewBinaryExpr(member(obj, "x"), Plus, a, nil),
			"({}.x + a);",
		},
		// A statement that opens on anything else needs nothing, which is why
		// `a + function () {}()` is legal JavaScript as written.
		"FunctionAfterAnIdentifier": {
			NewBinaryExpr(a, Plus, NewCallExpr(fn, []Expr{}, false, nil), nil),
			"a + function () {\n}();",
		},
		"PlainCall": {
			NewCallExpr(a, []Expr{}, false, nil),
			"a();",
		},
		"ArrayLiteral": {
			NewArrayExpr([]Expr{a}, nil),
			"[a];",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			printer := NewPrinter()
			printer.PrintStmt(&ExprStmt{Expr: test.expr})
			require.Equal(t, test.expected, printer.Output)
		})
	}
}

// TestPrintTypeAnnPrecedence pins the parentheses the type-annotation printer adds. The
// codegen AST records grouping in its tree shape and has no parenthesis node, so a member
// that binds looser than the operator holding it has to be wrapped. Each case builds the
// tree that source parentheses produce and asserts the emitted TypeScript reparses to the
// same grouping.
func TestPrintTypeAnnPrecedence(t *testing.T) {
	num := func() TypeAnn { return NewNumberTypeAnn(nil) }
	str := func() TypeAnn { return NewStringTypeAnn(nil) }
	boolean := func() TypeAnn { return NewBooleanTypeAnn(nil) }
	ref := func(name string) TypeAnn { return NewRefTypeAnn(name, nil) }
	fnTo := func(ret TypeAnn) TypeAnn { return NewFuncTypeAnn(nil, nil, ret, nil, nil) }

	tests := map[string]struct {
		typeAnn  TypeAnn
		expected string
	}{
		// TypeScript binds `&` tighter than `|`, so a union inside an intersection has to
		// be wrapped to keep its members from being pulled apart.
		"UnionInsideIntersection": {
			NewIntersectionTypeAnn([]TypeAnn{
				NewUnionTypeAnn([]TypeAnn{num(), str()}),
				boolean(),
			}),
			"(number | string) & boolean",
		},
		"UnionAsSecondIntersectionMember": {
			NewIntersectionTypeAnn([]TypeAnn{
				boolean(),
				NewUnionTypeAnn([]TypeAnn{num(), str()}),
			}),
			"boolean & (number | string)",
		},
		// An intersection inside a union needs nothing, since the tighter operator already
		// groups the way the tree does.
		"IntersectionInsideUnion": {
			NewUnionTypeAnn([]TypeAnn{
				num(),
				NewIntersectionTypeAnn([]TypeAnn{str(), boolean()}),
			}),
			"number | string & boolean",
		},
		"FlatUnion": {
			NewUnionTypeAnn([]TypeAnn{num(), str(), boolean()}),
			"number | string | boolean",
		},
		"FlatIntersection": {
			NewIntersectionTypeAnn([]TypeAnn{num(), str(), boolean()}),
			"number & string & boolean",
		},
		// A function type and a conditional type each run to the end of the enclosing
		// type, so both have to be wrapped inside a union or an intersection. TypeScript
		// rejects `number | () => string` outright, and reads
		// `number | A extends B ? C : D` as `(number | A) extends B ? C : D`.
		"FuncTypeInsideUnion": {
			NewUnionTypeAnn([]TypeAnn{num(), fnTo(str())}),
			"number | (() => string)",
		},
		"FuncTypeInsideIntersection": {
			NewIntersectionTypeAnn([]TypeAnn{num(), fnTo(str())}),
			"number & (() => string)",
		},
		"CondTypeInsideUnion": {
			NewUnionTypeAnn([]TypeAnn{num(), NewCondTypeAnn(ref("A"), ref("B"), str(), boolean())}),
			"number | (A extends B ? string : boolean)",
		},
		// `keyof` binds tighter than both operators, so `keyof A | B` reads as
		// `(keyof A) | B` and a union operand has to be wrapped.
		"UnionUnderKeyOf": {
			NewKeyOfTypeAnn(NewUnionTypeAnn([]TypeAnn{ref("A"), ref("B")})),
			"keyof (A | B)",
		},
		"RefUnderKeyOf": {
			NewKeyOfTypeAnn(ref("A")),
			"keyof A",
		},
		// An indexed access reads from a target that binds as tightly as a type
		// reference, so anything carrying an operator has to be wrapped.
		"UnionAsIndexTarget": {
			NewIndexTypeAnn(NewUnionTypeAnn([]TypeAnn{ref("A"), ref("B")}), str()),
			`(A | B)[string]`,
		},
		"KeyOfAsIndexTarget": {
			NewIndexTypeAnn(NewKeyOfTypeAnn(ref("A")), str()),
			`(keyof A)[string]`,
		},
		"IndexChain": {
			NewIndexTypeAnn(NewIndexTypeAnn(ref("A"), str()), num()),
			`A[string][number]`,
		},
		// Either side of `extends` would otherwise let a function type or a nested
		// conditional run past the `?` that follows.
		"CondTypeAsCheck": {
			NewCondTypeAnn(NewCondTypeAnn(ref("A"), ref("B"), num(), str()), ref("C"), num(), str()),
			"(A extends B ? number : string) extends C ? number : string",
		},
		"FuncTypeAsExtends": {
			NewCondTypeAnn(ref("A"), fnTo(str()), num(), str()),
			"A extends (() => string) ? number : string",
		},
		"UnionAsCheck": {
			NewCondTypeAnn(NewUnionTypeAnn([]TypeAnn{ref("A"), ref("B")}), ref("C"), num(), str()),
			"A | B extends C ? number : string",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			printer := NewPrinter()
			printer.PrintTypeAnn(test.typeAnn)
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
