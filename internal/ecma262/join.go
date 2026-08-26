package ecma262

import (
	"fmt"
	"io"
	"sort"
)

// Signature is the declared parameter shape of one overload, which is all the
// join needs from the type source. Params counts the declared parameters,
// excluding a receiver. Rest is true when the last declared parameter takes
// the remaining arguments, so every position from Params-1 onward exists.
type Signature struct {
	Params int
	Rest   bool
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

// ForSignature resolves the position-keyed parts of a fact against one
// overload. A spec algorithm maps to a single member even where the type
// source declares an overload set, so the algorithm-level claims apply to
// every signature unchanged. A claim that names a parameter position applies
// only to a signature that declares that position; where it does not, the
// return alias drops to AliasUnknown, since the value handed back is one the
// signature cannot name.
func (f MethodFact) ForSignature(s Signature) MethodFact {
	if f.Returns != AliasParam {
		return f
	}
	if f.ParamIndex != nil && s.Holds(*f.ParamIndex) {
		return f
	}
	f.Returns = AliasUnknown
	f.ParamIndex = nil
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
	PerSignature []MethodFact
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
			match.PerSignature = append(match.PerSignature, fact.ForSignature(sig))
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
	classified := 0
	for _, m := range report.Matched {
		if m.Fact.Classified {
			classified++
		}
	}
	_, err := fmt.Fprintf(w, "  join: %d matched (%d classified), %d declarations without a fact, %d facts without a declaration, %d unkeyed declarations, %d unjoinable facts\n",
		len(report.Matched), classified, len(report.DeclsWithoutFact),
		len(report.FactsWithoutDecl), len(report.UnkeyedDecls), len(report.UnjoinableFacts))
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
