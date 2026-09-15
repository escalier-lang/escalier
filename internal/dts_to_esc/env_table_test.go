package dts_to_esc

import (
	"sort"
	"strings"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/stretchr/testify/require"
)

// Every entry in the two environment tables names something the committed tree
// holds.
//
// The generator checks the other direction, that every `web:*` package it emits
// is classified, and it has to stay tolerant of a partial run. A stale entry is
// a property of the whole tree, so it is read here.
func TestEnvTablesMatchThePinnedLibSet(t *testing.T) {
	t.Parallel()

	modules := parseCommittedTree(t)
	mods := make(map[string]*StandaloneModule, len(modules))
	for uri, module := range modules {
		mods[uri] = &StandaloneModule{Module: module}
	}

	require.Empty(t, StaleEnvTableEntries(mods),
		"table entries naming something the tree does not hold")
}

// Every declaration in the committed tree carries the environments its package
// claims, and no declaration on every environment carries a decorator.
//
// The second half is what keeps the tree readable. Annotating a declaration
// available everywhere would say what an unannotated one already says, and
// would put a decorator on most of the tree.
func TestTheCommittedTreeCarriesItsEnvironments(t *testing.T) {
	t.Parallel()

	everywhere := AllEnvs()
	var wrong []string
	for uri, module := range parseCommittedTree(t) {
		module.Namespaces.Scan(func(_ string, ns *ast.Namespace) bool {
			for _, decl := range ns.Decls {
				names := ast.DeclNames(decl)
				if len(names) == 0 {
					continue
				}
				want := PackageDeclEnvs(uri, names[0])
				got, err := DeclEnvs(decl)
				require.NoError(t, err, "%s: %s", uri, names[0])
				if !got.Equals(want) {
					wrong = append(wrong, uri+": "+names[0]+
						" carries "+printEnvs(got)+", expected "+printEnvs(want))
					continue
				}
				if want.Equals(everywhere) && len(envDecoratorsOf(decl)) > 0 {
					wrong = append(wrong, uri+": "+names[0]+
						" is available everywhere and carries `@env` anyway")
				}
			}
			return true
		})
	}
	sort.Strings(wrong)
	require.Empty(t, wrong, "%s", strings.Join(wrong, "\n  "))
}

// envDecoratorsOf returns the `@env` decorators on a declaration.
func envDecoratorsOf(decl ast.Decl) []*ast.Decorator {
	var found []*ast.Decorator
	for _, dec := range ast.DeclDecorators(decl) {
		if dec.Name != nil && dec.Name.Name == EnvDecoratorName {
			found = append(found, dec)
		}
	}
	return found
}

// A `web:*` package the partition table does not name fails the run, since
// nothing says what file it is written to and so nothing says where it runs.
//
// A package the table does name but whose file carries no suffix is a different
// case: that is a claim to every environment rather than an omission.
// TestAnnotateEnvs_ReadsTheEnvironmentsFromTheFileName covers it.
func TestAnnotateEnvs_RejectsAPackageNoPartitionEntryNames(t *testing.T) {
	t.Parallel()

	err := AnnotateEnvs(envPackages(t, map[string]string{
		"web:brand_new": "export declare class C {}",
	}))
	require.Error(t, err)
	require.Equal(t,
		"converter: web:brand_new is in no partition entry, so nothing names its file",
		err.Error())
}

// A package's file name is where its environments are read from, so this pins
// both halves of the rule against the committed table. `web/dom.window.esc`
// narrows every declaration in it to a page, and `web/fetch.esc` carries no
// suffix and so annotates nothing.
func TestAnnotateEnvs_ReadsTheEnvironmentsFromTheFileName(t *testing.T) {
	t.Parallel()

	mods := envPackages(t, map[string]string{
		"web:dom":   "export declare class C {}",
		"web:fetch": "export declare class D {}",
	})
	require.NoError(t, AnnotateEnvs(mods))

	for _, ns := range mods["web:dom"].Module.Namespaces.Values() {
		for _, decl := range ns.Decls {
			decs := envDecoratorsOf(decl)
			require.Len(t, decs, 1)
			require.Equal(t, []Env{EnvWindow}, sortedEnvs(mustEnvsFromDecorator(t, decs[0])))
		}
	}
	for _, ns := range mods["web:fetch"].Module.Namespaces.Values() {
		for _, decl := range ns.Decls {
			require.Empty(t, envDecoratorsOf(decl))
		}
	}
}

