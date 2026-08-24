package dts_to_esc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/escalier-lang/escalier/internal/set"
)

// This file implements the read-only half of §6.4 of
// planning/builtins/implementation_plan.md: verifying that a committed
// `.esc` tree still covers every declaration the pinned `.d.ts` set
// produces. CI runs it after a TypeScript bump to find out what
// upstream gained.
//
// The exemption list check 1 needs — `globalThis`, `eval`, and the
// `intrinsic`-typed declarations of §6.1 — is not restated here. Every
// one of those names is in ExplicitDrops, so Route discards it before
// PartitionLib buckets anything. The diff compares converter output
// against the committed file, which means a dropped declaration has no
// converted counterpart to be reported missing against. The drop list
// keeps exactly one copy: the one the routing pass reads.
//
// Signature drift (§6.4 checks 2 and 3) is not implemented. It compares
// the two sides through the solver's `constrain`, which needs the
// solver to ingest a declaration module — SimpleSub M7.5. Until then a
// passing check means "every `.d.ts` name has an `.esc` counterpart",
// not "every counterpart still means the same thing".

// PackageDiff is what a converter re-run would add to one package's
// committed `.esc` file.
type PackageDiff struct {
	// Pkg is the package URI, e.g. "std:array".
	Pkg string

	// Path is the committed file's path relative to the `.esc` tree
	// root, e.g. "std/array.esc".
	Path string

	// Exists reports whether the committed file was on disk. When it
	// was not, NewDecls holds every converted declaration.
	Exists bool

	// NewDecls are converted declarations with no committed
	// counterpart of the same name.
	NewDecls []NewDecl

	// Removed names committed declarations absent from the converted
	// output, usually a TS-side removal. Nothing is ever deleted.
	Removed []string
}

// Empty reports whether the committed file already covers everything
// the converter produced.
func (d *PackageDiff) Empty() bool {
	return len(d.NewDecls) == 0
}

// NewDecl is one converted declaration the committed file lacks.
type NewDecl struct {
	Name string

	// Kind is the declaration's Escalier form: "class", "interface",
	// "type", "function", "value", or "namespace".
	Kind string
}

// CheckReport is the outcome of a read-only check run: one PackageDiff
// per package that produced a bucket, in package-URI order.
type CheckReport struct {
	Packages []PackageDiff
}

// CheckPartition converts every bucket in result and diffs it against
// the committed `.esc` tree rooted at escDir, without touching disk.
//
// escDir is the directory holding the `std/`, `web/`, and `node/`
// subtrees — internal/interop/data/ in the repo, the same root
// WritePartitionedTree writes into.
func CheckPartition(result *PartitionResult, escDir string) (*CheckReport, error) {
	diffs, err := diffPartition(result, escDir)
	if err != nil {
		return nil, err
	}
	return &CheckReport{Packages: diffs}, nil
}

// Failed reports whether the run found a `.d.ts` declaration with no
// `.esc` counterpart. Extra declarations on the `.esc` side do not fail
// the check — §6.4 reports them and leaves the deletion decision to a
// contributor.
func (r *CheckReport) Failed() bool {
	for i := range r.Packages {
		if !r.Packages[i].Empty() {
			return true
		}
	}
	return false
}

// Counts returns the number of missing declarations and informational
// removals across every package.
func (r *CheckReport) Counts() (decls, removed int) {
	for i := range r.Packages {
		p := &r.Packages[i]
		decls += len(p.NewDecls)
		removed += len(p.Removed)
	}
	return decls, removed
}

