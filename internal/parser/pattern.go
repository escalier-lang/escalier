package parser

import (
	"strconv"

	"github.com/escalier-lang/escalier/internal/ast"
)

// pattern = identifier | wildcard | tuple | object | rest | literal
func (p *Parser) pattern(allowIdentDefault bool, allowColonTypeAnn bool) ast.Pat {
	token := p.lexer.peek()

	// Optional `mut` prefix on a binding leaf. Mutability lives on the
	// *place* (the binding name), so it only attaches to identifier patterns.
	// Per-leaf mutability inside a destructuring pattern is expressed by
	// putting `mut` on each leaf, e.g. `{ mut x, y: mut a }`, never on the
	// surrounding tuple/object/rest pattern.
	if token.Type == Mut {
		mutSpan := token.Span
		p.lexer.consume() // consume 'mut'
		next := p.lexer.peek()
		if next.Type != Identifier {
			p.reportError(ast.MergeSpans(mutSpan, next.Span),
				"'mut' in pattern position must be followed by an identifier")
			// Don't recurse into p.pattern() for recovery — that path
			// can re-enter literalPat()'s default case and emit a
			// second "Expected a pattern" diagnostic for the same
			// offending token. Return nil; the caller treats nil as an
			// invalid pattern.
			return nil
		}
		p.lexer.consume() // consume identifier
		pat := p.identPat(next, allowIdentDefault, allowColonTypeAnn)
		if identPat, ok := pat.(*ast.IdentPat); ok {
			// Rebuild the IdentPat with a span that covers the leading
			// `mut` keyword, so diagnostics like CannotMutateImmutableError
			// underline the binding-side `mut`, not just the identifier.
			merged := ast.MergeSpans(mutSpan, identPat.Span())
			rebuilt := ast.NewIdentPat(
				identPat.Name, identPat.TypeAnn, identPat.Default, merged)
			rebuilt.Mutable = true
			return rebuilt
		}
		return pat
	}

	// nolint: exhaustive
	switch token.Type {
	case Identifier, String, Number, Boolean, Bigint:
		// Look ahead to determine if this is an extractor or instance pattern
		// by checking if the qualified identifier is followed by '(' or '{'
		savedState := p.lexer.saveState()
		p.lexer.consume() // consume first identifier

		// Skip through any dots and identifiers (qualified identifier)
		isQualified := false
		for p.lexer.peek().Type == Dot {
			p.lexer.consume() // consume dot
			next := p.lexer.peek()
			if next.Type == Identifier || isTypeKeywordIdentifier(next.Type) {
				p.lexer.consume() // consume identifier
				isQualified = true
			} else {
				break
			}
		}

		next := p.lexer.peek()
		p.lexer.restoreState(savedState) // restore to start
		p.lexer.consume()                // consume first identifier again

		if next.Type == OpenParen {
			return p.extractorPat(token)
		} else if next.Type == OpenBrace {
			return p.instancePat(token)
		} else if isQualified {
			// Qualified name without parens (e.g., Option.None) is an
			// extractor pattern with zero arguments.
			qualIdent := p.parseQualifiedIdent(token)
			span := qualIdent.Span()
			return ast.NewExtractorPat(qualIdent, nil, span)
		} else {
			return p.identPat(token, allowIdentDefault, allowColonTypeAnn)
		}
	case Underscore:
		p.lexer.consume()
		return ast.NewWildcardPat(token.Span)
	case OpenBracket:
		return p.tuplePat()
	case OpenBrace:
		return p.objectPat()
	case DotDotDot:
		return p.restPat(allowColonTypeAnn)
	default:
		return p.literalPat()
	}
}

// extractorPat = identifier '(' (pattern (',' pattern)*)? ')'
func (p *Parser) extractorPat(nameToken *Token) ast.Pat {
	qualIdent := p.parseQualifiedIdent(nameToken)
	p.lexer.consume() // consume '('
	patArgs := parseDelimSeq(p, CloseParen, Comma, func() ast.Pat {
		return p.pattern(true, true)
	})
	end := p.expect(CloseParen, AlwaysConsume)
	span := ast.NewSpan(nameToken.Span.Start, end, p.lexer.source.ID)
	return ast.NewExtractorPat(qualIdent, patArgs, span)
}

// instancePat = identifier '{' (objPatElem (',' objPatElem)*)? '}'
func (p *Parser) instancePat(nameToken *Token) ast.Pat {
	qualIdent := p.parseQualifiedIdent(nameToken)
	start := nameToken.Span.Start
	p.lexer.consume() // consume '{'
	patElems := parseDelimSeq(p, CloseBrace, Comma, p.objPatElem)
	end := p.expect(CloseBrace, AlwaysConsume)
	span := ast.NewSpan(start, end, p.lexer.source.ID)
	objectPat := ast.NewObjectPat(patElems, span)
	return ast.NewInstancePat(qualIdent, objectPat, span)
}

