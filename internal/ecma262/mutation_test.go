package ecma262

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/require"
)

var (
	summaryOnce sync.Once
	summary     *MutationSummary
)

// testSummary runs the mutation fixpoint over the committed graph once for the
// whole package.
func testSummary(t *testing.T) *MutationSummary {
	t.Helper()
	cfg := testCFG(t)
	summaryOnce.Do(func() {
		summary = NewMutationSummary(cfg)
	})
	return summary
}

// mutationsOf returns the summary of one builtin or abstract operation.
func mutationsOf(t *testing.T, name string) Mutations {
	t.Helper()
	cfg := testCFG(t)
	fn := cfg.Builtin(name)
	if fn == nil {
		fn = cfg.AbstractOp(name)
	}
	require.NotNil(t, fn, "no function named %s", name)
	return testSummary(t).Of(fn)
}

func TestMutationsString(t *testing.T) {
	tests := map[string]struct {
		mutations Mutations
		want      string
	}{
		"Empty":          {Mutations{}, "none"},
		"Receiver":       {Mutations{Receiver: true}, "receiver"},
		"OneArg":         {Mutations{Args: []int{0}}, "args{0}"},
		"SeveralArgs":    {Mutations{Args: []int{0, 2}}, "args{0,2}"},
		"Unattributable": {Mutations{Unattributable: true}, "unattributable"},
		"Incomplete":     {Mutations{Incomplete: true}, "incomplete"},
		"Every": {
			Mutations{Args: []int{1}, Receiver: true, Unattributable: true, Incomplete: true},
			"receiver args{1} unattributable incomplete",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, test.want, test.mutations.String())
		})
	}
}

