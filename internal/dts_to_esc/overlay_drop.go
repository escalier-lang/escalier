package dts_to_esc

import (
	"fmt"
	"sort"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
)

// Minimal forms a drop entry is written in. A drop is read for names
// alone, so these are the shortest spellings that parse and neither one
// invites writing a signature the run would then ignore.
const (
	dropDeclForm   = "export declare val <name>"
	dropMemberForm = "export declare interface <name> {\n    <member>: unknown,\n}"
)

// validateDropDecls rejects a drop entry carrying more than the minimal
// form. Every type annotation, signature, and body in a drop file is
// ignored. Accepting one would leave whoever wrote it expecting it to
// matter.
//
// A `val` entry names a whole declaration. An `interface` entry names
// members of the declaration it is titled with, one `<member>: unknown`
// per line. The root drop file takes `val` entries alone, since its
// names are whole symbols that belong to no package.
func validateDropDecls(rel, pkgURI string, decls []ast.Decl) error {
	for _, decl := range decls {
		switch d := decl.(type) {
		case *ast.VarDecl:
			if err := validateDropVarDecl(rel, d); err != nil {
				return err
			}
		case *ast.InterfaceDecl:
			if pkgURI == "" {
				return fmt.Errorf(
					"overlay: %s drops members of %s, but the root drop file names "+
						"whole symbols that belong to no package; move it to a "+
						"<scheme>/<package>.drop.esc file",
					rel, d.Name.Name)
			}
			if err := validateDropInterfaceDecl(rel, d); err != nil {
				return err
			}
		default:
			return fmt.Errorf(
				"overlay: %s drops the %s %s; write a whole declaration as `%s` "+
					"and its members as `%s`",
				rel, escDeclKind(decl), escDeclName(decl), dropDeclForm, dropMemberForm)
		}
	}
	return nil
}

// validateDropVarDecl holds a whole-declaration drop to `export declare
// val <name>`.
func validateDropVarDecl(rel string, d *ast.VarDecl) error {
	name := escDeclName(d)
	switch {
	case name == "":
		return fmt.Errorf("overlay: %s drops a binding with no name; write `%s`", rel, dropDeclForm)
	case d.TypeAnn != nil:
		return fmt.Errorf(
			"overlay: %s gives %s a type annotation, which a drop ignores; write `%s`",
			rel, name, dropDeclForm)
	case d.Init != nil:
		return fmt.Errorf(
			"overlay: %s gives %s an initializer, which a drop ignores; write `%s`",
			rel, name, dropDeclForm)
	case len(d.Decorators) > 0:
		return fmt.Errorf(
			"overlay: %s decorates %s, which a drop ignores; write `%s`",
			rel, name, dropDeclForm)
	}
	return nil
}

// validateDropInterfaceDecl holds a member drop to one
// `<member>: unknown` line per name. An empty body would name a whole
// declaration, which is the `val` form's job.
func validateDropInterfaceDecl(rel string, d *ast.InterfaceDecl) error {
	owner := d.Name.Name
	if len(d.TypeParams) > 0 || len(d.Extends) > 0 {
		return fmt.Errorf(
			"overlay: %s gives %s type parameters or an extends clause, which a "+
				"drop ignores; write `%s`", rel, owner, dropMemberForm)
	}
	if d.TypeAnn == nil || len(d.TypeAnn.Elems) == 0 {
		return fmt.Errorf(
			"overlay: %s drops %s with an empty body; a drop naming a whole "+
				"declaration is written `%s`", rel, owner, dropDeclForm)
	}
	for _, elem := range d.TypeAnn.Elems {
		prop, ok := elem.(*ast.PropertyTypeAnn)
		if !ok {
			return fmt.Errorf(
				"overlay: %s drops a member of %s as a signature, which a drop "+
					"ignores; write `%s`", rel, owner, dropMemberForm)
		}
		slot, ok := slotFor(prop.Name, false)
		if !ok {
			return fmt.Errorf(
				"overlay: %s drops a member of %s whose key has no textual name; "+
					"write `%s`", rel, owner, dropMemberForm)
		}
		if _, ok := prop.Value.(*ast.UnknownTypeAnn); !ok {
			return fmt.Errorf(
				"overlay: %s types %s.%s, which a drop ignores; write `%s`",
				rel, owner, slot.Name, dropMemberForm)
		}
		if prop.Optional || prop.Readonly {
			return fmt.Errorf(
				"overlay: %s marks %s.%s optional or readonly, which a drop "+
					"ignores; write `%s`", rel, owner, slot.Name, dropMemberForm)
		}
	}
	return nil
}

// dropPlan is what one package's drop files ask of the converted module:
// whole declarations by name, and members by owning declaration name.
//
// A member drop removes every member under that name. It takes the
// whole overload set with it, and reaches a static member as readily as
// an instance one. That is why the member sets hold bare names rather
// than the memberSlot the add and replace paths key on, which also
// records which side of the class a member lives on.
type dropPlan struct {
	decls   set.Set[string]
	members map[string]set.Set[string]
}

// newDropPlan reads the drop entries of one package's overlay files.
//
// The declaration kind read here is the drop file's own grammar, not the
// target's. validateDropDecls holds a drop file to `val` for a whole
// declaration and `interface` for a member list, so an `interface` entry
// names members of whatever the converted declaration turns out to be.
// dropDeclMembers is where the target's kind matters, and it takes a
// class as readily as an interface.
func newDropPlan(files []OverlayFile) dropPlan {
	plan := dropPlan{decls: set.NewSet[string](), members: map[string]set.Set[string]{}}
	for _, f := range files {
		if f.Op != OverlayDrop {
			continue
		}
		for _, decl := range f.Decls {
			iface, ok := decl.(*ast.InterfaceDecl)
			if !ok {
				plan.decls.Add(escDeclName(decl))
				continue
			}
			owner := iface.Name.Name
			names, ok := plan.members[owner]
			if !ok {
				names = set.NewSet[string]()
				plan.members[owner] = names
			}
			for _, elem := range iface.TypeAnn.Elems {
				if slot, ok := slotFor(elem.(*ast.PropertyTypeAnn).Name, false); ok {
					names.Add(slot.Name)
				}
			}
		}
	}
	return plan
}

// empty reports whether the plan asks for nothing.
func (p dropPlan) empty() bool {
	return p.decls.Len() == 0 && len(p.members) == 0
}

// sortedNames returns a set's members in sorted order, so a run that
// fails on more than one of them names the same one every time.
func sortedNames(names set.Set[string]) []string {
	out := names.ToSlice()
	sort.Strings(out)
	return out
}

// sortedOwners returns the declaration names a member-drop map is keyed
// by, sorted for the same reason sortedNames is.
func sortedOwners(members map[string]set.Set[string]) []string {
	out := make([]string, 0, len(members))
	for owner := range members {
		out = append(out, owner)
	}
	sort.Strings(out)
	return out
}
