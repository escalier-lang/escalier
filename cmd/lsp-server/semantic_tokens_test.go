package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/stretchr/testify/require"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// semanticTokensFor checks files, opens open, and returns its semantic tokens rendered as
// "line:col name type+modifiers", with line and col 1-based. It reads each token's text
// from open's contents, so the contents before a token on its line must be ASCII. It also
// returns the message of each parse or type error the check reported.
func semanticTokensFor(t *testing.T, files map[string]string, open string) ([]string, []string) {
	t.Helper()
	s := integrationServer(t, files)
	uri := openFile(t, s, open, files[open])
	result, err := s.textDocumentSemanticTokensFull(&glsp.Context{}, &protocol.SemanticTokensParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri},
	})
	require.NoError(t, err)
	var diagnostics []string
	for _, e := range s.checkOutput.ParseErrors {
		diagnostics = append(diagnostics, e.Message)
	}
	for _, e := range s.checkOutput.TypeErrors {
		diagnostics = append(diagnostics, e.Message())
	}
	return decodeSemanticTokens(t, files[open], result.Data), diagnostics
}

// decodeSemanticTokens renders data, the wire form of a file's tokens, as
// semanticTokensFor documents. src is the file's text.
func decodeSemanticTokens(t *testing.T, src string, data []protocol.UInteger) []string {
	t.Helper()
	require.Zero(t, len(data)%5, "token data holds five integers per token")
	lines := strings.Split(src, "\n")
	rendered := []string{}
	line, col := 0, 0
	for i := 0; i < len(data); i += 5 {
		if data[i] > 0 {
			col = 0
		}
		line += int(data[i])
		col += int(data[i+1])
		length := int(data[i+2])
		require.Less(t, line, len(lines))
		require.LessOrEqual(t, col+length, len(lines[line]))
		name := lines[line][col : col+length]
		kind := semanticTokenTypes[data[i+3]]
		for bit, modifier := range semanticTokenModifiers {
			if data[i+4]&(1<<bit) != 0 {
				kind += "+" + modifier
			}
		}
		rendered = append(rendered, fmt.Sprintf("%d:%d %s %s", line+1, col+1, name, kind))
	}
	return rendered
}

