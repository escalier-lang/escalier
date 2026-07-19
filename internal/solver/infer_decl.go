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
// scope placement. ns is the decl's dep_graph namespace, used only by the ClassDecl
// arm to reconstruct the class's qualified registry key; every other kind ignores it.
//
// The type MUST stay raw: inferComponent coalesces the binding var once, after every
// group member has constrained it (phase 3). Coalescing a `val` initializer here
// would read a recursive peer's still-empty binding var and freeze the binding to
// `never` — the bug behind splitting this out of inferVarDecl.
//
// A top-level destructuring VarDecl never reaches here. inferComponent intercepts
// each of its leaf keys via destructureDecl and binds them through
// bindModuleDestructureLeaf (M4 E3). The destructuring arm below is a defensive
// guard for a hand-built AST that bypasses that path.
//
// ok=false cases, each already reported:
//   - VarDecl without an initializer → MissingInitializerError
//   - VarDecl with a destructuring pattern → UnsupportedNodeError (initializer
//     still walked first, so it surfaces its own errors)
//   - any decl kind outside the M2 subset → UnsupportedNodeError
func (c *checker) inferDeclDef(scope *Scope, lvl int, d ast.Decl, ns string) (soltype.Type, provenance.Provenance, bool) {
	switch d := d.(type) {
	case *ast.VarDecl:
		if d.Else != nil {
			return c.inferModuleValElse(scope, lvl, d)
		}
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
	case *ast.ClassDecl:
		// inferClassDecl returns the RAW class value for the SCC driver and registers the
		// instance type binding plus the nominal ClassDef; ns is the namespace for keying.
		return c.inferClassDecl(scope, lvl, d, ns)
	default:
		c.reportUnsupported(d)
		return nil, nil, false
	}
}

