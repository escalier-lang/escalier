package parser

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/stretchr/testify/require"
)

// TestParseMethodReceiverForms pins the six receiver spellings a method accepts and the
// receiver each one parses to. A borrow is written with `&`, and its lifetime precedes
// `mut` the way it does in a borrow type annotation. A consuming receiver writes no `&`.
func TestParseMethodReceiverForms(t *testing.T) {
	tests := map[string]struct {
		receiver string
		mode     ast.ReceiverMode
		mut      bool
		lifetime string
	}{
		"shared borrow":               {receiver: "&self", mode: ast.BorrowReceiver},
		"mutable borrow":              {receiver: "&mut self", mode: ast.BorrowReceiver, mut: true},
		"shared borrow with lifetime": {receiver: "&'a self", mode: ast.BorrowReceiver, lifetime: "a"},
		"mutable borrow with lifetime": {
			receiver: "&'a mut self", mode: ast.BorrowReceiver, mut: true, lifetime: "a",
		},
		"consuming":         {receiver: "self", mode: ast.ConsumeReceiver},
		"mutable consuming": {receiver: "mut self", mode: ast.ConsumeReceiver, mut: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			script, messages := parseReceiverScript(t, "class Foo { bar<'a>("+tc.receiver+", x: number) -> number { return x } }")
			require.Empty(t, messages)
			method := findFirstMethodInScript(script)
			require.NotNil(t, method)
			recv := method.Receiver
			require.NotNil(t, recv)
			require.Equal(t, tc.mode, recv.Mode)
			require.Equal(t, tc.mut, recv.Mut)
			if tc.lifetime == "" {
				require.Nil(t, recv.Lifetime)
			} else {
				lt, ok := recv.Lifetime.(*ast.LifetimeAnn)
				require.True(t, ok)
				require.Equal(t, tc.lifetime, lt.Name)
			}
			// The receiver stays off the value parameters.
			require.Len(t, method.Fn.Params, 1)
		})
	}
}

// TestParseReceiverDiagnostics pins the receivers the parser reports. A lifetime belongs on a
// borrow, so writing one on a consuming receiver names the borrow it meant. A constructor fills
// in the instance it is handed, so only `&mut self` is accepted there.
func TestParseReceiverDiagnostics(t *testing.T) {
	tests := map[string]struct {
		input   string
		wantErr string
	}{
		"lifetime on a consuming receiver": {
			input:   "class Foo { bar<'a>('a self) -> number { return 1 } }",
			wantErr: "a lifetime belongs on a borrowed receiver, so write `&'a self`",
		},
		"lifetime on a mutable consuming receiver": {
			input:   "class Foo { bar<'a>(mut 'a self) -> number { return 1 } }",
			wantErr: "a lifetime belongs on a borrowed receiver, so write `&'a mut self`",
		},
		"constructor with a shared borrow": {
			input:   "class Foo { constructor(&self) {} }",
			wantErr: "the `self` parameter of a constructor must be declared `&mut self`",
		},
		"constructor with a consuming receiver": {
			input:   "class Foo { constructor(self) {} }",
			wantErr: "the `self` parameter of a constructor must be declared `&mut self`",
		},
		"constructor with a mutable consuming receiver": {
			input:   "class Foo { constructor(mut self) {} }",
			wantErr: "the `self` parameter of a constructor must be declared `&mut self`",
		},
		"constructor without a receiver": {
			input:   "class Foo { constructor() {} }",
			wantErr: "constructors must declare `&mut self` as their first parameter",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, messages := parseReceiverScript(t, tc.input)
			require.Equal(t, []string{tc.wantErr}, messages)
		})
	}
}

// parseReceiverScript parses input as a script and returns it with its parse error messages.
func parseReceiverScript(t *testing.T, input string) (*ast.Script, []string) {
	t.Helper()
	source := &ast.Source{ID: 0, Path: "input.esc", Contents: input}
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	script, parseErrors := NewParser(ctx, source).ParseScript()
	require.NotNil(t, script)
	messages := make([]string, len(parseErrors))
	for i, pe := range parseErrors {
		messages[i] = pe.Message
	}
	return script, messages
}
