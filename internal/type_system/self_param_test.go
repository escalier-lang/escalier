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

// TestFuncTypeEqualsConsidersSelfParam pins that FuncType.Equals compares
// SelfParam. Without this, a method's `(self) -> T` and `(mut self) -> T`
// FuncTypes are structurally equal — which would let normalization or
// any equality-keyed cache silently merge them, dropping receiver-mutability
// information from method-call typing.
func TestFuncTypeEqualsConsidersSelfParam(t *testing.T) {
	mkFn := func() *FuncType {
		return NewFuncType(nil, nil, nil, NewNeverType(nil), NewNeverType(nil))
	}
	receiver := NewTypeRefType(nil, "Receiver", nil)

	bare := mkFn()

	withImmutSelf := mkFn()
	withImmutSelf.SelfParam = &FuncParam{
		Pattern: NewIdentPat("self"),
		Type:    receiver,
	}

	withMutSelf := mkFn()
	withMutSelf.SelfParam = &FuncParam{
		Pattern: NewIdentPat("self"),
		Type:    NewMutType(nil, receiver),
	}

	otherReceiver := NewTypeRefType(nil, "Other", nil)
	withOtherSelf := mkFn()
	withOtherSelf.SelfParam = &FuncParam{
		Pattern: NewIdentPat("self"),
		Type:    otherReceiver,
	}

	assert.False(t, bare.Equals(withImmutSelf),
		"bare FuncType must not equal one with a SelfParam")
	assert.False(t, withImmutSelf.Equals(bare),
		"FuncType with SelfParam must not equal a bare one (symmetry)")
	assert.False(t, withImmutSelf.Equals(withMutSelf),
		"`self` and `mut self` FuncTypes must not be equal")
	assert.False(t, withImmutSelf.Equals(withOtherSelf),
		"FuncTypes with different SelfParam types must not be equal")

	withImmutSelf2 := mkFn()
	withImmutSelf2.SelfParam = &FuncParam{
		Pattern: NewIdentPat("self"),
		Type:    receiver,
	}
	assert.True(t, withImmutSelf.Equals(withImmutSelf2),
		"FuncTypes with structurally-equal SelfParams must be equal")
}
