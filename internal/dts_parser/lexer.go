package dts_parser

import (
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
	"private":    Private,
	"protected":  Protected,
	"public":     Public,
	"fn":         Fn,
	"function":   Function,
	"class":      Class,
	"get":        Get,
	"set":        Set,
	"static":     Static,
	"var":        Var,
	"let":        Let,
	"const":      Const,
	"val":        Val,
	"type":       Type,
	"interface":  Interface,
	"return":     Return,
	"import":     Import,
	"export":     Export,
	"declare":    Declare,
	"infer":      Infer,
	"if":         If,
	"else":       Else,
	"enum":       Enum,
	"try":        Try,
	"catch":      Catch,
	"throw":      Throw,
	"async":      Async,
	"await":      Await,
	"throws":     Throws,
	"gen":        Gen,
	"yield":      Yield,
	"true":       True,
	"false":      False,
	"null":       Null,
	"undefined":  Undefined,
	"number":     Number,
	"string":     String,
	"boolean":    Boolean,
	"bigint":     Bigint,
	"any":        Any,
	"never":      Never,
	"unknown":    Unknown,
	"mut":        Mut,
	"for":        For,
	"in":         In,
	"do":         Do,
	"symbol":     Symbol,
	"unique":     Unique,
	"keyof":      Keyof,
	"typeof":     Typeof,
	"readonly":   Readonly,
	"new":        New,
	"extends":    Extends,
	"is":         Is,
	"asserts":    Asserts,
	"abstract":   Abstract,
	"implements": Implements,
	"namespace":  Namespace,
	"module":     ModuleKeyword,
	"from":       From,
	"as":         As,
	"void":       Void,
	"object":     Object,
	"intrinsic":  Intrinsic,
}

// skipWhitespace advances past whitespace characters and returns the new offset and location.
// It handles spaces, tabs, and newlines, updating line and column numbers appropriately.
func (lexer *Lexer) skipWhitespace(startOffset int, start ast.Location) (int, ast.Location) {
	contents := lexer.source.Contents
	for startOffset < len(contents) {
		codePoint, width := utf8.DecodeRuneInString(contents[startOffset:])
		if codePoint != ' ' && codePoint != '\n' && codePoint != '\t' {
			break
		}
		startOffset += width
		if codePoint == '\n' {
			start.Line++
			start.Column = 1
		} else {
			start.Column++
		}
	}
	return startOffset, start
}

// scanIdent scans an identifier starting at the given offset and returns the normalized value
// and the ending offset. Returns empty string and start offset if not a valid identifier.
func (lexer *Lexer) scanIdent(startOffset int) (string, int) {
	contents := lexer.source.Contents
	n := len(contents)

	if startOffset >= n {
		return "", startOffset
	}

	// Fast path for ASCII identifiers (most common case)
	firstChar := contents[startOffset]
	if firstChar <= 127 {
		// Check ASCII identifier start: a-z, A-Z, _, $
		if !((firstChar >= 'a' && firstChar <= 'z') ||
			(firstChar >= 'A' && firstChar <= 'Z') ||
			firstChar == '_' || firstChar == '$') {
			return "", startOffset
		}

		// Scan ASCII identifier continuation: a-z, A-Z, 0-9, _, $
		i := startOffset + 1
		for i < n && contents[i] <= 127 {
			c := contents[i]
			if !((c >= 'a' && c <= 'z') ||
				(c >= 'A' && c <= 'Z') ||
				(c >= '0' && c <= '9') ||
				c == '_' || c == '$') {
				break
			}
			i++
		}

		// If we scanned to the end or hit ASCII non-identifier char, we're done (no normalization needed)
		if i >= n || contents[i] <= 127 {
			return contents[startOffset:i], i
		}

		// We hit a Unicode character - continue scanning from where we left off
		needsNormalization := true
		for i < n {
			// Fast check for ASCII continuation
			if contents[i] <= 127 {
				c := contents[i]
				if !((c >= 'a' && c <= 'z') ||
					(c >= 'A' && c <= 'Z') ||
					(c >= '0' && c <= '9') ||
					c == '_' || c == '$') {
					break
				}
				i++
				continue
			}

			// Unicode path
			codePoint, width := utf8.DecodeRuneInString(contents[i:])
			if !isIdentContinue(codePoint) {
				break
			}
			i += width
		}

		value := contents[startOffset:i]
		if needsNormalization {
			value = string(norm.NFC.Bytes([]byte(value)))
		}
		return value, i
	}

	// Slow path for Unicode identifiers starting with Unicode character
	codePoint, width := utf8.DecodeRuneInString(contents[startOffset:])

	// Check if it starts with a valid identifier start character
	if !idIdentStart(codePoint) {
		return "", startOffset
	}

	// Scan the full identifier and track if normalization is needed
	i := startOffset + width
	needsNormalization := true

	for i < n {
		// Fast check for ASCII continuation
		if contents[i] <= 127 {
			c := contents[i]
			if !((c >= 'a' && c <= 'z') ||
				(c >= 'A' && c <= 'Z') ||
				(c >= '0' && c <= '9') ||
				c == '_' || c == '$') {
				break
			}
			i++
			continue
		}

		// Unicode path
		codePoint, width := utf8.DecodeRuneInString(contents[i:])
		if !isIdentContinue(codePoint) {
			break
		}
		i += width
	}

	value := contents[startOffset:i]
	// Only normalize if we found non-ASCII characters
	if needsNormalization {
		value = string(norm.NFC.Bytes([]byte(value)))
	}
	return value, i
}

