package soltype

import (
	"fmt"
	"strconv"
)

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

// SkolemType is a rigid type parameter held abstract while a term is checked against a
// polymorphic expected type. It is a CONCRETE atomic type, so constrain compares it
// nominally and no concrete type is a subtype of it. A skolem is a subtype only of itself,
// of an inference var it flows into, and of its declared upper bound. ID keeps two
// parameters `T` and `U` distinct; Name is the source name for diagnostics and the printer;
// Upper is the declared constraint (`<U: T>`), nil when unconstrained.
type SkolemType struct {
	ID    int
	Name  string
	Upper Type
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

// NullPat matches the value `null` and binds nothing. Its type is NullType, an
// atom rather than a member of Lit, so LitPat has no field that can carry it and
// `null` needs a pattern of its own.
type NullPat struct{}

func (*NullPat) isPat() {}

// UndefinedPat matches the value `undefined` and binds nothing, the twin of
// NullPat for the UndefinedType atom.
type UndefinedPat struct{}

func (*UndefinedPat) isPat() {}

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
	// Throws is the type a call may raise, the twin of Ret for the exceptional exit and
	// covariant like it. Nil reads as `never`, so the zero value is non-throwing;
	// ThrowsOrNever collapses nil and an explicit `never` so no reader tells them apart.
	Throws  Type
	Inexact bool // PR4: trailing `...` ⇒ true; bare fn(...) ⇒ false (the exact zero value)
	// TypeParams are the function's own quantified type parameters; nil is monomorphic and
	// a class-level parameter is captured, not listed. LevelOf skips them, being minted deeper.
	TypeParams []*TypeParam
	// LifetimeParams are the function's quantified lifetime parameters, the twin of TypeParams;
	// LevelOf skips them too, though a body lifetime still counts through its RefType.
	LifetimeParams []*LifetimeParam
}

// ThrowsOrNever returns the type a call to t may raise, resolving the nil shorthand to the
// `never` it stands for, so no reader of the throws position has to test for nil.
func (t *FuncType) ThrowsOrNever() Type {
	if t.Throws == nil {
		return &NeverType{}
	}
	return t.Throws
}

// TypeParam is one quantified type parameter, shared by FuncType.TypeParams for
// function and method generics and by a class's own generics, so classes and functions
// describe their generics the same way. Var is the quantified inference variable that
// stands for the parameter. It is minted one level deeper than the enclosing binding
// and freshened per use by freshenAbove, rather than a named parameter resolved by a
// substitution pass. The declared constraint is seeded as Var's upper bound. `<U: T>` sets
// Var.UpperBounds to [T], so constrain and freshenAbove enforce and copy it with no new
// machinery. Default is the type filled in when a type argument is omitted. It is nil
// when the parameter is required. Type-argument resolution reads it, and constraint
// solving ignores it. Name is the source name kept for display, since TypeVarType
// carries none.
//
// Constraint is that same declared constraint kept where solving cannot overwrite it. A
// variable's upper-bound list grows whenever a constraint flows into the variable, so
// Var.UpperBounds[0] is the declared constraint only for a parameter that solving has not
// touched. `fn g<U>(u: U) { f(u) }` calling `fn f<A: string>(a: A)` appends string to the
// unbounded U, which puts an inferred bound at index 0. Constraint stays nil for that U,
// because a parameter written with no `:` clause declares nothing. Read Constraint, not
// Var.UpperBounds, to answer what the source wrote.
type TypeParam struct {
	Name       string
	Var        *TypeVarType
	Default    Type // nil ⇒ required
	Constraint Type // nil ⇒ unbounded
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
	// Throws is the type reading this property may raise, the twin of FuncType.Throws
	// and covariant like it. Nil reads as `never`, so the zero value is non-throwing.
	Throws Type
}

// ThrowsOrNever returns the type reading e may raise, resolving the nil shorthand to the
// `never` it stands for, so no reader of the throws position has to test for nil.
func (e *GetterElem) ThrowsOrNever() Type {
	if e.Throws == nil {
		return &NeverType{}
	}
	return e.Throws
}

// SetterElem is a computed write property `set x(self, v: T)`. Param is the value the
// setter accepts, in write position, so it is read contravariantly. SelfParam is the
// receiver of an instance setter and nil for a static setter, mirroring
// FuncType.SelfParam.
type SetterElem struct {
	Name      string
	SelfParam *FuncParam // nil ⇒ static setter; non-nil ⇒ instance setter
	Param     Type
	// Throws is the type writing this property may raise. It is covariant even though
	// Param is contravariant. What a write raises flows out to the writer, just as what a
	// getter raises flows out to the reader. Nil reads as `never`.
	Throws Type
}

// ThrowsOrNever returns the type writing e may raise, resolving the nil shorthand to the
// `never` it stands for, so no reader of the throws position has to test for nil.
func (e *SetterElem) ThrowsOrNever() Type {
	if e.Throws == nil {
		return &NeverType{}
	}
	return e.Throws
}

