package parser

import (
	"github.com/escalier-lang/escalier/internal/ast"
)

func (p *Parser) parseJSXElement() *ast.JSXElementExpr {
	opening := p.parseJSXOpening()

	span := ast.Span{
		Start: opening.Span().Start,
		End:   opening.Span().End,
	}

	var children []ast.JSXChild
	var closing *ast.JSXClosing

	if !opening.SelfClose {
		children = p.parseJSXChildren()
		closing = p.parseJSXClosing()
		span.End = closing.Span().End
	}

	return ast.NewJSXElement(opening, closing, children, span)
}

func (p *Parser) parseJSXOpening() *ast.JSXOpening {
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
			Start: start,
			End:   end,
		}
		return ast.NewJSXOpening("", nil, false, span)
	default:
		p.reportError(token.Span, "Expected an identifier or '>'")
	}

	attrs := p.parseJSXAttrs()

	var selfClosing bool

	token = p.lexer.next()

	//nolint: exhaustive
	switch token.Type {
	case SlashGreaterThan:
		selfClosing = true
	case GreaterThan:
		// do nothing
	default:
		p.reportError(token.Span, "Expected '/' or '/>'")
	}

	end := token.Span.End

	span := ast.Span{
		Start: start,
		End:   end,
	}

	return ast.NewJSXOpening(name, attrs, selfClosing, span)
}

func (p *Parser) parseJSXAttrs() []*ast.JSXAttr {
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
		case String:
			p.lexer.consume() // consume string
			value = ast.NewJSXString(token.Value, token.Span)
		case OpenBrace:
			p.lexer.consume() // consume '{'
			expr := p.parseExpr()
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

func (p *Parser) parseJSXClosing() *ast.JSXClosing {
	token := p.lexer.next()
	if token.Type != LessThanSlash {
		p.reportError(token.Span, "Expected '</'")
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
			Start: start,
			End:   end,
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
		Start: start,
		End:   end,
	}

	return ast.NewJSXClosing(name, span)
}

func (p *Parser) parseJSXChildren() []ast.JSXChild {
	children := []ast.JSXChild{}

	for {
		token := p.lexer.peek()

		//nolint: exhaustive
		switch token.Type {
		case LessThanSlash, EndOfFile:
			return children
		case LessThan:
			jsxElement := p.parseJSXElement()
			children = append(children, jsxElement)
		case OpenBrace:
			p.lexer.consume()
			expr := p.parseExpr()
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
