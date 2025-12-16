package interop

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dts_parser"
	"github.com/gkampitakis/go-snaps/snaps"
)

func TestConvertIdent(t *testing.T) {
	tests := []struct {
		name     string
		input    *dts_parser.Ident
		expected *ast.Ident
	}{
		{
			name: "simple identifier",
			input: dts_parser.NewIdent("foo", ast.Span{
				Start:    ast.Location{Line: 1, Column: 0},
				End:      ast.Location{Line: 1, Column: 3},
				SourceID: 0,
			}),
			expected: ast.NewIdentifier("foo", ast.Span{
				Start:    ast.Location{Line: 1, Column: 0},
				End:      ast.Location{Line: 1, Column: 3},
				SourceID: 0,
			}),
		},
		{
			name: "identifier with underscores",
			input: dts_parser.NewIdent("my_var", ast.Span{
				Start:    ast.Location{Line: 2, Column: 10},
				End:      ast.Location{Line: 2, Column: 16},
				SourceID: 0,
			}),
			expected: ast.NewIdentifier("my_var", ast.Span{
				Start:    ast.Location{Line: 2, Column: 10},
				End:      ast.Location{Line: 2, Column: 16},
				SourceID: 0,
			}),
		},
		{
			name: "camelCase identifier",
			input: dts_parser.NewIdent("myVariable", ast.Span{
				Start:    ast.Location{Line: 3, Column: 5},
				End:      ast.Location{Line: 3, Column: 15},
				SourceID: 0,
			}),
			expected: ast.NewIdentifier("myVariable", ast.Span{
				Start:    ast.Location{Line: 3, Column: 5},
				End:      ast.Location{Line: 3, Column: 15},
				SourceID: 0,
			}),
		},
		{
			name: "PascalCase identifier",
			input: dts_parser.NewIdent("MyClass", ast.Span{
				Start:    ast.Location{Line: 4, Column: 20},
				End:      ast.Location{Line: 4, Column: 27},
				SourceID: 0,
			}),
			expected: ast.NewIdentifier("MyClass", ast.Span{
				Start:    ast.Location{Line: 4, Column: 20},
				End:      ast.Location{Line: 4, Column: 27},
				SourceID: 0,
			}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertIdent(tt.input)

			if result.Name != tt.expected.Name {
				t.Errorf("expected Name %q, got %q", tt.expected.Name, result.Name)
			}

			if result.Span() != tt.expected.Span() {
				t.Errorf("expected Span %v, got %v", tt.expected.Span(), result.Span())
			}
		})
	}
}

