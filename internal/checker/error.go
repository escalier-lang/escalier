package checker

import (
	"strconv"

	"github.com/escalier-lang/escalier/internal/ast"
	. "github.com/escalier-lang/escalier/internal/type_system"
)

type Error interface {
	isError()
	Location() ast.Location
	Message() string
}

func (e UnimplementedError) isError()             {}
func (e InvalidObjectKeyError) isError()          {}
func (e KeyNotFoundError) isError()               {}
func (e OutOfBoundsError) isError()               {}
func (e RecursiveUnificationError) isError()      {}
func (e NotEnoughElementsToUnpackError) isError() {}
func (e CannotUnifyTypesError) isError()          {}
func (e UnknownIdentifierError) isError()         {}
func (e UnknownOperatorError) isError()           {}
func (e UnkonwnTypeError) isError()               {}
func (e CalleeIsNotCallableError) isError()       {}
func (e InvalidNumberOfArgumentsError) isError()  {}
func (e ExpectedObjectError) isError()            {}
func (e ExpectedArrayError) isError()             {}

type UnimplementedError struct {
	message string
}

func (e UnimplementedError) Location() ast.Location {
	return ast.Location{}
}
func (e UnimplementedError) Message() string {
	return "Unimplemented: " + e.message
}

type InvalidObjectKeyError struct {
	Key Type
}

func (e InvalidObjectKeyError) Location() ast.Location {
	return ast.Location{}
}
func (e InvalidObjectKeyError) Message() string {
	return "Invalid object key: " + e.Key.String()
}

type KeyNotFoundError struct {
	Object *ObjectType
	Key    ObjTypeKey
}

func (e KeyNotFoundError) Location() ast.Location {
	return ast.Location{}
}
func (e KeyNotFoundError) Message() string {
	return "Key not found in object: " + e.Key.String() + " in " + e.Object.String()
}

type OutOfBoundsError struct {
	Index  int
	Length int
}

func (e OutOfBoundsError) Location() ast.Location {
	return ast.Location{}
}
func (e OutOfBoundsError) Message() string {
	return "Index out of bounds: " + strconv.Itoa(e.Index) + " for length " + strconv.Itoa(e.Length)
}

type RecursiveUnificationError struct {
	Left  Type
	Right Type
}

func (e RecursiveUnificationError) Location() ast.Location {
	return ast.Location{}
}
func (e RecursiveUnificationError) Message() string {
	return "Recursive unification error: cannot unify " + e.Left.String() + " with " + e.Right.String()
}

type NotEnoughElementsToUnpackError struct{}

func (e NotEnoughElementsToUnpackError) Location() ast.Location {
	return ast.Location{}
}
func (e NotEnoughElementsToUnpackError) Message() string {
	return "Not enough elements to unpack"
}

type CannotUnifyTypesError struct {
	Left  Type
	Right Type
}

func (e CannotUnifyTypesError) Location() ast.Location {
	return ast.Location{}
}
func (e CannotUnifyTypesError) Message() string {
	return "Cannot unify types: " + e.Left.String() + " with " + e.Right.String()
}

type UnknownIdentifierError struct {
	Ident *ast.IdentExpr
}

func (e UnknownIdentifierError) Location() ast.Location {
	return ast.Location{}
}
func (e UnknownIdentifierError) Message() string {
	return "Unknown identifier: " + e.Ident.Name
}

type UnknownOperatorError struct {
	Operator string
}

func (e UnknownOperatorError) Location() ast.Location {
	return ast.Location{}
}
func (e UnknownOperatorError) Message() string {
	return "Unknown operator: " + e.Operator
}

type UnkonwnTypeError struct {
	TypeName string
}

func (e UnkonwnTypeError) Location() ast.Location {
	return ast.Location{}
}
func (e UnkonwnTypeError) Message() string {
	return "Unknown type: " + e.TypeName
}

type CalleeIsNotCallableError struct {
	Callee Type
}

func (e CalleeIsNotCallableError) Location() ast.Location {
	return ast.Location{}
}
func (e CalleeIsNotCallableError) Message() string {
	return "Callee is not callable: " + e.Callee.String()
}

type InvalidNumberOfArgumentsError struct {
	Callee *FuncType
	Args   []ast.Expr
}

func (e InvalidNumberOfArgumentsError) Location() ast.Location {
	return ast.Location{}
}
func (e InvalidNumberOfArgumentsError) Message() string {
	return "Invalid number of arguments for function: " + e.Callee.String() +
		". Expected: " + strconv.Itoa(len(e.Callee.Params)) + ", got: " + strconv.Itoa(len(e.Args))
}

type ExpectedObjectError struct {
	Type Type
}

func (e ExpectedObjectError) Location() ast.Location {
	return ast.Location{}
}
func (e ExpectedObjectError) Message() string {
	return "Expected an object type, but got: " + e.Type.String()
}

type ExpectedArrayError struct {
	Type Type
}

func (e ExpectedArrayError) Location() ast.Location {
	return ast.Location{}
}
func (e ExpectedArrayError) Message() string {
	return "Expected an array type, but got: " + e.Type.String()
}

// TODO: make this a sum type so that different error type can reference other
// types if necessary
// type Error struct {
// 	Message  string
// 	Location ast.Location
// }
