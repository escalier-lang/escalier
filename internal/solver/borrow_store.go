package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// A call stores one of its argument-borrows into another argument when the two share a
// lifetime and the shared lifetime sits inside the second parameter's referent. In
//
//	declare fn store<'a, 'b>(target: &'b mut {peer: &'a mut B}, item: &'a mut B)
//
// item's borrow lifetime 'a reappears at target's peer field. Whatever item borrows must
// therefore stay alive for as long as target.peer is readable, which is what "the call
// writes item's borrow into target" means. At the call `store(&mut a, &mut b)` that is the
// edge a → b at [peer], the same edge `val a = {peer: &mut b}` records at its initializer.
//
// A lifetime shared at the two parameters' outermost position is not a store. In
// `fn f<'a>(x: &'a mut B, y: &'a mut B)` the two borrows only share one region; neither
// referent is reachable through the other. The search below skips each candidate target's
// own outer lifetime and looks inside its referent. The stored argument's side has no such
// exemption: any lifetime its type mentions, outermost or nested, names data the callee
// holds and may write into the target.
//
// A signature that only reads through the shared lifetime gets the same treatment as one
// that writes, because the two are spelled alike: a mutable borrow whose referent mentions
// the argument's lifetime may write it there, and the recorder takes the permission at face
// value. That errs toward recording an alias the call might not form, which reports an
// escape the program does not have. The opposite reading would miss a real dangling borrow.
//
// `a.peers.push(&mut b)`, the container case this machinery exists for, is not covered. Four
// pieces are missing: an `Array` type with a method surface, a lifetime parameter on the
// container type tying the element borrow to the receiver, the receiver's own type at the
// call site, which memberValue strips off the signature it hands the call, and named
// lifetimes on a method signature.
//
// That last one is why no method call records a store today, through `self` or through an
// explicit parameter alike. A method signature reaches the call site with its lifetimes
// coalesced to the anonymous display lifetime rather than lifetime variables, so the two
// sides of a store share nothing the search can match.
type storeEdge struct {
	// arg is the index of the argument whose borrow the call stores.
	arg int
	// target is the index of the parameter the borrow is stored into.
	target int
	// path is the field path within the target's referent where the borrow lands, so
	// `&'b mut {peer: &'a mut B}` gives [peer]. A store position the walk reaches but
	// cannot name, such as a tuple element or a class type argument, keeps the path of
	// its container.
	path []placeSeg
}

// callStoreEdges returns the store effects the resolved signature fn declares, one per
// (argument, target, field path) triple.
//
// A target must be a mutable borrow: a shared borrow takes no write, and an owned-mutable
// parameter — a `RefType` with a nil lifetime — is moved into the callee, so what the callee
// writes into it the caller can no longer read. A source must be a borrow too, since an
// owned argument is moved into the callee and its escape belongs to the consuming-argument
// rule.
//
// A signature that declares no store returns none, the common case. Each side is classified
// by type kind alone before any type is walked, so an ordinary call over owned arguments
// costs one pass over the parameter list. Every parameter that is walked is walked once: a
// source's lifetimes are collected before the target loop, and a target's referent is
// searched into a map from lifetime to the paths it sits at rather than once per source
// lifetime.
func callStoreEdges(ctx *Context, fn *soltype.FuncType) []storeEdge {
	var sources, targets []int
	for i, p := range fn.Params {
		ref, isRef := p.Type.(*soltype.RefType)
		if !isRef || ref.Lt == nil {
			continue
		}
		sources = append(sources, i)
		if ref.Mut {
			targets = append(targets, i)
		}
	}
	// One borrow parameter alone declares no store: a store needs a source and a target that
	// are different parameters.
	if len(targets) == 0 || len(sources) < 2 {
		return nil
	}
	sourceLts := map[int][]*soltype.LifetimeVar{}
	for _, i := range sources {
		sourceLts[i] = lifetimeVarsIn(ctx, fn.Params[i].Type)
	}

	var out []storeEdge
	for _, j := range targets {
		dst := fn.Params[j].Type.(*soltype.RefType)
		sites := lifetimeSites(ctx, dst.Inner)
		if len(sites) == 0 {
			continue
		}
		for _, i := range sources {
			if i == j {
				continue
			}
			for _, lv := range sourceLts[i] {
				for _, path := range sites[lv] {
					out = append(out, storeEdge{arg: i, target: j, path: path})
				}
			}
		}
	}
	return out
}

