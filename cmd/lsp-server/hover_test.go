package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDropTypeBinder(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "NoBinder", input: "(x: number) -> C", expected: "(x: number) -> C"},
		{name: "PlainBinder", input: "<T, E>(error: E) -> Result<T, E>", expected: "(error: E) -> Result<T, E>"},
		{name: "NestedGenericBound", input: "<T: Array<number>>(a: T) -> C", expected: "(a: T) -> C"},
		{name: "FunctionTypedBound", input: "<T: fn (x: number) -> number>(a: T) -> C", expected: "(a: T) -> C"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, dropTypeBinder(tt.input))
		})
	}
}
