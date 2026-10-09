package parser

import (
	"github.com/escalier-lang/escalier/internal/ast"
)

// startsWhereClause reports whether the next token opens a `where` clause. `where` is not
// a keyword, since `insertAdjacentElement(where: InsertPosition)` names a parameter with
// it, so the clause is recognized by the token after it: a type annotation can start
// there, where an expression statement continuing an identifier cannot.
func (p *Parser) startsWhereClause() bool {
	tok := p.lexer.peek()
	if tok.Type != Identifier || tok.Value != "where" {
		return false
	}
	// nolint: exhaustive
	switch p.lexer.peek2().Type {
	case Identifier, Underscore, OpenBrace, OpenBracket, Ampersand, Lifetime, Mut, Fn, Keyof,
		Typeof, Infer, If, StrLit, NumLit, True, False, Null, Undefined, Number, String,
		Boolean, Symbol, Bigint, Any, Unknown, Never, Void, Minus, BackTick, Readonly:
		return true
	default:
		return false
	}
}

// whereClause parses an optional `where` clause after a signature or a declaration header
// and records each relation on the type parameter it bounds. A relation `X: Y` says X is a
// subtype of Y, the reading `:` has on a binder, a lifetime and a conditional type. `P: Y`
// with P a parameter of typeParams records Y as P's upper bound, and `X: P` records X as
// P's lower bound. A relation naming no parameter on either side is reported, since no
// binder can carry it. The end of the last relation is returned with found=true, or the
// zero location with found=false when there is no clause.
func (p *Parser) whereClause(typeParams []*ast.TypeParam) (end ast.Location, found bool) {
	if !p.startsWhereClause() {
		return ast.Location{}, false
	}
	p.lexer.consume() // consume 'where'
	for {
		sub := p.typeAnnRequired()
		if sub == nil {
			return end, true
		}
		end = sub.Span().End
		colon := p.lexer.peek()
		if colon.Type != Colon {
			p.reportError(colon.Span, "Expected : after the left side of a where clause relation")
			return end, true
		}
		p.lexer.consume() // consume ':'
		super := p.typeAnnRequired()
		if super == nil {
			return end, true
		}
		end = super.Span().End
		p.recordWhereRelation(typeParams, sub, super)
		if p.lexer.peek().Type != Comma {
			return end, true
		}
		p.lexer.consume() // consume ','
	}
}

// recordWhereRelation desugars one `sub: super` relation onto the binder it bounds. A
// parameter on the sub side gains super as an upper bound, and several upper bounds
// intersect, so `where T: A, T: B` is the bound `A & B`. Otherwise a parameter on the super
// side gains sub as a lower bound, and several lower bounds union, so `where X: P, Y: P` is
// the lower bound `X | Y`. The sub side is tried first, so `where P: Q` with both sides
// parameters is an upper bound on P.
func (p *Parser) recordWhereRelation(typeParams []*ast.TypeParam, sub, super ast.TypeAnn) {
	if tp := namedTypeParam(typeParams, sub); tp != nil {
		tp.UpperBound = joinBounds(tp.UpperBound, super, tp.UpperBoundInWhere, newIntersection)
		tp.UpperBoundInWhere = true
		return
	}
	if tp := namedTypeParam(typeParams, super); tp != nil {
		tp.LowerBound = joinBounds(tp.LowerBound, sub, true, newUnion)
		return
	}
	p.reportError(ast.MergeSpans(sub.Span(), super.Span()),
		"a where clause relation must name a type parameter of this declaration on one side")
}

// namedTypeParam returns the parameter of typeParams that t names as a bare reference,
// with no type arguments, lifetime or qualifier, or nil when t is anything else.
func namedTypeParam(typeParams []*ast.TypeParam, t ast.TypeAnn) *ast.TypeParam {
	ref, ok := t.(*ast.TypeRefTypeAnn)
	if !ok || len(ref.TypeArgs) > 0 || len(ref.LifetimeArgs) > 0 || ref.Lifetime != nil {
		return nil
	}
	ident, ok := ref.Name.(*ast.Ident)
	if !ok {
		return nil
	}
	for _, tp := range typeParams {
		if tp.Name == ident.Name {
			return tp
		}
	}
	return nil
}

// joinBounds adds bound to existing. A nil existing is replaced. An existing bound that
// an earlier relation of the same clause built, which is what joined says, is extended in
// place so three relations render as one flat `A & B & C`. Any other existing bound, such
// as one written inline on the binder, is wrapped together with the new one.
func joinBounds(existing, bound ast.TypeAnn, joined bool, join func([]ast.TypeAnn, ast.Span) ast.TypeAnn) ast.TypeAnn {
	if existing == nil {
		return bound
	}
	if joined {
		switch e := existing.(type) {
		case *ast.UnionTypeAnn:
			e.Types = append(e.Types, bound)
			return e
		case *ast.IntersectionTypeAnn:
			e.Types = append(e.Types, bound)
			return e
		}
	}
	return join([]ast.TypeAnn{existing, bound}, ast.MergeSpans(existing.Span(), bound.Span()))
}

func newUnion(types []ast.TypeAnn, span ast.Span) ast.TypeAnn {
	return ast.NewUnionTypeAnn(types, span)
}

func newIntersection(types []ast.TypeAnn, span ast.Span) ast.TypeAnn {
	return ast.NewIntersectionTypeAnn(types, span)
}
