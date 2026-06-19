package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/provenance"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// ValueBinding is a name's value-sort binding. M3 (PR1) replaces M2's plain
// MONOMORPHIC soltype.Type with a *slice* of TypeSchemes: exactly one for an
// ordinary value (the let-generalization swap), and — once PR6 lands — more than
// one for an overload set, in declaration order. Holding the slice now (rather
// than a nullable OverloadSet field) makes "is the scheme nil when overloaded?"
// unrepresentable: cardinality is just len(Schemes), and Schemes[i] lines up with
// the arm at Sources[i].
type ValueBinding struct {
	Schemes []TypeScheme // 1 = ordinary binding; >1 = overload set (PR6)
	// Sources holds every decl that contributes to this binding, in source order:
	// for a plain `val`/`fn` that is the single introducing decl, but a name with
	// multiple top-level FuncDecls (overloads) — or an erroneous redeclaration —
	// accumulates one entry per arm, including arms M2 currently rejects. This lets
	// a future go-to-definition navigate to all of them. Empty for prelude bindings.
	Sources []provenance.Provenance
	// Kind is the binding's declaration kind: VarKind for a reassignable `var`,
	// ValKind for everything else (a `val`, a function, a parameter, and every
	// prelude binding — all left at the zero value, ValKind). inferAssign gates
	// reassignment on Kind == VarKind, reporting CannotAssignToImmutableError for any
	// other kind. This is the binding-level REASSIGNABILITY gate only — deliberately
	// SEPARATE from type-level mutability (`mut`-field / aliasing / lifetime
	// transitions, M4), which is a property of the TYPE, not of this binding.
	Kind ast.VariableKind
	// ModuleLevel marks a top-level binding — one defined directly in the module
	// scope, as opposed to a function parameter or body-local `val`/`var`. It is set
	// only by inferComponent's phase-3 definitions, the module's SCC bindings, and
	// left false for every nested binding. inferAssign reads it to recognise a GLOBAL
	// WRITE: storing a value into module-level storage outlives every borrow region,
	// so a borrowed value written there escapes to 'static (M4 D3).
	ModuleLevel bool
	// VarID is the liveness VarID assigned to this binding's name by the function
	// body's rename pass (M4 G1), or 0 for a binding outside any liveness-analysed
	// body (every top-level binding, and any binding minted before its enclosing
	// body's prepass runs). The mutability-transition checker reads it to resolve a
	// captured outer variable back to its alias set — the new-checker analogue of the
	// old checker's type_system.Binding.VarID. It is metadata for transition checking
	// only and never participates in type inference.
	VarID int
}

// IsOverloaded reports whether this binding is an overload set. Consumers MUST
// check it before routing: an ordinary call (false) keeps M2's shipped
// subtype-constraint path, while an overloaded call (true) goes through
// resolveOverload (PR6). inferIdent's value-position lookup branches on it too.
// PR1 only ever builds single-scheme bindings, so this is always false today; the
// helper lands now so the ordinary path reads !b.IsOverloaded() from the start.
func (b ValueBinding) IsOverloaded() bool { return len(b.Schemes) > 1 }

// TypeBinding is a name's type-sort binding. M2 only ever populates this with
// the hand-seeded stdlib-type placeholders (§3.8); real type aliases/classes are
// M3+. The shape lands now because it is load-bearing for the two-map test
// harness.
type TypeBinding struct {
	Type soltype.Type
	// Sources holds every decl that contributes to this type binding, in source
	// order — mirroring ValueBinding.Sources. A plain `type`/`class` has a single
	// entry; interface declaration-merging (multiple `interface T` under one name)
	// accumulates one per arm. Empty for the prelude type placeholders. Not yet
	// populated — M2 only seeds stdlib-type placeholders here; real type aliases
	// and classes (and their merging) land in M3+.
	Sources []provenance.Provenance
}