func TestMutationSummarySampleFunctions(t *testing.T) {
	tests := map[string]struct {
		fn   string
		want string
	}{
		// push calls `Set(O, %6, E, true)`, and the seed says a call to `Set`
		// mutates its argument 0. That argument is `O`, which §4.2 puts at the
		// receiver through `Let O be ? ToObject(this value)`.
		"ReceiverThroughASeededCall": {"Array.prototype.push", "receiver"},
		// fill writes the receiver the same way, through `Set(O, Pk, value,
		// true)` inside its loop.
		"ReceiverInALoop": {"Array.prototype.fill", "receiver"},
		// slice calls `Set` too, but on the array `? ArraySpeciesCreate(O,
		// count)` handed it. That origin is fresh, so the write is invisible to
		// slice's caller and is discarded.
		"FreshWritesAreDiscarded": {"Array.prototype.slice", "none"},
		// Map.prototype.set appends to `M.[[MapData]]`, which no property
		// operation covers. It comes out mutating only because [[MapData]] is a
		// backing-store slot.
		"ReceiverThroughABackingStoreSlot": {"Map.prototype.set", "receiver"},
		"BackingStoreSlotOnASet":           {"Set.prototype.add", "receiver"},
		// Map.prototype.delete empties one entry of `M.[[MapData]]` in place
		// with `Set p.[[Key]] to EMPTY`. `[[Key]]` is a field of a Map Entry
		// Record rather than a backing-store slot, so the write counts only
		// because `p` came out of a read of `M.[[MapData]]` and carries the
		// receiver's interior origin.
		"SlotOnARecordReadOutOfABackingStore": {"Map.prototype.delete", "receiver"},
		// clear empties every entry of `M.[[MapData]]` the same way, and
		// WeakMap.prototype.delete empties the entry it matched in
		// `M.[[WeakMapData]]`.
		"ClearingEveryRecordInABackingStore": {"Map.prototype.clear", "receiver"},
		"SlotOnARecordInAWeakBackingStore":   {"WeakMap.prototype.delete", "receiver"},
		// Date.prototype.setTime writes `dateObject.[[DateValue]]`, the slot
		// Date.prototype.getTime reads back.
		"BackingStoreSlotOnADate": {"Date.prototype.setTime", "receiver"},
		// A finalization registry keeps its cells the way a Map keeps its
		// entries, so `finalizationRegistry.[[Cells]]` is a backing store too.
		"BackingStoreSlotOnARegistry": {"FinalizationRegistry.prototype.register", "receiver"},
		// A DataView setter stores bytes into the Data Block behind its buffer
		// through `SetValueInBuffer`. The buffer reaches that call as
		// `view.[[ViewedArrayBuffer]]`, an interior value of the view, so the
		// store is charged to the view and then to the receiver the setter
		// passed as it.
		"DataBlockWrite": {"DataView.prototype.setUint8", "receiver"},
		// TypedArray.prototype.set writes the same way, two calls further down
		// through `SetTypedArrayFromTypedArray`.
		"DataBlockWriteThroughAHelper": {"TypedArray.prototype.set", "receiver"},
		// Atomics.store writes the buffer behind the typed array it was handed
		// rather than a receiver, so the store lands on a parameter.
		"DataBlockWriteOnAParameter": {"Atomics.store", "args{0}"},
		// TypedArray.prototype.slice writes the buffer behind the array
		// TypedArraySpeciesCreate handed it. That allocator holds none of its
		// arguments, so the buffer is fresh and the write is discarded.
		// Only the prose step §3 could not lower is left to report.
		"InteriorOfAFreshAllocation": {"TypedArray.prototype.slice", "incomplete"},
		// InitializeTypedArrayFromTypedArray writes the buffer behind an
		// AllocateTypedArray result, and that allocator stores the name it was
		// given into the array, so reading inside its result can reach a
		// caller's value and the write cannot be placed.
		"InteriorOfAShallowAllocation": {"InitializeTypedArrayFromTypedArray", "args{0} unattributable"},
		// indexOf only reads the receiver, and every String method coerces its
		// receiver to a fresh string before touching it.
		"ReadOnlyMethod": {"Array.prototype.indexOf", "none"},
		// A static has no receiver, so the object it writes sits at a real
		// parameter position. freeze reaches the write through
		// `SetIntegrityLevel(O, ...)` rather than performing it itself.
		"ParameterThroughAHelper": {"Object.freeze", "args{0}"},
		"ParameterThroughASeed":   {"Reflect.set", "args{0}"},
		// assign writes its target through `CreateDataPropertyOrThrow(to, ...)`.
		// It reaches each source through `from.[[OwnPropertyKeys]]()` and
		// `from.[[GetOwnProperty]](nextKey)`, two internal methods the
		// read-only table answers for, so the write to `to` is the only fact
		// left and no warning stands beside it.
		"ParameterPastReadOnlyInternalMethods": {"Object.assign", "args{0}"},
		// @@replace calls `Set(rx, "lastIndex", 0, true)` on its receiver, and
		// separately appends to three lists through writes whose slot the
		// algorithm computes. The appends land on values the analysis cannot
		// place, so the mutation it found stands alongside the warning that it
		// could not read the whole algorithm.
		"MutatingAndIncomplete": {"RegExp.prototype [ @@replace ]", "receiver incomplete"},
		// setPrototypeOf performs no write of its own. It ends in
		// `O.[[SetPrototypeOf]](proto)`, a dispatch that resolves to no body,
		// and the internal-method table is what reports the write at all.
		"ParameterThroughAnInternalMethod":        {"Object.setPrototypeOf", "args{0}"},
		"ParameterThroughADeletingInternalMethod": {"Reflect.deleteProperty", "args{0}"},
		// The __proto__ setter reaches the same dispatch with its receiver.
		"ReceiverThroughAnInternalMethod": {"set Object.prototype.__proto__", "receiver"},
		// getOwnPropertyDescriptor bottoms out in `obj.[[GetOwnProperty]](key)`.
		// Naming that dispatch read-only is what lets the method come out
		// non-mutating rather than unreadable.
		"ReadOnlyInternalMethod":           {"Object.getOwnPropertyDescriptor", "none"},
		"ReadOnlyInternalMethodOnAKeyList": {"Reflect.ownKeys", "none"},
		// toLowerCase reaches the Unicode case-mapping table through a prose
		// step, which §3 emits as an opaque node.
		"OpaqueStep": {"String.prototype.toLowerCase", "incomplete"},
		// clear empties every entry of `S.[[SetData]]` with a step ESMeta does
		// not formalize. §3 reads the write out of the prose, so the method
		// reports the mutation rather than only that a step was unreadable.
		"ReceiverThroughAProseStep":           {"Set.prototype.clear", "receiver"},
		"ReceiverThroughAProseStepOnAWeakSet": {"WeakSet.prototype.delete", "receiver"},
		// The six read-modify-write Atomics methods hand a modification
		// function to `AtomicReadModifyWrite`, which stores bytes into the Data
		// Block behind the typed array it was passed. The step defining that
		// function is prose, and §3 reads it as the allocation it is.
		"ParameterThroughAReadModifyWrite": {"Atomics.add", "args{0}"},
		// compareExchange performs the same store itself rather than through a
		// helper.
		"ParameterThroughAnInlinedDataBlockWrite": {"Atomics.compareExchange", "args{0}"},
		// The Set algebra methods copy `O.[[SetData]]` and append to the copy,
		// so every write they perform lands on a value they made themselves.
		"FreshCopyOfABackingStore": {"Set.prototype.union", "none"},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, test.want, mutationsOf(t, test.fn).String())
		})
	}
}

