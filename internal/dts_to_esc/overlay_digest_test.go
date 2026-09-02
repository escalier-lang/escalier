package dts_to_esc

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/require"
)

// overlayMovedLib is overlayLib with `at` retyped, standing in for a
// TypeScript version that moves a member an overlay replaces. Recording
// against one and checking against the other is what a version bump
// does to a `replace`.
const overlayMovedLib = `
interface Array<T> { length: number; at(index: number): T | null; }
interface ArrayConstructor { new <T>(): Array<T>; isArray(arg: any): boolean; }
declare var Array: ArrayConstructor;
interface ArrayLike<T> { readonly length: number; }
declare function parseInt(string: string, radix?: number): number;
`

// replaceOverlay is the overlay tree the digest tests record: one
// member of the converted class and one whole declaration, the two
// things a `replace` can stand in for.
var replaceOverlay = map[string]string{
	"std/array.replace.esc": "export declare class Array<T> {\n" +
		"    at(self, index: number) -> T,\n}\n" +
		"export declare type ArrayLike<T> = { length: number }\n",
}

// readDigests returns one sidecar's contents.
func readDigests(t *testing.T, dir, rel string) string {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	require.NoError(t, err)
	return string(contents)
}

// TestOverlayDigests_RecordWritesASidecarBesideTheReplaceFile pins the
// file `dts_to_esc generate --update-digests` writes. Each entry
// addresses one converted member or declaration and carries the digest
// of its printed Escalier source.
func TestOverlayDigests_RecordWritesASidecarBesideTheReplaceFile(t *testing.T) {
	t.Parallel()
	dir := seedOverlay(t, replaceOverlay)
	_, err := applyOverlayIn(t, dir, overlayLib, true)
	require.NoError(t, err)

	snaps.MatchInlineSnapshot(t, readDigests(t, dir, "std/array.replace.digests.json"), snaps.Inline(`[
  {
    "decl": "Array",
    "member": "at",
    "kind": "method",
    "digest": "fb2b83d0692345dc"
  },
  {
    "decl": "ArrayLike",
    "digest": "d971c35a9fcaa007"
  }
]
`))
}

// TestOverlayDigests_RecordingTwiceLeavesTheSidecarByteIdentical keeps
// the sidecar out of the churn a re-run would otherwise produce. It is
// a committed input, so a run that changes nothing must leave it alone.
func TestOverlayDigests_RecordingTwiceLeavesTheSidecarByteIdentical(t *testing.T) {
	t.Parallel()
	dir := seedOverlay(t, replaceOverlay)
	_, err := applyOverlayIn(t, dir, overlayLib, true)
	require.NoError(t, err)
	first := readDigests(t, dir, "std/array.replace.digests.json")

	_, err = applyOverlayIn(t, dir, overlayLib, true)
	require.NoError(t, err)
	require.Equal(t, first, readDigests(t, dir, "std/array.replace.digests.json"))
}

// TestOverlayDigests_ReportAConvertedFormThatMoved is the check the
// granularity still needs. A `replace` forks its target, so upstream can
// retype the member it stands in for and the output would not move. The
// digest is what turns that silence into a failed run.
func TestOverlayDigests_ReportAConvertedFormThatMoved(t *testing.T) {
	t.Parallel()
	dir := seedOverlay(t, replaceOverlay)
	_, err := applyOverlayIn(t, dir, overlayLib, true)
	require.NoError(t, err)

	_, err = applyOverlayIn(t, dir, overlayMovedLib, false)
	require.EqualError(t, err,
		"overlay: std/array.replace.esc replaces Array.at, whose converted form has "+
			"changed since std/array.replace.digests.json recorded it; check the "+
			"overlay against the upstream declaration, then run "+
			"`dts_to_esc generate --update-digests`")
}

// TestOverlayDigests_ReportAnUnrecordedReplace covers the first run
// against a new overlay entry. Nothing pins what it stands in for yet,
// so the run stops rather than forking silently.
func TestOverlayDigests_ReportAnUnrecordedReplace(t *testing.T) {
	t.Parallel()
	_, err := applyOverlayIn(t, seedOverlay(t, replaceOverlay), overlayLib, false)
	require.EqualError(t, err,
		"overlay: std/array.replace.esc replaces Array.at, and "+
			"std/array.replace.digests.json records no digest for it; run "+
			"`dts_to_esc generate --update-digests` to record what the overlay "+
			"stands in for")
}

