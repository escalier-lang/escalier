package ecma262

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/require"
)

var (
	factsOnce     sync.Once
	allFacts      *Facts
	factsErr      error
	analyzedOnce  sync.Once
	analyzedFacts *Facts
)

// testFacts is the published fact set over the committed graph, curated layer
// merged in, classified once for the whole package. A test pinning what the
// converter consumes reads this.
func testFacts(t *testing.T) *Facts {
	t.Helper()
	cfg := testCFG(t)
	// The error is stored rather than asserted inside the Once, so every caller
	// sees a failure. Asserting in there would fail the first test to arrive and
	// hand every later one a nil fact set to dereference.
	factsOnce.Do(func() { allFacts, factsErr = NewFacts(cfg) })
	require.NoError(t, factsErr)
	return allFacts
}

// testAnalyzedFacts is what the committed graph alone concludes, before
// curated.json is merged over it. A test pinning what §4 can read off the graph
// reads this, so a curated entry never disguises what the analysis found.
func testAnalyzedFacts(t *testing.T) *Facts {
	t.Helper()
	cfg := testCFG(t)
	analyzedOnce.Do(func() {
		analyzedFacts = analyze(cfg)
	})
	return analyzedFacts
}

// position is the 0-based parameter position a fact returns, as AliasRef
// holds it.
func position(i int) *int {
	return &i
}

// returnsParam is the return fact for a builtin that hands back the declared
// parameter at position i.
func returnsParam(i int) ReturnFact {
	return ReturnFact{Kind: AliasParam, Index: position(i)}
}

// factOf returns the published fact for one builtin, failing when the graph
// does not hold it.
func factOf(t *testing.T, name string) MethodFact {
	t.Helper()
	fact, ok := testFacts(t).Of(name)
	require.True(t, ok, "no builtin named %s", name)
	return fact
}

// analyzedFactOf returns what the analysis alone concluded about one builtin.
func analyzedFactOf(t *testing.T, name string) MethodFact {
	t.Helper()
	fact, ok := testAnalyzedFacts(t).Of(name)
	require.True(t, ok, "no builtin named %s", name)
	return fact
}

func TestMethodFactString(t *testing.T) {
	tests := map[string]struct {
		fact MethodFact
		want string
	}{
		// TestFactsSampleMethods renders every shape NewFacts builds, so only
		// what those cases cannot show is stated here: a fact with nothing to
		// render, and a returned position past 0, which no builtin has.
		"NoDetermination": {MethodFact{}, "unclassified"},
		"ParamReturn": {
			MethodFact{Receiver: RecvNone, Returns: returnsParam(2)},
			"receiver:none returns:param(2)",
		},
		// A parameter return with no position reads as the missing position
		// rather than as position 0. Nothing in the package builds such a fact;
		// a hand-edited facts.json is where one would come from.
		"ParamReturnWithNoPosition": {
			MethodFact{Receiver: RecvNone, Returns: ReturnFact{Kind: AliasParam}},
			"receiver:none returns:param(?)",
		},
		// A union names every value it joined, which is what §8.2 spells the
		// lifetime union from.
		"UnionOverThreeValues": {
			MethodFact{Receiver: RecvBorrow, Returns: ReturnFact{Kind: AliasUnion, Members: []AliasRef{
				{Kind: AliasFresh},
				{Kind: AliasParam, Index: position(1)},
				{Kind: AliasReceiver},
			}}},
			"receiver:borrow returns:union(fresh, param(1), receiver)",
		},
		// A union with no members is the counterpart of the positionless
		// parameter return above, and comes from the same place.
		"UnionWithNoMembers": {
			MethodFact{Receiver: RecvNone, Returns: ReturnFact{Kind: AliasUnion}},
			"receiver:none returns:union(?)",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, test.want, test.fact.String())
		})
	}
}

