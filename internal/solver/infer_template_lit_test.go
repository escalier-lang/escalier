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
		{
			// A `number` placeholder admits a numeric span, so `"id-5"` conforms
			// (escalier-lang/escalier#1153).
			name: "NumberPlaceholderAccepted",
			src:  "val r: `id-${number}` = \"id-5\"",
		},
		{
			// A non-numeric span in the placeholder position is rejected. The template stays a
			// residual, so the diagnostic names it rather than an enumerated union.
			name:    "NumberPlaceholderRejected",
			src:     "val r: `id-${number}` = \"id-x\"",
			wantErr: "cannot constrain \"id-x\" <: `id-${number}`",
		},
		{
			// A `boolean` placeholder admits `true` and `false`.
			name: "BooleanPlaceholderAccepted",
			src:  "val r: `flag-${boolean}` = \"flag-true\"",
		},
		{
			name:    "BooleanPlaceholderRejected",
			src:     "val r: `flag-${boolean}` = \"flag-maybe\"",
			wantErr: "cannot constrain \"flag-maybe\" <: `flag-${boolean}`",
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

// A template whose interpolation is a residual intrinsic over `string`, such as
// `on${Uppercase<string>}`, stays symbolic and is decided by templateMatchesString. A literal
// satisfies it iff its interpolated span is a fixed point of the transform. Each of the four
// intrinsics is idempotent, so all decide by the same fixed-point test: `"onA"` and `"on5"` hold
// against Uppercase because it leaves them unchanged, while `"onb"` is rejected because Uppercase
// maps it to `"onB"`.
func TestInferTemplateLitOverStringIntrinsic(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string // "" ⇒ expect no error
	}{
		{
			name: "UppercaseSpanAccepted",
			src:  "val u: `on${Uppercase<string>}` = \"onA\"",
		},
		{
			name: "CaselessSpanAccepted",
			src:  "val u: `on${Uppercase<string>}` = \"on5\"",
		},
		{
			name:    "UppercaseLowercaseSpanRejected",
			src:     "val u: `on${Uppercase<string>}` = \"onb\"",
			wantErr: "cannot constrain \"onb\" <: `on${Uppercase<string>}`",
		},
		{
			name: "LowercaseSpanAccepted",
			src:  "val u: `on${Lowercase<string>}` = \"onb\"",
		},
		{
			name:    "LowercaseUppercaseSpanRejected",
			src:     "val u: `on${Lowercase<string>}` = \"onA\"",
			wantErr: "cannot constrain \"onA\" <: `on${Lowercase<string>}`",
		},
		{
			name: "CapitalizeSpanAccepted",
			src:  "val u: `on${Capitalize<string>}` = \"onAbc\"",
		},
		{
			name:    "CapitalizeLeadingLowerRejected",
			src:     "val u: `on${Capitalize<string>}` = \"onaBC\"",
			wantErr: "cannot constrain \"onaBC\" <: `on${Capitalize<string>}`",
		},
		{
			name: "UncapitalizeSpanAccepted",
			src:  "val u: `on${Uncapitalize<string>}` = \"onaBC\"",
		},
		{
			name:    "UncapitalizeLeadingUpperRejected",
			src:     "val u: `on${Uncapitalize<string>}` = \"onAbc\"",
			wantErr: "cannot constrain \"onAbc\" <: `on${Uncapitalize<string>}`",
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

// templateMatchesString decides a string literal against a template literal type. Each case drives a
// `val x: <template> = <literal>` binding through inferSource, so the whole reduce-and-constrain path
// runs, and its `wantErr` is empty when the assignment type-checks and the reported message when it
// does not. The cases cover every interpolation kind the matcher reads: the `string`, `number`, and
// `boolean` primitives, a union, an intersection carrying a complement, and a residual string
// intrinsic, alongside a literal and a bare segment that fold during reduction before the matcher sees
// them.
func TestTemplateMatchesString(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string // "" ⇒ expect no error
	}{
		{"a bare segment matches exactly", "val x: `hi` = \"hi\"", ""},
		{"a bare segment rejects a longer string", "val x: `hi` = \"hix\"", "cannot constrain \"hix\" <: \"hi\""},
		{"a string interp takes any middle", "val x: `on${string}` = \"onb\"", ""},
		{"a string interp needs the prefix", "val x: `on${string}` = \"xyz\"", "cannot constrain \"xyz\" <: `on${string}`"},
		{"a string interp allows an empty middle", "val x: `on${string}` = \"on\"", ""},
		{"a string interp matches up to a trailing quasi", "val x: `on${string}z` = \"onbz\"", ""},
		// A bare literal or numeric interpolation folds into the surrounding text during reduction,
		// so its rejection names the folded string rather than the template.
		{"a literal interp folds and matches its text", "val x: `on${\"a\"}x` = \"onax\"", ""},
		{"a literal interp folds and rejects other text", "val x: `on${\"a\"}x` = \"onbx\"", "cannot constrain \"onbx\" <: \"onax\""},
		{"a numeric literal interp folds to its rendered text", "val x: `on${5}` = \"on5\"", ""},
		// A primitive, intrinsic, or complement interpolation keeps the template symbolic, so the
		// matcher decides the span and a rejection names the template.
		{"a number interp matches a numeric span", "val x: `n${number}` = \"n5\"", ""},
		{"a number interp rejects a non-numeric span", "val x: `n${number}` = \"nx\"", "cannot constrain \"nx\" <: `n${number}`"},
		{"a boolean interp matches a boolean word", "val x: `on-${boolean}` = \"on-true\"", ""},
		{"a boolean interp rejects another word", "val x: `on-${boolean}` = \"on-maybe\"", "cannot constrain \"on-maybe\" <: `on-${boolean}`"},
		{"a union interp matches through its string member", "val x: `on${\"a\" | string}` = \"onb\"", ""},
		{"a union interp folds and matches a literal member", "val x: `on${\"a\" | \"b\"}` = \"onb\"", ""},
		{"a union interp folds and rejects a non-member", "val x: `on${\"a\" | \"b\"}` = \"onc\"", "cannot constrain \"onc\" <: \"ona\" | \"onb\""},
		{"an intersection with a complement matches an allowed span", "val x: `on${string & ¬\"a\"}` = \"onb\"", ""},
		{"an intersection with a complement rejects the excluded span", "val x: `on${string & ¬\"a\"}` = \"ona\"", "cannot constrain \"ona\" <: `on${string & ¬\"a\"}`"},
		{"an intrinsic interp admits a fixed-point span", "val x: `on${Uppercase<string>}` = \"onA\"", ""},
		{"an intrinsic interp rejects a non-fixed-point span", "val x: `on${Uppercase<string>}` = \"onb\"", "cannot constrain \"onb\" <: `on${Uppercase<string>}`"},
		{"two string interps split at the delimiter", "val x: `${string}-${string}` = \"a-b\"", ""},
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

// An interpolation the matcher cannot decide, a bare type parameter or an intrinsic over one, admits
// no span, so templateMatchesString fails the match rather than guesses. Such an interpolation keeps
// the whole template symbolic, so no `val` binding ever grounds it to reach the matcher; these cases
// call templateMatchesString directly.
func TestTemplateMatcherRejectsAbstractInterp(t *testing.T) {
	typeVar := &soltype.TypeVarType{ID: 1, Level: 1}
	tests := []struct {
		name   string
		interp soltype.Type
	}{
		{"a bare type variable", typeVar},
		{"an intrinsic over a type variable", &soltype.StringIntrinsicType{Kind: soltype.Uppercase, Operand: typeVar}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.False(t, templateMatchesString("onx", []string{"on", ""}, []soltype.Type{tt.interp}))
		})
	}
}

// A `${number}` placeholder is decided by JavaScript's Number() coercion: a span conforms when it is
// non-empty and Number(span) is finite. Each case drives a `val x: <template> = <literal>` binding, so
// the whole matcher runs, and its `wantErr` is the verdict tsc 5.8 gives for that assignment, empty
// when it type-checks and the reported message when it does not. The table pins Escalier to TypeScript
// across the number grammar and the multidigit, greedy, and multi-placeholder cases.
//
// The grammar admits more than a `number` value's own `String()` form: a hex, octal, or binary
// literal, scientific notation, a leading-zero run, a bare or trailing fraction, and a signed value all
// conform, since Number() coerces each to a finite number. `Infinity` and a non-numeric span do not. A
// number placeholder consumes greedily up to what follows it, so `${number}0` reads "100" as the
// number "10" and the literal "0".
func TestNumberPlaceholderMatchesTypeScript(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string // "" ⇒ expect no error
	}{
		{"multidigit integer", "val x: `${number}` = \"1000000\"", ""},
		{"leading zeros", "val x: `${number}` = \"007\"", ""},
		{"decimal", "val x: `${number}` = \"12.34\"", ""},
		{"bare fraction", "val x: `${number}` = \".5\"", ""},
		{"trailing dot", "val x: `${number}` = \"1.\"", ""},
		{"negative multidigit", "val x: `${number}` = \"-12\"", ""},
		{"negative zero", "val x: `${number}` = \"-0\"", ""},
		{"leading plus", "val x: `${number}` = \"+5\"", ""},
		{"scientific", "val x: `${number}` = \"1e10\"", ""},
		{"scientific signed exponent", "val x: `${number}` = \"1.5e-3\"", ""},
		{"hex literal", "val x: `${number}` = \"0x1f\"", ""},
		{"octal literal", "val x: `${number}` = \"0o17\"", ""},
		{"binary literal", "val x: `${number}` = \"0b101\"", ""},
		{"infinity rejected", "val x: `${number}` = \"Infinity\"", "cannot constrain \"Infinity\" <: `${number}`"},
		{"dotted non-number rejected", "val x: `${number}` = \"1.2.3\"", "cannot constrain \"1.2.3\" <: `${number}`"},
		{"trailing letters rejected", "val x: `${number}` = \"1px\"", "cannot constrain \"1px\" <: `${number}`"},
		{"empty span rejected", "val x: `${number}` = \"\"", "cannot constrain \"\" <: `${number}`"},
		// A digit quasi after the placeholder: the number consumes greedily up to the final "0".
		{"greedy up to a digit suffix", "val x: `${number}0` = \"100\"", ""},
		{"digit suffix must be present", "val x: `${number}0` = \"12\"", "cannot constrain \"12\" <: `${number}0`"},
		// A unit suffix quasi.
		{"multidigit before a unit suffix", "val x: `${number}px` = \"120px\"", ""},
		// Numbers separated by fixed text, the common multi-placeholder shape.
		{"dash-separated pair", "val x: `${number}-${number}` = \"12-34\"", ""},
		{"dash pair needs the dash", "val x: `${number}-${number}` = \"1234\"", "cannot constrain \"1234\" <: `${number}-${number}`"},
		{"semver triple", "val x: `v${number}.${number}.${number}` = \"v10.20.30\"", ""},
		// Adjacent placeholders with no separator. The earlier number takes exactly one character,
		// so a split a later boundary would allow is not reached: "12" reads as "1" and "2", while
		// "-12" reads as "-" and "12" and is rejected because "-" is no number.
		{"adjacent numbers take one digit each", "val x: `${number}${number}` = \"12\"", ""},
		{"adjacent numbers reject a leading sign", "val x: `${number}${number}` = \"-12\"", "cannot constrain \"-12\" <: `${number}${number}`"},
		{"adjacent numbers reject an interior dash", "val x: `${number}${number}` = \"12-34\"", "cannot constrain \"12-34\" <: `${number}${number}`"},
		{"adjacent numbers keep a sign on the second", "val x: `${number}${number}` = \"1-2\"", ""},
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

// Escalier's `${number}` parts from TypeScript on whitespace. TypeScript decides the placeholder by
// JavaScript's Number(), which strips surrounding whitespace and reads a whitespace-only span as 0, so
// it admits "  12  " and "   ". A number's textual form carries no whitespace, and a whitespace-only
// string is not a number, so `${number}` rejects any span with whitespace rather than mimic that
// coercion. These are the spans where the two disagree; each is a number to TypeScript and is rejected
// here.
func TestNumberPlaceholderRejectsWhitespace(t *testing.T) {
	numQuasis, numInterps := []string{"", ""}, []soltype.Type{num()}
	for _, s := range []string{"  12  ", " 12", "12 ", "   ", " ", "\t5", "5\n"} {
		t.Run(fmt.Sprintf("rejects %q", s), func(t *testing.T) {
			require.False(t, templateMatchesString(s, numQuasis, numInterps))
		})
	}
}

// A template with two `string` interpolations stays open on both sides. `keyof Obj` over an
// inexact `{a: number, ...}` reduces to `string`, so `${keyof Obj}-${keyof Obj}` reduces to the
// open template `${string}-${string}`, which admits any string the template can spell.
func TestTemplateOverTwoOpenInterps(t *testing.T) {
	nodes, ctx, errs := inferTypeNodes(t, `
		type Obj = {a: number, ...}
		type Result = `+"`${keyof Obj}-${keyof Obj}`"+`
	`)
	require.Empty(t, errs)
	result := expandAliasResidual(ctx, nodes["Result"])
	require.Equal(t, "`${string}-${string}`", soltype.Print(result))

	require.True(t, subtypeHolds(ctx, strLit("a-a"), result), "a dashed pairing is a member")
	require.True(t, subtypeHolds(ctx, strLit("b-c"), result), "so is any dashed pairing of strings")
	require.False(t, subtypeHolds(ctx, strLit("xyz"), result), "a string the template cannot spell is not")
}
