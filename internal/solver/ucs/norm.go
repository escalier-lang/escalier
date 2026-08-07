package ucs

import "github.com/escalier-lang/escalier/internal/ast"

// Norm is a term of the normalized form, the backtracking-free rewrite of the core.
// Every split tests one tag-level of one scrutinee and hands its sub-scrutinees to
// inner splits, so a consumer reasons about one tag-level at a time instead of
// walking a deep shape. A failed test falls to the enclosing split's default tail and
// never retries an earlier branch.
type Norm interface {
	Term
	isNorm()
}

func (*NormSplit) isNorm() {}
func (*NormGuard) isNorm() {}
func (*NormBind) isNorm()  {}

// NormSplit tests one tag-level of one scrutinee. Branches that tested the same
// scrutinee against different tags in the core are merged into one split here, so
// each scrutinee is visited once.
type NormSplit struct {
	Scrutinee *Scrutinee
	Branches  []*NormBranch
	// Default is where control goes when no branch's test matches. A nil Default
	// means no branch covers the remaining values, which the printer renders `✗`. A
	// catch-all arm and an `else` both land here.
	Default Norm
	Origin
}

// NormBranch is one branch of a normalized split: a single tag test and what runs
// when it matches.
type NormBranch struct {
	Test Test
	// Cont is what runs once Test matches. Bindings the branch introduces are
	// NormBind nodes at the head of Cont, and a guarded arm puts a NormGuard after
	// them so the guard reads those bindings.
	Cont Norm
	// Arm is the surface arm this branch came from. It survives merging and
	// flattening, so a diagnostic blames the arm the user typed. Origin.Node can
	// point at a synthesized node after those rewrites; Arm never does.
	Arm Spanned
	Origin
}

// NormGuard tests a boolean condition over the bindings its branch introduced.
// Unlike CoreGuard it names where a failed test continues, which is what removes the
// backtracking. Control moves to Default rather than retrying the branches above it.
//
// Default is the continuation the branch would have fallen through to in source
// order. For a two-arm match it is the enclosing split's tail, which is why a
// guarded branch covers nothing for exhaustiveness. Its continuation can always
// reach a later arm.
type NormGuard struct {
	Cond    ast.Expr
	Cont    Norm
	Default Norm
	Origin
}

// NormBind names a value projected out of a scrutinee, which is how a normalized
// branch introduces the leaves its pattern bound. In `Point { x, y }` the branch
// tests the `Point` tag and then binds `x` and `y` from the projections `p.x` and
// `p.y`.
type NormBind struct {
	// Name is the bound identifier.
	Name string
	// Pat is the pattern leaf the name came from, which the solver binds through so
	// the leaf keeps its annotation and its borrow mode. It is nil for a name the
	// desugarer invented, which has no pattern leaf behind it.
	Pat ast.Pat
	// Source is the projection the name binds to. Sibling binds under one branch
	// share their parent *Scrutinee, so the parent is evaluated once.
	Source *Scrutinee
	// Cont is what runs with Name in scope.
	Cont Norm
	Origin
}
