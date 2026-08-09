package solver

import (
	"maps"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/escalier-lang/escalier/internal/solver/ucs"
)

// Project-and-bind over a UCS IR projection path.
//
// The `ucs` package names a sub-scrutinee as a path rather than as a type. The path
// `l.start.x` is the root scrutinee `l`, a field step to `start`, then a field step to
// `x`. Turning that path into the type its value has is this file's job, and it is the
// one solver-side operation every walk over the IR depends on.
//
// The projection rule it applies is bindPatMode's. A leaf bound off a path must land at
// the type the same leaf would land at nested inside one whole pattern. Otherwise the IR
// walk and the `bindPattern` path everything else destructures through would infer
// different types for one leaf. That is why a resolved scrutinee carries the same triple
// bindPatMode threads down a nested pattern — the projection variable, the statically
// known shape, and the binding mode — and hands all three back to bindPatMode rather than
// rebuilding them from a single type.

// scrutineeView is what the solver has worked out about the value one IR scrutinee
// names. A binder holds one view per scrutinee it has reached, so a scrutinee shared by
// an inner split and every leaf beneath it is projected once.
type scrutineeView struct {
	// ty is the type a leaf's requirements are emitted against: the projection variable
	// a member lookup lowered the value into, or the root scrutinee's own carrier. Any
	// borrow is already peeled off it, since mode records the borrow instead.
	ty soltype.Type
	// concrete is the value's statically known shape, and nil when the path did not
	// reach one. It is what decides whether a leaf of a borrowed scrutinee projects a
	// borrow, so dropping it would silently bind such a leaf as an owned value.
	concrete soltype.Type
	// mode is the binding mode the root scrutinee's borrow fixed. It propagates
	// unchanged down the path, the same match ergonomics bindPatMode follows.
	mode bindMode
	// shape is the type union narrowing reads its members off. It is the scrutinee's own
	// type once that is concrete, and a coalesced snapshot of it while it is still an
	// inference variable.
	shape soltype.Type
	// tested is what the split's tag test left behind for the projections beneath this
	// scrutinee. It is nil until a split tests it, and nil afterwards for a test that
	// leaves nothing behind.
	tested tested
}

// tested is what one branch's tag test resolved for the steps that project out of the
// scrutinee it tested.
//
// It is a sum rather than a group of fields on scrutineeView because the alternatives are
// disjoint. A scrutinee is tested against exactly one tag, and each tag kind resolves a
// different thing. A tuple test mints element variables and an extractor test resolves
// constructor parameters, so no one value ever carries both.
//
// A literal test and a class test resolve nothing and leave it nil. A class test narrows
// the scrutinee's own type to the instance member view instead, so its field steps read
// that view through the ordinary member lookup and need nothing carried here.
type tested interface{ isTested() }

func (*testedObject) isTested()    {}
func (*testedTuple) isTested()     {}
func (*testedExtractor) isTested() {}

// testedObject carries the object test itself, which a field step consults to ask whether
// the key the pattern defaulted may be absent. Nothing else about an object test is
// resolved up front, since its keys are looked up one at a time by the steps beneath it.
type testedObject struct{ test *ucs.ObjectTest }

// testedTuple carries what a tuple test's whole-tuple requirement produced.
type testedTuple struct {
	// elems holds one projection variable per fixed element, minted when the requirement
	// was emitted. An index step reads its element out of this rather than emitting a
	// requirement of its own.
	elems []soltype.Type
	// scrut and concrete are the grounded tuple shapes the test resolved, which a suffix
	// step reads its `...rest` slice out of. Each is nil when the corresponding type is
	// not a grounded tuple.
	scrut, concrete *soltype.TupleType
}

// testedExtractor carries the constructor parameters an extractor test resolved. An
// extract step reads its value's type out of them, the interim protocol bindExtractorPat
// also binds through until M7's `[Symbol.customMatcher]` lands.
type testedExtractor struct{ params []*soltype.FuncParam }

