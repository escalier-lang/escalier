package checker

import (
	"github.com/moznion/go-optional"

	"github.com/escalier-lang/escalier/internal/ast"
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
		Source:  optional.None[ast.BindingSource](),
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
		Source:  optional.None[ast.BindingSource](),
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
		Source:  optional.None[ast.BindingSource](),
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
	// 	Source:  optional.None[ast.BindingSource](),
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
		Source:  optional.None[ast.BindingSource](),
		Type:    unaryLogicType,
		Mutable: false,
	}

	scope.Values["+"] = &binArithBinding
	scope.Values["-"] = &binArithBinding
	scope.Values["*"] = &binArithBinding
	scope.Values["/"] = &binArithBinding

	scope.Values["=="] = &binACompBinding
	scope.Values["!="] = &binACompBinding
	scope.Values["<"] = &binACompBinding
	scope.Values[">"] = &binACompBinding
	scope.Values["<="] = &binACompBinding
	scope.Values[">="] = &binACompBinding

	scope.Values["&&"] = &binLogicBinding
	scope.Values["||"] = &binLogicBinding

	// TODO: uncomment after adding support for calling overloaded functions
	// scope.Values["-"] = Binding{
	// 	Source:  optional.None[ast.BindingSource](),
	// 	Type:    NewIntersectionType(binArithType, unaryArithType),
	// 	Mutable: false,
	// }

	scope.Values["!"] = &unaryLogicBinding

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

	scope.Values["console"] = &Binding{
		Source:  optional.None[ast.BindingSource](),
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
	scope.setTypeAlias("Array", TypeAlias{
		Type:       arrayType,
		TypeParams: []*TypeParam{typeParam},
	})

	// TODO: ++: fn (a: string, b: string) -> string

	return scope
}
