package solver

import (
	"slices"
	"sort"
	"strings"

	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// The DNF/CNF normal forms MLstruct decides subtyping in, ported from MLscript's
// NormalForms.scala. A normal form is a Boolean formula over structural atoms and
// type variables, written either as a union of meets or as an intersection of
// joins. Pushing both sides of a constraint into one of those two shapes is what
// lets the solver decompose `sub <: super` deterministically instead of guessing
// which member of a union to try first.
//
// These types are SOLVER-INTERNAL and transient. They live for the duration of a
// constraint and are never stored in the Info side table, never handed to the
// printer, and never returned from coalescing. The surface representation stays
// soltype.Type, and toType converts a normal form back to one.
//
// # The two shapes
//
// A DNF is a union of Conjuncts and a CNF is an intersection of Disjuncts. A
// Conjunct is a meet of four parts, written `Lnf ∩ (⋂Vars) ∩ ¬Rnf ∩ (⋂¬NVars)`:
// a positive structural part, positive type variables, a negated structural part,
// and negated type variables. A Disjunct is the exact dual, a join written
// `Rnf ∪ (⋃Vars) ∪ ¬Lnf ∪ (⋃¬NVars)`. Negating one produces the other by
// permuting those four fields, which is De Morgan's law with no traversal.
//
// # Merge or keep separate
//
// LhsNf is an intersection of structural atoms and RhsNf is a union of them. When
// two atoms of the same kind land in one of those lists, the list tries to fuse
// them into a single atom and KEEPS THEM SEPARATE when no fusion is exact. Keeping
// members separate loses nothing: a two-member list is already the precise meet or
// join. Fusing where the fusion is inexact is what would lose precision, so the
// merge is an optimization that bails rather than a step that must succeed. This
// is caveat 4 in planning/ml_struct/02-caveats-and-mitigations.md.
//
// # Two shape decisions this file owns
//
// MLscript's normal form is lossy in two places, both a consequence of holding one
// slot per structural kind. This port holds a LIST per side instead, so neither
// loss is inherited. See planning/ml_struct/06-open-items.md findings 1 and 2.
//
//  1. LhsNf keeps intersected function atoms SEPARATE when they cannot fuse
//     exactly. MLscript fuses every pair to `(l0 | l1) -> (r0 & r1)`, which is
//     unsound when the codomains conflict. Keeping the arms apart is what lets
//     PR5 (#1062) decide an arrow intersection by the Frisch-Castagna-Benzaken
//     decomposition, the set-theoretically sound rule the conformance corpus in
//     constrain_nf_test.go states verdicts against. meetFuncs still performs the
//     fusion for the two cases where it is exact.
//  2. RhsNf holds a LIST of record atoms. MLscript holds one, and widens a
//     disjunct to `unknown` on meeting a second differently-named field, which
//     makes every subtype pass against a supertype union of two records. A list
//     keeps `{x: number} | {y: number}` precise in supertype position.
//
// # Exactness
//
// An `Inexact` flag on an object, a tuple, a function, or a union marks the type
// OPEN: the object tolerates fields it does not name, the tuple tolerates
// positions past the ones it lists, and the function tolerates argument counts
// past its arity. The zero value is exact, which is Escalier's default.
//
// The merges below decide exactness rather than carrying it through, because a
// blind field-union would be unsound for an exact object. Exact `{x}` names its
// whole key set, so nothing is both exactly-`{x}` and exactly-`{y}` and their meet
// is `never`, where the inexact pair meets at `{x, y, ...}`. The rules per kind are
// on meetObjects, meetTuples, and meetFuncs.
//
// The open marker also runs in OPPOSITE directions per kind, which is why one
// shared rule would be wrong. An exact object is a subtype of the otherwise-equal
// inexact one, since capping the key set only removes values. An inexact FUNCTION
// is the subtype instead, since tolerating more argument counts only removes
// values. widerByExactness records both directions in one place.
//
// This file is the only place structural exactness is decided. newIntersection and
// newUnion in lattice.go flatten, prune, dedup, and order a lattice node. They
// leave an uninhabited meet standing, which is escalier-lang/escalier#927, so a
// merge here cannot delegate its exactness rule to them without asking the question
// back.
//
// # Borrows
//
// A RefType is an atom, and normalization never takes the wrapper apart. Two
// borrow atoms that land in one conjunct or disjunct do fuse, and the fusion
// splits its work by sort: the pointees combine in the type algebra and the two
// lifetimes combine in the outlives lattice. meetRefs and joinRefs are that split,
// and the comment above them states which lifetime pairs combine exactly.
//
// A borrow is also the one atom a complement may not name, the `¬Ref` exclusion
// invariant. soltype.AssertNegatable states it and the two NegationType arms of
// mkDNF and mkCNF enforce it. Negation INSIDE a borrow is a different node and
// normalizes by the ordinary rules, so `mut 'a ({x: number} | ¬{y: string})` is
// fine.

// DNF is a union of conjuncts, the disjunctive normal form of a type. An empty
// conjunct list is `never`, the identity of `|`.
type DNF struct{ Conjuncts []Conjunct }

// CNF is an intersection of disjuncts, the conjunctive normal form of a type. An
// empty disjunct list is `unknown`, the identity of `&`.
type CNF struct{ Disjuncts []Disjunct }

// Conjunct is one meet of a DNF, reading `Lnf ∩ (⋂Vars) ∩ ¬Rnf ∩ (⋂¬NVars)`. All
// four parts may be empty, and a Conjunct with all four empty is `unknown`.
type Conjunct struct {
	Lnf   LhsNf
	Vars  set.Set[*soltype.TypeVarType]
	Rnf   RhsNf
	NVars set.Set[*soltype.TypeVarType]
}

// Disjunct is one join of a CNF and the dual of Conjunct, reading
// `Rnf ∪ (⋃Vars) ∪ ¬Lnf ∪ (⋃¬NVars)`. A Disjunct with all four parts empty is
// `never`. Conjunct.neg and Disjunct.neg convert between the two by permuting the
// four fields.
type Disjunct struct {
	Rnf   RhsNf
	Vars  set.Set[*soltype.TypeVarType]
	Lnf   LhsNf
	NVars set.Set[*soltype.TypeVarType]
}

// LhsNf is a normalized INTERSECTION of structural atoms, the positive part of a
// Conjunct and the negated part of a Disjunct. Atoms is canonically ordered by
// sortAtoms and holds at most one atom per structural kind that fused; kinds that
// could not fuse exactly keep one atom each. An empty Atoms is `unknown`.
type LhsNf struct{ Atoms []soltype.Type }

// RhsNf is a normalized UNION of structural atoms, the dual of LhsNf. It is the
// positive part of a Disjunct and the negated part of a Conjunct. An empty Atoms
// is `never`.
type RhsNf struct{ Atoms []soltype.Type }

// Base returns the single nominal class tag the intersection carries, and reports
// whether it carries exactly one. This is the slot the nominal meet writes to.
// glbClass fuses two ordered tags into one and collapses two unordered ones to
// `never`, so an intersection that still holds two tags here is one glbClass found
// no exact fusion for. `Cell<number> & Cell<string>` at an invariant position is
// such a pair. The two tags name one class, and neither argument stands for the
// meet.
func (l LhsNf) Base() (*soltype.ClassType, bool) {
	var found *soltype.ClassType
	for _, atom := range l.Atoms {
		ct, ok := atom.(*soltype.ClassType)
		if !ok {
			continue
		}
		if found != nil {
			return nil, false
		}
		found = ct
	}
	return found, found != nil
}

// --- Construction ---

// mkDNF pushes t into disjunctive normal form at polarity pol. It normalizes the
// BOOLEAN structure only: a union, an intersection, a negation, and a type
// variable are taken apart, and every other node becomes one structural atom with
// its children untouched. mkDeepDNF normalizes those children too.
//
// pol is the position t occupies. A negation flips it before normalizing its
// operand, so the polarity stays accurate all the way down, but no arm below reads
// it for anything else. The shallow form is therefore the same at either polarity.
// pol earns its place in mkDeepDNF, which threads it into the children so a
// function's parameters normalize at the polarity they really occupy.
func (c *Context) mkDNF(t soltype.Type, pol soltype.Polarity) DNF {
	switch t := t.(type) {
	case *soltype.NeverType:
		return DNF{Conjuncts: nil}
	case *soltype.UnknownType:
		return dnfTop()
	case *soltype.UnionType:
		if t.Inexact && t.TailBound == nil {
			// `A | B | ...` names A, B, and an open tail nothing bounds. The tail has
			// no atom to stand for it, so taking the union apart would drop it and hand
			// back a type narrower than the source wrote. The whole node stays one atom,
			// which round-trips exactly and keeps the flag.
			//
			// The opaque atom is the final shape rather than a placeholder, and it costs
			// only DISTRIBUTION. `(A | B | ...) & C` stays a two-atom meet instead of
			// becoming `(A & C) | (B & C) | ...`. Two alternatives were weighed and both
			// are worse. An atom of its own for the tail would have to denote ⊤, which
			// absorbs the named members and turns `A | B | ...` into `unknown`, losing
			// exactly what the diagnostics print. An `Open` flag on the DNF would
			// distribute, but nothing says what the tail meets C to, so the mark would
			// have to be polarity-aware to stay sound. Neither buys a verdict the opaque
			// atom gets wrong. The full comparison is on escalier-lang/escalier#1064.
			return dnfAtom(t)
		}
		out := DNF{Conjuncts: nil}
		for _, m := range t.Types {
			out = dnfOr(out, c.mkDNF(m, pol))
		}
		if t.TailBound != nil {
			// A bounded tail holds some unknown set of the bound's values, so no value
			// outside the bound reaches the union and every value inside it might. That
			// makes the bound one more disjunct for a decision about which values the
			// union admits. `5 <: ("a" | ... : string)` fails and `"z" <: ("a" | ... : string)`
			// holds, both by the ordinary union rule once the bound joins the members.
			//
			// The bound joins the DNF rather than the member list, so the named members
			// stay enumerable for keyof and mapped types. Adding it to the members would
			// let subsumption drop `"a"` against `string` and leave nothing to iterate.
			out = dnfOr(out, c.mkDNF(t.TailBound, pol))
		}
		return DNF{Conjuncts: c.canonicalConjuncts(out.Conjuncts)}
	case *soltype.IntersectionType:
		out := dnfTop()
		for _, m := range t.Types {
			out = dnfAnd(out, c.mkDNF(m, pol))
		}
		return DNF{Conjuncts: c.canonicalConjuncts(out.Conjuncts)}
	case *soltype.NegationType:
		// ¬T at pol is the complement of T at the flipped polarity, and the complement
		// of an intersection of joins is a union of meets. So normalize the operand to a
		// CNF and negate each disjunct into a conjunct. Negating permutes fields without
		// consulting the merges, so the result is canonicalized afterwards.
		negated := c.mkCNF(t.Inner, pol.Flip()).neg()
		conjuncts := c.canonicalConjuncts(negated.Conjuncts)
		// A borrow lands in a conjunct's Rnf exactly when the complement names it, so
		// scanning the result is what enforces the ¬Ref exclusion invariant. The scan
		// reads the result rather than t.Inner because a borrow reaches a negated part
		// from two shapes, not one. `¬(mut 'a T)` names the borrow directly, and
		// `¬(A | mut 'a T)` reaches it through De Morgan's law, which turns the
		// complement of a join into a meet of complements.
		assertBorrowFreeNegatedParts(conjuncts, func(a Conjunct) []soltype.Type { return a.Rnf.Atoms })
		return DNF{Conjuncts: conjuncts}
	case *soltype.TypeVarType:
		return DNF{Conjuncts: []Conjunct{newConjunct().withVar(t)}}
	default:
		return dnfAtom(t)
	}
}

// mkCNF pushes t into conjunctive normal form at polarity pol, the dual of mkDNF.
func (c *Context) mkCNF(t soltype.Type, pol soltype.Polarity) CNF {
	switch t := t.(type) {
	case *soltype.UnknownType:
		return CNF{Disjuncts: nil}
	case *soltype.NeverType:
		return cnfBot()
	case *soltype.IntersectionType:
		out := CNF{Disjuncts: nil}
		for _, m := range t.Types {
			out = cnfAnd(out, c.mkCNF(m, pol))
		}
		return CNF{Disjuncts: c.canonicalDisjuncts(out.Disjuncts)}
	case *soltype.UnionType:
		if t.Inexact && t.TailBound == nil {
			// An unbounded tail has no atom to stand for it; see the mkDNF arm.
			return cnfAtom(t)
		}
		out := cnfBot()
		for _, m := range t.Types {
			out = cnfOr(out, c.mkCNF(m, pol))
		}
		if t.TailBound != nil {
			// A bounded tail contributes its bound; see the mkDNF arm.
			out = cnfOr(out, c.mkCNF(t.TailBound, pol))
		}
		return CNF{Disjuncts: c.canonicalDisjuncts(out.Disjuncts)}
	case *soltype.NegationType:
		// The dual of the mkDNF arm. A disjunct holds its negated part in Lnf, so that
		// is the list the ¬Ref scan reads.
		negated := c.mkDNF(t.Inner, pol.Flip()).neg()
		disjuncts := c.canonicalDisjuncts(negated.Disjuncts)
		assertBorrowFreeNegatedParts(disjuncts, func(a Disjunct) []soltype.Type { return a.Lnf.Atoms })
		return CNF{Disjuncts: disjuncts}
	case *soltype.TypeVarType:
		return CNF{Disjuncts: []Disjunct{newDisjunct().withVar(t)}}
	default:
		return cnfAtom(t)
	}
}

// mkDeepDNF pushes t into disjunctive normal form and normalizes every position
// INSIDE its structural atoms as well, so a negation buried in a function's return
// or an object's field is pushed down rather than left as a surface node. This is
// the form constraint solving consumes. PR5 (#1062) calls it on both operands.
func (c *Context) mkDeepDNF(t soltype.Type, pol soltype.Polarity) DNF {
	return c.mkDNF(c.normalizeDeep(t, pol), pol)
}

// mkDeepCNF is the conjunctive twin of mkDeepDNF.
func (c *Context) mkDeepCNF(t soltype.Type, pol soltype.Polarity) CNF {
	return c.mkCNF(c.normalizeDeep(t, pol), pol)
}

// normalizeDeep rewrites every node of t into normal form and back, bottom-up, so
// the result is a surface type whose every position is normalized. It rides the
// soltype rewriting visitor, which visits a function's parameters and a
// negation's operand at the flipped polarity, so each position normalizes at the
// polarity it occupies.
func (c *Context) normalizeDeep(t soltype.Type, pol soltype.Polarity) soltype.Type {
	return t.Accept(&deepNormalizer{ctx: c}, pol)
}

// deepNormalizer is the rewriting visitor behind normalizeDeep. ExitType fires
// bottom-up, after a node's children are already normalized, so normalizing the
// node itself only has to settle the Boolean structure at that one level.
type deepNormalizer struct{ ctx *Context }

// EnterType takes over the walk for a borrow, keeps a μ-knot as written, and
// leaves every other node to the ordinary bottom-up rebuild.
//
// A μ-knot stands for an infinite type, and comparing two of them terminates only
// because the solver's cache recognizes a pair of knots it is already deciding.
// That cache keys on node identity, so rewriting a knot into an equal one hands the
// comparison a pair it has never seen. Each unfolding would then rewrite the copy
// it produced and the comparison would never close. So the knot is kept as
// written, and the constraint rules normalize what an unfolding exposes.
//
// A borrow needs its own arm because RefType.Accept PEELS the wrapper when the
// rewritten inner is not a type a borrow can point at, which is right for
// coalescing but wrong here: normalizing the inner of `mut (number | 5)` gives
// `number`, and peeling would hand back a bare `number` that has lost the `mut`.
// So the inner is normalized here and the result is kept only when it is still
// borrowable. Otherwise the borrow stands as written, which keeps the wrapper the
// header promises normalization never takes apart.
func (n *deepNormalizer) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	if _, isKnot := t.(*soltype.RecursiveType); isKnot {
		return soltype.EnterResult{Type: t, SkipChildren: true}
	}
	ref, isRef := t.(*soltype.RefType)
	if !isRef {
		return soltype.EnterResult{Type: nil, SkipChildren: false}
	}
	inner, borrowable := ref.Inner.Accept(n, pol).(soltype.RefInner)
	if !borrowable {
		return soltype.EnterResult{Type: ref, SkipChildren: true}
	}
	return soltype.EnterResult{
		Type:         &soltype.RefType{Mut: ref.Mut, Lt: ref.Lt, Inner: inner},
		SkipChildren: true,
	}
}

