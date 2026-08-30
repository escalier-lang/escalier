package parser

import "github.com/escalier-lang/escalier/internal/ast"

type TokenType int

const (
	Ampersand TokenType = iota
	AmpersandAmpersand
	Any
	Arrow
	AtSign
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
	From
	Gen
	Get
	GreaterThan
	GreaterThanEqual
	Identifier
	If
	Lifetime
	Implements
	Import
	In
	Infer
	Interface
	Invalid
	JSXText
	Keyof
	LessThan
	LessThanEqual
	LessThanSlash // Used for JSX
	LineComment
	Match
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
	QuestionDot
	QuestionOpenBracket
	QuestionOpenParen
	RegexLit
	Return
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
	Class // <-- add this for 'class' keyword
	Yield
	Private // <-- add this for 'private' keyword
	Symbol
	Unique
	Readonly
	Bigint
	Void
	Override
	Final // 'final' class modifier
	// Super is appended rather than placed near the other class keywords because the lexer
	// snapshots record a token's numeric value, so inserting mid-enum renumbers every token
	// after it.
	Super
	// Negation is `~` (U+007E), the prefix operator for a complement type. Appended for
	// the same reason as Super: a token added mid-enum renumbers every token after it.
	Negation
)

// followsAName reports whether tokenType can come directly after a name in a
// member or parameter position. A keyword that also marks a modifier is a
// modifier only when a name follows it, so what comes next is what tells the
// two apart. `set(v: any) -> unknown` names a method `set` because `(` opens
// its parameter list, where `set value(v: any)` marks an accessor because a
// name follows instead. The same rule reads `from` as a parameter name in
// `substr(mut self, from: number)`.
func followsAName(tokenType TokenType) bool {
	// nolint: exhaustive
	switch tokenType {
	case OpenParen, CloseParen, LessThan, Colon, Question, Comma, CloseBrace, Equal:
		return true
	default:
		return false
	}
}

// startsASignature reports whether tokenType opens a function signature: the
// type parameter list or the parameter list.
func startsASignature(tokenType TokenType) bool {
	// nolint: exhaustive
	switch tokenType {
	case OpenParen, LessThan:
		return true
	default:
		return false
	}
}

type Token struct {
	Span  ast.Span
	Type  TokenType
	Value string
}

func NewToken(kind TokenType, value string, span ast.Span) *Token {
	return &Token{Type: kind, Value: value, Span: span}
}
