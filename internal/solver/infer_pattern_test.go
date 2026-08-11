package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// --- M4 E1: structural destructuring patterns ---

// An object pattern in a `val` binds each named field at its field type. The
// function below reads the bound names back, so the inferred return shows the
// binding worked.
func TestInferValObjectPatternBinds(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(p: {x: number, y: string}) {
			val {x, y} = p
			return [x, y]
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: {x: number, y: string}) -> [number, string]", values["f"])
}

// An object pattern may bind a SUBSET of the scrutinee's fields: the per-field
// requirement is inexact ("has at least this field"), so the unmentioned `y` is
// tolerated.
func TestInferValObjectPatternPartial(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(p: {x: number, y: string}) {
			val {x} = p
			return x
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: {x: number, y: string}) -> number", values["f"])
}

// Destructuring a field the scrutinee lacks is a MissingPropertyError, blamed at
// the pattern field.
func TestInferValObjectPatternMissingField(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f(p: {x: number}) {
			val {z} = p
			return z
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "2:11-2:22: object is missing property: z", msgWithSpan(errs[0]))
}

// A tuple pattern binds per element at the element's type. Reordering the bound
// names in the result confirms each position bound the right element.
func TestInferValTuplePatternBinds(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn h(t: [number, string]) {
			val [a, b] = t
			return [b, a]
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (t: [number, string]) -> [string, number]", values["h"])
}

// A tuple pattern is exact in arity: binding more or fewer elements than the
// scrutinee has is a TupleLengthMismatchError.
func TestInferValTuplePatternWrongArity(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn h(t: [number, string]) {
			val [a, b, c] = t
			return a
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "2:11-2:27: cannot constrain tuple of length 2 <: tuple of length 3", msgWithSpan(errs[0]))
}

// A destructuring parameter types like a `val` destructuring of the argument: the
// leaves bind, and the parameter renders its pattern.
func TestInferObjectPatternParam(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn g({x, y}: {x: number, y: string}) {
			return [x, y]
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn ({x, y}: {x: number, y: string}) -> [number, string]", values["g"])
}

// An UN-annotated destructuring parameter infers its shape from the leaves' uses
// (usage-based inference), closing the coalesced object to exact (Policy A).
func TestInferObjectPatternParamInferred(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn g({a, b}) {
			return [a, b]
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn <T0, T1>({a, b}: {a: T0, b: T1}) -> [T0, T1]", values["g"])
}

// Patterns nest: an object pattern whose field is itself an object pattern binds
// the inner leaves at the nested field types.
func TestInferNestedObjectPattern(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(p: {pt: {x: number, y: string}}) {
			val {pt: {x, y}} = p
			return [x, y]
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: {pt: {x: number, y: string}}) -> [number, string]", values["f"])
}

// A wildcard element in a tuple pattern matches without binding a name.
func TestInferTuplePatternWildcard(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(t: [number, string]) {
			val [a, _] = t
			return a
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (t: [number, string]) -> number", values["f"])
}

// A leaf type annotation is enforced: annotating a field whose scrutinee type
// conflicts is a constraint error, not a silently dropped annotation.
func TestInferObjectPatternLeafTypeAnnConflict(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f(p: {x: number}) {
			val {x :: string} = p
			return x
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "3:9-3:20: cannot constrain number <: string", msgWithSpan(errs[0]))
}

// A matching leaf type annotation checks and is adopted as the leaf's type.
func TestInferObjectPatternLeafTypeAnnAdopted(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(p: {x: number}) {
			val {x :: number} = p
			return x
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: {x: number}) -> number", values["f"])
}

// A field default makes the field optional, so destructuring an absent field
// that carries a default binds the default's type instead of reporting a missing
// property.
//
// The scrutinee here cannot carry `z` at all, which #1053 argues should be a missing
// property rather than a match. TestPathBinderDefaultedKeyBindsAgainstScrutineeWithoutTheField
// in ucs_bind_test.go pins the same answer for the UCS IR path binder, so change the two
// together.
func TestInferObjectPatternLeafDefault(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(p: {x: number}) {
			val {z = 0} = p
			return z
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: {x: number}) -> 0", values["f"])
}

// An optional property reads as `T | undefined`, and both object-pattern forms bind that
// whole type. The key-value form used to pin its projection's upper bound to the declared
// `T` even for an optional property, which rejected the `undefined` half and reported
// `cannot constrain undefined <: number` on a legal destructuring. The shorthand form
// pins nothing, so the two disagreed on the same field.
func TestInferObjectPatternOptionalProperty(t *testing.T) {
	tests := map[string]string{
		"Shorthand": `
			fn f(p: {x?: number}) {
				val {x} = p
				return x
			}`,
		"KeyValue": `
			fn f(p: {x?: number}) {
				val {x: a} = p
				return a
			}`,
	}
	for name, src := range tests {
		t.Run(name, func(t *testing.T) {
			values, _, errs := inferSource(t, src)
			require.Empty(t, messagesWithSpan(errs))
			require.Equal(t, "fn (p: {x?: number}) -> number | undefined", values["f"])
		})
	}
}

// A trailing rest element relaxes the tuple requirement to an inexact prefix, so
// the fixed elements bind without a spurious arity error.
func TestInferTuplePatternRestPrefix(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(t: [number, string, boolean]) {
			val [a, ...rest] = t
			return a
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (t: [number, string, boolean]) -> number", values["f"])
}

// --- M9 rest patterns: a `...rest` element binds the leftover ---

// A tuple rest binds the elements past the fixed prefix, gathered into a tuple, and an
// object rest binds the properties the pattern's fields do not name. Each case returns
// the bound rest, so the function's return type is the inferred rest type. The three
// binding sites — a `val`, a destructuring parameter, and a `match` arm — share
// bindPattern, so each one is covered here.
func TestInferRestPatternBinds(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			// The suffix of an exact tuple is exact: two elements remain past `a`.
			name: "TupleValDestructure",
			src: `
				fn f(t: [number, string, boolean]) {
					val [a, ...rest] = t
					return rest
				}`,
			want: "fn (t: [number, string, boolean]) -> [string, boolean]",
		},
		{
			// A destructuring parameter binds the same way and renders its `...rest`
			// element back into the parameter's pattern.
			name: "TupleParam",
			src: `
				fn f([a, ...rest]: [number, string, boolean]) {
					return rest
				}`,
			want: "fn ([a, ...rest]: [number, string, boolean]) -> [string, boolean]",
		},
		{
			// A `match` arm binds against the same scrutinee a `val` would. The arm is
			// irrefutable over an exact tuple, so no catch-all is needed.
			name: "TupleMatchArm",
			src: `
				fn f(t: [number, string, boolean]) {
					return match t {
						[a, ...rest] => rest
					}
				}`,
			want: "fn (t: [number, string, boolean]) -> [string, boolean]",
		},
		{
			// An inexact scrutinee hands its open `...` tail to the suffix, since the
			// elements the tail hides are part of the leftover.
			name: "TupleInexactScrutinee",
			src: `
				fn f(t: [number, string, ...]) {
					val [a, ...rest] = t
					return rest
				}`,
			want: "fn (t: [number, string, ...]) -> [string, ...]",
		},
		{
			// A rest that consumes every remaining element binds the empty tuple.
			name: "TupleRestBindsEmptySuffix",
			src: `
				fn f(t: [number]) {
					val [a, ...rest] = t
					return rest
				}`,
			want: "fn (t: [number]) -> []",
		},
		{
			// The binding mode propagates into a nested pattern, so an inner rest binds
			// the inner tuple's suffix.
			name: "TupleNestedRest",
			src: `
				fn f(t: [number, [string, boolean, number]]) {
					val [a, [b, ...rest]] = t
					return rest
				}`,
			want: "fn (t: [number, [string, boolean, number]]) -> [boolean, number]",
		},
		{
			// The rest's sub-pattern is bound through the same walk as a fixed element,
			// so `...[b, c]` destructures the suffix in turn.
			name: "TupleRestSubPatternDestructures",
			src: `
				fn f(t: [number, string, boolean]) {
					val [a, ...[b, c]] = t
					return [c, b]
				}`,
			want: "fn (t: [number, string, boolean]) -> [boolean, string]",
		},
		{
			name: "ObjectValDestructure",
			src: `
				fn f(p: {x: number, y: string, z: boolean}) {
					val {x, ...rest} = p
					return rest
				}`,
			want: "fn (p: {x: number, y: string, z: boolean}) -> {y: string, z: boolean}",
		},
		{
			name: "ObjectParam",
			src: `
				fn f({x, ...rest}: {x: number, y: string}) {
					return rest
				}`,
			want: "fn ({x, ...rest}: {x: number, y: string}) -> {y: string}",
		},
		{
			name: "ObjectMatchArm",
			src: `
				fn f(p: {x: number, y: string}) {
					return match p {
						{x, ...rest} => rest
					}
				}`,
			want: "fn (p: {x: number, y: string}) -> {y: string}",
		},
		{
			// A leftover member carries through untouched, so it keeps the `?` and
			// `readonly` markers the scrutinee wrote.
			name: "ObjectRestKeepsMarkers",
			src: `
				fn f(p: {x: number, y?: string, readonly z: boolean}) {
					val {x, ...rest} = p
					return rest
				}`,
			want: "fn (p: {x: number, y?: string, readonly z: boolean}) -> {y?: string, readonly z: boolean}",
		},
		{
			// A key-value field names its key just as a shorthand does, so `{x: a}`
			// removes x from the leftover even though the leaf is named a.
			name: "ObjectKeyValueFieldNamesItsKey",
			src: `
				fn f(p: {x: number, y: string}) {
					val {x: a, ...rest} = p
					return rest
				}`,
			want: "fn (p: {x: number, y: string}) -> {y: string}",
		},
		{
			// An inexact scrutinee passes its open `...` tail on, since the properties
			// the tail hides belong to the leftover too.
			name: "ObjectInexactScrutinee",
			src: `
				fn f(p: {x: number, y: string, ...}) {
					val {x, ...rest} = p
					return rest
				}`,
			want: "fn (p: {x: number, y: string, ...}) -> {y: string, ...}",
		},
		{
			// A rest that names no leftover property binds the empty object.
			name: "ObjectRestBindsEmptyLeftover",
			src: `
				fn f(p: {x: number}) {
					val {x, ...rest} = p
					return rest
				}`,
			want: "fn (p: {x: number}) -> {}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, values["f"])
		})
	}
}

// A rest of a borrowed scrutinee is a borrow of the leftover, bounded by the
// scrutinee's lifetime, the same projection the sibling leaves take. Each case pins the
// rest against a borrow return annotation, so an owned or wrongly-mutable rest would
// fail that constraint and acceptance confirms the projection. That is the same
// technique TestDestructureMutBorrowLeafRendersBorrow uses, and for the same reason: a
// returned `&mut` leaf does not survive the return-join cleanly enough to assert on its
// rendered form.
func TestInferRestPatternBorrowMode(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "MutBorrowTupleRest",
			src: `
				fn f(line: &mut [{x: number}, {y: number}, {z: number}]) -> &mut [{y: number}, {z: number}] {
					val [a, ...rest] = line
					return rest
				}`,
			want: "fn <'a>(line: &'a mut [{x: number}, {y: number}, {z: number}]) -> &'a mut [{y: number}, {z: number}]",
		},
		{
			name: "SharedBorrowTupleRest",
			src: `
				fn f(line: &[{x: number}, {y: number}]) -> &[{y: number}] {
					val [a, ...rest] = line
					return rest
				}`,
			want: "fn <'a>(line: &'a [{x: number}, {y: number}]) -> &'a [{y: number}]",
		},
		{
			name: "MutBorrowObjectRest",
			src: `
				fn f(pt: &mut {a: {x: number}, b: {y: number}}) -> &mut {b: {y: number}} {
					val {a, ...rest} = pt
					return rest
				}`,
			want: "fn <'a>(pt: &'a mut {a: {x: number}, b: {y: number}}) -> &'a mut {b: {y: number}}",
		},
		{
			name: "SharedBorrowObjectRest",
			src: `
				fn f(pt: &{a: {x: number}, b: {y: number}}) -> &{b: {y: number}} {
					val {a, ...rest} = pt
					return rest
				}`,
			want: "fn <'a>(pt: &'a {a: {x: number}, b: {y: number}}) -> &'a {b: {y: number}}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, values["f"])
		})
	}
}

