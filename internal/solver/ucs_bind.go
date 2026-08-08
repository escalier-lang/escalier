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
	shape := carrier
	if _, isVar := carrier.(*soltype.TypeVarType); isVar {
		// Snapshot the scrutinee's union structure before any branch binds. A literal test
		// adds its literal as a lower bound on the scrutinee variable, so coalescing again
		// under a later branch would read that literal back as an extra union member and
		// narrow against a member the user never wrote. inferMatch takes the same snapshot
		// for the same reason.
		shape = soltype.CarrierOf(coalesce(rootType, soltype.Positive))
	}
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
func (b *pathBinder) narrowedBy(scope *Scope, s *ucs.Scrutinee, test ucs.Test) *pathBinder {
	// Resolve s on the receiver, before the clone, so the memo lands in the binder every
	// branch of the split shares. Resolving it on the clone instead would project the
	// scrutinee once per branch, minting a second variable and a second member lookup for
	// one value.
	v := b.viewOf(scope, s)
	next := &pathBinder{c: b.c, lvl: b.lvl, blame: b.blame, views: maps.Clone(b.views)}
	next.views[s] = next.applyTest(scope, b.blameFor(s), v, test)
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

// projectField resolves `parent.name`. The lookup is the same inexact one-property
// requirement bindPatMode's object arm emits, so a field the scrutinee lacks surfaces
// MissingPropertyError from the same rule a written `{name}` pattern does.
func (b *pathBinder) projectField(parent scrutineeView, node ast.Node, name string) scrutineeView {
	beta := b.c.freshAt(b.lvl)
	b.c.constrain(node, parent.ty, propReq(name, beta, parent.fieldOptional(name)))
	// Pin beta's upper bound to the field's own type when the scrutinee is a concrete
	// object. The requirement above gives beta the field only as a lower bound, which
	// cannot reject a refutable literal sub-pattern of the wrong kind, so `{x: "hi"}`
	// against `{x: number}` would go unreported. bindPatMode's key-value arm adds the same
	// bound for the same reason.
	//
	// An optional property gets no pin. Reading `x` off `{x?: number}` produces
	// `number | undefined`, so a bound of `number` would reject the `undefined` half of the
	// value the field actually holds. bindPatMode's shorthand arm, which every `{x}` goes
	// through, adds no pin at all for the same reason.
	if obj, ok := parent.ty.(*soltype.ObjectType); ok {
		if prop, found := obj.Prop(name); found && !prop.Optional {
			b.c.constrain(node, beta, prop.Type)
		}
	}
	return derive(parent, beta, fieldConcrete(parent.concrete, name))
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
	shape := v.shape
	if _, isVar := shape.(*soltype.TypeVarType); isVar {
		shape = soltype.CarrierOf(coalesce(shape, soltype.Positive))
	}
	u, ok := shape.(*soltype.UnionType)
	if !ok {
		return v
	}
	kept := make([]soltype.Type, 0, len(u.Types))
	for _, member := range u.Types {
		if testMatchesMemberShape(test, member) {
			kept = append(kept, member)
		}
	}
	// When the test matches no listed member there is nothing to narrow to. When it
	// matches every listed member, narrowing would reproduce the whole union, including an
	// inexact union's open tail. Either way the scrutinee's own type is the bind target.
	if len(kept) == 0 || len(kept) == len(u.Types) {
		return v
	}
	var narrowed soltype.Type
	if len(kept) == 1 && !u.Inexact {
		narrowed = kept[0]
	} else {
		// An inexact union keeps its open `...` tail through narrowing. A tail member may
		// carry the test's fields at any type, so the tail is retained and a narrowed
		// inexact member's fields read as `unknown`. Keeping the structured union rather
		// than collapsing to bare `unknown` is what leaves the listed members for the field
		// steps beneath to read.
		narrowed = &soltype.UnionType{Types: kept, Inexact: u.Inexact}
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
		obj, ok := soltype.CarrierOf(member).(*soltype.ObjectType)
		if !ok {
			return false
		}
		for _, key := range t.Keys {
			if _, found := obj.Prop(key.Name); !found {
				return false
			}
		}
		return true
	case *ucs.TupleTest:
		tup, ok := soltype.CarrierOf(member).(*soltype.TupleType)
		if !ok {
			return false
		}
		if t.Rest == ucs.TrailingRest {
			return len(tup.Elems) >= t.Len
		}
		return len(tup.Elems) == t.Len
	default:
		return false
	}
}

// tupleShapeReq mints one projection variable per element position and emits the
// whole-tuple requirement that lowers the scrutinee's matching element into each. inexact
// relaxes the requirement to "a tuple at least this long", which is what a trailing rest
// asks for; an exact requirement is what rejects a wrong arity with
// TupleLengthMismatchError. It resolves the grounded scrutinee and concrete tuples
// alongside, which a suffix step reads its slice out of.
func (b *pathBinder) tupleShapeReq(v scrutineeView, node ast.Node, count int, inexact bool) *testedTuple {
	elems := make([]soltype.Type, count)
	for i := range elems {
		elems[i] = b.c.freshAt(b.lvl)
	}
	b.c.constrain(node, v.ty, &soltype.TupleType{Elems: elems, Inexact: inexact})
	scrutTup, _ := b.c.groundedTuple(v.ty)
	concreteTup, _ := b.c.groundedTuple(v.concrete)
	// Pin each variable's upper bound to the scrutinee's own element when the scrutinee is
	// a grounded tuple. The requirement above gives each one the element only as a lower
	// bound, so without this a refutable literal sub-pattern of the wrong kind, `[a, "hi"]`
	// against `[number, number]`, would go unreported. bindPatMode's tuple arm adds the
	// same bound.
	if scrutTup != nil {
		for i := range elems {
			if i < len(scrutTup.Elems) {
				b.c.constrain(node, elems[i], scrutTup.Elems[i])
			}
		}
	}
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
	blame := blameName(t.Name, node)
	name := ast.QualIdentToString(t.Name)
	ct, ok := b.c.instancePatClass(scope, name)
	if !ok {
		b.c.report(&InstancePatternNotClassError{Node: blame, Name: name})
		// Resolve the fields against a fresh variable so a leaf beneath the test stays
		// defined without a second cascade against the real scrutinee.
		fresh := b.c.freshAt(b.lvl)
		v.ty, v.concrete, v.shape = fresh, nil, fresh
		return v
	}
	inst := b.c.freshClassInstance(ct, b.lvl)
	// The test narrows the scrutinee to the named class, so the instance flows into the
	// scrutinee and a scrutinee that cannot be this class is rejected here.
	b.c.constrain(blame, inst, v.ty)
	// Project the scrutinee's own instance when it names the same class, so its concrete
	// arguments give the field types directly. A downcast falls back to the asserted
	// instance.
	projected := inst
	if sc, ok := classCarrier(v.ty); ok && sc.Name == ct.Name {
		projected = sc
	}
	body, ok := b.c.ctx.projectClassBody(projected)
	if !ok {
		return v
	}
	v.ty, v.concrete, v.shape = body, body, body
	return v
}

// applyExtractorTest narrows the scrutinee to the constructor's return type and resolves
// the parameters its extract steps read. It is bindExtractorPat's narrowing read off the
// test, with the extracted values left to the projections rather than bound here.
func (b *pathBinder) applyExtractorTest(scope *Scope, node ast.Node, v scrutineeView, t *ucs.ExtractorTest) scrutineeView {
	blame := blameName(t.Name, node)
	name := ast.QualIdentToString(t.Name)
	ctor, ok := b.c.extractorCtor(scope, b.lvl, t.Name)
	if !ok {
		b.c.report(&ExtractorPatternNotCtorError{Node: blame, Name: name})
		return v
	}
	// The extracted value is an instance of the constructor's return type. Narrow the
	// scrutinee to it, the same assertion a class test makes.
	b.c.constrain(blame, ctor.Ret, v.ty)
	// Read the parameters at the scrutinee's concrete arguments by substituting them
	// directly, rather than relying on the narrowing constraint above to back-propagate
	// them.
	params := ctor.Params
	if sc, ok := classCarrier(v.ty); ok {
		if ret, isClass := ctor.Ret.(*soltype.ClassType); isClass && ret.Name == sc.Name {
			params = ctorParamsAt(ctor.Params, ret, sc)
		}
	}
	if t.Arity != len(params) {
		b.c.report(&ExtractorPatternArityError{Node: blame, Name: name, Expected: len(params), Got: t.Arity})
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

// blameName returns the node to blame for a test's class or extractor name, falling back
// to the caller's node. An ast.QualIdent is guaranteed only to carry a span, so a form
// that is not also an ast.Node names nothing a diagnostic can anchor to.
func blameName(qi ast.QualIdent, fallback ast.Node) ast.Node {
	if n, ok := blamableNode(qi); ok {
		return n
	}
	return fallback
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
