package dts_to_esc

import (
	"fmt"

	"github.com/escalier-lang/escalier/internal/ast"
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
