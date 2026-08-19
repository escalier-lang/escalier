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

// templateMatchesString decides a string literal against a template pattern character by character,
// matching each quasi in order and letting each interpolation consume a span its type admits. It is
// the rule behind `"onb" <: `on${string}``, and the cases below drive every interpolation kind it
// reads: a literal that must appear verbatim, the `string` primitive that consumes any span, a union
// that matches when a member does, and the kinds it leaves undecided — a non-string primitive and a
// bare type variable — which fail the match rather than guess.
func TestTemplateMatchesString(t *testing.T) {
	tests := []struct {
		name    string
		s       string
		quasis  []string
		interps []soltype.Type
		want    bool
	}{
		{"no quasis is not a template", "x", nil, nil, false},
		{"a bare segment matches exactly", "hi", []string{"hi"}, nil, true},
		{"a bare segment rejects a longer string", "hix", []string{"hi"}, nil, false},
		{"a string interp takes any middle", "onb", []string{"on", ""}, []soltype.Type{str()}, true},
		{"a string interp needs the prefix", "xyz", []string{"on", ""}, []soltype.Type{str()}, false},
		{"a string interp allows an empty middle", "on", []string{"on", ""}, []soltype.Type{str()}, true},
		{"a string interp matches up to a trailing quasi", "onbz", []string{"on", "z"}, []soltype.Type{str()}, true},
		{"a literal interp matches its own text", "onax", []string{"on", "x"}, []soltype.Type{strLit("a")}, true},
		{"a literal interp rejects other text", "onbx", []string{"on", "x"}, []soltype.Type{strLit("a")}, false},
		{"a non-string literal interp is left undecided", "on5", []string{"on", ""}, []soltype.Type{numLit(5)}, false},
		{"a number interp is left undecided", "n5", []string{"n", ""}, []soltype.Type{num()}, false},
		{"a union interp matches through its string member", "onb", []string{"on", ""}, []soltype.Type{newUnion(nil, []soltype.Type{strLit("a"), str()}, false)}, true},
		{"a union interp matches a literal member", "onb", []string{"on", ""}, []soltype.Type{newUnion(nil, []soltype.Type{strLit("a"), strLit("b")}, false)}, true},
		{"a union interp rejects a non-member", "onc", []string{"on", ""}, []soltype.Type{newUnion(nil, []soltype.Type{strLit("a"), strLit("b")}, false)}, false},
		{"a type-variable interp is left undecided", "onx", []string{"on", ""}, []soltype.Type{&soltype.TypeVarType{ID: 1, Level: 1}}, false},
		{"two string interps backtrack to a split", "a-b", []string{"", "-", ""}, []soltype.Type{str(), str()}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, templateMatchesString(tt.s, tt.quasis, tt.interps))
		})
	}
}

// A template with two open interpolations builds a tail bound where each interp ranges over its named
// choices and its bound, since a tail string may pair one interp's named choice with the other's
// bound. `${keyof Obj}-${keyof Obj}` over `{a: number, ...}` keeps the named "a-a" and bounds the tail
// by the three combinations that draw at least one side from `string`.
func TestTemplateOverTwoOpenInterpsBoundsEachSide(t *testing.T) {
	nodes, ctx, errs := inferTypeNodes(t, `
		type Obj = {a: number, ...}
		type Result = `+"`${keyof Obj}-${keyof Obj}`"+`
	`)
	require.Empty(t, errs)
	result := expandAliasResidual(ctx, nodes["Result"])
	require.Equal(t, "\"a-a\" | ... : (`${string}-${string}` | `${string}-a` | `a-${string}`)", soltype.Print(result))

	require.True(t, subtypeHolds(ctx, strLit("a-a"), result), "the named pairing is a member")
	require.True(t, subtypeHolds(ctx, strLit("b-c"), result), "so is a pairing the tail bounds")
	require.True(t, subtypeHolds(ctx, strLit("b-a"), result), "and one pairing a name with a bound string")
	require.False(t, subtypeHolds(ctx, strLit("xyz"), result), "a string the template cannot spell is not")
}