func (n *deepNormalizer) ExitType(t soltype.Type, pol soltype.Polarity) soltype.Type {
	return n.ctx.mkDNF(t, pol).toType()
}

// assertBorrowFreeNegatedParts enforces the ¬Ref exclusion invariant over a
// normalized complement. A borrow in a negated part is a `¬(mut 'a T)` however the
// source wrote it. negatedPart reads that part off one member, Rnf for a conjunct
// and Lnf for a disjunct.
func assertBorrowFreeNegatedParts[T any](members []T, negatedPart func(T) []soltype.Type) {
	for _, member := range members {
		for _, atom := range negatedPart(member) {
			soltype.AssertNegatable(atom)
		}
	}
}

// dnfTop is `unknown` as a DNF: one conjunct with nothing in it, since an empty
// meet is the top of the lattice.
func dnfTop() DNF {
	return DNF{Conjuncts: []Conjunct{newConjunct()}}
}

// cnfBot is `never` as a CNF: one disjunct with nothing in it, since an empty join
// is the bottom of the lattice.
func cnfBot() CNF {
	return CNF{Disjuncts: []Disjunct{newDisjunct()}}
}

// dnfAtom is a single structural atom as a DNF.
func dnfAtom(t soltype.Type) DNF {
	return DNF{Conjuncts: []Conjunct{newConjunct().withAtom(t)}}
}

// cnfAtom is a single structural atom as a CNF.
func cnfAtom(t soltype.Type) CNF {
	return CNF{Disjuncts: []Disjunct{newDisjunct().withAtom(t)}}
}

// newConjunct is the `unknown` conjunct, the identity the intersection folds start
// from.
func newConjunct() Conjunct {
	return Conjunct{
		Lnf:   LhsNf{Atoms: nil},
		Vars:  set.NewSet[*soltype.TypeVarType](),
		Rnf:   RhsNf{Atoms: nil},
		NVars: set.NewSet[*soltype.TypeVarType](),
	}
}

// newDisjunct is the `never` disjunct, the identity the union folds start from.
func newDisjunct() Disjunct {
	return Disjunct{
		Rnf:   RhsNf{Atoms: nil},
		Vars:  set.NewSet[*soltype.TypeVarType](),
		Lnf:   LhsNf{Atoms: nil},
		NVars: set.NewSet[*soltype.TypeVarType](),
	}
}

func (a Conjunct) withAtom(t soltype.Type) Conjunct {
	a.Lnf = LhsNf{Atoms: []soltype.Type{t}}
	return a
}

func (a Conjunct) withVar(v *soltype.TypeVarType) Conjunct {
	a.Vars = set.FromSlice([]*soltype.TypeVarType{v})
	return a
}

func (a Disjunct) withAtom(t soltype.Type) Disjunct {
	a.Rnf = RhsNf{Atoms: []soltype.Type{t}}
	return a
}

func (a Disjunct) withVar(v *soltype.TypeVarType) Disjunct {
	a.Vars = set.FromSlice([]*soltype.TypeVarType{v})
	return a
}

// --- Negation ---

// neg complements a DNF into a CNF. `¬(C₁ ∪ … ∪ Cₙ)` is `¬C₁ ∩ … ∩ ¬Cₙ`, so the
// conjuncts negate one for one into disjuncts and no atom is traversed. An empty
// DNF is `never`, and it negates to an empty CNF, which is `unknown`.
func (d DNF) neg() CNF {
	out := make([]Disjunct, len(d.Conjuncts))
	for i, conj := range d.Conjuncts {
		out[i] = conj.neg()
	}
	return CNF{Disjuncts: out}
}

// neg complements a CNF into a DNF, the dual of DNF.neg.
func (n CNF) neg() DNF {
	out := make([]Conjunct, len(n.Disjuncts))
	for i, disj := range n.Disjuncts {
		out[i] = disj.neg()
	}
	return DNF{Conjuncts: out}
}

// neg complements a conjunct into a disjunct. `¬(L ∩ V ∩ ¬R ∩ ¬N)` is
// `R ∪ N ∪ ¬L ∪ ¬V`, which is the same four parts in the dual slots: the two
// structural parts swap roles and so do the two variable sets. De Morgan's law is
// therefore a field permutation here rather than a rewrite.
func (a Conjunct) neg() Disjunct {
	return Disjunct{Rnf: a.Rnf, Vars: a.NVars, Lnf: a.Lnf, NVars: a.Vars}
}

// neg complements a disjunct into a conjunct, the dual of Conjunct.neg.
func (a Disjunct) neg() Conjunct {
	return Conjunct{Lnf: a.Lnf, Vars: a.NVars, Rnf: a.Rnf, NVars: a.Vars}
}

// --- Boolean algebra over normal forms ---