func TestConvertQualIdent(t *testing.T) {
	tests := []struct {
		name     string
		input    dts_parser.QualIdent
		expected ast.QualIdent
	}{
		{
			name: "simple identifier",
			input: dts_parser.NewIdent("foo", ast.Span{
				Start:    ast.Location{Line: 1, Column: 0},
				End:      ast.Location{Line: 1, Column: 3},
				SourceID: 0,
			}),
			expected: ast.NewIdentifier("foo", ast.Span{
				Start:    ast.Location{Line: 1, Column: 0},
				End:      ast.Location{Line: 1, Column: 3},
				SourceID: 0,
			}),
		},
		{
			name: "member access - one level",
			input: &dts_parser.Member{
				Left: dts_parser.NewIdent("obj", ast.Span{
					Start:    ast.Location{Line: 1, Column: 0},
					End:      ast.Location{Line: 1, Column: 3},
					SourceID: 0,
				}),
				Right: dts_parser.NewIdent("prop", ast.Span{
					Start:    ast.Location{Line: 1, Column: 4},
					End:      ast.Location{Line: 1, Column: 8},
					SourceID: 0,
				}),
			},
			expected: &ast.Member{
				Left: ast.NewIdentifier("obj", ast.Span{
					Start:    ast.Location{Line: 1, Column: 0},
					End:      ast.Location{Line: 1, Column: 3},
					SourceID: 0,
				}),
				Right: ast.NewIdentifier("prop", ast.Span{
					Start:    ast.Location{Line: 1, Column: 4},
					End:      ast.Location{Line: 1, Column: 8},
					SourceID: 0,
				}),
			},
		},
		{
			name: "member access - two levels",
			input: &dts_parser.Member{
				Left: &dts_parser.Member{
					Left: dts_parser.NewIdent("a", ast.Span{
						Start:    ast.Location{Line: 1, Column: 0},
						End:      ast.Location{Line: 1, Column: 1},
						SourceID: 0,
					}),
					Right: dts_parser.NewIdent("b", ast.Span{
						Start:    ast.Location{Line: 1, Column: 2},
						End:      ast.Location{Line: 1, Column: 3},
						SourceID: 0,
					}),
				},
				Right: dts_parser.NewIdent("c", ast.Span{
					Start:    ast.Location{Line: 1, Column: 4},
					End:      ast.Location{Line: 1, Column: 5},
					SourceID: 0,
				}),
			},
			expected: &ast.Member{
				Left: &ast.Member{
					Left: ast.NewIdentifier("a", ast.Span{
						Start:    ast.Location{Line: 1, Column: 0},
						End:      ast.Location{Line: 1, Column: 1},
						SourceID: 0,
					}),
					Right: ast.NewIdentifier("b", ast.Span{
						Start:    ast.Location{Line: 1, Column: 2},
						End:      ast.Location{Line: 1, Column: 3},
						SourceID: 0,
					}),
				},
				Right: ast.NewIdentifier("c", ast.Span{
					Start:    ast.Location{Line: 1, Column: 4},
					End:      ast.Location{Line: 1, Column: 5},
					SourceID: 0,
				}),
			},
		},
		{
			name: "member access - three levels (namespace.module.item)",
			input: &dts_parser.Member{
				Left: &dts_parser.Member{
					Left: &dts_parser.Member{
						Left: dts_parser.NewIdent("ns", ast.Span{
							Start:    ast.Location{Line: 1, Column: 0},
							End:      ast.Location{Line: 1, Column: 2},
							SourceID: 0,
						}),
						Right: dts_parser.NewIdent("mod", ast.Span{
							Start:    ast.Location{Line: 1, Column: 3},
							End:      ast.Location{Line: 1, Column: 6},
							SourceID: 0,
						}),
					},
					Right: dts_parser.NewIdent("sub", ast.Span{
						Start:    ast.Location{Line: 1, Column: 7},
						End:      ast.Location{Line: 1, Column: 10},
						SourceID: 0,
					}),
				},
				Right: dts_parser.NewIdent("item", ast.Span{
					Start:    ast.Location{Line: 1, Column: 11},
					End:      ast.Location{Line: 1, Column: 15},
					SourceID: 0,
				}),
			},
			expected: &ast.Member{
				Left: &ast.Member{
					Left: &ast.Member{
						Left: ast.NewIdentifier("ns", ast.Span{
							Start:    ast.Location{Line: 1, Column: 0},
							End:      ast.Location{Line: 1, Column: 2},
							SourceID: 0,
						}),
						Right: ast.NewIdentifier("mod", ast.Span{
							Start:    ast.Location{Line: 1, Column: 3},
							End:      ast.Location{Line: 1, Column: 6},
							SourceID: 0,
						}),
					},
					Right: ast.NewIdentifier("sub", ast.Span{
						Start:    ast.Location{Line: 1, Column: 7},
						End:      ast.Location{Line: 1, Column: 10},
						SourceID: 0,
					}),
				},
				Right: ast.NewIdentifier("item", ast.Span{
					Start:    ast.Location{Line: 1, Column: 11},
					End:      ast.Location{Line: 1, Column: 15},
					SourceID: 0,
				}),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertQualIdent(tt.input)

			// Compare using QualIdentToString helper
			resultStr := ast.QualIdentToString(result)
			expectedStr := ast.QualIdentToString(tt.expected)

			if resultStr != expectedStr {
				t.Errorf("expected %q, got %q", expectedStr, resultStr)
			}

			// For simple idents, also check the span
			if resultIdent, ok := result.(*ast.Ident); ok {
				if expectedIdent, ok := tt.expected.(*ast.Ident); ok {
					if resultIdent.Span() != expectedIdent.Span() {
						t.Errorf("expected Span %v, got %v", expectedIdent.Span(), resultIdent.Span())
					}
				}
			}

			// For member access, check the structure recursively
			if resultMember, ok := result.(*ast.Member); ok {
				if expectedMember, ok := tt.expected.(*ast.Member); ok {
					checkMemberEquality(t, resultMember, expectedMember)
				}
			}
		})
	}
}

