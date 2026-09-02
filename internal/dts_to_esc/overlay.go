package dts_to_esc

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/escalier-lang/escalier/internal/set"
)

// OverlayOp is what an overlay file's declarations do to the generated
// tree. Every declaration in a file takes the file's operation, which
// comes from the filename rather than from a marker on the declaration:
// `std/symbol.add.esc` carries OverlayAdd, `std/array.replace.esc`
// carries OverlayReplace.
//
// The parser has no marker to carry this instead. Decorators are its
// only annotation and internal/parser/decl.go rejects them on
// `interface`, `type`, `enum`, and `namespace`, which is most of the
// generated tree. See planning/builtins/implementation_plan.md §6.4.
type OverlayOp string

const (
	// OverlayAdd contributes a declaration or member no upstream source
	// has. A collision with a converted member fails the run.
	OverlayAdd OverlayOp = "add"

	// OverlayReplace stands in for what the upstream source expresses
	// wrongly. Each overlay member substitutes the converted member
	// sharing its key, in place, so a second run leaves the tree
	// byte-identical.
	OverlayReplace OverlayOp = "replace"

	// OverlayDrop names what the generator must not emit. A drop file's
	// declarations are read for their names alone.
	OverlayDrop OverlayOp = "drop"
)

// RootDropFile is the one overlay file that sits at the overlay root
// rather than under a package directory. Its entries are whole symbols
// that belong to no package — `eval` and `globalThis` — so they resolve
// during routing, before a package is assigned.
const RootDropFile = "drop.esc"

// OverlayFile is one parsed overlay fragment.
type OverlayFile struct {
	// Path is the file's slash-separated path relative to the overlay
	// root, such as "std/symbol.add.esc". Error messages name it.
	Path string

	// Op is the operation the filename carries.
	Op OverlayOp

	// PkgURI is the package the file applies to, empty for RootDropFile.
	PkgURI string

	// Decls are the file's top-level declarations, parsed by Escalier's
	// own parser.
	Decls []ast.Decl

	// Digests are the digests recorded in the sidecar beside a `replace`
	// file, keyed by the declaration or member each one addresses. Every
	// other operation leaves it empty, since only `replace` forks what
	// the upstream source says.
	Digests map[digestKey]string
}

// Overlay is the parsed contents of internal/interop/overlay/, the third
// generation input of §6.4 alongside the pinned `.d.ts` set and the
// ECMA-262 derived facts.
//
// Files are sorted by path, so a run applies them in the same order
// every time.
type Overlay struct {
	// Dir is the overlay root the files were read from. A run that
	// records digests writes the sidecars back under it.
	Dir string

	Files []OverlayFile

	// Sidecars are the digest sidecars found under the root, by path
	// relative to it and sorted. A run reads a sidecar through the
	// `replace` file it pairs with, so this list is what turns up the
	// ones that pair with none.
	Sidecars []string

	// RecordDigests makes a run take the digest of every converted
	// declaration and member a `replace` stands in for as the current
	// answer, and write the sidecars, instead of checking the recorded
	// ones. `dts_to_esc generate --update-digests` sets it.
	RecordDigests bool
}

// LoadOverlay parses every `.esc` file under dir. These are the only
// `.esc` files the generator reads, since it never reads a file it
// wrote. One that does not parse is a defect in a committed input, so
// the load stops and names it.
//
// Recognized layouts, both relative to dir:
//
//   - "drop.esc" — package-less whole-symbol drops.
//   - "<scheme>/<package>.<op>.esc" — an operation on one package, where
//     "<scheme>/<package>.esc" is that package's generated file. So
//     "std/symbol.add.esc" applies to the package written to
//     "std/symbol.esc".
//
// Any other `.esc` path is an error. A `replace` file also reads the
// digest sidecar beside it, named by DigestSuffix. Every other file,
// README.md among them, is ignored.
func LoadOverlay(dir string) (*Overlay, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("reading overlay dir %s: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("overlay path %s is not a directory", dir)
	}

	var files []OverlayFile
	var sidecars []string
	walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if strings.HasSuffix(d.Name(), DigestSuffix) {
			sidecars = append(sidecars, rel)
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".esc") {
			return nil
		}
		op, uri, err := classifyOverlayPath(rel)
		if err != nil {
			return err
		}
		decls, err := parseOverlayFile(path, rel)
		if err != nil {
			return err
		}
		if op == OverlayDrop {
			if err := validateDropDecls(rel, uri, decls); err != nil {
				return err
			}
		}
		file := OverlayFile{Path: rel, Op: op, PkgURI: uri, Decls: decls}
		if op == OverlayReplace {
			file.Digests, err = loadOverlayDigests(path)
			if err != nil {
				return err
			}
		}
		files = append(files, file)
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	sort.Strings(sidecars)
	return &Overlay{Dir: dir, Files: files, Sidecars: sidecars}, nil
}

// unpairedSidecars returns the digest sidecars that pair with no
// `replace` file, in path order. Nothing reads one, so leaving it would
// let a renamed or retired overlay keep a record no run ever checks. A
// run that records digests deletes them once it has applied the whole
// overlay. Every other run fails and names the first.
func (o *Overlay) unpairedSidecars() []string {
	paired := set.NewSet[string]()
	for _, f := range o.Files {
		if f.Op == OverlayReplace {
			paired.Add(digestPathFor(f.Path))
		}
	}
	var unpaired []string
	for _, rel := range o.Sidecars {
		if !paired.Contains(rel) {
			unpaired = append(unpaired, rel)
		}
	}
	return unpaired
}