func TestAliasJoin(t *testing.T) {
	receiver := aliasOne(alias{Kind: AliasReceiver})
	fresh := aliasOne(alias{Kind: AliasFresh})
	param0 := aliasOne(aliasOf(Param(0)))
	param1 := aliasOne(aliasOf(Param(1)))
	unknown := aliasOne(alias{Kind: AliasUnknown})

	tests := map[string]struct {
		a, b aliasSet
		// want is the joined members in the order sorted puts them, and kind
		// is the lattice point they add up to.
		want []alias
		kind AliasKind
	}{
		// The empty set is the accumulator before any return has been read, so
		// it never contributes. The loop below joins each pair both ways, which
		// covers it arriving on either side.
		"EmptyTakesTheOther": {aliasSet{}, receiver, []alias{{Kind: AliasReceiver}}, AliasReceiver},
		// An algorithm whose returns the walk never read says nothing about
		// what it hands back, which reads the same way an unresolved return
		// does.
		"EmptyIsUnknown": {aliasSet{}, aliasSet{}, []alias{}, AliasUnknown},
		// Two returns that agree contribute one member, and a position is part
		// of what they have to agree on.
		"AgreeingReturns":        {fresh, fresh, []alias{{Kind: AliasFresh}}, AliasFresh},
		"AgreeingParamPositions": {param1, param1, []alias{{Kind: AliasParam, Index: 1}}, AliasParam},
		// Two returns that disagree contribute two members, and the set is a
		// union. A fresh return counts as a distinct value the same way an
		// input origin does, which §4.3 states for two input origins only.
		"ReceiverAndFresh": {receiver, fresh, []alias{{Kind: AliasFresh}, {Kind: AliasReceiver}}, AliasUnion},
		// Two positions disagree the way two kinds do, and the union names
		// both. No builtin in the committed graph returns two different
		// parameters, so TestFactsUnionOverTwoReturnedParameters writes the
		// graph that does.
		"DifferingParamPosition": {
			param0, param1,
			[]alias{{Kind: AliasParam, Index: 0}, {Kind: AliasParam, Index: 1}},
			AliasUnion,
		},
		// A third return that agrees with neither joins the union as its own
		// member.
		"UnionKeepsEveryMember": {
			receiver.join(fresh), param0,
			[]alias{{Kind: AliasFresh}, {Kind: AliasParam, Index: 0}, {Kind: AliasReceiver}},
			AliasUnion,
		},
		// A return that agrees with a member the union already holds leaves the
		// set as it was.
		"UnionAbsorbsAnAgreeingReturn": {
			receiver.join(fresh), receiver,
			[]alias{{Kind: AliasFresh}, {Kind: AliasReceiver}},
			AliasUnion,
		},
		// Unknown is the top, so it drops every member rather than joining
		// them. `RegExp` loses the position it returns on one path this way.
		"UnknownWinsOverAKnownReturn": {receiver, unknown, []alias{}, AliasUnknown},
		"UnknownWinsOverAUnion":       {receiver.join(fresh), unknown, []alias{}, AliasUnknown},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			for _, joined := range []aliasSet{test.a.join(test.b), test.b.join(test.a)} {
				require.Equal(t, test.want, joined.sorted(), "join is not commutative")
				require.Equal(t, test.kind, joined.kind(), "join is not commutative")
			}
		})
	}
}

// classified renders the receiver and return determinations alone, leaving out
// the throws and rejects channels §9.2 adds. A test written against the §4.3
// classification then reads the same after §9.2 wires two more axes into the
// fact, and the channels are asserted where §9.2's own cases do it.
func classified(fact MethodFact) string {
	return MethodFact{Receiver: fact.Receiver, Returns: fact.Returns}.String()
}

