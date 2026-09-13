package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// A package importing nothing loads alone.
func TestPackageGroupsAnAcyclicPackageIsItsOwnGroup(t *testing.T) {
	t.Parallel()

	dir := seedStdlib(t, map[string]string{
		"std/alpha.esc": `export declare val a: number`,
		"std/beta.esc": `
			import "std:alpha"
			export declare val b: alpha.A
		`,
	})
	groups, err := BuildPackageGroups(dir)
	require.NoError(t, err)

	require.Equal(t, []string{"std:alpha"}, groups["std:alpha"])
	require.Equal(t, []string{"std:beta"}, groups["std:beta"])
}

// Two packages naming each other load together.
func TestPackageGroupsAPairCycleGroups(t *testing.T) {
	t.Parallel()

	dir := seedStdlib(t, map[string]string{
		"std/alpha.esc": `
			import "std:beta"
			export declare val a: beta.B
		`,
		"std/beta.esc": `
			import "std:alpha"
			export declare val b: alpha.A
		`,
	})
	groups, err := BuildPackageGroups(dir)
	require.NoError(t, err)

	want := []string{"std:alpha", "std:beta"}
	require.Equal(t, want, groups["std:alpha"])
	require.Equal(t, want, groups["std:beta"])
}

// A three-package cycle groups the same way, and a package importing into the
// cycle without being imported back stays out of it.
func TestPackageGroupsATripleCycleGroups(t *testing.T) {
	t.Parallel()

	dir := seedStdlib(t, map[string]string{
		"std/alpha.esc":   "import \"std:beta\"\nexport declare val a: number",
		"std/beta.esc":    "import \"std:gamma\"\nexport declare val b: number",
		"std/gamma.esc":   "import \"std:alpha\"\nexport declare val g: number",
		"std/outside.esc": "import \"std:alpha\"\nexport declare val o: number",
	})
	groups, err := BuildPackageGroups(dir)
	require.NoError(t, err)

	want := []string{"std:alpha", "std:beta", "std:gamma"}
	require.Equal(t, want, groups["std:alpha"])
	require.Equal(t, want, groups["std:beta"])
	require.Equal(t, want, groups["std:gamma"])
	require.Equal(t, []string{"std:outside"}, groups["std:outside"])
}

// A cycle running across schemes groups the same as one inside a scheme. The
// group is what loads together; the scheme only says where a file sits.
func TestPackageGroupsACrossSchemeCycleGroups(t *testing.T) {
	t.Parallel()

	dir := seedStdlib(t, map[string]string{
		"std/prelude.esc": "import \"web:core\"\nexport declare val p: number",
		"web/core.esc":    "import \"std:prelude\"\nexport declare val c: number",
	})
	groups, err := BuildPackageGroups(dir)
	require.NoError(t, err)

	require.Equal(t, []string{"std:prelude", "web:core"}, groups["web:core"])
}

// A cycle inside one tier is permitted. The browser tier is mutually recursive
// for real reasons, so refusing every cycle would refuse the shipped tree.
func TestCheckGroupTiersPermitsACycleInsideATier(t *testing.T) {
	t.Parallel()

	groups := PackageGroups{
		"web:dom":   {"web:dom", "web:webgl"},
		"web:webgl": {"web:dom", "web:webgl"},
	}
	require.Empty(t, CheckGroupTiers(groups, ast.Span{}))
}

// A cycle crossing a tier is refused, naming every member and its tier.
func TestCheckGroupTiersRefusesACycleAcrossTiers(t *testing.T) {
	t.Parallel()

	groups := PackageGroups{
		"web:dom":   {"web:dom", "web:fetch"},
		"web:fetch": {"web:dom", "web:fetch"},
	}
	errs := CheckGroupTiers(groups, ast.Span{})
	require.Equal(t, []string{
		"import cycle spans more than one runtime tier: web:dom (browser), web:fetch (portable); " +
			"a cycle may not cross a tier, since every member of one loads whenever any member does",
	}, errorMessagesOf(errs))
}

// One group reports once however many members name it.
func TestCheckGroupTiersReportsAGroupOnce(t *testing.T) {
	t.Parallel()

	group := []string{"web:core", "web:dom", "web:fetch"}
	groups := PackageGroups{"web:core": group, "web:dom": group, "web:fetch": group}
	require.Len(t, CheckGroupTiers(groups, ast.Span{}), 1)
}

// A package the partition does not hold has no tier, so a group containing one
// is left alone rather than refused. A hand-written test tree is full of them.
func TestCheckGroupTiersIgnoresAnUntieredPackage(t *testing.T) {
	t.Parallel()

	groups := PackageGroups{
		"std:alpha": {"std:alpha", "web:dom"},
		"web:dom":   {"std:alpha", "web:dom"},
	}
	require.Empty(t, CheckGroupTiers(groups, ast.Span{}))
}

