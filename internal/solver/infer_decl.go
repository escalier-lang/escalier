package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/provenance"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// inferDeclDef infers one top-level declaration for the SCC driver
// (inferComponent) and returns its RAW (un-coalesced, variable-carrying) type to
// constrain against the binding's binding var, the decl's provenance, and ok=false
// when it introduces no value. It does NOT bind the name — inferComponent owns
// scope placement.
//
// The type MUST stay raw: inferComponent coalesces the binding var once, after every
// group member has constrained it (phase 3). Coalescing a `val` initializer here
// would read a recursive peer's still-empty binding var and freeze the binding to
// `never` — the bug behind splitting this out of inferVarDecl.
//
// ok=false cases, each already reported:
//   - VarDecl without an initializer → MissingInitializerError
//   - VarDecl with a destructuring pattern → UnsupportedNodeError (initializer
//     still walked first, so it surfaces its own errors)
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
		// inferComponent constrains that raw type into the binding var, generalizes
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
// binding var once at completion, while inferVarDecl (body-level) coalesces it now.
// Walks the initializer regardless so it still surfaces its own errors, and
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
	switch {
	case d.TypeAnn != nil:
		// M2.5: constrain the initializer against the annotation (the one
		// non-provenance addition, §3.7), so `val x: number = "hi"` produces a
		// CannotConstrainError whose Sub (the "hi" literal) carries a
		// LiteralInference origin — precise blame, with the annotation as the
		// related node. The constraint node is the initializer, so even the
		// fallback span is the initializer, not the whole decl; the binding then
		// adopts the annotated type.
		//
		// Skip both the check and the adoption when the annotation is unsupported
		// (ok=false): resolveTypeAnn already reported it and returned a `never`
		// placeholder, so constraining `initT <: never` would cascade a spurious
		// error and adopting `never` would poison the binding. Keep the inferred
		// initializer type instead (error recovery).
		if annT, ok := c.resolveTypeAnn(d.TypeAnn, lvl); ok {
			c.constrainInitAgainstAnnotation(d.Init, initT, annT)
			c.checkExcessLiteralMembers(d.Init, initT, annT)
			initT = annT
		}
	case d.Kind == ast.VarKind:
		// M4 B3: eager-widen a DIRECT literal initializer to its primitive at the
		// constraint level (`5` ⇒ number), recursively through objects/tuples. This
		// is the constraint-level half of widening: the widened type propagates
		// through the bound graph, so a read of the binding widens too
		// (`var a = 5; val z = a` ⇒ z: number). A literal that arrives through a
		// REFERENCE (`var y = x`) is a type variable here, which widen passes
		// through unchanged. The binding var's Widenable flag widens that at coalesce
		// time. That covers display, the reassignment slot, and the binding's own
		// rendered type. A `val` keeps its literal singleton; only a mutable cell
		// widens.
		initT = widen(initT)
	}
	return initT, true
}

// constrainInitAgainstAnnotation constrains a `val`/`var` initializer against its
// resolved annotation, with one refinement over a plain constrain: a freshly
// constructed, unaliased initializer flowing into an OWNED-mutable annotation is
// allowed to take that mutable type.
//
// The C2 gate rejects immutable <: mutable structurally, because writing through the
// target would otherwise mutate a read-only value. But that is only unsound when a
// live immutable alias to the source exists. A freshly constructed literal has none —
// it is uniquely owned — so granting it the annotated mutable type is safe. This is
// Rule 2 with an empty alias set, the construction case `val items: mut {x} = {x: 1}`.
// The decision belongs at this value-flow site, which can see the source is fresh, not
// in the liveness-blind constrain engine.
//
// The upgrade constrains the initializer's shape against the borrow's INNER, the
// covariant read view, exactly as the non-mut path constrains against the annotation
// directly. It does not relate two independent mutable references, so no write-view
// invariance applies. It is gated on an owned-mutable annotation (Lt == nil): a borrow
// annotation (`'a mut …`) is a reference into a caller's region, not an owned value, so
// a fresh source flowing into it stays on the strict path. A non-fresh source — a
// variable, a call, a member access — also stays strict; its Rule 2 safety depends on
// liveness or the lifetime/region system (M4 G2), not on syntax.
func (c *checker) constrainInitAgainstAnnotation(init ast.Expr, initT, annT soltype.Type) {
	if ref, ok := annT.(*soltype.RefType); ok && ref.Mut && ref.Lt == nil && isFreshlyConstructed(init) {
		c.constrain(init, initT, ref.Inner)
		return
	}
	c.constrain(init, initT, annT)
}

