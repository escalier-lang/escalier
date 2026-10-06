package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/dep_graph"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// `Array` is the ingested declaration rather than a wrapper carrying an element type,
// so an array read resolves the members that declaration names. Each case takes the
// array as a parameter, since the type is what is under test rather than any value.
func TestArrayResolvesToTheIngestedClass(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want map[string]string
	}{
		{
			// The annotation resolves with nothing imported. The handle is read from the
			// package declaring `Array`, so the file needs no import to name it.
			name: "AnnotationResolvesWithNothingImported",
			src:  `fn f(xs: Array<number>) -> number { return 1 }`,
			want: map[string]string{"f": "fn (xs: Array<number>) -> number"},
		},
		{
			// A member the declaration names, which the retired concrete could not answer.
			name: "LengthReads",
			src:  `fn f(xs: Array<number>) { return xs.length }`,
			want: map[string]string{"f": "fn (xs: Array<number>) -> number"},
		},
		{
			// A method the declaration names, checked against its signature.
			name: "PushChecks",
			src:  `fn f(xs: mut Array<number>) { return xs.push(1) }`,
			want: map[string]string{"f": "fn (xs: mut Array<number>) -> number"},
		},
		{
			// A numeric key reads the element. An array declares no position for the key to
			// land on, so the element is the answer whatever the index is.
			name: "NumericLiteralIndexYieldsTheElement",
			src:  `fn f(xs: Array<number>) { return xs[0] }`,
			want: map[string]string{"f": "fn (xs: Array<number>) -> number"},
		},
		{
			name: "NumberTypedIndexYieldsTheElement",
			src:  `fn f(xs: Array<string>, k: number) { return xs[k] }`,
			want: map[string]string{"f": "fn (xs: Array<string>, k: number) -> string"},
		},
		{
			// Iteration binds the loop variable at the element type.
			name: "ForInBindsTheElement",
			src: `
				fn f(xs: Array<number>) {
					for x in xs {
						return x
					}
				}
			`,
			want: map[string]string{"f": "fn (xs: Array<number>) -> number"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errorMessagesOf(errs))
			for name, want := range tt.want {
				require.Equal(t, want, values[name], "binding %q", name)
			}
		})
	}
}

// `Array<T>` is covariant in T and `mut Array<T>` is invariant. `at(&self, index)` is an
// output position both views reach, and `push(&mut self, item: T)` an input position only a
// mutable reference reaches, so the widening holds for an immutable array while two mutable
// ones over different elements stay unrelated.
func TestArrayIsCovariantAndMutArrayIsNot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "SharedArrayWidens",
			src: `fn take(xs: Array<number>) -> number { return 1 }
				fn give(ys: Array<1>) { return take(ys) }`,
		},
		{
			name: "MutArrayDoesNotWiden",
			src: `fn take(xs: mut Array<number>) -> number { return 1 }
				fn give(ys: mut Array<1>) { return take(ys) }`,
			want: []string{"cannot constrain number <: 1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, _, errs := inferSource(t, tt.src)
			if len(tt.want) == 0 {
				require.Empty(t, errorMessagesOf(errs))
				return
			}
			require.Equal(t, tt.want, errorMessagesOf(errs))
		})
	}
}