// lifetimeSites maps each lifetime variable occurring in t to the field paths within t it
// occurs at. Reading the map by lifetime is what lets one walk of a target's referent answer
// for every source lifetime.
func lifetimeSites(ctx *Context, t soltype.Type) map[*soltype.LifetimeVar][][]placeSeg {
	sites := map[*soltype.LifetimeVar][][]placeSeg{}
	walkLifetimes(ctx, t, func(lv *soltype.LifetimeVar, path []placeSeg) {
		sites[lv] = append(sites[lv], path)
	})
	return sites
}

// lifetimeVarsIn returns the lifetime variables occurring anywhere in t, deduped by identity
// and ordered as the walk reaches them, which keeps the store edges a signature yields in a
// stable order across runs.
func lifetimeVarsIn(ctx *Context, t soltype.Type) []*soltype.LifetimeVar {
	var out []*soltype.LifetimeVar
	seen := set.NewSet[*soltype.LifetimeVar]()
	walkLifetimes(ctx, t, func(lv *soltype.LifetimeVar, _ []placeSeg) {
		if seen.Contains(lv) {
			return
		}
		seen.Add(lv)
		out = append(out, lv)
	})
	return out
}

// walkLifetimes calls visit once per lifetime-variable occurrence in t, passing the field
// path within t at which it occurs.
func walkLifetimes(ctx *Context, t soltype.Type, visit func(*soltype.LifetimeVar, []placeSeg)) {
	w := &lifetimeWalk{
		ctx:    ctx,
		visit:  visit,
		onPath: set.NewSet[soltype.Type](),
		budget: maxLifetimeWalkNodes,
		fuel:   maxAliasExpansionDepth,
	}
	w.walk(t, nil)
}

// maxLifetimeWalkNodes bounds how many type nodes one walk visits. Alias expansion is what
// makes the bound necessary: a chain of aliases that each name the next one twice describes
// exponentially many field paths, and walking them all takes minutes at a depth a signature
// could plausibly reach. A signature carrying this many type nodes is far past anything
// hand-written.
const maxLifetimeWalkNodes = 1024

// maxAliasExpansionDepth bounds how many alias expansions one root-to-node chain may stack.
// A recursive alias such as `type List<T> = {head: T, tail: List<T>}` expands forever
// otherwise: each expansion substitutes into a fresh body, which the identity-keyed onPath
// set cannot recognize as the one already being walked. Bounding the depth rather than
// refusing a repeated alias NAME is what keeps a re-nesting like `W<W<&'a mut B>>` correct,
// since its two levels are different types and the inner one holds the borrow.
//
// Running out of either limit records fewer store edges, the same silence a kind the walk
// does not descend produces.
const maxAliasExpansionDepth = 8

// lifetimeWalk reports where each lifetime variable occurs in a type, as a field path from
// the type's root. The soltype visitor cannot drive it: that visitor rewrites a type
// bottom-up and hands each node no record of the fields traversed to reach it, and the path
// is the whole point here. recordBorrowSources walks the AST by hand for the same reason.
//
// Three limits keep the walk finite and cheap. onPath holds the types on the current
// root-to-node chain, so a type that refers to itself terminates while a type reached twice
// at two different field paths still yields both. fuel is the alias expansions left on that
// chain, which onPath cannot bound, for the reason maxAliasExpansionDepth gives. budget caps
// the total nodes visited, for the reason maxLifetimeWalkNodes gives.
type lifetimeWalk struct {
	ctx    *Context
	visit  func(*soltype.LifetimeVar, []placeSeg)
	onPath set.Set[soltype.Type]
	budget int
	fuel   int
}

