package ecma262

import (
	"fmt"
	"sort"
	"strings"
)

// ReceiverKind is how a builtin takes the value it is called on. See
// planning/ecma-262/implementation_plan.md §4.3 and Appendix B.
type ReceiverKind string

const (
	// RecvBorrow is `&self`. The method reads its receiver and writes nothing
	// the caller can observe.
	RecvBorrow ReceiverKind = "borrow"
	// RecvMutBorrow is `&mut self`. The method mutates its receiver.
	RecvMutBorrow ReceiverKind = "mutBorrow"
	// RecvNone is no receiver at all. A class static and a namespace function
	// take their first real argument at parameter 0.
	RecvNone ReceiverKind = "none"
)

// AliasKind is what a builtin's return value aliases, which requirements.md
// FR4 reads as the lifetime the return borrows from.
type AliasKind string

const (
	// aliasUnset is the lattice bottom, the accumulator before any return has
	// been read. returnAlias resolves it to AliasUnknown.
	aliasUnset AliasKind = ""
	// AliasReceiver is a return that hands back the receiver.
	AliasReceiver AliasKind = "receiver"
	// AliasParam is a return that hands back a declared parameter, identified
	// by its 0-based position.
	AliasParam AliasKind = "param"
	// AliasFresh is a return of a value the algorithm allocated or a primitive
	// it computed. Nothing the caller holds is borrowed.
	AliasFresh AliasKind = "fresh"
	// AliasUnion is a return that aliases different values on different paths.
	AliasUnion AliasKind = "union"
	// AliasUnknown is a return the analysis could not tie to any of the above.
	// It is the top of the lattice, so joining it with anything yields it.
	AliasUnknown AliasKind = "unknown"
)

// alias is what one return, or the join of every return in an algorithm,
// aliases. Index is meaningful only when Kind is AliasParam.
type alias struct {
	Kind  AliasKind
	Index int
}

func (a alias) String() string {
	if a.Kind == AliasParam {
		return fmt.Sprintf("param(%d)", a.Index)
	}
	return string(a.Kind)
}

// join is the least upper bound of two aliases. Two returns that agree keep
// their alias, and two that disagree collapse to a union, including two
// parameters at different positions. Anything joined with `unknown` is unknown.
// Every return contributes whichever branch reaches it, which is what makes the
// walk path-insensitive.
func (a alias) join(other alias) alias {
	switch {
	case a.Kind == aliasUnset:
		return other
	case other.Kind == aliasUnset:
		return a
	case a == other:
		return a
	case a.Kind == AliasUnknown || other.Kind == AliasUnknown:
		return alias{Kind: AliasUnknown}
	default:
		return alias{Kind: AliasUnion}
	}
}

// aliasOf reads a returned value's origin as what the return aliases.
//
// An interior origin is a value read out of an object's backing store, not the
// object itself, so it cannot stand in for its holder where identity matters.
// `get DataView.prototype.buffer` returns `view.[[ViewedArrayBuffer]]`, which
// is not the view, so it is `unknown` rather than a borrow of the view.
func aliasOf(o Origin) alias {
	if o.Interior {
		return alias{Kind: AliasUnknown}
	}
	switch o.Kind {
	case OriginReceiver:
		return alias{Kind: AliasReceiver}
	case OriginParam:
		return alias{Kind: AliasParam, Index: o.Index}
	case OriginFresh:
		// A capturing allocator's result is still a value the algorithm made.
		// What it captured is reachable only through its interior, and the
		// check above refuses that.
		return alias{Kind: AliasFresh}
	default:
		return alias{Kind: AliasUnknown}
	}
}

// returnAlias joins what every return in one algorithm aliases. An algorithm
// with no return the serializer lowered is `unknown`, since the walk learned
// nothing about what it hands back. `String.prototype.localeCompare` is one.
// ESMeta lowers its argument coercions and stops there, because the comparison
// itself is implementation-defined.
func returnAlias(m *OriginMap) alias {
	var acc alias
	for _, node := range m.Func().Nodes {
		ret, ok := node.(*ReturnNode)
		if !ok {
			continue
		}
		acc = acc.join(aliasOf(m.Eval(ret.Value)))
	}
	if acc.Kind == aliasUnset {
		return alias{Kind: AliasUnknown}
	}
	return acc
}

// receiverKind reports how a builtin takes its receiver. A static and a
// namespace function have none. A method's receiver is mutable exactly when
// the mutation summary charged a write to it.
func receiverKind(fn *Func, mutations Mutations) ReceiverKind {
	if fn.Kind != BuiltinMethod {
		return RecvNone
	}
	if mutations.Receiver {
		return RecvMutBorrow
	}
	return RecvBorrow
}

// Coverage records which of MethodFact's determinations the analysis resolved.
// A determination is an independent axis of the fact, and requirements.md FR5's
// conservative fallback applies to each one on its own. A step the analysis
// could not read withholds only the determinations that read that step, so a
// missed write costs the receiver claim and leaves the return alias standing.
//
// Params, Throws, and Rejects join MethodFact in §8 and §9, and each adds its
// own field here.
type Coverage struct {
	// Receiver is set when the mutation fixpoint read the whole algorithm and
	// placed every write it saw. An opaque node carries no operands, so a
	// missed step could have written anything, and the receiver claim is
	// withheld rather than guessed.
	Receiver bool `json:"receiver"`
	// Returns is set whenever returnAlias ran, which is for every builtin.
	// The alias lattice has a top, so an algorithm the walk could not tie to
	// any origin resolves to AliasUnknown instead of leaving the axis open.
	//
	// A step the analysis could not read can itself be a return, and the join
	// then misses that path. `String.prototype.repeat` ends in a prose step
	// that returns n copies of the string appended together, so the `fresh`
	// it publishes comes from its empty-string path alone. FR4 accepts that,
	// because §7 records the alias for curation rather than applying it.
	Returns bool `json:"returns"`
}

