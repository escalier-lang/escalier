package solver

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/provenance"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// inferLiteral types a literal expression as its singleton soltype.LitType and
// records it in Info. M1's soltype.Lit set is num/str/bool only; the remaining
// ast literal kinds (regex, bigint, null, undefined) fall through to the M2
// subset guard until later milestones extend soltype.Lit (§ soltype/type.go
// Prim/Lit note).
func (c *checker) inferLiteral(e *ast.LiteralExpr) soltype.Type {
	var lit soltype.Lit
	switch l := e.Lit.(type) {
	case *ast.NumLit:
		lit = &soltype.NumLit{Value: l.Value}
	case *ast.StrLit:
		lit = &soltype.StrLit{Value: l.Value}
	case *ast.BoolLit:
		lit = &soltype.BoolLit{Value: l.Value}
	default:
		return c.reportUnsupported(e.Lit)
	}
	t := &soltype.LitType{Lit: lit}
	c.recordType(e, t)
	c.recordProv(t, e, LiteralInference)
	return t
}

// inferIdent resolves a value-position identifier through the scope chain — the
// production form of the spike's *Var case crossed with design-notes §"The
// constraint-generating AST walk". M3 (PR1) slots in the instantiation hook M2
// left as a TODO: an ordinary binding instantiates its sole scheme at the current
// level, so a polymorphic let gives each use fresh variables (a MonoScheme
// instantiates to itself, preserving M2's behavior for the monomorphic bindings).
//
// An overloaded binding's value-position type is the intersection of its arms,
// resolved through the probe — that is PR6 and unreachable today (M2 rejects
// multi-FuncDecl names, so no overloaded binding is ever bound), so PR1 asserts
// the single-scheme invariant rather than branching on it.
//
// A namespace name in value position is an error — namespaces are a separate
// binding sort and never flow as values. M4 moves that rejection OFF inferIdent
// to the value-position consumer (demandValue): a namespace is legal in the
// OBJECT position of a member/index chain (Foo.bar, A.B.c), so resolvePath
// surfaces it and only a consumer that needs a value rejects it. The error then
// fires once for both `f(Foo)` and a partial chain `f(A.B)`.
func (c *checker) inferIdent(scope *Scope, lvl int, e *ast.IdentExpr) soltype.Type {
	return c.demandValue(c.resolveIdentPath(scope, lvl, e, false), e)
}

// pathResult is the sum returned by resolvePath: a path expression resolves to
// EITHER a value (its instantiated type, in value) OR a namespace (ns), or it
// failed (err, with the diagnostic already reported, the caller should recover).
// At most one of value/ns is set; err is set instead when resolution reported an
// error. The value arm may itself hold the ErrorType recovery sentinel — that is a
// value, not an err — so a malformed-but-already-reported leaf does not double-report.
type pathResult struct {
	value soltype.Type
	ns    *Namespace
	err   bool
}

// resolvePath resolves an ident / member / index chain to a value or a namespace
// WITHOUT demanding either: the OBJECT position of a member/index tolerates a
// namespace (so Foo.bar and A.B.c walk through), while demandValue — called by
// every value-position consumer — rejects a namespace result. Any other
// expression kind in path position is an ordinary value expression.
// objPos marks that e sits in the OBJECT position of a member/index chain — a step
// on the path's spine rather than the whole place read. A spine step records no
// use-after-move use of its own, since the outermost place read records the full
// place that subsumes it: reading `pair.a.id` records one use of `pair.a.id`, not a
// separate whole-binding use of `pair` that would wrongly collide with a partial
// move of a sibling. inferExpr never threads objPos, so an off-spine subexpression
// such as a call argument or an index key resolves with objPos false and records its
// own uses.
func (c *checker) resolvePath(scope *Scope, lvl int, e ast.Expr, objPos bool) pathResult {
	switch e := e.(type) {
	case *ast.IdentExpr:
		return c.resolveIdentPath(scope, lvl, e, objPos)
	case *ast.MemberExpr:
		return c.resolveMemberPath(scope, lvl, e, objPos)
	case *ast.IndexExpr:
		return c.resolveIndexPath(scope, lvl, e, objPos)
	default:
		return pathResult{value: c.inferExpr(scope, lvl, e)}
	}
}

// demandValue collapses a pathResult for a value-position consumer: a namespace
// result becomes a NamespaceUsedAsValueError blaming node, and an already-reported
// failure recovers to the ErrorType sentinel.
func (c *checker) demandValue(r pathResult, node ast.Expr) soltype.Type {
	switch {
	case r.err:
		return &soltype.ErrorType{}
	case r.ns != nil:
		return c.report(&NamespaceUsedAsValueError{Node: node, NS: r.ns})
	default:
		return r.value
	}
}

// resolveIdentPath looks a bare identifier up in the value sort first, then the
// namespace sort, returning whichever it finds.
//
// Any binding still in scope has at least one scheme: inferComponent pre-binds
// each group member to a fresh MonoScheme, and on failure deletes the binding
// (scope.removeValue) rather than leaving it with an empty Schemes slice. So the
// len > 0 check should never fail in practice — but Schemes is a slice, not a
// guaranteed-non-empty field, so we guard it anyway: a malformed empty binding
// degrades to an unknown-identifier error instead of panicking on Schemes[0].
func (c *checker) resolveIdentPath(scope *Scope, lvl int, e *ast.IdentExpr, objPos bool) pathResult {
	if b, ok := scope.GetValue(e.Name); ok && len(b.Schemes) > 0 {
		t := c.bindingValue(lvl, b)
		c.recordType(e, t)
		// Record this read so the post-walk use-after-move pass can test it against the
		// consumed lattice. The binding's coalesced type drives the reference-shape
		// decision, since a fresh instantiation can hide a mono owned shape behind a
		// variable. A spine step of a member chain records nothing here; the outermost
		// member read records the full place instead.
		if !objPos {
			c.recordUse(e, bindingType(b))
		}
		return pathResult{value: t}
	}
	if ns, ok := scope.GetNamespace(e.Name); ok {
		return pathResult{ns: ns}
	}
	c.report(&UnknownIdentifierError{Ident: e})
	return pathResult{err: true}
}

// bindingValue instantiates a value binding at lvl for value position. An
// overloaded binding (PR6) — `val g = f`, or `f` passed as an argument — is the
// intersection of its arms (the one scoped lattice exception; see
// overloadIntersection and constrain's IntersectionType arm). An ordinary binding
// instantiates its sole scheme, so each use gets fresh variables. A direct call
// `f(x)` to an overloaded name never reaches here: inferCall intercepts the
// overloaded callee and routes it through resolveOverload first.
func (c *checker) bindingValue(lvl int, b ValueBinding) soltype.Type {
	if b.IsOverloaded() {
		return c.overloadIntersection(lvl, b)
	}
	return c.instantiate(b.Schemes[0], lvl)
}

// astKind returns a short surface name for any AST node — an expression,
// literal, declaration, or pattern — used in the M2 subset-guard error messages.
// It strips the leading "*ast." from the Go type name so e.g. *ast.BinaryExpr
// renders as "BinaryExpr". One helper serves every guard site (inferExpr,
// inferLiteral, inferDeclDef) so the format lives in a single place.
func astKind(n any) string {
	return strings.TrimPrefix(fmt.Sprintf("%T", n), "*ast.")
}

// inferFuncExpr types a function expression as a soltype.FuncType and records it
// in Info. It delegates to inferFunc, the shared core also used by inferFuncDecl
// (the plan's "reuse inferFuncExpr on the decl's sig+body", factored so neither
// side owns the other). M2 is monomorphic: any TypeParams on the signature are a
// generic function (M3) and are diagnosed as unsupported by inferFunc, not
// silently erased; an un-annotated param simply gets a fresh var, which
// coalesces to unknown/never at render time rather than a <T0> quantifier
// (generalization is M3).
func (c *checker) inferFuncExpr(scope *Scope, lvl int, e *ast.FuncExpr) soltype.Type {
	t := c.inferFunc(scope, lvl, e.FuncSig, e.Body, e)
	c.recordType(e, t)
	return t
}

// inferFunc is the shared function-typing core for FuncExpr and FuncDecl. It
// opens a child scope, binds each param (its annotation resolved to a soltype,
// or a fresh var when un-annotated), types the body block in that scope, and
// builds the n-ary soltype.FuncType. The return type is built solely from the
// body's `return` statements (joinReturnPoints); a body with no return produces
// void. When the signature carries a return annotation the inferred return is
// constrained against it and the annotated type becomes the function's return
// type. A bodyless (declare/ambient) function adopts its return annotation
// without constraining anything. node supplies the span stamped onto a
// return-annotation constraint failure.
func (c *checker) inferFunc(scope *Scope, lvl int, sig ast.FuncSig, body *ast.Block, node ast.Node) *soltype.FuncType {
	// Give this function its own named-lifetime scope so a `&'a` in its signature
	// resolves consistently across its params and return, without sharing the name
	// with an enclosing or sibling function. Restored on exit so a nested function
	// does not clobber the outer scope's names. namedLifetime allocates the map on
	// first use, so a body with no `&'a` annotation pays no allocation.
	savedNamedLts := c.namedLifetimes
	c.namedLifetimes = nil
	defer func() { c.namedLifetimes = savedNamedLts }()
	if len(sig.TypeParams) > 0 {
		// Generic functions (fn <T>(...)) need type schemes / generalization,
		// which are M3. The FuncExpr/FuncDecl kind itself is supported — it is the
		// type-param feature that is not — so diagnose it as an unsupported feature
		// (blaming the function node) rather than silently erasing the params, then
		// continue inferring monomorphically.
		c.reportUnsupportedFeature(node, "TypeParam")
	}
	fnScope := scope.Child()
	params := make([]*soltype.FuncParam, len(sig.Params))
	// paramTypes maps each bound parameter name to its soltype, consumed by the M4
	// G1 liveness pre-pass to seed parameter alias mutability.
	paramTypes := make(map[string]soltype.Type, len(sig.Params))
	for i, p := range sig.Params {
		pt := c.paramType(p, lvl)
		// Rule 2 of PR 3. A bare annotation is owned and only an `&` annotation
		// borrows. An `&` annotation already mints its lifetime in
		// resolveLifetimeAnn, so a parameter has nothing to attach here. A bare
		// `mut T` stays owned-mutable.
		// An `open` un-annotated param keeps its usage-inferred object inexact at
		// display time (B2). The marker only makes sense for an inferred var; an
		// annotated param's exactness is fixed by its annotation, and paramType
		// returns the resolved annotation (not a var) in that case.
		if p.Open {
			if v, ok := pt.(*soltype.TypeVarType); ok {
				v.Open = true
			}
		}
		if name, ok := identPatName(p.Pattern); ok {
			// The param's IdentPat IS its definition site, so record it as the binding's
			// source — symmetric to a val/var/fn binding (inferVarDecl/module.go). This
			// lets CannotAssignToImmutableError point "declared immutable here" at the
			// parameter (see bindingDecl). p.Pattern is an ast.Node (*ast.Param is not).
			sources := []provenance.Provenance{&ast.NodeProvenance{Node: p.Pattern}}
			if p.TypeAnn == nil {
				// An un-annotated param's type is the fresh var minted here, so a
				// param-type mismatch blames the param. An annotated param's blame
				// instead rides on its annotation, recorded by resolveTypeAnn.
				c.recordProv(pt, p.Pattern, ParamBinding)
			}
			// A parameter binding never generalizes — its var is fixed for the body — so
			// it is a MonoScheme; instantiate returns pt unchanged at every use.
			fnScope.defineValue(name, ValueBinding{Schemes: []TypeScheme{monoScheme(pt)}, Sources: sources})
			paramTypes[name] = pt
			// An `x?` parameter (parsed onto ast.Param.Optional) lowers the function's
			// `required` count without dropping the param — carried onto the soltype so
			// the accept-set rule and the printer (x?: T) see it. KNOWN GAP (M6): the
			// in-body binding keeps the param's declared type (pt), NOT widened to
			// `pt | undefined`, so a body that reads an omitted optional sees it at the
			// narrower type. Widening needs undefined/unions (M6); M3 has neither.
			params[i] = &soltype.FuncParam{Pattern: &soltype.IdentPat{Name: name}, Type: pt, Optional: p.Optional}
		} else if p.Pattern != nil {
			// M4 E1: a destructuring parameter such as `{x, y}` or `[a, b]`. bindPattern
			// binds each leaf into the function scope against the param's type and returns
			// the soltype mirror the printer renders. It also writes each leaf's type into
			// paramTypes, keyed by leaf name, so the liveness pre-pass seeds the leaf's
			// alias mutability. An un-annotated destructuring param mints a fresh var pt
			// whose mismatch blame should point at the pattern.
			if p.TypeAnn == nil {
				c.recordProv(pt, p.Pattern, ParamBinding)
			}
			mirror := c.bindPattern(fnScope, lvl, p.Pattern, pt, paramTypes)
			params[i] = &soltype.FuncParam{Pattern: mirror, Type: pt, Optional: p.Optional}
		} else {
			// A pattern-less param is not reachable from the real parser, which
			// synthesizes a placeholder. Blame the enclosing function rather than a nil
			// Span(), honoring the "never a panic" guarantee. Bind a synthetic name so
			// the parameter still types.
			c.reportUnsupported(node)
			name := fmt.Sprintf("arg%d", i)
			fnScope.defineValue(name, ValueBinding{Schemes: []TypeScheme{monoScheme(pt)}})
			paramTypes[name] = pt
			params[i] = &soltype.FuncParam{Pattern: &soltype.IdentPat{Name: name}, Type: pt, Optional: p.Optional}
		}
	}

	var ret soltype.Type = &soltype.Void{}
	var retExprs []ast.Expr
	hasBody := body != nil
	if hasBody {
		// PR3: open a fresh function context so every ReturnStmt encountered while
		// walking the body lands in our own returns list (a nested fn inside this
		// body opens its own context, so its returns never leak out here).
		saved := c.pushFuncCtx(sig.Async, node)
		// M4 G1: run the liveness pre-pass before walking the body so mutability
		// transitions are checked. It renames the body's variable nodes (writing the
		// VarIDs DetermineAliasSource and the alias tracker read) and seeds the
		// parameter alias sets onto c.fn. recordParamVarIDs then copies each param's
		// freshly-assigned VarID onto its binding so a closure capturing the param
		// resolves to its alias set.
		c.runLivenessPrePass(fnScope, sig.Params, paramTypes, body)
		recordParamVarIDs(fnScope, sig.Params)
		// Walk the body for type-checking and to collect its ReturnStmts; the
		// block's TAIL value is intentionally discarded. Unlike a value-position
		// block, where the last expression IS the block's value, a function body's
		// last expression is NOT an implicit return — only an explicit `return`
		// produces the function's value. This mirrors the old checker's
		// inferFuncBody.
		c.inferBlock(fnScope, lvl, body)
		// With the whole body walked, every move site is recorded, so the consumed
		// lattice is complete. Replay the recorded reads against it to report
		// use-after-move. This runs before popFuncCtx restores the outer context, since
		// it reads this body's move and use state off c.fn.
		c.checkUseAfterMoves()
		retExprs = c.fn.returnExprs
		ret = c.joinReturnPoints(node, lvl, c.popFuncCtx(saved))
	}
	// Return-annotation handling diverges by async-ness.
	//
	// Async: the function's EXTERNAL type is always `Promise<T>`, and the return
	// annotation — when present — names that external Promise, NOT the body's value.
	// So it must itself be a `Promise<…>`; the body returns the unwrapped inner,
	// constrained `<: inner`, and the annotation is presented as the external type
	// (no extra wrap). `Promise<_>` is allowed — the `_` resolves to a fresh var the
	// body flows into, inferring the inner. With no annotation the inferred body
	// return is wrapped. asyncReturn carries all of this (and the error/recovery
	// for a bare annotation like `async fn () -> number`).
	//
	// Non-async: the annotation governs the return directly — the body is
	// constrained `<: annotation` and the function returns the annotation (M2's
	// rule). An unsupported annotation (ok=false) was already reported by
	// resolveTypeAnn; recover by keeping the inferred body type (or unknown when
	// there is no body, since a synthetic Void would falsely signal "returns
	// nothing").
	if sig.Async {
		ret = c.asyncReturn(node, sig.Return, ret, hasBody, lvl)
	} else if sig.Return != nil {
		if annT, ok := c.resolveTypeAnn(sig.Return, lvl); ok {
			// Only constrain the body when there IS one; a bodyless (declare/ambient)
			// function simply adopts the annotation (constraining the synthetic Void
			// would raise a spurious `void <: T`).
			if hasBody {
				c.constrainReturnAgainstAnnotation(node, retExprs, ret, annT) // body <: declared return
			}
			ret = annT
		} else if !hasBody {
			ret = &soltype.UnknownType{}
		}
	}
	// A bare function value is exact (accept-set [required, len(Params)]): it rejects
	// extra arguments. A trailing `...` in the signature (sig.Inexact) marks it
	// inexact — it tolerates extra args when used as a callback (#677 §4.1), accept
	// [required, ∞). Note exactness governs callback subtyping, not direct calls: an
	// inexact value still rejects extras at a visible call site (the inferCall lint).
	ft := &soltype.FuncType{Params: params, Ret: ret, Inexact: sig.Inexact}
	// Record the function's own type against its node so a function flowing into a
	// non-function requirement blames the function, and FuncArityMismatchError can
	// carry a "defined here" related span. (For a named callee this raw FuncType is
	// re-minted by coalescing at binding time, so the entry is exact for inline
	// callees; M3's FromInstantiation makes named-callee blame precise.)
	c.recordProv(ft, node, FuncInference)
	return ft
}