// inferModuleValElse types a module-level `val pat = init else { … }` binding,
// returning the bound type for the SCC driver to place and generalize. A
// non-diverging else supplies the binding's fallback value, exactly as at body level,
// so `val num: number = u else { 0 }` is a valid top-level binding. A diverging else's
// `return` reports ReturnOutsideFunctionError on its own, since module scope has no
// enclosing function. Only a single identifier is bound at module scope; module-level
// destructuring is deferred, mirroring the plain `val`/`var` path.
func (c *checker) inferModuleValElse(scope *Scope, lvl int, d *ast.VarDecl) (soltype.Type, provenance.Provenance, bool) {
	if d.Init == nil {
		c.report(&MissingInitializerError{Decl: d})
		return nil, nil, false
	}
	ip, ok := d.Pattern.(*ast.IdentPat)
	if !ok {
		if d.Pattern != nil {
			c.reportUnsupported(d.Pattern)
		} else {
			c.reportUnsupported(d)
		}
		return nil, nil, false
	}
	initType := c.inferExpr(scope, lvl, d.Init)
	// The else runs only on a failed match, so it cannot see the binding.
	elseT, elseDiverges := c.inferBlock(scope.Child(), lvl, d.Else)

	var bound soltype.Type
	switch {
	case d.TypeAnn != nil:
		// An annotated narrowing pins the binding; a non-diverging fallback must fit it.
		narrowed, resolved := c.resolveTypeAnn(scope, d.TypeAnn, lvl)
		if !resolved {
			narrowed = initType
		} else {
			c.constrain(d, narrowed, initType)
		}
		if !elseDiverges {
			c.constrain(d, elseT, narrowed)
		}
		bound = narrowed
	case elseDiverges:
		bound = initType
	default:
		// The binding is the matched initializer OR the non-diverging fallback.
		res := c.freshAt(lvl)
		c.recordProv(res, d, ValElseBranch)
		c.constrain(d, initType, res)
		c.constrain(d, elseT, res)
		bound = res
	}
	c.recordType(ip, bound)
	return bound, &ast.NodeProvenance{Node: d}, true
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
	case d.TypeAnn == nil && isMutableIdentPat(d.Pattern) && freshLiteralShape(d.Init, c.acceptsBorrowLeaf):
		// An unannotated `val mut q = {…}` / `var mut q = {…}` from a freshly
		// constructed literal constructs an owned-mutable value. This mirrors the
		// annotated `val q: mut {x} = {x: 1}` upgrade in
		// constrainInitAgainstAnnotation, which uses the same fresh-literal
		// reasoning. A fresh literal is uniquely owned, so granting it the mutable
		// type aliases nothing. The literal's fields widen, since a mutable cell
		// admits any value of the field's primitive type. So `val mut q = {x: 1}`
		// is `mut {x: number}`. A `mut {x: 1}` would reject the ordinary write
		// `q.x = 2`. A borrow leaf is admitted too: `val mut b = {peer: &mut d}`
		// builds the owned-mutable `mut {peer: &mut {…}}`, whose field is repointable
		// and which is `&mut`-borrowable. A primitive initializer is not borrowable and
		// has no interior mutability to make mutable, so it falls back to the
		// var-widening and val-keep behaviour below.
		widened := widen(initT)
		if inner, ok := widened.(soltype.RefInner); ok {
			ref := soltype.NewRef(true, nil, inner)
			c.recordProv(ref, d.Init, OwnedMutConstruction)
			initT = ref
		} else if d.Kind == ast.VarKind {
			initT = widened
		}
	case d.TypeAnn == nil && c.bindingMovesOwnedPlace(d.Pattern, d.Init, initT):
		// `val q = p` / `val mut q = p` moves an owned value out of the place `p`
		// into the new binding `q`. The move consumes `p` and leaves `q` the sole
		// owner, so `q`'s mutability is taken from the binding pattern rather than
		// inherited from `p`. A `val mut q` thaws an owned-immutable source into an
		// owned-mutable binding, and a plain `val q` freezes an owned-mutable source
		// into an owned-immutable one. consumeBindingInit records the consume for the
		// same binding, and trackAliasesForIdentPat seeds `q` as a fresh owner.
		if isMutableIdentPat(d.Pattern) {
			// Thaw: widen the source's literal fields and wrap the result in an
			// owned-mutable cell, the same owned-mutable cell the fresh-literal
			// `val mut q = {…}` upgrade above produces. A place move's source is an
			// object, tuple, or owned RefType, so the widened type is a RefInner and
			// the wrap always applies.
			if inner, ok := widen(stripOwnedMut(initT)).(soltype.RefInner); ok {
				ref := soltype.NewRef(true, nil, inner)
				c.recordProv(ref, d.Init, OwnedMutConstruction)
				initT = ref
			}
		} else {
			// Freeze: take the source's immutable skeleton, so a later write through
			// `q` is rejected. A `var` binding still widens so a reassignment can store
			// another value of the widened shape.
			initT = stripOwnedMut(initT)
			if d.Kind == ast.VarKind {
				initT = widen(initT)
			}
		}
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
		if annT, ok := c.resolveTypeAnn(scope, d.TypeAnn, lvl); ok {
			annT = c.constrainInitAgainstAnnotation(d.Init, initT, annT)
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
		// time. That covers display, the reassignment target, and the binding's own
		// rendered type. A `val` keeps its literal singleton; only a mutable cell
		// widens.
		initT = widen(initT)
	}
	return initT, true
}

