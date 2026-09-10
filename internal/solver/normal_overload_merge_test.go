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

// Optionality meets the way a property's does. Two optional methods fuse to an optional one,
// since an object omitting the member satisfies both sides of the meet; an optional against a
// required one fuses to required, since the meet demands what either side demands. Dropping the
// marker would turn a value that omits the member from a member of the meet into a non-member.
func TestObjectMethodMeetKeepsOptionality(t *testing.T) {
	method := func(field string, param soltype.Type, optional bool) *soltype.ObjectType {
		return &soltype.ObjectType{
			Elems: []soltype.ObjTypeElem{
				&soltype.PropertyElem{Name: field, Type: num()},
				&soltype.MethodElem{Name: "m", Optional: optional, Signatures: []*soltype.FuncType{
					{Params: []*soltype.FuncParam{{Pattern: &soltype.IdentPat{Name: "x"}, Type: param}}, Ret: num()},
				}},
			},
			Inexact: true,
		}
	}
	tests := []struct {
		name       string
		aOpt, bOpt bool
		want       string
	}{
		{"both optional stays optional", true, true,
			"{a: number, b: number, m?(x: number | string) -> number, ...}"},
		{"optional against required is required", true, false,
			"{a: number, b: number, m(x: number | string) -> number, ...}"},
		{"neither optional stays required", false, false,
			"{a: number, b: number, m(x: number | string) -> number, ...}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Context{}
			fused, ok := c.meetObjects(method("a", num(), tt.aOpt), method("b", str(), tt.bOpt))
			require.True(t, ok)
			require.Equal(t, tt.want, soltype.Print(fused))
		})
	}
}

// A member whose OWN arms disagree on the receiver keeps the objects apart. buildMemberSigs
// rejects such a declaration, but it appends each arm before reporting, so a class that drew
// that error still carries the mixed receivers in its body. Reading the first arm alone would
// let two such members concatenate into a member mixing receivers, which is exactly what the
// receiver guard exists to prevent.
func TestSelfInconsistentOverloadStaysUnfused(t *testing.T) {
	c := &Context{}
	self := func(mut bool) *soltype.FuncParam {
		p := &soltype.FuncParam{Pattern: &soltype.IdentPat{Name: "self"}}
		if mut {
			p.Type = &soltype.RefType{Mut: true}
		}
		return p
	}
	// Both members agree on their FIRST arm, so a first-arm-only guard would pass them.
	mixed := func(field string, param soltype.Type) *soltype.ObjectType {
		return &soltype.ObjectType{
			Elems: []soltype.ObjTypeElem{
				&soltype.PropertyElem{Name: field, Type: num()},
				&soltype.MethodElem{Name: "m", Signatures: []*soltype.FuncType{
					{SelfParam: self(false), Params: []*soltype.FuncParam{{Type: param}}, Ret: num()},
					{SelfParam: self(true), Params: []*soltype.FuncParam{{Type: param}}, Ret: str()},
				}},
			},
			Inexact: true,
		}
	}
	_, ok := c.meetObjects(mixed("a", num()), mixed("b", str()))
	require.False(t, ok, "a member whose own arms disagree on the receiver keeps both atoms")
}
