package checker

import (
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dep_graph"
	"github.com/escalier-lang/escalier/internal/type_system"
)

func getPatternNames(pattern ast.Pat) []string {
	// Collect all identifiers that are bound by the pattern.
	// This mirrors the logic of BindingVisitor but returns a slice of names.
	namesSet := make(map[string]struct{})
	var collect func(ast.Pat)
	collect = func(pat ast.Pat) {
		switch p := pat.(type) {
		case *ast.IdentPat:
			namesSet[p.Name] = struct{}{}
		case *ast.ObjectPat:
			for _, elem := range p.Elems {
				switch e := elem.(type) {
				case *ast.ObjShorthandPat:
					namesSet[e.Key.Name] = struct{}{}
				case *ast.ObjKeyValuePat:
					collect(e.Value)
				case *ast.ObjRestPat:
					collect(e.Pattern)
				}
			}
		case *ast.TuplePat:
			for _, sub := range p.Elems {
				collect(sub)
			}
		case *ast.ExtractorPat:
			for _, arg := range p.Args {
				collect(arg)
			}
		case *ast.InstancePat:
			collect(p.Object)
		case *ast.RestPat:
			collect(p.Pattern)
			// WildcardPat, LitPat, etc. do not introduce bindings.
		}
	}
	collect(pattern)

	// Convert set to slice.
	names := make([]string, 0, len(namesSet))
	for n := range namesSet {
		names = append(names, n)
	}
	// Ensure deterministic order.
	// Sorting requires the sort package.
	// (Import added at top of file.)
	sort.Strings(names)
	return names
}

func getDeclIdentifier(decl ast.Decl) string {
	switch d := decl.(type) {
	case *ast.FuncDecl:
		return d.Name.Name
	case *ast.VarDecl:
		names := getPatternNames(d.Pattern)
		return strings.Join(names, ",")
	case *ast.TypeDecl:
		return d.Name.Name
	case *ast.InterfaceDecl:
		return d.Name.Name
	case *ast.EnumDecl:
		return d.Name.Name
	default:
		return ""
	}
}

const DEBUG = false

// A module can contain declarations from mutliple source files.
// The order of the declarations doesn't matter because we compute the dependency
// graph and codegen will ensure that the declarations are emitted in the correct
// order.
// TODO: all interface declarations in a namespace to shadow previous ones.
func (c *Checker) InferModule(ctx Context, m *ast.Module) []Error {
	depGraph := dep_graph.BuildDepGraph(m)

	// print out all of the dependencies in depGraph for debugging
	if DEBUG {
		for _, key := range depGraph.AllBindings() {
			decls := depGraph.GetDecls(key)
			deps := depGraph.GetDeps(key)
			fmt.Fprintf(os.Stderr, "Binding: %s, Decls: [", key)
			for _, decl := range decls {
				fmt.Fprintf(os.Stderr, "%s, ", getDeclIdentifier(decl))
			}
			fmt.Fprintf(os.Stderr, "], Deps: [")
			iter := deps.Iter()
			for ok := iter.First(); ok; ok = iter.Next() {
				fmt.Fprintf(os.Stderr, "%s, ", iter.Key())
			}
			fmt.Fprintf(os.Stderr, "]\n")
		}
	}

	return c.InferDepGraph(ctx, depGraph)
}

