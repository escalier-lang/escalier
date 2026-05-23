package interop

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dts_parser"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// DepInfo describes one dependency that may carry overrides. `PkgDir`
// is the directory containing that dep's package.json — `PkgDir`/overrides
// is scanned recursively for .esc files.
type DepInfo struct {
	Name   string
	PkgDir string
}

// ParsedOverride is a single override .esc file after parsing.
//
// Decls is the list of top-level declarations as produced by
// parser.ParseDecls. Per the override grammar
// (planning/interop_mutability/implementation_plan.md §2.2), each entry
// is either a *ast.DeclareGlobalDecl (`override declare global { ... }`)
// or a *ast.DeclareModuleDecl (`override declare module "name" { ... }`),
// in both cases with Override() == true. A single file may contain any
// number of these blocks in any order; Extract routes each into its own
// scope bucket keyed by module name ("" for global). Lexical
// `namespace Foo { ... }` nesting inside a block is preserved by
// Extract — it is not flattened here.
type ParsedOverride struct {
	Tier     OverrideTier
	FilePath string
	Source   *ast.Source
	Decls    []ast.Decl
}

// TypeChecker is the dependency injection seam used by Build to invoke
// the checker on each parsed override file. It returns the
// post-inference namespace for the file's global block (nil when the
// file has no `override declare global { ... }`) plus a map of
// per-module namespaces for `override declare module "name" { ... }`
// blocks. Errors from inference flow back through the returned []error.
//
// The caller is responsible for constructing a checker whose
// surrounding scope already contains the original .d.ts symbols the
// override references (the §5.2 sequencing constraint).
type TypeChecker func(p *ParsedOverride) (
	globalNs *type_system.Namespace,
	namedNs map[string]*type_system.Namespace,
	errs []error,
)

// Discover walks the three override locations (builtin FS, deps, user
// project) and returns the parsed override files grouped by tier.
//
// Parse errors are returned alongside any successfully-parsed files —
// the caller decides whether to proceed.
func Discover(
	ctx context.Context,
	root string,
	deps []DepInfo,
	builtin fs.FS,
) ([]*ParsedOverride, []error) {
	var (
		out  []*ParsedOverride
		errs []error
	)

	if builtin != nil {
		builtinFiles, sErrs := parseFromFS(ctx, builtin, ".", "builtin:/", OverrideTierBuiltin)
		out = append(out, builtinFiles...)
		errs = append(errs, sErrs...)
	}

	for _, dep := range deps {
		depRoot := filepath.Join(dep.PkgDir, "overrides")
		if !dirExists(depRoot) {
			continue
		}
		depFS := os.DirFS(depRoot)
		depFiles, dErrs := parseFromFS(ctx, depFS, ".", depRoot+"/", OverrideTierUserDep)
		out = append(out, depFiles...)
		errs = append(errs, dErrs...)
	}

	if root != "" {
		userRoot := filepath.Join(root, "overrides")
		if dirExists(userRoot) {
			userFS := os.DirFS(userRoot)
			userFiles, uErrs := parseFromFS(ctx, userFS, ".", userRoot+"/", OverrideTierUserProject)
			out = append(out, userFiles...)
			errs = append(errs, uErrs...)
		}
	}
	return out, errs
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// parseFromFS recursively walks `fsys` from `root`, parsing every
// `.esc` file as an Escalier module and tagging it with `tier`. The
// `pathPrefix` is prepended to file paths in returned errors so a
// reader can locate the file on disk (the embedded fs has no
// real-disk path of its own).
func parseFromFS(
	ctx context.Context,
	fsys fs.FS,
	root, pathPrefix string,
	tier OverrideTier,
) ([]*ParsedOverride, []error) {
	var (
		out  []*ParsedOverride
		errs []error
	)
	walkErr := fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
		if cErr := ctx.Err(); cErr != nil {
			return cErr
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("walking %s%s: %w", pathPrefix, p, err))
			return nil
		}
		if d.IsDir() {
			// The shared `internal/interop/data/` parent holds both
			// override subtrees (builtins/, libs/) and the new stdlib
			// scheme subtrees (std/, web/, node/) introduced by the
			// builtins workstream. The override loader has no business
			// in the scheme subtrees, so skip them at the top level.
			if p != root && isStdlibSchemeSubtree(p, root) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".esc") {
			return nil
		}
		contents, readErr := fs.ReadFile(fsys, p)
		if readErr != nil {
			errs = append(errs, fmt.Errorf("reading %s%s: %w", pathPrefix, p, readErr))
			return nil
		}
		fullPath := pathPrefix + p
		src := &ast.Source{Path: fullPath, Contents: string(contents)}
		decls, pErrs := parser.ParseDecls(ctx, src)
		if len(pErrs) > 0 {
			for _, pe := range pErrs {
				errs = append(errs, fmt.Errorf("parsing %s: %s", fullPath, pe.String()))
			}
			return nil
		}
		out = append(out, &ParsedOverride{
			Tier:     tier,
			FilePath: fullPath,
			Source:   src,
			Decls:    decls,
		})
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, fs.ErrNotExist) {
		errs = append(errs, fmt.Errorf("walking %s: %w", pathPrefix, walkErr))
	}
	return out, errs
}

