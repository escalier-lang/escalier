package tests

import (
	"strings"
	"testing"

	. "github.com/escalier-lang/escalier/internal/checker"
	"github.com/stretchr/testify/assert"
)

func TestPatternMatchStructuralVsNominal(t *testing.T) {
	tests := map[string]struct {
		input          string
		expectedValues map[string]string
	}{
		"Case6_PartialMatchSubsetOfFields": {
			input: `
				class User(name: string, age: number, email: string) { name, age, email }

				declare val user: User
				val result = match user {
					{name} => name,
					_ => "",
				}
			`,
			expectedValues: map[string]string{
				"result": `string`,
			},
		},
		"Case12_StructuralPatternMatchesGetter": {
			input: `
				class Circle(radius: number) {
					get area(self) -> number { return 3.14159 * radius * radius },
				}

				declare val circle: Circle
				val result = match circle {
					{area} => area,
					_ => 0,
				}
			`,
			expectedValues: map[string]string{
				"result": "number",
			},
		},
		"PartialMatchMultipleFields": {
			input: `
				class Point(x: number, y: number) { x, y }

				declare val p: Point
				val result = match p {
					{x, y} => x + y,
					_ => 0,
				}
			`,
			expectedValues: map[string]string{
				"result": "number",
			},
		},
		"Case1_StructuralDestructuringOfNominalUnion": {
			input: `
				class Point(x: number, y: number) { x, y }
				class Event(kind: string) { kind }

				declare val obj: Point | Event
				val result = match obj {
					{x, y} => x + y,
					{kind} => kind,
				}
			`,
			expectedValues: map[string]string{
				"result": "number | string",
			},
		},
		"Case7_SharedFieldsProduceUnionBindings": {
			input: `
				type FooBarBaz = {kind: "foo", value: string} | {kind: "bar", value: number} | {kind: "baz", flag: boolean}

				declare val fbb: FooBarBaz
				val result1 = match fbb {
					{value} => value,
					_ => "",
				}
				val result2 = match fbb {
					{flag} => flag,
					_ => false,
				}
			`,
			expectedValues: map[string]string{
				"result1": `string | number`,
				"result2": "boolean",
			},
		},
		"Case3_CorrectEnumInstanceMatching": {
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
		"Case5_MixedNominalAndStructuralPatterns": {
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
		"Case11_ObjectPatternWithLiteralValues": {
			input: `
				type Point = {x: number, y: number}
				declare val p: Point
				val result = match p {
					{x: 0, y: 0} => "origin",
					_ => "other",
				}
			`,
			expectedValues: map[string]string{
				"result": `"origin" | "other"`,
			},
		},
		"Case8_SharedFieldSameTypeAcrossAllMembers": {
			input: `
				type Shape = {kind: "circle", radius: number} | {kind: "square", side: number} | {kind: "rect", width: number, height: number}

				declare val shape: Shape
				val result = match shape {
					{kind} => kind,
				}
			`,
			expectedValues: map[string]string{
				"result": `"circle" | "square" | "rect"`,
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			actualTypes := inferModuleTypes(t, test.input)
			for key, expected := range test.expectedValues {
				assert.Equal(t, expected, actualTypes[key])
			}
		})
	}
}

func TestPatternMatchErrors(t *testing.T) {
	tests := map[string]struct {
		input          string
		expectedErrs   []string
		expectedValues map[string]string
	}{
		"PatternFieldNotFoundOnNominalType": {
			input: `
				class Point(x: number, y: number) { x, y }

				declare val p: Point
				val result = match p {
					{foo} => foo,
				}
			`,
			expectedErrs: []string{
				"Property foo does not exist on type {x: number, y: number}",
			},
			// Verify the unresolved pattern field resolves to undefined (not a leaked type var)
			expectedValues: map[string]string{
				"result": "undefined",
			},
		},
		"Case4_PatternFieldNotInAnyUnionMember": {
			input: `
				class Point(x: number, y: number) { x, y }
				class Event(kind: string) { kind }

				declare val obj: Point | Event
				val result = match obj {
					{foo} => foo,
				}
			`,
			expectedErrs: []string{
				"cannot be assigned to",
			},
		},
		"Case9_PatternFieldNotInAnyUnionMember_TypeAlias": {
			input: `
				type FooBar = {kind: "foo", value: string} | {kind: "bar", value: number}

				declare val fb: FooBar
				val result = match fb {
					{missing} => missing,
				}
			`,
			expectedErrs: []string{
				"cannot be assigned to",
			},
		},
		"Case10_PatternFieldsSplitAcrossMembers": {
			input: `
				class Point(x: number, y: number) { x, y }
				class Event(kind: string) { kind }

				declare val obj: Point | Event
				val result = match obj {
					{x, kind} => x,
				}
			`,
			expectedErrs: []string{
				"cannot be assigned to",
			},
		},
		"Case2_EnumConstructorAsMatchTarget": {
			input: `
				enum Color {
					RGB(r: number, g: number, b: number),
					Hex(code: string),
				}

				val result = match Color.RGB {
					Color.RGB(r, g, b) => r + g + b,
					Color.Hex(code) => code,
				}
			`,
			expectedErrs: []string{
				"constructor, not an instance",
			},
		},
		"ExtractorRestArgsMismatch": {
			input: `
				enum Foo {
					Bar(x: number),
				}
				declare val foo: Foo
				val result = match foo {
					Foo.Bar(a, b, ...rest) => a,
					_ => 0,
				}
			`,
			expectedErrs: []string{
				"Extractor return type mismatch",
			},
		},
		"PatternFieldMatchesSetterOnly": {
			input: `
				declare val obj: {
					get readable() -> string,
					set writable(value: number) -> undefined
				}
				val result = match obj {
					{writable} => writable,
				}
			`,
			expectedErrs: []string{
				"writable",
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			actualTypes, inferErrors := inferModuleTypesAndErrors(t, test.input)
			for _, expected := range test.expectedErrs {
				found := false
				for _, err := range inferErrors {
					if strings.Contains(err.Message(), expected) {
						found = true
						break
					}
				}
				assert.True(t, found, "expected error containing %q, got %v", expected, errMessages(inferErrors))
			}
			for key, expected := range test.expectedValues {
				assert.Equal(t, expected, actualTypes[key])
			}
		})
	}
}

func errMessages(errors []Error) []string {
	msgs := make([]string, len(errors))
	for i, err := range errors {
		msgs[i] = err.Message()
	}
	return msgs
}