// What §4.3 concludes from the graph alone, one method per shape. These read
// the analysis rather than the published facts, so a curated entry that later
// fills one of these determinations cannot hide what the graph does and does
// not settle.
func TestFactsSampleMethods(t *testing.T) {
	tests := map[string]struct {
		method string
		want   string
	}{
		// The §4.3 gate. push writes its receiver through `Set(O, ...)` and
		// ends in `Return len`, a length it computed itself.
		"MutatingMethodReturningALength": {"Array.prototype.push", "receiver:mutBorrow returns:fresh"},
		// fill writes the same way and ends in `Return O`, the receiver
		// `ToObject(this value)` handed it.
		"MutatingMethodReturningItsReceiver": {"Array.prototype.fill", "receiver:mutBorrow returns:receiver"},
		// slice writes only the array `ArraySpeciesCreate` allocated, and
		// returns that array.
		"ReadOnlyMethodReturningAFreshArray": {"Array.prototype.slice", "receiver:borrow returns:fresh"},
		// Map.prototype.set appends to `M.[[MapData]]` and ends in `Return M`.
		"BackingStoreWriteReturningItsReceiver": {"Map.prototype.set", "receiver:mutBorrow returns:receiver"},
		// A static has no receiver, and freeze ends in `Return O`, the object
		// it was passed.
		"StaticReturningItsArgument": {"Object.freeze", "receiver:none returns:param(0)"},
		// A namespace function has no receiver either. max returns a literal on
		// one path and a number read off its argument list on another, and
		// §4.2 leaves a property read unresolved, so the unresolved return wins
		// the join.
		"NamespaceFunction": {"Math.max", "receiver:none returns:unknown"},
		// The `Object` constructor returns `OrdinaryObjectCreate(...)` on one
		// path and `ToObject(value)`, which is its parameter, on another.
		"ReturnsDifferingOrigins": {"Object", "receiver:none returns:union(fresh, param(0))"},
		// The buffer a view was built over is the view's payload rather than
		// the view, so returning it borrows neither.
		"ReturnsAnInteriorValue": {"get DataView.prototype.buffer", "receiver:borrow returns:unknown"},
		// A method whose graph holds no return at all. ESMeta lowers
		// localeCompare's two argument coercions and stops there, because the
		// comparison itself is implementation-defined, so the walk has nothing
		// to read. The coercions are where the graph ends, not something the
		// return alias consults.
		"NoReturnToRead": {"String.prototype.localeCompare", "receiver:borrow returns:unknown"},
		// An iterator is its own iterable, so @@iterator returns the receiver.
		"SymbolKeyedMethod": {"Iterator.prototype [ @@iterator ]", "receiver:borrow returns:receiver"},
		// setTime writes `dateObject.[[DateValue]]` and returns the same value
		// it stored, which reaches the return through `TimeClip`, an operation
		// §4.2 does not resolve.
		"MutatingMethodWithAnUnresolvedReturn": {"Date.prototype.setTime", "receiver:mutBorrow returns:unknown"},
		// delete empties its entry with `Set p.[[Key]] to EMPTY`, a write to a
		// Map Entry Record read out of `M.[[MapData]]`. §4.1 charges it to the
		// Map, so the receiver claim is mutBorrow. The return is the boolean
		// the algorithm built, which is fresh and not the receiver.
		"MutationThroughARecordInABackingStore": {"Map.prototype.delete", "receiver:mutBorrow returns:fresh"},
		// A caller of an algorithm the analysis could not read every step of is
		// classified all the same, since §4.1 charges `Unattributable` up the
		// call graph and `Incomplete` not at all. next resumes the generator
		// through `GeneratorResume`, which carries the warning.
		"CallerOfAnIncompleteAlgorithm": {"GeneratorPrototype.next", "receiver:borrow returns:unknown"},
		// A method carrying either warning keeps its return alias and loses only
		// its receiver claim. TypedArray.prototype.slice reaches a prose step
		// that could have written the receiver for all the analysis can tell.
		// The typed array it hands back is one TypedArraySpeciesCreate
		// allocated.
		"IncompleteMethodReturningAFreshValue": {"TypedArray.prototype.slice", "returns:fresh"},
		// The same withheld receiver over a return the walk could not resolve.
		// toLowerCase returns what a prose step reaching the Unicode case-mapping
		// table produced.
		"IncompleteMethodReturningAnUnresolvedValue": {"String.prototype.toLowerCase", "returns:unknown"},
		// A static keeps its receiver claim through either warning, since having
		// none follows from `Func.Kind` rather than from a step. Array.of builds
		// its array through `Construct(C, ...)`, whose result the origin map
		// cannot place, so the write to it is unattributable and the return
		// unresolved.
		"UnattributableStatic": {"Array.of", "receiver:none returns:unknown"},
		// A method whose only mutation the specification states in prose. clear
		// empties `S.[[SetData]]` with a step ESMeta does not formalize, and §3
		// reads the write out of the wording. Without that the method publishes
		// no receiver claim at all.
		"MutationStatedInProse": {"Set.prototype.clear", "receiver:mutBorrow returns:fresh"},
		// A rest parameter is the List the call builds out of the arguments
		// the caller passed, so a write into it reaches nothing the caller
		// holds. concat prepends its receiver onto `items` with a write whose
		// slot the algorithm computes, and hands back an array
		// `ArraySpeciesCreate` allocated.
		"WriteIntoARestParameter": {"Array.prototype.concat", "receiver:borrow returns:fresh"},
		// union reads its receiver's payload and builds a new set from it. The
		// step copying that payload is prose too, so leaving it unrecognized
		// costs the method a borrow claim it can support.
		"AllocationStatedInProse": {"Set.prototype.union", "receiver:borrow returns:fresh"},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, test.want, classified(analyzedFactOf(t, test.method)))
		})
	}
}