// walk descends t, extending base by a segment at each named field. Only the kinds that can
// hold a borrow reachable by a field path are descended:
//
//   - A borrow reports its own lifetime at base, then descends its referent, so a borrow of
//     a borrow is reached.
//   - An object descends each named property at base plus that name. A member that names no
//     field of its own descends at base, attributing what it holds to the enclosing object,
//     the approximation recordBorrowSources makes for a computed key. walkObjElem says which
//     members those are.
//   - A tuple element, an array element, a class type argument, and a promise or generator
//     payload descend at base unchanged. None of them contributes a name: the place model
//     has no index segment, and neither a class type argument nor a payload is addressable
//     as a field at all. `&'c mut Box<&'a mut B>` therefore stores at the whole Box, which
//     is where a class holding a borrowed element lands.
//   - A union or intersection descends each member at base, since a borrow any member holds
//     is reachable through the whole.
//   - An alias descends its expansion at base. An alias is transparent, so a referent
//     written as `Holder<'a>` is searched the way the object the alias names is.
//
// Any other kind stops the walk, so the call reads as storing nothing through it. One is
// worth naming. A class LIFETIME argument — the `'a` of `Box<'a, T>`, as opposed to the type
// argument this walk does descend — is unreachable because a class declares no lifetime
// parameters, and it wants an arm here once one can be written.
func (w *lifetimeWalk) walk(t soltype.Type, base []placeSeg) {
	if t == nil || w.budget <= 0 || w.onPath.Contains(t) {
		return
	}
	w.budget--
	w.onPath.Add(t)
	defer w.onPath.Remove(t)

	switch t := t.(type) {
	case *soltype.RefType:
		if lv, ok := t.Lt.(*soltype.LifetimeVar); ok {
			w.visit(lv, base)
		}
		w.walk(t.Inner, base)
	case *soltype.ObjectType:
		for _, elem := range t.Elems {
			prop, isProp := elem.(*soltype.PropertyElem)
			if !isProp {
				w.walkObjElem(elem, base)
				continue
			}
			w.walk(prop.Type, appendSeg(base, prop.Name))
		}
	case *soltype.TupleType:
		for _, elem := range t.Elems {
			w.walk(elem, base)
		}
	case *soltype.ArrayType:
		w.walk(t.Elem, base)
	case *soltype.PromiseType:
		w.walk(t.Inner, base)
		w.walk(t.Err, base)
	case *soltype.GeneratorType:
		w.walk(t.Yield, base)
		w.walk(t.Ret, base)
		w.walk(t.Next, base)
	case *soltype.ClassType:
		for _, arg := range t.TypeArgs {
			w.walk(arg, base)
		}
	case *soltype.UnionType:
		for _, member := range t.Types {
			w.walk(member, base)
		}
	case *soltype.IntersectionType:
		for _, member := range t.Types {
			w.walk(member, base)
		}
	case *soltype.AliasType:
		w.walkAlias(t, base)
	}
}

// walkAlias descends an alias reference. A registered alias with a body is walked through its
// expansion alone: expandAlias substitutes the reference's type and lifetime arguments into
// the body, so the expansion already carries each argument at the field path it really sits
// at, and walking the arguments here as well would report a second occurrence at the
// container's path. An alias with no expansion — unregistered, bodyless, or reached with the
// expansion fuel spent — has its arguments attributed to base instead of being dropped.
func (w *lifetimeWalk) walkAlias(t *soltype.AliasType, base []placeSeg) {
	if w.ctx != nil && w.fuel > 0 {
		if def, ok := w.ctx.aliasDef(t.Name); ok && def.Body != nil {
			w.fuel--
			defer func() { w.fuel++ }()
			w.walk(w.ctx.expandAlias(t), base)
			return
		}
	}
	for _, arg := range t.TypeArgs {
		w.walk(arg, base)
	}
	for _, arg := range t.LifetimeArgs {
		if lv, ok := arg.(*soltype.LifetimeVar); ok {
			w.visit(lv, base)
		}
	}
}

// walkObjElem descends an object member that names no field of its own. Three hold data the
// enclosing object exposes, so each is attributed to base: a getter's value, a spread's
// source, and an index signature's value, which covers every key it admits rather than one.
// A method, a setter, and a constructor hold their borrows in a signature the enclosing
// object does not expose as data, so they contribute nothing.
func (w *lifetimeWalk) walkObjElem(elem soltype.ObjTypeElem, base []placeSeg) {
	switch elem := elem.(type) {
	case *soltype.GetterElem:
		w.walk(elem.Type, base)
	case *soltype.SpreadElem:
		w.walk(elem.Type, base)
	case *soltype.MappedElem:
		w.walk(elem.Value, base)
	}
}