// A `&mut` object rest is a MUTABLE borrow of the leftover, so a write through it
// succeeds without any `mut` marker on the rest, following the same Rust match
// ergonomics the sibling leaves follow.
func TestInferRestPatternMutBorrowAcceptsWrite(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(pt: &mut {a: {x: number}, b: {y: number}}) {
			val {a, ...rest} = pt
			rest.b = {y: 5}
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (pt: &mut {a: {x: number}, b: {y: number}}) -> undefined", values["f"])
}

// A scrutinee the pattern cannot read members off directly still yields a leftover. An
// object rest grounds its scrutinee through the same groundToObject an object spread's
// operand uses, so an alias, a class instance, or a mapped type resolves to the members
// the leftover keeps. A tuple rest instead slices the elements it was given, so a `...P`
// spread element in the suffix carries through unspliced.
func TestInferRestPatternGroundsScrutinee(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			// A `...P` spread splices before anything is read by index, so the rest binds
			// the elements the spread stands for rather than the spread itself.
			name: "TupleSpreadOverAlias",
			src: `
				type Pair = [string, boolean]
				fn f(t: [number, ...Pair]) {
					val [a, ...rest] = t
					return rest
				}`,
			want: "fn (t: [number, ...Pair]) -> [string, boolean]",
		},
		{
			// Splicing puts a real element at every index, so a fixed element may sit at a
			// position the spread contributed. b reads `string` out of `...Pair` and the
			// rest takes what is left of it.
			name: "TupleFixedElementInsideSpread",
			src: `
				type Pair = [string, boolean]
				fn f(t: [number, ...Pair]) {
					val [a, b, ...rest] = t
					return [a, b, rest]
				}`,
			want: "fn (t: [number, ...Pair]) -> [number, string, [boolean]]",
		},
		{
			// A prefix consuming every spliced element leaves the empty tuple.
			name: "TupleFixedPrefixConsumesSpread",
			src: `
				type Pair = [string, boolean]
				fn f(t: [number, ...Pair]) {
					val [a, b, c, ...rest] = t
					return [c, rest]
				}`,
			want: "fn (t: [number, ...Pair]) -> [boolean, []]",
		},
		{
			// Splicing recurses, so a spread whose operand itself spreads still resolves.
			name: "TupleNestedSpreadOverAlias",
			src: `
				type Inner = [boolean]
				type Pair = [string, ...Inner]
				fn f(t: [number, ...Pair]) {
					val [a, b, ...rest] = t
					return rest
				}`,
			want: "fn (t: [number, ...Pair]) -> [boolean]",
		},
		{
			// An inexact spread operand splices its known prefix and passes its open tail
			// to the suffix.
			name: "TupleInexactSpreadOperand",
			src: `
				type Open = [string, ...]
				fn f(t: [number, ...Open]) {
					val [a, ...rest] = t
					return rest
				}`,
			want: "fn (t: [number, ...Open]) -> [string, ...]",
		},
		{
			name: "ObjectAliasScrutinee",
			src: `
				type Rec = {x: number, y: string, z: boolean}
				fn f(p: Rec) {
					val {x, ...rest} = p
					return rest
				}`,
			want: "fn (p: Rec) -> {y: string, z: boolean}",
		},
		{
			// A class instance grounds to its projected body, which is inexact, so the
			// leftover keeps the open tail.
			name: "ObjectClassInstanceScrutinee",
			src: `
				class Point {
					x: number,
					y: string,
				}
				fn f(p: Point) {
					val {x, ...rest} = p
					return rest
				}`,
			want: "fn (p: Point) -> {y: string, ...}",
		},
		{
			// The scrutinee is a mapped type, which grounds to the members it computes,
			// so the leftover keeps the `?` marker `Partial` added.
			name: "ObjectMappedScrutinee",
			src: `
				type Point = {x: number, y: string}
				type Partial<T> = {[K]?: T[K] for K in keyof T}
				fn f(p: Partial<Point>) {
					val {x, ...rest} = p
					return rest
				}`,
			want: "fn (p: Partial<Point>) -> {y?: string}",
		},
		{
			// A method member names a key, so it lands in the leftover as a method rather
			// than flattening into a property.
			name: "ObjectMethodMemberSurvives",
			src: `
				fn f(p: {x: number, m(a: number) -> string}) {
					val {x, ...rest} = p
					return rest
				}`,
			want: "fn (p: {x: number, m(a: number) -> string}) -> {m(a: number) -> string}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, values["f"])
		})
	}
}

