package compiler

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/stretchr/testify/require"
)

// useChecker points the compiler at internal/checker for the rest of the test.
func useChecker(t *testing.T) {
	t.Helper()
	t.Setenv(CheckerEnvVar, CheckerLegacy)
}

// useSolver points the compiler at internal/solver for the rest of the test.
func useSolver(t *testing.T) {
	t.Helper()
	t.Setenv(CheckerEnvVar, CheckerSolver)
}

// messages renders each diagnostic's message. The two checkers word the same fault
// differently, so a test asserting one names the checker whose wording it expects.
func messages(diags []Diagnostic) []string {
	out := make([]string, len(diags))
	for i, d := range diags {
		out[i] = d.Message()
	}
	return out
}

// libSources wraps one lib/ file as the source list the entry points take.
func libSources(contents string) []*ast.Source {
	return []*ast.Source{{ID: 0, Path: "lib/index.esc", Contents: contents}}
}

// binSource wraps one bin/ file.
func binSource(contents string) *ast.Source {
	return &ast.Source{ID: 1, Path: "bin/index.esc", Contents: contents}
}

// TestSelectBackend checks which checker CheckerEnvVar names. The solver is the
// default, so only CheckerLegacy selects the old checker, and an unset variable and
// a value the seam does not recognize both run the solver.
func TestSelectBackend(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  backend
	}{
		{name: "Unset", value: "", want: solverBackend{}},
		{name: "Solver", value: CheckerSolver, want: solverBackend{}},
		{name: "Legacy", value: CheckerLegacy, want: checkerBackend{}},
		{name: "Unrecognized", value: "nonesuch", want: solverBackend{}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(CheckerEnvVar, test.value)
			require.IsType(t, test.want, selectBackend(PackageConfig{}))
		})
	}
}

// TestCheckLibReportsATypeError checks that a library's type error reaches the
// caller on either checker. The two word the fault differently, so each setting
// asserts its own checker's message.
func TestCheckLibReportsATypeError(t *testing.T) {
	const src = "export val x: number = \"hello\"\n"

	t.Run("Checker", func(t *testing.T) {
		useChecker(t)
		out := CheckLib(context.Background(), libSources(src), PackageConfig{})
		require.Empty(t, out.ParseErrors)
		require.Contains(t, messages(out.TypeErrors), `"hello" cannot be assigned to number`)
	})

	t.Run("Solver", func(t *testing.T) {
		useSolver(t)
		out := CheckLib(context.Background(), libSources(src), PackageConfig{})
		require.Empty(t, out.ParseErrors)
		require.Contains(t, messages(out.TypeErrors), `cannot constrain "hello" <: number`)
	})
}

// TestCheckBinScriptReadsTheLibrary checks the lib/ to bin/ seam on either checker:
// a script resolves a name the library declared, and reports one neither declared.
// Both settings run the same two scripts, since what is asserted is which names
// resolve rather than how a fault is worded.
func TestCheckBinScriptReadsTheLibrary(t *testing.T) {
	const lib = "export val greeting = \"hello\"\n"

	tests := []struct {
		name  string
		setup func(*testing.T)
	}{
		{name: "Checker", setup: useChecker},
		{name: "Solver", setup: useSolver},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.setup(t)
			ctx := context.Background()
			libOutput := CheckLib(ctx, libSources(lib), PackageConfig{})
			require.NotNil(t, libOutput.LibScope)

			resolved := CheckBinScript(ctx, libOutput.LibScope, binSource("val g = greeting\n"), PackageConfig{})
			require.Empty(t, resolved.ParseErrors)
			require.NotContains(t, messages(resolved.TypeErrors), "Unknown identifier: greeting")

			unresolved := CheckBinScript(ctx, libOutput.LibScope, binSource("val f = farewell\n"), PackageConfig{})
			require.Empty(t, unresolved.ParseErrors)
			require.Contains(t, messages(unresolved.TypeErrors), "Unknown identifier: farewell")
		})
	}
}

// TestCompileScriptImportsUsedLibSymbols checks that the emitted script imports the
// library names it used and no others, on either checker. This is what the LibScope
// interface's declaresTopLevel answers, and each checker answers it off its own
// representation of the library's surface.
func TestCompileScriptImportsUsedLibSymbols(t *testing.T) {
	const lib = "export val greeting = \"hello\"\nexport val unused = 0\n"

	tests := []struct {
		name  string
		setup func(*testing.T)
	}{
		{name: "Checker", setup: useChecker},
		{name: "Solver", setup: useSolver},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.setup(t)
			ctx := context.Background()

			libOutput := CheckLib(ctx, libSources(lib), PackageConfig{})
			require.NotNil(t, libOutput.LibScope)

			out := CompileScript(libOutput.LibScope, binSource("val g = greeting\n"))
			require.Empty(t, out.ParseErrors)
			require.Equal(t,
				"import { greeting } from \"../lib/index.js\";\nconst g = greeting;\n"+
					"//# sourceMappingURL=./index.js.map\n",
				out.CompUnits["bin/index"].JS)
		})
	}
}

