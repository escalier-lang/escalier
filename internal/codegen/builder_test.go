package codegen

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dep_graph"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/assert"
	"github.com/tidwall/btree"
)

func TestBuildNamespaceStatements(t *testing.T) {
	tests := map[string]struct {
		declNamespaces map[dep_graph.DeclID]string
		declIDs        []dep_graph.DeclID
		expected       string
	}{
		"EmptyNamespaces": {
			declNamespaces: map[dep_graph.DeclID]string{
				1: "",
				2: "",
			},
			declIDs:  []dep_graph.DeclID{1, 2},
			expected: "",
		},
		"SingleLevelNamespace": {
			declNamespaces: map[dep_graph.DeclID]string{
				1: "foo",
				2: "bar",
			},
			declIDs: []dep_graph.DeclID{1, 2},
			expected: `const bar = {};
const foo = {};`,
		},
		"TwoLevelNamespace": {
			declNamespaces: map[dep_graph.DeclID]string{
				1: "foo.bar",
			},
			declIDs: []dep_graph.DeclID{1},
			expected: `const foo = {};
foo.bar = {};`,
		},
		"ThreeLevelNamespace": {
			declNamespaces: map[dep_graph.DeclID]string{
				1: "foo.bar.baz",
			},
			declIDs: []dep_graph.DeclID{1},
			expected: `const foo = {};
foo.bar = {};
foo.bar.baz = {};`,
		},
		"MixedNamespaceLevels": {
			declNamespaces: map[dep_graph.DeclID]string{
				1: "foo",
				2: "foo.bar",
				3: "foo.bar.baz",
				4: "qux",
			},
			declIDs: []dep_graph.DeclID{1, 2, 3, 4},
			expected: `const foo = {};
foo.bar = {};
foo.bar.baz = {};
const qux = {};`,
		},
		"DuplicateNamespaces": {
			declNamespaces: map[dep_graph.DeclID]string{
				1: "foo.bar",
				2: "foo.bar",
				3: "foo.baz",
			},
			declIDs: []dep_graph.DeclID{1, 2, 3},
			expected: `const foo = {};
foo.bar = {};
foo.baz = {};`,
		},
		"OverlappingNamespaces": {
			declNamespaces: map[dep_graph.DeclID]string{
				1: "models.User",
				2: "models.Post",
				3: "models.utils.validation",
			},
			declIDs: []dep_graph.DeclID{1, 2, 3},
			expected: `const models = {};
models.Post = {};
models.User = {};
models.utils = {};
models.utils.validation = {};`,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			// Create a mock dependency graph
			depGraph := &dep_graph.DepGraph{
				Decls:         btree.Map[dep_graph.DeclID, ast.Decl]{},
				Deps:          btree.Map[dep_graph.DeclID, btree.Set[dep_graph.DeclID]]{},
				ValueBindings: btree.Map[string, dep_graph.DeclID]{},
				TypeBindings:  btree.Map[string, dep_graph.DeclID]{},
				DeclNamespace: btree.Map[dep_graph.DeclID, string]{},
			}

			// Populate the DeclNamespace map
			for declID, namespace := range test.declNamespaces {
				depGraph.DeclNamespace.Set(declID, namespace)
			}

			// Create a builder and test the method
			builder := &Builder{tempId: 0}
			stmts := builder.buildNamespaceStatements(test.declIDs, depGraph)

			// Use the printer to generate the output
			printer := NewPrinter()
			for i, stmt := range stmts {
				if i > 0 {
					printer.NewLine()
				}
				printer.PrintStmt(stmt)
			}

			assert.Equal(t, test.expected, printer.Output, "Generated namespace statements should match expected output")
		})
	}
}

