package parser

import (
	"context"
	"testing"
	"time"

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
		"NumberTypeAnn": {
			input: "number",
		},
		"NumberLiteralTypeAnn": {
			input: "5",
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
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := Source{
				Path:     "input.esc",
				Contents: test.input,
			}

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			parser := NewParser(ctx, source)
			typeAnn, errors := parser.typeAnn()

			snaps.MatchSnapshot(t, typeAnn)
			assert.Equal(t, errors, []*Error{})
		})
	}
}