// The committed `Array` keeps the variance the test prelude's does, and so do `Set` and `Map`.
// Their `&self` methods also take an element, as `includes(&self, searchElement: T)` and
// `has(&self, value: T)` do. Those methods cannot store the element, so an immutable view stays
// covariant. A `mut` view also reaches `push`, `add`, and `set`, which keeps it invariant.
func TestCommittedCollectionsAreCovariantAndMutOnesAreNot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "ArrayWidensIntoAUnion",
			src: `fn f(a: &Array<number>) { val b: &Array<number | string> = a }`,
		},
		{
			name: "ArrayWidensIntoAParameter",
			src: `fn f(xs: &Array<number | string>) -> number { return 0 }
				fn g(a: &Array<number>) -> number { return f(a) }`,
		},
		{
			name: "ArrayOfAnUnrelatedElementIsRejectedOnce",
			src: `fn f(a: &Array<string>) { val b: &Array<number> = a }`,
			want: []string{"cannot constrain string <: number"},
		},
		{
			name: "MutArrayDoesNotWiden",
			src: `fn f(a: &mut Array<number>) { val b: &mut Array<number | string> = a }`,
			want: []string{"cannot constrain string <: number"},
		},
		{
			name: "SetWidens",
			src: `import "std:set"
				fn f(a: &set.Set<number>) { val b: &set.Set<number | string> = a }`,
		},
		{
			name: "MutSetDoesNotWiden",
			src: `import "std:set"
				fn f(a: &mut set.Set<number>) { val b: &mut set.Set<number | string> = a }`,
			want: []string{"cannot constrain string <: number"},
		},
		{
			name: "MapWidensInBothParameters",
			src: `import "std:map"
				fn f(a: &map.Map<"k", number>) { val b: &map.Map<string, number | string> = a }`,
		},
		{
			name: "MutMapDoesNotWiden",
			src: `import "std:map"
				fn f(a: &mut map.Map<string, number>) { val b: &mut map.Map<string, number | string> = a }`,
			want: []string{"cannot constrain string <: number"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res := InferModuleAgainstStdlib(parseModule(t, tt.src), committedTree)
			if len(tt.want) == 0 {
				require.Empty(t, errorMessagesOf(res.Errors))
				return
			}
			require.Equal(t, tt.want, errorMessagesOf(res.Errors))
		})
	}
}

