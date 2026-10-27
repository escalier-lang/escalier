package ecma262

import (
	"fmt"
	"sort"
	"strings"

	"github.com/escalier-lang/escalier/internal/set"
)

// OriginKind classifies where a value in an algorithm came from. See
// planning/ecma-262/implementation_plan.md §4.2.
type OriginKind uint8

const (
	// originUnset is a name NewOriginMap's walk has not bound yet. It is the
	// bottom of the lattice, so joining it with an origin yields that origin,
	// and a name still unset when the walk finishes reads back as
	// OriginUnknown.
	//
	// A reader that reaches an unset name stays unset for this pass and takes
	// its origin on the next one. A join never retracts, so answering
	// `Unknown` early would pin the reader there and make the result depend on
	// the order the walk visits nodes in.
	originUnset OriginKind = iota
	// OriginReceiver is a builtin method's `this` value.
	OriginReceiver
	// OriginParam is a declared parameter, identified by its 0-based position.
	// The receiver is never a parameter, so there is no receiver offset.
	OriginParam
	// OriginFresh is a value the algorithm itself allocated. Mutating it is not
	// observable to the caller.
	OriginFresh
	// OriginGlobal is a module-level value, named by a GlobalExpr. The caller
	// never passed it, so writing it charges neither the receiver nor a
	// parameter. ECMA-262 has no such values. A front end lowering a library's
	// own source names its module-level bindings this way.
	OriginGlobal
	// OriginUnknown is a value the analysis could not tie to any of the above.
	// It is the top of the lattice, so joining it with anything yields it.
	OriginUnknown
)

// Origin is where a value came from. Index is the parameter position and is
// meaningful only when Kind is OriginParam.
//
// Interior marks a value reachable from the value Kind names, rather than that
// value itself. `view.[[ViewedArrayBuffer]]` in `SetViewValue` is the interior
// of parameter 0, and so is `options.status` read off a parameter named
// `options`. Writing an interior value writes the object holding it, which is
// what lets §4.1 charge a DataView setter's byte store to its receiver. It is
// not the object itself, so it never stands in where identity matters, such as
// §4.3's return alias.
//
// A backing-store slot and an ordinary property both reach interior values.
// Escalier's `mut` is deep, as docs/08_mutability.md states, so an immutable
// reference must not observe a write anywhere in the structure it reaches. A
// write through a property of the receiver is therefore a write to the
// receiver, even though the receiver's own methods may return what they did
// before. A further read off an interior value keeps the marker.
//
// Shallow on a fresh value marks one that is fresh only at the top. The
// algorithm allocated the value itself but not what it holds, so writing it
// stays invisible to the caller while reading inside it can reach a value the
// caller owns. An allocator that stores one of its arguments into the value it
// returns builds one, and so does the call that builds a rest parameter's List
// out of the caller's arguments. See shallowAllocators and paramOrigin.
//
// Shallow on the receiver or a parameter marks a value that is either that
// value or a fresh one, the join of the two. `options = options || {}` binds
// one. A write to it is charged to the parameter, which is sound because
// writing the fresh half is invisible. What it holds may come from either half,
// so a read inside it is `Unknown`.
type Origin struct {
	Kind     OriginKind
	Index    int
	Interior bool
	Shallow  bool
}

// Receiver, Fresh, ShallowFresh, Global and Unknown are the origins that carry
// no index. ShallowFresh is the value that is fresh while its contents are not,
// which is what a shallow allocator returns and what a rest parameter names.
var (
	Receiver     = Origin{Kind: OriginReceiver}
	Fresh        = Origin{Kind: OriginFresh}
	ShallowFresh = Origin{Kind: OriginFresh, Shallow: true}
	Global       = Origin{Kind: OriginGlobal}
	Unknown      = Origin{Kind: OriginUnknown}
)

// Param returns the origin of the i-th declared parameter, 0-based.
func Param(i int) Origin {
	return Origin{Kind: OriginParam, Index: i}
}