// A write the callee could not place travels up to its callers, since the value
// it wrote may have arrived as one of their arguments. `JSON.parse` writes
// nothing itself. It is unattributable because `InternalizeJSONProperty`, which
// it calls, writes a value that operation cannot place.
func TestMutationSummaryCarriesUnattributableWritesUpTheCallGraph(t *testing.T) {
	require.Equal(t, "unattributable", mutationsOf(t, "InternalizeJSONProperty").String())
	require.Equal(t, "unattributable", mutationsOf(t, "JSON.parse").String())
}

// A write to a value read out of a backing-store slot is charged to the object
// that holds the slot. `SetViewValue` passes `view.[[ViewedArrayBuffer]]` to
// `SetValueInBuffer`, which the seed says mutates its argument 0, so the store
// lands on `view` itself. Every DataView setter then passes its receiver as
// `view`.
func TestMutationSummaryChargesAnInteriorWriteToItsHolder(t *testing.T) {
	require.Equal(t, "args{0}", mutationsOf(t, "SetViewValue").String())
	require.Equal(t, "receiver", mutationsOf(t, "DataView.prototype.setFloat64").String())
}

// A mutation travels as far up the call graph as the origin map can follow it.
// `Object.defineProperties` performs no write of its own. It calls
// `ObjectDefineProperties(O, Properties)`, which calls `DefinePropertyOrThrow`
// on the same object, and only that last operation is seeded.
func TestMutationSummaryCarriesMutationsUpTheCallGraph(t *testing.T) {
	require.Equal(t, "args{0}", mutationsOf(t, "Object.defineProperties").String())
	require.Equal(t, "args{0}", mutationsOf(t, "ObjectDefineProperties").String())
	require.Equal(t, "args{0}", mutationsOf(t, "DefinePropertyOrThrow").String())
}

// The mutated position is the one the call site passes the written object at,
// not the position the seed names. `OrdinarySet(O, P, V, Receiver)` ends up
// writing `Receiver` rather than `O`, so its summary is position 3.
func TestMutationSummaryReadsThePositionTheCallPassesTheObjectAt(t *testing.T) {
	cfg := testCFG(t)
	fn := cfg.AbstractOp("OrdinarySet")
	require.NotNil(t, fn)
	require.Equal(t, []string{"O", "P", "V", "Receiver"}, fn.Params)
	require.Equal(t, []int{3}, testSummary(t).Of(fn).Args)
}

// A write the analysis cannot place leaves the function unattributable rather
// than silently non-mutating. `Array.of` builds its array through
// `CreateDataPropertyOrThrow(A, ...)`, where `A` came out of `Construct(C, ...)`.
// §4.2 leaves a constructor's result Unknown rather than fresh, since the
// constructor runs at runtime and may hand back a value the caller already
// holds.
func TestMutationSummaryUnattributableWrite(t *testing.T) {
	require.Equal(t, "unattributable", mutationsOf(t, "Array.of").String())
}

