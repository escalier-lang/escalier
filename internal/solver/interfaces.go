package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/provenance"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// interfaces.go binds an `interface` declaration to the object type its body
// describes.
//
// An interface is structural and transparent, so it binds to an AliasType over
// that object rather than to a nominal identity of its own. `interface Point {x:
// number}` and `type Point = {x: number}` give an annotation the same type, and
// a matching object literal satisfies either.
//
// One name may be declared several times. dep_graph groups every `interface
// Point` in a module under one type key, so a shell carries the whole group and
// the bound type is their merged member list.

// interfaceShell is one interface name's pre-bound identity, completed by
// inferInterfaceBody once every sibling identity in the component is bound.
type interfaceShell struct {
	// decls are every declaration of this name, in source order. The merge reads
	// them in that order, so a later member of the same name wins.
	decls []*ast.InterfaceDecl
	lvl   int
	// qname is the dep_graph-qualified name the AliasDef registers under and the
	// Name every AliasType handle pointing at it carries.
	qname string
	// declScope is scope, or a child holding a generic interface's type
	// parameters, so the body reads each `T` as the var the parameter binder
	// minted.
	declScope *Scope
	// ns is the interface's dep_graph namespace. The body resolves a bare
	// reference against it first, so a namespaced interface reaches a sibling.
	ns string
	// def is the registered AliasDef with a nil Body. inferInterfaceBody fills it.
	def *AliasDef
}

// preBindInterface resolves the group's type parameters, registers an AliasDef
// whose Body is still nil, and binds the name to an AliasType handle. Registering
// the name before any body resolves is what lets a sibling interface, or this one,
// name it while its own body is still being walked.
//
// The type parameters come from the first declaration, and every later one must
// write the same list. A declaration that disagrees reports
// InterfaceTypeParamMismatchError and contributes no members, since its body reads
// parameter names the shared list does not bind.
func (c *checker) preBindInterface(scope *Scope, lvl int, decls []*ast.InterfaceDecl, ns string) *interfaceShell {
	first := decls[0]

	prevNS := c.classNamespace
	c.classNamespace = ns
	defer func() { c.classNamespace = prevNS }()

	qname := c.qualifyDecl(ns, first.Name.Name)

	declScope := scope
	var typeParams []*soltype.TypeParam
	if len(first.TypeParams) > 0 {
		declScope = scope.Child()
		typeParams = c.resolveTypeParams(declScope, lvl, first.TypeParams)
	}

	// Every declaration's body resolves against the one parameter list above, so a
	// declaration writing different parameter names would read them as unbound, and
	// a bound or default it writes would be ignored. Drop it here rather than let
	// its body report a missing type for each use, or merge under a bound the source
	// never agreed on.
	merged := decls[:1]
	for _, d := range decls[1:] {
		if reason, mismatch := typeParamMismatch(first.TypeParams, d.TypeParams); mismatch {
			c.report(&InterfaceTypeParamMismatchError{
				Name:   first.Name.Name,
				Reason: reason,
				span:   d.Span(),
			})
			continue
		}
		merged = append(merged, d)
	}
	decls = merged

	def := &AliasDef{TypeParams: typeParams, Level: lvl - 1}
	c.ctx.registerAlias(qname, def)

	t := &soltype.AliasType{Name: qname}
	sources := make([]provenance.Provenance, 0, len(decls))
	for _, d := range decls {
		sources = append(sources, &ast.NodeProvenance{Node: d})
	}
	c.declTarget(scope).defineType(qname, TypeBinding{Type: t, Sources: sources})
	c.recordType(first.Name, t)

	return &interfaceShell{
		decls:     decls,
		lvl:       lvl,
		qname:     qname,
		declScope: declScope,
		ns:        ns,
		def:       def,
	}
}

// inferInterfaceBody resolves each declaration's members and stores their merge on
// the shell's AliasDef. It runs after every identity in the recursive group is
// pre-bound, so a member naming a sibling resolves.
//
// An `extends` clause contributes its target's members ahead of the body's own, so
// `interface Sq extends Rect {s: number}` binds the flattened surface rather than a
// subtyping relation. A member the body declares under a name `extends` also
// supplied replaces it.
func (c *checker) inferInterfaceBody(sh *interfaceShell) {
	prevNS := c.classNamespace
	c.classNamespace = sh.ns
	defer func() { c.classNamespace = prevNS }()

	// The shared object-member builder decides what a later member does to an
	// earlier one of the same name. It appends a method's signatures rather than
	// replacing it, so overloads split across declarations accumulate, and it
	// tracks the read and write accesses separately, so a getter in one
	// declaration and a setter in another form a pair rather than one dropping
	// the other.
	b := newObjElemBuilder(0)
	inexact := false
	for _, decl := range sh.decls {
		for _, ext := range decl.Extends {
			if obj, ok := c.resolveInterfaceParent(sh.declScope, ext, sh.lvl); ok {
				for _, elem := range obj.Elems {
					b.addElem(copyMergeableElem(elem))
				}
				inexact = inexact || obj.Inexact
			}
		}
		// A nil TypeAnn is parser error recovery for a body that failed to parse,
		// already reported. The declaration then contributes no members.
		if decl.TypeAnn == nil {
			continue
		}
		resolved, ok := c.resolveObjectTypeAnn(sh.declScope, decl.TypeAnn, sh.lvl)
		if !ok {
			continue
		}
		obj, isObj := resolved.(*soltype.ObjectType)
		if !isObj {
			continue
		}
		for _, elem := range obj.Elems {
			b.addElem(elem)
		}
		inexact = inexact || obj.Inexact
	}
	sh.def.Body = &soltype.ObjectType{Elems: b.result(), Inexact: inexact}
}

