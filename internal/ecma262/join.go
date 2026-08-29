package ecma262

import (
	"fmt"
	"io"
	"sort"
)

// Signature is the declared shape of one overload, which is all the join needs
// from the type source. Params counts the declared parameters, excluding a
// receiver. Rest is true when the last declared parameter takes the remaining
// arguments, so every position from Params-1 onward exists.
type Signature struct {
	Params int
	Rest   bool
	// PrimitiveReturn is true when every value the declared return type can
	// hold is a primitive. A union counts only when all of its members do, so
	// `string | undefined` counts and `string | ArrayBuffer` does not.
	PrimitiveReturn bool
}

// Holds reports whether the signature declares a parameter at the 0-based
// position pos.
func (s Signature) Holds(pos int) bool {
	if pos < 0 {
		return false
	}
	if s.Rest && s.Params > 0 {
		// The last declared parameter takes every remaining argument, so no
		// position past it is missing.
		return true
	}
	return pos < s.Params
}

// SignatureFact is one algorithm's fact resolved against one overload. Fact is
// what the analysis concluded. ReturnOwned is what the declared return type
// settled on top of it.
type SignatureFact struct {
	// Fact is the algorithm's fact with its position-keyed claims resolved
	// against this overload.
	Fact MethodFact
	// ReturnOwned is set when the resolved return is `unknown` and the
	// overload declares a primitive return type. Fact.Returns is left as the
	// analysis published it.
	ReturnOwned bool
}

func (f SignatureFact) String() string {
	if !f.ReturnOwned {
		return f.Fact.String()
	}
	return f.Fact.String() + " settled:owned"
}

// unnamedReturn reports whether the resolved fact hands back a value the join
// cannot name. Two things leave one. The walk read no return it could resolve,
// or ForSignature dropped a return naming a position this overload does not
// declare.
func (f SignatureFact) unnamedReturn() bool {
	return f.Fact.Returns.Kind == AliasUnknown
}

// ForSignature resolves a fact against one overload. A return naming a
// parameter position the signature does not declare drops to AliasUnknown, and
// a union drops whole as soon as one member names such a position. A return
// left `unknown` is then settled as owned where the signature declares a
// primitive return type. Every other claim carries over unchanged.
func (f MethodFact) ForSignature(s Signature) SignatureFact {
	resolved := SignatureFact{Fact: f.resolvePositions(s)}
	resolved.ReturnOwned = s.PrimitiveReturn && resolved.unnamedReturn()
	return resolved
}

// resolvePositions drops a return claim naming a parameter position this
// overload does not declare.
func (f MethodFact) resolvePositions(s Signature) MethodFact {
	refs := f.Returns.refs()
	// A union names its members, so one naming none claims values the fact
	// does not carry and no signature can resolve it. A parameter return with
	// no position is the same shape, and the loop below drops it.
	if f.Returns.Kind == AliasUnion && len(refs) == 0 {
		f.Returns = ReturnFact{Kind: AliasUnknown}
		return f
	}
	for _, ref := range refs {
		if ref.Kind != AliasParam {
			continue
		}
		if ref.Index == nil || !s.Holds(*ref.Index) {
			f.Returns = ReturnFact{Kind: AliasUnknown}
			return f
		}
	}
	return f
}

// Declaration is one member the type source declares. Signatures holds one
// entry per overload, in declaration order, so a fact's position-keyed parts
// resolve per overload while its algorithm-level parts apply to all of them.
type Declaration struct {
	Ref        MemberRef
	Signatures []Signature
}

// Declarations is the member surface one type source declares, split by
// whether a canonical spec key can name it.
type Declarations struct {
	// Keyed holds the members a MemberRef addresses.
	Keyed []Declaration
	// Unkeyed holds the dotted runtime paths that name no member of an
	// owner, so no fact can address them. The functions the global object
	// holds sit here, `parseInt` and `isNaN` among them. Their spec names
	// sit in the report's UnjoinableFacts for the same reason.
	Unkeyed []string
}

// Match is one declaration joined to the fact for its spec algorithm.
type Match struct {
	Decl     Declaration
	SpecName string
	Fact     MethodFact
	// PerSignature is Fact resolved against each of Decl.Signatures, in the
	// same order.
	PerSignature []SignatureFact
}

// ReceiverApplies reports whether the join may write the fact's receiver
// claim onto the declaration. A spec getter or setter carries a fixed
// mutability the converter already sets, so the join leaves it alone.
func (m Match) ReceiverApplies() bool {
	return m.Decl.Ref.Accessor == NotAccessor
}

// JoinReport is one run of the join. Every list but Matched is
// informational. The spec and the TypeScript lib drift independently, so a
// name on one side only is a gap to close rather than an error.
type JoinReport struct {
	// Matched holds every declaration a fact addresses.
	Matched []Match
	// DeclsWithoutFact holds the declarations no fact addresses, sorted.
	DeclsWithoutFact []MemberRef
	// FactsWithoutDecl holds the canonical spec names no declaration
	// addresses, sorted.
	FactsWithoutDecl []string
	// UnjoinableFacts holds the spec names no declaration can ever reach,
	// sorted. Two things land a name here. Normalize refuses it, which
	// covers the bare globals and the closures an algorithm defines. Or the
	// MemberRef it normalizes to is already claimed by another name. Either
	// way the name is counted apart from FactsWithoutDecl, which holds names
	// a declaration merely failed to claim.
	UnjoinableFacts []string
	// UnkeyedDecls holds the declared runtime paths no canonical spec key
	// names, sorted. They are the declaration-side counterpart of the
	// Normalize refusals in UnjoinableFacts.
	UnkeyedDecls []string
	// SettledReturns and UnsettledReturns split the resolved signatures whose
	// return the join cannot name, by whether the declared return type settled
	// it as owned. Both count per signature rather than per declaration.
	SettledReturns   int
	UnsettledReturns int
}

