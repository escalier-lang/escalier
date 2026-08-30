package dts_to_esc

import (
	"strings"
	"testing"

	"github.com/escalier-lang/escalier/internal/ecma262"
	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/require"
)

// committedCFG is the control-flow graph tools/spec-extract commits.
const committedCFG = "../../tools/spec-extract/cfg.json"

// factsOf builds a published fact set holding one receiver claim per name.
// Every fact also carries a return alias, since generation refuses a fact with
// a hole in it, and the diff reads neither that nor the spec target.
func factsOf(receivers map[string]ecma262.ReceiverKind) *ecma262.Facts {
	methods := make(map[string]ecma262.MethodFact, len(receivers))
	for name, kind := range receivers {
		methods[name] = ecma262.MethodFact{
			Receiver: kind,
			Returns:  ecma262.ReturnFact{Kind: ecma262.AliasFresh},
		}
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
		// `String.prototype.charAt` has an override entry and the analysis
		// reads its receiver as `borrow`, so the entry repeats what the fact
		// already says.
		"OverrideTheFactAgreesWith": {
			receivers: map[string]ecma262.ReceiverKind{
				"String.prototype.charAt": ecma262.RecvBorrow,
			},
			compared: []string{
				"String.prototype.charAt: fact borrow, override borrow [redundant]",
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
		// The two directions a disagreement runs in. A fact that mutates
		// against a source that does not is the soundness-relevant one, since
		// the hand-written answer would put the method on a non-mut receiver.
		"Disagreements": {
			receivers: map[string]ecma262.ReceiverKind{
				"String.prototype.charAt": ecma262.RecvMutBorrow,
				"Array.prototype.push":    ecma262.RecvBorrow,
			},
			compared: []string{
				"Array.prototype.push: fact borrow, heuristic mutBorrow [disagreement]",
				"String.prototype.charAt: fact mutBorrow, override borrow [disagreement]",
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
		// addressable by neither hand-written source. A withheld receiver
		// claim leaves the fact with nothing to compare.
		//
		// `Demo.prototype.set` is the case where the two signals for "no
		// receiver" point different ways. Its name normalizes to an instance
		// member, while its fact says the algorithm takes no receiver. Either
		// signal alone drops the method, so it is left out and nothing is
		// compared. Reading `none` as a borrow instead would compare it and
		// score a disagreement against the mutating `set` prefix.
		"ShapesTheDiffLeavesOut": {
			receivers: map[string]ecma262.ReceiverKind{
				"Array.isArray":                  ecma262.RecvNone,
				"Math.max":                       ecma262.RecvNone,
				"Demo.prototype.set":             ecma262.RecvNone,
				"get Map.prototype.size":         ecma262.RecvBorrow,
				"Array.prototype [ @@iterator ]": ecma262.RecvBorrow,
				"Array.prototype.sort":           "",
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

// nonEmpty maps an empty slice onto nil so a test case can leave the field out
// rather than spelling an empty literal.
func nonEmpty(lines []string) []string {
	if len(lines) == 0 {
		return nil
	}
	return lines
}

// Every override entry no fact answers, which §7 keeps. An owner outside
// ECMA-262 has no fact by construction, so the whole entry survives.
func TestValidateReceiversReportsOverridesWithNoFact(t *testing.T) {
	t.Parallel()

	report := ValidateReceivers(factsOf(map[string]ecma262.ReceiverKind{
		"String.prototype.charAt": ecma262.RecvBorrow,
	}))

	require.NotContains(t, report.OverrideOnly, "String.charAt")
	require.Contains(t, report.OverrideOnly, "String.trim")
	require.Contains(t, report.OverrideOnly, "Console.log")
}

// The §6 gate. The committed graph must leave no receiver the two sources
// answer differently, because §7 ranks the facts above the name tiers and a
// standing disagreement would silently change what the converter emits.
//
// A spec bump or a heuristic edit that reintroduces one fails here. Resolving
// it means fixing whichever side is wrong, or curating the method in
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

// The override entries the facts agree with. §7 deletes exactly this list from
// nonMutatingOverrides, so it is pinned here rather than recomputed there.
func TestCommittedGraphRedundantOverrides(t *testing.T) {
	t.Parallel()

	report := ValidateReceivers(committedFacts(t))

	snaps.MatchInlineSnapshot(t, strings.Join(report.Redundant(), "\n"), snaps.Inline(`Function.apply
Function.bind
Function.call
Object.propertyIsEnumerable
String.charAt
String.charCodeAt
String.codePointAt
String.endsWith
String.localeCompare
String.match
String.matchAll
String.normalize
String.padEnd
String.padStart
String.repeat
String.replace
String.replaceAll
String.search
String.split
String.startsWith
String.substring
String.trim
String.trimEnd
String.trimStart`))
}

// The override entries no fact answers, which §7 keeps. Every `web:*` owner is
// out of ECMA-262 scope by construction, and `String.substr` is an Annex B
// method the graph does not carry.
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

// The summary line and the three lists a reviewer acts on. A confirmed method
// is summarized rather than listed.
func TestWriteValidationReport(t *testing.T) {
	t.Parallel()

	report := ValidateReceivers(factsOf(map[string]ecma262.ReceiverKind{
		"Array.prototype.push":    ecma262.RecvMutBorrow,
		"Array.prototype.flat":    ecma262.RecvBorrow,
		"String.prototype.charAt": ecma262.RecvBorrow,
		"String.prototype.trim":   ecma262.RecvMutBorrow,
	}))

	var out strings.Builder
	require.NoError(t, WriteValidationReport(report, &out))

	// Only the entries the four facts address are compared, so every other
	// override entry lands in the third list. The lines below are the head of
	// it, and the assertion after them covers the rest.
	require.True(t, strings.HasPrefix(out.String(),
		`  receivers: 1 confirmed by a heuristic, 1 redundant overrides, 1 disagreements, 1 answered by the facts alone, 59 overrides no fact answers
    disagreement: String.prototype.trim: fact mutBorrow, override borrow
    redundant override: String.charAt
    override with no fact: Body.arrayBuffer
`), out.String())
	require.Contains(t, out.String(), "    override with no fact: String.trimEnd\n")
}

// committedFacts is the published fact set for the committed graph.
func committedFacts(t *testing.T) *ecma262.Facts {
	t.Helper()
	cfg, err := ecma262.LoadCFG(committedCFG)
	require.NoError(t, err)
	facts, err := ecma262.NewFacts(cfg)
	require.NoError(t, err)
	return facts
}