// pathBinder resolves IR projection paths into types and binds leaf patterns off them.
// It is seeded with the root scrutinee's inferred type, which is the only type the IR
// itself cannot supply.
//
// A split applies its branch's tag test through narrowedBy, which returns a derived
// binder instead of mutating this one. Two branches of the same split therefore narrow
// the same scrutinee differently without seeing each other's view, and each branch
// projects its own variables for the sub-scrutinees beneath it.
type pathBinder struct {
	c   *checker
	lvl int
	// blame is the node a constraint is anchored to when the scrutinee being resolved
	// carries no origin the solver can blame. A match arm, for one, has a span but is
	// not an ast.Node.
	blame ast.Node
	views map[*ucs.Scrutinee]scrutineeView
}

// newPathBinder builds the binder for one conditional form. root is the IR's root
// scrutinee and rootType the type the walk inferred for the match target, so the binder
// never re-infers the target and a side-effecting one such as `match f() { … }` is
// evaluated once. blame is the node a constraint falls back to, normally the whole
// `match`, `if val`, or `val … else`.
func (c *checker) newPathBinder(lvl int, blame ast.Node, root *ucs.Scrutinee, rootType soltype.Type) *pathBinder {
	b := &pathBinder{c: c, lvl: lvl, blame: blame, views: map[*ucs.Scrutinee]scrutineeView{}}
	carrier := soltype.CarrierOf(rootType)
	// Snapshot the scrutinee's union structure before any branch binds. A literal test
	// adds its literal as a lower bound on the scrutinee variable, so grounding again
	// under a later branch would read that literal back as an extra union member and
	// narrow against a member the user never wrote. inferMatch takes the same snapshot
	// for the same reason.
	shape := groundedCarrier(carrier)
	b.views[root] = scrutineeView{
		ty:       carrier,
		concrete: carrier,
		mode:     bindModeOf(rootType),
		shape:    shape,
	}
	return b
}

// narrowedBy returns a binder in which the split over s has tested it against test. The
// receiver is left untouched, so a caller can apply a different test to the same
// scrutinee for the next branch.
//
// Applying a test is what makes the projections beneath it resolvable. A tuple test
// mints the element variables an index step reads, a class test projects the instance
// member view a field step reads, and an extractor test resolves the constructor
// parameters an extract step reads.
//
// blame is the surface node a diagnostic about the test anchors to, which is the pattern
// the branch lowered from. A blame that names no node falls back to s's own origin.
func (b *pathBinder) narrowedBy(scope *Scope, s *ucs.Scrutinee, test ucs.Test, blame ucs.Spanned) *pathBinder {
	// Resolve s on the receiver, before the clone, so the memo lands in the binder every
	// branch of the split shares. Resolving it on the clone instead would project the
	// scrutinee once per branch, minting a second variable and a second member lookup for
	// one value.
	v := b.viewOf(scope, s)
	node, ok := blamableNode(blame)
	if !ok {
		node = b.blameFor(s)
	}
	next := &pathBinder{c: b.c, lvl: b.lvl, blame: b.blame, views: maps.Clone(b.views)}
	next.views[s] = next.applyTest(scope, node, v, test)
	return next
}

// typeAt resolves the path s names to the type its value has.
func (b *pathBinder) typeAt(scope *Scope, s *ucs.Scrutinee) soltype.Type {
	return b.viewOf(scope, s).ty
}

// bindAt binds pat against the value the path s names, defining each leaf the pattern
// introduces as a monomorphic binding in scope. It is the project-and-bind operation the
// typing walk calls for every leaf of a normalized branch.
func (b *pathBinder) bindAt(scope *Scope, s *ucs.Scrutinee, pat ast.Pat) soltype.Pat {
	return b.bindAtWith(scope, s, pat, nil, defineLeafMono)
}

// bindAtWith is bindAt parameterized by the leaf-placement strategy, the same split
// bindPatternWith makes for the top-level driver's pre-bound binding vars.
func (b *pathBinder) bindAtWith(
	scope *Scope, s *ucs.Scrutinee, pat ast.Pat, leafTypes map[string]soltype.Type, emit leafEmit,
) soltype.Pat {
	v := b.viewOf(scope, s)
	return b.c.bindPatMode(scope, b.lvl, pat, v.ty, v.concrete, v.mode, leafTypes, emit)
}

