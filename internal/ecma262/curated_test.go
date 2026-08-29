package ecma262

import (
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/escalier-lang/escalier/internal/set"
	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/require"
)

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

// entry builds a curated entry claiming one receiver, reviewed against the
// digest the graph currently holds for name.
func entry(t *testing.T, cfg *CFG, name string, kind ReceiverKind) CuratedEntry {
	t.Helper()
	fn := cfg.Builtin(name)
	require.NotNil(t, fn, "no builtin named %s", name)
	return CuratedEntry{
		Reason:     "stated for the test",
		ReviewedAt: fn.Digest,
		Receiver:   kind,
	}
}

// returnsEntry builds a curated entry claiming one return alias, reviewed
// against the digest the graph currently holds for name.
func returnsEntry(t *testing.T, cfg *CFG, name string, returns ReturnFact) CuratedEntry {
	t.Helper()
	e := entry(t, cfg, name, RecvBorrow)
	e.Receiver = ""
	e.Returns = returns
	return e
}

// The analysis leaves Demo.prototype.opaque's receiver open and settles
// Demo.prototype.read's, which is what the merge cases below are written
// against.
func TestDemoGraphClassification(t *testing.T) {
	t.Parallel()

	_, methods := demoFacts(t)
	require.Equal(t, "receiver:borrow returns:receiver", methods["Demo.prototype.read"].String())
	require.Equal(t, "returns:unknown", methods["Demo.prototype.opaque"].String())
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
// and §9 add shows up as wholly open until it is wired in. The alternative
// would have a new axis read as complete for every builtin the moment it is
// declared, which is the direction that hides work rather than surfacing it.
func TestUnclassifiedReadsAnUnwiredAxisAsOpen(t *testing.T) {
	t.Parallel()

	cfg, methods := demoFacts(t)
	facts := &Facts{SpecTarget: cfg.SpecTarget, Methods: methods}

	require.Equal(t,
		[]string{"Demo.prototype.opaque", "Demo.prototype.read"},
		facts.Unclassified(Axis("throws")))
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

// The two predicates read an axis they do not name in opposite directions, on
// purpose. A determination §8 or §9 has not added yet must not make an
// otherwise complete fact look like a hole that fails the run, and must show up
// as open work for a reviewer.
func TestCarriesAndAnswersDisagreeOnAnUnwiredAxis(t *testing.T) {
	t.Parallel()

	fact := MethodFact{Receiver: RecvBorrow, Returns: ReturnFact{Kind: AliasFresh}}
	require.True(t, fact.carries(Axis("throws")))
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

// A curated axis reaches the published fact, and the note says what it did to
// the analysis's answer.
func TestMergeCuration(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		entries func(*CFG) map[string]CuratedEntry
		// want is the merged fact for the name the entry addresses.
		name string
		want string
		note string
	}{
		// The ordinary case. The analysis withheld the axis, so the entry adds
		// a claim rather than displacing one.
		"FillIn": {
			entries: func(cfg *CFG) map[string]CuratedEntry {
				return map[string]CuratedEntry{
					"Demo.prototype.opaque": entry(t, cfg, "Demo.prototype.opaque", RecvMutBorrow),
				}
			},
			name: "Demo.prototype.opaque",
			want: "receiver:mutBorrow returns:unknown",
			note: "Demo.prototype.opaque receiver: fill-in mutBorrow",
		},
		// A claim the analysis published and the review contradicts. §6 reads
		// these first, since one of the two sources is wrong.
		"Correction": {
			entries: func(cfg *CFG) map[string]CuratedEntry {
				return map[string]CuratedEntry{
					"Demo.prototype.read": entry(t, cfg, "Demo.prototype.read", RecvMutBorrow),
				}
			},
			name: "Demo.prototype.read",
			want: "receiver:mutBorrow returns:receiver",
			note: "Demo.prototype.read receiver: correction mutBorrow over borrow",
		},
		// An entry that repeats the analysis changes nothing and is reported so
		// it can be deleted.
		"Redundant": {
			entries: func(cfg *CFG) map[string]CuratedEntry {
				return map[string]CuratedEntry{
					"Demo.prototype.read": entry(t, cfg, "Demo.prototype.read", RecvBorrow),
				}
			},
			name: "Demo.prototype.read",
			want: "receiver:borrow returns:receiver",
			note: "Demo.prototype.read receiver: redundant borrow over borrow",
		},
		// A return the analysis did name, contradicted. `read` ends in
		// `Return this`, so the analysis calls it `receiver`.
		"ReturnsCorrection": {
			entries: func(cfg *CFG) map[string]CuratedEntry {
				return map[string]CuratedEntry{
					"Demo.prototype.read": returnsEntry(t, cfg, "Demo.prototype.read", ReturnFact{Kind: AliasFresh}),
				}
			},
			name: "Demo.prototype.read",
			want: "receiver:borrow returns:fresh",
			note: "Demo.prototype.read returns: correction fresh over receiver",
		},
		// The same claim the analysis made, so the entry is deletable.
		"ReturnsRedundant": {
			entries: func(cfg *CFG) map[string]CuratedEntry {
				return map[string]CuratedEntry{
					"Demo.prototype.read": returnsEntry(t, cfg, "Demo.prototype.read", ReturnFact{Kind: AliasReceiver}),
				}
			},
			name: "Demo.prototype.read",
			want: "receiver:borrow returns:receiver",
			note: "Demo.prototype.read returns: redundant receiver over receiver",
		},
		// Curating one axis leaves the other where the analysis left it, which
		// is what makes the layer compose with the analysis rather than shadow
		// it.
		"OneAxisLeavesTheOther": {
			entries: func(cfg *CFG) map[string]CuratedEntry {
				return map[string]CuratedEntry{
					"Demo.prototype.opaque": returnsEntry(t, cfg, "Demo.prototype.opaque", ReturnFact{Kind: AliasFresh}),
				}
			},
			name: "Demo.prototype.opaque",
			want: "returns:fresh",
			note: "Demo.prototype.opaque returns: fill-in fresh",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			cfg, methods := demoFacts(t)
			report := mergeCuration(cfg, &Curation{Entries: test.entries(cfg)}, methods)

			require.Equal(t, test.want, methods[test.name].String())
			require.Len(t, report.Notes, 1)
			require.Equal(t, test.note, report.Notes[0].String())
			require.Empty(t, report.Stale)
			require.Empty(t, report.Unmatched)
			require.Empty(t, report.Refused)
		})
	}
}

// Whether a builtin has a receiver at all follows from its kind, so review
// cannot move it. A curated claim that contradicts the kind is refused and
// reported, and the analysis's answer stands.
func TestMergeCurationRefusesAReceiverTheKindContradicts(t *testing.T) {
	t.Parallel()

	cfg, err := ParseCFG([]byte(`{"specTarget":"abc","funcs":[` +
		`{"name":"Demo.make","kind":"builtin-static","params":[],"nodes":[` +
		`{"kind":"return","value":{"kind":"lit"}}]}]}`))
	require.NoError(t, err)
	methods := analyze(cfg).Methods
	require.Equal(t, "receiver:none returns:fresh", methods["Demo.make"].String())

	report := mergeCuration(cfg, &Curation{Entries: map[string]CuratedEntry{
		"Demo.make": entry(t, cfg, "Demo.make", RecvBorrow),
	}}, methods)

	require.Equal(t, []string{"Demo.make receiver: curated borrow, but a builtin-static has no receiver"}, report.Refused)
	require.Empty(t, report.Notes)
	require.Equal(t, "receiver:none returns:fresh", methods["Demo.make"].String())
}

// The mirror case. A method has a receiver whatever the review says.
func TestMergeCurationRefusesNoneOnAMethod(t *testing.T) {
	t.Parallel()

	cfg, methods := demoFacts(t)
	report := mergeCuration(cfg, &Curation{Entries: map[string]CuratedEntry{
		"Demo.prototype.opaque": entry(t, cfg, "Demo.prototype.opaque", RecvNone),
	}}, methods)

	require.Equal(t, []string{"Demo.prototype.opaque receiver: curated none, but a builtin-method has a receiver"}, report.Refused)
	require.Equal(t, "returns:unknown", methods["Demo.prototype.opaque"].String())
}

// A name the graph holds no builtin for is reported and applied to nothing, so
// an entry carried over from another spec revision degrades to a report line
// rather than inventing a method.
func TestMergeCurationReportsAnUnmatchedName(t *testing.T) {
	t.Parallel()

	cfg, methods := demoFacts(t)
	report := mergeCuration(cfg, &Curation{Entries: map[string]CuratedEntry{
		"Demo.prototype.gone": {
			Reason:     "stated for the test",
			ReviewedAt: "000000000000",
			Receiver:   RecvBorrow,
		},
	}}, methods)

	require.Equal(t, []string{"Demo.prototype.gone"}, report.Unmatched)
	require.Empty(t, report.Notes)
	require.NotContains(t, methods, "Demo.prototype.gone")
}

// An entry reviewed against an algorithm the graph no longer holds is applied
// and reported. Dropping it would trade a visible re-review prompt for a silent
// loss of coverage.
func TestMergeCurationAppliesAStaleEntry(t *testing.T) {
	t.Parallel()

	cfg, methods := demoFacts(t)
	stale := entry(t, cfg, "Demo.prototype.opaque", RecvMutBorrow)
	stale.ReviewedAt = "000000000000"
	report := mergeCuration(cfg, &Curation{Entries: map[string]CuratedEntry{
		"Demo.prototype.opaque": stale,
	}}, methods)

	require.Equal(t, []string{"Demo.prototype.opaque"}, report.Stale)
	require.Equal(t, "receiver:mutBorrow returns:unknown", methods["Demo.prototype.opaque"].String())
}

// Two algorithms that differ fingerprint differently, and re-parsing the same
// graph reproduces each fingerprint. Together those are what let a digest stand
// in for "the algorithm this entry was reviewed against".
func TestFuncDigestIdentifiesTheAlgorithm(t *testing.T) {
	t.Parallel()

	first, _ := demoFacts(t)
	second, err := ParseCFG([]byte(demoCFG))
	require.NoError(t, err)

	read := first.Builtin("Demo.prototype.read")
	opaque := first.Builtin("Demo.prototype.opaque")
	require.Len(t, read.Digest, digestLen)
	require.NotEqual(t, read.Digest, opaque.Digest)
	require.Equal(t, read.Digest, second.Builtin("Demo.prototype.read").Digest)

	// Editing one step changes that algorithm's digest and leaves the other's
	// alone, which is what keeps a spec bump from flagging every entry.
	edited, err := ParseCFG([]byte(strings.Replace(demoCFG, "whatever the host decides", "something else", 1)))
	require.NoError(t, err)
	require.Equal(t, read.Digest, edited.Builtin("Demo.prototype.read").Digest)
	require.NotEqual(t, opaque.Digest, edited.Builtin("Demo.prototype.opaque").Digest)
}

// An entry that cannot be reviewed or applied is a defect in committed data, so
// parsing refuses it rather than merging it as written.
func TestParseCurationRejects(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		entry string
		want  string
	}{
		"NoReason": {
			`{"reviewedAt":"abc","receiver":"borrow"}`,
			"curated entry Demo.m: has no reason",
		},
		"NoReviewedAt": {
			`{"reason":"r","receiver":"borrow"}`,
			"curated entry Demo.m: has no reviewedAt digest",
		},
		"NoDetermination": {
			`{"reason":"r","reviewedAt":"abc"}`,
			"curated entry Demo.m: answers no determination",
		},
		"UnknownReceiverKind": {
			`{"reason":"r","reviewedAt":"abc","receiver":"shared"}`,
			`curated entry Demo.m: names receiver "shared", which is not a receiver kind`,
		},
		// A return carrying a position but no kind is malformed rather than
		// absent, so it answers the axis and is refused on the kind.
		"ReturnsWithNoKind": {
			`{"reason":"r","reviewedAt":"abc","returns":{"index":0}}`,
			`curated entry Demo.m: returns "", which is not an alias kind`,
		},
		"UnknownAliasKind": {
			`{"reason":"r","reviewedAt":"abc","returns":{"kind":"borrowed"}}`,
			`curated entry Demo.m: returns "borrowed", which is not an alias kind`,
		},
		"ParamWithNoPosition": {
			`{"reason":"r","reviewedAt":"abc","returns":{"kind":"param"}}`,
			"curated entry Demo.m: returns a parameter but names no position",
		},
		"PositionOnANonParamReturn": {
			`{"reason":"r","reviewedAt":"abc","returns":{"kind":"fresh","index":0}}`,
			"curated entry Demo.m: returns fresh but names a position",
		},
		"UnionOfOne": {
			`{"reason":"r","reviewedAt":"abc","returns":{"kind":"union","members":[{"kind":"fresh"}]}}`,
			"curated entry Demo.m: returns a union of 1 members",
		},
		"UnionHoldingUnknown": {
			`{"reason":"r","reviewedAt":"abc","returns":{"kind":"union","members":[{"kind":"fresh"},{"kind":"unknown"}]}}`,
			"curated entry Demo.m: returns a union holding unknown",
		},
		"NegativeParamPosition": {
			`{"reason":"r","reviewedAt":"abc","returns":{"kind":"param","index":-1}}`,
			"curated entry Demo.m: returns the parameter at position -1",
		},
		"UnionMemberWithNoPosition": {
			`{"reason":"r","reviewedAt":"abc","returns":{"kind":"union","members":[{"kind":"fresh"},{"kind":"param"}]}}`,
			"curated entry Demo.m: union member: returns a parameter but names no position",
		},
		"UnionNamingOneValueTwice": {
			`{"reason":"r","reviewedAt":"abc","returns":{"kind":"union","members":[{"kind":"param","index":1},{"kind":"param","index":1}]}}`,
			"curated entry Demo.m: returns a union naming param(1) twice",
		},
		"MembersOnANonUnionReturn": {
			`{"reason":"r","reviewedAt":"abc","returns":{"kind":"fresh","members":[{"kind":"fresh"}]}}`,
			"curated entry Demo.m: returns fresh but names union members",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := ParseCuration([]byte(`{"entries":{"Demo.m":` + test.entry + `}}`))
			require.EqualError(t, err, test.want)
		})
	}
}

