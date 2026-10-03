package solver

import (
	"fmt"
	"slices"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// Borrow exclusivity. Data a borrow can write through may not be reachable at the same time
// through a borrow that expects it to hold still. A mutable borrow beside an immutable one is
// the pair that breaks. The immutable view promises stability and the mutable view can take it
// away.
//
// Two mutable borrows of one value are allowed. That is Rule 3 of
// planning/lifetimes/requirements.md, a deliberate departure from Rust: both views agree the
// data can change, and with one thread neither can observe a tear. `&mut` is invariant in its
// referent, so two mutable borrows of one place also agree on its type and neither can write a
// value the other would misread.
//
// Two immutable borrows are fine for the mirror-image reason, since neither writes.
//
// A loan is one borrow the checker tracks. It records the place the borrow reaches, whether a
// write can go through it, and how long it stays usable. Loans come from three sites:
//
//   - A `val`/`var` initializer that borrows a place, as in `val a = &mut x`. The binding holds
//     the loan, so the loan lasts as long as that binding is live.
//   - A call argument filling a `&` or `&mut` parameter. The callee holds it for the length of
//     the call and no name outlives that, so the loan lasts for the one statement.
//   - A call whose signature stores one argument into another, as in `store(&mut p, &mut b)`.
//     The target p holds the loan of b, so the loan lasts as long as p is live.
//
// A call argument's mutability comes from the PARAMETER, not from the argument. Passing
// `&mut x` to a `&T` parameter hands the callee a read-only view, so the callee cannot write
// through it and it counts as immutable. `f(&mut x, &mut x)` against `fn f(a: &T, b: &T)` is
// therefore accepted.
//
// Places carry field paths, so two borrows conflict only when their paths are prefix-related.
// `f(&x.a, &mut x.b)` reaches disjoint data and is accepted, while `f(&x.a, &mut x.a)` and
// `f(&x, &mut x.a)` both reach overlapping data and are rejected.
//
// A loan's live range is the liveness of the binding that holds it. `val a = &x` followed by
// `val b = &mut x` conflicts only when a is read after b is created. That is the NLL rule: a
// borrow nothing reads again constrains nothing. The conflict is reported at the second borrow,
// which is the point where the program first has two views of one value.
//
// What this does NOT cover yet:
//
//   - #1527: a borrow copied into a second binding. A loan records the place named at the
//     borrow site, so `val a = &x; val c = a` leaves c holding nothing and a conflict against
//     c is missed.
//   - #1528: a borrow stored into a container and read back out. The place a loan names is
//     the one written at the borrow site, so `holder.slot` is not known to reach what
//     `{slot: &x}` put there.
//   - #1530: an argument absorbed by an `Array`-typed rest parameter.
//     checkCallBorrowExclusivity pairs each argument with the parameter at its own index and
//     stops past the last one. A tuple-typed rest is fine, since expandTupleRest turns it into
//     ordinary positions before the walk runs. An `Array<E>` slot has none to expand into, so
//     `f(&mut x, &x)` against `fn(a: &mut T, ...rest: Array<&T>)` reports nothing.
//   - #1529: branches and loop back edges. Each loan is checked against the loans recorded
//     before it in source order, so a borrow on one arm of an `if` is weighed against a use on
//     the other, and a borrow late in a loop body is not weighed against the one the next
//     iteration still holds. Holder liveness rules out the common branch shapes, since a
//     binding scoped to one arm is dead on the other.
//   - #1486: anything rooted at a method's receiver. A `self` reference carries VarID 0, so
//     `&mut self.p` names no binding and records no loan. That one reaches further than this
//     check, since every analysis built on places sees the same 0.

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
	node      ast.Node
	first     ast.Span
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
	if e.SecondMut {
		return fmt.Sprintf("cannot borrow '%s' as mutable while %s borrowed as immutable", e.Place, held)
	}
	return fmt.Sprintf("cannot borrow '%s' as immutable while %s borrowed as mutable", e.Place, held)
}

