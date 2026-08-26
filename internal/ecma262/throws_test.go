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
	throwsOnce sync.Once
	throwsAll  *ThrowSummary
)

// testThrows runs the throw fixpoint over the committed graph once for the
// whole package.
func testThrows(t *testing.T) *ThrowSummary {
	t.Helper()
	cfg := testCFG(t)
	throwsOnce.Do(func() {
		throwsAll = NewThrowSummary(cfg)
	})
	return throwsAll
}

// throwsOf returns the throws of one builtin or abstract operation.
func throwsOf(t *testing.T, name string) Throws {
	t.Helper()
	cfg := testCFG(t)
	fn := cfg.Builtin(name)
	if fn == nil {
		fn = cfg.AbstractOp(name)
	}
	require.NotNil(t, fn, "no function named %s", name)
	return testThrows(t).Of(fn)
}

func TestRaisedString(t *testing.T) {
	tests := map[string]struct {
		raised Raised
		want   string
	}{
		"Class":    {Class("TypeError"), "TypeError"},
		"Origin":   {Propagated(Param(1)), "Origin(Param(1))"},
		"Callback": {CallbackThrows(Param(0)), "CallbackThrows(Param(0))"},
		"Untraced": {Untraced, "Unknown"},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, test.want, test.raised.String())
		})
	}
}

func TestThrowsString(t *testing.T) {
	tests := map[string]struct {
		throws Throws
		want   string
	}{
		"Empty":      {Throws{}, "none"},
		"Incomplete": {Throws{Incomplete: true}, "incomplete"},
		"Every": {
			Throws{
				Raised:     []Raised{Class("TypeError"), Propagated(Param(0)), CallbackThrows(Param(1)), Untraced},
				Incomplete: true,
			},
			"TypeError Origin(Param(0)) CallbackThrows(Param(1)) Unknown incomplete",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, test.want, test.throws.String())
		})
	}
}

func TestThrowSummarySampleFunctions(t *testing.T) {
	tests := map[string]struct {
		fn   string
		want string
	}{
		// push raises a TypeError itself when the array is too long, and
		// inherits one from every `?`-guarded coercion it opens with. §9.2 is
		// where the receiver coercion drops back out.
		"ClassesOnly": {"Array.prototype.push", "TypeError"},
		// toFixed keeps the RangeError it raises on an out-of-range
		// fractionDigits, which is the §9.2 gate. The prose step §3 could not
		// lower is what leaves it incomplete.
		"DomainCheck": {"Number.prototype.toFixed", "RangeError TypeError"},
		"URIDecoding": {"decodeURIComponent", "TypeError URIError"},
		// A method that raises nothing and reaches nothing that raises.
		// `Number.isInteger` tests its argument and returns a boolean.
		"RaisesNothing": {"Number.isInteger", "none"},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			throws := throwsOf(t, test.fn)
			require.Equal(t, test.want, Throws{Raised: throws.Raised}.String())
		})
	}
}

// A method that `?`-calls a function it was handed raises whatever that
// function raises, which is an effect rather than a value. FR13 turns the fact
// into throws polymorphism at the join.
func TestThrowSummaryCallbackEffect(t *testing.T) {
	tests := map[string]struct {
		fn   string
		want Raised
	}{
		// `? Call(callback, thisArg, « kValue, 𝔽(k), O »)` invokes the method's
		// first declared parameter.
		"ForEach": {"Array.prototype.forEach", CallbackThrows(Param(0))},
		// apply invokes its receiver rather than a parameter, since `Let func
		// be the this value` is the function being applied.
		"Apply": {"Function.prototype.apply", CallbackThrows(Receiver)},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			require.Contains(t, throwsOf(t, test.fn).Raised, test.want)
		})
	}
}

