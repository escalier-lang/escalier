package dts_to_esc

import (
	"strings"
	"testing"

	"github.com/escalier-lang/escalier/internal/ecma262"
	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/require"
)

// factsOf builds a fact set holding one receiver claim per name. The receiver
// is the only axis ValidateReceivers reads, so the facts are left short of the
// return alias, throws, and rejects that generation requires. Nothing here
// consults Facts.Incomplete, which is what would refuse them.
func factsOf(receivers map[string]ecma262.ReceiverKind) *ecma262.Facts {
	methods := make(map[string]ecma262.MethodFact, len(receivers))
	for name, kind := range receivers {
		methods[name] = ecma262.MethodFact{Receiver: kind}
	}
	return &ecma262.Facts{SpecTarget: "test", Methods: methods}
}

// One comparison per verdict, plus each shape the diff leaves out.
func TestValidateReceivers(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		receivers map[string]ecma262.ReceiverKind
		// compared is one rendered line per method both sources answer, with
		// the verdict appended.
		compared []string
		factOnly []string
	}{
		// `String.prototype.substr` has an override entry and the fact below
		// reads its receiver as `borrow`, so the entry repeats what the fact
		// already says. The committed graph carries no `substr` algorithm,
		// which is why the real entry survives.
		"OverrideTheFactAgreesWith": {
			receivers: map[string]ecma262.ReceiverKind{
				"String.prototype.substr": ecma262.RecvBorrow,
			},
			compared: []string{
				"String.prototype.substr: fact borrow, override borrow [redundant]",
			},
		},
		// `Array.prototype.push` matches the mutating `push` prefix, and the
		// analysis charges it a receiver write.
		"HeuristicTheFactAgreesWith": {
			receivers: map[string]ecma262.ReceiverKind{
				"Array.prototype.push": ecma262.RecvMutBorrow,
			},
			compared: []string{
				"Array.prototype.push: fact mutBorrow, heuristic mutBorrow [confirmed]",
			},
		},
		// A heuristic the fact contradicts. The fact tier outranks the name
		// tiers, so `push` is written non-mutating and the mutating `push`
		// prefix never reaches it.
		"HeuristicTheFactOverrules": {
			receivers: map[string]ecma262.ReceiverKind{
				"Array.prototype.push": ecma262.RecvBorrow,
			},
			compared: []string{
				"Array.prototype.push: fact borrow, heuristic mutBorrow [corrected]",
			},
		},
		// A tier-3 convention the fact contradicts. The convention outranks
		// the fact for the same reason an override entry does, so the two
		// score alike.
		"ConventionTheFactContradicts": {
			receivers: map[string]ecma262.ReceiverKind{
				"Array.prototype.toString": ecma262.RecvMutBorrow,
			},
			compared: []string{
				"Array.prototype.toString: fact mutBorrow, well-known borrow [disagreement]",
			},
		},
		// An override entry the fact contradicts. The entry outranks the fact,
		// so this is the one shape the §6 gate fails on: the hand-written
		// answer is what gets written, and one of the two sources is wrong.
		"OverrideTheFactContradicts": {
			receivers: map[string]ecma262.ReceiverKind{
				"String.prototype.substr": ecma262.RecvMutBorrow,
			},
			compared: []string{
				"String.prototype.substr: fact mutBorrow, override borrow [disagreement]",
			},
		},
		// `flat` matches no prefix and sits in no override entry, so the
		// converter reaches it through the `&mut self` default today.
		"NeitherSourceAnswers": {
			receivers: map[string]ecma262.ReceiverKind{
				"Array.prototype.flat": ecma262.RecvBorrow,
			},
			factOnly: []string{"Array.prototype.flat"},
		},
		// A static has no receiver to mutate, an accessor's polarity is fixed
		// where the object type is built, and a symbol-keyed member is
		// addressable by neither hand-written source.
		//
		// `Demo.prototype.opaque` is the fourth shape, a receiver the analysis
		// withheld. Its algorithm in internal/ecma262 is one prose step the
		// serializer could not lower, so the mutation fixpoint reads it as
		// incomplete and publishes no claim at all. Generation refuses such a
		// fact, so only an analyze result carries one.
		"ShapesTheDiffLeavesOut": {
			receivers: map[string]ecma262.ReceiverKind{
				"Array.isArray":                  ecma262.RecvNone,
				"Math.max":                       ecma262.RecvNone,
				"get Map.prototype.size":         ecma262.RecvBorrow,
				"Array.prototype [ @@iterator ]": ecma262.RecvBorrow,
				// "" is ReceiverKind's zero value, which is the absence of a
				// claim rather than one of its three kinds.
				"Demo.prototype.opaque": "",
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			report := ValidateReceivers(factsOf(test.receivers))

			compared := make([]string, 0, len(report.Compared))
			for _, diff := range report.Compared {
				compared = append(compared, diff.String()+" ["+string(diff.Verdict)+"]")
			}
			require.Equal(t, test.compared, nonEmpty(compared))
			require.Equal(t, test.factOnly, nonEmpty(report.FactOnly))
		})
	}
}

// A fact claiming no receiver is dropped even when its spec name normalizes to
// an instance member. The two are usually consistent, so this pins the guard
// that holds when they are not.
//
// `Demo.prototype.set` is that shape. comparableRef accepts the name, since it
// normalizes to a string-keyed instance member with no accessor, so claimsBorrow
// is the only guard that refuses it. Reading `none` as a borrow instead would
// compare the method and score a disagreement against the mutating `set` prefix.
func TestValidateReceiversDropsAReceiverlessInstanceName(t *testing.T) {
	t.Parallel()

	report := ValidateReceivers(factsOf(map[string]ecma262.ReceiverKind{
		"Demo.prototype.set": ecma262.RecvNone,
	}))

	require.Empty(t, report.Compared)
	require.Empty(t, report.FactOnly)
}