// The §4.3 gate's last spot-check. Every String method coerces its receiver
// with `ToString` before reading it, and §4.2 makes that coercion's result a
// fresh primitive, so no write can reach the receiver. The seven left out are
// the ones with a step the analysis could not read, so they withhold the receiver
// claim this checks.
func TestFactsEveryStringMethodBorrowsItsReceiver(t *testing.T) {
	var borrowed int
	var unread []string
	for name, fact := range testAnalyzedFacts(t).Methods {
		if !strings.HasPrefix(name, "String.prototype") {
			continue
		}
		if fact.Receiver == "" {
			unread = append(unread, name)
			continue
		}
		require.Equal(t, RecvBorrow, fact.Receiver, "%s mutates its receiver", name)
		borrowed++
	}
	sort.Strings(unread)

	require.Equal(t, 28, borrowed)
	require.Equal(t, []string{
		"String.prototype.normalize",
		"String.prototype.repeat",
		"String.prototype.split",
		"String.prototype.toLocaleLowerCase",
		"String.prototype.toLocaleUpperCase",
		"String.prototype.toLowerCase",
		"String.prototype.toUpperCase",
	}, unread)
}

// Only a builtin method has a receiver to borrow, and a returned parameter
// position names one of the method's own declared parameters, so §5 can always
// resolve the position a fact refers to. Every builtin in the graph gets a
// fact and nothing else does, since an abstract operation feeds the analysis
// but is not a library surface.
func TestFactsCoverEveryBuiltin(t *testing.T) {
	cfg := testCFG(t)
	f := testFacts(t)

	require.Equal(t, cfg.SpecTarget, f.SpecTarget)
	var builtins int
	for _, fn := range cfg.Funcs {
		if fn.Kind == AbstractOp {
			continue
		}
		builtins++
		fact, ok := f.Of(fn.Name)
		require.True(t, ok, "%s has no fact", fn.Name)
		// Every builtin resolves a return alias, warning or not.
		require.NotEmpty(t, fact.Returns.Kind, "%s has no return alias", fn.Name)
		if fn.Kind != BuiltinMethod {
			// `none` follows from the function kind, so no warning withholds it.
			require.Equal(t, RecvNone, fact.Receiver, "%s has no receiver", fn.Name)
		} else {
			require.Contains(t, []ReceiverKind{RecvBorrow, RecvMutBorrow}, fact.Receiver, fn.Name)
		}
		if fact.Returns.Kind != AliasParam {
			require.Nil(t, fact.Returns.Index, "%s carries a position it does not return", fn.Name)
		}
		if fact.Returns.Kind != AliasUnion {
			require.Empty(t, fact.Returns.Members, "%s carries members it did not join", fn.Name)
		}
		// Every position a fact names, whether alone or as one member of a
		// union, indexes a parameter the algorithm declares.
		for _, ref := range fact.Returns.refs() {
			if ref.Kind != AliasParam {
				continue
			}
			require.NotNil(t, ref.Index, "%s returns a parameter but no position", fn.Name)
			require.Less(t, *ref.Index, len(fn.Params), "%s: returned position", fn.Name)
			require.GreaterOrEqual(t, *ref.Index, 0, "%s: returned position", fn.Name)
		}
	}
	// Each builtin resolved above, so an equal count leaves no room for
	// anything else. Reading the count rather than each abstract operation's
	// name is what keeps `Set` from failing it, since that name belongs to
	// both the operations and the constructors.
	require.Equal(t, builtins, len(f.Methods))
}