// joinReturnPoints builds a function's return type from the ReturnStmt types
// collected while walking its body. No returns means the body produces no value,
// so the function returns void. A return-less body that always diverges via
// `throw` would be `never` in the old checker, but that case is deferred: throw,
// do, and match are not walked yet, so such a body cannot be recognized as
// diverging, and constrain has no `never <: T` arm — a raw NeverType minted here
// would spuriously fail the body-vs-annotation check. Recovery placeholders are
// the absorbing ErrorType sentinel (PR8), never a raw NeverType. A single return
// is the return type directly — no join var, no indirection. Multiple returns
// flow through a fresh join variable whose coalesced positive face is their
// union, constrained in source order so the rendered union reflects source
// order.
func (c *checker) joinReturnPoints(node ast.Node, lvl int, collected []soltype.Type) soltype.Type {
	switch len(collected) {
	case 0:
		return &soltype.Void{}
	case 1:
		return collected[0]
	default:
		// M4 D3: several returns of borrowed objects that differ only in lifetime join
		// into one borrow whose lifetime unites theirs. So `if c { return p } else {
		// return q }` over two `&mut` params is `&('a | 'b) mut {…}` rather than the
		// un-joined `&'a mut {…} | &'b mut {…}`. A mixed or non-borrow set falls through to
		// the generic union below.
		if joined, ok := c.joinBorrows(node, lvl, collected); ok {
			return joined
		}
		joinVar := c.freshAt(lvl)
		c.recordProv(joinVar, node, ReturnJoin)
		for _, rt := range collected {
			c.constrain(node, rt, joinVar)
		}
		return joinVar
	}
}

// constrainReturnAgainstAnnotation constrains a function body's joined return type
// against its declared return annotation, granting the immutable→mutable upgrade when
// the return annotation is owned-mutable and EVERY return value is uniquely owned. A
// function yields a value as owned-mutable only when each returned value is uniquely
// owned, so a single non-upgradable return on any path blocks the grant and the strict
// constraint runs. With the grant the joined return shape is constrained against the
// return annotation's immutable read view, the same covariant check tryUpgradeToOwnedMut
// runs at the other value-flow sites. The join is not a single source expression, so the
// decision is made here rather than through that per-expression helper.
func (c *checker) constrainReturnAgainstAnnotation(node ast.Node, retExprs []ast.Expr, ret, annT soltype.Type) {
	if ref, ok := annT.(*soltype.RefType); ok && ref.Mut && ref.Lt == nil && c.allReturnsUpgradable(retExprs) {
		c.constrain(node, ret, stripOwnedMut(ref.Inner))
		return
	}
	c.constrain(node, ret, annT)
}

// allReturnsUpgradable reports whether every return operand is upgradable per
// canUpgradeToOwnedMut, which already rejects an operand carrying an owned-mutable cell at
// any depth. An empty set, a bare `return` with a nil operand, or any non-upgradable
// operand makes it false, so the grant applies only when the whole join is uniquely owned
// and immutable.
func (c *checker) allReturnsUpgradable(retExprs []ast.Expr) bool {
	if len(retExprs) == 0 {
		return false
	}
	for _, e := range retExprs {
		if e == nil || !c.canUpgradeToOwnedMut(e) {
			return false
		}
	}
	return true
}

// joinBorrows synthesizes the join of several borrowed objects that differ only in
// lifetime (M4 D3). It applies only when EVERY input is a mutable borrow of an
// object, all sharing the same field-name set with each carrying a lifetime. The
// result is one mutable borrow whose lifetime is a fresh JOIN variable bounded below
// by each input's lifetime, so a positive-position result coalesces to `('a | 'b)`.
// The shared fields are constrained invariant across the inputs. A mut object's
// fields are observable in both directions, so a sound join pins them equal. Uniting
// differing field sets returns ok=false, because it would invent writable fields
// absent from one input.
//
// ok=false leaves the caller on its generic union path. A usage-inferred borrow is a
// type variable here, not a concrete RefType, so it returns ok=false. This matches
// the spike, which joins only concrete mut records.
func (c *checker) joinBorrows(node ast.Node, lvl int, types []soltype.Type) (soltype.Type, bool) {
	refs := make([]*soltype.RefType, len(types))
	objs := make([]*soltype.ObjectType, len(types))
	for i, t := range types {
		r, ok := t.(*soltype.RefType)
		if !ok || !r.Mut || r.Lt == nil {
			return nil, false
		}
		obj, ok := r.Inner.(*soltype.ObjectType)
		if !ok {
			return nil, false
		}
		if i > 0 && !sameObjectKeys(objs[0], obj) {
			return nil, false
		}
		refs[i] = r
		objs[i] = obj
	}

	joinLt := c.ctx.freshJoinLifetime(lvl)
	for _, r := range refs {
		c.ctx.constrainLt(r.Lt, joinLt)
	}
	// Pin each shared field invariant across the inputs: a mut object's fields are
	// read AND written through the join, so the join is sound only when they agree.
	for _, e := range objs[0].Elems {
		name := soltype.AsProperty(e).Name
		first, _ := objs[0].Prop(name)
		for _, obj := range objs[1:] {
			other, _ := obj.Prop(name)
			c.constrain(node, first.Type, other.Type)
			c.constrain(node, other.Type, first.Type)
		}
	}
	return &soltype.RefType{Mut: true, Lt: joinLt, Inner: objs[0]}, true
}

// constrainEscape constrains every borrow lifetime reachable in t to outlive
// 'static (M4 D3). It is the rule for a value flowing into module-level or otherwise
// 'static storage. A borrow that escapes its region must live forever, so each
// lifetime variable v gains the upper bound 'static. coalesceLifetime then resolves
// such a forced lifetime to 'static.
//
// inferAssign calls this on the source of a GLOBAL WRITE, a store into a
// module-level binding. The walk rides the shared soltype visitor through
// escapeVisitor, so it reaches a borrow in any structural position without a
// hand-maintained type switch.
//
// There is one boundary. The visitor treats a TypeVarType as a leaf, so a borrow
// reachable only through a usage-inferred variable is not forced here. That is the
// same place the global-write CarrierOf peel stops, and the deeper handling rides
// M4 G2.
func (c *checker) constrainEscape(t soltype.Type) {
	t.Accept(escapeVisitor{c: c}, soltype.Positive)
}

// escapeVisitor forces every borrow lifetime it reaches to outlive 'static. It
// rewrites nothing. EnterType records the constraint and returns an ordinary descent,
// so the shared rewriting visitor carries it through a RefType inner, object
// property, tuple element, union member, function parameter or return, and promise
// payload alike. The 'static bound is monotonic, so visiting a borrow in any polarity
// is sound.
type escapeVisitor struct{ c *checker }

func (v escapeVisitor) EnterType(t soltype.Type, _ soltype.Polarity) soltype.EnterResult {
	if r, ok := t.(*soltype.RefType); ok {
		if lt, ok := r.Lt.(*soltype.LifetimeVar); ok {
			v.c.ctx.constrainLt(lt, soltype.Static)
		}
	}
	return soltype.EnterResult{}
}

func (escapeVisitor) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

// sameObjectKeys reports whether two objects carry exactly the same set of property
// names — the join's precondition, since a mut object's field set is invariant.
func sameObjectKeys(a, b *soltype.ObjectType) bool {
	if len(a.Elems) != len(b.Elems) {
		return false
	}
	for _, e := range a.Elems {
		if _, ok := b.Prop(soltype.AsProperty(e).Name); !ok {
			return false
		}
	}
	return true
}

// asyncReturn computes an `async fn`'s external return type, which always faces
// callers as `Promise<T>`. When a return annotation is present it NAMES that
// external Promise (not the body's value), so it must itself be a `Promise<…>`:
// the body returns the unwrapped inner, constrained `<: inner`, and the annotation
// IS the external type — no extra wrap. `Promise<_>` works because `_` resolved to
// a fresh var the body's return flows into, inferring the inner. A bare annotation
// (`async fn () -> number`) is an AsyncReturnNotPromiseError; recovery wraps the
// inferred body return so the external face stays Promise-shaped. With no
// annotation the inferred body return (bodyType) is wrapped directly — preserving
// M3's "wrap an inferred return" model and its no-auto-flatten behavior (a body
// that already returns a Promise still wraps: `async fn (p: Promise<T>) { return
// await p }` is `Promise<Promise<T>>`; Awaited<T> is M9).
func (c *checker) asyncReturn(node ast.Node, ann ast.TypeAnn, bodyType soltype.Type, hasBody bool, lvl int) soltype.Type {
	if ann == nil {
		return c.wrapPromise(node, bodyType)
	}
	annT, ok := c.resolveTypeAnn(ann, lvl)
	if !ok {
		// Unsupported annotation — already reported by resolveTypeAnn. Recover as the
		// no-annotation case would (wrap the inferred body return); a bodyless fn has
		// no body to recover from, so wrap unknown rather than the synthetic Void.
		if !hasBody {
			bodyType = &soltype.UnknownType{}
		}
		return c.wrapPromise(node, bodyType)
	}
	promise, isPromise := annT.(*soltype.PromiseType)
	if !isPromise {
		// A non-Promise annotation on an async fn (`-> number`). Reject it, then
		// recover exactly like the unsupported-annotation case so the external face
		// stays Promise-shaped and callers don't cascade.
		c.report(&AsyncReturnNotPromiseError{Return: ann, Fn: node})
		if !hasBody {
			bodyType = &soltype.UnknownType{}
		}
		return c.wrapPromise(node, bodyType)
	}
	// Constrain the body's (unwrapped) return against the annotation's inner, and
	// present the annotation as the external type — it already IS the Promise.
	if hasBody {
		c.constrain(node, bodyType, promise.Inner) // body <: declared inner
	}
	return annT
}

// wrapPromise mints the external `Promise<inner>` face of an async function and
// records its provenance (PromiseWrap) against the function node.
func (c *checker) wrapPromise(node ast.Node, inner soltype.Type) soltype.Type {
	wrapped := &soltype.PromiseType{Inner: inner}
	c.recordProv(wrapped, node, PromiseWrap)
	return wrapped
}

