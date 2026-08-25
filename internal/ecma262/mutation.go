package ecma262

import (
	"fmt"
	"sort"
	"strings"

	"github.com/escalier-lang/escalier/internal/set"
)

// directMutators are the abstract operations that mutate an argument outright,
// mapped to the position they mutate. They are the fixpoint's base cases, the
// only mutations the analysis asserts rather than derives. See
// planning/ecma-262/implementation_plan.md §4.1 and requirements.md FR1.
//
// An entry reads as "a call to this operation mutates whatever was passed at
// this position", and the argument's origin decides what that means for the
// caller. `Array.prototype.push` calls `Set(O, ...)` where `O` is the receiver,
// so push mutates its receiver. `Array.prototype.slice` calls `Set(A, ...)` on
// the array `ArraySpeciesCreate` handed it, and a fresh origin is discarded.
//
// These operations are seeded because their bodies cannot be descended into.
// `Set` dispatches to the object's `[[Set]]` internal method, a callee the
// receiver's type chooses at runtime, and the writes below it land on
// property-descriptor records rather than on the argument. Claiming that
// `Set(O, ...)` mutates `O` therefore over-approximates. That is the
// FR5-conservative direction, since a mutation claimed where there is none
// fails loudly at a call site while a missed one is silent unsoundness.
//
// The map is a reviewed constant. A mutating operation the spec adds without an
// entry here produces a false non-mutating result.
var directMutators = map[string]int{
	// Property writes.
	"CreateDataProperty":        0,
	"CreateDataPropertyOrThrow": 0,
	"CreateMethodProperty":      0,
	"DefinePropertyOrThrow":     0,
	"DeletePropertyOrThrow":     0,
	"OrdinaryDefineOwnProperty": 0,
	"Set":                       0,
	// Integrity changes.
	"SetIntegrityLevel": 0,
	// Data-block writes. SetValueInBuffer stores bytes into the Data Block its
	// argument 0 holds, and the graph does not lower that store. Every write a
	// DataView setter, `Atomics.store`, or `TypedArray.prototype.set` performs
	// goes through it.
	"SetValueInBuffer": 0,
}

// backingStoreSlots are the internal slots that hold an object's mutable
// payload, so writing one mutates the object itself. See requirements.md FR3.
//
// A collection does not keep its contents as properties. `Map.prototype.set`
// appends to `M.[[MapData]]` and `Set.prototype.add` appends to `S.[[SetData]]`,
// neither through a property write, so the seed above never reaches them.
//
// A slot belongs here when it holds the value the object's own methods read
// back. `[[DateValue]]` qualifies, since `Date.prototype.setTime` writes it and
// `Date.prototype.getTime` reads it. `[[Prototype]]` and `[[Extensible]]` do
// not, because they describe how the object behaves rather than what it holds.
//
// Like the seed, a reviewed constant. A collection type entering the spec with
// a new payload slot needs an entry or its methods come out non-mutating.
var backingStoreSlots = set.FromSlice([]string{
	// Keyed collections.
	"MapData",
	"SetData",
	"WeakMapData",
	"WeakSetData",
	"WeakRefTarget",
	// Array buffers and the views over them.
	"ArrayBufferByteLength",
	"ArrayBufferData",
	"ArrayLength",
	"ByteLength",
	"ByteOffset",
	"TypedArrayName",
	"ViewedArrayBuffer",
	// Dates.
	"DateValue",
	// Finalization registries.
	"Cells",
})

// mutatingInternalMethods are the object internal methods that write the object
// they dispatch on, mapped to the argument position that object arrives at. The
// CFG flattens `O.[[SetPrototypeOf]](V)` to a call whose callee is the bare name
// `SetPrototypeOf` and whose argument 0 is `O`, so every entry mutates position
// 0. See planning/ecma-262/implementation_plan.md §4.1 and requirements.md FR1.
//
// These are the seed's counterpart for the dispatch the graph cannot follow.
// Each name resolves to no body, since the receiver's type chooses the
// implementation at runtime, and without an entry here the call reads as a step
// the analysis could not see. `Object.setPrototypeOf` and
// `Reflect.preventExtensions` reach their write through nothing else.
//
// Claiming the write over-approximates the same way the seed does. A Proxy trap
// may write elsewhere or nowhere, and assuming the object is mutated is the
// FR5-conservative direction.
var mutatingInternalMethods = map[string]int{
	"DefineOwnProperty": 0,
	"Delete":            0,
	"PreventExtensions": 0,
	"Set":               0,
	"SetPrototypeOf":    0,
}

