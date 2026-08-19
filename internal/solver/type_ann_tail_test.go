package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// The surface syntax `A | ... : R` resolves to a bounded tail and `A | B | ...` to an
// unbounded one, so a test can write the source directly rather than minting the union by
// hand. Each printed form reparses to the same type, which is the round-trip the bounded
// tail syntax exists to establish.
func TestResolveUnionTailSyntax(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"a bounded tail after several members", `type Result = "a" | "b" | ... : string`, `"a" | "b" | ... : string`},
		{"a bounded tail after one member", `type Result = "a" | ... : string`, `"a" | ... : string`},
		{"a numeric bound", `type Result = 0 | ... : number`, "0 | ... : number"},
		{"a parenthesized union bound", `type Result = "a" | ... : (number | string)`, `"a" | ... : (number | string)`},
		{"an unbounded tail", `type Result = "a" | "b" | ...`, `"a" | "b" | ...`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, ctx, errs := inferTypeNodes(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, soltype.Print(expandAliasResidual(ctx, nodes["Result"])))
		})
	}
}

// A tail bound written in source drives subtyping the same way a hand-built bounded union
// does, so the parser and lowering together produce a semantically live bound rather than a
// shape that only prints. Each probe mirrors one in TestBoundedTailIsNotTop, which builds the
// union with newBoundedUnion instead of parsing it.
func TestResolveUnionTailSyntaxSubtyping(t *testing.T) {
	nodes, ctx, errs := inferTypeNodes(t, `
		type Bounded = "a" | ... : string
		type NumberTail = "a" | ... : number
	`)
	require.Empty(t, errs)
	bounded := expandAliasResidual(ctx, nodes["Bounded"])
	numberTail := expandAliasResidual(ctx, nodes["NumberTail"])

	probes := []struct {
		name     string
		sub, sup soltype.Type
		want     bool
	}{
		// The bound decides what is a subtype of the union: a value it admits may be one of
		// the tail's members, and a value it rejects cannot be.
		{"a named member is a subtype of it", strLit("a"), bounded, true},
		{"another string is a subtype of it", strLit("z"), bounded, true},
		{"the bound itself is a subtype of it", str(), bounded, true},
		{"a number literal is not a subtype of it", numLit(5), bounded, false},
		{"a number is not a subtype of it", num(), bounded, false},
		// The bound also decides what the union is a subtype of, carrying the tail into a
		// supertype: every member is a string, so `string` absorbs the whole union, but a
		// `number` tail rejects the string tail's members.
		{"a bounded tail is a subtype of its own bound", bounded, str(), true},
		{"a string tail is not a subtype of a number tail", bounded, numberTail, false},
	}
	for _, p := range probes {
		require.Equal(t, p.want, subtypeHolds(ctx, p.sub, p.sup), p.name)
	}
}

// An unbounded tail written in source resolves to the open union that is top for subtyping,
// so every value is a subtype of it. These probes answer the other way from their bounded
// counterparts in TestResolveUnionTailSyntaxSubtyping, which is what makes the missing bound
// the thing that changed. The hand-built counterpart is TestOpenUnionIsTopForSubtypingOnly.
func TestResolveUnionTailSyntaxUnboundedSubtyping(t *testing.T) {
	// The unbounded marker needs two or more named members, unlike a bounded tail, which a
	// lone member can carry. TestParseInexactUnionMarkerRequiresUnion pins that rule.
	nodes, ctx, errs := inferTypeNodes(t, `type Open = "a" | "b" | ...`)
	require.Empty(t, errs)
	open := expandAliasResidual(ctx, nodes["Open"])

	probes := []struct {
		name string
		sub  soltype.Type
		want bool
	}{
		// An unbounded tail admits every value, so a number the bounded tail rejects is a
		// subtype here.
		{"a named member is a subtype of it", strLit("a"), true},
		{"a number literal is a subtype of it", numLit(5), true},
		{"a number is a subtype of it", num(), true},
		{"unknown is a subtype of it", &soltype.UnknownType{}, true},
	}
	for _, p := range probes {
		require.Equal(t, p.want, subtypeHolds(ctx, p.sub, open), p.name)
	}
}

// The bounded tail earns its syntax on the conditional-type examples #1131 raises: a check
// against `"a" | ... : infer U` binds U to the operand's own tail bound, and a check against a
// concrete tail bound selects the then branch only when the bounds agree. Each case is the
// source form of a CondType those examples built by hand.
func TestResolveUnionTailSyntaxUnderConditional(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			"infer binds U to the tail bound",
			`type Result = if ("a" | ... : string) : ("a" | ... : infer U) { U } else { boolean }`,
			"string",
		},
		{
			"the inferred bound flows into the then branch",
			`type Result = if ("a" | ... : string) : ("a" | ... : infer U) { [U] } else { boolean }`,
			"[string]",
		},
		{
			"a matching concrete bound selects the then branch",
			`type Result = if ("a" | ... : string) : ("a" | ... : string) { 9 } else { boolean }`,
			"9",
		},
		{
			"a non-matching bound selects the else branch",
			`type Result = if ("a" | ... : string) : ("a" | ... : number) { 9 } else { boolean }`,
			"boolean",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, ctx, errs := inferTypeNodes(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, soltype.Print(expandAliasResidual(ctx, nodes["Result"])))
		})
	}
}
