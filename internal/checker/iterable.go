package checker

import (
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// getSymbolIteratorID retrieves the unique symbol ID for Symbol.iterator
// from the SymbolConstructor type in the global scope.
func (c *Checker) getSymbolIteratorID() int {
	if c.GlobalScope == nil {
		return -1
	}

	symbolConstructor := c.GlobalScope.Namespace.Types["SymbolConstructor"]
	if symbolConstructor == nil {
		return -1
	}

	objType, ok := type_system.Prune(symbolConstructor.Type).(*type_system.ObjectType)
	if !ok {
		return -1
	}

	for _, elem := range objType.Elems {
		if prop, ok := elem.(*type_system.PropertyElem); ok {
			if prop.Name.Kind == type_system.StrObjTypeKeyKind && prop.Name.Str == "iterator" {
				if sym, ok := type_system.Prune(prop.Value).(*type_system.UniqueSymbolType); ok {
					return sym.Value
				}
			}
		}
	}

	return -1
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

	// Handle TupleType directly - tuples are iterable, yielding the union of element types
	if tuple, ok := t.(*type_system.TupleType); ok {
		switch len(tuple.Elems) {
		case 0:
			return type_system.NewNeverType(nil)
		case 1:
			return tuple.Elems[0]
		default:
			return type_system.NewUnionType(nil, tuple.Elems...)
		}
	}

	symIterID := c.getSymbolIteratorID()
	if symIterID < 0 {
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

// extractIteratorElementType extracts the element type T from an Iterator-like type.
// It handles TypeRefType (e.g. Iterator<T>, IterableIterator<T>, ArrayIterator<T>)
// by returning the first type argument.
func (c *Checker) extractIteratorElementType(ctx Context, t type_system.Type) type_system.Type {
	t = type_system.Prune(t)

	switch rt := t.(type) {
	case *type_system.TypeRefType:
		// Verify this is actually an Iterator-like type before extracting T.
		// Rather than maintaining a hardcoded allowlist, check if the name
		// contains "Iterator" or is "Generator" — this covers Iterator,
		// IterableIterator, IteratorObject, ArrayIterator, MapIterator,
		// SetIterator, StringIterator, and Generator.
		name := type_system.QualIdentToString(rt.Name)
		isIteratorLike := strings.Contains(name, "Iterator") || name == "Generator"
		if len(rt.TypeArgs) > 0 && isIteratorLike {
			return rt.TypeArgs[0]
		}
	case *type_system.ObjectType:
		// For expanded object types, look for the `next()` method and
		// extract the element type from IteratorResult<T, TReturn>
		for _, elem := range rt.Elems {
			if method, ok := elem.(*type_system.MethodElem); ok {
				if method.Name.Kind == type_system.StrObjTypeKeyKind && method.Name.Str == "next" {
					nextReturn := type_system.Prune(method.Fn.Return)
					if ref, ok := nextReturn.(*type_system.TypeRefType); ok {
						name := type_system.QualIdentToString(ref.Name)
						if name == "IteratorResult" && len(ref.TypeArgs) > 0 {
							return ref.TypeArgs[0]
						}
					}
				}
			}
		}
	}

	return nil
}
