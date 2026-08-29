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
	filterOnce   sync.Once
	filterReport FilterReport
)

// testFilterReport runs the coercion filter over the committed graph once for
// the whole package. It reads the analysis alone, so a curated entry cannot
// hide what the filter did.
func testFilterReport(t *testing.T) FilterReport {
	t.Helper()
	cfg := testCFG(t)
	filterOnce.Do(func() {
		filterReport = analyze(cfg).Filter()
	})
	return filterReport
}

// decisionsFor returns the filter's decisions for one builtin, rendered one per
// line.
func decisionsFor(t *testing.T, method string) string {
	t.Helper()
	var lines []string
	for _, decision := range testFilterReport(t).Decisions {
		if decision.Method == method {
			lines = append(lines, decision.String())
		}
	}
	return strings.Join(lines, "\n")
}

// The §9.2 gate. `Number.prototype.toFixed` keeps the `RangeError` it raises on
// a `fractionDigits` outside 0..100 and drops the `TypeError`
// `ThisNumberValue(this value)` raises on a receiver that is not a Number.
//
// Its other `TypeError` sites survive, so the published channel still names the
// class. They come from `ToNumber(fractionDigits)`, a coercion of a parameter,
// and the receiver branch is the only one built: proving that path unreachable
// needs the declared type of `fractionDigits`, which the shape-free facts do
// not carry.
func TestFilterKeepsADomainThrowAndDropsAReceiverCoercion(t *testing.T) {
	snaps.MatchInlineSnapshot(t, decisionsFor(t, "Number.prototype.toFixed"), snaps.Inline(`Number.prototype.toFixed: dropped #1 TypeError <- ThisNumberValue#12 [ThisNumberValue of receiver]
Number.prototype.toFixed: kept #3 TypeError <- ToIntegerOrInfinity#0 <- ToNumber#23 <- ToPrimitive#1 <- GetMethod#0 <- GetV#0 <- ToObject#3 [ToObject of param:0]
Number.prototype.toFixed: kept #3 TypeError <- ToIntegerOrInfinity#0 <- ToNumber#23 <- ToPrimitive#1 <- GetMethod#0 <- GetV#0 <- ToObject#6 [ToObject of param:0]
Number.prototype.toFixed: kept #3 TypeError <- ToIntegerOrInfinity#0 <- ToNumber#23 <- ToPrimitive#1 <- GetMethod#8
Number.prototype.toFixed: kept #3 TypeError <- ToIntegerOrInfinity#0 <- ToNumber#23 <- ToPrimitive#17
Number.prototype.toFixed: kept #3 TypeError <- ToIntegerOrInfinity#0 <- ToNumber#23 <- ToPrimitive#20 <- OrdinaryToPrimitive#20
Number.prototype.toFixed: kept #3 TypeError <- ToIntegerOrInfinity#0 <- ToNumber#23 <- ToPrimitive#9 <- Call#5
Number.prototype.toFixed: kept #3 TypeError <- ToIntegerOrInfinity#0 <- ToNumber#7 [ToNumber of param:0]`),
	)

	fact := analyzedFactOf(t, "Number.prototype.toFixed")
	require.Equal(t, []string{"RangeError", "TypeError"}, fact.Throws)
	require.Empty(t, fact.Rejects)
}

