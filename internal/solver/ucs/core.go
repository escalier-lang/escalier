package ucs

import "github.com/escalier-lang/escalier/internal/ast"

// Core is a term of the desugared core, the small language every conditional
// surface form lowers into. The core still carries whole source patterns.
// Flattening them into one-tag-level splits is what normalization does.
type Core interface {
	Term
	isCore()
}

func (*CoreSplit) isCore() {}
func (*CoreGuard) isCore() {}
func (*CoreBind) isCore()  {}

// CoreSplit tests a scrutinee against an ordered list of branches. Order is source
// order, and the first branch whose pattern matches wins, so the core preserves the
// surface's first-match semantics directly.
type CoreSplit struct {
	Scrutinee *Scrutinee
	Branches  []*CoreBranch
	// Else is the fallthrough taken when no branch matches. An `if val` and a
	// `val … else` always set it, since both write an `else` path. A `match` leaves
	// it nil, because a catch-all arm is an ordinary branch in the core and only
	// normalization moves it into the default tail. A nil Else prints as no `else`
	// clause at all.
	Else Core
	Origin
}

// CoreBranch is one arm of a core split. It keeps the arm's source pattern whole,
// nesting included.
type CoreBranch struct {
	// Pattern is the arm's pattern exactly as the user wrote it.
	Pattern ast.Pat
	// Cont is what runs once Pattern matches. A guarded arm puts a CoreGuard here so
	// the guard reads the bindings the pattern introduced.
	Cont Core
	// Arm is the surface arm this branch came from. It survives the merging and
	// flattening that normalization does, so a diagnostic blames the arm the user
	// typed. Origin.Node can point at a synthesized node after those rewrites; Arm
	// never does.
	Arm Spanned
	Origin
}

// CoreGuard tests a boolean condition after its branch's bindings are in scope, so
// the condition can read them. In `{x, y} if x > y => x` the guard sees `x` and `y`.
//
// A core guard has no failure continuation. A failed guard falls through to the
// remaining branches of the enclosing split in source order, which the ordered
// branch list already expresses. Normalization makes that continuation explicit as
// NormGuard.Default.
type CoreGuard struct {
	Cond ast.Expr
	Cont Core
	Origin
}

// CoreBind names an intermediate value so later stages see every binding the same
// way, whether the user wrote it or the desugarer introduced it.
type CoreBind struct {
	// Name is the bound identifier.
	Name string
	// Pat is the pattern leaf the name came from, which the solver binds through so
	// the leaf keeps its annotation and its borrow mode. It is nil for a name the
	// desugarer invented, which has no pattern leaf behind it.
	Pat ast.Pat
	// Source is the value the name binds to.
	Source *Scrutinee
	// Cont is what runs with Name in scope.
	Cont Core
	Origin
}
