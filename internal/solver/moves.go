package solver

import (
	"fmt"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// The move engine (PR 6 of the affine-semantics plan). When an owned value flows
// out of its source binding at a flow site — a `val`/`var` binding, a
// reassignment, a `return`, a field or element store, a consuming argument, an
// escaping closure capture, or a module-level write — ownership MOVES out of that
// binding, the binding is CONSUMED, and any later use of it is a use-after-move
// error.
//
// The engine has three parts:
//
//   - The flow sites call consumeOwned to record a move into c.fn.moveSites, the
//     per-statement consume map AnalyzeMoves folds into the branch-merged consumed
//     lattice from internal/liveness.
//   - inferIdent calls recordUse for every read of a reference-shaped binding,
//     accumulating the read sites into c.fn.useSites.
//   - After the body walk, checkUseAfterMoves runs AnalyzeMoves once and replays
//     each recorded use against the lattice, reporting a UseAfterMoveError when the
//     binding was Moved or MaybeMoved on a path reaching the read.
//
// Running the use check as a post-pass rather than inline is what makes conditional
// and loop moves correct: the lattice is a fixed point over the whole CFG, so a use
// that a later or back-edge move reaches is still caught.
//
// Known limitation: the lattice only ever raises a binding to Moved, never lowers it.
// Reassigning a `var` after it was moved gives it a fresh value, but the lattice still
// reads it as Moved, so a use after `q = …` re-initializes a consumed q reports a
// spurious use-after-move. Clearing a moved binding at its reassignment is left to a
// later precision pass.

// UseAfterMoveError is reported when a binding is read after an owned value has
// moved out of it. It blames the read and points its related span at the move
// site. Conditional records whether only SOME reaching paths moved the binding —
// a MaybeMoved lattice state — so a diagnostic consumer can distinguish a definite
// use-after-move from a possible one.
type UseAfterMoveError struct {
	// Name is the consumed binding's source name, for the message.
	Name string
	// Conditional is true when the binding is moved on some but not all paths
	// reaching the use (the MaybeMoved lattice state), false when every reaching
	// path moved it (Moved).
	Conditional bool
	// use is the read being rejected; it self-blames from here.
	use ast.Node
	// moveSite is the consume that moved the binding, used for the related span. It
	// may be nil when the move node was not recorded.
	moveSite ast.Node
}

func (*UseAfterMoveError) isSolverError()   {}
func (e *UseAfterMoveError) Span() ast.Span { return e.use.Span() }
func (e *UseAfterMoveError) Related() []ast.Span {
	if e.moveSite == nil {
		return nil
	}
	return []ast.Span{e.moveSite.Span()}
}
func (e *UseAfterMoveError) Message() string {
	return fmt.Sprintf("use of moved value '%s'", e.Name)
}

// moveUse is one recorded read of an owned-movable binding: the binding's VarID,
// the CFG point of the read, and the node to blame.
type moveUse struct {
	varID liveness.VarID
	ref   liveness.StmtRef
	node  ast.Node
	name  string
}

// isBorrowType reports whether t is a borrow — a RefType carrying a lifetime.
// Owned-immutable collapses to the bare inner and owned-mutable is a RefType with
// a nil lifetime, so only a non-nil Lt marks a borrow.
func isBorrowType(t soltype.Type) bool {
	r, ok := t.(*soltype.RefType)
	return ok && r.Lt != nil
}

// isReferenceShaped reports whether t is a reference-shaped value — an object,
// tuple, borrow, owned RefType, or type-parameter variable. These are the values a
// move can consume, so every read of one is recorded as a use to test against the
// consumed lattice. Value types — primitives, functions, promises — copy and are
// never consumed, so their reads are not tracked.
func isReferenceShaped(t soltype.Type) bool {
	switch t.(type) {
	case *soltype.ObjectType, *soltype.TupleType, *soltype.RefType, *soltype.TypeVarType:
		return true
	}
	return false
}

// isConcreteOwned reports whether t is a CONCRETE owned reference shape — an owned
// object, tuple, or owned RefType — excluding a bare type variable. A consuming
// parameter must be spelled as a concrete owned shape, so a fresh inference variable
// for an unannotated parameter does not consume its argument.
func isConcreteOwned(t soltype.Type) bool {
	switch t.(type) {
	case *soltype.ObjectType, *soltype.TupleType:
		return true
	case *soltype.RefType:
		return !isBorrowType(t)
	}
	return false
}

// isOwnedMovable reports whether t is an owned reference-shaped value, the kind a
// move at an owned destination consumes. Value types copy and borrows alias, so
// neither moves at an owned site. An owned object, tuple, or owned-mutable RefType
// moves, as does a bare type-parameter variable: generic code treats a `T` value as
// non-duplicable, the conservative affine assumption that makes
// `fn dup<T>(x: T) -> [T, T]` a double move.
//
// A borrow moves only when it escapes to a longer-lived region, which a module-level
// write forces; that case is consumed through consumeAtGlobalWrite, not here.
func isOwnedMovable(t soltype.Type) bool {
	if isBorrowType(t) {
		return false
	}
	return isReferenceShaped(t)
}

// currentStmtRef resolves the statement currently being walked to its CFG point.
// It is the program point a use or move records against. ok is false outside a
// function body or when the statement has no CFG ref.
func (c *checker) currentStmtRef() (liveness.StmtRef, bool) {
	if c.fn == nil || c.fn.stmtToRef == nil || c.fn.currentStmt == nil {
		return liveness.StmtRef{}, false
	}
	ref, ok := c.fn.stmtToRef[c.fn.currentStmt]
	return ref, ok
}

// recordUse records a read of identifier e whose value is reference-shaped, so
// checkUseAfterMoves can later test it against the consumed lattice. Borrows are
// recorded alongside owned values, because a borrow stored into a 'static global is
// consumed too. A read of a value type, a non-local, or a binding outside a function
// body records nothing.
func (c *checker) recordUse(e *ast.IdentExpr, t soltype.Type) {
	if c.fn == nil || c.fn.cfg == nil || e.VarID <= 0 {
		return
	}
	if !isReferenceShaped(t) {
		return
	}
	ref, ok := c.currentStmtRef()
	if !ok {
		return
	}
	c.fn.useSites = append(c.fn.useSites, moveUse{
		varID: liveness.VarID(e.VarID),
		ref:   ref,
		node:  e,
		name:  e.Name,
	})
}

// consumeOwned records a move of the owned binding the source expression names,
// at the given program point, blaming moveNode for the consume. The source must be
// a plain identifier bound to an owned-movable value; a borrow, value type, fresh
// literal, or member path consumes nothing here.
//
// It does NOT force the moved value's borrows to 'static. A return or local store
// flows the value out at the call's own lifetime, not 'static, so forcing here would
// wrongly collapse a finite param-lifetime borrow — `fn (p: &'a {x}) -> &'a {x}`
// returns p at 'a, not 'static. The 'static forcing runs only where the destination
// is genuinely permanent, the module-level write in consumeAtGlobalWrite.
func (c *checker) consumeOwned(source ast.Expr, sourceT soltype.Type, moveNode ast.Node, ref liveness.StmtRef) {
	if c.fn == nil || c.fn.cfg == nil || sourceT == nil {
		return
	}
	ident, ok := source.(*ast.IdentExpr)
	if !ok || ident.VarID <= 0 {
		return
	}
	if !isOwnedMovable(sourceT) {
		return
	}
	c.recordMove(liveness.VarID(ident.VarID), moveNode, ref)
}

// movesSourceInto reports whether flowing source into a destination of type destT
// moves the owned binding source names: source is an identifier bound to an
// owned-movable value and the destination takes ownership rather than borrowing. A
// borrow destination — a `&` annotation or an explicit `&source` initializer, which
// is a BorrowExpr rather than a plain identifier — keeps the source aliased and
// governed by the exclusivity rule, not consumed. It is the shared move-or-borrow
// decision for `val`/`var` bindings and reassignments.
func (c *checker) movesSourceInto(source ast.Expr, destT soltype.Type) bool {
	if source == nil || isBorrowType(destT) {
		return false
	}
	ident, ok := source.(*ast.IdentExpr)
	if !ok || ident.VarID <= 0 {
		return false
	}
	return isOwnedMovable(c.info.TypeOf(source))
}

// consumeBindingInit moves the owned source a `val`/`var` initializer names into
// the new binding. `val q = p` for an owned p consumes p; a borrow binding leaves
// p usable, whether the borrow comes from a `&` annotation — `val q: &{x} = p` — or
// an explicit `&p` initializer, which is a BorrowExpr rather than a plain
// identifier and so names no owned source to consume.
func (c *checker) consumeBindingInit(vd *ast.VarDecl, bindingT soltype.Type, stmt ast.Stmt) {
	if !c.movesSourceInto(vd.Init, bindingT) {
		return
	}
	ref, ok := c.fn.stmtToRef[stmt]
	if !ok {
		return
	}
	c.consumeOwned(vd.Init, c.info.TypeOf(vd.Init), vd.Init, ref)
}

// consumeAtGlobalWrite consumes the source binding of a module-level store. A store
// into a 'static global permanently transfers the value, so it consumes the source
// whether owned or a borrow — using the source afterward could mutate what the global
// now reads, the leak the affine rule closes. A value-type source copies and a
// non-identifier source names no binding, so neither consumes.
func (c *checker) consumeAtGlobalWrite(source ast.Expr, sourceT soltype.Type, moveNode ast.Node, ref liveness.StmtRef) {
	if c.fn == nil || c.fn.cfg == nil || sourceT == nil {
		return
	}
	ident, ok := source.(*ast.IdentExpr)
	if !ok || ident.VarID <= 0 {
		return
	}
	if !isReferenceShaped(sourceT) {
		return
	}
	c.recordMove(liveness.VarID(ident.VarID), moveNode, ref)
}

// consumeIntoLiteral moves an owned identifier built into a fresh object or tuple
// literal: storing an owned value into the literal transfers ownership into it, so a
// later use of the source is a use-after-move. A value-type element copies and a
// non-identifier element names no binding, so neither consumes.
func (c *checker) consumeIntoLiteral(el ast.Expr, elemT soltype.Type) {
	if c.fn == nil || c.fn.cfg == nil {
		return
	}
	ref, ok := c.currentStmtRef()
	if !ok {
		return
	}
	c.consumeOwned(el, elemT, el, ref)
}

// recordMove marks varID consumed at ref. A second move of the same binding at the
// same program point is an intra-statement reuse — `return [x, x]`, `f(x, x)`, the
// `dup` double move — so it is reported immediately as a use-after-move rather than
// waiting for the lattice, which is statement-granular and cannot order two moves
// within one statement.
func (c *checker) recordMove(varID liveness.VarID, moveNode ast.Node, ref liveness.StmtRef) {
	if c.fn == nil || c.fn.moveSites == nil || varID <= 0 {
		return
	}
	at := c.fn.moveSites[ref]
	if at == nil {
		at = set.NewSet[liveness.VarID]()
		c.fn.moveSites[ref] = at
	}
	if at.Contains(varID) {
		c.report(&UseAfterMoveError{
			Name:     c.varIDToName(varID),
			use:      moveNode,
			moveSite: c.fn.moveNodes[varID],
		})
		return
	}
	at.Add(varID)
	if c.fn.consumed != nil {
		c.fn.consumed.Add(varID)
	}
	if c.fn.moveNodes == nil {
		c.fn.moveNodes = map[liveness.VarID]ast.Node{}
	}
	c.fn.moveNodes[varID] = moveNode
}

// sourceAlreadyMoved reports whether source names a binding the walk has already
// consumed. A binding that borrows or aliases such a source is a use-after-move, which
// the move engine reports, so the mutability-transition check skips it rather than
// raising a redundant exclusivity conflict off the moved value's stale 'static-escape
// state.
//
// The consumed set accumulates over the whole walk and is not path-sensitive, so a
// move on one branch suppresses the exclusivity check for the same binding on a
// sibling branch where it was not moved. That is a narrow false negative in the
// exclusivity diagnostic, traded for keeping the common straight-line case free of a
// double report. A path-sensitive answer would need the consumed lattice, which is not
// built until after the walk.
func (c *checker) sourceAlreadyMoved(source ast.Expr) bool {
	if c.fn == nil || c.fn.consumed == nil {
		return false
	}
	ident, ok := source.(*ast.IdentExpr)
	if !ok || ident.VarID <= 0 {
		return false
	}
	return c.fn.consumed.Contains(liveness.VarID(ident.VarID))
}

// checkUseAfterMoves runs the consumed-lattice dataflow over the function body's
// CFG and reports a UseAfterMoveError for every recorded read of a binding the
// lattice finds Moved or MaybeMoved at the read's program point. It runs once,
// after the whole body is walked, so a move on a later or loop-back path is
// already recorded when a use is checked.
//
// StateBefore reads the binding's state just before the read's statement, so a move
// recorded AT that statement — the consume in `val q = p`, where reading p and
// moving it share one statement — does not flag its own source read.
func (c *checker) checkUseAfterMoves() {
	if c.fn == nil || c.fn.cfg == nil || len(c.fn.useSites) == 0 {
		return
	}
	info := liveness.AnalyzeMoves(c.fn.cfg, c.fn.moveSites)
	for _, u := range c.fn.useSites {
		state := info.StateBefore(u.ref, u.varID)
		if state == liveness.NotMoved {
			continue
		}
		c.report(&UseAfterMoveError{
			Name:        u.name,
			Conditional: state == liveness.MaybeMoved,
			use:         u.node,
			moveSite:    c.fn.moveNodes[u.varID],
		})
	}
}
