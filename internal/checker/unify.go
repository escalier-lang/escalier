package checker

import (
	"fmt"
	"os"
	"slices"

	"github.com/escalier-lang/escalier/internal/ast"
	. "github.com/escalier-lang/escalier/internal/type_system"
)

// If `unify` doesn't return an error it means that `t1` is a subtype of `t2` or
// they are the same type.
func (c *Checker) unify(ctx Context, t1, t2 Type) []Error {
	if t1 == nil || t2 == nil {
		panic("Cannot unify nil types")
	}

	t1 = Prune(t1)
	t2 = Prune(t2)

	// fmt.Fprintf(os.Stderr, "Unifying types %s and %s\n", t1, t2)

	// | TypeVarType, _ -> ...
	if _, ok := t1.(*TypeVarType); ok {
		return c.bind(ctx, t1, t2)
	}
	// | _, TypeVarType -> ...
	if _, ok := t2.(*TypeVarType); ok {
		return c.bind(ctx, t1, t2)
	}
	// TODO: Unification of mutable types with mutable types should be invariant
	// | MutableType, MutableType -> ...
	if mut1, ok := t1.(*MutableType); ok {
		if mut2, ok := t2.(*MutableType); ok {
			// MutableType can be unified with another MutableType
			// by unifying their underlying types
			return c.unifyMut(ctx, mut1, mut2)
		}
	}
	// TODO: This should only be allowed if the value being referenced has no
	// immutable references (i.e. the lifetime of any immutable references has
	// ended).
	// | _, MutableType -> ...
	if mut, ok := t2.(*MutableType); ok {
		return c.unify(ctx, t1, mut.Type)
	}
	// TODO: This should only be allowed if the value being referenced has no
	// immutable references (i.e. the lifetime of any immutable references has
	// ended).
	// NOTE: This avoids issues where a mutable reference will modify the value
	// while the immutable reference is still using it.
	// | MutableType, _ -> ...
	if mut, ok := t1.(*MutableType); ok {
		return c.unify(ctx, mut.Type, t2)
	}
	// | PrimType, PrimType -> ...
	if prim1, ok := t1.(*PrimType); ok {
		if prim2, ok := t2.(*PrimType); ok {
			if Equals(prim1, prim2) {
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
	if _, ok := t1.(*AnyType); ok {
		return nil
	}
	// | _, AnyType -> ...
	if _, ok := t2.(*AnyType); ok {
		return nil
	}
	// | WildcardType, _ -> ...
	if _, ok := t1.(*WildcardType); ok {
		return nil
	}
	// | _, WildcardType -> ...
	if _, ok := t2.(*WildcardType); ok {
		return nil
	}
	// | UnknownType, UnknownType -> ...
	if _, ok := t1.(*UnknownType); ok {
		if _, ok := t2.(*UnknownType); ok {
			return nil
		}
		// UnknownType cannot be assigned to other types
		return []Error{&CannotUnifyTypesError{
			T1: t1,
			T2: t2,
		}}
	}
	// | _, UnknownType -> ...
	if _, ok := t2.(*UnknownType); ok {
		// All types can be assigned to UnknownType
		return nil
	}
	// | NeveType, _ -> ...
	if _, ok := t1.(*NeverType); ok {
		return nil
	}
	// | _, NeverType -> ...
	if _, ok := t2.(*NeverType); ok {
		return []Error{&CannotUnifyTypesError{
			T1: t1,
			T2: t2,
		}}
	}
	// | TupleType, TupleType -> ...
	if tuple1, ok := t1.(*TupleType); ok {
		if tuple2, ok := t2.(*TupleType); ok {
			// TODO: handle spread
			errors := []Error{}

			// TODO: Don't allow more than one rest element in tuple1
			restElem2, ok := tuple2.Elems[len(tuple2.Elems)-1].(*RestSpreadType)
			if ok {
				elems2 := tuple2.Elems[:len(tuple2.Elems)-1]
				elems1 := tuple1.Elems[:len(elems2)]

				for elem1, elem2 := range Zip(elems1, elems2) {
					unifyErrors := c.unify(ctx, elem1, elem2)
					errors = slices.Concat(errors, unifyErrors)
				}
				remainingElems := tuple1.Elems[len(elems2):]
				tuple := NewTupleType(nil, remainingElems...)
				unifyErrors := c.unify(ctx, tuple, restElem2.Type)
				errors = slices.Concat(errors, unifyErrors)
				return errors
			}

			restElem1, ok := tuple1.Elems[len(tuple1.Elems)-1].(*RestSpreadType)
			if ok {
				elems1 := tuple1.Elems[:len(tuple1.Elems)-1]
				elems2 := tuple2.Elems[:len(elems1)]

				for elem1, elem2 := range Zip(elems1, elems2) {
					unifyErrors := c.unify(ctx, elem1, elem2)
					errors = slices.Concat(errors, unifyErrors)
				}
				remainingElems := tuple2.Elems[len(elems1):]
				tuple := NewTupleType(nil, remainingElems...)
				unifyErrors := c.unify(ctx, restElem1.Type, tuple)
				errors = slices.Concat(errors, unifyErrors)
				return errors
			}

			if len(tuple2.Elems) > len(tuple1.Elems) {
				// Unify the elements that are present in both tuples
				for elem1, elem2 := range Zip(tuple1.Elems, tuple2.Elems) {
					unifyErrors := c.unify(ctx, elem1, elem2)
					errors = slices.Concat(errors, unifyErrors)
				}

				extraElems := tuple2.Elems[len(tuple1.Elems):]
				first := GetNode(extraElems[0].Provenance())
				last := GetNode(extraElems[len(extraElems)-1].Provenance())

				// Any remaining elements in tuple2 should be typed as `undefined`
				// since they are not present in tuple1.
				for _, elem2 := range extraElems {
					node := GetNode(elem2.Provenance())
					undefined := NewUndefinedType(&ast.NodeProvenance{Node: node})
					unifyErrors := c.unify(ctx, elem2, undefined)
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
				unifyErrors := c.unify(ctx, elem1, elem2)
				errors = slices.Concat(errors, unifyErrors)
			}

			return errors
		}
	}
	// | TupleType, ArrayType -> ...
	if tuple1, ok := t1.(*TupleType); ok {
		if array2, ok := t2.(*TypeRefType); ok && array2.Name == "Array" {
			// A tuple can be unified with an array if all tuple elements
			// can be unified with the array's element type
			if len(array2.TypeArgs) == 1 {
				errors := []Error{}
				for _, elem := range tuple1.Elems {
					unifyErrors := c.unify(ctx, elem, array2.TypeArgs[0])
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
	if array1, ok := t1.(*TypeRefType); ok && array1.Name == "Array" {
		if tuple2, ok := t2.(*TupleType); ok {
			// An array can be unified with a tuple if the array's element type
			// can be unified with all tuple elements
			if len(array1.TypeArgs) == 1 {
				errors := []Error{}
				for _, elem := range tuple2.Elems {
					unifyErrors := c.unify(ctx, array1.TypeArgs[0], elem)
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
	if array1, ok := t1.(*TypeRefType); ok && array1.Name == "Array" {
		if array2, ok := t2.(*TypeRefType); ok && array2.Name == "Array" {
			// Both are Array types, unify their element types
			if len(array1.TypeArgs) == 1 && len(array2.TypeArgs) == 1 {
				return c.unify(ctx, array1.TypeArgs[0], array2.TypeArgs[0])
			}
			// If either array doesn't have exactly one type argument, they can't be unified
			return []Error{&CannotUnifyTypesError{
				T1: array1,
				T2: array2,
			}}
		}
	}
	// | RestSpreadType, ArrayType -> ...
	if rest, ok := t1.(*RestSpreadType); ok {
		if array, ok := t2.(*TypeRefType); ok && array.Name == "Array" {
			return c.unify(ctx, rest.Type, array)
		}
	}
	// | FuncType, FuncType -> ...
	if func1, ok := t1.(*FuncType); ok {
		if func2, ok := t2.(*FuncType); ok {
			return c.unifyFuncTypes(ctx, func1, func2)
		}
	}
	// | TypeRefType, TypeRefType (same alias name) -> ...
	if ref1, ok := t1.(*TypeRefType); ok {
		if ref2, ok := t2.(*TypeRefType); ok && ref1.Name == ref2.Name {
			if len(ref1.TypeArgs) == 0 && len(ref2.TypeArgs) == 0 {
				// If both type references have no type arguments, we can unify them
				// directly.

				// Most of the time, type references will have their TypeAlias
				// field set to whatever type alias they refer to when they were
				// created.  However, certain type references such as those used
				// for type parameters in generics may not have this field set.

				typeAlias1 := ref1.TypeAlias
				if typeAlias1 == nil {
					typeAlias1 = c.resolveQualifiedTypeAliasFromString(ctx, ref1.Name)
					if typeAlias1 == nil {
						return []Error{&UnknownTypeError{
							TypeName: ref1.Name,
							typeRef:  ref1,
						}}
					}
				}
				typeAlias2 := ref2.TypeAlias
				if typeAlias2 == nil {
					typeAlias2 = c.resolveQualifiedTypeAliasFromString(ctx, ref2.Name)
					if typeAlias2 == nil {
						return []Error{&UnknownTypeError{
							TypeName: ref2.Name,
							typeRef:  ref2,
						}}
					}
				}

				return []Error{}
				// TODO: Give each TypeAlias a unique ID and if they so avoid
				// situations where two different type aliases have the same
				// name but different definitions.
				// return c.unify(ctx, typeAlias1.Type, typeAlias2.Type)
			}

			// TODO: handle type args
			// We need to replace type type params in the type alias's type
			// with the type args from the type reference.
			panic(fmt.Sprintf("TODO: unify types %s and %s", ref1.String(), ref2.String()))
		}
	}
	// | TypeRefType, TypeRefType (different alias name) -> ...
	if ref1, ok := t1.(*TypeRefType); ok {
		if ref2, ok := t2.(*TypeRefType); ok && ref1.Name != ref2.Name {
			panic(fmt.Sprintf("TODO: unify types %#v and %#v", ref1, ref2))
			// TODO
		}
	}
	// | LitType, PrimType -> ...
	if lit, ok := t1.(*LitType); ok {
		if prim, ok := t2.(*PrimType); ok {
			if _, ok := lit.Lit.(*NumLit); ok && prim.Prim == "number" {
				return nil
			} else if _, ok := lit.Lit.(*StrLit); ok && prim.Prim == "string" {
				return nil
			} else if _, ok := lit.Lit.(*BoolLit); ok && prim.Prim == "boolean" {
				return nil
			} else if _, ok := lit.Lit.(*BigIntLit); ok && prim.Prim == "bigint" {
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
	if lit1, ok := t1.(*LitType); ok {
		if lit2, ok := t2.(*LitType); ok {
			if l1, ok := lit1.Lit.(*NumLit); ok {
				if l2, ok := lit2.Lit.(*NumLit); ok {
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
			if l1, ok := lit1.Lit.(*StrLit); ok {
				if l2, ok := lit2.Lit.(*StrLit); ok {
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
			if l1, ok := lit1.Lit.(*BoolLit); ok {
				if l2, ok := lit2.Lit.(*BoolLit); ok {
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
			if _, ok := lit1.Lit.(*UndefinedLit); ok {
				if _, ok := lit2.Lit.(*UndefinedLit); ok {
					return nil
				}
			}
			if _, ok := lit1.Lit.(*NullLit); ok {
				if _, ok := lit2.Lit.(*NullLit); ok {
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
	if regex1, ok := t1.(*RegexType); ok {
		if regex2, ok := t2.(*RegexType); ok {
			if Equals(regex1, regex2) {
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
	if lit, ok := t1.(*LitType); ok {
		if regexType, ok := t2.(*RegexType); ok {
			if strLit, ok := lit.Lit.(*StrLit); ok {
				matches := regexType.Regex.FindStringSubmatch(strLit.Value)
				if matches != nil {
					groupNames := regexType.Regex.SubexpNames()
					errors := []Error{}

					for i, name := range groupNames {
						if name != "" {
							groupErrors := c.unify(
								ctx,
								NewStrLitType(matches[i], nil),
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
	if lit, ok := t1.(*LitType); ok {
		if template, ok := t2.(*TemplateLitType); ok {
			if strLit, ok := lit.Lit.(*StrLit); ok {
				panic(fmt.Sprintf("TODO: unify types %#v and %#v", strLit, template))
				// TODO
			}
		}
	}
	// | UniqueSymbolType, UniqueSymbolType -> ...
	if unique1, ok := t1.(*UniqueSymbolType); ok {
		if unique2, ok := t2.(*UniqueSymbolType); ok {
			if Equals(unique1, unique2) {
				return nil
			} else {
				return []Error{&CannotUnifyTypesError{
					T1: unique1,
					T2: unique2,
				}}
			}
		}
	}
	// | _, ExtractorType -> ...
	if ext, ok := t2.(*ExtractorType); ok {
		if extObj, ok := ext.Extractor.(*ObjectType); ok {
			for _, elem := range extObj.Elems {
				if methodElem, ok := elem.(*MethodElem); ok {
					// TODO: look up the symbol ID for `Symbol.customMatcher`
					if methodElem.Name.Kind == SymObjTypeKeyKind && methodElem.Name.Sym == 2 {
						if len(methodElem.Fn.Params) != 1 {
							return []Error{&IncorrectParamCountForCustomMatcherError{
								Method:    methodElem.Fn,
								NumParams: len(methodElem.Fn.Params),
							}}
						}

						paramType := methodElem.Fn.Params[0].Type
						errors := c.unify(ctx, t1, paramType)

						if tuple, ok := methodElem.Fn.Return.(*TupleType); ok {
							// Find if the args have a rest element
							var restIndex = -1
							for i, elem := range ext.Args {
								if _, isRest := elem.(*RestSpreadType); isRest {
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
									argErrors := c.unify(ctx, tuple.Elems[i], ext.Args[i])
									errors = slices.Concat(errors, argErrors)
								}

								// Unify rest arguments with rest element type
								if len(ext.Args) > restIndex {
									restElem := ext.Args[restIndex].(*RestSpreadType)
									remainingArgsTupleType := NewTupleType(nil, tuple.Elems[restIndex:]...)

									restErrors := c.unify(ctx, restElem.Type, remainingArgsTupleType)
									errors = slices.Concat(errors, restErrors)
								}
							} else {
								// Tuple has no rest element, use strict equality check
								if len(tuple.Elems) == len(ext.Args) {
									for retElem, argType := range Zip(tuple.Elems, ext.Args) {
										argErrors := c.unify(ctx, retElem, argType)
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
	// }
	// | ExtractorType, ObjectType -> ...
	if ext, ok := t1.(*ExtractorType); ok {
		if obj, ok := t2.(*ObjectType); ok {
			panic(fmt.Sprintf("TODO: unify types %#v and %#v", ext, obj))
			// TODO
		}
	}
	// | ObjectType, ObjectType -> ...
	if obj1, ok := t1.(*ObjectType); ok {
		if obj2, ok := t2.(*ObjectType); ok {

			// TODO: handle exactness
			// TODO: handle unnamed elems, e.g. callable and newable signatures
			// TODO: handle spread
			// TODO: handle mapped type elems
			// TODO: handle getters/setters appropriately (we need to know which
			// type is being read from and which is being written to... does that
			// question even make sense?)

			errors := []Error{}

			namedElems1 := make(map[ObjTypeKey]Type)
			namedElems2 := make(map[ObjTypeKey]Type)

			keys1 := []ObjTypeKey{} // original order of keys in obj1
			keys2 := []ObjTypeKey{} // original order of keys in obj2

			var restType1 Type
			var restType2 Type

			for _, elem := range obj1.Elems {
				switch elem := elem.(type) {
				case *MethodElem:
					namedElems1[elem.Name] = elem.Fn
					keys1 = append(keys1, elem.Name)
				case *GetterElem:
					namedElems1[elem.Name] = elem.Fn.Return
					keys1 = append(keys1, elem.Name)
				case *SetterElem:
					namedElems1[elem.Name] = elem.Fn.Params[0].Type
					keys1 = append(keys1, elem.Name)
				case *PropertyElem:
					propType := elem.Value
					if elem.Optional {
						propType = NewUnionType(nil, propType, NewUndefinedType(nil))
					}
					namedElems1[elem.Name] = propType
					keys1 = append(keys1, elem.Name)
				case *RestSpreadElem:
					restType1 = elem.Value
				default: // skip other types of elems
				}
			}

			for _, elem := range obj2.Elems {
				switch elem := elem.(type) {
				case *MethodElem:
					namedElems2[elem.Name] = elem.Fn
					keys2 = append(keys2, elem.Name)
				case *GetterElem:
					namedElems2[elem.Name] = elem.Fn.Return
					keys2 = append(keys2, elem.Name)
				case *SetterElem:
					namedElems2[elem.Name] = elem.Fn.Params[0].Type
					keys2 = append(keys2, elem.Name)
				case *PropertyElem:
					propType := elem.Value
					if elem.Optional {
						propType = NewUnionType(nil, propType, NewUndefinedType(nil))
					}
					namedElems2[elem.Name] = propType
					keys2 = append(keys2, elem.Name)
				case *RestSpreadElem:
					restType2 = elem.Value
				default: // skip other types of elems
				}
			}

			if restType1 != nil && restType2 != nil {
				return []Error{&UnimplementedError{message: "unify types with two rest elems"}}
			} else if restType1 != nil {
				usedKeys2 := map[ObjTypeKey]bool{}
				for key1, value1 := range namedElems1 {
					if value2, ok := namedElems2[key1]; ok {
						unifyErrors := c.unify(ctx, value1, value2)
						errors = slices.Concat(errors, unifyErrors)
						usedKeys2[key1] = true
					} else {
						errors = slices.Concat(errors, []Error{&KeyNotFoundError{
							Object: obj2,
							Key:    key1,
						}})
					}
				}

				restElems := []ObjTypeElem{}
				for _, key := range keys2 {
					if _, ok := usedKeys2[key]; !ok {
						restElems = append(restElems, &PropertyElem{
							Name:     key,
							Optional: false, // TODO
							Readonly: false, // TODO
							Value:    namedElems2[key],
						})
					}
				}

				objType := NewObjectType(nil, restElems)

				unifyErrors := c.unify(ctx, objType, restType1)
				errors = slices.Concat(errors, unifyErrors)
			} else if restType2 != nil {
				usedKeys1 := map[ObjTypeKey]bool{}
				for key2, value2 := range namedElems2 {
					if value1, ok := namedElems1[key2]; ok {
						unifyErrors := c.unify(ctx, value1, value2)
						errors = slices.Concat(errors, unifyErrors)
						usedKeys1[key2] = true
					} else {
						errors = slices.Concat(errors, []Error{&KeyNotFoundError{
							Object: obj1,
							Key:    key2,
						}})
					}
				}

				if restType2 != nil {
					restElems := []ObjTypeElem{}
					for _, key := range keys1 {
						if _, ok := usedKeys1[key]; !ok {
							restElems = append(restElems, &PropertyElem{
								Name:     key,
								Optional: false, // TODO
								Readonly: false, // TODO
								Value:    namedElems1[key],
							})
						}
					}

					objType := NewObjectType(nil, restElems)

					unifyErrors := c.unify(ctx, restType2, objType)
					errors = slices.Concat(errors, unifyErrors)
				}
			} else {
				for key2, value2 := range namedElems2 {
					if value1, ok := namedElems1[key2]; ok {
						unifyErrors := c.unify(ctx, value1, value2)
						errors = slices.Concat(errors, unifyErrors)
					} else {
						errors = slices.Concat(errors, []Error{&KeyNotFoundError{
							Object: obj1,
							Key:    key2,
						}})
					}
				}
			}

			return errors
		}
	}
	// | IntersectionType, ObjectType -> ...
	if intersection, ok := t1.(*IntersectionType); ok {
		if obj, ok := t2.(*ObjectType); ok {
			panic(fmt.Sprintf("TODO: unify types %#v and %#v", intersection, obj))
		}
	}
	// | ObjectType, UnionType -> ...
	if obj, ok := t1.(*ObjectType); ok {
		if union, ok := t2.(*UnionType); ok {
			panic(fmt.Sprintf("TODO: unify types %#v and %#v", obj, union))
			// TODO
		}
	}
	// | IntersectionType, IntersectionType -> ...
	if intersection1, ok := t1.(*IntersectionType); ok {
		if intersection2, ok := t2.(*IntersectionType); ok {
			panic(fmt.Sprintf("TODO: unify types %#v and %#v", intersection1, intersection2))
			// TODO
		}
	}
	// | UnionType, _ -> ...
	if union, ok := t1.(*UnionType); ok {
		// All types in the union must be compatible with t2
		for _, unionType := range union.Types {
			unifyErrors := c.unify(ctx, unionType, t2)
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
	if union, ok := t2.(*UnionType); ok {
		// Try to unify t1 with any type in the union
		for _, unionType := range union.Types {
			// Try unifying - if any unification succeeds, we're good
			unifyErrors := c.unify(ctx, t1, unionType)
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
	expandedT1, _ := c.expandType(ctx, t1)
	if expandedT1 != t1 {
		t1 = expandedT1
		retry = true
	}
	expandedT2, _ := c.expandType(ctx, t2)
	if expandedT2 != t2 {
		t2 = expandedT2
		retry = true
	}

	if retry {
		return c.unify(ctx, t1, t2)
	}

	return []Error{&CannotUnifyTypesError{
		T1: t1,
		T2: t2,
	}}
}

// unifyFuncTypes unifies two function types
func (c *Checker) unifyFuncTypes(ctx Context, func1, func2 *FuncType) []Error {
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
			if _, isRest := param.Pattern.(*RestPat); isRest {
				func1RestIndex = i
				break
			}
		}
	}

	for i, param := range func2.Params {
		if param.Pattern != nil {
			if _, isRest := param.Pattern.(*RestPat); isRest {
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
			unifyErrors := c.unify(ctx, param2.Type, param1.Type)
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
		unifyErrors := c.unify(ctx, restParam2.Type, restParam1.Type)
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
			unifyErrors := c.unify(ctx, param2.Type, param1.Type)
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
			excessParamTypes := make([]Type, excessParamCount)
			for i := 0; i < excessParamCount; i++ {
				excessParamTypes[i] = func1.Params[func2RestIndex+i].Type
			}

			// Create an Array type from excess parameters
			// We need to find a type that all excess parameters can unify to
			// For simplicity, we'll create a union of all excess parameter types
			elementType := NewUnionType(nil, excessParamTypes...)

			// Create Array<elementType> and unify with rest parameter type
			arrayType := NewTypeRefType(nil, "Array", nil, elementType)
			unifyErrors := c.unify(ctx, restParam.Type, arrayType)
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
			unifyErrors := c.unify(ctx, param2.Type, param1.Type)
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
		unifyErrors := c.unify(ctx, func1.Return, func2.Return)
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
		unifyErrors := c.unify(ctx, func1.Throws, func2.Throws)
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
func (c *Checker) bind(ctx Context, t1 Type, t2 Type) []Error {
	if t1 == nil || t2 == nil {
		panic("Cannot bind nil types") // this should never happen
	}

	errors := []Error{}

	if !Equals(t1, t2) {
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

			if typeVar1, ok := t1.(*TypeVarType); ok {
				if typeVar2, ok := t2.(*TypeVarType); ok {
					if typeVar1.Constraint != nil && typeVar2.Constraint != nil {
						errors = c.unify(ctx, typeVar1.Constraint, typeVar2.Constraint)
					}
					typeVar1.Instance = t2
					typeVar1.SetProvenance(&TypeProvenance{
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
					if union, ok := t2.(*UnionType); ok {
						definedTypes := c.getDefinedElems(union)

						if len(union.Types) > len(definedTypes) {
							definedTypes = append(definedTypes, typeVar1.Default)
							t2 = NewUnionType(nil, definedTypes...)
						}
					}
				}

				if typeVar1.Constraint != nil {
					errors = c.unify(ctx, typeVar1.Constraint, t2)
				}
				typeVar1.Instance = t2
				typeVar1.SetProvenance(&TypeProvenance{
					Type: t2,
				})
				return errors
			}

			if typeVar2, ok := t2.(*TypeVarType); ok {
				// If t2 is a type variable with a default type, and t1 is a union type,
				// we remove any `null` or `undefined` types from t1 and add the default type
				// to the union if it's not already present.  This handles identifiers in
				// patterns that have default types such as in:
				//   let { a = 42 } : { a?: number } = obj;
				if typeVar2.Default != nil {
					if union, ok := t1.(*UnionType); ok {
						definedTypes := c.getDefinedElems(union)

						if len(union.Types) > len(definedTypes) {
							definedTypes = append(definedTypes, typeVar2.Default)
							t1 = NewUnionType(nil, definedTypes...)
						}
					}
				}

				if typeVar2.Constraint != nil {
					errors = c.unify(ctx, t1, typeVar2.Constraint)
				}
				typeVar2.Instance = t1
				typeVar2.SetProvenance(&TypeProvenance{
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
	t1     Type
}

func (v *OccursInVisitor) EnterType(t Type) Type {
	// No-op for entry
	return nil
}

func (v *OccursInVisitor) ExitType(t Type) Type {
	if Equals(Prune(t), v.t1) {
		v.result = true
	}
	return nil
}

func occursInType(t1, t2 Type) bool {
	visitor := &OccursInVisitor{result: false, t1: t1}
	t2.Accept(visitor)
	return visitor.result
}
