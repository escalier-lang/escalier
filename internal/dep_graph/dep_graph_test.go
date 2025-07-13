package dep_graph

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
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
			declarations, bindings := FindModuleBindings(module)
			_ = declarations // suppress unused variable warning for now

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
			declarations, bindings := FindModuleBindings(module)
			_ = declarations // suppress unused variable warning for now

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
		"VarDecl_IfElseScopeUseGlobalBeforeLocalDecl": {
			declCode: `val result = if cond {
				globalVar = true
				val globalVar = 5
			} else {
				false
			}`,
			validBindings: []DepBinding{{Name: "globalVar", Kind: DepKindValue}, {Name: "cond", Kind: DepKindValue}},
			expectedDeps:  []DepBinding{{Name: "globalVar", Kind: DepKindValue}, {Name: "cond", Kind: DepKindValue}},
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
		"VarDecl_WithTypeAnnotation": {
			declCode:      `val p: Point = {x: 5, y: 10}`,
			validBindings: []DepBinding{{Name: "x", Kind: DepKindValue}, {Name: "y", Kind: DepKindValue}, {Name: "Point", Kind: DepKindType}},
			expectedDeps:  []DepBinding{{Name: "Point", Kind: DepKindType}},
			declType:      "var",
		},
		"VarDecl_ComputedKeys": {
			declCode:      `val p: Point = {[x]: 5, [y]: 10}`,
			validBindings: []DepBinding{{Name: "x", Kind: DepKindValue}, {Name: "y", Kind: DepKindValue}, {Name: "Point", Kind: DepKindType}},
			expectedDeps:  []DepBinding{{Name: "x", Kind: DepKindValue}, {Name: "y", Kind: DepKindValue}, {Name: "Point", Kind: DepKindType}},
			declType:      "var",
		},
		"VarDecl_PropertyShorthand": {
			declCode:      `val p: Point = {x, y}`,
			validBindings: []DepBinding{{Name: "x", Kind: DepKindValue}, {Name: "y", Kind: DepKindValue}, {Name: "Point", Kind: DepKindType}},
			expectedDeps:  []DepBinding{{Name: "x", Kind: DepKindValue}, {Name: "y", Kind: DepKindValue}, {Name: "Point", Kind: DepKindType}},
			declType:      "var",
		},
		"VarDecl_IgnoresNonPropertyShorthandKeys": {
			declCode:      `val p: Point = {x: a, y: b}`,
			validBindings: []DepBinding{{Name: "a", Kind: DepKindValue}, {Name: "b", Kind: DepKindValue}, {Name: "Point", Kind: DepKindType}},
			expectedDeps:  []DepBinding{{Name: "a", Kind: DepKindValue}, {Name: "b", Kind: DepKindValue}, {Name: "Point", Kind: DepKindType}},
			declType:      "var",
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

			// Create valid bindings map (map from binding to mock DeclID)
			validBindings := make(map[DepBinding]DeclID)
			for i, binding := range test.validBindings {
				validBindings[binding] = DeclID(i + 100) // Use arbitrary DeclIDs starting from 100
			}

			// Find dependencies
			dependencies := FindDeclDependencies(module.Decls[0], validBindings)

			// Convert dependencies to bindings for comparison
			actualDeps := make([]DepBinding, 0)
			for declID := range dependencies {
				// Find the binding that corresponds to this DeclID
				for binding, id := range validBindings {
					if id == declID {
						actualDeps = append(actualDeps, binding)
						break
					}
				}
			}

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

			// Create valid bindings map (map from binding to mock DeclID)
			validBindings := make(map[DepBinding]DeclID)
			for i, binding := range test.validBindings {
				validBindings[binding] = DeclID(i + 100) // Use arbitrary DeclIDs starting from 100
			}

			// Find dependencies
			dependencies := FindDeclDependencies(decl, validBindings)

			// Convert dependencies to bindings for comparison
			actualDeps := make([]DepBinding, 0)
			for declID := range dependencies {
				// Find the binding that corresponds to this DeclID
				for binding, id := range validBindings {
					if id == declID {
						actualDeps = append(actualDeps, binding)
						break
					}
				}
			}

			assert.ElementsMatch(t, test.expectedDeps, actualDeps,
				"Expected dependencies %v, got %v", test.expectedDeps, actualDeps)
		})
	}
}

