package interop

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/type_system"
	"github.com/stretchr/testify/require"
)

const declareFooFile = `override declare global {
  declare fn foo() -> number
}
`

func TestDiscoverGroupsByTier(t *testing.T) {
	// Mirror the on-disk layout an Escalier package + its installed deps
	// would actually have:
	//
	//   userRoot/
	//     overrides/user.esc
	//     node_modules/lib/
	//       overrides/dep.esc
	builtin := fstest.MapFS{
		"core.esc": &fstest.MapFile{Data: []byte(declareFooFile)},
	}

	userRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(userRoot, "overrides"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(userRoot, "overrides", "user.esc"),
		[]byte(declareFooFile), 0o644,
	))

	depDir := filepath.Join(userRoot, "node_modules", "lib")
	require.NoError(t, os.MkdirAll(filepath.Join(depDir, "overrides"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(depDir, "overrides", "dep.esc"),
		[]byte(declareFooFile), 0o644,
	))

	files, errs := Discover(
		context.Background(),
		userRoot,
		[]DepInfo{{Name: "lib", PkgDir: depDir}},
		builtin,
	)
	require.Empty(t, errs)

	tierByName := map[string]OverrideTier{}
	for _, f := range files {
		base := filepath.Base(f.FilePath)
		tierByName[base] = f.Tier
	}
	require.Equal(t, OverrideTierBuiltin, tierByName["core.esc"])
	require.Equal(t, OverrideTierUserDep, tierByName["dep.esc"])
	require.Equal(t, OverrideTierUserProject, tierByName["user.esc"])
}

func TestDiscoverMissingOverrideDirsIsNotAnError(t *testing.T) {
	// Empty root (no overrides/ subdir) and no deps should yield no
	// files and no errors.
	root := t.TempDir()
	files, errs := Discover(context.Background(), root, nil, nil)
	require.Empty(t, errs)
	require.Empty(t, files)
}

func TestDiscoverReportsParseErrors(t *testing.T) {
	// Source with a clear syntax error: three `@` tokens the parser
	// can't recognise, at columns 18, 19, 20 of the only line.
	builtin := fstest.MapFS{
		"broken.esc": &fstest.MapFile{Data: []byte("declare global { @@@ }\n")},
	}
	files, errs := Discover(context.Background(), "", nil, builtin)
	require.Empty(t, files, "the broken file must not produce a ParsedOverride")
	require.Len(t, errs, 3)
	require.Equal(t, "parsing builtin:/broken.esc: 1:18-1:19: Unexpected token", errs[0].Error())
	require.Equal(t, "parsing builtin:/broken.esc: 1:19-1:20: Unexpected token", errs[1].Error())
	require.Equal(t, "parsing builtin:/broken.esc: 1:20-1:21: Unexpected token", errs[2].Error())
}

func TestBuildPipelineWithStubChecker(t *testing.T) {
	// Drive Build end-to-end with a stub TypeChecker that returns a
	// pre-built namespace. We assert that the final OverrideStore
	// reflects the contribution.
	builtin := fstest.MapFS{
		"core.esc": &fstest.MapFile{Data: []byte(declareFooFile)},
	}

	// Override-side type for global `foo`: fn() -> number. The stub
	// checker hands this pointer back for every parsed override, so it
	// is what should land in the store and what require.Same verifies.
	fn := type_system.NewFuncType(nil, nil, nil, type_system.NewNumPrimType(nil), nil)
	checker := func(p *ParsedOverride) (*type_system.Namespace, map[string]*type_system.Namespace, []error) {
		globalNs := type_system.NewNamespace()
		globalNs.Values["foo"] = &type_system.Binding{Type: fn}
		return globalNs, nil, nil
	}

	// Pre-load an original-side "foo" matching the override; without
	// one Merge would (correctly) flag the override as unknown.
	originals := map[string]*ModuleScope{
		"": {
			Container: Container{
				Free: map[string]*Effective{
					// Original-side type for global `foo`: fn() -> number.
					// Same shape as the override so the consistency check
					// passes; pointer identity is unimportant here.
					"foo": {Type: type_system.NewFuncType(nil, nil, nil, type_system.NewNumPrimType(nil), nil)},
				},
				Children: map[string]ChildScope{},
			},
		},
	}
	store, errs := Build(context.Background(), checker, "", nil, builtin, originals)
	require.Empty(t, errs)
	mod := store.Modules[""]
	require.NotNil(t, mod, "expected global module scope")
	eff := mod.Free["foo"]
	require.NotNil(t, eff)
	require.Same(t, fn, eff.Type)
	require.Equal(t, TierBuiltinOverride, eff.Source)
}

func TestBuildWithoutTypeCheckerErrorsWhenFilesPresent(t *testing.T) {
	builtin := fstest.MapFS{
		"core.esc": &fstest.MapFile{Data: []byte(declareFooFile)},
	}
	_, errs := Build(context.Background(), nil, "", nil, builtin, nil)
	require.NotEmpty(t, errs, "expected an error when TypeChecker is nil and files exist")
}

