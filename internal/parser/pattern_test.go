package parser

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/assert"
)

func TestParsePatternNoErrors(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"StringLiteral": {
			input: "\"hello\"",
		},
		"NumberLiteral": {
			input: "5",
		},
		"BooleanLiteralTrue": {
			input: "true",
		},
		"BooleanLiteralFalse": {
			input: "false",
		},
		"NullLiteral": {
			input: "null",
		},
		"UndefinedLiteral": {
			input: "undefined",
		},
		"RegexLiteral": {
			input: "/hello/gi",
		},
		"Identifier": {
			input: "x",
		},
		"IdentifierWithTypeAnnotation": {
			input: "x:number",
		},
		"IdentifierWithTypeAnnotationAndDefault": {
			input: "x:number = 5",
		},
		"Wildcard": {
			input: "_",
		},
		"TuplePatternWithRest": {
			input: "[a, b = 5, ...rest]",
		},
		"TuplePatternWithTypeAnnotations": {
			input: "[x:number, y:string = 5]",
		},
		"ObjectPatternWithRest": {
			input: "{a, b: c, ...rest}",
		},
		"ObjectPatternWithDefaults": {
			input: "{a = 5, b: c = \"hello\"}",
		},
		"ObjectPatternWithInlineTypeAnnotations": {
			input: "{x::number, y::string}",
		},
		"ObjectPatternWithInlineTypeAnnotationsAndDefaults": {
			input: "{x::number = 0, y::string = \"hello\"}",
		},
		"ObjectPatternWithKeyValueAndInlineTypeAnnotations": {
			input: "{x: a:number, y: b:string}",
		},
		"ObjectPatternWithKeyValueInlineTypeAnnotationsAndDefaults": {
			input: "{x: a:number = 0, y: b:string = \"hello\"}",
		},
		"ExtractPattern": {
			input: "Foo(a, b)",
		},
		"WildcardPattern": {
			input: "_",
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
			expr := parser.pattern(false, true)

			snaps.MatchSnapshot(t, expr)
			assert.Equal(t, parser.errors, []*Error{})
		})
	}
}
