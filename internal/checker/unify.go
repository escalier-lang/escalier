package checker

import (
	"fmt"
	"os"
	"slices"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// getSpanFromType extracts the span from a type's provenance if available
func getSpanFromType(t type_system.Type) ast.Span {
	if t == nil {
		return DEFAULT_SPAN
	}
	if prov := t.Provenance(); prov != nil {
		if nodeProv, ok := prov.(*ast.NodeProvenance); ok && nodeProv.Node != nil {
			return nodeProv.Node.Span()
		}
	}
	return DEFAULT_SPAN
}

// If `Unify` doesn't return an error it means that `t1` is a subtype of `t2` or
// they are the same type.
func (c *Checker) Unify(ctx Context, t1, t2 type_system.Type) []Error {
	if t1 == nil || t2 == nil {
		panic("Cannot unify nil types")
	}

	t1 = type_system.Prune(t1)
	t2 = type_system.Prune(t2)

	// fmt.Fprintf(os.Stderr, "Unifying types %s and %s\n", t1, t2)

	// | TypeVarType, _ -> ...
	if _, ok := t1.(*type_system.TypeVarType); ok {
		return c.bind(ctx, t1, t2)
	}
	// | _, TypeVarType -> ...
	if _, ok := t2.(*type_system.TypeVarType); ok {
		return c.bind(ctx, t1, t2)
	}
	// | MutableType, MutableType -> ...
	if mut1, ok := t1.(*type_system.MutabilityType); ok {
		if mut2, ok := t2.(*type_system.MutabilityType); ok {
			if mut1.Mutability == type_system.MutabilityMutable && mut2.Mutability == type_system.MutabilityMutable {
				return c.unifyMut(ctx, mut1, mut2)
			} else {
				return c.Unify(ctx, mut1.Type, mut2.Type)
			}
		}
	}
	// | MutableType, _ -> ...
	if mut1, ok := t1.(*type_system.MutabilityType); ok {
		// If t2 is a union or intersection, let their handling code deal with it
		// This ensures that mut types in unions/intersections are compared properly
		switch t2.(type) {
		case *type_system.UnionType, *type_system.IntersectionType:
			// Fall through to union/intersection handling below
		default:
			// It's okay to assign mutable types to immutable types
			return c.Unify(ctx, mut1.Type, t2)
		}
	}
	// | _, MutableType -> ...
	if mut2, ok := t2.(*type_system.MutabilityType); ok {
		// When the RHS is a MutabilityType, we need to unwrap it for unification
		// This allows patterns without mutability markers to match against mutable values
		return c.Unify(ctx, t1, mut2.Type)
	}
	// | PrimType, PrimType -> ...
	if prim1, ok := t1.(*type_system.PrimType); ok {
		if prim2, ok := t2.(*type_system.PrimType); ok {
			if type_system.Equals(prim1, prim2) {
				return nil
			}
			// Different primitive types cannot be unified
			return []Error{&CannotUnifyTypesError{
				T1: prim1,
				T2: prim2,
			}}
		}
	}
	// What's the difference between wildcard and any?
	// TODO: dedupe these types
	// | AnyType, _ -> ...
	if _, ok := t1.(*type_system.AnyType); ok {
		return nil
	}
	// | _, AnyType -> ...
	if _, ok := t2.(*type_system.AnyType); ok {
		return nil
	}
	// | WildcardType, _ -> ...
	if _, ok := t1.(*type_system.WildcardType); ok {
		return nil
	}
	// | _, WildcardType -> ...
	if _, ok := t2.(*type_system.WildcardType); ok {
		return nil
	}
	// | UnknownType, UnknownType -> ...
	if _, ok := t1.(*type_system.UnknownType); ok {
		if _, ok := t2.(*type_system.UnknownType); ok {
			return nil
		}
		// UnknownType cannot be assigned to other types
		return []Error{&CannotUnifyTypesError{
			T1: t1,
			T2: t2,
		}}
	}
	// | _, UnknownType -> ...
	if _, ok := t2.(*type_system.UnknownType); ok {
		// All types can be assigned to UnknownType
		return nil
	}
	// | NeveType, _ -> ...
	if _, ok := t1.(*type_system.NeverType); ok {
		return nil
	}
	// | _, NeverType -> ...
	if _, ok := t2.(*type_system.NeverType); ok {
		return []Error{&CannotUnifyTypesError{
			T1: t1,
			T2: t2,
		}}
	}
	// | VoidType, VoidType -> ...
	if _, ok := t1.(*type_system.VoidType); ok {
		if _, ok := t2.(*type_system.VoidType); ok {
			return nil
		}
		// void can be assigned to undefined literal
		if lit2, ok := t2.(*type_system.LitType); ok {
			if _, ok := lit2.Lit.(*type_system.UndefinedLit); ok {
				return nil
			}
		}
		return []Error{&CannotUnifyTypesError{
			T1: t1,
			T2: t2,
		}}
	}
	// | _, VoidType -> ...
	if _, ok := t2.(*type_system.VoidType); ok {
		// undefined literal can be assigned to void
		if lit1, ok := t1.(*type_system.LitType); ok {
			if _, ok := lit1.Lit.(*type_system.UndefinedLit); ok {
				return nil
			}
		}
		return []Error{&CannotUnifyTypesError{
			T1: t1,
			T2: t2,
		}}
	}
	// | TupleType, TupleType -> ...
	if tuple1, ok := t1.(*type_system.TupleType); ok {
		if tuple2, ok := t2.(*type_system.TupleType); ok {
			// TODO: handle spread
			errors := []Error{}

			// TODO: Don't allow more than one rest element in tuple1
			restElem2, ok := tuple2.Elems[len(tuple2.Elems)-1].(*type_system.RestSpreadType)
			if ok {
				elems2 := tuple2.Elems[:len(tuple2.Elems)-1]
				elems1 := tuple1.Elems[:len(elems2)]

				for elem1, elem2 := range Zip(elems1, elems2) {
					unifyErrors := c.Unify(ctx, elem1, elem2)
					errors = slices.Concat(errors, unifyErrors)
				}
				remainingElems := tuple1.Elems[len(elems2):]
				tuple := type_system.NewTupleType(nil, remainingElems...)
				unifyErrors := c.Unify(ctx, tuple, restElem2.Type)
				errors = slices.Concat(errors, unifyErrors)
				return errors
			}

			restElem1, ok := tuple1.Elems[len(tuple1.Elems)-1].(*type_system.RestSpreadType)
			if ok {
				elems1 := tuple1.Elems[:len(tuple1.Elems)-1]
				elems2 := tuple2.Elems[:len(elems1)]

				for elem1, elem2 := range Zip(elems1, elems2) {
					unifyErrors := c.Unify(ctx, elem1, elem2)
					errors = slices.Concat(errors, unifyErrors)
				}
				remainingElems := tuple2.Elems[len(elems1):]
				tuple := type_system.NewTupleType(nil, remainingElems...)
				unifyErrors := c.Unify(ctx, restElem1.Type, tuple)
				errors = slices.Concat(errors, unifyErrors)
				return errors
			}

			if len(tuple2.Elems) > len(tuple1.Elems) {
				// Unify the elements that are present in both tuples
				for elem1, elem2 := range Zip(tuple1.Elems, tuple2.Elems) {
					unifyErrors := c.Unify(ctx, elem1, elem2)
					errors = slices.Concat(errors, unifyErrors)
				}

				extraElems := tuple2.Elems[len(tuple1.Elems):]
				first := GetNode(extraElems[0].Provenance())
				last := GetNode(extraElems[len(extraElems)-1].Provenance())

				// Any remaining elements in tuple2 should be typed as `undefined`
				// since they are not present in tuple1.
				for _, elem2 := range extraElems {
					node := GetNode(elem2.Provenance())
					undefined := type_system.NewUndefinedType(&ast.NodeProvenance{Node: node})
					unifyErrors := c.Unify(ctx, elem2, undefined)
					errors = slices.Concat(errors, unifyErrors)
				}

				return slices.Concat(errors, []Error{&NotEnoughElementsToUnpackError{
					span: ast.MergeSpans(first.Span(), last.Span()),
				}})
			}

			if len(tuple1.Elems) != len(tuple2.Elems) {
				return []Error{&CannotUnifyTypesError{
					T1: tuple1,
					T2: tuple2,
				}}
			}

			for elem1, elem2 := range Zip(tuple1.Elems, tuple2.Elems) {
				unifyErrors := c.Unify(ctx, elem1, elem2)
				errors = slices.Concat(errors, unifyErrors)
			}

			return errors
		}
	}
	// | TupleType, ArrayType -> ...
	if tuple1, ok := t1.(*type_system.TupleType); ok {
		if array2, ok := t2.(*type_system.TypeRefType); ok && type_system.QualIdentToString(array2.Name) == "Array" {
			// A tuple can be unified with an array if all tuple elements
			// can be unified with the array's element type
			if len(array2.TypeArgs) == 1 {
				errors := []Error{}
				for _, elem := range tuple1.Elems {
					unifyErrors := c.Unify(ctx, elem, array2.TypeArgs[0])
					errors = slices.Concat(errors, unifyErrors)
				}
				return errors
			}
			return []Error{&CannotUnifyTypesError{
				T1: tuple1,
				T2: array2,
			}}
		}
	}
	// | ArrayType, TupleType -> ...
	if array1, ok := t1.(*type_system.TypeRefType); ok && type_system.QualIdentToString(array1.Name) == "Array" {
		if tuple2, ok := t2.(*type_system.TupleType); ok {
			// An array can be unified with a tuple if the array's element type
			// can be unified with all tuple elements
			if len(array1.TypeArgs) == 1 {
				errors := []Error{}
				for _, elem := range tuple2.Elems {
					unifyErrors := c.Unify(ctx, array1.TypeArgs[0], elem)
					errors = slices.Concat(errors, unifyErrors)
				}
				return errors
			}
			return []Error{&CannotUnifyTypesError{
				T1: array1,
				T2: tuple2,
			}}
		}
	}
	// | ArrayType, ArrayType -> ...
	if array1, ok := t1.(*type_system.TypeRefType); ok && type_system.QualIdentToString(array1.Name) == "Array" {
		if array2, ok := t2.(*type_system.TypeRefType); ok && type_system.QualIdentToString(array2.Name) == "Array" {
			// Both are Array types, unify their element types
			if len(array1.TypeArgs) == 1 && len(array2.TypeArgs) == 1 {
				return c.Unify(ctx, array1.TypeArgs[0], array2.TypeArgs[0])
			}
			// If either array doesn't have exactly one type argument, they can't be unified
			return []Error{&CannotUnifyTypesError{
				T1: array1,
				T2: array2,
			}}
		}
	}
	// | RestSpreadType, ArrayType -> ...
	if rest, ok := t1.(*type_system.RestSpreadType); ok {
		if array, ok := t2.(*type_system.TypeRefType); ok && type_system.QualIdentToString(array.Name) == "Array" {
			return c.Unify(ctx, rest.Type, array)
		}
	}
	// | FuncType, FuncType -> ...
	if func1, ok := t1.(*type_system.FuncType); ok {
		if func2, ok := t2.(*type_system.FuncType); ok {
			return c.unifyFuncTypes(ctx, func1, func2)
		}
	}
	// | TypeRefType, TypeRefType (same alias name) -> ...
	if ref1, ok := t1.(*type_system.TypeRefType); ok {
		if ref2, ok := t2.(*type_system.TypeRefType); ok && type_system.QualIdentToString(ref1.Name) == type_system.QualIdentToString(ref2.Name) {
			if len(ref1.TypeArgs) == 0 && len(ref2.TypeArgs) == 0 {
				// If both type references have no type arguments, we can unify them
				// directly.

				// Most of the time, type references will have their TypeAlias
				// field set to whatever type alias they refer to when they were
				// created.  However, certain type references such as those used
				// for type parameters in generics may not have this field set.

				typeAlias1 := ref1.TypeAlias
				if typeAlias1 == nil {
					typeAlias1 = resolveQualifiedTypeAlias(ctx, ref1.Name)
					if typeAlias1 == nil {
						return []Error{&UnknownTypeError{
							TypeName: type_system.QualIdentToString(ref1.Name),
							TypeRef:  ref1,
						}}
					}
				}
				typeAlias2 := ref2.TypeAlias
				if typeAlias2 == nil {
					typeAlias2 = resolveQualifiedTypeAlias(ctx, ref2.Name)
					if typeAlias2 == nil {
						return []Error{&UnknownTypeError{
							TypeName: type_system.QualIdentToString(ref2.Name),
							TypeRef:  ref2,
						}}
					}
				}
				return []Error{}
				// TODO: Give each TypeAlias a unique ID and if they so avoid
				// situations where two different type aliases have the same
				// name but different definitions.
				// return c.unify(ctx, typeAlias1.Type, typeAlias2.Type)
			} else {
				// Both references have the same alias name and may have type arguments.
				// Unify each corresponding type argument pairwise.
				if len(ref1.TypeArgs) != len(ref2.TypeArgs) {
					return []Error{&CannotUnifyTypesError{
						T1: ref1,
						T2: ref2,
					}}
				}
				errors := []Error{}
				for i := 0; i < len(ref1.TypeArgs); i++ {
					argErrors := c.Unify(ctx, ref1.TypeArgs[i], ref2.TypeArgs[i])
					errors = slices.Concat(errors, argErrors)
				}
				return errors
			}
		}
	}
	// | TypeRefType, TypeRefType (different alias name) -> ...
	if ref1, ok := t1.(*type_system.TypeRefType); ok {
		if ref2, ok := t2.(*type_system.TypeRefType); ok && ref1.Name != ref2.Name {
			// panic(fmt.Sprintf("TODO: unify types %#v and %#v", ref1, ref2))
			// TODO
		}
	}
	// | LitType, PrimType -> ...
	if lit, ok := t1.(*type_system.LitType); ok {
		if prim, ok := t2.(*type_system.PrimType); ok {
			if _, ok := lit.Lit.(*type_system.NumLit); ok && prim.Prim == "number" {
				return nil
			} else if _, ok := lit.Lit.(*type_system.StrLit); ok && prim.Prim == "string" {
				return nil
			} else if _, ok := lit.Lit.(*type_system.BoolLit); ok && prim.Prim == "boolean" {
				return nil
			} else if _, ok := lit.Lit.(*type_system.BigIntLit); ok && prim.Prim == "bigint" {
				return nil
			} else {
				return []Error{&CannotUnifyTypesError{
					T1: lit,
					T2: prim,
				}}
			}
		}
	}
	// | LitType, LitType -> ...
	if lit1, ok := t1.(*type_system.LitType); ok {
		if lit2, ok := t2.(*type_system.LitType); ok {
			if l1, ok := lit1.Lit.(*type_system.NumLit); ok {
				if l2, ok := lit2.Lit.(*type_system.NumLit); ok {
					if l1.Equal(l2) {
						return nil
					} else {
						return []Error{&CannotUnifyTypesError{
							T1: lit1,
							T2: lit2,
						}}
					}
				}
			}
			if l1, ok := lit1.Lit.(*type_system.StrLit); ok {
				if l2, ok := lit2.Lit.(*type_system.StrLit); ok {
					if l1.Equal(l2) {
						return nil
					} else {
						return []Error{&CannotUnifyTypesError{
							T1: lit1,
							T2: lit2,
						}}
					}
				}
			}
			if l1, ok := lit1.Lit.(*type_system.BoolLit); ok {
				if l2, ok := lit2.Lit.(*type_system.BoolLit); ok {
					if l1.Equal(l2) {
						return nil
					} else {
						return []Error{&CannotUnifyTypesError{
							T1: lit1,
							T2: lit2,
						}}
					}
				}
			}
			if _, ok := lit1.Lit.(*type_system.UndefinedLit); ok {
				if _, ok := lit2.Lit.(*type_system.UndefinedLit); ok {
					return nil
				}
			}
			if _, ok := lit1.Lit.(*type_system.NullLit); ok {
				if _, ok := lit2.Lit.(*type_system.NullLit); ok {
					return nil
				}
			}
			return []Error{&CannotUnifyTypesError{
				T1: lit1,
				T2: lit2,
			}}
		}
	}
	// | RegexType, RegexType -> ...
	if regex1, ok := t1.(*type_system.RegexType); ok {
		if regex2, ok := t2.(*type_system.RegexType); ok {
			if type_system.Equals(regex1, regex2) {
				return nil
			} else {
				return []Error{&CannotUnifyTypesError{
					T1: regex1,
					T2: regex2,
				}}
			}
		}
	}
	// | LitType (string), RegexType -> ...
	if lit, ok := t1.(*type_system.LitType); ok {
		if regexType, ok := t2.(*type_system.RegexType); ok {
			if strLit, ok := lit.Lit.(*type_system.StrLit); ok {
				matches := regexType.Regex.FindStringSubmatch(strLit.Value)
				if matches != nil {
					groupNames := regexType.Regex.SubexpNames()
					errors := []Error{}

					for i, name := range groupNames {
						if name != "" {
							groupErrors := c.Unify(
								ctx,
								type_system.NewStrLitType(nil, matches[i]),
								// By default this will be a `string` type, but
								// if the RegexType appears in a CondType's
								// Extend field, it will be a TypeVarType.
								regexType.Groups[name],
							)
							errors = slices.Concat(errors, groupErrors)
						}
					}

					return errors
				} else {
					return []Error{&CannotUnifyTypesError{
						T1: lit,
						T2: regexType,
					}}
				}
			}
		}
	}
	// | LitType (string), TemplateLitType -> ...
	if lit, ok := t1.(*type_system.LitType); ok {
		if template, ok := t2.(*type_system.TemplateLitType); ok {
			if strLit, ok := lit.Lit.(*type_system.StrLit); ok {
				panic(fmt.Sprintf("TODO: unify types %#v and %#v", strLit, template))
				// TODO
			}
		}
	}
	// | UniqueSymbolType, UniqueSymbolType -> ...
	if unique1, ok := t1.(*type_system.UniqueSymbolType); ok {
		if unique2, ok := t2.(*type_system.UniqueSymbolType); ok {
			if type_system.Equals(unique1, unique2) {
				return nil
			} else {
				return []Error{&CannotUnifyTypesError{
					T1: unique1,
					T2: unique2,
				}}
			}
		}
	}
	// TODO: dedupe with next case
	// | _, ExtractorType -> ...
	if ext, ok := t2.(*type_system.ExtractorType); ok {
		if extObj, ok := ext.Extractor.(*type_system.ObjectType); ok {
			for _, elem := range extObj.Elems {
				if methodElem, ok := elem.(*type_system.MethodElem); ok {
					// TODO: look up the symbol ID for `Symbol.customMatcher`
					if methodElem.Name.Kind == type_system.SymObjTypeKeyKind && methodElem.Name.Sym == 2 {
						if len(methodElem.Fn.Params) != 1 {
							return []Error{&IncorrectParamCountForCustomMatcherError{
								Method:    methodElem.Fn,
								NumParams: len(methodElem.Fn.Params),
							}}
						}

						paramType := methodElem.Fn.Params[0].Type
						errors := c.Unify(ctx, t1, paramType)

						if tuple, ok := methodElem.Fn.Return.(*type_system.TupleType); ok {
							// If the subject is a type reference, then we need
							// to substitute any type parameters in the tuple for
							// the type arguments specified in the subject's type
							// reference.
							// TODO: We might have to expand `t1` if the type alias
							// it's using points to another type alias.
							if typeRef, ok := t1.(*type_system.TypeRefType); ok {
								substitutions := make(map[string]type_system.Type)
								for i, typeParam := range typeRef.TypeAlias.TypeParams {
									substitutions[typeParam.Name] = typeRef.TypeArgs[i]
								}
								tuple = SubstituteTypeParams[*type_system.TupleType](tuple, substitutions)
							}

							// Find if the args have a rest element
							var restIndex = -1
							for i, elem := range ext.Args {
								if _, isRest := elem.(*type_system.RestSpreadType); isRest {
									restIndex = i
									break
								}
							}

							if restIndex != -1 {
								// Tuple has rest element
								// Must have at least as many args as elements before rest
								if len(ext.Args) < restIndex {
									return []Error{&ExtractorReturnTypeMismatchError{
										ExtractorType: ext,
										ReturnType:    tuple,
										NumArgs:       len(ext.Args),
										NumReturns:    len(tuple.Elems),
									}}
								}

								// Unify fixed elements (before rest)
								for i := 0; i < restIndex; i++ {
									argErrors := c.Unify(ctx, tuple.Elems[i], ext.Args[i])
									errors = slices.Concat(errors, argErrors)
								}

								// Unify rest arguments with rest element type
								if len(ext.Args) > restIndex {
									restElem := ext.Args[restIndex].(*type_system.RestSpreadType)
									remainingArgsTupleType := type_system.NewTupleType(nil, tuple.Elems[restIndex:]...)

									restErrors := c.Unify(ctx, restElem.Type, remainingArgsTupleType)
									errors = slices.Concat(errors, restErrors)
								}
							} else {
								// Tuple has no rest element, use strict equality check
								if len(tuple.Elems) == len(ext.Args) {
									for retElem, argType := range Zip(tuple.Elems, ext.Args) {
										argErrors := c.Unify(ctx, retElem, argType)
										errors = slices.Concat(errors, argErrors)
									}
								} else {
									return []Error{&ExtractorReturnTypeMismatchError{
										ExtractorType: ext,
										ReturnType:    tuple,
										NumArgs:       len(ext.Args),
										NumReturns:    len(tuple.Elems),
									}}
								}
							}
						} else {
							return []Error{&ExtractorMustReturnTupleError{
								ExtractorType: ext,
								ReturnType:    methodElem.Fn.Return,
							}}
						}

						return errors
					}
				}
			}
			return []Error{&MissingCustomMatcherError{
				ObjectType: extObj,
			}}
		}
		return []Error{&InvalidExtractorTypeError{
			ExtractorType: ext,
			ActualType:    ext.Extractor,
		}}
	}
	// TODO: dedupe with previous case
	// | ExtractorType, _ -> ...
	if ext, ok := t1.(*type_system.ExtractorType); ok {
		if extObj, ok := ext.Extractor.(*type_system.ObjectType); ok {
			for _, elem := range extObj.Elems {
				if methodElem, ok := elem.(*type_system.MethodElem); ok {
					// TODO: look up the symbol ID for `Symbol.customMatcher`
					if methodElem.Name.Kind == type_system.SymObjTypeKeyKind && methodElem.Name.Sym == 2 {
						if len(methodElem.Fn.Params) != 1 {
							return []Error{&IncorrectParamCountForCustomMatcherError{
								Method:    methodElem.Fn,
								NumParams: len(methodElem.Fn.Params),
							}}
						}

						paramType := methodElem.Fn.Params[0].Type
						errors := c.Unify(ctx, paramType, t2)

						if tuple, ok := methodElem.Fn.Return.(*type_system.TupleType); ok {
							// If the subject is a type reference, then we need
							// to substitute any type parameters in the tuple for
							// the type arguments specified in the subject's type
							// reference.
							// TODO: We might have to expand `t2` if the type alias
							// it's using points to another type alias.
							if typeRef, ok := t2.(*type_system.TypeRefType); ok {
								substitutions := make(map[string]type_system.Type)
								for i, typeParam := range typeRef.TypeAlias.TypeParams {
									substitutions[typeParam.Name] = typeRef.TypeArgs[i]
								}
								tuple = SubstituteTypeParams[*type_system.TupleType](tuple, substitutions)
							}

							// Find if the args have a rest element
							var restIndex = -1
							for i, elem := range ext.Args {
								if _, isRest := elem.(*type_system.RestSpreadType); isRest {
									restIndex = i
									break
								}
							}

							if restIndex != -1 {
								// Tuple has rest element
								// Must have at least as many args as elements before rest
								if len(ext.Args) < restIndex {
									return []Error{&ExtractorReturnTypeMismatchError{
										ExtractorType: ext,
										ReturnType:    tuple,
										NumArgs:       len(ext.Args),
										NumReturns:    len(tuple.Elems),
									}}
								}

								// Unify fixed elements (before rest)
								for i := 0; i < restIndex; i++ {
									argErrors := c.Unify(ctx, ext.Args[i], tuple.Elems[i])
									errors = slices.Concat(errors, argErrors)
								}

								// Unify rest arguments with rest element type
								if len(ext.Args) > restIndex {
									restElem := ext.Args[restIndex].(*type_system.RestSpreadType)
									remainingArgsTupleType := type_system.NewTupleType(nil, tuple.Elems[restIndex:]...)

									restErrors := c.Unify(ctx, remainingArgsTupleType, restElem.Type)
									errors = slices.Concat(errors, restErrors)
								}
							} else {
								// Tuple has no rest element, use strict equality check
								if len(tuple.Elems) == len(ext.Args) {
									for retElem, argType := range Zip(tuple.Elems, ext.Args) {
										argErrors := c.Unify(ctx, argType, retElem)
										errors = slices.Concat(errors, argErrors)
									}
								} else {
									return []Error{&ExtractorReturnTypeMismatchError{
										ExtractorType: ext,
										ReturnType:    tuple,
										NumArgs:       len(ext.Args),
										NumReturns:    len(tuple.Elems),
									}}
								}
							}
						} else {
							return []Error{&ExtractorMustReturnTupleError{
								ExtractorType: ext,
								ReturnType:    methodElem.Fn.Return,
							}}
						}

						return errors
					}
				}
			}
			return []Error{&MissingCustomMatcherError{
				ObjectType: extObj,
			}}
		}
		return []Error{&InvalidExtractorTypeError{
			ExtractorType: ext,
			ActualType:    ext.Extractor,
		}}
	}
	// | ObjectType, ObjectType -> ...
	if obj1, ok := t1.(*type_system.ObjectType); ok {
		if obj2, ok := t2.(*type_system.ObjectType); ok {
			if obj2.Nominal {
				// NOTE: We can't do an early return because if one of the object
				// types was inferred from a pattern, some of its properties may
				// be type variables that need to be unified.
				if obj1.ID != obj2.ID {
					// TODO: check what classes the objects extend
					return []Error{&CannotUnifyTypesError{
						T1: obj1,
						T2: obj2,
					}}
				}
			}

			// TODO: handle exactness
			// TODO: handle unnamed elems, e.g. callable and newable signatures
			// TODO: handle spread
			// TODO: handle mapped type elems
			// TODO: handle getters/setters appropriately (we need to know which
			// type is being read from and which is being written to... does that
			// question even make sense?)

			errors := []Error{}

			namedElems1 := make(map[type_system.ObjTypeKey]type_system.Type)
			namedElems2 := make(map[type_system.ObjTypeKey]type_system.Type)

			keys1 := []type_system.ObjTypeKey{} // original order of keys in obj1
			keys2 := []type_system.ObjTypeKey{} // original order of keys in obj2

			var restType1 type_system.Type
			var restType2 type_system.Type

			for _, elem := range obj1.Elems {
				switch elem := elem.(type) {
				case *type_system.MethodElem:
					namedElems1[elem.Name] = elem.Fn
					keys1 = append(keys1, elem.Name)
				case *type_system.GetterElem:
					namedElems1[elem.Name] = elem.Fn.Return
					keys1 = append(keys1, elem.Name)
				case *type_system.SetterElem:
					namedElems1[elem.Name] = elem.Fn.Params[0].Type
					keys1 = append(keys1, elem.Name)
				case *type_system.PropertyElem:
					propType := elem.Value
					if elem.Optional {
						propType = type_system.NewUnionType(nil, propType, type_system.NewUndefinedType(nil))
					}
					namedElems1[elem.Name] = propType
					keys1 = append(keys1, elem.Name)
				case *type_system.RestSpreadElem:
					restType1 = elem.Value
				default: // skip other types of elems
				}
			}

			for _, elem := range obj2.Elems {
				switch elem := elem.(type) {
				case *type_system.MethodElem:
					namedElems2[elem.Name] = elem.Fn
					keys2 = append(keys2, elem.Name)
				case *type_system.GetterElem:
					namedElems2[elem.Name] = elem.Fn.Return
					keys2 = append(keys2, elem.Name)
				case *type_system.SetterElem:
					namedElems2[elem.Name] = elem.Fn.Params[0].Type
					keys2 = append(keys2, elem.Name)
				case *type_system.PropertyElem:
					propType := elem.Value
					if elem.Optional {
						propType = type_system.NewUnionType(nil, propType, type_system.NewUndefinedType(nil))
					}
					namedElems2[elem.Name] = propType
					keys2 = append(keys2, elem.Name)
				case *type_system.RestSpreadElem:
					restType2 = elem.Value
				default: // skip other types of elems
				}
			}

			if restType1 != nil && restType2 != nil {
				return []Error{&UnimplementedError{message: "unify types with two rest elems"}}
			} else if restType1 != nil {
				usedKeys2 := map[type_system.ObjTypeKey]bool{}
				for _, key1 := range keys1 {
					value1 := namedElems1[key1]
					if value2, ok := namedElems2[key1]; ok {
						unifyErrors := c.Unify(ctx, value1, value2)
						errors = slices.Concat(errors, unifyErrors)
						usedKeys2[key1] = true
					} else {
						errors = slices.Concat(errors, []Error{&KeyNotFoundError{
							Object: obj2,
							Key:    key1,
							span:   getSpanFromType(value1),
						}})
						// Unify the missing property's type with 'undefined' so that it gets
						// properly resolved and doesn't remain as a type variable
						undefinedType := type_system.NewUndefinedType(nil)
						unifyErrors := c.Unify(ctx, value1, undefinedType)
						errors = slices.Concat(errors, unifyErrors)
					}
				}

				restElems := []type_system.ObjTypeElem{}
				for _, key := range keys2 {
					if _, ok := usedKeys2[key]; !ok {
						restElems = append(restElems, &type_system.PropertyElem{
							Name:     key,
							Optional: false, // TODO
							Readonly: false, // TODO
							Value:    namedElems2[key],
						})
					}
				}

				objType := type_system.NewObjectType(nil, restElems)

				unifyErrors := c.Unify(ctx, objType, restType1)
				errors = slices.Concat(errors, unifyErrors)
			} else if restType2 != nil {
				usedKeys1 := map[type_system.ObjTypeKey]bool{}
				for _, key2 := range keys2 {
					value2 := namedElems2[key2]
					if value1, ok := namedElems1[key2]; ok {
						unifyErrors := c.Unify(ctx, value1, value2)
						errors = slices.Concat(errors, unifyErrors)
						usedKeys1[key2] = true
					} else {
						errors = slices.Concat(errors, []Error{&KeyNotFoundError{
							Object: obj1,
							Key:    key2,
							span:   getSpanFromType(value2),
						}})
						// Unify the missing property's type with 'undefined' so that it gets
						// properly resolved and doesn't remain as a type variable
						undefinedType := type_system.NewUndefinedType(nil)
						unifyErrors := c.Unify(ctx, value2, undefinedType)
						errors = slices.Concat(errors, unifyErrors)
					}
				}

				restElems := []type_system.ObjTypeElem{}
				for _, key := range keys1 {
					if _, ok := usedKeys1[key]; !ok {
						restElems = append(restElems, &type_system.PropertyElem{
							Name:     key,
							Optional: false, // TODO
							Readonly: false, // TODO
							Value:    namedElems1[key],
						})
					}
				}

				objType := type_system.NewObjectType(nil, restElems)

				unifyErrors := c.Unify(ctx, restType2, objType)
				errors = slices.Concat(errors, unifyErrors)
			} else {
				for _, key2 := range keys2 {
					value2 := namedElems2[key2]
					if value1, ok := namedElems1[key2]; ok {
						unifyErrors := c.Unify(ctx, value1, value2)
						errors = slices.Concat(errors, unifyErrors)
					} else {
						errors = slices.Concat(errors, []Error{&KeyNotFoundError{
							Object: obj1,
							Key:    key2,
							span:   getSpanFromType(value2),
						}})
						// Unify the missing property's type with 'undefined' so that it gets
						// properly resolved and doesn't remain as a type variable
						undefinedType := type_system.NewUndefinedType(nil)
						unifyErrors := c.Unify(ctx, value2, undefinedType)
						errors = slices.Concat(errors, unifyErrors)
					}
				}
			}

			return errors
		}
	}
	// | IntersectionType, ObjectType -> ...
	if intersection, ok := t1.(*type_system.IntersectionType); ok {
		if obj, ok := t2.(*type_system.ObjectType); ok {
			panic(fmt.Sprintf("TODO: unify types %#v and %#v", intersection, obj))
		}
	}
	// | ObjectType, UnionType -> ...
	// if obj, ok := t1.(*ObjectType); ok {
	// 	if union, ok := t2.(*type_system.UnionType); ok {
	// 		panic(fmt.Sprintf("TODO: unify types %#v and %#v", obj, union))
	// 		// TODO
	// 	}
	// }
	// | IntersectionType, IntersectionType -> ...
	if intersection1, ok := t1.(*type_system.IntersectionType); ok {
		if intersection2, ok := t2.(*type_system.IntersectionType); ok {
			panic(fmt.Sprintf("TODO: unify types %#v and %#v", intersection1, intersection2))
			// TODO
		}
	}
	// | UnionType, _ -> ...
	if union, ok := t1.(*type_system.UnionType); ok {
		// special-case unification of union with object type
		if obj, ok := t2.(*type_system.ObjectType); ok {
			destructuredFields := make(map[type_system.ObjTypeKey]type_system.Type)
			var restType type_system.Type
			for _, elem := range obj.Elems {
				switch elem := elem.(type) {
				case *type_system.MethodElem:
					destructuredFields[elem.Name] = elem.Fn
				case *type_system.GetterElem:
					destructuredFields[elem.Name] = elem.Fn.Return
				case *type_system.SetterElem:
					destructuredFields[elem.Name] = elem.Fn.Params[0].Type
				case *type_system.PropertyElem:
					propType := elem.Value
					if elem.Optional {
						propType = type_system.NewUnionType(nil, propType, type_system.NewUndefinedType(nil))
					}
					destructuredFields[elem.Name] = propType
				case *type_system.RestSpreadElem:
					restType = elem.Value
				default: // skip other types of elems
				}
			}

			for name, t := range destructuredFields {
				fmt.Fprintf(os.Stderr, "%s: %s\n", name.String(), t.String())
			}

			matchingTypes := make(map[type_system.ObjTypeKey][]type_system.Type)
			// Track remaining fields for rest spread handling
			remainingFields := make(map[type_system.ObjTypeKey][]type_system.Type)
			remainingFieldsOrder := []type_system.ObjTypeKey{} // Track order of keys

			for _, unionType := range union.Types {
				if unionObj, ok := unionType.(*type_system.ObjectType); ok {
					for name := range destructuredFields {
						var t type_system.Type
						// Find the type of the field with this name in the union object
						for _, elem := range unionObj.Elems {
							switch elem := elem.(type) {
							case *type_system.MethodElem:
								if elem.Name == name {
									t = elem.Fn
								}
							case *type_system.GetterElem:
								if elem.Name == name {
									t = elem.Fn.Return
								}
							case *type_system.SetterElem:
								if elem.Name == name {
									t = elem.Fn.Params[0].Type
								}
							case *type_system.PropertyElem:
								if elem.Name == name {
									propType := elem.Value
									if elem.Optional {
										propType = type_system.NewUnionType(nil, propType, type_system.NewUndefinedType(nil))
									}
									t = propType
								}
							default: // skip other types of elems
							}
						}
						if t != nil {
							matchingTypes[name] = append(matchingTypes[name], t)
						}
					}

					// If restType is specified, collect remaining fields
					if restType != nil {
						for _, elem := range unionObj.Elems {
							switch elem := elem.(type) {
							case *type_system.MethodElem:
								if _, ok := destructuredFields[elem.Name]; !ok {
									if _, exists := remainingFields[elem.Name]; !exists {
										remainingFieldsOrder = append(remainingFieldsOrder, elem.Name)
									}
									remainingFields[elem.Name] = append(remainingFields[elem.Name], elem.Fn)
								}
							case *type_system.GetterElem:
								if _, ok := destructuredFields[elem.Name]; !ok {
									if _, exists := remainingFields[elem.Name]; !exists {
										remainingFieldsOrder = append(remainingFieldsOrder, elem.Name)
									}
									remainingFields[elem.Name] = append(remainingFields[elem.Name], elem.Fn.Return)
								}
							case *type_system.SetterElem:
								if _, ok := destructuredFields[elem.Name]; !ok {
									if _, exists := remainingFields[elem.Name]; !exists {
										remainingFieldsOrder = append(remainingFieldsOrder, elem.Name)
									}
									remainingFields[elem.Name] = append(remainingFields[elem.Name], elem.Fn.Params[0].Type)
								}
							case *type_system.PropertyElem:
								if _, ok := destructuredFields[elem.Name]; !ok {
									propType := elem.Value
									if elem.Optional {
										propType = type_system.NewUnionType(nil, propType, type_system.NewUndefinedType(nil))
									}
									if _, exists := remainingFields[elem.Name]; !exists {
										remainingFieldsOrder = append(remainingFieldsOrder, elem.Name)
									}
									remainingFields[elem.Name] = append(remainingFields[elem.Name], propType)
								}
							default: // skip other types of elems
							}
						}
					}
				}
			}
			errors := []Error{}
			for name, t := range destructuredFields {
				// if destructuredFields[name] doesn't exist, unify `undefined` with `t`
				if _, ok := matchingTypes[name]; !ok {
					undefined := type_system.NewUndefinedType(nil)
					unifyErrors := c.Unify(ctx, undefined, t)
					errors = slices.Concat(errors, unifyErrors)
				} else if len(matchingTypes[name]) == len(union.Types) {
					// Create a union of all matching types and unify with destructured field type
					unionOfMatchingTypes := type_system.NewUnionType(nil, matchingTypes[name]...)
					fieldType := destructuredFields[name]
					unifyErrors := c.Unify(ctx, unionOfMatchingTypes, fieldType)
					errors = slices.Concat(errors, unifyErrors)
				} else {
					// Create a union of all matching types and `undefined`, then unify with destructured field type
					unionOfMatchingTypes := type_system.NewUnionType(nil, append(matchingTypes[name], type_system.NewUndefinedType(nil))...)
					fieldType := destructuredFields[name]
					unifyErrors := c.Unify(ctx, unionOfMatchingTypes, fieldType)
					errors = slices.Concat(errors, unifyErrors)
				}
			}

			// Handle rest spread element if present
			if restType != nil {
				restElems := []type_system.ObjTypeElem{}
				for _, name := range remainingFieldsOrder {
					types := remainingFields[name]
					// Create a union of all types for this field across union members
					var fieldType type_system.Type
					if len(types) == 1 {
						fieldType = types[0]
					} else if len(types) > 1 {
						fieldType = type_system.NewUnionType(nil, types...)
					} else {
						// Field doesn't exist in any union member, use undefined
						fieldType = type_system.NewUndefinedType(nil)
					}

					restElems = append(restElems, &type_system.PropertyElem{
						Name:     name,
						Optional: false, // TODO: determine if this should be true
						Readonly: false, // TODO: determine if this should be true
						Value:    fieldType,
					})
				}

				objType := type_system.NewObjectType(nil, restElems)
				unifyErrors := c.Unify(ctx, objType, restType)
				errors = slices.Concat(errors, unifyErrors)
			}

			return errors
		}

		// All types in the union must be compatible with t2
		for _, t := range union.Types {
			unifyErrors := c.Unify(ctx, t, t2)
			// TODO: include the individual reasons why unification failed
			if len(unifyErrors) > 0 {
				// If any type in the union is not compatible, return error
				return []Error{&CannotUnifyTypesError{
					T1: union,
					T2: t2,
				}}
			}
		}
		return nil
	}
	// | _, UnionType -> ...
	if union, ok := t2.(*type_system.UnionType); ok {
		// Try to unify t1 with any type in the union
		for _, unionType := range union.Types {
			// fmt.Fprintf(os.Stderr, "Trying to unify %s with union member %s\n", t1.String(), unionType.String())
			// Try unifying - if any unification succeeds, we're good
			unifyErrors := c.Unify(ctx, t1, unionType)
			if len(unifyErrors) == 0 {
				// Successfully unified with one of the union types
				return nil
			}
		}
		// If we couldn't unify with any union member, return a unification error
		return []Error{&CannotUnifyTypesError{
			T1: t1,
			T2: union,
		}}
	}

	retry := false
	expandedT1, _ := c.ExpandType(ctx, t1, 1)
	if expandedT1 != t1 {
		t1 = expandedT1
		retry = true
	}
	expandedT2, _ := c.ExpandType(ctx, t2, 1)
	if expandedT2 != t2 {
		t2 = expandedT2
		retry = true
	}

	if retry {
		return c.Unify(ctx, t1, t2)
	}

	return []Error{&CannotUnifyTypesError{
		T1: t1,
		T2: t2,
	}}
}

// unifyFuncTypes unifies two function types
func (c *Checker) unifyFuncTypes(ctx Context, func1, func2 *type_system.FuncType) []Error {
	errors := []Error{}

	// For function types to be compatible:
	// 1. func2 can have fewer parameters than func1 (extra params in func1 can be ignored)
	// 2. Parameter types are contravariant: func2's param types must be supertypes of func1's
	// 3. Return types are covariant: func1's return type must be subtype of func2's
	// 4. Type parameters must be compatible

	// Check type parameters compatibility
	if len(func1.TypeParams) != len(func2.TypeParams) {
		return []Error{&CannotUnifyTypesError{
			T1: func1,
			T2: func2,
		}}
	}

	// Create a context for type parameter substitution
	// For now, we assume type parameters with the same position are equivalent
	// TODO: Handle more sophisticated type parameter constraints and bounds

	// Check parameters (contravariant)
	// Handle rest parameters: if func2 has a rest parameter, it can accept excess params from func1

	// Find if func1 and func2 have rest parameters
	var func1RestIndex = -1
	var func2RestIndex = -1

	for i, param := range func1.Params {
		if param.Pattern != nil {
			if _, isRest := param.Pattern.(*type_system.RestPat); isRest {
				func1RestIndex = i
				break
			}
		}
	}

	for i, param := range func2.Params {
		if param.Pattern != nil {
			if _, isRest := param.Pattern.(*type_system.RestPat); isRest {
				func2RestIndex = i
				break
			}
		}
	}

	if func1RestIndex != -1 && func2RestIndex != -1 {
		// Both functions have rest parameters
		// They must have the same number of fixed parameters and compatible rest types
		if func1RestIndex != func2RestIndex {
			return []Error{&CannotUnifyTypesError{
				T1: func1,
				T2: func2,
			}}
		}

		// Unify fixed parameters before the rest parameter
		for i := 0; i < func1RestIndex; i++ {
			param1 := func1.Params[i]
			param2 := func2.Params[i]

			// Parameter types are contravariant: unify param2.Type with param1.Type
			unifyErrors := c.Unify(ctx, param2.Type, param1.Type)
			errors = slices.Concat(errors, unifyErrors)

			// Optional parameter compatibility
			if param1.Optional && !param2.Optional {
				// This is fine - param2 is more restrictive
			} else if !param1.Optional && param2.Optional {
				// param1 requires the parameter but param2 makes it optional
				return []Error{&CannotUnifyTypesError{
					T1: func1,
					T2: func2,
				}}
			}
		}

		// Unify the rest parameters directly
		restParam1 := func1.Params[func1RestIndex]
		restParam2 := func2.Params[func2RestIndex]
		unifyErrors := c.Unify(ctx, restParam2.Type, restParam1.Type)
		errors = slices.Concat(errors, unifyErrors)

		// Check that both functions don't have parameters after rest (which shouldn't happen)
		if len(func1.Params) > func1RestIndex+1 || len(func2.Params) > func2RestIndex+1 {
			return []Error{&CannotUnifyTypesError{
				T1: func1,
				T2: func2,
			}}
		}

	} else if func2RestIndex != -1 {
		// Only func2 has a rest parameter at func2RestIndex
		// func1 must have at least as many fixed parameters as func2's fixed parameters
		if len(func1.Params) < func2RestIndex {
			return []Error{&CannotUnifyTypesError{
				T1: func1,
				T2: func2,
			}}
		}

		// Unify fixed parameters before the rest parameter
		for i := 0; i < func2RestIndex; i++ {
			param1 := func1.Params[i]
			param2 := func2.Params[i]

			// Parameter types are contravariant: unify param2.Type with param1.Type
			unifyErrors := c.Unify(ctx, param2.Type, param1.Type)
			errors = slices.Concat(errors, unifyErrors)

			// Optional parameter compatibility
			if param1.Optional && !param2.Optional {
				// This is fine - param2 is more restrictive
			} else if !param1.Optional && param2.Optional {
				// param1 requires the parameter but param2 makes it optional
				return []Error{&CannotUnifyTypesError{
					T1: func1,
					T2: func2,
				}}
			}
		}

		// Handle the rest parameter
		restParam := func2.Params[func2RestIndex]
		excessParamCount := len(func1.Params) - func2RestIndex

		if excessParamCount > 0 {
			// Collect excess parameters from func1
			excessParamTypes := make([]type_system.Type, excessParamCount)
			for i := 0; i < excessParamCount; i++ {
				excessParamTypes[i] = func1.Params[func2RestIndex+i].Type
			}

			// Create an Array type from excess parameters
			// We need to find a type that all excess parameters can unify to
			// For simplicity, we'll create a union of all excess parameter types
			elementType := type_system.NewUnionType(nil, excessParamTypes...)

			// Create Array<elementType> and unify with rest parameter type
			arrayType := type_system.NewTypeRefType(nil, "Array", nil, elementType)
			unifyErrors := c.Unify(ctx, restParam.Type, arrayType)
			errors = slices.Concat(errors, unifyErrors)
		} else {
			// No excess parameters, rest parameter should accept empty array
			// This is typically valid for rest parameters
		}

		// Check if there are any remaining parameters in func2 after the rest parameter
		// (This shouldn't happen if rest parameter is last, but handle it gracefully)
		if func2RestIndex+1 < len(func2.Params) {
			return []Error{&CannotUnifyTypesError{
				T1: func1,
				T2: func2,
			}}
		}
	} else {
		// Neither function has rest parameters, use original logic
		// func2 can have fewer parameters than func1
		if len(func2.Params) > len(func1.Params) {
			return []Error{&CannotUnifyTypesError{
				T1: func1,
				T2: func2,
			}}
		}

		// For each parameter in func2, the corresponding parameter in func1
		// must be unifiable (contravariant: func2 param type must be supertype of func1 param type)
		for i, param2 := range func2.Params {
			param1 := func1.Params[i]

			// Parameter types are contravariant: unify param2.Type with param1.Type
			unifyErrors := c.Unify(ctx, param2.Type, param1.Type)
			errors = slices.Concat(errors, unifyErrors)

			// Optional parameter compatibility
			// If param1 is optional, param2 can be either optional or required
			// If param1 is required, param2 must be required
			if param1.Optional && !param2.Optional {
				// This is fine - param2 is more restrictive
			} else if !param1.Optional && param2.Optional {
				// param1 requires the parameter but param2 makes it optional
				return []Error{&CannotUnifyTypesError{
					T1: func1,
					T2: func2,
				}}
			}
		}
	}

	// Check return types (covariant)
	if func1.Return != nil && func2.Return != nil {
		unifyErrors := c.Unify(ctx, func1.Return, func2.Return)
		errors = slices.Concat(errors, unifyErrors)
	} else if func1.Return == nil && func2.Return != nil {
		// func1 returns void/undefined, func2 expects a return type
		return []Error{&CannotUnifyTypesError{
			T1: func1,
			T2: func2,
		}}
	} else if func1.Return != nil && func2.Return == nil {
		// func1 returns something, func2 expects void - this might be OK
		// in some contexts (return value is ignored)
	}

	// Check throws types (covariant)
	if func1.Throws != nil && func2.Throws != nil {
		unifyErrors := c.Unify(ctx, func1.Throws, func2.Throws)
		errors = slices.Concat(errors, unifyErrors)
	} else if func1.Throws != nil && func2.Throws == nil {
		// func1 can throw but func2 doesn't expect throws - this might be an error
		// For now, we'll allow it (func2 doesn't handle the exception)
	} else if func1.Throws == nil && func2.Throws != nil {
		// func1 doesn't throw but func2 expects it might - this is fine
	}

	return errors
}

// TODO: check if t1 is already bound to an instance
// NOTE: be sure to call Prune on t1 and t2 before calling bind
// to ensure we are working with the most up-to-date types.
func (c *Checker) bind(ctx Context, t1 type_system.Type, t2 type_system.Type) []Error {
	if t1 == nil || t2 == nil {
		panic("Cannot bind nil types") // this should never happen
	}

	errors := []Error{}

	if !type_system.Equals(t1, t2) {
		if occursInType(t1, t2) {
			fmt.Fprintf(os.Stderr, "Recursive unification: cannot bind %s to %s\n", t1.String(), t2.String())
			return []Error{&RecursiveUnificationError{
				Left:  t1,
				Right: t2,
			}}
		} else {
			// There are three different cases:
			// - t1 and t2 are both type variables
			// - t1 is a type variable, t2 is a concrete type
			// - t1 is a concrete type, t2 is a type variable

			if typeVar1, ok := t1.(*type_system.TypeVarType); ok {
				if typeVar2, ok := t2.(*type_system.TypeVarType); ok {
					if typeVar1.Constraint != nil && typeVar2.Constraint != nil {
						errors = c.Unify(ctx, typeVar1.Constraint, typeVar2.Constraint)
					}
					typeVar1.Instance = t2
					typeVar1.SetProvenance(&type_system.TypeProvenance{
						Type: t2,
					})
					return errors
				}

				// If t2 is a type variable with a default type, and t1 is a union type,
				// we remove any `null` or `undefined` types from t1 and add the default type
				// to the union if it's not already present.  This handles identifiers in
				// patterns that have default types such as in:
				//   let { a = 42 } : { a?: number } = obj;
				if typeVar1.Default != nil {
					if union, ok := t2.(*type_system.UnionType); ok {
						definedTypes := c.getDefinedElems(union)

						if len(union.Types) > len(definedTypes) {
							definedTypes = append(definedTypes, typeVar1.Default)
							t2 = type_system.NewUnionType(nil, definedTypes...)
						}
					}
				}

				if typeVar1.Constraint != nil {
					errors = c.Unify(ctx, typeVar1.Constraint, t2)
				}
				// We need to know if typeVar1 was inferred from a new binding or not
				if typeVar1.FromBinding {
					typeVar1.Instance = removeUncertainMutability(t2)
				} else {
					typeVar1.Instance = t2
				}
				// QUESTION: What should the provenance be if t2 is a type_system.MutabilityType?
				typeVar1.SetProvenance(&type_system.TypeProvenance{
					Type: t2,
				})
				return errors
			}

			if typeVar2, ok := t2.(*type_system.TypeVarType); ok {
				// If t2 is a type variable with a default type, and t1 is a union type,
				// we remove any `null` or `undefined` types from t1 and add the default type
				// to the union if it's not already present.  This handles identifiers in
				// patterns that have default types such as in:
				//   let { a = 42 } : { a?: number } = obj;
				if typeVar2.Default != nil {
					if union, ok := t1.(*type_system.UnionType); ok {
						definedTypes := c.getDefinedElems(union)

						if len(union.Types) > len(definedTypes) {
							definedTypes = append(definedTypes, typeVar2.Default)
							t1 = type_system.NewUnionType(nil, definedTypes...)
						}
					}
				}

				if typeVar2.Constraint != nil {
					errors = c.Unify(ctx, t1, typeVar2.Constraint)
				}
				// We need to know if typeVar2 was inferred from a new binding or not
				if typeVar2.FromBinding {
					typeVar2.Instance = removeUncertainMutability(t1)
				} else {
					typeVar2.Instance = t1
				}
				// QUESTION: What should the provenance be if t1 is a type_system.MutabilityType?
				typeVar2.SetProvenance(&type_system.TypeProvenance{
					Type: t1,
				})
				return errors
			}
		}
	}

	return errors
}

type OccursInVisitor struct {
	result bool
	t1     type_system.Type
}

func (v *OccursInVisitor) EnterType(t type_system.Type) type_system.Type {
	// No-op for entry
	return nil
}

func (v *OccursInVisitor) ExitType(t type_system.Type) type_system.Type {
	if type_system.Equals(type_system.Prune(t), v.t1) {
		v.result = true
	}
	return nil
}

func occursInType(t1, t2 type_system.Type) bool {
	visitor := &OccursInVisitor{result: false, t1: t1}
	t2.Accept(visitor)
	return visitor.result
}

type RemoveUncertainMutabilityVisitor struct{}

func (v *RemoveUncertainMutabilityVisitor) EnterType(t type_system.Type) type_system.Type {
	// No-op for entry
	return nil
}

func (v *RemoveUncertainMutabilityVisitor) ExitType(t type_system.Type) type_system.Type {
	// If this is a type_system.MutabilityType with uncertain mutability, unwrap it
	if mut, ok := t.(*type_system.MutabilityType); ok && mut.Mutability == type_system.MutabilityUncertain {
		return mut.Type
	}
	return nil
}

func removeUncertainMutability(t type_system.Type) type_system.Type {
	visitor := &RemoveUncertainMutabilityVisitor{}
	result := t.Accept(visitor)
	return result
}
