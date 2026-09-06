package solver

import (
	"fmt"
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
			import { Point } from "pkg:geometry"
			val p = Point(1, 2)
		`),
		sourceOf(t, map[string]string{
			"pkg:geometry": `
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
			import { Hidden } from "pkg:internals"
		`),
		sourceOf(t, map[string]string{
			"pkg:internals": `
				export val shown: number = 1
				val Hidden: number = 2
			`,
		}),
	)

	require.Equal(t, []string{`package "pkg:internals" exports no "Hidden"`},
		errorMessagesOf(res.Errors))

	ns, ok := res.Packages.Lookup("pkg:internals")
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
			import { fromA } from "pkg:a"
			val x: number = fromA
		`),
		sourceOf(t, map[string]string{
			"pkg:a": `
				import { fromB } from "pkg:b"
				export val fromA: number = 1
			`,
			"pkg:b": `
				import { fromA } from "pkg:a"
				export val fromB: number = 2
			`,
		}),
	)

	// `pkg:b` re-enters `pkg:a` mid-load, so `fromA` is not bound on that side and
	// the reference in `pkg:b` is unresolved. That surfaces as one diagnostic on
	// the entry module's import, carrying the package's own message. What this
	// pins is that the run finishes at all, and that the entry module still gets
	// what it asked for.
	require.Equal(t, "number", soltype.Print(inferredValueType(t, res.Scope, "x")))

	_, loadedA := res.Packages.Lookup("pkg:a")
	require.True(t, loadedA, "pkg:a should be published after the walk")
	_, loadedB := res.Packages.Lookup("pkg:b")
	require.True(t, loadedB, "pkg:b should be published after the walk")
	require.False(t, res.Packages.Loading("pkg:a"), "no package should still be loading")
	require.False(t, res.Packages.Loading("pkg:b"), "no package should still be loading")
}