// A `std:*` package needs no entry. The language surface is every environment,
// and listing forty packages to say so would be noise.
func TestAnnotateEnvs_LeavesTheLanguageSurfaceAlone(t *testing.T) {
	t.Parallel()

	mods := envPackages(t, map[string]string{
		"std:brand_new": "export declare class C {}",
	})
	require.NoError(t, AnnotateEnvs(mods))
	for _, ns := range mods["std:brand_new"].Module.Namespaces.Values() {
		for _, decl := range ns.Decls {
			require.Empty(t, envDecoratorsOf(decl))
		}
	}
}

// An `@env` the source already carries is left alone, so a narrowed package
// does not end up with two.
//
// An overlay is where such an annotation comes from, and it says something the
// tables cannot. Stamping beside it would make CheckEnvs reject the pair and
// blame the author for what the generator added.
func TestAnnotateEnvs_LeavesAnAnnotationTheSourceCarries(t *testing.T) {
	t.Parallel()

	mods := envPackages(t, map[string]string{
		"web:dom": "@env(\"window\", \"service_worker\")\nexport declare class C {}",
	})
	require.NoError(t, AnnotateEnvs(mods))

	for _, ns := range mods["web:dom"].Module.Namespaces.Values() {
		for _, decl := range ns.Decls {
			require.Len(t, envDecoratorsOf(decl), 1)
			envs, err := DeclEnvs(decl)
			require.NoError(t, err)
			require.Equal(t, []Env{EnvWindow, EnvServiceWorker}, sortedEnvs(envs))
		}
	}
	require.Empty(t, envMessages(t, mods))
}

// An override beats its package's default, and is addressed by package as well
// as name so it cannot widen a same-named declaration elsewhere.
//
// The committed override map is empty, so this passes its own.
func TestPackageDeclEnvs_AnOverrideBeatsThePackageDefaultForOnePackage(t *testing.T) {
	t.Parallel()

	overrides := map[packageDecl]set.Set[Env]{
		{URI: "web:dom", Name: "Portable"}: AllEnvs(),
	}
	everywhere := []Env{EnvWindow, EnvDedicatedWorker, EnvSharedWorker, EnvServiceWorker}

	// The override answers for the package it names.
	require.Equal(t, everywhere, sortedEnvs(declEnvsFrom(overrides, "web:dom", "Portable")))
	// Another narrowed package holding the same name takes its own default.
	require.Equal(t, []Env{EnvWindow}, sortedEnvs(declEnvsFrom(overrides, "web:storage", "Portable")))
	// A declaration the map does not name takes its package's default.
	require.Equal(t, []Env{EnvWindow},
		sortedEnvs(declEnvsFrom(overrides, "web:dom", "HTMLCanvasElement")))
	// A package the table does not narrow is every environment.
	require.Equal(t, everywhere, sortedEnvs(declEnvsFrom(overrides, "web:url", "URL")))
}

// A declaration binding several names needs one answer for all of them, since
// one decorator answers for the whole declaration.
//
// A destructuring `val` is the case. EnvIndex records the declaration's set
// against every name it binds, so resolving the annotation against the first
// alone would let a second name's override go unread here while the index
// honoured it.
func TestDeclaredEnvs_NeedsEveryNameToAgree(t *testing.T) {
	t.Parallel()

	overrides := map[packageDecl]set.Set[Env]{
		{URI: "web:dom", Name: "Portable"}: AllEnvs(),
	}

	// Both names take web:dom's default, so they agree.
	envs, err := declaredEnvsFrom(overrides, "web:dom", []string{"a", "b"})
	require.NoError(t, err)
	require.Equal(t, []Env{EnvWindow}, sortedEnvs(envs))

	// One name carrying an override and the other not is the disagreement.
	_, err = declaredEnvsFrom(overrides, "web:dom", []string{"Portable", "HTMLCanvasElement"})
	require.Error(t, err)
	require.Equal(t,
		"converter: web:dom: one declaration binds \"Portable\" and "+
			"\"HTMLCanvasElement\" with different environments; a decorator answers "+
			"for the whole declaration, so give them one entry or split the declaration",
		err.Error())
}

// mustEnvsFromDecorator reads the set one `@env` names, failing the test rather
// than returning an error, since a decorator this package wrote should parse.
func mustEnvsFromDecorator(t *testing.T, dec *ast.Decorator) set.Set[Env] {
	t.Helper()
	envs, err := envsFromDecorator(dec)
	require.NoError(t, err)
	return envs
}
