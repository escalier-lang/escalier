package dts_parser

import (
	"unicode/utf8"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/lexer_util"
)

type Lexer struct {
	source        *ast.Source
	currentOffset int
	lastToken     *Token // Track last token for regex context
}

func NewLexer(source *ast.Source) *Lexer {
	return &Lexer{
		source:        source,
		currentOffset: 0,
		lastToken:     nil,
	}
}

// spanBetween builds the span of a token running from one byte offset to
// another. Every token the lexer produces gets its span this way, so a span
// and the text it covers can never disagree.
func (lexer *Lexer) spanBetween(startOffset, endOffset int) ast.Span {
	return ast.Span{
		Start:    ast.Location{Offset: startOffset},
		End:      ast.Location{Offset: endOffset},
		SourceID: lexer.source.ID,
	}
}

// currentLoc returns the position the lexer will read from next.
func (lexer *Lexer) currentLoc() ast.Location {
	return ast.Location{Offset: lexer.currentOffset}
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
	"global":     Global,
}

// skipWhitespace advances past whitespace characters and returns the new offset and location.
// It handles spaces, tabs, and newlines.
func (lexer *Lexer) skipWhitespace(startOffset int) int {
	contents := lexer.source.Contents
	for startOffset < len(contents) {
		codePoint, width := utf8.DecodeRuneInString(contents[startOffset:])
		if codePoint != ' ' && codePoint != '\n' && codePoint != '\t' {
			break
		}
		startOffset += width
	}
	return startOffset
}