// A top-level destructuring reads the same leftover a body-level one does. Its
// initializer is a reference to another module-level binding, which types as that
// binding's variable rather than as the annotation, so the rest resolves the shape off
// the variable's bounds. Each case checks the leaf types the module driver recorded.
func TestInferRestPatternTopLevelDestructure(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want map[string]string
	}{
		{
			name: "Tuple",
			src: `
				val tup: [number, string, boolean] = [1, "a", true]
				val [a, ...r] = tup`,
			want: map[string]string{"a": "number", "r": "[string, boolean]"},
		},
		{
			// The rest's own sub-pattern destructures the suffix, so it needs the suffix
			// resolved rather than a bare variable.
			name: "TupleRestSubPattern",
			src: `
				val tup: [number, string, boolean] = [1, "a", true]
				val [a, ...[b, c]] = tup`,
			want: map[string]string{"a": "number", "b": "string", "c": "boolean"},
		},
		{
			name: "Object",
			src: `
				val rec: {x: number, y: string} = {x: 1, y: "a"}
				val {x, ...rest} = rec`,
			want: map[string]string{"x": "number", "rest": "{y: string}"},
		},
		{
			name: "MatchArm",
			src: `
				val tup: [number, string, boolean] = [1, "a", true]
				val out = match tup {
					[a, ...rest] => rest
				}`,
			want: map[string]string{"out": "[string, boolean]"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
			for name, want := range tt.want {
				require.Equal(t, want, values[name], "binding %s", name)
			}
		})
	}
}

