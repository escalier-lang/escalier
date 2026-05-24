package checker

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/interop"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// stdlibSchemes is the display-ordered list of URI schemes the
// resolver recognizes for pseudo-package imports. The slice form is
// used only for the "unknown scheme" diagnostic; membership tests go
// through stdlibSchemesSet.
var stdlibSchemes = []string{"std", "web", "node"}

// stdlibSchemesSet is the membership view of stdlibSchemes.
var stdlibSchemesSet = set.FromSlice(stdlibSchemes)

// stdlibKnownFlags is the recognized set of binding-shape flags.
// Per §2.3 the slot is extensible (future `?type-only`, `?lazy`, …); the
// table-driven check means new entries slot in without restructuring.
var stdlibKnownFlags = set.FromSlice([]string{"local", "nested", "flat"})

// stdlibBindingShape captures the resolved binding-shape for a single
// import statement. Exactly one of the three booleans is true.
type stdlibBindingShape struct {
	local, nested, flat bool
}

// isSchemePrefixedImport reports whether spec begins with one of the
// recognized scheme prefixes (`std:`, `web:`, `node:`) or any other
// lowercase `<word>:` shape. Anything matching the second branch but
// not the first is routed to the stdlib loader so the user gets the
// taxonomy-aligned "unknown scheme" diagnostic rather than the npm
// loader's "could not find package.json".
func isSchemePrefixedImport(spec string) bool {
	colon := strings.IndexByte(spec, ':')
	if colon <= 0 {
		return false
	}
	scheme := spec[:colon]
	return stdlibSchemesSet.Contains(scheme) || isASCIILower(scheme)
}

func isASCIILower(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 'a' || c > 'z' {
			return false
		}
	}
	return true
}

// inferStdlibImport handles `import "<scheme>:<pkg>"` resolution and
// binding. Mirrors inferImport's contract: returns the diagnostics for
// the import statement, side-effecting bindings into ctx.Scope.
func (c *Checker) inferStdlibImport(ctx Context, importStmt *ast.ImportStmt) []Error {
	span := importStmt.Span()
	var errs []Error

	// --- Validation phase. Each check is independent and accumulates,
	// so a malformed import surfaces every problem at once rather than
	// forcing the user through fix-recompile-repeat.

	scheme, pkg, hasColon := splitScheme(importStmt.PackageName)
	schemeKnown := hasColon && isRecognizedScheme(scheme)
	switch {
	case !schemeKnown:
		errs = append(errs, &GenericError{
			message: fmt.Sprintf("unknown import scheme %q; recognized schemes: %s",
				scheme, strings.Join(stdlibSchemes, ", ")),
			span: span,
		})
	case pkg == "":
		errs = append(errs, &GenericError{
			message: fmt.Sprintf("missing package name after %q scheme", scheme),
			span:    span,
		})
	}

	// Named imports from scheme-prefixed URIs are rejected per FR4 /
	// "Named import from a pseudo-package URI" in the error taxonomy.
	if !importStmt.Bare() {
		errs = append(errs, &GenericError{
			message: fmt.Sprintf(
				"named imports from pseudo-package %q are not supported; "+
					"use a bare-string import (`import %q`) and access members through the namespace",
				importStmt.PackageName, importStmt.PackageName),
			span: span,
		})
	}

	shape, flagErrs := resolveStdlibFlags(importStmt.Flags, span)
	errs = append(errs, flagErrs...)

	// node:* is reserved; the resolver rejects every package until Node
	// support lands. Gated on schemeKnown so an unknown-scheme URI
	// doesn't also pretend to be a "node:* reserved" case.
	if schemeKnown && scheme == "node" {
		errs = append(errs, &GenericError{
			message: fmt.Sprintf("%q: node:* is reserved; not yet populated", importStmt.PackageName),
			span:    span,
		})
	}

	// Load+bind require a clean validation pass: we need a usable
	// scheme/pkg pair, a non-rejected import form, and a resolved
	// binding shape.
	if len(errs) > 0 {
		return errs
	}

	// Intra-SCC short-circuit. When we're inside a merged-SCC load and
	// the import targets another member of the same SCC, the merged
	// module's namespace tree already exposes that member at its
	// derived `<scheme>.<pkg>` path through the module-scope namespace.
	// A file-scope bind here would just shadow that live, populating
	// namespace with an empty filtered copy because the target package
	// hasn't gone through its placeholder phase yet.
	if c.activeSCC[importStmt.PackageName] {
		return errs
	}

	// --- Resolution + load phase. ---

	filePath, resolveErrs := c.resolveStdlibPath(scheme, pkg, span)
	if len(resolveErrs) > 0 {
		errs = append(errs, resolveErrs...)
		return errs
	}

	pkgNs, loadErrs := c.loadStdlibPackage(importStmt.PackageName, filePath, span)
	errs = append(errs, loadErrs...)
	if pkgNs == nil {
		// Either a load error already accumulated above, or an
		// in-progress sentinel (cycle); cycles are permitted per §4.
		return errs
	}

	// --- Bind phase. Single-class shortcut applies only to ?local
	// (per §2.4 last bullet).

	var bindErrs []Error

	switch {
	case shape.local:
		bindErrs = c.bindStdlibLocal(ctx, pkg, pkgNs, span)
	case shape.nested:
		bindErrs = c.bindStdlibNested(ctx, scheme, pkg, pkgNs, span)
	case shape.flat:
		bindErrs = c.bindStdlibFlat(ctx, scheme, importStmt.PackageName, pkgNs, span)
		// default: unreachable — resolveStdlibFlags always sets exactly
		// one shape.
	}

	errs = append(errs, bindErrs...)

	return errs
}