// An iteration callback receives the collection as `&Self`, an immutable borrow of the
// receiver. It can read the collection but neither change it nor keep it, and a widened
// view of the collection cannot reach a write through it.
func TestIterationCallbacksBorrowTheReceiver(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			// The checker rejects this callback. A caller may pass a `mut Array<number>` to
			// `widen`, because an immutable `Array` is covariant and `Array<number>` reads
			// as `Array<number | string>`. If the callback could take `arr` as mutable, its
			// `push("s")` would put a string into the caller's array of numbers. `forEach`
			// hands the callback `&Self`, an immutable borrow of the receiver, and an
			// immutable `Array` does not fit the callback's `mut` annotation.
			name: "ArrayCallbackCannotWriteThroughAWidenedView",
			src: `fn widen(xs: &Array<number | string>) {
				xs.forEach(fn (v, i, arr: mut Array<number | string>) {
					arr.push("s")
					return 0
				})
			}`,
			want: []string{"cannot constrain immutable Array<number | string> <: mutable Array<number | string>"},
		},
		{
			name: "SetCallbackCannotMutateTheSet",
			src: `import "std:set"
				fn f(xs: &set.Set<number>) {
					xs.forEach(fn (v, v2, s: mut set.Set<number>) {
						s.add(1)
						return 0
					})
				}`,
			want: []string{"cannot constrain immutable Set<number> <: mutable Set<number>"},
		},
		{
			// The callback claims it can keep the array, which a borrow does not allow.
			name: "CallbackAnnotatedWithTheOwnedArrayIsRejected",
			src: `fn f(xs: &Array<number>) {
				xs.forEach(fn (v: number, i: number, arr: Array<number>) { return 0 })
			}`,
			want: []string{"borrowed value Array<number> does not live long enough to satisfy owned Array<number>"},
		},
		{
			name: "CallbackAnnotatedWithABorrowReadsTheArray",
			src: `fn f(xs: &Array<number>) {
				xs.forEach(fn (v: number, i: number, arr: &Array<number>) { return arr.length })
			}`,
		},
		{
			name: "ArrayCallbackReadsTheArray",
			src: `fn f(xs: &Array<number>) -> number {
				var n = 0
				xs.forEach(fn (v, i, arr) {
					n = arr.length
					return 0
				})
				return n
			}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res := InferModuleAgainstStdlib(parseModule(t, tt.src), committedTree)
			if len(tt.want) == 0 {
				require.Empty(t, errorMessagesOf(res.Errors))
				return
			}
			require.Equal(t, tt.want, errorMessagesOf(res.Errors))
		})
	}
}

// A wrong element is rejected against the declaration's own signature, with the
// message that signature produces.
func TestArrayMethodRejectsAWrongElement(t *testing.T) {
	t.Parallel()

	_, _, errs := inferSource(t, `fn f(xs: mut Array<number>) { return xs.push("a") }`)
	require.Equal(t, []string{`cannot constrain "a" <: number`}, errorMessagesOf(errs))
}

// A rest parameter still checks each trailing argument against the element, the
// behavior #677 gave the retired concrete.
func TestArrayRestParamSurvivedTheRetirement(t *testing.T) {
	t.Parallel()

	_, _, errs := inferSource(t, `
		val g: fn (...xs: Array<number>) -> number = fn (...) { return 1 }
		val r = g(1, "a")
	`)
	require.Equal(t, []string{`cannot constrain "a" <: number`}, errorMessagesOf(errs))
}

// The handle refuses to load inside a speculation trial, since a discard truncates
// every bound the load journaled. A written `Array` settles the name before the
// reference resolves, so an annotation reached only through a trial still resolves —
// here the sole `Array` in the module sits in a lambda an overload call speculates on.
func TestArrayResolvesFromInsideAnOverloadTrial(t *testing.T) {
	t.Parallel()

	c := newTestChecker()
	module := parseModuleFiles(t, map[string]string{
		"input.esc": `
			declare fn pick(f: fn (xs: Array<number>) -> number) -> number
			declare fn pick(f: fn (s: string) -> number) -> number
			val r = pick(fn (xs: Array<number>) { return xs.length })
		`,
	})
	c.inferDepGraph(c.preludeScope().Child(), 0, module, dep_graph.BuildDepGraph(module))

	require.Empty(t, errorMessagesOf(c.errs))
	require.NotEmpty(t, c.ctx.arrayClass)
}

// arrayElem answers against the class name the run settled on, so it declines
// when there is nothing to compare against. It also declines an instance of that
// class carrying the wrong number of arguments, which is not an array whatever
// its name says.
func TestArrayHelpersDeclineWhatIsNotAnArray(t *testing.T) {
	t.Parallel()

	t.Run("NoArrayResolved", func(t *testing.T) {
		t.Parallel()
		c := &Context{}
		_, ok := c.arrayElem(&soltype.ClassType{Name: "Array", TypeArgs: []soltype.Type{num()}})
		require.False(t, ok)
	})
	t.Run("AnotherClassOfTheSameShape", func(t *testing.T) {
		t.Parallel()
		c := &Context{arrayClass: "Array"}
		_, ok := c.arrayElem(&soltype.ClassType{Name: "List", TypeArgs: []soltype.Type{num()}})
		require.False(t, ok)
	})
	t.Run("WrongArgumentCount", func(t *testing.T) {
		t.Parallel()
		c := &Context{arrayClass: "Array"}
		_, ok := c.arrayElem(&soltype.ClassType{Name: "Array"})
		require.False(t, ok)
		_, ok = c.arrayElem(&soltype.ClassType{
			Name: "Array", TypeArgs: []soltype.Type{num(), num()},
		})
		require.False(t, ok)
	})
}

// A tree supplying no `Array` is reported as a broken standard library, and the written
// reference is then an ordinary unbound name. Nothing claims an arity for a declaration
// nothing provides, and no handle is cached.
func TestArrayIsReportedWithoutAStdlib(t *testing.T) {
	t.Parallel()

	c := newChecker()
	module := parseModuleFiles(t, map[string]string{
		"input.esc": `fn f(xs: Array<number>) -> number { return 1 }`,
	})
	c.inferDepGraph(c.preludeScope().Child(), 0, module, dep_graph.BuildDepGraph(module))

	require.Equal(t, []string{
		"the standard library declares no class `Array`, which the checker needs",
		"the standard library declares no class `Promise`, which the checker needs",
		"cannot find type `Array`",
	}, errorMessagesOf(c.errs))
	require.Empty(t, c.ctx.arrayClass)
}

// A tuple fills `Array<E>` when every element it carries fits E. A `...P` spread contributes
// P's elements, and an inexact tuple may carry any trailing element. A `mut` tuple never fills
// a `mut Array<E>`, since a push through the array changes a length the tuple fixes. A fresh
// literal still reaches a `mut Array<E>` destination, because nothing else holds it.
func TestTupleIntoArray(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "LiteralIntoAnnotation",
			src:  `val x: Array<number> = [1, 2]`,
		},
		{
			name: "LiteralIntoParam",
			src: `fn f(xs: &Array<number>) -> number { return 0 }
				val y = f([1, 2])`,
		},
		{
			name: "EmptyLiteral",
			src:  `val x: Array<number> = []`,
		},
		{
			name: "WrongElement",
			src:  `val x: Array<string> = [1, 2]`,
			want: []string{
				"cannot constrain 1 <: string",
				"cannot constrain 2 <: string",
			},
		},
		{
			name: "SpreadElementFits",
			src: `fn h(xs: &Array<number>) -> number { return 0 }
				fn f(t: &[number, ...Array<number>]) { return h(t) }`,
		},
		{
			name: "LeadingElementBeforeSpreadDoesNotFit",
			src: `fn h(xs: &Array<number>) -> number { return 0 }
				fn f(t: &[string, ...Array<number>]) { return h(t) }`,
			want: []string{"cannot constrain string <: number"},
		},
		{
			name: "InexactTuple",
			src: `fn h(xs: &Array<number>) -> number { return 0 }
				fn f(t: &[number, ...]) { return h(t) }`,
			want: []string{"cannot constrain unknown <: number"},
		},
		{
			name: "MutTupleThroughAnImmutableView",
			src: `fn h(xs: &Array<number>) -> number { return 0 }
				fn f(t: &mut [number, number]) { return h(t) }`,
		},
		{
			name: "LiteralIntoMutAnnotationThenPush",
			src: `fn g() {
					val m: mut Array<number> = [1, 2]
					m.push(3)
				}`,
		},
		{
			name: "LiteralIntoMutParam",
			src: `fn h(xs: mut Array<number>) -> number { return 0 }
				val r = h([1, 2])`,
		},
		{
			name: "MutTupleIntoMutArrayOfItsElement",
			src: `fn h(xs: &mut Array<number>) -> number { return 0 }
				fn f(t: &mut [number, number]) { return h(t) }`,
			want: []string{"cannot constrain tuple <: Array<number>"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, _, errs := inferSource(t, tt.src)
			if len(tt.want) == 0 {
				require.Empty(t, errorMessagesOf(errs))
				return
			}
			require.Equal(t, tt.want, errorMessagesOf(errs))
		})
	}
}

// `Promise.race` and `Promise.any` bound their parameter by `[] | Array<unknown>`, so an array
// literal of promises reaches them through the tuple-into-`Array` rule. The cases load the real
// standard library, since the test prelude declares neither method.
func TestPromiseCombinatorsAcceptAnArrayLiteral(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
	}{
		{
			name: "Race",
			src:  `val p = Promise.race([Promise.resolve(1), Promise.resolve(2)])`,
		},
		{
			name: "Any",
			src:  `val p = Promise.any([Promise.resolve(1), Promise.resolve("a")])`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res := InferModuleAgainstStdlib(parseModule(t, tt.src), committedTree)
			require.Empty(t, errorMessagesOf(res.Errors))
		})
	}
}
