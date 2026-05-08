package checker

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// declareLifetimeParams allocates a fresh LifetimeVar for each
// user-written lifetime parameter (the `<'a, 'b>` clause on a function
// signature) and registers it on the supplied scope so inline
// references like `mut 'a Point` inside the same signature resolve
// back to it. Returns the allocated LifetimeVars in declaration order
// — the caller stores them on `FuncType.LifetimeParams`.
//
// A duplicate name within the same `<>` clause is treated as a soft
// error: subsequent occurrences silently shadow the earlier binding.
// Phase 13 may upgrade this to a real diagnostic; for now we mirror
// how duplicate type parameters are handled.
func (c *Checker) declareLifetimeParams(
	scope *Scope,
	astParams []*ast.LifetimeAnn,
) []*type_system.LifetimeVar {
	if len(astParams) == 0 {
		return nil
	}
	out := make([]*type_system.LifetimeVar, len(astParams))
	for i, ann := range astParams {
		lv := c.FreshLifetimeVar(ann.Name)
		out[i] = lv
		scope.SetLifetimeVar(ann.Name, lv)
	}
	return out
}

// resolveLifetimeAnn turns an AST LifetimeAnnNode (a single `'a` or a
// union `('a | 'b)`) into the corresponding type_system.Lifetime,
// looking up each named lifetime in the current scope. The literal
// name "static" resolves to a `LifetimeValue{IsStatic: true}` rather
// than a LifetimeVar — matching how Phase 8.4's escape detection
// constructs its `'static` markers.
//
// Returns nil if the input is nil. An unknown lifetime name produces
// a fresh LifetimeVar AND an UndeclaredLifetimeError (§9.7 class 2);
// the fresh var keeps downstream unification well-formed even when
// the diagnostic is a warning rather than an error.
func (c *Checker) resolveLifetimeAnn(
	scope *Scope,
	node ast.LifetimeAnnNode,
) (type_system.Lifetime, []Error) {
	if node == nil {
		return nil, nil
	}
	switch n := node.(type) {
	case *ast.LifetimeAnn:
		return c.resolveSingleLifetime(scope, n)
	case *ast.LifetimeUnionAnn:
		var errors []Error
		members := make([]type_system.Lifetime, len(n.Lifetimes))
		for i, m := range n.Lifetimes {
			lt, lerrs := c.resolveSingleLifetime(scope, m)
			members[i] = lt
			errors = append(errors, lerrs...)
		}
		return &type_system.LifetimeUnion{Lifetimes: members}, errors
	default:
		return nil, nil
	}
}

func (c *Checker) resolveSingleLifetime(
	scope *Scope,
	ann *ast.LifetimeAnn,
) (type_system.Lifetime, []Error) {
	if ann.Name == "static" {
		return &type_system.LifetimeValue{Name: "static", IsStatic: true}, nil
	}
	if lv := scope.GetLifetimeVar(ann.Name); lv != nil {
		return lv, nil
	}
	// §9.7 class 2: the user wrote `'a` without declaring it on the
	// enclosing signature. Allocate a fresh var so downstream
	// unification still has something to bind, then report the
	// undeclared use. Severity is determined by whether *any*
	// enclosing scope has lifetime declarations: if some sibling
	// names exist, this is most likely a typo (warning); if no clause
	// exists at all, the user almost certainly forgot the `<'a>`
	// declaration entirely (error).
	//
	// Collect siblings *before* registering the fallback var so the
	// undeclared name does not appear in its own Suggestions list.
	siblings := collectVisibleLifetimeNames(scope)
	lv := c.FreshLifetimeVar(ann.Name)
	scope.SetLifetimeVar(ann.Name, lv)
	err := UndeclaredLifetimeError{
		Name:         ann.Name,
		Suggestions:  siblings,
		hasEnclosing: len(siblings) > 0,
		span:         ann.Span(),
	}
	return lv, []Error{err}
}

// resolveSelfLifetimeForFn resolves a `'a self` lifetime annotation on a
// method/getter/setter receiver after the function's lifetime parameters
// have already been baked into `fn.LifetimeParams`. Used by
// `inferObjectTypeAnn` (interface method type-annotations) where the
// per-method scope is no longer reachable but the function's
// LifetimeParams are. The lookup falls back to `enclosingScope` so the
// surrounding interface's `<'a>` is also visible.
func (c *Checker) resolveSelfLifetimeForFn(
	enclosingScope *Scope,
	fn *type_system.FuncType,
	node ast.LifetimeAnnNode,
) (type_system.Lifetime, []Error) {
	if node == nil {
		return nil, nil
	}
	scope := enclosingScope
	if len(fn.LifetimeParams) > 0 {
		scope = &Scope{Parent: enclosingScope, Namespace: type_system.NewNamespace()}
		for _, lp := range fn.LifetimeParams {
			scope.SetLifetimeVar(lp.Name, lp)
		}
	}
	return c.resolveLifetimeAnn(scope, node)
}

// collectVisibleLifetimeNames returns the user-written names of every
// LifetimeVar visible from `scope` upward. Used to populate suggestion
// lists for UndeclaredLifetimeError and to decide its severity.
func collectVisibleLifetimeNames(scope *Scope) []string {
	out := []string{}
	seen := map[string]bool{}
	for s := scope; s != nil; s = s.Parent {
		for name := range s.Lifetimes {
			if !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	return out
}
