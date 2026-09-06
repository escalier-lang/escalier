package dts_to_esc

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dts_parser"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/btree"
)

// libInputsFrom parses each entry as one `lib.*.d.ts` file, keyed by the
// basename the collector reports it under.
func libInputsFrom(t *testing.T, files map[string]string) []LibInput {
	t.Helper()

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	inputs := make([]LibInput, 0, len(names))
	for i, name := range names {
		source := &ast.Source{Path: name, Contents: files[name], ID: i}
		mod, errs := dts_parser.NewDtsParser(source).ParseModule()
		require.Empty(t, errs, "parse errors in %s", name)
		inputs = append(inputs, LibInput{SourceFile: name, Module: mod})
	}
	return inputs
}

// The declaration shapes a runtime path can come from, one case per shape, and
// the shapes that contribute no path.
func TestCollectJSGlobalsReadsValueLevelPaths(t *testing.T) {
	t.Parallel()

	globals := CollectJSGlobals(libInputsFrom(t, map[string]string{
		"lib.probe.d.ts": `
			interface Math {
				sin(x: number): number;
				readonly PI: number;
				[Symbol.toStringTag]: string;
			}
			declare var Math: Math;
			declare function parseInt(s: string): number;
			declare class WeakRef<T> {
				static of(value: T): WeakRef<T>;
				deref(): T;
			}
			declare namespace Intl {
				function getCanonicalLocales(locales: string): string;
				class Segmenter {}
				type Key = string;
			}
			interface Thenable { then(): void; }
			type Alias = string;
		`,
	}))

	tests := map[string]struct {
		target string
		known  bool
	}{
		"AVarBinding":                {target: "Math", known: true},
		"AMethodOfABoundInterface":   {target: "Math.sin", known: true},
		"APropertyOfABoundInterface": {target: "Math.PI", known: true},
		"AFunction":                  {target: "parseInt", known: true},
		"AClass":                     {target: "WeakRef", known: true},
		"AStaticOfAClass":            {target: "WeakRef.of", known: true},
		"ANamespace":                 {target: "Intl", known: true},
		"AFunctionInANamespace":      {target: "Intl.getCanonicalLocales", known: true},
		"AClassInANamespace":         {target: "Intl.Segmenter", known: true},
		"TheAllowList":               {target: "Symbol.customMatcher", known: true},

		// An instance member is reached through a receiver, so it names no
		// path under the class binding.
		"AnInstanceMethod": {target: "WeakRef.deref", known: false},
		// A computed key has no plain-name form to spell in a decorator.
		"ASymbolKeyedMember": {target: "Math.toStringTag", known: false},
		// A type erases at codegen, so a decorator naming one would lower to
		// a ReferenceError.
		"AnUnboundInterface":  {target: "Thenable", known: false},
		"ATypeAlias":          {target: "Alias", known: false},
		"ATypeInANamespace":   {target: "Intl.Key", known: false},
		"ANameNobodyDeclares": {target: "Nonexistent", known: false},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, test.known, globals.Contains(test.target))
		})
	}
}

// A `.d.ts` interface body merges across files, so a member declared in a
// later lib file reaches the binding declared in an earlier one. This is what
// carries `Math.f16round`, declared in lib.esnext.float16.d.ts against the
// `declare var Math` of lib.es5.d.ts.
func TestCollectJSGlobalsMergesInterfaceBodiesAcrossFiles(t *testing.T) {
	t.Parallel()

	globals := CollectJSGlobals(libInputsFrom(t, map[string]string{
		"lib.es5.d.ts": `
			interface Math { sin(x: number): number; }
			declare var Math: Math;
		`,
		"lib.esnext.float16.d.ts": `
			interface Math { f16round(x: number): number; }
		`,
	}))

	require.True(t, globals.Contains("Math.sin"))
	require.True(t, globals.Contains("Math.f16round"))
}

// A module-shaped lib file declares its globals inside `declare global`, and
// they count the same as a script-shaped file's top-level ones. The pinned set
// has one such file, lib.esnext.iterator.d.ts.
func TestCollectJSGlobalsReadsDeclareGlobalBlocks(t *testing.T) {
	t.Parallel()

	globals := CollectJSGlobals(libInputsFrom(t, map[string]string{
		"lib.esnext.probe.d.ts": `
			export {};
			declare global {
				interface Wrapped { only(): void; }
				var Wrapped: Wrapped;
				function globalFn(): void;
			}
		`,
	}))

	require.True(t, globals.Contains("Wrapped"))
	require.True(t, globals.Contains("Wrapped.only"))
	require.True(t, globals.Contains("globalFn"))
}

// A binding reaches the members its interface inherits, not only the ones
// declared on the interface itself. `SymbolConstructor` and the typed-array
// constructors are both this shape in the pinned set.
func TestCollectJSGlobalsFollowsInterfaceExtends(t *testing.T) {
	t.Parallel()

	globals := CollectJSGlobals(libInputsFrom(t, map[string]string{
		"lib.probe.d.ts": `
			interface Base { inherited(): void; }
			interface Derived extends Base { own(): void; }
			declare var Thing: Derived;
		`,
	}))

	require.True(t, globals.Contains("Thing.own"))
	require.True(t, globals.Contains("Thing.inherited"))
}

