package ecma262

import (
	"testing"

	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/require"
)

// rejectsOf returns the reject channel of one builtin or abstract operation,
// rendered the way String renders the synchronous one.
func rejectsOf(t *testing.T, name string) string {
	t.Helper()
	return throwsOf(t, name).RejectsString()
}

func TestRaisedStringOfACombinatorForm(t *testing.T) {
	tests := map[string]struct {
		raised Raised
		want   string
	}{
		"ElementErr": {ElementErr(Param(0)), "ElementErr(Param(0))"},
		"Aggregate":  {AggregateErr(Param(0)), "AggregateError<ElementErr(Param(0))>"},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, test.want, test.raised.String())
		})
	}
}

func TestThrowsRejectsString(t *testing.T) {
	require.Equal(t, "none", Throws{}.RejectsString())
	require.Equal(t, "none", Throws{Raised: []Raised{Class("TypeError")}}.RejectsString())
	require.Equal(t,
		"TypeError ElementErr(Param(0))",
		Throws{Rejects: []Raised{Class("TypeError"), ElementErr(Param(0))}}.RejectsString(),
	)
}

// A promise-returning method carries a reject set of its own, and the two
// channels say different things about the same method. `Promise.prototype.then`
// is the case that shows the reject channel is computed rather than mirrored:
// it validates its receiver synchronously and rejects nothing itself.
func TestRejectSetIsDistinctFromTheThrowSet(t *testing.T) {
	tests := map[string]struct {
		fn      string
		throws  string
		rejects string
	}{
		"DirectReject":  {"Promise.reject", "TypeError Unknown", "Origin(Param(0))"},
		"NoRejectSite":  {"Promise.prototype.then", "TypeError Unknown", "none"},
		"AsyncIterator": {"AsyncGeneratorPrototype.next", "none", "TypeError"},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			throws := throwsOf(t, test.fn)
			require.Equal(t, test.throws, Throws{Raised: throws.Raised}.String())
			require.Equal(t, test.rejects, throws.RejectsString())
		})
	}
}

// A function that does not return a promise has no reject channel, whatever it
// does with a capability it was handed. `NewPromiseReactionJob`'s closure calls
// `? Call(promiseCapability.[[Reject]], ...)` on the capability its caller
// built, so the value settles a promise the closure does not return.
func TestRejectSetIsEmptyWithoutAReturnedPromise(t *testing.T) {
	require.False(t, testCFG(t).AbstractOp("NewPromiseReactionJob:clo0").Promise)
	require.Equal(t, "none", rejectsOf(t, "NewPromiseReactionJob:clo0"))
}

// `Promise.reject` hands its own argument to the reject function, so the reject
// set names the parameter rather than an error class. FR6 spells the fact
// `param:0` on the wire.
func TestRejectSetRecordsADirectRejectionsOrigin(t *testing.T) {
	require.Equal(t, []Raised{Propagated(Param(0))}, throwsOf(t, "Promise.reject").Rejects)
}

// The same error class reaches both channels when a method raises it on both
// paths, which is what makes the split per site rather than per error type.
// `Promise.try` raises a TypeError synchronously when its `this` value is not a
// constructor, and rejects with one when the callback it was handed is not
// callable. It also rejects with whatever that callback itself raises, since
// `Completion(Call(callback, ...))` routes the callback's throws to the reject
// sink rather than out through the method.
func TestRejectSetAndThrowSetShareAnErrorClass(t *testing.T) {
	throws := throwsOf(t, "Promise.try")
	require.Contains(t, throws.Raised, Class("TypeError"))
	require.Contains(t, throws.Rejects, Class("TypeError"))
	require.Contains(t, throws.Rejects, CallbackThrows(Param(0)))
	require.NotContains(t, throws.Raised, CallbackThrows(Param(0)))
}

