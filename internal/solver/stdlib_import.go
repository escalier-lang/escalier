package solver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/escalier-lang/escalier/internal/set"
)

// stdlib_import.go resolves the `std:` / `web:` / `node:` import surface: which
// URIs are well formed, which file each names, and what an importer binds for
// one.
//
// A pseudo-package is one `.esc` file. `std/prelude.esc` is `std:prelude`, the
// registry keys on the full URI, and the layout is flat, so a cross-package
// reference needs an explicit import the same as user code does.

// stdlibSchemes is the display-ordered list of URI schemes a pseudo-package
// import may name. The slice is for the unknown-scheme diagnostic; membership
// goes through stdlibSchemesSet.
var stdlibSchemes = []string{"std", "web", "node"}

// stdlibSchemesSet is the membership view of stdlibSchemes.
var stdlibSchemesSet = set.FromSlice(stdlibSchemes)

// stdlibSourceIDBase is the first source id a pseudo-package parses under. It
// sits above any id an entry module assigns, which are handed out from zero per
// module, so the two never collide.
const stdlibSourceIDBase = 1 << 20

// stdlibKnownFlags is the recognized set of binding-shape flags. `local` is the
// only one. The slot is extensible, and a table-driven check means a new entry
// slots in without restructuring.
var stdlibKnownFlags = set.FromSlice([]string{"local"})

// IsSchemePrefixedImport reports whether spec names a pseudo-package rather than
// an npm package.
//
// It admits any lowercase `<word>:` shape, not only the three recognized
// schemes, so a URI such as `bogus:thing` reaches the pseudo-package resolver
// and gets an unknown-scheme diagnostic rather than a confusing report from the
// npm side about a missing package.json.
func IsSchemePrefixedImport(spec string) bool {
	scheme, _, ok := splitScheme(spec)
	if !ok {
		return false
	}
	return stdlibSchemesSet.Contains(scheme) || isASCIILower(scheme)
}

// splitScheme cracks `scheme:pkg` into its parts. ok is false when there is no
// colon. The package portion may still be empty, which the caller reports.
func splitScheme(spec string) (scheme, pkg string, ok bool) {
	idx := strings.IndexByte(spec, ':')
	if idx <= 0 {
		return "", "", false
	}
	return spec[:idx], spec[idx+1:], true
}

// isASCIILower reports whether s is one or more lowercase ASCII letters.
func isASCIILower(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if s[i] < 'a' || s[i] > 'z' {
			return false
		}
	}
	return true
}

