package tests

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPreludeNonMutSourcesCallableOnNonMutReceiver locks in that the two
// sources the prelude strips `mut self` from are actually applied: the
// ECMA-262 receiver facts, and the dts_to_esc.NonMutatingOverrides entries
// for the members no fact addresses. Both are keyed by owner and member
// name, and both keys have to match what the lib types declare — a fact
// keyed `String.prototype.charAt` reaches the method only if the prelude
// looks it up under the owner `String`. A `.d.ts` method carries `mut self`
// until one of the two strips it, so a mismatch silently dead-codes the claim
// and leaves the method invisible on a non-mut receiver. Each case below calls
// one on a non-mut value, where that shows up as "Callee is not callable".
func TestPreludeNonMutSourcesCallableOnNonMutReceiver(t *testing.T) {
	tests := map[string]string{
		"String.charAt on non-mut": `
			declare val s: string
			val c = s.charAt(0)
		`,
		// `trim` matches no prefix, so no name tier answers it and the
		// receiver comes from the fact alone.
		"String.trim on non-mut": `
			declare val s: string
			val t = s.trim()
		`,
		"Object.toString on non-mut": `
			declare val o: Object
			val s = o.toString()
		`,
		// stripIteratorReceiverPolarity pins these: [Symbol.iterator]
		// and [Symbol.asyncIterator] are non-mutating on the source, so
		// they must be visible on a non-mut receiver. Pre-fix, these
		// were only callable via the asMutReceiver wrap inside
		// iterable.go.
		"Symbol.iterator on non-mut Array": `
			declare val xs: Array<number>
			val iter = xs[Symbol.iterator]()
		`,
		"Symbol.iterator on non-mut string": `
			declare val s: string
			val iter = s[Symbol.iterator]()
		`,
	}

	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			_, errs := inferScript(t, input)
			msgs := make([]string, len(errs))
			for i, e := range errs {
				msgs[i] = e.Message()
			}
			require.Empty(t, errs, "expected no inference errors, got %v", msgs)
		})
	}
}