// dnfOr unions two DNFs. A union of unions is one longer union, so the conjunct
// lists simply concatenate.
//
// Nothing is fused or ordered here, and neither does dnfAnd below. Fusing commits
// to one of several exact fusions and rules the others out, so fusing before every
// member of a union or intersection has been pooled would make the result depend
// on the order the members were written in. The mkDNF arms therefore pool first
// and call canonicalConjuncts once over the whole pool. Pooling is associative, so
// folding these binary operations left to right reaches the same pool as any other
// bracketing.
func dnfOr(a, b DNF) DNF {
	out := make([]Conjunct, 0, len(a.Conjuncts)+len(b.Conjuncts))
	out = append(out, a.Conjuncts...)
	out = append(out, b.Conjuncts...)
	return DNF{Conjuncts: out}
}

// dnfAnd intersects two DNFs by distributing the meet over both unions, so every
// conjunct of a meets every conjunct of b.
func dnfAnd(a, b DNF) DNF {
	out := make([]Conjunct, 0, len(a.Conjuncts)*len(b.Conjuncts))
	for _, x := range a.Conjuncts {
		for _, y := range b.Conjuncts {
			if met, ok := conjunctAnd(x, y); ok {
				out = append(out, met)
			}
		}
	}
	return DNF{Conjuncts: out}
}

// cnfAnd intersects two CNFs, the dual of dnfOr.
func cnfAnd(a, b CNF) CNF {
	out := make([]Disjunct, 0, len(a.Disjuncts)+len(b.Disjuncts))
	out = append(out, a.Disjuncts...)
	out = append(out, b.Disjuncts...)
	return CNF{Disjuncts: out}
}

// cnfOr unions two CNFs by distributing the join over both intersections, the dual
// of dnfAnd.
func cnfOr(a, b CNF) CNF {
	out := make([]Disjunct, 0, len(a.Disjuncts)*len(b.Disjuncts))
	for _, x := range a.Disjuncts {
		for _, y := range b.Disjuncts {
			if joined, ok := disjunctOr(x, y); ok {
				out = append(out, joined)
			}
		}
	}
	return CNF{Disjuncts: out}
}

// conjunctAnd intersects two conjuncts. The positive structural parts pool as one
// intersection, the negated ones pool as one union, since `¬R₁ ∩ ¬R₂` is
// `¬(R₁ ∪ R₂)`, and the two variable sets union. ok is false when a variable is
// held both positively and negatively.
//
// Whether the pooled structural parts are inhabited is settled later, by
// normalizeConjunct, so that every atom in the surrounding union or intersection
// is in the pool before any pair of them fuses.
func conjunctAnd(a, b Conjunct) (Conjunct, bool) {
	vars := a.Vars.Union(b.Vars)
	nvars := a.NVars.Union(b.NVars)
	// The same variable held both ways makes the conjunct `v ∩ ¬v`, which no value
	// inhabits under any instantiation, so no bound has to be consulted. Dropping
	// the conjunct rather than reporting an error is right because `never` is the
	// identity of the union its DNF is. The two sets are keyed by pointer, so this
	// fires only on one variable held both ways, never on two distinct variables
	// that solving later makes equal.
	if vars.Intersection(nvars).Len() > 0 {
		return Conjunct{}, false
	}
	return Conjunct{
		Lnf:   LhsNf{Atoms: pooled(a.Lnf.Atoms, b.Lnf.Atoms)},
		Vars:  vars,
		Rnf:   RhsNf{Atoms: pooled(a.Rnf.Atoms, b.Rnf.Atoms)},
		NVars: nvars,
	}, true
}

// disjunctOr unions two disjuncts, the dual of conjunctAnd. ok is false when a
// variable is held both positively and negatively.
func disjunctOr(a, b Disjunct) (Disjunct, bool) {
	vars := a.Vars.Union(b.Vars)
	nvars := a.NVars.Union(b.NVars)
	// The dual of the conjunct case. The same variable held both ways makes the
	// disjunct `v ∪ ¬v`, which every value inhabits under any instantiation, and it
	// is dropped because `unknown` is the identity of the intersection its CNF is.
	// The pointer keying is the same, so two distinct variables that solving later
	// makes equal do not fire this.
	if vars.Intersection(nvars).Len() > 0 {
		return Disjunct{}, false
	}
	return Disjunct{
		Rnf:   RhsNf{Atoms: pooled(a.Rnf.Atoms, b.Rnf.Atoms)},
		Vars:  vars,
		Lnf:   LhsNf{Atoms: pooled(a.Lnf.Atoms, b.Lnf.Atoms)},
		NVars: nvars,
	}, true
}

// --- The structural parts ---

// pooled concatenates two atom lists into a fresh slice, so fusing the result
// leaves both inputs alone. Two normal forms often share an atom list, since a
// conjunct that survived a merge unchanged keeps the slice it arrived with.
func pooled(a, b []soltype.Type) []soltype.Type {
	return slices.Concat(a, b)
}

// fuseAtoms fuses pairs of atoms until no pair fuses, which is the
// keep-un-mergeable-members-separate discipline: an atom that fuses with nothing
// simply stays in the list. role says whether the list stands for the meet of its
// atoms or their join, which picks meetAtoms or joinAtoms as the fusion. ok is
// false when a meet turns out to be uninhabited.
//
// The list is sorted before the scan and again after each fusion, so the pair
// chosen is decided by canonical order rather than by the order the atoms arrived
// in. That ordering carries weight once a kind admits more than one exact fusion
// of the same atoms, since taking one rules out the others. The arrows of
// `(number -> string) & (number -> boolean) & (string -> boolean)` fuse either by
// the shared domain of the first two, meeting their codomains, or by the shared
// codomain of the last two, joining their domains. The two results are equal types
// written differently. Scanning in canonical order settles which one a given SET
// of atoms reaches, so permuting the source members cannot change the normal form.
// TestCanonicalOrderIsPermutationStable runs that intersection in every order.
//
// Each fusion shortens the list, so the loop runs at most len(atoms) times.
func (c *Context) fuseAtoms(atoms []soltype.Type, role atomRole) ([]soltype.Type, bool) {
	sortAtoms(atoms)
	for {
		fused, i, j := c.firstFusablePair(atoms, role)
		if fused == nil {
			return atoms, true
		}
		if _, isNever := fused.(*soltype.NeverType); isNever {
			// Only a meet reaches this. `never` is the identity of the join a
			// joinOfAtoms list stands for, and joinAtoms never produces one anyway.
			return nil, false
		}
		atoms = replacePair(atoms, i, j, fused)
		sortAtoms(atoms)
	}
}

// firstFusablePair returns the fusion of the first pair of atoms that fuses,
// together with the two positions it replaces. The returned type is nil when no
// pair fuses.
func (c *Context) firstFusablePair(atoms []soltype.Type, role atomRole) (soltype.Type, int, int) {
	for i := range atoms {
		for j := i + 1; j < len(atoms); j++ {
			var fused soltype.Type
			var ok bool
			if role == meetOfAtoms {
				fused, ok = c.meetAtoms(atoms[i], atoms[j])
			} else {
				fused, ok = c.joinAtoms(atoms[i], atoms[j])
			}
			if ok {
				return fused, i, j
			}
		}
	}
	return nil, 0, 0
}

// replacePair drops positions i and j and appends the fused atom. i must be less
// than j, and the higher of the two is deleted first so the lower stays valid.
//
// It works in place rather than copying. fuseAtoms is its only caller and always
// owns the slice it passes, since every entry point clones before handing one
// over, and fuseAtoms already sorts that slice in place.
func replacePair(atoms []soltype.Type, i, j int, fused soltype.Type) []soltype.Type {
	atoms = slices.Delete(atoms, j, j+1)
	atoms = slices.Delete(atoms, i, i+1)
	return append(atoms, fused)
}

// withoutIndex returns a copy of atoms with element i removed. It copies because
// the caller's slice may be shared with another normal form.
func withoutIndex(atoms []soltype.Type, i int) []soltype.Type {
	out := make([]soltype.Type, 0, len(atoms)-1)
	out = append(out, atoms[:i]...)
	out = append(out, atoms[i+1:]...)
	return out
}

// sortAtoms orders an atom list in place, canonically.
//
// It orders by compareAtom rather than by compareType, because compareType has no
// structural arm for several kinds an atom can be. A class tag, an alias
// reference, and the residual type operators all land in one bucket and compare
// equal to each other there, so `Point & Line` and `Line & Point` would each keep
// the order they were written in and render differently.
func sortAtoms(atoms []soltype.Type) {
	sort.SliceStable(atoms, func(i, j int) bool { return compareAtom(atoms[i], atoms[j]) < 0 })
}

// compareAtom is the total order over atoms: compareType first, then the
// qualified rendering for two atoms compareType calls equal without their being
// equalType-equal. PrintQualified is the collision-free identity key the solver
// already forms elsewhere, and two atoms it also renders alike keep the order they
// arrived in, which is no worse than compareType alone.
func compareAtom(a, b soltype.Type) int {
	if c := compareType(a, b); c != 0 {
		return c
	}
	if equalType(a, b) {
		return 0
	}
	return strings.Compare(soltype.PrintQualified(a), soltype.PrintQualified(b))
}

// equalAtomLists reports whether two atom lists hold the same atoms in the same
// positions. Both are sorted, so this is the equality the merges test.
//
// It compares with equalType rather than by checking compareAtoms for a zero.
// compareType returns zero both for equal types and for two types it has no
// structural arm to tell apart, such as two different class tags, so a merge
// keying off a zero would treat `¬Point` and `¬Line` as the same negated part and
// drop one of them.
func equalAtomLists(a, b []soltype.Type) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !equalType(a[i], b[i]) {
			return false
		}
	}
	return true
}

// --- Atom merges ---

// meetAtoms fuses two atoms of an intersection into one. ok is false when no exact
// fusion exists, which tells the caller to keep both atoms. A returned `never`
// means the two atoms are disjoint, so the enclosing conjunct is uninhabited.
//
// The kinds not named in meetFusedAtoms never fuse. That costs nothing: a two-atom
// list is already the precise meet, and only the atom count grows.
//
// Every fusion mints one FromNormalization provenance edge here, at the single point
// each merge passes through, rather than in the per-kind helpers. The edge names a
// and b, the two atoms the merge combined. An identity or absorption result is one
// of the sources. `A & A` is `A` and `5 & number` is `5`, and recordFusionEdge skips
// such a result so the source keeps its own leaf origin. glbClass's meetClassArgs
// also runs from instanceBelow, which fuses two tags only to test a relation and
// discards the result. That call does not pass through here, so a throwaway tag meet
// mints no edge for the fused tag.
func (c *Context) meetAtoms(a, b soltype.Type) (soltype.Type, bool) {
	fused, ok := c.meetFusedAtoms(a, b)
	if ok {
		c.recordFusion(fused, a, b)
	}
	return fused, ok
}