// A callback effect travels up the call graph and lands on the parameter the
// caller passed the function at. `JSON.parse` never invokes its reviver
// itself. `InternalizeJSONProperty` does, as its own parameter 2, and the two
// `? InternalizeJSONProperty(root, rootName, reviver)` calls thread that back
// to `reviver`, which is parse's parameter 1.
func TestThrowSummaryRemapsACallbackEffectThroughTheCall(t *testing.T) {
	require.Contains(t, throwsOf(t, "InternalizeJSONProperty").Raised, CallbackThrows(Param(2)))

	parse := throwsOf(t, "JSON.parse")
	require.Contains(t, parse.Raised, CallbackThrows(Param(1)))
	require.NotContains(t, parse.Raised, CallbackThrows(Param(2)))
}

// A `throw` of a value the algorithm did not construct records where that value
// came from. `Generator.prototype.throw` raises its own first argument.
func TestThrowSummaryOriginThrow(t *testing.T) {
	require.Contains(t, throwsOf(t, "GeneratorPrototype.throw").Raised, Propagated(Param(0)))
}

// A raised value the origin map cannot tie to the receiver or a parameter is
// untraced rather than dropped. `RequireObjectCoercible` raises a value bound
// on one path by the error object it builds and on another by the argument it
// hands back, and the two definitions join to `Unknown`.
func TestThrowSummaryUntracedThrow(t *testing.T) {
	require.Contains(t, throwsOf(t, "RequireObjectCoercible").Raised, Untraced)
	require.Contains(t, throwsOf(t, "String.prototype.toLowerCase").Raised, Untraced)
}

// Each site keeps the whole chain back to the operation that raised the value,
// which is what §9.2 walks to recognize a coercion type-guard. Two chains that
// reach one step from different sources stay apart, so `? LengthOfArrayLike(O)`
// records both the `ToNumber` coercion of the length and the `@@toPrimitive`
// lookup below it.
func TestThrowSummaryProvenanceChains(t *testing.T) {
	snaps.MatchInlineSnapshot(t, throwsOf(t, "Array.prototype.forEach").SitesString(), snaps.Inline(`#2 TypeError <- ToObject#3
#2 TypeError <- ToObject#6
#4 TypeError <- LengthOfArrayLike#1 <- ToLength#0 <- ToIntegerOrInfinity#0 <- ToNumber#23 <- ToPrimitive#1 <- GetMethod#0 <- GetV#0 <- ToObject#3
#4 TypeError <- LengthOfArrayLike#1 <- ToLength#0 <- ToIntegerOrInfinity#0 <- ToNumber#23 <- ToPrimitive#1 <- GetMethod#0 <- GetV#0 <- ToObject#6
#4 TypeError <- LengthOfArrayLike#1 <- ToLength#0 <- ToIntegerOrInfinity#0 <- ToNumber#23 <- ToPrimitive#1 <- GetMethod#8
#4 TypeError <- LengthOfArrayLike#1 <- ToLength#0 <- ToIntegerOrInfinity#0 <- ToNumber#23 <- ToPrimitive#17
#4 TypeError <- LengthOfArrayLike#1 <- ToLength#0 <- ToIntegerOrInfinity#0 <- ToNumber#23 <- ToPrimitive#20 <- OrdinaryToPrimitive#20
#4 TypeError <- LengthOfArrayLike#1 <- ToLength#0 <- ToIntegerOrInfinity#0 <- ToNumber#23 <- ToPrimitive#9 <- Call#5
#4 TypeError <- LengthOfArrayLike#1 <- ToLength#0 <- ToIntegerOrInfinity#0 <- ToNumber#7
#9 TypeError
#19 TypeError <- Call#5
#19 CallbackThrows(Param(0))
`),
	)
}

