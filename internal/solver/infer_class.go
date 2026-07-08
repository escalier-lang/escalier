package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/provenance"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// inferClassDecl types a non-recursive class declaration (M5 B1). It returns the
// class's constructor as a raw FuncType for the SCC driver to constrain into the
// value binding var and generalize, along with the decl's provenance. As side
// effects it registers the instance TypeBinding in scope and the ClassDef in the
// nominal registry, so the class name resolves as a type and member lookup and the
// nominal constrain rule can read the projected body.
//
// Every field is built first, so a method body may read any field through `self`.
// Each method, getter, and setter is then inferred fully and appended to the shared
// instance Body, so a later member may reference an earlier one through `self`. Once
// every body has refined the field vars, a non-generic body is coalesced so member
// lookup reads concrete member types; a generic body keeps its parameter vars
// symbolic for per-instance projection.
func (c *checker) inferClassDecl(scope *Scope, lvl int, decl *ast.ClassDecl) (soltype.Type, provenance.Provenance, bool) {
	name := decl.Name.Name

	// Resolve the class's type parameters into a child scope so the body resolves the
	// class's T to one shared var, quantified at the class boundary and freshened per
	// construction. A non-generic class reuses the enclosing scope.
	declScope := scope
	var typeParams []*soltype.TypeParam
	if len(decl.TypeParams) > 0 {
		declScope = scope.Child()
		typeParams = c.resolveClassTypeParams(declScope, lvl, decl.TypeParams)
	}

	// The instance's nominal identity, carrying the class's own type-parameter vars as
	// its arguments. B1 uses the bare local name as the qualified key, correct for the
	// top-level default namespace; namespace-qualified keys ride the namespace work.
	self := &soltype.ClassType{Name: name, TypeArgs: typeParamVars(typeParams)}

	body := &soltype.ObjectType{}
	static := &soltype.ObjectType{}
	def := &ClassDef{
		Body:       body,
		Static:     static,
		Level:      lvl - 1,
		TypeParams: typeParams,
		Variance:   make([]Variance, len(typeParams)),
	}
	c.ctx.registerClass(name, def)
	// Register the type binding early so a self-referential type in the body resolves
	// to this class rather than falling through as unknown.
	scope.defineType(name, TypeBinding{
		Type:    self,
		Sources: []provenance.Provenance{&ast.NodeProvenance{Node: decl}},
	})
	c.recordType(decl.Name, self)

	// Resolve the declared supers so C1 can walk them; B1 records the edges only.
	def.Supers = c.resolveClassSupers(declScope, lvl, decl)

	// Build every field first so a method body may read any field through `self`,
	// then infer each method, getter, and setter — each fully, so its body refines its
	// own signature — appending it to the body as it goes, so a later member may
	// reference an earlier one through `self`. Intra-class forward references to a
	// later member are out of B1's scope.
	ctors := c.collectConstructors(decl)
	c.buildFieldSigs(declScope, lvl, decl, body, static)
	c.inferMembers(declScope, lvl, decl, self, body, static)
	ctorType := c.inferConstructor(declScope, lvl, decl, self, body, ctors)

	// A generic class keeps its member types symbolic — a field typed `T` stays the
	// class type-parameter var so member lookup can substitute an instance's argument
	// for it. Freezing would coalesce that unconstrained var to `never`. A non-generic
	// class has no such vars, so its body is coalesced to concrete member types.
	if len(typeParams) == 0 {
		c.freezeClassBody(body)
		c.freezeClassBody(static)
	}

	return ctorType, &ast.NodeProvenance{Node: decl}, true
}

// resolveClassTypeParams resolves a class's AST type parameters to soltype
// TypeParams, minting one shared var per parameter, recording its constraint as the
// var's upper bound and its resolved default, and declaring each into scope so the
// class body resolves the parameter name to that var.
func (c *checker) resolveClassTypeParams(scope *Scope, lvl int, params []*ast.TypeParam) []*soltype.TypeParam {
	out := make([]*soltype.TypeParam, len(params))
	for i, p := range params {
		v := c.freshAt(lvl)
		// Declare the parameter before resolving its own constraint and default so an
		// F-bounded `<T: Foo<T>>` or a defaulting `<T, U = T>` reference resolves to it.
		scope.defineType(p.Name, TypeBinding{Type: v})
		if p.Constraint != nil {
			if ct, ok := c.resolveClassTypeAnn(scope, p.Constraint, lvl); ok {
				c.ctx.addUpperBound(v, ct)
			}
		}
		var def soltype.Type
		if p.Default != nil {
			if dt, ok := c.resolveClassTypeAnn(scope, p.Default, lvl); ok {
				def = dt
			}
		}
		out[i] = &soltype.TypeParam{Name: p.Name, Var: v, Default: def}
	}
	return out
}