// MoveWhileBorrowedError reports a move of data a live borrow still reaches. After
// `val y = b` the new owner y holds the data, and a borrow taken from b still points at it.
type MoveWhileBorrowedError struct {
	// Place names the data moved, `x` for a whole binding and `x.a` for a field.
	Place  string
	move   ast.Node
	borrow ast.Span
}

func (*MoveWhileBorrowedError) isSolverError()        {}
func (e *MoveWhileBorrowedError) Span() ast.Span      { return e.move.Span() }
func (e *MoveWhileBorrowedError) Related() []ast.Span { return []ast.Span{e.borrow} }
func (e *MoveWhileBorrowedError) Message() string {
	return fmt.Sprintf("cannot move '%s' while it is borrowed", e.Place)
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

// ImplicitBorrowArgError reports an argument filling a `&` or `&mut` parameter without a borrow
// written at the call.
type ImplicitBorrowArgError struct {
	// Written is the borrow the argument needs, `&` or `&mut`, taken from the parameter.
	Written string
	node    ast.Node
}

func (*ImplicitBorrowArgError) isSolverError()        {}
func (e *ImplicitBorrowArgError) Span() ast.Span      { return e.node.Span() }
func (e *ImplicitBorrowArgError) Related() []ast.Span { return nil }
func (e *ImplicitBorrowArgError) Message() string {
	return fmt.Sprintf("this argument is borrowed by the callee, so write the borrow: `%s`", e.Written)
}

// loan is one borrow the exclusivity check tracks.
type loan struct {
	// place is the data the borrow reaches.
	place movePlace
	// mut says a write can go through this borrow. At a call it comes from the parameter,
	// which is what decides whether the callee may write.
	mut bool
	// holder is the binding the borrow is bound to, and 0 for a call argument. A held loan
	// lasts while its binding is live; one with no holder lasts for its own statement.
	holder liveness.VarID
	// holderPath is the field path within holder the borrow landed at, empty when the whole
	// binding took it. `b.peer = &mut d` lands at [peer]. Repointing that field ends the loan
	// there and leaves a sibling field's loan alone, which is the strong update the borrow
	// graph makes for the same statement.
	holderPath []placeSeg
	// ref is the statement the borrow is created at.
	ref liveness.StmtRef
	// node is the expression the diagnostic blames.
	node ast.Node
	// fromStore marks a loan a callee's store effect creates rather than one the program
	// writes. It takes hold only once that call returns, so a read in the call's own statement
	// is not yet reaching data through it.
	fromStore bool
	// seq orders this loan against the reads walked around it. It counts up and is never
	// reused, so it survives the loan list changing shape, where a position would not.
	seq int
	// callerOwned marks a loan held by a caller-owned target, a borrow parameter or a borrowing
	// receiver. It lasts while the body can still read the target, like any held loan. The
	// caller reads the target only after the function returns, and by then nothing in the frame
	// can change the stored local, so the loan has nothing to guard past the body's last read.
	// An async or generator body hands control back to the caller at each `await` or `yield`
	// while the frame still runs, so there the loan lasts to the end of the function.
	callerOwned bool
	// endSeq is the sequence at which a reassignment of the holder ended this loan, and 0 while
	// it still holds. The loan stays in the list so a read walked BEFORE that point is still
	// weighed against it; erasing it would let a later reassignment silence an earlier read.
	endSeq int
}

// nextLoanSeq returns the sequence number the next loan takes. It counts up across the whole
// body, so a number handed out once is never handed out again.
func (c *checker) nextLoanSeq() int {
	c.fn.loanSeq++
	return c.fn.loanSeq
}

// noteLoanRead records that e is the read a borrow performs to take its own loan. Reading a
// place is how a borrow of it is created, so that one read is not a second path to the data.
// Without this `val a = &mut x` would report x against the loan it just created.
func (c *checker) noteLoanRead(e ast.Expr) {
	if c.fn == nil || e == nil {
		return
	}
	if c.fn.loanReads == nil {
		c.fn.loanReads = set.NewSet[ast.Node]()
	}
	c.fn.loanReads.Add(e)
}

// loanPlace returns the place a borrow argument reaches. `&mut x.a` reaches x.a, and a bare
// name already holding a borrow reaches whatever that name reaches, which for now is the name
// itself. ok is false when the operand is not a place, such as a call result.
func loanPlace(arg ast.Expr) (movePlace, bool) {
	p, ok := exprPlace(borrowOperand(arg))
	if !ok || p.root <= 0 {
		return movePlace{}, false
	}
	return p, true
}

// paramBorrowMut reports whether a parameter borrows its argument, and whether the callee can
// write through it. A borrow parameter is a `RefType` carrying a lifetime. An owned parameter
// carries none and is moved into the callee rather than borrowed, so it is not a loan.
func paramBorrowMut(t soltype.Type) (bool, bool) {
	ref, ok := t.(*soltype.RefType)
	if !ok || ref.Lt == nil {
		return false, false
	}
	return ref.Mut, true
}

// placesOverlap reports whether two places reach any of the same data. They must be rooted at
// the same binding, and one field path must be a prefix of the other. `x.a` and `x.a.b` overlap
// because the second sits inside the first; `x.a` and `x.b` do not.
func placesOverlap(a, b movePlace) bool {
	return a.root == b.root && pathPrefixRelated(a.path, b.path)
}

// conflicts reports whether two loans may not be live at once. The pair has to disagree about
// whether the data can change, so one borrow writable and the other not. Two readers see the
// same value and two writers both expect the value to move under them, so neither pair is a
// conflict. Rule 3 in planning/lifetimes/requirements.md is what admits the second of those.
func conflicts(a, b loan) bool {
	return a.mut != b.mut && placesOverlap(a.place, b.place)
}

// reportBorrowConflict reports second as the borrow that broke exclusivity, pointing back at
// the borrow already live.
func (c *checker) reportBorrowConflict(first, second loan) {
	c.report(&BorrowAliasError{
		Place:      c.renderPlace(second.place),
		FirstPlace: c.renderPlace(first.place),
		FirstMut:   first.mut,
		SecondMut:  second.mut,
		node:       second.node,
		first:      first.node.Span(),
	})
}

// liveAt reports whether l is still usable at ref. A loan bound to a binding lasts while that
// binding is live, so one whose binding is never read again constrains nothing. A loan with no
// holder belongs to a call argument and lasts only for its own statement.
//
// The test is liveness ENTERING ref, not after it. A binding the statement at ref reads is live
// entering it and dead leaving it, and that read is exactly the use the loan has to be checked
// against. Asking IsLiveAfter would miss `readWrite(&x, b)`, where b's last use is the call
// being checked.
func (c *checker) liveAt(l loan, ref liveness.StmtRef) bool {
	// The caller can read a caller-owned target at any `await` or `yield`, so in a body that
	// suspends, the loan outlives the body's own last read of the target.
	if l.callerOwned && (c.fn.async || c.fn.gen) {
		return true
	}
	if l.holder <= 0 {
		return l.ref == ref
	}
	if c.fn.liveness == nil {
		return true
	}
	return c.fn.liveness.IsLiveBefore(ref, l.holder)
}

// checkAgainstHeldLoans reports a conflict between fresh and any loan already live at fresh's
// statement.
func (c *checker) checkAgainstHeldLoans(fresh loan) {
	for _, held := range c.fn.loans {
		// A loan the holder's reassignment ended reaches nothing from here on.
		if held.endSeq != 0 {
			continue
		}
		if !c.liveAt(held, fresh.ref) {
			continue
		}
		if conflicts(held, fresh) {
			c.reportBorrowConflict(held, fresh)
		}
	}
}

// recordBorrowLoan records the loan a `val`/`var` initializer creates, after reporting any
// conflict with a borrow already live. `val a = &mut x` binds a loan of x to a, which then
// lasts as long as a is read.
func (c *checker) recordBorrowLoan(holder int, init ast.Expr, ref liveness.StmtRef) {
	if c.fn == nil || holder <= 0 || init == nil {
		return
	}
	// A reassignment repoints the binding, so whatever it borrowed before is unreachable
	// through it. `var a = &mut x` followed by `a = &mut y` leaves no loan of x behind. This
	// is the strong update the flow-sensitive borrow graph makes for the same statement.
	c.dropLoansHeldBy(liveness.VarID(holder))
	borrow, ok := init.(*ast.BorrowExpr)
	if !ok {
		return
	}
	place, ok := loanPlace(borrow)
	if !ok {
		return
	}
	c.noteLoanRead(borrowOperand(borrow))
	fresh := loan{
		place:  place,
		mut:    borrow.Mut,
		holder: liveness.VarID(holder),
		ref:    ref,
		node:   borrow,
		seq:    c.nextLoanSeq(),
	}
	c.checkAgainstHeldLoans(fresh)
	c.fn.loans = append(c.fn.loans, fresh)
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

// borrowSite is one borrow expression the walk inferred: the expression, the statement it sits
// in, and the sequence the next loan would have taken when it was walked.
type borrowSite struct {
	borrow    *ast.BorrowExpr
	ref       liveness.StmtRef
	loanSeqAt int
}

// noteBorrowSite records a borrow expression as the walk infers it, for checkNestedBorrows to
// read once every loan is recorded.
func (c *checker) noteBorrowSite(e *ast.BorrowExpr) {
	if c.fn == nil {
		return
	}
	ref, ok := c.currentStmtRef()
	if !ok {
		return
	}
	c.fn.borrowSites = append(c.fn.borrowSites, borrowSite{borrow: e, ref: ref, loanSeqAt: c.fn.loanSeq + 1})
}

// checkNestedBorrows takes the loan of each borrow that is neither a binding's initializer nor
// a call argument. The `&mut b` in `val t = {x: &mut b}` is one, and so is the one in
// `return [a.spare, &mut b]`. Initializers and call arguments note their operand when they
// record the loan. Any other borrow reaches here with its operand un-noted.
//
// Each such borrow notes its operand as the read that takes its loan, so the use check does not
// count it as a second path to the data. The borrow is then weighed against the loans live at
// its statement, the way a call argument is. A loan written later in the source is not live at
// it yet, and one a reassignment ended before it no longer reaches anything. A pair reports
// only when its mutability differs, so a nested `&mut b` beside a live mutable loan of b is
// accepted and a nested `&b` beside one is not.
//
// The loan lasts for the borrow's own statement. A borrow inside a literal a binding holds
// lives as long as that binding, which this does not track. Reading such a borrow back out of
// the container is tracked in #1528.
//
// A borrow inside a returned expression that reported for reaching a local twice adds no
// diagnostic. The return's report already names the pair.
func (c *checker) checkNestedBorrows() {
	if c.fn == nil {
		return
	}
	if c.fn.loanReads == nil {
		c.fn.loanReads = set.NewSet[ast.Node]()
	}
	for _, site := range c.fn.borrowSites {
		operand := borrowOperand(site.borrow)
		if c.fn.loanReads.Contains(operand) {
			continue
		}
		place, ok := loanPlace(site.borrow)
		if !ok {
			continue
		}
		c.noteLoanRead(operand)
		if slices.ContainsFunc(c.fn.sharedPathSpans, func(s ast.Span) bool {
			return s.ContainsSpan(site.borrow.Span())
		}) {
			continue
		}
		fresh := loan{place: place, mut: site.borrow.Mut, ref: site.ref, node: site.borrow}
		for _, held := range c.fn.loans {
			if held.seq >= site.loanSeqAt {
				continue
			}
			if held.endSeq != 0 && held.endSeq < site.loanSeqAt {
				continue
			}
			// A store's loan begins once its call returns, so a borrow written in the same
			// statement is compared by the call's own argument check instead.
			if held.fromStore && held.ref == site.ref {
				continue
			}
			if c.liveAt(held, site.ref) && conflicts(held, fresh) {
				c.reportBorrowConflict(held, fresh)
				break
			}
		}
	}
}

// fieldWrite is the field a member assignment writes: the place, and the assignment target the
// diagnostic blames.
type fieldWrite struct {
	place  movePlace
	target ast.Expr
}

// noteFieldWrite records that the receiver chain of a member assignment is a write to the
// place the assignment targets. `x.a.v = 5` marks both `x.a` and `x` as writes to x.a.v,
// since the use check may have recorded either as a use site.
func (c *checker) noteFieldWrite(target *ast.MemberExpr) {
	if c.fn == nil {
		return
	}
	written, ok := exprPlace(target)
	if !ok || written.root <= 0 {
		return
	}
	if c.fn.fieldWrites == nil {
		c.fn.fieldWrites = map[ast.Node]fieldWrite{}
	}
	var recv ast.Expr = target.Object
	for recv != nil {
		c.fn.fieldWrites[recv] = fieldWrite{place: written, target: target}
		switch r := recv.(type) {
		case *ast.MemberExpr:
			recv = r.Object
		case *ast.IndexExpr:
			recv = r.Object
		default:
			recv = nil
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
// caller as well. targetPath is the field of target the borrow lands at, so a later store into
// that field can end this loan and leave a sibling field's alone.
func (c *checker) recordStoreEdgeLoan(place movePlace, mut bool, target liveness.VarID, targetPath []placeSeg, ref liveness.StmtRef, blame ast.Node) {
	if c.fn == nil || target <= 0 || place.root <= 0 {
		return
	}
	fresh := loan{
		place: place, mut: mut, holder: target, holderPath: targetPath, ref: ref, node: blame,
		fromStore: true, callerOwned: c.paramReferentOutlivesFrame(target), seq: c.nextLoanSeq(),
	}
	// One signature can write an argument into several positions of the target, so the same
	// loan reaches here once per position. Recording it once keeps a later conflict to one
	// diagnostic instead of one per position.
	for _, l := range c.fn.loans {
		if l.fromStore && l.holder == fresh.holder && l.ref == fresh.ref &&
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

// checkUsesAgainstLoans weighs each use of a place through its owner against the loans live at
// it. A use names the place directly, as in `val y = b` or `b.value`, rather than borrowing it.
// What it conflicts with depends on what the use does:
//
//   - A read conflicts with no loan. The owner is one more path to the value. A live `&mut`
//     already expects the value to change, and a live `&` sees no change from a read.
//   - A field write, as in `x.v = 5`, conflicts with a live immutable loan of the written
//     data, since the `&` holder expects it to hold still. Beside a live `&mut` it is one more
//     writer, which Rule 3 of planning/lifetimes/requirements.md allows.
//   - A move conflicts with every live loan, mutable or immutable. After `val y = b` the data
//     belongs to y, and a borrow taken from b would reach data its new owner controls.
//
// Reassigning the whole binding is not a use of the old value, so it reaches none of these.
// A borrow of the old value keeps pointing at it.
//
// A borrow is not a use for this check. The read a borrow performs to take its own loan is
// skipped. `&mut b` reads b, and that read is what creates the loan rather than a second path
// to it. The loan-against-loan check in conflicts compares one borrow with another, and it
// rejects only a mutable borrow beside an immutable one. So a second `&mut b` while a mutable
// loan of b is live is accepted, since two mutable borrows of one value are allowed.
//
// A read the use-after-move scan already reported is skipped too, so one bad read yields one
// diagnostic rather than two.
func (c *checker) checkUsesAgainstLoans(reported set.Set[ast.Node]) {
	if c.fn == nil || len(c.fn.loans) == 0 || len(c.fn.useSites) == 0 {
		return
	}
	if c.fn.loanReads == nil {
		c.fn.loanReads = set.NewSet[ast.Node]()
	}
	for _, u := range c.fn.useSites {
		if c.fn.loanReads.Contains(u.node) || reported.Contains(u.node) {
			continue
		}
		moved := c.fn.movedSources != nil && c.fn.movedSources.Contains(u.node)
		write, isWrite := c.fn.fieldWrites[u.node]
		if !moved && !isWrite {
			continue
		}
		// A write blames the field it writes, which may sit below the receiver the use names.
		place, blame := u.place, u.node
		if isWrite && !moved {
			place, blame = write.place, write.target
		}
		// A read INSIDE a returned expression already reported for reaching a local twice needs
		// no second diagnostic. The return names where the two paths leave together, which is
		// the more useful of the two. A read elsewhere in the body is a separate fact and keeps
		// its own.
		if slices.ContainsFunc(c.fn.sharedPathSpans, func(s ast.Span) bool {
			return s.ContainsSpan(u.node.Span())
		}) {
			continue
		}
		for _, l := range c.fn.loans {
			// Only the loans that existed when this read was walked. A borrow written later in
			// the source has not taken hold at the read, and on the other arm of a branch it
			// never does.
			if l.seq >= u.loanSeqAt {
				continue
			}
			// A loan ended before this read was walked reaches nothing at it. One ended after it
			// still does, which is what keeps a later reassignment from silencing an earlier
			// read.
			if l.endSeq != 0 && l.endSeq < u.loanSeqAt {
				continue
			}
			// A write conflicts only with an immutable loan. A move conflicts with any loan.
			if (!moved && l.mut) || !c.liveAt(l, u.ref) || !placesOverlap(l.place, place) {
				continue
			}
			// The call that declares a store is where its loan begins, so a read in that same
			// statement has not gone through the alias yet. `h.drain(&mut o)` reads h to reach
			// the method, and that read is the call itself rather than a second path to h.
			// Two borrows written in one statement are compared by the loan-against-loan check.
			if l.fromStore && l.ref == u.ref {
				continue
			}
			if moved {
				c.report(&MoveWhileBorrowedError{
					Place:  c.renderPlace(u.place),
					move:   u.node,
					borrow: l.node.Span(),
				})
			} else {
				c.report(&BorrowedValueWriteError{
					Place:  c.renderPlace(place),
					write:  blame,
					borrow: l.node.Span(),
				})
			}
			break
		}
	}
}

// checkExplicitBorrowArgs reports an argument that fills a borrow parameter without a borrow
// written at the call. A borrow is where a second view of a value comes into existence, and
// every borrow rule keys on it, so the call site is where it should be visible.
//
// Two arguments are already borrows and need nothing written:
//
//   - A borrow expression, `&e` or `&mut e`.
//   - A value whose own type is a borrow, passed by name. `fn g(m: &mut T) { f(m) }` hands on a
//     borrow g holds rather than creating one.
//
// An argument whose type has not settled is left alone, since what it becomes decides whether a
// borrow is missing and this walk cannot wait for it.
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
		mut, isBorrowParam := paramBorrowMut(fn.Params[i].Type)
		if !isBorrowParam {
			continue
		}
		if _, written := arg.(*ast.BorrowExpr); written {
			continue
		}
		argT := c.info.TypeOf(arg)
		if argT == nil {
			continue
		}
		if ref, isRef := argT.(*soltype.RefType); isRef && ref.Lt != nil {
			continue
		}
		if _, unsettled := argT.(*soltype.TypeVarType); unsettled {
			continue
		}
		written := "&"
		if mut {
			written = "&mut"
		}
		c.report(&ImplicitBorrowArgError{Written: written, node: arg})
	}
}

// checkCallBorrowExclusivity reports two arguments of one call that borrow overlapping data
// where at least one parameter can write. Every argument of a call is live at once, so the
// arguments are compared against each other as well as against the loans already held.
//
// `f(&x, &mut x)` and `f(m, m)` filling an immutable and a mutable parameter both report. The
// second shows why the parameter decides: m is one borrow reaching one place, and what makes
// the pair a conflict is that the callee may write through one view while reading the other.
func (c *checker) checkCallBorrowExclusivity(e *ast.CallExpr, fn *soltype.FuncType, ref liveness.StmtRef) {
	if c.fn == nil {
		return
	}
	args := make([]loan, 0, len(e.Args))
	for i, arg := range e.Args {
		if i >= len(fn.Params) {
			break
		}
		mut, isBorrow := paramBorrowMut(fn.Params[i].Type)
		if !isBorrow {
			continue
		}
		place, ok := loanPlace(arg)
		if !ok {
			continue
		}
		c.noteLoanRead(borrowOperand(arg))
		args = append(args, loan{place: place, mut: mut, ref: ref, node: arg})
	}
	for i := range args {
		for j := i + 1; j < len(args); j++ {
			if conflicts(args[i], args[j]) {
				c.reportBorrowConflict(args[i], args[j])
			}
		}
	}
	for _, a := range args {
		c.checkAgainstHeldLoans(a)
	}
}
