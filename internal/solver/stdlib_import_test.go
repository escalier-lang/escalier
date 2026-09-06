package solver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// seedStdlib writes a pseudo-package tree and returns its root. Each key is a
// slash-separated path under the root, so "std/math.esc" declares `std:math`.
//
// The tree is hand-written rather than the committed one on purpose. A test
// against three declarations says what it means, and it does not churn every
// time the converter regenerates what ships.
//
// Each package writes initialized values rather than the `declare val` a real
// pseudo-package uses. The solver reports a `declare` without an initializer as
// a missing one; the lib-shaped declaration kinds are #1238.
func seedStdlib(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, contents := range files {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(contents), 0o644))
	}
	return dir
}

// inferAgainstStdlib infers src against a pseudo-package tree built from files.
func inferAgainstStdlib(t *testing.T, src string, files map[string]string) *ModuleResult {
	t.Helper()
	return InferModuleWithSource(parseModule(t, src), StdlibSource(seedStdlib(t, files)))
}

// A bare import binds the package as a namespace under its lowercased package
// name, and its members resolve through that binding.
func TestStdlibImportBindsByPackageName(t *testing.T) {
	t.Parallel()

	res := inferAgainstStdlib(t, `
		import "std:math"
		val x = math.PI
	`, map[string]string{
		"std/math.esc": `export val PI: number = 3`,
	})

	require.Empty(t, errorMessagesOf(res.Errors))
	require.Equal(t, "number", soltype.Print(inferredValueType(t, res.Scope, "x")))
}

// A package whose sole class shares its name binds that class directly, under
// the class's own capitalization rather than the package's. This is the FR5
// single-class shortcut.
func TestStdlibImportBindsASingleClassDirectly(t *testing.T) {
	t.Parallel()

	res := inferAgainstStdlib(t, `
		import "std:array"
		val xs = Array(1)
		val n = xs.length
		val other = array.helper
	`, map[string]string{
		"std/array.esc": `
			export declare class Array {
				length: number,
			}
			export val helper: number = 1
		`,
	})

	require.Empty(t, errorMessagesOf(res.Errors))
	require.Equal(t, "Array", soltype.Print(inferredValueType(t, res.Scope, "xs")))
	require.Equal(t, "number", soltype.Print(inferredValueType(t, res.Scope, "n")))

	// The package's other exports stay reachable through the namespace. FR5 asks
	// for them on the class binding as well, which is #1466.
	require.Equal(t, "number", soltype.Print(inferredValueType(t, res.Scope, "other")))
}

// A package writing a name a file never imports is unbound. There is no ambient
// pseudo-package surface.
func TestStdlibNameIsUnboundWithoutAnImport(t *testing.T) {
	t.Parallel()

	res := inferAgainstStdlib(t, `val x = math.PI`, map[string]string{
		"std/math.esc": `export val PI: number = 3`,
	})

	require.Equal(t, []string{"Unknown identifier: math"}, errorMessagesOf(res.Errors))
}

// Every way a pseudo-package import can be malformed, one case each. A URI with
// several problems reports all of them, so the author fixes the import once
// rather than once per round.
func TestStdlibImportRejectsMalformedURIs(t *testing.T) {
	t.Parallel()

	files := map[string]string{"std/math.esc": `export val PI: number = 3`}

	tests := map[string]struct {
		src      string
		messages []string
	}{
		"AnUnknownScheme": {
			src:      `import "bogus:thing"`,
			messages: []string{`unknown import scheme "bogus"; recognized schemes: std, web, node`},
		},
		"NoPackageAfterTheScheme": {
			src:      `import "std:"`,
			messages: []string{`missing package name after "std" scheme`},
		},
		"AReservedScheme": {
			src:      `import "node:fs"`,
			messages: []string{`"node:fs": node:* is reserved; not yet populated`},
		},
		"AnUnknownFlag": {
			src:      `import "std:math?eager"`,
			messages: []string{`unknown import flag "eager"; recognized flags: local`},
		},
		"ARepeatedFlag": {
			src:      `import "std:math?local&local"`,
			messages: []string{`duplicate import flag "local"`},
		},
		"AnInvalidPackageName": {
			src: `import "std:Math"`,
			messages: []string{
				`cannot resolve import "std:Math": invalid package name "Math" in std:Math; ` +
					`expected lowercase letters, digits, and underscores`,
			},
		},
		"APackageNoFileDeclares": {
			src:      `import "std:nonexistent"`,
			messages: nil, // filled in below, since the message names a temp dir
		},
		// Several problems at once, reported together.
		"ANamedImportUnderAnUnknownScheme": {
			src: `import { thing } from "bogus:pkg"`,
			messages: []string{
				`unknown import scheme "bogus"; recognized schemes: std, web, node`,
				`named imports from pseudo-package "bogus:pkg" are not supported; ` +
					"use a bare-string import (`import \"bogus:pkg\"`) and access members through the namespace",
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if test.messages == nil {
				// The not-found message names the tree's root, so it is built
				// against the directory this case actually ran in.
				dir := seedStdlib(t, files)
				res := InferModuleWithSource(parseModule(t, test.src), StdlibSource(dir))
				require.Equal(t, []string{
					`cannot resolve import "std:nonexistent": unknown package "nonexistent" ` +
						`in std: scheme (no std/nonexistent.esc under ` + dir + `)`,
				}, errorMessagesOf(res.Errors))
				return
			}
			res := inferAgainstStdlib(t, test.src, files)
			require.Equal(t, test.messages, errorMessagesOf(res.Errors))
		})
	}
}