// paramOrigin returns the origin fn's i-th declared parameter is seeded with.
//
// A rest parameter is not a value the caller passed. It names the List the call
// builds out of the arguments the head does not name one by one. Writing that
// List is invisible to the caller, while the values in it are the caller's own,
// which is the split Shallow marks. `Array.prototype.concat` opens by
// prepending its receiver onto `items`, and that prepend reaches nothing the
// caller holds. A read out of the List lands on `Unknown`, since the value it
// yields is one argument rather than the formal's own position.
func paramOrigin(fn *Func, i int) Origin {
	if fn.Variadic != nil && *fn.Variadic == i {
		return ShallowFresh
	}
	return Param(i)
}

// interiorOf returns the origin of a value read out of o's backing store.
//
// A fresh object's interior is fresh too, unless it was built around a value
// the algorithm was given. `TypedArray.prototype.slice` writes the buffer
// behind the array `TypedArraySpeciesCreate` handed it, and that write is
// invisible to its caller.
func interiorOf(o Origin) Origin {
	switch o.Kind {
	case originUnset:
		// The walk has not reached the definition of the object being read.
		// See originUnset.
		return o
	case OriginReceiver, OriginParam:
		// A value that may be a fresh one holds whatever the fresh half holds.
		if o.Shallow {
			return Unknown
		}
		o.Interior = true
		return o
	case OriginFresh:
		// A value the algorithm allocated holds only values it also made,
		// unless the allocation is shallow.
		if o.Shallow {
			return Unknown
		}
		return Fresh
	case OriginGlobal:
		return Global
	default:
		return Unknown
	}
}

// insideOf returns the origin of a value read out of o by a property read, such
// as one entry of a List an object keeps in a backing-store slot, or a field of
// an options object.
//
// A value read off the receiver or a parameter is reachable from it, so writing
// it writes that holder, as Origin describes. `Map.prototype.delete` reaches the
// entry it empties through `M.[[MapData]]`, and the write to that entry is
// charged to `M`. A value read off a fresh allocation may be one of the values
// stored into it, which can be the caller's own, so that read breaks the chain.
func insideOf(o Origin) Origin {
	switch {
	case o.Kind == originUnset:
		// The walk has not reached the definition of the object being read.
		// See originUnset.
		return o
	case o.Shallow && (o.Kind == OriginReceiver || o.Kind == OriginParam):
		// The fresh half's contents are unknown. See Origin.
		return Unknown
	case o.Interior, o.Kind == OriginGlobal:
		return o
	case o.Kind == OriginReceiver, o.Kind == OriginParam:
		o.Interior = true
		return o
	default:
		return Unknown
	}
}

func (o Origin) String() string {
	var name string
	switch o.Kind {
	case originUnset:
		name = "unset"
	case OriginReceiver:
		name = "Receiver"
	case OriginParam:
		name = fmt.Sprintf("Param(%d)", o.Index)
	case OriginFresh:
		name = "Fresh"
	case OriginGlobal:
		name = "Global"
	case OriginUnknown:
		name = "Unknown"
	default:
		name = fmt.Sprintf("Origin(%d)", o.Kind)
	}
	if o.Shallow {
		name += "(shallow)"
	}
	if o.Interior {
		return "Interior(" + name + ")"
	}
	return name
}

// join is the least upper bound of two origins. Two definitions that agree keep
// their origin and two that disagree collapse to `Unknown`. That collapse is
// what makes the analysis path-insensitive. A name assigned on two branches
// takes the join of both rather than whichever the walk reached last.
//
// Two origins agree when they name the same value, whatever their Shallow
// marks, and the join is shallow when either side is. A fresh value also agrees
// with the receiver or a parameter, and the join is that value marked shallow.
// See Origin for why charging a write to it is sound.
func (o Origin) join(other Origin) Origin {
	switch {
	case o.Kind == originUnset:
		return other
	case other.Kind == originUnset:
		return o
	case o.sameValue(other):
		o.Shallow = o.Shallow || other.Shallow
		return o
	case o.Kind == OriginFresh && other.names():
		other.Shallow = true
		return other
	case other.Kind == OriginFresh && o.names():
		o.Shallow = true
		return o
	default:
		return Unknown
	}
}

