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

// TuplePat is a tuple destructuring pattern (M4 E1). Its sub-patterns are
// positional. It is carried on a destructured FuncParam.Pattern so the parameter
// renders and round-trips. The solver's pattern-typing helper produces it.
type TuplePat struct{ Elems []Pat }

func (*TuplePat) isPat() {}

// ObjectPat is an object destructuring pattern (M4 E1). Each field names a
// property and binds its value through a sub-pattern. A bare `{x}` shorthand is
// an ObjectPatField whose Value is an IdentPat of the same name.
type ObjectPat struct{ Fields []*ObjectPatField }

func (*ObjectPat) isPat() {}

// ObjectPatField is one `name: subpat` entry of an ObjectPat.
type ObjectPatField struct {
	Name  string
	Value Pat
}

// LitPat matches a literal value (M4 E1). It binds nothing. It is carried for
// rendering and for E2's match-arm typing.
type LitPat struct{ Lit Lit }

func (*LitPat) isPat() {}

// WildcardPat (`_`) matches anything and binds nothing (M4 E1).
type WildcardPat struct{}

func (*WildcardPat) isPat() {}

// ExtractorPat is a constructor/extractor pattern such as `Some(v)` or
// `Point(x, y)`. The solver does not yet produce it; it is a forward-declared
// member of the sealed set, the structural mirror of ast.ExtractorPat, and lands
// with the constructor patterns in M5. Name is the qualified constructor name
// rendered as a string, since soltype stays ast-free. Args are positional
// sub-patterns.
type ExtractorPat struct {
	Name string
	Args []Pat
}

func (*ExtractorPat) isPat() {}

// InstancePat is a class-instance pattern such as `Point { x, y }`. Like
// ExtractorPat it is forward-declared here, the mirror of ast.InstancePat, and
// lands with classes in M5. ClassName is the qualified class name as a string;
// Object is the field sub-pattern.
type InstancePat struct {
	ClassName string
	Object    *ObjectPat
}

func (*InstancePat) isPat() {}

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
// SelfParam carries the implicit `self` receiver of a method, so a method's
// FuncType records its receiver distinctly from its ordinary parameters. Its
// presence marks an instance method and its absence a static method or a plain
// function. Type is the receiver type after desugaring the Rust-style shorthand:
// `self` is `Self`, `mut self` is `mut Self`, `&self` is `&Self`, and `&mut self`
// is `&mut Self`. The printer reads that shape back to the shorthand, and the
// receiver's borrow and lifetime flow through the visitor the same way a
// parameter's do. Pattern names the receiver, always the `self` identifier.
type FuncType struct {
	SelfParam *FuncParam // nil ⇒ static method or plain function; non-nil ⇒ instance method
	Params    []*FuncParam
	Ret       Type
	Inexact   bool // PR4: trailing `...` ⇒ true; bare fn(...) ⇒ false (the exact zero value)
	// TypeParams are the function's own quantified type parameters; nil is monomorphic and
	// a class-level parameter is captured, not listed. LevelOf skips them, being minted deeper.
	TypeParams []*TypeParam
	// LifetimeParams are the function's quantified lifetime parameters, the twin of TypeParams;
	// LevelOf skips them too, though a body lifetime still counts through its RefType.
	LifetimeParams []*LifetimeParam
}

// TypeParam is one quantified type parameter, shared by FuncType.TypeParams for
// function and method generics and by a class's own generics, so classes and functions
// describe their generics the same way. Var is the quantified inference variable that
// stands for the parameter. It is minted one level deeper than the enclosing binding
// and freshened per use by freshenAbove, rather than a named parameter resolved by a
// substitution pass. The declared constraint is Var's upper bound. `<U extends T>` sets
// Var.UpperBounds to [T], so constrain and freshenAbove enforce and copy it with no new
// machinery. Default is the type filled in when a type argument is omitted. It is nil
// when the parameter is required. Type-argument resolution reads it, and constraint
// solving ignores it. Name is the source name kept for display, since TypeVarType
// carries none.
type TypeParam struct {
	Name    string
	Var     *TypeVarType
	Default Type // nil ⇒ required
}

