package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// preludeOnly is a tree whose only package is the prelude, declaring a class and
// a function so both sorts of export are covered.
var preludeOnly = map[string]string{
	"std/prelude.esc": `
		export class Widget {
			size: number,
		}
		export fn widgetize() -> Widget {
			return Widget(1)
		}
	`,
}

// The prelude package's exports are in scope with nothing imported, in both the
// value and the type sort.
func TestPreludeExportsResolveWithoutAnImport(t *testing.T) {
	t.Parallel()

	res := inferAgainstStdlib(t, `
		val w = widgetize()
		val n: number = w.size
		val direct: Widget = Widget(2)
	`, preludeOnly)

	require.Empty(t, errorMessagesOf(res.Errors))
	require.Equal(t, "Widget", soltype.Print(inferredValueType(t, res.Scope, "w")))
	require.Equal(t, "Widget", soltype.Print(inferredValueType(t, res.Scope, "direct")))
}

// A loaded package sees the prelude too, so a sibling `.esc` file under the
// stdlib tree names a prelude type without importing it. This is what lets the
// declarations left behind in `std:array` go on referring to `Array`.
func TestPreludeReachesALoadedPackage(t *testing.T) {
	t.Parallel()

	res := inferAgainstStdlib(t, `
		import "std:boxes"
		val w = boxes.unwrap()
	`, map[string]string{
		"std/prelude.esc": `
			export class Widget {
				size: number,
			}
		`,
		// No import of std:prelude, and `Widget` still resolves.
		"std/boxes.esc": `
			export fn unwrap() -> Widget {
				return Widget(1)
			}
		`,
	})

	require.Empty(t, errorMessagesOf(res.Errors))
	require.Equal(t, "Widget", soltype.Print(inferredValueType(t, res.Scope, "w")))
}

// A module's own declaration outranks the prelude export of the same name. The
// prelude sits above the module scope, so the nearer binding wins and a user's
// `class Array` is the one their code sees.
func TestModuleDeclarationOutranksThePrelude(t *testing.T) {
	t.Parallel()

	res := inferAgainstStdlib(t, `
		class Widget {
			label: string,
		}
		val w = Widget("hi")
		val s: string = w.label
	`, preludeOnly)

	require.Empty(t, errorMessagesOf(res.Errors))
	require.Equal(t, "Widget", soltype.Print(inferredValueType(t, res.Scope, "w")))
	// The module's `Widget` has a label and the prelude's has a size, so reading
	// the label is what says which one the reference resolved to.
	require.Equal(t, "string", soltype.Print(inferredValueType(t, res.Scope, "s")))
}

// A file's import outranks the prelude export of the same name. An import binds
// into the file scope, which is a child of the module scope, so it is nearer
// still.
func TestFileImportOutranksThePrelude(t *testing.T) {
	t.Parallel()

	res := inferAgainstStdlib(t, `
		import "std:other"
		val w = other.Widget("hi")
		val s: string = w.label
	`, map[string]string{
		"std/prelude.esc": `
			export class Widget {
				size: number,
			}
		`,
		"std/other.esc": `
			export class Widget {
				label: string,
			}
		`,
	})

	require.Empty(t, errorMessagesOf(res.Errors))
	require.Equal(t, "string", soltype.Print(inferredValueType(t, res.Scope, "s")))
}

// The prelude package does not inject into itself. Its own declarations resolve
// against the operator table alone, so a name it neither declares nor imports is
// unresolved rather than reaching back through the layer being built.
func TestPreludeDoesNotInjectIntoItself(t *testing.T) {
	t.Parallel()

	res := inferAgainstStdlib(t, `val n: number = 1`, map[string]string{
		"std/prelude.esc": `
			export fn make() -> Absent {
				return 1
			}
		`,
	})

	require.Len(t, res.Errors, 1)
	pkgErr, isPkgErr := res.Errors[0].(*PackageInferenceError)
	require.True(t, isPkgErr, "unexpected diagnostic: %s", res.Errors[0].Message())
	require.Equal(t, preludeURI, pkgErr.URI)
	require.Equal(t, []string{"cannot find type `Absent`"}, pkgErr.Messages)
}

// A prelude that fails to load leaves the run to report only what the program
// itself got wrong. The solver's own tests infer against no stdlib, so a report
// about the missing package would land on every one of them.
func TestAMissingPreludeIsSilent(t *testing.T) {
	t.Parallel()

	res := inferAgainstStdlib(t, `val n: number = 1`, map[string]string{
		"std/math.esc": `export val PI: number = 3`,
	})

	require.Empty(t, errorMessagesOf(res.Errors))
}
