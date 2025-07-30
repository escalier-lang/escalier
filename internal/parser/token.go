package parser

import "github.com/escalier-lang/escalier/internal/ast"

type TokenType int

const (
	Identifier TokenType = iota

	// literals
	NumLit
	StrLit
	RegexLit
	True
	False
	Null
	Undefined

	// misc
	Quasi
	JSXText
	Underscore
	LineComment
	BlockComment
	Colon
	Question

	// keywords
	Fn
	Get
	Set
	Static
	Var
	Val
	Type
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

	// type annotations
	Number
	String
	Boolean

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
	DotDotDot
	Arrow
	FatArrow
	Bang
	Ampersand
	AmpersandAmpersand
	Pipe
	PipePipe

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

	Mut
	For
	In
	Infer
)

type Token struct {
	Span  ast.Span
	Type  TokenType
	Value string
}

func NewToken(kind TokenType, value string, span ast.Span) *Token {
	return &Token{Type: kind, Value: value, Span: span}
}
