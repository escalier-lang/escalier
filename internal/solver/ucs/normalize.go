package ucs

import "github.com/escalier-lang/escalier/internal/set"

// Normalize rewrites a desugared core term into the normalized form. Two rewrites
// happen here.
//
// The first is merging. Every branch of one core split tests the same scrutinee, so
// all of them become branches of a single normalized split rather than a chain of
// one-branch splits. A consumer then visits each scrutinee once.
//
// The second is the tail. A core branch relies on the branches after it to say where
// a failed match continues, which is backtracking. Normalization names that
// continuation outright: a split's Default is what runs when no branch's test matches,
// and a guard's Default is what runs when its condition is false. Nothing retries a
// branch above it.
//
// A nested sub-pattern is left whole. Its branch keeps it as a NormBind with no name,
// which says "still to be matched against this projection". Flattening those into splits
// of their own is the next stage of the rewrite, the one the nested-pattern section of
// planning/ucs/implementation_plan.md describes. So the form is backtracking-free for a
// flat match and not yet for a nested one. A nameless bind names no continuation for the
// case where its sub-pattern does not match, and there is nowhere to put one. The split
// that flattening puts in its place is what carries that default.
func Normalize(c Core) Norm {
	return normalizeTerm(c, nil)
}

// normalizeTerm rewrites one core term. next is where control goes when the term fails
// to reach a leaf, which is what a guard falls back to. It is nil when nothing covers
// the failure, and the printer renders that as `✗`. A leaf cannot fail, so it ignores
// next and is returned as-is: normalization rewrites the splits around a leaf and
// leaves the leaf itself alone, which is why the same node ends up in both IRs.
func normalizeTerm(c Core, next Norm) Norm {
	switch n := c.(type) {
	case nil:
		return nil
	case *CoreSplit:
		return normalizeSplit(n, next)
	case *CoreGuard:
		return &NormGuard{
			Cond:    n.Cond,
			Cont:    normalizeTerm(n.Cont, next),
			Default: next,
			Origin:  n.Origin,
		}
	case *CoreBind:
		return &NormBind{
			Name:   n.Name,
			Pat:    n.Pat,
			Source: n.Source,
			Cont:   normalizeTerm(n.Cont, next),
			Origin: n.Origin,
		}
	case *BodyLeaf, *EscapeLeaf, *FallbackLeaf:
		return n.(Norm)
	default:
		return nil
	}
}

// normalizeSplit rewrites a core split into a normalized one. The split's own
// fallthrough is its `else` when it wrote one, which an `if val` and a `val … else`
// always do, and otherwise the continuation the enclosing term handed down.
func normalizeSplit(s *CoreSplit, next Norm) Norm {
	tail := next
	if s.Else != nil {
		// next is not dropped here. It becomes the `else`'s own failure continuation. A
		// leaf `else` cannot fail and ignores it, which is what an `if val` and a
		// `val … else` both write. A guard or a nested split `else` falls into it.
		tail = normalizeTerm(s.Else, next)
	}

	cands := make([]candidate, len(s.Branches))
	for i, branch := range s.Branches {
		test, binds := shallowTest(branch.Pattern, s.Scrutinee, branch.Origin)
		cands[i] = candidate{
			index:  i,
			branch: branch,
			test:   test,
			binds:  binds,
			nested: hasUnflattenedBind(binds),
		}
	}

	b := &splitBuilder{
		scrutinee: s.Scrutinee,
		tail:      tail,
		origin:    s.Origin,
		built:     map[string]Norm{},
		inlined:   set.NewSet[int](),
	}
	// A core split always becomes a split, even when no branch is left to test, because
	// the split is what names the scrutinee. Collapsing `match f() { _ => 1 }` to its
	// leaf would drop the only mention of `f()`, and a consumer walking the IR would
	// never evaluate the call.
	branches, dflt := b.build(cands)
	return &NormSplit{
		Scrutinee: s.Scrutinee,
		Branches:  branches,
		Default:   dflt,
		Origin:    s.Origin,
	}
}

// candidate is a core branch paired with what normalization derived from its pattern:
// the one tag-level test it makes and the leaves it binds. Deriving them once means a
// branch that appears both in the split and in an earlier branch's fallthrough is
// analyzed a single time.
type candidate struct {
	// index is the branch's position in the core split, which identifies it while
	// building. Two candidate lists holding the same branches build the same term.
	index  int
	branch *CoreBranch
	// test is the tag the branch tests, and nil when the branch runs
	// unconditionally. A catch-all pattern has no tag, and specialize also clears the
	// test of a branch an already-matched test guarantees.
	test  Test
	binds []bindSpec
	// nested marks a branch holding a sub-pattern this stage does not flatten, which is
	// matching the tag test does not account for. In `{x: 1}` the test names the key
	// `x` and the literal `1` rides a nameless bind, so passing the test does not mean
	// the branch matched. Such a branch is never made unconditional: a value that
	// reaches it can still fall through to the arms below.
	nested bool
}