func checkMemberEquality(t *testing.T, result, expected *ast.Member) {
	// Check right identifier
	if result.Right.Name != expected.Right.Name {
		t.Errorf("expected Right.Name %q, got %q", expected.Right.Name, result.Right.Name)
	}
	if result.Right.Span() != expected.Right.Span() {
		t.Errorf("expected Right.Span %v, got %v", expected.Right.Span(), result.Right.Span())
	}

	// Check left recursively
	switch resultLeft := result.Left.(type) {
	case *ast.Ident:
		if expectedLeft, ok := expected.Left.(*ast.Ident); ok {
			if resultLeft.Name != expectedLeft.Name {
				t.Errorf("expected Left.Name %q, got %q", expectedLeft.Name, resultLeft.Name)
			}
			if resultLeft.Span() != expectedLeft.Span() {
				t.Errorf("expected Left.Span %v, got %v", expectedLeft.Span(), resultLeft.Span())
			}
		} else {
			t.Errorf("expected Left to be *ast.Ident, got %T", expected.Left)
		}
	case *ast.Member:
		if expectedLeft, ok := expected.Left.(*ast.Member); ok {
			checkMemberEquality(t, resultLeft, expectedLeft)
		} else {
			t.Errorf("expected Left to be *ast.Member, got %T", expected.Left)
		}
	}
}