// nonEmpty maps an empty slice onto nil so a test case can leave the field out
// rather than spelling an empty literal.
func nonEmpty(lines []string) []string {
	if len(lines) == 0 {
		return nil
	}
	return lines
}

// Every override entry no fact answers. An owner outside ECMA-262 has no fact
// by construction, so the whole entry survives.
func TestValidateReceiversReportsOverridesWithNoFact(t *testing.T) {
	t.Parallel()

	report := ValidateReceivers(factsOf(map[string]ecma262.ReceiverKind{
		"String.prototype.substr": ecma262.RecvBorrow,
	}))

	require.NotContains(t, report.OverrideOnly, "String.substr")
	require.Contains(t, report.OverrideOnly, "Console.log")
}

// The §6 gate. No override entry may contradict the fact for the same method
// over the committed graph. An entry outranks the fact, so a standing
// contradiction means the converter and the prelude write an answer the spec
// analysis disputes, and one of the two sources is wrong.
//
// A spec bump or an added entry that introduces one fails here. Resolving it
// means fixing whichever side is wrong, or curating the method in
// internal/ecma262/curated.json, and recording the disposition in
// planning/ecma-262/validation_diff.md.
func TestCommittedGraphLeavesNoReceiverDisagreement(t *testing.T) {
	t.Parallel()

	report := ValidateReceivers(committedFacts(t))

	lines := make([]string, 0, len(report.Disagreements()))
	for _, diff := range report.Disagreements() {
		lines = append(lines, diff.String())
	}
	require.Empty(t, lines)
}

// The heuristics the facts overrule over the committed graph. Each is a name
// the name tiers answer wrongly wherever else it is spelled, so the list is
// pinned rather than left to grow unread. A new line here is a heuristic to
// re-read, as `copyWithin` was — see planning/ecma-262/validation_diff.md.
func TestCommittedGraphCorrectedHeuristics(t *testing.T) {
	t.Parallel()

	report := ValidateReceivers(committedFacts(t))

	lines := make([]string, 0, len(report.Corrected()))
	for _, diff := range report.Corrected() {
		lines = append(lines, diff.String())
	}
	snaps.MatchInlineSnapshot(t, strings.Join(lines, "\n"), snaps.Inline(`String.prototype.replace: fact borrow, heuristic mutBorrow
String.prototype.replaceAll: fact borrow, heuristic mutBorrow`))
}

// The override entries the facts agree with, which is none of the ones the
// table still holds. An entry that lands on this list is one the facts have
// caught up with, which makes it one to delete.
func TestCommittedGraphRedundantOverrides(t *testing.T) {
	t.Parallel()

	report := ValidateReceivers(committedFacts(t))

	snaps.MatchInlineSnapshot(t, strings.Join(report.Redundant(), "\n"), snaps.Inline(""))
}

// The override entries no fact answers, which are the ones nonMutatingOverrides
// still holds. Every `web:*` owner is out of ECMA-262 scope by construction,
// and `String.substr` is an Annex B method the graph does not carry.
func TestCommittedGraphOverridesWithNoFact(t *testing.T) {
	t.Parallel()

	report := ValidateReceivers(committedFacts(t))

	snaps.MatchInlineSnapshot(t, strings.Join(report.OverrideOnly, "\n"), snaps.Inline(`Body.arrayBuffer
Body.blob
Body.bytes
Body.formData
Body.json
Body.text
Console.assert
Console.clear
Console.debug
Console.dir
Console.dirxml
Console.error
Console.group
Console.groupCollapsed
Console.groupEnd
Console.info
Console.log
Console.table
Console.time
Console.timeEnd
Console.timeLog
Console.timeStamp
Console.trace
Console.warn
Request.arrayBuffer
Request.blob
Request.bytes
Request.formData
Request.json
Request.text
Response.arrayBuffer
Response.blob
Response.bytes
Response.formData
Response.json
Response.text
String.substr`))
}

// The summary line and the four lists a reviewer acts on. A confirmed method is
// summarized rather than listed.
func TestWriteValidationReport(t *testing.T) {
	t.Parallel()

	report := ValidateReceivers(factsOf(map[string]ecma262.ReceiverKind{
		"Array.prototype.push":    ecma262.RecvMutBorrow,
		"Array.prototype.flat":    ecma262.RecvBorrow,
		"Array.prototype.sort":    ecma262.RecvBorrow,
		"String.prototype.substr": ecma262.RecvMutBorrow,
	}))

	var out strings.Builder
	require.NoError(t, WriteValidationReport(report, &out))

	// Only the entries the four facts address are compared, so every other
	// override entry lands in the last list. The lines below are the head of
	// it, and the assertion after them covers the rest.
	require.True(t, strings.HasPrefix(out.String(),
		`  receivers: 1 confirmed by a name tier, 1 heuristics corrected, 0 redundant overrides, 1 disagreements, 1 answered by the facts alone, 36 overrides no fact answers
    disagreement: String.prototype.substr: fact mutBorrow, override borrow
    corrected heuristic: Array.prototype.sort: fact borrow, heuristic mutBorrow
    override with no fact: Body.arrayBuffer
`), out.String())
	require.Contains(t, out.String(), "    override with no fact: Console.warn\n")
}

// committedFacts is the published fact set for the committed graph.
func committedFacts(t *testing.T) *ecma262.Facts {
	t.Helper()
	facts, err := ecma262.CommittedFacts()
	require.NoError(t, err)
	return facts
}
