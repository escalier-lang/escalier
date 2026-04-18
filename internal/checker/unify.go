package checker

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"unsafe"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// unifyPairKey identifies a pair of types being unified.
// For TypeRefType, we use the TypeAlias pointer + typeArgKey(typeArgs)
// to capture meaningful identity across allocations.
type unifyPairKey struct {
	t1     unsafe.Pointer
	t1Args string // typeArgKey(typeArgs) for TypeRefType, empty otherwise
	t2     unsafe.Pointer
	t2Args string // typeArgKey(typeArgs) for TypeRefType, empty otherwise
}

type unifySeen map[unifyPairKey]bool

// makeUnifyPairKey builds a key that includes both the stable pointer
// and type args for TypeRefType.
func makeUnifyPairKey(t1, t2 type_system.Type) unifyPairKey {
	key := unifyPairKey{}
	if ref, ok := t1.(*type_system.TypeRefType); ok {
		if ref.TypeAlias != nil {
			key.t1 = unsafe.Pointer(ref.TypeAlias)
		} else {
			key.t1 = interfaceDataPointer(t1)
		}
		key.t1Args = typeArgKey(ref.TypeArgs)
	} else {
		key.t1 = interfaceDataPointer(t1)
	}
	if ref, ok := t2.(*type_system.TypeRefType); ok {
		if ref.TypeAlias != nil {
			key.t2 = unsafe.Pointer(ref.TypeAlias)
		} else {
			key.t2 = interfaceDataPointer(t2)
		}
		key.t2Args = typeArgKey(ref.TypeArgs)
	} else {
		key.t2 = interfaceDataPointer(t2)
	}
	return key
}

// interfaceDataPointer extracts the data pointer from a Go interface value.
func interfaceDataPointer(t type_system.Type) unsafe.Pointer {
	return (*[2]unsafe.Pointer)(unsafe.Pointer(&t))[1]
}

// typeArgKey produces a stable, deterministic string key for type arguments.
func typeArgKey(args []type_system.Type) string {
	parts := make([]string, len(args))
	for i, arg := range args {
		parts[i] = typeKey(arg)
	}
	return strings.Join(parts, ",")
}

// typeKey produces an injective, stable string key for a single type.
// It uses kind markers and explicit parentheses to ensure structurally
// different types never produce the same key. TypeVarType emits "$<ID>"
// (stable regardless of binding state), and TypeRefType with a resolved
// alias emits "@<pointer>" (preventing unbounded key growth for
// recursive type aliases — see #463). All compound types are recursed
// into so embedded TypeVarType/TypeRefType are always serialized using
// identity-based keys rather than structural expansion.
func typeKey(t type_system.Type) string {
	switch v := t.(type) {
	case *type_system.TypeVarType:
		return fmt.Sprintf("$%d", v.ID)
	case *type_system.TypeRefType:
		var head string
		if v.TypeAlias != nil {
			head = fmt.Sprintf("@%p", v.TypeAlias)
		} else {
			head = type_system.QualIdentToString(v.Name)
		}
		if len(v.TypeArgs) > 0 {
			head += "<" + typeArgKey(v.TypeArgs) + ">"
		}
		return head
	case *type_system.UnionType:
		parts := make([]string, len(v.Types))
		for i, u := range v.Types {
			parts[i] = typeKey(u)
		}
		return "U(" + strings.Join(parts, "|") + ")"
	case *type_system.IntersectionType:
		parts := make([]string, len(v.Types))
		for i, u := range v.Types {
			parts[i] = typeKey(u)
		}
		return "I(" + strings.Join(parts, "&") + ")"
	case *type_system.TupleType:
		parts := make([]string, len(v.Elems))
		for i, u := range v.Elems {
			parts[i] = typeKey(u)
		}
		return "T(" + strings.Join(parts, ",") + ")"
	case *type_system.FuncType:
		params := make([]string, len(v.Params))
		for i, p := range v.Params {
			params[i] = typeKey(p.Type)
		}
		ret := ""
		if v.Return != nil {
			ret = typeKey(v.Return)
		}
		return "F(" + strings.Join(params, ",") + ")->" + ret
	case *type_system.ObjectType:
		parts := make([]string, 0, len(v.Elems))
		for _, elem := range v.Elems {
			switch e := elem.(type) {
			case *type_system.PropertyElem:
				parts = append(parts, e.Name.String()+":"+typeKey(e.Value))
			case *type_system.MethodElem:
				parts = append(parts, e.Name.String()+":"+typeKey(e.Fn))
			case *type_system.CallableElem:
				parts = append(parts, "()"+typeKey(e.Fn))
			case *type_system.ConstructorElem:
				parts = append(parts, "new()"+typeKey(e.Fn))
			case *type_system.GetterElem:
				parts = append(parts, "get:"+e.Name.String()+":"+typeKey(e.Fn.Return))
			case *type_system.SetterElem:
				if len(e.Fn.Params) > 0 {
					parts = append(parts, "set:"+e.Name.String()+":"+typeKey(e.Fn.Params[0].Type))
				}
			case *type_system.RestSpreadElem:
				parts = append(parts, "..."+typeKey(e.Value))
			case *type_system.IndexSignatureElem:
				parts = append(parts, "["+typeKey(e.KeyType)+"]:"+typeKey(e.Value))
			case *type_system.MappedElem:
				parts = append(parts, "["+e.TypeParam.Name+"]:"+typeKey(e.Value))
			}
		}
		return "O{" + strings.Join(parts, ",") + "}"
	case *type_system.CondType:
		return "C(" + typeKey(v.Check) + ":" + typeKey(v.Extends) + "?" + typeKey(v.Then) + ":" + typeKey(v.Else) + ")"
	case *type_system.MutabilityType:
		return "M(" + string(v.Mutability) + typeKey(v.Type) + ")"
	case *type_system.RestSpreadType:
		return "..." + typeKey(v.Type)
	case *type_system.KeyOfType:
		return "K(" + typeKey(v.Type) + ")"
	case *type_system.IndexType:
		return typeKey(v.Target) + "[" + typeKey(v.Index) + "]"
	case *type_system.TemplateLitType:
		parts := make([]string, len(v.Types))
		for i, typ := range v.Types {
			parts[i] = typeKey(typ)
		}
		return "TL(" + strings.Join(parts, ",") + ")"
	case *type_system.ExtractorType:
		args := make([]string, len(v.Args))
		for i, a := range v.Args {
			args[i] = typeKey(a)
		}
		return "E(" + typeKey(v.Extractor) + "," + strings.Join(args, ",") + ")"
	default:
		return fmt.Sprint(t)
	}
}

// noMatchError is a sentinel error indicating that no case in unifyMatched
// handled the type combination. unifyPruned uses this to decide whether
// expansion and retry is appropriate.
type noMatchError struct{}

func (e *noMatchError) isError()        {}
func (e *noMatchError) Span() ast.Span  { return DEFAULT_SPAN }
func (e *noMatchError) Error() string   { return "no match" }
func (e *noMatchError) Message() string { return "no match" }
func (e *noMatchError) IsWarning() bool { return false }

// isNoMatch checks whether the error list consists solely of a noMatchError
// sentinel, indicating that unifyMatched found no applicable case.
func isNoMatch(errors []Error) bool {
	if len(errors) == 1 {
		_, ok := errors[0].(*noMatchError)
		return ok
	}
	return false
}

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

// isExprProvenance returns true if the type's provenance originates from an
// expression node (e.g. an object literal) rather than a type annotation.
func isExprProvenance(t type_system.Type) bool {
	if t == nil {
		return false
	}
	if prov := t.Provenance(); prov != nil {
		if nodeProv, ok := prov.(*ast.NodeProvenance); ok && nodeProv.Node != nil {
			_, isExpr := nodeProv.Node.(ast.Expr)
			return isExpr
		}
	}
	return false
}

// getKeyNotFoundSpan returns the appropriate span for a KeyNotFoundError.
// When obj comes from an expression (e.g. an object literal like {x: 5}),
// the error points at the literal itself since that's what the user needs
// to fix. Otherwise, obj comes from a type declaration and the error
// should point at propType (e.g. the `z` in a destructuring pattern
// `val {x, y, z} = p` that references a non-existent key).
func getKeyNotFoundSpan(obj *type_system.ObjectType, propType type_system.Type) ast.Span {
	if isExprProvenance(obj) {
		return getSpanFromType(obj)
	}
	return getSpanFromType(propType)
}

// isArrayType checks if a TypeRefType refers to the global Array type.
// This handles both simple names ("Array") and qualified names ("globalThis.Array")
// by checking the underlying TypeAlias pointer when available.
func (c *Checker) isArrayType(ref *type_system.TypeRefType) bool {
	// Check by TypeAlias pointer - both should point to the same global Array alias
	if ref.TypeAlias != nil && c.GlobalScope != nil {
		globalArrayAlias := c.GlobalScope.Namespace.Types["Array"]
		if globalArrayAlias != nil && ref.TypeAlias == globalArrayAlias {
			return true
		}
		// TypeAlias is set but doesn't match global Array - not the global Array
		return false
	}

	// Fallback: no TypeAlias set, check by simple name match
	return type_system.QualIdentToString(ref.Name) == "Array"
}

// sameTypeRef checks if two TypeRefTypes refer to the same type alias.
// This handles both same-name cases ("Array" == "Array") and qualified name cases
// where different names point to the same alias (e.g., "globalThis.Array" and "Array").
func (c *Checker) sameTypeRef(ref1, ref2 *type_system.TypeRefType) bool {
	// Check by name match first
	if type_system.QualIdentToString(ref1.Name) == type_system.QualIdentToString(ref2.Name) {
		return true
	}

	// Check by TypeAlias pointer - if both point to the same alias, they're the same type
	if ref1.TypeAlias != nil && ref2.TypeAlias != nil && ref1.TypeAlias == ref2.TypeAlias {
		return true
	}

	return false
}

// If `Unify` doesn't return an error it means that `t1` is a subtype of `t2` or
// they are the same type.
func (c *Checker) Unify(ctx Context, t1, t2 type_system.Type) []Error {
	return c.unifyInner(ctx, t1, t2, make(unifySeen))
}

// unifyInner is the internal entry point for unification with cycle tracking.
// Recursion termination is handled by the unifySeen visited set, which uses
// co-inductive reasoning to assume success for re-encountered type pairs.
func (c *Checker) unifyInner(ctx Context, t1, t2 type_system.Type, seen unifySeen) []Error {
	if t1 == nil || t2 == nil {
		panic("Cannot unify nil types")
	}

	// Save the TypeVarType (if any) before Prune reassigns t2 to the pruned
	// concrete type. Prune records the alias chain in tv2.InstanceChain, which
	// the widening fallback reads to update all aliased TypeVars.
	tv2, _ := t2.(*type_system.TypeVarType)
	t1 = type_system.Prune(t1)
	t2 = type_system.Prune(t2)

	errors := c.unifyPruned(ctx, t1, t2, seen)
	if len(errors) == 0 {
		return nil
	}

	// Property widening: when concrete-vs-concrete unification fails and the
	// SECOND (target) type was a Widenable TypeVarType, widen its Instance to a
	// union of the old and new types instead of reporting an error.
	//
	// Only the t2 side is checked because property writes always call
	// Unify(valueType, propertyTV), placing the Widenable TypeVar on the right.
	// Read sites (e.g. val s: string = obj.bar) call Unify(propertyTV, declaredType),
	// placing the TypeVar on the left — those must NOT widen, they must report
	// type errors.
	//
	// Strip MutabilityType wrappers before building the union — they come from
	// the uncertain-mutability wrapper on open object properties and should not
	// appear inside union members.
	//
	// When bind() aliases two Widenable TypeVars (tvA.Instance = tvB), Prune
	// path-compresses the chain but records it in tvA.InstanceChain. We use
	// that chain to update ALL aliased TypeVars so reads through any alias
	// observe the widened type.
	if widenableChain := widenableInstanceChain(tv2); len(widenableChain) > 0 {
		oldType := unwrapMutability(t2)
		// If oldType is still a TypeVarType (e.g. the chain ended at an unbound
		// TypeVar and bind failed due to an occurs check), we must not build a
		// union containing a live TypeVar — propagate the original error instead.
		if _, isTV := oldType.(*type_system.TypeVarType); isTV {
			return errors
		}
		newType := unwrapMutability(widenLiteral(t1))
		widened := oldType
		if !typeContains(oldType, newType) {
			widened = flatUnion(oldType, newType)
		}
		for _, tv := range widenableChain {
			tv.Instance = widened
		}
		return nil
	}

	return errors
}