func TestConvertTypeAnn(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		// Primitive types
		{"primitive any", "any"},
		{"primitive unknown", "unknown"},
		{"primitive void", "void"},
		{"primitive never", "never"},
		{"primitive string", "string"},
		{"primitive number", "number"},
		{"primitive boolean", "boolean"},
		{"primitive bigint", "bigint"},
		{"primitive symbol", "symbol"},
		{"primitive object", "object"},

		// Literal types
		{"string literal", `"hello"`},
		{"number literal", "42"},
		{"negative number literal", "-3.14"},
		{"boolean literal true", "true"},
		{"boolean literal false", "false"},

		// Type references
		{"simple type reference", "Foo"},
		{"type reference with one arg", "Array<string>"},
		{"type reference with multiple args", "Map<string, number>"},
		{"qualified type reference", "Foo.Bar"},
		{"nested qualified type reference", "A.B.C"},
		{"qualified with type args", "Foo.Bar<T>"},

		// Array types
		{"array type", "string[]"},
		{"array of array", "number[][]"},
		{"readonly array", "readonly string[]"},

		// Tuple types
		{"simple tuple", "[string, number]"},
		{"tuple with one element", "[boolean]"},
		{"tuple with three elements", "[string, number, boolean]"},
		{"empty tuple", "[]"},
		{"tuple with rest element", "[string, ...number[]]"},

		// Union types
		{"union of two types", "string | number"},
		{"union of three types", "string | number | boolean"},
		{"union with null", "string | null"},
		{"union with undefined", "number | undefined"},

		// Intersection types
		{"intersection of two types", "Foo & Bar"},
		{"intersection of three types", "A & B & C"},
		{"intersection with object types", "{a: string} & {b: number}"},

		// Function types
		{"simple function", "() => void"},
		{"function with one param", "(x: number) => string"},
		{"function with multiple params", "(x: number, y: string) => boolean"},
		{"function with optional param", "(x?: number) => void"},
		{"function with rest param", "(...args: string[]) => void"},
		{"function with type params", "<T>(x: T) => T"},
		{"function with constrained type param", "<T extends string>(x: T) => T"},

		// Constructor types
		{"simple constructor", "new () => Foo"},
		{"constructor with params", "new (x: number) => Bar"},
		{"abstract constructor", "abstract new () => Baz"},

		// Object types
		{"empty object", "{}"},
		{"object with one property", "{name: string}"},
		{"object with multiple properties", "{name: string, age: number}"},
		{"object with optional property", "{name?: string}"},
		{"object with readonly property", "{readonly id: number}"},

		// Indexed access types
		{"simple indexed access", "T[K]"},
		{"indexed with string literal", `T["name"]`},
		{"indexed with number literal", "T[0]"},
		{"nested indexed access", "T[K][P]"},

		// Conditional types
		{"simple conditional", "T extends U ? X : Y"},
		{"conditional with primitives", "T extends string ? true : false"},
		{"nested conditional", "T extends U ? (X extends Y ? A : B) : Z"},

		// Infer types
		{"infer in conditional", "T extends Array<infer U> ? U : T"},
		{"multiple infer", "T extends (arg: infer A) => infer R ? R : never"},

		// Template literal types
		{"simple template literal", "`hello`"},
		{"template with one substitution", "`hello ${string}`"},
		{"template with multiple substitutions", "`${string}-${number}`"},

		// KeyOf types
		{"simple keyof", "keyof T"},
		{"keyof object", "keyof {a: string, b: number}"},

		// TypeOf types
		{"typeof identifier", "typeof foo"},
		{"typeof qualified", "typeof Foo.bar"},

		// Mapped types
		{"simple mapped type", "{[K in T]: U}"},
		{"mapped type with readonly", "{readonly [K in T]: U}"},
		{"mapped type with optional", "{[K in T]?: U}"},
		{"mapped type with readonly and optional", "{readonly [K in T]?: U}"},
		{"mapped type with remove readonly", "{-readonly [K in T]: U}"},
		{"mapped type with remove optional", "{[K in T]-?: U}"},
		{"mapped type with as clause", "{[K in T as `get${K}`]: U}"},

		// Import types
		{"import type", `import("module")`},
		{"import type with member", `import("module").Foo`},
		{"import type with type args", `import("module").Foo<T>`},

		// Type predicates
		{"type predicate", "x is string"},

		// This type
		{"this type", "this"},

		// Rest and optional
		{"rest type in tuple", "[...string[]]"},
		{"optional type in tuple", "[string?]"},

		// Complex combinations
		{"union of functions", "(() => string) | ((x: number) => boolean)"},
		{"array of union", "(string | number)[]"},
		{"union of arrays", "string[] | number[]"},
		{"function returning union", "() => string | number"},
		{"conditional with union", "T extends string | number ? true : false"},
		{"indexed access of union", "(A | B)[K]"},
		{"generic with multiple constraints", "<T extends string, U extends number>(x: T, y: U) => void"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &ast.Source{
				Path:     "test.d.ts",
				Contents: tt.input,
				ID:       0,
			}
			parser := dts_parser.NewDtsParser(source)
			dtsTypeAnn := parser.ParseTypeAnn()

			if dtsTypeAnn == nil {
				t.Fatalf("Failed to parse .d.ts type: %s", tt.input)
			}

			// Convert to Escalier AST
			escalierTypeAnn, err := convertTypeAnn(dtsTypeAnn)

			if err != nil {
				t.Fatalf("Failed to convert type annotation: %s, error: %v", tt.input, err)
			}

			if escalierTypeAnn == nil {
				t.Fatalf("Failed to convert type annotation (nil result): %s", tt.input)
			}

			snaps.MatchSnapshot(t, escalierTypeAnn)
		})
	}
}