// Build is the full §5.5 pipeline: discover override files, run the
// provided `checker` over each to obtain typed namespaces, extract scope
// contributions, collapse three tiers, and merge against `originals`.
//
// `originals` is the post-inference original-side scope keyed by
// module specifier ("" = global). Pass nil for modules with no
// pre-loaded original — those overrides still produce store entries.
//
// Returns the merged store plus any errors collected from parsing,
// type-checking, or merge.
func Build(
	ctx context.Context,
	checker TypeChecker,
	root string,
	deps []DepInfo,
	builtin fs.FS,
	originals map[string]*ModuleScope,
) (*OverrideStore, []error) {
	files, errs := Discover(ctx, root, deps, builtin)

	// Per-tier per-file extraction → one scope map per tier.
	tierMaps := map[OverrideTier]map[string]*ModuleScope{
		OverrideTierUserProject: {},
		OverrideTierUserDep:     {},
		OverrideTierBuiltin:     {},
	}

	if checker == nil && len(files) > 0 {
		errs = append(errs, fmt.Errorf("interop.Build: nil TypeChecker, cannot type-check %d override file(s)", len(files)))
		return NewOverrideStore(), errs
	}

	for _, f := range files {
		globalNs, namedNs, tcErrs := checker(f)
		errs = append(errs, tcErrs...)
		// Checker errors do not short-circuit extraction: any namespaces
		// the checker did populate still contribute to the store, with
		// the errors flowing back alongside. Extract is nil-safe per
		// name, so partial namespaces just produce partial overrides.
		contributions := Extract(f.Decls, globalNs, namedNs, f.FilePath, f.Tier)
		mergeWithinTier(tierMaps[f.Tier], contributions, &errs)
	}

	collapsed := Collapse(
		tierMaps[OverrideTierUserProject],
		tierMaps[OverrideTierUserDep],
		tierMaps[OverrideTierBuiltin],
	)
	store, mErrs := Merge(originals, collapsed)
	errs = append(errs, mErrs...)
	return store, errs
}

// mergeWithinTier folds one file's contributions into the per-tier
// accumulator. A within-tier slot collision is ErrDuplicateMember.
func mergeWithinTier(
	dst, src map[string]*ModuleScope,
	errs *[]error,
) {
	for modName, srcMod := range src {
		dstMod, ok := dst[modName]
		if !ok {
			dst[modName] = srcMod
			continue
		}
		mergeWithinContainer(&dstMod.Container, &srcMod.Container, Path{Module: modName}, errs)
		// Module-level @all_pure: if either side claims it, the
		// merged scope claims it. Same-tier duplicate isn't reported —
		// the pragma is module-level metadata, not a slot.
		if srcMod.AllPure && !dstMod.AllPure {
			dstMod.AllPure = true
			dstMod.AllPureTier = srcMod.AllPureTier
		}
	}
}

