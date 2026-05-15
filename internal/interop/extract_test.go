package interop

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/type_system"
	"github.com/stretchr/testify/require"
)

// These tests drive Extract directly with hand-built AST + Namespace
// pairs. The full parse → check → extract flow is exercised by the
// integration fixture under fixtures/interop_mutability/.

func emptySpan() ast.Span { return ast.Span{} }

func TestExtractFreeFunctionFromDeclareGlobal(t *testing.T) {
	fn := type_system.NewFuncType(nil, nil, nil, type_system.NewNumPrimType(nil), nil)

	funcDecl := ast.NewFuncDecl(
		ast.NewIdentifier("foo", emptySpan()),
		nil, nil, nil, nil, nil, nil,
		false, true, false,
		emptySpan(),
	)
	declareGlobal := ast.NewDeclareGlobalDecl([]ast.Decl{funcDecl}, true, emptySpan())

	globalNs := type_system.NewNamespace()
	globalNs.Values["foo"] = &type_system.Binding{Type: fn}

	out := Extract(
		[]ast.Decl{declareGlobal},
		globalNs, nil,
		"shipped:/test.esc",
		OverrideTierShipped,
	)
	require.Contains(t, out, "", "expected entry under \"\" (global)")
	eff := out[""].Free["foo"]
	require.NotNil(t, eff, "expected Free[foo] to be set")
	require.Same(t, fn, eff.Type, "expected eff.Type to be the func from the namespace")
	require.Equal(t, OverrideTierShipped, eff.Tier)
}

func TestExtractFreeFunctionFromDeclareModule(t *testing.T) {
	fn := type_system.NewFuncType(nil, nil, nil, type_system.NewNumPrimType(nil), nil)

	funcDecl := ast.NewFuncDecl(
		ast.NewIdentifier("map", emptySpan()),
		nil, nil, nil, nil, nil, nil,
		false, true, false,
		emptySpan(),
	)
	declareModule := ast.NewDeclareModuleDecl(
		&ast.StrLit{Value: "lodash"},
		[]ast.Decl{funcDecl},
		true,
		emptySpan(),
	)

	modNs := type_system.NewNamespace()
	modNs.Values["map"] = &type_system.Binding{Type: fn}

	out := Extract(
		[]ast.Decl{declareModule},
		nil,
		map[string]*type_system.Namespace{"lodash": modNs},
		"shipped:/lodash.esc",
		OverrideTierShipped,
	)
	require.Contains(t, out, "lodash")
	eff := out["lodash"].Free["map"]
	require.NotNil(t, eff)
	require.Same(t, fn, eff.Type)
}

func TestExtractSkipsNonOverrideBlocks(t *testing.T) {
	funcDecl := ast.NewFuncDecl(
		ast.NewIdentifier("foo", emptySpan()),
		nil, nil, nil, nil, nil, nil,
		false, true, false,
		emptySpan(),
	)
	// override=false on the wrapper.
	declareGlobal := ast.NewDeclareGlobalDecl([]ast.Decl{funcDecl}, false, emptySpan())

	globalNs := type_system.NewNamespace()
	globalNs.Values["foo"] = &type_system.Binding{Type: type_system.NewNumPrimType(nil)}

	out := Extract(
		[]ast.Decl{declareGlobal},
		globalNs, nil,
		"test.esc",
		OverrideTierUserProject,
	)
	require.Empty(t, out, "non-override blocks must produce no scope contributions")
}

func TestExtractTypeAlias(t *testing.T) {
	alias := &type_system.TypeAlias{Type: type_system.NewNumPrimType(nil)}

	typeDecl := ast.NewTypeDecl(
		ast.NewIdentifier("MyNum", emptySpan()),
		nil, nil,
		false, true,
		emptySpan(),
	)
	declareGlobal := ast.NewDeclareGlobalDecl([]ast.Decl{typeDecl}, true, emptySpan())

	globalNs := type_system.NewNamespace()
	globalNs.Types["MyNum"] = alias

	out := Extract(
		[]ast.Decl{declareGlobal},
		globalNs, nil,
		"test.esc",
		OverrideTierUserProject,
	)
	ms := out[""]
	require.NotNil(t, ms, "expected global scope contribution")
	eff := ms.Free["MyNum"]
	require.NotNil(t, eff)
	require.Same(t, alias.Type, eff.Type, "expected MyNum leaf carrying the alias type")
}