// A scrutinee with no statically known shape leaves the leftover's CONTENTS unknowable,
// but not its kind. The rest of a tuple pattern is still some tuple, and the rest of an
// object pattern still some object, so each binds a variable bounded below by that kind's
// top: the empty inexact tuple `[...]` and the empty inexact object `{...}`. Every tuple
// is a subtype of `[...]` and every object of `{...}`, so the bound is the join over every
// leftover a caller could produce.
//
// The bound is what the leftover reads as, not merely how it renders. Without it the
// variable carries no bound at all and coalesces to `never` in a return position, which
// claims the function does not return and lets a caller assign the result to anything.
// `[...]` is also the tightest sound answer. The exact empty tuple `[]` would be wrong,
// since a caller passing `[1, "x"]` makes the leftover `["x"]`, which is not a subtype of
// `[]`. Naming the leftover exactly needs the parameter's arity, which only an annotation
// supplies; see TestInferRestPatternBinds.
//
// Two more scrutinees reach the same bounded variable, both already reported as constraint
// errors by the requirement the fixed prefix emits. TestInferTuplePatternRestFallbackGuards
// below pins that recovery.
func TestInferRestPatternUnknownScrutineeShape(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "Tuple",
			src: `
				fn f([a, ...rest]) {
					return rest
				}`,
			want: "fn ([a, ...rest]: [unknown, ...]) -> [...]",
		},
		{
			// The parameter is inexact because the pattern names a rest. See
			// TestInferRestPatternParamAdmitsExtraFields.
			name: "Object",
			src: `
				fn f({x, ...rest}) {
					return rest
				}`,
			want: "fn ({x, ...rest}: {x: unknown, ...}) -> {...}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, values["f"])
		})
	}
}

