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
		expected []string
	}{
		"VarDecl_SimpleIdent": {
			input: `
				val a = 5
				var b = 10
			`,
			expected: []string{"a", "b"},
		},
		"VarDecl_TupleDestructuring": {
			input: `
				val [x, y] = [1, 2]
				var [first, second] = getTuple()
			`,
			expected: []string{"x", "y", "first", "second"},
		},
		"VarDecl_ObjectDestructuring": {
			input: `
				val {name, age} = person
				var {x: width, y: height} = dimensions
			`,
			expected: []string{"name", "age", "width", "height"},
		},
		"VarDecl_ObjectShorthand": {
			input: `
				val {foo, bar} = obj
			`,
			expected: []string{"foo", "bar"},
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
			expected: []string{"add", "multiply"},
		},
		"TypeDecl_Simple": {
			input: `
				type Point = {x: number, y: number}
				type Color = "red" | "green" | "blue"
			`,
			expected: []string{"Point", "Color"},
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
			expected: []string{"User", "defaultUser", "createUser", "admin", "guest"},
		},
		"Empty_Module": {
			input:    ``,
			expected: []string{},
		},
		"VarDecl_NestedPatterns": {
			input: `
				val {user: {name, profile: {email}}} = data
				val [first, {x, y}] = coordinates
			`,
			expected: []string{"name", "email", "first", "x", "y"},
		},
		"VarDecl_RestPatterns": {
			input: `
				val [head, ...tail] = list
				val {id, ...rest} = object
			`,
			expected: []string{"head", "tail", "id", "rest"},
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

			// Extract binding names from the map
			actualBindings := make([]string, 0, len(bindings))
			for name := range bindings {
				actualBindings = append(actualBindings, name)
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
		expected    []string
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
			expected: []string{},
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
			expected: []string{},
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
			expected: []string{},
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
			expected: []string{},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			module := test.setupModule()
			bindings := FindModuleBindings(module)

			// Extract binding names from the map
			actualBindings := make([]string, 0, len(bindings))
			for name := range bindings {
				actualBindings = append(actualBindings, name)
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
		validBindings []string
		expectedDeps  []string
		declType      string // "var", "func", or "type"
	}{
		"VarDecl_SimpleDependency": {
			declCode:      `val result = globalVar + 5`,
			validBindings: []string{"globalVar", "otherVar"},
			expectedDeps:  []string{"globalVar"},
			declType:      "var",
		},
		"VarDecl_MultipleDependencies": {
			declCode:      `val result = globalVar + otherVar * thirdVar`,
			validBindings: []string{"globalVar", "otherVar", "thirdVar", "unused"},
			expectedDeps:  []string{"globalVar", "otherVar", "thirdVar"},
			declType:      "var",
		},
		"VarDecl_NoDependencies": {
			declCode:      `val result = 42`,
			validBindings: []string{"globalVar", "otherVar"},
			expectedDeps:  []string{},
			declType:      "var",
		},
		"VarDecl_NonValidDependency": {
			declCode:      `val result = unknownVar + 5`,
			validBindings: []string{"globalVar", "otherVar"},
			expectedDeps:  []string{},
			declType:      "var",
		},
		"VarDecl_WithFunctionCall": {
			declCode:      `val result = myFunction(globalVar, 5)`,
			validBindings: []string{"myFunction", "globalVar"},
			expectedDeps:  []string{"myFunction", "globalVar"},
			declType:      "var",
		},
		"VarDecl_IfElseScope": {
			declCode: `val result = if cond {
				val globalVar = true
				globalVar
			} else {
				false
			}`,
			validBindings: []string{"globalVar", "cond"},
			expectedDeps:  []string{"cond"},
			declType:      "var",
		},
		"FuncDecl_SimpleDependency": {
			declCode: `fn testFunc(a, b) {
				return globalVar + a
			}`,
			validBindings: []string{"globalVar", "otherVar"},
			expectedDeps:  []string{"globalVar"},
			declType:      "func",
		},
		"FuncDecl_ParameterShadowing": {
			declCode: `fn testFunc(globalVar, b) {
				return globalVar + b
			}`,
			validBindings: []string{"globalVar", "otherVar"},
			expectedDeps:  []string{}, // globalVar is shadowed by parameter
			declType:      "func",
		},
		"FuncDecl_LocalVariableShadowing": {
			declCode: `fn testFunc(a, b) {
				val globalVar = 10
				return globalVar + a
			}`,
			validBindings: []string{"globalVar", "otherVar"},
			expectedDeps:  []string{}, // globalVar is shadowed by local variable
			declType:      "func",
		},
		"FuncDecl_NestedScope": {
			declCode: `fn testFunc(a, b) {
				val local = globalVar
				return fn(x) {
					return local + otherVar + x
				}
			}`,
			validBindings: []string{"globalVar", "otherVar"},
			expectedDeps:  []string{"globalVar", "otherVar"},
			declType:      "func",
		},
		"FuncDecl_ParameterInNestedFunction": {
			declCode: `fn testFunc(a, b) {
				return fn(c) {
					return a + c + globalVar
				}
			}`,
			validBindings: []string{"globalVar", "a"},
			expectedDeps:  []string{"globalVar"}, // 'a' is a parameter, not a dependency
			declType:      "func",
		},
		"TypeDecl_SimpleDependency": {
			declCode:      `type MyType = BaseType`,
			validBindings: []string{"BaseType", "OtherType"},
			expectedDeps:  []string{"BaseType"},
			declType:      "type",
		},
		"TypeDecl_ComplexType": {
			declCode:      `type MyType = {field: BaseType, other: OtherType}`,
			validBindings: []string{"BaseType", "OtherType", "UnusedType"},
			expectedDeps:  []string{"BaseType", "OtherType"},
			declType:      "type",
		},
		"TypeDecl_NoDependencies": {
			declCode:      `type MyType = string`,
			validBindings: []string{"BaseType", "OtherType"},
			expectedDeps:  []string{},
			declType:      "type",
		},
		"VarDecl_NoInitializer": {
			declCode:      `declare var result: number`,
			validBindings: []string{"globalVar"},
			expectedDeps:  []string{},
			declType:      "var",
		},
		"FuncDecl_NoBody": {
			declCode: `fn testFunc(a, b) {
				return a + b
			}`,
			validBindings: []string{"globalVar"},
			expectedDeps:  []string{},
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
			validBindings := set.NewSet[string]()
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
		validBindings []string
		expectedDeps  []string
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
			validBindings: []string{"globalVar"},
			expectedDeps:  []string{},
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
			validBindings: []string{"globalVar"},
			expectedDeps:  []string{},
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
			validBindings: []string{}, // No valid bindings
			expectedDeps:  []string{},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			decl := test.setupDecl()

			// Create valid bindings set
			validBindings := set.NewSet[string]()
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