// The layer round-trips a fact of every shape the analysis builds, so an entry
// can answer any determination the analysis can.
func TestParseCurationAcceptsEveryReturnShape(t *testing.T) {
	t.Parallel()

	body := `{"reason":"r","reviewedAt":"abc","returns":`
	tests := map[string]string{
		"Fresh":    `{"kind":"fresh"}`,
		"Receiver": `{"kind":"receiver"}`,
		"Unknown":  `{"kind":"unknown"}`,
		"Param":    `{"kind":"param","index":0}`,
		"Union":    `{"kind":"union","members":[{"kind":"fresh"},{"kind":"param","index":1}]}`,
	}
	want := map[string]string{
		"Fresh": "fresh", "Receiver": "receiver", "Unknown": "unknown",
		"Param": "param(0)", "Union": "union(fresh, param(1))",
	}

	for name, returns := range tests {
		t.Run(name, func(t *testing.T) {
			curation, err := ParseCuration([]byte(`{"entries":{"Demo.m":` + body + returns + `}}}`))
			require.NoError(t, err)
			require.Equal(t, want[name], curation.Entries["Demo.m"].Returns.String())
		})
	}
}

// A layer that is not JSON at all fails before any entry is read, which is the
// other way committed data can be wrong.
func TestParseCurationRejectsMalformedJSON(t *testing.T) {
	t.Parallel()

	_, err := ParseCuration([]byte(`{"entries":`))
	require.ErrorContains(t, err, "decoding curation:")
}

