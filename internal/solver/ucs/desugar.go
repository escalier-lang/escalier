package ucs

import "github.com/escalier-lang/escalier/internal/ast"

// This file lowers the three conditional surface forms into the desugared core.
// `match`, `if val`, and `val … else` all become a CoreSplit over one root
// scrutinee, which is what lets one downstream walk replace the three hand-written
// paths in internal/solver.
//
// Lowering points at the surface nodes rather than copying them. A branch holds the
// arm's own ast.Pat, a leaf holds the arm's own ast.BlockOrExpr, and the root
// scrutinee holds the target expression the user wrote. Sharing those nodes is what
// keeps a side-effecting target evaluated once and lets a consumer read a pattern's
// annotations straight off the surface.
//
// Desugaring mints no CoreBind. A core branch keeps its arm's pattern whole, so
// every name the arm binds is still inside that pattern and nothing needs an
// intermediate name here. The binds a later stage reads, such as `bind x = p.x` for
// each leaf of `{x, y}`, come from normalization, which is what flattens a pattern
// into projections. CoreBind is the node to reach for when a surface form names a
// value ahead of its split.

// DesugarMatch lowers a `match` expression into a core split. Each arm becomes one
// branch that keeps the arm's pattern whole, and a guarded arm puts a CoreGuard
// between its branch and its body so the condition reads the names the pattern
// bound.
//
// The split's Else stays nil. A `match` writes no fallthrough clause, and a
// catch-all arm is an ordinary branch in the core. Moving that arm into a default
// tail is normalization's job.
func DesugarMatch(e *ast.MatchExpr) *CoreSplit {
	origin := At(OriginMatchArm, e)
	branches := make([]*CoreBranch, len(e.Cases))
	for i, matchCase := range e.Cases {
		branches[i] = desugarMatchCase(matchCase)
	}
	return &CoreSplit{
		Scrutinee: NewRoot(e.Target, scrutineeOrigin(OriginMatchArm, e.Target, origin)),
		Branches:  branches,
		Origin:    origin,
	}
}

// scrutineeOrigin builds the origin of a root scrutinee. It blames the target
// expression rather than the whole construct. A message about the value being
// tested then underlines `f()` instead of the entire `match f() { … }`.
//
// A construct the parser left without a target has no expression to blame. Its
// origin is then synthetic with the construct as its cause, so NearestSpan still
// reaches a real span instead of naming no position at all.
func scrutineeOrigin(kind OriginKind, target ast.Expr, cause Origin) Origin {
	origin := At(kind, target)
	if origin.Synthetic {
		return InventedFrom(kind, cause)
	}
	return origin
}

// desugarMatchCase lowers one `match` arm into a branch. It takes an *ast.MatchCase
// rather than a whole expression so that more than one surface form can reach it.
// The AST stores a `TryCatchExpr`'s catch clauses as []*ast.MatchCase too, so those
// clauses can lower through this path once the solver types them.
func desugarMatchCase(matchCase *ast.MatchCase) *CoreBranch {
	origin := At(OriginMatchArm, matchCase)
	var cont Core = &BodyLeaf{Body: matchCase.Body, Arm: matchCase, Origin: origin}
	if matchCase.Guard != nil {
		// The guard sits below the branch, so it runs with the pattern's bindings in
		// scope. It names no failure continuation, because the branches after this one
		// already say where a failed guard goes.
		cont = &CoreGuard{
			Cond:   matchCase.Guard,
			Cont:   cont,
			Origin: At(OriginGuard, matchCase.Guard),
		}
	}
	return &CoreBranch{
		Pattern: matchCase.Pattern,
		Cont:    cont,
		Arm:     matchCase,
		Origin:  origin,
	}
}

// DesugarIfVal lowers `if val pat = target { cons } else { alt }` into the same
// two-branch split a two-arm `match` produces. The pattern branch carries the
// consequent, and the `else` becomes the split's fallthrough rather than a second
// branch.
func DesugarIfVal(e *ast.IfValExpr) *CoreSplit {
	origin := At(OriginIfVal, e)
	return &CoreSplit{
		Scrutinee: NewRoot(e.Target, scrutineeOrigin(OriginIfVal, e.Target, origin)),
		Branches: []*CoreBranch{{
			Pattern: e.Pattern,
			Cont:    &BodyLeaf{Body: ast.BlockOrExpr{Block: &e.Cons}, Arm: e, Origin: origin},
			Arm:     e,
			Origin:  origin,
		}},
		Else:   ifValElse(e, origin),
		Origin: origin,
	}
}

// ifValElse builds the fallthrough of an `if val`. One that wrote an `else` gets a
// leaf holding that block or expression.
//
// An `if val` with no `else` still falls through, evaluating to `undefined` when the
// pattern does not match. Its fallthrough is therefore invented rather than left
// empty. A nil Else would instead claim no continuation covers the failure. Giving
// the invented leaf a real `undefined` body is what lets a later walk infer it as an
// ordinary expression rather than special-casing a missing `else`.
//
// The leaf is synthetic and carries no arm back-reference, since no surface arm
// produced it. Its cause is `origin`, so NearestSpan reaches the `if val` the user
// wrote. The `undefined` expression takes that same span. A consumer reads a body's
// position through BodySpan, which knows nothing about Origin and so cannot fall
// back to the cause chain, and an empty span there would put a diagnostic at line 0
// of whichever file holds source ID 0.
func ifValElse(e *ast.IfValExpr, origin Origin) Core {
	if e.Alt != nil {
		return &BodyLeaf{Body: *e.Alt, Arm: e, Origin: origin}
	}
	undefined := ast.NewLitExpr(ast.NewUndefined(e.Span()))
	return &BodyLeaf{
		Body:   ast.BlockOrExpr{Expr: undefined},
		Origin: InventedFrom(OriginIfVal, origin),
	}
}

// DesugarValElse lowers `val pat = init else { … }` into a split whose pattern
// branch is the success path and whose fallthrough is the `else`. It returns false
// for a declaration that is not a `val … else`. That is one with no `else` block, or
// one the parser left without an initializer.
//
// The success path is an EscapeLeaf rather than a body leaf. A `val … else` writes
// no body for the matched case. Its bindings escape into the enclosing block, and
// the rest of that block is the continuation.
//
// A narrowing annotation such as the `number` of `val x: number = u else { … }` sits
// on the declaration rather than on the pattern, so the branch pattern does not
// carry it. A consumer that needs it reads TypeAnn off the arm back-reference, which
// is the declaration itself.
func DesugarValElse(d *ast.VarDecl) (*CoreSplit, bool) {
	if d.Else == nil || d.Init == nil {
		return nil, false
	}
	origin := At(OriginValElse, d)
	return &CoreSplit{
		Scrutinee: NewRoot(d.Init, scrutineeOrigin(OriginValElse, d.Init, origin)),
		Branches: []*CoreBranch{{
			Pattern: d.Pattern,
			Cont:    &EscapeLeaf{Arm: d, Origin: origin},
			Arm:     d,
			Origin:  origin,
		}},
		Else: &FallbackLeaf{
			Body:   ast.BlockOrExpr{Block: d.Else},
			Arm:    d,
			Origin: origin,
		},
		Origin: origin,
	}, true
}
