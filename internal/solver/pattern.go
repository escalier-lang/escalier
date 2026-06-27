package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// bindPattern types an ast.Pat against a scrutinee type, binding every leaf
// identifier the pattern introduces into scope and returning the soltype.Pat
// mirror used to render a destructured parameter (M4 E1). It is the
// structural-pattern path shared by `val`/`var` destructuring and function-param
// destructuring. E2's `match` arms reuse it too.
//
// A pattern dispatches through the member-lookup constraint path, not subtyping.
// An ObjectPat `{x, y}` against a scrutinee `s` emits `s <: {x: βx, ...}` and
// `s <: {y: βy, ...}`, then binds x/y to βx/βy. Each requirement is the inexact,
// one-property requirement inferMember mints for a field read, so a pattern may
// bind a SUBSET of the scrutinee's fields. A TuplePat `[a, b]` emits
// `s <: [αa, αb]`, an exact tuple whose wrong arity is rejected. A trailing
// `...rest` relaxes that to an inexact prefix requirement. Only a field the
// scrutinee lacks, or a wrong tuple arity, is rejected, surfacing
// MissingPropertyError or TupleLengthMismatchError. The scrutinee's borrow
// wrapper is peeled first via CarrierOf, so a destructured borrow binds the
// borrowed contents, just as a member read does.
//
// leafTypes, when non-nil, receives each leaf binding's type keyed by name. The
// function-param path passes its paramTypes map so the liveness pre-pass can seed
// each leaf's alias mutability. Other callers pass nil.
//
// bindPattern places each leaf as a monomorphic binding in scope, the body-level
// and function-param strategy. The top-level driver needs the leaves constrained
// into pre-bound binding vars instead, so it calls bindPatternWith with its own
// emit (M4 E3).
func (c *checker) bindPattern(scope *Scope, lvl int, pat ast.Pat, scrutinee soltype.Type, leafTypes map[string]soltype.Type) soltype.Pat {
	return c.bindPatternWith(scope, lvl, pat, scrutinee, leafTypes, defineLeafMono)
}

// leafEmit places one bound leaf: it receives the leaf's name, its projected type,
// and its pattern node. defineLeafMono defines a fresh monomorphic binding in scope
// for the body-level and function-param paths. The top-level driver passes an emit
// that constrains the leaf's type into a pre-bound binding var instead (M4 E3).
type leafEmit func(scope *Scope, name string, t soltype.Type, node ast.Node)

// defineLeafMono is the default leaf-placement strategy: it defines the leaf as a
// monomorphic projection of the scrutinee. Used by every body-level and
// function-param destructuring path.
func defineLeafMono(scope *Scope, name string, t soltype.Type, _ ast.Node) {
	scope.defineValue(name, ValueBinding{Schemes: []TypeScheme{monoScheme(t)}})
}

// bindMode records how a pattern's leaves bind to the scrutinee. It is derived from
// the scrutinee's outermost borrow and propagated unchanged into nested sub-patterns,
// following Rust's match ergonomics. An owned scrutinee moves each leaf out. A
// borrowed scrutinee projects a receiver-bounded borrow of each leaf and never moves.
// lt is the scrutinee's borrow lifetime. Every projected leaf borrow shares it, so a
// leaf cannot outlive the scrutinee.
type bindMode struct {
	borrow borrowMode
	lt     soltype.Lifetime
}

type borrowMode byte

const (
	// bmOwned marks an owned scrutinee. Each leaf is moved out and takes its declared
	// mutability. A plain leaf is owned-immutable. A `mut` leaf is owned-mutable.
	bmOwned borrowMode = iota
	// bmShared marks an immutable `&` borrow scrutinee. Each leaf is a shared borrow
	// bounded by the scrutinee's lifetime. A `mut` leaf is an error. Mutable access
	// cannot be obtained through an immutable borrow.
	bmShared
	// bmMut marks a `&mut` borrow scrutinee. Each leaf is a mutable borrow bounded by
	// the scrutinee's lifetime, following Rust's match ergonomics. A `mut` marker is
	// redundant here.
	bmMut
)