// Join indexes classified facts by the MemberRef that addresses them, so a
// converted member can resolve its fact by name alone. The index carries no
// types, which is what keeps it valid whether the declarations come from a
// `.d.ts` today or from committed `.esc` later.
type Join struct {
	byRef map[MemberRef]keyedFact
	// unjoinable holds the spec names no declaration can reach, sorted.
	unjoinable []string
}

// keyedFact is one indexed fact together with the canonical spec name it was
// keyed from, which the report prints.
type keyedFact struct {
	name string
	fact MethodFact
}

// NewJoin indexes facts by MemberRef. Spec names are indexed in sorted order,
// so when two of them normalize to the same MemberRef the first one wins, the
// second joins the unjoinable list, and the index stays the same across runs.
func NewJoin(facts *Facts) *Join {
	names := make([]string, 0, len(facts.Methods))
	for name := range facts.Methods {
		names = append(names, name)
	}
	sort.Strings(names)

	j := &Join{byRef: make(map[MemberRef]keyedFact, len(names))}
	for _, name := range names {
		ref, ok := Normalize(name)
		if !ok {
			j.unjoinable = append(j.unjoinable, name)
			continue
		}
		if _, taken := j.byRef[ref]; taken {
			j.unjoinable = append(j.unjoinable, name)
			continue
		}
		j.byRef[ref] = keyedFact{name: name, fact: facts.Methods[name]}
	}
	sort.Strings(j.unjoinable)
	return j
}

// Lookup returns the canonical spec name and the fact that address ref.
func (j *Join) Lookup(ref MemberRef) (string, MethodFact, bool) {
	keyed, ok := j.byRef[ref]
	if !ok {
		return "", MethodFact{}, false
	}
	return keyed.name, keyed.fact, true
}

// Match joins decls against the index and reports both sides. Each
// declaration is looked up by its MemberRef, and the accessor tag it carries
// reaches the caller through Match.ReceiverApplies.
//
// A declaration that names an accessor still resolves, because the spec keys
// `get Map.prototype.size` as its own algorithm. Refusing it here would
// report it as a gap rather than as the deliberate carve-out it is.
func (j *Join) Match(decls Declarations) JoinReport {
	var report JoinReport
	claimed := make(map[string]bool, len(decls.Keyed))

	for _, decl := range decls.Keyed {
		name, fact, ok := j.Lookup(decl.Ref)
		if !ok {
			report.DeclsWithoutFact = append(report.DeclsWithoutFact, decl.Ref)
			continue
		}
		claimed[name] = true
		match := Match{Decl: decl, SpecName: name, Fact: fact}
		for _, sig := range decl.Signatures {
			resolved := fact.ForSignature(sig)
			match.PerSignature = append(match.PerSignature, resolved)
			switch {
			case resolved.ReturnOwned:
				report.SettledReturns++
			case resolved.unnamedReturn():
				report.UnsettledReturns++
			}
		}
		report.Matched = append(report.Matched, match)
	}

	// Each name is indexed under at most one MemberRef, so iterating the
	// index's values visits every keyed fact exactly once.
	for _, keyed := range j.byRef {
		if !claimed[keyed.name] {
			report.FactsWithoutDecl = append(report.FactsWithoutDecl, keyed.name)
		}
	}
	report.UnjoinableFacts = append(report.UnjoinableFacts, j.unjoinable...)
	report.UnkeyedDecls = append(report.UnkeyedDecls, decls.Unkeyed...)

	sort.Slice(report.DeclsWithoutFact, func(a, b int) bool {
		return report.DeclsWithoutFact[a].String() < report.DeclsWithoutFact[b].String()
	})
	sort.Strings(report.FactsWithoutDecl)
	sort.Strings(report.UnkeyedDecls)
	return report
}

// WriteJoinReport prints the join's counts and every name it could not match.
// The converter's operator reads it the way ReportPartition's summary is read,
// as a list of gaps rather than a failure.
func WriteJoinReport(report JoinReport, w io.Writer) error {
	// A matched fact does not always carry a receiver claim. The mutation
	// fixpoint withholds one it could not read whole, which is the
	// determination §7 auto-applies, so the count is worth reporting apart
	// from the match count.
	withReceiver := 0
	for _, m := range report.Matched {
		if m.Fact.Receiver != "" {
			withReceiver++
		}
	}
	_, err := fmt.Fprintf(w, "  join: %d matched (%d with a receiver claim), %d declarations without a fact, %d facts without a declaration, %d unkeyed declarations, %d unjoinable facts\n",
		len(report.Matched), withReceiver, len(report.DeclsWithoutFact),
		len(report.FactsWithoutDecl), len(report.UnkeyedDecls), len(report.UnjoinableFacts))
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "  returns: %d settled as owned by the declared type, %d left unknown\n",
		report.SettledReturns, report.UnsettledReturns)
	if err != nil {
		return err
	}
	for _, ref := range report.DeclsWithoutFact {
		if _, err := fmt.Fprintf(w, "    no fact: %s\n", ref); err != nil {
			return err
		}
	}
	for _, name := range report.FactsWithoutDecl {
		if _, err := fmt.Fprintf(w, "    no declaration: %s\n", name); err != nil {
			return err
		}
	}
	for _, path := range report.UnkeyedDecls {
		if _, err := fmt.Fprintf(w, "    unkeyed declaration: %s\n", path); err != nil {
			return err
		}
	}
	for _, name := range report.UnjoinableFacts {
		if _, err := fmt.Fprintf(w, "    unjoinable fact: %s\n", name); err != nil {
			return err
		}
	}
	return nil
}