// splitScheme cracks `scheme:pkg` into its parts. Returns ok=false if
// there's no colon. The pkg portion may still be empty (caller's job
// to flag).
func splitScheme(spec string) (scheme, pkg string, ok bool) {
	idx := strings.IndexByte(spec, ':')
	if idx <= 0 {
		return "", "", false
	}
	return spec[:idx], spec[idx+1:], true
}

func isRecognizedScheme(scheme string) bool {
	return stdlibSchemesSet.Contains(scheme)
}

// resolveStdlibFlags inspects the parsed flag list and returns the
// binding shape, defaulting to ?local. Reports unknown-flag,
// mutually-exclusive, and duplicate-flag diagnostics per the taxonomy.
func resolveStdlibFlags(flags []string, span ast.Span) (stdlibBindingShape, []Error) {
	shape := stdlibBindingShape{}
	if len(flags) == 0 {
		shape.local = true
		return shape, nil
	}

	seen := set.NewSet[string]()
	shapeFlags := []string{}
	var errs []Error
	for _, f := range flags {
		if f == "" {
			errs = append(errs, &GenericError{
				message: "empty flag in import specifier", span: span,
			})
			continue
		}
		if !stdlibKnownFlags.Contains(f) {
			recognized := stdlibKnownFlags.ToSlice()
			sort.Strings(recognized)
			errs = append(errs, &GenericError{
				message: fmt.Sprintf("unknown import flag %q; recognized flags: %s",
					f, strings.Join(recognized, ", ")),
				span: span,
			})
			continue
		}
		if seen.Contains(f) {
			errs = append(errs, &GenericError{
				message: fmt.Sprintf("duplicate import flag %q", f), span: span,
			})
			continue
		}
		seen.Add(f)
		if f == "local" || f == "nested" || f == "flat" {
			shapeFlags = append(shapeFlags, f)
		}
	}
	if len(errs) > 0 {
		return shape, errs
	}

	if len(shapeFlags) > 1 {
		sort.Strings(shapeFlags)
		return shape, []Error{&GenericError{
			message: fmt.Sprintf("binding-shape flags %q and %q are mutually exclusive; pick one",
				shapeFlags[0], shapeFlags[1]),
			span: span,
		}}
	}
	switch shapeFlags[0] {
	case "local":
		shape.local = true
	case "nested":
		shape.nested = true
	case "flat":
		shape.flat = true
	}
	return shape, nil
}

// resolveStdlibPath maps a `scheme:pkg` URI to an on-disk `.esc` file
// path under the configured stdlib data directory. Reports a not-found
// diagnostic when the file is missing.
func (c *Checker) resolveStdlibPath(scheme, pkg string, span ast.Span) (string, []Error) {
	dir, err := c.getStdlibDir()
	if err != nil {
		return "", []Error{&GenericError{message: err.Error(), span: span}}
	}
	if !isValidPackagePath(pkg) {
		return "", []Error{&GenericError{
			message: fmt.Sprintf("invalid package name %q in %s:%s; expected lowercase letters, digits, and underscores",
				pkg, scheme, pkg),
			span: span,
		}}
	}
	path := filepath.Join(dir, scheme, pkg+".esc")
	if info, statErr := os.Stat(path); statErr != nil || info.IsDir() {
		return "", []Error{&GenericError{
			message: fmt.Sprintf("unknown package %q in %s: scheme (no %s/%s.esc under %s)",
				pkg, scheme, scheme, pkg, dir),
			span: span,
		}}
	}
	return path, nil
}

