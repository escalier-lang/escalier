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
//
// Lifetimes declared on a *nested* FuncType's LifetimeParams shadow
// the outer scope's same-named vars, so the visitor pushes a shadow
// frame on entry to each inner FuncType and pops it on exit; only
// IDs not bound by any inner frame count as "used by the outer".
func collectInlineLifetimeIDs(fnType *type_system.FuncType) map[int]struct{} {
	c := &lifetimeIDCollector{out: map[int]struct{}{}}
	for _, p := range fnType.Params {
		p.Type.Accept(c)
	}
	if fnType.Return != nil {
		fnType.Return.Accept(c)
	}
	if fnType.Throws != nil {
		fnType.Throws.Accept(c)
	}
	return c.out
}

// lifetimeIDCollector is a read-only TypeVisitor that records every
// LifetimeVar ID appearing inline on a TypeRefType / ObjectType /
// TupleType, while honoring shadowing introduced by nested FuncType
// LifetimeParams.
type lifetimeIDCollector struct {
	out         map[int]struct{}
	shadowStack []map[int]bool
	depth       int // FuncType nesting depth; we shadow only inner FuncTypes
}

func (c *lifetimeIDCollector) EnterType(t type_system.Type) type_system.EnterResult {
	switch ty := t.(type) {
	case *type_system.TypeRefType:
		c.addLifetime(ty.Lifetime)
		for _, la := range ty.LifetimeArgs {
			c.addLifetime(la)
		}
	case *type_system.ObjectType:
		c.addLifetime(ty.Lifetime)
	case *type_system.TupleType:
		c.addLifetime(ty.Lifetime)
	case *type_system.FuncType:
		// Only inner FuncTypes introduce a shadow frame; the outer
		// function's LifetimeParams are the IDs we want to collect.
		if c.depth > 0 {
			frame := map[int]bool{}
			for _, lp := range ty.LifetimeParams {
				frame[lp.ID] = true
			}
			c.shadowStack = append(c.shadowStack, frame)
		}
		c.depth++
	}
	return type_system.EnterResult{}
}

func (c *lifetimeIDCollector) ExitType(t type_system.Type) type_system.Type {
	if _, ok := t.(*type_system.FuncType); ok {
		c.depth--
		if c.depth > 0 {
			c.shadowStack = c.shadowStack[:len(c.shadowStack)-1]
		}
	}
	return nil
}

func (c *lifetimeIDCollector) addLifetime(lt type_system.Lifetime) {
	if lt == nil {
		return
	}
	switch v := type_system.PruneLifetime(lt).(type) {
	case *type_system.LifetimeVar:
		for _, frame := range c.shadowStack {
			if frame[v.ID] {
				return
			}
		}
		c.out[v.ID] = struct{}{}
	case *type_system.LifetimeUnion:
		for _, m := range v.Lifetimes {
			c.addLifetime(m)
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
