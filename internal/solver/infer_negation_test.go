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

// newNegation can hand back a pointer that is not freshly minted: `¬¬T` folds to the operand's
// inner T, which already carries its own blame, and the lattice-bound folds return the shared
// zero-size NeverType/UnknownType singletons every instance of which shares one address.
// resolveNegationTypeAnn routes through recordProvForResult, which records neither, so a second
// annotation that folds to the same pointer overwrites no earlier blame and does not trip the
// debugProv unique-pointer guard. Each case resolves two distinct annotations that fold to one
// shared pointer and asserts the second does not panic. TestProvSharedSingletonRecordsNothing
// asserts the stronger fact that a singleton fold records nothing at all. inferSource leaves
// debugProv off, so the test drives the resolver directly with the guard on.
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

// A complement written inside a template-literal interpolation matches through the placeholder
// matcher's negation and intersection arms in constrain.go. `on${string & ¬"a"}` denotes every
// `on`-prefixed string whose interpolated span is a string other than `"a"`, so `"onb"` conforms
// and `"ona"` does not.
func TestInferNegationInTemplateLit(t *testing.T) {
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
}

// A complement may name a borrow. `¬(&Point)` denotes every value that is not a borrow of
// Point, so it admits a borrow of another type, a borrow of Point under a different
// lifetime, and every value that is not a borrow.
//
// A borrow reached only under a union or an intersection resolves the same way, since a
// complement over a lattice spine is a complement over what that spine names.
//
// The last row renders its lifetime, since the alias declares one and the assertion passes
// those names to the printer. The rows above declare none, so their borrows print as a
// bare `&`, the fallback for an inferred lifetime.
func TestInferNegationOfBorrowResolves(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "SharedBorrowOfClass",
			src:  "class Point { x: number }\ntype Result = ¬&Point",
			want: "¬&Point",
		},
		{
			name: "MutBorrowOfObject",
			src:  "type Result = ¬&mut {x: number}",
			want: "¬&mut {x: number}",
		},
		{
			name: "BorrowUnderUnion",
			src:  "class Point { x: number }\ntype Result = ¬(&Point | number)",
			want: "¬(number | &Point)",
		},
		{
			name: "BorrowUnderIntersection",
			src:  "class Point { x: number }\ntype Result = ¬(number & &Point)",
			want: "¬(number & &Point)",
		},
		{
			// The complement names one particular borrow, so the lifetime is what says
			// which. Rendering it is what distinguishes `¬(&'a Point)` from `¬(&Point)`,
			// the complement of any borrow of Point.
			name: "BorrowWithNamedLifetime",
			src:  "class Point { x: number }\ntype Result<'a> = ¬(&'a Point)",
			want: "¬&'a Point",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, ctx, errs := inferTypeNodes(t, tt.src)
			require.Empty(t, errs)
			got := soltype.PrintWithDeclaredParams(nodes["Result"], nil, aliasLifetimeParams(ctx, "Result"))
			require.Equal(t, tt.want, got)
		})
	}
}
