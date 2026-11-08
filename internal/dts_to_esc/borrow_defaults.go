package dts_to_esc

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
)

// valueTypeNames holds the declarations that TypeScript writes as interfaces but that
// name a value type. A `Promise` or `PromiseLike` is a promise and a `Function` is a
// function, so each is copied rather than borrowed.
var valueTypeNames = set.FromSlice([]string{
	"Promise",
	"PromiseLike",
	"Function",
	"CallableFunction",
	"NewableFunction",
})

// typeIndex maps the dotted name of every interface, class and type alias a set of
// converted modules declares to its declaration. A name declared inside a namespace
// carries the namespace chain, as in `Intl.DateTimeFormatOptions`.
type typeIndex struct {
	interfaces map[string]*ast.InterfaceDecl
	classes    map[string]*ast.ClassDecl
	aliases    map[string]*ast.TypeDecl
}

// newTypeIndex indexes the declarations of every module in mods. A declaration that
// namespace flattening lifted to the top level is indexed under its dotted runtime path
// as well as its name, so a reference written `Intl.DateTimeFormatOptions` finds it.
func newTypeIndex(mods ...*StandaloneModule) *typeIndex {
	idx := &typeIndex{
		interfaces: make(map[string]*ast.InterfaceDecl),
		classes:    make(map[string]*ast.ClassDecl),
		aliases:    make(map[string]*ast.TypeDecl),
	}
	for _, mod := range mods {
		mod.Module.Namespaces.Scan(func(ns string, n *ast.Namespace) bool {
			for _, decl := range n.Decls {
				path := mod.Paths[decl]
				switch d := decl.(type) {
				case *ast.InterfaceDecl:
					indexUnder(idx.interfaces, d, qualifiedName(ns, d.Name.Name), path)
				case *ast.ClassDecl:
					indexUnder(idx.classes, d, qualifiedName(ns, d.Name.Name), path)
				case *ast.TypeDecl:
					indexUnder(idx.aliases, d, qualifiedName(ns, d.Name.Name), path)
				}
			}
			return true
		})
	}
	return idx
}

// indexUnder adds decl to m under each of names that is not empty.
func indexUnder[T any](m map[string]T, decl T, names ...string) {
	for _, name := range names {
		if name != "" {
			m[name] = decl
		}
	}
}

// applyBorrowDefaults rewrites the signature of every declared function and every method
// of a declared class in mod, following rules I2 and I3 in
// planning/ownership_rules/requirements.md. idx holds the declarations a type in a
// signature may name.
//
// A `.d.ts` leaves most ownership facts open: whether a callee writes an argument, keeps
// it, or returns part of it. Each rule below is meant to be the most conservative choice
// the declaration allows. Under I3, an unsettled fact takes the form that rejects every
// program the true answer would reject, so a wrong guess makes a call stricter than it
// needs to be, never unsound. The one exception is retention. A borrowed parameter lasts
// for the call, since treating every parameter that may be stored as escaping would
// make every library call give up its arguments.
//
//   - A parameter whose type is an object type becomes a `&mut` borrow.
//   - Every other parameter keeps its type as written. That covers a primitive, a
//     function, a promise, a type parameter, `unknown`, a name idx does not declare, and
//     every union.
//   - A return whose type is an object type, or a union of object types and value types,
//     becomes a `&'a mut` borrow of each object type when it may share structure with a
//     borrowed parameter. Each such parameter takes the lifetime `'a`.
//   - A rest parameter, a `this` parameter, a constructor and a setter keep their
//     parameters as written.
//
// A borrowed parameter may share structure with the return unless every slot of its type
// holds a value type and the return type requires a member the parameter's type lacks.
// A parameter of value-type slots holds no object the return could be. The return can
// then only be the argument itself, and an argument without one of the return type's
// members cannot be.
func applyBorrowDefaults(mod *StandaloneModule, idx *typeIndex) {
	mod.Module.Namespaces.Scan(func(_ string, ns *ast.Namespace) bool {
		for _, decl := range ns.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Body == nil {
					idx.borrowSigDefaults(&d.FuncSig, set.NewSet[string]())
				}
			case *ast.ClassDecl:
				classTypeParams := typeParamNameSet(d.TypeParams)
				for _, elem := range d.Body {
					method, ok := elem.(*ast.MethodElem)
					if !ok || method.Fn == nil || method.Fn.Body != nil {
						continue
					}
					idx.borrowSigDefaults(&method.Fn.FuncSig, classTypeParams)
				}
			}
		}
		return true
	})
}

