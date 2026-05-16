// merge.go: the override-merge pipeline described in
// planning/interop_mutability/implementation_plan.md §5.
//
// Exports:
//
//   - Collapse: three-tier shadowing collapse of per-tier scopes (§5.5
//     step 7).
//   - Merge: eager merge of a collapsed override scope onto the
//     original (§5.6).
//
// The loader's full pipeline (Discover + Build) lives in loader.go.

package interop

import (
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// Collapse performs the three-tier shadowing collapse described in
// §5.5 step 7: for each (module, container path, slot) triple, take
// the highest-precedence non-nil entry across the three tiers
// (UserProject > UserDep > Builtin). Any tier map may be nil, in which
// case it contributes nothing.
//
// Each surviving Effective is returned with its Tier set to the tier
// it won from; the merge pass reads that field when populating Source.
func Collapse(
	userProject, userDep, builtin map[string]*ModuleScope,
) map[string]*ModuleScope {
	out := make(map[string]*ModuleScope)
	// Iterated highest-precedence first; collapseContainer's "skip if
	// already present" rule then guarantees the highest tier wins.
	collapseTier(out, userProject, OverrideTierUserProject)
	collapseTier(out, userDep, OverrideTierUserDep)
	collapseTier(out, builtin, OverrideTierBuiltin)
	return out
}

func collapseTier(out, moduleMap map[string]*ModuleScope, tier OverrideTier) {
	for name, srcScope := range moduleMap {
		if srcScope == nil {
			continue
		}
		dstScope := out[name]
		if dstScope == nil {
			dstScope = &ModuleScope{
				Container: newContainer(srcScope.Container.Origin),
			}
			out[name] = dstScope
		}
		if srcScope.AllPure && !dstScope.AllPure {
			dstScope.AllPure = true
			dstScope.AllPureTier = tier
		}
		collapseContainer(&dstScope.Container, &srcScope.Container, tier)
	}
}

func collapseContainer(dst, src *Container, ot OverrideTier) {
	for name, eff := range src.Free {
		if _, present := dst.Free[name]; present {
			continue
		}
		dst.Free[name] = eff.withTier(ot)
	}
	for name, child := range src.Children {
		dst.Children[name] = collapseChild(dst.Children[name], child, ot)
	}
}

// collapseChild folds `src` into `existing` for one Container.Children
// slot during the three-tier collapse. When existing is nil we build a
// fresh ChildScope of the same variant as src. When the variants match
// we recurse. When they mismatch the higher tier (already in existing)
// wins wholesale — src is dropped.
func collapseChild(existing, src ChildScope, tier OverrideTier) ChildScope {
	if existing == nil {
		return cloneChildWithTier(src, tier)
	}
	switch e := existing.(type) {
	case *NamespaceScope:
		s, ok := src.(*NamespaceScope)
		if !ok {
			return existing
		}
		collapseContainer(&e.Container, &s.Container, tier)
	case *ClassScope:
		s, ok := src.(*ClassScope)
		if !ok {
			return existing
		}
		collapseMemberSet(e.Instance, s.Instance, tier)
		collapseMemberSet(e.Static, s.Static, tier)
	case *InterfaceScope:
		s, ok := src.(*InterfaceScope)
		if !ok {
			return existing
		}
		collapseMemberSet(e.Instance, s.Instance, tier)
	}
	return existing
}

// cloneChildWithTier produces a fresh ChildScope of the same variant as
// src, populated by collapsing src's contents with `tier` stamped on
// every leaf.
func cloneChildWithTier(src ChildScope, tier OverrideTier) ChildScope {
	switch s := src.(type) {
	case *NamespaceScope:
		c := &NamespaceScope{Container: newContainer(s.Container.Origin)}
		collapseContainer(&c.Container, &s.Container, tier)
		return c
	case *ClassScope:
		c := &ClassScope{
			Origin:   s.Origin,
			Instance: NewMemberSet(),
			Static:   NewMemberSet(),
		}
		collapseMemberSet(c.Instance, s.Instance, tier)
		collapseMemberSet(c.Static, s.Static, tier)
		return c
	case *InterfaceScope:
		c := &InterfaceScope{
			Origin:   s.Origin,
			Instance: NewMemberSet(),
		}
		collapseMemberSet(c.Instance, s.Instance, tier)
		return c
	}
	return nil
}

func collapseMemberSet(dst, src *MemberSet, tier OverrideTier) {
	if src == nil {
		return
	}
	if dst == nil {
		return
	}
	copyIfAbsent(dst.Methods, src.Methods, tier)
	copyIfAbsent(dst.Getters, src.Getters, tier)
	copyIfAbsent(dst.Setters, src.Setters, tier)
	copyIfAbsent(dst.Properties, src.Properties, tier)
	if dst.Ctor == nil && src.Ctor != nil {
		dst.Ctor = src.Ctor.withTier(tier)
	}
}

func copyIfAbsent(dst, src map[string]*Effective, tier OverrideTier) {
	for name, eff := range src {
		if _, present := dst[name]; present {
			continue
		}
		dst[name] = eff.withTier(tier)
	}
}

func newContainer(origin Origin) Container {
	return Container{
		Free:     make(map[string]*Effective),
		Children: make(map[string]ChildScope),
		Origin:   origin,
	}
}

// Merge applies the collapsed override scope on top of the original
// per-module scopes and returns a fresh OverrideStore. The original
// side passes through with Source left at its zero value (Classify's lower tiers
// determine its classification); the override side replaces matching
// slots entirely and runs consistency.CheckSet on each method/free-fn
// slot that pairs with an original.
//
// Every Escalier library ships .d.ts for TypeScript back-compat
// (requirements.md §F), so an override-only leaf — with no matching
// original — is always either a typo or caller misuse (originals not
// pre-loaded). Such leaves still land in the store, but ErrUnknownMember
// is reported. If `original` is nil for a module that has overrides,
// every override leaf in that module produces an ErrUnknownMember.
func Merge(original, override map[string]*ModuleScope) (*OverrideStore, []error) {
	store := NewOverrideStore()
	var errs []error

	// Visit every module that appears on either side.
	seen := set.NewSet[string]()
	for modName := range original {
		seen.Add(modName)
	}
	for modName := range override {
		seen.Add(modName)
	}

	for modName := range seen {
		origScope := original[modName]
		overScope := override[modName]

		mergedScope := &ModuleScope{
			Container: newContainer(originOf(origScope, overScope)),
		}
		if overScope != nil {
			mergedScope.AllPure = overScope.AllPure
			mergedScope.AllPureTier = overScope.AllPureTier
		}
		store.Modules[modName] = mergedScope

		modPath := Path{Module: modName}
		mergeContainer(
			&mergedScope.Container,
			containerOf(origScope), containerOf(overScope),
			modPath, mergedScope.AllPure, mergedScope.AllPureTier, &errs,
		)
	}
	return store, errs
}

// originOf picks the Container.Origin to attach to the merged
// ModuleScope. The override side wins when present (it's the file a
// module-level diagnostic most likely wants to point at after merge);
// otherwise we fall back to the original side, then to a zero Origin.
//
// Mirrors childOrigin, which applies the same rule to ChildScope.
func originOf(orig, over *ModuleScope) Origin {
	if over != nil {
		return over.Container.Origin
	}
	if orig != nil {
		return orig.Container.Origin
	}
	return Origin{}
}

func containerOf(ms *ModuleScope) *Container {
	if ms == nil {
		return nil
	}
	return &ms.Container
}

func mergeContainer(
	dst, orig, over *Container,
	ownerPath Path,
	allPure bool,
	allPureTier OverrideTier,
	errs *[]error,
) {
	// Free entries: union of names from both sides.
	names := set.NewSet[string]()
	if orig != nil {
		for n := range orig.Free {
			names.Add(n)
		}
	}
	if over != nil {
		for n := range over.Free {
			names.Add(n)
		}
	}
	for n := range names {
		var origEff, overEff *Effective
		if orig != nil {
			origEff = orig.Free[n]
		}
		if over != nil {
			overEff = over.Free[n]
		}
		leaf, err := mergeLeaf(
			origEff, overEff, ownerPath.withMember(n, KindFree, false),
			false /*allPure n/a for free*/, allPureTier,
		)
		if err != nil {
			*errs = append(*errs, err)
		}
		if leaf != nil {
			dst.Free[n] = leaf
		}
	}

	// Children: union of names.
	childNames := set.NewSet[string]()
	if orig != nil {
		for n := range orig.Children {
			childNames.Add(n)
		}
	}
	if over != nil {
		for n := range over.Children {
			childNames.Add(n)
		}
	}
	for n := range childNames {
		var origCh, overCh ChildScope
		if orig != nil {
			origCh = orig.Children[n]
		}
		if over != nil {
			overCh = over.Children[n]
		}
		mergedChild := mergeChild(
			origCh, overCh, ownerPath.withChild(n),
			allPure, allPureTier, errs,
		)
		if mergedChild != nil {
			dst.Children[n] = mergedChild
		}
	}
}

// mergeChild merges one Container.Children entry across original and
// override sides. The variant of the result follows whichever side is
// present; if both sides have a value with the same variant we recurse
// per slot. A shape mismatch between the two sides (e.g. namespace vs
// class at the same name) is upstream ErrDuplicateMember — we emit the
// error and proceed with the override side's variant.
func mergeChild(
	orig, over ChildScope,
	ownerPath Path,
	allPure bool,
	allPureTier OverrideTier,
	errs *[]error,
) ChildScope {
	if orig == nil && over == nil {
		return nil
	}

	// Resolve the variant of the result and obtain its (possibly nil)
	// orig/over MemberSet pairs.
	if orig != nil && over != nil && !sameChildKind(orig, over) {
		*errs = append(*errs, &ErrDuplicateMember{
			Path:   ownerPath,
			First:  orig.ChildOrigin(),
			Second: over.ChildOrigin(),
		})
		orig = nil // proceed with override side only
	}

	return mergeSameKindChild(orig, over, ownerPath, allPure, allPureTier, errs)
}

func sameChildKind(a, b ChildScope) bool {
	switch a.(type) {
	case *NamespaceScope:
		_, ok := b.(*NamespaceScope)
		return ok
	case *ClassScope:
		_, ok := b.(*ClassScope)
		return ok
	case *InterfaceScope:
		_, ok := b.(*InterfaceScope)
		return ok
	}
	return false
}

// mergeSameKindChild assumes orig and over are either nil or the same
// variant. It picks a shape, recurses on Container, and merges any
// MemberSets the variant carries.
func mergeSameKindChild(
	orig, over ChildScope,
	ownerPath Path,
	allPure bool,
	allPureTier OverrideTier,
	errs *[]error,
) ChildScope {
	origin := childOrigin(orig, over)

	// Pick the shape based on whichever side is present (over wins).
	probe := over
	if probe == nil {
		probe = orig
	}

	switch probe.(type) {
	case *NamespaceScope:
		dst := &NamespaceScope{Container: newContainer(origin)}
		var origCont, overCont *Container
		if ns, ok := orig.(*NamespaceScope); ok {
			origCont = &ns.Container
		}
		if ns, ok := over.(*NamespaceScope); ok {
			overCont = &ns.Container
		}
		mergeContainer(&dst.Container, origCont, overCont,
			ownerPath, allPure, allPureTier, errs)
		return dst
	case *ClassScope:
		dst := &ClassScope{
			Origin:   origin,
			Instance: NewMemberSet(),
			Static:   NewMemberSet(),
		}
		origInstMembs := memberSetFromChild(orig, false)
		overInstMembs := memberSetFromChild(over, false)
		mergeMemberSet(
			dst.Instance, origInstMembs, overInstMembs,
			ownerPath, false, allPure, allPureTier, errs,
		)
		origStatMembs := memberSetFromChild(orig, true)
		overStatMembs := memberSetFromChild(over, true)
		mergeMemberSet(
			dst.Static, origStatMembs, overStatMembs,
			ownerPath, true, allPure, allPureTier, errs,
		)
		return dst
	case *InterfaceScope:
		dst := &InterfaceScope{
			Origin:   origin,
			Instance: NewMemberSet(),
		}
		origInstMembs := memberSetFromChild(orig, false)
		overInstMembs := memberSetFromChild(over, false)
		mergeMemberSet(
			dst.Instance, origInstMembs, overInstMembs,
			ownerPath, false, allPure, allPureTier, errs,
		)
		return dst
	}
	return nil
}

// childOrigin picks the Origin to attach to the merged ChildScope. The
// override side wins when present (it's the file a child-level
// diagnostic most likely wants to point at after merge); otherwise we
// fall back to the original side, then to a zero Origin.
//
// Mirrors originOf, which applies the same rule to ModuleScope.
func childOrigin(orig, over ChildScope) Origin {
	if over != nil {
		return over.ChildOrigin()
	}
	if orig != nil {
		return orig.ChildOrigin()
	}
	return Origin{}
}

// memberSetFromChild is a nil-safe wrapper around ChildScope.MembersFor. The
// method itself can't be called on a nil interface value (Go panics on
// nil-interface dispatch), so callers that may pass a nil ChildScope
// go through this helper.
func memberSetFromChild(c ChildScope, static bool) *MemberSet {
	if c == nil {
		return nil
	}
	return c.MembersFor(static)
}

func mergeMemberSet(
	dst, orig, over *MemberSet,
	ownerPath Path,
	static bool,
	allPure bool,
	allPureTier OverrideTier,
	errs *[]error,
) {
	// @all_pure only strips mut from instance-method receivers; static
	// methods have no receiver, so the flag is silenced on the static side.
	mergeKind(orig, over, dst, ownerPath, KindMethod, static, allPure && !static, allPureTier, errs)
	mergeKind(orig, over, dst, ownerPath, KindGetter, static, false, allPureTier, errs)
	mergeKind(orig, over, dst, ownerPath, KindSetter, static, false, allPureTier, errs)
	mergeKind(orig, over, dst, ownerPath, KindProperty, static, false, allPureTier, errs)

	// Ctor is a single slot.
	var origCtor, overCtor *Effective
	if orig != nil {
		origCtor = orig.Ctor
	}
	if over != nil {
		overCtor = over.Ctor
	}
	leaf, err := mergeLeaf(origCtor, overCtor, ownerPath.withMember("", KindCtor, static), false, allPureTier)
	if err != nil {
		*errs = append(*errs, err)
	}
	if leaf != nil {
		dst.Ctor = leaf
	}
}

func mergeKind(
	orig, over, dst *MemberSet,
	ownerPath Path,
	kind MemberKind,
	static bool,
	allPureForThisKind bool,
	allPureTier OverrideTier,
	errs *[]error,
) {
	origMap := kindMap(orig, kind)
	overMap := kindMap(over, kind)
	dMap := kindMap(dst, kind)
	names := set.NewSet[string]()
	for n := range origMap {
		names.Add(n)
	}
	for n := range overMap {
		names.Add(n)
	}
	for n := range names {
		leaf, err := mergeLeaf(
			origMap[n], overMap[n],
			ownerPath.withMember(n, kind, static),
			allPureForThisKind, allPureTier,
		)
		if err != nil {
			*errs = append(*errs, err)
		}
		if leaf != nil {
			dMap[n] = leaf
		}
	}
}

func kindMap(memberSet *MemberSet, kind MemberKind) map[string]*Effective {
	if memberSet == nil {
		return nil
	}
	switch kind {
	case KindMethod:
		return memberSet.Methods
	case KindGetter:
		return memberSet.Getters
	case KindSetter:
		return memberSet.Setters
	case KindProperty:
		return memberSet.Properties
	}
	return nil
}

// mergeLeaf produces the merged Effective for a single slot:
//   - both nil: returns nil (caller does not set).
//   - only orig: return a fresh Effective with Source left unset.
//   - only over: ErrUnknownMember — the override targets a name that
//     doesn't exist on the original side. Every Escalier library ships
//     .d.ts for TS back-compat (requirements.md §F), so there is no
//     "no original types" case; an override-only leaf is always either
//     a typo or caller misuse (originals not pre-loaded). The leaf is
//     still returned so the caller can populate the store, but the
//     error is reported.
//   - both: replace with override; run consistency check when both
//     are *FuncType.
//
// `allPureForKind` true means the slot is in the @all_pure-affected
// set (i.e. an instance method). When true and only orig is present,
// synthesise a stripped Effective.
func mergeLeaf(
	orig, over *Effective,
	p Path,
	allPureForKind bool,
	allPureTier OverrideTier,
) (*Effective, error) {
	switch {
	case orig == nil && over == nil:
		return nil, nil
	case orig != nil && over == nil:
		if allPureForKind {
			return synthesiseAllPure(orig, allPureTier), nil
		}
		// Leave Source unset — Classify's lower tiers decide.
		return &Effective{
			Type:    orig.Type,
			Origins: orig.Origins,
		}, nil
	case orig == nil && over != nil:
		return over, &ErrUnknownMember{
			Path:     p,
			Override: lastOrigin(over.Origins),
		}
	default:
		// Both: override replaces original. Consistency check on
		// FuncType pairs (single-signature for now; CheckSet handles
		// overload sets when packed under a union/intersection — left
		// for follow-up).
		if oFn, ok := orig.Type.(*type_system.FuncType); ok {
			if vFn, ok2 := over.Type.(*type_system.FuncType); ok2 {
				origin := lastOrigin(over.Origins)
				if err := Check(vFn, oFn, p, origin); err != nil {
					return over, err
				}
			}
		}
		return over, nil
	}
}

func synthesiseAllPure(orig *Effective, tier OverrideTier) *Effective {
	fn, ok := orig.Type.(*type_system.FuncType)
	if !ok || fn.SelfParam == nil {
		// Not a receiver-bearing method; @all_pure doesn't apply.
		return &Effective{
			Type:    orig.Type,
			Origins: orig.Origins,
		}
	}
	stripped := stripMutSelf(fn)
	return &Effective{
		Type:    stripped,
		Source:  tier.ResolutionTierFor(),
		Origins: orig.Origins,
	}
}

// stripMutSelf returns a shallow copy of fn with SelfParam.Type
// unwrapped from MutType, if it was wrapped.
func stripMutSelf(fn *type_system.FuncType) *type_system.FuncType {
	if fn.SelfParam == nil {
		return fn
	}
	mt, ok := fn.SelfParam.Type.(*type_system.MutType)
	if !ok {
		return fn
	}
	cp := *fn
	cp.SelfParam = &type_system.FuncParam{
		Pattern:  fn.SelfParam.Pattern,
		Type:     mt.Type,
		Optional: fn.SelfParam.Optional,
	}
	return &cp
}

func lastOrigin(origins []Origin) Origin {
	if len(origins) == 0 {
		return Origin{}
	}
	return origins[len(origins)-1]
}