// isValidPackagePath enforces the FR2 naming rule: lowercase letters, digits,
// and underscores. A hyphen is not allowed here. Substituting one for an
// underscore belongs to the third-party workstream.
func isValidPackagePath(pkg string) bool {
	if pkg == "" {
		return false
	}
	for i := range len(pkg) {
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

// validateStdlibImport reports everything wrong with one pseudo-package import.
//
// Each check is independent and accumulates, so a malformed import surfaces
// every problem at once rather than putting the author through one
// fix-and-recompile round per mistake.
func validateStdlibImport(stmt *ast.ImportStmt) []SolverError {
	span := stmt.Span()
	var errs []SolverError

	scheme, pkg, hasColon := splitScheme(stmt.PackageName)
	schemeKnown := hasColon && stdlibSchemesSet.Contains(scheme)
	switch {
	case !schemeKnown:
		errs = append(errs, &UnknownImportSchemeError{Scheme: scheme, span: span})
	case pkg == "":
		errs = append(errs, &MissingPackageNameError{Scheme: scheme, span: span})
	}

	errs = append(errs, validateStdlibFlags(stmt.Flags, span)...)

	// `node:` is recognized so that a URI under it reads as reserved rather than
	// as an unknown scheme, and every package under it is rejected until Node
	// support lands.
	if schemeKnown && scheme == "node" {
		errs = append(errs, &ReservedSchemeError{URI: stmt.PackageName, span: span})
	}
	return errs
}

// validateStdlibFlags checks a parsed flag list against stdlibKnownFlags,
// reporting an unknown flag and a repeated one.
func validateStdlibFlags(flags []string, span ast.Span) []SolverError {
	seen := set.NewSet[string]()
	var errs []SolverError
	for _, flag := range flags {
		if flag == "" {
			errs = append(errs, &EmptyImportFlagError{span: span})
			continue
		}
		// The repeat check runs first and on every flag, so `?eager&eager` reads
		// as one unknown flag written twice rather than as two unrelated unknowns.
		if seen.Contains(flag) {
			errs = append(errs, &DuplicateImportFlagError{Flag: flag, span: span})
			continue
		}
		seen.Add(flag)
		if !stdlibKnownFlags.Contains(flag) {
			recognized := stdlibKnownFlags.ToSlice()
			sort.Strings(recognized)
			errs = append(errs, &UnknownImportFlagError{
				Flag: flag, Recognized: recognized, span: span,
			})
		}
	}
	return errs
}

// resolveStdlibPath maps a `scheme:pkg` URI to the `.esc` file under dir that
// declares it.
func resolveStdlibPath(dir, uri string) (string, error) {
	scheme, pkg, ok := splitScheme(uri)
	if !ok || !stdlibSchemesSet.Contains(scheme) {
		return "", fmt.Errorf("unrecognized scheme in %q", uri)
	}
	if !isValidPackagePath(pkg) {
		return "", fmt.Errorf(
			"invalid package name %q in %s:%s; expected lowercase letters, digits, and underscores",
			pkg, scheme, pkg)
	}
	path := filepath.Join(dir, scheme, pkg+".esc")
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		return "", fmt.Errorf("unknown package %q in %s: scheme (no %s/%s.esc under %s)",
			pkg, scheme, scheme, pkg, dir)
	}
	return path, nil
}

// UnknownImportSchemeError reports an import whose scheme names no
// pseudo-package family.
type UnknownImportSchemeError struct {
	Scheme string
	span   ast.Span
}

func (e *UnknownImportSchemeError) Message() string {
	return fmt.Sprintf("unknown import scheme %q; recognized schemes: %s",
		e.Scheme, strings.Join(stdlibSchemes, ", "))
}
func (e *UnknownImportSchemeError) Span() ast.Span      { return e.span }
func (e *UnknownImportSchemeError) Related() []ast.Span { return nil }
func (e *UnknownImportSchemeError) isSolverError()      {}

// MissingPackageNameError reports a scheme with nothing after its colon.
type MissingPackageNameError struct {
	Scheme string
	span   ast.Span
}

func (e *MissingPackageNameError) Message() string {
	return fmt.Sprintf("missing package name after %q scheme", e.Scheme)
}
func (e *MissingPackageNameError) Span() ast.Span      { return e.span }
func (e *MissingPackageNameError) Related() []ast.Span { return nil }
func (e *MissingPackageNameError) isSolverError()      {}

// ReservedSchemeError reports an import under a scheme no package is published
// for yet.
type ReservedSchemeError struct {
	URI  string
	span ast.Span
}

func (e *ReservedSchemeError) Message() string {
	return fmt.Sprintf("%q: node:* is reserved; not yet populated", e.URI)
}
func (e *ReservedSchemeError) Span() ast.Span      { return e.span }
func (e *ReservedSchemeError) Related() []ast.Span { return nil }
func (e *ReservedSchemeError) isSolverError()      {}

// EmptyImportFlagError reports a `?` suffix with nothing in one of its slots.
type EmptyImportFlagError struct {
	span ast.Span
}

func (e *EmptyImportFlagError) Message() string     { return "empty flag in import specifier" }
func (e *EmptyImportFlagError) Span() ast.Span      { return e.span }
func (e *EmptyImportFlagError) Related() []ast.Span { return nil }
func (e *EmptyImportFlagError) isSolverError()      {}

// UnknownImportFlagError reports a binding-shape flag the resolver does not
// recognize.
type UnknownImportFlagError struct {
	Flag       string
	Recognized []string
	span       ast.Span
}

func (e *UnknownImportFlagError) Message() string {
	return fmt.Sprintf("unknown import flag %q; recognized flags: %s",
		e.Flag, strings.Join(e.Recognized, ", "))
}
func (e *UnknownImportFlagError) Span() ast.Span      { return e.span }
func (e *UnknownImportFlagError) Related() []ast.Span { return nil }
func (e *UnknownImportFlagError) isSolverError()      {}

// DuplicateImportFlagError reports one flag written twice on one import.
type DuplicateImportFlagError struct {
	Flag string
	span ast.Span
}

func (e *DuplicateImportFlagError) Message() string {
	return fmt.Sprintf("duplicate import flag %q", e.Flag)
}
func (e *DuplicateImportFlagError) Span() ast.Span      { return e.span }
func (e *DuplicateImportFlagError) Related() []ast.Span { return nil }
func (e *DuplicateImportFlagError) isSolverError()      {}

// StdlibSource returns a ModuleSource that reads pseudo-packages from dir, the
// directory holding the `std/`, `web/`, and `node/` subtrees.
//
// One file is one package is one module. The parse is handed only the file's
// basename, so a package's namespace comes out empty and a sibling file in the
// same subtree is invisible to it. Reaching one takes an explicit import, the
// rule user code follows.
func StdlibSource(dir string) ModuleSource {
	// The source id each package parses under is handed out by stdlibParses, once
	// per distinct file content and from a base no entry module reaches. A span
	// carries its id into provenance and into every diagnostic built from it, so
	// a package sharing the entry module's ids would make "declared here" point
	// at an unrelated offset in the user's own file, and two packages sharing one
	// would make either's read as the other's.
	return func(uri string) (*ast.Module, string, error) {
		path, err := resolveStdlibPath(dir, uri)
		if err != nil {
			return nil, "", err
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return nil, "", fmt.Errorf("reading %s: %w", path, err)
		}
		module, err := stdlibParses.get(string(contents), func(sourceID int) (*ast.Module, error) {
			source := &ast.Source{
				ID: sourceID,
				// The basename alone, so a package's namespace comes out empty rather
				// than derived from where the tree happens to sit on disk.
				Path:     filepath.Base(path),
				Contents: string(contents),
			}
			module, parseErrs := parser.ParseLibFiles(context.Background(), []*ast.Source{source})
			if len(parseErrs) > 0 {
				messages := make([]string, 0, len(parseErrs))
				for _, pe := range parseErrs {
					messages = append(messages, pe.String())
				}
				return nil, fmt.Errorf("parse errors in %s: %s", path, strings.Join(messages, "; "))
			}
			return module, nil
		})
		if err != nil {
			return nil, "", err
		}
		return module, path, nil
	}
}