// A builtin that hands back a different parameter on each path carries both
// positions. Three algorithms in the committed graph return two parameters,
// and all three are abstract operations rather than a library surface, so no
// fact is built for them. They are `Number::add`, `Number::multiply`, and
// `__CLAMP__`. A spec bump that gives a builtin this shape would otherwise
// publish a bare `union` with nothing for §8.2 to name.
func TestFactsUnionOverTwoReturnedParameters(t *testing.T) {
	cfg, err := ParseCFG([]byte(
		`{"specTarget":"abc","funcs":[` +
			`{"name":"Demo.pick","kind":"builtin-static","params":["a","b"],"nodes":[` +
			`{"kind":"branch"},` +
			`{"kind":"return","value":{"kind":"var","var":"a"}},` +
			`{"kind":"return","value":{"kind":"var","var":"b"}}]}]}`))
	require.NoError(t, err)

	facts, err := NewFacts(cfg)
	require.NoError(t, err)
	fact, ok := facts.Of("Demo.pick")
	require.True(t, ok)
	require.Equal(t, "receiver:none returns:union(param(0), param(1)) throws:none rejects:none", fact.String())

	encoded, err := json.Marshal(fact)
	require.NoError(t, err)
	require.Equal(t,
		`{"receiver":"none",`+
			`"returns":{"kind":"union","members":[{"kind":"param","index":0},{"kind":"param","index":1}]},`+
			`"throws":[],"rejects":[]}`,
		string(encoded))
}

// A name the graph does not hold is missing rather than unclassified. The §5
// join reports the two differently: one is a declaration the spec has no
// algorithm for, the other an algorithm the analysis could not read.
func TestFactsOfAnAbsentName(t *testing.T) {
	fact, ok := testFacts(t).Of("Array.prototype.nosuchmethod")
	require.False(t, ok)
	require.Equal(t, MethodFact{}, fact)
}

// A fact serializes as Appendix B describes. Every published fact carries both
// determinations, so a field absent from one is a hole Facts.Incomplete refuses
// to write rather than a claim a consumer has to interpret.
func TestFactsJSON(t *testing.T) {
	tests := map[string]struct {
		fact MethodFact
		want string
	}{
		"Method": {
			factOf(t, "Array.prototype.fill"),
			`{"receiver":"mutBorrow","returns":{"kind":"receiver"},"throws":["TypeError"],"rejects":[]}`,
		},
		// The analysis withheld this receiver, which is the shape Incomplete
		// refuses. Curation answers it before anything is written, so this
		// rendering only ever comes off an analyze result.
		"WithheldReceiver": {
			analyzedFactOf(t, "Array.prototype.toLocaleString"),
			`{"returns":{"kind":"fresh"},"throws":["TypeError"],"rejects":[]}`,
		},
		// The first parameter is written out like any other position. Every
		// parameter the committed graph returns sits at 0, so omitting it would
		// leave the index absent from the whole file and spell the common case
		// as missing data.
		"ReturnedPositionZero": {
			factOf(t, "Object.freeze"),
			`{"receiver":"none","returns":{"kind":"param","index":0},"throws":["TypeError"],"rejects":[]}`,
		},
		"ReturnedPositionPastZero": {
			MethodFact{Receiver: RecvNone, Returns: returnsParam(2), Throws: []string{}, Rejects: []string{}},
			`{"receiver":"none","returns":{"kind":"param","index":2},"throws":[],"rejects":[]}`,
		},
		// A fact that returns no parameter carries no position at all.
		"NoReturnedPosition": {
			factOf(t, "Array.prototype.push"),
			`{"receiver":"mutBorrow","returns":{"kind":"fresh"},"throws":["TypeError"],"rejects":[]}`,
		},
		// The §4.3 union gate. The `Object` constructor hands back an
		// `OrdinaryObjectCreate` result on one path and `ToObject(value)` on
		// another, and the entry names both so §8.2 can spell the lifetime
		// union they seed.
		"Union": {
			factOf(t, "Object"),
			`{"receiver":"none","returns":{"kind":"union","members":[{"kind":"fresh"},{"kind":"param","index":0}]},` +
				`"throws":["TypeError"],"rejects":[]}`,
		},
		// The zero fact carries neither field, which is what Of returns for a
		// name the graph does not hold.
		"NoDetermination": {MethodFact{}, `{}`},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			encoded, err := json.Marshal(test.fact)
			require.NoError(t, err)
			require.Equal(t, test.want, string(encoded))
		})
	}
}

