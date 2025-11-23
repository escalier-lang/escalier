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
		"UnknownTypeAnn": {
			input: "unknown",
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
			input: "A.B", // parses as a qualified type reference
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
		"MappedObjectTypeOptionalProperties": {
			input: "{[P]?: T[P] for P in keyof T}",
		},
		"MappedObjectTypePropertyRenaming": {
			input: "{[`prefix_${K}`]: T[K] for K in keyof T}",
		},
		"MappedObjectTypeWithFiltering": {
			input: "{[K]: T[K] for K in keyof T if T[K] : string}",
		},
		"ObjectTypeWithRestSpread": {
			input: "{x: string, ...T}",
		},
		"ObjectTypeWithOnlyRestSpread": {
			input: "{...T}",
		},
		"ObjectTypeWithMultipleRestSpread": {
			input: "{x: string, ...T, y: number, ...U}",
		},
		"Symbol": {
			input: "symbol",
		},
		"UniqueSymbol": {
			input: "unique symbol",
		},
		"TemplateLiteralType": {
			input: "`hello-${T}`",
		},
		"TemplateLiteralTypeMultipleParams": {
			input: "`${A}-${B}-${C}`",
		},
		"TemplateLiteralTypeNoParams": {
			input: "`hello-world`",
		},
		"KeyOfType": {
			input: "keyof T",
		},
		"UnionTypeWithKeyOf": {
			input: "keyof T | U",
		},
		"KeyOfObjectType": {
			input: "keyof {x: string, y: number}",
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
			assert.Equal(t, []*Error{}, parser.errors)
		})
	}
}
