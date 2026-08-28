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
	analyzedOnce  sync.Once
	analyzedFacts *Facts
)

// testFacts is the published fact set over the committed graph, curated layer
// merged in, classified once for the whole package. A test pinning what the
// converter consumes reads this.
func testFacts(t *testing.T) *Facts {
	t.Helper()
	cfg := testCFG(t)
	factsOnce.Do(func() {
		allFacts = NewFacts(cfg)
	})
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

// fullCoverage is what NewFacts builds for a builtin it read whole.
var fullCoverage = Coverage{Receiver: true, Returns: true}

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
			MethodFact{Classified: fullCoverage, Receiver: RecvNone, Returns: returnsParam(2)},
			"receiver:none returns:param(2)",
		},
		// A parameter return with no position reads as the missing position
		// rather than as position 0. Nothing in the package builds such a fact;
		// a hand-edited facts.json is where one would come from.
		"ParamReturnWithNoPosition": {
			MethodFact{Classified: fullCoverage, Receiver: RecvNone, Returns: ReturnFact{Kind: AliasParam}},
			"receiver:none returns:param(?)",
		},
		// A union names every value it joined, which is what §8.2 spells the
		// lifetime union from.
		"UnionOverThreeValues": {
			MethodFact{Classified: fullCoverage, Receiver: RecvBorrow, Returns: ReturnFact{Kind: AliasUnion, Members: []AliasRef{
				{Kind: AliasFresh},
				{Kind: AliasParam, Index: position(1)},
				{Kind: AliasReceiver},
			}}},
			"receiver:borrow returns:union(fresh, param(1), receiver)",
		},
		// A union with no members is the counterpart of the positionless
		// parameter return above, and comes from the same place.
		"UnionWithNoMembers": {
			MethodFact{Classified: fullCoverage, Receiver: RecvNone, Returns: ReturnFact{Kind: AliasUnion}},
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
		// A caller of an algorithm the analysis could not read whole is
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
			require.Equal(t, test.want, analyzedFactOf(t, test.method).String())
		})
	}
}

// The §4.3 gate's last spot-check. Every String method coerces its receiver
// with `ToString` before reading it, and §4.2 makes that coercion's result a
// fresh primitive, so no write can reach the receiver. The seven left out are
// the ones the analysis could not read whole, so they withhold the receiver
// claim this checks.
func TestFactsEveryStringMethodBorrowsItsReceiver(t *testing.T) {
	var borrowed int
	var unread []string
	for name, fact := range testAnalyzedFacts(t).Methods {
		if !strings.HasPrefix(name, "String.prototype") {
			continue
		}
		if !fact.Classified.Receiver {
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
		require.True(t, fact.Classified.Returns, "%s has no return alias", fn.Name)
		require.NotEmpty(t, fact.Returns.Kind, "%s covers a return alias it does not carry", fn.Name)
		switch {
		case fn.Kind != BuiltinMethod:
			// `none` follows from the function kind, so no warning withholds it.
			require.True(t, fact.Classified.Receiver, "%s has no receiver claim", fn.Name)
			require.Equal(t, RecvNone, fact.Receiver, "%s has no receiver", fn.Name)
		case !fact.Classified.Receiver:
			require.Empty(t, fact.Receiver, "%s carries a receiver claim it is not classified for", fn.Name)
		default:
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

	fact, ok := NewFacts(cfg).Of("Demo.pick")
	require.True(t, ok)
	require.Equal(t, "receiver:none returns:union(param(0), param(1))", fact.String())

	encoded, err := json.Marshal(fact)
	require.NoError(t, err)
	require.Equal(t,
		`{"classified":{"receiver":true,"returns":true},"receiver":"none",`+
			`"returns":{"kind":"union","members":[{"kind":"param","index":0},{"kind":"param","index":1}]}}`,
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

// A fact serializes as Appendix B describes. A determination its coverage
// leaves unset omits its field, so the converter cannot read an absent claim
// as a proven-empty one.
func TestFactsJSON(t *testing.T) {
	tests := map[string]struct {
		fact MethodFact
		want string
	}{
		"Method": {factOf(t, "Array.prototype.fill"), `{"classified":{"receiver":true,"returns":true},"receiver":"mutBorrow","returns":{"kind":"receiver"}}`},
		// A withheld receiver falls through to FR5's `&mut self`, so the entry
		// carries the return alias alone.
		"WithheldReceiver": {analyzedFactOf(t, "Array.prototype.toLocaleString"), `{"classified":{"receiver":false,"returns":true},"returns":{"kind":"fresh"}}`},
		// The first parameter is written out like any other position. Every
		// parameter the committed graph returns sits at 0, so omitting it would
		// leave the index absent from the whole file and spell the common case
		// as missing data.
		"ReturnedPositionZero": {factOf(t, "Object.freeze"), `{"classified":{"receiver":true,"returns":true},"receiver":"none","returns":{"kind":"param","index":0}}`},
		"ReturnedPositionPastZero": {
			MethodFact{Classified: fullCoverage, Receiver: RecvNone, Returns: returnsParam(2)},
			`{"classified":{"receiver":true,"returns":true},"receiver":"none","returns":{"kind":"param","index":2}}`,
		},
		// A fact that returns no parameter carries no position at all.
		"NoReturnedPosition": {factOf(t, "Array.prototype.push"), `{"classified":{"receiver":true,"returns":true},"receiver":"mutBorrow","returns":{"kind":"fresh"}}`},
		// The §4.3 union gate. The `Object` constructor hands back an
		// `OrdinaryObjectCreate` result on one path and `ToObject(value)` on
		// another, and the entry names both so §8.2 can spell the lifetime
		// union they seed.
		"Union": {factOf(t, "Object"), `{"classified":{"receiver":true,"returns":true},"receiver":"none","returns":{"kind":"union","members":[{"kind":"fresh"},{"kind":"param","index":0}]}}`},
		// A fact covering no determination carries neither field. `returns` is
		// a struct rather than a kind, so its absence is spelled by omitzero.
		"NoDetermination": {MethodFact{}, `{"classified":{"receiver":false,"returns":false}}`},
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
	snaps.MatchSnapshot(t, strings.Join(testAnalyzedFacts(t).Unclassified(), "\n"))
	require.Empty(t, testFacts(t).Unclassified())
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
		if fact.Classified.Receiver {
			counts["receiver "+string(fact.Receiver)]++
		} else {
			counts["receiver unclassified"]++
		}
		if fact.Classified.Returns {
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
	snaps.MatchInlineSnapshot(t, strings.Join(lines, "\n"), snaps.Inline(`receiver borrow: 246
receiver mutBorrow: 67
receiver none: 188
returns fresh: 232
returns param: 6
returns receiver: 15
returns union: 1
returns unknown: 247
total: 501`))
}
