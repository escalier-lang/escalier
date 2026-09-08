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

// `Array<T>` is covariant in T and `mut Array<T>` is invariant. `at(self, index)` is an
// output position both views reach, and `push(mut self, item: T)` an input position only a
// mutable reference reaches, so the widening holds for a shared array while two mutable
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
	c.inferDepGraph(sharedPrelude().Child(), 0, module, dep_graph.BuildDepGraph(module))

	require.Empty(t, errorMessagesOf(c.errs))
	require.NotEmpty(t, c.ctx.arrayClass)
}

// arrayElem and arrayOf both answer against the class name the run settled on, so
// both decline when there is nothing to compare against. arrayElem also declines an
// instance of that class carrying the wrong number of arguments, which is not an
// array whatever its name says.
func TestArrayHelpersDeclineWhatIsNotAnArray(t *testing.T) {
	t.Parallel()

	t.Run("NoArrayResolved", func(t *testing.T) {
		t.Parallel()
		c := &Context{}
		_, ok := c.arrayOf(num())
		require.False(t, ok)
		_, ok = c.arrayElem(&soltype.ClassType{Name: "Array", TypeArgs: []soltype.Type{num()}})
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

// Without a stdlib supplying `Array`, the name is simply unknown. Nothing claims an
// arity for a declaration nothing provides, and no handle is cached.
func TestArrayIsUnknownWithoutAStdlib(t *testing.T) {
	t.Parallel()

	c := newChecker()
	module := parseModuleFiles(t, map[string]string{
		"input.esc": `fn f(xs: Array<number>) -> number { return 1 }`,
	})
	c.inferDepGraph(sharedPrelude().Child(), 0, module, dep_graph.BuildDepGraph(module))

	require.Equal(t, []string{"cannot find type `Array`"}, errorMessagesOf(c.errs))
	require.Empty(t, c.ctx.arrayClass)
}