// A write to any slot of a record read out of a backing store is charged to the
// object holding that store. `Map.prototype.delete` empties its entry in place
// with `Set p.[[Key]] to EMPTY`. `[[Key]]` is a field of a Map Entry Record
// rather than a backing-store slot, so the slot list alone reports nothing.
// `p` came out of a read of `M.[[MapData]]`, which puts it at the receiver's
// interior, and the write lands on `M`.
//
// The three methods of this shape are `Map.prototype.delete`,
// `Map.prototype.clear`, and `WeakMap.prototype.delete`. Their Set counterparts
// reach the same answer by the other route. Each states the replacement in
// prose, and §3 lowers it to a write of `[[SetData]]` or `[[WeakSetData]]`,
// which the slot list covers on its own.
func TestMutationSummaryChargesARecordWriteToItsCollection(t *testing.T) {
	require.Equal(t, "receiver", mutationsOf(t, "Map.prototype.delete").String())
	require.Equal(t, "receiver", mutationsOf(t, "Map.prototype.clear").String())
	require.Equal(t, "receiver", mutationsOf(t, "WeakMap.prototype.delete").String())

	require.Equal(t, "receiver", mutationsOf(t, "Set.prototype.delete").String())
	require.Equal(t, "receiver", mutationsOf(t, "Set.prototype.clear").String())
	require.Equal(t, "receiver", mutationsOf(t, "WeakSet.prototype.delete").String())
}

// A write to a slot outside the backing-store list is skipped when the object
// written is the receiver itself rather than a value read out of its backing
// store. `ArrayIteratorPrototype.next` writes `[[IteratedArrayLike]]` and
// `[[ArrayLikeNextIndex]]` on the iterator, and both are cursor bookkeeping
// rather than payload. Whether an iterator's `next` mutates its receiver is a
// decision about the emitted surface, which §11's curation makes.
func TestMutationSummarySkipsNonBackingStoreSlots(t *testing.T) {
	require.Equal(t, "none", mutationsOf(t, "ArrayIteratorPrototype.next").String())
}

// A write whose slot the algorithm computes is charged to the object holding
// the value written, when that value is the object's own backing store. The
// three writes of this shape in the graph all store a byte into a Data Block,
// which the algorithm addresses by index. `GetModifySetValueInBuffer` reaches
// the block through `arrayBuffer.[[ArrayBufferData]]`, so the store lands on
// `arrayBuffer`, and `AtomicReadModifyWrite` passes the buffer behind its own
// typed array as it.
func TestMutationSummaryChargesAComputedSlotWriteOnAnInterior(t *testing.T) {
	require.Equal(t, "args{0} incomplete", mutationsOf(t, "GetModifySetValueInBuffer").String())
	require.Equal(t, "args{0}", mutationsOf(t, "AtomicReadModifyWrite").String())
}

// A write whose slot the algorithm computes leaves the function incomplete when
// the written value is neither fresh nor an interior. The analysis knows
// neither what the write reached nor whose value it was.
func TestMutationSummaryComputedSlotWriteOnAnUnplaceableValue(t *testing.T) {
	cfg, err := ParseCFG([]byte(
		`{"specTarget":"abc","funcs":[` +
			`{"name":"Demo","kind":"builtin-static","params":["x"],"nodes":[` +
			`{"kind":"let","target":"y","source":{"kind":"prop","object":{"kind":"var","var":"x"}}},` +
			`{"kind":"slotwrite","object":{"kind":"var","var":"y"},"value":{"kind":"lit"}}]}]}`))
	require.NoError(t, err)

	require.Equal(t, "incomplete", NewMutationSummary(cfg).Of(cfg.Builtin("Demo")).String())
}

// A write whose slot the algorithm computes is discarded when it lands on a
// rest parameter, because that List is what the call built out of the caller's
// arguments. `Array.prototype.concat` opens with "Let _items_ be a List whose
// first element is _O_ and whose subsequent elements are, in left to right
// order, the arguments passed to this function", which lowers to a write into
// `items` at a slot the algorithm computes. The prepend reaches nothing the
// caller holds, so the method mutates nothing and reads whole.
func TestMutationSummaryDiscardsAComputedSlotWriteOnARestParameter(t *testing.T) {
	require.Equal(t, "none", mutationsOf(t, "Array.prototype.concat").String())
}

