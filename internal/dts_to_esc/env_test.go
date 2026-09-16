package dts_to_esc

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/require"
)

// envPackages parses one Escalier source per pseudo-package URI.
//
// The environment rules read an already-converted tree, so the input here is
// Escalier rather than `.d.ts`. Writing the packages by hand is what lets one
// case hold two declarations and one annotation.
func envPackages(t *testing.T, sources map[string]string) map[string]*StandaloneModule {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	uris := make([]string, 0, len(sources))
	for uri := range sources {
		uris = append(uris, uri)
	}
	sort.Strings(uris)

	mods := map[string]*StandaloneModule{}
	for i, uri := range uris {
		module, errs := parser.ParseLibFiles(ctx, []*ast.Source{{
			ID: i, Path: uri + ".esc", Contents: sources[uri] + "\n",
		}})
		require.Empty(t, errs, "parse %s", uri)
		mods[uri] = &StandaloneModule{Module: module}
	}
	return mods
}

// envMessages renders each violation CheckEnvs reports, in its order.
func envMessages(t *testing.T, mods map[string]*StandaloneModule) []string {
	t.Helper()
	violations, err := CheckEnvs(mods)
	require.NoError(t, err)
	out := make([]string, 0, len(violations))
	for _, v := range violations {
		out = append(out, v.String())
	}
	return out
}

// A declaration reaching something absent from an environment it claims is
// reported at the reference.
func TestCheckEnvs_ReportsAReferenceOutsideTheReferentsEnvironments(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		sources map[string]string
		want    []string
	}{
		// The window-only class is reachable from everywhere the portable one
		// is, which is three environments it does not exist on.
		"AcrossPackages": {
			sources: map[string]string{
				"web:dom": "@env(\"window\")\nexport declare class HTMLFormElement {}",
				"web:file": "export declare class FormData {\n" +
					"    form: dom.HTMLFormElement\n}",
			},
			want: []string{
				"web:file: FormData.form names HTMLFormElement, " +
					"which is absent from dedicated_worker, shared_worker, service_worker",
			},
		},
		// Narrowing the member to the environments the referent has is the fix,
		// and it is what the annotation exists to express.
		"AMemberNarrowedToMatchIsFine": {
			sources: map[string]string{
				"web:dom": "@env(\"window\")\nexport declare class HTMLFormElement {}",
				"web:file": "export declare class FormData {\n" +
					"    @env(\"window\")\n    form: dom.HTMLFormElement\n}",
			},
			want: nil,
		},
		// An `extends` clause is the class's own reference, not a member's, so
		// it is checked against the class's environments and reported with no
		// member name.
		"AnExtendsClause": {
			sources: map[string]string{
				"web:dom": "@env(\"window\")\nexport declare class HTMLElement {}",
				"web:core": "@env(\"window\", \"worker\")\n" +
					"export declare class Widget extends dom.HTMLElement {}",
			},
			want: []string{
				"web:core: Widget names HTMLElement, " +
					"which is absent from dedicated_worker, shared_worker, service_worker",
			},
		},
		// A worker-only declaration reached from a window-only one reports the
		// window, which is the direction a rank could not express.
		"SiblingsReportEachOther": {
			sources: map[string]string{
				"web:sync":  "@env(\"worker\")\nexport declare class FileReaderSync {}",
				"web:pages": "@env(\"window\")\nexport declare val read: sync.FileReaderSync",
			},
			want: []string{
				"web:pages: read names FileReaderSync, which is absent from window",
			},
		},
		// A name no package declares carries no environments, so it is skipped
		// rather than read as available nowhere.
		"AnUndeclaredNameIsSkipped": {
			sources: map[string]string{
				"web:dom": "@env(\"window\")\nexport declare val x: NotDeclaredAnywhere",
			},
			want: nil,
		},
		// An unannotated declaration is available everywhere, so it can be
		// named from anywhere.
		"AnUnannotatedReferentIsEverywhere": {
			sources: map[string]string{
				"web:core":  "export declare class Event {}",
				"web:pages": "@env(\"window\")\nexport declare class UIEvent extends core.Event {}",
			},
			want: nil,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if len(test.want) == 0 {
				require.Empty(t, envMessages(t, envPackages(t, test.sources)))
				return
			}
			require.Equal(t, test.want, envMessages(t, envPackages(t, test.sources)))
		})
	}
}