func (lexer *Lexer) next() *Token {
	startOffset := lexer.currentOffset

	if startOffset >= len(lexer.source.Contents) {
		return NewToken(EndOfFile, "", lexer.spanBetween(startOffset, startOffset))
	}

	// Skip over whitespace
	startOffset = lexer.skipWhitespace(startOffset)

	codePoint, width := utf8.DecodeRuneInString(lexer.source.Contents[startOffset:])

	endOffset := startOffset + width

	var token *Token
	switch codePoint {
	case '+':
		if startOffset+1 < len(lexer.source.Contents) && lexer.source.Contents[startOffset+1] == '+' {
			endOffset++
			token = NewToken(PlusPlus, "++", lexer.spanBetween(startOffset, endOffset))
		} else {
			token = NewToken(Plus, "+", lexer.spanBetween(startOffset, endOffset))
		}
	case '-':
		if startOffset+1 < len(lexer.source.Contents) && lexer.source.Contents[startOffset+1] == '>' {
			endOffset++
			token = NewToken(Arrow, "->", lexer.spanBetween(startOffset, endOffset))
		} else {
			token = NewToken(Minus, "-", lexer.spanBetween(startOffset, endOffset))
		}
	case '*':
		token = NewToken(Asterisk, "*", lexer.spanBetween(startOffset, endOffset))
	case '/':
		// Handle regex literals vs division/comments
		if startOffset+1 < len(lexer.source.Contents) && lexer.source.Contents[startOffset+1] == '>' {
			endOffset++
			token = NewToken(SlashGreaterThan, "/>", lexer.spanBetween(startOffset, endOffset))
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
			value := lexer.source.Contents[startOffset:i]
			token = NewToken(LineComment, value, lexer.spanBetween(startOffset, endOffset))
		} else if startOffset+1 < len(lexer.source.Contents) && lexer.source.Contents[startOffset+1] == '*' {
			i := startOffset + 2
			n := len(lexer.source.Contents)
			for i < n {
				if i+1 < n && lexer.source.Contents[i] == '*' && lexer.source.Contents[i+1] == '/' {
					i += 2
					break
				}
				if lexer.source.Contents[i] == '\n' {
				} else {
				}
				i++
			}
			endOffset = i
			value := lexer.source.Contents[startOffset:i]
			token = NewToken(BlockComment, value, lexer.spanBetween(startOffset, endOffset))
		} else {
			token = NewToken(Slash, "/", lexer.spanBetween(startOffset, endOffset))
		}
	case '=':
		if startOffset+1 < len(lexer.source.Contents) && lexer.source.Contents[startOffset+1] == '=' {
			endOffset++
			token = NewToken(EqualEqual, "==", lexer.spanBetween(startOffset, endOffset))
		} else if startOffset+1 < len(lexer.source.Contents) && lexer.source.Contents[startOffset+1] == '>' {
			endOffset++
			token = NewToken(FatArrow, "=>", lexer.spanBetween(startOffset, endOffset))
		} else {
			token = NewToken(Equal, "=", lexer.spanBetween(startOffset, endOffset))
		}
	case ',':
		token = NewToken(Comma, ",", lexer.spanBetween(startOffset, endOffset))
	case ';':
		token = NewToken(Semicolon, ";", lexer.spanBetween(startOffset, endOffset))
	case '(':
		token = NewToken(OpenParen, "(", lexer.spanBetween(startOffset, endOffset))
	case ')':
		token = NewToken(CloseParen, ")", lexer.spanBetween(startOffset, endOffset))
	case '{':
		token = NewToken(OpenBrace, "{", lexer.spanBetween(startOffset, endOffset))
	case '}':
		token = NewToken(CloseBrace, "}", lexer.spanBetween(startOffset, endOffset))
	case '[':
		token = NewToken(OpenBracket, "[", lexer.spanBetween(startOffset, endOffset))
	case ']':
		token = NewToken(CloseBracket, "]", lexer.spanBetween(startOffset, endOffset))
	case '<':
		if startOffset+1 < len(lexer.source.Contents) && lexer.source.Contents[startOffset+1] == '=' {
			endOffset++
			token = NewToken(LessThanEqual, "<=", lexer.spanBetween(startOffset, endOffset))
		} else if startOffset+1 < len(lexer.source.Contents) && lexer.source.Contents[startOffset+1] == '/' {
			endOffset++
			token = NewToken(LessThanSlash, "</", lexer.spanBetween(startOffset, endOffset))
		} else {
			token = NewToken(LessThan, "<", lexer.spanBetween(startOffset, endOffset))
		}
	case '>':
		if startOffset+1 < len(lexer.source.Contents) && lexer.source.Contents[startOffset+1] == '=' {
			endOffset++
			token = NewToken(GreaterThanEqual, ">=", lexer.spanBetween(startOffset, endOffset))
		} else {
			token = NewToken(GreaterThan, ">", lexer.spanBetween(startOffset, endOffset))
		}
	case '|':
		if startOffset+1 < len(lexer.source.Contents) && lexer.source.Contents[startOffset+1] == '|' {
			endOffset++
			token = NewToken(PipePipe, "||", lexer.spanBetween(startOffset, endOffset))
		} else {
			token = NewToken(Pipe, "|", lexer.spanBetween(startOffset, endOffset))
		}
	case '&':
		if startOffset+1 < len(lexer.source.Contents) && lexer.source.Contents[startOffset+1] == '&' {
			endOffset++
			token = NewToken(AmpersandAmpersand, "&&", lexer.spanBetween(startOffset, endOffset))
		} else {
			token = NewToken(Ampersand, "&", lexer.spanBetween(startOffset, endOffset))
		}
	case '`':
		token = NewToken(BackTick, "`", lexer.spanBetween(startOffset, endOffset))
	case '?':
		token = NewToken(Question, "?", lexer.spanBetween(startOffset, endOffset))
	case '!':
		if startOffset+1 < len(lexer.source.Contents) && lexer.source.Contents[startOffset+1] == '=' {
			endOffset++
			token = NewToken(NotEqual, "!=", lexer.spanBetween(startOffset, endOffset))
		} else {
			token = NewToken(Bang, "!", lexer.spanBetween(startOffset, endOffset))
		}
	case ':':
		if startOffset+1 < len(lexer.source.Contents) && lexer.source.Contents[startOffset+1] == ':' {
			endOffset++
			token = NewToken(DoubleColon, "::", lexer.spanBetween(startOffset, endOffset))
		} else {
			token = NewToken(Colon, ":", lexer.spanBetween(startOffset, endOffset))
		}
	case '"', '\'':
		contents := lexer.source.Contents
		n := len(contents)
		i := startOffset + 1
		quote := codePoint // remember which quote character we're looking for
		for i < n {
			c := contents[i]
			if rune(c) == quote {
				break
			}
			i++
		}
		endOffset = i + 1                    // + 1 to include the closing quote
		value := contents[startOffset+1 : i] // without the quotes
		token = NewToken(StrLit, value, lexer.spanBetween(startOffset, endOffset))
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
					token = NewToken(DotDotDot, "...", lexer.spanBetween(startOffset, endOffset))
				} else {
					token = NewToken(Dot, ".", lexer.spanBetween(startOffset, endOffset))
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
				token = NewToken(NumLit, contents[startOffset:i], lexer.spanBetween(startOffset, endOffset))
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
				token = NewToken(NumLit, contents[startOffset:i], lexer.spanBetween(startOffset, endOffset))
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
				token = NewToken(NumLit, contents[startOffset:i], lexer.spanBetween(startOffset, endOffset))
			}
		}
	default:
		c := codePoint
		if lexer_util.IsIdentStart(c) {
			value, identEndOffset, _ := lexer_util.ScanIdent(lexer.source.Contents, startOffset)
			endOffset = identEndOffset

			span := lexer.spanBetween(startOffset, endOffset)

			if keyword, ok := keywords[value]; ok {
				token = NewToken(keyword, value, span)
			} else if value == "_" {
				token = NewToken(Underscore, value, span)
			} else {
				token = NewToken(Identifier, value, span)
			}
		} else if startOffset >= len(lexer.source.Contents) {
			token = NewToken(EndOfFile, "", lexer.spanBetween(startOffset, startOffset))
		} else {
			token = NewToken(Invalid, "", lexer.spanBetween(startOffset, startOffset))
		}
	}

	lexer.currentOffset = endOffset
	lexer.lastToken = token // Track the last token for regex context

	return token
}

func (lexer *Lexer) saveState() *Lexer {
	return &Lexer{
		source:        lexer.source,
		currentOffset: lexer.currentOffset,
		lastToken:     lexer.lastToken,
	}
}

func (lexer *Lexer) restoreState(saved *Lexer) {
	lexer.source = saved.source
	lexer.currentOffset = saved.currentOffset
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

	if startOffset >= len(lexer.source.Contents) {
		return nil
	}

	// Skip whitespace
	startOffset = lexer.skipWhitespace(startOffset)

	contents := lexer.source.Contents
	if startOffset >= len(contents) {
		return nil
	}

	// Scan identifier
	value, endOffset, _ := lexer_util.ScanIdent(lexer.source.Contents, startOffset)
	if value == "" {
		return nil
	}

	span := lexer.spanBetween(startOffset, endOffset)

	// Don't check keywords map - treat everything as an identifier
	// This allows reserved words to be used as identifiers in appropriate contexts
	if value == "_" {
		return NewToken(Underscore, value, span)
	}
	return NewToken(Identifier, value, span)
}
