package parser

import (
	"unicode"
	"unicode/utf8"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/lexer_util"
	"github.com/escalier-lang/escalier/internal/set"
)

type Lexer struct {
	source        *ast.Source
	currentOffset int
	lastToken     *Token // Track last token for regex context
	// lastCodeEnd is the end offset of the last non-comment token read. Several
	// productions consume a trailing comment on their way to the next token, so
	// currentOffset can sit past one. A span closed at lastCodeEnd stops at the
	// code instead.
	lastCodeEnd int
	// comments records every comment token this lexer produces. Save points
	// share one log so backtracking neither loses a comment nor records it
	// twice; see commentLog.
	comments *commentLog
}

func NewLexer(source *ast.Source) *Lexer {
	return &Lexer{
		source:        source,
		currentOffset: 0,
		lastToken:     nil,
		lastCodeEnd:   0,
		comments:      newCommentLog(),
	}
}

var keywords = map[string]TokenType{
	"private":    Private,
	"fn":         Fn,
	"class":      Class,
	"get":        Get,
	"set":        Set,
	"static":     Static,
	"var":        Var,
	"val":        Val,
	"type":       Type,
	"return":     Return,
	"import":     Import,
	"export":     Export,
	"declare":    Declare,
	"override":   Override,
	"final":      Final,
	"infer":      Infer,
	"if":         If,
	"else":       Else,
	"enum":       Enum,
	"interface":  Interface,
	"match":      Match,
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
	"void":       Void,
	"mut":        Mut,
	"for":        For,
	"from":       From,
	"in":         In,
	"do":         Do,
	"symbol":     Symbol,
	"unique":     Unique,
	"keyof":      Keyof,
	"typeof":     Typeof,
	"readonly":   Readonly,
	"new":        New,
	"extends":    Extends,
	"super":      Super,
	"implements": Implements,
	"is":         Is,
	"asserts":    Asserts,
}

// keywordTokens holds the token type of every entry in keywords. A keyword
// token carries its source text in Value, so a parser position that accepts a
// name can turn one back into an identifier.
var keywordTokens = func() set.Set[TokenType] {
	types := set.NewSet[TokenType]()
	for _, tokenType := range keywords {
		types.Add(tokenType)
	}
	return types
}()

// isKeyword reports whether tokenType is the type the lexer gives a keyword.
// Property names accept keywords, so `catch`, `match`, and `symbol` name
// members the way an identifier does.
func isKeyword(tokenType TokenType) bool {
	return keywordTokens.Contains(tokenType)
}

// bindingKeywords names the keywords a parameter or function declaration
// accepts as an ordinary name. Codegen emits a binding name verbatim, so every
// entry is a word JavaScript allows as an identifier: `fn f(in: number)` would
// become `const in = ...`. The literals `undefined`, `null`, `true`, and
// `false` are out too, since a pattern reads each as the value it names.
var bindingKeywords = set.FromSlice([]TokenType{
	Any, Asserts, Async, Bigint, Boolean, Declare, Final, Fn, From, Gen, Get,
	Infer, Is, Keyof, Match, Mut, Never, Number, Override, Readonly, Set,
	String, Symbol, Throws, Type, Unique, Unknown, Val,
})

