package checker

import (
	"github.com/moznion/go-optional"

	. "github.com/escalier-lang/escalier/internal/type_system"
)

func Prelude() *Scope {
	scope := NewScope(optional.None[*Scope]())

	binArithType := &FuncType{
		Params: []*FuncParam{
			NewFuncParam("a", NewNumType()),
			NewFuncParam("b", NewNumType()),
		},
		Return: NewNumType(),
	}

	binCompType := &FuncType{
		Params: []*FuncParam{
			NewFuncParam("a", NewNumType()),
			NewFuncParam("b", NewNumType()),
		},
		Return: NewBoolType(),
	}

	binLogicType := &FuncType{
		Params: []*FuncParam{
			NewFuncParam("a", NewBoolType()),
			NewFuncParam("b", NewBoolType()),
		},
		Return: NewBoolType(),
	}

	unaryArithType := &FuncType{
		Params: []*FuncParam{
			NewFuncParam("a", NewNumType()),
		},
		Return: NewNumType(),
	}

	unaryLogicType := &FuncType{
		Params: []*FuncParam{
			NewFuncParam("a", NewBoolType()),
		},
		Return: NewBoolType(),
	}

	scope.Values["+"] = binArithType
	scope.Values["-"] = binArithType
	scope.Values["*"] = binArithType
	scope.Values["/"] = binArithType

	scope.Values["=="] = binCompType
	scope.Values["!="] = binCompType
	scope.Values["<"] = binCompType
	scope.Values[">"] = binCompType
	scope.Values["<="] = binCompType
	scope.Values[">="] = binCompType

	scope.Values["&&"] = binLogicType
	scope.Values["||"] = binLogicType

	scope.Values["-"] = NewIntersectionType(binArithType, unaryArithType)
	scope.Values["!"] = unaryArithType

	scope.Values["!"] = unaryLogicType

	return scope
}
