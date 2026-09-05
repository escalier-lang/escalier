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

// pinnedRouting routes the pinned lib set through the committed
// overlay, which is the input generate reports on. The overlay's root
// drop file settles `eval` and the other whole-symbol drops before
// routing, so a run without it fails the fail-safe on the first
// dropped name.
func pinnedRouting(t *testing.T) *PartitionResult {
	t.Helper()
	libDir := filepath.Join("..", "..", "node_modules", "typescript", "lib")
	if _, err := os.Stat(libDir); err != nil {
		t.Skipf("TypeScript lib dir not present at %s; run `pnpm install`: %v", libDir, err)
	}
	basenames, err := DiscoverLibFiles(libDir)
	require.NoError(t, err)
	inputs, err := ParseLibFiles(libDir, basenames)
	require.NoError(t, err)
	overlay, err := LoadOverlay(filepath.Join("..", "interop", "overlay"))
	require.NoError(t, err)
	res, err := PartitionLibWithOverlay(inputs, overlay)
	require.NoError(t, err)
	return res
}

// TestReportTypeOnlyRouting_PinnedLibSet is the §6.1 gate over the real
// input: every web:dom type-only declaration is referenced by web:dom
// itself or by two or more packages. The report prints only what still
// needs a decision, so passing looks like an empty one.
//
// The second assertion states the misplacement half structurally. It
// still fails if the report's grouping ever swallows a finding.
func TestReportTypeOnlyRouting_PinnedLibSet(t *testing.T) {
	t.Parallel()
	res := pinnedRouting(t)

	var b strings.Builder
	require.NoError(t, ReportTypeOnlyRouting(res, &b))
	require.Empty(t, b.String())

	require.Empty(t, AnalyzeTypeOnlyRouting(res).forPackage(WebDOM.URI).SoleReferrer)
}

// TestUnreferencedDOMTypes_MatchesThePinnedLibSet keeps the gate above
// honest. That report hides whatever UnreferencedDOMTypes covers, so an
// empty report cannot on its own tell a clean tree from one whose new
// orphan the list happens to hide.
//
// Comparing the two sets catches both directions. A declaration the
// list does not cover is a new one needing a decision, and an entry
// that no longer appears is stale because TypeScript stopped declaring
// the name.
func TestUnreferencedDOMTypes_MatchesThePinnedLibSet(t *testing.T) {
	t.Parallel()
	routing := AnalyzeTypeOnlyRouting(pinnedRouting(t)).forPackage(WebDOM.URI)

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

	// Kind reaches web:dom only through Section's interpolation. Its
	// one referrer is the package that declares it, which is the
	// verdict a walk stopping at the template literal would miss.
	require.Empty(t, routing.Unreferenced)
	require.Equal(t, []SoleReferrer{
		{Name: "Section", DeclaredIn: "web:dom", ReferencedBy: "web:fetch"},
	}, routing.SoleReferrer)
}

// TestAnalyzeTypeOnlyRouting_ReadsTypeParameterBounds covers the other
// two slots a reference hides in. A type parameter's constraint and its
// default each name a type nothing else in the declaration mentions:
//
//	interface Request { pick<K extends Bag>(k: K): void }
//	interface ReadableStream<T = Deflt> { value: T }
//
// Skipping either slot reports Bag and Deflt as referenced by nothing.
func TestAnalyzeTypeOnlyRouting_ReadsTypeParameterBounds(t *testing.T) {
	t.Parallel()
	res, err := PartitionLib([]LibInput{parseLib(t, "lib.dom.d.ts", `
interface Request { pick<K extends Bag>(k: K): void; }
interface ReadableStream<T = Deflt> { value: T; }
interface Bag { size: number; }
interface Deflt { size: number; }
`)})
	require.NoError(t, err)

	routing := AnalyzeTypeOnlyRouting(res).forPackage(WebDOM.URI)

	require.Empty(t, routing.Unreferenced)
	require.Equal(t, []SoleReferrer{
		{Name: "Bag", DeclaredIn: "web:dom", ReferencedBy: "web:fetch"},
		{Name: "Deflt", DeclaredIn: "web:dom", ReferencedBy: "web:streams"},
	}, routing.SoleReferrer)
}