func TestBuildPrecedenceUserProjectBeatsBuiltin(t *testing.T) {
	builtin := fstest.MapFS{
		"core.esc": &fstest.MapFile{Data: []byte(declareFooFile)},
	}
	userRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(userRoot, "overrides"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(userRoot, "overrides", "user.esc"),
		[]byte(declareFooFile), 0o644,
	))

	// All three FuncTypes share the same shape — `fn() -> number` —
	// so the merge's signature-consistency check passes. Precedence is
	// asserted via pointer identity, not signature content.
	//
	// Original-side type for global `foo`.
	fnOrig := type_system.NewFuncType(nil, nil, nil, type_system.NewNumPrimType(nil), nil)
	// Builtin-tier override type, returned by the stub checker when it
	// sees core.esc.
	fnBuiltin := type_system.NewFuncType(nil, nil, nil, type_system.NewNumPrimType(nil), nil)
	// User-project-tier override type — expected winner since
	// UserProject outranks Builtin.
	fnUser := type_system.NewFuncType(nil, nil, nil, type_system.NewNumPrimType(nil), nil)
	tc := func(p *ParsedOverride) (*type_system.Namespace, map[string]*type_system.Namespace, []error) {
		ns := type_system.NewNamespace()
		fn := fnBuiltin
		if p.Tier == OverrideTierUserProject {
			fn = fnUser
		}
		ns.Values["foo"] = &type_system.Binding{Type: fn}
		return ns, nil, nil
	}

	// Pre-load an original-side "foo" so the override targets a known
	// symbol; the test's focus is tier precedence, not the unknown-
	// member behavior.
	originals := map[string]*ModuleScope{
		"": {
			Container: Container{
				Free:     map[string]*Effective{"foo": {Type: fnOrig}},
				Children: map[string]ChildScope{},
			},
		},
	}
	store, errs := Build(context.Background(), tc, userRoot, nil, builtin, originals)
	require.Empty(t, errs)
	eff := store.Modules[""].Free["foo"]
	require.NotNil(t, eff)
	require.Same(t, fnUser, eff.Type)
	require.Equal(t, TierUserOverride, eff.Source)
}

func TestBuildDuplicateWithinTierIsAnError(t *testing.T) {
	// Two files at the same tier that contribute the same name to the
	// same module slot → ErrDuplicateMember.
	builtin := fstest.MapFS{
		"a.esc": &fstest.MapFile{Data: []byte(declareFooFile)},
		"b.esc": &fstest.MapFile{Data: []byte(declareFooFile)},
	}
	// Shared override type returned for both a.esc and b.esc, both
	// claiming the same global `foo : fn() -> number` slot — the
	// collision is what the test exercises.
	fn := type_system.NewFuncType(nil, nil, nil, type_system.NewNumPrimType(nil), nil)
	tc := func(p *ParsedOverride) (*type_system.Namespace, map[string]*type_system.Namespace, []error) {
		ns := type_system.NewNamespace()
		ns.Values["foo"] = &type_system.Binding{Type: fn}
		return ns, nil, nil
	}
	_, errs := Build(context.Background(), tc, "", nil, builtin, nil)
	sawDup := false
	for _, e := range errs {
		if _, ok := e.(*ErrDuplicateMember); ok {
			sawDup = true
			break
		}
	}
	require.True(t, sawDup, "expected ErrDuplicateMember in errors; got %v", errs)
}

// TestDiscoverHonorsCanceledContext: a canceled context aborts the walk
// before parsing files, so no overrides come back. We can't easily
// observe the cancellation error without per-file synthesis, but the
// returned file set should be empty (cancellation short-circuits).
func TestDiscoverHonorsCanceledContext(t *testing.T) {
	builtin := fstest.MapFS{
		"a.esc": &fstest.MapFile{Data: []byte(declareFooFile)},
		"b.esc": &fstest.MapFile{Data: []byte(declareFooFile)},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	files, errs := Discover(ctx, "", nil, builtin)
	require.Empty(t, files, "canceled context must not yield parsed files")
	require.NotEmpty(t, errs, "canceled context should surface ctx.Err()")
	sawCanceled := false
	for _, e := range errs {
		if strings.Contains(e.Error(), context.Canceled.Error()) {
			sawCanceled = true
			break
		}
	}
	require.True(t, sawCanceled, "expected a context.Canceled error; got %v", errs)
}

// Ensure the parser is actually reading our content (vs silently
// returning an empty Decls slice that the rest of the test would also
// pass on).
func TestDiscoverActuallyParsesDecls(t *testing.T) {
	builtin := fstest.MapFS{
		"core.esc": &fstest.MapFile{Data: []byte(declareFooFile)},
	}
	files, errs := Discover(context.Background(), "", nil, builtin)
	require.Empty(t, errs)
	require.Len(t, files, 1)
	require.NotEmpty(t, files[0].Decls)
	_, ok := files[0].Decls[0].(*ast.DeclareGlobalDecl)
	require.True(t, ok, "expected top decl to be DeclareGlobalDecl; got %T", files[0].Decls[0])
}
