package parser

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/stretchr/testify/require"
)

func TestDeclDocs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "VarDecl",
			src:  "/** the answer */\nval x = 42",
			want: "/** the answer */",
		},
		{
			name: "FuncDecl",
			src:  "/** greets */\nfn greet() { return \"hi\" }",
			want: "/** greets */",
		},
		{
			name: "TypeDecl",
			src:  "/** a point */\ntype Point = {x: number, y: number}",
			want: "/** a point */",
		},
		{
			name: "InterfaceDecl",
			src:  "/** shape of a point */\ninterface Point { x: number }",
			want: "/** shape of a point */",
		},
		{
			name: "EnumDecl",
			src:  "/** the colors */\nenum Color { Red, Green }",
			want: "/** the colors */",
		},
		{
			name: "ClassDecl",
			src:  "/** a point */\nclass Point { x: number }",
			want: "/** a point */",
		},
		{
			name: "doc ahead of export",
			src:  "/** exported */\nexport val x = 1",
			want: "/** exported */",
		},
		{
			name: "doc ahead of declare",
			src:  "/** ambient */\ndeclare val x: number",
			want: "/** ambient */",
		},
		{
			name: "multi-line doc keeps its delimiters",
			src:  "/**\n * the answer\n */\nval x = 42",
			want: "/**\n * the answer\n */",
		},
		{
			name: "a line comment carries no doc",
			src:  "// just a note\nval x = 1",
			want: "",
		},
		{
			name: "a plain block comment carries no doc",
			src:  "/* just a note */\nval x = 1",
			want: "",
		},
		{
			name: "a line comment after the doc resets it",
			src:  "/** the answer */\n// but actually\nval x = 1",
			want: "",
		},
		{
			name: "the last of several docs wins",
			src:  "/** first */\n/** second */\nval x = 1",
			want: "/** second */",
		},
		{
			name: "no comment at all",
			src:  "val x = 1",
			want: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			decls, errors := ParseDecls(ctx, &ast.Source{Path: "input.esc", Contents: tc.src})
			require.Empty(t, errors)
			require.Len(t, decls, 1)
			require.Equal(t, tc.want, decls[0].Doc())
		})
	}
}

func TestDeclDocs_InsideADeclareModule(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	decls, errors := ParseDecls(ctx, &ast.Source{Path: "input.esc", Contents: `declare module "fs" {
    /** reads a file */
    declare fn readFile(path: string) -> string
    // not a doc
    declare val sep: string
}`})
	require.Empty(t, errors)
	require.Len(t, decls, 1)

	mod, ok := decls[0].(*ast.DeclareModuleDecl)
	require.True(t, ok, "decl is a DeclareModuleDecl")
	require.Len(t, mod.Decls, 2)
	require.Equal(t, "/** reads a file */", mod.Decls[0].Doc())
	require.Equal(t, "", mod.Decls[1].Doc())
}

func TestDeclDocs_InAScript(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	source := &ast.Source{Path: "input.esc", Contents: "/** the answer */\nval x = 42\n"}
	script, errors := NewParser(ctx, source).ParseScript()
	require.Empty(t, errors)
	require.Len(t, script.Stmts, 1)

	declStmt, ok := script.Stmts[0].(*ast.DeclStmt)
	require.True(t, ok, "stmt is a DeclStmt")
	require.Equal(t, "/** the answer */", declStmt.Decl.Doc())
}

func TestDeclDocs_ModuleFileDocAfterImports(t *testing.T) {
	t.Parallel()
	// The import section stops at the first comment that no import follows,
	// so the doc reaches the declaration below it rather than being consumed
	// as part of the imports.
	module, _ := parseSource(t, `import "std:fs"

/** the answer */
val x = 42
`)
	decls := namespacesOf(module)[0].Decls
	require.Len(t, decls, 1)
	require.Equal(t, "/** the answer */", decls[0].Doc())
}

func TestDeclDocs_TrailingCommentsAtEndOfFile(t *testing.T) {
	t.Parallel()
	// Comments with no declaration after them are still collected, they just
	// have nothing to attach to.
	module, source := parseSource(t, "val x = 1\n// nothing follows\n")
	decls := namespacesOf(module)[0].Decls
	require.Len(t, decls, 1)
	require.Equal(t, "", decls[0].Doc())
	require.Len(t, module.Comments[source.ID], 1)
}
