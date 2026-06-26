package soltype

import "fmt"

// Type is the sealed interface for all soltype nodes.
//
// Accept threads a polarity-flipping rewriting visitor over the node, defined in
// visitor.go. The structural type→type transforms coalesce, extrude, and
// freshenAbove are implemented on top of it so variance and the
// rebuild-from-children boilerplate live in one place. The marker isType stays
// unexported so the interface is sealed to this package.
type Type interface {
	isType()
	Accept(v TypeVisitor, pol Polarity) Type
}

// TypeVarType is an inference variable carrying Simple-sub lower/upper bound
// lists plus the level at which it was created, used for let-generalization.
type TypeVarType struct {
	ID          int
	Level       int
	LowerBounds []Type
	UpperBounds []Type
	// Open marks the variable of an `open` parameter. It is read only at
	// display-time coalescing. A usage-inferred object on an open var's upper
	// bounds stays inexact, row-polymorphic, instead of closing to exact. It has
	// no effect on constraint solving.
	Open bool
	// Widenable marks the binding var of an un-annotated `var`. Like Open it is
	// read only at coalescing. A widenable var's coalesced value has its literals
	// lowered to their primitives, so `5` becomes number in covariant position and
	// a mutable cell reads back as the primitive it may later hold. It has no
	// effect on constraint solving. This stays sound while the only position that
	// demands a literal super-type is the reassignment slot, itself a coalesced
	// view, because no other site can observe the literal the graph still holds.
	// Literal type annotations would be a second such site.
	Widenable bool
}

// BoundsAt returns the bounds relevant to a polarity: lowers in Positive
// position (the var becomes their union), uppers in Negative (their meet).
func (v *TypeVarType) BoundsAt(pol Polarity) []Type {
	if pol == Positive {
		return v.LowerBounds
	}
	return v.UpperBounds
}

// Prim is the closed set of primitives soltype carries. It mirrors the
// type_system package's Prim enum, but carries only the three the tests
// exercise. Further primitives such as BigIntPrim and SymbolPrim, and further
// literals such as BigIntLit, NullLit, and UndefinedLit, extend Prim and Lit to
// the full type_system set as the parser bridge surfaces them. The additions are
// inert from constrain's perspective, the same prim and literal arms with one
// more concrete each.
type Prim int

const (
	NumPrim Prim = iota
	StrPrim
	BoolPrim
)

type PrimType struct{ Prim Prim }

// Lit is the sealed interface for literal values inside a LitType.
// It mirrors type_system.Lit with NumLit, StrLit, and BoolLit concretes so each
// literal kind carries exactly the value field it needs, avoiding a flat struct
// where two of three value fields are dead per instance.
type Lit interface{ isLit() }

type NumLit struct{ Value float64 }
type StrLit struct{ Value string }
type BoolLit struct{ Value bool }

func (*NumLit) isLit()  {}
func (*StrLit) isLit()  {}
func (*BoolLit) isLit() {}

type LitType struct{ Lit Lit }

// Equal is structural equality on the contained literal.
func (l *LitType) Equal(o *LitType) bool {
	switch a := l.Lit.(type) {
	case *NumLit:
		b, ok := o.Lit.(*NumLit)
		return ok && a.Value == b.Value
	case *StrLit:
		b, ok := o.Lit.(*StrLit)
		return ok && a.Value == b.Value
	case *BoolLit:
		b, ok := o.Lit.(*BoolLit)
		return ok && a.Value == b.Value
	default:
		panic(fmt.Sprintf("unknown Lit type in LitType.Equal: %T", l.Lit))
	}
}

// Pat is the sealed interface for parameter patterns. It mirrors the role of
// type_system.Pat and ast.Pat but lives in soltype to keep soltype ast-free.
// Destructuring concretes such as TuplePat and RecordPat are added as the parser
// bridge surfaces them, with no FuncParam restructuring.
type Pat interface{ isPat() }

type IdentPat struct{ Name string }

func (*IdentPat) isPat() {}