// meetFusedAtoms computes the meet of two atoms without recording provenance.
func (c *Context) meetFusedAtoms(a, b soltype.Type) (soltype.Type, bool) {
	if equalType(a, b) {
		return a, true
	}
	if carriesKnot(a) || carriesKnot(b) {
		return nil, false
	}
	if fused, ok := meetValueAtoms(a, b); ok {
		return fused, true
	}
	switch a := a.(type) {
	case *soltype.ObjectType:
		if b, ok := b.(*soltype.ObjectType); ok {
			return c.meetObjects(a, b)
		}
	case *soltype.TupleType:
		if b, ok := b.(*soltype.TupleType); ok {
			return c.meetTuples(a, b)
		}
	case *soltype.FuncType:
		if b, ok := b.(*soltype.FuncType); ok {
			return c.meetFuncs(a, b)
		}
	case *soltype.ClassType:
		if b, ok := b.(*soltype.ClassType); ok {
			return c.glbClass(a, b)
		}
	case *soltype.RefType:
		if b, ok := b.(*soltype.RefType); ok {
			return c.meetRefs(a, b)
		}
	}
	return nil, false
}

// joinAtoms fuses two atoms of a union into one, the dual of meetAtoms. ok is
// false when no exact fusion exists, which keeps both atoms. This is where
// `{x: number} | {y: number}` stays two members: two records with different field
// names have no single record that stands for their union, so the merge bails and
// the union keeps both.
//
// It mints the FromNormalization edge naming a and b the way meetAtoms does.
func (c *Context) joinAtoms(a, b soltype.Type) (soltype.Type, bool) {
	fused, ok := c.joinFusedAtoms(a, b)
	if ok {
		c.recordFusion(fused, a, b)
	}
	return fused, ok
}

// joinFusedAtoms computes the join of two atoms without recording provenance.
func (c *Context) joinFusedAtoms(a, b soltype.Type) (soltype.Type, bool) {
	if equalType(a, b) {
		return a, true
	}
	if carriesKnot(a) || carriesKnot(b) {
		return nil, false
	}
	if wider, ok := widerByExactness(a, b); ok {
		return wider, true
	}
	if fused, ok := joinValueAtoms(a, b); ok {
		return fused, true
	}
	switch a := a.(type) {
	case *soltype.ObjectType:
		if b, ok := b.(*soltype.ObjectType); ok {
			return c.joinObjects(a, b)
		}
	case *soltype.TupleType:
		if b, ok := b.(*soltype.TupleType); ok {
			return c.joinTuples(a, b)
		}
	case *soltype.RefType:
		if b, ok := b.(*soltype.RefType); ok {
			return c.joinRefs(a, b)
		}
	}
	// Two function atoms never fuse under a union. Neither `(A -> C) | (B -> C)` nor
	// `(A -> C) | (A -> D)` has a single arrow that denotes it: a value of
	// `A -> (C | D)` may return a C on one input and a D on another, which no member
	// of the union permits.
	return nil, false
}

// widerByExactness returns the wider of two atoms that are EQUAL APART FROM their
// exactness, and reports whether the pair is such a pair. A union of two types
// where one contains the other is the containing one, so joinAtoms fuses to it.
//
// Which side is wider depends on the kind, because inexactness relaxes a different
// dimension in each one.
//
//   - An OBJECT's inexactness relaxes its key set. `{x: A, ...}` admits every
//     record `{x: A}` admits and also those carrying further fields, so the inexact
//     side is wider.
//   - A TUPLE's inexactness relaxes its length. `[A, ...]` admits every tuple `[A]`
//     admits and also the longer ones, so the inexact side is wider again.
//   - A FUNCTION's inexactness relaxes the argument counts a call may use, and the
//     value filling the type has to tolerate all of them. `fn (x: A, ...) -> B`
//     admits only functions taking any number of arguments, which is FEWER than
//     `fn (x: A) -> B` admits. Here the exact side is the wider one.
//
// Each arm tests likeness by copying one atom, flipping the copy's exactness to the
// other's, and comparing. That reuses equalType rather than re-spelling per-kind
// structural equality, and it keeps the test in step with equalType as kinds gain
// fields.
//
// An UNREDUCED atom is left alone, which is the same refusal plainProps and
// comparableTuples make. A `{...S}` does not know its own field names and a
// `[...P]` does not know its own positions, so neither supports the claim that the
// exact side caps what the inexact side leaves open. The constraint rules treat
// such an atom as inert and relate two of them only when they are equal. Fusing
// the pair therefore leaves those rules an atom they cannot take apart, and this
// declaration stops checking:
//
//	fn go<P>(x: [...P]) -> [...P] | [...P, ...] { return x }
func widerByExactness(a, b soltype.Type) (soltype.Type, bool) {
	switch a := a.(type) {
	case *soltype.ObjectType:
		b, isObj := b.(*soltype.ObjectType)
		if !isObj || a.Inexact == b.Inexact {
			return nil, false
		}
		if soltype.HasResidualElem(a.Elems) || soltype.HasResidualElem(b.Elems) {
			return nil, false
		}
		flipped := *a
		flipped.Inexact = b.Inexact
		if !equalType(&flipped, b) {
			return nil, false
		}
		if a.Inexact {
			return a, true
		}
		return b, true
	case *soltype.TupleType:
		b, isTuple := b.(*soltype.TupleType)
		if !isTuple || a.Inexact == b.Inexact {
			return nil, false
		}
		if hasSpreadElem(a.Elems) || hasSpreadElem(b.Elems) {
			return nil, false
		}
		flipped := *a
		flipped.Inexact = b.Inexact
		if !equalType(&flipped, b) {
			return nil, false
		}
		if a.Inexact {
			return a, true
		}
		return b, true
	case *soltype.FuncType:
		b, isFunc := b.(*soltype.FuncType)
		if !isFunc || a.Inexact == b.Inexact {
			return nil, false
		}
		flipped := *a
		flipped.Inexact = b.Inexact
		if !equalType(&flipped, b) {
			return nil, false
		}
		if a.Inexact {
			return b, true
		}
		return a, true
	}
	return nil, false
}

// carriesKnot reports whether t holds a μ-knot anywhere inside it. A fusion over
// such an atom is skipped, which keeps both atoms and loses no precision.
//
// The reason is the solver's cycle detection. Comparing two recursive types
// terminates because the constraint cache recognizes a pair of knots it is already
// deciding, and that cache keys on node identity. A fusion rebuilds the children it
// merges, so fusing `{next: undefined} | {next: μX.…}` into `{next: undefined | μX.…}`
// mints a fresh union node holding the knot. Each unfolding would mint another one,
// so the comparison would ask a pair the cache has never seen on every lap and would
// never close. Leaving the two atoms apart keeps the knot reachable as one operand
// of a pair, which is the pair the cache closes on.
func carriesKnot(t soltype.Type) bool {
	f := &knotFinder{}
	t.Accept(f, soltype.Positive)
	return f.found
}

// knotFinder is the walking visitor behind carriesKnot. It flags the first μ-knot
// it reaches and skips that node's children, since one occurrence is enough.
type knotFinder struct{ found bool }

func (f *knotFinder) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	if _, ok := t.(*soltype.RecursiveType); ok {
		f.found = true
		return soltype.EnterResult{SkipChildren: true}
	}
	return soltype.EnterResult{}
}

func (f *knotFinder) ExitType(t soltype.Type, pol soltype.Polarity) soltype.Type { return t }

// valueFamily is a set of runtime values no value outside it belongs to, so two atoms drawn
// from different families are disjoint and their meet is `never`. Five families cover the
// primitives and the two absence markers, and a sixth, refCellFamily, covers the borrows no
// primitive can be.
//
// Objects, tuples, functions, and class instances are deliberately absent. They are disjoint
// from a primitive too, but keeping two such atoms apart is already precise, so a family for
// them would buy nothing. refCellFamily uses only the cross-family rule, never the within-family
// one meetValueAtoms applies to the primitives, since two distinct borrows are not disjoint.
type valueFamily int

const (
	notValueAtom valueFamily = iota
	numberFamily
	stringFamily
	booleanFamily
	nullFamily
	undefinedFamily
	refCellFamily
)

// valueFamilyOf returns the family t draws its values from, and notValueAtom for
// a kind the families do not cover.
func valueFamilyOf(t soltype.Type) valueFamily {
	switch t := t.(type) {
	case *soltype.PrimType:
		return primFamily(t.Prim)
	case *soltype.LitType:
		return litFamily(t.Lit)
	case *soltype.NullType:
		return nullFamily
	case *soltype.UndefinedType:
		return undefinedFamily
	case *soltype.RefType:
		return refCellOrNot(t.Inner)
	}
	return notValueAtom
}

// refCellOrNot returns refCellFamily for a borrow over an object, a tuple, or a class
// instance, and notValueAtom for any other carrier, since a borrow is only as decided as the
// value it points at.
//
// Those three kinds stay themselves under every rewrite, so a borrow over one admits no
// primitive. The kinds it turns away can leave the RefInner set, and RefType.Accept peels a
// borrow whose inner does: a `mut β` whose β inlines to `string` becomes `string`, and a
// union, an intersection, or an alias can reduce to a bare primitive the same way. Reading
// such a borrow as disjoint from `string` would be wrong.
func refCellOrNot(carrier soltype.Type) valueFamily {
	switch carrier.(type) {
	case *soltype.ObjectType, *soltype.TupleType, *soltype.ClassType:
		return refCellFamily
	}
	return notValueAtom
}

func primFamily(p soltype.Prim) valueFamily {
	switch p {
	case soltype.NumPrim:
		return numberFamily
	case soltype.StrPrim:
		return stringFamily
	case soltype.BoolPrim:
		return booleanFamily
	}
	return notValueAtom
}

func litFamily(l soltype.Lit) valueFamily {
	switch l.(type) {
	case *soltype.NumLit:
		return numberFamily
	case *soltype.StrLit:
		return stringFamily
	case *soltype.BoolLit:
		return booleanFamily
	}
	return notValueAtom
}

// meetValueAtoms fuses two atoms drawn from the value families. Different
// families are disjoint, so the meet is `never`. Within one primitive family a
// literal is narrower than its primitive, so the literal wins, and two distinct
// atoms of the family are disjoint.
//
// Two borrows are the exception to that last rule, so they are handed back
// unfused. `mut {x: number}` and `mut {y: string}` are distinct atoms of
// refCellFamily and are not disjoint, since an object carrying both fields
// inhabits each. meetAtoms reaches meetRefs once this bails, and meetRefs decides
// the pair by comparing carriers.
//
// Equal atoms are answered here as well as by meetAtoms, which reaches them
// first. The overlap is deliberate. Without it `"hello"` met with `"hello"` would
// fall to the last rule and collapse to `never`, so the function would be right
// only for a caller that filters equal atoms out beforehand.
func meetValueAtoms(a, b soltype.Type) (soltype.Type, bool) {
	fa, fb := valueFamilyOf(a), valueFamilyOf(b)
	if fa == notValueAtom || fb == notValueAtom {
		return nil, false
	}
	if fa != fb {
		return &soltype.NeverType{}, true
	}
	if equalType(a, b) {
		return a, true
	}
	if fa == refCellFamily {
		return nil, false
	}
	if _, ok := a.(*soltype.PrimType); ok {
		return b, true
	}
	if _, ok := b.(*soltype.PrimType); ok {
		return a, true
	}
	// Two distinct atoms of one family, such as `5` and `6`.
	return &soltype.NeverType{}, true
}

