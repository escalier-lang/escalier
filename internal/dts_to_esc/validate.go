package dts_to_esc

import (
	"fmt"
	"io"
	"sort"

	"github.com/escalier-lang/escalier/internal/ecma262"
)

// validate.go is the receiver-mutability validation diff of
// planning/ecma-262/implementation_plan.md §6. It measures the spec-derived
// receiver claim of every ECMA-262 builtin against the hand-written answer the
// converter reaches today, so §7 can rank the facts above the name tiers with
// evidence rather than on faith.
//
// The hand-written answer is the union of two sources. nonMutatingOverrides
// names, per owner, the methods an override marks non-mutating.
// ClassifyMethodByName answers from the method's name alone. An entry outranks
// the heuristics, matching the tier order Classify walks.
//
// The diff is over methods, not statics. A fact for a static carries the
// receiver kind `none`, which claims nothing about mutability, and the
// hand-written sources have no matching notion.

// ReceiverSource says which hand-written source answered a method.
type ReceiverSource string

const (
	// SourceOverride is an entry in nonMutatingOverrides. It outranks the
	// heuristics, so a method with an entry is never answered by one.
	SourceOverride ReceiverSource = "override"
	// SourceHeuristic is a name-only tier of ClassifyMethodByName, which
	// covers the well-known allow-list, the `get*` prefix rule, and the
	// name-based prefixes.
	SourceHeuristic ReceiverSource = "heuristic"
)

// ReceiverVerdict is what the diff concluded about one method.
type ReceiverVerdict string

const (
	// VerdictRedundant is an override entry the fact agrees with. §7 deletes
	// these, since the fact answers the method on its own.
	VerdictRedundant ReceiverVerdict = "redundant"
	// VerdictConfirmed is a heuristic the fact agrees with. Nothing to delete,
	// because a heuristic classifies by name rather than per method, but the
	// agreement is what makes the fact source trustworthy at scale.
	VerdictConfirmed ReceiverVerdict = "confirmed"
	// VerdictDisagreement is a method the two sources answer differently. Each
	// one is either a hand-written answer the fact corrects or an analyzer bug
	// §4 has to fix, and the report cannot tell the two apart. Triaging them
	// is the §6 gate.
	VerdictDisagreement ReceiverVerdict = "disagreement"
)

// ReceiverDiff is one method both sources answer.
type ReceiverDiff struct {
	// SpecName is the canonical ECMA-262 key the fact is published under, such
	// as "Array.prototype.copyWithin".
	SpecName string
	// Ref is the member address SpecName normalizes to, which is how the
	// override table is keyed.
	Ref ecma262.MemberRef
	// FactMut is what the spec analysis concluded, true for `mutBorrow`.
	FactMut bool
	// HandMut is what the hand-written sources concluded, true for a mutating
	// receiver.
	HandMut bool
	// Source names which hand-written source produced HandMut.
	Source  ReceiverSource
	Verdict ReceiverVerdict
}

// String renders one line naming the method, the two answers, and the source
// the hand-written answer came from.
func (d ReceiverDiff) String() string {
	return fmt.Sprintf("%s: fact %s, %s %s",
		d.SpecName, receiverWord(d.FactMut), d.Source, receiverWord(d.HandMut))
}

// receiverWord spells a receiver-mutability answer the way both sides of the
// diff read it.
func receiverWord(mut bool) string {
	if mut {
		return "mutBorrow"
	}
	return "borrow"
}

// ValidationReport is one run of the diff.
type ValidationReport struct {
	// Compared holds every method both sources answer, sorted by spec name.
	Compared []ReceiverDiff
	// FactOnly holds the methods only the facts answer, sorted by spec name.
	// These are what §7 adds: today each falls through to the `&mut self`
	// default whatever the spec says.
	FactOnly []string
	// OverrideOnly holds the override entries no fact answers, one rendered
	// `Owner.member` line each, sorted. §7 keeps every one of them. A `web:*`
	// class such as `Console` is out of ECMA-262 scope by construction, and
	// `String.substr` is an Annex B method the graph does not carry.
	OverrideOnly []string
}

// Disagreements returns the compared methods the two sources answer
// differently, in the order Compared holds them. This is the list the §6 gate
// is about.
func (r ValidationReport) Disagreements() []ReceiverDiff {
	var found []ReceiverDiff
	for _, diff := range r.Compared {
		if diff.Verdict == VerdictDisagreement {
			found = append(found, diff)
		}
	}
	return found
}

// Redundant returns the override entries the facts agree with, one rendered
// `Owner.member` line each, in the order Compared holds them. §7 removes these
// from nonMutatingOverrides.
func (r ValidationReport) Redundant() []string {
	var found []string
	for _, diff := range r.Compared {
		if diff.Verdict == VerdictRedundant {
			found = append(found, diff.Ref.Owner+"."+diff.Ref.Member.String())
		}
	}
	return found
}

// Counts tallies the compared methods by verdict, which is what
// WriteValidationReport's summary line is built from.
func (r ValidationReport) Counts() map[ReceiverVerdict]int {
	counts := map[ReceiverVerdict]int{}
	for _, diff := range r.Compared {
		counts[diff.Verdict]++
	}
	return counts
}

