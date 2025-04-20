package checker

import (
	"github.com/moznion/go-optional"

	"github.com/escalier-lang/escalier/internal/ast"
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
	binArithBinding := Binding{
		Source:  optional.None[ast.BindingSource](),
		Type:    binArithType,
		Mutable: false,
	}

	binCompType := &FuncType{
		Params: []*FuncParam{
			NewFuncParam("a", NewNumType()),
			NewFuncParam("b", NewNumType()),
		},
		Return: NewBoolType(),
	}
	binACompBinding := Binding{
		Source:  optional.None[ast.BindingSource](),
		Type:    binCompType,
		Mutable: false,
	}

	binLogicType := &FuncType{
		Params: []*FuncParam{
			NewFuncParam("a", NewBoolType()),
			NewFuncParam("b", NewBoolType()),
		},
		Return: NewBoolType(),
	}
	binLogicBinding := Binding{
		Source:  optional.None[ast.BindingSource](),
		Type:    binLogicType,
		Mutable: false,
	}

	unaryArithType := &FuncType{
		Params: []*FuncParam{
			NewFuncParam("a", NewNumType()),
		},
		Return: NewNumType(),
	}
	unaryArithBinding := Binding{
		Source:  optional.None[ast.BindingSource](),
		Type:    unaryArithType,
		Mutable: false,
	}

	unaryLogicType := &FuncType{
		Params: []*FuncParam{
			NewFuncParam("a", NewBoolType()),
		},
		Return: NewBoolType(),
	}
	unaryLogicBinding := Binding{
		Source:  optional.None[ast.BindingSource](),
		Type:    unaryLogicType,
		Mutable: false,
	}

	scope.Values["+"] = binArithBinding
	scope.Values["-"] = binArithBinding
	scope.Values["*"] = binArithBinding
	scope.Values["/"] = binArithBinding

	scope.Values["=="] = binACompBinding
	scope.Values["!="] = binACompBinding
	scope.Values["<"] = binACompBinding
	scope.Values[">"] = binACompBinding
	scope.Values["<="] = binACompBinding
	scope.Values[">="] = binACompBinding

	scope.Values["&&"] = binLogicBinding
	scope.Values["||"] = binLogicBinding

	scope.Values["-"] = Binding{
		Source:  optional.None[ast.BindingSource](),
		Type:    NewIntersectionType(binArithType, unaryArithType),
		Mutable: false,
	}
	scope.Values["!"] = unaryArithBinding

	scope.Values["!"] = unaryLogicBinding

	return scope
}
