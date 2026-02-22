package checker

import (
	"strconv"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/provenance"
	"github.com/escalier-lang/escalier/internal/type_system"
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
func (e GenericError) isError()                             {}
func (e InvalidObjectKeyError) isError()                    {}
func (e KeyNotFoundError) isError()                         {}
func (e InterfaceMergeError) isError()                      {}
func (e TypeParamMismatchError) isError()                   {}
func (e OutOfBoundsError) isError()                         {}
func (e RecursiveUnificationError) isError()                {}
func (e NotEnoughElementsToUnpackError) isError()           {}
func (e CannotUnifyTypesError) isError()                    {}
func (e UnknownIdentifierError) isError()                   {}
func (e UnknownOperatorError) isError()                     {}
func (e UnknownTypeError) isError()                         {}
func (e CalleeIsNotCallableError) isError()                 {}
func (e InvalidNumberOfArgumentsError) isError()            {}
func (e NoMatchingOverloadError) isError()                  {}
func (e ExpectedObjectError) isError()                      {}
func (e ExpectedArrayError) isError()                       {}
func (e CyclicDependencyError) isError()                    {}
func (e UnknownPropertyError) isError()                     {}
func (e CannotMutateImmutableError) isError()               {}
func (e CannotMutateReadonlyPropertyError) isError()        {}
func (e IncorrectParamCountForCustomMatcherError) isError() {}
func (e ExtractorReturnTypeMismatchError) isError()         {}
func (e ExtractorMustReturnTupleError) isError()            {}
func (e MissingCustomMatcherError) isError()                {}
func (e InvalidExtractorTypeError) isError()                {}
func (e MissingRequiredPropError) isError()                 {}
func (e UnknownComponentError) isError()                    {}
func (e InvalidKeyPropError) isError()                      {}
func (e UnexpectedChildrenError) isError()                  {}
func (e UnresolvedExportAssignmentError) isError()          {}

type CannotMutateImmutableError struct {
	Type type_system.Type
	span ast.Span
}

func (e CannotMutateImmutableError) Span() ast.Span {
	return e.span
}
func (e CannotMutateImmutableError) Message() string {
	return "Cannot mutate immutable type: " + e.Type.String()
}

type CannotMutateReadonlyPropertyError struct {
	Type     type_system.Type
	Property string
	span     ast.Span
}

func (e CannotMutateReadonlyPropertyError) Span() ast.Span {
	return e.span
}
func (e CannotMutateReadonlyPropertyError) Message() string {
	return "Cannot mutate readonly property '" + e.Property + "' on type: " + e.Type.String()
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

type GenericError struct {
	message string
	span    ast.Span
}

func (e GenericError) Span() ast.Span {
	return e.span
}
func (e GenericError) Message() string {
	return e.message
}

// NewGenericError creates a new generic error with the given message and span
func NewGenericError(message string, span ast.Span) GenericError {
	return GenericError{
		message: message,
		span:    span,
	}
}

type InvalidObjectKeyError struct {
	Key  type_system.Type
	span ast.Span
}

func (e InvalidObjectKeyError) Span() ast.Span {
	return e.span
}
func (e InvalidObjectKeyError) Message() string {
	return "Invalid object key: " + e.Key.String()
}

type KeyNotFoundError struct {
	Object *type_system.ObjectType
	Key    type_system.ObjTypeKey
	span   ast.Span
}

func (e KeyNotFoundError) Span() ast.Span {
	return e.span
}
func (e KeyNotFoundError) Message() string {
	return "Key not found in object: " + e.Key.String() + " in " + e.Object.String()
}

type InterfaceMergeError struct {
	InterfaceName string
	PropertyName  string
	ExistingType  type_system.Type
	NewType       type_system.Type
	span          ast.Span
}

func (e InterfaceMergeError) Span() ast.Span {
	return e.span
}
func (e InterfaceMergeError) Message() string {
	return "Interface '" + e.InterfaceName + "' cannot be merged: property '" + e.PropertyName +
		"' has incompatible types. Existing type: " + e.ExistingType.String() +
		", new type: " + e.NewType.String()
}

type TypeParamMismatchError struct {
	InterfaceName string
	ExistingCount int
	NewCount      int
	message       string
	span          ast.Span
}

func (e TypeParamMismatchError) Span() ast.Span {
	return e.span
}
func (e TypeParamMismatchError) Message() string {
	return e.message
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
	Left  type_system.Type
	Right type_system.Type
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
	T1 type_system.Type
	T2 type_system.Type
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
	t1Str := e.T1.String()
	t2Str := e.T2.String()

	if obj1, ok := e.T1.(*type_system.ObjectType); ok {
		if obj1.Nominal {
			prov1 := e.T1.Provenance()
			if node1, ok := prov1.(*ast.NodeProvenance); ok {
				if decl1, ok := node1.Node.(*ast.ClassDecl); ok {
					t1Str = decl1.Name.Name
				}
			}
		}
	}

	if obj2, ok := e.T2.(*type_system.ObjectType); ok {
		if obj2.Nominal {
			prov2 := e.T2.Provenance()
			if node2, ok := prov2.(*ast.NodeProvenance); ok {
				if decl2, ok := node2.Node.(*ast.ClassDecl); ok {
					t2Str = decl2.Name.Name
				}
			}
		}
	}

	return t1Str + " cannot be assigned to " + t2Str
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
	TypeRef  *type_system.TypeRefType
}

func (e UnknownTypeError) Span() ast.Span {
	node := GetNode(e.TypeRef.Provenance())
	if node != nil {
		return node.Span()
	}
	return ast.Span{}
}
func (e UnknownTypeError) Message() string {
	return "Unknown type: " + e.TypeName
}

type CalleeIsNotCallableError struct {
	Type type_system.Type
	span ast.Span
}

func (e CalleeIsNotCallableError) Span() ast.Span {
	return e.span
}
func (e CalleeIsNotCallableError) Message() string {
	return "Callee is not callable: " + e.Type.String()
}

type InvalidNumberOfArgumentsError struct {
	CallExpr ast.Expr
	Callee   *type_system.FuncType
	Args     []ast.Expr
}

func (e InvalidNumberOfArgumentsError) Span() ast.Span {
	return e.CallExpr.Span()
}
func (e InvalidNumberOfArgumentsError) Message() string {
	return "Invalid number of arguments for function: " + e.Callee.String() +
		". Expected: " + strconv.Itoa(len(e.Callee.Params)) + ", got: " + strconv.Itoa(len(e.Args))
}

type NoMatchingOverloadError struct {
	CallExpr         *ast.CallExpr
	IntersectionType *type_system.IntersectionType
	AttemptedErrors  [][]Error
}

func (e NoMatchingOverloadError) Span() ast.Span {
	return e.CallExpr.Span()
}
func (e NoMatchingOverloadError) Message() string {
	msg := "No overload matches this call:\n"

	// Collect all function types from the intersection
	funcTypes := []*type_system.FuncType{}
	for _, t := range e.IntersectionType.Types {
		if funcType, ok := t.(*type_system.FuncType); ok {
			funcTypes = append(funcTypes, funcType)
		}
	}

	// Show each overload with its errors
	for i, funcType := range funcTypes {
		msg += "  Overload " + strconv.Itoa(i+1) + ": " + funcType.String()
		if i < len(e.AttemptedErrors) && len(e.AttemptedErrors[i]) > 0 {
			msg += "\n    Error: " + e.AttemptedErrors[i][0].Message()
		}
		msg += "\n"
	}

	return msg
}

type ExpectedObjectError struct {
	Type type_system.Type
	span ast.Span
}

func (e ExpectedObjectError) Span() ast.Span {
	return e.span
}
func (e ExpectedObjectError) Message() string {
	return "Expected an object type, but got: " + e.Type.String()
}

type ExpectedArrayError struct {
	Type type_system.Type
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
	ObjectType type_system.Type
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
	Method    *type_system.FuncType
	NumParams int
}

func (e IncorrectParamCountForCustomMatcherError) Span() ast.Span {
	return DEFAULT_SPAN
}
func (e IncorrectParamCountForCustomMatcherError) Message() string {
	return "Custom matcher method must have exactly one parameter, but got " + strconv.Itoa(e.NumParams)
}

type ExtractorReturnTypeMismatchError struct {
	ExtractorType *type_system.ExtractorType
	ReturnType    type_system.Type
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
	ExtractorType *type_system.ExtractorType
	ReturnType    type_system.Type
}

func (e ExtractorMustReturnTupleError) Span() ast.Span {
	return DEFAULT_SPAN
}
func (e ExtractorMustReturnTupleError) Message() string {
	return "Extractor must return a tuple type, but got: " + e.ReturnType.String()
}

type MissingCustomMatcherError struct {
	ObjectType *type_system.ObjectType
}

func (e MissingCustomMatcherError) Span() ast.Span {
	return DEFAULT_SPAN
}
func (e MissingCustomMatcherError) Message() string {
	return "Object type does not have a custom matcher method (Symbol.customMatcher)"
}

type InvalidExtractorTypeError struct {
	ExtractorType *type_system.ExtractorType
	ActualType    type_system.Type
}

func (e InvalidExtractorTypeError) Span() ast.Span {
	return DEFAULT_SPAN
}
func (e InvalidExtractorTypeError) Message() string {
	return "Extractor's extractor must be an object type, but got: " + e.ActualType.String()
}

type MissingRequiredPropError struct {
	PropName   string
	ObjectType type_system.Type
	span       ast.Span
}

func (e MissingRequiredPropError) Span() ast.Span {
	return e.span
}
func (e MissingRequiredPropError) Message() string {
	return "Missing required prop '" + e.PropName + "' for " + e.ObjectType.String()
}

type UnknownComponentError struct {
	Name string
	span ast.Span
}

func (e UnknownComponentError) Span() ast.Span {
	return e.span
}
func (e UnknownComponentError) Message() string {
	return "Component '" + e.Name + "' is not defined"
}

type InvalidKeyPropError struct {
	ActualType type_system.Type
	span       ast.Span
}

func (e InvalidKeyPropError) Span() ast.Span {
	return e.span
}
func (e InvalidKeyPropError) Message() string {
	return "Invalid 'key' prop type: expected string | number | null, got " + e.ActualType.String()
}

type UnexpectedChildrenError struct {
	ComponentName string
	span          ast.Span
}

func (e UnexpectedChildrenError) Span() ast.Span {
	return e.span
}
func (e UnexpectedChildrenError) Message() string {
	return "Component '" + e.ComponentName + "' does not accept children"
}

type UnresolvedExportAssignmentError struct {
	Name string
	span ast.Span
}

func (e UnresolvedExportAssignmentError) Span() ast.Span {
	return e.span
}
func (e UnresolvedExportAssignmentError) Message() string {
	return "Unresolved identifier in export assignment: " + e.Name
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
