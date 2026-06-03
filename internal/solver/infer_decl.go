package solver

import (
	"fmt"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// inferDecl types a single top-level declaration and binds its name into scope.
// PR-2 wires only VarDecl (the `val`/`var` path); FuncDecl lands in PR-3 and the
// remaining decl kinds in later milestones. Any not-yet-supported kind reports a
// clean UnsupportedNodeError (never a panic) and binds nothing.
func (c *checker) inferDecl(scope *Scope, lvl int, d ast.Decl) {
	switch d := d.(type) {
	case *ast.VarDecl:
		name, ok := varName(d)
		if !ok {
			// Destructuring patterns (TuplePat/ObjectPat) need the tuple/record
			// types that arrive in M4; until then a non-IdentPat binding is
			// outside the M2 subset. Report and bind nothing.
			c.report(&UnsupportedNodeError{
				errSpan: errSpan{span: d.Pattern.Span()},
				Kind:    patKind(d.Pattern),
			})
			return
		}
		b := c.inferVarDecl(scope, lvl, d)
		scope.defineValue(name, b)
	default:
		c.report(&UnsupportedNodeError{
			errSpan: errSpan{span: d.Span()},
			Kind:    declKind(d),
		})
	}
}

// inferVarDecl types a `val`/`var` declaration's initializer and returns the
// resulting MONOMORPHIC ValueBinding. The initializer is typed via inferExpr and
// coalesced at Positive polarity (coalesce-at-binding, §7) so the stored type is
// var-free and stable: a later SCC can't mutate it, ValueBinding.Type stays
// inspectable, and it is the natural monomorphic stand-in for M3's PolyScheme.
//
// The caller (inferDecl, and the body walk in a later PR) owns the name lookup
// and defineValue, so this routine serves both the module driver and body-level
// redeclaration (§3.2) unchanged.
//
// A `val`/`var` with no initializer needs a type annotation to infer from;
// annotation-driven binding lands with TypeAnn support in a later PR, so for now
// it is outside the PR-2 subset and reports UnsupportedNodeError.
func (c *checker) inferVarDecl(scope *Scope, lvl int, d *ast.VarDecl) ValueBinding {
	source := &ast.NodeProvenance{Node: d}
	if d.Init == nil {
		return ValueBinding{
			Type: c.report(&UnsupportedNodeError{
				errSpan: errSpan{span: d.Span()},
				Kind:    "VarDecl without initializer",
			}),
			Source: source,
		}
	}
	initType := c.inferExpr(scope, lvl, d.Init)
	t := coalesce(initType, soltype.Positive)
	c.recordType(d.Pattern, t)
	return ValueBinding{Type: t, Source: source}
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

// declKind returns a short surface name for a declaration node, used in the M2
// subset-guard error message. It strips the leading "*ast." from the Go type
// name so e.g. *ast.TypeDecl renders as "TypeDecl". Mirrors exprKind.
func declKind(d ast.Decl) string {
	return strings.TrimPrefix(fmt.Sprintf("%T", d), "*ast.")
}

// patKind returns a short surface name for a pattern node, used when a binding
// pattern is outside M2's IdentPat-only subset.
func patKind(p ast.Pat) string {
	return strings.TrimPrefix(fmt.Sprintf("%T", p), "*ast.")
}
