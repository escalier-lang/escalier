package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// TestResolveTypeAnnForTest checks the exported helper over one annotation of
// each shape a consumer reaches for: a primitive, a form that needs a declaration
// the caller supplies, and one that needs the prelude.
func TestResolveTypeAnnForTest(t *testing.T) {
	tests := map[string]struct {
		decls    string
		ann      string
		expected string
	}{
		"Primitive":    {"", "number", "number"},
		"Composite":    {"", "{x: number, y?: string}", "{x: number, y?: string}"},
		"Signature":    {"", "fn <T>(x: T) -> T", "fn <T>(x: T) -> T"},
		"CallerAlias":  {"type Box<T> = {value: T}", "Box<number>", "Box<number>"},
		"CallerClass":  {"class Point { x: number }", "Point", "Point"},
		"PreludeClass": {"", "Array<number>", "Array<number>"},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			ty, diagnostics, err := ResolveTypeAnnForTest(test.decls, test.ann)
			require.NoError(t, err)
			require.Empty(t, diagnostics)
			require.Equal(t, test.expected, soltype.Print(ty))
		})
	}
}

// TestResolveTypeAnnForTestPreludeName pins the name the prelude registers each
// class under. It leads with the `import:<uri>.` package-key prefix, which a
// consumer rendering the name has to account for: internal/codegen strips it,
// since TypeScript cannot write one.
func TestResolveTypeAnnForTestPreludeName(t *testing.T) {
	ty, diagnostics, err := ResolveTypeAnnForTest("", "Promise<number, string>")
	require.NoError(t, err)
	require.Empty(t, diagnostics)

	class, isClass := ty.(*soltype.ClassType)
	require.True(t, isClass, "Promise resolves to a class, got %T", ty)
	require.Equal(t, "import:std:prelude.Promise", class.Name)
}

// TestResolveTypeAnnForTestReportsDiagnostics checks that a caller exercising a
// recovery path reads the run's diagnostics rather than an error. The error is for
// a fault in the test itself.
func TestResolveTypeAnnForTestReportsDiagnostics(t *testing.T) {
	_, diagnostics, err := ResolveTypeAnnForTest("", "NoSuchType")
	require.NoError(t, err)
	require.NotEmpty(t, diagnostics)
}

// TestResolveTypeAnnForTestRejectsUnparsableSource checks the other half of that
// split. Source the parser rejects is an error, not a diagnostic, and the message
// says which of the two inputs failed so a typo in one does not point at the other.
func TestResolveTypeAnnForTestRejectsUnparsableSource(t *testing.T) {
	_, _, err := ResolveTypeAnnForTest("", "{{{")
	require.ErrorContains(t, err, `parsing the annotation "{{{"`)

	_, _, err = ResolveTypeAnnForTest("type = = =", "number")
	require.ErrorContains(t, err, "parsing the declarations")
}

// TestResolveTypeAnnForTestRejectsBindingNameCollision checks the guard on the
// name the helper hangs the annotation off. It starts with an underscore, which a
// declaration may also use, and a collision would silently resolve the caller's
// own binding instead of the annotation.
func TestResolveTypeAnnForTestRejectsBindingNameCollision(t *testing.T) {
	_, _, err := ResolveTypeAnnForTest("declare val "+annBindingName+": string", "number")
	require.ErrorContains(t, err, "rename it")
}
