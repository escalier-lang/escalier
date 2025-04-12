package parser

import (
	"strconv"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/moznion/go-optional"
)

func (p *Parser) parsePattern(allowIdentDefault bool) ast.Pat {
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
			patArgs := parseDelimSeq(p, CloseParen, Comma, func() ast.Pat {
				return p.parsePattern(true)
			})
			end := p.expect(CloseParen, AlwaysConsume)
			return ast.NewExtractorPat(name, patArgs, ast.NewSpan(start, end))
		} else { // Ident
			_default := optional.None[ast.Expr]()
			if allowIdentDefault && token.Type == Equal {
				p.lexer.consume()
				e := p.parseExpr()
				span = ast.MergeSpans(span, e.Span())
				_default = optional.Some(e)
			}
			return ast.NewIdentPat(name, _default, span)
		}
	case Underscore: // Wildcard
		p.lexer.consume()
		return ast.NewWildcardPat(token.Span)
	case OpenBracket: // Tuple
		p.lexer.consume() // consume '['
		patElems := parseDelimSeq(p, CloseBracket, Comma, func() ast.Pat {
			return p.parsePattern(true)
		})
		end := p.expect(CloseBracket, AlwaysConsume)
		return ast.NewTuplePat(patElems, ast.NewSpan(start, end))
	case OpenBrace: // Object
		p.lexer.consume() // consume '{'
		patElems := parseDelimSeq(p, CloseBrace, Comma, p.parseObjPatElem)
		end := p.expect(CloseBrace, AlwaysConsume)
		return ast.NewObjectPat(patElems, ast.NewSpan(start, end))
	case DotDotDot: // Rest
		p.lexer.consume() // consume '...'
		pat := p.parsePattern(true)
		span := token.Span
		if pat == nil {
			p.reportError(token.Span, "Expected pattern")
			return nil
		} else {
			span = ast.MergeSpans(span, pat.Span())
		}
		return ast.NewRestPat(pat, span)
	case String:
		p.lexer.consume()
		return ast.NewLitPat(&ast.StrLit{Value: token.Value}, token.Span)
	case Number:
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
	default:
		// TODO: return an invalid pattern
		p.reportError(token.Span, "Expected a pattern")
		return nil
	}
}

func (p *Parser) parseObjPatElem() ast.ObjPatElem {
	token := p.lexer.peek()

	if token.Type == Identifier {
		p.lexer.consume()
		key := token.Value
		span := token.Span

		token = p.lexer.peek()
		if token.Type == Colon {
			p.lexer.consume()
			value := p.parsePattern(true)
			span = ast.MergeSpans(span, value.Span())

			init := optional.None[ast.Expr]()
			token = p.lexer.peek()
			if token.Type == Equal {
				p.lexer.consume()
				e := p.parseExpr()
				span = ast.MergeSpans(span, e.Span())
				init = optional.Some(e)
			}

			return ast.NewObjKeyValuePat(key, value, init, span)
		} else {
			init := optional.None[ast.Expr]()
			token = p.lexer.peek()
			if token.Type == Equal {
				p.lexer.consume()
				e := p.parseExpr()
				span = ast.MergeSpans(span, e.Span())
				init = optional.Some(e)
			}

			return ast.NewObjShorthandPat(key, init, span)
		}
	} else if token.Type == DotDotDot {
		p.lexer.consume()

		pat := p.parsePattern(true)
		span := ast.MergeSpans(token.Span, pat.Span())
		return ast.NewObjRestPat(pat, span)
	} else {
		p.reportError(token.Span, "Expected identifier or '...'")
		return nil
	}
}
