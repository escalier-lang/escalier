package checker

import (
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// Phase 9.7: declared-vs-actual lifetime diagnostics.
//
// Three classes are reported here. The fourth (declared signature
// contradicts the body) is scaffolded but not fully implemented —
// running body-driven inference in a non-mutating "compare" mode
// requires refactoring inferLifetimesCore so that its leaf-writing
// sites can be redirected to a side buffer. That refactor is left as
// follow-up; the entry point checkDeclaredVsActualLifetimes is the
// hook where the comparison would land.

// UnusedLifetimeParamError reports a `<'a>` declaration that is not
// referenced by any parameter, return type, or throws annotation on
// the function. Severity: warning. The program is well-typed; the
// declaration is dead weight.
type UnusedLifetimeParamError struct {
	Name string
	span ast.Span
}

func (e UnusedLifetimeParamError) isError()        {}
func (e UnusedLifetimeParamError) IsWarning() bool { return true }
func (e UnusedLifetimeParamError) Span() ast.Span  { return e.span }
func (e UnusedLifetimeParamError) Message() string {
	return "lifetime parameter '" + e.Name + " is declared but never used"
}

// UndeclaredLifetimeError reports a use of `'a` inline in a type
// annotation when no enclosing function has declared `<'a>`. Without
// any enclosing `<>` clause the diagnostic is an error (the user
// almost certainly meant to declare it); when a clause exists with
// other lifetime names, it is a warning that probably indicates a
// typo, with Suggestions populated from siblings.
type UndeclaredLifetimeError struct {
	Name         string
	Suggestions  []string // sibling lifetime names declared in the nearest enclosing `<>`
	hasEnclosing bool     // whether an enclosing `<>` clause exists at all
	span         ast.Span
}

func (e UndeclaredLifetimeError) isError() {}
func (e UndeclaredLifetimeError) IsWarning() bool {
	// When a `<>` clause exists with siblings, this is most likely a
	// typo and we report it as a warning so callers can still proceed
	// (the fallback fresh LifetimeVar makes the type well-formed).
	// When no clause exists at all, an inline `'a` is unambiguously
	// undeclared and is a hard error.
	return e.hasEnclosing
}
func (e UndeclaredLifetimeError) Span() ast.Span { return e.span }
func (e UndeclaredLifetimeError) Message() string {
	msg := "lifetime '" + e.Name + " is used but not declared"
	if !e.hasEnclosing {
		msg += "; add `<'" + e.Name + ">` to the enclosing function signature"
	} else if len(e.Suggestions) > 0 {
		msg += "; did you mean '" + strings.Join(e.Suggestions, " or '") + "?"
	}
	return msg
}

// DeclaredLifetimeMismatchError reports a function signature whose
// declared lifetimes contradict what body-driven inference would have
// produced. Severity: error. Unlike the other two, this can be
// unsafe — callers will be told the result aliases p (or doesn't)
// while the body actually behaves the opposite way.
type DeclaredLifetimeMismatchError struct {
	Location string // e.g. "return type", "parameter 'p'"
	Declared type_system.Lifetime
	Inferred type_system.Lifetime
	span     ast.Span
}

func (e DeclaredLifetimeMismatchError) isError()        {}
func (e DeclaredLifetimeMismatchError) IsWarning() bool { return false }
func (e DeclaredLifetimeMismatchError) Span() ast.Span  { return e.span }
func (e DeclaredLifetimeMismatchError) Message() string {
	return "declared lifetime on " + e.Location +
		" disagrees with what the function body actually produces"
}

// reportUnusedLifetimeParams scans a fully-built FuncType for any
// LifetimeParams whose ID does not appear inline in the signature
// (param types, return type, throws type). Used by inferFuncSig and
// inferFuncTypeAnn after the function type has been assembled.
//
// astParams is the parallel AST `<'a, 'b>` clause from the original
// signature; it is used to attach a precise per-param span to each
// diagnostic. fallbackSpan covers the (rare) case where the AST and
// FuncType.LifetimeParams disagree in length — it points at the
// surrounding function declaration.
//
// Implements §9.7 class 1.
func reportUnusedLifetimeParams(
	fnType *type_system.FuncType,
	astParams []*ast.LifetimeAnn,
	fallbackSpan ast.Span,
) []Error {
	if len(fnType.LifetimeParams) == 0 {
		return nil
	}
	used := collectInlineLifetimeIDs(fnType)
	var errors []Error
	for i, lp := range fnType.LifetimeParams {
		if _, ok := used[lp.ID]; !ok {
			span := fallbackSpan
			if i < len(astParams) && astParams[i] != nil {
				span = astParams[i].Span()
			}
			errors = append(errors, UnusedLifetimeParamError{
				Name: lp.Name,
				span: span,
			})
		}
	}
	return errors
}

// collectInlineLifetimeIDs walks a FuncType's parameter / return /
// throws types and returns the set of LifetimeVar IDs that appear at
// any depth — covering both the outer Lifetime field and per-element
// LifetimeArgs on TypeRefTypes, plus lifetimes inside nested function
// types' params/return.
func collectInlineLifetimeIDs(fnType *type_system.FuncType) map[int]struct{} {
	out := map[int]struct{}{}
	// Lifetimes declared on a *nested* FuncType's LifetimeParams shadow
	// the outer scope's same-named vars, so we don't recurse through
	// nested signatures with their own LifetimeParams — the outer-only
	// IDs we collect here are the ones the outer signature uses.
	visited := map[type_system.Type]bool{}
	for _, p := range fnType.Params {
		collectLifetimeIDsInType(p.Type, fnType, out, visited)
	}
	if fnType.Return != nil {
		collectLifetimeIDsInType(fnType.Return, fnType, out, visited)
	}
	if fnType.Throws != nil {
		collectLifetimeIDsInType(fnType.Throws, fnType, out, visited)
	}
	return out
}

func collectLifetimeIDsInType(
	t type_system.Type,
	owner *type_system.FuncType,
	out map[int]struct{},
	visited map[type_system.Type]bool,
) {
	if t == nil || visited[t] {
		return
	}
	visited[t] = true
	t = type_system.Prune(t)
	switch ty := t.(type) {
	case *type_system.TypeRefType:
		collectLifetimeIDs(ty.Lifetime, out)
		for _, la := range ty.LifetimeArgs {
			collectLifetimeIDs(la, out)
		}
		for _, a := range ty.TypeArgs {
			collectLifetimeIDsInType(a, owner, out, visited)
		}
	case *type_system.ObjectType:
		collectLifetimeIDs(ty.Lifetime, out)
		for _, e := range ty.Elems {
			if pe, ok := e.(*type_system.PropertyElem); ok {
				collectLifetimeIDsInType(pe.Value, owner, out, visited)
			}
		}
	case *type_system.TupleType:
		collectLifetimeIDs(ty.Lifetime, out)
		for _, e := range ty.Elems {
			collectLifetimeIDsInType(e, owner, out, visited)
		}
	case *type_system.MutType:
		collectLifetimeIDsInType(ty.Type, owner, out, visited)
	case *type_system.UnionType:
		for _, m := range ty.Types {
			collectLifetimeIDsInType(m, owner, out, visited)
		}
	case *type_system.IntersectionType:
		for _, m := range ty.Types {
			collectLifetimeIDsInType(m, owner, out, visited)
		}
	case *type_system.FuncType:
		// A nested FuncType binds its own LifetimeParams. Only treat
		// inline lifetimes whose ID matches a LifetimeParam of `owner`
		// (or any outer scope) as "used by owner" — names locally bound
		// by the inner function don't count.
		shadowed := map[int]bool{}
		for _, lp := range ty.LifetimeParams {
			shadowed[lp.ID] = true
		}
		// Recurse but skip lifetimes shadowed by the inner function.
		nested := map[int]struct{}{}
		nestedVisited := map[type_system.Type]bool{}
		for _, p := range ty.Params {
			collectLifetimeIDsInType(p.Type, ty, nested, nestedVisited)
		}
		if ty.Return != nil {
			collectLifetimeIDsInType(ty.Return, ty, nested, nestedVisited)
		}
		if ty.Throws != nil {
			collectLifetimeIDsInType(ty.Throws, ty, nested, nestedVisited)
		}
		for id := range nested {
			if !shadowed[id] {
				out[id] = struct{}{}
			}
		}
	}
}

func collectLifetimeIDs(lt type_system.Lifetime, out map[int]struct{}) {
	if lt == nil {
		return
	}
	switch v := type_system.PruneLifetime(lt).(type) {
	case *type_system.LifetimeVar:
		out[v.ID] = struct{}{}
	case *type_system.LifetimeUnion:
		for _, m := range v.Lifetimes {
			collectLifetimeIDs(m, out)
		}
	}
}

// checkDeclaredVsActualLifetimes is the entry point for §9.7 class 3
// (declared signature contradicts the body). Currently scaffolded —
// it would need a non-mutating variant of inferLifetimesCore that
// collects what *would* have been written to each leaf and compares
// against what the user declared. Implementing that requires
// refactoring inferLifetimesCore's writing sites (Phase 8.4 escape
// detection at line ~189, the return-source pass that follows) to
// accept an output collector instead of mutating the type directly,
// then running a "compare" pass after the body has been type-checked.
//
// Kept here as a documented hook so the future implementation has a
// single landing point and the §9.7 scaffolding is visible.
func checkDeclaredVsActualLifetimes(
	_ *type_system.FuncType,
	_ ast.Node,
) []Error {
	// TODO(#phase-9.7): implement the compare pass. See
	// internal/checker/infer_lifetime.go:inferLifetimesCore — the
	// LifetimeParams != 0 early-return at the top would become a
	// branch into a non-mutating walker that produces a parallel set
	// of would-write-this-lifetime decisions, then this function
	// diffs them against the declared signature.
	return nil
}
