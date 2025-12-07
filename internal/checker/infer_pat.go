package checker

import (
	"slices"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/type_system"
)

func (c *Checker) inferPattern(
	ctx Context,
	pattern ast.Pat,
) (type_system.Type, map[string]*type_system.Binding, []Error) {

	bindings := map[string]*type_system.Binding{}
	var inferPatRec func(ast.Pat) (type_system.Type, []Error)

	inferPatRec = func(pat ast.Pat) (type_system.Type, []Error) {
		var t type_system.Type
		var errors []Error
		provenance := &ast.NodeProvenance{Node: pat}

		switch p := pat.(type) {
		case *ast.IdentPat:
			if p.TypeAnn != nil {
				// TODO: check if there's a default value, infer it, and unify
				// it with the type annotation.
				t, errors = c.inferTypeAnn(ctx, p.TypeAnn)
			} else {
				tvar := c.FreshVar(provenance)
				tvar.FromBinding = true
				if p.Default != nil {
					defaultType, defaultErrors := c.inferExpr(ctx, p.Default)
					errors = append(errors, defaultErrors...)
					tvar.Default = defaultType
				}
				t = tvar
			}

			// TODO: report an error if the name is already bound
			bindings[p.Name] = &type_system.Binding{
				Source:  provenance,
				Type:    t,
				Mutable: false, // TODO
			}
		case *ast.LitPat:
			t, errors = c.inferLit(p.Lit)
		case *ast.TuplePat:
			elems := make([]type_system.Type, len(p.Elems))
			for i, elem := range p.Elems {
				elemType, elemErrors := inferPatRec(elem)
				elems[i] = elemType
				errors = append(errors, elemErrors...)
			}
			t = type_system.NewTupleType(provenance, elems...)
		case *ast.ObjectPat:
			elems := []type_system.ObjTypeElem{}
			for _, elem := range p.Elems {
				switch elem := elem.(type) {
				case *ast.ObjKeyValuePat:
					t, elemErrors := inferPatRec(elem.Value)
					errors = append(errors, elemErrors...)
					name := type_system.NewStrKey(elem.Key.Name)
					prop := type_system.NewPropertyElem(name, t)
					prop.Optional = false
					elems = append(elems, prop)
				case *ast.ObjShorthandPat:
					// We can't infer the type of the shorthand pattern yet, so
					// we use a fresh type variable.
					var t type_system.Type
					if elem.TypeAnn != nil {
						// TODO: check if there's a default value, infer it, and unify
						// it with the type annotation.
						elemType, elemErrors := c.inferTypeAnn(ctx, elem.TypeAnn)
						t = elemType
						errors = append(errors, elemErrors...)
					} else {
						tvar := c.FreshVar(&ast.NodeProvenance{Node: elem})
						tvar.FromBinding = true
						if elem.Default != nil {
							defaultType, defaultErrors := c.inferExpr(ctx, elem.Default)
							errors = append(errors, defaultErrors...)
							tvar.Default = defaultType
						}
						t = tvar
					}
					name := type_system.NewStrKey(elem.Key.Name)
					// TODO: report an error if the name is already bound
					bindings[elem.Key.Name] = &type_system.Binding{
						Source:  &ast.NodeProvenance{Node: elem.Key},
						Type:    t,
						Mutable: false, // TODO
					}
					prop := type_system.NewPropertyElem(name, t)
					elems = append(elems, prop)
				case *ast.ObjRestPat:
					t, restErrors := inferPatRec(elem.Pattern)
					errors = slices.Concat(errors, restErrors)
					elems = append(elems, type_system.NewRestSpreadElem(t))
				}
			}
			t = type_system.NewObjectType(provenance, elems)
		case *ast.ExtractorPat:
			if binding := resolveQualifiedValue(ctx, convertQualIdent(p.Name)); binding != nil {
				args := make([]type_system.Type, len(p.Args))
				for i, arg := range p.Args {
					argType, argErrors := inferPatRec(arg)
					args[i] = argType
					errors = append(errors, argErrors...)
				}
				t = type_system.NewExtractorType(provenance, binding.Type, args...)
			} else {
				// TODO: generate an error for unresolved identifier
				t = type_system.NewNeverType(nil)
			}
		case *ast.InstancePat:
			patType, patBindings, patErrors := c.inferPattern(ctx, p.Object)
			typeAlias := resolveQualifiedTypeAlias(ctx, convertQualIdent(p.ClassName))

			for name, binding := range patBindings {
				bindings[name] = binding
			}

			typeAliasType := type_system.Prune(typeAlias.Type)

			if clsType, ok := typeAliasType.(*type_system.ObjectType); ok {
				if patType, ok := type_system.Prune(patType).(*type_system.ObjectType); ok {
					// We know that the object type inferred from this pattern
					// must be an instance of the class type, so we set the ID
					// of the pattern type to be the same as the class type.
					// Without this, the unify call below would fail because
					// an object type without a matching ID is not assignable
					// to an object type with a non-zero ID.
					patType.Nominal = true
					patType.ID = clsType.ID
				}
			}

			unifyErrors := c.Unify(ctx, patType, typeAliasType)

			errors = append(errors, patErrors...)
			errors = append(errors, unifyErrors...)

			t = typeAliasType
		case *ast.RestPat:
			argType, argErrors := inferPatRec(p.Pattern)
			errors = append(errors, argErrors...)
			t = type_system.NewRestSpreadType(provenance, argType)
		case *ast.WildcardPat:
			t = c.FreshVar(&ast.NodeProvenance{Node: pat})
			errors = []Error{}
		}

		t.SetProvenance(provenance)
		pat.SetInferredType(t)
		return t, errors
	}

	t, errors := inferPatRec(pattern)
	t.SetProvenance(&ast.NodeProvenance{
		Node: pattern,
	})
	pattern.SetInferredType(t)

	return t, bindings, errors
}
