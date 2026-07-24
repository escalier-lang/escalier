package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// An object-spread annotation over named or inline operands is stored unreduced, so the type keeps
// the `{...A, x: T}` form the source wrote rather than the merged object. Each case asserts the
// stored `Result` renders symbolically and that reducing it with the alias environment — the merge
// constrain performs to check a constraint — yields the merged object. The cases cover the merge
// rules the operator applies: field union, rightmost-wins override, the Flow optional show-through
// union, explicit fields beside spreads, and inexactness threading from an operand.
func TestInferObjectSpreadStaysSymbolic(t *testing.T) {
	tests := []struct {
		name         string
		src          string
		wantSymbolic string
		wantExpanded string
	}{
		{
			// Two disjoint operands union their fields.
			name: "DisjointOperands",
			src: `
				type A = {a: number}
				type B = {b: string}
				type Result = {...A, ...B}
			`,
			wantSymbolic: "{...A, ...B}",
			wantExpanded: "{a: number, b: string}",
		},
		{
			// A later operand's required field overrides an earlier one on the same key.
			name: "RightmostWins",
			src: `
				type A = {k: number}
				type B = {k: string}
				type Result = {...A, ...B}
			`,
			wantSymbolic: "{...A, ...B}",
			wantExpanded: "{k: string}",
		},
		{
			// The Flow optional show-through rule: a later optional field unions with the earlier
			// value and stays required, since the earlier field was required.
			name: "OptionalShowThrough",
			src: `
				type A = {k: number}
				type B = {k?: string}
				type Result = {...A, ...B}
			`,
			wantSymbolic: "{...A, ...B}",
			wantExpanded: "{k: number | string}",
		},
		{
			// Optional in both operands unions the values and keeps the field optional.
			name: "OptionalBoth",
			src: `
				type A = {k?: number}
				type B = {k?: string}
				type Result = {...A, ...B}
			`,
			wantSymbolic: "{...A, ...B}",
			wantExpanded: "{k?: number | string}",
		},
		{
			// A show-through union of two equal types collapses to the one type, so the merged
			// field is `k: number`, not `k: number | number`.
			name: "OptionalSameTypeDedups",
			src: `
				type A = {k: number}
				type B = {k?: number}
				type Result = {...A, ...B}
			`,
			wantSymbolic: "{...A, ...B}",
			wantExpanded: "{k: number}",
		},
		{
			// An explicit field written after a spread merges in like a one-field operand.
			name: "SpreadThenField",
			src: `
				type A = {a: number}
				type Result = {...A, x: string}
			`,
			wantSymbolic: "{...A, x: string}",
			wantExpanded: "{a: number, x: string}",
		},
		{
			// Nested spreads reduce all the way down: each level grounds to the object one level
			// in, so `{...{...{...{x}}}}` collapses to the innermost object.
			name: "NestedSpreads",
			src: `
				type Result = {...{...{...{x: number}}}}
			`,
			wantSymbolic: "{...{...{...{x: number}}}}",
			wantExpanded: "{x: number}",
		},
		{
			// A spread of an inexact object makes the merged object inexact.
			name: "InexactOperand",
			src: `
				type A = {a: number, ...}
				type Result = {...A}
			`,
			wantSymbolic: "{...A}",
			wantExpanded: "{a: number, ...}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, ctx, errs := inferTypeNodes(t, tt.src)
			require.Empty(t, errs)
			result := nodes["Result"]
			require.Equal(t, tt.wantSymbolic, soltype.Print(result))
			require.Equal(t, tt.wantExpanded, soltype.Print(expandResidual(ctx, result)))
		})
	}
}

// An object spread over a type parameter has no ground operand shape, so it stays symbolic in a
// function signature and round-trips from parameter to return. The reflexive `{...T} <: {...T}`
// from `return x` succeeds inertly by structural equality on the residual, so the displayed
// signature keeps the spread form rather than a merged object.
func TestInferObjectSpreadSignatureStaysSymbolic(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want map[string]string
	}{
		{
			name: "TypeParam",
			src:  `fn f<T>(x: {...T, a: number}) -> {...T, a: number} { return x }`,
			want: map[string]string{"f": "fn <T>(x: {...T, a: number}) -> {...T, a: number}"},
		},
		{
			name: "InlineOperand",
			src:  `fn g(x: {...{a: number}, b: string}) {}`,
			want: map[string]string{"g": "fn (x: {...{a: number}, b: string}) -> void"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
			for name, want := range tt.want {
				require.Equal(t, want, values[name])
			}
		})
	}
}

