package dts_to_esc

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/printer"
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
// holds a value type and the two types print differently.
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
	typeParams := outerTypeParams.Union(typeParamNameSet(sig.TypeParams))
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
	ret, retRefs := idx.borrowObjectTypes(sig.Return, typeParams)
	var shared []*ast.RefTypeAnn
	for _, ref := range borrowed {
		if idx.mayShareWithAny(ref.Inner, retRefs, typeParams) {
			shared = append(shared, ref)
		}
	}
	if len(shared) == 0 {
		return
	}
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
// and the two types print differently.
func (idx *typeIndex) mayShareStructure(param, ret ast.TypeAnn, typeParams set.Set[string]) bool {
	if !idx.holdsOnlyValueTypes(param, typeParams, set.NewSet[string]()) {
		return true
	}
	paramText, errParam := printer.Print(param, printer.DefaultOptions())
	retText, errRet := printer.Print(ret, printer.DefaultOptions())
	return errParam != nil || errRet != nil || paramText == retText
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
	switch t := t.(type) {
	case *ast.ObjectTypeAnn:
		return idx.elemsHoldOnlyValueTypes(t.Elems, typeParams, seen)
	case *ast.TupleTypeAnn:
		for _, elem := range t.Elems {
			if !idx.isValueType(elem, typeParams, seen) {
				return false
			}
		}
		return true
	case *ast.IntersectionTypeAnn:
		for _, member := range t.Types {
			if !idx.holdsOnlyValueTypes(member, typeParams, seen) {
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
				if !idx.holdsOnlyValueTypes(ext, declParams, seen) {
					return false
				}
			}
			return decl.TypeAnn == nil || idx.elemsHoldOnlyValueTypes(decl.TypeAnn.Elems, declParams, seen)
		}
		if decl, ok := idx.classes[name]; ok {
			return idx.classHoldsOnlyValueTypes(decl, seen)
		}
		if decl, ok := idx.aliases[name]; ok {
			return idx.holdsOnlyValueTypes(decl.TypeAnn, typeParamNameSet(decl.TypeParams), seen)
		}
	}
	return false
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

// classHoldsOnlyValueTypes is holdsOnlyValueTypes for the instance slots of a class,
// including the ones its superclass declares.
func (idx *typeIndex) classHoldsOnlyValueTypes(decl *ast.ClassDecl, seen set.Set[string]) bool {
	typeParams := typeParamNameSet(decl.TypeParams)
	if decl.Extends != nil && !idx.holdsOnlyValueTypes(decl.Extends, typeParams, seen) {
		return false
	}
	for _, elem := range decl.Body {
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
