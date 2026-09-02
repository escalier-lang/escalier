package parser

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/stretchr/testify/require"
)

func parseSource(t *testing.T, src string) (*ast.Module, *ast.Source) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	source := &ast.Source{ID: 0, Path: "lib/index.esc", Contents: src}
	module, errors := ParseLibFiles(ctx, []*ast.Source{source})
	require.Empty(t, errors)
	return module, source
}

func TestLexComments(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		contents string
		want     []string
	}{
		{"no comments", "val x = 1\n", nil},
		{"line comment", "// note\nval x = 1\n", []string{"// note"}},
		{"block comment", "/* note */\nval x = 1\n", []string{"/* note */"}},
		{"jsdoc", "/** note */\nval x = 1\n", []string{"/** note */"}},
		{
			"several in order",
			"// first\nval x = 1 // second\n/* third */\n",
			[]string{"// first", "// second", "/* third */"},
		},
		{
			"inside a function body",
			"fn f() {\n    // inner\n    return 1\n}\n",
			[]string{"// inner"},
		},
		{"comment marker inside a string", "val x = \"// not a comment\"\n", nil},
		{"slashes inside a string", "val x = \"http://example.com\"\n", nil},
		{"unterminated block comment", "val x = 1\n/* note", []string{"/* note"}},
		{
			"block comment spanning lines",
			"/* one\n   two */\nval x = 1\n",
			[]string{"/* one\n   two */"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			comments := LexComments(&ast.Source{Contents: tc.contents})
			text := make([]string, len(comments))
			for i, c := range comments {
				text[i] = c.Text
			}
			require.Equal(t, tc.want, nilIfEmpty(text))
		})
	}
}

func nilIfEmpty(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return s
}

func TestLexComments_SpansCoverTheCommentText(t *testing.T) {
	t.Parallel()
	// A multi-byte character ahead of a comment shifts its byte offset past
	// its column, so slicing by offset is the only way to recover the text.
	contents := "val π = 1 // a comment\nval x = 2\n"
	comments := LexComments(&ast.Source{Contents: contents})
	require.Len(t, comments, 1)

	span := comments[0].Span()
	require.Equal(t, "// a comment", contents[span.Start.Offset:span.End.Offset])
	require.Equal(t, comments[0].Text, contents[span.Start.Offset:span.End.Offset])
	require.Equal(t, 1, span.Start.Line)
	// The column counts code points, so it trails the byte offset by the one
	// extra byte `π` occupies.
	require.Equal(t, 11, span.Start.Column)
	require.Equal(t, 11, span.Start.Offset)
}

func TestLexComments_IsDoc(t *testing.T) {
	t.Parallel()
	comments := LexComments(&ast.Source{
		Contents: "// line\n/* block */\n/** doc */\nval x = 1\n",
	})
	require.Len(t, comments, 3)
	require.False(t, comments[0].IsDoc())
	require.False(t, comments[1].IsDoc())
	require.True(t, comments[2].IsDoc())
}

func TestParseLibFiles_CollectsComments(t *testing.T) {
	t.Parallel()
	module, source := parseSource(t, "// leading\nval x = 1 // trailing\n")
	comments := module.Comments[source.ID]
	require.Len(t, comments, 2)
	require.Equal(t, "// leading", comments[0].Text)
	require.Equal(t, "// trailing", comments[1].Text)
}

func TestParseScript_CollectsComments(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	source := &ast.Source{ID: 0, Path: "input.esc", Contents: "// leading\nval x = 1\n"}
	script, errors := NewParser(ctx, source).ParseScript()
	require.Empty(t, errors)
	require.Len(t, script.Comments, 1)
	require.Equal(t, "// leading", script.Comments[0].Text)
}

func TestNewCommentMap(t *testing.T) {
	t.Parallel()
	module, source := parseSource(t, `// above the function
fn f() {
    // inside the body
    return 1
}
// between declarations
val x = 1
`)
	comments := module.Comments[source.ID]
	require.Len(t, comments, 3)

	m := ast.NewCommentMap(module, comments)

	// A comment above a declaration sits outside every node's span, so no
	// node claims it. Which declaration it leads is #1311's question.
	unattached := m.Unattached()
	require.Len(t, unattached, 2)
	require.Equal(t, "// above the function", unattached[0].Text)
	require.Equal(t, "// between declarations", unattached[1].Text)

	// The body comment is claimed by the innermost node containing it, the
	// return statement's enclosing function rather than the module.
	var claimed []*ast.Comment
	for _, ns := range namespacesOf(module) {
		for _, decl := range ns.Decls {
			claimed = append(claimed, m.Comments(decl)...)
		}
	}
	require.Len(t, claimed, 1)
	require.Equal(t, "// inside the body", claimed[0].Text)
}

func TestNewCommentMap_InnermostNodeWins(t *testing.T) {
	t.Parallel()
	module, source := parseSource(t, `fn outer() {
    fn inner() {
        // deepest
        return 1
    }
    return inner
}
`)
	m := ast.NewCommentMap(module, module.Comments[source.ID])

	decls := namespacesOf(module)[0].Decls
	require.Len(t, decls, 1)
	outer := decls[0]
	// The comment lies inside `outer` too, but `inner` encloses it and exits
	// the walk first, so `outer` is left with nothing.
	require.Empty(t, m.Comments(outer))

	inner := innerFuncDecl(t, outer)
	require.Len(t, m.Comments(inner), 1)
	require.Equal(t, "// deepest", m.Comments(inner)[0].Text)
}

func TestNewCommentMap_NoComments(t *testing.T) {
	t.Parallel()
	module, source := parseSource(t, "val x = 1\n")
	m := ast.NewCommentMap(module, module.Comments[source.ID])
	require.Empty(t, m.Unattached())
	require.Empty(t, m.Comments(namespacesOf(module)[0].Decls[0]))
}

