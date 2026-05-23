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
	}
	return ""
}

// declKindLabel returns a short label for decl used in diagnostics
// ("function", "class", "value"). Matches the §3.3 grammar terms.
func declKindLabel(decl ast.Decl) string {
	switch decl.(type) {
	case *ast.FuncDecl:
		return "function"
	case *ast.ClassDecl:
		return "class"
	case *ast.VarDecl:
		return "value"
	default:
		return "declaration"
	}
}

// validateJsDecorators enforces the §3.4 loader rules on the
// declarations of a parsed pseudo-package module (rules 1-3; rule 4
// lives in a separate PR):
//
//  1. Every exported value-level top-level decl must carry `@js`.
//  2. An unexported value-level top-level decl is rejected (no runtime
//     mapping; invisible to importers — almost certainly a missing
//     `export`).
//  3. An unexported type-level decl is allowed and must not carry `@js`
//     (already enforced at parse time, so nothing to check here).
//
// Each diagnostic names the file (via the importing-file `span`), the
// declaration, and the rule that fired so the user can act on it
// without leaving their project.
func validateJsDecorators(filePath string, mod *ast.Module, importSpan ast.Span) []Error {
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
	default:
		return nil
	}

	jsDec, jsArg, jsOK := ast.FindJsDecorator(decl)
	name := declName(decl)
	if name == "" {
		name = "<unnamed>"
	}
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
	_ = jsArg // arg validation against TS lib globals lands in a separate PR
	return nil
}