// The methods whose receiver the analysis withholds. Each carries a mutation it
// could not place or a step it could not read, so the mutability is withheld
// rather than guessed, and each still publishes its return alias. A static is
// absent, having no receiver for a warning to cost it.
//
// This list is the §4 objective made visible, and shrinking it is what a change
// to the analysis is measured by. Every name on it is answered by an entry in
// curated.json, so nothing reaches §7's name heuristics, which is what the
// second assertion holds.
func TestFactsUnclassifiedMethodsAreListed(t *testing.T) {
	snaps.MatchSnapshot(t, strings.Join(testAnalyzedFacts(t).Unclassified(AxisReceiver), "\n"))
	require.Empty(t, testFacts(t).Unclassified(AxisReceiver))
}

// The return axis is where the published surface still has gaps. Every fact
// carries a return, and `answers` is what separates one naming a value the
// caller holds from an `unknown` naming none.
//
// `Date.prototype.getTime` is off the list because an entry answered it. The
// two `buffer` getters stay on it deliberately: what they hand back is a borrow
// of state the receiver holds, and no member of the alias lattice spells that
// without claiming the return is the receiver itself.
func TestFactsUnclassifiedReturnsAreListed(t *testing.T) {
	unresolved := testFacts(t).Unclassified(AxisReturns)

	require.Contains(t, unresolved, "get DataView.prototype.buffer")
	require.Contains(t, unresolved, "get TypedArray.prototype.buffer")
	require.NotContains(t, unresolved, "Date.prototype.getTime")
	require.NotContains(t, unresolved, "Date.prototype.valueOf")
	// The same count the tallies below record as `returns unknown`.
	require.Len(t, unresolved, 247)
}

// demoCFG is a two-method graph standing in for the shapes the committed one
// holds. `read` returns its receiver and writes nothing, so the analysis
// classifies both its determinations. `opaque`'s one step is prose the
// serializer could not lower, which withholds its receiver and leaves its
// return unresolved.
const demoCFG = `{"specTarget":"abc","funcs":[` +
	`{"name":"Demo.prototype.read","kind":"builtin-method","params":[],"nodes":[` +
	`{"kind":"return","value":{"kind":"this"}}]},` +
	`{"name":"Demo.prototype.opaque","kind":"builtin-method","params":[],"nodes":[` +
	`{"kind":"opaque","text":["Let _x_ be whatever the host decides."]}]}]}`

// demoFacts parses demoCFG and classifies it from the graph alone.
func demoFacts(t *testing.T) (*CFG, map[string]MethodFact) {
	t.Helper()
	cfg, err := ParseCFG([]byte(demoCFG))
	require.NoError(t, err)
	return cfg, analyze(cfg).Methods
}

// The analysis leaves Demo.prototype.opaque's receiver open and settles
// Demo.prototype.read's, which is what the axis cases below are written
// against.
func TestDemoGraphClassification(t *testing.T) {
	t.Parallel()

	_, methods := demoFacts(t)
	require.Equal(t, "receiver:borrow returns:receiver throws:none rejects:none",
		methods["Demo.prototype.read"].String())
	// The opaque step is one the throw fixpoint could not read either, so both
	// channels are published short. FR5 makes that the norm rather than a
	// state the fact records.
	require.Equal(t, "returns:unknown throws:none rejects:none",
		methods["Demo.prototype.opaque"].String())
}