// constrainInitAgainstAnnotation constrains a `val`/`var` initializer against its
// resolved annotation and returns the type the binding adopts, which may differ from
// the written annotation in three refinements over a plain constrain.
//
// 0. A generic function annotation is checked against a fresh instance, so the adopted
// annotation keeps its declared `<T>` as its only quantifier. See the inline note.
//
// 1. A uniquely-owned initializer flowing into an OWNED-mutable annotation is allowed to
// take that mutable type. The C2 gate rejects immutable <: mutable structurally, because
// writing through the target would otherwise mutate a read-only value. But that is only
// unsound when a live immutable alias to the source exists. A uniquely-owned source has
// none, so granting it the annotated mutable type is safe. This is Rule 2 with an empty
// alias set. Two sources qualify, both recognised by canUpgradeToOwnedMut: a freshly
// constructed literal such as `val items: mut {x} = {x: 1}`, and a consuming move of an
// owned place such as `val m: mut {x} = cfg` where `cfg` is dead afterward.
//
// The decision belongs at this value-flow site rather than inside the constraint solver.
// The solver, c.constrain, compares only the two types. It has no access to the source
// expression, to variable liveness, or to the move analysis, so it cannot tell whether
// the source is the sole owner of its value or is shared with a live immutable alias.
// Lacking that information it always rejects immutable <: mutable. That information is
// visible only here, so this site decides whether the source is uniquely owned and then
// hands the solver a check it can do soundly.
//
// tryUpgradeToOwnedMut constrains the initializer's shape against the borrow's INNER, its
// covariant read view, exactly as the non-mut path constrains against the annotation
// directly. A borrow annotation is a reference into a caller's region, not an owned value,
// so a source flowing into it stays on the strict path.
//
// 2. A bare owned annotation whose initializer is a borrow is a borrow-into-owned
// escape. A `val` binding consumes an owned source, so a borrowed `p` flowing into a
// bare owned destination, as in `val q: {x} = p`, does not alias `p`. The constraint takes the
// ordinary RefType<:bare arm, which trips BorrowEscapeError. The explicit `&` form
// `val q: &{x} = p` is the opt-in for an alias.
func (c *checker) constrainInitAgainstAnnotation(init ast.Expr, initT, annT soltype.Type) soltype.Type {
	// Check a generic function annotation in checking mode and adopt the untouched
	// annotation, so it renders `fn <T>(x: T) -> T`. Each declared type parameter is held
	// rigid as a skolem while the initializer is checked against it, so a body that forces a
	// parameter to a concrete value is rejected. `fn (x) { return x }` satisfies
	// `fn <T>(x: T) -> T`, while `fn (x) { return 5 }` fails with `cannot constrain 5 <: T`.
	// The check runs under a discard-only probe so the skolem bounds it records on the
	// initializer's own vars leave no trace, and the pristine annT is adopted as the binding
	// type. Constraining against the annotation directly would instead leak its vars as a
	// second quantifier, `fn <T0, T: T0>(x: T) -> T`.
	if funcTypeParamVars(annT).Len() > 0 {
		c.blameConstraintErrors(init, c.ctx.trialUnderProbe(initT, c.skolemizeGenericAnn(annT)))
		return annT
	}
	if c.tryUpgradeToOwnedMut(init, init, initT, annT) {
		return annT
	}
	c.constrain(init, initT, annT)
	return annT
}

// skolemizeGenericAnn copies a resolved annotation, replacing every generic function's own
// type-parameter var by a fresh skolem of the same name. This is the checking-mode rule for
// a function literal against a polymorphic annotation: the expected type is pushed into the
// term with its parameters held abstract, so constrain rejects a body that forces a
// parameter to a concrete value. For example `fn (x) { return 5 }` checked against
// `fn <T>(x: T) -> T` fails with `5 <: T`. A skolem is concrete, so it also propagates
// through an intermediate inference var, rejecting `fn (x) { return x }` against
// `fn <T>(x: T) -> number` where `x`'s skolem floor cannot satisfy `number`.
func (c *checker) skolemizeGenericAnn(annT soltype.Type) soltype.Type {
	return annT.Accept(&skolemizer{c: c, subst: map[*soltype.TypeVarType]*soltype.SkolemType{}}, soltype.Positive)
}

// skolemizer rewrites a resolved annotation, substituting each generic function's
// type-parameter var with a distinct skolem. It seeds a skolem per TypeParam as it enters
// each FuncType, then substitutes at every later occurrence of that var in the params and
// return, so a nested generic function is skolemized under its own binder too.
//
// The rebuilt FuncType drops its TypeParams list: its binder vars become skolems in the
// params and return, and constrain ignores the list. Nil-ing it also avoids acceptTypeParamVar,
// which requires a binder to stay a *TypeVarType and would panic on a skolem.
type skolemizer struct {
	c     *checker
	subst map[*soltype.TypeVarType]*soltype.SkolemType
}

func (s *skolemizer) EnterType(t soltype.Type, _ soltype.Polarity) soltype.EnterResult {
	switch t := t.(type) {
	case *soltype.FuncType:
		if len(t.TypeParams) == 0 {
			return soltype.EnterResult{} // monomorphic: ordinary descent
		}
		// Seed every skolem before carrying bounds, so a bound naming a sibling parameter
		// resolves to that sibling's skolem.
		for _, tp := range t.TypeParams {
			if _, done := s.subst[tp.Var]; !done {
				s.subst[tp.Var] = s.c.ctx.freshSkolem(tp.Name)
			}
		}
		for _, tp := range t.TypeParams {
			sk := s.subst[tp.Var]
			if sk.Upper == nil {
				sk.Upper = s.skolemizeBound(tp.Var.UpperBounds)
			}
		}
		cp := *t
		cp.TypeParams = nil
		return soltype.EnterResult{Type: &cp} // descend into the copy's params and return
	case *soltype.TypeVarType:
		if sk, ok := s.subst[t]; ok {
			return soltype.EnterResult{Type: sk, SkipChildren: true}
		}
	}
	return soltype.EnterResult{}
}