// unpairedSidecarError names a sidecar with no `replace` file to pair
// with, and the file it would pair with if the overlay held one.
func unpairedSidecarError(rel string) error {
	return fmt.Errorf(
		"overlay: %s records digests for %s, which is not a replace file the "+
			"overlay holds; run `dts_to_esc generate --update-digests` to remove "+
			"the sidecar, or restore the replace file it belongs beside",
		rel, strings.TrimSuffix(rel, DigestSuffix)+".esc")
}

// classifyOverlayPath reads a file's operation and target package out of
// its overlay-relative path. The empty URI is the root drop file, which
// names no package.
func classifyOverlayPath(rel string) (OverlayOp, string, error) {
	if rel == RootDropFile {
		return OverlayDrop, "", nil
	}
	dir, base, found := strings.Cut(rel, "/")
	if !found || strings.Contains(base, "/") {
		return "", "", fmt.Errorf(
			"overlay: %s sits at neither the overlay root nor a package directory; "+
				"write a package operation as <scheme>/<package>.<add|replace|drop>.esc "+
				"and package-less drops as %s", rel, RootDropFile)
	}
	stem := strings.TrimSuffix(base, ".esc")
	name, opText, found := cutLast(stem, ".")
	if !found {
		return "", "", fmt.Errorf(
			"overlay: %s names no operation; the filename carries it, "+
				"as in %s/%s.replace.esc", rel, dir, stem)
	}
	op := OverlayOp(opText)
	switch op {
	case OverlayAdd, OverlayReplace, OverlayDrop:
	default:
		return "", "", fmt.Errorf(
			"overlay: %s names the unknown operation %q; the operations are "+
				"add, replace, and drop", rel, opText)
	}
	pkgFile := dir + "/" + name + ".esc"
	pkg, ok := PackageForFile(pkgFile)
	if !ok {
		return "", "", fmt.Errorf(
			"overlay: %s targets %s, which is not a package in the partition "+
				"table (see internal/dts_to_esc/partition.go)", rel, pkgFile)
	}
	return op, pkg.URI, nil
}

// cutLast splits s around the last instance of sep, the way strings.Cut
// splits around the first.
func cutLast(s, sep string) (before, after string, found bool) {
	i := strings.LastIndex(s, sep)
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+len(sep):], true
}

// parseOverlayFile reads and parses one overlay file. rel labels it in
// errors so a report names the path a contributor typed rather than an
// absolute one.
func parseOverlayFile(path, rel string) ([]ast.Decl, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading overlay file %s: %w", rel, err)
	}
	decls, parseErrs := parser.ParseDecls(context.Background(), &ast.Source{
		Path:     path,
		Contents: string(contents),
	})
	if len(parseErrs) > 0 {
		msgs := make([]string, 0, len(parseErrs))
		for _, e := range parseErrs {
			msgs = append(msgs, e.Message)
		}
		return nil, fmt.Errorf("overlay: %s does not parse: %s", rel, strings.Join(msgs, "; "))
	}
	for _, decl := range decls {
		if escDeclName(decl) == "" {
			return nil, fmt.Errorf(
				"overlay: %s holds a %s with no addressable name; every overlay "+
					"declaration is matched by name", rel, escDeclKind(decl))
		}
	}
	return decls, nil
}

// GlobalDrops returns the names in the root drop file. They resolve
// during routing, ahead of the partition lookup, because a whole-symbol
// drop belongs to no package.
func (o *Overlay) GlobalDrops() set.Set[string] {
	names := set.NewSet[string]()
	if o == nil {
		return names
	}
	for _, f := range o.Files {
		if f.Op != OverlayDrop || f.PkgURI != "" {
			continue
		}
		for _, decl := range f.Decls {
			names.Add(escDeclName(decl))
		}
	}
	return names
}

// FilesFor returns the overlay files that apply to one package, in the
// order a run applies them: drops first, then replacements, then
// additions. Dropping ahead of the other two means an overlay may drop a
// converted member and add its own in its place.
func (o *Overlay) FilesFor(uri string) []OverlayFile {
	if o == nil || uri == "" {
		return nil
	}
	var out []OverlayFile
	for _, op := range []OverlayOp{OverlayDrop, OverlayReplace, OverlayAdd} {
		for _, f := range o.Files {
			if f.PkgURI == uri && f.Op == op {
				out = append(out, f)
			}
		}
	}
	return out
}

// PackageURIs returns every package URI the overlay names, sorted. A
// package with an `add` file but no upstream declarations is generated
// from the overlay alone, so the generator reads this to find those.
func (o *Overlay) PackageURIs() []string {
	if o == nil {
		return nil
	}
	seen := set.NewSet[string]()
	for _, f := range o.Files {
		if f.PkgURI != "" {
			seen.Add(f.PkgURI)
		}
	}
	uris := seen.ToSlice()
	sort.Strings(uris)
	return uris
}
