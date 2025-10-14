package checker

import (
	. "github.com/escalier-lang/escalier/internal/type_system"
)

func Prelude(c *Checker) *Scope {
	scope := NewScope()

	binArithType := NewFuncType(
		nil,
		[]*FuncParam{
			NewFuncParam(NewIdentPat("a"), NewNumPrimType(nil)),
			NewFuncParam(NewIdentPat("b"), NewNumPrimType(nil)),
		},
		NewNumPrimType(nil),
		NewNeverType(nil),
		nil,
	)
	binArithBinding := Binding{
		Source:  nil,
		Type:    binArithType,
		Mutable: false,
	}

	binCompType := NewFuncType(
		nil,
		[]*FuncParam{
			NewFuncParam(NewIdentPat("a"), NewNumPrimType(nil)),
			NewFuncParam(NewIdentPat("b"), NewNumPrimType(nil)),
		},
		NewBoolPrimType(nil),
		NewNeverType(nil),
		nil,
	)
	binACompBinding := Binding{
		Source:  nil,
		Type:    binCompType,
		Mutable: false,
	}

	binEqType := NewFuncType(
		nil,
		[]*FuncParam{
			NewFuncParam(NewIdentPat("a"), NewAnyType(nil)),
			NewFuncParam(NewIdentPat("b"), NewAnyType(nil)),
		},
		NewBoolPrimType(nil),
		NewNeverType(nil),
		nil,
	)
	binEqBinding := Binding{
		Source:  nil,
		Type:    binEqType,
		Mutable: false,
	}

	binLogicType := NewFuncType(
		nil,
		[]*FuncParam{
			NewFuncParam(NewIdentPat("a"), NewBoolPrimType(nil)),
			NewFuncParam(NewIdentPat("b"), NewBoolPrimType(nil)),
		},
		NewBoolPrimType(nil),
		NewNeverType(nil),
		nil,
	)
	binLogicBinding := Binding{
		Source:  nil,
		Type:    binLogicType,
		Mutable: false,
	}

	// unaryArithType := &FuncType{
	// 	Params: []*FuncParam{
	// 		NewFuncParam(NewIdentPat("a"), NewNumType()),
	// 	},
	// 	Return: NewNumType(),
	// }
	// unaryArithBinding := Binding{
	// 	Source:  nil,
	// 	Type:    unaryArithType,
	// 	Mutable: false,
	// }

	unaryLogicType := NewFuncType(
		nil,
		[]*FuncParam{
			NewFuncParam(NewIdentPat("a"), NewBoolPrimType(nil)),
		},
		NewBoolPrimType(nil),
		NewNeverType(nil),
		nil,
	)
	unaryLogicBinding := Binding{
		Source:  nil,
		Type:    unaryLogicType,
		Mutable: false,
	}

	scope.Namespace.Values["+"] = &binArithBinding
	scope.Namespace.Values["-"] = &binArithBinding
	scope.Namespace.Values["*"] = &binArithBinding
	scope.Namespace.Values["/"] = &binArithBinding

	scope.Namespace.Values["=="] = &binEqBinding
	scope.Namespace.Values["!="] = &binEqBinding
	scope.Namespace.Values["<"] = &binACompBinding
	scope.Namespace.Values[">"] = &binACompBinding
	scope.Namespace.Values["<="] = &binACompBinding
	scope.Namespace.Values[">="] = &binACompBinding

	scope.Namespace.Values["&&"] = &binLogicBinding
	scope.Namespace.Values["||"] = &binLogicBinding

	// TODO: uncomment after adding support for calling overloaded functions
	// scope.Namespace.Values["-"] = Binding{
	// 	Source:  nil,
	// 	Type:    NewIntersectionType(binArithType, unaryArithType),
	// 	Mutable: false,
	// }

	scope.Namespace.Values["!"] = &unaryLogicBinding

	var objElems []ObjTypeElem

	objElems = append(objElems, &MethodElem{
		Name: NewStrKey("log"),
		Fn: NewFuncType(
			nil,
			[]*FuncParam{
				NewFuncParam(NewIdentPat("msg"), NewStrPrimType(nil)),
			},
			NewUndefinedType(nil),
			NewNeverType(nil),
			nil,
		),
		MutSelf: nil,
	})

	scope.Namespace.Values["console"] = &Binding{
		Source:  nil,
		Type:    NewObjectType(nil, objElems),
		Mutable: false,
	}

	// Promise type with a simple then property to distinguish it from empty object
	promiseTypeParams := []*TypeParam{
		NewTypeParam("T"),
		NewTypeParamWithDefault("E", NewNeverType(nil)),
	}

	promiseElems := []ObjTypeElem{
		&PropertyElem{
			Name:     NewStrKey("then"),
			Value:    NewStrPrimType(nil), // Simplified for now
			Optional: false,
			Readonly: true,
		},
	}

	scope.setTypeAlias("Promise", &TypeAlias{
		Type:       NewObjectType(nil, promiseElems),
		TypeParams: promiseTypeParams,
	})

	// Error type with message property
	errorElems := []ObjTypeElem{
		&PropertyElem{
			Name:     NewStrKey("message"),
			Value:    NewStrPrimType(nil),
			Optional: false,
			Readonly: true,
		},
	}
	scope.setTypeAlias("Error", &TypeAlias{
		Type:       NewObjectType(nil, errorElems),
		TypeParams: []*TypeParam{},
	})

	// Error constructor function
	errorConstructorType := NewFuncType(
		nil,
		[]*FuncParam{
			NewFuncParam(NewIdentPat("message"), NewStrPrimType(nil)),
		},
		NewTypeRefType(nil, "Error", nil),
		NewNeverType(nil),
		nil,
	)
	errorConstructorBinding := Binding{
		Source:  nil,
		Type:    errorConstructorType,
		Mutable: false,
	}
	scope.Namespace.Values["Error"] = &errorConstructorBinding

	length := &PropertyElem{
		Name:     NewStrKey("length"),
		Value:    NewNumPrimType(nil),
		Optional: false,
		Readonly: true,
	}
	arrayType := NewObjectType(nil, []ObjTypeElem{length})
	typeParam := NewTypeParam("T")
	scope.setTypeAlias("Array", &TypeAlias{
		Type:       arrayType,
		TypeParams: []*TypeParam{typeParam},
	})

	// String concatenation operator
	strConcatType := NewFuncType(
		nil,
		[]*FuncParam{
			NewFuncParam(NewIdentPat("a"), NewStrPrimType(nil)),
			NewFuncParam(NewIdentPat("b"), NewStrPrimType(nil)),
		},
		NewStrPrimType(nil),
		NewNeverType(nil),
		nil,
	)
	strConcatBinding := Binding{
		Source:  nil,
		Type:    strConcatType,
		Mutable: false,
	}

	scope.Namespace.Values["++"] = &strConcatBinding

	// Symbol object with iterator and customMatcher unique symbols
	c.SymbolID++
	iteratorSymbol := NewUniqueSymbolType(nil, c.SymbolID)
	c.SymbolID++
	customMatcherSymbol := NewUniqueSymbolType(nil, c.SymbolID)

	symbolElems := []ObjTypeElem{
		&PropertyElem{
			Name:     NewStrKey("iterator"),
			Value:    iteratorSymbol,
			Optional: false,
			Readonly: true,
		},
		&PropertyElem{
			Name:     NewStrKey("customMatcher"),
			Value:    customMatcherSymbol,
			Optional: false,
			Readonly: true,
		},
	}

	scope.Namespace.Values["Symbol"] = &Binding{
		Source:  nil,
		Type:    NewObjectType(nil, symbolElems),
		Mutable: false,
	}

	return scope
}
