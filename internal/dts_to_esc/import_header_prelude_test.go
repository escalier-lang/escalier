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

// TestImportGraphImportsWebPreludeOnlyOutsideTheWeb asserts that a `web:*` package
// naming a `web:prelude` export takes no import edge for it, while a `std:*` package
// naming one does, since only a `web:*` package's scope holds the web prelude.
func TestImportGraphImportsWebPreludeOnlyOutsideTheWeb(t *testing.T) {
	t.Parallel()

	prelude, _ := convertSlice(t, "declare class Event { readonly type: string; }")
	web, _ := convertSlice(t, "declare function fire(e: Event): void;")
	std, _ := convertSlice(t, "declare function dispatch(e: Event): void;")

	graph, err := ImportGraph(map[string]*StandaloneModule{
		"web:prelude": prelude,
		"web:dom":     web,
		"std:events":  std,
	})
	require.NoError(t, err)
	require.Empty(t, graph["web:dom"])
	require.Equal(t, []string{"web:prelude"}, graph["std:events"])
}
