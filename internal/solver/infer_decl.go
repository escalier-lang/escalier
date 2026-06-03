package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// inferDecl types a single top-level declaration and binds its name into scope.
// PR-2 wires only VarDecl (the `val`/`var` path); FuncDecl lands in PR-3 (where
// repeated top-level functions become overloads) and the remaining decl kinds in
// later milestones. Any not-yet-supported kind reports a clean
// UnsupportedNodeError (never a panic) and binds nothing.
//
// The VarDecl path always walks the initializer first (so its errors surface
// even when the binding itself can't be formed), then decides whether to bind:
//   - no initializer            → MissingInitializerError, bind nothing
//   - non-IdentPat (destructure) → UnsupportedNodeError, bind nothing
//   - name already bound here   → DuplicateDeclarationError, keep the first
//   - otherwise                  → defineValue
func (c *checker) inferDecl(scope *Scope, lvl int, d ast.Decl) {
	switch d := d.(type) {
	case *ast.VarDecl:
		b, ok := c.inferVarDecl(scope, lvl, d)
		if !ok {
			// No initializer: inferVarDecl already reported it and there is no
			// type to bind. Don't pollute the scope with a placeholder binding —
			// a later reference should still fail as an unknown identifier.
			return
		}
		name, named := varName(d)
		if !named {
			// Destructuring patterns (TuplePat/ObjectPat) need the tuple/record
			// types that arrive in M4. The initializer was already walked above
			// (its errors surfaced); report the pattern and bind nothing.
			c.report(&UnsupportedNodeError{
				errSpan: errSpan{span: d.Pattern.Span()},
				Kind:    astKind(d.Pattern),
			})
			return
		}
		if scope.hasOwnValue(name) {
			// A duplicate top-level `val`/`var` is a redeclaration error (unlike a
			// FuncDecl, whose duplicates are overloads). Keep the first binding.
			c.report(&DuplicateDeclarationError{
				errSpan: errSpan{span: d.Span()},
				Name:    name,
			})
			return
		}
		scope.defineValue(name, b)
	default:
		c.report(&UnsupportedNodeError{
			errSpan: errSpan{span: d.Span()},
			Kind:    astKind(d),
		})
	}
}

// inferVarDecl types a `val`/`var` declaration's initializer and returns the
// resulting MONOMORPHIC ValueBinding, with ok=false when there is no initializer
// to infer from. The initializer is typed via inferExpr and coalesced at
// Positive polarity (coalesce-at-binding, §7) so the stored type is var-free and
// stable: a later SCC can't mutate it, ValueBinding.Type stays inspectable, and
// it is the natural monomorphic stand-in for M3's PolyScheme.
//
// inferVarDecl always walks the initializer (so a malformed RHS still surfaces
// its errors) and records the type, but it does NOT bind anything — the caller
// owns the name lookup, duplicate check, and defineValue, so this routine serves
// both the module driver and the body-level redeclaration path (§3.2) unchanged.
//
// A `val`/`var` with no initializer needs a type annotation to infer from;
// annotation-driven binding lands with TypeAnn support in a later PR, so for now
// it reports MissingInitializerError and returns ok=false.
func (c *checker) inferVarDecl(scope *Scope, lvl int, d *ast.VarDecl) (ValueBinding, bool) {
	if d.Init == nil {
		name, _ := varName(d)
		c.report(&MissingInitializerError{
			errSpan: errSpan{span: d.Span()},
			Name:    name,
		})
		return ValueBinding{}, false
	}
	initType := c.inferExpr(scope, lvl, d.Init)
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
