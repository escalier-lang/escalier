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
		// TypedArraySpeciesCreate handed it. That allocator captures none of
		// its arguments, so the buffer is fresh and the write is discarded.
		// Only the prose step §3 could not lower is left to report.
		"InteriorOfAFreshAllocation": {"TypedArray.prototype.slice", "incomplete"},
		// InitializeTypedArrayFromTypedArray writes the buffer behind an
		// AllocateTypedArray result, and that allocator stores the name it was
		// given into the array, so reading inside its result can reach a
		// caller's value and the write cannot be placed.
		"InteriorOfACapturingAllocation": {"InitializeTypedArrayFromTypedArray", "args{0} unattributable"},
		// indexOf only reads the receiver, and every String method coerces its
		// receiver to a fresh string before touching it.
		"ReadOnlyMethod": {"Array.prototype.indexOf", "none"},
		// A static has no receiver, so the object it writes sits at a real
		// parameter position. freeze reaches the write through
		// `SetIntegrityLevel(O, ...)` rather than performing it itself.
		"ParameterThroughAHelper": {"Object.freeze", "args{0}"},
		"ParameterThroughASeed":   {"Reflect.set", "args{0}"},
		// assign writes its target through `CreateDataPropertyOrThrow(to, ...)`
		// and also carries a step §3 could not lower, so the mutation it found
		// stands alongside the warning that it could not read the whole
		// algorithm.
		"MutatingAndIncomplete": {"Object.assign", "args{0} incomplete"},
		// toLowerCase reaches the Unicode case-mapping table through a prose
		// step, which §3 emits as an opaque node.
		"OpaqueStep": {"String.prototype.toLowerCase", "incomplete"},
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
	require.Equal(t, "unattributable incomplete", mutationsOf(t, "InternalizeJSONProperty").String())
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
	require.Equal(t, "args{0} incomplete", mutationsOf(t, "ObjectDefineProperties").String())
	require.Equal(t, "args{0} incomplete", mutationsOf(t, "DefinePropertyOrThrow").String())
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

// Only a backing-store slot write counts as a mutation of the object written.
// `Map.prototype.delete` empties the entry in place, `Set p.[[Key]] to EMPTY`,
// and `[[Key]]` is a field of a Map Entry Record rather than a backing store.
// The record itself came out of a read of `M.[[MapData]]`, which breaks the
// origin chain, so the receiver is not in `p` either. delete therefore comes
// out non-mutating, and §6's validation diff is where that disagreement with
// the hand-written overrides surfaces.
func TestMutationSummarySkipsNonBackingStoreSlots(t *testing.T) {
	require.Equal(t, "none", mutationsOf(t, "Map.prototype.delete").String())
}

// A callee that names one of the calling function's parameters is a function
// the caller was handed, not the abstract operation of that name. Charging it
// with the operation's mutations would claim a write the callback may never
// perform, so the call is treated as a body the analysis cannot read.
//
// No callee in the committed graph shadows an operation this way, so the case
// is stated as a graph of its own. `Set` is the seeded property write, and
// `Demo` calls a parameter that happens to carry the same name.
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
// quietly clean. Every call in the committed graph passes the argument, so the
// case is stated as a graph of its own.
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
// Appendix A reserves that shape and §3 emits none, so the case is stated as a
// graph of its own. Charging it the same way keeps a mutation from passing
// unseen, which is the direction the seed's own warning is about.
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
// backing-store slot, or the transfer function changes. A method that mutates
// nothing is the common case, and a builtin that carries neither a mutation nor
// a warning is one §4.3 can classify as non-mutating.
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
	}

	kinds := make([]string, 0, len(tallies))
	for kind, byKind := range tallies {
		kinds = append(kinds, fmt.Sprintf("%s: total %d, receiver %d, args %d, unattributable %d, incomplete %d",
			kind, byKind["total"], byKind["receiver"], byKind["args"], byKind["unattributable"], byKind["incomplete"]))
	}
	sort.Strings(kinds)
	snaps.MatchInlineSnapshot(t, strings.Join(kinds, "\n"), snaps.Inline(`abstract-op: total 701, receiver 0, args 47, unattributable 31, incomplete 268
builtin-method: total 313, receiver 57, args 0, unattributable 3, incomplete 35
builtin-static: total 188, receiver 0, args 7, unattributable 21, incomplete 58`))
}