// typeParamVars returns each type parameter's var, the arguments a class's own
// instance carries — the Self reference `Box<T>` fills its TypeArgs with the T vars.
func typeParamVars(params []*soltype.TypeParam) []soltype.Type {
	if len(params) == 0 {
		return nil
	}
	args := make([]soltype.Type, len(params))
	for i, p := range params {
		args[i] = p.Var
	}
	return args
}

// resolveClassSupers resolves a class's `extends` superclass and `implements`
// interfaces to their ClassTypes, in that order, dropping any that do not resolve to
// a class. B1 records these edges on ClassDef.Supers; the transitive subtype walk and
// the structural implements check land in C1.
func (c *checker) resolveClassSupers(scope *Scope, lvl int, decl *ast.ClassDecl) []*soltype.ClassType {
	var supers []*soltype.ClassType
	add := func(ref *ast.TypeRefTypeAnn) {
		if ref == nil {
			return
		}
		if ct := c.resolveClassRef(scope, ref); ct != nil {
			supers = append(supers, ct)
		}
	}
	add(decl.Extends)
	for _, impl := range decl.Implements {
		add(impl)
	}
	return supers
}

// resolveClassRef resolves a type reference that names a class to its ClassType, or
// nil when the name is not a registered class. B1 consults the type scope directly
// rather than routing through resolveTypeAnn, whose general TypeRef resolution lands
// with the alias work.
func (c *checker) resolveClassRef(scope *Scope, ref *ast.TypeRefTypeAnn) *soltype.ClassType {
	name := ast.QualIdentToString(ref.Name)
	if b, ok := scope.GetType(name); ok {
		if ct, ok := b.Type.(*soltype.ClassType); ok {
			return ct
		}
	}
	return nil
}

// resolveClassTypeAnn resolves a type annotation appearing in a class body. It first
// consults the type scope for a bare reference to a class or type parameter — the two
// names resolveTypeAnn's general TypeRef resolution does not yet cover — and otherwise
// delegates to resolveTypeAnn for primitives and structural types.
func (c *checker) resolveClassTypeAnn(scope *Scope, ann ast.TypeAnn, lvl int) (soltype.Type, bool) {
	if ref, ok := ann.(*ast.TypeRefTypeAnn); ok && len(ref.TypeArgs) == 0 {
		name := ast.QualIdentToString(ref.Name)
		if b, ok := scope.GetType(name); ok {
			return b.Type, true
		}
	}
	return c.resolveTypeAnn(ann, lvl)
}

// collectConstructors returns the explicit constructor elements of a class, reporting
// each one past the first as a duplicate. A well-formed class has zero or one.
func (c *checker) collectConstructors(decl *ast.ClassDecl) []*ast.ConstructorElem {
	var ctors []*ast.ConstructorElem
	for _, elem := range decl.Body {
		if ctor, ok := elem.(*ast.ConstructorElem); ok {
			if len(ctors) >= 1 {
				c.report(&MultipleConstructorsError{Ctor: ctor})
				continue
			}
			ctors = append(ctors, ctor)
		}
	}
	return ctors
}

// buildFieldSigs adds one PropertyElem per field to the instance or static body,
// resolving each field's annotation or minting a fresh var when it is unannotated. An
// instance field carrying an initializer is rejected — instance fields are set in the
// constructor — while a static field's initializer is inferred and checked against the
// field's declared type.
func (c *checker) buildFieldSigs(scope *Scope, lvl int, decl *ast.ClassDecl, body, static *soltype.ObjectType) {
	for _, elem := range decl.Body {
		field, ok := elem.(*ast.FieldElem)
		if !ok {
			continue
		}
		fieldName, ok := objKeyName(field.Name)
		if !ok {
			continue
		}
		var fieldType soltype.Type
		if field.Type != nil {
			if t, ok := c.resolveClassTypeAnn(scope, field.Type, lvl); ok {
				fieldType = t
			} else {
				fieldType = c.freshAt(lvl)
			}
		} else {
			fieldType = c.freshAt(lvl)
		}
		if field.Value != nil {
			if field.Static {
				// A static field's initializer must fit its declared type, so
				// `static x: number = "hi"` is rejected.
				initType := c.inferExpr(scope, lvl, field.Value)
				c.constrain(field.Value, initType, fieldType)
			} else {
				c.report(&FieldInitializerNotAllowedError{Field: field})
			}
		}
		prop := &soltype.PropertyElem{
			Name:     fieldName,
			Type:     fieldType,
			Optional: field.Optional,
			Readonly: field.Readonly,
		}
		if field.Static {
			static.Elems = append(static.Elems, prop)
		} else {
			body.Elems = append(body.Elems, prop)
		}
	}
}

