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
		token = p.lexer.peek()
		if token.Type == OpenParen {
			p.lexer.consume()
			pats := p.parsePatternSeq()
			token = p.lexer.peek()
			if token.Type != CloseParen {
				msg := fmt.Sprintf("Expected ')', got '%s'", token.Value)
				p.reportError(token.Span, msg)
			} else {
				p.lexer.consume()
			}
			return ast.NewExtractPat(name, pats, ast.Span{Start: token.Span.Start, End: token.Span.End})
		} else {
			return ast.NewIdentPat(name, token.Span)
		}
	case Underscore:
		p.lexer.consume()
		return ast.NewWildcardPat(token.Span)
	case OpenBracket:
		p.lexer.consume()
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
					p.reportError(token.Span, "Expected ','")
					return nil
				}
				p.lexer.consume()
				token = p.lexer.peek()
			}
			if token.Type == DotDotDot {
				// TODO: only try to parse an identifier after the ...
				p.lexer.consume()
				pat := p.parsePattern()
				patElems = append(patElems, ast.NewTupleRestPat(pat, token.Span))
			} else {
				pat := p.parsePattern()
				patElems = append(patElems, ast.NewTupleElemPat(pat, token.Span))
			}
			first = false
		}
		return ast.NewTuplePat(patElems, ast.Span{Start: token.Span.Start, End: token.Span.End})
	case OpenBrace:
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
				key := token.Value
				p.lexer.consume()
				token = p.lexer.peek()
				if token.Type == Colon {
					// TODO: handle optional initializers
					p.lexer.consume()
					value := p.parsePattern()
					patElems = append(patElems, ast.NewObjKeyValuePat(key, value, token.Span))
				} else {
					patElems = append(patElems, ast.NewObjShorthandPat(key, token.Span))
				}
			} else if token.Type == DotDotDot {
				p.lexer.consume()
				pat := p.parsePattern()
				patElems = append(patElems, ast.NewObjRestPat(pat, token.Span))
			} else {
				p.reportError(token.Span, "Expected identifier or '...'")
			}
			first = false
		}
		return ast.NewObjectPat(patElems, ast.Span{Start: token.Span.Start, End: token.Span.End})
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