// paramType resolves a param's type: its annotation when present, else a fresh
// inference variable at the current level (the spike's "fresh var per param").
// An unsupported annotation (ok=false) already reported its own error; the param
// adopts a fresh var rather than the `never` placeholder so the body and any
// call site recover against an unconstrained variable instead of cascading
// `<: never` failures.
func (c *checker) paramType(p *ast.Param, lvl int) soltype.Type {
	if p.TypeAnn != nil {
		if t, ok := c.resolveTypeAnn(p.TypeAnn, lvl); ok {
			return t
		}
	}
	return c.freshAt(lvl)
}

// inferBorrow types a borrow expression `&p` or `&mut p`. The result is a
// RefType over the operand's carrier, carrying a fresh inferred lifetime. The
// operand is constrained against the wrapper so the existing RefType<:RefType
// and bare<:RefType rules enforce the rest. An immutable operand fails the
// mutability check against `&mut`, and an owned operand satisfies a borrow destination
// the same way a call-site argument does.
//
// The inner is taken directly from the operand rather than a fresh variable, so
// the borrow renders against the operand's actual shape. `&mut p` on an
// owned-mutable `mut {x: number}` reads as `mut {x: number}` when the lifetime
// elides locally, and as `&'a mut {x: number}` when the borrow reaches an output.
//
// Concretely, `fn f(p: mut {x: number}) { return &mut p }` renders as
// `fn (p: mut {x: number}) -> mut {x: number}`. The fresh lifetime on `&mut p`
// has no upper bound from `p` because `p` is owned (Lt nil), so it occurs only
// positively in the return, fails the param-lifetime test, and D4 elides the
// wrapper. The borrow is real in the type graph; the elision just hides it at
// display time. A proper rejection of this dangling-borrow case needs the
// directional lifetime bounds slated for M6.5.
//
// `&p` and `&mut p` are the explicit borrow form. A binding initializer uses one of
// them to choose "borrow" over "move", as in `val q = &p` and `val q = &mut p`, so
// the borrow leaves the source usable where a bare `val q = p` would consume it.
func (c *checker) inferBorrow(scope *Scope, lvl int, e *ast.BorrowExpr) soltype.Type {
	// PR 4. An explicit borrow of a field path takes the receiver-bounded
	// lifetime of the implicit read at field granularity. The dispatch covers
	// both `MemberExpr` and the constant-string `IndexExpr` form, so
	// `&mut obj["foo"]` behaves the same as `&mut obj.foo`. A `&mut` of a
	// reference-shaped field has to go through this path. The ordinary borrow
	// path would reject it as a mutability mismatch against the immutable
	// wrap PR 4's `fieldReadBorrow` puts on an implicit read.
	switch arg := e.Arg.(type) {
	case *ast.MemberExpr:
		if !arg.OptChain && arg.Prop != nil && arg.Prop.Name != "" {
			return c.inferBorrowOfMember(scope, lvl, e, arg.Object, arg.Prop.Name, arg.Prop, arg)
		}
	case *ast.IndexExpr:
		if !arg.OptChain {
			if name, ok := constStringKey(arg.Index); ok {
				return c.inferBorrowOfMember(scope, lvl, e, arg.Object, name, arg.Index, arg)
			}
		}
	}
	sub := c.inferExpr(scope, lvl, e.Arg)
	return c.wrapBorrow(e, lvl, sub)
}

// wrapBorrow wraps an already-typed operand in a `&` or `&mut` borrow, the
// shared core of inferBorrow's main path and the namespace fallback in
// inferBorrowOfMember. The operand's ErrorType recovery sentinel passes
// through unchanged so a reported diagnostic does not cascade a second one.
// A primitive, function, or promise reports the non-borrowable diagnostic and
// builds the wrapper around a fresh inner var, keeping the surrounding
// expression cascade-safe.
func (c *checker) wrapBorrow(e *ast.BorrowExpr, lvl int, sub soltype.Type) soltype.Type {
	if _, ok := sub.(*soltype.ErrorType); ok {
		c.recordType(e, sub)
		return sub
	}
	var inner soltype.RefInner
	constrainable := true
	switch s := sub.(type) {
	case *soltype.RefType:
		inner = s.Inner
	case soltype.RefInner:
		// ObjectType, TupleType, or TypeVarType — all valid borrow inners.
		inner = s
	default:
		// A primitive, function, or promise is not a RefInner and has nothing
		// to borrow. Routing `5 <: &T` through bare<:RefType would raise a
		// second "cannot constrain" diagnostic on top of the single
		// non-borrowable report, so the constrain step is skipped.
		c.reportUnsupportedFeature(e, "borrow of a non-borrowable type")
		inner = c.freshAt(lvl)
		constrainable = false
	}
	lt := c.ctx.freshLifetime(lvl)
	target := &soltype.RefType{Mut: e.Mut, Lt: lt, Inner: inner}
	c.recordProv(target, e, BorrowExprOrigin)
	if constrainable {
		c.constrain(e, sub, target)
	}
	c.recordType(e, target)
	return target
}

// inferBorrowOfMember types `&obj.f` and `&mut obj.f`, plus the constant-string
// index form `&obj["foo"]` and `&mut obj["foo"]`, applying PR 4 rule 4. It
// reads the field as a borrow bounded by the receiver at the requested
// mutability. An owned receiver mints a fresh lifetime. A borrowed receiver's
// lifetime passes through. A `&mut` borrow requires the receiver to support a
// mutable view of the field, expressed as the same mutable inexact requirement
// inferMemberAssign uses for `obj.f = v`. The receiver is resolved through
// resolvePath rather than inferExpr so a namespace receiver walks through.
//
// A namespace receiver names a namespace value, not a field of a value object.
// `&Foo.bar` for namespace `Foo` is the form that hits this case. There is no
// receiver region to bound the borrow's lifetime. The namespace case resolves
// the member through resolveNamespaceMember and falls through to wrapBorrow on
// the resolved value, matching the pre-PR-4 path.
//
// Path-granular tracking that leaves a disjoint sibling such as `obj.g`
// independently usable is the partial-moves work in PR 7. PR 4 ships only the
// typing rule.
//
// The arguments name the parts of the field access uniformly across the two
// shapes the dispatch in inferBorrow accepts. recvExpr is the receiver
// expression. propName is the field name, taken from a dot identifier or a
// constant-string index key. provNode is the AST node that owns blame for the
// fresh check var's provenance, either the `.prop` identifier or the string
// literal key. accessNode is the whole MemberExpr or IndexExpr, used for the
// Info record on the inner access shape.
func (c *checker) inferBorrowOfMember(scope *Scope, lvl int, e *ast.BorrowExpr, recvExpr ast.Expr, propName string, provNode ast.Node, accessNode ast.Expr) soltype.Type {
	obj := c.resolvePath(scope, lvl, recvExpr, true)
	if obj.err {
		recovery := soltype.Type(&soltype.ErrorType{})
		c.recordType(accessNode, recovery)
		c.recordType(e, recovery)
		return recovery
	}
	if obj.ns != nil {
		// `Foo.bar` for a namespace `Foo` names a namespace value, not a field
		// of a value receiver. resolveNamespaceMember records the resolved
		// value's type on accessNode and reports an unknown name. The wrap
		// then runs on the value with no receiver-bounded lifetime.
		nsResult := c.resolveNamespaceMember(lvl, accessNode, obj.ns, propName)
		sub := c.demandValue(nsResult, accessNode)
		return c.wrapBorrow(e, lvl, sub)
	}
	recv := obj.value
	if _, ok := recv.(*soltype.ErrorType); ok {
		c.recordType(accessNode, recv)
		c.recordType(e, recv)
		return recv
	}
	// Borrowing a field reads its place, so a use-after-move test sees `&obj.f` as a
	// use of `obj.f`. The receiver resolved with objPos set, so it recorded no
	// whole-receiver use that would collide with a partial move of a sibling.
	c.recordMemberUse(accessNode)
	_, _, recvLt := soltype.UnwrapRef(recv)
	recvCarrier := soltype.CarrierOf(recv)
	// Read-after-write cache. A usage-inferred receiver may carry a recorded
	// write for this field. Take the cached value as the field's static shape
	// so the explicit borrow uses the same precise type the implicit read
	// would. The cache is keyed on a TypeVar receiver, matching the gate
	// inferMemberAssign uses when it records a write.
	var cachedT soltype.Type
	if c.fn != nil {
		if v, ok := recv.(*soltype.TypeVarType); ok {
			if t, found := c.fn.written[fieldKey{recvID: v.ID, field: propName}]; found {
				cachedT = t
			}
		}
	}
	// The constraint validates that the receiver supports the requested view
	// of this field. A `&mut` lowers to the mutable inexact requirement
	// inferMemberAssign uses for `obj.f = v`. A `&` lowers to the ordinary
	// immutable inexact read requirement. The requirement's fresh field var
	// is consumed here for the check only. The borrow returned to the caller
	// wires its Inner to the receiver's static property type when one is
	// known, so the wrap survives coalescing.
	//
	// Routing the result through the fresh field var would let the co-occurrence
	// pass widen it into a union node that is not a `RefInner` and peel the borrow
	// wrapper away. The pass widens because the mut-context flag pins the field
	// invariant, so the same var occurs in both polarities.
	fieldVar := c.freshAt(lvl)
	c.recordProv(fieldVar, provNode, MemberAccess)
	propSelection := soltype.ObjectType{
		Elems:   []soltype.ObjTypeElem{&soltype.PropertyElem{Name: propName, Type: fieldVar}},
		Inexact: true,
	}
	if e.Mut {
		mutPropSelection := &soltype.RefType{
			Mut:   true,
			Lt:    c.ctx.freshLifetime(lvl),
			Inner: &propSelection,
		}
		c.constrain(e, recv, mutPropSelection)
	} else {
		c.constrain(e, recvCarrier, &propSelection)
	}
	// Choose the inner so the borrow wrapper survives. The co-occurrence pass
	// peels any borrow whose inner is the bare field var `fieldVar`, so giving the
	// result a concrete inner is what keeps it rendering as a `RefType`. Pick
	// the most precise shape available: a read-after-write cache hit first,
	// then the property type from the receiver's annotation, and `fieldVar` itself
	// only when neither is known. borrowInnerOf peels owned-mut cells (deep-mut
	// output) but leaves borrow fields intact so they keep their own lifetime.
	var inner soltype.RefInner = fieldVar
	if cachedT != nil {
		if ri, ok := borrowInnerOf(cachedT); ok {
			inner = ri
		}
	} else if recvObj, ok := recvCarrier.(*soltype.ObjectType); ok {
		if prop, ok := recvObj.Prop(propName); ok {
			if ri, ok := borrowInnerOf(prop.Type); ok {
				inner = ri
			}
		}
	}
	lt := recvLt
	if lt == nil {
		lt = c.ctx.freshLifetime(lvl)
	}
	target := &soltype.RefType{Mut: e.Mut, Lt: lt, Inner: inner}
	c.recordProv(target, e, BorrowExprOrigin)
	// Record on the access node the shape an implicit read would produce.
	// fieldReadBorrow makes the same wrap decision valueProp uses, so hover
	// on the inner `obj.f` reads the same whether it stands alone or under
	// `&obj.f` or `&mut obj.f`.
	c.recordType(accessNode, c.fieldReadBorrow(fieldVar, recv, propName, lvl))
	c.recordType(e, target)
	return target
}

// borrowInnerOf returns the RefInner an explicit `&`/`&mut obj.f` should re-wrap
// at the receiver's lifetime. The lazy deep-mut form (PR 14) no longer synthesizes
// owned-mut cells for a plain `mut {a: {x}}`, so a field is usually the bare
// object/tuple the user wrote, returned by the ordinary RefInner cast. Two RefType
// cases remain, the same shapes fieldReadBorrow distinguishes:
//   - An explicit `mut {x}` field, Lt nil: peel to its bare inner so `&mut obj.f`
//     re-wraps a clean `{x}` rather than the fresh check var, which the
//     co-occurrence pass would widen into a union and strip the borrow.
//   - A borrow field, Lt set: return ok=false so the field's own borrow flows
//     through unchanged.
func borrowInnerOf(t soltype.Type) (soltype.RefInner, bool) {
	if r, ok := t.(*soltype.RefType); ok {
		if r.Lt == nil {
			return r.Inner, ok
		}
		return nil, false
	}
	ri, ok := t.(soltype.RefInner)
	return ri, ok
}

