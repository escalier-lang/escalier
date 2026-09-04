package dts_to_esc

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// hostDOMLib is the window half of a lib set covering all three
// verdicts. `Request` routes to web:fetch and `CompressionFormat` to
// web:compression by the §6.1 partition, so the three packages the
// analysis reads are web:fetch, web:compression, and the web:dom the
// rest falls through to.
const hostDOMLib = `
interface Request { url: string; }
interface RequestInit { method: string; }
type CompressionFormat = string;
interface Widget { id: string; }
`

// hostWorkerLib restates the two names a worker has. It restates them
// as TypeScript does, in full rather than by reference, which is what
// makes a name's absence here mean the worker does not have it.
const hostWorkerLib = `
interface Request { url: string; }
type CompressionFormat = string;
`

// hostESLib stands in for the ES core both hosts load. Neither host lib
// restates `Array`, so a rule reading only the worker lib would call
// std:array window-only.
const hostESLib = `
interface Array<T> { length: number; }
`

// hostRouting routes the three libs above through PartitionLib.
func hostRouting(t *testing.T) *PartitionResult {
	t.Helper()
	res, err := PartitionLib([]LibInput{
		parseLib(t, "lib.es5.d.ts", hostESLib),
		parseLib(t, "lib.dom.d.ts", hostDOMLib),
		parseLib(t, "lib.webworker.d.ts", hostWorkerLib),
	})
	require.NoError(t, err)
	return res
}

// TestAnalyzeHostAvailability covers the three verdicts.
//
//   - web:fetch holds `Request`, which the worker lib restates, and
//     `RequestInit`, which it does not. That is mixed.
//   - web:compression holds `CompressionFormat` alone, restated, so it
//     is worker-clean.
//   - web:dom holds `Widget`, which nothing restates, so it is
//     window-only.
//   - std:array holds `Array`, which neither host lib restates because
//     both load the ES core that declares it. That is worker-clean.
func TestAnalyzeHostAvailability(t *testing.T) {
	t.Parallel()

	require.Equal(t, []PackageHosts{
		{URI: "std:array", Verdict: WorkerCleanHost, Total: 1},
		{URI: "web:compression", Verdict: WorkerCleanHost, Total: 1},
		{URI: "web:dom", Verdict: WindowOnlyHost, Total: 1,
			WindowOnly: []string{"Widget"}},
		{URI: "web:fetch", Verdict: MixedHost, Total: 2,
			WindowOnly: []string{"RequestInit"}},
	}, AnalyzeHostAvailability(hostRouting(t)))
}

// TestAnalyzeHostAvailability_CountsANameOnce pins the dedupe. A trio
// reaches its bucket as three statements sharing one name, and counting
// each would put a package's total above the names it exports.
func TestAnalyzeHostAvailability_CountsANameOnce(t *testing.T) {
	t.Parallel()
	res, err := PartitionLib([]LibInput{parseLib(t, "lib.dom.d.ts", `
interface Request { url: string; }
interface RequestConstructor { new (): Request; }
declare var Request: RequestConstructor;
`)})
	require.NoError(t, err)

	hosts := AnalyzeHostAvailability(res)
	require.Len(t, hosts, 1)
	require.Equal(t, "web:fetch", hosts[0].URI)
	require.Equal(t, 2, hosts[0].Total)
}

// TestReportHostAvailability_Renders pins the report's text over the
// lib set above, which holds one package of each verdict and one mixed
// package MixedHostPackages does not record.
func TestReportHostAvailability_Renders(t *testing.T) {
	t.Parallel()

	var b strings.Builder
	require.NoError(t, ReportHostAvailability(hostRouting(t), &b))

	require.Equal(t, strings.Join([]string{
		"  host availability: 2 worker-clean, 1 window-only, 1 mixed",
		"    web:dom: window-only, 1 declaration",
		"    web:fetch: mixed — 1 of 2 window-only: RequestInit",
		"      needs a decision",
		"",
	}, "\n"), b.String())
}

// TestReportHostAvailability_RecordsAKnownMix covers the other branch
// of the mixed line. web:crypto is in MixedHostPackages, so its reason
// stands in for "needs a decision".
func TestReportHostAvailability_RecordsAKnownMix(t *testing.T) {
	t.Parallel()
	res, err := PartitionLib([]LibInput{
		parseLib(t, "lib.dom.d.ts", `
interface CryptoKey { extractable: boolean; }
interface EcKeyAlgorithm { namedCurve: string; }
`),
		parseLib(t, "lib.webworker.d.ts", `
interface CryptoKey { extractable: boolean; }
`),
	})
	require.NoError(t, err)

	var b strings.Builder
	require.NoError(t, ReportHostAvailability(res, &b))

	require.Equal(t, strings.Join([]string{
		"  host availability: 0 worker-clean, 0 window-only, 1 mixed",
		"    web:crypto: mixed — 1 of 2 window-only: EcKeyAlgorithm",
		"      recorded: " + MixedHostPackages["web:crypto"],
		"",
	}, "\n"), b.String())
}

// TestSummarizeNames covers the cap a long window-only list hits.
// web:dom's list runs to hundreds of names, and printing it whole would
// bury the packages a reader can act on.
func TestSummarizeNames(t *testing.T) {
	t.Parallel()
	names := make([]string, 0, windowOnlyNameCap+3)
	for i := 0; i < cap(names); i++ {
		names = append(names, string(rune('a'+i)))
	}

	require.Equal(t, "a, b, c", summarizeNames(names[:3]))
	require.Equal(t, strings.Join(names[:windowOnlyNameCap], ", ")+", and 3 more",
		summarizeNames(names))
}

// TestReportHostAvailability_PinnedLibSet is the §6.1 gate on the host
// axis over the real input: every mixed `web:*` package is one
// MixedHostPackages records. The report prints a line per package
// either way, so the gate reads the analysis rather than the text.
//
// The second assertion keeps MixedHostPackages honest in the other
// direction. An entry for a package that is no longer mixed is stale,
// which is what a TypeScript bump that restates a missing name leaves
// behind.
func TestReportHostAvailability_PinnedLibSet(t *testing.T) {
	t.Parallel()
	hosts := AnalyzeHostAvailability(pinnedRouting(t))

	mixed := map[string]bool{}
	for _, h := range hosts {
		if h.Verdict == MixedHost {
			mixed[h.URI] = true
			require.Contains(t, MixedHostPackages, h.URI,
				"%s is mixed with no recorded decision", h.URI)
		}
	}
	for uri := range MixedHostPackages {
		require.True(t, mixed[uri],
			"MixedHostPackages records %s, which is no longer mixed", uri)
	}
}

// TestAnalyzeHostAvailability_CoversEveryPackage pins the report's
// scope over the real input. A package missing from the analysis would
// pass the gate above by never being read.
func TestAnalyzeHostAvailability_CoversEveryPackage(t *testing.T) {
	t.Parallel()
	res := pinnedRouting(t)

	hosts := AnalyzeHostAvailability(res)
	require.Len(t, hosts, len(res.Buckets))
	for _, h := range hosts {
		require.Contains(t, res.Buckets, h.URI)
		require.NotZero(t, h.Total, "%s reads as holding no declaration", h.URI)
	}
}
