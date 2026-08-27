package ecma262

import (
	"fmt"
	"sort"
	"strings"

	"github.com/escalier-lang/escalier/internal/set"
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
	// AliasReceiver is a return that hands back the receiver.
	AliasReceiver AliasKind = "receiver"
	// AliasParam is a return that hands back a declared parameter, identified
	// by its 0-based position.
	AliasParam AliasKind = "param"
	// AliasFresh is a return of a value the algorithm allocated or a primitive
	// it computed. Nothing the caller holds is borrowed.
	AliasFresh AliasKind = "fresh"
	// AliasUnion is a return that aliases different values on different paths.
	// The values it joined are the union's members, which §8.2 reads as the
	// lifetimes the return borrows from.
	AliasUnion AliasKind = "union"
	// AliasUnknown is a return the analysis could not tie to any of the above.
	// It is the top of the lattice, so joining it with anything yields it.
	AliasUnknown AliasKind = "unknown"
)

// alias is one value a return hands back. Index is meaningful only when Kind
// is AliasParam.
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

// aliasSet is what every return in one algorithm aliases, joined. It is the
// lattice returnAlias accumulates over.
//
// members holds the distinct values the returns handed back. The empty set is
// the bottom, the accumulator before any return has been read. A set of one
// member is that member's kind, and a set of more is FR4's union, whose
// members are what §8.2 spells a lifetime union from.
//
// unknown is the top, and joining it with anything yields it. A return the
// walk could not resolve leaves the lifetime unbounded, so no member is worth
// reading beside it. `RegExp` hands back parameter 0 on one path and an
// unresolved value on another, and comes out `unknown` rather than a union
// naming that position.
type aliasSet struct {
	unknown bool
	members set.Set[alias]
}

// aliasOne is the set one return contributes. An unresolved return raises the
// top rather than adding a member, since `unknown` names no value to borrow.
func aliasOne(a alias) aliasSet {
	if a.Kind == AliasUnknown {
		return aliasSet{unknown: true}
	}
	return aliasSet{members: set.FromSlice([]alias{a})}
}

// join is the least upper bound of two sets, which is their union below the
// top. Two returns that agree contribute one member, and two that disagree
// contribute two, including two parameters at different positions. Every
// return contributes whichever branch reaches it, which is what makes the walk
// path-insensitive.
func (s aliasSet) join(other aliasSet) aliasSet {
	if s.unknown || other.unknown {
		return aliasSet{unknown: true}
	}
	return aliasSet{members: s.members.Union(other.members)}
}

// kind names the lattice point the set sits at. The bottom resolves to
// AliasUnknown the same way the top does, since an algorithm whose returns the
// walk never read says nothing about what it hands back.
func (s aliasSet) kind() AliasKind {
	switch members := s.sorted(); {
	case s.unknown || len(members) == 0:
		return AliasUnknown
	case len(members) == 1:
		return members[0].Kind
	default:
		return AliasUnion
	}
}

// sorted returns the members by kind and then by position. Map iteration order
// would otherwise leak into a fact's members and its rendering.
func (s aliasSet) sorted() []alias {
	members := s.members.ToSlice()
	sort.Slice(members, func(i, j int) bool {
		if members[i].Kind != members[j].Kind {
			return members[i].Kind < members[j].Kind
		}
		return members[i].Index < members[j].Index
	})
	return members
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
		// A shallow allocator's result is still a value the algorithm made.
		// What it holds is reachable only through its interior, and the check
		// above refuses that.
		return alias{Kind: AliasFresh}
	default:
		return alias{Kind: AliasUnknown}
	}
}