// LifetimeParam is one quantified lifetime parameter, the lifetime-sort analogue of TypeParam,
// shared by a function's or class's own lifetime params such as `fn get<'a>` and `class Ref<'a, T>`.
// Var is minted one level deeper and freshened per use; Bounds are the outlives constraints, where
// `<'b: 'a>` gives 'b the single bound 'a. Name keeps the source name for display.
type LifetimeParam struct {
	Name   string
	Var    *LifetimeVar
	Bounds []Lifetime // outlives constraints; nil ⇒ unconstrained
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
	Readonly bool // `readonly f: T`; forbids `obj.f = …` only, orthogonal to deep mut
}

func (*PropertyElem) isObjTypeElem() {}

// MethodElem is one named method of an ObjectType. Its signature is a FuncType
// whose first parameter is the `self` receiver, so member lookup and subtyping
// reuse the FuncType machinery with no method-specific path. An overloaded method
// holds its arms in Signatures, ordered most-specific-first the way the solver
// resolves an overload set. A plain method has exactly one signature. Static marks
// a static method, which lives on the constructor value rather than the instance.
type MethodElem struct {
	Name       string
	Signatures []*FuncType // len 1 = ordinary; >1 = overload set (most-specific-first)
	Static     bool
}

// GetterElem is a computed read property `get x(self) -> T`. Type is the value the
// getter returns, read covariantly like a PropertyElem's Type. SelfParam is the
// receiver of an instance getter and nil for a static getter, mirroring
// FuncType.SelfParam.
type GetterElem struct {
	Name      string
	SelfParam *FuncParam // nil ⇒ static getter; non-nil ⇒ instance getter
	Type      Type
}

// SetterElem is a computed write property `set x(self, v: T)`. Param is the value the
// setter accepts, in write position, so it is read contravariantly. SelfParam is the
// receiver of an instance setter and nil for a static setter, mirroring
// FuncType.SelfParam.
type SetterElem struct {
	Name      string
	SelfParam *FuncParam // nil ⇒ static setter; non-nil ⇒ instance setter
	Param     Type
}

// ConstructorElem is the call signature a class value carries. It is the constructor a
// class name resolves to as a value, so `Point(1, 2)` calls Fn. A class value holds
// exactly one, unnamed, alongside the class's static members. It is the single callable
// element the structural lattice admits, scoped to the class-value carrier rather than a
// general call-signature-in-any-object feature.
type ConstructorElem struct{ Fn *FuncType }

func (*MethodElem) isObjTypeElem()      {}
func (*GetterElem) isObjTypeElem()      {}
func (*SetterElem) isObjTypeElem()      {}
func (*ConstructorElem) isObjTypeElem() {}

// ObjElemName returns the member name of any ObjTypeElem kind. It is the shared
// name accessor for member lookup and structural equality, so those sites need no
// per-kind type switch of their own. A ConstructorElem is unnamed, so it returns the
// empty string. No source-derived member carries that name, so a name lookup never
// matches a constructor. Two constructors still pair up under the shared empty key when
// their objects are compared. It panics on an unknown element kind, matching the
// loud-fail discipline of AsProperty.
func ObjElemName(e ObjTypeElem) string {
	switch e := e.(type) {
	case *PropertyElem:
		return e.Name
	case *MethodElem:
		return e.Name
	case *GetterElem:
		return e.Name
	case *SetterElem:
		return e.Name
	case *ConstructorElem:
		return ""
	}
	panic(fmt.Sprintf("ObjElemName: unhandled ObjTypeElem %T", e))
}

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

