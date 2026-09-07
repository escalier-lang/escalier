package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A namespace block groups its members under its name, and an access reaches them
// through it.
func TestNamespaceMembersResolveThroughTheBlock(t *testing.T) {
	values, _, errs := inferSource(t, `
		namespace geo {
			val origin: number = 0
			fn twice(n: number) -> number {
				return n
			}
		}
		val o = geo.origin
		val t = geo.twice(1)
	`)
	require.Empty(t, errorMessagesOf(errs))
	require.Equal(t, "number", values["o"])
	require.Equal(t, "number", values["t"])
}

// A nested block is reached through its parent, so the members of `a.b` are read
// as `a.b.member`.
func TestNestedNamespaceBlocksNest(t *testing.T) {
	values, _, errs := inferSource(t, `
		namespace a {
			namespace b {
				val deep: number = 1
			}
		}
		val d = a.b.deep
	`)
	require.Empty(t, errorMessagesOf(errs))
	require.Equal(t, "number", values["d"])
}

// A name the block does not declare is reported against the namespace rather than
// resolving to something outside it.
func TestNamespaceReportsAnUnknownMember(t *testing.T) {
	_, _, errs := inferSource(t, `
		namespace geo {
			val origin: number = 0
		}
		val missing = geo.nowhere
	`)
	require.Equal(t, []string{"Namespace geo has no member: nowhere"}, errorMessagesOf(errs))
}

// A type a namespace declares binds under the qualified name the block gives it.
func TestNamespaceBindsItsTypes(t *testing.T) {
	_, types, errs := inferSource(t, `
		namespace geo {
			type Num = number
		}
	`)
	require.Empty(t, errorMessagesOf(errs))
	require.Equal(t, "number", types["geo.Num"])
}
