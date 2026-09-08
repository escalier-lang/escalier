package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// An interface binds a type an annotation can name. It is structural, so a
// matching object literal type-checks against it.
func TestInterfaceBindsAStructuralType(t *testing.T) {
	values, types, errs := inferSource(t, `
		declare interface Point {
			x: number,
		}
		val p: Point = {x: 1}
	`)
	require.Empty(t, errorMessagesOf(errs))
	require.Equal(t, "{x: number, ...}", types["Point"])
	require.Equal(t, "Point", values["p"])
}

// Two interfaces of one name contribute to one type.
func TestInterfaceDeclarationsMerge(t *testing.T) {
	_, types, errs := inferSource(t, `
		declare interface Point {
			x: number,
		}
		declare interface Point {
			y: number,
		}
	`)
	require.Empty(t, errorMessagesOf(errs))
	require.Equal(t, "{x: number, y: number, ...}", types["Point"])
}

// An `extends` clause prepends the referenced interface's members, so the bound
// type is the flattened surface.
func TestInterfaceExtendsCarriesMembers(t *testing.T) {
	_, types, errs := inferSource(t, `
		declare interface Rect {
			w: number,
		}
		declare interface Sq extends Rect {
			s: number,
		}
	`)
	require.Empty(t, errorMessagesOf(errs))
	require.Equal(t, "Rect & {s: number, ...}", types["Sq"])
}

// An exported interface reaches a package's surface, so an importer can name it
// in an annotation.
func TestExportedInterfaceReachesTheSurface(t *testing.T) {
	res := InferModuleWithSource(
		parseModule(t, `import "shapes"`),
		sourceOf(t, map[string]string{
			"shapes": `
				export declare interface Point {
					x: number,
				}
			`,
		}),
	)
	require.Empty(t, errorMessagesOf(res.Errors))

	ns, ok := res.Packages.Lookup("shapes")
	require.True(t, ok)
	require.Contains(t, ns.Types, "Point")
}

// Overloads split across declarations accumulate rather than replacing one
// another, matching what two signatures in one declaration give.
func TestInterfaceMergeAccumulatesOverloads(t *testing.T) {
	_, types, errs := inferSource(t, `
		declare interface F {
			m(a: number) -> number,
		}
		declare interface F {
			m(a: string) -> string,
		}
	`)
	require.Empty(t, errorMessagesOf(errs))
	require.Equal(t, "{m(a: number) -> number; m(a: string) -> string, ...}", types["F"])
}

// A getter in one declaration and a setter in another form a pair, so the name
// stays both readable and writable.
func TestInterfaceMergePairsAccessors(t *testing.T) {
	_, types, errs := inferSource(t, `
		declare interface A {
			get x() -> number,
		}
		declare interface A {
			set x(value: number),
		}
	`)
	require.Empty(t, errorMessagesOf(errs))
	require.Equal(t, "{get x() -> number, set x(value: number), ...}", types["A"])
}

// A parent is named rather than copied, so extending it cannot change what the
// parent means for any other reader.
func TestInterfaceExtendsLeavesTheParentAlone(t *testing.T) {
	_, types, errs := inferSource(t, `
		declare interface Base {
			m(a: number) -> number,
		}
		declare interface Child extends Base {
			m(a: string) -> string,
		}
	`)
	require.Empty(t, errorMessagesOf(errs))
	require.Equal(t, "{m(a: number) -> number, ...}", types["Base"])
	require.Equal(t, "Base & {m(a: string) -> string, ...}", types["Child"])
}

// One name declared both as an interface and as another kind of type has two
// definitions that cannot merge. What is checked is that every declaration under the
// name's type key is an interface, rather than a list of the kinds that conflict, so
// a declaration kind added later is caught without revisiting the check.
//
// A value sharing the name is not a conflict. It binds under the name's value key,
// which the type key holds nothing of, so `fn Bar()` beside `interface Bar` declares
// one type and one value rather than one name twice.
func TestInterfaceConflictingWithAnotherTypeReports(t *testing.T) {
	const iface = "declare interface Bar { y: number }\n"
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "Alias",
			src:  iface + "type Bar = number",
			want: []string{"cannot declare Bar as both an interface and another type"},
		},
		{
			name: "Class",
			src:  iface + "class Bar { y: number }",
			want: []string{"cannot declare Bar as both an interface and another type"},
		},
		{
			name: "Enum",
			src:  iface + "enum Bar { A }",
			want: []string{"cannot declare Bar as both an interface and another type"},
		},
		{
			name: "TwoInterfacesMerge",
			src:  iface + "declare interface Bar { z: string }",
		},
		{
			name: "AFunctionOfTheSameNameIsNotAConflict",
			src:  iface + "fn Bar() -> number { return 1 }",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			if len(tt.want) == 0 {
				require.Empty(t, errorMessagesOf(errs))
				return
			}
			require.Equal(t, tt.want, errorMessagesOf(errs))
		})
	}
}

