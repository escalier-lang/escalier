package parser

import (
	"strconv"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/moznion/go-optional"
)

func (p *Parser) parsePattern(allowIdentDefault bool) optional.Option[ast.Pat] {
	token := p.lexer.peek()
	start := token.Span.Start

	// nolint: exhaustive
	switch token.Type {
	case Identifier:
		p.lexer.consume()
		name := token.Value // TODO: support qualified identifiers
		span := token.Span

		token = p.lexer.peek()
		if token.Type == OpenParen { // Extractor
			p.lexer.consume() // consume '('
			patArgs := parseDelimSeq(p, CloseParen, Comma, func() optional.Option[ast.Pat] {
				return p.parsePattern(true)
			})
			end := p.expect(CloseParen, AlwaysConsume)
			return optional.Some[ast.Pat](
				ast.NewExtractorPat(name, patArgs, ast.NewSpan(start, end)),
			)
		} else { // Ident
			_default := optional.None[ast.Expr]()
			if allowIdentDefault && token.Type == Equal {
				p.lexer.consume()
				e := p.parseExpr().Unwrap() // TODO: handle the case when parseExpr() returns None
				span = ast.MergeSpans(span, e.Span())
				_default = optional.Some(e)
			}
			return optional.Some[ast.Pat](
				ast.NewIdentPat(name, _default, span),
			)
		}
	case Underscore: // Wildcard
		p.lexer.consume()
		return optional.Some[ast.Pat](
			ast.NewWildcardPat(token.Span),
		)
	case OpenBracket: // Tuple
		p.lexer.consume() // consume '['
		patElems := parseDelimSeq(p, CloseBracket, Comma, func() optional.Option[ast.Pat] {
			return p.parsePattern(true)
		})
		end := p.expect(CloseBracket, AlwaysConsume)
		return optional.Some[ast.Pat](
			ast.NewTuplePat(patElems, ast.NewSpan(start, end)),
		)
	case OpenBrace: // Object
		p.lexer.consume() // consume '{'
		patElems := parseDelimSeq(p, CloseBrace, Comma, p.parseObjPatElem)
		end := p.expect(CloseBrace, AlwaysConsume)
		return optional.Some[ast.Pat](
			ast.NewObjectPat(patElems, ast.NewSpan(start, end)),
		)
	case DotDotDot: // Rest
		p.lexer.consume() // consume '...'
		pat := p.parsePattern(true)
		span := token.Span
		pat.IfNone(func() {
			p.reportError(token.Span, "Expected pattern")
		})
		return optional.Map(pat, func(pat ast.Pat) ast.Pat {
			span = ast.MergeSpans(span, pat.Span())
			return ast.NewRestPat(pat, span)
		})
	case String:
		p.lexer.consume()
		return optional.Some[ast.Pat](
			ast.NewLitPat(&ast.StrLit{Value: token.Value}, token.Span),
		)
	case Number:
		p.lexer.consume()
		value, err := strconv.ParseFloat(token.Value, 64)
		if err != nil {
			p.reportError(token.Span, "Invalid number")
			return optional.None[ast.Pat]()
		}
		return optional.Some[ast.Pat](
			ast.NewLitPat(&ast.NumLit{Value: value}, token.Span),
		)
	case True:
		p.lexer.consume()
		return optional.Some[ast.Pat](
			ast.NewLitPat(&ast.BoolLit{Value: true}, token.Span),
		)
	case False:
		p.lexer.consume()
		return optional.Some[ast.Pat](
			ast.NewLitPat(&ast.BoolLit{Value: false}, token.Span),
		)
	case Null:
		p.lexer.consume()
		return optional.Some[ast.Pat](
			ast.NewLitPat(&ast.NullLit{}, token.Span),
		)
	case Undefined:
		p.lexer.consume()
		return optional.Some[ast.Pat](
			ast.NewLitPat(&ast.UndefinedLit{}, token.Span),
		)
	default:
		// TODO: return an invalid pattern
		p.reportError(token.Span, "Expected a pattern")
		return optional.None[ast.Pat]()
	}
}

func (p *Parser) parseObjPatElem() optional.Option[ast.ObjPatElem] {
	token := p.lexer.peek()

	if token.Type == Identifier {
		p.lexer.consume()
		key := token.Value
		span := token.Span

		token = p.lexer.peek()
		if token.Type == Colon {
			p.lexer.consume()
			value := p.parsePattern(true)
			value.IfSome(func(v ast.Pat) {
				span = ast.MergeSpans(span, v.Span())
			})

			init := optional.None[ast.Expr]()
			token = p.lexer.peek()
			if token.Type == Equal {
				p.lexer.consume()
				e := p.parseExpr()
				e.IfSome(func(e ast.Expr) {
					span = ast.MergeSpans(span, e.Span())
				})
				init = e
			}

			return optional.Map(value, func(v ast.Pat) ast.ObjPatElem {
				span = ast.MergeSpans(span, v.Span())
				return ast.NewObjKeyValuePat(key, v, init, span)
			})
		} else {
			init := optional.None[ast.Expr]()
			token = p.lexer.peek()
			if token.Type == Equal {
				p.lexer.consume()
				e := p.parseExpr().Unwrap() // TODO: handle the case when parseExpr() returns None
				span = ast.MergeSpans(span, e.Span())
				init = optional.Some(e)
			}

			return optional.Some[ast.ObjPatElem](
				ast.NewObjShorthandPat(key, init, span),
			)
		}
	} else if token.Type == DotDotDot {
		p.lexer.consume()

		pat := p.parsePattern(true).Unwrap() // TODO: handle the case when parseExpr() returns None
		span := ast.MergeSpans(token.Span, pat.Span())
		return optional.Some[ast.ObjPatElem](ast.NewObjRestPat(pat, span))
	} else {
		p.reportError(token.Span, "Expected identifier or '...'")
		return optional.None[ast.ObjPatElem]()
	}
}
