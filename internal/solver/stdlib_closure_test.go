package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// The closure holds every package a root reaches, and nothing else. A program
// importing one package reads that package and what it names, so a tree's other
// packages are neither read nor inferred.
func TestTheClosureHoldsWhatTheRootsReach(t *testing.T) {
	t.Parallel()

	dir := seedStdlib(t, map[string]string{
		"std/alpha.esc": `export declare val a: number`,
		"std/beta.esc": `
			import "std:alpha"
			export declare val b: number
		`,
		"std/solo.esc": `export declare val c: number`,
	})

	closure, err := BuildPackageClosure(dir, []string{"std:beta"})
	require.NoError(t, err)

	want := []string{"std:alpha", "std:beta"}
	require.Equal(t, want, closure["std:beta"])
	require.Equal(t, want, closure["std:alpha"], "every member maps to the whole closure")
	require.NotContains(t, closure, "std:solo", "no root reaches std:solo")
}

// A cycle is followed like any other edge. Nothing about it needs refusing: the
// members load as one module, and the dep graph orders their declarations.
func TestTheClosureFollowsACycle(t *testing.T) {
	t.Parallel()

	dir := seedStdlib(t, map[string]string{
		"std/alpha.esc": `
			import "std:beta"
			export declare val a: number
		`,
		"std/beta.esc": `
			import "std:alpha"
			export declare val b: number
		`,
	})

	closure, err := BuildPackageClosure(dir, []string{"std:alpha"})
	require.NoError(t, err)

	want := []string{"std:alpha", "std:beta"}
	require.Equal(t, want, closure["std:alpha"])
	require.Equal(t, want, closure["std:beta"])
}

// The closure crosses schemes, since an import does.
func TestTheClosureCrossesSchemes(t *testing.T) {
	t.Parallel()

	dir := seedStdlib(t, map[string]string{
		"std/alpha.esc": `export declare val a: number`,
		"web/beta.esc": `
			import "std:alpha"
			export declare val b: number
		`,
	})

	closure, err := BuildPackageClosure(dir, []string{"web:beta"})
	require.NoError(t, err)
	require.Equal(t, []string{"std:alpha", "web:beta"}, closure["web:beta"])
}

// A root the tree does not hold contributes nothing. The load reports that URI
// against the import that named it, which is where a reader can act on it.
func TestTheClosureIgnoresAnUnknownRoot(t *testing.T) {
	t.Parallel()

	dir := seedStdlib(t, map[string]string{
		"std/alpha.esc": `export declare val a: number`,
	})

	closure, err := BuildPackageClosure(dir, []string{"std:alpha", "std:nonesuch"})
	require.NoError(t, err)
	require.Equal(t, []string{"std:alpha"}, closure["std:alpha"])
	require.NotContains(t, closure, "std:nonesuch")
}

