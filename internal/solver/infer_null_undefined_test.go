package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// --- M9 PR16: `null` and `undefined` as a written surface ---

// Both words name a type in annotation position and a value in expression position, so an
// annotated binding round-trips the atom it was written with.
func TestInferNullUndefinedAnnotationRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "Null",
			src:  `val n: null = null`,
			want: "null",
		},
		{
			name: "Undefined",
			src:  `val n: undefined = undefined`,
			want: "undefined",
		},
		{
			// A nested position resolves the same way, so each atom composes rather than
			// being special-cased at the top of an annotation.
			name: "NestedInObject",
			src:  `val n: {a: null} = {a: null}`,
			want: "{a: null}",
		},
		{
			// Neither atom is a soltype.Lit, so an unannotated binding has no primitive to
			// widen toward and keeps the atom. widen's switch is over LitType only.
			name: "UnannotatedValKeepsAtom",
			src:  `val n = null`,
			want: "null",
		},
		{
			// A `var` widens its initializer so the cell can later hold another value of the
			// same primitive. `null` is already its own widest form, so widen leaves it be.
			name: "UnannotatedVarKeepsAtom",
			src:  `var n = undefined`,
			want: "undefined",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, values["n"])
		})
	}
}

// Two `null` annotations in one module each resolve without tripping the provenance table.
// soltype.NullType has no fields, so Go gives every instance one address, and Prov is keyed by
// pointer identity — recording against it would file both under a single entry and panic the
// debugProv guard on the second. resolveLitTypeAnn returns the atom before its recordProv call
// for that reason, the same as the `never` and `unknown` arms.
func TestInferNullAnnotationTwiceInOneModule(t *testing.T) {
	values, _, errs := inferSource(t, "val n: null = null\nval u: undefined = undefined")
	require.Empty(t, errs)
	require.Equal(t, "null", values["n"])
	require.Equal(t, "undefined", values["u"])
}

// Each atom relates only to itself. It is unrelated to the other atom, to `void`, and to every
// data type, which is TypeScript's behavior under strict null checks.
func TestInferNullUndefinedUnrelated(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "NullIsNotUndefined",
			src:  `val n: null = undefined`,
			want: "1:15-1:24: cannot constrain undefined <: null",
		},
		{
			name: "UndefinedIsNotNull",
			src:  `val n: undefined = null`,
			want: "1:20-1:24: cannot constrain null <: undefined",
		},
		{
			name: "NullIsNotANumber",
			src:  `val n: number = null`,
			want: "1:17-1:21: cannot constrain null <: number",
		},
		{
			name: "NumberIsNotNull",
			src:  `val n: null = 5`,
			want: "1:15-1:16: cannot constrain 5 <: null",
		},
		{
			// `void` is the result of a statement block with no value, which the body of
			// `g` below produces. It is a third atom, distinct from both absence markers.
			name: "NullIsNotVoid",
			src:  "fn g() {}\nval n: null = g()",
			want: "2:15-2:18: cannot constrain void <: null",
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

// Every type is a subtype of the top of the lattice, so each atom reaches `unknown` through
// constrain's `_ <: unknown` rule rather than through an arm of its own.
func TestInferNullUndefinedFlowIntoUnknown(t *testing.T) {
	_, _, errs := inferSource(t, "val n: unknown = null\nval u: unknown = undefined")
	require.Empty(t, errs)
}

// Reading an optional property joins `undefined` into the result, so `o.a` for
// `o: {a?: number}` infers `number | undefined`. That type is now writable, which closes the
// gap where the checker inferred a type no annotation could name.
func TestInferOptionalPropertyReadAgainstWrittenUndefined(t *testing.T) {
	values, _, errs := inferSource(t, `
		val o: {a?: number} = {}
		val x: number | undefined = o.a
	`)
	require.Empty(t, errs)
	require.Equal(t, "number | undefined", values["x"])
}

// A union renders its data members before its absence markers, which typeKindOrder fixes as
// NullType, then Void, then UndefinedType. The source order below is the reverse, so the render
// shows the canonical order rather than the written one.
func TestInferNullUndefinedUnionCanonicalOrder(t *testing.T) {
	values, _, errs := inferSource(t, `val u: undefined | null | number = 5`)
	require.Empty(t, errs)
	require.Equal(t, "number | null | undefined", values["u"])
}

// A `null` match arm binds against a union scrutinee that carries the atom, and the two arms
// together cover every member, so the match needs no catch-all. The catch-all arm still binds
// the whole union, since only an object or tuple pattern narrows a union scrutinee — a `null`
// arm leaves the members after it alone, exactly as a literal arm does.
func TestInferMatchNullArmCoversUnionMember(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(x: number | null) {
			return match x {
				null => 0,
				n => n
			}
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (x: number | null) -> number | null", values["f"])
}

// A union member no arm names leaves the match non-exhaustive. The `null` arm covers the
// `null` member, so `undefined` is what escapes.
func TestInferMatchNullArmMissingUndefinedMember(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f(x: null | undefined) {
			return match x {
				null => 0
			}
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "3:11-5:5: match is not exhaustive; add a catch-all branch", msgWithSpan(errs[0]))
}

// Naming both atoms covers a scrutinee made of nothing else.
func TestInferMatchBothAtomArmsCoverUnion(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(x: null | undefined) {
			return match x {
				null => 0,
				undefined => 1
			}
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (x: null | undefined) -> 0 | 1", values["f"])
}