// Member returns the named member of any kind and whether it is present. It
// generalizes Prop across property, method, getter, and setter elements, so member
// access and nominal subtyping look a name up through one call regardless of its
// kind. When a name is carried by both a getter and a setter it returns the first
// in declaration order. A caller that must distinguish the two inspects the returned
// element's concrete kind.
func (o *ObjectType) Member(name string) (ObjTypeElem, bool) {
	for _, e := range o.Elems {
		if ObjElemName(e) == name {
			return e, true
		}
	}
	return nil, false
}

// Constructor returns the object's constructor call signature and whether it carries
// one. A class value carries exactly one ConstructorElem, so this is the lookup a call
// site and the nominal-value constrain rule use to reach the constructor without a
// type switch of their own.
func (o *ObjectType) Constructor() (*ConstructorElem, bool) {
	for _, e := range o.Elems {
		if ctor, ok := e.(*ConstructorElem); ok {
			return ctor, true
		}
	}
	return nil, false
}

// AsProperty narrows an ObjTypeElem to its *PropertyElem. It is used at sites that
// handle only properties and do not yet process the method, getter, and setter
// kinds, so any other element reaching one is a wiring bug: a member kind added
// without extending that call site. It panics rather than silently skipping, so a
// missed element kind fails loudly instead of vanishing from subtyping, equality,
// or rendering. This matches type_system's convention, where print_type.go panics
// on an unhandled ObjTypeElem. Use it only at property-only sites. A site that must
// visit every element kind switches on the kind instead. Name lookups like Prop
// legitimately skip non-matching kinds.
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
// and Lt for the lifetime outlives check. M4 D1 lands the Lifetime sort (the
// LifetimeVar/StaticLifetime concretes, freshLifetime, constrainLt, the probe
// extension); D2 attaches a fresh lifetime to a borrowed parameter and activates
// the rule's outlives step. Until D2 every minted RefType still carries Lt == nil,
// so the immutable-borrow and lifetime forms above are only reached once D2 lands.
type RefType struct {
	Mut   bool
	Lt    Lifetime // nilable; carries a lifetime once D2 attaches one to a borrow
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
// it resolves to a borrowable type — is checked at constrain time. UnionType and
// IntersectionType join as RefInner so `&(A | B)` is one borrow over a union
// pointee, with a single lifetime and mutability for the whole value rather than
// `&A | &B` with independent lifetimes. A union or intersection must have uniform
// ownership. A borrowed member beside an owned one has no single owned-or-borrowed
// verdict and is rejected at the inference join where it forms. ClassType is a
// RefInner too, so a `mut 'a Point` borrows a class instance. AliasType is a
// RefInner as well, so a `mut 'a Point` over a type alias borrows through the same
// machinery; the lifetime-generic alias form lands in M7 PR4.
type RefInner interface {
	Type
	isRefInner()
}

func (*ObjectType) isRefInner()       {}
func (*TupleType) isRefInner()        {}
func (*TypeVarType) isRefInner()      {}
func (*UnionType) isRefInner()        {}
func (*IntersectionType) isRefInner() {}
func (*ClassType) isRefInner()        {}
func (*AliasType) isRefInner()        {}

// PromiseType is the result of an `async fn` and the requirement of an `await`.
// M3 carries it as a dedicated concrete (not a generic TypeRefType), keeping the
// scope narrow: it is the one stdlib generic the milestone needs typed (Iterable/
// Generator wait until M5+). The real, alias-driven `Promise<T>` lookup arrives
// with library type ingestion in M7.5 — the alias/`TypeRef` resolution machinery
// it uses lands in M7, the real stdlib structure in M7.5; until then, an
// `async fn () -> T` mints a
// PromiseType{T} externally and `await e` constrains `e <: PromiseType{U}` for a
// fresh U. Inner is covariant under subtyping (Promise<L> <: Promise<R> iff
// L <: R) and the `await` rule does NOT recursively flatten (so awaiting
// `Promise<Promise<T>>` yields `Promise<T>`, matching the milestone's
// no-auto-flatten contract — flattening is `Awaited<T>`, M9).
type PromiseType struct{ Inner Type }

// Void is the result type of a statement block with no value.
type Void struct{}

// NullType is the type whose only inhabitant is the `null` literal. It
// mirrors TypeScript's `null` type and sits alongside Void as a distinct
// atomic kind. The canonical comparator sorts both kinds last so a union
// such as `T | null | void` consistently renders with the data members
// first.
type NullType struct{}

// UndefinedType is the type whose only inhabitant is `undefined`, the atomic
// twin of NullType. Reading a property off a union where only some members
// carry it joins `undefined` for the members that lack it, so the read
// resolves to `T | undefined` (M5 D4). No source syntax produces it yet; it is
// minted internally by that join and renders as `undefined`.
type UndefinedType struct{}

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
// soltype.Type in every case. M6 promotes them to first-class lattice members:
// legal `constrain` inputs (M6 PR2), writable annotations (M6 PR2), and the
// subjects of a normalization pass (M6 PR1).
//
// UnionType.Inexact flags whether the union is open. A bare `A | B` is
// exact, so its inhabitants are exactly A ∪ B. An `A | B | ...` written with
// a trailing `...` is inexact: at least these, with an unknown-typed tail.
// The flag is Inexact rather than Exact so the zero value is exact, matching
// the ObjectType, TupleType, and FuncType convention. IntersectionType
// carries no exactness flag, since exactness is a property of the result
// rather than the meet. The flag and the smart constructors land with M6 PR1.
type UnionType struct {
	Types []Type
	// Inexact tracks the trailing `...` marker. The zero value is exact.
	Inexact bool
}
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

// ClassType is a nominal lattice element — the identity token for a class. Two
// ClassTypes are the same nominal type when their Name matches. The heavy per-class
// data — the projected member body, the resolved supers, and the inferred variance —
// lives in a side registry keyed by Name, so this token stays small and cheap to
// compare and rewrite.
type ClassType struct {
	// Name is the dep_graph-qualified name such as "Geometry.Point", not the bare
	// local identifier, so two classes named Point in different namespaces stay
	// distinct. It also keys the registry holding the heavy per-class data.
	Name string
	// TypeArgs are the type arguments, one per class type parameter, checked per position
	// by the variance the class registry records for that parameter.
	TypeArgs []Type
	// LifetimeArgs are the lifetime arguments, one per class lifetime parameter, so `Ref<'x, T>`
	// supplies arg 'x. They name the lifetime of borrowed data the instance holds, distinct from Lt.
	LifetimeArgs []Lifetime
	// Lt is the instance's own borrow lifetime, nil for an owned value. A `mut 'b
	// Point` wraps a ClassType in a RefType rather than setting Lt directly, so no
	// site sets Lt today and it is always nil.
	Lt Lifetime
	// Final marks a class whose subclasses cannot add members, so its instance type is
	// closed the way an exact object is (exact-types §2.6). The zero value false is
	// inexact, matching a non-final class whose subclasses may widen it.
	Final bool
	// Variant marks an enum variant such as `Color.RGB`, so it renders qualified by its
	// enum — the last two components of Name — rather than stripped to the bare `RGB` a
	// class renders under. This keeps two enums that share a variant name distinct at
	// display time. The zero value false is an ordinary class or the enum type itself.
	Variant bool
}

// AliasType is the use-site reference to a user-written `type Name<Params…> = Body`
// declaration. Like ClassType it is a small token. Name keys a side registry holding
// the heavy Body, so the token stays cheap to compare and rewrite. An alias is
// transparent, meaning it subtypes exactly as its expanded Body does, so the subtyping
// engine expands it through the alias registry rather than comparing the token
// nominally. A reference renders under Name, so `type Point = {x: number}` followed by
// `val p: Point = …` shows `Point`, not the expanded record.
//
// M7 PR4 adds a LifetimeArgs field for a lifetime-generic alias; until then a
// reference carries type arguments only.
type AliasType struct {
	// Name is the dep_graph-qualified name such as "Geometry.Point", not the bare local
	// identifier, so two aliases named Point in different namespaces stay distinct. It
	// also keys the registry holding the alias's Body.
	Name string
	// TypeArgs are the type arguments a generic reference supplies, one per alias type
	// parameter. Empty for a bare reference such as `Point`. The generic-instance and
	// substitution machinery lands in M7 PR2; PR1 mints only the empty-args form.
	TypeArgs []Type
}

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
func (*UndefinedType) isType()    {}
func (*NeverType) isType()        {}
func (*UnknownType) isType()      {}
func (*UnionType) isType()        {}
func (*IntersectionType) isType() {}
func (*ErrorType) isType()        {}
func (*ClassType) isType()        {}
func (*AliasType) isType()        {}

// LevelOf is the max level of any TypeVarType inside t; concrete leaves are 0.
// Trimmed to the M1 type set (grows back as later milestones add formers).
func LevelOf(t Type) int {
	switch t := t.(type) {
	case *TypeVarType:
		return t.Level
	case *FuncType:
		m := 0
		if t.SelfParam != nil {
			m = LevelOf(t.SelfParam.Type)
		}
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
			m = max(m, levelOfElem(e))
		}
		return m
	case *ClassType:
		// A nominal instance's level is the max level over its type and lifetime
		// arguments and its own borrow lifetime. The Name and Final identity carry no
		// variables.
		m := 0
		for _, a := range t.TypeArgs {
			m = max(m, LevelOf(a))
		}
		// A free lifetime arg such as the 'x in Ref<'x, T> must lift the level so the
		// freshener/extruder prune descends to freshen it, the same reason the RefType
		// arm folds in its lifetime.
		for _, la := range t.LifetimeArgs {
			m = max(m, LevelOfLifetime(la))
		}
		return max(m, LevelOfLifetime(t.Lt))
	case *AliasType:
		// An alias reference's level is the max level over its type arguments; the Name
		// carries no variables. A bare reference with no arguments is level 0.
		m := 0
		for _, a := range t.TypeArgs {
			m = max(m, LevelOf(a))
		}
		return m
	case *PromiseType:
		return LevelOf(t.Inner)
	case *RefType:
		// A borrow's level is the max of its inner content's and its lifetime's (M4
		// D2.5). The lifetime is a SECOND quantifiable variable on the wrapper: a
		// concrete-inner borrow whose lifetime var sits above a freshen/extrude limit
		// must NOT be pruned and shared whole, or two instantiations would alias one
		// LifetimeVar. Folding the lifetime level in here is the load-bearing change
		// that makes the level prune descend into such a borrow to freshen its
		// lifetime. LevelOfLifetime returns 0 for 'static and a nil slot.
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
		// PrimType, LitType, Void, NullType, UndefinedType, NeverType, UnknownType,
		// ErrorType: childless leaves. ErrorType is a sentinel at level 0.
		return 0
	}
}

// levelOfElem returns the max TypeVarType level across an object member's types, the
// per-element generalization of LevelOf's property case. A method's receiver rides
// inside each signature FuncType; a getter or setter carries its receiver directly,
// present only for an instance member. It panics on an unknown element kind, matching
// AsProperty.
func levelOfElem(e ObjTypeElem) int {
	switch e := e.(type) {
	case *PropertyElem:
		return LevelOf(e.Type)
	case *MethodElem:
		m := 0
		for _, sig := range e.Signatures {
			m = max(m, LevelOf(sig))
		}
		return m
	case *GetterElem:
		return max(selfLevel(e.SelfParam), LevelOf(e.Type))
	case *SetterElem:
		return max(selfLevel(e.SelfParam), LevelOf(e.Param))
	case *ConstructorElem:
		return LevelOf(e.Fn)
	}
	panic(fmt.Sprintf("levelOfElem: unhandled ObjTypeElem %T", e))
}

// selfLevel returns the level of a getter's or setter's receiver type, 0 for a static
// member whose receiver is nil.
func selfLevel(self *FuncParam) int {
	if self == nil {
		return 0
	}
	return LevelOf(self.Type)
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
