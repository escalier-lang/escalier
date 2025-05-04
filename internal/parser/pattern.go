package parser

import (
	"strconv"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/moznion/go-optional"
)

// pattern = identifier | wildcard | tuple | object | rest | literal
func (p *Parser) pattern(allowIdentDefault bool) (optional.Option[ast.Pat], []*Error) {
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
		return optional.Some[ast.Pat](
			ast.NewWildcardPat(token.Span),
		), errors
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
func (p *Parser) extractorPat(nameToken *Token) (optional.Option[ast.Pat], []*Error) {
	errors := []*Error{}
	p.lexer.consume() // consume '('
	patArgs, patErrors := parseDelimSeq(p, CloseParen, Comma, func() (optional.Option[ast.Pat], []*Error) {
		return p.pattern(true)
	})
	errors = append(errors, patErrors...)
	end, endErrors := p.expect(CloseParen, AlwaysConsume)
	errors = append(errors, endErrors...)
	return optional.Some[ast.Pat](
		ast.NewExtractorPat(nameToken.Value, patArgs, ast.NewSpan(nameToken.Span.Start, end)),
	), errors
}

// identPat = identifier ('=' expr)?
func (p *Parser) identPat(nameToken *Token, allowIdentDefault bool) (optional.Option[ast.Pat], []*Error) {
	errors := []*Error{}
	span := nameToken.Span
	token := p.lexer.peek()
	_default := optional.None[ast.Expr]()
	if allowIdentDefault && token.Type == Equal {
		p.lexer.consume()
		exprOption, exprErrors := p.expr()
		errors = append(errors, exprErrors...)
		exprOption.IfSome(func(e ast.Expr) {
			span = ast.MergeSpans(span, e.Span())
			_default = optional.Some(e)
		})
	}
	return optional.Some[ast.Pat](
		ast.NewIdentPat(nameToken.Value, _default, span),
	), errors
}

// tuplePat = '[' (pattern (',' pattern)*)? ']'
func (p *Parser) tuplePat() (optional.Option[ast.Pat], []*Error) {
	errors := []*Error{}
	token := p.lexer.peek()
	start := token.Span.Start
	p.lexer.consume() // consume '['
	patElems, patElemsErrors := parseDelimSeq(p, CloseBracket, Comma, func() (optional.Option[ast.Pat], []*Error) {
		return p.pattern(true)
	})
	errors = append(errors, patElemsErrors...)
	end, endErrors := p.expect(CloseBracket, AlwaysConsume)
	errors = append(errors, endErrors...)
	return optional.Some[ast.Pat](
		ast.NewTuplePat(patElems, ast.NewSpan(start, end)),
	), errors
}

// objectPat = '{' (objPatElem (',' objPatElem)*)? '}'
func (p *Parser) objectPat() (optional.Option[ast.Pat], []*Error) {
	errors := []*Error{}
	token := p.lexer.peek()
	start := token.Span.Start
	p.lexer.consume() // consume '{'
	patElems, patElemsErrors := parseDelimSeq(p, CloseBrace, Comma, p.objPatElem)
	errors = append(errors, patElemsErrors...)
	end, endErrors := p.expect(CloseBrace, AlwaysConsume)
	errors = append(errors, endErrors...)
	return optional.Some[ast.Pat](
		ast.NewObjectPat(patElems, ast.NewSpan(start, end)),
	), errors
}

// restPat = '...' pattern
func (p *Parser) restPat() (optional.Option[ast.Pat], []*Error) {
	errors := []*Error{}
	token := p.lexer.peek()
	p.lexer.consume() // consume '...'
	pat, patErrors := p.pattern(true)
	errors = append(errors, patErrors...)
	span := token.Span
	pat.IfNone(func() {
		errors = append(errors, NewError(token.Span, "Expected pattern"))
	})
	return optional.Map(pat, func(pat ast.Pat) ast.Pat {
		span = ast.MergeSpans(span, pat.Span())
		return ast.NewRestPat(pat, span)
	}), errors
}

// literalPat = string | number | 'true' | 'false' | 'null' | 'undefined'
func (p *Parser) literalPat() (optional.Option[ast.Pat], []*Error) {
	errors := []*Error{}
	token := p.lexer.peek()

	// nolint: exhaustive
	switch token.Type {
	case StrLit:
		p.lexer.consume()
		return optional.Some[ast.Pat](
			ast.NewLitPat(&ast.StrLit{Value: token.Value}, token.Span),
		), errors
	case NumLit:
		p.lexer.consume()
		value, err := strconv.ParseFloat(token.Value, 64)
		if err != nil {
			errors = append(errors, NewError(token.Span, "Invalid number"))
			return optional.None[ast.Pat](), errors
		}
		return optional.Some[ast.Pat](
			ast.NewLitPat(&ast.NumLit{Value: value}, token.Span),
		), errors
	case True:
		p.lexer.consume()
		return optional.Some[ast.Pat](
			ast.NewLitPat(&ast.BoolLit{Value: true}, token.Span),
		), errors
	case False:
		p.lexer.consume()
		return optional.Some[ast.Pat](
			ast.NewLitPat(&ast.BoolLit{Value: false}, token.Span),
		), errors
	case Null:
		p.lexer.consume()
		return optional.Some[ast.Pat](
			ast.NewLitPat(&ast.NullLit{}, token.Span),
		), errors
	case Undefined:
		p.lexer.consume()
		return optional.Some[ast.Pat](
			ast.NewLitPat(&ast.UndefinedLit{}, token.Span),
		), errors
	default:
		// TODO: return an invalid pattern
		errors = append(errors, NewError(token.Span, "Expected a pattern"))
		return optional.None[ast.Pat](), errors
	}
}

func (p *Parser) objPatElem() (optional.Option[ast.ObjPatElem], []*Error) {
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
			value.IfSome(func(v ast.Pat) {
				span = ast.MergeSpans(span, v.Span())
			})

			init := optional.None[ast.Expr]()
			token = p.lexer.peek()
			if token.Type == Equal {
				p.lexer.consume()
				exprOption, exprError := p.expr()
				init = exprOption
				errors = append(errors, exprError...)
				init.IfSome(func(e ast.Expr) {
					span = ast.MergeSpans(span, e.Span())
				})
			}

			return optional.Map(value, func(v ast.Pat) ast.ObjPatElem {
				span = ast.MergeSpans(span, v.Span())
				return ast.NewObjKeyValuePat(key, v, init, span)
			}), errors
		} else {
			init := optional.None[ast.Expr]()
			token = p.lexer.peek()
			if token.Type == Equal {
				p.lexer.consume()
				exprOption, exprError := p.expr()
				errors = append(errors, exprError...)
				exprOption.IfSome(func(e ast.Expr) {
					span = ast.MergeSpans(span, e.Span())
					init = optional.Some(e)
				})
			}

			return optional.Some[ast.ObjPatElem](
				ast.NewObjShorthandPat(key, init, span),
			), errors
		}
	} else if token.Type == DotDotDot {
		p.lexer.consume()

		pat, patErrors := p.pattern(true)
		errors = append(errors, patErrors...)
		return optional.Map(pat, func(pat ast.Pat) ast.ObjPatElem {
			span := ast.MergeSpans(token.Span, pat.Span())
			return ast.NewObjRestPat(pat, span)
		}), errors
	} else {
		errors = append(errors, NewError(token.Span, "Expected identifier or '...'"))
		return optional.None[ast.ObjPatElem](), errors
	}
}
