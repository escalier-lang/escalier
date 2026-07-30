package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A phantom parameter's argument cannot appear in the type an instantiation denotes, so two
// instantiations that differ only in those arguments are one type and check against each other in
// both directions. Each case below names a different reason the argument never lands in that type.
func TestInferPhantomArgumentsCompareEqual(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{
			// The recursive reference hands `{b: T}` to the next unfolding, so the payload is always
			// one unfolding deeper than the structure emitted so far. `Deep<number>` and
			// `Deep<string>` are both `{a: {a: {a: …}}}`.
			name: "PayloadAlwaysOneUnfoldingAhead",
			src: `
				type Deep<T> = {a: Deep<{b: T}>}
				declare fn make() -> Deep<number>
				val d: Deep<string> = make()
			`,
		},
		{
			// The same pair the other way round, since the erasure is a property of the identity
			// rather than of one side of the constraint.
			name: "PayloadAlwaysOneUnfoldingAheadReversed",
			src: `
				type Deep<T> = {a: Deep<{b: T}>}
				declare fn make() -> Deep<string>
				val d: Deep<number> = make()
			`,
		},
		{
			// Mutual recursion. P's parameter reaches only Q's parameter and Q's reaches only P's, so
			// neither ever lands in the `{a: {b: {a: …}}}` the pair denotes. The fixed point has to
			// settle both together, since each is phantom only because the other is.
			name: "MutuallyRecursivePair",
			src: `
				type P<T> = {a: Q<T>}
				type Q<U> = {b: P<U>}
				declare fn make() -> P<number>
				val d: P<string> = make()
			`,
		},
		{
			// A parameter the body never mentions. `Ignore<T>` denotes `number` whatever T is, so no
			// recursion is involved at all.
			name: "ParameterUnusedInTheBody",
			src: `
				type Ignore<T> = number
				declare fn make() -> Ignore<number>
				val d: Ignore<string> = make()
			`,
		},
		{
			// The erasure reaches a nested reference, so a relevant parameter carrying a phantom
			// instantiation still compares equal. Hold's own T is relevant, since it lands at `held`.
			name: "PhantomInstantiationInsideARelevantArgument",
			src: `
				type Deep<T> = {a: Deep<{b: T}>}
				type Hold<T> = {held: T}
				declare fn make() -> Hold<Deep<number>>
				val d: Hold<Deep<string>> = make()
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

// A parameter that does reach the type an alias denotes stays relevant, so its argument stays in the
// canonical identity and a mismatch is still reported. `Nest<T>` writes T at `here`, inside the
// object every unfolding emits, which is what separates it from `Deep<T>` above.
func TestInferRelevantParameterKeepsItsArgument(t *testing.T) {
	_, _, errs := inferSource(t, `
		type Nest<T> = {here: T, deeper: Nest<{b: T}>}
		declare fn make() -> Nest<number>
		val d: Nest<string> = make()
	`)
	require.Contains(t, messagesWithSpan(errs), "4:25-4:31: cannot constrain number <: string")
}

// The erasure is confined to the canonical identity constrain keys on. A reference still carries the
// arguments the source wrote, so a binding renders under those and not under an erased form.
func TestInferPhantomArgumentSurvivesInTheRenderedType(t *testing.T) {
	values, _, errs := inferSource(t, `
		type Deep<T> = {a: Deep<{b: T}>}
		declare fn make() -> Deep<number>
		val d: Deep<string> = make()
		fn f(p: Deep<number>) -> Deep<number> { return p }
	`)
	require.Empty(t, messagesWithSpan(errs))
	require.Equal(t, "Deep<string>", values["d"])
	require.Equal(t, "fn (p: Deep<number>) -> Deep<number>", values["f"])
}