// joinValueAtoms fuses two atoms drawn from the value families under a union. A
// primitive absorbs a literal of its own family, so `5 | number` is `number`.
// Anything else keeps both atoms, which is already precise.
//
// Two borrows keep both atoms as well. No borrow absorbs another the way a
// primitive absorbs its literal, so joinRefs decides them once this bails.
//
// Equal atoms are answered here too, for the reason meetValueAtoms gives. Without
// it two equal literals would read as un-fusable and stay two atoms, which is
// sound but needlessly imprecise.
func joinValueAtoms(a, b soltype.Type) (soltype.Type, bool) {
	fa, fb := valueFamilyOf(a), valueFamilyOf(b)
	if fa == notValueAtom || fb == notValueAtom || fa != fb {
		return nil, false
	}
	if equalType(a, b) {
		return a, true
	}
	if fa == refCellFamily {
		return nil, false
	}
	if _, ok := a.(*soltype.PrimType); ok {
		return a, true
	}
	if _, ok := b.(*soltype.PrimType); ok {
		return b, true
	}
	return nil, false
}

// meetObjects fuses two object atoms field by field, returns `never` when no value
// satisfies both, or keeps them apart when it can reach neither answer. Keeping two
// atoms is just as precise and costs only the extra atom, so the fusion is an
// optimization rather than an obligation.
//
// # What an object atom denotes
//
// Read an object as two bounds on the key set S of the values it admits. Every
// field it marks required is IN S, which is the floor. An exact object also caps S
// at the fields it names, and an inexact one leaves S uncapped.
//
//	{x: A, y?: B}       admits S = {x} and S = {x, y}
//	{x: A, y?: B, ...}  admits every S containing x
//
// # The rule
//
// A value has to satisfy both objects at once, so the meet's floor is the union of
// the two floors and its cap is the intersection of the two caps. Three rules
// follow.
//
//  1. Take a field one side REQUIRES and the other side caps out. Carrying it
//     breaks the second object's cap and omitting it breaks the first object's
//     floor, so no value satisfies both and the meet is `never`. This is what makes
//     exact `{x} & {y}` uninhabited.
//  2. A field inside the meet's cap survives. When both sides name it the meet
//     narrows it to their meet, and a field either side requires is required on the
//     meet, so `{x: A} & {x?: B}` is `{x: A & B}`.
//  3. A field only one side names and the other side caps out drops. Rule 1 already
//     answered the case where such a field is required, so nothing required is lost
//     here. Exact `{x: A, y?: B} & {x: C}` is `{x: A & C}`, because the second
//     object admits no y at all.
//
// The meet is exact when either side is, since one cap is enough to close the key
// set.
func (c *Context) meetObjects(a, b *soltype.ObjectType) (soltype.Type, bool) {
	pa, ok := plainProps(a)
	if !ok {
		return nil, false
	}
	pb, ok := plainProps(b)
	if !ok {
		return nil, false
	}
	if !requiredFieldsWithin(pa, pb, b.Inexact) || !requiredFieldsWithin(pb, pa, a.Inexact) {
		return &soltype.NeverType{}, true
	}
	elems := make([]soltype.ObjTypeElem, 0, len(pa)+len(pb))
	for _, p := range pa {
		q, shared := propNamed(pb, p.Name)
		if !shared {
			if !b.Inexact {
				continue
			}
			elems = append(elems, p)
			continue
		}
		// A readonly field and a writable one constrain what a holder may do rather
		// than which values inhabit the type, and no rule says which marker the fused
		// field should carry, so the two atoms stay separate.
		if p.Readonly != q.Readonly {
			return nil, false
		}
		elems = append(elems, &soltype.PropertyElem{
			Name:     p.Name,
			Type:     c.meetTypes(p.Type, q.Type),
			Optional: p.Optional && q.Optional,
			Readonly: p.Readonly,
		})
	}
	for _, q := range pb {
		if _, shared := propNamed(pa, q.Name); shared {
			continue
		}
		if !a.Inexact {
			continue
		}
		elems = append(elems, q)
	}
	return &soltype.ObjectType{Elems: sortedByName(elems), Inexact: a.Inexact && b.Inexact}, true
}

// requiredFieldsWithin reports whether every field props REQUIRES is one the
// allowed list names. Pass open as true when allowed came off an inexact object.
// Such an object caps nothing, so it admits any field name and the check passes
// without reading either list.
func requiredFieldsWithin(props, allowed []*soltype.PropertyElem, open bool) bool {
	if open {
		return true
	}
	for _, p := range props {
		if p.Optional {
			continue
		}
		if _, ok := propNamed(allowed, p.Name); !ok {
			return false
		}
	}
	return true
}

// joinObjects fuses two object atoms under a union. One object denotes their union
// only when the two carry the same field names and differ in AT MOST ONE field.
// `{x: A, y: C} | {x: B, y: C}` is `{x: A | B, y: C}`, since a value of the merged
// record has an x drawn from A or from B and is therefore a value of one member.
//
// A field is optional on the join when either side made it optional, so
// `{x: A} | {x?: B}` is `{x?: A | B}`. Such a value either carries no x, or
// carries one drawn from A or from B.
//
// Two differing fields break the fusion. `{x: A, y: C} | {x: B, y: D}` is not
// `{x: A | B, y: C | D}`, which would also admit a record pairing an x from A with
// a y from D, a record neither member admits. A marker difference spends the same
// budget, so `{x: A, y: C} | {x?: A, y: D}` keeps both atoms rather than fusing to
// a record that admits an absent x beside a y drawn from C. Differing field names
// break the fusion too, which is why `{x: number} | {y: number}` keeps both.
//
// Objects whose open markers disagree are kept apart. `{x: A} | {x: B, ...}` is not
// `{x: A | B, ...}`, which would admit a record carrying an x drawn from A beside a
// second field, and only the exact member admits an x from A. The pair that would
// fuse is the one whose fields also agree, and joinAtoms answers that one through
// widerByExactness before reaching here.
func (c *Context) joinObjects(a, b *soltype.ObjectType) (soltype.Type, bool) {
	if a.Inexact != b.Inexact {
		return nil, false
	}
	pa, ok := plainProps(a)
	if !ok {
		return nil, false
	}
	pb, ok := plainProps(b)
	if !ok {
		return nil, false
	}
	if !sameFieldNames(pa, pb) {
		return nil, false
	}
	elems := make([]soltype.ObjTypeElem, 0, len(pa))
	widened := 0
	for _, p := range pa {
		q, _ := propNamed(pb, p.Name)
		// Readonly stays a bail, for the reason meetObjects gives.
		if p.Readonly != q.Readonly {
			return nil, false
		}
		if p.Optional == q.Optional && equalType(p.Type, q.Type) {
			elems = append(elems, p)
			continue
		}
		widened++
		if widened > 1 {
			return nil, false
		}
		elems = append(elems, &soltype.PropertyElem{
			Name:     p.Name,
			Type:     c.joinTypes(p.Type, q.Type),
			Optional: p.Optional || q.Optional,
			Readonly: p.Readonly,
		})
	}
	return &soltype.ObjectType{Elems: sortedByName(elems), Inexact: a.Inexact}, true
}

// meetTuples fuses two tuple atoms position by position, or returns `never` when
// the two admit no common length.
//
// # What a tuple atom denotes
//
// A tuple's length set is a floor and a cap, the way an object's key set is. Its
// element list is the floor, and an exact tuple caps the length there while an
// inexact one leaves it uncapped.
//
//	[A, B]     admits length 2 alone
//	[A, ...]   admits every length from 1 up
//
// # The rule
//
// The meet's floor is the longer element list and its cap is the tighter of the
// two caps. A cap below that floor admits no length, so the meet is `never`. Both
// `[A] & [A, B]` and `[A] & [A', B', ...]` are uninhabited for that reason: the
// exact `[A]` caps the length at one, and the other side needs two.
//
// Otherwise every position of the longer list survives. A position both lists name
// narrows to the meet of the two elements. A position only the longer list names is
// unconstrained by the shorter one, whose open tail admits anything there, so the
// longer one's element carries over. `[A, ...] & [A', B', ...]` is
// `[A & A', B', ...]`.
//
// The meet is exact when either side is, since one cap is enough to fix the length.
func (c *Context) meetTuples(a, b *soltype.TupleType) (soltype.Type, bool) {
	if !comparableTuples(a, b) {
		return nil, false
	}
	length := max(len(a.Elems), len(b.Elems))
	if (!a.Inexact && length > len(a.Elems)) || (!b.Inexact && length > len(b.Elems)) {
		return &soltype.NeverType{}, true
	}
	elems := make([]soltype.Type, length)
	for i := range elems {
		switch {
		case i < len(a.Elems) && i < len(b.Elems):
			elems[i] = c.meetTypes(a.Elems[i], b.Elems[i])
		case i < len(a.Elems):
			elems[i] = a.Elems[i]
		default:
			elems[i] = b.Elems[i]
		}
	}
	return &soltype.TupleType{Elems: elems, Inexact: a.Inexact && b.Inexact}, true
}

// joinTuples fuses two tuple atoms under a union. One tuple denotes their union
// only when the two have the same length and differ in at most one position, since
// widening two positions at once would admit a tuple pairing one member's element
// with the other member's.
//
// Tuples of different lengths are kept apart. No single tuple denotes
// `[A, ...] | [A', B', ...]`, since `[A | A', ...]` would admit a one-element
// tuple whose element came from A', which neither member admits. There is one
// case that would fuse, a shared prefix matching position for position, where the
// longer tuple is a subtype of the shorter and the union is the shorter one.
// Recognizing it is a later refinement, not something the union rule needs.
//
// Tuples whose open markers disagree are kept apart too, since their length sets
// differ. The pair that would fuse is the one where the element lists also agree,
// and joinAtoms answers that one through widerByExactness before reaching here.
func (c *Context) joinTuples(a, b *soltype.TupleType) (soltype.Type, bool) {
	if !comparableTuples(a, b) || a.Inexact != b.Inexact || len(a.Elems) != len(b.Elems) {
		return nil, false
	}
	elems := make([]soltype.Type, len(a.Elems))
	widened := 0
	for i := range a.Elems {
		if equalType(a.Elems[i], b.Elems[i]) {
			elems[i] = a.Elems[i]
			continue
		}
		widened++
		if widened > 1 {
			return nil, false
		}
		elems[i] = c.joinTypes(a.Elems[i], b.Elems[i])
	}
	return &soltype.TupleType{Elems: elems, Inexact: a.Inexact}, true
}

