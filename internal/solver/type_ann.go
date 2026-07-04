package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// resolveTypeAnn converts a supported type annotation into a soltype.Type,
// returning ok=false with a `never` placeholder when the annotation is unsupported
// so a caller can recover by keeping the type it already inferred. The level `lvl`
// lets a supported wrapper with an unsupported inner recover that inner to a fresh
// var at the right level.
func (c *checker) resolveTypeAnn(ta ast.TypeAnn, lvl int) (soltype.Type, bool) {
	switch ta := ta.(type) {
	case *ast.NumberTypeAnn:
		return c.annPrim(ta, soltype.NumPrim), true
	case *ast.StringTypeAnn:
		return c.annPrim(ta, soltype.StrPrim), true
	case *ast.BooleanTypeAnn:
		return c.annPrim(ta, soltype.BoolPrim), true
	case *ast.TypeRefTypeAnn:
		// M3 (PR3) recognises a single generic stdlib reference: Promise<T>. The
		// real, alias-driven TypeRef resolution arrives in M7 — until then, any
		// other name (or arity) reports unsupported with a `never` placeholder so
		// the caller can recover by keeping the inferred type.
		//
		// FOOTGUN (removed in M7): this matches the bare NAME "Promise" WITHOUT
		// consulting the type scope, so it would preempt any user-defined
		// `type Promise<T> = …` alias. That is harmless today (user type aliases
		// don't resolve yet, and the prelude only seeds Promise as an opaque
		// placeholder), but M7's scope-driven TypeRef resolution MUST replace this
		// hardcoded check — resolve the name through the scope first — so a real
		// alias wins instead of being silently shadowed by this stub.
		if ast.QualIdentToString(ta.Name) == "Promise" && len(ta.TypeArgs) == 1 {
			// A lifetime-annotated Promise (`'a Promise<T>` or `Promise<'a, T>`) is not
			// supported: M3's PromiseType carries no lifetime, so silently accepting it
			// would drop the lifetime. Reject it as an unsupported feature rather than
			// coercing to a plain Promise<T>. (Lifetimes on referenced types land with
			// the wider TypeRef/lifetime work.)
			if len(ta.LifetimeArgs) > 0 || ta.Lifetime != nil {
				return c.reportUnsupportedFeature(ta, "lifetime annotation on Promise"), false
			}
			inner, ok := c.resolveTypeAnn(ta.TypeArgs[0], lvl)
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
			t := &soltype.PromiseType{Inner: inner}
			c.recordProv(t, ta, AnnotationType)
			return t, true
		}
		return c.reportUnsupported(ta), false
	case *ast.ObjectTypeAnn:
		return c.resolveObjectTypeAnn(ta, lvl)
	case *ast.TupleTypeAnn:
		return c.resolveTupleTypeAnn(ta, lvl)
	case *ast.MutableTypeAnn:
		return c.resolveMutableTypeAnn(ta, lvl)
	case *ast.RefTypeAnn:
		return c.resolveRefTypeAnn(ta, lvl)
	case *ast.UnionTypeAnn:
		return c.resolveUnionTypeAnn(ta, lvl)
	case *ast.IntersectionTypeAnn:
		return c.resolveIntersectionTypeAnn(ta, lvl)
	case *ast.FuncTypeAnn:
		return c.resolveFuncTypeAnn(ta, lvl)
	case *ast.WildcardTypeAnn:
		// `_` in type-annotation position is an inference placeholder: mint a fresh
		// var at the current level for the surrounding annotation to fill in. Today
		// the only site that uses it is the inner of `Promise<_>` on an async fn's
		// return, where the body's return flows into the var (asyncReturn), inferring
		// the inner. Unlike the other arms it reports NO error — `_` is a supported,
		// user-authored "infer this here" marker, not an unsupported feature.
		t := c.freshAt(lvl)
		c.recordProv(t, ta, WildcardAnnotation)
		return t, true
	default:
		return c.reportUnsupported(ta), false
	}
}

