package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestEncodeLike covers how encodeLike writes edited text back as a Go string literal of the
// original's kind. expected is "" when the text cannot be written.
func TestEncodeLike(t *testing.T) {
	tests := []struct {
		name     string
		original string
		text     string
		expected string
	}{
		{name: "Raw", original: "`fn f() {}`", text: "fn f() -> undefined {}", expected: "`fn f() -> undefined {}`"},
		{name: "RawCannotHoldBacktick", original: "`fn f() {}`", text: "fn f() -> `x` {}", expected: ""},
		{name: "Interpreted", original: `"fn f() {}"`, text: "fn f() -> number {}", expected: `"fn f() -> number {}"`},
		{name: "KeepsEscapes", original: `"fn f(\n) {}\t"`, text: "fn f(\n) -> number {}\t", expected: `"fn f(\n) -> number {}\t"`},
		{name: "QuotesInsertedText", original: `"fn f() {}"`, text: `fn f() -> "km" {}`, expected: `"fn f() -> \"km\" {}"`},
		{name: "KeepsMultiByteRune", original: `"fn é() {}"`, text: "fn é() -> 1 {}", expected: `"fn é() -> 1 {}"`},
		{name: "KeepsEscapedRune", original: `"fn f() { é }"`, text: "fn f() -> 1 { é }", expected: `"fn f() -> 1 { é }"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := encodeLike(tt.original, tt.text)
			if tt.expected == "" {
				require.False(t, ok)
				return
			}
			require.True(t, ok)
			require.Equal(t, tt.expected, got)
		})
	}
}

// TestUnresolvedType covers which rendered types unresolvedType treats as unresolved.
func TestUnresolvedType(t *testing.T) {
	tests := []struct {
		text     string
		expected bool
	}{
		{text: " -> error", expected: true},
		{text: " -> Generator<never, error, unknown>", expected: true},
		{text: " -> t4", expected: true},
		{text: " -> [t4, number]", expected: true},
		{text: " -> μX0.{next: X0}", expected: true},
		{text: " -> {t1: number}", expected: false},
		{text: " -> {t1?: number}", expected: false},
		{text: ` -> "fetch-error" | "syntax-error"`, expected: false},
		{text: " -> number", expected: false},
	}
	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			require.Equal(t, tt.expected, unresolvedType.MatchString(tt.text))
		})
	}
}
