package ecma262

import (
	"sort"

	"github.com/escalier-lang/escalier/internal/set"
)

// Sink is the exit a raised value leaves a function through. Every throw site
// belongs to exactly one, which splits a method's raised set into the
// synchronous `throws` and the `rejects` of the promise it returns. A generator
// needs no case of its own, since its `next` reaches the first sink and an async
// generator's the second. See implementation_plan.md §9.3 and FR13.
type Sink uint8

const (
	// SinkSync is a value that leaves as a synchronous abrupt completion.
	SinkSync Sink = iota
	// SinkReject is a value handed to the reject function of the promise
	// capability the algorithm returns.
	SinkReject
)

func (s Sink) String() string {
	if s == SinkReject {
		return "rejects"
	}
	return "throws"
}

// A promise capability record holds the promise and the two functions that
// settle it. These are the field names the graph reads off one.
const (
	// rejectSlot holds the function that rejects the promise. A step calling it
	// routes its argument to the reject sink.
	rejectSlot = "Reject"
	// resolveSlot holds the function that resolves the promise.
	resolveSlot = "Resolve"
)

// newCapability is the abstract operation that builds a promise capability: the
// promise, the function that resolves it, and the function that rejects it.
const newCapability = "NewPromiseCapability"

// completionValue is the field of a completion record holding the value it
// carries. The inlined `IfAbruptRejectPromise` reads it off a captured
// completion to hand the raised value to the capability's reject function.
const completionValue = "Value"

// argLists is where each invoker's argument list sits. `Call(F, V,
// argumentsList)` holds it third and `Construct(F, argumentsList, newTarget)`
// second. The graph writes the list as an allocation whose operands are the
// arguments, so a rejection's reason is the first operand of that allocation.
var argLists = map[string]int{
	"Call":      2,
	"Construct": 1,
}

// capabilityInvoke reports whether a call invokes one of the two functions a
// promise capability holds, and which. `? Call(promiseCapability.[[Reject]],
// undefined, « reason »)` is the shape, and the graph writes the invoked
// function as the argument an invoker reads rather than as the callee.
func capabilityInvoke(node *CallNode) (string, bool) {
	position, invoking := invokers[node.Callee]
	if !invoking || position >= len(node.Args) {
		return "", false
	}
	slot, ok := node.Args[position].(*SlotExpr)
	if !ok || (slot.Slot != rejectSlot && slot.Slot != resolveSlot) {
		return "", false
	}
	return slot.Slot, true
}

// rejectReason returns the value a step hands to a promise capability's reject
// function, and whether the step is such a call at all.
func rejectReason(node *CallNode) (Expr, bool) {
	slot, settling := capabilityInvoke(node)
	if !settling || slot != rejectSlot {
		return nil, false
	}
	position, known := argLists[node.Callee]
	if !known || position >= len(node.Args) {
		return nil, false
	}
	list, ok := node.Args[position].(*AllocExpr)
	if !ok || len(list.Args) == 0 {
		return nil, false
	}
	return list.Args[0], true
}

// rejectRoute is one step whose exceptions reach the reject sink, and where in
// the function that step sits.
type rejectRoute struct {
	node  *CallNode
	index int
}

// directReject is a step that rejects with a plain value rather than with an
// abrupt completion. `Promise.reject` is the shape: it hands its own argument
// to the reject function, so the reason is a value whose origin the map can
// read.
type directReject struct {
	rejectRoute
	reason Expr
}

// rejectPlan is where one function's rejections come from. It is worked out
// from the node list alone, so the fixpoint builds it once and reads it on
// every pass.
type rejectPlan struct {
	// routed holds the steps whose abrupt completion an `IfAbruptRejectPromise`
	// sends to the reject function. What each raises is whatever the fixpoint
	// concluded about the operation that step calls. routed and direct are the
	// two sources §9.3 names.
	routed []rejectRoute
	// direct holds the steps handed a plain value rather than a completion.
	direct []directReject
	// read holds the positions of the `Completion` captures the walk traced
	// back through. Such a capture is where an abrupt completion stops being
	// control flow and becomes a value, and §9.1 flags a function that has one
	// because it can no longer name the step that raised it. A capture this
	// walk read through is named after all, so it leaves the flag alone.
	read set.Set[int]
	// modeled holds what the hand-written combinator model contributes, which
	// has no step in the graph at all.
	modeled []Raised
	// delegated marks a function that hands the capability it built to another
	// function which rejects one. Those rejections settle the promise this
	// function returns and neither source above sees them, so the function is
	// flagged rather than published as if its reject set were whole.
	delegated bool
}