func (s *skolemizer) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

// skolemizeBound resolves a type parameter's declared constraint into its skolem's Upper,
// substituting a sibling parameter for that sibling's skolem. An unconstrained parameter
// has no bound, so it returns nil. A parameter with several constraints returns their
// intersection, so the skolem satisfies a super reachable through any one of them.
func (s *skolemizer) skolemizeBound(bounds []soltype.Type) soltype.Type {
	switch len(bounds) {
	case 0:
		return nil
	case 1:
		return bounds[0].Accept(s, soltype.Positive)
	default:
		members := make([]soltype.Type, len(bounds))
		for i, b := range bounds {
			members[i] = b.Accept(s, soltype.Positive)
		}
		return &soltype.IntersectionType{Types: members}
	}
}

// tryUpgradeToOwnedMut grants the immutable→mutable upgrade when a value of type srcT,
// built by src, flows into the type target. The upgrade fires only when target is
// owned-mutable — a RefType with Mut set and a nil lifetime — and src is uniquely owned
// per canUpgradeToOwnedMut. It then constrains srcT against target's immutable read view,
// stripOwnedMut of the inner, the same covariant check the non-mut path runs, and returns
// true. Otherwise it constrains nothing and returns false, leaving the caller to run its
// ordinary constraint against target.
//
// target is whatever owned-mutable type the value flows into at a given site, and every
// such site routes through here: the declaration initializer's annotation, the binding
// type of a reassignment, a `mut` parameter type, a `mut` return annotation, and a `mut`
// field's type. site is the node blamed on failure. src is the source expression, which
// canUpgradeToOwnedMut inspects for the syntactic fresh-literal fast path and the
// place-move path.
func (c *checker) tryUpgradeToOwnedMut(site ast.Node, src ast.Expr, srcT, target soltype.Type) bool {
	ref, ok := target.(*soltype.RefType)
	if !ok || !ref.Mut || ref.Lt != nil || !c.canUpgradeToOwnedMut(src) {
		return false
	}
	// Under the lazy deep-mut form the inner is already bare, and a nested `mut {x}` field
	// inside a non-mut container is rejected at the annotation site (#779), so stripOwnedMut
	// is a defensive no-op for most target types. It still lets a uniquely-owned source
	// flow covariantly into any owned-mut cell that reaches here. A fully uniquely-owned
	// source is owned at every level, so the upgrade is sound the whole way down.
	c.constrain(site, srcT, stripOwnedMut(ref.Inner))
	return true
}

