package interop

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/btree"
)

// These tests exercise rewriteReadonlyTwinRefs directly against a
// hand-built StandaloneModule so every switch branch in twin_rewrite.go
// is hit at least once. The integration test
// TestConvertBucket_ReadonlyTwinRewritesRefs covers the .d.ts → output
// path; this file covers the long tail of AST shapes that .d.ts source
// rarely produces.

var twinSpan = ast.Span{}

// twins is the fixture used by every test below: rewriting renames
// `RO` → `Mut` at TypeRef sites and wraps bare `Mut<…>` in
// MutableTypeAnn.
func twinsFixture() []readonlyTwin {
	return []readonlyTwin{{mutableName: "Mut", readonlyName: "RO"}}
}

func roRef() *ast.TypeRefTypeAnn {
	return ast.NewRefTypeAnn(ast.NewIdentifier("RO", twinSpan), nil, twinSpan)
}

func mutRef() *ast.TypeRefTypeAnn {
	return ast.NewRefTypeAnn(ast.NewIdentifier("Mut", twinSpan), nil, twinSpan)
}

func otherRef(name string) *ast.TypeRefTypeAnn {
	return ast.NewRefTypeAnn(ast.NewIdentifier(name, twinSpan), nil, twinSpan)
}

// runRewrite wraps decls in a minimal StandaloneModule (single root
// namespace) and runs the twin rewriter against the twins fixture.
func runRewrite(decls []ast.Decl) {
	var nss btree.Map[string, *ast.Namespace]
	nss.Set("", &ast.Namespace{Decls: decls})
	mod := &StandaloneModule{Module: ast.NewModule(nss)}
	rewriteReadonlyTwinRefs(mod, twinsFixture())
}

// rewriteOne wraps the supplied TypeAnn in a single TypeDecl, runs the
// rewriter, and returns the (possibly replaced) rewritten TypeAnn so
// individual shapes can be asserted on.
func rewriteOne(ann ast.TypeAnn) ast.TypeAnn {
	td := ast.NewTypeDecl(ast.NewIdentifier("X", twinSpan), nil, ann, false, true, twinSpan)
	runRewrite([]ast.Decl{td})
	return td.TypeAnn
}

// requireMutWrappingArray asserts that ann is `MutableTypeAnn{Mut}`.
func requireMutWrappingMut(t *testing.T, ann ast.TypeAnn, msg string) {
	t.Helper()
	m, ok := ann.(*ast.MutableTypeAnn)
	require.True(t, ok, "%s: expected MutableTypeAnn, got %T", msg, ann)
	ref, ok := m.Target.(*ast.TypeRefTypeAnn)
	require.True(t, ok, "%s: expected MutableTypeAnn wrapping TypeRef, got %T", msg, m.Target)
	require.Equal(t, "Mut", ast.QualIdentToString(ref.Name), "%s: wrapped ref name", msg)
}

// requireRenamedToMut asserts that ann is a bare TypeRef named `Mut`
// (i.e. a renamed `RO` reference, no MutableTypeAnn wrapper).
func requireRenamedToMut(t *testing.T, ann ast.TypeAnn, msg string) {
	t.Helper()
	ref, ok := ann.(*ast.TypeRefTypeAnn)
	require.True(t, ok, "%s: expected TypeRef, got %T", msg, ann)
	require.Equal(t, "Mut", ast.QualIdentToString(ref.Name), "%s: ref name", msg)
}

