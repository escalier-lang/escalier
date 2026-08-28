package ecma262

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/escalier-lang/escalier/internal/set"
)

// curated.go holds the hand-written fact layer described in
// planning/ecma-262/implementation_plan.md §4.4. The analyses in origin.go,
// mutation.go, and throws.go answer the determinations they can read off the
// control-flow graph; this layer answers the rest by review, so a determination
// the graph does not settle costs a curated entry rather than more analyzer.
//
// The layer is merged per determination, never per method. A curated entry that
// names the receiver leaves the same method's return alias to the analysis, so
// the two sources compose instead of one shadowing the other.

//go:embed curated.json
var curatedJSON []byte

// curated is the committed layer, parsed once. The file ships in the binary and
// its shape is fixed at build time, so a parse failure is a defect in committed
// data rather than a condition a caller can meet or recover from.
var curated = mustParseCuration(curatedJSON)

// Axis names one determination of a MethodFact. Coverage carries a flag per
// axis, and a curated entry supplies or corrects the axes it names.
type Axis string

const (
	AxisReceiver Axis = "receiver"
	AxisReturns  Axis = "returns"
)

// CuratedEntry is one reviewed record, keyed in curated.json by the canonical
// spec name of Appendix C. It carries the same determination fields as
// MethodFact plus the provenance a reviewer needs.
//
// Classified says which determinations the entry claims, exactly as it does on
// a MethodFact. An axis the entry leaves unset is not curated and keeps the
// analysis's answer, whatever that was.
type CuratedEntry struct {
	// Reason is why the curated answer is right, in the reviewer's words. It
	// is required, because a claim that outranks the analysis without a stated
	// argument cannot be re-reviewed.
	Reason string `json:"reason"`
	// Evidence names where the reason was checked, such as a spec clause, an
	// MDN page, or an engine the behavior was observed in.
	Evidence string `json:"evidence,omitempty"`
	// ReviewedAt is the Func.Digest of the algorithm at review time. When the
	// graph's digest no longer matches, the algorithm was rewritten under the
	// entry and curationReport lists it as stale.
	ReviewedAt string `json:"reviewedAt"`

	Classified Coverage     `json:"classified"`
	Receiver   ReceiverKind `json:"receiver,omitempty"`
	Returns    ReturnFact   `json:"returns,omitzero"`
}

// Curation is the whole committed layer.
type Curation struct {
	Entries map[string]CuratedEntry `json:"entries"`
}

// CurationNoteKind says what one curated axis did to the analysis's answer.
type CurationNoteKind string

const (
	// CurationFillIn is a curated axis the analysis left open, so the entry
	// adds a claim where there was none. It is the ordinary case and carries no
	// conflict. An axis reads as open when its coverage is unset, and a return
	// alias reads as open when it resolved to `unknown` too, since the top of
	// the alias lattice names no value the return hands back.
	CurationFillIn CurationNoteKind = "fill-in"
	// CurationCorrection is a curated axis contradicting a claim the analysis
	// published. §6's validation diff reads these first, because a correction
	// is either a spec subtlety the graph cannot express or an analyzer bug,
	// and the two look alike from here.
	CurationCorrection CurationNoteKind = "correction"
	// CurationRedundant is a curated axis repeating what the analysis already
	// concluded. The entry can be deleted, which is how the layer shrinks as
	// the analysis improves rather than accumulating.
	CurationRedundant CurationNoteKind = "redundant"
)

// CurationNote records one curated axis and what it did.
type CurationNote struct {
	Name string
	Axis Axis
	Kind CurationNoteKind
	// Analyzed renders the claim the analysis published, and is empty when it
	// withheld the axis.
	Analyzed string
	// Curated renders the claim the entry supplied.
	Curated string
}

func (n CurationNote) String() string {
	if n.Kind == CurationFillIn {
		return fmt.Sprintf("%s %s: %s %s", n.Name, n.Axis, n.Kind, n.Curated)
	}
	return fmt.Sprintf("%s %s: %s %s over %s", n.Name, n.Axis, n.Kind, n.Curated, n.Analyzed)
}

