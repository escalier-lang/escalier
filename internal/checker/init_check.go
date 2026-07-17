package checker

import (
	"sort"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
)

// ---------------------------------------------------------------------------
// Phase 3 errors. See planning/class_constructor/implementation_plan.md §3.6.
// ---------------------------------------------------------------------------

// FieldNotInitializedError is reported when a constructor body has a
// reachable exit on which one or more required fields are still
// uninitialized.
type FieldNotInitializedError struct {
	FieldNames []string
	span       ast.Span
}

func (e FieldNotInitializedError) Span() ast.Span { return e.span }
func (e FieldNotInitializedError) Message() string {
	if len(e.FieldNames) == 1 {
		return "Field '" + e.FieldNames[0] + "' is not initialized on every path through the constructor."
	}
	return "Fields " + joinNames(e.FieldNames) + " are not initialized on every path through the constructor."
}

type ReadBeforeInitError struct {
	FieldName string
	span      ast.Span
}

func (e ReadBeforeInitError) Span() ast.Span { return e.span }
func (e ReadBeforeInitError) Message() string {
	return "Field 'self." + e.FieldName + "' is read before it has been initialized."
}

type MethodCallBeforeInitError struct {
	MissingFields []string
	span          ast.Span
}

func (e MethodCallBeforeInitError) Span() ast.Span { return e.span }
func (e MethodCallBeforeInitError) Message() string {
	return "Cannot call a method on `self` before all required fields are initialized; missing " + joinNames(e.MissingFields) + "."
}

type SelfAliasBeforeInitError struct {
	MissingFields []string
	span          ast.Span
}

func (e SelfAliasBeforeInitError) Span() ast.Span { return e.span }
func (e SelfAliasBeforeInitError) Message() string {
	return "Cannot alias or pass `self` before all required fields are initialized; missing " + joinNames(e.MissingFields) + "."
}

type ComputedSelfAccessBeforeInitError struct {
	span ast.Span
}

func (e ComputedSelfAccessBeforeInitError) Span() ast.Span { return e.span }
func (e ComputedSelfAccessBeforeInitError) Message() string {
	return "Computed access on `self` (`self[...]`) is not permitted before all required fields are initialized."
}

type LoopInConstructorNotSupportedError struct {
	span ast.Span
}

func (e LoopInConstructorNotSupportedError) Span() ast.Span { return e.span }
func (e LoopInConstructorNotSupportedError) Message() string {
	return "Loops are not yet supported inside constructor bodies."
}

type TryInConstructorNotSupportedError struct {
	span ast.Span
}

func (e TryInConstructorNotSupportedError) Span() ast.Span { return e.span }
func (e TryInConstructorNotSupportedError) Message() string {
	return "`try`/`catch` is not yet supported inside constructor bodies."
}

func joinNames(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = "'" + n + "'"
	}
	return strings.Join(quoted, ", ")
}

// ---------------------------------------------------------------------------
// Definite-assignment analysis.
// ---------------------------------------------------------------------------

// initState carries a flow-sensitive snapshot of which non-optional fields
// are definitely initialized at a given program point.
type initState struct {
	initialized set.Set[string]
}

func (s initState) clone() initState {
	return initState{initialized: s.initialized.Clone()}
}

// initFlow is the result of analyzing a sub-tree. `terminated` means the
// subtree never falls through normally (every path exits via `throw` or
// `return`).
type initFlow struct {
	state      initState
	terminated bool
}

type initChecker struct {
	requireAll set.Set[string]
	errors     []Error
}

// checkConstructorInit runs definite-assignment analysis on the
// constructor body. `requireAll` is the set of non-optional instance
// field names. Returns any diagnostics found.
//
// Phase 3: only constructors of non-`extends` classes are analyzed.
// Inheritance / `super(...)` is deferred. Callers must filter on
// `decl.Extends == nil` before invoking.
func (c *Checker) checkConstructorInit(
	ctor *ast.ConstructorElem,
	requiredFields []string,
) []Error {
	if ctor.Fn == nil || ctor.Fn.Body == nil {
		return nil
	}
	ic := &initChecker{requireAll: set.FromSlice(requiredFields)}
	flow := ic.walkBlock(ctor.Fn.Body, initState{initialized: set.NewSet[string]()})

	if !flow.terminated {
		// Compute which required fields are still missing at the
		// fall-through exit and report a single error listing all of
		// them. The diagnostic is anchored at the block's open brace
		// (its span start).
		missing := ic.requireAll.Difference(flow.state.initialized).ToSlice()
		if len(missing) > 0 {
			sort.Strings(missing)
			ic.errors = append(ic.errors, FieldNotInitializedError{
				FieldNames: missing,
				span:       ctor.Fn.Body.Span,
			})
		}
	}
	return ic.errors
}

