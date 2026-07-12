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

	// namedLifetimes resolves a written lifetime name `'a` to its lifetime variable so
	// every `&'a` in one scope shares one variable. inferFunc saves and clears it on
	// entry and restores it on exit, so each function has its own named-lifetime scope,
	// nested functions included. inferComponent clears it per top-level binding for the
	// same reason. The map is allocated lazily by namedLifetime on first use.
	namedLifetimes map[string]*soltype.LifetimeVar

	// classNamespace is the dep_graph namespace of the class declaration currently
	// being inferred, empty at the root namespace and outside any class body.
	// inferClassDecl sets it on entry and restores it on exit. A class-body type
	// reference resolves through it first, so a bare `Point` written inside a class in
	// namespace `Geometry` finds the sibling `Geometry.Point` before falling back to a
	// root-namespace `Point`, mirroring dep_graph's qualified-first dependency
	// resolution. The class registry and every ClassType token are keyed by the
	// namespace-qualified name, so this reconstructs the qualified key a bare reference
	// omits.
	classNamespace string

	// classScope is the type scope of the class declaration whose body is being walked,
	// or nil outside a class body. inferClassDecl sets it to the class's declaration
	// scope — the child scope that holds the class's own type parameters — so a type
	// reference resolved by the general resolveTypeAnn path, such as a constructor or
	// method parameter annotation `food: D`, resolves the class's `D` rather than
	// reporting `Unsupported: TypeRefTypeAnn`. It is saved and restored around each class
	// declaration, so a nested class or a function body inside a method keeps the right
	// scope. The general scope-driven TypeRef resolution planned for M7 subsumes this.
	classScope *Scope
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
	// returnExprs holds each ReturnStmt's operand expression in the same source order as
	// returns, with a nil entry for a bare `return`. inferFunc reads it to decide the
	// immutable→mutable upgrade at a `mut` return annotation. The upgrade fires only when
	// every returned value is uniquely owned, so the join of the returns is too.
	returnExprs []ast.Expr

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
	// cfg is this body's control-flow graph, retained from the pre-pass so the move
	// engine can run AnalyzeMoves over it. liveness and stmtToRef are derived from
	// it; the move engine's branch-merged consumed lattice joins over the same
	// blocks (PR 5). nil at module top-level, where no pre-pass runs.
	cfg *liveness.CFG
	// moveSites records, per CFG statement position, the VarIDs the ordered walk
	// has consumed there — a binding moved by a return, a store, a consuming
	// argument, or an escaping capture. The walk records into it while walking the
	// body; AnalyzeMoves(cfg, moveSites) then yields the branch-merged consumed
	// lattice the use-after-move check reads after the body walk completes.
	moveSites map[liveness.StmtRef]set.Set[liveness.VarID]
	// moveNodes maps a consumed binding's VarID to the node that moved it, so a
	// UseAfterMoveError can point its "moved here" related span at the move site. A
	// binding moved on more than one path keeps the last-recorded move node, which
	// is a coarse but adequate blame target.
	moveNodes map[liveness.VarID]ast.Node
	// placeIDs assigns each field-level place one synthetic VarID (PR 7). A field-level
	// place is a root binding plus a path of field segments, such as `pair.a`. The key is
	// placeKey's encoding of the place, its root VarID followed by each segment's kind and
	// length-prefixed name, so two places with distinct roots or paths never share a key.
	// The value is the synthetic VarID the consumed lattice uses to track moves of that
	// place. Looking up the same place at its move site and at its use site returns one
	// stable VarID, so the use-after-move check can match them. Whole-binding places are
	// not stored here; they reuse their root VarID directly. The synthetic VarIDs come
	// from the module-wide varIDCounter, so they never collide with a real binding's VarID.
	placeIDs map[string]liveness.VarID
	// movePlaces records, for every VarID the move engine has consumed, the place it
	// stands for: a whole binding maps its root VarID to the path-empty place, and a
	// field move maps its synthetic VarID to the field place. A field place's synthetic
	// VarID is the one placeIDs assigned it, so the two maps agree on which VarID names
	// the place. checkUseAfterMoves scans it to find a moved place whose path is a prefix
	// of, or extends, the place a use reads — the partial-move conflict test that keeps a
	// moved `pair.a` from blocking a read of the sibling `pair.b`.
	movePlaces map[liveness.VarID]movePlace
	// useSites records every read of a reference-shaped place while walking the body,
	// each with the place read, the CFG point of the read, and the node to blame. The
	// place carries the field path, so a read of `pair.a` is distinguished from the
	// sibling `pair.b`. checkUseAfterMoves replays them against the consumed lattice
	// once the whole body, loop back edges included, has been walked, so a use that
	// some reaching path moved is caught even when the move is textually later.
	useSites []moveUse
	// eagerBorrowGraph records, per binding root VarID, the function-locals it borrows through
	// an explicit `&`/`&mut`, each tagged with the field path holding the borrow. The
	// initializer `val a = {peer: &mut b}` records the edge a → b at path [peer]. The
	// escape check follows these edges from a value flowing out of the frame to find a
	// borrow of a local that cannot outlive it, discriminating a field return `return
	// a.peer` from the disjoint `return a.data`. A parameter root is never recorded, since
	// a borrow of a parameter carries a caller lifetime that already outlives the frame.
	//
	// While the body is walked this map holds the source-order-current edge set for each
	// binding, updated by strong subtree replacement: a `var` reassignment clears the
	// binding's prior edges before recording its new ones, and a field store `recv.f = …`
	// clears only the [f] subtree. copyPlaceEdges reads it to project a source binding's
	// edges into a destructuring leaf, so it must reflect the state at the copy point. The
	// per-program-point graph the escape check reads is the flow-sensitive one analyzeBorrows
	// computes from borrowGens, not this eager map; resolveComponentEscapes passes that snapshot
	// to each escape helper rather than storing it here.
	eagerBorrowGraph map[liveness.VarID][]fieldBorrow
	// borrowGens records, per CFG statement position, the borrow-edge assignments that
	// statement makes: for each binding it re-points, the full new edge set the eager
	// walk computed. analyzeBorrows folds these forward over the CFG, joining edge sets at
	// branch merges by union so a borrow set on one branch reaches the merge, and replacing
	// a binding's whole edge set at each assignment so a reassignment clears the prior
	// referent. It is the flow-sensitive counterpart of moveSites.
	borrowGens map[liveness.StmtRef][]borrowAssign
	// borrowDirty accumulates the roots whose eager edge set changed while recording the
	// current statement's borrows. The recording sites flush it into borrowGens once the
	// statement's borrows are recorded, emitting one assignment per dirtied root. It is
	// reset after each flush.
	borrowDirty set.Set[liveness.VarID]
	// paramVarIDs holds the VarID of every parameter leaf binding. A borrow of a
	// parameter outlives the frame, so the escape check skips a referent in this set.
	paramVarIDs set.Set[liveness.VarID]
	// escapeSites records every value flowing out of the frame that might carry a borrow
	// of a function-local: a return value, a value stored into a parameter's field, and a
	// consuming argument. The decision is deferred to a post-pass, resolveComponentEscapes,
	// which runs once the whole body is walked, so the borrow-edge graph is complete and
	// the consumed lattice is available. A self-contained connected component re-anchors
	// and co-moves; anything else reports an EscapingBorrowError.
	escapeSites []escapeSite
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
	// pendingTransitions holds the phase/exclusivity conflicts the walk found, deferred
	// for emission until the consumed lattice is complete. checkMutabilityTransition
	// computes each conflict against the alias and liveness state at the transition's
	// program point and records it here rather than reporting it inline. After the body
	// walk, resolvePhaseTransitions emits each one unless the consumed lattice finds its
	// source moved on every reaching path, in which case the move engine's
	// use-after-move subsumes it. Deferring the emission lets the phase decision read
	// the complete post-walk lattice, so each conflict's path-sensitivity is decided in
	// one pass.
	//
	// INVARIANT: this slice must not be mutated under a discardable probe. The conflicts
	// here bypass openProbe's errs snapshot, since they reach c.errs only later through
	// resolvePhaseTransitions, so a discarded trial would not roll them back. It holds
	// today because checkMutabilityTransition runs only on the non-speculative statement
	// walk, never inside a probe. A probe opened around a statement walk that can reach
	// checkMutabilityTransition would have to journal this slice's length in openProbe,
	// the way errs is journaled.
	pendingTransitions []*MutabilityTransitionError
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
	return &checker{ctx: &Context{}, info: NewInfo(), prov: Prov{}, varIDCounter: 1}
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
		case *InexactTupleIntoExactError:
			err.prov, err.site = c.prov, n
		case *InexactUnionIntoExactError:
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
		case *ClassIntoExactObjectError:
			err.prov, err.site = c.prov, n
		case *StructuralIntoClassError:
			err.prov, err.site = c.prov, n
		case *ReadonlyFieldError:
			err.site = n
		case *ReadonlyFieldSubtypeError:
			err.site = n
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
	case *ast.IfLetExpr:
		return c.inferIfLet(scope, lvl, e)
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