// The leftover's kind bound is load-bearing at the use site, not just in rendering. A
// return of the unknown leftover is checked against the caller's expectation instead of
// passing as `never`, and destructuring more elements out of it than its kind guarantees
// is rejected. The rejection is correct rather than a limitation: nothing in
// `fn f(p) { val [a, ...rest] = p }` stops a caller from passing `[1]`, which makes the
// leftover empty, so `val [b, c] = rest` cannot be justified. Annotating the parameter
// gives the leftover a length and the same destructuring checks.
func TestInferRestPatternUnknownLeftoverUses(t *testing.T) {
	t.Run("returning the leftover is checked against the caller", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			fn f([a, ...rest]) { return rest }
			val bad: string = f([1, 2])
		`)
		require.Len(t, errs, 1)
		require.Equal(t, "3:22-3:31: cannot constrain tuple <: string", msgWithSpan(errs[0]))
	})

	t.Run("destructuring past the leftover's guaranteed length is rejected", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			fn f(p) {
				val [a, ...rest] = p
				val [b, c] = rest
				return c
			}
		`)
		require.Len(t, errs, 1)
		require.Equal(t, "4:9-4:15: cannot constrain tuple of length 0 <: tuple of length 2", msgWithSpan(errs[0]))
	})

	t.Run("reading a field off the leftover is rejected", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			fn f(p) {
				val {x, ...rest} = p
				return rest.y
			}
		`)
		require.Len(t, errs, 1)
		require.Equal(t, "4:17-4:18: object is missing property: y", msgWithSpan(errs[0]))
	})

	t.Run("an annotated scrutinee gives the leftover a length", func(t *testing.T) {
		values, _, errs := inferSource(t, `
			fn f(p: [number, string, boolean]) {
				val [a, ...rest] = p
				val [b, c] = rest
				return c
			}
		`)
		require.Empty(t, errs)
		require.Equal(t, "fn (p: [number, string, boolean]) -> boolean", values["f"])
	})
}

// An un-annotated parameter whose pattern names a `...rest` stays row-polymorphic, so a
// caller may pass an object carrying the properties the rest is there to collect. Policy A
// closes a usage-inferred object to exact, which would reject every such argument and
// leave the rest nothing to bind, so a rest opts the parameter out of that close the same
// way the written `open` marker does. A pattern with no rest still closes to exact.
func TestInferRestPatternParamAdmitsExtraFields(t *testing.T) {
	t.Run("a rest admits extra fields", func(t *testing.T) {
		values, _, errs := inferSource(t, `
			fn f({x, ...rest}) { return x }
			val r = f({x: 1, y: 2})
		`)
		require.Empty(t, errs)
		require.Equal(t, "fn <T0>({x, ...rest}: {x: T0, ...}) -> T0", values["f"])
	})

	t.Run("no rest still closes to exact", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			fn f({x}) { return x }
			val r = f({x: 1, y: 2})
		`)
		require.Len(t, errs, 1)
		require.Equal(t, "3:24-3:25: object has extra property: y", msgWithSpan(errs[0]))
	})

	t.Run("a tuple rest already admits a longer tuple", func(t *testing.T) {
		values, _, errs := inferSource(t, `
			fn f([a, ...rest]) { return a }
			val r = f([1, 2, 3])
		`)
		require.Empty(t, errs)
		require.Equal(t, "fn <T0>([a, ...rest]: [T0, ...]) -> T0", values["f"])
	})
}

// tupleRestType cuts the suffix out of the scrutinee's spliced element list, so it falls
// back to the `[...]`-bounded variable on the two shapes where that cut has no meaning.
//
//   - A scrutinee shorter than the fixed prefix has no elements left to cut. The guard is
//     what keeps the slice in range.
//   - A tuple whose spread never splices, as a spread over a type parameter does not, is
//     unreadable by index at all, so groundedTuple reports no shape.
//
// Neither case is diagnostic-free. The requirement the fixed prefix emits already rejects
// both, so the bounded variable is recovery on an erroring pattern rather than an inferred
// answer. Each case asserts that single error and confirms the rest still binds a name.
//
// A spread that DOES splice is not a fallback at all. It contributes real elements at real
// indices, which TestInferRestPatternGroundsScrutinee covers.
func TestInferTuplePatternRestFallbackGuards(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			// A two-element prefix against a one-element scrutinee leaves nothing to cut.
			name: "ScrutineeShorterThanFixedPrefix",
			src: `
				fn f(t: [number]) {
					val [a, b, ...rest] = t
					return rest
				}`,
			want: "2:13-2:21: cannot constrain tuple of length 1 <: tuple of length 2",
		},
		{
			// `...P` over a type parameter never splices, so no position after it is fixed
			// and the tuple cannot be read by index.
			name: "SpreadOverTypeParamNeverSplices",
			src: `
				fn f<P>(t: [number, ...P]) {
					val [a, ...rest] = t
					return rest
				}`,
			want: "3:10-3:22: cannot constrain [number, ...t1] <: tuple",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Len(t, errs, 1)
			require.Equal(t, tt.want, msgWithSpan(errs[0]))
			// The rest still binds, so `return rest` resolves the name rather than
			// cascading into an unknown-identifier error on top of the constraint error.
			require.NotContains(t, values["f"], "error")
		})
	}
}