// Both axes report an open determination, each spelled the way that axis
// spells it. `opaque` has its receiver withheld and its return resolved to the
// lattice top, so it is open on both; `read` answers both.
func TestUnclassifiedReadsBothAxes(t *testing.T) {
	t.Parallel()

	cfg, methods := demoFacts(t)
	facts := &Facts{SpecTarget: cfg.SpecTarget, Methods: methods}

	require.Equal(t, []string{"Demo.prototype.opaque"}, facts.Unclassified(AxisReceiver))
	require.Equal(t, []string{"Demo.prototype.opaque"}, facts.Unclassified(AxisReturns))

	// Answering one axis takes the method off that list alone.
	mergeCuration(cfg, &Curation{Entries: map[string]CuratedEntry{
		"Demo.prototype.opaque": entry(t, cfg, "Demo.prototype.opaque", RecvBorrow),
	}}, methods)
	require.Empty(t, facts.Unclassified(AxisReceiver))
	require.Equal(t, []string{"Demo.prototype.opaque"}, facts.Unclassified(AxisReturns))
}

// An axis `answers` does not name reads as unanswered, so each determination §8
// and §9 add shows up as wholly open until it is wired in.
func TestUnclassifiedReadsAnUnwiredAxisAsOpen(t *testing.T) {
	t.Parallel()

	cfg, methods := demoFacts(t)
	facts := &Facts{SpecTarget: cfg.SpecTarget, Methods: methods}

	require.Equal(t,
		[]string{"Demo.prototype.opaque", "Demo.prototype.read"},
		facts.Unclassified(Axis("params")))
}

// Every published fact carries a value on every axis, which is the invariant
// that lets facts.json omit coverage flags entirely. The analysis alone does
// not hold it — it withholds 24 receivers — and the curated layer is what
// closes the gap.
func TestFactsCarryEveryAxis(t *testing.T) {
	require.Empty(t, testFacts(t).Incomplete())
	require.Len(t, testAnalyzedFacts(t).Incomplete(), 24)
}

// A hole is named with the axis that has it, so the operator knows which
// determination to curate rather than only which method.
func TestIncompleteNamesTheAxis(t *testing.T) {
	t.Parallel()

	cfg, methods := demoFacts(t)
	facts := &Facts{SpecTarget: cfg.SpecTarget, Methods: methods}

	// `opaque` has no receiver; its return is `unknown`, which is a value.
	require.Equal(t,
		[]string{"Demo.prototype.opaque: no receiver determination"},
		facts.Incomplete())

	mergeCuration(cfg, &Curation{Entries: map[string]CuratedEntry{
		"Demo.prototype.opaque": entry(t, cfg, "Demo.prototype.opaque", RecvBorrow),
	}}, methods)
	require.Empty(t, facts.Incomplete())
}

// Every axis Incomplete walks has a case in `carries`. A fact populated on
// every axis carries all of them, and an empty fact carries none. Adding an
// axis to publishedAxes and forgetting the switch fails the first assertion,
// because the fail-closed default reads that axis as a hole however the fact is
// populated. Catching it here names the missing case; the gate would otherwise
// report every method as incomplete.
func TestCarriesCoversEveryPublishedAxis(t *testing.T) {
	t.Parallel()

	full := MethodFact{
		Receiver: RecvBorrow,
		Returns:  ReturnFact{Kind: AliasFresh},
		Throws:   []string{},
		Rejects:  []string{},
	}
	for _, axis := range publishedAxes {
		require.Truef(t, full.carries(axis),
			"%s is a published axis that carries does not name", axis)
		require.Falsef(t, MethodFact{}.carries(axis),
			"%s reads as carried on a fact holding nothing", axis)
	}
}

// An axis neither predicate names reads as a hole for generation and as open
// work for a reviewer. Both directions fail closed, so an axis §8 or §9 adds to
// publishedAxes without wiring up stops the run rather than shipping a
// determination nothing populates.
func TestCarriesReadsAnUnwiredAxisAsAHole(t *testing.T) {
	t.Parallel()

	fact := MethodFact{Receiver: RecvBorrow, Returns: ReturnFact{Kind: AliasFresh}}
	require.False(t, fact.carries(Axis("throws")))
	require.False(t, fact.answers(Axis("throws")))
}

// They also disagree on an `unknown` return, which is a value the converter can
// act on and an open question for a reviewer.
func TestCarriesAndAnswersDisagreeOnAnUnknownReturn(t *testing.T) {
	t.Parallel()

	fact := MethodFact{Receiver: RecvBorrow, Returns: ReturnFact{Kind: AliasUnknown}}
	require.True(t, fact.carries(AxisReturns))
	require.False(t, fact.answers(AxisReturns))
}