func TestBuildNamespaceHierarchy(t *testing.T) {
	tests := map[string]struct {
		namespace string
		expected  string
	}{
		"EmptyNamespace": {
			namespace: "",
			expected:  "",
		},
		"SingleLevel": {
			namespace: "foo",
			expected:  "const foo = {};",
		},
		"TwoLevels": {
			namespace: "foo.bar",
			expected: `const foo = {};
foo.bar = {};`,
		},
		"ThreeLevels": {
			namespace: "foo.bar.baz",
			expected: `const foo = {};
foo.bar = {};
foo.bar.baz = {};`,
		},
		"DeepNesting": {
			namespace: "very.deep.nested.namespace.structure",
			expected: `const very = {};
very.deep = {};
very.deep.nested = {};
very.deep.nested.namespace = {};
very.deep.nested.namespace.structure = {};`,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			builder := &Builder{tempId: 0}
			definedNamespaces := make(map[string]bool)
			stmts := builder.buildNamespaceHierarchy(test.namespace, definedNamespaces)

			// Use the printer to generate the output
			printer := NewPrinter()
			for i, stmt := range stmts {
				if i > 0 {
					printer.NewLine()
				}
				printer.PrintStmt(stmt)
			}

			assert.Equal(t, test.expected, printer.Output, "Generated hierarchy should match expected output")
		})
	}
}

func TestBuildNamespaceHierarchy_AvoidRedefinition(t *testing.T) {
	builder := &Builder{tempId: 0}
	definedNamespaces := make(map[string]bool)

	// First call should generate all statements
	stmts1 := builder.buildNamespaceHierarchy("foo.bar.baz", definedNamespaces)

	// Second call with overlapping namespace should only generate new parts
	stmts2 := builder.buildNamespaceHierarchy("foo.bar.qux", definedNamespaces)

	// Print first set of statements
	printer1 := NewPrinter()
	for i, stmt := range stmts1 {
		if i > 0 {
			printer1.NewLine()
		}
		printer1.PrintStmt(stmt)
	}

	// Print second set of statements
	printer2 := NewPrinter()
	for i, stmt := range stmts2 {
		if i > 0 {
			printer2.NewLine()
		}
		printer2.PrintStmt(stmt)
	}

	expected1 := `const foo = {};
foo.bar = {};
foo.bar.baz = {};`

	expected2 := `foo.bar.qux = {};`

	assert.Equal(t, expected1, printer1.Output, "First namespace hierarchy should generate all levels")
	assert.Equal(t, expected2, printer2.Output, "Second namespace hierarchy should only generate new levels")
}

