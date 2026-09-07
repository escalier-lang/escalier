package ast

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The name a bare import binds, one case per shape a specifier takes. An npm
// package name is not an identifier in general, so the derivation has to answer
// for every character one cannot hold.
func TestDeriveImportName(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		specifier string
		want      string
	}{
		"APlainName":       {specifier: "lodash", want: "lodash"},
		"APath":            {specifier: "lodash/fp", want: "fp"},
		"AScheme":          {specifier: "std:math", want: "math"},
		"ASchemeAndAPath":  {specifier: "std:collections/map", want: "map"},
		"AnUnderscore":     {specifier: "std:typed_arrays", want: "typed_arrays"},
		"AScopedPackage":   {specifier: "@types/node", want: "node"},
		"AHyphen":          {specifier: "fast-deep-equal", want: "fast_deep_equal"},
		"ATrailingDigit":   {specifier: "package-1", want: "package_1"},
		"ADot":             {specifier: "socket.io", want: "socket_io"},
		"ALeadingDigit":    {specifier: "3d-vectors", want: "_3d_vectors"},
		"AnEmptySpecifier": {specifier: "", want: ""},
		"AnEmptyPathTail":  {specifier: "pkg/", want: ""},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, test.want, DeriveImportName(test.specifier))
		})
	}
}

// An alias is what the import binds under, and the specifier is consulted only
// when none is written.
func TestImportStmtLocalName(t *testing.T) {
	t.Parallel()

	derived := NewImportStmt("fast-deep-equal", "", nil, Span{})
	require.Equal(t, "fast_deep_equal", derived.LocalName())

	aliased := NewImportStmt("fast-deep-equal", "fde", nil, Span{})
	require.Equal(t, "fde", aliased.LocalName())
}
