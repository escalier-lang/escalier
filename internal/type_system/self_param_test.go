package type_system

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// recordingVisitor collects every Type its EnterType is called on. Lets
// us assert that FuncType.Accept walks SelfParam.Type as part of
// normal traversal — required so receiver-attached lifetimes flow
// through substitution and lifetime machinery automatically.
type recordingVisitor struct{ visited []Type }

func (v *recordingVisitor) EnterType(t Type) EnterResult {
	v.visited = append(v.visited, t)
	return EnterResult{}
}
func (v *recordingVisitor) ExitType(t Type) Type { return nil }

// TestFuncTypeAcceptVisitsSelfParam pins that FuncType.Accept traverses
// SelfParam.Type so any visitor (substitution, lifetime walks, etc.)
// sees the receiver's inner types. Without this, receiver-attached
// lifetimes would be invisible to the lifetime substitution pass.
func TestFuncTypeAcceptVisitsSelfParam(t *testing.T) {
	receiverInner := NewTypeRefType(nil, "Receiver", nil)
	paramInner := NewTypeRefType(nil, "Param", nil)
	returnType := NewTypeRefType(nil, "Ret", nil)

	fn := NewFuncType(
		nil, nil,
		[]*FuncParam{{Pattern: NewIdentPat("p"), Type: paramInner}},
		returnType,
		NewNeverType(nil),
	)
	fn.SelfParam = &FuncParam{
		Pattern: NewIdentPat("self"),
		Type:    receiverInner,
	}

	v := &recordingVisitor{}
	fn.Accept(v)

	assert.Contains(t, v.visited, Type(receiverInner),
		"FuncType.Accept must visit SelfParam.Type")
	assert.Contains(t, v.visited, Type(paramInner),
		"FuncType.Accept must visit Params[].Type (regression check)")
	assert.Contains(t, v.visited, Type(returnType),
		"FuncType.Accept must visit Return (regression check)")
}
