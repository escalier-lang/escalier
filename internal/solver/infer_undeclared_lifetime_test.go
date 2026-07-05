package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/stretchr/testify/require"
)

// --- M6.5 PR10: error on a used-but-undeclared lifetime ---
//
// A named lifetime a signature uses must be bound by that signature's own `<…>` list.
// A `&'x` borrow or a bound right-hand side names `'x`; when no `<'x>` binder introduces
// it the name is a forgotten declaration or a typo. With no clause at all the use is a
// hard error; with a clause that binds other names it is a typo warning carrying the
// nearest declared siblings. The symmetric companion warns on a binder no use references.

// A used lifetime with no `<…>` clause is a hard error blaming each occurrence, since the
// author almost certainly meant to declare the binder. The borrow appears on both the
// parameter and the return, so each names 'b and each is blamed.
func TestInferUndeclaredLifetimeNoClauseHardError(t *testing.T) {
	_, _, errs := inferSource(t, `fn f(p: &'b {x: number}) -> &'b {x: number} { return p }`)
	require.Equal(t,
		[]string{
			"1:10-1:12: lifetime 'b is used but not declared; add `<'b>` to the enclosing function signature",
			"1:30-1:32: lifetime 'b is used but not declared; add `<'b>` to the enclosing function signature",
		},
		messagesWithSpan(errs))
	require.False(t, errs[0].(*UndeclaredLifetimeError).IsWarning(),
		"a use with no clause is a hard error")
}

// A used lifetime the clause does not bind is a typo warning, with the nearest declared
// sibling suggested. 'a is declared and borrowed by p, so it is not unused; only the
// stray 'b on q is undeclared, and 'a is one edit away.
func TestInferUndeclaredLifetimeWithClauseWarns(t *testing.T) {
	_, _, errs := inferSource(t, `fn f<'a>(p: &'a {x: number}, q: &'b {x: number}) { return p }`)
	require.Equal(t,
		[]string{"1:34-1:36: lifetime 'b is used but not declared; did you mean 'a?"},
		messagesWithSpan(errs))
	require.True(t, errs[0].(*UndeclaredLifetimeError).IsWarning(),
		"a use under an existing clause is a typo warning")
}

// A clause exists but no declared name is close enough to suggest, so the warning falls
// back to prompting the author to declare the name rather than offering a correction.
// `'xyz` is three edits from the stray 'a, beyond the suggestion threshold.
func TestInferUndeclaredLifetimeWithClauseNoCloseSuggestion(t *testing.T) {
	_, _, errs := inferSource(t,
		`fn f<'xyz>(p: &'xyz {x: number}, q: &'a {x: number}) { return p }`)
	require.Equal(t,
		[]string{"1:38-1:40: lifetime 'a is used but not declared; add `<'a>` to the signature's lifetime list"},
		messagesWithSpan(errs))
	require.True(t, errs[0].(*UndeclaredLifetimeError).IsWarning())
}

// The right-hand side of a declared bound is a use, so an undeclared name there is
// reported the same way. A function type annotation is a no-body site whose bounds lower
// rather than being checked, so only the undeclared-lifetime warning fires. 'a is one
// edit from the stray 'b.
func TestInferUndeclaredLifetimeInBoundRHS(t *testing.T) {
	_, _, errs := inferSource(t,
		`val f: fn<'a: 'b>(p: &'a {x: number}) -> &'a {x: number} = fn (p) { return p }`)
	require.Equal(t,
		[]string{"1:15-1:17: lifetime 'b is used but not declared; did you mean 'a?"},
		messagesWithSpan(errs))
	require.True(t, errs[0].(*UndeclaredLifetimeError).IsWarning())
}

// 'static is the built-in bottom of the outlives lattice, so it is never undeclared. A
// `&'static` borrow with no clause reports nothing.
func TestInferStaticLifetimeNeverUndeclared(t *testing.T) {
	_, _, errs := inferSource(t,
		`fn f(p: &'static {x: number}) -> &'static {x: number} { return p }`)
	require.Empty(t, errs)
}

