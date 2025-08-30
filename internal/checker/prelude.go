package checker

import (
	. "github.com/escalier-lang/escalier/internal/type_system"
)

func Prelude() *Scope {
	scope := NewScope()

	binArithType := &FuncType{
		TypeParams: nil,
		Params: []*FuncParam{
			NewFuncParam(NewIdentPat("a"), NewNumType()),
			NewFuncParam(NewIdentPat("b"), NewNumType()),
		},
		Return: NewNumType(),
		Throws: NewNeverType(),
	}
	binArithBinding := Binding{
		Source:  nil,
		Type:    binArithType,
		Mutable: false,
	}

	binCompType := &FuncType{
		TypeParams: nil,
		Params: []*FuncParam{
			NewFuncParam(NewIdentPat("a"), NewNumType()),
			NewFuncParam(NewIdentPat("b"), NewNumType()),
		},
		Return: NewBoolType(),
		Throws: NewNeverType(),
	}
	binACompBinding := Binding{
		Source:  nil,
		Type:    binCompType,
		Mutable: false,
	}

	binEqType := &FuncType{
		TypeParams: nil,
		Params: []*FuncParam{
			NewFuncParam(NewIdentPat("a"), NewAnyType()),
			NewFuncParam(NewIdentPat("b"), NewAnyType()),
		},
		Return: NewBoolType(),
		Throws: NewNeverType(),
	}
	binEqBinding := Binding{
		Source:  nil,
		Type:    binEqType,
		Mutable: false,
	}

	binLogicType := &FuncType{
		TypeParams: nil,
		Params: []*FuncParam{
			NewFuncParam(NewIdentPat("a"), NewBoolType()),
			NewFuncParam(NewIdentPat("b"), NewBoolType()),
		},
		Return: NewBoolType(),
		Throws: NewNeverType(),
	}
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

	unaryLogicType := &FuncType{
		TypeParams: nil,
		Params: []*FuncParam{
			NewFuncParam(NewIdentPat("a"), NewBoolType()),
		},
		Return: NewBoolType(),
		Throws: NewNeverType(),
	}
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

	objElems = append(objElems, &MethodElemType{
		Name: NewStrKey("log"),
		Fn: &FuncType{
			TypeParams: nil,
			Params: []*FuncParam{
				NewFuncParam(NewIdentPat("msg"), NewStrType()),
			},
			Return: NewLitType(&UndefinedLit{}),
			Throws: NewNeverType(),
		},
		MutSelf: nil,
	})

	scope.Namespace.Values["console"] = &Binding{
		Source:  nil,
		Type:    NewObjectType(objElems),
		Mutable: false,
	}

	// Promise type with a simple then property to distinguish it from empty object
	promiseTypeParams := []*TypeParam{
		NewTypeParam("T"),
		NewTypeParamWithDefault("E", NewNeverType()),
	}

	promiseElems := []ObjTypeElem{
		&PropertyElemType{
			Name:     NewStrKey("then"),
			Value:    NewStrType(), // Simplified for now
			Optional: false,
			Readonly: true,
		},
	}

	scope.setTypeAlias("Promise", &TypeAlias{
		Type:       NewObjectType(promiseElems),
		TypeParams: promiseTypeParams,
	})

	// Error type with message property
	errorElems := []ObjTypeElem{
		&PropertyElemType{
			Name:     NewStrKey("message"),
			Value:    NewStrType(),
			Optional: false,
			Readonly: true,
		},
	}
	scope.setTypeAlias("Error", &TypeAlias{
		Type:       NewObjectType(errorElems),
		TypeParams: []*TypeParam{},
	})

	// Error constructor function
	errorConstructorType := &FuncType{
		Params: []*FuncParam{
			NewFuncParam(NewIdentPat("message"), NewStrType()),
		},
		Return:     NewTypeRefType("Error", nil),
		Throws:     NewNeverType(),
		TypeParams: []*TypeParam{},
	}
	errorConstructorBinding := Binding{
		Source:  nil,
		Type:    errorConstructorType,
		Mutable: false,
	}
	scope.Namespace.Values["Error"] = &errorConstructorBinding

	length := &PropertyElemType{
		Name:     NewStrKey("length"),
		Value:    NewNumType(),
		Optional: false,
		Readonly: true,
	}
	arrayType := NewObjectType([]ObjTypeElem{length})
	typeParam := NewTypeParam("T")
	scope.setTypeAlias("Array", &TypeAlias{
		Type:       arrayType,
		TypeParams: []*TypeParam{typeParam},
	})

	// String concatenation operator
	strConcatType := &FuncType{
		TypeParams: nil,
		Params: []*FuncParam{
			NewFuncParam(NewIdentPat("a"), NewStrType()),
			NewFuncParam(NewIdentPat("b"), NewStrType()),
		},
		Return: NewStrType(),
		Throws: NewNeverType(),
	}
	strConcatBinding := Binding{
		Source:  nil,
		Type:    strConcatType,
		Mutable: false,
	}

	scope.Namespace.Values["++"] = &strConcatBinding

	return scope
}