// ConstructorElem is the call signature a class value carries. It is the constructor a
// class name resolves to as a value, so `Point(1, 2)` calls Fn. A class value holds
// exactly one, unnamed, alongside the class's static members. It is the single callable
// element the structural lattice admits, scoped to the class-value carrier rather than a
// general call-signature-in-any-object feature.
type ConstructorElem struct{ Fn *FuncType }

// SpreadElem is a `...A` object spread written as an element of an ObjectType, the object twin of
// the tuple's RestSpreadType (M9 PR5). `{...A, x: T}` is an ObjectType whose first element is a
// SpreadElem over A. An object carrying one is a residual: constrain passes it through untouched,
// the inert contract KeyofType shares, until the evaluator grounds Type to an object and merges its
// fields in at this position. A reduced object has no SpreadElem. A spread over a type parameter
// never grounds, so it stays symbolic and renders `...A`. Type is the spread source.
type SpreadElem struct{ Type Type }

func (*MethodElem) isObjTypeElem()      {}
func (*GetterElem) isObjTypeElem()      {}
func (*SetterElem) isObjTypeElem()      {}
func (*ConstructorElem) isObjTypeElem() {}
func (*MappedElem) isObjTypeElem()      {}
func (*SpreadElem) isObjTypeElem()      {}

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
	case *SpreadElem:
		// A spread is anonymous. It only appears in an unreduced object, and the name-keyed
		// equality and lookup paths compare such objects positionally instead, so this name is
		// never a match key.
		return ""
	case *MappedElem:
		// A mapped member names no single field; it stands for the whole computed member list. Like
		// a spread it only appears in an unreduced object, compared positionally rather than by
		// name, so this name is never a match key either.
		return ""
	}
	panic(fmt.Sprintf("ObjElemName: unhandled ObjTypeElem %T", e))
}

// HasObjectSpread reports whether an element list carries a `...A` spread, so the object is an
// unreduced residual rather than a concrete object. It is the object twin of hasRestSpread on the
// tuple side.
func HasObjectSpread(elems []ObjTypeElem) bool {
	for _, e := range elems {
		if _, ok := e.(*SpreadElem); ok {
			return true
		}
	}
	return false
}

// HasMappedElem reports whether an element list carries a `[K]: V for K in Keys` member, so the
// object is an unreduced residual rather than a concrete object. Such a member is always the only
// one in its list, since resolveObjectTypeAnn rejects an object mixing it with ordinary members.
func HasMappedElem(elems []ObjTypeElem) bool {
	for _, e := range elems {
		if _, ok := e.(*MappedElem); ok {
			return true
		}
	}
	return false
}

// HasResidualElem reports whether an element list makes its object an unreduced residual, carrying
// either a `...A` spread whose operand may still merge or a `[K]: V for K in Keys` member whose key
// set may still ground. Such an object is never decomposed structurally, since its final member list
// is not yet known; the evaluator reduces it first and constrain compares it inertly until then.
func HasResidualElem(elems []ObjTypeElem) bool {
	return HasObjectSpread(elems) || HasUnsettledMapped(elems)
}

// HasUnsettledMapped reports whether an element list carries a mapped member that reduction has not
// finished with. It is the mapped half of HasResidualElem. It also decides whether an object counts
// as ground, since an object whose every mapped member is settled has no reduction left to do.
func HasUnsettledMapped(elems []ObjTypeElem) bool {
	for _, e := range elems {
		if m, ok := e.(*MappedElem); ok && !MappedElemSettled(m) {
			return true
		}
	}
	return false
}

// MappedElemSettled reports whether reducing a mapped member again would change nothing. Only an
// index signature that adds `?` qualifies; everything else, the required form included, still reduces.
func MappedElemSettled(m *MappedElem) bool {
	return IsIndexSignature(m) && m.Optional == ModAdd
}

// IndexSignatures returns the object's index signatures in source order. An object may carry more
// than one, each over a different key set, as `{[K: string]?: number, [J: number]?: boolean}` does.
// Which one describes a given key is an assignability question, so the caller picks by probing each
// signature's key set rather than taking the first.
func (o *ObjectType) IndexSignatures() []*MappedElem {
	var sigs []*MappedElem
	for _, e := range o.Elems {
		if m, ok := e.(*MappedElem); ok && MappedElemSettled(m) {
			sigs = append(sigs, m)
		}
	}
	return sigs
}

// AsMapped returns the mapped member of an element list and whether one is present. A caller that
// has already checked HasMappedElem uses it to reach the member without repeating the type switch.
func AsMapped(elems []ObjTypeElem) (*MappedElem, bool) {
	for _, e := range elems {
		if m, ok := e.(*MappedElem); ok {
			return m, true
		}
	}
	return nil, false
}

