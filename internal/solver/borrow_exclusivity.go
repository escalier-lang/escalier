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
// write can go through it, and how long it stays usable. Loans come from four sites:
//
//   - A `val`/`var` initializer that borrows a place, as in `val a = &mut x`. The binding holds
//     the loan, so the loan lasts as long as that binding is live.
//   - A `val`/`var` initializer or a reassignment that names a binding holding loans, as in
//     `val c = a`. The new binding holds a copy of each loan, narrowed to immutable when its
//     type is `&`.
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
	// FirstVia and SecondVia name the closure a call may run, when the matching borrow is that
	// closure's access to what it captures during the call. Each is a phrase closureVia or
	// retainedVia returns, and is empty for a borrow the program writes. FirstRuns and
	// SecondRuns mark a closure the call is sure to run, because the call's callee names it.
	FirstVia   string
	SecondVia  string
	FirstRuns  bool
	SecondRuns bool
	// closure marks a conflict in which one of the two borrows is a closure's capture. E4
	// counts a capture as used while the closure runs, so it checks this conflict.
	closure bool
	node    ast.Node
	first   ast.Span
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
	move    ast.Node
	borrow  ast.Span
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

// closureVia returns the phrase a message names a closure by when the closure is reached
// through the binding `name`, as in `'sink'`.
func closureVia(name string) string {
	return fmt.Sprintf("'%s'", name)
}

// retainedVia returns the phrase a message names a closure by when the callee `callee` keeps
// it through its parameter `param`, as in `the 'handler' that 'onClick' retains`. An empty
// callee or param leaves that name out.
func retainedVia(param, callee string) string {
	held := "the function"
	if param != "" {
		held = fmt.Sprintf("the '%s'", param)
	}
	if callee == "" {
		return held + " the callee retains"
	}
	return fmt.Sprintf("%s that '%s' retains", held, callee)
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
	// via names the closure a call may run, for a loan standing for that closure's access to
	// what it captures during the call. It names the module-level binding a stored closure is
	// reached through, the parameter of the callee that retains it, or the local binding a
	// closure is called through or passed as. It is a phrase closureVia or retainedVia returns,
	// and is empty for a borrow the program writes.
	via string
	// viaRuns marks a closure named by via that the call is sure to run, because the call's
	// callee names it.
	viaRuns bool
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
		FirstVia:   first.via,
		SecondVia:  second.via,
		FirstRuns:  first.viaRuns,
		SecondRuns: second.viaRuns,
		closure:    isFuncNode(first.node) || isFuncNode(second.node),
		node:       second.node,
		first:      first.node.Span(),
	})
}

