package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// The rows here cover fusing objects that carry a method, an accessor, or a
// constructor. parseType lowers only property members, so each case builds its
// objects with the helpers below and combines them through newIntersection or
// newUnion, the same combine path the production solver takes. normDNF then renders
// the fused normal form a row asserts on.

// selfRecv is a method or accessor receiver. printSelfReceiver renders any
// non-borrow receiver as `self`, and both operands of a case share this type so
// their receivers compare equal.
func selfRecv() *soltype.FuncParam {
	return &soltype.FuncParam{Pattern: &soltype.IdentPat{Name: "self"}, Type: &soltype.ClassType{Name: "Self"}}
}

// methodElem builds a one-signature instance method `name(self, params) -> ret`.
func methodElem(name string, ret soltype.Type, params ...*soltype.FuncParam) *soltype.MethodElem {
	sig := &soltype.FuncType{SelfParam: selfRecv(), Params: params, Ret: ret}
	return &soltype.MethodElem{Name: name, Signatures: []*soltype.FuncType{sig}}
}

// overloadElem builds a method holding several signatures, an overload set.
func overloadElem(name string, sigs ...*soltype.FuncType) *soltype.MethodElem {
	for _, s := range sigs {
		s.SelfParam = selfRecv()
	}
	return &soltype.MethodElem{Name: name, Signatures: sigs}
}

// getterElem builds an instance getter `get name(self) -> t`.
func getterElem(name string, t soltype.Type) *soltype.GetterElem {
	return &soltype.GetterElem{Name: name, SelfParam: selfRecv(), Type: t}
}

// setterElem builds an instance setter `set name(self, value: t)`.
func setterElem(name string, t soltype.Type) *soltype.SetterElem {
	return &soltype.SetterElem{Name: name, SelfParam: selfRecv(), Param: t}
}

// setterThrows builds an instance setter that raises on write, `set name(self,
// value: t) throws e`.
func setterThrows(name string, t, e soltype.Type) *soltype.SetterElem {
	return &soltype.SetterElem{Name: name, SelfParam: selfRecv(), Param: t, Throws: e}
}

// ctorElem builds a constructor `new (params) -> ret`.
func ctorElem(ret soltype.Type, params ...*soltype.FuncParam) *soltype.ConstructorElem {
	return &soltype.ConstructorElem{Fn: &soltype.FuncType{Params: params, Ret: ret}}
}

func TestObjectMemberMeet(t *testing.T) {
	tests := []struct {
		name string
		a    *soltype.ObjectType
		b    *soltype.ObjectType
		want string
	}{
		{
			name: "objects sharing an identical method fuse their disjoint fields",
			a:    inexactObj(propElem("a", num()), methodElem("foo", num())),
			b:    inexactObj(propElem("b", num()), methodElem("foo", num())),
			want: "{a: number, b: number, foo(self) -> number, ...}",
		},
		{
			name: "a shared method with agreeing domains meets its codomains",
			a:    exactObj(methodElem("foo", newUnion(nil, []soltype.Type{num(), str()}, false), identParam("x", num()))),
			b:    exactObj(methodElem("foo", newUnion(nil, []soltype.Type{str(), boolT()}, false), identParam("x", num()))),
			want: "{foo(self, x: number) -> string}",
		},
		{
			name: "a shared getter meets its returned value",
			a:    exactObj(getterElem("x", newUnion(nil, []soltype.Type{num(), str()}, false))),
			b:    exactObj(getterElem("x", newUnion(nil, []soltype.Type{str(), boolT()}, false))),
			want: "{get x(self) -> string}",
		},
		{
			name: "a shared setter with equal raises joins the value it accepts",
			a:    exactObj(setterElem("x", num())),
			b:    exactObj(setterElem("x", str())),
			want: "{set x(self, value: number | string)}",
		},
		{
			name: "a shared setter with an equal written value meets its raises",
			a:    exactObj(setterThrows("x", num(), newUnion(nil, []soltype.Type{strLit("a"), strLit("b")}, false))),
			b:    exactObj(setterThrows("x", num(), newUnion(nil, []soltype.Type{strLit("b"), strLit("c")}, false))),
			want: `{set x(self, value: number) throws "b"}`,
		},
		{
			name: "a shared setter differing in both value and raises keeps both atoms",
			a:    exactObj(setterThrows("x", num(), strLit("a"))),
			b:    exactObj(setterThrows("x", str(), strLit("b"))),
			want: `{set x(self, value: number) throws "a"} & {set x(self, value: string) throws "b"}`,
		},
		{
			name: "a shared constructor joins its domain over a shared return",
			a:    exactObj(ctorElem(boolT(), identParam("x", num()))),
			b:    exactObj(ctorElem(boolT(), identParam("x", str()))),
			want: "{new (x: number | string) -> boolean}",
		},
		{
			name: "an identical overload set fuses without meeting the arms",
			a: inexactObj(propElem("a", num()), overloadElem("foo",
				exactFn(num(), identParam("x", num())),
				exactFn(str(), identParam("x", str())))),
			b: inexactObj(propElem("b", num()), overloadElem("foo",
				exactFn(num(), identParam("x", num())),
				exactFn(str(), identParam("x", str())))),
			want: "{a: number, b: number, foo(self, x: number) -> number; foo(self, x: string) -> string, ...}",
		},
		{
			name: "a method and a property sharing a name keep both atoms",
			a:    exactObj(methodElem("foo", num())),
			b:    exactObj(propElem("foo", num())),
			want: "{foo: number} & {foo(self) -> number}",
		},
		{
			name: "an exact object meets an exact object lacking its method: never",
			a:    exactObj(methodElem("foo", num())),
			b:    exactObj(methodElem("bar", num())),
			want: "never",
		},
		{
			name: "a get/set accessor pair keeps both atoms unfused",
			a:    exactObj(propElem("a", num()), getterElem("x", num()), setterElem("x", num())),
			b:    exactObj(propElem("b", num()), getterElem("x", num()), setterElem("x", num())),
			want: "{a: number, get x(self) -> number, set x(self, value: number)} & " +
				"{b: number, get x(self) -> number, set x(self, value: number)}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Context{}
			got := normDNF(c, newIntersection(nil, []soltype.Type{tt.a, tt.b}))
			require.Equal(t, tt.want, got)
		})
	}
}

