package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// A type a namespace declares is named through that namespace in annotation
// position, the same way a value is in expression position.
func TestQualifiedTypeAnnotationResolvesThroughANamespace(t *testing.T) {
	values, _, errs := inferSource(t, `
		namespace geo {
			type Num = number
		}
		val n: geo.Num = 3
	`)
	require.Empty(t, errorMessagesOf(errs))
	require.Equal(t, "Num", values["n"])
}

// A type an imported package exports is named through the import's binding.
func TestQualifiedTypeAnnotationResolvesThroughAnImport(t *testing.T) {
	res := InferModuleWithSource(
		parseModule(t, `
			import "geometry"
			val p: geometry.Point = geometry.Point(1, 2)
			val n: geometry.Num = 3
		`),
		sourceOf(t, map[string]string{
			"geometry": `
				export type Num = number
				export class Point {
					x: number,
					y: number,
				}
			`,
		}),
	)
	require.Empty(t, errorMessagesOf(res.Errors))
	require.Equal(t, "Point", soltype.Print(inferredValueType(t, res.Scope, "p")))
	require.Equal(t, "Num", soltype.Print(inferredValueType(t, res.Scope, "n")))
}

// A member the namespace does not declare is reported against the qualified name
// as written.
func TestQualifiedTypeAnnotationReportsAnUnknownMember(t *testing.T) {
	_, _, errs := inferSource(t, `
		namespace geo {
			type Num = number
		}
		val n: geo.Nope = 3
	`)
	require.Equal(t, []string{"cannot find type `geo.Nope`"}, errorMessagesOf(errs))
}
