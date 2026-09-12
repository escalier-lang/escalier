package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A member lookup asserts a kind: an object for a structural member, a class for an
// instance member, a class value for a static. A receiver written as a type alias or as
// `typeof v` is a handle naming one of those rather than the thing itself, so the lookup
// reads through the handle first.
//
// A property is unaffected either way, since the structural `{name: fieldVar}` requirement
// the read otherwise falls to is a property requirement and constrain expands an alias
// itself. A method, getter, or setter has no such requirement, so before the peel the read
// fell through and reported the member as missing.
func TestAMemberResolvesThroughAnAliasOrTypeof(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		binding string
		want    string
	}{
		{
			name: "AMethodBehindAnAlias",
			src: `
				type C = { plain(n: number) -> number }
				declare val c: C
				val r = c.plain(1)
			`,
			binding: "r",
			want:    "number",
		},
		{
			name: "AGetterBehindAnAlias",
			src: `
				type C = { get tag(self) -> string }
				declare val c: C
				val r = c.tag
			`,
			binding: "r",
			want:    "string",
		},
		{
			// The alias names a class rather than an object, so the read goes through the
			// projected class body.
			name: "AClassInstanceBehindAnAlias",
			src: `
				declare class K { m(self) -> number }
				type A = K
				declare val k: A
				val r = k.m()
			`,
			binding: "r",
			want:    "number",
		},
		{
			name: "AMemberThroughTypeofAValue",
			src: `
				declare class K { m(self) -> number }
				declare val k: K
				declare val k2: typeof k
				val r = k2.m()
			`,
			binding: "r",
			want:    "number",
		},
		{
			// A class value is an object carrying the constructor and the statics, so
			// `typeof K` reaches a static method the same way an alias reaches an ordinary
			// one. This is the shape `[Symbol.species]: typeof Array` needs; see #1412.
			name: "AStaticThroughTypeofAClass",
			src: `
				declare class K { static plain(n: number) -> number }
				declare val c: typeof K
				val r = c.plain(1)
			`,
			binding: "r",
			want:    "number",
		},
		{
			// An index read takes the same path as a dotted one.
			name: "AnIndexReadBehindAnAlias",
			src: `
				type C = { plain(n: number) -> number }
				declare val c: C
				val r = c["plain"]
			`,
			binding: "r",
			want:    "fn (n: number) -> number",
		},
		{
			// A borrow the expansion uncovers is peeled, so the alias reads the way the
			// inline `mut {…}` spelling of the same type does.
			name: "AMethodBehindAnAliasNamingABorrow",
			src: `
				type M = mut { m(self) -> number }
				declare val v: M
				val r = v.m()
			`,
			binding: "r",
			want:    "number",
		},
		{
			// One name reached twice at different arguments is not a cycle, so the walk
			// keeps going. A guard keyed on the name alone would stop after `Id<{…}>`.
			name: "OneAliasNameReachedTwiceAtDifferentArguments",
			src: `
				type Id<T> = T
				declare val v: Id<Id<{ m(self) -> number }>>
				val r = v.m()
			`,
			binding: "r",
			want:    "number",
		},
		{
			// A property already resolved through the structural path, and still does.
			name: "APropertyBehindAnAlias",
			src: `
				type C = { tag: string }
				declare val c: C
				val r = c.tag
			`,
			binding: "r",
			want:    "string",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errorMessagesOf(errs))
			require.Equal(t, tt.want, values[tt.binding])
		})
	}
}

// A write to a setter behind an alias reaches the setter, so the write is accepted rather
// than blamed on a property the object does not declare.
func TestASetterWriteResolvesThroughAnAlias(t *testing.T) {
	_, _, errs := inferSource(t, `
		type C = { get v(self) -> number, set v(mut self, x: number) }
		declare val c: mut C
		fn f() { c.v = 5 }
	`)
	require.Empty(t, errorMessagesOf(errs))
}

// Reading through the handle widens what resolves; it does not make a miss resolve. A name
// no member carries is still reported, and reported once.
func TestAMissBehindAnAliasIsStillReported(t *testing.T) {
	_, _, errs := inferSource(t, `
		type C = { tag: string }
		declare val c: C
		val r = c.nope
	`)
	require.Equal(t, []string{"object is missing property: nope"}, errorMessagesOf(errs))
}

// An alias naming itself stops the walk rather than spinning it. Its budget runs out and the
// handle comes back, which declines the lookup. The alias is rejected where it is declared, so
// the read reports nothing of its own.
func TestASelfReferentialAliasDoesNotSpinTheWalk(t *testing.T) {
	_, _, errs := inferSource(t, `
		type A = A
		declare val a: A
		val r = a.m
	`)
	require.Equal(t, []string{
		"recursive type alias `A` reaches itself without passing under a type constructor, " +
			"so no lap of the recursion emits any structure and the alias names no type; " +
			"wrap the recursive reference in an object, tuple, or function type",
	}, errorMessagesOf(errs))
}
