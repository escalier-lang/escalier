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
)

const declareFooFile = `override declare global {
  declare fn foo() -> number
}
`

func TestDiscoverGroupsByTier(t *testing.T) {
	shipped := fstest.MapFS{
		"core.esc": &fstest.MapFile{Data: []byte(declareFooFile)},
	}

	userRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(userRoot, "overrides"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(userRoot, "overrides", "user.esc"),
		[]byte(declareFooFile), 0o644,
	); err != nil {
		t.Fatalf("write user override: %v", err)
	}

	depDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(depDir, "overrides"), 0o755); err != nil {
		t.Fatalf("mkdir dep: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(depDir, "overrides", "dep.esc"),
		[]byte(declareFooFile), 0o644,
	); err != nil {
		t.Fatalf("write dep override: %v", err)
	}

	files, errs := Discover(
		context.Background(),
		userRoot,
		[]DepInfo{{Name: "lib", Dir: depDir}},
		shipped,
	)
	if len(errs) > 0 {
		t.Fatalf("unexpected discover errors: %v", errs)
	}

	tierByName := map[string]OverrideTier{}
	for _, f := range files {
		base := filepath.Base(f.FilePath)
		tierByName[base] = f.Tier
	}
	if tierByName["core.esc"] != OverrideTierShipped {
		t.Fatalf("expected core.esc tagged Shipped; got %v", tierByName["core.esc"])
	}
	if tierByName["dep.esc"] != OverrideTierUserDep {
		t.Fatalf("expected dep.esc tagged UserDep; got %v", tierByName["dep.esc"])
	}
	if tierByName["user.esc"] != OverrideTierUserProject {
		t.Fatalf("expected user.esc tagged UserProject; got %v", tierByName["user.esc"])
	}
}

func TestDiscoverMissingOverrideDirsIsNotAnError(t *testing.T) {
	// Empty root (no overrides/ subdir) and no deps should yield no
	// files and no errors.
	root := t.TempDir()
	files, errs := Discover(context.Background(), root, nil, nil)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(files) != 0 {
		t.Fatalf("expected zero files; got %d", len(files))
	}
}

func TestDiscoverReportsParseErrors(t *testing.T) {
	// Source with a clear syntax error.
	shipped := fstest.MapFS{
		"broken.esc": &fstest.MapFile{Data: []byte("declare global { @@@ }\n")},
	}
	files, errs := Discover(context.Background(), "", nil, shipped)
	if len(errs) == 0 {
		t.Fatalf("expected parse error to surface; got none")
	}
	for _, f := range files {
		if strings.Contains(f.FilePath, "broken.esc") {
			t.Fatalf("the broken file should not be in the parsed set; got %s", f.FilePath)
		}
	}
}

func TestBuildPipelineWithStubChecker(t *testing.T) {
	// Drive Build end-to-end with a stub TypeChecker that returns a
	// pre-built namespace. We assert that the final OverrideStore
	// reflects the contribution.
	shipped := fstest.MapFS{
		"core.esc": &fstest.MapFile{Data: []byte(declareFooFile)},
	}

	fn := type_system.NewFuncType(nil, nil, nil, type_system.NewNumPrimType(nil), nil)
	tc := func(p *ParsedOverride) (*type_system.Namespace, map[string]*type_system.Namespace, []error) {
		globalNs := type_system.NewNamespace()
		globalNs.Values["foo"] = &type_system.Binding{Type: fn}
		return globalNs, nil, nil
	}

	store, errs := Build(context.Background(), tc, "", nil, shipped, nil)
	if len(errs) > 0 {
		t.Fatalf("unexpected build errors: %v", errs)
	}
	mod := store.Modules[""]
	if mod == nil {
		t.Fatalf("expected global module scope")
	}
	eff := mod.Free["foo"]
	if eff == nil || eff.Type != fn {
		t.Fatalf("expected merged store to carry shipped foo; got %#v", eff)
	}
	if eff.Source != TierShippedOverride {
		t.Fatalf("expected Source=TierShippedOverride; got %v", eff.Source)
	}
}

