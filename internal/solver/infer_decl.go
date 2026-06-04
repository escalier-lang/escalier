package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/provenance"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// inferDeclDef infers a single top-level declaration's definition for the SCC
// driver (inferComponent) and returns the RAW (un-coalesced) soltype.Type to
// constrain against the binding's group var, the introducing decl's source
// provenance, and ok=false when the decl introduces no value. It does NOT bind
// the name — inferComponent owns scope placement, so the group var stays visible
// to every body before any of them is constrained (the LetRecGroup discipline).
//
// The type is returned RAW (variable-carrying), not coalesced: inferComponent
// coalesces the group var once, after every member has constrained it (phase 3).
// Coalescing a `val` initializer here instead would read a recursive peer's group
// var while it is still empty and freeze the binding to `never` — the bug a `val`
// inside a recursive group hit before this was split out of inferVarDecl.
//
// ok=false cases, each already reported:
//   - VarDecl without an initializer → MissingInitializerError
//   - VarDecl with a destructuring pattern → UnsupportedNodeError
//   - any decl kind outside the M2 subset → UnsupportedNodeError
//
// The VarDecl path walks the initializer first (so a malformed RHS still surfaces
// its errors) before reporting an unsupported pattern.
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
			c.report(&UnsupportedNodeError{
				errSpan: errSpan{span: d.Pattern.Span()},
				Kind:    astKind(d.Pattern),
			})
			return nil, nil, false
		}
		return initType, &ast.NodeProvenance{Node: d}, true
	case *ast.FuncDecl:
		b := c.inferFuncDecl(scope, lvl, d)
		return b.Type, b.Source, true
	default:
		c.report(&UnsupportedNodeError{
			errSpan: errSpan{span: d.Span()},
			Kind:    astKind(d),
		})
		return nil, nil, false
	}
}

// inferVarDeclInit types a `val`/`var` declaration's initializer and returns its
// RAW (un-coalesced, variable-carrying) type, with ok=false when there is no
// initializer to infer from (MissingInitializerError reported). It is the shared
// core of both binding paths: the SCC driver (inferDeclDef) constrains this raw
// type against the binding's group var and coalesces the var once at group
// completion, while the body-level path (inferVarDecl) coalesces it immediately
// for a stable stored binding. It walks the initializer regardless (so a
// malformed RHS still surfaces its errors) and binds nothing — the caller owns
// scope placement.
//
// A `val`/`var` with no initializer needs a type annotation to infer from;
// annotation-driven binding lands with TypeAnn support in a later PR, so for now
// it reports MissingInitializerError and returns ok=false.
func (c *checker) inferVarDeclInit(scope *Scope, lvl int, d *ast.VarDecl) (soltype.Type, bool) {
	if d.Init == nil {
		name, _ := varName(d)
		c.report(&MissingInitializerError{
			errSpan: errSpan{span: d.Span()},
			Name:    name,
		})
		return nil, false
	}
	return c.inferExpr(scope, lvl, d.Init), true
}

// inferVarDecl types a `val`/`var` declaration into a MONOMORPHIC ValueBinding,
// coalescing the initializer at Positive polarity (coalesce-at-binding, §7) so
// the stored type is var-free and stable. This is the body-level redeclaration
// path (§3.2); the module/SCC driver instead uses inferVarDeclInit's raw type so
// a recursive group coalesces only at completion (see inferDeclDef). ok=false
// when there is no initializer to infer from.
func (c *checker) inferVarDecl(scope *Scope, lvl int, d *ast.VarDecl) (ValueBinding, bool) {
	initType, ok := c.inferVarDeclInit(scope, lvl, d)
	if !ok {
		return ValueBinding{}, false
	}
	t := coalesce(initType, soltype.Positive)
	c.recordType(d.Pattern, t)
	return ValueBinding{Type: t, Source: &ast.NodeProvenance{Node: d}}, true
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
	return ValueBinding{Type: t, Source: &ast.NodeProvenance{Node: d}}
}
