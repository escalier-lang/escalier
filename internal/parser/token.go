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
	Equals
	Dot
	Comma
	BackTick
	LessThan
	LessThanEqual
	LessThanSlash
	GreaterThan

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