// readOnlyInternalMethods are the object internal methods that read the object
// they dispatch on and write nothing. A call to one contributes no mutation and
// no warning.
//
// This is the one place the analysis asserts the absence of a mutation rather
// than its presence, so the list is narrow. An entry belongs here only when the
// ordinary implementation runs no user code. `[[GetOwnProperty]]` returns a
// descriptor off the object's own property table, and `[[HasProperty]]` walks
// the prototype chain through that same lookup. `[[Get]]` is left out because
// an ordinary object's accessor property invokes a getter. `[[Call]]` and
// `[[Construct]]` are left out because they run a function body.
//
// A Proxy is the remaining escape, since every trap is user code. The analysis
// does not model that, and §6's validation diff against the hand-written
// overrides is where a trap's write would surface.
var readOnlyInternalMethods = set.FromSlice([]string{
	"GetOwnProperty",
	"GetPrototypeOf",
	"HasProperty",
	"IsExtensible",
	"OwnPropertyKeys",
})

// Mutations is what the fixpoint concluded about one function. Args holds the
// sorted 0-based positions of the declared parameters it may mutate. A method's
// receiver is not a parameter, so Receiver reports it separately.
//
// The two warnings name different failures, and §4.3 turns either into
// `classified: false` so FR5's name-based heuristics decide the method instead.
// Unattributable means the analysis saw a mutation it could not tie to the
// receiver or to a parameter. Incomplete means it could not read the whole
// algorithm, so a mutation may be hiding in the part it missed.
type Mutations struct {
	Args           []int
	Receiver       bool
	Unattributable bool
	Incomplete     bool
}

// String renders the facts that hold as a space-separated list, so a test can
// assert a summary in one line. A function with no facts reads "none".
func (m Mutations) String() string {
	var parts []string
	if m.Receiver {
		parts = append(parts, "receiver")
	}
	if len(m.Args) > 0 {
		positions := make([]string, 0, len(m.Args))
		for _, arg := range m.Args {
			positions = append(positions, fmt.Sprint(arg))
		}
		parts = append(parts, "args{"+strings.Join(positions, ",")+"}")
	}
	if m.Unattributable {
		parts = append(parts, "unattributable")
	}
	if m.Incomplete {
		parts = append(parts, "incomplete")
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, " ")
}

// facts is one function's summary while the fixpoint is still running. It holds
// the mutated positions as a set, which Mutations reports as a sorted slice.
type facts struct {
	args           set.Set[int]
	receiver       bool
	unattributable bool
	incomplete     bool
}

// MutationSummary holds the mutation facts of every function in a graph. It
// answers, for one function, which declared parameters it may mutate and
// whether it mutates its receiver, counting the writes it performs itself and
// those its callees perform on its behalf.
type MutationSummary struct {
	facts map[*Func]*facts
}

// NewMutationSummary runs the mutation fixpoint over cfg.
//
// The seed gives the base cases and every other summary derives from them. A
// function's own writes are charged to its receiver or to a parameter through
// the origin map of §4.2. A call charges each position the callee mutates to
// whatever the call passed there, and carries the callee's Unattributable flag
// up, since the value it could not place may have arrived as one of the
// arguments. A summary that grows re-enqueues its callers.
//
// A call whose callee resolves to no body is an object internal method or a
// function the caller supplied. The two internal-method tables answer for the
// first, and the second leaves the summary incomplete.
//
// The analysis never invents a mutation. Every position in an Args set traces
// back to a seed entry, an internal-method entry, or a backing-store slot
// write.
func NewMutationSummary(cfg *CFG) *MutationSummary {
	a := &analysis{
		summary: &MutationSummary{facts: make(map[*Func]*facts, len(cfg.Funcs))},
		origins: make(map[*Func]*OriginMap, len(cfg.Funcs)),
		callers: make(map[*Func][]*Func, len(cfg.Funcs)),
	}
	for _, fn := range cfg.Funcs {
		a.summary.facts[fn] = &facts{args: set.NewSet[int]()}
		a.origins[fn] = NewOriginMap(fn)
	}
	for _, fn := range cfg.Funcs {
		for _, callee := range a.callees(cfg, fn) {
			a.callers[callee] = append(a.callers[callee], fn)
		}
	}
	for name, position := range directMutators {
		if fn := cfg.AbstractOp(name); fn != nil {
			a.summary.facts[fn].args.Add(position)
		}
	}

	a.run(cfg)
	return a.summary
}

