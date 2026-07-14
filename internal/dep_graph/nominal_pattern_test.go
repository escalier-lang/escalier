package dep_graph

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/require"
)

// TestNominalPatternDependency checks that a binding deconstructing a class through an
// InstancePat or ExtractorPat records a VALUE dependency on that class. The class's
// projected member body is filled in only when its value key is inferred, so without
// this edge the reader can be ordered before the class's real inference and bind against
// the empty ClassDef shell, resolving each field to `never`. Regression test for the
// flaky nominal-pattern inference the CI run surfaced.
func TestNominalPatternDependency(t *testing.T) {
	tests := map[string]string{
		"MatchInstancePat": `
			class Point { x: number, y: number }
			fn f(p: Point) {
				return match p {
					Point { x, y } => x
				}
			}
		`,
		"MatchExtractorPat": `
			class Point { x: number, y: number }
			fn f(p: Point) {
				return match p {
					Point(x, y) => x
				}
			}
		`,
		"LocalValInstancePat": `
			class Point { x: number, y: number }
			fn f(p: Point) {
				val Point { x, y } = p
				return x
			}
		`,
		"NestedInstancePat": `
			class Point { x: number, y: number }
			fn f(pairs: [Point]) {
				return match pairs {
					[Point { x, y }] => x
				}
			}
		`,
	}
	for name, src := range tests {
		t.Run(name, func(t *testing.T) {
			source := &ast.Source{ID: 0, Path: "test.esc", Contents: src}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			module, errs := parser.ParseLibFiles(ctx, []*ast.Source{source})
			require.Empty(t, errs, "Expected no parsing errors")

			depGraph := BuildDepGraph(module)
			fKey := ValueBindingKey("f")
			pointValue := ValueBindingKey("Point")
			require.True(t, depGraph.HasBinding(fKey), "value:f should exist")
			require.True(t, depGraph.HasBinding(pointValue), "value:Point should exist")
			fDeps := depGraph.GetDeps(fKey)
			require.True(t, fDeps.Contains(pointValue),
				"f should depend on value:Point through the nominal pattern")
		})
	}
}