// recordCallStoreEdges handles, for the call e against the resolved signature fn, each store
// the signature declares. Where the borrow lands decides what happens, the same split a
// field store makes between recordFieldStoreEdges and checkParamFieldStoreEscape:
//
//   - Into a local, the call records a borrow edge. `store(&mut a, &mut b)` against the
//     store-effect signature in this file's opening comment records a → b at [peer], so a
//     later flow-out of a finds b the way it would had a's initializer written the borrow in
//     directly. The target's own place is the prefix, so `store(&mut a.slot, &mut b)` records
//     at [slot, peer].
//   - Into a parameter, the locals the argument carries are reported at once. The parameter's
//     referent belongs to the caller and outlives the frame, so a borrow of a local written
//     into it dangles. The report is direct rather than deferred to the escape post-pass,
//     because the callee borrows the argument instead of taking it, and the post-pass would
//     weigh an owned-looking argument as a connected-component move and consume it.
//
// A store is skipped when the argument carries no function-local, when the target names no
// binding, or when the two are the same binding, which would make a self-loop no reader of
// the graph can use.
//
// An edge is added to whatever the target already holds rather than replacing it, since a
// signature says where a borrow lands and not whether the callee overwrites what was there.
// A container that accumulates is the shape this models, and keeping the prior edge is the
// sound reading for one that overwrites.
//
// Recording alone does not reach the escape check, which reads the per-program-point graph
// rather than the eager one, so a call that recorded an edge flushes the roots it dirtied
// into this statement's borrowGens, the same handoff a `val` initializer makes.
func (c *checker) recordCallStoreEdges(e *ast.CallExpr, fn *soltype.FuncType, ref liveness.StmtRef) {
	if c.fn == nil || c.fn.eagerBorrowGraph == nil {
		return
	}
	recorded := false
	// A signature can write one argument to several positions in the target, so its escape
	// reaches this loop once per position. Collecting per argument and emitting after the loop
	// keeps one diagnostic per escaping local, blamed on the argument that carries it.
	escaping := map[int]set.Set[liveness.VarID]{}
	for _, edge := range callStoreEdges(c.ctx, fn) {
		if edge.arg >= len(e.Args) || edge.target >= len(e.Args) {
			continue
		}
		referents := c.storedReferents(e.Args[edge.arg])
		if len(referents) == 0 {
			continue
		}
		target, isPlace := exprPlace(borrowOperand(e.Args[edge.target]))
		if !isPlace || target.root <= 0 {
			continue
		}
		if c.fn.paramVarIDs.Contains(target.root) {
			carried, seen := escaping[edge.arg]
			if !seen {
				carried = set.NewSet[liveness.VarID]()
				escaping[edge.arg] = carried
			}
			for _, referent := range referents {
				carried.Add(referent)
			}
			continue
		}
		for _, referent := range referents {
			if target.root == referent {
				continue
			}
			c.addBorrowEdge(target.root, appendPath(target.path, edge.path), referent)
			recorded = true
		}
	}
	for arg := range len(e.Args) {
		if carried, ok := escaping[arg]; ok {
			c.reportEscapingLocals(carried, e.Args[arg])
		}
	}
	if recorded {
		c.flushBorrowDirty(ref)
	}
}

// storedReferents returns the function-locals a stored argument carries. A borrow of a place
// names one: `&mut b`, and a bare `b` passed to a shared-borrow parameter, both carry b. An
// argument that builds its carrier inline, such as `&{inner: &mut b}`, names one per borrow
// the carrier holds, found by the same scan escapingLocalsOf runs over a returned value.
func (c *checker) storedReferents(arg ast.Expr) []liveness.VarID {
	if referent, ok := c.isLocalReferent(borrowOperand(arg)); ok {
		return []liveness.VarID{referent}
	}
	var out []liveness.VarID
	seen := set.NewSet[liveness.VarID]()
	for _, b := range borrowsIn(arg) {
		referent, ok := c.isLocalReferent(b.Arg)
		if !ok || seen.Contains(referent) {
			continue
		}
		seen.Add(referent)
		out = append(out, referent)
	}
	return out
}

// borrowOperand returns the place a borrow argument names: the operand of an explicit
// `&`/`&mut`, or the argument itself when the call auto-borrows a place passed by name.
func borrowOperand(arg ast.Expr) ast.Expr {
	if b, ok := arg.(*ast.BorrowExpr); ok {
		return b.Arg
	}
	return arg
}
