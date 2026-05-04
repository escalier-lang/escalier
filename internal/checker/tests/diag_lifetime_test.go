package tests

import (
	"testing"

	. "github.com/escalier-lang/escalier/internal/checker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPhase9_7_UnusedLifetimeParam exercises §9.7 class 1: a `<'a>`
// declaration that no parameter, return type, or throws annotation
// references is reported as an UnusedLifetimeParamError (warning).
func TestPhase9_7_UnusedLifetimeParam(t *testing.T) {
	t.Parallel()

	t.Run("declared_but_no_inline_use", func(t *testing.T) {
		errs := mustInferScriptAllErrors(t, `
			type Point = {x: number}
			fn f<'a>(p: Point) -> Point { return {x: 0} }
		`)
		var found *UnusedLifetimeParamError
		for _, e := range errs {
			if ue, ok := e.(UnusedLifetimeParamError); ok {
				found = &ue
				break
			}
		}
		require.NotNil(t, found, "expected an UnusedLifetimeParamError; got %v", errs)
		assert.Equal(t, "a", found.Name)
		assert.True(t, found.IsWarning(), "class 1 is a warning")
		assert.Equal(t, "lifetime parameter 'a is declared but never used", found.Message())
	})

	t.Run("used_param_does_not_warn", func(t *testing.T) {
		errs := mustInferScriptAllErrors(t, `
			type Point = {x: number}
			fn f<'a>(p: mut 'a Point) -> mut 'a Point { return p }
		`)
		for _, e := range errs {
			if _, ok := e.(UnusedLifetimeParamError); ok {
				t.Fatalf("did not expect UnusedLifetimeParamError; got %s", e.Message())
			}
		}
	})

	t.Run("partially_used_only_unused_warns", func(t *testing.T) {
		// `'a` is used; `'b` is not.
		errs := mustInferScriptAllErrors(t, `
			type Point = {x: number}
			fn f<'a, 'b>(p: mut 'a Point) -> mut 'a Point { return p }
		`)
		count := 0
		for _, e := range errs {
			if ue, ok := e.(UnusedLifetimeParamError); ok {
				count++
				assert.Equal(t, "b", ue.Name, "only 'b should be flagged")
			}
		}
		assert.Equal(t, 1, count, "exactly one unused-param diagnostic expected")
	})
}

// TestPhase9_7_UndeclaredLifetime exercises §9.7 class 2: an inline
// `'a` reference with no enclosing `<'a>` declaration is reported as
// an UndeclaredLifetimeError. Severity is error when no enclosing
// `<>` clause exists at all, warning when one exists but the name
// does not match a declared sibling.
func TestPhase9_7_UndeclaredLifetime(t *testing.T) {
	t.Parallel()

	t.Run("no_enclosing_clause_is_error", func(t *testing.T) {
		errs := mustInferScriptAllErrors(t, `
			type Point = {x: number}
			fn f(p: mut 'a Point) -> mut 'a Point { return p }
		`)
		var found *UndeclaredLifetimeError
		for _, e := range errs {
			if ue, ok := e.(UndeclaredLifetimeError); ok {
				found = &ue
				break
			}
		}
		require.NotNil(t, found, "expected an UndeclaredLifetimeError; got %v", errs)
		assert.Equal(t, "a", found.Name)
		assert.False(t, found.IsWarning(),
			"class 2 with no enclosing <> clause is a hard error")
		assert.Equal(t,
			"lifetime 'a is used but not declared; add `<'a>` to the enclosing function signature",
			found.Message())
	})

	t.Run("typo_with_sibling_is_warning", func(t *testing.T) {
		// `<'a>` is declared; `'b` inline is a typo.
		errs := mustInferScriptAllErrors(t, `
			type Point = {x: number}
			fn f<'a>(p: mut 'b Point) -> mut 'a Point { return p }
		`)
		var found *UndeclaredLifetimeError
		for _, e := range errs {
			if ue, ok := e.(UndeclaredLifetimeError); ok && ue.Name == "b" {
				found = &ue
				break
			}
		}
		require.NotNil(t, found,
			"expected an UndeclaredLifetimeError for 'b; got %v", errs)
		assert.True(t, found.IsWarning(),
			"class 2 with sibling lifetimes is a warning (likely typo)")
		assert.Equal(t, []string{"a"}, found.Suggestions)
		assert.Equal(t,
			"lifetime 'b is used but not declared; did you mean 'a?",
			found.Message())
	})

	t.Run("declared_sibling_does_not_warn", func(t *testing.T) {
		errs := mustInferScriptAllErrors(t, `
			type Point = {x: number}
			fn f<'a, 'b>(p: mut 'a Point, q: mut 'b Point) -> mut 'a Point { return p }
		`)
		for _, e := range errs {
			if _, ok := e.(UndeclaredLifetimeError); ok {
				t.Fatalf("did not expect UndeclaredLifetimeError; got %s", e.Message())
			}
		}
	})
}

// TestPhase9_7_DeclaredLifetimeMismatch_Scaffolded acknowledges that
// class 3 is currently scaffolded but unimplemented. The hook is in
// place (see checkDeclaredVsActualLifetimes); when a future patch
// adds the non-mutating compare pass, this test should flip from
// "no error today" to asserting a DeclaredLifetimeMismatchError.
func TestPhase9_7_DeclaredLifetimeMismatch_Scaffolded(t *testing.T) {
	t.Parallel()
	// Signature claims the result aliases `p`; body returns a fresh
	// value. Today this is silently accepted because the scaffold is
	// a no-op. Once the compare pass lands, the assertion below
	// should be inverted to require exactly one
	// DeclaredLifetimeMismatchError.
	errs := mustInferScriptAllErrors(t, `
		type Point = {x: number}
		fn f<'a>(p: mut 'a Point) -> mut 'a Point { return {x: 0} }
	`)
	for _, e := range errs {
		if _, ok := e.(DeclaredLifetimeMismatchError); ok {
			t.Fatalf(
				"class 3 is scaffolded but unimplemented; once a compare "+
					"pass lands, update this test to expect the diagnostic. "+
					"got: %s", e.Message())
		}
	}
}