// sameValue reports whether two origins name the same value, ignoring Shallow.
func (o Origin) sameValue(other Origin) bool {
	o.Shallow, other.Shallow = false, false
	return o == other
}

// names reports whether o is the receiver or a parameter, or a value reachable
// from one.
func (o Origin) names() bool {
	return o.Kind == OriginReceiver || o.Kind == OriginParam
}

// Exact reports whether o is the receiver or a parameter itself: not a value
// reachable from it, and not a join of it with a fresh value. Only an exact
// origin stands in where identity matters, such as a raised value the throw
// analysis threads back to the caller.
func (o Origin) Exact() bool {
	return o.names() && !o.Interior && !o.Shallow
}

// identityCoercions are the abstract operations that hand back the value they
// were given, so an origin propagates through them. Calling one
// identity-preserving when it builds a new value would charge a mutation to the
// wrong place, so the list is short and reviewed by hand.
//
// ToObject is the entry that makes receiver tracking work. `Let O be ?
// ToObject(this value)` keeps `O` at the receiver, which is how
// `Array.prototype.push` comes out mutating. ToObject wraps a primitive
// receiver rather than returning it, so the entry over-approximates.
// directMutators describes why that is the safe direction under FR5.
//
// CanonicalizeKeyedCollectionKey returns its key unchanged apart from
// normalizing -0 to +0, which cannot apply to an object. Completion and
// NormalCompletion wrap a value in a completion record, and the serializer
// drops the matching unwrap, so a caller sees the value they were handed.
//
// A coercion that builds a new value is absent by design. ToString and ToNumber
// break the chain instead. See freshPrimitives.
var identityCoercions = set.FromSlice([]string{
	"CanonicalizeKeyedCollectionKey",
	"Completion",
	"NormalCompletion",
	"RequireObjectCoercible",
	"ToObject",
})

// freshPrimitives are the abstract operations that build a new primitive from
// their argument. Their result is `Fresh` for the same reason an allocation is,
// and with none of the risk, since a primitive cannot be mutated. The list
// exists so that `ToString` resolving away from its argument reads as a
// decision rather than an omission from allocators.
//
// `Let S be ? ToString(O)` is the case that matters. `O` is the receiver, `S`
// is a new string, and nothing done to `S` can reach `O`. That is why every
// `String.prototype` method comes out non-mutating.
//
// A predicate such as IsArray returns a primitive too and is deliberately
// absent. Listing every operation that returns a boolean would add review
// surface without changing an answer, since a predicate's result is only ever
// branched on.
var freshPrimitives = set.FromSlice([]string{
	// Coercions to a primitive. ToObject is not among them, since it returns an
	// object and is identity-preserving instead.
	"ToBigInt",
	"ToBigInt64",
	"ToBoolean",
	"ToDateString",
	"ToIndex",
	"ToInt32",
	"ToIntegerOrInfinity",
	"ToLength",
	"ToNumber",
	"ToNumeric",
	"ToPrimitive",
	"ToPropertyKey",
	"ToString",
	"ToUint16",
	"ToUint32",
	"ToZeroPaddedDecimalString",
	// The numeric type operations of ECMA-262 §6.1.6, which return a Number, a
	// BigInt, or a Boolean.
	"BigInt::divide",
	"BigInt::equal",
	"BigInt::exponentiate",
	"BigInt::leftShift",
	"BigInt::lessThan",
	"BigInt::remainder",
	"BigInt::toString",
	"BigInt::unsignedRightShift",
	"Number::add",
	"Number::equal",
	"Number::exponentiate",
	"Number::lessThan",
	"Number::sameValue",
	"Number::sameValueZero",
	"Number::toString",
	"Number::unaryMinus",
})