// comparableTuples reports whether two tuple atoms can be lined up position for
// position at all. A `...P` spread element rules the pair out, since the element
// list is not yet the tuple's real positions and pairing them up would compare a
// spread against an ordinary element.
//
// Neither the length nor the open marker is checked here, because the meet and the
// join differ on both. Each applies its own rule.
func comparableTuples(a, b *soltype.TupleType) bool {
	return !hasSpreadElem(a.Elems) && !hasSpreadElem(b.Elems)
}

func hasSpreadElem(elems []soltype.Type) bool {
	for _, e := range elems {
		if _, ok := e.(*soltype.RestSpreadType); ok {
			return true
		}
	}
	return false
}

// meetRefs and joinRefs below, and the three helpers they call, split their work by
// sort. The pointees combine in the type algebra through combineRefInners, and the
// two lifetimes combine in the outlives lattice through meetRefLifetimes or
// joinRefLifetimes. Neither sort is consulted about the other, which is the split the
// wrapper already draws: Inner is a Type and Lt is a Lifetime. Both merges keep
// borrows of differing mutability apart, since mut-decay relates those rather than a
// fused wrapper.
//
// Two borrows over one pointee whose lifetimes are distinct variables stay two
// atoms. The fusion ruled out is the one over the JOIN of the two lifetimes, the
// `&'c mut T` a return uniting two `mut` parameters produces. It also admits a
// borrow valid for 'c alone, which neither member admits, so it denotes strictly
// more than the union does. joinBorrows in infer_expr.go may widen to it because it
// is choosing one type for a value at a branch. A fusion here must denote exactly
// what it replaces, and a two-atom list already denotes the union precisely.

// meetRefs fuses two borrow atoms of an intersection. An uninhabited pointee makes
// the whole meet `never`, since nothing is there to borrow.
func (c *Context) meetRefs(a, b *soltype.RefType) (soltype.Type, bool) {
	if a.Mut != b.Mut {
		return nil, false
	}
	lt, ok := meetRefLifetimes(a.Lt, b.Lt)
	if !ok {
		return nil, false
	}
	inner, uninhabited, ok := c.combineRefInners(a, b, meetOfAtoms)
	if !ok {
		return nil, false
	}
	if uninhabited {
		return &soltype.NeverType{}, true
	}
	return soltype.NewRef(a.Mut, lt, inner), true
}

// joinRefs is the dual of meetRefs. It fuses only when the two borrows already agree
// on ONE of the two sorts, because a union does not distribute over the pair of
// them. Fusing `(&'static {x: number}) | (&'a {x: string})` would give
// `&'a {x: number | string}`, which admits an `&'a {x: number}` that neither member
// does.
func (c *Context) joinRefs(a, b *soltype.RefType) (soltype.Type, bool) {
	if a.Mut != b.Mut {
		return nil, false
	}
	if !ltEqual(a.Lt, b.Lt) && !equalType(a.Inner, b.Inner) {
		return nil, false
	}
	lt, ok := joinRefLifetimes(a.Lt, b.Lt)
	if !ok {
		return nil, false
	}
	inner, uninhabited, ok := c.combineRefInners(a, b, joinOfAtoms)
	// An uninhabited join needs both pointees uninhabited, which no atom pair
	// reaches, and fuseAtoms reads a `never` from a join as a failed MEET. Keeping
	// the two borrows apart states the join just as precisely.
	if !ok || uninhabited {
		return nil, false
	}
	return soltype.NewRef(a.Mut, lt, inner), true
}

// combineRefInners combines two borrows' pointees in the type algebra alone. role
// picks the direction, uninhabited reports a `never`, and ok is false when no exact
// combination exists. A MUTABLE borrow's pointee is invariant, so the two must
// already be equal. Widening `{x: number}` and `{x: string}` to `{x: number | string}`
// would let a holder write a string into the cell holding the number.
func (c *Context) combineRefInners(a, b *soltype.RefType, role atomRole) (inner soltype.RefInner, uninhabited, ok bool) {
	if equalType(a.Inner, b.Inner) {
		return a.Inner, false, true
	}
	if a.Mut {
		return nil, false, false
	}
	var combined soltype.Type
	if role == meetOfAtoms {
		combined, ok = c.meetAtoms(a.Inner, b.Inner)
	} else {
		combined, ok = c.joinAtoms(a.Inner, b.Inner)
	}
	if !ok {
		return nil, false, false
	}
	if _, isNever := combined.(*soltype.NeverType); isNever {
		return nil, true, true
	}
	// A combination no borrow may point at has no wrapper to sit in, so the two
	// borrows stay apart. Every merge arm over two borrowable pointees yields a
	// borrowable one or `never`, so this guards the RefInner set against a future
	// arm rather than answering a pair the merges reach today.
	borrowable, isRefInner := combined.(soltype.RefInner)
	if !isRefInner {
		return nil, false, false
	}
	return borrowable, false, true
}

// meetRefLifetimes combines two borrows' lifetimes in the outlives lattice alone.
// The meet is the longer-lived of the two, so 'static absorbs it as the bottom of
// the lattice. A nil slot is an owned cell carrying no lifetime. ok is false where
// no lifetime already names the meet, which keeps the two borrows as separate atoms.
func meetRefLifetimes(a, b soltype.Lifetime) (soltype.Lifetime, bool) {
	if ltEqual(a, b) {
		return a, true
	}
	if a == nil || b == nil {
		return nil, false
	}
	if soltype.IsStaticLifetime(a) || soltype.IsStaticLifetime(b) {
		return soltype.Static, true
	}
	return nil, false
}

// joinRefLifetimes is the dual of meetRefLifetimes, returning the shorter-lived of
// the two. 'static drops OUT of the join rather than absorbing it, since every other
// lifetime outlives it, so `(&'static T) | (&'a T)` is `&'a T`.
func joinRefLifetimes(a, b soltype.Lifetime) (soltype.Lifetime, bool) {
	if ltEqual(a, b) {
		return a, true
	}
	if a == nil || b == nil {
		return nil, false
	}
	if soltype.IsStaticLifetime(a) {
		return b, true
	}
	if soltype.IsStaticLifetime(b) {
		return a, true
	}
	return nil, false
}

// meetFuncs fuses two function atoms into one arrow, but only for the two cases
// where a single arrow denotes the intersection exactly.
//
//   - The domains agree: `(A -> C throws E) & (A -> D throws F)` is
//     `A -> (C & D) throws (E & F)`.
//   - The codomains and the raises agree and there is one parameter:
//     `(A -> C throws E) & (B -> C throws E)` is `(A | B) -> C throws E`.
//
// Any other pair keeps both atoms, which is decision 1 in this file's header and
// where this port departs from MLscript.
//
// The one-parameter restriction is load-bearing. Unioning each position
// independently would fuse `(number, number) -> C` and `(string, string) -> C`
// into a claim that the value accepts a number paired with a string.
//
// The second case demands equal raises where the first meets them. A value
// carrying both arrow types raises, on a given input, only what every arm that
// accepts that input admits. Under agreeing domains every input reaches both arms,
// so the meet of the two clauses is what a call raises. Under differing domains
// only the arm that accepts the input constrains it, so an input in `A` but outside
// `B` raises `E` rather than `E & F`. One clause cannot state both, so the pair is
// kept as two atoms instead.
//
// The fused arrow is inexact when either side is. An arrow's marker says the value
// tolerates every argument count from its arity up. A value satisfying both arrows
// meets both demands, and the open one is the stronger of the two.
func (c *Context) meetFuncs(a, b *soltype.FuncType) (soltype.Type, bool) {
	if !fusableFuncs(a, b) {
		return nil, false
	}
	if equalParamTypes(a, b) {
		return &soltype.FuncType{
			SelfParam:      nil,
			Params:         a.Params,
			Ret:            c.meetTypes(a.Ret, b.Ret),
			Throws:         c.meetThrows(a, b),
			Inexact:        a.Inexact || b.Inexact,
			TypeParams:     nil,
			LifetimeParams: nil,
		}, true
	}
	if len(a.Params) == 1 && equalType(a.Ret, b.Ret) && sameThrows(a, b) {
		param := a.Params[0]
		fused := &soltype.FuncParam{
			Pattern:  param.Pattern,
			Type:     c.joinTypes(param.Type, b.Params[0].Type),
			Optional: param.Optional,
			Rest:     param.Rest,
		}
		return &soltype.FuncType{
			SelfParam:      nil,
			Params:         []*soltype.FuncParam{fused},
			Ret:            a.Ret,
			Throws:         a.Throws,
			Inexact:        a.Inexact || b.Inexact,
			TypeParams:     nil,
			LifetimeParams: nil,
		}, true
	}
	return nil, false
}

// fusableFuncs reports whether two function atoms are alike enough for meetFuncs
// to build one arrow from them. Everything a fused arrow does not recompute must
// already agree: the arity and the per-parameter markers. A receiver or a quantifier
// rules the pair out, since one arrow cannot say which receiver it takes or how the
// two binder lists correspond. The trailing `...` is recomputed rather than required
// to agree, so it is not checked here.
//
// The raises are not checked here. meetFuncs recomputes them in its first case and
// requires them equal in its second, so each case states its own rule.
func fusableFuncs(a, b *soltype.FuncType) bool {
	if a.SelfParam != nil || b.SelfParam != nil {
		return false
	}
	if len(a.TypeParams) > 0 || len(b.TypeParams) > 0 {
		return false
	}
	if len(a.LifetimeParams) > 0 || len(b.LifetimeParams) > 0 {
		return false
	}
	if len(a.Params) != len(b.Params) {
		return false
	}
	for i := range a.Params {
		if a.Params[i].Optional != b.Params[i].Optional || a.Params[i].Rest != b.Params[i].Rest {
			return false
		}
	}
	return true
}

// sameThrows reports whether two arrows raise the same type. The nil shorthand for
// `never` is resolved first, so `fn () -> C` and `fn () -> C throws never` compare
// equal.
func sameThrows(a, b *soltype.FuncType) bool {
	return equalType(a.ThrowsOrNever(), b.ThrowsOrNever())
}

// meetThrows returns what a call to the arrow meetFuncs fuses may raise. The
// clauses meet, the way the codomains do, since `throws` is a covariant output
// position like the return type.
//
// An unwritten clause is kept unwritten when both arrows agree, so fusing two
// non-throwing arrows yields a third whose Throws stays nil rather than an explicit
// `never` the printer would have to suppress.
func (c *Context) meetThrows(a, b *soltype.FuncType) soltype.Type {
	if sameThrows(a, b) {
		return a.Throws
	}
	return c.meetTypes(a.ThrowsOrNever(), b.ThrowsOrNever())
}

func equalParamTypes(a, b *soltype.FuncType) bool {
	for i := range a.Params {
		if !equalType(a.Params[i].Type, b.Params[i].Type) {
			return false
		}
	}
	return true
}