func TestBuildDeclWithNamespace(t *testing.T) {
	tests := map[string]struct {
		declSource string
		ns         string
		expected   string
	}{
		"VarDecl_NoNamespace": {
			declSource: "val x = 42",
			ns:         "",
			expected:   "const x = 42;",
		},
		"VarDecl_WithNamespace": {
			declSource: "val x = 42",
			ns:         "foo",
			expected:   "const foo__x = 42;",
		},
		"VarDecl_Declared": {
			declSource: "declare val x = 42",
			ns:         "",
			expected:   "",
		},
		"FuncDecl_NoNamespace": {
			declSource: "fn add(a, b) { return a + b }",
			ns:         "",
			expected:   "function add(temp1, temp2) {\n  const a = temp1;\n  const b = temp2;\n  return a + b;\n}",
		},
		"FuncDecl_WithNamespace": {
			declSource: "fn add(a, b) { return a + b }",
			ns:         "math",
			expected:   "function math__add(temp1, temp2) {\n  const a = temp1;\n  const b = temp2;\n  return a + b;\n}",
		},
		"FuncDecl_Declared": {
			declSource: "declare fn add(a, b) { return a + b }",
			ns:         "",
			expected:   "",
		},
		"FuncDecl_NoBody": {
			declSource: "declare fn external()",
			ns:         "",
			expected:   "",
		},
		"TypeDecl": {
			declSource: "type MyType = number",
			ns:         "",
			expected:   "",
		},
		"VarDecl_ComplexPattern": {
			declSource: "val {x, y} = point",
			ns:         "",
			expected:   "const {x, y} = point;",
		},
		"VarDecl_ComplexPattern_WithNamespace": {
			declSource: "val result = calculateSum(1, 2, 3)",
			ns:         "utils",
			expected:   "const utils__result = calculateSum(1, 2, 3);",
		},
		"FuncDecl_WithDefaultParams": {
			declSource: "fn greet(name = \"World\") { return \"Hello, \" + name }",
			ns:         "",
			expected:   "function greet(temp1) {\n  const name = \"World\" = temp1;\n  return \"Hello, \" + name;\n}",
		},
		"FuncDecl_WithRestParams": {
			declSource: "fn sum(...args) { return 42 }",
			ns:         "math",
			expected:   "function math__sum(...temp1) {\n  const args = temp1;\n  return 42;\n}",
		},
		"VarDecl_Var": {
			declSource: "var counter = 0",
			ns:         "",
			expected:   "let counter = 0;",
		},
		"VarDecl_Var_WithNamespace": {
			declSource: "var count = 1",
			ns:         "state",
			expected:   "let state__count = 1;",
		},
		"FuncDecl_EmptyBody": {
			declSource: "fn noop() {}",
			ns:         "",
			expected:   "function noop();",
		},
		"VarDecl_StringLiteral": {
			declSource: "val message = \"hello\"",
			ns:         "constants",
			expected:   "const constants__message = \"hello\";",
		},
		"VarDecl_ObjectDestructuring": {
			declSource: "val {x, y} = point",
			ns:         "math",
			expected:   "const {math__x, math__y} = point;",
		},
		"VarDecl_TupleDestructuring": {
			declSource: "val [x, y] = point",
			ns:         "math",
			expected:   "const [math__x, math__y] = point;",
		},
		"FuncDecl_SingleParam": {
			declSource: "fn double(x) { return x * 2 }",
			ns:         "utils",
			expected:   "function utils__double(temp1) {\n  const x = temp1;\n  return x * 2;\n}",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			// Parse the declaration from source string
			decl := parseDecl(t, test.declSource)

			builder := &Builder{tempId: 0}
			var nsParts []string
			if test.ns != "" {
				nsParts = strings.Split(test.ns, ".")
			}
			stmts := builder.buildDeclWithNamespace(decl, nsParts)

			// Use the printer to generate the output
			printer := NewPrinter()
			for i, stmt := range stmts {
				if i > 0 {
					printer.NewLine()
				}
				printer.PrintStmt(stmt)
			}

			assert.Equal(t, test.expected, printer.Output, "Generated declaration should match expected output")
		})
	}
}