// The `?local` flag is the binding shape the bare form already uses, so writing
// it explicitly changes nothing.
func TestStdlibImportAcceptsTheLocalFlag(t *testing.T) {
	t.Parallel()

	res := inferAgainstStdlib(t, `
		import "std:math?local"
		val x = math.PI
	`, map[string]string{
		"std/math.esc": `export val PI: number = 3`,
	})

	require.Empty(t, errorMessagesOf(res.Errors))
	require.Equal(t, "number", soltype.Print(inferredValueType(t, res.Scope, "x")))
}

// One package reaches another only through an import. Sibling files in a scheme
// subtree do not see each other, since each is parsed as its own module under
// its basename alone.
func TestStdlibPackagesReachEachOtherOnlyByImport(t *testing.T) {
	t.Parallel()

	// `std:shapes` reads `four` from `std:units` through its own import, which is
	// what the value it exports is built from.
	res := inferAgainstStdlib(t, `
		import "std:shapes"
		val n = shapes.corners
	`, map[string]string{
		"std/units.esc": `export val four: number = 4`,
		"std/shapes.esc": `
			import "std:units"
			export val corners: number = units.four
		`,
	})

	require.Empty(t, errorMessagesOf(res.Errors))
	require.Equal(t, "number", soltype.Print(inferredValueType(t, res.Scope, "n")))

	_, loadedUnits := res.Packages.Lookup("std:units")
	require.True(t, loadedUnits, "the sibling package should be published too")
}

// A sibling file in the same subtree is invisible without an import. Each
// package parses under its basename alone, so nothing derives a shared
// namespace from where the tree sits on disk.
func TestStdlibSiblingPackageIsInvisibleWithoutAnImport(t *testing.T) {
	t.Parallel()

	res := inferAgainstStdlib(t, `
		import "std:shapes"
		val n = shapes.corners
	`, map[string]string{
		"std/units.esc":  `export val four: number = 4`,
		"std/shapes.esc": `export val corners: number = units.four`,
	})

	require.Equal(t, []string{
		"package \"std:shapes\" (" + res.Packages.Path("std:shapes") + ") has 1 error(s):\n" +
			"  Unknown identifier: units",
	}, errorMessagesOf(res.Errors))
}

// Which specifiers reach the pseudo-package resolver at all. Anything shaped
// `<lowercase word>:` does, so an unrecognized scheme gets a diagnostic naming
// the schemes rather than one about a missing package.json.
func TestIsSchemePrefixedImport(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		spec string
		want bool
	}{
		"ARecognizedScheme":   {spec: "std:math", want: true},
		"AnUnrecognizedOne":   {spec: "bogus:thing", want: true},
		"ABareSpecifier":      {spec: "lodash", want: false},
		"ARelativePath":       {spec: "./sibling", want: false},
		"AScopedNpmPackage":   {spec: "@types/node", want: false},
		"AnUppercaseScheme":   {spec: "STD:math", want: false},
		"ALeadingColon":       {spec: ":math", want: false},
		"AWindowsLookingPath": {spec: "C:/tmp/x", want: false},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, test.want, IsSchemePrefixedImport(test.spec))
		})
	}
}

// A repeated unknown flag reads as one unknown flag written twice, not as two
// unrelated unknowns.
func TestStdlibImportReportsARepeatedUnknownFlagOnce(t *testing.T) {
	t.Parallel()

	res := inferAgainstStdlib(t, `import "std:math?eager&eager"`, map[string]string{
		"std/math.esc": `export val PI: number = 3`,
	})

	require.Equal(t, []string{
		`unknown import flag "eager"; recognized flags: local`,
		`duplicate import flag "eager"`,
	}, errorMessagesOf(res.Errors))
}