// TestOverlayDigests_ReportAnEntryTheOverlayNoLongerReplaces keeps the
// sidecar in step with the file beside it. An entry for a member the
// overlay has stopped replacing pins a form nothing reads.
func TestOverlayDigests_ReportAnEntryTheOverlayNoLongerReplaces(t *testing.T) {
	t.Parallel()
	dir := seedOverlay(t, map[string]string{
		"std/array.replace.esc": "export declare class Array<T> {\n" +
			"    length: number,\n    at(self, index: number) -> T,\n}\n",
	})
	_, err := applyOverlayIn(t, dir, overlayLib, true)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "std", "array.replace.esc"),
		[]byte("export declare class Array<T> {\n    length: number,\n}\n"), 0o644))
	_, err = applyOverlayIn(t, dir, overlayLib, false)
	require.EqualError(t, err,
		"overlay: std/array.replace.digests.json records a digest for Array.at, "+
			"which std/array.replace.esc does not replace; run "+
			"`dts_to_esc generate --update-digests` to bring the two back in step")
}

// TestOverlayDigests_ReportASidecarWithNoReplaceFile covers the sidecar
// left behind when its overlay file is renamed or retired. Nothing reads
// it, so it would otherwise sit in the tree recording a form no run
// checks.
func TestOverlayDigests_ReportASidecarWithNoReplaceFile(t *testing.T) {
	t.Parallel()
	seed := map[string]string{
		"std/array.add.esc": "export declare class Array<T> {\n" +
			"    static of<T>(...items: Array<T>) -> Array<T>,\n}\n",
		"std/array.replace.digests.json": "[]\n",
	}
	dir := seedOverlay(t, seed)
	_, err := applyOverlayIn(t, dir, overlayLib, false)
	require.EqualError(t, err,
		"overlay: std/array.replace.digests.json records digests for "+
			"std/array.replace.esc, which is not a replace file the overlay holds; "+
			"run `dts_to_esc generate --update-digests` to remove the sidecar, or "+
			"restore the replace file it belongs beside")

	// The recording run is the recovery the report names, so it removes
	// the sidecar rather than stopping on it.
	dir = seedOverlay(t, seed)
	_, err = applyOverlayIn(t, dir, overlayLib, true)
	require.NoError(t, err)
	require.NoFileExists(t, filepath.Join(dir, "std", "array.replace.digests.json"))
}

// TestOverlayDigests_KeepAGetterAndASetterApart covers the one name that
// addresses two members. Each gets its own sidecar entry, keyed on the
// kind, and the check run reads each back against the member its digest
// was taken from.
func TestOverlayDigests_KeepAGetterAndASetterApart(t *testing.T) {
	t.Parallel()
	dir := seedOverlay(t, map[string]string{
		"std/array.replace.esc": "export declare class Array<T> {\n" +
			"    get size(self) -> number | undefined,\n" +
			"    set size(mut self, v: number | undefined),\n}\n",
	})
	_, err := applyOverlayIn(t, dir, overlayKindLib, true)
	require.NoError(t, err)

	snaps.MatchInlineSnapshot(t, readDigests(t, dir, "std/array.replace.digests.json"), snaps.Inline(`[
  {
    "decl": "Array",
    "member": "size",
    "kind": "getter",
    "digest": "306eee11108848af"
  },
  {
    "decl": "Array",
    "member": "size",
    "kind": "setter",
    "digest": "59a6dc0bffd1defa"
  }
]
`))

	_, err = applyOverlayIn(t, dir, overlayKindLib, false)
	require.NoError(t, err)
}

// overlayDocLib and overlayEditedDocLib differ only in the prose above
// the member `std/array.replace.esc` stands in for. Prose churn is the
// bulk of a TypeScript version bump, and it moves no shape.
const overlayDocLib = `
interface Array<T> { length: number; /** Reads one element. */ at(index: number): T | undefined; }
interface ArrayConstructor { new <T>(): Array<T>; isArray(arg: any): boolean; }
declare var Array: ArrayConstructor;
interface ArrayLike<T> { readonly length: number; }
`

const overlayEditedDocLib = `
interface Array<T> { length: number; /** Reads the element at index. */ at(index: number): T | undefined; }
interface ArrayConstructor { new <T>(): Array<T>; isArray(arg: any): boolean; }
declare var Array: ArrayConstructor;
interface ArrayLike<T> { readonly length: number; }
`

// TestOverlayDigests_IgnoreADocCommentEdit keeps the check on the shape
// a `replace` stands in for. The doc comment is not part of that shape:
// carryDeclMetadata moves the converted prose onto the overlay
// declaration, so an upstream edit still reaches the output and forks
// nothing.
func TestOverlayDigests_IgnoreADocCommentEdit(t *testing.T) {
	t.Parallel()
	dir := seedOverlay(t, map[string]string{
		"std/array.replace.esc": "export declare class Array<T> {\n" +
			"    at(self, index: number) -> T,\n}\n",
	})
	_, err := applyOverlayIn(t, dir, overlayDocLib, true)
	require.NoError(t, err)

	_, err = applyOverlayIn(t, dir, overlayEditedDocLib, false)
	require.NoError(t, err)
}
