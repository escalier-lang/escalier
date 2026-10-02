package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAClassMemberKeyedOffAnUnknownSymbolIsReported asserts that a class member whose
// key names no well-known symbol is rejected.
//
// soltype stores a member keyed off a symbol under a reserved name taken from a closed
// set, so a key outside that set names a member it cannot represent. Dropping such a
// member silently is what let an extractor class type-check while losing the
// `[Symbol.customMatcher]` signature a match against it reads.
func TestAClassMemberKeyedOffAnUnknownSymbolIsReported(t *testing.T) {
	tests := map[string]string{
		"AStaticMethod": `
			class C {
				msg: string,
				static [Symbol.notAWellKnownSymbol](subject: C) -> [string] {
					return [subject.msg]
				}
			}
		`,
		"AnInstanceMethod": `
			class C {
				[Symbol.notAWellKnownSymbol](&self) -> number { return 1 },
			}
		`,
		"AField": `
			class C {
				[Symbol.notAWellKnownSymbol]: number,
			}
		`,
	}

	for name, src := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, src)
			require.Len(t, errs, 1, "one key names one mistake, so it is reported once")
			require.Equal(t, "Unsupported: ComputedKey", errs[0].Message())
		})
	}
}

// TestAClassMemberKeyedOffAWellKnownSymbolIsAccepted asserts that the closed set covers
// `customMatcher`, which is what a class declares to say what a match against it binds.
func TestAClassMemberKeyedOffAWellKnownSymbolIsAccepted(t *testing.T) {
	values, _, errs := inferSource(t, `
		class C {
			msg: string,
			static [Symbol.customMatcher](subject: C) -> [string] {
				return [subject.msg]
			}
		}
	`)
	require.Empty(t, errs)
	require.Contains(t, values, "C")
	require.Contains(t, values["C"], "[Symbol.customMatcher]")
}
