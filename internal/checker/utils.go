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
		// Qualified identifier like A.B.C
		// First resolve the left part (A.B)
		leftNamespace := resolveQualifiedNamespace(ctx, qi.Left)
		if leftNamespace == nil {
			return nil
		}
		// Then look for the remaining identifier in the resolved namespace
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

func (c *Checker) validateTypeParams(
	ctx Context,
	existingParams []*type_system.TypeParam,
	newParams []*type_system.TypeParam,
	interfaceName string,
	span ast.Span,
) []Error {
	errors := []Error{}

	// Check if the number of type parameters match
	if len(existingParams) != len(newParams) {
		errors = append(errors, &TypeParamMismatchError{
			InterfaceName: interfaceName,
			ExistingCount: len(existingParams),
			NewCount:      len(newParams),
			message:       fmt.Sprintf("Interface '%s' has %d type parameter(s) but was previously declared with %d type parameter(s)", interfaceName, len(newParams), len(existingParams)),
			span:          span,
		})
		return errors
	}

	// Check each type parameter
	for i := range existingParams {
		existing := existingParams[i]
		new := newParams[i]

		// Check if names match
		if existing.Name != new.Name {
			errors = append(errors, &TypeParamMismatchError{
				InterfaceName: interfaceName,
				message:       fmt.Sprintf("Type parameter at position %d has name '%s' but was previously declared with name '%s'", i, new.Name, existing.Name),
				span:          span,
			})
		}

		// Check if constraints match
		if (existing.Constraint == nil) != (new.Constraint == nil) {
			errors = append(errors, &TypeParamMismatchError{
				InterfaceName: interfaceName,
				message:       fmt.Sprintf("Type parameter '%s' constraint mismatch in interface '%s'", new.Name, interfaceName),
				span:          span,
			})
		} else if existing.Constraint != nil && new.Constraint != nil {
			// Both have constraints, check if they're compatible
			unifyErrors := c.Unify(ctx, existing.Constraint, new.Constraint)
			if len(unifyErrors) > 0 {
				errors = append(errors, &TypeParamMismatchError{
					InterfaceName: interfaceName,
					message:       fmt.Sprintf("Type parameter '%s' has incompatible constraint in interface '%s'", new.Name, interfaceName),
					span:          span,
				})
			}
		}

		// Check if defaults match
		if (existing.Default == nil) != (new.Default == nil) {
			errors = append(errors, &TypeParamMismatchError{
				InterfaceName: interfaceName,
				message:       fmt.Sprintf("Type parameter '%s' default mismatch in interface '%s'", new.Name, interfaceName),
				span:          span,
			})
		} else if existing.Default != nil && new.Default != nil {
			// Both have defaults, check if they're compatible
			unifyErrors := c.Unify(ctx, existing.Default, new.Default)
			if len(unifyErrors) > 0 {
				errors = append(errors, &TypeParamMismatchError{
					InterfaceName: interfaceName,
					message:       fmt.Sprintf("Type parameter '%s' has incompatible default in interface '%s'", new.Name, interfaceName),
					span:          span,
				})
			}
		}
	}

	return errors
}

// extractTypeParamRefsFromTypeAnn extracts all type parameter names referenced in a type annotation
func extractTypeParamRefsFromTypeAnn(typeAnn ast.TypeAnn, availableTypeParams map[string]bool) []string {
	if typeAnn == nil {
		return nil
	}

	refs := make(map[string]bool)
	var visitor typeParamRefCollector
	visitor.availableTypeParams = availableTypeParams
	visitor.refs = refs
	typeAnn.Accept(&visitor)

	// Convert map to slice
	result := make([]string, 0, len(refs))
	for ref := range refs {
		result = append(result, ref)
	}
	return result
}

// typeParamRefCollector is a visitor that collects type parameter references
type typeParamRefCollector struct {
	ast.DefaultVisitor
	availableTypeParams map[string]bool
	refs                map[string]bool
}

func (v *typeParamRefCollector) EnterTypeAnn(typeAnn ast.TypeAnn) bool {
	if ref, ok := typeAnn.(*ast.TypeRefTypeAnn); ok {
		// Check if this is a simple identifier (not qualified)
		if ident, ok := ref.Name.(*ast.Ident); ok {
			name := ident.Name
			if v.availableTypeParams[name] {
				v.refs[name] = true
			}
		}
	}
	return true
}

// sortTypeParamsTopologically sorts type parameters so that dependencies come before dependents
func sortTypeParamsTopologically(typeParams []*ast.TypeParam) []*ast.TypeParam {
	if len(typeParams) <= 1 {
		return typeParams
	}

	// Build a map of type parameter names for quick lookup
	typeParamNames := make(map[string]bool)
	for _, tp := range typeParams {
		typeParamNames[tp.Name] = true
	}

	// Build dependency graph: map from type param name to list of type params it depends on
	deps := make(map[string][]string)
	for _, tp := range typeParams {
		var tpDeps []string
		if tp.Constraint != nil {
			tpDeps = append(tpDeps, extractTypeParamRefsFromTypeAnn(tp.Constraint, typeParamNames)...)
		}
		if tp.Default != nil {
			tpDeps = append(tpDeps, extractTypeParamRefsFromTypeAnn(tp.Default, typeParamNames)...)
		}
		deps[tp.Name] = tpDeps
	}

	// Perform topological sort using DFS
	sorted := make([]*ast.TypeParam, 0, len(typeParams))
	visited := make(map[string]bool)
	visiting := make(map[string]bool) // For cycle detection

	var visit func(string) bool
	visit = func(name string) bool {
		if visited[name] {
			return true // Already processed
		}
		if visiting[name] {
			// Cycle detected - return false to indicate we should keep original order
			return false
		}

		visiting[name] = true
		for _, depName := range deps[name] {
			if !visit(depName) {
				return false
			}
		}
		visiting[name] = false
		visited[name] = true

		// Find the type param and add it to sorted list
		for _, tp := range typeParams {
			if tp.Name == name {
				sorted = append(sorted, tp)
				break
			}
		}
		return true
	}

	// Visit all type parameters
	for _, tp := range typeParams {
		if !visited[tp.Name] {
			if !visit(tp.Name) {
				// Cycle detected, return original order
				return typeParams
			}
		}
	}

	return sorted
}