// The committed layer parses. mustParseCuration is what the package-level
// `curated` var runs at init, and it panics rather than returning, so this
// pins both halves of that contract.
func TestMustParseCuration(t *testing.T) {
	t.Parallel()

	require.NotEmpty(t, mustParseCuration(curatedJSON).Entries)
	require.PanicsWithValue(t,
		"ecma262: committed curated.json is invalid: curated entry Demo.m: has no reason",
		func() {
			mustParseCuration([]byte(`{"entries":{"Demo.m":{"reviewedAt":"abc","receiver":"borrow"}}}`))
		})
}

// failAfter is a writer that accepts n writes and then fails, so a test can put
// the failure on any one line of the report.
type failAfter struct {
	n int
}

func (w *failAfter) Write(p []byte) (int, error) {
	if w.n == 0 {
		return 0, errWriteFailed
	}
	w.n--
	return len(p), nil
}

var errWriteFailed = errors.New("write failed")

// Every line of the report propagates a writer error rather than reporting a
// truncated merge as a clean one. The counts are one write and each listed line
// is another, so failing after k writes puts the failure on line k.
func TestWriteCurationReportPropagatesAWriteError(t *testing.T) {
	t.Parallel()

	report := CurationReport{
		Notes: []CurationNote{
			{Name: "Demo.a", Axis: AxisReceiver, Kind: CurationFillIn, Curated: "borrow"},
			{Name: "Demo.b", Axis: AxisReceiver, Kind: CurationCorrection, Curated: "borrow", Analyzed: "mutBorrow"},
		},
		Stale:     []string{"Demo.c"},
		Unmatched: []string{"Demo.d"},
		Refused:   []string{"Demo.e receiver: curated borrow, but a builtin-static has no receiver"},
	}

	// Five writes succeed: the counts, the correction, the stale name, the
	// unmatched name, and the refused axis. The fill-in is summarized rather
	// than listed, so it is not one of them.
	for writes := range 5 {
		require.ErrorIs(t, WriteCurationReport(report, &failAfter{n: writes}), errWriteFailed,
			"a failure on write %d was swallowed", writes)
	}
	require.NoError(t, WriteCurationReport(report, &failAfter{n: 5}))
}

