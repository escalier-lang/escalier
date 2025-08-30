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
		"RegexLiteral": {
			input: "/hello/gi",
		},
		"Addition": {
			input: "a + b",
		},
		"Concatenation": {
			input: "a ++ b",
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
		"FuncExprWithReturnType": {
			input: "fn (a, b) -> number { a + b }",
		},
		"FuncExprWithThrows": {
			input: "fn (a, b) -> number throws Error { a + b }",
		},
		"FuncExprReturnIfElse": {
			input: `fn (value: string) { return if value != "" { value } else { "value is empty" } }`,
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
			input: "{ foo(self) { return 5 }, get bar(self) { return self.x }, set bar(mut self, x) { this.x = x } }",
		},
		"ObjectWithStaticMethod": {
			input: "{ foo() { return 5 } }",
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
		"MethodCall": {
			input: "foo.bar()/baz",
		},
		"MatchBasic": {
			input: "match x { 1 => \"one\", 2 => \"two\" }",
		},
		"MatchWithGuard": {
			input: "match x { [a, b] if a > b => \"first is greater\" }",
		},
		"MatchWithBlock": {
			input: "match x { _ => {\n  console.log(x)\n  \"unknown\"\n} }",
		},
		"MatchWithPatterns": {
			input: "match value { {name} => name, [first, ...rest] => first, _ => null }",
		},
		"MatchComplex": {
			input: "match result { Some(value) if value > 0 => value, Some(_) => 0, None => -1 }",
		},
		"TryBasic": {
			input: "try { riskyOperation() }",
		},
		"TryCatch": {
			input: "try { riskyOperation() } catch { error => { console.log(error) } }",
		},
		"TryCatchMultipleCases": {
			input: "try { operation() } catch { NetworkError(msg) => \"Network: \" ++ msg, TimeoutError => \"Timeout\", _ => \"Unknown error\" }",
		},
		"TryCatchWithGuard": {
			input: "try { getValue() } catch { error if error.code == 404 => \"Not found\", error => error.message }",
		},
		"TryCatchWithBlockBody": {
			input: "try { complexOperation() } catch { error => {\n  logError(error)\n  \"Failed\"\n} }",
		},
		"NestedTry": {
			input: "try { try { innerOperation() } catch { _ => null } } catch { outer => outer }",
		},
		"ThrowVariable": {
			input: "throw error",
		},
		"ThrowExpression": {
			input: "throw computeError()",
		},
		"ThrowStringLiteral": {
			input: "throw \"divide by zero\"",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := &ast.Source{
				ID:       0,
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
		"MatchMissingTarget": {
			input: "match { 1 => \"one\" }",
		},
		"MatchMissingOpeningBrace": {
			input: "match x 1 => \"one\" }",
		},
		"MatchMissingArrow": {
			input: "match x { 1 \"one\" }",
		},
		"MatchMissingPattern": {
			input: "match x { => \"one\" }",
		},
		"MatchIncompleteGuard": {
			input: "match x { 1 if => \"one\" }",
		},
		"MatchMissingBody": {
			input: "match x { 1 => }",
		},
		"MatchMissingClosingBrace": {
			input: "match x { 1 => \"one\"",
		},
		"TryMissingBlock": {
			input: "try",
		},
		"TryMissingOpeningBrace": {
			input: "try console.log(\"test\")",
		},
		"TryCatchMissingOpeningBrace": {
			input: "try { operation() } catch error => error }",
		},
		"TryCatchMissingClosingBrace": {
			input: "try { operation() } catch { error => error",
		},
		"TryCatchMissingPattern": {
			input: "try { operation() } catch { => \"error\" }",
		},
		"TryCatchMissingArrow": {
			input: "try { operation() } catch { error \"failed\" }",
		},
		"TryCatchMissingBody": {
			input: "try { operation() } catch { error => }",
		},
		"TryCatchIncompleteGuard": {
			input: "try { operation() } catch { error if => \"failed\" }",
		},
		"ThrowMissingExpression": {
			input: "throw",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := &ast.Source{
				ID:       0,
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
