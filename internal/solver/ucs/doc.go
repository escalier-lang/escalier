// Package ucs holds the conditional-normalization IR that Escalier's rich
// conditional surface lowers into. The three surface forms are `match`, `if val`,
// and `val … else`. The pipeline follows "The Ultimate Conditional Syntax" by Cheng
// and Parreaux, OOPSLA 2024, which the package is named after. See
// planning/ucs/implementation_plan.md.
//
// The package is pure IR with no behavior of its own. It imports internal/ast for
// the surface nodes it points back at. It never imports internal/solver or
// internal/soltype, so the typing walk can import it and a future internal/codegen
// consumer can too, with no cycle either way.
package ucs
