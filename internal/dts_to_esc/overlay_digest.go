package dts_to_esc

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DigestSuffix is the suffix of the sidecar that records what one
// `replace` file stands in for. The sidecar sits beside its overlay file, so
// `std/array.replace.esc` pairs with `std/array.replace.digests.json`.
const DigestSuffix = ".digests.json"

// digestKey addresses one recorded digest. Member is empty for a whole
// declaration a `replace` stands in for, and holds the member's name
// otherwise. The remaining two fields are the rest of the member's
// slot. Static separates the two sides of a class. Kind separates the
// members one name can address, a `get x()` and a `set x()` among them.
type digestKey struct {
	Decl   string
	Member string
	Static bool
	Kind   memberKind
}

// label names what a key addresses, in the form the overlay errors use.
func (k digestKey) label() string {
	if k.Member == "" {
		return k.Decl
	}
	return k.Decl + "." + k.Member
}

// keyForSlot addresses the digest of the converted members one overlay
// member stands in for.
func keyForSlot(owner string, slot memberSlot) digestKey {
	return digestKey{
		Decl:   owner,
		Member: slot.Name,
		Static: slot.Static,
		Kind:   slot.Kind,
	}
}

// OverlayDigest is one entry of a sidecar. It records the digest of the
// converted declaration or member an overlay `replace` entry stood in
// for when the digest was taken.
type OverlayDigest struct {
	Decl   string     `json:"decl"`
	Member string     `json:"member,omitempty"`
	Kind   memberKind `json:"kind,omitempty"`
	Static bool       `json:"static,omitempty"`
	Digest string     `json:"digest"`
}

// digestPass records or checks the digest of every converted
// declaration and member a `replace` stands in for.
//
// A `replace` forks its target. The overlay wins by construction, so a
// change upstream stops reaching the output. The digest is what turns
// that silence into a failed run. It pins the converted form the
// overlay was written against, and a run compares the two.
//
// Under record the pass takes each digest as the current answer instead
// of comparing, and ApplyOverlay then writes the sidecars.
// `dts_to_esc generate --update-digests` is how a contributor records
// what a new or revised overlay entry stands in for.
type digestPass struct {
	record bool

	// observed holds the digests this run computed, keyed by the
	// overlay file's path relative to the overlay root.
	observed map[string][]OverlayDigest
}

func newDigestPass(record bool) *digestPass {
	return &digestPass{record: record, observed: map[string][]OverlayDigest{}}
}

// visit takes the digest of the converted forms one `replace` entry
// substitutes, given their printed Escalier source. Outside record mode
// it fails when the sidecar has no entry for the key or records a
// different form.
func (dp *digestPass) visit(f OverlayFile, key digestKey, forms []string) error {
	found := digestOf(forms)
	dp.observed[f.Path] = append(dp.observed[f.Path], OverlayDigest{
		Decl:   key.Decl,
		Member: key.Member,
		Kind:   key.Kind,
		Static: key.Static,
		Digest: found,
	})
	if dp.record {
		return nil
	}
	recorded, ok := f.Digests[key]
	if !ok {
		return fmt.Errorf(
			"overlay: %s replaces %s, and %s records no digest for it; run "+
				"`dts_to_esc generate --update-digests` to record what the overlay "+
				"stands in for", f.Path, key.label(), digestPathFor(f.Path))
	}
	if recorded != found {
		return fmt.Errorf(
			"overlay: %s replaces %s, whose converted form has changed since %s "+
				"recorded it; check the overlay against the upstream declaration, "+
				"then run `dts_to_esc generate --update-digests`",
			f.Path, key.label(), digestPathFor(f.Path))
	}
	return nil
}

// finish closes the pass over one overlay file, reporting a recorded
// entry the file no longer replaces. Such an entry pins a form nothing
// reads. Record mode rewrites the sidecar from what the file replaces
// now, so it has nothing to report.
func (dp *digestPass) finish(f OverlayFile) error {
	if dp.record {
		return nil
	}
	replaced := map[digestKey]bool{}
	for _, e := range dp.observed[f.Path] {
		replaced[keyOf(e)] = true
	}
	for _, key := range sortedDigestKeys(f.Digests) {
		if !replaced[key] {
			return fmt.Errorf(
				"overlay: %s records a digest for %s, which %s does not replace; run "+
					"`dts_to_esc generate --update-digests` to bring the two back in step",
				digestPathFor(f.Path), key.label(), f.Path)
		}
	}
	return nil
}

