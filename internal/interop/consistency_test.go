package interop

import (
	"fmt"
	"testing"

	"github.com/escalier-lang/escalier/internal/type_system"
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
		"override of  changes signature shape (%s): override=%s, original=%s\n  override at :0",
		field, override, original,
	)
}

func TestCheckArityMismatch(t *testing.T) {
	override := mkFunc([]type_system.Type{stringPrim}, numberPrim)
	original := mkFunc([]type_system.Type{stringPrim, numberPrim}, numberPrim)
	err := Check(override, original, Path{}, Origin{})
	if err == nil {
		t.Fatal("expected arity mismatch error; got nil")
	}
	want := expectedSigMismatch("arity", override.String(), original.String())
	if err.Error() != want {
		t.Fatalf("error mismatch\n got: %q\nwant: %q", err.Error(), want)
	}
}

func TestCheckReturnMismatch(t *testing.T) {
	override := mkFunc([]type_system.Type{stringPrim}, stringPrim)
	original := mkFunc([]type_system.Type{stringPrim}, numberPrim)
	err := Check(override, original, Path{}, Origin{})
	if err == nil {
		t.Fatal("expected return mismatch error")
	}
	want := expectedSigMismatch("return", override.String(), original.String())
	if err.Error() != want {
		t.Fatalf("error mismatch\n got: %q\nwant: %q", err.Error(), want)
	}
}

func TestCheckParamMismatch(t *testing.T) {
	override := mkFunc([]type_system.Type{stringPrim}, numberPrim)
	original := mkFunc([]type_system.Type{numberPrim}, numberPrim)
	err := Check(override, original, Path{}, Origin{})
	if err == nil {
		t.Fatal("expected param mismatch error")
	}
	want := expectedSigMismatch("param[0]", override.String(), original.String())
	if err.Error() != want {
		t.Fatalf("error mismatch\n got: %q\nwant: %q", err.Error(), want)
	}
}

func TestCheckParamOptionalMismatch(t *testing.T) {
	override := mkFuncParams([]*type_system.FuncParam{mkOptionalParam(stringPrim)}, numberPrim)
	original := mkFuncParams([]*type_system.FuncParam{mkParam(stringPrim)}, numberPrim)
	err := Check(override, original, Path{}, Origin{})
	if err == nil {
		t.Fatal("expected optionality mismatch error; got nil")
	}
	want := expectedSigMismatch("param[0]", override.String(), original.String())
	if err.Error() != want {
		t.Fatalf("error mismatch\n got: %q\nwant: %q", err.Error(), want)
	}
}

func TestCheckEquivalentSignatures(t *testing.T) {
	override := mkFunc([]type_system.Type{stringPrim}, numberPrim)
	original := mkFunc([]type_system.Type{stringPrim}, numberPrim)
	if err := Check(override, original, Path{}, Origin{}); err != nil {
		t.Fatalf("expected no error; got %v", err)
	}
}

func TestCheckSetOverloadCountMismatch(t *testing.T) {
	o := []*type_system.FuncType{mkFunc(nil, numberPrim)}
	orig := []*type_system.FuncType{mkFunc(nil, numberPrim), mkFunc(nil, stringPrim)}
	err := CheckSet(o, orig, Path{}, Origin{})
	if err == nil {
		t.Fatal("expected overload count mismatch error")
	}
	want := expectedSigMismatch("overload count", "1", "2")
	if err.Error() != want {
		t.Fatalf("error mismatch\n got: %q\nwant: %q", err.Error(), want)
	}
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
	if err == nil {
		t.Fatal("expected per-signature mismatch error")
	}
	want := expectedSigMismatch("overload[1]/param[0]", override[1].String(), original[1].String())
	if err.Error() != want {
		t.Fatalf("error mismatch\n got: %q\nwant: %q", err.Error(), want)
	}
}