// bindModeOf derives the binding mode from the scrutinee's outermost borrow. Only a
// borrow with a real lifetime is a reference. An owned-mutable cell has a nil lifetime
// and is an owned value. Its leaves move out and take their own declared mutability
// rather than projecting a borrow.
func bindModeOf(scrutinee soltype.Type) bindMode {
	if r, ok := scrutinee.(*soltype.RefType); ok && r.Lt != nil {
		if r.Mut {
			return bindMode{borrow: bmMut, lt: r.Lt}
		}
		return bindMode{borrow: bmShared, lt: r.Lt}
	}
	return bindMode{borrow: bmOwned}
}

// bindPatternWith is bindPattern parameterized by the leaf-placement strategy. See
// bindPattern for the pattern-typing contract. The emit decides where each bound
// leaf lands. The binding mode is derived from the scrutinee here and threaded into
// the recursive walk so nested leaves inherit the scrutinee's borrow.
func (c *checker) bindPatternWith(scope *Scope, lvl int, pat ast.Pat, scrutinee soltype.Type, leafTypes map[string]soltype.Type, emit leafEmit) soltype.Pat {
	return c.bindPatMode(scope, lvl, pat, scrutinee, soltype.CarrierOf(scrutinee), bindModeOf(scrutinee), leafTypes, emit)
}

