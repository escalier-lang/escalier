package dts_parser

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/gkampitakis/go-snaps/snaps"
)

func TestPrimitiveTypes(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"any", "any"},
		{"unknown", "unknown"},
		{"string", "string"},
		{"number", "number"},
		{"boolean", "boolean"},
		{"symbol", "symbol"},
		{"null", "null"},
		{"undefined", "undefined"},
		{"never", "never"},
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

func TestLiteralTypes(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"string literal", `"hello"`},
		{"number literal", "42"},
		{"boolean true", "true"},
		{"boolean false", "false"},
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

func TestTypeReferences(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"simple identifier", "Foo"},
		{"qualified name", "Foo.Bar"},
		{"nested qualified name", "Foo.Bar.Baz"},
		{"with type args", "Array<string>"},
		{"with multiple type args", "Map<string, number>"},
		{"nested type args", "Array<Map<string, number>>"},
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

func TestUnionTypes(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"simple union", "string | number"},
		{"three types", "string | number | boolean"},
		{"with type refs", "Foo | Bar | Baz"},
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

func TestIntersectionTypes(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"simple intersection", "Foo & Bar"},
		{"three types", "Foo & Bar & Baz"},
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

func TestParenthesizedTypes(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"simple paren", "(string)"},
		{"paren with union", "(string | number)"},
		{"nested paren", "((string))"},
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

func TestErrorHandling(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		expectError bool
	}{
		{"unclosed type args", "Array<string", true},
		{"missing type after union", "string |", true},
		{"missing type after intersection", "Foo &", true},
		{"missing closing paren", "(string", true},
		{"empty type args", "Array<>", true},
		{"unexpected token", "123abc", false},
		{"trailing comma in type args", "Map<string,", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &ast.Source{
				Path:     "test.d.ts",
				Contents: tt.input,
				ID:       0,
			}
			parser := NewDtsParser(source)
			_ = parser.ParseTypeAnn()

			if tt.expectError {
				if len(parser.errors) == 0 {
					t.Fatalf("Expected error but got none for: %s", tt.input)
				}
				// Just verify we got an error, don't check the exact message
				// since error messages may vary
			} else {
				if len(parser.errors) > 0 {
					t.Logf("Got unexpected errors (may be ok): %v", parser.errors)
				}
			}
		})
	}
}

func TestComplexUnionIntersectionCombinations(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"union of intersections", "(Foo & Bar) | (Baz & Qux)"},
		{"intersection has higher precedence", "A | B & C"},
		{"multiple levels", "A | B & C | D"},
		{"nested with parens", "((A | B) & C) | D"},
		{"complex with type args", "Array<string> | Map<string, number> & Set<boolean>"},
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

func TestTypeReferenceEdgeCases(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"single type arg", "Promise<void>"},
		{"deeply nested qualified name", "A.B.C.D.E"},
		{"type args with union", "Result<string | number>"},
		{"type args with intersection", "Combined<Foo & Bar>"},
		{"deeply nested type args", "A<B<C<D<E>>>>"},
		{"multiple qualified names with type args", "Foo.Bar<Baz.Qux>"},
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

func TestParseModule(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"empty module", ""},
		{"module with line comments", "// comment\n// another comment"},
		{"module with block comments", "/* comment */ /* another */"},
		{"module with mixed comments", "// line\n/* block */\n// another line"},
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

			if module == nil {
				t.Fatalf("Failed to parse module: %s", tt.input)
			}

			if len(errors) > 0 {
				t.Logf("Got errors (may be expected): %v", errors)
			}

			snaps.MatchSnapshot(t, module)
		})
	}
}

func TestHelperMethods(t *testing.T) {
	t.Run("peek without consuming", func(t *testing.T) {
		source := &ast.Source{
			Path:     "test.d.ts",
			Contents: "string",
			ID:       0,
		}
		parser := NewDtsParser(source)

		token1 := parser.peek()
		token2 := parser.peek()

		if token1.Type != token2.Type || token1.Value != token2.Value {
			t.Fatalf("peek() should return same token without consuming")
		}
	})

	t.Run("consume advances token", func(t *testing.T) {
		source := &ast.Source{
			Path:     "test.d.ts",
			Contents: "string number",
			ID:       0,
		}
		parser := NewDtsParser(source)

		token1 := parser.consume()
		token2 := parser.peek()

		if token1.Type == token2.Type {
			t.Fatalf("consume() should advance to next token")
		}
	})

	t.Run("expect with matching token", func(t *testing.T) {
		source := &ast.Source{
			Path:     "test.d.ts",
			Contents: "string",
			ID:       0,
		}
		parser := NewDtsParser(source)

		token := parser.expect(parser.peek().Type)
		if token == nil {
			t.Fatalf("expect() should return token when types match")
		}
		if len(parser.errors) > 0 {
			t.Fatalf("expect() should not add error when types match")
		}
	})

	t.Run("expect with non-matching token", func(t *testing.T) {
		source := &ast.Source{
			Path:     "test.d.ts",
			Contents: "string",
			ID:       0,
		}
		parser := NewDtsParser(source)

		token := parser.expect(999) // Non-existent token type
		if token != nil {
			t.Fatalf("expect() should return nil when types don't match")
		}
		if len(parser.errors) == 0 {
			t.Fatalf("expect() should add error when types don't match")
		}
	})

	t.Run("reportError adds to errors", func(t *testing.T) {
		source := &ast.Source{
			Path:     "test.d.ts",
			Contents: "string",
			ID:       0,
		}
		parser := NewDtsParser(source)

		span := ast.Span{
			Start:    ast.Location{Line: 1, Column: 0},
			End:      ast.Location{Line: 1, Column: 1},
			SourceID: 0,
		}
		parser.reportError(span, "test error")

		if len(parser.errors) != 1 {
			t.Fatalf("reportError() should add error to errors list")
		}
		if parser.errors[0].Message != "test error" {
			t.Fatalf("reportError() should set correct error message")
		}
	})
}

