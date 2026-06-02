package soltype

// Type is the sealed interface for all soltype nodes. (Production name for the
// spike's SimpleType; marker renamed isSimpleType -> isType.)
type Type interface{ isType() }

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
	}
	return false
}

// Pat is the sealed interface for parameter patterns. Mirrors the role of
// type_system.Pat (and ast.Pat) but lives in soltype to keep soltype ast-free.
// M1 ships a single concrete (IdentPat); M2 adds destructuring concretes
// (TuplePat, RecordPat, …) as the parser bridge surfaces them, with no
// FuncParam restructuring.
type Pat interface{ isPat() }

type IdentPat struct{ Name string }

func (*IdentPat) isPat() {}

// FuncParam mirrors type_system.FuncParam. M1 omits Optional (no optional
// params until M3+); Pattern is reachable only through Pat concretes M1
// defines (IdentPat).
type FuncParam struct {
	Pattern Pat
	Type    Type
}

// FuncType is a (possibly multi-argument) function type.
type FuncType struct {
	Params []*FuncParam
	Ret    Type
}

// TupleType is a fixed-length tuple type.
type TupleType struct{ Elems []Type }

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

func (*TypeVarType) isType()      {}
func (*PrimType) isType()         {}
func (*LitType) isType()          {}
func (*FuncType) isType()         {}
func (*TupleType) isType()        {}
func (*Void) isType()             {}
func (*NeverType) isType()        {}
func (*UnknownType) isType()      {}
func (*UnionType) isType()        {}
func (*IntersectionType) isType() {}

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
	default:
		// PrimType, LitType, Void, NeverType, UnknownType, UnionType,
		// IntersectionType: UnionType/IntersectionType only appear in coalesced
		// output, where every TypeVarType has been inlined — so they contain no
		// level-bearing nodes reachable to LevelOf in M1. M6 (when these become
		// constrain inputs via user annotations) adds the recursive arms
		// alongside the distributivity rules in constrain.
		return 0
	}
}
