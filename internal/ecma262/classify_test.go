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
	factsOnce sync.Once
	allFacts  *Facts
)

// testFacts classifies the committed graph once for the whole package.
func testFacts(t *testing.T) *Facts {
	t.Helper()
	cfg := testCFG(t)
	factsOnce.Do(func() {
		allFacts = NewFacts(cfg)
	})
	return allFacts
}

// position is the 0-based parameter position a fact returns, as MethodFact
// holds it.
func position(i int) *int {
	return &i
}

// factOf returns the fact for one builtin, failing when the graph does not
// hold it.
func factOf(t *testing.T, name string) MethodFact {
	t.Helper()
	fact, ok := testFacts(t).Of(name)
	require.True(t, ok, "no builtin named %s", name)
	return fact
}

func TestMethodFactString(t *testing.T) {
	tests := map[string]struct {
		fact MethodFact
		want string
	}{
		// Every case in TestFactsSampleMethods renders a classified fact
		// through String, so only what those cases cannot show is stated here:
		// a fact with nothing to render, and a returned position past 0, which
		// no builtin has.
		"Unclassified": {MethodFact{}, "unclassified"},
		"ParamReturn": {
			MethodFact{Classified: true, Receiver: RecvNone, Returns: AliasParam, ParamIndex: position(2)},
			"receiver:none returns:param(2)",
		},
		// A parameter return with no position reads as the missing position
		// rather than as position 0. Nothing in the package builds such a fact;
		// a hand-edited facts.json is where one would come from.
		"ParamReturnWithNoPosition": {
			MethodFact{Classified: true, Receiver: RecvNone, Returns: AliasParam},
			"receiver:none returns:param(?)",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, test.want, test.fact.String())
		})
	}
}

func TestAliasJoin(t *testing.T) {
	receiver := alias{Kind: AliasReceiver}
	fresh := alias{Kind: AliasFresh}
	union := alias{Kind: AliasUnion}
	unknown := alias{Kind: AliasUnknown}

	tests := map[string]struct {
		a, b alias
		want alias
	}{
		// The bottom of the lattice is the accumulator before any return has
		// been read, so it never contributes. The loop below joins each pair
		// both ways, which covers the bottom arriving on either side.
		"UnsetTakesTheOther": {alias{}, receiver, receiver},
		// Two returns that agree keep their alias, and a position is part of
		// what they have to agree on.
		"AgreeingReturns":        {fresh, fresh, fresh},
		"AgreeingParamPositions": {aliasOf(Param(1)), aliasOf(Param(1)), aliasOf(Param(1))},
		// Two returns that disagree collapse to a union. A fresh return counts
		// as a distinct value the same way an input origin does, which §4.3
		// states for two input origins only.
		"ReceiverAndFresh": {receiver, fresh, union},
		// Two positions disagree the way two kinds do. No builtin in the
		// committed graph returns two different parameters, so this is the only
		// place the case is stated.
		"DifferingParamPosition": {aliasOf(Param(0)), aliasOf(Param(1)), union},
		// A third return that agrees with neither leaves the union standing.
		"UnionAbsorbsAnAgreeingReturn": {union, receiver, union},
		// Unknown is the top, so it wins over a union too.
		"UnknownWinsOverAKnownReturn": {receiver, unknown, unknown},
		"UnknownWinsOverAUnion":       {union, unknown, unknown},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, test.want, test.a.join(test.b))
			require.Equal(t, test.want, test.b.join(test.a), "join is not commutative")
		})
	}
}

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
		"ReturnsDifferingOrigins": {"Object", "receiver:none returns:union"},
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
		// A borrow claim is only as good as the mutation analysis behind it.
		// delete empties its entry with `Set p.[[Key]] to EMPTY`, a write to a
		// Map Entry Record read out of `M.[[MapData]]`, and §4.1 places
		// neither half. The method is classified because no warning stands, so
		// the wrong answer is published rather than handed to the heuristics.
		// §6's validation diff against the hand-written overrides is where the
		// three methods of this shape are triaged.
		"MutationTheAnalysisDoesNotSee": {"Map.prototype.delete", "receiver:borrow returns:fresh"},
		// A caller of an algorithm the analysis could not read whole is
		// classified all the same, since §4.1 charges `Unattributable` up the
		// call graph and `Incomplete` not at all. next resumes the generator
		// through `GeneratorResume`, which carries the warning.
		"CallerOfAnIncompleteAlgorithm": {"GeneratorPrototype.next", "receiver:borrow returns:unknown"},
		// A method carrying either warning is handed to the name heuristics
		// whole. toLowerCase reaches the Unicode case-mapping table through a
		// prose step, which leaves the analysis unable to read the algorithm.
		"IncompleteMethod": {"String.prototype.toLowerCase", "unclassified"},
		// Array.of builds its array through `Construct(C, ...)`, whose result
		// the origin map cannot place, so the write to it is unattributable.
		"UnattributableStatic": {"Array.of", "unclassified"},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, test.want, factOf(t, test.method).String())
		})
	}
}

