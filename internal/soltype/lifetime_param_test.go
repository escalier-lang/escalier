package soltype

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// counterCls builds a fresh monomorphic Counter instance for the tests.
func counterCls() *ClassType { return &ClassType{Name: "Counter"} }

// A function's own lifetime parameter renders as a `<'a>` binder before the receiver
// and params, and every `&'a` use inside renders under that name. A borrowing method
// `fn get<'a>(&'a self) -> &'a Counter` names 'a once and reuses it in the receiver and
// the return.
func TestPrintFuncLifetimeParam(t *testing.T) {
	tests := []struct {
		name string
		in   func() Type
		want string
	}{
		{
			name: "borrowing method",
			in: func() Type {
				la := &LifetimeVar{ID: 0, Level: 1}
				return &FuncType{
					LifetimeParams: []*LifetimeParam{{Name: "'a", Var: la}},
					SelfParam:      selfRecv(&RefType{Lt: la, Inner: counterCls()}),
					Ret:            &RefType{Lt: la, Inner: counterCls()},
				}
			},
			want: "fn <'a>(&'a self) -> &'a Counter",
		},
		{
			name: "outlives bound",
			in: func() Type {
				la := &LifetimeVar{ID: 0, Level: 1}
				lb := &LifetimeVar{ID: 1, Level: 1}
				return &FuncType{
					LifetimeParams: []*LifetimeParam{
						{Name: "'a", Var: la},
						{Name: "'b", Var: lb, Bounds: []Lifetime{la}},
					},
					Params: []*FuncParam{
						identP("x", &RefType{Lt: la, Inner: counterCls()}),
						identP("y", &RefType{Lt: lb, Inner: counterCls()}),
					},
					Ret: &Void{},
				}
			},
			want: "fn <'a, 'b: 'a>(x: &'a Counter, y: &'b Counter) -> void",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, Print(tt.in()))
		})
	}
}

// A class reference supplies both sorts of arguments: the lifetime argument 'x renders
// before the type argument, so `Ref<'x, number>` shows its lifetime first. PrintAsScheme
// names the free lifetime argument 'a.
func TestPrintClassLifetimeArgs(t *testing.T) {
	lx := &LifetimeVar{ID: 0, Level: 1}
	ref := &ClassType{Name: "Ref", LifetimeArgs: []Lifetime{lx}, TypeArgs: []Type{numP()}}
	require.Equal(t, "Ref<'a, number>", PrintAsScheme(ref))
}

// When a function captures a free scheme lifetime and also declares its own lifetime
// parameter, the generated name for the captured lifetime skips the parameter's source
// name, so the two render distinctly rather than both as 'a. Here the method declares 'a
// and captures a free lifetime carried by a Box<'x, T> return. The own parameter renders
// first under its source name 'a, and the captured lifetime skips 'a to take 'b, so the
// prefix reads `<'a, 'b>` with no collision.
func TestPrintFuncLifetimeParamNoNameCollision(t *testing.T) {
	own := &LifetimeVar{ID: 0, Level: 1}
	free := &LifetimeVar{ID: 1, Level: 1}
	fn := &FuncType{
		LifetimeParams: []*LifetimeParam{{Name: "'a", Var: own}},
		SelfParam:      selfRecv(&RefType{Lt: own, Inner: counterCls()}),
		Ret:            &ClassType{Name: "Box", LifetimeArgs: []Lifetime{free}, TypeArgs: []Type{numP()}},
	}
	require.Equal(t, "fn <'a, 'b>(&'a self) -> Box<'b, number>", PrintAsScheme(fn))
}

// A no-op rewrite over a FuncType carrying its own lifetime parameter keeps its pointer,
// since Accept never allocates when nothing changed.
func TestAcceptFuncLifetimeParamIdentity(t *testing.T) {
	la := &LifetimeVar{ID: 0, Level: 1}
	fn := &FuncType{
		LifetimeParams: []*LifetimeParam{{Name: "'a", Var: la}},
		SelfParam:      selfRecv(&RefType{Lt: la, Inner: counterCls()}),
		Ret:            &RefType{Lt: la, Inner: counterCls()},
	}
	require.Same(t, fn, fn.Accept(identityVisitor{}, Positive), "an unchanged FuncType keeps its pointer")
}

// Rewriting a type argument inside a class reference rebuilds the ClassType, carries the
// lifetime argument through unchanged, and replaces the type argument.
func TestAcceptClassLifetimeArgsCopyOnWrite(t *testing.T) {
	str := &PrimType{Prim: StrPrim}
	a := &TypeVarType{ID: 1}
	lx := &LifetimeVar{ID: 0, Level: 1}
	ref := &ClassType{Name: "Ref", LifetimeArgs: []Lifetime{lx}, TypeArgs: []Type{a}}

	got := ref.Accept(&replaceVar{target: a, repl: str}, Positive).(*ClassType)

	require.NotSame(t, ref, got, "a changed type argument forces a new ClassType")
	require.Same(t, lx, got.LifetimeArgs[0], "the lifetime argument carries through unchanged")
	require.Same(t, str, got.TypeArgs[0], "the type argument took the replacement")
}

// freeLifetimeVars excludes a function's own lifetime parameter — it is bound, not free —
// while still collecting a class reference's lifetime argument.
func TestFreeLifetimeVarsBoundLifetimeParam(t *testing.T) {
	t.Run("bound 'a is omitted", func(t *testing.T) {
		la := &LifetimeVar{ID: 0, Level: 1}
		fn := &FuncType{
			LifetimeParams: []*LifetimeParam{{Name: "'a", Var: la}},
			SelfParam:      selfRecv(&RefType{Lt: la, Inner: counterCls()}),
			Ret:            &RefType{Lt: la, Inner: counterCls()},
		}
		require.Empty(t, freeLifetimeVars(fn))
	})

	t.Run("a class reference's lifetime argument is collected", func(t *testing.T) {
		lx := &LifetimeVar{ID: 0, Level: 1}
		ref := &ClassType{Name: "Ref", LifetimeArgs: []Lifetime{lx}, TypeArgs: []Type{numP()}}
		require.Equal(t, []*LifetimeVar{lx}, freeLifetimeVars(ref))
	})

	t.Run("an outer lifetime reached only through a bound is collected", func(t *testing.T) {
		outer := &LifetimeVar{ID: 0, Level: 1}
		lb := &LifetimeVar{ID: 1, Level: 1}
		fn := &FuncType{
			LifetimeParams: []*LifetimeParam{{Name: "'b", Var: lb, Bounds: []Lifetime{outer}}},
			Params:         []*FuncParam{identP("x", &RefType{Lt: lb, Inner: counterCls()})},
			Ret:            &Void{},
		}
		require.Equal(t, []*LifetimeVar{outer}, freeLifetimeVars(fn))
	})
}

// LevelOf folds a class reference's lifetime argument into its level, so a free lifetime
// argument lifts the level the same way a type argument does.
func TestLevelOfClassLifetimeArgs(t *testing.T) {
	ref := &ClassType{
		Name:         "Ref",
		LifetimeArgs: []Lifetime{&LifetimeVar{ID: 0, Level: 7}},
		TypeArgs:     []Type{&TypeVarType{ID: 1, Level: 3}},
	}
	require.Equal(t, 7, LevelOf(ref), "the level is the max over the lifetime and type arguments")
}
