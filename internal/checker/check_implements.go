package checker

import (
	"slices"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// checkImplements verifies that classObj structurally satisfies each
// `implements I` clause recorded on it. For every member declared on the
// resolved interface body, it looks up a matching member on the class
// (walking `extends` for inherited members) and reports
// ClassDoesNotImplementInterfaceError when one is missing or has a
// mismatched signature.
func (c *Checker) checkImplements(
	ctx Context,
	decl *ast.ClassDecl,
	classObj *type_system.ObjectType,
) []Error {
	var errors []Error
	for _, ifaceRef := range classObj.Implements {
		ifaceName := type_system.QualIdentToString(ifaceRef.Name)
		span := decl.Span()
		for _, implAnn := range decl.Implements {
			if implAnn != nil && ast.QualIdentToString(implAnn.Name) == ifaceName {
				span = implAnn.Span()
				break
			}
		}
		errors = slices.Concat(errors, c.checkImplementsOne(ctx, decl, classObj, ifaceRef, span))
	}
	return errors
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
	for _, ifaceElem := range ifaceObj.Elems {
		errors = slices.Concat(errors,
			c.checkInterfaceElem(ctx, classObj, ifaceElem, className, ifaceName, sub, span))
	}
	return errors
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

func (c *Checker) checkInterfaceElem(
	ctx Context,
	classObj *type_system.ObjectType,
	ifaceElem type_system.ObjTypeElem,
	className, ifaceName string,
	sub map[string]type_system.Type,
	span ast.Span,
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
		ce := findElemByKey(ctx, c, classObj, ie.Name)
		if ce == nil {
			return missingMember(span, className, ifaceName, ie.Name.String())
		}
		ifaceFn := SubstituteTypeParams(ie.Fn, sub)
		switch m := ce.(type) {
		case *type_system.MethodElem:
			if !selfReceiverCompatible(ie.Fn, m.Fn) {
				return mismatchedMember(span, className, ifaceName, ie.Name.String(),
					"self receiver does not match")
			}
			if errs := c.unifyFuncTypes(ctx, m.Fn, ifaceFn, make(unifySeen)); len(errs) > 0 {
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
			return c.VerifyLifetimeCompatibility(ifaceFn, m.Fn, span)
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
		ce := findElemByKey(ctx, c, classObj, ie.Name)
		if ce == nil {
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
		ce := findElemByKey(ctx, c, classObj, ie.Name)
		if ce == nil {
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
		ce := findElemByKey(ctx, c, classObj, ie.Name)
		if ce == nil {
			if ie.Optional {
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
