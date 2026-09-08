package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// A type a namespace declares is named through that namespace in annotation
// position, the same way a value is in expression position.
func TestQualifiedTypeAnnotationResolvesThroughANamespace(t *testing.T) {
	t.Parallel()

	values, _, errs := inferSource(t, `
		namespace geo {
			type Num = number
		}
		val n: geo.Num = 3
	`)
	require.Empty(t, errorMessagesOf(errs))
	require.Equal(t, "Num", values["n"])
}

// A `namespace` block whose name a file also imports a package under is a
// collision, since `inner.Widget` would stand for two different namespaces.
// Neither is chosen; the source has to give one of them another name.
func TestNamespaceCollidingWithAnImportReports(t *testing.T) {
	t.Parallel()

	res := InferModuleWithSource(
		parseModule(t, `
			import "inner"
			namespace inner {
				type Widget = string
			}
		`),
		sourceOf(t, map[string]string{
			"inner": `
				export class Widget {
					fromInner: number,
				}
			`,
		}),
	)
	require.Equal(t, []string{
		"namespace inner has the name an import already binds; " +
			"rename the block or write `as` on the import",
	}, errorMessagesOf(res.Errors))
}

// Writing `as` on the import settles the collision, and each namespace is then
// reachable under its own name.
func TestAnAliasedImportClearsTheCollision(t *testing.T) {
	t.Parallel()

	res := InferModuleWithSource(
		parseModule(t, `
			import "inner" as pkg
			namespace inner {
				type Widget = string
			}
			val local: inner.Widget = "hi"
			val imported = pkg.Widget(1)
		`),
		sourceOf(t, map[string]string{
			"inner": `
				export class Widget {
					fromInner: number,
				}
			`,
		}),
	)
	require.Empty(t, errorMessagesOf(res.Errors))
	require.Equal(t, "Widget", soltype.Print(inferredValueType(t, res.Scope, "local")))
	require.Equal(t, "Widget", soltype.Print(inferredValueType(t, res.Scope, "imported")))
}

// A member the namespace does not declare is reported against the qualified name
// as written.
func TestQualifiedTypeAnnotationReportsAnUnknownMember(t *testing.T) {
	t.Parallel()

	_, _, errs := inferSource(t, `
		namespace geo {
			type Num = number
		}
		val n: geo.Nope = 3
	`)
	require.Equal(t, []string{"cannot find type `geo.Nope`"}, errorMessagesOf(errs))
}
