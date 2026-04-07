package type_system

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPruneConcreteType(t *testing.T) {
	strType := NewStrPrimType(nil)
	result := Prune(strType)
	assert.Equal(t, strType, result, "pruning a concrete type returns it unchanged")
}

func TestPruneUnboundTypeVar(t *testing.T) {
	tv := NewTypeVarType(nil, 1)
	result := Prune(tv)
	assert.Equal(t, tv, result, "pruning an unbound TypeVar returns it unchanged")
}

func TestPruneSingleTypeVarWithConcreteInstance(t *testing.T) {
	numType := NewNumPrimType(nil)
	tv := NewTypeVarType(nil, 1)
	tv.Instance = numType

	result := Prune(tv)
	assert.Equal(t, numType, result, "pruning a TypeVar with concrete Instance returns the Instance")
	assert.Equal(t, numType, tv.Instance, "Instance should still point to concrete type")
}

func TestPruneTwoTypeVarChain(t *testing.T) {
	numType := NewNumPrimType(nil)
	tvA := NewTypeVarType(nil, 1)
	tvB := NewTypeVarType(nil, 2)
	tvA.Instance = tvB
	tvB.Instance = numType

	result := Prune(tvA)
	assert.Equal(t, numType, result, "should resolve to the terminal concrete type")
	// Path compression: both should point directly at the concrete type.
	assert.Equal(t, numType, tvA.Instance, "tvA.Instance should be path-compressed to concrete type")
	assert.Equal(t, numType, tvB.Instance, "tvB.Instance should remain the concrete type")
}

func TestPruneThreeTypeVarChain(t *testing.T) {
	strType := NewStrPrimType(nil)
	tvA := NewTypeVarType(nil, 1)
	tvB := NewTypeVarType(nil, 2)
	tvC := NewTypeVarType(nil, 3)
	tvA.Instance = tvB
	tvB.Instance = tvC
	tvC.Instance = strType

	result := Prune(tvA)
	assert.Equal(t, strType, result, "should resolve through 3-node chain")
	assert.Equal(t, strType, tvA.Instance, "tvA should be path-compressed")
	assert.Equal(t, strType, tvB.Instance, "tvB should be path-compressed")
	assert.Equal(t, strType, tvC.Instance, "tvC should still point to concrete")
}

func TestPruneInstanceChainTwoNodes(t *testing.T) {
	numType := NewNumPrimType(nil)
	tvA := NewTypeVarType(nil, 1)
	tvB := NewTypeVarType(nil, 2)
	tvA.Instance = tvB
	tvB.Instance = numType

	Prune(tvA)

	// tvA should have InstanceChain [tvA, tvB]
	assert.Len(t, tvA.InstanceChain, 2, "tvA.InstanceChain should contain both TypeVars")
	assert.Equal(t, tvA, tvA.InstanceChain[0])
	assert.Equal(t, tvB, tvA.InstanceChain[1])

	// tvB should have InstanceChain [tvB] (suffix of same backing array)
	assert.Len(t, tvB.InstanceChain, 1, "tvB.InstanceChain should contain just tvB")
	assert.Equal(t, tvB, tvB.InstanceChain[0])
}

func TestPruneInstanceChainThreeNodes(t *testing.T) {
	strType := NewStrPrimType(nil)
	tvA := NewTypeVarType(nil, 1)
	tvB := NewTypeVarType(nil, 2)
	tvC := NewTypeVarType(nil, 3)
	tvA.Instance = tvB
	tvB.Instance = tvC
	tvC.Instance = strType

	Prune(tvA)

	assert.Len(t, tvA.InstanceChain, 3)
	assert.Equal(t, []*TypeVarType{tvA, tvB, tvC}, tvA.InstanceChain)

	assert.Len(t, tvB.InstanceChain, 2)
	assert.Equal(t, []*TypeVarType{tvB, tvC}, tvB.InstanceChain)

	assert.Len(t, tvC.InstanceChain, 1)
	assert.Equal(t, []*TypeVarType{tvC}, tvC.InstanceChain)
}

func TestPruneNoInstanceChainForDirectConcreteInstance(t *testing.T) {
	numType := NewNumPrimType(nil)
	tv := NewTypeVarType(nil, 1)
	tv.Instance = numType

	Prune(tv)

	// No chain when Instance is directly a concrete type (not another TypeVar).
	assert.Nil(t, tv.InstanceChain, "no InstanceChain when Instance is concrete")
}

func TestPruneInstanceChainNotOverwrittenOnSecondCall(t *testing.T) {
	numType := NewNumPrimType(nil)
	tvA := NewTypeVarType(nil, 1)
	tvB := NewTypeVarType(nil, 2)
	tvA.Instance = tvB
	tvB.Instance = numType

	Prune(tvA)
	originalChain := tvA.InstanceChain

	// After path compression, tvA.Instance is numType (concrete).
	// A second Prune should NOT overwrite the chain.
	Prune(tvA)
	assert.Equal(t, originalChain, tvA.InstanceChain, "InstanceChain should not be overwritten by second Prune")
}

