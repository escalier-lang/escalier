package parser

import (
	"github.com/escalier-lang/escalier/internal/ast"
)

// isStatementInitiator returns true if the token type begins a new statement
// or declaration. Used to decide whether recovery should attempt to parse an
// expression or bail out.
func (p *Parser) isStatementInitiator(tt TokenType) bool {
	// nolint: exhaustive
	switch tt {
	case Val, Var, Fn, Type, Interface, Enum, Class, Return, Throw,
		For, If, Import, Export, Declare, Async, EndOfFile:
		return true
	default:
		return false
	}
}

// skipToNextStatement consumes tokens until reaching a statement boundary:
// EOF, the given stop token, a newline boundary, or a statement-initiating keyword.
func (p *Parser) skipToNextStatement(stopOn TokenType) {
	for {
		token := p.lexer.peek()
		// nolint: exhaustive
		switch token.Type {
		case EndOfFile, stopOn:
			return
		case Val, Var, Fn, Type, Interface, Enum, Class, Return, Throw,
			For, If, Import, Export, Declare, Async:
			return
		default:
			p.lexer.consume()
			// Stop if the next token is on a different line (statement boundary).
			next := p.lexer.peek()
			if next.Span.Start.Line != token.Span.Start.Line {
				return
			}
		}
	}
}

// block = '{' stmt* '}'
func (p *Parser) block() ast.Block {
	var start ast.Location

	token := p.lexer.next()
	if token.Type != OpenBrace {
		p.reportError(token.Span, "Expected an opening brace")
		return ast.Block{Stmts: []ast.Stmt{}, Span: token.Span}
	} else {
		start = token.Span.Start
	}

	stmts, end := p.stmts(CloseBrace)
	span := ast.Span{Start: start, End: end, SourceID: p.lexer.source.ID}
	return ast.Block{Stmts: *stmts, Span: span}
}

func (p *Parser) stmts(stopOn TokenType) (*[]ast.Stmt, ast.Location) {
	stmts := []ast.Stmt{}

	token := p.lexer.peek()
	for {
		// Check if context has been cancelled (timeout or cancellation)
		select {
		case <-p.ctx.Done():
			// Return what we have so far when context is done
			return &stmts, token.Span.End
		default:
			// continue
		}

		//nolint: exhaustive
		switch token.Type {
		case stopOn:
			p.lexer.consume()
			return &stmts, token.Span.End
		case EndOfFile:
			// If we hit EOF before finding stopOn, return what we have
			return &stmts, token.Span.End
		case LineComment, BlockComment:
			p.lexer.consume()
			token = p.lexer.peek()
		default:
			stmt := p.stmt()
			if stmt != nil {
				stmts = append(stmts, stmt)
			} else {
				nextToken := p.lexer.peek()
				if token.Span.End.Line == nextToken.Span.End.Line &&
					token.Span.End.Column == nextToken.Span.End.Column {
					// No tokens were consumed — skip to the next statement
					// boundary to avoid an infinite loop.
					p.reportError(token.Span, "Unexpected token")
					p.skipToNextStatement(stopOn)
					nextToken = p.lexer.peek()
				}
				// Wrap the failed statement region in an ErrorStmt.
				stmts = append(stmts, ast.NewErrorStmt(
					ast.Span{Start: token.Span.Start, End: nextToken.Span.Start, SourceID: p.lexer.source.ID},
				))
			}
			token = p.lexer.peek()
		}
	}
}

// stmt = decl | ('return' expr?) | expr
func (p *Parser) stmt() ast.Stmt {
	token := p.lexer.peek()
	p.exprMode.Push(SingleLineExpr)
	defer p.exprMode.Pop()

	var stmt ast.Stmt

	// nolint: exhaustive
	switch token.Type {
	case Import:
		stmt = p.importStmt()
	case For:
		stmt = p.parseForInStmt()
	case Async, Fn, Var, Val, Type, Interface, Enum, Declare, Export, Class:
		decl := p.Decl()
		if decl == nil {
			return nil
		}
		stmt = ast.NewDeclStmt(decl, decl.Span())
	case Return:
		p.lexer.consume()
		expr := p.exprWithoutErrorCheck()
		if expr == nil {
			stmt = ast.NewReturnStmt(nil, token.Span)
		} else {
			stmt = ast.NewReturnStmt(
				expr, ast.MergeSpans(token.Span, expr.Span()),
			)
		}
	default:
		expr := p.exprWithoutErrorCheck()
		if expr == nil {
			stmt = nil
		} else {
			stmt = ast.NewExprStmt(expr, expr.Span())
		}
	}

	return stmt
}

