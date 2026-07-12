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
// constructor parameter's type. The synthesized constructor's parameters are the instance
// fields, so `x` and `y` bind at `number`.
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

// An extractor pattern binds against the DECLARED constructor's parameters, not the field
// list, so an explicit constructor whose parameters differ from the fields drives the
// argument types.
func TestInferExtractorPatExplicitConstructor(t *testing.T) {
	values, _, errs := inferSource(t, `
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
	require.Empty(t, errs)
	require.Equal(t, "fn (c: Celsius) -> [string, number]", values["f"])
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
