package checker

import (
	. "github.com/escalier-lang/escalier/internal/type_system"
)

func Prelude() *Scope {
	scope := NewScope(nil)

	binArithType := &FuncType{
		Params: []*FuncParam{
			NewFuncParam(NewIdentPat("a"), NewNumType()),
			NewFuncParam(NewIdentPat("b"), NewNumType()),
		},
		Return: NewNumType(),
	}
	binArithBinding := Binding{
		Source:  nil,
		Type:    binArithType,
		Mutable: false,
	}

	binCompType := &FuncType{
		Params: []*FuncParam{
			NewFuncParam(NewIdentPat("a"), NewNumType()),
			NewFuncParam(NewIdentPat("b"), NewNumType()),
		},
		Return: NewBoolType(),
	}
	binACompBinding := Binding{
		Source:  nil,
		Type:    binCompType,
		Mutable: false,
	}

	binLogicType := &FuncType{
		Params: []*FuncParam{
			NewFuncParam(NewIdentPat("a"), NewBoolType()),
			NewFuncParam(NewIdentPat("b"), NewBoolType()),
		},
		Return: NewBoolType(),
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
		Params: []*FuncParam{
			NewFuncParam(NewIdentPat("a"), NewBoolType()),
		},
		Return: NewBoolType(),
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

	scope.Namespace.Values["=="] = &binACompBinding
	scope.Namespace.Values["!="] = &binACompBinding
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
			Params: []*FuncParam{
				NewFuncParam(NewIdentPat("msg"), NewStrType()),
			},
			Return: NewLitType(&UndefinedLit{}),
		},
	})

	scope.Namespace.Values["console"] = &Binding{
		Source:  nil,
		Type:    NewObjectType(objElems),
		Mutable: false,
	}

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

	// TODO: ++: fn (a: string, b: string) -> string

	return scope
}
