package solver

import (
	"fmt"
	"sort"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// sourceOf returns a ModuleSource over a fixed set of packages, each written as
// Escalier source. A URI the map does not hold reports the same way a missing
// file would.
func sourceOf(t *testing.T, packages map[string]string) ModuleSource {
	t.Helper()
	return func(uri string) (*ast.Module, string, error) {
		src, ok := packages[uri]
		if !ok {
			return nil, "", fmt.Errorf("no such package")
		}
		return parseModuleFiles(t, map[string]string{uri + ".esc": src}), uri + ".esc", nil
	}
}

// errorMessagesOf renders each diagnostic's message, so a test asserts the text
// a user reads rather than a type name.
func errorMessagesOf(errs []SolverError) []string {
	out := make([]string, 0, len(errs))
	for _, e := range errs {
		out = append(out, e.Message())
	}
	return out
}

// An import resolves a class another module declares, and the importing module
// types against it.
func TestImportResolvesAClassFromAnotherModule(t *testing.T) {
	t.Parallel()

	res := InferModuleWithSource(
		parseModule(t, `
			import "geometry"
			val p = geometry.Point(1, 2)
		`),
		sourceOf(t, map[string]string{
			"geometry": `
				export class Point {
					x: number,
					y: number,
				}
			`,
		}),
	)

	require.Empty(t, errorMessagesOf(res.Errors))

	require.Equal(t, "Point", soltype.Print(inferredValueType(t, res.Scope, "p")))
}

// A package's surface holds only what it exports, so a consumer cannot name an
// internal declaration.
func TestImportSeesOnlyExportedDeclarations(t *testing.T) {
	t.Parallel()

	res := InferModuleWithSource(
		parseModule(t, `
			import "internals"
			val h = internals.Hidden
		`),
		sourceOf(t, map[string]string{
			"internals": `
				export val shown: number = 1
				val Hidden: number = 2
			`,
		}),
	)

	require.Equal(t, []string{"Namespace internals has no member: Hidden"},
		errorMessagesOf(res.Errors))

	ns, ok := res.Packages.Lookup("internals")
	require.True(t, ok)
	require.Contains(t, ns.Values, "shown")
	require.NotContains(t, ns.Values, "Hidden")
}

// Two packages importing each other terminate. The second entry into a package
// still being loaded reads the cycle sentinel and binds nothing, rather than
// recurring into a load that has not finished.
func TestMutuallyImportingPackagesTerminate(t *testing.T) {
	t.Parallel()

	res := InferModuleWithSource(
		parseModule(t, `
			import "a"
			val x: number = a.fromA
		`),
		sourceOf(t, map[string]string{
			"a": `
				import "b"
				export val fromA: number = 1
			`,
			"b": `
				import "a"
				export val fromB: number = 2
			`,
		}),
	)

	// The cycle is reported where it closes, in `b`, and reaches the entry
	// module through the wrapping every package diagnostic takes. One cycle gives
	// one diagnostic, naming the packages the loop runs through.
	require.Equal(t, []string{
		"package \"a\" (a.esc) has 1 error(s):\n" +
			"  package \"b\" (b.esc) has 1 error(s):\n" +
			"  import cycle: \"a\" -> \"b\" -> \"a\"",
	}, errorMessagesOf(res.Errors))

	// The run still finishes and the entry module still gets what it asked for:
	// each package publishes what it declared before the cycle closed.
	require.Equal(t, "number", soltype.Print(inferredValueType(t, res.Scope, "x")))

	_, loadedA := res.Packages.Lookup("a")
	require.True(t, loadedA, "a should be published after the walk")
	_, loadedB := res.Packages.Lookup("b")
	require.True(t, loadedB, "b should be published after the walk")
	require.False(t, res.Packages.Loading("a"), "no package should still be loading")
	require.False(t, res.Packages.Loading("b"), "no package should still be loading")
}

// A cycle longer than a pair names every package it runs through, in the order
// the imports close it.
func TestAThreePackageCycleNamesTheWholeLoop(t *testing.T) {
	t.Parallel()

	res := InferModuleWithSource(
		parseModule(t, `import "a"`),
		sourceOf(t, map[string]string{
			"a": `import "b"`,
			"b": `import "c"`,
			"c": `import "a"`,
		}),
	)

	require.Len(t, res.Errors, 1)
	require.Contains(t, res.Errors[0].Message(),
		`import cycle: "a" -> "b" -> "c" -> "a"`)
}

// A package importing itself is the shortest cycle, and reads as one.
func TestAPackageImportingItselfIsACycle(t *testing.T) {
	t.Parallel()

	res := InferModuleWithSource(
		parseModule(t, `import "solo"`),
		sourceOf(t, map[string]string{
			"solo": `import "solo"`,
		}),
	)

	require.Len(t, res.Errors, 1)
	require.Contains(t, res.Errors[0].Message(),
		`import cycle: "solo" -> "solo"`)
}

// Two modules declaring a class of the same name register two definitions. The
// package URI on every key is what keeps them apart in the one nominal registry
// a run shares.
func TestTwoPackagesDeclaringOneNameStayDistinct(t *testing.T) {
	t.Parallel()

	res := InferModuleWithSource(
		parseModule(t, `
			import "plane"
			import "space"
			val a = plane.Point(1)
			val b = space.Point(1)
			val ax = a.x
			val bz = b.z
		`),
		sourceOf(t, map[string]string{
			"plane": `
				export class Point {
					x: number,
				}
			`,
			"space": `
				export class Point {
					z: number,
				}
			`,
		}),
	)

	require.Empty(t, errorMessagesOf(res.Errors))

	flat := inferredValueType(t, res.Scope, "a")
	deep := inferredValueType(t, res.Scope, "b")

	// Both render as `Point`, since the display name strips the qualifier. The
	// qualified names are what tell the two apart.
	require.Equal(t, "Point", soltype.Print(flat))
	require.Equal(t, "Point", soltype.Print(deep))
	require.Equal(t, "import:plane.Point", soltype.PrintQualified(flat))
	require.Equal(t, "import:space.Point", soltype.PrintQualified(deep))

	// Each instance carries its own package's field. One shared definition would
	// leave one of these two reads unresolved, so this is what proves the two
	// registry entries are separate definitions rather than separate names for
	// one.
	require.Equal(t, "number", soltype.Print(inferredValueType(t, res.Scope, "ax")))
	require.Equal(t, "number", soltype.Print(inferredValueType(t, res.Scope, "bz")))
}

// A URI no source answers is a diagnostic on the import, not a failed run.
func TestImportOfAnUnknownPackageReports(t *testing.T) {
	t.Parallel()

	res := InferModuleWithSource(
		parseModule(t, `import "missing"`),
		sourceOf(t, map[string]string{}),
	)

	require.Equal(t, []string{`cannot resolve import "missing": no such package`},
		errorMessagesOf(res.Errors))
}

// A run with no module source reports its imports rather than loading anything,
// which is what the single-module entry point passes.
func TestImportWithNoModuleSourceReports(t *testing.T) {
	t.Parallel()

	_, _, errs := InferModule(parseModule(t, `import "anything"`))

	require.Equal(t,
		[]string{`cannot resolve import "anything": this inference run was given no module source`},
		errorMessagesOf(errs))
}

// An import binds into the importing file's own scope, so a sibling file of the
// same module does not see it.
func TestImportsDoNotLeakAcrossFiles(t *testing.T) {
	t.Parallel()

	module := parseModuleFiles(t, map[string]string{
		"a.esc": `
			import "lib"
			val fromA: number = lib.shared
		`,
		"b.esc": `
			val fromB: number = lib.shared
		`,
	})
	res := InferModuleWithSource(module, sourceOf(t, map[string]string{
		"lib": `export val shared: number = 1`,
	}))

	// `b.esc` writes the same expression as `a.esc` but never imports the
	// package, so `lib` is not a name it can see.
	require.Equal(t, []string{"Unknown identifier: lib"}, errorMessagesOf(res.Errors))

	require.Len(t, res.FileScopes, 2)

	importing := res.FileScopes[0]
	_, boundHere := importing.namespaces["lib"]
	require.True(t, boundHere, "the importing file should bind `lib` in its own scope")

	sibling := res.FileScopes[1]
	_, boundThere := sibling.namespaces["lib"]
	require.False(t, boundThere, "a sibling file should not see another file's import")
}

// A package exports its types, not only its values. The registry keys a type
// under the package URI while an importer names it bare, so the surface has to
// re-key it.
func TestImportResolvesAnExportedType(t *testing.T) {
	t.Parallel()

	res := InferModuleWithSource(
		parseModule(t, `
			import "geometry"
			val p: geometry.Point = geometry.Point(1, 2)
			val n: geometry.Num = 3
		`),
		sourceOf(t, map[string]string{
			"geometry": `
				export type Num = number
				export class Point {
					x: number,
					y: number,
				}
			`,
		}),
	)

	require.Empty(t, errorMessagesOf(res.Errors))
	require.Equal(t, "Point", soltype.Print(inferredValueType(t, res.Scope, "p")))
	require.Equal(t, "Num", soltype.Print(inferredValueType(t, res.Scope, "n")))
}

// A package resolves the types it declares itself. Registration keys them under
// the package URI, so a bare reference inside the package has to reach the same
// key.
func TestPackageResolvesItsOwnTypes(t *testing.T) {
	t.Parallel()

	res := InferModuleWithSource(
		parseModule(t, `
			import "geometry"
			val x = geometry.origin.x
		`),
		sourceOf(t, map[string]string{
			"geometry": `
				export class Point {
					x: number,
					y: number,
				}
				export val origin: Point = Point(0, 0)
			`,
		}),
	)

	require.Empty(t, errorMessagesOf(res.Errors))
	require.Equal(t, "number", soltype.Print(inferredValueType(t, res.Scope, "x")))
}

// A namespace a package declares reaches an importer as a nested namespace, so
// a member access walks it segment by segment.
func TestBareImportReachesANestedNamespace(t *testing.T) {
	t.Parallel()

	module := parseModuleFiles(t, map[string]string{
		"a.esc": `
			import "shapes"
			val n = shapes.geometry.sides
		`,
	})
	// The package's own file layout is what puts `sides` in a namespace: a file
	// under geometry/ declares into the `geometry` namespace, the same rule the
	// entry module follows.
	res := InferModuleWithSource(module, func(uri string) (*ast.Module, string, error) {
		if uri != "shapes" {
			return nil, "", fmt.Errorf("no such package")
		}
		return parseModuleFiles(t, map[string]string{
			"geometry/shapes.esc": `export val sides: number = 3`,
		}), "shapes.esc", nil
	})

	require.Empty(t, errorMessagesOf(res.Errors))
	require.Equal(t, "number", soltype.Print(inferredValueType(t, res.Scope, "n")))
}

// A package's own diagnostics reach the importer as one error on the import,
// carrying the package's messages as text. Reporting them directly would blame
// the importing file at an offset that belongs to another module's source.
func TestPackageDiagnosticsAreReportedOnTheImport(t *testing.T) {
	t.Parallel()

	res := InferModuleWithSource(
		parseModule(t, `import "bad"`),
		sourceOf(t, map[string]string{
			"bad": `export val broken: number = nowhere`,
		}),
	)

	require.Equal(t, []string{
		"package \"bad\" (bad.esc) has 1 error(s):\n  Unknown identifier: nowhere",
	}, errorMessagesOf(res.Errors))
}

// A package that reports diagnostics still publishes what it declared, so an
// importer works against the declarations it does have and a second import does
// not re-run the failed walk.
func TestAFailingPackageStillPublishesItsSurface(t *testing.T) {
	t.Parallel()

	res := InferModuleWithSource(
		parseModule(t, `
			import "partial"
			val g = partial.good
			val b = partial.bad
		`),
		sourceOf(t, map[string]string{
			"partial": `
				export val good: number = 1
				export val bad: number = nowhere
			`,
		}),
	)

	// The package's own diagnostic is the only one. Reading either member from
	// the importer adds nothing, so the failure does not travel.
	require.Len(t, res.Errors, 1)

	// `bad` reaches the surface alongside `good`. Its initializer is what failed,
	// and the surface takes the declared type rather than dropping the name, so a
	// consumer types against the whole package instead of the prefix that
	// happened to infer.
	ns, ok := res.Packages.Lookup("partial")
	require.True(t, ok)
	require.Contains(t, ns.Values, "good")
	require.Contains(t, ns.Values, "bad")

	require.Equal(t, "number", soltype.Print(inferredValueType(t, res.Scope, "g")))
	require.Equal(t, "number", soltype.Print(inferredValueType(t, res.Scope, "b")))
}

// An exported enum arrives with its variant constructors. The type and the
// namespace holding the constructors are two bindings, and a consumer needs
// both.
func TestImportResolvesAnExportedEnum(t *testing.T) {
	t.Parallel()

	res := InferModuleWithSource(
		parseModule(t, `
			import "paint"
			val c = paint.Color.Red()
		`),
		sourceOf(t, map[string]string{
			"paint": `
				export enum Color {
					Red,
					Green,
				}
			`,
		}),
	)

	require.Empty(t, errorMessagesOf(res.Errors))
	require.Equal(t, "Color", soltype.Print(inferredValueType(t, res.Scope, "c")))
}

// A declaration in one file of a module is visible to its sibling files. The
// file scope carries that file's imports and nothing else, so a declaration
// stays module-scoped even though it was inferred under a file scope.
func TestDeclarationsStayVisibleAcrossFiles(t *testing.T) {
	t.Parallel()

	res := InferModuleWithSource(parseModuleFiles(t, map[string]string{
		"a.esc": `
			export enum Color { Red, Green }
			export class Point { x: number, }
			export type Num = number
		`,
		"b.esc": `
			val c = Color.Red()
			val p = Point(1)
			val n: Num = 2
		`,
	}), nil)

	require.Empty(t, errorMessagesOf(res.Errors))
	require.Equal(t, "Color", soltype.Print(inferredValueType(t, res.Scope, "c")))
	require.Equal(t, "Point", soltype.Print(inferredValueType(t, res.Scope, "p")))
	require.Equal(t, "Num", soltype.Print(inferredValueType(t, res.Scope, "n")))
}

// A package resolves its own declaration of a name the root scope also seeds. The
// placeholder is in the root scope and the package's declaration is in its module
// scope, so the nearer one has to win.
//
// `Promise` is one of the five names `stdlibTypePlaceholders` seeds. This run
// supplies no `std:prelude`, so the placeholder is what the declaration outranks.
// TestModuleDeclarationOutranksThePrelude covers the same ranking against a
// prelude package that did load.
func TestPackageDeclarationOutranksThePreludeSeed(t *testing.T) {
	t.Parallel()

	res := InferModuleWithSource(
		parseModule(t, `
			import "futures"
			val p = futures.make()
			val v = p.value
		`),
		sourceOf(t, map[string]string{
			"futures": `
				export class Promise {
					value: number,
				}
				export fn make() -> Promise {
					return Promise(1)
				}
			`,
		}),
	)

	require.Empty(t, errorMessagesOf(res.Errors))
	require.Equal(t, "Promise", soltype.Print(inferredValueType(t, res.Scope, "p")))
	require.Equal(t, "number", soltype.Print(inferredValueType(t, res.Scope, "v")))
}

// The key prefix keeps two packages apart whose URIs a plain join would
// conflate. A namespace segment is an identifier, so it holds neither a colon
// nor a percent sign, and the prefix's own dots are escaped.
func TestPackageKeyPrefixIsUnambiguous(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		uri  string
		want string
	}{
		"TheEntryModuleHasNoPrefix": {uri: "", want: ""},
		"ASchemeURI":                {uri: "std:array", want: "import:std:array"},
		// A bare specifier carries no colon, so the marker is what stops
		// `lodash.Point` naming both this package's class and one in a `lodash/`
		// namespace.
		"ABareSpecifier": {uri: "lodash", want: "import:lodash"},
		// A dot in the URI would otherwise read as a namespace boundary, making
		// `npm:a.b`'s `D` and `npm:a`'s `b.D` one key.
		"AURIHoldingADot":     {uri: "npm:a.b", want: "import:npm:a%2Eb"},
		"AURIHoldingAPercent": {uri: "npm:a%b", want: "import:npm:a%25b"},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, test.want, packageKeyPrefix(test.uri))
		})
	}

	// The two the escaping exists to separate.
	require.NotEqual(t,
		qualify(packageKeyPrefix("npm:a.b"), "D"),
		qualify(packageKeyPrefix("npm:a"), qualify("b", "D")))
}

