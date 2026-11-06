package parser

import (
	"context"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/stretchr/testify/require"
)

// TestParseScriptImports covers where a script's imports may appear. The imports that
// open the script move into Script.Imports, and an import after a statement is
// reported and dropped.
func TestParseScriptImports(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		src     string
		imports []string
		stmts   int
		errs    []string
	}{
		{
			name:    "imports before the first statement",
			src:     "import \"std:console\"\nimport \"std:math\" as m\nval x = 1",
			imports: []string{"std:console", "std:math"},
			stmts:   1,
		},
		{
			name:  "no imports",
			src:   "val x = 1",
			stmts: 1,
		},
		{
			name:    "an import after a statement",
			src:     "import \"std:console\"\nval x = 1\nimport \"std:math\"",
			imports: []string{"std:console"},
			stmts:   1,
			errs:    []string{"An import must come before the script's first statement"},
		},
		{
			name:    "an import after a statement the parser could not read",
			src:     ")\nimport \"std:console\"\nval x = 1",
			imports: []string{"std:console"},
			stmts:   2,
			errs:    []string{"Unexpected token"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			source := &ast.Source{ID: 0, Path: "input.esc", Contents: tt.src}
			script, errs := NewParser(context.Background(), source).ParseScript()

			var messages []string
			for _, e := range errs {
				messages = append(messages, e.Message)
			}
			require.Equal(t, tt.errs, messages)

			var imports []string
			for _, imp := range script.Imports {
				imports = append(imports, imp.PackageName)
			}
			require.Equal(t, tt.imports, imports)
			require.Len(t, script.Stmts, tt.stmts)
		})
	}
}
