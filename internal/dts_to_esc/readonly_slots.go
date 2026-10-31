package dts_to_esc

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dts_parser"
)

// borrowReadonlyParams wraps each converted parameter whose source type is
// `Readonly<T>` in a borrow, and leaves the rest as they are. params[i] must
// be the conversion of src[i].
func borrowReadonlyParams(src []*dts_parser.Param, params []*ast.Param) {
	for i, p := range src {
		borrowReadonlyParam(p, params[i])
	}
}

// borrowReadonlyParam wraps param's type in a borrow when p, its source, is
// typed `Readonly<T>`. The annotation inside stays as written. The borrow is
// `&` when no slot reachable through `T` is writable and `&mut` otherwise. A
// `this` parameter and a rest parameter are left as they are.
func borrowReadonlyParam(p *dts_parser.Param, param *ast.Param) {
	if p.Rest || p.Name == nil || p.Name.Name == "this" || param.TypeAnn == nil {
		return
	}
	target, ok := readonlyTarget(p.Type)
	if !ok {
		return
	}
	mut := reachesWritableSlot(target, true)
	param.TypeAnn = ast.NewBorrowTypeAnn(mut, nil, param.TypeAnn, param.TypeAnn.Span())
}

// readonlyTarget returns the `T` of a `Readonly<T>` annotation. The second
// return value is false for any other annotation.
func readonlyTarget(t dts_parser.TypeAnn) (dts_parser.TypeAnn, bool) {
	ref, ok := t.(*dts_parser.TypeReference)
	if !ok || typeRefName(ref) != "Readonly" || len(ref.TypeArgs) != 1 {
		return nil, false
	}
	return ref.TypeArgs[0], true
}

// reachesWritableSlot reports whether some slot reachable through a value of
// type t is writable. A slot is a property, an index signature entry, an array
// element, or a tuple element. slotsReadonly marks the top-level slots of t
// readonly, the way `Readonly<T>` does, and leaves the slots below them as t
// declares them.
//
// The answer is true for every type whose slots it cannot see, such as a type
// parameter, `any`, or a reference to an interface. A false answer therefore
// means no write can reach a slot through t.
func reachesWritableSlot(t dts_parser.TypeAnn, slotsReadonly bool) bool {
	switch t := t.(type) {
	case *dts_parser.ParenthesizedType:
		return reachesWritableSlot(t.Type, slotsReadonly)
	case *dts_parser.OptionalType:
		return reachesWritableSlot(t.Type, slotsReadonly)
	case *dts_parser.RestType:
		return reachesWritableSlot(t.Type, slotsReadonly)
	case *dts_parser.PrimitiveType:
		switch t.Kind {
		case dts_parser.PrimAny, dts_parser.PrimUnknown, dts_parser.PrimObject:
			return true
		}
		return false
	case *dts_parser.LiteralType, *dts_parser.TemplateLiteralType, *dts_parser.KeyOfType:
		return false
	case *dts_parser.FunctionType, *dts_parser.ConstructorType:
		// A function is a value type. See I2 in
		// planning/ownership_rules/requirements.md.
		return false
	case *dts_parser.ArrayType:
		if !slotsReadonly && !t.Readonly {
			return true
		}
		return reachesWritableSlot(t.ElementType, false)
	case *dts_parser.TupleType:
		readonly := slotsReadonly || t.Readonly
		for _, elem := range t.Elements {
			if elem.Rest {
				// A rest element's type is the array its elements fill, so the
				// tuple's readonly flag passes down to that array's slots.
				if reachesWritableSlot(elem.Type, readonly) {
					return true
				}
				continue
			}
			if !readonly || reachesWritableSlot(elem.Type, false) {
				return true
			}
		}
		return false
	case *dts_parser.UnionType:
		return anyReachesWritableSlot(t.Types, slotsReadonly)
	case *dts_parser.IntersectionType:
		return anyReachesWritableSlot(t.Types, slotsReadonly)
	case *dts_parser.ObjectType:
		for _, member := range t.Members {
			if memberReachesWritableSlot(member, slotsReadonly) {
				return true
			}
		}
		return false
	case *dts_parser.TypeReference:
		if target, ok := readonlyTarget(t); ok {
			return reachesWritableSlot(target, true)
		}
		switch typeRefName(t) {
		case "Array":
			// `Readonly<Array<T>>` fixes the elements the way `readonly T[]`
			// does.
			if len(t.TypeArgs) == 1 {
				return !slotsReadonly || reachesWritableSlot(t.TypeArgs[0], false)
			}
		case "ReadonlyArray", "ReadonlySet":
			if len(t.TypeArgs) == 1 {
				return reachesWritableSlot(t.TypeArgs[0], false)
			}
		case "ReadonlyMap":
			if len(t.TypeArgs) == 2 {
				return anyReachesWritableSlot(t.TypeArgs, false)
			}
		}
		return true
	}
	return true
}

// anyReachesWritableSlot reports whether reachesWritableSlot holds for any of
// types.
func anyReachesWritableSlot(types []dts_parser.TypeAnn, slotsReadonly bool) bool {
	for _, t := range types {
		if reachesWritableSlot(t, slotsReadonly) {
			return true
		}
	}
	return false
}

// memberReachesWritableSlot reports whether member of an object type is a
// writable slot or holds a value through which a writable slot is reachable.
// slotsReadonly marks every slot of the object readonly.
func memberReachesWritableSlot(member dts_parser.InterfaceMember, slotsReadonly bool) bool {
	switch m := member.(type) {
	case *dts_parser.PropertySignature:
		if !slotsReadonly && !m.Readonly {
			return true
		}
		return m.TypeAnn == nil || reachesWritableSlot(m.TypeAnn, false)
	case *dts_parser.IndexSignature:
		if !slotsReadonly && !m.Readonly {
			return true
		}
		return reachesWritableSlot(m.ValueType, false)
	case *dts_parser.MethodSignature:
		// A method is a slot holding a function, so the slot itself is the
		// only thing a write can reach.
		return !slotsReadonly
	case *dts_parser.GetterSignature:
		return m.ReturnType == nil || reachesWritableSlot(m.ReturnType, false)
	case *dts_parser.SetterSignature:
		return !slotsReadonly
	case *dts_parser.CallSignature, *dts_parser.ConstructSignature:
		return false
	}
	return true
}