// isValidPackagePath enforces the FR2 naming rule: lowercase letters,
// digits, and underscores. Hyphens are not allowed in URI portion; the
// `-`→`_` substitution lives in the third-party workstream, not here.
func isValidPackagePath(pkg string) bool {
	if pkg == "" {
		return false
	}
	for i := 0; i < len(pkg); i++ {
		c := pkg[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		case c == '_':
		default:
			return false
		}
	}
	return true
}

func (c *Checker) getStdlibDir() (string, error) {
	c.stdlibDirOnce.Do(func() {
		c.stdlibDir, c.stdlibDirErr = interop.StdlibDir("")
	})
	return c.stdlibDir, c.stdlibDirErr
}

// loadStdlibPackage parses and infers the stdlib `.esc` file at
// filePath into a fresh namespace, caching the result in
// PackageRegistry keyed by the full URI. Per §2.3 bookkeeping keys on
// the URI, not the binding-shape flag.
func (c *Checker) loadStdlibPackage(uri, filePath string, span ast.Span) (*type_system.Namespace, []Error) {
	if pkgNs, found := c.PackageRegistry.Lookup(uri); found {
		// nil sentinel signals an in-progress (cyclic) load; callers
		// treat it as "skip binding for now" per §4's "import cycles
		// are permitted" note.
		return pkgNs, nil
	}

	// Cycle-aware path: if this URI is part of an SCC of size > 1,
	// load every member as a single merged module so cross-package type
	// references resolve through the dep_graph's own SCC handling.
	scc, sccErr := c.stdlibSCCFor(uri)
	if sccErr != nil {
		return nil, []Error{&GenericError{message: sccErr.Error(), span: span}}
	}
	if len(scc) > 1 {
		if errs := c.loadStdlibSCC(scc, span); len(errs) > 0 {
			return nil, errs
		}
		pkgNs, _ := c.PackageRegistry.Lookup(uri)
		return pkgNs, nil
	}

	c.PackageRegistry.MarkInProgress(uri)

	contents, err := os.ReadFile(filePath)
	if err != nil {
		delete(c.PackageRegistry.packages, uri)
		return nil, []Error{&GenericError{
			message: fmt.Sprintf("failed to read stdlib file %s: %s", filePath, err.Error()),
			span:    span,
		}}
	}

	sourceID := c.stdlibNextSourceID
	c.stdlibNextSourceID++
	source := &ast.Source{
		ID: sourceID,
		// Strip the directory off so deriveNamespaceFromPath returns ""
		// — stdlib files live in a flat namespace, not the directory
		// hierarchy of their on-disk location.
		Path:     filepath.Base(filePath),
		Contents: string(contents),
	}

	mod, parseErrs := parser.ParseLibFiles(c.ctx, []*ast.Source{source})
	if len(parseErrs) > 0 {
		delete(c.PackageRegistry.packages, uri)
		// Re-anchor each parse error to the importing-file `span` so the
		// IDE underlines the user's `import` statement, not a location
		// inside the stdlib file (which the user did not write and
		// cannot navigate to without leaving their project). The
		// original file:line:col is kept in the message body for
		// compiler contributors debugging the stub.
		errs := make([]Error, 0, len(parseErrs))
		for _, pe := range parseErrs {
			errs = append(errs, &GenericError{
				message: fmt.Sprintf("parse error in %s: %s", filePath, pe.String()),
				span:    span,
			})
		}
		return nil, errs
	}

	// Loader rules §3.4 (1-4): every exported value-level decl must
	// carry `@js`; unexported value-level decls are rejected; the `@js`
	// argument must name a known JS runtime path.
	if decErrs := c.validateJsDecorators(filePath, mod, span); len(decErrs) > 0 {
		delete(c.PackageRegistry.packages, uri)
		return nil, decErrs
	}

	pkgNs := type_system.NewNamespace()
	pkgScope := &Scope{Parent: c.GlobalScope, Namespace: pkgNs}
	pkgCtx := Context{Scope: pkgScope, IsAsync: false, IsPatMatch: false}
	_, inferErrs := c.InferModule(pkgCtx, mod)

	if updateErr := c.PackageRegistry.Update(uri, pkgNs); updateErr != nil {
		inferErrs = append(inferErrs, &GenericError{
			message: fmt.Sprintf("failed to register stdlib package %s: %s", uri, updateErr.Error()),
			span:    span,
		})
	}
	return pkgNs, inferErrs
}

