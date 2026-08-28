package ecma262

import (
	"sort"
	"strings"
	"testing"

	"github.com/escalier-lang/escalier/internal/set"
	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/require"
)

// demoCFG is a two-method graph standing in for the shapes the committed one
// holds. `read` returns its receiver and writes nothing, so the analysis
// classifies both determinations. `opaque` is a step the serializer could not
// lower, which withholds the receiver and leaves the return unresolved.
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
		Classified: Coverage{Receiver: true},
		Receiver:   kind,
	}
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
		// Curating one axis leaves the other where the analysis left it, which
		// is what makes the layer compose with the analysis rather than shadow
		// it.
		"OneAxisLeavesTheOther": {
			entries: func(cfg *CFG) map[string]CuratedEntry {
				e := entry(t, cfg, "Demo.prototype.opaque", RecvBorrow)
				e.Classified = Coverage{Returns: true}
				e.Receiver = ""
				e.Returns = ReturnFact{Kind: AliasFresh}
				return map[string]CuratedEntry{"Demo.prototype.opaque": e}
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
			Classified: Coverage{Receiver: true},
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
			`{"reviewedAt":"abc","classified":{"receiver":true},"receiver":"borrow"}`,
			"curated entry Demo.m: has no reason",
		},
		"NoReviewedAt": {
			`{"reason":"r","classified":{"receiver":true},"receiver":"borrow"}`,
			"curated entry Demo.m: has no reviewedAt digest",
		},
		"NoDetermination": {
			`{"reason":"r","reviewedAt":"abc","classified":{}}`,
			"curated entry Demo.m: claims no determination",
		},
		"UnknownReceiverKind": {
			`{"reason":"r","reviewedAt":"abc","classified":{"receiver":true},"receiver":"shared"}`,
			`curated entry Demo.m: claims receiver "shared", which is not a receiver kind`,
		},
		"MissingReceiverKind": {
			`{"reason":"r","reviewedAt":"abc","classified":{"receiver":true}}`,
			`curated entry Demo.m: claims receiver "", which is not a receiver kind`,
		},
		"UnknownAliasKind": {
			`{"reason":"r","reviewedAt":"abc","classified":{"returns":true},"returns":{"kind":"borrowed"}}`,
			`curated entry Demo.m: returns "borrowed", which is not an alias kind`,
		},
		"ParamWithNoPosition": {
			`{"reason":"r","reviewedAt":"abc","classified":{"returns":true},"returns":{"kind":"param"}}`,
			"curated entry Demo.m: returns a parameter but names no position",
		},
		"PositionOnANonParamReturn": {
			`{"reason":"r","reviewedAt":"abc","classified":{"returns":true},"returns":{"kind":"fresh","index":0}}`,
			"curated entry Demo.m: returns fresh but names a position",
		},
		"UnionOfOne": {
			`{"reason":"r","reviewedAt":"abc","classified":{"returns":true},"returns":{"kind":"union","members":[{"kind":"fresh"}]}}`,
			"curated entry Demo.m: returns a union of 1 members",
		},
		"UnionHoldingUnknown": {
			`{"reason":"r","reviewedAt":"abc","classified":{"returns":true},"returns":{"kind":"union","members":[{"kind":"fresh"},{"kind":"unknown"}]}}`,
			"curated entry Demo.m: returns a union holding unknown",
		},
		// A value on an axis the coverage leaves unclaimed would be dropped by
		// the merge with no report line, so a reviewer would see silence rather
		// than the claim they wrote.
		"ReceiverOnAnUnclaimedAxis": {
			`{"reason":"r","reviewedAt":"abc","classified":{"returns":true},"returns":{"kind":"fresh"},"receiver":"borrow"}`,
			`curated entry Demo.m: names receiver "borrow" on an axis its coverage leaves unclaimed`,
		},
		"ReturnsOnAnUnclaimedAxis": {
			`{"reason":"r","reviewedAt":"abc","classified":{"receiver":true},"receiver":"borrow","returns":{"kind":"fresh"}}`,
			"curated entry Demo.m: names returns fresh on an axis its coverage leaves unclaimed",
		},
		"UnionNamingOneValueTwice": {
			`{"reason":"r","reviewedAt":"abc","classified":{"returns":true},"returns":{"kind":"union","members":[{"kind":"param","index":1},{"kind":"param","index":1}]}}`,
			"curated entry Demo.m: returns a union naming param(1) twice",
		},
		"MembersOnANonUnionReturn": {
			`{"reason":"r","reviewedAt":"abc","classified":{"returns":true},"returns":{"kind":"fresh","members":[{"kind":"fresh"}]}}`,
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

	body := `{"reason":"r","reviewedAt":"abc","classified":{"returns":true},"returns":`
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

// A union is written by hand in whatever order reads best, and parsing puts its
// members in the order the analysis leaves them. Without that, a curated union
// would compare unequal to the analyzed fact it repeats and publish its members
// in an order no other fact uses.
func TestParseCurationSortsUnionMembers(t *testing.T) {
	t.Parallel()

	curation, err := ParseCuration([]byte(`{"entries":{"Demo.m":{"reason":"r","reviewedAt":"abc",` +
		`"classified":{"returns":true},"returns":{"kind":"union","members":[` +
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
			Classified: Coverage{Receiver: true},
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
