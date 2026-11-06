package solver

import (
	"slices"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// This file applies the rules of D16 in planning/ownership_rules/requirements.md to the
// lifetimes of one function body:
//
//  1. A borrow of a place that already has a borrow of the same kind outliving the current
//     frame takes that borrow's lifetime. The kind is immutable or mutable.
//  2. A returned borrow is owned when its lifetime ends with the function's frame, meaning it
//     is not tied to a parameter, a capture, or `'static`.
//  3. A borrow nested in a returned value is owned when its lifetime ends with the frame and
//     no other borrow in the returned value reaches an overlapping place.
//
// The frame is one call of the function whose body is walked. The rules use these terms:
//
//   - A lifetime is from outside the frame when the walk of the body did not mint it. A
//     parameter's lifetime, a capture's, and a module-level binding's are all minted before
//     the body is walked. A lifetime at a level outside the body's is from outside the frame
//     too, since it belongs to an enclosing body.
//   - A lifetime outlives the frame when it is `'static`, is from outside the frame, or must
//     outlive one that does. A lifetime's upper bounds are the lifetimes it must outlive. A
//     borrow stored into a captured binding outlives the frame too, since the binding belongs
//     to an enclosing body.
//   - A lifetime is foreign when the value it borrows may be one that something outside the
//     frame reaches, even though the body minted it. A borrow of a parameter or a captured
//     place is foreign, and so is a borrow read from a captured or module-level binding. So
//     is a call result whose lifetime the callee's signature ties to an argument, since the
//     callee may return any value that outlives that argument.
//   - A lifetime ends with the frame when nothing outside the frame can reach the value it
//     borrows. Neither it nor any lifetime flowing into it may be foreign or outlive the
//     frame. A lifetime's lower bounds are the lifetimes flowing into it.
//
// In this program the borrow stored in `cache` outlives the frame, so the returned `&b` takes
// its lifetime under the first rule. The return then does not end with the frame, and `h`
// infers `fn () -> &'static {x: 1}` rather than an owned return:
//
//	var cache: &{x: number} = &{x: 0}
//
//	fn h() {
//		val b = {x: 1}
//		cache = &b
//		return &b
//	}

// frameLifetimes is what D16's rules read about the lifetimes of the body being walked.
type frameLifetimes struct {
	// mark is the lifetime counter when the walk of the body began. A lifetime with a lower
	// ID is from outside the frame.
	mark int
	// `borrows` holds each borrow of a local place the body takes, in source order.
	borrows []frameBorrow
	// foreign holds the foreign lifetimes the body minted. It is nil until the first one.
	foreign set.Set[*soltype.LifetimeVar]
	// outliving holds the lifetimes the body minted that outlive the frame because a binding
	// of an enclosing body holds their borrows. It is nil until the first one.
	outliving set.Set[*soltype.LifetimeVar]
}

// frameBorrow is one borrow of a local place and the lifetime its type carries. `mut` is set
// for a `&mut`.
type frameBorrow struct {
	place movePlace
	lt    *soltype.LifetimeVar
	mut   bool
}

// startFrameLifetimes marks every lifetime minted so far as from outside the frame of the
// current body. It must run before the body is walked and after its parameters are typed.
func (c *checker) startFrameLifetimes() {
	c.fn.frame.mark = c.ctx.lifetimeCounter
}

// markForeign records lv as a foreign lifetime of the current body.
func (c *checker) markForeign(lv *soltype.LifetimeVar) {
	if c.fn.frame.foreign == nil {
		c.fn.frame.foreign = set.NewSet[*soltype.LifetimeVar]()
	}
	c.fn.frame.foreign.Add(lv)
}

// markOutlivesFrame records that each lifetime the body minted in t outlives the frame. t types
// a value that a binding of an enclosing body now holds.
func (c *checker) markOutlivesFrame(t soltype.Type) {
	if c.fn.node == nil {
		return
	}
	for _, lv := range heldLifetimes(t) {
		if c.fromOutsideFrame(lv) {
			continue
		}
		if c.fn.frame.outliving == nil {
			c.fn.frame.outliving = set.NewSet[*soltype.LifetimeVar]()
		}
		c.fn.frame.outliving.Add(lv)
	}
}

// fromOutsideFrame reports whether lv is from outside the frame of the current body.
func (c *checker) fromOutsideFrame(lv *soltype.LifetimeVar) bool {
	return lv.ID < c.fn.frame.mark || lv.Level < c.fn.lvl
}

// noteFrameBorrow records the borrow e for the rules. It must run once e's type is recorded.
// Only a lifetime the body minted is recorded, so a borrow that reuses a parameter's lifetime
// adds nothing. `&p.x` for `p: &{x: number}` is one such borrow.
//
//   - A borrow of a local place joins the borrows the first and third rules read. A parameter
//     is such a place, and a borrow of one is foreign as well, since the caller may still hold
//     the value.
//   - An immutable borrow of a module-level binding reads a value the module keeps for the
//     rest of the program, so its lifetime must outlive `'static`. A `&mut` of one is foreign
//     instead. E2 reads a `'static` mutable borrow as live at every later point, so making
//     `&mut cfg` `'static` would reject every later `&cfg` in the body.
//   - A borrow of a captured place is foreign.
func (c *checker) noteFrameBorrow(scope *Scope, e *ast.BorrowExpr) {
	if c.fn == nil || c.fn.node == nil {
		return
	}
	lv, ok := borrowLifetime(c.info.TypeOf(e))
	if !ok || c.fromOutsideFrame(lv) {
		return
	}
	p, isPlace := c.exprPlace(e.Arg)
	if isPlace && !c.fn.captureVarIDs.Contains(p.root) {
		c.fn.frame.borrows = append(c.fn.frame.borrows, frameBorrow{place: p, lt: lv, mut: e.Mut})
		if c.fn.paramVarIDs.Contains(p.root) {
			c.markForeign(lv)
		}
		return
	}
	root, ok := placeRootIdent(e.Arg)
	if !ok || (!isPlace && c.varIDs.Of(root) >= 0) {
		return
	}
	b, found := c.lookupValueBinding(scope, root.Name)
	if !found {
		return
	}
	if b.ModuleLevel && !e.Mut {
		c.ctx.constrainLt(lv, soltype.Static)
		return
	}
	c.markForeign(lv)
}

// noteOuterRead marks foreign each lifetime the read e copied into its type t when e names a
// captured or module-level binding. That binding still holds the borrows t holds once the
// frame ends.
func (c *checker) noteOuterRead(e *ast.IdentExpr, t soltype.Type) {
	if c.fn == nil || c.fn.node == nil {
		return
	}
	if id := c.varIDs.Of(e); id > 0 && !c.fn.captureVarIDs.Contains(id) {
		return
	}
	for _, lv := range heldLifetimes(t) {
		if !c.fromOutsideFrame(lv) {
			c.markForeign(lv)
		}
	}
}

// borrowLifetime returns the lifetime variable of the borrow t types. A union holding a borrow
// and `undefined`, the type of a borrow of an optional field, returns the borrow's.
func borrowLifetime(t soltype.Type) (*soltype.LifetimeVar, bool) {
	switch t := t.(type) {
	case *soltype.RefType:
		lv, ok := t.Lt.(*soltype.LifetimeVar)
		return lv, ok
	case *soltype.UnionType:
		for _, member := range t.Types {
			if lv, ok := borrowLifetime(member); ok {
				return lv, true
			}
		}
	}
	return nil, false
}

// placeRootIdent returns the identifier a chain of member and index reads starts from, `a`
// for `a.b[0].c`. ok is false when the chain starts from anything else, such as a call.
func placeRootIdent(e ast.Expr) (*ast.IdentExpr, bool) {
	for {
		switch x := e.(type) {
		case *ast.IdentExpr:
			return x, true
		case *ast.MemberExpr:
			e = x.Object
		case *ast.IndexExpr:
			e = x.Object
		default:
			return nil, false
		}
	}
}

// shareOutlivingBorrowLifetimes applies the first rule to every borrow the body took. Each
// borrow of a place that overlaps the place of a borrow of the same kind outliving the frame
// must outlive what that borrow outlives, so it outlives the frame as well. That can in turn
// reach a borrow of a third place, so the walk continues until no borrow is added.
//
// A borrow of the other kind is left alone. E2 already rejects it while the outliving borrow
// is live, so tying the two adds nothing a program can rely on.
//
// The order of the borrows in the source does not matter. A lifetime covers every point its
// borrow is used at, so a `&b` taken before `cache = &b` still reads the value `cache` keeps
// reading.
//
// It must run after the body is walked and before commitOwnedReturns reads the lifetimes.
func (c *checker) shareOutlivingBorrowLifetimes() {
	borrows := c.fn.frame.borrows
	witnesses := make([]soltype.Lifetime, len(borrows))
	var queue []int
	for i, b := range borrows {
		if w, ok := c.outlivingWitness(b.lt); ok {
			witnesses[i] = w
			queue = append(queue, i)
		}
	}
	for len(queue) > 0 {
		i := queue[0]
		queue = queue[1:]
		for j, b := range borrows {
			if b.mut != borrows[i].mut || !placesOverlap(b.place, borrows[i].place) {
				continue
			}
			c.ctx.constrainLt(b.lt, witnesses[i])
			if witnesses[j] == nil {
				witnesses[j] = witnesses[i]
				queue = append(queue, j)
			}
		}
	}
}

// outlivesFrame reports whether lt outlives the frame of the current body.
func (c *checker) outlivesFrame(lt soltype.Lifetime) bool {
	_, ok := c.outlivingWitness(lt)
	return ok
}

// outlivingWitness returns a lifetime lt must outlive that shows lt outlives the frame of the
// current body: `'static`, a lifetime from outside the frame, or one the body recorded as
// outliving the frame. ok is false when lt does not outlive the frame. The walk follows upper
// bounds and the links between a lifetime and its copies.
func (c *checker) outlivingWitness(lt soltype.Lifetime) (soltype.Lifetime, bool) {
	seen := set.NewSet[*soltype.LifetimeVar]()
	var walk func(soltype.Lifetime) (soltype.Lifetime, bool)
	walk = func(l soltype.Lifetime) (soltype.Lifetime, bool) {
		lv, ok := l.(*soltype.LifetimeVar)
		if !ok {
			return l, soltype.IsStaticLifetime(l)
		}
		if c.fromOutsideFrame(lv) || c.fn.frame.outliving.Contains(lv) {
			return lv, true
		}
		if seen.Contains(lv) {
			return nil, false
		}
		seen.Add(lv)
		for _, next := range slices.Concat(lv.UpperBounds, c.frameCopies(lv)) {
			if w, ok := walk(next); ok {
				return w, true
			}
		}
		return nil, false
	}
	return walk(lt)
}

// frameCopies returns the lifetimes ltCopies links to lv that the current body minted. A copy
// and the lifetime it copies borrow one value, so the rules read them together. A link to a
// lifetime from outside the frame is left out, since that one is a quantified lifetime of a
// signature the body instantiated, such as the callee's at a call.
func (c *checker) frameCopies(lv *soltype.LifetimeVar) []soltype.Lifetime {
	var out []soltype.Lifetime
	for _, linked := range c.ctx.ltCopies[lv] {
		if !c.fromOutsideFrame(linked) {
			out = append(out, linked)
		}
	}
	return out
}

// lifetimeEndsWithFrame reports whether lt ends with the frame of the current body. The walk
// follows lower bounds and the links between a lifetime and its copies.
func (c *checker) lifetimeEndsWithFrame(lt soltype.Lifetime) bool {
	seen := set.NewSet[*soltype.LifetimeVar]()
	var walk func(soltype.Lifetime) bool
	walk = func(l soltype.Lifetime) bool {
		lv, ok := l.(*soltype.LifetimeVar)
		if !ok {
			return false
		}
		if seen.Contains(lv) {
			return true
		}
		seen.Add(lv)
		if c.fn.frame.foreign.Contains(lv) || c.outlivesFrame(lv) {
			return false
		}
		for _, next := range slices.Concat(lv.LowerBounds, c.frameCopies(lv)) {
			if !walk(next) {
				return false
			}
		}
		return true
	}
	return walk(lt)
}

// markTiedCallLifetimes marks foreign each lifetime of the call's result that the callee's
// signature ties to an argument or the receiver. mark is the lifetime counter before the call
// was inferred, so a lifetime with a lower ID existed before the call and is not the call's
// own. fn is the signature the arguments were applied to. recv is the receiver expression and
// self the method's `self` parameter, and both are nil for a call with no receiver. args holds
// the argument types and ret is the call's result.
//
// A lifetime is tied when it occurs in one of fn's parameters, `self`, the arguments, or the
// receiver, or reaches one through the bounds of lifetimes the call minted. With
// `declare fn lookup<'a>(p: &'a {x: number}) -> &'a {x: number}`, the result of `lookup(&b)`
// carries the copy of `'a` the parameter carries, so it is tied. With
// `declare fn make(p: &{x: number}) -> &{x: number}` the result's lifetime reaches nothing.
// L2 keeps a body under that signature from returning `p`, so nothing outside the call
// reaches the result and it is not foreign.
func (c *checker) markTiedCallLifetimes(mark int, fn *soltype.FuncType, recv ast.Expr, self *soltype.FuncParam, args, ret soltype.Type) {
	if c.fn == nil || c.fn.node == nil || fn == nil {
		return
	}
	tied := set.NewSet[*soltype.LifetimeVar]()
	addAll := func(t soltype.Type) {
		for _, lv := range reachableLifetimes(t) {
			tied.Add(lv)
		}
	}
	for _, p := range fn.Params {
		addAll(p.Type)
	}
	for _, sp := range []*soltype.FuncParam{fn.SelfParam, self} {
		if sp != nil {
			addAll(sp.Type)
		}
	}
	addAll(args)
	if recv != nil {
		addAll(c.info.TypeOf(recv))
	}
	for _, lv := range reachableLifetimes(ret) {
		if lv.ID >= mark && reachesTied(lv, tied, mark) {
			c.markForeign(lv)
		}
	}
}

// reachesTied reports whether lv is in tied or reaches a member of tied through the lower and
// upper bounds of lifetimes with an ID of at least mark.
func reachesTied(lv *soltype.LifetimeVar, tied set.Set[*soltype.LifetimeVar], mark int) bool {
	seen := set.NewSet[*soltype.LifetimeVar]()
	queue := []*soltype.LifetimeVar{lv}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if tied.Contains(cur) {
			return true
		}
		if seen.Contains(cur) || cur.ID < mark {
			continue
		}
		seen.Add(cur)
		for _, b := range slices.Concat(cur.LowerBounds, cur.UpperBounds) {
			if next, ok := b.(*soltype.LifetimeVar); ok {
				queue = append(queue, next)
			}
		}
	}
	return false
}