// TestCompileAScriptWithNoLibrary checks the entry point a package with no lib/
// files takes: the script resolves what the prelude declares and nothing else, on
// either checker.
func TestCompileAScriptWithNoLibrary(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T)
	}{
		{name: "Checker", setup: useChecker},
		{name: "Solver", setup: useSolver},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.setup(t)
			out := Compile(&ast.Source{ID: 0, Path: "bin/index.esc", Contents: "val g = \"hello\"\n"})
			require.Empty(t, out.ParseErrors)
			require.NotContains(t, messages(out.TypeErrors), "Unknown identifier: g")
			require.Contains(t, out.CompUnits["index"].JS, `const g = "hello";`)
		})
	}
}

// TestCompileScriptImportsALibraryNamespace checks that a `namespace` block the
// library declares is imported by a script that reads through it, on either
// checker. A namespace is the second sort declaresTopLevel answers for, and each
// checker reads it off its own representation of the library.
func TestCompileScriptImportsALibraryNamespace(t *testing.T) {
	const lib = "namespace Geometry {\n\texport val origin = 0\n}\n"

	tests := []struct {
		name  string
		setup func(*testing.T)
	}{
		{name: "Checker", setup: useChecker},
		{name: "Solver", setup: useSolver},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.setup(t)
			ctx := context.Background()

			libOutput := CheckLib(ctx, libSources(lib), PackageConfig{})
			require.NotNil(t, libOutput.LibScope)

			out := CompileScript(libOutput.LibScope, binSource("val o = Geometry.origin\n"))
			require.Empty(t, out.ParseErrors)
			require.Contains(t, out.CompUnits["bin/index"].JS,
				"import { Geometry } from \"../lib/index.js\";")
		})
	}
}

// TestCheckLibWithoutAStandardLibrary checks what the solver path reports when the
// standard library tree cannot be found. The run reports the prelude classes as
// missing on top of this, so the diagnostic asserted here is the one naming the cause.
func TestCheckLibWithoutAStandardLibrary(t *testing.T) {
	useSolver(t)
	empty := t.TempDir()
	t.Setenv("ESCALIER_STDLIB_DIR", empty)

	out := CheckLib(context.Background(), libSources("export val greeting = \"hello\"\n"), PackageConfig{})

	require.Empty(t, out.ParseErrors)
	want := "cannot find the standard library: ESCALIER_STDLIB_DIR=" + strconv.Quote(empty) +
		" does not contain a std/ subdirectory"
	require.Contains(t, messages(out.TypeErrors), want)

	// It blames the module's first file. The fault is in the run's configuration
	// rather than in anything written, and that file is what a caller has to place
	// a diagnostic against.
	for _, err := range out.TypeErrors {
		if err.Message() == want {
			require.Equal(t, 0, err.Span().SourceID)
		}
	}
}

// TestCompileWithoutAStandardLibrary is TestCheckLibWithoutAStandardLibrary for a
// script with no library. The script path names the file the same way the module path
// does, so a caller places both diagnostics the same way.
func TestCompileWithoutAStandardLibrary(t *testing.T) {
	useSolver(t)
	empty := t.TempDir()
	t.Setenv("ESCALIER_STDLIB_DIR", empty)

	out := Compile(binSource("val greeting = \"hello\"\n"))

	require.Empty(t, out.ParseErrors)
	want := "cannot find the standard library: ESCALIER_STDLIB_DIR=" + strconv.Quote(empty) +
		" does not contain a std/ subdirectory"
	require.Contains(t, messages(out.TypeErrors), want)

	// It blames the script's file and nothing written in it. binSource carries id 1,
	// so the id asserted here is the file's rather than the zero value.
	for _, err := range out.TypeErrors {
		if err.Message() == want {
			require.Equal(t, ast.Span{SourceID: 1}, err.Span())
		}
	}
}

// fixtureLibSources reads a fixture's lib/ files as the source list the entry points
// take. It walks subdirectories, because a fixture puts a namespace's declarations in a
// directory of that name, and sorts by path so a multi-file fixture is assembled the
// same way on every run. It returns none for a fixture with no lib/ directory, which is
// a bin/-only one.
func fixtureLibSources(t *testing.T, fixtureDir string) []*ast.Source {
	t.Helper()
	libDir := filepath.Join(fixtureDir, "lib")
	if _, err := os.Stat(libDir); os.IsNotExist(err) {
		return nil
	}

	var paths []string
	err := filepath.WalkDir(libDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".esc") {
			paths = append(paths, path)
		}
		return nil
	})
	require.NoError(t, err)
	sort.Strings(paths)

	sources := make([]*ast.Source, 0, len(paths))
	for i, path := range paths {
		contents, err := os.ReadFile(path)
		require.NoError(t, err)
		rel, err := filepath.Rel(fixtureDir, path)
		require.NoError(t, err)
		sources = append(sources, &ast.Source{
			ID: i,
			// The path the CLI would pass, since a declaration's namespace comes from
			// the directory holding its file.
			Path:     rel,
			Contents: string(contents),
		})
	}
	return sources
}

