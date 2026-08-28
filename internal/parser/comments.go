package parser

import (
	"github.com/escalier-lang/escalier/internal/ast"
)

// LexComments returns every comment in source, sorted by start offset.
//
// It runs its own pass over the file rather than collecting comments as the
// parser consumes them. The parser backtracks by restoring a saved lexer
// state, so a comment the parser walks over can be walked over again on the
// retry, and a comment inside an abandoned attempt is never reached at all.
// Lexing the file once from a fresh lexer sees each comment exactly once.
func LexComments(source *ast.Source) []*ast.Comment {
	var comments []*ast.Comment
	for _, token := range NewLexer(source).Lex() {
		var kind ast.CommentKind
		switch token.Type {
		case LineComment:
			kind = ast.LineCommentKind
		case BlockComment:
			kind = ast.BlockCommentKind
		default:
			continue
		}
		comments = append(comments, ast.NewComment(kind, token.Value, token.Span))
	}
	return comments
}
