package interop

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/escalier-lang/escalier/internal/printer"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/tidwall/btree"
)

// This file implements §6.4 of planning/builtins/implementation_plan.md.
// Re-running the converter against an already-committed `.esc` tree adds
// what upstream TypeScript gained and rewrites nothing else, instead of
// overwriting the tree wholesale. A second entry point runs the same
// comparison read-only, which is what CI calls.
//
// Both modes run the same diff. CheckPartition reports it and
// RegeneratePartition applies it, so the two cannot disagree about what
// a re-run would change.
//
// The exemption list `--check` needs — `globalThis`, `eval`, and the
// `intrinsic`-typed declarations of §6.1 — is not restated here. Every
// one of those names is in ExplicitDrops, so Route discards it before
// PartitionLib buckets anything. The diff compares converter output
// against the committed file, which means a dropped declaration has no
// converted counterpart to be reported missing against. The drop list
// keeps exactly one copy: the one the routing pass reads.
//
// §6.4 states check 1 over declarations. Members are checked on the
// same footing here, because the write mode adds them and a check that
// stayed silent about them would tell CI a bump was clean while
// `regenerate` still had methods to write.
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
	// was not, NewDecls holds every converted declaration and the write
	// pass creates the file.
	Exists bool

	// NewDecls are converted declarations with no committed
	// counterpart of the same name.
	NewDecls []NewDecl

	// NewMembers are converted class or interface members missing from
	// the committed declaration that shares their owner's name.
	NewMembers []NewMember

	// Removed names committed declarations absent from the converted
	// output, usually a TS-side removal. Neither mode deletes anything.
	Removed []string
}

// Empty reports whether a re-run would leave the file untouched.
func (d *PackageDiff) Empty() bool {
	return len(d.NewDecls) == 0 && len(d.NewMembers) == 0
}

// NewDecl is one converted declaration the committed file lacks.
type NewDecl struct {
	Name string

	// Kind is the declaration's Escalier form: "class", "interface",
	// "type", "function", "value", or "namespace".
	Kind string

	// Text is the rendered Escalier source for the declaration,
	// including its JSDoc comment and `@js("...")` decorator.
	Text string
}

// NewMember is one converted member the committed declaration lacks.
type NewMember struct {
	// Owner is the name of the committed declaration the member is
	// added to.
	Owner string

	// Name is the member's key: an identifier, a string-literal key, or
	// a dotted computed key such as "Symbol.iterator". A constructor is
	// named "constructor".
	Name string

	// Static reports whether the member lives on the static side.
	Static bool

	// Text is the rendered Escalier source for the member alone, with
	// no indent and no trailing separator.
	Text string
}

// Label renders a member as it appears in a report, e.g.
// "Array.isArray (static)".
func (m *NewMember) Label() string {
	if m.Static {
		return fmt.Sprintf("%s.%s (static)", m.Owner, m.Name)
	}
	return fmt.Sprintf("%s.%s", m.Owner, m.Name)
}

// CheckReport is the outcome of a read-only `--check` run: one
// PackageDiff per package that produced a bucket, in package-URI order.
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
	runs, err := diffPartition(result, escDir)
	if err != nil {
		return nil, err
	}
	report := &CheckReport{Packages: make([]PackageDiff, 0, len(runs))}
	for _, run := range runs {
		report.Packages = append(report.Packages, run.diff)
	}
	return report, nil
}

// Failed reports whether the run found a `.d.ts` declaration or member
// with no `.esc` counterpart. Extra declarations on the `.esc` side do
// not fail the check — §6.4 reports them and leaves the deletion
// decision to a contributor.
func (r *CheckReport) Failed() bool {
	for i := range r.Packages {
		if !r.Packages[i].Empty() {
			return true
		}
	}
	return false
}

// Counts returns the number of missing declarations, missing members,
// and informational removals across every package.
func (r *CheckReport) Counts() (decls, members, removed int) {
	for i := range r.Packages {
		p := &r.Packages[i]
		decls += len(p.NewDecls)
		members += len(p.NewMembers)
		removed += len(p.Removed)
	}
	return decls, members, removed
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
		for j := range p.NewMembers {
			if _, err := fmt.Fprintf(w, "  missing member: %s\n", p.NewMembers[j].Label()); err != nil {
				return err
			}
		}
		for _, name := range p.Removed {
			if _, err := fmt.Fprintf(w, "  extra declaration: %s (absent from the .d.ts; not removed)\n", name); err != nil {
				return err
			}
		}
	}
	decls, members, removed := r.Counts()
	if _, err := fmt.Fprintf(w,
		"check: %d missing declarations, %d missing members, %d extra declarations\n",
		decls, members, removed); err != nil {
		return err
	}
	_, err := fmt.Fprint(w,
		"note: signature and property-type drift are not checked yet; "+
			"those compare both sides through the solver's constrain "+
			"(SimpleSub M7.5)\n")
	return err
}