// A receiver coercion is filtered whichever operation performs it. charAt opens
// with `? RequireObjectCoercible(this value)` and then `? ToString(O)`, and both
// checks read a value Escalier types as the receiver.
//
// The same `ToString` chain applied to `pos` is kept, so one method shows the
// two branches side by side.
func TestFilterDropsEveryCoercionOfTheReceiver(t *testing.T) {
	snaps.MatchInlineSnapshot(t, decisionsFor(t, "String.prototype.charAt"), snaps.Inline(`String.prototype.charAt: dropped #1 TypeError <- RequireObjectCoercible#5 [RequireObjectCoercible of receiver]
String.prototype.charAt: dropped #3 TypeError <- ToString#32 <- ToPrimitive#1 <- GetMethod#0 <- GetV#0 <- ToObject#3 [ToObject of receiver]
String.prototype.charAt: dropped #3 TypeError <- ToString#32 <- ToPrimitive#1 <- GetMethod#0 <- GetV#0 <- ToObject#6 [ToObject of receiver]
String.prototype.charAt: kept #3 TypeError <- ToString#32 <- ToPrimitive#1 <- GetMethod#8
String.prototype.charAt: kept #3 TypeError <- ToString#32 <- ToPrimitive#17
String.prototype.charAt: kept #3 TypeError <- ToString#32 <- ToPrimitive#20 <- OrdinaryToPrimitive#20
String.prototype.charAt: kept #3 TypeError <- ToString#32 <- ToPrimitive#9 <- Call#5
String.prototype.charAt: dropped #3 TypeError <- ToString#7 [ToString of receiver]
String.prototype.charAt: kept #5 TypeError <- ToIntegerOrInfinity#0 <- ToNumber#23 <- ToPrimitive#1 <- GetMethod#0 <- GetV#0 <- ToObject#3 [ToObject of param:0]
String.prototype.charAt: kept #5 TypeError <- ToIntegerOrInfinity#0 <- ToNumber#23 <- ToPrimitive#1 <- GetMethod#0 <- GetV#0 <- ToObject#6 [ToObject of param:0]
String.prototype.charAt: kept #5 TypeError <- ToIntegerOrInfinity#0 <- ToNumber#23 <- ToPrimitive#1 <- GetMethod#8
String.prototype.charAt: kept #5 TypeError <- ToIntegerOrInfinity#0 <- ToNumber#23 <- ToPrimitive#17
String.prototype.charAt: kept #5 TypeError <- ToIntegerOrInfinity#0 <- ToNumber#23 <- ToPrimitive#20 <- OrdinaryToPrimitive#20
String.prototype.charAt: kept #5 TypeError <- ToIntegerOrInfinity#0 <- ToNumber#23 <- ToPrimitive#9 <- Call#5
String.prototype.charAt: kept #5 TypeError <- ToIntegerOrInfinity#0 <- ToNumber#7 [ToNumber of param:0]`),
	)
}

// A namespace function has no receiver, so nothing it coerces reaches the one
// branch the filter builds. decodeURIComponent keeps the `URIError` its decode
// step raises and the `TypeError` from `ToString(encodedURIComponent)` alike.
func TestFilterDropsNothingWithoutAReceiver(t *testing.T) {
	for _, decision := range testFilterReport(t).Decisions {
		if decision.Method == "decodeURIComponent" {
			require.Falsef(t, decision.Dropped, "%s", decision)
		}
	}
	require.Equal(t, []string{"TypeError", "URIError"}, analyzedFactOf(t, "decodeURIComponent").Throws)
}

// A throw the chain reaches below a coercion is kept, because it reports the
// caller's own code failing rather than a type the declaration rules out.
// `ToString` on an object calls `ToPrimitive`, which looks up and invokes the
// object's `@@toPrimitive` method, and the throws under that are the lookup
// finding a non-callable, the method itself raising, and the method handing
// back an object.
//
// The chains reach `GetMethod`, `Call`, `OrdinaryToPrimitive`, and
// `ToPrimitive`'s own throw for a method that returned an object. None of those
// is a coercion, so the guard is unnamed rather than named and untraced.
func TestFilterKeepsAThrowBelowACoercion(t *testing.T) {
	var below []string
	for _, decision := range testFilterReport(t).Decisions {
		if decision.Method == "String.prototype.charAt" && decision.Coercion == "" {
			below = append(below, decision.Site)
		}
	}
	require.Equal(t, []string{
		"#3 TypeError <- ToString#32 <- ToPrimitive#1 <- GetMethod#8",
		"#3 TypeError <- ToString#32 <- ToPrimitive#17",
		"#3 TypeError <- ToString#32 <- ToPrimitive#20 <- OrdinaryToPrimitive#20",
		"#3 TypeError <- ToString#32 <- ToPrimitive#9 <- Call#5",
		"#5 TypeError <- ToIntegerOrInfinity#0 <- ToNumber#23 <- ToPrimitive#1 <- GetMethod#8",
		"#5 TypeError <- ToIntegerOrInfinity#0 <- ToNumber#23 <- ToPrimitive#17",
		"#5 TypeError <- ToIntegerOrInfinity#0 <- ToNumber#23 <- ToPrimitive#20 <- OrdinaryToPrimitive#20",
		"#5 TypeError <- ToIntegerOrInfinity#0 <- ToNumber#23 <- ToPrimitive#9 <- Call#5",
	}, below)
}