// inferCall types a function application. It types the callee and each argument,
// allocates a fresh result var, and constrains callee <: fn(args) -> res — the
// production form of the spike's *App case. The result var picks up the callee's
// return type (covariantly) and renders as that once coalesced; an arity or
// argument mismatch surfaces as a constraint error stamped with the call's span.
//
// Error recovery: a call to a known function still yields that function's
// declared return type even when the arguments don't match, so a downstream
// expression sees the real return type rather than a poisoned `never`. constrain
// short-circuits its FuncType arity arm before propagating the return into res,
// so the return is wired through directly here. The callee is concrete either as a
// bare FuncType (an inline callee) OR as a var whose lower bound is a FuncType (a
// named/generalized callee, which inferIdent now resolves through instantiate — see
// resolveFunc); both recover, so recovery no longer regresses for named callees.
//
// PR4 adds two #677 pieces: an EXACT all-required call demand, and the extra-arg
// lint that rejects passing more arguments than a concrete callee declares.
func (c *checker) inferCall(scope *Scope, lvl int, e *ast.CallExpr) soltype.Type {
	// PR6: a DIRECT call to an overloaded name resolves against the overload set via
	// resolveOverload, a phase distinct from constrain — so the disjunction stays out of
	// the lattice. A call through an intermediate binding (`g = f; g(x)`) doesn't match
	// here; it routes through the value-position intersection (constrain's
	// IntersectionType arm) instead.
	if ident, ok := e.Callee.(*ast.IdentExpr); ok {
		if b, found := scope.GetValue(ident.Name); found && b.IsOverloaded() {
			return c.inferOverloadedCall(scope, lvl, e, b)
		}
	}
	// Resolve the enclosing statement's CFG point before inferring the callee or
	// arguments. Inferring a child that contains statements, such as an `if` argument,
	// overwrites c.fn.currentStmt, so reading the point afterward would record an
	// argument move against an inner branch instead of this call's statement.
	consumeRef, hasConsumeRef := c.currentStmtRef()
	callee := c.inferExpr(scope, lvl, e.Callee)
	args := make([]*soltype.FuncParam, len(e.Args))
	for i, a := range e.Args {
		args[i] = &soltype.FuncParam{Type: c.inferExpr(scope, lvl, a)}
	}
	res := c.freshAt(lvl)
	c.recordProv(res, e, Application)

	// Arity lints (#677 §4.2.3): a DIRECT call rejects too-many AND too-few arguments
	// — for exact AND inexact callees alike. These are call-site checks the subtype
	// lattice does not model uniformly (an inexact callee tolerates extras as a
	// *callback*, accept-set [required, ∞), but supplying extras to a call you can see
	// is a mistake). They fire only when the callee is concrete; for a deferred (var)
	// callee they are best-effort skipped while too-few still surfaces from the gate.
	//
	// When a lint fires, the demand is reshaped to the callee's declared arity
	// (len(fn.Params)) so the EXACT synth's accept-set gate does NOT also report
	// arity (the lint owns the single, uniform message; the constraint does pure
	// type-flow on the supplied args). Too-many truncates to the prefix; too-few pads
	// the missing parameters with fresh vars, which impose no constraint on absent args.
	fn, resolved := resolveFunc(callee)
	demand := args
	switch {
	case resolved && !hasRest(fn) && len(args) > len(fn.Params):
		// A typed rest param (hasRest) absorbs any number of trailing args, so it is
		// never "too many" — only a fixed-arity (non-rest) callee trips this lint.
		c.errs = append(c.errs, &TooManyArgsError{Call: e, Fn: fn})
		demand = args[:len(fn.Params)]
	case resolved && len(args) < requiredCount(fn):
		c.errs = append(c.errs, &NotEnoughArgsError{Call: e, Fn: fn})
		demand = make([]*soltype.FuncParam, len(fn.Params))
		copy(demand, args)
		for i := len(args); i < len(fn.Params); i++ {
			demand[i] = &soltype.FuncParam{Type: c.freshAt(lvl)}
		}
	}

	// Grant the immutable→mutable upgrade per argument: a uniquely-owned argument
	// flowing into an owned-mutable parameter takes the mutable type, the same grant the
	// annotated declaration makes, so `f({x: 1})` and `f(cfg)` for an owned-mutable
	// parameter type-check. The argument's shape is constrained covariantly against the
	// parameter's immutable read view, and the demand entry for that argument is pinned to
	// the parameter's own type so the callee <: callShape constraint below does not re-check
	// it strictly.
	// consumeCallArgs still moves the argument, since an owned-mutable parameter is
	// concrete-owned. It runs only for a resolved callee, where the parameter types are
	// known. A deferred callee, one called through a `var`, keeps every argument on the
	// strict path.
	if resolved {
		for i := 0; i < len(e.Args) && i < len(fn.Params); i++ {
			if c.tryUpgradeToOwnedMut(e.Args[i], e.Args[i], demand[i].Type, fn.Params[i].Type) {
				// The upgrade constrained the argument's shape against the parameter's
				// immutable read view, so pin this argument's demand entry to the parameter's
				// own type; the callee <: callShape constraint below then re-checks it as
				// param<:param rather than strictly rejecting the immutable argument.
				demand[i] = &soltype.FuncParam{Type: fn.Params[i].Type}
			}
		}
	}

	// callShape is built EXACT with all N params required, on purpose. That gives
	// it accept-set [N, N] (N = arg count), so the callee <: callShape constraint
	// reads "the callee must accept exactly N args" — it holds iff
	// required(callee) <= N <= upper(callee). If callShape were INEXACT instead,
	// its accept-set would widen to [N, ∞), demanding upper(callee) = ∞ and thus
	// rejecting every call to a fixed-arity (exact) function.
	callShape := &soltype.FuncType{Params: demand, Ret: res}
	// Record the synthesized call-shape against the CallExpr so a FuncArityMismatchError
	// — now only from a DEFERRED callee's too-few (or a callback-arity failure), since
	// concrete arity faults are owned by the lints above — resolves its blame to the call.
	c.recordProv(callShape, e, CallShape)
	c.constrain(e, callee, callShape)
	if resolved {
		c.constrain(e, fn.Ret, res)
		// Passing an owned argument to a bare owned parameter moves it; a `&`/`&mut`
		// parameter auto-borrows and leaves the argument usable. An arity-mismatched call
		// still moves each argument that lines up with a parameter, so `store(p, p)` moves
		// the first p and a later use of p is a use-after-move. consumeCallArgs skips the
		// extra arguments that have no corresponding parameter.
		if hasConsumeRef {
			c.consumeCallArgs(e, fn, consumeRef)
		}
	}
	c.recordType(e, res)
	return res
}

// consumeCallArgs consumes each owned argument passed to a bare owned parameter of
// the resolved callee, recording the move at ref, the call's statement. A `&`/`&mut`
// parameter borrows, so it leaves the argument usable. An unannotated parameter, whose
// ownership is a fresh inference variable rather than a concrete owned shape, is left
// to borrow conservatively, so only a parameter typed as a concrete owned object,
// tuple, or owned RefType consumes its argument. An extra argument beyond the declared
// parameters, the surplus of a too-many-arguments call, has no parameter to move into,
// so it is skipped.
func (c *checker) consumeCallArgs(e *ast.CallExpr, fn *soltype.FuncType, ref liveness.StmtRef) {
	for i, arg := range e.Args {
		if i >= len(fn.Params) {
			break
		}
		if !isConcreteOwned(fn.Params[i].Type) {
			continue
		}
		c.consumeOwned(arg, c.info.TypeOf(arg), arg, ref)
	}
}

// inferOverloadedCall types a direct call to an overloaded name (PR6). It infers
// the types of the arguments, records the callee's overload type for Info, and
// resolves the call through resolveOverload, which trials each arm under a probe
// and commits the winner. Unlike the ordinary path it emits no callee <: callShape
// constraint.  Overload resolution is a separate phase that owns arity and argument
// checking. The TooManyArgs and NotEnoughArgs lints don't apply – arity is the
// per-arm gate, and a no-match becomes a NoMatchingOverloadError.
func (c *checker) inferOverloadedCall(scope *Scope, lvl int, e *ast.CallExpr, b ValueBinding) soltype.Type {
	args := make([]soltype.Type, len(e.Args))
	for i, a := range e.Args {
		args[i] = c.inferExpr(scope, lvl, a)
	}
	// Record the callee's display type for Info (hover) via overloadDisplayType, which
	// coalesces the schemes rather than instantiating them — resolveOverload below does
	// the (only) per-arm instantiation needed to type the call.
	c.recordType(e.Callee, overloadDisplayType(b))
	ret := c.resolveOverload(lvl, b, args, e)
	c.recordType(e, ret)
	return ret
}

// resolveFunc resolves a callee to its concrete FuncType, used to recover a
// call's return type. The callee is either a FuncType directly (an inline callee)
// or a var whose first FuncType lower bound is the function (a named/generalized
// callee, since inferIdent returns instantiate(scheme) — a fresh var). Looking
// through the var matters because otherwise an arity-mismatched call to a named
// function would lose return recovery and yield `never`.
//
// ok=false means no concrete func was found (e.g. a deferred callee with no lower
// bound yet) — the caller skips return recovery. PR1 bindings have at most one
// func lower bound; overload sets (PR6) resolve through resolveOverload, not here.
func resolveFunc(t soltype.Type) (*soltype.FuncType, bool) {
	switch t := t.(type) {
	case *soltype.FuncType:
		return t, true
	case *soltype.TypeVarType:
		for _, lb := range t.LowerBounds {
			if fn, ok := lb.(*soltype.FuncType); ok {
				return fn, true
			}
		}
	}
	return nil, false
}

