package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
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

// Two blocks of one name contribute to one namespace, the way two interfaces of
// one name contribute to one type.
func TestNamespaceBlocksOfOneNameMerge(t *testing.T) {
	values, _, errs := inferSource(t, `
		namespace geo {
			val a: number = 1
		}
		namespace geo {
			val b: number = 2
		}
		val x = geo.a
		val y = geo.b
	`)
	require.Empty(t, errorMessagesOf(errs))
	require.Equal(t, "number", values["x"])
	require.Equal(t, "number", values["y"])
}

// A block is bound under the name its enclosing directory namespace qualifies it
// with, so same-named blocks in sibling directories stay apart.
func TestNamespaceBlocksInSiblingDirectoriesStayDistinct(t *testing.T) {
	values, _, errs := inferSources(t, map[string]string{
		"geo/shapes.esc": `
			namespace util {
				val gv: number = 1
			}
		`,
		"math/ops.esc": `
			namespace util {
				val mv: string = "s"
			}
		`,
	})
	require.Empty(t, errorMessagesOf(errs))
	require.Equal(t, "number", values["geo.util.gv"])
	require.Equal(t, "string", values["math.util.mv"])
}

// An exported namespace block reaches a package's surface whole, so an importer
// reads its members through it.
func TestExportedNamespaceBlockReachesTheSurface(t *testing.T) {
	res := InferModuleWithSource(
		parseModule(t, `
			import "geo"
			val o = geo.shapes.origin
		`),
		sourceOf(t, map[string]string{
			"geo": `
				export namespace shapes {
					export val origin: number = 0
				}
			`,
		}),
	)
	require.Empty(t, errorMessagesOf(res.Errors))
	require.Equal(t, "number", soltype.Print(inferredValueType(t, res.Scope, "o")))
}
