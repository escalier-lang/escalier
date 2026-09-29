package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/compiler"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/solver"
	"github.com/stretchr/testify/require"
)

// solver_fixture_test.go checks every fixture on internal/solver and asserts only
// whether the package is accepted or rejected. It emits nothing and compares no
// `build/` tree, so it measures what the solver can check rather than what it can
// write. TestBuildFixtureTests is the emitting harness beside it.
//
// A fixture's committed `error.txt` is the expected answer. The build harness writes
// that file when a compile reports anything, so its presence records that the package
// is meant to be rejected and its absence that the package is meant to be accepted. A
// fixture a DISABLED marker holds out of that harness never ran, so its missing
// `error.txt` records nothing and this harness holds it out too.

// solverSkipCause is a root cause several fixtures wait on. Grouping the skip list by
// cause rather than listing fixtures flat is what makes it a work queue: one pull
// request per cause clears every fixture under it, which is what #1672 does.
type solverSkipCause struct {
	// name is how the cause reads in the test's summary.
	name string
	// ticket is the issue that clears it, or empty when nothing names it yet. An empty
	// ticket is itself a finding, since it marks work the cutover plan did not foresee.
	ticket string
}

var (
	causeAmbientScope = &solverSkipCause{
		name:   "a global the ambient builtin scope will bind",
		ticket: "#1666",
	}
	causePrimitiveMember = &solverSkipCause{
		name:   "a member read off a primitive, which needs its wrapper type",
		ticket: "#1714",
	}
	causeUnaryOperators = &solverSkipCause{
		name:   "unary operators",
		ticket: "#1653",
	}
	causeTemplateLiterals = &solverSkipCause{
		name:   "template literals",
		ticket: "#1654",
	}
	causeTypeCast = &solverSkipCause{
		name:   "typecast expressions",
		ticket: "#1655",
	}
	causeDoExpressions = &solverSkipCause{
		name:   "do expressions",
		ticket: "#1656",
	}
	causeIndexAndComputed = &solverSkipCause{
		name:   "index expressions, computed keys, and assignment to a member",
		ticket: "#1715",
	}
	causeNamespaceMember = &solverSkipCause{
		name:   "a bare reference to a namespace sibling",
		ticket: "#1716",
	}
	causeSuperGate = &solverSkipCause{
		name:   "the gate requiring a subclass constructor to call `super(…)`",
		ticket: "#1720",
	}
	causeIteration = &solverSkipCause{
		name:   "iteration and spreading an iterable",
		ticket: "#1717",
	}
	causePatterns = &solverSkipCause{
		name:   "rest and default sub-patterns in an extractor pattern",
		ticket: "#1718",
	}
	causeExtendsTypeArgs = &solverSkipCause{
		name:   "a generic named in an `extends` clause without its type arguments",
		ticket: "#1721",
	}
	causeUnionMember = &solverSkipCause{
		name:   "a property read on a union whose arms do not all declare it",
		ticket: "#1719",
	}
	causeStackOverflow = &solverSkipCause{
		name:   "inference overflows the stack",
		ticket: "#1695",
	}
)

// solverSkip is one fixture the solver cannot yet check, with the cause it waits on
// and the diagnostic that stands for it.
type solverSkip struct {
	fixture string
	cause   *solverSkipCause
	// reason is one diagnostic from the fixture's run, quoted as the run reports it.
	// A fixture usually reports several. This is the one that names the cause.
	reason string
}

