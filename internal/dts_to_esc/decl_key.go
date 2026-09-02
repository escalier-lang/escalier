package dts_to_esc

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/printer"
)

// memberKind is the form a member takes: a field, a method, an
// accessor, or a constructor. It is part of the key an overlay
// addresses a member by, so a `readonly x: T` field and a `get x() -> T`
// getter do not silently occupy one slot.
type memberKind string

const (
	kindField       memberKind = "field"
	kindProperty    memberKind = "property"
	kindMethod      memberKind = "method"
	kindGetter      memberKind = "getter"
	kindSetter      memberKind = "setter"
	kindConstructor memberKind = "constructor"
)

// memberSlot identifies a member by the name it is addressed with, which
// side of the class it lives on, and its kind. It is what an overlay
// `add` or `replace` matches a member by.
//
// An overload set collapses to one slot. `Array.find` has two signatures
// and one slot, so an overlay replacing that name restates both.
type memberSlot struct {
	Name   string
	Static bool
	Kind   memberKind
}

// nameAndSide is the slot with its kind dropped, leaving the name a
// member is addressed with and which side of the class it lives on.
// Two members sharing that much land on one name in the output whatever
// their kinds, so `add` rejects the second and `replace` pairs the two
// sides on it before comparing kind. A getter and a setter are the one
// pair allowed to share it.
func (s memberSlot) nameAndSide() memberSlot {
	return memberSlot{Name: s.Name, Static: s.Static}
}

// accessorPartner reports whether two kinds are the two halves of one
// accessor. A `get x()` and a `set x()` are the one pair of members that
// share a name; every other pair under one name is two members the
// declaration cannot hold at once.
func accessorPartner(a, b memberKind) bool {
	return (a == kindGetter && b == kindSetter) || (a == kindSetter && b == kindGetter)
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
		return slotFor(e.Name, e.Static, kindField)
	case *ast.MethodElem:
		return slotFor(e.Name, e.Static, kindMethod)
	case *ast.GetterElem:
		return slotFor(e.Name, e.Static, kindGetter)
	case *ast.SetterElem:
		return slotFor(e.Name, e.Static, kindSetter)
	case *ast.ConstructorElem:
		return memberSlot{Name: "constructor", Kind: kindConstructor}, true
	}
	return memberSlot{}, false
}

// objElemSlot is classElemSlot for an interface or object-type member.
// Call and construct signatures carry no name, so callers pass over them
// the same way they pass over an unnameable key.
func objElemSlot(elem ast.ObjTypeAnnElem) (memberSlot, bool) {
	switch e := elem.(type) {
	case *ast.PropertyTypeAnn:
		return slotFor(e.Name, false, kindProperty)
	case *ast.MethodTypeAnn:
		return slotFor(e.Name, false, kindMethod)
	case *ast.GetterTypeAnn:
		return slotFor(e.Name, false, kindGetter)
	case *ast.SetterTypeAnn:
		return slotFor(e.Name, false, kindSetter)
	}
	return memberSlot{}, false
}

// slotFor builds a memberSlot from a member key, reporting false for a
// key with no stable textual name.
func slotFor(key ast.ObjKey, static bool, kind memberKind) (memberSlot, bool) {
	switch k := key.(type) {
	case *ast.IdentExpr:
		return memberSlot{Name: k.Name, Static: static, Kind: kind}, true
	case *ast.StrLit:
		return memberSlot{Name: k.Value, Static: static, Kind: kind}, true
	case *ast.ComputedKey:
		if dotted := astExprDottedName(k.Expr); dotted != "" {
			return memberSlot{Name: dotted, Static: static, Kind: kind}, true
		}
	}
	return memberSlot{}, false
}

// memberOps is how the merge reads one member list: the slot a member
// fills and the Escalier source a digest is taken over. The two member
// lists a declaration can hold, a class body and an interface body, have
// no element type in common, so each supplies its own pair.
type memberOps[E any] struct {
	slot func(E) (memberSlot, bool)
	form func(E) (string, error)
}

// digestOptions returns the printer options a digest is taken under.
// Doc comments are left out. An overlay stands in for a member's shape,
// and carryMemberDocs hands the converted member's prose to the overlay
// member replacing it wherever that member wrote none of its own. So an
// upstream doc edit forks nothing and must not read as movement.
func digestOptions() printer.Options {
	opts := printer.DefaultOptions()
	opts.OmitDocComments = true
	return opts
}

// classMemberOps reads a class body.
var classMemberOps = memberOps[ast.ClassElem]{
	slot: classElemSlot,
	form: func(elem ast.ClassElem) (string, error) {
		return printer.PrintClassElem(elem, digestOptions())
	},
}

// objMemberOps reads an interface or object-type body.
var objMemberOps = memberOps[ast.ObjTypeAnnElem]{
	slot: objElemSlot,
	form: func(elem ast.ObjTypeAnnElem) (string, error) {
		return printer.PrintObjTypeAnnElem(elem, digestOptions())
	},
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
