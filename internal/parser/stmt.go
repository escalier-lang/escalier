package parser

import (
	"github.com/escalier-lang/escalier/internal/ast"
)

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
				// If no tokens have been consumed then we've encountered
				// something we don't know how to parse.  We consume the token
				// and then try to parse the another statement.
				if token.Span.End.Line == nextToken.Span.End.Line &&
					token.Span.End.Column == nextToken.Span.End.Column {
					p.reportError(token.Span, "Unexpected token")
					p.lexer.consume()
				}
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