// A coercion of a value read out of the receiver is kept. push reads its length
// with `? LengthOfArrayLike(O)`, which coerces `Get(O, "length")` — a value the
// receiver holds rather than the receiver, and one a getter on the prototype
// chain can make anything at all. The decision names the operation and reports
// the value untraced.
//
// The two `ToObject(this value)` sites at step 0 are dropped in the same method,
// so the difference is the value each checks rather than the operation.
func TestFilterKeepsACoercionOfAnInteriorValue(t *testing.T) {
	snaps.MatchInlineSnapshot(t, decisionsFor(t, "Array.prototype.push"), snaps.Inline(`Array.prototype.push: dropped #0 TypeError <- ToObject#3 [ToObject of receiver]
Array.prototype.push: dropped #0 TypeError <- ToObject#6 [ToObject of receiver]
Array.prototype.push: kept #2 TypeError <- LengthOfArrayLike#1 <- ToLength#0 <- ToIntegerOrInfinity#0 <- ToNumber#23 <- ToPrimitive#1 <- GetMethod#0 <- GetV#0 <- ToObject#3 [ToObject, value untraced]
Array.prototype.push: kept #2 TypeError <- LengthOfArrayLike#1 <- ToLength#0 <- ToIntegerOrInfinity#0 <- ToNumber#23 <- ToPrimitive#1 <- GetMethod#0 <- GetV#0 <- ToObject#6 [ToObject, value untraced]
Array.prototype.push: kept #2 TypeError <- LengthOfArrayLike#1 <- ToLength#0 <- ToIntegerOrInfinity#0 <- ToNumber#23 <- ToPrimitive#1 <- GetMethod#8
Array.prototype.push: kept #2 TypeError <- LengthOfArrayLike#1 <- ToLength#0 <- ToIntegerOrInfinity#0 <- ToNumber#23 <- ToPrimitive#17
Array.prototype.push: kept #2 TypeError <- LengthOfArrayLike#1 <- ToLength#0 <- ToIntegerOrInfinity#0 <- ToNumber#23 <- ToPrimitive#20 <- OrdinaryToPrimitive#20
Array.prototype.push: kept #2 TypeError <- LengthOfArrayLike#1 <- ToLength#0 <- ToIntegerOrInfinity#0 <- ToNumber#23 <- ToPrimitive#9 <- Call#5
Array.prototype.push: kept #2 TypeError <- LengthOfArrayLike#1 <- ToLength#0 <- ToIntegerOrInfinity#0 <- ToNumber#7 [ToNumber, value untraced]
Array.prototype.push: kept #7 TypeError
Array.prototype.push: kept #13 TypeError <- Set#4
Array.prototype.push: kept #16 TypeError <- Set#4`),
	)
}

// Only a `TypeError` is adjudicated. Every other class says something about the
// values a well-typed caller can pass rather than about their types, so the
// filter never sees it: `RangeError` on an out-of-range index, `URIError` on a
// malformed escape, `SyntaxError` on an unparseable string.
func TestFilterAdjudicatesTypeErrorsAlone(t *testing.T) {
	for _, decision := range testFilterReport(t).Decisions {
		require.Truef(t, strings.Contains(decision.Site, " TypeError"),
			"%s is not a TypeError site", decision)
	}
}

// The whole run's tallies, and the operations whose guards it dropped. This is
// the §9.2 review report in summary form: a change to coercionAOs or to the
// threading moves these numbers, and the reviewer sees which operation moved.
func TestFilterReportTallies(t *testing.T) {
	report := testFilterReport(t)
	adjudicated, dropped := report.Counts()

	guards := map[string]int{}
	for _, decision := range report.Dropped() {
		guards[decision.Coercion]++
	}
	require.Equal(t, 4882, adjudicated)
	require.Equal(t, 242, dropped)
	require.Equal(t, map[string]int{
		"RequireObjectCoercible": 31,
		"ThisBigIntValue":        2,
		"ThisBooleanValue":       2,
		"ThisNumberValue":        5,
		"ThisStringValue":        2,
		"ThisSymbolValue":        4,
		"ToObject":               166,
		"ToString":               30,
	}, guards)
}