// CurationReport is what one merge of the layer did, for review rather than for
// the wire. facts.json records the merged determinations and not where each one
// came from, because the converter applies a curated receiver claim and an
// analyzed one identically. Provenance is a reviewer's concern, so it lives
// here.
type CurationReport struct {
	// Notes holds one entry per curated axis, sorted by name and then axis.
	Notes []CurationNote
	// Stale holds the names whose algorithm changed since the entry was
	// reviewed, sorted. A stale entry is still applied. The spec rewriting an
	// algorithm rarely invalidates the reviewed claim, and dropping the entry
	// would trade a visible re-review prompt for a silent loss of coverage.
	Stale []string
	// Unmatched holds the curated names the graph holds no builtin for,
	// sorted. A misspelled key and a key from another spec revision both land
	// here, and TestCurationMatchesTheGraph is what turns the first into a
	// failing build.
	Unmatched []string
	// Refused holds the curated axes the graph contradicts outright, one
	// rendered line each, in the order the entries were read. The axis is not
	// applied. See receiverConflict for the one contradiction there is.
	Refused []string
}

// Counts renders the report's tallies by note kind, sorted, for the one-line
// summary WriteCurationReport prints.
func (r CurationReport) Counts() map[CurationNoteKind]int {
	counts := map[CurationNoteKind]int{}
	for _, note := range r.Notes {
		counts[note.Kind]++
	}
	return counts
}

// mustParseCuration parses the committed layer or panics, in the manner of
// regexp.MustCompile. See the `curated` var for why a failure here is not a
// recoverable condition.
func mustParseCuration(data []byte) *Curation {
	curation, err := ParseCuration(data)
	if err != nil {
		panic("ecma262: committed curated.json is invalid: " + err.Error())
	}
	return curation
}

// ParseCuration decodes the curated layer and rejects an entry that cannot be
// reviewed or applied. A claim with no reason, no reviewed digest, no axis, or
// an axis whose value is not one this package can spell is a defect in the data
// rather than an entry to apply as written.
func ParseCuration(data []byte) (*Curation, error) {
	var curation Curation
	if err := json.Unmarshal(data, &curation); err != nil {
		return nil, fmt.Errorf("decoding curation: %w", err)
	}
	for _, name := range sortedNames(curation.Entries) {
		entry := curation.Entries[name]
		if err := entry.validate(); err != nil {
			return nil, fmt.Errorf("curated entry %s: %w", name, err)
		}
		sortMembers(entry.Returns.Members)
		curation.Entries[name] = entry
	}
	return &curation, nil
}

// sortMembers puts a union's members in the order newReturnFact leaves them,
// by kind and then by position. A curated union is written by hand in whatever
// order reads best, and every comparison against an analyzed fact and every
// rendering assumes the one canonical order.
func sortMembers(members []AliasRef) {
	sort.Slice(members, func(i, j int) bool {
		if members[i].Kind != members[j].Kind {
			return members[i].Kind < members[j].Kind
		}
		return refPosition(members[i]) < refPosition(members[j])
	})
}

// refPosition is the position a member sorts by. Only a parameter carries one,
// and validate has already refused a parameter member without one.
func refPosition(ref AliasRef) int {
	if ref.Index == nil {
		return 0
	}
	return *ref.Index
}

// validate reports why an entry cannot be applied, or nil.
func (e CuratedEntry) validate() error {
	if e.Reason == "" {
		return fmt.Errorf("has no reason")
	}
	if e.ReviewedAt == "" {
		return fmt.Errorf("has no reviewedAt digest")
	}
	if !e.Classified.Receiver && !e.Classified.Returns {
		return fmt.Errorf("claims no determination")
	}
	if e.Classified.Receiver {
		switch e.Receiver {
		case RecvBorrow, RecvMutBorrow, RecvNone:
		default:
			return fmt.Errorf("claims receiver %q, which is not a receiver kind", e.Receiver)
		}
	}
	if !e.Classified.Receiver && e.Receiver != "" {
		return fmt.Errorf("names receiver %q on an axis its coverage leaves unclaimed", e.Receiver)
	}
	if e.Classified.Returns {
		return e.Returns.validate()
	}
	if !e.Returns.IsZero() {
		return fmt.Errorf("names returns %s on an axis its coverage leaves unclaimed", e.Returns)
	}
	return nil
}