// reachableLifetimes returns the lifetime variables lifetimeCollector finds in t, and those it
// finds in the bounds of each type variable t holds.
func reachableLifetimes(t soltype.Type) []*soltype.LifetimeVar {
	return collectLifetimes(t, true)
}

// heldLifetimes returns the lifetimes of the borrows a value of type t holds. It is
// reachableLifetimes without the parameters and return of a function type, which describe the
// function's calls rather than anything the value holds.
func heldLifetimes(t soltype.Type) []*soltype.LifetimeVar {
	return collectLifetimes(t, false)
}

// collectLifetimes returns the lifetimes reachableLifetimes returns, leaving out those under a
// function type's parameters and return when intoFuncs is false.
func collectLifetimes(t soltype.Type, intoFuncs bool) []*soltype.LifetimeVar {
	if t == nil {
		return nil
	}
	v := &boundLifetimeCollector{
		lifetimeCollector: lifetimeCollector{out: set.NewSet[*soltype.LifetimeVar]()},
		vars:              set.NewSet[*soltype.TypeVarType](),
		intoFuncs:         intoFuncs,
	}
	t.Accept(v, soltype.Positive)
	return v.order
}

// boundLifetimeCollector is a lifetimeCollector that also descends into the bounds of each
// type variable it reaches. It descends into a function type's parameters and return only
// when intoFuncs is set.
type boundLifetimeCollector struct {
	lifetimeCollector
	vars      set.Set[*soltype.TypeVarType]
	intoFuncs bool
}