// TestIndexSignatureObjectStaysUnfused guards the scope line of #1103: a mapped
// member such as an index signature has no per-member merge rule, so an object
// carrying one is kept as its own atom. A settled index signature is not a residual,
// so this checks fusableMember rather than the residual test alone keeps it out.
func TestIndexSignatureObjectStaysUnfused(t *testing.T) {
	c := &Context{}
	withIndexSig := func() *soltype.ObjectType {
		return &soltype.ObjectType{Elems: []soltype.ObjTypeElem{
			propElem("a", num()),
			&soltype.MappedElem{Key: &soltype.MappedKeyType{ID: 0, Name: "K"}, Keys: str(), Value: num()},
		}}
	}
	_, ok := c.meetObjects(withIndexSig(), withIndexSig())
	require.False(t, ok, "meetObjects keeps an index-signature object unfused")
	_, ok = c.joinObjects(withIndexSig(), withIndexSig())
	require.False(t, ok, "joinObjects keeps an index-signature object unfused")
}

func TestObjectMemberJoin(t *testing.T) {
	tests := []struct {
		name string
		a    *soltype.ObjectType
		b    *soltype.ObjectType
		want string
	}{
		{
			name: "objects agreeing but for one property widen it, keeping shared methods",
			a:    exactObj(propElem("x", num()), methodElem("foo", num())),
			b:    exactObj(propElem("x", str()), methodElem("foo", num())),
			want: "{foo(self) -> number, x: number | string}",
		},
		{
			name: "objects differing only in a getter widen it",
			a:    exactObj(getterElem("x", num()), methodElem("foo", num())),
			b:    exactObj(getterElem("x", str()), methodElem("foo", num())),
			want: "{foo(self) -> number, get x(self) -> number | string}",
		},
		{
			name: "objects differing in a method keep both atoms",
			a:    exactObj(methodElem("foo", num())),
			b:    exactObj(methodElem("foo", str())),
			want: "{foo(self) -> number} | {foo(self) -> string}",
		},
		{
			name: "objects differing in a setter keep both atoms",
			a:    exactObj(setterElem("x", num())),
			b:    exactObj(setterElem("x", str())),
			want: "{set x(self, value: number)} | {set x(self, value: string)}",
		},
		{
			name: "objects differing in a constructor keep both atoms",
			a:    exactObj(ctorElem(num())),
			b:    exactObj(ctorElem(str())),
			want: "{new () -> number} | {new () -> string}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Context{}
			got := normDNF(c, newUnion(nil, []soltype.Type{tt.a, tt.b}, false))
			require.Equal(t, tt.want, got)
		})
	}
}
