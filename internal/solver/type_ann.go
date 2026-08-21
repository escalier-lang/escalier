package solver

import (
	"fmt"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// resolveTypeAnn converts a supported type annotation into a soltype.Type,
// returning ok=false with a `never` placeholder when the annotation is unsupported
// so a caller can recover by keeping the type it already inferred. The level `lvl`
// lets a supported wrapper with an unsupported inner recover that inner to a fresh
// var at the right level.
func (c *checker) resolveTypeAnn(scope *Scope, ta ast.TypeAnn, lvl int) (soltype.Type, bool) {
	switch ta := ta.(type) {
	case *ast.NumberTypeAnn:
		return c.annPrim(ta, soltype.NumPrim), true
	case *ast.StringTypeAnn:
		return c.annPrim(ta, soltype.StrPrim), true
	case *ast.BooleanTypeAnn:
		return c.annPrim(ta, soltype.BoolPrim), true
	case *ast.NeverTypeAnn:
		// `never` is the bottom of the lattice, the empty type. A mapped type's key-remapping
		// expression names it to drop a field, `{[if K : "id" { never } else { K }]: … }`, which is
		// how a mapped type filters its key set.
		//
		// recordProvForResult records nothing here: NeverType is a shared zero-size singleton, so
		// a caller that needs a span for a rejected `never` falls back to the constraint site.
		t := &soltype.NeverType{}
		c.recordProvForResult(t, ta, AnnotationType)
		return t, true
	case *ast.UnknownTypeAnn:
		// `unknown` is the top of the lattice. Every type is a subtype of it through
		// constrain's `_ <: unknown` rule, so it is the bound that admits any type at a
		// position whose value is never read, as `fn () -> unknown` does in a type
		// parameter's constraint.
		//
		// recordProvForResult records nothing here, for the reason the NeverTypeAnn arm gives:
		// UnknownType is a shared zero-size singleton.
		t := &soltype.UnknownType{}
		c.recordProvForResult(t, ta, AnnotationType)
		return t, true
	case *ast.LitTypeAnn:
		return c.resolveLitTypeAnn(ta)
	case *ast.TypeRefTypeAnn:
		// Resolve through the type scope first so a user-defined alias, class, or type
		// parameter takes precedence over the built-in Promise stub below. A bare alias or
		// class reference resolves here; the prelude Promise placeholder is not a class, so
		// a `Promise<T>` reference with no user binding does not resolve here and reaches
		// the stub.
		if t, ok := c.resolveScopedTypeRef(scope, ta, lvl); ok {
			return t, true
		}
		// The built-in Promise<T>. The prelude seeds Promise as an opaque placeholder, so
		// an `async fn () -> Promise<T>` annotation resolves here. Any other name or arity
		// reports unsupported with a `never` placeholder so the caller can recover by
		// keeping the inferred type.
		if ast.QualIdentToString(ta.Name) == "Promise" && (len(ta.TypeArgs) == 1 || len(ta.TypeArgs) == 2) {
			// A lifetime-annotated Promise (`'a Promise<T>` or `Promise<'a, T>`) is not
			// supported: M3's PromiseType carries no lifetime, so silently accepting it
			// would drop the lifetime. Reject it as an unsupported feature rather than
			// coercing to a plain Promise<T>. (Lifetimes on referenced types land with
			// the wider TypeRef/lifetime work.)
			if len(ta.LifetimeArgs) > 0 || ta.Lifetime != nil {
				return c.reportUnsupportedFeature(ta, "lifetime annotation on Promise"), false
			}
			inner, ok := c.resolveTypeAnn(scope, ta.TypeArgs[0], lvl)
			if !ok {
				// The inner annotation was unsupported and already reported its own
				// error. The Promise itself IS supported, so keep the WRAPPER rather
				// than collapsing the whole annotation to the bare-var recovery the
				// caller applies on ok=false: `p: Promise<bad>` should stay Promise-
				// shaped (so `await p` and the rendered signature read as a Promise),
				// not degrade to an unconstrained var. Recover the inner to a fresh var
				// — cascade-safe in BOTH directions (an initializer flowing into
				// `Promise<freshVar>` constrains the var without failing; a `never` or
				// `unknown` inner would instead cascade a spurious `<: never` / `<:
				// unknown`, since constrain has no rule for either as an input).
				//
				// PR8 (planning/simple_sub/m3-implementation-plan.md) deliberately
				// KEEPS this fresh var rather than substituting its ErrorType sentinel:
				// PR8 repoints only the no-good-type recovery, and this one yields a
				// strictly better type — the fresh var generalizes (`Promise<_>` ⇒
				// `Promise<T0>`) where ErrorType would freeze it to `Promise<error>`.
				inner = c.freshAt(lvl)
			}
			// The optional second argument names the rejection type, `Promise<T, E>`. An
			// unwritten one leaves Err nil, the shorthand for a promise that cannot
			// reject. A bad second argument recovers to a fresh var for the reason the
			// payload does above.
			var errT soltype.Type
			if len(ta.TypeArgs) == 2 {
				errT, ok = c.resolveTypeAnn(scope, ta.TypeArgs[1], lvl)
				if !ok {
					errT = c.freshAt(lvl)
				}
			}
			t := &soltype.PromiseType{Inner: inner, Err: errT}
			c.recordProv(t, ta, AnnotationType)
			return t, true
		}
		// The built-in Generator and AsyncGenerator, the external face of a `gen fn` /
		// `async gen fn`. Like Promise they resolve here only when no user binding shadows
		// the prelude placeholder, and a lifetime annotation is rejected rather than silently
		// dropped. Y is what the body yields, R what it returns, and N what a `yield`
		// evaluates to; all three are required. The optional fourth argument names what
		// advancing the generator may raise, `Generator<Y, R, N, E>`, the same shape
		// `Promise<T, E>` takes. An unwritten one leaves Throws nil, the shorthand for a
		// generator that cannot raise.
		if name := ast.QualIdentToString(ta.Name); (name == "Generator" || name == "AsyncGenerator") &&
			(len(ta.TypeArgs) == 3 || len(ta.TypeArgs) == 4) {
			if len(ta.LifetimeArgs) > 0 || ta.Lifetime != nil {
				return c.reportUnsupportedFeature(ta, "lifetime annotation on "+name), false
			}
			slots := make([]soltype.Type, len(ta.TypeArgs))
			for i, arg := range ta.TypeArgs {
				slot, ok := c.resolveTypeAnn(scope, arg, lvl)
				if !ok {
					// Recover the slot to a fresh var and keep the Generator wrapper, for the
					// reason the Promise arm above gives: the wrapper itself is supported, and
					// a fresh var is cascade-safe where `never` or `unknown` would provoke a
					// second failure.
					slot = c.freshAt(lvl)
				}
				slots[i] = slot
			}
			var throws soltype.Type
			if len(slots) == 4 {
				throws = slots[3]
			}
			t := &soltype.GeneratorType{
				Yield: slots[0], Ret: slots[1], Next: slots[2], Throws: throws,
				Async: name == "AsyncGenerator",
			}
			c.recordProv(t, ta, AnnotationType)
			return t, true
		}
		// The built-in Array<T>, the element-typed sequence a rest parameter binds its trailing
		// arguments into. It is minimal by design: it carries the element type and nothing else, so
		// `xs.length` and `xs[0]` do not resolve. A local `Array` shadows the stub today, since
		// resolution reaches here only after resolveScopedTypeRef finds nothing. That ends once
		// `Array` and `Promise` are imported from the `std:collection` and `std:async` pseudo-packages.
		if ast.QualIdentToString(ta.Name) == "Array" && len(ta.TypeArgs) == 1 {
			if len(ta.LifetimeArgs) > 0 || ta.Lifetime != nil {
				return c.reportUnsupportedFeature(ta, "lifetime annotation on Array"), false
			}
			elem, ok := c.resolveTypeAnn(scope, ta.TypeArgs[0], lvl)
			if !ok {
				// Recover the element to a fresh var and keep the Array wrapper, for the reason
				// the Promise arm above gives: the wrapper itself is supported, and a fresh var
				// is cascade-safe where `never` or `unknown` would provoke a second failure.
				elem = c.freshAt(lvl)
			}
			t := &soltype.ArrayType{Elem: elem}
			c.recordProv(t, ta, AnnotationType)
			return t, true
		}
		if t, ok := c.resolveStringIntrinsic(scope, ta, lvl); ok {
			return t, true
		}
		if t, ok := c.resolveExactnessIntrinsic(scope, ta, lvl); ok {
			return t, true
		}
		// Nothing claimed the name. Either it names no declaration at all, or it names one of
		// the two built-in stubs above with an argument count neither accepts. Those stubs take
		// exactly one, and a user-defined Promise or Array would have resolved through the scope
		// before reaching here, so the name here is the built-in.
		name := ast.QualIdentToString(ta.Name)
		if name == "Promise" || name == "Array" {
			c.report(&TypeArgArityMismatchError{
				Ref: ta, Kind: BuiltinDeclKind, Name: name,
				Required: 1, Total: 1, Got: len(ta.TypeArgs),
			})
			return &soltype.NeverType{}, false
		}
		return c.report(&UnknownTypeError{Ref: ta, Name: name}), false
	case *ast.ObjectTypeAnn:
		return c.resolveObjectTypeAnn(scope, ta, lvl)
	case *ast.TupleTypeAnn:
		return c.resolveTupleTypeAnn(scope, ta, lvl)
	case *ast.MutableTypeAnn:
		return c.resolveMutableTypeAnn(scope, ta, lvl)
	case *ast.RefTypeAnn:
		return c.resolveRefTypeAnn(scope, ta, lvl)
	case *ast.UnionTypeAnn:
		return c.resolveUnionTypeAnn(scope, ta, lvl)
	case *ast.IntersectionTypeAnn:
		return c.resolveIntersectionTypeAnn(scope, ta, lvl)
	case *ast.FuncTypeAnn:
		return c.resolveFuncTypeAnn(scope, ta, lvl)
	case *ast.KeyOfTypeAnn:
		return c.resolveKeyOfTypeAnn(scope, ta, lvl)
	case *ast.NegationTypeAnn:
		return c.resolveNegationTypeAnn(scope, ta, lvl)
	case *ast.IndexTypeAnn:
		return c.resolveIndexTypeAnn(scope, ta, lvl)
	case *ast.TypeOfTypeAnn:
		return c.resolveTypeOfTypeAnn(scope, ta)
	case *ast.CondTypeAnn:
		return c.resolveCondTypeAnn(scope, ta, lvl)
	case *ast.InferTypeAnn:
		return c.resolveInferTypeAnn(scope, ta)
	case *ast.TemplateLitTypeAnn:
		return c.resolveTemplateLitTypeAnn(scope, ta, lvl)
	case *ast.WildcardTypeAnn:
		// `_` asks for a hole someone else fills. Which position it sits in decides who fills it,
		// and that is the only difference between the two forms below. Unlike the other arms it
		// reports NO error either way — `_` is a supported, user-authored marker.
		if c.inCondExtends {
			// In a conditional's pattern the match fills it, which makes `_` an `infer` clause with
			// no name: reduceCondInfer solves it from the check that decides the branch and drops
			// the capture, since no branch can reference a name the source never wrote. A plain
			// variable could not do this, because condOperandGround refuses to decide over one.
			// Each occurrence mints its own declaration, so two `_` are independent holes.
			decl := c.ctx.freshInferDecl(soltype.WildcardInferName)
			return &soltype.InferType{ID: decl.ID, Name: decl.Name, Binder: true}, true
		}
		// Everywhere else the value flowing in fills it, so `_` is an inference variable at the
		// current level. `Promise<_>` on an async fn's return relies on this: the body's return
		// flows into the variable, inferring the inner.
		t := c.freshAt(lvl)
		c.recordProv(t, ta, WildcardAnnotation)
		return t, true
	default:
		return c.reportUnsupported(ta), false
	}
}

// resolveObjectTypeAnn lowers an object type annotation to a soltype.ObjectType,
// honoring the trailing `...` inexact marker. A `name: T` / `name?: T` property resolves
// to a PropertyElem; a `...A` spread resolves to a SpreadElem, so `{...A, x: T}` is an
// ObjectType carrying a spread element — the object twin of a spread-carrying tuple. The
// evaluator merges the spread once its operand grounds; until then the object stays a
// residual that prints the way the source wrote it. A `new (…) -> T` member resolves to a
// ConstructorElem, the same construct signature a class value carries, which is what lets
// `InstanceType<C>` and `ConstructorParameters<C>` match a written object type. Method,
// getter, and setter members are not yet part of an object annotation and report an
// unsupported feature, with the object still built from the members that do resolve.
//
// A spread-free object dedups duplicate keys last-wins-first-position, keeping property
// names unique for Prop/equalType. A spread-carrying object keeps its elements in source
// order without deduping, since order is significant to the merge and reduceObject dedups
// when it grounds. A property whose value annotation is itself unsupported recovers that
// value to a fresh var and keeps the object shape — cascade-safe, mirroring the
// Promise<bad> recovery. The arm therefore always returns ok=true.
func (c *checker) resolveObjectTypeAnn(scope *Scope, ta *ast.ObjectTypeAnn, lvl int) (soltype.Type, bool) {
	// A `...A` spread and a `[K]: V for K in Keys` mapped member each make the object an unreduced
	// residual whose final member list the evaluator computes. Either one puts the whole object on
	// the ordered path below, which keeps source order for the override merge.
	//
	// The two mix freely with ordinary members, so `{a: number, [K]: V for K in Keys}` is one object
	// annotation. TypeScript has no such form and writes the intersection
	// `{a: number} & {[K in Keys]: V}` instead.
	hasResidual := false
	for _, elem := range ta.Elems {
		switch elem.(type) {
		case *ast.RestSpreadTypeAnn, *ast.MappedTypeAnn:
			hasResidual = true
		}
	}
	// Lowering is uniform across every member kind, so the two paths below differ only in how
	// they collect what it produces. A residual-free object is never reduced later, so its
	// members collapse here to the unique-key shape Prop and equalType assume. A residual object
	// keeps source order for the override merge and collapses in reduceObject once its spreads
	// and mapped members ground.
	lowering := &objAnnLowering{c: c, scope: scope, lvl: lvl}
	unsupported := false
	var elems []soltype.ObjTypeElem
	if !hasResidual {
		// The builder keeps source order and collapses members that answer the same access
		// under one name. A collapse is a redeclaration the source wrote twice, so it is
		// reported, with the collapsed list as the recovery.
		b := newObjElemBuilder(len(ta.Elems))
		for _, elem := range ta.Elems {
			resolved, ok := lowering.lower(elem)
			if !ok {
				unsupported = true
				continue
			}
			if resolved == nil || !b.addElem(resolved) {
				continue
			}
			// Every member that can displace another carries a span. ObjTypeAnnElem does
			// not declare Span, since a callable signature and a mapped member have none,
			// and neither of those reaches the builder.
			if blame, hasSpan := elem.(spanned); hasSpan {
				c.report(&DuplicateObjectMemberError{Name: soltype.ObjElemName(resolved), Elem: blame})
			}
		}
		elems = b.result()
	} else {
		// Source order is the merge order, so members are appended as written and nothing
		// collapses here. A spread superseding an earlier member is an override rather than a
		// redeclaration, which is why this path reports no duplicate.
		for _, elem := range ta.Elems {
			resolved, ok := lowering.lower(elem)
			if !ok {
				unsupported = true
				continue
			}
			if resolved != nil {
				elems = append(elems, resolved)
			}
		}
	}
	if unsupported {
		c.reportUnsupportedFeature(ta, "object type member other than a property, spread, mapped member, `new` signature, method, or accessor")
	}
	t := &soltype.ObjectType{Elems: elems, Inexact: ta.Inexact}
	c.recordProv(t, ta, AnnotationType)
	return t, true
}

// objAnnLowering lowers the members of one object type annotation. It carries the only state a
// member needs from its siblings, which is whether a construct signature was already seen: an
// object type holds at most one, so a second is reported rather than emitted.
type objAnnLowering struct {
	c     *checker
	scope *Scope
	lvl   int
	// sawCtor records that a `new (…) -> T` member was already lowered.
	sawCtor bool
}

// lower turns one written member into the element it contributes to the object.
//
// It returns ok=false for a member kind object type annotations do not support, which the caller
// reports once for the whole annotation rather than once per member. A nil element with ok=true
// is a supported member contributing nothing, because it already reported an error of its own —
// a second `new` signature, a key that is not a name, a property whose value did not resolve.
//
// Every kind lowers here, including the spread and mapped members that only ever appear on the
// ordered path. Which path an annotation takes decides how the elements are collected, not how
// they are built.
//
// A written receiver does not reach the lowered element. The parser peels `self` / `mut self`
// off into the member's Receiver so it never lands in Fn.Params, and subtyping compares a method
// through callableView, which drops SelfParam. A receiver written here therefore describes the
// shape without narrowing it, matching how it reads for a class method the annotation is checked
// against.
func (l *objAnnLowering) lower(elem ast.ObjTypeAnnElem) (soltype.ObjTypeElem, bool) {
	switch elem := elem.(type) {
	case *ast.PropertyTypeAnn:
		name, ft, ok := l.c.resolveObjectProperty(l.scope, elem, l.lvl)
		if !ok {
			return nil, true
		}
		return &soltype.PropertyElem{Name: name, Type: ft, Optional: elem.Optional, Readonly: elem.Readonly}, true
	case *ast.MethodTypeAnn:
		name, ok := objKeyName(elem.Name)
		if !ok {
			l.c.reportUnsupported(elem.Name)
			return nil, true
		}
		sig := l.c.resolveSigTypeAnn(l.scope, elem.Fn, l.lvl)
		return &soltype.MethodElem{Name: name, Signatures: []*soltype.FuncType{sig}}, true
	case *ast.GetterTypeAnn:
		name, ok := objKeyName(elem.Name)
		if !ok {
			l.c.reportUnsupported(elem.Name)
			return nil, true
		}
		// The value read and what reading it raises both come from the one resolved
		// signature. An absent `throws` clause leaves the signature's Throws nil, and nil is
		// the `never` shorthand GetterElem uses too, so it carries over with no special case.
		sig := l.c.resolveSigTypeAnn(l.scope, elem.Fn, l.lvl)
		return &soltype.GetterElem{Name: name, Type: sig.Ret, Throws: sig.Throws}, true
	case *ast.SetterTypeAnn:
		name, ok := objKeyName(elem.Name)
		if !ok {
			l.c.reportUnsupported(elem.Name)
			return nil, true
		}
		sig := l.c.resolveSigTypeAnn(l.scope, elem.Fn, l.lvl)
		// A well-formed setter declares exactly one value parameter beyond the receiver, the
		// value being assigned. Report any other count and then build the element from the
		// first parameter, or from `unknown` when there is none, so the object still carries a
		// member under the name the source wrote. This mirrors the class-member recovery.
		if len(sig.Params) != 1 {
			l.c.report(&SetterArityError{Name: name, Elem: elem, Count: len(sig.Params)})
		}
		var param soltype.Type = &soltype.UnknownType{}
		if len(sig.Params) > 0 {
			param = sig.Params[0].Type
		}
		return &soltype.SetterElem{Name: name, Param: param, Throws: sig.Throws}, true
	case *ast.ConstructorTypeAnn:
		if l.sawCtor {
			l.c.report(&DuplicateConstructorSignatureError{Ctor: elem})
			return nil, true
		}
		l.sawCtor = true
		return &soltype.ConstructorElem{Fn: l.c.resolveSigTypeAnn(l.scope, elem.Fn, l.lvl)}, true
	case *ast.RestSpreadTypeAnn:
		src, ok := l.c.resolveTypeAnn(l.scope, elem.Value, l.lvl)
		if !ok {
			src = l.c.freshAt(l.lvl)
		}
		return &soltype.SpreadElem{Type: src}, true
	case *ast.MappedTypeAnn:
		return l.c.resolveMappedElem(l.scope, elem, l.lvl), true
	}
	return nil, false
}

// resolveSigTypeAnn lowers the `(…) -> R throws E` tail a method, getter, or setter shares with
// a `fn` annotation. resolveFuncTypeAnn recovers every unsupported part of a signature to a
// fresh var, so it always yields a FuncType and its ok result is always true. Anything else is a
// wiring bug rather than a source error, so fail loudly instead of dropping the member.
func (c *checker) resolveSigTypeAnn(scope *Scope, ta *ast.FuncTypeAnn, lvl int) *soltype.FuncType {
	fn, _ := c.resolveFuncTypeAnn(scope, ta, lvl)
	sig, isFunc := fn.(*soltype.FuncType)
	if !isFunc {
		panic(fmt.Sprintf("resolveSigTypeAnn: signature resolved to %T, not *soltype.FuncType", fn))
	}
	return sig
}

// resolveMappedElem lowers a `[K]: V for K in Keys` member to a MappedElem, stored unreduced so the
// annotation prints the way the source wrote it rather than as the members it computes. constrain
// reduces the enclosing object when it checks a constraint against it, mirroring resolveKeyOfTypeAnn.
//
// The member sits in its object's element list alongside whatever else the source wrote, so
// `{a: number, [K]: V for K in Keys}` is one object rather than an intersection. reduceObject merges
// the computed fields with the sibling members in source order, the same merge a `...A` spread feeds.
//
// The `for K in Keys` clause binds K. The constraint itself resolves in the enclosing scope, since
// K is not in scope for the key set that binds it. The value, the bracketed key-remapping
// expression, and the `if C : E` filter each resolve in a child scope where K names the binding the
// evaluator substitutes a key for. That is the scope TypeScript gives a mapped type's key parameter,
// so `{[K]: T[K] for K in keyof T}` reads the same K in its value position that its clause
// introduced.
//
// An unsupported operand recovers to a fresh var, cascade-safe like the Promise<bad> recovery. A
// recovered operand leaves the mapped type unable to ground, so it stays symbolic rather than
// reducing to a wrong object.
func (c *checker) resolveMappedElem(scope *Scope, mapped *ast.MappedTypeAnn, lvl int) *soltype.MappedElem {
	keys, ok := c.resolveTypeAnn(scope, mapped.TypeParam.Constraint, lvl)
	if !ok {
		keys = c.freshAt(lvl)
	}

	key := c.ctx.freshMappedKey(mapped.TypeParam.Name)
	mappedScope := scope.Child()
	mappedScope.defineType(mapped.TypeParam.Name, TypeBinding{Type: key})

	value, ok := c.resolveTypeAnn(mappedScope, mapped.Value, lvl)
	if !ok {
		value = c.freshAt(lvl)
	}
	name := c.resolveMappedOperand(mappedScope, mapped.Name, lvl)
	// The parser fills Check and Extends together or leaves both nil, so one present without the
	// other is a malformed filter the reduction would have to guess at. Resolve the pair only when
	// both are written, which drops such a filter rather than applying half of it.
	var check, extends soltype.Type
	if mapped.Check != nil && mapped.Extends != nil {
		check = c.resolveMappedOperand(mappedScope, mapped.Check, lvl)
		extends = c.resolveMappedOperand(mappedScope, mapped.Extends, lvl)
	}

	return &soltype.MappedElem{
		Key:      key,
		Keys:     keys,
		Value:    value,
		Name:     name,
		Check:    check,
		Extends:  extends,
		Optional: mappedModifier(mapped.Optional),
		Readonly: mappedModifier(mapped.ReadOnly),
	}
}

// resolveMappedOperand lowers one of a mapped type's optional operands — the bracketed key-remapping
// expression, or either half of the `if C : E` filter. An absent annotation stays absent, and an
// unsupported one recovers to a fresh var so the mapped type keeps its shape.
func (c *checker) resolveMappedOperand(scope *Scope, ta ast.TypeAnn, lvl int) soltype.Type {
	if ta == nil {
		return nil
	}
	if t, ok := c.resolveTypeAnn(scope, ta, lvl); ok {
		return t
	}
	return c.freshAt(lvl)
}

// mappedModifier converts one written `readonly` or `?` marker to the modifier the reduction
// applies. An absent marker leaves the emitted field unmarked; `+` and the bare form add the marker
// and `-` removes it.
func mappedModifier(m *ast.MappedModifier) soltype.MappedModifier {
	switch {
	case m == nil:
		return soltype.ModNone
	case *m == ast.MMRemove:
		return soltype.ModRemove
	default:
		return soltype.ModAdd
	}
}

// resolveObjectProperty lowers one `name: T` / `name?: T` property annotation to its name and
// resolved field type. It reports false only when the property key is not a static name. A missing
// or unsupported value annotation recovers to a fresh var, keeping the object shape cascade-safe,
// mirroring the Promise<bad> recovery. Shared by the spread-free and spread-carrying paths.
func (c *checker) resolveObjectProperty(scope *Scope, prop *ast.PropertyTypeAnn, lvl int) (string, soltype.Type, bool) {
	name, ok := objKeyName(prop.Name)
	if !ok {
		c.reportUnsupported(prop.Name)
		return "", nil, false
	}
	var ft soltype.Type = c.freshAt(lvl)
	if prop.Value != nil {
		value := prop.Value
		// An owned-mutable field `{a: mut {x}}` is rejected (#779): a `mut` cell
		// nested inside a non-mut container is misleading, since the container's
		// immutability already reaches into the field. Recover to the field's bare
		// inner so the object keeps a sensible shape. A `&`/`&mut` borrow field is a
		// reference to external storage, not an interior cell, so it stays legal.
		if mta, ok := value.(*ast.MutableTypeAnn); ok {
			c.report(&MutFieldError{Ann: mta})
			value = mta.Target
		}
		if t, ok := c.resolveTypeAnn(scope, value, lvl); ok {
			ft = t
		}
	}
	return name, ft, true
}

// resolveTupleTypeAnn lowers a tuple type annotation to a soltype.TupleType, honoring the trailing
// `...` inexact marker. A rest-spread element (`[...P, x]`) becomes a soltype.RestSpreadType
// element; a tuple carrying one is a residual the evaluator reduces once the spread operand grounds
// to a concrete tuple, splicing it in position. Until then a spread over a type parameter stays
// symbolic. The bare trailing `...` inexact marker is carried on ta.Inexact, not as an element. An
// element whose annotation is unsupported recovers to a fresh var so the tuple keeps its arity. A
// `[number, ...Array<number>]` variadic tail is distinct and stays out of scope until Array lands;
// its spread operand fails to resolve today and recovers.
func (c *checker) resolveTupleTypeAnn(scope *Scope, ta *ast.TupleTypeAnn, lvl int) (soltype.Type, bool) {
	elems := make([]soltype.Type, 0, len(ta.Elems))
	for _, el := range ta.Elems {
		spread := false
		operand := el
		if rest, ok := el.(*ast.RestSpreadTypeAnn); ok {
			spread = true
			operand = rest.Value
		}
		// An owned-mutable element `[mut {x}]` or a mutable spread operand `[...mut P]` is rejected
		// (#779), the tuple twin of the object-property rejection: a `mut` cell nested inside a
		// non-mut container is misleading. Recover to its bare inner. A `&`/`&mut` borrow element
		// stays legal — it references external storage.
		if mta, ok := operand.(*ast.MutableTypeAnn); ok {
			c.report(&MutFieldError{Ann: mta})
			operand = mta.Target
		}
		t, ok := c.resolveTypeAnn(scope, operand, lvl)
		if !ok {
			t = c.freshAt(lvl)
		}
		if spread {
			t = &soltype.RestSpreadType{Operand: t}
		}
		elems = append(elems, t)
	}
	t := &soltype.TupleType{Elems: elems, Inexact: ta.Inexact}
	c.recordProv(t, ta, AnnotationType)
	return t, true
}

// resolveUnionTypeAnn lowers `A | B | …` through newUnion. An unsupported
// member recovers to a fresh var so the union shape survives, mirroring the
// Promise<bad> and object/tuple cascade-safe recovery. A union is always closed:
// openness is written as `string`, `number`, or `unknown` in a member, not as a
// trailing `...` on the union.
func (c *checker) resolveUnionTypeAnn(scope *Scope, ta *ast.UnionTypeAnn, lvl int) (soltype.Type, bool) {
	members := make([]soltype.Type, len(ta.Types))
	for i, m := range ta.Types {
		if t, ok := c.resolveTypeAnn(scope, m, lvl); ok {
			members[i] = t
		} else {
			// freshAt over ErrorType: preserves the source's union shape
			// in the rendered type. pruneUnion would drop an ErrorType
			// member and collapse the union.
			members[i] = c.freshAt(lvl)
		}
	}
	t := newUnion(c.ctx, members)
	// newUnion can collapse to an input member's pointer on single-member dedup or
	// subsumption, so the result may already carry that member's blame. recordProvForResult
	// records only a fresh, uniquely-owned result.
	c.recordProvForResult(t, ta, AnnotationType)
	return t, true
}

// resolveIntersectionTypeAnn is the meet twin of resolveUnionTypeAnn.
func (c *checker) resolveIntersectionTypeAnn(scope *Scope, ta *ast.IntersectionTypeAnn, lvl int) (soltype.Type, bool) {
	members := make([]soltype.Type, len(ta.Types))
	for i, m := range ta.Types {
		if t, ok := c.resolveTypeAnn(scope, m, lvl); ok {
			members[i] = t
		} else {
			members[i] = c.freshAt(lvl) // see resolveUnionTypeAnn
		}
	}
	t := newIntersection(c.ctx, members)
	c.recordProvForResult(t, ta, AnnotationType)
	return t, true
}

// resolveKeyOfTypeAnn lowers `keyof T` to a KeyofType residual and stores it unreduced, so the
// annotation prints the way the source wrote it — `keyof {x: number}` renders `keyof {x: number}`,
// not `"x"`. constrain reduces the residual when it checks a constraint against it. An unsupported
// operand recovers to a fresh var, cascade-safe like the Promise<bad> recovery.
func (c *checker) resolveKeyOfTypeAnn(scope *Scope, ta *ast.KeyOfTypeAnn, lvl int) (soltype.Type, bool) {
	operand, ok := c.resolveTypeAnn(scope, ta.Type, lvl)
	if !ok {
		operand = c.freshAt(lvl)
	}
	t := &soltype.KeyofType{Operand: operand}
	c.recordProv(t, ta, AnnotationType)
	return t, true
}

// resolveNegationTypeAnn lowers `~T` to the complement of its operand through newNegation, which
// folds `~never` to `unknown`, `~unknown` to `never`, `~~T` to `T`, and `~(open union)` to `never`.
// An unsupported operand recovers to a fresh var, cascade-safe like the Promise<bad> recovery.
//
// A borrow is an ordinary operand. `~(&'a Point)` names every value that is not that borrow.
func (c *checker) resolveNegationTypeAnn(scope *Scope, ta *ast.NegationTypeAnn, lvl int) (soltype.Type, bool) {
	operand, ok := c.resolveTypeAnn(scope, ta.Type, lvl)
	if !ok {
		operand = c.freshAt(lvl)
	}
	t := newNegation(operand)
	// newNegation can return a pointer that is not freshly minted: `~~T` folds to the
	// operand's inner T, and the lattice-bound folds return the shared zero-size
	// NeverType/UnknownType singletons. recordProvForResult skips both the already-blamed
	// inner and the module-shared singleton, mirroring resolveUnionTypeAnn.
	c.recordProvForResult(t, ta, AnnotationType)
	return t, true
}

// resolveLitTypeAnn lowers a literal type annotation to the type it names. A string, number, or
// boolean literal becomes its LitType through litTypeOf. This is the annotation home for a literal
// type, which an indexed access needs for its key, so `Point["x"]` carries `"x"` as a LitTypeAnn
// index.
//
// `null` and `undefined` become the atoms atomLitOf returns. That makes both writable in an
// annotation, so a binding can be declared `val n: null = null` and `NonNullable<T>` can test
// its argument against `null | undefined`. recordProvForResult records nothing for either atom,
// since both are shared zero-size singletons that cannot carry provenance.
//
// A literal with no soltype form, such as a regex or a bigint, reports unsupported and recovers.
func (c *checker) resolveLitTypeAnn(ta *ast.LitTypeAnn) (soltype.Type, bool) {
	if atom, _, isAtom := atomLitOf(ta.Lit); isAtom {
		c.recordProvForResult(atom, ta, AnnotationType)
		return atom, true
	}
	lit, ok := c.litTypeOf(ta.Lit)
	if !ok {
		return c.reportUnsupported(ta), false
	}
	c.recordProv(lit, ta, AnnotationType)
	return lit, true
}

// resolveIndexTypeAnn lowers `T[K]` to an IndexType residual and stores it unreduced, so the
// annotation prints the way the source wrote it — `Point["x"]` renders `Point["x"]`, not
// `number`. constrain reduces the residual when it checks a constraint against it, mirroring
// resolveKeyOfTypeAnn. An unsupported target or index recovers to a fresh var, cascade-safe like
// the Promise<bad> recovery.
func (c *checker) resolveIndexTypeAnn(scope *Scope, ta *ast.IndexTypeAnn, lvl int) (soltype.Type, bool) {
	target, ok := c.resolveTypeAnn(scope, ta.Target, lvl)
	if !ok {
		target = c.freshAt(lvl)
	}
	index, ok := c.resolveTypeAnn(scope, ta.Index, lvl)
	if !ok {
		index = c.freshAt(lvl)
	}
	t := &soltype.IndexType{Target: target, Index: index}
	c.recordProv(t, ta, AnnotationType)
	return t, true
}

// resolveCondTypeAnn lowers `if Check : Extends { Then } else { Else }` to a CondType residual and
// stores it unreduced, so the annotation prints the way the source wrote it. constrain decides the
// branch when it checks a constraint against it: a ground `Check <: Extends` probe selects Then or
// Else, while a conditional over a type parameter stays symbolic. An unsupported operand recovers to
// a fresh var, cascade-safe like the Promise<bad> recovery.
//
// Each `infer U` clause in the Extends operand introduces the name U. The name is declared once, in
// a child scope, before either operand resolves, so the clause and the Then branch's references to
// it share one declaration and a capture substituted for that declaration reaches both. The child
// scope covers the Extends and Then operands, matching the position TypeScript scopes an `infer`
// name to, so `if T : [infer U] { U } else { boolean }` resolves its Then branch to the capture its
// Extends declares while the same name written in the Else branch is an unbound reference. The
// evaluator fills each capture from the subtype check that decides the branch, at reduction time.
//
// A conditional whose Check is written as a bare type-parameter reference is marked Distribute, the
// naked-type-parameter rule the evaluator applies when the Check grounds to a union.
func (c *checker) resolveCondTypeAnn(scope *Scope, ta *ast.CondTypeAnn, lvl int) (soltype.Type, bool) {
	// Declare each name the Extends operand introduces, so the clause that declares it and the Then
	// branch's references to it resolve to one shared declaration.
	condScope := scope
	if names := inferAnnNames(ta.Extends); len(names) > 0 {
		condScope = scope.Child()
		for _, name := range names {
			condScope.defineType(name, TypeBinding{Type: c.ctx.freshInferDecl(name)})
		}
	}

	// The Extends operand is the one position an `infer` clause is legal in. Save and restore the
	// flag around each operand so a nested conditional's own operands decide it independently.
	savedInCondExtends := c.inCondExtends
	defer func() { c.inCondExtends = savedInCondExtends }()

	c.inCondExtends = false
	check, checkOK := c.resolveTypeAnn(scope, ta.Check, lvl)
	if !checkOK {
		check = c.freshAt(lvl)
	}

	c.inCondExtends = true
	extends, ok := c.resolveTypeAnn(condScope, ta.Extends, lvl)
	if !ok {
		extends = c.freshAt(lvl)
	}

	c.inCondExtends = false
	then, ok := c.resolveTypeAnn(condScope, ta.Then, lvl)
	if !ok {
		then = c.freshAt(lvl)
	}
	els, ok := c.resolveTypeAnn(scope, ta.Else, lvl)
	if !ok {
		els = c.freshAt(lvl)
	}

	t := &soltype.CondType{
		Check:   check,
		Extends: extends,
		Then:    then,
		Else:    els,
		// A Check the resolver could not resolve recovered to a fresh var, which is the same kind
		// nakedTypeParamCheck accepts, so the flag is decided only when the written Check actually
		// resolved. Otherwise a bare unresolvable name would be read as a type parameter.
		Distribute: checkOK && nakedTypeParamCheck(ta.Check, check),
	}
	c.recordProv(t, ta, AnnotationType)
	return t, true
}

// resolveInferTypeAnn lowers an `infer U` clause to the binder the evaluator captures a type at,
// reading U's declaration from the scope resolveCondTypeAnn declared it in so the binder and the
// branch's references carry one id. The clause is legal only inside a
// conditional's Extends operand, which is where a matched position exists to capture; anywhere else
// it names no capture, so it reports an unsupported feature and recovers.
func (c *checker) resolveInferTypeAnn(scope *Scope, ta *ast.InferTypeAnn) (soltype.Type, bool) {
	b, found := scope.GetType(ta.Name)
	decl, isDecl := b.Type.(*soltype.InferType)
	// resolveCondTypeAnn declares every name it collects from an Extends operand, so a clause reached
	// while resolving one always finds its declaration. A clause anywhere else finds none, or finds a
	// binding of some other sort under the same name, and is rejected either way.
	if !c.inCondExtends || !found || !isDecl {
		return c.reportUnsupportedFeature(ta, "infer outside a conditional type's extends operand"), false
	}
	t := &soltype.InferType{ID: decl.ID, Name: decl.Name, Binder: true}
	c.recordProv(t, ta, AnnotationType)
	return t, true
}

// nakedTypeParamCheck reports whether a conditional's Check was written as a bare reference to a
// type parameter, the shape TypeScript distributes a union Check over. The written form must be a
// name with no type arguments, and that name must resolve to a type variable, which is what
// resolveTypeParams mints for each `<T>` binder. A literal type, an alias, or a `[T]` wrapper each
// fails one of the two tests, so the conditional decides a union Check as a whole. This is the first
// of the two conditions distribution needs; soltype.CondType's Distribute field states both.
func nakedTypeParamCheck(ann ast.TypeAnn, resolved soltype.Type) bool {
	ref, ok := ann.(*ast.TypeRefTypeAnn)
	if !ok || len(ref.TypeArgs) > 0 {
		return false
	}
	_, isVar := resolved.(*soltype.TypeVarType)
	return isVar
}

// inferAnnNames returns the names the `infer U` clauses of one annotation subtree introduce, in
// source order with duplicates collapsed. resolveCondTypeAnn reads its Extends operand's names to
// bind them for the Then branch.
func inferAnnNames(ta ast.TypeAnn) []string {
	f := &inferAnnFinder{seen: set.NewSet[string]()}
	ta.Accept(f)
	return f.names
}

// inferAnnFinder is the AST visitor behind inferAnnNames. It collects each InferTypeAnn name it
// reaches, skipping one it has already recorded so a name written twice binds once.
type inferAnnFinder struct {
	ast.DefaultVisitor
	seen  set.Set[string]
	names []string
}

func (f *inferAnnFinder) EnterTypeAnn(ta ast.TypeAnn) bool {
	if it, ok := ta.(*ast.InferTypeAnn); ok && !f.seen.Contains(it.Name) {
		f.seen.Add(it.Name)
		f.names = append(f.names, it.Name)
	}
	return true
}

// resolveTypeOfTypeAnn lowers a `typeof v` query to a TypeofType residual: it resolves the value's
// type at annotation time and stores it unrendered behind the value reference, so the annotation
// prints `typeof v` the way the source wrote it while constrain compares against the resolved type.
// A name or member that resolves to no readable value reports an unsupported feature and recovers.
func (c *checker) resolveTypeOfTypeAnn(scope *Scope, ta *ast.TypeOfTypeAnn) (soltype.Type, bool) {
	ty, ok := c.resolveTypeOfQualIdent(scope, ta.Value)
	if !ok {
		return c.reportUnsupportedFeature(ta, "typeof of a name that is not a readable value"), false
	}
	t := &soltype.TypeofType{Ident: ast.QualIdentToString(ta.Value), Ty: ty}
	c.recordProv(t, ta, AnnotationType)
	return t, true
}

// resolveTemplateLitTypeAnn lowers a template literal type such as `on${T}` to a
// TemplateLitType residual and stores it unreduced, so the annotation prints the way the source
// wrote it rather than the reduced union of string literals. constrain reduces the residual when it
// checks a constraint against it, mirroring resolveKeyOfTypeAnn. The parser records one more fixed
// segment than interpolation, so Quasis carries len(Interps)+1 entries. An unsupported
// interpolation recovers to a fresh var, cascade-safe like the Promise<bad> recovery.
func (c *checker) resolveTemplateLitTypeAnn(scope *Scope, ta *ast.TemplateLitTypeAnn, lvl int) (soltype.Type, bool) {
	quasis := make([]string, len(ta.Quasis))
	for i, q := range ta.Quasis {
		quasis[i] = q.Value
	}
	interps := make([]soltype.Type, len(ta.TypeAnns))
	for i, ann := range ta.TypeAnns {
		t, ok := c.resolveTypeAnn(scope, ann, lvl)
		if !ok {
			t = c.freshAt(lvl)
		}
		interps[i] = t
	}
	t := &soltype.TemplateLitType{Quasis: quasis, Interps: interps}
	c.recordProv(t, ta, AnnotationType)
	return t, true
}

// stringIntrinsics maps each intrinsic string operator's reference name to its kind, so a
// `Uppercase<T>` reference resolves to a StringIntrinsicType residual. A user-defined type of the
// same name takes precedence, since resolveScopedTypeRef runs first.
var stringIntrinsics = map[string]soltype.StringIntrinsicKind{
	"Uppercase":    soltype.Uppercase,
	"Lowercase":    soltype.Lowercase,
	"Capitalize":   soltype.Capitalize,
	"Uncapitalize": soltype.Uncapitalize,
}

// resolveStringIntrinsic lowers an intrinsic string-operator reference `Uppercase<T>` and its three
// siblings to a StringIntrinsicType residual, stored unreduced so the annotation prints the way the
// source wrote it. It matches only a single-argument reference with no lifetime arguments; any other
// name or arity reports ok=false so the caller falls through to its unsupported-feature recovery. An
// unsupported operand recovers to a fresh var, cascade-safe like the Promise<bad> recovery.
func (c *checker) resolveStringIntrinsic(scope *Scope, ta *ast.TypeRefTypeAnn, lvl int) (soltype.Type, bool) {
	kind, named := stringIntrinsics[ast.QualIdentToString(ta.Name)]
	if !named {
		return nil, false
	}
	operand, ok := c.unaryIntrinsicOperand(scope, ta, lvl)
	if !ok {
		return nil, false
	}
	t := &soltype.StringIntrinsicType{Kind: kind, Operand: operand}
	c.recordProv(t, ta, AnnotationType)
	return t, true
}

// unaryIntrinsicOperand resolves the sole type argument of a one-argument intrinsic reference such
// as `Uppercase<T>` or `Exact<T>`. ok=false means the reference is not the one-argument form those
// intrinsics take, because its arity is not one or it carries a lifetime argument. The caller then
// reports no match, and its own caller falls through to the unsupported-feature recovery. An
// unsupported operand recovers to a fresh var, cascade-safe like the Promise<bad> recovery.
func (c *checker) unaryIntrinsicOperand(scope *Scope, ta *ast.TypeRefTypeAnn, lvl int) (soltype.Type, bool) {
	if len(ta.TypeArgs) != 1 || len(ta.LifetimeArgs) > 0 || ta.Lifetime != nil {
		return nil, false
	}
	operand, ok := c.resolveTypeAnn(scope, ta.TypeArgs[0], lvl)
	if !ok {
		operand = c.freshAt(lvl)
	}
	return operand, true
}

// exactnessIntrinsics maps each exactness operator's reference name to its kind, so an `Exact<T>`
// reference resolves to an ExactnessType residual. A user-defined type of the same name takes
// precedence, since resolveScopedTypeRef runs first.
var exactnessIntrinsics = map[string]soltype.ExactnessKind{
	"Exact":   soltype.MakeExact,
	"Inexact": soltype.MakeInexact,
}

// resolveExactnessIntrinsic lowers an `Exact<T>` or `Inexact<T>` reference to an ExactnessType
// residual, stored unreduced so the annotation prints the way the source wrote it. It matches only a
// single-argument reference with no lifetime arguments; any other name or arity reports ok=false so
// the caller falls through to its unsupported-feature recovery.
func (c *checker) resolveExactnessIntrinsic(scope *Scope, ta *ast.TypeRefTypeAnn, lvl int) (soltype.Type, bool) {
	kind, named := exactnessIntrinsics[ast.QualIdentToString(ta.Name)]
	if !named {
		return nil, false
	}
	operand, ok := c.unaryIntrinsicOperand(scope, ta, lvl)
	if !ok {
		return nil, false
	}
	t := &soltype.ExactnessType{Kind: kind, Operand: operand}
	c.recordProv(t, ta, AnnotationType)
	return t, true
}

// resolveTypeOfQualIdent resolves a `typeof` operand — a bare value name or a member chain
// `p.x` — to the value's type, porting the old checker's resolveTypeOfQualIdent
// (internal/checker/expand_type.go). A name reads the value's coalesced scheme type; a member
// chain projects each property off the resolved base. ok=false when a step does not resolve.
//
// The result is not stamped with provenance: it is a shared binding/property type, not the
// freshly-minted unique pointer recordProv requires, so a mismatch blames its constraint site.
func (c *checker) resolveTypeOfQualIdent(scope *Scope, ident ast.QualIdent) (soltype.Type, bool) {
	switch id := ident.(type) {
	case *ast.Ident:
		if b, ok := scope.GetValue(id.Name); ok {
			// bindingType takes the scheme's coalesced concrete type, not a fresh inference
			// var that would coalesce to unknown in a negative position such as the operand of
			// `keyof typeof v`. The dep graph orders v first, so its scheme is final here; a
			// definition-less binding yields nil, i.e. the ok=false path.
			if t := bindingType(b); t != nil {
				return t, true
			}
		}
		return nil, false
	case *ast.Member:
		recv, ok := c.resolveTypeOfQualIdent(scope, id.Left)
		if !ok {
			return nil, false
		}
		return c.typeofMember(recv, id.Right.Name)
	}
	return nil, false
}

// typeofMember projects the named property off a `typeof p.x` receiver: it strips any borrow
// wrapper, then reads the member off the carrier object, returning nothing when the receiver
// is not an object or carries no such readable member.
func (c *checker) typeofMember(recv soltype.Type, name string) (soltype.Type, bool) {
	obj, ok := c.ctx.readCarrierObject(readCarrier(recv))
	if !ok {
		return nil, false
	}
	read, hasValue, _ := memberReadContribution(obj, name)
	if !hasValue {
		return nil, false
	}
	return read, true
}

// resolveFuncTypeAnn lowers a function type annotation `fn<T>(p: A, ...) -> R` into a
// soltype.FuncType, recovering an unsupported part to a fresh var so the shape survives. A
// `<T>` list resolves through resolveTypeParams into a child scope, so a parameter, return,
// or union member that names `T` reads the annotation's own quantified var.
func (c *checker) resolveFuncTypeAnn(scope *Scope, ta *ast.FuncTypeAnn, lvl int) (soltype.Type, bool) {
	// A function type annotation is its own quantifier scope, so give it its own
	// named-lifetime map the way inferFunc does for a function body. Without this a
	// nested `fn<'a: 'static>(…)` annotation would resolve `'a` to the enclosing
	// function's `'a` and its declared bound would force that outer lifetime, so an
	// unrelated borrow parameter of the enclosing function would be pinned to 'static.
	savedNamedLts := c.namedLifetimes
	c.namedLifetimes = nil
	defer func() { c.namedLifetimes = savedNamedLts }()

	// Resolve the annotation's type parameters into a child scope so a parameter, the
	// return, and any union member all read a sibling `T` as one shared var. A
	// non-generic annotation reuses the enclosing scope.
	annScope := scope
	var typeParams []*soltype.TypeParam
	if len(ta.TypeParams) > 0 {
		annScope = scope.Child()
		typeParams = c.resolveTypeParams(annScope, lvl, ta.TypeParams)
	}

	// Report any named lifetime this annotation uses without binding it in its own `<…>`
	// list, and the symmetric unused binder, before lowering its bounds interns the names.
	c.checkLifetimeDeclarations(ta.LifetimeParams, ta.Params, ta.Return, ta.Throws)
	c.lowerLifetimeParamBounds(ta.LifetimeParams, lvl)

	params := make([]*soltype.FuncParam, len(ta.Params))
	for i, p := range ta.Params {
		pat := p.Pattern
		// A `...xs: T` parameter sets Rest. That flag is what raises the function's
		// accept-set ceiling and what an `infer` clause in the slot captures the surplus
		// arguments into. Three things the parser accepts are rejected here, and each
		// recovers the parameter to a positional one so the function keeps its arity.
		//
		//  1. A rest parameter written anywhere but last, since acceptSet reads the flag off
		//     the last parameter only.
		//  2. A rest parameter with no type annotation. The slot's type is what says how many
		//     arguments the parameter binds, and the fresh var an unannotated parameter
		//     recovers to says nothing, so keeping Rest would let the initializer decide the
		//     declared type's arity.
		//  3. A rest parameter marked `?`. A slot binding zero or more arguments is already
		//     omittable and a tuple slot fixes its count, so the marker changes nothing.
		rest := false
		optional := p.Optional
		if rp, ok := pat.(*ast.RestPat); ok {
			switch {
			case i != len(ta.Params)-1:
				c.report(&RestParamNotLastError{Param: rp})
			case p.TypeAnn == nil:
				c.report(&RestParamNeedsTypeError{Param: rp})
			case p.Optional:
				// The recovery drops the marker along with Rest. Keeping it would give the
				// parameter an accept-set the source never asked for and cascade a second
				// error out of the one just reported.
				c.report(&OptionalRestParamError{Param: rp})
				optional = false
			default:
				rest = true
			}
			pat = rp.Pattern
		}
		// A missing or unsupported parameter annotation recovers to a fresh var so
		// the function keeps its arity and shape, cascade-safe like Promise<bad>.
		var pt soltype.Type = c.freshAt(lvl)
		if p.TypeAnn != nil {
			if t, ok := c.resolveTypeAnn(annScope, p.TypeAnn, lvl); ok {
				pt = t
			}
		}
		// A generic function in parameter position is a rank-2 callback such as
		// `cb: <T>(x: T) -> T`. Its `<T>` binder is kept on the parameter's FuncType, so a
		// call site checks each argument against it by skolemizing `T`, and a use inside the
		// body instantiates `T` per call. constrain's FuncType arm performs both steps.
		// The pattern is carried for rendering and round-tripping only, with no scope
		// binding. mirrorParamPat preserves its full shape.
		params[i] = &soltype.FuncParam{Pattern: c.mirrorParamPat(pat), Type: pt, Optional: optional, Rest: rest}
	}

	// The parser requires `-> R`, so ta.Return is normally non-nil. Guard
	// defensively and recover an unsupported or absent return to a fresh var,
	// keeping the function shape.
	var ret soltype.Type = c.freshAt(lvl)
	if ta.Return != nil {
		if t, ok := c.resolveTypeAnn(annScope, ta.Return, lvl); ok {
			ret = t
		}
	}

	// An absent clause leaves Throws nil, which reads as `never`: the annotation promises
	// the function raises nothing. An unsupported clause recovers to a fresh var instead,
	// matching the parameter and return positions, so it is not re-reported at every use.
	var throws soltype.Type
	if ta.Throws != nil {
		throws = c.freshAt(lvl)
		if t, ok := c.resolveTypeAnn(annScope, ta.Throws, lvl); ok {
			throws = t
		}
	}

	t := &soltype.FuncType{Params: params, Ret: ret, Throws: throws, Inexact: ta.Inexact, TypeParams: typeParams}
	c.recordProv(t, ta, AnnotationType)
	return t, true
}

// isWildcardAnn reports whether ta is the `_` inference placeholder. A signature position
// written `_` asks to be inferred rather than declaring a type, so a check that faults an
// over-declared signature has nothing to fault there.
func isWildcardAnn(ta ast.TypeAnn) bool {
	_, wildcard := ta.(*ast.WildcardTypeAnn)
	return wildcard
}

// mirrorParamPat structurally mirrors a function-type-annotation parameter pattern
// into its soltype.Pat for rendering. A shape with no soltype counterpart is dropped.
func (c *checker) mirrorParamPat(pat ast.Pat) soltype.Pat {
	switch p := pat.(type) {
	case *ast.IdentPat:
		return &soltype.IdentPat{Name: p.Name}
	case *ast.WildcardPat:
		return &soltype.WildcardPat{}
	case *ast.LitPat:
		if _, atomPat, isAtom := atomLitOf(p.Lit); isAtom {
			return atomPat
		}
		if lt, ok := c.litTypeOf(p.Lit); ok {
			return &soltype.LitPat{Lit: lt.Lit}
		}
		return nil
	case *ast.RestPat:
		return &soltype.RestPat{Pattern: c.mirrorParamPat(p.Pattern)}
	case *ast.TuplePat:
		elems := make([]soltype.Pat, 0, len(p.Elems))
		for _, e := range p.Elems {
			elems = append(elems, c.mirrorParamPat(e))
		}
		return &soltype.TuplePat{Elems: elems}
	case *ast.ObjectPat:
		fields := make([]*soltype.ObjectPatField, 0, len(p.Elems))
		var rest soltype.Pat
		for _, elem := range p.Elems {
			switch e := elem.(type) {
			case *ast.ObjShorthandPat:
				// A bare `{x}` mirrors to a field whose value is the IdentPat `x`.
				fields = append(fields, &soltype.ObjectPatField{Name: e.Key.Name, Value: &soltype.IdentPat{Name: e.Key.Name}})
			case *ast.ObjKeyValuePat:
				fields = append(fields, &soltype.ObjectPatField{Name: e.Key.Name, Value: c.mirrorParamPat(e.Value)})
			case *ast.ObjRestPat:
				// An object's rest has no position among the named fields, so it mirrors
				// into ObjectPat.Rest rather than into the field list. A sub-pattern with
				// no soltype counterpart mirrors to the wildcard rather than to nil, so
				// the written `...` still renders. `{x, .../ab/}` reads back `{x, ..._}`,
				// the same recovery the tuple path's `[a, ..._]` produces, instead of
				// dropping the rest and reading back `{x}`.
				rest = c.mirrorParamPat(e.Pattern)
				if rest == nil {
					rest = &soltype.WildcardPat{}
				}
			}
		}
		return &soltype.ObjectPat{Fields: fields, Rest: rest}
	case *ast.ExtractorPat:
		args := make([]soltype.Pat, len(p.Args))
		for i, a := range p.Args {
			args[i] = c.mirrorParamPat(a)
		}
		return &soltype.ExtractorPat{Name: ast.QualIdentToString(p.Name), Args: args}
	case *ast.InstancePat:
		obj, _ := c.mirrorParamPat(p.Object).(*soltype.ObjectPat)
		return &soltype.InstancePat{ClassName: ast.QualIdentToString(p.ClassName), Object: obj}
	default:
		return nil
	}
}

// resolveMutableTypeAnn lowers a `mut T` annotation to an owned-mutable borrow,
// RefType{Mut: true, Lt: nil, Inner: T} (the C1 RefType wrapper). The lifetime
// borrow forms (`'a T`, `mut 'a T`) still defer: a named lifetime needs the
// lifetime sort (D1), and the parser already rejects a lifetime before a non-
// reference inner, so only the no-lifetime `mut` form reaches here.
//
// `mut` over a non-borrowable inner (a primitive, function, promise — anything
// outside RefInner) is a no-op in the value-types model: there is nothing to
// borrow. It reports an unsupported feature rather than fabricating a borrow over
// a type the wrapper cannot hold.
// resolveMutableTypeAnn stores the lazy deep-mut form (PR 14): the inner is wrapped
// in one owned-mutable RefType without rewriting its children. `mut {a: {x}}` stays
// `mut {a: {x}}` rather than deepening to `mut {a: mut {x}}`. The deep-mut rule —
// every nested object/tuple field is invariant and reads back mutable — is applied
// at access and constrain time, via fieldReadBorrow's recvMut propagation and
// constrain's mut-context flag, so the stored type matches the surface annotation.
func (c *checker) resolveMutableTypeAnn(scope *Scope, ta *ast.MutableTypeAnn, lvl int) (soltype.Type, bool) {
	ri, ok := c.borrowInner(scope, ta.Target, lvl)
	if !ok {
		return c.reportUnsupportedFeature(ta, "mut on a non-borrowable type"), false
	}
	t := soltype.NewRef(true, nil, ri)
	c.recordProv(t, ta, AnnotationType)
	return t, true
}

// borrowInner resolves the pointee of a `mut` annotation to a RefInner, the
// inner-resolution step of resolveMutableTypeAnn. resolveRefTypeAnn resolves its own
// inner directly so it can intercept a nested borrow before the RefInner cast. An
// unsupported inner recovers to a fresh var, which IS a RefInner, so the wrapper is
// preserved and the binding stays cascade-safe. ok=false means the inner resolved to a
// concrete non-borrowable type such as a primitive, function, or promise. The caller
// reports that with a wrapper-specific message.
func (c *checker) borrowInner(scope *Scope, ta ast.TypeAnn, lvl int) (soltype.RefInner, bool) {
	inner, ok := c.resolveTypeAnn(scope, ta, lvl)
	if !ok {
		return c.freshAt(lvl), true
	}
	ri, isRI := inner.(soltype.RefInner)
	return ri, isRI
}

// resolveRefTypeAnn lowers a borrow annotation `&T`, `&mut T`, `&'a T`, or `&'a mut T`
// to a soltype.RefType{Mut, Lt, Inner}. The inner must be a RefInner. A borrow of a
// value type such as a primitive has nothing to point at and is reported as an
// unsupported feature.
//
// resolveLifetimeAnn mints the lifetime. A bare `&` gets a fresh inferred lifetime, and
// `&'a` resolves the named lifetime to the variable that name denotes in the current
// function. Display naming is decided structurally at coalesce time, so a borrow that
// reaches an output renders under a quantified name like `&'a {x}`, while one that
// connects nothing elides. Unlike resolveMutableTypeAnn, this arm always sets Lt, so the
// result is a genuine borrow rather than an owned value.
func (c *checker) resolveRefTypeAnn(scope *Scope, ta *ast.RefTypeAnn, lvl int) (soltype.Type, bool) {
	lt := c.resolveLifetimeAnn(ta.Lifetime, lvl)
	inner, ok := c.resolveTypeAnn(scope, ta.Inner, lvl)
	if !ok {
		// Recover an unsupported inner to a fresh var so the borrow wrapper survives, cascade-safe.
		inner = c.freshAt(lvl)
	}
	// A borrow whose pointee is itself a borrow collapses to depth one.
	if nested, isRef := inner.(*soltype.RefType); isRef {
		return c.normalizeNestedBorrow(ta, lt, nested)
	}
	ri, isRI := inner.(soltype.RefInner)
	if !isRI {
		return c.reportUnsupportedFeature(ta, "borrow of a non-borrowable type"), false
	}
	// The lazy deep-mut form stores `&mut {a: {x}}` verbatim. The deep-mut rule is
	// applied at access and constrain time rather than by rewriting the pointee's
	// children here.
	t := &soltype.RefType{Mut: ta.Mut, Lt: lt, Inner: ri}
	c.recordProv(t, ta, AnnotationType)
	return t, true
}

// normalizeNestedBorrow collapses a borrow whose pointee is itself a borrow to depth
// one, for a nested borrow such as `&&Point`. Two cases:
//
//   - An immutable outer layer collapses, since an immutable borrow is Copy. `&'a &'b
//     Point` reduces to `&'a Point` at the outer lifetime, with 'b outliving 'a.
//   - A mutable outer layer is uninhabitable, since `&mut &…` would repoint the inner
//     borrow, which needs a storage cell the JS target cannot express. It is rejected.
func (c *checker) normalizeNestedBorrow(ta *ast.RefTypeAnn, outerLt soltype.Lifetime, inner *soltype.RefType) (soltype.Type, bool) {
	if ta.Mut {
		return c.reportUnsupportedFeature(ta, "mutable borrow of a borrow is uninhabitable"), false
	}
	if inner.Lt != nil && outerLt != nil {
		c.ctx.constrainLt(inner.Lt, outerLt)
	}
	t := soltype.NewRef(false, outerLt, inner.Inner)
	c.recordProv(t, ta, AnnotationType)
	return t, true
}

// resolveLifetimeAnn resolves the lifetime of a borrow annotation. A nil node is
// an inferred borrow and mints a fresh lifetime. A named `'a` resolves to the
// variable that name denotes.
func (c *checker) resolveLifetimeAnn(node ast.LifetimeAnnNode, lvl int) soltype.Lifetime {
	switch n := node.(type) {
	case *ast.LifetimeAnn:
		return c.namedLifetime(n.Name, lvl)
	default:
		// A nil node, or any unexpected form, is an inferred borrow with a fresh lifetime.
		return c.ctx.freshLifetime(lvl)
	}
}

// lowerLifetimeParamBounds lowers each declared `'a: 'b` bound ("'a outlives 'b") to
// constrainLt('a, 'b), so a signature's bound solves like one a body infers. Names
// resolve through namedLifetime, so a bound and a borrow writing the same name share a
// lifetime. A 'static right-hand side forces the lifetime to 'static; a 'static
// left-hand side is skipped, since the parser already rejects it as a binder.
func (c *checker) lowerLifetimeParamBounds(params []*ast.LifetimeParam, lvl int) {
	for _, p := range params {
		if len(p.Bounds) == 0 || p.Name == "static" {
			continue
		}
		sub := c.namedLifetime(p.Name, lvl)
		for _, b := range p.Bounds {
			c.ctx.constrainLt(sub, c.boundLifetime(b.Name, lvl))
		}
	}
}

// boundLifetime resolves a lifetime name on the right of an outlives bound. A 'static
// resolves to soltype.Static, the bottom of the outlives lattice. Any other name interns
// through namedLifetime, sharing the variable a borrow writing that name uses.
func (c *checker) boundLifetime(name string, lvl int) soltype.Lifetime {
	if name == "static" {
		return soltype.Static
	}
	return c.namedLifetime(name, lvl)
}

// namedLifetime resolves a written lifetime name to its variable, minting one on first
// appearance so every `&'a` in one function shares a single lifetime. The map is reset
// per function by inferFunc, per function type annotation by resolveFuncTypeAnn, and
// per top-level binding by inferComponent, so the same name in two such scopes denotes
// distinct lifetimes.
func (c *checker) namedLifetime(name string, lvl int) *soltype.LifetimeVar {
	if c.namedLifetimes == nil {
		c.namedLifetimes = map[string]*soltype.LifetimeVar{}
	}
	if lt, ok := c.namedLifetimes[name]; ok {
		return lt
	}
	lt := c.ctx.freshLifetime(lvl)
	c.namedLifetimes[name] = lt
	return lt
}

// annPrim mints a FRESH PrimType for an annotation and records it against the
// annotation node (AnnotationType origin) — the "fresh-atom discipline" (§3.3).
//
// Why fresh, rather than a single shared/interned `number` value? Provenance is
// the reason. The Prov side table is keyed by POINTER IDENTITY
// (soltype.Type -> Origin), so the only way to record "this primitive came from
// THIS annotation node" is for the primitive to be its own pointer, unique to this
// annotation. Three consequences follow:
//
//   - Precise blame. A unique atom per annotation lets `val x: number = "hi"`
//     resolve its `number` operand back to the exact annotation node — surfaced as
//     the related "expected here" span — and lets a prim/prim mismatch blame the
//     offending annotation instead of degrading to the constraint site (§3.3, §3.7).
//   - No Prov-invariant conflict. recordProv requires each type pointer to map to a
//     single node; the debugProv guard panics when a pointer is re-recorded against
//     a DIFFERENT node (prov.go). A shared `number` would be recorded against every
//     `number` annotation's node in turn — a conflicting overwrite. Fresh atoms each
//     write a distinct pointer, so there is never a conflict and no last-write-wins
//     blame.
//   - Free, because correctness ignores identity. constrain compares PrimType.Prim
//     BY VALUE (`r.Prim == l.Prim`, constrain.go), never by pointer, so two
//     distinct-but-equal `number`s still subtype-match. Freshness only ever adds a
//     redundant coinductive-`seen` entry, never a loop or a spurious mismatch.
//
// (soltype interns no primitive singletons anyway, so there is nothing to share —
// minting fresh is the natural choice here, not an added cost.)
func (c *checker) annPrim(ta ast.TypeAnn, p soltype.Prim) soltype.Type {
	t := &soltype.PrimType{Prim: p}
	c.recordProv(t, ta, AnnotationType)
	return t
}
