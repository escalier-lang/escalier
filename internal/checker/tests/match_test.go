package tests

import (
	"testing"

	. "github.com/escalier-lang/escalier/internal/checker"
	"github.com/stretchr/testify/assert"
)

// TestMatchTargetTypeInference verifies that when a function parameter is used
// as the target of a match expression, the parameter's type is inferred from
// the patterns used in the match arms.
func TestMatchTargetTypeInference(t *testing.T) {
	tests := map[string]struct {
		input          string
		expectedValues map[string]string
		expectedErrs   []string
	}{
		// ---------------------------------------------------------------
		// ExtractorPat: infer enum type from constructor return type
		// ---------------------------------------------------------------

		"ExtractorNonGenericEnum": {
			input: `
				enum Color {
					RGB(r: number, g: number, b: number),
					Hex(code: string),
				}
				val describe = fn (color) {
					return match color {
						Color.RGB(r, g, b) => r + g + b,
						Color.Hex(code) => code,
					}
				}
			`,
			expectedValues: map[string]string{
				"describe": "fn (color: Color) -> number | string",
			},
		},
		"ExtractorGenericEnumDirectMatch": {
			// Test that matching a generic enum with a TypeVar target works
			// when not inside a function (simpler case).
			input: `
				enum Option<T> {
					Some(value: T),
					None,
				}
				declare val option: Option<number>
				val result = match option {
					Option.Some(value) => value,
					Option.None => "none",
				}
			`,
			expectedValues: map[string]string{
				"result": `number | "none"`,
			},
		},
		"ExtractorGenericEnumNoFunc": {
			// Verify match with inferred generic target outside a function.
			// Uses a let-binding with a TypeVar target to isolate from function
			// machinery (generalization, call sites, etc.).
			input: `
				enum Option<T> {
					Some(value: T),
					None,
				}
				declare val option: Option<string>
				val result = match option {
					Option.Some(value) => value,
					Option.None => "none",
				}
			`,
			expectedValues: map[string]string{
				"result": `string`,
			},
		},
		"ExtractorGenericEnumConstrained": {
			// Test with body constraining T to number
			input: `
				enum Option<T> {
					Some(value: T),
					None,
				}
				val add1 = fn (option) {
					return match option {
						Option.Some(value) => value + 1,
						Option.None => 0,
					}
				}
			`,
			expectedValues: map[string]string{
				"add1": `fn (option: Option<number>) -> number`,
			},
		},
		"ExtractorGenericEnum": {
			input: `
				enum Option<T> {
					Some(value: T),
					None,
				}
				val describe = fn (option) {
					return match option {
						Option.Some(value) => value,
						Option.None => "none",
					}
				}
			`,
			expectedValues: map[string]string{
				"describe": `fn <T0>(option: Option<T0>) -> T0 | "none"`,
			},
		},
		"ExtractorWithWildcard": {
			input: `
				enum Color {
					RGB(r: number, g: number, b: number),
					Hex(code: string),
				}
				val describe = fn (color) {
					return match color {
						Color.RGB(r, g, b) => r + g + b,
						_ => "other",
					}
				}
			`,
			expectedValues: map[string]string{
				"describe": `fn (color: Color) -> number | "other"`,
			},
		},

		// ---------------------------------------------------------------
		// InstancePat: infer class union from instance patterns
		// ---------------------------------------------------------------

		"InstancePatMultipleClasses": {
			input: `
				class Point(x: number, y: number) { x, y }
				class Event(kind: string) { kind }
				val handle = fn (obj) {
					return match obj {
						Point {x, y} => x + y,
						Event {kind} => kind,
					}
				}
			`,
			expectedValues: map[string]string{
				"handle": "fn (obj: Point | Event) -> number | string",
			},
		},

		// ---------------------------------------------------------------
		// LitPat: infer literal union from literal patterns
		// ---------------------------------------------------------------

		"LitPatStringLiterals": {
			input: `
				val dir = fn (d) {
					return match d {
						"north" => 0,
						"south" => 1,
					}
				}
			`,
			expectedValues: map[string]string{
				"dir": `fn (d: "north" | "south") -> 0 | 1`,
			},
		},
		"LitPatBooleans": {
			input: `
				val check = fn (b) {
					return match b {
						true => "yes",
						false => "no",
					}
				}
			`,
			expectedValues: map[string]string{
				"check": `fn (b: boolean) -> "yes" | "no"`,
			},
		},

		// ---------------------------------------------------------------
		// ObjectPat: infer structural union from object patterns
		// ---------------------------------------------------------------

		"ObjectPatDiscriminatedUnion": {
			input: `
				val handle = fn (event) {
					return match event {
						{kind: "click", x, y} => x + y,
						{kind: "key", key} => key,
					}
				}
			`,
			expectedValues: map[string]string{
				"handle": `fn <T0>(event: {kind: "click", x: number, y: number} | {kind: "key", key: T0}) -> number | T0`,
			},
		},

		// ---------------------------------------------------------------
		// IdentPat: body usage constrains target via alias chain
		// ---------------------------------------------------------------

		"IdentPatBodyConstrainsTarget": {
			input: `
				val inc = fn (x) {
					return match x {
						n => n + 1,
					}
				}
			`,
			expectedValues: map[string]string{
				"inc": "fn (x: number) -> number",
			},
		},

		// ---------------------------------------------------------------
		// ExtractorPat: extractors from different enums
		// ---------------------------------------------------------------

		"ExtractorMixedEnums": {
			input: `
				enum Option<T> {
					Some(value: T),
					None,
				}
				enum Result<T, E> {
					Ok(value: T),
					Err(error: E),
				}
				val handle = fn (input) {
					return match input {
						Option.Some(value) => value,
						Result.Ok(value) => value,
					}
				}
			`,
			expectedValues: map[string]string{
				"handle": "fn <T0, T1, T2>(input: Option<T0> | Result<T1, T2>) -> T0 | T1",
			},
			expectedErrs: []string{
				"Non-exhaustive match: missing cases for Option<T0>, Result<T1, T2>",
			},
		},

		// ---------------------------------------------------------------
		// Explicitly typed param: no regression
		// ---------------------------------------------------------------

		"ExplicitlyTypedParam": {
			input: `
				enum Option<T> {
					Some(value: T),
					None,
				}
				val describe = fn (option: Option<number>) {
					return match option {
						Option.Some(value) => value,
						Option.None => 0,
					}
				}
			`,
			expectedValues: map[string]string{
				"describe": "fn (option: Option<number>) -> number",
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			actualTypes, inferErrors := inferModuleTypesAndErrors(t, test.input)

			// Separate errors from warnings.
			var errors []Error
			for _, err := range inferErrors {
				if !err.IsWarning() {
					errors = append(errors, err)
				}
			}

			// Check errors.
			assert.ElementsMatch(t, test.expectedErrs, errMessages(errors),
				"error messages should match exactly")

			// Check expected inferred types.
			for key, expected := range test.expectedValues {
				assert.Equal(t, expected, actualTypes[key],
					"type of %q should match", key)
			}
		})
	}
}