// Of returns fn's summary. A function the summary was not computed for reads
// back empty, the same as one that mutates nothing.
func (s *MutationSummary) Of(fn *Func) Mutations {
	f := s.facts[fn]
	if f == nil {
		return Mutations{}
	}
	var args []int
	if f.args.Len() > 0 {
		args = f.args.ToSlice()
		sort.Ints(args)
	}
	return Mutations{
		Args:           args,
		Receiver:       f.receiver,
		Unattributable: f.unattributable,
		Incomplete:     f.incomplete,
	}
}

// analysis is the state the fixpoint runs over. origins and callers are built
// once and read from then on. Only summary changes as the fixpoint iterates.
type analysis struct {
	summary *MutationSummary
	origins map[*Func]*OriginMap
	callers map[*Func][]*Func
}

// callees returns the functions fn calls, in call order and without repeats. A
// callee the analysis cannot resolve is left out, and transfer records it as
// incomplete when it reaches the call.
func (a *analysis) callees(cfg *CFG, fn *Func) []*Func {
	var called []*Func
	seen := set.NewSet[*Func]()
	add := func(callee string) {
		target := a.resolve(cfg, a.origins[fn], callee)
		if target == nil || seen.Contains(target) {
			return
		}
		seen.Add(target)
		called = append(called, target)
	}

	var nested func(Expr)
	nested = func(e Expr) {
		switch e := e.(type) {
		case *CallExpr:
			add(e.Callee)
			for _, arg := range e.Args {
				nested(arg)
			}
		case *AllocExpr:
			for _, arg := range e.Args {
				nested(arg)
			}
		case *SlotExpr:
			nested(e.Object)
		case *PropExpr:
			nested(e.Object)
		default:
			// No other expression holds a call.
		}
	}

	for _, node := range fn.Nodes {
		if call, ok := node.(*CallNode); ok {
			add(call.Callee)
		}
		for _, e := range readsOf(node) {
			nested(e)
		}
	}
	return called
}

// resolve returns the function a call node's callee names, or nil when the
// callee is not a body the analysis can read.
//
// A callee is either an abstract-operation name or a value the function holds,
// such as the `callbackfn` a caller passed in. A name bound to one of the
// function's own parameters is the second kind. No callee in the pinned graph
// shadows an operation this way; the check guards a spec bump that names a
// parameter after a seeded operation, where resolving by name alone would
// charge the callback with that operation's mutations.
func (a *analysis) resolve(cfg *CFG, origin *OriginMap, callee string) *Func {
	if origin.Of(callee).Kind == OriginParam {
		return nil
	}
	return cfg.AbstractOp(callee)
}

// run iterates the transfer function until no summary moves. It terminates
// because a summary only ever grows: a position enters an Args set and never
// leaves, and each flag goes from false to true at most once.
func (a *analysis) run(cfg *CFG) {
	queue := make([]*Func, len(cfg.Funcs))
	copy(queue, cfg.Funcs)
	queued := set.FromSlice(queue)

	for len(queue) > 0 {
		fn := queue[0]
		queue = queue[1:]
		queued.Remove(fn)

		if !a.transfer(cfg, fn) {
			continue
		}
		for _, caller := range a.callers[fn] {
			if queued.Contains(caller) {
				continue
			}
			queued.Add(caller)
			queue = append(queue, caller)
		}
	}
}

// transfer walks fn once, charging every mutation it can see to fn's receiver
// or to one of fn's parameters, and reports whether fn's summary grew.
func (a *analysis) transfer(cfg *CFG, fn *Func) bool {
	f := a.summary.facts[fn]
	origin := a.origins[fn]
	changed := false

	for _, node := range fn.Nodes {
		switch node := node.(type) {
		case *SlotWriteNode:
			if node.Slot == "" {
				// The algorithm computes the slot it writes, so the curated
				// list cannot say whether this write reaches the object's
				// backing store. A write onto a value the function allocated
				// itself stays invisible to the caller whichever slot it
				// lands on. At any other origin this is a write the analysis
				// cannot read, which is what Incomplete records.
				if origin.Eval(node.Object).Kind != OriginFresh {
					changed = f.markIncomplete() || changed
				}
			} else if backingStoreSlots.Contains(node.Slot) {
				changed = a.attribute(f, origin, node.Object) || changed
			}
		case *CallNode:
			changed = a.charge(cfg, f, origin, node.Callee, node.Args) || changed
		case *OpaqueNode:
			// A step §3 could not lower, such as a prose step.
			changed = f.markIncomplete() || changed
		default:
			// No other node shape writes a value.
		}
		// A call can also sit inside an expression the node reads. Appendix A
		// reserves that shape even though §3 emits none, so it is charged the
		// same way rather than passing unseen.
		for _, e := range readsOf(node) {
			changed = a.chargeNested(cfg, f, origin, e) || changed
		}
	}
	return changed
}

