package tests

import (
	"strings"
	"testing"

	. "github.com/escalier-lang/escalier/internal/checker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExhaustiveMatch(t *testing.T) {
	tests := map[string]struct {
		input          string
		expectedErrs   []string // exact error messages expected
		expectedWarns  []string // exact warning messages expected
		expectedValues map[string]string
	}{
		// ---------------------------------------------------------------
		// Exhaustive matches (no errors expected)
		// ---------------------------------------------------------------

		"EnumFullyCovered": {
			input: `
				enum Color {
					RGB(r: number, g: number, b: number),
					Hex(code: string),
				}
				declare val color: Color
				val result = match color {
					Color.RGB(r, g, b) => r + g + b,
					Color.Hex(code) => code,
				}
			`,
			expectedValues: map[string]string{
				"result": "number | string",
			},
		},
		"EnumWithCatchAll": {
			input: `
				enum Color {
					RGB(r: number, g: number, b: number),
					Hex(code: string),
				}
				declare val color: Color
				val result = match color {
					Color.RGB(r, g, b) => r + g + b,
					_ => "other",
				}
			`,
			expectedValues: map[string]string{
				"result": `number | "other"`,
			},
		},
		"BooleanBothBranches": {
			input: `
				declare val b: boolean
				val result = match b {
					true => "yes",
					false => "no",
				}
			`,
			expectedValues: map[string]string{
				"result": `"yes" | "no"`,
			},
		},
		"BooleanWithCatchAll": {
			input: `
				declare val b: boolean
				val result = match b {
					true => "yes",
					_ => "no",
				}
			`,
			expectedValues: map[string]string{
				"result": `"yes" | "no"`,
			},
		},
		"LiteralUnionFullyCovered": {
			input: `
				type Direction = "north" | "south" | "east" | "west"
				declare val dir: Direction
				val result = match dir {
					"north" => 0,
					"south" => 1,
					"east" => 2,
					"west" => 3,
				}
			`,
			expectedValues: map[string]string{
				"result": "0 | 1 | 2 | 3",
			},
		},
		"StructuralUnionFullyCoveredByObjectPatterns": {
			input: `
				type Shape = {kind: "circle", radius: number}
				           | {kind: "square", side: number}
				declare val shape: Shape
				val result = match shape {
					{kind} => kind,
				}
			`,
			expectedValues: map[string]string{
				"result": `"circle" | "square"`,
			},
		},
		"NominalUnionCoveredByInstancePatterns": {
			input: `
				class Point(x: number, y: number) { x, y }
				class Event(kind: string) { kind }
				declare val obj: Point | Event
				val result = match obj {
					Point {x, y} => x + y,
					Event {kind} => kind,
				}
			`,
			expectedValues: map[string]string{
				"result": "number | string",
			},
		},
		"NonFiniteTypeCoveredByCatchAll": {
			input: `
				declare val n: number
				val result = match n {
					0 => "zero",
					x => "nonzero",
				}
			`,
			expectedValues: map[string]string{
				"result": `"zero" | "nonzero"`,
			},
		},
		"StringTypeCoveredByCatchAll": {
			input: `
				declare val s: string
				val result = match s {
					"hello" => 1,
					_ => 0,
				}
			`,
			expectedValues: map[string]string{
				"result": "1 | 0",
			},
		},
		"GuardedBranchWithCatchAll": {
			input: `
				declare val n: number
				val result = match n {
					x if x > 0 => "positive",
					_ => "non-positive",
				}
			`,
			expectedValues: map[string]string{
				"result": `"positive" | "non-positive"`,
			},
		},
		"MixedNominalAndStructuralPatterns": {
			input: `
				class Point(x: number, y: number) { x, y }
				class Event(kind: string) { kind }
				declare val obj: Point | Event
				val result = match obj {
					Point {x, y} => x + y,
					{kind} => kind,
				}
			`,
			expectedValues: map[string]string{
				"result": "number | string",
			},
		},

		// ---------------------------------------------------------------
		// Non-exhaustive matches (errors expected)
		// ---------------------------------------------------------------

		"EnumMissingVariant": {
			input: `
				enum Color {
					RGB(r: number, g: number, b: number),
					Hex(code: string),
				}
				declare val color: Color
				val result = match color {
					Color.RGB(r, g, b) => r + g + b,
				}
			`,
			expectedErrs: []string{
				"Non-exhaustive match: missing cases for Color.Hex",
			},
		},
		"BooleanMissingFalse": {
			input: `
				declare val b: boolean
				val result = match b {
					true => "yes",
				}
			`,
			expectedErrs: []string{
				"Non-exhaustive match: missing cases for false",
			},
		},
		"BooleanMissingTrue": {
			input: `
				declare val b: boolean
				val result = match b {
					false => "no",
				}
			`,
			expectedErrs: []string{
				"Non-exhaustive match: missing cases for true",
			},
		},
		"LiteralUnionMissingMembers": {
			input: `
				type Direction = "north" | "south" | "east" | "west"
				declare val dir: Direction
				val result = match dir {
					"north" => 0,
					"south" => 1,
				}
			`,
			expectedErrs: []string{
				`Non-exhaustive match: missing cases for "east", "west"`,
			},
		},
		"NonFiniteTypeNoCatchAll": {
			input: `
				declare val n: number
				val result = match n {
					0 => "zero",
					1 => "one",
				}
			`,
			expectedErrs: []string{
				"Non-exhaustive match: type 'number' is not fully covered; add a catch-all branch",
			},
		},
		"StringTypeNoCatchAll": {
			input: `
				declare val s: string
				val result = match s {
					"hello" => 1,
					"world" => 2,
				}
			`,
			expectedErrs: []string{
				"Non-exhaustive match: type 'string' is not fully covered; add a catch-all branch",
			},
		},
		"NonFiniteTypeOnlyGuardedBranches": {
			input: `
				declare val n: number
				val result = match n {
					x if x > 0 => "positive",
					x if x < 0 => "negative",
				}
			`,
			expectedErrs: []string{
				"Non-exhaustive match: type 'number' is not fully covered; add a catch-all branch",
			},
		},
		"BooleanOnlyGuardedBranches": {
			input: `
				declare val b: boolean
				val result = match b {
					true if false => "never",
				}
			`,
			expectedErrs: []string{
				"Non-exhaustive match: missing cases for true, false",
			},
		},
		"EmptyMatchOnEnum": {
			input: `
				enum Option {
					Some(value: number),
					None(),
				}
				declare val opt: Option
				val result = match opt {
					_ => "something",
				}
			`,
			// No error: catch-all covers everything
		},
		"StructuralUnionPartialCoverage": {
			input: `
				type Shape = {kind: "circle", radius: number}
							| {kind: "square", side: number}
							| {kind: "rect", width: number, height: number}
				declare val shape: Shape
				val result = match shape {
					{radius} => radius,
				}
			`,
			expectedErrs: []string{
				"Non-exhaustive match: missing cases for {kind: \"square\", side: number}, {kind: \"rect\", width: number, height: number}",
			},
		},
		"NominalClassNoCatchAll": {
			input: `
				class Point(x: number, y: number) { x, y }
				declare val p: Point
				val result = match p {
					{x} => x,
				}
			`,
			expectedErrs: []string{
				"Non-exhaustive match: missing cases for {x: number, y: number}",
			},
		},

		// ---------------------------------------------------------------
		// Redundancy warnings
		// ---------------------------------------------------------------

		"RedundantDuplicateLiteralBranch": {
			input: `
				declare val b: boolean
				val result = match b {
					true => "yes",
					false => "no",
					false => "also no",
				}
			`,
			expectedWarns: []string{
				"Redundant match branch: this case is already covered by earlier branches",
			},
		},
		"RedundantCatchAllAfterFullCoverage": {
			input: `
				declare val b: boolean
				val result = match b {
					true => "yes",
					false => "no",
					_ => "unreachable",
				}
			`,
			expectedWarns: []string{
				"Redundant match branch: this case is already covered by earlier branches",
			},
		},
		"RedundantDuplicateEnumVariant": {
			input: `
				enum Color {
					RGB(r: number, g: number, b: number),
					Hex(code: string),
				}
				declare val color: Color
				val result = match color {
					Color.RGB(r, g, b) => r + g + b,
					Color.Hex(code) => code,
					Color.RGB(r, g, b) => 0,
				}
			`,
			expectedWarns: []string{
				"Redundant match branch: this case is already covered by earlier branches",
			},
		},
		"RedundantDuplicateStringLiteral": {
			input: `
				type Direction = "north" | "south" | "east" | "west"
				declare val dir: Direction
				val result = match dir {
					"north" => 0,
					"south" => 1,
					"north" => 99,
					"east" => 2,
					"west" => 3,
				}
			`,
			expectedWarns: []string{
				"Redundant match branch: this case is already covered by earlier branches",
			},
		},

		// ---------------------------------------------------------------
		// Guards (R6): guarded branches don't count for coverage
		// ---------------------------------------------------------------

		"GuardedBranchDoesNotCoverType": {
			input: `
				declare val b: boolean
				val result = match b {
					true if false => "never",
					true => "yes",
					false => "no",
				}
			`,
			// No error: unguarded true and false cover everything.
			// The guarded true doesn't affect coverage.
		},
		"GuardedBranchNotRedundant": {
			input: `
				declare val b: boolean
				val result = match b {
					true => "yes",
					true if false => "guarded duplicate",
					false => "no",
				}
			`,
			// No warnings: guarded branches are never flagged as redundant,
			// even if they cover an already-covered type.
		},

		// ---------------------------------------------------------------
		// Non-exhaustive match gated on prior errors (Phase 4)
		// ---------------------------------------------------------------

		"NoExhaustivenessCheckWhenPatternErrors": {
			input: `
				class Point(x: number, y: number) { x, y }
				declare val p: Point
				val result = match p {
					{nonexistent} => nonexistent,
				}
			`,
			expectedErrs: []string{
				"Property nonexistent does not exist on type {x: number, y: number}",
			},
			// Should NOT also produce a "Non-exhaustive match" error
			// because prior errors gate exhaustiveness checking.
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, inferErrors := inferModuleTypesAndErrors(t, test.input)

			// Separate errors from warnings.
			var errors []Error
			var warnings []Error
			for _, err := range inferErrors {
				if err.IsWarning() {
					warnings = append(warnings, err)
				} else {
					errors = append(errors, err)
				}
			}

			if len(test.expectedErrs) == 0 && len(test.expectedWarns) == 0 {
				// No errors or warnings expected.
				if len(errors) > 0 {
					for i, err := range errors {
						t.Logf("Unexpected Error[%d]: %s", i, err.Message())
					}
				}
				require.Empty(t, errors, "expected no errors")
				if len(warnings) > 0 {
					for i, w := range warnings {
						t.Logf("Unexpected Warning[%d]: %s", i, w.Message())
					}
				}
				require.Empty(t, warnings, "expected no warnings")
			}

			// Check expected errors (exact match).
			for _, expected := range test.expectedErrs {
				found := false
				for _, err := range errors {
					if err.Message() == expected {
						found = true
						break
					}
				}
				assert.True(t, found,
					"expected error %q, got errors: %v",
					expected, errMsgs(errors))
			}

			// Check expected warnings (exact match).
			for _, expected := range test.expectedWarns {
				found := false
				for _, w := range warnings {
					if w.Message() == expected {
						found = true
						break
					}
				}
				assert.True(t, found,
					"expected warning %q, got warnings: %v",
					expected, errMsgs(warnings))
			}

			// Check that NoExhaustivenessCheckWhenPatternErrors does NOT
			// produce exhaustiveness errors.
			if name == "NoExhaustivenessCheckWhenPatternErrors" {
				for _, err := range errors {
					assert.False(t, strings.Contains(err.Message(), "Non-exhaustive match"),
						"should not report exhaustiveness error when prior errors exist")
				}
			}

			// Check expected inferred types.
			if len(test.expectedValues) > 0 {
				actualTypes, _ := inferModuleTypesAndErrors(t, test.input)
				for key, expected := range test.expectedValues {
					assert.Equal(t, expected, actualTypes[key])
				}
			}
		})
	}
}

func errMsgs(errors []Error) []string {
	msgs := make([]string, len(errors))
	for i, err := range errors {
		msgs[i] = err.Message()
	}
	return msgs
}
