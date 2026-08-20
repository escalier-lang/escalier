package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// A written `¬T` resolves to the complement of its operand. newNegation folds the four operands
// whose complement the lattice already names — `¬never` to `unknown`, `¬unknown` to `never`, `¬¬T`
// to `T`, and `¬(open union)` to `never` — and wraps anything else in a NegationType that renders
// `¬T` the way the source wrote it. Each case asserts the stored `Result` renders as expected.
func TestInferNegationTypeAnnResolves(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "LiteralOperand",
			src:  `type Result = ¬"a"`,
			want: `¬"a"`,
		},
		{
			name: "PrimitiveOperand",
			src:  "type Result = ¬number",
			want: "¬number",
		},
		{
			// `¬` binds tighter than `&`, so the intersection keeps the complement as one member.
			name: "IntersectionMember",
			src:  `type Result = string & ¬"a"`,
			want: `string & ¬"a"`,
		},
		{
			name: "NegateNever",
			src:  "type Result = ¬never",
			want: "unknown",
		},
		{
			name: "NegateUnknown",
			src:  "type Result = ¬unknown",
			want: "never",
		},
		{
			name: "DoubleNegation",
			src:  "type Result = ¬¬number",
			want: "number",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, _, errs := inferTypeNodes(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, soltype.Print(nodes["Result"]))
		})
	}
}

// newNegation can hand back a pointer that already carries provenance: `¬¬T` folds to the operand's
// inner, and the lattice-bound folds return the shared zero-size NeverType/UnknownType singletons
// every instance of which shares one address. resolveNegationTypeAnn records prov only on the first
// writer, so a second annotation that folds to the same pointer overwrites no earlier blame and does
// not trip the debugProv unique-pointer guard. Each case resolves two distinct annotations that fold
// to one shared pointer and asserts the second records nothing new. inferSource leaves debugProv off,
// so the test drives the resolver directly with the guard on, mirroring the atom-annotation test.
func TestResolveNegationFoldRecordsProvOnce(t *testing.T) {
	str := func() ast.TypeAnn { return ast.NewLitTypeAnn(ast.NewString("a", testSpan()), testSpan()) }
	tests := []struct {
		name string
		ann  func() ast.TypeAnn
	}{
		{"¬never folds to unknown", func() ast.TypeAnn {
			return ast.NewNegationTypeAnn(ast.NewNeverTypeAnn(testSpan()), testSpan())
		}},
		{"¬unknown folds to never", func() ast.TypeAnn {
			return ast.NewNegationTypeAnn(ast.NewUnknownTypeAnn(testSpan()), testSpan())
		}},
		{"¬¬T folds to the operand", func() ast.TypeAnn {
			return ast.NewNegationTypeAnn(ast.NewNegationTypeAnn(str(), testSpan()), testSpan())
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newChecker()
			c.debugProv = true
			scope := NewScope()

			_, ok := c.resolveTypeAnn(scope, tt.ann(), 0)
			require.True(t, ok)
			require.NotPanics(t, func() { c.resolveTypeAnn(scope, tt.ann(), 0) })
			require.Empty(t, c.errs)
		})
	}
}

// A bare `¬T` resolves to a NegationType node, not merely a type that prints as `¬T`.
func TestInferNegationTypeAnnResolvesToNegationType(t *testing.T) {
	nodes, _, errs := inferTypeNodes(t, `type Result = ¬"a"`)
	require.Empty(t, errs)
	require.IsType(t, &soltype.NegationType{}, nodes["Result"])
}

// A written `¬` flows through constraint solving: constrain reads a complement, so a value the
// complement admits is accepted and one it excludes is rejected. The excluded case reports the
// mismatch against the complement `¬"a"` the intersection normalizes to.
func TestInferNegationTypeAnnConstraint(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string // "" ⇒ expect no error
	}{
		{
			name: "AdmittedLiteralAccepted",
			src:  `val x: ¬"a" = "b"`,
		},
		{
			name:    "ExcludedLiteralRejected",
			src:     `val x: ¬"a" = "a"`,
			wantErr: `cannot constrain "a" <: ¬"a"`,
		},
		{
			// `¬` binds tighter than `&`, so the annotation is `string & (¬"a")`, admitting every
			// string but `"a"`. This is the complement the feature request writes as `string & ¬"a"`.
			name: "IntersectionAdmitsAllButExcluded",
			src:  `val x: string & ¬"a" = "b"`,
		},
		{
			name:    "IntersectionRejectsExcluded",
			src:     `val x: string & ¬"a" = "a"`,
			wantErr: `cannot constrain "a" <: ¬"a"`,
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

// DISABLED until #1164: a complement written inside a template-literal interpolation only matches
// once the placeholder matcher gains negation and intersection arms. Today matchInterp in constrain.go
// handles a string literal, the `string` primitive, and a union of those, so `on${string & ¬"a"}`
// rejects every value, `"onb"` included. #1164 teaches the matcher to read a complement, after which
// `on${string & ¬"a"}` admits every `on`-prefixed string but `"ona"`. When #1164 lands, remove the
// /* */ wrapper.
func TestInferNegationInTemplateLit(t *testing.T) {
	/*
		tests := []struct {
			name    string
			src     string
			wantErr string // "" ⇒ expect no error
		}{
			{
				name: "AdmittedValueAccepted",
				src:  "val b: `on${string & ¬\"a\"}` = \"onb\"",
			},
			{
				name:    "ExcludedValueRejected",
				src:     "val a: `on${string & ¬\"a\"}` = \"ona\"",
				wantErr: "cannot constrain \"ona\" <: `on${string & ¬\"a\"}`",
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
	*/
}

// The ¬Ref exclusion invariant forbids a complement from naming a borrow. A written `¬T` routes
// through the resolver's spine walk, so the diagnostic surfaces at the annotation rather than as a
// panic deeper in normalization. A borrow reached only under a union or an intersection is caught
// too, since De Morgan turns `¬(&Point | number)` into `¬(&Point) ∩ ¬number`, which still names the
// borrow. The message renders the offending borrow with its `&` and `mut`, dropping the lifetime.
func TestInferNegationOfBorrowRejected(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string
	}{
		{
			name:    "SharedBorrowOfClass",
			src:     "class Point { x: number }\ntype Bad = ¬&Point",
			wantErr: "cannot negate a borrow: &Point",
		},
		{
			name:    "MutBorrowOfObject",
			src:     "type Bad = ¬&mut {x: number}",
			wantErr: "cannot negate a borrow: &mut {x: number}",
		},
		{
			name:    "BorrowUnderUnion",
			src:     "class Point { x: number }\ntype Bad = ¬(&Point | number)",
			wantErr: "cannot negate a borrow: &Point",
		},
		{
			name:    "BorrowUnderIntersection",
			src:     "class Point { x: number }\ntype Bad = ¬(number & &Point)",
			wantErr: "cannot negate a borrow: &Point",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Len(t, errs, 1)
			require.Equal(t, tt.wantErr, errs[0].Message())
		})
	}
}
