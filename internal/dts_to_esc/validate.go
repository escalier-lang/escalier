package dts_to_esc

import (
	"fmt"
	"io"
	"sort"

	"github.com/escalier-lang/escalier/internal/ecma262"
)

// validate.go is the receiver-mutability validation diff of
// planning/ecma-262/implementation_plan.md §6. It measures the spec-derived
// receiver claim of every ECMA-262 builtin against the hand-written answer for
// the same method, which is the evidence §7 ranked the facts above the name
// tiers on and which keeps a spec bump from changing an answer unnoticed.
//
// The hand-written answer is the union of two sources. nonMutatingOverrides
// names, per owner, the methods an override marks non-mutating.
// ClassifyMethodByName answers from the method's name alone. An entry outranks
// both the facts and the heuristics, matching the order
// `checker.UpdateMethodMutability` reads them in.
//
// The diff is over methods, not statics. A fact for a static carries the
// receiver kind `none`, which claims nothing about mutability, and the
// hand-written sources have no matching notion.

// ReceiverSource says which hand-written source answered a method.
type ReceiverSource string

const (
	// SourceOverride is an entry in nonMutatingOverrides. It outranks the
	// facts, so a method with an entry is answered by nothing below it.
	SourceOverride ReceiverSource = "override"
	// SourceWellKnown is the tier-3 allow-list of names that are
	// non-mutating by convention whatever type declares them, `toString` and
	// `valueOf` among them. It outranks the facts for the same reason an
	// override entry does: it is an answer a person wrote down.
	SourceWellKnown ReceiverSource = "well-known"
	// SourceHeuristic is a name-only tier below the facts, which covers the
	// `get*` prefix rule and the name-based prefixes.
	SourceHeuristic ReceiverSource = "heuristic"
)

// outranksFact reports whether a hand-written answer from this source wins
// where it contradicts the fact. Both readers walk the same order: the
// override table and the tier-3 conventions are consulted above the facts, and
// the name tiers below them.
func (s ReceiverSource) outranksFact() bool {
	return s == SourceOverride || s == SourceWellKnown
}

// ReceiverVerdict is what the diff concluded about one method.
type ReceiverVerdict string

const (
	// VerdictRedundant is an override entry the fact agrees with. The entry
	// repeats what the fact already says, so it is one to delete.
	VerdictRedundant ReceiverVerdict = "redundant"
	// VerdictConfirmed is a heuristic or a tier-3 convention the fact agrees
	// with. There is nothing to delete, because neither is written per
	// method. These agreements are the bulk of the evidence the §6 gate rests
	// on.
	VerdictConfirmed ReceiverVerdict = "confirmed"
	// VerdictCorrected is a heuristic the fact overrules. The fact tier sits
	// above the name tiers, so the fact is what the converter and the prelude
	// write and the heuristic's answer never reaches a receiver.
	// `String.prototype.replace` is one: the mutating `replace` prefix reads
	// it as a writer, and the spec algorithm returns a new string.
	//
	// Each one is still worth reading. A heuristic wrong on a spec method is
	// wrong on every other type that spells a method the same way, which is
	// how the `copyWithin` exact-name entry was found.
	VerdictCorrected ReceiverVerdict = "corrected"
	// VerdictDisagreement is a fact that a source above it contradicts, which
	// is an override entry or a tier-3 convention. That source outranks the
	// fact, so the hand-written answer is what gets written and the fact is
	// inert. Either the hand-written answer is stale or the analysis has a
	// bug, and the report cannot tell the two apart. Triaging them is the §6
	// gate.
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

// receiverWord spells a receiver-mutability answer with the two words
// ecma262.ReceiverKind uses, so both sides of the diff render alike.
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
	// These are what §7 adds. Each one falls through to the `&mut self`
	// default today, whatever the spec says about it.
	FactOnly []string
	// OverrideOnly holds the override entries no fact answers, one rendered
	// `Owner.member` line each, sorted. §7 keeps every one of them. A `web:*`
	// class such as `Console` is out of ECMA-262 scope by construction, and
	// `String.substr` is an Annex B method the graph does not carry.
	OverrideOnly []string
}

