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
// §6.4 states check 1 over declarations. Members are checked on the
// same footing here. The §6.4 write mode adds them, so a check that
// stayed silent about them would tell CI a bump was clean while the
// write mode still had methods to add.
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

	// Patch is the unified diff between the committed file and what a
	// regenerate run would leave in its place, empty when the two
	// already agree. Only the check mode fills it in.
	Patch string

	// Skipped names declarations whose body braces could not be located
	// in the committed source. Their missing members are in NewMembers
	// but not in Patch, since a splice into a body the pass cannot find
	// would be blind.
	Skipped []string
}

// Empty reports whether the committed file already covers everything
// the converter produced.
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
	// including its JSDoc comment and `@js("...")` decorator. The write
	// pass appends it verbatim.
	Text string
}

// NewMember is one converted member the committed declaration lacks.
type NewMember struct {
	// Owner is the name of the committed declaration the member would
	// be added to.
	Owner string

	// Name is the member's key: an identifier, a string-literal key, or
	// a dotted computed key such as "Symbol.iterator". A constructor is
	// named "constructor".
	Name string

	// Static reports whether the member lives on the static side.
	Static bool

	// Text is the rendered Escalier source for the member alone, with
	// no indent and no trailing separator. The write pass places both.
	Text string
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
//
// Each package's diff carries the patch a regenerate run would apply,
// so a contributor reads the same change here that the write mode
// would make.
func CheckPartition(result *PartitionResult, escDir string) (*CheckReport, error) {
	plans, err := planPartition(result, escDir)
	if err != nil {
		return nil, err
	}
	report := &CheckReport{Packages: make([]PackageDiff, 0, len(plans))}
	for _, plan := range plans {
		diff := plan.diff
		spliced := plan.splice()
		patch, err := unifiedDiff(diff.Path, diff.Exists, plan.contents, spliced.edits)
		if err != nil {
			return nil, fmt.Errorf("rendering the diff for %s: %w", diff.Pkg, err)
		}
		diff.Patch = patch
		diff.Skipped = spliced.skipped
		report.Packages = append(report.Packages, diff)
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

// Write prints the report to w: the unified diff for every package the
// re-run would change, then a note for each finding the diff cannot
// show, then a summary. The footer names the drift checks that are not
// implemented so a passing run is not read as full coverage.
func (r *CheckReport) Write(w io.Writer) error {
	for i := range r.Packages {
		p := &r.Packages[i]
		if _, err := fmt.Fprint(w, p.Patch); err != nil {
			return err
		}
		for _, name := range p.Skipped {
			if _, err := fmt.Fprintf(w,
				"note: %s: could not locate the body of %s; its missing members are not in the diff\n",
				p.Path, name); err != nil {
				return err
			}
		}
		for _, name := range p.Removed {
			if _, err := fmt.Fprintf(w,
				"note: %s: %s is absent from the .d.ts; the diff does not remove it\n",
				p.Path, name); err != nil {
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
	plans, err := planPartition(result, escDir)
	if err != nil {
		return nil, err
	}
	report := &RegenReport{Packages: make([]RegenResult, 0, len(plans))}
	for i := range plans {
		res, err := plans[i].apply(escDir)
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

// packagePlan is the change one package's file needs, computed but not
// yet applied. It holds the diff a check reports plus the two things a
// write needs to carry that diff out.
type packagePlan struct {
	diff PackageDiff

	// contents is the committed file's source, "" when it was absent.
	// Every offset the write pass computes indexes into this string.
	contents string

	// comments holds the comments the committed file contains, sorted by
	// start offset. The write pass consults them to splice a member before
	// a comment that trails the last member rather than after it.
	comments []*ast.Comment

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

// planPartition converts every bucket and diffs it against the
// committed tree, returning one plan per package. Buckets are visited
// in package-URI order so both modes report in the same sequence
// regardless of map iteration.
func planPartition(result *PartitionResult, escDir string) ([]packagePlan, error) {
	uris := make([]string, 0, len(result.Buckets))
	for uri := range result.Buckets {
		uris = append(uris, uri)
	}
	sort.Strings(uris)

	plans := make([]packagePlan, 0, len(uris))
	for _, uri := range uris {
		pkg, ok := PackageForURI(uri)
		if !ok {
			return nil, fmt.Errorf("planPartition: unknown package URI %q "+
				"(every bucket should come from Route, which only returns "+
				"URIs in PackageList)", uri)
		}
		mod, err := ConvertBucket(result.Buckets[uri])
		if err != nil {
			return nil, fmt.Errorf("converting bucket %s: %w", uri, err)
		}
		plan, err := planPackage(pkg, mod, escDir)
		if err != nil {
			return nil, err
		}
		plans = append(plans, *plan)
	}
	return plans, nil
}

// planPackage compares one converted bucket against its committed file
// and returns the plan for closing the gap.
func planPackage(pkg Package, mod *StandaloneModule, escDir string) (*packagePlan, error) {
	dest := filepath.Join(escDir, filepath.FromSlash(pkg.File))
	contents, exists, err := readCommitted(dest)
	if err != nil {
		return nil, err
	}

	plan := &packagePlan{
		diff:     PackageDiff{Pkg: pkg.URI, Path: pkg.File, Exists: exists},
		contents: contents,
	}
	diff := &plan.diff

	var committed []ast.Decl
	if exists {
		committed, plan.comments, err = parseCommitted(dest, contents)
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
			text, err := renderStandaloneDecl(decl)
			if err != nil {
				return nil, fmt.Errorf("rendering %s in %s: %w", name, pkg.URI, err)
			}
			diff.NewDecls = append(diff.NewDecls, NewDecl{
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
			diff.NewMembers = append(diff.NewMembers, m)
			plan.inserts = append(plan.inserts, memberInsert{
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
		diff.Removed = append(diff.Removed, name)
	}
	return plan, nil
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
// declarations and the comments it contains. ParseLibFiles is used rather
// than ParseDecls because a package file may open with `import "std:date"`
// and only ParseLibFiles admits import statements.
func parseCommitted(path, contents string) ([]ast.Decl, []*ast.Comment, error) {
	src := &ast.Source{Path: path, Contents: contents}
	mod, errs := parser.ParseLibFiles(context.Background(), []*ast.Source{src})
	if len(errs) > 0 {
		return nil, nil, fmt.Errorf("parsing %s: %s", path, errs[0].String())
	}
	var decls []ast.Decl
	mod.Namespaces.Scan(func(_ string, ns *ast.Namespace) bool {
		decls = append(decls, ns.Decls...)
		return true
	})
	return decls, mod.Comments[src.ID], nil
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
func renderStandaloneDecl(decl ast.Decl) (string, error) {
	var namespaces btree.Map[string, *ast.Namespace]
	namespaces.Set("", &ast.Namespace{Decls: []ast.Decl{decl}})
	return RenderStandaloneModule(&StandaloneModule{
		Module: ast.NewModule(namespaces),
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

// spaceCovered reports whether one of the committed declarations
// sharing a converted declaration's name already binds the space it
// needs.
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
// is a signature change, which §6.4 leaves to a contributor.
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
// the committed declaration of the same name does not already fill,
// each carrying the Escalier source the write pass splices in. `name`
// is the shared name, used to label each result.
//
// Only classes and interfaces have members, and memberHost has already
// paired the two sides by shape, so anything else yields nothing.
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
// did. A package whose contents would not change is not rewritten, so
// an unchanged file keeps its mtime.
func (p *packagePlan) apply(escDir string) (RegenResult, error) {
	res := RegenResult{
		Pkg:     p.diff.Pkg,
		Path:    p.diff.Path,
		Removed: p.diff.Removed,
	}
	if p.diff.Empty() {
		return res, nil
	}
	res.Created = !p.diff.Exists

	dest := filepath.Join(escDir, filepath.FromSlash(p.diff.Path))
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return res, fmt.Errorf("creating package dir for %s: %w", p.diff.Pkg, err)
	}

	spliced := p.splice()
	updated := applyEdits(p.contents, spliced.edits)
	if p.diff.Exists && updated == p.contents {
		// Every owner was skipped and nothing was appended, so there is
		// no byte to write. Rewriting identical contents would move the
		// file's mtime for no change.
		res.Skipped = spliced.skipped
		return res, nil
	}
	if err := os.WriteFile(dest, []byte(updated), 0o644); err != nil {
		return res, fmt.Errorf("writing %s: %w", dest, err)
	}

	res.AddedDecls = len(p.diff.NewDecls)
	res.AddedMembers = spliced.inserted
	res.Skipped = spliced.skipped
	return res, nil
}

// spliceResult is the change a re-run would make to one committed file,
// worked out but not applied. The check mode renders it and the write
// mode carries it out.
type spliceResult struct {
	// edits are the insertions, each an offset into the committed
	// source and the bytes that go in there.
	edits []textEdit

	// inserted counts the members that landed.
	inserted int

	// skipped names the declarations whose body braces could not be
	// located; their members were left out rather than spliced blind.
	skipped []string
}

// splice works out where every missing member goes in the body it
// belongs on and where the missing declarations go on the end. It
// builds no text of its own, so the check mode renders the result
// without assembling the file the write mode would.
func (p *packagePlan) splice() spliceResult {
	var res spliceResult

	// Group members by owner so one declaration takes one insertion.
	order := make([]ast.Decl, 0, len(p.inserts))
	byOwner := make(map[ast.Decl][]string, len(p.inserts))
	names := make(map[ast.Decl]string, len(p.inserts))
	for _, ins := range p.inserts {
		if _, seen := byOwner[ins.owner]; !seen {
			order = append(order, ins.owner)
			names[ins.owner] = ins.ownerName
		}
		byOwner[ins.owner] = append(byOwner[ins.owner], ins.text)
	}

	for _, owner := range order {
		at, brace, ok := bodyInsertPoint(p.contents, p.comments, owner)
		if !ok {
			res.skipped = append(res.skipped, names[owner])
			continue
		}
		res.inserted += len(byOwner[owner])
		res.edits = append(res.edits, textEdit{
			at:   at,
			text: memberInsertText(p.contents, at, brace, byOwner[owner]),
		})
	}
	if text := p.appendedDecls(); text != "" {
		res.edits = append(res.edits, textEdit{at: len(p.contents), text: text})
	}
	return res
}

// applyEdits inserts every edit into contents, producing the file the
// write pass leaves on disk. The edits go in from the end backwards so
// each offset still indexes into the text the spans were measured
// against.
func applyEdits(contents string, edits []textEdit) string {
	ordered := make([]textEdit, len(edits))
	copy(ordered, edits)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].at > ordered[j].at })
	out := contents
	for _, e := range ordered {
		out = out[:e.at] + e.text + out[e.at:]
	}
	return out
}

// appendedDecls renders the missing declarations as the block that goes
// on the end of the committed file, blank lines and all. A committed
// file that does not end with a newline gets one first, so the appended
// block starts on a line of its own. The result is "" when nothing is
// missing.
func (p *packagePlan) appendedDecls() string {
	if len(p.diff.NewDecls) == 0 {
		return ""
	}
	var sb strings.Builder
	if p.contents != "" && !strings.HasSuffix(p.contents, "\n") {
		sb.WriteString("\n")
	}
	for i, d := range p.diff.NewDecls {
		if p.contents != "" || i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(d.Text)
	}
	return sb.String()
}

// bodyInsertPoint locates where a new member goes inside a
// declaration's braces. It returns the offset just past the body's last
// byte of actual code and the offset of the closing brace itself, or
// false when the declaration does not end in a brace — a shape the
// splice cannot reason about, so the caller leaves it alone.
//
// The insertion point sits before any comment that trails the last
// member, not before the closing brace. A body ending in
// `// TODO: revisit` would otherwise take the separating comma inside
// that comment, rewriting a hand-written line and, when the last member
// carries no trailing comma of its own, leaving the body unparseable.
func bodyInsertPoint(contents string, comments []*ast.Comment, decl ast.Decl) (at, brace int, ok bool) {
	start := decl.Span().Start.Offset
	end := decl.Span().End.Offset
	if end <= 0 || end > len(contents) || contents[end-1] != '}' {
		return 0, 0, false
	}
	brace = end - 1
	return lastCodeOffset(contents, comments, start, brace), brace, true
}

// lastCodeOffset returns the offset just past the last byte of code in
// contents[from:to], meaning the position ahead of the comments and
// whitespace that trail it. It returns `from` when the range holds
// nothing but comments and whitespace.
//
// comments are the file's comments as the lexer read them, so a `//` inside a
// string literal never opens one here. The write pass supplies the comments
// parseCommitted collected. Those came from the parser's own lexer, so a `//`
// inside a template literal is excluded too.
func lastCodeOffset(contents string, comments []*ast.Comment, from, to int) int {
	inRange := ast.CommentsInRange(comments, from, to)
	at := to
	for {
		at = from + len(strings.TrimRight(contents[from:at], " \t\r\n"))
		// A line comment's token runs to the newline, so it holds whatever
		// spaces were written after its text. Trimming whitespace therefore
		// lands inside that comment rather than at its end. The step back
		// tests for a comment containing `at`, not for one ending there.
		last := len(inRange) - 1
		for last >= 0 && inRange[last].Span().Start.Offset >= at {
			last--
		}
		if last < 0 || inRange[last].Span().End.Offset < at {
			return at
		}
		at = inRange[last].Span().Start.Offset
		inRange = inRange[:last]
	}
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

