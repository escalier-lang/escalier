package dep_graph

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/assert"
	"github.com/tidwall/btree"
)

// createTestDepGraph creates a DepGraph for testing with the given bindings.
// It automatically assigns DeclIDs starting from 100 and sets all bindings
// to the empty namespace for simplicity in tests.
func createTestDepGraph(decl ast.Decl, validBindings []DepBinding) *DepGraph {
	namespaces := []string{""} // Test with just root namespace
	testDepGraph := NewDepGraph(namespaces)

	// Ensure the DeclNamespace slice is large enough for test DeclIDs (starting from 100)
	maxDeclID := len(validBindings) + 100
	testDepGraph.DeclNamespace = make([]string, maxDeclID)

	for i, binding := range validBindings {
		declID := DeclID(i + 100) // Use arbitrary DeclIDs starting from 100
		switch binding.Kind {
		case DepKindValue:
			testDepGraph.ValueBindings.Set(binding.Name, declID)
		case DepKindType:
			testDepGraph.TypeBindings.Set(binding.Name, declID)
		}
		// Set empty namespace for test bindings
		testDepGraph.DeclNamespace[declID] = "" // Use DeclID as slice index
	}

	testDepGraph.Decls = []ast.Decl{decl} // Add the test declaration

	return testDepGraph
}

