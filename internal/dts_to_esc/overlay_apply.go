package dts_to_esc

import (
	"fmt"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/tidwall/btree"
)

// ApplyOverlay folds every package-scoped overlay operation into the
// converted modules, in place. The package-less drops in the overlay's
// root file are not handled here — they resolve during routing, ahead of
// the partition lookup, so PartitionLibWithOverlay applies those.
//
// A package named by an `add` file that routed no upstream declaration
// is created from the overlay alone, so an addition is never silently
// lost. A `replace` or `drop` naming such a package fails, since there
// is nothing to stand in for or remove.
//
// An overlay entry naming a declaration or member the converted module
// does not have fails the run and names it. That is how a removal on the
// TypeScript side reaches a contributor, keyed on the overlay rather
// than on the tree the run overwrites.
func ApplyOverlay(mods map[string]*StandaloneModule, o *Overlay) error {
	for _, uri := range o.PackageURIs() {
		files := o.FilesFor(uri)
		mod, ok := mods[uri]
		if !ok {
			var err error
			mod, err = emptyModuleFor(uri, files)
			if err != nil {
				return err
			}
			mods[uri] = mod
		}
		for _, f := range files {
			if err := applyOverlayFile(mod, f); err != nil {
				return err
			}
		}
	}
	return nil
}

// emptyModuleFor builds the module an overlay-only package starts from.
// Only `add` can fill one, so a `replace` or `drop` on a package with no
// upstream declarations is an error rather than an empty file.
func emptyModuleFor(uri string, files []OverlayFile) (*StandaloneModule, error) {
	for _, f := range files {
		if f.Op != OverlayAdd {
			return nil, fmt.Errorf(
				"overlay: %s %ss in %s, which no upstream declaration routes to",
				f.Path, f.Op, uri)
		}
	}
	var namespaces btree.Map[string, *ast.Namespace]
	namespaces.Set("", &ast.Namespace{})
	return &StandaloneModule{
		Module: ast.NewModule(namespaces),
		Paths:  map[ast.Decl]string{},
	}, nil
}

// applyOverlayFile applies one file's declarations to the module of the
// package it targets.
func applyOverlayFile(mod *StandaloneModule, f OverlayFile) error {
	ns, ok := mod.Module.Namespaces.Get("")
	if !ok {
		return fmt.Errorf("overlay: %s targets %s, whose converted module has no root namespace",
			f.Path, f.PkgURI)
	}
	if f.Op == OverlayDrop {
		return applyDropFile(ns, f)
	}
	for _, ovDecl := range f.Decls {
		if err := applyOverlayDecl(mod, ns, f, ovDecl); err != nil {
			return err
		}
	}
	return nil
}

// applyDropFile removes the declarations and members one package's drop
// files name. A name the converted module does not have fails the run.
func applyDropFile(ns *ast.Namespace, f OverlayFile) error {
	plan := newDropPlan([]OverlayFile{f})
	if plan.empty() {
		return nil
	}

	kept := make([]ast.Decl, 0, len(ns.Decls))
	dropped := set.NewSet[string]()
	for _, decl := range ns.Decls {
		name := escDeclName(decl)
		if plan.decls.Contains(name) {
			dropped.Add(name)
			continue
		}
		kept = append(kept, decl)
	}
	for _, name := range sortedNames(plan.decls) {
		if !dropped.Contains(name) {
			return fmt.Errorf(
				"overlay: %s drops %s, which %s does not declare",
				f.Path, name, f.PkgURI)
		}
	}
	ns.Decls = kept

	for _, owner := range sortedOwners(plan.members) {
		host := findDecl(ns.Decls, owner)
		if host == nil {
			return fmt.Errorf(
				"overlay: %s drops members of %s, which %s does not declare",
				f.Path, owner, f.PkgURI)
		}
		if err := dropDeclMembers(f, owner, host, plan.members[owner]); err != nil {
			return err
		}
	}
	return nil
}

