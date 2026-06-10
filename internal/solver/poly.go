package solver

import "github.com/escalier-lang/escalier/internal/soltype"

// TypeScheme is a name's generalized type — the M3 replacement for M2's plain
// soltype.Type binding. A MonoScheme is a value that does not generalize (a
// parameter, a current-level RHS during inference, a body-level `val`, a prelude
// operator); a PolyScheme carries a generalize-level so each use can be
// instantiated with fresh variables (let-polymorphism). The IsAnnotated bit is
// forward-looking metadata for PR6's overload-recursion gate — PR1 sets it false
// and never reads it.
type TypeScheme interface {
	isScheme()
	// IsAnnotated reports whether this scheme came from a user-written signature.
	// PR1 always returns false; PR6 sets it per overload arm and folds it over a
	// binding's arms for the mutual-recursion-needs-annotation rule.
	IsAnnotated() bool
}

// MonoScheme is a non-generalized type. instantiate returns its Ty unchanged, so
// every use shares the same variables — the monomorphic discipline M2 had for
// every binding, now scoped to the bindings that genuinely must not generalize.
type MonoScheme struct {
	Ty        soltype.Type
	Annotated bool // PR6 only — consulted solely for overload arms
}

// PolyScheme is a generalized type. Body is the RAW (variable-carrying) type so
// instantiate can freshen it; every variable in Body with Level > Level is a
// quantified type parameter (freshened per use), variables at Level <= Level are
// captured from an enclosing scope (shared across uses). Level is the level the
// binding was generalized at (the SCC component's level).
type PolyScheme struct {
	Level     int
	Body      soltype.Type
	Annotated bool // PR6 only — set per overload arm; folds for the recursion gate
}

func (*MonoScheme) isScheme() {}
func (*PolyScheme) isScheme() {}

func (s *MonoScheme) IsAnnotated() bool { return s.Annotated }
func (s *PolyScheme) IsAnnotated() bool { return s.Annotated }

// monoScheme wraps a raw type as a single-scheme value binding's scheme — the
// common case for the param/prelude/body-level/raw-def bindings PR1 does not
// generalize.
func monoScheme(t soltype.Type) TypeScheme { return &MonoScheme{Ty: t} }

// instantiate produces a usable type from a scheme at level lvl. A MonoScheme
// instantiates to its type unchanged; a PolyScheme freshens every quantified
// variable (Level > scheme.Level) with a fresh variable at lvl, bounds and all,
// so two uses of a polymorphic binding never share inference variables. This is
// the inferIdent value-position hook M2 left as a TODO — M2 returned the binding
// type directly; PR1 routes it through here.
func (c *checker) instantiate(s TypeScheme, lvl int) soltype.Type {
	switch sc := s.(type) {
	case *MonoScheme:
		return sc.Ty
	case *PolyScheme:
		return c.freshenAbove(sc.Level, sc.Body, lvl, map[*soltype.TypeVarType]*soltype.TypeVarType{})
	}
	panic("instantiate: unknown TypeScheme")
}

// freshenAbove copies t, replacing each variable with Level > lim by a fresh
// variable at lvl (its bounds freshened too) and sharing every variable at
// Level <= lim. It is the per-use instantiation copy: the cache maps an original
// variable to its single fresh counterpart so repeated occurrences (and cyclic
// bounds) of one variable stay one variable. The fresh variable is inserted into
// the cache BEFORE its bounds are freshened so a recursive bound that references
// the original resolves to the in-progress copy rather than looping.
//
// PR1 lands this hand-rolled, parallel to coalesce/extrude; PR7 collapses all
// three onto a shared soltype rewriting visitor with no behavior change. Unlike
// those two it ignores polarity (it freshens uniformly, no variance flip).
func (c *checker) freshenAbove(lim int, t soltype.Type, lvl int, cache map[*soltype.TypeVarType]*soltype.TypeVarType) soltype.Type {
	return t.Accept(&freshener{c: c, lim: lim, lvl: lvl, cache: cache}, soltype.Positive)
}

// freshener is the soltype-visitor form of freshenAbove. The structural arms come
// from soltype.Accept; the level prune and the var node are the bespoke content.
// Unlike coalesce/extrude it IGNORES polarity (it freshens uniformly, no variance
// flip) — the shared visitor still flips on func params, harmlessly, because
// EnterType/ExitType never consult pol. The start polarity is therefore arbitrary
// (Positive).
type freshener struct {
	c     *checker
	lim   int
	lvl   int
	cache map[*soltype.TypeVarType]*soltype.TypeVarType
}

