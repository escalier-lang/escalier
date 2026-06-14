package parser

import "github.com/escalier-lang/escalier/internal/ast"

// parseFuncParams parses a function parameter list up to (but not consuming) the
// closing paren, returning the params and whether the list ends with a bare
// trailing `...` inexact marker (#677 §4.1: `fn(a, ...)` tolerates extra arguments
// when used as a callback). A bare `...` immediately before `)` is the inexact
// marker; `...pattern` stays an ordinary rest param handled by p.param. It mirrors
// parseDelimSeq's comma handling (including a tolerated trailing comma) so the
// three function forms — decl, expr, type annotation — parse identically.
func (p *Parser) parseFuncParams() (params []*ast.Param, inexact bool) {
	// Match parseDelimSeq's empty-but-non-nil result so callers (and AST snapshots)
	// see the same Params slice they did before this helper existed.
	params = []*ast.Param{}
	if p.lexer.peek().Type == CloseParen {
		return params, false
	}
	for {
		select {
		case <-p.ctx.Done():
			return params, inexact
		default:
		}

		// A bare `...` (the next token after it is the closing paren) marks the
		// function inexact. Anything else after `...` is a rest param (`...rest`),
		// which p.param parses, so back the lexer up and fall through.
		if p.lexer.peek().Type == DotDotDot {
			saved := p.lexer.saveState()
			p.lexer.consume() // tentatively consume '...'
			if p.lexer.peek().Type == CloseParen {
				return params, true
			}
			p.lexer.restoreState(saved)
		}

		param := p.param()
		if param == nil {
			return params, inexact
		}
		params = append(params, param)

		if p.lexer.peek().Type != Comma {
			return params, inexact
		}
		p.lexer.consume() // consume separator
		if p.lexer.peek().Type == CloseParen {
			return params, inexact // tolerated trailing comma
		}
	}
}

// parseDelimSeqInexact parses a separator-delimited sequence up to (but not
// consuming) the terminator, additionally recognizing a bare trailing `...` before
// the terminator as an inexact marker — the object/tuple type-annotation analogue
// of parseFuncParams' `fn(a, ...)`. A `...` immediately before the terminator
// returns inexact=true; anything else after `...` (a rest-spread element `...T`) is
// left for parserCombinator to parse, so the lexer is restored before falling
// through. Comma handling matches parseDelimSeq, including a tolerated trailing
// comma (the top-of-loop terminator check exits after the separator is consumed).
func parseDelimSeqInexact[T any](
	p *Parser,
	terminator TokenType,
	separator TokenType,
	parserCombinator func() T,
) (items []T, inexact bool) {
	return parseDelimSeqHelper(p, terminator, separator, true, parserCombinator)
}

func parseDelimSeq[T any](
	p *Parser,
	terminator TokenType,
	separator TokenType,
	// TODO: update this to return `nil` instead of `optional.None` when there
	// is no item
	parserCombinator func() T,
) []T {
	items, _ := parseDelimSeqHelper(p, terminator, separator, false, parserCombinator)
	return items
}

// parseDelimSeqHelper is the shared loop behind parseDelimSeq and
// parseDelimSeqInexact. It parses a separator-delimited sequence up to (but not
// consuming) the terminator, tolerating a trailing separator before the terminator.
//
// When recognizeInexact is set, a bare `...` immediately before the terminator is
// consumed and returned as inexact=true; anything else after `...` is a rest-spread
// element left for parserCombinator, so the lexer is restored before falling
// through. When recognizeInexact is false the `...` is left untouched and inexact is
// always false.
//
// recognizeInexact also selects the EOF behavior, the one other point the two forms
// differ. The inexact form stops cleanly at EOF. The plain form instead runs
// parserCombinator at EOF so it reports the expected-item / expected-terminator
// error at the truncation point — e.g. `foo(a,` yields a trailing ErrorExpr and an
// "Expected an expression" diagnostic.
func parseDelimSeqHelper[T any](
	p *Parser,
	terminator TokenType,
	separator TokenType,
	recognizeInexact bool,
	parserCombinator func() T,
) (items []T, inexact bool) {
	items = []T{}
	for {
		select {
		case <-p.ctx.Done():
			return items, inexact
		default:
		}

		tok := p.lexer.peek()
		if tok.Type == terminator {
			return items, inexact
		}
		// Only the inexact form treats EOF as a clean stop; the plain form falls
		// through to parserCombinator so it reports the truncation error.
		if tok.Type == EndOfFile && recognizeInexact {
			return items, inexact
		}

		if recognizeInexact && tok.Type == DotDotDot {
			saved := p.lexer.saveState()
			p.lexer.consume() // tentatively consume '...'
			if p.lexer.peek().Type == terminator {
				return items, true
			}
			p.lexer.restoreState(saved)
		}

		item := parserCombinator()
		if any(item) == nil {
			return items, inexact
		}
		items = append(items, item)

		if p.lexer.peek().Type != separator {
			return items, inexact
		}
		p.lexer.consume() // consume separator
	}
}