func TestBuildDepGraph(t *testing.T) {
	tests := map[string]struct {
		input                string
		expectedBindings     []DepBinding
		expectedDependencies [][2][]string // [bindings introduced by decl, dependencies]
	}{
		"Simple_Dependencies": {
			input: `
				type User = {name: string, age: number}
				val defaultAge = 18
				fn createUser(name) {
					return {name: name, age: defaultAge}
				}
				val admin = createUser("admin")
			`,
			expectedBindings: []DepBinding{
				{Name: "User", Kind: DepKindType},
				{Name: "defaultAge", Kind: DepKindValue},
				{Name: "createUser", Kind: DepKindValue},
				{Name: "admin", Kind: DepKindValue},
			},
			expectedDependencies: [][2][]string{
				{[]string{"User"}, []string{}},
				{[]string{"defaultAge"}, []string{}},
				{[]string{"createUser"}, []string{"defaultAge"}},
				{[]string{"admin"}, []string{"createUser"}},
			},
		},
		"Type_Dependencies": {
			input: `
				type Point = {x: number, y: number}
				type Shape = {center: Point, radius: number}
				val origin: Point = {x: 0, y: 0}
			`,
			expectedBindings: []DepBinding{
				{Name: "Point", Kind: DepKindType},
				{Name: "Shape", Kind: DepKindType},
				{Name: "origin", Kind: DepKindValue},
			},
			expectedDependencies: [][2][]string{
				{[]string{"Point"}, []string{}},
				{[]string{"Shape"}, []string{"Point"}},
				{[]string{"origin"}, []string{"Point"}},
			},
		},
		"No_Dependencies": {
			input: `
				val a = 5
				val b = 10
				type StringType = string
			`,
			expectedBindings: []DepBinding{
				{Name: "a", Kind: DepKindValue},
				{Name: "b", Kind: DepKindValue},
				{Name: "StringType", Kind: DepKindType},
			},
			expectedDependencies: [][2][]string{
				{[]string{"a"}, []string{}},
				{[]string{"b"}, []string{}},
				{[]string{"StringType"}, []string{}},
			},
		},
		"Tuple_Destructuring_Simple": {
			input: `
				val point = [10, 20]
				val [x, y] = point
				val sum = x + y
			`,
			expectedBindings: []DepBinding{
				{Name: "point", Kind: DepKindValue},
				{Name: "x", Kind: DepKindValue},
				{Name: "y", Kind: DepKindValue},
				{Name: "sum", Kind: DepKindValue},
			},
			expectedDependencies: [][2][]string{
				{[]string{"point"}, []string{}},
				{[]string{"x", "y"}, []string{"point"}},
				{[]string{"sum"}, []string{"x", "y"}},
			},
		},
		"Object_Destructuring_Simple": {
			input: `
				val user = {name: "Alice", age: 30}
				val {name, age} = user
				val greeting = "Hello " + name
			`,
			expectedBindings: []DepBinding{
				{Name: "user", Kind: DepKindValue},
				{Name: "name", Kind: DepKindValue},
				{Name: "age", Kind: DepKindValue},
				{Name: "greeting", Kind: DepKindValue},
			},
			expectedDependencies: [][2][]string{
				{[]string{"user"}, []string{}},
				{[]string{"name", "age"}, []string{"user"}},
				{[]string{"greeting"}, []string{"name", "age"}}, // Updated: now depends on the entire destructuring declaration
			},
		},
		"Object_Destructuring_With_Rename": {
			input: `
				val config = {width: 100, height: 50}
				val {width: w, height: h} = config
				val area = w * h
			`,
			expectedBindings: []DepBinding{
				{Name: "config", Kind: DepKindValue},
				{Name: "w", Kind: DepKindValue},
				{Name: "h", Kind: DepKindValue},
				{Name: "area", Kind: DepKindValue},
			},
			expectedDependencies: [][2][]string{
				{[]string{"config"}, []string{}},
				{[]string{"w", "h"}, []string{"config"}},
				{[]string{"area"}, []string{"w", "h"}},
			},
		},
		"Nested_Destructuring": {
			input: `
				val data = {coords: [5, 10], info: {id: 1}}
				val {coords: [x, y], info: {id}} = data
				val result = x + y + id
			`,
			expectedBindings: []DepBinding{
				{Name: "data", Kind: DepKindValue},
				{Name: "x", Kind: DepKindValue},
				{Name: "y", Kind: DepKindValue},
				{Name: "id", Kind: DepKindValue},
				{Name: "result", Kind: DepKindValue},
			},
			expectedDependencies: [][2][]string{
				{[]string{"data"}, []string{}},
				{[]string{"x", "y", "id"}, []string{"data"}},
				{[]string{"result"}, []string{"x", "y", "id"}},
			},
		},
		"Rest_Pattern_Destructuring": {
			input: `
				val numbers = [1, 2, 3, 4, 5]
				val [first, second, ...rest] = numbers
				val restSum = rest
				val total = first + second
			`,
			expectedBindings: []DepBinding{
				{Name: "numbers", Kind: DepKindValue},
				{Name: "first", Kind: DepKindValue},
				{Name: "second", Kind: DepKindValue},
				{Name: "rest", Kind: DepKindValue},
				{Name: "restSum", Kind: DepKindValue},
				{Name: "total", Kind: DepKindValue},
			},
			expectedDependencies: [][2][]string{
				{[]string{"numbers"}, []string{}},
				{[]string{"first", "second", "rest"}, []string{"numbers"}},
				{[]string{"restSum"}, []string{"first", "second", "rest"}}, // Updated: depends on the entire destructuring declaration
				{[]string{"total"}, []string{"first", "second", "rest"}},   // Updated: depends on the entire destructuring declaration
			},
		},
		"Mixed_Destructuring_With_Functions": {
			input: `
				fn getPoint() {
					return [100, 200]
				}
				val [startX, startY] = getPoint()
				fn getSize() {
					return {width: 50, height: 30}
				}
				val {width, height} = getSize()
				val area = width * height
				val diagonal = startX + startY
			`,
			expectedBindings: []DepBinding{
				{Name: "getPoint", Kind: DepKindValue},
				{Name: "startX", Kind: DepKindValue},
				{Name: "startY", Kind: DepKindValue},
				{Name: "getSize", Kind: DepKindValue},
				{Name: "width", Kind: DepKindValue},
				{Name: "height", Kind: DepKindValue},
				{Name: "area", Kind: DepKindValue},
				{Name: "diagonal", Kind: DepKindValue},
			},
			expectedDependencies: [][2][]string{
				{[]string{"getPoint"}, []string{}},
				{[]string{"startX", "startY"}, []string{"getPoint"}},
				{[]string{"getSize"}, []string{}},
				{[]string{"width", "height"}, []string{"getSize"}},
				{[]string{"area"}, []string{"width", "height"}},
				{[]string{"diagonal"}, []string{"startX", "startY"}},
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

			// Build dependency graph
			depGraph := BuildDepGraph(module)

			// Verify bindings
			actualBindings := depGraph.AllBindings()
			assert.ElementsMatch(t, test.expectedBindings, actualBindings,
				"Expected bindings %v, got %v", test.expectedBindings, actualBindings)

			// Verify dependencies
			for _, expectedDecl := range test.expectedDependencies {
				declBindings := expectedDecl[0]
				expectedDeps := expectedDecl[1]

				// For each binding in this declaration, verify its dependencies
				for _, bindingName := range declBindings {
					// Find the binding by name
					var binding DepBinding
					var found bool
					for _, b := range actualBindings {
						if b.Name == bindingName {
							binding = b
							found = true
							break
						}
					}
					assert.True(t, found, "Binding %s not found", bindingName)

					// Get actual dependencies using the new API
					actualDeps := depGraph.GetDependenciesForBinding(binding)
					actualDepNames := make([]string, 0, len(actualDeps))
					for declID := range actualDeps {
						// Get all bindings for this declaration
						bindings := depGraph.getBindingsForDecl(declID)
						for _, depBinding := range bindings {
							actualDepNames = append(actualDepNames, depBinding.Name)
						}
					}

					assert.ElementsMatch(t, expectedDeps, actualDepNames,
						"Expected dependencies for %s: %v, got %v", bindingName, expectedDeps, actualDepNames)
				}
			}
		})
	}
}

