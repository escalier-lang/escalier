package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestADuplicateTypeParamIsReported covers a declaration that binds one type-parameter
// name twice.
//
// A reference to the name can only mean one of them, so the later binder is unreachable
// and a caller has no way to say which parameter an argument fills. Every declaration that
// carries type parameters routes through resolveTypeParams, so one check covers them all.
//
// A constructor's own type parameters are unsupported, #1802, so the shape is rejected
// before the check is reached and is left out here. internal/checker covers it.
func TestADuplicateTypeParamIsReported(t *testing.T) {
	const want = "type parameter `T` is declared more than once"

	tests := map[string]string{
		"AFunction":   `declare fn f<T, T>(a: T) -> T`,
		"AClass":      "class C<T, T> {\n\tv: T,\n}",
		"AnEnum":      "enum E<T, T> {\n\tA(v: T),\n}",
		"AnAlias":     `type A<T, T> = {v: T}`,
		"AnInterface": "interface I<T, T> {\n\tv: T,\n}",
	}

	for name, src := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, src)
			require.Equal(t, []string{want}, errorMessagesOf(errs))
		})
	}
}

// TestADroppedDuplicateTypeParamRaisesNothingElse covers the annotations on the binder a
// duplicate name drops, and the parameter count a reference to the declaration is matched
// against.
//
// Both checkers resolve one parameter per distinct name, keeping the first binder. The
// later binder is unreachable, so its constraint and default are dropped with it and raise
// nothing of their own, and a reference writes one argument per distinct name.
func TestADroppedDuplicateTypeParamRaisesNothingElse(t *testing.T) {
	const want = "type parameter `T` is declared more than once"

	tests := map[string]string{
		// A bound or a default naming the duplicated name puts that name in its own
		// dependency cycle, the shape that has no order satisfying every annotation.
		"ASelfReferentialBound":   `declare fn g<T, T <: T>(a: T) -> T`,
		"ASelfReferentialDefault": `type Bad<T, T = T> = {v: T}`,
		"AClassReference":         "class C<T, T> {\n\tv: T,\n}\ndeclare val c: C<number>",
		"AnAliasReference":        "type A<T, T> = {v: T}\ndeclare val a: A<number>",
		"AnEnumReference":         "enum E<T, T> {\n\tV(v: T),\n}\ndeclare val e: E<number>",
	}

	for name, src := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, src)
			require.Equal(t, []string{want}, errorMessagesOf(errs))
		})
	}
}

// TestOnlyTheLaterDuplicateTypeParamIsReported asserts that a name bound three times
// raises two errors rather than three, so each error names a binder that could have been
// written differently.
func TestOnlyTheLaterDuplicateTypeParamIsReported(t *testing.T) {
	_, _, errs := inferSource(t, `declare fn f<T, T, T>(a: T) -> T`)
	require.Equal(t, []string{
		"type parameter `T` is declared more than once",
		"type parameter `T` is declared more than once",
	}, errorMessagesOf(errs))
}

// TestDistinctTypeParamsAreAccepted is the control: a declaration whose names differ
// raises nothing, including one whose default names an earlier sibling.
func TestDistinctTypeParamsAreAccepted(t *testing.T) {
	_, _, errs := inferSource(t, `declare fn ok<T, U = T>(a: T, b: U) -> T`)
	require.Empty(t, errorMessagesOf(errs))
}
