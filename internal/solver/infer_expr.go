package solver

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/provenance"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/escalier-lang/escalier/internal/solver/ucs"
)

// inferLiteral types a literal expression and records it in Info. A number, string, or
// boolean literal types as its singleton soltype.LitType. `null` and `undefined` type as
// the atoms atomLitOf returns. Neither carries provenance, for the reason atomLitOf gives.
// The remaining ast literal kinds, regex and bigint, fall through to the subset guard
// until soltype.Lit grows a member for each (§ soltype/type.go Prim/Lit note).
func (c *checker) inferLiteral(e *ast.LiteralExpr) soltype.Type {
	if atom, _, isAtom := atomLitOf(e.Lit); isAtom {
		c.recordType(e, atom)
		return atom
	}
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
// in Info. It delegates to inferFunc, the shared core also used by inferFuncDecl,
// factored so neither side owns the other. A `<T>` type-param list resolves into
// the function's own FuncType.TypeParams, quantified by value-binding
// generalization and freshened per call. An un-annotated param gets a fresh var,
// which generalization turns into a quantifier or coalesces to unknown/never at
// render time.
func (c *checker) inferFuncExpr(scope *Scope, lvl int, e *ast.FuncExpr) soltype.Type {
	t := c.inferFunc(scope, lvl, e.FuncSig, e.Body, e, true)
	c.recordType(e, t)
	return t
}

// inferFunc is the shared function-typing core for FuncExpr and FuncDecl. It
// opens a child scope, binds each param (its annotation resolved to a soltype,
// or a fresh var when un-annotated), types the body block in that scope, and
// builds the n-ary soltype.FuncType. The return type is built solely from the
// body's `return` statements (joinReturnPoints); a body with no return produces
// `undefined`. When the signature carries a return annotation the inferred return is
// constrained against it and the annotated type becomes the function's return
// type. A bodyless (declare/ambient) function adopts its return annotation
// without constraining anything. node supplies the span stamped onto a
// return-annotation constraint failure.
func (c *checker) inferFunc(scope *Scope, lvl int, sig ast.FuncSig, body *ast.Block, node ast.Node, allowTypeParams bool) *soltype.FuncType {
	// Give this function its own named-lifetime scope so a `&'a` in its signature
	// resolves consistently across its params and return, without sharing the name
	// with an enclosing or sibling function. Restored on exit so a nested function
	// does not clobber the outer scope's names. namedLifetime allocates the map on
	// first use, so a body with no `&'a` annotation pays no allocation.
	savedNamedLts := c.namedLifetimes
	c.namedLifetimes = nil
	defer func() { c.namedLifetimes = savedNamedLts }()
	// Take the name inferMemberBodies left for this member, and clear it so a lambda nested in
	// the body walked below is not blamed under the member's name. It is empty for every function
	// that is not a class member.
	memberName := c.memberName
	c.memberName = ""
	// Report any named lifetime the signature uses without binding it in its own `<…>`
	// list, and the symmetric unused binder. Run before resolving the params so the scan
	// reads the written names, not what namedLifetime has since interned.
	c.checkLifetimeDeclarations(sig.LifetimeParams, sig.Params, sig.Return, sig.Throws)
	// Resolve a standalone function's type parameters into a child scope so a param or
	// return annotation reads each `T` as one shared var. The var is minted above the
	// generalization level, so value-binding generalization quantifies it into
	// FuncType.TypeParams and instantiate freshens it per call. A non-generic function
	// reuses the enclosing scope and carries no type parameters.
	//
	// A method or constructor passes allowTypeParams=false. A member's own type
	// parameters need the per-instance projection the class-body freeze does not yet
	// apply, so resolving them would collapse two calls to one shared var. Report the
	// feature as unsupported and infer monomorphically until that work lands.
	declScope := scope
	var typeParams []*soltype.TypeParam
	if len(sig.TypeParams) > 0 {
		if allowTypeParams {
			declScope = scope.Child()
			typeParams = c.resolveTypeParams(declScope, lvl, sig.TypeParams)
		} else {
			c.reportUnsupportedFeature(node, "TypeParam")
		}
	}
	fnScope := declScope.Child()
	params := make([]*soltype.FuncParam, len(sig.Params))
	// paramTypes maps each bound parameter name to its soltype, consumed by the M4
	// G1 liveness pre-pass to seed parameter alias mutability.
	paramTypes := make(map[string]soltype.Type, len(sig.Params))
	for i, p := range sig.Params {
		pt := c.paramType(declScope, p, lvl)
		// A generic function in parameter position is a rank-2 callback such as
		// `g: <V>(x: V) -> V`. Its `<V>` binder is kept on the parameter's FuncType so a
		// caller's argument is checked against it by skolemizing `V`, and a call to the
		// parameter inside this body instantiates `V` per use. constrain's FuncType arm
		// performs both steps.
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
				// A pattern naming a `...rest` binds the properties its fields do not, so
				// the parameter has to admit an argument that carries them. Policy A would
				// otherwise close the usage-inferred object to exact, which rejects every
				// such argument and leaves the rest nothing to bind: `fn f({x, ...rest})`
				// would infer the parameter `{x: T0}` and reject `f({x: 1, y: 2})` for the
				// extra y. Marking the var open keeps the folded object inexact, the same
				// row-polymorphic shape the written `open` marker produces.
				if v, ok := pt.(*soltype.TypeVarType); ok && objectPatNamesRest(p.Pattern) {
					v.Open = true
				}
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

	// The clause is the sink every throw and call is checked against, so it resolves
	// before the body: `never` for no clause, which is how omitting it declares that the
	// function raises nothing; T for `throws T`; a fresh var for `throws _` or a bad one.
	// The clause form belongs to sync functions; the async arm below rejects one.
	var declaredThrows soltype.Type = &soltype.NeverType{}
	if sig.Throws != nil && !sig.Async {
		declaredThrows = c.freshAt(lvl)
		if annT, ok := c.resolveTypeAnn(declScope, sig.Throws, lvl); ok {
			declaredThrows = annT
		}
	}
	// An async fn cannot raise: its body's throws become the promise's rejection, so a
	// `-> Promise<V, E>` return annotation's E is the only surface that declares them.
	// It resolves before the body the way a sync clause does, and asyncPromise carries
	// that one classification to every later consumer. A written E seeds the sink and
	// every exceptional exit is checked against it. A nil sink leaves the rejection to
	// be inferred, minted lazily by throwsSink. A `throws` clause is rejected here.
	var asyncAnnT soltype.Type
	asyncAnnOK := false
	var asyncPromise *soltype.PromiseType
	if sig.Async {
		if sig.Return != nil {
			asyncAnnT, asyncAnnOK = c.resolveTypeAnn(declScope, sig.Return, lvl)
			if promise, isPromise := asyncAnnT.(*soltype.PromiseType); asyncAnnOK && isPromise {
				asyncPromise = promise
			}
		}
		if sig.Throws != nil {
			c.report(&AsyncThrowsClauseError{Throws: sig.Throws, Fn: node})
		}
		if asyncPromise != nil {
			declaredThrows = asyncPromise.ErrOrNever()
		} else {
			declaredThrows = nil
		}
	}

	// A generator's yield sink and `next` resolve before the body for the reason the
	// throws clause does, so each `yield` is checked at its own site. The return
	// annotation on a `gen fn` names the external Generator, and its slots seed the sinks.
	//
	// A generator cannot raise at the call either: its body's raises surface at `next(v)`,
	// so the annotation's E is the only surface that declares them and a `throws` clause is
	// rejected, exactly as on an `async fn`. A written E seeds the sink; otherwise the sink
	// is left to be inferred and lands in the wrapped generator's Throws.
	var gen *genSinks
	if sig.Gen {
		gen = c.resolveGenSinks(declScope, node, sig, lvl)
		if sig.Throws != nil && !sig.Async {
			c.report(&GenThrowsClauseError{Throws: sig.Throws, Fn: node})
		}
		if gen.ann != nil && gen.ann.Raises() {
			declaredThrows = gen.ann.Throws
		} else {
			declaredThrows = nil
		}
	}

	var ret soltype.Type = &soltype.UndefinedType{}
	var retExprs []ast.Expr
	// bodyDiverges records that every path through the body left along the exceptional
	// edge, and raised that some exceptional exit can actually raise. Both are read after
	// the walk to warn about a signature the body cannot deliver on.
	bodyDiverges := false
	raised := false
	// yielded records whether a generator body actually yields, read after the walk to
	// warn about a `gen` marker nothing uses.
	yielded := false
	// throws is what a caller sees on the exceptional edge. A bodyless `declare fn` has
	// only its clause. A body-carrying function reads the sink back once the body is
	// walked, which is the declared type when there was a clause and the inferred variable
	// when there was not. Nil means the function raises nothing.
	throws := declaredThrows
	hasBody := body != nil
	if hasBody {
		// PR3: open a fresh function context so every ReturnStmt encountered while
		// walking the body lands in our own returns list (a nested fn inside this
		// body opens its own context, so its returns never leak out here).
		saved := c.pushFuncCtx(sig.Async, node, lvl)
		c.fn.throws = declaredThrows
		if gen != nil {
			c.fn.gen = true
			c.fn.yields = gen.yields
			c.fn.yieldNext = gen.next
			c.fn.nextDeclared = gen.ann != nil
		}
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
		throws = c.fn.throws
		raised = c.fn.raised
		yielded = c.fn.yielded
		if gen != nil {
			gen.delegateNexts = c.fn.delegateNexts
		}
		collected := c.popFuncCtx(saved)
		// A body with no `return` that always leaves along the exceptional edge reaches
		// no normal exit, so it produces `never`, not the `undefined` a fall-through body
		// produces. Without this `fn f() -> number { throw Error("no") }` would report
		// `undefined <: number`.
		if len(collected) == 0 && blockDiverges(body) {
			ret = &soltype.NeverType{}
			bodyDiverges = true
		} else {
			ret = c.joinReturnPoints(node, lvl, collected)
		}
	}
	// A declared `throws T` the body never uses obliges every caller to handle an
	// exception that cannot occur, so warn and point at the clause. `throws _` asks for
	// inference rather than declaring anything, and a bodyless `declare fn` has no body to
	// measure against, so neither reaches here. Nor does an async clause: it already
	// drew an AsyncThrowsClauseError.
	if hasBody && !raised && sig.Throws != nil && !sig.Async && !isWildcardAnn(sig.Throws) {
		c.report(&UnusedThrowsClauseError{Ann: sig.Throws, Declared: throws})
	}
	// A `gen fn` whose body never yields returns a generator that finishes on the first
	// `next()` without producing a value, so the marker buys the caller nothing and costs
	// them a generator to unwrap. A bodyless `declare gen fn` has no body to measure.
	if hasBody && !yielded && sig.Gen {
		c.report(&GeneratorWithoutYieldError{Fn: node, Async: sig.Async})
	}
	// Return-annotation handling diverges by async-ness.
	//
	// Async: the function's EXTERNAL type is always `Promise<T>`, and the return
	// annotation — when present — names that external Promise, NOT the body's value.
	// So it must itself be a `Promise<…>`; the body returns the unwrapped inner,
	// constrained `<: inner`, and the annotation is presented as the external type
	// (no extra wrap). `Promise<_>` is allowed — the `_` resolves to a fresh var the
	// body flows into, inferring the inner. A bare annotation like
	// `async fn () -> number` is rejected, and an unresolved one was already reported
	// by resolveTypeAnn; both recover the way the no-annotation case does, wrapping
	// the inferred body return so the external face stays Promise-shaped and callers
	// don't cascade. That wrap is also what an unannotated async fn gets, preserving
	// M3's no-auto-flatten behavior: a body that already returns a Promise wraps
	// again, so `async fn (p: Promise<T>) { return await p }` is
	// `Promise<Promise<T>>`. Awaited<T> is M9.
	//
	// Non-async: the annotation governs the return directly — the body is
	// constrained `<: annotation` and the function returns the annotation (M2's
	// rule). An unsupported annotation (ok=false) was already reported by
	// resolveTypeAnn; recover by keeping the inferred body type (or unknown when
	// there is no body, since a synthetic `undefined` would falsely signal "returns
	// nothing").
	//
	// A generator takes its own arm ahead of the async one: an `async gen fn` faces
	// callers as an AsyncGenerator, not a Promise, so the async wrap never applies.
	//
	// It also moves the body's raises, the way the async arm moves them into the promise.
	// Obtaining a generator runs no body code, so what the body raises belongs on the
	// generator it returns rather than on the signature, and the function's own Throws
	// stays `never`. Iterating or delegating is what advances it, and those sites read the
	// slot back into their enclosing sink.
	if sig.Gen {
		ret = c.genReturn(node, gen, retExprs, ret, throws, hasBody)
		throws = nil
	} else if sig.Async {
		// The async arm also moves the body's throws. An `async fn` rejects its promise
		// rather than raising, so the body's throws become the promise's Err and the
		// function's own Throws stays `never`.
		if asyncPromise != nil {
			// Only constrain when there IS a body, for the reason the non-async arm
			// below spells out.
			if hasBody {
				c.constrain(node, ret, asyncPromise.Inner) // body <: declared inner
			}
			ret = asyncPromise
		} else {
			if sig.Return != nil && asyncAnnOK {
				c.report(&AsyncReturnNotPromiseError{Return: sig.Return, Fn: node})
			}
			ret = c.wrapPromise(node, ret, throws)
		}
		throws = nil
	} else if sig.Return != nil {
		if annT, ok := c.resolveTypeAnn(declScope, sig.Return, lvl); ok {
			// Only constrain the body when there IS one; a bodyless (declare/ambient)
			// function simply adopts the annotation (constraining the synthetic `undefined`
			// would raise a spurious `undefined <: T`).
			if hasBody {
				c.constrainReturnAgainstAnnotation(node, retExprs, ret, annT) // body <: declared return
				// No caller can observe an annotated return the body never reaches, so warn
				// and point at the annotation. A body that diverges into `never` on purpose
				// writes `-> never`, which is what it delivers and so is not flagged.
				if bodyDiverges && !isNeverType(annT) {
					c.report(&UnreachableReturnAnnotationError{Ann: sig.Return, Declared: annT})
				}
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
	ft := &soltype.FuncType{Params: params, Ret: ret, Throws: throws, Inexact: sig.Inexact, TypeParams: typeParams}
	// Record the function's own type against its node so a function flowing into a
	// non-function requirement blames the function, and FuncArityMismatchError can
	// carry a "defined here" related span. (For a named callee this raw FuncType is
	// re-minted by coalescing at binding time, so the entry is exact for inline
	// callees; M3's FromInstantiation makes named-callee blame precise.)
	c.recordProv(ft, node, FuncInference)
	// Queue the return type for the finite-inhabitant check. A `declare fn` adopts its annotation
	// with no body to recurse through, so only a body-carrying function is queued. The check itself
	// waits until the enclosing group's bound graph is complete, since a mutually-recursive knot is
	// not tied until every body in the group has been walked.
	if hasBody {
		c.queueReturnCheck(node, ft, lvl-1, memberName)
	}
	// A body-carrying function must prove every lifetime bound its signature declares,
	// so its bounds are checked against the inferred relation. A no-body `declare fn` has
	// no body to prove them, so it lowers each declared bound into constrainLt instead.
	// The bound then solves like one a body would infer.
	if hasBody {
		c.checkDeclaredLifetimeBounds(sig.LifetimeParams, ft)
		// A body-carrying generic function must actually produce every type parameter it
		// declares in an output position. A bodyless `declare fn` asserts its signature
		// with no body to check, so it is not verified here.
		c.checkTypeParamsProducible(node, ft)
	} else {
		c.lowerLifetimeParamBounds(sig.LifetimeParams, lvl)
	}
	return ft
}

// checkDeclaredLifetimeBounds verifies that the function body establishes every outlives
// relation the signature declares. A declared `<'a: 'b>` asserts 'a outlives 'b, and the
// body must prove it. The inferred outlives relation is read through ltOutlivesRelation,
// the same relation the printer renders, so a bound the signature declares and the
// printer would render are accepted together. A declared bound the inference does not
// prove is reported as a LifetimeBoundNotSatisfiedError blaming its binder.
//
// Declared bounds are NOT lowered into the graph for a body-carrying function. The body
// must prove each relation on its own, so lowering would make every declared bound
// trivially self-satisfying. The names resolve through the function's namedLifetimes
// map, which inferFunc has already populated from the signature's borrows, so a declared
// `'a` and the `&'a` borrow writing it share one variable.
func (c *checker) checkDeclaredLifetimeBounds(params []*ast.LifetimeParam, ft *soltype.FuncType) {
	hasBound := false
	for _, p := range params {
		if len(p.Bounds) > 0 {
			hasBound = true
			break
		}
	}
	if !hasBound {
		return
	}

	a, _, outlives := ltOutlivesRelation(ft, soltype.Positive)

	lookup := func(name string) (*soltype.LifetimeVar, bool) {
		if c.namedLifetimes == nil {
			return nil, false
		}
		v, ok := c.namedLifetimes[name]
		return v, ok
	}
	// sameLt reports whether two named lifetimes are one lifetime. They are one when they
	// are the same variable, or when the solved graph proved them mutually outliving so
	// they share an SCC representative.
	sameLt := func(x, y *soltype.LifetimeVar) bool {
		return a != nil && a.bs.repOf(x.ID) == a.bs.repOf(y.ID)
	}
	// staticForced reports whether the solved graph forces v to 'static, the escape
	// constraint v <: 'static that records 'static as an upper bound. A lower-bound
	// 'static is trivially true and forces nothing, so forcedToStatic reads the wrong
	// direction for this test. The bound set's static set reads upper bounds only, the
	// same set implies consults.
	staticForced := func(v *soltype.LifetimeVar) bool {
		return a != nil && a.bs.static.Contains(a.bs.repOf(v.ID))
	}
	// proves reports whether the inferred relation proves 'sub outlives 'super. outlives is
	// already transitive, so no further walk is needed here. implies reads reachability over
	// the whole condensed graph, and componentParams gathers every param in a join's
	// connected component, so a param outlives a join it feeds through any number of hops.
	proves := func(sub, super *soltype.LifetimeVar) bool {
		return sameLt(sub, super) || (outlives != nil && outlives(sub, super))
	}

	for _, p := range params {
		// A 'static left-hand side is not a bindable parameter. The parser rejects it, so
		// no binder named "static" reaches here. This guard is defensive against a
		// hand-built AST.
		if p.Name == "static" {
			continue
		}
		sub, subOk := lookup(p.Name)
		for _, b := range p.Bounds {
			satisfied := false
			switch {
			case !subOk:
				// The bound's left lifetime borrows nothing, so the body proves nothing
				// about it and the bound cannot hold.
				satisfied = false
			case staticForced(sub):
				// A 'static-forced lifetime outlives everything, so every bound on it holds.
				satisfied = true
			case b.Name == "static":
				// Only a 'static-forced left lifetime outlives 'static, and that case is
				// already satisfied above, so any other left lifetime fails the bound.
				satisfied = false
			default:
				super, superOk := lookup(b.Name)
				satisfied = superOk && proves(sub, super)
			}
			if !satisfied {
				c.report(&LifetimeBoundNotSatisfiedError{Sub: p.Name, Super: b.Name, Param: p})
			}
		}
	}
}

// joinReturnPoints builds a function's return type from the ReturnStmt types
// collected while walking its body. No returns means the body produces no value,
// so the function returns `undefined`. A return-less body that always diverges via
// `throw` is conceptually `never`. That case is deferred, because throw, do, and
// match are not walked yet, so such a body cannot be recognized as diverging.
// Recovery placeholders use the absorbing ErrorType sentinel rather than a raw
// NeverType. A single return is the return type directly, with no join variable
// and no indirection. Multiple returns flow through a fresh join variable whose
// coalesced positive face is their union, constrained in source order so the
// rendered union reflects source order.
func (c *checker) joinReturnPoints(node ast.Node, lvl int, collected []soltype.Type) soltype.Type {
	switch len(collected) {
	case 0:
		return &soltype.UndefinedType{}
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
		c.checkUniformOwnership(node, collected)
		return c.joinBranches(node, lvl, ReturnJoin, collected)
	}
}

// joinBranches unites the values several paths of one form produce into a single type. It
// mints a fresh variable, records prov on it under kind, and constrains each branch into it
// in source order, so the rendered union follows the source.
//
// Whether the branches agree on ownership is the caller's check to run, because the types
// that answer for ownership are not always the types being joined. A `val … else` joins the
// plain variable its projection walk lowered each leaf into, and that variable cannot show a
// borrow the fallback's binding mode carries.
//
// A form that hands its join variable to a walk cannot use this at all, because the walk
// needs the variable before the branch types exist. inferIfElse, inferIfVal, inferMatch, and
// inferTryCatch each mint their own and check ownership once the walk returns.
func (c *checker) joinBranches(node ast.Node, lvl int, kind ASTOriginKind, branches []soltype.Type) soltype.Type {
	joinVar := c.freshAt(lvl)
	c.recordProv(joinVar, node, kind)
	for _, b := range branches {
		c.constrain(node, b, joinVar)
	}
	return joinVar
}

// collectBranchOwnership sets sawBorrowed for a borrow and sawOwned for an owned
// object, tuple, or owned-mutable RefType, recursing through union and intersection
// members. Value types and inference variables carry no ownership and set neither.
func collectBranchOwnership(t soltype.Type, sawOwned, sawBorrowed *bool) {
	switch t := t.(type) {
	case *soltype.RefType:
		if t.Lt != nil {
			*sawBorrowed = true
		} else {
			*sawOwned = true
		}
	case *soltype.UnionType:
		for _, m := range t.Types {
			collectBranchOwnership(m, sawOwned, sawBorrowed)
		}
	case *soltype.IntersectionType:
		for _, m := range t.Types {
			collectBranchOwnership(m, sawOwned, sawBorrowed)
		}
	case *soltype.ObjectType, *soltype.TupleType:
		*sawOwned = true
	}
}

// checkUniformOwnership reports a MixedOwnershipError against node when the branches a
// join is about to union are some owned and some borrowed. Each branch is coalesced
// first so an inference variable resolves to the shape that flowed into it.
//
// A node reports the fault once. A `val … else` runs a separate join per leaf its pattern
// binds, and every one of those joins blames the whole declaration, so a pattern with two
// mixed leaves would otherwise underline the declaration twice.
func (c *checker) checkUniformOwnership(node ast.Node, branches []soltype.Type) {
	sawOwned, sawBorrowed := false, false
	for _, b := range branches {
		collectBranchOwnership(coalesce(b, soltype.Positive), &sawOwned, &sawBorrowed)
	}
	if !sawOwned || !sawBorrowed || c.reportedMixedOwnership(node) {
		return
	}
	c.report(&MixedOwnershipError{Node: node})
}

// reportedMixedOwnership reports whether node already carries a MixedOwnershipError.
func (c *checker) reportedMixedOwnership(node ast.Node) bool {
	for _, e := range c.errs {
		if prev, ok := e.(*MixedOwnershipError); ok && prev.Node == node {
			return true
		}
	}
	return false
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

// joinBorrows joins several mutable borrows of objects. It applies only when EVERY
// input is a mutable borrow of an object, all sharing the same field-name set with
// each carrying a lifetime, and returns ok=false otherwise so the caller falls back
// to its generic union path. The result depends on whether the shared fields
// reconcile:
//
//   - Reconcilable: one mutable borrow `&('a | 'b) mut {…}` whose lifetime is a fresh
//     join variable bounded below by each input's. The shared fields are pinned
//     invariant, since a mut field is read AND written through the single carrier.
//   - Conflicting: the read-until-narrowed union of the distinct borrows,
//     `&'a mut {x: number} | &'b mut {x: string}`, rather than erroring on the pin.
//
// The pin runs under a probe. A successful pin commits the single carrier. A failed
// pin discards its bounds and error and yields the union instead, so the union path
// leaves no trace.
//
// The union is governed by a read-until-narrowed contract: it is readable everywhere
// but writable at its conflicting fields only after narrowing. A read off the union
// yields the covariant union of the conflicting fields via the union read rule. A
// write to a conflicting field is rejected, which keeps the union sound: it is
// read-only at its conflicting fields, and a rejected write never changes its type.
// To write, narrow to one branch with
// `if val r2: mut {x: number} = r` and write through the fresh mutable view.
func (c *checker) joinBorrows(node ast.Node, lvl int, types []soltype.Type) (soltype.Type, bool) {
	inners, lts, allMut, ok := soltype.UnwrapRefs(types)
	if !ok || !allMut {
		return nil, false
	}
	objs := make([]*soltype.ObjectType, len(inners))
	for i, inner := range inners {
		obj, isObj := inner.(*soltype.ObjectType)
		if !isObj {
			return nil, false
		}
		if i > 0 && !sameObjectKeys(objs[0], obj) {
			return nil, false
		}
		objs[i] = obj
	}

	// Pin each shared field invariant across the inputs under a probe. A mut object's
	// fields are read AND written through the single carrier, so it is sound only when
	// they agree. Committing on success and discarding on failure lets the union path
	// leave no trace.
	errsBefore := len(c.errs)
	p := c.openProbe()
	for _, e := range objs[0].Elems {
		name := soltype.AsProperty(e).Name
		first, _ := objs[0].Prop(name)
		for _, obj := range objs[1:] {
			other, _ := obj.Prop(name)
			c.constrain(node, first.Type, other.Type)
			c.constrain(node, other.Type, first.Type)
		}
	}
	reconciled := len(c.errs) == errsBefore
	c.closeProbe(p, reconciled)
	if !reconciled {
		// The fields conflict. Discarding the probe rolled back the failed pin and
		// its error, so each borrow keeps its own lifetime. Build the
		// read-until-narrowed union of the original borrows.
		//
		// Pass no Context, so subsumption does not run. Each union member is a
		// distinct borrow the value can take at runtime, with its own lifetime.
		// Subsumption would trial one member <: another and drop the "subtype",
		// but two borrows differing only in lifetime each subtype the other
		// through a lifetime constraint the trial then discards. Dropping either
		// would retype a value that may be the shorter-lived borrow as the
		// longer-lived one, so a later read could outlive the borrowed data. The
		// distinct lifetimes must survive into the union for the borrow check to
		// stay sound.
		return newUnion(nil, types, false), true
	}
	// Reconcilable fields: unite the input lifetimes under one join lifetime and return the
	// single mutable carrier. Uniting them only here keeps the union path from minting a
	// dead lifetime.
	return &soltype.RefType{Mut: true, Lt: c.ctx.joinLifetimes(lvl, lts), Inner: objs[0]}, true
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

// readCarrier peels the borrow wrapper for a field-read requirement so the
// borrow-escape guard does not fire on a read, which is always legal. A concrete
// RefType or a union of borrows peels directly. A borrow held in a join variable's
// lower bounds — the shape a `val`-bound or call-result borrow takes — would
// otherwise propagate un-peeled into the requirement and escape-error, so the
// variable case peels its lower bounds, the same look-through resolveFunc uses to
// find a concrete callee behind a binding var. For example, in
//
//	val r = if c { p } else { q }   // p, q are &mut borrows
//	r.x                             // recv is r's variable, lower-bounded by both borrows
//
// the read peels the two borrow lower bounds to `{x: …} | {x: …}` rather than
// constraining the borrows against the field requirement directly.
//
// The peel keys off the receiver variable's lower bounds, not the kind of read. A
// variable with no borrow lower bound is returned unchanged. A parameter read like
// `fn f(p) { p.x }` is one such case: nothing flows into p inside the body, so p's
// variable has no lower bound, only the upper-bound field requirement the read adds,
// and it coalesces to an owned object rather than a borrow.
func readCarrier(recv soltype.Type) soltype.Type {
	v, ok := recv.(*soltype.TypeVarType)
	if !ok {
		return peelBorrows(recv)
	}
	// Peel each lower bound, the same look-through resolveFunc uses to find a concrete
	// callee behind a binding var. Divert to the peeled carriers only when a borrow is
	// actually present, so a non-borrow variable read keeps the variable-direct path.
	members := make([]soltype.Type, 0, len(v.LowerBounds))
	sawBorrow := false
	for _, lb := range v.LowerBounds {
		if lb == soltype.Type(v) {
			// A nested mut write such as `obj.p.x = 5` can leave the receiver
			// variable with a vacuous `v <: v` self-edge among its bounds, the same
			// case withoutSelf drops in coalesce. It constrains nothing, so skip it
			// rather than point the read back at the receiver variable.
			continue
		}
		if hasBorrowShape(lb) {
			sawBorrow = true
		}
		members = append(members, peelBorrows(lb))
	}
	if !sawBorrow {
		return v
	}
	return newUnion(nil, members, false)
}

// peelBorrows strips the borrow wrapper off a concrete carrier, distributing through
// a union so a union of borrows peels each member. A type variable is left as a leaf,
// since recursing into its bounds could loop on a cyclic bound, and constrain handles
// a bare variable on the sub side anyway.
func peelBorrows(t soltype.Type) soltype.Type {
	switch t := t.(type) {
	case *soltype.RefType:
		return t.Inner
	case *soltype.UnionType:
		members := make([]soltype.Type, len(t.Types))
		for i, m := range t.Types {
			members[i] = peelBorrows(m)
		}
		// The tail's bound is a member the union does not list, so it peels too. Dropping it
		// would leave the peeled union unbounded, which is top.
		tail := tailOf(t)
		if tail.bound != nil {
			tail.bound = peelBorrows(tail.bound)
		}
		return newUnionWithTail(nil, members, tail)
	}
	return t
}

// hasBorrowShape reports whether t is a borrow or a union with a borrow member. It
// gates readCarrier's look-through so only a variable actually holding a borrow
// diverts to the peeled-bound read path.
func hasBorrowShape(t soltype.Type) bool {
	switch t := t.(type) {
	case *soltype.RefType:
		return true
	case *soltype.UnionType:
		for _, m := range t.Types {
			if hasBorrowShape(m) {
				return true
			}
		}
	}
	return false
}

// sameObjectKeys reports whether two objects carry exactly the same set of property
// names — the join's precondition, since a mut object's field set is invariant.
func sameObjectKeys(a, b *soltype.ObjectType) bool {
	if len(a.Elems) != len(b.Elems) {
		return false
	}
	// A residual object has no settled key set to compare, so it never satisfies the join's
	// invariant-field precondition.
	if soltype.HasResidualElem(a.Elems) || soltype.HasResidualElem(b.Elems) {
		return false
	}
	for _, e := range a.Elems {
		if _, ok := b.Prop(soltype.AsProperty(e).Name); !ok {
			return false
		}
	}
	return true
}

// wrapPromise mints the external `Promise<inner, errT>` face of an async function and
// records its provenance (PromiseWrap) against the function node. errT is the body's
// throws sink — nil when the body has no exceptional exit — and needs no normalizing
// here, since readers collapse nil and an explicit `never` through ErrOrNever.
func (c *checker) wrapPromise(node ast.Node, inner, errT soltype.Type) soltype.Type {
	wrapped := &soltype.PromiseType{Inner: inner, Err: errT}
	c.recordProv(wrapped, node, PromiseWrap)
	return wrapped
}

// genSinks is the per-body generator state inferFunc seeds before walking a `gen fn`.
// `yields` is the sink each `yield` operand is constrained into, the twin of the throws
// sink. `next` is what a `yield` evaluates to, the value a caller sends back in through
// `next(v)`. `ann` is the return annotation when it is a matching generator, which
// genReturn presents as the external type; it is nil otherwise.
type genSinks struct {
	yields soltype.Type
	next   soltype.Type
	ann    *soltype.GeneratorType
	// delegateNexts is what the body's `yield from` operands accept, copied off the
	// funcCtx before it is popped. wrapGenerator meets them to get the external Next.
	delegateNexts []soltype.Type
	// async records the signature's async-ness so wrapGenerator picks Generator or
	// AsyncGenerator after popFuncCtx has already restored the enclosing context.
	async bool
}

// resolveGenSinks seeds a generator body's sinks from its return annotation, before the
// body is walked so each `yield` is checked at its own site.
//
// The annotation names the EXTERNAL generator, so it must be `Generator<Y, R, N>`, or
// `AsyncGenerator<Y, R, N>` when the function is async. A matching one seeds the yield
// sink from Y and `next` from N. Anything else draws a GenReturnNotGeneratorError and
// falls back to the no-annotation seeding.
//
// Without an annotation the yield sink is a fresh variable the yields flow into, and
// `next` is `unknown`. The Next slot is contravariant, so `unknown` is the neutral
// choice there, accepting every value a caller sends through `next(v)`. Inside the
// body a `yield` then evaluates to `unknown`, which needs narrowing before use.
func (c *checker) resolveGenSinks(scope *Scope, node ast.Node, sig ast.FuncSig, lvl int) *genSinks {
	gs := &genSinks{yields: c.freshAt(lvl), next: &soltype.UnknownType{}, async: sig.Async}
	if sig.Return == nil {
		return gs
	}
	annT, ok := c.resolveTypeAnn(scope, sig.Return, lvl)
	if !ok {
		// Unsupported annotation — already reported by resolveTypeAnn. Keep the
		// no-annotation seeding.
		return gs
	}
	g, isGen := annT.(*soltype.GeneratorType)
	if !isGen || g.Async != sig.Async {
		// A non-Generator annotation, or a Generator whose async-ness contradicts the
		// signature (`gen fn () -> AsyncGenerator<…>` and the reverse). Reject it, then
		// recover with the no-annotation seeding so the external face stays
		// Generator-shaped and callers don't cascade.
		c.report(&GenReturnNotGeneratorError{Return: sig.Return, Fn: node, Async: sig.Async})
		return gs
	}
	gs.yields = g.Yield
	gs.next = g.Next
	gs.ann = g
	return gs
}

// genReturn computes a `gen fn`'s external return type, always a generator, since
// calling one returns a generator object rather than the body's value. A matching
// annotation IS that type, with the body's return constrained against its `Ret` slot.
// Otherwise the inferred pieces are wrapped, with what the body raises going in the
// generator's Throws. A bodyless `declare gen fn` wraps `unknown` rather than the
// synthetic `undefined`, which would signal that it returns nothing.
func (c *checker) genReturn(node ast.Node, gs *genSinks, retExprs []ast.Expr, bodyType, throws soltype.Type, hasBody bool) soltype.Type {
	if gs.ann != nil {
		if hasBody {
			c.constrainReturnAgainstAnnotation(node, retExprs, bodyType, gs.ann.Ret) // body <: declared Ret
		}
		return gs.ann
	}
	if !hasBody {
		bodyType = &soltype.UnknownType{}
	}
	return c.wrapGenerator(node, gs, bodyType, throws)
}

// wrapGenerator mints the external generator face of a generator function and records
// its provenance (GeneratorWrap) against the function node. A nil throws leaves the
// Throws slot nil, the shorthand for a generator that cannot raise.
//
// Only an unannotated generator reaches here, so the Next slot is inferred. What the
// body delegates to fixes it, since `yield from` forwards a sent value into the
// delegate and so can only accept what the delegate accepts. A body with no delegation
// puts no requirement on the slot and keeps gs.next, the `unknown` resolveGenSinks
// seeded.
func (c *checker) wrapGenerator(node ast.Node, gs *genSinks, bodyType, throws soltype.Type) soltype.Type {
	next := c.meetNexts(gs.delegateNexts)
	if next == nil {
		next = gs.next
	}
	wrapped := &soltype.GeneratorType{
		Yield: gs.yields, Ret: bodyType, Next: next, Throws: throws, Async: gs.async,
	}
	c.recordProv(wrapped, node, GeneratorWrap)
	return wrapped
}

// paramType resolves a param's type: its annotation when present, else a fresh
// inference variable at the current level (the spike's "fresh var per param").
// An unsupported annotation (ok=false) already reported its own error; the param
// adopts a fresh var rather than the `never` placeholder so the body and any
// call site recover against an unconstrained variable instead of cascading
// `<: never` failures.
func (c *checker) paramType(scope *Scope, p *ast.Param, lvl int) soltype.Type {
	if p.TypeAnn != nil {
		if t, ok := c.resolveTypeAnn(scope, p.TypeAnn, lvl); ok {
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
	// A member callee whose type is an intersection of function arms is an overloaded
	// method, since memberValue gathers a multi-signature method's arms into an
	// IntersectionType. Resolving one arm here through resolveOverload, the method analogue
	// of the direct overloaded-name call above, keeps the disjunction out of the callee <:
	// callShape constraint and reuses the declaration-order deferral for an unconstrained
	// argument (E1). A `g = f; g(x)` call through an intermediate binding has an ident
	// callee, not a member one, so it stays on the value-position intersection path in
	// constrain.
	if _, isMember := e.Callee.(*ast.MemberExpr); isMember {
		if arms, ok := funcIntersectionArms(callee); ok {
			return c.inferMethodOverloadCall(scope, lvl, e, arms)
		}
	}
	// Instantiate a generic callee so each call binds its type parameters independently. A
	// rank-2 callback param is an unfreshened MonoScheme, so this keeps its `T` per-call.
	if ft, ok := callee.(*soltype.FuncType); ok && len(ft.TypeParams) > 0 {
		callee = c.ctx.instantiateFuncBinder(ft, lvl)
	}
	args := make([]*soltype.FuncParam, len(e.Args))
	for i, a := range e.Args {
		args[i] = &soltype.FuncParam{Type: c.inferExpr(scope, lvl, a)}
	}
	res := c.freshAt(lvl)
	c.recordProv(res, e, Application)

	// Arity lints (#677 §4.2.3): a DIRECT call rejects too-many AND too-few arguments, for exact
	// and inexact callees alike, since supplying extras to a call you can see is a mistake even
	// where the lattice tolerates them. They fire only for a concrete callee. When one fires the
	// demand is reshaped into the callee's accept-set so the synth's gate does not also report
	// arity: too-many truncates, too-few pads with fresh vars that constrain nothing.
	fn, resolved := resolveFunc(callee)
	if resolved {
		// A tuple-typed rest param expands to one positional param per element, so the lints, the
		// owned-mutable upgrade, and consumeCallArgs below read plain positions rather than
		// comparing an argument against the whole tuple.
		fn = expandTupleRest(fn)
	}
	demand := args
	switch {
	case resolved && !hasRest(fn) && len(args) > len(fn.Params):
		// A rest param survives expansion only when it binds an unbounded number of args,
		// so it absorbs any number of trailing ones and is never "too many". Only a
		// fixed-arity callee trips this lint.
		c.errs = append(c.errs, &TooManyArgsError{Call: e, Fn: fn})
		demand = args[:len(fn.Params)]
	case resolved && len(args) < requiredCount(fn):
		c.errs = append(c.errs, &NotEnoughArgsError{Call: e, Fn: fn})
		// Pad to whichever is larger, the declared parameter count or the required count.
		// The expansion above makes them equal for a callee whose rest parameter is an exact
		// tuple, but an INEXACT tuple rest is left unexpanded and can require more arguments
		// than it declares parameters: `fn (a: number, ...args: [string, boolean, ...]) -> R`
		// declares two and requires three. Padding to the parameter count alone would leave
		// the demand below the accept-set floor, so the gate would report an arity mismatch
		// on top of the lint that just fired.
		want := max(len(fn.Params), requiredCount(fn))
		demand = make([]*soltype.FuncParam, want)
		copy(demand, args)
		for i := len(args); i < want; i++ {
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
	// The shape's Throws slot is this body's sink, so constrain's covariant throws rule
	// records what the callee raises into it. A non-throwing `never` records nothing.
	callShape := &soltype.FuncType{Params: demand, Ret: res, Throws: c.throwsSink(lvl)}
	// A resolved callee that declares nothing raises nothing, so it leaves the enclosing
	// clause unused. Any other callee counts as raising, since its throws may still be an
	// unsolved variable at this point.
	if fn, ok := resolveFunc(callee); !ok || !isNeverType(fn.ThrowsOrNever()) {
		c.markRaised()
	}
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
		// A consuming argument carries its value out of the frame into the callee, so a
		// borrow of a local the argument carries escapes unless the argument owns a
		// self-contained component the move re-anchors. recordEscapeSite records the
		// argument for the post-pass to decide. A `&`/`&mut` parameter borrows instead of
		// consuming, so the isConcreteOwned gate above skips it.
		c.recordEscapeSite(arg, ref)
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

// inferMethodOverloadCall types a call to an overloaded method reached through a member
// callee, such as `p.m(args)` where `m` carries several signatures. It infers the
// arguments, then resolves one arm through resolveOverload — the same machinery a direct
// overloaded-name call uses, driven by the method's arms wrapped as monomorphic schemes.
// Like inferOverloadedCall it emits no callee <: callShape constraint. Overload resolution
// owns arity and argument checking, and a no-match becomes a NoMatchingOverloadError.
func (c *checker) inferMethodOverloadCall(scope *Scope, lvl int, e *ast.CallExpr, arms []*soltype.FuncType) soltype.Type {
	args := make([]soltype.Type, len(e.Args))
	for i, a := range e.Args {
		args[i] = c.inferExpr(scope, lvl, a)
	}
	schemes := make([]TypeScheme, len(arms))
	for i, arm := range arms {
		schemes[i] = &MonoScheme{Ty: arm}
	}
	ret := c.resolveOverload(lvl, ValueBinding{Schemes: schemes}, args, e)
	c.recordType(e, ret)
	return ret
}

// funcIntersectionArms reports whether t is an IntersectionType whose members are all
// FuncTypes, returning those arms. memberValue builds exactly this shape for an overloaded
// method, so it identifies a method-overload callee. A non-intersection, an empty
// intersection, or an intersection carrying a non-function member is not a method overload
// set, so it stays on the ordinary callee <: callShape path.
func funcIntersectionArms(t soltype.Type) ([]*soltype.FuncType, bool) {
	inter, ok := t.(*soltype.IntersectionType)
	if !ok || len(inter.Types) == 0 {
		return nil, false
	}
	arms := make([]*soltype.FuncType, len(inter.Types))
	for i, m := range inter.Types {
		// A direct assertion, not resolveFunc: memberValue builds each arm with
		// strippedMethodSig, so an arm is always a concrete FuncType with nothing to look
		// through. resolveFunc's var and constructor-object look-through would only broaden
		// this into a looser "is any intersection callable" test, pulling annotation- or
		// class-value-derived intersections off the ordinary callee <: callShape path.
		ft, ok := m.(*soltype.FuncType)
		if !ok {
			return nil, false
		}
		arms[i] = ft
	}
	return arms, true
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
//
// A class value is an object carrying its constructor as a ConstructorElem rather than a
// bare FuncType, so a call `Point(1, 2)` recovers the constructor signature through that
// element. An inline object callee yields it directly.
// A binding var yields it through an object lower bound, the same look-through the
// FuncType arm runs for a named function.
func resolveFunc(t soltype.Type) (*soltype.FuncType, bool) {
	switch t := t.(type) {
	case *soltype.FuncType:
		return t, true
	case *soltype.ObjectType:
		if ctor, ok := t.Constructor(); ok {
			return ctor.Fn, true
		}
	case *soltype.TypeVarType:
		for _, lb := range t.LowerBounds {
			switch lb := lb.(type) {
			case *soltype.FuncType:
				return lb, true
			case *soltype.ObjectType:
				if ctor, ok := lb.Constructor(); ok {
					return ctor.Fn, true
				}
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
// stored, so it recovers to `undefined`.
func (c *checker) inferAssign(scope *Scope, lvl int, e *ast.BinaryExpr) soltype.Type {
	undefinedT := soltype.Type(&soltype.UndefinedType{})
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
	// Record `undefined` on e up front as the recovery type. Every error path below
	// returns undefinedT without recording a type, so this guarantees the node is typed
	// on failure. The success path overwrites it with the stored value's type; see the
	// end of this function.
	c.recordType(e, undefinedT)

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
		return undefinedT
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
		return undefinedT
	}
	if b.Kind != ast.VarKind {
		c.report(&CannotAssignToImmutableError{
			Assign: e,
			Name:   target.Name,
			Decl:   bindingDecl(b),
		})
		return undefinedT
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
		// Record any borrow of a local the reassigned value introduces, so a later
		// flow-out of the target finds it. `a = &mut b` records a → b. Recording strong-
		// updates the binding, clearing its prior referent, and the flush lands the new
		// edge set at this statement's CFG point so the flow-sensitive graph joins it at
		// later merges.
		if !b.ModuleLevel {
			c.recordBorrowEdges(target.VarID, e.Right)
			if ref, ok := c.fn.stmtToRef[assignStmt]; ok {
				c.flushBorrowDirty(ref)
			}
		}
	}
	// The assignment evaluates to the value just stored — the SAME read face as
	// reading the target (inferIdent), so `val b = (a = 6)` ⇒ `b: number`. Use
	// instantiate (the read face), NOT the coalesced write-face targetT: targetT is a
	// display type that may be a Union/Intersection node, and re-injecting it into the
	// constraint graph here would later crash the coalescer when this value flows on
	// (e.g. through a `return`). This overwrites the `undefined` recorded for e above,
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
	undefinedT := soltype.Type(&soltype.UndefinedType{})
	if m.OptChain {
		// `recv?.prop = …` is not a meaningful assignment target; optional chaining is
		// M6 regardless, so report the whole target as unsupported rather than typing it.
		c.reportUnsupportedFeature(e.Left, "assignment to a member or index")
		return undefinedT
	}
	if m.Prop == nil || m.Prop.Name == "" {
		// A malformed `recv. = …`: the parser already reported the missing property
		// name, so emit nothing further and recover to `undefined`.
		return undefinedT
	}
	recv := c.inferWriteReceiver(scope, lvl, m.Object)
	// An accessor named prop resolves here rather than through the structural requirement
	// below, whose element is a PropertyElem. constrain's object arm matches the sub side
	// with ObjectType.Prop, so an accessor would read there as a missing property.
	if accessor, ok := c.writeAccessor(m.Prop.Name, readCarrier(recv)); ok {
		return c.inferAccessorAssign(lvl, e, m, recv, source, accessor, assignStmt)
	}
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
			// Storing a value that borrows a local into a parameter's field escapes, since
			// the parameter's object outlives the frame and the stored local would dangle in
			// the caller. checkParamFieldStoreEscape applies only when the receiver is a
			// parameter, and records the store for the post-pass to decide.
			c.checkParamFieldStoreEscape(m.Object, e.Right, ref)
			// A store into a LOCAL receiver's field records a borrow edge instead, rooted at
			// the field. It does not escape until the receiver itself flows out, at which
			// point the recorded edge is followed. `b.peer = &mut d` records b → d at [peer].
			c.recordFieldStoreEdges(m.Object, m.Prop.Name, e.Right, ref)
		}
	}
	// The assignment evaluates to the value just stored. recordType overwrites the
	// `undefined` recovery type inferAssign recorded on e before dispatching here.
	c.recordType(e, w)
	return w
}

// inferAccessorAssign types a write `recv.prop = source` that resolved to an accessor. A
// getter-only member is reported, having no setter to call. A setter write is a call, not
// a store: it checks the source against the setter's parameter and the receiver against
// its `self`, and it raises whatever the setter declares. It records nothing in `written`,
// since a setter has no cell a later read could shortcut to.
func (c *checker) inferAccessorAssign(
	lvl int,
	e *ast.BinaryExpr,
	m *ast.MemberExpr,
	recv soltype.Type,
	source soltype.Type,
	accessor soltype.ObjTypeElem,
	assignStmt ast.Stmt,
) soltype.Type {
	setter, ok := accessor.(*soltype.SetterElem)
	if !ok {
		out := c.report(&ReadOnlyPropertyError{Name: m.Prop.Name, Site: e})
		c.recordType(e, out)
		return out
	}
	// Writing through a setter runs its body, so the write is an exceptional exit of the
	// enclosing body exactly as a getter read is. This runs before errsBefore is captured,
	// so an undeclared raise does not suppress the move the write records below — the two
	// checks are independent.
	c.raiseAccessorThrows(lvl, e, setter.ThrowsOrNever())
	errsBefore := len(c.errs)
	c.checkReceiverMut(e.Left, recv, setter.SelfParam)
	c.constrain(e.Right, source, setter.Param)
	// A concretely owned parameter takes the value out of this frame, so the source
	// binding is consumed and a later use of it is a use-after-move. This mirrors
	// consumeCallArgs, since a setter write is a call on the argument side. The move
	// records against the assignment's statement, resolved from assignStmt rather than
	// c.fn.currentStmt, which inferring the receiver and source may have overwritten with
	// an inner branch statement. A rejected write records no move.
	if c.fn != nil && len(c.errs) == errsBefore && isConcreteOwned(setter.Param) {
		if ref, ok := c.fn.stmtToRef[assignStmt]; ok {
			c.consumeOwned(e.Right, source, e.Right, ref)
			c.recordEscapeSite(e.Right, ref)
		}
	}
	// The assignment evaluates to the value just written, widened the way a field write
	// widens it, so `val b = (c.x = 5)` reads `number` whether `x` is a field or a setter.
	// recordType overwrites the `undefined` recovery type inferAssign recorded on e before
	// dispatching here.
	w := widen(source)
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
	if !carrierIsVar(recv) {
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
// string-literal key, or a numeric key like {0: v}. A spread ({...o}) merges the
// operand object's fields in, following the same left-to-right rule the type-level
// operator applies. The forms it does not cover each report an UnsupportedNodeError
// and are skipped rather than panicking:
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
	// Each element contributes one field list, folded left to right by mergeSpreadOperands so a
	// later element wins over an earlier key. A property contributes its single field; a spread
	// contributes the operand object's fields, so `{...a, x: 1}` merges a's fields under x.
	operandElems := make([][]soltype.ObjTypeElem, 0, len(e.Elems))
	inexact := false
	for _, elem := range e.Elems {
		if spread, ok := elem.(*ast.ObjSpreadExpr); ok {
			op := c.inferExpr(scope, lvl, spread.Value)
			if _, errored := op.(*soltype.ErrorType); errored {
				// The operand already reported its own failure; absorb it rather than layering a
				// SpreadNotObjectError on the recovery sentinel, the object twin of the array arm.
				continue
			}
			obj, ground := newTypeEvaluator(c.ctx, newSeenPairs()).groundToObject(op)
			if !ground {
				// A spread whose operand has no ground object shape — a type variable, a
				// primitive — cannot merge its fields. Report it and keep the rest of the object.
				c.report(&SpreadNotObjectError{Spread: spread, Operand: op})
				continue
			}
			operandElems = append(operandElems, obj.Elems)
			inexact = inexact || obj.Inexact
			// Spreading an owned object moves it into the new object, the object twin of the
			// array-spread move.
			c.consumeIntoLiteral(spread.Value, op, stmtRef, hasStmtRef)
			continue
		}
		prop, ok := elem.(*ast.PropertyExpr)
		if !ok {
			// The method and constructor elements CallableExpr and ConstructorExpr arrive with
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
		// A literal property is never optional or readonly.
		operandElems = append(operandElems, []soltype.ObjTypeElem{&soltype.PropertyElem{Name: name, Type: ft}})
		// Building an owned value into the object moves it.
		c.consumeIntoLiteral(prop.Value, ft, stmtRef, hasStmtRef)
	}
	t := &soltype.ObjectType{Elems: mergeSpreadOperands(operandElems), Inexact: inexact}
	c.recordType(e, t)
	c.recordProv(t, e, ObjectField)
	return t
}

// objElemBuilder accumulates object PropertyElems under JavaScript's last-wins,
// first-position dedup: a repeated key updates the value in place at the key's
// original position, so property names stay unique — the invariant ObjectType.Prop
// and equalType rely on ({a: 1, b: 2, a: 3} ⇒ {a: 3, b: 2}). resolveObjectTypeAnn
// collects a residual-free annotation's members through it. An object literal merges
// its own operands in inferObject instead, since a literal's spread is a value.
//
// It is NOT recursive: it accumulates the direct members of ONE object level. Each
// member arrives already built, so a nested object is built by the caller's recursion
// before addElem files it.
type objElemBuilder struct {
	elems []soltype.ObjTypeElem
	pos   map[memberSlot]int // occupied access slot → index in elems
}

// memberSlot is the access a named member occupies: the name plus whether the member answers a
// read or a write. Two members collide only when they answer the same access, so a getter and a
// setter of one name coexist while a second getter replaces the first. A property answers both,
// so it collides with either half of a pair and displaces both.
type memberSlot struct {
	name  string
	write bool
}

// memberSlots returns the accesses elem answers, the keys it occupies in the builder.
func memberSlots(elem soltype.ObjTypeElem) []memberSlot {
	name := soltype.ObjElemName(elem)
	switch elem.(type) {
	case *soltype.PropertyElem:
		return []memberSlot{{name, false}, {name, true}}
	case *soltype.MethodElem, *soltype.GetterElem:
		return []memberSlot{{name, false}}
	case *soltype.SetterElem:
		return []memberSlot{{name, true}}
	}
	return nil
}

func newObjElemBuilder(capacity int) *objElemBuilder {
	return &objElemBuilder{
		elems: make([]soltype.ObjTypeElem, 0, capacity),
		pos:   make(map[memberSlot]int, capacity),
	}
}

// addElem files a named member under every access it answers, replacing whatever held those
// accesses before. The last member of a name wins and the first one's position is kept, which is
// the rule `{a: 1, b: 2, a: 3}` follows to yield `{a: 3, b: 2}` and the unique-key shape
// ObjectType.Prop and equalType rely on.
//
// Two methods of one name are the exception. They are the arms of an overload set rather than a
// redeclaration, so the later signature joins the earlier element instead of replacing it,
// matching what a class body builds through appendMethodSig.
//
// An unnamed member — a construct signature — occupies no slot and is appended as-is.
//
// The result reports whether this member displaced an earlier one, which is a redeclaration
// rather than a first declaration. A caller that treats one as an error reports it and keeps the
// collapsed list as the recovery. Merging two methods into an overload set is not a
// displacement, so it reports false.
func (b *objElemBuilder) addElem(elem soltype.ObjTypeElem) bool {
	slots := memberSlots(elem)
	if len(slots) == 0 {
		b.elems = append(b.elems, elem)
		return false
	}
	// Collect every position this member displaces. A property answers both accesses, so it can
	// displace two elements at once — the getter and the setter of a pair.
	displaced := []int{}
	for _, slot := range slots {
		i, held := b.pos[slot]
		if !held {
			continue
		}
		if incoming, isMethod := elem.(*soltype.MethodElem); isMethod {
			if existing, wasMethod := b.elems[i].(*soltype.MethodElem); wasMethod {
				existing.Signatures = append(existing.Signatures, incoming.Signatures...)
				return false
			}
		}
		if !slices.Contains(displaced, i) {
			displaced = append(displaced, i)
		}
	}
	// The earliest displaced position is the one this member inherits, keeping the first
	// occurrence's place. Any other displaced element is tombstoned, since removing it here
	// would shift every index already recorded in pos.
	at := len(b.elems)
	if len(displaced) > 0 {
		at = slices.Min(displaced)
		for _, i := range displaced {
			// Release the accesses the displaced member held. One it answered and the
			// incoming member does not must fall free rather than point at the replacement,
			// so a getter overriding a property leaves the write open for a later setter.
			for _, s := range memberSlots(b.elems[i]) {
				delete(b.pos, s)
			}
			b.elems[i] = nil
		}
		b.elems[at] = elem
	} else {
		b.elems = append(b.elems, elem)
	}
	for _, slot := range slots {
		b.pos[slot] = at
	}
	return len(displaced) > 0
}

// result returns the members in first-occurrence order, dropping the tombstones addElem leaves
// where a later member displaced an earlier one it did not take the position of.
func (b *objElemBuilder) result() []soltype.ObjTypeElem {
	out := make([]soltype.ObjTypeElem, 0, len(b.elems))
	for _, e := range b.elems {
		if e != nil {
			out = append(out, e)
		}
	}
	return out
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
	// TestBlameUndefinedSubjectFallsBackToCallSite).
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
	//   - A union receiver peels each member's borrow, so a read off a union of
	//     borrows reads each member through the union for-all rule.
	recvCarrier := readCarrier(recv)
	// A class instance, and any member that is a method or getter rather than a plain
	// field, resolves through the projected class body by direct member lookup rather
	// than the structural field-requirement below — the constraint path reads only
	// PropertyElems and cannot see a method or getter (M5 B1).
	if res, ok := c.projectedMember(lvl, blame, name, recvCarrier); ok {
		return res
	}
	// A `self` receiver inside a class body binds to the full instance object, which
	// carries method, getter, and setter members alongside fields. A read of such a
	// member resolves through member lookup here, since the structural field-requirement
	// path below reads only properties and panics on a non-property member (M5 B3).
	if res, ok := c.classBodyMember(lvl, blame, name, recv, recvCarrier); ok {
		return res
	}
	// A static read `Point.origin` resolves through member lookup here, since the
	// structural path below reads only properties and misses a static method or accessor.
	if res, ok := c.classValueMember(lvl, blame, name, recvCarrier); ok {
		return res
	}
	// A method, getter, or setter carried by a plain object type resolves here, since the
	// structural path below reads only properties. This is the annotation twin of the class
	// lookups above: an object type annotation is the one source of such an object, an object
	// literal having no syntax for these members.
	if res, ok := c.objectMember(lvl, blame, name, recvCarrier); ok {
		return res
	}
	// A generator receiver resolves through its own member list, which carries the `next`
	// method a caller advances it with. The structural path below cannot serve it. constrain
	// has no rule taking a generator to an object, so the field requirement that path builds
	// would fail on the receiver rather than read a member.
	if res, ok := c.generatorMember(lvl, blame, provNode, name, recvCarrier); ok {
		return res
	}
	// A union receiver reaches the structural requirement below, whose result is joined
	// per member inside constrain by constrainUnionFieldRead. That join runs with no
	// access to the enclosing throws sink, so the getters it may read through are
	// collected here instead, where the sink is in scope.
	c.raiseUnionAccessorThrows(lvl, blame, name, recvCarrier)
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
// a valid identifier. A dynamic key reads through the receiver's index signature,
// which dynamicIndexRead resolves.
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
	return c.dynamicIndexRead(scope, lvl, e, obj.value, objPos)
}

// dynamicIndexRead resolves `recv[k]` for a non-constant key by typing it as the indexed access
// `Recv[Kt]` and reducing it with the evaluator type-level `T[K]` uses, so `d[k]` agrees with `d.foo`.
// That reduction reads the index signature and mints both rejections. An ungrounded receiver stays
// unsupported, since the access would leave no type; reaching it needs an array or usage-inferred one.
func (c *checker) dynamicIndexRead(scope *Scope, lvl int, e *ast.IndexExpr, recv soltype.Type, objPos bool) pathResult {
	key := c.inferExpr(scope, lvl, e.Index)
	access := &soltype.IndexType{Target: recv, Index: key}
	reduced, reduceErrs, ok := c.ctx.reduceResidual(access, newSeenPairs())
	if len(reduceErrs) > 0 {
		c.blameConstraintErrors(e, reduceErrs)
		return pathResult{err: true}
	}
	if !ok {
		c.reportUnsupported(e)
		return pathResult{err: true}
	}
	if !objPos {
		c.recordMemberUse(e)
	}
	c.recordType(e, reduced)
	return pathResult{value: reduced}
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
// `await Promise<Promise<T>>` yields `Promise<T>` (Awaited<T> is M9). An await is
// also an exceptional exit: the requirement's rejection slot is the enclosing body's
// throws sink, so what the promise rejects with reaches that body's rejection. `await`
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
	// The requirement's Err slot is this body's throws sink, so constrain's covariant
	// rejection rule records what the awaited promise rejects with into it. A
	// non-rejecting promise carries `never` and records nothing. No raised flag is
	// tracked here: it feeds the unused-clause warning, which measures a `throws`
	// clause, and the async body an await sits in cannot have one.
	//
	// PR8: a failed argument is the ErrorType recovery placeholder, which absorbs in
	// constrain, so `<unknown> <: Promise<U>` no longer cascades a spurious second
	// diagnostic — res then stays unbound and coalesces to `never`, the right
	// recovery for awaiting something broken. The M2-era isRecoveryPlaceholder guard
	// this site used is gone.
	c.constrain(e, arg, &soltype.PromiseType{Inner: res, Err: c.throwsSink(lvl)})
	c.recordType(e, res)
	return res
}

// inferThrow types `throw e`. The thrown type is constrained into the enclosing body's
// throws sink, the way a `return` reaches the return type. It is `never`, so a `throw`
// sits where a value is expected: `return if ok { v } else { throw Error("no value") }`.
func (c *checker) inferThrow(scope *Scope, lvl int, e *ast.ThrowExpr) soltype.Type {
	arg := c.inferExpr(scope, lvl, e.Arg)
	c.constrain(e, arg, c.throwsSink(lvl))
	c.markRaised()
	// Recorded but given no provenance: every `&NeverType{}` shares one address, and Prov
	// is pointer-keyed, so a second throw would trip recordProv's guard. Info is node-keyed.
	t := &soltype.NeverType{}
	c.recordType(e, t)
	return t
}

// inferYield types `yield e` and `yield from g`. A yield outside a generator body is a
// WALK rejection symmetric to AwaitOutsideAsyncError: the operand is still walked so its
// own errors surface, and the enclosing function is related as the one to mark `gen`. A
// closure opens its own funcCtx with `gen` clear, so a `yield` inside one is rejected
// even within a generator, matching JavaScript.
//
// `yield e` constrains its operand into the yield sink and evaluates to the `Next` type,
// the value a caller sends back in through `next(v)`. A bare `yield` yields `undefined`.
//
// `yield from g` forwards the delegate's yields into the sink and evaluates to the
// delegate's return type, what the delegating generator sees once it is exhausted.
func (c *checker) inferYield(scope *Scope, lvl int, e *ast.YieldExpr) soltype.Type {
	if c.fn == nil || !c.fn.gen {
		if e.Value != nil {
			c.inferExpr(scope, lvl, e.Value) // surface operand-side errors anyway
		}
		// When the yield sits in a non-generator function, point Related() at that
		// function — it is the one to mark `gen`. At module top-level there is no
		// enclosing function, so EnclosingFn stays nil and Related() is empty.
		var enclosing ast.Node
		if c.fn != nil {
			enclosing = c.fn.node
		}
		// report returns the ErrorType recovery placeholder, so the rejected yield
		// never cascades a downstream failure on top of this error.
		t := c.report(&YieldOutsideGenError{Yield: e, EnclosingFn: enclosing})
		c.recordType(e, t)
		return t
	}
	// Recorded before the two forms split, so delegating counts as yielding: a body whose
	// only yield is a `yield from` does produce values, and its `gen` marker is used.
	c.fn.yielded = true
	if e.IsDelegate {
		// A `yield from` with no operand is a parse error the parser already reported;
		// recover with the error placeholder rather than walking nothing.
		if e.Value == nil {
			t := &soltype.ErrorType{}
			c.recordType(e, t)
			return t
		}
		arg := c.inferExpr(scope, lvl, e.Value)
		// Delegating advances the delegate, so what it raises reaches this body's sink
		// exactly as iterating it would.
		c.constrainIterationRaise(e.Value, arg, lvl)
		elem, res, delegateNext, ok := c.delegateElemType(arg)
		if !ok {
			// A delegate with no structure is the recursive case: `gen fn f() { yield from
			// f() }` reaches the delegation while f's own return is unsolved. State the
			// requirement instead of reading one, the way inferAwait constrains against
			// `Promise<U>`. Yield is covariant, so the delegate's yields land in this
			// body's sink and Ret is what the delegation produces.
			if c.delegateIsUnsolved(arg) {
				res := c.freshAt(lvl)
				req := &soltype.GeneratorType{Yield: c.fn.yields, Ret: res, Next: c.fn.yieldNext, Async: c.fn.async}
				c.constrain(e, arg, req)
				c.recordType(e, res)
				return res
			}
			// A delegate that already failed to infer is the ErrorType recovery
			// placeholder; it absorbs rather than cascading a second diagnostic, the
			// same rule inferForIn applies to a broken iterable.
			var t soltype.Type = &soltype.ErrorType{}
			if _, brokenDelegate := soltype.CarrierOf(arg).(*soltype.ErrorType); !brokenDelegate {
				t = c.report(&NotIterableError{Iterable: e.Value, Type: arg, Await: false})
			}
			c.recordType(e, t)
			return t
		}
		c.constrain(e, elem, c.fn.yields)
		// Delegating forwards whatever a caller sends into the delegate, so this body
		// can only accept what the delegate accepts. A declared Next is checked against
		// the delegate's here, at the delegation. An inferred one collects the
		// delegate's instead, and inferFunc meets everything collected to get a Next
		// narrow enough for every delegate.
		if delegateNext != nil {
			if c.fn.nextDeclared {
				c.constrain(e, c.fn.yieldNext, delegateNext)
			} else {
				c.fn.delegateNexts = append(c.fn.delegateNexts, delegateNext)
			}
		}
		c.recordType(e, res)
		return res
	}
	var val soltype.Type = &soltype.UndefinedType{}
	if e.Value != nil {
		val = c.inferExpr(scope, lvl, e.Value)
	}
	c.constrain(e, val, c.fn.yields)
	// Recorded but given no provenance: the Next type is one shared value seeded on the
	// funcCtx, and Prov is pointer-keyed, so a second yield would trip recordProv's
	// guard. Info is node-keyed.
	t := c.fn.yieldNext
	c.recordType(e, t)
	return t
}

// delegateIsUnsolved reports whether a `yield from` delegate is an inference variable
// the solve has not shaped, so the delegation must state its requirement rather than
// read one. A failed delegate is the ErrorType placeholder, not a variable, so it is
// excluded and keeps absorbing.
func (c *checker) delegateIsUnsolved(t soltype.Type) bool {
	return carrierIsVar(t)
}

// delegateElemType resolves what a `yield from` delegate hands back:
//
//   - the element type its iteration yields
//   - the value the delegation evaluates to once the delegate is exhausted
//   - the delegate's Next slot, what it accepts from a sent value
//
// A generator forwards its Yield, Ret, and Next slots, and an async one is a legal
// delegate only from an async generator body. Any other operand goes through
// syncElemType, where a tuple carries no return value and so finishes with `undefined`,
// and no Next slot at all, reported as a nil third result.
//
// A union resolves each branch the same way. Yield and Ret are covariant, so those
// results union. Next is contravariant and the delegation forwards into whichever
// branch is live, so the Next results meet instead. Branches with no Next slot put no
// requirement on the delegator and drop out of that meet.
func (c *checker) delegateElemType(t soltype.Type) (soltype.Type, soltype.Type, soltype.Type, bool) {
	carrier := groundedCarrier(t)
	if u, isUnion := carrier.(*soltype.UnionType); isUnion {
		// syncElemType walks a union too, but reports only element types. Recursing here
		// keeps each branch's Ret slot and lets an async branch through under the same
		// async-body rule a lone async delegate gets. A union is a legal delegate only
		// when every branch is. Inexactness carries to both results, since the unlisted
		// branches yield and return something unknown.
		elems := make([]soltype.Type, 0, len(u.Types))
		rets := make([]soltype.Type, 0, len(u.Types))
		nexts := make([]soltype.Type, 0, len(u.Types))
		for _, branch := range u.Types {
			elem, ret, next, ok := c.delegateElemType(branch)
			if !ok {
				return nil, nil, nil, false
			}
			elems = append(elems, elem)
			rets = append(rets, ret)
			if next != nil {
				nexts = append(nexts, next)
			}
		}
		return newUnion(c.ctx, elems, u.Inexact), newUnion(c.ctx, rets, u.Inexact), c.meetNexts(nexts), true
	}
	if g, isGen := carrier.(*soltype.GeneratorType); isGen {
		if g.Async && (c.fn == nil || !c.fn.async) {
			return nil, nil, nil, false
		}
		return g.Yield, g.Ret, g.Next, true
	}
	elem, ok := c.syncElemType(t)
	if !ok {
		return nil, nil, nil, false
	}
	return elem, &soltype.UndefinedType{}, nil, true
}

// meetNexts combines the Next slots a generator must satisfy at once into the single
// type its own Next slot carries. An empty list gives nil, which callers read as "no
// requirement" and answer with their own default.
func (c *checker) meetNexts(nexts []soltype.Type) soltype.Type {
	switch len(nexts) {
	case 0:
		return nil
	case 1:
		return nexts[0]
	default:
		return newIntersection(c.ctx, nexts)
	}
}

// inferIfElse types `if cond { cons } else { alt }`. The condition is
// constrained `<: boolean`; each branch is typed (an empty / missing else
// contributes `undefined`); the result is a fresh join var with each NON-DIVERGING
// branch as a lower bound, so the result coalesces to the union of the branches
// that can actually produce a value.
//
// Diverging branches contribute `never`. A branch that always exits before its tail,
// through a trailing `return` or `throw`, can never be the path that yields the if's
// value, so it drops out of the branch union rather than leaking its operand.
// `val x = if c { return 1 } else { "y" }` is `"y"`, not `1 | "y"`, and when both
// branches diverge the if's value coalesces to `never`. blockDiverges decides which
// branches those are.
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
	var altT soltype.Type = &soltype.UndefinedType{}
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
	var branches []soltype.Type
	if !consDiverges {
		c.constrain(e, consT, res)
		branches = append(branches, consT)
	}
	if !altDiverges {
		c.constrain(e, altT, res)
		branches = append(branches, altT)
	}
	c.checkUniformOwnership(e, branches)
	c.recordType(e, res)
	return res
}

// inferIfVal types `if val pat = target { cons }` with an optional `else { alt }` off the
// UCS normalized form. The target is inferred once, then ucs.DesugarIfVal and
// ucs.Normalize lower the form into a split over that one value and condWalk types the
// result.
//
// The pattern's names are bound ONLY in the consequent, at the narrowed member type. The
// alternate is the split's fallthrough, which the walk types in the scope the form
// started in, so it reads the scrutinee at its full type and never sees those names. Each
// non-diverging half constrains into one fresh branch-join var, exactly as inferIfElse
// joins its two branches.
func (c *checker) inferIfVal(scope *Scope, lvl int, e *ast.IfValExpr) soltype.Type {
	scrutinee := c.inferExpr(scope, lvl, e.Target)
	res := c.freshAt(lvl)
	c.recordProv(res, e, IfValBranch)
	core := ucs.DesugarIfVal(e)
	// The binder is seeded with the type inferred above, so the walk never re-infers the
	// target and a side-effecting one such as `if val x = f() { … }` runs its call once.
	binder := c.newPathBinder(lvl, e, core.Scrutinee, scrutinee)
	w := c.walkCond(scope, lvl, e, core, binder, res)
	// The two halves join into one value, so they have to agree on ownership, exactly as
	// inferIfElse's do. A diverging half produces no value and is left out of the check.
	c.checkUniformOwnership(e, w.bodies)
	c.recordType(e, res)
	return res
}

// bindRefutable binds a refutable pattern's names against a scrutinee, returning the type
// it bound at. The typing walk calls it for a leaf under an annotation test. It holds the
// parts of a refutable binding the IR does not model: the narrowing rule, the leaf's
// VarID, and the caller's own scope.
//
// ann is the narrowing annotation the surface wrote. A `match` arm and an `if val` write it
// on the pattern, and a `val … else` on the declaration. The IR carries it on the branch's
// test and passes it here, so no caller has to know which node holds it. A bare identifier
// binds at the type the annotation names, through bindNarrowedIdent.
//
// Any other pattern destructures through the shared structural path. Normalization mints
// no annotation test over such a pattern, so that arm recovers from a lowering that pairs
// one with a pattern anyway.
func (c *checker) bindRefutable(
	scope *Scope, lvl int, pat ast.Pat, ann ast.TypeAnn, scrutinee soltype.Type,
) soltype.Type {
	if ip, ok := pat.(*ast.IdentPat); ok && ann != nil {
		return c.bindNarrowedIdent(scope, lvl, ip, ann, scrutinee)
	}
	c.bindPattern(scope, lvl, pat, scrutinee, nil)
	return scrutinee
}

// bindNarrowedIdent binds a single identifier at the type its narrowing annotation `ann`
// names, and returns it. It is the shared core of the refutable identifier-narrowing path.
// All three refutable forms pass the annotation in directly, a `match` arm and an `if val`
// from the pattern and a `val … else` from the decl, so the annotation never moves between
// AST nodes.
//
// The annotation does two jobs and they have different answers. It tests the value, which is
// what decides whether the branch runs, and it declares the binding's type, which is the
// annotation itself. `x: number => x` over a `1 | 2` runs for both members and binds `x` at
// `number`, so an arm returning `x` contributes `number`. That matches the ordinary
// declaration rule, where `val x: number = 5` types `x` as `number` rather than as `5`.
// admitsPartOf answers only the first job, so it gates the constraint below and never the
// type the name takes.
func (c *checker) bindNarrowedIdent(scope *Scope, lvl int, ip *ast.IdentPat, ann ast.TypeAnn, scrutinee soltype.Type) soltype.Type {
	narrowed, resolved := c.resolveTypeAnn(scope, ann, lvl)
	switch {
	case !resolved:
		// The annotation was unsupported and already reported. Bind the name to the
		// whole scrutinee so the body still type-checks against a real type rather
		// than cascading a second error off a `never` placeholder.
		narrowed = scrutinee
	case c.admitsPartOf(scrutinee, narrowed):
		// Some value of the scrutinee fits the annotation, so the test can pass and nothing
		// needs constraining. The name keeps the annotated type set below, which is what the
		// test proved about every value reaching the body.
	default:
		// The annotation admits nothing the scrutinee holds. It may still name something
		// narrower than a member, the `1` of `if val x: 1 = u` over a `number | string`, so
		// the union-super exists rule decides. A `number` over a `string` fails it and is
		// reported here.
		c.constrain(ip, narrowed, scrutinee)
	}
	binding := ValueBinding{Schemes: []TypeScheme{monoScheme(narrowed)}}
	// Carry the rename-assigned VarID onto the binding so a later closure capture or
	// alias-set check resolves this name, matching the ordinary body-level decl path.
	if ip.VarID > 0 {
		binding.VarID = ip.VarID
	}
	scope.defineValue(ip.Name, binding)
	c.recordType(ip, narrowed)
	return narrowed
}

// admitsPartOf reports whether some value of the scrutinee fits the annotation, so the
// branch's test can pass and the binding it introduces is reachable.
//
// A union is answered member by member, since a value of the union is a value of one member.
// `x: number` over a `1 | 2 | string` finds `1`, so the arm can run. A union reached through
// a transparent alias is expanded first, the same unfold constrain performs, so
// `type U = 1 | 2 | string` answers the way the union written inline does.
//
// Two shapes answer false and take the caller's fallback. An annotation naming something
// narrower than every member fits none of them, which is the `1` of `x: 1` over a
// `number | string`, and the union-super exists rule accepts it instead. An unsolved
// scrutinee has no members to test at all, and trialling one would record a bound the probe
// then rolls back.
func (c *checker) admitsPartOf(scrutinee, ann soltype.Type) bool {
	if carrierIsVar(scrutinee) {
		return false
	}
	inner := c.expandAliasChain(soltype.CarrierOf(scrutinee))
	u, isUnion := inner.(*soltype.UnionType)
	if !isUnion {
		return c.typeAdmits(ann, inner)
	}
	// An inexact union's open tail holds values no listed member describes, so finding no
	// member proves nothing about the tail. That is why a false answer only sends the caller
	// to the union-super exists rule, which accepts through the tail, rather than reporting.
	for _, member := range u.Types {
		if c.typeAdmits(ann, member) {
			return true
		}
	}
	return false
}

// typeAdmits reports whether every value of t fits ann, which is the question the runtime
// test of a narrowing annotation answers. The trial runs under its own probe and returns its
// failures rather than reporting them, so a false answer leaves nothing behind.
func (c *checker) typeAdmits(ann, t soltype.Type) bool {
	return !hasHardError(c.ctx.trialUnderProbe(t, ann))
}

// inferValElse types a `val pat = init else { … }` binding off the UCS normalized form.
// The initializer is inferred once, then ucs.DesugarValElse and ucs.Normalize lower the
// declaration into a split whose one branch is the success path and whose fallthrough is
// the `else`, and condWalk types the result.
//
// The success path ends in the binding-escape leaf, which carries no body because the rest
// of the block is its continuation. Its leaves are installed in the block's scope once the
// walk is done. The `else` runs precisely when the pattern bound none of them, so it is
// typed first and never sees them.
//
// A non-diverging `else` supplies the value the declaration binds when the pattern did not
// match. An annotation pins the binding's type, so that value has to fit it. Otherwise
// joinFallbackLeaves takes the value apart with the same pattern and joins each leaf.
func (c *checker) inferValElse(scope *Scope, lvl int, d *ast.VarDecl) {
	core, ok := ucs.DesugarValElse(d)
	if !ok {
		// A declaration the parser left without an initializer. There is no value to bind
		// the pattern against and no failure for the `else` to cover.
		c.reportUnsupported(d)
		return
	}
	initType := c.inferExpr(scope, lvl, d.Init)
	// A `val … else` annotation lives on the declaration. A bare identifier narrows to it
	// through the split's annotation test. A destructuring pattern cannot distribute the
	// annotation across its leaves, as in `val [a, b]: [number, string] = u else { … }`, so
	// the lowering leaves that annotation out of the IR and it is reported here.
	_, identPat := d.Pattern.(*ast.IdentPat)
	if d.TypeAnn != nil && !identPat {
		c.reportUnsupportedFeature(d.Pattern, "narrowing type annotation on a destructuring pattern")
	}

	// The binder is seeded with the type inferred above, so the walk never re-infers the
	// initializer and a side-effecting one such as `val x = f() else { … }` runs its call
	// once. It holds the initializer alone, so each split's tag test narrows it and a leaf
	// reads only the members the pattern matched.
	binder := c.newPathBinder(lvl, d, core.Scrutinee, initType)
	// A `val … else` holds no arm body, so the walk joins nothing and takes no branch-join
	// var.
	w := c.walkCond(scope, lvl, d, core, binder, nil)
	// A diverging `else` produces no fallback value, so the leaves read the narrowed
	// initializer alone.
	if elseT, produces := w.fallback(); produces {
		if w.narrowed != nil {
			// A narrowing annotation fixes the binding's type on its own, so the fallback has
			// to fit that type. Such a declaration narrows a bare identifier, so it holds no
			// leaf below the name to join at.
			c.constrain(d, elseT, w.narrowed)
		} else {
			c.joinFallbackLeaves(scope, lvl, d, w.escaped, elseT)
		}
	}
	installEscaped(scope, w.escaped)
}

// fallbackLeaf is what the `else`'s fallback supplies for one name a `val … else` binds:
// the type projected out of the fallback, and the pattern node that named the leaf.
type fallbackLeaf struct {
	ty   soltype.Type
	node ast.Node
}

// joinFallbackLeaves rebinds each leaf the success path of a `val pat = init else { … }`
// bound, to the join of that leaf with the fallback's. escaped is the innermost scope the
// success path bound into, and fallback the type the `else` produced.
//
// The join sits BELOW the projection, one var per leaf. `val {x} = p else { {x: "s"} }`
// over `p: {x: number} | {y: string}` binds `x` at `number | "s"`. Joining above it would
// leave `{y: string}` in the union the leaf reads, since no tag test admitted the fallback
// and nothing may narrow a union holding it, and `x` would pick up that member's
// `undefined`.
//
// The second walk is a projection rather than a binding, so nothing a leaf carries is
// applied twice. See bindPurpose. It still checks the fallback against the pattern, which
// is what reports the missing `x` of `val {x} = p else { {y: 1} }`.
//
// Its requirements go against a fresh var carrying the fallback rather than against the
// fallback itself. One emitted straight against the fallback is anchored to the pattern
// leaf, and the `5` of `val {x} = p else { 5 }` would then underline the `x`.
func (c *checker) joinFallbackLeaves(scope *Scope, lvl int, d *ast.VarDecl, escaped *Scope, fallback soltype.Type) {
	produced := c.freshAt(lvl)
	leaves := map[string]fallbackLeaf{}
	// The fallback doubles as the shape, since it has not flowed into `produced` yet and a
	// `...rest` leaf reads its leftover members off a shape. It is passed unpeeled, so
	// projectLeaves reads the borrow off it and each leaf of a borrowed fallback is itself a
	// borrow. Peeling here instead would hand projectLeaves an owned carrier and every leaf
	// would bind owned however the `else` was written.
	c.projectLeaves(scope.Child(), lvl, d.Pattern, produced, fallback,
		func(_ *Scope, name string, t soltype.Type, node ast.Node) {
			leaves[name] = fallbackLeaf{ty: t, node: node}
		})
	// The requirements the projection emitted are upper bounds on `produced`. Pushing the
	// fallback in only now is what checks it against them.
	//
	// A borrowed fallback is peeled first, the same way a scrutinee is. The requirements a
	// pattern emits are owned, so pushing the borrow itself in would read as a borrow escaping
	// into an owned destination. The borrow is not lost by the peel: the leaves above already
	// took it off the fallback's binding mode.
	carrier, _ := c.scrutineeBinding(lvl, fallback)
	c.constrain(d, carrier, produced)
	for s := escaped; s != nil && s != scope; s = s.parent {
		for name, binding := range s.bindings() {
			if leaf, projected := leaves[name]; projected {
				c.joinLeaf(s, lvl, d, name, binding, leaf)
			}
		}
	}
}

// joinLeaf folds what the fallback supplies for one name into what the success path bound
// it to. binding is that success-path binding and leaf the fallback's projection. scope is
// where the name was bound, which is where a rebind lands. Every constraint is anchored to
// d, the whole declaration.
//
// A leaf whose type is already settled takes no join, and the fallback flows into that type
// instead. leafFixedType decides which leaves those are.
//
// The two halves have to agree on ownership, so a leaf of a borrowed scrutinee joined with
// an owned fallback reports MixedOwnershipError at the declaration. Without that check the
// name binds a union of a borrow and a plain value, and the author meets the mismatch only at
// a later write through it.
//
// The join does not route through joinBorrows the way joinReturnPoints does. joinBorrows
// applies only where every input is a mutable borrow of a grounded object, and the fallback's
// leaf is always the raw variable projectLeaves lowered the field into. That walk projects
// rather than binds, so it never reaches applyBindMode and never wraps a leaf in a borrow.
// Two borrowed leaves therefore stay un-joined, as `&'c {a: number} | &'d {a: number}`.
func (c *checker) joinLeaf(
	scope *Scope, lvl int, d *ast.VarDecl, name string, binding ValueBinding, leaf fallbackLeaf,
) {
	matched, mono := monoTypeOf(binding)
	if !mono {
		return
	}
	if fixed, ok := leafFixedType(matched, leaf.node); ok {
		c.constrain(d, leaf.ty, fixed)
		return
	}
	// The two sources the name binds from have to agree on ownership.
	c.checkUniformOwnership(d, []soltype.Type{matched, leaf.ty})
	join := c.joinBranches(d, lvl, ValElseBranch, []soltype.Type{matched, leaf.ty})
	binding.Schemes = []TypeScheme{monoScheme(join)}
	scope.defineValue(name, binding)
	// The join replaces what the binding walk recorded, the leaf projected off the
	// initializer alone, so an editor reads the type the name really binds at.
	c.recordType(leaf.node, join)
}

// leafFixedType returns the type a leaf's fallback has to fit when the leaf takes no join,
// and false when the leaf joins its two sources instead. matched is what the success path
// bound the leaf to, node the leaf's pattern.
//
// Two leaves take no join. An owned `mut` leaf is a cell the block writes through, so the
// fallback flows into its contents. A union of that cell and a plain value would reject
// `x.a = 2`. An annotated leaf already has the annotation as its type, so the fallback has
// to fit it. `val {x::number} = p else { {x: "s"} }` reports the `"s"`.
//
// An unannotated leaf of a borrowed scrutinee is neither. It names a place inside the
// initializer, where the fallback's fresh value does not live, so the two join and a write
// through the union is rejected. An annotated leaf of that same scrutinee is no borrow at
// all, since concreteLeaf drops the shape hint applyBindMode needs to wrap one.
func leafFixedType(matched soltype.Type, node ast.Node) (soltype.Type, bool) {
	if ref, isRef := matched.(*soltype.RefType); isRef && ref.Mut && ref.Lt == nil {
		return ref.Inner, true
	}
	if leafTypeAnn(node) != nil {
		return matched, true
	}
	return nil, false
}

// leafTypeAnn returns the type annotation a pattern leaf carries, and nil for one carrying
// none. A tuple element and an object key-value's value hang it off the identifier, an
// object shorthand off itself.
func leafTypeAnn(node ast.Node) ast.TypeAnn {
	switch n := node.(type) {
	case *ast.IdentPat:
		return n.TypeAnn
	case *ast.ObjShorthandPat:
		return n.TypeAnn
	default:
		return nil
	}
}

// monoTypeOf returns the one non-generalized type a binding holds. Every leaf a pattern
// binds is such a binding, and an overload set or a generalized scheme returns false.
func monoTypeOf(b ValueBinding) (soltype.Type, bool) {
	if len(b.Schemes) != 1 {
		return nil, false
	}
	mono, ok := b.Schemes[0].(*MonoScheme)
	if !ok {
		return nil, false
	}
	return mono.Ty, true
}

// inferMatch types a `match` expression off the UCS normalized form. The scrutinee is
// inferred once, then the arms are lowered by ucs.DesugarMatch and ucs.Normalize into
// splits over that one value, and condWalk types the result. Each split narrows the
// scrutinee by the tag its branch tests, each leaf of a matched pattern binds against
// the projection the IR names for it, and an optional `if` guard is typed as a boolean
// over those leaves. Every non-diverging arm body is constrained into one fresh
// branch-join var, exactly as inferIfElse joins its two branches. A diverging arm
// contributes `never`, so when every arm diverges the result coalesces to `never`.
//
// Exhaustiveness is checked off the same normalized form by checkCondExhaustive.
func (c *checker) inferMatch(scope *Scope, lvl int, e *ast.MatchExpr) soltype.Type {
	scrutinee := c.inferExpr(scope, lvl, e.Target)
	// Snapshot the scrutinee before any arm binds. A literal pattern adds its literal as a
	// lower bound, which would otherwise leak a phantom member into the coalesced union read
	// after the walk. Both the coverage check and the arms below a catch-all read their union
	// structure off this. The borrow stays on the snapshot rather than being peeled the way
	// groundedCarrier peels one, since narrowArmScrutinee rewraps what it narrows and
	// checkCondExhaustive reads its own carrier.
	matchShape := scrutinee
	if carrierIsVar(scrutinee) {
		matchShape = coalesce(scrutinee, soltype.Positive)
	}
	res := c.freshAt(lvl)
	c.recordProv(res, e, MatchBranch)
	core := ucs.DesugarMatch(e)
	// The binder is seeded with the type inferred above, so the walk never re-infers the
	// target and a side-effecting one such as `match f() { … }` runs its call once.
	binder := c.newPathBinder(lvl, e, core.Scrutinee, scrutinee)
	w := c.walkCond(scope, lvl, e, core, binder, res)
	// An arm below an unguarded catch-all can never run, so normalization leaves it out of
	// the split and the walk never reaches it. Report each one, then type it anyway through
	// the same per-arm path a `try`'s catch clauses take, so a fault inside dead code is
	// found rather than left for whoever deletes the arm above it.
	//
	// Its value is not one the match produces, so it joins neither res nor the ownership
	// check. Both would report against a value no execution reaches, on top of the
	// diagnostic that already names the arm.
	unreachable := c.reportUnreachableArms(e.Cases, w.arms)
	c.inferMatchArms(scope, lvl, e, unreachable, matchShape, scrutinee, c.freshAt(lvl))
	c.checkUniformOwnership(e, w.bodies)
	c.checkCondExhaustive(scope, lvl, w.norm, matchShape)
	c.recordType(e, res)
	return res
}

// reportUnreachableArms reports every untyped arm an unguarded catch-all above it covers,
// and returns all the untyped arms in source order so the caller can still type them.
//
// A split ends at the first arm with no tag to test, which is what keeps the arms below it
// out of the form the walk sees. The last arm the walk typed is the one they sit below,
// and the message points at it.
//
// Only a catch-all covers them. A bare `...rest` ends a split too and is already reported
// unsupported, so calling the arms below it unreachable would add advice that does not fit.
func (c *checker) reportUnreachableArms(arms []*ast.MatchCase, walked set.Set[ucs.Spanned]) []*ast.MatchCase {
	var covering *ast.MatchCase
	var unreachable []*ast.MatchCase
	for _, arm := range arms {
		if walked.Contains(arm) {
			covering = arm
			continue
		}
		unreachable = append(unreachable, arm)
		if covering != nil && covering.Guard == nil && ast.IsCatchAllPat(covering.Pattern) {
			c.report(&UnreachableMatchArmError{Arm: arm, Covering: covering})
		}
	}
	return unreachable
}

// inferMatchArms types each arm of a `try`'s catch clause and each unreachable arm of a
// `match`, constrains every non-diverging body into res, and returns those bodies for the
// caller's ownership check. Each arm binds in a fresh child scope, so a name one arm binds is
// invisible to the next, and node is blamed for each body's join. shape is the coalesced
// scrutinee narrowArmScrutinee reads union members from. scrutinee is what an arm binds
// against when no narrowing applies, so a var's identity and borrow survive there. inferMatch
// passes a snapshot and the original, inferTryCatch one already-concrete type for both.
//
// The reachable arms of a `match` are typed by condWalk instead, off the normalized form.
func (c *checker) inferMatchArms(
	scope *Scope, lvl int, node ast.Node, arms []*ast.MatchCase, shape, scrutinee, res soltype.Type,
) []soltype.Type {
	var bodies []soltype.Type
	for _, arm := range arms {
		armScope := scope.Child()
		armScrut := narrowArmScrutinee(shape, scrutinee, arm.Pattern)
		// A top-level annotation narrows the arm rather than asserting against what it binds,
		// so it goes through bindRefutable, the same helper the walk's live arms use. Binding
		// it as an ordinary pattern would assert the annotation on the whole scrutinee and
		// report `cannot constrain string <: number` for an `x: number` arm over a
		// `number | string`. That is a second diagnostic on an arm already reported dead.
		// Every other pattern form has no such annotation and destructures unchanged.
		c.bindRefutable(armScope, lvl, arm.Pattern, ucs.PatternNarrowingAnn(arm.Pattern), armScrut)
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
			c.constrain(node, bodyT, res)
			bodies = append(bodies, bodyT)
		}
	}
	return bodies
}

// inferTryCatch types `try { … } catch { pat => body, … }`. The try block is walked
// against a nested throws sink, a fresh variable installed over the enclosing body's. Every
// `throw` and every call inside the block therefore records into that variable rather than
// into the function's own clause. The catch arms match against what the variable collected,
// the way `match` arms match a scrutinee. rethrowUnhandled then sends whatever the arms
// leave over to the enclosing sink.
//
// The form's value is the join of the try block's tail value and each non-diverging arm
// body, the same branch-join inferMatch builds from its arms.
func (c *checker) inferTryCatch(scope *Scope, lvl int, e *ast.TryCatchExpr) soltype.Type {
	// A `try` with no arms catches nothing, so it is rejected and the block recovers as if
	// written on its own. Its throws reach the enclosing clause unchanged, which keeps the
	// fault to one diagnostic. An omitted `catch` and a written `catch { }` both land here.
	if len(e.Catch) == 0 {
		c.report(&MissingCatchArmError{Try: e})
		t, _ := c.inferBlock(scope.Child(), lvl, &e.Try)
		c.recordType(e, t)
		return t
	}

	res := c.freshAt(lvl)
	c.recordProv(res, e, TryCatchBranch)
	// The enclosing sink is read before the nested one is installed, so the rethrow below
	// reaches the clause of the function this `try` sits in.
	enclosing := c.throwsSink(lvl)
	collected := c.freshThrowsSink(lvl)
	c.recordProv(collected, e, CaughtThrows)

	// branches collects every value the form can produce, so the ownership check below sees
	// the try block's tail alongside the arm bodies. A diverging branch produces no value
	// and is left out of both the join and the check.
	var branches []soltype.Type
	// This call is what fills `collected`. Nothing assigns to it: walkTryBlock installs it as
	// the sink throwsSink returns, so each `throw` and each call inside the block constrains
	// into it and records a lower bound.
	tryT, tryDiverges := c.walkTryBlock(scope, lvl, &e.Try, collected)
	if !tryDiverges {
		c.constrain(e, tryT, res)
		branches = append(branches, tryT)
	}

	// The caught type is this walk's scrutinee, and the arms are typed exactly as a `match`
	// expression's are. It is already concrete, so it serves as both the narrowing shape and
	// the bind target.
	caught := c.caughtType(collected)
	branches = append(branches, c.inferMatchArms(scope, lvl, e, e.Catch, caught, caught, res)...)
	c.checkUniformOwnership(e, branches)
	c.rethrowUnhandled(scope, e, caught, enclosing)
	c.recordType(e, res)
	return res
}

// walkTryBlock walks a try block against sink, then restores the sink it replaced, so a
// `try` nested in another sends its own leftovers to the outer arms. installThrowsSink
// picks the field, so a top-level `try` collects into the checker and one in a body into
// that body's funcCtx. funcCtx.raised is cleared alongside, and rethrowUnhandled sets it
// again only when something escapes, so a fully covered `try` leaves a clause unused.
func (c *checker) walkTryBlock(scope *Scope, lvl int, b *ast.Block, sink soltype.Type) (soltype.Type, bool) {
	savedThrows := c.installThrowsSink(sink)
	savedRaised := false
	if c.fn != nil {
		savedRaised, c.fn.raised = c.fn.raised, false
	}
	t, diverges := c.inferBlock(scope.Child(), lvl, b)
	c.installThrowsSink(savedThrows)
	if c.fn != nil {
		c.fn.raised = savedRaised
	}
	return t, diverges
}

// caughtType returns the type a catch arm's pattern binds against: what the try block
// raised, reopened with a trailing `...`. The tail is there because a clause is a floor
// rather than a ceiling, so a call can raise something its signature did not name. A block
// with no known exceptional exit yields `unknown`, the tail on its own, since an inexact
// union with no members carries no value to bind against.
func (c *checker) caughtType(collected soltype.Type) soltype.Type {
	shape := coalesce(collected, soltype.Positive)
	if isNeverType(shape) {
		return &soltype.UnknownType{}
	}
	return newUnion(c.ctx, []soltype.Type{shape}, true)
}

// rethrowUnhandled sends the part of the caught union no arm covers into the enclosing
// throws sink. A value matching no arm is re-raised at runtime, so uncovered members draw a
// rethrow rather than the non-exhaustiveness error the equivalent `match` would draw.
//
// What escapes is the set difference `caught ∩ ¬handled`, where handled is the type the arms
// catch. The difference is taken one member at a time, which is exact because meeting a
// complement distributes over a union: `(A | B) ∩ ¬H` is `(A ∩ ¬H) | (B ∩ ¬H)`. A member
// survives when the meet still holds a value, which memberCaught decides.
//
// A guarded arm can fail its guard, so it catches nothing and contributes nothing to handled.
// An unguarded catch-all catches every value and is answered before the difference is taken.
// Only the MEMBERS are rethrown: every throws type is open already, and only `unknown` could
// carry the tail, so adding it would erase the named types the clause had.
func (c *checker) rethrowUnhandled(scope *Scope, e *ast.TryCatchExpr, caught, enclosing soltype.Type) {
	if ast.HasUnguardedCatchAll(e.Catch) {
		return
	}
	handled := c.armsCatchType(scope, e.Catch)
	// A non-union `caught` carries no named member to rethrow. It is `unknown`, the bare
	// open tail caughtType renders for a block that raises nothing known, or the ErrorType
	// recovery placeholder. Neither leaves anything for the enclosing clause to record.
	var rethrown soltype.Type = &soltype.NeverType{}
	if u, isUnion := caught.(*soltype.UnionType); isUnion {
		uncovered := make([]soltype.Type, 0, len(u.Types))
		for _, m := range u.Types {
			for _, part := range c.caughtMembers(m) {
				if c.memberCaught(part, handled, e.Catch) {
					continue
				}
				uncovered = append(uncovered, part)
			}
		}
		rethrown = newUnion(c.ctx, uncovered, false)
	}
	if isNeverType(rethrown) {
		// Every member an arm could name is covered. Codegen still emits a runtime rethrow
		// for a value that matches no arm, but such a value came from the open tail, so no
		// signature named its type. Recording it would mean widening the clause to
		// `unknown` to state what is already true of every clause.
		return
	}
	// The engine runs directly rather than through c.constrain, so its errors are dropped
	// in favour of one UnhandledRethrowError blamed here. A member's Prov entry is the
	// `throw` that produced it inside the block, and blaming that `throw` would be wrong:
	// the `try` caught it. What fails is the re-raise, which happens at this node.
	if errs := c.ctx.Constrain(rethrown, enclosing); len(errs) > 0 {
		c.report(&UnhandledRethrowError{Try: e, Rethrown: rethrown, Sink: enclosing})
	}
	c.markRaised()
}

// expandAliasChain follows a chain of transparent aliases to the type it stands for, so a
// caller decides against that type rather than against the alias handle. A seen-set of names
// breaks a degenerate cycle such as `type A = A`. A non-alias is returned unchanged.
func (c *checker) expandAliasChain(t soltype.Type) soltype.Type {
	seen := set.NewSet[string]()
	for {
		at, ok := t.(*soltype.AliasType)
		if !ok || seen.Contains(at.Name) {
			return t
		}
		seen.Add(at.Name)
		t = c.ctx.expandAlias(at)
	}
}

// caughtMembers returns the members one member of the caught union stands for, so each is
// weighed against the catch arms on its own. It expands a transparent alias and takes a union
// apart, and repeats through both, since an alias may name a union whose own members are
// aliases. `type Outer = Inner | "c"` over `type Inner = "a" | "b"` yields `"a"`, `"b"`, and
// `"c"`. A member that is neither an alias nor a union is returned as it is.
func (c *checker) caughtMembers(t soltype.Type) []soltype.Type {
	return c.appendCaughtMembers(nil, t, set.NewSet[string]())
}

// appendCaughtMembers is caughtMembers' walk, accumulating into out. seen holds the alias
// names already expanded, so a self-referential alias such as `type A = A | "x"` contributes
// `"x"` once and stops rather than expanding forever. The set is shared across the whole walk,
// which is sound because the result is a union: a name reached twice contributes the same
// members both times.
func (c *checker) appendCaughtMembers(out []soltype.Type, t soltype.Type, seen set.Set[string]) []soltype.Type {
	switch t := t.(type) {
	case *soltype.AliasType:
		if seen.Contains(t.Name) {
			return out
		}
		seen.Add(t.Name)
		return c.appendCaughtMembers(out, c.ctx.expandAlias(t), seen)
	case *soltype.UnionType:
		for _, m := range t.Types {
			out = c.appendCaughtMembers(out, m, seen)
		}
		return out
	}
	return append(out, t)
}

// unionParts returns a union's members, or the type itself as a one-element slice. It lets a
// caller iterate members without branching on whether it holds a union.
func unionParts(t soltype.Type) []soltype.Type {
	if u, ok := t.(*soltype.UnionType); ok {
		return u.Types
	}
	return []soltype.Type{t}
}

// memberCaught reports whether the catch arms catch every value of one member of the caught
// type, which keeps that member out of the rethrow. Either of two rules answers it. The set
// difference catches the member when `member ∩ ¬handled` holds no value, reading through
// subtyping so a base-class arm catches a subclass member. structuralMemberCovered reads an
// object or tuple pattern's shape, which names no type and so has no entry in handled.
func (c *checker) memberCaught(member, handled soltype.Type, arms []*ast.MatchCase) bool {
	return c.memberSubtracted(member, handled) ||
		structuralMemberCovered(member, arms)
}

// armsCatchType returns the type the unguarded catch arms catch, as one union. It is the
// `handled` side of the difference rethrowUnhandled takes, so a member of the caught type
// escapes rule 1 of memberCaught exactly when this type does not cover it.
//
// A guarded arm is skipped. Its guard can fail, so no value is caught on the strength of the
// arm's pattern alone. A pattern armCatchType cannot name contributes nothing, which makes
// the difference subtract less and the rethrow name more than what actually escapes.
func (c *checker) armsCatchType(scope *Scope, arms []*ast.MatchCase) soltype.Type {
	parts := make([]soltype.Type, 0, len(arms))
	for _, arm := range arms {
		if arm.Guard != nil {
			continue
		}
		if caught, ok := c.armCatchType(scope, arm.Pattern); ok {
			parts = append(parts, caught)
		}
	}
	return newUnion(c.ctx, parts, false)
}

// armCatchType returns the type one arm's pattern catches, and reports whether the pattern
// names a type at all.
//
//   - A wildcard or identifier binds every value, so it catches `unknown`.
//   - A literal pattern catches that literal's type, and the `null` and `undefined`
//     spellings catch those two atoms.
//   - An instance or extractor pattern catches the class it names, provided its
//     sub-patterns all bind unconditionally. `Color.RGB(r, g, b)` catches `Color.RGB`, but
//     `Color.RGB(0, g, b)` matches only when the first field is 0 and names no type.
//
// An object or tuple pattern reports false. It admits every value of a shape rather than of
// a type, and objectMemberHasKeys and tupleMemberFitsArity are what read that shape against a
// member, including through a borrow. structuralMemberCovered applies them, and the UCS IR's
// own tests apply the same two, so a written pattern and its IR test cannot disagree about
// which members a branch reaches. Rule 2 of memberCaught is where such a pattern is weighed.
func (c *checker) armCatchType(scope *Scope, p ast.Pat) (soltype.Type, bool) {
	switch pat := p.(type) {
	case *ast.WildcardPat, *ast.IdentPat:
		return &soltype.UnknownType{}, true
	case *ast.LitPat:
		if atom, _, isAtom := atomLitOf(pat.Lit); isAtom {
			return atom, true
		}
		if lt, ok := c.litTypeOf(pat.Lit); ok {
			return lt, true
		}
	case *ast.InstancePat:
		ct, ok := c.instancePatClass(scope, ast.QualIdentToString(pat.ClassName))
		if ok && irrefutablePat(pat.Object) {
			return ct, true
		}
	case *ast.ExtractorPat:
		ct, ok := c.resolveQualClassType(scope, pat.Name)
		if ok && allIrrefutable(pat.Args) {
			return ct, true
		}
	}
	return nil, false
}

// allIrrefutable reports whether every one of pats binds unconditionally, so a pattern built
// from them matches every value of a compatible type.
func allIrrefutable(pats []ast.Pat) bool {
	for _, p := range pats {
		if !irrefutablePat(p) {
			return false
		}
	}
	return true
}

// memberSubtracted reports whether handled leaves nothing of one caught member. The complement
// states that question to the solver, which moves `¬handled` to the supertype side and runs the
// trial as `member <: handled`. Only the member's OWN type variables are watched. Binding one
// settles what the member stands for, so the member is kept. Binding the arm's is the arm doing
// its job, and is what lets `catch { Failure{payload} => … }` catch a `Timeout<number>` member.
func (c *checker) memberSubtracted(member, handled soltype.Type) bool {
	// The intersection is built with a nil Context so its subsumption never calls constrain,
	// the same reason capturedBound builds its result that way.
	remainder := newIntersection(nil, []soltype.Type{member, &soltype.NegationType{Inner: handled}})
	empty, bound := c.ctx.trialBindsWatched(remainder, &soltype.NeverType{}, typeVarsIn(member))
	return empty && !bound
}

// typeVarsIn returns every type variable reachable from t, ordered by id so the walk is
// repeatable. A variable reached only through another's bound list counts, since binding it
// constrains what t can stand for just as directly.
func typeVarsIn(t soltype.Type) []*soltype.TypeVarType {
	vars := map[int]*soltype.TypeVarType{}
	t.Accept(&varCollector{out: vars, seen: set.NewSet[*soltype.TypeVarType]()}, soltype.Positive)
	ids := make([]int, 0, len(vars))
	for id := range vars {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	out := make([]*soltype.TypeVarType, 0, len(ids))
	for _, id := range ids {
		out = append(out, vars[id])
	}
	return out
}

// structuralMemberCovered reports whether some unguarded arm's object or tuple pattern
// irrefutably matches every value of a structural union member. An object pattern covers
// an object member when the member carries every field the pattern names. A tuple pattern
// covers a tuple member when their arities match. In both cases every sub-pattern must be
// irrefutable, so a nested literal such as `{x: 1}` or `[a, 2]` does not cover.
func structuralMemberCovered(member soltype.Type, arms []*ast.MatchCase) bool {
	for _, arm := range arms {
		if arm.Guard != nil {
			continue
		}
		if patternMatchesMemberShape(arm.Pattern, member) && irrefutablePat(arm.Pattern) {
			return true
		}
	}
	return false
}

// patternMatchesMemberShape reports whether a structural pattern's shape fits a union
// member, so the pattern can destructure that member. An object pattern fits an object
// member carrying every field the pattern names. A tuple pattern fits a tuple member of
// its fixed arity, or of at least that arity when the pattern ends in `...rest`. This is a
// shape test only. Refutability of the sub-patterns is checked separately by the caller
// that needs it. Every non-structural pattern returns false.
func patternMatchesMemberShape(pat ast.Pat, member soltype.Type) bool {
	switch p := pat.(type) {
	case *ast.ObjectPat:
		return objectMemberHasKeys(member, objectPatFieldNames(p))
	case *ast.TuplePat:
		arity, hasRest := tuplePatArity(p)
		return tupleMemberFitsArity(member, arity, hasRest)
	default:
		return false
	}
}

// objectMemberHasKeys reports whether a union member is an object carrying every one of
// names, which is what lets an object shape destructure that member. It is the shape rule
// itself, shared by a written object pattern and the UCS IR's object test so the two cannot
// disagree about which members a branch narrows to.
func objectMemberHasKeys(member soltype.Type, names []string) bool {
	obj, ok := soltype.CarrierOf(member).(*soltype.ObjectType)
	if !ok {
		return false
	}
	for _, name := range names {
		if _, found := obj.Prop(name); !found {
			return false
		}
	}
	return true
}

// tupleMemberFitsArity reports whether a union member is a tuple a shape of the given fixed
// arity can destructure. A trailing rest matches any tuple at least that long; without one
// the arity must match exactly. It is the tuple twin of objectMemberHasKeys, shared for the
// same reason.
func tupleMemberFitsArity(member soltype.Type, arity int, hasRest bool) bool {
	tup, ok := soltype.CarrierOf(member).(*soltype.TupleType)
	if !ok {
		return false
	}
	if hasRest {
		return len(tup.Elems) >= arity
	}
	return len(tup.Elems) == arity
}

// objectPatFieldNames returns the field names an object pattern binds. A shorthand or
// key-value element names one field. An `...rest` element names none. Every object-pattern key
// is an identifier, so it arrives as Key.Name; the parser also accepts the reserved type-name
// words `number`, `string`, `boolean`, and `bigint` as plain field names.
//
// The parser rejects a string-literal key `{ "foo": pat }`, a number-literal key `{ 42: pat }`,
// and a computed key `{ [expr]: pat }` alike, each with "Expected identifier or '...'".
// TODO: when those land, resolve a string- or number-literal key, and a computed key whose type
// is a string- or number-literal type, to its field name here. A symbol key such as
// `[Symbol.iterator]` needs the symbol-keyed member access M7 adds and has no string field name.
func objectPatFieldNames(p *ast.ObjectPat) []string {
	names := make([]string, 0, len(p.Elems))
	for _, elem := range p.Elems {
		switch e := elem.(type) {
		case *ast.ObjShorthandPat:
			names = append(names, e.Key.Name)
		case *ast.ObjKeyValuePat:
			names = append(names, e.Key.Name)
		}
	}
	return names
}

// tuplePatArity returns a tuple pattern's fixed-element count and whether it ends in a
// `...rest`. A rest element makes the pattern match any tuple at least as long as the
// fixed prefix.
func tuplePatArity(p *ast.TuplePat) (fixed int, hasRest bool) {
	for _, e := range p.Elems {
		if _, ok := e.(*ast.RestPat); ok {
			hasRest = true
			continue
		}
		fixed++
	}
	return fixed, hasRest
}

// narrowArmScrutinee returns the type an arm's pattern binds against. inferMatchArms is its
// only caller. For a structural object or tuple pattern over a union scrutinee, it keeps only
// the members whose shape the pattern matches, so the pattern destructures those members and
// leaves the rest for other arms. Narrowing is sound only in a refutable context such as an
// arm of a `match` or of a `try`'s catch clause. An irrefutable binding must still require
// the pattern's fields on every member. Every other pattern, and every non-union scrutinee,
// binds against the scrutinee unchanged.
//
// shape is the coalesced scrutinee the union structure is read from. scrutinee is what a
// non-narrowing arm binds against, so its var identity and borrow survive when no narrowing
// applies.
//
// The reachable arms of a `match` narrow through condWalk instead, which reads the same rule
// off the IR's tag tests rather than off the source pattern.
//
// A union whose members are borrows needs no peel here. objectMemberHasKeys and
// tupleMemberFitsArity each look through a member's borrow, so narrowing picks the right
// members, and what it returns still names them as borrows. bindPattern peels the result,
// through CarrierOf when narrowing kept one member and through peelBorrowUnion when it kept
// several.
func narrowArmScrutinee(shape, scrutinee soltype.Type, pat ast.Pat) soltype.Type {
	switch pat.(type) {
	case *ast.ObjectPat, *ast.TuplePat:
	default:
		return scrutinee
	}
	inner, mut, lt := soltype.UnwrapRef(shape)
	narrowed, ok := narrowUnionMembers(inner, func(m soltype.Type) bool {
		return patternMatchesMemberShape(pat, m)
	})
	if !ok {
		return scrutinee
	}
	// A kept object or tuple member is a RefInner, as is a rebuilt union, so a borrowed
	// scrutinee re-wraps its narrowed carrier under the same borrow. NewRef drops the
	// wrapper when the scrutinee was owned.
	if ri, ok := narrowed.(soltype.RefInner); ok {
		return soltype.NewRef(mut, lt, ri)
	}
	return narrowed
}

// narrowUnionMembers drops the members of a union shape that keep rejects, returning the
// narrowed type. ok=false means narrowing does not apply and the caller keeps the type it
// started from: shape is not a union, keep accepted none of its members, or keep accepted
// all of them, in which case the narrowed union would reproduce the original including an
// inexact one's open tail.
//
// It is the narrowing rule itself, shared by narrowArmScrutinee and the UCS IR's structural
// tests. shape must already be peeled of any borrow. Rewrapping is the caller's job,
// because the IR carries the borrow in its binding mode rather than on the type.
func narrowUnionMembers(shape soltype.Type, keep func(soltype.Type) bool) (soltype.Type, bool) {
	u, isUnion := shape.(*soltype.UnionType)
	if !isUnion {
		return nil, false
	}
	kept := make([]soltype.Type, 0, len(u.Types))
	for _, m := range u.Types {
		if keep(m) {
			kept = append(kept, m)
		}
	}
	if len(kept) == 0 || len(kept) == len(u.Types) {
		return nil, false
	}
	// An inexact union keeps its open `...` tail through narrowing. A tail member may carry
	// the tested fields at any type, so the tail is retained and the field-read rule (D4)
	// reads a narrowed inexact member's fields as `... | unknown`, i.e. `unknown`. An exact
	// union narrows to precisely the members that matched.
	tail := tailOf(u)
	if tail.bound != nil && closedShape(tail.bound) && !keep(tail.bound) {
		// No value the bound admits has the shape the test asks for, so the tail contributes
		// no member to this branch and goes. See closedShape for which bounds may be decided
		// this way: those whose own keys or arity pin every member drawn from them.
		tail = unionTail{}
	}
	if len(kept) == 1 && !tail.open {
		return kept[0], true
	}
	// Keep the structured inexact union rather than returning `unknown`. It is the bind
	// target for the branch's leaves, and constrainUnionFieldRead needs the listed members to
	// read each field before the tail widens it. Binding against bare `unknown` would leave
	// nothing to destructure.
	return &soltype.UnionType{Types: kept, Inexact: tail.open, TailBound: tail.bound}, true
}

// closedShape reports whether t's key set or arity is fully determined by t itself, so a
// `keep` test — which inspects only keys and arity, never field values — answers the same for
// every value t admits. Four shapes qualify:
//
//   - A primitive or a literal: a string is a string however it was written, and neither
//     carries object keys a test could find that t does not already show.
//   - An exact object: exactness forbids extra fields, so its inhabitants all carry exactly
//     t's keys. `{c: boolean}` lacking key "a" means no value it admits has "a".
//   - An exact tuple with no rest element: its inhabitants all have exactly t's length.
//
// An inexact object or tuple does not qualify: `{c: boolean, ...}` admits `{c: boolean, a: ...}`,
// which carries a key the bound does not name, so the bound failing a key test does not settle
// the members drawn from it. A union bound fails every object test while its members need not.
// Deciding those needs a disjointness question, a subtype check narrowUnionMembers holds no
// Context to ask, so it keeps such a tail — the wider, safe answer.
//
// The gate is what keeps narrowUnionMembers from over-narrowing. Its `keep` predicates are
// shape tests over one MEMBER, and a bound is not a member but the set its members are drawn
// from. Reading a bound's own failure as every member's is sound only where t's shape pins
// every member's keys, which is exactly what closedShape reports.
func closedShape(t soltype.Type) bool {
	switch t := t.(type) {
	case *soltype.PrimType, *soltype.LitType:
		return true
	case *soltype.ObjectType:
		return !t.Inexact
	case *soltype.TupleType:
		return !t.Inexact && !hasRestSpread(t.Elems)
	default:
		return false
	}
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

// irrefutablePat reports whether a pattern matches every value of a compatible
// type, so it can never fail at runtime. A wildcard or identifier binds
// unconditionally. An object or tuple pattern is irrefutable only when all of its
// sub-patterns are. A `...rest` element binds whatever the pattern's fixed parts leave
// behind. That leftover exists for every value of a compatible type, so a rest is
// irrefutable exactly when its own sub-pattern is. Whether the pattern's fixed arity
// fits the value at all is a separate question, answered by patternMatchesMemberShape.
// A literal pattern can fail, and the constructor patterns deferred to M5 are refutable,
// so both return false.
func irrefutablePat(p ast.Pat) bool {
	switch p := p.(type) {
	case *ast.WildcardPat, *ast.IdentPat:
		return true
	case *ast.RestPat:
		return irrefutablePat(p.Pattern)
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
			case *ast.ObjRestPat:
				if !irrefutablePat(e.Pattern) {
					return false
				}
			default:
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
// match diverges when every arm body does. DoExpr is not walked by the solver, which
// reports it unsupported, so that arm is unreachable from real source. It is kept in
// place so the form's divergence is already recognised the moment its inferExpr case
// lands, matching the checker rather than re-discovering divergence later. The checker's
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
	case *ast.TryCatchExpr:
		// Control falls out unless every way through leaves, so the try block and every arm
		// body must diverge. Same AND-fold as MatchExpr. An arm-less `try` is just its block.
		if !blockDiverges(&e.Try) {
			return false
		}
		for _, arm := range e.Catch {
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
// block branch). A nil-block-and-nil-expr alt is treated as a non-diverging `undefined`
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
		return &soltype.UndefinedType{}, false
	}
}
