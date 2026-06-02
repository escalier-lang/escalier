package solver

import "github.com/escalier-lang/escalier/internal/solver/soltype"

// Context owns the engine's mutable counters. M1 carries ONLY varCounter; the
// spike's lifetimeCounter / paramLifetimes / written fields are M4 (lifetimes
// and records/mut), so M1's Context is correspondingly lean.
type Context struct {
	varCounter int
}

// freshVar allocates a new inference variable at the given level, assigning it
// the next id in sequence.
func (c *Context) freshVar(level int) *soltype.TypeVarType {
	v := &soltype.TypeVarType{ID: c.varCounter, Level: level}
	c.varCounter++
	return v
}
