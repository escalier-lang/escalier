package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// An interface binds a type an annotation can name. It is structural, so a
// matching object literal type-checks against it.
func TestInterfaceBindsAStructuralType(t *testing.T) {
	values, types, errs := inferSource(t, `
		declare interface Point {
			x: number,
		}
		val p: Point = {x: 1}
	`)
	require.Empty(t, errorMessagesOf(errs))
	require.Equal(t, "{x: number}", types["Point"])
	require.Equal(t, "Point", values["p"])
}

// Two interfaces of one name contribute to one type.
func TestInterfaceDeclarationsMerge(t *testing.T) {
	_, types, errs := inferSource(t, `
		declare interface Point {
			x: number,
		}
		declare interface Point {
			y: number,
		}
	`)
	require.Empty(t, errorMessagesOf(errs))
	require.Equal(t, "{x: number, y: number}", types["Point"])
}

// An `extends` clause prepends the referenced interface's members, so the bound
// type is the flattened surface.
func TestInterfaceExtendsCarriesMembers(t *testing.T) {
	_, types, errs := inferSource(t, `
		declare interface Rect {
			w: number,
		}
		declare interface Sq extends Rect {
			s: number,
		}
	`)
	require.Empty(t, errorMessagesOf(errs))
	require.Equal(t, "{w: number, s: number}", types["Sq"])
}

// An exported interface reaches a package's surface, so an importer can name it
// in an annotation.
func TestExportedInterfaceReachesTheSurface(t *testing.T) {
	res := InferModuleWithSource(
		parseModule(t, `import "shapes"`),
		sourceOf(t, map[string]string{
			"shapes": `
				export declare interface Point {
					x: number,
				}
			`,
		}),
	)
	require.Empty(t, errorMessagesOf(res.Errors))

	ns, ok := res.Packages.Lookup("shapes")
	require.True(t, ok)
	require.Contains(t, ns.Types, "Point")
}