// ValidateReceivers diffs the receiver claim of every published fact against
// the hand-written answer for the same method.
//
// A fact takes part only when it addresses an instance method by a string key
// and carries a receiver claim. The three exclusions each name a method the
// hand-written sources cannot answer:
//
//   - A static or a namespace function has no receiver to mutate.
//   - An accessor's polarity is fixed where the object type is built, so no
//     tier is consulted for it.
//   - A symbol-keyed member cannot be addressed by the string-keyed override
//     table, and ClassifyMethodByName reads a bare name.
func ValidateReceivers(facts *ecma262.Facts) ValidationReport {
	var report ValidationReport
	claimed := map[string]bool{}

	for _, specName := range sortedFactNames(facts) {
		fact, _ := facts.Of(specName)
		ref, ok := ecma262.Normalize(specName)
		if !ok || !comparableRef(ref) || fact.Receiver == "" {
			continue
		}
		diff := ReceiverDiff{
			SpecName: specName,
			Ref:      ref,
			FactMut:  fact.Receiver == ecma262.RecvMutBorrow,
		}
		member := ref.Member.Name
		handMut, source, answered := handAnswer(ref.Owner, member)
		if !answered {
			report.FactOnly = append(report.FactOnly, specName)
			continue
		}
		diff.HandMut, diff.Source = handMut, source
		if diff.Source == SourceOverride {
			claimed[ref.Owner+"."+member] = true
		}
		diff.Verdict = verdictOf(diff)
		report.Compared = append(report.Compared, diff)
	}

	for _, entry := range overrideEntries() {
		if !claimed[entry] {
			report.OverrideOnly = append(report.OverrideOnly, entry)
		}
	}
	return report
}

// comparableRef reports whether a fact's member address is one the
// hand-written sources can answer. See ValidateReceivers for the three shapes
// it refuses.
func comparableRef(ref ecma262.MemberRef) bool {
	return ref.Sort == ecma262.SortInstance &&
		ref.Accessor == ecma262.NotAccessor &&
		ref.Member.Kind == ecma262.StrKey
}

// handAnswer returns the receiver mutability the hand-written sources reach
// for one member, the source that reached it, and whether either answered at
// all. An override entry is consulted first, matching the tier order Classify
// walks. A member neither source answers falls to the `&mut self` default,
// which is an absence of a claim rather than one, so it is reported apart.
func handAnswer(owner, member string) (mut bool, source ReceiverSource, answered bool) {
	if NonMutatingOverrides(owner).Contains(member) {
		return false, SourceOverride, true
	}
	if mut, ok := ClassifyMethodByName(member); ok {
		return mut, SourceHeuristic, true
	}
	return false, "", false
}

// verdictOf reads one comparison. The two sources either answer alike, which
// the source decides what to do about, or they contradict each other.
func verdictOf(diff ReceiverDiff) ReceiverVerdict {
	switch {
	case diff.FactMut != diff.HandMut:
		return VerdictDisagreement
	case diff.Source == SourceOverride:
		return VerdictRedundant
	default:
		return VerdictConfirmed
	}
}

// sortedFactNames returns the published spec names in order, so the report
// reads the same way whatever order the map hands them back.
func sortedFactNames(facts *ecma262.Facts) []string {
	names := make([]string, 0, len(facts.Methods))
	for name := range facts.Methods {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// overrideEntries returns every override entry as an `Owner.member` line,
// sorted.
func overrideEntries() []string {
	var entries []string
	for owner, members := range nonMutatingOverrides {
		for _, member := range members.ToSlice() {
			entries = append(entries, owner+"."+member)
		}
	}
	sort.Strings(entries)
	return entries
}

// WriteValidationReport prints the diff's tallies and every line a reviewer
// acts on. A confirmed method is the ordinary case and is summarized rather
// than listed. A disagreement needs triage, a redundant entry is one §7
// deletes, and an override entry no fact answers is one §7 keeps, so each of
// those is printed in full.
//
// The fact-only names are counted and not listed. There are dozens, and each
// one says only that the facts answer a method the name tiers leave to the
// `&mut self` default, which is what §7 is for rather than something to
// review.
func WriteValidationReport(report ValidationReport, w io.Writer) error {
	counts := report.Counts()
	_, err := fmt.Fprintf(w, "  receivers: %d confirmed by a heuristic, %d redundant overrides, %d disagreements, %d answered by the facts alone, %d overrides no fact answers\n",
		counts[VerdictConfirmed], counts[VerdictRedundant], counts[VerdictDisagreement],
		len(report.FactOnly), len(report.OverrideOnly))
	if err != nil {
		return err
	}
	for _, diff := range report.Disagreements() {
		if _, err := fmt.Fprintf(w, "    disagreement: %s\n", diff); err != nil {
			return err
		}
	}
	for _, entry := range report.Redundant() {
		if _, err := fmt.Fprintf(w, "    redundant override: %s\n", entry); err != nil {
			return err
		}
	}
	for _, entry := range report.OverrideOnly {
		if _, err := fmt.Fprintf(w, "    override with no fact: %s\n", entry); err != nil {
			return err
		}
	}
	return nil
}
