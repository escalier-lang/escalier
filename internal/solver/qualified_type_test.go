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

// A `namespace` block keeps its own name against an import of the same name. The
// block's members hold flat qualified keys, so they answer ahead of the namespace
// walk an import binding is reached through.
func TestLocalNamespaceBlockOutranksASameNamedImport(t *testing.T) {
	t.Parallel()

	res := InferModuleWithSource(
		parseModule(t, `
			import "inner"
			namespace inner {
				type Widget = string
			}
			val w: inner.Widget = "hi"
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
	require.Equal(t, "Widget", soltype.Print(inferredValueType(t, res.Scope, "w")))
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