func TestConvertMethodDecl(t *testing.T) {
	tests := []struct {
		name       string
		classInput string
		methodIdx  int
	}{
		{
			name:       "simple method",
			classInput: "declare class Test { foo(x: number): string }",
			methodIdx:  0,
		},
		{
			name:       "static method",
			classInput: "declare class Test { static create(): void }",
			methodIdx:  0,
		},
		{
			name:       "private method",
			classInput: "declare class Test { private helper(): void }",
			methodIdx:  0,
		},
		{
			name:       "async method",
			classInput: "declare class Test { async fetchData(): Promise<void> }",
			methodIdx:  0,
		},
		{
			name:       "method with type parameters",
			classInput: "declare class Test { map<T>(fn: () => T): T }",
			methodIdx:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &ast.Source{
				Path:     "test.d.ts",
				Contents: tt.classInput,
				ID:       0,
			}
			parser := dts_parser.NewDtsParser(source)
			module, errors := parser.ParseModule()

			if len(errors) > 0 {
				t.Fatalf("Parse errors: %v", errors)
			}

			if len(module.Statements) == 0 {
				t.Fatal("No statements parsed")
			}

			classDecl, ok := module.Statements[0].(*dts_parser.ClassDecl)
			if !ok {
				t.Fatalf("Expected ClassDecl, got %T", module.Statements[0])
			}

			if tt.methodIdx >= len(classDecl.Members) {
				t.Fatalf("Method index %d out of range (have %d members)", tt.methodIdx, len(classDecl.Members))
			}

			methodDecl, ok := classDecl.Members[tt.methodIdx].(*dts_parser.MethodDecl)
			if !ok {
				t.Fatalf("Expected MethodDecl at index %d, got %T", tt.methodIdx, classDecl.Members[tt.methodIdx])
			}

			result, err := convertMethodDecl(methodDecl)
			if err != nil {
				t.Fatalf("convertMethodDecl failed: %v", err)
			}

			snaps.MatchSnapshot(t, result)
		})
	}
}

func TestConvertPropertyDecl(t *testing.T) {
	tests := []struct {
		name       string
		classInput string
		propIdx    int
	}{
		{
			name:       "simple property",
			classInput: "declare class Test { count: number }",
			propIdx:    0,
		},
		{
			name:       "readonly property",
			classInput: "declare class Test { readonly id: string }",
			propIdx:    0,
		},
		{
			name:       "static property",
			classInput: "declare class Test { static version: string }",
			propIdx:    0,
		},
		{
			name:       "private property",
			classInput: "declare class Test { private secret: string }",
			propIdx:    0,
		},
		{
			name:       "optional property",
			classInput: "declare class Test { description?: string }",
			propIdx:    0,
		},
		{
			name:       "static readonly property",
			classInput: "declare class Test { static readonly MAX_SIZE: number }",
			propIdx:    0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &ast.Source{
				Path:     "test.d.ts",
				Contents: tt.classInput,
				ID:       0,
			}
			parser := dts_parser.NewDtsParser(source)
			module, errors := parser.ParseModule()

			if len(errors) > 0 {
				t.Fatalf("Parse errors: %v", errors)
			}

			if len(module.Statements) == 0 {
				t.Fatal("No statements parsed")
			}

			classDecl, ok := module.Statements[0].(*dts_parser.ClassDecl)
			if !ok {
				t.Fatalf("Expected ClassDecl, got %T", module.Statements[0])
			}

			if tt.propIdx >= len(classDecl.Members) {
				t.Fatalf("Property index %d out of range (have %d members)", tt.propIdx, len(classDecl.Members))
			}

			propDecl, ok := classDecl.Members[tt.propIdx].(*dts_parser.PropertyDecl)
			if !ok {
				t.Fatalf("Expected PropertyDecl at index %d, got %T", tt.propIdx, classDecl.Members[tt.propIdx])
			}

			result, err := convertPropertyDecl(propDecl)
			if err != nil {
				t.Fatalf("convertPropertyDecl failed: %v", err)
			}

			snaps.MatchSnapshot(t, result)
		})
	}
}