func TestBuildDecls(t *testing.T) {
	tests := map[string]struct {
		declSources []string
		namespaces  []string // corresponding namespace for each declaration
		expected    string
	}{
		"Single_Decl_No_Namespace": {
			declSources: []string{"val x = 42"},
			namespaces:  []string{""},
			expected:    "const x = 42;",
		},
		"Single_Decl_With_Namespace": {
			declSources: []string{"val x = 42"},
			namespaces:  []string{"foo"},
			expected: `const foo = {};
const foo__x = 42;
foo.x = foo__x;`,
		},
		"Multiple_Decls_Same_Namespace": {
			declSources: []string{
				"val x = 42",
				"fn double(n) { return n * 2 }",
			},
			namespaces: []string{"math", "math"},
			expected: `const math = {};
const math__x = 42;
math.x = math__x;
function math__double(temp1) {
  const n = temp1;
  return n * 2;
}
math.double = math__double;`,
		},
		"Multiple_Decls_Different_Namespaces": {
			declSources: []string{
				"val PI = 3.14159",
				"val message = \"hello\"",
			},
			namespaces: []string{"math", "strings"},
			expected: `const math = {};
const strings = {};
const math__PI = 3.14159;
math.PI = math__PI;
const strings__message = "hello";
strings.message = strings__message;`,
		},
		"Nested_Namespaces": {
			declSources: []string{
				"val add = 42",
				"val PI = 3.14",
			},
			namespaces: []string{"utils.math", "constants.math"},
			expected: `const constants = {};
constants.math = {};
const utils = {};
utils.math = {};
const utils__math__add = 42;
utils.math.add = utils__math__add;
const constants__math__PI = 3.14;
constants.math.PI = constants__math__PI;`,
		},
		"Mixed_Namespace_Levels": {
			declSources: []string{
				"val config = {debug: true}",
				"fn log(msg) { console.log(msg) }",
				"val VERSION = \"1.0.0\"",
			},
			namespaces: []string{"", "utils", "constants.app"},
			expected: `const constants = {};
constants.app = {};
const utils = {};
const config = {debug: true};
function utils__log(temp1) {
  const msg = temp1;
  console.log(msg);
}
utils.log = utils__log;
const constants__app__VERSION = "1.0.0";
constants.app.VERSION = constants__app__VERSION;`,
		},
		"Function_With_Complex_Namespace": {
			declSources: []string{
				"fn processData(input) { return input }",
			},
			namespaces: []string{"services.data.processing"},
			expected: `const services = {};
services.data = {};
services.data.processing = {};
function services__data__processing__processData(temp1) {
  const input = temp1;
  return input;
}
services.data.processing.processData = services__data__processing__processData;`,
		},
		"Variable_Destructuring_With_Namespace": {
			declSources: []string{
				"val {x, y} = getPoint()",
			},
			namespaces: []string{"coords"},
			expected: `const coords = {};
const {coords__x, coords__y} = getPoint();
coords.x = coords__x;
coords.y = coords__y;`,
		},
		"Multiple_Declarations_Overlapping_Namespaces": {
			declSources: []string{
				"val user = {name: \"Alice\"}",
				"fn createUser(name) { return {name} }",
				"val defaultUser = null",
			},
			namespaces: []string{"models.user", "models.user", "models.user.defaults"},
			expected: `const models = {};
models.user = {};
models.user.defaults = {};
const models__user__user = {name: "Alice"};
models.user.user = models__user__user;
function models__user__createUser(temp1) {
  const name = temp1;
  return {name};
}
models.user.createUser = models__user__createUser;
const models__user__defaults__defaultUser = null;
models.user.defaults.defaultUser = models__user__defaults__defaultUser;`,
		},
		"Type_Declaration_Skip": {
			declSources: []string{
				"type User = {name: string, age: number}",
				"val admin = {name: \"admin\", age: 30}",
			},
			namespaces: []string{"types", "data"},
			expected: `const data = {};
const types = {};
const data__admin = {name: "admin", age: 30};
data.admin = data__admin;`,
		},
		"Var_Declaration_With_Namespace": {
			declSources: []string{
				"var counter = 0",
				"var isEnabled = true",
			},
			namespaces: []string{"state.app", "state.ui"},
			expected: `const state = {};
state.app = {};
state.ui = {};
let state__app__counter = 0;
state.app.counter = state__app__counter;
let state__ui__isEnabled = true;
state.ui.isEnabled = state__ui__isEnabled;`,
		},
		// 		"Declared_Functions_Skip": {
		// 			declSources: []string{
		// 				"declare fn external()",
		// 				"fn internal() { return 42 }",
		// 			},
		// 			namespaces: []string{"api", "utils"},
		// 			expected: `const utils = {};
		// function utils__internal() {
		//   return 42;
		// }
		// utils.internal = utils__internal;`,
		// 		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			// Create a mock dependency graph with test data
			depGraph := &dep_graph.DepGraph{
				Decls:         btree.Map[dep_graph.DeclID, ast.Decl]{},
				Deps:          btree.Map[dep_graph.DeclID, btree.Set[dep_graph.DeclID]]{},
				ValueBindings: btree.Map[string, dep_graph.DeclID]{},
				TypeBindings:  btree.Map[string, dep_graph.DeclID]{},
				DeclNamespace: btree.Map[dep_graph.DeclID, string]{},
			}

			var declIDs []dep_graph.DeclID

			// Parse declarations and populate the dependency graph
			for i, source := range test.declSources {
				declID := dep_graph.DeclID(i + 1)
				decl := parseDecl(t, source)

				// Store the declaration
				depGraph.Decls.Set(declID, decl)
				depGraph.DeclNamespace.Set(declID, test.namespaces[i])
				declIDs = append(declIDs, declID)

				// Extract bindings and add them to the dependency graph
				switch d := decl.(type) {
				case *ast.VarDecl:
					bindingNames := ast.FindBindings(d.Pattern)
					for name := range bindingNames {
						// Create fully qualified name if in namespace
						qualifiedName := name
						if test.namespaces[i] != "" {
							qualifiedName = test.namespaces[i] + "." + name
						}
						depGraph.ValueBindings.Set(qualifiedName, declID)
					}
				case *ast.FuncDecl:
					if d.Name != nil && d.Name.Name != "" {
						name := d.Name.Name
						// Create fully qualified name if in namespace
						qualifiedName := name
						if test.namespaces[i] != "" {
							qualifiedName = test.namespaces[i] + "." + name
						}
						depGraph.ValueBindings.Set(qualifiedName, declID)
					}
				case *ast.TypeDecl:
					if d.Name != nil && d.Name.Name != "" {
						name := d.Name.Name
						// Create fully qualified name if in namespace
						qualifiedName := name
						if test.namespaces[i] != "" {
							qualifiedName = test.namespaces[i] + "." + name
						}
						depGraph.TypeBindings.Set(qualifiedName, declID)
					}
				}

				// Create empty dependencies for each declaration
				var deps btree.Set[dep_graph.DeclID]
				depGraph.Deps.Set(declID, deps)
			}

			// Create a builder and test BuildDecls
			builder := &Builder{tempId: 0}
			module := builder.BuildTopLevelDecls(declIDs, depGraph)

			// Use the printer to generate the output
			printer := NewPrinter()
			for i, stmt := range module.Stmts {
				if i > 0 {
					printer.NewLine()
				}
				printer.PrintStmt(stmt)
			}

			assert.Equal(t, test.expected, printer.Output, "Generated module should match expected output")
		})
	}
}