// returnAlias joins what every return in one algorithm aliases. An algorithm
// with no return the serializer lowered leaves the set empty, which kind reads
// as `unknown`, since the walk learned nothing about what it hands back.
// `String.prototype.localeCompare` is one. ESMeta lowers its argument
// coercions and stops there, because the comparison itself is
// implementation-defined.
func returnAlias(m *OriginMap) aliasSet {
	var acc aliasSet
	for _, node := range m.Func().Nodes {
		ret, ok := node.(*ReturnNode)
		if !ok {
			continue
		}
		acc = acc.join(aliasOne(aliasOf(m.Eval(ret.Value))))
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

// receiverCovered reports whether a warning leaves fn's receiver claim
// standing. Only a method has a receiver a missed step could have written.
func receiverCovered(fn *Func, mutations Mutations) bool {
	if fn.Kind != BuiltinMethod {
		return true
	}
	return !mutations.Unattributable && !mutations.Incomplete
}

// Coverage records which of MethodFact's determinations the analysis resolved.
// A step it could not read withholds only the ones that read it (FR5).
type Coverage struct {
	// Receiver is set for a static or namespace function, which has no receiver
	// to claim, and for a method the mutation fixpoint read whole.
	Receiver bool `json:"receiver"`
	// Returns is set for every builtin, since returnAlias is total. A return
	// hidden in a step the analysis could not read drops out of the join, a
	// loss FR4 accepts because §7 curates the alias rather than applying it.
	Returns bool `json:"returns"`
}

// AliasRef names one value a return hands back, as Appendix B of
// planning/ecma-262/implementation_plan.md serializes it. Index is the 0-based
// position among the declared parameters, which do not include a method's
// receiver, so position 0 on an instance method is its first argument. It is
// set exactly when Kind is AliasParam.
//
// Index is a pointer because position 0 is one a fact really carries, so
// omitting it would spell the first parameter as an absence.
type AliasRef struct {
	Kind  AliasKind `json:"kind"`
	Index *int      `json:"index,omitempty"`
}

func (r AliasRef) String() string {
	if r.Kind != AliasParam {
		return string(r.Kind)
	}
	// A parameter return with no position is a fact nothing in this package
	// builds. It reads as the missing position rather than as position 0,
	// which is the confusion the pointer exists to prevent.
	if r.Index == nil {
		return "param(?)"
	}
	return fmt.Sprintf("param(%d)", *r.Index)
}

// ReturnFact is FR4's return-borrow seed for one builtin, what its return
// value aliases. Kind is the alias lattice's answer over every return in the
// algorithm.
//
// Index and Members carry what the kind alone cannot spell, and at most one of
// them is ever set. A union names its members so §8.2 can spell the lifetime
// union they seed.
type ReturnFact struct {
	Kind AliasKind `json:"kind"`
	// Index is the returned parameter's position, set exactly when Kind is
	// AliasParam. See AliasRef.
	Index *int `json:"index,omitempty"`
	// Members holds the distinct values the returns joined, sorted, set
	// exactly when Kind is AliasUnion. Every member comes from one return's
	// own alias, which aliasOf reads as the receiver, a parameter, or a fresh
	// value. No member is a union, and none is `unknown`, which raises the
	// lattice top over the whole set instead.
	Members []AliasRef `json:"members,omitempty"`
}

// refs returns every value the return hands back. A union names them, and any
// other kind is its own single ref.
func (r ReturnFact) refs() []AliasRef {
	if r.Kind == AliasUnion {
		return r.Members
	}
	return []AliasRef{{Kind: r.Kind, Index: r.Index}}
}

func (r ReturnFact) String() string {
	if r.Kind != AliasUnion {
		return AliasRef{Kind: r.Kind, Index: r.Index}.String()
	}
	// A union with no members is the counterpart of a positionless parameter
	// return, a fact nothing in this package builds.
	if len(r.Members) == 0 {
		return "union(?)"
	}
	names := make([]string, 0, len(r.Members))
	for _, ref := range r.Members {
		names = append(names, ref.String())
	}
	return "union(" + strings.Join(names, ", ") + ")"
}

// newReturnFact publishes a joined alias set. A set of one member spells that
// member's kind and position, and a set of more spells a union that names them
// all.
func newReturnFact(s aliasSet) ReturnFact {
	members := s.sorted()
	switch s.kind() {
	case AliasParam:
		return ReturnFact{Kind: AliasParam, Index: new(members[0].Index)}
	case AliasUnion:
		refs := make([]AliasRef, 0, len(members))
		for _, member := range members {
			ref := AliasRef{Kind: member.Kind}
			if member.Kind == AliasParam {
				ref.Index = new(member.Index)
			}
			refs = append(refs, ref)
		}
		return ReturnFact{Kind: AliasUnion, Members: refs}
	default:
		return ReturnFact{Kind: s.kind()}
	}
}

// MethodFact is what the analysis concluded about one builtin. It serializes
// as one entry of facts.json, described in Appendix B of
// planning/ecma-262/implementation_plan.md.
//
// A determination Coverage leaves unset carries no claim, and its field is
// absent from the JSON, so a consumer never reads "unanalyzed" as "proven
// none". The converter applies requirements.md FR5's default instead, `&mut
// self` for an absent receiver.
//
// Receiver is a plain kind with no empty member, so omitempty encodes its
// absence. Returns is a struct, and omitzero encodes the same absence for it.
//
// Params, Throws, and Rejects join this shape in §8 and §9. Until §8.1 fills
// Params, no fact makes a parameter claim, so an absent Params is not yet
// Appendix B's proven-read-only one.
type MethodFact struct {
	Classified Coverage     `json:"classified"`
	Receiver   ReceiverKind `json:"receiver,omitempty"`
	Returns    ReturnFact   `json:"returns,omitzero"`
}

// String renders the covered determinations in one line, so a test can assert
// a fact whole. An uncovered one is left out, and a fact covering nothing reads
// "unclassified".
func (f MethodFact) String() string {
	var parts []string
	if f.Classified.Receiver {
		parts = append(parts, "receiver:"+string(f.Receiver))
	}
	if f.Classified.Returns {
		parts = append(parts, "returns:"+f.Returns.String())
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
// a method's mutability claim.
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
		fact := MethodFact{
			Classified: Coverage{
				Receiver: receiverCovered(fn, mutations),
				Returns:  true,
			},
			Returns: newReturnFact(returnAlias(summary.originsOf(fn))),
		}
		if fact.Classified.Receiver {
			fact.Receiver = receiverKind(fn, mutations)
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
// measured by. Only a method appears, and it still publishes its return alias,
// so the list marks a withheld determination rather than an empty fact.
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