// The base of a site is the operation that raised the value, whatever it
// travelled through to get out. A direct site is its own base.
func TestThrowSiteBase(t *testing.T) {
	cfg, err := ParseCFG([]byte(
		`{"specTarget":"abc","funcs":[` +
			`{"name":"Raiser","kind":"abstract-op","params":[],` +
			`"nodes":[{"kind":"branch"},{"kind":"throw","errorType":"RangeError"}]},` +
			`{"name":"Middle","kind":"abstract-op","params":[],` +
			`"nodes":[{"kind":"call","callee":"Raiser","args":[],"guard":"?"}]},` +
			`{"name":"Demo","kind":"builtin-static","params":[],"nodes":[` +
			`{"kind":"throw","errorType":"TypeError"},` +
			`{"kind":"call","callee":"Middle","args":[],"guard":"?"}]}]}`))
	require.NoError(t, err)

	sites := NewThrowSummary(cfg).Of(cfg.Builtin("Demo")).Sites
	require.Len(t, sites, 2)

	direct := sites[0]
	require.True(t, direct.Root.Direct())
	require.Equal(t, direct, direct.Base())

	propagated := sites[1]
	require.False(t, propagated.Root.Direct())
	require.Equal(t, "Middle", propagated.Root.Callee)
	require.Equal(t, "#1 RangeError <- Middle#0 <- Raiser#1", propagated.String())

	base := propagated.Base()
	require.True(t, base.Root.Direct())
	require.Equal(t, Class("RangeError"), base.Raised)
	require.Equal(t, 1, base.Index)
}

// Only a `?` guard propagates. `!` asserts that no abrupt completion arises and
// a plain call leaves the result unchecked, so neither contributes what the
// callee raises.
func TestThrowSummaryGuardsThatContributeNothing(t *testing.T) {
	graph := func(guard string) *CFG {
		cfg, err := ParseCFG([]byte(
			`{"specTarget":"abc","funcs":[` +
				`{"name":"Raiser","kind":"abstract-op","params":[],` +
				`"nodes":[{"kind":"throw","errorType":"RangeError"}]},` +
				`{"name":"Demo","kind":"builtin-static","params":[],` +
				`"nodes":[{"kind":"call","callee":"Raiser","args":[],"guard":"` + guard + `"}]}]}`))
		require.NoError(t, err)
		return cfg
	}

	tests := map[string]struct {
		guard string
		want  string
	}{
		"Question": {"?", "RangeError"},
		"Bang":     {"!", "none"},
		"Plain":    {"plain", "none"},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := graph(test.guard)
			require.Equal(t, test.want, NewThrowSummary(cfg).Of(cfg.Builtin("Demo")).String())
		})
	}
}

// A raised value standing for a callee's own parameter is threaded back to
// whatever the call passed there. `Demo` passes its parameter 1 at the position
// `Raiser` raises, so `Raiser`'s parameter 0 becomes `Demo`'s parameter 1.
func TestThrowSummaryRemapsAnOriginThroughTheCall(t *testing.T) {
	cfg, err := ParseCFG([]byte(
		`{"specTarget":"abc","funcs":[` +
			`{"name":"Raiser","kind":"abstract-op","params":["reason"],` +
			`"nodes":[{"kind":"throw","value":{"kind":"var","var":"reason"}}]},` +
			`{"name":"Demo","kind":"builtin-static","params":["first","second"],` +
			`"nodes":[{"kind":"call","callee":"Raiser","args":[{"kind":"var","var":"second"}],"guard":"?"}]}]}`))
	require.NoError(t, err)

	s := NewThrowSummary(cfg)
	require.Equal(t, "Origin(Param(0))", s.Of(cfg.AbstractOp("Raiser")).String())
	require.Equal(t, "Origin(Param(1))", s.Of(cfg.Builtin("Demo")).String())
}

// A callee's parametric raised value that the call fills with something the
// origin map cannot place is untraced rather than left at the callee's own
// parameter, which would name the wrong value at the join.
func TestThrowSummaryRemapsAnUnplaceableArgumentToUntraced(t *testing.T) {
	cfg, err := ParseCFG([]byte(
		`{"specTarget":"abc","funcs":[` +
			`{"name":"Raiser","kind":"abstract-op","params":["reason"],` +
			`"nodes":[{"kind":"throw","value":{"kind":"var","var":"reason"}}]},` +
			`{"name":"Demo","kind":"builtin-static","params":["first"],"nodes":[` +
			`{"kind":"let","target":"local","source":{"kind":"alloc"}},` +
			`{"kind":"call","callee":"Raiser","args":[{"kind":"var","var":"local"}],"guard":"?"}]}]}`))
	require.NoError(t, err)

	require.Equal(t, "Unknown", NewThrowSummary(cfg).Of(cfg.Builtin("Demo")).String())
}