func TestBuildDecls_WithDependencies(t *testing.T) {
	tests := map[string]struct {
		declSources  []string
		namespaces   []string
		dependencies map[int][]int // map[declIndex][]dependsOnDeclIndices
		expected     string
	}{
		"Simple_Dependency_Same_Namespace": {
			declSources: []string{
				"val base = 10",
				"val derived = base * 2",
			},
			namespaces: []string{"math", "math"},
			dependencies: map[int][]int{
				0: {},  // base has no dependencies
				1: {0}, // derived depends on base
			},
			expected: `const math = {};
const math__base = 10;
const math__derived = math__base * 2;
math.base = math__base;
math.derived = math__derived;`,
		},
		"Cross_Namespace_Dependencies": {
			declSources: []string{
				"val PI = 3.14159",
				"fn circleArea(r) { return PI * r * r }",
			},
			namespaces: []string{"constants", "geometry"},
			dependencies: map[int][]int{
				0: {},  // PI has no dependencies
				1: {0}, // circleArea depends on PI
			},
			expected: `const constants = {};
const geometry = {};
const constants__PI = 3.14159;
function geometry__circleArea(temp1) {
  const r = temp1;
  return constants__PI * r * r;
}
constants.PI = constants__PI;
geometry.circleArea = geometry__circleArea;`,
		},
		"Complex_Dependency_Chain": {
			declSources: []string{
				"val config = {multiplier: 2}",
				"val factor = config.multiplier",
				"fn calculate(x) { return x * factor }",
			},
			namespaces: []string{"app", "app.utils", "app.utils"},
			dependencies: map[int][]int{
				0: {},  // config has no dependencies
				1: {0}, // factor depends on config
				2: {1}, // calculate depends on factor
			},
			expected: `const app = {};
app.utils = {};
const app__config = {multiplier: 2};
const app__utils__factor = app__config.multiplier;
function app__utils__calculate(temp1) {
  const x = temp1;
  return x * app__utils__factor;
}
app.config = app__config;
app.utils.factor = app__utils__factor;
app.utils.calculate = app__utils__calculate;`,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			// TODO: Remove this once variable references are fully qualified
			t.Skipf("Skipping test %s", name)

			// Create a mock dependency graph with test data
			depGraph := &dep_graph.DepGraph{
				Decls:         btree.Map[dep_graph.DeclID, ast.Decl]{},
				Deps:          btree.Map[dep_graph.DeclID, btree.Set[dep_graph.DeclID]]{},
				ValueBindings: btree.Map[string, dep_graph.DeclID]{},
				TypeBindings:  btree.Map[string, dep_graph.DeclID]{},
				DeclNamespace: btree.Map[dep_graph.DeclID, string]{},
			}

			var declIDs []dep_graph.DeclID

			// Parse declarations and populate the dependency graph
			for i, source := range test.declSources {
				declID := dep_graph.DeclID(i + 1)
				decl := parseDecl(t, source)

				// Store the declaration
				depGraph.Decls.Set(declID, decl)
				depGraph.DeclNamespace.Set(declID, test.namespaces[i])
				declIDs = append(declIDs, declID)

				// Extract bindings and add them to the dependency graph
				switch d := decl.(type) {
				case *ast.VarDecl:
					bindingNames := ast.FindBindings(d.Pattern)
					for name := range bindingNames {
						// Create fully qualified name if in namespace
						qualifiedName := name
						if test.namespaces[i] != "" {
							qualifiedName = test.namespaces[i] + "." + name
						}
						depGraph.ValueBindings.Set(qualifiedName, declID)
					}
				case *ast.FuncDecl:
					if d.Name != nil && d.Name.Name != "" {
						name := d.Name.Name
						// Create fully qualified name if in namespace
						qualifiedName := name
						if test.namespaces[i] != "" {
							qualifiedName = test.namespaces[i] + "." + name
						}
						depGraph.ValueBindings.Set(qualifiedName, declID)
					}
				case *ast.TypeDecl:
					if d.Name != nil && d.Name.Name != "" {
						name := d.Name.Name
						// Create fully qualified name if in namespace
						qualifiedName := name
						if test.namespaces[i] != "" {
							qualifiedName = test.namespaces[i] + "." + name
						}
						depGraph.TypeBindings.Set(qualifiedName, declID)
					}
				}
			}

			// Set up dependencies
			for declIndex, depIndices := range test.dependencies {
				declID := dep_graph.DeclID(declIndex + 1)
				var deps btree.Set[dep_graph.DeclID]

				for _, depIndex := range depIndices {
					depDeclID := dep_graph.DeclID(depIndex + 1)
					deps.Insert(depDeclID)
				}

				depGraph.Deps.Set(declID, deps)
			}

			// Create a builder and test BuildDecls
			builder := &Builder{tempId: 0}
			module := builder.BuildTopLevelDecls(declIDs, depGraph)

			// Use the printer to generate the output
			printer := NewPrinter()
			for i, stmt := range module.Stmts {
				if i > 0 {
					printer.NewLine()
				}
				printer.PrintStmt(stmt)
			}

			assert.Equal(t, test.expected, printer.Output, "Generated module should match expected output")
		})
	}
}

