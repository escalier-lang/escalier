package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// Info is the AST->type side table (à la go/types.Info). The new checker records
// inferred types here rather than mutating ast nodes via InferredType() /
// SetInferredType(). No probe/cleanup discipline in M1 — that arrives with
// Prov/Probe in a later milestone.
type Info struct {
	types map[ast.Node]soltype.Type
}

// NewInfo returns an empty Info side table ready to record inferred types.
func NewInfo() *Info {
	return &Info{types: map[ast.Node]soltype.Type{}}
}

// TypeOf returns the type recorded for n, or nil if none has been set.
func (i *Info) TypeOf(n ast.Node) soltype.Type {
	return i.types[n]
}

// setType records t as the inferred type of n, overwriting any prior entry.
func (i *Info) setType(n ast.Node, t soltype.Type) {
	i.types[n] = t
}