func TestCommentsInRange(t *testing.T) {
	t.Parallel()
	contents := "// one\nval x = 1\n// two\nval y = 2\n// three\n"
	comments := LexComments(&ast.Source{Contents: contents})
	require.Len(t, comments, 3)

	tests := []struct {
		name       string
		start, end int
		want       []string
	}{
		{"whole file", 0, len(contents), []string{"// one", "// two", "// three"}},
		{"empty range", 0, 0, nil},
		{"first comment only", 0, 7, []string{"// one"}},
		{"excludes a comment ending past the range", 0, 5, nil},
		{"middle of the file", 7, 24, []string{"// two"}},
		{"past the last comment", 41, len(contents), nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ast.CommentsInRange(comments, tc.start, tc.end)
			text := make([]string, len(got))
			for i, c := range got {
				text[i] = c.Text
			}
			require.Equal(t, tc.want, nilIfEmpty(text))
		})
	}
}

func namespacesOf(module *ast.Module) []*ast.Namespace {
	var out []*ast.Namespace
	module.Namespaces.Scan(func(_ string, ns *ast.Namespace) bool {
		out = append(out, ns)
		return true
	})
	return out
}

// innerFuncDecl returns the one function declaration nested inside decl.
func innerFuncDecl(t *testing.T, decl ast.Decl) ast.Decl {
	t.Helper()
	fn, ok := decl.(*ast.FuncDecl)
	require.True(t, ok, "decl is a FuncDecl")
	require.NotNil(t, fn.Body)
	for _, stmt := range fn.Body.Stmts {
		if declStmt, ok := stmt.(*ast.DeclStmt); ok {
			return declStmt.Decl
		}
	}
	require.Fail(t, "no nested declaration found")
	return nil
}

func TestParserComments_SkipsTemplateLiteralText(t *testing.T) {
	t.Parallel()
	// A template literal's text is read by lexQuasi as one run, so slashes
	// inside it never open a comment. The interpolation between `${` and `}`
	// is ordinary source again, so a comment there is a comment.
	module, source := parseSource(t, "val x = `see http://example.com`\n"+
		"val y = `a ${ /* inner */ 1 } b`\n")
	comments := module.Comments[source.ID]
	require.Len(t, comments, 1)
	require.Equal(t, "/* inner */", comments[0].Text)
}

func TestParserComments_NestedTemplateLiterals(t *testing.T) {
	t.Parallel()
	module, source := parseSource(t, "val x = `a ${ `b ${ 1 } // not a comment` } c`\n")
	require.Empty(t, module.Comments[source.ID])
}

func TestSpanOffsets_AgreeWithLineAndColumn(t *testing.T) {
	t.Parallel()
	// A declaration's span must be sliceable by offset. A token kind lexed
	// outside next(), such as a template literal's text, is the case that can
	// leave the offset behind while line and column advance.
	//
	// The check covers declaration spans rather than every token, because a
	// block comment's end Column skips its delimiters and so does not line up
	// with its Offset.
	tests := []struct {
		name string
		src  string
	}{
		{"plain decl", "val x = 42\n"},
		{"template literal", "val x = `hi`\n"},
		{"template literal with interpolation", "val x = `a ${ 1 } b`\n"},
		{"multi-byte character", "val π = 1\n"},
		{"string literal", "val x = \"hi\"\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			module, _ := parseSource(t, tc.src)
			decls := namespacesOf(module)[0].Decls
			require.Len(t, decls, 1)

			span := decls[0].Span()
			require.Equal(t,
				lineColumnOffset(tc.src, span.End),
				span.End.Offset,
				"end offset disagrees with line %d column %d", span.End.Line, span.End.Column)
			require.Equal(t,
				lineColumnOffset(tc.src, span.Start),
				span.Start.Offset,
				"start offset disagrees with line %d column %d", span.Start.Line, span.Start.Column)
		})
	}
}

// lineColumnOffset converts a location's line and column into a byte offset by
// counting code points, the independent derivation the lexer's own offset
// bookkeeping is checked against.
func lineColumnOffset(contents string, loc ast.Location) int {
	line, column := 1, 1
	for i, r := range contents {
		if line == loc.Line && column == loc.Column {
			return i
		}
		if r == '\n' {
			line++
			column = 1
		} else {
			column++
		}
	}
	return len(contents)
}

func TestParserComments_SkipsJSXText(t *testing.T) {
	t.Parallel()
	// jsxChildren peeks a token before handing the run to lexJSXText, and the
	// peek reads text starting with `//` as a line comment. Once the run has
	// been lexed as JSX text, that comment is dropped.
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "line comment marker in JSX text",
			src:  "val x = <div>// nope</div>\n",
			want: nil,
		},
		{
			name: "block comment marker in JSX text",
			src:  "val x = <div>/* nope */</div>\n",
			want: nil,
		},
		{
			name: "a URL in JSX text",
			src:  "val x = <div>see http://example.com</div>\n",
			want: nil,
		},
		{
			name: "a real comment in a JSX expression container",
			src:  "val x = <div>{/* yes */ 1}</div>\n",
			want: []string{"/* yes */"},
		},
		{
			name: "a real comment beside JSX",
			src:  "// yes\nval x = <div>hi</div>\n",
			want: []string{"// yes"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			module, source := parseSource(t, tc.src)
			text := make([]string, 0, len(module.Comments[source.ID]))
			for _, c := range module.Comments[source.ID] {
				text = append(text, c.Text)
			}
			require.Equal(t, tc.want, nilIfEmpty(text))
		})
	}
}