// meetTypes is the meet a structural merge writes into a fused atom, such as the
// codomain of two fused arrows or the type of a field two records share. It
// normalizes the meet rather than building a bare intersection node, so a merge
// produces the same form the surrounding normal form is in. `number` met with
// `string` therefore reaches the fused atom as `never`, not as `number & string`.
//
// The recursion terminates because the operands are proper sub-parts of the two
// atoms being merged, and the kinds that could recur without shrinking — a μ-knot
// and a borrow — are opaque atoms that no merge takes apart. The polarity passed
// is Positive because a shallow normalization reads the same at either one.
func (c *Context) meetTypes(a, b soltype.Type) soltype.Type {
	met := c.mkDNF(newIntersection(nil, []soltype.Type{a, b}), soltype.Positive).toType()
	c.recordFusion(met, a, b)
	return met
}

// joinTypes is the join twin of meetTypes, the one a fused arrow's domain and a
// widened record field are written from.
func (c *Context) joinTypes(a, b soltype.Type) soltype.Type {
	joined := c.mkDNF(newUnion(nil, []soltype.Type{a, b}, false), soltype.Positive).toType()
	c.recordFusion(joined, a, b)
	return joined
}

// plainProps returns an object's members as properties, and ok is false when the
// object carries any other member kind, which keeps that object an unfused atom.
// The two reasons for refusing differ.
//
// A spread or a mapped member makes the object an unreduced residual whose real
// member list is not known yet, so there is nothing to fuse field by field until
// the evaluator settles it. See soltype.HasResidualElem. Refusing those two is
// required, not a choice.
//
// A method, an accessor, and a constructor could fuse under a rule of their own,
// since each carries a FuncType that could meet the way meetFuncs meets an arrow
// atom. No such rule exists, and none is needed: an unfused atom denotes the meet
// or the join just as precisely, so writing one would only shrink the normal form.
// TODO(#1103).
func plainProps(o *soltype.ObjectType) ([]*soltype.PropertyElem, bool) {
	props := make([]*soltype.PropertyElem, 0, len(o.Elems))
	for _, e := range o.Elems {
		p, ok := e.(*soltype.PropertyElem)
		if !ok {
			return nil, false
		}
		props = append(props, p)
	}
	return props, true
}

// sortedByName orders a fused object's members by field name. A fused object is
// the only one this file builds, and its member order would otherwise follow the
// order the two operands happened to reach the merge in, so `{x, ...} & {y, ...}`
// and `{y, ...} & {x, ...}` would render differently. Sorting makes the normal
// form read the same however the merge got there. An object no merge touched
// keeps the member order it was written with, since nothing rebuilds it.
func sortedByName(elems []soltype.ObjTypeElem) []soltype.ObjTypeElem {
	sort.SliceStable(elems, func(i, j int) bool {
		return soltype.ObjElemName(elems[i]) < soltype.ObjElemName(elems[j])
	})
	return elems
}

func propNamed(props []*soltype.PropertyElem, name string) (*soltype.PropertyElem, bool) {
	for _, p := range props {
		if p.Name == name {
			return p, true
		}
	}
	return nil, false
}

// sameFieldNames reports whether two property lists name the same fields. Order is
// irrelevant, since an object's element order is presentation only.
func sameFieldNames(a, b []*soltype.PropertyElem) bool {
	if len(a) != len(b) {
		return false
	}
	for _, p := range a {
		if _, ok := propNamed(b, p.Name); !ok {
			return false
		}
	}
	return true
}

// --- Merging whole conjuncts and disjuncts ---

// canonicalConjuncts fuses the conjuncts of a DNF where fusing is exact, then
// orders and dedups what is left. The result depends on the SET of conjuncts and
// not on the order they arrived in, so two normal forms of one type agree
// member for member. That is what lets a caller compare normal forms directly.
func (c *Context) canonicalConjuncts(conjuncts []Conjunct) []Conjunct {
	fused := make([]Conjunct, 0, len(conjuncts))
	for _, conj := range conjuncts {
		if normalized, ok := c.normalizeConjunct(conj); ok {
			fused = append(fused, normalized)
		}
	}
	merged := mergeAll(fused, c.tryMergeUnion, compareConjunct)
	return dedupSorted(merged, equalConjunct)
}

// normalizeConjunct fuses a conjunct's two pooled structural parts. ok is false
// when the positive part turns out to be uninhabited, which drops the conjunct
// from its DNF.
//
// Each list is cloned first, since fuseAtoms sorts in place and a conjunct that
// came through canonicalization unchanged still shares its slice with the normal
// form it arrived from.
func (c *Context) normalizeConjunct(a Conjunct) (Conjunct, bool) {
	lnf, inhabited := c.fuseAtoms(slices.Clone(a.Lnf.Atoms), meetOfAtoms)
	if !inhabited {
		return Conjunct{}, false
	}
	rnf, _ := c.fuseAtoms(slices.Clone(a.Rnf.Atoms), joinOfAtoms)
	return Conjunct{Lnf: LhsNf{Atoms: lnf}, Vars: a.Vars, Rnf: RhsNf{Atoms: rnf}, NVars: a.NVars}, true
}

// canonicalDisjuncts is the dual of canonicalConjuncts over a CNF.
func (c *Context) canonicalDisjuncts(disjuncts []Disjunct) []Disjunct {
	fused := make([]Disjunct, 0, len(disjuncts))
	for _, disj := range disjuncts {
		if normalized, ok := c.normalizeDisjunct(disj); ok {
			fused = append(fused, normalized)
		}
	}
	merged := mergeAll(fused, c.tryMergeInter, compareDisjunct)
	return dedupSorted(merged, equalDisjunct)
}

// normalizeDisjunct is the dual of normalizeConjunct. ok is false when the
// disjunct's NEGATED part turns out to be uninhabited, since negating `never`
// admits every value, which drops the disjunct from its CNF.
func (c *Context) normalizeDisjunct(a Disjunct) (Disjunct, bool) {
	lnf, inhabited := c.fuseAtoms(slices.Clone(a.Lnf.Atoms), meetOfAtoms)
	if !inhabited {
		return Disjunct{}, false
	}
	rnf, _ := c.fuseAtoms(slices.Clone(a.Rnf.Atoms), joinOfAtoms)
	return Disjunct{Rnf: RhsNf{Atoms: rnf}, Vars: a.Vars, Lnf: LhsNf{Atoms: lnf}, NVars: a.NVars}, true
}

// mergeAll repeatedly fuses pairs of members until no pair fuses, and returns the
// result in canonical order. It sorts once, and mergePass keeps the list sorted
// from there, so which pair fuses is decided by the order the members sort in
// rather than by the order they arrived in — the same reason fuseAtoms sorts
// between fusions. Each fusion shortens the list, so the loop runs at most
// len(items) times.
//
// items is sorted in place, so a caller that still needs the input order must pass
// a copy.
func mergeAll[T any](items []T, try func(a, b T) (T, bool), cmp func(a, b T) int) []T {
	slices.SortStableFunc(items, cmp)
	for {
		fused, changed := mergePass(items, try, cmp)
		if !changed {
			return fused
		}
		items = fused
	}
}

// mergePass fuses the first pair of members that fuses and returns the shortened
// list. It reports whether it fused anything.
//
// The two members it consumed are dropped and the fused one is placed where cmp
// puts it, so a sorted list comes back sorted and mergeAll sorts only once.
//
// It works in place, on the same terms mergeAll states.
func mergePass[T any](items []T, try func(a, b T) (T, bool), cmp func(a, b T) int) ([]T, bool) {
	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			fused, ok := try(items[i], items[j])
			if !ok {
				continue
			}
			// The higher index goes first so the lower one stays valid.
			rest := slices.Delete(slices.Delete(items, j, j+1), i, i+1)
			at, _ := slices.BinarySearchFunc(rest, fused, cmp)
			return slices.Insert(rest, at, fused), true
		}
	}
	return items, false
}

// dedupSorted drops members equal to the one before them under equal. The input
// must already be ordered so that equal members are adjacent.
//
// equal is a structural equality rather than a zero from the comparator that
// ordered the list. compareConjunct bottoms out in compareType, which returns zero
// both for equal types and for two types it has no arm to tell apart, so deduping
// on a zero would delete a distinct conjunct.
func dedupSorted[T any](items []T, equal func(a, b T) bool) []T {
	if len(items) < 2 {
		return items
	}
	out := items[:1]
	for _, item := range items[1:] {
		if !equal(out[len(out)-1], item) {
			out = append(out, item)
		}
	}
	return out
}

// equalConjunct reports whether two conjuncts hold the same four parts.
func equalConjunct(a, b Conjunct) bool {
	return equalAtomLists(a.Lnf.Atoms, b.Lnf.Atoms) &&
		equalAtomLists(a.Rnf.Atoms, b.Rnf.Atoms) &&
		a.Vars.Equals(b.Vars) && a.NVars.Equals(b.NVars)
}

// equalDisjunct is the dual of equalConjunct.
func equalDisjunct(a, b Disjunct) bool {
	return equalAtomLists(a.Lnf.Atoms, b.Lnf.Atoms) &&
		equalAtomLists(a.Rnf.Atoms, b.Rnf.Atoms) &&
		a.Vars.Equals(b.Vars) && a.NVars.Equals(b.NVars)
}

// tryMergeUnion fuses two conjuncts of a DNF into one when the fusion is exact,
// and reports false to keep them separate. Keeping them separate is precise: a
// two-conjunct DNF already denotes their union.
//
// Both variable sets must agree, since a conjunct's variables are opaque and
// nothing can be said about the union of two conjuncts that constrain different
// ones. That leaves the two structural parts, and the fusion works when one of
// them agrees too, so the union reduces to combining the other:
//
//	(L ∩ X) ∪ (L' ∩ X)  is  (L ∪ L') ∩ X          when the negated parts agree
//	(X ∩ ¬R) ∪ (X ∩ ¬R') is  X ∩ ¬(R ∩ R')        when the positive parts agree
//
// So the positive parts combine under a union and the negated ones under an
// intersection, which is the flip De Morgan's law puts on the negated side.
// combineAtoms does the combining and bails when it is not exact.
//
// This is the seam caveat 4 names. `{x: number} | {y: number}` fuses under
// neither rule, since the two records neither absorb one another nor join into a
// single record, so the DNF keeps two conjuncts and stays precise.
func (c *Context) tryMergeUnion(a, b Conjunct) (Conjunct, bool) {
	if !a.Vars.Equals(b.Vars) || !a.NVars.Equals(b.NVars) {
		return Conjunct{}, false
	}
	if equalAtomLists(a.Rnf.Atoms, b.Rnf.Atoms) {
		atoms, ok := c.combineAtoms(a.Lnf.Atoms, b.Lnf.Atoms, meetOfAtoms)
		if !ok {
			return Conjunct{}, false
		}
		return Conjunct{Lnf: LhsNf{Atoms: atoms}, Vars: a.Vars, Rnf: a.Rnf, NVars: a.NVars}, true
	}
	if equalAtomLists(a.Lnf.Atoms, b.Lnf.Atoms) {
		atoms, ok := c.combineAtoms(a.Rnf.Atoms, b.Rnf.Atoms, joinOfAtoms)
		if !ok {
			return Conjunct{}, false
		}
		return Conjunct{Lnf: a.Lnf, Vars: a.Vars, Rnf: RhsNf{Atoms: atoms}, NVars: a.NVars}, true
	}
	return Conjunct{}, false
}

