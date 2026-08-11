package solver

import (
	"slices"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
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
	return c.bindPatternWith(scope, lvl, pat, scrutinee, leafTypes, defineLeafMono, forBinding)
}

// leafEmit places one bound leaf: it receives the leaf's name, its projected type,
// and its pattern node. defineLeafMono defines a fresh monomorphic binding in scope
// for the body-level and function-param paths. The top-level driver passes an emit
// that constrains the leaf's type into a pre-bound binding var instead (M4 E3).
type leafEmit func(scope *Scope, name string, t soltype.Type, node ast.Node)

// bindPurpose says what one walk over a pattern is for.
//
// forBinding is the ordinary walk. Each leaf lands at the type the name takes, with its
// annotation, default, and `mut` marker applied, and every node the walk visits records
// that type for an editor to read.
//
// forProjection resolves the same leaf types out of the scrutinee, reports them to the
// emit, and does nothing else. It runs over a pattern a binding walk has already walked, to
// ask what a SECOND value supplies for each name the pattern binds. Three things follow
// from that:
//
//   - No leaf extra is applied. The annotation, default, and `mut` marker shape what the
//     name binds at, which the binding walk settled. Re-applying them would infer a
//     default's expression a second time.
//   - No type is recorded. The node's type is what the name binds at, not what this second
//     value supplies for it.
//   - Nothing is reported. Every fault in the pattern is one the binding walk already
//     reported, so reporting again would double each message.
type bindPurpose byte

const (
	forBinding bindPurpose = iota
	forProjection
)

// leafMut reports whether a leaf's `mut` marker applies at this walk. A projection walk
// answers false however the leaf was written, for two reasons. The marker's only effect on
// an owned scrutinee is to thaw the leaf into a cell, which is a binding's business and not
// a projection's. Against a borrowed one it reports MutLeafThroughSharedBorrowError, which
// the binding walk over the same pattern already reported.
func leafMut(marked bool, purpose bindPurpose) bool {
	return marked && purpose == forBinding
}

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
	// bmImm marks an immutable `&` borrow scrutinee. Each leaf is a shared borrow
	// bounded by the scrutinee's lifetime. A `mut` leaf is an error. Mutable access
	// cannot be obtained through an immutable borrow.
	bmImm
	// bmMut marks a `&mut` borrow scrutinee. Each leaf is a mutable borrow bounded by
	// the scrutinee's lifetime, following Rust's match ergonomics. A `mut` marker is
	// redundant here.
	bmMut
)

// bindModeOf derives the binding mode from the scrutinee's outermost borrow. Only a
// borrow with a real lifetime is a reference. An owned-mutable cell has a nil lifetime
// and is an owned value. Its leaves move out and take their own declared mutability
// rather than projecting a borrow.
//
// A union whose members are all borrows carries no outermost borrow to read, so it takes
// peelBorrowUnion instead. Callers reach both through scrutineeBinding.
func bindModeOf(scrutinee soltype.Type) bindMode {
	if r, ok := scrutinee.(*soltype.RefType); ok && r.Lt != nil {
		if r.Mut {
			return bindMode{borrow: bmMut, lt: r.Lt}
		}
		return bindMode{borrow: bmImm, lt: r.Lt}
	}
	return bindMode{borrow: bmOwned}
}

// scrutineeBinding splits a scrutinee into the two things a pattern walk needs from it:
// the carrier its leaves project out of, and the mode they bind through. The carrier
// never holds the borrow, since the mode records it instead.
//
// A single `&{…}` is peeled by CarrierOf and read by bindModeOf. A union of borrows has
// no outermost borrow for either to see, so peelBorrowUnion peels it per member. Every
// other scrutinee keeps CarrierOf's answer and binds owned.
func (c *checker) scrutineeBinding(lvl int, scrutinee soltype.Type) (soltype.Type, bindMode) {
	carrier := soltype.CarrierOf(scrutinee)
	if inner, mode, ok := c.peelBorrowUnion(lvl, carrier); ok {
		return inner, mode
	}
	return carrier, bindModeOf(scrutinee)
}

// peelBorrowUnion peels a union whose every member is a borrow into the union of the
// members' carriers, plus the mode a leaf of that union binds through. So
// `&'a {x: number} | &'b {y: string}` peels to `{x: number} | {y: string}`, and a leaf
// projects out of an owned member while the borrow rides the mode.
//
// ok is false for every other type, including a union holding one non-borrow member, an
// owned-mutable `mut {…}` cell, and an INEXACT union whose unlisted tail may be owned.
//
// The mode is mutable only when every member is, since a leaf reached through an immutable
// member cannot be written. Its lifetime is the members' join.
//
// TODO(#1087): narrow the mode along with the members. It is fixed here, at the whole
// scrutinee, before any branch's tag test drops members from it, so a branch of
// `&mut {x: …} | &{y: …}` testing for `x` binds immutable leaves and rejects a legal write.
func (c *checker) peelBorrowUnion(lvl int, t soltype.Type) (soltype.Type, bindMode, bool) {
	u, isUnion := t.(*soltype.UnionType)
	if !isUnion || u.Inexact || len(u.Types) == 0 {
		return nil, bindMode{}, false
	}
	inners := make([]soltype.Type, len(u.Types))
	lts := make([]soltype.Lifetime, len(u.Types))
	mut := true
	for i, member := range u.Types {
		r, isRef := member.(*soltype.RefType)
		if !isRef || r.Lt == nil {
			return nil, bindMode{}, false
		}
		inners[i], lts[i] = r.Inner, r.Lt
		mut = mut && r.Mut
	}
	borrow := bmImm
	if mut {
		borrow = bmMut
	}
	// The peel goes through the lattice's one union constructor, so a member that is itself
	// a union is flattened into the result. Building the node by hand would nest it, and
	// narrowing reads only a union's top-level members, so `&({a} | {b}) | &{c}` would
	// destructure as though `{a}` and `{b}` were not there.
	//
	// No Context, so subsumption does not run. It trials one member against another under a
	// probe and drops the subtype, which would change the member set narrowing reads.
	peeled := newUnion(nil, inners, false)
	return peeled, bindMode{borrow: borrow, lt: c.joinLifetimes(lvl, lts)}, true
}

// joinLifetimes returns one lifetime that every member of lts outlives, so a value drawn
// from any of them may carry it. Several distinct lifetimes unite under a fresh join
// variable holding each of them as a lower bound. lts must not be empty.
//
// Where lts names one lifetime already, that lifetime is returned unchanged and the common
// `&'a A | &'a B` mints no join variable.
func (c *checker) joinLifetimes(lvl int, lts []soltype.Lifetime) soltype.Lifetime {
	shared := true
	for _, lt := range lts[1:] {
		if !soltype.ContainsLifetime(lts[:1], lt) {
			shared = false
			break
		}
	}
	if shared {
		return lts[0]
	}
	joinLt := c.ctx.freshJoinLifetime(lvl)
	for _, lt := range lts {
		c.ctx.constrainLt(lt, joinLt)
	}
	return joinLt
}