func TestFindModuleBindings(t *testing.T) {
	tests := map[string]struct {
		sources  []*ast.Source
		expected []DepBinding
	}{
		"VarDecl_SimpleIdent": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						val a = 5
						var b = 10
					`,
				},
			},
			expected: []DepBinding{
				{Name: "a", Kind: DepKindValue},
				{Name: "b", Kind: DepKindValue},
			},
		},
		"VarDecl_TupleDestructuring": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						val [x, y] = [1, 2]
						var [first, second] = getTuple()
					`,
				},
			},
			expected: []DepBinding{
				{Name: "x", Kind: DepKindValue},
				{Name: "y", Kind: DepKindValue},
				{Name: "first", Kind: DepKindValue},
				{Name: "second", Kind: DepKindValue},
			},
		},
		"VarDecl_ObjectDestructuring": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						val {name, age} = person
						var {x: width, y: height} = dimensions
					`,
				},
			},
			expected: []DepBinding{
				{Name: "name", Kind: DepKindValue},
				{Name: "age", Kind: DepKindValue},
				{Name: "width", Kind: DepKindValue},
				{Name: "height", Kind: DepKindValue},
			},
		},
		"VarDecl_ObjectShorthand": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						val {foo, bar} = obj
					`,
				},
			},
			expected: []DepBinding{
				{Name: "foo", Kind: DepKindValue},
				{Name: "bar", Kind: DepKindValue},
			},
		},
		"FuncDecl_Simple": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						fn add(a, b) {
							return a + b
						}
						fn multiply(x, y) {
							return x * y
						}
					`,
				},
			},
			expected: []DepBinding{
				{Name: "add", Kind: DepKindValue},
				{Name: "multiply", Kind: DepKindValue},
			},
		},
		"TypeDecl_Simple": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						type Point = {x: number, y: number}
						type Color = "red" | "green" | "blue"
					`,
				},
			},
			expected: []DepBinding{
				{Name: "Point", Kind: DepKindType},
				{Name: "Color", Kind: DepKindType},
			},
		},
		"Mixed_Declarations": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						type User = {name: string, age: number}
						val defaultUser = {name: "John", age: 30}
						fn createUser(name, age) {
							return {name, age}
						}
						var [admin, guest] = [createUser("admin", 25), defaultUser]
					`,
				},
			},
			expected: []DepBinding{
				{Name: "User", Kind: DepKindType},
				{Name: "defaultUser", Kind: DepKindValue},
				{Name: "createUser", Kind: DepKindValue},
				{Name: "admin", Kind: DepKindValue},
				{Name: "guest", Kind: DepKindValue},
			},
		},
		"Empty_Module": {
			sources: []*ast.Source{
				{
					ID:       0,
					Path:     "test.esc",
					Contents: ``,
				},
			},
			expected: []DepBinding{},
		},
		"VarDecl_NestedPatterns": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						val {user: {name, profile: {email}}} = data
						val [first, {x, y}] = coordinates
					`,
				},
			},
			expected: []DepBinding{
				{Name: "name", Kind: DepKindValue},
				{Name: "email", Kind: DepKindValue},
				{Name: "first", Kind: DepKindValue},
				{Name: "x", Kind: DepKindValue},
				{Name: "y", Kind: DepKindValue},
			},
		},
		"VarDecl_RestPatterns": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						val [head, ...tail] = list
						val {id, ...rest} = object
					`,
				},
			},
			expected: []DepBinding{
				{Name: "head", Kind: DepKindValue},
				{Name: "tail", Kind: DepKindValue},
				{Name: "id", Kind: DepKindValue},
				{Name: "rest", Kind: DepKindValue},
			},
		},
		"MultipleFiles_SameDirectory": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "main.esc",
					Contents: `
						val mainVar = 42
						fn mainFunc() {
							return "main"
						}
					`,
				},
				{
					ID:   1,
					Path: "utils.esc",
					Contents: `
						type Helper = string
						val utilVar = "utility"
						fn utilFunc(x) {
							return x * 2
						}
					`,
				},
			},
			expected: []DepBinding{
				{Name: "mainVar", Kind: DepKindValue},
				{Name: "mainFunc", Kind: DepKindValue},
				{Name: "Helper", Kind: DepKindType},
				{Name: "utilVar", Kind: DepKindValue},
				{Name: "utilFunc", Kind: DepKindValue},
			},
		},
		"MultipleFiles_WithSubdirectories": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "main.esc",
					Contents: `
						val config = {name: "app", version: "1.0"}
						fn start() {
							return config
						}
					`,
				},
				{
					ID:   1,
					Path: "foo/math.esc",
					Contents: `
						type Vector = {x: number, y: number}
						fn add(a, b) {
							return a + b
						}
						val PI = 3.14159
					`,
				},
				{
					ID:   2,
					Path: "bar/string.esc",
					Contents: `
						fn concat(a, b) {
							return a + b
						}
						var delimiter = ","
					`,
				},
			},
			expected: []DepBinding{
				{Name: "config", Kind: DepKindValue},
				{Name: "start", Kind: DepKindValue},
				{Name: "foo.Vector", Kind: DepKindType},
				{Name: "foo.add", Kind: DepKindValue},
				{Name: "foo.PI", Kind: DepKindValue},
				{Name: "bar.concat", Kind: DepKindValue},
				{Name: "bar.delimiter", Kind: DepKindValue},
			},
		},
		"MultipleFiles_NestedSubdirectories": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "index.esc",
					Contents: `
						type App = {name: string}
						val app: App = {name: "MyApp"}
					`,
				},
				{
					ID:   1,
					Path: "core/engine.esc",
					Contents: `
						fn initialize() {
							return true
						}
						type Engine = {running: boolean}
					`,
				},
				{
					ID:   2,
					Path: "core/utils/helpers.esc",
					Contents: `
						val [first, second] = [1, 2]
						fn helper(x) {
							return x
						}
					`,
				},
				{
					ID:   3,
					Path: "models/user.esc",
					Contents: `
						type User = {id: number, name: string}
						val {defaultId, defaultName} = {defaultId: 0, defaultName: "Unknown"}
					`,
				},
			},
			expected: []DepBinding{
				{Name: "App", Kind: DepKindType},
				{Name: "app", Kind: DepKindValue},
				{Name: "core.initialize", Kind: DepKindValue},
				{Name: "core.Engine", Kind: DepKindType},
				{Name: "core.utils.first", Kind: DepKindValue},
				{Name: "core.utils.second", Kind: DepKindValue},
				{Name: "core.utils.helper", Kind: DepKindValue},
				{Name: "models.User", Kind: DepKindType},
				{Name: "models.defaultId", Kind: DepKindValue},
				{Name: "models.defaultName", Kind: DepKindValue},
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			module, errors := parser.ParseLibFiles(ctx, test.sources)

			// Ensure parsing was successful
			assert.Len(t, errors, 0, "Parser errors: %v", errors)

			// Find bindings
			declarations, valueBindings, typeBindings := FindModuleBindings(module)
			_ = declarations // suppress unused variable warning for now

			// Combine bindings for comparison
			actualBindings := make([]DepBinding, 0, valueBindings.Len()+typeBindings.Len())
			valueIter := valueBindings.Iter()
			for ok := valueIter.First(); ok; ok = valueIter.Next() {
				name := valueIter.Key()
				actualBindings = append(actualBindings, DepBinding{Name: name, Kind: DepKindValue})
			}
			typeIter := typeBindings.Iter()
			for ok := typeIter.First(); ok; ok = typeIter.Next() {
				name := typeIter.Key()
				actualBindings = append(actualBindings, DepBinding{Name: name, Kind: DepKindType})
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
				module := &ast.Module{
					Namespaces: btree.Map[string, *ast.Namespace]{},
				}
				module.Namespaces.Set("", &ast.Namespace{
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
				})
				return module
			},
			expected: []DepBinding{},
		},
		"FuncDecl_EmptyName": {
			setupModule: func() *ast.Module {
				module := &ast.Module{
					Namespaces: btree.Map[string, *ast.Namespace]{},
				}
				module.Namespaces.Set("", &ast.Namespace{
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
				})
				return module
			},
			expected: []DepBinding{},
		},
		"TypeDecl_NilName": {
			setupModule: func() *ast.Module {
				module := &ast.Module{
					Namespaces: btree.Map[string, *ast.Namespace]{},
				}
				module.Namespaces.Set("", &ast.Namespace{
					Decls: []ast.Decl{
						&ast.TypeDecl{
							Name:       nil, // Type with nil name
							TypeParams: []*ast.TypeParam{},
							TypeAnn: &ast.LitTypeAnn{
								Lit: &ast.StrLit{Value: "test"},
							},
						},
					},
				})
				return module
			},
			expected: []DepBinding{},
		},
		"TypeDecl_EmptyName": {
			setupModule: func() *ast.Module {
				module := &ast.Module{
					Namespaces: btree.Map[string, *ast.Namespace]{},
				}
				module.Namespaces.Set("", &ast.Namespace{
					Decls: []ast.Decl{
						&ast.TypeDecl{
							Name:       &ast.Ident{Name: ""}, // Type with empty name
							TypeParams: []*ast.TypeParam{},
							TypeAnn: &ast.LitTypeAnn{
								Lit: &ast.StrLit{Value: "test"},
							},
						},
					},
				})
				return module
			},
			expected: []DepBinding{},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			module := test.setupModule()
			declarations, valueBindings, typeBindings := FindModuleBindings(module)
			_ = declarations // suppress unused variable warning for now

			// Combine bindings for comparison
			actualBindings := make([]DepBinding, 0, valueBindings.Len()+typeBindings.Len())
			valueIter := valueBindings.Iter()
			for ok := valueIter.First(); ok; ok = valueIter.Next() {
				name := valueIter.Key()
				actualBindings = append(actualBindings, DepBinding{Name: name, Kind: DepKindValue})
			}
			typeIter := typeBindings.Iter()
			for ok := typeIter.First(); ok; ok = typeIter.Next() {
				name := typeIter.Key()
				actualBindings = append(actualBindings, DepBinding{Name: name, Kind: DepKindType})
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
		"VarDecl_QualifiedValueDependency": {
			declCode:      `val result = foo.bar + 5`,
			validBindings: []DepBinding{{Name: "foo.bar", Kind: DepKindValue}, {Name: "other.func", Kind: DepKindValue}},
			expectedDeps:  []DepBinding{{Name: "foo.bar", Kind: DepKindValue}},
			declType:      "var",
		},
		"VarDecl_MultipleQualifiedDependencies": {
			declCode: `val result = utils.math.add(data.values.first, data.values.second)`,
			validBindings: []DepBinding{
				{Name: "utils.math.add", Kind: DepKindValue},
				{Name: "data.values.first", Kind: DepKindValue},
				{Name: "data.values.second", Kind: DepKindValue},
				{Name: "other.unused", Kind: DepKindValue},
			},
			expectedDeps: []DepBinding{
				{Name: "utils.math.add", Kind: DepKindValue},
				{Name: "data.values.first", Kind: DepKindValue},
				{Name: "data.values.second", Kind: DepKindValue},
			},
			declType: "var",
		},
		"VarDecl_QualifiedTypeAndValueMixed": {
			declCode: `val config: app.Config = app.createConfig(defaults.host, defaults.port)`,
			validBindings: []DepBinding{
				{Name: "app.Config", Kind: DepKindType},
				{Name: "app.createConfig", Kind: DepKindValue},
				{Name: "defaults.host", Kind: DepKindValue},
				{Name: "defaults.port", Kind: DepKindValue},
			},
			expectedDeps: []DepBinding{
				{Name: "app.Config", Kind: DepKindType},
				{Name: "app.createConfig", Kind: DepKindValue},
				{Name: "defaults.host", Kind: DepKindValue},
				{Name: "defaults.port", Kind: DepKindValue},
			},
			declType: "var",
		},
		"FuncDecl_QualifiedDependencies": {
			declCode: `fn processData(input) {
				val processed = utils.transform(input)
				return storage.save(processed, config.outputPath)
			}`,
			validBindings: []DepBinding{
				{Name: "utils.transform", Kind: DepKindValue},
				{Name: "storage.save", Kind: DepKindValue},
				{Name: "config.outputPath", Kind: DepKindValue},
			},
			expectedDeps: []DepBinding{
				{Name: "utils.transform", Kind: DepKindValue},
				{Name: "storage.save", Kind: DepKindValue},
				{Name: "config.outputPath", Kind: DepKindValue},
			},
			declType: "func",
		},
		"FuncDecl_QualifiedWithLocalShadowing": {
			declCode: `fn testFunc(config) {
				val result = utils.process(config)
				return result + config.outputPath
			}`,
			validBindings: []DepBinding{
				{Name: "utils.process", Kind: DepKindValue},
				{Name: "config.outputPath", Kind: DepKindValue},
			},
			expectedDeps: []DepBinding{
				{Name: "utils.process", Kind: DepKindValue},
				{Name: "config.outputPath", Kind: DepKindValue}, // config.outputPath should still be detected as qualified name
			},
			declType: "func",
		},
		"TypeDecl_QualifiedTypeDependencies": {
			declCode:      `type Response = {data: api.Data, meta: core.Meta}`,
			validBindings: []DepBinding{{Name: "api.Data", Kind: DepKindType}, {Name: "core.Meta", Kind: DepKindType}, {Name: "unused.Type", Kind: DepKindType}},
			expectedDeps:  []DepBinding{{Name: "api.Data", Kind: DepKindType}, {Name: "core.Meta", Kind: DepKindType}},
			declType:      "type",
		},
		"TypeDecl_NestedQualifiedTypes": {
			declCode: `type Complex = {nested: {inner: models.User, config: settings.AppConfig}, items: Array<data.Item>}`,
			validBindings: []DepBinding{
				{Name: "models.User", Kind: DepKindType},
				{Name: "settings.AppConfig", Kind: DepKindType},
				{Name: "data.Item", Kind: DepKindType},
			},
			expectedDeps: []DepBinding{
				{Name: "models.User", Kind: DepKindType},
				{Name: "settings.AppConfig", Kind: DepKindType},
				{Name: "data.Item", Kind: DepKindType},
			},
			declType: "type",
		},
		"VarDecl_QualifiedNonValidDependency": {
			declCode:      `val result = unknown.module.func() + 5`,
			validBindings: []DepBinding{{Name: "known.func", Kind: DepKindValue}, {Name: "other.var", Kind: DepKindValue}},
			expectedDeps:  []DepBinding{}, // unknown.module.func is not in validBindings
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
			module, errors := parser.ParseLibFiles(ctx, []*ast.Source{source})

			// Ensure parsing was successful
			assert.Len(t, errors, 0, "Parser errors: %v", errors)
			assert.NotNil(t, module, "Module should not be nil")

			emptyNS, exists := module.Namespaces.Get("")
			assert.True(t, exists, "Module should have empty namespace")
			assert.Len(t, emptyNS.Decls, 1, "Module should have exactly one declaration")

			// Create a test DepGraph
			emptyNS2, _ := module.Namespaces.Get("")
			testDepGraph := createTestDepGraph(emptyNS2.Decls[0], test.validBindings)

			// Find dependencies
			declID := DeclID(0)
			dependencies := FindDeclDependencies(declID, testDepGraph)

			// Convert dependencies to bindings for comparison
			actualDeps := make([]DepBinding, 0)
			iter := dependencies.Iter()
			for ok := iter.First(); ok; ok = iter.Next() {
				declID := iter.Key()
				// Find the binding that corresponds to this DeclID
				found := false
				valueIter := testDepGraph.ValueBindings.Iter()
				for ok := valueIter.First(); ok; ok = valueIter.Next() {
					name := valueIter.Key()
					id := valueIter.Value()
					if id == declID {
						actualDeps = append(actualDeps, DepBinding{Name: name, Kind: DepKindValue})
						found = true
						break
					}
				}
				if !found {
					typeIter := testDepGraph.TypeBindings.Iter()
					for ok := typeIter.First(); ok; ok = typeIter.Next() {
						name := typeIter.Key()
						id := typeIter.Value()
						if id == declID {
							actualDeps = append(actualDeps, DepBinding{Name: name, Kind: DepKindType})
							break
						}
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
						Name:      "someVar",
						Source:    nil,
						Namespace: 0,
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

			// Create a test DepGraph using the helper function
			testDepGraph := createTestDepGraph(decl, test.validBindings)

			// Find dependencies
			declID := DeclID(0)
			dependencies := FindDeclDependencies(declID, testDepGraph)

			// Convert dependencies to bindings for comparison
			actualDeps := make([]DepBinding, 0)
			iter := dependencies.Iter()
			for ok := iter.First(); ok; ok = iter.Next() {
				declID := iter.Key()
				// Find the binding that corresponds to this DeclID
				found := false
				valueIter := testDepGraph.ValueBindings.Iter()
				for ok := valueIter.First(); ok; ok = valueIter.Next() {
					name := valueIter.Key()
					id := valueIter.Value()
					if id == declID {
						actualDeps = append(actualDeps, DepBinding{Name: name, Kind: DepKindValue})
						found = true
						break
					}
				}
				if !found {
					typeIter := testDepGraph.TypeBindings.Iter()
					for ok := typeIter.First(); ok; ok = typeIter.Next() {
						name := typeIter.Key()
						id := typeIter.Value()
						if id == declID {
							actualDeps = append(actualDeps, DepBinding{Name: name, Kind: DepKindType})
							break
						}
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
		sources              []*ast.Source
		expectedDeclCount    int
		expectedDependencies map[int][]int // [declaration index] -> [dependency declaration indices]
		expectedNamespaces   []string      // expected namespace names
	}{
		"Simple_Dependencies": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						type User = {name: string, age: number}
						val defaultAge = 18
						fn createUser(name) {
							return {name: name, age: defaultAge}
						}
						val admin = createUser("admin")
					`,
				},
			},
			expectedDeclCount: 4,
			expectedDependencies: map[int][]int{
				0: {},  // type User has no dependencies
				1: {},  // val defaultAge has no dependencies
				2: {1}, // fn createUser depends on defaultAge (index 1)
				3: {2}, // val admin depends on createUser (index 2)
			},
			expectedNamespaces: []string{""},
		},
		"Type_Dependencies": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						type Point = {x: number, y: number}
						type Shape = {center: Point, radius: number}
						val origin: Point = {x: 0, y: 0}
					`,
				},
			},
			expectedDeclCount: 3,
			expectedDependencies: map[int][]int{
				0: {},  // type Point has no dependencies
				1: {0}, // type Shape depends on Point (index 0)
				2: {0}, // val origin depends on Point (index 0)
			},
			expectedNamespaces: []string{""},
		},
		"No_Dependencies": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						val a = 5
						val b = 10
						type StringType = string
					`,
				},
			},
			expectedDeclCount: 3,
			expectedDependencies: map[int][]int{
				0: {}, // val a has no dependencies
				1: {}, // val b has no dependencies
				2: {}, // type StringType has no dependencies
			},
			expectedNamespaces: []string{""},
		},
		"Tuple_Destructuring_Simple": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						val point = [10, 20]
						val [x, y] = point
						val sum = x + y
					`,
				},
			},
			expectedDeclCount: 3,
			expectedDependencies: map[int][]int{
				0: {},  // val point has no dependencies
				1: {0}, // val [x, y] depends on point (index 0)
				2: {1}, // val sum depends on destructuring declaration (index 1)
			},
			expectedNamespaces: []string{""},
		},
		"Object_Destructuring_Simple": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						val user = {name: "Alice", age: 30}
						val {name, age} = user
						val greeting = "Hello " + name
					`,
				},
			},
			expectedDeclCount: 3,
			expectedDependencies: map[int][]int{
				0: {},  // val user has no dependencies
				1: {0}, // val {name, age} depends on user (index 0)
				2: {1}, // val greeting depends on destructuring declaration (index 1)
			},
			expectedNamespaces: []string{""},
		},
		"Object_Destructuring_With_Rename": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						val config = {width: 100, height: 50}
						val {width: w, height: h} = config
						val area = w * h
					`,
				},
			},
			expectedDeclCount: 3,
			expectedDependencies: map[int][]int{
				0: {},  // val config has no dependencies
				1: {0}, // val {width: w, height: h} depends on config (index 0)
				2: {1}, // val area depends on destructuring declaration (index 1)
			},
			expectedNamespaces: []string{""},
		},
		"Nested_Destructuring": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						val data = {coords: [5, 10], info: {id: 1}}
						val {coords: [x, y], info: {id}} = data
						val result = x + y + id
					`,
				},
			},
			expectedDeclCount: 3,
			expectedDependencies: map[int][]int{
				0: {},  // val data has no dependencies
				1: {0}, // val {coords: [x, y], info: {id}} depends on data (index 0)
				2: {1}, // val result depends on destructuring declaration (index 1)
			},
			expectedNamespaces: []string{""},
		},
		"Rest_Pattern_Destructuring": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
						val numbers = [1, 2, 3, 4, 5]
						val [first, second, ...rest] = numbers
						val restSum = rest
						val total = first + second
					`,
				},
			},
			expectedDeclCount: 4,
			expectedDependencies: map[int][]int{
				0: {},  // val numbers has no dependencies
				1: {0}, // val [first, second, ...rest] depends on numbers (index 0)
				2: {1}, // val restSum depends on destructuring declaration (index 1)
				3: {1}, // val total depends on destructuring declaration (index 1)
			},
			expectedNamespaces: []string{""},
		},
		"Mixed_Destructuring_With_Functions": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "test.esc",
					Contents: `
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
				},
			},
			expectedDeclCount: 6,
			expectedDependencies: map[int][]int{
				0: {},  // fn getPoint has no dependencies
				1: {0}, // val [startX, startY] depends on getPoint (index 0)
				2: {},  // fn getSize has no dependencies
				3: {2}, // val {width, height} depends on getSize (index 2)
				4: {3}, // val area depends on destructuring declaration (index 3)
				5: {1}, // val diagonal depends on destructuring declaration (index 1)
			},
			expectedNamespaces: []string{""},
		},
		"Multiple_Files_Simple_Dependencies": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "main.esc",
					Contents: `
						val config = {debug: true, version: "1.0"}
						val app = createApp(config)
					`,
				},
				{
					ID:   1,
					Path: "utils.esc",
					Contents: `
						fn createApp(config) {
							return {name: "MyApp", config: config}
						}
						val helper = "utility"
					`,
				},
			},
			expectedDeclCount: 4,
			expectedDependencies: map[int][]int{
				0: {},     // val config (main.esc) has no dependencies
				1: {0, 2}, // val app (main.esc) depends on config (index 0) and utils.createApp (index 2)
				2: {},     // fn utils.createApp has no dependencies
				3: {},     // val utils.helper has no dependencies
			},
			expectedNamespaces: []string{""},
		},
		"Multiple_Files_With_Subdirectories": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "main.esc",
					Contents: `
						type Config = {name: string, version: string}
						val config: Config = {name: "app", version: "1.0"}
						val calculator = math.add(5, 3)
						val greeting = strings.format("Hello {}", config.name)
					`,
				},
				{
					ID:   1,
					Path: "math/operations.esc",
					Contents: `
						fn add(a, b) {
							return a + b
						}
						fn multiply(a, b) {
							return a * b
						}
						val PI = 3.14159
					`,
				},
				{
					ID:   2,
					Path: "strings/format.esc",
					Contents: `
						fn format(template, value) {
							return template + " " + value
						}
						type Template = string
					`,
				},
			},
			expectedDeclCount: 9,
			expectedDependencies: map[int][]int{
				0: {},     // type Config (main.esc) has no dependencies
				1: {0},    // val config (main.esc) depends on Config (index 0)
				2: {4},    // val calculator (main.esc) depends on math.add (index 4)
				3: {1, 7}, // val greeting (main.esc) depends on strings.format (index 7) and config (index 1)
				4: {},     // fn math.add has no dependencies
				5: {},     // fn math.multiply has no dependencies
				6: {},     // val math.PI has no dependencies
				7: {},     // fn strings.format has no dependencies
				8: {},     // type strings.Template has no dependencies
			},
			expectedNamespaces: []string{"", "math", "strings"},
		},
		"Multiple_Files_Cross_Directory_Dependencies": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "index.esc",
					Contents: `
						val result = core.process(models.defaultUser)
						type Result = {success: boolean, data: models.User}
					`,
				},
				{
					ID:   1,
					Path: "core/engine.esc",
					Contents: `
						fn process(user) {
							return {success: true, data: user}
						}
						val engine: Engine = {running: true}
						type Engine = {running: boolean}
					`,
				},
				{
					ID:   2,
					Path: "models/user.esc",
					Contents: `
						type User = {id: number, name: string}
						val defaultUser: User = {id: 0, name: "Guest"}
						fn createUser(name) {
							return {id: 1, name: name}
						}
					`,
				},
			},
			expectedDeclCount: 8,
			expectedDependencies: map[int][]int{
				0: {2, 6}, // val result depends on core.engine.process (index 2) and models.user.defaultUser (index 6)
				1: {5},    // type Result depends on models.User (index 5)
				2: {},     // fn core.engine.process has no dependencies
				3: {4},    // val core.engine.engine depends on core.Engine (index 4)
				4: {},     // type core.Engine has no dependencies
				5: {},     // type models.User has no dependencies
				6: {5},    // val models.defaultUser depends on models.User (index 5)
				7: {},     // fn models.createUser has no dependencies
			},
			expectedNamespaces: []string{"", "core", "models"},
		},
		"Multiple_Files_Nested_Subdirectories": {
			sources: []*ast.Source{
				{
					ID:   0,
					Path: "app.esc",
					Contents: `
						val server = core.network.createServer(8080)
						val database = core.storage.connect("localhost")
						type App = {server: core.network.Server, db: core.storage.Database}
					`,
				},
				{
					ID:   1,
					Path: "core/network/http.esc",
					Contents: `
						type Server = {port: number, running: boolean}
						fn createServer(port) {
							return {port: port, running: false}
						}
						val defaultPort = 3000
					`,
				},
				{
					ID:   2,
					Path: "core/storage/db.esc",
					Contents: `
						type Database = {host: string, connected: boolean}
						fn connect(host) {
							return {host: host, connected: true}
						}
						val maxConnections = 100
					`,
				},
			},
			expectedDeclCount: 9,
			expectedDependencies: map[int][]int{
				0: {4},    // val server depends on core.network.http.createServer (index 4)
				1: {7},    // val database depends on core.storage.db.connect (index 7)
				2: {3, 6}, // type App depends on core.network.http.Server (index 3) and core.storage.db.Database (index 6)
				3: {},     // type core.network.http.Server has no dependencies
				4: {},     // fn core.network.http.createServer has no dependencies
				5: {},     // val core.network.http.defaultPort has no dependencies
				6: {},     // type core.storage.db.Database has no dependencies
				7: {},     // fn core.storage.db.connect has no dependencies
				8: {},     // val core.storage.db.maxConnections has no dependencies
			},
			expectedNamespaces: []string{"", "core.network", "core.storage"},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			module, errors := parser.ParseLibFiles(ctx, test.sources)

			// Ensure parsing was successful
			assert.Len(t, errors, 0, "Parser errors: %v", errors)

			// Build dependency graph
			depGraph := BuildDepGraph(module)

			// Get the declaration IDs
			declIDs := make([]DeclID, 0, len(depGraph.Decls))
			for i := range depGraph.Decls {
				declID := DeclID(i) // DeclID is now the slice index directly
				declIDs = append(declIDs, declID)
			}

			// Verify number of declarations
			actualDeclCount := len(declIDs)
			assert.Equal(t, test.expectedDeclCount, actualDeclCount,
				"Expected %d declarations, got %d", test.expectedDeclCount, actualDeclCount)

			// Verify namespaces
			assert.ElementsMatch(t, test.expectedNamespaces, depGraph.Namespaces,
				"Expected namespaces %v, got %v", test.expectedNamespaces, depGraph.Namespaces)

			// Verify dependencies for each declaration
			for declIndex, expectedDeps := range test.expectedDependencies {
				if declIndex >= len(declIDs) {
					t.Errorf("Test case has dependency expectations for declaration index %d, but only %d declarations exist",
						declIndex, len(declIDs))
					continue
				}

				declID := declIDs[declIndex]
				actualDeps := depGraph.GetDeclDeps(declID)

				// Convert expected dependency indices to actual DeclIDs
				expectedDeclIDs := make([]DeclID, len(expectedDeps))
				for i, depIndex := range expectedDeps {
					if depIndex >= len(declIDs) {
						t.Errorf("Test case expects dependency on declaration index %d, but only %d declarations exist",
							depIndex, len(declIDs))
						continue
					}
					expectedDeclIDs[i] = declIDs[depIndex]
				}

				// Convert actual dependencies to slice for comparison
				actualDepsSlice := make([]DeclID, 0, actualDeps.Len())
				iter := actualDeps.Iter()
				for ok := iter.First(); ok; ok = iter.Next() {
					actualDepsSlice = append(actualDepsSlice, iter.Key())
				}

				assert.ElementsMatch(t, expectedDeclIDs, actualDepsSlice,
					"Expected dependencies for declaration %d: %v, got %v", declIndex, expectedDeclIDs, actualDepsSlice)
			}
		})
	}
}