// write rewrites every `replace` file's sidecar from the digests the
// run took. It runs once the whole overlay has applied, so a run that
// fails partway records nothing rather than leaving half the tree
// rewritten.
//
// A file that replaces nothing any more loses its sidecar, and so does a
// sidecar left beside no `replace` file at all. That is how
// `--update-digests` clears a record a contributor has stopped needing.
func (dp *digestPass) write(o *Overlay) error {
	for _, f := range o.Files {
		if f.Op != OverlayReplace {
			continue
		}
		path := filepath.Join(o.Dir, filepath.FromSlash(digestPathFor(f.Path)))
		if err := writeOverlayDigests(path, dp.observed[f.Path]); err != nil {
			return err
		}
	}
	for _, rel := range o.unpairedSidecars() {
		path := filepath.Join(o.Dir, filepath.FromSlash(rel))
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing overlay digests %s: %w", rel, err)
		}
	}
	return nil
}

// digestOf hashes the printed Escalier source of what one entry stands
// in for. A member name addresses a whole overload set, so forms holds
// one string per signature and their order is the order the converted
// declaration lists them in.
//
// The first 16 hex digits of the SHA-256 are enough to catch an upstream
// edit and short enough to read in a diff.
func digestOf(forms []string) string {
	sum := sha256.Sum256([]byte(strings.Join(forms, "\n")))
	return hex.EncodeToString(sum[:])[:16]
}

// digestPathFor returns the sidecar path that pairs with an overlay
// file, by swapping the `.esc` suffix. It answers in whichever form it
// is asked, a path relative to the overlay root or one on disk.
func digestPathFor(escPath string) string {
	return strings.TrimSuffix(escPath, ".esc") + DigestSuffix
}

// loadOverlayDigests reads the sidecar beside one overlay file, given
// that file's path on disk. A file with no sidecar yet has no recorded
// entries, and the first run against it stops on the first entry it
// cannot find.
func loadOverlayDigests(escPath string) (map[digestKey]string, error) {
	path := digestPathFor(escPath)
	contents, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[digestKey]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading overlay digests %s: %w", filepath.Base(path), err)
	}
	var entries []OverlayDigest
	if err := json.Unmarshal(contents, &entries); err != nil {
		return nil, fmt.Errorf(
			"overlay: %s does not parse as JSON: %w", filepath.Base(path), err)
	}
	digests := map[digestKey]string{}
	for _, e := range entries {
		digests[keyOf(e)] = e.Digest
	}
	return digests, nil
}

// writeOverlayDigests writes one sidecar, sorted so a run rewrites it
// byte for byte. A file that replaces nothing gets no sidecar, and an
// existing one is removed.
func writeOverlayDigests(path string, entries []OverlayDigest) error {
	if len(entries) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing overlay digests %s: %w", filepath.Base(path), err)
		}
		return nil
	}
	sort.Slice(entries, func(i, j int) bool {
		return digestKeyLess(keyOf(entries[i]), keyOf(entries[j]))
	})
	contents, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding overlay digests %s: %w", filepath.Base(path), err)
	}
	contents = append(contents, '\n')
	if err := os.WriteFile(path, contents, 0o644); err != nil {
		return fmt.Errorf("writing overlay digests %s: %w", filepath.Base(path), err)
	}
	return nil
}

// keyOf returns what one sidecar entry addresses.
func keyOf(e OverlayDigest) digestKey {
	return digestKey{Decl: e.Decl, Member: e.Member, Static: e.Static, Kind: e.Kind}
}

// digestKeyLess orders two keys by declaration, then by instance before
// static, then by member name, then by kind. Both the sidecar and the
// stale-entry report follow it, so a rewritten file is byte-identical
// and a report names the first stale entry rather than an arbitrary one.
func digestKeyLess(a, b digestKey) bool {
	if a.Decl != b.Decl {
		return a.Decl < b.Decl
	}
	if a.Static != b.Static {
		return !a.Static
	}
	if a.Member != b.Member {
		return a.Member < b.Member
	}
	return a.Kind < b.Kind
}

// sortedDigestKeys orders one sidecar's keys.
func sortedDigestKeys(digests map[digestKey]string) []digestKey {
	keys := make([]digestKey, 0, len(digests))
	for key := range digests {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return digestKeyLess(keys[i], keys[j]) })
	return keys
}
