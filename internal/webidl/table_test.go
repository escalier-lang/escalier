package webidl

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

// repoRoot is the repository root relative to this package's directory, which
// is the working directory `go test` runs the package in.
var repoRoot = filepath.Join("..", "..")

// TestPinnedVersionMatchesPackageJSON asserts that PinnedVersion is the exact
// `@webref/idl` version package.json pins. It reads no installed package, so it
// runs before `pnpm install` too.
func TestPinnedVersionMatchesPackageJSON(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(filepath.Join(repoRoot, "package.json"))
	require.NoError(t, err)
	var manifest struct {
		DevDependencies map[string]string `json:"devDependencies"`
	}
	require.NoError(t, json.Unmarshal(data, &manifest))
	require.Equal(t, PinnedVersion, manifest.DevDependencies["@webref/idl"])
}

// TestTableMatchesInstalledPackage asserts that table.go is exactly what
// tools/webidl_facts writes from the installed `@webref/idl` package. A
// mismatch means the pin moved without the table being regenerated, or the
// table was edited by hand.
func TestTableMatchesInstalledPackage(t *testing.T) {
	t.Parallel()

	idlDir := filepath.Join(repoRoot, "node_modules", "@webref", "idl")
	if _, err := os.Stat(filepath.Join(idlDir, "package.json")); err != nil {
		t.Skipf("@webref/idl not present at %s; run `pnpm install`: %v", idlDir, err)
	}

	installed, err := PackageVersion(idlDir)
	require.NoError(t, err)
	require.Equal(t, PinnedVersion, installed)

	want, err := Generate(idlDir)
	require.NoError(t, err)
	got, err := os.ReadFile("table.go")
	require.NoError(t, err)
	require.Equal(t, string(want), string(got),
		"internal/webidl/table.go is stale; run `go run ./tools/webidl_facts`")
}

// TestTableFacts covers facts the later stages of
// planning/web_api_types/design.md read from the table, so a regeneration that
// changes one of them shows up as a named failure rather than only as a diff.
func TestTableFacts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want Definition
	}{
		// TypeScript declares `new()` on these, and WebIDL declares no
		// constructor. §5.3 drops the construct signatures.
		{"ImageBitmap", Definition{Kind: KindInterface, Exposed: []string{"Window", "Worker"}, Transferable: true}},
		{"MessagePort", Definition{Kind: KindInterface, Exposed: []string{"Window", "Worker", "AudioWorklet"}, Transferable: true}},
		{"Navigator", Definition{Kind: KindInterface, Exposed: []string{"Window"}}},
		{"Element", Definition{Kind: KindInterface, Exposed: []string{"Window"}}},
		// A worker reaches a MessagePort through this constructor instead.
		{"MessageChannel", Definition{Kind: KindInterface, Exposed: []string{"Window", "Worker"}, Constructible: true}},
		// Global scopes.
		{"Window", Definition{Kind: KindInterface, Exposed: []string{"Window"}, Global: []string{"Window"}}},
		{"DedicatedWorkerGlobalScope", Definition{
			Kind:    KindInterface,
			Exposed: []string{"DedicatedWorker"},
			Global:  []string{"Worker", "DedicatedWorker"},
		}},
		{"console", Definition{Kind: KindNamespace, Exposed: []string{"*"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Contains(t, Definitions, tt.name)
			require.Equal(t, tt.want, Definitions[tt.name])
		})
	}
}

// TestTableTransferable asserts the set of interfaces WebIDL marks
// `[Transferable]`, which §5.4 generates the `Transferable` union from.
func TestTableTransferable(t *testing.T) {
	t.Parallel()

	var got []string
	for name, def := range Definitions {
		if def.Transferable {
			got = append(got, name)
		}
	}
	sort.Strings(got)
	require.Equal(t, []string{
		"AudioData",
		"ImageBitmap",
		"MIDIAccess",
		"MediaSourceHandle",
		"MediaStreamTrack",
		"MediaStreamTrackHandle",
		"MessagePort",
		"OffscreenCanvas",
		"RTCDataChannel",
		"ReadableStream",
		"TransformStream",
		"VideoFrame",
		"WebTransportDatagramsWritable",
		"WebTransportReceiveStream",
		"WebTransportSendStream",
		"WritableStream",
	}, got)
}
