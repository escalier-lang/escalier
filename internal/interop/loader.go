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

// DepInfo describes one dependency that may carry overrides. `Dir` is
// the directory containing that dep's package.json — `Dir`/overrides
// is scanned recursively for .esc files.
type DepInfo struct {
	Name string
	Dir  string
}

// ParsedOverride is a single override .esc file after parsing.
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

// Discover walks the three override locations (shipped FS, deps, user
// project) and returns the parsed override files grouped by tier.
//
// Parse errors are returned alongside any successfully-parsed files —
// the caller decides whether to proceed.
func Discover(
	ctx context.Context,
	root string,
	deps []DepInfo,
	shipped fs.FS,
) ([]*ParsedOverride, []error) {
	var (
		out  []*ParsedOverride
		errs []error
	)

	if shipped != nil {
		shippedFiles, sErrs := parseFromFS(ctx, shipped, ".", "shipped:/", OverrideTierShipped)
		out = append(out, shippedFiles...)
		errs = append(errs, sErrs...)
	}

	for _, dep := range deps {
		depRoot := filepath.Join(dep.Dir, "overrides")
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
		mod, pErrs := parser.ParseLibFiles(ctx, []*ast.Source{src})
		if len(pErrs) > 0 {
			for _, pe := range pErrs {
				errs = append(errs, fmt.Errorf("parsing %s: %s", fullPath, pe.String()))
			}
			return nil
		}
		decls := collectDecls(mod)
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
// provided `tc` over each to obtain typed namespaces, extract scope
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
	tc TypeChecker,
	root string,
	deps []DepInfo,
	shipped fs.FS,
	originals map[string]*ModuleScope,
) (*OverrideStore, []error) {
	files, errs := Discover(ctx, root, deps, shipped)

	// Per-tier per-file extraction → one scope map per tier.
	tierMaps := map[OverrideTier]map[string]*ModuleScope{
		OverrideTierUserProject: {},
		OverrideTierUserDep:     {},
		OverrideTierShipped:     {},
	}

	if tc == nil && len(files) > 0 {
		errs = append(errs, fmt.Errorf("interop.Build: nil TypeChecker, cannot type-check %d override file(s)", len(files)))
		return NewOverrideStore(), errs
	}

	for _, f := range files {
		globalNs, namedNs, tcErrs := tc(f)
		errs = append(errs, tcErrs...)
		contributions := Extract(f.Decls, globalNs, namedNs, f.FilePath, f.Tier)
		mergeWithinTier(tierMaps[f.Tier], contributions, &errs)
	}

	collapsed := Collapse(
		[]map[string]*ModuleScope{
			tierMaps[OverrideTierUserProject],
			tierMaps[OverrideTierUserDep],
			tierMaps[OverrideTierShipped],
		},
		[]OverrideTier{
			OverrideTierUserProject,
			OverrideTierUserDep,
			OverrideTierShipped,
		},
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
		if srcMod.AllPure {
			dstMod.AllPure = true
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
				First:  firstOrigin(existing.Provenance),
				Second: firstOrigin(eff.Provenance),
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
		mergeWithinContainer(&existing.Container, &child.Container, appendChild(owner, name), errs)
		// Adoption happens after the merge: when existing's shape is
		// nil, mergeWithinMemberSet no-ops and we then take child's set
		// wholesale. Hoisting the adoption would alias existing and
		// child onto the same map and trigger spurious duplicate-member
		// errors in the call below.
		mergeWithinMemberSet(existing.Instance, child.Instance, appendChild(owner, name), false, errs)
		mergeWithinMemberSet(existing.Static, child.Static, appendChild(owner, name), true, errs)
		if existing.Instance == nil && child.Instance != nil {
			existing.Instance = child.Instance
		}
		if existing.Static == nil && child.Static != nil {
			existing.Static = child.Static
		}
	}
}

func mergeWithinMemberSet(dst, src *MemberSet, owner Path, static bool, errs *[]error) {
	if src == nil || dst == nil {
		return
	}
	for _, pair := range []struct {
		dst, src map[string]*Effective
		kind     MemberKind
	}{
		{dst.Methods, src.Methods, KindMethod},
		{dst.Getters, src.Getters, KindGetter},
		{dst.Setters, src.Setters, KindSetter},
		{dst.Properties, src.Properties, KindProperty},
	} {
		for name, eff := range pair.src {
			if existing, present := pair.dst[name]; present {
				p := owner
				p.Kind = pair.kind
				p.Static = static
				*errs = append(*errs, &ErrDuplicateMember{
					Path:   p,
					First:  firstOrigin(existing.Provenance),
					Second: firstOrigin(eff.Provenance),
				})
				continue
			}
			pair.dst[name] = eff
		}
	}
	if dst.Ctor == nil && src.Ctor != nil {
		dst.Ctor = src.Ctor
	} else if src.Ctor != nil {
		p := owner
		p.Kind = KindCtor
		p.Static = static
		*errs = append(*errs, &ErrDuplicateMember{
			Path:   p,
			First:  firstOrigin(dst.Ctor.Provenance),
			Second: firstOrigin(src.Ctor.Provenance),
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

func appendChild(owner Path, name string) Path {
	cp := owner
	cp.Owner = appendOwner(owner.Owner, name)
	return cp
}

// collectDecls flattens all declarations across all namespaces of a
// parsed module. Override files are single-file modules, so namespace
// grouping (by directory) is incidental — the override format treats
// the file's top-level decls as a flat list.
func collectDecls(m *ast.Module) []ast.Decl {
	if m == nil {
		return nil
	}
	var out []ast.Decl
	iter := m.Namespaces.Iter()
	for ok := iter.First(); ok; ok = iter.Next() {
		ns := iter.Value()
		if ns == nil {
			continue
		}
		out = append(out, ns.Decls...)
	}
	return out
}

func firstOrigin(ps []Origin) Origin {
	if len(ps) == 0 {
		return Origin{}
	}
	return ps[0]
}
