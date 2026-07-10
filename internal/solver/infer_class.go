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

// inferClassDecl types a class declaration (M5 B1). It returns the class's constructor
// as a raw FuncType for the SCC driver to constrain into the value binding var and
// generalize, along with the decl's provenance. It has two side effects. It registers
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
func (c *checker) inferClassDecl(scope *Scope, lvl int, decl *ast.ClassDecl) (soltype.Type, provenance.Provenance, bool) {
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
	// reached without a pre-registered shell mints and registers one here. B1 uses the
	// bare local name as the qualified key, correct for the top-level default namespace;
	// namespace-qualified keys ride the namespace work.
	self, def := c.getOrCreateClass(scope, decl)
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
	def.Supers = c.resolveClassSupers(declScope, decl)
	def.Implements = c.resolveClassImplements(declScope, decl)

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
func (c *checker) getOrCreateClass(scope *Scope, decl *ast.ClassDecl) (*soltype.ClassType, *ClassDef) {
	name := decl.Name.Name
	if def, ok := c.ctx.classDef(name); ok {
		if b, found := scope.GetType(name); found {
			if self, ok := b.Type.(*soltype.ClassType); ok && self.Name == name {
				return self, def
			}
		}
	}
	self := &soltype.ClassType{Name: name}
	def := &ClassDef{Body: &soltype.ObjectType{}, Static: &soltype.ObjectType{}}
	c.ctx.registerClass(name, def)
	// Register the type binding so a self-referential type in the body resolves to this
	// class rather than falling through as unknown.
	scope.defineType(name, TypeBinding{
		Type:    self,
		Sources: []provenance.Provenance{&ast.NodeProvenance{Node: decl}},
	})
	return self, def
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
func (c *checker) resolveClassSupers(scope *Scope, decl *ast.ClassDecl) []*soltype.ClassType {
	if decl.Extends == nil {
		return nil
	}
	if ct := c.resolveClassRef(scope, decl.Extends); ct != nil {
		return []*soltype.ClassType{ct}
	}
	return nil
}

// resolveClassImplements resolves a class's `implements` interfaces to their
// ClassTypes, dropping any that do not resolve to a class. `implements` is a
// conformance-only assertion the nominal subtype walk skips, so B1 records these on
// ClassDef.Implements apart from Supers; the structural conformance check lands in C1.
func (c *checker) resolveClassImplements(scope *Scope, decl *ast.ClassDecl) []*soltype.ClassType {
	var ifaces []*soltype.ClassType
	for _, impl := range decl.Implements {
		if ct := c.resolveClassRef(scope, impl); ct != nil {
			ifaces = append(ifaces, ct)
		}
	}
	return ifaces
}

// resolveClassRef resolves a type reference that names a class to its ClassType, or
// nil when the name is not a registered class. B1 consults the type scope directly
// rather than routing through resolveTypeAnn, whose general TypeRef resolution lands
// with the alias work.
//
// A nil result is currently dropped by the callers with no diagnostic. C1 reports the
// non-class case as NonClassSuperError, and M7's general TypeRef resolution reports an
// unbound name (planning/simple_sub/m5-implementation-plan.md).
func (c *checker) resolveClassRef(scope *Scope, ref *ast.TypeRefTypeAnn) *soltype.ClassType {
	name := ast.QualIdentToString(ref.Name)
	if b, ok := scope.GetType(name); ok {
		if ct, ok := b.Type.(*soltype.ClassType); ok {
			return ct
		}
	}
	return nil
}

// resolveClassTypeAnn resolves a type annotation appearing in a class body or a
// type-parameter bound. It first consults the type scope for a reference to a class or
// type parameter — a bare `Point` or `T`, or a generic class instance `Box<number>` — the
// names resolveTypeAnn's general TypeRef resolution does not yet cover, and otherwise
// delegates to resolveTypeAnn for primitives and structural types.
func (c *checker) resolveClassTypeAnn(scope *Scope, ann ast.TypeAnn, lvl int) (soltype.Type, bool) {
	if ref, ok := ann.(*ast.TypeRefTypeAnn); ok {
		name := ast.QualIdentToString(ref.Name)
		if b, ok := scope.GetType(name); ok {
			if len(ref.TypeArgs) == 0 {
				return b.Type, true
			}
			// A generic class reference like `Cmp<U>` names a class and supplies its type
			// arguments. Resolve each argument through the same class-scope path and mint a
			// fresh instance token carrying them, so a type-parameter bound such as
			// `<T: Cmp<U>>` resolves to `Cmp<U>` rather than falling through to general
			// TypeRef resolution. An unresolved argument recovers to a fresh var so the
			// reference keeps its arity, cascade-safe.
			if ct, ok := b.Type.(*soltype.ClassType); ok {
				args := make([]soltype.Type, len(ref.TypeArgs))
				for i, arg := range ref.TypeArgs {
					if at, ok := c.resolveClassTypeAnn(scope, arg, lvl); ok {
						args[i] = at
					} else {
						args[i] = c.freshAt(lvl)
					}
				}
				return &soltype.ClassType{Name: ct.Name, TypeArgs: args, Final: ct.Final}, true
			}
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

// pendingMember carries one method, getter, or setter from the signature phase to the
// body phase. shell is the signature the phase-1 pass appended and every sibling call
// resolves against; body carries the receiver, staticness, and function node its walk
// needs. apply installs the fully-inferred body signature onto the stored element once
// the body pass produces it.
type pendingMember struct {
	fn     *ast.FuncExpr
	recv   *ast.MethodReceiver
	static bool
	shell  *soltype.FuncType
	apply  func(bodyFt *soltype.FuncType)
}

// buildMemberSigs is phase 1 of the member walk. It appends one signature element per
// method, getter, and setter to the instance or static body before any body is inferred,
// so a call between two members of the same class resolves against the sibling's
// pre-declared signature. Each signature is a shell: fresh vars for every value parameter
// and the return, correct in arity but not yet refined by the body. The body pass links
// the real inferred signature into these fresh vars and installs it onto the element, so
// a sibling that read the shell before the body ran sees the refined type through the
// bound graph. An instance member missing its `self` receiver is reported here.
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
			shell := c.memberSigShell(lvl, elem.Fn)
			shell.SelfParam = c.selfParam(elem.Receiver, elem.Static, self)
			method, arm := appendMethodSig(targetBody(body, static, elem.Static), name, shell, elem.Static)
			pending = append(pending, pendingMember{
				fn: elem.Fn, recv: elem.Receiver, static: elem.Static, shell: shell,
				apply: func(bodyFt *soltype.FuncType) {
					bodyFt.SelfParam = shell.SelfParam
					method.Signatures[arm] = bodyFt
				},
			})
		case *ast.GetterElem:
			name, ok := objKeyName(elem.Name)
			if !ok {
				continue
			}
			c.checkSelfReceiver(name, elem, elem.Static, elem.Receiver)
			shell := c.memberSigShell(lvl, elem.Fn)
			getter := &soltype.GetterElem{Name: name, SelfParam: c.selfParam(elem.Receiver, elem.Static, self), Type: shell.Ret}
			target := targetBody(body, static, elem.Static)
			target.Elems = append(target.Elems, getter)
			pending = append(pending, pendingMember{
				fn: elem.Fn, recv: elem.Receiver, static: elem.Static, shell: shell,
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
			shell := c.memberSigShell(lvl, elem.Fn)
			var param soltype.Type = &soltype.UnknownType{}
			if len(shell.Params) > 0 {
				param = shell.Params[0].Type
			}
			setter := &soltype.SetterElem{Name: name, SelfParam: c.selfParam(elem.Receiver, elem.Static, self), Param: param}
			target := targetBody(body, static, elem.Static)
			target.Elems = append(target.Elems, setter)
			pending = append(pending, pendingMember{
				fn: elem.Fn, recv: elem.Receiver, static: elem.Static, shell: shell,
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

// inferMemberBodies is phase 2 of the member walk. It walks each method, getter, and
// setter body against the signature phase 1 appended. It links the inferred body
// signature into the shell's fresh vars — `bodyFt <: shell`, the same "inferred type <:
// pre-bound var" relation the module SCC driver uses for recursive functions — so a
// sibling that already read the shell resolves through the bound graph, then installs the
// fully-inferred signature onto the stored element so member access and class-vs-object
// subtyping read the real member type.
func (c *checker) inferMemberBodies(scope *Scope, lvl int, body *soltype.ObjectType, pending []pendingMember) {
	for _, m := range pending {
		bodyFt := c.inferMemberFunc(scope, lvl, m.fn, m.recv, m.static, body)
		c.linkMemberSig(m.fn, bodyFt, m.shell)
		m.apply(bodyFt)
	}
}

// linkMemberSig constrains a member's inferred body signature into the shell the
// signature phase pre-declared. Only the callable parts — parameters and return — are
// related, since the shell's SelfParam is stored on the element, not compared here. The
// single `bodyFt <: shell` direction suffices: parameters are contravariant, so a
// sibling call's argument flows shell parameter → body parameter, and the return is
// covariant, so the body's inferred return flows body return → shell return, exactly the
// grounding the recursive reference needs.
func (c *checker) linkMemberSig(node ast.Node, bodyFt, shell *soltype.FuncType) {
	callable := func(ft *soltype.FuncType) *soltype.FuncType {
		return &soltype.FuncType{Params: ft.Params, Ret: ft.Ret, Inexact: ft.Inexact}
	}
	c.constrain(node, callable(bodyFt), callable(shell))
}

// memberSigShell builds a member's signature shell: one fresh var per value parameter,
// preserving arity, parameter names, and optionality, plus a fresh return var. The body
// pass refines these vars, so the shell only needs to be callable at the right arity for
// a sibling call to resolve before the body runs.
func (c *checker) memberSigShell(lvl int, fn *ast.FuncExpr) *soltype.FuncType {
	params := make([]*soltype.FuncParam, len(fn.Params))
	for i, p := range fn.Params {
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
// return the caller links into the member's signature shell.
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
// The check builds the call graph over methods with bodies — an edge from a method to
// each sibling method its body reads through `self` — and inspects each strongly
// connected component. A component of two or more methods is a mutual-recursion cycle; a
// self-recursive method forms a single-method cycle that infers `never` the way a
// self-recursive top-level function does, so it is not gated. A gated cycle reports every
// member, since annotating any one of them resolves it.
func (c *checker) checkMethodRecursionAnnotations(decl *ast.ClassDecl) {
	methods := map[string]*ast.MethodElem{}
	var names []string
	for _, elem := range decl.Body {
		m, ok := elem.(*ast.MethodElem)
		if !ok || m.Fn.Body == nil {
			continue
		}
		name, ok := objKeyName(m.Name)
		if !ok {
			continue
		}
		if _, seen := methods[name]; !seen {
			names = append(names, name)
		}
		methods[name] = m
	}
	if len(names) < 2 {
		return
	}
	successors := func(name string) []string {
		return selfMethodRefs(methods[name].Fn.Body, methods)
	}
	for _, component := range graph.StronglyConnectedComponents(names, successors) {
		if len(component) < 2 {
			continue
		}
		annotated := false
		for _, name := range component {
			if methods[name].Fn.FuncSig.Return != nil {
				annotated = true
				break
			}
		}
		if annotated {
			continue
		}
		// Sort the group so the diagnostic reads the same regardless of the order the SCC
		// walk yields the component's members.
		group := append([]string(nil), component...)
		sort.Strings(group)
		for _, name := range component {
			c.report(&RecursiveMethodAnnotationError{Name: name, Elem: methods[name], Group: group})
		}
	}
}

// selfMethodRefs returns the names of the sibling methods a method body reads through
// `self`, keyed against the class's method set so a `self.field` read or a call to a
// non-method member does not count as a call-graph edge. Both a call `self.m()` and a bare
// reference `self.m` count, since either makes the body depend on the sibling's signature.
func selfMethodRefs(body *ast.Block, methods map[string]*ast.MethodElem) []string {
	v := &selfMethodVisitor{methods: methods, found: set.NewSet[string]()}
	body.Accept(v)
	return v.found.ToSlice()
}

// selfMethodVisitor collects `self.<method>` references while walking a method body.
type selfMethodVisitor struct {
	ast.DefaultVisitor
	methods map[string]*ast.MethodElem
	found   set.Set[string]
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
	if _, isMethod := v.methods[member.Prop.Name]; isMethod {
		v.found.Add(member.Prop.Name)
	}
	return true
}

// freezeClassBody coalesces each member's type in place so member lookup and the
// class-vs-object rule read concrete member types rather than the fresh vars a field
// held before a constructor assignment refined it. Each member is coalesced
// individually rather than the whole object at once, since coalesce's object-merge pass
// reads every element as a property and panics on a method, getter, or setter member.
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