// A pseudo-package parses under a source id of its own, so a diagnostic about
// one never points into the importing file.
func TestStdlibPackageParsesUnderItsOwnSourceID(t *testing.T) {
	t.Parallel()

	res := inferAgainstStdlib(t, `
		import "std:math"
		val x = math.PI
	`, map[string]string{
		"std/math.esc": `export val PI: number = 3`,
	})
	require.Empty(t, errorMessagesOf(res.Errors))

	ns, ok := res.Packages.Lookup("std:math")
	require.True(t, ok)
	binding, ok := ns.Values["PI"]
	require.True(t, ok)
	require.NotEmpty(t, binding.Sources)

	node, ok := binding.Sources[0].(*ast.NodeProvenance)
	require.True(t, ok, "a declaration's provenance should name its AST node")
	require.GreaterOrEqual(t, node.Node.Span().SourceID, stdlibSourceIDBase,
		"a package's declarations should carry ids above every entry-module one")
}

// Every pseudo-package diagnostic points at the import statement that raised
// it, so a reader is sent to the line they can act on.
func TestStdlibImportDiagnosticsPointAtTheImport(t *testing.T) {
	t.Parallel()

	files := map[string]string{"std/math.esc": `export val PI: number = 3`}
	for name, src := range map[string]string{
		"AnUnknownScheme":      `import "bogus:thing"`,
		"NoPackageName":        `import "std:"`,
		"AReservedScheme":      `import "node:fs"`,
		"AnUnknownFlag":        `import "std:math?eager"`,
		"ARepeatedFlag":        `import "std:math?local&local"`,
		"ANamedImport":         `import { PI } from "std:math"`,
		"AnInvalidPackageName": `import "std:Math"`,
		"AMissingPackage":      `import "std:nonexistent"`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			res := inferAgainstStdlib(t, src, files)
			require.NotEmpty(t, res.Errors)
			for _, e := range res.Errors {
				require.Equal(t, 0, e.Span().SourceID,
					"a diagnostic on an import carries the importing file's id")
				require.Less(t, e.Span().Start.Offset, e.Span().End.Offset)
				require.Empty(t, e.Related())
				require.NotEmpty(t, e.Message())
			}
		})
	}
}

// A `?` slot with nothing in it is its own diagnostic, distinct from a flag the
// resolver does not recognize.
func TestStdlibImportReportsAnEmptyFlag(t *testing.T) {
	t.Parallel()

	span := ast.Span{Start: ast.Location{Offset: 3}, End: ast.Location{Offset: 20}}
	errs := validateStdlibFlags([]string{""}, span)
	require.Len(t, errs, 1)
	require.Equal(t, "empty flag in import specifier", errs[0].Message())
	require.Equal(t, span, errs[0].Span())
	require.Empty(t, errs[0].Related())
}

// The string rules the URI checks rest on, at the edges the resolver's own
// paths do not reach.
func TestStdlibURIStringRules(t *testing.T) {
	t.Parallel()

	require.False(t, isASCIILower(""), "an empty scheme is not a scheme")
	require.False(t, isValidPackagePath(""), "an empty package name is not one")
	require.True(t, isValidPackagePath("typed_arrays"))
	require.False(t, isValidPackagePath("typed-arrays"), "a hyphen is not allowed here")

	// resolveStdlibPath is reachable on its own, so it re-checks the scheme
	// rather than trusting that validation ran first.
	_, err := resolveStdlibPath(t.TempDir(), "bogus:thing")
	require.EqualError(t, err, `unrecognized scheme in "bogus:thing"`)
	_, err = resolveStdlibPath(t.TempDir(), "nocolon")
	require.EqualError(t, err, `unrecognized scheme in "nocolon"`)
}

// A file the resolver finds but cannot parse fails the load with the parser's
// own complaint, rather than publishing a half-read package.
func TestStdlibSourceReportsAParseError(t *testing.T) {
	t.Parallel()

	dir := seedStdlib(t, map[string]string{"std/broken.esc": `export val = = =`})
	_, _, err := StdlibSource(dir)("std:broken")
	require.ErrorContains(t, err, "parse errors in ")
	require.ErrorContains(t, err, filepath.Join(dir, "std", "broken.esc"))
}

// A path that resolves to a directory rather than a file is not a package.
func TestStdlibSourceRejectsADirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "std", "shaped.esc"), 0o755))

	_, _, err := StdlibSource(dir)("std:shaped")
	require.ErrorContains(t, err, `unknown package "shaped" in std: scheme`)
}
