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
		require.Equal(t, "`Celsius` has no [Symbol.customMatcher] method", msgWithSpan(errs[0]))
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
	require.Equal(t, "5:16-5:17: object is missing property: z", msgWithSpan(errs[0]))
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
	require.Equal(t, "5:5-5:13: extractor pattern `Point` expects 2 arguments but got 1", msgWithSpan(errs[0]))
}

// An instance pattern whose name resolves to no class is an InstancePatternNotClassError.
// The inner fields still bind against a fresh var, so the arm body does not cascade into
// unknown-identifier errors.
func TestInferInstancePatNotAClass(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f(p: number) {
			return match p {
				Missing { x } => x
			}
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "4:5-4:18: `Missing` does not name a class and cannot be used as an instance pattern.", msgWithSpan(errs[0]))
}

// An extractor pattern whose name resolves to no constructor is an
// ExtractorPatternNotCtorError. A plain value binding is not callable as a constructor.
func TestInferExtractorPatNotAConstructor(t *testing.T) {
	_, _, errs := inferSource(t, `
		val g = 5
		fn f(p: number) {
			return match p {
				g(x) => x
			}
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "5:5-5:9: `g` is not a constructor and cannot be used as an extractor pattern.", msgWithSpan(errs[0]))
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
	require.Equal(t, "6:5-6:19: cannot constrain Point <: Circle", msgWithSpan(errs[0]))
}

// --- M5 D2: nominal union exhaustiveness ---
//
// An enum type is the union of its variant tokens, so a match over an enum reaches the
// union exhaustiveness path. An extractor pattern covers a variant member when it names
// that variant and binds its arguments, so an arm per variant makes the match exhaustive
// with no catch-all. The variant name resolves through the enum's namespace, so
// `Color.RGB` finds the `Color.RGB` variant token.

// An arm per enum variant covers every union member, so the match needs no catch-all. The
// arms bind each variant's fields and return them, so the inferred type shows the binding
// worked.
func TestInferMatchEnumExhaustive(t *testing.T) {
	values, _, errs := inferSource(t, `
		enum Color {
			RGB(r: number, g: number, b: number),
			Hex(code: string),
		}
		fn f(c: Color) {
			return match c {
				Color.RGB(r, g, b) => r,
				Color.Hex(code) => 0
			}
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (c: Color.RGB | Color.Hex) -> number", values["f"])
}

// A match leaving an enum variant uncovered is non-exhaustive. Covering only `Color.RGB`
// leaves `Color.Hex` unmatched, and enum variants are final so no width tail is implied,
// so a catch-all is required.
func TestInferMatchEnumMissingVariant(t *testing.T) {
	_, _, errs := inferSource(t, `
		enum Color {
			RGB(r: number, g: number, b: number),
			Hex(code: string),
		}
		fn f(c: Color) {
			return match c {
				Color.RGB(r, g, b) => r
			}
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "7:11-9:5: match is not exhaustive; add a catch-all branch", msgWithSpan(errs[0]))
}

// An unguarded catch-all covers every enum variant, so a single covered variant plus a
// catch-all is exhaustive.
func TestInferMatchEnumCatchAll(t *testing.T) {
	values, _, errs := inferSource(t, `
		enum Color {
			RGB(r: number, g: number, b: number),
			Hex(code: string),
		}
		fn f(c: Color) {
			return match c {
				Color.RGB(r, g, b) => r,
				_ => 0
			}
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (c: Color.RGB | Color.Hex) -> number", values["f"])
}

// An extractor arm covers a variant only when its argument sub-patterns are irrefutable. A
// literal argument such as `Color.RGB(0, g, b)` matches only when the first field is 0, so
// it does not cover the whole `Color.RGB` member, and the match is non-exhaustive.
func TestInferMatchEnumRefutableArgNotExhaustive(t *testing.T) {
	_, _, errs := inferSource(t, `
		enum Color {
			RGB(r: number, g: number, b: number),
			Hex(code: string),
		}
		fn f(c: Color) {
			return match c {
				Color.RGB(0, g, b) => g,
				Color.Hex(code) => 0
			}
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "7:11-10:5: match is not exhaustive; add a catch-all branch", msgWithSpan(errs[0]))
}

// DISABLED until M5 D3. A union mixing a structural-object member with a non-object member,
// `"a" | {x: number}`, matched only by the object pattern `{x}`. The `"a"` member is
// uncovered, so the match is non-exhaustive. Binding `{x}` against the whole union today
// also constrains every member to carry `x`, reporting a spurious `cannot constrain "a" <:
// object` on the `"a"` member. D3's match-arm narrowing binds `{x}` against only the
// `{x: number}` member, dropping that binding error while the non-exhaustive report stays.
// Re-enable when D3 lands and assert the single non-exhaustive error the commented body
// records.
func TestInferMatchUnionUncoveredWithStructuralMember(t *testing.T) {
	/*
		_, _, errs := inferSource(t, `
			fn f(b: boolean) {
				val p = if b { "a" } else { {x: 1} }
				return match p {
					{x} => x
				}
			}
		`)
		require.Len(t, errs, 1)
		require.Equal(t, "4:11-6:5: match is not exhaustive; add a catch-all branch", msgWithSpan(errs[0]))
	*/
}

// DISABLED until M5 D3. A match over a structural-object union such as
// `{x: number} | {y: string}` with an object pattern per member should be exhaustive and
// bind each arm against its matching member. Binding an object pattern against a union
// scrutinee currently constrains every member to carry the named field, so `{x}` against
// the `{y: string}` member reports a missing property, and the union exhaustiveness path
// reports each structural arm unsupported. Both resolve when D3's match-arm narrowing
// lands, which binds an object pattern against only the members carrying its fields.
// Re-enable then and assert the empty-error, exhaustive result the commented body records.
func TestInferMatchStructuralObjectUnion(t *testing.T) {
	/*
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
	*/
}