// Every dropped site names the receiver as the value it checked, which is the
// one branch §9.2 builds. A drop that named a parameter would be the branch
// #1301 closed, reintroduced by accident.
func TestFilterDropsOnlyReceiverCoercions(t *testing.T) {
	for _, decision := range testFilterReport(t).Dropped() {
		require.Equalf(t, "receiver", decision.Coerced, "%s drops a value that is not the receiver", decision)
	}
}

// The report reads as a tally line and one line per dropped site. A kept site
// stays out of it: the published fact already names the exception it
// contributes, so printing it would bury the heuristic's decisions in the
// throws the analysis stands behind.
func TestWriteFilterReport(t *testing.T) {
	t.Parallel()

	var out strings.Builder
	require.NoError(t, WriteFilterReport(FilterReport{Decisions: []FilterDecision{
		{
			Method:   "Demo.prototype.read",
			Site:     "#0 TypeError <- ToObject#3",
			Coercion: "ToObject",
			Coerced:  "receiver",
			Dropped:  true,
		},
		{
			Method:   "Demo.prototype.read",
			Site:     "#2 TypeError <- ToNumber#7",
			Coercion: "ToNumber",
			Coerced:  "param:0",
		},
		{Method: "Demo.prototype.read", Site: "#4 TypeError"},
	}}, &out))

	snaps.MatchInlineSnapshot(t, out.String(), snaps.Inline(`  coercion filter: 3 TypeError sites adjudicated, 1 dropped, 0 methods under-reported
    Demo.prototype.read: dropped #0 TypeError <- ToObject#3 [ToObject of receiver]
`),
	)
}

// A rejection the combinator model supplies has no site to adjudicate, so it
// reaches the published channel whatever the filter does to the sites beside
// it. `Promise.all` forwards the reject type of its element promises and
// rejects with a `TypeError` of its own on a non-iterable argument.
func TestFilterCarriesModeledRejections(t *testing.T) {
	fact := analyzedFactOf(t, "Promise.all")
	require.Equal(t, []string{"TypeError", "elementErrOf:param:0", "unknown"}, fact.Rejects)
}

// The throw steps behind coercionAOs, one line per operation. Each entry was
// added by reading the operation in ECMA-262 and confirming that every
// `TypeError` it raises of its own reports the wrong dynamic type for the value
// at coercionGuardArg, which is what makes a receiver coercion unreachable for
// a well-typed caller. The graph carries the step but not the reason, so the
// snapshot pins what was read rather than proving it.
//
// A spec bump that adds or moves a throw step in one of these operations shows
// up here. That is the prompt to re-read the operation against FR11, not to
// update the snapshot.
//
// `ToNumeric` raises nothing of its own. It delegates to `ToPrimitive` and
// `ToNumber`, so it never bottoms out a chain, and it is listed because FR11
// names it and a later spec revision could give it a check of its own.
func TestCoercionAOsRaiseOnTheirFirstArgument(t *testing.T) {
	cfg := testCFG(t)
	var lines []string
	for _, name := range sortedStrings(coercionAOs.ToSlice()) {
		fn := cfg.AbstractOp(name)
		require.NotNilf(t, fn, "%s is not an abstract operation the graph holds", name)
		require.Greaterf(t, len(fn.Params), coercionGuardArg,
			"%s declares no parameter at the position the filter reads", name)
		lines = append(lines, fmt.Sprintf("%s(%s) raises at %s",
			name, fn.Params[coercionGuardArg], throwSteps(fn)))
	}

	snaps.MatchInlineSnapshot(t, strings.Join(lines, "\n"), snaps.Inline(`RequireObjectCoercible(argument) raises at #5 TypeError
ThisBigIntValue(value) raises at #12 TypeError
ThisBooleanValue(value) raises at #12 TypeError
ThisNumberValue(value) raises at #12 TypeError
ThisStringValue(value) raises at #12 TypeError
ThisSymbolValue(value) raises at #12 TypeError
ToNumber(argument) raises at #7 TypeError
ToNumeric(value) raises at no step of its own
ToObject(argument) raises at #3 TypeError, #6 TypeError
ToString(argument) raises at #7 TypeError`))
}

