package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
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
func (c *checker) bindPattern(scope *Scope, lvl int, pat ast.Pat, scrutinee soltype.Type, leafTypes map[string]soltype.Type) soltype.Pat {
	scrutinee = soltype.CarrierOf(scrutinee)
	switch p := pat.(type) {
	case *ast.IdentPat:
		t := c.applyLeafExtras(scope, lvl, p, scrutinee, p.TypeAnn, p.Default)
		c.bindLeaf(scope, p.Name, t, p, leafTypes)
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
		// scrutinee, so the literal flows INTO the scrutinee. `5 <: number` checks.
		// The check is exact against a concrete scrutinee such as a top-level `match`
		// arm. For a NESTED slot the scrutinee here is the field's covariant result
		// var, which carries no upper bound. So a kind mismatch like `{x: "hi"}`
		// against `{x: number}` is not yet rejected. The refutable literal-pattern
		// check lands with E2's `match`, which this path is laid out to extend. A
		// literal pattern binds nothing.
		c.constrain(p, lt, scrutinee)
		c.recordType(p, lt)
		return &soltype.LitPat{Lit: lt.Lit}

	case *ast.TuplePat:
		// A trailing `...rest` element makes the pattern match any tuple at least as
		// long as the fixed prefix, so the requirement becomes an INEXACT tuple over
		// the fixed elements. Without a rest the requirement stays exact and a wrong
		// arity is a TupleLengthMismatchError. The rest element itself needs typed
		// rest tuples, which arrive in M9, so it is reported unsupported and binds
		// nothing.
		fixed := make([]ast.Pat, 0, len(p.Elems))
		inexact := false
		for _, e := range p.Elems {
			if _, isRest := e.(*ast.RestPat); isRest {
				inexact = true
				c.reportUnsupported(e)
				continue
			}
			fixed = append(fixed, e)
		}
		elemTypes := make([]soltype.Type, len(fixed))
		for i := range fixed {
			elemTypes[i] = c.freshAt(lvl)
		}
		// Each αi lowers from the scrutinee's matching element, so a sub-pattern binds
		// at that element's type.
		c.constrain(p, scrutinee, &soltype.TupleType{Elems: elemTypes, Inexact: inexact})
		subs := make([]soltype.Pat, len(fixed))
		for i, e := range fixed {
			subs[i] = c.bindPattern(scope, lvl, e, elemTypes[i], leafTypes)
		}
		c.recordType(p, scrutinee)
		return &soltype.TuplePat{Elems: subs}

	case *ast.ObjectPat:
		fields := make([]*soltype.ObjectPatField, 0, len(p.Elems))
		for _, elem := range p.Elems {
			switch e := elem.(type) {
			case *ast.ObjShorthandPat:
				// A default makes the field optional. `{x = 0}` binds even when x is
				// absent, so the requirement must not demand it.
				beta := c.freshAt(lvl)
				c.constrain(e, scrutinee, propReq(e.Key.Name, beta, e.Default != nil))
				t := c.applyLeafExtras(scope, lvl, e, beta, e.TypeAnn, e.Default)
				c.bindLeaf(scope, e.Key.Name, t, e, leafTypes)
				fields = append(fields, &soltype.ObjectPatField{
					Name:  e.Key.Name,
					Value: &soltype.IdentPat{Name: e.Key.Name},
				})
			case *ast.ObjKeyValuePat:
				// A default on the value sub-pattern, as in `{x: a = 0}`, likewise makes
				// the field optional.
				beta := c.freshAt(lvl)
				c.constrain(e, scrutinee, propReq(e.Key.Name, beta, patternDefaultsField(e.Value)))
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
		// ExtractorPat and InstancePat are the constructor patterns; they are M5. A
		// bare RestPat is only meaningful inside a tuple or object. Report and bind
		// nothing.
		c.reportUnsupported(pat)
		return &soltype.WildcardPat{}
	}
}

// applyLeafExtras resolves a destructured leaf's optional type annotation
// (`{x :: T}`, `[a :: T]`) and default value (`{x = d}`, `[a = d]`) against its
// slot type, returning the type to bind. An annotation constrains the slot to
// satisfy it and is then adopted as the leaf's type, mirroring how an annotated
// `val` adopts its annotation. A default is required to satisfy that bound type
// and flows into it, so a leaf bound from an absent-but-defaulted field reads the
// default's type rather than `never`.
func (c *checker) applyLeafExtras(scope *Scope, lvl int, node ast.Node, slot soltype.Type, typeAnn ast.TypeAnn, def ast.Expr) soltype.Type {
	bound := slot
	if typeAnn != nil {
		if annT, ok := c.resolveTypeAnn(typeAnn, lvl); ok {
			c.constrain(node, slot, annT)
			bound = annT
		}
	}
	if def != nil {
		defT := c.inferExpr(scope, lvl, def)
		c.constrain(def, defT, bound)
	}
	return bound
}

// patternDefaultsField reports whether a destructured field's value sub-pattern
// carries a default (`{x: a = 0}`), which makes the field optional.
func patternDefaultsField(p ast.Pat) bool {
	ip, ok := p.(*ast.IdentPat)
	return ok && ip.Default != nil
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
// mints for a field read. optional marks the property `name?: t` so an absent
// field is tolerated, which a destructuring default relies on.
func propReq(name string, t soltype.Type, optional bool) *soltype.ObjectType {
	return &soltype.ObjectType{
		Elems:   []soltype.ObjTypeElem{&soltype.PropertyElem{Name: name, Type: t, Optional: optional}},
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