// A parent is referenced rather than read, so an interface naming a sibling that
// extends it back resolves. Nothing has to be flattened, which is what a cycle
// makes impossible.
func TestInterfaceExtendsResolvesARecursiveGroup(t *testing.T) {
	_, types, errs := inferSource(t, `
		declare interface A {
			b: B,
		}
		declare interface B extends A {
			x: number,
		}
	`)
	require.Empty(t, errorMessagesOf(errs))
	require.Equal(t, "{b: B, ...}", types["A"])
	require.Equal(t, "A & {x: number, ...}", types["B"])
}

// An interface is inexact: it names the members a value must carry, not the
// members it may carry.
func TestInterfaceIsInexact(t *testing.T) {
	_, _, errs := inferSource(t, `
		declare interface Rect {
			w: number,
		}
		val extra: Rect = {w: 1, other: 2}
	`)
	require.Empty(t, errorMessagesOf(errs))
}

// A member the parent names is still required, so `extends` is inheritance rather
// than a hint.
func TestInterfaceExtendsStillRequiresTheParentsMembers(t *testing.T) {
	_, _, errs := inferSource(t, `
		declare interface Rect {
			w: number,
		}
		declare interface Sq extends Rect {
			s: number,
		}
		val ok: Sq = {w: 1, s: 2}
		val missing: Sq = {s: 2}
	`)
	require.Equal(t, []string{"object is missing property: w"}, errorMessagesOf(errs))
}

// A member a parent declares is readable through a value the child types.
func TestInterfaceExtendsMembersAreReadable(t *testing.T) {
	values, _, errs := inferSource(t, `
		declare interface Rect {
			w: number,
		}
		declare interface Sq extends Rect {
			s: number,
		}
		val q: Sq = {w: 1, s: 2}
		val inherited = q.w
		val own = q.s
	`)
	require.Empty(t, errorMessagesOf(errs))
	require.Equal(t, "number", values["inherited"])
	require.Equal(t, "number", values["own"])
}

// One name binds one type-parameter list, so every declaration of an interface has to
// write the same one. A later declaration that disagrees is reported and contributes no
// members, since its body reads against parameters the group does not have.
//
// A bound and a default are the asymmetric cases: only the first declaration may write
// either, so a later declaration repeating a bound is rejected even when the bound is the
// one already in force.
func TestInterfaceTypeParamMismatchReports(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "Arity",
			src:  "declare interface Box<T> { v: T }\ndeclare interface Box<T, U> { w: U }",
			want: []string{"declarations of interface Box must agree on their type parameters: they differ in how many it writes"},
		},
		{
			name: "Names",
			src:  "declare interface Box<T> { v: T }\ndeclare interface Box<U> { w: U }",
			want: []string{"declarations of interface Box must agree on their type parameters: they differ in the names they write"},
		},
		{
			name: "Variance",
			src:  "declare interface Box<T> { v: T }\ndeclare interface Box<out T> { w: T }",
			want: []string{"declarations of interface Box must agree on their type parameters: they differ in variance"},
		},
		{
			name: "ALaterBound",
			src:  "declare interface Box<T> { v: T }\ndeclare interface Box<T: string> { w: T }",
			want: []string{"declarations of interface Box must agree on their type parameters: only the first declaration may write a bound"},
		},
		{
			name: "ALaterDefault",
			src:  "declare interface Box<T> { v: T }\ndeclare interface Box<T = string> { w: T }",
			want: []string{"declarations of interface Box must agree on their type parameters: only the first declaration may write a default"},
		},
		{
			name: "MatchingListsMerge",
			src:  "declare interface Box<T> { v: T }\ndeclare interface Box<T> { w: T }",
		},
		{
			// The bound belongs to the first declaration, which is the one that may write
			// it, so the group merges and both members land.
			name: "ABoundOnTheFirstDeclarationIsKept",
			src:  "declare interface Box<T: string> { v: T }\ndeclare interface Box<T> { w: T }",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			if len(tt.want) == 0 {
				require.Empty(t, errorMessagesOf(errs))
				return
			}
			require.Equal(t, tt.want, errorMessagesOf(errs))
		})
	}
}
