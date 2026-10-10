package parser

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
)

// startsWhereClause reports whether the next tokens open a `where` clause. `where` is not a
// keyword, since `insertAdjacentElement(where: InsertPosition)` names a parameter with it,
// so the clause is recognized by its shape: `where` followed by a type annotation and a
// `:`. The probe parses that annotation and then restores the lexer and the error list, so
// a `where` that starts anything else is left untouched. A lifetime after `where` counts as
// a clause so that whereClause can report it.
func (p *Parser) startsWhereClause() bool {
	tok := p.lexer.peek()
	if tok.Type != Identifier || tok.Value != "where" {
		return false
	}
	saved := p.saveState()
	defer p.restoreState(saved)
	p.lexer.consume() // consume 'where'
	if p.lexer.peek().Type == Lifetime {
		return true
	}
	return p.typeAnn() != nil && p.lexer.peek().Type == Colon
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
	// built holds the union and intersection nodes this clause made to merge two relations
	// on one parameter, so a third relation extends them in place and a bound the source
	// wrote as a union or intersection is never extended.
	built := set.NewSet[ast.TypeAnn]()
	for {
		if tok := p.lexer.peek(); tok.Type == Lifetime {
			end = p.skipLifetimeRelation(tok)
		} else {
			relEnd, ok := p.whereRelation(typeParams, built)
			if !ok {
				return end, true
			}
			end = relEnd
		}
		if p.lexer.peek().Type != Comma {
			return end, true
		}
		p.lexer.consume() // consume ','
	}
}

// whereRelation parses one `sub: super` relation and records it. It returns the relation's
// end and ok=true, or ok=false after reporting a relation that did not parse, at which point
// the caller stops reading the clause.
func (p *Parser) whereRelation(typeParams []*ast.TypeParam, built set.Set[ast.TypeAnn]) (ast.Location, bool) {
	sub := p.typeAnnRequired()
	if _, isErr := sub.(*ast.ErrorTypeAnn); isErr {
		return sub.Span().End, false
	}
	colon := p.lexer.peek()
	if colon.Type != Colon {
		p.reportError(colon.Span, "Expected : after the left side of a where clause relation")
		return sub.Span().End, false
	}
	p.lexer.consume() // consume ':'
	super := p.typeAnnRequired()
	if _, isErr := super.(*ast.ErrorTypeAnn); isErr {
		return super.Span().End, false
	}
	p.recordWhereRelation(typeParams, sub, super, built)
	return super.Span().End, true
}

// skipLifetimeRelation reports a lifetime relation in a `where` clause, which is written on
// the lifetime's binder instead, and consumes it through its right side so the clause
// continues at the next comma. tok is the lifetime token, still on the lexer.
func (p *Parser) skipLifetimeRelation(tok *Token) ast.Location {
	p.reportError(tok.Span, "lifetime bounds are written on the lifetime binder, as <'a: 'b>")
	p.lexer.consume() // consume the lifetime
	end := tok.Span.End
	if p.lexer.peek().Type != Colon {
		return end
	}
	p.lexer.consume() // consume ':'
	for {
		next := p.lexer.peek()
		if next.Type != Lifetime {
			break
		}
		p.lexer.consume()
		end = next.Span.End
		if p.lexer.peek().Type != Ampersand {
			break
		}
		p.lexer.consume() // consume '&'
	}
	return end
}

// recordWhereRelation desugars one `sub: super` relation onto the binder it bounds. A
// parameter on the sub side gains super as an upper bound, and several upper bounds
// intersect, so `where T: A, T: B` is the bound `A & B`. Otherwise a parameter on the super
// side gains sub as a lower bound, and several lower bounds union, so `where X: P, Y: P` is
// the lower bound `X | Y`. The sub side is tried first, so `where P: Q` with both sides
// parameters is an upper bound on P. built is the clause's set of merge nodes, extended
// with any node this call makes.
func (p *Parser) recordWhereRelation(typeParams []*ast.TypeParam, sub, super ast.TypeAnn, built set.Set[ast.TypeAnn]) {
	if tp := namedTypeParam(typeParams, sub); tp != nil {
		tp.UpperBound = joinBounds(tp.UpperBound, super, built, newIntersection)
		tp.UpperBoundInWhere = true
		return
	}
	if tp := namedTypeParam(typeParams, super); tp != nil {
		tp.LowerBound = joinBounds(tp.LowerBound, sub, built, newUnion)
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

// joinBounds adds bound to existing. A nil existing is replaced. An existing node in built
// was made by an earlier relation of the same clause, so it is extended in place and three
// relations render as one flat `A & B & C`. Any other existing bound, one the source wrote
// on the binder or as a union or intersection of its own, is wrapped together with the new
// one in a fresh node, which is added to built.
func joinBounds(existing, bound ast.TypeAnn, built set.Set[ast.TypeAnn], join func([]ast.TypeAnn, ast.Span) ast.TypeAnn) ast.TypeAnn {
	if existing == nil {
		return bound
	}
	if built.Contains(existing) {
		switch e := existing.(type) {
		case *ast.UnionTypeAnn:
			e.Types = append(e.Types, bound)
			return e
		case *ast.IntersectionTypeAnn:
			e.Types = append(e.Types, bound)
			return e
		}
	}
	node := join([]ast.TypeAnn{existing, bound}, ast.MergeSpans(existing.Span(), bound.Span()))
	built.Add(node)
	return node
}

func newUnion(types []ast.TypeAnn, span ast.Span) ast.TypeAnn {
	return ast.NewUnionTypeAnn(types, span)
}

func newIntersection(types []ast.TypeAnn, span ast.Span) ast.TypeAnn {
	return ast.NewIntersectionTypeAnn(types, span)
}
