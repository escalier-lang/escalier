package soltype

import (
	"fmt"
	"strconv"
	"strings"
)

// Precedence levels for type operators, matching the Escalier parser (and
// type_system/print_type.go). Higher values bind more tightly. M1 carries only
// the subset reachable from coalesced output — functions, unions, intersections,
// and atoms; type_system's precPrefix (keyof/mut/...) has no M1 analogue yet.
const (
	precFunc         = 2 // fn (...) -> T — return type is greedy, needs parens in union/intersection
	precUnion        = 3 // A | B
	precIntersection = 4 // A & B
	precAtom         = 6 // primary types, never need parens
)

// typePrec returns the printing precedence of a coalesced M1 type.
func typePrec(t Type) int {
	switch t.(type) {
	case *FuncType:
		return precFunc
	case *UnionType:
		return precUnion
	case *IntersectionType:
		return precIntersection
	default:
		// PrimType, LitType, TupleType, Void, NeverType, UnknownType — atoms.
		// TypeVarType never appears in coalesced output (coalesce inlines every
		// variable, m1-implementation-plan Delta #1), so it has no printer arm.
		return precAtom
	}
}

// Print renders a coalesced Type as an Escalier type-annotation string.
//
// This is Delta #2 of m1-implementation-plan §2.2: a native soltype printer that
// shares NO code with type_system.PrintType but deliberately mirrors its surface
// forms so the two checkers' rendered types stay string-comparable in M7's
// differential harness. It renders the M1 coalesced type set only
// (PrimType/LitType/FuncType/TupleType/Void/NeverType/UnknownType/UnionType/
// IntersectionType). There is no <T0, ...> quantifier prefix in M1 — no named
// refs exist to collect, since coalescing always inlines variables; that
// machinery lands in M3 with the rest of the polymorphism-rendering bundle
// (§3.3).
//
// Print is distinct from solver's describe(): describe renders a RAW,
// uncoalesced type (t0, function, number) mid-constrain for error messages,
// whereas Print renders a COALESCED type as user-facing syntax. They look
// similar but operate at different stages and must not be merged (§2.2).
func Print(t Type) string {
	return printType(t)
}

// printTypeMinPrec prints a child type, wrapping it in parentheses when its
// precedence is below the required minimum — mirrors type_system's helper of the
// same shape, so e.g. a function inside a union renders as
// `(fn () -> number) | string`.
func printTypeMinPrec(t Type, minPrec int) string {
	result := printType(t)
	if typePrec(t) < minPrec {
		return "(" + result + ")"
	}
	return result
}

func printType(t Type) string {
	switch t := t.(type) {
	case *PrimType:
		return printPrim(t.Prim)
	case *LitType:
		return printLit(t.Lit)
	case *NeverType:
		return "never"
	case *UnknownType:
		return "unknown"
	case *Void:
		return "void"
	case *TupleType:
		elems := make([]string, len(t.Elems))
		for i, e := range t.Elems {
			elems[i] = printType(e)
		}
		return "[" + strings.Join(elems, ", ") + "]"
	case *FuncType:
		return "fn " + printFuncTail(t)
	case *UnionType:
		parts := make([]string, len(t.Types))
		for i, m := range t.Types {
			parts[i] = printTypeMinPrec(m, precUnion)
		}
		return strings.Join(parts, " | ")
	case *IntersectionType:
		parts := make([]string, len(t.Types))
		for i, m := range t.Types {
			parts[i] = printTypeMinPrec(m, precIntersection)
		}
		return strings.Join(parts, " & ")
	}
	panic(fmt.Sprintf("printType: unhandled %T", t))
}

// printFuncTail renders the "(params) -> ret" portion of a function, without the
// leading "fn" keyword. Kept as a separate helper so M3 can compose it with a
// <...> quantifier prefix without byte-slicing the "fn " back off.
func printFuncTail(t *FuncType) string {
	ps := make([]string, len(t.Params))
	for i, p := range t.Params {
		ps[i] = paramName(p, i) + ": " + printType(p.Type)
	}
	return "(" + strings.Join(ps, ", ") + ") -> " + printType(t.Ret)
}

// paramName renders p.Pattern. M1's only Pat concrete is IdentPat; a nil or
// otherwise-unknown pattern falls back to a positional name ("x0", "x1", ...).
// M2's destructuring Pat concretes add their own arms here.
func paramName(p *FuncParam, i int) string {
	if pat, ok := p.Pattern.(*IdentPat); ok {
		return pat.Name
	}
	return "x" + strconv.Itoa(i)
}

// printPrim maps a Prim to its Escalier surface name — mirrors
// type_system/print_type.go's printPrimType.
func printPrim(p Prim) string {
	switch p {
	case NumPrim:
		return "number"
	case StrPrim:
		return "string"
	case BoolPrim:
		return "boolean"
	}
	panic(fmt.Sprintf("printPrim: unhandled Prim %d", p))
}

// printLit renders a literal value in Escalier surface syntax.
func printLit(lit Lit) string {
	switch lit := lit.(type) {
	case *StrLit:
		return strconv.Quote(lit.Value)
	case *NumLit:
		// 64-bit precision, matching solver's describe() (see its comment):
		// NumLit.Value is a float64, so bitSize 32 would round-trip through
		// float32 and misrender values beyond float32's range/mantissa.
		// type_system's printer still uses bitSize 32 here — a latent bug noted
		// in describe — so this is the one surface form where Print is
		// deliberately more correct than the renderer it otherwise mirrors.
		return strconv.FormatFloat(lit.Value, 'f', -1, 64)
	case *BoolLit:
		return strconv.FormatBool(lit.Value)
	}
	panic(fmt.Sprintf("printLit: unhandled Lit %T", lit))
}