// A nested function is judged by its own clause, not an enclosing one. The inner `relay`
// declares no `<'a>`, so its `&'a` borrows are undeclared with no clause and are hard
// errors, even though the outer function declares `<'a>`. An enclosing function's
// lifetimes are not visible to a nested one.
func TestInferUndeclaredLifetimeNestedJudgedByOwnClause(t *testing.T) {
	_, _, errs := inferSource(t, `fn outer<'a>(p: &'a {x: number}) -> &'a {x: number} {
  val relay = fn (q: &'a {x: number}) -> &'a {x: number} { return q }
  return p
}`)
	require.Equal(t,
		[]string{
			"2:23-2:25: lifetime 'a is used but not declared; add `<'a>` to the enclosing function signature",
			"2:43-2:45: lifetime 'a is used but not declared; add `<'a>` to the enclosing function signature",
		},
		messagesWithSpan(errs))
	require.False(t, errs[0].(*UndeclaredLifetimeError).IsWarning(),
		"the inner function has no clause, so its use is a hard error")
}

// A declared binder that no borrow and no bound references is dead weight. `<'a>` is
// declared but p borrows at a fresh inferred lifetime, so 'a is unused and warns.
func TestInferUnusedLifetimeParamWarns(t *testing.T) {
	_, _, errs := inferSource(t, `fn f<'a>(p: &{x: number}) { return p }`)
	require.Equal(t,
		[]string{"1:6-1:8: lifetime parameter 'a is declared but never used"},
		messagesWithSpan(errs))
	require.True(t, errs[0].(*UnusedLifetimeParamError).IsWarning())
}

// A name bound more than once warns unused only once, blaming its first binder. The
// undeclared and unused directions agree on deduplication. The check runs directly on a
// hand-built signature, since the parser does not produce a repeated binder.
func TestCheckLifetimeDeclarationsDuplicateBinderWarnsOnce(t *testing.T) {
	c := newChecker()
	first := ast.NewLifetimeParam("a", nil, ast.Span{Start: ast.Location{Line: 1, Column: 6}, End: ast.Location{Line: 1, Column: 8}})
	second := ast.NewLifetimeParam("a", nil, ast.Span{Start: ast.Location{Line: 1, Column: 10}, End: ast.Location{Line: 1, Column: 12}})
	params := []*ast.LifetimeParam{first, second}

	c.checkLifetimeDeclarations(params, nil, nil, nil)

	require.Len(t, c.errs, 1, "a name bound twice warns unused once")
	ue, ok := c.errs[0].(*UnusedLifetimeParamError)
	require.True(t, ok)
	require.Equal(t, "a", ue.Name)
	require.Same(t, first, ue.Param, "the first binder is blamed")
}

// A binder used only as a bound right-hand side counts as used, so it does not warn as
// unused. `<'a: 'c, 'b: 'c, 'c>` declares 'c and references it from both bounds; 'c also
// borrows nothing on its own, yet the bound uses keep it live.
func TestInferBoundRHSCountsAsUse(t *testing.T) {
	_, _, errs := inferSource(t, `fn pick<'a: 'c, 'b: 'c, 'c>(p: &'a mut {x: number}, q: &'b mut {x: number}) -> &'c mut {x: number} {
		if true { return p } else { return q }
	}`)
	require.Empty(t, errs)
}

// A well-formed signature that declares and uses every lifetime reports nothing.
func TestInferDeclaredAndUsedLifetimeNoDiagnostic(t *testing.T) {
	_, _, errs := inferSource(t,
		`fn f<'a>(p: &'a {x: number}) -> &'a {x: number} { return p }`)
	require.Empty(t, errs)
}

// nearestLifetimes ranks declared siblings by edit distance and keeps only the closest
// within the threshold, so a stray name points at its likeliest intended binder.
func TestNearestLifetimes(t *testing.T) {
	tests := []struct {
		name     string
		use      string
		siblings []string
		want     []string
	}{
		// Single-letter names are all one edit apart, so every sibling is an equally close
		// suggestion, returned in declaration order.
		{"all one edit", "c", []string{"a", "b"}, []string{"a", "b"}},
		// Among mixed distances only the minimum-distance siblings are kept.
		{"keeps closest only", "abd", []string{"abc", "xyz"}, []string{"abc"}},
		// A sibling farther than the threshold is no suggestion at all.
		{"beyond threshold", "a", []string{"xyz"}, nil},
		// No declared siblings means no suggestion.
		{"no siblings", "a", nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, nearestLifetimes(tt.use, tt.siblings))
		})
	}
}
