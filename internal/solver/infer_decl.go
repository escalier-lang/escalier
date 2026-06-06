package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/provenance"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// inferDeclDef infers one top-level declaration for the SCC driver
// (inferComponent) and returns its RAW (un-coalesced, variable-carrying) type to
// constrain against the binding's group var, the decl's provenance, and ok=false
// when it introduces no value. It does NOT bind the name — inferComponent owns
// scope placement.
//
// The type MUST stay raw: inferComponent coalesces the group var once, after every
// group member has constrained it (phase 3). Coalescing a `val` initializer here
// would read a recursive peer's still-empty group var and freeze the binding to
// `never` — the bug behind splitting this out of inferVarDecl.
//
// ok=false cases, each already reported:
//   - VarDecl without an initializer → MissingInitializerError
//   - VarDecl with a destructuring pattern → UnsupportedNodeError (initializer
//     still walked first, so a malformed RHS surfaces its own errors)
//   - any decl kind outside the M2 subset → UnsupportedNodeError
func (c *checker) inferDeclDef(scope *Scope, lvl int, d ast.Decl) (soltype.Type, provenance.Provenance, bool) {
	switch d := d.(type) {
	case *ast.VarDecl:
		initType, ok := c.inferVarDeclInit(scope, lvl, d)
		if !ok {
			return nil, nil, false
		}
		if _, named := varName(d); !named {
			// Destructuring patterns (TuplePat/ObjectPat) need the tuple/record
			// types that arrive in M4. The initializer was already walked above
			// (its errors surfaced); report the pattern and produce no binding.
			c.reportUnsupported(d.Pattern)
			return nil, nil, false
		}
		return initType, &ast.NodeProvenance{Node: d}, true
	case *ast.FuncDecl:
		b := c.inferFuncDecl(scope, lvl, d)
		// inferFuncDecl always records exactly one source (this decl); inferComponent
		// accumulates these per-decl sources into the binding's Sources slice.
		return b.Type, b.Sources[0], true
	default:
		c.reportUnsupported(d)
		return nil, nil, false
	}
}

// inferVarDeclInit types a `val`/`var` initializer and returns its RAW
// (un-coalesced) type, ok=false when there's no initializer (MissingInitializerError
// reported). Shared core of both binding paths; they differ only in WHEN they
// coalesce: inferDeclDef (SCC driver) keeps it raw so inferComponent coalesces the
// group var once at completion, while inferVarDecl (body-level) coalesces it now.
// Walks the initializer regardless so a malformed RHS still surfaces errors, and
// binds nothing — the caller owns scope placement.
//
// A `val`/`var` with no initializer needs a type annotation (TypeAnn support lands
// in a later PR); for now it reports MissingInitializerError and returns ok=false.
func (c *checker) inferVarDeclInit(scope *Scope, lvl int, d *ast.VarDecl) (soltype.Type, bool) {
	if d.Init == nil {
		c.report(&MissingInitializerError{Decl: d})
		return nil, false
	}
	initT := c.inferExpr(scope, lvl, d.Init)
	if d.TypeAnn != nil {
		// M2.5: constrain the initializer against the annotation (the one
		// non-provenance addition, §3.7), so `val x: number = "hi"` produces a
		// CannotConstrainError whose LHS (the "hi" literal) carries a
		// LiteralInference origin — precise blame, with the annotation as the
		// related node. The constraint node is the initializer, so even the
		// fallback span is the RHS, not the whole decl; the binding then adopts the
		// annotated type.
		//
		// Skip both the check and the adoption when the annotation is unsupported
		// (ok=false): resolveTypeAnn already reported it and returned a `never`
		// placeholder, so constraining `initT <: never` would cascade a spurious
		// error and adopting `never` would poison the binding. Keep the inferred
		// initializer type instead (error recovery).
		if annT, ok := c.resolveTypeAnn(d.TypeAnn); ok {
			c.constrain(d.Init, initT, annT)
			initT = annT
		}
	}
	return initT, true
}

// inferVarDecl types a `val`/`var` into a MONOMORPHIC ValueBinding, coalescing the
// initializer at Positive polarity now (coalesce-at-binding, §7) so the stored type
// is var-free and stable. This is the body-level path (§3.2), safe to coalesce
// eagerly because a body-level `val` is never part of a recursive top-level group;
// the SCC driver keeps the raw type instead (see inferDeclDef). ok=false when there
// is no initializer.
func (c *checker) inferVarDecl(scope *Scope, lvl int, d *ast.VarDecl) (ValueBinding, bool) {
	initType, ok := c.inferVarDeclInit(scope, lvl, d)
	if !ok {
		return ValueBinding{}, false
	}
	t := coalesce(initType, soltype.Positive)
	c.recordType(d.Pattern, t)
	return ValueBinding{Type: t, Sources: []provenance.Provenance{&ast.NodeProvenance{Node: d}}}, true
}

// varName returns the bound name of a VarDecl whose pattern is an IdentPat, with
// ok=false for any other pattern shape. M2 binds IdentPat-only patterns,
// mirroring M1's IdentPat-only FuncParam; destructuring (`val [a, b] = …`)
// arrives in M4 once tuple/record types exist (§3.2).
func varName(d *ast.VarDecl) (string, bool) {
	if p, ok := d.Pattern.(*ast.IdentPat); ok {
		return p.Name, true
	}
	return "", false
}

// inferFuncDecl types a function declaration into a monomorphic ValueBinding,
// reusing the shared inferFunc core (infer_expr.go) on the decl's signature and
// body. Like inferVarDecl it returns the binding rather than defining it, so the
// caller owns scope placement: the SCC driver (inferComponent) binds a
// self/mutually recursive group to a fresh var first so each body can see itself
// (and its group peers), then rebinds to the inferred type. Repeated top-level
// FuncDecls under one name are constrained into the same var as monomorphic
// overload arms; the overload-intersection representation is M3.
func (c *checker) inferFuncDecl(scope *Scope, lvl int, d *ast.FuncDecl) ValueBinding {
	t := c.inferFunc(scope, lvl, d.FuncSig, d.Body, d)
	return ValueBinding{Type: t, Sources: []provenance.Provenance{&ast.NodeProvenance{Node: d}}}
}