// bindPatMode is bindPatternWith's recursive core, carrying the binding mode the
// top-level scrutinee fixed. The mode propagates unchanged into every sub-pattern, so
// a leaf of `&mut [a, [b]]` binds `b` as a `&mut` borrow just as it binds `a`.
//
// concrete is the scrutinee's resolved type for this level when it is statically
// known. It is the tuple element or object field a leaf projects, not the fresh
// projection variable carried in scrutinee. An owned `mut` leaf thaws concrete, so it
// renders as a clean `mut {…}` cell rather than a variable inside the cell. concrete
// is nil when the scrutinee's shape is not statically known. The thaw then falls back
// to the projection variable.
func (c *checker) bindPatMode(scope *Scope, lvl int, pat ast.Pat, scrutinee soltype.Type, concrete soltype.Type, mode bindMode, leafTypes map[string]soltype.Type, emit leafEmit) soltype.Pat {
	scrutinee = soltype.CarrierOf(scrutinee)
	switch p := pat.(type) {
	case *ast.IdentPat:
		t := c.applyLeafExtras(scope, lvl, p, scrutinee, p.TypeAnn, p.Default)
		t = c.applyBindMode(lvl, p, p.Mutable, t, c.concreteLeaf(concrete, p.TypeAnn), mode)
		c.bindLeaf(scope, p.Name, t, p, leafTypes, emit)
		return &soltype.IdentPat{Name: p.Name}

	case *ast.WildcardPat:
		c.recordType(p, scrutinee)
		return &soltype.WildcardPat{}

	case *ast.LitPat:
		lt, ok := c.litTypeOf(p.Lit)
		if !ok {
			c.reportUnsupported(p.Lit)
			return &soltype.WildcardPat{}
		}
		// A literal pattern asserts the literal is an admissible value of the
		// scrutinee, so the literal flows INTO the scrutinee. `5 <: number` checks.
		// The check is exact against a concrete scrutinee such as a top-level `match`
		// arm. For a NESTED slot the scrutinee here is the field's covariant result
		// var, which carries no upper bound. So a kind mismatch like `{x: "hi"}`
		// against `{x: number}` is not yet rejected. The refutable literal-pattern
		// check lands with E2's `match`, which this path is laid out to extend. A
		// literal pattern binds nothing.
		c.constrain(p, lt, scrutinee)
		c.recordType(p, lt)
		return &soltype.LitPat{Lit: lt.Lit}

	case *ast.TuplePat:
		// A trailing `...rest` element makes the pattern match any tuple at least as
		// long as the fixed prefix, so the requirement becomes an INEXACT tuple over
		// the fixed elements. Without a rest the requirement stays exact and a wrong
		// arity is a TupleLengthMismatchError. The rest element itself needs typed
		// rest tuples, which arrive in M9, so it is reported unsupported and binds
		// nothing.
		fixed := make([]ast.Pat, 0, len(p.Elems))
		inexact := false
		for _, e := range p.Elems {
			if _, isRest := e.(*ast.RestPat); isRest {
				inexact = true
				c.reportUnsupported(e)
				continue
			}
			fixed = append(fixed, e)
		}
		elemTypes := make([]soltype.Type, len(fixed))
		for i := range fixed {
			elemTypes[i] = c.freshAt(lvl)
		}
		// Each αi lowers from the scrutinee's matching element, so a sub-pattern binds
		// at that element's type.
		c.constrain(p, scrutinee, &soltype.TupleType{Elems: elemTypes, Inexact: inexact})
		// When the scrutinee is a concrete tuple, pin each αi's upper bound to the
		// matching element. The constraint above gives αi the element only as a lower
		// bound, which cannot reject a refutable literal sub-pattern of the wrong kind.
		// The upper bound makes a nested literal flow against the real element type, so
		// `[a, "hi"]` against `[number, number]` reports the mismatch.
		tup, _ := scrutinee.(*soltype.TupleType)
		if tup != nil {
			for i := range fixed {
				if i < len(tup.Elems) {
					c.constrain(fixed[i], elemTypes[i], tup.Elems[i])
				}
			}
		}
		// Child concrete types come from the threaded concrete tuple, not from the
		// scrutinee: at a nested level the scrutinee is the parent's element variable,
		// so only the threaded concrete still carries the element shape a borrowed leaf
		// must inspect to decide whether to borrow.
		concreteTup, _ := concrete.(*soltype.TupleType)
		subs := make([]soltype.Pat, len(fixed))
		for i, e := range fixed {
			var elemConcrete soltype.Type
			if concreteTup != nil && i < len(concreteTup.Elems) {
				elemConcrete = concreteTup.Elems[i]
			}
			subs[i] = c.bindPatMode(scope, lvl, e, elemTypes[i], elemConcrete, mode, leafTypes, emit)
		}
		c.recordType(p, scrutinee)
		return &soltype.TuplePat{Elems: subs}

	case *ast.ObjectPat:
		fields := make([]*soltype.ObjectPatField, 0, len(p.Elems))
		for _, elem := range p.Elems {
			switch e := elem.(type) {
			case *ast.ObjShorthandPat:
				// A default makes the field optional. `{x = 0}` binds even when x is
				// absent, so the requirement must not demand it.
				beta := c.freshAt(lvl)
				c.constrain(e, scrutinee, propReq(e.Key.Name, beta, e.Default != nil))
				t := c.applyLeafExtras(scope, lvl, e, beta, e.TypeAnn, e.Default)
				t = c.applyBindMode(lvl, e, e.Mutable, t, c.concreteLeaf(fieldConcrete(concrete, e.Key.Name), e.TypeAnn), mode)
				c.bindLeaf(scope, e.Key.Name, t, e, leafTypes, emit)
				fields = append(fields, &soltype.ObjectPatField{
					Name:  e.Key.Name,
					Value: &soltype.IdentPat{Name: e.Key.Name},
				})
			case *ast.ObjKeyValuePat:
				// A default on the value sub-pattern, as in `{x: a = 0}`, likewise makes
				// the field optional.
				beta := c.freshAt(lvl)
				c.constrain(e, scrutinee, propReq(e.Key.Name, beta, patternDefaultsField(e.Value)))
				// When the scrutinee is a concrete object, pin beta's upper bound to the
				// field type. propReq gives beta the field only as a lower bound, which
				// cannot reject a refutable literal sub-pattern of the wrong kind. The
				// upper bound makes a nested literal flow against the real field type, so
				// `{x: "hi"}` against `{x: number}` reports the mismatch.
				if o, ok := scrutinee.(*soltype.ObjectType); ok {
					if prop, found := o.Prop(e.Key.Name); found {
						c.constrain(e, beta, prop.Type)
					}
				}
				sub := c.bindPatMode(scope, lvl, e.Value, beta, fieldConcrete(concrete, e.Key.Name), mode, leafTypes, emit)
				fields = append(fields, &soltype.ObjectPatField{Name: e.Key.Name, Value: sub})
			default:
				// ObjRestPat (`{...rest}`) needs object rest types, which arrive in M9.
				c.reportUnsupported(elem)
			}
		}
		c.recordType(p, scrutinee)
		return &soltype.ObjectPat{Fields: fields}

	default:
		// ExtractorPat and InstancePat are the constructor patterns; they are M5. A
		// bare RestPat is only meaningful inside a tuple or object. Report and bind
		// nothing.
		c.reportUnsupported(pat)
		return &soltype.WildcardPat{}
	}
}

