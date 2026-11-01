package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// This file defines the destinations a flow can have, the columns of F2's table.

// flowDest is the destination of one flow site. Each struct below is one kind of
// destination.
type flowDest interface {
	isFlowDest()
}

// ownedDest is a destination that takes ownership.
type ownedDest struct{}

// borrowDest is a destination that takes a borrow.
type borrowDest struct {
	// borrow is the kind of the borrow the destination takes.
	borrow borrowKind
	// lt is the lifetime of the borrow the destination takes.
	lt soltype.Lifetime
	// reborrows marks a destination that borrows a borrow source again rather than
	// copying it, which F4 gives a method receiver and a pattern over a borrow.
	reborrows bool
}

// inferredReturnDest is a return with no annotation. It takes ownership of an owned
// value and carries a borrow on as one, as F3's `make` and `same` show.
type inferredReturnDest struct{}

// paramDest is a parameter whose ownership the callee's body decides.
type paramDest struct {
	// param is the parameter the flow passes to.
	param *soltype.FuncParam
	// reborrows is the reborrows flag the borrowDest the parameter settles into carries.
	reborrows bool
	// callLifetime, when set, is the lifetime a borrow into the parameter takes in place
	// of the one the parameter declares.
	callLifetime soltype.Lifetime
}

// calleeDest is an argument of a call whose callee's signature the call could not see.
type calleeDest struct {
	// call is the call the argument belongs to.
	call *ast.CallExpr
	// index is the position of the argument in call.
	index int
}

// captureDest is a closure capture. F3 decides it from where the closure flows.
type captureDest struct {
	// writes marks a capture whose closure writes the captured local.
	writes bool
}

func (*ownedDest) isFlowDest()          {}
func (*borrowDest) isFlowDest()         {}
func (*inferredReturnDest) isFlowDest() {}
func (*paramDest) isFlowDest()          {}
func (*calleeDest) isFlowDest()         {}
func (*captureDest) isFlowDest()        {}
