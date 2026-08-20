package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// These tests cover fusing objects that carry a method, an accessor, or a
// constructor. Each case states its input as an intersection or a union of object
// types written in Escalier source, which parseType lowers, and asserts the normal
// form the merge produces.

// TestObjectMemberMeet exercises meetObjects over member-carrying objects.
func TestObjectMemberMeet(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "objects sharing an identical method fuse their disjoint fields",
			in:   "{a: number, foo(self) -> number, ...} & {b: number, foo(self) -> number, ...}",
			want: "{a: number, b: number, foo(self) -> number, ...}",
		},
		{
			name: "a shared method with agreeing domains meets its codomains",
			in:   "{foo(self, x: number) -> number | string} & {foo(self, x: number) -> string | boolean}",
			want: "{foo(self, x: number) -> string}",
		},
		{
			name: "a shared getter meets its returned value",
			in:   "{get x(self) -> number | string} & {get x(self) -> string | boolean}",
			want: "{get x(self) -> string}",
		},
		{
			name: "a shared setter with equal raises joins the value it accepts",
			in:   "{set x(self, v: number) -> undefined} & {set x(self, v: string) -> undefined}",
			want: "{set x(self, value: number | string)}",
		},
		{
			name: "a shared setter with an equal written value meets its raises",
			in: `{set x(self, v: number) -> undefined throws "a" | "b"} & ` +
				`{set x(self, v: number) -> undefined throws "b" | "c"}`,
			want: `{set x(self, value: number) throws "b"}`,
		},
		{
			name: "a shared setter differing in both value and raises keeps both atoms",
			in: `{set x(self, v: number) -> undefined throws "a"} & ` +
				`{set x(self, v: string) -> undefined throws "b"}`,
			want: `{set x(self, value: number) throws "a"} & {set x(self, value: string) throws "b"}`,
		},
		{
			name: "a shared constructor joins its domain over a shared return",
			in:   "{new (x: number) -> boolean} & {new (x: string) -> boolean}",
			want: "{new (x: number | string) -> boolean}",
		},
		{
			name: "an identical overload set fuses without meeting the arms",
			in: "{a: number, foo(self, x: number) -> number, foo(self, x: string) -> string, ...} & " +
				"{b: number, foo(self, x: number) -> number, foo(self, x: string) -> string, ...}",
			want: "{a: number, b: number, foo(self, x: number) -> number; foo(self, x: string) -> string, ...}",
		},
		{
			name: "an exact object meets an inexact one requiring a member it caps out: never",
			in:   "{foo(self) -> number} & {bar(self) -> number, ...}",
			want: "never",
		},
		{
			name: "an exact object meeting an inexact subset closes the result",
			in:   "{bar(self) -> number, foo(self) -> number} & {foo(self) -> number, ...}",
			want: "{bar(self) -> number, foo(self) -> number}",
		},
		{
			name: "two inexact objects meet a shared method and keep disjoint fields open",
			in: "{a: number, foo(self) -> number | string, ...} & " +
				"{b: number, foo(self) -> string | boolean, ...}",
			want: "{a: number, b: number, foo(self) -> string, ...}",
		},
		{
			name: "a method and a property sharing a name keep both atoms",
			in:   "{foo(self) -> number} & {foo: number}",
			want: "{foo: number} & {foo(self) -> number}",
		},
		{
			name: "a readonly and a writable field of one name keep both atoms",
			in:   "{readonly x: number} & {x: number}",
			want: "{x: number} & {readonly x: number}",
		},
		{
			name: "an exact object meets an exact object lacking its method: never",
			in:   "{foo(self) -> number} & {bar(self) -> number}",
			want: "never",
		},
		{
			name: "a get/set accessor pair keeps both atoms unfused",
			in: "{a: number, get x(self) -> number, set x(self, v: number) -> undefined} & " +
				"{b: number, get x(self) -> number, set x(self, v: number) -> undefined}",
			want: "{a: number, get x(self) -> number, set x(self, value: number)} & " +
				"{b: number, get x(self) -> number, set x(self, value: number)}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Context{}
			require.Equal(t, tt.want, normDNF(c, parseType(t, tt.in)))
		})
	}
}