// splitBuilder builds the branches of one core split. Everything it holds is fixed for
// that split: the scrutinee every branch tests, the tail control reaches when no branch
// covers the value, and the split's own provenance.
type splitBuilder struct {
	scrutinee *Scrutinee
	tail      Norm
	origin    Origin
	// built caches the fallthrough term for a set of candidates. A guarded branch falls
	// into the branches after it, so without the cache a run of guarded branches would
	// rebuild overlapping subsets over and over. Each cached term is also shared rather
	// than duplicated, so a consumer that walks the IR visits it once.
	built map[string]Norm
	// inlined holds the index of every candidate whose continuation an already-built
	// term runs unconditionally, which is what specializing a fallthrough does to a
	// branch the matched test proved. A branch for one of those is a duplicate this
	// rewrite introduced, and build drops it rather than emit a test that cannot pass.
	inlined set.Set[int]
}

// term is what a branch continues into when its own continuation fails. Unlike the
// split the whole core split becomes, it collapses to the unconditional continuation
// when no candidate is left to test. The scrutinee the collapsed split would have named
// is already evaluated by the split this term sits inside, so nothing is lost.
func (b *splitBuilder) term(cands []candidate) Norm {
	if len(cands) == 0 {
		return b.tail
	}
	key := candidatesKey(cands)
	if term, ok := b.built[key]; ok {
		return term
	}

	branches, dflt := b.build(cands)
	var term Norm = dflt
	if len(branches) > 0 {
		term = &NormSplit{
			Scrutinee: b.scrutinee,
			Branches:  branches,
			Default:   dflt,
			Origin:    b.origin,
		}
	}
	b.built[key] = term
	return term
}

// build turns a list of candidates into the branches of one split and the default tail
// control reaches when none of them matches.
func (b *splitBuilder) build(cands []candidate) ([]*NormBranch, Norm) {
	var branches []*NormBranch
	dflt := b.tail
	for i, cand := range cands {
		if cand.test != nil && b.inlined.Contains(cand.index) && capturedBy(cands[:i], cand.test) {
			// An earlier branch's fallthrough already runs this branch's continuation,
			// and an earlier test already captured every value this one would match, so
			// nothing can reach the branch. Emitting it would cost a test that always
			// fails and a second copy of the arm's binds and body.
			//
			// capturedBy is what makes the drop sound rather than a guess. It is not
			// separately observable while testImplies holds only between equal tags,
			// since an inlined candidate is then always captured too, and it is what
			// keeps the drop correct if that relation ever becomes one-directional.
			//
			// A branch nothing inlined is left alone even when it is unreachable. That
			// is an arm the user wrote dead rather than one this rewrite duplicated, and
			// dropping it would leave the coverage check nothing to report.
			continue
		}
		// Only a branch whose continuation can fail needs to name where it continues.
		// An unguarded arm ends in a leaf, so it never falls through and the
		// fallthrough would be dead weight.
		var fallthru Norm
		if mayFall(cand.branch.Cont) {
			fallthru = b.term(specialize(cands[i+1:], cand.test))
		}
		cont := wrapBinds(cand.binds, normalizeTerm(cand.branch.Cont, fallthru))
		if cand.test == nil {
			// The branch always runs, so it is the split's tail and every candidate
			// after it is unreachable. Recording it is what lets an outer branch for the
			// same candidate be dropped, since this term already runs its continuation.
			dflt = cont
			b.inlined.Add(cand.index)
			break
		}
		branches = append(branches, &NormBranch{
			Test:   cand.test,
			Cont:   cont,
			Arm:    cand.branch.Arm,
			Origin: cand.branch.Origin,
		})
	}
	return branches, dflt
}

// mayFall reports whether a core term can finish without reaching a leaf, so the branch
// holding it needs a fallthrough. A guard fails when its condition is false. A nested
// split may match none of its branches. A leaf always produces a value.
//
// Answering true when the term in fact always reaches a leaf costs a fallthrough that
// nothing reads, so the conservative answer for a nested split is safe.
func mayFall(c Core) bool {
	switch n := c.(type) {
	case *CoreGuard:
		return true
	case *CoreSplit:
		return true
	case *CoreBind:
		return mayFall(n.Cont)
	default:
		return false
	}
}
