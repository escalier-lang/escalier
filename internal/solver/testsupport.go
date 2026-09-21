package solver

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// testsupport.go exports one entry point for tests in packages that consume
// soltype values but cannot reach this package's resolver. resolveTypeAnn is a
// method on the unexported checker, so a consumer such as internal/codegen has no
// way to turn the annotation syntax a test wants to write into the type it
// denotes. parser.ParseTypeAnn reaches the syntax but not the resolution, and an
// annotation naming a class or alias needs a scope those declarations were
// inferred into, which is why this runs a whole module rather than one rule.
//
// The file deliberately does not end in _test.go. Such a file compiles only into
// its own package's test binary, so nothing outside could import what it declares.
// Nothing in production calls what this one exports.

// annBindingName is the binding ResolveTypeAnnForTest hangs the annotation off.
// A leading underscore starts a valid identifier, so decls could bind the same
// name and shadow the annotation. ResolveTypeAnnForTest rejects source that
// mentions it rather than returning the wrong type.
const annBindingName = "__ann_for_test"

// testStdlibDir returns the pseudo-package tree this package's own tests infer
// against, the one that declares `Array`, `Promise`, and the rest of the prelude.
// The path is resolved against this file rather than the working directory,
// because a caller in another package runs from its own.
func testStdlibDir() string {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		panic("testStdlibDir: runtime.Caller gave no path for this file")
	}
	return filepath.Join(filepath.Dir(thisFile), "testdata", "stdlib")
}

// ResolveTypeAnnForTest resolves one Escalier type annotation to the soltype.Type
// the checker builds for it.
//
// decls is module source declaring whatever the annotation references, such as a
// class, a type alias, or an interface. It is empty for an annotation that names
// only primitives and the prelude. ann is the annotation itself, resolved as the
// type of a `declare val`, so any form a declaration can be annotated with is
// accepted.
//
// It returns the resolved type and every diagnostic the run reported. A caller
// asserting on a well-formed annotation checks that the diagnostics are empty. One
// exercising a recovery path reads them instead. The error is non-nil only when
// the source does not parse or binds no type, which is a fault in the test rather
// than a result to assert on.
//
// A type it cannot produce is one no source spells. The error sentinel, a skolem,
// a complement, an unresolved inference variable, and a signature-less overload
// set all have to be built by hand.
func ResolveTypeAnnForTest(decls, ann string) (soltype.Type, []SolverError, error) {
	if strings.Contains(decls, annBindingName) {
		return nil, nil, fmt.Errorf(
			"the declarations mention %s, the name this helper binds the annotation to; rename it",
			annBindingName)
	}

	// The annotation parses on its own first, so a syntax error in it is reported
	// against the annotation rather than against the module built around it.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, parseErrors := parser.ParseTypeAnn(ctx, ann); len(parseErrors) > 0 {
		return nil, nil, fmt.Errorf("parsing the annotation %q: %s", ann, parseErrors[0].Message)
	}

	src := decls + "\ndeclare val " + annBindingName + ": " + ann + "\n"
	source := &ast.Source{ID: 0, Path: "testsupport.esc", Contents: src}
	module, parseErrors := parser.ParseLibFiles(ctx, []*ast.Source{source})
	if len(parseErrors) > 0 {
		// The annotation already parsed, so what is left is the declarations.
		return nil, nil, fmt.Errorf("parsing the declarations: %s", parseErrors[0].Message)
	}

	scope, _, errs := InferModule(module, StdlibSource(testStdlibDir()))
	binding, bound := scope.GetValue(annBindingName)
	if !bound || len(binding.Schemes) == 0 {
		return nil, errs, fmt.Errorf("resolving %q: the annotation bound no type", ann)
	}
	return schemeType(binding.Schemes[0]), errs, nil
}
