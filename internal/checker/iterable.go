package checker

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// getSymbolIteratorID retrieves the unique symbol ID for Symbol.iterator
// from the SymbolConstructor type in the global scope.
func (c *Checker) getSymbolIteratorID() (int, bool) {
	if c.GlobalScope == nil {
		return 0, false
	}

	symbolConstructor := c.GlobalScope.Namespace.Types["SymbolConstructor"]
	if symbolConstructor == nil {
		return 0, false
	}

	objType, ok := type_system.Prune(symbolConstructor.Type).(*type_system.ObjectType)
	if !ok {
		return 0, false
	}

	for _, elem := range objType.Elems {
		if prop, ok := elem.(*type_system.PropertyElem); ok {
			if prop.Name.Kind == type_system.StrObjTypeKeyKind && prop.Name.Str == "iterator" {
				if sym, ok := type_system.Prune(prop.Value).(*type_system.UniqueSymbolType); ok {
					return sym.Value, true
				}
			}
		}
	}

	return 0, false
}

// GetIterableElementType extracts the element type T from an Iterable<T> type.
// It does this by looking up [Symbol.iterator]() on the type, then extracting
// the first type argument from the returned Iterator type.
// Returns nil if the type is not iterable.
func (c *Checker) GetIterableElementType(ctx Context, t type_system.Type) type_system.Type {
	t = type_system.Prune(t)

	// Unwrap MutabilityType
	if mut, ok := t.(*type_system.MutabilityType); ok {
		t = type_system.Prune(mut.Type)
	}

	// Handle TupleType directly - tuples are iterable, yielding the union of element types.
	// RestSpreadType elements (e.g. ...string[] in [number, ...string[]]) are unwrapped
	// to extract the inner array's element type.
	if tuple, ok := t.(*type_system.TupleType); ok {
		if len(tuple.Elems) == 0 {
			return type_system.NewNeverType(nil)
		}
		elemTypes := make([]type_system.Type, 0, len(tuple.Elems))
		for _, elem := range tuple.Elems {
			if rest, ok := elem.(*type_system.RestSpreadType); ok {
				// Extract the element type from the spread's inner type (e.g. Array<string> → string)
				if ref, ok := type_system.Prune(rest.Type).(*type_system.TypeRefType); ok && len(ref.TypeArgs) > 0 {
					elemTypes = append(elemTypes, ref.TypeArgs[0])
				} else {
					elemTypes = append(elemTypes, rest.Type)
				}
			} else {
				elemTypes = append(elemTypes, elem)
			}
		}
		if len(elemTypes) == 1 {
			return elemTypes[0]
		}
		return type_system.NewUnionType(nil, elemTypes...)
	}

	symIterID, ok := c.getSymbolIteratorID()
	if !ok {
		return nil
	}

	// Look up [Symbol.iterator] on the type
	symKey := type_system.NewUniqueSymbolType(nil, symIterID)
	indexKey := IndexKey{
		Type: symKey,
		span: ast.Span{},
	}

	iteratorMethod, errors := c.getMemberType(ctx, t, indexKey)
	if len(errors) > 0 {
		return nil
	}

	iteratorMethod = type_system.Prune(iteratorMethod)

	// [Symbol.iterator] should be a function that returns an Iterator
	funcType, ok := iteratorMethod.(*type_system.FuncType)
	if !ok {
		return nil
	}

	returnType := type_system.Prune(funcType.Return)

	// Extract the element type from the return type.
	// The return type should be Iterator<T, ...>, IterableIterator<T, ...>,
	// ArrayIterator<T>, etc. - all have T as their first type argument.
	return c.extractIteratorElementType(ctx, returnType)
}

// extractIteratorElementType extracts the element type T from an Iterator-like type
// by looking up the `next` method and unifying its return type with IteratorResult<T>.
// This is a structural check — any type with a compatible `next()` method qualifies.
func (c *Checker) extractIteratorElementType(ctx Context, t type_system.Type) type_system.Type {
	if c.GlobalScope == nil {
		return nil
	}
	iteratorResultAlias := c.GlobalScope.Namespace.Types["IteratorResult"]
	if iteratorResultAlias == nil || len(iteratorResultAlias.TypeParams) == 0 {
		return nil
	}

	// Look up the `next` method on the candidate type
	nextKey := PropertyKey{Name: "next", span: ast.Span{}}
	nextMethod, errors := c.getMemberType(ctx, t, nextKey)
	if len(errors) > 0 {
		return nil
	}

	nextMethod = type_system.Prune(nextMethod)
	funcType, ok := nextMethod.(*type_system.FuncType)
	if !ok {
		return nil
	}

	// Create a fresh type variable for the element type T
	freshT := c.FreshVar(nil)
	freshTReturn := c.FreshVar(nil)

	// Build IteratorResult<freshT, freshTReturn> as a TypeRefType
	expectedReturn := type_system.NewTypeRefType(nil, "IteratorResult",
		iteratorResultAlias, freshT, freshTReturn)

	// Unify the next() return type with IteratorResult<T, TReturn>
	unifyErrors := c.Unify(ctx, funcType.Return, expectedReturn)
	if len(unifyErrors) > 0 {
		return nil
	}

	return type_system.Prune(freshT)
}
