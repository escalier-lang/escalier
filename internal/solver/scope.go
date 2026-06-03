package solver

import (
	"github.com/escalier-lang/escalier/internal/provenance"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// ValueBinding is a name's value-sort binding. In M2 it holds a plain
// MONOMORPHIC soltype.Type — M1 ships no schemes, so there is no generalization
// yet. M3 swaps Type for a TypeScheme when let-generalization lands (open
// question §7 (a): take the mechanical field-swap churn then rather than
// over-abstract now).
type ValueBinding struct {
	Type   soltype.Type          // M2: monomorphic. M3 replaces with a TypeScheme.
	Source provenance.Provenance // the introducing VarDecl/FuncDecl/param; nil for prelude
}

// TypeBinding is a name's type-sort binding. M2 only ever populates this with
// the hand-seeded stdlib-type placeholders (§3.8); real type aliases/classes are
// M3+. The shape lands now because it is load-bearing for the two-map test
// harness.
type TypeBinding struct {
	Type   soltype.Type
	Source provenance.Provenance
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
