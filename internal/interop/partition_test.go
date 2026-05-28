package interop

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRoute_ExplicitPartition(t *testing.T) {
	t.Parallel()
	// Explicit-partition routing keys on name only — the sourceFile
	// argument is ignored unless the name falls through to the DOM
	// residual rule (covered separately by TestRoute_DOMResidual and
	// TestRoute_StandalonePackageWinsOverDOMResidual).
	cases := []struct {
		name    string
		wantURI string
	}{
		{"Array", "std:array"},
		{"ArrayConstructor", "std:array"},
		{"parseInt", "std:number"},
		{"Promise", "std:async"},
		{"Awaited", "std:async"},
		{"Partial", "std:object"},
		{"URIError", "std:url"},
		{"encodeURIComponent", "std:url"},
		{"Math", "std:math"},
		{"WebAssembly", "std:wasm"},
		{"fetch", "web:fetch"},
		{"ReadableStream", "web:streams"},
		{"WebGLRenderingContext", "web:webgl"},
		{"URL", "web:url"},
		{"WebSocket", "web:websocket"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Route(tc.name, "")
			require.False(t, got.Drop)
			require.False(t, got.Unmapped)
			require.Equal(t, tc.wantURI, got.Pkg.URI)
		})
	}
}

func TestRoute_DOMResidual(t *testing.T) {
	t.Parallel()
	// A name that is not in the explicit partition but originates in
	// lib.dom.d.ts (or its iterable companions) routes to web:dom.
	cases := []struct {
		name       string
		sourceFile string
	}{
		{"HTMLCanvasElement", "lib.dom.d.ts"},
		{"SVGCircleElement", "lib.dom.d.ts"},
		{"HTMLElementTagNameMap", "lib.dom.d.ts"},
		{"Document", "lib.dom.iterable.d.ts"},
		{"Element", "lib.dom.asynciterable.d.ts"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Route(tc.name, tc.sourceFile)
			require.False(t, got.Drop)
			require.False(t, got.Unmapped)
			require.Equal(t, "web:dom", got.Pkg.URI)
			require.Equal(t, "web/dom.esc", got.Pkg.File)
		})
	}
}

func TestRoute_ExplicitDrops(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"globalThis", "eval"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := Route(name, "lib.es5.d.ts")
			require.True(t, got.Drop)
			require.False(t, got.Unmapped)
		})
	}
}

func TestRoute_Unmapped(t *testing.T) {
	t.Parallel()
	// A made-up symbol that lives in a non-DOM source file is the
	// fail-safe case — the caller is expected to surface UnmappedError.
	got := Route("TotallyMadeUpSymbol", "lib.es2099.weirdness.d.ts")
	require.True(t, got.Unmapped)
	require.False(t, got.Drop)
	require.Equal(t, Package{}, got.Pkg)
}

func TestUnmappedError_MentionsSymbolSourceAndTable(t *testing.T) {
	t.Parallel()
	err := UnmappedError("FooBar", "lib.es5.d.ts")
	require.EqualError(t, err, `converter: unmapped top-level declaration "FooBar" from lib.es5.d.ts; add it to internal/interop/partition.go (see planning/builtins/implementation_plan.md §6.1) or to ExplicitDrops if intentional`)
}

func TestRoute_StandalonePackageWinsOverDOMResidual(t *testing.T) {
	t.Parallel()
	// Symbols declared in lib.dom.d.ts that belong to a standalone
	// sibling family must route to the sibling, not absorb into
	// web:dom via the residual rule. (Lookup order: explicit map
	// before residual.)
	cases := []struct {
		name    string
		wantURI string
	}{
		{"Request", "web:fetch"},
		{"Response", "web:fetch"},
		{"Headers", "web:fetch"},
		{"Crypto", "web:crypto"},
		{"Worker", "web:workers"},
		{"WebGL2RenderingContext", "web:webgl"},
		{"Blob", "web:file"},
		{"File", "web:file"},
		{"Performance", "web:performance"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Route(tc.name, "lib.dom.d.ts")
			require.Equal(t, tc.wantURI, got.Pkg.URI)
		})
	}
}

func TestPackageList_IncludesDOMAndIsSorted(t *testing.T) {
	t.Parallel()
	list := PackageList()
	require.NotEmpty(t, list)
	require.Contains(t, list, "web:dom")
	require.Contains(t, list, "std:array")
	for i := 1; i < len(list); i++ {
		require.Less(t, list[i-1], list[i],
			"PackageList must be sorted; %q !< %q at %d",
			list[i-1], list[i], i)
	}
}

func TestPackageForURI(t *testing.T) {
	t.Parallel()
	got, ok := PackageForURI("std:array")
	require.True(t, ok)
	require.Equal(t, "std/array.esc", got.File)

	got, ok = PackageForURI("web:dom")
	require.True(t, ok)
	require.Equal(t, "web/dom.esc", got.File)

	_, ok = PackageForURI("std:does_not_exist")
	require.False(t, ok)
}

func TestSchemeOf(t *testing.T) {
	t.Parallel()
	require.Equal(t, "std", SchemeOf("std:array"))
	require.Equal(t, "web", SchemeOf("web:dom"))
	require.Equal(t, "", SchemeOf("nocolon"))
}