// TestSolverEmitsDefinitionsForEveryFixture is the done-condition of #1675: the solver
// path renders a `.d.ts` for every fixture whose lib/ module the checker path renders
// one for. The committed golden says which those are, so a fixture whose source does
// not parse and whose golden is therefore empty is not held to it.
//
// It asserts only that something is written. Whether it matches what the checker path
// writes for the same source is #1676's job, and today much of it does not.
func TestSolverEmitsDefinitionsForEveryFixture(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join("..", "..", "fixtures"))
	require.NoError(t, err)

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		t.Run(entry.Name(), func(t *testing.T) {
			fixtureDir := filepath.Join("..", "..", "fixtures", entry.Name())
			sources := fixtureLibSources(t, fixtureDir)
			if len(sources) == 0 {
				t.Skip("no lib/ module to emit definitions for")
			}
			golden, err := os.ReadFile(filepath.Join(fixtureDir, "build", "lib", "index.d.ts"))
			if err != nil {
				require.True(t, os.IsNotExist(err), "reading the golden: %v", err)
			}
			if len(golden) == 0 {
				t.Skip("the checker path emits no definitions for this fixture either")
			}

			useSolver(t)
			out := CompilePackage(sources, PackageConfig{})
			require.NotEmpty(t, out.CompUnits["lib/index"].DTS)
		})
	}
}

// TestBothCheckersEmitTheSameJS asserts that a source exercising a type the emitter
// reads emits identical JavaScript whichever checker ran.
func TestBothCheckersEmitTheSameJS(t *testing.T) {
	tests := map[string]string{
		"NullableIfValGuardsItsTarget": `
			declare val maybe: number | undefined
			export val got = if val n = maybe { n } else { 0 }
		`,
		"NonNullableIfValDoesNotGuard": `
			declare val always: number
			export val got = if val n = always { n } else { 0 }
		`,
		"OptionalChainForms": `
			declare val o: {e?: {f: string, m: fn () -> number}, g?: fn () -> number}
			export val member = o.e?.f
			export val index = o.e?["f"]
			export val call = o.g?()
			export val method = o.e?.m()
		`,
		"MethodReferenceKeepsItsReceiver": `
			declare val obj: {m: fn () -> number}
			export val m = obj.m
		`,
		"ConstructorCallTakesNew": `
			class Point { x: number, y: number }
			export val p = Point(1, 2)
		`,
		"AClassDerivesItsConstructor": `
			class Counter { count: number }
			export val c = Counter(0)
			export val n = c.count
		`,
		"APatternOnAClassTestsInstanceOf": `
			class Point { x: number }
			declare val v: unknown
			export val hit = match v { p: Point => 1, _ => 0 }
		`,
		"AFunctionFieldReadOffSelfIsNotBound": `
			class Fix {
			    f: fn (a: number) -> number,
			    recurse(&self, arg: number) -> number {
			        val g = self.f
			        return g(arg)
			    },
			}
		`,
		"AMethodReadOffSelfKeepsItsReceiver": `
			class Counter {
			    count: number,
			    bump(&self) -> number { return self.count },
			    handle(&self) -> fn () -> number { return self.bump },
			}
		`,
	}

	for name, source := range tests {
		t.Run(name, func(t *testing.T) {
			sources := libSources(source)

			useChecker(t)
			want := CompilePackage(sources, PackageConfig{}).CompUnits["lib/index"].JS
			useSolver(t)
			got := CompilePackage(sources, PackageConfig{}).CompUnits["lib/index"].JS

			require.NotEmpty(t, want, "the checker emits something to compare against")
			require.Equal(t, want, got, "the two checkers emit different JavaScript")
		})
	}
}

// TestAPatternOnAnAliasEmitsNoInstanceOfGuard asserts that neither checker emits an
// `instanceof` guard for a pattern annotated with an alias of a class.
//
// The guard tests the name the annotation wrote. An alias declares no runtime binding,
// so `value instanceof Alias` throws a ReferenceError. An alias is therefore not
// nominal for emission even though the class it stands for is.
//
// The absence of the guard is asserted directly rather than left to the output
// comparison above, because a comparison passes whether both emit the guard or neither
// does, and emitting it is the fault.
func TestAPatternOnAnAliasEmitsNoInstanceOfGuard(t *testing.T) {
	sources := libSources(`
		class Point { x: number }
		type Alias = Point
		declare val v: unknown
		export val hit = match v { p: Alias => 1, _ => 0 }
	`)

	for name, use := range map[string]func(*testing.T){"checker": useChecker, "solver": useSolver} {
		t.Run(name, func(t *testing.T) {
			use(t)
			js := CompilePackage(sources, PackageConfig{}).CompUnits["lib/index"].JS
			require.NotEmpty(t, js)
			require.NotContains(t, js, "instanceof Alias",
				"an alias has no runtime binding, so the guard would throw a ReferenceError")
		})
	}
}

// TestBothCheckersGuardAnOptionalChainTarget asserts that an `if val` over an optional
// chain guards its target against `undefined` whichever checker ran. The chain `o.e?.f`
// yields `undefined` when `o.e` is absent, and the guard reads that from the chain's type.
//
// The guard is asserted directly rather than left to the output comparison, because a
// comparison passes whether both emit the guard or neither does, and omitting it is the
// fault.
func TestBothCheckersGuardAnOptionalChainTarget(t *testing.T) {
	sources := libSources(`
		declare val o: {e?: {f: string}}
		export val got = if val s = o.e?.f { s } else { "" }
	`)

	for name, use := range map[string]func(*testing.T){"checker": useChecker, "solver": useSolver} {
		t.Run(name, func(t *testing.T) {
			use(t)
			js := CompilePackage(sources, PackageConfig{}).CompUnits["lib/index"].JS
			require.Contains(t, js, "o.e?.f != undefined")
		})
	}
}

