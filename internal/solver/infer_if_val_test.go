package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// --- M6 PR7: if-val / val-else refutable narrowing ---

// TestInferIfValAndValElse drives the refutable-binding forms through inferSource.
// Each case either infers a binding type, asserted against want, or reports errors,
// asserted in full against wantErrs. A type-annotated identifier pattern binds at the part
// of the union its annotation admits. Subsumption at finalization then drops a literal
// alternate such as 0 into a primitive sibling like number.
func TestInferIfValAndValElse(t *testing.T) {
	tests := []struct {
		name string
		src  string
		// binding is the name whose inferred type is checked; defaults to "f".
		binding string
		// want is the expected printed type of binding, checked when wantErrs is nil.
		want string
		// wantErrs, when non-nil, is the full set of error messages expected; the
		// binding type is not checked in that case.
		wantErrs []string
	}{
		{
			// The consequent binds x at number; the alternate's 0 is subsumed into it.
			name: "if-val narrows union to member",
			src: `fn f(u: number | string) {
				return if val x: number = u { x } else { 0 }
			}`,
			want: "fn (u: number | string) -> number",
		},
		{
			// A bare identifier pattern carries no annotation, so it binds the union.
			name: "if-val bare ident binds whole scrutinee",
			src: `fn f(u: number | string) {
				return if val x = u { x } else { 0 }
			}`,
			want: "fn (u: number | string) -> number | string",
		},
		{
			// A union annotation picks the matching sub-union.
			name: "if-val narrows to sub-union",
			src: `fn f(u: number | string | boolean) {
				return if val x: number | string = u { x } else { 0 }
			}`,
			want: "fn (u: number | string | boolean) -> number | string",
		},
		{
			// No else contributes `undefined` on the non-matching path.
			name: "if-val without else joins with undefined",
			src: `fn f(u: number | string) {
				return if val x: number = u { x }
			}`,
			want: "fn (u: number | string) -> number | undefined",
		},
		{
			// Narrowing binds a fresh x and never re-types the scrutinee, so both the
			// alternate and the code after the if-val read u at its full union type.
			// The `else { u }` flows u into r, exercising the alternate's view, and
			// `return u` exercises the continuation.
			name: "if-val leaves scrutinee type unchanged",
			src: `fn f(u: number | string) {
				val r = if val x: number = u { x } else { u }
				return u
			}`,
			want: "fn (u: number | string) -> number | string",
		},
		{
			// An annotation that is no member of the union has no branch to pick.
			name: "if-val narrow rejects non-member",
			src: `fn f(u: number | string) {
				return if val x: boolean = u { x } else { 0 }
			}`,
			wantErrs: []string{"2:22-2:29: cannot constrain boolean <: number | string"},
		},
		{
			// `mut {x: number}` picks the matching borrow branch; the write checks
			// against it and the scrutinee keeps its full borrow-union type.
			name: "if-val narrows borrow union for write",
			src: `fn f(p: &mut {x: number}, q: &mut {x: string}) {
				val r = if true { p } else { q }
				if val r2: mut {x: number} = r {
					r2.x = 5
				}
				return r
			}`,
			want: "fn <'a, 'b>(p: &'a mut {x: number}, q: &'b mut {x: string}) -> &'a mut {x: number} | &'b mut {x: string}",
		},
		{
			// r2 binds at mut {x: number}, so a string write to r2.x is rejected.
			name: "if-val narrowed write is type-checked",
			src: `fn f(p: &mut {x: number}, q: &mut {x: string}) {
				val r = if true { p } else { q }
				if val r2: mut {x: number} = r {
					r2.x = "hi"
				}
			}`,
			wantErrs: []string{
				"4:6-4:17: cannot constrain number <: string",
				"4:6-4:17: cannot constrain string <: number",
			},
		},
		{
			// The else diverges, so the body past it reads x at the narrowed type.
			name: "val-else narrows and binds for the rest of the block",
			src: `fn f(u: number | string) {
				val x: number = u else { return "no" }
				return x
			}`,
			want: `fn (u: number | string) -> number | "no"`,
		},
		{
			// The else runs in the enclosing scope, so it reads the outer `fallback`.
			name: "val-else else reads outer binding",
			src: `fn f(u: number | string, fallback: number) {
				val x: number = u else { return fallback }
				return x
			}`,
			want: "fn (u: number | string, fallback: number) -> number",
		},
		{
			// The else binds nothing of the pattern, so referencing x there fails.
			name: "val-else else cannot see the pattern binding",
			src: `fn f(u: number | string) {
				val x: number = u else { return x }
				return x
			}`,
			wantErrs: []string{"2:37-2:38: Unknown identifier: x"},
		},
		{
			// A structural pattern binds its leaves for the rest of the block.
			name: "val-else structural pattern binds leaves",
			src: `fn f(u: {x: number, y: string}) {
				val {x, y} = u else { return [0, ""] }
				return [x, y]
			}`,
			want: `fn (u: {x: number, y: string}) -> [number, string]`,
		},
		{
			// A decl-level annotation on a destructuring pattern would need the
			// annotation distributed across the leaves, which is unsupported.
			name: "val-else narrowing annotation on a destructuring pattern is unsupported",
			src: `fn f(u: [number, string]) {
				val [a, b]: [number, string] = u else { return }
				return a
			}`,
			wantErrs: []string{"2:9-2:15: Unsupported: narrowing type annotation on a destructuring pattern"},
		},
		{
			// A non-diverging else supplies a fallback. The annotation pins x to number,
			// and the fallback 0 fits, so x is number on both the match and no-match path.
			name: "val-else non-diverging else supplies a fallback",
			src: `fn f(u: number | string) {
				val x: number = u else { 0 }
				return x
			}`,
			want: "fn (u: number | string) -> number",
		},
		{
			// A non-diverging else's fallback must fit the annotated binding type.
			name: "val-else fallback must fit the annotation",
			src: `fn f(u: number | string) {
				val x: number = u else { "no" }
				return x
			}`,
			wantErrs: []string{`2:30-2:34: cannot constrain "no" <: number`},
		},
		{
			// With no annotation the binding's type joins the initializer with the
			// fallback. Subsumption then drops the literal 0 into number.
			name: "val-else unannotated joins init and fallback",
			src: `fn f(u: number | string) {
				val n = u else { 0 }
				return n
			}`,
			want: "fn (u: number | string) -> number | string",
		},
		{
			// A module-level val-else with a non-diverging else is a valid top-level
			// binding: the fallback gives num a value on the no-match path.
			name:    "val-else at module top level with a fallback binds",
			src:     "val u: number | string = 5\nval num: number = u else { 0 }",
			binding: "num",
			want:    "number",
		},
		{
			// A diverging else at module top level has no enclosing function to return
			// from, so its `return` is rejected.
			name:     "val-else at module top level with a diverging else is rejected",
			src:      "val u: number | string = 5\nval num: number = u else { return }",
			wantErrs: []string{"2:28-2:34: return can only be used inside a function"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			if tt.wantErrs != nil {
				require.Equal(t, tt.wantErrs, messagesWithSpan(errs))
				return
			}
			require.Empty(t, errs)
			binding := tt.binding
			if binding == "" {
				binding = "f"
			}
			require.Equal(t, tt.want, values[binding])
		})
	}
}

