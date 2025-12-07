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

func (c *Checker) astKeyToTypeKey(ctx Context, key ast.ObjKey) (*type_system.ObjTypeKey, []Error) {
	switch key := key.(type) {
	case *ast.IdentExpr:
		newKey := type_system.NewStrKey(key.Name)
		return &newKey, nil
	case *ast.StrLit:
		newKey := type_system.NewStrKey(key.Value)
		return &newKey, nil
	case *ast.NumLit:
		newKey := type_system.NewNumKey(key.Value)
		return &newKey, nil
	case *ast.ComputedKey:
		// TODO: return the error
		keyType, _ := c.inferExpr(ctx, key.Expr) // infer the expression for side-effects

		switch t := type_system.Prune(keyType).(type) {
		case *type_system.LitType:
			switch lit := t.Lit.(type) {
			case *type_system.StrLit:
				newKey := type_system.NewStrKey(lit.Value)
				return &newKey, nil
			case *type_system.NumLit:
				newKey := type_system.NewNumKey(lit.Value)
				return &newKey, nil
			default:
				return nil, []Error{&InvalidObjectKeyError{Key: t, span: key.Span()}}
			}
		case *type_system.UniqueSymbolType:
			newKey := type_system.NewSymKey(t.Value)
			return &newKey, nil
		default:
			panic(&InvalidObjectKeyError{Key: t, span: key.Span()})
		}
	default:
		panic(fmt.Sprintf("Unknown object key type: %T", key))
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

// resolveQualifiedTypeAlias resolves a qualified type name by traversing namespace hierarchy
func resolveQualifiedTypeAlias(ctx Context, qualIdent type_system.QualIdent) *type_system.TypeAlias {
	switch qi := qualIdent.(type) {
	case *type_system.Ident:
		// Simple identifier, use existing scope lookup
		return ctx.Scope.getTypeAlias(qi.Name)
	case *type_system.Member:
		// Qualified identifier like A.B.Type
		// First resolve the left part (A.B)
		leftNamespace := resolveQualifiedNamespace(ctx, qi.Left)
		if leftNamespace == nil {
			return nil
		}
		// Then look for the type in the resolved namespace
		if typeAlias, ok := leftNamespace.Types[qi.Right.Name]; ok {
			return typeAlias
		}
		return nil
	default:
		return nil
	}
}

func resolveQualifiedValue(ctx Context, qualIdent type_system.QualIdent) *type_system.Binding {
	switch qi := qualIdent.(type) {
	case *type_system.Ident:
		// Simple identifier, use existing scope lookup
		return ctx.Scope.GetValue(qi.Name)
	case *type_system.Member:
		// Qualified identifier like A.B.Type
		// First resolve the left part (A.B)
		leftNamespace := resolveQualifiedNamespace(ctx, qi.Left)
		if leftNamespace == nil {
			return nil
		}
		// Then look for the type in the resolved namespace
		if binding, ok := leftNamespace.Values[qi.Right.Name]; ok {
			return binding
		}
		return nil
	default:
		return nil
	}
}

// resolveQualifiedNamespace resolves a qualified identifier to a namespace
func resolveQualifiedNamespace(ctx Context, qualIdent type_system.QualIdent) *type_system.Namespace {
	switch qi := qualIdent.(type) {
	case *type_system.Ident:
		// Simple identifier, check if it's a namespace
		return ctx.Scope.getNamespace(qi.Name)
	case *type_system.Member:
		// Qualified identifier like A.B
		// First resolve the left part
		leftNamespace := resolveQualifiedNamespace(ctx, qi.Left)
		if leftNamespace == nil {
			return nil
		}
		// Then look for the right part in the resolved namespace
		if namespace, ok := leftNamespace.Namespaces[qi.Right.Name]; ok {
			return namespace
		}
		return nil
	default:
		return nil
	}
}

func convertQualIdent(astIdent ast.QualIdent) type_system.QualIdent {
	switch id := astIdent.(type) {
	case *ast.Ident:
		return type_system.NewIdent(id.Name)
	case *ast.Member:
		left := convertQualIdent(id.Left)
		right := type_system.NewIdent(id.Right.Name)
		return &type_system.Member{Left: left, Right: right}
	default:
		panic(fmt.Sprintf("Unknown QualIdent type: %T", astIdent))
	}
}
