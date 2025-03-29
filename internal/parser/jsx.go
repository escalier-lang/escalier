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
	if _, ok := token.(*TLessThan); !ok {
		p.reportError(token.Span(), "Expected '<'")
	}

	var name string
	token = p.lexer.next()
	switch t := token.(type) {
	case *TIdentifier:
		p.lexer.consume() // consume identifier
		name = t.Value
	case *TGreaterThan:
		p.lexer.consume() // consume '>'
		return ast.NewJSXOpening("", nil, false, token.Span())
	default:
		p.reportError(token.Span(), "Expected an identifier or '>'")
	}

	attrs := p.parseJSXAttrs()

	var selfClosing bool

	token = p.lexer.next()
	switch token.(type) {
	case *TSlashGreaterThan:
		selfClosing = true
	case *TGreaterThan:
		// do nothing
	default:
		p.reportError(token.Span(), "Expected '/' or '/>'")
	}

	return ast.NewJSXOpening(name, attrs, selfClosing, token.Span())
}

func (p *Parser) parseJSXAttrs() []*ast.JSXAttr {
	attrs := []*ast.JSXAttr{}

	for {
		token := p.lexer.peek()
		name := ""
		if ident, ok := token.(*TIdentifier); ok {
			p.lexer.consume() // consume identifier
			name = ident.Value
		} else {
			break
		}

		// parse attribute value
		token = p.lexer.peek()
		if _, ok := token.(*TEquals); ok {
			p.lexer.consume() // consume equals
		} else {
			p.reportError(token.Span(), "Expected '='")
		}

		var value ast.JSXAttrValue

		// parse attribute value
		token = p.lexer.peek()
		switch t := token.(type) {
		case *TString:
			p.lexer.consume() // consume string
			value = ast.NewJSXString(t.Value, token.Span())
		case *TOpenBrace:
			p.lexer.consume() // consume '{'
			expr := p.ParseExpr()
			value = ast.NewJSXExprContainer(expr, token.Span())
			token = p.lexer.peek()
			if _, ok := token.(*TCloseBrace); ok {
				p.lexer.consume() // consume '}'
			} else {
				p.reportError(token.Span(), "Expected '}'")
			}
		default:
			p.reportError(token.Span(), "Expected a string or an expression")
		}

		attr := ast.NewJSXAttr(name, &value, token.Span())
		attrs = append(attrs, attr)
	}

	return attrs
}

func (p *Parser) parseJSXClosing() *ast.JSXClosing {
	token := p.lexer.next()
	if _, ok := token.(*TLessThanSlash); !ok {
		p.reportError(token.Span(), "Expected '</'")
	}

	var name string
	token = p.lexer.next()
	switch token.(type) {
	case *TIdentifier:
		p.lexer.consume() // consume identifier
		name = token.(*TIdentifier).Value
	case *TGreaterThan:
		p.lexer.consume() // consume '>'
		return ast.NewJSXClosing("", token.Span())
	default:
		p.reportError(token.Span(), "Expected an identifier or '>'")
	}

	token = p.lexer.next()
	if _, ok := token.(*TGreaterThan); !ok {
		p.reportError(token.Span(), "Expected '>'")
	}

	return ast.NewJSXClosing(name, token.Span())
}

func (p *Parser) parseJSXChildren() []ast.JSXChild {
	children := []ast.JSXChild{}

	for {
		token := p.lexer.peek()

		switch token.(type) {
		case *TLessThanSlash, *TEndOfFile:
			return children
		case *TLessThan:
			jsxElement := p.parseJSXElement()
			children = append(children, jsxElement)
		case *TOpenBrace:
			p.lexer.consume()
			expr := p.ParseExpr()
			token = p.lexer.peek()
			if _, ok := token.(*TCloseBrace); ok {
				p.lexer.consume()
			} else {
				p.reportError(token.Span(), "Expected '}'")
			}
			children = append(children, ast.NewJSXExprContainer(expr, token.Span()))
		default:
			token := p.lexer.lexJSXText()
			text := ast.NewJSXText(token.Value, token.Span())
			children = append(children, text)
		}
	}
}