// inferMembers infers each method, getter, and setter of a class fully — its body
// refines its own signature through the shared inferFunc core — and appends the
// resulting element to the instance or static body. An instance member missing its
// `self` receiver is reported. Each member is appended before the next is inferred, so
// a later member may reference an earlier one through `self`.
func (c *checker) inferMembers(scope *Scope, lvl int, decl *ast.ClassDecl, self *soltype.ClassType, body, static *soltype.ObjectType) {
	for _, elem := range decl.Body {
		switch elem := elem.(type) {
		case *ast.MethodElem:
			name, ok := objKeyName(elem.Name)
			if !ok {
				continue
			}
			c.checkSelfReceiver(name, elem, elem.Static, elem.Receiver)
			ft := c.inferMemberFunc(scope, lvl, elem.Fn, elem.Receiver, elem.Static, self, body)
			ft.SelfParam = c.selfParam(elem.Receiver, elem.Static, self)
			appendMethodSig(targetBody(body, static, elem.Static), name, ft, elem.Static)
		case *ast.GetterElem:
			name, ok := objKeyName(elem.Name)
			if !ok {
				continue
			}
			c.checkSelfReceiver(name, elem, elem.Static, elem.Receiver)
			ft := c.inferMemberFunc(scope, lvl, elem.Fn, elem.Receiver, elem.Static, self, body)
			getter := &soltype.GetterElem{Name: name, SelfParam: c.selfParam(elem.Receiver, elem.Static, self), Type: ft.Ret}
			target := targetBody(body, static, elem.Static)
			target.Elems = append(target.Elems, getter)
		case *ast.SetterElem:
			name, ok := objKeyName(elem.Name)
			if !ok {
				continue
			}
			c.checkSelfReceiver(name, elem, elem.Static, elem.Receiver)
			ft := c.inferMemberFunc(scope, lvl, elem.Fn, elem.Receiver, elem.Static, self, body)
			var param soltype.Type = &soltype.UnknownType{}
			if len(ft.Params) > 0 {
				param = ft.Params[0].Type
			}
			setter := &soltype.SetterElem{Name: name, SelfParam: c.selfParam(elem.Receiver, elem.Static, self), Param: param}
			target := targetBody(body, static, elem.Static)
			target.Elems = append(target.Elems, setter)
		}
	}
}

// targetBody selects the static or instance body for a member.
func targetBody(body, static *soltype.ObjectType, isStatic bool) *soltype.ObjectType {
	if isStatic {
		return static
	}
	return body
}

// checkSelfReceiver reports a non-static instance member that omits its `self`
// receiver.
func (c *checker) checkSelfReceiver(name string, elem ast.ClassElem, static bool, recv *ast.MethodReceiver) {
	if !static && recv == nil {
		c.report(&MissingSelfReceiverError{Name: name, Elem: elem})
	}
}

// inferMemberFunc infers one member body via the shared inferFunc core, binding `self`
// to the instance body — owned-mutable for a `mut self` receiver — so field reads and
// writes inside the body resolve through the record machinery. It returns the inferred
// FuncType, whose params and return the caller installs on the member element.
func (c *checker) inferMemberFunc(scope *Scope, lvl int, fn *ast.FuncExpr, recv *ast.MethodReceiver, static bool, self *soltype.ClassType, body *soltype.ObjectType) *soltype.FuncType {
	memberScope := scope.Child()
	if !static {
		c.bindSelf(memberScope, recv, body)
	}
	return c.inferFunc(memberScope, lvl, fn.FuncSig, fn.Body, fn)
}