// --- The walk over the normalized form ---
//
// The cases below pin what routing `if val` and `val … else` through the UCS IR is
// responsible for: the scope each half runs in, the projections a nested pattern binds
// through, and typing each half once. The types and messages the two forms produce are
// pinned by TestInferIfValAndValElse above.

// A diagnostic from either form names the construct the user wrote. Lowering erases the
// difference between `match`, `if val`, and `val … else`, so without the origin the IR
// carries, a message about a failed pattern could name the wrong one. This is the golden
// test for that: every case blames a span inside the construct it came from, and none
// reaches for a `match`'s wording.
func TestIfValAndValElseDiagnosticsNameTheirConstruct(t *testing.T) {
	tests := map[string]struct {
		src     string
		want    string
		blame   string
		related []string
	}{
		// An annotation that is no member of the scrutinee's union underlines the
		// annotation the `if val` wrote.
		"IfValNarrowRejectsNonMember": {
			src:     `fn f(u: number | string) { return if val x: boolean = u { x } else { 0 } }`,
			want:    "1:45-1:52: cannot constrain boolean <: number | string",
			blame:   "boolean",
			related: []string{"number | string"},
		},
		// A fault inside the consequent blames the consequent, not the whole `if val`.
		"IfValConsequentFault": {
			src:   `fn f(u: number | string) { return if val x: number = u { x.nope } else { 0 } }`,
			want:  "1:58-1:64: cannot constrain number <: object",
			blame: "x.nope",
		},
		// A `val … else` names its `else`: the fallback that does not fit the annotated
		// binding underlines the value the `else` produced.
		"ValElseFallbackDoesNotFit": {
			src:     "fn f(u: number | string) {\n\tval x: number = u else { \"no\" }\n\treturn x\n}",
			want:    `2:27-2:31: cannot constrain "no" <: number`,
			blame:   `"no"`,
			related: []string{"number"},
		},
		// A name the `else` cannot see is reported against the `else`'s own reference.
		"ValElseCannotSeeTheBinding": {
			src:   "fn f(u: number | string) {\n\tval x: number = u else { return x }\n\treturn x\n}",
			want:  "2:34-2:35: Unknown identifier: x",
			blame: "x",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			requireBlame(t, tt.src, errs, tt.want, tt.blame, tt.related...)
			require.NotContains(t, errs[0].Message(), "match")
		})
	}
}

