package codegen

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dep_graph"
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
