package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// TestMultiBoundRenders is the M1 end-to-end demo that the engine→coalesce→print
// pipeline is wired up correctly: a variable with two distinct lower bounds,
// coalesced in positive position, renders as a union of those bounds.
//
// It lives in package solver (not soltype) because it drives the engine's
// unexported Context/freshVar and coalesce, then reaches soltype.Print across
// the package boundary — soltype must not import solver (m1-implementation-plan
// §3.1), so the pipeline can only be exercised from this side.
func TestMultiBoundRenders(t *testing.T) {
	c := &Context{}
	a := c.freshVar(1)
	a.LowerBounds = []soltype.Type{num(), str()}
	got := soltype.Print(coalesce(a, soltype.Positive))
	require.Equal(t, "number | string", got)
}

// A class, alias, or enum keeps its type parameters in the Context registry rather than in
// the type it binds, so a binding renders them under the names the source wrote only when the
// registry is consulted. These cover both binding sorts end to end, from Escalier source
// through inference to the rendered string.
func TestBindingsRenderSourceTypeParamNames(t *testing.T) {
	tests := []struct {
		name       string
		src        string
		wantValues map[string]string
		wantTypes  map[string]string
	}{
		{
			name:       "ClassNamesItsOwnParameter",
			src:        `class Node<T> { value: T, tail: Node<T> }`,
			wantValues: map[string]string{"Node": "<T> {new (value: T, tail: Node<T>) -> Node<T>}"},
			wantTypes:  map[string]string{"Node": "Node<T>"},
		},
		{
			// The constructor takes v before k, so first appearance would order the binders
			// V, K. The prefix follows the `<K, V>` clause the declaration wrote instead.
			name: "ClassBindersFollowDeclarationOrder",
			src: `
				class Pair<K, V> {
					k: K,
					v: V,
					constructor(mut self, v: V, k: K) {
						self.v = v
						self.k = k
					},
				}
			`,
			wantValues: map[string]string{"Pair": "<K, V> {new (v: V, k: K) -> Pair<K, V>}"},
			wantTypes:  map[string]string{"Pair": "Pair<K, V>"},
		},
		{
			name:       "AliasNamesItsOwnParameter",
			src:        `type Alias<T> = {v: T}`,
			wantValues: map[string]string{},
			wantTypes:  map[string]string{"Alias": "{v: T}"},
		},
		{
			name:       "EnumNamesItsOwnParameter",
			src:        `enum Opt<T> { Some(v: T), None }`,
			wantValues: map[string]string{},
			wantTypes:  map[string]string{"Opt": "Opt.Some<T> | Opt.None<T>"},
		},
		{
			// The regression guard on the path that already names its parameters: a FuncType
			// carries its own TypeParams, so the printer reads the names off the type.
			name:       "FunctionKeepsItsOwnParameter",
			src:        `fn identity<T>(x: T) -> T { return x }`,
			wantValues: map[string]string{"identity": "fn <T>(x: T) -> T"},
			wantTypes:  map[string]string{},
		},
		{
			// Binding the class value again instantiates it, which freshens the variable
			// standing for T. The name is read off the argument position rather than matched
			// on the declaration's own variable, so both bindings render alike.
			name: "AnInstantiatedClassValueKeepsTheName",
			src: `
				class Box<T> { value: T }
				val Alias = Box
			`,
			wantValues: map[string]string{
				"Box":   "<T> {new (value: T) -> Box<T>}",
				"Alias": "<T> {new (value: T) -> Box<T>}",
			},
			wantTypes: map[string]string{"Box": "Box<T>"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, types, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.wantValues, values)
			require.Equal(t, tt.wantTypes, types)
		})
	}
}