// TestTwinRewriter_TypeAnnShapes exercises every TypeAnn variant that
// has child slots so each branch of (*twinRewriter).rewrite fires. The
// pattern is the same for every shape: place an `RO` and a `Mut` ref
// in every child slot, then assert that after rewrite the `RO` was
// renamed to a bare `Mut` ref and the `Mut` was wrapped in
// MutableTypeAnn.
func TestTwinRewriter_TypeAnnShapes(t *testing.T) {
	t.Parallel()

	t.Run("TupleTypeAnn", func(t *testing.T) {
		out := rewriteOne(ast.NewTupleTypeAnn([]ast.TypeAnn{roRef(), mutRef()}, twinSpan))
		tup := out.(*ast.TupleTypeAnn)
		requireRenamedToMut(t, tup.Elems[0], "tuple[0]")
		requireMutWrappingMut(t, tup.Elems[1], "tuple[1]")
	})

	t.Run("UnionTypeAnn", func(t *testing.T) {
		out := rewriteOne(ast.NewUnionTypeAnn([]ast.TypeAnn{roRef(), mutRef()}, twinSpan))
		u := out.(*ast.UnionTypeAnn)
		requireRenamedToMut(t, u.Types[0], "union[0]")
		requireMutWrappingMut(t, u.Types[1], "union[1]")
	})

	t.Run("IntersectionTypeAnn", func(t *testing.T) {
		out := rewriteOne(ast.NewIntersectionTypeAnn([]ast.TypeAnn{roRef(), mutRef()}, twinSpan))
		i := out.(*ast.IntersectionTypeAnn)
		requireRenamedToMut(t, i.Types[0], "inter[0]")
		requireMutWrappingMut(t, i.Types[1], "inter[1]")
	})

	t.Run("KeyOfTypeAnn", func(t *testing.T) {
		out := rewriteOne(ast.NewKeyOfTypeAnn(mutRef(), twinSpan))
		k := out.(*ast.KeyOfTypeAnn)
		requireMutWrappingMut(t, k.Type, "keyof.Type")
	})

	t.Run("IndexTypeAnn", func(t *testing.T) {
		out := rewriteOne(ast.NewIndexTypeAnn(mutRef(), roRef(), twinSpan))
		ix := out.(*ast.IndexTypeAnn)
		requireMutWrappingMut(t, ix.Target, "index.Target")
		requireRenamedToMut(t, ix.Index, "index.Index")
	})

	t.Run("CondTypeAnn", func(t *testing.T) {
		out := rewriteOne(ast.NewCondTypeAnn(mutRef(), roRef(), mutRef(), roRef(), twinSpan))
		c := out.(*ast.CondTypeAnn)
		requireMutWrappingMut(t, c.Check, "cond.Check")
		requireRenamedToMut(t, c.Extends, "cond.Extends")
		requireMutWrappingMut(t, c.Then, "cond.Then")
		requireRenamedToMut(t, c.Else, "cond.Else")
	})

	t.Run("MatchTypeAnn", func(t *testing.T) {
		out := rewriteOne(ast.NewMatchTypeAnn(mutRef(), []*ast.MatchTypeAnnCase{
			{Extends: roRef(), Cons: mutRef()},
		}, twinSpan))
		m := out.(*ast.MatchTypeAnn)
		requireMutWrappingMut(t, m.Target, "match.Target")
		requireRenamedToMut(t, m.Cases[0].Extends, "match.case.Extends")
		requireMutWrappingMut(t, m.Cases[0].Cons, "match.case.Cons")
	})

	t.Run("TemplateLitTypeAnn", func(t *testing.T) {
		out := rewriteOne(ast.NewTemplateLitTypeAnn(nil, []ast.TypeAnn{roRef(), mutRef()}, twinSpan))
		tl := out.(*ast.TemplateLitTypeAnn)
		requireRenamedToMut(t, tl.TypeAnns[0], "template[0]")
		requireMutWrappingMut(t, tl.TypeAnns[1], "template[1]")
	})

	t.Run("ImportTypeAnn", func(t *testing.T) {
		out := rewriteOne(ast.NewImportType("foo", ast.NewIdentifier("X", twinSpan),
			[]ast.TypeAnn{roRef(), mutRef()}, twinSpan))
		im := out.(*ast.ImportTypeAnn)
		requireRenamedToMut(t, im.TypeArgs[0], "import[0]")
		requireMutWrappingMut(t, im.TypeArgs[1], "import[1]")
	})

	t.Run("MutableTypeAnn", func(t *testing.T) {
		// `mut RO<…>` should have its inner `RO` renamed to `Mut`; the
		// outer wrapper is left alone (no double-wrap).
		out := rewriteOne(ast.NewMutableTypeAnn(roRef(), twinSpan))
		m := out.(*ast.MutableTypeAnn)
		requireRenamedToMut(t, m.Target, "mutable.Target")
	})

	t.Run("RestSpreadTypeAnn", func(t *testing.T) {
		out := rewriteOne(ast.NewRestSpreadTypeAnn(mutRef(), twinSpan))
		rs := out.(*ast.RestSpreadTypeAnn)
		requireMutWrappingMut(t, rs.Value, "rest.Value")
	})

	t.Run("FuncTypeAnn", func(t *testing.T) {
		// FuncTypeAnn (the type-position function type) is distinct from
		// FuncSig (decl-position). Place RO/Mut in every slot.
		param := &ast.Param{
			Pattern: ast.NewIdentPat("x", false, nil, nil, twinSpan),
			TypeAnn: roRef(),
		}
		fn := ast.NewFuncTypeAnn(nil, []*ast.TypeParam{{
			Name:       "T",
			Constraint: mutRef(),
			Default:    roRef(),
		}}, []*ast.Param{param}, mutRef(), roRef(), twinSpan)

		out := rewriteOne(fn)
		f := out.(*ast.FuncTypeAnn)
		requireMutWrappingMut(t, f.TypeParams[0].Constraint, "func.TP.Constraint")
		requireRenamedToMut(t, f.TypeParams[0].Default, "func.TP.Default")
		requireRenamedToMut(t, f.Params[0].TypeAnn, "func.Param")
		requireMutWrappingMut(t, f.Return, "func.Return")
		requireRenamedToMut(t, f.Throws, "func.Throws")
	})

	t.Run("TypeRefTypeAnn rename non-ident name is no-op", func(t *testing.T) {
		// QualIdent that isn't a plain *Ident must be left alone — twin
		// names live in the unqualified namespace.
		qual := &ast.Member{
			Left:  ast.NewIdentifier("ns", twinSpan),
			Right: ast.NewIdentifier("RO", twinSpan),
		}
		out := rewriteOne(ast.NewRefTypeAnn(qual, nil, twinSpan))
		ref := out.(*ast.TypeRefTypeAnn)
		_, isMut := out.(*ast.MutableTypeAnn)
		require.False(t, isMut, "qualified ref must not be wrapped")
		require.Same(t, qual, ref.Name, "qualified ref name preserved verbatim")
	})

	t.Run("non-twin TypeRef untouched", func(t *testing.T) {
		// Names that aren't in either twin set must survive unchanged.
		out := rewriteOne(otherRef("Other"))
		ref := out.(*ast.TypeRefTypeAnn)
		require.Equal(t, "Other", ast.QualIdentToString(ref.Name))
	})

	t.Run("leaf TypeAnns survive default case", func(t *testing.T) {
		// Each leaf variant must hit the explicit leaf list, not panic
		// at the rewrite() default.
		leaves := []ast.TypeAnn{
			ast.NewNumberTypeAnn(twinSpan),
			ast.NewStringTypeAnn(twinSpan),
			ast.NewBooleanTypeAnn(twinSpan),
			ast.NewSymbolTypeAnn(twinSpan),
			ast.NewBigintTypeAnn(twinSpan),
			ast.NewAnyTypeAnn(twinSpan),
			ast.NewUnknownTypeAnn(twinSpan),
			ast.NewNeverTypeAnn(twinSpan),
			ast.NewVoidTypeAnn(twinSpan),
			ast.NewTypeOfTypeAnn(ast.NewIdentifier("RO", twinSpan), twinSpan),
			ast.NewInferTypeAnn("T", twinSpan),
			ast.NewWildcardTypeAnn(twinSpan),
			ast.NewIntrinsicTypeAnn(twinSpan),
		}
		for _, leaf := range leaves {
			out := rewriteOne(leaf)
			require.Same(t, leaf, out, "leaf %T should be returned unchanged", leaf)
		}
	})
}

