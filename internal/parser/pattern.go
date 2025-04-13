package parser

import (
	"strconv"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/moznion/go-optional"
)

func (p *Parser) parsePattern(allowIdentDefault bool) (optional.Option[ast.Pat], []*Error) {
	token := p.lexer.peek()
	start := token.Span.Start

	errors := []*Error{}

	// nolint: exhaustive
	switch token.Type {
	case Identifier:
		p.lexer.consume()
		name := token.Value // TODO: support qualified identifiers
		span := token.Span

		token = p.lexer.peek()
		if token.Type == OpenParen { // Extractor
			p.lexer.consume() // consume '('
			patArgs, patErrors := parseDelimSeq(p, CloseParen, Comma, func() (optional.Option[ast.Pat], []*Error) {
				return p.parsePattern(true)
			})
			errors = append(errors, patErrors...)
			end, endErrors := p.expect(CloseParen, AlwaysConsume)
			errors = append(errors, endErrors...)
			return optional.Some[ast.Pat](
				ast.NewExtractorPat(name, patArgs, ast.NewSpan(start, end)),
			), errors
		} else { // Ident
			_default := optional.None[ast.Expr]()
			if allowIdentDefault && token.Type == Equal {
				p.lexer.consume()
				exprOption, exprErrors := p.parseExpr()
				errors = append(errors, exprErrors...)
				exprOption.IfSome(func(e ast.Expr) {
					span = ast.MergeSpans(span, e.Span())
					_default = optional.Some(e)
				})
			}
			return optional.Some[ast.Pat](
				ast.NewIdentPat(name, _default, span),
			), errors
		}
	case Underscore: // Wildcard
		p.lexer.consume()
		return optional.Some[ast.Pat](
			ast.NewWildcardPat(token.Span),
		), errors
	case OpenBracket: // Tuple
		p.lexer.consume() // consume '['
		patElems, patElemsErrors := parseDelimSeq(p, CloseBracket, Comma, func() (optional.Option[ast.Pat], []*Error) {
			return p.parsePattern(true)
		})
		errors = append(errors, patElemsErrors...)
		end, endErrors := p.expect(CloseBracket, AlwaysConsume)
		errors = append(errors, endErrors...)
		return optional.Some[ast.Pat](
			ast.NewTuplePat(patElems, ast.NewSpan(start, end)),
		), errors
	case OpenBrace: // Object
		p.lexer.consume() // consume '{'
		patElems, patElemsErrors := parseDelimSeq(p, CloseBrace, Comma, p.parseObjPatElem)
		errors = append(errors, patElemsErrors...)
		end, endErrors := p.expect(CloseBrace, AlwaysConsume)
		errors = append(errors, endErrors...)
		return optional.Some[ast.Pat](
			ast.NewObjectPat(patElems, ast.NewSpan(start, end)),
		), errors
	case DotDotDot: // Rest
		p.lexer.consume() // consume '...'
		pat, patErrors := p.parsePattern(true)
		errors = append(errors, patErrors...)
		span := token.Span
		pat.IfNone(func() {
			errors = append(errors, NewError(token.Span, "Expected pattern"))
		})
		return optional.Map(pat, func(pat ast.Pat) ast.Pat {
			span = ast.MergeSpans(span, pat.Span())
			return ast.NewRestPat(pat, span)
		}), errors
	case String:
		p.lexer.consume()
		return optional.Some[ast.Pat](
			ast.NewLitPat(&ast.StrLit{Value: token.Value}, token.Span),
		), errors
	case Number:
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

func (p *Parser) parseObjPatElem() (optional.Option[ast.ObjPatElem], []*Error) {
	token := p.lexer.peek()
	errors := []*Error{}

	if token.Type == Identifier {
		p.lexer.consume()
		key := token.Value
		span := token.Span

		token = p.lexer.peek()
		if token.Type == Colon {
			p.lexer.consume()
			value, valueError := p.parsePattern(true)
			errors = append(errors, valueError...)
			value.IfSome(func(v ast.Pat) {
				span = ast.MergeSpans(span, v.Span())
			})

			init := optional.None[ast.Expr]()
			token = p.lexer.peek()
			if token.Type == Equal {
				p.lexer.consume()
				exprOption, exprError := p.parseExpr()
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
				exprOption, exprError := p.parseExpr()
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

		pat, patErrors := p.parsePattern(true)
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
