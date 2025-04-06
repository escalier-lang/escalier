package parser

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/escalier-lang/escalier/internal/ast"
	"golang.org/x/text/unicode/norm"
)

type Source struct {
	Path     string
	Contents string
}

type Lexer struct {
	source          Source
	currentOffset   int
	currentLocation ast.Location
}

func NewLexer(source Source) *Lexer {
	return &Lexer{
		source:          source,
		currentOffset:   0,
		currentLocation: ast.Location{Line: 1, Column: 1},
	}
}

var keywords = map[string]TokenType{
	"fn":        Fn,
	"get":       Get,
	"set":       Set,
	"static":    Static,
	"var":       Var,
	"val":       Val,
	"return":    Return,
	"import":    Import,
	"export":    Export,
	"declare":   Declare,
	"if":        If,
	"else":      Else,
	"match":     Match,
	"try":       Try,
	"catch":     Catch,
	"finally":   Finally,
	"throw":     Throw,
	"async":     Async,
	"await":     Await,
	"gen":       Gen,
	"yield":     Yield,
	"true":      True,
	"false":     False,
	"null":      Null,
	"undefined": Undefined,
}

func (lexer *Lexer) next() *Token {
	startOffset := lexer.currentOffset
	start := lexer.currentLocation

	if startOffset >= len(lexer.source.Contents) {
		return NewToken(EndOfFile, "", ast.Span{Start: start, End: start})
	}

	codePoint, width := utf8.DecodeRuneInString(lexer.source.Contents[startOffset:])

	// Skip over whitespace
	for codePoint == ' ' || codePoint == '\n' || codePoint == '\t' {
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
	end := ast.Location{Line: start.Line, Column: start.Column + 1}

	var token *Token
	switch codePoint {
	case '+':
		token = NewToken(Plus, "+", ast.Span{Start: start, End: end})
	case '-':
		token = NewToken(Minus, "-", ast.Span{Start: start, End: end})
	case '*':
		token = NewToken(Asterisk, "*", ast.Span{Start: start, End: end})
	case '/':
		// TODO: handle comments, e.g. // and /* */
		if strings.HasPrefix(lexer.source.Contents[startOffset:], "/>") {
			endOffset++
			end.Column++
			token = NewToken(SlashGreaterThan, "/>", ast.Span{Start: start, End: end})
		} else {
			token = NewToken(Slash, "/", ast.Span{Start: start, End: end})
		}
	case '=':
		if strings.HasPrefix(lexer.source.Contents[startOffset:], "==") {
			endOffset++
			end.Column++
			token = NewToken(EqualEqual, "==", ast.Span{Start: start, End: end})
		} else if strings.HasPrefix(lexer.source.Contents[startOffset:], "=>") {
			endOffset++
			end.Column++
			token = NewToken(FatArrow, "=>", ast.Span{Start: start, End: end})
		} else {
			token = NewToken(Equal, "=", ast.Span{Start: start, End: end})
		}
	case ',':
		token = NewToken(Comma, ",", ast.Span{Start: start, End: end})
	case '(':
		token = NewToken(OpenParen, "(", ast.Span{Start: start, End: end})
	case ')':
		token = NewToken(CloseParen, ")", ast.Span{Start: start, End: end})
	case '{':
		token = NewToken(OpenBrace, "{", ast.Span{Start: start, End: end})
	case '}':
		token = NewToken(CloseBrace, "}", ast.Span{Start: start, End: end})
	case '[':
		token = NewToken(OpenBracket, "[", ast.Span{Start: start, End: end})
	case ']':
		token = NewToken(CloseBracket, "]", ast.Span{Start: start, End: end})
	case '<':
		if strings.HasPrefix(lexer.source.Contents[startOffset:], "<=") {
			endOffset++
			end.Column++
			token = NewToken(LessThanEqual, "<=", ast.Span{Start: start, End: end})
		} else if strings.HasPrefix(lexer.source.Contents[startOffset:], "</") {
			endOffset++
			end.Column++
			token = NewToken(LessThanSlash, "</", ast.Span{Start: start, End: end})
		} else {
			token = NewToken(LessThan, "<", ast.Span{Start: start, End: end})
		}
	case '>':
		if strings.HasPrefix(lexer.source.Contents[startOffset:], ">=") {
			endOffset++
			end.Column++
			token = NewToken(GreaterThanEqual, ">=", ast.Span{Start: start, End: end})
		} else {
			token = NewToken(GreaterThan, ">", ast.Span{Start: start, End: end})
		}
	case '`':
		token = NewToken(BackTick, "`", ast.Span{Start: start, End: end})
	case '?':
		if strings.HasPrefix(lexer.source.Contents[startOffset:], "?.") {
			endOffset++
			end.Column++
			token = NewToken(QuestionDot, "?.", ast.Span{Start: start, End: end})
		} else if strings.HasPrefix(lexer.source.Contents[startOffset:], "?(") {
			endOffset++
			end.Column++
			token = NewToken(QuestionOpenParen, "?(", ast.Span{Start: start, End: end})
		} else if strings.HasPrefix(lexer.source.Contents[startOffset:], "?[") {
			endOffset++
			end.Column++
			token = NewToken(QuestionOpenBracket, "?[", ast.Span{Start: start, End: end})
		} else {
			token = NewToken(Question, "?", ast.Span{Start: start, End: end})
		}
	case '!':
		if strings.HasPrefix(lexer.source.Contents[startOffset:], "!=") {
			endOffset++
			end.Column++
			token = NewToken(BangEqual, "!=", ast.Span{Start: start, End: end})
		} else {
			token = NewToken(Bang, "!", ast.Span{Start: start, End: end})
		}
	case ':':
		token = NewToken(Colon, ":", ast.Span{Start: start, End: end})
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
		endOffset = i + 1                    // + 1 to include the closing quote
		value := contents[startOffset+1 : i] // without the quotes
		end.Column = start.Column + (i - startOffset)
		token = NewToken(String, value, ast.Span{Start: start, End: end})
	case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9', '.':
		contents := lexer.source.Contents
		n := len(contents)
		i := startOffset
		isDecimal := false

		if codePoint == '.' {
			isDecimal = true
			i++
		} else {
			i++
		}

		for i < n {
			c := contents[i]
			if c == '.' && !isDecimal {
				isDecimal = true
				i++
				continue
			}
			if c < '0' || c > '9' {
				break
			}
			i++
		}

		endOffset = i
		if isDecimal && i == startOffset+1 {
			if strings.HasPrefix(contents[startOffset:], "...") {
				endOffset += 2
				end.Column += 2
				token = NewToken(DotDotDot, "...", ast.Span{Start: start, End: end})
			} else {
				token = NewToken(Dot, ".", ast.Span{Start: start, End: end})
			}
		} else {
			end.Column = start.Column + (i - startOffset)
			token = NewToken(Number, contents[startOffset:i], ast.Span{Start: start, End: end})
		}
	default:
		c := codePoint
		if idIdentStart(c) {
			contents := lexer.source.Contents

			n := len(contents)
			i := startOffset
			for i < n {
				codePoint, width := utf8.DecodeRuneInString(lexer.source.Contents[i:])
				if !isIdentContinue(codePoint) {
					break
				}
				i += width
			}
			endOffset = i

			value := string(norm.NFC.Bytes([]byte(contents[startOffset:i])))
			end.Column = start.Column + utf8.RuneCountInString(value)
			span := ast.Span{Start: start, End: end}

			if keyword, ok := keywords[value]; ok {
				token = NewToken(keyword, value, span)
			} else if value == "_" {
				token = NewToken(Underscore, value, span)
			} else {
				token = NewToken(Identifier, value, span)
			}
		} else if startOffset >= len(lexer.source.Contents) {
			token = NewToken(EndOfFile, "", ast.Span{Start: start, End: start})
		} else {
			token = NewToken(Invalid, "", ast.Span{Start: start, End: start})
		}
	}

	lexer.currentOffset = endOffset
	lexer.currentLocation = end

	return token
}

// Based on https://www.unicode.org/reports/tr31/#D1
func idIdentStart(r rune) bool {
	return (r == '_' || r == '$' || // '_', '$' are not included in the UAX-31 spec
		unicode.IsLetter(r) ||
		unicode.Is(unicode.Nl, r) ||
		unicode.Is(unicode.Other_ID_Start, r)) &&
		!unicode.Is(unicode.Pattern_Syntax, r) &&
		!unicode.Is(unicode.Pattern_White_Space, r)
}

// Based on https://www.unicode.org/reports/tr31/#D1
func isIdentContinue(r rune) bool {
	return (r == '_' || r == '$' || // '_', '$' are not included in the UAX-31 spec
		unicode.IsLetter(r) ||
		unicode.Is(unicode.Nl, r) ||
		unicode.Is(unicode.Other_ID_Start, r) ||
		unicode.Is(unicode.Mn, r) ||
		unicode.Is(unicode.Mc, r) ||
		unicode.Is(unicode.Nd, r) ||
		unicode.Is(unicode.Pc, r) ||
		unicode.Is(unicode.Other_ID_Continue, r)) &&
		!unicode.Is(unicode.Pattern_Syntax, r) &&
		!unicode.Is(unicode.Pattern_White_Space, r)
}

func (lexer *Lexer) peek() *Token {
	// save the lexer state
	offset := lexer.currentOffset
	location := lexer.currentLocation
	token := lexer.next()
	// restore the lexer state
	lexer.currentOffset = offset
	lexer.currentLocation = location
	return token
}

func (lexer *Lexer) consume() {
	lexer.next()
}

func (lexer *Lexer) lexQuasi() *Token {
	startOffset := lexer.currentOffset
	start := lexer.currentLocation
	end := start

	contents := lexer.source.Contents
	n := len(contents)
	i := startOffset
	for i < n {
		c := contents[i]
		if c == '$' {
			if i+1 < n && contents[i+1] == '{' {
				i += 2
				end.Column += 2
				break
			}
		}
		if c == '`' {
			i++
			end.Column++
			break
		}

		if c == '\n' {
			end.Line++
			end.Column = 1
		} else {
			end.Column++
		}

		i++
	}
	endOffset := i

	lexer.currentOffset = endOffset
	lexer.currentLocation = end

	var value string
	if i >= n {
		value = contents[startOffset:]
		// TODO: report an error
	} else {
		value = contents[startOffset:i]
	}

	return NewToken(Quasi, value, ast.Span{Start: start, End: end})
}

func (lexer *Lexer) lexJSXText() *Token {
	startOffset := lexer.currentOffset
	start := lexer.currentLocation
	end := start

	contents := lexer.source.Contents
	n := len(contents)
	i := startOffset

	for i < n {
		c := contents[i]
		if c == '<' || c == '{' {
			break
		}
		if c == '\n' {
			end.Line++
			end.Column = 1
		} else {
			end.Column++
		}
		i++
	}
	endOffset := i

	lexer.currentOffset = endOffset
	lexer.currentLocation = end

	var value string
	if i >= n {
		value = contents[startOffset:]
		// TODO: report an errors
	} else {
		value = contents[startOffset:endOffset]
	}
	return NewToken(JSXText, value, ast.Span{Start: start, End: end})
}

func (lexer *Lexer) Lex() []*Token {
	var tokens []*Token

	for lexer.currentOffset < len(lexer.source.Contents) {
		tokens = append(tokens, lexer.next())
	}

	return tokens
}
