package solver

import (
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
// # A list of atoms per side, not one slot per kind
//
// MLscript gives LhsNf and RhsNf one slot per structural kind, which loses
// precision in two places — see planning/ml_struct/06-open-items.md findings 1 and
// 2. This port holds a LIST per side instead, so neither loss is inherited.
//
// Finding 1 pays off immediately. A supertype union of two differently-named
// records stays precise here, where MLscript widens the disjunct to `unknown` on
// meeting the second field name and makes every subtype pass against it.
//
// # What a later PR adds
//
// Atoms accumulate in their lists. Two equal atoms dedup, and two atoms of one
// kind that are not equal are both kept. Nothing is merged structurally yet, so
// `number ∩ string` stays two atoms rather than collapsing to `never`, and two
// inexact records stay two atoms rather than merging field-wise. The merge table
// and the rule for fusing whole conjuncts land next, and finding 2 — keeping
// intersected arrows apart rather than fusing them unsoundly — is settled there.

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
// sortAtoms and holds no two equal atoms. An empty Atoms is `unknown`.
type LhsNf struct{ Atoms []soltype.Type }

// RhsNf is a normalized UNION of structural atoms, the dual of LhsNf. It is the
// positive part of a Disjunct and the negated part of a Conjunct. An empty Atoms
// is `never`.
type RhsNf struct{ Atoms []soltype.Type }

// Base returns the single nominal class tag the intersection carries, and reports
// whether it carries exactly one. This is the slot the nominal meet will write to:
// once PR4 (#1061) supplies it, two related tags fuse into one and two unrelated
// ones make the conjunct `never`, so a well-formed conjunct holds at most one tag
// and this accessor is total on it.
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
// its children untouched.
//
// pol is the position t occupies. A negation flips it before normalizing its
// operand, so the polarity stays accurate all the way down, but no arm below reads
// it for anything else. The shallow form is therefore the same at either polarity.
// pol is threaded so that a later pass can normalize an atom's children at the
// polarity they really occupy, which is where the parameters of a function and the
// operand of a negation part company.
func (c *Context) mkDNF(t soltype.Type, pol soltype.Polarity) DNF {
	switch t := t.(type) {
	case *soltype.NeverType:
		return DNF{Conjuncts: nil}
	case *soltype.UnknownType:
		return dnfTop()
	case *soltype.UnionType:
		if t.Inexact {
			// `A | B | ...` names A, B, and an open tail of unknown content. The tail has
			// no atom to stand for it, so taking the union apart would drop it and hand
			// back a type narrower than the source wrote. The whole node stays one atom,
			// which round-trips exactly and keeps the flag. PR7 (#1064) decides how far
			// an inexact union can be decomposed.
			return dnfAtom(t)
		}
		out := DNF{Conjuncts: nil}
		for _, m := range t.Types {
			out = dnfOr(out, c.mkDNF(m, pol))
		}
		return DNF{Conjuncts: canonicalConjuncts(out.Conjuncts)}
	case *soltype.IntersectionType:
		out := dnfTop()
		for _, m := range t.Types {
			out = dnfAnd(out, c.mkDNF(m, pol))
		}
		return DNF{Conjuncts: canonicalConjuncts(out.Conjuncts)}
	case *soltype.NegationType:
		// ¬T at pol is the complement of T at the flipped polarity, and the complement
		// of an intersection of joins is a union of meets. So normalize the operand to a
		// CNF and negate each disjunct into a conjunct. Negating permutes fields without
		// consulting the merges, so the result is canonicalized afterwards.
		negated := c.mkCNF(t.Inner, pol.Flip()).neg()
		return DNF{Conjuncts: canonicalConjuncts(negated.Conjuncts)}
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
		return CNF{Disjuncts: canonicalDisjuncts(out.Disjuncts)}
	case *soltype.UnionType:
		if t.Inexact {
			// The open tail has no atom to stand for it; see the mkDNF arm.
			return cnfAtom(t)
		}
		out := cnfBot()
		for _, m := range t.Types {
			out = cnfOr(out, c.mkCNF(m, pol))
		}
		return CNF{Disjuncts: canonicalDisjuncts(out.Disjuncts)}
	case *soltype.NegationType:
		negated := c.mkDNF(t.Inner, pol.Flip()).neg()
		return CNF{Disjuncts: canonicalDisjuncts(negated.Disjuncts)}
	case *soltype.TypeVarType:
		return CNF{Disjuncts: []Disjunct{newDisjunct().withVar(t)}}
	default:
		return cnfAtom(t)
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
// Nothing is ordered here, and neither does dnfAnd below. The mkDNF arms pool
// every member first and call canonicalConjuncts once over the whole pool, so
// ordering and deduping see the complete list. Pooling is associative, so folding
// these binary operations left to right reaches the same pool as any other
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
// held both positively and negatively, which makes the meet `v ∩ ¬v`.
//
// The pooled structural parts are put in order later, by normalizeConjunct, once
// every atom in the surrounding union or intersection is in the pool.
func conjunctAnd(a, b Conjunct) (Conjunct, bool) {
	vars := a.Vars.Union(b.Vars)
	nvars := a.NVars.Union(b.NVars)
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
// variable is held both positively and negatively, since `v ∪ ¬v` admits every
// value and `unknown` is the identity of the intersection the disjuncts sit in.
func disjunctOr(a, b Disjunct) (Disjunct, bool) {
	vars := a.Vars.Union(b.Vars)
	nvars := a.NVars.Union(b.NVars)
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

// pooled concatenates two atom lists into a fresh slice, so ordering the result
// leaves both inputs alone. Two normal forms often share an atom list, since a
// conjunct that survived canonicalization unchanged keeps the slice it arrived
// with.
func pooled(a, b []soltype.Type) []soltype.Type {
	out := make([]soltype.Type, 0, len(a)+len(b))
	out = append(out, a...)
	out = append(out, b...)
	return out
}

// copyAtoms is pooled over a single list, for the same reason.
func copyAtoms(atoms []soltype.Type) []soltype.Type {
	return append([]soltype.Type(nil), atoms...)
}

// dedupAtoms orders an atom list canonically and drops the duplicates that pooling
// two lists can produce, so `number ∩ number` holds one atom rather than two.
//
// Dropping a duplicate is the only combining an atom list does at this stage. Two
// atoms of one kind that are not equal are both kept, which costs nothing: a
// two-atom list is already the precise meet or join of its atoms.
func dedupAtoms(atoms []soltype.Type) []soltype.Type {
	sortAtoms(atoms)
	if len(atoms) < 2 {
		return atoms
	}
	out := atoms[:1]
	for _, atom := range atoms[1:] {
		if !equalType(out[len(out)-1], atom) {
			out = append(out, atom)
		}
	}
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
// positions. Both are sorted, so this is the equality equalConjunct tests.
//
// It compares with equalType rather than by checking compareAtoms for a zero.
// compareType returns zero both for equal types and for two types it has no
// structural arm to tell apart, such as two different class tags, so deduping on a
// zero would treat a conjunct negating `Point` and one negating `Line` as the same
// conjunct and delete one of them.
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

// --- Canonicalizing the conjunct and disjunct lists ---

// canonicalConjuncts orders and dedups the conjuncts of a DNF. The result depends
// on the SET of conjuncts and not on the order they arrived in, so two normal
// forms of one type agree member for member. That is what lets a caller compare
// normal forms directly.
func canonicalConjuncts(conjuncts []Conjunct) []Conjunct {
	normalized := make([]Conjunct, 0, len(conjuncts))
	for _, conj := range conjuncts {
		normalized = append(normalized, normalizeConjunct(conj))
	}
	sortConjuncts(normalized)
	return dedupSorted(normalized, equalConjunct)
}

// normalizeConjunct puts a conjunct's two pooled structural parts in order.
func normalizeConjunct(a Conjunct) Conjunct {
	return Conjunct{
		Lnf:   LhsNf{Atoms: dedupAtoms(copyAtoms(a.Lnf.Atoms))},
		Vars:  a.Vars,
		Rnf:   RhsNf{Atoms: dedupAtoms(copyAtoms(a.Rnf.Atoms))},
		NVars: a.NVars,
	}
}

// canonicalDisjuncts is the dual of canonicalConjuncts over a CNF.
func canonicalDisjuncts(disjuncts []Disjunct) []Disjunct {
	normalized := make([]Disjunct, 0, len(disjuncts))
	for _, disj := range disjuncts {
		normalized = append(normalized, normalizeDisjunct(disj))
	}
	sortDisjuncts(normalized)
	return dedupSorted(normalized, equalDisjunct)
}

// normalizeDisjunct is the dual of normalizeConjunct.
func normalizeDisjunct(a Disjunct) Disjunct {
	return Disjunct{
		Rnf:   RhsNf{Atoms: dedupAtoms(copyAtoms(a.Rnf.Atoms))},
		Vars:  a.Vars,
		Lnf:   LhsNf{Atoms: dedupAtoms(copyAtoms(a.Lnf.Atoms))},
		NVars: a.NVars,
	}
}

func sortConjuncts(items []Conjunct) {
	sort.SliceStable(items, func(i, j int) bool { return compareConjunct(items[i], items[j]) < 0 })
}

func sortDisjuncts(items []Disjunct) {
	sort.SliceStable(items, func(i, j int) bool { return compareDisjunct(items[i], items[j]) < 0 })
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
func negate(t soltype.Type) soltype.Type {
	return &soltype.NegationType{Inner: t}
}
