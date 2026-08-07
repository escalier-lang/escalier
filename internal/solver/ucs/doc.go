// Package ucs holds the conditional-normalization IR that Escalier's rich
// conditional surface lowers into. The three surface forms are `match`, `if val`,
// and `val … else`. The pipeline follows "The Ultimate Conditional Syntax" by Cheng
// and Parreaux, OOPSLA 2024, which the package is named after. See
// planning/ucs/implementation_plan.md.
//
// Two foundations are shared by everything the package will hold. An Origin records
// which surface node a term came from, so a diagnostic raised anywhere in the
// pipeline can point back at what the user typed. A Scrutinee names the value a test
// examines, either a match target or a projection out of one.
//
// The package is pure IR with no behavior of its own. It imports internal/ast for
// the surface nodes it points back at and internal/set for key sets. It never
// imports internal/solver or internal/soltype, so the typing walk can import it and
// a future internal/codegen consumer can too, with no cycle either way.
package ucs