// isFuncNode reports whether n is a function or closure.
func isFuncNode(n ast.Node) bool {
	_, ok := n.(ast.Func)
	return ok
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
	// A store into a caller-owned target holds its loan only at the statements that run after
	// it, so a statement on a branch the store is not on sees no loan.
	if l.callerOwned && !c.stmtReaches(l.ref, ref) {
		return false
	}
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
// lasts as long as a is read. holderT is the type of the binding holder. An initializer that
// names another binding gives holder a copy of each loan that binding holds, as
// copyHeldLoans describes.
func (c *checker) recordBorrowLoan(holder liveness.VarID, holderT soltype.Type, init ast.Expr, ref liveness.StmtRef) {
	if c.fn == nil || holder <= 0 || init == nil {
		return
	}
	ident, isIdent := init.(*ast.IdentExpr)
	if isIdent && c.varIDs.Of(ident) == holder {
		// `a = a` leaves a holding what it held.
		return
	}
	// A reassignment repoints the binding, so whatever it borrowed before is unreachable
	// through it. `var a = &mut x` followed by `a = &mut y` leaves no loan of x behind. This
	// is the strong update the flow-sensitive borrow graph makes for the same statement.
	c.dropLoansHeldBy(holder)
	if isIdent {
		c.copyHeldLoans(holder, holderT, ident, ref)
		return
	}
	borrow, ok := init.(*ast.BorrowExpr)
	if !ok {
		return
	}
	place, ok := c.loanPlace(borrow)
	if !ok {
		return
	}
	c.noteLoanRead(borrowOperand(borrow))
	fresh := loan{
		place:  place,
		mut:    borrow.Mut,
		holder: holder,
		ref:    ref,
		node:   borrow,
		seq:    c.nextLoanSeq(),
	}
	c.checkAgainstHeldLoans(fresh)
	c.fn.loans = append(c.fn.loans, fresh)
}

// copyHeldLoans gives holder, a binding of type holderT, a copy of each loan the binding
// source names still holds, recorded at ref. `val c = a` after `val a = &x` copies the borrow
// a holds, so c reaches x too, and the loan lasts while either binding is read. A `&` holder
// narrows a mutable loan to an immutable one, so after `val b = &mut a`, the copy
// `val c: &{x: number} = b` holds an immutable loan of a.
func (c *checker) copyHeldLoans(holder liveness.VarID, holderT soltype.Type, source *ast.IdentExpr, ref liveness.StmtRef) {
	from := c.varIDs.Of(source)
	if from <= 0 {
		return
	}

	writable := true
	if b, isBorrow := c.borrowOfType(holderT); isBorrow {
		writable = b.anyMut
	}

	// The range reads c.fn.loans once, so the copies appended below are not visited.
	for _, l := range c.fn.loans {
		if l.holder != from || l.endSeq != 0 || l.via != "" {
			continue
		}

		cp := l
		cp.mut = l.mut && writable
		cp.holder = holder
		cp.ref, cp.seq = ref, c.nextLoanSeq()
		cp.fromStore, cp.callerOwned = false, false
		c.fn.loans = append(c.fn.loans, cp)
	}
}

// capturedLocal is a local of the current body that a closure captures.
type capturedLocal struct {
	root liveness.VarID
	// `mut` says the closure writes the local.
	mut bool
}

// recordCaptureLoans records each local of the current body that `closure` captures in
// capturedLocals and capturedTypes, and records the closure as a use of it. A closure in
// namedClosures takes no loan here, since the calls of its name take them. Any other closure
// takes its capture loans at its own statement, since the expression it sits in may call it
// there.
//
// A capture whose type has value semantics is left out, since a closure holding a primitive
// cannot see a later change to the binding it copied. So is a capture from a body further
// out, which this body's tables do not track.
func (c *checker) recordCaptureLoans(scope *Scope, closure ast.Func) {
	if c.fn == nil {
		return
	}
	ref, hasRef := c.currentStmtRef()
	moved := c.capturesMovedBy[closure]
	var captured []capturedLocal
	for _, capture := range c.closureCaptures(closure) {
		b, found := scope.GetValue(capture.Name)
		if !found || b.VarID <= 0 {
			continue
		}
		root := liveness.VarID(b.VarID)
		if name, ok := c.fn.varIDNames[root]; !ok || name != capture.Name {
			continue
		}
		if eventRef, ok := c.eventStmtRef(); ok {
			c.emitCaptureFlows(closure, root, bindingDecl(b), bindingType(b), capture.IsMutable, moved[capture.Name], eventRef)
		}
		if isValueType(bindingType(b)) {
			continue
		}
		// Writing the closure reads the local, so capturing one that has moved is a use after
		// the move.
		if hasRef {
			c.fn.useSites = append(c.fn.useSites, moveUse{place: movePlace{root: root}, ref: ref, node: closure, loanSeqAt: c.fn.loanSeq + 1})
		}
		// A closure that moves the local takes it here and owns it from then on, so the local
		// is moved rather than lent. The move blames the expression in the closure that moves
		// it. A module-level name a module's function captures cannot move at all.
		if moveNode, ok := moved[capture.Name]; ok {
			if c.fn.capturesModule && c.fn.captureVarIDs.Contains(root) {
				c.reportModuleLevelMove(moveNode)
				continue
			}
			if hasRef {
				c.recordMovePlace(movePlace{root: root}, moveNode, ref)
				continue
			}
		}
		if c.fn.capturedTypes == nil {
			c.fn.capturedTypes = map[liveness.VarID]soltype.Type{}
		}
		c.fn.capturedTypes[root] = bindingType(b)
		captured = append(captured, capturedLocal{root: root, mut: capture.IsMutable})
	}
	if len(captured) > 0 {
		if c.fn.capturedLocals == nil {
			c.fn.capturedLocals = map[ast.Func][]capturedLocal{}
		}
		c.fn.capturedLocals[closure] = append(c.fn.capturedLocals[closure], captured...)
	}
	if !hasRef || (c.fn.namedClosures != nil && c.fn.namedClosures.Contains(closure)) {
		return
	}
	for _, local := range captured {
		c.takeCaptureLoan(local, ref, closure, "", false)
	}
}

// useClosureCaptures takes the capture loans of the closure `expr` names for the statement at
// `ref`, and records a read of each captured local there. runs reports whether the call is
// sure to run the closure. The closure's captures are the
// capture edges the name holds in the borrow graph, whether it was bound to the closure or
// copied from a name that was. A local captured through several edges is taken once, as a
// write when any edge writes it. Any expression other than a name records nothing.
func (c *checker) useClosureCaptures(expr ast.Expr, ref liveness.StmtRef, runs bool) {
	e, ok := expr.(*ast.IdentExpr)
	if !ok || c.fn == nil || c.varIDs.Of(e) <= 0 {
		return
	}
	writes := map[liveness.VarID]bool{}
	for _, edge := range c.fn.eagerBorrowGraph[c.varIDs.Of(e)] {
		if edge.capture {
			writes[edge.referent] = writes[edge.referent] || edge.mut
		}
	}
	roots := make([]liveness.VarID, 0, len(writes))
	for root := range writes {
		roots = append(roots, root)
	}
	// Sorting keeps the order of the loans, and so of any reports, the same from run to run.
	slices.Sort(roots)
	for _, root := range roots {
		c.takeCaptureLoan(capturedLocal{root: root, mut: writes[root]}, ref, e, closureVia(e.Name), runs)
		c.fn.useSites = append(c.fn.useSites, moveUse{
			place:     movePlace{root: root},
			ref:       ref,
			node:      e,
			loanSeqAt: c.fn.loanSeq + 1,
			via:       closureVia(e.Name),
			viaRuns:   runs,
			viaReads:  !writes[root],
		})
	}
}

// takeCaptureLoan records a loan of the whole captured local for the statement at `ref`,
// after reporting any conflict with a loan already live there. The loan has no holder, so it
// lasts for that statement alone. It is mutable when the closure writes the capture. `node`
// is what a conflict report blames, and `via` names the closure for it, or is empty when the
// capture is not reached through a call. runs reports whether the call is sure to run it.
func (c *checker) takeCaptureLoan(local capturedLocal, ref liveness.StmtRef, node ast.Node, via string, runs bool) {
	fresh := loan{
		place:   movePlace{root: local.root},
		mut:     local.mut,
		ref:     ref,
		node:    node,
		seq:     c.nextLoanSeq(),
		via:     via,
		viaRuns: runs,
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

// stmtReaches reports whether the statement at `to` can run after the statement at `from`. That
// holds for a later statement of the same block, and for any statement of a block the CFG reaches
// from `from`'s block.
func (c *checker) stmtReaches(from, to liveness.StmtRef) bool {
	if from.BlockID == to.BlockID && from.StmtIdx <= to.StmtIdx {
		return true
	}
	cfg := c.fn.cfg
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
		place, ok := c.loanPlace(site.borrow)
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

// noteFieldWrite records that the receiver chain of a member or index assignment is a
// write to the place the assignment targets. `target` is the assignment's left side and
// `object` is its receiver. `x.a.v = 5` marks both `x.a` and `x` as writes to `x.a.v`,
// since the use check may have recorded either as a use site.
func (c *checker) noteFieldWrite(target, object ast.Expr) {
	if c.fn == nil {
		return
	}
	written, ok := c.exprPlace(target)
	if !ok || written.root <= 0 {
		return
	}
	if c.fn.fieldWrites == nil {
		c.fn.fieldWrites = map[ast.Node]fieldWrite{}
	}
	recv := object
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
	// A dynamic index off a binding, such as the `xs[i]` in `xs[i].x = 3` with `i: number`,
	// names the whole binding. Its read records no use, since every read of one element would
	// then count as a read of the others. The write records the use here for the use check to
	// find.
	if ix, isIndex := object.(*ast.IndexExpr); isIndex && c.fn.cfg != nil {
		place, ok := c.exprPlace(ix)
		ref, hasRef := c.currentStmtRef()
		if ok && len(place.path) == 0 && hasRef {
			c.fn.useSites = append(c.fn.useSites, moveUse{place: place, ref: ref, node: ix, loanSeqAt: c.fn.loanSeq + 1})
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
					Place:   c.renderPlace(c.movedPlaceOf(u.node, u.place, l.place)),
					Via:     l.via,
					ViaRuns: l.viaRuns,
					move:    u.node,
					borrow:  l.node.Span(),
				})
			} else {
				// The written place collapses a dynamic index into its container, so it
				// would name `xs[i].x` as `xs`. The write's own target names it as written.
				c.report(&BorrowedValueWriteError{
					Place:  c.renderWrittenPlace(place, blame),
					write:  blame,
					borrow: l.node.Span(),
				})
			}
			break
		}
	}
}

// movedPlaceOf returns the place the read node moves that overlaps loanPlace. A
// destructuring reads its source to move parts of it, so `val {f: d} = p` reads the place
// `p` and moves `p.f`. When node moved no such place, it returns read, the place node reads.
// Of several such places, it returns the one with the lowest lattice id, so every run
// names the same one.
func (c *checker) movedPlaceOf(node ast.Node, read, loanPlace movePlace) movePlace {
	found := false
	var bestID liveness.VarID
	for id, p := range c.fn.movePlaces {
		if c.fn.moveNodes[id] != node || !placesOverlap(p, loanPlace) {
			continue
		}
		if !found || id < bestID {
			found, bestID = true, id
		}
	}
	if !found {
		return read
	}
	return c.fn.movePlaces[bestID]
}

// renderWrittenPlace names the place a write targets for a diagnostic. It renders target,
// the expression the write names, as written, so `xs[1].x = 3` names `xs[1].x`. When the
// printer cannot render target, it renders place instead.
func (c *checker) renderWrittenPlace(place movePlace, target ast.Node) string {
	return writtenPlaceName(target, c.renderPlace(place))
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
		borrow, isBorrow := c.borrowOfType(fn.Params[i].Type)
		if !isBorrow {
			continue
		}
		place, ok := c.loanPlace(arg)
		if !ok {
			continue
		}
		c.noteLoanRead(borrowOperand(arg))
		// The callee can narrow a union parameter to its `&mut` member and write through
		// it, so a parameter with any `&mut` member takes a mutable loan.
		args = append(args, loan{place: place, mut: borrow.anyMut, ref: ref, node: arg})
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
	if len(args) > 0 {
		if c.fn.callArgLoans == nil {
			c.fn.callArgLoans = map[ast.Node][]loan{}
		}
		c.fn.callArgLoans[e] = args
	}
}

// markNamedClosure adds `closure` to namedClosures. It must run before the closure is
// inferred, since inferring it records its capture accesses.
func (c *checker) markNamedClosure(closure ast.Func) {
	if c.fn == nil {
		return
	}
	if c.fn.namedClosures == nil {
		c.fn.namedClosures = set.NewSet[ast.Func]()
	}
	c.fn.namedClosures.Add(closure)
}

// retainedClosureStore records a value that may carry a closure and stays reachable after its
// statement. The value is stored into a module-level binding, or passed to a parameter whose
// function type carries a retention lifetime.
type retainedClosureStore struct {
	// via names the binding or parameter that keeps the closure, as closureVia or retainedVia
	// returns it.
	via  string
	expr ast.Expr
	ref  liveness.StmtRef
	// call is the call whose parameter keeps the closure, and nil for a store into a
	// module-level binding.
	call ast.Node
	// static marks a closure kept for the rest of the program. Every call after the store may
	// run it.
	static bool
	// holders are the bindings that keep the closure reachable when static is false. They are
	// the arguments and the receiver whose parameter types share the retention lifetime, and
	// each binding the call's result or another holder flows into. A call may run the closure
	// only while one of them is live.
	holders []liveness.VarID
}

// noteRetainedClosureStore records store for recordRetainedCaptureLoans to find the locals its
// closures write. It returns the store's index in retainedClosureStores, or -1 when the body
// tracks no stores.
func (c *checker) noteRetainedClosureStore(store retainedClosureStore) int {
	if c.fn == nil || c.fn.cfg == nil {
		return -1
	}
	c.fn.retainedClosureStores = append(c.fn.retainedClosureStores, store)
	return len(c.fn.retainedClosureStores) - 1
}

// noteRetainedClosureArgs records each argument of the call e that fills a parameter fn keeps
// past the call, as a retainedClosureStore. Such a parameter's type holds a function type with a
// retention lifetime, at its top or inside a union, an object field, or an array. A whole
// argument filling it is taken as kept, so each closure written in an object literal counts.
//
// A retention lifetime that is 'static, or bound to outlive 'static, keeps the closure for the
// rest of the program. Any other retention lifetime keeps it as long as a binding that shares
// the lifetime is live. When no binding shares it, the callee can keep the closure no longer
// than the call, and nothing is recorded.
func (c *checker) noteRetainedClosureArgs(e *ast.CallExpr, fn *soltype.FuncType, ref liveness.StmtRef) {
	recv, self := c.calleeReceiver(e.Callee)
	for i, arg := range e.Args {
		param, ok := paramForArg(fn, i)
		if !ok {
			break
		}
		lts := retentionLifetimes(param.Type)
		if len(lts) == 0 {
			continue
		}
		paramName := ""
		if ip, isIdent := param.Pattern.(*soltype.IdentPat); isIdent {
			paramName = ip.Name
		}
		store := retainedClosureStore{
			via:  retainedVia(paramName, calleeName(e.Callee)),
			expr: arg,
			ref:  ref,
			call: e,
		}
		var shared []*soltype.LifetimeVar
		for _, lt := range lts {
			if lifetimeEscapedToStatic(lt) {
				store.static = true
			} else if lv, isVar := lt.(*soltype.LifetimeVar); isVar {
				shared = append(shared, lv)
			}
		}
		returned := false
		if !store.static {
			store.holders = c.retentionHolders(e, fn, recv, self, i, shared)
			returned = typeMentionsLifetime(c.ctx, fn.Ret, shared)
			if len(store.holders) == 0 && !returned {
				continue
			}
		}
		idx := c.noteRetainedClosureStore(store)
		if returned && idx >= 0 {
			if c.fn.retainedResults == nil {
				c.fn.retainedResults = map[ast.Expr][]int{}
			}
			c.fn.retainedResults[e] = append(c.fn.retainedResults[e], idx)
		}
	}
}

// paramForArg returns the parameter of fn the argument at index i fills. Every argument past a
// trailing rest parameter fills that parameter. ok is false when no parameter takes the argument.
func paramForArg(fn *soltype.FuncType, i int) (*soltype.FuncParam, bool) {
	if i < len(fn.Params) {
		return fn.Params[i], true
	}
	if n := len(fn.Params); n > 0 && fn.Params[n-1].Rest {
		return fn.Params[n-1], true
	}
	return nil, false
}

// retentionHolders returns the bindings that share one of the lifetimes in shared with the
// parameter at index skip of the call e. They are the arguments, and the receiver recv whose
// parameter is self, that name a binding and whose parameter types mention one of those
// lifetimes. For an argument written as a borrow, such as `&mut em`, the holder is the borrowed
// binding, and only when the borrowed type mentions one of those lifetimes.
func (c *checker) retentionHolders(
	e *ast.CallExpr, fn *soltype.FuncType, recv ast.Expr, self *soltype.FuncParam, skip int, shared []*soltype.LifetimeVar,
) []liveness.VarID {
	var holders []liveness.VarID
	add := func(expr ast.Expr, t soltype.Type) {
		// A borrow written at the call ends with the statement, so only its referent can keep
		// the closure, and only when the referent's own type mentions the lifetime.
		if borrow, isBorrow := expr.(*ast.BorrowExpr); isBorrow {
			ref, isRef := t.(*soltype.RefType)
			if !isRef {
				return
			}
			expr, t = borrow.Arg, ref.Inner
		}
		if !typeMentionsLifetime(c.ctx, t, shared) {
			return
		}
		if p, ok := c.exprPlace(expr); ok && p.root > 0 && !slices.Contains(holders, p.root) {
			holders = append(holders, p.root)
		}
	}
	if recv != nil && self != nil {
		add(recv, self.Type)
	}
	for j, arg := range e.Args {
		if j == skip {
			continue
		}
		if param, ok := paramForArg(fn, j); ok {
			add(arg, param.Type)
		}
	}
	return holders
}

// noteRetainedHolder adds holder to the holders of each store whose closure the value init
// carries. init carries it when init is a call whose result keeps the closure, or a name that
// is already one of the store's holders. It must run once the binding init flows into is known.
func (c *checker) noteRetainedHolder(holder liveness.VarID, init ast.Expr) {
	if c.fn == nil || holder <= 0 || init == nil {
		return
	}
	for _, idx := range c.fn.retainedResults[init] {
		c.fn.retainedClosureStores[idx].holders = append(c.fn.retainedClosureStores[idx].holders, holder)
	}
	p, ok := c.exprPlace(init)
	if !ok || p.root <= 0 || p.root == holder {
		return
	}
	for i := range c.fn.retainedClosureStores {
		store := &c.fn.retainedClosureStores[i]
		if slices.Contains(store.holders, p.root) && !slices.Contains(store.holders, holder) {
			store.holders = append(store.holders, holder)
		}
	}
}

// typeMentionsLifetime reports whether one of the lifetimes in lts occurs in t, as a borrow's
// lifetime, a function's retention lifetime, or a class or alias lifetime argument.
func typeMentionsLifetime(ctx *Context, t soltype.Type, lts []*soltype.LifetimeVar) bool {
	if t == nil || len(lts) == 0 {
		return false
	}
	occ, _ := walkLtOcc(t, soltype.Positive)
	found, _ := lifetimeVarsIn(ctx, t)
	return slices.ContainsFunc(lts, func(lv *soltype.LifetimeVar) bool {
		_, inOcc := occ[lv]
		return inOcc || slices.Contains(found, lv)
	})
}

// retentionLifetimes returns the retention lifetime of each function type in t, reached through
// unions, intersections, object fields, and type arguments. A function type's own parameters
// and return are not searched, since a function there belongs to a callback's signature.
func retentionLifetimes(t soltype.Type) []soltype.Lifetime {
	v := &retentionVisitor{}
	t.Accept(v, soltype.Positive)
	return v.lts
}

// retentionVisitor collects the retention lifetimes retentionLifetimes returns.
type retentionVisitor struct {
	lts []soltype.Lifetime
}

func (v *retentionVisitor) EnterType(t soltype.Type, _ soltype.Polarity) soltype.EnterResult {
	ft, isFn := t.(*soltype.FuncType)
	if !isFn {
		return soltype.EnterResult{}
	}
	if ft.Lt != nil {
		v.lts = append(v.lts, ft.Lt)
	}
	return soltype.EnterResult{SkipChildren: true}
}

func (*retentionVisitor) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

// calleeName returns the name a call's callee is written as, `onClick` for `onClick(h)` and `on`
// for `button.on(h)`, and "" for any other callee.
func calleeName(callee ast.Expr) string {
	switch callee := callee.(type) {
	case *ast.IdentExpr:
		return callee.Name
	case *ast.MemberExpr:
		return callee.Prop.Name
	}
	return ""
}

// callSite is one call in the body, the statement it runs at, and the loan sequence reserved
// for it when it was walked.
type callSite struct {
	node ast.Node
	ref  liveness.StmtRef
	seq  int
}

// noteCallSite records the call e at `ref`, reserving a loan sequence for it so a loan the
// post-pass takes at the call orders against the reads and borrows walked around it.
func (c *checker) noteCallSite(e ast.Node, ref liveness.StmtRef) {
	if c.fn == nil || c.fn.cfg == nil {
		return
	}
	c.fn.callSites = append(c.fn.callSites, callSite{node: e, ref: ref, seq: c.nextLoanSeq()})
}

// recordRetainedCaptureLoans finds each owned local a closure writes when the closure reaches a
// store noteRetainedClosureStore recorded. The closure is one written inside the stored value or
// one the value reaches through the borrow graph at the store, so a closure bound to a name,
// copied to another name, or chosen on a branch counts. A local the closures only read is left
// out, and so is a capture holding a borrow.
//
// Each call the store reaches takes a mutable loan of each such local for the call's statement,
// and records a read of the local there. It reports each conflict between that loan and another
// loan live at the call. The walk has already recorded those loans by the time this runs, so it
// checks them itself. checkUsesAgainstLoans weighs a move at the call against the loan.
func (c *checker) recordRetainedCaptureLoans(flowBorrowGraph *flowBorrowGraph) {
	taken := map[int]set.Set[liveness.VarID]{}
	for _, store := range c.fn.retainedClosureStores {
		graph := flowBorrowGraph.fieldBorrowGraphBefore(store.ref)
		writes := map[liveness.VarID]bool{}
		var edges []fieldBorrow
		for _, closure := range closuresIn(store.expr) {
			for _, local := range c.fn.capturedLocals[closure] {
				writes[local.root] = writes[local.root] || local.mut
				collectAllEdgesFrom(local.root, set.NewSet[liveness.VarID](), graph, &edges)
			}
		}
		if p, ok := c.exprPlace(store.expr); ok && p.root > 0 {
			collectEdgesFrom(p.root, p.path, set.NewSet[liveness.VarID](), graph, &edges)
		}
		for _, edge := range edges {
			if edge.capture {
				writes[edge.referent] = writes[edge.referent] || edge.mut
			}
		}
		var roots []liveness.VarID
		for root, written := range writes {
			// A closure that only reads the local needs it unchanged only while the closure
			// runs, and the frame cannot write it then.
			if written && c.isOwnedMovable(c.fn.capturedTypes[root]) {
				roots = append(roots, root)
			}
		}
		// Sorting keeps the order of the loans, and so of any reports, the same from run to run.
		slices.Sort(roots)
		for i, call := range c.fn.callSites {
			// Anything that reaches the stored closure can call it, so a call after the store may
			// run it. A closure kept for the rest of the program is reachable from every such
			// call, and any other only while one of its holders is live. A call that retains the
			// closure takes the capture loans for its own statement where its argument is
			// inferred, so it is left out here.
			if !c.stmtReaches(store.ref, call.ref) || call.node == store.call {
				continue
			}
			if !store.static && !c.anyLiveAt(store.holders, call.ref) {
				continue
			}
			if taken[i] == nil {
				taken[i] = set.NewSet[liveness.VarID]()
			}
			for _, root := range roots {
				if taken[i].Contains(root) {
					continue
				}
				taken[i].Add(root)
				c.takeCallLoan(movePlace{root: root}, call, store.via)
			}
		}
	}
}

// anyLiveAt reports whether one of holders is live entering the statement at ref.
func (c *checker) anyLiveAt(holders []liveness.VarID, ref liveness.StmtRef) bool {
	return slices.ContainsFunc(holders, func(h liveness.VarID) bool {
		return c.liveAt(loan{holder: h, ref: ref}, ref)
	})
}

// takeCallLoan records a mutable loan of `place` for the statement `call` runs at, and a read
// of `place` there, after reporting each conflict with another loan live at that statement.
// `via` names the binding or parameter that keeps the closure the call may run, for the reports.
func (c *checker) takeCallLoan(place movePlace, call callSite, via string) {
	fresh := loan{place: place, mut: true, ref: call.ref, node: call.node, seq: call.seq, via: via}
	// The call's own borrow arguments are live for as long as the call runs.
	for _, arg := range c.fn.callArgLoans[call.node] {
		if conflicts(arg, fresh) {
			c.reportBorrowConflict(arg, fresh)
		}
	}
	for _, other := range c.fn.loans {
		if !conflicts(other, fresh) {
			continue
		}
		if other.seq < fresh.seq {
			if other.endSeq != 0 && other.endSeq < fresh.seq {
				continue
			}
			if c.liveAt(other, fresh.ref) {
				c.reportBorrowConflict(other, fresh)
			}
		} else if c.liveAt(fresh, other.ref) {
			c.reportBorrowConflict(fresh, other)
		}
	}
	c.fn.loans = append(c.fn.loans, fresh)
	c.fn.useSites = append(c.fn.useSites, moveUse{place: place, ref: call.ref, node: call.node, loanSeqAt: call.seq + 1, via: via})
}