// borrowSigDefaults applies applyBorrowDefaults to one signature. outerTypeParams holds
// the type parameters of the enclosing class.
func (idx *typeIndex) borrowSigDefaults(sig *ast.FuncSig, outerTypeParams set.Set[string]) {
	// A method sees the type parameters of its class as well as its own. A name in this
	// set is never an object type, since it may be instantiated with a value type.
	typeParams := outerTypeParams.Union(typeParamNameSet(sig.TypeParams))

	// Wrap each parameter of an object type in a `&mut` borrow, and keep the borrows so
	// the return can be tied to them below. A rest parameter has a RestPat, so it fails
	// the IdentPat check. `this` is left to receiver classification.
	var borrowed []*ast.RefTypeAnn
	for _, param := range sig.Params {
		if param.TypeAnn == nil {
			continue
		}
		pat, ok := param.Pattern.(*ast.IdentPat)
		if !ok || pat.Name == "this" || !idx.isObjectType(param.TypeAnn, typeParams) {
			continue
		}
		ref := ast.NewBorrowTypeAnn(true, nil, param.TypeAnn, param.TypeAnn.Span())
		param.TypeAnn = ref
		borrowed = append(borrowed, ref)
	}

	if sig.Return == nil {
		return
	}

	// Build the borrowed form of the return without installing it yet. ret is the new
	// return type and retRefs holds the borrows it added, one per object type. When the
	// return has no object type, retRefs is empty and no parameter can share with it.
	ret, retRefs := idx.borrowObjectTypes(sig.Return, typeParams)

	// Keep the borrowed parameters that may share structure with one of the returned
	// object types. A parameter that cannot share keeps a borrow scoped to the call, so
	// holding the result does not keep it borrowed.
	var shared []*ast.RefTypeAnn
	for _, ref := range borrowed {
		if idx.mayShareWithAny(ref.Inner, retRefs, typeParams) {
			shared = append(shared, ref)
		}
	}

	// With no parameter to tie it to, the return stays owned, so ret is discarded.
	if len(shared) == 0 {
		return
	}

	// Tie the sharing parameters and the returned borrows together with one new lifetime
	// parameter. The name skips any lifetime the signature already declares.
	name := freshLifetimeName(sig.LifetimeParams)
	span := sig.Return.Span()
	sig.LifetimeParams = append(sig.LifetimeParams, ast.NewLifetimeParam(name, nil, span))
	for _, ref := range append(shared, retRefs...) {
		ref.Lifetime = ast.NewLifetimeAnn(name, ref.Span())
	}
	sig.Return = ret
}

// borrowObjectTypes returns t with each object type in it wrapped in a `&mut` borrow,
// along with the borrows it wrote. t is wrapped whole when it is an object type. In a
// union, each member that is an object type is wrapped and each value type is left
// alone. Any other t comes back unchanged with no borrows, and so does a union with a
// member that is neither. t itself is not modified.
func (idx *typeIndex) borrowObjectTypes(t ast.TypeAnn, typeParams set.Set[string]) (ast.TypeAnn, []*ast.RefTypeAnn) {
	if idx.isObjectType(t, typeParams) {
		ref := ast.NewBorrowTypeAnn(true, nil, t, t.Span())
		return ref, []*ast.RefTypeAnn{ref}
	}
	union, ok := t.(*ast.UnionTypeAnn)
	if !ok {
		return t, nil
	}
	members := make([]ast.TypeAnn, len(union.Types))
	var refs []*ast.RefTypeAnn
	for i, member := range union.Types {
		switch {
		case idx.isObjectType(member, typeParams):
			ref := ast.NewBorrowTypeAnn(true, nil, member, member.Span())
			members[i] = ref
			refs = append(refs, ref)
		case idx.isValueType(member, typeParams, set.NewSet[string]()):
			members[i] = member
		default:
			return t, nil
		}
	}
	if len(refs) == 0 {
		return t, nil
	}
	return ast.NewUnionTypeAnn(members, union.Span()), refs
}

// mayShareWithAny reports whether an argument of type param may share structure with the
// type any borrow in rets borrows. See mayShareStructure.
func (idx *typeIndex) mayShareWithAny(param ast.TypeAnn, rets []*ast.RefTypeAnn, typeParams set.Set[string]) bool {
	for _, ret := range rets {
		if idx.mayShareStructure(param, ret.Inner, typeParams) {
			return true
		}
	}
	return false
}