// The tallies over every published builtin, which move when the mutation
// analysis, the origin map, the classification, or curated.json changes. Each
// determination is counted on its own, so the return-alias distribution spans
// every builtin while the receiver one leaves out any method that withholds it.
//
// No method withholds one today. The analysis leaves 24 receivers open and the
// curated layer answers all 24, which is why `receiver unclassified` is absent
// rather than zero.
func TestFactsTallies(t *testing.T) {
	counts := map[string]int{}
	for _, fact := range testFacts(t).Methods {
		counts["total"]++
		if fact.Receiver != "" {
			counts["receiver "+string(fact.Receiver)]++
		} else {
			counts["receiver unclassified"]++
		}
		if fact.Returns.Kind != "" {
			counts["returns "+string(fact.Returns.Kind)]++
		} else {
			counts["returns unclassified"]++
		}
	}

	lines := make([]string, 0, len(counts))
	for key, count := range counts {
		lines = append(lines, fmt.Sprintf("%s: %d", key, count))
	}
	sort.Strings(lines)
	snaps.MatchInlineSnapshot(t, strings.Join(lines, "\n"), snaps.Inline(`receiver borrow: 245
receiver mutBorrow: 68
receiver none: 188
returns fresh: 232
returns param: 6
returns receiver: 15
returns union: 1
returns unknown: 247
total: 501`))
}

// The seed surface §8.2 reads. Every name here is an annotation a curator
// writes, so the snapshot is the work list, and a spec bump that adds or
// retires a borrowing return moves it rather than staling a hand-kept list in
// the planning docs.
func TestFactsReturnSeedsAreListed(t *testing.T) {
	report := testFacts(t).ReturnSeeds()

	var sb strings.Builder
	require.NoError(t, WriteReturnsReport(report, &sb))
	snaps.MatchSnapshot(t, sb.String())

	// The three tallies partition the published builtins, so nothing falls
	// between the returns that need an annotation and the two kinds that
	// need none.
	require.Equal(t, len(testFacts(t).Methods), len(report.Seeds)+report.Owned+report.Open)
	// The open count is the same set Unclassified names on the returns axis.
	require.Equal(t, len(testFacts(t).Unclassified(AxisReturns)), report.Open)
}

// A `fresh` return is owned and an `unknown` one names no value, so neither
// reaches the seed list. `Demo.prototype.read` returns its receiver and
// `Demo.prototype.opaque` resolves to the lattice top.
func TestReturnSeedsExcludeTheKindsThatSeedNothing(t *testing.T) {
	_, methods := demoFacts(t)
	facts := &Facts{Methods: methods}

	report := facts.ReturnSeeds()

	require.Equal(t, []ReturnSeed{
		{Name: "Demo.prototype.read", Fact: ReturnFact{Kind: AliasReceiver}},
	}, report.Seeds)
	require.Equal(t, 0, report.Owned)
	require.Equal(t, 1, report.Open)
}

// Seeds sort by alias kind, then by the returned position, then by name, so a
// curator reads the parameter returns in position order rather than in the
// order `param(10)` would take lexically.
func TestReturnSeedsSortByKindThenPositionThenName(t *testing.T) {
	facts := &Facts{Methods: map[string]MethodFact{
		"C.two":   {Returns: returnsParam(10)},
		"C.one":   {Returns: returnsParam(2)},
		"C.three": {Returns: ReturnFact{Kind: AliasReceiver}},
		"C.four":  {Returns: ReturnFact{Kind: AliasUnion, Members: []AliasRef{{Kind: AliasFresh}, {Kind: AliasParam, Index: position(0)}}}},
		"C.five":  {Returns: returnsParam(2)},
	}}

	var names []string
	for _, seed := range facts.ReturnSeeds().Seeds {
		names = append(names, seed.Fact.String()+" "+seed.Name)
	}

	require.Equal(t, []string{
		"param(2) C.five",
		"param(2) C.one",
		"param(10) C.two",
		"receiver C.three",
		"union(fresh, param(0)) C.four",
	}, names)
}
