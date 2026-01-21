package dep_graph

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/assert"
)

func TestMergeOverloadedFunctions(t *testing.T) {
	tests := map[string]struct {
		source            string
		description       string
		expectedDeclCount int                                    // After merging, how many decls should remain
		checkDeps         func(t *testing.T, depGraph *DepGraph) // Custom dependency checks
	}{
		"TwoOverloadsWithDifferentDeps": {
			source: `
				type NumberConfig = {value: number}
				type StringConfig = {text: string}
				
				fn process(config: NumberConfig) -> string {
					return "number"
				}
				
				fn process(config: StringConfig) -> string {
					return "string"
				}
			`,
			description:       "Two overloads with different type dependencies should merge",
			expectedDeclCount: 3, // 2 types + 1 merged function
			checkDeps: func(t *testing.T, depGraph *DepGraph) {
				// Debug: print all value bindings
				t.Log("Value bindings after merge:")
				iter := depGraph.ValueBindings.Iter()
				for ok := iter.First(); ok; ok = iter.Next() {
					name := iter.Key()
					id := iter.Value()
					t.Logf("  %s -> DeclID(%d)", name, id)
				}

				// Find the process function
				processDeclID, found := depGraph.ValueBindings.Get("process")
				assert.True(t, found, "process function should be in value bindings")

				// Get its dependencies
				deps := depGraph.GetDeclDeps(processDeclID)

				// Debug: print dependencies
				t.Logf("process dependencies: %v", deps)

				// Should depend on both NumberConfig and StringConfig
				numberConfigID, _ := depGraph.TypeBindings.Get("NumberConfig")
				stringConfigID, _ := depGraph.TypeBindings.Get("StringConfig")

				t.Logf("NumberConfig ID: %d, StringConfig ID: %d", numberConfigID, stringConfigID)

				assert.True(t, deps.Contains(numberConfigID), "process should depend on NumberConfig")
				assert.True(t, deps.Contains(stringConfigID), "process should depend on StringConfig")
			},
		},
		"ThreeOverloadsWithChainedDeps": {
			source: `
				type TypeA = {a: number}
				type TypeB = {b: string}
				type TypeC = {c: boolean}
				
				fn handler(x: TypeA) -> string {
					return "a"
				}
				
				fn handler(x: TypeB) -> string {
					return "b"
				}
				
				fn handler(x: TypeC) -> string {
					return "c"
				}
			`,
			description:       "Three overloads should merge into single node",
			expectedDeclCount: 4, // 3 types + 1 merged function
			checkDeps: func(t *testing.T, depGraph *DepGraph) {
				handlerDeclID, found := depGraph.ValueBindings.Get("handler")
				assert.True(t, found, "handler function should be in value bindings")

				deps := depGraph.GetDeclDeps(handlerDeclID)

				typeAID, _ := depGraph.TypeBindings.Get("TypeA")
				typeBID, _ := depGraph.TypeBindings.Get("TypeB")
				typeCID, _ := depGraph.TypeBindings.Get("TypeC")

				assert.True(t, deps.Contains(typeAID), "handler should depend on TypeA")
				assert.True(t, deps.Contains(typeBID), "handler should depend on TypeB")
				assert.True(t, deps.Contains(typeCID), "handler should depend on TypeC")
			},
		},
		"SingleFunctionNoMerge": {
			source: `
				type Config = {value: number}
				
				fn process(config: Config) -> string {
					return "ok"
				}
			`,
			description:       "Single function should not be affected by merge",
			expectedDeclCount: 2, // 1 type + 1 function
			checkDeps: func(t *testing.T, depGraph *DepGraph) {
				processDeclID, found := depGraph.ValueBindings.Get("process")
				assert.True(t, found, "process function should be in value bindings")

				deps := depGraph.GetDeclDeps(processDeclID)
				configID, _ := depGraph.TypeBindings.Get("Config")
				assert.True(t, deps.Contains(configID), "process should depend on Config")
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			source := &ast.Source{
				ID:       0,
				Path:     "test.esc",
				Contents: test.source,
			}

			sources := []*ast.Source{source}
			module, parseErrors := parser.ParseLibFiles(ctx, sources)

			assert.Len(t, parseErrors, 0, "Parse errors for %s: %v", test.description, parseErrors)
			assert.NotNil(t, module, "Module should not be nil for %s", test.description)

			// Build dependency graph
			depGraph := BuildDepGraph(module)
			assert.NotNil(t, depGraph, "Dependency graph should not be nil for %s", test.description)

			// Before merging, count total decls
			declCountBefore := len(depGraph.Decls)
			t.Logf("Decls before merge: %d", declCountBefore)

			// Debug: print bindings before merge
			t.Log("Value bindings BEFORE merge:")
			iter := depGraph.ValueBindings.Iter()
			for ok := iter.First(); ok; ok = iter.Next() {
				name := iter.Key()
				id := iter.Value()
				decl, _ := depGraph.GetDecl(id)
				t.Logf("  %s -> DeclID(%d) %T", name, id, decl)
			}

			// Create a mock overloadDecls map
			// We need to scan through the module to find overloaded functions
			overloadDecls := make(map[string][]*ast.FuncDecl)
			nsIter := module.Namespaces.Iter()
			for ok := nsIter.First(); ok; ok = nsIter.Next() {
				ns := nsIter.Value()
				for _, decl := range ns.Decls {
					if funcDecl, ok := decl.(*ast.FuncDecl); ok {
						funcName := funcDecl.Name.Name
						overloadDecls[funcName] = append(overloadDecls[funcName], funcDecl)
					}
				}
			}

			// Merge overloaded functions
			depGraph.MergeOverloadedFunctions(overloadDecls)

			// After merging, check decl count
			declCountAfter := len(depGraph.Decls)
			t.Logf("Decls after merge: %d", declCountAfter)
			assert.Equal(t, test.expectedDeclCount, declCountAfter,
				"Expected %d declarations after merge for %s", test.expectedDeclCount, test.description)

			// Run custom dependency checks
			if test.checkDeps != nil {
				test.checkDeps(t, depGraph)
			}

			// Verify that Components were recomputed
			assert.NotNil(t, depGraph.Components, "Components should be recomputed after merge")
		})
	}
}