// allocators are the abstract operations that return a value they allocated.
// Their result is `Fresh`, the one origin whose mutations the fixpoint
// discards, since a write to a value the algorithm made itself is invisible to
// its caller. Listing an operation that can hand back a value the caller
// already holds would turn a real mutation invisible.
//
// Construct and ProxyCreate are absent for that reason. Construct runs a
// constructor chosen at runtime and may return any object, including one of its
// arguments. A write to a proxy reaches its target, so ProxyCreate's result is
// not independent of its argument. Both resolve to `Unknown`.
//
// ArraySpeciesCreate and the TypedArray create family run a constructor the
// caller can replace, so they carry the same risk. They are listed anyway,
// because `Array.prototype.slice` and its neighbours build their result through
// one. Reading a value back out of that result is a slot or property access,
// which breaks the chain regardless.
var allocators = set.FromSlice([]string{
	// Ordinary objects and records.
	"MakeBasicObject",
	"OrdinaryCreateFromConstructor",
	"OrdinaryObjectCreate",
	"__NEW_ERROR_OBJ__",
	"__NEW_OBJ__",
	// Arrays and lists.
	"ArrayCreate",
	"ArraySpeciesCreate",
	"CreateArrayFromList",
	"CreateListFromArrayLike",
	"MakeMatchIndicesIndexPairArray",
	// Typed arrays, array buffers, and data blocks.
	"AllocateArrayBuffer",
	"AllocateSharedArrayBuffer",
	"AllocateTypedArray",
	"CreateByteDataBlock",
	"CreateSharedByteDataBlock",
	"MakeDataViewWithBufferWitnessRecord",
	"MakeTypedArrayWithBufferWitnessRecord",
	"TypedArrayCreateFromConstructor",
	"TypedArrayCreateSameType",
	"TypedArraySpeciesCreate",
	// Iterators and iterator results.
	"CreateArrayIterator",
	"CreateAsyncFromSyncIterator",
	"CreateForInIterator",
	"CreateIteratorFromClosure",
	"CreateIteratorResultObject",
	"CreateListIteratorRecord",
	"CreateMapIterator",
	"CreateRegExpStringIterator",
	"CreateSetIterator",
	// Functions and arguments objects.
	"BoundFunctionCreate",
	"CreateBuiltinFunction",
	"CreateDynamicFunction",
	"CreateMappedArgumentsObject",
	"CreateUnmappedArgumentsObject",
	"MakeArgGetter",
	"MakeArgSetter",
	"OrdinaryFunctionCreate",
	// Promises and jobs.
	"CreateResolvingFunctions",
	"HostMakeJobCallback",
	"NewPromiseCapability",
	"NewPromiseReactionJob",
	"NewPromiseResolveThenableJob",
	// Other exotic objects.
	"RegExpAlloc",
	"RegExpCreate",
	"StringCreate",
	// Environments and modules.
	"CreateDefaultExportSyntheticModule",
	"ModuleNamespaceCreate",
	"NewDeclarativeEnvironment",
	"NewFunctionEnvironment",
	"NewGlobalEnvironment",
	"NewModuleEnvironment",
	"NewObjectEnvironment",
})

// shallowAllocators are the allocators that build their result around a value
// they were given, so reading inside that result can reach a value the caller
// already owns. interiorOf keeps every other allocator's result fresh all the
// way down.
//
// `MakeDataViewWithBufferWitnessRecord` is the shape. It ends in `return
// « obj, byteLength »`, so the fresh record holds the very view it was passed.
//
// The list is derived from the graph rather than judged by hand. An allocator
// belongs here when one of its parameters reaches a place interiorOf would read
// it back from: the operands of an allocation it returns, or a backing-store
// slot it writes on the value it allocated.
// TestShallowAllocatorsMatchTheGraph recomputes it, so a spec bump that
// changes an allocator's shape fails there.
var shallowAllocators = set.FromSlice([]string{
	"AllocateArrayBuffer",
	"AllocateSharedArrayBuffer",
	"AllocateTypedArray",
	"HostMakeJobCallback",
	"MakeDataViewWithBufferWitnessRecord",
	"MakeTypedArrayWithBufferWitnessRecord",
})

// OriginMap holds the origin of every value name in one function.
//
// returns, when set, answers what a call to a function in the same graph hands
// back, as NewOriginMaps describes. A map built without it treats every call
// outside the tables below as `Unknown`.
type OriginMap struct {
	fn      *Func
	origins map[string]Origin
	free    set.Set[string]
	returns func(m *OriginMap, callee string) (Origin, bool)

	// stored, when set, answers what the graph's functions that implement a
	// declared function type return, one origin each in the implementer's
	// terms. It reports false when any of them is not known yet.
	stored func(via string) ([]Origin, bool)
}