// inferTypeParams infers type parameters from AST type parameters by creating
// fresh type variables for constraints and defaults.
//
// This helper is intended for module-level type declarations such as TypeDecl,
// ClassDecl, EnumDecl, and InterfaceDecl. It only mirrors the AST type parameter
// list into a corresponding slice of *type_system.TypeParam by allocating fresh
// type variables for any constraint and default types.
//
// Note that this function:
//   - does NOT add the inferred type parameters to any scope,
//   - does NOT perform any constraint checking or error reporting, and
//   - is NOT a replacement for inferFuncTypeParams, which is responsible for
//     function-level generic parameter handling and associated diagnostics.
func (c *Checker) inferTypeParams(astTypeParams []*ast.TypeParam) []*type_system.TypeParam {
	// Sort type parameters topologically so dependencies come first
	sortedTypeParams := ast.SortTypeParamsTopologically(astTypeParams)

	typeParams := make([]*type_system.TypeParam, len(sortedTypeParams))
	for i, typeParam := range sortedTypeParams {
		var constraintType type_system.Type
		var defaultType type_system.Type
		if typeParam.Constraint != nil {
			constraintType = c.FreshVar(&ast.NodeProvenance{Node: typeParam.Constraint})
		}
		if typeParam.Default != nil {
			defaultType = c.FreshVar(&ast.NodeProvenance{Node: typeParam.Default})
		}
		typeParams[i] = &type_system.TypeParam{
			Name:       typeParam.Name,
			Constraint: constraintType,
			Default:    defaultType,
		}
	}
	return typeParams
}

// unifyTypeParams unifies the placeholder type parameters (with FreshVar constraints/defaults)
// with the fully inferred type parameters (with resolved constraint/default types).
func (c *Checker) unifyTypeParams(
	ctx Context,
	existingTypeParams []*type_system.TypeParam,
	inferredTypeParams []*type_system.TypeParam,
) []Error {
	errors := []Error{}

	for i, existingTypeParam := range existingTypeParams {
		if i >= len(inferredTypeParams) {
			break // Safety check in case of mismatched lengths
		}
		inferredTypeParam := inferredTypeParams[i]

		if existingTypeParam.Constraint != nil && inferredTypeParam.Constraint != nil {
			constraintErrors := c.Unify(ctx, existingTypeParam.Constraint, inferredTypeParam.Constraint)
			errors = slices.Concat(errors, constraintErrors)
		}
		if existingTypeParam.Default != nil && inferredTypeParam.Default != nil {
			defaultErrors := c.Unify(ctx, existingTypeParam.Default, inferredTypeParam.Default)
			errors = slices.Concat(errors, defaultErrors)
		}
	}

	return errors
}

// validateInterfaceMerge checks that when merging interface declarations,
// properties with the same name have compatible (identical) types as required by TypeScript.
func (c *Checker) validateInterfaceMerge(
	ctx Context,
	existingInterface *type_system.ObjectType,
	newInterface *type_system.ObjectType,
	decl *ast.InterfaceDecl,
) []Error {
	errors := []Error{}

	// Build a map of property names to their types from the existing interface
	existingProps := make(map[type_system.ObjTypeKey]type_system.Type)
	for _, elem := range existingInterface.Elems {
		switch elem := elem.(type) {
		case *type_system.PropertyElem:
			existingProps[elem.Name] = elem.Value
		case *type_system.MethodElem:
			existingProps[elem.Name] = elem.Fn
		case *type_system.GetterElem:
			existingProps[elem.Name] = elem.Fn.Return
		case *type_system.SetterElem:
			existingProps[elem.Name] = elem.Fn.Params[0].Type
		}
	}

	// Check each property in the new interface against the existing interface
	for _, elem := range newInterface.Elems {
		var name type_system.ObjTypeKey
		var newType type_system.Type

		switch elem := elem.(type) {
		case *type_system.PropertyElem:
			name = elem.Name
			newType = elem.Value
		case *type_system.MethodElem:
			name = elem.Name
			newType = elem.Fn
		case *type_system.GetterElem:
			name = elem.Name
			newType = elem.Fn.Return
		case *type_system.SetterElem:
			name = elem.Name
			newType = elem.Fn.Params[0].Type
		default:
			continue
		}

		// If a property with this name already exists, check type compatibility
		if existingType, exists := existingProps[name]; exists {
			// Properties with the same name must have identical types
			unifyErrors := c.Unify(ctx, newType, existingType)
			if len(unifyErrors) > 0 {
				// Add a more specific error for interface merging
				errors = append(errors, &InterfaceMergeError{
					InterfaceName: decl.Name.Name,
					PropertyName:  name.String(),
					ExistingType:  existingType,
					NewType:       newType,
					span:          decl.Name.Span(),
				})
			}
		}
	}

	return errors
}