// equal is what sorts a curated entry into redundant or correction, so every
// way two return facts can differ has to register. Members are compared in
// order, which parsing and newReturnFact both establish.
func TestReturnFactEqual(t *testing.T) {
	t.Parallel()

	union := func(members ...AliasRef) ReturnFact {
		return ReturnFact{Kind: AliasUnion, Members: members}
	}
	param := func(i int) AliasRef { return AliasRef{Kind: AliasParam, Index: position(i)} }
	fresh := AliasRef{Kind: AliasFresh}

	tests := map[string]struct {
		a, b ReturnFact
		want bool
	}{
		"SameKind":          {ReturnFact{Kind: AliasFresh}, ReturnFact{Kind: AliasFresh}, true},
		"DifferingKind":     {ReturnFact{Kind: AliasFresh}, ReturnFact{Kind: AliasReceiver}, false},
		"SamePosition":      {returnsParam(1), returnsParam(1), true},
		"DifferingPosition": {returnsParam(0), returnsParam(1), false},
		// A position on one side only. The fields are pointers, so this is the
		// case a plain dereference would panic on.
		"PositionOnOneSide":       {returnsParam(0), ReturnFact{Kind: AliasParam}, false},
		"SameMembers":             {union(fresh, param(0)), union(fresh, param(0)), true},
		"DifferingMemberKind":     {union(fresh, param(0)), union(AliasRef{Kind: AliasReceiver}, param(0)), false},
		"DifferingMemberPosition": {union(fresh, param(0)), union(fresh, param(2)), false},
		"DifferingMemberCount":    {union(fresh, param(0)), union(fresh, param(0), param(1)), false},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, test.want, test.a.equal(test.b))
			require.Equal(t, test.want, test.b.equal(test.a), "equal is not symmetric")
		})
	}
}

