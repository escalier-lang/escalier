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
	// OriginUnknown is a value the analysis could not tie to any of the above.
	// It is the top of the lattice, so joining it with anything yields it.
	OriginUnknown
)

// Origin is where a value came from. Index is the parameter position and is
// meaningful only when Kind is OriginParam.
//
// Interior marks a value read out of the backing store of the value Kind names,
// rather than that value itself. `view.[[ViewedArrayBuffer]]` in `SetViewValue`
// is the interior of parameter 0. Writing an interior value writes the object
// holding it, which is what lets §4.1 charge a DataView setter's byte store to
// its receiver. It is not the object itself, so it never stands in where
// identity matters, such as §4.3's return alias.
//
// Captures marks a fresh value built around something the algorithm was given.
// It changes nothing about the fresh value, only about its interior. See
// capturingAllocators.
type Origin struct {
	Kind     OriginKind
	Index    int
	Interior bool
	Captures bool
}

// Receiver, Fresh and Unknown are the origins that carry no index.
var (
	Receiver = Origin{Kind: OriginReceiver}
	Fresh    = Origin{Kind: OriginFresh}
	Unknown  = Origin{Kind: OriginUnknown}
)

// Param returns the origin of the i-th declared parameter, 0-based.
func Param(i int) Origin {
	return Origin{Kind: OriginParam, Index: i}
}

// interiorOf returns the origin of a value read out of o's backing store.
//
// A fresh object's interior is fresh too, unless the allocator that built it
// captured one of its arguments. `TypedArray.prototype.slice` writes the buffer
// behind the array `TypedArraySpeciesCreate` handed it, and that write is
// invisible to its caller.
func interiorOf(o Origin) Origin {
	switch o.Kind {
	case originUnset:
		// The walk has not reached the definition of the object being read.
		// See originUnset.
		return o
	case OriginReceiver, OriginParam:
		o.Interior = true
		return o
	case OriginFresh:
		// A value the algorithm allocated holds only values it also made,
		// unless the allocator captured something it was given.
		if o.Captures {
			return Unknown
		}
		return Fresh
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
	case OriginUnknown:
		name = "Unknown"
	default:
		name = fmt.Sprintf("Origin(%d)", o.Kind)
	}
	if o.Captures {
		name += "(captures)"
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
func (o Origin) join(other Origin) Origin {
	switch {
	case o.Kind == originUnset:
		return other
	case other.Kind == originUnset:
		return o
	case o == other:
		return o
	default:
		return Unknown
	}
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

// capturingAllocators are the allocators that build their result around a value
// they were given, so reading inside that result can reach a value the caller
// already owns. interiorOf keeps every other allocator's result fresh.
//
// `MakeDataViewWithBufferWitnessRecord` is the shape. It ends in `return
// « obj, byteLength »`, so the fresh record holds the very view it was passed.
//
// The list is derived from the graph rather than judged by hand. An allocator
// belongs here when one of its parameters reaches a place interiorOf would read
// it back from: the operands of an allocation it returns, or a backing-store
// slot it writes on the value it allocated.
// TestCapturingAllocatorsMatchTheGraph recomputes it, so a spec bump that
// changes an allocator's shape fails there.
var capturingAllocators = set.FromSlice([]string{
	"AllocateArrayBuffer",
	"AllocateSharedArrayBuffer",
	"AllocateTypedArray",
	"HostMakeJobCallback",
	"MakeDataViewWithBufferWitnessRecord",
	"MakeTypedArrayWithBufferWitnessRecord",
})

// OriginMap holds the origin of every value name in one function.
type OriginMap struct {
	fn      *Func
	origins map[string]Origin
	free    set.Set[string]
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
	m := &OriginMap{
		fn:      fn,
		origins: make(map[string]Origin, len(fn.Params)+len(fn.Nodes)),
		free:    freeNames(fn),
	}
	for {
		changed := false
		for i, p := range fn.Params {
			changed = m.bind(p, Param(i)) || changed
		}
		for _, node := range fn.Nodes {
			switch node := node.(type) {
			case *LetNode:
				changed = m.bind(node.Target, m.eval(node.Source)) || changed
			case *CallNode:
				if node.Target != "" {
					changed = m.bind(node.Target, m.evalCall(node.Callee, node.Args)) || changed
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
		return Unknown
	default:
		// An operand the graph left out, which reaches Eval as a nil Expr.
		return Unknown
	}
}

// evalCall returns the origin of a call's result.
func (m *OriginMap) evalCall(callee string, args []Expr) Origin {
	switch {
	case allocators.Contains(callee):
		if capturingAllocators.Contains(callee) {
			return Origin{Kind: OriginFresh, Captures: true}
		}
		return Fresh
	case freshPrimitives.Contains(callee):
		// A new primitive holds nothing, so it never captures.
		return Fresh
	case identityCoercions.Contains(callee) && len(args) > 0:
		return m.eval(args[0])
	default:
		// Everything else breaks the chain. Get reads a value out of a
		// container, and that value is a different object from the container.
		// A predicate such as IsArray returns a boolean standing for none of
		// its arguments.
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
