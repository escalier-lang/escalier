package solver

import (
	"fmt"
	"slices"
	"strings"

	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// Context owns the engine's mutable counters. M1 carries ONLY varCounter; M4 D1
// adds lifetimeCounter for the lifetime sort. M4 C3's read-after-write cache,
// the `written` map, lives on the checker's per-function context, not here.
//
// M3 (PR5) adds the nullable probe pointer. The engine's bound-mutating core
// (constrain/extrude) lives on *Context, so the speculation journal must live
// here too: when probe is non-nil, every bound append snapshots the variable's
// bound-list lengths so a discarded trial can truncate them back. nil is the
// non-speculative path — the common case — and pays only a nil check per append.
// The push/pop discipline (openProbe/closeProbe) lives on the checker carrier,
// which also owns the side-table cleanups (Info/Prov).
//
// Bound lists are extended ONLY through addLowerBound/addUpperBound, which fuse
// the probe snapshot with the append. A bare `v.LowerBounds = append(...)` must
// never appear — routing every append through the two helpers is what makes
// "forgot to journal the mutation" structurally impossible (an un-journaled
// append would silently survive a Discard and corrupt committed state). The one
// in-place write to a bound list is replaceLowerBound, which journals its own undo.
type Context struct {
	varCounter int

	// paramVars holds the variable of every type parameter a declaration writes, and every
	// copy that instantiation or extrusion makes of one. A call instantiates a binder by
	// substituting fresh variables for its parameters, so a copy only stays reachable through
	// the bounds a body recorded on the parameter. resolveTypeParams adds each parameter's
	// variable, and the copiers add each copy through copyParamMark. isParamVar reads it.
	paramVars set.Set[*soltype.TypeVarType]

	// symbolCounter mints the id a `unique symbol` annotation carries. Each written
	// annotation is its own symbol, so the counter advances per annotation rather than
	// per name, and two references to one declaration share an id because they share the
	// resolved type rather than because they re-resolve to the same number.
	symbolCounter int
	probe         *Probe

	// readRaises maps a field-read requirement to the throws types its read site already
	// raised, one per raising getter it found on the receiver. registerReadRaises fills it
	// and readRaised reads it.
	readRaises map[*soltype.ObjectType][]soltype.Type

	// pendingRigidLts holds one entry per super signature whose lifetime binder a subtype
	// check instantiated, waiting for checkRigidLifetimes to read what the sub required
	// of the fresh lifetimes once the enclosing component has solved. skolemizeFuncBinder
	// appends, and an entry is rolled back with the probe that recorded it.
	pendingRigidLts []*rigidLtCheck

	// packages holds the surface of every package this run has loaded. A run
	// shares one registry, so two modules importing the same package read the
	// same declarations. It is nil on a Context built outside newChecker.
	packages *PackageRegistry

	// wellKnownSymbolMembers maps the id of each well-known symbol the prelude declares,
	// such as the type of `Symbol.iterator`, to the reserved member name the written key
	// `[Symbol.iterator]` names. wellKnownSymbolTypes maps the other way, from that member
	// name to the symbol. resolveWellKnownSymbols fills both once per run.
	wellKnownSymbolMembers map[int]string
	wellKnownSymbolTypes   map[string]*soltype.UniqueSymbolType

	// arrayClass is the qualified class name the prelude's `Array` binds to, read
	// off the prelude scope once per run. The subtyping and iteration rules that single an
	// array out compare against it, so they cost a string comparison rather than a
	// package load and answer the same inside a speculation trial as outside one.
	// It is empty when the run resolved no `Array`, in which case no type in play is
	// one and every such rule declines.
	arrayClass string

	// promiseClass is the qualified class name the prelude's `Promise` binds to, read
	// off the prelude scope once per run. The await rule and the async return wrap
	// build an instance of it, and constrain checks one against another through the
	// ordinary nominal rule. It is empty when the run resolved no `Promise`.
	promiseClass string

	// lifetimeCounter mints the next LifetimeVar id (M4 D1). Lifetimes are a
	// SECOND bounded sort solved by the same machinery as types: a fresh lifetime
	// gets the next id here, its bounds are extended only through
	// addLowerLtBound/addUpperLtBound, and a speculation trial journals it under
	// the same probe discipline as a TypeVarType.
	lifetimeCounter int

	// ltProxyOrigin maps an outer-extruded lifetime proxy to the lifetime it was
	// extruded from (M4 D2.5). constrainLt consults it through findLtProxy to reuse
	// an existing proxy for a repeated cross-level outlives constraint, so the bound
	// dedup is not defeated by minting a fresh proxy each time. It is metadata only
	// and is never rolled back: a stale entry for a proxy that a discarded trial
	// removed from its bound list is simply never matched, since findLtProxy scans
	// only bounds currently present.
	ltProxyOrigin map[*soltype.LifetimeVar]soltype.Lifetime

	// methodLifetimes holds every lifetime variable a frozen class method's signature
	// quantifies on its own: the lifetimes it declares in its `<…>` list and the ones its
	// body inferred for its borrows. A lifetime the class declares is not in it, since it
	// belongs to the instance. A member access freshens these per access, so two calls to
	// one method do not share them. recordMethodLifetimes fills it when a class body freezes.
	methodLifetimes set.Set[*soltype.LifetimeVar]

	// classes is the nominal registry (M5): each class's heavy data — the projected
	// instance body, the resolved supers, and the per-parameter variance — keyed by
	// the class's dep_graph-qualified name, the same string stored in
	// soltype.ClassType.Name. inferClassDecl writes an entry per class decl; member
	// lookup and the nominal constrain rule read it. Every ClassDef comes from a
	// top-level decl and lives for the whole inference run, so an entry is inserted or
	// overwritten but never removed for scope exit.
	classes map[string]*ClassDef

	// measuredClasses counts the classes whose variance measureVariance has made final. A
	// provisional class records the count it was measured at and measures again only once
	// the count moves.
	measuredClasses int

	// aliases is the type-alias registry, the transparent-alias twin of classes. It holds
	// each alias's Body and level, keyed by the alias's dep_graph-qualified name, the same
	// string stored in soltype.AliasType.Name. preBindAlias writes an entry per `type` decl,
	// and expandAlias reads the Body to unfold an alias reference to its structural type at
	// subtyping time. Every AliasDef comes from a top-level decl and lives for the whole
	// inference run.
	aliases map[string]*AliasDef

	// aliasInterns maps an alias reference's canonical identity — its name and the rendered
	// form of its type arguments, e.g. "List<number>" — to one representative AliasType, so
	// two structurally-equal reference nodes share a pointer. constrain's seen-set keys by
	// pointer identity, and expandAlias mints a fresh substituted node each unfold, so a
	// generic recursive alias such as List<number> would produce a new node every lap and
	// never hit the cache. Interning gives the cache a stable canonical key so the cycle
	// closes. The map lives for the whole run and is never rolled back on a discarded probe.
	// A representative is only ever compared by identity for the seen-set and never expanded,
	// so a stale entry cannot make a constraint wrong.
	aliasInterns map[string]*soltype.AliasType

	// uniformKnots memoizes, per alias name, the μ-knot the regular-tree check proves for the
	// instantiations one level into that alias's recursion, or a zero entry when it proves none.
	// Nearly every alias memoizes the zero entry, so a reference pays one map lookup and nothing
	// more. The map lives for the whole run: the check reads only an alias's registered definition,
	// which no constraint and no discarded probe changes. See regular.go.
	uniformKnots map[string]*uniformKnot

	// unfoldingAliases counts, per alias name, how many constraints on the current path are running
	// on that alias's expansion. evalTypeOperator reads it to hand constrain an alias's μ-knot only
	// on the second unfolding, where a cycle can be, rather than on the first. Entries are added and
	// removed by constrainUnfoldingAlias around the constraint each unfolding produces, so the map
	// describes the current path rather than the whole run.
	unfoldingAliases map[string]int

	// knotting is true while a level of some alias's unfolding is being walked for the regular-tree
	// check. That walk reduces the level's residual operators, and a reduction can re-enter
	// constrain, which would ask for a knot again and start a second walk inside the first. The flag
	// makes the inner ask fall back to the plain expansion instead. See regular.go.
	knotting bool

	// buildingAliases holds the qualified names of the aliases and interfaces whose bodies
	// inferComponent is resolving. Until the window closes, such an alias may still have a nil
	// Body, which expandAlias reads as an absorbing ErrorType, and its productivity and phantom
	// marks are not set. subsumeMembers leaves out of every comparison a member that names one.
	// It is nil outside that window.
	buildingAliases set.Set[string]

	// borrowedReads holds, per type variable, the field reads made through a borrow whose
	// pointee is that variable. Each lower bound the variable gains is read under every
	// entry's borrow. addBorrowedRead appends to it and constrainBorrowedVarFieldRead
	// explains the rule.
	borrowedReads map[*soltype.TypeVarType][]borrowedRead

	// slotModes holds, per type variable, the writable slot of a weak binding the variable
	// stands for. Each lower bound the variable gains is widened as that slot widens it.
	// markSlot fills it for a weak binding's own variables, and widenSlotBound for the
	// variables it mints. widenSlotBound explains the rule.
	slotModes map[*soltype.TypeVarType]slotMode

	// muBinderCount numbers the μ-variables the regular-tree check has minted, so two aliases whose
	// knots are composed into one type carry distinct binders. coalesce numbers its own binders per
	// walk instead, since the knots one walk produces are numbered together.
	muBinderCount int

	// unwrapDepth counts the type operators constrain has evaluated along the constraint path it
	// is currently on, so an unwrap that never bottoms out is cut off at maxUnwrapDepth. constrain
	// increments it around each recursive call it makes on an evaluated operator and restores it
	// when that call returns, so the value always names the current path rather than the whole run.
	unwrapDepth int

	// shallowestAssumed is the depth of the shallowest goal that the running constraint derivation
	// has closed a coinductive assumption on. It holds math.MaxInt when the derivation has closed
	// on none. Each constrain frame takes the field over for its own derivation, then folds the
	// result back into the enclosing frame's value on the way out, so what a frame reads covers
	// its own subtree. Comparing that against the frame's own depth is what decides whether the
	// frame's verdict may be memoized — see seenPairs. Outside any derivation the field holds
	// whatever the last one left, which nothing reads.
	//
	// A branch whose failure the caller discards must not inform that caller's comparison. Every
	// arm that rejects a branch therefore restores the field alongside rolling the branch's bounds
	// back. Those arms are the losing trials in trialAndCommit, the throwaway-probe helpers beside
	// it, and constrainNominalWalk's rejected superclass candidates. An accepted branch keeps its
	// contribution, since the caller keeps that derivation. Folding a rejected branch in could
	// only ever suppress a memo entry the caller had earned, never admit one it had not, so the
	// restore buys precision rather than soundness.
	shallowestAssumed int

	// unionCommits maps an inference var that a union-super trial pinned by committing a bare
	// type-variable member to the super union it was chosen from, so `"hi" <: (T | number)`
	// records T → (T | number). A later constraint that forces an incompatible bound onto the
	// var reads this to breadcrumb the failure back to that union choice.
	unionCommits map[*soltype.TypeVarType]*soltype.UnionType

	// fusionRecorder records the interior provenance edge a fusion mints: fused was
	// produced by merging the atoms in from. The engine's fusion sites live on
	// *Context, while the Prov table lives on the checker carrier, so the carrier
	// installs this hook in newChecker to bridge the two. It is nil on a Context that
	// carries no such table, where a fusion records nothing. See recordFusionEdge for
	// what an installed recorder writes.
	fusionRecorder func(fused soltype.Type, from []soltype.Type)

	// holeVars holds the variable each `_` written outside a conditional's pattern
	// resolved to. markHole fills it and freshenBoundHoles reads it.
	holeVars set.Set[*soltype.TypeVarType]

	// frozenVars and frozenLts hold the type and lifetime variables reachable from a
	// cached stdlib checker, when frozenVarChecks is set. loadAmbientChecker fills
	// them, and every fork of the cached checker shares them along with the variables
	// they hold. They are empty otherwise. checkThawed and checkThawedLt read them.
	frozenVars set.Set[*soltype.TypeVarType]
	frozenLts  set.Set[*soltype.LifetimeVar]
	// frozenClasses and frozenAliases hold the class and alias definitions registered on
	// a cached stdlib checker, the same way. checkThawedClass and checkThawedAlias read
	// them.
	frozenClasses set.Set[*ClassDef]
	frozenAliases set.Set[*AliasDef]
}

// markHole records v as the variable a `_` resolved to.
func (c *Context) markHole(v *soltype.TypeVarType) {
	if c.holeVars == nil {
		c.holeVars = set.NewSet[*soltype.TypeVarType]()
	}
	c.holeVars.Add(v)
}

// frozenVarChecks makes loadAmbientChecker record the variables the stdlib load
// minted, so a run forked from it panics when it writes to one. The solver's tests
// set it, and other packages' tests set it through EnableFrozenStdlibChecks.
var frozenVarChecks bool

// EnableFrozenStdlibChecks makes every run forked from a cached stdlib checker panic
// when it writes to state the stdlib load created. It covers only checkers cached
// after the call, so call it before the first inference run. It is for tests and is not safe to call concurrently with one.
func EnableFrozenStdlibChecks() {
	frozenVarChecks = true
}

// checkThawed panics when v is a variable the cached stdlib load minted. site names
// the write, for the panic message.
func (c *Context) checkThawed(v *soltype.TypeVarType, site string) {
	if c.frozenVars.Contains(v) {
		panic(fmt.Sprintf("%s wrote stdlib type variable %d", site, v.ID))
	}
}

// checkThawedClass is checkThawed for a class definition.
func (c *Context) checkThawedClass(def *ClassDef, site string) {
	if c.frozenClasses.Contains(def) {
		panic(fmt.Sprintf("%s wrote stdlib class %s", site, def.varianceSelf))
	}
}

// checkThawedAlias panics when def is an alias definition registered on the cached stdlib
// checker. name is the alias's registered name, and site names the work about to be done
// for it, for the panic message.
func (c *Context) checkThawedAlias(def *AliasDef, name, site string) {
	if c.frozenAliases.Contains(def) {
		panic(fmt.Sprintf("%s for stdlib alias %s", site, name))
	}
}

// checkThawedLt is checkThawed for a lifetime variable.
func (c *Context) checkThawedLt(v *soltype.LifetimeVar, site string) {
	if c.frozenLts.Contains(v) {
		panic(fmt.Sprintf("%s wrote stdlib lifetime variable %d", site, v.ID))
	}
}

// recordFusion records that fused came from merging the atoms in from, when a carrier
// installed a recorder. A fusion helper in normal.go or classes.go calls it at the
// fresh node it allocates, so the interior FromNormalization edge is minted at the
// one place that knows the two source atoms. A no-op when no recorder is installed,
// which is every Context built outside newChecker, such as the bare `&Context{}` the
// engine-level tests use.
func (c *Context) recordFusion(fused soltype.Type, from ...soltype.Type) {
	if c.fusionRecorder != nil {
		c.fusionRecorder(fused, from)
	}
}

// tagUnionCommit records that committing union u pinned v, so a later failure on v can name
// u. The prior entry is restored on rollback, so a discarded trial drops the tag with its bound.
func (c *Context) tagUnionCommit(v *soltype.TypeVarType, u *soltype.UnionType) {
	if c.unionCommits == nil {
		c.unionCommits = map[*soltype.TypeVarType]*soltype.UnionType{}
	}
	if c.probe != nil {
		prev, had := c.unionCommits[v]
		c.probe.onRollback(func() {
			if had {
				c.unionCommits[v] = prev
			} else {
				delete(c.unionCommits, v)
			}
		})
	}
	c.unionCommits[v] = u
}

// internAlias returns the shared representative for an alias reference's canonical
// identity, minting one on first sight. Two AliasType nodes naming the same alias with
// type arguments that render identically map to one pointer. That pointer is the canonical
// identity formed from the alias and its arguments, the identity constrain's cycle guard
// keys on. A non-generic reference keys on its name alone.
func (c *Context) internAlias(at *soltype.AliasType) *soltype.AliasType {
	// PrintQualified renders the reference under qualified names for every nested alias and
	// class, so two aliases sharing a local name across namespaces never collide on one key,
	// and a nested recursive alias such as List<List<number>> serializes finitely because an
	// argument renders under its own name without expanding.
	//
	// An argument a phantom parameter receives is erased first, since no instantiation's type
	// depends on it. `type Deep<T> = {a: Deep<{b: T}>}` denotes `{a: {a: …}}` whatever T is, so
	// Deep<number> and Deep<string> both key on "Deep" and share a representative.
	k := soltype.PrintQualified(c.erasePhantomArgs(at))
	if c.aliasInterns == nil {
		c.aliasInterns = map[string]*soltype.AliasType{}
	}
	if rep, ok := c.aliasInterns[k]; ok {
		return rep
	}
	c.aliasInterns[k] = at
	return at
}

// aliasDef returns the registered AliasDef for a qualified alias name, or ok=false
// when no alias of that name has been registered on this Context.
func (c *Context) aliasDef(name string) (*AliasDef, bool) {
	def, ok := c.aliases[name]
	return def, ok
}

// notProductive reports whether t is a reference to an alias checkProductive rejected. Both the
// evaluator and constrain consult it to leave such a reference alone, since the alias names no type
// to unfold toward and its diagnostic is already reported.
func (c *Context) notProductive(t soltype.Type) bool {
	ref, ok := t.(*soltype.AliasType)
	if !ok {
		return false
	}
	def, registered := c.aliasDef(ref.Name)
	return registered && def.NotProductive
}

// registerAlias inserts def under a qualified alias name, allocating the registry
// map on first use.
func (c *Context) registerAlias(name string, def *AliasDef) {
	if c.aliases == nil {
		c.aliases = map[string]*AliasDef{}
	}
	c.aliases[name] = def
}

// classDef returns the registered ClassDef for a qualified class name, or ok=false
// when no class of that name has been registered on this Context.
func (c *Context) classDef(name string) (*ClassDef, bool) {
	def, ok := c.classes[name]
	return def, ok
}

// settledClassDef returns the registered ClassDef for a qualified class name once its
// variance is final, settling a provisional one first. It returns ok=false when no such
// class is registered, its body is still being inferred, or its variance stays
// provisional.
func (c *Context) settledClassDef(name string) (*ClassDef, bool) {
	def, ok := c.classes[name]
	if !ok || !def.varianceMeasured {
		return nil, false
	}
	c.settleVariance(def)
	if def.varianceProvisional {
		return nil, false
	}
	return def, true
}

// registerClass inserts def under a qualified class name, allocating the registry
// map on first use.
func (c *Context) registerClass(name string, def *ClassDef) {
	if c.classes == nil {
		c.classes = map[string]*ClassDef{}
	}
	c.classes[name] = def
}

// forgetKeyPrefix drops every registration whose qualified name starts with prefix,
// along with the two caches keyed off those names.
//
// A run's registries are keyed by a name carrying the package or script a declaration
// came from, so checking one script twice against the same run leaves the first
// check's definitions in place for the second to find. See the call in forScript,
// which is where that happens.
func (c *Context) forgetKeyPrefix(prefix string) {
	for name := range c.classes {
		if strings.HasPrefix(name, prefix) {
			delete(c.classes, name)
			delete(c.uniformKnots, name)
		}
	}
	for name := range c.aliases {
		if strings.HasPrefix(name, prefix) {
			delete(c.aliases, name)
			delete(c.uniformKnots, name)
		}
	}
	// An interned alias reference keys on its rendered form, which spells the alias
	// and each of its arguments under a qualified name, so the prefix can appear
	// anywhere in the key. A representative is only compared by identity, so dropping
	// more entries than this script registered costs a re-intern and nothing else.
	for key := range c.aliasInterns {
		if strings.Contains(key, prefix) {
			delete(c.aliasInterns, key)
		}
	}
}

// freshVar allocates a new inference variable at the given level, assigning it
// the next id in sequence.
func (c *Context) freshVar(level int) *soltype.TypeVarType {
	v := &soltype.TypeVarType{ID: c.varCounter, Level: level}
	c.varCounter++
	return v
}

// markParamVar records v in paramVars, as a type parameter's variable or a copy of one.
func (c *Context) markParamVar(v *soltype.TypeVarType) {
	if c.paramVars == nil {
		c.paramVars = set.NewSet[*soltype.TypeVarType]()
	}
	c.paramVars.Add(v)
}

// isParamVar reports whether v is a type parameter's variable or a copy of one.
func (c *Context) isParamVar(v *soltype.TypeVarType) bool {
	return c.paramVars.Contains(v)
}

// copyParamMark marks the copy nv when the variable it was copied from is in paramVars.
func (c *Context) copyParamMark(nv, from *soltype.TypeVarType) {
	if c.isParamVar(from) {
		c.markParamVar(nv)
	}
}

// freshSymbol mints one particular symbol, the type a written `unique symbol` names. Each
// call answers a symbol distinct from every other, which is what the annotation means: two
// declarations of `unique symbol` are two values, and nothing about either is written down
// for a reader to compare instead.
//
// `name` is the dotted path a program reads the symbol through, such as `sym`, `C.key`, or
// `i.key`, and is empty when no such path exists. The symbol carries it for display.
func (c *Context) freshSymbol(name string) *soltype.UniqueSymbolType {
	s := &soltype.UniqueSymbolType{ID: c.symbolCounter, Name: name}
	c.symbolCounter++
	return s
}

// markTypeParamsRigid makes params rigid for constrain from here on. fromBelow makes them
// rigid on the super side of a constraint as well as the sub side. A parameter with a
// declared lower bound, or one a sibling's lower bound names, as `U` in
// `where Box<U>: T`, is rigid on the super side either way, so a value that reaches it
// through that bound meets its skolem rather than binding it.
//
// Each parameter's bounds are read as its variable holds them now. A parameter that is
// already rigid keeps the data its first mark gave it, since a bound recorded after that
// mark is not one a body may rely on.
func (c *Context) markTypeParamsRigid(params []*soltype.TypeParam, fromBelow bool) {
	namedInLower := set.NewSet[*soltype.TypeVarType]()
	for _, tp := range params {
		if tp.LowerBound != nil {
			for _, v := range typeVarsIn(tp.LowerBound) {
				namedInLower.Add(v)
			}
		}
	}
	for _, tp := range params {
		if tp.Var.Rigid != nil {
			continue
		}
		c.checkThawed(tp.Var, "rigidifying a type parameter")
		tp.Var.Rigid = &soltype.RigidParam{
			Name:      tp.Name,
			Uppers:    slices.Clone(tp.AllUpperBounds()),
			Lowers:    slices.Clone(tp.AllLowerBounds()),
			FromBelow: fromBelow || tp.LowerBound != nil || namedInLower.Contains(tp.Var),
		}
	}
}

// rigidSkolem returns the skolem the rigid parameter v is read as, whose upper bound meets
// the parameter's upper bounds and whose lower bound joins its lower bounds. The bounds
// stay unsubstituted, so a bound naming a sibling parameter is read through that sibling's
// skolem in turn. v must be rigid.
func (c *Context) rigidSkolem(v *soltype.TypeVarType) *soltype.SkolemType {
	rp := v.Rigid
	if rp.Skolem != nil {
		return rp.Skolem
	}
	sk := c.freshSkolem(rp.Name)
	sk.Param = v
	switch len(rp.Uppers) {
	case 0:
	case 1:
		sk.Upper = rp.Uppers[0]
	default:
		sk.Upper = &soltype.IntersectionType{Types: rp.Uppers}
	}
	switch len(rp.Lowers) {
	case 0:
	case 1:
		sk.Lower = rp.Lowers[0]
	default:
		sk.Lower = &soltype.UnionType{Types: rp.Lowers}
	}
	rp.Skolem = sk
	return sk
}

// nameRigidParams returns t for display with each rigid parameter's variable replaced by
// the parameter's skolem, which prints under the parameter's name. A diagnostic names its
// operands through this, so `Cmp<T> <: number` reads as written rather than as
// `Cmp<t3> <: number`. The result is for rendering only. The type `constrain` checks is
// untouched and keeps the variable, which the variable arms read as the parameter when it
// meets another variable.
func (c *Context) nameRigidParams(t soltype.Type) soltype.Type {
	if t == nil {
		return t
	}
	return t.Accept(&rigidNameSubst{c: c}, soltype.Positive)
}

// rigidNameSubst rewrites each rigid parameter's variable to the parameter's skolem,
// minting the skolem on first use. Any other variable is left as it is.
type rigidNameSubst struct {
	c *Context
}

func (s *rigidNameSubst) EnterType(t soltype.Type, _ soltype.Polarity) soltype.EnterResult {
	if v, ok := t.(*soltype.TypeVarType); ok && v.Rigid != nil {
		return soltype.EnterResult{Type: s.c.rigidSkolem(v), SkipChildren: true}
	}
	return soltype.EnterResult{}
}

func (s *rigidNameSubst) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

// freshSkolem mints a distinct rigid type parameter carrying the given source name. It
// draws from the same counter as freshVar so every skolem has a unique ID, which is what
// keeps two parameters `T` and `U` from unifying.
func (c *Context) freshSkolem(name string) *soltype.SkolemType {
	s := &soltype.SkolemType{ID: c.varCounter, Name: name}
	c.varCounter++
	return s
}

// freshMuBinder mints the next μ-variable for a knot the regular-tree check proves, naming it under
// the same X0, X1, … convention coalesce's binders follow.
func (c *Context) freshMuBinder() *soltype.RecursiveVarType {
	v := &soltype.RecursiveVarType{ID: c.muBinderCount, Name: muBinderName(c.muBinderCount)}
	c.muBinderCount++
	return v
}

// freshInferDecl mints the declaration one `infer U` clause introduces, carrying the source name for
// display. It draws from the same counter as freshVar so every declaration has a unique id, which is
// what keeps a nested conditional's `infer U` distinct from the enclosing conditional's clause of the
// same name. The node it returns is the reference form, which resolveCondTypeAnn binds in the scope
// the branches read; the clause itself copies the id onto a binder.
func (c *Context) freshInferDecl(name string) *soltype.InferType {
	t := &soltype.InferType{ID: c.varCounter, Name: name}
	c.varCounter++
	return t
}

// freshMappedKey mints the binding one mapped type's `for K in …` clause introduces, carrying the
// source name for display. It draws from the same counter as freshVar so every binding has a unique
// id, which is what keeps a nested mapped type's key distinct from the enclosing one's when both are
// written K. resolveMappedTypeAnn binds the node it returns in the scope the value, key-remapping,
// and filter positions resolve in, so each reference to K is that one binding.
func (c *Context) freshMappedKey(name string) *soltype.MappedKeyType {
	t := &soltype.MappedKeyType{ID: c.varCounter, Name: name}
	c.varCounter++
	return t
}

// freshLifetime allocates a new lifetime variable at the given level, assigning it
// the next id in sequence. Lifetimes now ride the same let-generalization level
// hierarchy as types (M4 D2.5): a lifetime minted inside its scheme's
// generalize-level is freshened per instantiation, so two uses of a
// borrow-passing function never share one LifetimeVar.
func (c *Context) freshLifetime(level int) *soltype.LifetimeVar {
	lv := &soltype.LifetimeVar{ID: c.lifetimeCounter, Level: level}
	c.lifetimeCounter++
	return lv
}

// freshJoinLifetime allocates a lifetime variable for a multi-source join site
// (M4 D3). A join site is a return or branch uniting several borrows. It is
// identical to freshLifetime but sets Join, so coalesceLifetime expands it to the
// union of the param lifetimes it reaches rather than naming it as a borrow origin.
func (c *Context) freshJoinLifetime(level int) *soltype.LifetimeVar {
	lv := c.freshLifetime(level)
	lv.Join = true
	return lv
}

// joinLifetimes returns one lifetime that every member of lts outlives, so a value drawn
// from any of them may carry it. Several distinct lifetimes unite under a fresh join
// variable holding each of them as a lower bound, which is the join site freshJoinLifetime
// exists to serve. lts must not be empty.
//
// Where lts names one lifetime already, that lifetime is returned unchanged and the common
// `&'a A | &'a B` mints no join variable.
func (c *Context) joinLifetimes(level int, lts []soltype.Lifetime) soltype.Lifetime {
	shared := true
	for _, lt := range lts[1:] {
		if !soltype.ContainsLifetime(lts[:1], lt) {
			shared = false
			break
		}
	}
	if shared {
		return lts[0]
	}
	joinLt := c.freshJoinLifetime(level)
	for _, lt := range lts {
		c.constrainLt(lt, joinLt)
	}
	return joinLt
}

// addLowerLtBound appends lt to v's lower bounds, journaling the mutation in the
// active probe first so a discarded trial truncates it away. This (and
// addUpperLtBound) is the ONLY sanctioned way to extend a lifetime bound list —
// the second sort inherits the type sort's "appends only through journaling
// helpers" invariant so an un-journaled append cannot survive a Discard.
func (c *Context) addLowerLtBound(v *soltype.LifetimeVar, lt soltype.Lifetime) {
	c.recordLtMutation(v)
	v.LowerBounds = append(v.LowerBounds, lt)
}

// addUpperLtBound is the upper-bound counterpart of addLowerLtBound.
func (c *Context) addUpperLtBound(v *soltype.LifetimeVar, lt soltype.Lifetime) {
	c.recordLtMutation(v)
	v.UpperBounds = append(v.UpperBounds, lt)
}

// recordLtProxy notes that proxy is an outer-extruded copy of origin, so a later
// repeated outlives constraint can reuse the proxy rather than mint a new one (M4
// D2.5). Lazily allocates the map.
func (c *Context) recordLtProxy(proxy *soltype.LifetimeVar, origin soltype.Lifetime) {
	if c.ltProxyOrigin == nil {
		c.ltProxyOrigin = map[*soltype.LifetimeVar]soltype.Lifetime{}
	}
	c.ltProxyOrigin[proxy] = origin
}

// findLtProxy returns a lifetime among bounds that is an outer-extruded proxy of
// origin, or nil if none. Identity-keyed: a proxy matches only when ltProxyOrigin
// records it against the exact origin pointer. Scanning live bounds keeps it
// probe-safe — a proxy a discarded trial removed from its bound list is not found.
func (c *Context) findLtProxy(bounds []soltype.Lifetime, origin soltype.Lifetime) soltype.Lifetime {
	for _, b := range bounds {
		if bv, ok := b.(*soltype.LifetimeVar); ok && c.ltProxyOrigin[bv] == origin {
			return bv
		}
	}
	return nil
}

// recordLtMutation snapshots v's bound-list lengths in the active probe, if any,
// BEFORE a bound append mutates v — the lifetime-sort twin of recordMutation. A
// no-op when no probe is open; recordLt dedups per probe, so the first touch's
// snapshot covers every later append to v under the same probe.
func (c *Context) recordLtMutation(v *soltype.LifetimeVar) {
	c.checkThawedLt(v, "a lifetime bound")
	if c.probe != nil {
		c.probe.recordLt(v)
	}
}

// addLowerBound appends t to v's lower bounds, journaling the mutation in the
// active probe first so a discarded trial truncates it away. This (and
// addUpperBound) is the ONLY sanctioned way to extend a bound list.
func (c *Context) addLowerBound(v *soltype.TypeVarType, t soltype.Type) {
	c.recordMutation(v)
	v.LowerBounds = append(v.LowerBounds, t)
}

// addUpperBound is the upper-bound counterpart of addLowerBound.
func (c *Context) addUpperBound(v *soltype.TypeVarType, t soltype.Type) {
	c.recordMutation(v)
	v.UpperBounds = append(v.UpperBounds, t)
}

// replaceLowerBound replaces v's lower bound at index i with t and returns the errors that
// checking t against v's upper bounds and borrowed reads reports, as adding t would. A
// discarded probe puts the replaced bound back.
func (c *Context) replaceLowerBound(v *soltype.TypeVarType, i int, t soltype.Type) []SolverError {
	c.recordMutation(v)
	prev := v.LowerBounds[i]
	v.LowerBounds[i] = t
	if c.probe != nil {
		c.probe.onRollback(func() { v.LowerBounds[i] = prev })
	}
	seen := newSeenPairs()
	var errs []SolverError
	for _, ub := range v.UpperBounds {
		errs = append(errs, c.constrain(t, ub, seen, false)...)
	}
	for _, r := range c.borrowedReads[v] {
		errs = append(errs, c.readThroughLowerBound(r, v, t, seen)...)
	}
	return c.breadcrumbUnionCommit(errs, v)
}

// borrowedRead is a field-read requirement made through a borrow whose pointee is a type
// variable. borrow carries the mutability and lifetime the read is bounded by. shape is the
// requirement the variable itself is constrained against. It names the same properties as
// req, each with a fresh variable that every lower bound's ordinary read flows into.
type borrowedRead struct {
	borrow *soltype.RefType
	req    *soltype.ObjectType
	shape  *soltype.ObjectType
}

// addBorrowedRead records r against v in borrowedReads. Under an open probe a discard
// removes the entry again, as it truncates a bound list.
func (c *Context) addBorrowedRead(v *soltype.TypeVarType, r borrowedRead) {
	if c.borrowedReads == nil {
		c.borrowedReads = map[*soltype.TypeVarType][]borrowedRead{}
	}
	prev := len(c.borrowedReads[v])
	c.borrowedReads[v] = append(c.borrowedReads[v], r)
	if c.probe != nil {
		c.probe.onRollback(func() { c.borrowedReads[v] = c.borrowedReads[v][:prev] })
	}
}

// recordMutation snapshots v's bound-list lengths in the active probe, if any,
// BEFORE a bound append mutates v. A no-op when no probe is open. record itself
// dedups (only the first touch of v in a probe snapshots), so calling this at
// every append site is cheap and correct: append-only bound lists mean the first
// snapshot already covers every later append to v under the same probe. Reached
// only through addLowerBound, addUpperBound, and replaceLowerBound. A replacement
// keeps the list's length, so replaceLowerBound registers its own rollback to put
// the replaced bound back.
func (c *Context) recordMutation(v *soltype.TypeVarType) {
	c.checkThawed(v, "a type bound")
	if c.probe != nil {
		c.probe.record(v)
	}
}
