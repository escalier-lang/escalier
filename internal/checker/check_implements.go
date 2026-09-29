package checker

import (
	"slices"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// checkImplements checks each `implements I` clause on classObj. The keyword
// means two things.
//
// A class with a body has to satisfy the interface, and a missing or
// mismatched member reports ClassDoesNotImplementInterfaceError. A `declare`
// class takes the interface's members instead, so a member it does not resolve
// itself is inherited rather than missing. One it does resolve is a narrowing
// override, still checked for assignability to the interface's.
func (c *Checker) checkImplements(
	ctx Context,
	decl *ast.ClassDecl,
	classObj *type_system.ObjectType,
) []Error {
	var errors []Error
	for _, ifaceRef := range classObj.Implements {
		errors = slices.Concat(errors,
			c.checkImplementsOne(ctx, decl, classObj, ifaceRef, implementsSpan(decl, ifaceRef)))
	}
	if decl.Declare() {
		errors = slices.Concat(errors, c.checkContributedConflicts(ctx, decl, classObj))
	}
	return errors
}

// implementsSpan returns the span of the clause entry naming ifaceRef, so a
// diagnostic points at that interface rather than the whole declaration. It
// falls back to the declaration's span when no entry matches by name.
func implementsSpan(decl *ast.ClassDecl, ifaceRef *type_system.TypeRefType) ast.Span {
	ifaceName := type_system.QualIdentToString(ifaceRef.Name)
	for _, implAnn := range decl.Implements {
		if implAnn != nil && ast.QualIdentToString(implAnn.Name) == ifaceName {
			return implAnn.Span()
		}
	}
	return decl.Span()
}

// checkContributedConflicts reports a member name two implemented interfaces
// declare with types that do not agree, which member lookup would otherwise
// resolve to whichever comes first. A name the class settles itself is checked
// by checkImplementsOne instead and takes no part here.
//
// Agreement is mutual assignability, asked through Check rather than Unify so
// the comparison cannot bind a type var on either side.
func (c *Checker) checkContributedConflicts(
	ctx Context,
	decl *ast.ClassDecl,
	classObj *type_system.ObjectType,
) []Error {
	type contribution struct {
		ifaceName string
		elemType  type_system.Type
	}

	var errors []Error
	contributed := map[type_system.ObjTypeKey]contribution{}
	for _, ifaceRef := range classObj.Implements {
		expanded, expandErrors := c.expandTypeRef(ctx, ifaceRef)
		if len(expandErrors) > 0 {
			continue
		}
		ifaceObj, ok := type_system.Prune(expanded).(*type_system.ObjectType)
		if !ok {
			continue
		}
		ifaceName := type_system.QualIdentToString(ifaceRef.Name)
		for _, ifaceElem := range c.collectInterfaceElems(ctx, ifaceObj, set.NewSet[*type_system.ObjectType]()) {
			key, ok := elemKey(ifaceElem)
			if !ok {
				continue
			}
			elemType := contributedElemType(ifaceElem)
			if elemType == nil {
				continue
			}
			first, seen := contributed[key]
			if !seen {
				contributed[key] = contribution{ifaceName: ifaceName, elemType: elemType}
				continue
			}
			if first.ifaceName == ifaceName {
				continue
			}
			if c.findClassElem(ctx, classObj, key, true) != nil {
				continue
			}
			if c.Check(ctx, elemType, first.elemType) &&
				c.Check(ctx, first.elemType, elemType) {
				continue
			}
			errors = append(errors, &ConflictingInterfaceMembersError{
				ClassName:   decl.Name.Name,
				FirstIface:  first.ifaceName,
				SecondIface: ifaceName,
				MemberName:  key.String(),
				span:        implementsSpan(decl, ifaceRef),
			})
		}
	}
	return errors
}

// contributedElemType returns the type an element contributes under a member
// name, or nil for one with no single type to compare. An overloaded method
// contributes nothing, since comparing arm by arm is deferred to #651.
func contributedElemType(elem type_system.ObjTypeElem) type_system.Type {
	switch e := elem.(type) {
	case *type_system.MethodElem:
		if len(e.Signatures) != 1 {
			return nil
		}
		return withOptional(e.Signatures[0], e.Optional)
	case *type_system.GetterElem:
		return e.Fn.Return
	case *type_system.SetterElem:
		return setterArgType(e.Fn)
	case *type_system.PropertyElem:
		return withOptional(e.Value, e.Optional)
	}
	return nil
}

// withOptional widens an optional member's type with `undefined`, so an
// optional member and a required one of the same type do not compare equal.
func withOptional(t type_system.Type, optional bool) type_system.Type {
	if !optional {
		return t
	}
	return type_system.NewUnionType(nil, t, type_system.NewUndefinedType(nil))
}

func (c *Checker) checkImplementsOne(
	ctx Context,
	decl *ast.ClassDecl,
	classObj *type_system.ObjectType,
	ifaceRef *type_system.TypeRefType,
	span ast.Span,
) []Error {
	expanded, expandErrors := c.expandTypeRef(ctx, ifaceRef)
	if len(expandErrors) > 0 {
		return expandErrors
	}
	ifaceObj, ok := type_system.Prune(expanded).(*type_system.ObjectType)
	if !ok {
		return nil
	}

	className := decl.Name.Name
	ifaceName := type_system.QualIdentToString(ifaceRef.Name)

	// Build a `Self` substitution that rewrites every reference to the
	// interface (by name and via the literal `Self` alias) to a TypeRef
	// for the class. Without this, methods like `clone(self) -> Self`
	// would never match `clone(self) -> Class` because the two are
	// distinct nominal types.
	sub := buildSelfSubstitution(ctx, decl, ifaceName)

	var errors []Error
	for _, ifaceElem := range c.collectInterfaceElems(ctx, ifaceObj, set.NewSet[*type_system.ObjectType]()) {
		errors = slices.Concat(errors,
			c.checkInterfaceElem(ctx, classObj, ifaceElem, className, ifaceName, sub, span, decl.Declare()))
	}
	return errors
}

// collectInterfaceElems returns every member an interface declares, its own
// and those it inherits through its `extends` clause. A redeclared member
// shadows the inherited one, and `seen` stops a cycle in the `extends` graph.
func (c *Checker) collectInterfaceElems(
	ctx Context,
	ifaceObj *type_system.ObjectType,
	seen set.Set[*type_system.ObjectType],
) []type_system.ObjTypeElem {
	if seen.Contains(ifaceObj) {
		return nil
	}
	seen.Add(ifaceObj)

	elems := slices.Clone(ifaceObj.Elems)
	declared := set.NewSet[type_system.ObjTypeKey]()
	for _, elem := range elems {
		if key, ok := elemKey(elem); ok {
			declared.Add(key)
		}
	}

	for _, superRef := range ifaceObj.Extends {
		expanded, expandErrors := c.expandTypeRef(ctx, superRef)
		if len(expandErrors) > 0 {
			continue
		}
		superObj, ok := type_system.Prune(expanded).(*type_system.ObjectType)
		if !ok {
			continue
		}
		for _, elem := range c.collectInterfaceElems(ctx, superObj, seen) {
			key, ok := elemKey(elem)
			if ok && declared.Contains(key) {
				continue
			}
			if ok {
				declared.Add(key)
			}
			elems = append(elems, elem)
		}
	}
	return elems
}

// buildSelfSubstitution returns the substitution map applied to interface
// elements before they're compared to class elements.
func buildSelfSubstitution(ctx Context, decl *ast.ClassDecl, ifaceName string) map[string]type_system.Type {
	classAlias := ctx.Scope.GetTypeAlias(decl.Name.Name)
	var classRef type_system.Type
	if classAlias != nil {
		typeArgs := make([]type_system.Type, len(classAlias.TypeParams))
		for i, tp := range classAlias.TypeParams {
			typeArgs[i] = type_system.NewTypeRefType(nil, tp.Name, nil)
		}
		classRef = type_system.NewTypeRefType(
			&ast.NodeProvenance{Node: decl}, decl.Name.Name, classAlias, typeArgs...)
	} else {
		classRef = type_system.NewTypeRefType(
			&ast.NodeProvenance{Node: decl}, decl.Name.Name, nil)
	}
	return map[string]type_system.Type{
		ifaceName: classRef,
		"Self":    classRef,
	}
}

// checkInterfaceElem compares one interface member against the class.
// `declared` marks a `declare` class, where a member the class does not
// resolve itself is inherited rather than missing.
func (c *Checker) checkInterfaceElem(
	ctx Context,
	classObj *type_system.ObjectType,
	ifaceElem type_system.ObjTypeElem,
	className, ifaceName string,
	sub map[string]type_system.Type,
	span ast.Span,
	declared bool,
) []Error {
	// Direction: every check below asks "is the class member assignable
	// to the interface member?" — i.e. could the class member be used
	// wherever the interface member is expected. This means parameters
	// are contravariant (class may accept wider input than the iface
	// promises) and return types are covariant (class may return
	// narrower output than the iface promises). Setter arguments are
	// the exception: callers *write* through them, so the iface arg
	// must be assignable to the class arg.
	switch ie := ifaceElem.(type) {
	case *type_system.MethodElem:
		ce := c.findClassElem(ctx, classObj, ie.Name, declared)
		if ce == nil {
			if declared {
				return nil
			}
			return missingMember(span, className, ifaceName, ie.Name.String())
		}
		// Comparing an overload arm by arm is deferred to #651, and
		// comparing only the first arm would reject a method that does
		// conform, so an overload on either side goes unchecked. The
		// interop tree reaches this through interfaces such as
		// `CanvasDrawImage`, whose `drawImage` declares three arms.
		if len(ie.Signatures) > 1 {
			return nil
		}
		ieSig := ie.SingleSig()
		ifaceFn := SubstituteTypeParams(ieSig, sub)
		switch m := ce.(type) {
		case *type_system.MethodElem:
			if len(m.Signatures) > 1 {
				return nil
			}
			mSig := m.SingleSig()
			if !selfReceiverCompatible(ieSig, mSig) {
				return mismatchedMember(span, className, ifaceName, ie.Name.String(),
					"self receiver does not match")
			}
			if errs := c.unifyFuncTypes(ctx, mSig, ifaceFn, make(unifySeen)); len(errs) > 0 {
				return mismatchedMember(span, className, ifaceName, ie.Name.String(),
					"signature does not match")
			}
			// Once the method matches structurally, check that the
			// implementation's lifetime relationships are at least as
			// conservative as the interface's. Today this only fires
			// when both sides carry explicit `<'a>` lifetime params on
			// the method signature; interface-method elision (deferred
			// to a later Phase 12 task) will populate them automatically
			// for body-less interfaces.
			//
			// Safe to call unconditionally: when neither side annotates
			// lifetimes, `GetLifetime` returns nil for both the
			// interface and the impl return, and the routine short-
			// circuits to nil. When the structures differ enough that
			// only one side has lifetimes, `unifyFuncTypes` above will
			// already have failed and we won't reach this line.
			return c.VerifyLifetimeCompatibility(ifaceFn, mSig, span)
		case *type_system.PropertyElem:
			if errs := c.Unify(ctx, m.Value, ifaceFn); len(errs) > 0 {
				return mismatchedMember(span, className, ifaceName, ie.Name.String(),
					"property does not satisfy method signature")
			}
		default:
			return mismatchedMember(span, className, ifaceName, ie.Name.String(),
				"is not a method")
		}
	case *type_system.GetterElem:
		ce := c.findClassElem(ctx, classObj, ie.Name, declared)
		if ce == nil {
			if declared {
				return nil
			}
			return missingMember(span, className, ifaceName, ie.Name.String())
		}
		ifaceRet := SubstituteTypeParams(ie.Fn.Return, sub)
		switch m := ce.(type) {
		case *type_system.GetterElem:
			if !selfReceiverCompatible(ie.Fn, m.Fn) {
				return mismatchedMember(span, className, ifaceName, ie.Name.String(),
					"self receiver does not match")
			}
			if errs := c.Unify(ctx, m.Fn.Return, ifaceRet); len(errs) > 0 {
				return mismatchedMember(span, className, ifaceName, ie.Name.String(),
					"getter return type does not match")
			}
		case *type_system.PropertyElem:
			if m.Optional {
				return mismatchedMember(span, className, ifaceName, ie.Name.String(),
					"property is optional but interface requires it")
			}
			if errs := c.Unify(ctx, m.Value, ifaceRet); len(errs) > 0 {
				return mismatchedMember(span, className, ifaceName, ie.Name.String(),
					"property type does not match getter")
			}
		default:
			return mismatchedMember(span, className, ifaceName, ie.Name.String(),
				"is not a getter or property")
		}
	case *type_system.SetterElem:
		ce := c.findClassElem(ctx, classObj, ie.Name, declared)
		if ce == nil {
			if declared {
				return nil
			}
			return missingMember(span, className, ifaceName, ie.Name.String())
		}
		// A setter's input type lives on its single non-self parameter.
		// Setters are write-only: the iface arg must be assignable to
		// the class arg, hence the iface→class direction below.
		ifaceArg := setterArgType(ie.Fn)
		switch m := ce.(type) {
		case *type_system.SetterElem:
			if !selfReceiverCompatible(ie.Fn, m.Fn) {
				return mismatchedMember(span, className, ifaceName, ie.Name.String(),
					"self receiver does not match")
			}
			classArg := setterArgType(m.Fn)
			if ifaceArg != nil && classArg != nil {
				if errs := c.Unify(ctx, SubstituteTypeParams(ifaceArg, sub), classArg); len(errs) > 0 {
					return mismatchedMember(span, className, ifaceName, ie.Name.String(),
						"setter argument type does not match")
				}
			}
		case *type_system.PropertyElem:
			if m.Optional {
				return mismatchedMember(span, className, ifaceName, ie.Name.String(),
					"property is optional but interface requires it")
			}
			if ifaceArg != nil {
				if errs := c.Unify(ctx, SubstituteTypeParams(ifaceArg, sub), m.Value); len(errs) > 0 {
					return mismatchedMember(span, className, ifaceName, ie.Name.String(),
						"property type does not match setter")
				}
			}
		default:
			return mismatchedMember(span, className, ifaceName, ie.Name.String(),
				"is not a setter or property")
		}
	case *type_system.PropertyElem:
		ce := c.findClassElem(ctx, classObj, ie.Name, declared)
		if ce == nil {
			if declared || ie.Optional {
				return nil
			}
			return missingMember(span, className, ifaceName, ie.Name.String())
		}
		cp, ok := ce.(*type_system.PropertyElem)
		if !ok {
			return mismatchedMember(span, className, ifaceName, ie.Name.String(),
				"member is not a property")
		}
		if !ie.Optional && cp.Optional {
			return mismatchedMember(span, className, ifaceName, ie.Name.String(),
				"property is optional but interface requires it")
		}
		ifaceVal := SubstituteTypeParams(ie.Value, sub)
		if errs := c.Unify(ctx, cp.Value, ifaceVal); len(errs) > 0 {
			return mismatchedMember(span, className, ifaceName, ie.Name.String(),
				"property type does not match")
		}
	}
	return nil
}

// selfReceiverCompatible returns true when a class method's self receiver
// satisfies the interface's. The receivers must agree exactly: an interface
// declaring `mut self` is not satisfied by a class method declaring `self`
// (the class loses mutation ability), and vice versa (the class would
// require mutability the interface doesn't promise). A nil receiver (no
// `self`, e.g. a static method) only matches another nil.
func selfReceiverCompatible(ifaceFn, classFn *type_system.FuncType) bool {
	if (ifaceFn.SelfParam == nil) != (classFn.SelfParam == nil) {
		return false
	}
	return type_system.ReceiverIsMut(ifaceFn) == type_system.ReceiverIsMut(classFn)
}

// setterArgType returns the value-input type of a setter signature.
// Setters carry only the value param in `Fn.Params` — `self` is recorded
// separately and is not part of the signature.
func setterArgType(fn *type_system.FuncType) type_system.Type {
	if fn == nil || len(fn.Params) == 0 {
		return nil
	}
	return fn.Params[0].Type
}

// findClassElem returns the class member an interface member is compared
// against, which is what member lookup resolves the name to. On an ordinary
// class that is any inherited member, so the search walks all of `extends`.
//
// On a `declare` class it walks the class body and the superclass chain but
// skips the implemented interfaces, which resolveImplements also records in
// `extends`. Walking those would compare an interface's member with itself.
func (c *Checker) findClassElem(
	ctx Context,
	classObj *type_system.ObjectType,
	key type_system.ObjTypeKey,
	declared bool,
) type_system.ObjTypeElem {
	if !declared {
		return findElemByKey(ctx, c, classObj, key)
	}

	for _, elem := range classObj.Elems {
		if k, ok := elemKey(elem); ok && k == key {
			return elem
		}
	}

	implemented := set.FromSlice(classObj.Implements)
	for _, superRef := range classObj.Extends {
		if implemented.Contains(superRef) {
			continue
		}
		expanded, expandErrors := c.expandTypeRef(ctx, superRef)
		if len(expandErrors) > 0 {
			continue
		}
		superObj, ok := type_system.Prune(expanded).(*type_system.ObjectType)
		if !ok {
			continue
		}
		if elem := findElemByKey(ctx, c, superObj, key); elem != nil {
			return elem
		}
	}
	return nil
}

// findElemByKey looks for a non-callable element with the given key on
// objType, walking `extends` to find inherited members.
func findElemByKey(ctx Context, c *Checker, objType *type_system.ObjectType, key type_system.ObjTypeKey) type_system.ObjTypeElem {
	for _, elem := range objType.Elems {
		if k, ok := elemKey(elem); ok && k == key {
			return elem
		}
	}
	for _, ext := range objType.Extends {
		expanded, _ := c.expandTypeRef(ctx, ext)
		if parent, ok := type_system.Prune(expanded).(*type_system.ObjectType); ok {
			if found := findElemByKey(ctx, c, parent, key); found != nil {
				return found
			}
		}
	}
	return nil
}

func elemKey(elem type_system.ObjTypeElem) (type_system.ObjTypeKey, bool) {
	switch e := elem.(type) {
	case *type_system.MethodElem:
		return e.Name, true
	case *type_system.GetterElem:
		return e.Name, true
	case *type_system.SetterElem:
		return e.Name, true
	case *type_system.PropertyElem:
		return e.Name, true
	}
	return type_system.ObjTypeKey{}, false
}

func missingMember(span ast.Span, className, ifaceName, member string) []Error {
	return []Error{&ClassDoesNotImplementInterfaceError{
		ClassName:     className,
		InterfaceName: ifaceName,
		MemberName:    member,
		Reason:        "missing",
		span:          span,
	}}
}

func mismatchedMember(span ast.Span, className, ifaceName, member, reason string) []Error {
	return []Error{&ClassDoesNotImplementInterfaceError{
		ClassName:     className,
		InterfaceName: ifaceName,
		MemberName:    member,
		Reason:        reason,
		span:          span,
	}}
}