// TestTwinRewriter_ObjectTypeAnnElems exercises every ObjTypeAnnElem
// variant so each branch of (*twinRewriter).rewriteObject fires.
func TestTwinRewriter_ObjectTypeAnnElems(t *testing.T) {
	t.Parallel()
	mkFn := func() *ast.FuncTypeAnn {
		return ast.NewFuncTypeAnn(nil, nil,
			[]*ast.Param{{
				Pattern: ast.NewIdentPat("x", false, nil, nil, twinSpan),
				TypeAnn: roRef(),
			}}, mutRef(), nil, twinSpan)
	}
	mkKey := func(s string) ast.ObjKey {
		return ast.NewIdent(s, twinSpan)
	}

	obj := ast.NewObjectTypeAnn([]ast.ObjTypeAnnElem{
		&ast.CallableTypeAnn{Fn: mkFn()},
		&ast.ConstructorTypeAnn{Fn: mkFn()},
		&ast.MethodTypeAnn{Name: mkKey("m"), Fn: mkFn()},
		&ast.GetterTypeAnn{Name: mkKey("g"), Fn: mkFn()},
		&ast.SetterTypeAnn{Name: mkKey("s"), Fn: mkFn()},
		&ast.PropertyTypeAnn{Name: mkKey("p"), Value: roRef()},
		&ast.MappedTypeAnn{
			TypeParam: &ast.IndexParamTypeAnn{Name: "K", Constraint: mutRef()},
			Name:      roRef(),
			Value:     mutRef(),
			Check:     roRef(),
			Extends:   mutRef(),
		},
		ast.NewRestSpreadTypeAnn(roRef(), twinSpan),
	}, twinSpan)

	out := rewriteOne(obj).(*ast.ObjectTypeAnn)

	// Helper to pull and assert on the inner FuncTypeAnn shape used by
	// the first five elems.
	assertFn := func(fn *ast.FuncTypeAnn, label string) {
		requireRenamedToMut(t, fn.Params[0].TypeAnn, label+".param")
		requireMutWrappingMut(t, fn.Return, label+".return")
	}

	assertFn(out.Elems[0].(*ast.CallableTypeAnn).Fn, "callable")
	assertFn(out.Elems[1].(*ast.ConstructorTypeAnn).Fn, "constructor")
	assertFn(out.Elems[2].(*ast.MethodTypeAnn).Fn, "method")
	assertFn(out.Elems[3].(*ast.GetterTypeAnn).Fn, "getter")
	assertFn(out.Elems[4].(*ast.SetterTypeAnn).Fn, "setter")

	prop := out.Elems[5].(*ast.PropertyTypeAnn)
	requireRenamedToMut(t, prop.Value, "property.Value")

	m := out.Elems[6].(*ast.MappedTypeAnn)
	requireMutWrappingMut(t, m.TypeParam.Constraint, "mapped.TypeParam.Constraint")
	requireRenamedToMut(t, m.Name, "mapped.Name")
	requireMutWrappingMut(t, m.Value, "mapped.Value")
	requireRenamedToMut(t, m.Check, "mapped.Check")
	requireMutWrappingMut(t, m.Extends, "mapped.Extends")

	rest := out.Elems[7].(*ast.RestSpreadTypeAnn)
	requireRenamedToMut(t, rest.Value, "object-rest.Value")
}