// TuplePat is a tuple destructuring pattern. Its sub-patterns are
// positional. It is carried on a destructured FuncParam.Pattern so the parameter
// renders and round-trips. The solver's pattern-typing helper produces it.
type TuplePat struct{ Elems []Pat }

func (*TuplePat) isPat() {}

// ObjectPat is an object destructuring pattern. Each field names a
// property and binds its value through a sub-pattern. A bare `{x}` shorthand is
// an ObjectPatField whose Value is an IdentPat of the same name.
type ObjectPat struct{ Fields []*ObjectPatField }

func (*ObjectPat) isPat() {}

// ObjectPatField is one `name: subpat` entry of an ObjectPat.
type ObjectPatField struct {
	Name  string
	Value Pat
}

// LitPat matches a literal value. It binds nothing. It is carried for rendering
// and for match-arm typing.
type LitPat struct{ Lit Lit }

func (*LitPat) isPat() {}

// WildcardPat (`_`) matches anything and binds nothing.
type WildcardPat struct{}

func (*WildcardPat) isPat() {}

// ExtractorPat is a constructor/extractor pattern such as `Some(v)` or
// `Point(x, y)`. The solver does not yet produce it. It is a forward-declared
// member of the sealed set and the structural mirror of ast.ExtractorPat. Name
// is the qualified constructor name rendered as a string, since soltype stays
// ast-free. Args are positional sub-patterns.
type ExtractorPat struct {
	Name string
	Args []Pat
}

func (*ExtractorPat) isPat() {}

// InstancePat is a class-instance pattern such as `Point { x, y }`. Like
// ExtractorPat it is forward-declared here, the mirror of ast.InstancePat.
// ClassName is the qualified class name as a string. Object is the field
// sub-pattern.
type InstancePat struct {
	ClassName string
	Object    *ObjectPat
}

func (*InstancePat) isPat() {}

// FuncParam mirrors type_system.FuncParam. Pattern is reachable through the Pat
// concretes such as IdentPat. Optional marks an `x?` parameter. It lowers the
// function's `required` count, the accept-set lower bound, without removing the
// parameter from the declared list. The parameter stays at its position with its
// type, the slot simply may go unsupplied.
type FuncParam struct {
	Pattern  Pat
	Type     Type
	Optional bool // x? — lowers `required` without changing arity (len(Params))
	// Rest marks a typed rest param (`...xs: T[]`), which must be the LAST param. It
	// binds zero or more trailing arguments, so it is never required and lifts the
	// function's accept-set upper bound to ∞. This is distinct from the inexact
	// `...` marker, which is callback-only. Per-extra element-type checking needs
	// Array types; this models only the arity effect.
	Rest bool
}

// FuncType is a possibly multi-argument function type. Inexact distinguishes two
// forms. A bare `fn(p1..pn)` is exact and tolerates at most n arguments, an
// accept-set of [required, n]. A `fn(p1..pn, ...)` written with a trailing `...`
// is inexact and tolerates extra arguments when used as a callback, an accept-set
// of [required, ∞). Exactness governs callback subtyping, not direct calls. See
// the solver's acceptSet and the FuncType<:FuncType constrain rule.
//
// The flag is Inexact rather than Exact so the ZERO VALUE is exact, matching
// Escalier's exact-by-default semantics. A function minted without thinking about
// exactness is correctly exact, and the structural rewriters coalesce, extrude,
// and freshenAbove carry the flag through unchanged. Only the parser's `...`
// marker sets it.
type FuncType struct {
	Params  []*FuncParam
	Ret     Type
	Inexact bool // trailing `...` ⇒ true; bare fn(...) ⇒ false (the exact zero value)
}

// TupleType is a tuple type. Inexact follows the ObjectType/FuncType convention:
// the zero value is exact, so a tuple is fixed-length by default and only the
// parser's trailing `...` marker sets it. An inexact tuple (`[A, ...]`) accepts a
// longer tuple as a subtype, matching the shared prefix element-wise.
type TupleType struct {
	Elems   []Type
	Inexact bool // trailing `...` ⇒ true
}