// inferAssign types a reassignment `target = source` — the only BinaryExpr form the
// M3 walk handles. The source value is typed first (so its own errors surface
// regardless of the target's validity), then the target is resolved and gated:
//
//   - The target must be a place: an IdentExpr resolving to a value binding. A
//     literal, call, member, or any other non-place target is an
//     InvalidAssignmentTargetError (member targets `obj.x = …` need record types,
//     M4). An ident that resolves to no binding is an UnknownIdentifierError.
//   - The binding must be reassignable: only a `var` (Kind == VarKind) is. A `val`,
//     function, parameter, or prelude binding is a CannotAssignToImmutableError.
//
// On success the source is constrained `<: target` (the binding's coalesced type),
// the new-solver form of the old checker's `Unify(rightType, leftType)`: the value
// being stored must be a subtype of the binding's type. Reassigning an annotated `var a:
// number = 5` with `a = 6` checks; an un-annotated `var a = 5` now widens its
// binding to `number` (M4 B3), so `a = 6` checks there too.
//
// The assignment EXPRESSION evaluates to the value just stored, so its type is the
// target binding's type — `val b = (a = 6)` for `var a: number` yields
// `b: number`. On an error path (invalid / immutable / unknown target) no value is
// stored, so it recovers to `void`.
func (c *checker) inferAssign(scope *Scope, lvl int, e *ast.BinaryExpr) soltype.Type {
	voidT := soltype.Type(&soltype.Void{})
	// Guard a malformed assignment node (the real parser substitutes ast.NewError for
	// a missing operand, so this is unreachable from source — but a hand-built AST
	// could have a nil operand). Blame the whole expression rather than dereferencing
	// a nil operand in inferExpr / InvalidAssignmentTargetError later.
	if e.Left == nil || e.Right == nil {
		return c.reportUnsupported(e)
	}
	// M4 G1: snapshot the enclosing statement BEFORE walking the RHS. Reassignment
	// transition checking needs this assignment's statement to find its CFG StmtRef,
	// but walking an RHS that contains statements re-enters inferStmt and overwrites
	// c.fn.currentStmt. A `b = if c { … } else { … }`, a match, or a block expression
	// all do this. Capturing the statement here keeps the reassignment path on the
	// right program point, the way the var-decl path threads its statement explicitly.
	var assignStmt ast.Stmt
	if c.fn != nil {
		assignStmt = c.fn.currentStmt
	}
	sourceT := c.inferExpr(scope, lvl, e.Right)
	// Record void on e up front as the recovery type: every error path below returns
	// voidT without recording a type, so this guarantees the node is typed on failure.
	// The success path overwrites it with the stored value's type (see end of function).
	c.recordType(e, voidT)

	target, ok := e.Left.(*ast.IdentExpr)
	if !ok {
		// A member target (obj.x = …) is a field write: the receiver must accept a
		// write to that field (M4 C3). An index target (xs[i] = …) still needs Array
		// and index types (M7), so it stays unsupported, distinct from a fundamentally
		// invalid target like `5 = x` or `f() = x`, which is an
		// InvalidAssignmentTargetError.
		switch left := e.Left.(type) {
		case *ast.MemberExpr:
			return c.inferMemberAssign(scope, lvl, e, left, sourceT, assignStmt)
		case *ast.IndexExpr:
			c.reportUnsupportedFeature(e.Left, "assignment to a member or index")
		default:
			c.report(&InvalidAssignmentTargetError{Target: e.Left})
		}
		return voidT
	}
	b, found := scope.GetValue(target.Name)
	if !found || len(b.Schemes) == 0 {
		// Not a value binding. Mirror inferIdent's value-position behavior: a name that
		// resolves to a namespace reports NamespaceUsedAsValue; otherwise it is an
		// unknown identifier.
		if ns, isNS := scope.GetNamespace(target.Name); isNS {
			c.report(&NamespaceUsedAsValueError{Node: target, NS: ns})
		} else {
			c.report(&UnknownIdentifierError{Ident: target})
		}
		return voidT
	}
	if b.Kind != ast.VarKind {
		c.report(&CannotAssignToImmutableError{
			Assign: e,
			Name:   target.Name,
			Decl:   bindingDecl(b),
		})
		return voidT
	}
	// The source value must be a subtype of the target binding's type. Use the binding's
	// COALESCED type (schemeType — what Info records and the printer renders), not a
	// fresh instantiation: instantiating a generalized binding yields a var carrying
	// only its LOWER bounds (the read/covariant face), so `a = "x"` for `var a:
	// number` would merely add another lower bound and wrongly succeed. The coalesced
	// type is the concrete binding type — `number` for an annotated var, and (since M4
	// B3) the widened `number` for an un-annotated `var a = 5`, so `a = 6` ⇒
	// `6 <: number` checks.
	//
	// freshenAll copies the coalesced type so constraining the source cannot mutate
	// type-parameter vars the coalesced form still shares with the binding
	// (coalesceScheme retains them by pointer): without the copy, reassigning a
	// polymorphic var would poison it for every later use. A var-free coalesced type
	// (the common annotated/literal case) freshens to itself.
	//
	// A probe can't do this: Discard would also roll back the constraint's real errors
	// and the source's bound, while Commit would keep the binding poisoning — we need to
	// suppress one side effect, not the whole trial. freshenAll isolates just the var.
	//
	// b.Schemes[0]: a reassignable binding is always single-scheme — overload sets
	// come only from FuncDecls, whose Kind is never VarKind, so they are rejected by
	// the `b.Kind != ast.VarKind` gate above before reaching here.
	targetT := c.freshenAll(schemeType(b.Schemes[0]), lvl)
	c.recordType(target, targetT)
	// M4 G1: track the alias this reassignment creates and check its mutability
	// transition, but only when the constraint below succeeds — an ill-typed
	// reassignment must not seed a false-positive transition error off types that
	// never matched. assignErrsBefore captures the pre-constraint error count.
	assignErrsBefore := len(c.errs)
	if b.ModuleLevel {
		// This is a global write, a store into module-level storage that lives for the
		// program's whole run. Any borrow the value carries must outlive every borrow
		// region, so it escapes to 'static (M4 D3).
		//
		// The value-compatibility check runs against the source's CARRIER, not the
		// borrow itself. A borrow forced to 'static is owned-forever, so it satisfies an
		// owned destination. Comparing the whole borrow would instead trip the
		// borrow-into-owned BorrowEscapeError, the rule that rejects a borrow which does
		// NOT escape. CarrierOf is the identity on a non-borrow source, so an ordinary
		// global write such as `n = 5` is unaffected. The peel only looks through a
		// top-level borrow and drops the source's mutability check. The fuller treatment
		// of an escaped borrow's mutability rides M4 G2.
		//
		// Escape runs only when the compatibility check passes, so a rejected store does
		// not leave the source's lifetime forced to 'static. So `var sink = {…}; fn(p:
		// mut {…}) { sink = p }` reports p as `mut 'static {…}`.
		errsBefore := len(c.errs)
		// A uniquely-owned source stored into an owned-mutable global takes the same
		// immutable→mutable upgrade as the local reassignment path, so `sink = {x: 1}`
		// type-checks. The carrier feeds the upgrade for the same reason it feeds the
		// strict check below: a borrow forced to 'static satisfies the owned global.
		if !c.tryUpgradeToOwnedMut(e, e.Right, soltype.CarrierOf(sourceT), targetT) {
			c.constrain(e, soltype.CarrierOf(sourceT), targetT)
		}
		if len(c.errs) == errsBefore {
			// The store aliases the source into a permanent module-level storage location. If the
			// source's mutability differs from that location's and the source stays live at
			// the conflicting mutability, that is a mut↔immutable transition the local
			// reassignment path cannot see, since the global target is not a tracked
			// local. Check it before forcing the escape so the source's own escape is
			// not double-counted as a prior permanent alias.
			c.checkGlobalWriteTransition(target, e.Right, bindingType(b), assignStmt)
			c.constrainEscape(sourceT)
			// The store transfers the value into a permanent 'static storage location, so it consumes
			// the source binding. A later use of the source is then a use-after-move, the
			// affine rule that closes the leak the global write otherwise allowed.
			// checkGlobalWriteTransition above skips the source's own self-conflict,
			// leaving the consume to govern the source and the exclusivity rule to govern
			// any OTHER live alias of the same value.
			if c.fn != nil {
				if ref, ok := c.fn.stmtToRef[assignStmt]; ok {
					c.consumeAtGlobalWrite(e.Right, sourceT, e.Right, ref)
				}
			}
			// KNOWN GAP (#762): this store is accepted even though it is not sound in
			// general. checkGlobalWriteTransition is an in-body check only. The store
			// escapes the source to 'static, but nothing forces the CALLER to pass a
			// unique 'static borrow, so the caller may retain a live mutable alias to the
			// same value and mutate it after the call, which the immutable global then
			// observes. Closing this needs the call site to enforce the 'static borrow as
			// unique, which is the borrow checker's job. Move/affine semantics (#762),
			// under the sound borrow checker (#618), will eventually reject it.
		}
	} else if !c.tryUpgradeToOwnedMut(e, e.Right, sourceT, targetT) {
		// A uniquely-owned source reassigned into an owned-mutable `var` takes the
		// immutable→mutable upgrade, the same grant the annotated declaration makes. The
		// upgrade fires only for a RefType target, so a union target instead routes through
		// constrain's union-super exists rule, which trials each member under a probe.
		c.constrain(e, sourceT, targetT)
	}
	if c.fn != nil && len(c.errs) == assignErrsBefore && target.VarID > 0 {
		// Track against the binding's own type, not the freshened targetT. The G2
		// escape query reads the recorded type, and a later global write forces the
		// lifetime on the binding's shared pointer. The freshened copy carries an
		// independent lifetime var that the escape never touches. isMutableType reads
		// the same top-level Mut from either, so the alias mutability is unchanged.
		c.trackAliasesForAssignment(target, e.Right, bindingType(b), assignStmt)
		// A non-module reassignment that moves its source consumes it. The global-write
		// branch above runs its own consume, so this covers only the local-binding
		// reassignment.
		if !b.ModuleLevel && c.movesSourceInto(e.Right, bindingType(b)) {
			if ref, ok := c.fn.stmtToRef[assignStmt]; ok {
				c.consumeOwned(e.Right, c.info.TypeOf(e.Right), e.Right, ref)
			}
		}
	}
	// The assignment evaluates to the value just stored — the SAME read face as
	// reading the target (inferIdent), so `val b = (a = 6)` ⇒ `b: number`. Use
	// instantiate (the read face), NOT the coalesced write-face targetT: targetT is a
	// display type that may be a Union/Intersection node, and re-injecting it into the
	// constraint graph here would later crash the coalescer when this value flows on
	// (e.g. through a `return`). This overwrites the `void` recorded for e above,
	// which now serves only as the error-path recovery value.
	valueT := c.instantiate(b.Schemes[0], lvl)
	c.recordType(e, valueT)
	return valueT
}

// inferMemberAssign types a field write `recv.prop = source` (M4 C3). It extends
// inferAssign's member-target branch: the receiver must ACCEPT a write to prop, so
// the source is constrained against a mutable, inexact one-property requirement
//
//	recv <: mut {prop: widen(source), ...}
//
// The inexact requirement says "must accept a write to this field," not "is exactly
// this shape." The mut wrapper makes the receiver a mutable cell, which the C3
// coalesce fold collapses with the receiver's other selections into one `mut`
// object. The stored value is WIDENED (5 ⇒ number) because writing through a `mut`
// receiver is itself a mutation — a later write may store any number — mirroring
// the `var`-binding widening (B3).
//
// The write requirement carries a fresh lifetime (D2): a mut-borrow receiver of
// any lifetime is accepted (the fresh var imposes no obligation), and an owned
// receiver satisfies the borrow destination by the RefType rule.
//
// A write has no result borrow to construct, so it needs no counterpart to the
// read path's fieldReadBorrow. That helper builds the value a read yields: a
// reference-shaped field comes back as a fresh borrow bounded by the receiver.
// Here the borrow exists only as the `mut` requirement above. The requirement
// constrains the receiver and is then discarded. The assignment's own value is
// the plain stored `w` recorded below.
//
// When the receiver is a variable, the widened type is recorded in `written` so a
// later read of the same field returns it (read-after-write; see valueProp). The
// assignment evaluates to the value just stored, so its type is the widened source.
func (c *checker) inferMemberAssign(scope *Scope, lvl int, e *ast.BinaryExpr, m *ast.MemberExpr, source soltype.Type, assignStmt ast.Stmt) soltype.Type {
	voidT := soltype.Type(&soltype.Void{})
	if m.OptChain {
		// `recv?.prop = …` is not a meaningful assignment target; optional chaining is
		// M6 regardless, so report the whole target as unsupported rather than typing it.
		c.reportUnsupportedFeature(e.Left, "assignment to a member or index")
		return voidT
	}
	if m.Prop == nil || m.Prop.Name == "" {
		// A malformed `recv. = …`: the parser already reported the missing property
		// name, so emit nothing further and recover to void.
		return voidT
	}
	recv := c.inferWriteReceiver(scope, lvl, m.Object)
	w := widen(source)
	// An owned-mutable field takes the immutable→mutable upgrade through the same shared
	// helper as the other value-flow sites, so the field write stays consistent with them:
	// a uniquely-owned source is constrained covariantly against the field's read view, and
	// the field's owned-mutable type is stored. The field's declared type is read off the
	// receiver's carrier. An owned-mutable field arises only through inference, since #779
	// rejects the annotation, so no source program reaches this branch today. The guard
	// keeps the field write consistent for when one does.
	if recvObj, ok := soltype.CarrierOf(recv).(*soltype.ObjectType); ok {
		if prop, ok := recvObj.Prop(m.Prop.Name); ok && c.tryUpgradeToOwnedMut(e.Right, e.Right, source, prop.Type) {
			w = prop.Type
		}
	}
	// Catch a readonly write at the assignment site so the diagnostic blames it
	// outright; a TypeVar receiver falls through to the structural
	// ReadonlyFieldSubtypeError the ObjectType write view raises.
	if recvObj, ok := soltype.CarrierOf(recv).(*soltype.ObjectType); ok {
		if prop, ok := recvObj.Prop(m.Prop.Name); ok && prop.Readonly {
			c.report(&ReadonlyFieldError{Field: m.Prop.Name, site: e})
			c.recordWritten(recv, m.Prop.Name, w)
			c.recordType(e, w)
			return w
		}
	}
	req := &soltype.RefType{
		Mut: true,
		// A fresh lifetime imposes no obligation on the receiver (D2): constrainLt
		// gives the new variable an upper bound and constrains nothing back, so a
		// mut-borrow receiver of ANY lifetime satisfies the write requirement. A
		// nil lifetime would instead reject a borrow receiver as an escape.
		Lt: c.ctx.freshLifetime(lvl),
		Inner: &soltype.ObjectType{
			Elems:   []soltype.ObjTypeElem{&soltype.PropertyElem{Name: m.Prop.Name, Type: w}},
			Inexact: true, // "must accept a write to this field," not a full shape
		},
	}
	errsBefore := len(c.errs)
	c.constrain(e, recv, req)
	c.recordWritten(recv, m.Prop.Name, w)
	// M4 G1: when the written value aliases a variable, merge the receiver's and the
	// source's alias sets so a later transition off either sees the shared value. Only
	// when the write type-checked, so a rejected write does not record a bogus alias.
	if c.fn != nil && len(c.errs) == errsBefore {
		c.trackAliasesForPropAssignment(e.Left, e.Right)
		// Storing an owned value into a field transfers ownership into the receiver, so
		// the source binding is consumed and a later use of it is a use-after-move. The
		// move records against the assignment's statement, resolved from assignStmt
		// rather than c.fn.currentStmt, which inferring the receiver and source may have
		// overwritten with an inner branch statement.
		if ref, ok := c.fn.stmtToRef[assignStmt]; ok {
			c.consumeOwned(e.Right, source, e.Right, ref)
		}
	}
	// The assignment evaluates to the value just stored. recordType overwrites the
	// `void` recovery type inferAssign recorded on e before dispatching here.
	c.recordType(e, w)
	return w
}

// inferWriteReceiver infers the receiver of a field write `recv.prop = …`. Each
// member/index step in the receiver CHAIN imposes a MUTABLE requirement on its own
// receiver rather than the immutable read a plain access would, so writing one nested
// field marks the whole container `mut` (#779): `fn foo(obj) { obj.p.x = 5 }` infers
// `obj: mut {p: …}`, the mutability propagating up to the outermost container. The
// alternative — an owned-mut cell on the inner field inside an immutable container —
// is no longer a valid annotation, so inference must not produce it.
//
// Only the direct receiver of an assignment takes this path; a plain read elsewhere
// in the body stays immutable. A bare identifier reads through ordinary inferExpr,
// since there is no enclosing container to mark.
func (c *checker) inferWriteReceiver(scope *Scope, lvl int, e ast.Expr) soltype.Type {
	switch e := e.(type) {
	case *ast.MemberExpr:
		if e.OptChain || e.Prop == nil || e.Prop.Name == "" {
			return c.inferExpr(scope, lvl, e)
		}
		recv := c.inferWriteReceiver(scope, lvl, e.Object)
		return c.mutFieldRead(lvl, e, e.Prop, e.Prop.Name, recv)
	case *ast.IndexExpr:
		// A constant-string index `obj["p"].x = …` reads the same field `obj.p` would,
		// so it propagates mutability the same way. A dynamic key is not a supported
		// place and falls through to the ordinary path.
		if name, ok := constStringKey(e.Index); ok && !e.OptChain {
			recv := c.inferWriteReceiver(scope, lvl, e.Object)
			return c.mutFieldRead(lvl, e, e.Index, name, recv)
		}
		return c.inferExpr(scope, lvl, e)
	default:
		return c.inferExpr(scope, lvl, e)
	}
}

// mutFieldRead reads property `name` off `recv` as the receiver of a write chain.
// When the receiver's shape is still an inference variable, it imposes a MUTABLE
// requirement `mut {name: fieldVar, ...}` so the inferred container folds `mut` at
// coalesce time (#779) and hands back a mutable borrow of the field, the deep-mut read
// view, so a deeper write relates the field covariantly under the mut-context
// invariance. When the receiver is already concrete — an annotated `mut {…}` or a
// borrow — it defers to valueProp's ordinary read: the deep-mut machinery already
// yields a mutable borrow off a mut receiver, and imposing a fresh inexact requirement
// would clash with the annotation's exact shape under the write-back. blame is the
// access node a constraint failure points at; provNode is the node the fresh field
// var's provenance records.
func (c *checker) mutFieldRead(lvl int, blame ast.Node, provNode ast.Node, name string, recv soltype.Type) soltype.Type {
	// Read-after-write (M4 C3): a field already written to the same receiver var
	// returns the recorded concrete type, the same shortcut valueProp takes.
	if c.fn != nil {
		if v, ok := recv.(*soltype.TypeVarType); ok {
			if t, found := c.fn.written[fieldKey{recvID: v.ID, field: name}]; found {
				c.recordType(blame, t)
				return t
			}
		}
	}
	if _, isVar := soltype.CarrierOf(recv).(*soltype.TypeVarType); !isVar {
		// Concrete receiver: a plain read composes the chain correctly.
		return c.valueProp(lvl, blame, provNode, name, recv).value
	}
	fieldVar := c.freshAt(lvl)
	c.recordProv(fieldVar, provNode, MemberAccess)
	c.constrain(blame, recv, &soltype.RefType{
		Mut: true,
		// A fresh lifetime imposes no obligation on the receiver (D2), matching the
		// leaf write requirement in inferMemberAssign.
		Lt: c.ctx.freshLifetime(lvl),
		Inner: &soltype.ObjectType{
			Elems:   []soltype.ObjTypeElem{&soltype.PropertyElem{Name: name, Type: fieldVar}},
			Inexact: true,
		},
	})
	out := soltype.NewRef(true, c.ctx.freshLifetime(lvl), fieldVar)
	c.recordType(blame, out)
	return out
}