// mayShareStructure reports whether a return of type ret may share structure with an
// argument of type param. It is false only when every slot of param holds a value type
// and the argument cannot be the return itself, which mayBeArgument decides.
func (idx *typeIndex) mayShareStructure(param, ret ast.TypeAnn, typeParams set.Set[string]) bool {
	if !idx.holdsOnlyValueTypes(param, typeParams, set.NewSet[string]()) {
		return true
	}
	return idx.mayBeArgument(param, ret, typeParams)
}

// mayBeArgument reports whether a value of type param may be returned as type ret. It
// is false only when ret requires a member by name that param does not declare, as
// `DOMMatrix` requires `multiply` and `DOMMatrixInit` does not declare it. An alias, a
// subtype and a structurally compatible type all declare every member ret requires.
func (idx *typeIndex) mayBeArgument(param, ret ast.TypeAnn, typeParams set.Set[string]) bool {
	paramMembers, ok := idx.memberNames(param, typeParams, set.NewSet[string]())
	if !ok || paramMembers.open {
		return true
	}
	retMembers, ok := idx.memberNames(ret, typeParams, set.NewSet[string]())
	if !ok {
		return true
	}
	for _, name := range retMembers.required.ToSlice() {
		if !paramMembers.all.Contains(name) {
			return false
		}
	}
	return true
}

// memberSet holds the instance member names an object type declares.
type memberSet struct {
	// all holds every member name, and required the names of the members that are not
	// optional.
	all, required set.Set[string]
	// open is true when the type also has members it does not name, through an index
	// signature or a mapped type.
	open bool
}

// add returns m with the member that slot names added. ok is false for a member with no
// name, which marks m open instead.
func (m memberSet) add(slot memberSlot, ok, optional bool) memberSet {
	if !ok {
		m.open = true
		return m
	}
	m.all.Add(slot.Name)
	if !optional {
		m.required.Add(slot.Name)
	}
	return m
}

// memberNames returns the instance members the object type t declares, including the
// ones it inherits. ok is false when t is not an object type whose members idx can
// list, such as a type parameter, a union or a tuple. seen holds the declarations
// already being walked.
func (idx *typeIndex) memberNames(t ast.TypeAnn, typeParams, seen set.Set[string]) (memberSet, bool) {
	members := memberSet{all: set.NewSet[string](), required: set.NewSet[string]()}
	ok := idx.walkMembers(t, typeParams, seen, func(block memberBlock) bool {
		if block.tuple {
			// A tuple's members are its indexes, which no named member can require.
			return false
		}
		members = objElemNames(members, block.objElems)
		for _, elem := range block.classElems {
			if _, isCtor := elem.(*ast.ConstructorElem); isCtor {
				continue
			}
			slot, ok := classElemSlot(elem)
			if ok && slot.Static {
				continue
			}
			field, isField := elem.(*ast.FieldElem)
			members = members.add(slot, ok, isField && field.Optional)
		}
		return true
	})
	return members, ok
}

// memberBlock is one group of members a type declares: the elements of an object type,
// the body of a class, or the elements of a tuple. Only the slice for its kind is set,
// and tuple marks a tuple, whose element list may be empty. typeParams holds the type
// parameters in scope for the block, which are those of the declaration it came from.
type memberBlock struct {
	objElems   []ast.ObjTypeAnnElem
	classElems []ast.ClassElem
	tupleElems []ast.TypeAnn
	tuple      bool
	typeParams set.Set[string]
}