func TestSemanticTokensMarkMutableBindings(t *testing.T) {
	tests := []struct {
		name     string
		file     string
		src      string
		expected []string
	}{
		{
			name: "MutBindingAndReads",
			file: "bin/main.esc",
			src: `fn main() {
    val mut p = {x: 0}
    p.x = p.x + 1
    val total = p.x
}`,
			expected: []string{
				"2:13 p variable+mutable",
				"3:5 p variable+mutable",
				"3:11 p variable+mutable",
				"4:17 p variable+mutable",
			},
		},
		{
			name: "MutParam",
			file: "bin/main.esc",
			src: `fn reset(mut p: {x: number}) {
    p.x = 0
}`,
			expected: []string{
				"1:14 p parameter+mutable",
				"2:5 p parameter+mutable",
			},
		},
		{
			name: "MutableBorrowParam",
			file: "bin/main.esc",
			src: `fn reset(p: &mut {x: number}) {
    p.x = 0
}`,
			expected: []string{
				"1:10 p parameter+mutable",
				"2:5 p parameter+mutable",
			},
		},
		{
			name: "LocalHoldingMutableBorrow",
			file: "bin/main.esc",
			src: `fn main() {
    val mut p = {x: 0}
    val r = &mut p
    r.x = 1
}`,
			expected: []string{
				"2:13 p variable+mutable",
				"3:9 r variable+mutable",
				"3:18 p variable+mutable",
				"4:5 r variable+mutable",
			},
		},
		{
			// `var` makes a name reassignable, which leaves the value it holds immutable.
			name: "ImmutableBindingsHaveNoTokens",
			file: "bin/main.esc",
			src: `fn main() {
    val a = 1
    var b = a
    b = 2
}`,
			expected: []string{},
		},
		{
			name: "ImmutableBorrowParamHasNoToken",
			file: "bin/main.esc",
			src: `fn read(p: &{x: number}) -> number {
    return p.x
}`,
			expected: []string{},
		},
		{
			name: "ShorthandDestructuring",
			file: "bin/main.esc",
			src: `fn main() {
    val {mut x, y} = {x: {a: 1}, y: 2}
    x.a = y
}`,
			expected: []string{
				"2:14 x variable+mutable",
				"3:5 x variable+mutable",
			},
		},
		{
			name: "ShorthandPropertyRead",
			file: "bin/main.esc",
			src: `fn main() {
    val mut p = {x: 0}
    val o = {p}
}`,
			expected: []string{
				"2:13 p variable+mutable",
				"3:14 p variable+mutable",
			},
		},
		{
			name: "ShorthandParamHoldingMutableBorrow",
			file: "bin/main.esc",
			src: `fn reset({p}: {p: &mut {x: number}}) {
    p.x = 0
}`,
			expected: []string{
				"1:11 p parameter+mutable",
				"2:5 p parameter+mutable",
			},
		},
		{
			// A constructor's `&mut self` is a parameter whose pattern spans `&mut self`.
			name: "ConstructorReceiver",
			file: "bin/main.esc",
			src: `class Box {
    value: number,
    constructor(&mut self) { self.value = 0 },
}`,
			expected: []string{
				"3:5 constructor method+mutable",
				"3:22 self parameter+mutable",
				"3:30 self parameter+mutable",
			},
		},
		{
			name: "LocalNamedSelf",
			file: "bin/main.esc",
			src: `fn main() {
    val mut self = {x: 0}
    self.x = 1
}`,
			expected: []string{
				"2:13 self variable+mutable",
				"3:5 self variable+mutable",
			},
		},
		{
			name: "ModuleLevelBinding",
			file: "lib/index.esc",
			src: `val mut totals = {sum: 0}
export fn add(n: number) {
    totals.sum = totals.sum + n
}`,
			expected: []string{
				"1:9 totals variable+mutable",
				"3:5 totals variable+mutable",
				"3:18 totals variable+mutable",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens, diagnostics := semanticTokensFor(t, map[string]string{tt.file: tt.src}, tt.file)
			require.Empty(t, diagnostics)
			require.Equal(t, tt.expected, tokens)
		})
	}
}

// TestEncodeSemanticTokensCountsUTF16 asserts that a token's column and length count
// UTF-16 code units. "😀" is four bytes and two UTF-16 code units, and "é" is two bytes
// and one code unit.
func TestEncodeSemanticTokensCountsUTF16(t *testing.T) {
	src := "a = \"😀\"; bé\nc"
	lineMap := ast.NewLineMap(src)
	span := func(text string) ast.Span {
		start := strings.Index(src, text)
		return ast.Span{Start: ast.Location{Offset: start}, End: ast.Location{Offset: start + len(text)}}
	}
	tokens := []semanticToken{
		{span: span("c"), tokenType: tokenTypeVariable, modifiers: tokenModifierMutable},
		{span: span("bé"), tokenType: tokenTypeParameter, modifiers: tokenModifierMutable},
		{span: span("a"), tokenType: tokenTypeVariable, modifiers: 0},
	}
	require.Equal(t, []protocol.UInteger{
		0, 0, 1, tokenTypeVariable, 0,
		0, 10, 2, tokenTypeParameter, tokenModifierMutable,
		1, 0, 1, tokenTypeVariable, tokenModifierMutable,
	}, encodeSemanticTokens(lineMap, tokens))
}

