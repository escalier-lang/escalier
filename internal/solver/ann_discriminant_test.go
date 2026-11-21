package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// annTestMsg returns the AnnotationTestError message for an annotation that resolves to the
// type rendered as ann.
func annTestMsg(ann string) string {
	return "no runtime test tells `" + ann + "` apart from the scrutinee's other members. " +
		"Give each object type a property such as `kind` with a distinct literal type."
}

// TestObjectAnnotationNeedsARuntimeTest covers narrowing annotations that name an object
// type at the root of a `match`, an `if val`, and a `val … else`. Such an annotation is
// accepted when a property with literal types tells its values apart from the scrutinee's
// other members, or when nothing needs telling apart. Otherwise it is reported.
func TestObjectAnnotationNeedsARuntimeTest(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		wantErrs []string
	}{
		{
			name: "a discriminant field tells the members apart",
			src: `type A = {kind: "a", a: number}
type B = {kind: "b", b: string}
declare val u: &(A | B)
val r = match u {
	x: &B => x.b,
	y: &A => y.a,
}`,
		},
		{
			name: "an annotation naming a union of members",
			src: `type A = {kind: "a", a: number}
type B = {kind: "b", b: number}
type C = {kind: "c", c: string}
declare val u: A | B | C
val r = match u {
	x: A | B => 1,
	y: C => 2,
}`,
		},
		{
			name: "null, undefined, and number members beside the objects",
			src: `type A = {kind: "a", a: number}
type B = {kind: "b", b: string}
declare val u: A | B | null | undefined | number
val r = match u {
	x: B => 1,
	y: A => 2,
	_ => 3,
}`,
		},
		{
			name: "literal members beside the objects",
			src: `type A = {kind: "a", a: number}
type B = {kind: "b", b: string}
declare val u: A | B | 0 | true | "none"
val r = match u {
	x: B => 1,
	y: A => 2,
	_ => 3,
}`,
		},
		{
			name: "a symbol member beside a discriminant other than description",
			src: `type A = {kind: "a", a: number}
declare val u: A | symbol
val r = match u {
	x: A => 1,
	_ => 2,
}`,
		},
		{
			name: "a discriminant declared after a method",
			src: `type A = {area() -> number, kind: "a"}
type B = {area() -> number, kind: "b"}
declare val u: A | B
val r = match u {
	x: B => 1,
	y: A => 2,
}`,
		},
		{
			name: "a class member declaring a distinct literal",
			src: `class Circle { kind: "circle", r: number }
type Square = {kind: "square", w: number}
declare val s: Circle | Square
val r = match s {
	q: Square => q.w,
	_ => 0,
}`,
		},
		{
			name: "an annotation admitting the whole scrutinee",
			src: `type A = {kind: "a", a: number}
declare val u: A
val r = match u {
	x: A => x.a,
}`,
		},
		{
			// An `unknown` scrutinee lists no members, so codegen tests the annotation's
			// own shape instead.
			name: "an unknown scrutinee",
			src: `declare val v: unknown
val r = match v {
	x: {title: string} => x.title,
	_ => "",
}`,
		},
		{
			name: "a scrutinee still being inferred",
			src: `fn f(v) {
	return match v {
		x: {x: number} => 1,
		_ => 0,
	}
}`,
		},
		{
			name: "members told apart only by shape",
			src: `declare val v: {x: number} | {y: string}
val r = match v {
	a: {x: number} => 1,
	b: {y: string} => 2,
}`,
			wantErrs: []string{
				"3:5-3:16: " + annTestMsg("{x: number}"),
				"4:5-4:16: " + annTestMsg("{y: string}"),
			},
		},
		{
			name: "members sharing a literal",
			src: `type A = {kind: "a", n: number}
type B = {kind: "a", s: string}
declare val v: A | B
val r = match v {
	x: A => 1,
	_ => 2,
}`,
			wantErrs: []string{"5:5-5:6: " + annTestMsg("A")},
		},
		{
			name: "a member lacking the discriminant",
			src: `type A = {kind: "a", n: number}
type B = {kind: "b", s: string}
type C = {n: number, s: string}
declare val v: A | B | C
val r = match v {
	x: A => 1,
	_ => 2,
}`,
			wantErrs: []string{"6:5-6:6: " + annTestMsg("A")},
		},
		{
			name: "an optional discriminant",
			src: `type A = {kind?: "a", n: number}
type B = {kind: "b", s: string}
declare val v: A | B
val r = match v {
	x: A => 1,
	_ => 2,
}`,
			wantErrs: []string{"5:5-5:6: " + annTestMsg("A")},
		},
		{
			// `"ab".length` reads `2`, so a string passes a test of `length`.
			name: "a string member beside a length discriminant",
			src: `declare val v: {length: 2} | string
val r = match v {
	x: {length: 2} => 1,
	_ => 2,
}`,
			wantErrs: []string{"3:5-3:16: " + annTestMsg("{length: 2}")},
		},
		{
			// `Symbol("x").description` reads `"x"`, so a symbol passes a test of
			// `description`.
			name: "a symbol member beside a description discriminant",
			src: `declare val v: {description: "x"} | symbol
val r = match v {
	o: {description: "x"} => 1,
	_ => 2,
}`,
			wantErrs: []string{"3:5-3:23: " + annTestMsg(`{description: "x"}`)},
		},
		{
			name: "an if val annotation",
			src: `declare val v: {x: number} | {y: string}
val r = if val a: {x: number} = v { 1 } else { 2 }`,
			wantErrs: []string{"2:19-2:30: " + annTestMsg("{x: number}")},
		},
		{
			name: "a val else annotation",
			src: `declare val v: {x: number} | {y: string}
fn f() -> number {
	val a: {x: number} = v else { return 0 }
	return a.x
}`,
			wantErrs: []string{"3:9-3:20: " + annTestMsg("{x: number}")},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, errs := inferSource(t, test.src)
			require.Equal(t, test.wantErrs, messagesWithSpan(t, errs))
			// The error blames the annotation alone and names no related node.
			for _, err := range errs {
				if annErr, isAnnErr := err.(*AnnotationTestError); isAnnErr {
					require.Empty(t, annErr.Related())
				}
			}
		})
	}
}
