package dts_to_esc

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// routingLib exercises every rule the type-only analysis applies, in
// one lib.dom.d.ts. `Request` routes to web:fetch and `ReadableStream`
// to web:streams by the §6.1 partition, so the referring declarations
// sit in packages other than the web:dom the rest falls through to.
const routingLib = `
interface Request { sole: SoleOpts; shared: SharedOpts; }
interface ReadableStream { shared: SharedOpts; }
type SoleOpts = string;
type SharedOpts = string;
type Orphan = string;
interface SelfRef { next: SelfRef; }
interface Trio { length: number; }
interface TrioConstructor { new (): Trio; }
declare var Trio: TrioConstructor;
`

// TestAnalyzeTypeOnlyRouting covers the four verdicts the analysis
// reaches over routingLib above.
//
//   - SoleOpts has one referrer outside web:dom, so it reads as
//     misplaced.
//   - SharedOpts has two, which makes it shared vocabulary that belongs
//     in web:dom.
//   - Orphan has none, and SelfRef's only reference is its own, which
//     does not speak for any package.
//   - Trio and TrioConstructor are the two halves of a class, so
//     `declare var Trio` keeps both out of the analysis entirely.
func TestAnalyzeTypeOnlyRouting(t *testing.T) {
	t.Parallel()
	res, err := PartitionLib([]LibInput{parseLib(t, "lib.dom.d.ts", routingLib)})
	require.NoError(t, err)

	routing := AnalyzeTypeOnlyRouting(res).forPackage(WebDOM.URI)

	require.Equal(t, []SoleReferrer{
		{Name: "SoleOpts", DeclaredIn: "web:dom", ReferencedBy: "web:fetch"},
	}, routing.SoleReferrer)
	require.Equal(t, []UnreferencedDecl{
		{Name: "Orphan", DeclaredIn: "web:dom"},
		{Name: "SelfRef", DeclaredIn: "web:dom"},
	}, routing.Unreferenced)
}

// TestReportTypeOnlyRouting_NamesNothing covers the quiet case: a lib
// whose every type-only declaration is either shared vocabulary or
// already in the package that references it writes no report at all.
func TestReportTypeOnlyRouting_NamesNothing(t *testing.T) {
	t.Parallel()
	res, err := PartitionLib([]LibInput{parseLib(t, "lib.dom.d.ts", `
interface Request { shared: SharedOpts; }
interface ReadableStream { shared: SharedOpts; }
type SharedOpts = string;
`)})
	require.NoError(t, err)

	var b strings.Builder
	require.NoError(t, ReportTypeOnlyRouting(res, &b))
	require.Empty(t, b.String())
}

// TestReportTypeOnlyRouting_PinnedLibSet is the §6.1 gate over the
// real input: every web:dom type-only declaration is referenced by
// web:dom itself or by two or more packages. An empty report is what
// passing looks like, so the test states the gate twice — once as the
// report, and once as the orphan set the report suppresses.
//
// The second assertion is what keeps the suppression honest. A name
// UnreferencedDOMTypes no longer covers fails here as a new orphan, and
// an entry the lib set stopped declaring fails as a stale one.
func TestReportTypeOnlyRouting_PinnedLibSet(t *testing.T) {
	t.Parallel()
	libDir := filepath.Join("..", "..", "node_modules", "typescript", "lib")
	if _, err := os.Stat(libDir); err != nil {
		t.Skipf("TypeScript lib dir not present at %s; run `pnpm install`: %v", libDir, err)
	}
	basenames, err := DiscoverLibFiles(libDir)
	require.NoError(t, err)
	inputs, err := ParseLibFiles(libDir, basenames)
	require.NoError(t, err)
	res, err := PartitionLib(inputs)
	require.NoError(t, err)

	var b strings.Builder
	require.NoError(t, ReportTypeOnlyRouting(res, &b))
	require.Empty(t, b.String())

	routing := AnalyzeTypeOnlyRouting(res).forPackage(WebDOM.URI)
	require.Empty(t, routing.SoleReferrer)
	orphans := make([]string, 0, len(routing.Unreferenced))
	for _, e := range routing.Unreferenced {
		orphans = append(orphans, e.Name)
	}
	acknowledged := UnreferencedDOMTypes.ToSlice()
	sort.Strings(acknowledged)
	require.Equal(t, acknowledged, orphans)
}

// TestAnalyzeTypeOnlyRouting_ReadsTemplateLiteralInterpolations pins
// the one place a reference hides from a walk that stops at the type
// annotation's surface. lib.dom.d.ts writes every reference to
// `AutoFillSection` inside an interpolation:
//
//	type AutoFill = AutoFillBase | `${OptionalPrefixToken<AutoFillSection>}…`
//
// A walk that skips template literals reports such a name as
// referenced by nothing, which reads as a drop candidate.
func TestAnalyzeTypeOnlyRouting_ReadsTemplateLiteralInterpolations(t *testing.T) {
	t.Parallel()
	res, err := PartitionLib([]LibInput{parseLib(t, "lib.dom.d.ts",
		"interface Request { section: Section; }\n"+
			"type Section = `section-${Kind}`;\n"+
			"type Kind = \"shipping\" | \"billing\";\n")})
	require.NoError(t, err)

	routing := AnalyzeTypeOnlyRouting(res).forPackage(WebDOM.URI)

	// Kind reaches web:dom only through Section's interpolation, so it
	// is shared vocabulary rather than an orphan.
	require.Empty(t, routing.Unreferenced)
	require.Equal(t, []SoleReferrer{
		{Name: "Section", DeclaredIn: "web:dom", ReferencedBy: "web:fetch"},
	}, routing.SoleReferrer)
}
