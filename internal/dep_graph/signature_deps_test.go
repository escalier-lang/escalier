package dep_graph

import (
	"context"
	"strings"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/btree"
)

// TestSignatureBoundKeys covers which function keys the graph binds from their signatures
// and the dependencies it gives them. For the key named in each case it asserts:
//   - whether the key is signature-bound;
//   - whether it has a body key;
//   - its dependencies in the check order;
//   - its body key's dependencies, when it has one.
//
// A key is signature-bound when every declaration under it is a function whose signature
// writes each parameter type and the return type with no `_`.
func TestSignatureBoundKeys(t *testing.T) {
	tests := []struct {
		name      string
		src       string
		key       BindingKey
		bound     bool
		hasBody   bool
		checkDeps string
		bodyDeps  string
	}{
		{
			name: "CompleteSignatureDependsOnlyOnWhatItNames",
			src: `
				type Shape = {w: number, h: number}
				val scale = 2
				fn area(s: Shape) -> number { return s.w * s.h * scale }
			`,
			key:       ValueBindingKey("area"),
			bound:     true,
			hasBody:   true,
			checkDeps: "type:Shape",
			bodyDeps:  "type:Shape value:area value:scale",
		},
		{
			name: "TypeParameterBoundCountsAsSignature",
			src: `
				type Shape = {w: number, h: number}
				fn first<T: Shape>(xs: Array<T>) -> number { return 0 }
			`,
			key:       ValueBindingKey("first"),
			bound:     true,
			hasBody:   true,
			checkDeps: "type:Shape",
			bodyDeps:  "type:Shape value:first",
		},
		{
			name: "MissingReturnTypeKeepsBodyDependencies",
			src: `
				val scale = 2
				fn double(n: number) { return n * scale }
			`,
			key:       ValueBindingKey("double"),
			checkDeps: "value:scale",
		},
		{
			name: "MissingParamTypeKeepsBodyDependencies",
			src: `
				val scale = 2
				fn double(n) -> number { return n * scale }
			`,
			key:       ValueBindingKey("double"),
			checkDeps: "value:scale",
		},
		{
			name: "InferredErrorTypeKeepsBodyDependencies",
			src: `
				val scale = 2
				fn double(n: number) -> number throws _ { return n * scale }
			`,
			key:       ValueBindingKey("double"),
			checkDeps: "value:scale",
		},
		{
			name: "NestedPlaceholderKeepsBodyDependencies",
			src: `
				val scale = 2
				fn apply(f: fn (x: number) -> _) -> number { return scale }
			`,
			key:       ValueBindingKey("apply"),
			checkDeps: "value:scale",
		},
		{
			name: "DeclaredFunctionHasNoBodyKey",
			src: `
				type Shape = {w: number, h: number}
				declare fn area(s: Shape) -> number
			`,
			key:       ValueBindingKey("area"),
			bound:     true,
			checkDeps: "type:Shape",
		},
		{
			name: "OverloadWithEveryArmComplete",
			src: `
				val scale = 2
				fn size(x: number) -> number { return x * scale }
				fn size(x: string) -> number { return 0 }
			`,
			key:      ValueBindingKey("size"),
			bound:    true,
			hasBody:  true,
			bodyDeps: "value:scale value:size",
		},
		{
			name: "OverloadWithAnIncompleteArm",
			src: `
				val scale = 2
				fn size(x: number) -> number { return x * scale }
				fn size(x: string) { return 0 }
			`,
			key:       ValueBindingKey("size"),
			checkDeps: "value:scale",
		},
		{
			name: "NamespacedFunction",
			src: `
				namespace geom {
					type Shape = {w: number, h: number}
					fn area(s: Shape) -> number { return s.w * s.h }
				}
			`,
			key:       ValueBindingKey("geom.area"),
			bound:     true,
			hasBody:   true,
			checkDeps: "type:geom.Shape",
			bodyDeps:  "type:geom.Shape value:geom.area",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			module, errs := parser.ParseLibFiles(context.Background(), []*ast.Source{
				{ID: 0, Path: "test.esc", Contents: tt.src},
			})
			require.Empty(t, errs)
			graph := BuildDepGraph(module)
			require.Equal(t, tt.bound, graph.IsSignatureBound(tt.key))
			require.Equal(t, tt.hasBody, graph.HasBodyKey(tt.key))
			require.Equal(t, tt.checkDeps, keysString(graph.CheckDeps(tt.key)))
			if tt.hasBody {
				require.Equal(t, tt.bodyDeps, keysString(graph.CheckDeps(BodyBindingKey(tt.key.Name()))))
			}
		})
	}
}

// keysString renders a set of keys in its sorted order, separated by spaces.
func keysString(keys btree.Set[BindingKey]) string {
	var parts []string
	iter := keys.Iter()
	for ok := iter.First(); ok; ok = iter.Next() {
		parts = append(parts, iter.Key().String())
	}
	return strings.Join(parts, " ")
}