// A union is written by hand in whatever order reads best, and parsing puts its
// members in the order the analysis leaves them. Without that, a curated union
// would compare unequal to the analyzed fact it repeats and publish its members
// in an order no other fact uses.
func TestParseCurationSortsUnionMembers(t *testing.T) {
	t.Parallel()

	curation, err := ParseCuration([]byte(`{"entries":{"Demo.m":{"reason":"r","reviewedAt":"abc",` +
		`"returns":{"kind":"union","members":[` +
		`{"kind":"receiver"},{"kind":"param","index":2},{"kind":"fresh"},{"kind":"param","index":0}]}}}}`))
	require.NoError(t, err)

	returns := curation.Entries["Demo.m"].Returns
	require.Equal(t, "union(fresh, param(0), param(2), receiver)", returns.String())
	require.True(t, returns.equal(newReturnFact(aliasSet{members: set.FromSlice([]alias{
		{Kind: AliasReceiver}, {Kind: AliasParam, Index: 2},
		{Kind: AliasFresh}, {Kind: AliasParam, Index: 0},
	})})), "a curated union does not compare equal to the analyzed fact it repeats")
}

// Every name in curated.json addresses a builtin the committed graph holds, and
// every entry was reviewed against the algorithm the graph holds now. This is
// what turns a misspelled key and an algorithm rewritten under a reviewer into
// a failing build rather than a line in a report nobody reads.
func TestCurationMatchesTheGraph(t *testing.T) {
	report := testFacts(t).Curation()

	require.Empty(t, report.Unmatched, "curated names the committed graph holds no builtin for")
	require.Empty(t, report.Stale, "curated entries reviewed against an algorithm the graph no longer holds")
	require.Empty(t, report.Refused, "curated axes the graph contradicts")
}

