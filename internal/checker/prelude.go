package checker

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/moznion/go-optional"
)

func Prelude() *Scope {
	scope := NewScope(optional.None[*Scope]())

	binArithType := &ast.FuncType{
		Params: []*ast.FuncParam{
			ast.NewFuncParam("a", ast.NewNumType()),
			ast.NewFuncParam("b", ast.NewNumType()),
		},
		Return: ast.NewNumType(),
	}

	binCompType := &ast.FuncType{
		Params: []*ast.FuncParam{
			ast.NewFuncParam("a", ast.NewNumType()),
			ast.NewFuncParam("b", ast.NewNumType()),
		},
		Return: ast.NewBoolType(),
	}

	binLogicType := &ast.FuncType{
		Params: []*ast.FuncParam{
			ast.NewFuncParam("a", ast.NewBoolType()),
			ast.NewFuncParam("b", ast.NewBoolType()),
		},
		Return: ast.NewBoolType(),
	}

	unaryArithType := &ast.FuncType{
		Params: []*ast.FuncParam{
			ast.NewFuncParam("a", ast.NewNumType()),
		},
		Return: ast.NewNumType(),
	}

	unaryLogicType := &ast.FuncType{
		Params: []*ast.FuncParam{
			ast.NewFuncParam("a", ast.NewBoolType()),
		},
		Return: ast.NewBoolType(),
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

	scope.Values["-"] = ast.NewIntersectionType(binArithType, unaryArithType)
	scope.Values["!"] = unaryArithType

	scope.Values["!"] = unaryLogicType

	return scope
}