// parseForInStmt = 'for' 'await'? pattern 'in' expr '{' stmt* '}'
func (p *Parser) parseForInStmt() ast.Stmt {
	startSpan := p.lexer.peek().Span
	p.lexer.consume() // consume 'for'

	// Check for 'await' keyword
	isAwait := false
	if p.lexer.peek().Type == Await {
		isAwait = true
		p.lexer.consume()
	}

	// Parse pattern (loop variable)
	pattern := p.pattern(false, false)
	if pattern == nil {
		token := p.lexer.peek()
		p.reportError(token.Span, "Expected a pattern after 'for'")
		return nil
	}

	// Expect 'in' keyword
	inToken := p.lexer.peek()
	if inToken.Type != In {
		p.reportError(inToken.Span, "Expected 'in' after pattern in for loop")
		return nil
	}
	p.lexer.consume() // consume 'in'

	// Parse iterable expression
	iterable := p.expr()

	// Parse body block
	body := p.block()

	return ast.NewForInStmt(pattern, iterable, body, isAwait,
		ast.MergeSpans(startSpan, body.Span))
}

// importStmt = 'import' importSpecifiers 'from' string
// importSpecifiers = '{' namedImport (',' namedImport)* '}' | '*' 'as' identifier
// namedImport = identifier ('as' identifier)?
func (p *Parser) importStmt() ast.Stmt {
	importToken := p.lexer.next()
	if importToken.Type != Import {
		p.reportError(importToken.Span, "Expected 'import'")
		return nil
	}

	var specifiers []*ast.ImportSpecifier
	token := p.lexer.peek()

	// Parse import specifiers
	if token.Type == Asterisk {
		// Namespace import: import * as ns from "module"
		p.lexer.consume()
		asToken := p.lexer.next()
		if asToken.Type != Identifier || asToken.Value != "as" {
			p.reportError(asToken.Span, "Expected 'as' after '*'")
			return nil
		}
		nameToken := p.lexer.next()
		if nameToken.Type != Identifier {
			p.reportError(nameToken.Span, "Expected identifier after 'as'")
			return nil
		}
		specifier := ast.NewImportSpecifier(
			"*",
			nameToken.Value,
			ast.MergeSpans(token.Span, nameToken.Span),
		)
		specifiers = append(specifiers, specifier)
	} else if token.Type == OpenBrace {
		// Named imports: import { foo, bar as baz } from "module"
		p.lexer.consume()
		for {
			token = p.lexer.peek()
			if token.Type == CloseBrace {
				p.lexer.consume()
				break
			}
			if token.Type == Comma {
				p.lexer.consume()
				continue
			}
			if token.Type != Identifier {
				p.reportError(token.Span, "Expected identifier in import specifier")
				return nil
			}

			nameToken := p.lexer.next()
			name := nameToken.Value
			alias := ""

			// Check for "as" alias
			nextToken := p.lexer.peek()
			if nextToken.Type == Identifier && nextToken.Value == "as" {
				p.lexer.consume()
				aliasToken := p.lexer.next()
				if aliasToken.Type != Identifier {
					p.reportError(aliasToken.Span, "Expected identifier after 'as'")
					return nil
				}
				alias = aliasToken.Value
				specifier := ast.NewImportSpecifier(
					name,
					alias,
					ast.MergeSpans(nameToken.Span, aliasToken.Span),
				)
				specifiers = append(specifiers, specifier)
			} else {
				specifier := ast.NewImportSpecifier(name, alias, nameToken.Span)
				specifiers = append(specifiers, specifier)
			}
		}
	} else {
		p.reportError(token.Span, "Expected import specifiers ('{' or '*')")
		return nil
	}

	// Expect 'from'
	fromToken := p.lexer.next()
	if fromToken.Type != From {
		p.reportError(fromToken.Span, "Expected 'from' after import specifiers")
		return nil
	}

	// Expect string literal for module path
	moduleToken := p.lexer.next()
	if moduleToken.Type != StrLit {
		p.reportError(moduleToken.Span, "Expected string literal for module path")
		return nil
	}

	span := ast.MergeSpans(importToken.Span, moduleToken.Span)
	return ast.NewImportStmt(specifiers, moduleToken.Value, span)
}
