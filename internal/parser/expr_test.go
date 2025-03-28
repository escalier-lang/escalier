package parser

import (
	"testing"

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
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := Source{
				Path:     "input.esc",
				Contents: test.input,
			}

			parser := NewParser(source)
			expr := parser.ParseExpr()

			snaps.MatchSnapshot(t, expr)
			assert.Len(t, parser.Errors, 0)
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
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := Source{
				Path:     "input.esc",
				Contents: test.input,
			}

			parser := NewParser(source)
			expr := parser.ParseExpr()

			snaps.MatchSnapshot(t, expr)
			assert.Greater(t, len(parser.Errors), 0)
			snaps.MatchSnapshot(t, parser.Errors)
		})
	}
}