// Disagreements returns the compared methods whose override entry contradicts
// the fact, in the order Compared holds them. This is the list the §6 gate is
// about.
func (r ValidationReport) Disagreements() []ReceiverDiff {
	return r.withVerdict(VerdictDisagreement)
}

// Corrected returns the compared methods whose heuristic the fact overrules, in
// the order Compared holds them. Each is a name the heuristics answer wrongly
// on every type that spells a method that way, so the list is a review item
// rather than a failure.
func (r ValidationReport) Corrected() []ReceiverDiff {
	return r.withVerdict(VerdictCorrected)
}

// withVerdict returns the compared methods the diff reached one verdict on.
func (r ValidationReport) withVerdict(verdict ReceiverVerdict) []ReceiverDiff {
	var found []ReceiverDiff
	for _, diff := range r.Compared {
		if diff.Verdict == verdict {
			found = append(found, diff)
		}
	}
	return found
}

// Redundant returns the override entries the facts agree with, one rendered
// `Owner.member` line each, in the order Compared holds them. Each is an entry
// nonMutatingOverrides can drop.
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
// and claims a receiver to borrow. The exclusions each name a method the
// hand-written sources cannot answer:
//
//   - A static or a namespace function has no receiver to mutate. Two things
//     say so, the `none` receiver kind the fact carries and the member sort
//     the spec name normalizes to, and either one is enough to leave the
//     method out. So a name whose shape disagrees with its algorithm's kind is
//     dropped rather than read as a borrow.
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
		if !ok || !comparableRef(ref) || !claimsBorrow(fact.Receiver) {
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

// claimsBorrow reports whether a receiver kind is a mutability claim about a
// value the caller holds. A withheld claim is empty, and `none` says the
// builtin takes no receiver at all, so neither is one of the two answers the
// diff compares.
func claimsBorrow(kind ecma262.ReceiverKind) bool {
	return kind == ecma262.RecvBorrow || kind == ecma262.RecvMutBorrow
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
	if wellKnownMember(ecma262.StrMember(member)) {
		return false, SourceWellKnown, true
	}
	if mut, ok := ClassifyMethodByName(member); ok {
		return mut, SourceHeuristic, true
	}
	return false, "", false
}

// verdictOf reads one comparison. The two sources either answer alike or
// contradict each other, and what each of those means depends on where the
// hand-written source sits relative to the fact tier.
func verdictOf(diff ReceiverDiff) ReceiverVerdict {
	agree := diff.FactMut == diff.HandMut
	switch {
	case agree && diff.Source == SourceOverride:
		return VerdictRedundant
	case agree:
		return VerdictConfirmed
	case diff.Source.outranksFact():
		return VerdictDisagreement
	default:
		return VerdictCorrected
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
// than listed. A disagreement needs triage, a corrected heuristic is a name to
// re-read, a redundant entry is one to delete, and an override entry no fact
// answers is one to keep, so each of those is printed in full.
//
// The fact-only names are counted and not listed. There are dozens of them,
// and each one says only that the facts answer a method the name tiers leave to
// the `&mut self` default. That is what §7 is for rather than something to
// review.
func WriteValidationReport(report ValidationReport, w io.Writer) error {
	counts := report.Counts()
	_, err := fmt.Fprintf(w, "  receivers: %d confirmed by a name tier, %d heuristics corrected, %d redundant overrides, %d disagreements, %d answered by the facts alone, %d overrides no fact answers\n",
		counts[VerdictConfirmed], counts[VerdictCorrected], counts[VerdictRedundant],
		counts[VerdictDisagreement], len(report.FactOnly), len(report.OverrideOnly))
	if err != nil {
		return err
	}
	for _, diff := range report.Disagreements() {
		if _, err := fmt.Fprintf(w, "    disagreement: %s\n", diff); err != nil {
			return err
		}
	}
	for _, diff := range report.Corrected() {
		if _, err := fmt.Fprintf(w, "    corrected heuristic: %s\n", diff); err != nil {
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