// Write prints the report to w, one section per package with findings,
// then a summary. The footer names the drift checks that are not
// implemented so a passing run is not read as full coverage.
func (r *CheckReport) Write(w io.Writer) error {
	for i := range r.Packages {
		p := &r.Packages[i]
		if p.Empty() && len(p.Removed) == 0 {
			continue
		}
		if _, err := fmt.Fprintf(w, "%s (%s)\n", p.Pkg, p.Path); err != nil {
			return err
		}
		if !p.Exists {
			if _, err := fmt.Fprintf(w, "  missing file\n"); err != nil {
				return err
			}
		}
		for _, d := range p.NewDecls {
			if _, err := fmt.Fprintf(w, "  missing declaration: %s (%s)\n", d.Name, d.Kind); err != nil {
				return err
			}
		}
		for _, name := range p.Removed {
			if _, err := fmt.Fprintf(w, "  extra declaration: %s (absent from the .d.ts; not removed)\n", name); err != nil {
				return err
			}
		}
	}
	decls, removed := r.Counts()
	if _, err := fmt.Fprintf(w,
		"check: %d missing declarations, %d extra declarations\n",
		decls, removed); err != nil {
		return err
	}
	_, err := fmt.Fprint(w,
		"note: signature and property-type drift are not checked yet; "+
			"those compare both sides through the solver's constrain "+
			"(SimpleSub M7.5)\n")
	return err
}

// diffPartition converts every bucket and diffs it against the
// committed tree. Buckets are visited in package-URI order so the
// report reads the same way on every run regardless of map iteration.
func diffPartition(result *PartitionResult, escDir string) ([]PackageDiff, error) {
	uris := make([]string, 0, len(result.Buckets))
	for uri := range result.Buckets {
		uris = append(uris, uri)
	}
	sort.Strings(uris)

	diffs := make([]PackageDiff, 0, len(uris))
	for _, uri := range uris {
		pkg, ok := PackageForURI(uri)
		if !ok {
			return nil, fmt.Errorf("diffPartition: unknown package URI %q "+
				"(every bucket should come from Route, which only returns "+
				"URIs in PackageList)", uri)
		}
		mod, err := ConvertBucket(result.Buckets[uri])
		if err != nil {
			return nil, fmt.Errorf("converting bucket %s: %w", uri, err)
		}
		diff, err := diffPackage(pkg, mod, escDir)
		if err != nil {
			return nil, err
		}
		diffs = append(diffs, *diff)
	}
	return diffs, nil
}

// diffPackage compares one converted bucket against its committed file.
func diffPackage(pkg Package, mod *StandaloneModule, escDir string) (*PackageDiff, error) {
	dest := filepath.Join(escDir, filepath.FromSlash(pkg.File))
	contents, exists, err := readCommitted(dest)
	if err != nil {
		return nil, err
	}

	diff := &PackageDiff{Pkg: pkg.URI, Path: pkg.File, Exists: exists}

	var committed []ast.Decl
	if exists {
		committed, err = parseCommitted(dest, contents)
		if err != nil {
			return nil, err
		}
	}

	byName := make(map[string][]ast.Decl, len(committed))
	for _, decl := range committed {
		if name := escDeclName(decl); name != "" {
			byName[name] = append(byName[name], decl)
		}
	}

	convertedNames := set.NewSet[string]()
	for _, decl := range standaloneDecls(mod) {
		name := escDeclName(decl)
		if name == "" {
			continue
		}
		convertedNames.Add(name)
		if !spaceCovered(byName[name], decl) {
			diff.NewDecls = append(diff.NewDecls, NewDecl{
				Name: name,
				Kind: escDeclKind(decl),
			})
		}
	}

	// A name can be committed twice — once in each space — so report it
	// at most once.
	reported := set.NewSet[string]()
	for _, decl := range committed {
		name := escDeclName(decl)
		if name == "" || convertedNames.Contains(name) || reported.Contains(name) {
			continue
		}
		reported.Add(name)
		diff.Removed = append(diff.Removed, name)
	}
	return diff, nil
}

// readCommitted reads a committed `.esc` file. A file that is not on
// disk is not an error — every declaration in its package is missing,
// which is exactly what a first run against an unseeded tree means.
func readCommitted(path string) (string, bool, error) {
	contents, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("reading %s: %w", path, err)
	}
	return string(contents), true, nil
}

