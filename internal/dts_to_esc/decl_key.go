package dts_to_esc

import (
	"github.com/escalier-lang/escalier/internal/ast"
)

// memberSlot identifies a member by the name it is addressed with plus
// which side of the class it lives on. It is what an overlay `add` or
// `replace` matches a member by, and what the re-run's member diff pairs
// the two sides by.
//
// Member kind is not part of the slot, so a `readonly x: T` field and a
// `get x() -> T` getter occupy the same one. Whether an overlay may
// change a member's kind deliberately is settled in #1356.
//
// An overload set collapses to one slot as well: `Array.find` has two
// signatures and one slot, so an overlay replacing that name restates
// both.
type memberSlot struct {
	Name   string
	Static bool
}

// classElemSlot returns the slot a class member fills. The bool is
// false for a member whose key is neither an identifier, a string
// literal, nor a dotted computed key such as `[Symbol.iterator]`. Every
// caller runs both sides through this function, so an unnameable member
// takes part in no match: an overlay cannot address it and the member
// diff never reports it.
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
// Call and construct signatures carry no name, so callers pass over them
// the same way they pass over an unnameable key.
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
// bound to a destructuring pattern, say. An unnamed declaration is
// addressed by neither the overlay nor the member diff.
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

// escDeclKind returns the Escalier form of a declaration, for error
// messages that name what a contributor wrote.
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
