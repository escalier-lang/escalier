package checker

import (
	"fmt"
	"os"

	"github.com/escalier-lang/escalier/internal/type_system"
)

type Scope struct {
	Parent    *Scope // optional, parent is nil for the root scope
	Namespace *type_system.Namespace
	// Lifetimes maps a user-written lifetime name (without the leading
	// tick) to the LifetimeVar allocated for it on the enclosing
	// function signature. Populated by inferFuncSig / inferFuncTypeAnn
	// so that inline `'a` references inside parameter or return type
	// annotations can be resolved to the same LifetimeVar that appears
	// in FuncType.LifetimeParams.
	Lifetimes map[string]*type_system.LifetimeVar
}

func NewScope() *Scope {
	return &Scope{
		Parent:    nil,
		Namespace: type_system.NewNamespace(),
	}
}

func (s *Scope) WithNewScope() *Scope {
	return &Scope{
		Parent:    s,
		Namespace: type_system.NewNamespace(),
	}
}

func (s *Scope) WithNewScopeAndNamespace(ns *type_system.Namespace) *Scope {
	return &Scope{
		Parent:    s,
		Namespace: ns,
	}
}

// GetLifetimeVar resolves a lifetime name (e.g. "a" for `'a`) to the
// LifetimeVar in the nearest enclosing scope that declared it.
func (s *Scope) GetLifetimeVar(name string) *type_system.LifetimeVar {
	if s.Lifetimes != nil {
		if lv, ok := s.Lifetimes[name]; ok {
			return lv
		}
	}
	if s.Parent != nil {
		return s.Parent.GetLifetimeVar(name)
	}
	return nil
}

// SetLifetimeVar registers a LifetimeVar under the user-written name in
// this scope. Subsequent type-annotation inference inside the same
// function context resolves `'name` references to this var.
func (s *Scope) SetLifetimeVar(name string, lv *type_system.LifetimeVar) {
	if s.Lifetimes == nil {
		s.Lifetimes = map[string]*type_system.LifetimeVar{}
	}
	s.Lifetimes[name] = lv
}

func (s *Scope) GetValue(name string) *type_system.Binding {
	if v, ok := s.Namespace.Values[name]; ok {
		return v
	}
	if s.Parent != nil {
		return s.Parent.GetValue(name)
	}
	return nil
}

func (s *Scope) setValue(name string, binding *type_system.Binding) {
	if _, ok := s.Namespace.Values[name]; ok {
		fmt.Fprintf(os.Stderr, "DEBUG: value %s already exists in current scope\n", name)
		panic("value already exists")
	}
	s.Namespace.Values[name] = binding
}

func (s *Scope) getNamespace(name string) *type_system.Namespace {
	if v, ok := s.Namespace.GetNamespace(name); ok {
		return v
	}
	if s.Parent != nil {
		return s.Parent.getNamespace(name)
	}
	return nil
}

func (s *Scope) setNamespace(name string, namespace *type_system.Namespace) {
	if _, ok := s.Namespace.GetNamespace(name); ok {
		panic("namespace already exists")
	}
	if err := s.Namespace.SetNamespace(name, namespace); err != nil {
		panic(err)
	}
}

func (s *Scope) GetTypeAlias(name string) *type_system.TypeAlias {
	if v, ok := s.Namespace.Types[name]; ok {
		return v
	}
	if s.Parent != nil {
		return s.Parent.GetTypeAlias(name)
	}
	return nil
}

func (s *Scope) SetTypeAlias(name string, alias *type_system.TypeAlias) {
	if _, ok := s.Namespace.Types[name]; ok {
		panic(fmt.Sprintf("type alias already exists: %s", name))
	}
	s.Namespace.Types[name] = alias
}
