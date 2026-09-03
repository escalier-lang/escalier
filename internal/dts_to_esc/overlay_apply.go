package dts_to_esc

import (
	"fmt"
	"strings"

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
func applyOverlayDecl(
	mod *StandaloneModule,
	ns *ast.Namespace,
	f OverlayFile,
	ovDecl ast.Decl,
) error {
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
			if err := checkMergeDecl(f, name, hostDecl.TypeParams, ovClass); err != nil {
				return err
			}
			body, err := mergeMembers(
				f, name, hostDecl.Body, ovClass.Body, classElemSlot)
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
			if err := checkMergeDecl(f, name, hostDecl.TypeParams, ovIface); err != nil {
				return err
			}
			elems, err := mergeMembers(
				f, name, hostDecl.TypeAnn.Elems, ovIface.TypeAnn.Elems, objElemSlot)
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

// checkMergeDecl holds an overlay's member operation to what a merge
// reads: the members, and the type parameters they are read under. The
// type parameters have to agree, since an overlay binding `<U>` would
// leave its members referring to a name the generated declaration does
// not bind. Everything else the overlay writes around its members goes
// unread, so writing any of it fails rather than being dropped in
// silence.
func checkMergeDecl(f OverlayFile, name string, host []*ast.TypeParam, overlay ast.Decl) error {
	if typeParamNames(host) != typeParamNames(overlayTypeParams(overlay)) {
		return fmt.Errorf(
			"overlay: %s writes %s%s, which the converted declaration binds as %s%s; "+
				"a member operation keeps the converted declaration's type parameters, "+
				"so the overlay restates them as they are",
			f.Path, name, typeParamNames(overlayTypeParams(overlay)), name,
			typeParamNames(host))
	}
	if part := unreadDeclPart(overlay); part != "" {
		return fmt.Errorf(
			"overlay: %s writes %s on %s, which a member operation does not read; "+
				"it contributes members alone, so drop it from the overlay",
			f.Path, part, name)
	}
	return nil
}

// overlayTypeParams returns the type parameters of the one declaration
// kind a member operation merges into, and nothing for any other.
func overlayTypeParams(decl ast.Decl) []*ast.TypeParam {
	switch d := decl.(type) {
	case *ast.ClassDecl:
		return d.TypeParams
	case *ast.InterfaceDecl:
		return d.TypeParams
	}
	return nil
}

// unreadDeclPart names the first thing an overlay declaration writes
// around its members that a member operation would drop, or "" when it
// writes none. A decorator, a `final` modifier, an `extends` or
// `implements` clause, and a lifetime parameter all belong to the
// converted declaration alone.
func unreadDeclPart(decl ast.Decl) string {
	switch d := decl.(type) {
	case *ast.ClassDecl:
		switch {
		case len(d.Decorators) > 0:
			return "a decorator"
		case d.Final():
			return "a final modifier"
		case d.Extends != nil:
			return "an extends clause"
		case len(d.Implements) > 0:
			return "an implements clause"
		case len(d.LifetimeParams) > 0:
			return "a lifetime parameter"
		}
	case *ast.InterfaceDecl:
		switch {
		case len(d.Extends) > 0:
			return "an extends clause"
		case len(d.LifetimeParams) > 0:
			return "a lifetime parameter"
		}
	}
	return ""
}

// typeParamNames renders a type parameter list by name alone, as `<T>`.
// Constraints and defaults are the converted declaration's to state, so
// they take no part in the comparison. An empty list renders as "", so a
// declaration with no parameters reads as its own name.
func typeParamNames(params []*ast.TypeParam) string {
	if len(params) == 0 {
		return ""
	}
	names := make([]string, len(params))
	for i, param := range params {
		names[i] = param.Name
	}
	return "<" + strings.Join(names, ", ") + ">"
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

// addMembers appends every overlay member, failing on a member the
// converted declaration already holds. A taken slot is a correction, so
// the report points at `replace`. A name taken under another kind is a
// kind change, so it points at a `drop` and an `add` instead. One file
// may add several signatures under one name, and may add the `set x()`
// beside a converted `get x()`.
func addMembers[E any](
	f OverlayFile,
	owner string,
	host, overlay []E,
	slotOf func(E) (memberSlot, bool),
) ([]E, error) {
	converted, hostKinds := groupMembers(host, slotOf)
	added := map[memberSlot][]memberKind{}
	out := make([]E, 0, len(host)+len(overlay))
	out = append(out, host...)
	for _, m := range overlay {
		slot, ok := slotOf(m)
		if !ok {
			out = append(out, m)
			continue
		}
		if _, taken := converted[slot]; taken {
			return nil, fmt.Errorf(
				"overlay: %s adds %s, which the converted declaration already "+
					"has; correct it with a replace overlay instead",
				f.Path, memberLabel(owner, slot))
		}
		if clash := unpairedKinds(hostKinds[slot.nameAndSide()], slot.Kind); len(clash) > 0 {
			return nil, fmt.Errorf(
				"overlay: %s adds %s as a %s, which the converted declaration "+
					"declares as %s; drop the member and add the new form to change "+
					"its kind", f.Path, memberLabel(owner, slot), slot.Kind, joinKinds(clash))
		}
		held := added[slot.nameAndSide()]
		if err := checkAddedKinds(f, owner, slot, held); err != nil {
			return nil, err
		}
		if !containsKind(held, slot.Kind) {
			added[slot.nameAndSide()] = append(held, slot.Kind)
		}
		out = append(out, m)
	}
	return out, nil
}

// checkAddedKinds pairs one overlay member against what the same file
// already adds under that name. Signatures of one method overload and a
// getter pairs with a setter; every other repeat is two members under
// one name, which no declaration can hold.
func checkAddedKinds(f OverlayFile, owner string, slot memberSlot, held []memberKind) error {
	if containsKind(held, slot.Kind) {
		if slot.Kind == kindMethod {
			return nil
		}
		return fmt.Errorf(
			"overlay: %s adds %s twice as a %s; only signatures overload",
			f.Path, memberLabel(owner, slot), slot.Kind)
	}
	if clash := unpairedKinds(held, slot.Kind); len(clash) > 0 {
		return fmt.Errorf(
			"overlay: %s adds %s as a %s beside %s it adds under the same name; "+
				"one name holds one member, or a getter and a setter",
			f.Path, memberLabel(owner, slot), slot.Kind, joinKinds(clash))
	}
	return nil
}

// unpairedKinds returns the kinds under one name that cannot stand
// beside kind. Empty means the name is free, or holds only the other
// half of kind's accessor.
func unpairedKinds(held []memberKind, kind memberKind) []memberKind {
	var clash []memberKind
	for _, h := range held {
		if !accessorPartner(h, kind) {
			clash = append(clash, h)
		}
	}
	return clash
}

// containsKind reports whether kind is among held.
func containsKind(held []memberKind, kind memberKind) bool {
	for _, h := range held {
		if h == kind {
			return true
		}
	}
	return false
}

// replaceMembers substitutes each overlay member for the converted
// member sharing its key, in place, and fails on a key the converted
// declaration does not have.
//
// The key carries the member's kind, so a `readonly x: T` and a
// `get x()` do not occupy one slot and no substitution crosses kinds. It
// addresses a whole overload set, since a name alone cannot pick one of
// `Array.find`'s two signatures apart, so a `replace` restates every
// signature under the name. Restating fewer fails, and so does a second
// field or accessor under one name.
func replaceMembers[E any](
	f OverlayFile,
	owner string,
	host, overlay []E,
	slotOf func(E) (memberSlot, bool),
) ([]E, error) {
	hostGroups, hostKinds := groupMembers(host, slotOf)
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

	for _, slot := range order {
		converted, ok := hostGroups[slot]
		if !ok {
			return nil, missingSlotError(f, owner, slot, hostKinds)
		}
		if len(groups[slot]) > 1 && slot.Kind != kindMethod {
			return nil, fmt.Errorf(
				"overlay: %s replaces %s twice as a %s; only signatures overload",
				f.Path, memberLabel(owner, slot), slot.Kind)
		}
		if len(groups[slot]) < len(converted) {
			return nil, fmt.Errorf(
				"overlay: %s replaces %d of the %d signatures of %s; a replace "+
					"restates the whole overload set, since a name is what addresses it",
				f.Path, len(groups[slot]), len(converted), memberLabel(owner, slot))
		}
		carryMemberDocs(converted, groups[slot])
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
		// The first converted member under this key takes the overlay's
		// whole overload set; the rest of the converted set gives way.
		if !substituted.Contains(slot) {
			substituted.Add(slot)
			out = append(out, group...)
		}
	}
	return out, nil
}

// docHolder is a member carrying a JSDoc comment. Both ast.ClassElem and
// ast.ObjTypeAnnElem declare the pair.
type docHolder interface {
	Doc() string
	SetDoc(string)
}

// carryMemberDocs moves the converted members' JSDoc onto the overlay
// members standing in for them, pairing the two lists by position. It is
// the member-level counterpart of carryDeclMetadata, and fills in only
// where the overlay member wrote no doc of its own.
func carryMemberDocs[E any](converted, overlay []E) {
	for i, m := range overlay {
		if i >= len(converted) {
			return
		}
		member, ok := any(m).(docHolder)
		if !ok || member.Doc() != "" {
			continue
		}
		if host, ok := any(converted[i]).(docHolder); ok {
			member.SetDoc(host.Doc())
		}
	}
}

// groupMembers collects a declaration's members by the slot each fills,
// and records which kinds every name is declared under. The kinds are
// what tells a member the declaration does not have from one the overlay
// wrote under the wrong kind.
func groupMembers[E any](
	members []E,
	slotOf func(E) (memberSlot, bool),
) (map[memberSlot][]E, map[memberSlot][]memberKind) {
	groups := map[memberSlot][]E{}
	kinds := map[memberSlot][]memberKind{}
	for _, m := range members {
		slot, ok := slotOf(m)
		if !ok {
			continue
		}
		if _, seen := groups[slot]; !seen {
			kinds[slot.nameAndSide()] = append(kinds[slot.nameAndSide()], slot.Kind)
		}
		groups[slot] = append(groups[slot], m)
	}
	return groups, kinds
}

// missingSlotError reports an overlay member the converted declaration
// cannot be paired with. A name held under another kind gets a narrower
// message, since the overlay found its target and wrote it in the wrong
// form. The missing half of an accessor is an addition; every other
// mismatch is a kind change, so a `drop` and an `add`.
func missingSlotError(
	f OverlayFile,
	owner string,
	slot memberSlot,
	hostKinds map[memberSlot][]memberKind,
) error {
	held := hostKinds[slot.nameAndSide()]
	if len(held) == 0 {
		return fmt.Errorf(
			"overlay: %s replaces %s, which the converted declaration does "+
				"not have", f.Path, memberLabel(owner, slot))
	}
	clash := unpairedKinds(held, slot.Kind)
	if len(clash) == 0 {
		return fmt.Errorf(
			"overlay: %s replaces %s as a %s, which the converted declaration "+
				"declares only as %s; contribute the %s with an add overlay instead",
			f.Path, memberLabel(owner, slot), slot.Kind, joinKinds(held), slot.Kind)
	}
	return fmt.Errorf(
		"overlay: %s replaces %s as a %s, which the converted declaration "+
			"declares as %s; drop the member and add the new form to change its kind",
		f.Path, memberLabel(owner, slot), slot.Kind, joinKinds(clash))
}

// memberLabel names a member the way an overlay report does. A static
// member is marked, since one name reaches both sides of a class and
// `Array.of` alone would read as the instance member of a declaration
// that has both.
func memberLabel(owner string, slot memberSlot) string {
	if slot.Static {
		return "static " + owner + "." + slot.Name
	}
	return owner + "." + slot.Name
}

// joinKinds names the kinds one member name is declared under, as `a
// getter and a setter`.
func joinKinds(kinds []memberKind) string {
	names := make([]string, len(kinds))
	for i, kind := range kinds {
		names[i] = "a " + string(kind)
	}
	if len(names) == 1 {
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
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
