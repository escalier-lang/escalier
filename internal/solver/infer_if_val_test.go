package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dep_graph"
	"github.com/escalier-lang/escalier/internal/soltype"
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
			name: "val-else at module top level with a fallback binds",
			src: `val u: number | string = 5
				val num: number = u else { 0 }`,
			binding: "num",
			want:    "number",
		},
		{
			// A diverging else at module top level has no enclosing function to return
			// from, so its `return` is rejected.
			name: "val-else at module top level with a diverging else is rejected",
			src: `val u: number | string = 5
				val num: number = u else { return }`,
			wantErrs: []string{"2:32-2:38: return can only be used inside a function"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			if tt.wantErrs != nil {
				require.Equal(t, tt.wantErrs, messagesWithSpan(t, errs))
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
			src: `fn f(u: number | string) {
					val x: number = u else { "no" }
					return x
				}`,
			want:    `2:31-2:35: cannot constrain "no" <: number`,
			blame:   `"no"`,
			related: []string{"number"},
		},
		// A name the `else` cannot see is reported against the `else`'s own reference.
		"ValElseCannotSeeTheBinding": {
			src: `fn f(u: number | string) {
					val x: number = u else { return x }
					return x
				}`,
			want:  "2:38-2:39: Unknown identifier: x",
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
			src: `fn f(p: {x: number} | {y: string}) {
					val {x} = p else { return 0 }
					return x
				}`,
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

// A `val … else` whose `else` supplies a fallback destructures that fallback with the same
// pattern, so a fallback the pattern cannot take apart is rejected. Each leaf of the
// declaration reads the initializer's leaf joined with the fallback's, so a fallback that
// binds no such leaf leaves the name with nothing to read from that path.
//
// Each message underlines the value the `else` produced rather than the pattern leaf that
// could not take it apart. That is the narrowest span naming something the user can change,
// since the pattern is what the rest of the block reads.
func TestInferValElseChecksTheFallbackAgainstThePattern(t *testing.T) {
	tests := map[string]struct {
		src  string
		want string
	}{
		// The fallback is an object of the union's other shape, which the pattern cannot
		// destructure.
		"FallbackMissesAField": {
			src: `fn f(p: {x: number} | {y: string}) {
					val {x} = p else { {y: 1} }
					return x
				}`,
			want: "2:25-2:31: object is missing property: x",
		},
		// The fallback is not an object at all.
		"FallbackIsNotAnObject": {
			src: `fn f(p: {x: number} | {y: string}) {
					val {x} = p else { 5 }
					return x
				}`,
			want: "2:25-2:26: cannot constrain 5 <: object",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Len(t, errs, 1)
			require.Equal(t, tt.want, msgWithSpan(t, errs[0]))
		})
	}
}

// An annotation fixes the binding's type only where it narrows a bare identifier. One on a
// destructuring pattern is unsupported and pins nothing, so that declaration destructures
// its fallback like an unannotated one and reports the leaf the fallback cannot supply.
// Treating the unsupported annotation as a pin would check the fallback against the
// initializer alone, which it satisfies, and infer `x: number` off a declaration that can
// bind no `x` at all.
func TestInferValElseJoinsPastAnUnsupportedAnnotation(t *testing.T) {
	values, _, errs := inferSource(t,
		`fn f(p: {x: number} | {y: string}) {
				val {x}: {x: number} = p else { {y: "s"} }
				return x
			}`)
	require.Equal(t, []string{
		"2:9-2:12: Unsupported: narrowing type annotation on a destructuring pattern",
		"2:37-2:45: object is missing property: x",
	}, messagesWithSpan(t, errs))
	// The fallback carries no `x`, so the only lower bound reaching the name is the one the
	// narrowed initializer projected.
	require.Equal(t, "fn (p: {x: number} | {y: string}) -> number", values["f"])
}

// A fallback the pattern can destructure contributes its own leaf types, so each bound name
// reads either source rather than the initializer's alone. Two lower bounds reach `x` in
// both cases below, one per source.
//
// Neither picks up an `undefined` from the scrutinee member that carries no `x`. The join
// sits below the projection, so the tag test the pattern makes narrows the initializer
// before any leaf reads it, and the member constrainUnionFieldRead would answer `undefined`
// for is gone by then. A `p` holding that member fails the test and takes the `else`, so
// the name is never absent at run time.
func TestInferValElseLeavesReadTheFallback(t *testing.T) {
	tests := map[string]struct {
		src  string
		want string
	}{
		"OuterLevel": {
			src: `fn f(p: {x: number} | {y: string}) {
					val {x} = p else { {x: "s"} }
					return x
				}`,
			want: `fn (p: {x: number} | {y: string}) -> number | "s"`,
		},
		// A nested pattern flattens into one split per tag-level and every level narrows, so
		// the leaf below them joins the same two sources.
		"NestedLevel": {
			src: `fn f(p: {a: {x: number}} | {a: {y: string}}) {
					val {a: {x}} = p else { {a: {x: "s"}} }
					return x
				}`,
			want: `fn (p: {a: {x: number}} | {a: {y: string}}) -> number | "s"`,
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

// The pattern of a `val … else` is walked twice, once binding off the narrowed initializer
// and once projecting the fallback the `else` produced. The join is recorded over what the
// binding walk left, so what an editor reads for `v` is what the name binds at rather than
// the initializer's half alone.
func TestInferValElseRecordsTheJoinedLeafType(t *testing.T) {
	module := parseModule(t, `fn f(p: {x: number} | {y: string}) {
			val {x: v} = p else { {x: "s"} }
			return v
		}`)
	c := newChecker()
	c.inferDepGraph(sharedPrelude().Child(), 0, module, dep_graph.BuildDepGraph(module))
	require.Empty(t, c.errs)
	leaf := findIdentPat(module, "v")
	require.NotNil(t, leaf)
	require.Equal(t, `number | "s"`, soltype.Print(coalesce(c.info.TypeOf(leaf), soltype.Positive)))
}

// The second walk over the pattern projects rather than binds, so a leaf's default and
// annotation are typed once even though the pattern is walked twice. A fault inside a
// default is reported once, and the default's expression is inferred once.
func TestInferValElseTypesALeafDefaultOnce(t *testing.T) {
	_, _, errs := inferSource(t, `fn f(p: {x: number} | {y: string}) {
			val {x = nope} = p else { {x: 5} }
			return x
		}`)
	require.Equal(t, []string{"2:13-2:17: Unknown identifier: nope"}, messagesWithSpan(t, errs))
}

// A `mut` leaf of an owned scrutinee is an owned-mutable cell, and the fallback flows into
// the cell's contents rather than joining beside it. The name stays one cell, so the write
// below it checks against the shape the cell holds.
func TestInferValElseJoinsInsideAMutLeaf(t *testing.T) {
	values, _, errs := inferSource(t, `fn f(p: {x: {a: number}} | {y: string}) {
			val {mut x} = p else { {x: {a: 1}} }
			x.a = 2
			return x
		}`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: {x: {a: number}} | {y: string}) -> mut {a: number}", values["f"])
}

// A leaf's own type annotation fixes what the name binds at, so the fallback has to fit it
// rather than widening the name to a union with it. The message underlines the value the
// `else` produced.
func TestInferValElseChecksTheFallbackAgainstALeafAnnotation(t *testing.T) {
	values, _, errs := inferSource(t, `fn f(p: {x: number} | {y: string}) {
			val {x::number} = p else { {x: "s"} }
			return x
		}`)
	require.Equal(t, []string{`2:35-2:38: cannot constrain "s" <: number`}, messagesWithSpan(t, errs))
	require.Equal(t, "fn (p: {x: number} | {y: string}) -> number", values["f"])
}

// A leaf's annotation fixes its type even where the scrutinee is a union of borrows, so the
// fallback fits the annotation rather than widening the name beside it. Such a leaf is no
// borrow itself. concreteLeaf drops the shape hint for an annotated leaf, and applyBindMode
// wraps a leaf in a borrow only where that hint says the value is borrowable.
//
// The scrutinee itself checks clean. Its members are peeled per member, so the leaves project
// out of owned members and the borrow rides the binding mode.
func TestInferValElseChecksAnAnnotatedLeafOfABorrowedUnion(t *testing.T) {
	tests := map[string]struct {
		src string
		// wantErrs is the full set of expected messages, and nil means the source checks clean.
		wantErrs []string
	}{
		"FallbackFitsTheAnnotation": {
			src: `fn f(p: &{x: number} | &{y: string}) {
					val {x: v: number} = p else { {x: 5} }
					return 0
				}`,
		},
		"FallbackViolatesTheAnnotation": {
			src: `fn f(p: &{x: number} | &{y: string}) {
					val {x: v: number} = p else { {x: "s"} }
					return 0
				}`,
			wantErrs: []string{
				`2:40-2:43: cannot constrain "s" <: number`,
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			module := parseModule(t, tt.src)
			c := newChecker()
			c.inferDepGraph(sharedPrelude().Child(), 0, module, dep_graph.BuildDepGraph(module))
			require.Equal(t, tt.wantErrs, messagesWithSpan(t, c.errs))
			leaf := findIdentPat(module, "v")
			require.NotNil(t, leaf)
			// The name binds at the annotation on both paths, with no fallback member beside it.
			require.Equal(t, "number", soltype.Print(coalesce(c.info.TypeOf(leaf), soltype.Positive)))
		})
	}
}

// An object `...rest` leaf reads its leftover members off the fallback's own shape, so the
// members the pattern did not name survive the join. Both `p` and the fallback carry `z`
// outside the keys `{x, ...rest}` names, so `rest` reads it from either source.
func TestInferValElseJoinsAnObjectRestLeaf(t *testing.T) {
	values, _, errs := inferSource(t, `fn f(p: {x: number, z: boolean} | {y: string}) {
			val {x, ...rest} = p else { {x: 1, z: false} }
			return rest
		}`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: {y: string} | {x: number, z: boolean}) -> {z: boolean}", values["f"])
}

// A leaf's default covers the key being absent from either source, so the fallback may omit
// it. The projection over the fallback asks for the key the same relaxed way the binding
// walk does, so `x` reads the default rather than picking up an `undefined`.
func TestInferValElseFallbackMayOmitADefaultedLeaf(t *testing.T) {
	values, _, errs := inferSource(t, `fn f(p: {x?: number}) {
			val {x = 5} = p else { {} }
			return x
		}`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: {x?: number}) -> number", values["f"])
}

// A pattern the walk cannot type reports once, not once per walk. The projection over the
// fallback runs the same arms the binding walk did, so an ungated report would double every
// message a pattern raises.
func TestInferValElseReportsAPatternFaultOnce(t *testing.T) {
	_, _, errs := inferSource(t, `fn f(p: {x: number}) {
			val Foo{x} = p else { {x: 1} }
			return x
		}`)
	require.Equal(t, []string{
		"2:8-2:14: `Foo` does not name a class and cannot be used as an instance pattern.",
	}, messagesWithSpan(t, errs))
}

// findIdentPat returns the identifier pattern binding name, and nil when the module holds
// none. It walks through the AST visitor rather than reaching into the block the
// declaration sits in.
func findIdentPat(module *ast.Module, name string) *ast.IdentPat {
	f := &identPatFinder{name: name}
	module.Accept(f)
	return f.found
}

type identPatFinder struct {
	ast.DefaultVisitor
	name  string
	found *ast.IdentPat
}

func (f *identPatFinder) EnterPat(p ast.Pat) bool {
	if ip, ok := p.(*ast.IdentPat); ok && ip.Name == f.name {
		f.found = ip
	}
	return true
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
			src: `fn f(l: {start: {x: number, y: string}}) {
					val {start: {x, y}} = l else { return [0, ""] }
					return [x, y]
				}`,
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
	require.Equal(t, "4:11-4:12: Unknown identifier: x", msgWithSpan(t, errs[0]))
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
			src: `fn g(s: string) { return {x: 1} }
				fn f() { return if val {x} = g(2) { x } else { 0 } }`,
			want: "2:36-2:37: cannot constrain 2 <: string",
		},
		"ValElse": {
			src: `fn g(s: string) { return {x: 1} }
				fn f() {
					val {x} = g(2) else { return 0 }
					return x
				}`,
			want: "3:18-3:19: cannot constrain 2 <: string",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Len(t, errs, 1)
			require.Equal(t, tt.want, msgWithSpan(t, errs[0]))
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
			src: `fn f(u: number | string) {
					val n = u else { nope }
					return n
				}`,
			want: "2:23-2:27: Unknown identifier: nope",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Len(t, errs, 1)
			require.Equal(t, tt.want, msgWithSpan(t, errs[0]))
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
			src: `fn f(u: number | string) {
					return match u {
						x: number => x,
						_ => 0,
					}
				}`,
			want: "fn (u: number | string) -> number",
		},
		{
			// Two annotated arms name the two members between them, so the arms cover the
			// union with no catch-all.
			name: "annotated arms cover a union",
			src: `fn f(u: number | string) {
					return match u {
						x: number => x,
						y: string => y,
					}
				}`,
			want: "fn (u: number | string) -> number | string",
		},
		{
			// One annotated arm leaves the `string` member with no arm to run, so the arms
			// are not exhaustive and the message names that member. The span runs from
			// `match` to the closing brace.
			name: "one annotated arm leaves a member uncovered",
			src: `fn f(u: number | string) {
					return match u {
						x: number => x,
					}
				}`,
			wantErrs: []string{"2:13-4:7: match is not exhaustive; add a branch for `string`"},
		},
		{
			// An annotation admitting the whole scrutinee covers it outright, so no arm below
			// is needed.
			name: "an arm annotated with the whole union covers it",
			src: `fn f(u: number | string) {
					return match u {
						x: number | string => x,
					}
				}`,
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
			// The `number` annotation cannot constrain the `string` scrutinee, so the arm can
			// never run, and that one arm leaves `string` non-exhaustive, so both errors fire.
			name: "an annotation no member fits is rejected",
			src: `fn f(s: string) {
					return match s {
						x: number => x,
					}
				}`,
			wantErrs: []string{
				"3:10-3:16: cannot constrain number <: string",
				"2:13-4:7: match is not exhaustive; `string` admits values no pattern names, so add a catch-all branch",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			if tt.wantErrs != nil {
				require.Equal(t, tt.wantErrs, messagesWithSpan(t, errs))
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
		"BelowAWildcard": `fn f(u: number | string) {
				return match u {
					_ => 0,
					x: number => x,
				}
			}`,
		"BelowABareIdent": `fn f(u: number | string) {
				return match u {
					other => 0,
					x: number => x,
				}
			}`,
	}
	want := "4:6-4:20: this match arm is unreachable because an arm above it matches every value; drop it, or move it above that arm"

	for name, src := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, src)
			require.Equal(t, []string{want}, messagesWithSpan(t, errs))
		})
	}
}

// A narrowing annotation picks out the part of the scrutinee it admits, so an annotation
// wider than a member is accepted rather than rejected. Every value of a `1 | 2` is a number,
// so `x: number` matches both members and the arm runs.
//
// The name takes the annotated type, not the members the test let through. `x: number` binds
// `x` at `number`, so an arm returning `x` contributes `number` to the form's type and never
// `1 | 2`. That is the ordinary declaration rule, where `val x: number = 5` types `x` as
// `number` rather than as `5`, and it is why the annotation reads as a declaration of the
// binding rather than only as a test of the value.
//
// The three forms share bindNarrowedIdent, so each accepts and rejects the same annotations
// and binds at the same type.
func TestInferAnnotationWiderThanAMemberBindsTheAnnotation(t *testing.T) {
	tests := map[string]struct {
		src string
		// binding is the name whose printed type is checked.
		binding string
		// want is that name's printed type, checked when wantErrs is empty.
		want string
		// wantErrs, when non-empty, is the full set of expected messages.
		wantErrs []string
	}{
		// Both members fit the annotation, so the arm runs for either and `x` is a `number`.
		// The literal members do not reach the return type.
		"MatchArm": {
			src: `fn f(u: 1 | 2) {
					return match u {
						x: number => x,
					}
				}`,
			binding: "f",
			want:    "fn (u: 1 | 2) -> number",
		},
		// The `else`'s 0 is subsumed into the `number` the consequent contributes.
		"IfVal": {
			src:     `fn f(u: 1 | 2) { return if val x: number = u { x } else { 0 } }`,
			binding: "f",
			want:    "fn (u: 1 | 2) -> number",
		},
		"ValElse": {
			src: `fn f(u: 1 | 2) {
					val x: number = u else { return 0 }
					return x
				}`,
			binding: "f",
			want:    "fn (u: 1 | 2) -> number",
		},
		// `unknown` is the top of the lattice, so it admits every value of any scrutinee, and
		// the name it binds is an `unknown`.
		"Unknown": {
			src: `fn f(u: number | string) {
					return match u {
						x: unknown => x,
					}
				}`,
			binding: "f",
			want:    "fn (u: number | string) -> unknown",
		},
		// Only the two literal members fit, so the first arm runs for those and the second
		// takes the rest. Each arm's name is its own annotation.
		"WiderThanSomeMembers": {
			src: `fn f(u: 1 | 2 | string) {
					return match u {
						x: number => x,
						s: string => s,
					}
				}`,
			binding: "f",
			want:    "fn (u: string | 1 | 2) -> number | string",
		},
		// A transparent alias carries the alias rather than the union it stands for, so the
		// members are reached by expanding it first. Without that the annotation would be
		// measured against the alias as a single shape and fit nothing.
		"AliasedUnion": {
			src: `type U = 1 | 2 | string
				fn f(u: U) {
					return match u {
						x: number => x,
						s: string => 0,
					}
				}`,
			binding: "f",
			want:    "fn (u: U) -> number",
		},
		// An annotation narrower than every member fits none of them, so the union-super
		// exists rule decides instead. The name takes the annotation either way.
		"NarrowerThanEveryMember": {
			src:     `fn f(u: number | string) { return if val x: 1 = u { x } else { 0 } }`,
			binding: "f",
			want:    "fn (u: number | string) -> 0 | 1",
		},
		// No value of the scrutinee is a number and no member holds one, so neither rule
		// accepts the annotation and the arm can never run. The bare `string` scrutinee is left
		// non-exhaustive by that one arm, so the coverage check names it too.
		"AdmitsNothing": {
			src: `fn f(s: string) {
					return match s {
						x: number => x,
					}
				}`,
			binding: "f",
			wantErrs: []string{
				"3:10-3:16: cannot constrain number <: string",
				"2:13-4:7: match is not exhaustive; `string` admits values no pattern names, so add a catch-all branch",
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			if tt.wantErrs != nil {
				require.Equal(t, tt.wantErrs, messagesWithSpan(t, errs))
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
			src: `fn f(p: [number, string]) { return match p {
					[a: number, b: string] => [a, b],
				} }`,
			want: "fn (p: [number, string]) -> [number, string]",
		},
		{
			name: "if val tuple leaves",
			src:  `fn f(p: [number, string]) { return if val [a: number, b: string] = p { [a, b] } else { [0, ""] } }`,
			want: "fn (p: [number, string]) -> [number, string]",
		},
		{
			name: "val else tuple leaves",
			src: `fn f(p: [number, string]) {
					val [a: number, b: string] = p else { return [0, ""] }
					return [a, b]
				}`,
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
			src: `fn f(p: {a: number, b: string}) {
					val {a::number} = p else { return 0 }
					return a
				}`,
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
			name: "val else tuple leaf annotation must fit",
			src: `fn f(p: [number, string]) {
					val [a: string, b: string] = p else { return ["", ""] }
					return [a, b]
				}`,
			wantErrs: []string{"2:11-2:20: cannot constrain number <: string"},
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
				require.Equal(t, tt.wantErrs, messagesWithSpan(t, errs))
				return
			}
			require.Empty(t, errs)
			require.Equal(t, tt.want, values["f"])
		})
	}
}

// Binding-based narrowing keeps a chained guard's display clean. Escalier rebinds on
// refinement rather than re-typing the scrutinee, so each guard computes a fresh binding
// whose type is frozen at that site and a later guard starts from that clean base.
//
// NO COMPLEMENT ARISES ALONG THE WAY. Narrowing drops the union members a guard's
// annotation cannot admit, so the chain below reaches `boolean` as a plain member list and
// the negation simplifier in simplify.go is never reached. A solver that re-typed one
// long-lived variable would instead accumulate `& ~string & ~number` on it and need that
// simplifier to read `boolean` back out. These cases pin that the accumulating form never
// arises, which is what keeps the simplifier's input small when a complement does show up
// from somewhere else.
//
// Each case returns the binding a guard introduced, so the function's return type IS that
// binding's rendered type. A diverging `else` supplies the other path, and its string
// fallbacks are subsumed into `string` at finalization.
func TestInferChainedGuardsRenderSimplifiedBindings(t *testing.T) {
	tests := map[string]struct {
		src  string
		want string
	}{
		// One guard over a three-member union binds exactly the member its annotation
		// admits, with no residual complement over the two it excluded.
		"OneGuard": {
			src: `fn f(u: number | string | boolean) {
					val x: number = u else { return 0 }
					return x
				}`,
			want: "fn (u: number | string | boolean) -> number",
		},
		// The first guard's binding is a two-member union, and the second narrows THAT
		// rather than the scrutinee. Both levels render as plain member lists.
		"TwoGuardsEndingAtOneMember": {
			src: `fn f(u: number | string | boolean) {
					val x: number | string = u else { return "no value" }
					val y: string = x else { return "not a string" }
					return y
				}`,
			want: "fn (u: number | string | boolean) -> string",
		},
		// Two guards that between them exclude two of the three members. This is the
		// chain a flow-narrowing solver would render as
		// `(number | string | boolean) & ~string & ~number`; here the first guard binds
		// `number | boolean` and the second binds `boolean`, with no complement built.
		"TwoGuardsReachingTheThirdMember": {
			src: `fn f(u: number | string | boolean) {
					val x: number | boolean = u else { return true }
					val y: boolean = x else { return false }
					return y
				}`,
			want: "fn (u: number | string | boolean) -> boolean",
		},
		// Narrowing binds a fresh name and never re-types the scrutinee, so `u` still
		// reads at its declared union after two guards have run.
		"ScrutineeKeepsItsDeclaredType": {
			src: `fn f(u: number | string | boolean) {
					val x: number | string = u else { return u }
					val y: string = x else { return u }
					return u
				}`,
			want: "fn (u: number | string | boolean) -> number | string | boolean",
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