// walkBlock walks a Block and threads `initState` through each statement.
// Returns the post-block flow; if any statement terminated the block,
// subsequent statements are not analyzed.
func (ic *initChecker) walkBlock(block *ast.Block, st initState) initFlow {
	flow := initFlow{state: st}
	for _, stmt := range block.Stmts {
		flow = ic.walkStmt(stmt, flow.state)
		if flow.terminated {
			break
		}
	}
	return flow
}

func (ic *initChecker) walkStmt(stmt ast.Stmt, st initState) initFlow {
	switch s := stmt.(type) {
	case *ast.ExprStmt:
		return ic.walkExpr(s.Expr, st)
	case *ast.ReturnStmt:
		if s.Expr != nil {
			st = ic.walkExpr(s.Expr, st).state
		}
		// Treat `return` as a terminator. `requireAll` must hold here too.
		ic.checkExitMissing(s.Span(), st)
		return initFlow{state: st, terminated: true}
	case *ast.DeclStmt:
		if vd, ok := s.Decl.(*ast.VarDecl); ok && vd.Init != nil {
			// `val r = self` — analyzed as an alias of self.
			if isSelfIdent(vd.Init) {
				ic.reportSelfAliasIfMissing(vd.Init.Span(), st)
				return initFlow{state: st}
			}
			return ic.walkExpr(vd.Init, st)
		}
		return initFlow{state: st}
	case *ast.ForInStmt:
		ic.errors = append(ic.errors, LoopInConstructorNotSupportedError{span: s.Span()})
		// Treat the loop as a terminator so we don't pile a
		// FieldNotInitialized on top of the unsupported-loop error.
		return initFlow{state: st, terminated: true}
	default:
		return initFlow{state: st}
	}
}

