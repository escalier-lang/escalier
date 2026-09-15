package dts_to_esc

import (
	"sort"
	"strings"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
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

// A `web:*` package with no entry fails the run rather than defaulting to every
// environment, so a new package has to be classified.
func TestAnnotateEnvs_RejectsAnUnclassifiedWebPackage(t *testing.T) {
	t.Parallel()

	err := AnnotateEnvs(envPackages(t, map[string]string{
		"web:brand_new": "export declare class C {}",
	}))
	require.Error(t, err)
	require.Equal(t,
		"converter: packageEnvs has no entry for web:brand_new; say which "+
			"environments each exists on in internal/dts_to_esc/env_table.go",
		err.Error())
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

// An override is addressed by package as well as name, so it cannot widen a
// same-named declaration in another package.
func TestPackageDeclEnvs_AnOverrideIsAddressedByPackage(t *testing.T) {
	t.Parallel()

	require.Equal(t, []Env{EnvWindow, EnvDedicatedWorker, EnvSharedWorker, EnvServiceWorker},
		sortedEnvs(PackageDeclEnvs("web:dom", "XMLHttpRequestBodyInit")))
	// The same name in a narrowed package that holds no override takes that
	// package's default.
	require.Equal(t, []Env{EnvWindow},
		sortedEnvs(PackageDeclEnvs("web:storage", "XMLHttpRequestBodyInit")))
}

// A declaration whose environments differ from its package's takes the override
// rather than the package default.
//
// `XMLHttpRequestBodyInit` is the case in the tree: `web:dom` is a window, and
// this alias is `Blob | BufferSource | FormData | URLSearchParams | string`,
// every arm of which each environment has.
func TestPackageDeclEnvs_AnOverrideBeatsThePackageDefault(t *testing.T) {
	t.Parallel()

	require.Equal(t, []Env{EnvWindow},
		sortedEnvs(PackageDeclEnvs("web:dom", "HTMLCanvasElement")))
	require.Equal(t, []Env{EnvWindow, EnvDedicatedWorker, EnvSharedWorker, EnvServiceWorker},
		sortedEnvs(PackageDeclEnvs("web:dom", "XMLHttpRequestBodyInit")))
}