// validate reports why a return fact cannot be applied, or nil. It holds the
// same invariants newReturnFact builds: a parameter return names its position,
// a union names its members, and no other kind carries either.
func (r ReturnFact) validate() error {
	switch r.Kind {
	case AliasParam:
		if r.Index == nil {
			return fmt.Errorf("returns a parameter but names no position")
		}
		if *r.Index < 0 {
			return fmt.Errorf("returns the parameter at position %d", *r.Index)
		}
	case AliasUnion:
		if len(r.Members) < 2 {
			return fmt.Errorf("returns a union of %d members", len(r.Members))
		}
		seen := set.NewSet[alias]()
		for _, member := range r.Members {
			value := alias{Kind: member.Kind, Index: refPosition(member)}
			if seen.Contains(value) {
				return fmt.Errorf("returns a union naming %s twice", member)
			}
			seen.Add(value)
			// A union names the values its several returns hand back. Neither
			// lattice point that stands for more than one value is such a
			// value, so neither can be a member.
			if member.Kind == AliasUnion || member.Kind == AliasUnknown {
				return fmt.Errorf("returns a union holding %s", member.Kind)
			}
			if err := (ReturnFact{Kind: member.Kind, Index: member.Index}).validate(); err != nil {
				return fmt.Errorf("union member: %w", err)
			}
		}
	case AliasReceiver, AliasFresh, AliasUnknown:
	default:
		return fmt.Errorf("returns %q, which is not an alias kind", r.Kind)
	}
	if r.Kind != AliasParam && r.Index != nil {
		return fmt.Errorf("returns %s but names a position", r.Kind)
	}
	if r.Kind != AliasUnion && len(r.Members) > 0 {
		return fmt.Errorf("returns %s but names union members", r.Kind)
	}
	return nil
}

// IsZero reports whether the fact carries no claim at all. encoding/json's
// omitzero consults it, which is how a MethodFact with an uncovered return
// omits the field entirely.
func (r ReturnFact) IsZero() bool {
	return r.Kind == "" && r.Index == nil && len(r.Members) == 0
}

// equal reports whether two return facts make the same claim. Members are
// compared in order, which both newReturnFact and ParseCuration leave sorted by
// kind and then position.
func (r ReturnFact) equal(other ReturnFact) bool {
	if r.Kind != other.Kind || len(r.Members) != len(other.Members) {
		return false
	}
	if (r.Index == nil) != (other.Index == nil) {
		return false
	}
	if r.Index != nil && *r.Index != *other.Index {
		return false
	}
	for i, member := range r.Members {
		if !(ReturnFact{Kind: member.Kind, Index: member.Index}).equal(
			ReturnFact{Kind: other.Members[i].Kind, Index: other.Members[i].Index}) {
			return false
		}
	}
	return true
}

// mergeCuration merges the layer over the analyzed facts in place and reports
// what each curated axis did. A name the graph holds no builtin for is reported
// and applied to nothing, so an entry from another spec revision degrades to a
// report line rather than inventing a method.
func mergeCuration(cfg *CFG, curation *Curation, methods map[string]MethodFact) CurationReport {
	var report CurationReport
	for _, name := range sortedNames(curation.Entries) {
		entry := curation.Entries[name]
		fn := cfg.Builtin(name)
		if fn == nil {
			report.Unmatched = append(report.Unmatched, name)
			continue
		}
		if fn.Digest != entry.ReviewedAt {
			report.Stale = append(report.Stale, name)
		}

		fact := methods[name]
		if entry.Classified.Receiver {
			if reason := receiverConflict(fn, entry.Receiver); reason != "" {
				report.Refused = append(report.Refused,
					fmt.Sprintf("%s %s: curated %s, but %s", name, AxisReceiver, entry.Receiver, reason))
			} else {
				report.Notes = append(report.Notes, receiverNote(name, fact, entry))
				fact.Receiver = entry.Receiver
				fact.Classified.Receiver = true
			}
		}
		if entry.Classified.Returns {
			report.Notes = append(report.Notes, returnsNote(name, fact, entry))
			fact.Returns = entry.Returns
			fact.Classified.Returns = true
		}
		methods[name] = fact
	}
	return report
}