// copyMergeableElem returns a member safe to hand to an objElemBuilder that may
// merge into it. The builder appends to a MethodElem's signature list in place, so
// a method taken from a parent interface's stored body is copied first, leaving the
// parent's own type unchanged.
func copyMergeableElem(elem soltype.ObjTypeElem) soltype.ObjTypeElem {
	m, isMethod := elem.(*soltype.MethodElem)
	if !isMethod {
		return elem
	}
	sigs := make([]*soltype.FuncType, len(m.Signatures))
	copy(sigs, m.Signatures)
	dup := *m
	dup.Signatures = sigs
	return &dup
}

// resolveInterfaceParent resolves one `extends` target to the object type whose
// members it contributes. A target naming something other than an object reports
// InterfaceExtendsNonObjectError and contributes nothing.
func (c *checker) resolveInterfaceParent(scope *Scope, ref *ast.TypeRefTypeAnn, lvl int) (*soltype.ObjectType, bool) {
	resolved, ok := c.resolveTypeAnn(scope, ref, lvl)
	if !ok {
		return nil, false
	}
	expanded := c.expandAliasChain(resolved)
	if obj, isObj := expanded.(*soltype.ObjectType); isObj {
		return obj, true
	}
	// The reference resolved, so an ErrorType here is an unfilled body: the target
	// is a sibling in this same recursive group. An interface flattens its parents'
	// members into its own, which a cycle gives no way to compute, so report it
	// rather than silently binding the subset that happened to resolve.
	if _, isErr := expanded.(*soltype.ErrorType); isErr {
		c.report(&InterfaceExtendsCycleError{Name: soltype.Print(resolved), span: ref.Span()})
		return nil, false
	}
	c.report(&InterfaceExtendsNonObjectError{Name: soltype.Print(resolved), span: ref.Span()})
	return nil, false
}

// InterfaceExtendsNonObjectError reports an `extends` target that is not an object
// type. An interface flattens its parents' members into its own, so a parent with
// no member list has nothing to contribute.
type InterfaceExtendsNonObjectError struct {
	// Name is the target as written, rendered.
	Name string
	span ast.Span
}

func (e *InterfaceExtendsNonObjectError) Message() string {
	return "an interface can only extend an object type, not " + e.Name
}
func (e *InterfaceExtendsNonObjectError) Span() ast.Span      { return e.span }
func (e *InterfaceExtendsNonObjectError) Related() []ast.Span { return nil }
func (e *InterfaceExtendsNonObjectError) isSolverError()      {}

// typeParamMismatch reports whether a later declaration's type parameters can
// merge under the first declaration's list, and why not when they cannot.
//
// The first declaration's list is the one every body resolves against, so a later
// declaration writing a bound or a default is writing something nothing reads.
// Rejecting that is what stops `Box<T: string>` and `Box<T: number>` merging under
// whichever came first.
func typeParamMismatch(first, later []*ast.TypeParam) (string, bool) {
	if len(first) != len(later) {
		return "they differ in how many it writes", true
	}
	for i := range later {
		if first[i].Name != later[i].Name {
			return "they differ in the names they write", true
		}
		if later[i].Variance != first[i].Variance {
			return "they differ in variance", true
		}
		if later[i].Constraint != nil {
			return "only the first declaration may write a bound", true
		}
		if later[i].Default != nil {
			return "only the first declaration may write a default", true
		}
	}
	return "", false
}

// InterfaceTypeParamMismatchError reports a declaration whose type-parameter list
// differs from the first declaration of the same interface. One name binds one
// parameter list, so the declarations have to agree on it.
type InterfaceTypeParamMismatchError struct {
	// Name is the interface every declaration in the group declares.
	Name string
	// Reason says how this declaration's list differs from the first's.
	Reason string
	span   ast.Span
}

func (e *InterfaceTypeParamMismatchError) Message() string {
	return "declarations of interface " + e.Name + " must agree on their type parameters: " + e.Reason
}
func (e *InterfaceTypeParamMismatchError) Span() ast.Span      { return e.span }
func (e *InterfaceTypeParamMismatchError) Related() []ast.Span { return nil }
func (e *InterfaceTypeParamMismatchError) isSolverError()      {}

// InterfaceExtendsCycleError reports an `extends` target that is part of the same
// recursive group as the interface extending it. Flattening the parent's members
// requires those members, which a cycle never finishes producing.
type InterfaceExtendsCycleError struct {
	// Name is the target as written, rendered.
	Name string
	span ast.Span
}

func (e *InterfaceExtendsCycleError) Message() string {
	return "an interface cannot extend " + e.Name + ", which is part of the same recursive group"
}
func (e *InterfaceExtendsCycleError) Span() ast.Span      { return e.span }
func (e *InterfaceExtendsCycleError) Related() []ast.Span { return nil }
func (e *InterfaceExtendsCycleError) isSolverError()      {}
