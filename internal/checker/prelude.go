package checker

import (
	"github.com/escalier-lang/escalier/internal/type_system"
)

func Prelude(c *Checker) *Scope {
	scope := NewScope()

	binArithType := type_system.NewFuncType(
		nil,
		nil,
		[]*type_system.FuncParam{
			type_system.NewFuncParam(type_system.NewIdentPat("a"), type_system.NewNumPrimType(nil)),
			type_system.NewFuncParam(type_system.NewIdentPat("b"), type_system.NewNumPrimType(nil)),
		},
		type_system.NewNumPrimType(nil),
		type_system.NewNeverType(nil),
	)
	binArithBinding := type_system.Binding{
		Source:  nil,
		Type:    binArithType,
		Mutable: false,
	}

	binCompType := type_system.NewFuncType(
		nil,
		nil,
		[]*type_system.FuncParam{
			type_system.NewFuncParam(type_system.NewIdentPat("a"), type_system.NewNumPrimType(nil)),
			type_system.NewFuncParam(type_system.NewIdentPat("b"), type_system.NewNumPrimType(nil)),
		},
		type_system.NewBoolPrimType(nil),
		type_system.NewNeverType(nil),
	)
	binACompBinding := type_system.Binding{
		Source:  nil,
		Type:    binCompType,
		Mutable: false,
	}

	binEqType := type_system.NewFuncType(
		nil,
		nil,
		[]*type_system.FuncParam{
			type_system.NewFuncParam(type_system.NewIdentPat("a"), type_system.NewAnyType(nil)),
			type_system.NewFuncParam(type_system.NewIdentPat("b"), type_system.NewAnyType(nil)),
		},
		type_system.NewBoolPrimType(nil),
		type_system.NewNeverType(nil),
	)
	binEqBinding := type_system.Binding{
		Source:  nil,
		Type:    binEqType,
		Mutable: false,
	}

	binLogicType := type_system.NewFuncType(
		nil,
		nil,
		[]*type_system.FuncParam{
			type_system.NewFuncParam(type_system.NewIdentPat("a"), type_system.NewBoolPrimType(nil)),
			type_system.NewFuncParam(type_system.NewIdentPat("b"), type_system.NewBoolPrimType(nil)),
		},
		type_system.NewBoolPrimType(nil),
		type_system.NewNeverType(nil),
	)
	binLogicBinding := type_system.Binding{
		Source:  nil,
		Type:    binLogicType,
		Mutable: false,
	}

	// unaryArithType := &FuncType{
	// 	Params: []*type_system.FuncParam{
	// 		type_system.NewFuncParam(type_system.NewIdentPat("a"), NewNumType()),
	// 	},
	// 	Return: NewNumType(),
	// }
	// unaryArithBinding := type_system.Binding{
	// 	Source:  nil,
	// 	Type:    unaryArithType,
	// 	Mutable: false,
	// }

	unaryLogicType := type_system.NewFuncType(
		nil,
		nil,
		[]*type_system.FuncParam{
			type_system.NewFuncParam(type_system.NewIdentPat("a"), type_system.NewBoolPrimType(nil)),
		},
		type_system.NewBoolPrimType(nil),
		type_system.NewNeverType(nil),
	)
	unaryLogicBinding := type_system.Binding{
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
	// scope.Namespace.Values["-"] = type_system.Binding{
	// 	Source:  nil,
	// 	Type:    NewIntersectionType(binArithType, unaryArithType),
	// 	Mutable: false,
	// }

	scope.Namespace.Values["!"] = &unaryLogicBinding

	var objElems []type_system.ObjTypeElem

	objElems = append(objElems, &type_system.MethodElem{
		Name: type_system.NewStrKey("log"),
		Fn: type_system.NewFuncType(
			nil,
			nil,
			[]*type_system.FuncParam{
				type_system.NewFuncParam(type_system.NewIdentPat("msg"), type_system.NewStrPrimType(nil)),
			},
			type_system.NewUndefinedType(nil),
			type_system.NewNeverType(nil),
		),
		MutSelf: nil,
	})

	scope.Namespace.Values["console"] = &type_system.Binding{
		Source:  nil,
		Type:    type_system.NewObjectType(nil, objElems),
		Mutable: false,
	}

	// Promise type with a simple then property to distinguish it from empty object
	promiseTypeParams := []*type_system.TypeParam{
		type_system.NewTypeParam("T"),
		type_system.NewTypeParamWithDefault("E", type_system.NewNeverType(nil)),
	}

	promiseElems := []type_system.ObjTypeElem{
		&type_system.PropertyElem{
			Name:     type_system.NewStrKey("then"),
			Value:    type_system.NewStrPrimType(nil), // Simplified for now
			Optional: false,
			Readonly: true,
		},
	}

	scope.SetTypeAlias("Promise", &type_system.TypeAlias{
		Type:       type_system.NewNominalObjectType(nil, promiseElems),
		TypeParams: promiseTypeParams,
	})

	// Error type with message property
	errorElems := []type_system.ObjTypeElem{
		&type_system.PropertyElem{
			Name:     type_system.NewStrKey("message"),
			Value:    type_system.NewStrPrimType(nil),
			Optional: false,
			Readonly: true,
		},
	}
	scope.SetTypeAlias("Error", &type_system.TypeAlias{
		Type:       type_system.NewNominalObjectType(nil, errorElems),
		TypeParams: []*type_system.TypeParam{},
	})

	// Error constructor function
	errorConstructorType := type_system.NewFuncType(
		nil,
		nil,
		[]*type_system.FuncParam{
			type_system.NewFuncParam(type_system.NewIdentPat("message"), type_system.NewStrPrimType(nil)),
		},
		type_system.NewTypeRefType(nil, "Error", nil),
		type_system.NewNeverType(nil),
	)
	errorConstructorBinding := type_system.Binding{
		Source:  nil,
		Type:    errorConstructorType,
		Mutable: false,
	}
	scope.Namespace.Values["Error"] = &errorConstructorBinding

	length := &type_system.PropertyElem{
		Name:     type_system.NewStrKey("length"),
		Value:    type_system.NewNumPrimType(nil),
		Optional: false,
		Readonly: true,
	}
	arrayType := type_system.NewNominalObjectType(nil, []type_system.ObjTypeElem{length})
	typeParam := type_system.NewTypeParam("T")
	scope.SetTypeAlias("Array", &type_system.TypeAlias{
		Type:       arrayType,
		TypeParams: []*type_system.TypeParam{typeParam},
	})

	// String concatenation operator
	strConcatType := type_system.NewFuncType(
		nil,
		nil,
		[]*type_system.FuncParam{
			type_system.NewFuncParam(type_system.NewIdentPat("a"), type_system.NewStrPrimType(nil)),
			type_system.NewFuncParam(type_system.NewIdentPat("b"), type_system.NewStrPrimType(nil)),
		},
		type_system.NewStrPrimType(nil),
		type_system.NewNeverType(nil),
	)
	strConcatBinding := type_system.Binding{
		Source:  nil,
		Type:    strConcatType,
		Mutable: false,
	}

	scope.Namespace.Values["++"] = &strConcatBinding

	// Symbol object with iterator and customMatcher unique symbols
	c.SymbolID++
	iteratorSymbol := type_system.NewUniqueSymbolType(nil, c.SymbolID)
	c.SymbolID++
	customMatcherSymbol := type_system.NewUniqueSymbolType(nil, c.SymbolID)

	symbolElems := []type_system.ObjTypeElem{
		&type_system.PropertyElem{
			Name:     type_system.NewStrKey("iterator"),
			Value:    iteratorSymbol,
			Optional: false,
			Readonly: true,
		},
		&type_system.PropertyElem{
			Name:     type_system.NewStrKey("customMatcher"),
			Value:    customMatcherSymbol,
			Optional: false,
			Readonly: true,
		},
	}

	scope.Namespace.Values["Symbol"] = &type_system.Binding{
		Source:  nil,
		Type:    type_system.NewObjectType(nil, symbolElems),
		Mutable: false,
	}

	return scope
}