// resolveObjectTypeAnn lowers an object type annotation to a soltype.ObjectType,
// honoring the trailing `...` inexact marker. M4 ships PropertyElem only: a
// `name: T` / `name?: T` property resolves to a PropertyElem; method/getter/setter
// members (M5), mapped/index signatures and the object rest/spread (M9) are not
// part of M4's object and report an unsupported feature, with the object still
// built from the properties that do resolve. Duplicate keys follow the
// last-wins-first-position dedup inferObject uses, keeping property names unique.
//
// A property whose value annotation is itself unsupported recovers that value to a
// fresh var and keeps the object shape — cascade-safe, mirroring the Promise<bad>
// recovery — so the binding still checks structurally. The arm therefore always
// returns ok=true: any unsupported sub-part has already reported its own error.
func (c *checker) resolveObjectTypeAnn(ta *ast.ObjectTypeAnn, lvl int) (soltype.Type, bool) {
	b := newObjElemBuilder(len(ta.Elems))
	unsupported := false
	for _, elem := range ta.Elems {
		prop, ok := elem.(*ast.PropertyTypeAnn)
		if !ok {
			unsupported = true
			continue
		}
		name, ok := objKeyName(prop.Name)
		if !ok {
			c.reportUnsupported(prop.Name)
			continue
		}
		// A missing or unsupported value annotation recovers to a fresh var, keeping
		// the object shape cascade-safe — mirroring the Promise<bad> recovery.
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
			if t, ok := c.resolveTypeAnn(value, lvl); ok {
				ft = t
			}
		}
		b.add(name, ft, prop.Optional, prop.Readonly)
	}
	if unsupported {
		c.reportUnsupportedFeature(ta, "object type member other than a property")
	}
	t := &soltype.ObjectType{Elems: b.elems, Inexact: ta.Inexact}
	c.recordProv(t, ta, AnnotationType)
	return t, true
}

// resolveTupleTypeAnn lowers a tuple type annotation to a soltype.TupleType,
// honoring the trailing `...` inexact marker. A rest-spread / variadic element
// (`[...P]`, `[number, ...Array<number>]`) defers with its type-level feature
// (M9 / M7) and reports unsupported; the bare trailing `...` inexact marker is
// carried on ta.Inexact, not as an element. An element whose annotation is
// unsupported recovers to a fresh var so the tuple keeps its arity.
func (c *checker) resolveTupleTypeAnn(ta *ast.TupleTypeAnn, lvl int) (soltype.Type, bool) {
	elems := make([]soltype.Type, 0, len(ta.Elems))
	unsupported := false
	for _, el := range ta.Elems {
		if _, isRest := el.(*ast.RestSpreadTypeAnn); isRest {
			unsupported = true
			continue
		}
		// An owned-mutable element `[mut {x}]` is rejected (#779), the tuple twin of
		// the object-property rejection above: a `mut` cell nested inside a non-mut
		// container is misleading. Recover to the element's bare inner. A `&`/`&mut`
		// borrow element stays legal — it references external storage.
		if mta, ok := el.(*ast.MutableTypeAnn); ok {
			c.report(&MutFieldError{Ann: mta})
			el = mta.Target
		}
		if t, ok := c.resolveTypeAnn(el, lvl); ok {
			elems = append(elems, t)
		} else {
			elems = append(elems, c.freshAt(lvl))
		}
	}
	if unsupported {
		c.reportUnsupportedFeature(ta, "tuple spread or variadic element")
	}
	t := &soltype.TupleType{Elems: elems, Inexact: ta.Inexact}
	c.recordProv(t, ta, AnnotationType)
	return t, true
}

