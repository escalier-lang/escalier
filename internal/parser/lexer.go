package parser

import (
	"strconv"
	"unicode/utf8"
)

type Source struct {
	Path     string
	Contents string
}

type Lexer struct {
	source            Source
	currentOffset     int
	currentLocation   Location
	afterPeekOffset   int
	afterPeakLocation Location
}

func NewLexer(source Source) *Lexer {
	return &Lexer{
		source:          source,
		currentOffset:   0,
		currentLocation: Location{Line: 1, Column: 1},
		// The peek state is invalid until the first call to peekToken.
		afterPeekOffset:   -1,
		afterPeakLocation: Location{Line: 0, Column: 0},
	}
}

var KEYWORDS = map[string]TokenKind{
	"fn":      &TFn{},
	"var":     &TVar{},
	"val":     &TVal{},
	"return":  &TReturn{},
	"import":  &TImport{},
	"export":  &TExport{},
	"declare": &TDeclare{},
}

func (lexer *Lexer) peekAndMaybeConsume(consume bool) Token {
	startOffset := lexer.currentOffset
	start := lexer.currentLocation

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
	end := Location{Line: start.Line, Column: start.Column + 1}

	var token Token
	switch codePoint {
	case '+':
		token = Token{
			Kind: &TPlus{},
			Span: Span{Start: start, End: end},
		}
	case '-':
		token = Token{
			Kind: &TMinus{},
			Span: Span{Start: start, End: end},
		}
	case '*':
		token = Token{
			Kind: &TAsterisk{},
			Span: Span{Start: start, End: end},
		}
	case '/':
		token = Token{
			Kind: &TSlash{},
			Span: Span{Start: start, End: end},
		}
	case '=':
		token = Token{
			Kind: &TEquals{},
			Span: Span{Start: start, End: end},
		}
	case ',':
		token = Token{
			Kind: &TComma{},
			Span: Span{Start: start, End: end},
		}
	case '(':
		token = Token{
			Kind: &TOpenParen{},
			Span: Span{Start: start, End: end},
		}
	case ')':
		token = Token{
			Kind: &TCloseParen{},
			Span: Span{Start: start, End: end},
		}
	case '{':
		token = Token{
			Kind: &TOpenBrace{},
			Span: Span{Start: start, End: end},
		}
	case '}':
		token = Token{
			Kind: &TCloseBrace{},
			Span: Span{Start: start, End: end},
		}
	case '[':
		token = Token{
			Kind: &TOpenBracket{},
			Span: Span{Start: start, End: end},
		}
	case ']':
		token = Token{
			Kind: &TCloseBracket{},
			Span: Span{Start: start, End: end},
		}
	case '?':
		nextCodePoint, width := utf8.DecodeRuneInString(lexer.source.Contents[startOffset+width:])
		endOffset += width
		end.Column++

		switch nextCodePoint {
		case '.':
			token = Token{
				Kind: &TQuestionDot{},
				Span: Span{Start: start, End: end},
			}
		case '(':
			token = Token{
				Kind: &TQuestionOpenParen{},
				Span: Span{Start: start, End: end},
			}
		case '[':
			token = Token{
				Kind: &TQuestionOpenBracket{},
				Span: Span{Start: start, End: end},
			}
		default:
			token = Token{
				Kind: &TInvalid{}, // TODO: include the character in the token
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
			Kind: &TString{Value: str},
			Span: Span{Start: start, End: end},
		}
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
			token = Token{
				Kind: &TDot{},
				Span: Span{Start: start, End: end},
			}
		} else {
			num, err := strconv.ParseFloat(contents[startOffset:i], 64)
			if err != nil {
				// TODO: handle parsing errors
			}
			end.Column = start.Column + (i - startOffset)
			token = Token{
				Kind: &TNumber{Value: num},
				Span: Span{Start: start, End: end},
			}
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

		if keyword, ok := KEYWORDS[ident]; ok {
			token = Token{
				Kind: keyword,
				Span: span,
			}
		} else {
			token = Token{
				Kind: &TIdentifier{Value: ident},
				Span: span,
			}
		}
	default:
		if startOffset >= len(lexer.source.Contents) {
			token = Token{
				Kind: &TEndOfFile{},
				Span: Span{Start: start, End: start},
			}
		} else {
			token = Token{
				Kind: &TInvalid{},
				Span: Span{Start: start, End: start},
			}
		}
	}

	if !consume {
		lexer.afterPeekOffset = endOffset
		lexer.afterPeakLocation = end
	} else {
		lexer.afterPeekOffset = -1
		lexer.afterPeakLocation = Location{Line: 0, Column: 0}

		lexer.currentOffset = endOffset
		lexer.currentLocation = end
	}

	return token
}

func (lexer *Lexer) peek() Token {
	return lexer.peekAndMaybeConsume(false)
}

func (lexer *Lexer) next() Token {
	return lexer.peekAndMaybeConsume(true)
}

// func expect[V T](lexer *Lexer) (V, error) {
// 	token := lexer.next()
// 	t, ok := token.Data.(V)
// 	if !ok {
// 		var zero V
// 		return zero, fmt.Errorf("unexpected token")
// 	}
// 	return t, nil
// }

func (lexer *Lexer) consume() {
	if lexer.afterPeekOffset != -1 {
		lexer.currentOffset = lexer.afterPeekOffset
		lexer.currentLocation = lexer.afterPeakLocation

		// Reset the peek state
		lexer.afterPeekOffset = -1
		lexer.afterPeakLocation = Location{Line: 0, Column: 0}
	}
}

func (lexer *Lexer) Lex() []Token {
	var tokens []Token

	for lexer.currentOffset < len(lexer.source.Contents) {
		tokens = append(tokens, lexer.next())
	}

	return tokens
}
