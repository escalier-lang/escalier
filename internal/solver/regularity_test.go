package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// checkRegular accepts an alias whose recursion carries its parameters around the cycle without
// wrapping them. Each case names a shape the reachable-instantiation set stays finite for, so the
// evaluator's active-state guard closes the recursion on its own.
func TestCheckRegularAccepts(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{
			// The canonical regular recursion. `List<number>` expands to a body naming
			// `List<number>` again, the identical instantiation state.
			name: "PassThroughParameter",
			src:  `type List<T> = {head: T, tail?: List<T>}`,
		},
		{
			// A non-generic recursive alias has no parameter to grow, so no instantiation state
			// varies from lap to lap.
			name: "NoTypeParameters",
			src:  `type Json = number | string | {items: Json}`,
		},
		{
			// The recursive argument reads a component out of the parameter rather than wrapping
			// it, so the argument is no larger than the parameter it came from.
			name: "IndexedAccessArgument",
			src:  `type DeepPartial<T> = {[K]?: DeepPartial<T[K]> for K in keyof T}`,
		},
		{
			// A union is a choice among its members rather than a wrapper, so `T | number` carries
			// T at the same size the bare parameter has.
			name: "UnionArgument",
			src: `
				type Wrap<T> = {value: T}
				type Chain<T> = {next?: Chain<T | number>} | Wrap<T>
			`,
		},
		{
			// The recursion passes a type an `infer` clause captured, which is a component of the
			// checked type rather than a wrapper around the parameter.
			name: "InferCapture",
			src:  `type Flatten<T> = if T : [infer U] { Flatten<U> } else { T }`,
		},
		{
			// The parameter is wrapped, but the reference is to an alias outside any cycle, so the
			// expansion finishes after one step.
			name: "NonRecursiveWrapping",
			src: `
				type Box<T> = {value: T}
				type Wrap<T> = Box<{a: T}>
			`,
		},
		{
			// Mutual recursion that passes both parameters through is regular the same way a
			// self-recursive pass-through is.
			name: "MutualPassThrough",
			src: `
				type A<T> = {b?: B<T>}
				type B<T> = {a?: A<T>}
			`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, errs := inferSource(t, test.src)
			require.Empty(t, messagesWithSpan(errs))
		})
	}
}

// checkRegular rejects an alias that wraps one of its own parameters in the argument of a recursive
// reference. Each case names a shape whose argument is strictly larger every lap, so its
// instantiation state never repeats and only the runtime budgets would stop the expansion.
func TestCheckRegularRejects(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			// The canonical rejection. The argument gains an object wrapper every lap.
			name: "SelfRecursiveObjectWrapper",
			src:  `type Grow<T> = Grow<{a: T}>`,
			want: []string{
				"2:10-2:14: recursive type alias `Grow` grows type parameter `T` under a type " +
					"constructor, so its expansion is unbounded; break the recursion with a nominal " +
					"type or pass `T` through unwrapped",
			},
		},
		{
			// The recursive reference sits inside a union, which does not shield the growing
			// argument from the check.
			name: "RecursiveReferenceInUnion",
			src:  `type A<T> = {x: T} | A<{y: T}>`,
			want: []string{
				"2:10-2:11: recursive type alias `A` grows type parameter `T` under a type " +
					"constructor, so its expansion is unbounded; break the recursion with a nominal " +
					"type or pass `T` through unwrapped",
			},
		},
		{
			// The recursive reference sits under an indexed access. The access does not grow its
			// target, but the argument it is applied to still does.
			name: "UnderIndexedAccess",
			src:  `type Grow<T> = Grow<{a: T, b: T}>["a"]`,
			want: []string{
				"2:10-2:14: recursive type alias `Grow` grows type parameter `T` under a type " +
					"constructor, so its expansion is unbounded; break the recursion with a nominal " +
					"type or pass `T` through unwrapped",
			},
		},
		{
			// The reference is the value expression of a mapped type, which reduces it once per
			// key. The enclosing object does not count against the argument, but the `{a: T}` the
			// argument is written as does.
			name: "InMappedValue",
			src:  `type Grow<T> = {[K]: Grow<{a: T}>[K] for K in keyof T}`,
			want: []string{
				"2:10-2:14: recursive type alias `Grow` grows type parameter `T` under a type " +
					"constructor, so its expansion is unbounded; break the recursion with a nominal " +
					"type or pass `T` through unwrapped",
			},
		},
		{
			// Mutual recursion. Neither body names itself, so the cycle is found through the alias
			// reference graph, and only the alias that wraps its own parameter is blamed.
			name: "MutualRecursion",
			src: `
				type A<T> = B<{x: T}>
				type B<U> = A<U>
			`,
			want: []string{
				"3:10-3:11: recursive type alias `A` grows type parameter `T` under a type " +
					"constructor, so its expansion is unbounded; break the recursion with a nominal " +
					"type or pass `T` through unwrapped",
			},
		},
		{
			// Two parameters grow, so each is blamed on its own, in the order the source declares
			// them rather than alphabetically.
			name: "BothParametersGrow",
			src:  `type Grow<T, U> = Grow<{a: U}, [T]>`,
			want: []string{
				"2:10-2:14: recursive type alias `Grow` grows type parameter `T` under a type " +
					"constructor, so its expansion is unbounded; break the recursion with a nominal " +
					"type or pass `T` through unwrapped",
				"2:10-2:14: recursive type alias `Grow` grows type parameter `U` under a type " +
					"constructor, so its expansion is unbounded; break the recursion with a nominal " +
					"type or pass `U` through unwrapped",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, errs := inferSource(t, "\n\t\t\t\t"+test.src+"\n")
			require.Equal(t, test.want, messagesWithSpan(errs))
		})
	}
}

// The check is sound but incomplete, and this is the incompleteness. `Grow` never expands past its
// base case, since `Grow<never>` selects the `never` branch and stops. Deciding that in general is
// the halting problem. The check sees only that the recursive argument wraps the parameter, so it
// rejects the alias. The case is here so the boundary is pinned rather than discovered by a user.
func TestCheckRegularRejectsTerminatingBaseCase(t *testing.T) {
	_, _, errs := inferSource(t, `
		type Grow<T> = if T : never { never } else { Grow<{a: T}> }
	`)
	require.Equal(t, []string{
		"2:8-2:12: recursive type alias `Grow` grows type parameter `T` under a type " +
			"constructor, so its expansion is unbounded; break the recursion with a nominal " +
			"type or pass `T` through unwrapped",
	}, messagesWithSpan(errs))
}