// bindStdlibLocal binds the package under the lowercased last URI
// segment in the importing file's scope. If the package declares a
// top-level class whose name matches the package name
// case-insensitively (FR5 single-class shortcut), bind that class
// directly with its original capitalization instead.
func (c *Checker) bindStdlibLocal(ctx Context, pkg string, pkgNs *type_system.Namespace, span ast.Span) []Error {
	filtered := filterExportedNamespace(pkgNs)

	// Single-class shortcut: look for a class whose name matches the
	// package name case-insensitively. Activation requires the package
	// to expose both a value (the constructor) and a type alias under
	// the same identifier — which is exactly the shape a class
	// declaration produces.
	if className, ok := findSingleClassShortcut(filtered, pkg); ok {
		ns := ctx.Scope.Namespace
		ns.Values[className] = filtered.Values[className]
		ns.Types[className] = filtered.Types[className]
		// TODO (§2.4): also expose other package exports as namespace
		// members on the same binding, with static methods winning on
		// collision. Deferred until a stdlib package actually has both
		// a class and non-class exports — the current `std:array` stub
		// has only the class.
		return nil
	}

	bindingName := lastSegmentLower(pkg)
	if err := ctx.Scope.Namespace.SetNamespace(bindingName, filtered); err != nil {
		return []Error{&GenericError{
			message: fmt.Sprintf("cannot bind stdlib namespace %q: %s", bindingName, err.Error()),
			span:    span,
		}}
	}
	return nil
}

// findSingleClassShortcut returns the original-capitalization class
// name when ns exposes a value+type pair whose identifier matches pkg
// case-insensitively. Returns ("", false) otherwise.
func findSingleClassShortcut(ns *type_system.Namespace, pkg string) (string, bool) {
	for name := range ns.Values {
		if !strings.EqualFold(name, pkg) {
			continue
		}
		if _, hasType := ns.Types[name]; hasType {
			return name, true
		}
	}
	return "", false
}

// bindStdlibNested binds the package under <scheme>.<pkg> in the file
// scope, lazily creating the per-scheme namespace. Duplicate ?nested
// imports of the same package within a single file are silently
// idempotent — bookkeeping keys on the URI (per §2.3) so the first
// import "won" and any subsequent ones add nothing.
func (c *Checker) bindStdlibNested(ctx Context, scheme, pkg string, pkgNs *type_system.Namespace, span ast.Span) []Error {
	schemeNs, err := getOrCreateSchemeNamespace(ctx.Scope.Namespace, scheme)
	if err != nil {
		return []Error{&GenericError{message: err.Error(), span: span}}
	}
	if _, exists := schemeNs.GetNamespace(pkg); exists {
		// Already bound from an earlier ?nested import of the same URI.
		return nil
	}
	filtered := filterExportedNamespace(pkgNs)
	if err := schemeNs.SetNamespace(pkg, filtered); err != nil {
		return []Error{&GenericError{
			message: fmt.Sprintf("cannot bind %s.%s: %s", scheme, pkg, err.Error()),
			span:    span,
		}}
	}
	return nil
}