// RegenReport is the outcome of an additive write run, one entry per
// package that produced a bucket, in package-URI order.
type RegenReport struct {
	Packages []RegenResult
}

// RegenResult is what the write pass did to one package's file.
type RegenResult struct {
	Pkg  string
	Path string

	// Created is true when the file was absent and written from
	// scratch.
	Created bool

	// AddedDecls and AddedMembers count what the pass appended.
	AddedDecls   int
	AddedMembers int

	// Skipped names declarations whose body braces the pass could not
	// locate in the committed source, so their missing members were
	// left alone rather than spliced blind.
	Skipped []string

	// Removed names committed declarations absent from the `.d.ts`
	// side. Reported, never deleted.
	Removed []string
}

// RegeneratePartition applies the additive half of §6.4 to the
// committed `.esc` tree rooted at escDir: it adds declarations and
// members the `.d.ts` has and the tree lacks, and rewrites nothing
// else. An existing declaration's body, signature, decorators, and
// hand-added annotations are left byte-for-byte intact, so the §7
// hand-edits survive a re-run.
//
// A package whose file is absent is written in full — everything in it
// is missing.
func RegeneratePartition(result *PartitionResult, escDir string) (*RegenReport, error) {
	runs, err := diffPartition(result, escDir)
	if err != nil {
		return nil, err
	}
	report := &RegenReport{Packages: make([]RegenResult, 0, len(runs))}
	for i := range runs {
		res, err := runs[i].apply(escDir)
		if err != nil {
			return nil, err
		}
		report.Packages = append(report.Packages, res)
	}
	return report, nil
}

// Write prints one line per package the pass changed, then a summary.
func (r *RegenReport) Write(w io.Writer) error {
	var decls, members int
	for i := range r.Packages {
		p := &r.Packages[i]
		decls += p.AddedDecls
		members += p.AddedMembers
		if p.AddedDecls == 0 && p.AddedMembers == 0 && len(p.Removed) == 0 && len(p.Skipped) == 0 {
			continue
		}
		verb := "updated"
		if p.Created {
			verb = "created"
		}
		if _, err := fmt.Fprintf(w, "%s %s (%s): +%d declarations, +%d members\n",
			verb, p.Pkg, p.Path, p.AddedDecls, p.AddedMembers); err != nil {
			return err
		}
		for _, name := range p.Skipped {
			if _, err := fmt.Fprintf(w,
				"  skipped %s: could not locate its body in the committed source\n",
				name); err != nil {
				return err
			}
		}
		for _, name := range p.Removed {
			if _, err := fmt.Fprintf(w,
				"  %s is absent from the .d.ts; left in place for review\n", name); err != nil {
				return err
			}
		}
	}
	_, err := fmt.Fprintf(w, "regenerate: +%d declarations, +%d members\n", decls, members)
	return err
}

// packageRerun holds everything both modes need for one package: the
// diff they report or apply, plus the committed source and converted
// declarations the write pass splices from.
type packageRerun struct {
	diff PackageDiff

	// contents is the committed file's source, "" when it was absent.
	// Every offset the write pass computes indexes into this string.
	contents string

	// inserts pairs each missing member with the committed declaration
	// it belongs on, in the order the members appear in the converted
	// declaration.
	inserts []memberInsert
}

// memberInsert is one missing member plus the committed declaration the
// write pass splices it into.
type memberInsert struct {
	// owner is the committed declaration whose body receives the
	// member; its span locates the braces to splice between.
	owner ast.Decl

	// ownerName is that declaration's name, kept for diagnostics — a
	// declaration whose braces cannot be located is reported by name.
	ownerName string

	// text is the rendered member, with no indent and no separator.
	text string
}

// diffPartition converts every bucket and diffs it against the
// committed tree. Buckets are visited in package-URI order so both
// modes report in the same sequence regardless of map iteration.
func diffPartition(result *PartitionResult, escDir string) ([]packageRerun, error) {
	uris := make([]string, 0, len(result.Buckets))
	for uri := range result.Buckets {
		uris = append(uris, uri)
	}
	sort.Strings(uris)

	runs := make([]packageRerun, 0, len(uris))
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
		run, err := diffPackage(pkg, mod, escDir)
		if err != nil {
			return nil, err
		}
		runs = append(runs, *run)
	}
	return runs, nil
}

