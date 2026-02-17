package parser

import (
	"github.com/escalier-lang/escalier/internal/ast"
)

// jsxElementOrFragment parses either a JSX element or a JSX fragment.
// Fragments are detected by <> (LessThan followed immediately by GreaterThan).
func (p *Parser) jsxElementOrFragment() ast.Expr {
	// Peek ahead to detect fragment: <>
	// We need to check if < is followed immediately by >
	first := p.lexer.peek()
	if first.Type != LessThan {
		p.reportError(first.Span, "Expected '<'")
		return nil
	}

	// Save position and peek at next token
	p.lexer.consume() // consume '<'
	second := p.lexer.peek()

	if second.Type == GreaterThan {
		// This is a fragment: <>
		return p.jsxFragmentAfterOpening(first.Span.Start)
	}

	// This is a regular element, continue parsing
	return p.jsxElementAfterLessThan(first.Span.Start)
}

// jsxFragmentAfterOpening parses a fragment after the < has been consumed.
// Called when we've seen < and peeked > (fragment opening).
func (p *Parser) jsxFragmentAfterOpening(start ast.Location) *ast.JSXFragmentExpr {
	// Consume the >
	token := p.lexer.next()
	end := token.Span.End

	openingSpan := ast.Span{
		Start:    start,
		End:      end,
		SourceID: p.lexer.source.ID,
	}
	opening := ast.NewJSXOpening("", []ast.JSXAttrElem{}, false, openingSpan)

	// Parse children
	children := p.jsxChildren()

	// Parse closing </>
	closing := p.jsxClosing()

	span := ast.MergeSpans(openingSpan, closing.Span())
	return ast.NewJSXFragment(opening, closing, children, span)
}

// jsxElementAfterLessThan parses an element after the < has been consumed.
func (p *Parser) jsxElementAfterLessThan(start ast.Location) *ast.JSXElementExpr {
	opening := p.jsxOpeningAfterLessThan(start)

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
		return ast.NewJSXOpening("", []ast.JSXAttrElem{}, false, span)
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

// jsxOpeningAfterLessThan parses a JSX opening tag after < has been consumed.
// This is called when we've already determined this is NOT a fragment.
func (p *Parser) jsxOpeningAfterLessThan(start ast.Location) *ast.JSXOpening {
	var name string
	token := p.lexer.next()

	//nolint: exhaustive
	switch token.Type {
	case Identifier:
		name = token.Value
	default:
		p.reportError(token.Span, "Expected an identifier")
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

func (p *Parser) jsxAttrs() []ast.JSXAttrElem {
	attrs := []ast.JSXAttrElem{}

	for {
		// Check if context has been cancelled (timeout or cancellation)
		select {
		case <-p.ctx.Done():
			// Return what we have so far when context is done
			return attrs
		default:
			// continue
		}

		token := p.lexer.peek()

		// Check for spread attribute: {...expr}
		if token.Type == OpenBrace {
			start := token.Span.Start
			p.lexer.consume() // consume '{'

			token = p.lexer.peek()
			if token.Type == DotDotDot {
				p.lexer.consume() // consume '...'
				expr := p.expr()
				if expr == nil {
					return attrs
				}

				token = p.lexer.peek()
				end := token.Span.End
				if token.Type == CloseBrace {
					p.lexer.consume() // consume '}'
				} else {
					p.reportError(token.Span, "Expected '}'")
				}

				span := ast.Span{Start: start, End: end, SourceID: p.lexer.source.ID}
				attrs = append(attrs, ast.NewJSXSpreadAttr(expr, span))
				continue
			} else {
				// Not a spread, this is an error - we consumed '{' but didn't find '...'
				p.reportError(token.Span, "Expected '...' for spread attribute")
				return attrs
			}
		}

		// Regular named attribute
		name := ""
		if token.Type == Identifier {
			p.lexer.consume() // consume identifier
			name = token.Value
		} else {
			break
		}

		// Check for attribute value (optional for boolean shorthand)
		token = p.lexer.peek()
		if token.Type == Equal {
			p.lexer.consume() // consume equals

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
		} else {
			// Boolean shorthand: <input disabled />
			attr := ast.NewJSXAttr(name, nil, token.Span)
			attrs = append(attrs, attr)
		}
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
		// Check if context has been cancelled (timeout or cancellation)
		select {
		case <-p.ctx.Done():
			// Return what we have so far when context is done
			return children
		default:
			// continue
		}

		token := p.lexer.peek()

		//nolint: exhaustive
		switch token.Type {
		case LessThanSlash, EndOfFile:
			return children
		case LessThan:
			jsx := p.jsxElementOrFragment()
			if jsx != nil {
				children = append(children, jsx.(ast.JSXChild))
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
			// Try to lex JSX text at the current position
			jsxToken := p.lexer.lexJSXText()
			// If lexJSXText returns empty content, we have a token that was already
			// lexed (like <= after a malformed JSX tag). Consume it to avoid infinite loop.
			if jsxToken.Value == "" {
				p.lexer.consume()
				p.reportError(token.Span, "Unexpected token in JSX children")
				// Use the token's value as text to recover
				text := ast.NewJSXText(token.Value, token.Span)
				children = append(children, text)
			} else {
				text := ast.NewJSXText(jsxToken.Value, jsxToken.Span)
				children = append(children, text)
			}
		}
	}
}
