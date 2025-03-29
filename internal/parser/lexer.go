package parser

import (
	"unicode/utf8"

	"github.com/escalier-lang/escalier/internal/ast"
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

// var KEYWORDS = map[string](func(span ast.Span) Token){
// 	"fn":      NewToken,
// 	"var":     NewVar,
// 	"val":     NewVal,
// 	"return":  NewReturn,
// 	"import":  NewImport,
// 	"export":  NewExport,
// 	"declare": NewDeclare,
// }

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
		if startOffset+1 < len(lexer.source.Contents) {
			nextCodePoint, _ := utf8.DecodeRuneInString(lexer.source.Contents[startOffset+1:])
			if nextCodePoint == '>' {
				endOffset++
				end.Column++
				token = NewToken(SlashGreaterThan, "/>", ast.Span{Start: start, End: end})
			} else {
				token = NewToken(Slash, "/", ast.Span{Start: start, End: end})
			}
		} else {
			token = NewToken(Slash, "/", ast.Span{Start: start, End: end})
		}
	case '=':
		// TODO: handle ==, =>, etc.
		token = NewToken(Equals, "=", ast.Span{Start: start, End: end})
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
		if startOffset+1 < len(lexer.source.Contents) {
			nextCodePoint, _ := utf8.DecodeRuneInString(lexer.source.Contents[startOffset+1:])
			switch nextCodePoint {
			case '=':
				endOffset++
				end.Column++
				token = NewToken(LessThanEqual, "<=", ast.Span{Start: start, End: end})
			case '/':
				endOffset++
				end.Column++
				token = NewToken(LessThanSlash, "</", ast.Span{Start: start, End: end})
			default:
				token = NewToken(LessThan, "<", ast.Span{Start: start, End: end})
			}
		} else {
			token = NewToken(LessThan, "<", ast.Span{Start: start, End: end})
		}
	case '>':
		// TODO: handle >=
		token = NewToken(GreaterThan, ">", ast.Span{Start: start, End: end})
	case '`':
		token = NewToken(BackTick, "`", ast.Span{Start: start, End: end})
	case '?':
		nextCodePoint, width := utf8.DecodeRuneInString(lexer.source.Contents[startOffset+width:])
		endOffset += width
		end.Column++

		switch nextCodePoint {
		case '.':
			token = NewToken(QuestionDot, ".", ast.Span{Start: start, End: end})
		case '(':
			token = NewToken(QuestionOpenParen, "(", ast.Span{Start: start, End: end})
		case '[':
			token = NewToken(QuestionOpenBracket, "[", ast.Span{Start: start, End: end})
		default:
			token = NewToken(Invalid, "", ast.Span{Start: start, End: end})
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
			token = NewToken(Dot, ".", ast.Span{Start: start, End: end})
		} else {
			end.Column = start.Column + (i - startOffset)
			token = NewToken(Number, contents[startOffset:i], ast.Span{Start: start, End: end})
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
		value := contents[startOffset:i]
		end.Column = start.Column + i - startOffset
		span := ast.Span{Start: start, End: end}

		switch value {
		case "fn":
			token = NewToken(Fn, "fn", span)
		case "var":
			token = NewToken(Var, "var", span)
		case "val":
			token = NewToken(Val, "val", span)
		case "return":
			token = NewToken(Return, "return", span)
		case "import":
			token = NewToken(Import, "import", span)
		case "export":
			token = NewToken(Export, "export", span)
		case "declare":
			token = NewToken(Declare, "declare", span)
		default:
			token = NewToken(Identifier, value, span)
		}
	default:
		if startOffset >= len(lexer.source.Contents) {
			token = NewToken(EndOfFile, "", ast.Span{Start: start, End: start})
		} else {
			token = NewToken(Invalid, "", ast.Span{Start: start, End: start})
		}
	}

	lexer.currentOffset = endOffset
	lexer.currentLocation = end

	return token
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
				break
			}
		}
		if c == '`' {
			i++
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
	end.Column = endOffset

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

	value := contents[startOffset:endOffset]
	return NewToken(JSXText, value, ast.Span{Start: start, End: end})
}

func (lexer *Lexer) Lex() []*Token {
	var tokens []*Token

	for lexer.currentOffset < len(lexer.source.Contents) {
		tokens = append(tokens, lexer.next())
	}

	return tokens
}
