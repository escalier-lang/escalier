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

			require.Equal(t, test.want, classified(methods[test.name]))
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
	require.Equal(t, "receiver:none returns:fresh", classified(methods["Demo.make"]))

	report := mergeCuration(cfg, &Curation{Entries: map[string]CuratedEntry{
		"Demo.make": entry(t, cfg, "Demo.make", RecvBorrow),
	}}, methods)

	require.Equal(t, []string{"Demo.make receiver: curated borrow, but a builtin-static has no receiver"}, report.Refused)
	require.Empty(t, report.Notes)
	require.Equal(t, "receiver:none returns:fresh", classified(methods["Demo.make"]))
}

// The mirror case. A method has a receiver whatever the review says.
func TestMergeCurationRefusesNoneOnAMethod(t *testing.T) {
	t.Parallel()

	cfg, methods := demoFacts(t)
	report := mergeCuration(cfg, &Curation{Entries: map[string]CuratedEntry{
		"Demo.prototype.opaque": entry(t, cfg, "Demo.prototype.opaque", RecvNone),
	}}, methods)

	require.Equal(t, []string{"Demo.prototype.opaque receiver: curated none, but a builtin-method has a receiver"}, report.Refused)
	require.Equal(t, "returns:unknown", classified(methods["Demo.prototype.opaque"]))
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
	require.Equal(t, "receiver:mutBorrow returns:unknown", classified(methods["Demo.prototype.opaque"]))
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
	tests := []struct {
		name    string
		returns string
		want    string
	}{
		{"Fresh", `{"kind":"fresh"}`, "fresh"},
		{"Receiver", `{"kind":"receiver"}`, "receiver"},
		{"Unknown", `{"kind":"unknown"}`, "unknown"},
		{"Param", `{"kind":"param","index":0}`, "param(0)"},
		{
			"Union",
			`{"kind":"union","members":[{"kind":"fresh"},{"kind":"param","index":1}]}`,
			"union(fresh, param(1))",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			curation, err := ParseCuration([]byte(`{"entries":{"Demo.m":` + body + test.returns + `}}}`))
			require.NoError(t, err)
			require.Equal(t, test.want, curation.Entries["Demo.m"].Returns.String())
		})
	}
}

// A layer that is not JSON at all fails before any entry is read, which is the
// other way committed data can be wrong.
func TestParseCurationRejectsMalformedJSON(t *testing.T) {
	t.Parallel()

	_, err := ParseCuration([]byte(`{"entries":`))
	require.EqualError(t, err, "decoding curation: unexpected end of JSON input")
}

// The committed layer parses, which is what every NewFacts call depends on.
func TestCommittedLayerParses(t *testing.T) {
	t.Parallel()

	curation, err := parseCommitted(curatedJSON)
	require.NoError(t, err)
	require.NotEmpty(t, curation.Entries)
}

// A malformed layer reaches the caller as an error that names both the file and
// the entry at fault, rather than taking the process down at init.
func TestParseCommittedReportsAnInvalidFile(t *testing.T) {
	t.Parallel()

	_, err := parseCommitted([]byte(`{"entries":{"Demo.m":{"reviewedAt":"abc","receiver":"borrow"}}}`))
	require.EqualError(t, err,
		"committed curated.json is invalid: curated entry Demo.m: has no reason")
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

// A curated axis that contradicts a claim the analysis published is either a
// spec subtlety the graph cannot express or an analyzer bug, and the report
// cannot tell the two apart. §6's gate reads this category first, so the first
// one to appear fails here rather than reaching facts.json unreviewed. Record
// its disposition in planning/ecma-262/validation_diff.md.
func TestCurationLeavesNoCorrection(t *testing.T) {
	var corrections []string
	for _, note := range testFacts(t).Curation().Notes {
		if note.Kind == CurationCorrection {
			corrections = append(corrections, note.String())
		}
	}
	require.Empty(t, corrections)
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
