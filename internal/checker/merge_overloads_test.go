package checker

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/type_system"
	"github.com/stretchr/testify/require"
)

// methodElem builds a single-arm MethodElem named `name` whose
// signature has receiver mutability `mut` and a single
// `tag: <litTag>` literal-typed param. The resulting elems mirror
// the post-elaboration / pre-merge shape that every collection site
// hands to MergeMethodOverloads.
func methodElemArm(name string, mut bool, litTag string) *type_system.MethodElem {
	sig := type_system.NewFuncType(nil, nil,
		[]*type_system.FuncParam{{
			Pattern: type_system.NewIdentPat("tag"),
			Type:    type_system.NewStrLitType(nil, litTag),
		}},
		type_system.NewNumPrimType(nil), nil)
	sig.SelfParam = type_system.NewSelfParam(
		type_system.NewTypeRefType(nil, "Self", nil), mut)
	return type_system.NewMethodElem(type_system.NewStrKey(name), sig)
}

func TestMergeMethodOverloads_NoDuplicates_RoundTrip(t *testing.T) {
	c := NewChecker(nil)
	elems := []type_system.ObjTypeElem{
		methodElemArm("foo", false, "a"),
		methodElemArm("bar", false, "b"),
	}
	out, errs := c.MergeMethodOverloads(elems, ast.Span{})
	require.Empty(t, errs)
	require.Equal(t, elems, out)
	for _, e := range out {
		me := e.(*type_system.MethodElem)
		require.Len(t, me.Signatures, 1)
	}
}

func TestMergeMethodOverloads_CollapsesSameName(t *testing.T) {
	c := NewChecker(nil)
	a := methodElemArm("foo", false, "a")
	b := methodElemArm("foo", false, "b")
	other := methodElemArm("bar", false, "x")
	elems := []type_system.ObjTypeElem{a, b, other}

	out, errs := c.MergeMethodOverloads(elems, ast.Span{})
	require.Empty(t, errs)
	require.Len(t, out, 2)

	merged, ok := out[0].(*type_system.MethodElem)
	require.True(t, ok)
	require.Equal(t, "foo", merged.Name.Str)
	require.Len(t, merged.Signatures, 2)

	bar, ok := out[1].(*type_system.MethodElem)
	require.True(t, ok)
	require.Equal(t, "bar", bar.Name.Str)
	require.Len(t, bar.Signatures, 1)
}

func TestMergeMethodOverloads_ReceiverMutMismatch(t *testing.T) {
	c := NewChecker(nil)
	a := methodElemArm("swap", false, "a")
	b := methodElemArm("swap", true, "b")
	elems := []type_system.ObjTypeElem{a, b}

	out, errs := c.MergeMethodOverloads(elems, ast.Span{})
	require.Len(t, errs, 1)
	mismatch, ok := errs[0].(OverloadReceiverMutMismatchError)
	require.True(t, ok)
	require.Equal(t, "swap", mismatch.Name)
	require.Equal(t, "self", mismatch.FirstReceiver)
	require.Equal(t, "mut self", mismatch.OtherReceiver)

	// The first arm's shape survives; the mismatched arm is dropped.
	require.Len(t, out, 1)
	merged := out[0].(*type_system.MethodElem)
	require.Len(t, merged.Signatures, 1)
	require.False(t, type_system.ReceiverIsMut(merged.Signatures[0]))
}

func TestMergeMethodOverloads_ReceiverMutMismatch_ReverseDirection(t *testing.T) {
	c := NewChecker(nil)
	a := methodElemArm("swap", true, "a")
	b := methodElemArm("swap", false, "b")
	elems := []type_system.ObjTypeElem{a, b}

	out, errs := c.MergeMethodOverloads(elems, ast.Span{})
	require.Len(t, errs, 1)
	mismatch, ok := errs[0].(OverloadReceiverMutMismatchError)
	require.True(t, ok)
	require.Equal(t, "swap", mismatch.Name)
	require.Equal(t, "mut self", mismatch.FirstReceiver)
	require.Equal(t, "self", mismatch.OtherReceiver)

	// The first arm's `mut self` shape survives; the mismatched
	// `self` arm is dropped.
	require.Len(t, out, 1)
	merged := out[0].(*type_system.MethodElem)
	require.Len(t, merged.Signatures, 1)
	require.True(t, type_system.ReceiverIsMut(merged.Signatures[0]))
}

// TestMergeMethodOverloads_StableErrorOrder pins the ordering of
// OverloadReceiverMutMismatchErrors when more than one method name
// has a mismatch. Go map iteration is randomized per-run, so iterating
// indicesByName directly would expose this test to flakes.
func TestMergeMethodOverloads_StableErrorOrder(t *testing.T) {
	c := NewChecker(nil)
	elems := []type_system.ObjTypeElem{
		methodElemArm("alpha", false, "a"),
		methodElemArm("alpha", true, "b"),
		methodElemArm("beta", true, "a"),
		methodElemArm("beta", false, "b"),
		methodElemArm("gamma", false, "a"),
		methodElemArm("gamma", true, "b"),
	}
	for i := 0; i < 20; i++ {
		_, errs := c.MergeMethodOverloads(elems, ast.Span{})
		require.Len(t, errs, 3)
		require.Equal(t, "alpha", errs[0].(OverloadReceiverMutMismatchError).Name)
		require.Equal(t, "beta", errs[1].(OverloadReceiverMutMismatchError).Name)
		require.Equal(t, "gamma", errs[2].(OverloadReceiverMutMismatchError).Name)
	}
}

func TestMergeMethodOverloads_Idempotent(t *testing.T) {
	c := NewChecker(nil)
	elems := []type_system.ObjTypeElem{
		methodElemArm("foo", false, "a"),
		methodElemArm("foo", false, "b"),
	}
	once, errs := c.MergeMethodOverloads(elems, ast.Span{})
	require.Empty(t, errs)
	twice, errs := c.MergeMethodOverloads(once, ast.Span{})
	require.Empty(t, errs)
	require.Equal(t, once, twice)
}