func TestSemanticTokensMarkMutatingMethods(t *testing.T) {
	tests := []struct {
		name     string
		file     string
		src      string
		expected []string
	}{
		{
			// `constructor` and `bump`, the members that take `&mut self`, are marked, and
			// only where the class declares them. rust-analyzer leaves a call such as
			// `c.bump()` unmarked.
			// `take` moves the instance into the call and mutates its own binding, so the
			// caller's value is not mutated through it. The `self` of `&mut self` and of
			// `mut self` is marked in the receiver and in the body, and the `self` of
			// `&self` is not.
			name: "ClassMethods",
			file: "bin/main.esc",
			src: `class Counter {
    n: number,
    constructor(&mut self) { self.n = 0 },
    bump(&mut self) -> undefined { self.n = self.n + 1 },
    take(mut self) -> number {
        self.n = 0
        return self.n
    },
    read(&self) -> number { return self.n },
}
fn main() {
    val mut c = Counter()
    c.bump()
    val n = c.read()
    val m = c.take()
}`,
			expected: []string{
				"3:5 constructor method+mutable",
				"3:22 self parameter+mutable",
				"3:30 self parameter+mutable",
				"4:5 bump method+mutable",
				"4:15 self parameter+mutable",
				"4:36 self parameter+mutable",
				"4:45 self parameter+mutable",
				"5:14 self parameter+mutable",
				"6:9 self parameter+mutable",
				"7:16 self parameter+mutable",
				"12:13 c variable+mutable",
				"13:5 c variable+mutable",
				"14:13 c variable+mutable",
				"15:13 c variable+mutable",
			},
		},
		{
			// Calls, getter reads and setter writes are unmarked, whether through `self` or
			// through a `&mut` parameter. The declarations of `bump`, `twice` and both
			// halves of `value` are marked.
			name: "CallsOnSelfAndThroughBorrowAndMutGetter",
			file: "bin/main.esc",
			src: `class Counter {
    n: number,
    constructor(&mut self) { self.n = 0 },
    bump(&mut self) -> undefined { self.n = self.n + 1 },
    twice(&mut self) -> undefined {
        self.bump()
    },
    get value(&mut self) -> number { return self.n },
    set value(&mut self, v: number) { self.n = v },
}
fn f(c: &mut Counter) {
    c.bump()
    val v = c.value
    c.value = v
}`,
			expected: []string{
				"3:5 constructor method+mutable",
				"3:22 self parameter+mutable",
				"3:30 self parameter+mutable",
				"4:5 bump method+mutable",
				"4:15 self parameter+mutable",
				"4:36 self parameter+mutable",
				"4:45 self parameter+mutable",
				"5:5 twice method+mutable",
				"5:16 self parameter+mutable",
				"6:9 self parameter+mutable",
				"8:9 value property+mutable",
				"8:20 self parameter+mutable",
				"8:45 self parameter+mutable",
				"9:9 value property+mutable",
				"9:20 self parameter+mutable",
				"9:39 self parameter+mutable",
				"11:6 c parameter+mutable",
				"12:5 c parameter+mutable",
				"13:13 c parameter+mutable",
				"14:5 c parameter+mutable",
			},
		},
		{
			// A constructor takes `&mut self`, so its keyword is marked after a `private`.
			name: "PrivateConstructor",
			file: "bin/main.esc",
			src: `class Box {
    value: number,
    private constructor(&mut self) { self.value = 0 },
}`,
			expected: []string{
				"3:13 constructor method+mutable",
				"3:30 self parameter+mutable",
				"3:38 self parameter+mutable",
			},
		},
		{
			// `push` takes `&mut self`, and a call to it is unmarked, as rust-analyzer
			// leaves it. The receiver `xs` is marked as a mutable binding.
			name: "StdlibMethodCall",
			file: "bin/main.esc",
			src: `fn main() {
    val mut xs: Array<number> = []
    xs.push(1)
    val n = xs.indexOf(1)
}`,
			expected: []string{
				"2:13 xs variable+mutable",
				"3:5 xs variable+mutable",
				"4:13 xs variable+mutable",
			},
		},
		{
			// The `&mut self` method and setter are marked, and the `&self` method and
			// getter are not.
			name: "InterfaceMemberDeclarations",
			file: "lib/index.esc",
			src: `interface Stack {
    push(&mut self, n: number) -> undefined,
    peek(&self) -> number,
    get top(&self) -> number,
    set top(&mut self, n: number),
}`,
			expected: []string{
				"2:5 push method+mutable",
				"5:9 top property+mutable",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens, diagnostics := semanticTokensFor(t, map[string]string{tt.file: tt.src}, tt.file)
			require.Empty(t, diagnostics)
			require.Equal(t, tt.expected, tokens)
		})
	}
}
