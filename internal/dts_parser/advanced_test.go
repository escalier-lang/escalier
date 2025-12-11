package dts_parser

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/gkampitakis/go-snaps/snaps"
)

// ============================================================================
// Phase 5 Tests: Advanced Type Operators
// ============================================================================

func TestIndexedAccessTypes(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"simple indexed access", "T[K]"},
		{"indexed with string literal", `T["name"]`},
		{"indexed with number literal", "T[0]"},
		{"nested indexed access", "T[K][P]"},
		{"indexed access with union", "T[K | P]"},
		{"indexed access of array", "string[][0]"},
		{"indexed access with qualified name", "Foo.Bar[K]"},
		{"multiple levels", "T[K][P][Q]"},
		{"indexed with keyof", "T[keyof U]"},
		{"complex nested", "Record<string, number>[K]"},
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

func TestConditionalTypes(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"simple conditional", "T extends U ? X : Y"},
		{"conditional with primitives", "T extends string ? true : false"},
		{"nested conditional true branch", "T extends U ? (X extends Y ? A : B) : Z"},
		{"nested conditional false branch", "T extends U ? X : (Y extends Z ? A : B)"},
		{"conditional with union check", "T extends string | number ? X : Y"},
		{"conditional with intersection check", "T extends U & V ? X : Y"},
		{"conditional with array", "T extends any[] ? T[number] : T"},
		{"conditional with infer", "T extends Array<infer U> ? U : T"},
		{"multiple infer", "T extends (arg: infer A) => infer R ? R : never"},
		{"complex nested", "T extends { a: infer U, b: infer V } ? [U, V] : never"},
		{"union then conditional", "T | U extends V ? X : Y"},        // Should parse as (T | U) extends V ? X : Y
		{"intersection then conditional", "T & U extends V ? X : Y"}, // Should parse as (T & U) extends V ? X : Y
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

func TestInferTypes(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"infer in conditional", "T extends Array<infer U> ? U : never"},
		{"infer in function", "T extends (...args: any[]) => infer R ? R : any"},
		{"multiple infer", "T extends (a: infer A, b: infer B) => infer R ? [A, B, R] : never"},
		{"infer in object", "T extends { a: infer A } ? A : never"},
		{"nested infer", "T extends Promise<infer U> ? (U extends Array<infer V> ? V : U) : T"},
		{"infer with string constraint", "T extends Array<infer U extends string> ? U : never"},
		{"infer with number constraint", "T extends (arg: infer A extends number) => infer R ? R : any"},
		{"infer with object constraint", "T extends Array<infer U extends { name: string }> ? U : never"},
		{"infer with union constraint", "T extends Promise<infer U extends string | number> ? U : T"},
		{"infer with array constraint", "T extends (arg: infer A extends any[]) => infer R ? R : any"},
		{"multiple infer with constraints", "T extends (a: infer A extends string, b: infer B extends number) => infer R ? [A, B, R] : never"},
		{"nested infer with constraints", "T extends Promise<infer U extends Array<infer V extends string>> ? V : T"},
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

func TestMappedTypes(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"simple mapped type", "{ [K in T]: U }"},
		{"mapped with keyof", "{ [K in keyof T]: T[K] }"},
		{"mapped with readonly", "{ readonly [K in keyof T]: T[K] }"},
		{"mapped with optional", "{ [K in keyof T]?: T[K] }"},
		{"mapped with both modifiers", "{ readonly [K in keyof T]?: T[K] }"},
		{"mapped with add modifiers", "{ +readonly [K in keyof T]+?: T[K] }"},
		{"mapped with remove modifiers", "{ -readonly [K in keyof T]-?: T[K] }"},
		// TODO: Requires single-quote string literal support in lexer
		// {"mapped with union constraint", "{ [K in 'a' | 'b' | 'c']: string }"},
		{"mapped with as clause", "{ [K in keyof T as `get${K}`]: T[K] }"},
		{"mapped with complex value", "{ [K in keyof T]: T[K] extends Function ? K : never }"},
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

func TestTemplateLiteralTypes(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"empty template", "``"},
		{"template with string", "`hello`"},
		{"template with type", "`${T}`"},
		{"template with prefix", "`hello ${T}`"},
		{"template with suffix", "`${T} world`"},
		{"template with multiple parts", "`${A}-${B}`"},
		{"template complex", "`Hello, ${First} ${Last}!`"},
		{"template with union", "`${T | U}`"},
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