func TestBuildDecls_EdgeCases(t *testing.T) {
	tests := map[string]struct {
		declSources []string
		namespaces  []string
		expected    string
	}{
		"Empty_Declarations": {
			declSources: []string{},
			namespaces:  []string{},
			expected:    "",
		},
		"Only_Type_Declarations": {
			declSources: []string{
				"type User = {name: string}",
				"type Config = {debug: boolean}",
			},
			namespaces: []string{"models", "app"},
			expected: `const app = {};
const models = {};`,
		},
		"Deep_Namespace_Hierarchy": {
			declSources: []string{
				"val constant = \"deep\"",
			},
			namespaces: []string{"company.project.module.submodule.utils"},
			expected: `const company = {};
company.project = {};
company.project.module = {};
company.project.module.submodule = {};
company.project.module.submodule.utils = {};
const company__project__module__submodule__utils__constant = "deep";
company.project.module.submodule.utils.constant = company__project__module__submodule__utils__constant;`,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			// Create a mock dependency graph with test data
			depGraph := &dep_graph.DepGraph{
				Decls:         btree.Map[dep_graph.DeclID, ast.Decl]{},
				Deps:          btree.Map[dep_graph.DeclID, btree.Set[dep_graph.DeclID]]{},
				ValueBindings: btree.Map[string, dep_graph.DeclID]{},
				TypeBindings:  btree.Map[string, dep_graph.DeclID]{},
				DeclNamespace: btree.Map[dep_graph.DeclID, string]{},
			}

			var declIDs []dep_graph.DeclID

			// Parse declarations and populate the dependency graph
			for i, source := range test.declSources {
				declID := dep_graph.DeclID(i + 1)
				decl := parseDecl(t, source)

				// Store the declaration
				depGraph.Decls.Set(declID, decl)
				depGraph.DeclNamespace.Set(declID, test.namespaces[i])
				declIDs = append(declIDs, declID)

				// Extract bindings and add them to the dependency graph
				switch d := decl.(type) {
				case *ast.VarDecl:
					bindingNames := ast.FindBindings(d.Pattern)
					for name := range bindingNames {
						// Create fully qualified name if in namespace
						qualifiedName := name
						if test.namespaces[i] != "" {
							qualifiedName = test.namespaces[i] + "." + name
						}
						depGraph.ValueBindings.Set(qualifiedName, declID)
					}
				case *ast.FuncDecl:
					if d.Name != nil && d.Name.Name != "" {
						name := d.Name.Name
						// Create fully qualified name if in namespace
						qualifiedName := name
						if test.namespaces[i] != "" {
							qualifiedName = test.namespaces[i] + "." + name
						}
						depGraph.ValueBindings.Set(qualifiedName, declID)
					}
				case *ast.TypeDecl:
					if d.Name != nil && d.Name.Name != "" {
						name := d.Name.Name
						// Create fully qualified name if in namespace
						qualifiedName := name
						if test.namespaces[i] != "" {
							qualifiedName = test.namespaces[i] + "." + name
						}
						depGraph.TypeBindings.Set(qualifiedName, declID)
					}
				}

				// Create empty dependencies for each declaration
				var deps btree.Set[dep_graph.DeclID]
				depGraph.Deps.Set(declID, deps)
			}

			// Create a builder and test BuildDecls
			builder := &Builder{tempId: 0}
			module := builder.BuildTopLevelDecls(declIDs, depGraph)

			// Use the printer to generate the output
			printer := NewPrinter()
			for i, stmt := range module.Stmts {
				if i > 0 {
					printer.NewLine()
				}
				printer.PrintStmt(stmt)
			}

			assert.Equal(t, test.expected, printer.Output, "Generated module should match expected output")
		})
	}
}

// Helper function to parse a declaration from a source string
func parseDecl(t *testing.T, source string) ast.Decl {
	astSource := &ast.Source{
		ID:       0,
		Path:     "test.esc",
		Contents: source,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	parser := parser.NewParser(ctx, astSource)
	decl := parser.Decl()

	if decl == nil {
		t.Fatalf("Failed to parse declaration: %s", source)
	}

	return decl
}