// TestBothCheckersEmitTheSameDefinitionsForAnExtractor asserts that a class usable in a
// pattern declares the same static side in the emitted `.d.ts` whichever checker ran.
//
// A class is usable in a pattern by declaring `[Symbol.customMatcher]`, which says what
// a match against it binds. soltype stores a member keyed off a well-known symbol under
// a reserved spelling and takes the symbols it accepts from a closed set, so the member
// reaches the type only because `customMatcher` is in that set.
func TestBothCheckersEmitTheSameDefinitionsForAnExtractor(t *testing.T) {
	sources := libSources(`
		class C {
		    msg: string,
		    static [Symbol.customMatcher](subject: C) -> [string] {
		        return [subject.msg]
		    }
		}
	`)

	useChecker(t)
	want := CompilePackage(sources, PackageConfig{}).CompUnits["lib/index"].DTS
	useSolver(t)
	got := CompilePackage(sources, PackageConfig{}).CompUnits["lib/index"].DTS

	require.Contains(t, want, "[Symbol.customMatcher](subject: C): [string]",
		"the checker declares the matcher, so there is something to match against")
	require.Equal(t, want, got, "the two checkers emit different definitions")
}

// TestTheSolverEmitsAClassTypeParameterBound asserts that a class's declared bound reaches
// the emitted `.d.ts` as an `extends` clause.
//
// The clause is emitted from the parameter variable's upper-bound list, and that list
// grows as constraints flow in, so a bounded parameter a method also reads can end up
// carrying two. Emission writes a clause only for a lone bound, so the declared one has to
// be the only one the display carries.
//
// The clause is written on the constructor signature under the name the source gave the
// parameter, since TypeScript has no generic `const` to hang a class's parameters on.
//
// internal/checker emits `<T extends unknown>` for both sources, which says nothing, so
// this asserts what the solver emits rather than that the two agree.
func TestTheSolverEmitsAClassTypeParameterBound(t *testing.T) {
	tests := map[string]string{
		"AParameterOnlyTheConstructorReads": `
			class Holder<T: {value: number}> { peer: T }
		`,
		"AParameterAMethodAlsoReads": `
			class Holder<T: {value: number}> {
			    peer: T,
			    get(&self) -> T { return self.peer },
			}
		`,
	}

	for name, src := range tests {
		t.Run(name, func(t *testing.T) {
			useSolver(t)
			dts := CompilePackage(libSources(src), PackageConfig{}).CompUnits["lib/index"].DTS
			require.Contains(t, dts,
				"declare const Holder: {new <T extends {value: number}>(peer: T): Holder<T>};")
		})
	}
}

// TestBothCheckersBindAClassTypeParamOnItsConstructor covers the type parameters a
// generic class's emitted constructor signature writes.
//
// TypeScript has no generic `const`, so a class's parameters cannot be written beside the
// value and each signature that uses one binds it. The declared name, bound, and default
// reach that binder. A parameter no constructor argument mentions is bound too, since a
// signature reading `new (log: string): Consumer<never>` constructs a `Consumer` that
// accepts nothing.
func TestBothCheckersBindAClassTypeParamOnItsConstructor(t *testing.T) {
	tests := map[string]struct {
		src  string
		want string
	}{
		"AParameterTheConstructorReads": {
			src:  `class Box<T> { value: T }`,
			want: "declare const Box: {new <T>(value: T): Box<T>};",
		},
		// Nothing in the constructor's arguments mentions `T`, so only the return type
		// has it. The binder still has to be written for `new Consumer("x")` to infer
		// anything but `Consumer<never>`.
		"AParameterNoArgumentMentions": {
			src:  `class Consumer<T> { log: string }`,
			want: "declare const Consumer: {new <T>(log: string): Consumer<T>};",
		},
		// The binders are positional, so `new Pair<string, number>` has to mean what
		// `class Pair<A, B>` wrote even though the constructor takes `b` first.
		"ParametersTheConstructorTakesOutOfOrder": {
			src: `
				class Pair<A, B> {
				    b: B,
				    a: A,
				    constructor(&mut self, b: B, a: A) {
				        self.b = b
				        self.a = a
				    },
				}
			`,
			want: "declare const Pair: {new <A, B>(b: B, a: A): Pair<A, B>};",
		},
		"AParameterWithADefault": {
			src: `
				declare class Task<T, E = never> {
				    run(&self) -> T,
				    fail(&self, r: E) -> never,
				}
			`,
			want: "declare const Task: {new <T, E = never>(): Task<T, E>};",
		},
		// A bound naming the parameter it constrains has to reach the same binder the
		// signature writes, not the variable the declaration minted before the display
		// copied it.
		"AParameterItsOwnBoundNames": {
			src:  `class Node<T: {next: T}> { v: T }`,
			want: "declare const Node: {new <T extends {next: T}>(v: T): Node<T>};",
		},
		"AParameterASiblingsBoundNames": {
			src:  `class P<A: {x: number}, B: A> { a: A, b: B }`,
			want: "declare const P: {new <A extends {x: number}, B extends A>(a: A, b: B): P<A, B>};",
		},
		// A class with no parameters writes no binder, which is the control.
		"AClassWithNoParameters": {
			src:  `class Point { x: number, y: number }`,
			want: "declare const Point: {new (x: number, y: number): Point};",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			useChecker(t)
			want := CompilePackage(libSources(test.src), PackageConfig{}).CompUnits["lib/index"].DTS
			useSolver(t)
			got := CompilePackage(libSources(test.src), PackageConfig{}).CompUnits["lib/index"].DTS

			require.Contains(t, want, test.want)
			require.Equal(t, want, got, "the two checkers emit different definitions")
		})
	}
}

