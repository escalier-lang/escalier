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

// A package name holds lowercase letters, digits, and underscores, which is
// what `std:typed_arrays` and `std:base64` are made of. The hyphenated form is
// rejected, in the malformed-URI table below.
func TestStdlibImportAcceptsUnderscoresAndDigits(t *testing.T) {
	t.Parallel()

	res := inferAgainstStdlib(t, `
		import "std:typed_arrays"
		import "std:base64"
		val a = typed_arrays.BYTES_PER_ELEMENT
		val b = base64.padding
	`, map[string]string{
		"std/typed_arrays.esc": `export val BYTES_PER_ELEMENT: number = 4`,
		"std/base64.esc":       `export val padding: string = "="`,
	})

	require.Empty(t, errorMessagesOf(res.Errors))
	require.Equal(t, "number", soltype.Print(inferredValueType(t, res.Scope, "a")))
	require.Equal(t, "string", soltype.Print(inferredValueType(t, res.Scope, "b")))
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
		// An underscore separates words in a package name. Substituting a hyphen
		// for one belongs to the third-party workstream, so `std:typed_arrays`
		// resolves where `std:typed-arrays` does not.
		"AHyphenatedPackageName": {
			src: `import "std:typed-arrays"`,
			messages: []string{
				`cannot resolve import "std:typed-arrays": invalid package name "typed-arrays" ` +
					`in std:typed-arrays; expected lowercase letters, digits, and underscores`,
			},
		},
		// A `?` with nothing after it is its own diagnostic, distinct from a flag
		// the resolver does not recognize.
		"AnEmptyFlag": {
			src:      `import "std:math?"`,
			messages: []string{"empty flag in import specifier"},
		},
		"APackageNoFileDeclares": {
			src:      `import "std:nonexistent"`,
			messages: nil, // filled in below, since the message names a temp dir
		},
		// Several problems at once, reported together.
		"AnUnknownSchemeAndARepeatedFlag": {
			src: `import "bogus:pkg?local&local"`,
			messages: []string{
				`unknown import scheme "bogus"; recognized schemes: std, web, node`,
				`duplicate import flag "local"`,
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

// A file the resolver finds but cannot parse fails the load with the parser's
// own complaint, rather than publishing a half-read package.
func TestStdlibSourceReportsAParseError(t *testing.T) {
	t.Parallel()

	dir := seedStdlib(t, map[string]string{"std/broken.esc": `export val = = =`})
	_, _, err := StdlibSource(dir)("std:broken")
	require.EqualError(t, err,
		"parse errors in "+filepath.Join(dir, "std", "broken.esc")+": "+
			"11-12: Expected a pattern; 7-10: Expected pattern; "+
			"13-14: Unexpected token, '='; 15-16: Unexpected token, '='; "+
			"16-16: Expected an expression")
}

// A path that resolves to a directory rather than a file is not a package.
func TestStdlibSourceRejectsADirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "std", "shaped.esc"), 0o755))

	_, _, err := StdlibSource(dir)("std:shaped")
	require.EqualError(t, err,
		`unknown package "shaped" in std: scheme (no std/shaped.esc under `+dir+`)`)
}

// The single-class shortcut fires for a class and nothing else.
//
// A package exporting a function beside a same-named type has a value and a
// type under one name, the pair a class produces, but binding that function
// directly would shadow the namespace: a member access that finds a value never
// reaches a namespace of the same name, so every other export would be
// unreachable.
func TestStdlibShortcutFiresOnlyForAClass(t *testing.T) {
	t.Parallel()

	res := inferAgainstStdlib(t, `
		import "std:array"
		val n = array.helper
	`, map[string]string{
		"std/array.esc": `
			export type array = number
			export fn array(x: number) -> number { return x }
			export val helper: number = 1
		`,
	})

	require.Empty(t, errorMessagesOf(res.Errors))
	require.Equal(t, "number", soltype.Print(inferredValueType(t, res.Scope, "n")))

	// The package binds as a namespace, and no value shadows it.
	_, boundNamespace := res.FileScopes[0].GetNamespace("array")
	require.True(t, boundNamespace)
	_, boundValue := res.FileScopes[0].values["array"]
	require.False(t, boundValue, "the shortcut should not bind a non-class value")
}

// An `as` clause names the namespace a pseudo-package binds under, the same way
// it does for an npm specifier. This is what lets a package re-expose a
// namespace the converter flattened under the name its references were written
// against, as `Intl` is.
func TestStdlibImportBindsUnderItsAlias(t *testing.T) {
	t.Parallel()

	res := inferAgainstStdlib(t, `
		import "std:intl" as Intl
		val n: number = Intl.answer
	`, map[string]string{
		"std/intl.esc": `export val answer: number = 42`,
	})

	require.Empty(t, errorMessagesOf(res.Errors))
	fileScope, held := res.FileScopes[0]
	require.True(t, held)
	_, bound := fileScope.GetNamespace("Intl")
	require.True(t, bound, "the alias names the binding")
	_, derived := fileScope.GetNamespace("intl")
	require.False(t, derived, "the derived name is not bound alongside the alias")
}

// An alias renames the namespace binding and nothing else. A package whose sole
// class is named after it still binds that class under its own capitalization,
// since that binding is the class rather than the package.
func TestStdlibImportAliasLeavesTheSingleClassShortcut(t *testing.T) {
	t.Parallel()

	res := inferAgainstStdlib(t, `
		import "std:shapes" as S
		val a = Shapes(1)
		val b = S.Shapes(2)
	`, map[string]string{
		"std/shapes.esc": `
			export class Shapes {
				size: number,
			}
		`,
	})

	require.Empty(t, errorMessagesOf(res.Errors))
	require.Equal(t, "Shapes", soltype.Print(inferredValueType(t, res.Scope, "a")))
	require.Equal(t, "Shapes", soltype.Print(inferredValueType(t, res.Scope, "b")))
}