// Every write in the committed graph whose slot the algorithm computes and
// whose object sits at a declared parameter, as `name: position`. Nothing can
// say what such a write reached, so it leaves its function `Incomplete`. None
// is in a builtin, and a spec bump that puts one there fails here rather than
// quietly costing that builtin its receiver claim.
//
// A write on a parameter's interior is charged to the parameter holding it, so
// it is not one of these.
func TestGraphComputedSlotWritesOnADeclaredParameter(t *testing.T) {
	cfg := testCFG(t)

	var found []string
	for _, fn := range cfg.Funcs {
		origins := NewOriginMap(fn)
		for _, node := range fn.Nodes {
			write, ok := node.(*SlotWriteNode)
			if !ok || write.Slot != "" {
				continue
			}
			if o := origins.Eval(write.Object); o.Kind == OriginParam && !o.Interior {
				require.Equal(t, AbstractOp, fn.Kind, "%s is a builtin", fn.Name)
				found = append(found, fmt.Sprintf("%s: %d", fn.Name, o.Index))
			}
		}
	}
	sort.Strings(found)

	snaps.MatchInlineSnapshot(t, strings.Join(found, "\n"), snaps.Inline(`AddValueToKeyedGroup: 0
CopyDataBlockBytes: 0
CopyDataBlockBytes: 0
GatherAvailableAncestors: 1
InnerModuleEvaluation: 1
InnerModuleLinking: 1
__APPEND_LIST__: 0
__REMOVE_ELEM__: 1`))
}

// The origin map decides what a callee is before the seed does. A callee bound
// to one of the calling function's parameters is a function the caller was
// handed, so charging it with the seeded operation's mutations would claim a
// write the callback may never perform.
//
// This pins the order of those two checks. The graph holds callbacks, and
// `chargeUnresolved` treats them as unreadable bodies on every one, but none
// carries a seeded name, so reversing the order would leave every real answer
// and every tally unchanged. `TestGraphHoldsNoneOfTheHandWrittenShapes` is what
// reports the day the graph grows a collision.
func TestMutationSummaryCalleeNamingAParameterIsACallback(t *testing.T) {
	cfg, err := ParseCFG([]byte(
		`{"specTarget":"abc","funcs":[` +
			`{"name":"Set","kind":"abstract-op","params":["O","P","V","Throw"]},` +
			`{"name":"Demo","kind":"builtin-static","params":["Set","target"],` +
			`"nodes":[{"kind":"call","callee":"Set","args":[{"kind":"var","var":"target"}],"guard":"?"}]}]}`))
	require.NoError(t, err)

	s := NewMutationSummary(cfg)
	require.Equal(t, "args{0}", s.Of(cfg.AbstractOp("Set")).String())
	require.Equal(t, "incomplete", s.Of(cfg.Builtin("Demo")).String())
}

// A call that omits the argument its callee mutates leaves the write with no
// argument expression to charge it to, so the caller is incomplete rather than
// quietly clean. The guard also keeps the index off the end of the argument
// list. Every call in the committed graph passes the argument, so the case is
// stated as a graph of its own.
func TestMutationSummaryCallOmittingTheMutatedArgument(t *testing.T) {
	cfg, err := ParseCFG([]byte(
		`{"specTarget":"abc","funcs":[` +
			`{"name":"Set","kind":"abstract-op","params":["O","P","V","Throw"]},` +
			`{"name":"Demo","kind":"builtin-static","params":["target"],` +
			`"nodes":[{"kind":"call","callee":"Set","args":[],"guard":"?"}]}]}`))
	require.NoError(t, err)

	s := NewMutationSummary(cfg)
	require.Equal(t, "incomplete", s.Of(cfg.Builtin("Demo")).String())
}

// A call can sit inside an expression rather than in a node of its own.
// `ParseCFG` accepts that shape, so leaving it uncharged would drop a mutation
// with no warning attached, which is the one outcome the two warnings exist to
// prevent. §3 emits none today, so the case is stated as a graph of its own.
func TestMutationSummaryCallNestedInAnExpression(t *testing.T) {
	cfg, err := ParseCFG([]byte(
		`{"specTarget":"abc","funcs":[` +
			`{"name":"Set","kind":"abstract-op","params":["O","P","V","Throw"]},` +
			`{"name":"Demo","kind":"builtin-static","params":["target"],"nodes":[` +
			`{"kind":"let","target":"x","source":{"kind":"call","callee":"Set",` +
			`"args":[{"kind":"var","var":"target"}]}}]}]}`))
	require.NoError(t, err)

	s := NewMutationSummary(cfg)
	require.Equal(t, "args{0}", s.Of(cfg.Builtin("Demo")).String())
}