// The registry answers what it holds: the path a package was read from, and
// every URI a run reached.
func TestPackageRegistryReportsWhatItHolds(t *testing.T) {
	t.Parallel()

	r := NewPackageRegistry()
	require.Equal(t, "", r.Path("absent"), "an unknown URI has no path")
	require.Empty(t, r.URIs())

	r.markLoading("first", "first.esc")
	require.True(t, r.Loading("first"))
	require.Equal(t, "first.esc", r.Path("first"))
	ns, found := r.Lookup("first")
	require.True(t, found, "a loading URI is found")
	require.Nil(t, ns, "a loading URI answers the cycle sentinel")

	r.publish("first", newNamespace("first"))
	require.False(t, r.Loading("first"))
	ns, found = r.Lookup("first")
	require.True(t, found)
	require.NotNil(t, ns)

	// A publish for a URI no load opened records it anyway, so a surface is
	// never dropped for want of a preceding markLoading.
	r.publish("second", newNamespace("second"))
	require.Equal(t, "", r.Path("second"))

	uris := r.URIs()
	sort.Strings(uris)
	require.Equal(t, []string{"first", "second"}, uris)
}

// A destructuring export carries every name its pattern binds, so a consumer
// can import any leaf of it. An extractor and an instance pattern bind through
// their sub-patterns, which a walk written for tuples and objects alone misses.
func TestExportedSurfaceCarriesEveryPatternLeaf(t *testing.T) {
	t.Parallel()

	res := InferModuleWithSource(
		parseModule(t, `
			import "shapes"
			val a = shapes.first
			val b = shapes.rest
			val c = shapes.x
			val d = shapes.y
			val e = shapes.inner
		`),
		sourceOf(t, map[string]string{
			"shapes": `
				class Some { value: number, }
				export val [first, ...rest] = [1, 2, 3]
				export val {x, y} = {x: 1, y: 2}
				export val Some(inner) = Some(1)
			`,
		}),
	)

	require.Empty(t, errorMessagesOf(res.Errors))
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		_, ok := res.Scope.GetValue(name)
		require.True(t, ok, "expected %q bound", name)
	}
}