// walkMembers calls visit with each block of members the type t declares, including the
// blocks it inherits. It follows a reference to an alias, an interface's `extends`
// clause, and a class's superclass and the interfaces it implements, and it visits each
// member of an intersection. An interface or class visits its inherited blocks before
// its own.
//
// It returns false as soon as visit does, and also when t is not a type whose members
// idx can list. That covers a name in typeParams, a name idx does not declare, a
// reference already in seen, and any type that is not an object type, tuple,
// intersection or reference, such as a union. seen holds the declarations already
// being walked, and walkMembers restores it before returning.
func (idx *typeIndex) walkMembers(t ast.TypeAnn, typeParams, seen set.Set[string], visit func(memberBlock) bool) bool {
	switch t := t.(type) {
	case *ast.ObjectTypeAnn:
		return visit(memberBlock{objElems: t.Elems, typeParams: typeParams})
	case *ast.TupleTypeAnn:
		return visit(memberBlock{tupleElems: t.Elems, tuple: true, typeParams: typeParams})
	case *ast.IntersectionTypeAnn:
		for _, member := range t.Types {
			if !idx.walkMembers(member, typeParams, seen, visit) {
				return false
			}
		}
		return true
	case *ast.TypeRefTypeAnn:
		name := ast.QualIdentToString(t.Name)
		if typeParams.Contains(name) || seen.Contains(name) {
			return false
		}
		seen.Add(name)
		defer seen.Remove(name)
		if decl, ok := idx.interfaces[name]; ok {
			declParams := typeParamNameSet(decl.TypeParams)
			for _, ext := range decl.Extends {
				if !idx.walkMembers(ext, declParams, seen, visit) {
					return false
				}
			}
			return decl.TypeAnn == nil || visit(memberBlock{objElems: decl.TypeAnn.Elems, typeParams: declParams})
		}
		if decl, ok := idx.classes[name]; ok {
			declParams := typeParamNameSet(decl.TypeParams)
			supers := decl.Implements
			if decl.Extends != nil {
				supers = append([]*ast.TypeRefTypeAnn{decl.Extends}, supers...)
			}
			for _, super := range supers {
				if !idx.walkMembers(super, declParams, seen, visit) {
					return false
				}
			}
			return visit(memberBlock{classElems: decl.Body, typeParams: declParams})
		}
		if decl, ok := idx.aliases[name]; ok {
			return idx.walkMembers(decl.TypeAnn, typeParamNameSet(decl.TypeParams), seen, visit)
		}
	}
	return false
}

// objElemNames adds the members of an object type to members. A call or construct
// signature declares no member name.
func objElemNames(members memberSet, elems []ast.ObjTypeAnnElem) memberSet {
	for _, elem := range elems {
		switch e := elem.(type) {
		case *ast.CallableTypeAnn, *ast.ConstructorTypeAnn:
			continue
		case *ast.PropertyTypeAnn:
			slot, ok := objElemSlot(e)
			members = members.add(slot, ok, e.Optional)
		case *ast.MethodTypeAnn:
			slot, ok := objElemSlot(e)
			members = members.add(slot, ok, e.Optional)
		default:
			slot, ok := objElemSlot(e)
			members = members.add(slot, ok, false)
		}
	}
	return members
}

// merge returns m with the members of other added.
func (m memberSet) merge(other memberSet) memberSet {
	m.all = m.all.Union(other.all)
	m.required = m.required.Union(other.required)
	m.open = m.open || other.open
	return m
}

// isObjectType reports whether t is an object type: an object literal type that is not
// only call signatures, a tuple, an intersection of object types, or a reference to an
// interface, class or type alias that declares one. A name in typeParams is a type
// parameter and is not an object type.
func (idx *typeIndex) isObjectType(t ast.TypeAnn, typeParams set.Set[string]) bool {
	return idx.isObjectTypeSeen(t, typeParams, set.NewSet[string]())
}

// isObjectTypeSeen is isObjectType with the alias names already being resolved in seen,
// which stops a recursive alias.
func (idx *typeIndex) isObjectTypeSeen(t ast.TypeAnn, typeParams, seen set.Set[string]) bool {
	switch t := t.(type) {
	case *ast.ObjectTypeAnn:
		return !onlyCallables(t)
	case *ast.TupleTypeAnn:
		return true
	case *ast.IntersectionTypeAnn:
		for _, member := range t.Types {
			if !idx.isObjectTypeSeen(member, typeParams, seen) {
				return false
			}
		}
		return len(t.Types) > 0
	case *ast.TypeRefTypeAnn:
		name := ast.QualIdentToString(t.Name)
		if typeParams.Contains(name) || valueTypeNames.Contains(name) {
			return false
		}
		if decl, ok := idx.interfaces[name]; ok {
			return len(decl.Extends) > 0 || decl.TypeAnn == nil || !onlyCallables(decl.TypeAnn)
		}
		if _, ok := idx.classes[name]; ok {
			return true
		}
		if decl, ok := idx.aliases[name]; ok && !seen.Contains(name) {
			seen.Add(name)
			defer seen.Remove(name)
			return idx.isObjectTypeSeen(decl.TypeAnn, typeParamNameSet(decl.TypeParams), seen)
		}
	}
	return false
}

