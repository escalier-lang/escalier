package ucs

import (
	"strconv"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
)

// specialize returns the candidates that can still run once the matched test has
// succeeded, in source order. This is what a branch whose guard fails continues into,
// and dropping what cannot run is what keeps the continuation from re-testing a tag the
// value is already known to fail.
//
// Three things can happen to a later candidate. A test the matched one guarantees is
// cleared, since a value that passed the first passes the second too. A test that cannot
// hold of the same value is dropped. Anything else survives with its test intact and is
// re-tested.
//
// Clearing a test says the tag needs no re-testing, not that the candidate's branch
// matched. A candidate with no test still fails when its guard's condition is false or
// when a split over a projection below its tag matches nothing, and both continue into
// the candidates after it. `1 if f() => a, 1 if g() => b, _ => c` reaches `c` when both
// guards fail, and `{x: 1} => a, {x: 2} => b, _ => c` reaches `c` when `p.x` is neither.
// Truncating the branch list at a cleared candidate is build's job, and it truncates only
// the branches, not what that candidate falls into.
//
// A candidate that made no test taught nothing, so every later candidate survives
// unchanged.
func specialize(cands []candidate, matched Test) []candidate {
	if matched == nil {
		return cands
	}
	out := make([]candidate, 0, len(cands))
	for _, cand := range cands {
		switch {
		case cand.test == nil:
			out = append(out, cand)
		case testImplies(matched, cand.test):
			cand.test = nil
			out = append(out, cand)
		case testsDisjoint(matched, cand.test):
			// Nothing to do: no value passes both tests, so this candidate cannot run.
		default:
			out = append(out, cand)
		}
	}
	return out
}

// capturedBy reports whether one of the earlier candidates already takes every value
// test would match, which means control cannot reach the branch test belongs to. Those
// candidates are the branches ahead of it in the same split, and reaching it means each
// of their tests failed. A test the failed one takes every value of fails too.
//
// This is the opposite direction from the implication specialize uses. There the
// question is what an already-matched test proves about a later one, so the matched test
// is the narrower one. Here the later test has to be the narrower one for its branch to
// be dead.
func capturedBy(earlier []candidate, test Test) bool {
	for _, cand := range earlier {
		if cand.test != nil && testImplies(test, cand.test) {
			return true
		}
	}
	return false
}

// candidatesKey identifies a candidate list by the branches in it and whether each
// still makes its test, which is everything build reads. A `!` marks a candidate
// specialize made unconditional, so it does not key the same as the branch with its
// test intact. Specializing can drop a candidate from the middle of a run, so the key
// names every candidate rather than only the first.
func candidatesKey(cands []candidate) string {
	var sb strings.Builder
	for _, cand := range cands {
		sb.WriteString(strconv.Itoa(cand.index))
		if cand.test == nil {
			sb.WriteByte('!')
		}
		sb.WriteByte(',')
	}
	return sb.String()
}

// testImplies reports whether every value passing a also passes b, which lets a branch
// that already matched a run b's branch without re-testing.
//
// It answers true only where the two tests hold of the same values under any reading of
// what a structural test accepts. Whether an object test rejects a value carrying extra
// fields is left to the consumer, as the RestKind doc explains, so two object tests
// that name different keys are not compared. Answering false only costs a re-test in
// the normalized form.
func testImplies(a, b Test) bool {
	switch a := a.(type) {
	case *LitTest:
		b, ok := b.(*LitTest)
		return ok && litEqual(a.Lit, b.Lit)
	case *ObjectTest:
		b, ok := b.(*ObjectTest)
		return ok && a.Rest == b.Rest && sameKeys(a.Keys, b.Keys)
	case *TupleTest:
		b, ok := b.(*TupleTest)
		return ok && a.Len == b.Len && a.Rest == b.Rest
	case *ClassTest:
		b, ok := b.(*ClassTest)
		return ok && ast.QualIdentToString(a.Name) == ast.QualIdentToString(b.Name)
	case *ExtractorTest:
		// Two runs of one extractor are taken to agree, the same assumption a branch
		// order relies on when the surface repeats a pattern.
		b, ok := b.(*ExtractorTest)
		return ok && a.Arity == b.Arity &&
			ast.QualIdentToString(a.Name) == ast.QualIdentToString(b.Name)
	default:
		return false
	}
}

// testsDisjoint reports whether no value passes both tests, which lets a branch that
// already matched drop the other branch from what it falls through to.
//
// Only two different literals qualify. Every other pair could hold of one value: a
// subclass passes both its own class test and its parent's, an extractor is free to
// match a value of any shape, and two structural tests overlap whenever one shape
// extends the other. Answering false only keeps a branch that cannot run.
func testsDisjoint(a, b Test) bool {
	litA, aIsLit := a.(*LitTest)
	litB, bIsLit := b.(*LitTest)
	return aIsLit && bIsLit && !litEqual(litA.Lit, litB.Lit)
}

// sameKeys reports whether two object tests name the same fields with the same
// optionality. Order does not matter, since `{x, y}` and `{y, x}` accept the same
// values.
func sameKeys(a, b []ObjectKey) bool {
	if len(a) != len(b) {
		return false
	}
	optional := make(map[string]bool, len(a))
	for _, key := range a {
		optional[key.Name] = key.Optional
	}
	for _, key := range b {
		was, found := optional[key.Name]
		if !found || was != key.Optional {
			return false
		}
	}
	return true
}

// litEqual reports whether two literals name the same value. A literal test compares
// values rather than nodes, so the `1` of one arm and the `1` of another are the same
// tag.
func litEqual(a, b ast.Lit) bool {
	switch a := a.(type) {
	case *ast.BoolLit:
		b, ok := b.(*ast.BoolLit)
		return ok && a.Value == b.Value
	case *ast.NumLit:
		b, ok := b.(*ast.NumLit)
		return ok && a.Value == b.Value
	case *ast.StrLit:
		b, ok := b.(*ast.StrLit)
		return ok && a.Value == b.Value
	case *ast.RegexLit:
		b, ok := b.(*ast.RegexLit)
		return ok && a.Value == b.Value
	case *ast.BigIntLit:
		b, ok := b.(*ast.BigIntLit)
		return ok && a.Value.Cmp(&b.Value) == 0
	case *ast.NullLit:
		_, ok := b.(*ast.NullLit)
		return ok
	case *ast.UndefinedLit:
		_, ok := b.(*ast.UndefinedLit)
		return ok
	default:
		return false
	}
}
