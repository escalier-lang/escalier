package dts_to_esc

import (
	"sort"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/stretchr/testify/require"
)

// pinnedLibDir is the pinned TypeScript lib set, relative to this package.
const pinnedLibDir = "../../node_modules/typescript/lib"

// convertBothWebLibs routes lib.dom.d.ts and lib.webworker.d.ts together and
// converts the result.
//
// The run's own DroppedSources skips the worker lib, and this reads it anyway.
// The environments a member exists on come from which file declared it, so the
// table has to answer before the tree carries the worker surface, and this is
// what says it does. #1633 is the change that undrops the file.
func convertBothWebLibs(t *testing.T) (map[string]*StandaloneModule, map[int]string) {
	t.Helper()
	inputs, err := ParseLibFiles(pinnedLibDir,
		[]string{"lib.dom.d.ts", "lib.webworker.d.ts"})
	require.NoError(t, err)

	// The run's own drop list, minus the worker libs. Keeping
	// lib.scripthost.d.ts dropped matters even though this reads neither: a file
	// no table names would widen to every environment rather than be flagged.
	prevDropped, prevResidual := DroppedSources, DOMResidualSources
	DroppedSources = set.FromSlice([]string{"lib.scripthost.d.ts"})
	DOMResidualSources = set.FromSlice([]string{"lib.dom.d.ts", "lib.webworker.d.ts"})
	defer func() { DroppedSources, DOMResidualSources = prevDropped, prevResidual }()

	res, err := PartitionLib(inputs)
	require.NoError(t, err)
	mods, err := ConvertBuckets(res, nil)
	require.NoError(t, err)
	return mods, res.SourceFiles
}

// memberEnvNames renders the environments each member of one declaration exists
// on, keyed by member name.
func memberEnvNames(
	t *testing.T, mods map[string]*StandaloneModule, sourceFiles map[int]string, want string,
) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, mod := range mods {
		mod.Module.Namespaces.Scan(func(_ string, ns *ast.Namespace) bool {
			for _, decl := range ns.Decls {
				names := ast.DeclNames(decl)
				if len(names) == 0 || names[0] != want {
					continue
				}
				cls, ok := decl.(*ast.ClassDecl)
				if !ok {
					continue
				}
				for _, elem := range cls.Body {
					envs := MemberEnvsFromLibs(mod, elem, sourceFiles)
					name := classElemLabel(elem)
					if envs == nil {
						out[name] = append(out[name], "everywhere")
						continue
					}
					out[name] = append(out[name], printEnvs(envs))
				}
			}
			return true
		})
	}
	return out
}

// A member one lib declares is narrowed to that lib's environments, and a
// member both declare is available in both.
//
// `Performance` is the case #1613 named: a window has `timing`, `navigation`
// and `eventCounts`, and every other member is shared. Reading which lib
// declared each member is what tells the two apart, and the union over the
// files that declared it is the answer.
func TestMemberEnvsFromLibs_SeparatesAWindowMemberFromASharedOne(t *testing.T) {
	mods, sourceFiles := convertBothWebLibs(t)
	envs := memberEnvNames(t, mods, sourceFiles, "Performance")
	require.NotEmpty(t, envs, "Performance converted to no class")

	everywhere := "window, dedicated_worker, shared_worker, service_worker"
	for _, name := range []string{"timing", "navigation", "eventCounts"} {
		require.Equal(t, []string{"window"}, envs[name], "%s is declared by lib.dom alone", name)
	}
	for _, name := range []string{"now", "mark", "measure", "timeOrigin"} {
		require.Equal(t, []string{everywhere}, envs[name], "%s is declared by both libs", name)
	}
}

// The same reading over a class TypeScript builds from a trio, where the
// members arrive through fusion rather than through the interface merge.
//
// `URL.createObjectURL` is the window's `Blob | MediaSource` overload against
// the worker's `Blob` one, so the two signatures are distinct members and only
// the wider one is a window's.
func TestMemberEnvsFromLibs_ReachesAFusedClass(t *testing.T) {
	mods, sourceFiles := convertBothWebLibs(t)
	envs := memberEnvNames(t, mods, sourceFiles, "URL")
	require.NotEmpty(t, envs, "URL converted to no class")

	// `createObjectURL` is an overload set after the merge: the window's
	// `Blob | MediaSource` signature and the worker's `Blob` one are distinct
	// members, so each takes the environments of the lib that declared it.
	overloads := envs["createObjectURL"]
	sort.Strings(overloads)
	require.Equal(t, []string{
		"dedicated_worker, shared_worker, service_worker",
		"window",
	}, overloads)

	// A static both libs declare identically stays available in both.
	require.Equal(t,
		[]string{"window, dedicated_worker, shared_worker, service_worker"},
		envs["revokeObjectURL"])
}

// Most of the web surface is shared, so a reading that marked it all as a
// window's would be wrong in the direction that matters: it would hide the
// whole tree from a worker.
func TestMemberEnvsFromLibs_MostSharedMembersAreNotNarrowed(t *testing.T) {
	mods, sourceFiles := convertBothWebLibs(t)

	var windowOnly, shared, everywhere int
	for _, mod := range mods {
		mod.Module.Namespaces.Scan(func(_ string, ns *ast.Namespace) bool {
			for _, decl := range ns.Decls {
				cls, ok := decl.(*ast.ClassDecl)
				if !ok {
					continue
				}
				for _, elem := range cls.Body {
					switch envs := MemberEnvsFromLibs(mod, elem, sourceFiles); {
					case envs == nil:
						everywhere++
					case envs.Len() == 1 && envs.Contains(EnvWindow):
						windowOnly++
					default:
						shared++
					}
				}
			}
			return true
		})
	}

	require.Positive(t, shared, "no member reads as shared")
	require.Positive(t, windowOnly, "no member reads as a window's alone")
	t.Logf("window-only %d, shared %d, everywhere %d", windowOnly, shared, everywhere)
}

// The lib set's reading and the package table disagree, and the gap is the work
// #1633 has to reconcile.
//
// packageEnvs answers per package and says every `web:*` package a browser
// carries is a window's, which its own comment records as being for want of a
// source. The lib set is that source, and it says otherwise for a quarter of
// the members: `web:webgl` is largely worker-available through an
// `OffscreenCanvas`, and `web:indexeddb` wholly so.
//
// Pinning the number keeps the gap from moving unnoticed while the two readings
// coexist.
func TestTheLibReadingDisagreesWithThePackageTable(t *testing.T) {
	mods, sourceFiles := convertBothWebLibs(t)

	var read, outside int
	for uri, mod := range mods {
		declared := PackageDeclEnvs(uri, "")
		mod.Module.Namespaces.Scan(func(_ string, ns *ast.Namespace) bool {
			for _, decl := range ns.Decls {
				cls, ok := decl.(*ast.ClassDecl)
				if !ok {
					continue
				}
				for _, elem := range cls.Body {
					envs := MemberEnvsFromLibs(mod, elem, sourceFiles)
					if envs == nil {
						continue
					}
					read++
					if envs.Difference(declared).Len() > 0 {
						outside++
					}
				}
			}
			return true
		})
	}

	require.Equal(t, 7762, read, "members the lib set answers for")
	require.Equal(t, 2058, outside,
		"members available outside the environments their package claims")
}
