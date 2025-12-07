package checker

import (
	"fmt"
	"iter"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/type_system"
)

func Zip[T, U any](t []T, u []U) iter.Seq2[T, U] {
	return func(yield func(T, U) bool) {
		for i := range min(len(t), len(u)) { // range over int (Go 1.22)
			if !yield(t[i], u[i]) {
				return
			}
		}
	}
}

func patToPat(p ast.Pat) type_system.Pat {
	switch p := p.(type) {
	case *ast.IdentPat:
		return &type_system.IdentPat{Name: p.Name}
	case *ast.LitPat:
		panic("TODO: handle literal pattern")
		// return &LitPat{Lit: p.Lit}
	case *ast.TuplePat:
		elems := make([]type_system.Pat, len(p.Elems))
		for i, elem := range p.Elems {
			elems[i] = patToPat(elem)
		}
		return &type_system.TuplePat{Elems: elems}
	case *ast.ObjectPat:
		elems := make([]type_system.ObjPatElem, len(p.Elems))
		for i, elem := range p.Elems {
			switch elem := elem.(type) {
			case *ast.ObjKeyValuePat:
				elems[i] = &type_system.ObjKeyValuePat{
					Key:   elem.Key.Name,
					Value: patToPat(elem.Value),
				}
			case *ast.ObjShorthandPat:
				elems[i] = &type_system.ObjShorthandPat{
					Key: elem.Key.Name,
				}
			case *ast.ObjRestPat:
				elems[i] = &type_system.ObjRestPat{
					Pattern: patToPat(elem.Pattern),
				}
			default:
				panic("unknown object pattern element type")
			}
		}
		return &type_system.ObjectPat{Elems: elems}
	case *ast.ExtractorPat:
		args := make([]type_system.Pat, len(p.Args))
		for i, arg := range p.Args {
			args[i] = patToPat(arg)
		}
		return &type_system.ExtractorPat{Name: ast.QualIdentToString(p.Name), Args: args}
	case *ast.RestPat:
		return &type_system.RestPat{Pattern: patToPat(p.Pattern)}
	default:
		panic("unknown pattern type: " + fmt.Sprintf("%T", p))
	}
}

// Helper function to remove undefined from a union type
func removeUndefinedFromType(t type_system.Type) type_system.Type {
	if unionType, ok := type_system.Prune(t).(*type_system.UnionType); ok {
		nonUndefinedTypes := []type_system.Type{}
		for _, typ := range unionType.Types {
			if litType, ok := type_system.Prune(typ).(*type_system.LitType); ok {
				if _, isUndefined := litType.Lit.(*type_system.UndefinedLit); isUndefined {
					continue // Skip undefined
				}
			}
			nonUndefinedTypes = append(nonUndefinedTypes, typ)
		}
		if len(nonUndefinedTypes) == 0 {
			return type_system.NewNeverType(nil)
		}
		return type_system.NewUnionType(nil, nonUndefinedTypes...)
	}
	return t
}

func (c *Checker) getDefinedElems(unionType *type_system.UnionType) []type_system.Type {
	definedElems := []type_system.Type{}
	for _, elem := range unionType.Types {
		elem = type_system.Prune(elem)
		switch elem := elem.(type) {
		case *type_system.LitType:
			switch elem.Lit.(type) {
			case *type_system.NullLit:
				continue
			case *type_system.UndefinedLit:
				continue
			default:
				definedElems = append(definedElems, elem)
			}
		default:
			definedElems = append(definedElems, elem)
		}
	}

	return definedElems
}
