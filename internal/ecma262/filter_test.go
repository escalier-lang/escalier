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

// Every step of a receiver coercion goes, not only the step that checks the
// type. charAt opens with `? RequireObjectCoercible(this value)` and then `?
// ToString(O)`, and its whole step 3 falls: `ToString`'s own Symbol check by the
// base rule, and the `@@toPrimitive` machinery past it because a String
// receiver leaves `ToString` at its first step and never reaches that call.
//
// The same operations applied to `pos` are all kept, so one method shows the
// two branches side by side and the value each coercion was handed is the whole
// difference.
func TestFilterDropsEveryCoercionOfTheReceiver(t *testing.T) {
	snaps.MatchInlineSnapshot(t, decisionsFor(t, "String.prototype.charAt"), snaps.Inline(`String.prototype.charAt: dropped #1 TypeError <- RequireObjectCoercible#5 [RequireObjectCoercible of receiver]
String.prototype.charAt: dropped #3 TypeError <- ToString#32 <- ToPrimitive#1 <- GetMethod#0 <- GetV#0 <- ToObject#3 [ToObject of receiver]
String.prototype.charAt: dropped #3 TypeError <- ToString#32 <- ToPrimitive#1 <- GetMethod#0 <- GetV#0 <- ToObject#6 [ToObject of receiver]
String.prototype.charAt: dropped #3 TypeError <- ToString#32 <- ToPrimitive#1 <- GetMethod#8 [past ToString of receiver]
String.prototype.charAt: dropped #3 TypeError <- ToString#32 <- ToPrimitive#17 [past ToString of receiver]
String.prototype.charAt: dropped #3 TypeError <- ToString#32 <- ToPrimitive#20 <- OrdinaryToPrimitive#20 [past ToString of receiver]
String.prototype.charAt: dropped #3 TypeError <- ToString#32 <- ToPrimitive#9 <- Call#5 [past ToString of receiver]
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

// A throw the chain reaches past a coercion of a parameter is kept. `charAt`
// coerces `pos` with `ToIntegerOrInfinity`, which reaches `ToNumber` and then
// the `@@toPrimitive` machinery, and the receiver branch settles none of it: a
// `pos` the declaration types loosely can be an object with a `@@toPrimitive`
// that raises.
//
// The same steps past the receiver's own `ToString` are dropped, which is what
// TestFilterDropsEveryCoercionOfTheReceiver holds. The two chains run through
// the same operations, so the value each coercion was handed is the whole
// difference.
func TestFilterKeepsAThrowPastACoercionOfAParameter(t *testing.T) {
	var kept []string
	for _, decision := range testFilterReport(t).Decisions {
		if decision.Method == "String.prototype.charAt" && !decision.Dropped {
			kept = append(kept, decision.Site)
		}
	}
	require.Equal(t, []string{
		"#5 TypeError <- ToIntegerOrInfinity#0 <- ToNumber#23 <- ToPrimitive#1 <- GetMethod#0 <- GetV#0 <- ToObject#3",
		"#5 TypeError <- ToIntegerOrInfinity#0 <- ToNumber#23 <- ToPrimitive#1 <- GetMethod#0 <- GetV#0 <- ToObject#6",
		"#5 TypeError <- ToIntegerOrInfinity#0 <- ToNumber#23 <- ToPrimitive#1 <- GetMethod#8",
		"#5 TypeError <- ToIntegerOrInfinity#0 <- ToNumber#23 <- ToPrimitive#17",
		"#5 TypeError <- ToIntegerOrInfinity#0 <- ToNumber#23 <- ToPrimitive#20 <- OrdinaryToPrimitive#20",
		"#5 TypeError <- ToIntegerOrInfinity#0 <- ToNumber#23 <- ToPrimitive#9 <- Call#5",
		"#5 TypeError <- ToIntegerOrInfinity#0 <- ToNumber#7",
	}, kept)
}

// The decisions are the `TypeError` sites of every builtin, on both channels,
// and nothing else.
//
// Neither direction is spare. A class other than `TypeError` says something
// about the values a well-typed caller can pass rather than about their types,
// so the filter must not weigh one: a `RangeError` on an out-of-range index, a
// `URIError` on a malformed escape, a `SyntaxError` on an unparseable string.
// And a `TypeError` site the filter never reaches is one it silently keeps,
// which is the shape a dropped channel would take — filterThrows walks the
// synchronous sites and the rejection sites separately, so losing either leaves
// the other passing.
func TestFilterAdjudicatesEveryTypeErrorSite(t *testing.T) {
	cfg := testCFG(t)
	summary := testThrows(t)

	var want []string
	for _, fn := range cfg.Funcs {
		if fn.Kind != BuiltinMethod && fn.Kind != BuiltinStatic {
			continue
		}
		throws := summary.Of(fn)
		for _, site := range append(throws.SyncSites(), throws.RejectSites()...) {
			if site.Exception == Class("TypeError") {
				want = append(want, fn.Name+": "+site.String())
			}
		}
	}

	var got []string
	for _, decision := range testFilterReport(t).Decisions {
		got = append(got, decision.Method+": "+decision.Site)
	}

	sort.Strings(want)
	sort.Strings(got)
	require.Equal(t, want, got)
}

// The whole run's tallies, and the operations whose guards it dropped. This is
// the §9.2 review report in summary form: a change to coercions or to the
// threading moves these numbers, and the reviewer sees which operation moved.
func TestFilterReportTallies(t *testing.T) {
	report := testFilterReport(t)
	adjudicated, dropped := report.Counts()

	guards := map[string]int{}
	for _, decision := range report.Dropped() {
		guards[decision.Coercion]++
	}
	require.Equal(t, 4882, adjudicated)
	require.Equal(t, 362, dropped)
	require.Equal(t, map[string]int{
		"RequireObjectCoercible": 31,
		"ThisBigIntValue":        2,
		"ThisBooleanValue":       2,
		"ThisNumberValue":        5,
		"ThisStringValue":        2,
		"ThisSymbolValue":        4,
		"ToObject":               166,
		"ToString":               150,
	}, guards)

	// The identity rule accounts for 120 of the ToString drops, every one of
	// them a `String.prototype` method whose receiver `ToString` hands back.
	under := map[string]int{}
	for _, decision := range report.Dropped() {
		if decision.PastCoercion {
			under[decision.Coercion]++
		}
	}
	require.Equal(t, map[string]int{"ToString": 120}, under)
}

// Every dropped site names the receiver as the value it checked, because the
// receiver branch is the only one built. A drop naming a parameter would be the
// parameter branch firing without the declared types it needs, and the facts
// carry none, so it would discard a `TypeError` a caller can raise. #1301
// tracks building that branch on types read from the joined declaration.
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

	snaps.MatchInlineSnapshot(t, out.String(), snaps.Inline(`  coercion filter: 3 TypeError sites adjudicated, 1 dropped
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

// The throw steps behind each entry of coercions, one line per operation. Every
// entry was added by reading the operation in ECMA-262 and confirming that its
// `TypeError` reports the wrong dynamic type for the value at coercionGuardArg,
// and that `accepts` and `returnsAtOnce` say what it does per receiver type. The
// graph carries the step but not the reason, so the snapshot pins what was read
// rather than proving it.
//
// A spec bump that adds or moves a throw step in one of these operations shows
// up here. That is the prompt to re-read the operation against FR11, not to
// update the snapshot.
func TestCoercionsRaiseOnTheirFirstArgument(t *testing.T) {
	cfg := testCFG(t)
	var lines []string
	for _, name := range sortedStrings(coercionNames()) {
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

// A coercion returns at once only for a type it accepts. Returning a value is
// how it avoids its own `Throw` step, so an entry claiming otherwise would drop
// the steps past a coercion that raises before reaching them.
func TestCoercionsReturnOnlyWhatTheyAccept(t *testing.T) {
	t.Parallel()

	for name, c := range coercions {
		for _, returned := range c.returnsAtOnce.ToSlice() {
			require.Containsf(t, c.accepts.ToSlice(), returned,
				"%s returns a %s it does not accept", name, returned)
		}
	}
}

// coercionNames returns the operations the filter weighs.
func coercionNames() []string {
	names := make([]string, 0, len(coercions))
	for name := range coercions {
		names = append(names, name)
	}
	return names
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
	require.NotContains(t, coercionNames(), "ToPrimitive")

	fn := testCFG(t).AbstractOp("ToPrimitive")
	require.NotNil(t, fn)
	require.Equal(t, "#17 TypeError", throwSteps(fn))

	// No site is dropped on its account. One that bottoms out there survives
	// unless a coercion further out is an identity for the receiver, which is
	// pastReceiverIdentity's rule and not this one.
	var reached int
	for _, decision := range testFilterReport(t).Decisions {
		if !strings.HasSuffix(decision.Site, "<- ToPrimitive#17") {
			continue
		}
		reached++
		require.NotEqualf(t, "ToPrimitive", decision.Coercion, "%s", decision)
		if decision.Dropped {
			require.Truef(t, decision.PastCoercion, "%s", decision)
		}
	}
	require.NotZero(t, reached)
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