// freeNames returns fn's free names, the value names it reads but never binds.
// A closure's captured value is the common case. `Iterator.prototype.drop`
// builds a helper that opens with `Let remaining be integerLimit`, and
// `integerLimit` belongs to the algorithm that built the helper.
//
// Such a name is `Unknown` from the first pass, since the walk never learns more
// about it. Left at the lattice bottom it would contribute nothing to a join,
// and `remaining`, which a later step assigns a literal, would come out `Fresh`.
// A mutation of it would then read as unobservable to the caller when on one
// path it is the captured value.
func freeNames(fn *Func) set.Set[string] {
	bound := set.FromSlice(fn.Params)
	for _, node := range fn.Nodes {
		switch node := node.(type) {
		case *LetNode:
			bound.Add(node.Target)
		case *CallNode:
			if node.Target != "" {
				bound.Add(node.Target)
			}
		default:
			// No other node shape binds a name.
		}
	}

	free := set.NewSet[string]()
	var read func(Expr)
	read = func(e Expr) {
		switch e := e.(type) {
		case *VarExpr:
			if !bound.Contains(e.Var) {
				free.Add(e.Var)
			}
		case *CallExpr:
			for _, arg := range e.Args {
				read(arg)
			}
		case *AllocExpr:
			for _, arg := range e.Args {
				read(arg)
			}
		case *SlotExpr:
			read(e.Object)
		case *PropExpr:
			read(e.Object)
		default:
			// A this value, a literal, and an absent operand name nothing.
		}
	}
	for _, node := range fn.Nodes {
		switch node := node.(type) {
		case *LetNode:
			read(node.Source)
		case *CallNode:
			for _, arg := range node.Args {
				read(arg)
			}
		case *SlotWriteNode:
			read(node.Object)
			read(node.Value)
		case *ReturnNode:
			read(node.Value)
		case *ThrowNode:
			read(node.Value)
		default:
			// No other node shape reads an operand.
		}
	}
	return free
}

// NewOriginMap computes the origins of fn's value names. Declared parameters
// seed the map, and each Let and each Call result binding takes the join of
// every definition of its name.
//
// The walk is path-insensitive. It never interprets a branch and reads the node
// list as a flat sequence. Repeating it until nothing moves makes the result
// independent of the order the serializer emitted the nodes in, so a name a
// loop's back edge redefines still reaches its uses. It terminates because an
// origin only climbs the lattice, from unset to one origin to `Unknown`.
func NewOriginMap(fn *Func) *OriginMap {
	return newOriginMap(fn, nil, nil)
}

func newOriginMap(fn *Func, returns func(m *OriginMap, callee string) (Origin, bool), stored func(via string) ([]Origin, bool)) *OriginMap {
	m := &OriginMap{
		fn:      fn,
		origins: make(map[string]Origin, len(fn.Params)+len(fn.Nodes)),
		free:    freeNames(fn),
		returns: returns,
		stored:  stored,
	}
	for {
		changed := false
		for i, p := range fn.Params {
			changed = m.bind(p, paramOrigin(fn, i)) || changed
		}
		for _, node := range fn.Nodes {
			switch node := node.(type) {
			case *LetNode:
				changed = m.bind(node.Target, m.eval(node.Source)) || changed
			case *CallNode:
				if node.Target != "" {
					changed = m.bind(node.Target, m.evalCallNode(node)) || changed
				}
			default:
				// No other node shape binds a name.
			}
		}
		if !changed {
			return m
		}
	}
}