// An entry the analysis has caught up with is deletable, and leaving it in
// place would grow a second source of truth for a determination that no longer
// needs one.
func TestCurationHasNoRedundantEntries(t *testing.T) {
	var redundant []string
	for _, note := range testFacts(t).Curation().Notes {
		if note.Kind == CurationRedundant {
			redundant = append(redundant, note.String())
		}
	}
	require.Empty(t, redundant)
}

// Every curated determination in one list, which is what a reviewer reads to
// see the whole layer at once.
//
// All 27 are fill-ins. The 24 receivers answer an axis the analysis withheld.
// The three returns answer one it resolved to `unknown`, which names no value,
// and each reads a primitive out of a slot — a copy the analysis cannot
// recognize as one without the declared return type. Nothing here corrects a
// claim the analysis actually made, so §6 has no disagreement to triage yet.
func TestCurationReportOverTheCommittedGraph(t *testing.T) {
	lines := make([]string, 0, len(testFacts(t).Curation().Notes))
	for _, note := range testFacts(t).Curation().Notes {
		lines = append(lines, note.String())
	}
	sort.Strings(lines)
	snaps.MatchSnapshot(t, strings.Join(lines, "\n"))
}

// The report prints its tallies and then every line that needs a reviewer. A
// fill-in is the ordinary case and is counted rather than listed.
func TestWriteCurationReport(t *testing.T) {
	t.Parallel()

	cfg, methods := demoFacts(t)
	stale := entry(t, cfg, "Demo.prototype.read", RecvBorrow)
	stale.ReviewedAt = "000000000000"
	report := mergeCuration(cfg, &Curation{Entries: map[string]CuratedEntry{
		"Demo.prototype.opaque": entry(t, cfg, "Demo.prototype.opaque", RecvMutBorrow),
		"Demo.prototype.read":   stale,
		"Demo.prototype.gone": {
			Reason:     "stated for the test",
			ReviewedAt: "000000000000",
			Receiver:   RecvBorrow,
		},
	}}, methods)

	var out strings.Builder
	require.NoError(t, WriteCurationReport(report, &out))
	snaps.MatchInlineSnapshot(t, out.String(), snaps.Inline(`  curation: 1 fill-ins, 0 corrections, 1 redundant, 1 stale, 1 unmatched, 0 refused
    Demo.prototype.read receiver: redundant borrow over borrow
    Demo.prototype.read: reviewed against an algorithm the graph no longer holds
    Demo.prototype.gone: curated, but the graph holds no such builtin
`))
}