func (v *boundLifetimeCollector) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	switch t := t.(type) {
	case *soltype.TypeVarType:
		if !v.vars.Contains(t) {
			v.vars.Add(t)
			for _, b := range slices.Concat(t.LowerBounds, t.UpperBounds) {
				b.Accept(v, pol)
			}
		}
	case *soltype.FuncType:
		if !v.intoFuncs {
			v.add(t.Lt)
			return soltype.EnterResult{Type: t, SkipChildren: true}
		}
	}
	return v.lifetimeCollector.EnterType(t, pol)
}

// frameOwnedReturn returns the owned type the second and third rules give a return of type t,
// and false when they own none of its borrows. ann is the function's return annotation, or nil
// when it has none.
//
//   - A borrow is owned as its referent when ownedBorrows decides it. Its referent is walked
//     the same way, so `return {p: &mut b}` owns both the object and the borrow of `b`.
//   - A type variable is owned as the union of its referents when every type that flows into
//     it is a borrow the rules own. With `declare fn make(p: &{x: number}) -> &{x: number}`,
//     the binding `r` in `val r = make(&b); return r` is one.
//   - A borrow at a position ann writes as a `&mut` is kept, since an owned value does not
//     check against a `&mut` annotation.
//   - A borrow that is a member of a union return is kept.
func (c *checker) frameOwnedReturn(t, ann soltype.Type) (soltype.Type, bool) {
	if v, ok := t.(*soltype.TypeVarType); ok {
		if annotatesMutBorrow(ann) {
			return nil, false
		}

		refs, ok := refLowerBounds(v, set.NewSet[*soltype.TypeVarType]())
		if !ok || len(refs) == 0 {
			return nil, false
		}

		inners := make([]soltype.Type, 0, len(refs))
		for _, r := range refs {
			owned, ok := c.frameOwnedReturn(r, ann)
			// A borrow kept at the top comes back as itself.
			if !ok || owned == soltype.Type(r) {
				return nil, false
			}
			inners = append(inners, owned)
		}

		if len(inners) == 1 {
			return inners[0], true
		}
		return newUnion(nil, inners), true
	}

	var found []returnedBorrow
	var open []int
	rewriteBorrows(t,
		func(r *soltype.RefType) bool {
			open = append(open, len(found))
			found = append(found, returnedBorrow{ref: r})
			return false
		},
		func() {
			last := len(open) - 1
			found[open[last]].end = len(found)
			open = open[:last]
		},
	)

	kept := set.NewSet[*soltype.RefType]()
	keepMutAnnotated(t, ann, kept)
	owned := c.ownedBorrows(found, kept)

	// A read of a `val` copies its borrow's lifetime before the first rule ties the borrow to
	// the one it shares a place with. Tying each kept copy too makes the signature render what
	// the borrow outlives, `&'static` for a value module-level state also reads.
	for i, b := range found {
		if !owned[i] {
			c.tieToOutlivingWitness(b.ref)
		}
	}

	// A borrow of an optional field is typed as a union with `undefined`, and rewriteBorrows
	// does not enter a union. Each borrow member of a union return is kept, so it is tied the
	// same way.
	if u, ok := t.(*soltype.UnionType); ok {
		for _, m := range u.Types {
			if r, ok := m.(*soltype.RefType); ok && r.Lt != nil {
				c.tieToOutlivingWitness(r)
			}
		}
	}

	if !slices.Contains(owned, true) {
		return nil, false
	}

	i := 0
	return rewriteBorrows(t,
		func(*soltype.RefType) bool {
			i++
			return owned[i-1]
		},
		func() {},
	), true
}

