package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// checker is the per-inference-run carrier for the M2 constraint-generating
// walk. It wraps M1's *Context (the fresh-var counter + Constrain), threads the
// Info side table that records node→type, and accumulates SolverErrors. It is
// the method receiver for the whole walk.
//
// The walk is a direct recursive switch over ast.Expr/Stmt/Decl, NOT the shared
// ast.Visitor. This is a deliberate deviation from the CLAUDE.md "use the
// existing visitor" convention: constraint generation is bottom-up and
// value-producing (every node synthesizes a soltype.Type), which the visitor —
// shaped for enter/exit transformation with no per-node synthesized value — does
// not model cleanly. The shape matches both the simplesub spike's typeTerm and
// the old checker's inferExpr. See m2-implementation-plan §3.2.
type checker struct {
	ctx  *Context      // M1: freshVar(level), Constrain(sub, super) []SolverError
	info *Info         // M1: node → soltype.Type side table (unexported setType)
	prov Prov          // M2.5: soltype.Type → Origin (leaf FromAST only), the inverse of info
	errs []SolverError // accumulated; mirrors the spike's []error threading

	// fn is the enclosing function context for the body currently being walked —
	// its async flag (does `await` here resolve?), the function's AST node (for
	// await-outside-async blame), and the live list of every ReturnStmt expression
	// type collected from the body so far (PR3). It is nil at module top-level (and
	// inside a top-level `val` initializer): a top-level `await` is rejected by
	// inferAwait, and a top-level `return` — reachable inside an `if` in a `val`
	// initializer — by inferStmt's ReturnOutsideFunctionError. inferFunc pushes a
	// fresh context on entry and pops it on exit, so a nested function has its own
	// returns collection (a return inside an inner fn never escapes to the outer).
	fn *funcCtx

	// debugProv, when set, makes recordProv panic on a conflicting overwrite (the
	// same type pointer recorded against a *different* node) — turning the
	// implicit "every minted type is a unique pointer" invariant into an enforced
	// one. Off in production (a span bug must never crash the compiler); flipped on
	// by tests that exercise the guard. See recordProv.
	debugProv bool

	// varIDCounter is the module-wide running allocator for liveness VarIDs (M4 G1).
	// Each function body's pre-pass renames its locals starting from this counter and
	// then advances it, so VarIDs are unique across every body in one inference run
	// rather than restarting at 1 per body. That makes a binding's VarID name the same
	// variable in any frame, so a stale or cross-frame id can never collide with an
	// unrelated local in another body's alias/liveness tables. It starts at 1. The id 0
	// is the unset sentinel, and negative ids mark non-local bindings.
	varIDCounter int

	// preludeNames caches the immutable prelude root scope's sorted value names so the
	// liveness pre-pass collects them once instead of re-walking and re-sorting the
	// prelude for every function body (M4 G1). preludeNamesRoot is the scope the cache
	// was computed for. collectOuterBindings recomputes if a different root appears.
	preludeNames     []string
	preludeNamesRoot *Scope

	// paramLifetimes is the set of lifetime-variable ids that originate on a
	// function parameter (M4 D2). A `mut`-borrow param without a declared lifetime
	// is a borrow of whatever the caller lends, so attachParamLifetimes mints a
	// fresh lifetime var for it and records its id here. These are the only
	// lifetimes that get named in the rendered output — a borrow originates at a
	// parameter — while internal join vars (D3) stay anonymous and render as the
	// union of their reachable param lifetimes.
	paramLifetimes set.Set[int]

	// namedLifetimes resolves a written lifetime name `'a` to its lifetime variable so
	// every `&'a` in one scope shares one variable. inferFunc saves and clears it on
	// entry and restores it on exit, so each function has its own named-lifetime scope,
	// nested functions included. inferComponent clears it per top-level binding for the
	// same reason. The map is allocated lazily by namedLifetime on first use.
	namedLifetimes map[string]*soltype.LifetimeVar
}

// fieldKey identifies a written field by the receiver variable's ID and the
// property name — the key into a function body's `written` map (M4 C3).
type fieldKey struct {
	recvID int
	field  string
}