// A generator surfaces its body's failures synchronously and an async generator
// surfaces them as rejections of the promise each `next` hands back. Both route
// through the one partition with no case of their own: the pair below raises
// the same value, its own first argument, and the channel differs.
func TestRejectSetSubsumesAsyncGenerators(t *testing.T) {
	sync := throwsOf(t, "GeneratorPrototype.throw")
	require.Contains(t, sync.Raised, Propagated(Param(0)))
	require.Equal(t, "none", sync.RejectsString())

	async := throwsOf(t, "AsyncGeneratorPrototype.throw")
	require.Contains(t, async.Rejects, Propagated(Param(0)))
}

// A combinator's synchronous validation and its `IfAbruptRejectPromise` steps
// land in different channels. `Promise.all` raises a TypeError synchronously
// through `? NewPromiseCapability(C)` when `this` is not a constructor, and
// rejects with one when `? GetIterator(iterable)` finds the argument is not
// iterable.
func TestRejectSetSplitsACombinatorBySite(t *testing.T) {
	sites := func(sites []ThrowSite) []string {
		var chains []string
		for _, site := range sites {
			chains = append(chains, site.String())
		}
		return chains
	}
	all := throwsOf(t, "Promise.all")

	require.Contains(t, sites(all.SyncSites()), "#2 TypeError <- NewPromiseCapability#3")
	require.Contains(t, sites(all.RejectSites()), "#19 rejects TypeError <- GetIterator#20")
	// A validation `Throw` the algorithm writes for itself reads the same way.
	// `Promise.try` raises one before it ever builds a capability.
	require.Contains(t, sites(throwsOf(t, "Promise.try").SyncSites()), "#4 TypeError")
}

// The four combinators forward the reject type of the promises their iterable
// yields. That value arrives through the promise-resolution machinery, so the
// model supplies it by name, and it is added to what the walk found rather than
// replacing it. `Promise.allSettled` shows the difference: its element channel
// is empty, and the TypeError it rejects with on a non-iterable argument still
// stands.
func TestCombinatorRejects(t *testing.T) {
	tests := map[string]struct {
		fn   string
		want string
	}{
		"All":        {"Promise.all", "TypeError ElementErr(Param(0)) Unknown"},
		"Race":       {"Promise.race", "TypeError ElementErr(Param(0)) Unknown"},
		"Any":        {"Promise.any", "TypeError AggregateError<ElementErr(Param(0))> Unknown"},
		"AllSettled": {"Promise.allSettled", "TypeError Unknown"},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, test.want, rejectsOf(t, test.fn))
		})
	}
}

// Every combinator the model names is a promise-returning builtin with the
// parameter the entry keys on, so a spec bump that renames one fails here
// rather than quietly modeling nothing.
func TestPromiseCombinatorsMatchTheGraph(t *testing.T) {
	cfg := testCFG(t)
	for name, model := range promiseCombinators {
		t.Run(name, func(t *testing.T) {
			fn := cfg.Builtin(name)
			require.NotNil(t, fn, "no builtin named %s", name)
			require.True(t, fn.Promise, "%s does not return a promise", name)
			require.Contains(t, fn.Params, model.iterable)
		})
	}
}

// The two partitions are disjoint and together they are every site, so no site
// is counted in both channels or dropped from both.
func TestSitePartitionCoversEverySite(t *testing.T) {
	summary := testThrows(t)
	for _, fn := range testCFG(t).Funcs {
		throws := summary.Of(fn)
		require.Len(t, throws.Sites, len(throws.SyncSites())+len(throws.RejectSites()), fn.Name)
	}
}

// Each channel holds exactly the values its own sites raise, apart from the
// combinator model, which has no site at all.
func TestChannelsHoldWhatTheirSitesRaise(t *testing.T) {
	summary := testThrows(t)
	for _, fn := range testCFG(t).Funcs {
		throws := summary.Of(fn)
		for _, site := range throws.SyncSites() {
			require.Contains(t, throws.Raised, site.Raised, fn.Name)
			require.NotContains(t, combinatorRejects(fn), site.Raised, fn.Name)
		}
		for _, site := range throws.RejectSites() {
			require.Contains(t, throws.Rejects, site.Raised, fn.Name)
		}
		for _, modeled := range combinatorRejects(fn) {
			require.Contains(t, throws.Rejects, modeled, fn.Name)
		}
	}
}