func TestExtractInterfaceInstanceMethod(t *testing.T) {
	methodFn := type_system.NewFuncType(nil, nil, nil, type_system.NewNumPrimType(nil), nil)
	iface := type_system.NewObjectType(nil, []type_system.ObjTypeElem{
		type_system.NewMethodElem(type_system.NewStrKey("ping"), methodFn),
	})

	methodAnn := &ast.MethodTypeAnn{
		Name: ast.NewIdent("ping", emptySpan()),
		Fn:   nil,
	}
	objTypeAnn := ast.NewObjectTypeAnn([]ast.ObjTypeAnnElem{methodAnn}, emptySpan())
	interfaceDecl := ast.NewInterfaceDecl(
		ast.NewIdentifier("Pinger", emptySpan()),
		nil, nil, nil, objTypeAnn,
		false, true, emptySpan(),
	)
	declareGlobal := ast.NewDeclareGlobalDecl([]ast.Decl{interfaceDecl}, true, emptySpan())

	globalNs := type_system.NewNamespace()
	globalNs.Types["Pinger"] = &type_system.TypeAlias{Type: iface}

	out := Extract(
		[]ast.Decl{declareGlobal},
		globalNs, nil,
		"test.esc",
		OverrideTierShipped,
	)
	ms := out[""]
	require.NotNil(t, ms, "expected global contribution")
	child := ms.Children["Pinger"]
	require.NotNil(t, child, "expected Children[Pinger]")
	require.NotNil(t, child.Instance, "expected Instance MemberSet populated on interface child")
	eff := child.Instance.Methods["ping"]
	require.NotNil(t, eff)
	require.Same(t, methodFn, eff.Type, "expected ping method to carry its FuncType from the namespace")
}

func TestExtractNamespaceNesting(t *testing.T) {
	fn := type_system.NewFuncType(nil, nil, nil, type_system.NewNumPrimType(nil), nil)
	innerFunc := ast.NewFuncDecl(
		ast.NewIdentifier("inner", emptySpan()),
		nil, nil, nil, nil, nil, nil,
		false, true, false,
		emptySpan(),
	)
	innerNs := ast.NewNamespaceDecl(
		ast.NewIdentifier("Util", emptySpan()),
		[]ast.Decl{innerFunc},
		false, false, emptySpan(),
	)
	declareGlobal := ast.NewDeclareGlobalDecl([]ast.Decl{innerNs}, true, emptySpan())

	globalNs := type_system.NewNamespace()
	utilNs := type_system.NewNamespace()
	utilNs.Values["inner"] = &type_system.Binding{Type: fn}
	globalNs.Namespaces["Util"] = utilNs

	out := Extract(
		[]ast.Decl{declareGlobal},
		globalNs, nil,
		"test.esc",
		OverrideTierUserProject,
	)
	ms := out[""]
	require.NotNil(t, ms, "expected global contribution")
	child := ms.Children["Util"]
	require.NotNil(t, child, "expected Children[Util] for nested namespace")
	require.Nil(t, child.Instance, "namespace child should not have Instance populated")
	require.Nil(t, child.Static, "namespace child should not have Static populated")
	eff := child.Free["inner"]
	require.NotNil(t, eff, "expected nested namespace fn")
	require.Same(t, fn, eff.Type)
}

