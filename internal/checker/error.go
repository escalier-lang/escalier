package checker

import (
	"strconv"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/provenance"
	"github.com/escalier-lang/escalier/internal/type_system"
	. "github.com/escalier-lang/escalier/internal/type_system"
)

var DEFAULT_SPAN = ast.Span{
	Start:    ast.Location{Line: 1, Column: 1},
	End:      ast.Location{Line: 1, Column: 1},
	SourceID: -1,
}

type Error interface {
	isError()
	Span() ast.Span
	Message() string
}

func (e UnimplementedError) isError()                       {}
func (e InvalidObjectKeyError) isError()                    {}
func (e KeyNotFoundError) isError()                         {}
func (e OutOfBoundsError) isError()                         {}
func (e RecursiveUnificationError) isError()                {}
func (e NotEnoughElementsToUnpackError) isError()           {}
func (e CannotUnifyTypesError) isError()                    {}
func (e UnknownIdentifierError) isError()                   {}
func (e UnknownOperatorError) isError()                     {}
func (e UnknownTypeError) isError()                         {}
func (e CalleeIsNotCallableError) isError()                 {}
func (e InvalidNumberOfArgumentsError) isError()            {}
func (e ExpectedObjectError) isError()                      {}
func (e ExpectedArrayError) isError()                       {}
func (e CyclicDependencyError) isError()                    {}
func (e UnknownPropertyError) isError()                     {}
func (e CannotMutateImmutableError) isError()               {}
func (e IncorrectParamCountForCustomMatcherError) isError() {}
func (e ExtractorReturnTypeMismatchError) isError()         {}
func (e ExtractorMustReturnTupleError) isError()            {}
func (e MissingCustomMatcherError) isError()                {}
func (e InvalidExtractorTypeError) isError()                {}

type CannotMutateImmutableError struct {
	Type Type
	span ast.Span
}

func (e CannotMutateImmutableError) Span() ast.Span {
	return e.span
}
func (e CannotMutateImmutableError) Message() string {
	return "Cannot mutate immutable type: " + e.Type.String()
}

type UnimplementedError struct {
	message string
	span    ast.Span
}

func (e UnimplementedError) Span() ast.Span {
	return e.span
}
func (e UnimplementedError) Message() string {
	return "Unimplemented: " + e.message
}

type InvalidObjectKeyError struct {
	Key  Type
	span ast.Span
}

func (e InvalidObjectKeyError) Span() ast.Span {
	return e.span
}
func (e InvalidObjectKeyError) Message() string {
	return "Invalid object key: " + e.Key.String()
}

type KeyNotFoundError struct {
	Object *ObjectType
	Key    ObjTypeKey
}

func (e KeyNotFoundError) Span() ast.Span {
	return DEFAULT_SPAN
}
func (e KeyNotFoundError) Message() string {
	return "Key not found in object: " + e.Key.String() + " in " + e.Object.String()
}

type OutOfBoundsError struct {
	Index  int
	Length int
	span   ast.Span
}

func (e OutOfBoundsError) Span() ast.Span {
	return e.span
}
func (e OutOfBoundsError) Message() string {
	return "Index out of bounds: " + strconv.Itoa(e.Index) + " for length " + strconv.Itoa(e.Length)
}

type RecursiveUnificationError struct {
	Left  Type
	Right Type
}

func (e RecursiveUnificationError) Span() ast.Span {
	return DEFAULT_SPAN
}
func (e RecursiveUnificationError) Message() string {
	return "Recursive unification error: cannot unify " + e.Left.String() + " with " + e.Right.String()
}

type NotEnoughElementsToUnpackError struct {
	span ast.Span
}

func (e NotEnoughElementsToUnpackError) Span() ast.Span {
	return e.span
}
func (e NotEnoughElementsToUnpackError) Message() string {
	return "Not enough elements to unpack"
}

type CannotUnifyTypesError struct {
	T1 Type
	T2 Type
}

func (e CannotUnifyTypesError) Span() ast.Span {
	// We're assigning T1 to T2 so I think we want to
	// report the primary location of the error as the t1Node.
	t1Prov := e.T1.Provenance()
	t1Node := GetNode(t1Prov)
	// TODO: ensure every node has a provenance
	if t1Node == nil {
		// This code is triggered by the NewTypeRefType("Error", nil) in
		// prelude.go, which does not have a provenance set.
		return DEFAULT_SPAN
	}
	return t1Node.Span()
}
func (e CannotUnifyTypesError) Message() string {
	return e.T1.String() + " cannot be assigned to " + e.T2.String()
}

type UnknownIdentifierError struct {
	Ident *ast.IdentExpr
	span  ast.Span
}

func (e UnknownIdentifierError) Span() ast.Span {
	return e.span
}
func (e UnknownIdentifierError) Message() string {
	return "Unknown identifier: " + e.Ident.Name
}

type UnknownOperatorError struct {
	Operator string
}

