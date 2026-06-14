package soltype

import "fmt"

// Type is the sealed interface for all soltype nodes. (Production name for the
// spike's SimpleType; marker renamed isSimpleType -> isType.)
//
// Accept threads a polarity-flipping rewriting visitor over the node (visitor.go);
// the structural type→type transforms (coalesce / extrude / freshenAbove) are
// implemented on top of it so variance and the rebuild-from-children boilerplate
// live in one place. The marker isType stays unexported so the interface is sealed
// to this package.
type Type interface {
	isType()
	Accept(v TypeVisitor, pol Polarity) Type
}

// TypeVarType is an inference variable carrying Simple-sub lower/upper bound
// lists plus the level at which it was created (for let-generalization in M3).
type TypeVarType struct {
	ID          int
	Level       int
	LowerBounds []Type
	UpperBounds []Type
	// Open marks the variable of an `open` parameter (M4 B2). It is read only at
	// display-time coalescing: a usage-inferred object on an open var's upper
	// bounds stays inexact (row-polymorphic) instead of closing to exact. It has
	// no effect on constraint solving.
	Open bool
	// Widenable marks the binding var of an un-annotated `var` (M4 B3). Like Open
	// it is read only at coalescing: a widenable var's coalesced value has its
	// literals lowered to their primitives (`5` ⇒ number) in covariant position,
	// so a mutable cell reads back as the primitive it may later hold. It has no
	// effect on constraint solving. This stays sound while the only position that
	// demands a literal super-type is the reassignment slot — itself a coalesced
	// view — because no other site can observe the literal the graph still holds;
	// literal type annotations (a second such site) are a later milestone.
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

// Prim is the closed set of primitives M1 carries. Mirrors the type_system
// package's Prim enum, but only the three M1's tests exercise; M2+ extends
// Prim (BigIntPrim, SymbolPrim) and Lit (BigIntLit, NullLit, UndefinedLit)
// to the full type_system set as the parser bridge surfaces them. The
// additions are inert from constrain's perspective — same prim/literal arms
// with one more concrete each — so the deferral is purely scope, not design.
type Prim int

const (
	NumPrim Prim = iota
	StrPrim
	BoolPrim
)

type PrimType struct{ Prim Prim }

// Lit is the sealed interface for literal values inside a LitType.
// Mirrors type_system.Lit (with NumLit/StrLit/BoolLit concretes) so each
// literal kind carries exactly the value field it needs — no flat struct
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

// Pat is the sealed interface for parameter patterns. Mirrors the role of
// type_system.Pat (and ast.Pat) but lives in soltype to keep soltype ast-free.
// M1 ships a single concrete (IdentPat); M2 adds destructuring concretes
// (TuplePat, RecordPat, …) as the parser bridge surfaces them, with no
// FuncParam restructuring.
type Pat interface{ isPat() }

type IdentPat struct{ Name string }

func (*IdentPat) isPat() {}

// FuncParam mirrors type_system.FuncParam. Pattern is reachable only through Pat
// concretes M1 defines (IdentPat). M3 (PR4) adds Optional: an `x?` parameter
// lowers the function's `required` count (the accept-set lower bound) without
// removing the parameter from the declared list — it stays at its position with
// its type, the slot simply may go unsupplied.
type FuncParam struct {
	Pattern  Pat
	Type     Type
	Optional bool // PR4: x? — lowers `required` without changing arity (len(Params))
	// Rest marks a typed rest param (`...xs: T[]`), which must be the LAST param: it
	// binds zero or more trailing arguments, so it is never required and lifts the
	// function's accept-set upper bound to ∞ (#677 §4.2.3) — distinct from the inexact
	// `...` marker (which is callback-only). Per-extra element-type checking (§4.2.2)
	// needs Array types and is M4; M3 models only the arity effect.
	Rest bool
}

// FuncType is a (possibly multi-argument) function type. M3 (PR4) adds Inexact: a
// bare `fn(p1..pn)` is exact (tolerates at most n arguments — accept-set
// [required, n]); a `fn(p1..pn, ...)` written with a trailing `...` is inexact (it
// tolerates extra arguments when used as a callback — accept-set [required, ∞)).
// Exactness governs callback subtyping, not direct calls (#677); see solver's
// acceptSet / the FuncType<:FuncType constrain rule.
//
// The flag is Inexact (not Exact) so the ZERO VALUE is exact, matching Escalier's
// exact-by-default semantics: a function minted without thinking about exactness is
// correctly exact, and the structural rewriters (coalesce, extrude, freshenAbove)
// carry the flag through unchanged. Only the parser's `...` marker sets it.
type FuncType struct {
	Params  []*FuncParam
	Ret     Type
	Inexact bool // PR4: trailing `...` ⇒ true; bare fn(...) ⇒ false (the exact zero value)
}

// TupleType is a tuple type. Inexact follows the ObjectType/FuncType convention:
// the zero value is exact, so a tuple is fixed-length by default and only the
// parser's trailing `...` marker sets it. An inexact tuple (`[A, ...]`) accepts a
// longer tuple as a subtype, matching the shared prefix element-wise.
type TupleType struct {
	Elems   []Type
	Inexact bool // trailing `...` ⇒ true
}

// ObjectType is the structural object type — the carrier for object literals,
// object/interface annotations, and (M5) class instance bodies, so one
// structural-decomposition routine serves all three. It promotes M2's
// RecordType{Fields} to an ordered element list. M4 ships only PropertyElem;
// MethodElem/GetterElem/SetterElem arrive in M5, and IndexSigElem plus the
// object rest/spread RestElem in M9, each a new ObjTypeElem arm.
//
// Inexact follows M3's FuncType convention: the zero value is exact, matching
// Escalier's exact-by-default semantics, so every object M2 already mints —
// literals, member-access requirements — is exact by default with no
// construction-site churn. Only the parser's trailing `...` marker sets it.
// Subtyping matches elements by name (order is irrelevant); the slice order is
// preserved only for stable rendering.
type ObjectType struct {
	Elems   []ObjTypeElem // ordered, name-deduped (last wins); Prop(name) lookup
	Inexact bool          // trailing `...` ⇒ true
}

// ObjTypeElem is the sealed set of object members, mirroring type_system's
// ObjTypeElem. M4 ships PropertyElem only; method/getter/setter members (M5),
// index signatures and the object rest/spread (M9) add arms later.
type ObjTypeElem interface{ isObjTypeElem() }

// PropertyElem is one named value property of an ObjectType.
type PropertyElem struct {
	Name     string
	Type     Type
	Optional bool // `x?: T`; the M9 object-spread show-through rule keys off it
}

func (*PropertyElem) isObjTypeElem() {}

// Prop returns the named property and whether it is present. Property names are
// unique in a well-formed ObjectType — the constraint solver dedups duplicate
// keys (last value wins) when it builds an object from a literal — so the first
// match is the property. The scan is linear because objects are small; it is the
// single canonical property lookup shared by constraining, structural equality,
// and member access. M4's Elems are all PropertyElem; M5 widens the lookup to
// method/getter/setter members.
func (o *ObjectType) Prop(name string) (*PropertyElem, bool) {
	for _, e := range o.Elems {
		if p, ok := e.(*PropertyElem); ok && p.Name == name {
			return p, true
		}
	}
	return nil, false
}

// AsProperty narrows an ObjTypeElem to its *PropertyElem. M4 ships PropertyElem
// as the only ObjTypeElem kind, so any other element is a bug: a later member
// kind (method/getter/setter in M5, index signature or object rest/spread in M9)
// wired up without extending the call site that processes every element. It
// panics rather than silently skipping, matching type_system's unknown-element
// convention (print_type.go panics on an unhandled ObjTypeElem) and the M4 plan's
// standing rule that a missed element kind must fail loudly, not vanish from
// subtyping, equality, or rendering. Use it at sites that must visit EVERY
// element; name lookups like Prop legitimately skip non-matching kinds instead.
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
// The single RefType<:RefType constrain rule (M4 C2) reads Mut for inner variance
// and Lt for the lifetime outlives check; until then a RefType only carries data.
// Lt is always nil in C1 — the Lifetime sort and its constrain rule arrive in M4
// D1/D2, so the immutable-borrow and lifetime forms above are reachable only then.
type RefType struct {
	Mut   bool
	Lt    Lifetime // nilable; always nil until the lifetime sort lands (D1)
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
// M4 admits ObjectType / TupleType / TypeVarType. The TypeVarType arm covers a
// borrow whose content is still an inference variable; the content invariant — that
// it resolves to a borrowable type — is checked at constrain time. UnionType /
// IntersectionType (M6 inputs), AliasType (M7), and ClassType (M5) add their
// isRefInner arms with those milestones.
type RefInner interface {
	Type
	isRefInner()
}

func (*ObjectType) isRefInner()  {}
func (*TupleType) isRefInner()   {}
func (*TypeVarType) isRefInner() {}

// PromiseType is the result of an `async fn` and the requirement of an `await`.
// M3 carries it as a dedicated concrete (not a generic TypeRefType), keeping the
// scope narrow: it is the one stdlib generic the milestone needs typed (Iterable/
// Generator wait until M5+). The real, alias-driven `Promise<T>` lookup arrives
// with TypeRef ingestion in M7; until then, an `async fn () -> T` mints a
// PromiseType{T} externally and `await e` constrains `e <: PromiseType{U}` for a
// fresh U. Inner is covariant under subtyping (Promise<L> <: Promise<R> iff
// L <: R) and the `await` rule does NOT recursively flatten (so awaiting
// `Promise<Promise<T>>` yields `Promise<T>`, matching the milestone's
// no-auto-flatten contract — flattening is `Awaited<T>`, M9).
type PromiseType struct{ Inner Type }

// Void is the result type of a statement block with no value.
type Void struct{}

// NeverType (⊥) and UnknownType (⊤) are the bottom/top of the subtype lattice —
// the coalesced output of an empty-bounds single-polarity variable (positive ⇒
// never, negative ⇒ unknown). The spike emits these via type_system; M1 carries
// them natively because they're fundamental to the lattice, not optional sugar.
type NeverType struct{}
type UnknownType struct{}

// UnionType / IntersectionType are coalesced-output nodes for multi-bound
// single-polarity variables (positive ⇒ union of lowers, negative ⇒ intersection
// of uppers). The spike emits these via type_system.NewUnionType /
// NewIntersectionType; M1 carries them natively so coalescing returns
// soltype.Type in every case. Their *subtyping rules* in constrain are M6 —
// these nodes appear only as coalesced output in M1, never as constrain inputs.
type UnionType struct{ Types []Type }
type IntersectionType struct{ Types []Type }

// ErrorType is the error-recovery sentinel (M3 PR8) — a childless atom distinct
// from never (⊥) and unknown (⊤). Unlike those two, which are coalesced-OUTPUT
// only ("appear only as coalesced output, never as constrain inputs", above),
// ErrorType is a legal constrain INPUT that ABSORBS in both directions: any
// constraint with an ErrorType operand trivially succeeds. report (solver) mints
// it as the value-position placeholder after emitting a diagnostic, so the
// placeholder never cascades a second, spurious failure — the standard "error
// type" of TS / Roslyn / GHC. It is never user-spellable (distinct from a future
// `any`) and never produced from user syntax; it renders as `error` for
// diagnostics/debug only.
type ErrorType struct{} // ⊤⊥ absorbing sentinel; see PR8

func (*TypeVarType) isType()      {}
func (*PrimType) isType()         {}
func (*LitType) isType()          {}
func (*FuncType) isType()         {}
func (*TupleType) isType()        {}
func (*ObjectType) isType()       {}
func (*RefType) isType()          {}
func (*PromiseType) isType()      {}
func (*Void) isType()             {}
func (*NeverType) isType()        {}
func (*UnknownType) isType()      {}
func (*UnionType) isType()        {}
func (*IntersectionType) isType() {}
func (*ErrorType) isType()        {}

// LevelOf is the max level of any TypeVarType inside t; concrete leaves are 0.
// Trimmed to the M1 type set (grows back as later milestones add formers).
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
		// Inner is a RefInner, which embeds Type; the borrow wrapper itself carries
		// no variable, so the level is its content's.
		return LevelOf(t.Inner)
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
		// PrimType, LitType, Void, NeverType, UnknownType, ErrorType: childless leaves
		// (ErrorType is a sentinel, level 0).
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
