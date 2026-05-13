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
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dts_parser"
	"github.com/escalier-lang/escalier/internal/type_system"
)

func appendOwner(owner dts_parser.QualIdent, child string) dts_parser.QualIdent {
	right := dts_parser.NewIdent(child, ast.Span{})
	if owner == nil {
		return right
	}
	return &dts_parser.Member{Left: owner, Right: right}
}

// Collapse performs the three-tier shadowing collapse described in
// §5.5 step 7: for each (module, container path, slot) triple, take
// the highest-precedence non-nil entry across the three input maps
// (UserProject > UserDep > Shipped).
//
// `tiers` is expected to be ordered by precedence (highest first); the
// caller is responsible for that ordering. Passing fewer than three
// maps is allowed (missing tiers are skipped).
//
// Each surviving Effective has its Tier field stamped with the tier it
// won from; the merge pass reads that field when populating Source.
func Collapse(tiers []map[string]*ModuleScope, tierOrder []OverrideTier) map[string]*ModuleScope {
	out := make(map[string]*ModuleScope)
	for i, t := range tiers {
		ot := tierOrder[i]
		for modName, ms := range t {
			if ms == nil {
				continue
			}
			dst := out[modName]
			if dst == nil {
				dst = &ModuleScope{
					Container: newContainer(ms.Container.Origin),
				}
				out[modName] = dst
			}
			if ms.AllPure && !dst.AllPure {
				dst.AllPure = true
				dst.AllPureTier = ot
			}
			collapseContainer(&dst.Container, &ms.Container, ot)
		}
	}
	return out
}

func collapseContainer(dst, src *Container, ot OverrideTier) {
	for name, eff := range src.Free {
		if _, present := dst.Free[name]; present {
			continue
		}
		dst.Free[name] = stampedCopy(eff, ot)
	}
	for name, child := range src.Children {
		dstChild := dst.Children[name]
		if dstChild == nil {
			dstChild = &ChildScope{
				Container: newContainer(child.Container.Origin),
			}
			if child.Instance != nil {
				dstChild.Instance = NewMemberSet()
			}
			if child.Static != nil {
				dstChild.Static = NewMemberSet()
			}
			dst.Children[name] = dstChild
		}
		collapseContainer(&dstChild.Container, &child.Container, ot)
		collapseMemberSet(dstChild.Instance, child.Instance, ot)
		collapseMemberSet(dstChild.Static, child.Static, ot)
	}
}

func collapseMemberSet(dst, src *MemberSet, ot OverrideTier) {
	if src == nil {
		return
	}
	if dst == nil {
		return
	}
	copyIfAbsent(dst.Methods, src.Methods, ot)
	copyIfAbsent(dst.Getters, src.Getters, ot)
	copyIfAbsent(dst.Setters, src.Setters, ot)
	copyIfAbsent(dst.Properties, src.Properties, ot)
	if dst.Ctor == nil && src.Ctor != nil {
		dst.Ctor = stampedCopy(src.Ctor, ot)
	}
}

func copyIfAbsent(dst, src map[string]*Effective, ot OverrideTier) {
	for name, eff := range src {
		if _, present := dst[name]; present {
			continue
		}
		dst[name] = stampedCopy(eff, ot)
	}
}

// stampedCopy returns a copy of eff with Tier/Source stamped. We copy
// rather than mutate so the per-tier Effective produced by Extract is
// not shared across collapse runs.
func stampedCopy(eff *Effective, ot OverrideTier) *Effective {
	if eff == nil {
		return nil
	}
	cp := *eff
	cp.Tier = ot
	cp.Source = ot.ResolutionTierFor()
	return &cp
}

func newContainer(origin Origin) Container {
	return Container{
		Free:     make(map[string]*Effective),
		Children: make(map[string]*ChildScope),
		Origin:   origin,
	}
}

