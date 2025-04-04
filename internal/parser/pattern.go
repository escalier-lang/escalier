package parser

import (
	"fmt"
	"strconv"

	"github.com/escalier-lang/escalier/internal/ast"
)

func (p *Parser) parsePattern() ast.Pat {
	token := p.lexer.peek()

	//nolint: exhaustive
	switch token.Type {
	case Identifier:
		p.lexer.consume()
		name := token.Value // TODO: support qualified identifiers
		span := token.Span

		token = p.lexer.peek()
		if token.Type == OpenParen {
			p.lexer.consume() // consume '('

			patArgs := []ast.ExtractorPatArg{}
			first := true
			for {
				token = p.lexer.peek()
				if token.Type == CloseParen {
					p.lexer.consume()
					break
				}
				if !first {
					if token.Type != Comma {
						msg := fmt.Sprintf("Expected ',', got '%s'", token.Value)
						p.reportError(token.Span, msg)
					} else {
						p.lexer.consume()
						token = p.lexer.peek()
					}
				}
				if token.Type == DotDotDot {
					p.lexer.consume()
					span := token.Span

					var identPat *ast.IdentPat
					token = p.lexer.peek()
					if token.Type == Identifier {
						p.lexer.consume()
						identPat = ast.NewIdentPat(token.Value, token.Span)
						span = ast.MergeSpans(span, identPat.Span())
					} else {
						p.reportError(token.Span, "Expected identifier")
						identPat = ast.NewIdentPat("", token.Span)
					}
					patArgs = append(patArgs, ast.NewExtractorRestArgPat(identPat, span))
				} else {
					pat := p.parsePattern()
					span := pat.Span()

					var init ast.Expr
					token = p.lexer.peek()
					if token.Type == Equal {
						p.lexer.consume()
						init = p.ParseExprWithMarker(MarkerDelim)
						span = ast.MergeSpans(span, init.Span())
					}
					patArgs = append(patArgs, ast.NewExtractorArgPat(pat, init, span))
				}
				first = false
			}

			end := token.Span.End
			span = ast.NewSpan(span.Start, end)
			return ast.NewExtractorPat(name, patArgs, span)
		} else {
			return ast.NewIdentPat(name, span)
		}
	case Underscore: // Wildcard
		p.lexer.consume()
		return ast.NewWildcardPat(token.Span)
	case OpenBracket: // Tuple
		start := token.Span.Start
		p.lexer.consume() // consume '['
		patElems := []ast.TuplePatElem{}
		first := true
		for {
			token = p.lexer.peek()
			if token.Type == CloseBracket {
				p.lexer.consume()
				break
			}
			if !first {
				if token.Type != Comma {
					msg := fmt.Sprintf("Expected ',', got '%s'", token.Value)
					p.reportError(token.Span, msg)
				} else {
					p.lexer.consume()
					token = p.lexer.peek()
				}
			}
			if token.Type == DotDotDot {
				p.lexer.consume()
				span := token.Span

				var identPat *ast.IdentPat
				token = p.lexer.peek()
				if token.Type == Identifier {
					p.lexer.consume()
					identPat = ast.NewIdentPat(token.Value, token.Span)
					span = ast.MergeSpans(span, identPat.Span())
				} else {
					p.reportError(token.Span, "Expected identifier")
					identPat = ast.NewIdentPat("", token.Span)
				}
				patElems = append(patElems, ast.NewTupleRestPat(identPat, span))
			} else {
				pat := p.parsePattern()
				span := pat.Span()

				var init ast.Expr
				token = p.lexer.peek()
				if token.Type == Equal {
					p.lexer.consume()
					init = p.ParseExprWithMarker(MarkerDelim)
					span = ast.MergeSpans(span, init.Span())
				}
				patElems = append(patElems, ast.NewTupleElemPat(pat, init, span))
			}
			first = false
		}
		end := token.Span.End
		return ast.NewTuplePat(patElems, ast.NewSpan(start, end))
	case OpenBrace: // Object
		start := token.Span.Start
		p.lexer.consume()
		patElems := []ast.ObjPatElem{}
		first := true
		for {
			token = p.lexer.peek()
			if token.Type == CloseBrace {
				p.lexer.consume()
				break
			}
			if !first {
				if token.Type != Comma {
					p.reportError(token.Span, "Expected ','")
					return nil
				}
				p.lexer.consume()
				token = p.lexer.peek()
			}
			if token.Type == Identifier {
				p.lexer.consume()
				key := token.Value
				span := token.Span

				token = p.lexer.peek()
				if token.Type == Colon {
					p.lexer.consume()
					value := p.parsePattern()
					span = ast.MergeSpans(span, value.Span())

					var init ast.Expr
					token = p.lexer.peek()
					if token.Type == Equal {
						p.lexer.consume()
						init = p.ParseExprWithMarker(MarkerDelim)
						span = ast.MergeSpans(span, init.Span())
					}

					patElems = append(patElems, ast.NewObjKeyValuePat(key, value, init, span))
				} else {
					var init ast.Expr
					token = p.lexer.peek()
					if token.Type == Equal {
						p.lexer.consume()
						init = p.ParseExprWithMarker(MarkerDelim)
						span = ast.MergeSpans(span, init.Span())
					}

					patElems = append(patElems, ast.NewObjShorthandPat(key, init, span))
				}
			} else if token.Type == DotDotDot {
				p.lexer.consume()

				pat := p.parsePattern()
				span := ast.MergeSpans(token.Span, pat.Span())
				patElems = append(patElems, ast.NewObjRestPat(pat, span))
			} else {
				p.reportError(token.Span, "Expected identifier or '...'")
			}
			first = false
		}
		end := token.Span.End
		return ast.NewObjectPat(patElems, ast.NewSpan(start, end))
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

func (parser *Parser) parsePatternSeq() []ast.Pat {
	pats := []ast.Pat{}

	// handles empty sequences
	token := parser.lexer.peek()

	//nolint: exhaustive
	switch token.Type {
	case CloseBracket, CloseParen, CloseBrace:
		return pats
	default:
	}

	pat := parser.parsePattern()
	pats = append(pats, pat)

	token = parser.lexer.peek()

	for {
		//nolint: exhaustive
		switch token.Type {
		case Comma:
			// TODO: handle trailing comma
			parser.lexer.consume()
			pat = parser.parsePattern()
			pats = append(pats, pat)
			token = parser.lexer.peek()
		default:
			return pats
		}
	}
}