// Two modules declaring a class of the same name register two definitions. The
// package URI on every key is what keeps them apart in the one nominal registry
// a run shares.
func TestTwoPackagesDeclaringOneNameStayDistinct(t *testing.T) {
	t.Parallel()

	res := InferModuleWithSource(
		parseModule(t, `
			import { Point as Flat } from "pkg:plane"
			import { Point as Deep } from "pkg:space"
			val a = Flat(1)
			val b = Deep(1)
			val ax = a.x
			val bz = b.z
		`),
		sourceOf(t, map[string]string{
			"pkg:plane": `
				export class Point {
					x: number,
				}
			`,
			"pkg:space": `
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
	require.Equal(t, "import:pkg:plane.Point", soltype.PrintQualified(flat))
	require.Equal(t, "import:pkg:space.Point", soltype.PrintQualified(deep))

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
		parseModule(t, `import { thing } from "pkg:missing"`),
		sourceOf(t, map[string]string{}),
	)

	require.Equal(t, []string{`cannot resolve import "pkg:missing": no such package`},
		errorMessagesOf(res.Errors))
}

// A run with no module source reports its imports rather than loading anything,
// which is what the single-module entry point passes.
func TestImportWithNoModuleSourceReports(t *testing.T) {
	t.Parallel()

	_, _, errs := InferModule(parseModule(t, `import { thing } from "pkg:anything"`))

	require.Equal(t,
		[]string{`cannot resolve import "pkg:anything": this inference run was given no module source`},
		errorMessagesOf(errs))
}

// An import binds into the importing file's own scope, so a sibling file of the
// same module does not see it.
func TestImportsDoNotLeakAcrossFiles(t *testing.T) {
	t.Parallel()

	module := parseModuleFiles(t, map[string]string{
		"a.esc": `
			import { shared } from "pkg:lib"
			val fromA: number = shared
		`,
		"b.esc": `
			val fromB: number = 1
		`,
	})
	res := InferModuleWithSource(module, sourceOf(t, map[string]string{
		"pkg:lib": `export val shared: number = 1`,
	}))

	require.Empty(t, errorMessagesOf(res.Errors))
	require.Len(t, res.FileScopes, 2)

	importing := res.FileScopes[0]
	_, boundHere := importing.values["shared"]
	require.True(t, boundHere, "the importing file should bind `shared` in its own scope")

	sibling := res.FileScopes[1]
	_, boundThere := sibling.values["shared"]
	require.False(t, boundThere, "a sibling file should not see another file's import")
}

// A bare import binds the package as a namespace under the last segment of its
// URI.
func TestBareImportBindsTheLastURISegment(t *testing.T) {
	t.Parallel()

	module := parseModuleFiles(t, map[string]string{
		"a.esc": `import "pkg:shapes"`,
	})
	res := InferModuleWithSource(module, sourceOf(t, map[string]string{
		"pkg:shapes": `export val sides: number = 3`,
	}))

	require.Empty(t, errorMessagesOf(res.Errors))
	ns, ok := res.FileScopes[0].GetNamespace("shapes")
	require.True(t, ok, "expected the package bound under `shapes`")
	require.Contains(t, ns.Values, "sides")
}

// A package exports its types, not only its values. The registry keys a type
// under the package URI while an importer names it bare, so the surface has to
// re-key it.
func TestImportResolvesAnExportedType(t *testing.T) {
	t.Parallel()

	res := InferModuleWithSource(
		parseModule(t, `
			import { Point, Num } from "pkg:geometry"
			val p: Point = Point(1, 2)
			val n: Num = 3
		`),
		sourceOf(t, map[string]string{
			"pkg:geometry": `
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
			import { origin } from "pkg:geometry"
			val x = origin.x
		`),
		sourceOf(t, map[string]string{
			"pkg:geometry": `
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
			import "pkg:shapes"
			val n = shapes.geometry.sides
		`,
	})
	// The package's own file layout is what puts `sides` in a namespace: a file
	// under geometry/ declares into the `geometry` namespace, the same rule the
	// entry module follows.
	res := InferModuleWithSource(module, func(uri string) (*ast.Module, string, error) {
		if uri != "pkg:shapes" {
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
		parseModule(t, `import { broken } from "pkg:bad"`),
		sourceOf(t, map[string]string{
			"pkg:bad": `export val broken: number = nowhere`,
		}),
	)

	require.Equal(t, []string{
		"package \"pkg:bad\" (pkg:bad.esc) has 1 error(s):\n  Unknown identifier: nowhere",
	}, errorMessagesOf(res.Errors))
}

// A package that reports diagnostics still publishes what it declared, so an
// importer works against the declarations it does have and a second import does
// not re-run the failed walk.
func TestAFailingPackageStillPublishesItsSurface(t *testing.T) {
	t.Parallel()

	res := InferModuleWithSource(
		parseModule(t, `import { good } from "pkg:partial"`),
		sourceOf(t, map[string]string{
			"pkg:partial": `
				export val good: number = 1
				export val bad: number = nowhere
			`,
		}),
	)

	require.Len(t, res.Errors, 1)
	ns, ok := res.Packages.Lookup("pkg:partial")
	require.True(t, ok)
	require.Contains(t, ns.Values, "good")
}

// An exported enum arrives with its variant constructors. The type and the
// namespace holding the constructors are two bindings, and a consumer needs
// both.
func TestImportResolvesAnExportedEnum(t *testing.T) {
	t.Parallel()

	res := InferModuleWithSource(
		parseModule(t, `
			import { Color } from "pkg:paint"
			val c = Color.Red()
		`),
		sourceOf(t, map[string]string{
			"pkg:paint": `
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

// A named specifier can name a namespace the package declares, not only a value
// or a type.
func TestNamedImportOfANamespace(t *testing.T) {
	t.Parallel()

	res := InferModuleWithSource(
		parseModule(t, `
			import { geometry } from "pkg:shapes"
			val n = geometry.sides
		`),
		func(uri string) (*ast.Module, string, error) {
			if uri != "pkg:shapes" {
				return nil, "", fmt.Errorf("no such package")
			}
			return parseModuleFiles(t, map[string]string{
				"geometry/shapes.esc": `export val sides: number = 3`,
			}), "shapes.esc", nil
		},
	)

	require.Empty(t, errorMessagesOf(res.Errors))
	require.Equal(t, "number", soltype.Print(inferredValueType(t, res.Scope, "n")))
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

// A package resolves its own declaration of a name the prelude also seeds. The
// prelude's placeholder is in the root scope and the package's declaration is in
// its module scope, so the nearer one has to win.
func TestPackageDeclarationOutranksThePreludeSeed(t *testing.T) {
	t.Parallel()

	res := InferModuleWithSource(
		parseModule(t, `
			import { make } from "pkg:futures"
			val p = make()
			val v = p.value
		`),
		sourceOf(t, map[string]string{
			"pkg:futures": `
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