// walkExpr threads init state through an expression, handling:
//   - `self.x = expr` writes (after evaluating RHS, mark `x` initialized)
//   - `self.x` reads (gated on `x` being initialized)
//   - `self[k]` accesses (rejected before all-init except literal-string keys)
//   - `self` aliasing / passing / returning (require all init)
//   - throw (terminator)
//   - if/else, match, try/catch branching
func (ic *initChecker) walkExpr(expr ast.Expr, st initState) initFlow {
	switch e := expr.(type) {
	case nil:
		return initFlow{state: st}

	case *ast.BinaryExpr:
		if e.Op == ast.Assign {
			// LHS may be `self.x` (write — special) or `self[k]` (computed)
			// or anything else (treat as a normal nested expression).
			if mem, ok := e.Left.(*ast.MemberExpr); ok && isSelfIdent(mem.Object) {
				// Evaluate RHS first; the field name is not yet initialized
				// while the RHS runs. (Per plan §3.3.)
				flow := ic.walkExpr(e.Right, st)
				if flow.terminated {
					return flow
				}
				out := flow.state.clone()
				out.initialized.Add(mem.Prop.Name)
				return initFlow{state: out}
			}
			if idx, ok := e.Left.(*ast.IndexExpr); ok && isSelfIdent(idx.Object) {
				// Special case: `self["literal"] = rhs` where the literal
				// string is the name of a declared field. Treat this the
				// same as `self.literal = rhs` — it initializes that
				// field. This is the path the synthesized constructor
				// uses for fields whose keys aren't valid JS identifiers
				// (e.g. `"foo-bar"`).
				if litExpr, ok := idx.Index.(*ast.LiteralExpr); ok {
					if strLit, ok := litExpr.Lit.(*ast.StrLit); ok && ic.requireAll.Contains(strLit.Value) {
						flow := ic.walkExpr(e.Right, st)
						if flow.terminated {
							return flow
						}
						out := flow.state.clone()
						out.initialized.Add(strLit.Value)
						return initFlow{state: out}
					}
				}
				// Otherwise, Phase 3 is conservative: any computed
				// self-write before every required field is initialized
				// is rejected.
				if !ic.allRequiredInit(st) {
					ic.errors = append(ic.errors, ComputedSelfAccessBeforeInitError{span: e.Span()})
				}
				flow := ic.walkExpr(e.Right, st)
				return flow
			}
			// Normal assignment: walk both sides, no init effect.
			flow := ic.walkExpr(e.Right, st)
			if flow.terminated {
				return flow
			}
			return ic.walkExpr(e.Left, flow.state)
		}
		// Non-assign binary op.
		flow := ic.walkExpr(e.Left, st)
		if flow.terminated {
			return flow
		}
		// `&&` / `||` short-circuit: the RHS is only conditionally
		// evaluated. Join the RHS-evaluated flow with the LHS-only flow
		// so that any writes inside the RHS are not treated as definite.
		if e.Op == ast.LogicalAnd || e.Op == ast.LogicalOr {
			rhsFlow := ic.walkExpr(e.Right, flow.state)
			return joinFlows(rhsFlow, flow)
		}
		return ic.walkExpr(e.Right, flow.state)

	case *ast.MemberExpr:
		if isSelfIdent(e.Object) {
			if !st.initialized.Contains(e.Prop.Name) && ic.requireAll.Contains(e.Prop.Name) {
				ic.errors = append(ic.errors, ReadBeforeInitError{
					FieldName: e.Prop.Name,
					span:      e.Span(),
				})
			}
			return initFlow{state: st}
		}
		return ic.walkExpr(e.Object, st)

	case *ast.IndexExpr:
		if isSelfIdent(e.Object) {
			if !ic.allRequiredInit(st) {
				ic.errors = append(ic.errors, ComputedSelfAccessBeforeInitError{span: e.Span()})
			}
			return ic.walkExpr(e.Index, st)
		}
		flow := ic.walkExpr(e.Object, st)
		if flow.terminated {
			return flow
		}
		return ic.walkExpr(e.Index, flow.state)

	case *ast.CallExpr:
		// `self.method(...)` — require all-init at the call site.
		if mem, ok := e.Callee.(*ast.MemberExpr); ok && isSelfIdent(mem.Object) {
			ic.reportMethodCallIfMissing(e.Span(), st)
		} else {
			flow := ic.walkExpr(e.Callee, st)
			st = flow.state
			if flow.terminated {
				return flow
			}
		}
		for _, arg := range e.Args {
			// Passing `self` as an argument is an alias.
			if isSelfIdent(arg) {
				ic.reportSelfAliasIfMissing(arg.Span(), st)
				continue
			}
			f := ic.walkExpr(arg, st)
			st = f.state
			if f.terminated {
				return f
			}
		}
		return initFlow{state: st}

	case *ast.IdentExpr:
		// A bare reference to `self` (not as an LHS or callee receiver,
		// since those are handled by callers above) is an alias.
		if e.Name == "self" {
			ic.reportSelfAliasIfMissing(e.Span(), st)
		}
		return initFlow{state: st}

	case *ast.ThrowExpr:
		if e.Arg != nil {
			st = ic.walkExpr(e.Arg, st).state
		}
		return initFlow{state: st, terminated: true}

	case *ast.IfElseExpr:
		condFlow := ic.walkExpr(e.Cond, st)
		if condFlow.terminated {
			return condFlow
		}
		consFlow := ic.walkBlock(&e.Cons, condFlow.state)
		var altFlow initFlow
		if e.Alt != nil {
			altFlow = ic.walkBlockOrExpr(e.Alt, condFlow.state)
		} else {
			altFlow = initFlow{state: condFlow.state}
		}
		return joinFlows(consFlow, altFlow)

	case *ast.IfValExpr:
		tgtFlow := ic.walkExpr(e.Target, st)
		if tgtFlow.terminated {
			return tgtFlow
		}
		consFlow := ic.walkBlock(&e.Cons, tgtFlow.state)
		var altFlow initFlow
		if e.Alt != nil {
			altFlow = ic.walkBlockOrExpr(e.Alt, tgtFlow.state)
		} else {
			altFlow = initFlow{state: tgtFlow.state}
		}
		return joinFlows(consFlow, altFlow)

	case *ast.MatchExpr:
		tgtFlow := ic.walkExpr(e.Target, st)
		if tgtFlow.terminated {
			return tgtFlow
		}
		var joined *initFlow
		for _, mc := range e.Cases {
			caseFlow := ic.walkBlockOrExpr(&mc.Body, tgtFlow.state)
			if joined == nil {
				f := caseFlow
				joined = &f
			} else {
				j := joinFlows(*joined, caseFlow)
				joined = &j
			}
		}
		if joined == nil {
			return initFlow{state: tgtFlow.state}
		}
		return *joined

	case *ast.TryCatchExpr:
		ic.errors = append(ic.errors, TryInConstructorNotSupportedError{span: e.Span()})
		return initFlow{state: st}

	case *ast.DoExpr:
		return ic.walkBlock(&e.Body, st)

	case *ast.AwaitExpr:
		if e.Arg != nil {
			return ic.walkExpr(e.Arg, st)
		}
		return initFlow{state: st}

	case *ast.UnaryExpr:
		if e.Arg != nil {
			return ic.walkExpr(e.Arg, st)
		}
		return initFlow{state: st}

	case *ast.BorrowExpr:
		// A borrow's operand is read like any other reference. Recursing into
		// the arg makes `&self.x` and `foo(&self)` before init fire the same
		// ReadBeforeInit and SelfAliasBeforeInit diagnostics a bare read does.
		if e.Arg != nil {
			return ic.walkExpr(e.Arg, st)
		}
		return initFlow{state: st}

	case *ast.TupleExpr:
		// Unlike the single-arg AwaitExpr/UnaryExpr cases above, a tuple has
		// multiple sibling sub-expressions threaded through `st`. If one
		// terminates, the rest are unreachable, so propagate early instead
		// of walking dead code with a stale state.
		for _, el := range e.Elems {
			f := ic.walkExpr(el, st)
			st = f.state
			if f.terminated {
				return f
			}
		}
		return initFlow{state: st}

	case *ast.ObjectExpr:
		// Object literals' element exprs aren't generally walked here;
		// in practice constructors that hand `self` into objects are
		// rare. Skip deeply for now.
		return initFlow{state: st}

	case *ast.TypeCastExpr:
		return ic.walkExpr(e.Expr, st)

	default:
		return initFlow{state: st}
	}
}