// constrain merges an object-spread residual to check satisfaction, while the stored type stays
// the `{...A, ...B}` form. A value matching the merged object is accepted; one whose field the
// merge rejects fails against the merged shape, so the diagnostic names the merged field's type.
func TestInferObjectSpreadConstraint(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string // "" ⇒ expect no error
	}{
		{
			name: "MergedObjectAccepted",
			src: `
				type A = {a: number}
				type B = {b: string}
				val v: {...A, ...B} = {a: 1, b: "x"}
			`,
		},
		{
			name: "MissingFieldRejected",
			src: `
				type A = {a: number}
				type B = {b: string}
				val v: {...A, ...B} = {a: 1}
			`,
			wantErr: "object is missing property: b",
		},
		{
			// The rightmost operand wins, so the merged `k` is string and a number is rejected.
			name: "OverrideRejected",
			src: `
				type A = {k: number}
				type B = {k: string}
				val v: {...A, ...B} = {k: 1}
			`,
			wantErr: `cannot constrain 1 <: string`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			if tt.wantErr == "" {
				require.Empty(t, errs)
				return
			}
			require.Len(t, errs, 1)
			require.Equal(t, tt.wantErr, errs[0].Message())
		})
	}
}

// A value-level object spread `{...o, x: v}` merges the operand's fields eagerly when the operand
// is a concrete object. Like the array-spread literal, the merge needs a concrete operand: an
// inline object splices in, while a bare variable reference stays a type variable during the value
// solve and is rejected. The merge rules match the type level: fields splice in, a later property
// overrides an earlier key, and an operand with no ground object shape is rejected.
func TestInferObjectSpreadLiteral(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		want    string
		wantErr string
	}{
		{
			name: "MergeInlineObject",
			src:  `val o = {...{p: 1}, x: 2}`,
			want: "{p: 1, x: 2}",
		},
		{
			// A later property overrides an earlier spread field on the same key.
			name: "LaterPropertyWins",
			src:  `val o = {...{p: 1}, p: 2}`,
			want: "{p: 2}",
		},
		{
			name:    "SpreadNonObjectRejected",
			src:     `val o = {...5, x: 1}`,
			wantErr: "cannot spread 5 into an object",
		},
		{
			// A bare variable operand stays a type variable during the value solve, like the
			// array-spread literal, so it has no ground object shape to merge and is rejected.
			// The message names the raw mid-solve variable the operand inferred to.
			name: "SpreadVariableRejected",
			src: `
				val x = {a: 1}
				val o = {...x}
			`,
			wantErr: "cannot spread t2 into an object",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			if tt.wantErr != "" {
				require.Len(t, errs, 1)
				require.Equal(t, tt.wantErr, errs[0].Message())
				return
			}
			require.Empty(t, errs)
			require.Equal(t, tt.want, values["o"])
		})
	}
}

// A rejected constraint whose subject is an object-spread residual names it structurally in the
// diagnostic — `{...t1, ...}` rather than the bare `?` the default describe arm would render — so
// the inert node stays legible in error messages. The trailing `...` marks the residual inexact,
// matching soltype.Print. describe is the raw mid-constrain renderer, so the operand shows as the
// raw var `t1` rather than the param name `T` the coalesced printer would use.
func TestInferObjectSpreadResidualErrorMessage(t *testing.T) {
	_, _, errs := inferSource(t, `fn f<T>(x: {...T, ...}) -> number { return x }`)
	require.Len(t, errs, 1)
	require.IsType(t, &CannotConstrainError{}, errs[0])
	require.Equal(t, "1:12-1:23: cannot constrain {...t1, ...} <: number", msgWithSpan(errs[0]))
}
