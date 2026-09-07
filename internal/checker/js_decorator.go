package checker

import (
	"fmt"

	"github.com/escalier-lang/escalier/internal/ast"
)

// declName returns a printable name for decl used in loader diagnostics.
// Returns "" if decl has no obvious single-identifier name (e.g. a
// VarDecl with a destructuring pattern); diagnostics fall back to the
// span in that case.
func declName(decl ast.Decl) string {
	switch d := decl.(type) {
	case *ast.VarDecl:
		if id, ok := d.Pattern.(*ast.IdentPat); ok {
			return id.Name
		}
		return ""
	case *ast.FuncDecl:
		if d.Name != nil {
			return d.Name.Name
		}
		return ""
	case *ast.ClassDecl:
		if d.Name != nil {
			return d.Name.Name
		}
		return ""
	case *ast.TypeDecl:
		if d.Name != nil {
			return d.Name.Name
		}
		return ""
	case *ast.InterfaceDecl:
		if d.Name != nil {
			return d.Name.Name
		}
		return ""
	case *ast.EnumDecl:
		if d.Name != nil {
			return d.Name.Name
		}
		return ""
	}
	return ""
}

// declNameOrPlaceholder is declName with a "<unnamed>" fallback for
// diagnostics that need a non-empty string.
func declNameOrPlaceholder(decl ast.Decl) string {
	if n := declName(decl); n != "" {
		return n
	}
	return "<unnamed>"
}

// declKindLabel returns a short label for decl used in diagnostics
// ("function", "class", "value", …). Matches the §3.3 grammar terms.
func declKindLabel(decl ast.Decl) string {
	switch decl.(type) {
	case *ast.FuncDecl:
		return "function"
	case *ast.ClassDecl:
		return "class"
	case *ast.VarDecl:
		return "value"
	case *ast.TypeDecl:
		return "type"
	case *ast.InterfaceDecl:
		return "interface"
	case *ast.EnumDecl:
		return "enum"
	default:
		return "declaration"
	}
}

// validateJsDecorators enforces the §3.4 loader rules on the
// declarations of a parsed pseudo-package module:
//
//  1. Every exported value-level top-level decl must carry `@js`.
//  2. An unexported value-level top-level decl is rejected (no runtime
//     mapping; invisible to importers — almost certainly a missing
//     `export`).
//  3. An unexported type-level decl is rejected too. Importers'
//     file-scope bindings share the canonical pkgNs pointer (see
//     bindStdlibLocal / bindStdlibNested), so any member in pkgNs is
//     potentially reachable through qualified refs — unexported types
//     would leak. Forbidding them at the loader keeps the
//     share-by-pointer model safe; the `@js` decorator is forbidden
//     on type-level decls at parse time, so we don't need to check it
//     here.
//
// Whether the `@js` argument names a real JS runtime path is settled
// where the decorator is written, not here. `dts_to_esc.ValidateJSTargets`
// answers it against the pinned `.d.ts` set the tree is generated from,
// which is the only source that agrees with the tree by construction.
//
// Each diagnostic names the file (via the importing-file `span`), the
// declaration, and the rule that fired so the user can act on it
// without leaving their project.
func (c *Checker) validateJsDecorators(filePath string, mod *ast.Module, importSpan ast.Span) []Error {
	var errs []Error
	mod.Namespaces.Scan(func(_ string, ns *ast.Namespace) bool {
		for _, decl := range ns.Decls {
			errs = append(errs, validateJsDecorator(filePath, decl, importSpan)...)
		}
		return true
	})
	return errs
}

func validateJsDecorator(filePath string, decl ast.Decl, importSpan ast.Span) []Error {
	switch decl.(type) {
	case *ast.VarDecl, *ast.FuncDecl, *ast.ClassDecl:
		// fall through — value-level decl handled below
	case *ast.TypeDecl, *ast.InterfaceDecl, *ast.EnumDecl:
		// Rule 3: type-level decls must be exported. No `@js` checks
		// apply (parser forbids `@js` on type-level decls).
		if !decl.Export() {
			return []Error{&GenericError{
				message: fmt.Sprintf(
					"unexported %s %q in pseudo-package file %s would leak to importers "+
						"through the shared package namespace; "+
						"add `export` or remove the declaration",
					declKindLabel(decl), declNameOrPlaceholder(decl), filePath),
				span: importSpan,
			}}
		}
		return nil
	default:
		return nil
	}

	jsDec, _, jsOK := ast.FindJsDecorator(decl)
	name := declNameOrPlaceholder(decl)
	kindLabel := declKindLabel(decl)

	if !decl.Export() {
		// Rule 2: unexported value-level decl is rejected.
		return []Error{&GenericError{
			message: fmt.Sprintf(
				"unexported %s %q in pseudo-package file %s has no runtime mapping; "+
					"add `export` (and an `@js(...)` decorator) or remove the declaration",
				kindLabel, name, filePath),
			span: importSpan,
		}}
	}

	// Rule 1: exported value-level decl must carry `@js`.
	if jsDec == nil {
		return []Error{&GenericError{
			message: fmt.Sprintf(
				"exported %s %q in pseudo-package file %s is missing an `@js(\"...\")` decorator",
				kindLabel, name, filePath),
			span: importSpan,
		}}
	}
	if !jsOK {
		// `@js` is present but malformed — argument isn't a single string
		// literal. Loader rule, not a parser error: the parser accepts any
		// positional expression list to leave room for future decorators.
		return []Error{&GenericError{
			message: fmt.Sprintf(
				"`@js` decorator on %s %q in pseudo-package file %s must take a single string-literal argument",
				kindLabel, name, filePath),
			span: importSpan,
		}}
	}
	return nil
}
