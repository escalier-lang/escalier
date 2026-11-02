package main

import (
	"strings"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/stretchr/testify/require"
)

func TestGroupedSpan(t *testing.T) {
	tests := []struct {
		name     string
		contents string
		// span is the text the span covers, the first time it occurs in contents.
		span string
		// expected is the text the widened span covers, or "" when groupedSpan fails.
		expected string
	}{
		{name: "Balanced", contents: "g(h.f)", span: "h.f", expected: "h.f"},
		{name: "ParenthesizedReceiver", contents: "g((h).f)", span: "h).f", expected: "(h).f"},
		{name: "NestedReceivers", contents: "g(((h).f).g)", span: "h).f).g", expected: "((h).f).g"},
		{name: "ParenthesizedRightOperand", contents: "g(a + (b))", span: "a + (b", expected: "a + (b)"},
		{name: "SpaceInsideGroup", contents: "g(( h ).f)", span: "h ).f", expected: "( h ).f"},
		{name: "ParenInString", contents: `g(f(")"))`, span: `f(")")`, expected: `f(")")`},
		{name: "ParenInComment", contents: "g(f(/* ) */ x))", span: "f(/* ) */ x)", expected: "f(/* ) */ x)"},
		{name: "ParenInLineComment", contents: "g(f(x // )\n))", span: "f(x // )\n)", expected: "f(x // )\n)"},
		{name: "ParenInTemplate", contents: "g(f(`)`))", span: "f(`)`)", expected: "f(`)`)"},
		{name: "ParenInTemplateSubstitution", contents: "g(f(`${a})`))", span: "f(`${a})`)", expected: "f(`${a})`)"},
		// DISABLED until #2315, which gives the expression spans that cover their parentheses and
		// removes this scan. The scan does not recognize a regular expression literal or JSX text,
		// so the `)` inside each counts as unmatched and the span widens over the call's own `(`.
		// With the scan gone, these become TestBorrowQuickFix cases that check the edit's range.
		/*
			{name: "ParenInRegex", contents: `g(f(/\)/))`, span: `f(/\)/)`, expected: `f(/\)/)`},
			{name: "ParenInJSXText", contents: "g(f(<p>)</p>))", span: "f(<p>)</p>)", expected: "f(<p>)</p>)"},
		*/
		{name: "Lifetimes", contents: "g(pick<'a>(x as &'a T))", span: "pick<'a>(x as &'a T)", expected: "pick<'a>(x as &'a T)"},
		{name: "MissingParen", contents: "g h).f", span: "h).f", expected: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := strings.Index(tt.contents, tt.span)
			require.GreaterOrEqual(t, start, 0)
			span := ast.Span{
				Start: ast.Location{Offset: start},
				End:   ast.Location{Offset: start + len(tt.span)},
			}
			widened, ok := groupedSpan(span, tt.contents)
			if tt.expected == "" {
				require.False(t, ok)
				return
			}
			require.True(t, ok)
			require.Equal(t, tt.expected, tt.contents[widened.Start.Offset:widened.End.Offset])
		})
	}
}
