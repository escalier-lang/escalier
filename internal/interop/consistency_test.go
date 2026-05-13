package interop

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/type_system"
)

func mkParam(t type_system.Type) *type_system.FuncParam {
	return &type_system.FuncParam{Type: t}
}

func mkFunc(params []type_system.Type, ret type_system.Type) *type_system.FuncType {
	fps := make([]*type_system.FuncParam, len(params))
	for i, t := range params {
		fps[i] = mkParam(t)
	}
	return type_system.NewFuncType(nil, nil, fps, ret, nil)
}

var (
	stringPrim = type_system.NewStrPrimType(nil)
	numberPrim = type_system.NewNumPrimType(nil)
)

func TestCheckArityMismatch(t *testing.T) {
	override := mkFunc([]type_system.Type{stringPrim}, numberPrim)
	original := mkFunc([]type_system.Type{stringPrim, numberPrim}, numberPrim)
	err := Check(override, original, Path{}, Origin{})
	if err == nil {
		t.Fatal("expected arity mismatch error; got nil")
	}
	sigErr, ok := err.(*ErrSignatureMismatch)
	if !ok {
		t.Fatalf("expected ErrSignatureMismatch; got %T", err)
	}
	if sigErr.Field != "arity" {
		t.Fatalf("expected Field=arity; got %q", sigErr.Field)
	}
}

func TestCheckReturnMismatch(t *testing.T) {
	override := mkFunc([]type_system.Type{stringPrim}, stringPrim)
	original := mkFunc([]type_system.Type{stringPrim}, numberPrim)
	err := Check(override, original, Path{}, Origin{})
	if err == nil {
		t.Fatal("expected return mismatch error")
	}
	if sigErr, ok := err.(*ErrSignatureMismatch); !ok || sigErr.Field != "return" {
		t.Fatalf("expected Field=return; got %v", err)
	}
}

func TestCheckParamMismatch(t *testing.T) {
	override := mkFunc([]type_system.Type{stringPrim}, numberPrim)
	original := mkFunc([]type_system.Type{numberPrim}, numberPrim)
	err := Check(override, original, Path{}, Origin{})
	sigErr, ok := err.(*ErrSignatureMismatch)
	if !ok || sigErr.Field != "param[0]" {
		t.Fatalf("expected Field=param[0]; got %v", err)
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
	sigErr, ok := err.(*ErrSignatureMismatch)
	if !ok || sigErr.Field != "overload count" {
		t.Fatalf("expected Field=overload count; got %v", err)
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
	sigErr, ok := err.(*ErrSignatureMismatch)
	if !ok {
		t.Fatalf("expected ErrSignatureMismatch; got %v", err)
	}
	if sigErr.Field != "overload[1]/param[0]" {
		t.Fatalf("expected Field=overload[1]/param[0]; got %q", sigErr.Field)
	}
}
