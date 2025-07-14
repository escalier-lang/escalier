package checker

import (
	"fmt"
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
	if tv1, ok := t1.(*TypeVarType); ok {
		return c.bind(tv1, t2)
	}
	// | _, TypeVarType -> ...
	if tv2, ok := t2.(*TypeVarType); ok {
		return c.bind(tv2, t1)
	}
	// | PrimType, PrimType -> ...
	if prim1, ok := t1.(*PrimType); ok {
		if prim2, ok := t2.(*PrimType); ok {
			if prim1.Equal(prim2) {
				return nil
			}
		}
	}
	// | WildcardType, _ -> ...
	if _, ok := t1.(*WildcardType); ok {
		return nil
	}
	// | _, WildcardType -> ...
	if _, ok := t2.(*WildcardType); ok {
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
	// | UnknownType, _ -> ...
	if _, ok := t2.(*UnknownType); ok {
		return nil
	}
	// | TupleType, TupleType -> ...
	if tuple1, ok := t1.(*TupleType); ok {
		if tuple2, ok := t2.(*TupleType); ok {
			// TODO: handle spread
			errors := []Error{}

			// TODO: Don't allow more than one rest element in tuple1
			restElem, ok := tuple2.Elems[len(tuple2.Elems)-1].(*RestSpreadType)
			if ok {
				elems2 := tuple2.Elems[:len(tuple2.Elems)-1]
				elems1 := tuple1.Elems[:len(elems2)]

				for elem1, elem2 := range Zip(elems1, elems2) {
					unifyErrors := c.unify(ctx, elem1, elem2)
					errors = slices.Concat(errors, unifyErrors)
				}
				remainingElems := tuple1.Elems[len(elems2):]
				tuple := &TupleType{
					Elems: remainingElems,
				}
				unifyErrors := c.unify(ctx, restElem.Type, tuple)
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
					undefinedType := NewLitType(&UndefinedLit{})
					node := GetNode(elem2.Provenance())
					// We set the provenance just in case it's needed for error
					// reporting.
					undefinedType.SetProvenance(&ast.NodeProvenance{
						Node: node,
					})
					unifyErrors := c.unify(ctx, elem2, undefinedType)
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
	if tuple1, ok := t2.(*TupleType); ok {
		if array2, ok := t2.(*TypeRefType); ok && array2.Name == "Array" {
			panic(fmt.Sprintf("TODO: unify types %#v and %#v", tuple1, array2))
			// TODO
		}
	}
	// | ArrayType, TupleType -> ...
	if array1, ok := t1.(*TypeRefType); ok && array1.Name == "Array" {
		if tuple2, ok := t2.(*TupleType); ok {
			panic(fmt.Sprintf("TODO: unify types %#v and %#v", array1, tuple2))
			// TODO
		}
	}
	// | ArrayType, ArrayType -> ...
	if array1, ok := t1.(*TypeRefType); ok && array1.Name == "Array" {
		if array2, ok := t2.(*TypeRefType); ok && array2.Name == "Array" {
			panic(fmt.Sprintf("TODO: unify types %#v and %#v", array1, array2))
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
			panic(fmt.Sprintf("TODO: unify types %#v and %#v", func1.String(), func2.String()))
			// TODO
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
					typeAlias1 = ctx.Scope.getTypeAlias(ref1.Name)
					if typeAlias1 == nil {
						return []Error{&UnkonwnTypeError{
							TypeName: ref1.Name,
							typeRef:  ref1,
						}}
					}
				}
				typeAlias2 := ref2.TypeAlias
				if typeAlias2 == nil {
					typeAlias2 = ctx.Scope.getTypeAlias(ref2.Name)
					if typeAlias2 == nil {
						return []Error{&UnkonwnTypeError{
							TypeName: ref2.Name,
							typeRef:  ref2,
						}}
					}
				}

				return c.unify(ctx, typeAlias1.Type, typeAlias2.Type)
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
			if unique1.Equal(unique2) {
				return nil
			} else {
				return []Error{&CannotUnifyTypesError{
					T1: unique1,
					T2: unique2,
				}}
			}
		}
	}
	// | ObjectType, ExtractType -> ...
	if obj, ok := t1.(*ObjectType); ok {
		if ext, ok := t2.(*ExtractorType); ok {
			panic(fmt.Sprintf("TODO: unify types %#v and %#v", obj, ext))
			// TODO
		}
	}
	// | ExtractType, ObjectType -> ...
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
			keys1 := []ObjTypeKey{} // original order of keys in obj2

			var restType Type

			for _, elem := range obj1.Elems {
				switch elem := elem.(type) {
				case *MethodElemType:
					namedElems1[elem.Name] = elem.Fn
					keys1 = append(keys1, elem.Name)
				case *GetterElemType:
					namedElems1[elem.Name] = elem.Fn.Return
					keys1 = append(keys1, elem.Name)
				case *SetterElemType:
					namedElems1[elem.Name] = elem.Fn.Params[0].Type
					keys1 = append(keys1, elem.Name)
				case *PropertyElemType:
					namedElems1[elem.Name] = elem.Value
					keys1 = append(keys1, elem.Name)
				default: // skip other types of elems
				}
			}

			for _, elem := range obj2.Elems {
				switch elem := elem.(type) {
				case *MethodElemType:
					namedElems2[elem.Name] = elem.Fn
				case *GetterElemType:
					namedElems2[elem.Name] = elem.Fn.Return
				case *SetterElemType:
					namedElems2[elem.Name] = elem.Fn.Params[0].Type
				case *PropertyElemType:
					namedElems2[elem.Name] = elem.Value
				case *RestSpreadElemType:
					restType = elem.Value
				default: // skip other types of elems
				}
			}

			usedKeys := map[ObjTypeKey]bool{}
			for key2, value2 := range namedElems2 {
				if value1, ok := namedElems1[key2]; ok {
					unifyErrors := c.unify(ctx, value1, value2)
					errors = slices.Concat(errors, unifyErrors)
					usedKeys[key2] = true
				} else {
					errors = slices.Concat(errors, []Error{&KeyNotFoundError{
						Object: obj1,
						Key:    key2,
					}})
				}
			}

			if restType != nil {
				restElems := []ObjTypeElem{}
				for _, key := range keys1 {
					if _, ok := usedKeys[key]; !ok {
						restElems = append(restElems, &PropertyElemType{
							Name:     key,
							Optional: false, // TODO
							Readonly: false, // TODO
							Value:    namedElems1[key],
						})
					}
				}

				objType := NewObjectType(restElems)

				unifyErrors := c.unify(ctx, restType, objType)
				errors = slices.Concat(errors, unifyErrors)
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
		panic(fmt.Sprintf("TODO: unify types %#v and %#v", union, t2))
		// TODO
	}
	// | _, UnionType -> ...
	if union, ok := t2.(*UnionType); ok {
		panic(fmt.Sprintf("TODO: unify types %#v and %#v", t1, union))
	}

	retry := false
	if typeRef, ok := t1.(*TypeRefType); ok {
		if alias := ctx.Scope.getTypeAlias(typeRef.Name); alias != nil {
			// TODO: apply type args
			t1 = alias.Type
			retry = true
		}
	}
	if typeRef, ok := t2.(*TypeRefType); ok {
		if alias := ctx.Scope.getTypeAlias(typeRef.Name); alias != nil {
			// TODO: apply type args
			t2 = alias.Type
			retry = true
		}
	}

	if retry {
		return c.unify(ctx, t1, t2)
	}

	// TODO: try to expand each type and then try to unify them again
	panic(fmt.Sprintf("TODO: unify types %s and %s", t1.String(), t2.String()))
}

// TODO: check if t1 is already bound to an instance
func (c *Checker) bind(t1 *TypeVarType, t2 Type) []Error {
	if t1 == nil || t2 == nil {
		panic("Cannot bind nil types") // this should never happen
	}

	if !t1.Equal(t2) {
		if occursInType(t1, t2) {
			return []Error{&RecursiveUnificationError{
				Left:  t1,
				Right: t2,
			}}
		} else {
			t1.Instance = t2
			t1.SetProvenance(&TypeProvenance{
				Type: t2,
			})
		}
	}

	return nil
}

type OccursInVisitor struct {
	result bool
	t1     Type
}

func (v *OccursInVisitor) VisitType(t Type) {
	if Prune(t).Equal(v.t1) {
		v.result = true
	}
}

func occursInType(t1, t2 Type) bool {
	visitor := &OccursInVisitor{result: false, t1: t1}
	t2.Accept(visitor)
	return visitor.result
}