// A single-member group is never a cross-tier cycle, whatever its tier.
func TestCheckGroupTiersIgnoresASingleton(t *testing.T) {
	t.Parallel()

	require.Empty(t, CheckGroupTiers(PackageGroups{"web:dom": {"web:dom"}}, ast.Span{}))
}

// inferAgainstCyclicStdlib infers src against a tree that may hold cycles, with
// the prelude classes the checker's own rules name merged in.
func inferAgainstCyclicStdlib(t *testing.T, src string, files map[string]string) *ModuleResult {
	t.Helper()
	return InferModuleAgainstStdlib(parseModule(t, src), seedStdlib(t, withPreludeClasses(files)))
}

// Two packages naming each other load once, and each resolves the other's
// types. This is what a group buys: loading either one alone would reach a name
// the other has not declared yet.
func TestACyclicPairLoadsAndResolvesBothWays(t *testing.T) {
	t.Parallel()

	files := map[string]string{
		"std/alpha.esc": `
			import "std:beta"
			export declare class Alpha {
				partner: beta.Beta,
			}
		`,
		"std/beta.esc": `
			import "std:alpha"
			export declare class Beta {
				partner: alpha.Alpha,
			}
		`,
	}

	t.Run("EachIsRegisteredUnderItsOwnURI", func(t *testing.T) {
		res := inferAgainstCyclicStdlib(t, `
			import "std:alpha"
			import "std:beta"
			declare val a: alpha.Alpha
			declare val b: beta.Beta
			val fromA = a.partner
			val fromB = b.partner
		`, files)

		require.Empty(t, errorMessagesOf(res.Errors))
		require.Equal(t, "Beta", soltype.Print(inferredValueType(t, res.Scope, "fromA")))
		require.Equal(t, "Alpha", soltype.Print(inferredValueType(t, res.Scope, "fromB")))
	})

	t.Run("ImportingOneLoadsTheGroup", func(t *testing.T) {
		res := inferAgainstCyclicStdlib(t, `
			import "std:alpha"
			declare val a: alpha.Alpha
			val partner = a.partner
		`, files)

		require.Empty(t, errorMessagesOf(res.Errors))
		require.Equal(t, "Beta", soltype.Print(inferredValueType(t, res.Scope, "partner")))
		// Both members are registered, whichever one the file imported.
		for _, uri := range []string{"std:alpha", "std:beta"} {
			ns, found := res.Packages.Lookup(uri)
			require.True(t, found, "%s is not registered", uri)
			require.NotNil(t, ns, "%s registered with no surface", uri)
		}
	})
}

// A three-package cycle behaves the same as a pair.
func TestACyclicTripleLoadsAndResolves(t *testing.T) {
	t.Parallel()

	res := inferAgainstCyclicStdlib(t, `
		import "std:alpha"
		declare val a: alpha.Alpha
		val next = a.next
	`, map[string]string{
		"std/alpha.esc": "import \"std:beta\"\nexport declare class Alpha { next: beta.Beta }",
		"std/beta.esc":  "import \"std:gamma\"\nexport declare class Beta { next: gamma.Gamma }",
		"std/gamma.esc": "import \"std:alpha\"\nexport declare class Gamma { next: alpha.Alpha }",
	})

	require.Empty(t, errorMessagesOf(res.Errors))
	require.Equal(t, "Beta", soltype.Print(inferredValueType(t, res.Scope, "next")))
}

// An acyclic package still takes the single-package path, which the group map
// leaves as a group of one.
func TestAnAcyclicPackageStillLoadsAlone(t *testing.T) {
	t.Parallel()

	res := inferAgainstCyclicStdlib(t, `
		import "std:math"
		val x = math.PI
	`, map[string]string{
		"std/math.esc": `export val PI: number = 3`,
	})

	require.Empty(t, errorMessagesOf(res.Errors))
	require.Equal(t, "number", soltype.Print(inferredValueType(t, res.Scope, "x")))
}

// A non-exported declaration in one member is unreachable from an importer,
// even though the group inferred as one module and the sibling could see it.
func TestAGroupPublishesOnlyExportedDeclarations(t *testing.T) {
	t.Parallel()

	files := map[string]string{
		"std/alpha.esc": `
			import "std:beta"
			export declare val shared: number
			declare val secret: number
			export declare class Alpha { partner: beta.Beta }
		`,
		"std/beta.esc": `
			import "std:alpha"
			export declare class Beta { partner: alpha.Alpha }
		`,
	}

	t.Run("AnExportedSiblingResolves", func(t *testing.T) {
		res := inferAgainstCyclicStdlib(t, `
			import "std:alpha"
			val n = alpha.shared
		`, files)
		require.Empty(t, errorMessagesOf(res.Errors))
		require.Equal(t, "number", soltype.Print(inferredValueType(t, res.Scope, "n")))
	})

	t.Run("AnUnexportedOneDoesNot", func(t *testing.T) {
		res := inferAgainstCyclicStdlib(t, `
			import "std:alpha"
			val n = alpha.secret
		`, files)
		require.NotEmpty(t, errorMessagesOf(res.Errors))
	})
}
