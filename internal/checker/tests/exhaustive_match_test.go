package tests

import (
	"strings"
	"testing"

	. "github.com/escalier-lang/escalier/internal/checker"
	"github.com/stretchr/testify/assert"
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
		"InstancePatWithInnerExhaustive": {
			input: `
				class Toggle(value: boolean) { value }
				class Label(text: string) { text }
				declare val obj: Toggle | Label
				val result = match obj {
					Toggle {value: true} => "on",
					Toggle {value: false} => "off",
					Label {text} => text,
				}
			`,
			expectedValues: map[string]string{
				"result": `"on" | "off" | string`,
			},
		},
		"InstancePatWithInnerNonExhaustive": {
			input: `
				class Toggle(value: boolean) { value }
				class Label(text: string) { text }
				declare val obj: Toggle | Label
				val result = match obj {
					Toggle {value: true} => "on",
					Label {text} => text,
				}
			`,
			expectedErrs: []string{
				"Non-exhaustive match: Toggle is missing inner cases for false",
			},
		},
		"InstancePatWithEnumPropertyExhaustive": {
			input: `
				enum Status { Active(), Inactive() }
				class Task(status: Status) { status }
				class Note(body: string) { body }
				declare val item: Task | Note
				val result = match item {
					Task {status: Status.Active()} => "active",
					Task {status: Status.Inactive()} => "inactive",
					Note {body} => body,
				}
			`,
			expectedValues: map[string]string{
				"result": `"active" | "inactive" | string`,
			},
		},
		"InstancePatWithEnumPropertyNonExhaustive": {
			input: `
				enum Status { Active(), Inactive() }
				class Task(status: Status) { status }
				class Note(body: string) { body }
				declare val item: Task | Note
				val result = match item {
					Task {status: Status.Active()} => "active",
					Note {body} => body,
				}
			`,
			expectedErrs: []string{
				"Non-exhaustive match: Task is missing inner cases for Status.Inactive",
			},
		},
		"InstancePatWithLiteralPropertyExhaustive": {
			input: `
				class Packet(kind: "data" | "ack") { kind }
				class Error(code: number) { code }
				declare val msg: Packet | Error
				val result = match msg {
					Packet {kind: "data"} => 1,
					Packet {kind: "ack"} => 2,
					Error {code} => code,
				}
			`,
			expectedValues: map[string]string{
				"result": "1 | 2 | number",
			},
		},
		"InstancePatWithLiteralPropertyNonExhaustive": {
			input: `
				class Packet(kind: "data" | "ack") { kind }
				class Error(code: number) { code }
				declare val msg: Packet | Error
				val result = match msg {
					Packet {kind: "data"} => 1,
					Error {code} => code,
				}
			`,
			expectedErrs: []string{
				`Non-exhaustive match: Packet is missing inner cases for "ack"`,
			},
		},
		"InstancePatWithNonFinitePropertyNeedsCatchAll": {
			input: `
				class Box(value: number) { value }
				class Empty() {}
				declare val obj: Box | Empty
				val result = match obj {
					Box {value: 0} => "zero",
					Empty {} => "empty",
				}
			`,
			expectedErrs: []string{
				"Non-exhaustive match: Box is not fully covered; add a catch-all branch",
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
		"RedundantDuplicateInstancePattern": {
			input: `
				class Point(x: number, y: number) { x, y }
				class Event(kind: string) { kind }
				declare val obj: Point | Event
				val result = match obj {
					Point {x, y} => x + y,
					Event {kind} => kind,
					Point {x, y} => 0,
				}
			`,
			expectedWarns: []string{
				"Redundant match branch: this case is already covered by earlier branches",
			},
		},
		"RedundantDuplicateExtractorSubPattern": {
			input: `
				enum Wrapper {
					Bool(value: boolean),
					Num(value: number),
				}
				declare val w: Wrapper
				val result = match w {
					Wrapper.Bool(true) => "true",
					Wrapper.Bool(false) => "false",
					Wrapper.Bool(true) => "redundant",
					Wrapper.Num(n) => "num",
				}
			`,
			expectedWarns: []string{
				"Redundant match branch: this case is already covered by earlier branches",
			},
		},
		"RedundantDuplicateCatchAllOnNonFiniteType": {
			input: `
				declare val n: number
				val result = match n {
					x => x,
					_ => 0,
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
		// Tuple exhaustiveness (Phase 5)
		// ---------------------------------------------------------------

		// Case 14: All boolean-boolean combinations covered.
		"TupleBoolBoolFullyCovered": {
			input: `
				declare val pair: [boolean, boolean]
				val result = match pair {
					[true, true] => "both",
					[true, false] => "first only",
					[false, true] => "second only",
					[false, false] => "neither",
				}
			`,
			expectedValues: map[string]string{
				"result": `"both" | "first only" | "second only" | "neither"`,
			},
		},
		// Case 15: Missing boolean-boolean combinations.
		"TupleBoolBoolMissingCombinations": {
			input: `
				declare val pair: [boolean, boolean]
				val result = match pair {
					[true, true] => "both",
					[false, false] => "neither",
				}
			`,
			expectedErrs: []string{
				"Non-exhaustive match: missing cases for [true, false], [false, true]",
			},
		},
		// Tuple with wildcard elements covering multiple combinations.
		"TupleBoolBoolWithWildcard": {
			input: `
				declare val pair: [boolean, boolean]
				val result = match pair {
					[true, _] => "first is true",
					[false, _] => "first is false",
				}
			`,
			expectedValues: map[string]string{
				"result": `"first is true" | "first is false"`,
			},
		},
		// Tuple with a catch-all pattern at the top level.
		"TupleCatchAll": {
			input: `
				declare val pair: [boolean, boolean]
				val result = match pair {
					[true, true] => "both",
					_ => "other",
				}
			`,
			expectedValues: map[string]string{
				"result": `"both" | "other"`,
			},
		},
		// Tuple with literal union elements.
		"TupleLiteralUnionFullyCovered": {
			input: `
				type AB = "a" | "b"
				declare val pair: [AB, boolean]
				val result = match pair {
					["a", true] => 1,
					["a", false] => 2,
					["b", true] => 3,
					["b", false] => 4,
				}
			`,
			expectedValues: map[string]string{
				"result": "1 | 2 | 3 | 4",
			},
		},
		// Tuple containing a non-finite element requires a catch-all.
		"TupleNonFiniteElementNoCatchAll": {
			input: `
				declare val pair: [boolean, number]
				val result = match pair {
					[true, 0] => "zero",
					[false, 1] => "one",
				}
			`,
			expectedErrs: []string{
				"Non-exhaustive match: type '[boolean, number]' is not fully covered; add a catch-all branch",
			},
		},
		// Tuple with non-finite element covered by a catch-all.
		"TupleNonFiniteElementWithCatchAll": {
			input: `
				declare val pair: [boolean, number]
				val result = match pair {
					[true, 0] => "zero",
					_ => "other",
				}
			`,
			expectedValues: map[string]string{
				"result": `"zero" | "other"`,
			},
		},
		// Redundant tuple branch.
		"TupleRedundantBranch": {
			input: `
				declare val pair: [boolean, boolean]
				val result = match pair {
					[true, true] => "both",
					[true, false] => "first only",
					[false, true] => "second only",
					[false, false] => "neither",
					[true, true] => "duplicate",
				}
			`,
			expectedWarns: []string{
				"Redundant match branch: this case is already covered by earlier branches",
			},
		},
		// TuplePat with all ident elements is a catch-all.
		"TupleAllIdentIsCatchAll": {
			input: `
				declare val pair: [boolean, boolean]
				val result = match pair {
					[a, b] => "got it",
				}
			`,
			expectedValues: map[string]string{
				"result": `"got it"`,
			},
		},

		// ---------------------------------------------------------------
		// Tuple with rest spread (Phase 5)
		// ---------------------------------------------------------------

		// A tuple with a rest element is non-finite and requires a catch-all.
		"TupleRestSpreadNoCatchAll": {
			input: `
				declare val x: [boolean, ...Array<number>]
				val result = match x {
					[true, ...rest] => rest,
				}
			`,
			expectedErrs: []string{
				"Non-exhaustive match: type '[boolean, ...Array<number>]' is not fully covered; add a catch-all branch",
			},
		},
		// Patterns [x] and [x, ...rest] against a rest-spread tuple.
		// [x, ...rest] acts as a catch-all since all elements are
		// wildcard/ident (rest is treated like a catch-all element).
		"TupleRestPatterns": {
			input: `
				declare val x: [number, ...Array<number>]
				val result = match x {
					[x] => x,
					[x, y] => y,
					[x, y, ...rest] => rest.length,
				}
			`,
			expectedValues: map[string]string{
				"result": "number | number | number",
			},
		},
		// A tuple with a rest element covered by a catch-all.
		"TupleRestSpreadWithCatchAll": {
			input: `
				declare val x: [boolean, ...Array<number>]
				val result = match x {
					[true, ...rest] => rest,
					_ => [],
				}
			`,
			expectedValues: map[string]string{
				"result": "Array<number> | []",
			},
		},

		// ---------------------------------------------------------------
		// Union of tuples (Phase 5)
		// ---------------------------------------------------------------

		// Empty tuple is inhabited and covered by a catch-all.
		"TupleEmptyFullyCovered": {
			input: `
				declare val x: []
				val result = match x {
					_ => "empty",
				}
			`,
			expectedValues: map[string]string{
				"result": `"empty"`,
			},
		},

		// TuplePat matching against a union of tuple types.
		"TupleUnionOfTuplesFullyCovered": {
			input: `
				type Pair = ["a", "a"] | ["b", "b"]
				declare val x: Pair
				val result = match x {
					["a", "a"] => 1,
					["b", "b"] => 2,
				}
			`,
			expectedValues: map[string]string{
				"result": "1 | 2",
			},
		},

		// TuplePat with all-ident on a mixed union should only cover
		// tuple members, not non-tuple members like number.
		"TupleMixedUnionIdentNotGlobalCatchAll": {
			input: `
				type T = ["a", "a"] | ["b", "b"] | number
				declare val x: T
				val result = match x {
					[a, b] => a,
				}
			`,
			expectedErrs: []string{
				"Non-exhaustive match: missing cases for number",
			},
		},

		// ---------------------------------------------------------------
		// Nested exhaustiveness (Phase 7)
		// ---------------------------------------------------------------

		// Case 16: Nested extractor with catch-all inner pattern.
		"NestedExtractorWithCatchAll": {
			input: `
				enum Result {
					Ok(value: number),
					Err(message: string),
				}
				declare val r: Result
				val result = match r {
					Result.Ok(0) => "zero",
					Result.Ok(n) => "other",
					Result.Err(message) => message,
				}
			`,
			expectedValues: map[string]string{
				"result": `"zero" | "other" | string`,
			},
		},
		// Case 17: Nested extractor without catch-all inner pattern.
		"NestedExtractorNonExhaustive": {
			input: `
				enum Result {
					Ok(value: number),
					Err(message: string),
				}
				declare val r: Result
				val result = match r {
					Result.Ok(0) => "zero",
					Result.Ok(1) => "one",
					Result.Err(message) => message,
				}
			`,
			expectedErrs: []string{
				"Non-exhaustive match: Result.Ok is not fully covered; add a catch-all branch",
			},
		},
		// Case 18: Nested boolean exhaustiveness inside extractor.
		"NestedExtractorBooleanExhaustive": {
			input: `
				enum Wrapper {
					Bool(value: boolean),
					Str(value: string),
				}
				declare val w: Wrapper
				val result = match w {
					Wrapper.Bool(true) => "yes",
					Wrapper.Bool(false) => "no",
					Wrapper.Str(s) => s,
				}
			`,
			expectedValues: map[string]string{
				"result": `"yes" | "no" | string`,
			},
		},
		// Case 19: Structural object patterns with catch-all bindings.
		"NestedObjectPatternWithCatchAll": {
			input: `
				type Shape = {kind: "circle", radius: number}
				           | {kind: "square", side: number}
				declare val shape: Shape
				val result = match shape {
					{kind: "circle", radius} => radius,
					{kind: "square", side} => side,
				}
			`,
			expectedValues: map[string]string{
				"result": "number | number",
			},
		},
		// Case 20: Structural object patterns without inner catch-all.
		"NestedObjectPatternNonExhaustive": {
			input: `
				type Shape = {kind: "circle", radius: number}
				           | {kind: "square", side: number}
				declare val shape: Shape
				val result = match shape {
					{kind: "circle", radius: 0} => "point",
					{kind: "circle", radius: 1} => "unit",
					{kind: "square", side} => side,
				}
			`,
			expectedErrs: []string{
				`Non-exhaustive match: {kind: "circle", radius: number} is not fully covered; add a catch-all branch`,
			},
		},
		// Nested boolean exhaustiveness missing case.
		"NestedExtractorBooleanMissingFalse": {
			input: `
				enum Wrapper {
					Bool(value: boolean),
					Str(value: string),
				}
				declare val w: Wrapper
				val result = match w {
					Wrapper.Bool(true) => "yes",
					Wrapper.Str(s) => s,
				}
			`,
			expectedErrs: []string{
				"Non-exhaustive match: Wrapper.Bool is missing inner cases for false",
			},
		},

		// ---------------------------------------------------------------
		// Deeply nested combinations (Phase 7)
		// ---------------------------------------------------------------

		// Multi-arg extractor with all boolean combinations covered.
		"MultiArgExtractorBoolBoolExhaustive": {
			input: `
				enum Value {
					Pair(a: boolean, b: boolean),
					Single(x: number),
				}
				declare val v: Value
				val result = match v {
					Value.Pair(true, true) => 1,
					Value.Pair(true, false) => 2,
					Value.Pair(false, true) => 3,
					Value.Pair(false, false) => 4,
					Value.Single(x) => x,
				}
			`,
			expectedValues: map[string]string{
				"result": "1 | 2 | 3 | 4 | number",
			},
		},
		// Multi-arg extractor missing a boolean combination.
		"MultiArgExtractorBoolBoolMissing": {
			input: `
				enum Value {
					Pair(a: boolean, b: boolean),
					Single(x: number),
				}
				declare val v: Value
				val result = match v {
					Value.Pair(true, true) => 1,
					Value.Pair(false, false) => 2,
					Value.Single(x) => x,
				}
			`,
			expectedErrs: []string{
				"Non-exhaustive match: Value.Pair is missing inner cases for [true, false], [false, true]",
			},
		},
		// Nested enum: Option wrapping Result, fully exhaustive.
		"NestedEnumOptionResultExhaustive": {
			input: `
				enum Result {
					Ok(value: number),
					Err(message: string),
				}
				enum Option {
					Some(value: Result),
					None(),
				}
				declare val o: Option
				val result = match o {
					Option.Some(Result.Ok(x)) => x,
					Option.Some(Result.Err(msg)) => msg,
					Option.None() => "nothing",
				}
			`,
			expectedValues: map[string]string{
				"result": `number | string | "nothing"`,
			},
		},
		// Nested enum: Option wrapping Result, missing Result.Err.
		"NestedEnumOptionResultMissing": {
			input: `
				enum Result {
					Ok(value: number),
					Err(message: string),
				}
				enum Option {
					Some(value: Result),
					None(),
				}
				declare val o: Option
				val result = match o {
					Option.Some(Result.Ok(x)) => x,
					Option.None() => "nothing",
				}
			`,
			expectedErrs: []string{
				"Non-exhaustive match: Option.Some is missing inner cases for Result.Err",
			},
		},
		// Object pattern with enum-valued property, exhaustive.
		"ObjectWithEnumPropertyExhaustive": {
			input: `
				enum Status {
					Active(),
					Inactive(),
				}
				type Item = {kind: "todo", status: Status}
				          | {kind: "note", body: string}
				declare val item: Item
				val result = match item {
					{kind: "todo", status: Status.Active()} => "active todo",
					{kind: "todo", status: Status.Inactive()} => "inactive todo",
					{kind: "note", body} => body,
				}
			`,
			expectedValues: map[string]string{
				"result": `"active todo" | "inactive todo" | string`,
			},
		},
		// Object pattern with enum-valued property, missing variant.
		"ObjectWithEnumPropertyMissing": {
			input: `
				enum Status {
					Active(),
					Inactive(),
				}
				type Item = {kind: "todo", status: Status}
				          | {kind: "note", body: string}
				declare val item: Item
				val result = match item {
					{kind: "todo", status: Status.Active()} => "active todo",
					{kind: "note", body} => body,
				}
			`,
			expectedErrs: []string{
				`Non-exhaustive match: {kind: "todo", status: Status} is missing inner cases for Status.Inactive`,
			},
		},
		// Object with two boolean properties where each property is
		// individually exhausted but the cross-product is not covered.
		// Per-property independent checking would falsely report this
		// as exhaustive.
		"ObjectCorrelatedBooleanPropertiesNonExhaustive": {
			input: `
				type Shape = {kind: "rect", tall: boolean, wide: boolean}
				           | {kind: "circle"}
				declare val s: Shape
				val result = match s {
					{kind: "rect", tall: true, wide: true} => "big",
					{kind: "rect", tall: false, wide: false} => "small",
					{kind: "circle"} => "round",
				}
			`,
			expectedErrs: []string{
				`Non-exhaustive match: {kind: "rect", tall: boolean, wide: boolean} is missing inner cases for [true, false], [false, true]`,
			},
		},
		// Three-level nesting: enum containing enum containing boolean.
		"ThreeLevelNestedExhaustive": {
			input: `
				enum Inner {
					Flag(value: boolean),
					Num(value: number),
				}
				enum Outer {
					Wrap(inner: Inner),
					Empty(),
				}
				declare val o: Outer
				val result = match o {
					Outer.Wrap(Inner.Flag(true)) => "yes",
					Outer.Wrap(Inner.Flag(false)) => "no",
					Outer.Wrap(Inner.Num(n)) => "num",
					Outer.Empty() => "empty",
				}
			`,
			expectedValues: map[string]string{
				"result": `"yes" | "no" | "num" | "empty"`,
			},
		},
		// Three-level nesting: missing innermost case.
		"ThreeLevelNestedMissing": {
			input: `
				enum Inner {
					Flag(value: boolean),
					Num(value: number),
				}
				enum Outer {
					Wrap(inner: Inner),
					Empty(),
				}
				declare val o: Outer
				val result = match o {
					Outer.Wrap(Inner.Flag(true)) => "yes",
					Outer.Wrap(Inner.Num(n)) => "num",
					Outer.Empty() => "empty",
				}
			`,
			expectedErrs: []string{
				"Non-exhaustive match: Outer.Wrap is missing inner cases for Inner.Flag",
			},
		},

		// ---------------------------------------------------------------
		// Nested tuple in union (Phase 7)
		// ---------------------------------------------------------------

		// Union of tuples with boolean element, all cases covered.
		"UnionTupleBooleanElementExhaustive": {
			input: `
				type T = ["tag", boolean] | ["other", number]
				declare val t: T
				val result = match t {
					["tag", true] => "yes",
					["tag", false] => "no",
					["other", n] => n,
				}
			`,
			expectedValues: map[string]string{
				"result": `"yes" | "no" | number`,
			},
		},
		// Union of tuples with boolean element, missing false.
		"UnionTupleBooleanElementMissing": {
			input: `
				type T = ["tag", boolean] | ["other", number]
				declare val t: T
				val result = match t {
					["tag", true] => "yes",
					["other", n] => n,
				}
			`,
			expectedErrs: []string{
				`Non-exhaustive match: ["tag", boolean] is missing inner cases for ["tag", false]`,
			},
		},
		// Extractor wrapping a tuple, all combos covered.
		"ExtractorWrappingTupleExhaustive": {
			input: `
				enum Container {
					Pair(a: boolean, b: boolean),
					Empty(),
				}
				declare val c: Container
				val result = match c {
					Container.Pair(true, true) => 1,
					Container.Pair(true, false) => 2,
					Container.Pair(false, _) => 3,
					Container.Empty() => 0,
				}
			`,
			expectedValues: map[string]string{
				"result": "1 | 2 | 3 | 0",
			},
		},
		// Extractor wrapping a tuple, missing combos.
		"ExtractorWrappingTupleMissing": {
			input: `
				enum Container {
					Pair(a: boolean, b: boolean),
					Empty(),
				}
				declare val c: Container
				val result = match c {
					Container.Pair(true, true) => 1,
					Container.Pair(false, false) => 2,
					Container.Empty() => 0,
				}
			`,
			expectedErrs: []string{
				"Non-exhaustive match: Container.Pair is missing inner cases for [true, false], [false, true]",
			},
		},
		// Tuple with catch-all wildcard fully covers union member.
		"UnionTupleWildcardCovers": {
			input: `
				type T = ["a", boolean] | ["b", number]
				declare val t: T
				val result = match t {
					["a", _] => "a",
					["b", n] => n,
				}
			`,
			expectedValues: map[string]string{
				"result": `"a" | number`,
			},
		},

		// ---------------------------------------------------------------
		// Object types with all-finite properties (#436)
		// ---------------------------------------------------------------

		"ObjectAllFinitePropsExhaustive": {
			input: `
				type Flag = {kind: "flag", value: boolean}
				declare val f: Flag
				val result = match f {
					{kind: "flag", value: true} => "on",
					{kind: "flag", value: false} => "off",
				}
			`,
			expectedValues: map[string]string{
				"result": `"on" | "off"`,
			},
		},
		"ObjectAllFinitePropsMissing": {
			input: `
				type Flag = {kind: "flag", value: boolean}
				declare val f: Flag
				val result = match f {
					{kind: "flag", value: true} => "on",
				}
			`,
			expectedErrs: []string{
				"Non-exhaustive match: missing cases for {kind: \"flag\", value: false}",
			},
		},
		"ObjectAllFinitePropsWithWildcard": {
			input: `
				type Flag = {kind: "flag", value: boolean}
				declare val f: Flag
				val result = match f {
					{kind: "flag", value: true} => "on",
					_ => "off",
				}
			`,
			expectedValues: map[string]string{
				"result": `"on" | "off"`,
			},
		},
		"ObjectMultipleFiniteProps": {
			input: `
				type Cell = {row: boolean, col: boolean}
				declare val c: Cell
				val result = match c {
					{row: true, col: true} => 1,
					{row: true, col: false} => 2,
					{row: false, col: true} => 3,
					{row: false, col: false} => 4,
				}
			`,
			expectedValues: map[string]string{
				"result": "1 | 2 | 3 | 4",
			},
		},
		"ObjectMultipleFinitePropsMissing": {
			input: `
				type Cell = {row: boolean, col: boolean}
				declare val c: Cell
				val result = match c {
					{row: true, col: true} => 1,
					{row: false, col: false} => 2,
				}
			`,
			expectedErrs: []string{
				"Non-exhaustive match: missing cases for {row: true, col: false}, {row: false, col: true}",
			},
		},
		"ObjectWithPartialWildcard": {
			input: `
				type Cell = {row: boolean, col: boolean}
				declare val c: Cell
				val result = match c {
					{row: true, col} => 1,
					{row: false, col: true} => 2,
					{row: false, col: false} => 3,
				}
			`,
			expectedValues: map[string]string{
				"result": "1 | 2 | 3",
			},
		},

		// ---------------------------------------------------------------
		// Extractor wrapping a finite object (#436)
		// ---------------------------------------------------------------

		"ExtractorWrappingFiniteObjectExhaustive": {
			input: `
				type Cell = {row: boolean, col: boolean}
				enum Container {
					Wrap(value: Cell),
					Empty(),
				}
				declare val c: Container
				val result = match c {
					Container.Wrap({row: true, col: true}) => 1,
					Container.Wrap({row: true, col: false}) => 2,
					Container.Wrap({row: false, col: true}) => 3,
					Container.Wrap({row: false, col: false}) => 4,
					Container.Empty() => 0,
				}
			`,
			expectedValues: map[string]string{
				"result": "1 | 2 | 3 | 4 | 0",
			},
		},
		"ExtractorWrappingFiniteObjectMissing": {
			input: `
				type Cell = {row: boolean, col: boolean}
				enum Container {
					Wrap(value: Cell),
					Empty(),
				}
				declare val c: Container
				val result = match c {
					Container.Wrap({row: true, col: true}) => 1,
					Container.Wrap({row: false, col: false}) => 2,
					Container.Empty() => 0,
				}
			`,
			expectedErrs: []string{
				"Non-exhaustive match: Container.Wrap is missing inner cases for {row: true, col: false}, {row: false, col: true}",
			},
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

		// ---------------------------------------------------------------
		// IdentPat with type annotations
		// ---------------------------------------------------------------

		"IdentPatWithTypeAnnExhaustive": {
			input: `
				declare val x: number | string
				val result = match x {
					n: number => n,
					s: string => s,
				}
			`,
			expectedValues: map[string]string{
				"result": "number | string",
			},
		},
		"IdentPatWithTypeAnnNonExhaustive": {
			input: `
				declare val x: number | string
				val result = match x {
					n: number => n,
				}
			`,
			expectedErrs: []string{
				"Non-exhaustive match: missing cases for string",
			},
		},
		"IdentPatWithTypeAnnAndCatchAll": {
			input: `
				declare val x: number | string | boolean
				val result = match x {
					n: number => n,
					other => other,
				}
			`,
			expectedValues: map[string]string{
				"result": "number | number | string | boolean",
			},
		},

		// ---------------------------------------------------------------
		// ObjShorthandPat with type annotations
		// ---------------------------------------------------------------

		"ObjShorthandWithTypeAnnExhaustive": {
			input: `
				type Item = {kind: "a", value: number} | {kind: "b", value: string}
				declare val item: Item
				val result = match item {
					{kind: "a", value::number} => value,
					{kind: "b", value::string} => value,
				}
			`,
			expectedValues: map[string]string{
				"result": "number | string",
			},
		},
		// patternsEqual must distinguish typed idents so that
		// Container.Box(a:string) and Container.Box(b:number) are not
		// treated as duplicate patterns during redundancy detection.
		"ExtractorTypedIdentArgsNotRedundant": {
			input: `
				enum Container {
					Box(inner: string | number),
					Pair(a: number, b: number),
				}
				declare val c: Container
				val result = match c {
					Container.Box(a: string) => a,
					Container.Box(b: number) => b,
					Container.Pair(a, b) => a + b,
				}
			`,
			// no redundancy warnings expected
			expectedValues: map[string]string{
				"result": "string | number | number",
			},
		},

		// objPatternsEqual must compare TypeAnn on ObjShorthandPat
		// so that {value::string} and {value::number} are not duplicates.
		"ObjShorthandTypedNotRedundant": {
			input: `
				type Item = {kind: "a", value: string | number} | {kind: "b", data: boolean}
				declare val item: Item
				val result = match item {
					{kind: "a", value::string} => value,
					{kind: "a", value::number} => value,
					{kind: "b", data} => data,
				}
			`,
			// no redundancy warnings expected
			expectedValues: map[string]string{
				"result": "string | number | boolean",
			},
		},

		// When the shorthand type annotation matches the member's property
		// type exactly, a duplicate branch should still be flagged redundant.
		"ObjShorthandTypedDuplicateIsRedundant": {
			input: `
				type T = {value: number}
				declare val t: T
				val result = match t {
					{value::number} => value,
					{value::number} => value,
				}
			`,
			expectedWarns: []string{
				"Redundant match branch: this case is already covered by earlier branches",
			},
			expectedValues: map[string]string{
				"result": "number | number",
			},
		},

		// When the shorthand type annotation matches the member's property
		// type exactly, a single branch should be recognized as exhaustive.
		"ObjShorthandTypedMatchesPropertyType": {
			input: `
				type T = {value: number}
				declare val t: T
				val result = match t {
					{value::number} => value,
				}
			`,
			expectedValues: map[string]string{
				"result": "number",
			},
		},

		"ObjShorthandWithTypeAnnNonExhaustive": {
			input: `
				type Item = {kind: "a", value: number} | {kind: "b", value: string}
				declare val item: Item
				val result = match item {
					{kind: "a", value::number} => value,
				}
			`,
			expectedErrs: []string{
				"Non-exhaustive match: {kind: \"b\", value: string} is missing inner cases for \"b\"",
			},
		},

		// ---------------------------------------------------------------
		// Tuple-union with typed IdentPat
		// ---------------------------------------------------------------

		// [x: string] should NOT match a [number] union member.
		"TupleUnionTypedIdentNotCatchAll": {
			input: `
				type T = [number] | [string]
				declare val t: T
				val result = match t {
					[x: string] => x,
				}
			`,
			expectedErrs: []string{
				"Non-exhaustive match: missing cases for [number]",
			},
		},
		"TupleUnionTypedIdentExhaustive": {
			input: `
				type T = [number] | [string]
				declare val t: T
				val result = match t {
					[x: string] => x,
					[n: number] => n,
				}
			`,
			expectedValues: map[string]string{
				"result": "string | number",
			},
		},

		// ---------------------------------------------------------------
		// typeAnnsMatchForEquality symmetry
		// ---------------------------------------------------------------

		// Patterns with boolean vs true type annotations should not be
		// considered equal (boolean covers true, but not vice versa).
		"TypedIdentBooleanVsTrueNotRedundant": {
			input: `
				declare val x: boolean
				val result = match x {
					a: boolean => a,
					b: boolean => b,
				}
			`,
			expectedWarns: []string{
				"Redundant match branch: this case is already covered by earlier branches",
			},
			expectedValues: map[string]string{
				"result": "boolean | boolean",
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			actualTypes, inferErrors := inferModuleTypesAndErrors(t, test.input)

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

			// Check errors: exact set match (no missing, no unexpected).
			assert.ElementsMatch(t, test.expectedErrs, errMessages(errors),
				"error messages should match exactly")

			// Check warnings: exact set match (no missing, no unexpected).
			assert.ElementsMatch(t, test.expectedWarns, errMessages(warnings),
				"warning messages should match exactly")

			// Check that NoExhaustivenessCheckWhenPatternErrors does NOT
			// produce exhaustiveness errors.
			if name == "NoExhaustivenessCheckWhenPatternErrors" {
				for _, err := range errors {
					assert.False(t, strings.Contains(err.Message(), "Non-exhaustive match"),
						"should not report exhaustiveness error when prior errors exist")
				}
			}

			// Check expected inferred types.
			for key, expected := range test.expectedValues {
				assert.Equal(t, expected, actualTypes[key])
			}
		})
	}
}
