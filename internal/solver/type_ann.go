package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// resolveTypeAnn converts an M2-supported type annotation into a soltype.Type.
// M2 needs only the primitive annotations that annotated params and return
// types use (number/string/boolean); everything richer — type references,
// generics, object/tuple/function annotations, unions — is represented by types
// later milestones add (M3/M4/M6) and resolves to an UnsupportedNodeError here.
// It takes no scope: the supported set is all closed primitives, with no name to
// look up. Name resolution against the type scope arrives with TypeRef support.
func (c *checker) resolveTypeAnn(ta ast.TypeAnn) soltype.Type {
	switch ta := ta.(type) {
	case *ast.NumberTypeAnn:
		return &soltype.PrimType{Prim: soltype.NumPrim}
	case *ast.StringTypeAnn:
		return &soltype.PrimType{Prim: soltype.StrPrim}
	case *ast.BooleanTypeAnn:
		return &soltype.PrimType{Prim: soltype.BoolPrim}
	default:
		return c.report(&UnsupportedNodeError{
			errSpan: errSpan{span: ta.Span()},
			Kind:    typeAnnKind(ta),
		})
	}
}

// typeAnnKind names a type-annotation node for the subset-guard error message.
func typeAnnKind(ta ast.TypeAnn) string { return astKind(ta) }