// bindElemAt binds the leaf an object pattern's shorthand element introduces, the `x` of
// `{mut x}`. A shorthand is not an ast.Pat, so it cannot go through bindAt, and it carries
// the annotation, default, and `mut` marker the name alone does not.
//
// It resolves the field itself rather than through viewOf, because a shorthand's field
// lookup takes no upper bound. bindPatMode's shorthand arm takes none either. The leaf is
// the projection rather than a value handed to a sub-pattern, and pinning it would make a
// `&mut` leaf invariantly exact against the scrutinee's field, which applyBindMode's bmMut
// arm spells out. Reading the same field through projectField would add that pin and
// reject a `mut` leaf the written pattern accepts.
func (b *pathBinder) bindElemAt(scope *Scope, s *ucs.Scrutinee, elem *ast.ObjShorthandPat) {
	v := b.shorthandView(scope, s, elem)
	t := b.c.applyLeafExtras(scope, b.lvl, elem, v.ty, elem.TypeAnn, elem.Default)
	t = b.c.applyBindMode(b.lvl, elem, elem.Mutable, t, b.c.concreteLeaf(v.concrete, elem.TypeAnn), v.mode)
	b.c.bindLeaf(scope, elem.Key.Name, t, elem, nil, defineLeafMono)
}

// shorthandView resolves the field a shorthand element names into the variable its leaf
// binds at, memoizing it on s the way viewOf memoizes every other projection. A default
// relaxes the lookup to tolerate an absent field, since `{x = 0}` binds x either way.
func (b *pathBinder) shorthandView(scope *Scope, s *ucs.Scrutinee, elem *ast.ObjShorthandPat) scrutineeView {
	if v, ok := b.views[s]; ok {
		return v
	}
	parent := b.viewOf(scope, s.Parent)
	name := elem.Key.Name
	beta := b.c.freshAt(b.lvl)
	b.c.constrain(elem, parent.ty, propReq(name, beta, elem.Default != nil))
	v := derive(parent, beta, fieldConcrete(parent.concrete, name))
	b.views[s] = v
	return v
}

// viewOf returns s's view, resolving and memoizing it on first use. Memoizing is what
// materializes each scrutinee once: the inner split and every leaf beneath it hold the
// same *ucs.Scrutinee pointer, so they share one projection rather than each emitting
// its own member lookup.
func (b *pathBinder) viewOf(scope *Scope, s *ucs.Scrutinee) scrutineeView {
	if v, ok := b.views[s]; ok {
		return v
	}
	var v scrutineeView
	if s.IsRoot() {
		// A root the binder was not seeded with names a target whose type nothing
		// inferred. Bind against a fresh variable so its leaves still land in scope and a
		// later reference resolves instead of cascading into an unknown-identifier error.
		fresh := b.c.freshAt(b.lvl)
		v = scrutineeView{ty: fresh, shape: fresh}
	} else {
		v = b.project(scope, s)
	}
	b.views[s] = v
	return v
}

// project resolves one step of a path, building the view of s from the view of its
// parent.
func (b *pathBinder) project(scope *Scope, s *ucs.Scrutinee) scrutineeView {
	parent := b.viewOf(scope, s.Parent)
	node := b.blameFor(s)
	switch step := s.Step.(type) {
	case ucs.FieldStep:
		return b.projectField(parent, node, step.Name)
	case ucs.IndexStep:
		return b.projectIndex(s.Parent, parent, node, step.Index)
	case ucs.ExtractStep:
		return b.projectExtract(parent, step.Index)
	case ucs.SuffixStep:
		return b.projectSuffix(parent, node, step.From)
	case ucs.RemainderStep:
		return b.projectRemainder(parent, node, step.Exclude)
	default:
		// ucs.Step is a closed interface, so this arm is reached only by a step kind added
		// to the IR without a projection rule here. Recover with a fresh variable rather
		// than resolving to something the leaf would silently mis-bind against.
		return derive(parent, b.c.freshAt(b.lvl), nil)
	}
}

// projectField resolves `parent.name` through the checker's one field projection, so a
// field the scrutinee lacks surfaces MissingPropertyError from the same rule a written
// `{name: sub}` pattern does.
func (b *pathBinder) projectField(parent scrutineeView, node ast.Node, name string) scrutineeView {
	proj, concrete := b.c.projectField(node, b.lvl, parent.ty, parent.concrete, name, parent.fieldOptional(name))
	return derive(parent, proj, concrete)
}

