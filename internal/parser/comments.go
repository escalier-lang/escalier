package parser

import (
	"sort"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
)

// commentLog collects the comments a lexer produces.
//
// The lexer records into it, rather than the parser collecting comments as it
// consumes tokens or a separate pass reading the file again. Recording at the
// lexer is what keeps a `//` inside a template literal or inside JSX text
// from being taken for a comment. lexQuasi and lexJSXText read those regions,
// and each produces one token for the whole run of text rather than a comment
// token.
//
// A parser backtracks by restoring a saved lexer state, so it lexes the same
// region more than once and offers the same comment more than once. seen
// holds the start offset of every comment already recorded, which is what
// makes the second offer a no-op. Every lexer derived from a parser's save
// point shares one log, so a comment recorded on an abandoned attempt is
// still recorded once.
//
// A lookahead can read a region as ordinary tokens before the parser hands it
// to lexQuasi or lexJSXText, recording a comment inside text that only looks
// like one. Both functions call discard to drop it.
type commentLog struct {
	comments []*ast.Comment
	seen     set.Set[int]
}

func newCommentLog() *commentLog {
	return &commentLog{comments: nil, seen: set.NewSet[int]()}
}

// record adds a comment token to the log, ignoring one already recorded.
func (l *commentLog) record(token *Token) {
	var kind ast.CommentKind
	//nolint: exhaustive
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

// discard drops the comments recorded to start within [start, end).
//
// A lookahead reaches a region before the parser knows what it is. jsxChildren
// peeks a token before handing the run to lexJSXText, and that peek reads the
// `// nope` in `<div>// nope</div>` as a line comment. Lexing the region as
// one token of text drops the comment recorded inside it. Backtracking that
// re-reads the region as ordinary source records it again, and there it is a
// comment.
func (l *commentLog) discard(start, end int) {
	kept := l.comments[:0]
	for _, comment := range l.comments {
		at := comment.Span().Start.Offset
		if at >= start && at < end {
			l.seen.Remove(at)
			continue
		}
		kept = append(kept, comment)
	}
	l.comments = kept
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

// skipComments consumes the comment tokens ahead of the next non-comment
// token and returns that token, without consuming it.
//
// A production that reads one member of a list calls this before it dispatches
// on the token it finds. Without it the comment token itself is read where the
// member belongs, and `{a: 1, /* m */ b: 2}` reports "Expected a property
// name" on the comment. The comment stays in the parser's comment log either
// way, and ast.AttachComments gives it an owner once the parse is done.
func (p *Parser) skipComments() *Token {
	for {
		select {
		case <-p.ctx.Done():
			return p.lexer.peek()
		default:
		}
		token := p.lexer.peek()
		if token.Type != LineComment && token.Type != BlockComment {
			return token
		}
		p.lexer.consume()
	}
}