// The whole route back to the operation that raised a rejected value is kept,
// the same as on the synchronous channel, so §9.2's coercion filter reads a
// reject site the way it reads a throw site.
//
// The synchronous site at #30 is a §3 lowering artifact. The step is `Let
// completion be ThrowCompletion(exception)`, which builds a completion the
// algorithm then enqueues, and the serializer writes it as a `Throw`. The
// method returns a promise on every path and cannot throw synchronously.
func TestRejectSiteProvenanceChains(t *testing.T) {
	snaps.MatchInlineSnapshot(t, throwsOf(t, "AsyncGeneratorPrototype.throw").SitesString(), snaps.Inline(`#4 rejects TypeError <- AsyncGeneratorValidate#0 <- RequireInternalSlot#2
#4 rejects TypeError <- AsyncGeneratorValidate#0 <- RequireInternalSlot#5
#4 rejects TypeError <- AsyncGeneratorValidate#5
#24 rejects Origin(Param(0))
#30 Origin(Param(0))
`),
	)
}

// rejectGraph is the inlined `IfAbruptRejectPromise` shape: a plain call whose
// completion is captured as a value, bound to a name, and handed to the
// capability's reject function. reason is what the reject call is passed.
func rejectGraph(t *testing.T, promise bool, reason string) *CFG {
	t.Helper()
	returns := "false"
	if promise {
		returns = "true"
	}
	cfg, err := ParseCFG([]byte(
		`{"specTarget":"abc","funcs":[` +
			`{"name":"Raiser","kind":"abstract-op","params":[],` +
			`"nodes":[{"kind":"throw","errorType":"RangeError"}]},` +
			`{"name":"Demo","kind":"builtin-static","params":["r"],"promise":` + returns + `,"nodes":[` +
			`{"kind":"call","target":"%0","callee":"Raiser","args":[],"guard":"plain"},` +
			`{"kind":"call","target":"%1","callee":"Completion",` +
			`"args":[{"kind":"var","var":"%0"}],"guard":"plain"},` +
			`{"kind":"let","target":"status","source":{"kind":"var","var":"%1"}},` +
			`{"kind":"call","target":"%2","callee":"Call","guard":"?","args":[` +
			`{"kind":"slot","object":{"kind":"var","var":"cap"},"slot":"Reject"},` +
			`{"kind":"lit"},` +
			`{"kind":"alloc","args":[` + reason + `]}]}]}]}`))
	require.NoError(t, err)
	return cfg
}

// A completion the reject walk read through is a step the analysis can name
// after all, so it no longer leaves the function incomplete. The same shape in
// a function that returns no promise stays incomplete, since nothing there says
// where the captured completion goes.
func TestRejectWalkReadsThroughACapturedCompletion(t *testing.T) {
	const completionValue = `{"kind":"slot","object":{"kind":"var","var":"status"},"slot":"Value"}`

	cfg := rejectGraph(t, true, completionValue)
	throws := NewThrowSummary(cfg).Of(cfg.Builtin("Demo"))
	require.Equal(t, "none", throws.String())
	require.Equal(t, "RangeError", throws.RejectsString())
	require.Equal(t, "#0 rejects RangeError <- Raiser#0", throws.Sites[0].String())

	cfg = rejectGraph(t, false, completionValue)
	throws = NewThrowSummary(cfg).Of(cfg.Builtin("Demo"))
	require.Equal(t, "incomplete", throws.String())
	require.Equal(t, "none", throws.RejectsString())
}

// directRejectGraph is `Promise.reject`'s shape: one step handing a plain value
// to the capability's reject function, with no completion capture in between.
func directRejectGraph(t *testing.T, reason string) *CFG {
	t.Helper()
	cfg, err := ParseCFG([]byte(
		`{"specTarget":"abc","funcs":[` +
			`{"name":"Demo","kind":"builtin-static","params":["r"],"promise":true,"nodes":[` +
			`{"kind":"call","target":"%0","callee":"Call","guard":"?","args":[` +
			`{"kind":"slot","object":{"kind":"var","var":"cap"},"slot":"Reject"},` +
			`{"kind":"lit"},` +
			`{"kind":"alloc","args":[` + reason + `]}]}]}]}`))
	require.NoError(t, err)
	return cfg
}

