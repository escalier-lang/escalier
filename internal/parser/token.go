package parser

import "github.com/escalier-lang/escalier/internal/ast"

type TokenType int

const (
	// Token types
	Number TokenType = iota
	String
	Quasi
	JSXText
	Identifier

	// keywords
	Fn
	Var
	Val
	If
	Else
	Match
	Try
	Catch
	Finally
	Throw
	Async
	Await
	Gen
	Yield
	Return
	Import
	Export
	Declare

	// operators
	Plus
	Minus
	Asterisk
	Slash
	SlashGreaterThan
	Dot
	Comma
	BackTick
	Equal
	EqualEqual
	NotEqual
	LessThan
	LessThanEqual
	GreaterThan
	GreaterThanEqual
	LessThanSlash // Used for JSX

	// optional chaining
	QuestionOpenParen
	QuestionDot
	QuestionOpenBracket

	// grouping
	OpenParen
	CloseParen
	OpenBrace
	CloseBrace
	OpenBracket
	CloseBracket

	Invalid
	EndOfFile
)

type Token struct {
	Span  ast.Span
	Type  TokenType
	Value string
}

func NewToken(kind TokenType, value string, span ast.Span) *Token {
	return &Token{Type: kind, Value: value, Span: span}
}
