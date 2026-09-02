package dts_to_esc

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/escalier-lang/escalier/internal/set"
)

// HandAuthoredPackages names the packages a run never writes and never
// removes: a `std:*` or `web:*` package with no upstream counterpart,
// authored by hand and committed like any other source file.
//
// The list is empty. Every package in the partition table today takes
// its declarations from the pinned lib set, so the generator owns every
// file under internal/interop/data/{std,web}/. The §7 stdlib bootstrap
// is where the first hand-authored package is expected.
var HandAuthoredPackages = set.NewSet[string]()

// GenerateOptions is the input set of one `generate` run.
type GenerateOptions struct {
	// LibDir holds the pinned `lib.*.d.ts` files, typically
	// node_modules/typescript/lib.
	LibDir string

	// OverlayDir holds the hand-written `.esc` fragments, typically
	// internal/interop/overlay.
	OverlayDir string

	// OutDir is the root of the generated tree, typically
	// internal/interop/data. Package files are written under its std/
	// and web/ subdirectories.
	OutDir string

	// HandAuthored names the packages the run leaves alone. Callers pass
	// HandAuthoredPackages; a nil set means the run owns every package.
	HandAuthored set.Set[string]

	// RecordDigests makes the run rewrite each `replace` file's digest
	// sidecar from the converted forms it stands in for, rather than
	// checking the recorded ones. It is how a contributor records what a
	// new or revised overlay entry replaces.
	RecordDigests bool
}

// GenerateResult reports what one run did, for the caller to print.
type GenerateResult struct {
	// LibFiles are the basenames the run discovered under LibDir.
	LibFiles []string

	// Partition is the routed and merged input, kept for the reports
	// that read the drop notes and per-package declaration counts.
	Partition *PartitionResult

	// Modules are the converted packages the run wrote, keyed by URI.
	// The ECMA-262 join reads them for the members each package
	// declares.
	Modules map[string]*StandaloneModule

	// Written lists the package URIs the run wrote, sorted.
	Written []string

	// Removed lists the slash-separated paths, relative to OutDir, of
	// the generated files the run deleted because no input accounts for
	// them any more.
	Removed []string
}

// Generate writes the whole generated tree from the three inputs of
// §6.4: the pinned `.d.ts` set, the ECMA-262 derived facts the converter
// applies, and the overlay. It reads no file it wrote, so seeding an
// empty tree and re-running against a populated one are the same
// operation, and `git diff` is the review surface for a TypeScript
// version bump.
//
// A run deletes the generated packages it no longer emits, so a package
// that stops being routed leaves the tree rather than lingering as a
// file no input accounts for. Packages in opts.HandAuthored are exempt
// from both the write and the delete.
func Generate(opts GenerateOptions) (*GenerateResult, error) {
	overlay, err := LoadOverlay(opts.OverlayDir)
	if err != nil {
		return nil, err
	}
	overlay.RecordDigests = opts.RecordDigests

	basenames, err := DiscoverLibFiles(opts.LibDir)
	if err != nil {
		return nil, err
	}
	if len(basenames) == 0 {
		return nil, fmt.Errorf("no lib.*.d.ts files found under %s", opts.LibDir)
	}
	inputs, err := ParseLibFiles(opts.LibDir, basenames)
	if err != nil {
		return nil, err
	}

	partition, err := PartitionLibWithOverlay(inputs, overlay)
	if err != nil {
		return nil, err
	}
	mods, err := ConvertBuckets(partition)
	if err != nil {
		return nil, err
	}
	if err := ApplyOverlay(mods, overlay); err != nil {
		return nil, err
	}
	for uri := range mods {
		if opts.HandAuthored.Contains(uri) {
			delete(mods, uri)
		}
	}

	written, err := WriteConvertedTree(mods, opts.OutDir)
	if err != nil {
		return nil, err
	}
	if err := ScaffoldNodeDir(opts.OutDir); err != nil {
		return nil, err
	}
	removed, err := removeStalePackages(opts.OutDir, set.FromSlice(written), opts.HandAuthored)
	if err != nil {
		return nil, err
	}

	return &GenerateResult{
		LibFiles:  basenames,
		Partition: partition,
		Modules:   mods,
		Written:   written,
		Removed:   removed,
	}, nil
}

// removeStalePackages deletes every `.esc` file in the generated
// subtrees that this run did not write. That covers two cases. A
// package that stops being routed leaves the tree, and so does a file
// that matches no package in the partition table at all.
//
// Only the subtrees the partition table writes into are scanned, so
// data/node/ and anything else beside them is untouched. A file whose
// package is hand-authored is left alone, whether or not the partition
// table names that package.
//
// Returns the deleted paths, relative to outDir and slash-separated,
// sorted.
func removeStalePackages(outDir string, written, handAuthored set.Set[string]) ([]string, error) {
	var removed []string
	for _, dir := range generatedDirs() {
		entries, err := os.ReadDir(filepath.Join(outDir, dir))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("reading generated dir %s: %w", dir, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".esc") {
				continue
			}
			rel := dir + "/" + entry.Name()
			uri := packageURIForFile(rel)
			if written.Contains(uri) || handAuthored.Contains(uri) {
				continue
			}
			if err := os.Remove(filepath.Join(outDir, filepath.FromSlash(rel))); err != nil {
				return nil, fmt.Errorf("removing stale %s: %w", rel, err)
			}
			removed = append(removed, rel)
		}
	}
	sort.Strings(removed)
	return removed, nil
}

// packageURIForFile returns the package URI a generated file belongs to,
// so "std/typed_arrays.esc" gives "std:typed_arrays". Every entry in the
// partition table pairs its URI with its file by that rule, which
// TestPackageURIForFile_MatchesThePartitionTable holds the two to.
//
// removeStalePackages derives the URI rather than looking the file up in
// the table, so a hand-authored package the table does not name is still
// recognized and left where it is.
func packageURIForFile(file string) string {
	dir, base, found := strings.Cut(file, "/")
	if !found {
		return ""
	}
	return dir + ":" + strings.TrimSuffix(base, ".esc")
}

// generatedDirs returns the subdirectories of the output root that hold
// generated package files, sorted. Today that is std/ and web/, read off
// the partition table rather than restated.
func generatedDirs() []string {
	dirs := set.NewSet[string]()
	for _, uri := range PackageList() {
		pkg, ok := PackageForURI(uri)
		if !ok {
			continue
		}
		if dir, _, found := strings.Cut(pkg.File, "/"); found {
			dirs.Add(dir)
		}
	}
	out := dirs.ToSlice()
	sort.Strings(out)
	return out
}
