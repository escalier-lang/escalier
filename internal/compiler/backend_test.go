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
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/stretchr/testify/require"
)

// useChecker points the compiler at internal/checker and at the repo's standard
// library tree. The tree matters on the solver path, which resolves `std:prelude`
// from disk, and the test binary runs from a temporary directory the lazy
// stdlibdir.StdlibDir("") walk cannot find the repo root from.
func useChecker(t *testing.T) {
	t.Helper()
	t.Setenv(CheckerEnvVar, "")
	t.Setenv("ESCALIER_STDLIB_DIR", filepath.Join("..", "interop", "data"))
}

// useSolver is useChecker with CheckerEnvVar set to the solver.
func useSolver(t *testing.T) {
	t.Helper()
	useChecker(t)
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

// TestSelectBackend checks which checker CheckerEnvVar names. The old checker is
// the default, so an unset variable and a value the seam does not recognize both
// leave the compiler's behavior as it was before the solver path existed.
func TestSelectBackend(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  backend
	}{
		{name: "Unset", value: "", want: checkerBackend{}},
		{name: "Solver", value: CheckerSolver, want: solverBackend{}},
		{name: "Unrecognized", value: "nonesuch", want: checkerBackend{}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(CheckerEnvVar, test.value)
			require.IsType(t, test.want, selectBackend())
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
		out := CheckLib(context.Background(), libSources(src))
		require.Empty(t, out.ParseErrors)
		require.Contains(t, messages(out.TypeErrors), `"hello" cannot be assigned to number`)
	})

	t.Run("Solver", func(t *testing.T) {
		useSolver(t)
		out := CheckLib(context.Background(), libSources(src))
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
			libOutput := CheckLib(ctx, libSources(lib))
			require.NotNil(t, libOutput.LibScope)

			resolved := CheckBinScript(ctx, libOutput.LibScope, binSource("val g = greeting\n"))
			require.Empty(t, resolved.ParseErrors)
			require.NotContains(t, messages(resolved.TypeErrors), "Unknown identifier: greeting")

			unresolved := CheckBinScript(ctx, libOutput.LibScope, binSource("val f = farewell\n"))
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
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			libOutput := CheckLib(ctx, libSources(lib))
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

// TestCompileReportsTheSolverCodegenGap checks that a compile on the solver path
// says its output is not yet correct, and that the checker path says nothing extra.
// Codegen reads types only internal/checker stamps onto the tree, so the solver's
// JavaScript is wrong in ways no other diagnostic names.
func TestCompileReportsTheSolverCodegenGap(t *testing.T) {
	const gap = solverCodegenGap

	t.Run("Checker", func(t *testing.T) {
		useChecker(t)
		out := CompilePackage(append(libSources("export val greeting = \"hello\"\n"), binSource("val g = greeting\n")))
		require.NotContains(t, messages(out.TypeErrors), gap)
	})

	t.Run("Solver", func(t *testing.T) {
		useSolver(t)
		out := CompilePackage(append(libSources("export val greeting = \"hello\"\n"), binSource("val g = greeting\n")))
		// One for the library's compilation unit and one for the script's, since
		// each is a file whose emitted output is wrong.
		require.Equal(t, 2, countMessage(messages(out.TypeErrors), gap))
		// The gap covers output that is written but not yet right, so the library's
		// .d.ts is emitted rather than left empty.
		require.NotEmpty(t, out.CompUnits["lib/index"].DTS)

		// Each blames the file it is about, so a caller placing diagnostics puts
		// them on the library and on the script rather than both on one.
		blamed := []int{}
		for _, err := range out.TypeErrors {
			if err.Message() == gap {
				blamed = append(blamed, err.Span().SourceID)
			}
		}
		require.Equal(t, []int{0, 1}, blamed)
	})
}

// solverCodegenGap is the message the solver path reports for each file it emits.
const solverCodegenGap = "ESCALIER_CHECKER=solver does not yet emit correct output for this file: " +
	"a read of a function-typed field is bound to its receiver, and the .d.ts does not " +
	"yet match the one the old checker writes"

// countMessage returns how many of msgs equal want.
func countMessage(msgs []string, want string) int {
	n := 0
	for _, msg := range msgs {
		if msg == want {
			n++
		}
	}
	return n
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
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			libOutput := CheckLib(ctx, libSources(lib))
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

	out := CheckLib(context.Background(), libSources("export val greeting = \"hello\"\n"))

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

// TestCompileScriptReportsTheGapOfTheCheckerThatChecked checks that the codegen gap
// follows the checker that checked the script rather than the one selected when the
// emit happens. A LibScope holds the checker that produced it, so reading the gap off
// the selected backend instead would emit solver-checked JavaScript with nothing
// saying it cannot be trusted.
func TestCompileScriptReportsTheGapOfTheCheckerThatChecked(t *testing.T) {
	useSolver(t)
	libOutput := CheckLib(context.Background(), libSources("export val greeting = \"hello\"\n"))
	require.NotNil(t, libOutput.LibScope)

	// The library keeps the solver. The variable goes back to the old checker.
	t.Setenv(CheckerEnvVar, "")

	out := CompileScript(libOutput.LibScope, binSource("val g = greeting\n"))
	require.Empty(t, out.ParseErrors)
	require.Equal(t, 1, countMessage(messages(out.TypeErrors), solverCodegenGap))
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

// solverInferenceOverflows are fixtures whose inference overflows the stack on the
// solver path, before any emission runs. A Go stack overflow is fatal and takes the
// test binary with it, so these are skipped rather than allowed to fail. Tracked in
// #1695.
var solverInferenceOverflows = set.FromSlice([]string{"class_with_fluent_mutating_methods"})

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
			if solverInferenceOverflows.Contains(entry.Name()) {
				t.Skip("inference overflows the stack on the solver path, see #1695")
			}
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
			out := CompilePackage(sources)
			require.NotEmpty(t, out.CompUnits["lib/index"].DTS)
		})
	}
}

// TestBothCheckersEmitTheSameJS asserts that a source exercising a type the emitter
// reads emits identical JavaScript whichever checker ran.
//
// A source whose class reads a function-typed field is left out. The solver binds such
// a read to its receiver and the checker does not, which is #1782 rather than anything
// about the types read here.
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
			want := CompilePackage(sources).CompUnits["lib/index"].JS
			useSolver(t)
			got := CompilePackage(sources).CompUnits["lib/index"].JS

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
			js := CompilePackage(sources).CompUnits["lib/index"].JS
			require.NotEmpty(t, js)
			require.NotContains(t, js, "instanceof Alias",
				"an alias has no runtime binding, so the guard would throw a ReferenceError")
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
	want := CompilePackage(sources).CompUnits["lib/index"].DTS
	useSolver(t)
	got := CompilePackage(sources).CompUnits["lib/index"].DTS

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
			dts := CompilePackage(libSources(src)).CompUnits["lib/index"].DTS
			require.Contains(t, dts,
				"declare const Holder: {new <T0 extends {value: number}>(peer: T0): Holder<T0>};")
		})
	}
}
