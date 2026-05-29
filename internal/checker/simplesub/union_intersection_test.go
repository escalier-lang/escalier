package simplesub

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// union / intersection annotation builders.
func union(ts ...SimpleType) *Union               { return &Union{types: ts} }
func intersection(ts ...SimpleType) *Intersection { return &Intersection{types: ts} }

// TestConstrainUnionAnnotation exercises the union lattice rules directly:
//   - X <: (A | B)  iff  X <: A or X <: B
//   - (A | B) <: Y  iff  A <: Y and B <: Y
func TestConstrainUnionAnnotation(t *testing.T) {
	tests := []struct {
		name     string
		lhs, rhs SimpleType
		wantErr  bool
	}{
		// X <: (A | B): a member matches.
		{"number <: number | string", num(), union(num(), str()), false},
		{"string <: number | string", str(), union(num(), str()), false},
		// X <: (A | B): no member matches.
		{"boolean </: number | string", boolean(), union(num(), str()), true},
		// (A | B) <: Y: every member must be <: Y.
		{"number | number <: number", union(num(), num()), num(), false},
		{"number | string </: number", union(num(), str()), num(), true},
		// literal <: its primitive, inside a union.
		{"5 <: number | string", litNumT(5), union(num(), str()), false},
		// nested: a union of records against a single record (each member must fit).
		{"union of records <: record", union(rec("x", num()), rec("x", num(), "y", num())), rec("x", num()), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := NewInferer()
			errs := in.Constrain(tt.lhs, tt.rhs)
			if tt.wantErr {
				require.NotEmpty(t, errs)
			} else {
				require.Empty(t, errs)
			}
		})
	}
}

// TestConstrainIntersectionAnnotation exercises the intersection lattice rules:
//   - X <: (A & B)  iff  X <: A and X <: B
//   - (A & B) <: Y  iff  A <: Y or B <: Y
func TestConstrainIntersectionAnnotation(t *testing.T) {
	tests := []struct {
		name     string
		lhs, rhs SimpleType
		wantErr  bool
	}{
		// X <: (A & B): X must satisfy both. A record with both fields satisfies
		// {x} & {y}.
		{"{x,y} <: {x} & {y}", rec("x", num(), "y", num()),
			intersection(rec("x", num()), rec("y", num())), false},
		// X <: (A & B): missing one half fails.
		{"{x} </: {x} & {y}", rec("x", num()),
			intersection(rec("x", num()), rec("y", num())), true},
		// (A & B) <: Y: one member suffices.
		{"number & string <: number", intersection(num(), str()), num(), false},
		// (A & B) <: Y: neither member matches.
		{"number & string </: boolean", intersection(num(), str()), boolean(), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := NewInferer()
			errs := in.Constrain(tt.lhs, tt.rhs)
			if tt.wantErr {
				require.NotEmpty(t, errs)
			} else {
				require.Empty(t, errs)
			}
		})
	}
}

// TestUnionAnnotationParamRoundTrips: a parameter annotated `number | string` is
// usable and renders back as the written annotation — the union works as a
// constraint *input*, not just as inferred output.
//
//	fn f(x: number | string) { return x }  ==>  fn (x: number | string) -> number | string
func TestUnionAnnotationParamRoundTrips(t *testing.T) {
	f := &Lam{
		Params:     []string{"x"},
		ParamTypes: []SimpleType{union(num(), str())},
		Body:       vr("x"),
	}
	got, errs := Render(f)
	require.Empty(t, errs)
	require.Equal(t, "fn (x: number | string) -> number | string", got)
}

// TestIntersectionAnnotationParamRoundTrips: an intersection annotation round-
// trips through Render.
//
//	fn f(x: {a: number} & {b: string}) { return x }
//	  ==>  fn (x: {a: number} & {b: string}) -> {a: number} & {b: string}
func TestIntersectionAnnotationParamRoundTrips(t *testing.T) {
	f := &Lam{
		Params:     []string{"x"},
		ParamTypes: []SimpleType{intersection(rec("a", num()), rec("b", str()))},
		Body:       vr("x"),
	}
	got, errs := Render(f)
	require.Empty(t, errs)
	require.Equal(t, "fn (x: {a: number} & {b: string}) -> {a: number} & {b: string}", got)
}

// TestVariableAgainstUnionRecordsWholeUnion guards a soundness subtlety: when
// the lower side of `X <: (A | B)` is a *variable*, the "exists" rule must not
// speculatively try a member (that would add a bound and wrongly pin the
// variable to the first member). Instead the whole union is recorded as the
// variable's upper bound, so the variable still admits every member.
//
// Here `a <: number | string` must leave a accepting BOTH number and string as
// lower bounds, while still rejecting boolean.
func TestVariableAgainstUnionRecordsWholeUnion(t *testing.T) {
	in := NewInferer()
	a := in.freshVar(0)
	require.Empty(t, in.Constrain(a, union(num(), str())))
	// Both union members are valid lower bounds for a.
	require.Empty(t, in.Constrain(num(), a), "number is a subtype of number | string")
	require.Empty(t, in.Constrain(str(), a), "string is a subtype of number | string")
	// A non-member is still rejected.
	require.NotEmpty(t, in.Constrain(boolean(), a), "boolean is not a subtype of number | string")
}

// TestUnionAnnotationAcceptsAndRejectsArgument is the plan's headline check: a
// function taking `number | string` accepts a number argument and rejects a
// boolean, verified end-to-end through application.
func TestUnionAnnotationAcceptsAndRejectsArgument(t *testing.T) {
	mkApp := func(arg Term) Term {
		// (fn (x: number | string) { return x })(arg)
		fn := &Lam{
			Params:     []string{"x"},
			ParamTypes: []SimpleType{union(num(), str())},
			Body:       vr("x"),
		}
		return &App{Fn: fn, Arg: arg}
	}

	_, errs := Infer(mkApp(litNum(5)))
	require.Empty(t, errs, "a number argument should satisfy number | string")

	_, errs = Infer(mkApp(&Lit{Kind: "bool", Bool: true}))
	require.NotEmpty(t, errs, "a boolean argument should be rejected by number | string")
}