// A step whose throws the analysis could not read leaves the function
// incomplete rather than quietly raising nothing. A function value read off a
// property is one shape. `Array.prototype.toString` calls whatever `? Get(array,
// "join")` returned, and the graph holds no body for that value.
func TestThrowSummaryIncomplete(t *testing.T) {
	tests := map[string]struct {
		fn   string
		want bool
	}{
		// A prose step §3 emits as an opaque node.
		"OpaqueStep": {"String.prototype.toLowerCase", true},
		// `? Call(func, array)` where `func` was read off the receiver.
		"InvokedPropertyValue": {"Array.prototype.toString", true},
		// `Completion(%9)` captures the result of a plain `Call(procedure, ...)`
		// and `? IteratorClose(iterated, result)` re-raises it, so the
		// callback's throws leave the method along an edge the guards do not
		// record.
		"CapturedCompletion": {"Iterator.prototype.forEach", true},
		// Every step of push resolves.
		"WhollyReadable": {"Array.prototype.push", false},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, test.want, throwsOf(t, test.fn).Incomplete)
		})
	}
}

// An abstract operation whose body is a dispatch to the internal method of the
// same name is incomplete, since the graph writes the dispatch as a call to the
// bare name and it resolves back to the operation. `Get(O, P)` is `?
// O.[[Get]](P, O)` and nothing else, so nothing would report that a Proxy trap
// running under it can raise anything at all.
func TestThrowSummarySelfDispatchIsIncomplete(t *testing.T) {
	// The operations are looked up by hand rather than through throwsOf, since
	// `Set` also names the `Set` constructor.
	cfg := testCFG(t)
	summary := testThrows(t)

	// `Get` dispatches and nothing else, so the flag is all it has to report.
	require.Equal(t, "incomplete", summary.Of(cfg.AbstractOp("Get")).String())
	// `Set` raises a TypeError of its own beside the dispatch.
	require.Equal(t, "TypeError incomplete", summary.Of(cfg.AbstractOp("Set")).String())
}

// Each shape of step whose throws the analysis could not read leaves the
// function incomplete rather than quietly raising nothing. `Raiser` is there so
// that a shape which silently dropped its step would come back "none" and read
// as a function that raises nothing.
func TestThrowSummaryIncompleteShapes(t *testing.T) {
	const raiser = `{"name":"Raiser","kind":"abstract-op","params":[],` +
		`"nodes":[{"kind":"throw","errorType":"RangeError"}]},`

	tests := map[string]string{
		// An object internal method, whose implementation is chosen at runtime
		// and which a Proxy trap can replace with any code at all.
		"UnresolvedCallee": `{"name":"Demo","kind":"builtin-static","params":["target"],` +
			`"nodes":[{"kind":"call","callee":"GetOwnProperty",` +
			`"args":[{"kind":"var","var":"target"}],"guard":"?"}]}`,
		// A callee bound to one of the calling function's parameters is a
		// function the caller was handed, whatever it is named, so it resolves
		// to no body even when an abstract operation shares its name.
		"CalleeNamingAParameter": raiser +
			`{"name":"Demo","kind":"builtin-static","params":["Raiser"],` +
			`"nodes":[{"kind":"call","callee":"Raiser","args":[],"guard":"?"}]}`,
		// An abrupt completion the algorithm captures as a value, which the
		// guards no longer say which step raised.
		"CapturedCompletion": raiser +
			`{"name":"Demo","kind":"builtin-static","params":[],"nodes":[` +
			`{"kind":"call","target":"%0","callee":"Raiser","args":[],"guard":"plain"},` +
			`{"kind":"call","target":"%1","callee":"Completion",` +
			`"args":[{"kind":"var","var":"%0"}],"guard":"plain"}]}`,
	}

	for name, funcs := range tests {
		t.Run(name, func(t *testing.T) {
			cfg, err := ParseCFG([]byte(`{"specTarget":"abc","funcs":[` + funcs + `]}`))
			require.NoError(t, err)

			require.Equal(t, "incomplete", NewThrowSummary(cfg).Of(cfg.Builtin("Demo")).String())
		})
	}
}

