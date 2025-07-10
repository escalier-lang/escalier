package checker

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/stretchr/testify/assert"
)

func TestFindModuleBindings(t *testing.T) {
	tests := map[string]struct {
		input    string
		expected []DepBinding
	}{
		"VarDecl_SimpleIdent": {
			input: `
				val a = 5
				var b = 10
			`,
			expected: []DepBinding{
				{Name: "a", Kind: DepKindValue},
				{Name: "b", Kind: DepKindValue},
			},
		},
		"VarDecl_TupleDestructuring": {
			input: `
				val [x, y] = [1, 2]
				var [first, second] = getTuple()
			`,
			expected: []DepBinding{
				{Name: "x", Kind: DepKindValue},
				{Name: "y", Kind: DepKindValue},
				{Name: "first", Kind: DepKindValue},
				{Name: "second", Kind: DepKindValue},
			},
		},
		"VarDecl_ObjectDestructuring": {
			input: `
				val {name, age} = person
				var {x: width, y: height} = dimensions
			`,
			expected: []DepBinding{
				{Name: "name", Kind: DepKindValue},
				{Name: "age", Kind: DepKindValue},
				{Name: "width", Kind: DepKindValue},
				{Name: "height", Kind: DepKindValue},
			},
		},
		"VarDecl_ObjectShorthand": {
			input: `
				val {foo, bar} = obj
			`,
			expected: []DepBinding{
				{Name: "foo", Kind: DepKindValue},
				{Name: "bar", Kind: DepKindValue},
			},
		},
		"FuncDecl_Simple": {
			input: `
				fn add(a, b) {
					return a + b
				}
				fn multiply(x, y) {
					return x * y
				}
			`,
			expected: []DepBinding{
				{Name: "add", Kind: DepKindValue},
				{Name: "multiply", Kind: DepKindValue},
			},
		},
		"TypeDecl_Simple": {
			input: `
				type Point = {x: number, y: number}
				type Color = "red" | "green" | "blue"
			`,
			expected: []DepBinding{
				{Name: "Point", Kind: DepKindType},
				{Name: "Color", Kind: DepKindType},
			},
		},
		"Mixed_Declarations": {
			input: `
				type User = {name: string, age: number}
				val defaultUser = {name: "John", age: 30}
				fn createUser(name, age) {
					return {name, age}
				}
				var [admin, guest] = [createUser("admin", 25), defaultUser]
			`,
			expected: []DepBinding{
				{Name: "User", Kind: DepKindType},
				{Name: "defaultUser", Kind: DepKindValue},
				{Name: "createUser", Kind: DepKindValue},
				{Name: "admin", Kind: DepKindValue},
				{Name: "guest", Kind: DepKindValue},
			},
		},
		"Empty_Module": {
			input:    ``,
			expected: []DepBinding{},
		},
		"VarDecl_NestedPatterns": {
			input: `
				val {user: {name, profile: {email}}} = data
				val [first, {x, y}] = coordinates
			`,
			expected: []DepBinding{
				{Name: "name", Kind: DepKindValue},
				{Name: "email", Kind: DepKindValue},
				{Name: "first", Kind: DepKindValue},
				{Name: "x", Kind: DepKindValue},
				{Name: "y", Kind: DepKindValue},
			},
		},
		"VarDecl_RestPatterns": {
			input: `
				val [head, ...tail] = list
				val {id, ...rest} = object
			`,
			expected: []DepBinding{
				{Name: "head", Kind: DepKindValue},
				{Name: "tail", Kind: DepKindValue},
				{Name: "id", Kind: DepKindValue},
				{Name: "rest", Kind: DepKindValue},
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := &ast.Source{
				ID:       0,
				Path:     "test.esc",
				Contents: test.input,
			}

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			parser := parser.NewParser(ctx, source)
			module, errors := parser.ParseModule()

			// Ensure parsing was successful
			assert.Len(t, errors, 0, "Parser errors: %v", errors)

			// Find bindings
			bindings := FindModuleBindings(module)

			// Extract binding objects from the map
			actualBindings := make([]DepBinding, 0, len(bindings))
			for binding := range bindings {
				actualBindings = append(actualBindings, binding)
			}

			// Sort both slices for reliable comparison
			assert.ElementsMatch(t, test.expected, actualBindings,
				"Expected bindings %v, got %v", test.expected, actualBindings)
		})
	}
}

