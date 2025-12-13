package dts_parser

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/gkampitakis/go-snaps/snaps"
)

// ============================================================================
// Phase 3 Tests: Function & Constructor Types
// ============================================================================

func TestFunctionTypes(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"simple function", "() => void"},
		{"with single param", "(x: number) => string"},
		{"with multiple params", "(x: number, y: string) => boolean"},
		{"with optional param", "(x?: number) => string"},
		{"with rest param", "(...args: string[]) => void"},
		{"mixed params", "(x: number, y?: string, ...rest: any[]) => void"},
		{"return function", "(x: number) => (y: string) => boolean"},
		{"nested in union", "string | ((x: number) => string)"},
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

func TestFunctionTypesWithTypeParams(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"single type param", "<T>(x: T) => T"},
		{"multiple type params", "<T, U>(x: T, y: U) => T"},
		{"with constraint", "<T extends string>(x: T) => T"},
		{"with default", "<T = string>(x: T) => T"},
		{"constraint and default", "<T extends string = \"hello\">(x: T) => T"},
		{"multiple with constraints", "<T extends string, U extends number>(x: T, y: U) => T"},
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

func TestConstructorTypes(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"simple constructor", "new () => Object"},
		{"with params", "new (x: number, y: string) => MyClass"},
		{"with type params", "new <T>(x: T) => MyClass<T>"},
		{"with optional param", "new (x?: number) => MyClass"},
		{"complex", "new <T extends Base>(x: T, y?: string) => Derived<T>"},
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

func TestTypePredicates(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"simple predicate", "(x: any) => x is string"},
		{"with type param", "<T>(x: T) => x is NonNullable<T>"},
		{"asserts only", "(x: any) => asserts x"},
		{"asserts with type", "(x: any) => asserts x is string"},
		{"complex type", "(value: unknown) => value is string | number"},
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

func TestParenthesizedVsFunctionType(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"parenthesized type", "(string)"},
		{"parenthesized union", "(string | number)"},
		{"function no params", "() => void"},
		{"function one param", "(x: string) => void"},
		{"nested paren", "((string))"},
		{"function returning function", "() => () => void"},
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

func TestTypeKeywordsAsIdentifiers(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"string param", "(string: string) => number"},
		{"number param", "(number: number) => string"},
		{"boolean param", "(boolean: boolean) => void"},
		{"bigint param", "(bigint: bigint) => string"},
		{"multiple type keywords", "(string: string, number: number, boolean: boolean) => void"},
		{"mixed with regular", "(value: string, number: number) => boolean"},
		{"type predicate with type keyword", "(string: any) => string is string"},
		{"asserts with type keyword", "(number: any) => asserts number"},
		{"asserts with type and keyword", "(boolean: any) => asserts boolean is boolean"},
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
