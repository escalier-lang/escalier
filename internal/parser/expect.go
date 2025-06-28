package parser

import (
	"fmt"

	"github.com/escalier-lang/escalier/internal/ast"
)

var TokenMap = map[TokenType]string{
	Identifier:          "identifier",
	NumLit:              "number",
	StrLit:              "string",
	True:                "true",
	False:               "false",
	Asterisk:            "*",
	Plus:                "+",
	Minus:               "-",
	OpenParen:           "(",
	CloseParen:          ")",
	OpenBracket:         "[",
	CloseBracket:        "]",
	OpenBrace:           "{",
	CloseBrace:          "}",
	Comma:               ",",
	Dot:                 ".",
	Colon:               ":",
	Question:            "?",
	QuestionDot:         "?.",
	QuestionOpenParen:   "?(",
	QuestionOpenBracket: "?[",
	Arrow:               "->",
	FatArrow:            "=>",
	Fn:                  "fn",
	Val:                 "val",
	Var:                 "var",
	Type:                "type",
	Return:              "return",
	Declare:             "declare",
	Export:              "export",
	Import:              "import",
	If:                  "if",
	Else:                "else",
	Match:               "match",
	Try:                 "try",
	Catch:               "catch",
	Finally:             "finally",
	Throw:               "throw",
	Async:               "async",
	Await:               "await",
	Gen:                 "gen",
	Yield:               "yield",
}

type Consume int

const (
	AlwaysConsume Consume = iota
	ConsumeOnMatch
	ConsumeOnMismatch
)

func (p *Parser) expect(tt TokenType, consume Consume) ast.Location {
	token := p.lexer.peek()
	if consume == AlwaysConsume {
		p.lexer.consume()
	}
	if token.Type != tt {
		if consume == ConsumeOnMismatch {
			p.lexer.consume()
		}
		p.reportError(token.Span, fmt.Sprintf("Expected %s but got %s", TokenMap[tt], TokenMap[token.Type]))
		return token.Span.End
	}
	if consume == ConsumeOnMatch {
		p.lexer.consume()
	}
	return token.Span.End
}
