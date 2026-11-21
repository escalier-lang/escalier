package dts_to_esc

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/stretchr/testify/require"
)

// rewriteRecovering runs rewrite the way ConvertModuleWithOverrides does, returning an
// unhandledNodeError panic as an error.
func rewriteRecovering(rewrite func()) (err error) {
	defer recoverUnhandledNode(&err)
	rewrite()
	return nil
}

// TestRefRewriterReportsAnUnhandledNode asserts that refRewriter reports a node variant
// its switches don't list as an error naming the construct. The conversion never
// produces a `function` keyword type, so FunctionTopTypeAnn stands in for a variant a
// later converter change could introduce.
func TestRefRewriterReportsAnUnhandledNode(t *testing.T) {
	tests := map[string]struct {
		rewrite func(r *refRewriter)
		wantErr string
	}{
		"a type annotation": {
			rewrite: func(r *refRewriter) { r.rewrite(ast.NewFunctionTopTypeAnn(ast.Span{})) },
			wantErr: "unsupported type annotation FunctionTopTypeAnn",
		},
		"a type annotation inside a declaration": {
			rewrite: func(r *refRewriter) {
				decl := ast.NewTypeDecl(
					ast.NewIdentifier("F", ast.Span{}), nil, ast.NewFunctionTopTypeAnn(ast.Span{}),
					false, false, ast.Span{},
				)
				r.rewriteDecl(decl)
			},
			wantErr: "unsupported type annotation FunctionTopTypeAnn",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			err := rewriteRecovering(func() { test.rewrite(&refRewriter{}) })
			require.EqualError(t, err, test.wantErr)
		})
	}
}

// TestRecoverUnhandledNodeReraisesOtherPanics asserts that a panic other than an
// unhandledNodeError passes through recoverUnhandledNode.
func TestRecoverUnhandledNodeReraisesOtherPanics(t *testing.T) {
	require.PanicsWithValue(t, "boom", func() {
		_ = rewriteRecovering(func() { panic("boom") })
	})
}