// NewOriginMaps computes the origin map of every function in cfg, letting a
// call to an abstract operation take its result's origin from what that
// operation returns. `ToDate(x)` comes out fresh when every return in `ToDate`
// is, and a helper that returns its argument hands the caller that argument's
// origin.
//
// What each operation returns depends on the maps of the operations it calls,
// so the maps are computed in rounds. The first round answers every call
// `Unknown`, the lattice top, which is sound. Each later round reads the
// returns the round before computed. Those only move down the lattice, since a
// more precise callee makes its caller more precise, so the rounds stop.
func NewOriginMaps(cfg *CFG) map[*Func]*OriginMap {
	summaries := make(map[*Func]Origin)
	implementers := implementersOf(cfg)
	stored := func(via string) ([]Origin, bool) {
		var rs []Origin
		for _, target := range implementers[via] {
			r, ok := summaries[target]
			if !ok {
				return nil, false
			}
			rs = append(rs, r)
		}
		return rs, true
	}
	for {
		maps := make(map[*Func]*OriginMap, len(cfg.Funcs))
		for _, fn := range cfg.Funcs {
			maps[fn] = newOriginMap(fn, func(m *OriginMap, callee string) (Origin, bool) {
				target := resolveCallee(cfg, m, callee)
				if target == nil {
					return Origin{}, false
				}
				r, ok := summaries[target]
				return r, ok
			}, stored)
		}
		changed := false
		for _, fn := range cfg.Funcs {
			if fn.Kind != AbstractOp {
				continue
			}
			r := returnOrigin(maps[fn])
			if prev, ok := summaries[fn]; !ok || prev != r {
				summaries[fn] = r
				changed = true
			}
		}
		if !changed {
			return maps
		}
	}
}

// implementersOf maps each declared function type to the graph's functions
// that implement it, in graph order.
func implementersOf(cfg *CFG) map[string][]*Func {
	implementers := map[string][]*Func{}
	for _, fn := range cfg.Funcs {
		for _, via := range fn.Implements {
			implementers[via] = append(implementers[via], fn)
		}
	}
	return implementers
}

// returnOrigin joins the origins of every value fn returns, in fn's own terms.
// A function with no return reads as unset.
func returnOrigin(m *OriginMap) Origin {
	var acc Origin
	for _, node := range m.fn.Nodes {
		if ret, ok := node.(*ReturnNode); ok {
			acc = acc.join(m.eval(ret.Value))
		}
	}
	return acc
}

// instantiate reads a callee's return origin, r, against the arguments one call
// passes. A returned parameter becomes whatever the call passed there.
func (m *OriginMap) instantiate(r Origin, args []Expr) Origin {
	switch r.Kind {
	case OriginFresh, OriginGlobal:
		return r
	case OriginParam:
		if r.Index >= len(args) {
			return Unknown
		}
		o := m.eval(args[r.Index])
		if r.Interior {
			o = insideOf(o)
		}
		if r.Shallow && o.Kind != originUnset {
			o = o.join(Fresh)
		}
		return o
	default:
		// No return at all, an unknown one, or the receiver, which an abstract
		// operation does not have.
		return Unknown
	}
}

// bind joins origin into name's current origin and reports whether that moved
// name up the lattice.
func (m *OriginMap) bind(name string, origin Origin) bool {
	joined := m.origins[name].join(origin)
	if joined == m.origins[name] {
		return false
	}
	m.origins[name] = joined
	return true
}

// Func returns the function the map was computed for.
func (m *OriginMap) Func() *Func {
	return m.fn
}

// Of returns the origin of a value name. A name the walk never bound is
// `Unknown`.
func (m *OriginMap) Of(name string) Origin {
	return resolved(m.origins[name])
}

// Eval returns the origin of an expression read in this function. The mutation
// fixpoint calls it to charge a mutated value to the receiver or a parameter.
func (m *OriginMap) Eval(e Expr) Origin {
	return resolved(m.eval(e))
}

// resolved turns the lattice bottom into `Unknown`. A name reaches the end of
// the walk unset when every definition of it evaluated to unset, which means
// the walk learned nothing about it.
func resolved(o Origin) Origin {
	if o.Kind == originUnset {
		return Unknown
	}
	return o
}