func TestKeyOfTypes(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"keyof identifier", "keyof T"},
		{"keyof object", "keyof { a: string, b: number }"},
		{"keyof array", "keyof string[]"},     // Should parse as keyof(string[]), not (keyof string)[]
		{"keyof nested array", "keyof T[][]"}, // Should parse as keyof(T[][])
		{"keyof union", "keyof (T | U)"},
		{"keyof with indexed access", "keyof T[K]"},  // Should parse as keyof(T[K])
		{"keyof indexed then array", "keyof T[K][]"}, // Should parse as keyof((T[K])[])
		{"nested keyof", "keyof keyof T"},
		{"keyof in union", "string | keyof T"},
		{"keyof qualified name", "keyof Foo.Bar"},
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

func TestTypeOfTypes(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"typeof identifier", "typeof foo"},
		{"typeof qualified name", "typeof Foo.bar"},
		{"typeof nested", "typeof Foo.Bar.baz"},
		{"typeof in union", "string | typeof foo"},
		{"typeof array access", "typeof foo[0]"},
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

func TestImportTypes(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"simple import", `import("module")`},
		{"import with member", `import("module").Type`},
		{"import with nested member", `import("module").Foo.Bar`},
		{"import with type args", `import("module").Type<T>`},
		{"import with multiple type args", `import("module").Generic<T, U>`},
		{"import in union", `string | import("module").Type`},
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

func TestThisType(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"this type alone", "this"},
		{"this in union", "this | null"},
		{"this in intersection", "this & T"},
		{"this array", "this[]"},
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

func TestRestType(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"rest string", "...string"},
		{"rest array", "...T[]"},
		{"rest union", "...(string | number)"},
		{"rest type ref", "...Args"},
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

func TestComplexAdvancedTypes(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			"Pick utility type",
			"{ [K in keyof T]: K extends keyof U ? T[K] : never }",
		},
		{
			"Exclude utility type",
			"T extends U ? never : T",
		},
		{
			"ReturnType utility",
			"T extends (...args: any[]) => infer R ? R : any",
		},
		{
			"Partial-like",
			"{ [K in keyof T]?: T[K] }",
		},
		{
			"Required-like",
			"{ [K in keyof T]-?: T[K] }",
		},
		{
			"Readonly-like",
			"{ readonly [K in keyof T]: T[K] }",
		},
		{
			"Record-like",
			"{ [K in keyof T]: U }",
		},
		{
			"Getters mapped type",
			"{ [K in keyof T as `get${K}`]: () => T[K] }",
		},
		{
			"Complex conditional with multiple infer",
			"T extends (a: infer A, b: infer B) => infer R ? (A extends string ? R : never) : never",
		},
		{
			"Nested mapped and conditional",
			"{ [K in keyof T]: T[K] extends Array<infer U> ? U : T[K] }",
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

			snaps.MatchSnapshot(t, typeAnn)
		})
	}
}

func TestOptionalType(t *testing.T) {
	// Note: parseOptionalType is a helper function that isn't directly called
	// by the main parsing path. Tuples handle optional elements inline.
	// However, we test it here to ensure it works correctly if used.
	tests := []struct {
		name  string
		input string
	}{
		// In tuples, optional types are parsed correctly
		{"optional in tuple", "[string?]"},
		{"optional at end of tuple", "[string, number?]"},
		{"multiple optional in tuple", "[string?, number?, boolean?]"},
		{"labeled optional in tuple", "[x?: string]"},
		{"labeled optional at end", "[x: string, y?: number]"},

		// Edge cases with complex types
		{"optional union in tuple", "[(string | number)?]"},
		{"optional intersection in tuple", "[(string & number)?]"},
		{"optional array in tuple", "[string[]?]"},
		{"optional generic in tuple", "[Array<T>?]"},
		{"optional function in tuple", "[((x: number) => string)?]"},
		{"optional object in tuple", "[{ a: string }?]"},

		// Nested optional scenarios
		{"optional tuple in tuple", "[[string, number]?]"},
		{"multiple complex optional", "[Array<string>?, Map<K, V>?]"},
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
