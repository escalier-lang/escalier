package interop

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRoute_ExplicitPartition(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		sourceFile string
		wantURI    string
	}{
		{"Array", "lib.es5.d.ts", "std:array"},
		{"ArrayConstructor", "lib.es5.d.ts", "std:array"},
		{"parseInt", "lib.es5.d.ts", "std:number"},
		{"Promise", "lib.es2015.promise.d.ts", "std:async"},
		{"Awaited", "lib.es5.d.ts", "std:async"},
		{"Partial", "lib.es5.d.ts", "std:object"},
		{"URIError", "lib.es5.d.ts", "std:url"},
		{"encodeURIComponent", "lib.es5.d.ts", "std:url"},
		{"Math", "lib.es5.d.ts", "std:math"},
		{"WebAssembly", "lib.es2018.intl.d.ts", "std:wasm"},
		{"fetch", "lib.dom.d.ts", "web:fetch"},
		{"ReadableStream", "lib.dom.d.ts", "web:streams"},
		{"WebGLRenderingContext", "lib.dom.d.ts", "web:webgl"},
		{"URL", "lib.dom.d.ts", "web:url"},
		{"WebSocket", "lib.dom.d.ts", "web:websocket"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Route(tc.name, tc.sourceFile)
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
	require.ErrorContains(t, err, "FooBar")
	require.ErrorContains(t, err, "lib.es5.d.ts")
	require.ErrorContains(t, err, "internal/interop/partition.go")
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

func TestIsKnownPackageURI(t *testing.T) {
	t.Parallel()
	require.True(t, IsKnownPackageURI("std:array"))
	require.True(t, IsKnownPackageURI("web:fetch"))
	require.True(t, IsKnownPackageURI("web:dom"))
	require.False(t, IsKnownPackageURI("std:typo"))
	require.False(t, IsKnownPackageURI("node:fs"))
}

func TestSchemeOf(t *testing.T) {
	t.Parallel()
	require.Equal(t, "std", SchemeOf("std:array"))
	require.Equal(t, "web", SchemeOf("web:dom"))
	require.Equal(t, "", SchemeOf("nocolon"))
}
