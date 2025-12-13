package dts_parser

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/gkampitakis/go-snaps/snaps"
)

// Test the specific example from the user's request
func TestRealWorldSymbolInterface(t *testing.T) {
	input := `interface Symbol {
    /** Returns a string representation of an object. */
    toString(): string;

    /** Returns the primitive value of the specified object. */
    valueOf(): symbol;
}`

	source := &ast.Source{
		Path:     "test.d.ts",
		Contents: input,
		ID:       0,
	}
	parser := NewDtsParser(source)
	module, errors := parser.ParseModule()

	if len(errors) > 0 {
		t.Fatalf("Unexpected errors: %v", errors)
	}

	if module == nil {
		t.Fatal("Expected module to be parsed")
	}

	if len(module.Statements) != 1 {
		t.Fatalf("Expected 1 statement, got %d", len(module.Statements))
	}

	snaps.MatchSnapshot(t, module)
}

// Test comments in various positions
func TestCommentsInObjectTypes(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			"single line comment before property",
			"type Foo = { /** doc */ prop: string }",
		},
		{
			"block comment before property",
			"type Foo = { /* comment */ prop: string }",
		},
		{
			"multiple comments before property",
			"type Foo = { /** doc */ /* inline */ prop: string }",
		},
		{
			"comments between properties",
			"type Foo = { a: string; /** doc for b */ b: number }",
		},
		{
			"comment after separator",
			"type Foo = { a: string, /** doc */ b: number }",
		},
		{
			"multi-line JSDoc comment",
			`type Foo = {
    /**
     * This is a property
     * with a multi-line comment
     */
    prop: string;
}`,
		},
		{
			"comments before method",
			"type Foo = { /** Gets value */ getValue(): number }",
		},
		{
			"comments before index signature",
			"type Foo = { /** Index sig */ [key: string]: any }",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &ast.Source{
				Path:     "test.d.ts",
				Contents: tt.input,
				ID:       0,
			}
			parser := NewDtsParser(source)
			module, errors := parser.ParseModule()

			if len(errors) > 0 {
				t.Fatalf("Unexpected errors: %v", errors)
			}

			snaps.MatchSnapshot(t, module)
		})
	}
}