// Union narrowing applies to both refutable forms, at every tag-level rather than only
// the outermost. Each level's test picks the members it can destructure, so the leaf reads
// the field of the member the pattern matched. Without narrowing the leaf would read the
// field off both members and pick up the `undefined` the other one leaves.
func TestInferRefutableFormsNarrowUnions(t *testing.T) {
	tests := map[string]struct {
		src  string
		want string
	}{
		"IfValOuterLevel": {
			src:  `fn f(p: {x: number} | {y: string}) { return if val {x} = p { x } else { 0 } }`,
			want: "fn (p: {x: number} | {y: string}) -> number",
		},
		"IfValNestedLevel": {
			src:  `fn f(p: {a: {x: number}} | {a: {y: string}}) { return if val {a: {x}} = p { x } else { 0 } }`,
			want: "fn (p: {a: {x: number}} | {a: {y: string}}) -> number",
		},
		// A diverging `else` produces no value, so the declaration's leaves read only the
		// initializer and narrowing applies to them the same way.
		"ValElseWithADivergingElse": {
			src:  "fn f(p: {x: number} | {y: string}) {\n\tval {x} = p else { return 0 }\n\treturn x\n}",
			want: "fn (p: {x: number} | {y: string}) -> number",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, values["f"])
		})
	}
}

// A `val … else` whose `else` supplies a fallback binds its leaves off the initializer
// joined with that fallback, so the fallback has to satisfy the pattern too. No tag test
// ever admitted it, so nothing narrows the value the leaves read: narrowing to the member
// the pattern matched would leave the fallback unchecked and the leaves reading only the
// initializer's half.
func TestInferValElseChecksTheFallbackAgainstThePattern(t *testing.T) {
	tests := map[string]struct {
		src  string
		want string
	}{
		// The fallback is an object of the union's other shape, which the pattern cannot
		// destructure.
		"FallbackMissesAField": {
			src:  "fn f(p: {x: number} | {y: string}) {\n\tval {x} = p else { {y: 1} }\n\treturn x\n}",
			want: "2:21-2:27: object is missing property: x",
		},
		// The fallback is not an object at all.
		"FallbackIsNotAnObject": {
			src:  "fn f(p: {x: number} | {y: string}) {\n\tval {x} = p else { 5 }\n\treturn x\n}",
			want: "2:21-2:22: cannot constrain 5 <: object",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Len(t, errs, 1)
			require.Equal(t, tt.want, msgWithSpan(errs[0]))
		})
	}
}

// An annotation fixes the binding's type only where it narrows a bare identifier. One on a
// destructuring pattern is unsupported and pins nothing, so that declaration joins its
// fallback like an unannotated one: the fallback is checked against the pattern, and the
// leaves read it. Treating the unsupported annotation as a pin would leave the fallback
// checked against the initializer alone, which it satisfies, and infer `x: number` off a
// declaration that can bind no `x` at all.
func TestInferValElseJoinsPastAnUnsupportedAnnotation(t *testing.T) {
	values, _, errs := inferSource(t,
		"fn f(p: {x: number} | {y: string}) {\n\tval {x}: {x: number} = p else { {y: \"s\"} }\n\treturn x\n}")
	require.Equal(t, []string{
		"2:6-2:9: Unsupported: narrowing type annotation on a destructuring pattern",
		"2:34-2:42: object is missing property: x",
	}, messagesWithSpan(errs))
	require.Equal(t, "fn (p: {x: number} | {y: string}) -> number | undefined", values["f"])
}