// projectIndex resolves `parent.i`, the i-th element of a tuple. parentScrut names the
// scrutinee `parent` is the view of, so a tuple shape minted here is written back onto it
// and every later step off the same value reads that one shape.
func (b *pathBinder) projectIndex(parentScrut *ucs.Scrutinee, parent scrutineeView, node ast.Node, i int) scrutineeView {
	tup, ok := parent.tested.(*testedTuple)
	if !ok || i >= len(tup.elems) {
		// No tuple test fixed this scrutinee's arity, so nothing has minted a variable for
		// position i yet. Emit an inexact requirement pinning the positions up to it —
		// "the scrutinee is a tuple at least this long" — so any path resolves, not only one
		// a split has already tested. A normalized index step always sits under a tuple
		// test, so this runs only for a path built without one, and it mints another
		// requirement if a still deeper index arrives afterwards.
		tup = b.tupleShapeReq(parent, node, i+1, true)
		parent.tested = tup
		b.views[parentScrut] = parent
	}
	var concrete soltype.Type
	if tup.concrete != nil && i < len(tup.concrete.Elems) {
		concrete = tup.concrete.Elems[i]
	}
	return derive(parent, tup.elems[i], concrete)
}

// projectExtract resolves the i-th positional value an extractor yields, the `v` of
// `Ok(v)`. The values come from the constructor parameters the enclosing extractor test
// resolved, the interim protocol bindExtractorPat binds through until M7 replaces it with
// `[Symbol.customMatcher]`. Nothing is constrained here, so the step blames no node. The
// extractor test already narrowed the scrutinee when it resolved the constructor.
func (b *pathBinder) projectExtract(parent scrutineeView, i int) scrutineeView {
	ext, ok := parent.tested.(*testedExtractor)
	if !ok || i >= len(ext.params) {
		// Either no extractor test resolved a constructor for this scrutinee, or the
		// pattern took more values than the constructor yields. The arity error is reported
		// where the test is applied, so recovering with a fresh variable here keeps the
		// leaves defined without a second diagnostic.
		return derive(parent, b.c.freshAt(b.lvl), nil)
	}
	param := ext.params[i].Type
	return derive(parent, param, param)
}

// projectSuffix resolves `parent[from..]`, the tuple elements past a fixed prefix, which
// is what a tuple pattern's `...rest` binds.
func (b *pathBinder) projectSuffix(parent scrutineeView, node ast.Node, from int) scrutineeView {
	// With no tuple test above it the grounded shapes are unknown, and tupleRestType falls
	// back to grounding the scrutinee itself.
	var scrutTup, concreteTup *soltype.TupleType
	if tup, ok := parent.tested.(*testedTuple); ok {
		scrutTup, concreteTup = tup.scrut, tup.concrete
	}
	suffix, concrete := b.c.tupleRestType(b.lvl, node, parent.ty, scrutTup, concreteTup, from)
	return derive(parent, suffix, concrete)
}

// projectRemainder resolves `parent \ exclude`, the scrutinee's members minus the keys
// the object pattern named, which is what an object pattern's `...rest` binds.
func (b *pathBinder) projectRemainder(parent scrutineeView, node ast.Node, exclude set.Set[string]) scrutineeView {
	leftover, concrete := b.c.objectRestType(b.lvl, node, parent.ty, parent.concrete, exclude)
	return derive(parent, leftover, concrete)
}

// derive builds the view of a value projected out of parent. The binding mode carries
// over unchanged, which is what makes every leaf of a borrowed scrutinee a borrow however
// deep the path reaches it.
func derive(parent scrutineeView, ty, concrete soltype.Type) scrutineeView {
	shape := concrete
	if shape == nil {
		shape = ty
	}
	return scrutineeView{ty: ty, concrete: concrete, mode: parent.mode, shape: shape}
}

