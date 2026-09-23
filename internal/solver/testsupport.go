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

// testsupport.go lets a test outside this package turn annotation syntax into
// the type it denotes, which resolveTypeAnn cannot do for it because it is a
// method on the unexported checker.
//
// It runs a whole module because an annotation naming a class needs a scope that
// class was inferred into. The filename avoids the _test.go suffix, since such a
// file compiles only into its own package's test binary. Production calls none
// of this.

// annBindingName is the binding the annotation hangs off. A leading underscore
// starts a valid identifier, so decls could bind it too and shadow the
// annotation, which is what the guard below rejects.
const annBindingName = "__ann_for_test"

// testStdlibDir returns the pseudo-package tree this package's tests infer
// against, resolved against this file because a caller elsewhere runs from its
// own directory.
func testStdlibDir() string {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		panic("testStdlibDir: runtime.Caller gave no path for this file")
	}
	return filepath.Join(filepath.Dir(thisFile), "testdata", "stdlib")
}

// ResolveTypeAnnForTest resolves one Escalier type annotation to the soltype.Type
// the checker builds for it. decls is module source declaring whatever the
// annotation references, and is empty for one naming only primitives and the
// prelude.
//
// The diagnostics are the run's own, for a caller exercising a recovery path. The
// error is a fault in the test itself: source that does not parse, or that binds
// no type.
func ResolveTypeAnnForTest(decls, ann string) (soltype.Type, []SolverError, error) {
	if strings.Contains(decls, annBindingName) {
		return nil, nil, fmt.Errorf(
			"the declarations mention %s, the name this helper binds the annotation to; rename it",
			annBindingName)
	}

	// Parsing the annotation alone first is what points a syntax error at it
	// rather than at the module built around it.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	parsed, parseErrors := parser.ParseTypeAnn(ctx, ann)
	if len(parseErrors) > 0 {
		return nil, nil, fmt.Errorf("parsing the annotation %q: %s", ann, parseErrors[0].Message)
	}
	if parsed == nil {
		// Whitespace or a comment parses without complaint and yields no node. Left
		// alone it reaches inference as a `declare val` with no type, whose
		// diagnostic names this helper's binding instead of the caller's mistake.
		return nil, nil, fmt.Errorf("the annotation %q declares no type", ann)
	}

	src := decls + "\ndeclare val " + annBindingName + ": " + ann + "\n"
	source := &ast.Source{ID: 0, Path: "testsupport.esc", Contents: src}
	module, parseErrors := parser.ParseLibFiles(ctx, []*ast.Source{source})
	if len(parseErrors) > 0 {
		return nil, nil, fmt.Errorf("parsing the declarations: %s", parseErrors[0].Message)
	}

	scope, _, errs := InferModule(module, StdlibSource(testStdlibDir()))
	binding, bound := scope.GetValue(annBindingName)
	if !bound || len(binding.Schemes) == 0 {
		// Defensive: a `declare val` that parsed always binds. Kept so an upstream
		// change surfaces here rather than as a nil dereference.
		return nil, errs, fmt.Errorf("resolving %q: the annotation bound no type", ann)
	}
	return schemeType(binding.Schemes[0]), errs, nil
}