// canUpgradeToOwnedMut reports whether the value built by src may be granted an
// owned-mutable type when it flows into an owned-mutable target. The grant is sound only
// when the value is uniquely owned, so no live immutable alias can observe a write through
// the new mutable view. This is Rule 2 of the mutability-transition checker with an empty
// alias set. Three cases show what it returns and why:
//
//   - A syntactically fresh literal returns true. In `val m: mut {x} = {x: 1}` the literal
//     is newly built and nothing else refers to it, so it is uniquely owned and granting it
//     mutability aliases nothing. A fresh literal carries no identifier and needs no VarID,
//     so this case holds at module top level where the liveness pre-pass has not run.
//
//   - A consuming move of an owned place returns true. In `val m: mut {x} = cfg` the move
//     consumes `cfg` and leaves `m` the sole owner, so again no live alias remains, and a
//     later use of `cfg` is a use-after-move. exprPlace ties the place to a VarID, so this
//     case holds only inside a function body where the move engine records the consume.
//
//   - A literal wrapping an owned-mutable leaf returns false. In `{p: inner}` with
//     `inner: mut {x: number}`, `inner` already holds a mutable cell. The upgrade constrains
//     the source against the target's covariant read view, which would widen that cell — for
//     example accept it where `mut {x: number | string}` is expected — and that is unsound.
//     Returning false routes the source to the strict mut<:mut path, which pins the cell's
//     element type invariant. containsOwnedMut is recursive, so an owned-mutable cell at any
//     depth rejects the whole source.
//
// It generalizes the fresh-literal-only isFreshlyConstructed: both share the
// freshLiteralShape recursion and differ only at a non-literal leaf, which this predicate
// accepts when it is an owned-place move carrying no owned-mutable cell. The recursion
// reaches such a leaf nested inside a fresh literal, so `{p: cfg}` qualifies when `cfg` is a
// dead owned variable even though the literal is not identifier-free.
//
// SOUNDNESS: every leaf this predicate admits must be consumed by the move engine, so a
// later use of it is a use-after-move. That holds because movesOwnedPlace gates on
// isConcreteOwned, a strict subset of the isOwnedMovable set consumeOwned moves at every
// flow site, so the upgrade set is a subset of the consume set. Widening movesOwnedPlace
// toward a place consumeOwned does not move would break this and grant a mutable view with
// no backing move.
func (c *checker) canUpgradeToOwnedMut(src ast.Expr) bool {
	return freshLiteralShape(src, func(leaf ast.Expr) bool {
		if c.acceptsBorrowLeaf(leaf) {
			return true
		}
		t := c.info.TypeOf(leaf)
		return !containsOwnedMut(t) && movesOwnedPlace(leaf, t)
	})
}

// acceptsBorrowLeaf reports whether leaf is a borrow expression `&e`/`&mut e` that may sit in
// an owned-mutable literal — `val mut b = {peer: &mut d}` builds the owned-mutable `mut {peer:
// &mut d}`, so `b` is `&mut`-borrowable and its field repointable. The container's mutability
// lets the field be repointed but grants no write to the referent beyond what the borrow
// already carries, and the borrow's pointee stays invariant through the C2 RefType arm, so the
// covariant read-view constrain the upgrade runs does not widen the pointee. A borrow of a
// function-local that this makes constructible is tracked by the borrow-edge graph, so a later
// flow-out of the container is caught by the escape check. It is confined to a function body,
// where that escape tracking runs.
func (c *checker) acceptsBorrowLeaf(leaf ast.Expr) bool {
	if c.fn == nil {
		return false
	}
	_, ok := leaf.(*ast.BorrowExpr)
	return ok
}