// TestTwinRewriter_ClassElems exercises every ClassElem variant so each
// branch of (*twinRewriter).rewriteClassElem fires. The integration
// test only covers MethodElem.
func TestTwinRewriter_ClassElems(t *testing.T) {
	t.Parallel()
	mkFn := func() *ast.FuncExpr {
		return ast.NewFuncExpr(nil, nil,
			[]*ast.Param{{
				Pattern: ast.NewIdentPat("x", false, nil, nil, twinSpan),
				TypeAnn: roRef(),
			}}, mutRef(), nil, false, nil, twinSpan)
	}
	mkKey := func(s string) ast.ObjKey { return ast.NewIdent(s, twinSpan) }

	cd := ast.NewClassDecl(
		ast.NewIdentifier("C", twinSpan), nil, nil, nil, nil,
		[]ast.ClassElem{
			&ast.FieldElem{Name: mkKey("f"), Type: roRef(), Span_: twinSpan},
			&ast.MethodElem{Name: mkKey("m"), Fn: mkFn(), Span_: twinSpan},
			&ast.GetterElem{Name: mkKey("g"), Fn: mkFn(), Span_: twinSpan},
			&ast.SetterElem{Name: mkKey("s"), Fn: mkFn(), Span_: twinSpan},
			&ast.ConstructorElem{Fn: mkFn(), Span_: twinSpan},
		},
		false, true, twinSpan,
	)
	runRewrite([]ast.Decl{cd})

	requireRenamedToMut(t, cd.Body[0].(*ast.FieldElem).Type, "FieldElem.Type")

	assertFnExpr := func(fn *ast.FuncExpr, label string) {
		requireRenamedToMut(t, fn.Params[0].TypeAnn, label+".param")
		requireMutWrappingMut(t, fn.Return, label+".return")
	}
	assertFnExpr(cd.Body[1].(*ast.MethodElem).Fn, "MethodElem")
	assertFnExpr(cd.Body[2].(*ast.GetterElem).Fn, "GetterElem")
	assertFnExpr(cd.Body[3].(*ast.SetterElem).Fn, "SetterElem")
	assertFnExpr(cd.Body[4].(*ast.ConstructorElem).Fn, "ConstructorElem")
}