// A fallback the pattern can destructure contributes its own leaf types, so the bound name
// reads either source rather than the initializer's alone. Three lower bounds reach `x`
// here: `number` and `"s"` from the two sources, and `undefined`.
//
// The `undefined` is constrainUnionFieldRead's doing, not the join's. Reading a property
// off a union joins what each member yields, and `{y: string}` carries no `x`, so that
// member contributes `undefined`. A plain `val {x} = p` over the same scrutinee infers
// `number | undefined` for the same reason. What the join decides is only that the union is
// still whole at the read: the fallback is a half no tag test admitted, so nothing narrows
// `{y: string}` away the way an `if val` over the same `p` does.
//
// That `undefined` is wrong, and #1076 removes it. A `p` holding `{y: string}` fails the
// `{x}` test and takes the `else`, so the leaf is never absent at run time. What this
// asserts is today's type rather than the intended one.
func TestInferValElseLeavesReadTheFallback(t *testing.T) {
	values, _, errs := inferSource(t, "fn f(p: {x: number} | {y: string}) {\n\tval {x} = p else { {x: \"s\"} }\n\treturn x\n}")
	require.Empty(t, errs)
	require.Equal(t, `fn (p: {x: number} | {y: string}) -> number | "s" | undefined`, values["f"])
}

// A nested pattern flattens into one split per tag-level, and each level's leaves bind off
// the projection the level above matched. The bound names read the nested field types, so
// the walk's projections agree with what one whole pattern would have bound.
func TestInferRefutableFormsBindThroughProjections(t *testing.T) {
	tests := map[string]struct {
		src  string
		want string
	}{
		"IfVal": {
			src:  `fn f(l: {start: {x: number, y: string}}) { return if val {start: {x, y}} = l { [x, y] } else { [0, ""] } }`,
			want: `fn (l: {start: {x: number, y: string}}) -> [number, string]`,
		},
		"ValElse": {
			src:  "fn f(l: {start: {x: number, y: string}}) {\n\tval {start: {x, y}} = l else { return [0, \"\"] }\n\treturn [x, y]\n}",
			want: `fn (l: {start: {x: number, y: string}}) -> [number, string]`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, values["f"])
		})
	}
}

// The names an `if val` binds are scoped to its consequent. The walk puts every bind in a
// child scope, so `x` is gone by the statement after the form.
func TestInferIfValBindingDoesNotEscape(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f(p: {x: number}) {
			if val {x} = p { x } else { 0 }
			return x
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "4:11-4:12: Unknown identifier: x", msgWithSpan(errs[0]))
}

// The target expression is inferred once, before the walk, and the pattern binds against
// that one type. The ill-typed argument below is the probe: inferring `g(2)` emits one
// constraint failure, so a walk that re-inferred the target would report it twice.
func TestInferRefutableFormsInferTheTargetOnce(t *testing.T) {
	tests := map[string]struct {
		src  string
		want string
	}{
		"IfVal": {
			src:  "fn g(s: string) { return {x: 1} }\nfn f() { return if val {x} = g(2) { x } else { 0 } }",
			want: "2:32-2:33: cannot constrain 2 <: string",
		},
		"ValElse": {
			src:  "fn g(s: string) { return {x: 1} }\nfn f() {\n\tval {x} = g(2) else { return 0 }\n\treturn x\n}",
			want: "3:14-3:15: cannot constrain 2 <: string",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Len(t, errs, 1)
			require.Equal(t, tt.want, msgWithSpan(errs[0]))
		})
	}
}

// A pattern that tests nothing always binds, so normalization drops the `else` below it as
// a path nothing reaches. Both forms type that `else` anyway, so a fault inside it is
// still reported rather than going unchecked until the pattern gains an annotation.
func TestInferRefutableFormsTypeAnUnreachableElse(t *testing.T) {
	tests := map[string]struct {
		src  string
		want string
	}{
		"IfVal": {
			src:  `fn f(u: number | string) { return if val x = u { x } else { nope } }`,
			want: "1:61-1:65: Unknown identifier: nope",
		},
		"ValElse": {
			src:  "fn f(u: number | string) {\n\tval n = u else { nope }\n\treturn n\n}",
			want: "2:19-2:23: Unknown identifier: nope",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Len(t, errs, 1)
			require.Equal(t, tt.want, msgWithSpan(errs[0]))
		})
	}
}

