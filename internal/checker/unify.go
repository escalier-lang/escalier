package checker

import (
	"fmt"

	"github.com/escalier-lang/escalier/internal/ast"
)

func match[T1 ast.Type, T2 ast.Type](t1, t2 ast.Type, callback func(T1, T2)) bool {
	if t1, ok1 := t1.(T1); ok1 {
		if t2, ok2 := t2.(T2); ok2 {
			callback(t1, t2)
			return true
		}
	}
	return false
}

// If `unify` doesn't return an error it means that `t1` is a subtype of `t2` or
// they are the same type.
func (c *Checker) unify(ctx Context, t1, t2 ast.Type) []*Error {
	if t1 == nil || t2 == nil {
		return []*Error{{message: "Cannot unify nil types"}}
	}

	t1 = ast.Prune(t1)
	t2 = ast.Prune(t2)

	// | TypeVarType, _ -> ...
	if tv1, ok := t1.(*ast.TypeVarType); ok {
		return c.bind(ctx, tv1, t2)
	}
	// | _, TypeVarType -> ...
	if tv2, ok := t2.(*ast.TypeVarType); ok {
		return c.bind(ctx, tv2, t1)
	}
	// | PrimType, PrimType -> ...
	if prim1, ok := t1.(*ast.PrimType); ok {
		if prim2, ok := t2.(*ast.PrimType); ok {
			if prim1.Equal(prim2) {
				return nil
			}
		}
	}
	// | WildcardType, _ -> ...
	if _, ok := t1.(*ast.WildcardType); ok {
		return nil
	}
	// | _, WildcardType -> ...
	if _, ok := t2.(*ast.WildcardType); ok {
		return nil
	}
	// | NeveType, _ -> ...
	if _, ok := t1.(*ast.NeverType); ok {
		return nil
	}
	// | UnknownType, _ -> ...
	if _, ok := t2.(*ast.UnknownType); ok {
		return nil
	}
	// | TupleType, TupleType -> ...
	if tuple1, ok := t1.(*ast.TupleType); ok {
		if tuple2, ok := t2.(*ast.TupleType); ok {
			panic(fmt.Sprintf("TODO: unify types %v and %v", tuple1, tuple2))
			// TODO
		}
	}
	// | TupleType, ArrayType -> ...
	if tuple1, ok := t2.(*ast.TupleType); ok {
		if array2, ok := t2.(*ast.TypeRefType); ok && array2.Name == "Array" {
			panic(fmt.Sprintf("TODO: unify types %v and %v", tuple1, array2))
			// TODO
		}
	}
	// | ArrayType, TupleType -> ...
	if array1, ok := t1.(*ast.TypeRefType); ok && array1.Name == "Array" {
		if tuple2, ok := t2.(*ast.TupleType); ok {
			panic(fmt.Sprintf("TODO: unify types %v and %v", array1, tuple2))
			// TODO
		}
	}
	// | ArrayType, ArrayType -> ...
	if rest, ok := t1.(*ast.RestSpreadType); ok {
		if array, ok := t2.(*ast.TypeRefType); ok && array.Name == "Array" {
			panic(fmt.Sprintf("TODO: unify types %v and %v", rest, array))
			// TODO
		}
	}
	// | FuncType, FuncType -> ...
	if func1, ok := t1.(*ast.FuncType); ok {
		if func2, ok := t2.(*ast.FuncType); ok {
			panic(fmt.Sprintf("TODO: unify types %v and %v", func1, func2))
			// TODO
		}
	}
	// | TypeRefType, TypeRefType (same alias name) -> ...
	if ref1, ok := t1.(*ast.TypeRefType); ok {
		if ref2, ok := t2.(*ast.TypeRefType); ok && ref1.Name == ref2.Name {
			panic(fmt.Sprintf("TODO: unify types %v and %v", ref1, ref2))
			// TODO
		}
	}
	// | TypeRefType, TypeRefType (different alias name) -> ...
	if ref1, ok := t1.(*ast.TypeRefType); ok {
		if ref2, ok := t2.(*ast.TypeRefType); ok && ref1.Name != ref2.Name {
			panic(fmt.Sprintf("TODO: unify types %v and %v", ref1, ref2))
			// TODO
		}
	}
	// | LitType, PrimType -> ...
	if lit, ok := t1.(*ast.LitType); ok {
		if prim, ok := t2.(*ast.PrimType); ok {
			panic(fmt.Sprintf("TODO: unify types %v and %v", lit, prim))
			// TODO
		}
	}
	// | LitType, LitType -> ...
	if lit1, ok := t1.(*ast.LitType); ok {
		if lit2, ok := t2.(*ast.LitType); ok {
			panic(fmt.Sprintf("TODO: unify types %v and %v", lit1, lit2))
			// TODO
		}
	}
	// | LitType (string), TemplateLitType -> ...
	if lit, ok := t1.(*ast.LitType); ok {
		if template, ok := t2.(*ast.TemplateLitType); ok {
			if strLit, ok := lit.Lit.(*ast.StrLit); ok {
				panic(fmt.Sprintf("TODO: unify types %v and %v", strLit, template))
				// TODO
			}
		}
	}
	// | UniqueSymbolType, UniqueSymbolType -> ...
	if unique1, ok := t1.(*ast.UniqueSymbolType); ok {
		if unique2, ok := t2.(*ast.UniqueSymbolType); ok {
			if unique1.Equal(unique2) {
				return nil
			} else {
				// TODO: include unique1 and unique2 in the error message
				return []*Error{{message: "Cannot unify unique symbols"}}
			}
		}
	}
	// | ObjectType, ExtractType -> ...
	if obj, ok := t1.(*ast.ObjectType); ok {
		if ext, ok := t2.(*ast.ExtractorType); ok {
			panic(fmt.Sprintf("TODO: unify types %v and %v", obj, ext))
			// TODO
		}
	}
	// | ExtractType, ObjectType -> ...
	if ext, ok := t1.(*ast.ExtractorType); ok {
		if obj, ok := t2.(*ast.ObjectType); ok {
			panic(fmt.Sprintf("TODO: unify types %v and %v", ext, obj))
			// TODO
		}
	}
	// | ObjectType, ObjectType -> ...
	if obj1, ok := t1.(*ast.ObjectType); ok {
		if obj2, ok := t2.(*ast.ObjectType); ok {
			panic(fmt.Sprintf("TODO: unify types %v and %v", obj1, obj2))
			// TODO
		}
	}
	// | IntersectionType, ObjectType -> ...
	if intersection, ok := t1.(*ast.IntersectionType); ok {
		if obj, ok := t2.(*ast.ObjectType); ok {
			panic(fmt.Sprintf("TODO: unify types %v and %v", intersection, obj))
		}
	}
	// | ObjectType, UnionType -> ...
	if obj, ok := t1.(*ast.ObjectType); ok {
		if union, ok := t2.(*ast.UnionType); ok {
			panic(fmt.Sprintf("TODO: unify types %v and %v", obj, union))
			// TODO
		}
	}
	// | IntersectionType, IntersectionType -> ...
	if intersection1, ok := t1.(*ast.IntersectionType); ok {
		if intersection2, ok := t2.(*ast.IntersectionType); ok {
			panic(fmt.Sprintf("TODO: unify types %v and %v", intersection1, intersection2))
			// TODO
		}
	}
	// | UnionType, _ -> ...
	if union, ok := t1.(*ast.UnionType); ok {
		panic(fmt.Sprintf("TODO: unify types %v and %v", union, t2))
		// TODO
	}
	// | _, UnionType -> ...
	if union, ok := t2.(*ast.UnionType); ok {
		panic(fmt.Sprintf("TODO: unify types %v and %v", t1, union))
	}

	// TODO: try to expand each type and then try to unify them again
	panic(fmt.Sprintf("TODO: unify types %v and %v", t1, t2))
}

func (c *Checker) bind(ctx Context, t1 *ast.TypeVarType, t2 ast.Type) []*Error {
	if t1 == nil || t2 == nil {
		return []*Error{{message: "Cannot bind nil types"}}
	}

	if !t1.Equal(t2) {
		if occursInType(t1, t2) {
			// TODO: include t1 and t2 in the error message
			return []*Error{{message: "recursive unification error"}}
		} else {
			// TODO: actually bind t1 and t2
		}
	}

	return nil
}

type OccursInVisitor struct {
	result bool
	t1     ast.Type
}

func (v *OccursInVisitor) VisitType(t ast.Type) {
	if ast.Prune(t).Equal(v.t1) {
		v.result = true
	}
}

func occursInType(t1, t2 ast.Type) bool {
	visitor := &OccursInVisitor{result: false}
	t2.Accept(visitor)
	return visitor.result
}