// returnedBorrow is one borrow in a returned value. The borrows of the value are listed in the
// order rewriteBorrows visits them, and those in `ref`'s referent are the ones from `ref`'s
// own index up to `end`.
type returnedBorrow struct {
	ref *soltype.RefType
	end int
}

// annotatesMutBorrow reports whether the annotation ann is a `&mut`.
func annotatesMutBorrow(ann soltype.Type) bool {
	r, ok := ann.(*soltype.RefType)
	return ok && r.Mut && r.Lt != nil
}

// keepMutAnnotated adds to `kept` each borrow in t at a position the annotation ann writes as a
// `&mut`. It walks t and ann together through borrows, the properties of objects matched by
// name, and the elements of tuples matched by index, and stops where the two differ in shape.
func keepMutAnnotated(t, ann soltype.Type, kept set.Set[*soltype.RefType]) {
	switch t := t.(type) {
	case *soltype.RefType:
		a, ok := ann.(*soltype.RefType)
		if !ok {
			return
		}
		if annotatesMutBorrow(a) {
			kept.Add(t)
		}
		keepMutAnnotated(t.Inner, a.Inner, kept)
	case *soltype.ObjectType:
		a, ok := ann.(*soltype.ObjectType)
		if !ok {
			return
		}
		for _, e := range t.Elems {
			p, ok := e.(*soltype.PropertyElem)
			if !ok {
				continue
			}
			for _, ae := range a.Elems {
				if ap, ok := ae.(*soltype.PropertyElem); ok && ap.Name == p.Name {
					keepMutAnnotated(p.Type, ap.Type, kept)
				}
			}
		}
	case *soltype.TupleType:
		a, ok := ann.(*soltype.TupleType)
		if !ok {
			return
		}
		for i, e := range t.Elems {
			if i < len(a.Elems) {
				keepMutAnnotated(e, a.Elems[i], kept)
			}
		}
	}
}