// `ToPrimitive` is the coercion the list leaves out. Its one `Throw` step is
// the one ECMA-262 reaches after an `@@toPrimitive` method has handed back an
// object rather than a primitive, so it reports the caller's own code failing
// and not a check on the value `ToPrimitive` was given. A declared receiver
// type rules out neither.
//
// Listing it would drop 31 sites over the committed graph. `ToPrimitive` still
// appears mid-chain, where the base is the `ToObject` inside its
// `@@toPrimitive` lookup, and that base is weighed on its own account.
func TestToPrimitiveIsNotACoercionGuard(t *testing.T) {
	require.NotContains(t, coercionAOs.ToSlice(), "ToPrimitive")

	fn := testCFG(t).AbstractOp("ToPrimitive")
	require.NotNil(t, fn)
	require.Equal(t, "#17 TypeError", throwSteps(fn))

	// Every site that bottoms out there is kept, and the decision names no
	// coercion, so the report reads it as a throw below a coercion rather than
	// as one the filter weighed and let through.
	var reached int
	for _, decision := range testFilterReport(t).Decisions {
		if !strings.HasSuffix(decision.Site, "<- ToPrimitive#17") {
			continue
		}
		reached++
		require.Falsef(t, decision.Dropped, "%s", decision)
		require.Emptyf(t, decision.Coercion, "%s", decision)
	}
	require.NotZero(t, reached)
}

// underReportedCFG is a two-method graph whose algorithms both hold a step the
// serializer could not lower, so the throw fixpoint reads neither whole.
// `read` returns a value, and `load` builds a promise and returns it.
const underReportedCFG = `{"specTarget":"abc","funcs":[` +
	`{"name":"Demo.prototype.read","kind":"builtin-method","params":[],"nodes":[` +
	`{"kind":"opaque","text":["Let _x_ be whatever the host decides."]}]},` +
	`{"name":"Demo.prototype.load","kind":"builtin-method","params":[],"promise":true,"nodes":[` +
	`{"kind":"opaque","text":["Let _x_ be whatever the host decides."]}]}]}`

// An unread step leaves the throws of both methods short and the rejections of
// only the one that builds a promise. `read` has no reject sink for the step to
// feed, so its empty rejection channel is a proven-empty result, and the report
// says so rather than claiming a gap on both channels.
//
// Both methods are named all the same, because FR10 asks for a method whose
// throw paths the analysis could not resolve to be flagged rather than guessed
// at, and that holds whichever channel is short.
func TestFilterReportsWhichChannelIsShort(t *testing.T) {
	t.Parallel()

	cfg, err := ParseCFG([]byte(underReportedCFG))
	require.NoError(t, err)
	facts := analyze(cfg)

	require.Equal(t, []UnderReport{
		{Method: "Demo.prototype.load", Rejects: true},
		{Method: "Demo.prototype.read"},
	}, facts.Filter().UnderReported)
	require.Equal(t, []string{"Demo.prototype.load", "Demo.prototype.read"}, facts.Unclassified(AxisThrows))
	require.Equal(t, []string{"Demo.prototype.load"}, facts.Unclassified(AxisRejects))

	var out strings.Builder
	require.NoError(t, WriteFilterReport(facts.Filter(), &out))
	snaps.MatchInlineSnapshot(t, out.String(), snaps.Inline(`  coercion filter: 0 TypeError sites adjudicated, 0 dropped, 2 methods under-reported
    Demo.prototype.load: a step the throw analysis could not read leaves its throws and its rejections short
    Demo.prototype.read: a step the throw analysis could not read leaves its throws short
`))
}

// throwSteps renders every step of fn that raises an error class it names, as
// its position and that class.
func throwSteps(fn *Func) string {
	var steps []string
	for i, node := range fn.Nodes {
		if throw, ok := node.(*ThrowNode); ok && throw.ErrorType != "" {
			steps = append(steps, fmt.Sprintf("#%d %s", i, throw.ErrorType))
		}
	}
	if len(steps) == 0 {
		return "no step of its own"
	}
	return strings.Join(steps, ", ")
}

// sortedStrings returns a copy of names in order, so a rendered list reads the
// same on every run.
func sortedStrings(names []string) []string {
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	return sorted
}
