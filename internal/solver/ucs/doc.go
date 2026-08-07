// Package ucs holds the conditional-normalization IR that Escalier's rich
// conditional surface lowers into. The three surface forms are `match`, `if val`,
// and `val … else`. The pipeline follows "The Ultimate Conditional Syntax" by Cheng
// and Parreaux, OOPSLA 2024, which the package is named after. See
// planning/ucs/implementation_plan.md.
//
// Two term languages live here.
//
// The desugared core is what the surface lowers into directly. A CoreSplit tests a
// scrutinee against an ordered list of branches, a CoreBind names an intermediate
// value, a CoreGuard tests a boolean over a branch's bindings, and a leaf ends a
// branch. A core branch keeps its arm's source pattern whole, nesting and all.
//
// The normalized form is a backtracking-free rewrite of the core. Every NormSplit
// tests exactly one tag-level of one scrutinee. A tag-level is the outermost tag a
// pattern names and nothing under it, so `Line { start: {x, y} }` contributes the
// `Line` tag alone. A nested sub-pattern becomes a projected sub-scrutinee with a
// split of its own, and a failed test falls to the split's default tail instead of
// retrying an earlier branch. The typing walk, the coverage check, and a later
// codegen consumer all read this form.
//
// The package is pure IR with no behavior of its own. It imports internal/ast for
// the surface nodes it points back at and internal/set for key sets. It never
// imports internal/solver or internal/soltype, so the typing walk can import it and
// a future internal/codegen consumer can too, with no cycle either way.
package ucs
