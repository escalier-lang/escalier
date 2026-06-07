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

// TupleType is a fixed-length tuple type.
type TupleType struct{ Elems []Type }

// RecordType is a record/object type with named value fields. M2 ships the
// BASIC width-and-depth-subtyping form that record literals and field reads
// need ({a: 5}, recv.a); the richer object system — optional fields, methods,
// getters/setters, index signatures, spreads, `mut`, and usage-inference — is
// M4. It mirrors the role of type_system.ObjectType but carries only named
// value fields (no method/getter/setter/mapped elements yet), so it stays a
// flat list of Name→Type fields. Subtyping matches fields by name (order is
// irrelevant); the slice order is preserved only for stable rendering.
type RecordType struct{ Fields []*RecordField }

// RecordField is one named field of a RecordType.
type RecordField struct {
	Name string
	Type Type
}

// Field returns the type of the named field and whether it is present. Field
// names are unique in a well-formed RecordType — the constraint solver dedups
// duplicate keys (last value wins) when it builds a record from a literal — so
// the first match is the field. The scan is linear because records are small;
// it is the single canonical field lookup shared by constraining, structural
// equality, and member access.
func (r *RecordType) Field(name string) (Type, bool) {
	for _, f := range r.Fields {
		if f.Name == name {
			return f.Type, true
		}
	}
	return nil, false
}

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
func (*RecordType) isType()       {}
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
	case *RecordType:
		m := 0
		for _, f := range t.Fields {
			m = max(m, LevelOf(f.Type))
		}
		return m
	case *PromiseType:
		return LevelOf(t.Inner)
	default:
		// PrimType, LitType, Void, NeverType, UnknownType, ErrorType, UnionType,
		// IntersectionType: ErrorType is a childless sentinel (level 0);
		// UnionType/IntersectionType only appear in coalesced
		// output, where every TypeVarType has been inlined — so they contain no
		// level-bearing nodes reachable to LevelOf in M1. M6 (when these become
		// constrain inputs via user annotations) adds the recursive arms
		// alongside the distributivity rules in constrain.
		return 0
	}
}
