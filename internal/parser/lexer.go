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

func (lexer *Lexer) peekToken() Token {
	offset := lexer.offset
	start := Location{Line: lexer.line, Column: lexer.column}
	codePoint, width := utf8.DecodeRuneInString(lexer.source.Contents[offset:])

	// Skip over whitespace
	for codePoint == ' ' || codePoint == '\n' {
		offset += width
		if codePoint == '\n' {
			start.Line++
			start.Column = 1
		} else {
			start.Column++
		}
		codePoint, width = utf8.DecodeRuneInString(lexer.source.Contents[offset:])
	}

	end := Location{Line: start.Line, Column: start.Column + 1}

	switch codePoint {
	case '+':
		return Token{
			Data: &TPlus{},
			Span: Span{Start: start, End: end},
		}
	case '-':
		return Token{
			Data: &TMinus{},
			Span: Span{Start: start, End: end},
		}
	case '*':
		return Token{
			Data: &TAsterisk{},
			Span: Span{Start: start, End: end},
		}
	case '/':
		return Token{
			Data: &TSlash{},
			Span: Span{Start: start, End: end},
		}
	case '=':
		return Token{
			Data: &TEquals{},
			Span: Span{Start: start, End: end},
		}
	case ',':
		return Token{
			Data: &TComma{},
			Span: Span{Start: start, End: end},
		}
	case '(':
		return Token{
			Data: &TOpenParen{},
			Span: Span{Start: start, End: end},
		}
	case ')':
		return Token{
			Data: &TCloseParen{},
			Span: Span{Start: start, End: end},
		}
	case '{':
		return Token{
			Data: &TOpenBrace{},
			Span: Span{Start: start, End: end},
		}
	case '}':
		return Token{
			Data: &TCloseBrace{},
			Span: Span{Start: start, End: end},
		}
	case '[':
		return Token{
			Data: &TOpenBracket{},
			Span: Span{Start: start, End: end},
		}
	case ']':
		return Token{
			Data: &TCloseBracket{},
			Span: Span{Start: start, End: end},
		}
	case '.':
		return Token{
			Data: &TDot{},
			Span: Span{Start: start, End: end},
		}
	case '?':
		nextCodePoint, _ := utf8.DecodeRuneInString(lexer.source.Contents[offset+width:])
		switch nextCodePoint {
		case '.':
			end.Column++
			return Token{
				Data: &TQuestionDot{},
				Span: Span{Start: start, End: end},
			}
		case '(':
			end.Column++
			return Token{
				Data: &TQuestionOpenParen{},
				Span: Span{Start: start, End: end},
			}
		case '[':
			end.Column++
			return Token{
				Data: &TQuestionOpenBracket{},
				Span: Span{Start: start, End: end},
			}
		default:
			return Token{
				Data: &TInvalid{}, // TODO: include the character in the token
				Span: Span{Start: start, End: end},
			}
		}
	case '"':
		contents := lexer.source.Contents
		n := len(contents)
		i := offset + 1
		for i < n {
			c := contents[i]
			if c == '"' {
				break
			}
			i++
		}
		str := contents[offset+1 : i-1] // without the quotes
		end.Column = start.Column + i - offset
		return Token{
			Data: &TString{Value: str},
			Span: Span{Start: start, End: end},
		}
	case '1', '2', '3', '4', '5', '6', '7', '8', '9':
		contents := lexer.source.Contents
		n := len(contents)
		i := offset + 1
		for i < n {
			c := contents[i]
			if c < '0' || c > '9' {
				break
			}
			i++
		}
		// TODO: handle parsing errors
		num, _ := strconv.ParseFloat(contents[offset:i], 64)
		end.Column = start.Column + i - offset
		return Token{
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
		i := offset + 1
		for i < n {
			c := contents[i]
			if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '_' && c != '$' {
				break
			}
			i++
		}
		ident := contents[offset:i]
		end.Column = start.Column + i - offset
		span := Span{Start: start, End: end}

		switch ident {
		case "fn":
			return Token{
				Data: &TFn{},
				Span: span,
			}
		case "var":
			return Token{
				Data: &TVar{},
				Span: span,
			}
		case "val":
			return Token{
				Data: &TVal{},
				Span: span,
			}
		default:
			return Token{
				Data: &TIdentifier{Value: ident},
				Span: span,
			}
		}
	default:
		if offset >= len(lexer.source.Contents) {
			return Token{
				Data: &TEOF{},
				Span: Span{Start: start, End: start},
			}
		}
		return Token{Data: &TInvalid{}} // TODO: include the character in the token
	}
}

// We need a way to look at the next token without consume it
func (lexer *Lexer) nextToken() Token {
	start := Location{Line: lexer.line, Column: lexer.column}
	codePoint := lexer.nextCodePoint()

	// skip whitespace
	for codePoint == ' ' || codePoint == '\n' {
		start = Location{Line: lexer.line, Column: lexer.column}
		codePoint = lexer.nextCodePoint()
	}

	end := Location{Line: lexer.line, Column: lexer.column}

	switch codePoint {
	case '+':
		return Token{
			Data: &TPlus{},
			Span: Span{Start: start, End: end},
		}
	case '-':
		return Token{
			Data: &TMinus{},
			Span: Span{Start: start, End: end},
		}
	case '*':
		return Token{
			Data: &TAsterisk{},
			Span: Span{Start: start, End: end},
		}
	case '/':
		return Token{
			Data: &TSlash{},
			Span: Span{Start: start, End: end},
		}
	case '=':
		return Token{
			Data: &TEquals{},
			Span: Span{Start: start, End: end},
		}
	case ',':
		return Token{
			Data: &TComma{},
			Span: Span{Start: start, End: end},
		}
	case '(':
		return Token{
			Data: &TOpenParen{},
			Span: Span{Start: start, End: end},
		}
	case ')':
		return Token{
			Data: &TCloseParen{},
			Span: Span{Start: start, End: end},
		}
	case '{':
		return Token{
			Data: &TOpenBrace{},
			Span: Span{Start: start, End: end},
		}
	case '}':
		return Token{
			Data: &TCloseBrace{},
			Span: Span{Start: start, End: end},
		}
	case '[':
		return Token{
			Data: &TOpenBracket{},
			Span: Span{Start: start, End: end},
		}
	case ']':
		return Token{
			Data: &TCloseBracket{},
			Span: Span{Start: start, End: end},
		}
	case '.':
		return Token{
			Data: &TDot{},
			Span: Span{Start: start, End: end},
		}
	case '?':
		nextCodePoint, width := utf8.DecodeRuneInString(lexer.source.Contents[lexer.offset:])
		switch nextCodePoint {
		case '.':
			lexer.offset += width
			lexer.column++
			end := Location{Line: lexer.line, Column: lexer.column}
			return Token{
				Data: &TQuestionDot{},
				Span: Span{Start: start, End: end},
			}
		case '(':
			lexer.offset += width
			lexer.column++
			end := Location{Line: lexer.line, Column: lexer.column}
			return Token{
				Data: &TQuestionOpenParen{},
				Span: Span{Start: start, End: end},
			}
		case '[':
			lexer.offset += width
			lexer.column++
			end := Location{Line: lexer.line, Column: lexer.column}
			return Token{
				Data: &TQuestionOpenBracket{},
				Span: Span{Start: start, End: end},
			}
		default:
			return Token{
				Data: &TInvalid{}, // TODO: include the character in the token
				Span: Span{Start: start, End: end},
			}
		}
	case '"':
		// TODO: handle unicode characters
		// TODO: handle escapes
		contents := lexer.source.Contents
		n := len(contents)
		i := lexer.offset
		for i < n {
			c := contents[i]
			if c == '"' {
				break
			}
			i++
		}
		str := contents[lexer.offset:i] // without the quotes
		lexer.column += i - lexer.offset
		lexer.offset = i + 1
		end := Location{Line: lexer.line, Column: lexer.column}
		return Token{
			Data: &TString{Value: str},
			Span: Span{Start: start, End: end},
		}
	case '1', '2', '3', '4', '5', '6', '7', '8', '9':
		contents := lexer.source.Contents
		n := len(contents)
		i := lexer.offset
		for i < n {
			c := contents[i]
			if c < '0' || c > '9' {
				break
			}
			i++
		}
		// TODO: handle parsing errors
		num, _ := strconv.ParseFloat(contents[lexer.offset-1:i], 64)
		lexer.column += i - lexer.offset
		lexer.offset = i
		end := Location{Line: lexer.line, Column: lexer.column}
		return Token{
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
		i := lexer.offset
		for i < n {
			c := contents[i]
			if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '_' && c != '$' {
				break
			}
			i++
		}
		ident := contents[lexer.offset-1 : i]
		lexer.column += i - lexer.offset
		lexer.offset = i
		end := Location{Line: lexer.line, Column: lexer.column}
		span := Span{Start: start, End: end}

		switch ident {
		case "fn":
			return Token{
				Data: &TFn{},
				Span: span,
			}
		case "var":
			return Token{
				Data: &TVar{},
				Span: span,
			}
		case "val":
			return Token{
				Data: &TVal{},
				Span: span,
			}
		default:
			return Token{
				Data: &TIdentifier{Value: ident},
				Span: span,
			}
		}
	default:
		if lexer.offset >= len(lexer.source.Contents) {
			loc := Location{Line: lexer.line, Column: lexer.column}
			return Token{
				Data: &TEOF{},
				Span: Span{Start: loc, End: loc},
			}
		}
		return Token{Data: &TInvalid{}} // TODO: include the character in the token
	}
}

func (lexer *Lexer) Lex() []Token {
	var tokens []Token

	for lexer.offset < len(lexer.source.Contents) {
		tokens = append(tokens, lexer.nextToken())
	}

	return tokens
}