func TestFindModuleBindings_EmptyNames(t *testing.T) {
	// Test edge cases with empty or missing names
	tests := map[string]struct {
		setupModule func() *ast.Module
		expected    []DepBinding
	}{
		"FuncDecl_NilName": {
			setupModule: func() *ast.Module {
				return &ast.Module{
					Decls: []ast.Decl{
						&ast.FuncDecl{
							Name: nil, // Function with nil name
							FuncSig: ast.FuncSig{
								TypeParams: []*ast.TypeParam{},
								Params:     []*ast.Param{},
								Return:     nil,
								Throws:     nil,
							},
							Body: &ast.Block{
								Stmts: []ast.Stmt{},
								Span: ast.Span{
									Start:    ast.Location{Line: 1, Column: 1},
									End:      ast.Location{Line: 1, Column: 1},
									SourceID: 0,
								},
							},
						},
					},
				}
			},
			expected: []DepBinding{},
		},
		"FuncDecl_EmptyName": {
			setupModule: func() *ast.Module {
				return &ast.Module{
					Decls: []ast.Decl{
						&ast.FuncDecl{
							Name: &ast.Ident{Name: ""}, // Function with empty name
							FuncSig: ast.FuncSig{
								TypeParams: []*ast.TypeParam{},
								Params:     []*ast.Param{},
								Return:     nil,
								Throws:     nil,
							},
							Body: &ast.Block{
								Stmts: []ast.Stmt{},
								Span: ast.Span{
									Start:    ast.Location{Line: 1, Column: 1},
									End:      ast.Location{Line: 1, Column: 1},
									SourceID: 0,
								},
							},
						},
					},
				}
			},
			expected: []DepBinding{},
		},
		"TypeDecl_NilName": {
			setupModule: func() *ast.Module {
				return &ast.Module{
					Decls: []ast.Decl{
						&ast.TypeDecl{
							Name:       nil, // Type with nil name
							TypeParams: []*ast.TypeParam{},
							TypeAnn: &ast.LitTypeAnn{
								Lit: &ast.StrLit{Value: "test"},
							},
						},
					},
				}
			},
			expected: []DepBinding{},
		},
		"TypeDecl_EmptyName": {
			setupModule: func() *ast.Module {
				return &ast.Module{
					Decls: []ast.Decl{
						&ast.TypeDecl{
							Name:       &ast.Ident{Name: ""}, // Type with empty name
							TypeParams: []*ast.TypeParam{},
							TypeAnn: &ast.LitTypeAnn{
								Lit: &ast.StrLit{Value: "test"},
							},
						},
					},
				}
			},
			expected: []DepBinding{},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			module := test.setupModule()
			bindings := FindModuleBindings(module)

			// Extract binding objects from the map
			actualBindings := make([]DepBinding, 0, len(bindings))
			for binding := range bindings {
				actualBindings = append(actualBindings, binding)
			}

			assert.ElementsMatch(t, test.expected, actualBindings,
				"Expected bindings %v, got %v", test.expected, actualBindings)
		})
	}
}

func TestFindModuleBindings_NilModule(t *testing.T) {
	// Test with nil module
	assert.Panics(t, func() {
		FindModuleBindings(nil)
	}, "Should panic when module is nil")
}