func TestExtractDestructuredVarDecl(t *testing.T) {
	// Object-pattern destructuring binds two names; each should be
	// resolved against the checker-produced namespace and surface as
	// Free leaves.
	fnA := type_system.NewFuncType(nil, nil, nil, type_system.NewNumPrimType(nil), nil)
	fnB := type_system.NewFuncType(nil, nil, nil, type_system.NewStrPrimType(nil), nil)

	objPat := ast.NewObjectPat(
		[]ast.ObjPatElem{
			ast.NewObjShorthandPat(ast.NewIdentifier("a", emptySpan()), false, nil, nil, emptySpan()),
			ast.NewObjShorthandPat(ast.NewIdentifier("b", emptySpan()), false, nil, nil, emptySpan()),
		},
		emptySpan(),
	)
	varDecl := ast.NewVarDecl(ast.ValKind, objPat, nil, nil, false, true, emptySpan())
	declareGlobal := ast.NewDeclareGlobalDecl([]ast.Decl{varDecl}, true, emptySpan())

	globalNs := type_system.NewNamespace()
	globalNs.Values["a"] = &type_system.Binding{Type: fnA}
	globalNs.Values["b"] = &type_system.Binding{Type: fnB}

	out := Extract(
		[]ast.Decl{declareGlobal},
		globalNs, nil,
		"test.esc",
		OverrideTierShipped,
	)
	ms := out[""]
	require.NotNil(t, ms, "expected global contribution")
	require.NotNil(t, ms.Free["a"])
	require.Same(t, fnA, ms.Free["a"].Type, "expected Free[a] to carry fnA")
	require.NotNil(t, ms.Free["b"])
	require.Same(t, fnB, ms.Free["b"].Type, "expected Free[b] to carry fnB")
}

func TestExtractClassDropsStaticMembers(t *testing.T) {
	// Static-side overrides are intentionally dropped until static
	// lookup is wired (see comment in buildClassChild). Without the
	// skip, Extract would record a static method with Type=nil and the
	// merge step's "override wins" branch would clobber the original's
	// typed static slot. This test pins the drop behavior.
	instanceFn := type_system.NewFuncType(nil, nil, nil, type_system.NewNumPrimType(nil), nil)
	cls := type_system.NewObjectType(nil, []type_system.ObjTypeElem{
		type_system.NewMethodElem(type_system.NewStrKey("inst"), instanceFn),
	})

	instMethod := &ast.MethodElem{
		Name:   ast.NewIdent("inst", emptySpan()),
		Fn:     nil,
		Static: false,
		Span_:  emptySpan(),
	}
	staticMethod := &ast.MethodElem{
		Name:   ast.NewIdent("doStatic", emptySpan()),
		Fn:     nil,
		Static: true,
		Span_:  emptySpan(),
	}
	classDecl := ast.NewClassDecl(
		ast.NewIdentifier("Widget", emptySpan()),
		nil, nil, nil, nil,
		[]ast.ClassElem{instMethod, staticMethod},
		false, true, emptySpan(),
	)
	declareGlobal := ast.NewDeclareGlobalDecl([]ast.Decl{classDecl}, true, emptySpan())

	globalNs := type_system.NewNamespace()
	globalNs.Types["Widget"] = &type_system.TypeAlias{Type: cls}

	out := Extract(
		[]ast.Decl{declareGlobal},
		globalNs, nil,
		"test.esc",
		OverrideTierShipped,
	)
	ms := out[""]
	require.NotNil(t, ms)
	child := ms.Children["Widget"]
	require.NotNil(t, child, "expected Children[Widget]")
	require.NotNil(t, child.Instance)
	require.NotNil(t, child.Instance.Methods["inst"], "expected instance method inst recorded")
	require.NotNil(t, child.Static, "expected Static MemberSet allocated (class shape signal)")
	require.NotContains(t, child.Static.Methods, "doStatic", "expected static methods to be dropped during extraction")
}

func TestExtractMissingNamespaceEntryProducesNilType(t *testing.T) {
	// When the checker hasn't populated a binding for a declared name
	// (e.g. a typo override), the extractor should not insert a Free
	// leaf — the slot simply doesn't appear.
	funcDecl := ast.NewFuncDecl(
		ast.NewIdentifier("orphan", emptySpan()),
		nil, nil, nil, nil, nil, nil,
		false, true, false,
		emptySpan(),
	)
	declareGlobal := ast.NewDeclareGlobalDecl([]ast.Decl{funcDecl}, true, emptySpan())

	globalNs := type_system.NewNamespace()

	out := Extract(
		[]ast.Decl{declareGlobal},
		globalNs, nil,
		"test.esc",
		OverrideTierShipped,
	)
	ms := out[""]
	require.NotNil(t, ms, "expected module scope created even when bindings missing")
	require.NotContains(t, ms.Free, "orphan", "expected no leaf for binding the checker didn't produce")
}