// An interface, a type alias and an enum carry `@env` too. Most of the web tree
// is interfaces, so a mechanism that reached only classes and values would have
// nothing to say about three declarations in four.
func TestCheckEnvs_ReachesEveryAnnotatableDeclarationKind(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"Interface": "@env(\"window\")\nexport declare interface Target { x: number }",
		"TypeAlias": "@env(\"window\")\nexport declare type Target = number",
		"Enum":      "@env(\"window\")\nexport enum Target { A, B }",
		"Class":     "@env(\"window\")\nexport declare class Target {}",
		"Value":     "@env(\"window\")\nexport declare val Target: number",
	}

	for name, decl := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, []string{
				"web:b: Holder.t names Target, " +
					"which is absent from dedicated_worker, shared_worker, service_worker",
			}, envMessages(t, envPackages(t, map[string]string{
				"web:a": decl,
				"web:b": "export declare class Holder {\n    t: a.Target\n}",
			})))
		})
	}
}

// The qualifier on a cross-package reference names a package, not a
// declaration, so only the last segment is resolved.
//
// The tree declares globals called `crypto`, `performance` and `fetch` beside
// the packages of those names. Reading the head as a reference would make every
// package that writes `crypto.Crypto` look like it names the global.
func TestCheckEnvs_AQualifierIsNotAReference(t *testing.T) {
	t.Parallel()

	require.Empty(t, envMessages(t, envPackages(t, map[string]string{
		"web:crypto": "@env(\"window\")\nexport declare val crypto: number\n" +
			"export declare class Crypto {}",
		"web:dom": "export declare class Doc {\n    c: crypto.Crypto\n}",
	})))
}

// A type parameter is not a declaration, so one whose name matches an annotated
// declaration is left alone.
func TestCheckEnvs_ATypeParameterIsNotAReference(t *testing.T) {
	t.Parallel()

	require.Empty(t, envMessages(t, envPackages(t, map[string]string{
		"web:a": "@env(\"window\")\nexport declare class E {}",
		"web:b": "export declare class Box<E> {\n    v: E\n}",
	})))
}

// Two declarations of one name intersect, so neither half's narrowing is lost.
//
// TypeScript's class idiom pairs an interface with a `declare var` of the same
// name, and an overload set is one declaration per signature. Overwriting would
// leave the entry at whichever was scanned last.
func TestEnvIndex_IntersectsOverADuplicateName(t *testing.T) {
	t.Parallel()

	index, err := EnvIndex(envPackages(t, map[string]string{
		"web:a": "export declare type X = number\n" +
			"@env(\"window\")\nexport declare val X: number",
	}))
	require.NoError(t, err)
	require.Equal(t, []Env{EnvWindow}, sortedEnvs(index["X"]))
}

// An unannotated member takes its class's environments, so a window-only class
// reports for a member reaching a worker-only name without that member carrying
// a marker of its own.
func TestCheckEnvs_AMemberInheritsItsClassEnvironments(t *testing.T) {
	t.Parallel()

	require.Equal(t, []string{
		"web:pages: Page.sync names FileReaderSync, which is absent from window",
	}, envMessages(t, envPackages(t, map[string]string{
		"web:sync": "@env(\"worker\")\nexport declare class FileReaderSync {}",
		"web:pages": "@env(\"window\")\nexport declare class Page {\n" +
			"    sync: sync.FileReaderSync\n}",
	})))
}

