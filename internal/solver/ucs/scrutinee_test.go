package ucs

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/stretchr/testify/require"
)

func TestNewRootIsARootScrutinee(t *testing.T) {
	target := ident("p")
	root := NewRoot(target, At(OriginMatchArm, arm(span(1, 1, 8))))

	require.True(t, root.IsRoot())
	require.Nil(t, root.Parent)
	require.Nil(t, root.Step)
	require.Same(t, target, root.Target)
}

// TestProjectSharesItsParent locks the once-evaluation guarantee. Sibling
// projections hold the same parent pointer, so a consumer evaluates the parent once
// and reads both projections off it.
func TestProjectSharesItsParent(t *testing.T) {
	origin := At(OriginMatchArm, arm(span(1, 1, 8)))
	root := NewRoot(ident("p"), origin)
	x := root.Project(FieldStep{Name: "x"}, origin)
	y := root.Project(FieldStep{Name: "y"}, origin)

	require.False(t, x.IsRoot())
	require.Same(t, root, x.Parent)
	require.Same(t, root, y.Parent)
	require.Nil(t, x.Target)
	require.Equal(t, FieldStep{Name: "x"}, x.Step)
	require.Equal(t, "p.x", x.String())
	require.Equal(t, "p.y", y.String())
}

// TestStepEqual covers the comparison every consumer must use. `==` on two Step
// values panics as soon as either is a RemainderStep, since a set is a map, so Equal
// is the only safe way to ask whether two projections match.
func TestStepEqual(t *testing.T) {
	tests := []struct {
		name  string
		left  Step
		right Step
		want  bool
	}{
		{"same field", FieldStep{Name: "x"}, FieldStep{Name: "x"}, true},
		{"different field", FieldStep{Name: "x"}, FieldStep{Name: "y"}, false},
		{"same index", IndexStep{Index: 1}, IndexStep{Index: 1}, true},
		{"different index", IndexStep{Index: 0}, IndexStep{Index: 1}, false},
		{"same result", ResultStep{Index: 0}, ResultStep{Index: 0}, true},
		{
			// A tuple element and an extractor result resolve through different
			// machinery, so the same position is not the same projection.
			"index is not a result",
			IndexStep{Index: 0},
			ResultStep{Index: 0},
			false,
		},
		{"same suffix", SuffixStep{From: 1}, SuffixStep{From: 1}, true},
		{"different suffix", SuffixStep{From: 1}, SuffixStep{From: 2}, false},
		{
			"same remainder",
			RemainderStep{Exclude: set.FromSlice([]string{"x", "y"})},
			RemainderStep{Exclude: set.FromSlice([]string{"y", "x"})},
			true,
		},
		{
			"different remainder",
			RemainderStep{Exclude: set.FromSlice([]string{"x"})},
			RemainderStep{Exclude: set.FromSlice([]string{"x", "y"})},
			false,
		},
		{
			"remainder is not a field",
			RemainderStep{Exclude: set.FromSlice([]string{"x"})},
			FieldStep{Name: "x"},
			false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, test.left.Equal(test.right))
			require.Equal(t, test.want, test.right.Equal(test.left))
		})
	}
}

func TestScrutineePaths(t *testing.T) {
	origin := At(OriginMatchArm, arm(span(1, 1, 8)))
	root := func(name string) *Scrutinee { return NewRoot(ident(name), origin) }

	tests := []struct {
		name string
		in   *Scrutinee
		want string
	}{
		{
			"root target",
			root("p"),
			"p",
		},
		{
			// A side-effecting target is one shared node, so no test or bind re-runs it.
			"root call target",
			NewRoot(ast.NewCall(ident("f"), nil, false, ast.Span{}), origin),
			"f()",
		},
		{
			"object field",
			root("p").Project(FieldStep{Name: "x"}, origin),
			"p.x",
		},
		{
			"nested object field",
			root("l").Project(FieldStep{Name: "start"}, origin).Project(FieldStep{Name: "x"}, origin),
			"l.start.x",
		},
		{
			"tuple element",
			root("xs").Project(IndexStep{Index: 0}, origin),
			"xs.0",
		},
		{
			// The `v` of `Ok(v)` is the extractor's first positional result. The `#`
			// keeps it distinct from the tuple element `r.0` above.
			"extractor result",
			root("r").Project(ResultStep{Index: 0}, origin),
			"r.#0",
		},
		{
			// The `rest` of `[first, ...rest]` is the suffix past the fixed prefix.
			"tuple suffix",
			root("xs").Project(SuffixStep{From: 1}, origin),
			"xs[1..]",
		},
		{
			// The `rest` of `{x, ...rest}` is the scrutinee minus the keys named here.
			"object remainder",
			root("p").Project(RemainderStep{Exclude: set.FromSlice([]string{"x"})}, origin),
			`p \ {x}`,
		},
		{
			// Excluded keys render in sorted order so the path is deterministic.
			"object remainder with several keys",
			root("p").Project(RemainderStep{Exclude: set.FromSlice([]string{"y", "x"})}, origin),
			`p \ {x, y}`,
		},
		{
			"nil scrutinee",
			nil,
			"<nil>",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, test.in.String())
		})
	}
}