func mergeWithinContainer(
	dst, src *Container,
	owner Path,
	errs *[]error,
) {
	for name, eff := range src.Free {
		if existing, present := dst.Free[name]; present {
			*errs = append(*errs, &ErrDuplicateMember{
				Path:   withFreeName(owner, name),
				First:  headOrigin(existing.Origins),
				Second: headOrigin(eff.Origins),
			})
			continue
		}
		dst.Free[name] = eff
	}
	for name, child := range src.Children {
		existing, ok := dst.Children[name]
		if !ok {
			dst.Children[name] = child
			continue
		}
		mergeWithinChildScope(existing, child, owner.withChild(name), errs)
	}
}

// mergeWithinChildScope folds `incoming` into `existing` for the
// within-tier accumulator. Same-variant merges are recursive; a
// shape mismatch (e.g. namespace + class at the same name) is reported
// as ErrShapeConflict and the first-seen variant is kept.
func mergeWithinChildScope(existing, incoming ChildScope, childOwner Path, errs *[]error) {
	switch e := existing.(type) {
	case *NamespaceScope:
		i, ok := incoming.(*NamespaceScope)
		if !ok {
			reportChildShapeConflict(existing, incoming, childOwner, errs)
			return
		}
		mergeWithinContainer(&e.Container, &i.Container, childOwner, errs)
	case *ClassScope:
		i, ok := incoming.(*ClassScope)
		if !ok {
			reportChildShapeConflict(existing, incoming, childOwner, errs)
			return
		}
		mergeWithinMemberSet(e.Instance, i.Instance, childOwner, false, errs)
		mergeWithinMemberSet(e.Static, i.Static, childOwner, true, errs)
	case *InterfaceScope:
		i, ok := incoming.(*InterfaceScope)
		if !ok {
			reportChildShapeConflict(existing, incoming, childOwner, errs)
			return
		}
		mergeWithinMemberSet(e.Instance, i.Instance, childOwner, false, errs)
	}
}

func reportChildShapeConflict(existing, incoming ChildScope, childOwner Path, errs *[]error) {
	*errs = append(*errs, &ErrShapeConflict{
		Path:   childOwner,
		First:  existing.ChildOrigin(),
		Second: incoming.ChildOrigin(),
	})
}

// mergeWithinKind folds src's entries for one MemberKind slot into dst.
// Same-name collisions are reported as ErrDuplicateMember; new entries
// move over by reference.
func mergeWithinKind(
	dst, src map[string]*Effective,
	owner Path,
	kind MemberKind,
	static bool,
	errs *[]error,
) {
	for name, eff := range src {
		if existing, present := dst[name]; present {
			p := owner
			p.Kind = kind
			p.Static = static
			*errs = append(*errs, &ErrDuplicateMember{
				Path:   p,
				First:  headOrigin(existing.Origins),
				Second: headOrigin(eff.Origins),
			})
			continue
		}
		dst[name] = eff
	}
}

func mergeWithinMemberSet(dst, src *MemberSet, owner Path, static bool, errs *[]error) {
	if src == nil || dst == nil {
		return
	}
	mergeWithinKind(dst.Methods, src.Methods, owner, KindMethod, static, errs)
	mergeWithinKind(dst.Getters, src.Getters, owner, KindGetter, static, errs)
	mergeWithinKind(dst.Setters, src.Setters, owner, KindSetter, static, errs)
	mergeWithinKind(dst.Properties, src.Properties, owner, KindProperty, static, errs)
	if dst.Ctor == nil && src.Ctor != nil {
		dst.Ctor = src.Ctor
	} else if src.Ctor != nil {
		p := owner
		p.Kind = KindCtor
		p.Static = static
		*errs = append(*errs, &ErrDuplicateMember{
			Path:   p,
			First:  headOrigin(dst.Ctor.Origins),
			Second: headOrigin(src.Ctor.Origins),
		})
	}
}

func withFreeName(owner Path, name string) Path {
	cp := owner
	cp.Kind = KindFree
	if name != "" {
		cp.Name = dts_parser.NewIdent(name, ast.Span{})
	}
	return cp
}

func headOrigin(ps []Origin) Origin {
	if len(ps) == 0 {
		return Origin{}
	}
	return ps[0]
}
