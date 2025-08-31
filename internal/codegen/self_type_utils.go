package codegen

import (
	"github.com/escalier-lang/escalier/internal/type_system"
)

// Ensure selfTypeRefVisitor implements TypeVisitor by adding ExitType.
func (v *selfTypeRefVisitor) ExitType(t type_system.Type) type_system.Type {
	return nil
}

// containsSelfTypeRef checks if a type contains a TypeRefType with name 'Self' using TypeVisitor.
func containsSelfTypeRef(t type_system.Type) bool {
	visitor := &selfTypeRefVisitor{found: false}
	t.Accept(visitor)
	return visitor.found
}

type selfTypeRefVisitor struct {
	found bool
}

func (v *selfTypeRefVisitor) EnterType(t type_system.Type) type_system.Type {
	if tref, ok := type_system.Prune(t).(*type_system.TypeRefType); ok {
		if tref.Name == "Self" {
			v.found = true
		}
	}
	return nil // continue traversal
}

// replaceSelfWithThis returns a copy of the type with all TypeRefType 'Self' replaced by a TypeScript 'this' type using TypeVisitor.
func replaceSelfWithThis(t type_system.Type) type_system.Type {
	visitor := &selfReplaceVisitor{}
	return t.Accept(visitor)
}

type selfReplaceVisitor struct{}

func (v *selfReplaceVisitor) EnterType(t type_system.Type) type_system.Type {
	return nil // continue traversal
}

func (v *selfReplaceVisitor) ExitType(t type_system.Type) type_system.Type {
	if tref, ok := type_system.Prune(t).(*type_system.TypeRefType); ok {
		if tref.Name == "Self" {
			return &type_system.TypeRefType{Name: "this", TypeAlias: nil, TypeArgs: nil}
			// return &type_system.GlobalThisType{}
		}
	}
	return nil
}
