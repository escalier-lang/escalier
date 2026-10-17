package codegen

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// TestKindRegistryRendering covers the .d.ts forms a type-constructor parameter renders as: the
// constraint on its binder, an application of it, a constructor argument, and the registry
// declarations the module emits for the constructors it named.
func TestKindRegistryRendering(t *testing.T) {
	box := &soltype.ClassType{Name: "Box"}
	tagged := &soltype.ClassType{Name: "Tagged"}
	sized := &soltype.AliasType{Name: "Sized"}
	a := &soltype.TypeVarType{ID: 1}
	f := &soltype.TypeVarType{ID: 2}

	// fn <F<out _>: Sized, A>(fa: F<A>) -> F<number>
	sig := &soltype.FuncType{
		TypeParams: []*soltype.TypeParam{
			{Name: "F", Var: f, Slots: []soltype.SlotVariance{soltype.SlotCovariant},
				CtorBound: &soltype.TypeCtorType{Base: sized, Fixed: []soltype.Type{nil}}},
			{Name: "A", Var: a},
		},
		Params: []*soltype.FuncParam{solParam("fa", &soltype.TypeAppType{Head: f, Args: []soltype.Type{a}})},
		Ret:    &soltype.TypeAppType{Head: f, Args: []soltype.Type{solNum()}},
	}
	render := newSolTypeAnnBuilder(solNames{preludePrefix: solPreludePrefix}, "t", nil)
	render.kinds = newKindRegistry(nil)
	printer := NewPrinter()
	printer.PrintTypeAnn(render.render(sig))
	require.Equal(t,
		"<F extends {[K in URIS1]: URItoKind1<unknown>[K] extends Sized<unknown> ? K : never}[URIS1], A>"+
			"(fa: Kind1<F, A>) => Kind1<F, number>",
		printer.Output)

	// Constructor arguments render as their URIs.
	for _, tt := range []struct {
		ctor *soltype.TypeCtorType
		want string
	}{
		{ctor: &soltype.TypeCtorType{Base: box, Fixed: []soltype.Type{nil}}, want: `"Box"`},
		{ctor: &soltype.TypeCtorType{Base: tagged, Fixed: []soltype.Type{solStr(), nil}}, want: `"Tagged<string, _>"`},
		// A fixed argument naming a variable has no registry binder for it.
		{ctor: &soltype.TypeCtorType{Base: tagged, Fixed: []soltype.Type{a, nil}}, want: "unknown"},
	} {
		printer := NewPrinter()
		printer.PrintTypeAnn(render.typeAnn(tt.ctor))
		require.Equal(t, tt.want, printer.Output)
	}

	// The registry declares one entry per constructor the render named, ambient for a script.
	kindRender := newSolTypeAnnBuilder(solNames{preludePrefix: solPreludePrefix}, "", nil)
	kindRender.kinds = render.kinds
	kindRender.typeParamNames = map[*soltype.TypeVarType]string{}
	printer = NewPrinter()
	for _, stmt := range kindRegistryStmts(render.kinds, kindRender, false) {
		printer.PrintStmt(stmt)
		printer.Output += "\n"
	}
	require.Equal(t, "declare interface URItoKind1<A0> {Box: Box<A0>, \"Tagged<string, _>\": Tagged<string, A0>}\n"+
		"declare type URIS1 = keyof URItoKind1<unknown>;\n"+
		"declare type Kind1<F extends URIS1, A0> = URItoKind1<A0>[F];\n",
		printer.Output)
}

// TestKindRegistryNames covers the names the registry mints. A name the module declares pushes
// the minted name aside, and two constructors that print alike get distinct URIs.
func TestKindRegistryNames(t *testing.T) {
	r := newKindRegistry(set.FromSlice([]string{"Kind1", "K"}))
	registry, uris, kind := r.kindNames(1)
	require.Equal(t, []string{"URItoKind1", "URIS1", "Kind1_"}, []string{registry, uris, kind})
	require.Equal(t, "K_", r.name("K"))

	first, ok := r.uriOf(&soltype.TypeCtorType{Base: &soltype.ClassType{Name: "A.Box"}, Fixed: []soltype.Type{nil}})
	require.True(t, ok)
	second, ok := r.uriOf(&soltype.TypeCtorType{Base: &soltype.ClassType{Name: "B.Box"}, Fixed: []soltype.Type{nil}})
	require.True(t, ok)
	again, ok := r.uriOf(&soltype.TypeCtorType{Base: &soltype.ClassType{Name: "A.Box"}, Fixed: []soltype.Type{nil}})
	require.True(t, ok)
	require.Equal(t, []string{"Box", "Box#2", "Box"}, []string{first, second, again})
}
