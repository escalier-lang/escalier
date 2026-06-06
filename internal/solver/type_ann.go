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
		return c.annPrim(ta, soltype.NumPrim)
	case *ast.StringTypeAnn:
		return c.annPrim(ta, soltype.StrPrim)
	case *ast.BooleanTypeAnn:
		return c.annPrim(ta, soltype.BoolPrim)
	default:
		return c.reportUnsupported(ta)
	}
}

// annPrim mints a FRESH PrimType for an annotation and records it against the
// annotation node (AnnotationType origin). The fresh-atom discipline (§3.3):
// there is no interned `number` singleton, so each annotation's atom is its own
// pointer and is directly recordable — which is what lets the `number` in
// `val x: number = "hi"` resolve to the annotation as the related blame node, and
// a prim/prim mismatch blame the offending annotation. Subtyping is unaffected:
// constrain compares PrimType.Prim by value, not pointer.
func (c *checker) annPrim(ta ast.TypeAnn, p soltype.Prim) soltype.Type {
	t := &soltype.PrimType{Prim: p}
	c.recordProv(t, ta, AnnotationType)
	return t
}