// receiverConflict reports why fn cannot take the curated receiver kind, or "".
//
// Whether a builtin has a receiver at all follows from Func.Kind rather than
// from any step, so the graph settles it outright and no review can move it.
// §7 auto-applies the receiver claim, so a curated `borrow` on a static would
// put a `&self` on a declaration that has no self, which is the one curated
// mistake the converter cannot absorb.
func receiverConflict(fn *Func, kind ReceiverKind) string {
	switch {
	case fn.Kind == BuiltinMethod && kind == RecvNone:
		return "a " + string(fn.Kind) + " has a receiver"
	case fn.Kind != BuiltinMethod && kind != RecvNone:
		return "a " + string(fn.Kind) + " has no receiver"
	default:
		return ""
	}
}

// receiverNote reads one curated receiver claim against what the analysis
// concluded for the same method.
func receiverNote(name string, analyzed MethodFact, entry CuratedEntry) CurationNote {
	note := CurationNote{Name: name, Axis: AxisReceiver, Curated: string(entry.Receiver)}
	switch {
	case !analyzed.Classified.Receiver:
		note.Kind = CurationFillIn
	case analyzed.Receiver == entry.Receiver:
		note.Kind = CurationRedundant
		note.Analyzed = string(analyzed.Receiver)
	default:
		note.Kind = CurationCorrection
		note.Analyzed = string(analyzed.Receiver)
	}
	return note
}

// returnsNote reads one curated return alias against what the analysis
// concluded for the same method.
//
// returnAlias is total, so a builtin always carries a covered return. An
// `unknown` one is nonetheless a return the walk could not tie to any value, so
// a curated answer over it adds information rather than contradicting a claim.
// It reads as a fill-in, which keeps the corrections list to the entries where
// the two sources genuinely disagree.
func returnsNote(name string, analyzed MethodFact, entry CuratedEntry) CurationNote {
	note := CurationNote{Name: name, Axis: AxisReturns, Curated: entry.Returns.String()}
	switch {
	case !analyzed.Classified.Returns || analyzed.Returns.Kind == AliasUnknown:
		note.Kind = CurationFillIn
	case analyzed.Returns.equal(entry.Returns):
		note.Kind = CurationRedundant
		note.Analyzed = analyzed.Returns.String()
	default:
		note.Kind = CurationCorrection
		note.Analyzed = analyzed.Returns.String()
	}
	return note
}

// sortedNames returns a curation's keys in order, so a report and a merge both
// read the entries the same way whatever order the map hands them back.
func sortedNames(entries map[string]CuratedEntry) []string {
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// WriteCurationReport prints the merge's tallies and every line that needs a
// reviewer. A fill-in is the ordinary case and is summarized rather than
// listed. A correction, a redundant entry, a stale entry, an unmatched name,
// and a refused axis each name something to act on, so each is printed in full.
func WriteCurationReport(report CurationReport, w io.Writer) error {
	counts := report.Counts()
	_, err := fmt.Fprintf(w, "  curation: %d fill-ins, %d corrections, %d redundant, %d stale, %d unmatched, %d refused\n",
		counts[CurationFillIn], counts[CurationCorrection], counts[CurationRedundant],
		len(report.Stale), len(report.Unmatched), len(report.Refused))
	if err != nil {
		return err
	}
	for _, note := range report.Notes {
		if note.Kind == CurationFillIn {
			continue
		}
		if _, err := fmt.Fprintf(w, "    %s\n", note); err != nil {
			return err
		}
	}
	for _, name := range report.Stale {
		if _, err := fmt.Fprintf(w, "    %s: reviewed against an algorithm the graph no longer holds\n", name); err != nil {
			return err
		}
	}
	for _, name := range report.Unmatched {
		if _, err := fmt.Fprintf(w, "    %s: curated, but the graph holds no such builtin\n", name); err != nil {
			return err
		}
	}
	for _, line := range report.Refused {
		if _, err := fmt.Fprintf(w, "    %s\n", line); err != nil {
			return err
		}
	}
	return nil
}