// An `extends` cycle terminates rather than recurring forever. TypeScript
// rejects one, but the collector reads whatever the pin ships.
func TestCollectJSGlobalsTerminatesOnAnExtendsCycle(t *testing.T) {
	t.Parallel()

	globals := CollectJSGlobals(libInputsFrom(t, map[string]string{
		"lib.probe.d.ts": `
			interface A extends B { fromA(): void; }
			interface B extends A { fromB(): void; }
			declare var Thing: A;
		`,
	}))

	require.True(t, globals.Contains("Thing.fromA"))
	require.True(t, globals.Contains("Thing.fromB"))
}

// The findings a bad target produces, one case per shape of wrongness.
func TestValidateJSTargetsNamesWhatIsWrong(t *testing.T) {
	t.Parallel()

	globals := CollectJSGlobals(libInputsFrom(t, map[string]string{
		"lib.probe.d.ts": `
			interface Math { sin(x: number): number; }
			declare var Math: Math;
		`,
	}))

	tests := map[string]struct {
		target  string
		message string
	}{
		"AnUnknownMemberOfAKnownPrefix": {
			target:  "Math.f16round",
			message: "std:probe: `@js(\"Math.f16round\")` on \"f16round\" names no JS runtime global, and \"Math\" has no known runtime member \"f16round\"",
		},
		"AnUnknownPrefix": {
			target:  "Atomics.add",
			message: "std:probe: `@js(\"Atomics.add\")` on \"add\" names no JS runtime global, and \"Atomics\" is not a known top-level global",
		},
		"AnUnknownBareName": {
			target:  "WeakRef",
			message: "std:probe: `@js(\"WeakRef\")` on \"WeakRef\" names no JS runtime global",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			mods := map[string]*StandaloneModule{
				"std:probe": moduleWithJSTarget(test.target),
			}
			findings := ValidateJSTargets(mods, globals)
			require.Len(t, findings, 1)
			require.Equal(t, test.message, findings[0].String())
		})
	}
}

// A target the lib set declares produces no finding, so the check reports only
// what is actually wrong.
func TestValidateJSTargetsAcceptsAKnownTarget(t *testing.T) {
	t.Parallel()

	globals := CollectJSGlobals(libInputsFrom(t, map[string]string{
		"lib.probe.d.ts": `
			interface Math { sin(x: number): number; }
			declare var Math: Math;
		`,
	}))

	mods := map[string]*StandaloneModule{"std:probe": moduleWithJSTarget("Math.sin")}
	require.Empty(t, ValidateJSTargets(mods, globals))
}

// moduleWithJSTarget builds a one-declaration module whose sole function
// carries `@js(target)`. The declared name is the target's last segment, which
// is the shape the converter emits.
func moduleWithJSTarget(target string) *StandaloneModule {
	name := target
	for i := len(target) - 1; i >= 0; i-- {
		if target[i] == '.' {
			name = target[i+1:]
			break
		}
	}
	fn := ast.NewFuncDecl(
		ast.NewIdentifier(name, ast.Span{}),
		nil, nil, nil, nil, nil, nil,
		true, true, false,
		ast.Span{},
	)
	attachJSDecorator(fn, target)

	var namespaces btree.Map[string, *ast.Namespace]
	namespaces.Set("", &ast.Namespace{Decls: []ast.Decl{fn}})
	return &StandaloneModule{
		Module: ast.NewModule(namespaces),
		Paths:  map[ast.Decl]string{fn: target},
	}
}

// The §1402 gate. Every `@js` target the run writes names a runtime path the
// pinned `.d.ts` set declares.
//
// The two are read from the same input, so this holds by construction for a
// generated decorator. What it pins is the overlay, whose targets are written
// by hand, and the collector itself: a shape it stops reading takes real
// targets out of the set and fails here.
func TestGeneratedTreeJSTargetsAreAllKnown(t *testing.T) {
	t.Parallel()

	libDir := filepath.Join("..", "..", "node_modules", "typescript", "lib")
	if _, err := os.Stat(filepath.Join(libDir, "lib.es5.d.ts")); err != nil {
		t.Skipf("TypeScript lib files not present at %s; run `pnpm install`: %v", libDir, err)
	}
	receivers := NewReceiverFacts(committedFacts(t))

	res, err := Generate(GenerateOptions{
		LibDir:       libDir,
		OverlayDir:   filepath.Join("..", "interop", "overlay"),
		OutDir:       t.TempDir(),
		HandAuthored: HandAuthoredPackages,
		Facts:        receivers,
	})
	require.NoError(t, err)

	basenames, err := DiscoverLibFiles(libDir)
	require.NoError(t, err)
	inputs, err := ParseLibFiles(libDir, basenames)
	require.NoError(t, err)

	globals := CollectJSGlobals(inputs)
	require.Greater(t, globals.Len(), 1000, "the collector read almost nothing from the lib set")
	require.Empty(t, ValidateJSTargets(res.Modules, globals))
}
