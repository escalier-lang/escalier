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

			// Create valid bindings maps (split by kind)
			var valueBindings btree.Map[string, DeclID]
			var typeBindings btree.Map[string, DeclID]
			for i, binding := range test.validBindings {
				declID := DeclID(i + 100) // Use arbitrary DeclIDs starting from 100
				if binding.Kind == DepKindValue {
					valueBindings.Set(binding.Name, declID)
				} else if binding.Kind == DepKindType {
					typeBindings.Set(binding.Name, declID)
				}
			}

			// Find dependencies
			dependencies := FindDeclDependencies(module.Decls[0], valueBindings, typeBindings)

			// Convert dependencies to bindings for comparison
			actualDeps := make([]DepBinding, 0)
			iter := dependencies.Iter()
			for ok := iter.First(); ok; ok = iter.Next() {
				declID := iter.Key()
				// Find the binding that corresponds to this DeclID
				found := false
				valueIter := valueBindings.Iter()
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
					typeIter := typeBindings.Iter()
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

			// Create valid bindings maps (split by kind)
			var valueBindings btree.Map[string, DeclID]
			var typeBindings btree.Map[string, DeclID]
			for i, binding := range test.validBindings {
				declID := DeclID(i + 100) // Use arbitrary DeclIDs starting from 100
				if binding.Kind == DepKindValue {
					valueBindings.Set(binding.Name, declID)
				} else if binding.Kind == DepKindType {
					typeBindings.Set(binding.Name, declID)
				}
			}

			// Find dependencies
			dependencies := FindDeclDependencies(decl, valueBindings, typeBindings)

			// Convert dependencies to bindings for comparison
			actualDeps := make([]DepBinding, 0)
			iter := dependencies.Iter()
			for ok := iter.First(); ok; ok = iter.Next() {
				declID := iter.Key()
				// Find the binding that corresponds to this DeclID
				found := false
				valueIter := valueBindings.Iter()
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
					typeIter := typeBindings.Iter()
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
		input                string
		expectedDeclCount    int
		expectedDependencies map[int][]int // [declaration index] -> [dependency declaration indices]
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
			expectedDeclCount: 4,
			expectedDependencies: map[int][]int{
				0: {},  // type User has no dependencies
				1: {},  // val defaultAge has no dependencies
				2: {1}, // fn createUser depends on defaultAge (index 1)
				3: {2}, // val admin depends on createUser (index 2)
			},
		},
		"Type_Dependencies": {
			input: `
				type Point = {x: number, y: number}
				type Shape = {center: Point, radius: number}
				val origin: Point = {x: 0, y: 0}
			`,
			expectedDeclCount: 3,
			expectedDependencies: map[int][]int{
				0: {},  // type Point has no dependencies
				1: {0}, // type Shape depends on Point (index 0)
				2: {0}, // val origin depends on Point (index 0)
			},
		},
		"No_Dependencies": {
			input: `
				val a = 5
				val b = 10
				type StringType = string
			`,
			expectedDeclCount: 3,
			expectedDependencies: map[int][]int{
				0: {}, // val a has no dependencies
				1: {}, // val b has no dependencies
				2: {}, // type StringType has no dependencies
			},
		},
		"Tuple_Destructuring_Simple": {
			input: `
				val point = [10, 20]
				val [x, y] = point
				val sum = x + y
			`,
			expectedDeclCount: 3,
			expectedDependencies: map[int][]int{
				0: {},  // val point has no dependencies
				1: {0}, // val [x, y] depends on point (index 0)
				2: {1}, // val sum depends on destructuring declaration (index 1)
			},
		},
		"Object_Destructuring_Simple": {
			input: `
				val user = {name: "Alice", age: 30}
				val {name, age} = user
				val greeting = "Hello " + name
			`,
			expectedDeclCount: 3,
			expectedDependencies: map[int][]int{
				0: {},  // val user has no dependencies
				1: {0}, // val {name, age} depends on user (index 0)
				2: {1}, // val greeting depends on destructuring declaration (index 1)
			},
		},
		"Object_Destructuring_With_Rename": {
			input: `
				val config = {width: 100, height: 50}
				val {width: w, height: h} = config
				val area = w * h
			`,
			expectedDeclCount: 3,
			expectedDependencies: map[int][]int{
				0: {},  // val config has no dependencies
				1: {0}, // val {width: w, height: h} depends on config (index 0)
				2: {1}, // val area depends on destructuring declaration (index 1)
			},
		},
		"Nested_Destructuring": {
			input: `
				val data = {coords: [5, 10], info: {id: 1}}
				val {coords: [x, y], info: {id}} = data
				val result = x + y + id
			`,
			expectedDeclCount: 3,
			expectedDependencies: map[int][]int{
				0: {},  // val data has no dependencies
				1: {0}, // val {coords: [x, y], info: {id}} depends on data (index 0)
				2: {1}, // val result depends on destructuring declaration (index 1)
			},
		},
		"Rest_Pattern_Destructuring": {
			input: `
				val numbers = [1, 2, 3, 4, 5]
				val [first, second, ...rest] = numbers
				val restSum = rest
				val total = first + second
			`,
			expectedDeclCount: 4,
			expectedDependencies: map[int][]int{
				0: {},  // val numbers has no dependencies
				1: {0}, // val [first, second, ...rest] depends on numbers (index 0)
				2: {1}, // val restSum depends on destructuring declaration (index 1)
				3: {1}, // val total depends on destructuring declaration (index 1)
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
			expectedDeclCount: 6,
			expectedDependencies: map[int][]int{
				0: {},  // fn getPoint has no dependencies
				1: {0}, // val [startX, startY] depends on getPoint (index 0)
				2: {},  // fn getSize has no dependencies
				3: {2}, // val {width, height} depends on getSize (index 2)
				4: {3}, // val area depends on destructuring declaration (index 3)
				5: {1}, // val diagonal depends on destructuring declaration (index 1)
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

			// Create a sorted list of declaration IDs for consistent ordering
			declIDs := depGraph.AllDeclarations()

			// Verify number of declarations
			actualDeclCount := len(declIDs)
			assert.Equal(t, test.expectedDeclCount, actualDeclCount,
				"Expected %d declarations, got %d", test.expectedDeclCount, actualDeclCount)

			// Sort by the declaration ID to ensure consistent ordering
			// (since declaration IDs are assigned in order, this gives us document order)
			for i := 0; i < len(declIDs)-1; i++ {
				for j := i + 1; j < len(declIDs); j++ {
					if declIDs[i] > declIDs[j] {
						declIDs[i], declIDs[j] = declIDs[j], declIDs[i]
					}
				}
			}

			// Verify dependencies for each declaration
			for declIndex, expectedDeps := range test.expectedDependencies {
				if declIndex >= len(declIDs) {
					t.Errorf("Test case has dependency expectations for declaration index %d, but only %d declarations exist",
						declIndex, len(declIDs))
					continue
				}

				declID := declIDs[declIndex]
				actualDeps := depGraph.GetDependencies(declID)

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
