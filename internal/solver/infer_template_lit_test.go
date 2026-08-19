package solver

import (
	"fmt"
	"strings"
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// A template literal type is stored as a residual and reduced by taking the cartesian product over
// its interpolations, folding each string-literal interpolation into the surrounding segments. Each
// case asserts the stored `Result` renders the way the source wrote it, then asserts that reducing
// it with the alias environment — the expansion constrain performs to check a constraint — produces
// the union of string literals. The cases cover a bare template, a literal interpolation, a union
// interpolation, two interpolations whose product enumerates every pairing, and a named alias
// interpolation that expands to its union body before the product.
func TestInferTemplateLitReduction(t *testing.T) {
	tests := []struct {
		name         string
		src          string
		wantSymbolic string
		wantExpanded string
	}{
		{
			// A template with no interpolation collapses to the lone string literal.
			name:         "NoInterpolation",
			src:          "type Result = `abc`",
			wantSymbolic: "`abc`",
			wantExpanded: `"abc"`,
		},
		{
			// A single string-literal interpolation folds into the surrounding text.
			name:         "LiteralInterpolation",
			src:          "type Result = `on${\"click\"}`",
			wantSymbolic: "`on${\"click\"}`",
			wantExpanded: `"onclick"`,
		},
		{
			// A union interpolation reduces to one string literal per member.
			name:         "UnionInterpolation",
			src:          "type Result = `on${\"a\" | \"b\"}`",
			wantSymbolic: "`on${\"a\" | \"b\"}`",
			wantExpanded: `"ona" | "onb"`,
		},
		{
			// Two union interpolations enumerate every pairing as the cartesian product.
			name:         "TwoInterpolations",
			src:          "type Result = `${\"a\" | \"b\"}-${\"x\" | \"y\"}`",
			wantSymbolic: "`${\"a\" | \"b\"}-${\"x\" | \"y\"}`",
			wantExpanded: `"a-x" | "a-y" | "b-x" | "b-y"`,
		},
		{
			// A named alias interpolation expands to its union body before the product.
			name: "AliasInterpolation",
			src: `
				type Dir = "left" | "right"
				type Result = ` + "`to-${Dir}`",
			wantSymbolic: "`to-${Dir}`",
			wantExpanded: `"to-left" | "to-right"`,
		},
		{
			// A union interpolation grounds each member, so an aliased member collapses to its
			// literal rather than surviving as a residual interpolation.
			name: "UnionMemberAlias",
			src: `
				type B = "b"
				type U = "a" | B
				type Result = ` + "`x${U}`",
			wantSymbolic: "`x${U}`",
			wantExpanded: `"xa" | "xb"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, ctx, errs := inferTypeNodes(t, tt.src)
			require.Empty(t, errs)
			result := nodes["Result"]
			require.Equal(t, tt.wantSymbolic, soltype.Print(result))
			require.Equal(t, tt.wantExpanded, soltype.Print(expandResidual(ctx, result)))
		})
	}
}

// A template literal over a type parameter renders symbolically in a function signature and
// round-trips from parameter to return. The function fn f<T>(k: `on${T}`) -> `on${T}` { return k }
// keeps `on${T}` on both positions. The reflexive residual-to-residual constraint from `return k`
// succeeds inertly by structural equality, since the abstract interpolation never grounds.
func TestInferTemplateLitSignatureStaysSymbolic(t *testing.T) {
	values, _, errs := inferSource(t, "fn f<T>(k: `on${T}`) -> `on${T}` { return k }")
	require.Empty(t, errs)
	require.Equal(t, "fn <T>(k: `on${T}`) -> `on${T}`", values["f"])
}

// constrain reduces a ground template literal to the union of string literals to check
// satisfaction, while the stored type stays the residual. A value in the reduced union is accepted;
// one outside it is rejected against the union, so the diagnostic names the enumerated literals.
func TestInferTemplateLitConstraint(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string // "" ⇒ expect no error
	}{
		{
			name: "MemberAccepted",
			src:  "val r: `on${\"a\" | \"b\"}` = \"ona\"",
		},
		{
			name:    "NonMemberRejected",
			src:     "val r: `on${\"a\" | \"b\"}` = \"onc\"",
			wantErr: `cannot constrain "onc" <: "ona" | "onb"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			if tt.wantErr == "" {
				require.Empty(t, errs)
				return
			}
			require.Len(t, errs, 1)
			require.Equal(t, tt.wantErr, errs[0].Message())
		})
	}
}

// An intrinsic string operator `Uppercase<T>` and its three siblings are stored as residuals and
// reduced over a string-literal operand. Each case asserts the stored `Result` renders the way the
// source wrote it, then asserts that reducing it maps the operand's characters, distributing over a
// union operand. The cases cover each of the four operators and the union-distribution rule.
func TestInferStringIntrinsicReduction(t *testing.T) {
	tests := []struct {
		name         string
		src          string
		wantSymbolic string
		wantExpanded string
	}{
		{
			name:         "Uppercase",
			src:          `type Result = Uppercase<"abc">`,
			wantSymbolic: `Uppercase<"abc">`,
			wantExpanded: `"ABC"`,
		},
		{
			name:         "Lowercase",
			src:          `type Result = Lowercase<"ABC">`,
			wantSymbolic: `Lowercase<"ABC">`,
			wantExpanded: `"abc"`,
		},
		{
			name:         "Capitalize",
			src:          `type Result = Capitalize<"abc">`,
			wantSymbolic: `Capitalize<"abc">`,
			wantExpanded: `"Abc"`,
		},
		{
			name:         "Uncapitalize",
			src:          `type Result = Uncapitalize<"ABC">`,
			wantSymbolic: `Uncapitalize<"ABC">`,
			wantExpanded: `"aBC"`,
		},
		{
			// The operator distributes over a union operand, mapping each member.
			name:         "UnionOperand",
			src:          `type Result = Uppercase<"a" | "b">`,
			wantSymbolic: `Uppercase<"a" | "b">`,
			wantExpanded: `"A" | "B"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, ctx, errs := inferTypeNodes(t, tt.src)
			require.Empty(t, errs)
			result := nodes["Result"]
			require.Equal(t, tt.wantSymbolic, soltype.Print(result))
			require.Equal(t, tt.wantExpanded, soltype.Print(expandResidual(ctx, result)))
		})
	}
}

// A string-intrinsic residual over a type parameter renders symbolically in a function signature and
// round-trips from parameter to return: `fn f<T>(k: Uppercase<T>) -> Uppercase<T> { return k }`
// keeps `Uppercase<T>` on both positions. The reflexive `Uppercase<T> <: Uppercase<T>` from
// `return k` succeeds inertly by structural equality on the residual, since the abstract operand
// never grounds.
func TestInferStringIntrinsicSignatureStaysSymbolic(t *testing.T) {
	values, _, errs := inferSource(t, `fn f<T>(k: Uppercase<T>) -> Uppercase<T> { return k }`)
	require.Empty(t, errs)
	require.Equal(t, "fn <T>(k: Uppercase<T>) -> Uppercase<T>", values["f"])
}

// constrain reduces a ground string-intrinsic residual to the transformed literal to check
// satisfaction, while the stored type stays the residual. The transformed literal is accepted; any
// other literal is rejected against it, so the diagnostic names the mapped value.
func TestInferStringIntrinsicConstraint(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string // "" ⇒ expect no error
	}{
		{
			name: "MappedAccepted",
			src:  `val r: Uppercase<"abc"> = "ABC"`,
		},
		{
			name:    "UnmappedRejected",
			src:     `val r: Uppercase<"abc"> = "abc"`,
			wantErr: `cannot constrain "abc" <: "ABC"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			if tt.wantErr == "" {
				require.Empty(t, errs)
				return
			}
			require.Len(t, errs, 1)
			require.Equal(t, tt.wantErr, errs[0].Message())
		})
	}
}

// A string-intrinsic residual over the whole `string` primitive denotes the strings the transform
// leaves unchanged, since each of the four transforms is idempotent. A string literal satisfies the
// residual iff the transform maps it to itself: `Uppercase<string>` accepts `"A"` and rejects `"a"`,
// and Lowercase, Capitalize, and Uncapitalize accept and reject by the same fixed-point test.
func TestInferStringIntrinsicOverString(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string // "" ⇒ expect no error
	}{
		{
			name: "UppercaseAcceptsUppercase",
			src:  `val u: Uppercase<string> = "A"`,
		},
		{
			name:    "UppercaseRejectsLowercase",
			src:     `val u: Uppercase<string> = "a"`,
			wantErr: `cannot constrain "a" <: Uppercase<string>`,
		},
		{
			name: "LowercaseAcceptsLowercase",
			src:  `val l: Lowercase<string> = "a"`,
		},
		{
			name:    "LowercaseRejectsUppercase",
			src:     `val l: Lowercase<string> = "A"`,
			wantErr: `cannot constrain "A" <: Lowercase<string>`,
		},
		{
			name: "CapitalizeAcceptsLeadingUpper",
			src:  `val c: Capitalize<string> = "Abc"`,
		},
		{
			name:    "CapitalizeRejectsLeadingLower",
			src:     `val c: Capitalize<string> = "abc"`,
			wantErr: `cannot constrain "abc" <: Capitalize<string>`,
		},
		{
			name: "UncapitalizeAcceptsLeadingLower",
			src:  `val c: Uncapitalize<string> = "aBC"`,
		},
		{
			name:    "UncapitalizeRejectsLeadingUpper",
			src:     `val c: Uncapitalize<string> = "ABC"`,
			wantErr: `cannot constrain "ABC" <: Uncapitalize<string>`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			if tt.wantErr == "" {
				require.Empty(t, errs)
				return
			}
			require.Len(t, errs, 1)
			require.Equal(t, tt.wantErr, errs[0].Message())
		})
	}
}

// A string intrinsic nested inside a template interpolation composes: `on${Capitalize<K>}`
// over `type K = "click"` reduces the inner `Capitalize<K>` to `"Click"`, then folds it into the
// template to yield `"onClick"`. This is the `EventName<K>` shape the utility-type suite builds on.
func TestInferTemplateLitWithStringIntrinsic(t *testing.T) {
	nodes, ctx, errs := inferTypeNodes(t, `
		type K = "click"
		type Result = `+"`on${Capitalize<K>}`")
	require.Empty(t, errs)
	result := nodes["Result"]
	require.Equal(t, "`on${Capitalize<K>}`", soltype.Print(result))
	require.Equal(t, `"onClick"`, soltype.Print(expandResidual(ctx, result)))
}

// The four intrinsic string operators are built-in, not aliases, so a `type Uppercase<T> = …`
// declaration is rejected and the declaration never binds. A `Uppercase<…>` reference then still
// resolves to the built-in operator: `Uppercase<"abc">` reduces to `"ABC"`, so `"abc"` is rejected
// against it. Two errors surface — the reserved-name declaration and the failed constraint.
func TestInferStringIntrinsicCannotBeRedefined(t *testing.T) {
	_, _, errs := inferSource(t, `
		type Uppercase<T> = T
		val r: Uppercase<"abc"> = "abc"
	`)
	require.Len(t, errs, 2)
	require.Equal(t, `"Uppercase" is a built-in type operator and cannot be redefined`, errs[0].Message())
	require.Equal(t, `cannot constrain "abc" <: "ABC"`, errs[1].Message())
}

// A template literal whose cartesian product would exceed the combination cap is rejected with one
// diagnostic rather than materializing an unbounded union. A 25-member union across three
// interpolations enumerates 25^3 = 15625 combinations, past the 10000 cap.
func TestInferTemplateLitTooComplex(t *testing.T) {
	members := make([]string, 25)
	for i := range members {
		members[i] = fmt.Sprintf("%q", fmt.Sprint(i))
	}
	src := "type D = " + strings.Join(members, " | ") + "\nval r: `${D}${D}${D}` = \"000\""
	_, _, errs := inferSource(t, src)
	require.Len(t, errs, 1)
	require.Equal(t, "template literal type `${D}${D}${D}` is too complex to reduce; it expands to more than 10000 members", errs[0].Message())
}