// UncountableKeys reports whether a key set names infinitely many keys: a `string`/`number` prim or
// a union holding one. The caller must ground it first, since an abstract operand reads as countable.
func UncountableKeys(t Type) bool {
	switch t := t.(type) {
	case *PrimType:
		return t.Prim == StrPrim || t.Prim == NumPrim
	case *UnionType:
		for _, member := range t.Types {
			if UncountableKeys(member) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// MappedShorthandForm reports whether a mapped member renders as `[Key: Keys]` rather than with a
// trailing `for Key in Keys`. Only a key remapping rules it out, since it occupies the brackets.
func MappedShorthandForm(m *MappedElem) bool {
	return m.Name == nil
}

// IsIndexSignature reports whether a mapped member is one, as in `{[K: string]?: number}`: an
// uncountable key set with no remapping or filter. Carries UncountableKeys' grounding precondition.
func IsIndexSignature(m *MappedElem) bool {
	return m.Name == nil && m.Check == nil && m.Extends == nil && UncountableKeys(m.Keys)
}

// MappedOptionalOperands returns the operands a mapped member carries only when the source wrote
// them, skipping the absent ones. The key-remapping expression and the two filter operands each
// have a nil form, so every walk over them shares this one nil-filtering step.
func MappedOptionalOperands(m *MappedElem) []Type {
	operands := make([]Type, 0, 3)
	for _, operand := range []Type{m.Name, m.Check, m.Extends} {
		if operand != nil {
			operands = append(operands, operand)
		}
	}
	return operands
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

// ReadMember returns the member a READ of name resolves to. When a getter and a
// setter share the name, the getter is the half a read wants, so it wins over
// declaration order. `class C { set x(mut self, n: number) {…}, get x(self) -> number
// {…} }` read through `c.x` therefore yields the getter's `number` rather than the
// setter Member would return. Every other member kind resolves the same as Member.
//
// A setter-only name still resolves HERE, to the setter. This is not a claim that the
// member is readable. It is what lets a caller tell a name that is absent from one that
// is present but exposes no read, so `memberValue` can report `Property 'x' is
// write-only` instead of the generic missing-property error a not-found would produce.
func (o *ObjectType) ReadMember(name string) (ObjTypeElem, bool) {
	return o.preferredMember(name, func(e ObjTypeElem) bool {
		_, ok := e.(*GetterElem)
		return ok
	})
}

// WriteMember returns the member a WRITE of name resolves to, the mirror of
// ReadMember. When a getter and a setter share the name, the setter is the half a
// write wants. `class C { get x(self) -> number {…}, set x(mut self, n: number) {…} }`
// written through `c.x = 5` therefore checks 5 against the setter's parameter rather
// than against the getter Member would return.
//
// A getter-only name resolves here to the getter, the mirror of ReadMember resolving a
// setter-only name. That is what lets `writeAccessor` report `Property 'x' is read-only`
// rather than the generic missing-property error a not-found would produce.
func (o *ObjectType) WriteMember(name string) (ObjTypeElem, bool) {
	return o.preferredMember(name, func(e ObjTypeElem) bool {
		_, ok := e.(*SetterElem)
		return ok
	})
}

// preferredMember returns the first element named name that satisfies preferred, or
// the first element named name at all when none does. It is the shared scan behind
// ReadMember and WriteMember, which differ only in which half of a getter/setter pair
// they prefer.
//
// The preference is a tie-break between two elements sharing a name, not a filter. Both
// callers pass a predicate matching a single accessor kind, so without the fallback
// ReadMember would return only getters and every property and method read would resolve
// to nothing. The fallback is therefore what carries the ordinary members, and returning
// the opposite accessor half for an accessor-only name follows from the same rule.
func (o *ObjectType) preferredMember(name string, preferred func(ObjTypeElem) bool) (ObjTypeElem, bool) {
	var first ObjTypeElem
	for _, e := range o.Elems {
		if ObjElemName(e) != name {
			continue
		}
		if preferred(e) {
			return e, true
		}
		if first == nil {
			first = e
		}
	}
	return first, first != nil
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
// machinery. RecursiveType joins them for the same reason an AliasType does: a μ-knot over an
// object is a borrowable value, and admitting it keeps RefType.Accept from peeling the `mut`
// wrapper off a borrow whose inner coalesced to a knot.
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
func (*RecursiveType) isRefInner()    {}

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
//
// Err is the type the promise rejects with, the twin of FuncType.Throws for the
// asynchronous exceptional exit. What an `async fn`'s body throws lands here rather
// than on the function's own Throws, since a caller observes the rejection only by
// awaiting the promise. Like Inner it is covariant, and like FuncType.Throws a nil
// Err is shorthand for `never`; ErrOrNever collapses nil and an explicit `never`
// so no reader tells them apart. A promise that cannot reject is therefore the
// zero value and renders as the one-argument `Promise<T>`.
type PromiseType struct {
	Inner Type
	Err   Type
}

// ErrOrNever returns the type t may reject with, resolving the nil shorthand to the
// `never` it stands for, so callers compare one canonical value.
func (t *PromiseType) ErrOrNever() Type {
	if t.Err == nil {
		return &NeverType{}
	}
	return t.Err
}

// Rejects reports whether t can reject: its Err carries something other than the nil
// shorthand or an explicit `never`. The printer and the solver's raised-tracking both
// ask this question, so it lives here to keep their answers locked together.
func (t *PromiseType) Rejects() bool {
	return t.Err != nil && !isNever(t.Err)
}

// GeneratorType is the external face of a `gen fn`: calling one returns a generator
// object rather than the body's value. It is a dedicated concrete for the reason
// PromiseType is: one stdlib generic the milestone needs typed ahead of library
// ingestion in M7.5. Yield is the union of the types the body's `yield` expressions
// produce, Ret is the body's return type, and Next is the type a `yield` expression
// evaluates to, the value a caller passes back in through `next(v)`. Yield and Ret
// are covariant; Next is contravariant, since it is an input the way a parameter is.
// Async distinguishes an `async gen fn`'s AsyncGenerator from a sync Generator; the
// two are unrelated under subtyping. All three type fields are always non-nil.
type GeneratorType struct {
	Yield Type
	Ret   Type
	Next  Type
	Async bool
}

// Name returns the stdlib name t renders under, `Generator` or `AsyncGenerator`,
// so the printer and diagnostics spell the async split the same way.
func (t *GeneratorType) Name() string {
	if t.Async {
		return "AsyncGenerator"
	}
	return "Generator"
}

// ArrayType is a homogeneous sequence of Elem, written `Array<T>`. It is a dedicated concrete
// for the reason PromiseType is: one stdlib generic the milestone needs typed ahead of library
// ingestion. It exists to give a rest parameter an element type, the arity-and-element pair a
// tuple-typed rest cannot express. Elem is covariant, the read-only reading a rest parameter
// needs. The minimal form carries no members, so `xs.length` and `xs[0]` do not resolve.
type ArrayType struct{ Elem Type }

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

// ClassType is a nominal lattice element — the nominal handle for a class. Two
// ClassTypes are the same nominal type when their Name matches. The heavy per-class
// data — the projected member body, the resolved supers, and the inferred variance —
// lives in a side registry keyed by Name, so this handle stays small and cheap to
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

// AliasType is the use-site reference to a `type Name = Body` declaration, a small handle
// whose Name keys the Body in a side registry, like ClassType. An alias is transparent:
// the subtyping engine expands it to its Body, while it renders under Name.
type AliasType struct {
	// Name is the dep_graph-qualified name such as "Geometry.Point", so two aliases named
	// Point in different namespaces stay distinct. It also keys the registry.
	Name string
	// TypeArgs are the type arguments a generic reference supplies, one per alias type
	// parameter, substituted into the body at expansion. A non-generic reference carries none.
	TypeArgs []Type
	// LifetimeArgs are the lifetime arguments a lifetime-generic reference supplies, one per
	// alias lifetime parameter, so `Foo<'a>` and `Foo<'b>` are distinct nodes rather than
	// colliding. They join Name and TypeArgs in the instance identity the recursion guard and
	// M9's cycle cache key on, so two borrows of one alias at different lifetimes never share a
	// cache entry. A borrow's own lifetime rides a RefType wrapper, not this field.
	LifetimeArgs []Lifetime
}

// KeyofType is the residual `keyof Operand` type operator (M9 PR1a), the first of
// the type-level operators. It is inert: it carries no bounds, constrain never
// records one against it, and it flows through the solver's structural machinery
// untouched — the "adds no new mutable solver state" invariant it shares with the
// spike's ResidualOp. An evaluator reduces it once its operand is ground (M9 PR1b);
// until then a `keyof T` over a type parameter stays symbolic and renders `keyof T`.
// Inexact records whether the operand's key set is open, the seed for exactness
// propagation through reduction (M9 PR8). The flag is Inexact rather than Exact so the
// zero value is exact, matching the ObjectType, TupleType, FuncType, and UnionType
// convention. It is carried through the visitor and left unread until that work lands.
type KeyofType struct {
	Operand Type
	Inexact bool
}

// IndexType is the residual `Target[Index]` indexed-access type operator (M9 PR2), the
// sibling of KeyofType. Like KeyofType it is inert: it carries no bounds, constrain never
// records one against it, and it flows through the solver's structural machinery untouched.
// An evaluator reduces it once its operands are ground — `{x: number}["x"]` ⇒ `number`, a
// tuple `[a, b][0]` ⇒ `a`, and a union index distributes so `T["a" | "b"]` ⇒ `T["a"] |
// T["b"]`. Until then a `T[K]` over a type parameter stays symbolic and renders `T[K]`.
// Inexact records whether the target's member set is open, the seed for exactness
// propagation through reduction (M9 PR8). It follows KeyofType's convention, so the zero
// value is exact. It is carried through the visitor and left unread until that work lands.
type IndexType struct {
	Target  Type
	Index   Type
	Inexact bool
}

// TypeofType is the residual `typeof x` type query. Like KeyofType it is inert: it carries no
// bounds, constrain never records one against it, and it flows through the solver's structural
// machinery untouched, rendering `typeof <Ident>` the way the source wrote it. Ty is the queried
// value's type, resolved at annotation time, and is what constrain compares against when it
// decides a constraint on the query, the transparent-but-named treatment an alias gets. Ident is
// the value reference for display, such as "x" or "p.inner".
type TypeofType struct {
	Ident string
	Ty    Type
}

// CondType is the residual `if Check : Extends { Then } else { Else }` conditional type operator.
// Like KeyofType it is inert: it carries no bounds, constrain never records one against it, and it
// flows through the solver's structural machinery untouched, rendering the way the source wrote it.
// An evaluator reduces it once Check and Extends are ground. It decides `Check <: Extends` with an
// assignability probe and reduces to Then on success or Else on failure. Until then a conditional
// over a type parameter stays symbolic.
//
// An Extends carrying an `infer U` binder is matched structurally against Check first, so each
// binder captures the type at its position and the Then branch reads those captures. See InferType.
type CondType struct {
	Check   Type
	Extends Type
	Then    Type
	Else    Type
	// Distribute marks a conditional whose Check was written as a bare type-parameter reference,
	// such as the `T` of `type Wrap<T> = if T : string { [T] } else { boolean }`. It mirrors
	// TypeScript's naked-type-parameter rule, stated here in full since two separate conditions
	// decide whether a union Check is taken apart, and both must hold:
	//
	//  1. The Check was written as a bare parameter name, which is what this flag records. Every
	//     other written form leaves it clear, `[T]`, `{value: T}`, and `Other<T>` alike.
	//  2. The Check reduced to a union. A conditional over a single type has nothing to take apart,
	//     so the flag on its own changes no reduction.
	//
	// When both hold, the conditional decides one member at a time and unions the results. Every
	// position that named the parameter reads the member rather than the whole union, the Extends
	// operand and both branches alike, so `Wrap<"a" | 1>` reduces to `["a"] | boolean`.
	//
	// When either fails, the conditional decides once over the whole union. That is how a user
	// captures a union as a single type: `if [T] : [infer U] { [U] } else { "no" }` binds U to the
	// whole union and yields `[1 | "a"]` for `"a" | 1`, where the same alias written `if T : [infer
	// U]` over `["a"] | [1]` binds U per member and yields `[1] | ["a"]`.
	//
	// A body that needs both a Check it cannot write bare and distribution can wrap itself in
	// `if T : T { … } else { … }`, whose own Check is bare. The wrapper distributes and each member
	// runs the inner conditional on its own. Its Else branch is unreachable, since a member is
	// always a subtype of itself.
	Distribute bool
}

// InferType is the `infer U` clause a conditional's Extends operand declares, and the reference to
// that name from the conditional's Then branch. The evaluator stands a fresh inference variable in
// for each clause, lets the `Check <: Extends` constraint infer what that variable holds, and
// substitutes the result before it reduces the selected branch.
//
// ID is the declaration the two forms share. The clause and every reference to it carry one id, so
// substituting a captured type reaches exactly the positions that declaration stands at. A nested
// conditional writing the same name draws a distinct id, which is what makes its clause and
// references a separate declaration that shadows the enclosing one.
//
// Name is the source name, carried for display. Binder marks the declaring clause, which renders
// `infer U` where a reference renders the bare `U`, so a stored conditional prints
// `if T : [infer U] { U } else { boolean }` the way the source wrote it.
//
// It is a leaf with no bounds, and nothing constrains against it. A conditional whose captures no
// match has filled has not decided its branch, so the whole conditional is still inert.
type InferType struct {
	ID     int
	Name   string
	Binder bool
}

// WildcardInferName is the Name an anonymous `infer` binder carries — the one a `_` written in a
// conditional's Extends operand mints. Such a binder is filled by the match like any other and then
// discarded, since no branch can reference a name the source never wrote.
//
// The name is unreachable from source, which is what makes it safe as a sentinel: the parser
// requires an Identifier after `infer`, and `_` lexes as its own token, so `infer _` does not parse.
const WildcardInferName = "_"

// IsWildcardInfer reports whether t is an anonymous `infer` binder, the lowered form of `_` in a
// pattern. The printer renders one as the bare `_` the source wrote rather than as `infer _`.
func (t *InferType) IsWildcardInfer() bool { return t.Binder && t.Name == WildcardInferName }

// MappedModifier states how a mapped type adjusts one member marker, `readonly` or `?`, on each
// field it emits. ModAdd sets the marker and ModRemove clears it. ModNone means the source wrote no
// modifier, which leaves the marker to be inherited from the field's source member when the mapped
// type is homomorphic and off otherwise. The source writes `readonly`/`+readonly` and `?`/`+?` for
// ModAdd and `-readonly`/`-?` for ModRemove.
type MappedModifier int

const (
	ModNone MappedModifier = iota
	ModAdd
	ModRemove
)

// MappedKeyType is the key variable a mapped type binds, the `K` of
// `{[K]: T[K] for K in keyof T}`. It is an inert leaf: nothing constrains against it, and the
// evaluator substitutes one key of the mapped type's Keys union for it before reducing the
// positions that name it. A mapped type still carrying it in a reduced position has not chosen a
// key yet, so the whole mapped type is inert.
//
// ID is the binding this node stands for. The `for K in …` clause and every reference to K in the
// value, key-remapping, and filter positions carry one id, so substituting a key reaches exactly
// the positions that binding stands at. A nested mapped type writing the same name draws a distinct
// id, which is what makes its own binding shadow the enclosing one. Name is the source name,
// carried for display.
type MappedKeyType struct {
	ID   int
	Name string
}

// MappedElem is the `[K]: V for K in Keys` member that computes an object's whole member list. It
// is the only member its object carries, because it describes every field rather than one of them;
// resolveObjectTypeAnn rejects an object mixing it with ordinary members. An object holding one is
// an unreduced residual: constrain records no bound against it and it flows through the solver's
// structural machinery untouched, rendering the way the source wrote it. The evaluator replaces it
// with the members it computes once Keys grounds to a union of string-literal keys, emitting one
// field per key with Key bound to that key.
//
// The fields mirror the surface syntax `{readonly [Name]?: Value for Key in Keys if Check : Extends}`:
//
//   - Keys is the constraint after `in`, the key set to iterate. `keyof T` is the usual source.
//   - Value is the type each emitted field takes, normally an indexed access such as `T[K]`.
//   - Name is the key-remapping expression written in the brackets. It is nil when the brackets
//     hold the bare key variable, so no remapping applies. A key it reduces to `never` drops that
//     field, the way TypeScript's `as` clause filters, and one it reduces to a union of names
//     contributes a field per name.
//   - Check and Extends are the optional `if C : E` filter. A key whose substituted `Check <: Extends`
//     fails is dropped. Both are nil when the source wrote no filter.
//   - Optional and Readonly are the `?` and `readonly` markers, each adding or removing the marker
//     on every emitted field. With neither written, a mapped member whose Keys is written `keyof T`
//     inherits each marker from the member the key names on T, so the identity mapped type really
//     is the identity.
//
// The enclosing ObjectType carries the trailing `...` inexact marker, so `{[K]: V for K in Keys, ...}`
// reduces to an inexact object without this member holding a flag of its own.
type MappedElem struct {
	Key      *MappedKeyType
	Keys     Type
	Value    Type
	Name     Type // nil ⇒ the brackets hold the bare key variable, no remapping
	Check    Type // nil with Extends ⇒ no `if C : E` filter
	Extends  Type
	Optional MappedModifier
	Readonly MappedModifier
}

// RestSpreadType is the residual `...P` spread element inside a tuple type, mirroring the old
// checker's RestSpreadType (internal/type_system/types.go). It is only meaningful as an element of
// a TupleType.Elems list: `[...P, x]` is a TupleType whose first element is a RestSpreadType. A
// tuple carrying one is inert — constrain passes it through untouched, the "adds no new mutable
// solver state" invariant it shares with KeyofType — until the evaluator splices Operand's tuple
// elements in at its position. A spread over a type parameter never grounds, so it stays symbolic
// and renders `...T`. The M4 literal case `[...pair, 3]` over a known tuple value reduces in
// inferTuple; this element covers the annotation over an abstract operand.
type RestSpreadType struct {
	Operand Type
}

// TemplateLitType is the residual template literal type operator, such as
// `on${T}`. Quasis holds the fixed string segments and Interps the interpolated
// types between them, so Quasis has exactly one more entry than Interps. Like KeyofType
// it is inert: it carries no bounds, constrain never records one against it, and it flows
// through the solver's structural machinery untouched, rendering `on${T}` the way
// the source wrote it. An evaluator reduces it once its interpolations are ground, taking
// the cartesian product over interpolated unions — `on${"a" | "b"}` ⇒ `"ona" | "onb"`.
// A template whose interpolation stays abstract, such as a type parameter, keeps that
// interpolation and stays symbolic.
type TemplateLitType struct {
	Quasis  []string
	Interps []Type
}

// StringIntrinsicKind names one of the four string intrinsics TypeScript exposes: Uppercase,
// Lowercase, Capitalize, and Uncapitalize. Each is written `intrinsic` in the shipped .d.ts.
type StringIntrinsicKind int

const (
	Uppercase StringIntrinsicKind = iota
	Lowercase
	Capitalize
	Uncapitalize
)

// String renders the operator's surface name, used by the printer to render `Uppercase<T>`
// and by the resolver to register the four intrinsics under their reference names.
func (k StringIntrinsicKind) String() string {
	switch k {
	case Uppercase:
		return "Uppercase"
	case Lowercase:
		return "Lowercase"
	case Capitalize:
		return "Capitalize"
	case Uncapitalize:
		return "Uncapitalize"
	}
	panic(fmt.Sprintf("StringIntrinsicKind.String: unhandled kind %d", int(k)))
}

// StringIntrinsicType is the residual intrinsic string operator `Uppercase<T>` and its three
// siblings. Like KeyofType it is inert: it carries no bounds, constrain never records
// one against it, and it flows through the solver's structural machinery untouched, rendering
// `Uppercase<T>` the way the source wrote it. An evaluator reduces it over a string-literal
// operand — `Uppercase<"abc">` ⇒ `"ABC"` — distributing over a union operand, and stays
// symbolic over an abstract operand such as a type parameter.
type StringIntrinsicType struct {
	Kind    StringIntrinsicKind
	Operand Type
}

// ExactnessKind names the two exactness intrinsics. `Exact<T>` closes a type's trailing `...` marker
// and `Inexact<T>` opens it. exact-types §6.1 and §6.2 specify the pair.
type ExactnessKind int

const (
	MakeExact ExactnessKind = iota
	MakeInexact
)

// String renders the operator's surface name, used by the printer to render `Exact<T>` and by the
// resolver to register the two intrinsics under their reference names.
func (k ExactnessKind) String() string {
	switch k {
	case MakeExact:
		return "Exact"
	case MakeInexact:
		return "Inexact"
	}
	panic(fmt.Sprintf("ExactnessKind.String: unhandled kind %d", int(k)))
}

// ExactnessType is the residual `Exact<T>` or `Inexact<T>` operator. Like KeyofType it is inert: it
// carries no bounds, constrain never records one against it, and it flows through the solver's
// structural machinery untouched, rendering `Exact<T>` the way the source wrote it.
//
// An evaluator reduces it once its operand grounds to a type carrying a trailing `...` marker. An
// object, a tuple, a function, and a union each carry one. `Inexact` sets that marker and `Exact`
// clears it, so `Inexact<{x: number}>` reduces to `{x: number, ...}`. An operand with no such
// marker, a primitive or a literal, reduces to itself. An abstract operand such as a type parameter
// stays symbolic.
type ExactnessType struct {
	Kind    ExactnessKind
	Operand Type
}

// RecursiveType is the μ-knot, the finite form of a type whose unfolding never ends. Body is one
// level of that unfolding and Binder is the variable Body names itself through, so `μX0.{next: X0}`
// is the type of a value whose `next` field holds another value of the same type. Unfolding it
// substitutes the whole knot for its binder in Body, which is how constrain compares a knot against
// a structural type.
//
// coalesce mints one when its walk re-enters an inference variable already on the current path, so a
// recursive position renders as the shape it stands for rather than collapsing to `never` or
// `unknown`.
// `fn f() { return {next: f()} }` infers `fn () -> {next: μX0.{next: X0}}`.
type RecursiveType struct {
	Binder *RecursiveVarType
	Body   Type
}

// RecursiveVarType is the variable a RecursiveType binds, the `X0` of `μX0.{next: X0}`. It is a leaf
// that carries no bounds, and nothing constrains against it. Unfolding the enclosing knot replaces
// it with that knot before any comparison reaches it.
//
// ID is the binding this node stands for. The binder and every reference to it carry one id, so
// unfolding reaches exactly the positions that binding stands at. A nested knot draws a distinct id,
// which is what makes its own binding shadow the enclosing one. Name is the display name its
// producer assigned, `X0`, `X1`, and so on; DisplayName supplies a fallback when a producer left it
// empty.
type RecursiveVarType struct {
	ID   int
	Name string
}

// DisplayName is the name a μ-binder renders under: the name its producer assigned, else the raw
// `r{ID}` debug form, mirroring printType's `t{ID}` for an unnamed inference variable. It is
// exported so the solver's describe, the second per-node renderer beside printType, names a binder
// the same way.
func (v *RecursiveVarType) DisplayName() string {
	if v.Name != "" {
		return v.Name
	}
	return "r" + strconv.Itoa(v.ID)
}

func (*TypeVarType) isType()         {}
func (*KeyofType) isType()           {}
func (*IndexType) isType()           {}
func (*TypeofType) isType()          {}
func (*CondType) isType()            {}
func (*InferType) isType()           {}
func (*MappedKeyType) isType()       {}
func (*RestSpreadType) isType()      {}
func (*TemplateLitType) isType()     {}
func (*StringIntrinsicType) isType() {}
func (*ExactnessType) isType()       {}
func (*RecursiveType) isType()       {}
func (*RecursiveVarType) isType()    {}
func (*PrimType) isType()            {}
func (*LitType) isType()             {}
func (*FuncType) isType()            {}
func (*TupleType) isType()           {}
func (*ObjectType) isType()          {}
func (*RefType) isType()             {}
func (*PromiseType) isType()         {}
func (*GeneratorType) isType()       {}
func (*ArrayType) isType()           {}
func (*Void) isType()                {}
func (*NullType) isType()            {}
func (*UndefinedType) isType()       {}
func (*NeverType) isType()           {}
func (*UnknownType) isType()         {}
func (*UnionType) isType()           {}
func (*IntersectionType) isType()    {}
func (*ErrorType) isType()           {}
func (*ClassType) isType()           {}
func (*AliasType) isType()           {}
func (*SkolemType) isType()          {}

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
		m = max(m, LevelOf(t.Ret))
		if t.Throws != nil {
			m = max(m, LevelOf(t.Throws))
		}
		return m
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
		// An alias reference's level is the max level over its type and lifetime arguments;
		// the Name carries no variables. A bare reference with no arguments is level 0. A free
		// lifetime arg such as the 'a in Foo<'a> lifts the level so the freshener/extruder
		// prune descends to freshen it, the same reason the ClassType arm folds in its args.
		m := 0
		for _, a := range t.TypeArgs {
			m = max(m, LevelOf(a))
		}
		for _, la := range t.LifetimeArgs {
			m = max(m, LevelOfLifetime(la))
		}
		return m
	case *KeyofType:
		// A residual operator's level is its operand's, so a `keyof T` over an out-of-level
		// type parameter lifts the level and the freshener/extruder prune descends to
		// freshen the operand, exactly as the single-child PromiseType arm does.
		return LevelOf(t.Operand)
	case *IndexType:
		// An indexed-access residual's level is the max over its two operands, so a `T[K]`
		// with an out-of-level target or index lifts the level and the freshener/extruder
		// prune descends into both, the two-child analogue of the KeyofType arm.
		return max(LevelOf(t.Target), LevelOf(t.Index))
	case *TypeofType:
		// A `typeof x` query's level is its resolved value type's, the same single-child rule
		// KeyofType and PromiseType follow.
		return LevelOf(t.Ty)
	case *CondType:
		// A conditional residual's level is the max over its four operands, so an out-of-level
		// operand lifts the level and the freshener/extruder prune descends into all four, the
		// four-child analogue of the two-child IndexType arm.
		return max(max(LevelOf(t.Check), LevelOf(t.Extends)), max(LevelOf(t.Then), LevelOf(t.Else)))
	case *RestSpreadType:
		// A `...P` spread element's level is its operand's, so an out-of-level spread operand lifts
		// the enclosing tuple's level and the freshener/extruder prune descends to freshen it, the
		// same single-child rule the KeyofType arm follows. The TupleType arm already maxes over its
		// elements, so this element contributes its operand's level there.
		return LevelOf(t.Operand)
	case *TemplateLitType:
		// A template literal's level is the max over its interpolations, so an out-of-level
		// interpolation lifts the level and the freshener/extruder prune descends into it, the
		// multi-child analogue of the KeyofType arm. The fixed string segments carry no variables.
		m := 0
		for _, interp := range t.Interps {
			m = max(m, LevelOf(interp))
		}
		return m
	case *StringIntrinsicType:
		// A string-intrinsic residual's level is its operand's, the same single-child rule the
		// KeyofType arm follows.
		return LevelOf(t.Operand)
	case *ExactnessType:
		// An `Exact<T>` residual's level is its operand's, the same single-child rule the KeyofType
		// arm follows.
		return LevelOf(t.Operand)
	case *RecursiveType:
		// A μ-knot's level is its body's, the same single-child rule the KeyofType arm follows, so a
		// knot whose body holds an out-of-level variable lifts the level and the freshener/extruder
		// prune descends into the body. Binder is a binding this node owns rather than a variable the
		// solver generalizes, so it contributes nothing. That split is what carries a knot across a
		// level boundary intact: the body's variables are freshened and the binder is left alone.
		return LevelOf(t.Body)
	case *PromiseType:
		// A promise's level is the max of its payload's and its rejection type's, so an
		// out-of-level Err lifts the level and the freshener/extruder prune descends to
		// freshen it, the same reason the FuncType arm folds in its Throws.
		return max(LevelOf(t.Inner), throwsLevel(t.Err))
	case *GeneratorType:
		// A generator's level is the max over its three slots, so an out-of-level yield,
		// return, or next type lifts the level and the freshener/extruder prune descends
		// into all three, the three-child analogue of the PromiseType arm.
		return max(max(LevelOf(t.Yield), LevelOf(t.Ret)), LevelOf(t.Next))
	case *ArrayType:
		// An array's level is its element's, the same single-child rule PromiseType follows.
		return LevelOf(t.Elem)
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
		// ErrorType, InferType, MappedKeyType, RecursiveVarType: childless leaves. ErrorType is a
		// sentinel at level 0. An InferType names a capture, a MappedKeyType names a mapped type's key,
		// and a RecursiveVarType names an enclosing μ-knot's binder; each is substituted by the
		// evaluator or by unfolding rather than generalized by the solver, so none lifts the level.
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
		return max(selfLevel(e.SelfParam), LevelOf(e.Type), throwsLevel(e.Throws))
	case *SetterElem:
		return max(selfLevel(e.SelfParam), LevelOf(e.Param), throwsLevel(e.Throws))
	case *ConstructorElem:
		return LevelOf(e.Fn)
	case *SpreadElem:
		// A `...A` spread element's level is its operand's, so an out-of-level spread operand lifts
		// the enclosing object's level and the freshener/extruder prune descends to freshen it.
		return LevelOf(e.Type)
	case *MappedElem:
		// A mapped member's level is the max over the operands that can hold a variable, so an
		// out-of-level operand lifts the enclosing object's level and the freshener/extruder prune
		// descends into each. Key is a binding this member owns rather than a variable the solver
		// generalizes, so it contributes nothing. Name, Check, and Extends are absent unless the
		// source wrote them.
		m := max(LevelOf(e.Keys), LevelOf(e.Value))
		for _, operand := range MappedOptionalOperands(e) {
			m = max(m, LevelOf(operand))
		}
		return m
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

// throwsLevel returns the level of an exceptional-exit position — an accessor's throws
// or a promise's Err — 0 for the nil shorthand that stands for `never`. `never` is a
// childless leaf at level 0, so the shorthand and an explicit `never` agree without
// materializing one.
func throwsLevel(throws Type) int {
	if throws == nil {
		return 0
	}
	return LevelOf(throws)
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
