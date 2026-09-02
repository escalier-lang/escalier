package parser

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/stretchr/testify/require"
)

// attachments parses src as a script, attaches its comments, and renders each
// attachment as "<slot> <node type> <comment text>" in walk order. Rendering
// the whole result lets a test assert every placement at once instead of
// drilling into one node at a time.
func attachments(t *testing.T, src string) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	source := &ast.Source{ID: 0, Path: "input.esc", Contents: src}
	script, errors := NewParser(ctx, source).ParseScript()
	require.Empty(t, errors)

	unattached := ast.AttachComments(script, script.Comments, source.LineMap())
	c := &attachmentLister{DefaultVisitor: ast.DefaultVisitor{}}
	script.Accept(c)
	c.record(script)
	for _, comment := range unattached {
		c.lines = append(c.lines, "unattached "+comment.Text)
	}
	return c.lines
}

type attachmentLister struct {
	ast.DefaultVisitor
	lines []string
	seen  map[ast.Node]bool
}

func (c *attachmentLister) record(n ast.Node) {
	if c.seen == nil {
		c.seen = map[ast.Node]bool{}
	}
	if c.seen[n] {
		return
	}
	c.seen[n] = true
	for _, slot := range []struct {
		name     string
		comments []*ast.Comment
	}{
		{"leading", n.LeadingComments()},
		{"trailing", n.TrailingComments()},
		{"dangling", n.DanglingComments()},
	} {
		for _, comment := range slot.comments {
			c.lines = append(c.lines, fmt.Sprintf("%s %T %s", slot.name, n, comment.Text))
		}
	}
}

func (c *attachmentLister) EnterExpr(e ast.Expr) bool               { c.record(e); return true }
func (c *attachmentLister) EnterStmt(s ast.Stmt) bool               { c.record(s); return true }
func (c *attachmentLister) EnterDecl(d ast.Decl) bool               { c.record(d); return true }
func (c *attachmentLister) EnterTypeAnn(t ast.TypeAnn) bool         { c.record(t); return true }
func (c *attachmentLister) EnterPat(p ast.Pat) bool                 { c.record(p); return true }
func (c *attachmentLister) EnterClassElem(e ast.ClassElem) bool     { c.record(e); return true }
func (c *attachmentLister) EnterObjExprElem(e ast.ObjExprElem) bool { c.record(e); return true }

func TestAttachComments(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "a comment on its own line leads the statement below it",
			src:  "// note\nval x = 1\n",
			want: []string{"leading *ast.DeclStmt // note"},
		},
		{
			name: "a comment after a statement trails it",
			src:  "val x = 1 // note\n",
			want: []string{"trailing *ast.DeclStmt // note"},
		},
		{
			name: "a block comment before an expression leads that expression",
			src:  "val x = /* three */ 3\n",
			want: []string{"leading *ast.LiteralExpr /* three */"},
		},
		{
			name: "a trailing comment beats leading the next statement",
			src:  "val x = 1 // about x\nval y = 2\n",
			want: []string{"trailing *ast.DeclStmt // about x"},
		},
		{
			name: "each statement keeps its own comment",
			src:  "// about x\nval x = 1\n// about y\nval y = 2\n",
			want: []string{
				"leading *ast.DeclStmt // about x",
				"leading *ast.DeclStmt // about y",
			},
		},
		{
			name: "several comments above one statement all lead it",
			src:  "// first\n// second\nval x = 1\n",
			want: []string{
				"leading *ast.DeclStmt // first",
				"leading *ast.DeclStmt // second",
			},
		},
		{
			name: "a comment inside a body leads the statement it precedes",
			src:  "fn f() {\n    // inner\n    return 1\n}\n",
			want: []string{"leading *ast.ReturnStmt // inner"},
		},
		{
			name: "a comment between a condition and its brace trails the condition",
			src:  "val x = if true /* here */ {\n    1\n} else {\n    2\n}\n",
			want: []string{"trailing *ast.LiteralExpr /* here */"},
		},
		{
			name: "a comment before an argument leads that argument",
			src:  "val x = f(a, /* b */ b)\n",
			want: []string{"leading *ast.IdentExpr /* b */"},
		},
		{
			name: "a comment at the end of the file dangles on the script",
			src:  "val x = 1\n// last word\n",
			want: []string{"dangling *ast.Script // last word"},
		},
		{
			name: "a script with no comments attaches nothing",
			src:  "val x = 1\n",
			want: nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, nilIfEmpty(attachments(t, tc.src)))
		})
	}
}

// A comment alone in a block has no sibling to lead or trail, so it lands in
// the dangling slot of the innermost node enclosing it. A `fn` declaration
// holds its block directly, so that node is the FuncDecl.
func TestAttachCommentsDanglesInAnEmptyBody(t *testing.T) {
	t.Parallel()
	require.Equal(t, []string{"dangling *ast.FuncDecl // nothing here"},
		attachments(t, "fn f() {\n    // nothing here\n}\n"))
}

// Running the pass twice leaves each node holding the comment once. The pass
// collects every placement before writing any slot, so the second run replaces
// what the first wrote instead of appending to it.
func TestAttachCommentsIsRepeatable(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	source := &ast.Source{ID: 0, Path: "input.esc", Contents: "// note\nval x = 1\n"}
	script, errors := NewParser(ctx, source).ParseScript()
	require.Empty(t, errors)

	require.Empty(t, ast.AttachComments(script, script.Comments, source.LineMap()))
	require.Empty(t, ast.AttachComments(script, script.Comments, source.LineMap()))

	require.Len(t, script.Stmts, 1)
	require.Len(t, script.Stmts[0].LeadingComments(), 1)
	require.Equal(t, "// note", script.Stmts[0].LeadingComments()[0].Text)
}

// A comment from another file names a source the tree does not cover, so no
// node takes it.
func TestAttachCommentsRejectsAnotherSource(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	source := &ast.Source{ID: 0, Path: "input.esc", Contents: "val x = 1\n"}
	script, errors := NewParser(ctx, source).ParseScript()
	require.Empty(t, errors)

	elsewhere := ast.NewComment(ast.LineCommentKind, "// elsewhere",
		ast.NewSpan(ast.Location{Offset: 0}, ast.Location{Offset: 12}, 7))
	unattached := ast.AttachComments(script, []*ast.Comment{elsewhere}, source.LineMap())
	require.Len(t, unattached, 1)
	require.Equal(t, "// elsewhere", unattached[0].Text)
}