// TestTwinRewriter_DeclVariants exercises every Decl variant so each
// branch of (*twinRewriter).rewriteDecl fires.
func TestTwinRewriter_DeclVariants(t *testing.T) {
	t.Parallel()

	// VarDecl with type annotation.
	vd := ast.NewVarDecl(ast.ValKind,
		ast.NewIdentPat("v", false, nil, nil, twinSpan),
		roRef(), nil, false, true, twinSpan)

	// FuncDecl exercises rewriteFuncSig.
	fd := ast.NewFuncDecl(
		ast.NewIdentifier("f", twinSpan), nil, nil,
		[]*ast.Param{{
			Pattern: ast.NewIdentPat("p", false, nil, nil, twinSpan),
			TypeAnn: roRef(),
		}},
		mutRef(), roRef(), nil, false, true, false, twinSpan,
	)

	// TypeDecl with both an RHS and a TypeParam constraint.
	td := ast.NewTypeDecl(ast.NewIdentifier("T", twinSpan),
		[]*ast.TypeParam{{Name: "X", Constraint: roRef()}},
		mutRef(), false, true, twinSpan)

	// InterfaceDecl with Extends (rename canary) + body.
	id := ast.NewInterfaceDecl(
		ast.NewIdentifier("I", twinSpan), nil, nil,
		[]*ast.TypeRefTypeAnn{roRef()},
		ast.NewObjectTypeAnn([]ast.ObjTypeAnnElem{
			&ast.PropertyTypeAnn{Name: ast.NewIdent("p", twinSpan), Value: roRef()},
		}, twinSpan),
		false, true, twinSpan,
	)

	// ClassDecl with Extends + Implements (both renamed) + TypeParam default.
	cd := ast.NewClassDecl(
		ast.NewIdentifier("C", twinSpan), nil,
		[]*ast.TypeParam{{Name: "T", Default: roRef()}},
		roRef(),                          // Extends — must be renamed
		[]*ast.TypeRefTypeAnn{roRef()},   // Implements — must be renamed
		nil, false, true, twinSpan,
	)

	// EnumDecl with TypeParam constraint.
	ed := ast.NewEnumDecl(ast.NewIdentifier("E", twinSpan),
		[]*ast.TypeParam{{Name: "T", Constraint: roRef()}},
		nil, false, true, twinSpan)

	// ExportAssignmentStmt — no rewritable slots, must not panic.
	eas := ast.NewExportAssignmentStmt(ast.NewIdentifier("X", twinSpan), true, twinSpan)

	// NamespaceDecl, DeclareModuleDecl, DeclareGlobalDecl — each wraps
	// an inner VarDecl with an RO ref. After rewrite the inner ref
	// should be renamed.
	innerVarFor := func() *ast.VarDecl {
		return ast.NewVarDecl(ast.ValKind,
			ast.NewIdentPat("x", false, nil, nil, twinSpan),
			roRef(), nil, false, true, twinSpan)
	}
	innerNs := innerVarFor()
	innerMod := innerVarFor()
	innerGlobal := innerVarFor()

	nsd := ast.NewNamespaceDecl(ast.NewIdentifier("NS", twinSpan),
		[]ast.Decl{innerNs}, false, false, twinSpan)
	dmd := ast.NewDeclareModuleDecl(&ast.StrLit{Value: "m"},
		[]ast.Decl{innerMod}, false, twinSpan)
	dgd := ast.NewDeclareGlobalDecl([]ast.Decl{innerGlobal}, false, twinSpan)

	runRewrite([]ast.Decl{vd, fd, td, id, cd, ed, eas, nsd, dmd, dgd})

	requireRenamedToMut(t, vd.TypeAnn, "VarDecl.TypeAnn")

	requireRenamedToMut(t, fd.Params[0].TypeAnn, "FuncDecl.Param")
	requireMutWrappingMut(t, fd.Return, "FuncDecl.Return")
	requireRenamedToMut(t, fd.Throws, "FuncDecl.Throws")

	requireMutWrappingMut(t, td.TypeAnn, "TypeDecl.TypeAnn")
	requireRenamedToMut(t, td.TypeParams[0].Constraint, "TypeDecl.TP.Constraint")

	require.Equal(t, "Mut", ast.QualIdentToString(id.Extends[0].Name), "InterfaceDecl.Extends rename")
	prop := id.TypeAnn.Elems[0].(*ast.PropertyTypeAnn)
	requireRenamedToMut(t, prop.Value, "InterfaceDecl.body.Property.Value")

	require.Equal(t, "Mut", ast.QualIdentToString(cd.Extends.Name), "ClassDecl.Extends rename")
	require.Equal(t, "Mut", ast.QualIdentToString(cd.Implements[0].Name), "ClassDecl.Implements rename")
	requireRenamedToMut(t, cd.TypeParams[0].Default, "ClassDecl.TP.Default")

	requireRenamedToMut(t, ed.TypeParams[0].Constraint, "EnumDecl.TP.Constraint")

	require.Equal(t, "X", eas.Name.Name, "ExportAssignmentStmt.Name unchanged")

	requireRenamedToMut(t, innerNs.TypeAnn, "NamespaceDecl inner VarDecl")
	requireRenamedToMut(t, innerMod.TypeAnn, "DeclareModuleDecl inner VarDecl")
	requireRenamedToMut(t, innerGlobal.TypeAnn, "DeclareGlobalDecl inner VarDecl")
}