// resolveUnionTypeAnn lowers `A | B | …` through newUnion. An unsupported
// member recovers to a fresh var so the union shape survives, mirroring the
// Promise<bad> and object/tuple cascade-safe recovery. A trailing `...` in the
// source sets ta.Inexact, which carries onto the resolved union.
func (c *checker) resolveUnionTypeAnn(ta *ast.UnionTypeAnn, lvl int) (soltype.Type, bool) {
	members := make([]soltype.Type, len(ta.Types))
	for i, m := range ta.Types {
		if t, ok := c.resolveTypeAnn(m, lvl); ok {
			members[i] = t
		} else {
			// freshAt over ErrorType: preserves the source's union shape
			// in the rendered type. pruneUnion would drop an ErrorType
			// member and collapse the union.
			members[i] = c.freshAt(lvl)
		}
	}
	t := newUnion(c.ctx, members, ta.Inexact)
	// newUnion can collapse to an input member's pointer (single-member
	// dedup, or subsumption). Re-recording Prov on a pointer that already
	// carries it would overwrite the narrower child-annotation blame and
	// trip the debugProv guard.
	if !c.hasProv(t) {
		c.recordProv(t, ta, AnnotationType)
	}
	return t, true
}

// resolveIntersectionTypeAnn is the meet twin of resolveUnionTypeAnn.
func (c *checker) resolveIntersectionTypeAnn(ta *ast.IntersectionTypeAnn, lvl int) (soltype.Type, bool) {
	members := make([]soltype.Type, len(ta.Types))
	for i, m := range ta.Types {
		if t, ok := c.resolveTypeAnn(m, lvl); ok {
			members[i] = t
		} else {
			members[i] = c.freshAt(lvl) // see resolveUnionTypeAnn
		}
	}
	t := newIntersection(c.ctx, members)
	if !c.hasProv(t) {
		c.recordProv(t, ta, AnnotationType)
	}
	return t, true
}