// ObjectType is the structural object type, the carrier for object literals,
// object/interface annotations, and class instance bodies, so one
// structural-decomposition routine serves all three. It promotes the earlier
// RecordType{Fields} to an ordered element list. It currently carries only
// PropertyElem. MethodElem, GetterElem, SetterElem, IndexSigElem, and the object
// rest/spread RestElem each add a new ObjTypeElem arm later.
//
// Inexact follows the FuncType convention. The zero value is exact, matching
// Escalier's exact-by-default semantics, so every object already minted, both
// literals and member-access requirements, is exact by default with no
// construction-site churn. Only the parser's trailing `...` marker sets it.
// Subtyping matches elements by name, so order is irrelevant. The slice order is
// preserved only for stable rendering.
type ObjectType struct {
	Elems   []ObjTypeElem // ordered, name-deduped (last wins); Prop(name) lookup
	Inexact bool          // trailing `...` ⇒ true
}

// ObjTypeElem is the sealed set of object members, mirroring type_system's
// ObjTypeElem. It currently carries PropertyElem only. Method, getter, and setter
// members, index signatures, and the object rest/spread add arms later.
type ObjTypeElem interface{ isObjTypeElem() }

// PropertyElem is one named value property of an ObjectType.
type PropertyElem struct {
	Name     string
	Type     Type
	Optional bool // `x?: T`; the object-spread show-through rule keys off it
	Readonly bool // `readonly f: T`; forbids `obj.f = …` only, orthogonal to deep mut
}

func (*PropertyElem) isObjTypeElem() {}

// Prop returns the named property and whether it is present. Property names are
// unique in a well-formed ObjectType — the constraint solver dedups duplicate
// keys (last value wins) when it builds an object from a literal — so the first
// match is the property. The scan is linear because objects are small. It is the
// single canonical property lookup shared by constraining, structural equality,
// and member access. The Elems are currently all PropertyElem; the lookup widens
// to method/getter/setter members later.
func (o *ObjectType) Prop(name string) (*PropertyElem, bool) {
	for _, e := range o.Elems {
		if p, ok := e.(*PropertyElem); ok && p.Name == name {
			return p, true
		}
	}
	return nil, false
}

// AsProperty narrows an ObjTypeElem to its *PropertyElem. PropertyElem is
// currently the only ObjTypeElem kind, so any other element is a bug. It would be
// a later member kind such as a method, getter, setter, index signature, or
// object rest/spread wired up without extending the call site that processes
// every element. It panics rather than silently skipping, matching type_system's
// unknown-element convention where print_type.go panics on an unhandled
// ObjTypeElem, and the standing rule that a missed element kind must fail loudly,
// not vanish from subtyping, equality, or rendering. Use it at sites that must
// visit EVERY element. Name lookups like Prop legitimately skip non-matching
// kinds instead.
func AsProperty(e ObjTypeElem) *PropertyElem {
	p, ok := e.(*PropertyElem)
	if !ok {
		panic(fmt.Sprintf("AsProperty: unhandled ObjTypeElem %T", e))
	}
	return p
}

// RefType is the single wrapper for borrows and mutability. A bare value is owned
// and immutable; wrapping it in a RefType marks it mutable, borrowed, or both:
//
//	Mut=false Lt=nil   forbidden degenerate cell — NewRef returns the bare Inner
//	Mut=false Lt='a    immutable borrow
//	Mut=true  Lt=nil   owned mutable
//	Mut=true  Lt='a    mutable borrow
//
// The single RefType<:RefType constrain rule reads Mut for inner variance and Lt
// for the lifetime outlives check. The Lifetime sort comprises the LifetimeVar
// and StaticLifetime concretes, freshLifetime, constrainLt, and the probe
// extension. A fresh lifetime is attached to a borrowed parameter, activating the
// rule's outlives step. Until lifetimes originate on borrows every minted RefType
// still carries Lt == nil, so the immutable-borrow and lifetime forms above are
// only reached once that lands.
type RefType struct {
	Mut   bool
	Lt    Lifetime // nilable; carries a lifetime once one is attached to a borrow
	Inner RefInner
}