// A package whose name is not an identifier still binds a reachable namespace,
// since the derived name maps every character an identifier cannot hold.
func TestHyphenatedPackageBindsAWritableName(t *testing.T) {
	t.Parallel()

	res := InferModuleWithSource(
		parseModule(t, `
			import "fast-deep-equal"
			val n = fast_deep_equal.isEqual
		`),
		sourceOf(t, map[string]string{
			"fast-deep-equal": `export val isEqual: number = 1`,
		}),
	)

	require.Empty(t, errorMessagesOf(res.Errors))
	require.Equal(t, "number", soltype.Print(inferredValueType(t, res.Scope, "n")))
}

// `as` picks the name instead, which is how an author avoids a derived one.
func TestImportAliasBindsUnderTheChosenName(t *testing.T) {
	t.Parallel()

	res := InferModuleWithSource(
		parseModule(t, `
			import "fast-deep-equal" as fde
			val n = fde.isEqual
		`),
		sourceOf(t, map[string]string{
			"fast-deep-equal": `export val isEqual: number = 1`,
		}),
	)

	require.Empty(t, errorMessagesOf(res.Errors))
	require.Equal(t, "number", soltype.Print(inferredValueType(t, res.Scope, "n")))
	_, derived := res.FileScopes[0].GetNamespace("fast_deep_equal")
	require.False(t, derived, "an alias replaces the derived name rather than adding to it")
}