// No root reaches anything, so nothing loads as a group.
func TestTheClosureIsEmptyWithoutRoots(t *testing.T) {
	t.Parallel()

	dir := seedStdlib(t, map[string]string{
		"std/alpha.esc": `export declare val a: number`,
	})

	closure, err := BuildPackageClosure(dir, nil)
	require.NoError(t, err)
	require.Empty(t, closure)
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

// A group that reports a diagnostic still binds its surface. Returning nothing
// would turn one error inside the cycle into an unbound-name error on every
// reference in the importing file.
func TestAReportingGroupStillBinds(t *testing.T) {
	t.Parallel()

	res := inferAgainstCyclicStdlib(t, `
		import "std:alpha"
		declare val a: alpha.Alpha
		val ok = a.fine
	`, map[string]string{
		"std/alpha.esc": `
			import "std:beta"
			export declare class Alpha {
				fine: number,
				broken: Nonexistent,
				partner: beta.Beta,
			}
		`,
		"std/beta.esc": `
			import "std:alpha"
			export declare class Beta { partner: alpha.Alpha }
		`,
	})

	messages := errorMessagesOf(res.Errors)
	require.Len(t, messages, 1, "only the group's own diagnostic, with no cascade: %v", messages)
	require.Contains(t, messages[0], "cannot find type `Nonexistent`")
	// The surface bound anyway, so the reference resolves.
	require.Equal(t, "number", soltype.Print(inferredValueType(t, res.Scope, "ok")))
}

// An `as` clause on an import naming a sibling in the same cycle is refused. A
// member is reached by its own name inside the merged module, so an alias would
// resolve nothing; saying so beats binding nothing.
func TestAnAliasedIntraGroupImportIsRefused(t *testing.T) {
	t.Parallel()

	res := inferAgainstCyclicStdlib(t, `
		import "std:alpha"
		declare val a: alpha.Alpha
		val partner = a.partner
	`, map[string]string{
		"std/alpha.esc": `
			import "std:beta" as b
			export declare class Alpha { partner: b.Beta }
		`,
		"std/beta.esc": `
			import "std:alpha"
			export declare class Beta { partner: alpha.Alpha }
		`,
	})

	messages := errorMessagesOf(res.Errors)
	require.NotEmpty(t, messages)
	require.Contains(t, messages[0],
		`cannot import "std:beta" as "b": the two packages import each other and load as `+
			`one module, where a sibling is reached by its own name; write the bare import`)
}

// Two members of one group binding the same name are refused. A member reaches
// a sibling by that name, so the two could not be told apart.
func TestAGroupWithCollidingBindingsIsRefused(t *testing.T) {
	t.Parallel()

	res := inferAgainstCyclicStdlib(t, `
		import "std:url"
		val x = 1
	`, map[string]string{
		"std/url.esc": "import \"web:url\"\nexport declare val a: number",
		"web/url.esc": "import \"std:url\"\nexport declare val b: number",
	})

	// The pair also spans tiers, since `std:*` is the language tier and `web:url`
	// is portable, so both diagnostics are correct and both are reported.
	require.Contains(t, errorMessagesOf(res.Errors),
		`std:url and web:url import each other and both bind "url"; a member of a cycle `+
			`reaches a sibling by that name, so the two cannot be told apart`)
}

// A cycle whose members sit in different tiers loads like any other. Refusing
// one was a property of the tier table, and which runtimes carry a declaration
// is now a question about that declaration rather than about an import edge.
func TestACycleAcrossTiersLoads(t *testing.T) {
	t.Parallel()

	res := inferAgainstCyclicStdlib(t, `
		import "web:fetch"
		declare val a: fetch.Alpha
		val partner = a.partner
	`, map[string]string{
		"web/fetch.esc": `
			import "web:dom"
			export declare class Alpha {
				partner: dom.Beta,
			}
		`,
		"web/dom.esc": `
			import "web:fetch"
			export declare class Beta {
				partner: fetch.Alpha,
			}
		`,
	})

	require.Empty(t, errorMessagesOf(res.Errors))
	require.Equal(t, "Beta", soltype.Print(inferredValueType(t, res.Scope, "partner")))
}

// The committed tree's cycles load. `web:dom` sits in an eleven-package
// component, and every member coming back with a surface is what says the
// closure resolved the references between them.
func TestTheCommittedTreeLoadsItsCycles(t *testing.T) {
	t.Parallel()

	closure, err := BuildPackageClosure(committedTree, []string{"web:dom"})
	require.NoError(t, err)
	members := closure["web:dom"]
	require.Greater(t, len(members), 1, "web:dom reaches no other package")

	res := InferModuleAgainstStdlib(parseModule(t, `
		import "web:dom"
		val x = 1
	`), committedTree)

	for _, uri := range members {
		ns, found := res.Packages.Lookup(uri)
		require.True(t, found, "%s is not registered", uri)
		require.NotNil(t, ns, "%s registered with no surface", uri)
	}
	for _, e := range res.Errors {
		_, isCycle := e.(*ImportCycleError)
		require.False(t, isCycle, "import cycle reported: %s", e.Message())
	}
}

// A closure holds what the roots reach and no more, which is what keeps a load
// proportional to the program rather than to the tree. `web:fetch` is portable
// and names nothing in the browser tier.
func TestTheCommittedTreeClosureIsBoundedByTheRoots(t *testing.T) {
	t.Parallel()

	closure, err := BuildPackageClosure(committedTree, []string{"web:fetch"})
	require.NoError(t, err)
	require.Contains(t, closure, "web:fetch")
	require.NotContains(t, closure, "web:dom", "web:fetch reaches the DOM")
}

// committedTree is the generated tree these tests read, relative to this
// package's directory.
const committedTree = "../interop/data"
