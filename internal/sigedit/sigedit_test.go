package sigedit

import (
	"context"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"

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