// TestTheSolverBindsAClassTypeParamOnItsConstructor asserts that `Holder<T>` emits its
// parameter as the constructor signature's own binder, beside a static member that stands
// outside it.
//
// TypeScript has no generic `const`, so the only place the value's type can bind `T` is
// the construct signature, and a static member cannot name it there. The solver's class
// value carries the parameters on its constructor, so the emitted signature follows.
func TestTheSolverBindsAClassTypeParamOnItsConstructor(t *testing.T) {
	useSolver(t)
	dts := CompilePackage(libSources(`
		class Holder<T: {value: number}> {
		    peer: T,
		    static s(x: number) -> number { return x },
		}
	`), PackageConfig{}).CompUnits["lib/index"].DTS

	require.Contains(t, dts,
		"declare const Holder: {new <T extends {value: number}>(peer: T): Holder<T>, s(x: number): number};")
}

// TestAGenericClassStaticSideIsSubsumed asserts that `Picker.pick`, whose body returns `n`
// or `1`, emits `number` rather than `number | 1`.
//
// A class's static side is read through a display of its own, the one that holds every
// declared parameter. Subsuming a union needs the ambient context, so it runs where that
// display is built. Both displays are canonicalized the same way as a result.
func TestAGenericClassStaticSideIsSubsumed(t *testing.T) {
	src := `
		class Picker<T> {
		    v: T,
		    static pick(flag: boolean, n: number) {
		        if flag { return n } else { return 1 }
		    },
		}
	`

	useChecker(t)
	want := CompilePackage(libSources(src), PackageConfig{}).CompUnits["lib/index"].DTS
	useSolver(t)
	got := CompilePackage(libSources(src), PackageConfig{}).CompUnits["lib/index"].DTS

	require.Contains(t, want, "pick(flag: boolean, n: number): number}")
	require.Equal(t, want, got, "the two checkers emit different definitions")
}

// TestBothCheckersSynthesizeAComputedKeyConstructor asserts that a class whose only
// field is keyed off `Symbol.iterator` emits the same constructor on both checkers.
//
// A variable key such as `[k]` is left out. soltype takes a computed key from a closed
// set of well-known symbols, so internal/solver reports `Unsupported: ComputedKey` for
// one and drops the field, which makes its `.d.ts` disagree even where the emitted
// JavaScript matches. Comparing a shape one checker rejects says nothing, and
// TestEmitsTheImplicitConstructor in internal/codegen covers what emission derives for
// a variable key without running either checker.
func TestBothCheckersSynthesizeAComputedKeyConstructor(t *testing.T) {
	src := "class Odd {\n\t[Symbol.iterator]: number,\n}"

	useChecker(t)
	want := CompilePackage(libSources(src), PackageConfig{}).CompUnits["lib/index"].JS
	useSolver(t)
	got := CompilePackage(libSources(src), PackageConfig{}).CompUnits["lib/index"].JS

	require.Contains(t, want, "this[Symbol.iterator] = _field1;")
	require.Equal(t, want, got, "the two checkers emit different JavaScript")
}

// TestNeitherCheckerSynthesizesAKeyFromACall asserts that a class whose field key comes
// from a call emits no constructor on either checker.
//
// `makeKey()` answers with a fresh symbol per call, so a constructor reading it per
// construction would assign a property no reader can name. internal/checker reports that
// the class needs an explicit constructor; internal/solver emits the same class without
// one, which is #1715 rather than anything about this rule.
func TestNeitherCheckerSynthesizesAKeyFromACall(t *testing.T) {
	src := "declare fn makeKey() -> unique symbol\nclass Odd {\n\t[makeKey()]: number,\n}"

	useChecker(t)
	want := CompilePackage(libSources(src), PackageConfig{}).CompUnits["lib/index"].JS
	useSolver(t)
	got := CompilePackage(libSources(src), PackageConfig{}).CompUnits["lib/index"].JS

	require.NotContains(t, want, "constructor")
	require.Equal(t, want, got, "the two checkers emit different JavaScript")
}