// recordWritten remembers that field `name` of receiver `recv` was written with
// type `t`, so a later read in the same function body returns it (read-after-write;
// see valueProp). Only a VARIABLE receiver has a stable ID to key on; a non-variable
// receiver — a literal or another expression — cannot be read back through the same
// binding, so there is nothing to record. The cache is per function body (c.fn): a
// write at module top-level (c.fn == nil) records nothing, which is sound.
func (c *checker) recordWritten(recv soltype.Type, name string, t soltype.Type) {
	if c.fn == nil {
		return
	}
	if v, ok := recv.(*soltype.TypeVarType); ok {
		c.fn.written[fieldKey{recvID: v.ID, field: name}] = t
	}
}

// bindingDecl returns the AST node of the binding's introducing declaration — the
// "declared immutable here" related span for CannotAssignToImmutableError — or nil
// when the binding has no source node (a prelude binding, or the synthetic
// placeholder for an unsupported param). It reads the first Source: a plain
// `val`/`var`/`fn` — and now a parameter — has exactly one.
func bindingDecl(b ValueBinding) ast.Node {
	if len(b.Sources) == 0 {
		return nil
	}
	if np, ok := b.Sources[0].(*ast.NodeProvenance); ok {
		return np.Node
	}
	return nil
}

// inferTuple types a tuple literal as a soltype.TupleType of its element types
// and records it in Info. Elements are typed left-to-right in the current scope.
//
// A spread element ([...xs]) splices the operand's element types into the literal.
// For example, [...pair, 3] over pair: [number, string] builds
// [number, string, number]. M4 handles only this concrete-literal splice: the
// operand must infer to a TupleType. An inexact operand ([number, ...]) has unknown
// length, so it may only be spread as the last element. There its known prefix
// extends the literal and its unknown tail makes the result inexact too. An inexact
// operand anywhere else would put a later element at an unknown position, which is an
// InexactTupleSpreadError. A spread of any other type is a SpreadNotTupleError. A
// spread whose operand already errored is absorbed silently, so the recovery
// sentinel does not cascade a second diagnostic. Two type-level cousins defer to
// M7/M9: a tuple-spread type over an abstract operand [...P, x], and a typed
// variadic tail [number, ...Array<number>].
func (c *checker) inferTuple(scope *Scope, lvl int, e *ast.TupleExpr) soltype.Type {
	// Resolve the enclosing statement's CFG point before inferring elements, which can
	// overwrite c.fn.currentStmt with an inner branch statement.
	stmtRef, hasStmtRef := c.currentStmtRef()
	elems := make([]soltype.Type, 0, len(e.Elems))
	inexact := false
	for i, el := range e.Elems {
		spread, ok := el.(*ast.ArraySpreadExpr)
		if !ok {
			elemT := c.inferExpr(scope, lvl, el)
			elems = append(elems, elemT)
			// Building an owned value into the tuple moves it.
			c.consumeIntoLiteral(el, elemT, stmtRef, hasStmtRef)
			continue
		}
		switch op := c.inferExpr(scope, lvl, spread.Value).(type) {
		case *soltype.TupleType:
			if op.Inexact && i != len(e.Elems)-1 {
				c.report(&InexactTupleSpreadError{Spread: spread, Operand: op})
				continue
			}
			elems = append(elems, op.Elems...)
			if op.Inexact {
				inexact = true // a trailing inexact spread carries through
			}
			// Spreading an owned tuple moves its elements into the new tuple, so the
			// spread operand is consumed.
			c.consumeIntoLiteral(spread.Value, op, stmtRef, hasStmtRef)
		case *soltype.ErrorType:
			// The operand already reported its own failure; absorb it rather than
			// layering a SpreadNotTupleError on the recovery sentinel.
		default:
			c.report(&SpreadNotTupleError{Spread: spread, Operand: op})
		}
	}
	t := &soltype.TupleType{Elems: elems, Inexact: inexact}
	c.recordType(e, t)
	c.recordProv(t, e, TupleElem)
	return t
}

// inferObject types an object literal as an exact soltype.ObjectType. A property
// with a static key folds into the object. A static key is an identifier label, a
// string-literal key, or a numeric key like {0: v}. The forms it does not cover
// each report an UnsupportedNodeError and are skipped rather than panicking:
//   - spreads ({...o}), deferred to M9 with object rest/spread,
//   - method/constructor elements, which arrive with classes in M5,
//   - computed keys ({[k]: v}), which need M9 index signatures,
//   - shorthand ({x}, a property with no value).
//
// Usage-inference depth builds on this elsewhere, for example inferring an open
// object from how a value is used. Here the closed object the literal spells out is
// built.
//
// Duplicate keys follow JavaScript semantics: the last value wins, keeping the
// property at its first position ({a: 1, b: 2, a: 3} ⇒ {a: 3, b: 2}). This keeps
// property names unique, the invariant ObjectType.Prop / equalType rely on.
func (c *checker) inferObject(scope *Scope, lvl int, e *ast.ObjectExpr) soltype.Type {
	// Resolve the enclosing statement's CFG point before inferring property values,
	// which can overwrite c.fn.currentStmt with an inner branch statement.
	stmtRef, hasStmtRef := c.currentStmtRef()
	b := newObjElemBuilder(len(e.Elems))
	for _, elem := range e.Elems {
		prop, ok := elem.(*ast.PropertyExpr)
		if !ok {
			// ObjSpreadExpr is object rest/spread, which is M9. The method and
			// constructor elements CallableExpr and ConstructorExpr arrive with
			// classes in M5.
			c.reportUnsupported(elem)
			continue
		}
		if prop.Value == nil {
			// Shorthand ({x}) needs the ident's binding folded in as the value.
			c.reportUnsupported(prop)
			continue
		}
		name, ok := objKeyName(prop.Name)
		if !ok {
			// A computed key ({[k]: v}) carries no static property name, so it is M9.
			// Blame the key itself, which has its own narrower span, not the whole
			// property.
			c.reportUnsupported(prop.Name)
			continue
		}
		ft := c.inferExpr(scope, lvl, prop.Value)
		b.add(name, ft, false, false) // a literal property is never optional or readonly
		// Building an owned value into the object moves it.
		c.consumeIntoLiteral(prop.Value, ft, stmtRef, hasStmtRef)
	}
	t := &soltype.ObjectType{Elems: b.elems}
	c.recordType(e, t)
	c.recordProv(t, e, ObjectField)
	return t
}

// objElemBuilder accumulates object PropertyElems under JavaScript's last-wins,
// first-position dedup: a repeated key updates the value in place at the key's
// original position, so property names stay unique — the invariant ObjectType.Prop
// and equalType rely on ({a: 1, b: 2, a: 3} ⇒ {a: 3, b: 2}). Shared by inferObject
// (object literals) and resolveObjectTypeAnn (object type annotations) so the dedup
// rule lives in one place.
//
// It is NOT recursive: it accumulates the direct properties of ONE object level.
// Each property's type arrives already built — inferObject computes it with
// inferExpr, resolveObjectTypeAnn with resolveTypeAnn — so a nested object is built
// by that caller's recursion before add stores it.
type objElemBuilder struct {
	elems []soltype.ObjTypeElem
	pos   map[string]int // property name → index in elems
}

func newObjElemBuilder(capacity int) *objElemBuilder {
	return &objElemBuilder{
		elems: make([]soltype.ObjTypeElem, 0, capacity),
		pos:   make(map[string]int, capacity),
	}
}

func (b *objElemBuilder) add(name string, t soltype.Type, optional, readonly bool) {
	pe := &soltype.PropertyElem{Name: name, Type: t, Optional: optional, Readonly: readonly}
	if i, dup := b.pos[name]; dup {
		b.elems[i] = pe // last value wins, first position kept
		return
	}
	b.pos[name] = len(b.elems)
	b.elems = append(b.elems, pe)
}

// inferMember types a field read (recv.prop) in value position: it resolves the
// member as a path and demands a value, so a property read returns its type while
// a member that resolves to a namespace (A.B used as a value) is rejected.
// Optional chaining (recv?.prop) needs union/undefined handling and is M6.
func (c *checker) inferMember(scope *Scope, lvl int, e *ast.MemberExpr) soltype.Type {
	return c.demandValue(c.resolveMemberPath(scope, lvl, e, false), e)
}

// inferIndex types `obj[index]` in value position — namespace index access
// (Foo["bar"]) and the constant-string bracket form of property access
// (obj["foo-bar"]); dynamic value indexing is M7.
func (c *checker) inferIndex(scope *Scope, lvl int, e *ast.IndexExpr) soltype.Type {
	return c.demandValue(c.resolveIndexPath(scope, lvl, e, false), e)
}

// resolveMemberPath resolves `obj.prop`. It first resolves the object as a path —
// so a namespace object (Foo.bar, A.B.c) walks through as a non-lexical member
// lookup — and otherwise types the object as an ordinary value receiver and reads
// the property structurally.
func (c *checker) resolveMemberPath(scope *Scope, lvl int, e *ast.MemberExpr, objPos bool) pathResult {
	if e.OptChain {
		// Optional chaining (recv?.prop) is wholesale unsupported in M2; report it
		// up front and do NOT descend into the receiver, so a single diagnostic
		// stands for the construct instead of cascading the receiver's errors. The
		// MemberExpr kind is supported — it is the optional-chain feature that is
		// not — so this is an UnsupportedFeatureError blaming the member.
		c.reportUnsupportedFeature(e, "OptionalChain")
		return pathResult{err: true}
	}
	obj := c.resolvePath(scope, lvl, e.Object, true)
	if obj.err {
		return pathResult{err: true}
	}
	if e.Prop == nil || e.Prop.Name == "" {
		// A malformed `recv.` with no valid property name: the parser already
		// reported the missing identifier, so constraining recv <: {"": res} here
		// would only layer a spurious "object is missing property: " on top. Yield
		// the ErrorType recovery sentinel (PR8) — NOT a raw never — so that if this
		// read flows into a sink (`if recv. {}`, `await recv.`, `var x = recv.`) the
		// sentinel absorbs in constrain rather than cascading `never <: …`. report
		// already emitted the diagnostic here (via the parser), so no extra error.
		t := soltype.Type(&soltype.ErrorType{})
		c.recordType(e, t)
		return pathResult{value: t}
	}
	if obj.ns != nil {
		return c.resolveNamespaceMember(lvl, e, obj.ns, e.Prop.Name)
	}
	// Record the read of this place as the outermost read of the chain, so a later
	// use-after-move test sees the full path `pair.a` rather than the spine's bare
	// root. A spine step has objPos true and records nothing; the full place subsumes
	// it.
	if !objPos {
		c.recordMemberUse(e)
	}
	return c.valueMember(lvl, e, obj.value)
}

// valueMember reads property prop off a value receiver: it allocates a fresh
// result var and constrains recv <: {prop: fieldVar, ...} — the basic form from the
// plan's §3.2 table. The requirement is INEXACT: a member read asks only that the
// receiver has AT LEAST this property, so width tolerance is expressed as
// inexactness rather than as an unconditionally width-tolerant arm.
//
// This inexactness currently flows out to the inferred param type. A param used
// only through member reads coalesces to its upper bound, so `fn (p) { p.foo }`
// infers an inexact param `{foo: number, ...}`. M4 phase B PR B1 ("close
// usage-inferred shapes to exact") will seal that coalesced result to exact via
// the Policy-A close, rendering `{foo: number}`. The per-access requirement minted
// here stays inexact; only the coalesced result is closed.
//
// The ObjectType <: ObjectType arm of constrain lowers fieldVar from the receiver's
// matching property (so fieldVar coalesces to that property's type); a receiver
// missing the property surfaces as a MissingPropertyError stamped with the member's span.
func (c *checker) valueMember(lvl int, e *ast.MemberExpr, recv soltype.Type) pathResult {
	// Record the fresh result var against the .prop IDENTIFIER (not the whole
	// MemberExpr), so a missing-property read blames the property (.foo), not the
	// receiver.
	return c.valueProp(lvl, e, e.Prop, e.Prop.Name, recv)
}

