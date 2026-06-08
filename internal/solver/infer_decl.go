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
			// (its errors surfaced); report the pattern and produce no binding. A
			// nil pattern (not produced by the parser, which synthesizes a
			// placeholder, but possible in a hand-built AST) blames the decl instead,
			// mirroring inferFunc — never a nil-node Span() panic.
			if d.Pattern != nil {
				c.reportUnsupported(d.Pattern)
			} else {
				c.reportUnsupported(d)
			}
			return nil, nil, false
		}
		return initType, &ast.NodeProvenance{Node: d}, true
	case *ast.FuncDecl:
		// inferFuncDecl returns the RAW func type and its source directly;
		// inferComponent constrains that raw type into the group var, generalizes
		// once the group is complete, and accumulates the per-decl source into the
		// binding's Sources slice.
		t, src := c.inferFuncDecl(scope, lvl, d)
		return t, src, true
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
		if annT, ok := c.resolveTypeAnn(d.TypeAnn, lvl); ok {
			c.constrain(d.Init, initT, annT)
			initT = annT
		}
	}
	return initT, true
}

// inferVarDecl types a body-level `val`/`var` into a GENERALIZED ValueBinding —
// the let-polymorphism rule (M3, PR1) that replaces M2's coalesce-at-binding
// freeze. It infers the initializer one level deeper (lvl+1) and generalizes at
// lvl: variables created in the RHS (level > lvl) become reusable type parameters,
// while variables captured from an enclosing scope (level <= lvl) stay shared — so
// `fn (y) { val getY = fn () { y }; [getY(), getY()] }` keeps getY's captured `y`
// instead of freezing it to `never`, the bug eager coalescing caused. A body-level
// `val` is never recursive (the name is bound only after its initializer is typed),
// so there is no pre-binding/group var; the SCC driver owns the recursive top-level
// path (see inferDeclDef). ok=false when there is no initializer.
func (c *checker) inferVarDecl(scope *Scope, lvl int, d *ast.VarDecl) (ValueBinding, bool) {
	initType, ok := c.inferVarDeclInit(scope, lvl+1, d)
	if !ok {
		return ValueBinding{}, false
	}
	scheme := c.generalize(initType, lvl)
	// The recorded display type retains any quantified type-parameter vars (it is
	// not var-free), so Info consumers must render it with soltype.PrintAsScheme, not
	// plain soltype.Print — same contract as the top-level path (see module.go).
	c.recordType(d.Pattern, schemeType(scheme))
	// PR8: carry the introducing decl's kind so inferAssign can gate reassignment —
	// only a `var` is reassignable, a `val` is not.
	return ValueBinding{
		Schemes: []TypeScheme{scheme},
		Sources: []provenance.Provenance{&ast.NodeProvenance{Node: d}},
		Kind:    d.Kind,
	}, true
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

// inferFuncDecl types a function declaration and returns its RAW (un-coalesced,
// variable-carrying) func type plus its provenance, NOT a ValueBinding: the SCC
// driver (inferComponent) owns scope placement and generalization. It binds a
// self/mutually recursive group to a fresh var first so each body can see itself
// (and its group peers), constrains this raw type into that var, and generalizes
// the group once complete (PR1). Returning the raw type directly (rather than
// round-tripping through a single-MonoScheme ValueBinding) removes the unchecked
// `.(*MonoScheme)` assertion the SCC driver would otherwise need. Repeated
// top-level FuncDecls under one name are constrained into the same var as
// monomorphic overload arms; the overload-intersection representation is M3.
func (c *checker) inferFuncDecl(scope *Scope, lvl int, d *ast.FuncDecl) (soltype.Type, provenance.Provenance) {
	t := c.inferFunc(scope, lvl, d.FuncSig, d.Body, d)
	return t, &ast.NodeProvenance{Node: d}
}
