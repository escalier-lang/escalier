package solver

import (
	"fmt"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
)

// jsDecoratorErrors reports the value-level declarations of a pseudo-package
// module that break the `@js` loader rules:
//
//  1. An exported value-level declaration carries `@js("...")` with a single
//     string-literal argument.
//  2. A value-level declaration is exported.
//
// Only the declarations in namespace `under` and the namespaces nested in it
// are checked, and an empty `under` checks every declaration. path names the
// file in each diagnostic, and span anchors it, since the declaration's own span
// points into a file the importer did not write. A type-level declaration may
// stay unexported, because exportedSurface leaves it out of what an importer can
// reach.
func jsDecoratorErrors(path string, module *ast.Module, under string, span ast.Span) []SolverError {
	var errs []SolverError
	module.Namespaces.Scan(func(key string, ns *ast.Namespace) bool {
		if under != "" && key != under && !strings.HasPrefix(key, under+".") {
			return true
		}
		for _, decl := range ns.Decls {
			if err := checkJsDecorator(path, decl, span); err != nil {
				errs = append(errs, err)
			}
		}
		return true
	})
	return errs
}

// checkJsDecorator returns the diagnostic for decl, or nil when decl keeps the
// rules jsDecoratorErrors lists.
func checkJsDecorator(path string, decl ast.Decl, span ast.Span) SolverError {
	kind, name, isValue := valueDeclLabel(decl)
	if !isValue {
		return nil
	}
	if !decl.Export() {
		return &PseudoPackageDeclError{
			Path: path, Kind: kind, Name: name, Reason: unexportedValue, span: span,
		}
	}
	dec, _, ok := ast.FindJsDecorator(decl)
	switch {
	case dec == nil:
		return &PseudoPackageDeclError{
			Path: path, Kind: kind, Name: name, Reason: missingJsDecorator, span: span,
		}
	case !ok:
		return &PseudoPackageDeclError{
			Path: path, Kind: kind, Name: name, Reason: malformedJsDecorator, span: span,
		}
	}
	return nil
}

// valueDeclLabel returns the kind word and name a diagnostic gives decl, and
// reports whether decl is a value-level declaration. A `val` that destructures
// has no single name, so it reads as `<unnamed>`.
func valueDeclLabel(decl ast.Decl) (kind, name string, isValue bool) {
	switch d := decl.(type) {
	case *ast.VarDecl:
		name = "<unnamed>"
		if id, ok := d.Pattern.(*ast.IdentPat); ok {
			name = id.Name
		}
		return "value", name, true
	case *ast.FuncDecl:
		return "function", d.Name.Name, true
	case *ast.ClassDecl:
		return "class", d.Name.Name, true
	}
	return "", "", false
}

// pseudoPackageDeclReason is the rule a PseudoPackageDeclError reports.
type pseudoPackageDeclReason int

const (
	missingJsDecorator pseudoPackageDeclReason = iota
	malformedJsDecorator
	unexportedValue
)

// PseudoPackageDeclError reports a declaration in a pseudo-package file that
// breaks a rule jsDecoratorErrors lists. It is anchored at the import that
// loaded the file.
type PseudoPackageDeclError struct {
	// Path is the pseudo-package file that holds the declaration.
	Path string
	// Kind is the declaration's kind word, such as "value" or "class".
	Kind string
	// Name is the declared name.
	Name   string
	Reason pseudoPackageDeclReason
	span   ast.Span
}

func (e *PseudoPackageDeclError) Message() string {
	switch e.Reason {
	case missingJsDecorator:
		return fmt.Sprintf("exported %s %q in pseudo-package file %s is missing an `@js(\"...\")` decorator",
			e.Kind, e.Name, e.Path)
	case malformedJsDecorator:
		return fmt.Sprintf("`@js` decorator on %s %q in pseudo-package file %s must take a single string-literal argument",
			e.Kind, e.Name, e.Path)
	default:
		return fmt.Sprintf("unexported %s %q in pseudo-package file %s has no runtime mapping; "+
			"add `export` (and an `@js(...)` decorator) or remove the declaration",
			e.Kind, e.Name, e.Path)
	}
}
func (e *PseudoPackageDeclError) Span() ast.Span      { return e.span }
func (e *PseudoPackageDeclError) Related() []ast.Span { return nil }
func (e *PseudoPackageDeclError) isSolverError()      {}
