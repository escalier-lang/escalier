package parser

import (
	"strconv"

	"github.com/escalier-lang/escalier/internal/ast"
)

// pattern = identifier | wildcard | tuple | object | rest | literal
func (p *Parser) pattern(allowIdentDefault bool) ast.Pat {
	token := p.lexer.peek()

	// nolint: exhaustive
	switch token.Type {
	case Identifier:
		p.lexer.consume()
		next := p.lexer.peek()
		if next.Type == OpenParen {
			return p.extractorPat(token)
		} else {
			return p.identPat(token, allowIdentDefault)
		}
	case Underscore:
		p.lexer.consume()
		return ast.NewWildcardPat(token.Span)
	case OpenBracket:
		return p.tuplePat()
	case OpenBrace:
		return p.objectPat()
	case DotDotDot:
		return p.restPat()
	default:
		return p.literalPat()
	}
}

// extractorPat = identifier '(' (pattern (',' pattern)*)? ')'
func (p *Parser) extractorPat(nameToken *Token) ast.Pat {
	p.lexer.consume() // consume '('
	patArgs := parseDelimSeq(p, CloseParen, Comma, func() ast.Pat {
		return p.pattern(true)
	})
	end := p.expect(CloseParen, AlwaysConsume)
	span := ast.NewSpan(nameToken.Span.Start, end, p.lexer.source.ID)
	return ast.NewExtractorPat(nameToken.Value, patArgs, span)
}

// identPat = identifier ('=' expr)?
func (p *Parser) identPat(nameToken *Token, allowIdentDefault bool) ast.Pat {
	span := nameToken.Span
	token := p.lexer.peek()
	var _default ast.Expr
	if allowIdentDefault && token.Type == Equal {
		p.lexer.consume()
		expr := p.expr()
		if expr != nil {
			span = ast.MergeSpans(span, expr.Span())
			_default = expr
		}
	}
	return ast.NewIdentPat(nameToken.Value, _default, span)
}

// tuplePat = '[' (pattern (',' pattern)*)? ']'
func (p *Parser) tuplePat() ast.Pat {
	token := p.lexer.peek()
	start := token.Span.Start
	p.lexer.consume() // consume '['
	patElems := parseDelimSeq(p, CloseBracket, Comma, func() ast.Pat {
		return p.pattern(true)
	})
	end := p.expect(CloseBracket, AlwaysConsume)
	return ast.NewTuplePat(patElems, ast.NewSpan(start, end, p.lexer.source.ID))
}

// objectPat = '{' (objPatElem (',' objPatElem)*)? '}'
func (p *Parser) objectPat() ast.Pat {
	token := p.lexer.peek()
	start := token.Span.Start
	p.lexer.consume() // consume '{'
	patElems := parseDelimSeq(p, CloseBrace, Comma, p.objPatElem)
	end := p.expect(CloseBrace, AlwaysConsume)
	return ast.NewObjectPat(patElems, ast.NewSpan(start, end, p.lexer.source.ID))
}

// restPat = '...' pattern
func (p *Parser) restPat() ast.Pat {
	token := p.lexer.peek()
	p.lexer.consume() // consume '...'
	pat := p.pattern(true)
	span := token.Span
	if pat == nil {
		p.reportError(token.Span, "Expected pattern")
		return nil
	}
	span = ast.MergeSpans(span, pat.Span())
	return ast.NewRestPat(pat, span)
}

// literalPat = string | number | regex | 'true' | 'false' | 'null' | 'undefined'
func (p *Parser) literalPat() ast.Pat {
	token := p.lexer.peek()

	// nolint: exhaustive
	switch token.Type {
	case StrLit:
		p.lexer.consume()
		return ast.NewLitPat(&ast.StrLit{Value: token.Value}, token.Span)
	case NumLit:
		p.lexer.consume()
		value, err := strconv.ParseFloat(token.Value, 64)
		if err != nil {
			p.reportError(token.Span, "Invalid number")
			return nil
		}
		return ast.NewLitPat(&ast.NumLit{Value: value}, token.Span)
	case True:
		p.lexer.consume()
		return ast.NewLitPat(&ast.BoolLit{Value: true}, token.Span)
	case False:
		p.lexer.consume()
		return ast.NewLitPat(&ast.BoolLit{Value: false}, token.Span)
	case Null:
		p.lexer.consume()
		return ast.NewLitPat(&ast.NullLit{}, token.Span)
	case Undefined:
		p.lexer.consume()
		return ast.NewLitPat(&ast.UndefinedLit{}, token.Span)
	case RegexLit:
		p.lexer.consume()
		return ast.NewLitPat(&ast.RegexLit{Value: token.Value}, token.Span)
	default:
		// TODO: return an invalid pattern
		p.reportError(token.Span, "Expected a pattern")
		return nil
	}
}

func (p *Parser) objPatElem() ast.ObjPatElem {
	token := p.lexer.peek()

	// nolint: exhaustive
	switch token.Type {
	case Identifier:
		p.lexer.consume()
		key := ast.NewIdentifier(token.Value, token.Span)
		span := token.Span

		token = p.lexer.peek()
		if token.Type == Colon {
			p.lexer.consume()
			value := p.pattern(true)
			if value != nil {
				span = ast.MergeSpans(span, value.Span())
			}

			var init ast.Expr
			token = p.lexer.peek()
			if token.Type == Equal {
				p.lexer.consume()
				expr := p.expr()
				init = expr
				if init != nil {
					span = ast.MergeSpans(span, init.Span())
				}
			}

			if value == nil {
				return nil
			}

			span = ast.MergeSpans(span, value.Span())
			return ast.NewObjKeyValuePat(key, value, init, span)
		} else {
			var init ast.Expr
			token = p.lexer.peek()
			if token.Type == Equal {
				p.lexer.consume()
				expr := p.expr()
				if expr != nil {
					span = ast.MergeSpans(span, expr.Span())
					init = expr
				}
			}

			return ast.NewObjShorthandPat(key, init, span)
		}
	case DotDotDot:
		p.lexer.consume()

		pat := p.pattern(true)
		if pat == nil {
			return nil
		}
		span := ast.MergeSpans(token.Span, pat.Span())
		return ast.NewObjRestPat(pat, span)
	default:
		p.reportError(token.Span, "Expected identifier or '...'")
		return nil
	}
}