// fieldOptional reports whether the object test over this scrutinee tolerates the named
// field being absent, which a destructuring default produces. `{x = 0}` binds x to 0 when
// the field is absent, so a requirement that demanded it would reject a value the pattern
// matches. Absent an object test the field is required, matching a pattern with no
// default.
func (v scrutineeView) fieldOptional(name string) bool {
	obj, ok := v.tested.(*testedObject)
	if !ok {
		return false
	}
	for _, key := range obj.test.Keys {
		if key.Name == name {
			return key.Optional
		}
	}
	return false
}

// applyTest narrows v by the tag test one branch of a split applies to it, and resolves
// whatever that test leaves behind for the projections beneath it.
func (b *pathBinder) applyTest(scope *Scope, node ast.Node, v scrutineeView, test ucs.Test) scrutineeView {
	// A scrutinee is tested against one tag per branch, so whatever an earlier call left
	// here belongs to a different branch's test and must not be read by this one's steps.
	v.tested = nil
	switch t := test.(type) {
	case *ucs.ObjectTest:
		// An object test emits nothing itself. Its keys are looked up one at a time, by the
		// field steps beneath it, which is the same per-field dispatch bindPatMode's object
		// arm makes.
		v = b.narrowUnion(v, test)
		v.tested = &testedObject{test: t}
		return v
	case *ucs.TupleTest:
		v = b.narrowUnion(v, test)
		v.tested = b.tupleShapeReq(v, node, t.Len, t.Rest == ucs.TrailingRest)
		return v
	case *ucs.LitTest:
		b.applyLitTest(node, v, t)
		return v
	case *ucs.ClassTest:
		return b.applyClassTest(scope, node, v, t)
	case *ucs.ExtractorTest:
		return b.applyExtractorTest(scope, node, v, t)
	default:
		return v
	}
}

// narrowUnion drops the union members the test cannot destructure, so a branch that
// tests one variant binds against that variant alone rather than against the whole
// union. It is narrowMatchArm read off the IR's tag test instead of off the source
// pattern, and it keeps that function's rules so PR6 inherits variant-narrowing
// unchanged.
func (b *pathBinder) narrowUnion(v scrutineeView, test ucs.Test) scrutineeView {
	// A borrowed shape is left wrapped rather than peeled the way groundedCarrier peels
	// one. The narrowed result below replaces v.ty and v.concrete, and nothing here
	// rewraps it, so peeling first would drop the borrow off a nested scrutinee.
	shape := v.shape
	if _, isVar := shape.(*soltype.TypeVarType); isVar {
		shape = groundedCarrier(shape)
	}
	narrowed, ok := narrowUnionMembers(shape, func(member soltype.Type) bool {
		return testMatchesMemberShape(test, member)
	})
	if !ok {
		// Narrowing does not apply, so the scrutinee's own type stays the bind target. The
		// borrow needs no rewrap the way narrowMatchArm's does, since it rides mode rather
		// than the type.
		return v
	}
	v.ty, v.concrete, v.shape = narrowed, narrowed, narrowed
	return v
}

// testMatchesMemberShape reports whether a structural tag test's shape fits a union
// member, so a branch under that test can destructure the member. It is
// patternMatchesMemberShape read off the IR's test rather than off the source pattern. An
// object test fits an object member carrying every key the test names, and a tuple test
// fits a tuple member of its fixed arity, or of at least that arity under a trailing
// rest. Every other test kind returns false, the same gate narrowMatchArm puts on object
// and tuple patterns alone.
func testMatchesMemberShape(test ucs.Test, member soltype.Type) bool {
	switch t := test.(type) {
	case *ucs.ObjectTest:
		names := make([]string, len(t.Keys))
		for i, key := range t.Keys {
			names[i] = key.Name
		}
		return objectMemberHasKeys(member, names)
	case *ucs.TupleTest:
		return tupleMemberFitsArity(member, t.Len, t.Rest == ucs.TrailingRest)
	default:
		return false
	}
}

// tupleShapeReq resolves what a tuple test leaves for the steps beneath it, through the
// checker's one tuple projection. Every element's upper bound is anchored to node, since a
// normalized test names no sub-pattern to underline the way a written tuple pattern does.
func (b *pathBinder) tupleShapeReq(v scrutineeView, node ast.Node, count int, inexact bool) *testedTuple {
	elems, scrutTup, concreteTup := b.c.projectTuple(node, b.lvl, v.ty, v.concrete, count, inexact, nil)
	return &testedTuple{elems: elems, scrut: scrutTup, concrete: concreteTup}
}