// A specifier whose scheme is not lowercase is not diverted to the
// pseudo-package path, so it reaches the npm side with its colon intact and
// binds what follows the colon.
func TestBareImportDropsANonSchemePrefix(t *testing.T) {
	t.Parallel()

	require.False(t, IsSchemePrefixedImport("HTTP:thing"),
		"an uppercase scheme names no pseudo-package family")

	res := InferModuleWithSource(
		parseModule(t, `
			import "HTTP:thing"
			val n = thing.value
		`),
		sourceOf(t, map[string]string{
			"HTTP:thing": `export val value: number = 1`,
		}),
	)

	require.Empty(t, errorMessagesOf(res.Errors))
	require.Equal(t, "number", soltype.Print(inferredValueType(t, res.Scope, "n")))
}

// A bare import of a path binds the last segment, so `lodash/fp` binds `fp`.
func TestBareImportOfAPathBindsItsLastSegment(t *testing.T) {
	t.Parallel()

	res := InferModuleWithSource(
		parseModule(t, `
			import "lodash/fp"
			val n = fp.value
		`),
		// A flat filename, so the package's own export lands at its root rather
		// than in a namespace derived from the specifier's directory.
		func(uri string) (*ast.Module, string, error) {
			if uri != "lodash/fp" {
				return nil, "", fmt.Errorf("no such package")
			}
			return parseModuleFiles(t, map[string]string{
				"fp.esc": `export val value: number = 1`,
			}), "fp.esc", nil
		},
	)

	require.Empty(t, errorMessagesOf(res.Errors))
	require.Equal(t, "number", soltype.Print(inferredValueType(t, res.Scope, "n")))
}

// A qualified annotation reaches the package the import names, not a same-named
// declaration the file made itself. `outer` declares its own `Widget` and imports
// another, so `inner.Widget` has to answer with the imported one.
func TestAQualifiedNameReachesTheImportedDeclaration(t *testing.T) {
	t.Parallel()

	res := InferModuleWithSource(
		parseModule(t, `
			import "outer"
			val w = outer.make()
			val tag = w.fromInner
		`),
		sourceOf(t, map[string]string{
			"outer": `
				import "inner"
				export class Widget {
					fromOuter: number,
				}
				export fn make() -> inner.Widget {
					return inner.Widget(1)
				}
			`,
			"inner": `
				export class Widget {
					fromInner: number,
				}
			`,
		}),
	)

	require.Empty(t, errorMessagesOf(res.Errors))
	require.Equal(t, "number", soltype.Print(inferredValueType(t, res.Scope, "tag")))
}
