package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestUnionFieldWrite covers field writes through a receiver whose type is a union. Each
// form of holding the value reports UnionFieldWriteError, naming the first member whose field
// rejects the written value, or the union when every member accepts it. An immutable binding,
// an immutable borrow and a readonly field report their own diagnostic instead. A write
// through a narrowed binding, a whole-binding reassignment, a write to a field some member
// lacks, and a write through a variable that joins several values keep the behaviour they
// have without a union receiver.
func TestUnionFieldWrite(t *testing.T) {
	const decls = `type A = {kind: "a", a: number}
type B = {kind: "b", b: string}
type U = A | B
type C = {kind: "c", a: number}
type OA = {kind: "a", v: number}
type OB = {kind: "b", v: string}
declare val ns: number | string
`
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "OwnedLocalFromLiteral",
			src: `fn f() {
    val mut u: U = {kind: "a", a: 1}
    u.kind = "b"
}`,
			want: []string{"10:5-10:11: cannot assign \"b\" to 'u.kind', since 'u' may hold an 'A', whose 'kind' is \"a\"; narrow 'u' with a `match` and write through the narrowed binding"},
		},
		{
			name: "OwnedLocalFromParam",
			src: `fn f(init: U) -> U {
    val mut u: U = init
    u.kind = "b"
    return u
}`,
			want: []string{"10:5-10:11: cannot assign \"b\" to 'u.kind', since 'u' may hold an 'A', whose 'kind' is \"a\"; narrow 'u' with a `match` and write through the narrowed binding"},
		},
		{
			name: "UnannotatedLocal",
			src: `fn f(init: U) {
    val mut u = init
    u.kind = "b"
}`,
			want: []string{"10:5-10:11: cannot assign \"b\" to 'u.kind', since 'u' may hold an 'A', whose 'kind' is \"a\"; narrow 'u' with a `match` and write through the narrowed binding"},
		},
		{
			name: "MutBorrowParam",
			src:  `fn f(q: &mut U) { q.kind = "b" }`,
			want: []string{"8:19-8:25: cannot assign \"b\" to 'q.kind', since 'q' may hold an 'A', whose 'kind' is \"a\"; narrow 'q' with a `match` and write through the narrowed binding"},
		},
		{
			name: "MutBorrowParamNamesSecondMember",
			src:  `fn f(q: &mut U) { q.kind = "a" }`,
			want: []string{"8:19-8:25: cannot assign \"a\" to 'q.kind', since 'q' may hold a 'B', whose 'kind' is \"b\"; narrow 'q' with a `match` and write through the narrowed binding"},
		},
		{
			name: "InlineUnion",
			src:  `fn f(q: &mut ({kind: "a", n: number} | {kind: "b", n: number})) { q.kind = "b" }`,
			want: []string{"8:67-8:73: cannot assign \"b\" to 'q.kind', since 'q' may hold a '{kind: \"a\", n: number}', whose 'kind' is \"a\"; narrow 'q' with a `match` and write through the narrowed binding"},
		},
		{
			name: "Nested",
			src: `fn f(init: {inner: U}) {
    val mut h = init
    h.inner.kind = "b"
}`,
			want: []string{"10:5-10:17: cannot assign \"b\" to 'h.inner.kind', since 'h.inner' may hold an 'A', whose 'kind' is \"a\"; narrow 'h.inner' with a `match` and write through the narrowed binding"},
		},
		{
			name: "NestedThroughBorrow",
			src:  `fn f(h: &mut {inner: U}) { h.inner.kind = "b" }`,
			want: []string{"8:28-8:40: cannot assign \"b\" to 'h.inner.kind', since 'h.inner' may hold an 'A', whose 'kind' is \"a\"; narrow 'h.inner' with a `match` and write through the narrowed binding"},
		},
		{
			name: "ConstantStringIndex",
			src: `fn f(init: U) {
    val mut u: U = init
    u["kind"] = "b"
}`,
			want: []string{"10:5-10:14: cannot assign \"b\" to 'u[\"kind\"]', since 'u' may hold an 'A', whose 'kind' is \"a\"; narrow 'u' with a `match` and write through the narrowed binding"},
		},
		{
			name: "ArrayElement",
			src:  `fn f(mut arr: Array<U>) { arr[0].kind = "b" }`,
			want: []string{"8:27-8:38: cannot assign \"b\" to 'arr[0].kind', since 'arr[0]' may hold an 'A', whose 'kind' is \"a\"; narrow 'arr[0]' with a `match` and write through the narrowed binding"},
		},
		{
			name: "WiderValueIntoSharedField",
			src: `fn f(init: OA | OB) -> OA | OB {
    val mut o: OA | OB = init
    o.v = ns
    return o
}`,
			want: []string{"10:5-10:8: cannot assign number | string to 'o.v', since 'o' may hold an 'OA', whose 'v' is number; narrow 'o' with a `match` and write through the narrowed binding"},
		},
		{
			name: "EveryMemberAccepts",
			src: `fn f(init: A | C) {
    val mut u: A | C = init
    u.a = 2
}`,
			want: []string{"10:5-10:8: cannot assign to 'u.a', since 'u' has the union type 'A | C' and a write through it could leave the value matching none of its members; narrow 'u' with a `match` and write through the narrowed binding"},
		},
		{
			name: "OptionalReceiverIsNotAUnionWrite",
			src: `fn f(init: A | undefined) {
    val mut u: A | undefined = init
    u.a = 2
}`,
			want: []string{"10:5-10:12: cannot constrain undefined <: object"},
		},
		{
			name: "NullableReceiverIsNotAUnionWrite",
			src: `fn f(init: A | null) {
    val mut u: A | null = init
    u.a = 2
}`,
			want: []string{"10:5-10:12: cannot constrain null <: object"},
		},
		{
			name: "ImmutableBindingReportsOnlyTheBinding",
			src: `fn f(init: U) {
    val u: U = init
    u.kind = "b"
}`,
			want: []string{"10:5-10:17: cannot mutate 'u' because its binding is immutable; declare it as `val mut u`"},
		},
		{
			name: "ImmutableBorrowReportsTheBorrow",
			src:  `fn f(q: &U) { q.kind = "b" }`,
			want: []string{"8:9-8:11: cannot constrain immutable U <: mutable object"},
		},
		{
			name: "MissingFieldOnSomeMember",
			src: `fn f(init: U) {
    val mut u: U = init
    u.a = 2
}`,
			want: []string{"2:10-2:32: object is missing property: a"},
		},
		{
			name: "ReadonlyMemberField",
			src: `type RA = {readonly kind: "a"}
type RB = {readonly kind: "b"}
fn f(q: &mut (RA | RB)) { q.kind = "a" }`,
			want: []string{"10:27-10:33: cannot assign to 'kind' because it is a readonly property"},
		},
		{
			name: "JoinedVariableIsNotAUnionWrite",
			src: `fn f(p: {x: {a: number}} | {y: string}) {
    val {mut x} = p else { {x: {a: 1}} }
    x.a = 2
}`,
			want: nil,
		},
		{
			name: "JoinOfDeclaredMembers",
			src: `fn f(c: boolean, a: A, b: B) {
    val mut x = if c { a } else { b }
    x.kind = "b"
}`,
			want: []string{"10:5-10:11: cannot assign \"b\" to 'x.kind', since 'x' may hold an 'A', whose 'kind' is \"a\"; narrow 'x' with a `match` and write through the narrowed binding"},
		},
		{
			name: "JoinOfFreshLiterals",
			src: `fn f(c: boolean) {
    val mut x = if c { {kind: "a", a: 1} } else { {kind: "b", b: "s"} }
    x.kind = "c"
}`,
			want: []string{"10:5-10:11: cannot assign to 'x.kind', since 'x' has the union type '{kind: string, a: number} | {kind: string, b: string}' and a write through it could leave the value matching none of its members; narrow 'x' with a `match` and write through the narrowed binding"},
		},
		{
			name: "JoinOfClassInstances",
			src: `class Foo { n: number }
class Bar { n: number }
fn f(c: boolean) {
    val mut x = if c { Foo(1) } else { Bar(2) }
    x.n = 3
}`,
			want: []string{"12:5-12:8: cannot assign to 'x.n', since 'x' has the union type 'Foo | Bar' and a write through it could leave the value matching none of its members; narrow 'x' with a `match` and write through the narrowed binding"},
		},
		{
			name: "JoinOfSameShape",
			src: `fn f(c: boolean, p: {a: number}) {
    val mut x = if c { p } else { {a: 1} }
    x.a = 2
}`,
			want: nil,
		},
		{
			name: "NarrowedBindingWrite",
			src: `fn f(q: &mut U) {
    match q {
        v: &mut A => { v.a = 2 },
        _ => {},
    }
}`,
			want: nil,
		},
		{
			name: "NonUnionReceiver",
			src:  `fn f(q: &mut A) { q.a = 2 }`,
			want: nil,
		},
		{
			name: "WholeBindingReassignment",
			src: `fn f(init: U) {
    var u: U = init
    u = {kind: "b", b: "x"}
}`,
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, decls+tt.src)
			require.Equal(t, tt.want, messagesWithSpan(t, errs))
		})
	}
}