// bindsAsAName reports whether a keyword can stand where a binding name
// belongs. `String.prototype.substr(from, length)` converts to a parameter
// named `from`, and `Reflect.get` to a function named `get`.
func bindsAsAName(tokenType TokenType) bool {
	return bindingKeywords.Contains(tokenType)
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
func (lexer *Lexer) lexRegex(startOffset int) *Token {
	contents := lexer.source.Contents
	n := len(contents)
	i := startOffset + 1 // Skip the opening '/'

	// Parse the regex pattern
	for i < n {
		c := contents[i]
		if c == '/' {
			i++ // Include the closing '/'
			break
		}
		if c == '\\' && i+1 < n {
			// Skip escaped character (both the backslash and the next char)
			i += 2
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
	}

	// Parse regex flags (if any)
	for i < n {
		c := contents[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			i++
		} else {
			break
		}
	}

	endOffset := i
	value := contents[startOffset:endOffset]

	return NewToken(RegexLit, value, lexer.spanBetween(startOffset, endOffset))
}

func (lexer *Lexer) next() *Token {
	startOffset := lexer.currentOffset

	if startOffset >= len(lexer.source.Contents) {
		return NewToken(EndOfFile, "", lexer.spanBetween(startOffset, startOffset))
	}

	// Skip over whitespace with ASCII fast path
	for startOffset < len(lexer.source.Contents) {
		c := lexer.source.Contents[startOffset]
		if c == ' ' || c == '\t' || c == '\n' {
			startOffset++
		} else if c < 128 {
			// Non-whitespace ASCII, break
			break
		} else {
			// Non-ASCII character, use UTF-8 decoding
			codePoint, width := utf8.DecodeRuneInString(lexer.source.Contents[startOffset:])
			if unicode.IsSpace(codePoint) {
				startOffset += width
			} else {
				break
			}
		}
	}

	var codePoint rune
	var width int
	if startOffset < len(lexer.source.Contents) {
		if b := lexer.source.Contents[startOffset]; b < utf8.RuneSelf {
			codePoint, width = rune(b), 1
		} else {
			codePoint, width = utf8.DecodeRuneInString(lexer.source.Contents[startOffset:])
		}
	}

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
				i++
			}
			endOffset = i
			value := lexer.source.Contents[startOffset:i]
			token = NewToken(BlockComment, value, lexer.spanBetween(startOffset, endOffset))
		} else if lexer.isRegexContext() {
			// Lex as regex literal
			token = lexer.lexRegex(startOffset)
			endOffset = token.Span.End.Offset
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
	case '@':
		token = NewToken(AtSign, "@", lexer.spanBetween(startOffset, endOffset))
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
	case '~':
		token = NewToken(Negation, "~", lexer.spanBetween(startOffset, endOffset))
	case '`':
		token = NewToken(BackTick, "`", lexer.spanBetween(startOffset, endOffset))
	case '?':
		if startOffset+1 < len(lexer.source.Contents) && lexer.source.Contents[startOffset+1] == '.' {
			endOffset++
			token = NewToken(QuestionDot, "?.", lexer.spanBetween(startOffset, endOffset))
		} else if startOffset+1 < len(lexer.source.Contents) && lexer.source.Contents[startOffset+1] == '(' {
			endOffset++
			token = NewToken(QuestionOpenParen, "?(", lexer.spanBetween(startOffset, endOffset))
		} else if startOffset+1 < len(lexer.source.Contents) && lexer.source.Contents[startOffset+1] == '[' {
			endOffset++
			token = NewToken(QuestionOpenBracket, "?[", lexer.spanBetween(startOffset, endOffset))
		} else {
			token = NewToken(Question, "?", lexer.spanBetween(startOffset, endOffset))
		}
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
	case '\'':
		// Lifetime token: ' followed by an identifier (e.g. 'a, 'static).
		// Escalier uses double quotes for strings and has no character literals,
		// so a lone ' followed by an ident-start is unambiguously a lifetime.
		identValue, identEndOffset, _ := lexer_util.ScanIdent(lexer.source.Contents, startOffset+1)
		if identValue != "" {
			endOffset = identEndOffset
			token = NewToken(Lifetime, identValue, lexer.spanBetween(startOffset, endOffset))
		} else {
			token = NewToken(Invalid, "'", lexer.spanBetween(startOffset, endOffset))
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
		token = NewToken(StrLit, value, lexer.spanBetween(startOffset, endOffset))
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
			if startOffset+2 < len(contents) && contents[startOffset+1] == '.' && contents[startOffset+2] == '.' {
				endOffset += 2
				token = NewToken(DotDotDot, "...", lexer.spanBetween(startOffset, endOffset))
			} else {
				token = NewToken(Dot, ".", lexer.spanBetween(startOffset, endOffset))
			}
		} else {
			token = NewToken(NumLit, contents[startOffset:i], lexer.spanBetween(startOffset, endOffset))
		}
	default:
		value, identEndOffset, _ := lexer_util.ScanIdent(lexer.source.Contents, startOffset)
		if value != "" {
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
			// Invalid character - ensure we advance past it to avoid infinite loop
			// endOffset was already set to startOffset + width at the top
			token = NewToken(Invalid, "", lexer.spanBetween(startOffset, endOffset))
		}
	}

	lexer.currentOffset = endOffset
	lexer.lastToken = token // Track the last token for regex context
	if token.Type != LineComment && token.Type != BlockComment {
		lexer.lastCodeEnd = endOffset
	}
	lexer.comments.record(token)

	return token
}