// A rest the walk cannot place is reported once and recovered against a fresh variable,
// so its leaves stay defined and a later reference resolves rather than cascading into an
// unknown-identifier error. Each case asserts the single reported error and reads a leaf
// of the rejected rest back.
func TestInferRestPatternRejectedPositions(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			// A tuple rest before another element stands for a run of elements whose
			// length nothing pins down, so no position after it names a fixed element.
			name: "TupleNonTrailingRest",
			src: `
				fn f(t: [number, string, boolean]) {
					val [...rest, b] = t
					return [rest, b]
				}`,
			want: "3:11-3:18: Unsupported: RestPat",
		},
		{
			// An object rest must come last, the position JavaScript's own destructuring
			// requires, even though the leftover itself does not depend on the order.
			name: "ObjectNonTrailingRest",
			src: `
				fn f(p: {x: number, y: string}) {
					val {...rest, x} = p
					return rest
				}`,
			want: "3:11-3:18: Unsupported: ObjRestPat",
		},
		{
			// A second rest has no leftover of its own, since the first already takes every
			// property the fields do not name. The non-final one is the rejected one.
			name: "ObjectSecondRest",
			src: `
				fn f(p: {x: number, y: string}) {
					val {x, ...rest, ...more} = p
					return [rest, more]
				}`,
			want: "3:14-3:21: Unsupported: ObjRestPat",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Len(t, errs, 1)
			require.Equal(t, tt.want, msgWithSpan(errs[0]))
		})
	}
}

// A function-type annotation mirrors its parameter patterns into soltype so the type
// reads back the way the source wrote it. A rest element survives that round trip in both
// the tuple and the object form, and a rest whose sub-pattern has no soltype counterpart
// still renders its `...` with the wildcard placeholder rather than disappearing. A regex
// literal is such a sub-pattern: soltype has no LitType for one, so mirrorParamPat
// returns nil for it.
func TestMirrorParamPatRestRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "TupleRest",
			src:  `type F = fn ([a, ...rest]: [number, string]) -> number`,
			want: "fn ([a, ...rest]: [number, string]) -> number",
		},
		{
			name: "ObjectRest",
			src:  `type F = fn ({x, ...rest}: {x: number, y: string}) -> number`,
			want: "fn ({x, ...rest}: {x: number, y: string}) -> number",
		},
		{
			name: "ObjectRestDestructures",
			src:  `type F = fn ({x, ...{y}}: {x: number, y: string}) -> number`,
			want: "fn ({x, ...{y}}: {x: number, y: string}) -> number",
		},
		{
			name: "TupleRestWithoutMirror",
			src:  `type F = fn ([a, .../ab/]: [number, string]) -> number`,
			want: "fn ([a, ..._]: [number, string]) -> number",
		},
		{
			name: "ObjectRestWithoutMirror",
			src:  `type F = fn ({x, .../ab/}: {x: number, y: string}) -> number`,
			want: "fn ({x, ..._}: {x: number, y: string}) -> number",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, _, errs := inferTypeNodes(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, soltype.Print(nodes["F"]))
		})
	}
}

// A closure capturing a destructured leaf resolves the leaf's binding. This
// exercises the liveness wiring: the leaf's rename-assigned VarID is copied onto
// its binding, so trackCapturedAliases finds it instead of skipping a VarID-0
// binding.
func TestInferDestructuredLeafClosureCapture(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(p: {x: number}) {
			val {x} = p
			val g = fn () { return x }
			return g()
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: {x: number}) -> number", values["f"])
}