func TestBuildWithoutTypeCheckerErrorsWhenFilesPresent(t *testing.T) {
	shipped := fstest.MapFS{
		"core.esc": &fstest.MapFile{Data: []byte(declareFooFile)},
	}
	_, errs := Build(context.Background(), nil, "", nil, shipped, nil)
	if len(errs) == 0 {
		t.Fatalf("expected an error when TypeChecker is nil and files exist")
	}
}

func TestBuildPrecedenceUserProjectBeatsShipped(t *testing.T) {
	shipped := fstest.MapFS{
		"core.esc": &fstest.MapFile{Data: []byte(declareFooFile)},
	}
	userRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(userRoot, "overrides"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(userRoot, "overrides", "user.esc"),
		[]byte(declareFooFile), 0o644,
	); err != nil {
		t.Fatalf("write: %v", err)
	}

	fnShipped := type_system.NewFuncType(nil, nil, nil, type_system.NewNumPrimType(nil), nil)
	fnUser := type_system.NewFuncType(nil, nil, nil, type_system.NewStrPrimType(nil), nil)
	tc := func(p *ParsedOverride) (*type_system.Namespace, map[string]*type_system.Namespace, []error) {
		ns := type_system.NewNamespace()
		fn := fnShipped
		if p.Tier == OverrideTierUserProject {
			fn = fnUser
		}
		ns.Values["foo"] = &type_system.Binding{Type: fn}
		return ns, nil, nil
	}

	store, errs := Build(context.Background(), tc, userRoot, nil, shipped, nil)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	eff := store.Modules[""].Free["foo"]
	if eff == nil || eff.Type != fnUser {
		t.Fatalf("expected user-project to win over shipped; got %#v", eff)
	}
	if eff.Source != TierUserOverride {
		t.Fatalf("expected TierUserOverride; got %v", eff.Source)
	}
}

func TestBuildDuplicateWithinTierIsAnError(t *testing.T) {
	// Two files at the same tier that contribute the same name to the
	// same module slot → ErrDuplicateMember.
	shipped := fstest.MapFS{
		"a.esc": &fstest.MapFile{Data: []byte(declareFooFile)},
		"b.esc": &fstest.MapFile{Data: []byte(declareFooFile)},
	}
	fn := type_system.NewFuncType(nil, nil, nil, type_system.NewNumPrimType(nil), nil)
	tc := func(p *ParsedOverride) (*type_system.Namespace, map[string]*type_system.Namespace, []error) {
		ns := type_system.NewNamespace()
		ns.Values["foo"] = &type_system.Binding{Type: fn}
		return ns, nil, nil
	}
	_, errs := Build(context.Background(), tc, "", nil, shipped, nil)
	sawDup := false
	for _, e := range errs {
		if _, ok := e.(*ErrDuplicateMember); ok {
			sawDup = true
			break
		}
	}
	if !sawDup {
		t.Fatalf("expected ErrDuplicateMember in errors; got %v", errs)
	}
}

// Ensure the parser is actually reading our content (vs silently
// returning an empty Decls slice that the rest of the test would also
// pass on).
func TestDiscoverActuallyParsesDecls(t *testing.T) {
	shipped := fstest.MapFS{
		"core.esc": &fstest.MapFile{Data: []byte(declareFooFile)},
	}
	files, errs := Discover(context.Background(), "", nil, shipped)
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file; got %d", len(files))
	}
	if len(files[0].Decls) == 0 {
		t.Fatalf("expected at least one decl; got 0")
	}
	if _, ok := files[0].Decls[0].(*ast.DeclareGlobalDecl); !ok {
		t.Fatalf("expected top decl to be DeclareGlobalDecl; got %T", files[0].Decls[0])
	}
}
