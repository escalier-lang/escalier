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
	"github.com/escalier-lang/escalier/internal/type_system"
)

// stdlibSchemes lists the URI schemes the resolver recognizes for
// pseudo-package imports. Ordering matters only for "unknown scheme"
// diagnostic text — display recognized schemes in a stable order.
var stdlibSchemes = []string{"std", "dom", "node"}

// stdlibKnownFlags is the recognized set of binding-shape flags.
// Per §2.3 the slot is extensible (future `?type-only`, `?lazy`, …); the
// table-driven check means new entries slot in without restructuring.
var stdlibKnownFlags = map[string]bool{
	"local":  true,
	"nested": true,
	"flat":   true,
}

// stdlibBindingShape captures the resolved binding-shape for a single
// import statement. Exactly one of the three booleans is true.
type stdlibBindingShape struct {
	local, nested, flat bool
}

// isSchemePrefixedImport reports whether spec begins with one of the
// recognized scheme prefixes (`std:`, `dom:`, `node:`). Used by
// inferImport to dispatch between the npm-style loader and the stdlib
// loader before path resolution.
func isSchemePrefixedImport(spec string) bool {
	for _, scheme := range stdlibSchemes {
		if strings.HasPrefix(spec, scheme+":") {
			return true
		}
	}
	// Any other `<word>:` shape is also a scheme — treated as an
	// unknown-scheme diagnostic by the resolver. Detecting that here
	// instead of bailing to the npm loader means the user gets the
	// taxonomy-aligned message ("unknown scheme") rather than a
	// confusing "could not find package.json".
	if colon := strings.IndexByte(spec, ':'); colon > 0 {
		scheme := spec[:colon]
		if isASCIILower(scheme) {
			return true
		}
	}
	return false
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

	scheme, pkg, ok := splitScheme(importStmt.PackageName)
	if !ok || !isRecognizedScheme(scheme) {
		return []Error{&GenericError{
			message: fmt.Sprintf("unknown import scheme %q; recognized schemes: %s",
				scheme, strings.Join(stdlibSchemes, ", ")),
			span: span,
		}}
	}
	if pkg == "" {
		return []Error{&GenericError{
			message: fmt.Sprintf("missing package name after %q scheme", scheme),
			span:    span,
		}}
	}

	// Named imports from scheme-prefixed URIs are rejected per FR4 /
	// "Named import from a pseudo-package URI" in the error taxonomy.
	if !importStmt.Bare() {
		return []Error{&GenericError{
			message: fmt.Sprintf(
				"named imports from pseudo-package %q are not supported; "+
					"use a bare-string import (`import %q`) and access members through the namespace",
				importStmt.PackageName, importStmt.PackageName),
			span: span,
		}}
	}

	// Validate flags and resolve the binding shape.
	shape, flagErrs := resolveStdlibFlags(importStmt.Flags, span)
	if len(flagErrs) > 0 {
		return flagErrs
	}

	// node:* is reserved; the resolver rejects every package until Node
	// support lands.
	if scheme == "node" {
		return []Error{&GenericError{
			message: fmt.Sprintf("%q: node:* is reserved; not yet populated", importStmt.PackageName),
			span:    span,
		}}
	}

	// Resolve to a file path under the stdlib data directory.
	filePath, resolveErrs := c.resolveStdlibPath(scheme, pkg, span)
	if len(resolveErrs) > 0 {
		return resolveErrs
	}

	// Load the package's namespace, with PackageRegistry as the cache.
	pkgNs, loadErrs := c.loadStdlibPackage(importStmt.PackageName, filePath, span)
	if len(loadErrs) > 0 {
		return loadErrs
	}
	if pkgNs == nil {
		return nil // in-progress / cycle sentinel; cycles are permitted per §4
	}

	// Bind per shape. Only ?local is implemented in this slice; ?nested
	// and ?flat will be wired up in the follow-up alongside the
	// single-class shortcut.
	if shape.local {
		return c.bindStdlibLocal(ctx, pkg, pkgNs, span)
	}
	flag := "nested"
	if shape.flat {
		flag = "flat"
	}
	return []Error{&GenericError{
		message: fmt.Sprintf("?%s binding-shape is not yet implemented; use ?local", flag),
		span:    span,
	}}
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
	for _, s := range stdlibSchemes {
		if s == scheme {
			return true
		}
	}
	return false
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

	seen := map[string]bool{}
	shapeFlags := []string{}
	var errs []Error
	for _, f := range flags {
		if f == "" {
			errs = append(errs, &GenericError{
				message: "empty flag in import specifier", span: span,
			})
			continue
		}
		if !stdlibKnownFlags[f] {
			recognized := []string{}
			for k := range stdlibKnownFlags {
				recognized = append(recognized, k)
			}
			sort.Strings(recognized)
			errs = append(errs, &GenericError{
				message: fmt.Sprintf("unknown import flag %q; recognized flags: %s",
					f, strings.Join(recognized, ", ")),
				span: span,
			})
			continue
		}
		if seen[f] {
			errs = append(errs, &GenericError{
				message: fmt.Sprintf("duplicate import flag %q", f), span: span,
			})
			continue
		}
		seen[f] = true
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
		// Surface parse errors with the importing-file span so the user
		// sees the diagnostic at the import statement, not in a file
		// they didn't write.
		errs := make([]Error, 0, len(parseErrs))
		for _, pe := range parseErrs {
			errs = append(errs, &GenericError{
				message: fmt.Sprintf("parse error in %s: %s", filePath, pe.String()),
				span:    span,
			})
		}
		return nil, errs
	}

	pkgNs := type_system.NewNamespace()
	pkgScope := &Scope{Parent: c.GlobalScope, Namespace: pkgNs}
	pkgCtx := Context{Scope: pkgScope, IsAsync: false, IsPatMatch: false}
	inferErrs := c.InferModule(pkgCtx, mod)

	if updateErr := c.PackageRegistry.Update(uri, pkgNs); updateErr != nil {
		inferErrs = append(inferErrs, &GenericError{
			message: fmt.Sprintf("failed to register stdlib package %s: %s", uri, updateErr.Error()),
			span:    span,
		})
	}
	return pkgNs, inferErrs
}

// bindStdlibLocal binds the package namespace under the lowercased
// last URI segment in the importing file's scope.
func (c *Checker) bindStdlibLocal(ctx Context, pkg string, pkgNs *type_system.Namespace, span ast.Span) []Error {
	bindingName := lastSegmentLower(pkg)
	filtered := filterExportedNamespace(pkgNs)
	if err := ctx.Scope.Namespace.SetNamespace(bindingName, filtered); err != nil {
		return []Error{&GenericError{
			message: fmt.Sprintf("cannot bind stdlib namespace %q: %s", bindingName, err.Error()),
			span:    span,
		}}
	}
	return nil
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