// appendMethodSig installs a method signature under name, merging it into an existing
// same-named MethodElem as an overload arm rather than adding a second element.
func appendMethodSig(obj *soltype.ObjectType, name string, sig *soltype.FuncType, static bool) {
	for _, e := range obj.Elems {
		if m, ok := e.(*soltype.MethodElem); ok && m.Name == name {
			m.Signatures = append(m.Signatures, sig)
			return
		}
	}
	obj.Elems = append(obj.Elems, &soltype.MethodElem{Name: name, Signatures: []*soltype.FuncType{sig}, Static: static})
}

// selfParam builds the SelfParam a member's signature carries, or nil for a static
// member. A `mut self` receiver wraps the instance in an owned-mutable borrow; a plain
// `self` carries the bare instance.
func (c *checker) selfParam(recv *ast.MethodReceiver, static bool, self *soltype.ClassType) *soltype.FuncParam {
	if static || recv == nil {
		return nil
	}
	return &soltype.FuncParam{Pattern: &soltype.IdentPat{Name: "self"}, Type: c.selfType(recv, self)}
}

// selfType returns the type the `self` binding takes in a member body: an
// owned-mutable borrow of the instance for `mut self`, the bare instance otherwise.
func (c *checker) selfType(recv *ast.MethodReceiver, self soltype.Type) soltype.Type {
	if recv != nil && recv.Mut {
		return soltype.NewRef(true, nil, self.(soltype.RefInner))
	}
	return self
}

// bindSelf binds the `self` identifier in a member body scope to the instance's
// fields, wrapped owned-mutable for a `mut self` receiver so field writes type-check.
//
// `self` carries only the field members, not the methods: field reads and writes flow
// through the record subtyping machinery, whose object arm reads only properties and
// would panic on a method member. A field-only `self` therefore keeps that path sound.
// A method calling a sibling method through `self` is out of B1's scope; external
// member access still reaches every member through the projected class body.
func (c *checker) bindSelf(scope *Scope, recv *ast.MethodReceiver, body *soltype.ObjectType) {
	fields := &soltype.ObjectType{Elems: fieldElems(body)}
	var selfBody soltype.Type = fields
	if recv != nil && recv.Mut {
		selfBody = soltype.NewRef(true, nil, fields)
	}
	scope.defineValue("self", ValueBinding{Schemes: []TypeScheme{monoScheme(selfBody)}})
}

// fieldElems returns the property members of a class body, sharing each PropertyElem
// pointer so a write through `self` refines the same field type the projected body
// reads.
func fieldElems(body *soltype.ObjectType) []soltype.ObjTypeElem {
	var fields []soltype.ObjTypeElem
	for _, e := range body.Elems {
		if _, ok := e.(*soltype.PropertyElem); ok {
			fields = append(fields, e)
		}
	}
	return fields
}

// freezeClassBody coalesces each member's type in place so member lookup and the
// class-vs-object rule read concrete member types rather than the fresh vars a field
// held before a constructor assignment refined it. Each member is coalesced
// individually rather than the whole object at once, since the object's owned-mut
// bubbling pass reads only PropertyElems and would panic on a method member.
func (c *checker) freezeClassBody(obj *soltype.ObjectType) {
	for i, e := range obj.Elems {
		switch e := e.(type) {
		case *soltype.PropertyElem:
			obj.Elems[i] = &soltype.PropertyElem{Name: e.Name, Type: coalesce(e.Type, soltype.Positive), Optional: e.Optional, Readonly: e.Readonly}
		case *soltype.GetterElem:
			obj.Elems[i] = &soltype.GetterElem{Name: e.Name, SelfParam: e.SelfParam, Type: coalesce(e.Type, soltype.Positive)}
		case *soltype.SetterElem:
			obj.Elems[i] = &soltype.SetterElem{Name: e.Name, SelfParam: e.SelfParam, Param: coalesce(e.Param, soltype.Negative)}
		case *soltype.MethodElem:
			sigs := make([]*soltype.FuncType, len(e.Signatures))
			for j, sig := range e.Signatures {
				if cs, ok := coalesce(sig, soltype.Positive).(*soltype.FuncType); ok {
					sigs[j] = cs
				} else {
					sigs[j] = sig
				}
			}
			obj.Elems[i] = &soltype.MethodElem{Name: e.Name, Signatures: sigs, Static: e.Static}
		}
	}
}