// DeclEnvs and MemberEnvs resolve a decorator into the set it names.
func TestEnvResolution(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		src  string
		want []Env
	}{
		"UnannotatedIsEveryEnvironment": {
			src:  "export declare class C {}",
			want: []Env{EnvWindow, EnvDedicatedWorker, EnvSharedWorker, EnvServiceWorker},
		},
		"OneEnvironment": {
			src:  "@env(\"window\")\nexport declare class C {}",
			want: []Env{EnvWindow},
		},
		"SeveralEnvironments": {
			src:  "@env(\"window\", \"service_worker\")\nexport declare class C {}",
			want: []Env{EnvWindow, EnvServiceWorker},
		},
		// A group stands for several environments, so a declaration on every
		// worker kind writes one tag rather than three.
		"AGroupExpands": {
			src:  "@env(\"worker\")\nexport declare class C {}",
			want: []Env{EnvDedicatedWorker, EnvSharedWorker, EnvServiceWorker},
		},
		"AGroupBesideAnEnvironment": {
			src:  "@env(\"worker\", \"window\")\nexport declare class C {}",
			want: []Env{EnvWindow, EnvDedicatedWorker, EnvSharedWorker, EnvServiceWorker},
		},
		// Naming one environment twice says the same thing once.
		"ARepeatedNameIsOneEnvironment": {
			src:  "@env(\"window\", \"window\")\nexport declare class C {}",
			want: []Env{EnvWindow},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			envs, err := DeclEnvs(onlyDecl(t, test.src))
			require.NoError(t, err)
			require.Equal(t, test.want, sortedEnvs(envs))
		})
	}
}

// The vocabulary is closed, so a tag naming nothing fails rather than narrowing
// a declaration to a set no reference can satisfy.
func TestEnvResolution_RejectsAMalformedAnnotation(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		src  string
		want string
	}{
		"UnknownEnvironment": {
			src: "@env(\"browser\")\nexport declare class C {}",
			want: "`@env` names unknown environment \"browser\"; the vocabulary is " +
				"dedicated_worker, service_worker, shared_worker, window, worker",
		},
		"NoArguments": {
			src: "@env\nexport declare class C {}",
			want: "`@env` names no environment; list at least one of " +
				"dedicated_worker, service_worker, shared_worker, window, worker",
		},
		"EmptyArguments": {
			src: "@env()\nexport declare class C {}",
			want: "`@env` names no environment; list at least one of " +
				"dedicated_worker, service_worker, shared_worker, window, worker",
		},
		"ANonStringArgument": {
			src:  "@env(1)\nexport declare class C {}",
			want: "`@env` takes string-literal arguments",
		},
		"TwoAnnotations": {
			src:  "@env(\"window\")\n@env(\"worker\")\nexport declare class C {}",
			want: "two `@env` decorators; write one naming every environment",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := DeclEnvs(onlyDecl(t, test.src))
			require.Error(t, err)
			require.Equal(t, test.want, err.Error())
		})
	}
}

// A member cannot claim an environment its class does not have. The class is
// what an importer reaches the member through, so a member existing where the
// class does not is unreachable rather than available.
func TestMemberEnvs_RejectsAMemberOutsideItsClass(t *testing.T) {
	t.Parallel()

	cls, ok := onlyDecl(t, "@env(\"window\")\nexport declare class C {\n"+
		"    @env(\"service_worker\")\n    x: number\n}").(*ast.ClassDecl)
	require.True(t, ok)

	owner, err := DeclEnvs(cls)
	require.NoError(t, err)
	_, err = MemberEnvs(cls.Body[0], owner)
	require.Error(t, err)
	require.Equal(t,
		"member claims service_worker, which its class does not; "+
			"a member exists only where its class does",
		err.Error())
}

// onlyDecl parses one Escalier declaration and returns it.
func onlyDecl(t *testing.T, src string) ast.Decl {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	module, errs := parser.ParseLibFiles(ctx, []*ast.Source{{ID: 0, Path: "t.esc", Contents: src + "\n"}})
	require.Empty(t, errs)
	for _, ns := range module.Namespaces.Values() {
		if len(ns.Decls) > 0 {
			return ns.Decls[0]
		}
	}
	t.Fatal("no declaration parsed")
	return nil
}