// bindStdlibFlat merges exported package members directly into the
// per-scheme namespace. Name collisions across two ?flat-imported
// packages in the same file are hard errors per FR4 — the second
// import is rejected and the diagnostic names the prior contributing
// package.
func (c *Checker) bindStdlibFlat(ctx Context, scheme, uri string, pkgNs *type_system.Namespace, span ast.Span) []Error {
	schemeNs, err := getOrCreateSchemeNamespace(ctx.Scope.Namespace, scheme)
	if err != nil {
		return []Error{&GenericError{message: err.Error(), span: span}}
	}
	filtered := filterExportedNamespace(pkgNs)
	contributors := c.flatContributorsFor(ctx.Scope, scheme)

	// Iterate identifiers in a deterministic order so the diagnostic
	// chosen on a multi-name collision is stable across runs.
	names := flatIdentifiers(filtered)
	for _, name := range names {
		if prior, ok := contributors[name]; ok && prior != uri {
			return []Error{&GenericError{
				message: fmt.Sprintf(
					"?flat name collision: %q is contributed by both %q and %q; "+
						"rename upstream or drop one import's ?flat flag",
					name, prior, uri),
				span: span,
			}}
		}
	}
	for _, name := range names {
		// Sub-namespaces go through SetNamespace so the
		// "namespace-vs-value/type at the same name" invariant is
		// enforced locally. Values/Types use a manual cross-check
		// against schemeNs.Namespaces for the symmetric case (the
		// type_system package has no setter for those slots).
		// Belt-and-suspenders: the contributors check above should
		// already have caught any of these cases, but the local
		// invariant check protects against future drift.
		if subNs, ok := filtered.Namespaces[name]; ok {
			if _, exists := schemeNs.Namespaces[name]; !exists {
				if setErr := schemeNs.SetNamespace(name, subNs); setErr != nil {
					return []Error{&GenericError{message: setErr.Error(), span: span}}
				}
			}
		}
		if binding, ok := filtered.Values[name]; ok {
			if _, conflict := schemeNs.Namespaces[name]; conflict {
				return []Error{&GenericError{
					message: fmt.Sprintf(
						"cannot merge value %q from %q: name already occupied by a sub-namespace in `%s`",
						name, uri, scheme),
					span: span,
				}}
			}
			schemeNs.Values[name] = binding
		}
		if alias, ok := filtered.Types[name]; ok {
			if _, conflict := schemeNs.Namespaces[name]; conflict {
				return []Error{&GenericError{
					message: fmt.Sprintf(
						"cannot merge type %q from %q: name already occupied by a sub-namespace in `%s`",
						name, uri, scheme),
					span: span,
				}}
			}
			schemeNs.Types[name] = alias
		}
		contributors[name] = uri
	}
	return nil
}

// flatIdentifiers returns the union of identifier names exposed by ns
// across values, types, and sub-namespaces, sorted for deterministic
// iteration. Unioning is correct here because Namespace's invariants
// treat the three slots as a single identifier space (with the
// value+type carve-out for the class pattern) — see
// type_system.Namespace.SetNamespace. ?flat collision detection works
// on identifiers, not slots, per FR4.
func flatIdentifiers(ns *type_system.Namespace) []string {
	ids := set.NewSet[string]()
	for name := range ns.Values {
		ids.Add(name)
	}
	for name := range ns.Types {
		ids.Add(name)
	}
	for name := range ns.Namespaces {
		ids.Add(name)
	}
	out := ids.ToSlice()
	sort.Strings(out)
	return out
}

// getOrCreateSchemeNamespace returns the `<scheme>` sub-namespace on
// ns, creating it on first access. Returns an error if a non-namespace
// binding (value/type) already occupies that name in ns.
func getOrCreateSchemeNamespace(ns *type_system.Namespace, scheme string) (*type_system.Namespace, error) {
	if existing, ok := ns.GetNamespace(scheme); ok {
		return existing, nil
	}
	schemeNs := type_system.NewNamespace()
	if err := ns.SetNamespace(scheme, schemeNs); err != nil {
		return nil, fmt.Errorf("cannot create per-scheme namespace %q: %w", scheme, err)
	}
	return schemeNs, nil
}

// flatContributorsFor returns the per-file, per-scheme map tracking
// which URI contributed each ?flat-merged identifier. The map is
// lazily created so files that never use ?flat pay no cost.
func (c *Checker) flatContributorsFor(fileScope *Scope, scheme string) map[string]string {
	if c.flatContributors == nil {
		c.flatContributors = make(map[*Scope]map[string]map[string]string)
	}
	perFile, ok := c.flatContributors[fileScope]
	if !ok {
		perFile = make(map[string]map[string]string)
		c.flatContributors[fileScope] = perFile
	}
	perScheme, ok := perFile[scheme]
	if !ok {
		perScheme = make(map[string]string)
		perFile[scheme] = perScheme
	}
	return perScheme
}

// lastSegmentLower returns the last `_`-separated segment of pkg,
// lowercased. For `math` it returns `math`; for `typed_arrays` it
// returns `typed_arrays` (the package portion is already the "last
// segment" of the URI under our flat scheme:pkg layout).
func lastSegmentLower(pkg string) string {
	// The URI layout is `scheme:pkg` with `pkg` already being a single
	// segment (multi-word packages use `_`, not `/`). So the "last URI
	// segment" is just pkg itself; lowercasing is the only normalization
	// required here.
	return strings.ToLower(pkg)
}