// A top-level annotation on a `match` arm's pattern narrows the scrutinee, the same way the
// annotation of an `if val x: number = u` does. The arm runs only for the values the
// annotation admits, so the arms below it stay reachable and bind the whole scrutinee.
func TestInferMatchArmAnnotationNarrows(t *testing.T) {
	tests := []struct {
		name string
		src  string
		// want is the printed type of `f`, checked when wantErrs is nil.
		want string
		// wantErrs, when non-nil, is the full set of expected messages.
		wantErrs []string
	}{
		{
			// The arm picks the `number` member of the union rather than asserting the whole
			// union fits `number`, and the catch-all below it is not reported unreachable.
			name: "an annotated arm narrows a union",
			src:  "fn f(u: number | string) {\n\treturn match u {\n\t\tx: number => x,\n\t\t_ => 0,\n\t}\n}",
			want: "fn (u: number | string) -> number",
		},
		{
			// Two annotated arms name the two members between them, so the arms cover the
			// union with no catch-all.
			name: "annotated arms cover a union",
			src:  "fn f(u: number | string) {\n\treturn match u {\n\t\tx: number => x,\n\t\ty: string => y,\n\t}\n}",
			want: "fn (u: number | string) -> number | string",
		},
		{
			// One annotated arm leaves the `string` member with no arm to run, so the arms
			// are not exhaustive. The span runs from `match` to the closing brace.
			name:     "one annotated arm leaves a member uncovered",
			src:      "fn f(u: number | string) {\n\treturn match u {\n\t\tx: number => x,\n\t}\n}",
			wantErrs: []string{"2:9-4:3: match is not exhaustive; add a catch-all branch"},
		},
		{
			// An annotation admitting the whole scrutinee covers it outright, so no arm below
			// is needed.
			name: "an arm annotated with the whole union covers it",
			src:  "fn f(u: number | string) {\n\treturn match u {\n\t\tx: number | string => x,\n\t}\n}",
			want: "fn (u: number | string) -> number | string",
		},
		{
			// The same annotation one line away in an `if val`. The arm above reads it the
			// same way, which is what makes one spelling mean one thing.
			name: "if val narrows the same union",
			src:  `fn f(u: number | string) { return if val x: number = u { x } else { 0 } }`,
			want: "fn (u: number | string) -> number",
		},
		{
			// The scrutinee holds no value the annotation admits, so the arm can never run and
			// the narrowing constraint fails. Two things in the message say it is a narrowing
			// failure rather than a failed assertion on the bound value. The direction is
			// `number <: string`, the annotation into the scrutinee, and the span is the
			// annotation rather than the value the arm binds.
			name:     "an annotation no member fits is rejected",
			src:      "fn f(s: string) {\n\treturn match s {\n\t\tx: number => x,\n\t}\n}",
			wantErrs: []string{"3:6-3:12: cannot constrain number <: string"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			if tt.wantErrs != nil {
				require.Equal(t, tt.wantErrs, messagesWithSpan(errs))
				return
			}
			require.Empty(t, errs)
			require.Equal(t, tt.want, values["f"])
		})
	}
}

// An arm below an unguarded catch-all never runs, so the walk leaves it out and
// inferMatchArms types it separately. A top-level annotation narrows there too, so the dead
// arm earns the one diagnostic naming it dead and no second one from its annotation.
func TestInferUnreachableArmAnnotationStillNarrows(t *testing.T) {
	tests := map[string]string{
		"BelowAWildcard":  "fn f(u: number | string) {\n\treturn match u {\n\t\t_ => 0,\n\t\tx: number => x,\n\t}\n}",
		"BelowABareIdent": "fn f(u: number | string) {\n\treturn match u {\n\t\tother => 0,\n\t\tx: number => x,\n\t}\n}",
	}
	want := "4:3-4:17: this match arm is unreachable because an arm above it matches every value; drop it, or move it above that arm"

	for name, src := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, src)
			require.Equal(t, []string{want}, messagesWithSpan(errs))
		})
	}
}

