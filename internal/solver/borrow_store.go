package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// A call stores one argument's borrow into another when the two share a lifetime and that
// lifetime sits inside the second parameter's referent. In
//
//	declare fn store<'a, 'b>(target: &'b mut {peer: &'a mut B}, item: &'a mut B)
//
// item's 'a reappears at target's peer field, so whatever item borrows must outlive
// target.peer. At `store(&mut a, &mut b)` that is the edge a → b at [peer], the same edge
// `val a = {peer: &mut b}` records at its initializer.
//
// Sharing a lifetime at both parameters' outermost position is not a store. In
// `fn f<'a>(x: &'a mut B, y: &'a mut B)` neither referent is reachable through the other. So
// the search skips a target's own outer lifetime and looks inside its referent. The source
// side has no such exemption. Any lifetime its type mentions names data the callee holds.
//
// A signature that only reads through the shared lifetime is treated as one that writes,
// since the two are spelled alike. That can record an alias the call never forms, reporting an
// escape the program does not have. The opposite reading would miss a real dangling borrow.
//
// A method call reaches this recorder through its explicit parameters. freezeClassBody
// coalesces each member's signature into the class body, and a lifetime written at two
// borrows survives that pass, so the two sides still share a variable at the call site.
//
// Two method shapes miss it. A store into the `self` receiver does not reach the recorder,
// since memberValue strips the receiver off the signature it hands the call. An overloaded
// method call does not either, since inferMethodOverloadCall resolves its arm and returns
// before the recorder runs.
//
// So `a.peers.push(&mut b)`, the container case this exists for, is not covered. It needs an
// `Array` type with a method surface and the receiver's type at the call site.
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