// valueProp is the shared core of property access off a value receiver, used by
// both dot access (obj.prop) and constant-string index access (obj["foo-bar"]).
// blame is the node a constraint failure points at — the whole access expression;
// provNode is the node the fresh result var's provenance is recorded against —
// the property identifier for dot access, the string-literal key for index access
// — so a missing-property read blames the property, not the receiver. name is the
// property key being read.
func (c *checker) valueProp(lvl int, blame ast.Node, provNode ast.Node, name string, recv soltype.Type) pathResult {
	// Read-after-write (M4 C3): a read of a field just written to the same receiver
	// var returns the recorded concrete type instead of minting a fresh var, so
	// `obj.x = 5; obj.x` is `number`. The write already constrained the receiver to
	// carry the field, so no additional requirement is needed here.
	//
	// Provenance is deliberately NOT recorded on the returned type. Unlike the fresh
	// `fieldVar` below, the recorded type is SHARED — it also sits in the `written` map, in
	// the write's requirement, and is handed to every read of this field — so it is not
	// the freshly-minted unique pointer recordProv requires (recording it would panic
	// under debugProv and mis-blame the other aliases). A later constraint failure on
	// this value therefore blames its constraint site rather than this `.prop`, the
	// same graceful site fallback a Prov-less type takes everywhere (see
	// TestBlameVoidSubjectFallsBackToCallSite).
	if c.fn != nil {
		if v, ok := recv.(*soltype.TypeVarType); ok {
			if t, found := c.fn.written[fieldKey{recvID: v.ID, field: name}]; found {
				c.recordType(blame, t)
				return pathResult{value: t}
			}
		}
	}
	// Strip the borrow wrapper before building the field-read requirement (D2):
	//   - Reading a field through a `mut`/`'a` borrow is always legal and yields
	//     the field's value, not the borrow.
	//   - It keeps the requirement off the RefType, so the RefType<:bare escape
	//     guard fires only when the borrow flows into an owned destination, not on a read.
	//   - A non-borrow receiver is returned unchanged, leaving plain vars untouched.
	recvCarrier := soltype.CarrierOf(recv)
	fieldVar := c.freshAt(lvl)
	// The member-requirement record {prop: fieldVar} is deliberately NOT recorded —
	// MissingPropertyError blames this inner fieldVar, so the record would be a dead
	// entry (§3.3).
	c.recordProv(fieldVar, provNode, MemberAccess)
	c.constrain(blame, recvCarrier, &soltype.ObjectType{
		Elems:   []soltype.ObjTypeElem{&soltype.PropertyElem{Name: name, Type: fieldVar}},
		Inexact: true, // "has at least this property" — width tolerance is inexactness
	})
	// fieldReadBorrow takes the whole receiver and unwraps mut/lifetime internally,
	// applying PR 4 rule 4 to produce the field-bounded borrow.
	out := c.fieldReadBorrow(fieldVar, recv, name, lvl)
	c.recordType(blame, out)
	return pathResult{value: out}
}

// fieldReadBorrow applies PR 4 rule 4. A member read yields a borrow of the
// field bounded by the receiver when the field is reference-shaped. An owned
// receiver mints a fresh lifetime here. A borrowed receiver's lifetime passes
// through. The wrap reads the field's static shape off a concrete receiver
// carrier. A primitive or function field stays a value, since PrimType and
// FuncType are excluded from RefInner. A field whose static type is itself an
// immutable borrow copies the borrow out flat rather than nesting, setting up
// PR 9's nested-borrow normalization.
//
// A receiver whose shape is not statically known returns the existing `fieldVar`
// unchanged. A usage-inferred TypeVar carrier and an index path with no
// concrete property both fall into this branch. The inferred-receiver paths
// keep their pre-PR-4 behaviour, so only annotated reference shapes pick up
// the new borrow.
func (c *checker) fieldReadBorrow(fieldVar *soltype.TypeVarType, recv soltype.Type, name string, lvl int) soltype.Type {
	_, recvMut, recvLt := soltype.UnwrapRef(recv)
	obj, ok := soltype.CarrierOf(recv).(*soltype.ObjectType)
	if !ok {
		return fieldVar
	}
	prop, ok := obj.Prop(name)
	if !ok {
		return fieldVar
	}
	switch fieldType := prop.Type.(type) {
	case *soltype.RefType:
		if fieldType.Lt == nil {
			// An owned-mutable field cell — formerly an explicit `mut {x}` field, the
			// awkward interior-mutability shape now rejected at the annotation site
			// (#779). Read it as a receiver-bounded borrow, capping `mut` by the
			// receiver's mutability. The lazy deep-mut form does not mint these for a
			// plain `mut {a: {x}}`; that field is bare, handled by the bare arm below.
			// This arm is therefore defensive — kept for any owned-mut cell that still
			// reaches a read.
			lt := recvLt
			if lt == nil {
				lt = c.ctx.freshLifetime(lvl)
			}
			return &soltype.RefType{Mut: fieldType.Mut && recvMut, Lt: lt, Inner: fieldType.Inner}
		}
		if !fieldType.Mut {
			// Flat copy-out of an immutable borrow field. Immutable borrows are
			// freely duplicable, so the read hands back the field's borrow at
			// its own lifetime rather than nesting under the receiver's. A
			// `&mut` field falls through to the no-wrap branch. Aliasing a
			// mutable borrow needs the move-engine work in PR 6, and PR 9
			// retires the depth-two `&mut &mut` shape entirely.
			return fieldType
		}
		return fieldVar
	case *soltype.ObjectType, *soltype.TupleType:
		// A bare object/tuple field a borrowed receiver lends is read as a
		// receiver-bounded borrow whose mutability follows the receiver (PR 14): a
		// mutable borrow yields `&mut`, an immutable one `&`. This is where the lazy
		// deep-mut rule lives — under a `&mut {a: {x}}` receiver, `p.a` reads `&mut {x}`.
		lt := recvLt
		if lt == nil {
			// An owned receiver yields the field's owned value, not a borrow, so a
			// field read can be moved out of it. `pair.a` off an owned `pair` is the
			// owned field `{x}` and flows into an owned binding, argument, return, or
			// store as a move that consumes `pair.a`. The move engine keys the consume
			// on the field's place (PR 7). A mutable receiver yields an owned-mutable
			// field and an immutable one the bare field.
			return soltype.NewRef(recvMut, nil, fieldType.(soltype.RefInner))
		}
		if recvMut {
			// Mutable read: keep the concrete field shape as the borrow's inner, the
			// role the eager form's owned-mut cell played, so a chained read `p.a.b.c`
			// sees the nested structure and the borrow survives the co-occurrence pass.
			// Routing it through `fieldVar` would pin the var invariant in both
			// polarities and widen it into a union that peels the borrow.
			return &soltype.RefType{Mut: true, Lt: lt, Inner: fieldType.(soltype.RefInner)}
		}
		// Immutable read: route through the fresh field-read var (PR 4).
		return &soltype.RefType{Mut: false, Lt: lt, Inner: fieldVar}
	default:
		return fieldVar
	}
}

// resolveIndexPath resolves `obj[index]`. A namespace object is indexed by a
// constant string key — Foo["bar"] is the bracket form of Foo.bar — while a
// dynamic key (Foo[k]) is rejected. A value object indexed by a constant string
// key is the bracket form of property access — obj["foo-bar"] reads the same
// property as obj.foo would, and lets the source name a property whose key is not
// a valid identifier. A dynamic key over a value (array element / index-signature
// read) needs Array and index types from M7, so it stays unsupported here.
func (c *checker) resolveIndexPath(scope *Scope, lvl int, e *ast.IndexExpr, objPos bool) pathResult {
	if e.OptChain {
		c.reportUnsupportedFeature(e, "OptionalChain")
		return pathResult{err: true}
	}
	obj := c.resolvePath(scope, lvl, e.Object, true)
	if obj.err {
		return pathResult{err: true}
	}
	if obj.ns != nil {
		name, ok := constStringKey(e.Index)
		if !ok {
			c.report(&DynamicNamespaceIndexError{Index: e, NS: obj.ns})
			return pathResult{err: true}
		}
		return c.resolveNamespaceMember(lvl, e, obj.ns, name)
	}
	if name, ok := constStringKey(e.Index); ok {
		if !objPos {
			c.recordMemberUse(e)
		}
		return c.valueProp(lvl, e, e.Index, name, obj.value)
	}
	// A dynamic key over a value (array element / index-signature read) needs Array
	// and index types, which land in M7; until then it is outside the supported
	// subset.
	c.reportUnsupported(e)
	return pathResult{err: true}
}

// resolveNamespaceMember looks name up in ns directly and non-lexically — a
// namespace member resolution reads the namespace's OWN maps, never walking a
// parent scope (unlike Scope.GetValue/GetType/GetNamespace). A nested namespace is
// returned as a namespace so a longer chain keeps walking; a value member is
// instantiated and recorded against node; an absent name is an
// UnknownNamespaceMemberError. node is the member/index expression, for blame and
// the Info record.
func (c *checker) resolveNamespaceMember(lvl int, node ast.Expr, ns *Namespace, name string) pathResult {
	if nested, ok := ns.Nested[name]; ok {
		return pathResult{ns: nested}
	}
	if b, ok := ns.Values[name]; ok && len(b.Schemes) > 0 {
		t := c.bindingValue(lvl, b)
		c.recordType(node, t)
		return pathResult{value: t}
	}
	c.report(&UnknownNamespaceMemberError{Node: node, NS: ns, Name: name})
	return pathResult{err: true}
}

// constStringKey reads a statically-constant string index key. Only a string
// literal qualifies — Foo["bar"]; a numeric, identifier, or otherwise dynamic key
// returns false so the caller can reject it.
func constStringKey(e ast.Expr) (string, bool) {
	if lit, ok := e.(*ast.LiteralExpr); ok {
		if s, ok := lit.Lit.(*ast.StrLit); ok {
			return s.Value, true
		}
	}
	return "", false
}

// objKeyName reads the static field name of an object-literal key. Object field
// names are strings, so an identifier label, a string-literal key, or a numeric
// key all map to a field. A numeric key is coerced to its string form the way
// JavaScript does, so {0: v} names the field "0". A computed key ({[k]: v}) carries
// no static name and returns false so the caller can raise a structured error.
// Full index-signature support rides M9.
func objKeyName(k ast.ObjKey) (string, bool) {
	switch k := k.(type) {
	case *ast.IdentExpr:
		return k.Name, true
	case *ast.StrLit:
		return k.Value, true
	case *ast.NumLit:
		return strconv.FormatFloat(k.Value, 'f', -1, 64), true
	default:
		return "", false
	}
}

// identPatName reads the name of an IdentPat. M2 binds IdentPat-only patterns
// (mirroring M1's IdentPat-only FuncParam); the comma-ok form lets callers raise
// a structured error for the destructuring patterns deferred to M4.
func identPatName(pat ast.Pat) (string, bool) {
	if ip, ok := pat.(*ast.IdentPat); ok {
		return ip.Name, true
	}
	return "", false
}

// inferAwait types `await e`. The argument is constrained `<: Promise<U>` for a
// fresh U, and U is the await's value type — exactly the rule M3's milestone
// pins ("`await e` requires `e <: Promise<U>` for some `U` and produces `U`",
// 01-milestones.md §M3). No auto-flatten: U may itself be a Promise, so
// `await Promise<Promise<T>>` yields `Promise<T>` (Awaited<T> is M9). `await`
// outside an `async` function is rejected by the WALK (this function), not the
// type rule — the argument is still walked so its own errors surface, and the
// await contributes a `never` placeholder so a downstream consumer doesn't see a
// stray inference variable that would never be solved.
func (c *checker) inferAwait(scope *Scope, lvl int, e *ast.AwaitExpr) soltype.Type {
	if c.fn == nil || !c.fn.async {
		c.inferExpr(scope, lvl, e.Arg) // surface argument-side errors anyway
		// When the await sits in a (non-async) function, point Related() at that
		// function — it is the one to mark `async`. At module top-level there is no
		// enclosing function, so EnclosingFn stays nil and Related() is empty.
		var enclosing ast.Node
		if c.fn != nil {
			enclosing = c.fn.node
		}
		// report returns the ErrorType recovery placeholder (PR8), so the rejected
		// await never cascades a downstream `<unknown> <: T` on top of this error.
		t := c.report(&AwaitOutsideAsyncError{Await: e, EnclosingFn: enclosing})
		c.recordType(e, t)
		return t
	}
	arg := c.inferExpr(scope, lvl, e.Arg)
	res := c.freshAt(lvl)
	c.recordProv(res, e, AwaitResult)
	// Synthesize the Promise<U> requirement at this call site. It isn't given its
	// own provenance — the operand the user sees blame on is the awaited expression
	// (`e.Arg`), already recorded by inferExpr; the synthesized Promise wrapper is
	// internal scaffolding for the constraint, not a user-authored type.
	//
	// PR8: a failed argument is the ErrorType recovery placeholder, which absorbs in
	// constrain, so `<unknown> <: Promise<U>` no longer cascades a spurious second
	// diagnostic — res then stays unbound and coalesces to `never`, the right
	// recovery for awaiting something broken. The M2-era isRecoveryPlaceholder guard
	// this site used is gone.
	c.constrain(e, arg, &soltype.PromiseType{Inner: res})
	c.recordType(e, res)
	return res
}