// solverSkips is the measure this harness exists to produce: what the solver cannot
// yet check, and why. Every entry was seeded from a run rather than predicted, and the
// harness fails when an entry starts passing, so the list burns down instead of
// rotting.
//
// A fixture rejected for several causes is filed under the one to clear first. Its
// other diagnostics surface once that one lands and the entry is re-seeded.
var solverSkips = []solverSkip{
	{"async_await", causeAmbientScope, "Unknown identifier: fetch"},
	{"bin_and_lib", causeAmbientScope, "Unknown identifier: console"},
	{"bin_only", causeAmbientScope, "Unknown identifier: console"},
	{"logging", causeAmbientScope, "Unknown identifier: console"},
	{"namespace_use_parent_symbol", causeAmbientScope, "cannot find type `Function`"},
	{"try_catch", causeAmbientScope, "Unknown identifier: Error"},

	{"function_overloading", causePrimitiveMember, "cannot constrain number <: object"},
	{"function_overloading_with_deps", causePrimitiveMember, "cannot constrain number <: object"},
	{"grouping", causePrimitiveMember, "cannot constrain number <: object"},
	{"if_val", causePrimitiveMember, "cannot constrain number <: object"},

	{"literals", causeUnaryOperators, "Unsupported: UnaryExpr"},
	{"template_literals", causeTemplateLiterals, "Unsupported: TemplateLitExpr"},
	{"generic_class", causeTypeCast, "Unsupported: TypeCastExpr"},
	{"do", causeDoExpressions, "Unsupported: DoExpr"},

	{"class_with_computed_members", causeIndexAndComputed, "Unsupported: assignment to a member or index"},
	{"class_with_getter_setter", causeIndexAndComputed, "Unsupported: IndexExpr"},
	{"objects_with_computed_members", causeIndexAndComputed, "Unsupported: ComputedKey"},

	{"namespace_bin_import", causeNamespaceMember, "Unknown identifier: Point"},
	{"namespace_simple_same", causeNamespaceMember, "Unknown identifier: base"},

	{"class_field_name_collision", causeSuperGate, "A subclass constructor must call `super(…)`"},
	{"class_inheritance_namespaces", causeSuperGate, "A subclass constructor must call `super(…)`"},

	{"generators", causeIteration, "cannot spread t5 into a tuple"},
	{"iterators", causeIteration, "string is not iterable"},

	{"extractor_rest_arg", causePatterns, "extractor pattern `C` expects 3 arguments but got 2"},
	{"extractor_with_defaults", causePatterns, "cannot constrain undefined <: string"},
	{"pattern_matching", causePatterns, "object is missing property: area"},

	{"interface", causeExtendsTypeArgs, "type alias `Box` expects 1 type argument but got 0"},

	{"member_access", causeUnionMember, "the package is accepted, and error.txt records a rejection"},

	{"class_with_fluent_mutating_methods", causeStackOverflow, "inference never returns"},
}

// TestCheckFixturesOnSolver checks each fixture on the solver and asserts the package
// is accepted or rejected as its `error.txt` records.
func TestCheckFixturesOnSolver(t *testing.T) {
	_, currentFile, _, _ := runtime.Caller(0)
	rootDir := filepath.Join(filepath.Dir(currentFile), "..", "..")
	dataDir := filepath.Join(rootDir, "internal", "interop", "data")

	t.Setenv(compiler.CheckerEnvVar, compiler.CheckerSolver)
	// The solver resolves `std:prelude` from disk, and the test binary's working
	// directory is not a place the lazy repo-root walk finds the tree from.
	t.Setenv("ESCALIER_STDLIB_DIR", dataDir)
	t.Setenv("ESCALIER_BUILTINS_DIR", dataDir)

	entries, err := os.ReadDir(filepath.Join(rootDir, "fixtures"))
	require.NoError(t, err)

	fixtures := set.NewSet[string]()
	disabled := set.NewSet[string]()
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		fixtures.Add(entry.Name())
		if _, err := os.Stat(filepath.Join(rootDir, "fixtures", entry.Name(), disabledMarker)); err == nil {
			disabled.Add(entry.Name())
		}
	}

	skips := make(map[string]solverSkip, len(solverSkips))
	for _, skip := range solverSkips {
		// A renamed or deleted fixture would otherwise leave a dead entry behind, and
		// for the stack-overflow entry that would let the fixture run and take the test
		// binary down with it.
		require.True(t, fixtures.Contains(skip.fixture), "solverSkips names no such fixture")
		require.False(t, disabled.Contains(skip.fixture),
			"a disabled fixture is held out of the run already, so it needs no entry")
		require.NotContains(t, skips, skip.fixture, "one fixture is listed twice")
		skips[skip.fixture] = skip
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		t.Run(entry.Name(), func(t *testing.T) {
			skip, skipped := skips[entry.Name()]
			if skipped && skip.cause == causeStackOverflow {
				// A Go stack overflow is fatal and takes the test binary with it, so this
				// fixture cannot be run at all, not even to see whether it still fails.
				t.Skipf("%s: %s (%s)", skip.cause.name, skip.reason, skip.cause.ticket)
			}

			fixtureDir := filepath.Join(rootDir, "fixtures", entry.Name())
			// A disabled fixture never ran, so its missing error.txt records that rather
			// than an expected acceptance, and there is no answer to check against.
			if reason, err := os.ReadFile(filepath.Join(fixtureDir, disabledMarker)); err == nil {
				t.Skipf("disabled: %s", strings.TrimSpace(string(reason)))
			}

			sources := fixtureSources(t, fixtureDir)
			require.NotEmpty(t, sources, "a fixture declares at least one .esc file")

			_, statErr := os.Stat(filepath.Join(fixtureDir, "error.txt"))
			wantRejected := statErr == nil
			diagnostics := checkOnSolver(sources)

			if skipped {
				// Re-run rather than trusting the list. An entry that starts passing is
				// a failure, so clearing a cause forces its fixtures back into the run.
				checksAsExpected := (len(diagnostics) > 0) == wantRejected
				require.False(t, checksAsExpected,
					"this fixture now checks as expected; drop it from solverSkips")
				t.Skipf("%s: %s", skip.cause.name, skip.reason)
			}

			if wantRejected {
				require.NotEmpty(t, diagnostics,
					"error.txt records a rejection, so the solver has to report something")
				return
			}
			require.Empty(t, diagnostics, "the package is expected to check cleanly")
		})
	}

	reportSolverSkipCauses(t, fixtures.Len()-disabled.Len())
}

