package parser

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/escalier-lang/escalier/internal/ast"
	"golang.org/x/text/unicode/norm"
)

type Lexer struct {
	source          *ast.Source
	currentOffset   int
	currentLocation ast.Location
	lastToken       *Token // Track last token for regex context
}

func NewLexer(source *ast.Source) *Lexer {
	return &Lexer{
		source:          source,
		currentOffset:   0,
		currentLocation: ast.Location{Line: 1, Column: 1},
		lastToken:       nil,
	}
}

var keywords = map[string]TokenType{
	"fn":        Fn,
	"get":       Get,
	"set":       Set,
	"static":    Static,
	"var":       Var,
	"val":       Val,
	"type":      Type,
	"return":    Return,
	"import":    Import,
	"export":    Export,
	"declare":   Declare,
	"infer":     Infer,
	"if":        If,
	"else":      Else,
	"match":     Match,
	"try":       Try,
	"catch":     Catch,
	"throw":     Throw,
	"async":     Async,
	"await":     Await,
	"throws":    Throws,
	"gen":       Gen,
	"yield":     Yield,
	"true":      True,
	"false":     False,
	"null":      Null,
	"undefined": Undefined,
	"number":    Number,
	"string":    String,
	"boolean":   Boolean,
	"any":       Any,
	"never":     Never,
	"unknown":   Unknown,
	"mut":       Mut,
	"for":       For,
	"in":        In,
	"do":        Do,
}

// isRegexContext determines if a '/' should be treated as the start of a regex literal
// Based on the previous token, we can determine the context
func (lexer *Lexer) isRegexContext() bool {
	if lexer.lastToken == nil {
		return true // At the beginning of input, / starts a regex
	}

	//nolint:exhaustive // We handle the important cases and have a default
	switch lexer.lastToken.Type {
	// After these tokens, / starts a regex
	case OpenParen, OpenBracket, OpenBrace, Comma, Colon, Question,
		Equal, EqualEqual, NotEqual, LessThan, LessThanEqual,
		GreaterThan, GreaterThanEqual, Plus, PlusPlus, Minus, Asterisk,
		Ampersand, AmpersandAmpersand, Pipe, PipePipe, Bang,
		Return, If, Else, Match, Try, Catch, Throw,
		Arrow, FatArrow:
		return true
	// After these tokens, / is division
	case Identifier, NumLit, StrLit, RegexLit, True, False, Null, Undefined,
		CloseParen, CloseBracket, CloseBrace:
		return false
	default:
		return true // Default to regex for safety
	}
}