// TestTwinRewriter_NoTwinsFastPath exercises the early return when the
// twins slice is empty (no readonly/mutable pairs were detected in the
// bucket). Module must be left untouched.
func TestTwinRewriter_NoTwinsFastPath(t *testing.T) {
	t.Parallel()
	vd := ast.NewVarDecl(ast.ValKind,
		ast.NewIdentPat("v", false, nil, nil, twinSpan),
		roRef(), nil, false, true, twinSpan)
	var nss btree.Map[string, *ast.Namespace]
	nss.Set("", &ast.Namespace{Decls: []ast.Decl{vd}})
	mod := &StandaloneModule{Module: ast.NewModule(nss)}

	rewriteReadonlyTwinRefs(mod, nil)

	ref := vd.TypeAnn.(*ast.TypeRefTypeAnn)
	require.Equal(t, "RO", ast.QualIdentToString(ref.Name), "no twins → no rewrite")
}

// TestTwinRewriter_ImplementsMutableCanary verifies the canary panic
// in renameTypeRefInPlace fires when a mutable twin name appears in a
// slot (Extends/Implements) that cannot carry a `mut` wrapper. The
// pinned TS lib corpus never produces this shape; if it ever does, the
// rewrite would silently emit a non-mut reference, so we want to know.
func TestTwinRewriter_ImplementsMutableCanary(t *testing.T) {
	t.Parallel()
	cd := ast.NewClassDecl(
		ast.NewIdentifier("C", twinSpan), nil, nil,
		nil,
		[]*ast.TypeRefTypeAnn{mutRef()}, // mutable twin in Implements
		nil, false, true, twinSpan,
	)
	require.PanicsWithValue(t,
		// The panic message is built with fmt.Sprintf — match it exactly.
		`twinRewriter.renameTypeRefInPlace: mutable twin "Mut" appears in an Extends/Implements slot, which cannot carry a `+"`mut`"+` wrapper — the readonly-twin rewrite assumed no class or interface in the pinned TS lib extends a mutable twin directly`,
		func() { runRewrite([]ast.Decl{cd}) },
	)
}