// A narrowing annotation picks out the part of the scrutinee it admits, so an annotation
// wider than a member still narrows to that member rather than being rejected. Every value of
// a `1 | 2` is a number, so `x: number` matches all of them and binds `x` at `1 | 2`. The
// annotation is not the type the name takes; the part of the scrutinee it admits is.
//
// The three forms share bindNarrowedIdent, so each accepts and rejects the same annotations.
// The `binding` column is the name whose inferred type is checked, since the two declaration
// forms below join their fallback value into what `f` returns and the arm's own type is the
// one worth reading.
func TestInferAnnotationWiderThanAMemberNarrowsToIt(t *testing.T) {
	tests := map[string]struct {
		src string
		// binding is the name whose printed type is checked.
		binding string
		// want is that name's printed type, checked when wantErr is empty.
		want string
		// wantErr, when non-empty, is the one message expected.
		wantErr string
	}{
		// The whole scrutinee fits the annotation, so the test is irrefutable and the arm
		// binds every member.
		"MatchArm": {
			src:     "fn f(u: 1 | 2) {\n\treturn match u {\n\t\tx: number => x,\n\t}\n}",
			binding: "f",
			want:    "fn (u: 1 | 2) -> 1 | 2",
		},
		"IfVal": {
			src:     `fn f(u: 1 | 2) { return if val x: number = u { x } else { 0 } }`,
			binding: "f",
			want:    "fn (u: 1 | 2) -> 0 | 1 | 2",
		},
		"ValElse": {
			src:     "fn f(u: 1 | 2) {\n\tval x: number = u else { return 0 }\n\treturn x\n}",
			binding: "f",
			want:    "fn (u: 1 | 2) -> 0 | 1 | 2",
		},
		// `unknown` is the top of the lattice, so it admits every value of any scrutinee.
		"Unknown": {
			src:     "fn f(u: number | string) {\n\treturn match u {\n\t\tx: unknown => x,\n\t}\n}",
			binding: "f",
			want:    "fn (u: number | string) -> number | string",
		},
		// Only some members fit, so the arm binds those and the arm below it takes the rest.
		"WiderThanSomeMembers": {
			src:     "fn f(u: 1 | 2 | string) {\n\treturn match u {\n\t\tx: number => x,\n\t\ts: string => s,\n\t}\n}",
			binding: "f",
			want:    "fn (u: string | 1 | 2) -> string | 1 | 2",
		},
		// A transparent alias carries the alias rather than the union it stands for, so the
		// members are reached by expanding it first. Without that the annotation would be
		// measured against the alias as a single shape and admit nothing.
		"AliasedUnion": {
			src:     "type U = 1 | 2 | string\nfn f(u: U) {\n\treturn match u {\n\t\tx: number => x,\n\t\ts: string => 0,\n\t}\n}",
			binding: "f",
			want:    "fn (u: U) -> 0 | 1 | 2",
		},
		// An annotation narrower than every member admits none of them, so the union-super
		// exists rule decides and the name takes the annotation itself.
		"NarrowerThanEveryMember": {
			src:     `fn f(u: number | string) { return if val x: 1 = u { x } else { 0 } }`,
			binding: "f",
			want:    "fn (u: number | string) -> 0 | 1",
		},
		// No value of the scrutinee is a number and no member holds one, so the annotation
		// picks out nothing and the arm can never run.
		"AdmitsNothing": {
			src:     "fn f(s: string) {\n\treturn match s {\n\t\tx: number => x,\n\t}\n}",
			binding: "f",
			wantErr: "3:6-3:12: cannot constrain number <: string",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			if tt.wantErr != "" {
				require.Equal(t, []string{tt.wantErr}, messagesWithSpan(errs))
				return
			}
			require.Empty(t, errs)
			require.Equal(t, tt.want, values[tt.binding])
		})
	}
}

