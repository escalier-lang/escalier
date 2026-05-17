package tests

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPreludeOverridesCallableOnNonMutReceiver locks in that the
// mutabilityOverrides entries in checker/prelude.go are actually
// applied — i.e. the override map's class-name keys match the lib
// type aliases and the methods named in each entry exist on the
// corresponding interface. Without this coverage, a typo like
// `"chatAt"` (for `String.charAt`) or a missing entry for
// `Object.toString` silently dead-codes the override and the method
// becomes invisible on a non-mut receiver post-#612 polarity flip;
// "Callee is not callable" is the loud failure that this test catches.
func TestPreludeOverridesCallableOnNonMutReceiver(t *testing.T) {
	tests := map[string]string{
		"String.charAt on non-mut": `
			declare val s: string
			val c = s.charAt(0)
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