// A function the summary never saw reads back as mutating nothing, and so does
// one the fixpoint analyzed and found clean.
func TestMutationSummaryOfUnknownFunc(t *testing.T) {
	s := testSummary(t)
	require.Equal(t, Mutations{}, s.Of(&Func{Name: "nosuchfunction"}))
	require.Equal(t, Mutations{}, s.Of(testCFG(t).Builtin("Array.prototype.indexOf")))
}

// Every seed entry has to name an operation the graph holds, or it can never
// fire and the mutations below it go unreported. CreateMethodProperty is the
// one entry with no function at the pinned revision. Nothing reachable from a
// builtin calls it, since the spec uses it from the syntax-directed operations
// §3 drops. It stays in the seed because the seed is a review artifact of FR1's
// vocabulary rather than a list of what this graph happens to contain. A spec
// bump that renames a seeded operation fails here.
func TestMutationSummarySeedResolves(t *testing.T) {
	cfg := testCFG(t)

	absent := make([]string, 0, len(directMutators))
	for name := range directMutators {
		if cfg.AbstractOp(name) == nil {
			absent = append(absent, name)
		}
	}
	sort.Strings(absent)
	snaps.MatchInlineSnapshot(t, strings.Join(absent, "\n"), snaps.Inline(`CreateMethodProperty`))
}

// The internal-method tables answer for a call only when the callee resolves to
// no abstract operation, so an entry whose name the graph also defines as an
// operation is inert: the body answers instead. Three entries are inert at the
// pinned revision. They stay listed because the tables are a review artifact of
// FR1's vocabulary rather than a list of what this graph happens to leave
// unresolved. A spec bump that adds or drops a body for one of these names fails
// here rather than quietly changing which rule decides the call.
func TestInternalMethodTablesResolve(t *testing.T) {
	cfg := testCFG(t)

	var inert []string
	for name := range mutatingInternalMethods {
		if cfg.AbstractOp(name) != nil {
			inert = append(inert, "mutating: "+name)
		}
	}
	for _, name := range readOnlyInternalMethods.ToSlice() {
		if cfg.AbstractOp(name) != nil {
			inert = append(inert, "read-only: "+name)
		}
	}
	sort.Strings(inert)
	snaps.MatchInlineSnapshot(t, strings.Join(inert, "\n"), snaps.Inline(`mutating: Set
read-only: HasProperty
read-only: IsExtensible`))
}

// An internal method takes the object it dispatches on as its first argument, so
// a call with no argument there is one the serializer lowered differently than
// the table expects. The write has no argument expression to charge, which
// leaves the caller incomplete rather than silently non-mutating, and keeps the
// index off the end of the argument list. No call in the committed graph omits
// that argument, so the case is stated as a graph of its own.
func TestInternalMethodTableOnACallMissingItsObject(t *testing.T) {
	cfg, err := ParseCFG([]byte(
		`{"specTarget": "test", "funcs": [
			{"name": "Demo", "kind": "abstract-op", "params": ["x"], "nodes": [
				{"kind": "call", "callee": "SetPrototypeOf", "args": [], "guard": "plain"}
			]}
		]}`))
	require.NoError(t, err)

	fn := cfg.AbstractOp("Demo")
	require.NotNil(t, fn)
	require.Equal(t, "incomplete", NewMutationSummary(cfg).Of(fn).String())
}

// The same ordering holds for the internal-method tables, where getting it wrong
// is worse than with the seed. A callback named `Delete` charged from the
// mutating table claims a write that may not happen, which is merely imprecise.
// One named `GetOwnProperty` answered from the read-only table asserts that
// arbitrary user code mutates nothing, which is unsound.
func TestInternalMethodTableSkipsACallback(t *testing.T) {
	cfg, err := ParseCFG([]byte(
		`{"specTarget": "test", "funcs": [
			{"name": "Demo", "kind": "abstract-op", "params": ["Delete", "x"], "nodes": [
				{"kind": "call", "callee": "Delete", "args": [
					{"kind": "var", "var": "x"}
				], "guard": "plain"}
			]}
		]}`))
	require.NoError(t, err)

	fn := cfg.AbstractOp("Demo")
	require.NotNil(t, fn)
	require.Equal(t, "incomplete", NewMutationSummary(cfg).Of(fn).String())
}