// A type annotation on a pattern leaf asserts rather than narrows, which is the opposite of
// what the same syntax does on a whole binding. A top-level `if val x: number = u` picks the
// member of `u` the annotation names, so a `u` that is a `string` takes the `else`. A leaf's
// annotation names no tag the branch tests: the leaf binds at the annotated type and the
// projection has to fit it, so `[a: string, …]` over `[number, …]` is rejected outright
// rather than sending control to the `else`.
//
// The three nested spellings each hang the annotation off a different node — a tuple
// element and an object key-value's value are `IdentPat.TypeAnn`, an object shorthand is
// `ObjShorthandPat.TypeAnn` written with `::` — and all three reach the same rule.
func TestInferNestedLeafAnnotations(t *testing.T) {
	tests := []struct {
		name string
		src  string
		// want is the printed type of `f`, checked when wantErrs is nil.
		want string
		// wantErrs, when non-nil, is the full set of expected messages.
		wantErrs []string
	}{
		{
			name: "match arm tuple leaves",
			src:  "fn f(p: [number, string]) { return match p {\n\t[a: number, b: string] => [a, b],\n} }",
			want: "fn (p: [number, string]) -> [number, string]",
		},
		{
			name: "if val tuple leaves",
			src:  `fn f(p: [number, string]) { return if val [a: number, b: string] = p { [a, b] } else { [0, ""] } }`,
			want: "fn (p: [number, string]) -> [number, string]",
		},
		{
			name: "val else tuple leaves",
			src:  "fn f(p: [number, string]) {\n\tval [a: number, b: string] = p else { return [0, \"\"] }\n\treturn [a, b]\n}",
			want: "fn (p: [number, string]) -> [number, string]",
		},
		{
			name: "if val object key-value leaves",
			src:  `fn f(p: {a: number, b: string}) { return if val {a: x: number, b: y: string} = p { [x, y] } else { [0, ""] } }`,
			want: "fn (p: {a: number, b: string}) -> [number, string]",
		},
		{
			name: "if val object shorthand leaves",
			src:  `fn f(p: {a: number, b: string}) { return if val {a::number, b::string} = p { [a, b] } else { [0, ""] } }`,
			want: "fn (p: {a: number, b: string}) -> [number, string]",
		},
		{
			name: "val else object shorthand leaf",
			src:  "fn f(p: {a: number, b: string}) {\n\tval {a::number} = p else { return 0 }\n\treturn a\n}",
			want: "fn (p: {a: number, b: string}) -> number",
		},
		{
			// The leaf binds at the annotation, so a wider one widens the name. A `5` element
			// read through `a: number` binds `a` at `number` rather than at the literal.
			name: "a leaf annotation widens the name",
			src:  `fn f(p: [5, string]) { return if val [a: number, b: string] = p { a } else { 0 } }`,
			want: "fn (p: [5, string]) -> number",
		},
		{
			// The element is a `number` and the annotation says `string`, so the projection
			// does not fit. Control does not fall to the `else` the way a failed tag test
			// would send it.
			name:     "if val tuple leaf annotation must fit",
			src:      `fn f(p: [number, string]) { return if val [a: string, b: string] = p { [a, b] } else { ["", ""] } }`,
			wantErrs: []string{"1:44-1:53: cannot constrain number <: string"},
		},
		{
			name:     "val else tuple leaf annotation must fit",
			src:      "fn f(p: [number, string]) {\n\tval [a: string, b: string] = p else { return [\"\", \"\"] }\n\treturn [a, b]\n}",
			wantErrs: []string{"2:7-2:16: cannot constrain number <: string"},
		},
		{
			name:     "if val object shorthand annotation must fit",
			src:      `fn f(p: {a: number, b: string}) { return if val {a::string} = p { a } else { "" } }`,
			wantErrs: []string{"1:50-1:59: cannot constrain number <: string"},
		},
		{
			name:     "if val object key-value annotation must fit",
			src:      `fn f(p: {a: number, b: string}) { return if val {a: x: string} = p { x } else { "" } }`,
			wantErrs: []string{"1:53-1:62: cannot constrain number <: string"},
		},
		{
			// The branch's own tag still narrows the scrutinee, so the leaf's annotation is
			// checked against the member the object test picked rather than the whole union.
			name: "a leaf annotation reads the narrowed member",
			src:  `fn f(p: {a: number} | {b: string}) { return if val {a: x: number} = p { x } else { 0 } }`,
			want: "fn (p: {a: number} | {b: string}) -> number",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			if tt.wantErrs != nil {
				require.Equal(t, tt.wantErrs, messagesWithSpan(errs))
				return
			}
			require.Empty(t, errs)
			require.Equal(t, tt.want, values["f"])
		})
	}
}