// newRejectPlan works out where fn's rejections come from.
//
// A function that does not return a promise has none. It may still call some
// capability's reject function, as `NewPromiseReactionJob`'s closure does, but
// that capability belongs to whoever handed it over, so the value settles a
// promise this function does not return.
func newRejectPlan(fn *Func, rejecters set.Set[string]) *rejectPlan {
	plan := &rejectPlan{read: set.NewSet[int]()}
	if !fn.Promise {
		return plan
	}
	plan.modeled = combinatorRejects(fn)
	plan.delegated = delegatesCapability(fn, rejecters)

	scan := &rejectScan{fn: fn, defs: definitions(fn), plan: plan}
	routed := set.NewSet[int]()
	for i, node := range fn.Nodes {
		call, ok := node.(*CallNode)
		if !ok {
			continue
		}
		reason, rejecting := rejectReason(call)
		if !rejecting {
			continue
		}
		steps := scan.raisingSteps(reason, set.NewSet[string]())
		if len(steps) == 0 {
			// A reason the walk could not trace to a raising step is a plain
			// value. So is one it could trace to a step that raises nothing,
			// such as the freshly built error object
			// `AsyncFromSyncIteratorPrototype.return` rejects with, and there
			// the origin map answers `Unknown` rather than naming a class. The
			// graph carries the class only on a `Throw` step, so a rejection
			// built that way cannot be named until the serializer records it.
			plan.direct = append(plan.direct, directReject{rejectRoute{node: call, index: i}, reason})
			continue
		}
		for _, step := range steps {
			if routed.Contains(step) {
				continue
			}
			routed.Add(step)
			call, ok := fn.Nodes[step].(*CallNode)
			if !ok {
				continue
			}
			plan.routed = append(plan.routed, rejectRoute{node: call, index: step})
		}
	}
	sort.Slice(plan.routed, func(i, j int) bool { return plan.routed[i].index < plan.routed[j].index })
	return plan
}

// rejecters returns the names of the functions with a step that rejects a
// promise capability, whether that capability is one they built or one they
// were handed.
func rejecters(cfg *CFG) set.Set[string] {
	names := set.NewSet[string]()
	for _, fn := range cfg.Funcs {
		for _, node := range fn.Nodes {
			call, ok := node.(*CallNode)
			if !ok {
				continue
			}
			if slot, settling := capabilityInvoke(call); settling && slot == rejectSlot {
				names.Add(fn.Name)
				break
			}
		}
	}
	return names
}

// delegatesCapability reports whether fn passes a promise capability it built to
// a function that rejects one. `AsyncFromSyncIteratorPrototype.next` is the
// shape: it hands its capability to `AsyncFromSyncIteratorContinuation`, where
// three of the four rejections of the promise it returns happen. Following the
// value in would take a summary of which parameter each function rejects, which
// §9.3 does not build, so the caller is flagged instead. The check is on the
// immediate callee, which leaves the combinators alone; each passes its
// capability to a `PerformPromise*` operation that only resolves it.
func delegatesCapability(fn *Func, rejecters set.Set[string]) bool {
	held := capabilityNames(fn)
	if held.Len() == 0 {
		return false
	}
	for _, node := range fn.Nodes {
		call, ok := node.(*CallNode)
		if !ok || !rejecters.Contains(call.Callee) {
			continue
		}
		for _, arg := range call.Args {
			if name, ok := arg.(*VarExpr); ok && held.Contains(name.Var) {
				return true
			}
		}
	}
	return false
}

// capabilityNames returns the value names a function binds to a promise
// capability it built. A binding that renames one carries it along, which is how
// `let promiseCapability be %0` reaches the name the steps below it use.
func capabilityNames(fn *Func) set.Set[string] {
	held := set.NewSet[string]()
	for {
		grew := false
		for _, node := range fn.Nodes {
			switch node := node.(type) {
			case *CallNode:
				if node.Callee == newCapability && node.Target != "" && !held.Contains(node.Target) {
					held.Add(node.Target)
					grew = true
				}
			case *LetNode:
				source, ok := node.Source.(*VarExpr)
				if ok && held.Contains(source.Var) && !held.Contains(node.Target) {
					held.Add(node.Target)
					grew = true
				}
			default:
				// No other node shape binds a name.
			}
		}
		if !grew {
			return held
		}
	}
}