func (lexer *Lexer) next() *Token {
	startOffset := lexer.currentOffset
	start := lexer.currentLocation

	if startOffset >= len(lexer.source.Contents) {
		return NewToken(EndOfFile, "", ast.Span{Start: start, End: start, SourceID: lexer.source.ID})
	}

	// Skip over whitespace
	startOffset, start = lexer.skipWhitespace(startOffset, start)

	codePoint, width := utf8.DecodeRuneInString(lexer.source.Contents[startOffset:])

	endOffset := startOffset + width
	end := ast.Location{Line: start.Line, Column: start.Column + 1}

	var token *Token
	switch codePoint {
	case '+':
		if startOffset+1 < len(lexer.source.Contents) && lexer.source.Contents[startOffset+1] == '+' {
			endOffset++
			end.Column++
			token = NewToken(PlusPlus, "++", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
		} else {
			token = NewToken(Plus, "+", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
		}
	case '-':
		if startOffset+1 < len(lexer.source.Contents) && lexer.source.Contents[startOffset+1] == '>' {
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
		if startOffset+1 < len(lexer.source.Contents) && lexer.source.Contents[startOffset+1] == '>' {
			endOffset++
			end.Column++
			token = NewToken(SlashGreaterThan, "/>", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
		} else if startOffset+1 < len(lexer.source.Contents) && lexer.source.Contents[startOffset+1] == '/' {
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
		} else if startOffset+1 < len(lexer.source.Contents) && lexer.source.Contents[startOffset+1] == '*' {
			i := startOffset + 2
			n := len(lexer.source.Contents)
			for i < n {
				if i+1 < n && lexer.source.Contents[i] == '*' && lexer.source.Contents[i+1] == '/' {
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
		} else {
			token = NewToken(Slash, "/", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
		}
	case '=':
		if startOffset+1 < len(lexer.source.Contents) && lexer.source.Contents[startOffset+1] == '=' {
			endOffset++
			end.Column++
			token = NewToken(EqualEqual, "==", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
		} else if startOffset+1 < len(lexer.source.Contents) && lexer.source.Contents[startOffset+1] == '>' {
			endOffset++
			end.Column++
			token = NewToken(FatArrow, "=>", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
		} else {
			token = NewToken(Equal, "=", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
		}
	case ',':
		token = NewToken(Comma, ",", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
	case ';':
		token = NewToken(Semicolon, ";", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
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
		if startOffset+1 < len(lexer.source.Contents) && lexer.source.Contents[startOffset+1] == '=' {
			endOffset++
			end.Column++
			token = NewToken(LessThanEqual, "<=", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
		} else if startOffset+1 < len(lexer.source.Contents) && lexer.source.Contents[startOffset+1] == '/' {
			endOffset++
			end.Column++
			token = NewToken(LessThanSlash, "</", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
		} else {
			token = NewToken(LessThan, "<", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
		}
	case '>':
		if startOffset+1 < len(lexer.source.Contents) && lexer.source.Contents[startOffset+1] == '=' {
			endOffset++
			end.Column++
			token = NewToken(GreaterThanEqual, ">=", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
		} else {
			token = NewToken(GreaterThan, ">", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
		}
	case '|':
		if startOffset+1 < len(lexer.source.Contents) && lexer.source.Contents[startOffset+1] == '|' {
			endOffset++
			end.Column++
			token = NewToken(PipePipe, "||", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
		} else {
			token = NewToken(Pipe, "|", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
		}
	case '&':
		if startOffset+1 < len(lexer.source.Contents) && lexer.source.Contents[startOffset+1] == '&' {
			endOffset++
			end.Column++
			token = NewToken(AmpersandAmpersand, "&&", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
		} else {
			token = NewToken(Ampersand, "&", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
		}
	case '`':
		token = NewToken(BackTick, "`", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
	case '?':
		token = NewToken(Question, "?", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
	case '!':
		if startOffset+1 < len(lexer.source.Contents) && lexer.source.Contents[startOffset+1] == '=' {
			endOffset++
			end.Column++
			token = NewToken(NotEqual, "!=", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
		} else {
			token = NewToken(Bang, "!", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
		}
	case ':':
		if startOffset+1 < len(lexer.source.Contents) && lexer.source.Contents[startOffset+1] == ':' {
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
			// Check if it's a dot, '...' or a decimal number starting with '.'
			if i >= n || contents[i] < '0' || contents[i] > '9' {
				// It's a dot or '...'
				endOffset = i
				if i+2 < n && contents[i] == '.' && contents[i+1] == '.' {
					endOffset += 2
					end.Column += 2
					token = NewToken(DotDotDot, "...", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
				} else {
					token = NewToken(Dot, ".", ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
				}
			} else {
				// It's a decimal number starting with '.'
				for i < n {
					c := contents[i]
					if c < '0' || c > '9' {
						break
					}
					i++
				}
				endOffset = i
				end.Column = start.Column + (i - startOffset)
				token = NewToken(NumLit, contents[startOffset:i], ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
			}
		} else {
			// Check for hex literal (0x or 0X)
			if codePoint == '0' && i+1 < n && (contents[i+1] == 'x' || contents[i+1] == 'X') {
				i += 2 // skip '0x' or '0X'
				// Scan hex digits
				for i < n {
					c := contents[i]
					if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
						break
					}
					i++
				}
				endOffset = i
				end.Column = start.Column + (i - startOffset)
				token = NewToken(NumLit, contents[startOffset:i], ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
			} else {
				// Regular decimal/integer number
				i++
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
				end.Column = start.Column + (i - startOffset)
				token = NewToken(NumLit, contents[startOffset:i], ast.Span{Start: start, End: end, SourceID: lexer.source.ID})
			}
		}
	default:
		c := codePoint
		if idIdentStart(c) {
			value, endIdent := lexer.scanIdent(startOffset)
			endOffset = endIdent

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
	// Fast path for common ASCII cases
	if r <= 127 {
		return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_' || r == '$'
	}
	// Unicode path
	return (unicode.IsLetter(r) ||
		unicode.Is(unicode.Nl, r) ||
		unicode.Is(unicode.Other_ID_Start, r)) &&
		!unicode.Is(unicode.Pattern_Syntax, r) &&
		!unicode.Is(unicode.Pattern_White_Space, r)
}

// Based on https://www.unicode.org/reports/tr31/#D1
func isIdentContinue(r rune) bool {
	// Fast path for common ASCII cases
	if r <= 127 {
		return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '$'
	}
	// Unicode path
	return (unicode.IsLetter(r) ||
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

// SaveState creates a snapshot of the current lexer state (exported for use by dts_parser)
func (lexer *Lexer) SaveState() *Lexer {
	return lexer.saveState()
}

// RestoreState restores the lexer to a previously saved state (exported for use by dts_parser)
func (lexer *Lexer) RestoreState(saved *Lexer) {
	lexer.restoreState(saved)
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

func (lexer *Lexer) Lex() []*Token {
	var tokens []*Token

	for lexer.currentOffset < len(lexer.source.Contents) {
		tokens = append(tokens, lexer.next())
	}

	return tokens
}

// Peek returns the next token without consuming it (exported for use by dts_parser)
func (lexer *Lexer) Peek() *Token {
	return lexer.peek()
}

// Next advances the lexer and returns the next token (exported for dts_parser)
func (lexer *Lexer) Next() *Token {
	return lexer.next()
}

// Consume advances the lexer to the next token (exported for dts_parser)
func (lexer *Lexer) Consume() {
	lexer.consume()
}

// peekIdent peeks at the next token and returns it as an Identifier if it's a valid
// identifier-like token (including keywords that can be used as identifiers in certain contexts).
// This is useful in contexts where keywords can be used as property names or parameter names.
// Returns nil if the next token is not identifier-like.
// Does not consume the token - caller must call Consume() if they want to advance.
func (lexer *Lexer) peekIdent() *Token {
	startOffset := lexer.currentOffset
	start := lexer.currentLocation

	if startOffset >= len(lexer.source.Contents) {
		return nil
	}

	// Skip whitespace
	startOffset, start = lexer.skipWhitespace(startOffset, start)

	contents := lexer.source.Contents
	if startOffset >= len(contents) {
		return nil
	}

	// Scan identifier
	value, _ := lexer.scanIdent(startOffset)
	if value == "" {
		return nil
	}

	end := ast.Location{
		Line:   start.Line,
		Column: start.Column + utf8.RuneCountInString(value),
	}
	span := ast.Span{Start: start, End: end, SourceID: lexer.source.ID}

	// Don't check keywords map - treat everything as an identifier
	// This allows reserved words to be used as identifiers in appropriate contexts
	if value == "_" {
		return NewToken(Underscore, value, span)
	}
	return NewToken(Identifier, value, span)
}