// applyLeafExtras resolves a destructured leaf's optional type annotation
// (`{x :: T}`, `[a :: T]`) and default value (`{x = d}`, `[a = d]`) against its
// slot type, returning the type to bind. An annotation constrains the slot to
// satisfy it and is then adopted as the leaf's type, mirroring how an annotated
// `val` adopts its annotation. A default is required to satisfy that bound type
// and flows into it, so a leaf bound from an absent-but-defaulted field reads the
// default's type rather than `never`.
func (c *checker) applyLeafExtras(scope *Scope, lvl int, node ast.Node, slot soltype.Type, typeAnn ast.TypeAnn, def ast.Expr) soltype.Type {
	bound := slot
	if typeAnn != nil {
		if annT, ok := c.resolveTypeAnn(typeAnn, lvl); ok {
			c.constrain(node, slot, annT)
			bound = annT
		}
	}
	if def != nil {
		defT := c.inferExpr(scope, lvl, def)
		c.constrain(def, defT, bound)
	}
	return bound
}

// applyBindMode wraps a destructured leaf's slot type according to the scrutinee's
// binding mode and the leaf's own `mut` marker. It returns the type the leaf binds at.
//
//   - Owned scrutinee: the leaf is moved out. A `mut` leaf thaws into an owned-mutable
//     cell, so a later write through it succeeds. A plain leaf keeps the slot's
//     immutable type.
//   - `&` scrutinee: the leaf is a shared borrow bounded by the scrutinee's lifetime.
//     A `mut` leaf is rejected and recovers as the shared borrow. Mutable access cannot
//     be projected out of an immutable borrow.
//   - `&mut` scrutinee: the leaf is a mutable borrow bounded by the scrutinee's
//     lifetime. The `mut` marker is redundant.
//
// The borrow wrap is gated on the concrete element being borrowable, mirroring
// fieldReadBorrow. A primitive or function element is copied, not borrowed, so it is
// returned unchanged. A leaf whose element shape is not statically known has a nil
// concrete and is also returned unchanged. This is the same conservative choice
// fieldReadBorrow makes for an unknown receiver.
func (c *checker) applyBindMode(lvl int, node ast.Node, mut bool, slot, concrete soltype.Type, mode bindMode) soltype.Type {
	switch mode.borrow {
	case bmShared:
		if mut {
			c.report(&MutLeafThroughSharedBorrowError{Node: node})
		}
		if ri, ok := slot.(soltype.RefInner); ok && soltype.BorrowableType(concrete) {
			return soltype.NewRef(false, mode.lt, ri)
		}
		return slot
	case bmMut:
		if _, ok := slot.(soltype.RefInner); ok && soltype.BorrowableType(concrete) {
			// Route the projection through a fresh variable rather than wrapping the
			// slot directly. A tuple or object pattern pins each leaf's slot to the
			// scrutinee's concrete element as an upper bound. That makes the slot
			// invariantly exact under the `&mut` wrapper, so an inexact write
			// requirement `mut {y, ...}` would clash with the exact element. The fresh
			// variable takes the slot only as a lower bound. Its shape stays free to
			// absorb the write requirement.
			v := c.freshAt(lvl)
			c.constrain(node, slot, v)
			return soltype.NewRef(true, mode.lt, v)
		}
		return slot
	default: // bmOwned
		if mut {
			return c.thawOwnedLeaf(lvl, node, slot, concrete)
		}
		return slot
	}
}

