package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

func TestPreludeOperatorBindings(t *testing.T) {
	s := NewPrelude()
	tests := []struct {
		op   string
		want string
	}{
		{"+", "fn (a: number, b: number) -> number"},
		{"-", "fn (a: number, b: number) -> number"},
		{"*", "fn (a: number, b: number) -> number"},
		{"/", "fn (a: number, b: number) -> number"},
		{"<", "fn (a: number, b: number) -> boolean"},
		{">", "fn (a: number, b: number) -> boolean"},
		{"<=", "fn (a: number, b: number) -> boolean"},
		{">=", "fn (a: number, b: number) -> boolean"},
		{"==", "fn (a: unknown, b: unknown) -> boolean"},
		{"!=", "fn (a: unknown, b: unknown) -> boolean"},
		{"&&", "fn (a: boolean, b: boolean) -> boolean"},
		{"||", "fn (a: boolean, b: boolean) -> boolean"},
		{"!", "fn (a: boolean) -> boolean"},
		{"++", "fn (a: string, b: string) -> string"},
	}
	for _, tt := range tests {
		t.Run(tt.op, func(t *testing.T) {
			b, ok := s.GetValue(tt.op)
			require.True(t, ok, "operator %q should be bound in the prelude", tt.op)
			require.Equal(t, tt.want, renderBinding(b))
		})
	}
}

func TestPreludeStdlibTypePlaceholders(t *testing.T) {
	s := NewPrelude()
	for _, name := range []string{
		"Promise", "Iterable", "AsyncIterable",
		"Generator", "AsyncGenerator", "IteratorResult",
	} {
		t.Run(name, func(t *testing.T) {
			b, ok := s.GetType(name)
			require.True(t, ok, "stdlib type %q should resolve to a placeholder", name)
			require.IsType(t, &soltype.UnknownType{}, b.Type)
		})
	}
}

// A stdlib type name lives in the type sort, not the value sort: looking it up
// as a value must miss (so a value-position reference would error, not silently
// resolve to the placeholder).
func TestPreludeStdlibNamesAreTypesNotValues(t *testing.T) {
	s := NewPrelude()
	_, ok := s.GetValue("Promise")
	require.False(t, ok)
}