// tieToOutlivingWitness constrains the lifetime of r to outlive a lifetime outlivingWitness
// finds for it, and does nothing when r's lifetime does not outlive the frame.
func (c *checker) tieToOutlivingWitness(r *soltype.RefType) {
	if w, ok := c.outlivingWitness(r.Lt); ok {
		c.ctx.constrainLt(r.Lt, w)
	}
}

// ownedBorrows reports, for each borrow of one returned value, whether the rules own it. A
// borrow in `kept` is never owned. Any other borrow is owned when all of these hold:
//
//   - Its lifetime ends with the frame.
//   - No borrow the body took of a place it reaches outlives the frame, whatever its kind. A
//     borrow of the other kind is an E2 error already, and keeping the return a borrow leaves
//     that error the only one.
//   - No other borrow in the value reaches a place overlapping one it reaches. A borrow reaches
//     the places it borrows and those the borrows in its referent reach. A borrow and one in
//     its referent are not compared, since the referent is reached only through the borrow.
//
// In `return {l: &mut b, r: &mut b}` both borrows reach `b`, so the caller gets two paths to one
// value and both stay borrows. An owned value has one owner, so owning either would hide the
// other path.
func (c *checker) ownedBorrows(borrows []returnedBorrow, kept set.Set[*soltype.RefType]) []bool {
	places := make([][]movePlace, len(borrows))
	for i, b := range borrows {
		places[i] = c.borrowedPlaces(b.ref.Lt)
	}
	reach := make([][]movePlace, len(borrows))
	for i, b := range borrows {
		reach[i] = slices.Concat(places[i:b.end]...)
	}
	owned := make([]bool, len(borrows))
	for i, b := range borrows {
		if kept.Contains(b.ref) || !c.lifetimeEndsWithFrame(b.ref.Lt) || c.reachesOutlivingBorrow(reach[i]) {
			continue
		}
		owned[i] = true
		for j, other := range borrows {
			nested := (i < j && j < b.end) || (j < i && i < other.end)
			if j != i && !nested && anyPlacesOverlap(reach[i], reach[j]) {
				owned[i] = false
				break
			}
		}
	}
	return owned
}