// Merge applies the collapsed override scope on top of the original
// per-module scopes and returns a fresh OverrideStore. The original
// side passes through with no Source stamped (Classify's lower tiers
// determine its classification); the override side replaces matching
// slots entirely and runs consistency.CheckSet on each method/free-fn
// slot that pairs with an original.
//
// `original` may be nil for any module that wasn't pre-loaded — in
// that case only override-side entries are reported (the consistency
// check is skipped) and ErrUnknownMember is suppressed since there's
// nothing to compare against.
func Merge(original, override map[string]*ModuleScope) (*OverrideStore, []error) {
	store := NewOverrideStore()
	var errs []error

	// Visit every module that appears on either side.
	seen := make(map[string]bool)
	for modName := range original {
		seen[modName] = true
	}
	for modName := range override {
		seen[modName] = true
	}

	for modName := range seen {
		orig := original[modName]
		over := override[modName]

		ms := &ModuleScope{
			Container: newContainer(originOf(orig, over)),
		}
		if over != nil {
			ms.AllPure = over.AllPure
			ms.AllPureTier = over.AllPureTier
		}
		store.Modules[modName] = ms

		modPath := Path{Module: modName}
		mergeContainer(&ms.Container, containerOf(orig), containerOf(over), modPath, ms.AllPure, ms.AllPureTier, &errs)
	}
	return store, errs
}

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
	owner Path,
	allPure bool,
	allPureTier OverrideTier,
	errs *[]error,
) {
	// Free entries: union of names from both sides.
	names := make(map[string]bool)
	if orig != nil {
		for n := range orig.Free {
			names[n] = true
		}
	}
	if over != nil {
		for n := range over.Free {
			names[n] = true
		}
	}
	for n := range names {
		var oEff, vEff *Effective
		if orig != nil {
			oEff = orig.Free[n]
		}
		if over != nil {
			vEff = over.Free[n]
		}
		leaf, err := mergeLeaf(oEff, vEff, withName(owner, n, KindFree, false), false /*allPure n/a for free*/, allPureTier, orig != nil)
		if err != nil {
			*errs = append(*errs, err)
		}
		if leaf != nil {
			dst.Free[n] = leaf
		}
	}

	// Children: union of names.
	childNames := make(map[string]bool)
	if orig != nil {
		for n := range orig.Children {
			childNames[n] = true
		}
	}
	if over != nil {
		for n := range over.Children {
			childNames[n] = true
		}
	}
	for n := range childNames {
		var oCh, vCh *ChildScope
		if orig != nil {
			oCh = orig.Children[n]
		}
		if over != nil {
			vCh = over.Children[n]
		}
		mergedChild := mergeChild(oCh, vCh, withChildOwner(owner, n), allPure, allPureTier, errs)
		if mergedChild != nil {
			dst.Children[n] = mergedChild
		}
	}
}

func mergeChild(
	orig, over *ChildScope,
	owner Path,
	allPure bool,
	allPureTier OverrideTier,
	errs *[]error,
) *ChildScope {
	dst := &ChildScope{
		Container: newContainer(childOrigin(orig, over)),
	}
	// Decide shape: namespace (no Instance/Static on either side) or
	// class/interface (Instance/Static populated).
	hasMembers := (orig != nil && (orig.Instance != nil || orig.Static != nil)) ||
		(over != nil && (over.Instance != nil || over.Static != nil))
	if hasMembers {
		dst.Instance = NewMemberSet()
		dst.Static = NewMemberSet()
	}

	mergeContainer(&dst.Container, containerFromChild(orig), containerFromChild(over), owner, allPure, allPureTier, errs)

	if hasMembers {
		mergeMemberSet(dst.Instance, msFromChild(orig, false), msFromChild(over, false), owner, false, allPure, allPureTier, errs)
		mergeMemberSet(dst.Static, msFromChild(orig, true), msFromChild(over, true), owner, true, allPure, allPureTier, errs)
	}
	return dst
}

func childOrigin(orig, over *ChildScope) Origin {
	if over != nil {
		return over.Container.Origin
	}
	if orig != nil {
		return orig.Container.Origin
	}
	return Origin{}
}

func containerFromChild(c *ChildScope) *Container {
	if c == nil {
		return nil
	}
	return &c.Container
}

func msFromChild(c *ChildScope, static bool) *MemberSet {
	if c == nil {
		return nil
	}
	if static {
		return c.Static
	}
	return c.Instance
}

