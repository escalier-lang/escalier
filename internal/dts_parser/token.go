package dts_parser

import "github.com/escalier-lang/escalier/internal/ast"

type TokenType int

const (
	Ampersand TokenType = iota
	AmpersandAmpersand
	Any
	Arrow
	Asterisk
	Async
	Await
	BackTick
	Bang
	BlockComment
	Boolean
	Catch
	CloseBrace
	CloseBracket
	CloseParen
	Colon
	Comma
	DoubleColon
	Declare
	Do
	Dot
	DotDotDot
	Else
	EndOfFile
	Enum
	Equal
	EqualEqual
	Extends
	Is
	Asserts
	Export
	False
	FatArrow
	Fn
	For
	Gen
	Get
	GreaterThan
	GreaterThanEqual
	Identifier
	If
	Import
	In
	Infer
	Invalid
	JSXText
	Keyof
	LessThan
	LessThanEqual
	LessThanSlash // Used for JSX
	LineComment
	Minus
	Mut
	Never
	New
	NotEqual
	Null
	Number
	NumLit
	OpenBrace
	OpenBracket
	OpenParen
	Pipe
	PipePipe
	Plus
	PlusPlus
	Quasi
	Question
	RegexLit
	Return
	Semicolon
	Set
	Slash
	SlashGreaterThan
	Static
	String
	StrLit
	Throw
	Throws
	True
	Try
	Type
	Typeof
	Undefined
	Underscore
	Unknown
	Val
	Var
	Class
	Yield
	Private
	Symbol
	Unique
	Readonly
	Const
	Let
	Interface
	Abstract
	Implements
	Namespace
	ModuleKeyword
	Function
	Protected
	Public
	From
	As
	Bigint
	Void
	Object
)

type Token struct {
	Span  ast.Span
	Type  TokenType
	Value string
}

func NewToken(kind TokenType, value string, span ast.Span) *Token {
	return &Token{Type: kind, Value: value, Span: span}
}