func TestDepGraph_HelperMethods(t *testing.T) {
	input := `
		val x = 5
		val y = x + 10
		fn double(n) {
			return n * 2
		}
		val result = double(y)
	`

	source := &ast.Source{
		ID:       0,
		Path:     "test.esc",
		Contents: input,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	parser := parser.NewParser(ctx, source)
	module, errors := parser.ParseModule()

	assert.Len(t, errors, 0, "Parser errors: %v", errors)

	depGraph := BuildDepGraph(module)

	// Test HasBinding
	xBinding := DepBinding{Name: "x", Kind: DepKindValue}
	assert.True(t, depGraph.HasBinding(xBinding), "Should have binding x")

	nonExistentBinding := DepBinding{Name: "nonexistent", Kind: DepKindValue}
	assert.False(t, depGraph.HasBinding(nonExistentBinding), "Should not have nonexistent binding")

	// Test GetBinding
	decl, exists := depGraph.GetBinding(xBinding)
	assert.True(t, exists, "Should find binding x")
	assert.NotNil(t, decl, "Declaration should not be nil")

	_, exists = depGraph.GetBinding(nonExistentBinding)
	assert.False(t, exists, "Should not find nonexistent binding")

	// Test GetDependents using the new API
	xDependents := depGraph.GetDependentsForBinding(xBinding)
	yBinding := DepBinding{Name: "y", Kind: DepKindValue}
	yDeclID, yExists := depGraph.Bindings[yBinding]
	assert.True(t, yExists, "Should find y binding")
	assert.True(t, xDependents.Contains(yDeclID),
		"y should depend on x")

	// Test AllBindings
	allBindings := depGraph.AllBindings()
	assert.Len(t, allBindings, 4, "Should have 4 bindings")

	expectedNames := []string{"x", "y", "double", "result"}
	actualNames := make([]string, len(allBindings))
	for i, binding := range allBindings {
		actualNames[i] = binding.Name
	}
	assert.ElementsMatch(t, expectedNames, actualNames, "Should have expected binding names")
}
