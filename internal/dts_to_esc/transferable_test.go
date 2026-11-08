package dts_to_esc

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/escalier-lang/escalier/internal/dts_parser"
	"github.com/escalier-lang/escalier/internal/webidl"
	"github.com/stretchr/testify/require"
)

// transferableArmNames returns the arm names of the `type Transferable` stmts
// declares at the top level, in order.
func transferableArmNames(t *testing.T, stmts []dts_parser.Statement) []string {
	t.Helper()
	for _, stmt := range stmts {
		td, ok := stmt.(*dts_parser.TypeDecl)
		if !ok || td.Name.Name != transferableAlias {
			continue
		}
		union, ok := td.TypeAnn.(*dts_parser.UnionType)
		require.True(t, ok, "Transferable is not a union")
		names := make([]string, len(union.Types))
		for i, arm := range union.Types {
			ref, ok := arm.(*dts_parser.TypeReference)
			require.True(t, ok, "arm %d is not a type reference", i)
			names[i] = typeRefName(ref)
		}
		return names
	}
	require.Fail(t, "no type Transferable")
	return nil
}

// TestDeriveTransferable asserts that a `web:*` bucket's `Transferable` lists
// the declared `[Transferable]` interfaces in name order, then the arms
// TypeScript lists that WebIDL does not define, and that every other bucket and
// the input slices are left alone.
func TestDeriveTransferable(t *testing.T) {
	t.Parallel()

	const src = `
interface Port {}
interface Bitmap {}
interface Canvas {}
type Transferable = Port | Canvas | ArrayBuffer;
`
	defs := map[string]webidl.Definition{
		// TypeScript lists it and WebIDL agrees.
		"Port": {Kind: webidl.KindInterface, Transferable: true},
		// WebIDL marks it and TypeScript omits it.
		"Bitmap": {Kind: webidl.KindInterface, Transferable: true},
		// TypeScript lists it and WebIDL does not mark it.
		"Canvas": {Kind: webidl.KindInterface},
		// WebIDL marks it and no bucket declares it.
		"Handle": {Kind: webidl.KindInterface, Transferable: true},
	}
	web := parseLib(t, "web.d.ts", src).Module.Statements
	std := parseLib(t, "std.d.ts", src).Module.Statements
	buckets := map[string][]dts_parser.Statement{"web:dom": web, "std:x": std}

	got := deriveTransferable(buckets, defs)

	// The `web:*` bucket's union is derived. It gains `Bitmap`, loses `Canvas`, and
	// keeps `ArrayBuffer`, which WebIDL does not define.
	require.Equal(t, []string{"Bitmap", "Port", "ArrayBuffer"}, transferableArmNames(t, got["web:dom"]))
	// A bucket outside `web:*` keeps TypeScript's union as written.
	require.Equal(t, []string{"Port", "Canvas", "ArrayBuffer"}, transferableArmNames(t, got["std:x"]))
	// deriveTransferable copies the alias it rewrites rather than editing it, so the
	// statements the test passed in as the `web:dom` bucket still hold TypeScript's
	// union. ConvertBuckets reads its buckets from the PartitionResult that Generate
	// also returns, and a reader of that result sees what the partition produced.
	require.Equal(t, []string{"Port", "Canvas", "ArrayBuffer"}, transferableArmNames(t, web))
}

// TestDeriveTransferable_PinnedLibSet asserts the union the pinned lib set and
// the pinned WebIDL table produce. WebIDL marks sixteen interfaces, and the
// four the pinned libs do not declare are left out.
func TestDeriveTransferable_PinnedLibSet(t *testing.T) {
	t.Parallel()

	repoRoot, err := findRepoRoot()
	require.NoError(t, err)
	libDir := filepath.Join(repoRoot, "node_modules", "typescript", "lib")
	if _, err := os.Stat(libDir); err != nil {
		t.Skipf("pinned TypeScript lib set not present at %s: %v", libDir, err)
	}
	basenames, err := DiscoverLibFiles(libDir)
	require.NoError(t, err)
	inputs, err := ParseLibFiles(libDir, basenames)
	require.NoError(t, err)
	res, err := PartitionLibWithOverlay(inputs, committedOverlay(t))
	require.NoError(t, err)

	got := deriveTransferable(res.Buckets, webidl.Definitions)
	require.Equal(t, []string{
		"AudioData",
		"ImageBitmap",
		"MIDIAccess",
		"MediaSourceHandle",
		"MediaStreamTrack",
		"MessagePort",
		"OffscreenCanvas",
		"RTCDataChannel",
		"ReadableStream",
		"TransformStream",
		"VideoFrame",
		"WritableStream",
		"ArrayBuffer",
	}, transferableArmNames(t, got["web:dom"]))

	declared := declaredNames(res.Buckets)
	var undeclared []string
	for _, name := range transferableNames(webidl.Definitions) {
		if !declared.Contains(name) {
			undeclared = append(undeclared, name)
		}
	}
	require.Equal(t, []string{
		"MediaStreamTrackHandle",
		"WebTransportDatagramsWritable",
		"WebTransportReceiveStream",
		"WebTransportSendStream",
	}, undeclared)
}
