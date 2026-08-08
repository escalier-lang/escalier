package ucs

import "github.com/escalier-lang/escalier/internal/set"

// Normalize rewrites a desugared core term into the normalized form. Three rewrites
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
// The third is flattening. A core branch keeps its arm's pattern whole, nesting and all.
// Normalization reads one tag-level off it and turns each sub-pattern below that level
// into a split over the projection it matches, recursing to whatever depth the pattern
// has. `Line { start: {x, y} }` becomes a split on `l` testing `Line`, then a split on
// `l.start` testing `{x, y}`, then binds of `l.start.x` and `l.start.y`. A consumer
// therefore never sees more than one tag-level at a time. A projected split fails the
// same way the branch's guard does, into the branch's fallthrough, so flattening adds no
// backtracking.
//
// When two branches nest under one tag, their projected splits chain rather than merging
// into a single split. In `{a: {x}} => …, {a: [y]} => …` the first branch's `{a}` already
// proves the second's, so the first falls into a second split over `p.a` instead of both
// sub-patterns becoming branches of one. Neither split is specialized against the other
// either, so a chain can re-test a tag it has already decided. `{x: 1} if g => a,
// {x: 1} => b` tests `p.x` against 1 a second time when the guard fails. Specializing
// across tag-levels is a rewrite this stage does not make.
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

	reads := projections{}
	cands := make([]candidate, len(s.Branches))
	for i, branch := range s.Branches {
		test, binds := shallowTest(branch.Pattern, s.Scrutinee, branch.Origin)
		cands[i] = candidate{
			index:  i,
			branch: branch,
			test:   test,
			binds:  binds,
			nested: reads.hasSplit(binds),
		}
	}

	b := &splitBuilder{
		scrutinee: s.Scrutinee,
		tail:      tail,
		origin:    s.Origin,
		built:     map[string]Norm{},
		inlined:   set.NewSet[int](),
		reads:     reads,
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
// analyzed a single time. The projections memo does the same for the sub-patterns below
// that tag, which are read as each copy of the branch is built.
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
	// nested marks a branch whose pattern nests below the tag-level its test names. In
	// `{x: 1}` the test names the key `x` and a split over `p.x` matches the literal
	// `1`, so passing the test does not mean the branch matched. Such a branch needs a
	// fallthrough, since a value that reaches it can still end up in the arms below.
	nested bool
}

// splitBuilder builds the branches of one core split. Everything it holds is fixed for
// that split: the scrutinee every branch tests, the tail control reaches when no branch
// covers the value, and the split's own provenance.
type splitBuilder struct {
	scrutinee *Scrutinee
	tail      Norm
	origin    Origin
	// built caches the fallthrough term for a set of candidates. A branch that can fail
	// falls into the branches after it, so without the cache a run of such branches
	// would rebuild overlapping subsets over and over. Each cached term is also shared
	// rather than duplicated, so a consumer that walks the IR visits it once.
	//
	// A branch's own continuation is not shared with the copy of it inside an earlier
	// branch's fallthrough, because the two are reached under different specializations
	// and need not be the same term. In `{a: 1} => …, {b: 2} => …, _ => …` the second
	// arm is built once for its branch of the split and once inside the first arm's
	// fallthrough, so a run of arms that can fail costs nodes with the square of its
	// length. A run of arms testing one tag does not pay it, since their tests clear and
	// their branches drop. A match short enough for a person to read keeps the squared
	// count small either way.
	built map[string]Norm
	// reads memoizes the tag-level read of each sub-pattern below the branches' own
	// tags, so the copies this rewrite makes of a branch name one *Scrutinee per path
	// rather than one per copy.
	reads projections
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
		// Only a branch that can fail needs to name where it continues. An unguarded arm
		// whose pattern nests no further ends in a leaf, so it never falls through and
		// the fallthrough would be dead weight. Nesting is the other way a branch fails:
		// the tag test passes and a split over a projection below it does not match.
		var fallthru Norm
		if cand.nested || mayFall(cand.branch.Cont) {
			fallthru = b.term(specialize(cands[i+1:], cand.test))
		}
		nest := binder{fallthru: fallthru, arm: cand.branch.Arm, reads: b.reads}
		cont := nest.wrap(cand.binds, normalizeTerm(cand.branch.Cont, fallthru))
		if cand.test == nil {
			// The branch makes no test, so control reaches it whenever it reaches the
			// split at all and it becomes the split's tail. A candidate after it is
			// reachable only through this one's fallthrough, which is where a failed
			// guard and an unmatched sub-pattern both continue. Recording it is what lets
			// an outer branch for the same candidate be dropped, since this term already
			// runs its continuation.
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