func TestFindDeclDependencies(t *testing.T) {
	tests := map[string]struct {
		declCode      string
		validBindings []DepBinding
		expectedDeps  []DepBinding
		declType      string // "var", "func", or "type"
	}{
		"VarDecl_SimpleDependency": {
			declCode:      `val result = globalVar + 5`,
			validBindings: []DepBinding{{Name: "globalVar", Kind: DepKindValue}, {Name: "otherVar", Kind: DepKindValue}},
			expectedDeps:  []DepBinding{{Name: "globalVar", Kind: DepKindValue}},
			declType:      "var",
		},
		"VarDecl_MultipleDependencies": {
			declCode:      `val result = globalVar + otherVar * thirdVar`,
			validBindings: []DepBinding{{Name: "globalVar", Kind: DepKindValue}, {Name: "otherVar", Kind: DepKindValue}, {Name: "thirdVar", Kind: DepKindValue}, {Name: "unused", Kind: DepKindValue}},
			expectedDeps:  []DepBinding{{Name: "globalVar", Kind: DepKindValue}, {Name: "otherVar", Kind: DepKindValue}, {Name: "thirdVar", Kind: DepKindValue}},
			declType:      "var",
		},
		"VarDecl_NoDependencies": {
			declCode:      `val result = 42`,
			validBindings: []DepBinding{{Name: "globalVar", Kind: DepKindValue}, {Name: "otherVar", Kind: DepKindValue}},
			expectedDeps:  []DepBinding{},
			declType:      "var",
		},
		"VarDecl_NonValidDependency": {
			declCode:      `val result = unknownVar + 5`,
			validBindings: []DepBinding{{Name: "globalVar", Kind: DepKindValue}, {Name: "otherVar", Kind: DepKindValue}},
			expectedDeps:  []DepBinding{},
			declType:      "var",
		},
		"VarDecl_WithFunctionCall": {
			declCode:      `val result = myFunction(globalVar, 5)`,
			validBindings: []DepBinding{{Name: "myFunction", Kind: DepKindValue}, {Name: "globalVar", Kind: DepKindValue}},
			expectedDeps:  []DepBinding{{Name: "myFunction", Kind: DepKindValue}, {Name: "globalVar", Kind: DepKindValue}},
			declType:      "var",
		},
		"VarDecl_IfElseScope": {
			declCode: `val result = if cond {
				val globalVar = true
				globalVar
			} else {
				false
			}`,
			validBindings: []DepBinding{{Name: "globalVar", Kind: DepKindValue}, {Name: "cond", Kind: DepKindValue}},
			expectedDeps:  []DepBinding{{Name: "cond", Kind: DepKindValue}},
			declType:      "var",
		},
		"FuncDecl_SimpleDependency": {
			declCode: `fn testFunc(a, b) {
				return globalVar + a
			}`,
			validBindings: []DepBinding{{Name: "globalVar", Kind: DepKindValue}, {Name: "otherVar", Kind: DepKindValue}},
			expectedDeps:  []DepBinding{{Name: "globalVar", Kind: DepKindValue}},
			declType:      "func",
		},
		"FuncDecl_ParameterShadowing": {
			declCode: `fn testFunc(globalVar, b) {
				return globalVar + b
			}`,
			validBindings: []DepBinding{{Name: "globalVar", Kind: DepKindValue}, {Name: "otherVar", Kind: DepKindValue}},
			expectedDeps:  []DepBinding{}, // globalVar is shadowed by parameter
			declType:      "func",
		},
		"FuncDecl_LocalVariableShadowing": {
			declCode: `fn testFunc(a, b) {
				val globalVar = 10
				return globalVar + a
			}`,
			validBindings: []DepBinding{{Name: "globalVar", Kind: DepKindValue}, {Name: "otherVar", Kind: DepKindValue}},
			expectedDeps:  []DepBinding{}, // globalVar is shadowed by local variable
			declType:      "func",
		},
		"FuncDecl_NestedScope": {
			declCode: `fn testFunc(a, b) {
				val local = globalVar
				return fn(x) {
					return local + otherVar + x
				}
			}`,
			validBindings: []DepBinding{{Name: "globalVar", Kind: DepKindValue}, {Name: "otherVar", Kind: DepKindValue}},
			expectedDeps:  []DepBinding{{Name: "globalVar", Kind: DepKindValue}, {Name: "otherVar", Kind: DepKindValue}},
			declType:      "func",
		},
		"FuncDecl_ParameterInNestedFunction": {
			declCode: `fn testFunc(a, b) {
				return fn(c) {
					return a + c + globalVar
				}
			}`,
			validBindings: []DepBinding{{Name: "globalVar", Kind: DepKindValue}, {Name: "a", Kind: DepKindValue}},
			expectedDeps:  []DepBinding{{Name: "globalVar", Kind: DepKindValue}}, // 'a' is a parameter, not a dependency
			declType:      "func",
		},
		"TypeDecl_SimpleDependency": {
			declCode:      `type MyType = BaseType`,
			validBindings: []DepBinding{{Name: "BaseType", Kind: DepKindType}, {Name: "OtherType", Kind: DepKindType}},
			expectedDeps:  []DepBinding{{Name: "BaseType", Kind: DepKindType}},
			declType:      "type",
		},
		"TypeDecl_ComplexType": {
			declCode:      `type MyType = {field: BaseType, other: OtherType}`,
			validBindings: []DepBinding{{Name: "BaseType", Kind: DepKindType}, {Name: "OtherType", Kind: DepKindType}, {Name: "UnusedType", Kind: DepKindType}},
			expectedDeps:  []DepBinding{{Name: "BaseType", Kind: DepKindType}, {Name: "OtherType", Kind: DepKindType}},
			declType:      "type",
		},
		"TypeDecl_NoDependencies": {
			declCode:      `type MyType = string`,
			validBindings: []DepBinding{{Name: "BaseType", Kind: DepKindType}, {Name: "OtherType", Kind: DepKindType}},
			expectedDeps:  []DepBinding{},
			declType:      "type",
		},
		"VarDecl_NoInitializer": {
			declCode:      `declare var result: number`,
			validBindings: []DepBinding{{Name: "globalVar", Kind: DepKindValue}},
			expectedDeps:  []DepBinding{},
			declType:      "var",
		},
		"FuncDecl_NoBody": {
			declCode: `fn testFunc(a, b) {
				return a + b
			}`,
			validBindings: []DepBinding{{Name: "globalVar", Kind: DepKindValue}},
			expectedDeps:  []DepBinding{},
			declType:      "func",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// Create a module with the declaration
			var moduleCode string
			switch test.declType {
			case "var":
				moduleCode = test.declCode
			case "func":
				moduleCode = test.declCode
			case "type":
				moduleCode = test.declCode
			}

			source := &ast.Source{
				ID:       0,
				Path:     "test.esc",
				Contents: moduleCode,
			}

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			parser := parser.NewParser(ctx, source)
			module, errors := parser.ParseModule()

			// Ensure parsing was successful
			assert.Len(t, errors, 0, "Parser errors: %v", errors)
			assert.NotNil(t, module, "Module should not be nil")
			assert.Len(t, module.Decls, 1, "Module should have exactly one declaration")

			// Create valid bindings set
			validBindings := set.NewSet[DepBinding]()
			for _, binding := range test.validBindings {
				validBindings.Add(binding)
			}

			// Find dependencies
			dependencies := FindDeclDependencies(module.Decls[0], validBindings)

			// Convert dependencies set to slice for comparison
			actualDeps := dependencies.ToSlice()

			assert.ElementsMatch(t, test.expectedDeps, actualDeps,
				"Expected dependencies %v, got %v", test.expectedDeps, actualDeps)
		})
	}
}