func TestNegativeLiterals(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"negative number", "-42"},
		{"negative float", "-3.14"},
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

			// Negative numbers might not be supported yet,
			// but we should test behavior
			snaps.MatchSnapshot(t, map[string]interface{}{
				"typeAnn": typeAnn,
				"errors":  parser.errors,
			})
		})
	}
}

func TestWhitespaceHandling(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"union with spaces", "string  |  number"},
		{"intersection with spaces", "Foo  &  Bar"},
		{"type args with spaces", "Array < string >"},
		{"qualified name with spaces around dots", "Foo . Bar . Baz"},
		{"parens with spaces", "(  string  )"},
		{"newlines in union", "string |\nnumber |\nboolean"},
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

			// Some whitespace handling might cause parsing to fail
			// We want to document this behavior
			snaps.MatchSnapshot(t, map[string]interface{}{
				"typeAnn": typeAnn,
				"errors":  parser.errors,
			})
		})
	}
}

func TestMixedOperatorPrecedence(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string // Description of expected parse tree structure
	}{
		{
			"intersection binds tighter than union",
			"A | B & C",
			"should parse as: A | (B & C)",
		},
		{
			"multiple intersections in union",
			"A & B | C & D | E & F",
			"should parse as: (A & B) | (C & D) | (E & F)",
		},
		{
			"parentheses override precedence",
			"(A | B) & (C | D)",
			"should parse with explicit grouping",
		},
		{
			"complex nested structure",
			"A | B & C | (D & E) | F",
			"should parse as: A | (B & C) | (D & E) | F",
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
			typeAnn := parser.ParseTypeAnn()

			if typeAnn == nil {
				t.Fatalf("Failed to parse type: %s", tt.input)
			}

			if len(parser.errors) > 0 {
				t.Fatalf("Unexpected errors: %v", parser.errors)
			}

			// Log the expected structure for documentation
			t.Logf("Expected: %s", tt.expected)
			snaps.MatchSnapshot(t, typeAnn)
		})
	}
}

func TestEmptyInput(t *testing.T) {
	t.Run("empty string", func(t *testing.T) {
		source := &ast.Source{
			Path:     "test.d.ts",
			Contents: "",
			ID:       0,
		}
		parser := NewDtsParser(source)
		typeAnn := parser.ParseTypeAnn()

		// Empty input should return nil
		if typeAnn != nil {
			t.Fatalf("Expected nil for empty input, got: %v", typeAnn)
		}
	})
}

func TestSingleCharacterTypes(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"single letter type", "T"},
		{"single char string literal", `"a"`},
		{"single digit number literal", "1"},
		{"zero literal", "0"},
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

func TestQualifiedIdentifierBoundaries(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"two parts", "A.B"},
		{"ten parts", "A.B.C.D.E.F.G.H.I.J"},
		{"single identifier followed by dot", "A."},
		{"starts with dot", ".A"},
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

			// Some of these should fail (like .A or A.)
			snaps.MatchSnapshot(t, map[string]interface{}{
				"typeAnn": typeAnn,
				"errors":  parser.errors,
			})
		})
	}
}

func TestTypeArgumentsBoundaries(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"single arg no spaces", "A<B>"},
		{"ten type args", "Fn<A, B, C, D, E, F, G, H, I, J>"},
		{"nested three levels", "A<B<C<D>>>"},
		{"mixed primitives and refs", "Map<string, Array<number>>"},
		{"union in type arg", "Result<Success | Error>"},
		{"intersection in type arg", "Combined<Base & Mixin>"},
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

func TestStringLiteralVariations(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"empty string", `""`},
		{"string with spaces", `"hello world"`},
		{"string with special chars", `"hello\nworld"`},
		{"string with unicode", `"hello 🌍"`},
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

func TestModuleWithUnexpectedTokens(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"random characters", "@@##$$"},
		{"partial type", "Array<"},
		{"mismatched brackets", "}{][)("},
		{"number at start", "123"},
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

			// These should produce errors
			if len(errors) == 0 {
				t.Logf("Warning: Expected errors but got none for: %s", tt.input)
			}

			snaps.MatchSnapshot(t, map[string]interface{}{
				"module": module,
				"errors": len(errors), // Just count, not full errors
			})
		})
	}
}
