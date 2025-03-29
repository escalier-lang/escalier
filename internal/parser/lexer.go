package parser

import (
	"strconv"
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

var KEYWORDS = map[string](func(span ast.Span) Token){
	"fn":      NewFn,
	"var":     NewVar,
	"val":     NewVal,
	"return":  NewReturn,
	"import":  NewImport,
	"export":  NewExport,
	"declare": NewDeclare,
}

func (lexer *Lexer) next() Token {
	startOffset := lexer.currentOffset
	start := lexer.currentLocation

	if startOffset >= len(lexer.source.Contents) {
		return NewEndOfFile(ast.Span{Start: start, End: start})
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

	var token Token
	switch codePoint {
	case '+':
		token = NewPlus(ast.Span{Start: start, End: end})
	case '-':
		token = NewMinus(ast.Span{Start: start, End: end})
	case '*':
		token = NewAsterisk(ast.Span{Start: start, End: end})
	case '/':
		if startOffset+1 < len(lexer.source.Contents) {
			nextCodePoint, _ := utf8.DecodeRuneInString(lexer.source.Contents[startOffset+1:])
			if nextCodePoint == '>' {
				endOffset++
				end.Column++
				token = NewSlashGreaterThan(ast.Span{Start: start, End: end})
			} else {
				token = NewSlash(ast.Span{Start: start, End: end})
			}
		} else {
			token = NewSlash(ast.Span{Start: start, End: end})
		}
	case '=':
		// TODO: handle ==, =>, etc.
		token = NewEquals(ast.Span{Start: start, End: end})
	case ',':
		token = NewComma(ast.Span{Start: start, End: end})
	case '(':
		token = NewOpenParen(ast.Span{Start: start, End: end})
	case ')':
		token = NewCloseParen(ast.Span{Start: start, End: end})
	case '{':
		token = NewOpenBrace(ast.Span{Start: start, End: end})
	case '}':
		token = NewCloseBrace(ast.Span{Start: start, End: end})
	case '[':
		token = NewOpenBracket(ast.Span{Start: start, End: end})
	case ']':
		token = NewCloseBracket(ast.Span{Start: start, End: end})
	case '<':
		if startOffset+1 < len(lexer.source.Contents) {
			nextCodePoint, _ := utf8.DecodeRuneInString(lexer.source.Contents[startOffset+1:])
			switch nextCodePoint {
			case '=':
				endOffset++
				end.Column++
				token = NewLessThanEqual(ast.Span{Start: start, End: end})
			case '/':
				endOffset++
				end.Column++
				token = NewLessThanSlash(ast.Span{Start: start, End: end})
			default:
				token = NewLessThan(ast.Span{Start: start, End: end})
			}
		} else {
			token = NewLessThan(ast.Span{Start: start, End: end})
		}
	case '>':
		// TODO: handle >=
		token = NewGreaterThan(ast.Span{Start: start, End: end})
	case '`':
		token = NewBackTick(ast.Span{Start: start, End: end})
	case '?':
		nextCodePoint, width := utf8.DecodeRuneInString(lexer.source.Contents[startOffset+width:])
		endOffset += width
		end.Column++

		switch nextCodePoint {
		case '.':
			token = NewQuestionDot(ast.Span{Start: start, End: end})
		case '(':
			token = NewQuestionOpenParen(ast.Span{Start: start, End: end})
		case '[':
			token = NewQuestionOpenBracket(ast.Span{Start: start, End: end})
		default:
			token = NewInvalid(ast.Span{Start: start, End: end})
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
		token = NewString(value, ast.Span{Start: start, End: end})
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
			token = NewDot(ast.Span{Start: start, End: end})
		} else {
			value, err := strconv.ParseFloat(contents[startOffset:i], 64)
			if err != nil {
				// TODO: handle parsing errors
			}
			end.Column = start.Column + (i - startOffset)
			token = NewNumber(value, ast.Span{Start: start, End: end})
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

		if keyword, ok := KEYWORDS[value]; ok {
			token = keyword(span)
		} else {
			token = NewIdentifier(value, span)
		}
	default:
		if startOffset >= len(lexer.source.Contents) {
			token = NewEndOfFile(ast.Span{Start: start, End: start})
		} else {
			token = NewInvalid(ast.Span{Start: start, End: start})
		}
	}

	lexer.currentOffset = endOffset
	lexer.currentLocation = end

	return token
}

func (lexer *Lexer) peek() Token {
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

func (lexer *Lexer) lexQuasi() *TQuasi {
	startOffset := lexer.currentOffset
	start := lexer.currentLocation
	end := start

	contents := lexer.source.Contents
	n := len(contents)
	i := startOffset
	last := false
	for i < n {
		c := contents[i]
		if c == '$' {
			if i+1 < n && contents[i+1] == '{' {
				break
			}
		}
		if c == '`' {
			last = true
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
	endOffset := i + 2
	end.Column += 2
	if last {
		endOffset--
		end.Column--
	}

	lexer.currentOffset = endOffset
	lexer.currentLocation = end

	incomplete := false
	var value string
	if i >= n {
		last = true
		incomplete = true
		value = contents[startOffset:]
		// TODO: report an error
	} else {
		value = contents[startOffset:i]
	}

	return NewQuasi(value, last, incomplete, ast.Span{Start: start, End: end})
}

func (lexer *Lexer) lexJSXText() *TJSXText {
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
	return NewJSXText(value, ast.Span{Start: start, End: end})
}

func (lexer *Lexer) Lex() []Token {
	var tokens []Token

	for lexer.currentOffset < len(lexer.source.Contents) {
		tokens = append(tokens, lexer.next())
	}

	return tokens
}
