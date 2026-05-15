package interop

import (
	"strconv"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dts_parser"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// OverrideStore holds the post-merge module map. Per-tier pre-merge
// module maps exist only inside Build and are not retained — every
// diagnostic that needs provenance reads Effective.Provenance, which
// already carries the contributing Origins.
//
// See planning/interop_mutability/implementation_plan.md §5.
type OverrideStore struct {
	// Modules keyed by module specifier ("" = global; "lodash", "fs", etc.).
	Modules map[string]*ModuleScope
}

// NewOverrideStore returns an empty store.
func NewOverrideStore() *OverrideStore {
	return &OverrideStore{Modules: make(map[string]*ModuleScope)}
}

// Container holds the slots populated by modules and namespaces:
// free-function-style entries plus a map of nested children. Embedded by
// both ModuleScope and ChildScope so namespace-nested functions land in
// the same slot as module-top-level functions.
type Container struct {
	Free     map[string]*Effective  // free functions, vals, type aliases
	Children map[string]*ChildScope // nested namespaces / classes / interfaces
	Origin   Origin                 // declaring file for diagnostics
}

// ModuleScope is the per-module root.
type ModuleScope struct {
	Container
	AllPure bool // module-level @all_pure pragma
	// Tier is the OverrideTier that produced this ModuleScope's AllPure
	// flag. Meaningful only when AllPure is true.
	AllPureTier OverrideTier
}

// ChildScope is a namespace, class, or interface. Namespaces use only
// Container.Free and Container.Children; classes/interfaces use Instance
// and Static. A ChildScope with both shapes populated is an upstream
// ErrDuplicateMember — namespace+class declaration merging is not supported.
type ChildScope struct {
	Container
	Instance *MemberSet // nil for namespaces
	Static   *MemberSet // nil for namespaces
}

// MemberSet groups the four independent slots that share a name space
// within a class/interface, plus the constructor.
type MemberSet struct {
	Methods    map[string]*Effective
	Getters    map[string]*Effective
	Setters    map[string]*Effective
	Properties map[string]*Effective
	Ctor       *Effective // single slot per class
}

// NewMemberSet returns an empty MemberSet with all maps initialised.
// Ctor is intentionally left nil — callers nil-check it because there
// is at most one constructor per class.
func NewMemberSet() *MemberSet {
	return &MemberSet{
		Methods:    make(map[string]*Effective),
		Getters:    make(map[string]*Effective),
		Setters:    make(map[string]*Effective),
		Properties: make(map[string]*Effective),
	}
}

// OverrideTier identifies where an override came from. Distinct from
// ResolutionTier (the 7-tier classification ladder) — OverrideTier is
// only used inside the override system to drive the internal three-tier
// collapse (§5.5).
//
// Lower integer = higher precedence.
type OverrideTier int

const (
	OverrideTierUserProject OverrideTier = iota // requirements tier 1b
	OverrideTierUserDep                         // requirements tier 1a
	OverrideTierShipped                         // requirements tier 4
)

// ResolutionTierFor maps an OverrideTier to the broader ResolutionTier
// the merge stamps on Effective.Source.
func (t OverrideTier) ResolutionTierFor() ResolutionTier {
	switch t {
	case OverrideTierUserProject, OverrideTierUserDep:
		return TierUserOverride
	case OverrideTierShipped:
		return TierShippedOverride
	}
	return TierDefault
}

// Effective is the merged result for a single member. Receiver shape
// (no receiver / self / mut self) is encoded structurally on Type's
// *FuncType.SelfParam — callers use type_system.ReceiverIsMut.
type Effective struct {
	Type       type_system.Type
	Source     ResolutionTier
	Provenance []Origin

	// Tier records the OverrideTier that produced this leaf in the
	// collapsed per-tier scope. Set by extract; consumed by merge when
	// stamping Source. Not meaningful on Effectives after merge — merge
	// derives Source from this field once and may then clear it.
	Tier OverrideTier
}

// OriginKind discriminates the kind of source location.
type OriginKind int

const (
	OriginalDTS OriginKind = iota
	OverrideFile
)

// Origin is the declaring location of an entry (or the source-side of a
// merge participant).
type Origin struct {
	Kind OriginKind
	// FilePath is an opaque, human-readable locator surfaced in
	// diagnostics as "<FilePath>:<line>". It is never opened, parsed,
	// or matched against — purely a label.
	//
	// Conventions used by the loader (and expected by any caller):
	//   - Real on-disk files: an absolute path (so diagnostics don't
	//     depend on the compiler's CWD). Use the path the user typed
	//     where possible; don't normalize or symlink-resolve.
	//   - Sources with no real path (embedded FS, synthetic input):
	//     a "scheme:/path" form, e.g. "shipped:/data/libs/lodash.esc".
	//     The colon-prefix disambiguates from a real path.
	//   - Empty string is legal but renders as ":<line>" in diagnostics
	//     and should be avoided outside tests.
	FilePath string
	Span     ast.Span
}

// MemberKind discriminates the slot a Path addresses.
type MemberKind int

const (
	KindFree MemberKind = iota
	KindMethod
	KindGetter
	KindSetter
	KindProperty
	KindCtor
)

// Path is the structured address of a member, used for resolver
// queries and diagnostics. Mirrors the tree walk.
type Path struct {
	Module string               // "" for global
	Owner  dts_parser.QualIdent // nil for module-free / global top-level
	Name   dts_parser.PropertyKey
	Kind   MemberKind
	Static bool
}

// Resolve walks Modules by Path. Returns nil if no override applies.
func (s *OverrideStore) Resolve(p Path) *Effective {
	if s == nil {
		return nil
	}
	mod := s.Modules[p.Module]
	if mod == nil {
		return nil
	}

	container := &mod.Container
	var child *ChildScope
	if p.Owner != nil {
		child = walkChild(mod.Children, p.Owner)
		if child == nil {
			return nil
		}
		container = &child.Container
	}

	name := canonicalNameFromPK(p.Name)
	if p.Kind == KindFree {
		if container.Free == nil {
			return nil
		}
		return container.Free[name]
	}

	if child == nil {
		return nil
	}
	set := child.Instance
	if p.Static {
		set = child.Static
	}
	if set == nil {
		return nil
	}
	switch p.Kind {
	case KindMethod:
		return set.Methods[name]
	case KindGetter:
		return set.Getters[name]
	case KindSetter:
		return set.Setters[name]
	case KindProperty:
		return set.Properties[name]
	case KindCtor:
		return set.Ctor
	}
	return nil
}

// walkChild descends a Member chain through Container.Children. Returns
// nil if any segment doesn't resolve.
func walkChild(children map[string]*ChildScope, qi dts_parser.QualIdent) *ChildScope {
	segs := qualIdentSegments(qi)
	if len(segs) == 0 {
		return nil
	}
	var cur *ChildScope
	for i, seg := range segs {
		if i == 0 {
			cur = children[seg]
		} else {
			if cur == nil || cur.Children == nil {
				return nil
			}
			cur = cur.Children[seg]
		}
		if cur == nil {
			return nil
		}
	}
	return cur
}

// qualIdentSegments flattens a QualIdent into its identifier-path segments.
func qualIdentSegments(qi dts_parser.QualIdent) []string {
	switch q := qi.(type) {
	case *dts_parser.Ident:
		return []string{q.Name}
	case *dts_parser.Member:
		return append(qualIdentSegments(q.Left), q.Right.Name)
	}
	return nil
}

// canonicalNameFromPK is the single source of truth for canonicalising
// a member name into a string map key.
//
//   - Plain identifier `foo` → "foo".
//   - Qualified path `Symbol.iterator` → "[Symbol.iterator]".
//   - String literal `"foo bar"` → `["foo bar"]` (the inner quotes are
//     part of the key).
//   - Numeric literal `42` → "[42]".
func canonicalNameFromPK(key dts_parser.PropertyKey) string {
	switch k := key.(type) {
	case *dts_parser.Ident:
		return k.Name
	case *dts_parser.StringLiteral:
		return "[\"" + k.Value + "\"]"
	case *dts_parser.NumberLiteral:
		return "[" + strconv.FormatFloat(k.Value, 'g', -1, 64) + "]"
	case *dts_parser.ComputedKey:
		return "[" + computedKeyString(k.Expr) + "]"
	}
	return ""
}

// computedKeyString stringifies the expression inside a ComputedKey.
// Supports identifier and dotted-member paths (the two accepted forms
// per §2.2) and string literals.
func computedKeyString(e dts_parser.Expr) string {
	switch x := e.(type) {
	case *dts_parser.IdentExpr:
		return x.Name
	case *dts_parser.MemberExpr:
		return computedKeyString(x.Object) + "." + x.Prop.Name
	case *dts_parser.LitExpr:
		if s, ok := x.Lit.(*dts_parser.StringLiteral); ok {
			return "\"" + s.Value + "\""
		}
	}
	return ""
}

// CanonicalNameFromObjKey canonicalises an ast.ObjKey to the same string
// space as canonicalNameFromPK. Used by the shape extractor in
// extract.go when ingesting override .esc files.
func CanonicalNameFromObjKey(key ast.ObjKey) string {
	switch k := key.(type) {
	case *ast.IdentExpr:
		return k.Name
	case *ast.StrLit:
		return "[\"" + k.Value + "\"]"
	case *ast.NumLit:
		return "[" + strconv.FormatFloat(k.Value, 'g', -1, 64) + "]"
	case *ast.ComputedKey:
		return "[" + computedKeyStringFromAst(k.Expr) + "]"
	}
	return ""
}

func computedKeyStringFromAst(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.IdentExpr:
		return x.Name
	case *ast.MemberExpr:
		return computedKeyStringFromAst(x.Object) + "." + identNameFromAst(x.Prop)
	case *ast.LiteralExpr:
		if s, ok := x.Lit.(*ast.StrLit); ok {
			return "\"" + s.Value + "\""
		}
	}
	return ""
}