// parseCommitted parses a committed `.esc` file into its top-level
// declarations. ParseLibFiles is used rather than ParseDecls because a
// package file may open with `import "std:date"` and only ParseLibFiles
// admits import statements.
func parseCommitted(path, contents string) ([]ast.Decl, error) {
	src := &ast.Source{Path: path, Contents: contents}
	mod, errs := parser.ParseLibFiles(context.Background(), []*ast.Source{src})
	if len(errs) > 0 {
		return nil, fmt.Errorf("parsing %s: %s", path, errs[0].String())
	}
	var decls []ast.Decl
	mod.Namespaces.Scan(func(_ string, ns *ast.Namespace) bool {
		decls = append(decls, ns.Decls...)
		return true
	})
	return decls, nil
}

// standaloneDecls returns the converted module's declarations. The
// standalone converter flattens everything into the root namespace, so
// this is one flat list.
func standaloneDecls(mod *StandaloneModule) []ast.Decl {
	var decls []ast.Decl
	mod.Module.Namespaces.Scan(func(_ string, ns *ast.Namespace) bool {
		decls = append(decls, ns.Decls...)
		return true
	})
	return decls
}

// declSpace is the namespace a declaration binds its name in. Escalier
// keeps values and types apart the way TypeScript does, so one name can
// carry both an `interface Foo` and a `declare var Foo` — the TS lib's
// class-via-trio idiom relies on exactly that.
type declSpace int

const (
	// valueSpace holds `val`/`var` bindings and functions.
	valueSpace declSpace = 1 << iota
	// typeSpace holds type aliases and interfaces.
	typeSpace
	// bothSpaces is what a class, enum, or namespace binds: one name
	// carrying a value and a type at once.
	bothSpaces = valueSpace | typeSpace
)

// escDeclSpace returns the space a declaration binds in.
func escDeclSpace(decl ast.Decl) declSpace {
	switch decl.(type) {
	case *ast.VarDecl, *ast.FuncDecl:
		return valueSpace
	case *ast.TypeDecl, *ast.InterfaceDecl:
		return typeSpace
	case *ast.ClassDecl, *ast.EnumDecl, *ast.NamespaceDecl:
		return bothSpaces
	}
	return 0
}

// spaceCovered reports whether one of the committed declarations
// sharing a converted declaration's name already binds the space it
// needs.
//
// Overlap rather than an exact kind match is what makes a §7 hand-fusion
// stick: a contributor who collapses the converter's `interface Foo` and
// `declare var Foo: FooConstructor` into one `declare class Foo` binds
// both spaces, so neither converted half is reported missing on the next
// re-run.
func spaceCovered(committed []ast.Decl, converted ast.Decl) bool {
	want := escDeclSpace(converted)
	for _, decl := range committed {
		if escDeclSpace(decl)&want != 0 {
			return true
		}
	}
	return false
}

// escDeclName returns the name a top-level declaration is addressed by,
// or "" for a declaration with no single-identifier name — a VarDecl
// bound to a destructuring pattern, say. Unnamed declarations take no
// part in the diff.
func escDeclName(decl ast.Decl) string {
	switch d := decl.(type) {
	case *ast.VarDecl:
		if id, ok := d.Pattern.(*ast.IdentPat); ok {
			return id.Name
		}
	case *ast.FuncDecl:
		if d.Name != nil {
			return d.Name.Name
		}
	case *ast.ClassDecl:
		if d.Name != nil {
			return d.Name.Name
		}
	case *ast.TypeDecl:
		if d.Name != nil {
			return d.Name.Name
		}
	case *ast.InterfaceDecl:
		if d.Name != nil {
			return d.Name.Name
		}
	case *ast.EnumDecl:
		if d.Name != nil {
			return d.Name.Name
		}
	case *ast.NamespaceDecl:
		if d.Name != nil {
			return d.Name.Name
		}
	}
	return ""
}

// escDeclKind returns the Escalier form of a declaration, for reports.
func escDeclKind(decl ast.Decl) string {
	switch decl.(type) {
	case *ast.ClassDecl:
		return "class"
	case *ast.InterfaceDecl:
		return "interface"
	case *ast.TypeDecl:
		return "type"
	case *ast.FuncDecl:
		return "function"
	case *ast.VarDecl:
		return "value"
	case *ast.EnumDecl:
		return "enum"
	case *ast.NamespaceDecl:
		return "namespace"
	}
	return "declaration"
}