// handWrittenShapes are the four shapes the committed graph does not hold, each
// of which a rule in the transfer function answers for.
type handWrittenShapes struct {
	shadowed      []string // a callee bound to a parameter that also names a seed or table entry
	shortCall     []string // a call omitting a position its resolved callee mutates
	shortDispatch []string // an internal-method call with no dispatching object
	nested        []string // an expression holding a call
}

// findHandWrittenShapes reports where cfg holds each shape. The gate below runs
// it over the committed graph and expects nothing; the test after that runs it
// over a graph holding all four and expects each one found, so the gate cannot
// rot into a check that passes because it looks at nothing.
func findHandWrittenShapes(cfg *CFG, s *MutationSummary) handWrittenShapes {
	var holdsCall func(Expr) bool
	holdsCall = func(e Expr) bool {
		switch e := e.(type) {
		case *CallExpr:
			return true
		case *AllocExpr:
			for _, arg := range e.Args {
				if holdsCall(arg) {
					return true
				}
			}
		case *SlotExpr:
			return holdsCall(e.Object)
		case *PropExpr:
			return holdsCall(e.Object)
		}
		return false
	}

	var found handWrittenShapes
	for _, fn := range cfg.Funcs {
		origin := NewOriginMap(fn)
		for _, node := range fn.Nodes {
			for _, e := range readsOf(node) {
				if e != nil && holdsCall(e) {
					found.nested = append(found.nested, fn.Name)
				}
			}

			call, ok := node.(*CallNode)
			if !ok {
				continue
			}
			where := fmt.Sprintf("%s calls %s", fn.Name, call.Callee)

			// resolve consults the origin map before the graph, so a callee
			// bound to a parameter is a callback whatever it is named.
			if origin.Of(call.Callee).Kind == OriginParam {
				_, seeded := directMutators[call.Callee]
				_, mutating := mutatingInternalMethods[call.Callee]
				if seeded || mutating || readOnlyInternalMethods.Contains(call.Callee) {
					found.shadowed = append(found.shadowed, where)
				}
				continue
			}
			if target := cfg.AbstractOp(call.Callee); target != nil {
				for _, position := range s.Of(target).Args {
					if position >= len(call.Args) {
						found.shortCall = append(found.shortCall, where)
					}
				}
				continue
			}
			if position, ok := mutatingInternalMethods[call.Callee]; ok {
				if position >= len(call.Args) {
					found.shortDispatch = append(found.shortDispatch, where)
				}
			}
		}
	}
	return found
}

// Each shape above is tested with a hand-written graph of its own, because the
// committed graph holds none of them. This checks they are still absent, which
// is what those tests assume and cannot themselves show.
//
// A failure here is not a defect. It means the pinned spec grew the case, so the
// hand-written test is no longer the only description of how the analysis treats
// it and the real occurrence wants reading.
func TestGraphHoldsNoneOfTheHandWrittenShapes(t *testing.T) {
	found := findHandWrittenShapes(testCFG(t), testSummary(t))

	require.Empty(t, found.shadowed,
		"a callee bound to a parameter shares a name with a seed or internal-method entry; "+
			"TestMutationSummaryCalleeNamingAParameterIsACallback and "+
			"TestInternalMethodTableSkipsACallback describe how that call is treated")
	require.Empty(t, found.shortCall,
		"a call omits an argument position its callee mutates; "+
			"TestMutationSummaryCallOmittingTheMutatedArgument describes how it is treated")
	require.Empty(t, found.shortDispatch,
		"an internal-method call omits its dispatching object; "+
			"TestInternalMethodTableOnACallMissingItsObject describes how it is treated")
	require.Empty(t, found.nested,
		"an expression holds a nested call; "+
			"TestMutationSummaryCallNestedInAnExpression describes how it is charged")
}