// lexRegex lexes a regex literal starting from the given position
func (lexer *Lexer) lexRegex(startOffset int, start ast.Location) *Token {
	end := start

	contents := lexer.source.Contents
	n := len(contents)
	i := startOffset + 1 // Skip the opening '/'

	// Parse the regex pattern
	for i < n {
		c := contents[i]
		if c == '/' {
			i++ // Include the closing '/'
			end.Column++
			break
		}
		if c == '\\' && i+1 < n {
			// Skip escaped character (both the backslash and the next char)
			i += 2
			end.Column += 2
			continue
		}
		if c == '\n' {
			// Regex literals cannot contain unescaped newlines
			// TODO: report an error
			break
		}
		if c == '\r' {
			// Also handle Windows line endings
			break
		}
		i++
		end.Column++
	}

	// Parse regex flags (if any)
	for i < n {
		c := contents[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			i++
			end.Column++
		} else {
			break
		}
	}

	endOffset := i
	value := contents[startOffset:endOffset]

	return NewToken(RegexLit, value, ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
}

func (lexer *Lexer) next() *Token {
	startOffset := lexer.currentOffset
	start := lexer.currentLocation

	if startOffset >= len(lexer.source.Contents) {
		return NewToken(EndOfFile, "", ast.Span{Start: start, End: start, SourceID: lexer.source.ID})
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
		if strings.HasPrefix(lexer.source.Contents[startOffset:], "++") {
			endOffset++
			end.Column++
			token = NewToken(PlusPlus, "++", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
		} else {
			token = NewToken(Plus, "+", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
		}
	case '-':
		if strings.HasPrefix(lexer.source.Contents[startOffset:], "->") {
			endOffset++
			end.Column++
			token = NewToken(Arrow, "->", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
		} else {
			token = NewToken(Minus, "-", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
		}
	case '*':
		token = NewToken(Asterisk, "*", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
	case '/':
		// Handle regex literals vs division/comments
		if strings.HasPrefix(lexer.source.Contents[startOffset:], "/>") {
			endOffset++
			end.Column++
			token = NewToken(SlashGreaterThan, "/>", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
		} else if strings.HasPrefix(lexer.source.Contents[startOffset:], "//") {
			i := startOffset + 2
			n := len(lexer.source.Contents)
			for i < n {
				if lexer.source.Contents[i] == '\n' {
					break
				}
				i++
			}
			endOffset = i
			end.Column = start.Column + (i - startOffset)
			value := lexer.source.Contents[startOffset:i]
			token = NewToken(LineComment, value, ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
		} else if strings.HasPrefix(lexer.source.Contents[startOffset:], "/*") {
			i := startOffset + 2
			n := len(lexer.source.Contents)
			for i < n {
				if strings.HasPrefix(lexer.source.Contents[i:], "*/") {
					i += 2
					break
				}
				if lexer.source.Contents[i] == '\n' {
					end.Line++
					end.Column = 1
				} else {
					end.Column++
				}
				i++
			}
			endOffset = i
			value := lexer.source.Contents[startOffset:i]
			token = NewToken(BlockComment, value, ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
		} else if lexer.isRegexContext() {
			// Lex as regex literal
			token = lexer.lexRegex(startOffset, start)
			endOffset = startOffset + len(token.Value)
			end = token.Span.End
		} else {
			token = NewToken(Slash, "/", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
		}
	case '=':
		if strings.HasPrefix(lexer.source.Contents[startOffset:], "==") {
			endOffset++
			end.Column++
			token = NewToken(EqualEqual, "==", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
		} else if strings.HasPrefix(lexer.source.Contents[startOffset:], "=>") {
			endOffset++
			end.Column++
			token = NewToken(FatArrow, "=>", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
		} else {
			token = NewToken(Equal, "=", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
		}
	case ',':
		token = NewToken(Comma, ",", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
	case '(':
		token = NewToken(OpenParen, "(", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
	case ')':
		token = NewToken(CloseParen, ")", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
	case '{':
		token = NewToken(OpenBrace, "{", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
	case '}':
		token = NewToken(CloseBrace, "}", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
	case '[':
		token = NewToken(OpenBracket, "[", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
	case ']':
		token = NewToken(CloseBracket, "]", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
	case '<':
		if strings.HasPrefix(lexer.source.Contents[startOffset:], "<=") {
			endOffset++
			end.Column++
			token = NewToken(LessThanEqual, "<=", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
		} else if strings.HasPrefix(lexer.source.Contents[startOffset:], "</") {
			endOffset++
			end.Column++
			token = NewToken(LessThanSlash, "</", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
		} else {
			token = NewToken(LessThan, "<", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
		}
	case '>':
		if strings.HasPrefix(lexer.source.Contents[startOffset:], ">=") {
			endOffset++
			end.Column++
			token = NewToken(GreaterThanEqual, ">=", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
		} else {
			token = NewToken(GreaterThan, ">", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
		}
	case '|':
		if strings.HasPrefix(lexer.source.Contents[startOffset:], "||") {
			endOffset++
			end.Column++
			token = NewToken(PipePipe, "||", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
		} else {
			token = NewToken(Pipe, "|", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
		}
	case '&':
		if strings.HasPrefix(lexer.source.Contents[startOffset:], "&&") {
			endOffset++
			end.Column++
			token = NewToken(AmpersandAmpersand, "&&", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
		} else {
			token = NewToken(Ampersand, "&", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
		}
	case '`':
		token = NewToken(BackTick, "`", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
	case '?':
		if strings.HasPrefix(lexer.source.Contents[startOffset:], "?.") {
			endOffset++
			end.Column++
			token = NewToken(QuestionDot, "?.", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
		} else if strings.HasPrefix(lexer.source.Contents[startOffset:], "?(") {
			endOffset++
			end.Column++
			token = NewToken(QuestionOpenParen, "?(", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
		} else if strings.HasPrefix(lexer.source.Contents[startOffset:], "?[") {
			endOffset++
			end.Column++
			token = NewToken(QuestionOpenBracket, "?[", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
		} else {
			token = NewToken(Question, "?", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
		}
	case '!':
		if strings.HasPrefix(lexer.source.Contents[startOffset:], "!=") {
			endOffset++
			end.Column++
			token = NewToken(NotEqual, "!=", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
		} else {
			token = NewToken(Bang, "!", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
		}
	case ':':
		if strings.HasPrefix(lexer.source.Contents[startOffset:], "::") {
			endOffset++
			end.Column++
			token = NewToken(DoubleColon, "::", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
		} else {
			token = NewToken(Colon, ":", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
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
		end.Column = start.Column + (endOffset - startOffset)
		token = NewToken(StrLit, value, ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
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
				token = NewToken(DotDotDot, "...", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
			} else {
				token = NewToken(Dot, ".", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
			}
		} else {
			end.Column = start.Column + (i - startOffset)
			token = NewToken(NumLit, contents[startOffset:i], ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
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
			span := ast.Span{Start: start, End: end, SourceID: lexer.source.ID}

			if keyword, ok := keywords[value]; ok {
				token = NewToken(keyword, value, span)
			} else if value == "_" {
				token = NewToken(Underscore, value, span)
			} else {
				token = NewToken(Identifier, value, span)
			}
		} else if startOffset >= len(lexer.source.Contents) {
			token = NewToken(EndOfFile, "", ast.Span{Start: start, End: start, SourceID: lexer.source.ID})
		} else {
			token = NewToken(Invalid, "", ast.Span{Start: start, End: start, SourceID: lexer.source.ID})
		}
	}

	lexer.currentOffset = endOffset
	lexer.currentLocation = end
	lexer.lastToken = token // Track the last token for regex context

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

func (lexer *Lexer) saveState() *Lexer {
	return &Lexer{
		source:          lexer.source,
		currentOffset:   lexer.currentOffset,
		currentLocation: lexer.currentLocation,
		lastToken:       lexer.lastToken,
	}
}

func (lexer *Lexer) restoreState(saved *Lexer) {
	lexer.source = saved.source
	lexer.currentOffset = saved.currentOffset
	lexer.currentLocation = saved.currentLocation
	lexer.lastToken = saved.lastToken
}

func (lexer *Lexer) peek() *Token {
	savedState := lexer.saveState()
	token := lexer.next()
	lexer.restoreState(savedState)
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

	return NewToken(Quasi, value, ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
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
	return NewToken(JSXText, value, ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
}

func (lexer *Lexer) Lex() []*Token {
	var tokens []*Token

	for lexer.currentOffset < len(lexer.source.Contents) {
		tokens = append(tokens, lexer.next())
	}

	return tokens
}