// freshLiteralShape reports whether e is a primitive literal, or an object/tuple literal
// whose every element satisfies leafOK. It is the structural recursion shared by
// isFreshlyConstructed and canUpgradeToOwnedMut, which differ only in what they accept at
// a non-literal leaf. isFreshlyConstructed accepts nothing there; canUpgradeToOwnedMut
// accepts a uniquely-owned place move.
func freshLiteralShape(e ast.Expr, leafOK func(ast.Expr) bool) bool {
	switch e := e.(type) {
	case *ast.LiteralExpr:
		return true
	case *ast.TupleExpr:
		for _, elem := range e.Elems {
			if !freshLiteralShape(elem, leafOK) {
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
			if !freshLiteralShape(prop.Value, leafOK) {
				return false
			}
		}
		return true
	default:
		return leafOK(e)
	}
}

// stripOwnedMut returns t's deeply-immutable skeleton by peeling every owned-mut
// cell (Mut set, Lt nil) and recursing through objects and tuples. Borrows are
// left untouched. Under the lazy deep-mut form (PR 14) a plain `mut {a: {x}}` stores
// a bare inner, and a nested `mut {x}` field inside a non-mut container is now
// rejected at the annotation site (#779), so there is usually nothing to strip; the
// peel is retained defensively so a fresh literal can upgrade into any deeply-mutable
// target the whole way down.
func stripOwnedMut(t soltype.Type) soltype.Type {
	switch t := t.(type) {
	case *soltype.RefType:
		if t.Mut && t.Lt == nil {
			return stripOwnedMut(t.Inner)
		}
		return t
	case *soltype.ObjectType:
		elems := make([]soltype.ObjTypeElem, len(t.Elems))
		for i, e := range t.Elems {
			p := soltype.AsProperty(e)
			elems[i] = &soltype.PropertyElem{Name: p.Name, Type: stripOwnedMut(p.Type), Optional: p.Optional, Readonly: p.Readonly}
		}
		return &soltype.ObjectType{Elems: elems, Inexact: t.Inexact}
	case *soltype.TupleType:
		elems := make([]soltype.Type, len(t.Elems))
		for i, e := range t.Elems {
			elems[i] = stripOwnedMut(e)
		}
		return &soltype.TupleType{Elems: elems, Inexact: t.Inexact}
	default:
		return t
	}
}

// bindingMovesOwnedPlace reports whether the unannotated initializer of an IdentPat
// binding moves an owned value out of a place. A place is a binding or a field path
// such as `p` or `pair.a`, and its value moves when it is a concrete owned object,
// tuple, or owned RefType. Such a binding takes ownership of the value, so the
// binding's declared mutability replaces the source's. A `val mut q` thaws an
// owned-immutable source into a mutable binding, and a plain `val q` freezes an
// owned-mutable source into an immutable one.
//
// A borrow, value type, fresh literal, generic type-parameter value, or non-place
// initializer is not a place move and keeps the ordinary inference. exprPlace also
// fails outside a function body, where the rename pass has assigned no VarID, so the
// mutability rewrite is confined to bodies where the move engine enforces the consume.
func (c *checker) bindingMovesOwnedPlace(pat ast.Pat, init ast.Expr, initT soltype.Type) bool {
	if _, ok := pat.(*ast.IdentPat); !ok {
		return false
	}
	return movesOwnedPlace(init, initT)
}

// movesOwnedPlace reports whether init names a uniquely-owned place whose value moves
// when it flows into an owning destination. A place is a binding or a field path, so
// exprPlace succeeds on it. Its value moves when it is a concrete owned object, tuple, or owned
// RefType. The move consumes the place and leaves the destination its sole owner.
// exprPlace fails outside a function body, where the rename pass has assigned no VarID, so
// a move is confined to bodies where the move engine enforces the consume. This is the
// place-move half of both bindingMovesOwnedPlace and the canUpgradeToOwnedMut leaf check.
func movesOwnedPlace(init ast.Expr, initT soltype.Type) bool {
	if _, ok := exprPlace(init); !ok {
		return false
	}
	return isConcreteOwned(initT)
}

// isOwnedMut reports whether t is an owned-mutable cell — a RefType with Mut set and a
// nil lifetime. It is the shape a `mut T` annotation lowers to and the shape the
// immutable→mutable upgrade grants.
func isOwnedMut(t soltype.Type) bool {
	ref, ok := t.(*soltype.RefType)
	return ok && ref.Mut && ref.Lt == nil
}

// containsOwnedMut reports whether t is an owned-mutable cell or holds one nested inside
// an object or tuple. It detects exactly what stripOwnedMut would peel. The
// immutable→mutable upgrade constrains the source against the target's covariant read view,
// which is sound only when the source has no mutable cell to widen, so canUpgradeToOwnedMut
// rejects a source where this returns true and routes it to the strict mut<:mut path. A
// borrow's pointee belongs to another region, so it is left to the borrow rules and not
// recursed into.
func containsOwnedMut(t soltype.Type) bool {
	switch t := t.(type) {
	case *soltype.RefType:
		return isOwnedMut(t)
	case *soltype.ObjectType:
		for _, e := range t.Elems {
			if containsOwnedMut(soltype.AsProperty(e).Type) {
				return true
			}
		}
		return false
	case *soltype.TupleType:
		for _, e := range t.Elems {
			if containsOwnedMut(e) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// isMutableIdentPat reports whether p is a simple identifier binding written with
// the `mut` prefix, as in `val mut q = …`. The construction upgrade applies only to
// an IdentPat. A `mut` leaf inside a destructuring pattern carries the same flag and
// is thawed per-leaf by applyBindMode during bindPattern, not here.
func isMutableIdentPat(p ast.Pat) bool {
	ip, ok := p.(*ast.IdentPat)
	return ok && ip.Mutable
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
		// A spread element ([...xs]) splices a variable number of inferred elements
		// into the literal, so an AST element position no longer lines up with the
		// inferred tuple's. Index ranges over the INFERRED tuple, which
		// ExtraElementError.Index and .Sub both refer to. Span() resolves each
		// element's own origin node through prov, so per-element blame stays precise.
		// The site is only a fallback. It is the AST element at i when the literal has
		// no spread, since positions then match, and the whole literal otherwise.
		hasSpread := false
		for _, el := range tup.Elems {
			if _, ok := el.(*ast.ArraySpreadExpr); ok {
				hasSpread = true
				break
			}
		}
		// Elements beyond the target's declared prefix are excess on the literal.
		for i := len(ann.Elems); i < len(subTup.Elems); i++ {
			err := &ExtraElementError{Sub: subTup, Super: ann, Index: i}
			if !hasSpread && i < len(tup.Elems) {
				err.prov, err.site = c.prov, tup.Elems[i]
			} else {
				err.prov, err.site = c.prov, tup
			}
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
	// display type, the reassignment target, and any read of the binding all widen
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
	// not var-free), so Info consumers must render it with renderScheme, which threads
	// lifetime bounds. Plain soltype.Print drops the var names and plain PrintAsScheme
	// drops the lifetime bounds. Same contract as the top-level path (see module.go).
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
// generalized bindings. A destructured name reads a component of the initializer, so
// it shares that component's type rather than quantifying over it. Top-level
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
	// Record each leaf's borrow edges from the initializer sub-expression it binds, so a
	// return of a leaf that projects a borrow of a local is caught. `val {peer} = {peer:
	// &mut b}` records peer → b. The correspondence is structural, so it tracks a leaf
	// only when the initializer's shape mirrors the pattern's.
	if c.fn != nil && c.fn.eagerBorrowGraph != nil && d.Init != nil {
		c.recordDestructureBorrowEdges(d.Pattern, d.Init)
	}
}

// recordDestructureBorrowEdges records the borrow edges of each destructuring leaf by
// matching the pattern against the initializer structurally. When the initializer is a
// place, recordPatternPlaceEdges projects the pattern over it, so `val {peer} = a` carries
// a's edges into peer. Otherwise an identifier leaf records edges from the sub-expression
// bound to it, an object pattern matches each element to the initializer property of the
// same name, and a tuple pattern matches by position. A leaf whose initializer
// sub-expression is not statically present, such as a property supplied by a spread,
// records nothing.
func (c *checker) recordDestructureBorrowEdges(pat ast.Pat, init ast.Expr) {
	if p, ok := exprPlace(init); ok && p.root > 0 {
		c.recordPatternPlaceEdges(pat, p)
		return
	}
	switch pat := pat.(type) {
	case *ast.IdentPat:
		c.recordBorrowEdges(pat.VarID, init)
	case *ast.ObjectPat:
		obj, ok := init.(*ast.ObjectExpr)
		if !ok {
			return
		}
		props := map[string]ast.Expr{}
		for _, elem := range obj.Elems {
			if p, ok := elem.(*ast.PropertyExpr); ok && p.Value != nil {
				if name, ok := objKeyName(p.Name); ok {
					props[name] = p.Value
				}
			}
		}
		for _, elem := range pat.Elems {
			switch e := elem.(type) {
			case *ast.ObjShorthandPat:
				if v, ok := props[e.Key.Name]; ok {
					c.recordBorrowEdges(e.VarID, v)
				} else if e.Default != nil {
					// The property is absent from the initializer, so the leaf takes the
					// shorthand default, such as `val {peer = &mut b} = obj`.
					c.recordBorrowEdges(e.VarID, e.Default)
				}
			case *ast.ObjKeyValuePat:
				if v, ok := props[e.Key.Name]; ok {
					c.recordDestructureBorrowEdges(e.Value, v)
				}
			}
		}
	case *ast.TuplePat:
		tup, ok := init.(*ast.TupleExpr)
		if !ok {
			return
		}
		for i, elem := range pat.Elems {
			if i < len(tup.Elems) {
				c.recordDestructureBorrowEdges(elem, tup.Elems[i])
			}
		}
	}
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
	// A `declare fn` carries a nil body, so inferFunc types it as the no-body site it is:
	// it adopts the declared return without constraining a synthetic `void`, and lowers
	// its declared lifetime bounds instead of checking them against a body.
	t := c.inferFunc(scope, lvl, d.FuncSig, d.Body, d, true)
	return t, &ast.NodeProvenance{Node: d}
}
