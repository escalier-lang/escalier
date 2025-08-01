package parser

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/assert"
)

func TestParseTypeAnnNoErrors(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"StringTypeAnn": {
			input: "string",
		},
		"StringLiteralTypeAnn": {
			input: "\"hello\"",
		},
		"RegexLiteralTypeAnn": {
			input: "/hello/gi",
		},
		"NumberTypeAnn": {
			input: "number",
		},
		"NumberLiteralTypeAnn": {
			input: "5",
		},
		"TrueLiteralTypeAnn": {
			input: "true",
		},
		"FalseLiteralTypeAnn": {
			input: "false",
		},
		"FuncWithoutParams": {
			input: "fn() -> number",
		},
		"FuncWithParams": {
			input: "fn(x: number, y: string) -> boolean",
		},
		"FuncWithTypeParams": {
			input: "fn<T: number, U: string>(x: T, y: U) -> boolean",
		},
		"UnionType": {
			input: "A | B | C",
		},
		"IntersectionType": {
			input: "A & B & C",
		},
		"UnionAndIntersectionType": {
			input: "A & B | X & Y",
		},
		"IndexedTypeWithBrackets": {
			input: "A[B]",
		},
		"IndexedTypeWithDot": {
			input: "A.B",
		},
		"QualifiedTypeRef": {
			input: "Foo.Bar",
		},
		"DeepQualifiedTypeRef": {
			input: "Foo.Bar.Baz",
		},
		"QualifiedTypeRefWithTypeArgs": {
			input: "Foo.Bar<T, U>",
		},
		"MutableType": {
			input: "mut A",
		},
		"ConditionalType": {
			input: "if A : B { C } else { D }",
		},
		"ConditionalTypeWithChaining": {
			input: "if A : B { C } else if E : F { G } else { H }",
		},
		"InferType": {
			input: "infer T",
		},
		"ConditionalTypeWithInfer": {
			input: "if T : fn(...args: infer P) -> any { P } else { never }",
		},
		"BasicObjectType": {
			input: "{a: A, b?: B, [c]: C, [d]?: D}",
		},
		"MappedObjectType": {
			input: "{[K]: T[K] for K in Keys<T>}",
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
			typeAnn := parser.typeAnn()

			snaps.MatchSnapshot(t, typeAnn)
			assert.Equal(t, parser.errors, []*Error{})
		})
	}
}