// reachesOutlivingBorrow reports whether a borrow the body took of a place overlapping one of
// `places` outlives the frame.
func (c *checker) reachesOutlivingBorrow(places []movePlace) bool {
	for _, b := range c.fn.frame.borrows {
		if anyPlacesOverlap([]movePlace{b.place}, places) && c.outlivesFrame(b.lt) {
			return true
		}
	}
	return false
}

// anyPlacesOverlap reports whether a place in `as` overlaps a place in `bs`.
func anyPlacesOverlap(as, bs []movePlace) bool {
	for _, a := range as {
		for _, b := range bs {
			if placesOverlap(a, b) {
				return true
			}
		}
	}
	return false
}

// borrowedPlaces returns the places of the frame borrows whose lifetimes flow into lt. The walk
// follows lower bounds and the links between a lifetime and its copies, as
// lifetimeEndsWithFrame does, so a read of `val t = &mut b` borrows `b`.
func (c *checker) borrowedPlaces(lt soltype.Lifetime) []movePlace {
	byLifetime := map[*soltype.LifetimeVar][]movePlace{}
	for _, b := range c.fn.frame.borrows {
		byLifetime[b.lt] = append(byLifetime[b.lt], b.place)
	}
	var out []movePlace
	seen := set.NewSet[*soltype.LifetimeVar]()
	var walk func(soltype.Lifetime)
	walk = func(l soltype.Lifetime) {
		lv, ok := l.(*soltype.LifetimeVar)
		if !ok || seen.Contains(lv) {
			return
		}
		seen.Add(lv)
		out = append(out, byLifetime[lv]...)
		for _, next := range slices.Concat(lv.LowerBounds, c.frameCopies(lv)) {
			walk(next)
		}
	}
	walk(lt)
	return out
}

