package sigedit

import (
	"context"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/escalier-lang/escalier/internal/solver"

	"github.com/stretchr/testify/require"
)

func TestSkipPast(t *testing.T) {
	tests := []struct {
		name     string
		contents string
		want     byte
		marks    string
		// expected is the offset skipPast returns, or -1 when it fails.
		expected int
	}{
		{name: "Immediate", contents: ")", want: ')', expected: 1},
		{name: "Whitespace", contents: " \t\n)", want: ')', expected: 4},
		{name: "Marks", contents: ", ...)", want: ')', marks: ",.", expected: 6},
		{name: "LineComment", contents: "// c\n)", want: ')', expected: 6},
		{name: "BlockComment", contents: "/* ) */)", want: ')', expected: 8},
		{name: "UnclosedBlockComment", contents: "/* )", want: ')', expected: -1},
		{name: "UnterminatedLineComment", contents: "// )", want: ')', expected: -1},
		{name: "OtherByte", contents: "x)", want: ')', expected: -1},
		{name: "End", contents: "  ", want: ')', expected: -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := skipPast(tt.contents, 0, tt.want, tt.marks)
			if tt.expected < 0 {
				require.False(t, ok)
				return
			}
			require.True(t, ok)
			require.Equal(t, tt.expected, got)
		})
	}
}

func TestApply(t *testing.T) {
	tests := []struct {
		name     string
		edits    []Edit
		expected string
	}{
		{name: "Replace", edits: []Edit{{Start: 0, End: 1, Text: "x"}}, expected: "xbc"},
		{name: "OutOfOrder", edits: []Edit{{Start: 2, End: 2, Text: "2"}, {Start: 0, End: 0, Text: "0"}}, expected: "0ab2c"},
		{name: "InsertionsAtOneOffsetKeepTheirOrder", edits: []Edit{{Start: 1, End: 1, Text: "x"}, {Start: 1, End: 1, Text: "y"}}, expected: "axybc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, Apply("abc", tt.edits))
		})
	}
}

// TestParamListEnd covers where paramListEnd finds the `)` that closes a function's
// parameter list. Each case parses src, then scans contents, which is src with bytes
// replaced to model text the parse did not read. expected is the offset paramListEnd
// returns, or -1 when it fails.
func TestParamListEnd(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		contents string
		expected int
	}{
		{name: "AfterLastParam", src: "fn f(x: number) {}", contents: "fn f(x: number) {}", expected: 15},
		{name: "NoParams", src: "fn f() {}", contents: "fn f() {}", expected: 6},
		{name: "NoParamsAfterBinders", src: "fn f<T>() {}", contents: "fn f<T>() {}", expected: 9},
		{name: "BinderListNotClosed", src: "fn f<T>() {}", contents: "fn f<T]() {}", expected: -1},
		{name: "ParamListNotOpened", src: "fn f() {}", contents: "fn f]) {}", expected: -1},
		{name: "ParamListNotClosed", src: "fn f(x: number) {}", contents: "fn f(x: number] {}", expected: -1},
		{name: "ParenthesizedLastParamType", src: "fn f(p: &({a: number} | {b: number})) {}", contents: "fn f(p: &({a: number} | {b: number})) {}", expected: 37},
		{name: "ParenInStringLiteralType", src: "fn f(p: \")\" | \"(\") {}", contents: "fn f(p: \")\" | \"(\") {}", expected: 18},
		{name: "ParenInComment", src: "fn f(p: number /* ) */) {}", contents: "fn f(p: number /* ) */) {}", expected: 23},
		{name: "ParenInLineComment", src: "fn f(\n\tp: number, // )\n) {}", contents: "fn f(\n\tp: number, // )\n) {}", expected: 24},
		{name: "LifetimesInParamTypes", src: "fn f(a: &'static Flat, b: &'static Flat) {}", contents: "fn f(a: &'static Flat, b: &'static Flat) {}", expected: 40},
		{name: "StringEndingInBackslash", src: "fn f(p: \"\\\", q: number) {}", contents: "fn f(p: \"\\\", q: number) {}", expected: 23},
		{name: "ParenInTemplateText", src: "fn f(p: `a(${string}`) {}", contents: "fn f(p: `a(${string}`) {}", expected: 22},
		{name: "NestedTemplateInHole", src: "fn f(p: `a${`(${number}`}b`) {}", contents: "fn f(p: `a${`(${number}`}b`) {}", expected: 28},
		{name: "FunctionTypeParam", src: "fn f(g: fn (x: number) -> number) {}", contents: "fn f(g: fn (x: number) -> number) {}", expected: 33},
		{name: "UnclosedStringLiteral", src: "fn f(x: number) {}", contents: "fn f(x: \"number) {}", expected: -1},
		{name: "UnclosedBlockCommentInParams", src: "fn f(x: number) {}", contents: "fn f(x: /*number) {}", expected: -1},
		{name: "UnclosedLineCommentInParams", src: "fn f(x: number) {}", contents: "fn f(x: //number) {}", expected: -1},
		{name: "UnclosedTemplateLiteral", src: "fn f(x: number) {}", contents: "fn f(x: `number) {}", expected: -1},
		{name: "UnclosedTemplateHole", src: "fn f(x: number) {}", contents: "fn f(x: `${number) {}", expected: -1},
		{name: "UnclosedStringInTemplateHole", src: "fn f(x: number) {}", contents: "fn f(x: `${\"n) {}", expected: -1},
		{name: "BracesInTemplateHole", src: "fn f(p: `a${{b: number}}(`) {}", contents: "fn f(p: `a${{b: number}}(`) {}", expected: 27},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decls, errs := parser.ParseDecls(context.Background(), &ast.Source{Path: "t.esc", Contents: tt.src})
			require.Empty(t, errs)
			require.Len(t, decls, 1)
			fn, ok := decls[0].(*ast.FuncDecl)
			require.True(t, ok)
			got, ok := paramListEnd(Target{Name: fn.Name, Sig: &fn.FuncSig}, tt.contents)
			if tt.expected < 0 {
				require.False(t, ok)
				return
			}
			require.True(t, ok)
			require.Equal(t, tt.expected, got)
		})
	}
}