// isValueType reports whether t is a value type: a primitive, a literal, a function, a
// promise, or a union of these. A name in typeParams is a type parameter, which may be
// bound to an object type, so it is not one. seen holds the aliases already being
// resolved.
func (idx *typeIndex) isValueType(t ast.TypeAnn, typeParams, seen set.Set[string]) bool {
	switch t := t.(type) {
	case *ast.NumberTypeAnn, *ast.StringTypeAnn, *ast.BooleanTypeAnn, *ast.SymbolTypeAnn,
		*ast.UniqueSymbolTypeAnn, *ast.BigintTypeAnn, *ast.LitTypeAnn, *ast.NeverTypeAnn,
		*ast.TemplateLitTypeAnn, *ast.FuncTypeAnn, *ast.KeyOfTypeAnn:
		return true
	case *ast.UnionTypeAnn:
		for _, member := range t.Types {
			if !idx.isValueType(member, typeParams, seen) {
				return false
			}
		}
		return true
	case *ast.ObjectTypeAnn:
		return onlyCallables(t)
	case *ast.TypeRefTypeAnn:
		name := ast.QualIdentToString(t.Name)
		if typeParams.Contains(name) {
			return false
		}
		if valueTypeNames.Contains(name) {
			return true
		}
		if decl, ok := idx.interfaces[name]; ok {
			return len(decl.Extends) == 0 && decl.TypeAnn != nil && onlyCallables(decl.TypeAnn)
		}
		if decl, ok := idx.aliases[name]; ok && !seen.Contains(name) {
			seen.Add(name)
			defer seen.Remove(name)
			return idx.isValueType(decl.TypeAnn, typeParamNameSet(decl.TypeParams), seen)
		}
	}
	return false
}

// holdsOnlyValueTypes reports whether every slot of the object type t holds a value type.
// A property, a getter, an index signature and a tuple element are slots. A method is
// not. An inherited slot counts, and a slot whose type is a type parameter does not hold
// a value type. seen holds the declarations already being walked.
func (idx *typeIndex) holdsOnlyValueTypes(t ast.TypeAnn, typeParams, seen set.Set[string]) bool {
	return idx.walkMembers(t, typeParams, seen, func(block memberBlock) bool {
		for _, elem := range block.tupleElems {
			if !idx.isValueType(elem, block.typeParams, seen) {
				return false
			}
		}
		return idx.elemsHoldOnlyValueTypes(block.objElems, block.typeParams, seen) &&
			idx.classElemsHoldOnlyValueTypes(block.classElems, block.typeParams, seen)
	})
}

// elemsHoldOnlyValueTypes is holdsOnlyValueTypes for the members of an object type.
func (idx *typeIndex) elemsHoldOnlyValueTypes(elems []ast.ObjTypeAnnElem, typeParams, seen set.Set[string]) bool {
	for _, elem := range elems {
		switch e := elem.(type) {
		case *ast.PropertyTypeAnn:
			if !idx.isValueType(e.Value, typeParams, seen) {
				return false
			}
		case *ast.GetterTypeAnn:
			if e.Fn.Return == nil || !idx.isValueType(e.Fn.Return, typeParams, seen) {
				return false
			}
		case *ast.MappedTypeAnn:
			if !idx.isValueType(e.Value, typeParams, seen) {
				return false
			}
		case *ast.RestSpreadTypeAnn:
			return false
		}
	}
	return true
}

// classElemsHoldOnlyValueTypes is holdsOnlyValueTypes for the instance fields and
// getters a class body declares. A static member is not a slot of an instance.
func (idx *typeIndex) classElemsHoldOnlyValueTypes(elems []ast.ClassElem, typeParams, seen set.Set[string]) bool {
	for _, elem := range elems {
		switch e := elem.(type) {
		case *ast.FieldElem:
			if !e.Static && (e.Type == nil || !idx.isValueType(e.Type, typeParams, seen)) {
				return false
			}
		case *ast.GetterElem:
			if !e.Static && (e.Fn == nil || e.Fn.Return == nil || !idx.isValueType(e.Fn.Return, typeParams, seen)) {
				return false
			}
		}
	}
	return true
}

// onlyCallables reports whether t has at least one member and every member is a call
// signature, which makes it a function type.
func onlyCallables(t *ast.ObjectTypeAnn) bool {
	if len(t.Elems) == 0 {
		return false
	}
	for _, elem := range t.Elems {
		if _, ok := elem.(*ast.CallableTypeAnn); !ok {
			return false
		}
	}
	return true
}

// freshLifetimeName returns the first of `a`, `b`, … that no parameter in params declares.
func freshLifetimeName(params []*ast.LifetimeParam) string {
	taken := set.NewSet[string]()
	for _, p := range params {
		taken.Add(p.Name)
	}
	for c := 'a'; ; c++ {
		if name := string(c); !taken.Contains(name) {
			return name
		}
	}
}
