package ucs

import (
	"slices"
	"strconv"
	"strings"

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
//
// Compare two steps with Equal, never with ==. RemainderStep holds a set, which is a
// map, so == on two Step values panics at runtime with "comparing uncomparable type"
// the moment either side is a RemainderStep.
type Step interface {
	isStep()
	// Equal reports whether other names the same projection.
	Equal(other Step) bool
}

func (FieldStep) isStep()     {}
func (IndexStep) isStep()     {}
func (ResultStep) isStep()    {}
func (SuffixStep) isStep()    {}
func (RemainderStep) isStep() {}

// FieldStep reaches an object field by name, the `x` of `p.x`.
type FieldStep struct{ Name string }

func (s FieldStep) Equal(other Step) bool {
	o, ok := other.(FieldStep)
	return ok && o.Name == s.Name
}

// IndexStep reaches a tuple element by position, the `0` of `xs.0`.
type IndexStep struct{ Index int }

func (s IndexStep) Equal(other Step) bool {
	o, ok := other.(IndexStep)
	return ok && o.Index == s.Index
}

// ResultStep reaches one of the positional values an extractor yields. The `v` of
// `Ok(v)` is result 0 of the `Ok` extractor. It is a separate step from IndexStep
// because the solver resolves it through the extractor rather than through tuple
// indexing, even though both name a position. The printer keeps them apart too, so a
// path that should go through an extractor cannot pass for a tuple index.
type ResultStep struct{ Index int }

func (s ResultStep) Equal(other Step) bool {
	o, ok := other.(ResultStep)
	return ok && o.Index == s.Index
}

// SuffixStep reaches the tuple elements past a fixed prefix, which is what `...rest`
// binds in a tuple pattern. From is the index of the suffix's first element, so
// `[first, ...rest]` reaches `rest` through SuffixStep{From: 1}.
//
// Only a trailing rest is expressible. The suffix a SuffixStep names runs to the end
// of the tuple, so there is no way to say "and then two more fixed elements". The
// parser does accept a rest anywhere in a tuple pattern, as in
// `[first, ...mid, last]`, but nothing downstream supports one. bindPattern's
// TuplePat case folds every rest element into a single inexact tail the same way. A
// lowering pass must reject a non-trailing rest rather than emit a SuffixStep that
// silently drops the elements after it.
type SuffixStep struct{ From int }

func (s SuffixStep) Equal(other Step) bool {
	o, ok := other.(SuffixStep)
	return ok && o.From == s.From
}

// RemainderStep reaches an object with a set of keys removed, which is what
// `...rest` binds in an object pattern. Exclude holds exactly the keys the object
// pattern named at this level, so a key matched only by a deeper nested split still
// appears in the remainder. In `{x, ...rest}` the step excludes `x` and `rest` binds
// everything else.
type RemainderStep struct{ Exclude set.Set[string] }

func (s RemainderStep) Equal(other Step) bool {
	o, ok := other.(RemainderStep)
	return ok && o.Exclude.Equals(s.Exclude)
}

// String renders the scrutinee's projection path.
func (s *Scrutinee) String() string { return scrutineeString(s) }

// scrutineeString renders a scrutinee's projection path. A root renders its target
// expression and every projection appends its step, so `l.start.x` reads as the
// chain of steps that reaches it.
func scrutineeString(s *Scrutinee) string {
	if s == nil {
		return "<nil>"
	}
	if s.IsRoot() {
		return exprString(s.Target)
	}
	base := scrutineeString(s.Parent)
	switch step := s.Step.(type) {
	case FieldStep:
		return base + "." + step.Name
	case IndexStep:
		return base + "." + strconv.Itoa(step.Index)
	case ResultStep:
		// The `#` keeps an extractor result apart from the tuple element `r.0`. The
		// solver resolves the two through different lookups, so a snapshot must not
		// let one stand in for the other.
		return base + ".#" + strconv.Itoa(step.Index)
	case SuffixStep:
		return base + "[" + strconv.Itoa(step.From) + "..]"
	case RemainderStep:
		keys := step.Exclude.ToSlice()
		slices.Sort(keys)
		return base + " \\ {" + strings.Join(keys, ", ") + "}"
	default:
		return base + "." + nodeKind(s.Step)
	}
}