func (c *Checker) unifyPruned(ctx Context, t1, t2 type_system.Type, seen unifySeen) []Error {
	for {
		c.checkTimeout()
		errors := c.unifyMatched(ctx, t1, t2, seen)
		if len(errors) == 0 {
			return nil
		}

		// If a case in unifyMatched handled the types and returned errors,
		// those errors are authoritative — don't try expansion.
		if !isNoMatch(errors) {
			return errors
		}

		// From here, no case in unifyMatched matched. Try expansion.

		ref1, isRef1 := t1.(*type_system.TypeRefType)
		ref2, isRef2 := t2.(*type_system.TypeRefType)

		// Try expanding TypeRefTypes. Use canExpandTypeRef as a "should we
		// expand?" predicate, then delegate to ExpandType + unifyInner
		// for the actual retry. ExpandType creates fresh copies via the
		// visitor (preventing mutation of shared TypeAlias types, e.g.
		// `type Point = {x: number, y: number}; val p: Point = {x: 1, y: 2}`
		// would corrupt Point's alias if unification mutated the shared pointer),
		// and unifyInner provides Prune + widening on the expanded result.
		refCanExpand := false
		if isRef1 && c.canExpandTypeRef(ctx, ref1) {
			refCanExpand = true
		}
		if isRef2 && c.canExpandTypeRef(ctx, ref2) {
			refCanExpand = true
		}
		if refCanExpand {
			key := makeUnifyPairKey(t1, t2)
			if seen[key] {
				return nil // co-inductive assumption: assume success
			}
			seen[key] = true
			refExpT1, _ := c.ExpandType(ctx, t1, 1)
			refExpT2, _ := c.ExpandType(ctx, t2, 1)
			return c.unifyInner(ctx, refExpT1, refExpT2, seen)
		}

		// Try expanding TypeOfType and other non-TypeRef expandable types.
		// ExpandType with count=0 skips TypeRef expansion (already handled).
		// Pointer-equality check is reliable here: ExpandType(t, 0) returns
		// the same pointer when t contains nothing expandable at count=0
		// (e.g. an ObjectType with only TypeRefType properties).
		nonRefExpT1, _ := c.ExpandType(ctx, t1, 0)
		nonRefExpT2, _ := c.ExpandType(ctx, t2, 0)
		if nonRefExpT1 != t1 || nonRefExpT2 != t2 {
			// Prune after expansion to resolve any TypeVarTypes returned by
			// expansion (e.g. TypeOfType resolves to a scope binding's
			// TypeVarType, which must be pruned before re-entering unifyMatched).
			t1 = type_system.Prune(nonRefExpT1)
			t2 = type_system.Prune(nonRefExpT2)
			continue
		}

		// Last resort for TypeRefTypes that canExpandTypeRef refused (e.g.
		// refs blocked by IsTypeParam, or cycle detection). ExpandType may
		// still expand these if the alias resolves to a non-nominal type.
		// For nominal TypeRefTypes (classes), ExpandType returns nil (the
		// visitor checks Nominal and bails), so lastResortT == t and this
		// branch is a no-op — execution falls through to CannotUnifyTypesError.
		//
		// For non-nominal refused refs (e.g. cycle-blocked aliases),
		// ExpandType may return a different type, triggering unifyInner.
		// This is needed for pattern matching against structural types:
		// `match p { {foo} => foo }` requires expanding the TypeRefType to
		// access the ObjectType's properties.
		//
		// Termination: unifyInner re-enters unifyMatched with
		// concrete types. For structural types, unification proceeds over
		// finite property sets and bottoms out without re-entering this path.
		if isRef1 || isRef2 {
			key := makeUnifyPairKey(t1, t2)
			if seen[key] {
				return nil // co-inductive assumption: assume success
			}
			seen[key] = true
			lastResortT1, _ := c.ExpandType(ctx, t1, 1)
			lastResortT2, _ := c.ExpandType(ctx, t2, 1)
			if lastResortT1 != t1 || lastResortT2 != t2 {
				return c.unifyInner(ctx, lastResortT1, lastResortT2, seen)
			}
		}

		// Nothing could be expanded, return a real error
		return []Error{&CannotUnifyTypesError{T1: t1, T2: t2}}
	}
}

