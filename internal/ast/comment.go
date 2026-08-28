package ast

import (
	"sort"

	"github.com/escalier-lang/escalier/internal/lexer_util"
)

// CommentKind distinguishes the two comment syntaxes the lexer recognizes.
type CommentKind int

const (
	// LineCommentKind is a `//` comment, running to the end of the line.
	LineCommentKind CommentKind = iota
	// BlockCommentKind is a `/* ... */` comment, which may span lines.
	BlockCommentKind
)

// Comment is one comment found in a source file. Comments are not part of
// any node's children. They are collected per source file into a list that
// hangs off the Script or Module the file parsed into, and associated with
// nodes on demand by NewCommentMap.
//
// Text is the comment's source text with its delimiters kept, so a line
// comment starts with `//` and a block comment starts with `/*` and ends
// with `*/` unless the file ended first.
type Comment struct {
	Kind CommentKind
	Text string
	span Span
}

func NewComment(kind CommentKind, text string, span Span) *Comment {
	return &Comment{Kind: kind, Text: text, span: span}
}

func (c *Comment) Span() Span { return c.span }

// IsDoc reports whether the comment is a JSDoc block, meaning a `/** ... */`
// comment. A declaration's own JSDoc is retained separately through the
// Documented interface, so a comment map built over a parsed file reports
// the same text twice: once here and once as the owning node's Doc.
func (c *Comment) IsDoc() bool {
	return c.Kind == BlockCommentKind && lexer_util.IsJSDoc(c.Text)
}

// CommentsInRange returns the comments that lie entirely within the byte
// range [start, end) of the source file the comments came from. The input
// must be sorted by start offset, which is the order the lexer produces and
// the order every parse entry point stores.
//
// The range is a byte range rather than a Span because the callers that need
// it are splicing source text, and because a caller holding only an offset
// has no line and column to build a Span from.
func CommentsInRange(comments []*Comment, start, end int) []*Comment {
	lo := sort.Search(len(comments), func(i int) bool {
		return comments[i].span.Start.Offset >= start
	})
	hi := lo
	for hi < len(comments) && comments[hi].span.End.Offset <= end {
		hi++
	}
	return comments[lo:hi]
}

// CommentMap associates each comment with the innermost node whose span
// encloses it.
//
// Enclosure is the only rule applied. A comment written above a declaration
// sits outside that declaration's span, so it belongs to whatever larger node
// encloses it and is unattached at the top level of a file. Deciding that such
// a comment leads the declaration below it is a separate question, tracked in
// #1311; nothing here presumes an answer to it.
type CommentMap struct {
	byNode     map[Node][]*Comment
	unattached []*Comment
}

// Comments returns the comments enclosed by n and by no descendant of n, in
// source order. The result is empty for a node that encloses no comment.
func (m *CommentMap) Comments(n Node) []*Comment {
	return m.byNode[n]
}

// Unattached returns the comments no node in the walked tree encloses, in
// source order. At the top level of a file this is every comment between
// declarations, since a declaration's span starts at its first token.
func (m *CommentMap) Unattached() []*Comment {
	return m.unattached
}

// NewCommentMap walks root and assigns each comment to the innermost node
// enclosing it.
//
// The walk claims a comment on the way out of a node rather than on the way
// in. A visitor exits a node's children before the node itself, so a child
// that encloses the comment always claims it first and the innermost node
// wins without the walk tracking depth.
func NewCommentMap(root Node, comments []*Comment) *CommentMap {
	m := &CommentMap{byNode: map[Node][]*Comment{}}
	if len(comments) == 0 {
		return m
	}

	sorted := make([]*Comment, len(comments))
	copy(sorted, comments)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].span.Start.Offset < sorted[j].span.Start.Offset
	})

	c := &commentClaimer{comments: sorted, claimed: make([]bool, len(sorted)), m: m}
	root.Accept(c)

	for i, comment := range sorted {
		if !c.claimed[i] {
			m.unattached = append(m.unattached, comment)
		}
	}
	return m
}

// commentClaimer is the visitor NewCommentMap runs. It holds the comments
// sorted by start offset alongside a parallel flag per comment recording
// whether some node has already claimed it.
type commentClaimer struct {
	DefaultVisitor
	comments []*Comment
	claimed  []bool
	m        *CommentMap
}

// claim assigns to n every comment n encloses that no node has claimed yet.
func (c *commentClaimer) claim(n Node) {
	span := n.Span()
	// A synthesized span carries no offsets, so it would enclose only a
	// comment at the very start of the file and never the one meant for it.
	// Treating it as enclosing nothing keeps the claim on a real node.
	if span.End.Offset <= span.Start.Offset {
		return
	}
	lo := sort.Search(len(c.comments), func(i int) bool {
		return c.comments[i].span.Start.Offset >= span.Start.Offset
	})
	for i := lo; i < len(c.comments); i++ {
		comment := c.comments[i]
		if comment.span.Start.Offset >= span.End.Offset {
			break
		}
		if c.claimed[i] || comment.span.End.Offset > span.End.Offset {
			continue
		}
		if comment.span.SourceID != span.SourceID {
			continue
		}
		c.claimed[i] = true
		c.m.byNode[n] = append(c.m.byNode[n], comment)
	}
}

func (c *commentClaimer) ExitExpr(e Expr)               { c.claim(e) }
func (c *commentClaimer) ExitStmt(s Stmt)               { c.claim(s) }
func (c *commentClaimer) ExitDecl(d Decl)               { c.claim(d) }
func (c *commentClaimer) ExitTypeAnn(t TypeAnn)         { c.claim(t) }
func (c *commentClaimer) ExitPat(p Pat)                 { c.claim(p) }
func (c *commentClaimer) ExitClassElem(e ClassElem)     { c.claim(e) }
func (c *commentClaimer) ExitObjExprElem(e ObjExprElem) { c.claim(e) }
