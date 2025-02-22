package parser

import (
	"strconv"
	"unicode/utf8"
)

type Source struct {
	path     string
	Contents string
}

type Lexer struct {
	source Source
	offset int
	column int
	line   int
}

func NewLexer(source Source) *Lexer {
	return &Lexer{
		source: source,
		offset: 0,
		line:   1,
		column: 1,
	}
}

func (lexer *Lexer) nextCodePoint() rune {
	codePoint, width := utf8.DecodeRuneInString(lexer.source.Contents[lexer.offset:])
	lexer.offset += width
	lexer.column++

	if codePoint == '\n' {
		lexer.line++
		lexer.column = 1
	}

	return codePoint
}

// We need a way to look at the next token without consume it
func (lexer *Lexer) nextToken() Token {
	codePoint := lexer.nextCodePoint()

	// skip whitespace
	for codePoint == ' ' || codePoint == '\n' {
		codePoint = lexer.nextCodePoint()
	}

	switch codePoint {
	case '+':
		return Token{Data: &TPlus{}}
	case '-':
		return Token{Data: &TMinus{}}
	case '*':
		return Token{Data: &TAsterisk{}}
	case '/':
		return Token{Data: &TSlash{}}
	case '=':
		return Token{Data: &TEquals{}}
	case ',':
		return Token{Data: &TComma{}}
	case '(':
		return Token{Data: &TOpenParen{}}
	case ')':
		return Token{Data: &TCloseParen{}}
	case '{':
		return Token{Data: &TOpenBrace{}}
	case '}':
		return Token{Data: &TCloseBrace{}}
	case '[':
		return Token{Data: &TOpenBracket{}}
	case ']':
		return Token{Data: &TCloseBracket{}}
	case '.':
		return Token{Data: &TDot{}}
	case '?':
		nextCodePoint, width := utf8.DecodeRuneInString(lexer.source.Contents[lexer.offset:])
		switch nextCodePoint {
		case '.':
			lexer.offset += width
			lexer.column++
			return Token{Data: &TQuestionDot{}}
		case '(':
			lexer.offset += width
			lexer.column++
			return Token{Data: &TQuestionOpenParen{}}
		case '[':
			lexer.offset += width
			lexer.column++
			return Token{Data: &TQuestionOpenBracket{}}
		default:
			return Token{Data: &TInvalid{}}
		}
	case '"':
		contents := lexer.source.Contents
		n := len(contents)
		start := lexer.offset
		i := start
		for i < n {
			c := contents[i]
			if c == '"' {
				break
			}
			i++
		}
		str := contents[start:i]
		lexer.offset = i
		return Token{Data: &TString{Value: str}}
	case '1', '2', '3', '4', '5', '6', '7', '8', '9':
		contents := lexer.source.Contents
		n := len(contents)
		start := lexer.offset - 1
		i := start
		for i < n {
			c := contents[i]
			if c < '0' || c > '9' {
				break
			}
			i++
		}
		// TODO: handle parsing errors
		num, _ := strconv.ParseFloat(contents[start:i], 64)
		lexer.offset = i
		return Token{Data: &TNumber{Value: num}}
	case '_', '$',
		'a', 'b', 'c', 'd', 'e', 'f', 'g', 'h', 'i', 'j', 'k', 'l', 'm',
		'n', 'o', 'p', 'q', 'r', 's', 't', 'u', 'v', 'w', 'x', 'y', 'z',
		'A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 'I', 'J', 'K', 'L', 'M',
		'N', 'O', 'P', 'Q', 'R', 'S', 'T', 'U', 'V', 'W', 'X', 'Y', 'Z':

		contents := lexer.source.Contents
		n := len(contents)
		start := lexer.offset - 1
		i := start
		for i < n {
			c := contents[i]
			if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '_' && c != '$' {
				break
			}
			i++
		}
		ident := contents[start:i]
		lexer.offset = i
		switch ident {
		case "fn":
			return Token{Data: &TFn{}}
		case "var":
			return Token{Data: &TVar{}}
		case "val":
			return Token{Data: &TVal{}}
		default:
			return Token{Data: &TIdentifier{Value: contents[start:i]}}
		}
	default:
		if lexer.offset >= len(lexer.source.Contents) {
			return Token{Data: &TEOF{}}
		}
		return Token{Data: &TInvalid{}}
	}
}

func (lexer *Lexer) Lex() []Token {
	var tokens []Token

	for lexer.offset < len(lexer.source.Contents) {
		tokens = append(tokens, lexer.nextToken())
	}

	return tokens
}