// A reason that is a plain value rather than a captured completion is the
// direct-rejection source, and the origin map reads where it came from.
func TestRejectWalkRecordsAPlainReason(t *testing.T) {
	cfg := directRejectGraph(t, `{"kind":"var","var":"r"}`)
	throws := NewThrowSummary(cfg).Of(cfg.Builtin("Demo"))
	require.Equal(t, "Origin(Param(0))", throws.RejectsString())
	require.Equal(t, "#0 rejects Origin(Param(0))", throws.Sites[0].String())
}

// A reason the walk can place nowhere is untraced rather than dropped, which
// FR6 spells `unknown` on the wire.
func TestRejectWalkRecordsAnUnplaceableReason(t *testing.T) {
	cfg := directRejectGraph(t, `{"kind":"alloc","args":[]}`)
	require.Equal(t, "Unknown", NewThrowSummary(cfg).Of(cfg.Builtin("Demo")).RejectsString())
}

// Calling one of the two functions a promise capability holds contributes to
// neither channel. `NewPromiseCapability` built them, they settle a promise
// rather than run code the caller supplied, and reading the step as an invoke
// of an untraceable function value would leave every promise-returning method
// incomplete and raising an unknown value.
func TestCapabilityInvokeContributesToNeitherChannel(t *testing.T) {
	cfg := directRejectGraph(t, `{"kind":"var","var":"r"}`)
	demo := cfg.Builtin("Demo")
	throws := NewThrowSummary(cfg).Of(demo)
	require.False(t, throws.Incomplete)
	require.Equal(t, "none", throws.String())

	settle, ok := capabilityInvoke(demo.Nodes[0].(*CallNode))
	require.True(t, ok)
	require.Equal(t, rejectSlot, settle)
}

// A function that hands the capability it built to another function which
// rejects one is flagged, since those rejections settle the promise it returns
// and no source above sees them.
// `AsyncFromSyncIteratorPrototype.next` passes its capability to
// `AsyncFromSyncIteratorContinuation`, which rejects it three times over.
func TestRejectSetFlagsADelegatedCapability(t *testing.T) {
	require.True(t, throwsOf(t, "AsyncFromSyncIteratorPrototype.next").Incomplete)

	// The combinators pass their capability to a `PerformPromise*` operation
	// that resolves rather than rejects it, so the flag stays off and the
	// modeled element rejections stand as a whole answer.
	for name := range promiseCombinators {
		require.False(t, throwsOf(t, name).Incomplete, name)
	}
}

// The reason a rejection carries is read from the invoker's own argument list,
// which sits third in `Call(F, V, argumentsList)` and second in `Construct(F,
// argumentsList, newTarget)`.
func TestRejectReasonReadsEachInvokersArgumentList(t *testing.T) {
	const reject = `{"kind":"slot","object":{"kind":"var","var":"cap"},"slot":"Reject"}`
	const list = `{"kind":"alloc","args":[{"kind":"var","var":"r"}]}`

	tests := map[string]string{
		"Call":      `"callee":"Call","args":[` + reject + `,{"kind":"lit"},` + list + `]`,
		"Construct": `"callee":"Construct","args":[` + reject + `,` + list + `,{"kind":"lit"}]`,
	}

	for name, call := range tests {
		t.Run(name, func(t *testing.T) {
			cfg, err := ParseCFG([]byte(
				`{"specTarget":"abc","funcs":[` +
					`{"name":"Demo","kind":"builtin-static","params":["r"],"promise":true,` +
					`"nodes":[{"kind":"call","target":"%0","guard":"?",` + call + `}]}]}`))
			require.NoError(t, err)

			require.Equal(t, "Origin(Param(0))", NewThrowSummary(cfg).Of(cfg.Builtin("Demo")).RejectsString())
		})
	}
}