// thawOwnedLeaf turns a `mut` leaf moved out of an owned scrutinee into an
// owned-mutable cell. It is the destructuring analogue of the `val mut q = p` thaw in
// inferVarDeclInit. When the leaf's projected type is statically known the cell wraps
// the widened concrete type directly. The common case is a concrete tuple or object
// scrutinee. The cell then renders as a clean `mut {y: number}`, and a later write
// checks against the concrete shape, exactly as the IdentPat thaw does.
//
// When the projected type is not statically known, concrete is nil. The thaw then
// routes the projection variable through a fresh widenable variable. The slot flows in
// as a lower bound, and widening at coalesce time turns a literal field into its
// primitive. The cell carries a variable rather than a concrete object. That is less
// precise to render but still admits the write.
func (c *checker) thawOwnedLeaf(lvl int, node ast.Node, slot, concrete soltype.Type) soltype.Type {
	if concrete != nil {
		widened := widen(stripOwnedMut(concrete))
		inner, ok := widened.(soltype.RefInner)
		if !ok {
			// A primitive or function leaf is not borrowable, so `mut` is a no-op. It
			// keeps its slot type, mirroring `val mut a = 1` keeping the primitive. Only
			// an object or tuple leaf thaws into a mutable cell.
			return slot
		}
		ref := soltype.NewRef(true, nil, inner)
		c.recordProv(ref, node, OwnedMutConstruction)
		return ref
	}
	v := c.freshAt(lvl)
	v.Widenable = true
	c.constrain(node, slot, v)
	ref := soltype.NewRef(true, nil, v)
	c.recordProv(ref, node, OwnedMutConstruction)
	return ref
}

// concreteLeaf resolves the concrete type a leaf binds at. A leaf with its own type
// annotation adopts that annotation rather than the scrutinee's projected type, so the
// scrutinee-derived concrete hint does not apply and is dropped. Otherwise the
// scrutinee-derived concrete type is used, which is non-nil only when the scrutinee's
// shape is statically known. A concrete type that is still an inference variable is
// treated as unknown, since wrapping a variable defeats the clean-rendering the hint
// exists to provide.
func (c *checker) concreteLeaf(concrete soltype.Type, typeAnn ast.TypeAnn) soltype.Type {
	if typeAnn != nil {
		return nil
	}
	if _, isVar := concrete.(*soltype.TypeVarType); isVar {
		return nil
	}
	return concrete
}

// fieldConcrete returns field `name`'s type from a concrete object type, or nil when
// t is not a concrete object or lacks the field. It reads the threaded concrete type,
// so it resolves a field even at a nested level where the scrutinee is a projection
// variable. It is the object-pattern analogue of indexing a concrete tuple's elements.
func fieldConcrete(t soltype.Type, name string) soltype.Type {
	if o, ok := t.(*soltype.ObjectType); ok {
		if prop, found := o.Prop(name); found {
			return prop.Type
		}
	}
	return nil
}

// patternDefaultsField reports whether a destructured field's value sub-pattern
// carries a default (`{x: a = 0}`), which makes the field optional.
func patternDefaultsField(p ast.Pat) bool {
	ip, ok := p.(*ast.IdentPat)
	return ok && ip.Default != nil
}

// bindLeaf places one identifier leaf bound to t via emit and records its type. When
// leafTypes is non-nil it also reports the leaf's type by name for the liveness
// pre-pass. The default emit, defineLeafMono, defines a monomorphic projection of the
// scrutinee in scope. The top-level driver's emit constrains t into a pre-bound
// binding var instead.
func (c *checker) bindLeaf(scope *Scope, name string, t soltype.Type, node ast.Node, leafTypes map[string]soltype.Type, emit leafEmit) {
	emit(scope, name, t, node)
	c.recordType(node, t)
	if leafTypes != nil {
		leafTypes[name] = t
	}
}

// propReq builds the inexact one-property requirement `{name: t, ...}` — "the
// receiver has at least this field" — the same shape inferMember's valueProp
// mints for a field read. optional marks the property `name?: t` so an absent
// field is tolerated, which a destructuring default relies on.
func propReq(name string, t soltype.Type, optional bool) *soltype.ObjectType {
	return &soltype.ObjectType{
		Elems:   []soltype.ObjTypeElem{&soltype.PropertyElem{Name: name, Type: t, Optional: optional}},
		Inexact: true,
	}
}

// litTypeOf lowers an ast literal to its soltype LitType, mirroring inferLiteral.
// ok=false for a literal kind outside the M-subset (the caller reports it).
func (c *checker) litTypeOf(lit ast.Lit) (*soltype.LitType, bool) {
	switch l := lit.(type) {
	case *ast.NumLit:
		return &soltype.LitType{Lit: &soltype.NumLit{Value: l.Value}}, true
	case *ast.StrLit:
		return &soltype.LitType{Lit: &soltype.StrLit{Value: l.Value}}, true
	case *ast.BoolLit:
		return &soltype.LitType{Lit: &soltype.BoolLit{Value: l.Value}}, true
	}
	return nil, false
}
