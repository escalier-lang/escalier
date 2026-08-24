package ecma262

import (
	"fmt"
	"sort"
	"strings"

	"github.com/escalier-lang/escalier/internal/set"
)

// directMutators are the abstract operations that mutate an argument outright,
// mapped to the argument position they mutate. They are the fixpoint's base
// cases, the only mutations the analysis asserts rather than derives. See
// planning/ecma-262/implementation_plan.md §4.1 and requirements.md FR1.
//
// An entry reads as "a call to this operation mutates whatever was passed at
// this position". `Array.prototype.push` calls `Set(O, %6, E, true)`, so the
// `Set` entry selects `O`, whose origin is the receiver, and push comes out
// mutating its receiver. The same entry answers differently in
// `Array.prototype.slice`, which calls `Set(A, "length", ...)` on the array
// `ArraySpeciesCreate` handed it. That origin is fresh, so the write is
// invisible to slice's caller and is discarded.
//
// These operations are seeded rather than analyzed because their bodies cannot
// be descended into. `Set`'s body is a single dispatch to the object's
// `[[Set]]` internal method, a callee chosen at runtime by the receiver's type.
// The ordinary path below it dispatches again on the prototype chain and can
// end in a call to a user-supplied setter. The concrete writes land on
// property-descriptor records several layers down, phrased in ESMeta's internal
// object representation rather than as a write to the argument.
//
// Claiming that `Set(O, ...)` mutates `O` over-approximates, since a Proxy's
// `[[Set]]` trap may write elsewhere. That is the FR5-conservative direction. A
// mutation claimed where there is none fails loudly at a call site, while a
// missed one is silent unsoundness.
//
// Composite mutators stay derived. `Object.freeze` gets position 0 from its
// call to `SetIntegrityLevel`, which the fixpoint reads off that operation's
// own summary. `SetIntegrityLevel` is seeded for robustness rather than
// necessity, since its own body writes through `DefinePropertyOrThrow`.
//
// The map is a reviewed constant. A mutating operation the spec adds without an
// entry here produces a false non-mutating result, so validating it against the
// spec text is part of the spec-bump runbook.
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
	// Data-block writes. SetValueInBuffer writes bytes into the Data Block an
	// ArrayBuffer holds in its `[[ArrayBufferData]]` slot. Its argument 0 is
	// that buffer, and every write a DataView setter, `Atomics.store`, or
	// `TypedArray.prototype.set` performs goes through it. The write itself is
	// a byte-range store the graph does not lower, so nothing below this entry
	// reports it.
	"SetValueInBuffer": 0,
}

// backingStoreSlots are the internal slots that hold an object's mutable
// payload, so that writing one mutates the object itself. See requirements.md
// FR3.
//
// A collection does not keep its contents as properties. `Map.prototype.set`
// appends to `M.[[MapData]]` and `Set.prototype.add` appends to
// `S.[[SetData]]`, and neither goes through a property write, so neither is
// reachable from the seed above. Without this list both would come out
// non-mutating.
//
// The list is deliberately narrow. A slot belongs here when it holds the value
// the object's own methods read back. `[[DateValue]]` qualifies, since
// `Date.prototype.setTime` writes it and `Date.prototype.getTime` reads it
// back. `[[Prototype]]` and `[[Extensible]]` do not, because they describe how
// the object behaves rather than what it holds. Leaving them out costs nothing,
// since `Object.setPrototypeOf` and `Object.preventExtensions` reach their
// writes through an internal method the graph does not resolve and come out
// incomplete either way.
//
// Like the seed, this is a reviewed constant. A collection type entering the
// spec with a new payload slot needs an entry here or its methods come out
// non-mutating.
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

