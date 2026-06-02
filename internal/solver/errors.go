package solver

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/escalier-lang/escalier/internal/soltype"
)

// SolverError is the sealed interface for constraint-solving failures. Each
// concrete struct carries typed references to the offending soltype.Type values
// (not just a rendered string) so LSP/tooling consumers can inspect what the
// error refers to — e.g. navigate to a type's declaration — without reparsing
// the message. Modeled on internal/checker/error.go's shape.
//
// Errors are span-free in M1 (there is no parser bridge until M2). M2 adds a
// Span() ast.Span method and likely rebases these onto the checker's diagnostic
// types per 02-design-notes.md Settled Decision #4.
type SolverError interface {
	isSolverError()
	Message() string
}

// CannotConstrainError fires when a non-variable LHS/RHS pair fails to match:
// prim/prim mismatch, lit/lit mismatch, lit/prim mismatch, and the generic
// "no rule applies" fall-through at the end of constrain.
type CannotConstrainError struct {
	LHS, RHS soltype.Type
}

// FuncArityMismatchError fires on FuncType <: FuncType when LHS has MORE params
// than RHS (the fewer-params-is-subtype rule). Holds the full FuncTypes, not
// just the arities, so consumers can report param/return types too.
type FuncArityMismatchError struct {
	LHS, RHS *soltype.FuncType
}

// TupleLengthMismatchError fires on TupleType <: TupleType with different
// lengths (M1's exact-tuple case; M4 may narrow the firing conditions when the
// inexact flag is added).
type TupleLengthMismatchError struct {
	LHS, RHS *soltype.TupleType
}

func (*CannotConstrainError) isSolverError()     {}
func (*FuncArityMismatchError) isSolverError()   {}
func (*TupleLengthMismatchError) isSolverError() {}

func (e *CannotConstrainError) Message() string {
	return fmt.Sprintf("cannot constrain %s <: %s", describe(e.LHS), describe(e.RHS))
}

func (e *FuncArityMismatchError) Message() string {
	return fmt.Sprintf("cannot constrain function of arity %d <: function of arity %d",
		len(e.LHS.Params), len(e.RHS.Params))
}

func (e *TupleLengthMismatchError) Message() string {
	return fmt.Sprintf("cannot constrain tuple of length %d <: tuple of length %d",
		len(e.LHS.Elems), len(e.RHS.Elems))
}

// describe renders a RAW, uncoalesced type for in-flight error messages (t0,
// function, number). Distinct from soltype.Print, which renders coalesced
// output as user-facing Escalier syntax (see m1-implementation-plan §2.2). It
// lives in solver because it walks bound-carrying variables, and its wording
// matches the spike's verbatim so test assertions stay stable.
func describe(t soltype.Type) string {
	switch t := t.(type) {
	case *soltype.PrimType:
		return primName(t.Prim)
	case *soltype.LitType:
		switch l := t.Lit.(type) {
		case *soltype.StrLit:
			return strconv.Quote(l.Value)
		case *soltype.NumLit:
			return strconv.FormatFloat(l.Value, 'f', -1, 32)
		case *soltype.BoolLit:
			return strconv.FormatBool(l.Value)
		}
		return "?"
	case *soltype.FuncType:
		return "function"
	case *soltype.TupleType:
		return "tuple"
	case *soltype.Void:
		return "void"
	case *soltype.NeverType:
		return "never"
	case *soltype.UnknownType:
		return "unknown"
	case *soltype.UnionType:
		return joinDescribe(t.Types, " | ")
	case *soltype.IntersectionType:
		return joinDescribe(t.Types, " & ")
	case *soltype.TypeVarType:
		return "t" + strconv.Itoa(t.ID)
	}
	return "?"
}

func joinDescribe(types []soltype.Type, sep string) string {
	parts := make([]string, len(types))
	for i, t := range types {
		parts[i] = describe(t)
	}
	return strings.Join(parts, sep)
}

// primName maps a Prim to the surface name used in raw error messages.
func primName(p soltype.Prim) string {
	switch p {
	case soltype.NumPrim:
		return "number"
	case soltype.StrPrim:
		return "string"
	case soltype.BoolPrim:
		return "boolean"
	}
	return "?"
}

// primOf maps a Lit concrete to the Prim it is a literal of: *NumLit -> NumPrim,
// *StrLit -> StrPrim, *BoolLit -> BoolPrim. Used by the LitType <: PrimType arm
// of constrain (a literal is a subtype of its primitive).
func primOf(lit soltype.Lit) soltype.Prim {
	switch lit.(type) {
	case *soltype.NumLit:
		return soltype.NumPrim
	case *soltype.StrLit:
		return soltype.StrPrim
	case *soltype.BoolLit:
		return soltype.BoolPrim
	}
	panic(fmt.Sprintf("primOf: unhandled Lit %T", lit))
}
