package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// resolveTypeAnn converts an M2-supported type annotation into a soltype.Type,
// returning ok=false when the annotation is outside the supported set. M2 needs
// only the primitive annotations that annotated params and return types use
// (number/string/boolean); everything richer — type references, generics,
// object/tuple/function annotations, unions — is represented by types later
// milestones add (M3/M4/M6) and resolves to an UnsupportedNodeError here, with
// ok=false and a `never` placeholder so a caller can recover by keeping the type
// it already inferred (rather than constraining against / adopting `never`, which
// would cascade a spurious `<: never` error and poison the binding). It takes the
// current inference level `lvl` so a supported generic with an UNSUPPORTED inner (a
// malformed `Promise<…>`) can recover its inner to a fresh var at the right level
// while keeping the wrapper; the primitive arms ignore lvl. Full name resolution
// against the type scope still arrives with TypeRef support (M7).
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
// Promise<bad> and object/tuple cascade-safe recovery. The Inexact flag and
// its parser surface land in PR4.
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
	t := newUnion(c.ctx, members, false)
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

// borrowInner resolves the pointee of a `mut` or `&` annotation to a RefInner, the
// shared inner-resolution step of resolveMutableTypeAnn and resolveRefTypeAnn. An
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
	ri, ok := c.borrowInner(ta.Inner, lvl)
	if !ok {
		return c.reportUnsupportedFeature(ta, "borrow of a non-borrowable type"), false
	}
	// The lazy deep-mut form (PR 14) stores `&mut {a: {x}}` verbatim; the deep-mut
	// rule is applied at access and constrain time rather than by rewriting the
	// pointee's children here.
	t := &soltype.RefType{Mut: ta.Mut, Lt: c.resolveLifetimeAnn(ta.Lifetime, lvl), Inner: ri}
	c.recordProv(t, ta, AnnotationType)
	return t, true
}

// resolveLifetimeAnn resolves the lifetime slot of a borrow annotation. A nil node is
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

// namedLifetime resolves a written lifetime name to its variable, minting one on first
// appearance so every `&'a` in one function shares a single lifetime. The map is reset
// per function by inferFunc and per top-level binding by inferComponent, so the same
// name in two such scopes denotes distinct lifetimes.
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
