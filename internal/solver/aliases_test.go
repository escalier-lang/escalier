package solver

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/require"
)

// TestInferTypeAliasBasic covers a non-generic `type` alias end to end (M7 PR1): the
// type binding renders under the alias name, an object literal that fits the aliased
// record type-checks and the binding renders under the alias name rather than the
// expanded body, and a primitive alias flows structurally.
func TestInferTypeAliasBasic(t *testing.T) {
	tests := []struct {
		name       string
		src        string
		wantValues map[string]string
		wantTypes  map[string]string
	}{
		{
			name:      "RecordAliasBinds",
			src:       `type Point = {x: number, y: number}`,
			wantTypes: map[string]string{"Point": "Point"},
		},
		{
			name: "AnnotatedValueRendersUnderAliasName",
			src: `
				type Point = {x: number, y: number}
				val p: Point = {x: 1, y: 2}
			`,
			wantValues: map[string]string{"p": "Point"},
			wantTypes:  map[string]string{"Point": "Point"},
		},
		{
			name: "PrimitiveAliasAcceptsMatchingValue",
			src: `
				type Foo = number
				val x: Foo = 5
			`,
			wantValues: map[string]string{"x": "Foo"},
			wantTypes:  map[string]string{"Foo": "Foo"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, types, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
			for name, want := range tt.wantValues {
				require.Equal(t, want, values[name], "value binding %q", name)
			}
			for name, want := range tt.wantTypes {
				require.Equal(t, want, types[name], "type binding %q", name)
			}
		})
	}
}

// TestInferTypeAliasRejectsMissingField checks that an alias is transparent under
// subtyping: an object literal missing a field the aliased record requires is rejected
// against the expanded body, with the full missing-property message (M7 PR1).
func TestInferTypeAliasRejectsMissingField(t *testing.T) {
	src := `
		type Point = {x: number, y: number}
		val p: Point = {x: 1}
	`
	_, _, errs := inferSource(t, src)
	require.Len(t, errs, 1)
	require.Equal(t, "object is missing property: y", errs[0].Message())
}

// TestInferTypeAliasRejectsMismatchedPrimitive checks that a primitive alias rejects a
// value of the wrong primitive, since the alias expands to its body at subtyping time.
func TestInferTypeAliasRejectsMismatchedPrimitive(t *testing.T) {
	src := `
		type Foo = number
		val x: Foo = "hi"
	`
	_, _, errs := inferSource(t, src)
	require.Len(t, errs, 1)
	require.Equal(t, `cannot constrain "hi" <: number`, errs[0].Message())
}

// TestInferTypeAliasMissingBodyDoesNotPanic guards the parser error-recovery case where
// `type Foo =` yields a TypeDecl with a nil TypeAnn. Inference runs despite parse errors
// in the real pipeline, so inferTypeDecl must bind the alias to a recovery type rather
// than route the nil annotation to reportUnsupported(nil), whose error has no span.
func TestInferTypeAliasMissingBodyDoesNotPanic(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	// Parse directly so the malformed source reaches inference; the standard harness
	// rejects parse errors, but the real compiler and LSP keep going on a partial AST.
	// `type Foo =` yields a TypeDecl with a nil TypeAnn that reaches inferTypeDecl.
	module, _ := parser.ParseLibFiles(ctx, []*ast.Source{
		{ID: 0, Path: "input.esc", Contents: `type Foo =`},
	})
	// InferModule only collects diagnostics; the nil-Node crash surfaces when a caller
	// renders one, so exercise Span() and Message() on every returned error the way the
	// CLI and LSP formatters do.
	require.NotPanics(t, func() {
		_, _, errs := InferModule(module)
		for _, e := range errs {
			_ = e.Span()
			_ = e.Message()
		}
	})
}
