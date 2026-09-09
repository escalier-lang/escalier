package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// #1517. Two normal-form atoms sharing a method or a constructor fuse whether the shared
// member carries one signature or an overload set. An overload set denotes the intersection
// of its arms, so the meet of two sets is the concatenation of their arms, canonicalized by
// newIntersection.
//
// None of this changes what the compiler accepts or rejects. An unfused pair of atoms
// denotes the meet exactly, so the fusion only shrinks the normal form.

// TestObjectOverloadMethodMeet covers the method half through meetObjects. The objects are
// written inexact so that neither caps out on a field the other lacks, which would settle
// the meet as `never` before any member rule ran.
func TestObjectOverloadMethodMeet(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "two overload sets concatenate every arm of both",
			in: "{a: number, foo(self, x: number) -> number, foo(self, x: string) -> string, ...} & " +
				"{b: number, foo(self, x: boolean) -> boolean, foo(self, x: null) -> null, ...}",
			want: "{a: number, b: number, foo(self, x: number) -> number; " +
				"foo(self, x: string) -> string; foo(self, x: boolean) -> boolean; " +
				"foo(self, x: null) -> null, ...}",
		},
		{
			name: "an arm both sides carry appears once",
			in: "{a: number, foo(self, x: number) -> number, foo(self, x: string) -> string, ...} & " +
				"{b: number, foo(self, x: number) -> number, ...}",
			want: "{a: number, b: number, foo(self, x: number) -> number; " +
				"foo(self, x: string) -> string, ...}",
		},
		{
			name: "sets of different lengths fuse",
			in: "{a: number, foo(self, x: number) -> number, foo(self, x: string) -> string, ...} & " +
				"{b: number, foo(self, x: boolean) -> boolean, ...}",
			want: "{a: number, b: number, foo(self, x: number) -> number; " +
				"foo(self, x: string) -> string; foo(self, x: boolean) -> boolean, ...}",
		},
		{
			name: "two single signatures that share a domain still fuse exactly, into one arm",
			in: "{a: number, foo(self, x: number) -> number | string, ...} & " +
				"{b: number, foo(self, x: number) -> string | boolean, ...}",
			want: "{a: number, b: number, foo(self, x: number) -> string, ...}",
		},
		{
			name: "two single signatures with no exact fuse concatenate instead of keeping both atoms",
			in: "{a: number, foo(self, x: number) -> number, ...} & " +
				"{b: number, foo(self, x: string) -> string, ...}",
			want: "{a: number, b: number, foo(self, x: number) -> number; " +
				"foo(self, x: string) -> string, ...}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Context{}
			require.Equal(t, tt.want, normDNF(c, parseType(t, tt.in)))
		})
	}
}

// TestObjectOverloadConstructorMeet covers the constructor half. An object type annotation
// accepts at most one `new` signature, so an overloaded constructor cannot be written as a
// type and the objects are built directly. A class declaring several constructors is where
// the shape comes from, which #1501 gave a Signatures list to carry.
func TestObjectOverloadConstructorMeet(t *testing.T) {
	ctor := func(param soltype.Type, ret soltype.Type) *soltype.FuncType {
		return &soltype.FuncType{
			Params: []*soltype.FuncParam{{Pattern: &soltype.IdentPat{Name: "x"}, Type: param}},
			Ret:    ret,
		}
	}
	obj := func(field string, sigs ...*soltype.FuncType) *soltype.ObjectType {
		return &soltype.ObjectType{
			Elems: []soltype.ObjTypeElem{
				&soltype.PropertyElem{Name: field, Type: num()},
				&soltype.ConstructorElem{Signatures: sigs},
			},
			Inexact: true,
		}
	}
	c := &Context{}
	boolT := func() soltype.Type { return &soltype.PrimType{Prim: soltype.BoolPrim} }

	fused, ok := c.meetObjects(
		obj("a", ctor(num(), boolT()), ctor(str(), boolT())),
		obj("b", ctor(boolT(), boolT())),
	)
	require.True(t, ok, "two objects sharing an overloaded constructor fuse to one atom")
	require.Equal(t,
		"{new (x: number) -> boolean; new (x: string) -> boolean; new (x: boolean) -> boolean, "+
			"a: number, b: number, ...}",
		soltype.Print(fused))
}

// TestOverloadReceiverMismatchStaysUnfused pins the receiver guard. Under the concatenation
// each arm keeps the receiver it was written with, so two sets whose receivers disagree
// would fuse into a member mixing them — a shape classes.go could not read, since it takes
// the first arm's receiver as the receiver of the whole member. The guard keeps both atoms
// instead, which denotes the meet just as exactly.
//
// The objects are built directly because an object type annotation has no `mut self`
// receiver syntax.
func TestOverloadReceiverMismatchStaysUnfused(t *testing.T) {
	c := &Context{}
	method := func(field string, mut bool) *soltype.ObjectType {
		self := &soltype.FuncParam{Pattern: &soltype.IdentPat{Name: "self"}}
		if mut {
			self.Type = &soltype.RefType{Mut: true}
		}
		return &soltype.ObjectType{
			Elems: []soltype.ObjTypeElem{
				&soltype.PropertyElem{Name: field, Type: num()},
				&soltype.MethodElem{Name: "foo", Signatures: []*soltype.FuncType{
					{SelfParam: self, Ret: num()},
				}},
			},
			Inexact: true,
		}
	}
	_, ok := c.meetObjects(method("a", true), method("b", false))
	require.False(t, ok, "methods whose receivers disagree keep both atoms")
}