// Namespace is the third binding sort — a separate kind from a soltype.Type, so
// a namespace never flows as a value. M2 keeps the structure (for qualified
// BindingKey resolution across files) and the free-floating
// NamespaceUsedAsValueError; the member-access *lookup* (Foo.bar) is M4.
type Namespace struct {
	Name   string // qualified, from dep_graph.GetNamespace
	Values map[string]ValueBinding
	Types  map[string]TypeBinding
	Nested map[string]*Namespace
}

// Scope is the package-owned, multi-sorted analogue of type_system's scope (the
// milestone forbids reusing type_system's). It has one map per binding sort plus
// a parent link for lexical lookup.
//
// Why namespaces are stored by pointer while values/types are stored by value:
// the distinction is by *kind*, not by slot. A Namespace is a shared, mutable,
// recursive container — keyed by its qualified dep_graph BindingKey, referenced
// from several scopes/files (so a pointer gives it one identity), populated
// incrementally after insertion (a struct value in a map can't be field-mutated
// in place), and recursive (Namespace.Nested is also *Namespace). A
// ValueBinding/TypeBinding, by contrast, is a tiny identity-free record that is
// replaced wholesale — defineValue is a plain map overwrite, and redeclaration
// and the rec-group rebind both swap the entry rather than mutate it — so
// storing it by value avoids a per-binding allocation and a nil case for no
// loss. (Namespace.Values/Types follow the same value rule; Namespace.Nested
// follows the same pointer rule.)
type Scope struct {
	values     map[string]ValueBinding
	types      map[string]TypeBinding
	namespaces map[string]*Namespace
	parent     *Scope
}

// NewScope returns an empty root scope (no parent).
func NewScope() *Scope {
	return &Scope{
		values:     map[string]ValueBinding{},
		types:      map[string]TypeBinding{},
		namespaces: map[string]*Namespace{},
	}
}

// Child returns a fresh scope whose parent is s, for entering a nested lexical
// region (a function body, a block).
func (s *Scope) Child() *Scope {
	child := NewScope()
	child.parent = s
	return child
}

// defineValue inserts b under name in THIS scope's value map. It OVERWRITES any
// existing binding for name in this scope — it does not panic or error on a
// duplicate the way the old checker's setValue does. Two M2 paths rely on the
// overwrite: body-level variable redeclaration (§3.2) and inferComponent, which
// binds each rec-group name twice (fresh var, then coalesced type) (§3.7).
func (s *Scope) defineValue(name string, b ValueBinding) {
	s.values[name] = b
}

// removeValue deletes name from THIS scope's value map (a no-op if absent). The
// SCC driver uses it to retract a value binding it pre-bound to a fresh var but
// whose declaration then failed to produce a definition, so the mutation goes
// through Scope's API rather than touching the map directly.
func (s *Scope) removeValue(name string) {
	delete(s.values, name)
}

// defineType inserts b under name in this scope's type map (overwrite, as above).
func (s *Scope) defineType(name string, b TypeBinding) {
	s.types[name] = b
}

// defineNamespace inserts ns under name in this scope's namespace map.
func (s *Scope) defineNamespace(name string, ns *Namespace) {
	s.namespaces[name] = ns
}

// GetValue resolves name in the value sort by lexical lookup: this scope's own
// map, then up the parent chain. The comma-ok form makes the not-found case
// explicit at every call site.
func (s *Scope) GetValue(name string) (ValueBinding, bool) {
	for cur := s; cur != nil; cur = cur.parent {
		if b, ok := cur.values[name]; ok {
			return b, true
		}
	}
	return ValueBinding{}, false
}

// GetType resolves name in the type sort by the same lexical walk.
func (s *Scope) GetType(name string) (TypeBinding, bool) {
	for cur := s; cur != nil; cur = cur.parent {
		if b, ok := cur.types[name]; ok {
			return b, true
		}
	}
	return TypeBinding{}, false
}

// GetNamespace resolves name in the namespace sort by the same lexical walk.
func (s *Scope) GetNamespace(name string) (*Namespace, bool) {
	for cur := s; cur != nil; cur = cur.parent {
		if ns, ok := cur.namespaces[name]; ok {
			return ns, true
		}
	}
	return nil, false
}