// preludeURI is the package every run loads whether or not the source imports it.
const preludeURI = "std:prelude"

// checkOnSolver checks one package and returns the diagnostics that belong to it,
// rendered as the run reports them.
//
// The prelude's own diagnostics are dropped. Every run loads it and it currently
// reports two, so counting them would make all 73 fixtures look rejected. Clearing
// them is #1664. Any other package's diagnostics are kept, because a fixture reaches
// one only by importing it, which makes its failure part of that fixture's story.
func checkOnSolver(sources []*ast.Source) []string {
	output := compiler.CheckPackage(sources)

	var diagnostics []string
	for _, diagnostic := range output.TypeErrors {
		if fromPackage, ok := diagnostic.(*solver.PackageInferenceError); ok && fromPackage.URI == preludeURI {
			continue
		}
		diagnostics = append(diagnostics, diagnostic.Message())
	}
	for _, parseError := range output.ParseErrors {
		diagnostics = append(diagnostics, parseError.Message)
	}
	return diagnostics
}

// fixtureSources reads a fixture's `lib/` and `bin/` files as the source list the
// entry points take. It walks subdirectories, since a fixture puts a namespace's
// declarations in a directory of that name, and sorts by path so a multi-file fixture
// is assembled the same way on every run.
//
// Each source's path is the fixture-relative one the CLI would pass, which is what
// CheckPackage splits the library from the scripts by.
func fixtureSources(t *testing.T, fixtureDir string) []*ast.Source {
	t.Helper()

	var paths []string
	for _, dir := range []string{"lib", "bin"} {
		root := filepath.Join(fixtureDir, dir)
		if _, err := os.Stat(root); os.IsNotExist(err) {
			continue
		}
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".esc") {
				paths = append(paths, path)
			}
			return nil
		})
		require.NoError(t, err)
	}
	sort.Strings(paths)

	sources := make([]*ast.Source, 0, len(paths))
	for i, path := range paths {
		contents, err := os.ReadFile(path)
		require.NoError(t, err)
		rel, err := filepath.Rel(fixtureDir, path)
		require.NoError(t, err)
		sources = append(sources, &ast.Source{
			ID:       i,
			Path:     filepath.ToSlash(rel),
			Contents: string(contents),
		})
	}
	return sources
}

// reportSolverSkipCauses logs how many fixtures each cause holds back, so a run says
// where the remaining work is rather than only that some fixtures are skipped. Run
// with `-v` to read it. runnable counts the fixtures this harness checks at all, which
// leaves out the ones a DISABLED marker holds back.
func reportSolverSkipCauses(t *testing.T, runnable int) {
	t.Helper()

	counts := map[*solverSkipCause]int{}
	causes := []*solverSkipCause{}
	for _, skip := range solverSkips {
		if counts[skip.cause] == 0 {
			causes = append(causes, skip.cause)
		}
		counts[skip.cause]++
	}
	sort.SliceStable(causes, func(i, j int) bool { return counts[causes[i]] > counts[causes[j]] })

	unticketed := set.NewSet[string]()
	lines := make([]string, 0, len(causes))
	for _, cause := range causes {
		ticket := cause.ticket
		if ticket == "" {
			ticket = "no ticket yet"
			unticketed.Add(cause.name)
		}
		lines = append(lines, fmt.Sprintf("  %2d  %s (%s)", counts[cause], cause.name, ticket))
	}

	t.Logf("the solver checks %d of %d fixtures; %d are held back by %d causes, %d of them unticketed:\n%s",
		runnable-len(solverSkips), runnable, len(solverSkips), len(causes), unticketed.Len(),
		strings.Join(lines, "\n"))
}
