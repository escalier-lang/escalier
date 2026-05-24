package checker

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTarjanSCCs_NonCyclicImporterStaysSingleton pins the Tarjan
// invariant that a node which imports into an SCC but isn't reachable
// back from the SCC is its own singleton component, not absorbed into
// the cycle. Without this, the loader would merge a non-cyclic
// importer into the cyclic load — duplicating its declarations into
// the merged module and breaking the singleton load path it should
// have taken.
func TestTarjanSCCs_NonCyclicImporterStaysSingleton(t *testing.T) {
	// app → dom ↔ webgl (app is a one-way importer of the cycle).
	edges := map[string][]string{
		"web:app":   {"web:dom"},
		"web:dom":   {"web:webgl"},
		"web:webgl": {"web:dom"},
	}
	sccs := tarjanSCCs(edges)
	got := normalizeSCCs(sccs)
	want := [][]string{
		{"web:app"},
		{"web:dom", "web:webgl"},
	}
	require.Equal(t, want, got)
}

// TestTarjanSCCs_TwoCyclesWithBridge verifies SCC isolation when one
// cycle imports into another: bridge → {dom ↔ webgl} and bridge is
// itself part of a different cycle {bridge ↔ host}. The two cycles
// must remain distinct SCCs (no transitive merging).
func TestTarjanSCCs_TwoCyclesWithBridge(t *testing.T) {
	edges := map[string][]string{
		"web:dom":    {"web:webgl"},
		"web:webgl":  {"web:dom"},
		"web:bridge": {"web:dom", "web:host"},
		"web:host":   {"web:bridge"},
	}
	got := normalizeSCCs(tarjanSCCs(edges))
	want := [][]string{
		{"web:bridge", "web:host"},
		{"web:dom", "web:webgl"},
	}
	require.Equal(t, want, got)
}

// TestBuildStdlibPkgGraph_FullPipeline exercises the full scan +
// extract + Tarjan pipeline against an on-disk tempdir that covers
// the cases the higher-level tests don't isolate:
//
//   - A 2-cycle inside `web/` (dom ↔ webgl).
//   - A `web/` package that imports into the cycle but isn't part of it
//     (app → dom) — must remain a singleton SCC.
//   - An isolated `std/` package with no imports — singleton.
//   - A `std/` package with non-pseudo imports (a user-package import
//     and a `node:` import) — both must be filtered out of the graph.
//   - A `node/` subdir entirely — must be skipped, since `node:` isn't
//     in schemesWithSCCSupport.
func TestBuildStdlibPkgGraph_FullPipeline(t *testing.T) {
	dir := makeTempStdlibDir(t, map[string]string{
		"web/dom.esc": `
import "web:webgl?nested"
export declare class HTMLCanvasElement {}
`,
		"web/webgl.esc": `
import "web:dom?nested"
export declare class WebGLRenderingContext {}
`,
		"web/app.esc": `
import "web:dom?nested"
export declare class App {}
`,
		"std/math.esc": `
export declare fn add(a: number, b: number) -> number
`,
		"std/util.esc": `
import "node:fs"
import "some-user-package"
export declare fn util() -> number
`,
		// node/* is in stdlibSchemes but not schemesWithSCCSupport, so the
		// scan must skip this directory entirely.
		"node/fs.esc": `
import "web:dom?nested"
export declare fn readFile(path: string) -> string
`,
	})

	got, err := buildStdlibPkgGraph(context.Background(), dir)
	require.NoError(t, err)

	// Convert the returned URI → SCC map into the canonical form
	// `URI → sorted member list` so test assertions are stable.
	gotByURI := map[string][]string{}
	for uri, scc := range got {
		s := append([]string(nil), scc...)
		sort.Strings(s)
		gotByURI[uri] = s
	}
	want := map[string][]string{
		"web:dom":   {"web:dom", "web:webgl"},
		"web:webgl": {"web:dom", "web:webgl"},
		"web:app":   {"web:app"},
		"std:math":  {"std:math"},
		"std:util":  {"std:util"},
	}
	require.Equal(t, want, gotByURI)
}

// makeTempStdlibDir writes the {relative-path → contents} map under a
// fresh t.TempDir() and returns the directory.
func makeTempStdlibDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, contents := range files {
		full := filepath.Join(dir, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(contents), 0o644))
	}
	return dir
}

// normalizeSCCs sorts each component and the outer list so test
// assertions don't depend on Tarjan's visit order.
func normalizeSCCs(sccs [][]string) [][]string {
	out := make([][]string, len(sccs))
	for i, scc := range sccs {
		s := append([]string(nil), scc...)
		sort.Strings(s)
		out[i] = s
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i][0] < out[j][0]
	})
	return out
}
