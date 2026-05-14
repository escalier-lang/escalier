package interop

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/type_system"
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
	ms, ok := out[""]
	if !ok {
		t.Fatalf("expected entry under \"\" (global); got %#v", out)
	}
	eff := ms.Free["foo"]
	if eff == nil {
		t.Fatalf("expected Free[foo] to be set; got nil")
	}
	if eff.Type != fn {
		t.Fatalf("expected eff.Type to be the func from the namespace; got %#v", eff.Type)
	}
	if eff.Tier != OverrideTierShipped {
		t.Fatalf("expected Tier=Shipped; got %v", eff.Tier)
	}
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
	ms, ok := out["lodash"]
	if !ok {
		t.Fatalf("expected entry under \"lodash\"; got %#v", out)
	}
	if eff := ms.Free["map"]; eff == nil || eff.Type != fn {
		t.Fatalf("expected Free[map] to carry func type; got %#v", eff)
	}
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
	if len(out) != 0 {
		t.Fatalf("non-override blocks must produce no scope contributions; got %#v", out)
	}
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
	if ms == nil {
		t.Fatalf("expected global scope contribution")
	}
	eff := ms.Free["MyNum"]
	if eff == nil || eff.Type != alias.Type {
		t.Fatalf("expected MyNum leaf carrying the alias type; got %#v", eff)
	}
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
	if ms == nil {
		t.Fatalf("expected global contribution")
	}
	child := ms.Children["Pinger"]
	if child == nil {
		t.Fatalf("expected Children[Pinger]; got %#v", ms.Children)
	}
	if child.Instance == nil {
		t.Fatalf("expected Instance MemberSet populated on interface child")
	}
	eff := child.Instance.Methods["ping"]
	if eff == nil || eff.Type != methodFn {
		t.Fatalf("expected ping method to carry its FuncType from the namespace; got %#v", eff)
	}
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
	if ms == nil {
		t.Fatalf("expected global contribution")
	}
	child := ms.Children["Util"]
	if child == nil {
		t.Fatalf("expected Children[Util] for nested namespace")
	}
	if child.Instance != nil || child.Static != nil {
		t.Fatalf("namespace child should not have Instance/Static populated")
	}
	if eff := child.Free["inner"]; eff == nil || eff.Type != fn {
		t.Fatalf("expected nested namespace fn; got %#v", eff)
	}
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
	if ms == nil {
		t.Fatalf("expected module scope created even when bindings missing")
	}
	if _, present := ms.Free["orphan"]; present {
		t.Fatalf("expected no leaf for binding the checker didn't produce")
	}
}