// The order the fixpoint reaches a function in leaves no mark on its facts.
// What each step raises and where that value came from come out the same off a
// graph that merely lists its functions in another order. The route a site
// records back to its source is the one exception, since the fixpoint keeps
// whichever route it found first.
func TestThrowSummaryIsIndependentOfFuncOrder(t *testing.T) {
	// facts renders one function's throws without the routes: the raised set,
	// then one sorted line per site naming the step, the value, and the step
	// the value came from. Two sites can render the same line, since the step a
	// value came from is named by its position in a function the line does not
	// name, so the lines are compared as a bag rather than in order.
	facts := func(throws Throws) string {
		lines := make([]string, 0, len(throws.Sites))
		for _, site := range throws.Sites {
			base := site.Base()
			lines = append(lines, fmt.Sprintf("#%d %s %s from #%d %s", site.Index, site.Sink, site.Raised, base.Index, base.Raised))
		}
		sort.Strings(lines)
		return throws.String() + " / " + throws.RejectsString() + "\n" + strings.Join(lines, "\n")
	}

	forward := testThrows(t)

	reversed, err := LoadCFG(cfgPath)
	require.NoError(t, err)
	for i, j := 0, len(reversed.Funcs)-1; i < j; i, j = i+1, j-1 {
		reversed.Funcs[i], reversed.Funcs[j] = reversed.Funcs[j], reversed.Funcs[i]
	}
	backward := NewThrowSummary(reversed)

	for _, fn := range reversed.Funcs {
		// `Set` names both the property-write abstract operation and the `Set`
		// constructor, so the two graphs are matched up within one index.
		same := testCFG(t).Builtin(fn.Name)
		if fn.Kind == AbstractOp {
			same = testCFG(t).AbstractOp(fn.Name)
		}
		require.NotNil(t, same, "no function named %s", fn.Name)
		require.Equal(t, facts(forward.Of(same)), facts(backward.Of(fn)), fn.Name)
	}
}

// A cycle in the call graph settles instead of nesting one propagation hop
// deeper on every pass. Each step records one site per value it raises and
// source that value came from, and both are drawn from finite sets, so the
// chains stop growing once every step has its sites.
func TestThrowSummaryTerminatesOnACycle(t *testing.T) {
	cfg, err := ParseCFG([]byte(
		`{"specTarget":"abc","funcs":[` +
			`{"name":"Even","kind":"abstract-op","params":[],"nodes":[` +
			`{"kind":"throw","errorType":"RangeError"},` +
			`{"kind":"call","callee":"Odd","args":[],"guard":"?"}]},` +
			`{"name":"Odd","kind":"abstract-op","params":[],"nodes":[` +
			`{"kind":"throw","errorType":"TypeError"},` +
			`{"kind":"call","callee":"Even","args":[],"guard":"?"}]}]}`))
	require.NoError(t, err)

	s := NewThrowSummary(cfg)
	require.Equal(t, "RangeError TypeError", s.Of(cfg.AbstractOp("Even")).String())
	require.Equal(t, "RangeError TypeError", s.Of(cfg.AbstractOp("Odd")).String())
}

// A function the summary never saw raises nothing, and so does one the fixpoint
// analyzed and found clean.
func TestThrowSummaryOfUnknownFunc(t *testing.T) {
	s := testThrows(t)
	require.Equal(t, Throws{}, s.Of(&Func{Name: "nosuchfunction"}))
	require.Equal(t, Throws{}, s.Of(testCFG(t).Builtin("Number.isInteger")))
}