// RefInner is the sealed set of types that may sit inside a RefType. PrimType /
// LitType / FuncType / PromiseType are deliberately excluded: a promise or function
// reference is shared, not borrowed, and a `mut` primitive is a JS no-op. Excluding
// PromiseType blocks borrowing the promise itself — there is no `mut Promise` or
// `'a Promise`. It does NOT block the promise's payload from being a borrow:
// `Promise<mut 'a Point>` is a PromiseType whose type argument is a RefType, which
// is well-formed.
//
// It currently admits ObjectType, TupleType, and TypeVarType. The TypeVarType arm
// covers a borrow whose content is still an inference variable. The content
// invariant, that it resolves to a borrowable type, is checked at constrain time.
// UnionType, IntersectionType, AliasType, and ClassType add their isRefInner arms
// later.
type RefInner interface {
	Type
	isRefInner()
}

func (*ObjectType) isRefInner()  {}
func (*TupleType) isRefInner()   {}
func (*TypeVarType) isRefInner() {}

// PromiseType is the result of an `async fn` and the requirement of an `await`.
// It is a dedicated concrete rather than a generic TypeRefType, keeping the scope
// narrow. It is the one stdlib generic typed directly; Iterable and Generator
// wait. The real, alias-driven `Promise<T>` lookup arrives with TypeRef
// ingestion. Until then, an `async fn () -> T` mints a PromiseType{T} externally
// and `await e` constrains `e <: PromiseType{U}` for a fresh U. Inner is covariant
// under subtyping, so Promise<L> <: Promise<R> iff L <: R. The `await` rule does
// NOT recursively flatten, so awaiting `Promise<Promise<T>>` yields `Promise<T>`,
// matching the no-auto-flatten contract. Flattening is `Awaited<T>`.
type PromiseType struct{ Inner Type }

// Void is the result type of a statement block with no value.
type Void struct{}

// NullType is the type whose only inhabitant is the `null` literal. It
// mirrors TypeScript's `null` type and sits alongside Void as a distinct
// atomic kind. The canonical comparator sorts both kinds last so a union
// such as `T | null | void` consistently renders with the data members
// first.
type NullType struct{}

// NeverType (⊥) and UnknownType (⊤) are the bottom and top of the subtype
// lattice, the coalesced output of an empty-bounds single-polarity variable. A
// positive variable coalesces to never, a negative one to unknown. soltype
// carries them natively because they are fundamental to the lattice, not optional
// sugar.
type NeverType struct{}
type UnknownType struct{}

// UnionType / IntersectionType are coalesced-output nodes for multi-bound
// single-polarity variables. A positive variable coalesces to a union of its
// lowers, a negative one to an intersection of its uppers. soltype carries them
// natively so coalescing returns soltype.Type in every case. They are later
// promoted to first-class lattice members: legal `constrain` inputs, writable
// annotations, and the subjects of a normalization pass.
//
// UnionType.Inexact flags whether the union is open. A bare `A | B` is
// exact, so its inhabitants are exactly A ∪ B. An `A | B | ...` written with
// a trailing `...` is inexact, meaning at least these, with an unknown-typed tail.
// The flag is Inexact rather than Exact so the zero value is exact, matching
// the ObjectType, TupleType, and FuncType convention. IntersectionType
// carries no exactness flag, since exactness is a property of the result
// rather than the meet.
type UnionType struct {
	Types []Type
	// Inexact tracks the trailing `...` marker. The zero value is exact.
	Inexact bool
}
type IntersectionType struct{ Types []Type }