// funcCtx is the per-function inference context — pushed by inferFunc on entry to
// a function body and popped on exit. The async flag lets inferAwait diagnose a
// non-async use (and drives the external Promise wrap); node is the enclosing
// function's AST node, surfaced as the "make this async" related span on an
// await-outside-async error; returns accumulates every ReturnStmt expression type
// collected from the body (in source order, valued AND bare — bare returns
// contribute Void) so inferFunc can join them into the function's return type
// before constraining against the return annotation.
//
// Nesting is handled by save/restore through the pointer pushFuncCtx returns, not a
// parent chain on the struct: a nested fn opens its own ctx (so its returns never
// leak to the outer), and the outer is restored verbatim on pop.
type funcCtx struct {
	async   bool
	node    ast.Node
	returns []soltype.Type

	// written records the widened type stored into a receiver variable's field by a
	// field-write `recv.prop = source` (M4 C3), keyed by the receiver var's ID and
	// the property name. A later read of the same field returns this concrete type
	// instead of minting a fresh var, so `obj.x = 5; obj.x` is `number`
	// (read-after-write). It is purely a precision win: write-after-read already
	// works through ordinary bound accumulation.
	//
	// It lives here, not on the checker, because read-after-write is an INTRA-BODY
	// relation: scoping the cache to the function body it belongs to makes that
	// correct by construction rather than relying on the global fresh-var counter to
	// keep one body's receiver IDs from colliding with another's. Push/pop of funcCtx
	// isolates it for free — a nested function gets its own map and the outer's is
	// restored on exit. A field write at module top-level (c.fn == nil) simply gets
	// no read-after-write precision, which is sound.
	written map[fieldKey]soltype.Type

	// liveness, aliases, stmtToRef, and varIDNames are the mutability-transition
	// checking state for THIS function body (M4 G1), populated by runLivenessPrePass
	// before the body is walked. They are the new-checker analogue of the old
	// checker's Context.Liveness/Aliases/StmtToRef/VarIDNames. Scoping them to funcCtx
	// gives a nested function its own liveness analysis for free. Push/pop isolates them
	// exactly like `written`. They stay nil at module top-level (c.fn == nil), where the
	// transition checker is a no-op.
	liveness   *liveness.LivenessInfo
	aliases    *liveness.AliasTracker
	stmtToRef  map[ast.Stmt]liveness.StmtRef
	varIDNames map[liveness.VarID]string
	// varIDTypes maps each tracked variable's VarID to its soltype. It is the bridge
	// the transition checker uses to query the lifetime sort for a `'static` escape in
	// M4 G2. It replaces the dropped HasStatic{Mut,Imm}Alias bits. A value whose borrow
	// lifetime D3 forced `<: 'static` has a permanent outside alias, and that borrow's
	// Mut supplies its mutability. The stored type is the SAME pointer the inference
	// graph mutates. So a lifetime that a later global write `sink = p` forces to
	// 'static after the binding is recorded is visible here through the shared
	// LifetimeVar. Populated at the same sites that seed alias mutability: parameter
	// leaves, decl bindings, and reassignment targets.
	varIDTypes map[liveness.VarID]soltype.Type
	// currentStmt is the enclosing statement currently being walked (M4 G1), set by
	// inferStmt. A reassignment `a = e` lives in expression position, so the transition
	// checker reads the enclosing statement from here to find its CFG StmtRef.
	currentStmt ast.Stmt
}

// pushFuncCtx enters the inference context for function `node` (async controls
// whether `await` is allowed and the external Promise wrap). It returns the
// PREVIOUS context; the caller restores it by handing that pointer to popFuncCtx
// after walking the body. Push/pop is straight-line, not deferred: the caller needs
// popFuncCtx's returned return-points as a value, and the walk reports errors rather
// than panicking, so there is no unwind to guard against.
func (c *checker) pushFuncCtx(async bool, node ast.Node) *funcCtx {
	saved := c.fn
	c.fn = &funcCtx{async: async, node: node, written: map[fieldKey]soltype.Type{}}
	return saved
}

// popFuncCtx restores the previous function context (the pointer pushFuncCtx
// returned) and hands back the return-point types collected from the body just
// walked, so the caller can join them into the function's return type.
func (c *checker) popFuncCtx(saved *funcCtx) []soltype.Type {
	collected := c.fn.returns
	c.fn = saved
	return collected
}

// newChecker returns a checker with a fresh Context, an empty Info table, and an
// empty Prov side table.
func newChecker() *checker {
	return &checker{ctx: &Context{}, info: NewInfo(), prov: Prov{}, paramLifetimes: set.NewSet[int](), varIDCounter: 1}
}

// freshAt allocates a fresh inference variable at the given level. Provenance for
// a fresh var is recorded by its construction site (recordProv), not here.
func (c *checker) freshAt(lvl int) *soltype.TypeVarType {
	return c.ctx.freshVar(lvl)
}

// constrain asserts source <: target, then stamps blame onto each resulting error.
//
// The operands map onto the engine's sub/super names (Context.Constrain):
//   - source is sub: the value being checked.
//   - target is super: the expected type.
//
// These data-flow names hold here because no contravariant flip has happened yet.
// In `x = e`, `e` is the source and `x` is the target.
//
// For each error Constrain returns:
//  1. Assign the Prov table and the constraint node n to the error.
//  2. The error's Span()/Related() then resolve per-operand blame through Prov on
//     demand. When an operand has no Prov entry, blame falls back to n's span,
//     never the zero span (§3.5).
//
// The engine never touches Prov. These fields are assigned after Constrain returns,
// so the hot loop stays off the table. That is the perf invariant, §3.9. Bridge
// errors never flow through here; they self-blame from their own node.
func (c *checker) constrain(n ast.Node, source, target soltype.Type) {
	for _, e := range c.ctx.Constrain(source, target) {
		switch err := e.(type) {
		case *CannotConstrainError:
			err.prov, err.site = c.prov, n
		case *TupleLengthMismatchError:
			err.prov, err.site = c.prov, n
		case *MissingPropertyError:
			err.prov, err.site = c.prov, n
		case *InexactIntoExactError:
			err.prov, err.site = c.prov, n
		case *ExtraPropertyError:
			err.prov, err.site = c.prov, n
		case *OptionalPropertyError:
			err.prov, err.site = c.prov, n
		case *FuncArityMismatchError:
			err.prov, err.site = c.prov, n
		case *MutabilityMismatchError:
			err.prov, err.site = c.prov, n
		case *BorrowEscapeError:
			err.prov, err.site = c.prov, n
		}
		c.errs = append(c.errs, e)
	}
}

