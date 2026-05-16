package interop

import (
	"strconv"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dts_parser"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// OverrideStore holds the post-merge module map. Per-tier pre-merge
// module maps exist only inside Build and are not retained — every
// diagnostic that needs provenance reads Effective.Origins, which
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
	Free     map[string]*Effective // free functions, vals, type aliases
	Children map[string]ChildScope // nested namespaces / classes / interfaces
	Origin   Origin                // declaring file for diagnostics
}

// ModuleScope is the per-module root.
type ModuleScope struct {
	Container
	AllPure bool // module-level @all_pure pragma
	// Tier is the OverrideTier that produced this ModuleScope's AllPure
	// flag. Meaningful only when AllPure is true.
	AllPureTier OverrideTier
}

//sumtype:decl

// ChildScope is one of {NamespaceScope, ClassScope, InterfaceScope} —
// the three shapes a Container.Children entry can take. Class/namespace
// declaration merging is not supported, so only NamespaceScope carries
// nested Free/Children; class and interface scopes carry only their
// MemberSet(s) and an Origin.
type ChildScope interface {
	isChildScope()
	// ChildOrigin is the declaration site used for diagnostics.
	ChildOrigin() Origin
	// MembersFor returns Instance (static=false) or Static (static=true)
	// for the variants that carry them: ClassScope has both; InterfaceScope
	// has only Instance; NamespaceScope has neither. Returns nil when the
	// requested side doesn't apply to the variant.
	MembersFor(static bool) *MemberSet
}

// NamespaceScope is a `namespace Foo { ... }` block. Its Container
// holds nested namespaces and free declarations; it has no instance
// or static members.
type NamespaceScope struct {
	Container Container
}

// ClassScope is a `class Foo { ... }` declaration. Instance carries
// non-static methods/getters/setters/properties + Ctor; Static carries
// the static side. Class+namespace declaration merging is not
// supported, so a ClassScope has no Free/Children — only an Origin.
type ClassScope struct {
	Origin   Origin
	Instance *MemberSet
	Static   *MemberSet
}

// InterfaceScope is an `interface Foo { ... }` declaration. Interfaces
// have no static side, so only Instance is populated. Like ClassScope,
// no namespace-merge side — just an Origin.
type InterfaceScope struct {
	Origin   Origin
	Instance *MemberSet
}

func (*NamespaceScope) isChildScope() {}
func (*ClassScope) isChildScope()     {}
func (*InterfaceScope) isChildScope() {}

func (s *NamespaceScope) ChildOrigin() Origin { return s.Container.Origin }
func (s *ClassScope) ChildOrigin() Origin     { return s.Origin }
func (s *InterfaceScope) ChildOrigin() Origin { return s.Origin }

// MembersFor: NamespaceScope has no instance or static side.
func (*NamespaceScope) MembersFor(bool) *MemberSet { return nil }

// MembersFor: ClassScope returns Instance (static=false) or Static (static=true).
func (s *ClassScope) MembersFor(static bool) *MemberSet {
	if static {
		return s.Static
	}
	return s.Instance
}

// MembersFor: InterfaceScope returns Instance for the instance side
// and nil for static (interfaces have no static).
func (s *InterfaceScope) MembersFor(static bool) *MemberSet {
	if static {
		return nil
	}
	return s.Instance
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
	OverrideTierBuiltin                         // requirements tier 4
)

// ResolutionTierFor maps an OverrideTier to the broader ResolutionTier
// the merge sets on Effective.Source.
func (t OverrideTier) ResolutionTierFor() ResolutionTier {
	switch t {
	case OverrideTierUserProject, OverrideTierUserDep:
		return TierUserOverride
	case OverrideTierBuiltin:
		return TierBuiltinOverride
	}
	return TierDefault
}

// Effective is the merged result for a single member. Receiver shape
// (no receiver / self / mut self) is encoded structurally on Type's
// *FuncType.SelfParam — callers use type_system.ReceiverIsMut.
type Effective struct {
	Type    type_system.Type
	Source  ResolutionTier
	Origins []Origin

	// Tier records the OverrideTier that produced this leaf in the
	// collapsed per-tier scope. Set by extract; consumed by merge to
	// derive Source. Not meaningful on Effectives after merge.
	Tier OverrideTier
}

// withTier returns a copy of e with Tier set to `ot` and Source derived
// from it. Returns nil when called on a nil receiver. Copies rather
// than mutating so the per-tier Effective produced by Extract is not
// shared across collapse runs.
func (e *Effective) withTier(tier OverrideTier) *Effective {
	if e == nil {
		return nil
	}
	cp := *e
	cp.Tier = tier
	cp.Source = tier.ResolutionTierFor()
	return &cp
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
	//     a "scheme:/path" form, e.g. "builtin:/data/libs/lodash.esc".
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

// withMember returns a copy of p with the member-addressing trio
// (Name, Kind, Static) set. Module and Owner — the navigation half of
// the Path — are unchanged. An empty `name` clears Name (used for
// Ctor, which has no name of its own).
func (p Path) withMember(name string, kind MemberKind, static bool) Path {
	if name != "" {
		p.Name = dts_parser.NewIdent(name, ast.Span{})
	} else {
		p.Name = nil
	}
	p.Kind = kind
	p.Static = static
	return p
}

// withChild returns a copy of p with `name` appended to the Owner
// chain — the breadcrumb of nested class/interface/namespace segments
// the merge recursion has descended through. Used when the merge enters
// a Container.Children[name] so leaves emitted beneath get a fully-
// qualified Path for diagnostics.
func (p Path) withChild(name string) Path {
	right := dts_parser.NewIdent(name, ast.Span{})
	if p.Owner == nil {
		p.Owner = right
	} else {
		p.Owner = &dts_parser.Member{Left: p.Owner, Right: right}
	}
	return p
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

	name := canonicalNameFromPK(p.Name)

	// Top-level of module: only KindFree is meaningful (member kinds
	// require a class/interface owner).
	if p.Owner == nil {
		if p.Kind == KindFree {
			return mod.Container.Free[name]
		}
		return nil
	}

	child := walkChild(mod.Children, p.Owner)
	if child == nil {
		return nil
	}

	if p.Kind == KindFree {
		// Free lookups can only land in a NamespaceScope — class and
		// interface scopes don't carry a Free side.
		ns, ok := child.(*NamespaceScope)
		if !ok {
			return nil
		}
		return ns.Container.Free[name]
	}

	// Member access (method/getter/setter/property/ctor): only class
	// and interface variants carry MemberSets; NamespaceScope returns
	// nil from MembersFor and falls through.
	set := child.MembersFor(p.Static)
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
// nil if any segment doesn't resolve. Only NamespaceScope can host
// nested children — descent through a class/interface mid-chain fails.
func walkChild(children map[string]ChildScope, qi dts_parser.QualIdent) ChildScope {
	segs := qualIdentSegments(qi)
	if len(segs) == 0 {
		return nil
	}
	var cur ChildScope
	for i, seg := range segs {
		if i == 0 {
			cur = children[seg]
		} else {
			ns, ok := cur.(*NamespaceScope)
			if !ok {
				return nil
			}
			cur = ns.Container.Children[seg]
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