// rewriteBorrows returns t with each borrow `strip` accepts replaced by its referent. It
// visits every borrow in t in one fixed order, the referent of each borrow included, and calls
// `strip` once on each, so two walks of one type see the same sequence. It calls `leave` once
// the borrow's referent is walked. A borrow `strip` rejects is kept as it is, with its referent
// unchanged. The walk descends into objects, tuples and borrows, and keeps any other type as
// it is.
func rewriteBorrows(t soltype.Type, strip func(*soltype.RefType) bool, leave func()) soltype.Type {
	return t.Accept(&borrowRewriter{strip: strip, leave: leave}, soltype.Positive)
}

// borrowRewriter is the visitor rewriteBorrows walks a type with.
type borrowRewriter struct {
	strip func(*soltype.RefType) bool
	leave func()
}

func (v *borrowRewriter) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	switch t := t.(type) {
	case *soltype.RefType:
		// A borrow with no lifetime is an owned-mutable cell, not a borrow.
		if t.Lt == nil {
			return soltype.EnterResult{SkipChildren: true}
		}
		stripped := v.strip(t)
		// The referent is walked even when the borrow is kept, so `strip` sees every borrow.
		inner := t.Inner.Accept(v, pol)
		v.leave()
		if !stripped {
			return soltype.EnterResult{SkipChildren: true}
		}
		return soltype.EnterResult{Type: inner, SkipChildren: true}
	case *soltype.ObjectType:
		// A residual object has no settled property list to walk.
		return soltype.EnterResult{SkipChildren: soltype.HasResidualElem(t.Elems)}
	case *soltype.TupleType:
		return soltype.EnterResult{}
	}
	return soltype.EnterResult{SkipChildren: true}
}