// dropDeclMembers removes every member of host whose name is in names.
// A name takes its whole overload set with it, and reaches a static
// member as readily as an instance one. A name that matches nothing
// fails the run.
func dropDeclMembers(f OverlayFile, owner string, host ast.Decl, names set.Set[string]) error {
	hit := set.NewSet[string]()
	switch d := host.(type) {
	case *ast.ClassDecl:
		d.Body = dropByName(d.Body, classElemSlot, names, hit)
	case *ast.InterfaceDecl:
		if d.TypeAnn != nil {
			d.TypeAnn.Elems = dropByName(d.TypeAnn.Elems, objElemSlot, names, hit)
		}
	default:
		return fmt.Errorf(
			"overlay: %s drops members of the %s %s, which holds none",
			f.Path, escDeclKind(host), owner)
	}
	for _, name := range sortedNames(names) {
		if !hit.Contains(name) {
			return fmt.Errorf(
				"overlay: %s drops %s.%s, which the converted declaration does not have",
				f.Path, owner, name)
		}
	}
	return nil
}

// dropByName keeps the members whose name is absent from names, and
// records in hit every name it removed at least one member for.
func dropByName[E any](
	members []E,
	slotOf func(E) (memberSlot, bool),
	names set.Set[string],
	hit set.Set[string],
) []E {
	kept := make([]E, 0, len(members))
	for _, m := range members {
		slot, ok := slotOf(m)
		if ok && names.Contains(slot.Name) {
			hit.Add(slot.Name)
			continue
		}
		kept = append(kept, m)
	}
	return kept
}

// applyOverlayDecl applies one `add` or `replace` declaration.
//
// A declaration the converted module does not have is appended by `add`
// and fails the run under `replace`. One it does have is merged member
// by member when the two agree on declaration kind and that kind holds
// members. Every other pair is a whole-declaration replacement, which is
// how a shape the converter gets structurally wrong is corrected — an
// `interface` the runtime exposes as a class, say.
func applyOverlayDecl(mod *StandaloneModule, ns *ast.Namespace, f OverlayFile, ovDecl ast.Decl) error {
	name := escDeclName(ovDecl)
	idx := findDeclIndex(ns.Decls, name)
	if idx < 0 {
		if f.Op == OverlayReplace {
			return fmt.Errorf(
				"overlay: %s replaces %s, which %s does not declare",
				f.Path, name, f.PkgURI)
		}
		ns.Decls = append(ns.Decls, ovDecl)
		return nil
	}

	host := ns.Decls[idx]
	switch hostDecl := host.(type) {
	case *ast.ClassDecl:
		if ovClass, ok := ovDecl.(*ast.ClassDecl); ok {
			body, err := mergeMembers(f, name, hostDecl.Body, ovClass.Body, classElemSlot)
			if err != nil {
				return err
			}
			hostDecl.Body = body
			return nil
		}
	case *ast.InterfaceDecl:
		if ovIface, ok := ovDecl.(*ast.InterfaceDecl); ok {
			if hostDecl.TypeAnn == nil || ovIface.TypeAnn == nil {
				break
			}
			elems, err := mergeMembers(f, name, hostDecl.TypeAnn.Elems, ovIface.TypeAnn.Elems, objElemSlot)
			if err != nil {
				return err
			}
			hostDecl.TypeAnn.Elems = elems
			return nil
		}
	}

	if f.Op == OverlayAdd {
		return fmt.Errorf(
			"overlay: %s adds the %s %s, which %s already declares; correct an "+
				"existing declaration with a replace overlay",
			f.Path, escDeclKind(host), name, f.PkgURI)
	}
	ns.Decls[idx] = ovDecl
	carryDeclMetadata(mod, host, ovDecl)
	return nil
}

// carryDeclMetadata moves the converted declaration's JSDoc and dotted
// runtime path onto the overlay declaration that stands in for it. The
// prose is upstream documentation of the same name, and the path is what
// the ECMA-262 join addresses the declaration's members by.
//
// An overlay declaration carrying its own doc comment keeps it. The
// upstream prose fills in only where the overlay wrote none.
func carryDeclMetadata(mod *StandaloneModule, host, replacement ast.Decl) {
	if replacement.Doc() == "" {
		replacement.SetDoc(host.Doc())
	}
	if path, ok := mod.Paths[host]; ok {
		mod.Paths[replacement] = path
		delete(mod.Paths, host)
	}
}

