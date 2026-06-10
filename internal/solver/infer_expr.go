package solver

import (
	"fmt"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
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
// A namespace name in value position can only fail in M2: there is no legal
// namespace-member position yet (MemberExpr is value-only and there is no
// IndexExpr), so raising NamespaceUsedAsValueError on any namespace name is
// correct here. M4 moves that error to the value-position consumer once
// qualified Foo.bar access lands.
func (c *checker) inferIdent(scope *Scope, lvl int, e *ast.IdentExpr) soltype.Type {
	// Any binding still in scope has at least one scheme: inferComponent pre-binds
	// each group member to a fresh MonoScheme, and on failure deletes the binding
	// (scope.removeValue) rather than leaving it with an empty Schemes slice. So the
	// len > 0 check below should never fail in practice — but Schemes is a slice, not
	// a guaranteed-non-empty field, so we guard it anyway: a malformed empty binding
	// degrades to an unknown-identifier error here instead of panicking on Schemes[0].
	//
	// An overloaded name in VALUE position (PR6) — `val g = f`, or `f` passed as an
	// argument — is the intersection of its arms (the one scoped lattice exception;
	// see overloadIntersection and constrain's IntersectionType arm). A direct call
	// `f(x)` never reaches here: inferCall intercepts the overloaded callee and routes
	// it through resolveOverload before typing the callee as a value.
	if b, ok := scope.GetValue(e.Name); ok && len(b.Schemes) > 0 {
		var t soltype.Type
		if b.IsOverloaded() {
			t = c.overloadIntersection(lvl, b)
		} else {
			t = c.instantiate(b.Schemes[0], lvl)
		}
		c.recordType(e, t)
		return t
	}
	if _, ok := scope.GetNamespace(e.Name); ok {
		return c.report(&NamespaceUsedAsValueError{Ident: e})
	}
	return c.report(&UnknownIdentifierError{Ident: e})
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
// builds the n-ary soltype.FuncType. When the signature carries a return
// annotation the inferred body type is constrained against it and the annotated
// type becomes the function's return type; otherwise the body type is the
// return type directly. A bodyless (declare/ambient) function adopts its return
// annotation without constraining anything. node supplies the span stamped onto
// a return-annotation constraint failure.
func (c *checker) inferFunc(scope *Scope, lvl int, sig ast.FuncSig, body *ast.Block, node ast.Node) *soltype.FuncType {
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
	for i, p := range sig.Params {
		pt := c.paramType(p, lvl)
		name, ok := identPatName(p.Pattern)
		var sources []provenance.Provenance
		if !ok {
			// Destructuring params (TuplePat/ObjectPat) need record/tuple types —
			// they arrive in M4. M2 binds IdentPat only. A non-nil pattern blames
			// itself (its own narrower span); a pattern-less param (not reachable
			// from the real parser) blames the enclosing function, since Param.Span()
			// dereferences the nil pattern — honoring M2's "never a panic" guarantee.
			if p.Pattern != nil {
				c.reportUnsupported(p.Pattern)
			} else {
				c.reportUnsupported(node)
			}
			name = fmt.Sprintf("arg%d", i)
			// Leave sources empty: the synthetic arg%d name is not a real place, so
			// there is nothing useful for go-to-definition or a related span to point at.
		} else {
			// The param's IdentPat IS its definition site, so record it as the binding's
			// source — symmetric to a val/var/fn binding (inferVarDecl/module.go). This
			// lets CannotAssignToImmutableError point "declared immutable here" at the
			// parameter (see bindingDecl). p.Pattern is an ast.Node (*ast.Param is not).
			sources = []provenance.Provenance{&ast.NodeProvenance{Node: p.Pattern}}
			if p.TypeAnn == nil {
				// An un-annotated param's type is the fresh var minted here, so a
				// param-type mismatch blames the param. An annotated param's blame
				// instead rides on its annotation, recorded by resolveTypeAnn.
				c.recordProv(pt, p.Pattern, ParamBinding)
			}
		}
		// A parameter binding never generalizes — its var is fixed for the body — so
		// it is a MonoScheme; instantiate returns pt unchanged at every use.
		fnScope.defineValue(name, ValueBinding{Schemes: []TypeScheme{monoScheme(pt)}, Sources: sources})
		// An `x?` parameter (parsed onto ast.Param.Optional) lowers the function's
		// `required` count without dropping the param — carried onto the soltype so
		// the accept-set rule and the printer (x?: T) see it. KNOWN GAP (M6): the
		// in-body binding keeps the param's declared type (pt), NOT widened to
		// `pt | undefined`, so a body that reads an omitted optional sees it at the
		// narrower type. Widening needs undefined/unions (M6); M3 has neither.
		params[i] = &soltype.FuncParam{Pattern: &soltype.IdentPat{Name: name}, Type: pt, Optional: p.Optional}
	}

	var ret soltype.Type = &soltype.Void{}
	hasBody := body != nil
	var collected []soltype.Type
	if hasBody {
		// PR3: open a fresh function context so every ReturnStmt encountered while
		// walking the body lands in our own returns list (a nested fn inside this
		// body opens its own context, so its returns never leak out here).
		saved := c.pushFuncCtx(sig.Async, node)
		// The function body wants the TAIL value for the return-point join (a
		// diverging `{ return 5 }` body still returns 5, collected into c.fn.returns
		// below), so the divergence flag is irrelevant here — only value-position
		// callers (inferIfElse) consult it.
		ret, _ = c.inferBlock(fnScope, lvl, body)
		collected = c.popFuncCtx(saved)
	}
	// PR3 — block return-point join. M2 only used the block tail and dropped non-
	// tail returns; M3 joins EVERY ReturnStmt (collected above) with the tail. The
	// join is a fresh var with each return point as a lower bound: when there is
	// no explicit return, ret stays the tail unchanged (preserving M2's monomorphic
	// renders); when there is at least one, all paths flow through one variable
	// whose coalesced positive face is their union.
	//
	// Fast path: a single return that IS the block tail (`fn f() { … return X }`,
	// where inferStmt returns the return's value as the tail too — same pointer).
	// ret already IS that return, so minting a join var would add a pointless
	// indirection plus two redundant constraints the coalescer must later dedup.
	if len(collected) > 0 && !(len(collected) == 1 && collected[0] == ret) {
		joinVar := c.freshAt(lvl)
		c.recordProv(joinVar, node, ReturnJoin)
		// Source-order constraint: collected returns first (in source order), then
		// the block tail. When the tail IS one of the collected returns, the
		// duplicate bound is dedup'd at render time — the rendered union reflects
		// source order, not constraint order.
		for _, rt := range collected {
			c.constrain(node, rt, joinVar)
		}
		c.constrain(node, ret, joinVar)
		ret = joinVar
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
				c.constrain(node, ret, annT) // body <: declared return
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
	// the missing slots with fresh vars, which impose no constraint on absent args.
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
	}
	c.recordType(e, res)
	return res
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

// inferAssign types a reassignment `target = rhs` — the only BinaryExpr form the
// M3 walk handles. The RHS is typed first (so its own errors surface regardless of
// the target's validity), then the target is resolved and gated:
//
//   - The target must be a place: an IdentExpr resolving to a value binding. A
//     literal, call, member, or any other non-place LHS is an
//     InvalidAssignmentTargetError (member targets `obj.x = …` need record types,
//     M4). An ident that resolves to no binding is an UnknownIdentifierError.
//   - The binding must be reassignable: only a `var` (Kind == VarKind) is. A `val`,
//     function, parameter, or prelude binding is a CannotAssignToImmutableError.
//
// On success the RHS is constrained `<: target` (the binding's coalesced type),
// the new-solver form of the old checker's `Unify(rightType, leftType)`: the value
// being stored must be a subtype of the slot. Reassigning an annotated `var a:
// number = 5` with `a = 6` checks; an un-annotated `var a = 5` keeps the literal
// type `5`, so `a = 6` does NOT check until `var` literal widening (M4).
//
// The assignment EXPRESSION evaluates to the value just stored, so its type is the
// target binding's slot type — `val b = (a = 6)` for `var a: number` yields
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
	rhs := c.inferExpr(scope, lvl, e.Right)
	// Record void on e up front as the recovery type: every error path below returns
	// voidT without recording a type, so this guarantees the node is typed on failure.
	// The success path overwrites it with the stored value's type (see end of function).
	c.recordType(e, voidT)

	target, ok := e.Left.(*ast.IdentExpr)
	if !ok {
		// A member/index target (obj.x = …, xs[i] = …) is a structurally VALID place
		// whose type rule needs record/array types — deferred to M4 — so report it as
		// an unsupported feature, distinct from a fundamentally invalid target like
		// `5 = x` or `f() = x`, which is an InvalidAssignmentTargetError.
		switch e.Left.(type) {
		case *ast.MemberExpr, *ast.IndexExpr:
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
		if _, isNS := scope.GetNamespace(target.Name); isNS {
			c.report(&NamespaceUsedAsValueError{Ident: target})
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
	// The RHS must be a subtype of the target binding's type. Use the binding's
	// COALESCED type (schemeType — what Info records and the printer renders), not a
	// fresh instantiation: instantiating a generalized binding yields a var carrying
	// only its LOWER bounds (the read/covariant face), so `a = "x"` for `var a:
	// number` would merely add another lower bound and wrongly succeed. The coalesced
	// type is the concrete slot type — `number` for an annotated var, the literal `5`
	// for an un-annotated one (so `a = 6` ⇒ `6 <: 5` does NOT check until M4 `var`
	// literal widening).
	//
	// freshenAll copies the coalesced type so constraining the RHS cannot mutate
	// type-parameter vars the coalesced form still shares with the binding
	// (coalesceScheme retains them by pointer): without the copy, reassigning a
	// polymorphic var would poison it for every later use. A var-free coalesced type
	// (the common annotated/literal case) freshens to itself.
	//
	// A probe can't do this: Discard would also roll back the constraint's real errors
	// and the RHS's bound, while Commit would keep the binding poisoning — we need to
	// suppress one side effect, not the whole trial. freshenAll isolates just the var.
	//
	// b.Schemes[0]: a reassignable binding is always single-scheme — overload sets
	// come only from FuncDecls, whose Kind is never VarKind, so they are rejected by
	// the `b.Kind != ast.VarKind` gate above before reaching here.
	targetT := c.freshenAll(schemeType(b.Schemes[0]), lvl)
	c.recordType(target, targetT)
	c.constrainAssign(e, rhs, targetT)
	// The assignment evaluates to the value just stored — the SAME read face as
	// reading the target (inferIdent), so `val b = (a = 6)` ⇒ `b: number`. Use
	// instantiate (the read face), NOT the coalesced write-face targetT: targetT is a
	// display type that may be a Union/Intersection node, and re-injecting it into the
	// constraint graph here would later crash the coalescer when this value flows on
	// (e.g. as a function-body tail). This overwrites the `void` recorded for e above,
	// which now serves only as the error-path recovery value.
	valueT := c.instantiate(b.Schemes[0], lvl)
	c.recordType(e, valueT)
	return valueT
}

// constrainAssign asserts `rhs <: targetT` for a reassignment. For a UNION target it
// applies the union-RHS rule — X <: (A | B) iff X <: A or X <: B — by trying each
// member speculatively under a probe, committing the first that holds. constrain
// itself has no UnionType-RHS rule until M6, so without this a legal assignment of a
// union member (`var a = if c { 1 } else { 2 }; a = 1`) would be wrongly rejected.
// A non-union target takes the ordinary single-constraint path.
//
// KNOWN GAP (M6): when `rhs` is (or contains) an inference variable, committing the
// first matching member over-narrows it. `var a = 1 | 2; a = x` for an un-annotated
// param `x` commits `x <: 1` and infers `x: 1` instead of the sound `x: 1 | 2`,
// which can wrongly reject a later use of `x` that needs `2`. This is INCOMPLETE,
// not unsound — the committed bound is always stronger than required, so no invalid
// program is accepted. It is not fixable here: the obvious "don't commit, fall
// through to constrain(rhs, targetT)" injects the COALESCED union node into rhs's
// bound list and panics the coalescer (coalesced output must never re-enter the
// graph). A correct fix needs first-class union subtyping with inference variables
// — M6's deferred union/intersection rules in constrain. Pinned by
// TestInferAssignUnionTargetVarRHSOverNarrows.
func (c *checker) constrainAssign(n ast.Node, rhs, targetT soltype.Type) {
	union, ok := targetT.(*soltype.UnionType)
	if !ok {
		c.constrain(n, rhs, targetT)
		return
	}
	for _, member := range union.Types {
		p := c.openProbe()
		errs := c.ctx.Constrain(rhs, member)
		c.closeProbe(p, len(errs) == 0) // commit the first member that holds; else roll back
		if len(errs) == 0 {
			return
		}
	}
	// No member matched: report once against the whole union (CannotConstrainError),
	// blaming the assignment.
	c.constrain(n, rhs, targetT)
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
// A spread element ([...xs]) is an ArraySpreadExpr, which is not in the M2 walk,
// so inferExpr reports it as unsupported and contributes a never placeholder in
// its slot — the tuple is still built (error already accumulated), never a panic.
func (c *checker) inferTuple(scope *Scope, lvl int, e *ast.TupleExpr) soltype.Type {
	elems := make([]soltype.Type, len(e.Elems))
	for i, el := range e.Elems {
		elems[i] = c.inferExpr(scope, lvl, el)
	}
	t := &soltype.TupleType{Elems: elems}
	c.recordType(e, t)
	c.recordProv(t, e, TupleElem)
	return t
}

// inferObject types an object literal as a soltype.RecordType. M2 covers the
// basic case only: a `name: value` property with a static (identifier or string)
// key. The forms it does not cover each report an UnsupportedNodeError and are
// skipped rather than panicking (the deeper object system is M4):
//   - spreads ({...o}) and method/constructor elements,
//   - computed ({[k]: v}) and numeric ({0: v}) keys,
//   - shorthand ({x}, i.e. a property with no value).
//
// Usage-inference depth (e.g. inferring an open record from how a value is used)
// is explicitly M4; M2 builds the closed record the literal spells out.
//
// Duplicate keys follow JavaScript semantics: the last value wins, keeping the
// field at its first position ({a: 1, b: 2, a: 3} ⇒ {a: 3, b: 2}). This keeps
// field names unique, the invariant RecordType.Field / equalType rely on.
func (c *checker) inferObject(scope *Scope, lvl int, e *ast.ObjectExpr) soltype.Type {
	fields := make([]*soltype.RecordField, 0, len(e.Elems))
	pos := make(map[string]int, len(e.Elems)) // field name → index in fields, for last-wins dedup
	for _, elem := range e.Elems {
		prop, ok := elem.(*ast.PropertyExpr)
		if !ok {
			// ObjSpreadExpr, CallableExpr (method), ConstructorExpr — all M4.
			c.reportUnsupported(elem)
			continue
		}
		if prop.Value == nil {
			// Shorthand ({x}) needs the ident's binding folded in as the value — M4.
			c.reportUnsupported(prop)
			continue
		}
		name, ok := objKeyName(prop.Name)
		if !ok {
			// Computed/numeric keys carry no static field name — M4. Blame the key
			// itself (its own narrower span), not the whole property.
			c.reportUnsupported(prop.Name)
			continue
		}
		ft := c.inferExpr(scope, lvl, prop.Value)
		if i, dup := pos[name]; dup {
			fields[i] = &soltype.RecordField{Name: name, Type: ft} // last value wins, first position kept
			continue
		}
		pos[name] = len(fields)
		fields = append(fields, &soltype.RecordField{Name: name, Type: ft})
	}
	t := &soltype.RecordType{Fields: fields}
	c.recordType(e, t)
	c.recordProv(t, e, ObjectField)
	return t
}

// inferMember types a field read (recv.prop). It types the receiver, allocates a
// fresh result var, and constrains recv <: {prop: res} — the basic form from the
// plan's §3.2 table. The record <: record arm of constrain lowers res from the
// receiver's matching field (so res coalesces to that field's type); a receiver
// missing the field surfaces as a MissingPropertyError stamped with the member's
// span. Optional chaining (recv?.prop) needs union/undefined handling and is M6.
func (c *checker) inferMember(scope *Scope, lvl int, e *ast.MemberExpr) soltype.Type {
	if e.OptChain {
		// Optional chaining (recv?.prop) is wholesale unsupported in M2; report it
		// up front and do NOT descend into the receiver, so a single diagnostic
		// stands for the construct instead of cascading the receiver's errors. The
		// MemberExpr kind is supported — it is the optional-chain feature that is
		// not — so this is an UnsupportedFeatureError blaming the member.
		return c.reportUnsupportedFeature(e, "OptionalChain")
	}
	recv := c.inferExpr(scope, lvl, e.Object)
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
		return t
	}
	res := c.freshAt(lvl)
	// Record the fresh result var against the .prop IDENTIFIER (not the whole
	// MemberExpr), so a missing-property read blames the property (.foo), not the
	// receiver. The member-requirement record {prop: res} is deliberately NOT
	// recorded — MissingPropertyError blames this inner res var, so the record
	// would be a dead entry (§3.3).
	c.recordProv(res, e.Prop, MemberAccess)
	c.constrain(e, recv, &soltype.RecordType{Fields: []*soltype.RecordField{{Name: e.Prop.Name, Type: res}}})
	c.recordType(e, res)
	return res
}

// objKeyName reads the static field name of an object-literal key. M2 records
// have string field names, so an identifier label or a string-literal key maps
// to a field; numeric and computed keys do not (they need M4's wider object
// system) and return false so the caller can raise a structured error.
func objKeyName(k ast.ObjKey) (string, bool) {
	switch k := k.(type) {
	case *ast.IdentExpr:
		return k.Name, true
	case *ast.StrLit:
		return k.Value, true
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
// if's value contribution — so `fn f(c) { if c { return X } else { Y } }` flows X
// into the function's return type (via the block return-point join) AND Y into
// the if's value, which the enclosing block joins. The two roles are orthogonal:
// X is a return point, but not part of the if-EXPRESSION's value.
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
// ThrowExpr / MatchExpr /
// DoExpr are not yet walked by the solver (inferExpr reports them unsupported), so
// these arms are unreachable from real source TODAY; they are kept in place so a
// form's divergence is already recognised the moment its inferExpr case lands,
// matching the checker rather than re-discovering divergence later. The checker's
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
