package ucs

import (
	"strconv"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
)

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
// A nested sub-pattern is left whole for now. Its branch keeps it as a NormBind with no
// name, which says "still to be matched against this projection". Flattening those into
// splits of their own is the next stage of the rewrite; see the PR4 section of
// planning/ucs/implementation_plan.md.
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
		tail = normalizeTerm(s.Else, next)
	}

	cands := make([]candidate, len(s.Branches))
	for i, branch := range s.Branches {
		test, binds := shallowTest(branch.Pattern, s.Scrutinee, branch.Origin)
		cands[i] = candidate{index: i, branch: branch, test: test, binds: binds}
	}

	b := &splitBuilder{
		scrutinee: s.Scrutinee,
		tail:      tail,
		origin:    s.Origin,
		built:     map[string]Norm{},
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
}

// splitBuilder builds the branches of one core split. Everything it holds is fixed for
// that split: the scrutinee every branch tests, the tail control reaches when no branch
// covers the value, and the split's own provenance.
type splitBuilder struct {
	scrutinee *Scrutinee
	tail      Norm
	origin    Origin
	// built caches the fallthrough term for a set of candidates. A guarded branch's
	// fallthrough is built from the branches after it, so without the cache a run of
	// guarded branches would rebuild overlapping suffixes over and over. Each cached
	// term is also shared rather than duplicated, so a consumer that walks the IR
	// visits it once.
	built map[string]Norm
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
			// after it is unreachable.
			dflt = cont
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

// specialize returns the candidates that can still run once matched has succeeded, in
// source order. This is what a branch whose guard fails continues into, and dropping
// what cannot run is what keeps the continuation from re-testing a tag the value is
// already known to fail.
//
// Three things can happen to a later candidate. A test the matched one guarantees
// becomes unconditional, since a value that passed the first passes the second too, so
// it and nothing after it survives. A test that cannot hold of the same value is
// dropped. Anything else survives with its test intact and is re-tested.
//
// A matched candidate with no test taught nothing, so every later candidate survives
// unchanged.
func specialize(cands []candidate, matched Test) []candidate {
	if matched == nil {
		return cands
	}
	out := make([]candidate, 0, len(cands))
	for _, cand := range cands {
		switch {
		case cand.test == nil:
			return append(out, cand)
		case testImplies(matched, cand.test):
			cand.test = nil
			return append(out, cand)
		case testsDisjoint(matched, cand.test):
			// Nothing to do: no value passes both tests, so this candidate cannot run.
		default:
			out = append(out, cand)
		}
	}
	return out
}

// candidatesKey identifies a candidate list by the branches in it and whether each
// still makes its test, which is everything build reads. A `!` marks a candidate
// specialize made unconditional, so it does not key the same as the branch with its
// test intact.
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

// bindSpec is one leaf a branch's pattern binds: the name, the pattern leaf the solver
// binds through, and the projection the value comes from. A spec with no name holds a
// sub-pattern this stage does not flatten, which its branch keeps whole until nested
// patterns become splits of their own.
type bindSpec struct {
	name   string
	pat    ast.Pat
	source *Scrutinee
	origin Origin
}

// wrapBinds nests a branch's binds around its continuation, so the leaves are in scope
// for everything the branch runs, a guard condition included.
func wrapBinds(binds []bindSpec, cont Norm) Norm {
	for i := len(binds) - 1; i >= 0; i-- {
		bind := binds[i]
		cont = &NormBind{
			Name:   bind.name,
			Pat:    bind.pat,
			Source: bind.source,
			Cont:   cont,
			Origin: bind.origin,
		}
	}
	return cont
}

// shallowTest reads one tag-level off a pattern: the tag its branch tests, and the
// leaves it binds out of scrutinee. The test is nil for a catch-all, which binds
// without testing.
//
// It goes exactly one level deep. A sub-pattern that is an identifier becomes a bind on
// its projection, and any other sub-pattern becomes a nameless bind that keeps the
// sub-pattern whole. Turning those into splits over their projections is the next stage
// of the rewrite.
func shallowTest(p ast.Pat, scrutinee *Scrutinee, origin Origin) (Test, []bindSpec) {
	switch p := p.(type) {
	case nil, *ast.WildcardPat:
		// A wildcard binds without testing. A branch carrying no pattern at all is a
		// lowering bug rather than a form the surface can write, and treating it the
		// same way keeps normalization total so the bug shows up as a printable IR.
		return nil, nil
	case *ast.IdentPat:
		// An identifier pattern binds the scrutinee itself rather than a projection of
		// it, so the bind's source is the scrutinee node.
		return nil, []bindSpec{{
			name:   p.Name,
			pat:    p,
			source: scrutinee,
			origin: leafOrigin(origin, p),
		}}
	case *ast.LitPat:
		return &LitTest{Lit: p.Lit}, nil
	case *ast.TuplePat:
		return tupleTest(p, scrutinee, origin)
	case *ast.ObjectPat:
		return objectTest(p, scrutinee, origin)
	case *ast.InstancePat:
		// An instance pattern tests a nominal tag and then reaches its fields the same
		// way a structural object does. The fields are not part of the tag, so the
		// object test objectTest derives is dropped and only its binds are kept.
		_, binds := objectTest(p.Object, scrutinee, origin)
		return &ClassTest{Name: p.ClassName}, binds
	case *ast.ExtractorPat:
		binds := make([]bindSpec, 0, len(p.Args))
		for i, arg := range p.Args {
			binds = appendBind(binds, arg, scrutinee, ExtractStep{Index: i}, origin)
		}
		return &ExtractorTest{Name: p.Name, Arity: len(p.Args)}, binds
	default:
		// A bare rest pattern is the only form left, and it is meaningful only inside a
		// tuple or an object. Keeping it whole as a nameless bind hands it to the
		// solver's pattern walk, which reports it, instead of dropping the names under
		// it here.
		return nil, []bindSpec{{pat: p, source: scrutinee, origin: leafOrigin(origin, p)}}
	}
}

// tupleTest derives the tag and binds of a tuple pattern. Len counts the fixed prefix,
// and a trailing rest relaxes the test to that prefix and binds the suffix past it.
//
// A rest anywhere but last names no suffix a SuffixStep can reach, as SuffixStep spells
// out, so the test covers the prefix before it and nothing after it binds. The pattern
// is already unsupported downstream; reporting it belongs to the pass that lowers the
// surface, which sees the source the IR no longer holds.
func tupleTest(p *ast.TuplePat, scrutinee *Scrutinee, origin Origin) (Test, []bindSpec) {
	fixed, rest := splitTrailingRest(p.Elems)
	binds := make([]bindSpec, 0, len(fixed)+1)
	for i, elem := range fixed {
		binds = appendBind(binds, elem, scrutinee, IndexStep{Index: i}, origin)
	}
	kind := NoRest
	if len(fixed) < len(p.Elems) {
		kind = TrailingRest
	}
	if rest != nil {
		binds = appendBind(binds, rest.Pattern, scrutinee, SuffixStep{From: len(fixed)}, origin)
	}
	return &TupleTest{Len: len(fixed), Rest: kind}, binds
}

// splitTrailingRest splits a tuple pattern's elements at its first rest. fixed holds
// the elements before it, which bind by position. rest is that element only when it is
// the pattern's last one, the only position whose suffix is expressible. It mirrors how
// the solver's pattern walk splits the same elements.
func splitTrailingRest(elems []ast.Pat) (fixed []ast.Pat, rest *ast.RestPat) {
	for i, elem := range elems {
		r, isRest := elem.(*ast.RestPat)
		if !isRest {
			continue
		}
		if i == len(elems)-1 {
			return elems[:i], r
		}
		return elems[:i], nil
	}
	return elems, nil
}

// objectTest derives the tag and binds of an object pattern. Keys are the fields the
// pattern names at this level in source order, and a field's own sub-pattern becomes a
// bind on the projected field rather than part of the tag.
//
// A rest binds the scrutinee with the keys named here removed. The key set it excludes
// is exactly those keys, so a field a deeper pattern matches is still in a shallower
// rest.
func objectTest(p *ast.ObjectPat, scrutinee *Scrutinee, origin Origin) (Test, []bindSpec) {
	if p == nil {
		return &ObjectTest{}, nil
	}
	objKeys := make([]ObjectKey, 0, len(p.Elems))
	binds := make([]bindSpec, 0, len(p.Elems))
	named := set.NewSet[string]()
	var rest *ast.ObjRestPat

	for i, elem := range p.Elems {
		switch e := elem.(type) {
		case *ast.ObjShorthandPat:
			// A default binds the field even when it is absent, so the test must not
			// demand it. The solver's pattern walk makes the same field optional.
			objKeys = append(objKeys, ObjectKey{Name: e.Key.Name, Optional: e.Default != nil})
			named.Add(e.Key.Name)
			// A shorthand element is not an ast.Pat, so the bind names no pattern leaf.
			binds = append(binds, bindSpec{
				name:   e.Key.Name,
				source: scrutinee.Project(FieldStep{Name: e.Key.Name}, leafOrigin(origin, e)),
				origin: leafOrigin(origin, e),
			})
		case *ast.ObjKeyValuePat:
			objKeys = append(objKeys, ObjectKey{
				Name:     e.Key.Name,
				Optional: valueDefaultsField(e.Value),
			})
			named.Add(e.Key.Name)
			binds = appendBind(binds, e.Value, scrutinee, FieldStep{Name: e.Key.Name}, origin)
		case *ast.ObjRestPat:
			// One rest takes every property the pattern did not name, so a second has
			// nothing left, and only a last one holds the whole remainder. The solver's
			// pattern walk reports the other placements.
			if rest == nil && i == len(p.Elems)-1 {
				rest = e
			}
		}
	}

	kind := NoRest
	if rest != nil {
		kind = TrailingRest
		step := RemainderStep{Exclude: named}
		binds = appendBind(binds, rest.Pattern, scrutinee, step, origin)
	}
	return &ObjectTest{Keys: objKeys, Rest: kind}, binds
}

// valueDefaultsField reports whether an object pattern's value sub-pattern carries a
// default, as `{x: a = 0}` does, which makes the field optional the same way a
// shorthand default does.
func valueDefaultsField(p ast.Pat) bool {
	ident, ok := p.(*ast.IdentPat)
	return ok && ident.Default != nil
}

// appendBind adds what a sub-pattern contributes at the projection step reaches. A
// wildcard binds nothing and needs no test, so it adds nothing at all. An identifier
// binds the projection. Any other sub-pattern is kept whole as a nameless bind for the
// stage that flattens nesting.
func appendBind(binds []bindSpec, p ast.Pat, parent *Scrutinee, step Step, origin Origin) []bindSpec {
	if p == nil {
		return binds
	}
	if _, isWildcard := p.(*ast.WildcardPat); isWildcard {
		return binds
	}
	leaf := leafOrigin(origin, p)
	spec := bindSpec{pat: p, source: parent.Project(step, leaf), origin: leaf}
	if ident, ok := p.(*ast.IdentPat); ok {
		spec.name = ident.Name
	}
	return append(binds, spec)
}

// leafOrigin points a bind and its projection at the pattern leaf they came from, so a
// message about one blames the field the user wrote rather than the whole arm. It keeps
// the branch's kind, since the leaf lowered from the same surface construct, and falls
// back to the branch's origin when the leaf names no node.
func leafOrigin(branch Origin, leaf Spanned) Origin {
	if _, ok := SpanOf(leaf); !ok {
		return branch
	}
	return At(branch.Kind, leaf)
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