// report accumulates a structured error and returns the error-recovery sentinel
// (PR8) so a caller can `return c.report(...)` in value position. ErrorType
// absorbs in both directions inside constrain, so this placeholder never cascades
// a second, spurious failure — replacing M2's overloaded `never` placeholder
// (never is the lattice bottom, coalesced-output only, with no constrain-input
// rule). It is minted ONLY here, where a diagnostic was definitely emitted, never
// by freshVar — the discipline that keeps absorption from silently hiding a
// genuine checker bug.
func (c *checker) report(e SolverError) soltype.Type {
	c.errs = append(c.errs, e)
	return &soltype.ErrorType{}
}

// reportUnsupported records an UnsupportedNodeError for an AST node whose kind is
// outside the M2 subset. The node self-blames: both the span and the rendered kind
// (astKind) come from it. When the unsupported thing is a child carried by its
// parent — an object property's computed key, a function's destructuring pattern —
// pass that child node directly (it embeds ast.Node and carries its own, narrower
// span). Returns the never placeholder so a caller can `return c.reportUnsupported(n)`
// in value position.
func (c *checker) reportUnsupported(node ast.Node) soltype.Type {
	return c.report(&UnsupportedNodeError{Node: node})
}

// reportUnsupportedFeature records an UnsupportedFeatureError: the node's KIND is
// supported but a feature of it is not (optional chaining on a MemberExpr, type
// params on a FuncExpr). The node carries the blame span; feature names what is
// unsupported, since astKind would name the supported parent.
func (c *checker) reportUnsupportedFeature(node ast.Node, feature string) soltype.Type {
	return c.report(&UnsupportedFeatureError{Node: node, Feature: feature})
}

// recordType records t as the inferred type of n in the Info side table. Wraps
// the unexported setType — which is why the whole M2 walk lives in package
// solver. The AST stays untouched (no InferredType() writes); Info is the single
// source of truth for node→type.
//
// Under a probe (M3 PR5) snapshotMapEntry captures the prior entry and registers
// a rollback closure, so a discarded speculative trial leaves Info exactly as it
// was — the entry restored if n had one, deleted if it did not.
func (c *checker) recordType(n ast.Node, t soltype.Type) {
	snapshotMapEntry(c, c.info.types, n)
	c.info.setType(n, t)
}

// inferExpr dispatches on the concrete expression kind. PR-1 wired the two leaf
// cases (literals, identifiers); PR-3 adds the function/application walk
// (FuncExpr, CallExpr — the block/statement walk they drive lives in
// infer_stmt.go); PR-4 adds tuples, object literals, and member access; M3 adds
// await/if-else and (PR8) the assignment form of BinaryExpr. Every remaining kind
// falls through to a clean UnsupportedNodeError (never a panic).
func (c *checker) inferExpr(scope *Scope, lvl int, e ast.Expr) soltype.Type {
	switch e := e.(type) {
	case *ast.LiteralExpr:
		return c.inferLiteral(e)
	case *ast.IdentExpr:
		return c.inferIdent(scope, lvl, e)
	case *ast.FuncExpr:
		return c.inferFuncExpr(scope, lvl, e)
	case *ast.CallExpr:
		return c.inferCall(scope, lvl, e)
	case *ast.TupleExpr:
		return c.inferTuple(scope, lvl, e)
	case *ast.ObjectExpr:
		return c.inferObject(scope, lvl, e)
	case *ast.MemberExpr:
		return c.inferMember(scope, lvl, e)
	case *ast.IndexExpr:
		return c.inferIndex(scope, lvl, e)
	case *ast.AwaitExpr:
		return c.inferAwait(scope, lvl, e)
	case *ast.IfElseExpr:
		return c.inferIfElse(scope, lvl, e)
	case *ast.MatchExpr:
		return c.inferMatch(scope, lvl, e)
	case *ast.BorrowExpr:
		return c.inferBorrow(scope, lvl, e)
	case *ast.BinaryExpr:
		// PR8 handles the ASSIGNMENT op only (`a = expr`); every other binary
		// operator (+, ==, &&, ++, …) needs the operator-scheme walk over the prelude
		// bindings, a separate unlanded PR, so it stays UnsupportedNodeError.
		if e.Op == ast.Assign {
			return c.inferAssign(scope, lvl, e)
		}
		return c.reportUnsupported(e)
	default:
		return c.reportUnsupported(e)
	}
}
