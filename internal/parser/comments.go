package parser

import (
	"sort"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
)

// commentLog collects the comments a lexer produces.
//
// The lexer records into it rather than the parser collecting comments as it
// consumes tokens, and rather than a separate pass over the file. Recording
// at the lexer is what keeps a `//` inside a template literal or inside JSX
// text from being taken for a comment: those regions are read by lexQuasi and
// lexJSXText, which produce one token for the whole run of text and never
// produce a comment token.
//
// A parser backtracks by restoring a saved lexer state, so it lexes the same
// region more than once and offers the same comment more than once. seen
// holds the start offset of every comment already recorded, which is what
// makes the second offer a no-op. Every lexer derived from a parser's save
// point shares one log, so a comment recorded on an abandoned attempt is
// still recorded once.
type commentLog struct {
	comments []*ast.Comment
	seen     set.Set[int]
}

func newCommentLog() *commentLog {
	return &commentLog{seen: set.NewSet[int]()}
}

// record adds a comment token to the log, ignoring one already recorded.
func (l *commentLog) record(token *Token) {
	var kind ast.CommentKind
	switch token.Type {
	case LineComment:
		kind = ast.LineCommentKind
	case BlockComment:
		kind = ast.BlockCommentKind
	default:
		return
	}
	start := token.Span.Start.Offset
	if l.seen.Contains(start) {
		return
	}
	l.seen.Add(start)
	l.comments = append(l.comments, ast.NewComment(kind, token.Value, token.Span))
}

// sorted returns the recorded comments ordered by start offset. Backtracking
// can record a later comment before an earlier one, so the log is ordered
// here rather than assumed to be in order.
func (l *commentLog) sorted() []*ast.Comment {
	out := make([]*ast.Comment, len(l.comments))
	copy(out, l.comments)
	sort.Slice(out, func(i, j int) bool {
		return out[i].Span().Start.Offset < out[j].Span().Start.Offset
	})
	return out
}

// Comments returns the comments the parser's lexer has read so far, ordered by
// start offset. Call it after parsing, when the lexer has reached the end of
// the file.
func (p *Parser) Comments() []*ast.Comment {
	return p.lexer.comments.sorted()
}

// LexComments returns every comment in source, ordered by start offset.
//
// It lexes the whole file, so unlike the comments a parse collects it does not
// depend on the parser reaching every region. It reads a template literal's
// text and JSX text as ordinary tokens, since neither is entered without a
// parser to recognize it, so a `//` inside either is reported as a comment.
// Prefer Parser.Comments for a file that is being parsed anyway.
func LexComments(source *ast.Source) []*ast.Comment {
	lexer := NewLexer(source)
	lexer.Lex()
	return lexer.comments.sorted()
}
