package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// --- M5 D1: nominal patterns (InstancePat / ExtractorPat) ---

// An instance pattern `Point { x, y }` in a match arm deconstructs a class instance
// through the class's projected member view, binding each named field at its member
// type. The arm body reads the bound names back, so the inferred return shows the
// binding worked.
func TestInferInstancePatBindsFields(t *testing.T) {
	values, _, errs := inferSource(t, `
		class Point { x: number, y: number }
		fn f(p: Point) {
			return match p {
				Point { x, y } => [x, y]
			}
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: Point) -> [number, number]", values["f"])
}

// An instance pattern in a `val` binds the class instance's fields the same way a bare
// object pattern does, since both dispatch through member lookup.
func TestInferInstancePatValDestructure(t *testing.T) {
	values, _, errs := inferSource(t, `
		class Point { x: number, y: number }
		fn f(p: Point) {
			val Point { x, y } = p
			return [x, y]
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: Point) -> [number, number]", values["f"])
}

// An extractor pattern `Point(x, y)` binds each argument sub-pattern against the matching
// constructor parameter's type. This is the M5 interim: the extractor protocol is the
// instance's `[Symbol.customMatcher]` method, which needs symbol-keyed members and lands in
// M7. Point's synthesized constructor parameters are its fields, so `x` and `y` bind at
// `number`, the same shape a custom matcher returning `[x, y]` would produce.
func TestInferExtractorPatBindsArgs(t *testing.T) {
	values, _, errs := inferSource(t, `
		class Point { x: number, y: number }
		fn f(p: Point) {
			return match p {
				Point(x, y) => [x, y]
			}
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: Point) -> [number, number]", values["f"])
}

// DISABLED until M7. An extractor pattern deconstructs through the class instance's
// `[Symbol.customMatcher]` method, not its constructor. M5 has no symbol-keyed members, so
// bindExtractorPat binds against constructor parameters as an interim. This case pins that
// interim by extracting `label`, a constructor parameter that is never stored on the
// instance and so is not recoverable from an instance value. Under M7's `[Symbol.customMatcher]`
// resolution, `Celsius` declares no custom matcher, so the match is rejected outright.
// Re-enable when M7 lands and assert the missing-custom-matcher error the commented body
// records.
func TestInferExtractorPatExplicitConstructor(t *testing.T) {
	/*
		_, _, errs := inferSource(t, `
			class Celsius {
				degrees: number,
				constructor(mut self, label: string, degrees: number) {
					self.degrees = degrees
				},
			}
			fn f(c: Celsius) {
				return match c {
					Celsius(label, degrees) => [label, degrees]
				}
			}
		`)
		require.Len(t, errs, 1)
		// M7: `Celsius` has no `[Symbol.customMatcher]` method, so it cannot be used as an
		// extractor pattern. The exact error type lands with the matcher resolution.
		require.Equal(t, "`Celsius` has no [Symbol.customMatcher] method", msgWithSpan(t, errs[0]))
	*/
}

// A generic class instance pattern projects the field at the instance's type argument, so
// a `Box<number>` scrutinee binds `value` at `number`.
func TestInferInstancePatGeneric(t *testing.T) {
	values, _, errs := inferSource(t, `
		class Box<T> { value: T }
		fn f(b: Box<number>) {
			return match b {
				Box { value } => value
			}
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (b: Box<number>) -> number", values["f"])
}

// A generic extractor pattern likewise binds the argument at the instance's type argument.
func TestInferExtractorPatGeneric(t *testing.T) {
	values, _, errs := inferSource(t, `
		class Box<T> { value: T }
		fn f(b: Box<string>) {
			return match b {
				Box(value) => value
			}
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (b: Box<string>) -> string", values["f"])
}

// A nested sub-pattern inside an instance pattern binds through the field's own shape: the
// field `pos` is an object, so `{x, y}` against it binds `x`/`y` at their nested types.
func TestInferInstancePatNested(t *testing.T) {
	values, _, errs := inferSource(t, `
		class Line { start: {x: number, y: number} }
		fn f(l: Line) {
			return match l {
				Line { start: {x, y} } => [x, y]
			}
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (l: Line) -> [number, number]", values["f"])
}

// An instance pattern may bind a SUBSET of the class's fields: each field requirement is
// inexact, the same as a bare object pattern, so an unmentioned field is tolerated.
func TestInferInstancePatOmitFields(t *testing.T) {
	values, _, errs := inferSource(t, `
		class Point { x: number, y: number }
		fn f(p: Point) {
			val Point { x } = p
			return x
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: Point) -> number", values["f"])
}

// An instance pattern may rename each field to a fresh binding via `field: name`, binding
// the new name at the field's member type.
func TestInferInstancePatRenameFields(t *testing.T) {
	values, _, errs := inferSource(t, `
		class Point { x: number, y: string }
		fn f(p: Point) {
			val Point { x: a, y: b } = p
			return [a, b]
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: Point) -> [number, string]", values["f"])
}

// An instance pattern naming a field the class lacks is a member-lookup miss, reported as
// a MissingPropertyError against the projected body.
func TestInferInstancePatWrongField(t *testing.T) {
	_, _, errs := inferSource(t, `
		class Point { x: number, y: number }
		fn f(p: Point) {
			return match p {
				Point { x, z } => x
			}
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "5:16-5:17: object is missing property: z", msgWithSpan(t, errs[0]))
}

// An extractor pattern whose argument count differs from the constructor's parameter count
// is an ExtractorPatternArityError.
func TestInferExtractorPatWrongArity(t *testing.T) {
	_, _, errs := inferSource(t, `
		class Point { x: number, y: number }
		fn f(p: Point) {
			return match p {
				Point(x) => x
			}
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "5:5-5:13: extractor pattern `Point` expects 2 arguments but got 1", msgWithSpan(t, errs[0]))
}

// An instance pattern whose name resolves to no class is an InstancePatternNotClassError.
// The inner fields still bind against a fresh var, so the arm body does not cascade into
// unknown-identifier errors. The bare `number` scrutinee is left non-exhaustive by the one
// invalid arm, so the coverage check names it too.
func TestInferInstancePatNotAClass(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f(p: number) {
			return match p {
				Missing { x } => x
			}
		}
	`)
	require.Equal(t, []string{
		"4:5-4:18: `Missing` does not name a class and cannot be used as an instance pattern.",
		"3:11-5:5: match is not exhaustive; `number` admits values no pattern names, so add a catch-all branch",
	}, messagesWithSpan(t, errs))
}

// An extractor pattern naming a value binding rather than a constructor is invalid, and that
// one invalid arm leaves the `number` scrutinee non-exhaustive, so the coverage check reports
// it too.
func TestInferExtractorPatNotAConstructor(t *testing.T) {
	_, _, errs := inferSource(t, `
		val g = 5
		fn f(p: number) {
			return match p {
				g(x) => x
			}
		}
	`)
	require.Equal(t, []string{
		"5:5-5:9: `g` is not a constructor and cannot be used as an extractor pattern.",
		"4:11-6:5: match is not exhaustive; `number` admits values no pattern names, so add a catch-all branch",
	}, messagesWithSpan(t, errs))
}

// An instance pattern narrows the scrutinee to the named class. A scrutinee that is a
// different, unrelated class cannot be that class, so the assertion is a nominal mismatch.
func TestInferInstancePatNominalMismatch(t *testing.T) {
	_, _, errs := inferSource(t, `
		class Point { x: number, y: number }
		class Circle { radius: number }
		fn f(c: Circle) {
			return match c {
				Point { x, y } => [x, y]
			}
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "6:5-6:19: cannot constrain Point <: Circle", msgWithSpan(t, errs[0]))
}

// --- M5 D2: nominal union exhaustiveness ---
//
// An enum type is the union of its variant handles, so a match over an enum reaches the
// union exhaustiveness path. An extractor pattern covers a variant member when it names
// that variant and binds its arguments, so an arm per variant makes the match exhaustive
// with no catch-all. The variant name resolves through the enum's namespace, so
// `Color.RGB` finds the `Color.RGB` variant handle.

// Exhaustiveness of a match over an enum. Each row shares the same two-variant `Color`
// enum and varies only the arms. A row with wantVal expects a clean inference; a row with
// wantErr expects that single error. The cases: an arm per variant is exhaustive with no
// catch-all and binds each variant's fields; leaving a variant uncovered is non-exhaustive;
// a catch-all covers the remaining variants; and an extractor arm with a refutable literal
// argument such as `Color.RGB(0, g, b)`, which matches only when the first field is 0, does
// not cover its variant, so the match is non-exhaustive. The two failing rows differ in what
// the message asks for. A variant no arm names takes a new branch, while one named by a
// refutable argument takes a branch binding it irrefutably.
func TestInferMatchEnumExhaustiveness(t *testing.T) {
	tests := []struct {
		name    string
		arms    string
		wantVal string // inferred type of f on success; empty when an error is expected
		wantErr string // full error message with span on failure; empty on success
	}{
		{
			name:    "ArmPerVariantNeedsNoCatchAll",
			arms:    "Color.RGB(r, g, b) => r,\n\t\t\t\tColor.Hex(code) => 0",
			wantVal: "fn (c: Color) -> number",
		},
		{
			name:    "MissingVariant",
			arms:    "Color.RGB(r, g, b) => r",
			wantErr: "7:11-9:5: match is not exhaustive; add a branch for `Color.Hex`",
		},
		{
			name:    "CatchAllCoversRemainingVariants",
			arms:    "Color.RGB(r, g, b) => r,\n\t\t\t\t_ => 0",
			wantVal: "fn (c: Color) -> number",
		},
		{
			name:    "RefutableArgDoesNotCover",
			arms:    "Color.RGB(0, g, b) => g,\n\t\t\t\tColor.Hex(code) => 0",
			wantErr: "7:11-10:5: match is not exhaustive; `Color.RGB` is matched only by a branch whose own pattern can fail, so add a branch that matches it irrefutably",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, `
		enum Color {
			RGB(r: number, g: number, b: number),
			Hex(code: string),
		}
		fn f(c: Color) {
			return match c {
				`+tt.arms+`
			}
		}
	`)
			if tt.wantErr == "" {
				require.Empty(t, errs)
				require.Equal(t, tt.wantVal, values["f"])
			} else {
				require.Len(t, errs, 1)
				require.Equal(t, tt.wantErr, msgWithSpan(t, errs[0]))
			}
		})
	}
}

// TestInferMatchGenericEnumExhaustiveness checks that a match over a generic enum value is
// exhaustive through the alias. The scrutinee `o: MyOption<number>` carries the enum's alias
// handle with a `number` argument, and checkCondExhaustive expands it to the substituted
// variant union `MyOption.Some<number> | MyOption.None<number>`. An arm per variant covers
// that union without a catch-all.
func TestInferMatchGenericEnumExhaustiveness(t *testing.T) {
	values, _, errs := inferSource(t, `
		enum MyOption<T> {
			Some(value: T),
			None,
		}
		fn f(o: MyOption<number>) {
			return match o {
				MyOption.Some(value) => value,
				MyOption.None() => 0,
			}
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (o: MyOption<number>) -> number", values["f"])
}

// TestInferMatchUnionAliasExhaustiveness checks that exhaustiveness looks through a
// transparent user alias, the same expansion the enum path relies on. Each row shares the
// same `type Pet = Dog | Cat` alias and varies only the arms. The alias expands to its class
// union, so an arm per class is exhaustive without a catch-all, and dropping one row's arm
// leaves the match non-exhaustive with a diagnostic spanning the match expression.
func TestInferMatchUnionAliasExhaustiveness(t *testing.T) {
	tests := []struct {
		name    string
		arms    string
		wantErr string // full diagnostic with span; empty when the match is exhaustive
	}{
		{
			name: "ArmPerMemberIsExhaustive",
			arms: "Dog(name) => name,\n\t\t\t\tCat(name) => name",
		},
		{
			name:    "MissingMemberIsNonExhaustive",
			arms:    "Dog(name) => name",
			wantErr: "6:11-8:5: match is not exhaustive; add a branch for `Cat`",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, `
		class Dog { name: string }
		class Cat { name: string }
		type Pet = Dog | Cat
		fn f(p: Pet) {
			return match p {
				`+tt.arms+`
			}
		}
	`)
			if tt.wantErr == "" {
				require.Empty(t, errs)
			} else {
				require.Len(t, errs, 1)
				require.Equal(t, tt.wantErr, msgWithSpan(t, errs[0]))
			}
		})
	}
}

// A union member the coverage rules cannot evaluate — an object member here — is
// non-exhaustive when no arm covers it, not silently accepted. The scrutinee is
// `Point | {y: number}`: the `Point(x)` arm covers the nominal member, but the `{y: number}`
// member has no arm, and no structural arm exists to defer on, so the match is
// non-exhaustive. This is the nominal-plus-structural analogue of the enum-variant case the
// plan's D2 accept set writes as `Color.RGB | {x: number}`, which is not directly
// constructible since a variant constructor returns the whole enum union.
func TestInferMatchNominalMemberUncoveredStructural(t *testing.T) {
	_, _, errs := inferSource(t, `
		class Point { x: number }
		fn f(b: boolean) {
			val p = if b { Point(1) } else { {y: 2} }
			return match p {
				Point(x) => x
			}
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "5:11-7:5: match is not exhaustive; add a branch for `{y: 2}`", msgWithSpan(t, errs[0]))
}

// A union mixing a structural-object member with a non-object member, `"a" | {x: number}`,
// matched only by the object pattern `{x}`. Match-arm narrowing binds `{x}` against only
// the `{x: number}` member, so no spurious `cannot constrain "a" <: object` is reported.
// The `"a"` member is uncovered, so the match stays non-exhaustive.
func TestInferMatchUnionUncoveredWithStructuralMember(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f(b: boolean) {
			val p = if b { "a" } else { {x: 1} }
			return match p {
				{x} => x
			}
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "4:11-6:5: match is not exhaustive; add a branch for `\"a\"`", msgWithSpan(t, errs[0]))
}

// A match over a structural-object union such as `{x: number} | {y: string}` with an object
// pattern per member is exhaustive and binds each arm against its matching member. Match-arm
// narrowing binds `{x}` against only the `{x: number}` member and `{y}` against only the
// `{y: string}` member, so neither reports a missing property and both members are covered.
func TestInferMatchStructuralObjectUnion(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(p: {x: number} | {y: string}) {
			return match p {
				{x} => 1,
				{y} => 2
			}
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: {x: number} | {y: string}) -> 1 | 2", values["f"])
}

// A structural-object union with a member no arm's shape covers stays non-exhaustive. The
// `{x}` and `{y}` arms cover the first two members of `{x: number} | {y: string} |
// {z: boolean}`, but no arm names the `z` field, so the third member is uncovered.
func TestInferMatchStructuralObjectUnionUncovered(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f(p: {x: number} | {y: string} | {z: boolean}) {
			return match p {
				{x} => 1,
				{y} => 2
			}
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "3:11-6:5: match is not exhaustive; add a branch for `{z: boolean}`", msgWithSpan(t, errs[0]))
}

// Match-arm narrowing routes each tuple pattern to the union members of its fixed arity, so
// a union of differently-sized tuples covers with one arm per arity. `[a, c]` binds against
// the arity-2 member `[1, 2]` and `[s]` against the arity-1 member `["a"]`, so the match is
// exhaustive and each arm reads its own member's element types.
func TestInferMatchTupleUnionDifferentArity(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(b: boolean) {
			val x = if b { [1, 2] } else { ["a"] }
			return match x {
				[a, c] => a,
				[s] => s
			}
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, `fn (b: boolean) -> 1 | "a"`, values["f"])
}

// A tuple rest pattern `[head, ...tail]` binds the first element and captures the
// remaining elements as a tuple. It matches any tuple at least as long as its fixed
// prefix, so a single such arm covers a union of tuples of differing arity and the match
// is exhaustive. `head` binds at each member's first element, so the arm's body joins the
// two.
func TestInferMatchTupleRestPattern(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(p: [number, number] | [string]) {
			return match p {
				[head, ...tail] => head
			}
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: [string] | [number, number]) -> number | string", values["f"])
}

// A match arm's object test narrows a union soundly. With `{x}` binding against only the
// `{x: number}` member, it reads `x` at `number` without error.
func TestInferMatchExactUnionNarrowsCleanly(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn g(p: {x: number} | {y: string}) {
			return match p {
				{x} => x,
				_ => 0
			}
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: {x: number} | {y: string}) -> number", values["g"])
}

// An irrefutable `val {x} = p` cannot narrow the way a refutable match arm does, so it reads
// `x` off the whole `{x: number} | {y: string}` union. The field-read rule (M5 D4) reads a
// property present on some but not all members as `T | undefined`, so `val {x} = p` binds
// `x: number | undefined` with no error. The refutable/irrefutable split survives: a refutable
// `{x}` arm narrows to `x: number`, while this irrefutable read keeps the `undefined`.
func TestValDestructureUnionReadsPartialFieldAsUndefined(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(p: {x: number} | {y: string}) {
			val {x} = p
			return x
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: {x: number} | {y: string}) -> number | undefined", values["f"])
}

// A direct field read `p.x` takes the same partial-union path as a destructure (M5 D4), so
// reading `x` off `{x: number} | {y: string}` yields `number | undefined` rather than
// rejecting on the `{y: string}` member that lacks `x`.
func TestInferMemberUnionReadsPartialFieldAsUndefined(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(p: {x: number} | {y: string}) {
			return p.x
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: {x: number} | {y: string}) -> number | undefined", values["f"])
}

// A property every member carries at a different type reads as the join of those types (M5
// D4), with no undefined since no member lacks it. So `p.x` over `{x: number} | {x: string}`
// yields `number | string`.
func TestInferMemberUnionReadsCommonFieldAsJoin(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(p: {x: number} | {x: string}) {
			return p.x
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: {x: number} | {x: string}) -> number | string", values["f"])
}

// A property no member of an exact union carries is an error (M5 D4). Reading `p.z` off
// `{x: number} | {y: string}` would always evaluate to undefined, which is never useful, so
// it is rejected like an absent field on a single object rather than binding `undefined`.
func TestInferMemberUnionAbsentFieldErrors(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f(p: {x: number} | {y: string}) {
			return p.z
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "3:13-3:14: object is missing property: z", msgWithSpan(t, errs[0]))
}

// Reading an optional property off a single object is the single-object counterpart of the
// union field-read rule (#887): `p.x` off `{x?: number}` yields `number | undefined` rather
// than erroring, because the source may omit the property at runtime.
func TestInferMemberOptionalFieldReadsAsUndefined(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(p: {x?: number}) {
			return p.x
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: {x?: number}) -> number | undefined", values["f"])
}

// Destructuring an optional property takes the same single-object read path (#887), so
// `val {x} = p` over `{x?: number}` binds `x: number | undefined`.
func TestValDestructureOptionalFieldReadsAsUndefined(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(p: {x?: number}) {
			val {x} = p
			return x
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: {x?: number}) -> number | undefined", values["f"])
}

// The optional-read widening is specific to the field-read requirement. Filling a required
// annotated property from an optional source is a genuine subtyping demand and still fails
// with OptionalPropertyError (#887), since the source may omit the property.
func TestOptionalSourceIntoRequiredTargetStillErrors(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f(p: {x?: number}) {
			val q: {x: number} = p
			return q
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "object property is optional but required: x", errs[0].Message())
}

// The partial-union field read joins through a class instance's projected body, not only a
// plain object (#886). A property every member of a class-instance union carries at a
// different type reads as the join, so `p.x` over `A | B` where both declare `x` yields
// `number | string`, with no undefined since no member lacks it.
func TestInferMemberClassUnionReadsCommonFieldAsJoin(t *testing.T) {
	values, _, errs := inferSource(t, `
		class A { x: number, y: number }
		class B { x: string, z: boolean }
		fn f(p: A | B) {
			return p.x
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: A | B) -> number | string", values["f"])
}

// A property on some but not all members of a class-instance union reads as `T | undefined`
// (#886), mirroring the plain-object rule: the member lacking the field contributes undefined.
func TestInferMemberClassUnionReadsPartialFieldAsUndefined(t *testing.T) {
	values, _, errs := inferSource(t, `
		class A { x: number }
		class B { y: string }
		fn f(p: A | B) {
			return p.x
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: A | B) -> number | undefined", values["f"])
}

// A union mixing a plain object and a class instance joins through the same per-member read
// (#886): `p.x` over `{x: string} | Point` reads the object's `x` and the instance's projected
// `x`, yielding their join.
func TestInferMemberMixedObjectClassUnionReadsField(t *testing.T) {
	values, _, errs := inferSource(t, `
		class Point { x: number, y: number }
		fn f(p: {x: string} | Point) {
			return p.x
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: {x: string} | Point) -> number | string", values["f"])
}

// A getter resolves through member lookup in the partial-union read (#886), contributing its
// declared return type like a property. So `p.v` over a union of classes each exposing `v` as
// a getter reads as the join of the getter return types.
func TestInferMemberClassUnionReadsGetter(t *testing.T) {
	values, _, errs := inferSource(t, `
		class A { _v: number, get v(self) -> number { return self._v } }
		class B { _v: string, get v(self) -> string { return self._v } }
		fn f(p: A | B) {
			return p.v
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: A | B) -> number | string", values["f"])
}

// A method resolves through member lookup in the partial-union read (#886), contributing its
// receiver-stripped callable signature — the same value a direct `p.area` read yields. Methods
// with distinct signatures join into a union of function types.
func TestInferMemberClassUnionReadsMethod(t *testing.T) {
	values, _, errs := inferSource(t, `
		class A { area(self) -> number { return 1 } }
		class B { area(self, scale: number) -> string { return "x" } }
		fn f(p: A | B) {
			return p.area
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: A | B) -> (fn () -> number) | (fn (scale: number) -> string)", values["f"])
}

// An overloaded method contributes the intersection of its receiver-stripped arms in the
// partial-union read (#886), matching the value a direct read of an overloaded method yields.
// Both members expose the same two-arm `area`, so the reads join to one intersection.
func TestInferMemberClassUnionReadsOverloadedMethod(t *testing.T) {
	values, _, errs := inferSource(t, `
		class A {
			area(self, x: number) -> number { return x },
			area(self, x: string) -> string { return x },
		}
		class B {
			area(self, x: number) -> number { return x },
			area(self, x: string) -> string { return x },
		}
		fn f(p: A | B) {
			return p.area
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: A | B) -> (fn (x: number) -> number) & (fn (x: string) -> string)", values["f"])
}

// A property readable on no member of an exact class-instance union is an error (#886), the
// class analogue of TestInferMemberUnionAbsentFieldErrors.
func TestInferMemberClassUnionAbsentFieldErrors(t *testing.T) {
	_, _, errs := inferSource(t, `
		class A { x: number }
		class B { y: string }
		fn f(p: A | B) {
			return p.z
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "5:13-5:14: object is missing property: z", msgWithSpan(t, errs[0]))
}

// A setter-only member reads as undefined at runtime, so when another member exposes the name
// readably the join includes undefined (#886). Here `A` exposes `v` only as a setter and `B`
// carries it as a property, so `p.v` reads `string | undefined`.
func TestInferMemberClassUnionSetterOnlyReadsUndefined(t *testing.T) {
	values, _, errs := inferSource(t, `
		class A { _v: number, set v(mut self, x: number) { self._v = x } }
		class B { v: string }
		fn f(p: A | B) {
			return p.v
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: A | B) -> string | undefined", values["f"])
}

// A union member carrying both halves of an accessor pair contributes its GETTER to the
// read join, whichever half the class body declares first. Here `A` declares `set v` before
// `get v`, so a lookup that took declaration order would treat `v` as write-only on `A` and
// fold `undefined` into the join.
func TestInferMemberClassUnionAccessorPairReadsGetter(t *testing.T) {
	values, _, errs := inferSource(t, `
		class A { _v: number, set v(mut self, x: string) { }, get v(self) -> string { return "a" } }
		class B { v: string }
		fn f(p: A | B) {
			return p.v
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: A | B) -> string", values["f"])
}

// A setter-only member is not readable, so a member exposed only as a setter across every
// union member yields no readable value. With no member to read, the access is a
// missing-property error rather than binding as bare undefined (#886).
func TestInferMemberClassUnionSetterOnlyEverywhereErrors(t *testing.T) {
	_, _, errs := inferSource(t, `
		class A { _v: number, set v(mut self, x: number) { self._v = x } }
		class B { _w: string, set v(mut self, x: string) { self._w = x } }
		fn f(p: A | B) {
			return p.v
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "5:13-5:14: object is missing property: v", msgWithSpan(t, errs[0]))
}
