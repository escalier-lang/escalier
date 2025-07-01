package parser

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/assert"
)

func TestParseExprNoErrors(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"StringLiteral": {
			input: "\"hello\"",
		},
		"TemplateStringLiteralWithExprs": {
			input: "`hello ${name}`",
		},
		"TemplateStringMultipleLines": {
			input: "`hello\nworld`",
		},
		"TemplateStringLiteralWithMultipleExprs": {
			input: "`a${b}c${d}e`",
		},
		"NestedTemplateStringLiteral": {
			input: "`a${`b${c}d`}e`",
		},
		"TaggedTemplateStringLiteral": {
			input: "gql`query userId { user { id } }`",
		},
		"NumberLiteral": {
			input: "5",
		},
		"NumberLiteralDecimal": {
			input: "1.5",
		},
		"NumberLiteralTrailingDecimal": {
			input: "1.",
		},
		"NumberLiteralLeadingDecimal": {
			input: ".5",
		},
		"Addition": {
			input: "a + b",
		},
		"AddSub": {
			input: "a - b + c",
		},
		"MulAdd": {
			input: "a * b + c * d",
		},
		"MulDiv": {
			input: "a / b * c",
		},
		"UnaryOps": {
			input: "+a - -b",
		},
		"SingleUnaryOp": {
			input: "-5",
		},
		"Parens": {
			input: "a * (b + c)",
		},
		"Call": {
			input: "foo(a, b, c)",
		},
		"CallPrecedence": {
			input: "a + foo(b)",
		},
		"CurriedCall": {
			input: "foo(a)(b)(c)",
		},
		"OptChainCall": {
			input: "foo?(bar)",
		},
		"ArrayLiteral": {
			input: "[1, 2, 3]",
		},
		"Member": {
			input: "a.b?.c",
		},
		"MemberPrecedence": {
			input: "a + b.c",
		},
		"Index": {
			input: "a[base + offset]",
		},
		"IndexPrecedence": {
			input: "a + b[c]",
		},
		"MultipleIndexes": {
			input: "a[i][j]",
		},
		"OptChainIndex": {
			input: "a?[base + offset]",
		},
		"FuncExpr": {
			input: "fn (a, b) { a + b }",
		},
		"IfElse": {
			input: "if cond { a } else { b }",
		},
		"IfElseChaining": {
			input: "if cond1 { a } else if cond2 { b } else { c }",
		},
		"BasicObject": {
			input: "{ 0: \"hello\", foo: 5, \"bar\"?: true, baz, [qux]: false }",
		},
		"EmptyObject": {
			input: "{}",
		},
		"ObjectWithSpreads": {
			input: "{a, ...b, ...{c, d}}",
		},
		"ObjectWithMethods": {
			input: "{ foo(self) { return 5 }, get bar(self) { return self.x }, set bar(x) { this.x = x } }",
		},
		"LessThan": {
			input: "a < b",
		},
		"GreaterThan": {
			input: "a > b",
		},
		"LessThanEqual": {
			input: "a <= b",
		},
		"GreaterThanEqual": {
			input: "a >= b",
		},
		"Equal": {
			input: "a == b",
		},
		"NotEqual": {
			input: "a != b",
		},
		"FuncCall": {
			input: "foo()",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := &ast.Source{
				ID:       1,
				Path:     "input.esc",
				Contents: test.input,
			}

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			parser := NewParser(ctx, source)
			expr := parser.expr()

			snaps.MatchSnapshot(t, expr)
			assert.Equal(t, []*Error{}, parser.errors)
		})
	}
}

func TestParseExprErrorHandling(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"IncompleteBinaryExpr": {
			input: "a - b +",
		},
		"ExtraOperatorsInBinaryExpr": {
			input: "a + * b",
		},
		"IncompleteCall": {
			input: "foo(a,",
		},
		"IncompleteMember": {
			input: "a + b.",
		},
		"IncompleteMemberOptChain": {
			input: "a + b?.",
		},
		"MismatchedParens": {
			input: "a * (b + c]",
		},
		"MismatchedBracketsArrayLiteral": {
			input: "[1, 2, 3)",
		},
		"MismatchedBracketsIndex": {
			input: "foo[bar)",
		},
		"IncompleteTemplateLiteral": {
			input: "`foo",
		},
		"IncompleteTaggedTemplateLiteral": {
			input: "foo`bar",
		},
		"ParamsMissingOpeningParen": {
			input: "fn a, b) { a + b }",
		},
		"ParamsMissingClosingParen": {
			input: "fn (a, b { a + b }",
		},
		"IfElseMissingOpeningBraces": {
			input: "if cond a } else b }",
		},
		"IfElseMissingCondition": {
			input: "if { a } else { b }",
		},
		"IncompleteElse": {
			input: "if { a } else",
		},
		"ObjectMissingColon": {
			input: "{ foo 5, bar: 10 }",
		},
		"ObjectMissingComma": {
			input: "{ foo: 5 bar: 10 }",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := &ast.Source{
				ID:       1,
				Path:     "input.esc",
				Contents: test.input,
			}

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			parser := NewParser(ctx, source)
			expr := parser.expr()

			snaps.MatchSnapshot(t, expr)
			assert.Greater(t, len(parser.errors), 0)
			snaps.MatchSnapshot(t, parser.errors)
		})
	}
}
