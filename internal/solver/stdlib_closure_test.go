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

// An `as` clause on an import naming a sibling works like any other alias. Each
// member binds the sibling in its own file scope under the name it wrote, so
// nothing depends on the two agreeing.
func TestAnAliasedIntraClosureImportBinds(t *testing.T) {
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

	require.Empty(t, errorMessagesOf(res.Errors))
	require.Equal(t, "Beta", soltype.Print(inferredValueType(t, res.Scope, "partner")))
}

// Two packages whose URIs derive one name load together. A member's declarations
// land under a prefix carrying the scheme, so `std:url` and `web:url` are two
// namespaces rather than one ambiguous name, and a closure may hold both.
//
// The importing file aliases one of them, since two bare imports would both bind
// `url` in that file and the second would shadow the first. That is ordinary
// import shadowing and has nothing to do with the closure.
func TestTwoPackagesOfOneDerivedNameLoadTogether(t *testing.T) {
	t.Parallel()

	res := inferAgainstCyclicStdlib(t, `
		import "std:url"
		import "web:url" as weburl
		declare val a: url.Parsed
		declare val b: weburl.Parsed
		val href = a.href
		val origin = b.origin
	`, map[string]string{
		"std/url.esc": `export declare class Parsed { href: string }`,
		"web/url.esc": `export declare class Parsed { origin: string }`,
	})

	require.Empty(t, errorMessagesOf(res.Errors))
	require.Equal(t, "string", soltype.Print(inferredValueType(t, res.Scope, "href")))
	require.Equal(t, "string", soltype.Print(inferredValueType(t, res.Scope, "origin")))
}

// The same pair reached from inside the closure. Each member writes the bare
// name of what it imported, and the two resolve apart.
func TestTwoMembersOfOneDerivedNameResolveApart(t *testing.T) {
	t.Parallel()

	res := inferAgainstCyclicStdlib(t, `
		import "std:consumer"
		declare val c: consumer.Holder
		val href = c.fromStd.href
		val origin = c.fromWeb.origin
	`, map[string]string{
		"std/url.esc": `export declare class Parsed { href: string }`,
		"web/url.esc": `export declare class Parsed { origin: string }`,
		"std/consumer.esc": `
			import "std:url"
			import "web:url" as weburl
			export declare class Holder {
				fromStd: url.Parsed,
				fromWeb: weburl.Parsed,
			}
		`,
	})

	require.Empty(t, errorMessagesOf(res.Errors))
	require.Equal(t, "string", soltype.Print(inferredValueType(t, res.Scope, "href")))
	require.Equal(t, "string", soltype.Print(inferredValueType(t, res.Scope, "origin")))
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

// The committed tree's cycles load. `web:dom` reaches most of the `web:*` tree
// and several `std:*` packages, and every member coming back with a surface is
// what says the closure resolved the references between them.
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

// A declaration the committed tree routes to the package owning its API is
// reached under that package's prefix, by a program that imports it alone.
//
// Each of these sat in `web:dom` before #1605, so reaching one meant importing
// the whole DOM and writing `dom.Name`. The test is what says the move landed
// where a caller looks.
func TestTheCommittedTreeReachesEachAPIUnderItsOwnPackage(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		uri  string
		src  string
		want string
	}{
		"WebAudio": {
			uri:  "web:web_audio",
			src:  "declare val n: web_audio.ScriptProcessorNode\nval read = n.bufferSize",
			want: "number",
		},
		"WebRTC": {
			uri:  "web:web_rtc",
			src:  "declare val s: web_rtc.RTCDTMFSender\nval read = s.toneBuffer",
			want: "string",
		},
		"Payments": {
			uri:  "web:payments",
			src:  "declare val a: payments.PaymentAddress\nval read = a.city",
			want: "string",
		},
		"Credentials": {
			uri:  "web:credentials",
			src:  "declare val c: credentials.Credential\nval read = c.id",
			want: "string",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			src := "import \"" + test.uri + "\"\n" + test.src + "\n"
			res := InferModuleAgainstStdlib(parseModule(t, src), committedTree)
			// The committed tree reports known diagnostics of its own, so what
			// this asserts is the member's type rather than a clean run. Naming
			// the old prefix, as in `dom.Credential`, leaves `read` at `never`.
			require.Equal(t, test.want, soltype.Print(inferredValueType(t, res.Scope, "read")))
		})
	}
}

// committedTree is the generated tree these tests read, relative to this
// package's directory.
const committedTree = "../interop/data"