// TestReportTypeOnlyRouting_Renders pins the report's text. The other
// report tests assert an empty one, which says nothing about what a
// finding looks like when there is one.
//
// FnOpts also covers the type parameters a function type carries.
// BoundOpts is named only by `<T extends BoundOpts>`, so a walk that
// misses that slot puts it on the unreferenced line.
func TestReportTypeOnlyRouting_Renders(t *testing.T) {
	t.Parallel()
	res, err := PartitionLib([]LibInput{parseLib(t, "lib.dom.d.ts", `
interface Request { a: AlphaOpts; b: BetaOpts; f: FnOpts; }
type AlphaOpts = string;
type BetaOpts = string;
type FnOpts = <T extends BoundOpts>(x: T) => void;
type BoundOpts = string;
type Orphan = string;
`)})
	require.NoError(t, err)

	var b strings.Builder
	require.NoError(t, ReportTypeOnlyRouting(res, &b))

	require.Equal(t,
		"  web:dom: 3 type-only decls only web:fetch references"+
			" (AlphaOpts, BetaOpts, FnOpts)\n"+
			"  web:dom: 1 type-only decl nothing references (Orphan)\n",
		b.String())
}

// TestAnalyzeTypeOnlyRouting_ReadsEveryTypeShape covers the type
// annotation shapes the other tests leave out: a constructor type's
// type parameters, a tuple's rest element, and its optional element.
// A name reachable only through one of those reads as referenced by
// nothing if the walk skips that shape.
//
// QueuingStrategySize routes to web:streams by the §6.1 partition, so
// the sole referrers span two declaring packages and the analysis has
// to order them by that first.
func TestAnalyzeTypeOnlyRouting_ReadsEveryTypeShape(t *testing.T) {
	t.Parallel()
	res, err := PartitionLib([]LibInput{parseLib(t, "lib.dom.d.ts", `
interface Request { c: CtorOpts; t: TupOpts; q: QueuingStrategySize; }
type CtorOpts = new <T extends CtorBound>(x: T) => void;
type CtorBound = string;
type TupOpts = [...RestOpts[], OptOpts?];
type RestOpts = string;
type OptOpts = string;
interface QueuingStrategySize { size: number; }
`)})
	require.NoError(t, err)

	routing := AnalyzeTypeOnlyRouting(res)

	// CtorBound, RestOpts, and OptOpts each reach web:dom only through
	// the shape under test, so an empty list is what proves the walk
	// found them. The filter drops Request, which this lib declares
	// without a `declare var` and nothing references.
	require.Empty(t, routing.forPackage(WebDOM.URI).Unreferenced)
	require.Equal(t, []SoleReferrer{
		{Name: "CtorOpts", DeclaredIn: "web:dom", ReferencedBy: "web:fetch"},
		{Name: "TupOpts", DeclaredIn: "web:dom", ReferencedBy: "web:fetch"},
		{Name: "QueuingStrategySize", DeclaredIn: "web:streams", ReferencedBy: "web:fetch"},
	}, routing.SoleReferrer)
}

// TestAnalyzeTypeOnlyRouting_ReadsExtendsAndImplements covers a class's
// superclass and the interfaces it implements. Both name a type the
// members repeat only by accident, so a walk that reads members alone
// reports the two as referenced by nothing.
func TestAnalyzeTypeOnlyRouting_ReadsExtendsAndImplements(t *testing.T) {
	t.Parallel()
	res, err := PartitionLib([]LibInput{parseLib(t, "lib.dom.d.ts", `
declare class Holder extends BaseOpts implements IfaceOpts { x: number; }
interface BaseOpts { b: number; }
interface IfaceOpts { i: number; }
`)})
	require.NoError(t, err)

	require.Empty(t, AnalyzeTypeOnlyRouting(res).forPackage(WebDOM.URI).Unreferenced)
}

// TestAnalyzeTypeOnlyRouting_ReadsInferConstraint covers the bound on
// an `infer`. `T extends Promise<infer U extends Bound>` names Bound
// only there, so a walk that stops at the infer reports it as
// referenced by nothing.
func TestAnalyzeTypeOnlyRouting_ReadsInferConstraint(t *testing.T) {
	t.Parallel()
	res, err := PartitionLib([]LibInput{parseLib(t, "lib.dom.d.ts", `
interface Request { u: Unwrap<Promise<string>>; }
type Unwrap<T> = T extends Promise<infer U extends Bound> ? U : never;
type Bound = string;
`)})
	require.NoError(t, err)

	routing := AnalyzeTypeOnlyRouting(res).forPackage(WebDOM.URI)

	require.Empty(t, routing.Unreferenced)
	require.Equal(t, []SoleReferrer{
		{Name: "Unwrap", DeclaredIn: "web:dom", ReferencedBy: "web:fetch"},
	}, routing.SoleReferrer)
}