func (lexer *Lexer) saveState() *Lexer {
	return &Lexer{
		source:        lexer.source,
		currentOffset: lexer.currentOffset,
		lastToken:     lexer.lastToken,
		lastCodeEnd:   lexer.lastCodeEnd,
		comments:      lexer.comments,
	}
}

func (lexer *Lexer) restoreState(saved *Lexer) {
	lexer.source = saved.source
	lexer.currentOffset = saved.currentOffset
	lexer.lastToken = saved.lastToken
	lexer.lastCodeEnd = saved.lastCodeEnd
}

// currentLoc returns the position the lexer will read from next. The parser
// uses it to close a span it opened at an earlier token.
func (lexer *Lexer) currentLoc() ast.Location {
	return ast.Location{Offset: lexer.currentOffset}
}

// lastCodeLoc returns the end of the last non-comment token the lexer read.
// The parser closes a span with it where currentLoc would reach past a comment
// written after the construct, as in `{a: number /* m */, b: string}`.
func (lexer *Lexer) lastCodeLoc() ast.Location {
	return ast.Location{Offset: lexer.lastCodeEnd}
}

// sameLine reports whether two positions fall on the same source line. The
// parser consults it where a line break separates two constructs that would
// otherwise read as one, such as an argument list opening on the line after
// its callee.
func (lexer *Lexer) sameLine(a, b ast.Location) bool {
	return lexer.source.LineMap().SameLine(a.Offset, b.Offset)
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

// peek2 returns the token two positions ahead without consuming input, the
// two-token lookahead a couple of contextual parses need — for example telling a
// variance modifier `out T` from a parameter named `out`.
func (lexer *Lexer) peek2() *Token {
	savedState := lexer.saveState()
	lexer.next()
	token := lexer.next()
	lexer.restoreState(savedState)
	return token
}

func (lexer *Lexer) consume() {
	lexer.next()
}

func (lexer *Lexer) lexQuasi() *Token {
	startOffset := lexer.currentOffset

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
		i++
	}
	endOffset := i

	lexer.currentOffset = endOffset
	lexer.comments.discard(startOffset, endOffset)

	var value string
	if i >= n {
		value = contents[startOffset:]
		// TODO: report an error
	} else {
		value = contents[startOffset:i]
	}

	return NewToken(Quasi, value, lexer.spanBetween(startOffset, endOffset))
}

func (lexer *Lexer) lexJSXText() *Token {
	startOffset := lexer.currentOffset

	contents := lexer.source.Contents
	n := len(contents)
	i := startOffset

	for i < n {
		c := contents[i]
		if c == '<' || c == '{' {
			break
		}
		i++
	}
	endOffset := i

	lexer.currentOffset = endOffset
	lexer.comments.discard(startOffset, endOffset)

	var value string
	if i >= n {
		value = contents[startOffset:]
		// TODO: report an errors
	} else {
		value = contents[startOffset:endOffset]
	}
	return NewToken(JSXText, value, lexer.spanBetween(startOffset, endOffset))
}

func (lexer *Lexer) Lex() []*Token {
	var tokens []*Token

	for lexer.currentOffset < len(lexer.source.Contents) {
		tokens = append(tokens, lexer.next())
	}

	return tokens
}

// Peek returns the next token without consuming it (exported for dts_parser)
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