// identPat = identifier (':' typeAnn)? ('=' expr)?
func (p *Parser) identPat(nameToken *Token, allowIdentDefault bool, allowColonTypeAnn bool) ast.Pat {
	span := nameToken.Span

	// Check for inline type annotation
	var typeAnn ast.TypeAnn
	token := p.lexer.peek()
	if allowColonTypeAnn && token.Type == Colon {
		p.lexer.consume() // consume ':'
		typeAnn = p.typeAnn()
		if typeAnn != nil {
			span = ast.MergeSpans(span, typeAnn.Span())
		}
	}

	// Check for default value
	var default_ ast.Expr
	token = p.lexer.peek()
	if allowIdentDefault && token.Type == Equal {
		p.lexer.consume()
		expr := p.expr()
		if expr != nil {
			span = ast.MergeSpans(span, expr.Span())
			default_ = expr
		}
	}
	return ast.NewIdentPat(nameToken.Value, typeAnn, default_, span)
}

// tuplePat = '[' (pattern (',' pattern)*)? ']'
func (p *Parser) tuplePat() ast.Pat {
	token := p.lexer.peek()
	start := token.Span.Start
	p.lexer.consume() // consume '['
	patElems := parseDelimSeq(p, CloseBracket, Comma, func() ast.Pat {
		return p.pattern(true, true)
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
func (p *Parser) restPat(allowColonTypeAnn bool) ast.Pat {
	token := p.lexer.peek()
	p.lexer.consume() // consume '...'
	pat := p.pattern(true, allowColonTypeAnn)
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

	// Shorthand `mut` prefix: `{ mut x }`. Mutability lives on the
	// *binding* leaf, so the flag goes on the resulting ObjShorthandPat.
	// Key-value forms (`{ x: mut a }`) recurse through pattern() and pick
	// up `mut` via the IdentPat path; nothing extra needed here.
	mutShorthand := false
	var mutPrefixSpan ast.Span
	if token.Type == Mut {
		mutSpan := token.Span
		p.lexer.consume() // consume 'mut'
		next := p.lexer.peek()
		if next.Type != Identifier {
			p.reportError(ast.MergeSpans(mutSpan, next.Span),
				"'mut' in object pattern position must be followed by an identifier")
			return nil
		}
		mutShorthand = true
		mutPrefixSpan = mutSpan
		token = next
	}

	// nolint: exhaustive
	switch token.Type {
	case Identifier, String, Number, Boolean, Bigint:
		p.lexer.consume()
		key := ast.NewIdentifier(token.Value, token.Span)
		span := token.Span

		token = p.lexer.peek()
		if token.Type == Colon {
			if mutShorthand {
				p.reportError(ast.MergeSpans(mutPrefixSpan, span),
					"'mut' in object pattern only applies to shorthand bindings; use `key: mut value` for nested patterns")
			}
			p.lexer.consume()
			value := p.pattern(true, true)
			if value != nil {
				span = ast.MergeSpans(span, value.Span())
			}

			if value == nil {
				return nil
			}

			span = ast.MergeSpans(span, value.Span())
			return ast.NewObjKeyValuePat(key, value, span)
		} else {
			// Handle shorthand pattern: {x::number} or {x::number = 0} or {x = 0}
			var typeAnn ast.TypeAnn

			// Check for inline type annotation
			token = p.lexer.peek()
			if token.Type == DoubleColon {
				p.lexer.consume() // consume '::'
				typeAnn = p.typeAnn()
				if typeAnn != nil {
					span = ast.MergeSpans(span, typeAnn.Span())
				}
			}

			// Check for default value
			var default_ ast.Expr
			token = p.lexer.peek()
			if token.Type == Equal {
				p.lexer.consume()
				expr := p.expr()
				if expr != nil {
					span = ast.MergeSpans(span, expr.Span())
					default_ = expr
				}
			}

			// Extend the span to cover the leading `mut` keyword so
			// diagnostics underline the full binding-side declaration.
			if mutShorthand {
				span = ast.MergeSpans(mutPrefixSpan, span)
			}
			pat := ast.NewObjShorthandPat(key, typeAnn, default_, span)
			pat.Mutable = mutShorthand
			return pat
		}
	case DotDotDot:
		p.lexer.consume()

		pat := p.pattern(true, true)
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
