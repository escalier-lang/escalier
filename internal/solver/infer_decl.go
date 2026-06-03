package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// inferVarDecl types a val/var declaration's initializer into a monomorphic
// ValueBinding (no generalization in M2). When the decl carries a type
// annotation the initializer is constrained against it and the annotated type
// becomes the binding's type; a missing initializer (rare in M2) takes a fresh
// var. The binding is returned, not defined — the caller decides which scope it
// lands in (the body walk overwrites the current scope; the module driver, PR-2,
// seeds the module scope).
//
// This helper is shared with PR-2's single-module decl driver; the body-level
// DeclStmt walk (PR-3, infer_stmt.go) is its first consumer.
func (c *checker) inferVarDecl(scope *Scope, lvl int, d *ast.VarDecl) ValueBinding {
	var t soltype.Type
	if d.Init != nil {
		t = c.inferExpr(scope, lvl, d.Init)
	} else {
		t = c.freshAt(lvl)
	}
	if d.TypeAnn != nil {
		annT := c.resolveTypeAnn(d.TypeAnn)
		c.constrain(d, t, annT) // initializer <: declared type
		t = annT
	}
	return ValueBinding{Type: t, Source: d.Provenance()}
}

// inferFuncDecl types a top-level function declaration into a monomorphic
// ValueBinding, reusing the shared inferFunc core on the decl's signature and
// body. Like inferVarDecl it returns the binding rather than defining it; the
// SCC driver (PR-5) binds the name (a self/mutually recursive group is bound to
// a fresh var first so the body can see itself — that wiring is PR-5's).
func (c *checker) inferFuncDecl(scope *Scope, lvl int, d *ast.FuncDecl) ValueBinding {
	t := c.inferFunc(scope, lvl, d.FuncSig, d.Body, d)
	return ValueBinding{Type: t, Source: d.Provenance()}
}