// definitions indexes each value name a function binds by the positions of the
// steps that bind it. A name bound on two paths has both, which is what lets
// the walk below read an abrupt completion back to either step it came from.
func definitions(fn *Func) map[string][]int {
	defs := make(map[string][]int, len(fn.Nodes))
	for i, node := range fn.Nodes {
		switch node := node.(type) {
		case *LetNode:
			defs[node.Target] = append(defs[node.Target], i)
		case *CallNode:
			if node.Target != "" {
				defs[node.Target] = append(defs[node.Target], i)
			}
		default:
			// No other node shape binds a name.
		}
	}
	return defs
}

// rejectScan walks one function's value names back to the steps that raised
// what its rejections carry.
type rejectScan struct {
	fn   *Func
	defs map[string][]int
	plan *rejectPlan
}

// raisingSteps walks the value handed to a reject function back to the steps
// whose abrupt completion it carries, and returns their positions. ESMeta
// inlines `IfAbruptRejectPromise`, so the shape is four steps:
//
//	%1 = plain GetPromiseResolve(C)
//	%2 = plain Completion(%1)
//	let promiseResolve = %2
//	%3 = ? Call(promiseCapability.[[Reject]], lit, alloc(promiseResolve.[[Value]]))
//
// Reading back to `GetPromiseResolve` is what makes the rejection a fact about
// that operation rather than an untraced value, so the walk unwraps
// `.[[Value]]`, follows a binding to its source, and stops at the capture. seen
// holds the names already walked, since a name is bound from itself on the path
// where the completion turns out to be a normal one.
func (s *rejectScan) raisingSteps(e Expr, seen set.Set[string]) []int {
	switch e := e.(type) {
	case *SlotExpr:
		if e.Slot == completionValue {
			return s.raisingSteps(e.Object, seen)
		}
	case *VarExpr:
		if seen.Contains(e.Var) {
			return nil
		}
		seen.Add(e.Var)
		var steps []int
		for _, i := range s.defs[e.Var] {
			switch def := s.fn.Nodes[i].(type) {
			case *LetNode:
				steps = append(steps, s.raisingSteps(def.Source, seen)...)
			case *CallNode:
				if def.Callee != completionCapture || len(def.Args) == 0 {
					continue
				}
				captured := s.capturedSteps(def.Args[0])
				if len(captured) == 0 {
					continue
				}
				s.plan.read.Add(i)
				steps = append(steps, captured...)
			default:
				// No other node shape binds a name.
			}
		}
		return steps
	default:
		// A `this` value, a literal, an allocation, and a property read name no
		// step the walk can follow.
	}
	return nil
}

// capturedSteps returns the positions of the steps whose result a `Completion`
// capture wrapped. The operand names a call's result, so a name bound to
// anything else contributes nothing.
func (s *rejectScan) capturedSteps(e Expr) []int {
	name, ok := e.(*VarExpr)
	if !ok {
		return nil
	}
	var steps []int
	for _, i := range s.defs[name.Var] {
		call, ok := s.fn.Nodes[i].(*CallNode)
		if !ok || call.Callee == completionCapture {
			continue
		}
		steps = append(steps, i)
	}
	return steps
}

// promiseCombinator is the hand-written model of one `Promise` combinator's
// element rejections. iterable names the parameter holding the promises whose
// reject type the combinator forwards, and form is what it forwards that type
// as. A nil form is a combinator that never rejects from its elements.
type promiseCombinator struct {
	iterable string
	form     func(Origin) Raised
}

// promiseCombinators models the four `Promise` combinators by name. Each
// forwards the reject type of the promises its iterable yields, which arrives
// through the promise-resolution machinery rather than along an edge the graph
// holds, so FR13 asks for the four to be modeled by hand. The model is the
// element channel alone, unioned with the routed rejections the walk above
// finds, which is why `Promise.allSettled` keeps its own iterator `TypeError`
// while forwarding nothing from its elements.
// TestPromiseCombinatorsMatchTheGraph checks each entry against the graph.
var promiseCombinators = map[string]promiseCombinator{
	"Promise.all":        {iterable: "iterable", form: ElementErr},
	"Promise.race":       {iterable: "iterable", form: ElementErr},
	"Promise.any":        {iterable: "iterable", form: AggregateErr},
	"Promise.allSettled": {iterable: "iterable"},
}

// combinatorRejects returns what the hand-written model contributes to fn's
// reject set, which is nothing for any function that is not one of the four
// combinators.
func combinatorRejects(fn *Func) []Raised {
	model, modeled := promiseCombinators[fn.Name]
	if !modeled || model.form == nil {
		return nil
	}
	for i, param := range fn.Params {
		if param == model.iterable {
			return []Raised{model.form(Param(i))}
		}
	}
	return nil
}
