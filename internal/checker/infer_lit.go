package checker

import (
	"fmt"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/type_system"
)

func (c *Checker) inferLit(lit ast.Lit) (type_system.Type, []Error) {
	provenance := &ast.NodeProvenance{Node: lit}

	var t type_system.Type
	errors := []Error{}
	switch lit := lit.(type) {
	case *ast.StrLit:
		t = type_system.NewStrLitType(provenance, lit.Value)
	case *ast.NumLit:
		t = type_system.NewNumLitType(provenance, lit.Value)
	case *ast.BoolLit:
		t = type_system.NewBoolLitType(provenance, lit.Value)
	case *ast.RegexLit:
		// TODO: createa a separate type for regex literals
		t, _ = type_system.NewRegexTypeWithPatternString(provenance, lit.Value)
	case *ast.BigIntLit:
		t = type_system.NewBigIntLitType(provenance, lit.Value)
	case *ast.NullLit:
		t = type_system.NewNullType(provenance)
	case *ast.UndefinedLit:
		t = type_system.NewUndefinedType(provenance)
	default:
		panic(fmt.Sprintf("Unknown literal type: %T", lit))
	}

	return t, errors
}