func (f *freshener) EnterType(t soltype.Type, _ soltype.Polarity) soltype.EnterResult {
	// Every variable inside t is at or below lim: nothing to freshen, SHARE the node
	// (the original pointer flows through unchanged — Accept's identity preservation
	// gives the same result for the structural arms too). Two consequences worth
	// naming:
	//
	//  1. (Soundness coupling) LevelOf returns a *TypeVarType's own Level only — it
	//     does NOT descend into the var's bounds — so this prune is sound only because
	//     the MLsub level invariant holds (a var's level >= the level of everything in
	//     its bounds, maintained by constrain/extrude). LevelOf DOES recurse into the
	//     structural formers, INCLUDING Union/Intersection (PR6 made an overloaded
	//     value's arm IntersectionType a legal scheme-body/constrain input — see
	//     soltype.LevelOf): without that, a generic arm's Level>lim var would hide under
	//     the level-0 intersection and two instantiations of a let-bound overload would
	//     alias a variable that should have been fresh. The analogous extrude
	//     (constrain.go) rests on the same level invariant.
	//  2. (Identity) The shared subtree's pointer is reused across every instantiation
	//     and the scheme body, so compound nodes are NOT uniquely minted per use. Prov
	//     and Info are pointer-keyed; a shared monomorphic node keeps its one original
	//     entry (which is what lets a concrete callee's blame resolve), but the
	//     "unique pointer per mint" property recordProv's debugProv guard assumes does
	//     not hold for these shared nodes. Anything recording provenance against an
	//     instantiated node must account for the sharing.
	if soltype.LevelOf(t) <= f.lim {
		return soltype.EnterResult{Type: t, SkipChildren: true}
	}
	v, ok := t.(*soltype.TypeVarType)
	if !ok {
		return soltype.EnterResult{} // structural node: let Accept rebuild it
	}
	if nv, ok := f.cache[v]; ok {
		return soltype.EnterResult{Type: nv, SkipChildren: true}
	}
	nv := f.c.freshAt(f.lvl)
	f.cache[v] = nv
	// Mint the FromInstantiation interior edge: the fresh var was copied from v.
	// PR1 only records the edge; the multi-hop renderer that chases it back to an
	// AST leaf is M11.5 (NodeFor still resolves only FromAST today).
	f.c.recordInstantiation(nv, v)
	// nv is freshly minted here, so these whole-slice bound assignments are
	// intentionally NOT journaled by the probe (see Probe's doc): a fresh var is
	// unreachable after a Discard, so it self-rolls-back. This is the one sanctioned
	// non-append bound write — it touches only fresh vars, never a var the probe has
	// recorded. The cache is populated BEFORE the bounds are freshened so a recursive
	// bound referencing v resolves to the in-progress nv rather than looping.
	nv.LowerBounds = f.freshenBounds(v.LowerBounds)
	nv.UpperBounds = f.freshenBounds(v.UpperBounds)
	return soltype.EnterResult{Type: nv, SkipChildren: true}
}

func (f *freshener) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

// freshenAll returns a copy of t with EVERY TypeVarType replaced by a fresh var at
// lvl (its bounds copied), sharing no var with the input. Unlike freshenAbove it
// applies no level prune, so it descends through every former — including the
// coalesced Union/Intersection nodes whose LevelOf is 0 (which freshenAbove's
// LevelOf prune would skip) — and freshens vars wherever they occur.
//
// inferAssign uses it on a binding's coalesced slot type: coalesceScheme RETAINS
// type-parameter vars by pointer, so constraining the RHS against that type would
// mutate the binding's own vars and poison a reassigned polymorphic var for every
// later use. Freshening first makes the constraint mutate throwaway copies instead.
// A var-free input (the common annotated/literal case) is returned unchanged.
func (c *checker) freshenAll(t soltype.Type, lvl int) soltype.Type {
	return t.Accept(&allFreshener{c: c, lvl: lvl, cache: map[*soltype.TypeVarType]*soltype.TypeVarType{}}, soltype.Positive)
}

// allFreshener is the soltype-visitor form of freshenAll: it replaces every var
// (no level prune; that is the only difference from freshener) and lets Accept
// rebuild/descend through the structural and union/intersection nodes. It ignores
// polarity (it freshens uniformly).
type allFreshener struct {
	c     *checker
	lvl   int
	cache map[*soltype.TypeVarType]*soltype.TypeVarType
}

func (f *allFreshener) EnterType(t soltype.Type, _ soltype.Polarity) soltype.EnterResult {
	v, ok := t.(*soltype.TypeVarType)
	if !ok {
		return soltype.EnterResult{} // structural / atom node: let Accept rebuild or descend
	}
	if nv, ok := f.cache[v]; ok {
		return soltype.EnterResult{Type: nv, SkipChildren: true}
	}
	nv := f.c.freshAt(f.lvl)
	f.cache[v] = nv // populate BEFORE bounds so a cyclic bound referencing v resolves to nv
	nv.LowerBounds = f.freshenBounds(v.LowerBounds)
	nv.UpperBounds = f.freshenBounds(v.UpperBounds)
	// SkipChildren is a no-op for a var (Accept treats it as a leaf), but we set it
	// honestly: the var's bounds are a side graph, not tree children, so we freshen
	// them above rather than letting the walk descend.
	return soltype.EnterResult{Type: nv, SkipChildren: true}
}

func (f *allFreshener) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

func (f *allFreshener) freshenBounds(bounds []soltype.Type) []soltype.Type {
	if len(bounds) == 0 {
		return nil
	}
	out := make([]soltype.Type, len(bounds))
	for i, b := range bounds {
		out[i] = b.Accept(f, soltype.Positive)
	}
	return out
}

// freshenBounds freshens a var's bound list, preserving the nil-for-empty shape.
// Freshening ignores polarity (no variance flip), so it walks at a fixed Positive.
func (f *freshener) freshenBounds(bounds []soltype.Type) []soltype.Type {
	if len(bounds) == 0 {
		return nil
	}
	out := make([]soltype.Type, len(bounds))
	for i, b := range bounds {
		out[i] = b.Accept(f, soltype.Positive)
	}
	return out
}

// generalize turns the inferred type of a binding (its binding var) into a
// PolyScheme quantified at lvl: every variable with Level > lvl becomes a type
// parameter, captured outer variables (Level <= lvl) stay monomorphic. Body is
// kept RAW for instantiation — coalescing for display happens later (schemeType /
// renderScheme), never here.
//
// Simplification (single-polarity elimination + co-occurrence merging, PR2) is not
// applied to the raw body: it runs at DISPLAY time inside coalesceScheme, so the
// body keeps every variable for instantiation while the rendered signature stays
// compact. See simplify.go.
func (c *checker) generalize(t soltype.Type, lvl int) TypeScheme {
	return &PolyScheme{Level: lvl, Body: t}
}