// inferIfElse types `if cond { cons } else { alt }`. The condition is
// constrained `<: boolean`; each branch is typed (an empty / missing else
// contributes Void); the result is a fresh join var with each NON-DIVERGING
// branch as a lower bound, so the result coalesces to the union of the branches
// that can actually produce a value.
//
// Diverging branches contribute `never`: a branch that always exits before its
// tail (today a trailing `return`; `throw` / `-> never` calls join this set once
// they land — see blockDiverges) can never be the path that yields the if's
// value, so it drops out of the branch union entirely rather than leaking its
// operand. `val x = if c { return 1 } else { "y" }` is `"y"`, not `1 | "y"`, and
// when both branches diverge the if's value coalesces to `never`.
//
// Block return-point interaction: any ReturnStmt inside either branch is still
// collected on the enclosing function's funcCtx by inferStmt — independent of the
// if's value contribution — so `fn f(c) { val x = if c { return X } else { Y } }`
// flows X into the function's return type (via joinReturnPoints) AND Y into the
// if's value, which binds x. The two roles are orthogonal: X is a return point,
// but not part of the if-EXPRESSION's value.
func (c *checker) inferIfElse(scope *Scope, lvl int, e *ast.IfElseExpr) soltype.Type {
	cond := c.inferExpr(scope, lvl, e.Cond)
	// The synthesized `boolean` requirement is intentionally NOT recorded in Prov
	// (so a `string <: boolean` failure has no "expected boolean here" related
	// span): it is a language rule, not a user-authored annotation, so there is no
	// source node to anchor it to — recording it against e.Cond would only make
	// Related() echo Span(). This matches inferAwait's synthesized Promise and
	// inferMember's synthesized record requirement, both deliberately unrecorded.
	//
	// PR8: a failed condition is the ErrorType recovery placeholder, which absorbs
	// in constrain, so `<unknown> <: boolean` no longer cascades a spurious second
	// diagnostic — the M2-era isRecoveryPlaceholder guard this site used is gone.
	c.constrain(e.Cond, cond, &soltype.PrimType{Prim: soltype.BoolPrim})
	consT, consDiverges := c.inferBlock(scope.Child(), lvl, &e.Cons)
	var altT soltype.Type = &soltype.Void{}
	altDiverges := false
	if e.Alt != nil {
		altT, altDiverges = c.inferBlockOrExpr(scope, lvl, e.Alt)
	}
	res := c.freshAt(lvl)
	c.recordProv(res, e, IfElseBranch)
	// A diverging branch contributes `never` to the value — i.e. nothing to the
	// branch union — so skip its lower-bound constraint. inferBlock still walked it
	// above (reporting branch-local errors and collecting its `return` as a function
	// return point); only its block-tail VALUE is dropped here. When both branches
	// diverge, res keeps no lower bounds and coalesces to `never`.
	if !consDiverges {
		c.constrain(e, consT, res)
	}
	if !altDiverges {
		c.constrain(e, altT, res)
	}
	c.recordType(e, res)
	return res
}

// inferMatch types a `match` expression. The scrutinee is inferred once. Each arm
// then types its pattern against the scrutinee in a child scope carrying the arm's
// bindings, the same E1 bindPattern path `val` destructuring uses. An optional `if`
// guard is typed as a boolean, then the arm body is inferred. Every non-diverging
// arm body is constrained into one fresh branch-join var, exactly as inferIfElse
// joins its two branches. A diverging arm contributes `never`, so when every arm
// diverges the result coalesces to `never`.
//
// Exhaustiveness is checked from structural exactness by checkMatchExhaustive.
func (c *checker) inferMatch(scope *Scope, lvl int, e *ast.MatchExpr) soltype.Type {
	scrutinee := c.inferExpr(scope, lvl, e.Target)
	// Snapshot the scrutinee for the exhaustiveness check before any arm binds. A
	// literal pattern adds its literal as a lower bound, which would otherwise leak
	// a phantom member into the coalesced union read after the arm loop.
	matchShape := scrutinee
	if _, isVar := soltype.CarrierOf(scrutinee).(*soltype.TypeVarType); isVar {
		matchShape = coalesce(scrutinee, soltype.Positive)
	}
	res := c.freshAt(lvl)
	c.recordProv(res, e, MatchBranch)
	for _, arm := range e.Cases {
		// Each arm binds its pattern's leaves in a fresh child scope so a name bound
		// by one arm is invisible to the next. bindPattern peels the scrutinee's borrow
		// for the member-lookup requirements but reapplies the binding mode to each
		// leaf, so a leaf of a `&mut` scrutinee binds as a `&mut` borrow, just as `val`
		// destructuring does. A missing field or wrong tuple arity surfaces here too.
		armScope := scope.Child()
		c.bindPattern(armScope, lvl, arm.Pattern, scrutinee, nil)
		if arm.Guard != nil {
			// A guard is an ordinary boolean condition over the arm's bindings. As in
			// inferIfElse, the synthesized boolean requirement is left out of Prov. It
			// is a language rule, not a user annotation, so there is no source node to
			// anchor a related span to.
			guard := c.inferExpr(armScope, lvl, arm.Guard)
			c.constrain(arm.Guard, guard, &soltype.PrimType{Prim: soltype.BoolPrim})
		}
		bodyT, diverges := c.inferBlockOrExpr(armScope, lvl, &arm.Body)
		if !diverges {
			c.constrain(e, bodyT, res)
		}
	}
	c.checkMatchExhaustive(e, matchShape)
	c.recordType(e, res)
	return res
}

// checkMatchExhaustive reports a NonExhaustiveMatchError when no arm covers every
// value the coalesced scrutinee can take, dispatching on its union or object/tuple shape.
func (c *checker) checkMatchExhaustive(e *ast.MatchExpr, scrutinee soltype.Type) {
	carrier := soltype.CarrierOf(scrutinee)
	if u, ok := carrier.(*soltype.UnionType); ok {
		if !c.unionMatchExhaustive(e, u) {
			c.report(&NonExhaustiveMatchError{Match: e})
		}
		return
	}
	inexact, isStructural := structuralInexact(carrier)
	if !isStructural {
		return
	}
	for _, arm := range e.Cases {
		// A guarded arm can always fail its guard, so it never makes a match
		// exhaustive. Only an unguarded covering arm does.
		if arm.Guard == nil && armCoversShape(arm.Pattern, inexact) {
			return
		}
	}
	c.report(&NonExhaustiveMatchError{Match: e})
}

// unionMatchExhaustive reports whether the unguarded arms cover a union scrutinee.
// An inexact union needs a catch-all. An exact one needs every member covered.
func (c *checker) unionMatchExhaustive(e *ast.MatchExpr, u *soltype.UnionType) bool {
	for _, arm := range e.Cases {
		if arm.Guard == nil && isCatchAll(arm.Pattern) {
			return true
		}
	}
	if u.Inexact {
		return false
	}
	for _, member := range u.Types {
		if !c.unionMemberCovered(member, e.Cases) {
			return false
		}
	}
	return true
}

// unionMemberCovered reports whether some unguarded arm matches one union member via
// a catch-all or an equal literal pattern. Other shapes read uncovered, so coverage
// is sound but only complete for literal members. Nominal members are covered
// instead by M5's enum leg through constructor patterns; coverage of plain
// structural-object members by object patterns is not yet handled.
func (c *checker) unionMemberCovered(member soltype.Type, arms []*ast.MatchCase) bool {
	memberLit, memberIsLit := member.(*soltype.LitType)
	for _, arm := range arms {
		if arm.Guard != nil {
			continue
		}
		if isCatchAll(arm.Pattern) {
			return true
		}
		armLit, ok := arm.Pattern.(*ast.LitPat)
		if !ok || !memberIsLit {
			continue
		}
		if lt, ok := c.litTypeOf(armLit.Lit); ok && memberLit.Equal(lt) {
			return true
		}
	}
	return false
}

// structuralInexact returns the Inexact flag of an object or tuple type and whether
// the type is one of those structural forms at all. M4's match exhaustiveness reads
// nothing else off the scrutinee.
func structuralInexact(t soltype.Type) (inexact bool, ok bool) {
	switch t := t.(type) {
	case *soltype.ObjectType:
		return t.Inexact, true
	case *soltype.TupleType:
		return t.Inexact, true
	default:
		return false, false
	}
}

// armCoversShape reports whether an unguarded arm makes the match exhaustive for a
// scrutinee of the given exactness. A wildcard or identifier pattern binds
// unconditionally, so it covers any value. An object or tuple pattern covers an
// exact scrutinee only when it is irrefutable. Every sub-pattern must itself be
// irrefutable, so a nested literal such as `{x: 1}` does not count. Such a pattern
// never covers an inexact scrutinee. An inexact scrutinee's open tail may hold
// values the pattern cannot see, so it still needs a true catch-all. A literal
// pattern is refutable and never covers.
func armCoversShape(p ast.Pat, inexact bool) bool {
	if isCatchAll(p) {
		return true
	}
	switch p.(type) {
	case *ast.ObjectPat, *ast.TuplePat:
		return !inexact && irrefutablePat(p)
	default:
		return false
	}
}

// isCatchAll reports whether a pattern matches every value unconditionally, so an
// unguarded arm with it alone makes any match exhaustive. A wildcard or
// identifier binds without testing the value. Every other pattern is refutable.
func isCatchAll(p ast.Pat) bool {
	switch p.(type) {
	case *ast.WildcardPat, *ast.IdentPat:
		return true
	default:
		return false
	}
}

// irrefutablePat reports whether a pattern matches every value of a compatible
// type, so it can never fail at runtime. A wildcard or identifier binds
// unconditionally. An object or tuple pattern is irrefutable only when all of its
// sub-patterns are. A literal pattern can fail, and the constructor patterns deferred
// to M5 are refutable, so both return false.
func irrefutablePat(p ast.Pat) bool {
	switch p := p.(type) {
	case *ast.WildcardPat, *ast.IdentPat:
		return true
	case *ast.TuplePat:
		for _, e := range p.Elems {
			if !irrefutablePat(e) {
				return false
			}
		}
		return true
	case *ast.ObjectPat:
		for _, elem := range p.Elems {
			switch e := elem.(type) {
			case *ast.ObjShorthandPat:
				// A shorthand binds an identifier, which always matches.
			case *ast.ObjKeyValuePat:
				if !irrefutablePat(e.Value) {
					return false
				}
			default:
				// ObjRestPat is M9; treat anything else as refutable.
				return false
			}
		}
		return true
	default:
		return false
	}
}

// blockDiverges reports whether a block always transfers control out before
// reaching its tail — its last statement diverges — so the block completes no
// value and contributes `never` to any value-position consumer. A diverging
// block's `return` is still a function return point — inferStmt collects it
// independently — this governs only the block's VALUE contribution.
//
// This trio (blockDiverges / stmtDiverges / exprDiverges / blockOrExprDiverges)
// mirrors the old checker's blockAlwaysExits / stmtAlwaysExits / exprAlwaysExits
// (internal/checker/infer_func.go) so the two analyses extend in lockstep: when a
// new diverging form is recognised in one, add the matching arm in the other.
func blockDiverges(b *ast.Block) bool {
	if b == nil || len(b.Stmts) == 0 {
		return false
	}
	return stmtDiverges(b.Stmts[len(b.Stmts)-1])
}

func stmtDiverges(s ast.Stmt) bool {
	switch s := s.(type) {
	case *ast.ReturnStmt:
		return true
	case *ast.ExprStmt:
		return exprDiverges(s.Expr)
	default:
		return false
	}
}

// exprDiverges mirrors the checker's exprAlwaysExits. It is a structural AND-fold
// over specific child positions — an `if`/`else` diverges only if BOTH arms do, a
// `match` only if EVERY arm does, a block only on its LAST statement — not a walk
// that visits every node, so the AST visitor is deliberately not used here: a
// visitor would flatten the tree and lose the which-child/AND structure, and force
// suppressing descent into the parts that must be ignored (the `if` condition, call
// arguments). The recursive switch is the right shape; the visitor is for the dual
// problem of collecting every `return` regardless of position.
//
// MatchExpr is walked by inferMatch in M4 E2, so its arm reflects real source. A
// match diverges when every arm body does. ThrowExpr and DoExpr are not yet walked
// by the solver, which reports them unsupported, so those arms are unreachable from
// real source today. They are kept in place so a form's divergence is already
// recognised the moment its inferExpr case lands, matching the checker rather than
// re-discovering divergence later. The checker's
// CallExpr `-> never` arm is deliberately omitted: the solver represents a call's
// result as an unresolved variable mid-walk (bounds lists, not a single prunable
// Instance), so "this call returns never" is a coalescing-time fact — revisit when
// `-> never` calls reach the solver.
func exprDiverges(e ast.Expr) bool {
	switch e := e.(type) {
	case *ast.ThrowExpr:
		return true
	case *ast.IfElseExpr:
		// Without an `else`, fall-through is reachable when the condition is false.
		if e.Alt == nil {
			return false
		}
		return blockDiverges(&e.Cons) && blockOrExprDiverges(e.Alt)
	case *ast.MatchExpr:
		// A match diverges only if EVERY arm does. Exhaustiveness is checked
		// elsewhere; a non-exhaustive match conservatively does not diverge (the
		// safe default — a false negative just keeps a value where there is none).
		if len(e.Cases) == 0 {
			return false
		}
		for _, arm := range e.Cases {
			if !blockOrExprDiverges(&arm.Body) {
				return false
			}
		}
		return true
	case *ast.DoExpr:
		return blockDiverges(&e.Body)
	default:
		return false
	}
}

func blockOrExprDiverges(b *ast.BlockOrExpr) bool {
	switch {
	case b.Block != nil:
		return blockDiverges(b.Block)
	case b.Expr != nil:
		return exprDiverges(b.Expr)
	default:
		return false
	}
}

// inferBlockOrExpr types an `else` arm: either a block (`else { ... }`) or a
// single expression (`else if ...` chains, which the parser desugars into Alt =
// expr). It returns the arm's value together with whether the arm DIVERGES (so
// inferIfElse drops it from the branch union, exactly as it drops a diverging
// block branch). A nil-block-and-nil-expr alt is treated as a non-diverging Void
// (the only honest recovery for a malformed AST shape that shouldn't arise from
// the real parser).
//
// Scoping: a BLOCK runs in a child scope (it may declare body-local val/var), an
// EXPRESSION runs in the enclosing scope. This is not an asymmetry — it is the
// walk's uniform rule (only blocks introduce a scope; sub-expressions are always
// typed in the current scope, as inferCall/inferTuple/inferMember do, since an
// expression never binds a name). An `else if`'s nested IfElseExpr childs its own
// cons/alt in turn, so each block still gets exactly one scope.
func (c *checker) inferBlockOrExpr(scope *Scope, lvl int, b *ast.BlockOrExpr) (soltype.Type, bool) {
	switch {
	case b.Block != nil:
		return c.inferBlock(scope.Child(), lvl, b.Block)
	case b.Expr != nil:
		return c.inferExpr(scope, lvl, b.Expr), exprDiverges(b.Expr)
	default:
		return &soltype.Void{}, false
	}
}