// diffPackage compares one converted bucket against its committed file.
func diffPackage(pkg Package, mod *StandaloneModule, escDir string) (*packageRerun, error) {
	dest := filepath.Join(escDir, filepath.FromSlash(pkg.File))
	contents, exists, err := readCommitted(dest)
	if err != nil {
		return nil, err
	}

	run := &packageRerun{
		diff: PackageDiff{
			Pkg:    pkg.URI,
			Path:   pkg.File,
			Exists: exists,
		},
		contents: contents,
	}

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

	converted := standaloneDecls(mod)
	convertedNames := set.NewSet[string]()
	for _, decl := range converted {
		name := escDeclName(decl)
		if name == "" {
			continue
		}
		convertedNames.Add(name)

		if !spaceCovered(byName[name], decl) {
			text, err := renderStandaloneDecl(mod, decl)
			if err != nil {
				return nil, fmt.Errorf("rendering %s in %s: %w", name, pkg.URI, err)
			}
			run.diff.NewDecls = append(run.diff.NewDecls, NewDecl{
				Name: name,
				Kind: escDeclKind(decl),
				Text: text,
			})
			continue
		}

		host := memberHost(byName[name], decl)
		if host == nil {
			continue
		}
		members, err := missingMembers(name, host, decl)
		if err != nil {
			return nil, fmt.Errorf("comparing members of %s in %s: %w", name, pkg.URI, err)
		}
		for _, m := range members {
			run.diff.NewMembers = append(run.diff.NewMembers, m)
			run.inserts = append(run.inserts, memberInsert{
				owner:     host,
				ownerName: name,
				text:      m.Text,
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
		run.diff.Removed = append(run.diff.Removed, name)
	}
	return run, nil
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

// renderStandaloneDecl prints one converted declaration in the same
// form WriteStandaloneModule would emit it, JSDoc included. The result
// ends with a newline.
func renderStandaloneDecl(mod *StandaloneModule, decl ast.Decl) (string, error) {
	var namespaces btree.Map[string, *ast.Namespace]
	namespaces.Set("", &ast.Namespace{Decls: []ast.Decl{decl}})
	return RenderStandaloneModule(&StandaloneModule{
		Module: ast.NewModule(namespaces),
		Docs:   mod.Docs,
	})
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

// spaceCovered reports whether one of the committed declarations sharing a
// converted declaration's name already binds the space it needs.
//
// Overlap rather than an exact kind match is what makes a §7 hand-fusion
// stick: a contributor who collapses the converter's `interface Foo` and
// `declare var Foo: FooConstructor` into one `declare class Foo` binds
// both spaces, so neither converted half is reported missing on the next
// re-run and neither is written back.
func spaceCovered(committed []ast.Decl, converted ast.Decl) bool {
	want := escDeclSpace(converted)
	for _, decl := range committed {
		if escDeclSpace(decl)&want != 0 {
			return true
		}
	}
	return false
}

// memberHost returns the committed declaration whose members the
// converted declaration's members are compared against, or nil when the
// converted declaration has no member list or nothing of its shape was
// committed under that name.
//
// Only a same-shape pair is member-comparable. A class and an interface
// hold their members in different slots, and turning one into the other
// is a signature change that §6.4 never applies on its own.
func memberHost(committed []ast.Decl, converted ast.Decl) ast.Decl {
	switch converted.(type) {
	case *ast.ClassDecl:
		for _, decl := range committed {
			if _, ok := decl.(*ast.ClassDecl); ok {
				return decl
			}
		}
	case *ast.InterfaceDecl:
		for _, decl := range committed {
			if _, ok := decl.(*ast.InterfaceDecl); ok {
				return decl
			}
		}
	}
	return nil
}

// missingMembers returns the members of the converted declaration that
// the committed declaration of the same name does not already fill.
//
// Only classes and interfaces have members. A converted class matched
// against a committed interface (or the reverse) yields nothing: the
// two forms are not member-comparable, and rewriting one into the other
// is a signature change, which §6.4 never applies automatically.
func missingMembers(name string, committed, converted ast.Decl) ([]NewMember, error) {
	opts := printer.DefaultOptions()

	switch conv := converted.(type) {
	case *ast.ClassDecl:
		host, ok := committed.(*ast.ClassDecl)
		if !ok {
			return nil, nil
		}
		filled := set.NewSet[memberSlot]()
		for _, elem := range host.Body {
			if slot, ok := classElemSlot(elem); ok {
				filled.Add(slot)
			}
		}
		var out []NewMember
		for _, elem := range conv.Body {
			slot, ok := classElemSlot(elem)
			if !ok || filled.Contains(slot) {
				continue
			}
			filled.Add(slot)
			text, err := printer.PrintClassElem(elem, opts)
			if err != nil {
				return nil, err
			}
			out = append(out, NewMember{
				Owner: name, Name: slot.Name, Static: slot.Static, Text: text,
			})
		}
		return out, nil

	case *ast.InterfaceDecl:
		host, ok := committed.(*ast.InterfaceDecl)
		if !ok || host.TypeAnn == nil || conv.TypeAnn == nil {
			return nil, nil
		}
		filled := set.NewSet[memberSlot]()
		for _, elem := range host.TypeAnn.Elems {
			if slot, ok := objElemSlot(elem); ok {
				filled.Add(slot)
			}
		}
		var out []NewMember
		for _, elem := range conv.TypeAnn.Elems {
			slot, ok := objElemSlot(elem)
			if !ok || filled.Contains(slot) {
				continue
			}
			filled.Add(slot)
			text, err := printer.PrintObjTypeAnnElem(elem, opts)
			if err != nil {
				return nil, err
			}
			out = append(out, NewMember{
				Owner: name, Name: slot.Name, Static: slot.Static, Text: text,
			})
		}
		return out, nil
	}
	return nil, nil
}

// memberSlot identifies a member by the name it is addressed with plus
// which side of the class it lives on. Members share a slot when a
// hand-edit could have turned one into the other: a converted
// `readonly x: T` field and a committed `get x() -> T` occupy the same
// slot, so the re-run leaves the hand-written getter alone instead of
// adding a duplicate field beside it.
//
// Overload sets collapse to one slot too. Whether the committed
// overloads still cover the `.d.ts` ones is a signature question, which
// §6.4 checks 2 and 3 answer once the solver can compare them.
type memberSlot struct {
	Name   string
	Static bool
}

// classElemSlot returns the slot a class member fills. The bool is
// false for a member whose key is neither an identifier, a string
// literal, nor a dotted computed key such as `[Symbol.iterator]`. Both
// sides run through this function, so an unnameable member is invisible
// to the member diff. It is never reported and never written.
func classElemSlot(elem ast.ClassElem) (memberSlot, bool) {
	switch e := elem.(type) {
	case *ast.FieldElem:
		return slotFor(e.Name, e.Static)
	case *ast.MethodElem:
		return slotFor(e.Name, e.Static)
	case *ast.GetterElem:
		return slotFor(e.Name, e.Static)
	case *ast.SetterElem:
		return slotFor(e.Name, e.Static)
	case *ast.ConstructorElem:
		return memberSlot{Name: "constructor"}, true
	}
	return memberSlot{}, false
}

// objElemSlot is classElemSlot for an interface or object-type member.
// Call and construct signatures carry no name, so the diff passes over
// them the same way it passes over an unnameable key.
func objElemSlot(elem ast.ObjTypeAnnElem) (memberSlot, bool) {
	switch e := elem.(type) {
	case *ast.PropertyTypeAnn:
		return slotFor(e.Name, false)
	case *ast.MethodTypeAnn:
		return slotFor(e.Name, false)
	case *ast.GetterTypeAnn:
		return slotFor(e.Name, false)
	case *ast.SetterTypeAnn:
		return slotFor(e.Name, false)
	}
	return memberSlot{}, false
}

// slotFor builds a memberSlot from a member key, reporting false for a
// key with no stable textual name.
func slotFor(key ast.ObjKey, static bool) (memberSlot, bool) {
	switch k := key.(type) {
	case *ast.IdentExpr:
		return memberSlot{Name: k.Name, Static: static}, true
	case *ast.StrLit:
		return memberSlot{Name: k.Value, Static: static}, true
	case *ast.ComputedKey:
		if dotted := astExprDottedName(k.Expr); dotted != "" {
			return memberSlot{Name: dotted, Static: static}, true
		}
	}
	return memberSlot{}, false
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

// apply writes this package's additions to disk and returns what it
// did. A package with nothing missing is not rewritten, so an
// unchanged file keeps its mtime.
func (r *packageRerun) apply(escDir string) (RegenResult, error) {
	res := RegenResult{
		Pkg:     r.diff.Pkg,
		Path:    r.diff.Path,
		Removed: r.diff.Removed,
	}
	if r.diff.Empty() {
		return res, nil
	}
	res.Created = !r.diff.Exists

	dest := filepath.Join(escDir, filepath.FromSlash(r.diff.Path))
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return res, fmt.Errorf("creating package dir for %s: %w", r.diff.Pkg, err)
	}

	updated, inserted, skipped := r.splice()
	if err := os.WriteFile(dest, []byte(updated), 0o644); err != nil {
		return res, fmt.Errorf("writing %s: %w", dest, err)
	}

	res.AddedDecls = len(r.diff.NewDecls)
	res.AddedMembers = inserted
	res.Skipped = skipped
	return res, nil
}

// splice produces the new file contents: missing members spliced into
// the bodies they belong on, then missing declarations appended. Every
// byte the committed file already held is carried over unchanged.
//
// `inserted` counts the members that landed. `skipped` names the
// declarations whose body braces could not be located; their members
// were left out rather than spliced blind.
func (r *packageRerun) splice() (out string, inserted int, skipped []string) {
	type edit struct {
		at   int
		text string
	}

	// Group members by owner so one declaration takes one insertion.
	order := make([]ast.Decl, 0, len(r.inserts))
	byOwner := make(map[ast.Decl][]string, len(r.inserts))
	names := make(map[ast.Decl]string, len(r.inserts))
	for _, ins := range r.inserts {
		if _, seen := byOwner[ins.owner]; !seen {
			order = append(order, ins.owner)
			names[ins.owner] = ins.ownerName
		}
		byOwner[ins.owner] = append(byOwner[ins.owner], ins.text)
	}

	var edits []edit
	for _, owner := range order {
		at, brace, ok := bodyInsertPoint(r.contents, owner)
		if !ok {
			skipped = append(skipped, names[owner])
			continue
		}
		inserted += len(byOwner[owner])
		edits = append(edits, edit{
			at:   at,
			text: memberInsertText(r.contents, at, brace, byOwner[owner]),
		})
	}

	// Apply from the end backwards so each offset still indexes into
	// the text the spans were measured against.
	sort.Slice(edits, func(i, j int) bool { return edits[i].at > edits[j].at })
	out = r.contents
	for _, e := range edits {
		out = out[:e.at] + e.text + out[e.at:]
	}

	if len(r.diff.NewDecls) > 0 {
		var sb strings.Builder
		sb.WriteString(out)
		if out != "" && !strings.HasSuffix(out, "\n") {
			sb.WriteString("\n")
		}
		for i, d := range r.diff.NewDecls {
			if out != "" || i > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(d.Text)
		}
		out = sb.String()
	}
	return out, inserted, skipped
}

// bodyInsertPoint locates where a new member goes inside a
// declaration's braces. It returns the offset just past the last
// non-whitespace byte inside the body and the offset of the closing
// brace itself, or false when the declaration does not end in a brace
// — a shape the splice cannot reason about, so the caller leaves it
// alone.
func bodyInsertPoint(contents string, decl ast.Decl) (at, brace int, ok bool) {
	end := offsetOf(contents, decl.Span().End)
	if end <= 0 || end > len(contents) || contents[end-1] != '}' {
		return 0, 0, false
	}
	brace = end - 1
	at = brace
	for at > 0 && isSpaceByte(contents[at-1]) {
		at--
	}
	return at, brace, true
}

// memberInsertText builds the text spliced in at `at`, where `brace` is
// the offset of the body's closing brace. Each member goes on its own
// line with a trailing comma; a comma is prepended when the member
// before it lacks one, and a newline is appended when the closing brace
// shared the line with the body's last member.
func memberInsertText(contents string, at, brace int, members []string) string {
	const indent = "    "
	var sb strings.Builder
	if at > 0 && contents[at-1] != ',' && contents[at-1] != '{' {
		sb.WriteString(",")
	}
	for _, m := range members {
		sb.WriteString("\n")
		sb.WriteString(indentLines(m, indent))
		sb.WriteString(",")
	}
	if !strings.Contains(contents[at:brace], "\n") {
		sb.WriteString("\n")
	}
	return sb.String()
}

// indentLines prefixes every line of text with indent. A member printed
// on its own starts at column 1; splicing it into a body puts it one
// level in.
func indentLines(text, indent string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if line == "" {
			continue
		}
		lines[i] = indent + line
	}
	return strings.Join(lines, "\n")
}

func isSpaceByte(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// offsetOf converts a parser location into a byte offset into contents.
// Lines and columns are both 1-based and columns count runes, matching
// the lexer. A location past the end of contents returns len(contents).
func offsetOf(contents string, loc ast.Location) int {
	line, column := 1, 1
	for i, r := range contents {
		if line == loc.Line && column == loc.Column {
			return i
		}
		if r == '\n' {
			line++
			column = 1
		} else {
			column++
		}
	}
	return len(contents)
}