// A `var` tuple destructuring widens each leaf to its primitive, the B3 widening
// applied through the initializer.
func TestInferVarTupleDestructureWidens(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f() {
			var [a, b] = [1, 2]
			return [a, b]
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn () -> [number, number]", values["f"])
}

// A `var` object destructuring widens its leaf the same way.
func TestInferVarObjectDestructureWidens(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f() {
			var {x} = {x: 5}
			return x
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn () -> number", values["f"])
}

// Destructuring a `mut` borrow scrutinee peels the borrow via CarrierOf and binds
// the borrowed field values, just as a member read through the borrow would.
func TestInferDestructureBorrowScrutinee(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(p: mut {x: number, y: string}) {
			val {x, y} = p
			return [x, y]
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: mut {x: number, y: string}) -> [number, string]", values["f"])
}

// A default is checked against an explicit leaf annotation: a default that the
// annotation rejects is a constraint error.
func TestInferObjectPatternLeafDefaultViolatesAnnotation(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f(p: {x: number}) {
			val {x :: number = "hi"} = p
			return x
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, `3:23-3:27: cannot constrain "hi" <: number`, msgWithSpan(errs[0]))
}

// --- M4 E2: the `match` expression ---

// A match over a structural pattern binds the pattern's leaves and types the arm
// body against them. An exact-object scrutinee with a matching structural arm is
// exhaustive without a catch-all.
func TestInferMatchBindsArm(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(p: {x: number, y: string}) {
			return match p {
				{x, y} => [x, y]
			}
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: {x: number, y: string}) -> [number, string]", values["f"])
}

// An unannotated param used only as a match scrutinee infers its shape from the arm
// patterns, the same usage-based inference a member read drives. Each arm's pattern
// emits its member-lookup requirements onto the scrutinee var. A bound field the body
// never uses lands as `unknown`, and the inferred object closes to exact (Policy A).
func TestInferMatchParamUsageObject(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(p) {
			return match p {
				{x, y} => x,
				_ => 0
			}
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn <T0>(p: {x: T0, y: unknown}) -> T0 | 0", values["f"])
}

// The same usage inference applies through a tuple pattern: the scrutinee infers a
// tuple whose arity is the pattern's and whose unused element lands as `unknown`.
func TestInferMatchParamUsageTuple(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(p) {
			return match p {
				[a, b] => a
			}
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn <T0>(p: [T0, unknown]) -> T0", values["f"])
}

// Every non-diverging arm body joins into one branch-union result, exactly as an
// if/else joins its two branches. A non-structural scrutinee such as `number` is
// not subject to the exactness exhaustiveness check, so the literal-pattern arms
// need no extra catch-all to type.
func TestInferMatchJoinsArms(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(n: number) {
			return match n {
				1 => "one",
				_ => "other"
			}
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, `fn (n: number) -> "one" | "other"`, values["f"])
}

// An exact-object scrutinee whose structural arm matches its shape needs no
// catch-all.
func TestInferMatchExactNeedsNoCatchAll(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(p: {x: number, y: number}) {
			return match p {
				{x, y} => x
			}
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: {x: number, y: number}) -> number", values["f"])
}

// An object pattern may name a subset of the scrutinee's fields. `{x}` over the exact
// two-field `{x: number, y: string}` binds `x` and ignores `y`. The pattern is irrefutable,
// so it covers the scrutinee and the match needs no catch-all. Coverage requires only that
// the scrutinee carry every field the pattern names, not that the pattern name every field.
func TestInferMatchPartialObjectPatternCovers(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(p: {x: number, y: string}) {
			return match p {
				{x} => x
			}
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: {x: number, y: string}) -> number", values["f"])
}

// An inexact-object scrutinee carries an open tail of unknown values, so a
// structural arm does not cover it. A missing catch-all is a NonExhaustiveMatchError.
func TestInferMatchInexactNeedsCatchAll(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f(p: {x: number, y: number, ...}) {
			return match p {
				{x, y} => x
			}
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "3:11-5:5: match is not exhaustive; add a catch-all branch", msgWithSpan(errs[0]))
}

// An inexact-object scrutinee with an unguarded catch-all arm is exhaustive.
// The arms return `x`, a `number` field, and the literal `0`. The join is
// `number | 0`, which the finalization subsumption pass collapses to `number`
// since `0 <: number`.
func TestInferMatchInexactWithCatchAll(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(p: {x: number, y: number, ...}) {
			return match p {
				{x, y} => x,
				_ => 0
			}
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: {x: number, y: number, ...}) -> number", values["f"])
}

// An exact union scrutinee whose arms cover every member needs no catch-all. An
// if/else over two string literals infers the exact union `"a" | "b"`, and the two
// literal patterns match one member each, so the arms together are exhaustive.
func TestInferMatchExactUnionCoversMembers(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(b: boolean) {
			val x = if b { "a" } else { "b" }
			return match x {
				"a" => 1,
				"b" => 2
			}
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, `fn (b: boolean) -> 1 | 2`, values["f"])
}

// An exact union scrutinee with a member left uncovered is non-exhaustive. The
// arms match `"a"` only, so `"b"` escapes and the message names it.
func TestInferMatchExactUnionMissingMember(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f(b: boolean) {
			val x = if b { "a" } else { "b" }
			return match x {
				"a" => 1
			}
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "4:11-6:5: match is not exhaustive; add a branch for `\"b\"`", msgWithSpan(errs[0]))
}

// A guarded arm binds its pattern before its guard runs, so a literal pattern in a
// guarded arm records its literal as a lower bound on the scrutinee. Exhaustiveness
// reads the scrutinee's shape from before any arm binds, so a guarded arm's foreign
// literal does not leak into the union as a phantom uncovered member. The two
// unguarded arms cover `"a" | "b"`, so the match is exhaustive despite the guarded
// `"z"` arm.
func TestInferMatchExactUnionGuardedArmNoPhantomMember(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f(b: boolean) {
			val x = if b { "a" } else { "b" }
			return match x {
				"a" => 1,
				"b" => 2,
				"z" if b => 3
			}
		}
	`)
	require.Empty(t, errs)
}

// An inexact union scrutinee carries an open tail of unknown values, so covering
// every listed member is not enough — only an unguarded catch-all makes it
// exhaustive. The scrutinee is annotated inexact through a binding. The message
// names the uncovered members alongside the catch-all, since each still takes a
// branch of its own.
func TestInferMatchInexactUnionNeedsCatchAll(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f(b: boolean) {
			val x: number | string | ... = if b { 1 } else { "b" }
			return match x {
				1 => 1,
				"b" => 2
			}
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "4:11-7:5: match is not exhaustive; add branches for `number`, `string`, and a catch-all branch", msgWithSpan(errs[0]))
}

// An inexact union scrutinee with an unguarded catch-all arm is exhaustive.
func TestInferMatchInexactUnionWithCatchAll(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f(b: boolean) {
			val x: number | string | ... = if b { 1 } else { "b" }
			return match x {
				1 => 1,
				"b" => 2,
				_ => 0
			}
		}
	`)
	require.Empty(t, errs)
}

// A tuple pattern `[a, c]` over the union `[1, 2] | [3, 4]`. Both members are arity-2
// tuples, so the arm covers every member and the match is exhaustive. Binding `[a, c]`
// against the whole union constrains each member to the tuple shape, so `a`/`c` bind at the
// joined element types `1 | 3` / `2 | 4` and the match type-checks.
func TestInferMatchTupleUnionExhaustive(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(b: boolean) {
			val x = if b { [1, 2] } else { [3, 4] }
			return match x {
				[a, c] => a
			}
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (b: boolean) -> 1 | 3", values["f"])
}

// A guarded arm can always fail its guard, so it never makes a match exhaustive. An
// inexact scrutinee still needs a separate catch-all even when a guarded arm names
// its whole shape.
func TestInferMatchGuardedArmDoesNotCover(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f(p: {x: number, ...}, b: boolean) {
			return match p {
				{x} if b => x
			}
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "3:11-5:5: match is not exhaustive; add a catch-all branch", msgWithSpan(errs[0]))
}

// A guard is typed as a boolean over the arm's bindings, so a non-boolean guard is
// a constraint error.
func TestInferMatchGuardMustBeBoolean(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f(p: {x: number}) {
			return match p {
				{x} if x => x,
				_ => 0
			}
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "4:12-4:13: cannot constrain number <: boolean", msgWithSpan(errs[0]))
}

// An arm whose only structural pattern is refutable does not cover an exact
// scrutinee. A nested literal such as `{x: 1}` can fail, so the match is
// non-exhaustive, and the message asks for a branch binding the same shape
// irrefutably rather than for a catch-all.
func TestInferMatchRefutableArmNonExhaustive(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f(p: {x: number}) {
			return match p {
				{x: 1} => 10
			}
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "3:11-5:5: match is not exhaustive; `{x: number}` is matched only by a branch whose own pattern can fail, so add a branch that matches it irrefutably", msgWithSpan(errs[0]))
}

// A nested literal pattern flows against the scrutinee's concrete field type, so a
// kind mismatch is rejected, just as a top-level literal pattern is.
func TestInferMatchNestedWrongLiteralRejected(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f(p: {x: number}) {
			return match p {
				{x: "hi"} => 1,
				_ => 0
			}
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, `4:9-4:13: cannot constrain "hi" <: number`, msgWithSpan(errs[0]))
}

// The same check applies to a nested literal in a tuple pattern element.
func TestInferMatchTupleNestedWrongLiteralRejected(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f(t: [number, number]) {
			return match t {
				[a, "hi"] => 1,
				_ => 0
			}
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, `4:9-4:13: cannot constrain "hi" <: number`, msgWithSpan(errs[0]))
}

// A correctly-typed nested literal pattern still type-checks.
func TestInferMatchNestedRightLiteralOK(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(p: {x: number}) {
			return match p {
				{x: 1} => 1,
				_ => 0
			}
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: {x: number}) -> 0 | 1", values["f"])
}

// A name a match arm binds is local to that arm. Referencing it in the arm body
// must not register a module-level dependency on a top-level binding of the same
// name. Here the arm leaf `ov` shadows the top-level overloaded `ov`. Without
// per-arm scoping the dep graph would form a false {f, ov} cycle and wrongly
// require fully-annotated overload signatures.
func TestInferMatchArmBindingScopedInDepGraph(t *testing.T) {
	_, _, errs := inferSources(t, map[string]string{
		"main": `
			fn f(p: {ov: number}) {
				return match p {
					{ov} => ov
				}
			}
			fn ov(a: number) { return f({ov: a}) }
			fn ov(a: string) { return a }
		`,
	})
	require.Empty(t, errs)
}

// Patterns nest across kinds: an object pattern whose field is a tuple pattern
// binds the inner elements at the nested element types.
func TestInferObjectContainingTuplePattern(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(o: {pt: [number, string]}) {
			val {pt: [a, b]} = o
			return [b, a]
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (o: {pt: [number, string]}) -> [string, number]", values["f"])
}
