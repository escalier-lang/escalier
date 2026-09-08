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
	require.Equal(t, "{x: number}", types["Point"])
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
	require.Equal(t, "{x: number, y: number}", types["Point"])
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
	require.Equal(t, "{w: number, s: number}", types["Sq"])
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
	require.Equal(t, "{m(a: number) -> number; m(a: string) -> string}", types["F"])
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
	require.Equal(t, "{get x() -> number, set x(value: number)}", types["A"])
}

// An interface's members are copied out of a parent's stored type, so extending it
// leaves the parent unchanged for every other reader.
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
	require.Equal(t, "{m(a: number) -> number}", types["Base"])
	require.Equal(t, "{m(a: number) -> number; m(a: string) -> string}", types["Child"])
}

// Declarations of one interface must agree on their type parameters, since the
// group binds one list and every body resolves against it.
func TestInterfaceRejectsMismatchedTypeParams(t *testing.T) {
	_, types, errs := inferSource(t, `
		declare interface Box<T> {
			a: T,
		}
		declare interface Box<U> {
			b: U,
		}
	`)
	require.Equal(t,
		[]string{"declarations of interface Box must agree on their type parameters: " +
			"they differ in the names they write"},
		errorMessagesOf(errs))
	// The rejected declaration contributes nothing, so no unresolved parameter
	// leaks into the bound type.
	require.Equal(t, "{a: T}", types["Box"])
}

// One name declared both as an interface and as another kind of type has two
// definitions that cannot merge.
func TestInterfaceConflictingWithAnAliasReports(t *testing.T) {
	_, _, errs := inferSource(t, `
		type Bar = number
		declare interface Bar {
			y: number,
		}
	`)
	require.Equal(t,
		[]string{"cannot declare Bar as both an interface and another type"},
		errorMessagesOf(errs))
}

// An `extends` target in the interface's own recursive group has no finished
// member list to flatten.
func TestInterfaceExtendsCycleReports(t *testing.T) {
	_, _, errs := inferSource(t, `
		declare interface A {
			b: B,
		}
		declare interface B extends A {
			x: number,
		}
	`)
	require.Equal(t,
		[]string{"an interface cannot extend A, which is part of the same recursive group"},
		errorMessagesOf(errs))
}

// The first declaration's parameter list is what every body resolves against, so a
// bound a later declaration writes would be ignored. Merging under one of two
// disagreeing bounds is what the report prevents.
func TestInterfaceRejectsAConflictingTypeParamBound(t *testing.T) {
	_, types, errs := inferSource(t, `
		declare interface Box<T: string> {
			a: T,
		}
		declare interface Box<T: number> {
			b: T,
		}
	`)
	require.Equal(t,
		[]string{"declarations of interface Box must agree on their type parameters: " +
			"only the first declaration may write a bound"},
		errorMessagesOf(errs))
	require.Equal(t, "{a: T}", types["Box"])
}

// A default is rejected for the same reason a bound is: an omitted argument is
// filled from the first declaration's list, so a later default names nothing.
func TestInterfaceRejectsAConflictingTypeParamDefault(t *testing.T) {
	_, _, errs := inferSource(t, `
		declare interface Box<T = string> {
			a: T,
		}
		declare interface Box<T = number> {
			b: T,
		}
	`)
	require.Equal(t,
		[]string{"declarations of interface Box must agree on their type parameters: " +
			"only the first declaration may write a default"},
		errorMessagesOf(errs))
}

// A later declaration that writes the same names and no bound merges, which is the
// ordinary case a generic interface split across declarations takes.
func TestInterfaceMergesWhenLaterDeclarationsOmitBounds(t *testing.T) {
	_, types, errs := inferSource(t, `
		declare interface Box<T: string> {
			a: T,
		}
		declare interface Box<T> {
			b: T,
		}
	`)
	require.Empty(t, errorMessagesOf(errs))
	require.Equal(t, "{a: T, b: T}", types["Box"])
}