func TestConvertGetterDecl(t *testing.T) {
	tests := []struct {
		name       string
		classInput string
		getterIdx  int
	}{
		{
			name:       "simple getter",
			classInput: "declare class Test { get value(): number }",
			getterIdx:  0,
		},
		{
			name:       "static getter",
			classInput: "declare class Test { static get instance(): MyClass }",
			getterIdx:  0,
		},
		{
			name:       "private getter",
			classInput: "declare class Test { private get internalState(): any }",
			getterIdx:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &ast.Source{
				Path:     "test.d.ts",
				Contents: tt.classInput,
				ID:       0,
			}
			parser := dts_parser.NewDtsParser(source)
			module, errors := parser.ParseModule()

			if len(errors) > 0 {
				t.Fatalf("Parse errors: %v", errors)
			}

			if len(module.Statements) == 0 {
				t.Fatal("No statements parsed")
			}

			classDecl, ok := module.Statements[0].(*dts_parser.ClassDecl)
			if !ok {
				t.Fatalf("Expected ClassDecl, got %T", module.Statements[0])
			}

			if tt.getterIdx >= len(classDecl.Members) {
				t.Fatalf("Getter index %d out of range (have %d members)", tt.getterIdx, len(classDecl.Members))
			}

			getterDecl, ok := classDecl.Members[tt.getterIdx].(*dts_parser.GetterDecl)
			if !ok {
				t.Fatalf("Expected GetterDecl at index %d, got %T", tt.getterIdx, classDecl.Members[tt.getterIdx])
			}

			result, err := convertGetterDecl(getterDecl)
			if err != nil {
				t.Fatalf("convertGetterDecl failed: %v", err)
			}

			snaps.MatchSnapshot(t, result)
		})
	}
}

func TestConvertSetterDecl(t *testing.T) {
	tests := []struct {
		name       string
		classInput string
		setterIdx  int
	}{
		{
			name:       "simple setter",
			classInput: "declare class Test { set value(v: number) }",
			setterIdx:  0,
		},
		{
			name:       "static setter",
			classInput: "declare class Test { static set config(cfg: object) }",
			setterIdx:  0,
		},
		{
			name:       "private setter",
			classInput: "declare class Test { private set data(d: any) }",
			setterIdx:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &ast.Source{
				Path:     "test.d.ts",
				Contents: tt.classInput,
				ID:       0,
			}
			parser := dts_parser.NewDtsParser(source)
			module, errors := parser.ParseModule()

			if len(errors) > 0 {
				t.Fatalf("Parse errors: %v", errors)
			}

			if len(module.Statements) == 0 {
				t.Fatal("No statements parsed")
			}

			classDecl, ok := module.Statements[0].(*dts_parser.ClassDecl)
			if !ok {
				t.Fatalf("Expected ClassDecl, got %T", module.Statements[0])
			}

			if tt.setterIdx >= len(classDecl.Members) {
				t.Fatalf("Setter index %d out of range (have %d members)", tt.setterIdx, len(classDecl.Members))
			}

			setterDecl, ok := classDecl.Members[tt.setterIdx].(*dts_parser.SetterDecl)
			if !ok {
				t.Fatalf("Expected SetterDecl at index %d, got %T", tt.setterIdx, classDecl.Members[tt.setterIdx])
			}

			result, err := convertSetterDecl(setterDecl)
			if err != nil {
				t.Fatalf("convertSetterDecl failed: %v", err)
			}

			snaps.MatchSnapshot(t, result)
		})
	}
}