// The §4.3 gate's last spot-check. Every String method coerces its receiver
// with `ToString` before reading it, and §4.2 makes that coercion's result a
// fresh primitive, so no write can reach the receiver. The seven left out are
// the ones the analysis could not read at all, which carry no receiver claim
// to check.
func TestFactsEveryStringMethodBorrowsItsReceiver(t *testing.T) {
	var borrowed int
	var unread []string
	for name, fact := range testFacts(t).Methods {
		if !strings.HasPrefix(name, "String.prototype") {
			continue
		}
		if !fact.Classified {
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
		if !fact.Classified {
			require.Equal(t, MethodFact{}, fact, "%s carries a claim it is not classified for", fn.Name)
			continue
		}
		if fn.Kind == BuiltinMethod {
			require.Contains(t, []ReceiverKind{RecvBorrow, RecvMutBorrow}, fact.Receiver, fn.Name)
		} else {
			require.Equal(t, RecvNone, fact.Receiver, "%s has no receiver", fn.Name)
		}
		if fact.Returns == AliasParam {
			require.NotNil(t, fact.ParamIndex, "%s returns a parameter but no position", fn.Name)
			require.Less(t, *fact.ParamIndex, len(fn.Params), "%s: returned position", fn.Name)
			require.GreaterOrEqual(t, *fact.ParamIndex, 0, "%s: returned position", fn.Name)
		} else {
			require.Nil(t, fact.ParamIndex, "%s carries a position it does not return", fn.Name)
		}
	}
	// Each builtin resolved above, so an equal count leaves no room for
	// anything else. Reading the count rather than each abstract operation's
	// name is what keeps `Set` from failing it, since that name belongs to
	// both the operations and the constructors.
	require.Equal(t, builtins, len(f.Methods))
}

// A name the graph does not hold is missing rather than unclassified. The §5
// join reports the two differently: one is a declaration the spec has no
// algorithm for, the other an algorithm the analysis could not read.
func TestFactsOfAnAbsentName(t *testing.T) {
	fact, ok := testFacts(t).Of("Array.prototype.nosuchmethod")
	require.False(t, ok)
	require.Equal(t, MethodFact{}, fact)
}

// A classified fact serializes as Appendix B describes, and an unclassified one
// carries nothing beside the flag, so the converter cannot read an absent claim
// as a proven-empty one.
func TestFactsJSON(t *testing.T) {
	tests := map[string]struct {
		fact MethodFact
		want string
	}{
		"Method":       {factOf(t, "Array.prototype.fill"), `{"classified":true,"receiver":"mutBorrow","returns":"receiver"}`},
		"Unclassified": {factOf(t, "String.prototype.toLowerCase"), `{"classified":false}`},
		// The first parameter is written out like any other position. Every
		// parameter the committed graph returns sits at 0, so omitting it would
		// leave paramIndex absent from the whole file and spell the common case
		// as missing data.
		"ReturnedPositionZero": {factOf(t, "Object.freeze"), `{"classified":true,"receiver":"none","returns":"param","paramIndex":0}`},
		"ReturnedPositionPastZero": {
			MethodFact{Classified: true, Receiver: RecvNone, Returns: AliasParam, ParamIndex: position(2)},
			`{"classified":true,"receiver":"none","returns":"param","paramIndex":2}`,
		},
		// A fact that returns no parameter carries no position at all.
		"NoReturnedPosition": {factOf(t, "Array.prototype.push"), `{"classified":true,"receiver":"mutBorrow","returns":"fresh"}`},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			encoded, err := json.Marshal(test.fact)
			require.NoError(t, err)
			require.Equal(t, test.want, string(encoded))
		})
	}
}

// The methods FR5 hands to the converter's name heuristics. Each carries a
// mutation the analysis could not place or a step it could not read, so it is
// emitted with no claim at all rather than with a guess.
//
// This list is the §4 objective made visible: shrinking it is what a change to
// the analysis is measured by, and the tallies below record the same number
// against the classified ones.
func TestFactsUnclassifiedMethodsAreListed(t *testing.T) {
	snaps.MatchSnapshot(t, strings.Join(testFacts(t).Unclassified(), "\n"))
}

// The tallies over every builtin, which move when the mutation analysis, the
// origin map, or the classification changes.
func TestFactsTallies(t *testing.T) {
	counts := map[string]int{}
	for _, fact := range testFacts(t).Methods {
		counts["total"]++
		if !fact.Classified {
			counts["unclassified"]++
			continue
		}
		counts["receiver "+string(fact.Receiver)]++
		counts["returns "+string(fact.Returns)]++
	}

	lines := make([]string, 0, len(counts))
	for key, count := range counts {
		lines = append(lines, fmt.Sprintf("%s: %d", key, count))
	}
	sort.Strings(lines)
	snaps.MatchInlineSnapshot(t, strings.Join(lines, "\n"), snaps.Inline(`receiver borrow: 225
receiver mutBorrow: 57
receiver none: 138
returns fresh: 177
returns param: 6
returns receiver: 15
returns union: 1
returns unknown: 221
total: 501
unclassified: 81`))
}