// TestObjectMemberJoin exercises joinObjects over member-carrying objects.
func TestObjectMemberJoin(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "objects agreeing but for one property widen it, keeping shared methods",
			in:   "{x: number, foo(self) -> number} | {x: string, foo(self) -> number}",
			want: "{foo(self) -> number, x: number | string}",
		},
		{
			name: "objects differing only in a getter widen it",
			in:   "{get x(self) -> number, foo(self) -> number} | {get x(self) -> string, foo(self) -> number}",
			want: "{foo(self) -> number, get x(self) -> number | string}",
		},
		{
			name: "two inexact objects differing in one property widen it and stay open",
			in:   "{x: number, foo(self) -> number, ...} | {x: string, foo(self) -> number, ...}",
			want: "{foo(self) -> number, x: number | string, ...}",
		},
		{
			name: "a method-carrying object absorbs its own open version at the open one",
			in:   "{foo(self) -> number} | {foo(self) -> number, ...}",
			want: "{foo(self) -> number, ...}",
		},
		{
			name: "objects differing in a method keep both atoms",
			in:   "{foo(self) -> number} | {foo(self) -> string}",
			want: "{foo(self) -> number} | {foo(self) -> string}",
		},
		{
			name: "objects differing in a setter keep both atoms",
			in:   "{set x(self, v: number) -> undefined} | {set x(self, v: string) -> undefined}",
			want: "{set x(self, value: number)} | {set x(self, value: string)}",
		},
		{
			name: "objects differing in a constructor keep both atoms",
			in:   "{new () -> number} | {new () -> string}",
			want: "{new () -> number} | {new () -> string}",
		},
		{
			name: "a readonly and a writable field of one name keep both atoms",
			in:   "{readonly x: number} | {x: string}",
			want: "{x: string} | {readonly x: number}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Context{}
			require.Equal(t, tt.want, normDNF(c, parseType(t, tt.in)))
		})
	}
}

// TestIndexSignatureObjectStaysUnfused guards the scope line of #1103: a mapped
// member such as an index signature has no per-member merge rule, so an object
// carrying one is kept as its own atom. parseType does not lower a mapped member,
// so this builds the object directly. A settled index signature is not a residual,
// so this checks fusableMember rather than the residual test alone keeps it out.
func TestIndexSignatureObjectStaysUnfused(t *testing.T) {
	c := &Context{}
	// A settled index signature is `[K: string]?: number`, an optional member over an
	// uncountable key set. MappedElemSettled recognizes it only when Optional is ModAdd.
	// The non-optional `[K: string]: number` is uninhabited, so it stays an unsettled
	// residual instead, which the residual check already keeps out.
	withIndexSig := func() *soltype.ObjectType {
		return &soltype.ObjectType{Elems: []soltype.ObjTypeElem{
			propElem("a", num()),
			&soltype.MappedElem{Key: &soltype.MappedKeyType{ID: 0, Name: "K"}, Keys: str(), Value: num(), Optional: soltype.ModAdd},
		}}
	}
	require.True(t, soltype.MappedElemSettled(withIndexSig().Elems[1].(*soltype.MappedElem)),
		"the index signature must be settled for this test to exercise fusableMember")
	_, ok := c.meetObjects(withIndexSig(), withIndexSig())
	require.False(t, ok, "meetObjects keeps an index-signature object unfused")
	_, ok = c.joinObjects(withIndexSig(), withIndexSig())
	require.False(t, ok, "joinObjects keeps an index-signature object unfused")
}

// TestStaticMethodMismatchStaysUnfused covers meetMethods' Static bail. A static
// method lives on the constructor value and an instance method on the instance, so
// two methods of one name that disagree on Static are not the same member and the
// objects stay apart. Object type annotations have no static-member syntax, so this
// builds the objects directly.
func TestStaticMethodMismatchStaysUnfused(t *testing.T) {
	c := &Context{}
	method := func(static bool) *soltype.ObjectType {
		return &soltype.ObjectType{Elems: []soltype.ObjTypeElem{
			&soltype.MethodElem{Name: "foo", Static: static, Signatures: []*soltype.FuncType{{Ret: num()}}},
		}}
	}
	_, ok := c.meetObjects(method(true), method(false))
	require.False(t, ok, "a static and an instance method of one name keep both atoms")
}
