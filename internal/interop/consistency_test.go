package interop

import (
	"fmt"
	"testing"

	"github.com/escalier-lang/escalier/internal/type_system"
	"github.com/stretchr/testify/require"
)

func mkParam(t type_system.Type) *type_system.FuncParam {
	return &type_system.FuncParam{Type: t}
}

func mkOptionalParam(t type_system.Type) *type_system.FuncParam {
	return &type_system.FuncParam{Type: t, Optional: true}
}

func mkFunc(params []type_system.Type, ret type_system.Type) *type_system.FuncType {
	fps := make([]*type_system.FuncParam, len(params))
	for i, t := range params {
		fps[i] = mkParam(t)
	}
	return type_system.NewFuncType(nil, nil, fps, ret, nil)
}

func mkFuncParams(params []*type_system.FuncParam, ret type_system.Type) *type_system.FuncType {
	return type_system.NewFuncType(nil, nil, params, ret, nil)
}

var (
	stringPrim = type_system.NewStrPrimType(nil)
	numberPrim = type_system.NewNumPrimType(nil)
)

// expectedSigMismatch builds the full error message produced by
// ErrSignatureMismatch.Error() so tests assert the complete diagnostic
// text rather than just the Field axis.
func expectedSigMismatch(field, override, original string) string {
	return fmt.Sprintf(
		"override of <unknown> changes signature shape (%s): override=%s, original=%s\n  override at :0",
		field, override, original,
	)
}

func TestCheckArityMismatch(t *testing.T) {
	override := mkFunc([]type_system.Type{stringPrim}, numberPrim)
	original := mkFunc([]type_system.Type{stringPrim, numberPrim}, numberPrim)
	err := Check(override, original, Path{}, Origin{})
	require.Error(t, err, "expected arity mismatch error")
	require.Equal(t, expectedSigMismatch("arity", override.String(), original.String()), err.Error())
}

func TestCheckReturnMismatch(t *testing.T) {
	override := mkFunc([]type_system.Type{stringPrim}, stringPrim)
	original := mkFunc([]type_system.Type{stringPrim}, numberPrim)
	err := Check(override, original, Path{}, Origin{})
	require.Error(t, err, "expected return mismatch error")
	require.Equal(t, expectedSigMismatch("return", override.String(), original.String()), err.Error())
}

func TestCheckParamMismatch(t *testing.T) {
	override := mkFunc([]type_system.Type{stringPrim}, numberPrim)
	original := mkFunc([]type_system.Type{numberPrim}, numberPrim)
	err := Check(override, original, Path{}, Origin{})
	require.Error(t, err, "expected param mismatch error")
	require.Equal(t, expectedSigMismatch("param[0]", override.String(), original.String()), err.Error())
}

func TestCheckParamOptionalMismatch(t *testing.T) {
	override := mkFuncParams([]*type_system.FuncParam{mkOptionalParam(stringPrim)}, numberPrim)
	original := mkFuncParams([]*type_system.FuncParam{mkParam(stringPrim)}, numberPrim)
	err := Check(override, original, Path{}, Origin{})
	require.Error(t, err, "expected optionality mismatch error")
	require.Equal(t, expectedSigMismatch("param[0]", override.String(), original.String()), err.Error())
}

func TestCheckEquivalentSignatures(t *testing.T) {
	override := mkFunc([]type_system.Type{stringPrim}, numberPrim)
	original := mkFunc([]type_system.Type{stringPrim}, numberPrim)
	require.NoError(t, Check(override, original, Path{}, Origin{}))
}

func TestCheckSetOverloadCountMismatch(t *testing.T) {
	o := []*type_system.FuncType{mkFunc(nil, numberPrim)}
	orig := []*type_system.FuncType{mkFunc(nil, numberPrim), mkFunc(nil, stringPrim)}
	err := CheckSet(o, orig, Path{}, Origin{})
	require.Error(t, err, "expected overload count mismatch error")
	require.Equal(t, expectedSigMismatch("overload count", "1", "2"), err.Error())
}

func TestCheckNilOverride(t *testing.T) {
	original := mkFunc([]type_system.Type{stringPrim}, numberPrim)
	err := Check(nil, original, Path{}, Origin{})
	require.Error(t, err, "expected nilFunc mismatch error")
	require.Equal(t, expectedSigMismatch("nilFunc", "nilFunc", original.String()), err.Error())
}

func TestCheckNilOriginal(t *testing.T) {
	override := mkFunc([]type_system.Type{stringPrim}, numberPrim)
	err := Check(override, nil, Path{}, Origin{})
	require.Error(t, err, "expected nilFunc mismatch error")
	require.Equal(t, expectedSigMismatch("nilFunc", override.String(), "nilFunc"), err.Error())
}

func TestCheckSetNilSignature(t *testing.T) {
	override := []*type_system.FuncType{nil}
	original := []*type_system.FuncType{mkFunc([]type_system.Type{stringPrim}, numberPrim)}
	err := CheckSet(override, original, Path{}, Origin{})
	require.Error(t, err, "expected nilFunc mismatch error")
	require.Equal(t, expectedSigMismatch("nilFunc", "nilFunc", original[0].String()), err.Error())
}

func TestCheckSetPerSignatureMismatchAnnotatesIndex(t *testing.T) {
	override := []*type_system.FuncType{
		mkFunc([]type_system.Type{stringPrim}, numberPrim),
		mkFunc([]type_system.Type{stringPrim}, numberPrim),
	}
	original := []*type_system.FuncType{
		mkFunc([]type_system.Type{stringPrim}, numberPrim),
		mkFunc([]type_system.Type{numberPrim}, numberPrim),
	}
	err := CheckSet(override, original, Path{}, Origin{})
	require.Error(t, err, "expected per-signature mismatch error")
	require.Equal(t, expectedSigMismatch("overload[1]/param[0]", override[1].String(), original[1].String()), err.Error())
}
