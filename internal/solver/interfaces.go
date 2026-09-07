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
// The type parameters come from the first declaration. A group whose declarations
// disagree on their parameter lists is rejected by reportInterfaceParamMismatch
// rather than merged under one of them.
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

	var elems []soltype.ObjTypeElem
	inexact := false
	for _, decl := range sh.decls {
		for _, ext := range decl.Extends {
			if obj, ok := c.resolveInterfaceParent(sh.declScope, ext, sh.lvl); ok {
				elems = mergeObjElems(elems, obj.Elems)
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
			// resolveObjectTypeAnn answers with a residual type for a body holding a
			// spread or a mapped member, which has no member list to merge. Bind the
			// residual alone, since merging a group is only defined over member lists.
			sh.def.Body = resolved
			return
		}
		elems = mergeObjElems(elems, obj.Elems)
		inexact = inexact || obj.Inexact
	}
	sh.def.Body = &soltype.ObjectType{Elems: elems, Inexact: inexact}
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
	// An unfilled body expands to ErrorType, which happens when the target is a
	// sibling in this same recursive group. Its members are not available yet and
	// an interface cycle has no flattened surface to compute, so contribute
	// nothing and leave the diagnostic to the reference that failed.
	if _, isErr := expanded.(*soltype.ErrorType); isErr {
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

// mergeObjElems appends src's members to dst, with a later member replacing an
// earlier one of the same name in place. Keeping the earlier position means
// `interface P {x: number}` followed by `interface P {y: string, x: 1}` renders as
// `{x: 1, y: string}`, so a reader sees the order the name was introduced in.
//
// An unnamed member, such as a call or construct signature, has no name to match
// on and is appended.
func mergeObjElems(dst, src []soltype.ObjTypeElem) []soltype.ObjTypeElem {
	for _, elem := range src {
		name := soltype.ObjElemName(elem)
		if name == "" {
			dst = append(dst, elem)
			continue
		}
		replaced := false
		for i, existing := range dst {
			if soltype.ObjElemName(existing) == name {
				dst[i] = elem
				replaced = true
				break
			}
		}
		if !replaced {
			dst = append(dst, elem)
		}
	}
	return dst
}