// resolveFuncTypeAnn lowers a monomorphic function type annotation
// `fn(p: A, ...) -> R` into a soltype.FuncType, mapping ta.Inexact to
// FuncType.Inexact. An unsupported part recovers to a fresh var so the function
// shape survives, cascade-safe like the Promise/object/tuple arms. Generic, throws,
// and rest-param annotations report an unsupported feature and recover as a
// monomorphic function. A lifetime parameter is supported. Its declared outlives
// bounds lower into constrainLt so the annotation's borrows solve against them.
func (c *checker) resolveFuncTypeAnn(ta *ast.FuncTypeAnn, lvl int) (soltype.Type, bool) {
	if len(ta.TypeParams) > 0 {
		c.reportUnsupportedFeature(ta, "generic function type annotation")
	}
	if ta.Throws != nil {
		c.reportUnsupportedFeature(ta, "throws clause in function type annotation")
	}
	// A function type annotation is its own quantifier scope, so give it its own
	// named-lifetime map the way inferFunc does for a function body. Without this a
	// nested `fn<'a: 'static>(…)` annotation would resolve `'a` to the enclosing
	// function's `'a` and its declared bound would force that outer lifetime, so an
	// unrelated borrow parameter of the enclosing function would be pinned to 'static.
	savedNamedLts := c.namedLifetimes
	c.namedLifetimes = nil
	defer func() { c.namedLifetimes = savedNamedLts }()
	c.lowerLifetimeParamBounds(ta.LifetimeParams, lvl)

	params := make([]*soltype.FuncParam, len(ta.Params))
	for i, p := range ta.Params {
		pat := p.Pattern
		// A rest param recovers to a normal positional param. acceptSet/hasRest assume
		// a rest param is last, which the parser does not enforce, so Rest is unset.
		if rp, ok := pat.(*ast.RestPat); ok {
			c.reportUnsupportedFeature(rp, "rest parameter in function type annotation")
			pat = rp.Pattern
		}
		// A missing or unsupported parameter annotation recovers to a fresh var so
		// the function keeps its arity and shape, cascade-safe like Promise<bad>.
		var pt soltype.Type = c.freshAt(lvl)
		if p.TypeAnn != nil {
			if t, ok := c.resolveTypeAnn(p.TypeAnn, lvl); ok {
				pt = t
			}
		}
		// The pattern is carried for rendering and round-tripping only, with no scope
		// binding. mirrorParamPat preserves its full shape.
		params[i] = &soltype.FuncParam{Pattern: c.mirrorParamPat(pat), Type: pt, Optional: p.Optional}
	}

	// The parser requires `-> R`, so ta.Return is normally non-nil. Guard
	// defensively and recover an unsupported or absent return to a fresh var,
	// keeping the function shape.
	var ret soltype.Type = c.freshAt(lvl)
	if ta.Return != nil {
		if t, ok := c.resolveTypeAnn(ta.Return, lvl); ok {
			ret = t
		}
	}

	t := &soltype.FuncType{Params: params, Ret: ret, Inexact: ta.Inexact}
	c.recordProv(t, ta, AnnotationType)
	return t, true
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
		if lt, ok := c.litTypeOf(p.Lit); ok {
			return &soltype.LitPat{Lit: lt.Lit}
		}
		return nil
	case *ast.TuplePat:
		elems := make([]soltype.Pat, 0, len(p.Elems))
		for _, e := range p.Elems {
			// soltype.TuplePat has no rest element, so a `...rest` is dropped.
			if _, isRest := e.(*ast.RestPat); isRest {
				continue
			}
			elems = append(elems, c.mirrorParamPat(e))
		}
		return &soltype.TuplePat{Elems: elems}
	case *ast.ObjectPat:
		fields := make([]*soltype.ObjectPatField, 0, len(p.Elems))
		for _, elem := range p.Elems {
			switch e := elem.(type) {
			case *ast.ObjShorthandPat:
				// A bare `{x}` mirrors to a field whose value is the IdentPat `x`.
				fields = append(fields, &soltype.ObjectPatField{Name: e.Key.Name, Value: &soltype.IdentPat{Name: e.Key.Name}})
			case *ast.ObjKeyValuePat:
				fields = append(fields, &soltype.ObjectPatField{Name: e.Key.Name, Value: c.mirrorParamPat(e.Value)})
			}
			// ObjRestPat has no soltype counterpart and is dropped.
		}
		return &soltype.ObjectPat{Fields: fields}
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
func (c *checker) resolveMutableTypeAnn(ta *ast.MutableTypeAnn, lvl int) (soltype.Type, bool) {
	ri, ok := c.borrowInner(ta.Target, lvl)
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
func (c *checker) borrowInner(ta ast.TypeAnn, lvl int) (soltype.RefInner, bool) {
	inner, ok := c.resolveTypeAnn(ta, lvl)
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
func (c *checker) resolveRefTypeAnn(ta *ast.RefTypeAnn, lvl int) (soltype.Type, bool) {
	lt := c.resolveLifetimeAnn(ta.Lifetime, lvl)
	inner, ok := c.resolveTypeAnn(ta.Inner, lvl)
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
// an inferred borrow and mints a fresh lifetime. A named `'a` resolves to the variable
// that name denotes. A `('a | 'b)` union resolves each member and joins them in a
// LifetimeUnion.
func (c *checker) resolveLifetimeAnn(node ast.LifetimeAnnNode, lvl int) soltype.Lifetime {
	switch n := node.(type) {
	case *ast.LifetimeAnn:
		return c.namedLifetime(n.Name, lvl)
	case *ast.LifetimeUnionAnn:
		members := make([]soltype.Lifetime, len(n.Lifetimes))
		for i, m := range n.Lifetimes {
			members[i] = c.namedLifetime(m.Name, lvl)
		}
		return &soltype.LifetimeUnion{Lifetimes: members}
	default:
		// A nil node, or any unexpected form, is an inferred borrow with a fresh lifetime.
		return c.ctx.freshLifetime(lvl)
	}
}

// lowerLifetimeParamBounds asserts each declared outlives bound in a `<…>` quantifier
// list as a constrainLt, so a bound written in a signature participates in solving like
// one a body infers. A binder `'a: 'b` reads "'a outlives 'b", which is 'a <: 'b in the
// outlives lattice, so it lowers to constrainLt('a, 'b). Each name resolves through
// namedLifetime, which interns one LifetimeVar per written name in the current
// signature, so a bound and a borrow that write the same name share a lifetime. A
// 'static on the right resolves to soltype.Static, the bottom of the outlives lattice.
// That forces the bound lifetime to 'static, the same escape-to-static constraint an
// escaping borrow emits.
//
// A 'static on the left is not a bindable parameter, which the parser already rejects.
// The binder is still built with that name, so skip it here rather than interning a
// bogus "static" variable.
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
