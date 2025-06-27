package parser

import (
	"github.com/escalier-lang/escalier/internal/ast"
)

func (p *Parser) jsxElement() (*ast.JSXElementExpr, []*Error) {
	errors := []*Error{}

	opening, openErrors := p.jsxOpening()
	errors = append(errors, openErrors...)

	span := ast.Span{
		Start: opening.Span().Start,
		End:   opening.Span().End,
	}

	if !opening.SelfClose {
		children, childrenErrors := p.jsxChildren()
		errors = append(errors, childrenErrors...)
		closing, closingErrors := p.jsxClosing()
		errors = append(errors, closingErrors...)

		return ast.NewJSXElement(
			opening, closing, children, ast.MergeSpans(span, closing.Span()),
		), errors
	}

	return ast.NewJSXElement(
		opening, nil, []ast.JSXChild{}, span,
	), errors
}

func (p *Parser) jsxOpening() (*ast.JSXOpening, []*Error) {
	errors := []*Error{}
	token := p.lexer.next()
	if token.Type != LessThan {
		errors = append(errors, NewError(token.Span, "Expected '<'"))
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
		return ast.NewJSXOpening("", []*ast.JSXAttr{}, false, span), errors
	default:
		errors = append(errors, NewError(token.Span, "Expected an identifier or '>'"))
	}

	attrs, attrsErrors := p.jsxAttrs()
	errors = append(errors, attrsErrors...)

	var selfClosing bool

	token = p.lexer.next()

	//nolint: exhaustive
	switch token.Type {
	case SlashGreaterThan:
		selfClosing = true
	case GreaterThan:
		// do nothing
	default:
		errors = append(errors, NewError(token.Span, "Expected '>' or '/>'"))
	}

	end := token.Span.End

	span := ast.Span{
		Start: start,
		End:   end,
	}

	return ast.NewJSXOpening(name, attrs, selfClosing, span), errors
}

func (p *Parser) jsxAttrs() ([]*ast.JSXAttr, []*Error) {
	attrs := []*ast.JSXAttr{}
	errors := []*Error{}

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
			errors = append(errors, NewError(token.Span, "Expected '='"))
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
			expr, exprErrors := p.expr()
			errors = append(errors, exprErrors...)
			if expr == nil {
				errors := append(errors, NewError(token.Span, "Expected an expression after '{'"))
				return attrs, errors
			}
			value = ast.NewJSXExprContainer(expr, token.Span)
			token = p.lexer.peek()
			if token.Type == CloseBrace {
				p.lexer.consume() // consume '}'
			} else {
				errors = append(errors, NewError(token.Span, "Expected '}'"))
			}
		default:
			errors = append(errors, NewError(token.Span, "Expected a string or an expression"))
		}

		attr := ast.NewJSXAttr(name, &value, token.Span)
		attrs = append(attrs, attr)
	}

	return attrs, errors
}

func (p *Parser) jsxClosing() (*ast.JSXClosing, []*Error) {
	errors := []*Error{}

	token := p.lexer.next()
	if token.Type != LessThanSlash {
		errors = append(errors, NewError(token.Span, "Expected '</'"))
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
			Start: start,
			End:   end,
		}
		return ast.NewJSXClosing("", span), errors
	default:
		errors = append(errors, NewError(token.Span, "Expected an identifier or '>'"))
	}

	token = p.lexer.next()
	if token.Type != GreaterThan {
		errors = append(errors, NewError(token.Span, "Expected '>'"))
	}

	end := token.Span.End
	span := ast.Span{
		Start: start,
		End:   end,
	}

	return ast.NewJSXClosing(name, span), errors
}

func (p *Parser) jsxChildren() ([]ast.JSXChild, []*Error) {
	children := []ast.JSXChild{}
	errors := []*Error{}

	for {
		token := p.lexer.peek()

		//nolint: exhaustive
		switch token.Type {
		case LessThanSlash, EndOfFile:
			return children, errors
		case LessThan:
			jsxElement, jsxErrors := p.jsxElement()
			errors = append(errors, jsxErrors...)
			if jsxElement != nil {
				children = append(children, jsxElement)
			}
		case OpenBrace:
			p.lexer.consume()
			expr, exprErrors := p.expr()
			errors = append(errors, exprErrors...)
			// TODO: handle the case when parseExpr() returns nil
			token = p.lexer.peek()
			if token.Type == CloseBrace {
				p.lexer.consume()
			} else {
				errors = append(errors, NewError(token.Span, "Expected '}'"))
			}
			children = append(children, ast.NewJSXExprContainer(expr, token.Span))
		default:
			token := p.lexer.lexJSXText()
			text := ast.NewJSXText(token.Value, token.Span)
			children = append(children, text)
		}
	}
}
