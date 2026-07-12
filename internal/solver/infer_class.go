package solver

import (
	"fmt"
	"sort"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/graph"
	"github.com/escalier-lang/escalier/internal/provenance"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// inferClassDecl types a class declaration (M5 B1). It returns the class's value type
// for the SCC driver to constrain into the value binding var and generalize, along with
// the decl's provenance. The value is the bare constructor FuncType for a class with no
// statics, or a ctor-plus-static object for one with static members (see classValue). It
// has two side effects. It registers
// the instance TypeBinding in scope, so the class name resolves as a type. It registers
// the ClassDef in the nominal registry, so member lookup and the nominal constrain rule
// can read the projected body.
//
// A class in a mutually-recursive group reuses the nominal token and empty ClassDef the
// SCC driver pre-bound for the group through getOrCreateClass (M5 B2), so a forward
// reference to a sibling defined later in the group resolves before its body is inferred.
// A class reached without a pre-bound pair mints and registers one here.
//
// The member walk is two-phase: every field, method, getter, and setter signature is
// appended to the body first, then each body is walked with `self` bound to the full
// body. So a method calling another method of the same class resolves through the
// pre-declared sibling signature, whether self-recursive, mutually recursive, or a
// forward call to a member declared later.
//
// Once every body has refined the field vars, a non-generic body is coalesced so member
// lookup reads concrete member types. A generic body keeps its parameter vars symbolic
// for per-instance projection.
func (c *checker) inferClassDecl(scope *Scope, lvl int, decl *ast.ClassDecl, ns string) (soltype.Type, provenance.Provenance, bool) {
	// A class-body type reference resolves against the class's own namespace first, so a
	// bare sibling reference such as `start: Point` inside a class in namespace
	// `Geometry` finds `Geometry.Point`. Save and restore around the walk, since a class
	// is only ever inferred at top level.
	prevNS := c.classNamespace
	c.classNamespace = ns
	defer func() { c.classNamespace = prevNS }()

	// Resolve the class's type parameters into a child scope so the body resolves the
	// class's T to one shared var, quantified at the class boundary and freshened per
	// construction. A non-generic class reuses the enclosing scope.
	declScope := scope
	var typeParams []*soltype.TypeParam
	if len(decl.TypeParams) > 0 {
		declScope = scope.Child()
		typeParams = c.resolveTypeParams(declScope, lvl, decl.TypeParams)
	}

	// The instance's nominal identity and its heavy ClassDef. getOrCreateClass returns
	// the pair the SCC pre-pass registered for this class — an empty shell it minted
	// before any type params were resolved, so that a sibling in the same recursive group
	// resolves a forward reference to this class through the shared token (B2). A class
	// reached without a pre-registered shell mints and registers one here. The registry,
	// the minted token, and the scope type binding are all keyed by the namespace-
	// qualified name, so two sibling `class Point` declarations in different namespaces
	// stay distinct.
	self, def := c.getOrCreateClass(scope, decl, ns)
	// Populate the type-param-derived fields the pre-pass left empty. This is the second
	// phase: the pre-pass registers a bare identity so forward references resolve, and
	// this call — running once every sibling is registered, so a bound like `<T: Sibling>`
	// resolves — fills in the resolved type params. The token carries the class's own
	// type-parameter vars as its arguments.
	self.TypeArgs = typeParamVars(typeParams)
	def.Level = lvl - 1
	def.TypeParams = typeParams
	def.Variance = make([]Variance, len(typeParams))
	body := def.Body
	static := def.Static
	c.recordType(decl.Name, self)

	// Resolve the declared extends edge and implements interfaces so C1 can walk and
	// check them; B1 records them only.
	def.Supers = c.resolveClassSupers(declScope, lvl, decl)
	def.Implements = c.resolveClassImplements(declScope, lvl, decl)

	// Two-phase member walk (B3). Phase 1 appends a signature element for every field,
	// method, getter, and setter to the instance or static body before any body is
	// inferred. Phase 2 then walks each method, getter, setter, and the constructor body
	// with `self` bound to the full body, so a call between two methods of the same class
	// resolves through the pre-declared sibling signature — self-recursive, mutually
	// recursive, or a forward call to a member declared later.
	ctors := c.collectConstructors(decl)
	c.buildFieldSigs(declScope, lvl, decl, body, static)
	pending := c.buildMemberSigs(declScope, lvl, decl, self, body, static)
	// A mutually recursive method group with no annotated return cannot ground its own
	// return types, so it is reported before any body runs. Reporting here, not during
	// body inference, keeps the diagnostic off the inferred-never recovery.
	c.checkMethodRecursionAnnotations(decl)
	c.inferMemberBodies(declScope, lvl, body, pending)
	ctorType := c.inferConstructor(declScope, lvl, decl, self, body, ctors)

	// Coalesce each member so lookup reads concrete member types rather than the fresh
	// vars a field held before a constructor assignment refined it. A non-generic class
	// has no type-parameter vars, so its body coalesces fully. A generic class keeps its
	// own type-parameter vars — and each method's own type parameters — symbolic so member
	// lookup can substitute an instance's argument for them (B8): a plain freeze would
	// collapse a member typed through `T`, such as `read(self) { self.v }`, to `never`
	// because the intermediate inference var's only bound is the still-unconstrained `T`.
	if len(typeParams) == 0 {
		c.freezeClassBody(body, nil, nil)
		c.freezeClassBody(static, nil, nil)
	} else {
		keep := classKeepVars(typeParams, body, static)
		flow := keptFlowMap(keep)
		c.freezeClassBody(body, keep, flow)
		c.freezeClassBody(static, keep, flow)
	}

	return c.classValue(ctorType, static), &ast.NodeProvenance{Node: decl}, true
}

// classValue produces the RAW value type a class name binds to. A class with no statics
// binds its bare constructor FuncType; one with statics binds an exact object holding a
// ConstructorElem plus the static members, so `Point(…)` constructs and `Point.origin` reads.
func (c *checker) classValue(ctorType soltype.Type, static *soltype.ObjectType) soltype.Type {
	if len(static.Elems) == 0 {
		return ctorType
	}
	// inferConstructor always returns a FuncType, so anything else is a wiring bug.
	// Fail loudly rather than drop the statics by returning the bare ctorType.
	ctorFn, ok := ctorType.(*soltype.FuncType)
	if !ok {
		panic(fmt.Sprintf("classValue: constructor is %T, not *soltype.FuncType", ctorType))
	}
	elems := make([]soltype.ObjTypeElem, 0, len(static.Elems)+1)
	elems = append(elems, &soltype.ConstructorElem{Fn: ctorFn})
	elems = append(elems, static.Elems...)
	return &soltype.ObjectType{Elems: elems}
}

// getOrCreateClass returns the nominal ClassType token and ClassDef a class binds to,
// reusing the pair already registered for the class or minting and registering a fresh
// one when none exists. Either way the class name resolves to the returned token and def
// before the body is walked, so a self- or sibling-reference in the body reaches them.
// inferClassDecl then fills the def in place, so a sibling that captured the token as a
// forward reference sees the finished body through the same object.
//
// The SCC driver calls it as a pre-pass over each class in a type-key component — the
// group of mutually-recursive classes the dep graph condensed together — so every class
// in the group has a resolved type binding and an empty ClassDef before the first value
// key infers a body (M5 B2). That pre-pass discards the return; only inferClassDecl reads
// it. No placeholder-patch phase is needed: the token a sibling captured is the one that
// carries the finished body.
func (c *checker) getOrCreateClass(scope *Scope, decl *ast.ClassDecl, ns string) (*soltype.ClassType, *ClassDef) {
	qname := qualifyClassName(ns, decl)
	if def, ok := c.ctx.classDef(qname); ok {
		if b, found := scope.GetType(qname); found {
			if self, ok := b.Type.(*soltype.ClassType); ok && self.Name == qname {
				return self, def
			}
		}
	}
	self := &soltype.ClassType{Name: qname, Final: decl.Final()}
	def := &ClassDef{Body: &soltype.ObjectType{}, Static: &soltype.ObjectType{}}
	c.ctx.registerClass(qname, def)
	// Register the type binding under the qualified name so a cross-namespace reference
	// resolves it and a self-referential type in the body resolves to this class rather
	// than falling through as unknown. A bare sibling reference resolves through the
	// checker's classNamespace, which reconstructs this qualified key.
	scope.defineType(qname, TypeBinding{
		Type:    self,
		Sources: []provenance.Provenance{&ast.NodeProvenance{Node: decl}},
	})
	return self, def
}

// qualifyClassName returns a class's dep_graph-qualified name — the namespace joined
// to the local name with a dot, or the bare local name at the root namespace. This is
// the same `CurrentNamespace + "." + name` rule dep_graph forms binding keys with, so
// the registry key and the ClassType token match the value binding's qualified key.
func qualifyClassName(ns string, decl *ast.ClassDecl) string {
	if ns == "" {
		return decl.Name.Name
	}
	return ns + "." + decl.Name.Name
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

// resolveClassSupers resolves a class's `extends` superclass to its ClassType,
// returning a single-element slice or nil when the class has no superclass or it does
// not resolve to a class. B1 records this edge on ClassDef.Supers; the transitive
// nominal subtype walk lands in C1.
func (c *checker) resolveClassSupers(scope *Scope, lvl int, decl *ast.ClassDecl) []*soltype.ClassType {
	if decl.Extends == nil {
		return nil
	}
	ct := c.resolveClassRef(scope, decl.Extends, lvl)
	if ct == nil {
		return nil
	}
	if ct.Final {
		// A final class has no subclasses, so it cannot be a superclass. Report the
		// clause and drop the edge, so the erroneous subtype relationship is never
		// recorded in the nominal graph.
		c.errs = append(c.errs, &CannotExtendFinalClassError{
			Ref:  decl.Extends,
			Name: ast.QualIdentToString(decl.Extends.Name),
		})
		return nil
	}
	return []*soltype.ClassType{ct}
}

// resolveClassImplements resolves a class's `implements` interfaces to their
// ClassTypes, dropping any that do not resolve to a class. `implements` is a
// conformance-only assertion the nominal subtype walk skips, so B1 records these on
// ClassDef.Implements apart from Supers; the structural conformance check lands in C1.
func (c *checker) resolveClassImplements(scope *Scope, lvl int, decl *ast.ClassDecl) []*soltype.ClassType {
	var ifaces []*soltype.ClassType
	for _, impl := range decl.Implements {
		if ct := c.resolveClassRef(scope, impl, lvl); ct != nil {
			ifaces = append(ifaces, ct)
		}
	}
	return ifaces
}

// resolveClassRef resolves an `extends` or `implements` reference to its ClassType. The
// clause requires a class, so a non-class binding such as a type parameter is reported as a
// NonClassSuperError; an unbound name stays silent for M7's TypeRef resolution to report.
func (c *checker) resolveClassRef(scope *Scope, ref *ast.TypeRefTypeAnn, lvl int) *soltype.ClassType {
	name := ast.QualIdentToString(ref.Name)
	b, ok := c.lookupClassBinding(scope, name)
	if !ok {
		return nil
	}
	ct, isClass := b.Type.(*soltype.ClassType)
	if !isClass {
		c.errs = append(c.errs, &NonClassSuperError{Ref: ref, Name: name})
		return nil
	}
	return c.buildClassInstance(scope, ct, ref, lvl)
}

// buildClassInstance returns the token a class reference resolves to: the bare class with
// no type arguments, or a fresh instance carrying the resolved arguments for a generic one
// like `Animal<D>`. An unresolved argument recovers to a fresh var, keeping arity cascade-safe.
func (c *checker) buildClassInstance(scope *Scope, ct *soltype.ClassType, ref *ast.TypeRefTypeAnn, lvl int) *soltype.ClassType {
	if len(ref.TypeArgs) == 0 {
		return ct
	}
	args := make([]soltype.Type, len(ref.TypeArgs))
	for i, arg := range ref.TypeArgs {
		if at, ok := c.resolveClassTypeAnn(scope, arg, lvl); ok {
			args[i] = at
		} else {
			args[i] = c.freshAt(lvl)
		}
	}
	return &soltype.ClassType{Name: ct.Name, TypeArgs: args, Final: ct.Final}
}

// lookupClassBinding resolves a written type name to its scope TypeBinding, honoring
// three precedence levels in order:
//
//  1. A lexically-scoped type parameter shadows everything. A type parameter is bound
//     under its bare name in the class's own child scope, so a bare lookup that lands on
//     a non-class binding — a `T` in `class Box<T>` — wins outright, even against a
//     sibling class of the same name.
//  2. A same-namespace class comes next. Classes are keyed by their qualified name, so a
//     bare `Point` written inside a class in namespace `Geometry` resolves the sibling
//     `Geometry.Point` here, ahead of a root-namespace `Point`. This mirrors dep_graph's
//     qualified-first dependency resolution.
//  3. A bare class binding is the fallback — a root-namespace class referenced bare, or
//     an already-qualified `Geometry.Point` reference written from another namespace,
//     whose doubly-qualified probe in step 2 missed.
func (c *checker) lookupClassBinding(scope *Scope, name string) (TypeBinding, bool) {
	bare, bareOK := scope.GetType(name)
	if bareOK {
		if _, isClass := bare.Type.(*soltype.ClassType); !isClass {
			return bare, true
		}
	}
	if c.classNamespace != "" {
		if b, ok := scope.GetType(c.classNamespace + "." + name); ok {
			return b, true
		}
	}
	return bare, bareOK
}

// resolveClassTypeAnn resolves a type annotation in a class body or type-parameter bound.
// It tries the scope through resolveScopedTypeRef first, so a class or type parameter takes
// precedence over resolveTypeAnn's hardcoded `Promise` stub, then delegates to resolveTypeAnn
// for primitives and structural types.
func (c *checker) resolveClassTypeAnn(scope *Scope, ann ast.TypeAnn, lvl int) (soltype.Type, bool) {
	if ref, ok := ann.(*ast.TypeRefTypeAnn); ok {
		if t, ok := c.resolveScopedTypeRef(scope, ref, lvl); ok {
			return t, true
		}
	}
	return c.resolveTypeAnn(scope, ann, lvl)
}

// resolveScopedTypeRef resolves a type reference through lookupClassBinding, covering a
// bare `Point` or `T` and a generic instance `Box<number>`. It returns ok=false when the
// name is unbound or has arguments but is not a class, and never routes back through
// resolveTypeAnn, so resolveTypeAnn's TypeRef arm can fall back to it without recursing.
func (c *checker) resolveScopedTypeRef(scope *Scope, ref *ast.TypeRefTypeAnn, lvl int) (soltype.Type, bool) {
	name := ast.QualIdentToString(ref.Name)
	b, ok := c.lookupClassBinding(scope, name)
	if !ok {
		return nil, false
	}
	if len(ref.TypeArgs) == 0 {
		return b.Type, true
	}
	if ct, ok := b.Type.(*soltype.ClassType); ok {
		return c.buildClassInstance(scope, ct, ref, lvl), true
	}
	return nil, false
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

// pendingMember carries one method, getter, or setter from the signature phase to the body
// phase; apply installs the fully-inferred signature onto the stored element.
type pendingMember struct {
	fn     *ast.FuncExpr
	recv   *ast.MethodReceiver
	static bool
	stub   *soltype.FuncType
	apply  func(bodyFt *soltype.FuncType)
}

// buildMemberSigs is phase 1 of the member walk: it appends a signature stub — fresh vars
// for each parameter and the return, correct in arity but not yet refined — for every
// method, getter, and setter, so a sibling call resolves before any body is walked. A
// non-static instance member missing its `self` receiver is reported here.
func (c *checker) buildMemberSigs(
	scope *Scope,
	lvl int,
	decl *ast.ClassDecl,
	self *soltype.ClassType,
	body, static *soltype.ObjectType,
) []pendingMember {
	var pending []pendingMember
	for _, elem := range decl.Body {
		switch elem := elem.(type) {
		case *ast.MethodElem:
			name, ok := objKeyName(elem.Name)
			if !ok {
				continue
			}
			c.checkSelfReceiver(name, elem, elem.Static, elem.Receiver)
			stub := c.memberSigStub(lvl, elem.Fn)
			stub.SelfParam = c.selfParam(elem.Receiver, elem.Static, self)
			method, arm := appendMethodSig(targetBody(body, static, elem.Static), name, stub, elem.Static)
			pending = append(pending, pendingMember{
				fn: elem.Fn, recv: elem.Receiver, static: elem.Static, stub: stub,
				apply: func(bodyFt *soltype.FuncType) {
					bodyFt.SelfParam = stub.SelfParam
					method.Signatures[arm] = bodyFt
				},
			})
		case *ast.GetterElem:
			name, ok := objKeyName(elem.Name)
			if !ok {
				continue
			}
			c.checkSelfReceiver(name, elem, elem.Static, elem.Receiver)
			stub := c.memberSigStub(lvl, elem.Fn)
			getter := &soltype.GetterElem{Name: name, SelfParam: c.selfParam(elem.Receiver, elem.Static, self), Type: stub.Ret}
			target := targetBody(body, static, elem.Static)
			target.Elems = append(target.Elems, getter)
			pending = append(pending, pendingMember{
				fn: elem.Fn, recv: elem.Receiver, static: elem.Static, stub: stub,
				apply: func(bodyFt *soltype.FuncType) { getter.Type = bodyFt.Ret },
			})
		case *ast.SetterElem:
			name, ok := objKeyName(elem.Name)
			if !ok {
				continue
			}
			c.checkSelfReceiver(name, elem, elem.Static, elem.Receiver)
			// A well-formed setter declares exactly one value parameter beyond `self` — the
			// value being assigned. Report a paramless or multi-parameter setter, then still
			// build the elem from the first value parameter, or `unknown` when there is none,
			// so member access recovers.
			if len(elem.Fn.Params) != 1 {
				c.report(&SetterArityError{Name: name, Elem: elem, Count: len(elem.Fn.Params)})
			}
			stub := c.memberSigStub(lvl, elem.Fn)
			var param soltype.Type = &soltype.UnknownType{}
			if len(stub.Params) > 0 {
				param = stub.Params[0].Type
			}
			setter := &soltype.SetterElem{Name: name, SelfParam: c.selfParam(elem.Receiver, elem.Static, self), Param: param}
			target := targetBody(body, static, elem.Static)
			target.Elems = append(target.Elems, setter)
			pending = append(pending, pendingMember{
				fn: elem.Fn, recv: elem.Receiver, static: elem.Static, stub: stub,
				apply: func(bodyFt *soltype.FuncType) {
					if len(bodyFt.Params) > 0 {
						setter.Param = bodyFt.Params[0].Type
					}
				},
			})
		}
	}
	return pending
}

// inferMemberBodies is phase 2 of the member walk: it walks each member body, links the
// inferred signature into its stub so a sibling that read the stub grounds through the
// bound graph, then installs the real signature onto the stored element.
func (c *checker) inferMemberBodies(scope *Scope, lvl int, body *soltype.ObjectType, pending []pendingMember) {
	for _, m := range pending {
		bodyFt := c.inferMemberFunc(scope, lvl, m.fn, m.recv, m.static, body)
		c.linkMemberSig(m.fn, bodyFt, m.stub)
		m.apply(bodyFt)
	}
}

// linkMemberSig constrains a member's inferred signature into its stub. The single
// `bodyFt <: stub` direction grounds both parameters (contravariant, so a sibling call's
// argument flows stub → body) and the return (covariant, so the body's return flows body →
// stub); SelfParam lives on the element and is not compared here.
func (c *checker) linkMemberSig(node ast.Node, bodyFt, stub *soltype.FuncType) {
	callable := func(ft *soltype.FuncType) *soltype.FuncType {
		return &soltype.FuncType{Params: ft.Params, Ret: ft.Ret, Inexact: ft.Inexact}
	}
	c.constrain(node, callable(bodyFt), callable(stub))
}

// memberSigStub builds a member's signature stub: one fresh var per value parameter,
// preserving arity, parameter names, and optionality, plus a fresh return var.
func (c *checker) memberSigStub(lvl int, fn *ast.FuncExpr) *soltype.FuncType {
	params := make([]*soltype.FuncParam, len(fn.Params))
	for i, p := range fn.Params {
		// A destructuring parameter has no single name, so the stub uses a positional
		// placeholder. It never surfaces: the stub carries arity and fresh-var types only,
		// and the body pass installs the real signature, whose inferFunc binds the pattern.
		name, ok := identPatName(p.Pattern)
		if !ok {
			name = fmt.Sprintf("arg%d", i)
		}
		params[i] = &soltype.FuncParam{
			Pattern:  &soltype.IdentPat{Name: name},
			Type:     c.freshAt(lvl),
			Optional: p.Optional,
		}
	}
	return &soltype.FuncType{Params: params, Ret: c.freshAt(lvl), Inexact: fn.FuncSig.Inexact}
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
// to the full instance body — owned-mutable for a `mut self` receiver — so field reads
// and writes resolve through the record machinery and a sibling call resolves through the
// pre-declared member signature. It returns the inferred FuncType, whose params and
// return the caller links into the member's signature stub.
func (c *checker) inferMemberFunc(
	scope *Scope,
	lvl int,
	fn *ast.FuncExpr,
	recv *ast.MethodReceiver,
	static bool,
	body *soltype.ObjectType,
) *soltype.FuncType {
	memberScope := scope.Child()
	if !static {
		c.bindSelf(memberScope, recv, body)
	}
	return c.inferFunc(memberScope, lvl, fn.FuncSig, fn.Body, fn)
}

// appendMethodSig installs a method signature under name, merging it into an existing
// same-named MethodElem as an overload arm rather than adding a second element. It
// returns the MethodElem and the index of the appended arm, so the body pass can install
// the fully-inferred signature onto that exact arm after overload merging.
func appendMethodSig(obj *soltype.ObjectType, name string, sig *soltype.FuncType, static bool) (*soltype.MethodElem, int) {
	for _, e := range obj.Elems {
		if m, ok := e.(*soltype.MethodElem); ok && m.Name == name {
			m.Signatures = append(m.Signatures, sig)
			return m, len(m.Signatures) - 1
		}
	}
	m := &soltype.MethodElem{Name: name, Signatures: []*soltype.FuncType{sig}, Static: static}
	obj.Elems = append(obj.Elems, m)
	return m, 0
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

// bindSelf binds the `self` identifier in a member or constructor body scope to the full
// instance body — fields and every method, getter, and setter. The view snapshots the
// body's element slice while sharing each element pointer, so a field write such as
// `self.x = v` refines the same field-type var the projected body reads, and a sibling
// call `self.m()` resolves against the member signature the phase-1 pass installed on the
// shared element. A `mut self` receiver wraps the view in an owned-mutable borrow so
// field writes type-check; a plain `self` binds the bare view.
//
// A `self.field` read or write dispatches through the record subtyping machinery, whose
// object arm threads the borrow and mutability rules field access needs: read-through-
// borrow, read-after-write, and the contravariant write view under `mut`. A `self.method`
// read cannot take that path, since the object arm reads only properties and panics on a
// method member; valueProp intercepts it and resolves through member lookup instead. A
// method's own receiver ownership is checked separately at the call site as a `receiver
// <: SelfParam` constraint, not by this binding.
func (c *checker) bindSelf(scope *Scope, recv *ast.MethodReceiver, body *soltype.ObjectType) {
	// Snapshot the element slice, sharing each element pointer so a write through `self`
	// refines the same field-type var the projected body reads and a sibling signature
	// installed by the body pass shows through the shared pointer.
	view := &soltype.ObjectType{Elems: append([]soltype.ObjTypeElem(nil), body.Elems...)}
	var selfBody soltype.Type = view
	if recv != nil && recv.Mut {
		selfBody = soltype.NewRef(true, nil, view)
	}
	scope.defineValue("self", ValueBinding{Schemes: []TypeScheme{monoScheme(selfBody)}})
}

// checkMethodRecursionAnnotations reports a group of mutually recursive methods that has
// no return-type annotation anywhere in the cycle. A method signature is pre-declared
// before its body is walked so a sibling call resolves, but an un-annotated return in a
// cycle stays an inference variable the cycle cannot ground on its own, so it collapses to
// `never` rather than a useful type. An annotation on any member of the cycle breaks it.
// This mirrors the recursion gate top-level overloaded functions use.
//
// The check builds the call graph over instance methods with bodies — an edge from a
// method arm to each sibling method its body reads through `self` — and inspects each
// strongly connected component. Only instance methods are nodes: a static method has no
// `self` receiver to reach a sibling through, so it cannot join a self-recursion cycle.
// Each overload arm is its own node keyed by its position, so two same-named arms are not
// collapsed and every arm keeps its own annotation state and blame span. A `self.m`
// reference edges to every arm named `m`, since overload resolution may reach any of them.
// A component of two or more arms is a mutual-recursion cycle; a self-recursive method
// forms a single-arm component that infers `never` the way a self-recursive top-level
// function does, so it is not gated. A gated cycle reports every arm, since annotating any
// one of them resolves it.
//
// The check is syntactic: it follows only a direct `self.m` reference. A call through an
// aliased receiver such as `val s = self` or a cycle that spans two classes is not gated;
// it falls back to `never` inference, the same as an ungrounded top-level cycle.
func (c *checker) checkMethodRecursionAnnotations(decl *ast.ClassDecl) {
	type methodArm struct {
		name string
		elem *ast.MethodElem
	}
	var arms []methodArm
	byName := map[string][]int{} // method name → indices into arms sharing it
	for _, elem := range decl.Body {
		m, ok := elem.(*ast.MethodElem)
		if !ok || m.Static || m.Fn.Body == nil {
			continue
		}
		name, ok := objKeyName(m.Name)
		if !ok {
			continue
		}
		byName[name] = append(byName[name], len(arms))
		arms = append(arms, methodArm{name: name, elem: m})
	}
	if len(arms) < 2 {
		return
	}
	names := set.NewSet[string]()
	for name := range byName {
		names.Add(name)
	}
	nodes := make([]int, len(arms))
	for i := range arms {
		nodes[i] = i
	}
	successors := func(i int) []int {
		var out []int
		for _, name := range selfMethodRefs(arms[i].elem.Fn.Body, names) {
			out = append(out, byName[name]...)
		}
		return out
	}
	for _, component := range graph.StronglyConnectedComponents(nodes, successors) {
		if len(component) < 2 {
			continue
		}
		annotated := false
		for _, i := range component {
			if arms[i].elem.Fn.FuncSig.Return != nil {
				annotated = true
				break
			}
		}
		if annotated {
			continue
		}
		// Sort the deduped names so the diagnostic reads the same regardless of the order
		// the SCC walk yields the component's arms, and so two arms of one name read once.
		groupSet := set.NewSet[string]()
		for _, i := range component {
			groupSet.Add(arms[i].name)
		}
		group := groupSet.ToSlice()
		sort.Strings(group)
		for _, i := range component {
			c.report(&RecursiveMethodAnnotationError{Name: arms[i].name, Elem: arms[i].elem, Group: group})
		}
	}
}

// selfMethodRefs returns the names of the sibling methods a method body reads through
// `self`, keyed against the class's instance-method names so a `self.field` read or a call
// to a non-method member does not count as a call-graph edge. Both a call `self.m()` and a
// bare reference `self.m` count, since either makes the body depend on the sibling's
// signature.
func selfMethodRefs(body *ast.Block, names set.Set[string]) []string {
	v := &selfMethodVisitor{names: names, found: set.NewSet[string]()}
	body.Accept(v)
	return v.found.ToSlice()
}

// selfMethodVisitor collects `self.<method>` references while walking a method body.
type selfMethodVisitor struct {
	ast.DefaultVisitor
	names set.Set[string]
	found set.Set[string]
}

func (v *selfMethodVisitor) EnterExpr(e ast.Expr) bool {
	member, ok := e.(*ast.MemberExpr)
	if !ok || member.Prop == nil {
		return true
	}
	ident, ok := member.Object.(*ast.IdentExpr)
	if !ok || ident.Name != "self" {
		return true
	}
	if v.names.Contains(member.Prop.Name) {
		v.found.Add(member.Prop.Name)
	}
	return true
}

// classKeepVars collects the type-parameter vars a generic class body coalesces around:
// the class's own TypeParam vars plus every method signature's own TypeParam vars. These
// stay symbolic through the coalesce so member lookup can substitute an instance's
// argument for a class parameter and instantiate a method's own parameters per call (B8).
func classKeepVars(typeParams []*soltype.TypeParam, bodies ...*soltype.ObjectType) set.Set[*soltype.TypeVarType] {
	keep := set.NewSet[*soltype.TypeVarType]()
	for _, tp := range typeParams {
		keep.Add(tp.Var)
	}
	for _, obj := range bodies {
		for _, elem := range obj.Elems {
			m, ok := elem.(*soltype.MethodElem)
			if !ok {
				continue
			}
			for _, sig := range m.Signatures {
				for _, tp := range sig.TypeParams {
					keep.Add(tp.Var)
				}
			}
		}
	}
	return keep
}

// keptFlowMap maps each inference var to the kept type-parameter vars that flow into it —
// the kept vars T for which T <: v is recorded transitively through the upper-bound graph.
// It exists because constrain stores a var-var edge on ONE side: an unannotated field read
// `self.v` on `class Box<T>` records the read's result var on T's upper bounds rather than T
// on the result's lower bounds, so the result coalesces to `never` when read positively. The
// map lets freezeClassBody recover T as the value flowing into such a var, adding it
// back as a positive-position lower-bound contribution. Each var's kept sources are sorted by
// id so the coalesced union orders deterministically.
//
// Worked example. For
//
//	class Box<T> {
//	    v: T,
//	    read(self) { return self.v },
//	    alias(self) { return self.read() },
//	}
//
// T is the kept var t1. Reading `self.v` inside `read` constrains t1 <: t4, where t4 is
// `read`'s return var, and `alias` returning `self.read()` constrains t4 <: t5, where t5 is
// `alias`'s return var. constrain records each edge on the smaller var's upper bounds, so the
// stored graph is t1.upper=[t4], t4.upper=[t5]; t4 and t5 have no lower bounds. Given
// keep={t1}, the forward walk over upper-bound edges reaches t4 then t5, so the result is
//
//	{ t4: [t1], t5: [t1] }
//
// The kept var t1 is not itself a key — only the vars it flows into are. freezeClassBody then
// coalesces `read`'s return t4 to t1 and `alias`'s return t5 to t1 rather than to `never`.
func keptFlowMap(keep set.Set[*soltype.TypeVarType]) map[*soltype.TypeVarType][]*soltype.TypeVarType {
	reached := map[*soltype.TypeVarType]set.Set[*soltype.TypeVarType]{}
	for kv := range keep {
		// Forward DFS over upper-bound var edges from kv: every var reached has kv <: it,
		// so kv is one of the values flowing into it. seen bounds the walk on a cycle.
		seen := set.NewSet[*soltype.TypeVarType]()
		stack := []*soltype.TypeVarType{kv}
		for len(stack) > 0 {
			v := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, ub := range v.UpperBounds {
				uv, ok := ub.(*soltype.TypeVarType)
				if !ok || seen.Contains(uv) {
					continue
				}
				seen.Add(uv)
				if reached[uv] == nil {
					reached[uv] = set.NewSet[*soltype.TypeVarType]()
				}
				reached[uv].Add(kv)
				stack = append(stack, uv)
			}
		}
	}
	flow := make(map[*soltype.TypeVarType][]*soltype.TypeVarType, len(reached))
	for v, sources := range reached {
		vars := sources.ToSlice()
		sort.Slice(vars, func(i, j int) bool { return vars[i].ID < vars[j].ID })
		flow[v] = vars
	}
	return flow
}

// freezeClassBody coalesces each member's type in place so member lookup and the
// class-vs-object rule read concrete member types rather than the fresh vars a field held
// before a constructor assignment refined it. Each member is coalesced individually rather
// than the whole object at once, since coalesce's object-merge pass reads every element as a
// property and panics on a method, getter, or setter member.
//
// keep and flow are nil for a non-generic class, where coalesceKeeping reduces to a plain
// coalesce. A generic class passes its type-parameter vars as keep — held symbolic instead of
// inlined to their bounds — and the kept-flow map so a member typed through a class parameter,
// such as `read(self) { self.v }` on `class Box<T>`, reads `T` rather than collapsing to
// `never`, leaving `T` in place for projectClassMember to substitute at an instance's argument.
func (c *checker) freezeClassBody(obj *soltype.ObjectType, keep set.Set[*soltype.TypeVarType], flow map[*soltype.TypeVarType][]*soltype.TypeVarType) {
	for i, e := range obj.Elems {
		switch e := e.(type) {
		case *soltype.PropertyElem:
			obj.Elems[i] = &soltype.PropertyElem{Name: e.Name, Type: coalesceKeeping(e.Type, soltype.Positive, keep, flow), Optional: e.Optional, Readonly: e.Readonly}
		case *soltype.GetterElem:
			obj.Elems[i] = &soltype.GetterElem{Name: e.Name, SelfParam: e.SelfParam, Type: coalesceKeeping(e.Type, soltype.Positive, keep, flow)}
		case *soltype.SetterElem:
			obj.Elems[i] = &soltype.SetterElem{Name: e.Name, SelfParam: e.SelfParam, Param: coalesceKeeping(e.Param, soltype.Negative, keep, flow)}
		case *soltype.MethodElem:
			sigs := make([]*soltype.FuncType, len(e.Signatures))
			for j, sig := range e.Signatures {
				if cs, ok := coalesceKeeping(sig, soltype.Positive, keep, flow).(*soltype.FuncType); ok {
					sigs[j] = cs
				} else {
					sigs[j] = sig
				}
			}
			obj.Elems[i] = &soltype.MethodElem{Name: e.Name, Signatures: sigs, Static: e.Static}
		}
	}
}
