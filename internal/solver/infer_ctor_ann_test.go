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
// `InstanceType<typeof C>` relies on. classValue gives a class with no statics its bare
// constructor FuncType, so the sub is a function rather than an object, and constrain's
// constructor-only rule is what accepts it. A class with statics is already an object carrying a
// ConstructorElem, and it needs an inexact target since its statics are extra members.
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

// The constructor-only rule fires only when the signature is the target's whole member list. A
// target that also names a static demands a member a bare function cannot supply, so it stays on
// the object-against-object arm and reports the miss there.
func TestInferBareFunctionMissesConstructorTargetWithStatics(t *testing.T) {
	_, _, errs := inferSource(t, `
		class Point {
			x: number,
			constructor(mut self, x: number) { self.x = x },
		}
		val ctor: {new (x: number) -> Point, origin: Point} = Point
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "cannot constrain function <: object", errs[0].Message())
}