// callStoreEdges returns the store effects fn declares, one per (argument, target, path).
//
// A target must be a mutable borrow. A shared borrow takes no write, and an owned-mutable
// parameter — a `RefType` with a nil lifetime — is moved into the callee, so the caller can no
// longer read what the callee writes there. A source must be a borrow too, since an owned
// argument is moved and its escape belongs to the consuming-argument rule.
//
// Each side is classified by type kind before any type is walked, so a call over owned
// arguments costs one pass over the parameter list and walks no type at all. Every parameter
// that is walked is walked once.
//
// A walk that exhausts either allowance saw only part of its type, so a shared lifetime may
// sit past where it stopped. Such a pair takes a store at the whole target rather than
// none, since dropping the edge would drop the escape a borrow written there raises.
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
	sourceTruncated := map[int]bool{}
	for _, i := range sources {
		sourceLts[i], sourceTruncated[i] = lifetimeVarsIn(ctx, fn.Params[i].Type)
	}

	var out []storeEdge
	for _, j := range targets {
		dst := fn.Params[j].Type.(*soltype.RefType)
		sites, targetTruncated := lifetimeSites(ctx, dst.Inner)
		if len(sites) == 0 && !targetTruncated {
			continue
		}
		for _, i := range sources {
			if i == j {
				continue
			}
			// An unnamed path is the widest position the recorder can name, and every field
			// read through the target follows it. See the truncation rule above.
			if targetTruncated || sourceTruncated[i] {
				out = append(out, storeEdge{arg: i, target: j})
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
func lifetimeSites(ctx *Context, t soltype.Type) (map[*soltype.LifetimeVar][][]placeSeg, bool) {
	sites := map[*soltype.LifetimeVar][][]placeSeg{}
	truncated := walkLifetimes(ctx, t, func(lv *soltype.LifetimeVar, path []placeSeg) {
		sites[lv] = append(sites[lv], path)
	})
	return sites, truncated
}

// lifetimeVarsIn returns the lifetime variables occurring anywhere in t, deduped by identity
// and ordered as the walk reaches them. That order keeps a signature's store edges stable
// across runs.
func lifetimeVarsIn(ctx *Context, t soltype.Type) ([]*soltype.LifetimeVar, bool) {
	var out []*soltype.LifetimeVar
	seen := set.NewSet[*soltype.LifetimeVar]()
	truncated := walkLifetimes(ctx, t, func(lv *soltype.LifetimeVar, _ []placeSeg) {
		if seen.Contains(lv) {
			return
		}
		seen.Add(lv)
		out = append(out, lv)
	})
	return out, truncated
}

// walkLifetimes calls visit once per lifetime-variable occurrence in t, passing the field
// path within t at which it occurs. truncated reports that an allowance ran out before the
// walk reached every node, so the occurrences visit saw are a subset of what t holds.
func walkLifetimes(
	ctx *Context, t soltype.Type, visit func(*soltype.LifetimeVar, []placeSeg),
) (truncated bool) {
	w := &lifetimeWalk{
		ctx:            ctx,
		visit:          visit,
		onPath:         set.NewSet[soltype.Type](),
		nodesLeft:      maxLifetimeWalkNodes,
		aliasDepthLeft: maxAliasExpansionDepth,
	}
	w.walk(t, nil)
	return w.truncated
}

// maxLifetimeWalkNodes bounds how many type nodes one walk visits. Alias expansion is what
// makes the bound necessary. A chain of aliases that each name the next one twice describes
// exponentially many field paths, and walking them all takes minutes at a depth a signature
// could plausibly reach. Running out marks the walk truncated, which callStoreEdges reads as
// a store at the whole target.
const maxLifetimeWalkNodes = 1024

// maxAliasExpansionDepth bounds how many alias expansions one root-to-node chain may stack.
// A recursive alias such as `type List<T> = {head: T, tail: List<T>}` expands forever
// otherwise. Each expansion substitutes into a fresh body, which the identity-keyed onPath set
// cannot recognize as the one already being walked. Bounding the depth rather than refusing a
// repeated alias NAME is what keeps a re-nesting like `W<W<&'a mut B>>` correct: its two
// levels are different types and the inner one holds the borrow.
//
// Running out marks the walk truncated, the same as running out of the node allowance.
const maxAliasExpansionDepth = 8

// lifetimeWalk reports where each lifetime variable occurs in a type, as a field path from
// the type's root. The soltype visitor cannot drive it. That visitor rewrites a type bottom-up
// and hands each node no record of the fields traversed to reach it, and the path is the whole
// point here. recordBorrowSources walks the AST by hand for the same reason.
//
// Three limits keep the walk finite and cheap:
//
//   - onPath holds the types on the current root-to-node chain, so a type that refers to
//     itself terminates while a type reached at two different field paths still yields both.
//   - aliasDepthLeft bounds the alias expansions on that chain, which onPath cannot. See
//     maxAliasExpansionDepth.
//   - nodesLeft caps the total nodes visited. See maxLifetimeWalkNodes.
type lifetimeWalk struct {
	ctx    *Context
	visit  func(*soltype.LifetimeVar, []placeSeg)
	onPath set.Set[soltype.Type]
	// nodesLeft is the node allowance remaining for the WHOLE walk, counted down from
	// maxLifetimeWalkNodes. It is never restored, so every branch spends one shared pool.
	nodesLeft int
	// aliasDepthLeft is the alias expansions remaining on the CURRENT root-to-node chain,
	// counted down from maxAliasExpansionDepth and restored on the way back up.
	aliasDepthLeft int
	// truncated records that one of the two allowances ran out, so the walk stopped before
	// reaching every node. Its caller reads this to tell "t holds no more occurrences" apart
	// from "the walk stopped looking", which decide a store differently.
	truncated bool
}

// walk descends t, extending base by a segment at each named field. Only the kinds that can
// hold a borrow reachable by a field path are descended:
//
//   - A borrow reports its own lifetime at base, then descends its referent, so a borrow of a
//     borrow is reached.
//   - An object descends each named property at base plus that name. A member that names no
//     field of its own descends at base, attributing what it holds to the enclosing object.
//     walkObjElem says which members those are.
//   - A tuple element, an array element, a class type or lifetime argument, and a promise or
//     generator payload descend at base unchanged. None of them contributes a name, since the
//     place model has no index segment and neither a class argument nor a payload is
//     addressable as a field. So `&'c mut Box<&'a mut B>` stores at the whole Box, which is
//     where a class holding a borrowed element lands, and so does the `&'c mut Holder<'a>` a
//     class lifetime parameter spells.
//   - A union or intersection descends each member at base, since a borrow any member holds is
//     reachable through the whole.
//   - An alias descends its expansion at base. An alias is transparent, so `Holder<'a>` is
//     searched the way the object it names is.
//
// Any other kind stops the walk there, so the call reads as storing nothing through it. That
// silence is deliberate, unlike the truncation an allowance causes: the kind holds no
// field-addressable borrow.
func (w *lifetimeWalk) walk(t soltype.Type, base []placeSeg) {
	if t == nil || w.onPath.Contains(t) {
		return
	}
	if w.nodesLeft <= 0 {
		w.truncated = true
		return
	}
	w.nodesLeft--
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
		for _, arg := range t.LifetimeArgs {
			if lv, ok := arg.(*soltype.LifetimeVar); ok {
				w.visit(lv, base)
			}
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
// expansion alone. expandAlias substitutes the reference's type and lifetime arguments into
// the body, so the expansion already carries each argument at the field path it really sits
// at. Walking the arguments here as well would report a second occurrence at the container's
// path. An alias with no expansion — unregistered, bodyless, or reached with the chain's alias
// depth spent — has its arguments attributed to base instead of being dropped.
func (w *lifetimeWalk) walkAlias(t *soltype.AliasType, base []placeSeg) {
	if w.ctx != nil {
		if def, ok := w.ctx.aliasDef(t.Name); ok && def.Body != nil {
			if w.aliasDepthLeft <= 0 {
				// The expansion this would have walked holds occurrences the caller does not
				// get to see, so say the walk stopped short rather than reading the
				// reference's own arguments as all the alias contributes.
				w.truncated = true
				return
			}
			w.aliasDepthLeft--
			defer func() { w.aliasDepthLeft++ }()
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
// source, and an index signature's value, which covers every key it admits rather than one. A
// method, a setter, and a constructor hold their borrows in a signature the object does not
// expose as data, so they contribute nothing.
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

// recordCallStoreEdges handles each store fn declares at the call e. Where the borrow lands
// decides what happens, the same split a field store makes between recordFieldStoreEdges and
// checkParamFieldStoreEscape:
//
//   - Into a local, the call records a borrow edge. `store(&mut a, &mut b)` against the
//     signature in this file's opening comment records a → b at [peer], so a later flow-out of
//     a finds b. The target's own place is the prefix, so `store(&mut a.slot, &mut b)` records
//     at [slot, peer].
//   - Into a parameter, the locals the argument carries are reported at once. The parameter's
//     referent belongs to the caller and outlives the frame, so a borrow of a local written
//     into it dangles. Reporting here rather than deferring to the escape post-pass is what
//     keeps the callee's borrow from being weighed as a move, which would consume the locals.
//
// A store is skipped when the argument carries no function-local, when the target names no
// binding, or when the two are the same binding, which would make an unusable self-loop.
//
// An edge is added to what the target already holds rather than replacing it, since a
// signature says where a borrow lands and not whether the callee overwrites what was there.
// This models a container that accumulates, and keeps the sound reading for one that
// overwrites.
//
// The escape check reads the per-program-point graph rather than the eager one, so a call that
// recorded an edge flushes the roots it dirtied into this statement's borrowGens, the same
// handoff a `val` initializer makes.
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
// names one. Both `&mut b` and a bare `b` passed to a shared-borrow parameter carry b. An
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
