package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// bindPattern types an ast.Pat against a scrutinee type, binding every leaf
// identifier the pattern introduces into scope and returning the soltype.Pat
// mirror used to render a destructured parameter (M4 E1). It is the
// structural-pattern path shared by `val`/`var` destructuring and function-param
// destructuring; E2's `match` arms reuse it too.
//
// A pattern dispatches through the member-lookup constraint path, NOT subtyping.
// An ObjectPat `{x, y}` against a scrutinee `s` emits `s <: {x: βx, ...}` and
// `s <: {y: βy, ...}` — the same inexact, one-property requirement inferMember
// mints for a field read — and binds x/y to βx/βy. Because each requirement is
// inexact, a pattern may bind a SUBSET of the scrutinee's fields. A TuplePat
// `[a, b]` emits `s <: [αa, αb]` (an EXACT tuple, so a wrong arity is rejected).
// Only a field the scrutinee lacks, or a wrong tuple arity, is rejected,
// surfacing MissingPropertyError / TupleLengthMismatchError. The scrutinee's
// borrow wrapper is peeled first (CarrierOf), so a destructured borrow binds the
// borrowed contents, just as a member read does.
//
// leafTypes, when non-nil, receives each leaf binding's type keyed by name. The
// function-param path passes its paramTypes map so the liveness pre-pass can seed
// each leaf's alias mutability; other callers pass nil.
func (c *checker) bindPattern(scope *Scope, lvl int, pat ast.Pat, scrutinee soltype.Type, leafTypes map[string]soltype.Type) soltype.Pat {
	scrutinee = soltype.CarrierOf(scrutinee)
	switch p := pat.(type) {
	case *ast.IdentPat:
		c.bindLeaf(scope, p.Name, scrutinee, p, leafTypes)
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
		// scrutinee, so the literal flows INTO the scrutinee: `5 <: number` checks.
		// This is exact against a concrete scrutinee (a top-level `match` arm). For a
		// NESTED slot the scrutinee here is the field's covariant result var, which
		// carries no upper bound, so a kind mismatch like `{x: "hi"}` against `{x:
		// number}` is not yet rejected — the refutable literal-pattern check lands
		// with E2's `match`, which this path is laid out to extend. A literal pattern
		// binds nothing.
		c.constrain(p, lt, scrutinee)
		c.recordType(p, lt)
		return &soltype.LitPat{Lit: lt.Lit}

	case *ast.TuplePat:
		elemTypes := make([]soltype.Type, len(p.Elems))
		for i := range p.Elems {
			elemTypes[i] = c.freshAt(lvl)
		}
		// scrutinee <: [α0, …, αn] — an EXACT tuple requirement, so a wrong arity is
		// a TupleLengthMismatchError. Each αi lowers from the scrutinee's matching
		// element, so a sub-pattern binds at that element's type.
		c.constrain(p, scrutinee, &soltype.TupleType{Elems: elemTypes})
		subs := make([]soltype.Pat, len(p.Elems))
		for i, e := range p.Elems {
			subs[i] = c.bindPattern(scope, lvl, e, elemTypes[i], leafTypes)
		}
		c.recordType(p, scrutinee)
		return &soltype.TuplePat{Elems: subs}

	case *ast.ObjectPat:
		fields := make([]*soltype.ObjectPatField, 0, len(p.Elems))
		for _, elem := range p.Elems {
			switch e := elem.(type) {
			case *ast.ObjShorthandPat:
				beta := c.freshAt(lvl)
				c.constrain(e, scrutinee, propReq(e.Key.Name, beta))
				c.bindLeaf(scope, e.Key.Name, beta, e, leafTypes)
				fields = append(fields, &soltype.ObjectPatField{
					Name:  e.Key.Name,
					Value: &soltype.IdentPat{Name: e.Key.Name},
				})
			case *ast.ObjKeyValuePat:
				beta := c.freshAt(lvl)
				c.constrain(e, scrutinee, propReq(e.Key.Name, beta))
				sub := c.bindPattern(scope, lvl, e.Value, beta, leafTypes)
				fields = append(fields, &soltype.ObjectPatField{Name: e.Key.Name, Value: sub})
			default:
				// ObjRestPat (`{...rest}`) needs object rest types, which arrive in M9.
				c.reportUnsupported(elem)
			}
		}
		c.recordType(p, scrutinee)
		return &soltype.ObjectPat{Fields: fields}

	default:
		// ExtractorPat / InstancePat (constructor patterns) are M5; a bare RestPat is
		// only meaningful inside a tuple/object. Report and bind nothing.
		c.reportUnsupported(pat)
		return &soltype.WildcardPat{}
	}
}

// bindLeaf binds one identifier leaf to t in scope as a monomorphic projection of
// the scrutinee, records its type, and (when leafTypes is non-nil) reports the
// leaf's type by name for the liveness pre-pass.
func (c *checker) bindLeaf(scope *Scope, name string, t soltype.Type, node ast.Node, leafTypes map[string]soltype.Type) {
	scope.defineValue(name, ValueBinding{Schemes: []TypeScheme{monoScheme(t)}})
	c.recordType(node, t)
	if leafTypes != nil {
		leafTypes[name] = t
	}
}

// propReq builds the inexact one-property requirement `{name: t, ...}` — "the
// receiver has at least this field" — the same shape inferMember's valueProp
// mints for a field read.
func propReq(name string, t soltype.Type) *soltype.ObjectType {
	return &soltype.ObjectType{
		Elems:   []soltype.ObjTypeElem{&soltype.PropertyElem{Name: name, Type: t}},
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