// applyLitTest asserts the branch's literal is an admissible value of the scrutinee, so
// the literal flows INTO the scrutinee and `5 <: number` checks. It binds nothing, since
// a literal test names no sub-scrutinee. It is bindPatMode's literal arm read off the
// test.
func (b *pathBinder) applyLitTest(node ast.Node, v scrutineeView, t *ucs.LitTest) {
	if atom, _, isAtom := atomLitOf(t.Lit); isAtom {
		// A `null` or `undefined` test asserts the atom is admissible, the same direction as
		// the literal below.
		b.c.constrain(node, atom, v.ty)
		return
	}
	lt, ok := b.c.litTypeOf(t.Lit)
	if !ok {
		b.c.reportUnsupported(t.Lit)
		return
	}
	b.c.constrain(node, lt, v.ty)
}

// applyClassTest narrows the scrutinee to the named class and resolves the instance
// member view its field steps read. It is bindInstancePat's narrowing read off the test,
// with the fields left to the projections rather than bound here.
func (b *pathBinder) applyClassTest(scope *Scope, node ast.Node, v scrutineeView, t *ucs.ClassTest) scrutineeView {
	name := ast.QualIdentToString(t.Name)
	ct, ok := b.c.instancePatClass(scope, name)
	if !ok {
		b.c.report(&InstancePatternNotClassError{Node: node, Name: name})
		// Resolve the fields against a fresh variable so a leaf beneath the test stays
		// defined without a second cascade against the real scrutinee.
		fresh := b.c.freshAt(b.lvl)
		v.ty, v.concrete, v.shape = fresh, nil, fresh
		return v
	}
	target, targetConcrete := b.c.narrowToClass(b.lvl, node, ct, v.ty, v.concrete)
	v.ty, v.concrete, v.shape = target, targetConcrete, target
	return v
}

// applyExtractorTest narrows the scrutinee to the constructor's return type and resolves
// the parameters its extract steps read. It is bindExtractorPat's narrowing read off the
// test, with the extracted values left to the projections rather than bound here.
func (b *pathBinder) applyExtractorTest(scope *Scope, node ast.Node, v scrutineeView, t *ucs.ExtractorTest) scrutineeView {
	name := ast.QualIdentToString(t.Name)
	ctor, ok := b.c.extractorCtor(scope, b.lvl, t.Name)
	if !ok {
		b.c.report(&ExtractorPatternNotCtorError{Node: node, Name: name})
		return v
	}
	params := b.c.narrowToExtractor(node, ctor, v.ty)
	if t.Arity != len(params) {
		b.c.report(&ExtractorPatternArityError{Node: node, Name: name, Expected: len(params), Got: t.Arity})
	}
	v.tested = &testedExtractor{params: params}
	return v
}

// blameFor returns the node a constraint emitted while resolving s is anchored to. A
// scrutinee's origin points at the source pattern the projection came from, which is the
// narrowest honest span. That origin holds only a Spanned, so a scrutinee whose origin
// names something the solver cannot blame falls back to the node the binder was seeded
// with.
func (b *pathBinder) blameFor(s *ucs.Scrutinee) ast.Node {
	if s != nil {
		if n, ok := blamableNode(s.Origin.Node); ok {
			return n
		}
	}
	return b.blame
}

// blamableNode returns n as an ast.Node a diagnostic can anchor to, and false when it is
// not one or holds no node at all.
//
// The nil test runs through ucs.SpanOf rather than through `n != nil`. A Spanned field can
// hold a typed nil pointer, which is non-nil as an interface value, satisfies the ast.Node
// assertion, and then panics the moment a diagnostic reads its span. SpanOf is the guard
// the ucs package added for exactly that hazard.
func blamableNode(n ucs.Spanned) (ast.Node, bool) {
	if _, ok := ucs.SpanOf(n); !ok {
		return nil, false
	}
	node, ok := n.(ast.Node)
	return node, ok
}