func (c *Checker) unifyMatched(ctx Context, t1, t2 type_system.Type, seen unifySeen) []Error {

	// | TypeVarType, ErrorType -> bind
	// | ErrorType, TypeVarType -> bind
	// | ErrorType, _ -> success (suppress cascading errors)
	// | _, ErrorType -> success (suppress cascading errors)
	_, t1IsTypeVar := t1.(*type_system.TypeVarType)
	_, t2IsTypeVar := t2.(*type_system.TypeVarType)
	_, t1IsError := t1.(*type_system.ErrorType)
	_, t2IsError := t2.(*type_system.ErrorType)
	if (t1IsTypeVar && t2IsError) || (t1IsError && t2IsTypeVar) {
		return c.bind(ctx, t1, t2, seen)
	}
	if t1IsError || t2IsError {
		return nil
	}

	// | TypeVarType, _ -> ...
	if t1IsTypeVar {
		return c.bind(ctx, t1, t2, seen)
	}
	// | _, TypeVarType -> ...
	if t2IsTypeVar {
		return c.bind(ctx, t1, t2, seen)
	}
	// | MutableType, MutableType -> ...
	if mut1, ok := t1.(*type_system.MutabilityType); ok {
		if mut2, ok := t2.(*type_system.MutabilityType); ok {
			if mut1.Mutability == type_system.MutabilityMutable && mut2.Mutability == type_system.MutabilityMutable {
				return c.unifyMut(ctx, mut1, mut2)
			} else {
				return c.unifyInner(ctx, mut1.Type, mut2.Type, seen)
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
			return c.unifyInner(ctx, mut1.Type, t2, seen)
		}
	}
	// | _, MutableType -> ...
	if mut2, ok := t2.(*type_system.MutabilityType); ok {
		// When the RHS is a MutabilityType, we need to unwrap it for unification
		// This allows patterns without mutability markers to match against mutable values
		return c.unifyInner(ctx, t1, mut2.Type, seen)
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
	// | PrimType, ObjectType (empty and non-nominal) -> ...
	// A primitive type can unify with an empty non-nominal object type.
	// This is because {} represents "any non-nullish value",
	// and primitives like string, number, boolean are non-nullish.
	// This enables branded types like "string & {}".
	if _, ok := t1.(*type_system.PrimType); ok {
		if obj, ok := t2.(*type_system.ObjectType); ok {
			if len(obj.Elems) == 0 && !obj.Nominal {
				return nil
			}
		}
	}
	// | LitType (non-nullish), ObjectType (empty and non-nominal) -> ...
	// Literal types (numbers, strings, booleans) can unify with empty object types
	// since they are non-nullish values. This supports branded types in unions.
	if lit, ok := t1.(*type_system.LitType); ok {
		if obj, ok := t2.(*type_system.ObjectType); ok {
			if len(obj.Elems) == 0 && !obj.Nominal {
				// Only allow non-nullish literals
				switch lit.Lit.(type) {
				case *type_system.NumLit, *type_system.StrLit, *type_system.BoolLit, *type_system.BigIntLit:
					return nil
				}
			}
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
	// | KeyOfType, KeyOfType -> expand both to get their keys, then check if keyof1's keys are a subset of keyof2's keys
	if keyof1, ok := t1.(*type_system.KeyOfType); ok {
		if keyof2, ok := t2.(*type_system.KeyOfType); ok {
			// Expand both keyof types to get their actual keys
			expandedKeys1, _ := c.ExpandType(ctx, keyof1, 1)
			expandedKeys2, _ := c.ExpandType(ctx, keyof2, 1)

			// Check if expansion succeeded (result is not still a KeyOfType)
			_, stillKeyOf1 := expandedKeys1.(*type_system.KeyOfType)
			_, stillKeyOf2 := expandedKeys2.(*type_system.KeyOfType)

			// If both were successfully expanded to concrete keys, unify the expanded types
			if !stillKeyOf1 && !stillKeyOf2 {
				return c.unifyInner(ctx, expandedKeys1, expandedKeys2, seen)
			}

			// If neither could be expanded (e.g., both are keyof TypeVar), try to unify the underlying types.
			// During interface merging, keyof constraints on type parameters may have different
			// internal type variable IDs but represent the same constraint structurally.
			innerErrors := c.unifyInner(ctx, keyof1.Type, keyof2.Type, seen)
			if len(innerErrors) == 0 {
				return nil
			}

			// Return innerErrors to enforce constraint compatibility rather than
			// unconditionally treating different TypeVar IDs as compatible.
			return innerErrors
		}
	}
	// | TupleType, TupleType -> ...
	if tuple1, ok := t1.(*type_system.TupleType); ok {
		if tuple2, ok := t2.(*type_system.TupleType); ok {
			return c.unifyTuples(ctx, tuple1, tuple2, seen)
		}
	}
	// | TupleType, ArrayType -> ...
	if tuple1, ok := t1.(*type_system.TupleType); ok {
		if array2, ok := t2.(*type_system.TypeRefType); ok && c.isArrayType(array2) {
			if len(array2.TypeArgs) == 1 {
				errors := []Error{}
				for _, elem := range tuple1.Elems {
					if rest, ok := elem.(*type_system.RestSpreadType); ok {
						// Unify rest type with Array<T>
						unifyErrors := c.unifyInner(ctx, rest.Type, array2, seen)
						errors = slices.Concat(errors, unifyErrors)
					} else {
						unifyErrors := c.unifyInner(ctx, elem, array2.TypeArgs[0], seen)
						errors = slices.Concat(errors, unifyErrors)
					}
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
	if array1, ok := t1.(*type_system.TypeRefType); ok && c.isArrayType(array1) {
		if tuple2, ok := t2.(*type_system.TupleType); ok {
			if len(array1.TypeArgs) == 1 {
				errors := []Error{}
				for _, elem := range tuple2.Elems {
					if rest, ok := elem.(*type_system.RestSpreadType); ok {
						// Unify Array<T> with rest type
						unifyErrors := c.unifyInner(ctx, array1, rest.Type, seen)
						errors = slices.Concat(errors, unifyErrors)
					} else {
						unifyErrors := c.unifyInner(ctx, array1.TypeArgs[0], elem, seen)
						errors = slices.Concat(errors, unifyErrors)
					}
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
	if array1, ok := t1.(*type_system.TypeRefType); ok && c.isArrayType(array1) {
		if array2, ok := t2.(*type_system.TypeRefType); ok && c.isArrayType(array2) {
			// Both are Array types, unify their element types
			if len(array1.TypeArgs) == 1 && len(array2.TypeArgs) == 1 {
				return c.unifyInner(ctx, array1.TypeArgs[0], array2.TypeArgs[0], seen)
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
		if array, ok := t2.(*type_system.TypeRefType); ok && c.isArrayType(array) {
			return c.unifyInner(ctx, rest.Type, array, seen)
		}
	}
	// | FuncType, FuncType -> ...
	if func1, ok := t1.(*type_system.FuncType); ok {
		if func2, ok := t2.(*type_system.FuncType); ok {
			return c.unifyFuncTypes(ctx, func1, func2, seen)
		}
	}
	// | TypeRefType, TypeRefType (same alias) -> ...
	// This handles both same-name cases ("Array" == "Array") and qualified name cases
	// where different names point to the same alias (e.g., "globalThis.Array" and "Array")
	if ref1, ok := t1.(*type_system.TypeRefType); ok {
		if ref2, ok := t2.(*type_system.TypeRefType); ok && c.sameTypeRef(ref1, ref2) {
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
					argErrors := c.unifyInner(ctx, ref1.TypeArgs[i], ref2.TypeArgs[i], seen)
					errors = slices.Concat(errors, argErrors)
				}
				return errors
			}
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
							groupErrors := c.unifyInner(
								ctx,
								type_system.NewStrLitType(nil, matches[i]),
								// By default this will be a `string` type, but
								// if the RegexType appears in a CondType's
								// Extend field, it will be a TypeVarType.
								regexType.Groups[name],
								seen,
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
	// | _, ExtractorType -> ...
	if ext, ok := t2.(*type_system.ExtractorType); ok {
		return c.unifyExtractor(ctx, t1, ext, false, seen)
	}
	// | ExtractorType, _ -> ...
	if ext, ok := t1.(*type_system.ExtractorType); ok {
		return c.unifyExtractor(ctx, t2, ext, true, seen)
	}
	// | ObjectType, ObjectType -> ...
	if obj1, ok := t1.(*type_system.ObjectType); ok {
		if obj2, ok := t2.(*type_system.ObjectType); ok {
			// In pattern-matching mode, allow structural patterns to match
			// against nominal types by falling through to property comparison.
			if obj2.Nominal && !ctx.IsPatMatch {
				if obj1.ID != obj2.ID {
					// TODO(#424): check what classes the objects extend
					return []Error{&CannotUnifyTypesError{
						T1: obj1,
						T2: obj2,
					}}
				}
			}

			// TODO(#419): handle exactness
			// TODO(#420): handle unnamed elems, e.g. callable and newable signatures
			// TODO(#421): handle spread when both closed sides have RestSpreadElems
			// TODO(#422): handle mapped type elems
			// TODO(#423): handle getters/setters with proper variance (currently
			// flattened into the same map as properties, losing read/write directionality)

			errors := []Error{}

			collected1 := collectObjElemTypes(obj1, true)
			namedElems1 := collected1.Read
			keys1 := collected1.Keys
			restTypes1 := collected1.RestTypes

			collected2 := collectObjElemTypes(obj2, true)
			namedElems2 := collected2.Read
			keys2 := collected2.Keys
			restTypes2 := collected2.RestTypes

			// Open-vs-open: unify shared properties, merge non-shared,
			// unify row variables.
			//
			// Note: we mutate obj1.Elems/obj2.Elems even when c.Unify calls
			// return errors. This is intentional — the unifier throughout this
			// file collects errors and continues (best-effort inference).
			// Partial type information from the merge is better than none for
			// downstream inference and error reporting.
			if obj1.Open && obj2.Open {
				for _, key := range keys1 {
					value1, has1 := namedElems1[key]
					value2, has2 := namedElems2[key]
					if has1 && has2 {
						unifyErrors := c.unifyInner(ctx, value1, value2, seen)
						errors = slices.Concat(errors, unifyErrors)
					}
					if wt1, ok1 := collected1.Write[key]; ok1 {
						if wt2, ok2 := collected2.Write[key]; ok2 {
							unifyErrors := c.unifyInner(ctx, wt1, wt2, seen)
							errors = slices.Concat(errors, unifyErrors)
						}
					}
				}
				// Add elems from obj2 not in obj1 to obj1.
				// Share the original elem pointers (not copies) so that mutable
				// fields like Written are visible to both open types - they are
				// in the same inference scope and represent the same parameter.
				//
				// Read and write presence are checked independently because a
				// property name can be split across separate GetterElem (read)
				// and SetterElem (write) elements, tracked in different maps.
				// For example, given:
				//   obj1 = { get x(self) -> number }  // Read["x"] exists, Write["x"] absent
				//   obj2 = { set x(mut self, v: number) }  // Read["x"] absent, Write["x"] exists
				// A single check against namedElems1 (the read map) would see
				// "x" already present in obj1 and skip the merge, losing obj2's
				// setter. By checking read and write separately, the setter is
				// correctly appended so obj1 ends up with both get x and set x.
				for _, key := range keys2 {
					re := collected2.OrigRead[key]
					we := collected2.OrigWrite[key]
					if re != nil {
						if _, has := namedElems1[key]; !has {
							obj1.Elems = append(obj1.Elems, re)
						}
					}
					if we != nil && we != re {
						if _, has := collected1.Write[key]; !has {
							obj1.Elems = append(obj1.Elems, we)
						}
					}
				}
				// Add elems from obj1 not in obj2 to obj2.
				for _, key := range keys1 {
					re := collected1.OrigRead[key]
					we := collected1.OrigWrite[key]
					if re != nil {
						if _, has := namedElems2[key]; !has {
							obj2.Elems = append(obj2.Elems, re)
						}
					}
					if we != nil && we != re {
						if _, has := collected2.Write[key]; !has {
							obj2.Elems = append(obj2.Elems, we)
						}
					}
				}
				// Unify row variables if both have RestSpreadElems
				if len(restTypes1) == 1 && len(restTypes2) == 1 {
					unifyErrors := c.unifyInner(ctx, restTypes1[0], restTypes2[0], seen)
					errors = slices.Concat(errors, unifyErrors)
				}
				return errors
			}

			// Open(t1)-vs-closed(t2): unify shared properties preserving
			// t1/t2 directionality, add closed-only properties to the open type.
			// Open-only properties are allowed (structural subtyping).
			if obj1.Open && !obj2.Open {
				for _, key := range keys2 {
					value1, has1 := namedElems1[key]
					value2, has2 := namedElems2[key]
					if has1 && has2 {
						unifyErrors := c.unifyInner(ctx, value1, value2, seen)
						errors = slices.Concat(errors, unifyErrors)
					}
					re := collected2.OrigRead[key]
					we := collected2.OrigWrite[key]
					// Copy missing read elem from closed to open.
					if re != nil && !has1 {
						obj1.Elems = append(obj1.Elems, copyObjTypeElem(re))
					}
					// Copy missing write elem from closed to open (skip if same as read elem).
					if we != nil && we != re {
						if _, hasW1 := collected1.Write[key]; !hasW1 {
							obj1.Elems = append(obj1.Elems, copyObjTypeElem(we))
						}
					}
				}
				// Copy or unify index signatures from closed to open.
				for _, sig := range collected2.IndexSignatures {
					if existing := findMatchingIndexSignature(sig, collected1.IndexSignatures); existing != nil {
						// Both sides have the same key-kind — unify their value types.
						unifyErrors := c.unifyInner(ctx, existing.Value, sig.Value, seen)
						errors = slices.Concat(errors, unifyErrors)
					} else {
						obj1.Elems = append(obj1.Elems, copyObjTypeElem(sig))
					}
				}
				return errors
			}

			// Closed(t1)-vs-open(t2): same logic, but t1 is closed and t2 is open.
			if !obj1.Open && obj2.Open {
				for _, key := range keys1 {
					value1, has1 := namedElems1[key]
					value2, has2 := namedElems2[key]
					if has1 && has2 {
						unifyErrors := c.unifyInner(ctx, value1, value2, seen)
						errors = slices.Concat(errors, unifyErrors)
					}
					re := collected1.OrigRead[key]
					we := collected1.OrigWrite[key]
					// Copy missing read elem from closed to open.
					if re != nil && !has2 {
						obj2.Elems = append(obj2.Elems, copyObjTypeElem(re))
					}
					// Copy missing write elem from closed to open (skip if same as read elem).
					if we != nil && we != re {
						if _, hasW2 := collected2.Write[key]; !hasW2 {
							obj2.Elems = append(obj2.Elems, copyObjTypeElem(we))
						}
					}
				}
				// Copy or unify index signatures from closed to open.
				for _, sig := range collected1.IndexSignatures {
					if existing := findMatchingIndexSignature(sig, collected2.IndexSignatures); existing != nil {
						unifyErrors := c.unifyInner(ctx, existing.Value, sig.Value, seen)
						errors = slices.Concat(errors, unifyErrors)
					} else {
						obj2.Elems = append(obj2.Elems, copyObjTypeElem(sig))
					}
				}
				return errors
			}

			// Closed-vs-closed: handle rest element distribution.
			//
			// Multiple RestSpreadElems on one side (from object spread) need
			// to be distributed against the other side's properties.
			hasRests1 := len(restTypes1) > 0
			hasRests2 := len(restTypes2) > 0

			if hasRests1 && !hasRests2 {
				errors = slices.Concat(errors, c.unifyClosedWithRests(ctx, obj1, obj2, keys2, namedElems2, false, seen))
			} else if hasRests2 && !hasRests1 {
				errors = slices.Concat(errors, c.unifyClosedWithRests(ctx, obj2, obj1, keys1, namedElems1, true, seen))
			} else if hasRests1 && hasRests2 {
				// TODO(#410): implement unification when both sides have RestSpreadElems
				return []Error{&UnimplementedError{message: "unify types with rest elems on both sides"}}
			} else if ctx.IsPatMatch {
				// In pattern-matching mode: unify shared properties, and verify
				// all pattern fields (keys1) exist on the target (keys2).
				// Target fields not in the pattern are silently skipped (partial matching).
				// namedElems2 is the Read map, so setter-only properties won't appear
				// here and are correctly treated as not found for pattern matching.
				for _, key1 := range keys1 {
					value2, found := namedElems2[key1]
					if found {
						unifyErrors := c.unifyInner(ctx, namedElems1[key1], value2, seen)
						errors = slices.Concat(errors, unifyErrors)
					} else {
						errors = append(errors, &PropertyNotFoundError{
							Property: key1,
							Object:   obj2,
							span:     getKeyNotFoundSpan(obj1, namedElems1[key1]),
						})
						// Resolve the pattern field's type to undefined so it doesn't
						// leak as an unresolved type variable into the match arm result.
						undefinedType := type_system.NewUndefinedType(nil)
						c.unifyInner(ctx, namedElems1[key1], undefinedType, seen)
					}
				}
			} else {
				for _, key2 := range keys2 {
					value2, has2 := namedElems2[key2]
					// Unify write types for setter-only keys.
					if !has2 {
						wt1, hasW1 := collected1.Write[key2]
						wt2, hasW2 := collected2.Write[key2]
						if hasW1 && hasW2 {
							unifyErrors := c.unifyInner(ctx, wt1, wt2, seen)
							errors = slices.Concat(errors, unifyErrors)
						} else if hasW2 && !hasW1 {
							errors = slices.Concat(errors, []Error{&KeyNotFoundError{
								Object: obj1,
								Key:    key2,
							}})
						}
						continue
					}
					if value1, ok := namedElems1[key2]; ok {
						unifyErrors := c.unifyInner(ctx, value1, value2, seen)
						// Wrap CannotUnifyTypesError with property context when
						// the property was inferred during row inference.
						for i, err := range unifyErrors {
							if cue, ok := err.(*CannotUnifyTypesError); ok {
								var inferredAt *MemberAccessKeyProvenance
								if pe, ok := collected2.OrigRead[key2].(*type_system.PropertyElem); ok {
									if makp, ok := pe.Provenance.(*MemberAccessKeyProvenance); ok {
										inferredAt = makp
									}
								}
								if inferredAt == nil {
									if pe, ok := collected1.OrigRead[key2].(*type_system.PropertyElem); ok {
										if makp, ok := pe.Provenance.(*MemberAccessKeyProvenance); ok {
											inferredAt = makp
										}
									}
								}
								if inferredAt != nil {
									unifyErrors[i] = &PropertyTypeMismatchError{
										Property:   key2,
										T1:         cue.T1,
										T2:         cue.T2,
										InferredAt: inferredAt,
										span:       cue.Span(),
									}
								}
							}
						}
						errors = slices.Concat(errors, unifyErrors)
					} else if idxValType := findIndexSignatureForKey(key2, collected1.IndexSignatures); idxValType != nil {
						// obj1 has an index signature matching this key — unify value types
						unifyErrors := c.unifyInner(ctx, idxValType, value2, seen)
						errors = slices.Concat(errors, unifyErrors)
					} else {
						knfErr := &KeyNotFoundError{
							Object: obj1,
							Key:    key2,
							span:   getKeyNotFoundSpan(obj1, value2),
						}
						if propElem, ok := collected2.OrigRead[key2].(*type_system.PropertyElem); ok {
							if makp, ok := propElem.Provenance.(*MemberAccessKeyProvenance); ok {
								knfErr.InferredAt = makp
							}
						}
						errors = slices.Concat(errors, []Error{knfErr})
						// Unify the missing property's type with 'undefined' so that it gets
						// properly resolved and doesn't remain as a type variable.
						// We intentionally discard the errors since we already
						// reported the KeyNotFoundError above.
						undefinedType := type_system.NewUndefinedType(nil)
						c.unifyInner(ctx, value2, undefinedType, seen)
					}
				}

				// Check keys in obj1 not in obj2 against obj2's index signatures
				if len(collected2.IndexSignatures) > 0 {
					for _, key1 := range keys1 {
						if _, has := namedElems2[key1]; !has {
							if value1, ok := namedElems1[key1]; ok {
								if idxValType := findIndexSignatureForKey(key1, collected2.IndexSignatures); idxValType != nil {
									unifyErrors := c.unifyInner(ctx, value1, idxValType, seen)
									errors = slices.Concat(errors, unifyErrors)
								}
							}
						}
					}
				}

				// Unify matching index signatures from both sides
				for _, sig1 := range collected1.IndexSignatures {
					if match := findMatchingIndexSignature(sig1, collected2.IndexSignatures); match != nil {
						unifyErrors := c.unifyInner(ctx, sig1.Value, match.Value, seen)
						errors = slices.Concat(errors, unifyErrors)
					}
				}

				// Enforce TypeScript numeric/string index signature compatibility:
				// when an object has both [key: string]: S and [index: number]: N,
				// N must be compatible with S because JavaScript coerces numeric
				// keys to strings (obj[1] === obj["1"]).
				// Check within each object and across objects.
				errors = slices.Concat(errors, c.unifyNumericWithStringIndexSigs(ctx, collected1.IndexSignatures, seen))
				errors = slices.Concat(errors, c.unifyNumericWithStringIndexSigs(ctx, collected2.IndexSignatures, seen))

				// Cross-object check: if one side has a numeric sig and the other
				// has a string sig, their value types must also be compatible.
				for _, s1 := range collected1.IndexSignatures {
					p1, ok := s1.KeyType.(*type_system.PrimType)
					if !ok {
						continue
					}
					for _, s2 := range collected2.IndexSignatures {
						p2, ok := s2.KeyType.(*type_system.PrimType)
						if !ok {
							continue
						}
						if p1.Prim == type_system.NumPrim && p2.Prim == type_system.StrPrim {
							errors = slices.Concat(errors, c.unifyInner(ctx, s1.Value, s2.Value, seen))
						} else if p1.Prim == type_system.StrPrim && p2.Prim == type_system.NumPrim {
							// Args are swapped so the numeric value is always t1 and the
							// string value is always t2, matching the within-object check
							// in unifyNumericWithStringIndexSigs(numVal, strVal).
							errors = slices.Concat(errors, c.unifyInner(ctx, s2.Value, s1.Value, seen))
						}
					}
				}
			}

			return errors
		}
	}
	// | IntersectionType, IntersectionType -> ...
	// Special case: both types are intersections
	// First, try to distribute intersections over unions (Phase 2: Distributive laws)
	// A & (B | C) should be equivalent to (A & B) | (A & C)
	if intersection1, ok := t1.(*type_system.IntersectionType); ok {
		distributed1, _ := distributeIntersectionOverUnion(intersection1)
		// Check if distribution occurred by seeing if the type changed
		if _, stillIntersection := distributed1.(*type_system.IntersectionType); !stillIntersection {
			// Distribution created a different type (likely a union), retry unification
			return c.unifyInner(ctx, distributed1, t2, seen)
		}

		if intersection2, ok := t2.(*type_system.IntersectionType); ok {
			distributed2, _ := distributeIntersectionOverUnion(intersection2)
			// Check if distribution occurred on t2
			if _, stillIntersection := distributed2.(*type_system.IntersectionType); !stillIntersection {
				// Distribution occurred on t2, retry unification
				return c.unifyInner(ctx, distributed1, distributed2, seen)
			}

			// Probe-then-commit: trial-unify clones to avoid partially mutating
			// TypeVars on failure (see #381).
			// For A & B <: C & D, every constraint in C & D must be satisfied by A & B
			// This means for each part of t2, at least one part of t1 must be a subtype
			errors := []Error{}
			for _, t2Part := range intersection2.Types {
				found := false
				for _, t1Part := range intersection1.Types {
					varMapping := make(map[int]*type_system.TypeVarType)
					t1Clone := c.deepCloneType(t1Part, varMapping)
					t2Clone := c.deepCloneType(t2Part, varMapping)
					probeErrors := c.unifyInner(ctx, t1Clone, t2Clone, seen)
					if len(probeErrors) == 0 {
						// Probe succeeded — safe to unify originals.
						c.unifyInner(ctx, t1Part, t2Part, seen)
						found = true
						break
					}
				}
				if !found {
					// Could not find a matching type in intersection1 for this t2Part
					errors = append(errors, &CannotUnifyTypesError{
						T1: intersection1,
						T2: intersection2,
					})
				}
			}
			return errors
		}
	}
	// | IntersectionType, _ -> check if intersection is subtype of t2
	// For an intersection A & B to be a subtype of C, at least one part of the
	// intersection must be a subtype of C, OR the combined intersection satisfies C.
	// We try each part and if any succeeds, the intersection is valid.
	if intersection, ok := t1.(*type_system.IntersectionType); ok {
		// First, try distribution (Phase 2: Distributive laws)
		distributed, _ := distributeIntersectionOverUnion(intersection)
		// Check if distribution occurred
		if _, stillIntersection := distributed.(*type_system.IntersectionType); !stillIntersection {
			// Distribution created a different type (likely a union), retry unification
			return c.unifyInner(ctx, distributed, t2, seen)
		}

		// Probe-then-commit: trial-unify clones to avoid partially mutating
		// TypeVars on failure (see #381).
		var allErrors []Error
		for _, part := range intersection.Types {
			varMapping := make(map[int]*type_system.TypeVarType)
			partClone := c.deepCloneType(part, varMapping)
			t2Clone := c.deepCloneType(t2, varMapping)
			probeErrors := c.unifyInner(ctx, partClone, t2Clone, seen)
			if len(probeErrors) == 0 {
				// Probe succeeded — safe to unify originals.
				c.unifyInner(ctx, part, t2, seen)
				return nil
			}
			allErrors = slices.Concat(allErrors, probeErrors)
		}
		// None of the parts successfully unified with t2
		return allErrors
	}
	// | _, IntersectionType -> check if t1 is subtype of intersection
	// For A to be a subtype of B & C, A must be a subtype of both B and C.
	// This is because B & C requires all the properties of both B and C.
	if intersection, ok := t2.(*type_system.IntersectionType); ok {
		// First, try distribution (Phase 2: Distributive laws)
		distributed, _ := distributeIntersectionOverUnion(intersection)
		// Check if distribution occurred
		if _, stillIntersection := distributed.(*type_system.IntersectionType); !stillIntersection {
			// Distribution created a different type (likely a union), retry unification
			return c.unifyInner(ctx, t1, distributed, seen)
		}

		errors := []Error{}
		for _, part := range intersection.Types {
			unifyErrors := c.unifyInner(ctx, t1, part, seen)
			errors = slices.Concat(errors, unifyErrors)
		}
		return errors
	}
	// | UnionType, _ -> ...
	if union, ok := t1.(*type_system.UnionType); ok {
		// special-case unification of union with object type
		if obj, ok := t2.(*type_system.ObjectType); ok {
			collectedObj := collectObjElemTypes(obj, false)
			destructuredFields := collectedObj.Read
			var restType type_system.Type
			if len(collectedObj.RestTypes) > 0 {
				restType = collectedObj.RestTypes[0]
			}

			matchingTypes := make(map[type_system.ObjTypeKey][]type_system.Type)
			// Track which destructured fields exist as write-only (setter)
			// across union members so we can distinguish "field doesn't exist"
			// from "field exists but is not readable."
			writeOnlyFields := make(map[type_system.ObjTypeKey]bool)
			// Track remaining fields for rest spread handling
			remainingFields := make(map[type_system.ObjTypeKey][]type_system.Type)
			remainingFieldsOrder := []type_system.ObjTypeKey{} // Track order of keys

			for _, unionType := range union.Types {
				expanded, _ := c.ExpandType(ctx, unionType, 1)
				if unionObj, ok := expanded.(*type_system.ObjectType); ok {
					collectedUnionObj := collectObjElemTypes(unionObj, false)
					for name := range destructuredFields {
						if t, ok := collectedUnionObj.Read[name]; ok {
							matchingTypes[name] = append(matchingTypes[name], t)
						} else if _, ok := collectedUnionObj.Write[name]; ok {
							writeOnlyFields[name] = true
						}
					}

					// If restType is specified, collect remaining fields
					if restType != nil {
						for _, key := range collectedUnionObj.Keys {
							readType := collectedUnionObj.Read[key]
							if readType == nil {
								// Setter-only key — not readable, skip.
								continue
							}
							if _, ok := destructuredFields[key]; !ok {
								if _, exists := remainingFields[key]; !exists {
									remainingFieldsOrder = append(remainingFieldsOrder, key)
								}
								remainingFields[key] = append(remainingFields[key], readType)
							}
						}
					}
				}
			}
			errors := []Error{}
			for name, t := range destructuredFields {
				if _, ok := matchingTypes[name]; !ok {
					if writeOnlyFields[name] {
						// Field exists as a setter on at least one union
						// member but is not readable — report an error.
						errors = append(errors, &UnknownPropertyError{
							ObjectType: union,
							Property:   name.String(),
						})
					}
					undefined := type_system.NewUndefinedType(nil)
					unifyErrors := c.unifyInner(ctx, undefined, t, seen)
					errors = slices.Concat(errors, unifyErrors)
				} else if len(matchingTypes[name]) == len(union.Types) {
					// Create a union of all matching types and unify with destructured field type
					unionOfMatchingTypes := type_system.NewUnionType(nil, matchingTypes[name]...)
					fieldType := destructuredFields[name]
					unifyErrors := c.unifyInner(ctx, unionOfMatchingTypes, fieldType, seen)
					errors = slices.Concat(errors, unifyErrors)
				} else {
					// Create a union of all matching types and `undefined`, then unify with destructured field type
					unionOfMatchingTypes := type_system.NewUnionType(nil, append(matchingTypes[name], type_system.NewUndefinedType(nil))...)
					fieldType := destructuredFields[name]
					unifyErrors := c.unifyInner(ctx, unionOfMatchingTypes, fieldType, seen)
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
				unifyErrors := c.unifyInner(ctx, objType, restType, seen)
				errors = slices.Concat(errors, unifyErrors)
			}

			return errors
		}

		// All types in the union must be compatible with t2
		for _, t := range union.Types {
			unifyErrors := c.unifyInner(ctx, t, t2, seen)
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
	// | ObjectType, UnionType (pattern matching) -> ...
	if ctx.IsPatMatch {
		if patObj, ok := t1.(*type_system.ObjectType); ok {
			if union, ok := t2.(*type_system.UnionType); ok {
				return c.unifyPatternWithUnion(ctx, patObj, union, seen)
			}
		}
	}
	// | _, UnionType -> ...
	if union, ok := t2.(*type_system.UnionType); ok {
		// Probe-then-commit: trial-unify clones to avoid partially mutating
		// TypeVars on failure (see #381).
		for _, unionType := range union.Types {
			varMapping := make(map[int]*type_system.TypeVarType)
			t1Clone := c.deepCloneType(t1, varMapping)
			unionTypeClone := c.deepCloneType(unionType, varMapping)
			probeErrors := c.unifyInner(ctx, t1Clone, unionTypeClone, seen)
			if len(probeErrors) == 0 {
				// Probe succeeded — safe to unify originals.
				c.unifyInner(ctx, t1, unionType, seen)
				return nil
			}
		}
		// If we couldn't unify with any union member, return a unification error
		return []Error{&CannotUnifyTypesError{
			T1: t1,
			T2: union,
		}}
	}

	return []Error{&noMatchError{}}
}

// unifyExtractor unifies a subject type against an ExtractorType by finding
// the [Symbol.customMatcher] method, unifying the subject with the method's
// param type, and then unifying the extractor's args with the method's return
// tuple elements. When swapped is true, the Unify argument order is reversed
// (ext args first, tuple elements second) to preserve the original t1/t2
// directionality from the caller.
func (c *Checker) unifyExtractor(
	ctx Context,
	subject type_system.Type,
	ext *type_system.ExtractorType,
	swapped bool,
	seen unifySeen,
) []Error {
	// Helper to call Unify in the correct argument order.
	unify := func(a, b type_system.Type) []Error {
		if swapped {
			return c.unifyInner(ctx, b, a, seen)
		}
		return c.unifyInner(ctx, a, b, seen)
	}

	methodElem, extObj := c.findCustomMatcherMethod(ext)
	if extObj == nil {
		return []Error{&InvalidExtractorTypeError{
			ExtractorType: ext,
			ActualType:    ext.Extractor,
		}}
	}
	if methodElem == nil {
		return []Error{&MissingCustomMatcherError{
			ObjectType: extObj,
		}}
	}
	if len(methodElem.Fn.Params) != 1 {
		return []Error{&IncorrectParamCountForCustomMatcherError{
			Method:    methodElem.Fn,
			NumParams: len(methodElem.Fn.Params),
		}}
	}

	// Instantiate the method's type parameters with fresh TypeVars so that
	// bare type parameter references (e.g. TypeRefType("T")) are replaced
	// before unification. Without this, ExpandType would resolve "T" to
	// NeverType when "T" is not in scope (it's only in the enum declaration
	// scope, not the match expression's scope).
	//
	// Note: instantiateGenericFunc preserves the param count, so the
	// single-param validation on line 1243 is still satisfied after this.
	fn := methodElem.Fn
	if len(fn.TypeParams) > 0 {
		fn = c.instantiateGenericFunc(fn)
	}

	paramType := fn.Params[0].Type
	errors := unify(subject, paramType)

	tuple, ok := fn.Return.(*type_system.TupleType)
	if !ok {
		return []Error{&ExtractorMustReturnTupleError{
			ExtractorType: ext,
			ReturnType:    fn.Return,
		}}
	}

	// If the subject is a type reference, substitute any type parameters
	// in the tuple for the type arguments specified in the subject's type
	// reference.
	// TODO(#430): We might have to expand the subject if the type alias
	// it's using points to another type alias.
	if typeRef, ok := subject.(*type_system.TypeRefType); ok {
		typeAlias := typeRef.TypeAlias
		if typeAlias != nil && len(typeRef.TypeArgs) >= len(typeAlias.TypeParams) {
			substitutions := make(map[string]type_system.Type)
			for i, typeParam := range typeAlias.TypeParams {
				substitutions[typeParam.Name] = typeRef.TypeArgs[i]
			}
			tuple = SubstituteTypeParams(tuple, substitutions)
		}
	}

	// Find if the args have a rest element.
	var restIndex = -1
	for i, elem := range ext.Args {
		if _, isRest := elem.(*type_system.RestSpreadType); isRest {
			restIndex = i
			break
		}
	}

	if restIndex != -1 {
		// Tuple has rest element.
		// Must have at least as many tuple elements as fixed args before rest.
		if len(tuple.Elems) < restIndex {
			return []Error{&ExtractorReturnTypeMismatchError{
				ExtractorType: ext,
				ReturnType:    tuple,
				NumArgs:       len(ext.Args),
				NumReturns:    len(tuple.Elems),
			}}
		}

		// Unify fixed elements (before rest).
		for i := 0; i < restIndex; i++ {
			argErrors := unify(tuple.Elems[i], ext.Args[i])
			errors = slices.Concat(errors, argErrors)
		}

		// Unify rest arguments with rest element type.
		if len(ext.Args) > restIndex {
			restElem := ext.Args[restIndex].(*type_system.RestSpreadType)
			remainingArgsTupleType := type_system.NewTupleType(nil, tuple.Elems[restIndex:]...)

			restErrors := unify(restElem.Type, remainingArgsTupleType)
			errors = slices.Concat(errors, restErrors)
		}
	} else {
		// Tuple has no rest element, use strict equality check.
		if len(tuple.Elems) == len(ext.Args) {
			for retElem, argType := range Zip(tuple.Elems, ext.Args) {
				argErrors := unify(retElem, argType)
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

	return errors
}

// unifyClosedWithRests unifies a closed ObjectType that has RestSpreadElems
// (restObj) against a closed ObjectType with no rests (targetObj).
//
// It first expands restObj.Elems in source order — inlining bound
// RestSpreadElems and applying JavaScript override semantics (later entries
// win) — to produce a flat effective property map. It then unifies each
// effective property against the matching target property.
//
// Any target properties not covered by the effective map are "remaining".
// These are distributed to unbound rest elements (TypeVarTypes) as follows:
//
//   - One unbound rest: bind it to an ObjectType containing all remaining
//     properties.
//     Note the ordering matters for overrides. Both variants have the
//     same type shape but different element order, which affects which
//     value wins for shared keys:
//
//     // fn <T0>(obj: T0) -> {x: 1, ...T0}
//     fn foo(obj) {
//     return {x: 1, ...obj}
//     }
//     foo({x: 5, y: 2}).x // 5
//
//     // fn <T0>(obj: T0) -> {...T0, x: 1}
//     fn foo(obj) {
//     return {...obj, x: 1}
//     }
//     foo({x: 5, y: 2}).x // 1.
//
//   - Multiple unbound rests with remaining properties: error — the system
//     cannot determine which rest should receive which properties. In
//     practice this case is unlikely to be reached because rests originate
//     from function parameters, which are bound by call-site arguments
//     before the return type is unified.
//
//   - Zero unbound rests with remaining properties: error — all rests are
//     bound but the target has extra properties not accounted for.
func (c *Checker) unifyClosedWithRests(
	ctx Context,
	restObj, targetObj *type_system.ObjectType,
	targetKeys []type_system.ObjTypeKey,
	targetNamed map[type_system.ObjTypeKey]type_system.Type,
	swapped bool,
	seen unifySeen,
) []Error {
	errors := []Error{}

	// unifyPair preserves the original t1/t2 ordering for Unify calls.
	// When swapped is false, restObj=t1 and targetObj=t2.
	// When swapped is true, restObj=t2 and targetObj=t1.
	unifyPair := func(restVal, targetVal type_system.Type) []Error {
		if swapped {
			return c.unifyInner(ctx, targetVal, restVal, seen)
		}
		return c.unifyInner(ctx, restVal, targetVal, seen)
	}

	// 1. Expand restObj.Elems into a flat effective property map by walking
	//    in source order and inlining bound RestSpreadElems. Later entries
	//    overwrite earlier ones, giving JavaScript override semantics.
	//    Unbound rests (TypeVarType) are collected separately.
	effectiveKeys := []type_system.ObjTypeKey{}
	effectiveValues := map[type_system.ObjTypeKey]type_system.Type{}
	var unboundRests []*type_system.TypeVarType
	addEffective := func(name type_system.ObjTypeKey, value type_system.Type) {
		if _, exists := effectiveValues[name]; !exists {
			effectiveKeys = append(effectiveKeys, name)
		}
		effectiveValues[name] = value
	}
	for _, elem := range restObj.Elems {
		switch elem := elem.(type) {
		case *type_system.PropertyElem:
			addEffective(elem.Name, elem.Value)
		case *type_system.MethodElem:
			addEffective(elem.Name, elem.Fn)
		case *type_system.GetterElem:
			addEffective(elem.Name, elem.Fn.Return)
		case *type_system.SetterElem:
			// Setters on the outer object are kept (direct access).
			addEffective(elem.Name, elem.Fn.Params[0].Type)
		case *type_system.RestSpreadElem:
			pruned := type_system.Prune(elem.Value)
			if mut, ok := pruned.(*type_system.MutabilityType); ok {
				pruned = type_system.Prune(mut.Type)
			}
			if tv, ok := pruned.(*type_system.TypeVarType); ok && tv.Instance == nil {
				unboundRests = append(unboundRests, tv)
			} else if obj := resolveToObjectType(pruned); obj != nil {
				// Apply spread semantics: methods → fn type, getters → return
				// type, setter-only → skipped.
				for _, re := range obj.Elems {
					switch re := re.(type) {
					case *type_system.PropertyElem:
						addEffective(re.Name, re.Value)
					case *type_system.MethodElem:
						addEffective(re.Name, re.Fn)
					case *type_system.GetterElem:
						addEffective(re.Name, re.Fn.Return)
					case *type_system.SetterElem:
						// Setter-only not readable via spread — skip.
					case *type_system.RestSpreadElem:
						// Nested rest from chained destructuring (e.g. the T3 in
						// {y: T2, ...T3}). Collect so remaining target properties
						// can flow into it.
						innerPruned := type_system.Prune(re.Value)
						if tv, ok := innerPruned.(*type_system.TypeVarType); ok && tv.Instance == nil {
							unboundRests = append(unboundRests, tv)
						}
					}
				}
			}
		}
	}

	// 2. Unify effective properties against the target.
	usedTargetKeys := map[type_system.ObjTypeKey]bool{}
	for _, key := range effectiveKeys {
		value := effectiveValues[key]
		if targetValue, ok := targetNamed[key]; ok {
			unifyErrors := unifyPair(value, targetValue)
			errors = slices.Concat(errors, unifyErrors)
			usedTargetKeys[key] = true
		} else {
			errors = slices.Concat(errors, []Error{&KeyNotFoundError{
				Object: targetObj,
				Key:    key,
				span:   getKeyNotFoundSpan(targetObj, value),
			}})
			undefinedType := type_system.NewUndefinedType(nil)
			c.unifyInner(ctx, value, undefinedType, seen)
		}
	}

	// 3. Collect remaining target properties not matched by effective props.
	remainingElems := []type_system.ObjTypeElem{}
	for _, key := range targetKeys {
		if !usedTargetKeys[key] {
			value := targetNamed[key]
			if value == nil {
				// Setter-only key — not readable, skip.
				continue
			}
			remainingElems = append(remainingElems, &type_system.PropertyElem{
				Name:  key,
				Value: value,
			})
		}
	}

	// 4. Assign remaining properties to unbound rests.
	if len(unboundRests) == 1 {
		objType := type_system.NewObjectType(nil, remainingElems)
		unifyErrors := unifyPair(unboundRests[0], objType)
		errors = slices.Concat(errors, unifyErrors)
	} else if len(unboundRests) > 1 && len(remainingElems) > 0 {
		errors = append(errors, &UnimplementedError{
			message: fmt.Sprintf(
				"cannot distribute %d properties across %d unbound rest spread elements; consider using a single spread or explicit property definitions",
				len(remainingElems),
				len(unboundRests),
			),
		})
	} else if len(unboundRests) == 0 && len(remainingElems) > 0 {
		// All rests are bound but there are leftover properties — error.
		for _, elem := range remainingElems {
			if prop, ok := elem.(*type_system.PropertyElem); ok {
				errors = append(errors, &KeyNotFoundError{
					Object: restObj,
					Key:    prop.Name,
					span:   getKeyNotFoundSpan(restObj, prop.Value),
				})
			}
		}
	}
	// else: no remaining properties — all rests stay as-is (empty objects if unbound)

	return errors
}

// unifyFuncTypes unifies two function types
func (c *Checker) unifyFuncTypes(ctx Context, func1, func2 *type_system.FuncType, seen unifySeen) []Error {
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
			unifyErrors := c.unifyInner(ctx, param2.Type, param1.Type, seen)
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
		unifyErrors := c.unifyInner(ctx, restParam2.Type, restParam1.Type, seen)
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
			unifyErrors := c.unifyInner(ctx, param2.Type, param1.Type, seen)
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
			unifyErrors := c.unifyInner(ctx, restParam.Type, arrayType, seen)
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
		// Neither function has rest parameters
		// In TypeScript, a function with fewer parameters can be assigned to a function
		// type expecting more parameters (e.g., () => {} is valid for (event) => void).
		// This is because when the function is called, extra arguments are simply ignored.

		// Determine the minimum number of parameters to check
		minParams := len(func1.Params)
		if len(func2.Params) < minParams {
			minParams = len(func2.Params)
		}

		// For each parameter in both functions, check type compatibility
		for i := 0; i < minParams; i++ {
			param1 := func1.Params[i]
			param2 := func2.Params[i]

			// Parameter types are contravariant: unify param2.Type with param1.Type
			unifyErrors := c.unifyInner(ctx, param2.Type, param1.Type, seen)
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

		// If func1 has more parameters than func2, those extra params must be optional
		// Otherwise func1 would require arguments that func2 callers won't provide
		for i := len(func2.Params); i < len(func1.Params); i++ {
			if !func1.Params[i].Optional {
				return []Error{&CannotUnifyTypesError{
					T1: func1,
					T2: func2,
				}}
			}
		}

		// If func1 has fewer parameters than func2, that's fine - extra args are ignored
	}

	// Check return types (covariant)
	if func1.Return != nil && func2.Return != nil {
		unifyErrors := c.unifyInner(ctx, func1.Return, func2.Return, seen)
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
		unifyErrors := c.unifyInner(ctx, func1.Throws, func2.Throws, seen)
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
func (c *Checker) bind(ctx Context, t1 type_system.Type, t2 type_system.Type, seen unifySeen) []Error {
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
						errors = c.unifyInner(ctx, typeVar1.Constraint, typeVar2.Constraint, seen)
					} else if typeVar1.Constraint != nil && typeVar2.Constraint == nil {
						// Propagate the constraint to typeVar2 since it becomes the
						// representative of this equivalence class after binding.
						typeVar2.Constraint = typeVar1.Constraint
					}
					// Propagate IsObjectRest so that Prune() returns a TypeVar
					// that preserves the marker for the tuple spread check.
					typeVar2.IsObjectRest = typeVar2.IsObjectRest || typeVar1.IsObjectRest
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
					errors = c.unifyInner(ctx, typeVar1.Constraint, t2, seen)
				}
				// IsParam is only set on fresh vars for unannotated parameters
				// (infer_func.go), so IsParam and Constraint won't both be set
				// today. If that changes, constraint unification above may bind
				// the type variable as a side effect, and openClosedObjectForParam
				// checks Instance != nil to avoid double-binding.
				if typeVar1.IsParam {
					if opened := c.openClosedObjectForParam(typeVar1, t2); opened {
						return errors
					}
				}
				// When a TypeVar with an ArrayConstraint is bound to a concrete
				// Array or tuple type, update the constraint flags so that
				// resolution at closing time produces the correct type.
				if typeVar1.ArrayConstraint != nil {
					if handled, bindErrs := c.handleArrayConstraintBinding(ctx, typeVar1, t2, seen); handled {
						return append(errors, bindErrs...)
					}
				}
				// When binding a Widenable TypeVar, widen literals to their
				// primitive types and recursively widen object/tuple literals
				// (e.g. "hello" -> string, {x: 1} -> {x: number}).
				targetType := t2
				if typeVar1.Widenable {
					targetType = widenLiteral(targetType)
				}
				// We need to know if typeVar1 was inferred from a new binding or not
				if typeVar1.FromBinding {
					typeVar1.Instance = removeUncertainMutability(targetType)
				} else {
					typeVar1.Instance = targetType
				}
				// QUESTION: What should the provenance be if t2 is a type_system.MutabilityType?
				typeVar1.SetProvenance(&type_system.TypeProvenance{
					Type: targetType,
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
					errors = c.unifyInner(ctx, t1, typeVar2.Constraint, seen)
				}
				// See comment in typeVar1 branch above re: IsParam and Constraint.
				if typeVar2.IsParam {
					if opened := c.openClosedObjectForParam(typeVar2, t1); opened {
						return errors
					}
				}
				// When binding a Widenable TypeVar, widen literals to their
				// primitive types and recursively widen object/tuple literals
				// (e.g. "hello" -> string, {x: 1} -> {x: number}).
				targetType := t1
				if typeVar2.Widenable {
					targetType = widenLiteral(targetType)
				}
				// We need to know if typeVar2 was inferred from a new binding or not
				if typeVar2.FromBinding {
					typeVar2.Instance = removeUncertainMutability(targetType)
				} else {
					typeVar2.Instance = targetType
				}
				// QUESTION: What should the provenance be if t1 is a type_system.MutabilityType?
				typeVar2.SetProvenance(&type_system.TypeProvenance{
					Type: targetType,
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

func (v *OccursInVisitor) EnterType(t type_system.Type) type_system.EnterResult {
	// No-op for entry
	return type_system.EnterResult{}
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
	if visitor.result {
		return true
	}
	// Defensive: Accept doesn't traverse ArrayConstraint children, so check
	// them explicitly. In practice a TypeVar is unlikely to occur inside its
	// own ArrayConstraint, but a missed occurs check could cause an infinite
	// loop during unification. Note that this check is already recursive:
	// the occursInType calls below will themselves hit this same block if
	// ElemTypeVar or a LiteralIndexes entry is a TypeVar with its own
	// ArrayConstraint, so nested constraints are covered without additional work.
	if tv, ok := type_system.Prune(t2).(*type_system.TypeVarType); ok && tv.ArrayConstraint != nil {
		if occursInType(t1, tv.ArrayConstraint.ElemTypeVar) {
			return true
		}
		for _, elemTV := range tv.ArrayConstraint.LiteralIndexes {
			if occursInType(t1, elemTV) {
				return true
			}
		}
	}
	return false
}

// handleArrayConstraintBinding handles the case where a TypeVarType with an
// ArrayConstraint is being bound to a concrete type. If the bound type is an
// Array, mut Array, or tuple, the constraint is updated accordingly and the
// function returns true to indicate that binding was handled. Otherwise it
// returns false and normal binding proceeds.
func (c *Checker) handleArrayConstraintBinding(ctx Context, typeVar *type_system.TypeVarType, boundType type_system.Type, seen unifySeen) (bool, []Error) {
	constraint := typeVar.ArrayConstraint
	inner := boundType
	isMut := false
	if mut, ok := inner.(*type_system.MutabilityType); ok {
		isMut = mut.Mutability == type_system.MutabilityMutable
		inner = mut.Type
	}

	switch t := inner.(type) {
	case *type_system.TypeRefType:
		if c.isArrayType(t) && len(t.TypeArgs) > 0 {
			// Passed to Array<T> or mut Array<T> — force array resolution
			constraint.HasNonLiteralIndex = true
			if isMut {
				constraint.HasMutatingMethod = true
			}
			// Unify the constraint's element type var with the array's element type
			var errs []Error
			if unifyErrs := c.unifyInner(ctx, constraint.ElemTypeVar, t.TypeArgs[0], seen); len(unifyErrs) > 0 {
				errs = append(errs, unifyErrs...)
			}
			for _, elemTV := range constraint.LiteralIndexes {
				if unifyErrs := c.unifyInner(ctx, elemTV, t.TypeArgs[0], seen); len(unifyErrs) > 0 {
					errs = append(errs, unifyErrs...)
				}
			}
			// Bind the TypeVar to the array type and clear the constraint so
			// that resolveArrayConstraintsInType won't re-resolve it.
			typeVar.Instance = boundType
			typeVar.ArrayConstraint = nil
			return true, errs
		}
	case *type_system.TupleType:
		// Passed to a tuple type — unify element types pairwise, handling
		// RestSpreadType for variadic tuples (e.g. [number, string, ...Array<boolean>]).
		prefix, rest, suffix := splitTupleAtRest(t.Elems)
		var errs []Error
		for idx, tv := range constraint.LiteralIndexes {
			var targetType type_system.Type
			if idx < len(prefix) {
				// Index falls in the fixed prefix
				targetType = prefix[idx]
			} else if rest != nil {
				// Compute where suffix starts in the logical tuple
				// (unknown length, so suffix indexes can't be mapped from
				// literal indexes). Indexes beyond the prefix fall into rest.
				targetType = rest.Type
				// Extract element type from Array<T> if the rest is an array
				if ref, ok := type_system.Prune(targetType).(*type_system.TypeRefType); ok && c.isArrayType(ref) && len(ref.TypeArgs) > 0 {
					targetType = ref.TypeArgs[0]
				}
			} else if idx < len(prefix)+len(suffix) {
				// No rest, index falls in suffix (fixed tuple)
				targetType = suffix[idx-len(prefix)]
			}
			if targetType != nil {
				if unifyErrs := c.unifyInner(ctx, tv, targetType, seen); len(unifyErrs) > 0 {
					errs = append(errs, unifyErrs...)
				}
			}
		}
		// Bind the TypeVar to the tuple and clear the constraint so that
		// resolveArrayConstraintsInType won't recreate a different tuple.
		typeVar.Instance = boundType
		typeVar.ArrayConstraint = nil
		return true, errs
	}
	return false, nil
}

// This is needed because when an unannotated parameter (e.g. `fn foo(obj)`) is
// passed to a function with a typed parameter (e.g. `fn bar(x: {a: number})`),
// bind() would normally set the type variable's Instance to the closed ObjectType
// from bar's annotation. That would prevent the parameter from accepting additional
// properties inferred from other usage in the function body (e.g. `obj.b = "hi"`).
// By converting to an open copy with a RestSpreadElem row variable, the parameter
// picks up bar's constraints while remaining extensible.
func (c *Checker) openClosedObjectForParam(typeVar *type_system.TypeVarType, boundType type_system.Type) bool {
	if typeVar.Instance != nil {
		return false // already bound (e.g. during constraint unification)
	}
	// Unwrap MutabilityType if present (e.g. `fn bar(x: mut {a: number})`).
	var mutWrapper *type_system.MutabilityType
	inner := boundType
	if mut, ok := inner.(*type_system.MutabilityType); ok {
		mutWrapper = mut
		inner = mut.Type
	}
	closedObj, ok := inner.(*type_system.ObjectType)
	if !ok || closedObj.Open || closedObj.Nominal {
		return false
	}
	// Deep-copy elements so that mutations (e.g. Written flag) on the open
	// copy do not leak back to the closed source type.
	elems := copyObjTypeElems(closedObj.Elems)
	elems = append(elems, type_system.NewRestSpreadElem(c.FreshVar(nil)))
	openCopy := &type_system.ObjectType{
		Elems:     elems,
		Open:      true,
		Immutable: closedObj.Immutable,
		Mutable:   closedObj.Mutable,
	}
	// Re-wrap in MutabilityType if the original was wrapped.
	if mutWrapper != nil {
		typeVar.Instance = &type_system.MutabilityType{
			Type:       openCopy,
			Mutability: mutWrapper.Mutability,
		}
	} else {
		typeVar.Instance = openCopy
	}
	// Provenance points to the original closed type (not the open copy) so
	// that error messages and diagnostics can trace back to the source
	// annotation. This early return skips bind()'s normal provenance write,
	// which would also set it to boundType — so the result is the same.
	typeVar.SetProvenance(&type_system.TypeProvenance{
		Type: boundType,
	})
	return true
}

// copyObjTypeElem returns a shallow copy of elem so that mutable fields
// (e.g. PropertyElem.Written) are not shared between the source and the copy.
// All named elem types are copied defensively so that future mutable fields
// on any elem type are automatically isolated.
//
// Written is reset to false on the copy, so this function should only be used
// when copying elems from a closed type annotation into an open inferred type.
// In that context the new type hasn't written to the property yet, and any
// future writes will set Written on the copy without affecting the original.
// Do NOT use this when copying between two open types in the same function
// body, as it would incorrectly discard existing Written information.
func copyObjTypeElem(elem type_system.ObjTypeElem) type_system.ObjTypeElem {
	switch e := elem.(type) {
	case *type_system.PropertyElem:
		cp := *e
		cp.Written = false
		return &cp
	case *type_system.MethodElem:
		cp := *e
		return &cp
	case *type_system.GetterElem:
		cp := *e
		return &cp
	case *type_system.SetterElem:
		cp := *e
		return &cp
	case *type_system.IndexSignatureElem:
		cp := *e
		return &cp
	default:
		return elem
	}
}

// copyObjTypeElems returns a shallow copy of the slice, delegating to
// copyObjTypeElem for each element. See copyObjTypeElem for usage constraints.
func copyObjTypeElems(elems []type_system.ObjTypeElem) []type_system.ObjTypeElem {
	out := make([]type_system.ObjTypeElem, len(elems))
	for i, elem := range elems {
		out[i] = copyObjTypeElem(elem)
	}
	return out
}

type RemoveUncertainMutabilityVisitor struct{}

func (v *RemoveUncertainMutabilityVisitor) EnterType(t type_system.Type) type_system.EnterResult {
	// No-op for entry
	return type_system.EnterResult{}
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

// distributeIntersectionOverUnion distributes an intersection type over any unions it contains.
// For example: A & (B | C) becomes (A & B) | (A & C)
// Returns the original type if no distribution is needed or if distribution involves problematic nominal types.
func distributeIntersectionOverUnion(intersection *type_system.IntersectionType) (type_system.Type, bool) {
	// Check if any of the types in the intersection is a union
	var unionIndex = -1
	for i, t := range intersection.Types {
		t = type_system.Prune(t)
		if _, ok := t.(*type_system.UnionType); ok {
			unionIndex = i
			break
		}
	}

	// No union found, return the intersection as-is
	if unionIndex == -1 {
		return intersection, false
	}

	union := type_system.Prune(intersection.Types[unionIndex]).(*type_system.UnionType)

	otherTypes := make([]type_system.Type, 0, len(intersection.Types)-1)
	otherTypes = append(otherTypes, intersection.Types[:unionIndex]...)
	otherTypes = append(otherTypes, intersection.Types[unionIndex+1:]...)

	// Create a new union where each member is the intersection of otherTypes with that union member
	distributedTypes := make([]type_system.Type, 0, len(union.Types))
	for _, unionMember := range union.Types {
		// Create intersection: otherTypes & unionMember
		intersectionTypes := make([]type_system.Type, 0, len(otherTypes)+1)
		intersectionTypes = append(intersectionTypes, otherTypes...)
		intersectionTypes = append(intersectionTypes, unionMember)

		// Create the intersection (NewIntersectionType will normalize it)
		newIntersection := type_system.NewIntersectionType(nil, intersectionTypes...)

		distributedTypes = append(distributedTypes, newIntersection)
	}

	return type_system.NewUnionType(nil, distributedTypes...), true
}

// unwrapMutability strips a synthetic uncertain-mutability (mut?) wrapper if
// present, returning the inner type. Explicit mut wrappers are preserved.
// This is used during property widening to avoid leaking mut? wrappers into
// union members — those wrappers come from the open object constructor and
// are resolved during generalization.
func unwrapMutability(t type_system.Type) type_system.Type {
	if mut, ok := t.(*type_system.MutabilityType); ok && mut.Mutability == type_system.MutabilityUncertain {
		return mut.Type
	}
	return t
}

// widenLiteral recursively widens types for Widenable TypeVar bindings:
//   - LitType → corresponding PrimType (e.g. "hello" → string, 42 → number)
//   - ObjectType → copy with property values recursively widened
//   - TupleType → copy with element types recursively widened
//
// If the type is wrapped in a MutabilityType, explicit mut is preserved but
// uncertain mut? is stripped for primitives (scalar values that are replaced,
// not mutated in place) and for structured types (to avoid leaking mut? into
// inferred signatures). Types that don't match any case are returned unchanged.
func widenLiteral(t type_system.Type) type_system.Type {
	inner := t
	var mutWrapper *type_system.MutabilityType
	if mut, ok := inner.(*type_system.MutabilityType); ok {
		mutWrapper = mut
		inner = mut.Type
	}
	// Follow TypeVar instances so we can widen the underlying concrete type
	// (e.g. MutabilityType wrapping a TypeVar whose Instance is an ObjectType).
	inner = type_system.Prune(inner)

	var widened type_system.Type
	switch v := inner.(type) {
	case *type_system.LitType:
		widened = widenLitToPrim(v)
		if widened == nil {
			return t
		}
	case *type_system.ObjectType:
		widened = widenObjectLiterals(v)
	case *type_system.TupleType:
		widened = widenTupleLiterals(v)
	default:
		return t
	}

	// Preserve explicit mut but strip uncertain mut?. The mut? wrapper on
	// property values tracks whether the value will be mutated, which
	// generalization later resolves to mut or immutable. Stripping it here
	// prevents it from leaking into inferred type signatures.
	if mutWrapper != nil && mutWrapper.Mutability != type_system.MutabilityUncertain {
		return &type_system.MutabilityType{
			Type:       widened,
			Mutability: mutWrapper.Mutability,
		}
	}
	return widened
}

// widenLitToPrim converts a LitType to its corresponding PrimType.
// Returns nil if the literal kind is not recognized.
func widenLitToPrim(lit *type_system.LitType) type_system.Type {
	switch lit.Lit.(type) {
	case *type_system.NumLit:
		return type_system.NewNumPrimType(nil)
	case *type_system.StrLit:
		return type_system.NewStrPrimType(nil)
	case *type_system.BoolLit:
		return type_system.NewBoolPrimType(nil)
	case *type_system.BigIntLit:
		// TODO(#228): bigint literal widening is implemented here but
		// untestable until the parser supports bigint literals (e.g. 1n)
		// in all expression positions.
		return type_system.NewBigIntPrimType(nil)
	default:
		return nil
	}
}

// widenObjectLiterals returns a copy of the ObjectType with all element types
// recursively widened (literals → primitives). Handles property values, method
// param/return types, and getter/setter types.
func widenObjectLiterals(obj *type_system.ObjectType) *type_system.ObjectType {
	newElems := make([]type_system.ObjTypeElem, len(obj.Elems))
	changed := false
	for i, elem := range obj.Elems {
		switch e := elem.(type) {
		case *type_system.PropertyElem:
			// Prune through TypeVars, then widen. widenLiteral handles
			// stripping uncertain mut? from literals while preserving it
			// on objects/tuples so generalization can resolve mutability.
			val := type_system.Prune(e.Value)
			widened := widenLiteral(val)
			if widened != e.Value {
				changed = true
				newElems[i] = &type_system.PropertyElem{
					Name:     e.Name,
					Optional: e.Optional,
					Readonly: e.Readonly,
					Value:    widened,
					Written:  e.Written,
				}
			} else {
				newElems[i] = elem
			}
		case *type_system.MethodElem:
			if fn := widenFuncType(e.Fn); fn != e.Fn {
				changed = true
				newElems[i] = &type_system.MethodElem{
					Name: e.Name, Fn: fn, MutSelf: e.MutSelf,
				}
			} else {
				newElems[i] = elem
			}
		case *type_system.GetterElem:
			if fn := widenFuncType(e.Fn); fn != e.Fn {
				changed = true
				newElems[i] = &type_system.GetterElem{
					Name: e.Name, Fn: fn,
				}
			} else {
				newElems[i] = elem
			}
		case *type_system.SetterElem:
			if fn := widenFuncType(e.Fn); fn != e.Fn {
				changed = true
				newElems[i] = &type_system.SetterElem{
					Name: e.Name, Fn: fn,
				}
			} else {
				newElems[i] = elem
			}
		default:
			newElems[i] = elem
		}
	}
	if !changed {
		return obj
	}
	result := type_system.NewObjectType(nil, newElems)
	result.Open = obj.Open
	return result
}

// widenFuncType returns a copy of the FuncType with parameter and return types
// recursively widened. Returns the original if nothing changed.
func widenFuncType(fn *type_system.FuncType) *type_system.FuncType {
	changed := false

	ret := type_system.Prune(fn.Return)
	widenedReturn := widenLiteral(ret)
	if widenedReturn != fn.Return {
		changed = true
	}

	newParams := make([]*type_system.FuncParam, len(fn.Params))
	for i, p := range fn.Params {
		pt := type_system.Prune(p.Type)
		widened := widenLiteral(pt)
		if widened != p.Type {
			changed = true
			newParams[i] = &type_system.FuncParam{
				Pattern:  p.Pattern,
				Type:     widened,
				Optional: p.Optional,
			}
		} else {
			newParams[i] = p
		}
	}

	if !changed {
		return fn
	}
	return type_system.NewFuncType(nil, fn.TypeParams, newParams, widenedReturn, fn.Throws)
}

// widenTupleLiterals returns a copy of the TupleType with all element types
// recursively widened (literals → primitives) and uncertain mutability wrappers
// stripped.
func widenTupleLiterals(tuple *type_system.TupleType) *type_system.TupleType {
	newElems := make([]type_system.Type, len(tuple.Elems))
	changed := false
	for i, elem := range tuple.Elems {
		val := type_system.Prune(elem)
		widened := widenLiteral(val)
		if widened != elem {
			changed = true
		}
		newElems[i] = widened
	}
	if !changed {
		return tuple
	}
	return type_system.NewTupleType(nil, newElems...)
}

// widenableInstanceChain returns the Widenable TypeVars from tv's alias chain.
// If tv has an InstanceChain (populated by Prune when it path-compressed
// through other TypeVars), only the Widenable members are returned. If tv is a
// single Widenable TypeVar with no chain, it is returned as a one-element
// slice. Returns nil if tv is nil or not Widenable.
func widenableInstanceChain(tv *type_system.TypeVarType) []*type_system.TypeVarType {
	if tv == nil || !tv.Widenable {
		return nil
	}
	if tv.InstanceChain == nil {
		return []*type_system.TypeVarType{tv}
	}
	var widenable []*type_system.TypeVarType
	for _, member := range tv.InstanceChain {
		if member.Widenable {
			widenable = append(widenable, member)
		}
	}
	return widenable
}

// collectUnionMembers collects the leaf (non-union) types from t. If t is a
// UnionType its members are recursively flattened; otherwise t itself is returned.
func collectUnionMembers(t type_system.Type) []type_system.Type {
	if union, ok := t.(*type_system.UnionType); ok {
		var members []type_system.Type
		for _, m := range union.Types {
			members = append(members, collectUnionMembers(m)...)
		}
		return members
	}
	return []type_system.Type{t}
}

// flatUnion builds a union from oldType and newType, flattening either operand
// if it is already a UnionType so the result is always a single-level union.
func flatUnion(oldType, newType type_system.Type) type_system.Type {
	oldMembers := collectUnionMembers(oldType)
	newMembers := collectUnionMembers(newType)
	// Deduplicate: only add new members that aren't already present.
	members := oldMembers
	for _, n := range newMembers {
		found := false
		for _, o := range oldMembers {
			if type_system.Equals(o, n) {
				found = true
				break
			}
		}
		if !found {
			members = append(members, n)
		}
	}
	return type_system.NewUnionType(nil, members...)
}

// typeContains checks whether every leaf type in needle is already present in
// haystack. Both sides are flattened if they are UnionTypes.
func typeContains(haystack type_system.Type, needle type_system.Type) bool {
	haystackMembers := collectUnionMembers(haystack)
	for _, n := range collectUnionMembers(needle) {
		found := false
		for _, h := range haystackMembers {
			if type_system.Equals(h, n) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// splitTupleAtRest splits a tuple's elements into a prefix of fixed elements,
// the RestSpreadType (if any), and a suffix of fixed elements after the rest.
// If no RestSpreadType is found, rest is nil and suffix is empty.
func splitTupleAtRest(elems []type_system.Type) (prefix []type_system.Type, rest *type_system.RestSpreadType, suffix []type_system.Type) {
	for i, elem := range elems {
		if r, ok := elem.(*type_system.RestSpreadType); ok {
			return elems[:i], r, elems[i+1:]
		}
	}
	return elems, nil, nil
}

// unifyTuples handles all tuple-vs-tuple unification cases including
// RestSpreadType at any position.
func (c *Checker) unifyTuples(ctx Context, tuple1, tuple2 *type_system.TupleType, seen unifySeen) []Error {
	prefix1, rest1, suffix1 := splitTupleAtRest(tuple1.Elems)
	prefix2, rest2, suffix2 := splitTupleAtRest(tuple2.Elems)

	// Case: neither side has a rest spread — plain tuple unification
	if rest1 == nil && rest2 == nil {
		return c.unifyFixedTuples(ctx, tuple1, tuple2, seen)
	}

	// Case: only tuple2 has a rest spread — fixed-vs-variadic
	if rest1 == nil && rest2 != nil {
		return c.unifyFixedVsVariadic(ctx, tuple1.Elems, prefix2, rest2, suffix2, seen)
	}

	// Case: only tuple1 has a rest spread — variadic-vs-fixed (mirror)
	if rest1 != nil && rest2 == nil {
		return c.unifyVariadicVsFixed(ctx, prefix1, rest1, suffix1, tuple2.Elems, seen)
	}

	// Case: both sides have a rest spread — variadic-vs-variadic
	return c.unifyVariadicVsVariadic(ctx, prefix1, rest1, suffix1, prefix2, rest2, suffix2, seen)
}

// unifyFixedTuples handles unification of two tuples with no RestSpreadType
// elements. The call convention is Unify(source, target): tuple1 is the value
// being assigned and tuple2 is the target (pattern or type annotation).
//
//   - tuple1 has more elements than tuple2: OK — extras in the value are ignored.
//   - tuple2 has more elements than tuple1: Error — the target expects more
//     elements than the value provides (NotEnoughElementsToUnpackError).
//   - Same length: unify pairwise.
func (c *Checker) unifyFixedTuples(ctx Context, tuple1, tuple2 *type_system.TupleType, seen unifySeen) []Error {
	errors := []Error{}

	if len(tuple2.Elems) > len(tuple1.Elems) {
		// The target (tuple2) expects more elements than the value (tuple1)
		// provides. Unify the elements that are present in both tuples, then
		// report the extra target bindings as unpack errors.
		for elem1, elem2 := range Zip(tuple1.Elems, tuple2.Elems) {
			unifyErrors := c.unifyInner(ctx, elem1, elem2, seen)
			errors = slices.Concat(errors, unifyErrors)
		}

		extraElems := tuple2.Elems[len(tuple1.Elems):]
		first := GetNode(extraElems[0].Provenance())
		last := GetNode(extraElems[len(extraElems)-1].Provenance())

		// Type the extra target bindings as `undefined` since they have no
		// corresponding value element.
		for _, elem2 := range extraElems {
			node := GetNode(elem2.Provenance())
			undefined := type_system.NewUndefinedType(&ast.NodeProvenance{Node: node})
			unifyErrors := c.unifyInner(ctx, elem2, undefined, seen)
			errors = slices.Concat(errors, unifyErrors)
		}

		return slices.Concat(errors, []Error{&NotEnoughElementsToUnpackError{
			span: ast.MergeSpans(first.Span(), last.Span()),
		}})
	}

	// tuple1 has >= tuple2's length. Unify pairwise up to tuple2's length;
	// any extra elements in tuple1 (the value) are ignored.
	for i, elem2 := range tuple2.Elems {
		unifyErrors := c.unifyInner(ctx, tuple1.Elems[i], elem2, seen)
		errors = slices.Concat(errors, unifyErrors)
	}

	return errors
}

// unifyFixedVsVariadic handles: source is a fixed tuple, target is variadic.
// The call convention is Unify(source, target): fixedElems is the value being
// assigned and prefix2/rest2/suffix2 describe the target's structure.
//
// The target requires at least len(prefix2)+len(suffix2) elements from the
// source to fill its mandatory positions. If the source doesn't have enough,
// that's an error. Any extra source elements beyond the mandatory positions
// are absorbed by the target's rest spread.
//
// Example: [1, "a", "b"] vs [number, ...Array<string>]
// - prefix: Unify(1, number)
// - rest absorbs ["a", "b"]: Unify(["a", "b"], Array<string>)
func (c *Checker) unifyFixedVsVariadic(
	ctx Context,
	fixedElems []type_system.Type,
	prefix2 []type_system.Type, rest2 *type_system.RestSpreadType, suffix2 []type_system.Type,
	seen unifySeen,
) []Error {
	requiredCount := len(prefix2) + len(suffix2)
	if len(fixedElems) < requiredCount {
		// The source doesn't have enough elements to fill the target's
		// mandatory prefix and suffix positions.
		return []Error{&CannotUnifyTypesError{
			T1: type_system.NewTupleType(nil, fixedElems...),
			T2: type_system.NewTupleType(nil, append(append(prefix2, rest2), suffix2...)...),
		}}
	}

	errors := []Error{}

	// Unify prefix elements pairwise
	for i, elem2 := range prefix2 {
		unifyErrors := c.unifyInner(ctx, fixedElems[i], elem2, seen)
		errors = slices.Concat(errors, unifyErrors)
	}

	// Unify suffix elements pairwise (from the end)
	for i, elem2 := range suffix2 {
		fixedIdx := len(fixedElems) - len(suffix2) + i
		unifyErrors := c.unifyInner(ctx, fixedElems[fixedIdx], elem2, seen)
		errors = slices.Concat(errors, unifyErrors)
	}

	// The middle elements are absorbed by the rest spread
	middleElems := fixedElems[len(prefix2) : len(fixedElems)-len(suffix2)]
	restTuple := type_system.NewTupleType(nil, middleElems...)
	unifyErrors := c.unifyInner(ctx, restTuple, rest2.Type, seen)
	errors = slices.Concat(errors, unifyErrors)

	return errors
}

// unifyVariadicVsFixed handles: source is variadic, target is a fixed tuple.
// The call convention is Unify(source, target): prefix1/rest1/suffix1 describe
// the source's structure and fixedElems is the target.
//
// The target only needs its own positions filled. The source's prefix and
// suffix are mandatory elements in the source — we unify them pairwise with
// the target up to the target's length. If the source has more mandatory
// elements than the target, the extras are ignored (same convention as
// unifyFixedTuples). The rest spread binds to the remaining target elements
// between the prefix and suffix matches.
//
// Example: [number, ...T] (source) vs [number, string] (target)
// - prefix: Unify(number, number)
// - rest absorbs [string]: Unify(T, [string])
func (c *Checker) unifyVariadicVsFixed(
	ctx Context,
	prefix1 []type_system.Type, rest1 *type_system.RestSpreadType, suffix1 []type_system.Type,
	fixedElems []type_system.Type,
	seen unifySeen,
) []Error {
	errors := []Error{}

	// Unify prefix elements pairwise, up to the shorter of the two.
	// If the source prefix is longer, extras are ignored.
	prefixLen := min(len(prefix1), len(fixedElems))
	for i := range prefixLen {
		unifyErrors := c.unifyInner(ctx, prefix1[i], fixedElems[i], seen)
		errors = slices.Concat(errors, unifyErrors)
	}

	// If the target was exhausted by the prefix, the rest and suffix are
	// extra source elements — ignored per the source-extras convention.
	if len(fixedElems) <= len(prefix1) {
		return errors
	}

	// Unify suffix elements pairwise from the end, up to the shorter of the two.
	// Anchoring the suffix to the end of fixedElems is sound regardless of
	// whether rest1.Type is concrete or unresolved: the suffix's position at the
	// end of the source tuple is a structural invariant of the type (e.g. in
	// [...T, string], the string is always last no matter what T resolves to).
	// The rest absorbs whatever target elements remain between the prefix and
	// suffix matches.
	remaining := fixedElems[prefixLen:]
	suffixLen := min(len(suffix1), len(remaining))
	for i := range suffixLen {
		unifyErrors := c.unifyInner(ctx, suffix1[len(suffix1)-suffixLen+i], remaining[len(remaining)-suffixLen+i], seen)
		errors = slices.Concat(errors, unifyErrors)
	}

	// The middle target elements are absorbed by the source's rest spread
	middleElems := remaining[:len(remaining)-suffixLen]
	restTuple := type_system.NewTupleType(nil, middleElems...)
	unifyErrors := c.unifyInner(ctx, rest1.Type, restTuple, seen)
	errors = slices.Concat(errors, unifyErrors)

	return errors
}

// unifyVariadicVsVariadic handles: tuple with rest vs tuple with rest.
// Example: [A, ...R1, B] vs [C, ...R2, D]
// - Unify prefixes pairwise up to the shorter prefix
// - Unify suffixes pairwise up to the shorter suffix
// - Collect any extra prefix/suffix elements from the longer side
// - Unify the rest types, wrapping extras in a tuple with rest if needed
func (c *Checker) unifyVariadicVsVariadic(
	ctx Context,
	prefix1 []type_system.Type, rest1 *type_system.RestSpreadType, suffix1 []type_system.Type,
	prefix2 []type_system.Type, rest2 *type_system.RestSpreadType, suffix2 []type_system.Type,
	seen unifySeen,
) []Error {
	errors := []Error{}

	// Unify prefixes pairwise up to the shorter one
	minPrefix := min(len(prefix1), len(prefix2))
	for i := 0; i < minPrefix; i++ {
		unifyErrors := c.unifyInner(ctx, prefix1[i], prefix2[i], seen)
		errors = slices.Concat(errors, unifyErrors)
	}

	// Unify suffixes pairwise up to the shorter one (from the end)
	minSuffix := min(len(suffix1), len(suffix2))
	for i := 0; i < minSuffix; i++ {
		unifyErrors := c.unifyInner(ctx, suffix1[len(suffix1)-minSuffix+i], suffix2[len(suffix2)-minSuffix+i], seen)
		errors = slices.Concat(errors, unifyErrors)
	}

	// Collect extras from the longer prefix/suffix
	extraPrefix1 := prefix1[minPrefix:]
	extraPrefix2 := prefix2[minPrefix:]
	extraSuffix1 := suffix1[:len(suffix1)-minSuffix]
	extraSuffix2 := suffix2[:len(suffix2)-minSuffix]

	// Build the types for the rest unification.
	// If one side has extras, the other side's rest absorbs them.
	// E.g. [A, B, ...R1] vs [A, ...R2] → Unify(R2, [B, ...R1])
	if len(extraPrefix1) == 0 && len(extraSuffix1) == 0 && len(extraPrefix2) == 0 && len(extraSuffix2) == 0 {
		// Same number of fixed elements on both sides — unify rests directly
		unifyErrors := c.unifyInner(ctx, rest1.Type, rest2.Type, seen)
		errors = slices.Concat(errors, unifyErrors)
	} else if len(extraPrefix2) == 0 && len(extraSuffix2) == 0 {
		// tuple1 has extra fixed elements; rest2 absorbs them
		// [A, B, C, ...R1] vs [A, ...R2] → R2 = [B, C, ...R1]
		var wrapped []type_system.Type
		wrapped = append(wrapped, extraPrefix1...)
		wrapped = append(wrapped, rest1)
		wrapped = append(wrapped, extraSuffix1...)
		wrappedTuple := type_system.NewTupleType(nil, wrapped...)
		unifyErrors := c.unifyInner(ctx, wrappedTuple, rest2.Type, seen)
		errors = slices.Concat(errors, unifyErrors)
	} else if len(extraPrefix1) == 0 && len(extraSuffix1) == 0 {
		// tuple2 has extra fixed elements; rest1 absorbs them
		var wrapped []type_system.Type
		wrapped = append(wrapped, extraPrefix2...)
		wrapped = append(wrapped, rest2)
		wrapped = append(wrapped, extraSuffix2...)
		wrappedTuple := type_system.NewTupleType(nil, wrapped...)
		unifyErrors := c.unifyInner(ctx, rest1.Type, wrappedTuple, seen)
		errors = slices.Concat(errors, unifyErrors)
	} else {
		// Both sides have extras — cannot unify
		return []Error{&CannotUnifyTypesError{
			T1: type_system.NewTupleType(nil, append(append(prefix1, rest1), suffix1...)...),
			T2: type_system.NewTupleType(nil, append(append(prefix2, rest2), suffix2...)...),
		}}
	}

	return errors
}

// collectedElemTypes holds the result of collectObjElemTypes.
// Read and Write maps track readable and writable types separately:
//   - PropertyElem/MethodElem populate Read (and PropertyElem also populates Write)
//   - GetterElem populates Read only
//   - SetterElem populates Write only
type collectedElemTypes struct {
	Read            map[type_system.ObjTypeKey]type_system.Type        // readable types (Property, Method, Getter)
	Write           map[type_system.ObjTypeKey]type_system.Type        // writable types (Property, Setter)
	Keys            []type_system.ObjTypeKey                           // all names, deduplicated, in insertion order
	OrigRead        map[type_system.ObjTypeKey]type_system.ObjTypeElem // original readable elem; nil if not requested
	OrigWrite       map[type_system.ObjTypeKey]type_system.ObjTypeElem // original writable elem; nil if not requested
	RestTypes       []type_system.Type
	IndexSignatures []*type_system.IndexSignatureElem // index signatures (e.g. [key: string]: T)
}

// collectObjElemTypes extracts named property types from an ObjectType into
// separate Read and Write maps. RestSpreadElem values are collected separately
// in RestTypes.
func collectObjElemTypes(obj *type_system.ObjectType, collectOrigElems bool) collectedElemTypes {
	result := collectedElemTypes{
		Read:  make(map[type_system.ObjTypeKey]type_system.Type),
		Write: make(map[type_system.ObjTypeKey]type_system.Type),
	}
	if collectOrigElems {
		result.OrigRead = make(map[type_system.ObjTypeKey]type_system.ObjTypeElem)
		result.OrigWrite = make(map[type_system.ObjTypeKey]type_system.ObjTypeElem)
	}
	seen := make(map[type_system.ObjTypeKey]bool)
	for _, elem := range obj.Elems {
		switch elem := elem.(type) {
		case *type_system.PropertyElem:
			propType := elem.Value
			if elem.Optional {
				propType = type_system.NewUnionType(nil, propType, type_system.NewUndefinedType(nil))
			}
			result.Read[elem.Name] = propType
			result.Write[elem.Name] = propType
			if !seen[elem.Name] {
				result.Keys = append(result.Keys, elem.Name)
				seen[elem.Name] = true
			}
			if collectOrigElems {
				result.OrigRead[elem.Name] = elem
				result.OrigWrite[elem.Name] = elem
			}
		case *type_system.MethodElem:
			result.Read[elem.Name] = elem.Fn
			if !seen[elem.Name] {
				result.Keys = append(result.Keys, elem.Name)
				seen[elem.Name] = true
			}
			if collectOrigElems {
				result.OrigRead[elem.Name] = elem
			}
		case *type_system.GetterElem:
			result.Read[elem.Name] = elem.Fn.Return
			if !seen[elem.Name] {
				result.Keys = append(result.Keys, elem.Name)
				seen[elem.Name] = true
			}
			if collectOrigElems {
				result.OrigRead[elem.Name] = elem
			}
		case *type_system.SetterElem:
			result.Write[elem.Name] = elem.Fn.Params[0].Type
			if !seen[elem.Name] {
				result.Keys = append(result.Keys, elem.Name)
				seen[elem.Name] = true
			}
			if collectOrigElems {
				result.OrigWrite[elem.Name] = elem
			}
		case *type_system.IndexSignatureElem:
			result.IndexSignatures = append(result.IndexSignatures, elem)
		case *type_system.RestSpreadElem:
			result.RestTypes = append(result.RestTypes, elem.Value)
		default:
			// skip CallableElem, ConstructorElem, MappedElem, etc.
		}
	}
	return result
}

// findIndexSignatureForKey returns the value type of the index signature
// whose key type is compatible with the given property key, or nil if none match.
// For numeric keys, a numeric index signature is preferred over a string one
// so the result is order-independent.
func findIndexSignatureForKey(key type_system.ObjTypeKey, indexSigs []*type_system.IndexSignatureElem) type_system.Type {
	// For numeric keys, prefer the numeric index signature over the string
	// fallback so that iteration order doesn't affect the result.
	if key.Kind == type_system.NumObjTypeKeyKind {
		var strFallback type_system.Type
		for _, sig := range indexSigs {
			if prim, ok := sig.KeyType.(*type_system.PrimType); ok {
				switch prim.Prim {
				case type_system.NumPrim:
					return sig.Value
				case type_system.StrPrim:
					if strFallback == nil {
						strFallback = sig.Value
					}
				}
			}
		}
		return strFallback
	}

	for _, sig := range indexSigs {
		if prim, ok := sig.KeyType.(*type_system.PrimType); ok {
			switch prim.Prim {
			case type_system.StrPrim:
				if key.Kind == type_system.StrObjTypeKeyKind {
					return sig.Value
				}
			case type_system.SymbolPrim:
				if key.Kind == type_system.SymObjTypeKeyKind {
					return sig.Value
				}
			}
		}
	}
	return nil
}

// findMatchingIndexSignature returns the first index signature in existing
// whose KeyType PrimType matches sig's KeyType PrimType, or nil if none match.
func findMatchingIndexSignature(sig *type_system.IndexSignatureElem, existing []*type_system.IndexSignatureElem) *type_system.IndexSignatureElem {
	sigPrim, ok := sig.KeyType.(*type_system.PrimType)
	if !ok {
		return nil
	}
	for _, e := range existing {
		if ePrim, ok := e.KeyType.(*type_system.PrimType); ok && ePrim.Prim == sigPrim.Prim {
			return e
		}
	}
	return nil
}

// unifyNumericWithStringIndexSigs enforces the TypeScript rule that when an
// object has both [key: string]: S and [index: number]: N, N must be compatible
// with S (because JavaScript coerces numeric keys to strings: obj[1] === obj["1"]).
func (c *Checker) unifyNumericWithStringIndexSigs(ctx Context, sigs []*type_system.IndexSignatureElem, seen unifySeen) []Error {
	var numVal, strVal type_system.Type
	for _, sig := range sigs {
		if prim, ok := sig.KeyType.(*type_system.PrimType); ok {
			switch prim.Prim {
			case type_system.NumPrim:
				numVal = sig.Value
			case type_system.StrPrim:
				strVal = sig.Value
			}
		}
	}
	if numVal != nil && strVal != nil {
		return c.unifyInner(ctx, numVal, strVal, seen)
	}
	return nil
}

func (c *Checker) unifyPatternWithUnion(
	ctx Context,
	pat *type_system.ObjectType,
	union *type_system.UnionType,
	seen unifySeen,
) []Error {
	// 1. Collect pattern field names and their type variables
	patFields := collectObjElemTypes(pat, false).Read

	// 2. For each union member, check if it has ALL pattern fields.
	//    Union members may be TypeRefTypes (e.g. class names) that need expansion.
	matchingFieldTypes := make(map[type_system.ObjTypeKey][]type_system.Type)
	matchedMembers := []type_system.Type{}
	for _, member := range union.Types {
		expanded, _ := c.ExpandType(ctx, member, 1)
		memberObj, ok := expanded.(*type_system.ObjectType)
		if !ok {
			continue // skip non-object union members (e.g. primitive types)
		}
		memberFields := collectObjElemTypes(memberObj, false).Read

		allMatch := true
		for key := range patFields {
			if _, ok := memberFields[key]; !ok {
				allMatch = false
				break
			}
		}
		if allMatch {
			matchedMembers = append(matchedMembers, member)
			for key := range patFields {
				matchingFieldTypes[key] = append(
					matchingFieldTypes[key], memberFields[key],
				)
			}
		}
	}

	// 3. If no members matched, error
	if len(matchedMembers) == 0 {
		return []Error{&CannotUnifyTypesError{T1: pat, T2: union}}
	}

	// 4. Store matched members for future exhaustiveness checking.
	pat.MatchedUnionMembers = matchedMembers

	// 5. Unify each pattern field's type variable with the union of matched types.
	errors := []Error{}
	for key, patType := range patFields {
		fieldUnion := type_system.NewUnionType(nil, matchingFieldTypes[key]...)
		unifyErrors := c.unifyInner(ctx, patType, fieldUnion, seen)
		errors = append(errors, unifyErrors...)
	}
	return errors
}