func identNameFromAst(n *ast.Ident) string {
	if n == nil {
		return ""
	}
	return n.Name
}

// pathForMember constructs a Path for the member in a ClassifyContext.
// Returns a zero Path if the member shape is one Classify doesn't query
// the store for (e.g. unsupported member type).
func pathForMember(ctx ClassifyContext) Path {
	if ctx.Member == nil {
		return Path{}
	}
	var owner dts_parser.QualIdent
	if ctx.ClassName != "" {
		owner = dts_parser.NewIdent(ctx.ClassName, ast.Span{})
	}
	p := Path{
		Module: ctx.ModulePath,
		Owner:  owner,
	}
	switch m := ctx.Member.(type) {
	case *dts_parser.MethodDecl:
		p.Name = m.Name
		p.Kind = KindMethod
		p.Static = m.Modifiers.Static
	case *dts_parser.GetterDecl:
		p.Name = m.Name
		p.Kind = KindGetter
		p.Static = m.Modifiers.Static
	case *dts_parser.SetterDecl:
		p.Name = m.Name
		p.Kind = KindSetter
		p.Static = m.Modifiers.Static
	case *dts_parser.PropertyDecl:
		p.Name = m.Name
		p.Kind = KindProperty
		p.Static = m.Modifiers.Static
	case *dts_parser.ConstructorDecl:
		p.Kind = KindCtor
	default:
		return Path{}
	}
	return p
}

// overrideToResult converts an override hit into a ClassifyResult.
// Receiver mutability is read off the override's *FuncType.SelfParam.
func overrideToResult(eff *Effective) ClassifyResult {
	mut := false
	if fn, ok := eff.Type.(*type_system.FuncType); ok {
		mut = type_system.ReceiverIsMut(fn)
	}
	return ClassifyResult{Mut: mut, Source: eff.Source}
}