// isFreshlyConstructed reports whether e is a syntactically fresh, unaliased value: a
// literal, or an object/tuple literal whose every element is itself freshly
// constructed. Such a value captures no reference to an existing binding, so it is
// uniquely owned. It is deliberately conservative and identifier-free: any IdentExpr,
// call, member access, or spread disqualifies the whole expression, because a captured
// variable could alias a value held immutably elsewhere. Being identifier-free also
// makes it sound without VarIDs, so it holds at module top level where the liveness
// pre-pass has not run. A richer freshness judgment over provably-unique non-literal
// expressions is the lifetime/region work (M4 G2).
func isFreshlyConstructed(e ast.Expr) bool {
	switch e := e.(type) {
	case *ast.LiteralExpr:
		return true
	case *ast.TupleExpr:
		for _, elem := range e.Elems {
			if !isFreshlyConstructed(elem) {
				return false
			}
		}
		return true
	case *ast.ObjectExpr:
		for _, elem := range e.Elems {
			prop, ok := elem.(*ast.PropertyExpr)
			if !ok || prop.Value == nil {
				return false
			}
			if !isFreshlyConstructed(prop.Value) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// checkExcessLiteralMembers is the construction-site excess check (M4 A3): a
// syntactic object/tuple LITERAL checked against an object/tuple annotation may
// not carry members the target does not declare, EVEN when the target is inexact
// (`{x, ...}` / `[number, ...]`). It is the construction-site twin of the
// direct-call extra-arg rule: a non-literal source takes ordinary width
// subtyping (an inexact target admits extras), but a literal spells out its
// members, so an undeclared one is a construction error.
//
// SCOPE (M4 A3): it is wired here, at annotated `val`/`var` bindings, only. The
// twin sites the rule will eventually cover — literal call arguments and return
// positions checked against an inexact annotation — defer to the milestone work
// that adds annotated call/return checking; until then a literal flowing into an
// inexact target through those paths takes ordinary width subtyping.
//
// It fires only for an INEXACT target. An exact target already rejects extras
// through the ordinary ObjectType/TupleType constrain arm (ExtraPropertyError /
// TupleLengthMismatchError run by the c.constrain call above), so checking it here
// too would double-report. The errors reuse A1's ExtraPropertyError for objects
// and the parallel ExtraElementError for tuples, wired with the same Prov table /
// site as the constraint path so blame resolves to the offending member.
//
// A `mut T` annotation resolves to a RefType wrapping the object/tuple, so peel
// the borrow first: the excess rule is about the literal's SHAPE versus the
// target's declared members, which is independent of the borrow's mutability. The
// literal source itself is never a borrow, so only the annotation needs peeling.
func (c *checker) checkExcessLiteralMembers(e ast.Expr, sub, annT soltype.Type) {
	switch ann := soltype.CarrierOf(annT).(type) {
	case *soltype.ObjectType:
		if !ann.Inexact {
			return
		}
		obj, isLit := e.(*ast.ObjectExpr)
		subObj, isObj := sub.(*soltype.ObjectType)
		if !isLit || !isObj {
			return
		}
		for _, elem := range obj.Elems {
			prop, ok := elem.(*ast.PropertyExpr)
			if !ok || prop.Value == nil {
				continue
			}
			name, ok := objKeyName(prop.Name)
			if !ok {
				continue
			}
			if _, declared := ann.Prop(name); declared {
				continue
			}
			err := &ExtraPropertyError{Sub: subObj, Super: ann, Name: name}
			err.prov, err.site = c.prov, prop
			c.errs = append(c.errs, err)
		}
	case *soltype.TupleType:
		if !ann.Inexact {
			return
		}
		tup, isLit := e.(*ast.TupleExpr)
		subTup, isTup := sub.(*soltype.TupleType)
		if !isLit || !isTup {
			return
		}
		// Elements beyond the target's declared prefix are excess on the literal.
		for i := len(ann.Elems); i < len(tup.Elems) && i < len(subTup.Elems); i++ {
			err := &ExtraElementError{Sub: subTup, Super: ann, Index: i}
			err.prov, err.site = c.prov, tup.Elems[i]
			c.errs = append(c.errs, err)
		}
	}
}

// inferVarDecl types a body-level `val`/`var` into a GENERALIZED ValueBinding —
// the let-polymorphism rule (M3, PR1) that replaces M2's coalesce-at-binding
// freeze. It infers the initializer one level deeper (lvl+1) and generalizes at
// lvl: variables created in the initializer (level > lvl) become reusable type parameters,
// while variables captured from an enclosing scope (level <= lvl) stay shared — so
// `fn (y) { val getY = fn () { y }; [getY(), getY()] }` keeps getY's captured `y`
// instead of freezing it to `never`, the bug eager coalescing caused. A body-level
// `val` is never recursive (the name is bound only after its initializer is typed),
// so there is no pre-binding or binding var; the SCC driver owns the recursive top-level
// path (see inferDeclDef). ok=false when there is no initializer.
func (c *checker) inferVarDecl(scope *Scope, lvl int, d *ast.VarDecl) (ValueBinding, bool) {
	initType, ok := c.inferVarDeclInit(scope, lvl+1, d)
	if !ok {
		return ValueBinding{}, false
	}
	bound := initType
	// M4 B3: an un-annotated body-level `var` widens at coalesce time like its
	// top-level peer. The top-level SCC path flags the binding var it already
	// mints; a body-level binding has none — it generalizes the initializer
	// directly — so wrap the initializer in a fresh widenable var (initType <: v)
	// and generalize that. The flag rides v through coalescing, so the recorded
	// display type, the reassignment slot, and any read of the binding all widen
	// consistently. An annotated `var` adopts its annotation and needs no widening.
	//
	// Skip the wrap when the initializer recovered to the ErrorType sentinel: it
	// absorbs in constrain, so `initType <: v` would leave v with no lower bound,
	// coalescing to `never` and cascading a spurious error at every reassignment.
	// Generalizing the ErrorType directly keeps the absorbing recovery binding.
	_, isErr := initType.(*soltype.ErrorType)
	if d.Kind == ast.VarKind && d.TypeAnn == nil && !isErr {
		v := c.freshAt(lvl + 1)
		v.Widenable = true
		c.constrain(d.Init, initType, v)
		bound = v
	}
	scheme := c.generalize(bound, lvl)
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

// inferDestructureDecl types a body-level destructuring `val`/`var` such as
// `val {x, y} = p` or `val [a, b] = t` (M4 E1). It types the initializer through
// the shared inferVarDeclInit core, so an annotation is honored and a `var`
// initializer is widened, then binds the pattern's leaves against that type via
// bindPattern.
//
// The leaves are MONOMORPHIC projections of the initializer, not independently
// generalized bindings. A destructured name reads a slot of the initializer, so
// it shares that slot's type rather than quantifying over it. Top-level
// destructuring at module scope still defers. It needs the SCC driver to bind
// several names from one decl, so this path is body-level only and inferDeclDef
// reports a top-level destructuring decl as unsupported.
//
// After binding, the leaves are wired into the liveness machinery so a closure
// capturing a leaf resolves its alias set and a mutability transition through it
// is checked. This is the per-leaf analogue of the IdentPat path's VarID copy
// plus trackAliasesForVarDecl.
func (c *checker) inferDestructureDecl(scope *Scope, lvl int, d *ast.VarDecl) {
	initType, ok := c.inferVarDeclInit(scope, lvl, d)
	if !ok {
		return
	}
	c.bindPattern(scope, lvl, d.Pattern, initType, nil)
	c.trackDestructureLeaves(scope, d.Pattern)
}

// varName returns the bound name of a VarDecl whose pattern is an IdentPat, with
// ok=false for any other pattern shape. M2 binds IdentPat-only patterns,
// mirroring M1's IdentPat-only FuncParam. Body-level destructuring such as
// `val [a, b] = …` is handled by inferDestructureDecl (M4 E1). Top-level
// destructuring still defers, since it needs the SCC driver to bind several names
// from one decl.
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