// MethodFact is what the analysis concluded about one builtin. It serializes
// as one entry of facts.json, described in Appendix B of
// planning/ecma-262/implementation_plan.md.
//
// A determination Coverage leaves unset carries no claim. Its field is absent
// from the JSON, so a consumer never reads "unanalyzed" as "proven none". The
// converter applies requirements.md FR5's default for that axis instead, which
// for an absent receiver is `&mut self`.
//
// Appendix B draws that absence with pointers. Receiver and Returns are plain
// kinds, which have no empty member, so omitempty encodes their absence the
// same way. ParamIndex needs the pointer, because its zero value is a position
// a fact can really carry.
//
// Params, Throws, and Rejects join this shape in §8 and §9. Until §8.1 fills
// Params, no fact makes a parameter claim, so an absent Params is not yet
// Appendix B's proven-read-only one.
type MethodFact struct {
	Classified Coverage     `json:"classified"`
	Receiver   ReceiverKind `json:"receiver,omitempty"`
	Returns    AliasKind    `json:"returns,omitempty"`
	// ParamIndex is the 0-based position Returns aliases. It indexes the
	// declared parameters, which do not include a method's receiver, so
	// position 0 on an instance method is its first argument. It is set
	// exactly when Returns is AliasParam, and position 0 is written out like
	// any other, since omitting it would spell the first parameter as an
	// absence.
	ParamIndex *int `json:"paramIndex,omitempty"`
}

// String renders the covered determinations in one line, so a test can assert
// a fact whole. An uncovered determination is left out rather than spelled with
// a placeholder value, and a fact covering nothing reads "unclassified".
func (f MethodFact) String() string {
	var parts []string
	if f.Classified.Receiver {
		parts = append(parts, "receiver:"+string(f.Receiver))
	}
	if f.Classified.Returns {
		returns := string(f.Returns)
		if f.Returns == AliasParam {
			// A parameter return with no position is a fact nothing in this
			// package builds. It reads as the missing position rather than as
			// position 0, which is the confusion the pointer exists to prevent.
			returns = "param(?)"
			if f.ParamIndex != nil {
				returns = fmt.Sprintf("param(%d)", *f.ParamIndex)
			}
		}
		parts = append(parts, "returns:"+returns)
	}
	if len(parts) == 0 {
		return "unclassified"
	}
	return strings.Join(parts, " ")
}

// Facts is the classification of every builtin in one graph, keyed by the
// canonical spec names of Appendix C in
// planning/ecma-262/implementation_plan.md. An abstract operation feeds the
// analysis but is not a library surface, so it is absent here.
type Facts struct {
	SpecTarget string                `json:"specTarget"`
	Methods    map[string]MethodFact `json:"methods"`
}

// NewFacts classifies every builtin in cfg. It runs the mutation fixpoint
// itself, which supplies the receiver axis and the two warnings that withhold
// it.
//
// The two axes carry different risk, which is why a warning takes only one of
// them. Receiver mutability is a soundness claim §7 auto-applies, and a wrong
// `borrow` lets an immutable value call a mutating method. The return alias is
// FR4's lifetime seed, which §7 records for curation rather than applies, so
// withholding it costs precision and buys no safety.
//
// A published receiver claim is only as strong as §4.1. A mutation the analysis
// does not see leaves no warning, so the borrow is published rather than
// withheld. §6's diff against the hand-written overrides is what authorizes §7
// to trust this source.
func NewFacts(cfg *CFG) *Facts {
	summary := NewMutationSummary(cfg)

	facts := &Facts{
		SpecTarget: cfg.SpecTarget,
		Methods:    make(map[string]MethodFact, len(cfg.Funcs)),
	}
	for _, fn := range cfg.Funcs {
		if fn.Kind != BuiltinMethod && fn.Kind != BuiltinStatic {
			continue
		}
		mutations := summary.Of(fn)
		returns := returnAlias(summary.originsOf(fn))
		fact := MethodFact{
			Classified: Coverage{
				Receiver: !mutations.Unattributable && !mutations.Incomplete,
				Returns:  true,
			},
			Returns: returns.Kind,
		}
		if fact.Classified.Receiver {
			fact.Receiver = receiverKind(fn, mutations)
		}
		if returns.Kind == AliasParam {
			fact.ParamIndex = new(returns.Index)
		}
		facts.Methods[fn.Name] = fact
	}
	return facts
}

// Of returns the fact for a canonical spec name, and whether the graph held
// that builtin at all. §5 reports a name the graph does not hold as unmatched,
// which is not the same as one the analysis left unclassified.
func (f *Facts) Of(name string) (MethodFact, bool) {
	fact, ok := f.Methods[name]
	return fact, ok
}

// Unclassified returns the names whose receiver claim FR5 hands to the
// converter's name-based heuristics, sorted. Shrinking this list is what §4 is
// measured by. A method listed here still publishes its return alias, so the
// list names a withheld determination rather than an empty fact.
func (f *Facts) Unclassified() []string {
	names := make([]string, 0, len(f.Methods))
	for name, fact := range f.Methods {
		if !fact.Classified.Receiver {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}
