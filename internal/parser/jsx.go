package parser

import (
	"github.com/escalier-lang/escalier/internal/ast"
)

func (p *Parser) jsxElement() *ast.JSXElementExpr {
	opening := p.jsxOpening()

	span := ast.Span{
		Start:    opening.Span().Start,
		End:      opening.Span().End,
		SourceID: p.lexer.source.ID,
	}

	if !opening.SelfClose {
		children := p.jsxChildren()
		closing := p.jsxClosing()

		return ast.NewJSXElement(
			opening, closing, children, ast.MergeSpans(span, closing.Span()),
		)
	}

	return ast.NewJSXElement(opening, nil, []ast.JSXChild{}, span)
}

func (p *Parser) jsxOpening() *ast.JSXOpening {
	token := p.lexer.next()
	if token.Type != LessThan {
		p.reportError(token.Span, "Expected '<'")
	}

	start := token.Span.Start

	var name string
	token = p.lexer.next()

	//nolint: exhaustive
	switch token.Type {
	case Identifier:
		name = token.Value
	case GreaterThan:
		end := token.Span.End
		span := ast.Span{
			Start:    start,
			End:      end,
			SourceID: p.lexer.source.ID,
		}
		return ast.NewJSXOpening("", []*ast.JSXAttr{}, false, span)
	default:
		p.reportError(token.Span, "Expected an identifier or '>'")
	}

	attrs := p.jsxAttrs()

	var selfClosing bool

	token = p.lexer.next()

	//nolint: exhaustive
	switch token.Type {
	case SlashGreaterThan:
		selfClosing = true
	case GreaterThan:
		// do nothing
	default:
		p.reportError(token.Span, "Expected '>' or '/>'")
	}

	end := token.Span.End

	span := ast.Span{
		Start:    start,
		End:      end,
		SourceID: p.lexer.source.ID,
	}

	return ast.NewJSXOpening(name, attrs, selfClosing, span)
}

func (p *Parser) jsxAttrs() []*ast.JSXAttr {
	attrs := []*ast.JSXAttr{}

	for {
		token := p.lexer.peek()
		name := ""
		if token.Type == Identifier {
			p.lexer.consume() // consume identifier
			name = token.Value
		} else {
			break
		}

		// parse attribute value
		token = p.lexer.peek()
		if token.Type == Equal {
			p.lexer.consume() // consume equals
		} else {
			p.reportError(token.Span, "Expected '='")
		}

		var value ast.JSXAttrValue

		// parse attribute value
		token = p.lexer.peek()

		//nolint: exhaustive
		switch token.Type {
		case StrLit:
			p.lexer.consume() // consume string
			value = ast.NewJSXString(token.Value, token.Span)
		case OpenBrace:
			p.lexer.consume() // consume '{'
			expr := p.expr()
			if expr == nil {
				return attrs
			}
			value = ast.NewJSXExprContainer(expr, token.Span)
			token = p.lexer.peek()
			if token.Type == CloseBrace {
				p.lexer.consume() // consume '}'
			} else {
				p.reportError(token.Span, "Expected '}'")
			}
		default:
			p.reportError(token.Span, "Expected a string or an expression")
		}

		attr := ast.NewJSXAttr(name, &value, token.Span)
		attrs = append(attrs, attr)
	}

	return attrs
}

func (p *Parser) jsxClosing() *ast.JSXClosing {
	token := p.lexer.next()
	if token.Type != LessThanSlash {
		p.reportError(token.Span, "Expected '</'")
	}

	start := token.Span.Start

	var name string
	token = p.lexer.next()

	// nolint: exhaustive
	switch token.Type {
	case Identifier:
		name = token.Value
	case GreaterThan:
		end := token.Span.End
		span := ast.Span{
			Start:    start,
			End:      end,
			SourceID: p.lexer.source.ID,
		}
		return ast.NewJSXClosing("", span)
	default:
		p.reportError(token.Span, "Expected an identifier or '>'")
	}

	token = p.lexer.next()
	if token.Type != GreaterThan {
		p.reportError(token.Span, "Expected '>'")
	}

	end := token.Span.End
	span := ast.Span{
		Start:    start,
		End:      end,
		SourceID: p.lexer.source.ID,
	}

	return ast.NewJSXClosing(name, span)
}

func (p *Parser) jsxChildren() []ast.JSXChild {
	children := []ast.JSXChild{}

	for {
		token := p.lexer.peek()

		//nolint: exhaustive
		switch token.Type {
		case LessThanSlash, EndOfFile:
			return children
		case LessThan:
			jsxElement := p.jsxElement()
			if jsxElement != nil {
				children = append(children, jsxElement)
			}
		case OpenBrace:
			p.lexer.consume()
			expr := p.expr()
			// TODO: handle the case when parseExpr() returns nil
			token = p.lexer.peek()
			if token.Type == CloseBrace {
				p.lexer.consume()
			} else {
				p.reportError(token.Span, "Expected '}'")
			}
			children = append(children, ast.NewJSXExprContainer(expr, token.Span))
		default:
			token := p.lexer.lexJSXText()
			text := ast.NewJSXText(token.Value, token.Span)
			children = append(children, text)
		}
	}
}
