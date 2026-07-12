package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// requireOrigin asserts the Prov table records ty as minted from node for reason
// kind — the FromAST leaf the M2.5 population sites write (§3.3).
func requireOrigin(t *testing.T, c *checker, ty soltype.Type, node ast.Node, kind ASTOriginKind) {
	t.Helper()
	o, ok := c.prov[ty]
	require.True(t, ok, "expected a Prov entry for the minted type")
	fa, ok := o.(FromAST)
	require.True(t, ok, "expected a FromAST origin")
	require.Same(t, node, fa.Node, "origin node")
	require.Equal(t, kind, fa.Kind, "origin kind")
}

// A literal mints a LitType recorded against its LiteralExpr.
func TestProvLiteralInference(t *testing.T) {
	c := newChecker()
	e := numExpr(5)
	ty := c.inferExpr(NewScope(), 0, e)
	requireOrigin(t, c, ty, e, LiteralInference)
}

// A tuple/object literal records its aggregate type against the literal node.
func TestProvTupleAndObject(t *testing.T) {
	c := newChecker()
	tup := tupleExpr(numExpr(1), strExpr("hi"))
	tupTy := c.inferExpr(NewScope(), 0, tup)
	requireOrigin(t, c, tupTy, tup, TupleElem)

	obj := objExpr(prop("a", numExpr(5)))
	objTy := c.inferExpr(NewScope(), 0, obj)
	requireOrigin(t, c, objTy, obj, ObjectField)
}

// A call records its fresh result var (Application) against the CallExpr and the
// synthesized call-shape (CallShape) against the same node.
func TestProvCallResultAndShape(t *testing.T) {
	c := newChecker()
	scope := NewScope()
	scope.defineValue("inc", ValueBinding{Schemes: []TypeScheme{monoScheme(&soltype.FuncType{
		Params: []*soltype.FuncParam{{Pattern: &soltype.IdentPat{Name: "n"}, Type: &soltype.PrimType{Prim: soltype.NumPrim}}},
		Ret:    &soltype.PrimType{Prim: soltype.NumPrim},
	})}})
	e := ast.NewCall(identExpr("inc"), []ast.Expr{numExpr(7)}, false, testSpan())

	res := c.inferExpr(scope, 0, e)
	requireOrigin(t, c, res, e, Application)

	// The call-shape FuncType{args, res} is not returned, so find it in the table:
	// the sole CallShape entry, recorded against the CallExpr.
	var shapeNode ast.Node
	var shapes int
	for _, o := range c.prov {
		if fa, ok := o.(FromAST); ok && fa.Kind == CallShape {
			shapeNode = fa.Node
			shapes++
		}
	}
	require.Equal(t, 1, shapes, "exactly one CallShape entry")
	require.Same(t, ast.Node(e), shapeNode)
}

// A member read records its fresh result var against the .prop IDENTIFIER, not the
// whole MemberExpr — so missing-property blame is the property, not the receiver.
func TestProvMemberAccessRecordedAgainstProp(t *testing.T) {
	c := newChecker()
	e := memberExpr(objExpr(prop("a", numExpr(5))), "a")
	res := c.inferExpr(NewScope(), 0, e)
	requireOrigin(t, c, res, e.Prop, MemberAccess)
}

// A function records its own FuncType (FuncInference) against the function node and
// each un-annotated param's fresh var (ParamBinding) against the param's pattern.
func TestProvFuncTypeAndParamBinding(t *testing.T) {
	c := newChecker()
	e := funcExpr([]*ast.Param{param("x", nil)}, nil, block(exprStmt(identExpr("x"))))
	ty := c.inferExpr(NewScope(), 0, e)
	requireOrigin(t, c, ty, e, FuncInference)

	ft, ok := ty.(*soltype.FuncType)
	require.True(t, ok)
	requireOrigin(t, c, ft.Params[0].Type, e.Params[0].Pattern, ParamBinding)
}

// An annotated param's fresh PrimType is recorded against its annotation
// (AnnotationType), and the ParamBinding origin is NOT recorded for it — its blame
// rides on the annotation (the fresh-atom discipline, §3.3).
func TestProvAnnotatedParamUsesAnnotationOrigin(t *testing.T) {
	c := newChecker()
	ann := numAnn()
	e := funcExpr([]*ast.Param{param("x", ann)}, nil, block(exprStmt(identExpr("x"))))
	ty := c.inferExpr(NewScope(), 0, e)

	ft := ty.(*soltype.FuncType)
	requireOrigin(t, c, ft.Params[0].Type, ann, AnnotationType)
}

// resolveTypeAnn mints a fresh PrimType per annotation and records it
// (AnnotationType) — the fresh-atom discipline that makes a shared `number` no
// longer a blind spot.
func TestProvAnnotationType(t *testing.T) {
	c := newChecker()
	ta := numAnn()
	ty, ok := c.resolveTypeAnn(NewScope(), ta, 0)
	require.True(t, ok)
	requireOrigin(t, c, ty, ta, AnnotationType)
}

// An identifier use records NOTHING (§3.3): recording against the binding's shared
// atom would overwrite the definition's origin. So inferring a bare ident over a
// pre-bound atom leaves the table empty.
func TestProvIdentRecordsNothing(t *testing.T) {
	c := newChecker()
	scope := NewScope()
	scope.defineValue("x", ValueBinding{Schemes: []TypeScheme{monoScheme(&soltype.PrimType{Prim: soltype.NumPrim})}})
	c.inferExpr(scope, 0, identExpr("x"))
	require.Empty(t, c.prov, "an ident use must record no provenance")
}

// The honest absence (§3.2): a synthesized (coalesced) type has no Prov entry, so
// NodeFor reports a miss rather than lying.
func TestProvCoalescedTypeHasNoEntry(t *testing.T) {
	c := newChecker()
	v := c.freshAt(0)
	co := coalesce(v, soltype.Positive) // empty bounds → never, a fresh synthesized atom
	_, ok := c.prov.NodeFor(co)
	require.False(t, ok, "a coalesced type must have no provenance entry")
}

// The debugProv guard (finding #5) enforces the unique-pointer invariant: recording
// the SAME type pointer against a DIFFERENT node panics (catching a future
// interned/coalesced-pointer reuse that would silently mis-blame), while
// re-recording against the same node is idempotent and allowed.
func TestProvDebugGuardCatchesConflictingOverwrite(t *testing.T) {
	c := newChecker()
	c.debugProv = true
	ty := &soltype.PrimType{Prim: soltype.NumPrim}
	nodeA := numAnn()
	nodeB := strAnn()

	c.recordProv(ty, nodeA, AnnotationType)
	require.NotPanics(t, func() { c.recordProv(ty, nodeA, AnnotationType) }, "re-recording the same node is idempotent")
	require.Panics(t, func() { c.recordProv(ty, nodeB, AnnotationType) }, "re-recording a different node violates the invariant")
}

// With the guard OFF (production default), a conflicting overwrite does not panic —
// a span bug must never crash the compiler (it degrades to last-write-wins blame).
func TestProvDebugGuardOffDoesNotPanic(t *testing.T) {
	c := newChecker() // debugProv defaults false
	ty := &soltype.PrimType{Prim: soltype.NumPrim}
	c.recordProv(ty, numAnn(), AnnotationType)
	require.NotPanics(t, func() { c.recordProv(ty, strAnn(), AnnotationType) })
}