// bindPatternWith is bindPattern parameterized by the leaf-placement strategy. See
// bindPattern for the pattern-typing contract. The emit decides where each bound
// leaf lands. The binding mode is derived from the scrutinee here and threaded into
// the recursive walk so nested leaves inherit the scrutinee's borrow.
func (c *checker) bindPatternWith(scope *Scope, lvl int, pat ast.Pat, scrutinee soltype.Type, leafTypes map[string]soltype.Type, emit leafEmit, purpose bindPurpose) soltype.Pat {
	carrier, mode := c.scrutineeBinding(lvl, scrutinee)
	return c.bindPatMode(scope, lvl, pat, carrier, carrier, mode, leafTypes, emit, purpose)
}

// projectLeaves resolves each leaf pat names out of scrutinee and reports it to found. It
// binds nothing, records nothing, and reports nothing. See bindPurpose. A `val … else` reads
// it to find what the `else`'s fallback value supplies for each name the declaration binds.
//
// concrete is the scrutinee's statically known shape. A caller whose value has not yet
// flowed into scrutinee passes that value here, so a `...rest` leaf resolves the leftover
// members it really has rather than an opaque `{...}`.
func (c *checker) projectLeaves(scope *Scope, lvl int, pat ast.Pat, scrutinee, concrete soltype.Type, found leafEmit) {
	shape, mode := c.scrutineeBinding(lvl, concrete)
	c.bindPatMode(scope, lvl, pat, scrutinee, shape, mode, nil, found, forProjection)
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
func (c *checker) bindPatMode(scope *Scope, lvl int, pat ast.Pat, scrutinee soltype.Type, concrete soltype.Type, scrutineeMode bindMode, leafTypes map[string]soltype.Type, emit leafEmit, purpose bindPurpose) soltype.Pat {
	scrutinee = soltype.CarrierOf(scrutinee)
	switch p := pat.(type) {
	case *ast.IdentPat:
		t := scrutinee
		if purpose == forBinding {
			t = c.applyLeafExtras(scope, lvl, p, scrutinee, p.TypeAnn, p.Default)
		}
		t = c.applyBindMode(lvl, p, leafMut(p.Mutable, purpose), t, c.concreteLeaf(concrete, p.TypeAnn), scrutineeMode)
		c.bindLeaf(scope, p.Name, t, p, leafTypes, emit, purpose)
		return &soltype.IdentPat{Name: p.Name}

	case *ast.WildcardPat:
		c.recordPatType(purpose, p, scrutinee)
		return &soltype.WildcardPat{}

	case *ast.LitPat:
		if atom, atomPat, isAtom := atomLitOf(p.Lit); isAtom {
			// A `null` or `undefined` arm asserts the atom is an admissible value of the
			// scrutinee, the same direction as the literal case below, and binds nothing.
			c.constrain(p, atom, scrutinee)
			c.recordPatType(purpose, p, atom)
			return atomPat
		}
		lt, ok := c.litTypeOf(p.Lit)
		if !ok {
			c.reportPatUnsupported(purpose, p.Lit)
			return &soltype.WildcardPat{}
		}
		// A literal pattern asserts the literal is an admissible value of the
		// scrutinee, so the literal flows INTO the scrutinee. `5 <: number` checks.
		// The check is exact against a concrete scrutinee such as a top-level `match`
		// arm. For a NESTED field the scrutinee here is the field's covariant result
		// var, which carries no upper bound. So a kind mismatch like `{x: "hi"}`
		// against `{x: number}` is not yet rejected. The refutable literal-pattern
		// check lands with E2's `match`, which this path is laid out to extend. A
		// literal pattern binds nothing.
		c.constrain(p, lt, scrutinee)
		c.recordPatType(purpose, p, lt)
		return &soltype.LitPat{Lit: lt.Lit}

	case *ast.TuplePat:
		// A trailing `...rest` element makes the pattern match any tuple at least as
		// long as the fixed prefix, so the requirement becomes an INEXACT tuple over
		// the fixed elements. Without a rest the requirement stays exact and a wrong
		// arity is a TupleLengthMismatchError. Only a trailing rest has a suffix to
		// bind. splitTupleRest returns any other one as recovered.
		fixed, rest, recovered := splitTupleRest(p.Elems)
		if len(recovered) > 0 {
			c.reportPatUnsupported(purpose, recovered[0])
		}
		inexact := rest != nil || len(recovered) > 0
		// Each element sub-pattern blames its own node for the upper-bound pin, so
		// `[a, "hi"]` against `[number, number]` underlines the literal rather than the
		// whole pattern.
		elemTypes, scrutTup, concreteTup := c.projectTuple(p, lvl, scrutinee, concrete, len(fixed), inexact, func(i int) ast.Node { return fixed[i] })
		subs := make([]soltype.Pat, 0, len(p.Elems))
		for i, e := range fixed {
			var elemConcrete soltype.Type
			if concreteTup != nil && i < len(concreteTup.Elems) {
				elemConcrete = concreteTup.Elems[i]
			}
			subs = append(subs, c.bindPatMode(scope, lvl, e, elemTypes[i], elemConcrete, scrutineeMode, leafTypes, emit, purpose))
		}
		if rest != nil {
			// The rest's sub-pattern binds through the same walk a fixed element does, so
			// it inherits the scrutinee's borrow and `[a, ...[b, c]]` destructures the
			// suffix in turn.
			suffix, suffixConcrete := c.tupleRestType(lvl, rest, scrutinee, scrutTup, concreteTup, len(fixed))
			sub := c.bindPatMode(scope, lvl, rest.Pattern, suffix, suffixConcrete, scrutineeMode, leafTypes, emit, purpose)
			subs = append(subs, &soltype.RestPat{Pattern: sub})
		}
		for _, e := range recovered {
			subs = append(subs, c.bindRecoveredElem(scope, lvl, e, scrutineeMode, leafTypes, emit, purpose))
		}
		c.recordPatType(purpose, p, scrutinee)
		return &soltype.TuplePat{Elems: subs}

	case *ast.ObjectPat:
		fields := make([]*soltype.ObjectPatField, 0, len(p.Elems))
		// A rest takes every property outside named, so it binds after this loop, once
		// the key set is complete.
		named := set.NewSet[string]()
		var rest *ast.ObjRestPat
		var recovered []ast.Pat
		for i, elem := range p.Elems {
			switch e := elem.(type) {
			case *ast.ObjShorthandPat:
				// A default makes the field optional. `{x = 0}` binds even when x is
				// absent, so the requirement must not demand it.
				beta := c.freshAt(lvl)
				c.constrain(e, scrutinee, propReq(e.Key.Name, beta, e.Default != nil))
				var t soltype.Type = beta
				if purpose == forBinding {
					t = c.applyLeafExtras(scope, lvl, e, beta, e.TypeAnn, e.Default)
				}
				t = c.applyBindMode(lvl, e, leafMut(e.Mutable, purpose), t, c.concreteLeaf(fieldConcrete(concrete, e.Key.Name), e.TypeAnn), scrutineeMode)
				c.bindLeaf(scope, e.Key.Name, t, e, leafTypes, emit, purpose)
				named.Add(e.Key.Name)
				fields = append(fields, &soltype.ObjectPatField{
					Name:  e.Key.Name,
					Value: &soltype.IdentPat{Name: e.Key.Name},
				})
			case *ast.ObjKeyValuePat:
				// A default on the value sub-pattern, as in `{x: a = 0}`, likewise makes
				// the field optional.
				beta, betaConcrete := c.projectField(e, lvl, scrutinee, concrete, e.Key.Name, patternDefaultsField(e.Value))
				sub := c.bindPatMode(scope, lvl, e.Value, beta, betaConcrete, scrutineeMode, leafTypes, emit, purpose)
				named.Add(e.Key.Name)
				fields = append(fields, &soltype.ObjectPatField{Name: e.Key.Name, Value: sub})
			case *ast.ObjRestPat:
				// One rest takes every unnamed property, so a second has nothing left. It
				// must also come last, the position JavaScript itself requires.
				if rest == nil && i == len(p.Elems)-1 {
					rest = e
				} else {
					c.reportPatUnsupported(purpose, elem)
					recovered = append(recovered, e.Pattern)
				}
			default:
				c.reportPatUnsupported(purpose, elem)
			}
		}
		var restPat soltype.Pat
		if rest != nil {
			// The rest binds through the same walk a field does, so it inherits the
			// scrutinee's borrow.
			leftover, leftoverConcrete := c.objectRestType(lvl, rest, scrutinee, concrete, named)
			restPat = c.bindPatMode(scope, lvl, rest.Pattern, leftover, leftoverConcrete, scrutineeMode, leafTypes, emit, purpose)
		}
		// A reported rest still binds against a fresh variable, so a later reference to one
		// of its leaves resolves instead of cascading into an unknown-identifier error.
		for _, sub := range recovered {
			c.bindPatMode(scope, lvl, sub, c.freshAt(lvl), nil, scrutineeMode, leafTypes, emit, purpose)
		}
		c.recordPatType(purpose, p, scrutinee)
		return &soltype.ObjectPat{Fields: fields, Rest: restPat}

	case *ast.InstancePat:
		return c.bindInstancePat(scope, lvl, p, scrutinee, concrete, scrutineeMode, leafTypes, emit, purpose)

	case *ast.ExtractorPat:
		return c.bindExtractorPat(scope, lvl, p, scrutinee, scrutineeMode, leafTypes, emit, purpose)

	default:
		// A bare RestPat is only meaningful inside a tuple or object. Report and bind
		// nothing.
		c.reportPatUnsupported(purpose, pat)
		return &soltype.WildcardPat{}
	}
}

// projectField lowers the scrutinee's `name` field into a fresh variable through the
// member-lookup requirement, returning that variable and the concrete field type to thread
// beside it. It is the one field projection, shared by an object pattern's key-value arm
// and the UCS IR's field step, so the two cannot drift.
//
// optional relaxes the requirement to tolerate an absent field, which a destructuring
// default asks for. blame is the node the requirement is anchored to.
//
// An object pattern's SHORTHAND arm does not call this. It binds the projection as the leaf
// itself rather than handing it to a sub-pattern, so it needs neither the concrete field
// type nor the upper bound below, and pinning there would make a `&mut` leaf invariantly
// exact against the scrutinee's element. See applyBindMode's bmMut arm.
func (c *checker) projectField(
	blame ast.Node, lvl int, scrutinee, concrete soltype.Type, name string, optional bool,
) (proj, projConcrete soltype.Type) {
	beta := c.freshAt(lvl)
	c.constrain(blame, scrutinee, propReq(name, beta, optional))
	// When the scrutinee is a concrete object, pin beta's upper bound to the field type.
	// propReq gives beta the field only as a lower bound, which cannot reject a refutable
	// literal sub-pattern of the wrong kind. The upper bound makes a nested literal flow
	// against the real field type, so `{x: "hi"}` against `{x: number}` reports the
	// mismatch.
	//
	// An optional property gets no pin. Reading `x` off `{x?: number}` produces
	// `number | undefined`, so a bound of `number` would reject the `undefined` half of the
	// value the field actually holds.
	if obj, ok := scrutinee.(*soltype.ObjectType); ok {
		if prop, found := obj.Prop(name); found && !prop.Optional {
			c.constrain(blame, beta, prop.Type)
		}
	}
	return beta, fieldConcrete(concrete, name)
}

// projectTuple lowers the scrutinee's first count elements into fresh variables through one
// whole-tuple requirement, returning those variables and the grounded scrutinee and concrete
// tuples a `...rest` reads its suffix out of. It is the one tuple projection, shared by a
// tuple pattern and the UCS IR's tuple test, so the two cannot drift.
//
// inexact relaxes the requirement to "a tuple at least this long", which a trailing rest
// asks for. An exact requirement is what rejects a wrong arity. blame anchors the
// requirement, and blameElem anchors each element's upper bound, so a caller holding
// sub-patterns can underline the offending element rather than the whole shape. Pass nil for
// blameElem to anchor every pin to blame.
func (c *checker) projectTuple(
	blame ast.Node, lvl int, scrutinee, concrete soltype.Type, count int, inexact bool, blameElem func(int) ast.Node,
) (elems []soltype.Type, scrutTup, concreteTup *soltype.TupleType) {
	elems = make([]soltype.Type, count)
	for i := range elems {
		elems[i] = c.freshAt(lvl)
	}
	// Each αi lowers from the scrutinee's matching element, so a sub-pattern binds at that
	// element's type.
	c.constrain(blame, scrutinee, &soltype.TupleType{Elems: elems, Inexact: inexact})
	// Both shapes are spliced once, since every read by the caller is by index. See
	// groundedTuple.
	scrutTup, _ = c.groundedTuple(scrutinee)
	// Child concrete types come from the threaded concrete tuple, not from the scrutinee. At
	// a nested level the scrutinee is the parent's element variable, so only the threaded
	// concrete still carries the element shape a borrowed leaf must inspect to decide
	// whether to borrow.
	concreteTup = c.concreteTupleShape(concrete)
	// When the scrutinee is a concrete tuple, pin each αi's upper bound to the matching
	// element. The requirement above gives αi the element only as a lower bound, which
	// cannot reject a refutable literal sub-pattern of the wrong kind. The upper bound makes
	// a nested literal flow against the real element type, so `[a, "hi"]` against
	// `[number, number]` reports the mismatch.
	if scrutTup != nil {
		for i := range elems {
			if i >= len(scrutTup.Elems) {
				continue
			}
			node := blame
			if blameElem != nil {
				node = blameElem(i)
			}
			c.constrain(node, elems[i], scrutTup.Elems[i])
		}
	}
	return elems, scrutTup, concreteTup
}

// objectPatNamesRest reports whether pat is an object pattern carrying a `...rest`. Only
// an object pattern is asked about: a tuple pattern's rest already reaches the parameter
// type through the inexact requirement its fixed prefix emits, so `fn f([a, ...rest])`
// infers `[unknown, ...]` and accepts a longer tuple with no further marking.
func objectPatNamesRest(pat ast.Pat) bool {
	obj, ok := pat.(*ast.ObjectPat)
	if !ok {
		return false
	}
	for _, elem := range obj.Elems {
		if _, isRest := elem.(*ast.ObjRestPat); isRest {
			return true
		}
	}
	return false
}

// splitTupleRest splits a tuple pattern's elements at its first `...rest`. fixed holds the
// elements before it, which bind positionally. rest is that element when it is the
// pattern's last, the only position with a suffix to bind. Otherwise recovered holds it
// and everything after, none of which sits at a known index: the rest stands for a run of
// elements whose length nothing pins down. A pattern with no rest is all fixed.
func splitTupleRest(elems []ast.Pat) (fixed []ast.Pat, rest *ast.RestPat, recovered []ast.Pat) {
	for i, e := range elems {
		r, isRest := e.(*ast.RestPat)
		if !isRest {
			continue
		}
		if i == len(elems)-1 {
			return elems[:i], r, nil
		}
		return elems[:i], nil, elems[i:]
	}
	return elems, nil, nil
}

// bindRecoveredElem binds a tuple element the walk could not place, reached only after a
// non-trailing `...rest` was reported. A fresh variable keeps its leaves defined so a later
// reference does not cascade. A rest is unwrapped, since bindPatMode rejects a bare one.
func (c *checker) bindRecoveredElem(scope *Scope, lvl int, e ast.Pat, scrutineeMode bindMode, leafTypes map[string]soltype.Type, emit leafEmit, purpose bindPurpose) soltype.Pat {
	if r, isRest := e.(*ast.RestPat); isRest {
		sub := c.bindPatMode(scope, lvl, r.Pattern, c.freshAt(lvl), nil, scrutineeMode, leafTypes, emit, purpose)
		return &soltype.RestPat{Pattern: sub}
	}
	return c.bindPatMode(scope, lvl, e, c.freshAt(lvl), nil, scrutineeMode, leafTypes, emit, purpose)
}

// tupleRestType resolves what a trailing `...rest` binds: the scrutinee's elements past the
// fixed prefix, re-gathered into a tuple. An inexact scrutinee hands its open tail on. With
// no tuple shape, as for an un-annotated parameter, the leftover is still SOME tuple, so it
// binds a variable bounded below by `[...]`. Unbounded it would coalesce to `never`, which
// lets a caller assign the result to anything. restConcrete drives the borrow wrap.
func (c *checker) tupleRestType(lvl int, node ast.Node, scrutinee soltype.Type, scrutTup, concreteTup *soltype.TupleType, prefix int) (bind, restConcrete soltype.Type) {
	tup, ok := c.restTupleShape(scrutinee, scrutTup, concreteTup)
	if !ok || len(tup.Elems) < prefix {
		v := c.freshAt(lvl)
		c.constrain(node, &soltype.TupleType{Inexact: true}, v)
		return v, nil
	}
	suffix := &soltype.TupleType{
		Elems:   slices.Clone(tup.Elems[prefix:]),
		Inexact: tup.Inexact,
	}
	return suffix, suffix
}

// restTupleShape picks the tuple a rest reads its suffix out of: concreteTup at a nested
// level, scrutTup at a top-level destructuring. Both are nil when the initializer
// references another module-level binding, whose variable is coalesced to its bound shape.
func (c *checker) restTupleShape(scrutinee soltype.Type, scrutTup, concreteTup *soltype.TupleType) (*soltype.TupleType, bool) {
	if concreteTup != nil {
		return concreteTup, true
	}
	if scrutTup != nil {
		return scrutTup, true
	}
	if _, isVar := scrutinee.(*soltype.TypeVarType); !isVar {
		return nil, false
	}
	return c.groundedTuple(groundedCarrier(scrutinee))
}

// concreteTupleShape resolves the statically known tuple a destructured leaf reads its
// element out of, and nil for a type that fixes no element. It is the tuple twin of
// fieldConcrete's union arm: a tuple grounds through groundedTuple, and a union of tuples
// grounds to the elementwise union of theirs. So `[a, b]` over
// `&[{p: number}, string] | &[{q: number}, string]` reads its first element as
// `{p: number} | {q: number}` and borrows it rather than moving it out.
//
// The members must agree on arity, since one element position has to name one type. An
// inexact member or union fixes no arity at all.
func (c *checker) concreteTupleShape(t soltype.Type) *soltype.TupleType {
	if tup, ok := c.groundedTuple(t); ok {
		return tup
	}
	u, isUnion := t.(*soltype.UnionType)
	if !isUnion || u.Inexact || len(u.Types) == 0 {
		return nil
	}
	members := make([]*soltype.TupleType, len(u.Types))
	for i, member := range u.Types {
		tup, ok := c.groundedTuple(member)
		if !ok || tup.Inexact {
			return nil
		}
		if i > 0 && len(tup.Elems) != len(members[0].Elems) {
			return nil
		}
		members[i] = tup
	}
	elems := make([]soltype.Type, len(members[0].Elems))
	for i := range elems {
		at := make([]soltype.Type, len(members))
		for j, m := range members {
			at[j] = m.Elems[i]
		}
		// No Context, so subsumption does not run. See peelBorrowUnion.
		elems[i] = newUnion(nil, at, false)
	}
	return &soltype.TupleType{Elems: elems}
}

// groundedTuple splices every `...P` spread into position so elements can be read by index,
// grounding `[number, ...Pair]` over `type Pair = [string, boolean]` to
// `[number, string, boolean]`. ok=false past an abstract spread, where no position is fixed.
func (c *checker) groundedTuple(t soltype.Type) (*soltype.TupleType, bool) {
	tup, ok := t.(*soltype.TupleType)
	if !ok {
		return nil, false
	}
	return newTypeEvaluator(c.ctx, newSeenPairs()).groundTuple(tup)
}

// objectRestType resolves what an object pattern's `...rest` binds: the scrutinee's members
// minus the keys its fields name, typed as the fresh object JavaScript's rest builds at run
// time rather than as `Omit<T, K>`. restMember decides each surviving member's form. With no
// ground object shape it binds a variable bounded below by `{...}`, tupleRestType's twin,
// and inferFunc opens such a parameter so a caller can fill the rest.
func (c *checker) objectRestType(lvl int, node ast.Node, scrutinee, concrete soltype.Type, named set.Set[string]) (bind, restConcrete soltype.Type) {
	obj, ok := c.restObjectShape(scrutinee, concrete)
	if !ok {
		v := c.freshAt(lvl)
		c.constrain(node, &soltype.ObjectType{Inexact: true}, v)
		return v, nil
	}
	// A setter reads as `undefined` unless a getter shares its name, so the getter names
	// are collected before any member is converted.
	getters := set.NewSet[string]()
	for _, el := range obj.Elems {
		if g, isGetter := el.(*soltype.GetterElem); isGetter {
			getters.Add(g.Name)
		}
	}
	elems := make([]soltype.ObjTypeElem, 0, len(obj.Elems))
	for _, el := range obj.Elems {
		if named.Contains(soltype.ObjElemName(el)) {
			continue
		}
		if kept, keep := restMember(el, getters); keep {
			elems = append(elems, kept)
		}
	}
	leftover := &soltype.ObjectType{Elems: elems, Inexact: obj.Inexact}
	return leftover, leftover
}

// restMember gives one leftover member the form it takes in the object a rest builds. A rest
// READS each name off the scrutinee and stores the result, so the leftover holds data
// properties rather than the accessors the scrutinee declared.
//
//   - A getter is read once and its result stored, so it becomes a plain property at the
//     getter's own type. `{x, ...rest}` over `{x: number, get y(self) -> string}` binds rest
//     at `{y: string}`. The property is writable, since a write after the copy mutates the
//     fresh object and never reaches the getter. A `throws` the getter declares is raised at
//     the destructuring itself, so the stored property does not carry it.
//   - A setter whose name has no getter reads as `undefined`, which is what the copy stores,
//     so it becomes a property at `undefined` rather than being dropped. getters holds the
//     names that do have one; a setter listed there is dropped, since that getter's arm has
//     already contributed the property.
//
// Every other member carries through unchanged. keep=false marks a dropped member.
func restMember(el soltype.ObjTypeElem, getters set.Set[string]) (soltype.ObjTypeElem, bool) {
	switch el := el.(type) {
	case *soltype.GetterElem:
		return &soltype.PropertyElem{Name: el.Name, Type: el.Type}, true
	case *soltype.SetterElem:
		if getters.Contains(el.Name) {
			return nil, false
		}
		return &soltype.PropertyElem{Name: el.Name, Type: &soltype.UndefinedType{}}, true
	default:
		return el, true
	}
}

// restObjectShape reports the object a rest reads its leftover members out of, the object
// twin of restTupleShape. It grounds the threaded concrete type first, then the scrutinee,
// then the scrutinee coalesced to the shape its bounds have fixed.
func (c *checker) restObjectShape(scrutinee, concrete soltype.Type) (*soltype.ObjectType, bool) {
	eval := newTypeEvaluator(c.ctx, newSeenPairs())
	if concrete != nil {
		if obj, ok := eval.groundToObject(concrete); ok {
			return obj, true
		}
	}
	if scrutinee == nil {
		return nil, false
	}
	if obj, ok := eval.groundToObject(scrutinee); ok {
		return obj, true
	}
	if _, isVar := scrutinee.(*soltype.TypeVarType); !isVar {
		return nil, false
	}
	return eval.groundToObject(groundedCarrier(scrutinee))
}

// bindInstancePat types a class-instance pattern `Name { x, y }`: it narrows the scrutinee
// to the named class, then binds each field sub-pattern against the projected member view.
// A missing field yields a MissingPropertyError; a non-class name an InstancePatternNotClassError.
func (c *checker) bindInstancePat(scope *Scope, lvl int, p *ast.InstancePat, scrutinee, concrete soltype.Type, scrutineeMode bindMode, leafTypes map[string]soltype.Type, emit leafEmit, purpose bindPurpose) soltype.Pat {
	// Record the pattern node's type against the scrutinee it matches, the same as the
	// sibling tuple/object cases, so hover and type-at-position resolve on the pattern.
	c.recordPatType(purpose, p, scrutinee)
	name := ast.QualIdentToString(p.ClassName)
	ct, ok := c.instancePatClass(scope, name)
	if !ok {
		c.reportPat(purpose, &InstancePatternNotClassError{Node: p, Name: name})
		// Bind the inner fields against a fresh var so a later reference to a bound leaf
		// stays defined without a second cascade error against the real scrutinee.
		obj, _ := c.bindPatMode(scope, lvl, p.Object, c.freshAt(lvl), nil, scrutineeMode, leafTypes, emit, purpose).(*soltype.ObjectPat)
		return &soltype.InstancePat{ClassName: name, Object: obj}
	}
	target, targetConcrete := c.narrowToClass(lvl, p, ct, scrutinee, concrete)
	obj, _ := c.bindPatMode(scope, lvl, p.Object, target, targetConcrete, scrutineeMode, leafTypes, emit, purpose).(*soltype.ObjectPat)
	return &soltype.InstancePat{ClassName: ct.Name, Object: obj}
}

// narrowToClass narrows scrutinee to the class ct and returns the instance member view its
// fields project out of, along with the concrete type to thread beside it. Both come back
// as the scrutinee and concrete passed in when the class has no projectable body.
//
// It is the narrowing half of an instance pattern, shared with the UCS IR's class test so
// the two cannot drift. blame is the node the narrowing constraint is anchored to: the
// written pattern for bindInstancePat, the tag test's class name for the IR.
func (c *checker) narrowToClass(
	lvl int, blame ast.Node, ct *soltype.ClassType, scrutinee, concrete soltype.Type,
) (target, targetConcrete soltype.Type) {
	inst := c.freshClassInstance(ct, lvl)
	// The pattern narrows the scrutinee to the named class. The instance flows into the
	// scrutinee, so a scrutinee that cannot be this class is rejected here.
	c.constrain(blame, inst, scrutinee)
	// Project the scrutinee's own instance when it names the same class, so its concrete
	// arguments give the field types directly; a downcast falls back to the asserted instance.
	projected := inst
	if sc, ok := classCarrier(scrutinee); ok && sc.Name == ct.Name {
		projected = sc
	}
	if body, ok := c.ctx.projectClassBody(projected); ok {
		return body, body
	}
	return scrutinee, concrete
}

// bindExtractorPat types an extractor pattern `Name(a, b)`: it narrows the scrutinee to the
// constructor's return type, then binds each argument against a constructor parameter. A
// non-constructor name is an ExtractorPatternNotCtorError, a wrong count an ExtractorPatternArityError.
//
// Binding against constructor parameters is an interim gate. The real protocol deconstructs
// through the instance's `[Symbol.customMatcher]` method, which needs symbol-keyed members
// soltype lacks, so it is deferred to M7 (m5-implementation-plan.md §"Nominal patterns").
func (c *checker) bindExtractorPat(scope *Scope, lvl int, p *ast.ExtractorPat, scrutinee soltype.Type, scrutineeMode bindMode, leafTypes map[string]soltype.Type, emit leafEmit, purpose bindPurpose) soltype.Pat {
	// Record the pattern node's type against the scrutinee it matches, the same as the
	// sibling tuple/object cases, so hover and type-at-position resolve on the pattern.
	c.recordPatType(purpose, p, scrutinee)
	name := ast.QualIdentToString(p.Name)
	ctor, ok := c.extractorCtor(scope, lvl, p.Name)
	if !ok {
		c.reportPat(purpose, &ExtractorPatternNotCtorError{Node: p, Name: name})
		// Bind each argument against a fresh var so its leaves stay defined and a later
		// reference does not cascade into an unknown-identifier error.
		args := make([]soltype.Pat, len(p.Args))
		for i, a := range p.Args {
			args[i] = c.bindPatMode(scope, lvl, a, c.freshAt(lvl), nil, scrutineeMode, leafTypes, emit, purpose)
		}
		return &soltype.ExtractorPat{Name: name, Args: args}
	}
	params := c.narrowToExtractor(p, ctor, scrutinee)
	if len(p.Args) != len(params) {
		c.reportPat(purpose, &ExtractorPatternArityError{Node: p, Name: name, Expected: len(params), Got: len(p.Args)})
	}
	args := make([]soltype.Pat, len(p.Args))
	for i, a := range p.Args {
		var paramType soltype.Type = c.freshAt(lvl)
		if i < len(params) {
			paramType = params[i].Type
		}
		args[i] = c.bindPatMode(scope, lvl, a, paramType, paramType, scrutineeMode, leafTypes, emit, purpose)
	}
	return &soltype.ExtractorPat{Name: name, Args: args}
}

// narrowToExtractor narrows scrutinee to ctor's return type and returns the parameters the
// extracted values bind against, read at the scrutinee's own type arguments.
//
// It is the narrowing half of an extractor pattern, shared with the UCS IR's extractor test
// so the two cannot drift. blame is the node the narrowing constraint is anchored to: the
// written pattern for bindExtractorPat, the tag test's name for the IR. The caller checks
// the returned count against how many values its pattern takes.
func (c *checker) narrowToExtractor(blame ast.Node, ctor *soltype.FuncType, scrutinee soltype.Type) []*soltype.FuncParam {
	// The extracted value is an instance of the constructor's return type. Narrow the
	// scrutinee to it, the same assertion an instance pattern makes.
	c.constrain(blame, ctor.Ret, scrutinee)
	// Read the parameters at the scrutinee's concrete arguments by substituting them
	// directly, rather than relying on the narrowing constraint above to back-propagate them.
	if sc, ok := classCarrier(scrutinee); ok {
		if ret, isClass := ctor.Ret.(*soltype.ClassType); isClass && ret.Name == sc.Name {
			return ctorParamsAt(ctor.Params, ret, sc)
		}
	}
	return ctor.Params
}

// ctorParamsAt rewrites a constructor's parameter types from its return instance's argument
// vars (ret) to the scrutinee instance's concrete arguments (sc), so an extractor over a
// generic scrutinee reads its parameters at the scrutinee's arguments.
//
// For `class Box<T> { value: T }` the instantiated constructor is `fn (value: t0) -> Box<t0>`,
// so ret is `Box<t0>`. Matching `Box(v)` against a `Box<string>` scrutinee makes sc `Box<string>`,
// so ctorParamsAt maps t0 to string and rewrites the parameter `value: t0` to `value: string`;
// v then binds at string. A non-generic constructor, whose return carries no arguments, builds
// an empty substitution and returns the parameters unchanged.
func ctorParamsAt(params []*soltype.FuncParam, ret, sc *soltype.ClassType) []*soltype.FuncParam {
	subst := &typeSubst{
		types:     map[*soltype.TypeVarType]soltype.Type{},
		lifetimes: map[*soltype.LifetimeVar]soltype.Lifetime{},
	}
	for i := 0; i < min(len(ret.TypeArgs), len(sc.TypeArgs)); i++ {
		if v, ok := ret.TypeArgs[i].(*soltype.TypeVarType); ok {
			subst.types[v] = sc.TypeArgs[i]
		}
	}
	for i := 0; i < min(len(ret.LifetimeArgs), len(sc.LifetimeArgs)); i++ {
		if lv, ok := ret.LifetimeArgs[i].(*soltype.LifetimeVar); ok {
			subst.lifetimes[lv] = sc.LifetimeArgs[i]
		}
	}
	if len(subst.types) == 0 && len(subst.lifetimes) == 0 {
		return params
	}
	out := make([]*soltype.FuncParam, len(params))
	for i, param := range params {
		cp := *param
		cp.Type = param.Type.Accept(subst, soltype.Positive)
		out[i] = &cp
	}
	return out
}

// instancePatClass resolves an instance pattern's name to its class handle, honoring the
// same scope precedence a written class reference uses. It returns ok=false when the name
// is unbound or names a value or type parameter rather than a class.
func (c *checker) instancePatClass(scope *Scope, name string) (*soltype.ClassType, bool) {
	b, ok := c.lookupClassBinding(scope, name)
	if !ok {
		return nil, false
	}
	ct, ok := b.Type.(*soltype.ClassType)
	return ct, ok
}

// extractorCtor resolves an extractor pattern's name to a class value's constructor
// signature. It looks the name up in the value sort, since a class binds its constructor on
// the value side of its dual type/value binding, then instantiates that value at lvl through
// bindingValue and pulls the constructor out with ctorSignature. Instantiating per call
// freshens a generic constructor's type parameters, so two match arms each written `Box(v)`
// get independent argument vars rather than sharing one. It returns ok=false when the name
// is unbound, carries no scheme, or resolves to a value that is not callable as a
// constructor.
func (c *checker) extractorCtor(scope *Scope, lvl int, qi ast.QualIdent) (*soltype.FuncType, bool) {
	b, ok := c.resolveQualValue(scope, qi)
	if !ok || len(b.Schemes) == 0 {
		return nil, false
	}
	return ctorSignature(c.bindingValue(lvl, b))
}

// resolveQualValue resolves a qualified identifier to its value binding. A bare name
// resolves by lexical lookup. A member name `Foo.bar` resolves its left through the
// namespace sort, then reads the member from that namespace's own value map, the same
// non-lexical member resolution resolvePath uses for a member-access expression.
func (c *checker) resolveQualValue(scope *Scope, qi ast.QualIdent) (ValueBinding, bool) {
	switch q := qi.(type) {
	case *ast.Ident:
		return scope.GetValue(q.Name)
	case *ast.Member:
		ns, ok := c.resolveQualNamespace(scope, q.Left)
		if !ok {
			return ValueBinding{}, false
		}
		b, ok := ns.Values[q.Right.Name]
		return b, ok
	}
	return ValueBinding{}, false
}

// resolveQualNamespace resolves a qualified identifier to a namespace. A bare name
// resolves through the namespace sort; a member name walks the nested namespace map.
func (c *checker) resolveQualNamespace(scope *Scope, qi ast.QualIdent) (*Namespace, bool) {
	switch q := qi.(type) {
	case *ast.Ident:
		return scope.GetNamespace(q.Name)
	case *ast.Member:
		parent, ok := c.resolveQualNamespace(scope, q.Left)
		if !ok {
			return nil, false
		}
		ns, ok := parent.Nested[q.Right.Name]
		return ns, ok
	}
	return nil, false
}

// resolveQualClassType resolves a qualified identifier to the class handle it names in the
// type sort. A bare name resolves through lookupClassBinding, honoring the class-namespace
// precedence a written class reference uses. A member name `Color.RGB` reads the handle from
// its namespace's own type map, so an enum variant resolves to its final variant class. It
// returns ok=false when the name is unbound or names a non-class binding.
func (c *checker) resolveQualClassType(scope *Scope, qi ast.QualIdent) (*soltype.ClassType, bool) {
	var b TypeBinding
	var ok bool
	switch q := qi.(type) {
	case *ast.Ident:
		b, ok = c.lookupClassBinding(scope, q.Name)
	case *ast.Member:
		ns, nsOK := c.resolveQualNamespace(scope, q.Left)
		if !nsOK {
			return nil, false
		}
		b, ok = ns.Types[q.Right.Name]
	}
	if !ok {
		return nil, false
	}
	ct, isClass := b.Type.(*soltype.ClassType)
	return ct, isClass
}

// ctorSignature resolves a value to its constructor signature in one of three shapes: a class
// value object's ConstructorElem, a bare FuncType, or either of those reached through a binding
// var's lower bounds. The var case is the common one, since a class value is a pre-bound var
// during its own inference component with the constructor recorded as a lower bound.
//
// For `class Point { x: number, y: number }` the value `Point` resolves mid-inference to a var
// whose lower bound is `{new (x: number, y: number) -> Point}`, and ctorSignature looks through
// the var to that object, returning its Constructor().Fn. Every class value has that shape,
// statics or not. The bare-FuncType arm is what an extractor pattern naming a plain function
// resolves through. A var with two conflicting constructor lower bounds is ambiguous and left
// unresolved.
func ctorSignature(t soltype.Type) (*soltype.FuncType, bool) {
	switch t := t.(type) {
	case *soltype.FuncType:
		return t, true
	case *soltype.ObjectType:
		if ctor, ok := t.Constructor(); ok {
			return ctor.Fn, true
		}
	case *soltype.TypeVarType:
		// Scan the var's lower bounds for a constructor, requiring all that resolve to agree.
		// A var can carry more than one lower bound, so keep the first constructor found and
		// compare each later one against it: if two name different constructors the var is an
		// ambiguous join of unrelated class values, so bail rather than pick one arbitrarily.
		var found *soltype.FuncType
		for _, lb := range t.LowerBounds {
			fn, ok := ctorSignature(lb)
			if !ok {
				continue
			}
			if found != nil && !equalType(found, fn) {
				return nil, false
			}
			found = fn
		}
		if found != nil {
			return found, true
		}
	}
	return nil, false
}

// freshClassInstance builds an instance of ct with a fresh inference var for each of the
// class's type parameters and a fresh lifetime for each lifetime parameter, so a pattern
// narrows a scrutinee to the class at unconstrained arguments the surrounding constraints
// then pin. A non-generic class yields the bare handle.
func (c *checker) freshClassInstance(ct *soltype.ClassType, lvl int) *soltype.ClassType {
	def, ok := c.ctx.classDef(ct.Name)
	if !ok {
		return &soltype.ClassType{Name: ct.Name, Final: ct.Final}
	}
	var typeArgs []soltype.Type
	if len(def.TypeParams) > 0 {
		typeArgs = make([]soltype.Type, len(def.TypeParams))
		for i := range typeArgs {
			typeArgs[i] = c.freshAt(lvl)
		}
	}
	var ltArgs []soltype.Lifetime
	if len(def.LifetimeParams) > 0 {
		ltArgs = make([]soltype.Lifetime, len(def.LifetimeParams))
		for i := range ltArgs {
			ltArgs[i] = c.ctx.freshLifetime(lvl)
		}
	}
	return &soltype.ClassType{Name: ct.Name, TypeArgs: typeArgs, LifetimeArgs: ltArgs, Final: ct.Final}
}

// applyLeafExtras resolves a destructured leaf's optional type annotation
// (`{x :: T}`, `[a :: T]`) and default value (`{x = d}`, `[a = d]`) against its
// leaf type, returning the type to bind. An annotation constrains the leaf type
// to satisfy it and is then adopted as the leaf's type, mirroring how an annotated
// `val` adopts its annotation. A default is required to satisfy that bound type
// and flows into it, so a leaf bound from an absent-but-defaulted field reads the
// default's type rather than `never`.
func (c *checker) applyLeafExtras(scope *Scope, lvl int, node ast.Node, leafType soltype.Type, typeAnn ast.TypeAnn, def ast.Expr) soltype.Type {
	bound := leafType
	if typeAnn != nil {
		if annT, ok := c.resolveTypeAnn(scope, typeAnn, lvl); ok {
			c.constrain(node, leafType, annT)
			bound = annT
		}
	}
	if def != nil {
		defT := c.inferExpr(scope, lvl, def)
		c.constrain(def, defT, bound)
	}
	return bound
}

// applyBindMode wraps a destructured leaf's leaf type according to the scrutinee's
// binding mode and the leaf's own `mut` marker. It returns the type the leaf binds at.
//
//   - Owned scrutinee: the leaf is moved out. A `mut` leaf thaws into an owned-mutable
//     cell, so a later write through it succeeds. A plain leaf keeps the leaf's
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
func (c *checker) applyBindMode(lvl int, node ast.Node, mut bool, leafType, concrete soltype.Type, scrutineeMode bindMode) soltype.Type {
	switch scrutineeMode.borrow {
	case bmImm:
		if mut {
			c.report(&MutLeafThroughSharedBorrowError{Node: node})
		}
		if ri, ok := leafType.(soltype.RefInner); ok && soltype.BorrowableType(concrete) {
			return soltype.NewRef(false, scrutineeMode.lt, ri)
		}
		return leafType
	case bmMut:
		if _, ok := leafType.(soltype.RefInner); ok && soltype.BorrowableType(concrete) {
			// Route the projection through a fresh variable rather than wrapping the
			// leaf type directly. A tuple or object pattern pins each leaf type to the
			// scrutinee's concrete element as an upper bound. That makes the leaf type
			// invariantly exact under the `&mut` wrapper, so an inexact write
			// requirement `mut {y, ...}` would clash with the exact element. The fresh
			// variable takes the leaf type only as a lower bound. Its shape stays free to
			// absorb the write requirement.
			v := c.freshAt(lvl)
			c.constrain(node, leafType, v)
			return soltype.NewRef(true, scrutineeMode.lt, v)
		}
		return leafType
	default: // bmOwned
		if mut {
			return c.thawOwnedLeaf(lvl, node, leafType, concrete)
		}
		return leafType
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
// routes the projection variable through a fresh widenable variable. The leaf type
// flows in as a lower bound, and widening at coalesce time turns a literal field into
// its primitive. The cell carries a variable rather than a concrete object. That is
// less precise to render but still admits the write.
func (c *checker) thawOwnedLeaf(lvl int, node ast.Node, leafType, concrete soltype.Type) soltype.Type {
	if concrete != nil {
		widened := widen(stripOwnedMut(concrete))
		inner, ok := widened.(soltype.RefInner)
		if !ok {
			// A primitive or function leaf is not borrowable, so `mut` is a no-op. It
			// keeps its leaf type, mirroring `val mut a = 1` keeping the primitive. Only
			// an object or tuple leaf thaws into a mutable cell.
			return leafType
		}
		ref := soltype.NewRef(true, nil, inner)
		c.recordProv(ref, node, OwnedMutConstruction)
		return ref
	}
	v := c.freshAt(lvl)
	v.Widenable = true
	c.constrain(node, leafType, v)
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
	switch t := t.(type) {
	case *soltype.ObjectType:
		if prop, found := t.Prop(name); found {
			return prop.Type
		}
	case *soltype.UnionType:
		// A union of objects knows the field's shape as the union of what each member holds
		// there. Reading it is what lets a leaf still decide whether to borrow when narrowing
		// left the scrutinee as several members. An inexact union's open tail may carry the
		// field at any type, so its shape is not known and the read answers nil.
		if t.Inexact {
			return nil
		}
		fields := make([]soltype.Type, len(t.Types))
		for i, member := range t.Types {
			field := fieldConcrete(member, name)
			if field == nil {
				return nil
			}
			fields[i] = field
		}
		if len(fields) == 0 {
			return nil
		}
		// No Context, so subsumption does not run. See peelBorrowUnion.
		return newUnion(nil, fields, false)
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
func (c *checker) bindLeaf(scope *Scope, name string, t soltype.Type, node ast.Node, leafTypes map[string]soltype.Type, emit leafEmit, purpose bindPurpose) {
	emit(scope, name, t, node)
	c.recordPatType(purpose, node, t)
	if leafTypes != nil {
		leafTypes[name] = t
	}
}

// reportPat reports a fault found while walking a pattern, unless the walk is a projection.
// A projection walks a pattern the binding walk already walked, so every fault it finds is
// one the binding walk already reported. See bindPurpose.
func (c *checker) reportPat(purpose bindPurpose, e SolverError) {
	if purpose == forBinding {
		c.report(e)
	}
}

// reportPatUnsupported is reportPat for a pattern form the walk cannot type at all.
// reportUnsupported words that message.
func (c *checker) reportPatUnsupported(purpose bindPurpose, n ast.Node) {
	if purpose == forBinding {
		c.reportUnsupported(n)
	}
}

// recordPatType records the type a pattern node resolved to, which is what an editor reads
// for that node. A projection records nothing, since the node's type belongs to the binding
// walk rather than to the second value a projection is reading. See bindPurpose.
func (c *checker) recordPatType(purpose bindPurpose, n ast.Node, t soltype.Type) {
	if purpose == forBinding {
		c.recordType(n, t)
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

// atomLitOf lowers `null` and `undefined` to the soltype type each one names and to the
// pattern that matches it. They are the two literals whose type is not a LitType, since
// soltype.Lit has no member for either. litTypeOf below therefore cannot return them, and
// every site that turns a written literal into a type asks this function first. ok=false
// for every other literal kind, which litTypeOf covers.
//
// The caller must not record provenance against the returned type. soltype.NullType and
// soltype.UndefinedType are empty structs, so Go gives every instance of one the same
// address, and the Prov side table is keyed by pointer identity. Recording against one
// would file every `null` in the module under a single entry, so each would report the
// last one's span, and the debugProv guard would panic on the second. The NeverTypeAnn
// and UnknownTypeAnn arms in resolveTypeAnn skip recording for the same reason.
func atomLitOf(lit ast.Lit) (soltype.Type, soltype.Pat, bool) {
	switch lit.(type) {
	case *ast.NullLit:
		return &soltype.NullType{}, &soltype.NullPat{}, true
	case *ast.UndefinedLit:
		return &soltype.UndefinedType{}, &soltype.UndefinedPat{}, true
	}
	return nil, nil, false
}

// litTypeOf lowers an ast literal to its soltype LitType, mirroring inferLiteral.
// ok=false for a literal kind outside the M-subset (the caller reports it).
// `null` and `undefined` also return ok=false here even though each has a soltype
// form, since neither form is a LitType. A caller that accepts them asks atomLitOf
// above first.
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