// spanDiag is a diagnostic that blames span.
type spanDiag struct{ span ast.Span }

func (d spanDiag) Span() ast.Span { return d.span }

// TestWritesNeverWhileDiagnosed covers when edits that write `never` are held back for a
// diagnostic inside the declaration, which spans offsets 10 to 50.
func TestWritesNeverWhileDiagnosed(t *testing.T) {
	at := func(start, end int) ast.Span {
		return ast.Span{Start: ast.Location{Offset: start}, End: ast.Location{Offset: end}}
	}
	missing := &solver.MissingSignatureError{Node: ast.NewIdentifier("f", at(13, 14))}
	never := []Edit{{Start: 20, End: 20, Text: " -> never"}}
	tests := []struct {
		name     string
		edits    []Edit
		diags    []interface{ Span() ast.Span }
		expected bool
	}{
		{name: "NeverWithDiagnosticInside", edits: never, diags: []interface{ Span() ast.Span }{spanDiag{at(30, 35)}}, expected: true},
		{name: "OtherTypeWithDiagnosticInside", edits: []Edit{{Start: 20, End: 20, Text: " -> number"}}, diags: []interface{ Span() ast.Span }{spanDiag{at(30, 35)}}, expected: false},
		{name: "NeverWithDiagnosticOutside", edits: never, diags: []interface{ Span() ast.Span }{spanDiag{at(60, 65)}}, expected: false},
		{name: "NeverWithOnlyMissingSignature", edits: never, diags: []interface{ Span() ast.Span }{missing}, expected: false},
		{name: "NeverWithNoDiagnostic", edits: never, expected: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := Target{DeclSpan: at(10, 50)}
			require.Equal(t, tt.expected, WritesNeverWhileDiagnosed(target, tt.edits, tt.diags))
		})
	}
}

// TestTargetsSkipsBindingsWithoutSignatures asserts that Targets returns no target for a
// `val` that binds something other than a function expression, or a function expression
// info records no signature for.
func TestTargetsSkipsBindingsWithoutSignatures(t *testing.T) {
	src := "val n = 5\nval f = fn (x: number) -> number { return x }\nval g: fn () -> number = fn () { return 1 }"
	decls, errs := parser.ParseDecls(context.Background(), &ast.Source{Path: "t.esc", Contents: src})
	require.Empty(t, errs)
	require.Len(t, decls, 3)
	require.Empty(t, Targets(solver.NewInfo(), decls, 0))
}

// TestBinderOffset covers where binderOffset writes a function expression's binder list.
// Each case parses src as one `val` binding a function expression, then scans contents.
// expected is the offset binderOffset returns, or -1 when it fails.
func TestBinderOffset(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		contents string
		expected int
	}{
		{name: "AfterFnKeyword", src: "val f = fn (x: number) {}", contents: "val f = fn (x: number) {}", expected: 11},
		{name: "AfterAsyncFnKeyword", src: "val f = async fn () {}", contents: "val f = async fn () {}", expected: 17},
		{name: "NoFnKeyword", src: "val f = fn (x: number) {}", contents: "val f = gn (x: number) {}", expected: -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decls, errs := parser.ParseDecls(context.Background(), &ast.Source{Path: "t.esc", Contents: tt.src})
			require.Empty(t, errs)
			require.Len(t, decls, 1)
			d, ok := decls[0].(*ast.VarDecl)
			require.True(t, ok)
			fe, ok := d.Init.(*ast.FuncExpr)
			require.True(t, ok)
			got, ok := binderOffset(Target{Name: d.Pattern, Sig: &fe.FuncSig, Closure: fe}, tt.contents)
			if tt.expected < 0 {
				require.False(t, ok)
				return
			}
			require.True(t, ok)
			require.Equal(t, tt.expected, got)
		})
	}
}
