package solver

import (
	"fmt"
	"slices"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/printer"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// This file holds the diagnostics of the exclusivity rules E2 and E3 in
// planning/ownership_rules/requirements.md, which the dataflow pass in ownership_dataflow.go
// reports, and the check that each borrow is written, as F4 states. It also records the loans
// a store leaves a binding holding, which the frame-exit checks in return_escape.go read.
//
// Data a borrow can write through may not be reachable at the same time through a borrow that
// expects it to hold still. A mutable borrow beside an immutable one is the pair that breaks.
// The immutable view promises stability and the mutable view can take it away. Two mutable
// borrows of one value are allowed, since both views agree the data can change, and with one
// thread neither can observe a tear. Two immutable borrows are fine, since neither writes.

// BorrowAliasError reports two borrows of overlapping data live at once that disagree about
// whether it can change, so one can write through its view and the other expects it to hold
// still.
type BorrowAliasError struct {
	// Place is what the blamed borrow reaches and FirstPlace what the borrow already live
	// reaches, each `x` for a whole binding and `x.a` for a field. They overlap and are often
	// equal, but a borrow of a whole binding conflicts with a borrow of one of its fields, and
	// then naming only one of them leaves the reader hunting for the other.
	Place      string
	FirstPlace string
	// FirstMut and SecondMut say whether a write can go through each borrow. Exactly one of them
	// is true, since a conflicting pair is one mutable borrow beside one immutable borrow. Second is
	// the one the error is blamed on, since it is where the program first holds two views.
	FirstMut  bool
	SecondMut bool
	// FirstVia and SecondVia name the closure a call may run, when the matching borrow is that
	// closure's access to what it captures during the call. Each is a phrase closureVia or
	// retainedVia returns, and is empty for a borrow the program writes. FirstRuns and
	// SecondRuns mark a closure the call is sure to run, because the call's callee names it.
	FirstVia   string
	SecondVia  string
	FirstRuns  bool
	SecondRuns bool
	// `root` is the VarID of the local Place starts with, and `places` holds the place of the
	// blamed borrow and then of the live one.
	root   liveness.VarID
	places [2]movePlace
	// `exits` holds the span of each return a holder of either path flows into, so
	// frameExitReported can match a frame-exit diagnostic at that return.
	exits []ast.Span
	node  ast.Node
	first ast.Span
}

func (*BorrowAliasError) isSolverError()        {}
func (e *BorrowAliasError) Span() ast.Span      { return e.node.Span() }
func (e *BorrowAliasError) Related() []ast.Span { return []ast.Span{e.first} }
func (e *BorrowAliasError) Message() string {
	// One place covers both borrows when they name the same data, so "it" is unambiguous.
	// Where the two differ the other place is named, since the reader cannot otherwise tell
	// which part of the binding the live borrow holds.
	held := "it is"
	if e.FirstPlace != e.Place {
		held = fmt.Sprintf("'%s' is", e.FirstPlace)
	}
	if e.SecondVia != "" {
		if e.SecondMut {
			return fmt.Sprintf("this call %s %s, which writes '%s' while %s borrowed as immutable", runsVerb(e.SecondRuns), e.SecondVia, e.Place, held)
		}
		return fmt.Sprintf("this call %s %s, which reads '%s' while %s borrowed as mutable", runsVerb(e.SecondRuns), e.SecondVia, e.Place, held)
	}
	if e.FirstVia != "" {
		if e.FirstMut {
			return fmt.Sprintf("cannot borrow '%s' as immutable in a call that %s %s, which writes it", e.Place, runsVerb(e.FirstRuns), e.FirstVia)
		}
		return fmt.Sprintf("cannot borrow '%s' as mutable in a call that %s %s, which reads it", e.Place, runsVerb(e.FirstRuns), e.FirstVia)
	}
	if e.SecondMut {
		return fmt.Sprintf("cannot borrow '%s' as mutable while %s borrowed as immutable", e.Place, held)
	}
	return fmt.Sprintf("cannot borrow '%s' as immutable while %s borrowed as mutable", e.Place, held)
}

// MoveWhileBorrowedError reports a move of data a live borrow still reaches. After
// `val y = b` the new owner y holds the data, and a borrow taken from b still points at it.
type MoveWhileBorrowedError struct {
	// Place names the data moved, `x` for a whole binding and `x.a` for a field.
	Place string
	// Via names the closure the call may run, when the borrow is the write that closure makes
	// during the call, and is empty otherwise. It is a phrase closureVia or retainedVia returns.
	// ViaRuns marks a closure the call is sure to run, because the call's callee names it.
	Via     string
	ViaRuns bool
	// `root` is the VarID of the local Place starts with, and `places` holds the place moved
	// and then the place of the blamed borrow.
	root   liveness.VarID
	places [2]movePlace
	// `exits` holds the span of each expression that carries the moved value out of the
	// body. ownedExits finds them, and frameExitReported matches a frame-exit diagnostic
	// against them.
	exits  []ast.Span
	move   ast.Node
	borrow ast.Span
}

func (*MoveWhileBorrowedError) isSolverError()        {}
func (e *MoveWhileBorrowedError) Span() ast.Span      { return e.move.Span() }
func (e *MoveWhileBorrowedError) Related() []ast.Span { return []ast.Span{e.borrow} }
func (e *MoveWhileBorrowedError) Message() string {
	if e.Via != "" {
		return fmt.Sprintf("cannot move '%s' in a call that %s %s, which writes it", e.Place, runsVerb(e.ViaRuns), e.Via)
	}
	return fmt.Sprintf("cannot move '%s' while it is borrowed", e.Place)
}

// runsVerb returns how a message says a call reaches a closure: "runs" when the call is sure
// to run it and "may run" otherwise.
func runsVerb(runs bool) string {
	if runs {
		return "runs"
	}
	return "may run"
}

// BorrowedValueWriteError reports a field write through the owner while an immutable borrow of
// the written data is live.
type BorrowedValueWriteError struct {
	// Place names the field written, `x.v` for `x.v = 5`.
	Place  string
	write  ast.Node
	borrow ast.Span
}

func (*BorrowedValueWriteError) isSolverError()        {}
func (e *BorrowedValueWriteError) Span() ast.Span      { return e.write.Span() }
func (e *BorrowedValueWriteError) Related() []ast.Span { return []ast.Span{e.borrow} }
func (e *BorrowedValueWriteError) Message() string {
	return fmt.Sprintf("cannot assign to '%s' while it is borrowed as immutable", e.Place)
}

// MissingBorrowError is an error whose fix writes a borrow before the expression it reports.
// ImplicitBorrowArgError and ImplicitBorrowError implement it.
type MissingBorrowError interface {
	SolverError
	// Borrow returns the operator the expression needs written before it, `&` or `&mut`.
	Borrow() string
	// Expr returns the expression the borrow is written before. Its span is the error's span.
	Expr() ast.Expr
	// ShorthandKey returns the property name when Expr is the key of a shorthand property
	// such as `{f}`, and "" for any other expression. The borrow of a shorthand key is
	// written by spelling the property out, as in `{f: &f}`.
	ShorthandKey() string
}

// ImplicitBorrowArgError reports an argument filling a `&` or `&mut` parameter without a borrow
// written at the call.
type ImplicitBorrowArgError struct {
	// Written is the borrow the argument needs, `&` or `&mut`, taken from the parameter.
	Written string
	node    ast.Expr
}

func (e *ImplicitBorrowArgError) Borrow() string      { return e.Written }
func (e *ImplicitBorrowArgError) Expr() ast.Expr      { return e.node }
func (*ImplicitBorrowArgError) ShorthandKey() string  { return "" }
func (*ImplicitBorrowArgError) isSolverError()        {}
func (e *ImplicitBorrowArgError) Span() ast.Span      { return e.node.Span() }
func (e *ImplicitBorrowArgError) Related() []ast.Span { return nil }
func (e *ImplicitBorrowArgError) Message() string {
	return fmt.Sprintf("this argument is borrowed by the callee, so write the borrow: `%s`", e.Written)
}

// ImplicitBorrowError reports a value filling a `&` or `&mut` destination without a borrow
// written where it flows. The destination is a binding, a reassignment, a field or element
// store, a setter's parameter, a `return`, or an element of a literal. A call argument reports
// ImplicitBorrowArgError instead.
type ImplicitBorrowError struct {
	// Written is the borrow the value needs, `&` or `&mut`, taken from the destination's type.
	Written string
	node    ast.Expr
	// shorthandKey is what ShorthandKey returns.
	shorthandKey string
}

func (e *ImplicitBorrowError) Borrow() string       { return e.Written }
func (e *ImplicitBorrowError) Expr() ast.Expr       { return e.node }
func (e *ImplicitBorrowError) ShorthandKey() string { return e.shorthandKey }
func (*ImplicitBorrowError) isSolverError()         {}
func (e *ImplicitBorrowError) Span() ast.Span       { return e.node.Span() }
func (e *ImplicitBorrowError) Related() []ast.Span  { return nil }
func (e *ImplicitBorrowError) Message() string {
	return fmt.Sprintf("this value is borrowed by its destination, so write the borrow: `%s`", e.Written)
}

// OptionalChainBorrowError reports an optional chain that fills a borrow destination without a
// borrow written. The fix ImplicitBorrowError suggests does not apply, because a borrow of an
// optional chain such as `&h?.f` is not supported. The program binds the receiver first and
// borrows the plain read through it, as in `if val k = h { val d = &k.f }`.
type OptionalChainBorrowError struct {
	node ast.Node
}

func (*OptionalChainBorrowError) isSolverError()        {}
func (e *OptionalChainBorrowError) Span() ast.Span      { return e.node.Span() }
func (e *OptionalChainBorrowError) Related() []ast.Span { return nil }
func (e *OptionalChainBorrowError) Message() string {
	return "this optional chain is borrowed by its destination, and an optional chain cannot be borrowed, " +
		"so bind the receiver with `if val` and borrow the field through it"
}

// `loan` is one borrow a call's store effect or a store into a field leaves a binding holding.
// The frame-exit checks in return_escape.go read the loans to tell which stores still reach
// the caller.
type loan struct {
	// place is the data the borrow reaches.
	place movePlace
	// `mut` says a write can go through this borrow.
	mut bool
	// `holder` is the binding the borrow is stored in. The loan lasts while that binding is live.
	holder liveness.VarID
	// holderPath is the field path within holder the borrow landed at, empty when the whole
	// binding took it. `b.peer = &mut d` lands at [peer]. Repointing that field ends the loan
	// there and leaves a sibling field's loan alone, which is the strong update the borrow
	// graph makes for the same statement.
	holderPath []placeSeg
	// `ref` is the statement the borrow is stored at.
	ref liveness.StmtRef
	// callerOwned marks a loan held by a caller-owned target, a borrow parameter or a borrowing
	// receiver. The caller can read such a target once the function returns.
	callerOwned bool
	// endSeq is the sequence at which a store into the holder ended this loan, and 0 while it
	// still holds. The loan stays in the list, so a check can tell an ended store from one that
	// was never recorded.
	endSeq int
}

// nextLoanSeq returns the sequence number the next ended loan takes. It counts up across the
// whole body, so a number handed out once is never handed out again.
func (c *checker) nextLoanSeq() int {
	c.fn.loanSeq++
	return c.fn.loanSeq
}

// loanPlace returns the place a borrow argument reaches. `&mut x.a` reaches x.a, and a bare
// name already holding a borrow reaches whatever that name reaches, which for now is the name
// itself. ok is false when the operand is not a place, such as a call result.
func (c *checker) loanPlace(arg ast.Expr) (movePlace, bool) {
	p, ok := c.exprPlace(borrowOperand(arg))
	if !ok || p.root <= 0 {
		return movePlace{}, false
	}
	return p, true
}

// placesOverlap reports whether two places reach any of the same data. They must be rooted at
// the same binding, and one field path must be a prefix of the other. `x.a` and `x.a.b` overlap
// because the second sits inside the first; `x.a` and `x.b` do not.
func placesOverlap(a, b movePlace) bool {
	return a.root == b.root && pathPrefixRelated(a.path, b.path)
}

// isFuncNode reports whether n is a function or closure.
func isFuncNode(n ast.Node) bool {
	_, ok := n.(ast.Func)
	return ok
}

// capturedLocal is a local of the current body that a closure captures.
type capturedLocal struct {
	root liveness.VarID
	// `mut` says the closure writes the local.
	mut bool
	// paths are the field paths under root that the closure reads or writes, as
	// capturedPaths finds them. An empty list means the closure captures the whole local.
	paths [][]placeSeg
	// `t` is the type of the local's binding.
	t soltype.Type
}

// places returns each place under the local that the closure captures.
func (l capturedLocal) places() []movePlace {
	if len(l.paths) == 0 {
		return []movePlace{{root: l.root}}
	}
	out := make([]movePlace, len(l.paths))
	for i, path := range l.paths {
		out[i] = movePlace{root: l.root, path: path}
	}
	return out
}

// endLoansOnRebind ends the loans `holder` holds when `init` gives it a new value, since the
// binding no longer reaches what it stored before. `a = a` leaves `a` holding what it held.
func (c *checker) endLoansOnRebind(holder liveness.VarID, init ast.Expr) {
	if c.fn == nil || holder <= 0 || init == nil {
		return
	}
	if ident, isIdent := init.(*ast.IdentExpr); isIdent && c.varIDs.Of(ident) == holder {
		return
	}
	c.dropLoansHeldBy(holder)
}

// dropLoansHeldBy ends the loans bound to holder, which a reassignment of that binding has made
// unreachable from here on. They stay in the list carrying the sequence they ended at, so a read
// walked before the reassignment is still weighed against them.
func (c *checker) dropLoansHeldBy(holder liveness.VarID) {
	ended := c.nextLoanSeq()
	for i := range c.fn.loans {
		if c.fn.loans[i].holder == holder && c.fn.loans[i].endSeq == 0 {
			c.fn.loans[i].endSeq = ended
		}
	}
}

// endLoansAt ends the loans stored in `holder`'s field `base`, or in a field nested inside it. A
// store into that field replaces what it held, so those loans reach nothing from here on.
// `b.peer = &mut e` after `b.peer = &mut d` ends the loan of d. A loan stored in `b.peer.next`
// ends too, while one stored in the sibling field `b.data` keeps holding. Like dropLoansHeldBy,
// it marks each loan with the sequence it ended at rather than erasing it, so a read walked
// before the store is still weighed against what the field held then.
func (c *checker) endLoansAt(holder liveness.VarID, base []placeSeg) {
	c.endLoansWhere(holder, base, func(loan) bool { return true })
}

// endLoansAtPostDominating is endLoansAt limited to the loans whose statement every path to the
// end of the function leads through `ref`. That holds when `ref`'s block post-dominates the loan's
// block, meaning every path from the loan's block to the CFG exit passes through `ref`'s block. A
// loan with a path to the exit that skips `ref` keeps holding.
func (c *checker) endLoansAtPostDominating(holder liveness.VarID, base []placeSeg, ref liveness.StmtRef) {
	c.endLoansWhere(holder, base, func(l loan) bool { return c.blockPostDominates(ref.BlockID, l.ref.BlockID) })
}

// stmtReaches reports whether the statement at `to` can run after the statement at `from` in
// `cfg`. That holds for a later statement of the same block, and for any statement of a block the
// CFG reaches from `from`'s block.
func stmtReaches(cfg *liveness.CFG, from, to liveness.StmtRef) bool {
	if from.BlockID == to.BlockID && from.StmtIdx <= to.StmtIdx {
		return true
	}
	if cfg == nil || from.BlockID < 0 || from.BlockID >= len(cfg.Blocks) {
		return true
	}
	seen := set.NewSet[int]()
	pending := slices.Clone(cfg.Blocks[from.BlockID].Successors)
	for len(pending) > 0 {
		b := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if b.ID == to.BlockID {
			return true
		}
		if seen.Contains(b.ID) {
			continue
		}
		seen.Add(b.ID)
		pending = append(pending, b.Successors...)
	}
	return false
}

// blockPostDominates reports whether every path from block `from` to the CFG exit passes through
// block `by`. A block post-dominates itself.
func (c *checker) blockPostDominates(by, from int) bool {
	if by == from {
		return true
	}
	// Without a CFG, or with a block ID outside it, nothing is known about the paths, so the
	// answer is the safe one. A caller then keeps the loan holding.
	cfg := c.fn.cfg
	if cfg == nil || by < 0 || by >= len(cfg.Blocks) || from < 0 || from >= len(cfg.Blocks) {
		return false
	}
	// Walk forward from `from` depth-first and treat `by` as a wall. The walk never steps into
	// `by` or past it, so any block it does reach has a path from `from` that avoids `by`.
	seen := set.NewSet[int]()
	pending := []*liveness.BasicBlock{cfg.Blocks[from]}
	for len(pending) > 0 {
		b := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		// A path into `by` passes through it, so it is no counterexample. A block already
		// visited has had its successors queued once, which also stops the walk on a loop.
		if b.ID == by || seen.Contains(b.ID) {
			continue
		}
		// Reaching the exit means some path from `from` gets there without passing `by`.
		if b == cfg.Exit {
			return false
		}
		seen.Add(b.ID)
		pending = append(pending, b.Successors...)
	}
	// The walk ran out of blocks without reaching the exit, so every path to it runs into `by`.
	return true
}

// endLoansWhere ends each loan stored in `holder`'s field `base`, or in a field nested inside it,
// for which `selects` returns `true`. It marks each one with the sequence it ended at rather than
// erasing it, so a read walked before that point is still weighed against the loan.
func (c *checker) endLoansWhere(holder liveness.VarID, base []placeSeg, selects func(loan) bool) {
	ended := c.nextLoanSeq()
	for i := range c.fn.loans {
		l := &c.fn.loans[i]
		if l.holder == holder && l.endSeq == 0 && pathHasPrefix(l.holderPath, base) && selects(*l) {
			l.endSeq = ended
		}
	}
}

// recordStoreEdgeLoan records the loan a call's store effect creates. `store(&mut p, &mut b)`
// against a signature that writes its second argument into the first leaves p reaching b, so
// from that point p holds a mutable borrow of b. An immutable borrow of b then conflicts with
// it. A second mutable borrow does not, since two mutable borrows of one value are allowed.
//
// Whether a write can go through the loan comes from the SOURCE parameter, since that is the
// view the target ends up holding. A signature storing a `&'a B` leaves the target able to read
// the item and not to write it, even though the target itself is a mutable borrow.
//
// `place` is the data the stored borrow reaches and `target` is the binding it lands in, so the
// loan is a borrow of `place` held by `target`. It lasts as long as `target` is live, the same rule a
// borrow bound to a name follows, and that holds for a target whose referent belongs to the
// caller as well. targetPath is the field of `target` the borrow lands at, so a later store into
// that field can end this loan and leave a sibling field's alone.
func (c *checker) recordStoreEdgeLoan(place movePlace, mut bool, target liveness.VarID, targetPath []placeSeg, ref liveness.StmtRef) {
	if c.fn == nil || target <= 0 || place.root <= 0 {
		return
	}
	fresh := loan{
		place: place, mut: mut, holder: target, holderPath: targetPath, ref: ref,
		callerOwned: c.paramReferentOutlivesFrame(target),
	}
	// One signature can write an argument into several positions of the target, so the same
	// loan reaches here once per position, and it is recorded once.
	for _, l := range c.fn.loans {
		if l.holder == fresh.holder && l.ref == fresh.ref &&
			l.mut == fresh.mut && placesEqual(l.place, fresh.place) &&
			slices.Equal(l.holderPath, fresh.holderPath) {
			return
		}
	}
	c.fn.loans = append(c.fn.loans, fresh)
}

// placesEqual reports whether two places name the same data, root and full field path alike.
func placesEqual(a, b movePlace) bool {
	return a.root == b.root && slices.Equal(a.path, b.path)
}

// writtenPlaceName renders target, the expression a write names, as written. When the
// printer cannot render target, it returns fallback.
func writtenPlaceName(target ast.Node, fallback string) string {
	if text, err := printer.Print(target, printer.DefaultOptions()); err == nil {
		return text
	}
	return fallback
}

// checkExplicitBorrowArgs reports an argument that fills a borrow parameter without a borrow
// written at the call. A borrow is where a second view of a value comes into existence, and
// every borrow rule keys on it, so the call site is where it should be visible. missingBorrow
// lists the arguments that need nothing written. An argument that is an object or tuple literal
// has each element checked against the parameter's matching slot, as at any other flow site.
//
// A method call's RECEIVER is not an argument and does not reach here. It keeps auto-borrowing,
// because the method's signature already names the mode: `bump(&mut self)` says the receiver is
// taken mutably, and `c.bump()` has no second reading for a written borrow to disambiguate.
func (c *checker) checkExplicitBorrowArgs(e *ast.CallExpr, fn *soltype.FuncType) {
	for i, arg := range e.Args {
		if i >= len(fn.Params) || fn.Params[i].Rest {
			// A rest parameter gathers its arguments into a value the callee owns, so none of
			// them fills a borrow slot. #1530 covers what the borrow rules still miss there.
			break
		}
		c.checkBorrowsWritten(arg, fn.Params[i].Type, true)
	}
}

// missingBorrow returns the borrow, `&` or `&mut`, that src needs written to fill a destination
// of type destT. A nil destT stands for a destination that takes its type from src, such as an
// unannotated binding. It returns false when src needs nothing written.
//
// Two sources need a borrow written:
//
//   - An owned value filling a borrow destination. The destination decides the mutability.
//   - A field read through a borrow that fieldReadThroughBorrow recognizes. It needs the borrow
//     written when it fills a borrow destination, a nil destT, or a destination typed by a type
//     variable, as a generic parameter is. A borrow destination decides the mutability, and
//     otherwise the receiver's borrow does.
//
// These sources need nothing:
//
//   - A borrow expression, `&e` or `&mut e`.
//   - Any other value whose own type is a borrow. `fn g(m: &mut T) { f(m) }` hands on a
//     borrow g holds rather than creating one.
//   - A value type, which has no borrow form.
//   - A value whose type has not settled on an owned shape. What it becomes decides whether a
//     borrow is missing, and the caller cannot wait for it.
//
// A field read through a borrow that fills an owned destination is left to the constraint
// that rejects a borrow in an owned slot, since a written borrow would not fill it either.
func (c *checker) missingBorrow(src ast.Expr, destT soltype.Type) (string, bool) {
	if src == nil {
		return "", false
	}
	if _, written := src.(*ast.BorrowExpr); written {
		return "", false
	}
	var mut, isBorrow bool
	if destT != nil {
		var borrow typeBorrow
		borrow, isBorrow = c.borrowOfType(destT)
		mut = borrow.mut
	}
	_, destIsVar := destT.(*soltype.TypeVarType)
	if readMut, isRead := c.fieldReadThroughBorrow(src); isRead && (destT == nil || destIsVar || isBorrow) {
		if !isBorrow {
			mut = readMut
		}
		return borrowOperator(mut), true
	}
	if !isBorrow || !c.isSettledOwned(c.info.TypeOf(src)) {
		return "", false
	}
	return borrowOperator(mut), true
}

// borrowOperator returns the operator that writes a borrow of the given mutability.
func borrowOperator(mut bool) string {
	if mut {
		return "&mut"
	}
	return "&"
}

// fieldReadThroughBorrow reports whether src is a member read through a borrow that yields a
// borrow of the field, and whether the receiver's borrow is `&mut`. With `line: &Line`, `line.start`
// is such a read when `start` is declared as an object, tuple, class instance, or alias of
// one. These reads yield something else and return false:
//
//   - A read off an owned receiver, which yields the owned field.
//   - A read of a field declared as a borrow, which hands on the field's own borrow.
//   - A read that ends at a value type or a function, which copies.
//   - A read off a receiver whose shape is not statically known, which has no declared field.
func (c *checker) fieldReadThroughBorrow(src ast.Expr) (bool, bool) {
	member, isMember := src.(*ast.MemberExpr)
	if !isMember || member.Prop == nil {
		return false, false
	}
	recvT, prop, declared := c.declaredReceiverField(member)
	if !declared {
		return false, false
	}
	_, recvMut, recvLt := soltype.UnwrapRef(recvT)
	if recvLt == nil {
		return false, false
	}
	fieldT := c.expandAliasChain(prop.Type)
	if _, fieldIsBorrow := c.borrowOfType(fieldT); fieldIsBorrow {
		return false, false
	}
	// A borrowed receiver carries a lifetime, so fieldBorrow mints none and its level
	// argument goes unread.
	if _, isRead := c.fieldBorrow(fieldT, nil, recvMut, recvLt, 0); !isRead {
		return false, false
	}
	// A field declared `mut` reads as `&mut` even through `&`. Writing `&mut r.f` needs a
	// `&mut` receiver, so the written borrow takes the receiver's mutability.
	return recvMut, true
}

// declaredReceiverField returns the receiver type member reads off and the field it reads,
// according to the declared types along member's chain. The receiver type has `null` and
// `undefined` removed. It returns false when a link's type or field is not known.
func (c *checker) declaredReceiverField(member *ast.MemberExpr) (soltype.Type, *soltype.PropertyElem, bool) {
	recvT := c.declaredRead(member.Object)
	if recvT == nil {
		return nil, nil, false
	}
	recvT, _ = definedReceiver(recvT)
	prop, declared := c.declaredField(recvT, member.Prop.Name)
	return recvT, prop, declared
}

// declaredRead returns the type the read e yields according to the declared types along its
// member chain, or nil when a link's type is not known. A link that is not a member read
// takes the type recorded for it. A member read takes its field's declared type, wrapped as
// fieldBorrow wraps it when the receiver is a borrow. An optional field or link reads as the
// field without the `undefined` it may add.
//
// The declared types name each link of a chain read through an immutable borrow, where the
// recorded types do not. With `h: &{f: {g: T}}`, the recorded type of `h.f` borrows a
// variable rather than `{g: T}`, so it declares no field `g` for `h.f.g` to read.
func (c *checker) declaredRead(e ast.Expr) soltype.Type {
	member, isMember := e.(*ast.MemberExpr)
	if !isMember || member.Prop == nil {
		t := c.info.TypeOf(e)
		if t == nil {
			return nil
		}
		return c.expandAliasChain(t)
	}
	recvT, prop, declared := c.declaredReceiverField(member)
	if !declared {
		return nil
	}
	fieldT := c.expandAliasChain(prop.Type)
	if _, recvMut, recvLt := soltype.UnwrapRef(recvT); recvLt != nil {
		if borrow, ok := c.fieldBorrow(fieldT, nil, recvMut, recvLt, 0); ok {
			return borrow
		}
	}
	return fieldT
}

// typeBorrow is the borrow a type stands for. A borrow member is a RefType carrying a
// lifetime, and a bare borrow is its own single member. mut marks a borrow whose borrow
// members are all `&mut`. anyMut marks one with at least one `&mut` borrow member. lt is the
// borrow's lifetime.
type typeBorrow struct {
	mut    bool
	anyMut bool
	lt     soltype.Lifetime
}

// borrowOfType returns the borrow a value of type t is, and false when t is not a borrow. t is
// a borrow when it is a RefType carrying a lifetime. A union is a borrow when it has at least
// one such member and every other member is a value type, as `&T | undefined` and
// `&T | number` are. An alias counts as what it names, at the top of t and as a member of the
// union. The borrow's lifetime is `'static` when any borrow member's is, and the first borrow
// member's otherwise.
func (c *checker) borrowOfType(t soltype.Type) (typeBorrow, bool) {
	// A type that is not a union is a borrow only when it is a RefType with a lifetime.
	t = c.expandAliasChain(t)
	u, isUnion := t.(*soltype.UnionType)
	if !isUnion {
		r, isRef := t.(*soltype.RefType)
		if !isRef || r.Lt == nil {
			return typeBorrow{}, false
		}
		return typeBorrow{mut: r.Mut, anyMut: r.Mut, lt: r.Lt}, true
	}

	// Fold each borrow member into out. mut starts true so it stays true only while every
	// borrow member is `&mut`.
	out := typeBorrow{mut: true}
	found := false
	for _, member := range u.Types {
		member = c.expandAliasChain(member)
		if r, isRef := member.(*soltype.RefType); isRef && r.Lt != nil {
			// A value of the union may be any of its members, so the borrow lasts as long as
			// its longest-lived member. `'static` outlives every other lifetime. Two lifetime
			// variables have no known order, so the first one stands for both.
			if !found || soltype.IsStaticLifetime(r.Lt) {
				out.lt = r.Lt
			}
			out.mut = out.mut && r.Mut
			out.anyMut = out.anyMut || r.Mut
			found = true
			continue
		}

		// Any member other than a borrow must be a value type, such as `undefined` or
		// `number`, which holds nothing a borrow could reach.
		if !c.holdsValueType(member) {
			return typeBorrow{}, false
		}
	}

	// A union of value types alone borrows nothing.
	if !found {
		return typeBorrow{}, false
	}
	return out, true
}

// isSettledOwned reports whether a value of type t is owned and its shape is known. A union
// counts when every member does. A read of a module-level binding inside a function types as a
// variable whose lower bound is the binding's type, so a variable counts when such a bound does.
func (c *checker) isSettledOwned(t soltype.Type) bool {
	if u, isUnion := c.expandAliasChain(t).(*soltype.UnionType); isUnion {
		return len(u.Types) > 0 && !slices.ContainsFunc(u.Types, func(member soltype.Type) bool {
			return !c.isSettledOwned(member)
		})
	}
	return c.holdsConcreteOwned(t, set.NewSet[*soltype.TypeVarType]())
}

// checkWrittenBorrow reports src when it fills a destination of type destT that borrows it
// without a borrow written, as checkBorrowsWritten does for a destination that is not a call
// argument. It returns whether anything in src was reported, and a caller records no alias or
// move for src when it was.
func (c *checker) checkWrittenBorrow(src ast.Expr, destT soltype.Type) bool {
	return c.checkBorrowsWritten(src, destT, false)
}

// immutableView returns t with each `&mut` borrow in a positive position rewritten to `&`. A
// borrow in a function's parameter keeps its mutability, since loosening it would make t ask
// more of a function, not less.
func immutableView(t soltype.Type) soltype.Type {
	return t.Accept(immutableViewVisitor{}, soltype.Positive)
}

type immutableViewVisitor struct{}

func (immutableViewVisitor) EnterType(soltype.Type, soltype.Polarity) soltype.EnterResult {
	return soltype.EnterResult{}
}

func (immutableViewVisitor) ExitType(t soltype.Type, pol soltype.Polarity) soltype.Type {
	if ref, ok := t.(*soltype.RefType); ok && ref.Mut && pol == soltype.Positive {
		return &soltype.RefType{Mut: false, Lt: ref.Lt, Inner: ref.Inner}
	}
	return t
}

// checkTarget returns the type a source is checked against for a destination of type destT.
// missing says the source was already reported for a borrow it left unwritten. The check then
// reads destT through immutableView, so it reports a wrong shape but not the mutability the
// missing borrow would have supplied.
func checkTarget(destT soltype.Type, missing bool) soltype.Type {
	if missing {
		return immutableView(destT)
	}
	return destT
}

// checkBorrowsWritten reports each value src produces that fills a destination of type destT
// without the borrow destT needs written. arg says src is a call argument, which selects
// ImplicitBorrowArgError over ImplicitBorrowError. It returns whether anything was reported,
// including an element of a literal in src.
//
// A conditional, `match`, `do`, or `try` produces the value of one of its branches, so each
// branch's result is checked on its own and reported where its borrow belongs. A value that
// needs no borrow of its own and is an object or tuple literal has each element checked
// against the slot of destT it fills.
func (c *checker) checkBorrowsWritten(src ast.Expr, destT soltype.Type, arg bool) bool {
	reported := false
	for _, r := range branchResults(src) {
		written, missing := c.missingBorrow(r, destT)
		if !missing {
			if c.checkLiteralElemBorrows(r, destT) {
				reported = true
			}
			continue
		}
		switch {
		case hasOptionalLink(r):
			// `&h?.f` is rejected as a borrow of an optional chain, so suggesting it would
			// trade this error for that one.
			c.report(&OptionalChainBorrowError{node: r})
		case arg:
			c.report(&ImplicitBorrowArgError{Written: written, node: r})
		default:
			c.report(&ImplicitBorrowError{Written: written, node: r})
		}
		// A literal element is moved into the literal as the literal is built, before its
		// destination is known. The element is a borrow the program left unwritten, not a move.
		c.retractMove(r)
		if t := c.eventTable(); t != nil {
			t.unwritten.Add(r)
		}
		reported = true
	}
	if reported && c.fn != nil {
		if c.fn.unwrittenBorrows == nil {
			c.fn.unwrittenBorrows = set.NewSet[ast.Node]()
		}
		c.fn.unwrittenBorrows.Add(src)
	}
	return reported
}

// reportBorrowIntoOwned reports src when the value it produces, of type srcT, may be a borrow
// that fills an owned destination of type destT, as T6 states. It returns whether it
// reported. The flow out of a reported src reads as a plain read of its place, so the
// rejected borrow lends nothing.
func (c *checker) reportBorrowIntoOwned(src ast.Expr, srcT, destT soltype.Type) bool {
	ref := c.borrowMemberOf(srcT)
	if ref == nil {
		return false
	}
	c.report(&BorrowEscapeError{Sub: ref, Super: destT, site: src})
	if t := c.eventTable(); t != nil {
		t.unwritten.Add(src)
	}
	return true
}

// borrowMemberOf returns the borrow a value of type t may be, or nil when it is never one. A
// RefType with a lifetime is its own borrow, and a union returns its first such member, so
// `&{x: number} | null` returns `&{x: number}`.
func (c *checker) borrowMemberOf(t soltype.Type) *soltype.RefType {
	t = c.expandAliasChain(c.borrowBehindVar(t))
	members := []soltype.Type{t}
	if u, isUnion := t.(*soltype.UnionType); isUnion {
		members = u.Types
	}
	for _, m := range members {
		if ref, isRef := c.expandAliasChain(m).(*soltype.RefType); isRef && ref.Lt != nil {
			return ref
		}
	}
	return nil
}

// checkLiteralElemBorrows checks each element of an object or tuple literal src against the
// slot of destT it fills, through checkWrittenBorrow, and returns whether any element was
// reported. `{f: p}` filling `{f: &T}` reports p. A borrow `&{f: p}` filling `&{f: &T}` is
// checked against the borrow's inner type. A nil destT gives every element a nil slot. Any
// other src, and an element whose slot destT does not name, is left alone.
func (c *checker) checkLiteralElemBorrows(src ast.Expr, destT soltype.Type) bool {
	if destT != nil {
		destT = c.expandAliasChain(destT)
	}
	if b, isBorrow := src.(*ast.BorrowExpr); isBorrow {
		if destT == nil {
			return c.checkLiteralElemBorrows(b.Arg, nil)
		}
		if ref, isRef := destT.(*soltype.RefType); isRef {
			return c.checkLiteralElemBorrows(b.Arg, ref.Inner)
		}
		return false
	}
	reported := false
	check := func(el ast.Expr, slotT soltype.Type) {
		if c.checkWrittenBorrow(el, slotT) {
			reported = true
		}
	}
	switch lit := src.(type) {
	case *ast.ObjectExpr:
		var obj *soltype.ObjectType
		if destT != nil {
			var isObj bool
			if obj, isObj = soltype.CarrierOf(destT).(*soltype.ObjectType); !isObj {
				return false
			}
		}
		checkProp := func(prop *ast.PropertyExpr, slotT soltype.Type) {
			from := len(c.errs)
			check(prop.ValueExpr(), slotT)
			key, isIdent := prop.Name.(*ast.IdentExpr)
			if prop.Value != nil || !isIdent {
				return
			}
			// A shorthand property `{f}` has no Value, so its key is the expression checked. The
			// error on that key records the property name for ShorthandKey.
			for _, e := range c.errs[from:] {
				if missing, ok := e.(*ImplicitBorrowError); ok && missing.node == ast.Expr(key) {
					missing.shorthandKey = key.Name
				}
			}
		}
		for _, elem := range lit.Elems {
			prop, isProp := elem.(*ast.PropertyExpr)
			if !isProp {
				continue
			}
			if obj == nil {
				checkProp(prop, nil)
				continue
			}
			name, named := objKeyName(prop.Name)
			if !named {
				continue
			}
			if slot, found := obj.Prop(name); found {
				checkProp(prop, slot.Type)
			}
		}
	case *ast.TupleExpr:
		var elemT, target soltype.Type
		uniform := destT == nil
		if !uniform {
			target = soltype.CarrierOf(destT)
			elemT, uniform = c.ctx.arrayElem(target)
		}
		if uniform {
			for _, el := range lit.Elems {
				if _, spread := el.(*ast.ArraySpreadExpr); !spread {
					check(el, elemT)
				}
			}
			return reported
		}
		tuple, isTuple := target.(*soltype.TupleType)
		if !isTuple {
			return false
		}
		for i, el := range lit.Elems {
			// A spread fills a run of slots whose length the literal does not show, so no later
			// element has a known slot.
			if _, spread := el.(*ast.ArraySpreadExpr); spread || i >= len(tuple.Elems) {
				break
			}
			check(el, tuple.Elems[i])
		}
	}
	return reported
}
