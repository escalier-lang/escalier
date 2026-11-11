package solver

import (
	"maps"
	"slices"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// checkSignatureImpliesBodyLifetimes reports each outlives relation a body imposes between
// two lifetimes its function's own `<…>` list declares, or between one of them and
// 'static, that the declared bounds do not imply. Once a function names its lifetimes, the
// written signature is the contract its callers are checked against, so a relation the
// body needs and the written signature leaves out is one no caller is held to. A lifetime
// the signature leaves unnamed is not checked here, because the relations the body imposes
// on it go into the function's inferred type, where callers do see them.
//
// `fn f<'a, 'b>(x: &'a T, y: &'b T) -> &'b T { return x }` needs 'a to outlive 'b to return
// x at 'b, and reports it. Writing `<'a: 'b, 'b>` declares it. Two lifetimes the body makes
// equal need a bound in each direction. A lifetime the body forces to 'static needs a
// `'a: 'static` bound. Bounds are closed transitively, so `<'a: 'b, 'b: 'c, 'c>` implies
// 'a: 'c.
//
// The signature's own types imply bounds too. A borrow `&'a T` is only well formed when
// every lifetime written inside T outlives 'a, so `p: &'a &'b T` implies 'b: 'a without a
// written bound. Those count as declared.
//
// A relation from one of the signature's lifetimes to a lifetime an enclosing function
// declares is checked the same way. `fn outer<'a>(p: &mut {cb: 'a fn () -> undefined})` with
// `fn <'b>(g: 'b fn () -> undefined) { p.cb = g }` in its body needs 'b to outlive outer's 'a,
// which `<'b: 'a>` declares.
//
// sig is the function's signature and ft its inferred type. outerScope and outerBounds are
// the enclosing functions' lifetimes and their bounds, as enclosingLifetimes and
// enclosingBounds held them before the signature hid the names it binds. outerOwners is
// enclosingOwners at the same point. A lifetime the enclosing class declares is not checked
// here.
func (c *checker) checkSignatureImpliesBodyLifetimes(sig ast.FuncSig, ft *soltype.FuncType, outerScope map[string]*soltype.LifetimeVar, outerBounds map[string]set.Set[string], outerOwners map[string]string) {
	ltParams := sig.LifetimeParams
	named := map[string]*soltype.LifetimeVar{}
	binders := map[string]*ast.LifetimeParam{}
	var order []string
	for _, bound := range c.signatureLifetimeNames(ltParams) {
		named[bound.param.Name] = bound.v
		binders[bound.param.Name] = bound.param
		order = append(order, bound.param.Name)
	}
	if len(order) == 0 {
		return
	}

	// Read the outlives relation the body recorded on the inferred type.
	a, _, _ := ltOutlivesRelation(ft, soltype.Positive)
	if a == nil {
		return
	}
	// requires reads only the directed edges the body recorded. The relation the printer
	// renders also counts every parameter connected to a return-only lifetime as outliving
	// it, which is a display rule and would report a bound the body does not need.
	requires := func(sub, super *soltype.LifetimeVar) bool {
		return a.outlivesGraph.implies(sub.ID, super.ID)
	}
	forcedStatic := func(v *soltype.LifetimeVar) bool {
		return a.outlivesGraph.static.Contains(a.outlivesGraph.repOf(v.ID))
	}

	// Compare every ordered pair of named lifetimes. Each relation the body requires must
	// follow from the written bounds or the bounds the signature's types imply.
	declared := declaredOutlives(ltParams, impliedOutlives(sig))
	for _, sub := range order {
		subVar := named[sub]
		if forcedStatic(subVar) {
			if !declared.static.Contains(sub) {
				c.report(&LifetimeRelationUndeclaredError{Sub: sub, Super: "static", Param: binders[sub]})
			}
			// A lifetime that outlives 'static outlives every other, so each relation the
			// body draws from it follows from the one reported above.
			continue
		}
		for _, super := range order {
			if sub == super || !requires(subVar, named[super]) {
				continue
			}
			if !declared.implies(sub, super) {
				c.report(&LifetimeRelationUndeclaredError{Sub: sub, Super: super, Param: binders[sub]})
			}
		}
	}

	// An enclosing function's lifetime is a fixed lifetime here, which a caller of this function
	// does not choose. So each relation the body requires from one of the signature's lifetimes
	// to it has to be declared too. A name the signature binds itself hides the enclosing
	// lifetime of that name, which then cannot be named in a bound.
	// A bound on an enclosing lifetime also covers each one that lifetime's function declares
	// or implies it outlives.
	own := set.NewSet[string]()
	for _, p := range ltParams {
		own.Add(p.Name)
	}
	covers := func(sub, super string) bool {
		if declared.implies(sub, super) {
			return true
		}
		for via := range declared.outlives[sub] {
			if !own.Contains(via) && outerBounds[via].Contains(super) {
				return true
			}
		}
		return false
	}
	outerNames := slices.Sorted(maps.Keys(outerScope))
	for _, sub := range order {
		if forcedStatic(named[sub]) {
			continue
		}
		reach := set.FromSlice(outlivedBy(named[sub]))
		for _, name := range outerNames {
			if !reach.Contains(outerScope[name]) {
				continue
			}
			shadowed := own.Contains(name)
			if shadowed || !covers(sub, name) {
				c.report(&LifetimeRelationUndeclaredError{Sub: sub, Super: name, Shadowed: shadowed, Owner: outerOwners[name], Param: binders[sub]})
			}
		}
	}
}

// signatureLifetime is one name a signature's `<…>` list binds, with the variable the
// signature's positions interned under it.
type signatureLifetime struct {
	param *ast.LifetimeParam
	v     *soltype.LifetimeVar
}

// signatureLifetimeNames returns the names own binds, in declaration order, each with its
// variable. An entry binds nothing and is left out when it is `'static`, when an earlier
// entry bound the same name, or when the signature never wrote the name and so interned no
// variable for it. checkLifetimeDeclarations reports the last two. It reads the signature's
// own named-lifetime scope, so it runs while that scope is installed.
func (c *checker) signatureLifetimeNames(own []*ast.LifetimeParam) []signatureLifetime {
	var out []signatureLifetime
	seen := set.NewSet[string]()
	for _, p := range own {
		if p.Name == "static" || seen.Contains(p.Name) {
			continue
		}
		seen.Add(p.Name)
		v, written := c.namedLifetimes[p.Name]
		if !written {
			continue
		}
		out = append(out, signatureLifetime{param: p, v: v})
	}
	return out
}

// declaredLtBounds is the outlives relation a `<…>` list declares, closed transitively.
type declaredLtBounds struct {
	// outlives maps a name to the names it is declared to outlive, directly or through a
	// chain of bounds.
	outlives map[string]set.Set[string]
	// static holds the names declared to outlive 'static, directly or through a chain.
	static set.Set[string]
}

// implies reports whether the declared bounds make sub outlive super.
func (d declaredLtBounds) implies(sub, super string) bool {
	if d.static.Contains(sub) {
		return true
	}
	reach, ok := d.outlives[sub]
	return ok && reach.Contains(super)
}

// declaredOutlives closes the bounds ltParams declares, together with the implied bounds in
// implied, into the relation each name outlives. implied maps a name to the names it
// outlives directly.
func declaredOutlives(ltParams []*ast.LifetimeParam, implied map[string][]string) declaredLtBounds {
	// Gather the direct edges from the implied bounds and the written ones.
	direct := map[string][]string{}
	for name, supers := range implied {
		direct[name] = append(direct[name], supers...)
	}
	for _, p := range ltParams {
		for _, b := range p.Bounds {
			direct[p.Name] = append(direct[p.Name], b.Name)
		}
	}
	// Walk the edges from each name to find every name it reaches. A name that reaches
	// 'static is recorded as outliving it.
	d := declaredLtBounds{outlives: map[string]set.Set[string]{}, static: set.NewSet[string]()}
	for name := range direct {
		reach := set.NewSet[string]()
		work := append([]string(nil), direct[name]...)
		for len(work) > 0 {
			next := work[len(work)-1]
			work = work[:len(work)-1]
			if reach.Contains(next) {
				continue
			}
			reach.Add(next)
			work = append(work, direct[next]...)
		}
		if reach.Contains("static") {
			d.static.Add(name)
		}
		d.outlives[name] = reach
	}
	return d
}

// impliedOutlives returns the bounds sig's parameter and return annotations imply by being
// well formed, as a map from a name to the names it directly outlives. Each `&'a T` makes
// every lifetime written inside T outlive 'a.
func impliedOutlives(sig ast.FuncSig) map[string][]string {
	v := &impliedOutlivesCollector{out: map[string][]string{}}
	for _, p := range sig.Params {
		if p.TypeAnn != nil {
			p.TypeAnn.Accept(v)
		}
	}
	if sig.Return != nil {
		sig.Return.Accept(v)
	}
	return v.out
}

// impliedOutlivesCollector records, for each named borrow it visits, an edge from every
// lifetime its borrowed type writes to the borrow's own lifetime. It does not descend
// into a nested function annotation.
type impliedOutlivesCollector struct {
	ast.DefaultVisitor
	out map[string][]string
}

func (v *impliedOutlivesCollector) EnterTypeAnn(t ast.TypeAnn) bool {
	switch n := t.(type) {
	case *ast.RefTypeAnn:
		// Only a borrow with a written lifetime other than 'static implies a bound.
		outer, ok := n.Lifetime.(*ast.LifetimeAnn)
		if !ok || outer.Name == "static" || n.Inner == nil {
			return true
		}
		// Every lifetime the borrowed type writes must outlive the borrow's lifetime.
		var inner lifetimeUseCollector
		n.Inner.Accept(&inner)
		for _, u := range append(inner.uses, inner.refArgs...) {
			if u.Name != outer.Name {
				v.out[u.Name] = append(v.out[u.Name], outer.Name)
			}
		}
		// Keep descending, since a borrow nested inside implies bounds of its own.
		return true
	case *ast.FuncTypeAnn:
		// A function annotation starts its own lifetime scope, so a name written inside it
		// does not refer to the enclosing signature's lifetime of the same name. The walk
		// would stop here even if it did, because a borrow inside a function annotation
		// implies nothing. No value of that borrow's type has to exist. `cb: fn(x: &'a &'b T)`
		// passes a callback that accepts such a value, so nothing forces 'b to outlive 'a.
		return false
	}
	return true
}
