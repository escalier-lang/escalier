package parser

import (
	"strconv"

	"github.com/escalier-lang/escalier/internal/ast"
)

// pattern = identifier | wildcard | tuple | object | rest | literal
func (p *Parser) pattern(allowIdentDefault bool) (ast.Pat, []*Error) {
	token := p.lexer.peek()
	errors := []*Error{}

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
		return ast.NewWildcardPat(token.Span), errors
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
func (p *Parser) extractorPat(nameToken *Token) (ast.Pat, []*Error) {
	errors := []*Error{}
	p.lexer.consume() // consume '('
	patArgs, patErrors := parseDelimSeqNonOptional(p, CloseParen, Comma, func() (ast.Pat, []*Error) {
		return p.pattern(true)
	})
	errors = append(errors, patErrors...)
	end, endErrors := p.expect(CloseParen, AlwaysConsume)
	errors = append(errors, endErrors...)
	return ast.NewExtractorPat(nameToken.Value, patArgs, ast.NewSpan(nameToken.Span.Start, end)), errors
}

// identPat = identifier ('=' expr)?
func (p *Parser) identPat(nameToken *Token, allowIdentDefault bool) (ast.Pat, []*Error) {
	errors := []*Error{}
	span := nameToken.Span
	token := p.lexer.peek()
	var _default ast.Expr
	if allowIdentDefault && token.Type == Equal {
		p.lexer.consume()
		exprOption, exprErrors := p.expr()
		errors = append(errors, exprErrors...)
		exprOption.IfSome(func(e ast.Expr) {
			span = ast.MergeSpans(span, e.Span())
			_default = e
		})
	}
	return ast.NewIdentPat(nameToken.Value, _default, span), errors
}

// tuplePat = '[' (pattern (',' pattern)*)? ']'
func (p *Parser) tuplePat() (ast.Pat, []*Error) {
	errors := []*Error{}
	token := p.lexer.peek()
	start := token.Span.Start
	p.lexer.consume() // consume '['
	patElems, patElemsErrors := parseDelimSeqNonOptional(p, CloseBracket, Comma, func() (ast.Pat, []*Error) {
		return p.pattern(true)
	})
	errors = append(errors, patElemsErrors...)
	end, endErrors := p.expect(CloseBracket, AlwaysConsume)
	errors = append(errors, endErrors...)
	return ast.NewTuplePat(patElems, ast.NewSpan(start, end)), errors
}

// objectPat = '{' (objPatElem (',' objPatElem)*)? '}'
func (p *Parser) objectPat() (ast.Pat, []*Error) {
	errors := []*Error{}
	token := p.lexer.peek()
	start := token.Span.Start
	p.lexer.consume() // consume '{'
	patElems, patElemsErrors := parseDelimSeqNonOptional(p, CloseBrace, Comma, p.objPatElem)
	errors = append(errors, patElemsErrors...)
	end, endErrors := p.expect(CloseBrace, AlwaysConsume)
	errors = append(errors, endErrors...)
	return ast.NewObjectPat(patElems, ast.NewSpan(start, end)), errors
}

// restPat = '...' pattern
func (p *Parser) restPat() (ast.Pat, []*Error) {
	errors := []*Error{}
	token := p.lexer.peek()
	p.lexer.consume() // consume '...'
	pat, patErrors := p.pattern(true)
	errors = append(errors, patErrors...)
	span := token.Span
	if pat == nil {
		errors = append(errors, NewError(token.Span, "Expected pattern"))
		return nil, errors
	}
	span = ast.MergeSpans(span, pat.Span())
	return ast.NewRestPat(pat, span), errors
}

// literalPat = string | number | 'true' | 'false' | 'null' | 'undefined'
func (p *Parser) literalPat() (ast.Pat, []*Error) {
	errors := []*Error{}
	token := p.lexer.peek()

	// nolint: exhaustive
	switch token.Type {
	case StrLit:
		p.lexer.consume()
		return ast.NewLitPat(&ast.StrLit{Value: token.Value}, token.Span), errors
	case NumLit:
		p.lexer.consume()
		value, err := strconv.ParseFloat(token.Value, 64)
		if err != nil {
			errors = append(errors, NewError(token.Span, "Invalid number"))
			return nil, errors
		}
		return ast.NewLitPat(&ast.NumLit{Value: value}, token.Span), errors
	case True:
		p.lexer.consume()
		return ast.NewLitPat(&ast.BoolLit{Value: true}, token.Span), errors
	case False:
		p.lexer.consume()
		return ast.NewLitPat(&ast.BoolLit{Value: false}, token.Span), errors
	case Null:
		p.lexer.consume()
		return ast.NewLitPat(&ast.NullLit{}, token.Span), errors
	case Undefined:
		p.lexer.consume()
		return ast.NewLitPat(&ast.UndefinedLit{}, token.Span), errors
	default:
		// TODO: return an invalid pattern
		errors = append(errors, NewError(token.Span, "Expected a pattern"))
		return nil, errors
	}
}

func (p *Parser) objPatElem() (ast.ObjPatElem, []*Error) {
	token := p.lexer.peek()
	errors := []*Error{}

	if token.Type == Identifier {
		p.lexer.consume()
		key := ast.NewIdentifier(token.Value, token.Span)
		span := token.Span

		token = p.lexer.peek()
		if token.Type == Colon {
			p.lexer.consume()
			value, valueError := p.pattern(true)
			errors = append(errors, valueError...)
			if value != nil {
				span = ast.MergeSpans(span, value.Span())
			}

			var init ast.Expr
			token = p.lexer.peek()
			if token.Type == Equal {
				p.lexer.consume()
				exprOption, exprError := p.expr()
				init = exprOption.Unwrap()
				errors = append(errors, exprError...)
				if init != nil {
					span = ast.MergeSpans(span, init.Span())
				}
			}

			if value == nil {
				return nil, errors
			}

			span = ast.MergeSpans(span, value.Span())
			return ast.NewObjKeyValuePat(key, value, init, span), errors
		} else {
			var init ast.Expr
			token = p.lexer.peek()
			if token.Type == Equal {
				p.lexer.consume()
				exprOption, exprError := p.expr()
				errors = append(errors, exprError...)
				exprOption.IfSome(func(e ast.Expr) {
					span = ast.MergeSpans(span, e.Span())
					init = e
				})
			}

			return ast.NewObjShorthandPat(key, init, span), errors
		}
	} else if token.Type == DotDotDot {
		p.lexer.consume()

		pat, patErrors := p.pattern(true)
		errors = append(errors, patErrors...)
		if pat == nil {
			return nil, errors
		}
		span := ast.MergeSpans(token.Span, pat.Span())
		return ast.NewObjRestPat(pat, span), errors
	} else {
		errors = append(errors, NewError(token.Span, "Expected identifier or '...'"))
		return nil, errors
	}
}