// readsOf returns the expressions a node reads, so the walk can find a call
// nested inside one.
func readsOf(node Node) []Expr {
	switch node := node.(type) {
	case *LetNode:
		return []Expr{node.Source}
	case *CallNode:
		return node.Args
	case *SlotWriteNode:
		return []Expr{node.Object, node.Value}
	case *ReturnNode:
		return []Expr{node.Value}
	case *ThrowNode:
		return []Expr{node.Value}
	default:
		// A branch and an opaque step read nothing.
		return nil
	}
}

// chargeNested charges every call nested inside e, and reports whether that
// grew the summary.
func (a *analysis) chargeNested(cfg *CFG, f *facts, origin *OriginMap, e Expr) bool {
	changed := false
	switch e := e.(type) {
	case *CallExpr:
		changed = a.charge(cfg, f, origin, e.Callee, e.Args) || changed
		for _, arg := range e.Args {
			changed = a.chargeNested(cfg, f, origin, arg) || changed
		}
	case *AllocExpr:
		for _, arg := range e.Args {
			changed = a.chargeNested(cfg, f, origin, arg) || changed
		}
	case *SlotExpr:
		changed = a.chargeNested(cfg, f, origin, e.Object) || changed
	case *PropExpr:
		changed = a.chargeNested(cfg, f, origin, e.Object) || changed
	default:
		// A name, a this value, a literal, and an absent operand hold no call.
	}
	return changed
}

// charge attributes one call's mutations to the calling function, and reports
// whether that grew its summary.
func (a *analysis) charge(cfg *CFG, f *facts, origin *OriginMap, callee string, args []Expr) bool {
	target := a.resolve(cfg, origin, callee)
	if target == nil {
		return a.chargeUnresolved(f, origin, callee, args)
	}

	changed := false
	summary := a.summary.facts[target]
	if summary.unattributable {
		// The callee wrote a value it could not place, and that value may have
		// arrived as one of these arguments.
		changed = f.markUnattributable() || changed
	}
	for position := range summary.args {
		if position >= len(args) {
			// No call in the pinned graph omits an argument its callee mutates.
			// A spec bump that introduces one leaves a write with no argument
			// expression to charge it to.
			changed = f.markIncomplete() || changed
			continue
		}
		changed = a.attribute(f, origin, args[position]) || changed
	}
	return changed
}

// chargeUnresolved charges a call whose callee is not a body the analysis can
// read, and reports whether that grew the summary. The two tables above answer
// for an object internal method. Anything else is a step the analysis could not
// see.
func (a *analysis) chargeUnresolved(f *facts, origin *OriginMap, callee string, args []Expr) bool {
	if origin.Of(callee).Kind == OriginParam {
		// A function the caller supplied, which may name an internal method
		// without being one.
		return f.markIncomplete()
	}
	if position, ok := mutatingInternalMethods[callee]; ok {
		if position >= len(args) {
			// Every internal method takes the dispatching object first, so a
			// call with no argument there is one the serializer lowered
			// differently than this table expects.
			return f.markIncomplete()
		}
		return a.attribute(f, origin, args[position])
	}
	if readOnlyInternalMethods.Contains(callee) {
		return false
	}
	return f.markIncomplete()
}

// attribute charges one mutated value expression to fn's receiver or to one of
// its parameters, and reports whether that grew the summary.
func (a *analysis) attribute(f *facts, origin *OriginMap, e Expr) bool {
	switch o := origin.Eval(e); o.Kind {
	case OriginReceiver:
		if f.receiver {
			return false
		}
		f.receiver = true
		return true
	case OriginParam:
		if f.args.Contains(o.Index) {
			return false
		}
		f.args.Add(o.Index)
		return true
	case OriginFresh:
		// A write to a value the function allocated itself, which its caller
		// never sees.
		return false
	default:
		// OriginUnknown. A mutation the analysis cannot place.
		return f.markUnattributable()
	}
}

func (f *facts) markIncomplete() bool {
	if f.incomplete {
		return false
	}
	f.incomplete = true
	return true
}

func (f *facts) markUnattributable() bool {
	if f.unattributable {
		return false
	}
	f.unattributable = true
	return true
}