func (v *borrowRewriter) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type {
	return t
}

// refLowerBounds returns the borrows that flow into v, reading through the type variables
// among its lower bounds. ok is false when a lower bound is neither a borrow nor a type
// variable.
func refLowerBounds(v *soltype.TypeVarType, seen set.Set[*soltype.TypeVarType]) ([]*soltype.RefType, bool) {
	if seen.Contains(v) {
		return nil, true
	}
	seen.Add(v)
	var out []*soltype.RefType
	for _, lb := range v.LowerBounds {
		switch lb := lb.(type) {
		case *soltype.RefType:
			out = append(out, lb)
		case *soltype.TypeVarType:
			refs, ok := refLowerBounds(lb, seen)
			if !ok {
				return nil, false
			}
			out = append(out, refs...)
		default:
			return nil, false
		}
	}
	return out, true
}

// mayBeBorrowed reports whether t could still be a borrow when the union of a function's
// returns is built. A `RefType` carrying a lifetime is one outright. A type variable is one
// too, since what it resolves to is not settled while the rewrites are decided. `return id(p)`
// records such a variable. Treating an unsettled return as owned would let a rewrite through
// and build the mixed union commitOwnedReturnTypes exists to prevent.
func mayBeBorrowed(t soltype.Type) bool {
	switch t := t.(type) {
	case *soltype.RefType:
		return t.Lt != nil
	case *soltype.TypeVarType:
		return true
	}
	return false
}

// commitOwnedReturns writes the owned type the rules of D16 give each return with
// commitOwnedReturnTypes. It must run after shareOutlivingBorrowLifetimes, which settles the
// lifetimes it reads.
func (c *checker) commitOwnedReturns() {
	var ownedReturns map[int]soltype.Type
	for i, t := range c.fn.returns {
		if owned, ok := c.frameOwnedReturn(t, c.fn.returnAnn); ok {
			if ownedReturns == nil {
				ownedReturns = map[int]soltype.Type{}
			}
			ownedReturns[i] = owned
		}
	}
	c.commitOwnedReturnTypes(ownedReturns)
}

// commitOwnedReturnTypes writes every collected rewrite onto the function's return types, or
// writes none of them.
//
// A function's returns are unioned, and Escalier rejects a union that mixes an owned member
// with a borrowed one. Rewriting only some of them builds exactly that. In
//
//	fn f(p: &mut B, cond: boolean) {
//		val mut b = {value: 0}
//		if cond { return &mut b }
//		return p
//	}
//
// only the first return is owned, since a borrow of a parameter does not end with the frame.
// Owning that one and leaving `return p` borrowed would union `B` with `&'a mut B` and reject
// the whole function. Holding the rewrite back leaves both borrowed, which is uniform and
// checks.
func (c *checker) commitOwnedReturnTypes(ownedReturns map[int]soltype.Type) {
	if len(ownedReturns) == 0 {
		return
	}
	for i, t := range c.fn.returns {
		if _, rewritten := ownedReturns[i]; rewritten {
			continue
		}
		if mayBeBorrowed(t) {
			return
		}
	}
	for i, t := range ownedReturns {
		c.fn.returns[i] = t
	}
}