func (e UnknownOperatorError) Span() ast.Span {
	return DEFAULT_SPAN
}
func (e UnknownOperatorError) Message() string {
	return "Unknown operator: " + e.Operator
}

type UnknownTypeError struct {
	TypeName string
	typeRef  *TypeRefType
}

func (e UnknownTypeError) Span() ast.Span {
	node := GetNode(e.typeRef.Provenance())
	return node.Span()
}
func (e UnknownTypeError) Message() string {
	return "Unknown type: " + e.TypeName
}

type CalleeIsNotCallableError struct {
	Type Type
	span ast.Span
}

func (e CalleeIsNotCallableError) Span() ast.Span {
	return e.span
}
func (e CalleeIsNotCallableError) Message() string {
	return "Callee is not callable: " + e.Type.String()
}

type InvalidNumberOfArgumentsError struct {
	Callee *FuncType
	Args   []ast.Expr
}

func (e InvalidNumberOfArgumentsError) Span() ast.Span {
	return DEFAULT_SPAN
}
func (e InvalidNumberOfArgumentsError) Message() string {
	return "Invalid number of arguments for function: " + e.Callee.String() +
		". Expected: " + strconv.Itoa(len(e.Callee.Params)) + ", got: " + strconv.Itoa(len(e.Args))
}

type ExpectedObjectError struct {
	Type Type
}

func (e ExpectedObjectError) Span() ast.Span {
	return DEFAULT_SPAN
}
func (e ExpectedObjectError) Message() string {
	return "Expected an object type, but got: " + e.Type.String()
}

type ExpectedArrayError struct {
	Type Type
}

func (e ExpectedArrayError) Span() ast.Span {
	return DEFAULT_SPAN
}
func (e ExpectedArrayError) Message() string {
	return "Expected an array type, but got: " + e.Type.String()
}

type CyclicDependencyError struct{}

func (e CyclicDependencyError) Span() ast.Span {
	return DEFAULT_SPAN
}
func (e CyclicDependencyError) Message() string {
	return "Cyclic dependency detected"
}

type UnknownPropertyError struct {
	ObjectType Type
	Property   string
	span       ast.Span
}

func (e UnknownPropertyError) Span() ast.Span {
	return e.span
}
func (e UnknownPropertyError) Message() string {
	return "Unknown property '" + e.Property + "' in object type " + e.ObjectType.String()
}

type IncorrectParamCountForCustomMatcherError struct {
	Method    *FuncType
	NumParams int
}

func (e IncorrectParamCountForCustomMatcherError) Span() ast.Span {
	return DEFAULT_SPAN
}
func (e IncorrectParamCountForCustomMatcherError) Message() string {
	return "Custom matcher method must have exactly one parameter, but got " + strconv.Itoa(e.NumParams)
}

type ExtractorReturnTypeMismatchError struct {
	ExtractorType *ExtractorType
	ReturnType    Type
	NumArgs       int
	NumReturns    int
}

func (e ExtractorReturnTypeMismatchError) Span() ast.Span {
	return DEFAULT_SPAN
}
func (e ExtractorReturnTypeMismatchError) Message() string {
	return "Extractor return type mismatch: expected " + strconv.Itoa(e.NumArgs) + " return values, but got " + strconv.Itoa(e.NumReturns)
}

type ExtractorMustReturnTupleError struct {
	ExtractorType *ExtractorType
	ReturnType    Type
}

func (e ExtractorMustReturnTupleError) Span() ast.Span {
	return DEFAULT_SPAN
}
func (e ExtractorMustReturnTupleError) Message() string {
	return "Extractor must return a tuple type, but got: " + e.ReturnType.String()
}

type MissingCustomMatcherError struct {
	ObjectType *ObjectType
}

func (e MissingCustomMatcherError) Span() ast.Span {
	return DEFAULT_SPAN
}
func (e MissingCustomMatcherError) Message() string {
	return "Object type does not have a custom matcher method (Symbol.customMatcher)"
}

type InvalidExtractorTypeError struct {
	ExtractorType *ExtractorType
	ActualType    Type
}

func (e InvalidExtractorTypeError) Span() ast.Span {
	return DEFAULT_SPAN
}
func (e InvalidExtractorTypeError) Message() string {
	return "Extractor's extractor must be an object type, but got: " + e.ActualType.String()
}

// TODO: make this a sum type so that different error type can reference other
// types if necessary
// type Error struct {
// 	Message  string
// 	Location ast.Span
// }

func GetNode(p provenance.Provenance) ast.Node {
	if p == nil {
		return nil
	}
	switch prov := p.(type) {
	case *ast.NodeProvenance:
		return prov.Node
	case *type_system.TypeProvenance:
		if prov.Type == nil {
			return nil
		}
		t := prov.Type
		if t == nil {
			return nil
		}
		return GetNode(t.Provenance())
	default:
		return nil
	}
}
