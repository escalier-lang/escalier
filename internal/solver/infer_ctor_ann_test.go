package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// --- M9 PR15: `new (…) -> T` members in object type annotations ---

// A `new (…) -> T` member resolves to the same ConstructorElem a class value carries, and the
// soltype printer renders it back as `new (…) -> T`. The signature accepts everything a `fn`
// annotation does, since both parse and resolve through the same path. The BesideProperty case
// shows the one way the rendering departs from the source, which the next test explains.
func TestInferConstructorTypeAnnRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"OneParam", `type Result = {new (x: number) -> {a: number}}`, "{new (x: number) -> {a: number}}"},
		{"NoParams", `type Result = {new () -> {a: number}}`, "{new () -> {a: number}}"},
		{
			"BesideProperty",
			`type Result = {new (x: number) -> {a: number}, origin: number}`,
			"{origin: number, new (x: number) -> {a: number}}",
		},
		{
			"RestParam",
			`type Result = {new (...args: [number, string]) -> {a: number}}`,
			"{new (...args: [number, string]) -> {a: number}}",
		},
		{
			"Throws",
			`type Result = {new (x: number) -> {a: number} throws string}`,
			"{new (x: number) -> {a: number} throws string}",
		},
		{
			"Inexact",
			`type Result = {new (x: number) -> {a: number}, ...}`,
			"{new (x: number) -> {a: number}, ...}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, _, errs := inferTypeNodes(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, soltype.Print(nodes["Result"]))
		})
	}
}

// A construct signature is unnamed, so the dedup builder has no key to file it under and appends
// it after the properties. That ordering is why BesideProperty above renders the signature last
// while the source wrote it first. A generic signature keeps its own type parameters, which the
// enclosing annotation does not bind.
func TestInferConstructorTypeAnnGeneric(t *testing.T) {
	nodes, _, errs := inferTypeNodes(t, `type Result = {new <T>(x: T) -> {a: T}}`)
	require.Empty(t, errs)
	require.Equal(t, "{new <T>(x: T) -> {a: T}}", soltype.Print(nodes["Result"]))
}

// A type named inside a construct signature is a dependency of the enclosing alias, so the
// declaration order in the source does not matter. ObjectTypeAnn.Accept already walks a
// ConstructorTypeAnn's signature, which is what puts the reference in the dependency graph.
func TestInferConstructorTypeAnnForwardReference(t *testing.T) {
	nodes, _, errs := inferTypeNodes(t, `
		type Result = {new (x: Arg) -> Made}
		type Arg = number
		type Made = {a: number}
	`)
	require.Empty(t, errs)
	require.Equal(t, "{new (x: Arg) -> Made}", soltype.Print(nodes["Result"]))
}

// soltype.ObjectType.Constructor() returns at most one construct signature and constrain checks a
// requirement against that one, so a second `new` member is reported rather than dropped in
// silence. The object still carries the first signature, so the annotation stays usable.
func TestInferDuplicateConstructorTypeAnnRejected(t *testing.T) {
	nodes, _, errs := inferTypeNodes(t, `
		type Result = {new (x: number) -> {a: number}, new (y: string) -> {a: number}}
	`)
	require.Len(t, errs, 1)
	require.IsType(t, &DuplicateConstructorSignatureError{}, errs[0])
	require.Equal(t, "An object type may declare at most one `new` signature.", errs[0].Message())
	require.Equal(t, "{new (x: number) -> {a: number}}", soltype.Print(nodes["Result"]))
}

// A `...A` spread puts the object on the ordered path, where a construct signature keeps its
// source position rather than being appended. The spread's operand grounds here, so the member
// list merges and the signature survives the merge.
func TestInferConstructorTypeAnnWithSpread(t *testing.T) {
	nodes, ctx, errs := inferTypeNodes(t, `
		type Base = {origin: number}
		type Result = {new (x: number) -> {a: number}, ...Base}
	`)
	require.Empty(t, errs)
	require.Equal(t, "{new (x: number) -> {a: number}, origin: number}",
		soltype.Print(expandResidual(ctx, nodes["Result"])))
}

// The spread merge folds two operands together under the member they overlap on. A construct
// signature is unnamed and a property key may itself be the empty string, so both would answer
// `""` if the merge keyed on the name alone. They are distinct members, so an object carrying
// both keeps both, in either written order.
func TestInferConstructorTypeAnnAndEmptyStringKeyBothSurvive(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			"SignatureFirst",
			`type Result = {new () -> number, "": string, ...Base}`,
			`{new () -> number, "": string, q: number}`,
		},
		{
			"PropertyFirst",
			`type Result = {"": string, new () -> number, ...Base}`,
			`{"": string, new () -> number, q: number}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, ctx, errs := inferTypeNodes(t, "type Base = {q: number}\n"+tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, soltype.Print(expandResidual(ctx, nodes["Result"])))
		})
	}
}

// keyofObject projects property, getter, and setter names. A construct signature is unnamed, so it
// contributes no key at all rather than an empty-string one, and `keyof {new (…) -> T, origin: N}`
// is just `"origin"`. A signature-only object has an empty key set, which is `never`.
func TestInferKeyofConstructorCarryingObject(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			"SignatureIsSkipped",
			`type Result = keyof {new (x: number) -> {a: number}, origin: number}`,
			`"origin"`,
		},
		{
			"SignatureOnlyHasNoKeys",
			`type Result = keyof {new (x: number) -> {a: number}}`,
			"never",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, ctx, errs := inferTypeNodes(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, soltype.Print(expandResidual(ctx, nodes["Result"])))
		})
	}
}

// A class value fills an object annotation that names a construct signature, which is what
// `InstanceType<typeof C>` relies on. Both cases go through the same object-against-object arm,
// since classValue gives every class an object carrying a ConstructorElem. The class with
// statics needs an inexact target, because its statics are extra members against an exact one.
func TestInferClassValueSatisfiesConstructorAnnotation(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{
			"NoStatics",
			`
				class Point {
					x: number,
					constructor(mut self, x: number) { self.x = x },
				}
				val ctor: {new (x: number) -> Point} = Point
			`,
		},
		{
			"WithStatics",
			`
				class Counter {
					n: number,
					static zero: number = 0,
					constructor(mut self, n: number) { self.n = n },
				}
				val ctor: {new (n: number) -> Counter, ...} = Counter
			`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
		})
	}
}

// A target that names a static the class does not declare reports the missing member, since a
// class value is an object and this is ordinary object subtyping.
func TestInferClassValueMissesConstructorTargetWithStatics(t *testing.T) {
	_, _, errs := inferSource(t, `
		class Point {
			x: number,
			constructor(mut self, x: number) { self.x = x },
		}
		val ctor: {new (x: number) -> Point, origin: Point} = Point
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "object is missing property: origin", errs[0].Message())
}

// A plain function never fills a target that names a construct signature. Only a class value
// carries a ConstructorElem, and a FuncType records nothing about constructibility, so accepting
// one here would make every function a constructor.
func TestInferPlainFunctionMissesConstructorTarget(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn make(x: number) -> {a: number} { return {a: x} }
		val ctor: {new (x: number) -> {a: number}} = make
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "cannot constrain function <: object", errs[0].Message())
}
