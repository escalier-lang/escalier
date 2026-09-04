package ast

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func elemTestSpan() Span {
	return Span{Start: Location{Offset: 3}, End: Location{Offset: 11}, SourceID: 0}
}

func elemTestFn() *FuncTypeAnn {
	return NewFuncTypeAnn(nil, nil, nil, nil, nil, elemTestSpan())
}

// Every variant records the range its constructor was given, so a member's
// position is fixed once it is built rather than written afterwards.
func TestObjTypeAnnElemConstructorsRecordTheSpan(t *testing.T) {
	t.Parallel()
	span := elemTestSpan()
	key := NewIdent("a", span)

	elems := map[string]ObjTypeAnnElem{
		"call signature":      NewCallableTypeAnn(elemTestFn(), span),
		"construct signature": NewConstructorTypeAnn(elemTestFn(), span),
		"method":              NewMethodTypeAnn(key, elemTestFn(), nil, span),
		"getter":              NewGetterTypeAnn(key, elemTestFn(), nil, span),
		"setter":              NewSetterTypeAnn(key, elemTestFn(), nil, span),
		"property":            NewPropertyTypeAnn(key, false, false, NewNumberTypeAnn(span), span),
		"mapped member": NewMappedTypeAnn(
			&IndexParamTypeAnn{Name: "K", Constraint: NewStringTypeAnn(span)},
			nil, NewNumberTypeAnn(span), nil, nil, nil, nil, false, span,
		),
		"rest spread": NewRestSpreadTypeAnn(NewNumberTypeAnn(span), span),
	}
	for name, elem := range elems {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, span, elem.Span())
		})
	}
}

// Key answers for a member's own name. The four variants that declare none
// return nil, which is what lets a caller ask without a type switch.
func TestObjTypeAnnElemKey(t *testing.T) {
	t.Parallel()
	span := elemTestSpan()
	key := NewIdent("a", span)

	t.Run("named variants return the key they were built with", func(t *testing.T) {
		t.Parallel()
		for name, elem := range map[string]ObjTypeAnnElem{
			"method":   NewMethodTypeAnn(key, elemTestFn(), nil, span),
			"getter":   NewGetterTypeAnn(key, elemTestFn(), nil, span),
			"setter":   NewSetterTypeAnn(key, elemTestFn(), nil, span),
			"property": NewPropertyTypeAnn(key, false, false, NewNumberTypeAnn(span), span),
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				require.Same(t, key, elem.Key())
			})
		}
	})

	t.Run("keyless variants return nil", func(t *testing.T) {
		t.Parallel()
		// A mapped member has a Name field, but it names the type a key is
		// rewritten to rather than the key the member is addressed by, so it
		// belongs with the keyless variants.
		for name, elem := range map[string]ObjTypeAnnElem{
			"call signature":      NewCallableTypeAnn(elemTestFn(), span),
			"construct signature": NewConstructorTypeAnn(elemTestFn(), span),
			"mapped member": NewMappedTypeAnn(
				&IndexParamTypeAnn{Name: "K", Constraint: NewStringTypeAnn(span)},
				NewStringTypeAnn(span), NewNumberTypeAnn(span), nil, nil, nil, nil, false, span,
			),
			"rest spread": NewRestSpreadTypeAnn(NewNumberTypeAnn(span), span),
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				require.Nil(t, elem.Key())
			})
		}
	})
}
