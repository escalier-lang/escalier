package solver

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// checkProductive accepts an alias whose every recursion emits a level of structure. Each case names
// a shape whose laps build an infinite tree, so the alias denotes a type even when no finite
// expansion of it exists.
func TestCheckProductiveAccepts(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{
			// The canonical productive recursion. Each lap emits one `{head, tail}` object.
			name: "ObjectWrapper",
			src:  `type List<T> = {head: T, tail?: List<T>}`,
		},
		{
			// A non-generic recursive alias is productive on the same terms: the object it emits
			// each lap is what makes the infinite tree well defined.
			name: "NoTypeParameters",
			src:  `type Json = number | string | {items: Json}`,
		},
		{
			// The simplest productive self-reference, with no union and no parameter to carry.
			name: "SelfReference",
			src:  `type SelfA = {a: SelfA}`,
		},
		{
			// The DOM shape. One recursive reference sits under an object and the other under a
			// tuple, and both guard.
			name: "ObjectAndTuple",
			src:  `type Node = {parent?: Node, children: [Node]}`,
		},
		{
			// The mapped type emits an object per lap, and the recursive reference sits in its value
			// expression.
			name: "MappedValue",
			src:  `type DeepPartial<T> = {[K]?: DeepPartial<T[K]> for K in keyof T}`,
		},
		{
			// The recursion grows its own argument every lap, so the instantiations never repeat and
			// the type is not regular. It is still productive, and productivity is what decides
			// whether the alias denotes a type. TypeScript accepts this shape too.
			name: "GrowingArgumentUnderObject",
			src:  `type Deep<T> = {a: Deep<{b: T}>}`,
		},
		{
			// The recursive reference sits under an object even though a union sits above it, so the
			// union does not leave it at the top of the body.
			name: "UnionAroundObject",
			src: `
				type Wrap<T> = {value: T}
				type Chain<T> = {next?: Chain<T | number>} | Wrap<T>
			`,
		},
		{
			// A conditional's branch guards, since reaching the branch means some instantiation can
			// take the other one and stop. This is the shape every recursive utility type takes.
			name: "ConditionalBranch",
			src:  `type Flatten<T> = if T : [infer U] { Flatten<U> } else { T }`,
		},
		{
			// The same shape with the recursion in the Else branch and the base case in the Then.
			name: "ConditionalBaseCase",
			src:  `type Shrink<T> = if T : never { never } else { Shrink<{a: T}> }`,
		},
		{
			// The reference is to an alias outside any cycle, so nothing returns to `Wrap` at all.
			name: "NonRecursiveReference",
			src: `
				type Box<T> = {value: T}
				type Wrap<T> = Box<{a: T}>
			`,
		},
		{
			// Mutual recursion where each hop emits an object.
			name: "MutualUnderObjects",
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

// checkProductive rejects an alias that reaches itself with no type constructor in between. Each
// case names a shape whose laps emit nothing, so the alias satisfies its own equation without
// naming any type.
func TestCheckProductiveRejects(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			// The bare self-reference. `type Bad = Bad` holds of every type, so it picks none.
			name: "BareSelfReference",
			src:  `type Bad = Bad`,
			want: []string{
				"2:10-2:13: recursive type alias `Bad` reaches itself without passing under a type " +
					"constructor, so no lap of the recursion emits any structure and the alias names no " +
					"type; wrap the recursive reference in an object, tuple, or function type",
			},
		},
		{
			// The generic twin. `G(T) = G({a: T})` is satisfied by every constant function.
			name: "SelfReferenceWithGrowingArgument",
			src:  `type Grow<T> = Grow<{a: T}>`,
			want: []string{
				"2:10-2:14: recursive type alias `Grow` reaches itself without passing under a type " +
					"constructor, so no lap of the recursion emits any structure and the alias names no " +
					"type; wrap the recursive reference in an object, tuple, or function type",
			},
		},
		{
			// A union is a choice among its members rather than a wrapper, so it does not guard.
			// Unfolding this alias widens the union forever instead of building a tree.
			name: "UnderUnion",
			src:  `type A<T> = {x: T} | A<{y: T}>`,
			want: []string{
				"2:10-2:11: recursive type alias `A` reaches itself without passing under a type " +
					"constructor, so no lap of the recursion emits any structure and the alias names no " +
					"type; wrap the recursive reference in an object, tuple, or function type",
			},
		},
		{
			// An indexed access reads a component out of its target rather than wrapping it, so a
			// lap through one emits nothing.
			name: "UnderIndexedAccess",
			src:  `type Grow<T> = Grow<{a: T, b: T}>["a"]`,
			want: []string{
				"2:10-2:14: recursive type alias `Grow` reaches itself without passing under a type " +
					"constructor, so no lap of the recursion emits any structure and the alias names no " +
					"type; wrap the recursive reference in an object, tuple, or function type",
			},
		},
		{
			// `keyof` projects a key set out of its operand, which is a read rather than a wrap.
			name: "UnderKeyof",
			src:  `type Grow<T> = keyof Grow<{a: T, b: T}>`,
			want: []string{
				"2:10-2:14: recursive type alias `Grow` reaches itself without passing under a type " +
					"constructor, so no lap of the recursion emits any structure and the alias names no " +
					"type; wrap the recursive reference in an object, tuple, or function type",
			},
		},
		{
			// An object spread merges its operand's fields into the object rather than nesting them
			// under one, so the enclosing `{…}` guards nothing here.
			name: "UnderObjectSpread",
			src:  `type Grow<T> = {...Grow<{a: T}>}`,
			want: []string{
				"2:10-2:14: recursive type alias `Grow` reaches itself without passing under a type " +
					"constructor, so no lap of the recursion emits any structure and the alias names no " +
					"type; wrap the recursive reference in an object, tuple, or function type",
			},
		},
		{
			// The positional twin. A `...P` element splices its operand's elements into the tuple.
			name: "UnderTupleSpread",
			src:  `type Grow<T> = [...Grow<[T]>]`,
			want: []string{
				"2:10-2:14: recursive type alias `Grow` reaches itself without passing under a type " +
					"constructor, so no lap of the recursion emits any structure and the alias names no " +
					"type; wrap the recursive reference in an object, tuple, or function type",
			},
		},
		{
			// A conditional guards its branches but not its Check, which every lap evaluates.
			name: "UnderConditionalCheck",
			src:  `type Bad = if Bad : number { number } else { string }`,
			want: []string{
				"2:10-2:13: recursive type alias `Bad` reaches itself without passing under a type " +
					"constructor, so no lap of the recursion emits any structure and the alias names no " +
					"type; wrap the recursive reference in an object, tuple, or function type",
			},
		},
		{
			// Mutual recursion. Neither body names itself, so the cycle is found through the alias
			// reference graph, and each alias on it names the others in its own diagnostic.
			name: "MutualRecursion",
			src: `
				type A<T> = B<{x: T}>
				type B<U> = A<U>
			`,
			want: []string{
				"4:10-4:11: recursive type alias `B` reaches itself through `A` without passing under " +
					"a type constructor, so no lap of the recursion emits any structure and the alias " +
					"names no type; wrap the recursive reference in an object, tuple, or function type",
				"3:10-3:11: recursive type alias `A` reaches itself through `B` without passing under " +
					"a type constructor, so no lap of the recursion emits any structure and the alias " +
					"names no type; wrap the recursive reference in an object, tuple, or function type",
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

// A value checks against a productive alias whose instantiations never repeat. `Deep<number>` has no
// finite expansion — its payloads are `{b: number}`, then `{b: {b: number}}`, and so on — so
// constrain cannot decide the constraint by unfolding it. Two references to one instantiation denote
// one type, and subtyping is reflexive, so the canonical identity settles it with no unfolding at
// all. Each case reaches that identity by a different route: one directly, one through the object
// the alias emits.
func TestInferNonRegularAliasChecks(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{
			name: "SameInstantiationDirectly",
			src: `
				type Deep<T> = {a: Deep<{b: T}>}
				declare fn make() -> Deep<number>
				val d: Deep<number> = make()
			`,
		},
		{
			name: "SameInstantiationUnderTheEmittedObject",
			src: `
				type Deep<T> = {a: Deep<{b: T}>}
				declare fn make() -> Deep<{b: number}>
				val d: Deep<number> = {a: make()}
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

// Comparing two instantiations of a non-regular alias whose parameter reaches the type it denotes
// has no finite answer. Every lap reaches a pair of instantiations no earlier lap did, so neither
// the reflexive shortcut nor the coinductive seen-set closes it. The walk reports the mismatch it
// finds at the first `here` field, then runs on until maxUnwrapDepth stops it, and that diagnostic
// names the alias rather than the arguments the walk had grown by then. `Deep`, whose parameter never
// reaches the type it denotes, settles instead — see TestInferPhantomArgumentsCompareEqual.
func TestInferNonRegularAliasComparisonDoesNotSettle(t *testing.T) {
	_, _, errs := inferSource(t, `
		type Nest<T> = {here: T, deeper: Nest<{b: T}>}
		declare fn make() -> Nest<number>
		val d: Nest<string> = make()
	`)
	require.Equal(t, []string{
		"4:25-4:31: cannot constrain number <: string",
		"4:25-4:31: comparing two instantiations of `Nest` reached the limit of 200 type-operator " +
			"expansions and was cut off; either the two sides recurse without ever repeating a pair " +
			"the check can close on, or their alias chains run deeper than the limit unfolds",
	}, messagesWithSpan(errs))
}

// The same limit cuts off a chain of aliases that each name the one below them, which settles in
// principle but not within the budget. The chain is what the second half of the diagnostic names,
// and the evaluator's maxExpandDepth truncates the same shape the same way — see
// TestInferSpreadChainBudgetsTerminate. A chain this long is pathological, so paying for it with a
// diagnostic rather than a larger budget keeps the non-regular comparison above cheap to cut off.
func TestInferDeepAliasChainReachesTheLimit(t *testing.T) {
	src := "type A0 = number\n"
	for i := 1; i <= maxUnwrapDepth+1; i++ {
		src += fmt.Sprintf("type A%d = A%d\n", i, i-1)
	}
	src += fmt.Sprintf("val v: A%d = 1\n", maxUnwrapDepth+1)
	_, _, errs := inferSource(t, src)
	require.Len(t, errs, 1)
	require.Equal(t,
		"comparing 1 with A1 reached the limit of 200 type-operator expansions and was cut off; "+
			"either the two sides recurse without ever repeating a pair the check can close on, or "+
			"their alias chains run deeper than the limit unfolds",
		errs[0].Message())
}

// A non-productive alias absorbs at every constraint site, the way an ErrorType operand does. Its
// definition-time diagnostic already says the alias names no type, so a value checked against it
// would only produce a second failure derived from the first.
func TestInferNotProductiveAliasAbsorbs(t *testing.T) {
	_, _, errs := inferSource(t, `
		type Grow<T> = Grow<{a: T}>
		val v: Grow<number> = 1
	`)
	require.Equal(t, []string{
		"2:8-2:12: recursive type alias `Grow` reaches itself without passing under a type " +
			"constructor, so no lap of the recursion emits any structure and the alias names no type; " +
			"wrap the recursive reference in an object, tuple, or function type",
	}, messagesWithSpan(errs))
}