// TestTheSolverEmitsASymbolAPropertyDeclares covers the `.d.ts` the solver emits for a
// unique symbol a `readonly` property declares. The value the symbol is named after holds
// it at the path TypeScript writes, so a type naming the symbol reads `typeof i.key` and a
// key off it reads `[i.key]`. The property that declares the symbol keeps `unique symbol`.
//
// internal/checker writes each such key as a string, so this asserts what the solver emits
// rather than that the two agree.
func TestTheSolverEmitsASymbolAPropertyDeclares(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "Interface",
			src: `declare interface I { readonly key: unique symbol }
declare val i: I
export val k = i.key
export val o = {[i.key]: 1}`,
			want: `declare interface I {
  readonly key: unique symbol;
}
declare const i: I;
export declare const k: typeof i.key;
export declare const o: {[i.key]: 1};
`,
		},
		{
			name: "InterfaceThatExtends",
			src: `declare interface Base { x: number }
declare interface I extends Base { readonly key: unique symbol }
declare val i: I
export val o = {[i.key]: 1}`,
			want: `declare interface Base {
  x: number;
}
declare interface I extends Base {
  readonly key: unique symbol;
}
declare const i: I;
export declare const o: {[i.key]: 1};
`,
		},
		{
			name: "ObjectTypeAlias",
			src: `type T = {readonly tag: unique symbol}
declare val t: T
export val o = {[t.tag]: "x"}`,
			want: `declare type T = {readonly tag: unique symbol};
declare const t: T;
export declare const o: {[t.tag]: "x"};
`,
		},
		{
			name: "InlineObjectType",
			src: `declare val inline: {readonly key: unique symbol}
export val o = {[inline.key]: true}`,
			want: `declare const inline: {readonly key: unique symbol};
export declare const o: {[inline.key]: true};
`,
		},
		{
			name: "StaticReadonlyField",
			src: `declare class C { static readonly key: unique symbol }
export val k = C.key
export val o = {[C.key]: 1}`,
			want: `declare type C = {};
declare const C: {new (): C, readonly key: unique symbol};
export declare const k: typeof C.key;
export declare const o: {[C.key]: 1};
`,
		},
		{
			// The interface's symbol is named after the class field that holds a value of it.
			name: "InterfaceNamedByAStaticReadonlyField",
			src: `declare interface I { readonly key: unique symbol }
declare class C { static readonly i: I }
export val o = {[C.i.key]: 1}`,
			want: `declare interface I {
  readonly key: unique symbol;
}
declare type C = {};
declare const C: {new (): C, readonly i: I};
export declare const o: {[C.i.key]: 1};
`,
		},
		{
			// A `var` may be reassigned, so the solver gives the symbol no name. The first
			// top-level `val` holding it names it instead.
			name: "SymbolHeldByAVal",
			src: `declare interface I { readonly key: unique symbol }
declare var v: I
export val k = v.key
export val o = {[k]: 1}`,
			want: `declare interface I {
  readonly key: typeof k;
}
declare let v: I;
export declare const k: unique symbol;
export declare const o: {[k]: 1};
`,
		},
		{
			name: "NestedReadonlyProperty",
			src: `declare val n: {readonly a: {readonly b: unique symbol}}
export val k = n.a.b`,
			want: `declare const n: {readonly a: {readonly b: unique symbol}};
export declare const k: typeof n.a.b;
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			useSolver(t)
			dts := CompilePackage(libSources(tt.src), PackageConfig{}).CompUnits["lib/index"].DTS
			require.Equal(t, tt.want, dts)
		})
	}
}

// TestTheSolverEmitsARootSymbolInsideANamespace asserts that a namespace block names a
// unique symbol the module root declares by the root value that holds it, both as
// `typeof sym` and as a `[sym]` key. Writing `unique symbol` there would declare a new
// symbol. When the block declares its own member called `sym`, the root `sym` is out of
// reach, so the block names the symbol through a private alias of the same type.
func TestTheSolverEmitsARootSymbolInsideANamespace(t *testing.T) {
	useSolver(t)
	sources := []*ast.Source{
		{ID: 0, Path: "lib/index.esc", Contents: "export declare val sym: unique symbol\nexport val r = sym\n"},
		{ID: 1, Path: "lib/plain/a.esc", Contents: "export val s2 = sym\nexport val o = {[sym]: 1}\n"},
		{ID: 2, Path: "lib/shadow/b.esc", Contents: "export val sym = 5\nexport val s3 = r\nexport val o2 = {[r]: 1}\n"},
	}
	dts := CompilePackage(sources, PackageConfig{}).CompUnits["lib/index"].DTS
	require.Equal(t, `declare const __sym_symbol: typeof sym;
export declare const sym: unique symbol;
export declare const r: typeof sym;
declare namespace plain {
  export const o: {[sym]: 1};
  export const s2: typeof sym;
}
declare namespace shadow {
  export const o2: {[__sym_symbol]: 1};
  export const s3: typeof __sym_symbol;
  export const sym: 5;
}
`, dts)
}

// TestTheSolverEmitsASymbolANamespacedTypeDeclares asserts that an interface in a namespace
// block keeps `unique symbol` on the property that declares its symbol when the value the
// symbol is named after sits at the module root.
func TestTheSolverEmitsASymbolANamespacedTypeDeclares(t *testing.T) {
	useSolver(t)
	sources := []*ast.Source{
		{ID: 0, Path: "lib/index.esc", Contents: "export declare val i: ns.I\nexport val o = {[i.key]: 1}\n"},
		{ID: 1, Path: "lib/ns/a.esc", Contents: "export declare interface I { readonly key: unique symbol }\n"},
	}
	dts := CompilePackage(sources, PackageConfig{}).CompUnits["lib/index"].DTS
	require.Equal(t, `export declare const i: ns.I;
export declare const o: {[i.key]: 1};
declare namespace ns {
  export interface I {
    readonly key: unique symbol;
  }
}
`, dts)
}

// TestTheSolverEmitsABlockSymbolApartFromARootOne asserts that a symbol a `do` block
// declares stays apart from a root value of the same name. Each is its own symbol, so a key
// off the block's symbol is written through the value that holds it.
func TestTheSolverEmitsABlockSymbolApartFromARootOne(t *testing.T) {
	useSolver(t)
	src := "export declare val sym: unique symbol\nexport val x = do {\n\tdeclare val sym: unique symbol\n\tsym\n}\nexport val o = {[x]: 1}\n"
	dts := CompilePackage(libSources(src), PackageConfig{}).CompUnits["lib/index"].DTS
	require.Equal(t, `export declare const x: unique symbol;
export declare const o: {[x]: 1};
export declare const sym: unique symbol;
`, dts)
}

// TestTheSolverEmitsARegexCaptureAsString covers the `.d.ts` of a conditional whose Extends
// operand is a capturing regex type. TypeScript has no regex type, so the pattern emits as
// `string` and each captured name emits as the string type it captures, joined with
// `undefined` for a group a match can leave out.
func TestTheSolverEmitsARegexCaptureAsString(t *testing.T) {
	useSolver(t)
	src := "export type Strip<T> = if T : /^_*(?<Ident>\\w+)_*$/ { Ident } else { T }\n" +
		"export type Opt<T> = if T : /^a(?<B>b)?$/ { [B] } else { never }"
	got := CompilePackage(libSources(src), PackageConfig{}).CompUnits["lib/index"].DTS
	require.Equal(t, "export declare type Opt<T> = T extends string ? [string | undefined] : never;\n"+
		"export declare type Strip<T> = T extends string ? string : T;\n", got)
}

// TestBothCheckersBindOnlyAMethodRead covers which member and index reads emit `.bind`. A
// read of a method of an instance, an object type or an interface keeps its receiver when it
// travels as a value, so `obj.m` emits `obj.m.bind(obj)` and `obj["m"]` emits
// `obj["m"].bind(obj)`. A field or property holding a plain function reads no receiver, and
// a static method reads none either, so those emit the bare read. A read called on the spot
// emits no `.bind`. The two checkers agree on every case.
func TestBothCheckersBindOnlyAMethodRead(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "AFieldHoldingAFunction",
			src:  "class Fix {\n\tf: fn(a: number) -> number,\n\trun(&self) -> number {\n\t\tval g = self.f\n\t\treturn g(1)\n\t},\n}",
			want: "const g = this.f;",
		},
		{
			name: "AMethodThroughSelf",
			src:  "class C {\n\tm(&self) -> number { return 1 },\n\trun(&self) -> number {\n\t\tval g = self.m\n\t\treturn g()\n\t},\n}",
			want: "const g = this.m.bind(this);",
		},
		{
			name: "AMethodOfAnInstance",
			src:  "class C {\n\tm(&self) -> number { return 1 },\n}\nval c = C()\nexport val g = c.m",
			want: "export const g = c.m.bind(c);",
		},
		{
			name: "AnOverloadedMethod",
			src:  "class C {\n\tm(&self, x: number) -> number { return x },\n\tm(&self, x: string) -> string { return x },\n}\nval c = C()\nexport val g = c.m",
			want: "export const g = c.m.bind(c);",
		},
		{
			name: "AMethodOfAnObjectType",
			src:  "declare val obj: {m(&self) -> number}\nexport val g = obj.m",
			want: "export const g = obj.m.bind(obj);",
		},
		{
			name: "AMethodOfAnInterface",
			src:  "declare interface I { m(&self) -> number }\ndeclare val i: I\nexport val g = i.m",
			want: "export const g = i.m.bind(i);",
		},
		{
			// A method the TypeScript lib declares names no `self`, and still runs against
			// the value it was read from.
			name: "AMethodTheLibDeclares",
			src:  "val xs: mut Array<number> = [1]\nexport val push = xs.push",
			want: "export const push = xs.push.bind(xs);",
		},
		{
			name: "APropertyHoldingAFunction",
			src:  "declare val obj: {m: fn () -> number}\nexport val m = obj.m",
			want: "export const m = obj.m;",
		},
		{
			name: "AStaticMethod",
			src:  "class C {\n\tstatic make() -> number { return 1 },\n}\nexport val g = C.make",
			want: "export const g = C.make;",
		},
		{
			name: "AMethodReadByIndex",
			src:  "class C {\n\tm(&self) -> number { return 1 },\n}\nval c = C()\nexport val g = c[\"m\"]",
			want: "export const g = c[\"m\"].bind(c);",
		},
		{
			name: "AMethodReadByAKeyBinding",
			src:  "class C {\n\tm(&self) -> number { return 1 },\n}\nval c = C()\nval k = \"m\"\nexport val g = c[k]",
			want: "export const g = c[k].bind(c);",
		},
		{
			name: "AMethodReadByIndexOffACall",
			src:  "class C {\n\tm(&self) -> number { return 1 },\n}\nfn make() -> C { return C() }\nexport val g = make()[\"m\"]",
			want: "export const g = temp1[\"m\"].bind(temp1);",
		},
		{
			name: "AMethodReadByIndexAndCalled",
			src:  "class C {\n\tm(&self) -> number { return 1 },\n}\nval c = C()\nexport val n = c[\"m\"]()",
			want: "export const n = c[\"m\"]();",
		},
		{
			name: "APropertyHoldingAFunctionReadByIndex",
			src:  "declare val obj: {m: fn () -> number}\nexport val m = obj[\"m\"]",
			want: "export const m = obj[\"m\"];",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			useChecker(t)
			checked := CompilePackage(libSources(tt.src), PackageConfig{}).CompUnits["lib/index"].JS
			useSolver(t)
			solved := CompilePackage(libSources(tt.src), PackageConfig{}).CompUnits["lib/index"].JS

			require.Contains(t, solved, tt.want)
			require.Equal(t, checked, solved, "the two checkers emit different JavaScript")
		})
	}
}

// TestTheSolverBindsAMethodReadThroughAUnion asserts that the solver binds a method read off a
// union receiver whose members declare the method. internal/checker emits the read bare, so this
// asserts what the solver emits rather than that the two agree.
func TestTheSolverBindsAMethodReadThroughAUnion(t *testing.T) {
	useSolver(t)
	src := "class A {\n\tm(&self) -> number { return 1 },\n}\n" +
		"class B {\n\tm(&self) -> number { return 2 },\n}\n" +
		"declare val x: A | B\nexport val g = x.m"
	got := CompilePackage(libSources(src), PackageConfig{}).CompUnits["lib/index"].JS
	require.Contains(t, got, "export const g = x.m.bind(x);")
}

// TestTheSolverEmitsAnObjectRestOverAUnion asserts that an object rest over a union emits
// the union of each member's leftover, the type TypeScript infers for the same pattern.
// internal/checker merges the leftovers into one object, so this asserts what the solver
// emits rather than that the two agree.
func TestTheSolverEmitsAnObjectRestOverAUnion(t *testing.T) {
	useSolver(t)
	src := "type FB = {kind: \"foo\", id: number, foo: string} | {kind: \"bar\", id: number, bar: boolean}\n" +
		"declare val fb: FB\n" +
		"val {kind: k, id: i, ...rest2} = fb"
	got := CompilePackage(libSources(src), PackageConfig{}).CompUnits["lib/index"].DTS
	require.Contains(t, got, "declare const rest2: {foo: string} | {bar: boolean};")
}

// TestTheSolverEmitsAKeyAbsenceTest asserts that an object pattern whose keys fit one exact
// union member, and fit another that carries more, also tests that the extra keys are
// absent. `{radius}` then matches only the unlabelled shape.
func TestTheSolverEmitsAKeyAbsenceTest(t *testing.T) {
	useSolver(t)
	src := "export fn f(s: {radius: number} | {radius: number, label: string}) { return match s { {radius} => radius, {radius, label} => label } }"
	got := CompilePackage(libSources(src), PackageConfig{}).CompUnits["lib/index"].JS
	require.Contains(t, got, `if (temp3 != null && "radius" in temp3 && !("label" in temp3)) {`)
	require.Contains(t, got, `} else if (temp3 != null && "radius" in temp3 && "label" in temp3) {`)
}

// TestTheSolverEmitsAKeyAbsenceTestForAnImpliedArm asserts that an arm whose object test an
// earlier guarded arm's equal test implies still checks the absent keys. The guard can
// fail, so the second `{radius}` arm runs on the values the first one tested.
func TestTheSolverEmitsAKeyAbsenceTestForAnImpliedArm(t *testing.T) {
	useSolver(t)
	src := "export fn f(s: {radius: number} | {radius: number, label: string}) { return match s { {radius} if radius > 0 => 1, {radius} => 2, {radius, label} => 3 } }"
	got := CompilePackage(libSources(src), PackageConfig{}).CompUnits["lib/index"].JS
	require.Contains(t, got, `} else if (temp3 != null && "radius" in temp3 && !("label" in temp3)) {`)
}

// TestTheSolverEmitsNoKeyAbsenceTestForANestedPattern asserts that an object pattern nested
// in a tuple tests only the keys it names. The match is exhaustive, so a test that also
// checked `label` was absent would let the labelled value fall past every arm.
func TestTheSolverEmitsNoKeyAbsenceTestForANestedPattern(t *testing.T) {
	useSolver(t)
	src := "export fn f(s: [{radius: number} | {radius: number, label: string}, number]) { return match s { [{radius}, _] => radius } }"
	got := CompilePackage(libSources(src), PackageConfig{}).CompUnits["lib/index"].JS
	require.Contains(t, got, `if (temp3.length == 2 && temp3[0] != null && "radius" in temp3[0]) {`)
}
