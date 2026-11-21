package dts_to_esc

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/stretchr/testify/require"
)

// rewriteRecovering runs rewrite the way the conversion entry points do, returning an
// unhandledNodeError panic as an error.
func rewriteRecovering(rewrite func()) (err error) {
	defer recoverUnhandledNode(&err)
	rewrite()
	return nil
}

// newDecl, newClassElem and newObjMember stand in for a node variant a later change
// could add to ast. Embedding the interface gives each the interface's unexported
// marker method, and no refRewriter switch lists them.
type newDecl struct{ ast.Decl }
type newClassElem struct{ ast.ClassElem }
type newObjMember struct{ ast.ObjTypeAnnElem }

// TestRefRewriterReportsAnUnhandledNode asserts that refRewriter reports a node variant
// its switches don't list as an error naming the construct. The conversion never
// produces a `function` keyword type, so FunctionTopTypeAnn stands in for a type
// annotation a later converter change could introduce.
func TestRefRewriterReportsAnUnhandledNode(t *testing.T) {
	tests := map[string]struct {
		rewrite func(r *refRewriter)
		wantErr string
	}{
		"a type annotation": {
			rewrite: func(r *refRewriter) { r.rewrite(ast.NewFunctionTopTypeAnn(ast.Span{})) },
			wantErr: "unsupported type annotation FunctionTopTypeAnn (refRewriter.rewrite has no case for it)",
		},
		"a type annotation inside a declaration": {
			rewrite: func(r *refRewriter) {
				decl := ast.NewTypeDecl(
					ast.NewIdentifier("F", ast.Span{}), nil, ast.NewFunctionTopTypeAnn(ast.Span{}),
					false, false, ast.Span{},
				)
				r.rewriteDecl(decl)
			},
			wantErr: "unsupported type annotation FunctionTopTypeAnn (refRewriter.rewrite has no case for it)",
		},
		"a declaration": {
			rewrite: func(r *refRewriter) { r.rewriteDecl(newDecl{}) },
			wantErr: "unsupported declaration dts_to_esc.newDecl (refRewriter.rewriteDecl has no case for it)",
		},
		"a class member": {
			rewrite: func(r *refRewriter) { r.rewriteClassElem(newClassElem{}) },
			wantErr: "unsupported class member dts_to_esc.newClassElem (refRewriter.rewriteClassElem has no case for it)",
		},
		"an object type member": {
			rewrite: func(r *refRewriter) {
				r.rewriteObject(ast.NewObjectTypeAnn([]ast.ObjTypeAnnElem{newObjMember{}}, ast.Span{}))
			},
			wantErr: "unsupported object type member dts_to_esc.newObjMember (refRewriter.rewriteObject has no case for it)",
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