// eval returns an expression's origin. It keeps the lattice bottom rather than
// resolving it, which separates the two reasons a name can be unbound when the
// walk reads it. A name whose definition the walk has not reached yet is still
// bottom and takes its origin on a later pass, as originUnset describes. A free
// name is never going to be bound, so waiting gains nothing and the answer is
// `Unknown` from the first pass.
func (m *OriginMap) eval(e Expr) Origin {
	switch e := e.(type) {
	case *VarExpr:
		if m.free.Contains(e.Var) {
			return Unknown
		}
		return m.origins[e.Var]
	case *GlobalExpr:
		return Global
	case *ThisExpr:
		// Only a prototype method has a `this` value to track. A static or
		// namespace function's `this` is the constructor or the namespace
		// object, never a parameter.
		if m.fn.Kind == BuiltinMethod {
			return Receiver
		}
		return Unknown
	case *CallExpr:
		return m.evalCall(e.Callee, e.Args)
	case *AllocExpr, *LitExpr:
		return Fresh
	case *SlotExpr:
		// A backing-store slot holds the object's own payload, so the value
		// read out of one is charged to the object that holds it. Reading such
		// a slot off an interior value keeps the same base, which is how
		// `targetBuffer` stays at parameter 0 in `SetTypedArrayFromTypedArray`.
		if backingStoreSlots.Contains(e.Slot) {
			return interiorOf(m.eval(e.Object))
		}
		// Any other read breaks the chain. The value read out of a container is
		// a different object from the container itself.
		return Unknown
	case *PropExpr:
		// A property read off the receiver, a parameter, or an interior value
		// is interior to it. `p` in `Map.prototype.delete` is one entry of the
		// List in the receiver's `[[MapData]]`, so a write to `p` writes the
		// Map. See insideOf for the reads that break the chain.
		return insideOf(m.eval(e.Object))
	default:
		// An operand the graph left out, which reaches Eval as a nil Expr.
		return Unknown
	}
}

// evalCallNode returns the origin of the result of a call node.
//
// A call through a stored function returns what the callback contract
// promises, an owned value, or what one of the graph's own implementers of its
// type returns. A callee bound to a parameter is a callback whatever its Via
// says, which evalCall answers.
func (m *OriginMap) evalCallNode(node *CallNode) Origin {
	if node.Via == "" || m.Of(node.Callee).Kind == OriginParam {
		return m.evalCall(node.Callee, node.Args)
	}
	if m.stored == nil {
		return Unknown
	}
	rs, ok := m.stored(node.Via)
	if !ok {
		return Unknown
	}
	acc := Fresh
	for _, r := range rs {
		acc = acc.join(resolved(m.instantiate(r, node.Args)))
	}
	return acc
}

// evalCall returns the origin of a call's result.
func (m *OriginMap) evalCall(callee string, args []Expr) Origin {
	switch {
	case allocators.Contains(callee):
		if shallowAllocators.Contains(callee) {
			return ShallowFresh
		}
		return Fresh
	case freshPrimitives.Contains(callee):
		// A new primitive holds nothing, so it is fresh all the way down.
		return Fresh
	case identityCoercions.Contains(callee) && len(args) > 0:
		return m.eval(args[0])
	case m.Of(callee).Kind == OriginParam:
		// A function the caller supplied. A callback returns an owned value,
		// so its result aliases nothing the function was given.
		return Fresh
	default:
		if m.returns != nil {
			if r, ok := m.returns(m, callee); ok {
				return m.instantiate(r, args)
			}
		}
		// Everything else breaks the chain. That covers a call into a
		// function whose returns the map was not given and an operation with
		// no body, such as an internal method.
		return Unknown
	}
}

// Names returns every value name the map binds, sorted. A name beginning with
// `%` is a temporary ESMeta's compiler introduced, not a name the spec text
// uses.
func (m *OriginMap) Names() []string {
	names := make([]string, 0, len(m.origins))
	for name := range m.origins {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// String renders the map as one `name: origin` line per name, sorted by name,
// so a test can assert the whole map at once. Origin.String spells each origin,
// so a parameter reads as `Param(0)`.
func (m *OriginMap) String() string {
	names := m.Names()

	var sb strings.Builder
	for _, name := range names {
		fmt.Fprintf(&sb, "%s: %s\n", name, m.Of(name))
	}
	return sb.String()
}
