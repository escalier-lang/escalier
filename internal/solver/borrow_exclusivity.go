package solver

import (
	"fmt"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// Borrow exclusivity. Data a borrow can write through may not be reachable at the same time
// through a borrow that expects it to hold still. A mutable borrow beside a shared one is the
// pair that breaks: the shared view promises stability and the mutable view can take it away.
//
// Two mutable borrows of one value are allowed. That is Rule 3 of
// planning/lifetimes/requirements.md, a deliberate departure from Rust: both views agree the
// data can change, and with one thread neither can observe a tear. `&mut` is invariant in its
// referent, so two mutable borrows of one place also agree on its type and neither can write a
// value the other would misread.
//
// Two shared borrows are fine for the mirror-image reason, since neither writes.
//
// A loan is one borrow the checker tracks. It records the place the borrow reaches, whether a
// write can go through it, and how long it stays usable. Loans come from two sites:
//
//   - A `val`/`var` initializer that borrows a place, as in `val a = &mut x`. The binding holds
//     the loan, so the loan lasts as long as that binding is live.
//   - A call argument filling a `&` or `&mut` parameter. The callee holds it for the length of
//     the call and no name outlives that, so the loan lasts for the one statement.
//
// A call argument's mutability comes from the PARAMETER, not from the argument. Passing
// `&mut x` to a `&T` parameter hands the callee a read-only view, so the callee cannot write
// through it and it counts as shared. `f(&mut x, &mut x)` against `fn f(a: &T, b: &T)` is
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
//   - A borrow reaching a place through another binding. `val a = &x` then `f(a, &mut x)`
//     reports at the second borrow rather than at the call, and `val a = &x; val c = a` leaves
//     c untracked, so a conflict against c is missed.
//   - A borrow stored into a container and read back out. The place a loan names is the one
//     written at the borrow site.
//   - A trailing argument absorbed by a rest parameter. checkCallBorrowExclusivity pairs each
//     argument with the parameter at its own index and stops past the last one, so a call that
//     passes more arguments than the signature declares leaves the extras unchecked. Reaching
//     them means reading the rest parameter's ELEMENT type, which the arity-only model of
//     FuncParam.Rest does not settle yet.
//   - Loans crossing a loop back edge. Each loan is checked against the loans recorded before
//     it in source order, so a borrow created late in a body is not checked against one the
//     next iteration would still hold.
//   - Anything rooted at a method's receiver. A `self` reference carries VarID 0, so
//     `&mut self.p` names no binding and records no loan. #1486 covers that, and it reaches
//     further than this check: every analysis built on places sees the same 0.

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
	// is true, since a conflicting pair is one mutable borrow beside one shared borrow. Second is
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
	// ref is the statement the borrow is created at.
	ref liveness.StmtRef
	// node is the expression the diagnostic blames.
	node ast.Node
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
	fresh := loan{
		place:  place,
		mut:    borrow.Mut,
		holder: liveness.VarID(holder),
		ref:    ref,
		node:   borrow,
	}
	c.checkAgainstHeldLoans(fresh)
	c.fn.loans = append(c.fn.loans, fresh)
}

// dropLoansHeldBy removes the loans bound to holder, which a reassignment of that binding has
// made unreachable.
func (c *checker) dropLoansHeldBy(holder liveness.VarID) {
	kept := c.fn.loans[:0]
	for _, l := range c.fn.loans {
		if l.holder != holder {
			kept = append(kept, l)
		}
	}
	c.fn.loans = kept
}

// checkCallBorrowExclusivity reports two arguments of one call that borrow overlapping data
// where at least one parameter can write. Every argument of a call is live at once, so the
// arguments are compared against each other as well as against the loans already held.
//
// `f(&x, &mut x)` and `f(m, m)` filling a shared and a mutable parameter both report. The
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