// The gate above passes over the committed graph. This shows it passes because
// the graph holds none of the shapes rather than because the walk finds nothing,
// by running the same walk over a graph that holds all four.
func TestHandWrittenShapesAreFoundWhenPresent(t *testing.T) {
	cfg, err := ParseCFG([]byte(
		`{"specTarget":"abc","funcs":[` +
			`{"name":"Set","kind":"abstract-op","params":["O","P","V","Throw"]},` +
			`{"name":"Shadow","kind":"abstract-op","params":["Set","x"],"nodes":[` +
			`{"kind":"call","callee":"Set","args":[{"kind":"var","var":"x"}],"guard":"plain"}]},` +
			`{"name":"Short","kind":"abstract-op","params":["x"],"nodes":[` +
			`{"kind":"call","callee":"Set","args":[],"guard":"plain"}]},` +
			`{"name":"ShortDispatch","kind":"abstract-op","params":["x"],"nodes":[` +
			`{"kind":"call","callee":"SetPrototypeOf","args":[],"guard":"plain"}]},` +
			`{"name":"Nested","kind":"abstract-op","params":["x"],"nodes":[` +
			`{"kind":"let","target":"y","source":{"kind":"call","callee":"Set",` +
			`"args":[{"kind":"var","var":"x"}]}}]}]}`))
	require.NoError(t, err)

	found := findHandWrittenShapes(cfg, NewMutationSummary(cfg))
	require.Equal(t, []string{"Shadow calls Set"}, found.shadowed)
	require.Equal(t, []string{"Short calls Set"}, found.shortCall)
	require.Equal(t, []string{"ShortDispatch calls SetPrototypeOf"}, found.shortDispatch)
	require.Equal(t, []string{"Nested"}, found.nested)
}

// Every function in the committed graph gets a summary, and two invariants hold
// across all of them. A mutated position is one of the function's own declared
// parameters, so §4.3 can always name the parameter a fact refers to. Only a
// builtin method mutates a receiver, because only a builtin method has one.
func TestMutationSummaryCoversEveryFunction(t *testing.T) {
	cfg := testCFG(t)
	s := testSummary(t)

	for _, fn := range cfg.Funcs {
		m := s.Of(fn)
		for _, position := range m.Args {
			require.Less(t, position, len(fn.Params), "%s: mutated position %d", fn.Name, position)
			require.GreaterOrEqual(t, position, 0, "%s: mutated position %d", fn.Name, position)
		}
		if m.Receiver {
			require.Equal(t, BuiltinMethod, fn.Kind, "%s mutates a receiver it does not have", fn.Name)
		}
	}
}

// The tallies over the whole graph, which move when a seed entry, a
// backing-store slot, an internal-method entry, or the transfer function
// changes. A method that mutates nothing is the common case.
//
// `classifiable` counts the functions carrying neither warning, the ones §4.3
// decides from the analysis rather than handing to FR5's name-based heuristics.
// It is the number a change to this analysis should push up.
func TestMutationSummaryTallies(t *testing.T) {
	cfg := testCFG(t)
	s := testSummary(t)

	tallies := map[FuncKind]map[string]int{}
	for _, fn := range cfg.Funcs {
		byKind, ok := tallies[fn.Kind]
		if !ok {
			byKind = map[string]int{}
			tallies[fn.Kind] = byKind
		}
		m := s.Of(fn)
		byKind["total"]++
		if m.Receiver {
			byKind["receiver"]++
		}
		if len(m.Args) > 0 {
			byKind["args"]++
		}
		if m.Unattributable {
			byKind["unattributable"]++
		}
		if m.Incomplete {
			byKind["incomplete"]++
		}
		if !m.Unattributable && !m.Incomplete {
			byKind["classifiable"]++
		}
	}

	kinds := make([]string, 0, len(tallies))
	for kind, byKind := range tallies {
		kinds = append(kinds, fmt.Sprintf("%s: total %d, receiver %d, args %d, unattributable %d, incomplete %d, classifiable %d",
			kind, byKind["total"], byKind["receiver"], byKind["args"], byKind["unattributable"], byKind["incomplete"], byKind["classifiable"]))
	}
	sort.Strings(kinds)
	snaps.MatchInlineSnapshot(t, strings.Join(kinds, "\n"), snaps.Inline(`abstract-op: total 701, receiver 0, args 49, unattributable 37, incomplete 226, classifiable 449
builtin-method: total 313, receiver 64, args 0, unattributable 3, incomplete 22, classifiable 289
builtin-static: total 188, receiver 0, args 20, unattributable 21, incomplete 38, classifiable 145`))
}