func TestPruneChainWithUnboundTerminal(t *testing.T) {
	// Chain where the terminal TypeVar has no Instance (unbound).
	tvA := NewTypeVarType(nil, 1)
	tvB := NewTypeVarType(nil, 2)
	tvA.Instance = tvB
	// tvB.Instance is nil (unbound)

	result := Prune(tvA)
	assert.Equal(t, tvB, result, "should resolve to the terminal unbound TypeVar")

	// InstanceChain should still be recorded.
	assert.Len(t, tvA.InstanceChain, 2)
	assert.Equal(t, []*TypeVarType{tvA, tvB}, tvA.InstanceChain)
}

func TestPruneUnboundTerminalNotSelfReferencing(t *testing.T) {
	// After pruning tvA -> tvB (unbound), tvB.Instance must remain nil.
	// A bug where the compression loop sets tvB.Instance = tvB would
	// create a self-loop causing infinite loops on subsequent Prune calls.
	tvA := NewTypeVarType(nil, 1)
	tvB := NewTypeVarType(nil, 2)
	tvA.Instance = tvB

	Prune(tvA)
	assert.Nil(t, tvB.Instance, "terminal unbound TypeVar must not get a self-referencing Instance")

	// A subsequent Prune on tvB must not loop.
	result := Prune(tvB)
	assert.Equal(t, tvB, result, "pruning unbound tvB should return tvB itself")
}

func TestPruneUnboundTerminalThreeNodeChain(t *testing.T) {
	// tvA -> tvB -> tvC (unbound). Compression should point tvA and tvB
	// at tvC, but NOT set tvC.Instance = tvC.
	tvA := NewTypeVarType(nil, 1)
	tvB := NewTypeVarType(nil, 2)
	tvC := NewTypeVarType(nil, 3)
	tvA.Instance = tvB
	tvB.Instance = tvC

	result := Prune(tvA)
	assert.Equal(t, tvC, result)
	assert.Equal(t, tvC, tvA.Instance, "tvA should be compressed to tvC")
	assert.Equal(t, tvC, tvB.Instance, "tvB should be compressed to tvC")
	assert.Nil(t, tvC.Instance, "tvC must remain unbound")
}

func TestPruneMiddleThenHead_PreservesFullChain(t *testing.T) {
	// Prune tvB first (middle of tvA->tvB->tvC->number), then prune tvA.
	// tvA's InstanceChain must include tvC even though tvB->tvC was already
	// path-compressed by the first Prune call.
	numType := NewNumPrimType(nil)
	tvA := NewTypeVarType(nil, 1)
	tvB := NewTypeVarType(nil, 2)
	tvC := NewTypeVarType(nil, 3)
	tvA.Instance = tvB
	tvB.Instance = tvC
	tvC.Instance = numType

	// Prune middle first — tvB gets [tvB, tvC], tvC gets [tvC].
	Prune(tvB)
	assert.Equal(t, []*TypeVarType{tvB, tvC}, tvB.InstanceChain)
	assert.Equal(t, []*TypeVarType{tvC}, tvC.InstanceChain)

	// Now prune from the head.
	Prune(tvA)

	// tvA's chain must include all three, not just [tvA, tvB].
	assert.Equal(t, []*TypeVarType{tvA, tvB, tvC}, tvA.InstanceChain,
		"tvA.InstanceChain should include tvC from tvB's existing chain")

	// tvB's chain must NOT be truncated to [tvB].
	assert.Equal(t, []*TypeVarType{tvB, tvC}, tvB.InstanceChain,
		"tvB.InstanceChain should preserve its original [tvB, tvC]")
}

func TestPruneCalledOnMiddleOfChain(t *testing.T) {
	numType := NewNumPrimType(nil)
	tvA := NewTypeVarType(nil, 1)
	tvB := NewTypeVarType(nil, 2)
	tvC := NewTypeVarType(nil, 3)
	tvA.Instance = tvB
	tvB.Instance = tvC
	tvC.Instance = numType

	// Prune starting from tvB (middle of chain).
	result := Prune(tvB)
	assert.Equal(t, numType, result)
	assert.Equal(t, numType, tvB.Instance, "tvB should be path-compressed")

	// tvB should have its own chain [tvB, tvC].
	assert.Len(t, tvB.InstanceChain, 2)
	assert.Equal(t, []*TypeVarType{tvB, tvC}, tvB.InstanceChain)

	// tvA was not pruned, so its chain should not be set yet.
	assert.Nil(t, tvA.InstanceChain)
}