// Mutations is what the fixpoint concluded about one function. Args holds the
// 0-based positions of the declared parameters the function may mutate,
// sorted. A method's receiver is not a parameter, so it is reported separately
// by Receiver.
//
// Unattributable and Incomplete are two different failures, and §4.3 turns
// either one into `classified: false` so that FR5's name-based heuristics
// decide the method instead. Unattributable means the analysis saw a mutation
// and could not tie it to the receiver or to a parameter. It knows something
// was written but not what. Incomplete means the analysis could not see the
// whole algorithm, so a mutation may be hiding in the part it could not read.
type Mutations struct {
	Args           []int
	Receiver       bool
	Unattributable bool
	Incomplete     bool
}

// String renders a summary as a space-separated list of the facts that hold, so
// a test can assert one in a single line. A function with no facts reads
// "none".
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
// answers, for one function, which of its declared parameters it may mutate and
// whether it mutates its receiver, counting both the writes the function
// performs itself and those the functions it calls perform on its behalf.
type MutationSummary struct {
	facts map[*Func]*facts
}

// NewMutationSummary runs the mutation fixpoint over cfg.
//
// The seed above gives the base cases, and the summary of every other function
// is derived from them. A function's own writes are charged to its receiver or
// to one of its parameters through the origin map of §4.2, and a call charges
// each position the callee mutates to whatever the call passed there. Since a
// callee's summary can grow after its callers have been read, a function whose
// summary grows re-enqueues its callers.
//
// A call also carries the callee's Unattributable flag up, since the value the
// callee could not place may have arrived as one of the arguments.
//
// The analysis never invents a mutation. Every position in an Args set traces
// back either to a seed entry or to a backing-store slot write.
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
// back empty, which is the answer for a function that mutates nothing.
func (s *MutationSummary) Of(fn *Func) Mutations {
	f := s.facts[fn]
	if f == nil {
		return Mutations{}
	}
	args := f.args.ToSlice()
	sort.Ints(args)
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
// callee the analysis cannot resolve is left out, and the transfer function
// records it as incomplete when it reaches the call.
func (a *analysis) callees(cfg *CFG, fn *Func) []*Func {
	var called []*Func
	seen := set.NewSet[*Func]()
	for _, node := range fn.Nodes {
		call, ok := node.(*CallNode)
		if !ok {
			continue
		}
		callee := a.resolve(cfg, a.origins[fn], call.Callee)
		if callee == nil || seen.Contains(callee) {
			continue
		}
		seen.Add(callee)
		called = append(called, callee)
	}
	return called
}

// resolve returns the function a call node's callee names, or nil when the
// callee is not a body the analysis can read.
//
// A callee is either an abstract-operation name or a value the function holds,
// such as the `callbackfn` a caller passed in. The origin map tells the two
// apart, since a name bound to one of the function's own parameters is a
// callback rather than the operation of that name. No callee in the pinned
// graph shadows an operation this way. The check is there for a spec bump that
// names a parameter after a seeded operation, where resolving by name alone
// would charge the callback with that operation's mutations.
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
			callee := a.resolve(cfg, origin, node.Callee)
			if callee == nil {
				// The callee is an internal method the receiver's type
				// chooses, or a function the caller supplied. Neither is a
				// body the analysis can read.
				changed = f.markIncomplete() || changed
				continue
			}
			summary := a.summary.facts[callee]
			if summary.unattributable {
				// The callee wrote a value it could not place, and that value
				// may have arrived as one of these arguments.
				changed = f.markUnattributable() || changed
			}
			for position := range summary.args {
				if position >= len(node.Args) {
					// No call in the pinned graph omits an argument its callee
					// mutates. A spec bump that introduces one leaves a write
					// with no argument expression to charge it to.
					changed = f.markIncomplete() || changed
					continue
				}
				changed = a.attribute(f, origin, node.Args[position]) || changed
			}
		case *OpaqueNode:
			// A step §3 could not lower, such as a prose step.
			changed = f.markIncomplete() || changed
		default:
			// No other node shape writes a value.
		}
	}
	return changed
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
