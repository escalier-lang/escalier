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
	// originUnset is a name the analysis has not bound to anything yet. It is
	// the bottom of the lattice, so joining it with an origin yields that
	// origin. A name still unset when the analysis finishes reads back as
	// OriginUnknown.
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
type Origin struct {
	Kind  OriginKind
	Index int
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

func (o Origin) String() string {
	switch o.Kind {
	case originUnset:
		return "unset"
	case OriginReceiver:
		return "Receiver"
	case OriginParam:
		return fmt.Sprintf("Param(%d)", o.Index)
	case OriginFresh:
		return "Fresh"
	case OriginUnknown:
		return "Unknown"
	default:
		return fmt.Sprintf("Origin(%d)", o.Kind)
	}
}

// join is the least upper bound of two origins. Two definitions that agree keep
// their origin. Two that disagree collapse to OriginUnknown. That collapse is
// what makes the analysis path-insensitive. A name assigned on two branches
// takes the join of both definitions rather than whichever one the walk
// reached last.
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
// were given, so an origin propagates through them. The list is short and
// reviewed by hand. Calling an operation identity-preserving when it builds a
// new value would charge a mutation to the wrong place.
//
// ToObject is the entry that makes receiver tracking work. `Let O be ?
// ToObject(this value)` keeps `O` at the receiver, which is how
// `Array.prototype.push` comes out mutating. ToObject wraps a primitive
// receiver rather than returning it, so the entry over-approximates. That is
// the safe direction under FR5. A mutation claimed where there is none fails
// loudly at a call site, while a missed one is silent unsoundness.
//
// CanonicalizeKeyedCollectionKey returns its key unchanged apart from
// normalizing -0 to +0, which cannot apply to an object. Completion and
// NormalCompletion wrap a value in a completion record, and the serializer
// drops the matching unwrap, so a caller sees the value they were handed.
//
// Coercions that build a new value are absent by design. ToString and ToNumber
// break the chain. That is why every `String.prototype` method comes out
// non-mutating. The algorithm coerces `this` to a fresh string and never writes
// back through the receiver.
var identityCoercions = set.FromSlice([]string{
	"CanonicalizeKeyedCollectionKey",
	"Completion",
	"NormalCompletion",
	"RequireObjectCoercible",
	"ToObject",
})

// allocators are the abstract operations that return a value they allocated.
// Their result is `Fresh`, the one origin whose mutations the fixpoint
// discards, because a write to a value the algorithm made itself is invisible
// to its caller. Every entry must therefore genuinely allocate. Listing an
// operation that can hand back a value the caller already holds would turn a
// real mutation invisible.
//
// Two absences follow from that. Construct runs a constructor chosen at runtime
// and may return any object, including one of its arguments. ProxyCreate
// allocates a proxy, but a write to that proxy reaches its target, so its
// result is not independent of its argument. Both resolve to `Unknown` instead.
//
// ArraySpeciesCreate, TypedArraySpeciesCreate, and the TypedArrayCreate family
// run a constructor the caller can replace, so they carry the same risk. §4.2
// lists ArraySpeciesCreate anyway, because `Array.prototype.slice` and its
// neighbours build their result through it. Reading a value back out of that
// result is a slot or property access, which breaks the chain regardless.
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

// OriginMap holds the origin of every value name in one function.
type OriginMap struct {
	fn      *Func
	origins map[string]Origin
}

// NewOriginMap computes the origins of fn's value names. Declared parameters
// seed the map, and each Let and each Call result binding takes the join of
// every definition of its name.
//
// The walk is path-insensitive. It never interprets a branch and reads the node
// list as a flat sequence. Repeating the walk until nothing moves makes the
// result independent of the order the serializer emitted the nodes in, so a
// name a loop's back edge redefines still reaches its uses. The repetition
// terminates because an origin only climbs the lattice, from unset to one
// origin to `Unknown`.
func NewOriginMap(fn *Func) *OriginMap {
	m := &OriginMap{fn: fn, origins: make(map[string]Origin, len(fn.Params)+len(fn.Nodes))}
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

// eval returns an expression's origin, keeping the lattice bottom rather than
// resolving it. A name read before the walk reaches its definition then
// contributes nothing to the reader instead of pinning it at `Unknown`.
func (m *OriginMap) eval(e Expr) Origin {
	switch e := e.(type) {
	case *VarExpr:
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
	case *SlotExpr, *PropExpr:
		// A read, so the chain breaks here. The value read out of a container
		// is a different object from the container itself.
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
		return Fresh
	case identityCoercions.Contains(callee) && len(args) > 0:
		return m.eval(args[0])
	default:
		// Get, ToString, ToNumber, and everything else either read a value out
		// of a container or build a new one.
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