func mergeMemberSet(
	dst, orig, over *MemberSet,
	owner Path,
	static bool,
	allPure bool,
	allPureTier OverrideTier,
	errs *[]error,
) {
	// @all_pure only strips mut from instance-method receivers; static
	// methods have no receiver, so the flag is silenced on the static side.
	mergeKind(orig, over, dst, owner, KindMethod, static, allPure && !static, allPureTier, errs)
	mergeKind(orig, over, dst, owner, KindGetter, static, false, allPureTier, errs)
	mergeKind(orig, over, dst, owner, KindSetter, static, false, allPureTier, errs)
	mergeKind(orig, over, dst, owner, KindProperty, static, false, allPureTier, errs)

	// Ctor is a single slot.
	var oCtor, vCtor *Effective
	if orig != nil {
		oCtor = orig.Ctor
	}
	if over != nil {
		vCtor = over.Ctor
	}
	leaf, err := mergeLeaf(oCtor, vCtor, withName(owner, "", KindCtor, static), false, allPureTier, orig != nil)
	if err != nil {
		*errs = append(*errs, err)
	}
	if leaf != nil {
		dst.Ctor = leaf
	}
}

func mergeKind(
	orig, over, dst *MemberSet,
	owner Path,
	kind MemberKind,
	static bool,
	allPureForThisKind bool,
	allPureTier OverrideTier,
	errs *[]error,
) {
	oMap := kindMap(orig, kind)
	vMap := kindMap(over, kind)
	dMap := kindMap(dst, kind)
	names := make(map[string]bool)
	for n := range oMap {
		names[n] = true
	}
	for n := range vMap {
		names[n] = true
	}
	origParentExists := orig != nil
	for n := range names {
		leaf, err := mergeLeaf(oMap[n], vMap[n], withName(owner, n, kind, static), allPureForThisKind, allPureTier, origParentExists)
		if err != nil {
			*errs = append(*errs, err)
		}
		if leaf != nil {
			dMap[n] = leaf
		}
	}
}

func kindMap(ms *MemberSet, kind MemberKind) map[string]*Effective {
	if ms == nil {
		return nil
	}
	switch kind {
	case KindMethod:
		return ms.Methods
	case KindGetter:
		return ms.Getters
	case KindSetter:
		return ms.Setters
	case KindProperty:
		return ms.Properties
	}
	return nil
}

// mergeLeaf produces the merged Effective for a single slot:
//   - both nil: returns nil (caller does not set).
//   - only orig: return a fresh Effective with Source unstamped.
//   - only over: ErrUnknownMember when the parent original scope was
//     present (origParentExists==true) — the override targets a
//     non-existent sibling. When the parent was absent
//     (origParentExists==false) the override is emitted as-is per §5.7.
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
	origParentExists bool,
) (*Effective, error) {
	switch {
	case orig == nil && over == nil:
		return nil, nil
	case orig != nil && over == nil:
		if allPureForKind {
			return synthesiseAllPure(orig, allPureTier), nil
		}
		// Leave Source unstamped — Classify's lower tiers decide.
		return &Effective{
			Type:       orig.Type,
			Provenance: orig.Provenance,
		}, nil
	case orig == nil && over != nil:
		if origParentExists {
			return over, &ErrUnknownMember{
				Path:     p,
				Override: lastOrigin(over.Provenance),
			}
		}
		// Parent original wasn't pre-loaded — accept the override
		// silently (no original to compare against).
		return over, nil
	default:
		// Both: override replaces original. Consistency check on
		// FuncType pairs (single-signature for now; CheckSet handles
		// overload sets when packed under a union/intersection — left
		// for follow-up).
		if oFn, ok := orig.Type.(*type_system.FuncType); ok {
			if vFn, ok2 := over.Type.(*type_system.FuncType); ok2 {
				origin := lastOrigin(over.Provenance)
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
			Type:       orig.Type,
			Provenance: orig.Provenance,
		}
	}
	stripped := stripMutSelf(fn)
	return &Effective{
		Type:       stripped,
		Source:     tier.ResolutionTierFor(),
		Provenance: orig.Provenance,
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

func lastOrigin(ps []Origin) Origin {
	if len(ps) == 0 {
		return Origin{}
	}
	return ps[len(ps)-1]
}

// withName returns owner with Name/Kind/Static set.
func withName(owner Path, name string, kind MemberKind, static bool) Path {
	if name != "" {
		owner.Name = dts_parser.NewIdent(name, ast.Span{})
	} else {
		owner.Name = nil
	}
	owner.Kind = kind
	owner.Static = static
	return owner
}

func withChildOwner(owner Path, child string) Path {
	// Append the child segment onto Owner via dts_parser.Member chain.
	// For now we use a synthetic identifier wrapper since the merge
	// only consumes Owner for diagnostics.
	cp := owner
	cp.Owner = appendOwner(owner.Owner, child)
	return cp
}
