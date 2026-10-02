package tests

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	. "github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/require"
)

// TestADuplicateTypeParamIsReported covers a declaration that binds one type-parameter
// name twice.
//
// A reference to the name can only mean one of them, so the later binder is unreachable
// and a caller has no way to say which parameter an argument fills. The message matches
// internal/solver's, which TestADuplicateTypeParamIsReported there asserts.
func TestADuplicateTypeParamIsReported(t *testing.T) {
	const want = "type parameter `T` is declared more than once"

	tests := map[string]string{
		"AFunction":   `declare fn f<T, T>(a: T) -> T`,
		"AClass":      "class C<T, T> {\n\tv: T,\n}",
		"AnEnum":      "enum E<T, T> {\n\tA(v: T),\n}",
		"AnAlias":     `type A<T, T> = {v: T}`,
		"AnInterface": "interface I<T, T> {\n\tv: T,\n}",
		"AConstructor": "class C {\n\tv: number,\n" +
			"\tconstructor<T, T>(&mut self, a: T) {\n\t\tself.v = 1\n\t},\n}",
	}

	for name, src := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, []string{want}, messagesOf(inferErrors(t, src)))
		})
	}
}

// TestATypeParamDefaultCannotLookForward covers a default that names a parameter with no
// argument of its own by the time the default is filled in.
//
// A reference omitting a trailing argument fills it from that parameter's default,
// substituting the arguments before it, so a default can only name an earlier parameter.
// Both messages match internal/solver's.
func TestATypeParamDefaultCannotLookForward(t *testing.T) {
	tests := map[string]struct {
		input string
		want  []string
	}{
		"ALaterSibling": {
			input: `declare fn k<T = U, U = string>(a: T, b: U) -> T`,
			want:  []string{"the default for type parameter `T` cannot reference `U`, which is declared after it"},
		},
		// A default naming its own parameter is the same rule, since the parameter has no
		// argument until its own default is filled in.
		"ItsOwnParameter": {
			input: `type Loop<T = T> = {v: T}`,
			want:  []string{"the default for type parameter `T` cannot reference `T` itself"},
		},
		// The name is reported once per default, so a default naming one parameter twice
		// raises one error.
		"ANameReachedTwice": {
			input: `type Pair<T = Pair2<U, U>, U = string> = {a: T, b: U}
				type Pair2<A, B> = {a: A, b: B}`,
			want: []string{"the default for type parameter `T` cannot reference `U`, which is declared after it"},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, test.want, messagesOf(inferErrors(t, test.input)))
		})
	}
}

// TestADefaultNamingAnEarlierParamIsAccepted is the control for both checks: distinct
// names, and a default reaching a parameter declared before it.
func TestADefaultNamingAnEarlierParamIsAccepted(t *testing.T) {
	require.Empty(t, messagesOf(inferErrors(t, `declare fn ok<T, U = T>(a: T, b: U) -> T`)))
}

// inferErrors infers input as one library module and returns the errors it raised.
func inferErrors(t *testing.T, input string) []Error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	module, parseErrors := parser.ParseLibFiles(ctx,
		[]*ast.Source{{ID: 0, Path: "input.esc", Contents: input}})
	require.Empty(t, parseErrors, "expected no parse errors")

	c := NewChecker(ctx)
	_, errors := c.InferModule(Context{Scope: Prelude(c)}, module)
	return errors
}

// messagesOf renders each error's message, so a test asserts the full text.
func messagesOf(errs []Error) []string {
	out := make([]string, 0, len(errs))
	for _, e := range errs {
		out = append(out, e.Message())
	}
	return out
}
