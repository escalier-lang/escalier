package tests

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPreludeOverridesCallableOnNonMutReceiver locks in that the
// dts_to_esc.NonMutatingOverrides entries the prelude reads are actually
// applied. The override map's owner keys have to match the lib type
// aliases, and the methods named in each entry have to exist on the
// corresponding interface. A `.d.ts` method carries `mut self` until an
// entry or a name heuristic strips it, so a typo like `"chatAt"` for
// `String.charAt` silently dead-codes the override and leaves the method
// invisible on a non-mut receiver. Each case below calls one on a non-mut
// value, where that shows up as "Callee is not callable".
func TestPreludeOverridesCallableOnNonMutReceiver(t *testing.T) {
	tests := map[string]string{
		"String.charAt on non-mut": `
			declare val s: string
			val c = s.charAt(0)
		`,
		// `trim` matches no prefix, so no name tier answers it and the
		// entry is what carries the claim.
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