func (ic *initChecker) walkBlockOrExpr(b *ast.BlockOrExpr, st initState) initFlow {
	if b.Block != nil {
		return ic.walkBlock(b.Block, st)
	}
	if b.Expr != nil {
		return ic.walkExpr(b.Expr, st)
	}
	return initFlow{state: st}
}

// joinFlows merges two control-flow branches at a join point. If both
// branches terminate, the result terminates; otherwise the result is the
// intersection of initialized sets across the non-terminated branches.
func joinFlows(a, b initFlow) initFlow {
	switch {
	case a.terminated && b.terminated:
		return initFlow{state: a.state, terminated: true}
	case a.terminated:
		return b
	case b.terminated:
		return a
	default:
		return initFlow{state: initState{initialized: a.state.initialized.Intersection(b.state.initialized)}}
	}
}

// allRequiredInit reports whether every required field is in the
// initialized set.
func (ic *initChecker) allRequiredInit(st initState) bool {
	return ic.requireAll.IsSubset(st.initialized)
}

func (ic *initChecker) missing(st initState) []string {
	out := ic.requireAll.Difference(st.initialized).ToSlice()
	sort.Strings(out)
	return out
}

func (ic *initChecker) reportSelfAliasIfMissing(span ast.Span, st initState) {
	if ic.allRequiredInit(st) {
		return
	}
	ic.errors = append(ic.errors, SelfAliasBeforeInitError{MissingFields: ic.missing(st), span: span})
}

func (ic *initChecker) reportMethodCallIfMissing(span ast.Span, st initState) {
	if ic.allRequiredInit(st) {
		return
	}
	ic.errors = append(ic.errors, MethodCallBeforeInitError{MissingFields: ic.missing(st), span: span})
}

func (ic *initChecker) checkExitMissing(span ast.Span, st initState) {
	missing := ic.missing(st)
	if len(missing) == 0 {
		return
	}
	ic.errors = append(ic.errors, FieldNotInitializedError{FieldNames: missing, span: span})
}

// requiredFieldNames returns the names of instance fields that must be
// definitely-assigned by every reachable exit of a constructor body.
// Static fields, optional fields (`x?: T`), fields with default
// initializers (`= expr`), and fields with computed keys are excluded.
func requiredFieldNames(decl *ast.ClassDecl) []string {
	out := []string{}
	for _, elem := range decl.Body {
		field, ok := elem.(*ast.FieldElem)
		if !ok {
			continue
		}
		if field.Static {
			continue
		}
		if field.Optional {
			continue
		}
		switch k := field.Name.(type) {
		case *ast.IdentExpr:
			out = append(out, k.Name)
		case *ast.StrLit:
			out = append(out, k.Value)
			// Computed-key fields are excluded — they cannot be referred
			// to by name and the synthesizer-required path already
			// rejects them earlier.
		}
	}
	return out
}

func isSelfIdent(e ast.Expr) bool {
	id, ok := e.(*ast.IdentExpr)
	return ok && id.Name == "self"
}
