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

func (lexer *Lexer) _peekToken(consume bool) Token {
	startOffset := lexer.offset
	start := Location{Line: lexer.line, Column: lexer.column}
	codePoint, width := utf8.DecodeRuneInString(lexer.source.Contents[startOffset:])

	// Skip over whitespace
	for codePoint == ' ' || codePoint == '\n' {
		startOffset += width
		if codePoint == '\n' {
			start.Line++
			start.Column = 1
		} else {
			start.Column++
		}
		codePoint, width = utf8.DecodeRuneInString(lexer.source.Contents[startOffset:])
	}

	endOffset := startOffset + width

	end := Location{Line: start.Line, Column: start.Column + 1}

	var token Token
	switch codePoint {
	case '+':
		token = Token{
			Data: &TPlus{},
			Span: Span{Start: start, End: end},
		}
	case '-':
		token = Token{
			Data: &TMinus{},
			Span: Span{Start: start, End: end},
		}
	case '*':
		token = Token{
			Data: &TAsterisk{},
			Span: Span{Start: start, End: end},
		}
	case '/':
		token = Token{
			Data: &TSlash{},
			Span: Span{Start: start, End: end},
		}
	case '=':
		token = Token{
			Data: &TEquals{},
			Span: Span{Start: start, End: end},
		}
	case ',':
		token = Token{
			Data: &TComma{},
			Span: Span{Start: start, End: end},
		}
	case '(':
		token = Token{
			Data: &TOpenParen{},
			Span: Span{Start: start, End: end},
		}
	case ')':
		token = Token{
			Data: &TCloseParen{},
			Span: Span{Start: start, End: end},
		}
	case '{':
		token = Token{
			Data: &TOpenBrace{},
			Span: Span{Start: start, End: end},
		}
	case '}':
		token = Token{
			Data: &TCloseBrace{},
			Span: Span{Start: start, End: end},
		}
	case '[':
		token = Token{
			Data: &TOpenBracket{},
			Span: Span{Start: start, End: end},
		}
	case ']':
		token = Token{
			Data: &TCloseBracket{},
			Span: Span{Start: start, End: end},
		}
	case '.':
		token = Token{
			Data: &TDot{},
			Span: Span{Start: start, End: end},
		}
	case '?':
		nextCodePoint, width := utf8.DecodeRuneInString(lexer.source.Contents[startOffset+width:])
		endOffset += width
		end.Column++

		switch nextCodePoint {
		case '.':
			token = Token{
				Data: &TQuestionDot{},
				Span: Span{Start: start, End: end},
			}
		case '(':
			token = Token{
				Data: &TQuestionOpenParen{},
				Span: Span{Start: start, End: end},
			}
		case '[':
			token = Token{
				Data: &TQuestionOpenBracket{},
				Span: Span{Start: start, End: end},
			}
		default:
			token = Token{
				Data: &TInvalid{}, // TODO: include the character in the token
				Span: Span{Start: start, End: end},
			}
		}
	case '"':
		contents := lexer.source.Contents
		n := len(contents)
		i := startOffset + 1
		for i < n {
			c := contents[i]
			if c == '"' {
				break
			}
			i++
		}
		endOffset = i + 1                  // + 1 to include the closing quote
		str := contents[startOffset+1 : i] // without the quotes
		end.Column = start.Column + (i - startOffset)
		token = Token{
			Data: &TString{Value: str},
			Span: Span{Start: start, End: end},
		}
	case '1', '2', '3', '4', '5', '6', '7', '8', '9':
		contents := lexer.source.Contents
		n := len(contents)
		i := startOffset + 1
		for i < n {
			c := contents[i]
			if c < '0' || c > '9' {
				break
			}
			i++
		}
		endOffset = i
		num, _ := strconv.ParseFloat(contents[startOffset:i], 64) // TODO: handle parsing errors
		end.Column = start.Column + (i - startOffset)
		token = Token{
			Data: &TNumber{Value: num},
			Span: Span{Start: start, End: end},
		}
	case '_', '$',
		'a', 'b', 'c', 'd', 'e', 'f', 'g', 'h', 'i', 'j', 'k', 'l', 'm',
		'n', 'o', 'p', 'q', 'r', 's', 't', 'u', 'v', 'w', 'x', 'y', 'z',
		'A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 'I', 'J', 'K', 'L', 'M',
		'N', 'O', 'P', 'Q', 'R', 'S', 'T', 'U', 'V', 'W', 'X', 'Y', 'Z':

		contents := lexer.source.Contents
		n := len(contents)
		i := startOffset + 1
		for i < n {
			c := contents[i]
			if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '_' && c != '$' {
				break
			}
			i++
		}
		endOffset = i
		ident := contents[startOffset:i]
		end.Column = start.Column + i - startOffset
		span := Span{Start: start, End: end}

		switch ident {
		case "fn":
			token = Token{
				Data: &TFn{},
				Span: span,
			}
		case "var":
			token = Token{
				Data: &TVar{},
				Span: span,
			}
		case "val":
			token = Token{
				Data: &TVal{},
				Span: span,
			}
		default:
			token = Token{
				Data: &TIdentifier{Value: ident},
				Span: span,
			}
		}
	default:
		if startOffset >= len(lexer.source.Contents) {
			token = Token{
				Data: &TEOF{},
				Span: Span{Start: start, End: start},
			}
		} else {
			token = Token{Data: &TInvalid{}} // TODO: include the character in the token
		}
	}

	if consume {
		lexer.offset = endOffset
		lexer.column = end.Column
		lexer.line = end.Line
	}

	return token
}

func (lexer *Lexer) peekToken() Token {
	return lexer._peekToken(false)
}

func (lexer *Lexer) nextToken() Token {
	return lexer._peekToken(true)
}

func (lexer *Lexer) Lex() []Token {
	var tokens []Token

	for lexer.offset < len(lexer.source.Contents) {
		tokens = append(tokens, lexer.nextToken())
	}

	return tokens
}
