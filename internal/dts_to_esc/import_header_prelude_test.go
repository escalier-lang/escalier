package dts_to_esc

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestReadsPreludeBare covers which packages reach a prelude's exports with no
// import and no qualifier.
func TestReadsPreludeBare(t *testing.T) {
	t.Parallel()

	tests := []struct {
		importer, prelude string
		want              bool
	}{
		{"std:math", "std:prelude", true},
		{"web:dom", "std:prelude", true},
		{"web:dom", "web:prelude", true},
		{"web:prelude", "web:prelude", false},
		{"std:math", "web:prelude", false},
		{"web:dom", "web:fetch", false},
	}
	for _, tt := range tests {
		require.Equal(t, tt.want, ReadsPreludeBare(tt.importer, tt.prelude), "%s reading %s", tt.importer, tt.prelude)
	}
}

// TestStdPackageCannotNameWebPrelude asserts that a `web:*` package naming a
// `web:prelude` export takes no import edge for it, and that a `std:*` package
// naming one fails the tier check. Every `std:*` package is in the language
// tier, which sits below `web:prelude`.
func TestStdPackageCannotNameWebPrelude(t *testing.T) {
	t.Parallel()

	prelude, _ := convertSlice(t, "declare class Event { readonly type: string; }")
	web, _ := convertSlice(t, "declare function fire(e: Event): void;")
	std, _ := convertSlice(t, "declare function dispatch(e: Event): void;")
	mods := map[string]*StandaloneModule{
		"web:prelude": prelude,
		"web:dom":     web,
		"std:object":  std,
	}

	graph, err := ImportGraph(mods)
	require.NoError(t, err)
	require.Empty(t, graph["web:dom"])

	violations, err := CheckTiers(mods, graph)
	require.NoError(t, err)
	require.Len(t, violations, 1)
	require.Equal(t,
		"std:object (language tier) imports web:prelude (core tier) for Event",
		violations[0].String())
}