// mergeMembers folds one overlay declaration's members into the
// converted declaration's, and returns the merged list.
//
// Under `add` each overlay member is appended, and a member the
// converted declaration already has fails the run. Under `replace` each
// overlay member substitutes the converted member sharing its key, at
// that member's position, and a key the converted declaration does not
// have fails the run. Substituting in place rather than appending is
// what keeps a second run byte-identical.
//
// A key addresses a whole overload set, since memberSlot returns `find`
// for both of `Array.find`'s signatures. So a `replace` restates every
// signature under the name it replaces, and the converted signatures
// under that name all give way to the overlay's.
func mergeMembers[E any](
	f OverlayFile,
	owner string,
	host, overlay []E,
	slotOf func(E) (memberSlot, bool),
) ([]E, error) {
	if f.Op == OverlayAdd {
		return addMembers(f, owner, host, overlay, slotOf)
	}
	return replaceMembers(f, owner, host, overlay, slotOf)
}

// addMembers appends every overlay member, failing on a key the
// converted declaration already fills.
func addMembers[E any](
	f OverlayFile,
	owner string,
	host, overlay []E,
	slotOf func(E) (memberSlot, bool),
) ([]E, error) {
	filled := set.NewSet[memberSlot]()
	for _, m := range host {
		if slot, ok := slotOf(m); ok {
			filled.Add(slot)
		}
	}
	out := make([]E, 0, len(host)+len(overlay))
	out = append(out, host...)
	for _, m := range overlay {
		slot, ok := slotOf(m)
		if ok && filled.Contains(slot) {
			return nil, fmt.Errorf(
				"overlay: %s adds %s.%s, which the converted declaration already "+
					"has; correct it with a replace overlay instead",
				f.Path, owner, slot.Name)
		}
		if ok {
			filled.Add(slot)
		}
		out = append(out, m)
	}
	return out, nil
}

// replaceMembers substitutes each overlay member for the converted
// member sharing its key, in place, and fails on a key the converted
// declaration does not have.
func replaceMembers[E any](
	f OverlayFile,
	owner string,
	host, overlay []E,
	slotOf func(E) (memberSlot, bool),
) ([]E, error) {
	groups := map[memberSlot][]E{}
	var order []memberSlot
	for _, m := range overlay {
		slot, ok := slotOf(m)
		if !ok {
			return nil, fmt.Errorf(
				"overlay: %s replaces a member of %s whose key has no textual name; "+
					"a replace addresses a member by name", f.Path, owner)
		}
		if _, seen := groups[slot]; !seen {
			order = append(order, slot)
		}
		groups[slot] = append(groups[slot], m)
	}

	substituted := set.NewSet[memberSlot]()
	out := make([]E, 0, len(host)+len(overlay))
	for _, m := range host {
		slot, ok := slotOf(m)
		if !ok {
			out = append(out, m)
			continue
		}
		group, replaced := groups[slot]
		if !replaced {
			out = append(out, m)
			continue
		}
		// The first converted member under this name takes the overlay's
		// whole overload set; the rest of the converted set gives way.
		if !substituted.Contains(slot) {
			substituted.Add(slot)
			out = append(out, group...)
		}
	}
	for _, slot := range order {
		if !substituted.Contains(slot) {
			return nil, fmt.Errorf(
				"overlay: %s replaces %s.%s, which the converted declaration does "+
					"not have", f.Path, owner, slot.Name)
		}
	}
	return out, nil
}

// findDecl returns the declaration addressed by name, or nil.
func findDecl(decls []ast.Decl, name string) ast.Decl {
	if i := findDeclIndex(decls, name); i >= 0 {
		return decls[i]
	}
	return nil
}

// findDeclIndex returns the index of the declaration addressed by name,
// or -1.
func findDeclIndex(decls []ast.Decl, name string) int {
	for i, decl := range decls {
		if escDeclName(decl) == name {
			return i
		}
	}
	return -1
}
