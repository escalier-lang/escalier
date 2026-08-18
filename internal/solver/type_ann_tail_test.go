package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// The surface syntax `A | ...R` resolves to a bounded tail and `A | B | ...` to an unbounded
// one, so a test can write the source directly rather than minting the union by hand.
func TestResolveUnionTailSyntax(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"a bounded tail after several members", `type Result = "a" | "b" | ...string`, `"a" | "b" | ...string`},
		{"a bounded tail after one member", `type Result = "a" | ...string`, `"a" | ...string`},
		{"a numeric bound", `type Result = 0 | ...number`, "0 | ...number"},
		{"a parenthesized union bound", `type Result = "a" | ...(number | string)`, `"a" | ...(number | string)`},
		{"an unbounded tail", `type Result = "a" | "b" | ...`, `"a" | "b" | ...`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, ctx, errs := inferTypeNodes(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, soltype.Print(expandAliasResidual(ctx, nodes["Result"])))
		})
	}
}