func TestFindDeclDependencies_EdgeCases(t *testing.T) {
	tests := map[string]struct {
		setupDecl     func() ast.Decl
		validBindings []DepBinding
		expectedDeps  []DepBinding
	}{
		"VarDecl_NilInit": {
			setupDecl: func() ast.Decl {
				return &ast.VarDecl{
					Kind: ast.ValKind,
					Pattern: &ast.IdentPat{
						Name:    "test",
						Default: nil,
					},
					Init: nil, // No initializer
					TypeAnn: &ast.LitTypeAnn{
						Lit: &ast.StrLit{Value: "string"},
					},
				}
			},
			validBindings: []DepBinding{{Name: "globalVar", Kind: DepKindValue}},
			expectedDeps:  []DepBinding{},
		},
		"FuncDecl_NilBody": {
			setupDecl: func() ast.Decl {
				return &ast.FuncDecl{
					Name: &ast.Ident{Name: "testFunc"},
					FuncSig: ast.FuncSig{
						TypeParams: []*ast.TypeParam{},
						Params:     []*ast.Param{},
						Return:     nil,
						Throws:     nil,
					},
					Body: nil, // No body
				}
			},
			validBindings: []DepBinding{{Name: "globalVar", Kind: DepKindValue}},
			expectedDeps:  []DepBinding{},
		},
		"EmptyValidBindings": {
			setupDecl: func() ast.Decl {
				return &ast.VarDecl{
					Kind: ast.ValKind,
					Pattern: &ast.IdentPat{
						Name:    "test",
						Default: nil,
					},
					Init: &ast.IdentExpr{
						Name:   "someVar",
						Source: nil,
					},
					TypeAnn: nil,
				}
			},
			validBindings: []DepBinding{}, // No valid bindings
			expectedDeps:  []DepBinding{},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			decl := test.setupDecl()

			// Create valid bindings set
			validBindings := set.NewSet[DepBinding]()
			for _, binding := range test.validBindings {
				validBindings.Add(binding)
			}

			// Find dependencies
			dependencies := FindDeclDependencies(decl, validBindings)

			// Convert dependencies set to slice for comparison
			actualDeps := dependencies.ToSlice()

			assert.ElementsMatch(t, test.expectedDeps, actualDeps,
				"Expected dependencies %v, got %v", test.expectedDeps, actualDeps)
		})
	}
}