// ErrorType is the error-recovery sentinel, a childless atom distinct from never
// (⊥) and unknown (⊤). Unlike those two, which are coalesced-OUTPUT only and never
// appear as constrain inputs, ErrorType is a legal constrain INPUT that ABSORBS in
// both directions, so any constraint with an ErrorType operand trivially succeeds.
// The solver's report mints it as the value-position placeholder after emitting a
// diagnostic, so the placeholder never cascades a second, spurious failure. This
// is the standard "error type" of TS, Roslyn, and GHC. It is never user-spellable,
// distinct from a future `any`, and never produced from user syntax. It renders as
// `error` for diagnostics and debug only.
type ErrorType struct{} // ⊤⊥ absorbing sentinel

func (*TypeVarType) isType()      {}
func (*PrimType) isType()         {}
func (*LitType) isType()          {}
func (*FuncType) isType()         {}
func (*TupleType) isType()        {}
func (*ObjectType) isType()       {}
func (*RefType) isType()          {}
func (*PromiseType) isType()      {}
func (*Void) isType()             {}
func (*NullType) isType()         {}
func (*NeverType) isType()        {}
func (*UnknownType) isType()      {}
func (*UnionType) isType()        {}
func (*IntersectionType) isType() {}
func (*ErrorType) isType()        {}

// LevelOf is the max level of any TypeVarType inside t; concrete leaves are 0.
// It covers the current type set and grows as new formers are added.
func LevelOf(t Type) int {
	switch t := t.(type) {
	case *TypeVarType:
		return t.Level
	case *FuncType:
		m := 0
		for _, p := range t.Params {
			m = max(m, LevelOf(p.Type))
		}
		return max(m, LevelOf(t.Ret))
	case *TupleType:
		m := 0
		for _, e := range t.Elems {
			m = max(m, LevelOf(e))
		}
		return m
	case *ObjectType:
		m := 0
		for _, e := range t.Elems {
			m = max(m, LevelOf(AsProperty(e).Type))
		}
		return m
	case *PromiseType:
		return LevelOf(t.Inner)
	case *RefType:
		// A borrow's level is the max of its inner content's and its lifetime's. The
		// lifetime is a SECOND quantifiable variable on the wrapper. A concrete-inner
		// borrow whose lifetime var sits above a freshen/extrude limit must NOT be
		// pruned and shared whole, or two instantiations would alias one LifetimeVar.
		// Folding the lifetime level in here is the load-bearing change that makes the
		// level prune descend into such a borrow to freshen its lifetime.
		// LevelOfLifetime returns 0 for 'static and a nil slot.
		return max(LevelOf(t.Inner), LevelOfLifetime(t.Lt))
	// Union and Intersection recurse into their members for the same reason, but only
	// the IntersectionType arm is load-bearing today. overloadIntersection (solver) is
	// the ONE producer of a lattice node carrying LIVE inference variables that flows
	// into freshenAbove — a let-bound overload's value-position type, whose generic arm
	// holds a Level>0 var. If LevelOf returned 0 here, freshenAbove's level prune would
	// treat the whole intersection as level 0, SHARE it, and alias that var across
	// instantiations (two uses of the overload would cross-contaminate). So the level
	// MUST reflect the members. UnionType has no such producer — every soltype.UnionType
	// is coalesce OUTPUT (var-free) — so its recursion is currently dead, kept for
	// symmetry and for when the type set grows a union with live vars (e.g. a generic
	// union annotation). Coalesced-output unions/intersections hold no live vars, so
	// both arms still return 0 for them.
	case *UnionType:
		return maxMemberLevel(t.Types)
	case *IntersectionType:
		return maxMemberLevel(t.Types)
	default:
		// PrimType, LitType, Void, NullType, NeverType, UnknownType, ErrorType:
		// childless leaves. ErrorType is a sentinel at level 0.
		return 0
	}
}

// maxMemberLevel returns the highest LevelOf across a Union/Intersection's members,
// 0 for an empty slice. Shared by the two lattice arms of LevelOf so their identical
// recursion lives in one place.
func maxMemberLevel(types []Type) int {
	m := 0
	for _, e := range types {
		m = max(m, LevelOf(e))
	}
	return m
}
