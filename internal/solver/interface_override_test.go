package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestInterfaceOverride covers an interface that redeclares a member one of its parents
// declares. An override the parent's member does not admit is reported at the redeclared
// member, and a compatible one is accepted.
func TestInterfaceOverride(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "IncompatibleField",
			src: `interface Base { v: number }
interface Seq extends Base { v: string }
fn use(s: Seq) { return s.v }`,
			want: []string{"2:30-2:39: interface `Seq` redeclares inherited member `v` with type `string`, " +
				"which is not compatible with `number` declared by `Base`"},
		},
		{
			name: "IncompatibleMethod",
			src: `interface Base { m(&self) -> number }
interface Seq extends Base { m(&self) -> string }
fn use(s: Seq) { return s.m() }`,
			want: []string{"2:30-2:48: interface `Seq` redeclares inherited member `m` with type `fn () -> string`, " +
				"which is not compatible with `fn () -> number` declared by `Base`"},
		},
		{
			name: "IncompatibleSymbolIterator",
			src: `interface Base { [Symbol.iterator]() -> Iterator<number> }
interface Seq extends Base { [Symbol.iterator]() -> Iterator<string> }
fn use(s: Seq) { for x in s { return x } }`,
			want: []string{"2:30-2:69: interface `Seq` redeclares inherited member `[Symbol.iterator]` with type " +
				"`fn () -> Iterator<string, undefined, unknown>`, which is not compatible with " +
				"`fn () -> Iterator<number, undefined, unknown>` declared by `Base`"},
		},
		{
			name: "UnsoundUpcast",
			src: `interface Base { v: number }
interface Seq extends Base { v: string }
declare val s: Seq
val b: Base = s
val n: number = b.v`,
			want: []string{"2:30-2:39: interface `Seq` redeclares inherited member `v` with type `string`, " +
				"which is not compatible with `number` declared by `Base`"},
		},
		{
			name: "IncompatibleGetter",
			src: `interface Base { get v(&self) -> number }
interface Seq extends Base { get v(&self) -> string }`,
			want: []string{"2:30-2:52: interface `Seq` redeclares inherited member `v` with type `string`, " +
				"which is not compatible with `number` declared by `Base`"},
		},
		{
			name: "IncompatibleGrandparentMember",
			src: `interface Base { v: number }
interface Mid extends Base { w: number }
interface Seq extends Mid { v: string }`,
			want: []string{"3:29-3:38: interface `Seq` redeclares inherited member `v` with type `string`, " +
				"which is not compatible with `number` declared by `Base`"},
		},
		{
			name: "IncompatibleGenericParent",
			src: `interface Base<T> { v: T }
interface Seq<T> extends Base<T> { v: string }`,
			want: []string{"2:36-2:45: interface `Seq` redeclares inherited member `v` with type `string`, " +
				"which is not compatible with `T` declared by `Base<T>`"},
		},
		{
			name: "IncompatibleWithEitherGrandparent",
			src: `interface A { readonly v: number }
interface B { readonly v: number | string }
interface Mid extends A, B {}
interface Seq extends Mid { readonly v: string }`,
			want: []string{"4:29-4:47: interface `Seq` redeclares inherited member `v` with type `string`, " +
				"which is not compatible with `number` declared by `A`"},
		},
		{
			name: "IncompatibleClassGrandparent",
			src: `class P { v: number }
interface Mid extends P {}
interface Seq extends Mid { v: string }`,
			want: []string{"3:29-3:38: interface `Seq` redeclares inherited member `v` with type `string`, " +
				"which is not compatible with `number` declared by `P`"},
		},
		{
			name: "DeclaredInterface",
			src: `declare interface Base { v: number }
declare interface Seq extends Base { v: string }`,
			want: []string{"2:38-2:47: interface `Seq` redeclares inherited member `v` with type `string`, " +
				"which is not compatible with `number` declared by `Base`"},
		},
		{
			name: "ReadonlyOverWritable",
			src: `interface Base { v: number }
interface Seq extends Base { readonly v: number }`,
			want: []string{"2:30-2:48: interface `Seq` redeclares inherited member `v` as a readonly field, " +
				"but `Base` declares it as a writable field"},
		},
		{
			name: "NarrowedReadonlyField",
			src: `interface Base { readonly v: number | string }
interface Seq extends Base { readonly v: string }
fn use(s: Seq) -> string { return s.v }`,
			want: []string{},
		},
		{
			name: "NarrowedMethodReturn",
			src: `interface Base { m(&self) -> number | string }
interface Seq extends Base { m(&self) -> string }`,
			want: []string{},
		},
		{
			name: "SameType",
			src: `interface Base { v: number }
interface Seq extends Base { v: number }`,
			want: []string{},
		},
		{
			name: "GenericParentAtArgument",
			src: `interface Base<T> { v: T }
interface Seq extends Base<string> { v: string }`,
			want: []string{},
		},
		{
			name: "SiblingDeclaredLater",
			src: `interface Seq extends Base { v: string }
interface Base { v: number, s: Seq }`,
			want: []string{"1:30-1:39: interface `Seq` redeclares inherited member `v` with type `string`, " +
				"which is not compatible with `number` declared by `Base`"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, _, errs := inferSource(t, tt.src)
			got := messagesWithSpan(t, errs)
			if got == nil {
				got = []string{}
			}
			require.Equal(t, tt.want, got)
		})
	}
}
