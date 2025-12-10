package dts_parser

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/gkampitakis/go-snaps/snaps"
)

// ============================================================================
// Phase 2 Tests: Array Types and Tuple Types
// ============================================================================

func TestArrayTypes(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"simple array", "string[]"},
		{"number array", "number[]"},
		{"nested array", "string[][]"},
		{"triple nested array", "number[][][]"},
		{"array of type reference", "Foo[]"},
		{"array of union", "(string | number)[]"},
		{"array of intersection", "(Foo & Bar)[]"},
		{"array with type args", "Array<string>[]"},
		{"qualified name array", "Foo.Bar[]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &ast.Source{
				Path:     "test.d.ts",
				Contents: tt.input,
				ID:       0,
			}
			parser := NewDtsParser(source)
			typeAnn := parser.ParseTypeAnn()

			if typeAnn == nil {
				t.Fatalf("Failed to parse type: %s", tt.input)
			}

			if len(parser.errors) > 0 {
				t.Fatalf("Unexpected errors: %v", parser.errors)
			}

			snaps.MatchSnapshot(t, typeAnn)
		})
	}
}

func TestTupleTypes(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"empty tuple", "[]"},
		{"single element", "[string]"},
		{"two elements", "[string, number]"},
		{"three elements", "[string, number, boolean]"},
		{"with primitives", "[any, unknown, never]"},
		{"with type references", "[Foo, Bar, Baz]"},
		{"mixed types", "[string, Foo, number]"},
		{"with union elements", "[string | number, boolean]"},
		{"with intersection elements", "[Foo & Bar, Baz]"},
		{"nested tuples", "[[string, number], [boolean, any]]"},
		{"tuple of arrays", "[string[], number[]]"},
		{"array of tuples", "[string, number][]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &ast.Source{
				Path:     "test.d.ts",
				Contents: tt.input,
				ID:       0,
			}
			parser := NewDtsParser(source)
			typeAnn := parser.ParseTypeAnn()

			if typeAnn == nil {
				t.Fatalf("Failed to parse type: %s", tt.input)
			}

			if len(parser.errors) > 0 {
				t.Fatalf("Unexpected errors: %v", parser.errors)
			}

			snaps.MatchSnapshot(t, typeAnn)
		})
	}
}

func TestTupleWithOptionalElements(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"single optional", "[string?]"},
		{"optional at end", "[string, number?]"},
		{"multiple optional", "[string?, number?]"},
		{"mixed optional required", "[string, number?, boolean]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &ast.Source{
				Path:     "test.d.ts",
				Contents: tt.input,
				ID:       0,
			}
			parser := NewDtsParser(source)
			typeAnn := parser.ParseTypeAnn()

			if typeAnn == nil {
				t.Fatalf("Failed to parse type: %s", tt.input)
			}

			if len(parser.errors) > 0 {
				t.Fatalf("Unexpected errors: %v", parser.errors)
			}

			snaps.MatchSnapshot(t, typeAnn)
		})
	}
}

func TestTupleWithRestElements(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"rest at end", "[string, ...number[]]"},
		{"rest only", "[...string[]]"},
		{"multiple before rest", "[string, number, ...boolean[]]"},
		{"rest with union", "[string, ...(number | boolean)[]]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &ast.Source{
				Path:     "test.d.ts",
				Contents: tt.input,
				ID:       0,
			}
			parser := NewDtsParser(source)
			typeAnn := parser.ParseTypeAnn()

			if typeAnn == nil {
				t.Fatalf("Failed to parse type: %s", tt.input)
			}

			if len(parser.errors) > 0 {
				t.Fatalf("Unexpected errors: %v", parser.errors)
			}

			snaps.MatchSnapshot(t, typeAnn)
		})
	}
}

func TestTupleWithLabels(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"single labeled", "[x: string]"},
		{"multiple labeled", "[x: string, y: number]"},
		{"mixed labeled unlabeled", "[string, y: number]"},
		{"labeled optional", "[x?: string]"},
		{"labeled with colon and optional", "[x?: string, y: number]"},
		{"labeled rest", "[x: string, ...rest: number[]]"},
		{"all labeled", "[x: string, y: number, z: boolean]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &ast.Source{
				Path:     "test.d.ts",
				Contents: tt.input,
				ID:       0,
			}
			parser := NewDtsParser(source)
			typeAnn := parser.ParseTypeAnn()

			if typeAnn == nil {
				t.Fatalf("Failed to parse type: %s", tt.input)
			}

			if len(parser.errors) > 0 {
				t.Fatalf("Unexpected errors: %v", parser.errors)
			}

			snaps.MatchSnapshot(t, typeAnn)
		})
	}
}

func TestTupleEdgeCases(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"trailing comma", "[string, number,]"},
		{"single trailing comma", "[string,]"},
		{"multiple trailing commas", "[string,,]"},
		{"spaces", "[ string , number ]"},
		{"newlines", "[\n  string,\n  number\n]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &ast.Source{
				Path:     "test.d.ts",
				Contents: tt.input,
				ID:       0,
			}
			parser := NewDtsParser(source)
			typeAnn := parser.ParseTypeAnn()

			// Some edge cases might fail
			snaps.MatchSnapshot(t, map[string]interface{}{
				"typeAnn": typeAnn,
				"errors":  parser.errors,
			})
		})
	}
}

func TestArrayAndTupleCombinations(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"union with array", "string[] | number[]"},
		{"union with tuple", "[string, number] | [boolean]"},
		{"intersection with array", "Foo[] & Bar[]"},
		{"array of union", "(string | number)[]"},
		{"tuple of unions", "[string | number, boolean | any]"},
		{"complex nested", "Array<[string, number]> | [Foo[], Bar]"},
		{"parenthesized array", "(string)[]"},
		{"array in type args", "Map<string, number[]>"},
		{"tuple in type args", "Result<[string, number]>"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &ast.Source{
				Path:     "test.d.ts",
				Contents: tt.input,
				ID:       0,
			}
			parser := NewDtsParser(source)
			typeAnn := parser.ParseTypeAnn()

			if typeAnn == nil {
				t.Fatalf("Failed to parse type: %s", tt.input)
			}

			if len(parser.errors) > 0 {
				t.Fatalf("Unexpected errors: %v", parser.errors)
			}

			snaps.MatchSnapshot(t, typeAnn)
		})
	}
}

func TestArrayVsIndexAccess(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		description string
	}{
		{"array type", "string[]", "should be array type"},
		{"empty brackets after type", "Foo[]", "should be array type"},
		{"nested arrays", "string[][]", "should be nested array types"},
		{"tuple", "[string]", "should be tuple type"},
		{"empty tuple", "[]", "should be empty tuple"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &ast.Source{
				Path:     "test.d.ts",
				Contents: tt.input,
				ID:       0,
			}
			parser := NewDtsParser(source)
			typeAnn := parser.ParseTypeAnn()

			if typeAnn == nil {
				t.Fatalf("Failed to parse type: %s", tt.input)
			}

			if len(parser.errors) > 0 {
				t.Fatalf("Unexpected errors: %v", parser.errors)
			}

			t.Logf("Description: %s", tt.description)
			snaps.MatchSnapshot(t, typeAnn)
		})
	}
}