// tryMergeInter fuses two disjuncts of a CNF into one when the fusion is exact,
// the dual of tryMergeUnion. The two disjuncts are combined under an intersection
// this time, so the roles swap:
//
//	(R ∪ X) ∩ (R' ∪ X)   is  (R ∩ R') ∪ X         when the negated parts agree
//	(X ∪ ¬L) ∩ (X ∪ ¬L') is  X ∪ ¬(L ∪ L')        when the positive parts agree
func (c *Context) tryMergeInter(a, b Disjunct) (Disjunct, bool) {
	if !a.Vars.Equals(b.Vars) || !a.NVars.Equals(b.NVars) {
		return Disjunct{}, false
	}
	if equalAtomLists(a.Lnf.Atoms, b.Lnf.Atoms) {
		atoms, ok := c.combineAtoms(a.Rnf.Atoms, b.Rnf.Atoms, joinOfAtoms)
		if !ok {
			return Disjunct{}, false
		}
		return Disjunct{Rnf: RhsNf{Atoms: atoms}, Vars: a.Vars, Lnf: a.Lnf, NVars: a.NVars}, true
	}
	if equalAtomLists(a.Rnf.Atoms, b.Rnf.Atoms) {
		atoms, ok := c.combineAtoms(a.Lnf.Atoms, b.Lnf.Atoms, meetOfAtoms)
		if !ok {
			return Disjunct{}, false
		}
		return Disjunct{Rnf: a.Rnf, Vars: a.Vars, Lnf: LhsNf{Atoms: atoms}, NVars: a.NVars}, true
	}
	return Disjunct{}, false
}

// atomRole says how a list of atoms is read: meetOfAtoms for a list standing for
// the intersection of its atoms, which is what LhsNf holds, and joinOfAtoms for
// one standing for their union, which is what RhsNf holds.
type atomRole int

const (
	meetOfAtoms atomRole = iota
	joinOfAtoms
)

// combineAtoms combines the two atom lists tryMergeUnion or tryMergeInter is left
// with. It combines them in the direction OPPOSITE to how the atoms within one
// list are read: two meetOfAtoms lists are unioned and two joinOfAtoms lists are
// intersected. That is what the two rules in tryMergeUnion's comment ask for, and
// tryMergeInter reaches the same two shapes with the roles swapped. ok is false
// when no exact combination exists, which keeps the conjuncts or disjuncts apart.
//
// Two shapes combine.
//
//  1. One list's atoms are a subset of the other's. Adding an atom to a
//     meetOfAtoms list narrows what it denotes and adding one to a joinOfAtoms
//     list widens it, so in both readings the SUBSET list is the one that already
//     denotes the combination. `A ∪ (A ∩ B)` is A, and `A ∩ (A ∪ B)` is A.
//  2. The lists differ in exactly one atom, and that pair combines exactly. The
//     pair combines in the opposite direction to the lists' own reading, so a
//     meetOfAtoms pair joins and a joinOfAtoms pair meets.
func (c *Context) combineAtoms(a, b []soltype.Type, role atomRole) ([]soltype.Type, bool) {
	if atomsSubset(a, b) {
		return a, true
	}
	if atomsSubset(b, a) {
		return b, true
	}
	if len(a) != len(b) {
		return nil, false
	}
	// Both lists are sorted, so a differing atom shows up as a position where they
	// disagree. More than one such position means the lists differ in more than one
	// atom, which no single combined atom can express.
	diff := -1
	for i := range a {
		if equalType(a[i], b[i]) {
			continue
		}
		if diff >= 0 {
			return nil, false
		}
		diff = i
	}
	if diff < 0 {
		return a, true
	}
	if role == meetOfAtoms {
		fused, ok := c.joinAtoms(a[diff], b[diff])
		if !ok {
			return nil, false
		}
		return replaceAtom(a, diff, fused), true
	}
	fused, ok := c.meetAtoms(a[diff], b[diff])
	if !ok {
		return nil, false
	}
	if _, isNever := fused.(*soltype.NeverType); isNever {
		// The combined atom is uninhabited, and `never` is the identity of the union
		// the atom sits in, so the position drops rather than being replaced. This is
		// what collapses `¬number | ¬string` to `unknown`: the two conjuncts agree on
		// their positive parts, their negated parts meet to `never`, and a conjunct
		// negating `never` admits every value.
		return withoutIndex(a, diff), true
	}
	return replaceAtom(a, diff, fused), true
}

// replaceAtom returns a copy of atoms with position i replaced and the result
// reordered, since the combined atom may sort elsewhere than the one it replaced.
func replaceAtom(atoms []soltype.Type, i int, atom soltype.Type) []soltype.Type {
	out := append([]soltype.Type(nil), atoms...)
	out[i] = atom
	sortAtoms(out)
	return out
}

func atomsSubset(a, b []soltype.Type) bool {
	for _, x := range a {
		found := false
		for _, y := range b {
			if equalType(x, y) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// --- Canonical ordering ---

// compareConjunct is the total order canonical conjunct order is built on. It
// compares the four parts in the order they read, so two conjuncts holding the
// same parts compare equal whatever route built them.
func compareConjunct(a, b Conjunct) int {
	if c := compareAtoms(a.Lnf.Atoms, b.Lnf.Atoms); c != 0 {
		return c
	}
	if c := compareVars(a.Vars, b.Vars); c != 0 {
		return c
	}
	if c := compareAtoms(a.Rnf.Atoms, b.Rnf.Atoms); c != 0 {
		return c
	}
	return compareVars(a.NVars, b.NVars)
}

// compareDisjunct is the dual of compareConjunct.
func compareDisjunct(a, b Disjunct) int {
	if c := compareAtoms(a.Rnf.Atoms, b.Rnf.Atoms); c != 0 {
		return c
	}
	if c := compareVars(a.Vars, b.Vars); c != 0 {
		return c
	}
	if c := compareAtoms(a.Lnf.Atoms, b.Lnf.Atoms); c != 0 {
		return c
	}
	return compareVars(a.NVars, b.NVars)
}

// compareAtoms orders two atom lists by length, then position for position under
// compareAtom. Both lists are kept sorted by the same order, so two lists holding
// the same atoms compare equal.
func compareAtoms(a, b []soltype.Type) int {
	if c := len(a) - len(b); c != 0 {
		return c
	}
	for i := range a {
		if c := compareAtom(a[i], b[i]); c != 0 {
			return c
		}
	}
	return 0
}

// compareVars orders two variable sets by size, then by their ids. Iterating a
// set.Set yields no order, so both sides are sorted by id first.
func compareVars(a, b set.Set[*soltype.TypeVarType]) int {
	as, bs := sortedVars(a), sortedVars(b)
	if c := len(as) - len(bs); c != 0 {
		return c
	}
	for i := range as {
		if c := as[i].ID - bs[i].ID; c != 0 {
			return c
		}
	}
	return 0
}

// sortedVars returns a variable set's members ordered by id, so rendering and
// comparison read them in one order.
func sortedVars(vars set.Set[*soltype.TypeVarType]) []*soltype.TypeVarType {
	out := vars.ToSlice()
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// --- Back to a surface type ---

// toType renders a DNF as the union its conjuncts denote. An empty DNF is `never`.
// The result is an ordinary soltype.Type: the normal form itself never leaves the
// solver.
func (d DNF) toType() soltype.Type {
	parts := make([]soltype.Type, len(d.Conjuncts))
	for i, conj := range d.Conjuncts {
		parts[i] = conj.toType()
	}
	return newUnion(nil, parts, false)
}

// toType renders a CNF as the intersection its disjuncts denote. An empty CNF is
// `unknown`.
func (n CNF) toType() soltype.Type {
	parts := make([]soltype.Type, len(n.Disjuncts))
	for i, disj := range n.Disjuncts {
		parts[i] = disj.toType()
	}
	return newIntersection(nil, parts)
}

// toType renders a conjunct as `Lnf ∩ (⋂Vars) ∩ ¬Rnf ∩ (⋂¬NVars)`. An empty part
// contributes nothing: an empty Lnf is `unknown` and an empty Rnf makes the
// negated part `¬never`, and both are the identity of the intersection.
func (a Conjunct) toType() soltype.Type {
	parts := make([]soltype.Type, 0, len(a.Lnf.Atoms)+a.Vars.Len()+1+a.NVars.Len())
	parts = append(parts, a.Lnf.Atoms...)
	for _, v := range sortedVars(a.Vars) {
		parts = append(parts, v)
	}
	if len(a.Rnf.Atoms) > 0 {
		parts = append(parts, negate(a.Rnf.toType()))
	}
	for _, v := range sortedVars(a.NVars) {
		parts = append(parts, negate(v))
	}
	return newIntersection(nil, parts)
}

// toType renders a disjunct as `Rnf ∪ (⋃Vars) ∪ ¬Lnf ∪ (⋃¬NVars)`, the dual of
// Conjunct.toType. An empty Lnf makes the negated part `¬unknown`, which is
// `never`, the identity of the union.
func (a Disjunct) toType() soltype.Type {
	parts := make([]soltype.Type, 0, len(a.Rnf.Atoms)+a.Vars.Len()+1+a.NVars.Len())
	parts = append(parts, a.Rnf.Atoms...)
	for _, v := range sortedVars(a.Vars) {
		parts = append(parts, v)
	}
	if len(a.Lnf.Atoms) > 0 {
		parts = append(parts, negate(a.Lnf.toType()))
	}
	for _, v := range sortedVars(a.NVars) {
		parts = append(parts, negate(v))
	}
	return newUnion(nil, parts, false)
}

// toType renders an intersection of atoms. An empty list is `unknown`.
func (l LhsNf) toType() soltype.Type {
	return newIntersection(nil, l.Atoms)
}

// toType renders a union of atoms. An empty list is `never`.
func (r RhsNf) toType() soltype.Type {
	return newUnion(nil, r.Atoms, false)
}

// negate wraps a rendered part in a complement. No simplification is needed at
// the two sites that call it. The part is a non-empty atom list or a type
// variable, so it is never a lattice bound whose complement is the other bound,
// and mkDNF decomposes every negation it meets, so no atom is itself a complement
// that a second one would cancel.
//
// It goes through soltype.NewNegation, which enforces the ¬Ref exclusion
// invariant. Nothing is expected to trip that check here, since the parts rendered
// below come from a normal form whose negated side mkDNF already scanned for a
// borrow. It is the construction-site gate, applied wherever a complement is
// built.
func negate(t soltype.Type) soltype.Type {
	return soltype.NewNegation(t)
}
