package ucs

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
)

// Scrutinee is the value a split tests. The root scrutinee is the match target the
// user wrote. Every other scrutinee is a projection out of an enclosing one, such as
// the field `x` of an object an outer split already matched.
//
// A scrutinee is a shared node, not a path each consumer re-derives. An inner split
// and every bind beneath it hold the same *Scrutinee pointer, so a codegen consumer
// can evaluate it once into a local and read every projection off that local.
// Sharing is also what keeps a side-effecting target evaluated exactly once. In
// `match f() { … }` the root scrutinee is the single `f()` node, so no test or bind
// re-runs the call.
type Scrutinee struct {
	// Target is the match target expression. It is set exactly on a root scrutinee,
	// where Parent is nil.
	Target ast.Expr
	// Parent is the scrutinee this one projects out of. It is nil on a root.
	Parent *Scrutinee
	// Step is the projection that reaches this scrutinee from Parent. It is nil on a
	// root.
	Step Step
	// Origin points at the source pattern this projection came from, so a split over
	// a flattened sub-scrutinee blames the user's nested pattern rather than the
	// internal path.
	Origin
}

// NewRoot builds the scrutinee for a match target expression.
func NewRoot(target ast.Expr, origin Origin) *Scrutinee {
	return &Scrutinee{Target: target, Origin: origin}
}

// Project builds the sub-scrutinee reached by applying step to s. Callers share one
// result across the inner split and its binds rather than projecting the same step
// twice, so the projection is evaluated once.
func (s *Scrutinee) Project(step Step, origin Origin) *Scrutinee {
	return &Scrutinee{Parent: s, Step: step, Origin: origin}
}

// IsRoot reports whether s is a match target rather than a projection.
func (s *Scrutinee) IsRoot() bool { return s.Parent == nil }

// Step is one segment of a projection path: how a sub-scrutinee is reached from the
// scrutinee that encloses it.
type Step interface{ isStep() }

func (FieldStep) isStep()     {}
func (IndexStep) isStep()     {}
func (ResultStep) isStep()    {}
func (SuffixStep) isStep()    {}
func (RemainderStep) isStep() {}

// FieldStep reaches an object field by name, the `x` of `p.x`.
type FieldStep struct{ Name string }

// IndexStep reaches a tuple element by position, the `0` of `xs.0`.
type IndexStep struct{ Index int }

// ResultStep reaches one of the positional values an extractor yields. The `v` of
// `Ok(v)` is result 0 of the `Ok` extractor. It is a separate step from IndexStep
// because the solver resolves it through the extractor rather than through tuple
// indexing, even though both name a position.
type ResultStep struct{ Index int }

// SuffixStep reaches the tuple elements past a fixed prefix, which is what `...rest`
// binds in a tuple pattern. From is the index of the suffix's first element, so
// `[first, ...rest]` reaches `rest` through SuffixStep{From: 1}.
type SuffixStep struct{ From int }

// RemainderStep reaches an object with a set of keys removed, which is what
// `...rest` binds in an object pattern. Exclude holds exactly the keys the object
// pattern named at this level, so a key matched only by a deeper nested split still
// appears in the remainder. In `{x, ...rest}` the step excludes `x` and `rest` binds
// everything else.
type RemainderStep struct{ Exclude set.Set[string] }
