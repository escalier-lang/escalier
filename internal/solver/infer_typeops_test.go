package solver

import (
	"fmt"
	"strings"
	"testing"

	"github.com/escalier-lang/escalier/internal/dep_graph"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// notProductiveMsg renders the full definition-time rejection checkProductive reports when alias
// reaches itself with no type constructor in between, prefixed with span. Several cases below
// declare such an alias and differ only in those two values. TestCheckProductiveRejects spells the
// wording out literally.
func notProductiveMsg(span, alias string) string {
	return fmt.Sprintf("%s: recursive type alias `%s` reaches itself without passing under a type "+
		"constructor, so no lap of the recursion emits any structure and the alias names no type; "+
		"wrap the recursive reference in an object, tuple, or function type", span, alias)
}

// inferTypeNodes infers src and returns the raw soltype.Type of each top-level type binding
// alongside the checker's Context, so a test can reduce a stored residual instead of only reading
// its printed form. An alias binding yields its definition body, the same node inferModule
// prints. It is the raw-type twin of inferModule, test-only.
func inferTypeNodes(t *testing.T, src string) (map[string]soltype.Type, *Context, []SolverError) {
	t.Helper()
	module := parseModule(t, src)
	c := newChecker()
	scope := sharedPrelude().Child()
	c.inferDepGraph(scope, 0, module, dep_graph.BuildDepGraph(module))
	nodes := make(map[string]soltype.Type, len(scope.types))
	for name, b := range scope.types {
		ty := b.Type
		if alias, ok := ty.(*soltype.AliasType); ok {
			if def, ok := c.ctx.aliasDef(alias.Name); ok {
				ty = def.Body
			}
		}
		nodes[name] = ty
	}
	return nodes, c.ctx, c.errs
}

// expandResidual reduces a residual type-level operator such as `keyof Point` against the alias
// environment, the eager expansion constrain performs when it checks a constraint. Production
// keeps a named residual symbolic at annotation and display time, so this test-only helper lets a
// test assert what a residual expands to without routing through a constraint.
func expandResidual(ctx *Context, ty soltype.Type) soltype.Type {
	return newTypeEvaluator(ctx, newSeenPairs()).reduce(ty)
}

// expandAliasResidual substitutes a generic alias instance's arguments into the alias body and then
// reduces the result, the two steps constrain performs when it checks a constraint against a
// reference such as `Elem<[number]>`. A test uses it to assert what one instantiation reduces to.
// A type that is not an alias reference reduces directly, so a table may mix the two shapes.
func expandAliasResidual(ctx *Context, ty soltype.Type) soltype.Type {
	if alias, ok := ty.(*soltype.AliasType); ok {
		ty = ctx.expandAlias(alias)
	}
	return expandResidual(ctx, ty)
}

// `keyof` over a named type reference — an alias or a class — is stored unexpanded, so the type
// keeps the name the source wrote rather than the referenced type's keys. Each case names the
// operand through an alias or class, asserts the stored `Result` renders `keyof Name`, and asserts
// that reducing it with the alias environment — the expansion constrain performs to check a
// constraint — yields the referenced type's keys. The cases cover the operand shapes keyof
// projects (object, single-key object, union, tuple, primitive) and the reference kinds it
// resolves (recursive alias, generic alias, class).
func TestInferKeyofNamedTypeStaysSymbolic(t *testing.T) {
	tests := []struct {
		name         string
		src          string
		wantSymbolic string
		wantExpanded string
	}{
		{
			// An object expands to the union of its property names.
			name: "Object",
			src: `
				type Obj = {x: number, y: string}
				type Result = keyof Obj
			`,
			wantSymbolic: "keyof Obj",
			wantExpanded: `"x" | "y"`,
		},
		{
			// A single-key object collapses to the lone string literal, not a one-member union.
			name: "SingleKeyObject",
			src: `
				type Obj = {only: number}
				type Result = keyof Obj
			`,
			wantSymbolic: "keyof Obj",
			wantExpanded: `"only"`,
		},
		{
			// An inexact object's key union is inexact too: the known
			// keys plus a trailing `...` standing for the unlisted ones.
			name: "InexactObject",
			src: `
				type Obj = {x: number, y: string, ...}
				type Result = keyof Obj
			`,
			wantSymbolic: "keyof Obj",
			wantExpanded: `"x" | "y" | ...`,
		},
		{
			// A single-key inexact object keeps the union wrapper rather than collapsing to the lone
			// literal, since the open tail makes `"only" | ...` strictly weaker than bare `"only"`.
			name: "InexactSingleKeyObject",
			src: `
				type Obj = {only: number, ...}
				type Result = keyof Obj
			`,
			wantSymbolic: "keyof Obj",
			wantExpanded: `"only" | ...`,
		},
		{
			// A key is readable from a union only when every member carries it, so the members'
			// key sets intersect. These two share no key, so the intersection is empty.
			name: "Union",
			src: `
				type U = {a: number} | {b: number}
				type Result = keyof U
			`,
			wantSymbolic: "keyof U",
			wantExpanded: "never",
		},
		{
			// The shared key survives the intersection and the per-member keys drop out.
			name: "UnionSharedKey",
			src: `
				type U = {a: number, shared: string} | {b: boolean, shared: string}
				type Result = keyof U
			`,
			wantSymbolic: "keyof U",
			wantExpanded: `"shared"`,
		},
		{
			// Every shared key survives, not just one: "x" and "y" are on both members, while "a"
			// and "b" each appear on one and drop out.
			name: "UnionSharedKeys",
			src: `
				type U = {a: number, x: string, y: boolean} | {b: number, x: string, y: boolean}
				type Result = keyof U
			`,
			wantSymbolic: "keyof U",
			wantExpanded: `"x" | "y"`,
		},
		{
			// One member's keys are a subset of the other's, so the intersection is that subset
			// and only the key the wider member adds on its own drops out.
			name: "UnionSubsetKeys",
			src: `
				type U = {a: number, b: string} | {a: number, b: string, c: boolean}
				type Result = keyof U
			`,
			wantSymbolic: "keyof U",
			wantExpanded: `"a" | "b"`,
		},
		{
			// Three members intersect pairwise down to the one key all three carry.
			name: "UnionThreeMembers",
			src: `
				type U = {a: number, x: string} | {b: number, x: string} | {a: number, b: number, x: string}
				type Result = keyof U
			`,
			wantSymbolic: "keyof U",
			wantExpanded: `"x"`,
		},
		{
			// An intersection carries both operands' members, so its key sets union. This is the
			// case that keeps every key, in contrast to the union arm above.
			name: "Intersection",
			src: `
				type I = {a: number, shared: string} & {b: boolean, shared: string}
				type Result = keyof I
			`,
			wantSymbolic: "keyof I",
			wantExpanded: `"a" | "b" | "shared"`,
		},
		{
			// An inexact member's open key set may carry "a" as well, so the intersection cannot
			// rule "a" out. The written keys intersect to "shared" and the result stays open.
			name: "UnionInexactMember",
			src: `
				type U = {a: number, shared: string} | {b: boolean, shared: string, ...}
				type Result = keyof U
			`,
			wantSymbolic: "keyof U",
			wantExpanded: `"shared" | ...`,
		},
		{
			// An inexact union has an unlisted member whose keys are unknown, so it cannot close
			// the key set either. The written members still intersect to "shared", left open.
			name: "InexactUnion",
			src: `
				type U = {a: number, shared: string} | {b: boolean, shared: string} | ...
				type Result = keyof U
			`,
			wantSymbolic: "keyof U",
			wantExpanded: `"shared" | ...`,
		},
		{
			// An empty intersection is never even when a member was inexact. The open tail marks
			// a key set that may be larger than its written keys, but a union with no member is
			// never whatever its exactness, so nothing survives to carry the tail.
			name: "UnionInexactMemberDisjoint",
			src: `
				type U = {a: number, ...} | {b: boolean}
				type Result = keyof U
			`,
			wantSymbolic: "keyof U",
			wantExpanded: "never",
		},
		{
			// A primitive member has no keys, so the intersection with it is empty however many
			// keys the object member carries.
			name: "UnionWithPrimitive",
			src: `
				type U = {a: number} | number
				type Result = keyof U
			`,
			wantSymbolic: "keyof U",
			wantExpanded: "never",
		},
		{
			// A tuple yields only its own numeric indices, the keys Object.keys returns. It omits
			// "length" and the inherited Array.prototype members TypeScript's keyof includes.
			name: "Tuple",
			src: `
				type Tup = [number, string]
				type Result = keyof Tup
			`,
			wantSymbolic: "keyof Tup",
			wantExpanded: "0 | 1",
		},
		{
			// keyof of a primitive is never, since a primitive has no enumerable keys.
			name: "Primitive",
			src: `
				type Num = number
				type Result = keyof Num
			`,
			wantSymbolic: "keyof Num",
			wantExpanded: "never",
		},
		{
			// `null` and `undefined` have no members either, so each projects the empty key set
			// the same way a primitive does. A mapped type over one reduces to `{}` rather than
			// stalling on an unreduced `keyof null`.
			name: "NullAtom",
			src: `
				type N = null
				type Result = keyof N
			`,
			wantSymbolic: "keyof N",
			wantExpanded: "never",
		},
		{
			name: "UndefinedAtom",
			src: `
				type U = undefined
				type Result = keyof U
			`,
			wantSymbolic: "keyof U",
			wantExpanded: "never",
		},
		{
			// A recursive alias terminates: projecting its keys never descends into the recursive
			// `children` field value.
			name: "RecursiveAlias",
			src: `
				type Tree = {value: number, children: Tree}
				type Result = keyof Tree
			`,
			wantSymbolic: "keyof Tree",
			wantExpanded: `"children" | "value"`,
		},
		{
			// A generic alias instantiation substitutes its argument, then projects the keys.
			name: "GenericAlias",
			src: `
				type Box<T> = {value: T}
				type Result = keyof Box<number>
			`,
			wantSymbolic: "keyof Box<number>",
			wantExpanded: `"value"`,
		},
		{
			// A non-final class projects to an inexact instance body, since a subclass may add
			// members, so its key union is open: `"x" | "y" | ...`.
			name: "Class",
			src: `
				class Point {
					x: number,
					y: number,
				}
				type Result = keyof Point
			`,
			wantSymbolic: "keyof Point",
			wantExpanded: `"x" | "y" | ...`,
		},
		{
			// A final class has no subclasses to widen it, so its instance body is exact and its
			// key union closed.
			name: "FinalClass",
			src: `
				final class Point {
					x: number,
					y: number,
				}
				type Result = keyof Point
			`,
			wantSymbolic: "keyof Point",
			wantExpanded: `"x" | "y"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, ctx, errs := inferTypeNodes(t, tt.src)
			require.Empty(t, errs)
			result := nodes["Result"]
			// The stored form stays symbolic: a named operand is not expanded at annotation time.
			require.Equal(t, tt.wantSymbolic, soltype.Print(result))
			// Reducing with the alias environment — what constrain does to check a constraint —
			// expands the named operand to the referenced type's keys.
			require.Equal(t, tt.wantExpanded, soltype.Print(expandResidual(ctx, result)))
		})
	}
}

// A `keyof` residual renders symbolically in a function signature and round-trips from parameter
// to return: `fn (k: keyof X) -> keyof X { return k }` keeps `keyof X` on both positions. For a
// type parameter the reflexive `keyof T <: keyof T` from `return k` succeeds inertly by structural
// equality on the residual; for a class it succeeds by expanding both sides to the projected keys.
// Either way the displayed signature keeps the name rather than the keys.
func TestInferKeyofSignatureStaysSymbolic(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want map[string]string
	}{
		{
			name: "TypeParam",
			src:  `fn f<T>(k: keyof T) -> keyof T { return k }`,
			want: map[string]string{"f": "fn <T>(k: keyof T) -> keyof T"},
		},
		{
			// A union operand with a type-parameter member has no enumerable key set to
			// intersect, so the whole operator stays symbolic rather than reducing to the object
			// member's keys.
			name: "UnionWithTypeParam",
			src:  `fn f<T>(k: keyof (T | {a: number})) -> keyof (T | {a: number}) { return k }`,
			want: map[string]string{"f": "fn <T>(k: keyof (T | {a: number})) -> keyof (T | {a: number})"},
		},
		{
			name: "Class",
			src: `
				class Point {
					x: number,
					y: number,
				}
				fn g(k: keyof Point) -> keyof Point { return k }
			`,
			want: map[string]string{"g": "fn (k: keyof Point) -> keyof Point"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
			for name, want := range tt.want {
				require.Equal(t, want, values[name])
			}
		})
	}
}

// constrain expands a `keyof` residual over a type alias or class to check satisfaction, while
// the stored type stays named. A key the referenced type's key set contains is accepted; one it
// lacks is rejected against the expanded keys, so the diagnostic names the projected union. The
// expansion runs at every constraint site: a `val` annotation, a generic alias instantiation, an
// alias that forwards to another alias, and an argument checked against a parameter's type.
func TestInferKeyofAliasConstraint(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string // "" ⇒ expect no error
	}{
		{
			name: "AliasMemberAccepted",
			src: `
				type Point = {x: number, y: number}
				val k: keyof Point = "x"
			`,
		},
		{
			name: "AliasNonMemberRejected",
			src: `
				type Point = {x: number, y: number}
				val k: keyof Point = "z"
			`,
			wantErr: `cannot constrain "z" <: "x" | "y"`,
		},
		{
			name: "GenericAliasMemberAccepted",
			src: `
				type Box<T> = {value: T}
				val k: keyof Box<number> = "value"
			`,
		},
		{
			name: "GenericAliasNonMemberRejected",
			src: `
				type Box<T> = {value: T}
				val k: keyof Box<number> = "size"
			`,
			wantErr: `cannot constrain "size" <: "value"`,
		},
		{
			name: "AliasForwardingToAlias",
			src: `
				type Point = {x: number, y: number}
				type P2 = Point
				val k: keyof P2 = "y"
			`,
		},
		{
			name: "ClassMemberAccepted",
			src: `
				class Point {
					x: number,
					y: number,
				}
				val k: keyof Point = "x"
			`,
		},
		{
			name: "CallArgumentAccepted",
			src: `
				type Point = {x: number, y: number}
				fn pick(k: keyof Point) -> number { return 1 }
				val r = pick("x")
			`,
		},
		{
			name: "CallArgumentRejected",
			src: `
				type Point = {x: number, y: number}
				fn pick(k: keyof Point) -> number { return 1 }
				val r = pick("z")
			`,
			wantErr: `cannot constrain "z" <: "x" | "y"`,
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

// A union operand whose members include a type parameter has one member whose keys the evaluator
// cannot read. That member can only shrink the intersection, so the reduction folds the members it
// can read first and consults the unreadable one only for what remains. When the readable members
// already intersect to nothing, the result is never, since no key the type parameter turns out to
// carry can be put back. When they still share a key, the intersection is uncomputable and the
// whole operator stays symbolic.
//
// Each case checks an argument against the parameter, so the diagnostic names what `keyof`
// reduced to. The first three write the type parameter in a different position of the same
// operand and reduce alike, since skipping an unreadable member rather than bailing out on it
// keeps the result independent of member order.
func TestInferKeyofUnionWithUnreadableMember(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string
	}{
		{
			// The two readable members share no key, so the intersection is empty before the
			// type parameter is even consulted.
			name: "DisjointReadableMembersLast",
			src: `
				fn f<T>(k: keyof ({a: number} | {b: number} | T)) {}
				val r = f("a")
			`,
			wantErr: `cannot constrain "a" <: never`,
		},
		{
			// The same operand with the type parameter written first reduces the same way.
			name: "DisjointReadableMembersFirst",
			src: `
				fn f<T>(k: keyof (T | {a: number} | {b: number})) {}
				val r = f("a")
			`,
			wantErr: `cannot constrain "a" <: never`,
		},
		{
			// And with a readable member on each side of it.
			name: "DisjointReadableMembersAround",
			src: `
				fn f<T>(k: keyof ({a: number} | {b: number} | T | {c: number})) {}
				val r = f("a")
			`,
			wantErr: `cannot constrain "a" <: never`,
		},
		{
			// The readable members both carry "x", so the type parameter decides whether "x"
			// survives and the operator stays symbolic. The union renders its members in
			// canonical order, so the type variable leads.
			name: "OverlappingReadableMembers",
			src: `
				fn f<T>(k: keyof ({a: number, x: string} | {b: number, x: string} | T)) {}
				val r = f("a")
			`,
			wantErr: `cannot constrain "a" <: keyof t11 | object | object`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Len(t, errs, 1)
			require.Equal(t, tt.wantErr, errs[0].Message())
		})
	}
}

// A `keyof` annotation over an inline structural operand is stored unreduced, so the parameter
// type prints the way the source wrote it rather than the operand's keys. An inline object keeps
// its braces, and a union operand keeps its parentheses under the `keyof` prefix. constrain
// reduces the residual when it checks a constraint; the stored and displayed form does not.
func TestInferKeyofAnnotationStaysSymbolic(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want map[string]string
	}{
		{
			name: "InlineObject",
			src:  `fn h(k: keyof {x: number, y: string}) {}`,
			want: map[string]string{"h": "fn (k: keyof {x: number, y: string}) -> undefined"},
		},
		{
			name: "UnionOperand",
			src:  `fn g<T>(x: keyof (T | {a: number})) {}`,
			want: map[string]string{"g": "fn <T>(x: keyof (T | {a: number})) -> undefined"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
			for name, want := range tt.want {
				require.Equal(t, want, values[name])
			}
		})
	}
}

// A nested `keyof keyof` stays symbolic in the stored type and, when reduced, terminates instead
// of looping on the same shape. Over a type parameter it stays the `keyof keyof T` residual in the
// signature; a ground `keyof keyof {a, b}` also stays symbolic in the stored type, and reducing it
// yields never, since the inner keyof projects string-literal keys and a string literal has no
// keys of its own.
func TestInferKeyofNested(t *testing.T) {
	t.Run("TypeParamInSignature", func(t *testing.T) {
		values, _, errs := inferSource(t, `fn f<T>(k: keyof keyof T) {}`)
		require.Empty(t, errs)
		require.Equal(t, "fn <T>(k: keyof keyof T) -> undefined", values["f"])
	})
	t.Run("GroundObject", func(t *testing.T) {
		nodes, ctx, errs := inferTypeNodes(t, `type Result = keyof keyof {a: number, b: string}`)
		require.Empty(t, errs)
		result := nodes["Result"]
		require.Equal(t, "keyof keyof {a: number, b: string}", soltype.Print(result))
		require.Equal(t, "never", soltype.Print(expandResidual(ctx, result)))
	})
}

// A rejected constraint whose subject is a `keyof` residual names it structurally in the
// diagnostic — `cannot constrain keyof t1 <: number` rather than the bare `?` the default
// describe arm would render — so the inert node stays legible in error messages. describe is
// the raw mid-constrain renderer, so the operand shows as the raw var `t1` rather than the
// param name `T` the coalesced printer would use.
func TestInferKeyofResidualErrorMessage(t *testing.T) {
	_, _, errs := inferSource(t, `fn f<T>(k: keyof T) -> number { return k }`)
	require.Len(t, errs, 1)
	require.IsType(t, &CannotConstrainError{}, errs[0])
	require.Equal(t, "1:12-1:19: cannot constrain keyof t1 <: number", msgWithSpan(errs[0]))
}

// Checking a value against `keyof` of a non-productive recursive alias terminates instead of
// looping. checkProductive rejects the alias at its declaration and the evaluator declines to
// expand it, so `keyof` has no ground operand to project keys from and stays a residual. constrain
// treats a residual inert, so the second error is the value conservatively rejected against it. The
// rejection blames the annotation as the source wrote it, `keyof A<number>`. The alias itself
// absorbs at a constraint site; the `keyof` around it does not, which is why this case still reports
// two errors where TestInferNotProductiveAliasAbsorbs reports one.
func TestInferKeyofNotProductiveAliasTerminates(t *testing.T) {
	_, _, errs := inferSource(t, `
		type A<T> = {x: T} | A<{y: T}>
		val k: keyof A<number> = "x"
	`)
	require.Equal(t, []string{
		notProductiveMsg("2:8-2:9", "A"),
		`3:28-3:31: cannot constrain "x" <: keyof A<number>`,
	}, messagesWithSpan(errs))
}

// Checking a value against a mapped type whose value expression grows its own argument terminates
// instead of looping, and here only the two expansion budgets stop it. The alias emits an object
// every lap, so checkProductive accepts it. Its argument is strictly larger every lap, so the
// active-state guard never meets a state it has seen. What reduction chases is `Grow<…>["a"]`, an
// indexed access whose target is another `Grow<…>["a"]`, so the chase never bottoms out. A mapped
// type reduces its value expression once per key, so each lap also branches by the key count.
//
// maxExpandKeyChars is the budget that binds, since it is one monotonic pool shared across the whole
// reduction. Each field's diagnostic names the truncated target the walk gave up on, and the printer
// elides the deepest levels of it as `…`. The two keys of the first case give up at different
// depths for that reason: reducing the value for `"a"` spends most of the pool, leaving the `"b"`
// reduction to stop after one lap.
func TestInferMappedGrowingAliasTerminates(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			// Two keys, so the mapped type reduces its value expression twice.
			name: "TwoKeys",
			src: `
				type Grow<T> = {[K]: Grow<{a: T, b: T}>[K] for K in keyof T}
				val x: Grow<{a: number, b: number}> = {a: 1, b: 2}
			`,
			want: []string{
				`3:47-3:48: cannot constrain 1 <: Grow<{a: {a: {a: …, b: …}, b: {a: …, b: …}}, b: {a: {a: …, b: …}, b: {a: …, b: …}}}>["a"]`,
				`3:53-3:54: cannot constrain 2 <: Grow<{a: {a: number, b: number}, b: {a: number, b: number}}>["b"]`,
			},
		},
		{
			// One key, so the mapped type reduces its value expression once.
			name: "OneKey",
			src: `
				type Grow<T> = {[K]: Grow<{a: T}>[K] for K in keyof T}
				val x: Grow<{a: number}> = {a: 1}
			`,
			want: []string{
				`3:36-3:37: cannot constrain 1 <: Grow<{a: {a: {a: …}}}>["a"]`,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, errs := inferSource(t, test.src)
			require.Equal(t, test.want, messagesWithSpan(errs))
		})
	}
}

// Checking a value against an alias whose argument doubles each lap terminates instead of looping.
// Left free to expand, each lap of these aliases would double the rendered instantiation key, so a
// few dozen laps would render a key of astronomical length without ever branching. Neither alias is
// productive — an object spread merges its operand in rather than nesting it, and an indexed access
// reads a component out of its target — so checkProductive rejects both at their declarations and
// the evaluator declines the first expansion. The walk never starts.
//
// Each case reaches the alias through a different operator, an object spread's operand and an
// indexed access's target, so the refusal is shown to cover every reduction that grounds an operand
// rather than one operator's path through expandAliasGuarded. Neither involves a mapped type.
//
// The definition-time rejection is the only diagnostic. A non-productive alias absorbs at a
// constraint site, so the value check that would otherwise report a second, derived failure reports
// nothing.
func TestInferDoublingAliasArgumentTerminates(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			// The recursion sits in an object spread's operand, which grounds the alias each lap.
			name: "UnderObjectSpread",
			src: `
				type Grow<T> = {...Grow<{a: T, b: T}>}
				val v: Grow<{a: number, b: number}> = 1
			`,
			want: []string{notProductiveMsg("2:10-2:14", "Grow")},
		},
		{
			// The recursion sits in an indexed access's target, which expands it each lap too.
			name: "UnderIndexedAccess",
			src: `
				type Grow<T> = Grow<{a: T, b: T}>["a"]
				val v: Grow<{a: number, b: number}> = 1
			`,
			want: []string{notProductiveMsg("2:10-2:14", "Grow")},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, errs := inferSource(t, test.src)
			require.Equal(t, test.want, messagesWithSpan(errs))
		})
	}
}

// spreadChainSrc builds a chain of aliases, each spreading the one below it `fanOut` times, and a
// value annotated with the top one so checking it has to ground the whole chain. No alias here
// recurses, so checkProductive accepts every one. Grounding the top alias costs fanOut^levels
// expansions, which is what makes these the shapes the two expansion budgets exist for.
func spreadChainSrc(levels, fanOut int) string {
	var b strings.Builder
	b.WriteString("type A0 = {a: number}\n")
	for i := 1; i <= levels; i++ {
		operands := make([]string, fanOut)
		for j := range operands {
			operands[j] = fmt.Sprintf("...A%d", i-1)
		}
		fmt.Fprintf(&b, "type A%d = {%s}\n", i, strings.Join(operands, ", "))
	}
	fmt.Fprintf(&b, "val v: A%d = 1\n", levels)
	return b.String()
}

// The two expansion budgets each stop a shape the other does not, and neither shape involves
// recursion, so checkProductive has nothing to say about either. Without the budgets both hang.
//
// Both cases reject the value, because the annotation never grounds to an object the number could
// satisfy. The point is that inference finishes at all, and that it blames the top alias's
// unexpanded body rather than a type the truncated walk had built.
func TestInferSpreadChainBudgetsTerminate(t *testing.T) {
	tests := []struct {
		name   string
		levels int
		fanOut int
		want   string
	}{
		{
			// Deep and narrow. One expansion per level, so the reduction path is 250 deep while the
			// keys it renders are three characters each and spend almost none of the shared pool.
			// maxExpandDepth is the cap that binds.
			name: "DeepChainBoundByDepth", levels: 250, fanOut: 1,
			want: "252:15-252:16: cannot constrain 1 <: {...A249}",
		},
		{
			// Shallow and wide. Two expansions per level over forty levels is 2^41 expansions, and
			// maxExpandDepth is restored when each sibling branch finishes, so it never binds at a
			// path depth of forty. The monotonic maxExpandKeyChars is what stops this one.
			name: "WideFanBoundByKeyChars", levels: 40, fanOut: 2,
			want: "42:14-42:15: cannot constrain 1 <: {...B39, ...B39}",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			src := spreadChainSrc(test.levels, test.fanOut)
			// The wide case renders its operands under a second name so the two cases' expected
			// messages stay distinguishable at a glance.
			if test.fanOut > 1 {
				src = strings.ReplaceAll(src, "A", "B")
			}
			_, _, errs := inferSource(t, src)
			require.Equal(t, []string{test.want}, messagesWithSpan(errs))
		})
	}
}

// `keyof keyof X` reduces to `never` for every X. What the inner `keyof` projects is a key set —
// string literals for an object, number literals for a tuple — and `keyof` over a literal is
// already `never`, so the outer operator has no keys to name whatever the inner operand turns out
// to be. Each case names an operand shape the inner `keyof` treats differently and asserts the
// same answer.
func TestInferKeyofKeyofIsNever(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		// The inner `keyof` grounds to `"a" | "b"`, whose members are string literals.
		{"Object", `type Result = keyof keyof {a: number, b: string}`},
		// The inner `keyof` grounds to `0 | 1`, whose members are number literals.
		{"Tuple", `type Result = keyof keyof [number, string]`},
		// The inner `keyof` grounds to `never`, the empty key set.
		{"Never", `type Result = keyof keyof never`},
		// The inner `keyof` stays symbolic over a type parameter. The rule still holds, since
		// `keyof T` names a key set for every T.
		{"TypeParam", `type Result<T> = keyof keyof T`},
		// The operand is an alias, which the inner `keyof` would expand.
		{"Alias", `
			type Obj = {a: number}
			type Result = keyof keyof Obj
		`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			types, ctx, errs := inferTypeNodes(t, test.src)
			require.Empty(t, errs)
			require.Contains(t, types, "Result")
			require.Equal(t, "never", soltype.Print(expandResidual(ctx, types["Result"])))
		})
	}
}

// `keyof` over a non-productive recursive alias stays symbolic. A lap of this alias's recursion
// passes only under `keyof`, which reads a key set out of its operand rather than wrapping it, so
// checkProductive rejects the alias and the evaluator declines to expand it. The `keyof` then has no
// ground operand to project keys from.
//
// The `keyof keyof T` ⇒ `never` rule does not fire here, even though this alias body is
// `keyof Grow<…>` and one expansion would put a `keyof` directly under a `keyof`. Seeing that
// requires expanding the rejected alias, which is exactly what the marker forbids. The rule still
// answers for every operand the evaluator will expand — see TestInferKeyofKeyofIsNever.
//
// The alias absorbs at a constraint site, but the `keyof` the annotation wraps around it does not,
// so the value check still reports its own rejection, and it names the argument the source wrote.
func TestInferKeyofNotProductiveAliasStaysSymbolic(t *testing.T) {
	_, _, errs := inferSource(t, `
		type Grow<T> = keyof Grow<{a: T, b: T}>
		val k: keyof Grow<{a: number, b: number}> = "x"
	`)
	require.Equal(t, []string{
		notProductiveMsg("2:8-2:12", "Grow"),
		`3:47-3:50: cannot constrain "x" <: keyof Grow<{a: number, b: number}>`,
	}, messagesWithSpan(errs))
}

// A `typeof v` query is stored as a residual behind the value reference, so an annotation prints
// `typeof v` the way the source wrote it rather than the resolved type. It resolves a bare name
// and a member chain; reducing the residual yields the referenced value's type. The value's
// coalesced type keeps its literal `{a: 1}`, so that is what the query resolves to.
func TestInferTypeof(t *testing.T) {
	tests := []struct {
		name         string
		src          string
		wantSymbolic string
		wantResolved string
	}{
		{
			// A bare `typeof v` names the value and resolves to its object type.
			name: "BareValue",
			src: `
				val v = {a: 1}
				type Result = typeof v
			`,
			wantSymbolic: "typeof v",
			wantResolved: "{a: 1}",
		},
		{
			// A member chain `typeof p.inner` resolves the base value and projects the named
			// property off it.
			name: "MemberChain",
			src: `
				val p = {inner: {a: 1}}
				type Result = typeof p.inner
			`,
			wantSymbolic: "typeof p.inner",
			wantResolved: "{a: 1}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, ctx, errs := inferTypeNodes(t, tt.src)
			require.Empty(t, errs)
			result := nodes["Result"]
			require.Equal(t, tt.wantSymbolic, soltype.Print(result))
			require.Equal(t, tt.wantResolved, soltype.Print(expandResidual(ctx, result)))
		})
	}
}

// constrain unwraps a `typeof v` query to the value's type to check a constraint against it,
// while the stored type stays the named query. The unwrap fires wherever the query appears: as
// the annotation a value is assigned to (the super side), as the type of a value flowing into a
// concrete annotation (the sub side), off a member chain, and as a function parameter's type
// checked against an argument. A matching value is accepted; a mismatch is rejected against the
// resolved type, so the diagnostic names the value's field.
func TestInferTypeofConstraint(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string // "" ⇒ expect no error
	}{
		{
			name: "AnnotationAccepted",
			src: `
				val v = {a: 1}
				val w: typeof v = v
			`,
		},
		{
			name: "AnnotationRejected",
			src: `
				val v = {a: 1}
				val w: typeof v = {a: "hi"}
			`,
			wantErr: `cannot constrain "hi" <: 1`,
		},
		{
			// The query on the sub side: a value typed `typeof v` flows into a concrete
			// annotation, so constrain unwraps the sub to the value's type.
			name: "SubPositionAccepted",
			src: `
				val v = {a: 1}
				val a: typeof v = v
				val b: {a: number} = a
			`,
		},
		{
			name: "SubPositionRejected",
			src: `
				val v = {a: 1}
				val a: typeof v = v
				val b: {a: string} = a
			`,
			wantErr: `cannot constrain 1 <: string`,
		},
		{
			name: "MemberChainRejected",
			src: `
				val p = {inner: {a: 1}}
				val w: typeof p.inner = {a: "x"}
			`,
			wantErr: `cannot constrain "x" <: 1`,
		},
		{
			name: "CallArgumentAccepted",
			src: `
				val v = {a: 1}
				fn f(p: typeof v) -> number { return 1 }
				val r = f({a: 1})
			`,
		},
		{
			name: "CallArgumentRejected",
			src: `
				val v = {a: 1}
				fn f(p: typeof v) -> number { return 1 }
				val r = f({a: "x"})
			`,
			wantErr: `cannot constrain "x" <: 1`,
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

// A `typeof v` on both sides of a flow checks reflexively: the identity function's `return k`
// constrains `typeof v <: typeof v`, which constrain decides by unwrapping both sides to the
// value's type. The signature keeps the query on both positions.
func TestInferTypeofIdentity(t *testing.T) {
	values, _, errs := inferSource(t, `
		val v = {a: 1}
		fn id(k: typeof v) -> typeof v { return k }
	`)
	require.Empty(t, errs)
	require.Equal(t, `fn (k: typeof v) -> typeof v`, values["id"])
}

// The canonical `keyof typeof x`, the value→type bridge: `typeof x` names the value and `keyof`
// wraps it, both staying symbolic, so the type prints `keyof typeof x` as written. Reducing it
// resolves `typeof x` to the value's object type `{a: 1}` and projects its single key `"a"`.
func TestInferKeyofTypeofValue(t *testing.T) {
	nodes, ctx, errs := inferTypeNodes(t, `
		val x = {a: 1}
		type Result = keyof typeof x
	`)
	require.Empty(t, errs)
	result := nodes["Result"]
	require.Equal(t, "keyof typeof x", soltype.Print(result))
	require.Equal(t, `"a"`, soltype.Print(expandResidual(ctx, result)))
}

// Indexed access `T[K]` over a named type reference is stored unexpanded, like `keyof`, so the
// type keeps the name the source wrote rather than the type at the key. Each case names the target
// through an alias or class, asserts the stored `Result` renders `Name[K]`, and asserts that
// reducing it — the expansion constrain performs to check a constraint — yields the type at the
// key. The cases cover the target shapes indexed access resolves through: an object property, a
// tuple element, the `T[keyof T]` value union, a union-key distribution, a generic alias
// instantiation, and a class body.
func TestInferIndexNamedTypeStaysSymbolic(t *testing.T) {
	tests := []struct {
		name         string
		src          string
		wantSymbolic string
		wantExpanded string
	}{
		{
			// A string-literal key selects the named property's value type.
			name: "ObjectProperty",
			src: `
				type Obj = {x: number, y: string}
				type Result = Obj["x"]
			`,
			wantSymbolic: `Obj["x"]`,
			wantExpanded: "number",
		},
		{
			// A numeric-literal key selects the tuple element at that position.
			name: "TupleElement",
			src: `
				type Tup = [number, string]
				type Result = Tup[1]
			`,
			wantSymbolic: "Tup[1]",
			wantExpanded: "string",
		},
		{
			// `T[keyof T]` reduces `keyof T` to the key union, then distributes the access over it,
			// yielding the union of every value type.
			name: "ValueUnion",
			src: `
				type Obj = {x: number, y: string}
				type Result = Obj[keyof Obj]
			`,
			wantSymbolic: "Obj[keyof Obj]",
			wantExpanded: "number | string",
		},
		{
			// An explicit union index distributes member-wise: `Obj["x" | "y"]` ⇒ `Obj["x"] |
			// Obj["y"]`, the same distribute mechanism `T[keyof T]` rides.
			name: "UnionKeyDistribution",
			src: `
				type Obj = {x: number, y: string}
				type Result = Obj["x" | "y"]
			`,
			wantSymbolic: `Obj["x" | "y"]`,
			wantExpanded: "number | string",
		},
		{
			// A union target distributes the access member-wise: `(A | B)["x"]` reads `x` off each
			// member and unions the results.
			name: "UnionTarget",
			src: `
				type A = {x: number}
				type B = {x: string}
				type U = A | B
				type Result = U["x"]
			`,
			wantSymbolic: `U["x"]`,
			wantExpanded: "number | string",
		},
		{
			// An intersection target carries a key when any member has it, so `(A & B)["x"]`
			// selects `x` from the member that declares it.
			name: "IntersectionTarget",
			src: `
				type A = {x: number}
				type B = {y: string}
				type C = A & B
				type Result = C["y"]
			`,
			wantSymbolic: `C["y"]`,
			wantExpanded: "string",
		},
		{
			// When more than one member carries the key, the access is the meet of their value
			// types, so `(A & B)["x"]` with `A.x: number` and `B.x: string` reduces to their
			// intersection.
			name: "IntersectionTargetOverlap",
			src: `
				type A = {x: number}
				type B = {x: string}
				type C = A & B
				type Result = C["x"]
			`,
			wantSymbolic: `C["x"]`,
			wantExpanded: "number & string",
		},
		{
			// A generic alias instantiation substitutes its argument, then selects the property.
			name: "GenericAlias",
			src: `
				type Box<T> = {value: T}
				type Result = Box<number>["value"]
			`,
			wantSymbolic: `Box<number>["value"]`,
			wantExpanded: "number",
		},
		{
			// A class projects its instance body, the same key set an object yields.
			name: "Class",
			src: `
				class Point {
					x: number,
					y: number,
				}
				type Result = Point["x"]
			`,
			wantSymbolic: `Point["x"]`,
			wantExpanded: "number",
		},
		{
			// An indexed access reads the class body, so a key carried by both halves of an
			// accessor pair selects the getter's type whichever half the body declares first.
			// Here `set x` comes before `get x`, and the access still reduces to the getter's
			// `number` rather than reporting the key as write-only.
			name: "ClassAccessorPair",
			src: `
				class C {
					v: number,
					set x(mut self, n: number) { self.v = n },
					get x(self) -> number { return self.v },
				}
				type Result = C["x"]
			`,
			wantSymbolic: `C["x"]`,
			wantExpanded: "number",
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

// An indexed-access residual renders symbolically in a function signature and round-trips from
// parameter to return: `fn f<T>(k: T["a"]) -> T["a"] { return k }` keeps `T["a"]` on both
// positions. The reflexive `T["a"] <: T["a"]` from `return k` succeeds inertly by structural
// equality on the residual, so the displayed signature keeps the access rather than the value.
// An inline structural target keeps its braces under the access, and a `keyof` index prints
// verbatim inside the brackets.
func TestInferIndexStaysSymbolic(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want map[string]string
	}{
		{
			name: "TypeParamRoundTrip",
			src:  `fn f<T>(k: T["a"]) -> T["a"] { return k }`,
			want: map[string]string{"f": `fn <T>(k: T["a"]) -> T["a"]`},
		},
		{
			name: "InlineObjectTarget",
			src:  `fn h(k: {x: number, y: string}["x"]) {}`,
			want: map[string]string{"h": `fn (k: {x: number, y: string}["x"]) -> undefined`},
		},
		{
			name: "KeyofIndex",
			src:  `fn g<T>(k: T[keyof T]) {}`,
			want: map[string]string{"g": "fn <T>(k: T[keyof T]) -> undefined"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
			for name, want := range tt.want {
				require.Equal(t, want, values[name])
			}
		})
	}
}

// constrain expands an indexed-access residual over an alias, tuple, or class to check
// satisfaction, while the stored type stays named. A value matching the type at the key is
// accepted; a mismatch is rejected against the resolved type, so the diagnostic names it. The
// expansion runs at every constraint site: a `val` annotation and a function argument.
func TestInferIndexConstraint(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string // "" ⇒ expect no error
	}{
		{
			name: "ObjectPropertyAccepted",
			src: `
				type Obj = {x: number, y: string}
				val v: Obj["x"] = 5
			`,
		},
		{
			name: "ObjectPropertyRejected",
			src: `
				type Obj = {x: number, y: string}
				val v: Obj["x"] = "hi"
			`,
			wantErr: `cannot constrain "hi" <: number`,
		},
		{
			name: "TupleElementAccepted",
			src: `
				type Tup = [number, string]
				val v: Tup[1] = "hi"
			`,
		},
		{
			name: "TupleElementRejected",
			src: `
				type Tup = [number, string]
				val v: Tup[1] = 5
			`,
			wantErr: `cannot constrain 5 <: string`,
		},
		{
			name: "ValueUnionAccepted",
			src: `
				type Obj = {x: number, y: string}
				val v: Obj[keyof Obj] = "hi"
			`,
		},
		{
			name: "CallArgumentRejected",
			src: `
				type Obj = {x: number, y: string}
				fn take(v: Obj["x"]) -> number { return 1 }
				val r = take("hi")
			`,
			wantErr: `cannot constrain "hi" <: number`,
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

// Reducing a ground indexed access to a key the target lacks reports a dedicated diagnostic at
// the constraint site: an object key with no member is an UnknownObjectKeyError, and a tuple index
// outside the element range is a TupleIndexOutOfRangeError. Each names the target's shape and the
// offending key.
func TestInferIndexReductionError(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string
		wantTyp SolverError
	}{
		{
			name: "UnknownObjectKey",
			src: `
				type Obj = {x: number}
				val v: Obj["z"] = 5
			`,
			wantErr: `object {x: number} has no property "z"`,
			wantTyp: &UnknownObjectKeyError{},
		},
		{
			name: "TupleIndexOutOfRange",
			src: `
				type Tup = [number, string]
				val v: Tup[5] = 1
			`,
			wantErr: "index 5 is out of range for tuple [number, string]",
			wantTyp: &TupleIndexOutOfRangeError{},
		},
		{
			// A key absent from every intersection member is genuinely missing, so one member's
			// absence diagnostic surfaces rather than one per member.
			name: "IntersectionKeyAbsentFromAll",
			src: `
				type A = {x: number}
				type B = {y: string}
				val v: (A & B)["z"] = 5
			`,
			wantErr: `object {x: number} has no property "z"`,
			wantTyp: &UnknownObjectKeyError{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Len(t, errs, 1)
			require.IsType(t, tt.wantTyp, errs[0])
			require.Equal(t, tt.wantErr, errs[0].Message())
		})
	}
}

// A rejected constraint whose subject is an indexed-access residual names it structurally in the
// diagnostic — `cannot constrain t1["a"] <: number` rather than the bare `?` the default describe
// arm would render — so the inert node stays legible. describe is the raw mid-constrain renderer,
// so the target shows as the raw var `t1` rather than the coalesced printer's param name `T`.
func TestInferIndexResidualErrorMessage(t *testing.T) {
	_, _, errs := inferSource(t, `fn f<T>(k: T["a"]) -> number { return k }`)
	require.Len(t, errs, 1)
	require.IsType(t, &CannotConstrainError{}, errs[0])
	require.Equal(t, `1:12-1:18: cannot constrain t1["a"] <: number`, msgWithSpan(errs[0]))
}

// A tuple-spread annotation `[...P, x]` is stored as a residual and reduced by splicing each
// spread operand's tuple in position once the operand grounds to a concrete tuple. Each case
// asserts the stored `Result` renders the way the source wrote it, then asserts that reducing it
// with the alias environment — the expansion constrain performs to check a constraint — splices
// the operands. The cases cover the operand shapes reduction handles: an inline tuple, a named
// alias expanded to a tuple, an inexact operand spliced only as the last element, an inexact
// operand in an earlier position that keeps the spread symbolic, and a trailing `...` marker.
func TestInferTupleSpreadReduction(t *testing.T) {
	tests := []struct {
		name         string
		src          string
		wantSymbolic string
		wantExpanded string
	}{
		{
			// An inline tuple operand splices its elements in position.
			name: "InlineTuple",
			src: `
				type Result = [...[number, string], boolean]
			`,
			wantSymbolic: "[...[number, string], boolean]",
			wantExpanded: "[number, string, boolean]",
		},
		{
			// A named alias operand expands to its tuple body, then splices.
			name: "AliasOperand",
			src: `
				type Pair = [number, string]
				type Result = [...Pair, boolean]
			`,
			wantSymbolic: "[...Pair, boolean]",
			wantExpanded: "[number, string, boolean]",
		},
		{
			// Nested spreads collapse inside-out: each spread operand reduces before the outer
			// splice, so a chain of single-element spreads flattens to the innermost tuple.
			name:         "NestedSpreads",
			src:          `type Result = [...[...[...[number]]]]`,
			wantSymbolic: "[...[...[...[number]]]]",
			wantExpanded: "[number]",
		},
		{
			// Two spread operands splice left to right around a positional element.
			name: "TwoSpreads",
			src: `
				type Result = [...[number], string, ...[boolean]]
			`,
			wantSymbolic: "[...[number], string, ...[boolean]]",
			wantExpanded: "[number, string, boolean]",
		},
		{
			// An inexact operand spliced as the last element extends the prefix and carries its
			// open tail, so the result is inexact too.
			name: "InexactOperandLast",
			src: `
				type Rest = [number, ...]
				type Result = [boolean, ...Rest]
			`,
			wantSymbolic: "[boolean, ...Rest]",
			wantExpanded: "[boolean, number, ...]",
		},
		{
			// An inexact operand in an earlier position would put a later element at an unknown
			// position, so the spread stays symbolic around the expanded operand.
			name: "InexactOperandNotLast",
			src: `
				type Rest = [number, ...]
				type Result = [...Rest, boolean]
			`,
			wantSymbolic: "[...Rest, boolean]",
			wantExpanded: "[...[number, ...], boolean]",
		},
		{
			// A trailing `...` marker round-trips and reduces to an inexact tuple.
			name: "TrailingInexactMarker",
			src: `
				type Result = [...[number], ...]
			`,
			wantSymbolic: "[...[number], ...]",
			wantExpanded: "[number, ...]",
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

// A tuple-spread residual over a type parameter renders symbolically in a function signature and
// round-trips from parameter to return: `fn f<T>(x: [...T, number]) -> [...T, number] { return x }`
// keeps `[...T, number]` on both positions. The reflexive `[...T, number] <: [...T, number]` from
// `return x` succeeds inertly by structural equality on the residual, since the abstract operand
// never grounds.
func TestInferTupleSpreadSignatureStaysSymbolic(t *testing.T) {
	values, _, errs := inferSource(t, `fn f<T>(x: [...T, number]) -> [...T, number] { return x }`)
	require.Empty(t, errs)
	require.Equal(t, "fn <T>(x: [...T, number]) -> [...T, number]", values["f"])
}

// constrain reduces a ground tuple-spread annotation to the spliced tuple to check satisfaction,
// while the stored type stays the residual. A value whose elements match the spliced positions is
// accepted; a mismatch is rejected against the spliced element, so the diagnostic names the
// reduced position. The reduction runs at every constraint site: a `val` annotation over an inline
// operand, over an aliased operand, and an argument checked against a parameter's type.
func TestInferTupleSpreadConstraint(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string // "" ⇒ expect no error
	}{
		{
			name: "InlineOperandAccepted",
			src: `
				val r: [...[number, string], boolean] = [1, "a", true]
			`,
		},
		{
			name: "InlineOperandRejected",
			src: `
				val r: [...[number, string], boolean] = [1, "a", "b"]
			`,
			wantErr: `cannot constrain "b" <: boolean`,
		},
		{
			name: "AliasOperandAccepted",
			src: `
				type Pair = [number, string]
				val r: [...Pair, boolean] = [1, "a", true]
			`,
		},
		{
			name: "AliasOperandRejected",
			src: `
				type Pair = [number, string]
				val r: [...Pair, boolean] = [1, "a", 2]
			`,
			wantErr: `cannot constrain 2 <: boolean`,
		},
		{
			name: "CallArgumentAccepted",
			src: `
				fn f(p: [...[number], string]) -> number { return 1 }
				val r = f([1, "a"])
			`,
		},
		{
			name: "CallArgumentRejected",
			src: `
				fn f(p: [...[number], string]) -> number { return 1 }
				val r = f([1, 2])
			`,
			wantErr: `cannot constrain 2 <: string`,
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

// A spliced tuple grounds even when one of its elements is still a residual. constrain decides that
// from the root of the reduced tuple rather than from the whole tree, so an element that reduces at
// the position it lands in does not make the enclosing tuple inert. An inert tuple would be compared
// structurally against the value, rejecting a constraint that holds.
//
// Each case here reaches a residual through a different route: a `keyof` element the splice carries
// in, a splice nested inside another splice, and a recursive tuple alias whose own body spreads
// itself. The last case is why the root check matters most, since such an alias always has a `...L`
// one level down and would never ground under a whole-tree reading.
func TestInferTupleSpreadGroundsWithResidualElements(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{
			// The splice carries in a `keyof` that reduces to `"c"` where it lands.
			name: "KeyofElement",
			src: `
				type P = {c: number}
				val r: [...[keyof P], boolean] = ["c", true]
			`,
		},
		{
			// The operand is itself a spliced tuple, so the reduced result holds a nested one.
			name: "NestedSplice",
			src: `
				type Inner = [number]
				val r: [...[...Inner, string]] = [1, "a"]
			`,
		},
		{
			// A recursive tuple alias. Its spliced form always names itself one level down, so a
			// whole-tree check never lets it ground. No finite value inhabits `L`, so the constraint
			// runs between two types rather than from a literal.
			name: "RecursiveTupleAlias",
			src: `
				type L = [number, [...L]]
				declare fn mk() -> [...L]
				fn use() -> [number, [...L]] { return mk() }
			`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Empty(t, Messages(errs))
		})
	}
}

// A rest element whose operand never grounds keeps the tuple inert, which is what the root check
// must still report. A type parameter has no tuple to splice, so `[...T, number]` stays residual and
// the value is rejected against it rather than against some spliced form. The operand renders as the
// var the call instantiated `T` to rather than as `T` itself, since the rejection happens at the
// call site.
func TestInferTupleSpreadOverTypeParamStaysInert(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f<T>(x: [...T, number]) -> number { return 1 }
		val r = f([1])
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "cannot constrain tuple <: [...t7, number]", errs[0].Message())
}

// A `mut` spread operand `[...mut P]` is rejected at the annotation site the same way a positional
// `[mut X]` element is: the enclosing tuple decides mutability, so an owned-mutable operand nested
// in it is misleading. The annotation reports MutFieldError and recovers to the bare operand, which
// still splices in position, so `[...mut P, boolean]` over `type P = [number]` checks against
// `[1, true]` with only the one diagnostic.
func TestInferTupleSpreadMutOperandRejected(t *testing.T) {
	_, _, errs := inferSource(t, `
		type P = [number]
		val r: [...mut P, boolean] = [1, true]
	`)
	require.Len(t, errs, 1)
	require.IsType(t, &MutFieldError{}, errs[0])
	require.Equal(t, "owned-mutable field annotation is not allowed; the enclosing context decides mutability — wrap the whole annotation in `mut` to make this field writable, or use interior mutability", errs[0].Message())
}

// Checking a value against a tuple spread of a non-productive recursive alias terminates instead of
// looping. The recursive reference is a `...P` element, which splices its operand's elements into
// the tuple rather than nesting them under one, so no lap emits a level and checkProductive rejects
// the alias. The evaluator then declines to expand it, the annotation keeps its `[...A<number>, …]`
// residual, and constrain treats that residual inert, conservatively rejecting the value. The alias
// itself absorbs at a constraint site; the tuple wrapped around it does not.
func TestInferTupleSpreadNotProductiveAliasTerminates(t *testing.T) {
	_, _, errs := inferSource(t, `
		type A<T> = [T, ...A<[T]>]
		val r: [...A<number>, boolean] = [1, 2]
	`)
	require.Equal(t, []string{
		notProductiveMsg("2:8-2:9", "A"),
		"3:36-3:42: cannot constrain tuple <: [...A<number>, boolean]",
	}, messagesWithSpan(errs))
}

// `keyof` and indexed access over a tuple carrying an unreduced `...P` spread stay symbolic: the
// tuple has no ground index set until the spread grounds. Over a concrete inline spread the tuple
// reduces first, so `keyof` projects the spliced indices and indexed access selects the spliced
// element. Over an abstract operand both keep the operator symbolic rather than projecting the
// `...P` element as a single position.
func TestInferKeyofIndexOverSpreadTuple(t *testing.T) {
	tests := []struct {
		name         string
		src          string
		wantSymbolic string
		wantExpanded string
	}{
		{
			// keyof over a ground spread reduces the tuple to [number, string, boolean], then
			// projects its indices.
			name:         "KeyofGroundSpread",
			src:          `type Result = keyof [...[number, string], boolean]`,
			wantSymbolic: "keyof [...[number, string], boolean]",
			wantExpanded: "0 | 1 | 2",
		},
		{
			// keyof over an abstract spread operand stays symbolic.
			name:         "KeyofAbstractSpread",
			src:          `fn f<T>(k: keyof [...T, boolean]) {}`,
			wantSymbolic: "keyof [...T, boolean]",
		},
		{
			// Indexed access over a ground spread reduces the tuple, then selects the element.
			name:         "IndexGroundSpread",
			src:          `type Result = [...[number, string], boolean][2]`,
			wantSymbolic: "[...[number, string], boolean][2]",
			wantExpanded: "boolean",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.wantExpanded == "" {
				// A signature-level case: assert the residual renders symbolically and does not crash.
				values, _, errs := inferSource(t, tt.src)
				require.Empty(t, errs)
				require.Equal(t, "fn <T>(k: "+tt.wantSymbolic+") -> undefined", values["f"])
				return
			}
			nodes, ctx, errs := inferTypeNodes(t, tt.src)
			require.Empty(t, errs)
			result := nodes["Result"]
			require.Equal(t, tt.wantSymbolic, soltype.Print(result))
			require.Equal(t, tt.wantExpanded, soltype.Print(expandResidual(ctx, result)))
		})
	}
}

// A conditional `if Check : Extends { Then } else { Else }` over ground operands is stored
// unreduced, so the type prints the way the source wrote it, and reducing it — the branch selection
// constrain performs to check a constraint — decides `Check <: Extends` and yields the selected
// branch. Each case names a ground Check and Extends, asserts the stored `Result` renders the
// conditional verbatim, and asserts the reduced branch. The cases cover the decision shapes branch
// selection handles: a reflexive primitive match, a literal against its primitive, a rejected
// primitive that takes Else, a selected branch that is itself a union, and a Check resolved through
// an alias.
func TestInferCondBranchSelection(t *testing.T) {
	tests := []struct {
		name         string
		src          string
		wantSymbolic string
		wantExpanded string
	}{
		{
			// A primitive is a subtype of itself, so the Then branch is selected.
			name:         "ReflexivePrimitive",
			src:          `type Result = if number : number { "yes" } else { "no" }`,
			wantSymbolic: `if number : number { "yes" } else { "no" }`,
			wantExpanded: `"yes"`,
		},
		{
			// A string literal is a subtype of the `string` primitive, so Then is selected.
			name:         "LiteralUnderPrimitive",
			src:          `type Result = if "hi" : string { number } else { boolean }`,
			wantSymbolic: `if "hi" : string { number } else { boolean }`,
			wantExpanded: "number",
		},
		{
			// `string` is not a subtype of `number`, so the Else branch is selected.
			name:         "RejectedTakesElse",
			src:          `type Result = if string : number { number } else { boolean }`,
			wantSymbolic: `if string : number { number } else { boolean }`,
			wantExpanded: "boolean",
		},
		{
			// The selected branch is reduced too, so a union branch yields the union.
			name:         "UnionBranch",
			src:          `type Result = if number : number { number | string } else { boolean }`,
			wantSymbolic: `if number : number { number | string } else { boolean }`,
			wantExpanded: "number | string",
		},
		{
			// A Check named through an alias decides by expanding the alias for the probe, while the
			// stored conditional keeps the alias name.
			name: "AliasCheck",
			src: `
				type N = number
				type Result = if N : number { "yes" } else { "no" }
			`,
			wantSymbolic: `if N : number { "yes" } else { "no" }`,
			wantExpanded: `"yes"`,
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

// A conditional over a type parameter renders symbolically in a function signature and stays
// symbolic there: `fn f<T>(x: if T : number { string } else { boolean })` keeps the whole
// conditional, since `T` never grounds so the `T <: number` probe cannot decide a branch. A
// non-ground Extends keeps it symbolic the same way.
func TestInferCondSignatureStaysSymbolic(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want map[string]string
	}{
		{
			name: "TypeParamCheck",
			src:  `fn f<T>(x: if T : number { string } else { boolean }) {}`,
			want: map[string]string{"f": "fn <T>(x: if T : number { string } else { boolean }) -> undefined"},
		},
		{
			name: "TypeParamExtends",
			src:  `fn g<T>(x: if number : T { string } else { boolean }) {}`,
			want: map[string]string{"g": "fn <T>(x: if number : T { string } else { boolean }) -> undefined"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
			for name, want := range tt.want {
				require.Equal(t, want, values[name])
			}
		})
	}
}

// constrain reduces a conditional to the selected branch to check satisfaction, while the stored
// type stays the residual. A value matching the selected branch is accepted; a mismatch is rejected
// against that branch, so the diagnostic names the branch the conditional resolved to. The reduction
// runs at every constraint site: a `val` annotation whose conditional takes Then, one that takes
// Else, a generic alias instantiation that substitutes its argument before deciding, and a function
// argument.
func TestInferCondConstraint(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string // "" ⇒ expect no error
	}{
		{
			name: "ThenBranchAccepted",
			src:  `val x: if number : number { string } else { boolean } = "hi"`,
		},
		{
			name:    "ThenBranchRejected",
			src:     `val x: if number : number { string } else { boolean } = 5`,
			wantErr: `cannot constrain 5 <: string`,
		},
		{
			name: "ElseBranchAccepted",
			src:  `val x: if string : number { string } else { boolean } = true`,
		},
		{
			name:    "ElseBranchRejected",
			src:     `val x: if string : number { string } else { boolean } = "hi"`,
			wantErr: `cannot constrain "hi" <: boolean`,
		},
		{
			// A generic alias substitutes its argument into the conditional, then decides the branch:
			// `Choose<number>` reduces to the Then branch `string`.
			name: "GenericAliasThen",
			src: `
				type Choose<T> = if T : number { string } else { boolean }
				val x: Choose<number> = "hi"
			`,
		},
		{
			name: "GenericAliasElse",
			src: `
				type Choose<T> = if T : number { string } else { boolean }
				val x: Choose<string> = 5
			`,
			wantErr: "cannot constrain 5 <: boolean",
		},
		{
			name: "CallArgumentRejected",
			src: `
				fn take(v: if number : number { string } else { boolean }) -> number { return 1 }
				val r = take(5)
			`,
			wantErr: `cannot constrain 5 <: string`,
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

// A rejected constraint whose subject is a symbolic conditional names it structurally in the
// diagnostic — the full `if … : … { … } else { … }` form rather than the bare `?` the default
// describe arm would render — so the inert node stays legible. describe is the raw mid-constrain
// renderer, so the Check shows as the raw var `t1` rather than the coalesced printer's param name.
func TestInferCondResidualErrorMessage(t *testing.T) {
	_, _, errs := inferSource(t, `fn f<T>(k: if T : number { string } else { boolean }) -> number { return k }`)
	require.Len(t, errs, 1)
	require.IsType(t, &CannotConstrainError{}, errs[0])
	require.Equal(t, "1:12-1:51: cannot constrain if t1 : number { string } else { boolean } <: number", msgWithSpan(errs[0]))
}

// An `infer U` clause outside a conditional's Extends operand names no matched position, so it
// reports one UnsupportedFeatureError rather than resolving to a capture nothing ever fills. The
// cases cover the positions an `infer` can be written in but never captures from: a plain
// annotation, a conditional's Check, and the Then branch of a conditional that captures nothing.
func TestInferOutsideExtendsUnsupported(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{name: "PlainAnnotation", src: `val x: infer U = 5`},
		{name: "CondCheck", src: `type Result = if infer U : number { "y" } else { "n" }`},
		{name: "CondThen", src: `type Result = if number : number { infer U } else { "n" }`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Len(t, errs, 1)
			require.IsType(t, &UnsupportedFeatureError{}, errs[0])
			require.Equal(t, "Unsupported: infer outside a conditional type's extends operand", errs[0].Message())
		})
	}
}

// A self-referential conditional alias is rejected at its declaration and reports nothing further.
// A conditional guards its two branches, since reaching one means a later instantiation can take
// the other and stop, but its Check is evaluated on every lap and so guards nothing. `type Bad = if
// Bad : number { … }` therefore reaches itself emitting nothing, and checkProductive rejects it.
// The value checked against it absorbs, so the definition-time diagnostic is the only one.
func TestInferCondSelfReferentialAliasRejected(t *testing.T) {
	_, _, errs := inferSource(t, `
		type Bad = if Bad : number { number } else { string }
		val x: Bad = 5
	`)
	require.Equal(t, []string{notProductiveMsg("2:8-2:11", "Bad")}, messagesWithSpan(errs))
}

// `keyof` and indexed access compose over a ground conditional: the conditional selects its branch
// first, then the outer operator reduces over that branch. `keyof (if number : number { {x, y} }
// else { {z} })` projects the Then branch's keys, and indexing the Else branch of a rejected
// conditional selects that branch's member.
func TestInferCondComposesWithOperators(t *testing.T) {
	tests := []struct {
		name         string
		src          string
		wantExpanded string
	}{
		{
			// number <: number holds, so keyof projects the Then branch's keys.
			name:         "KeyofOverCond",
			src:          `type Result = keyof if number : number { {x: number, y: string} } else { {z: boolean} }`,
			wantExpanded: `"x" | "y"`,
		},
		{
			// string is not a subtype of number, so the access reads `x` off the Else branch.
			name:         "IndexOverCond",
			src:          `type Result = (if string : number { {x: number} } else { {x: boolean} })["x"]`,
			wantExpanded: "boolean",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, ctx, errs := inferTypeNodes(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.wantExpanded, soltype.Print(expandResidual(ctx, nodes["Result"])))
		})
	}
}

// An `infer U` clause in a conditional's Extends operand captures the type sitting at its matched
// position, and the Then branch reads that capture. Each case reduces a conditional whose Check is
// ground and asserts the type the selected branch yields. The cases cover the positions the matcher
// walks — a tuple element, an object property, a function parameter and return, a promise payload,
// and a type argument — plus the capture-combining and mismatch rules.
func TestInferCondInferCapture(t *testing.T) {
	tests := []struct {
		name         string
		src          string
		wantExpanded string
	}{
		{
			// The single tuple element is captured and returned by the Then branch.
			name:         "TupleElement",
			src:          `type Result = if [number] : [infer U] { U } else { boolean }`,
			wantExpanded: "number",
		},
		{
			// Two captures bind independently, and the Then branch may reorder them.
			name:         "TwoTupleElements",
			src:          `type Result = if [number, string] : [infer A, infer B] { [B, A] } else { boolean }`,
			wantExpanded: "[string, number]",
		},
		{
			// One name written at two positions keeps both matched types, unioned.
			name:         "RepeatedName",
			src:          `type Result = if [number, string] : [infer U, infer U] { U } else { boolean }`,
			wantExpanded: "number | string",
		},
		{
			// A tuple whose arity differs from the pattern's does not align, so Else is selected.
			name:         "TupleArityMismatch",
			src:          `type Result = if [number] : [infer A, infer B] { A } else { boolean }`,
			wantExpanded: "boolean",
		},
		{
			// A pattern mixing a capture with a written type binds the capture, then the
			// `Check <: Extends` probe rejects on the written position, so Else is selected.
			name:         "WrittenPositionRejects",
			src:          `type Result = if [number, string] : [infer A, number] { A } else { boolean }`,
			wantExpanded: "boolean",
		},
		{
			name:         "ObjectProperty",
			src:          `type Result = if {a: number, b: string} : {a: infer A, b: infer B} { [A, B] } else { boolean }`,
			wantExpanded: "[number, string]",
		},
		{
			// An inexact pattern captures from a wider object, since the probe allows the extra key.
			name:         "InexactObjectPattern",
			src:          `type Result = if {a: number, b: string} : {a: infer A, ...} { A } else { boolean }`,
			wantExpanded: "number",
		},
		{
			// An exact pattern narrower than the Check is rejected by the probe, so Else is selected.
			name:         "ExactObjectPatternRejectsWiderCheck",
			src:          `type Result = if {a: number, b: string} : {a: infer A} { A } else { boolean }`,
			wantExpanded: "boolean",
		},
		{
			// A property the Check does not carry cannot be matched, so Else is selected.
			name:         "MissingObjectProperty",
			src:          `type Result = if {a: number} : {z: infer Z, ...} { Z } else { boolean }`,
			wantExpanded: "boolean",
		},
		{
			name: "FunctionReturn",
			src: `
				type F = fn (x: number) -> string
				type Result = if F : fn (x: number) -> infer R { R } else { boolean }
			`,
			wantExpanded: "string",
		},
		{
			name: "FunctionParam",
			src: `
				type F = fn (x: number) -> string
				type Result = if F : fn (x: infer P) -> string { P } else { boolean }
			`,
			wantExpanded: "number",
		},
		{
			name:         "PromisePayload",
			src:          `type Result = if Promise<number> : Promise<infer U> { U } else { boolean }`,
			wantExpanded: "number",
		},
		{
			// The Check names an alias, which expands to the tuple the pattern matches against.
			name: "AliasCheckExpands",
			src: `
				type Pair = [number, string]
				type Result = if Pair : [infer A, infer B] { A } else { boolean }
			`,
			wantExpanded: "number",
		},
		{
			// Both sides name the same alias, so the match runs argument by argument.
			name: "SameAliasArguments",
			src: `
				type Box<T> = {v: T}
				type Result = if Box<number> : Box<infer U> { U } else { boolean }
			`,
			wantExpanded: "number",
		},
		{
			// The pattern's alias expands to its body, so a structural Check still captures.
			name: "AliasPatternExpands",
			src: `
				type Box<T> = {v: T}
				type Result = if {v: number} : Box<infer U> { U } else { boolean }
			`,
			wantExpanded: "number",
		},
		{
			// A Check of an unrelated shape does not align with the pattern, so Else is selected.
			name:         "ShapeMismatch",
			src:          `type Result = if string : [infer U] { U } else { boolean }`,
			wantExpanded: "boolean",
		},
		{
			// A capture may be read from a nested conditional in the Then branch, which decides
			// after the capture is substituted in.
			name:         "NestedConditionalReadsCapture",
			src:          `type Result = if [number] : [infer U] { if U : number { "num" } else { "other" } } else { boolean }`,
			wantExpanded: `"num"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, ctx, errs := inferTypeNodes(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.wantExpanded, soltype.Print(expandAliasResidual(ctx, nodes["Result"])))
		})
	}
}

// A conditional written over a naked type parameter distributes over a union Check: each member
// decides its own branch and the results union. A Check written as anything else decides the union
// as a whole, which is how a user opts out. Each case instantiates a generic alias and asserts the
// type the instantiation reduces to.
func TestInferCondDistribution(t *testing.T) {
	tests := []struct {
		name         string
		src          string
		wantExpanded string
	}{
		{
			// The members select different branches, so the result unions one of each.
			name: "MembersSelectDifferentBranches",
			src: `
				type Wrap<T> = if T : string { [T] } else { boolean }
				type Result = Wrap<"a" | 1>
			`,
			wantExpanded: `boolean | ["a"]`,
		},
		{
			// A branch naming the distributed parameter reads it as the member, so the two members
			// yield two distinct tuples rather than one tuple of the whole union.
			name: "BranchReadsTheMember",
			src: `
				type Wrap<T> = if T : string { [T] } else { boolean }
				type Result = Wrap<"a" | "b">
			`,
			wantExpanded: `["a"] | ["b"]`,
		},
		{
			// A tuple-wrapped Check is not a naked type parameter, so the union decides as a whole and
			// the branch keeps it: one tuple of the union rather than a union of tuples.
			name: "TupleWrappedCheckOptsOut",
			src: `
				type NoDist<T> = if [T] : [string] { [T] } else { boolean }
				type Result = NoDist<"a" | "b">
			`,
			wantExpanded: `["a" | "b"]`,
		},
		{
			// A union written directly in the Check is not a type-parameter reference, so it decides
			// as a whole and takes the Else branch.
			name:         "WrittenUnionCheckDoesNotDistribute",
			src:          `type Result = if number | string : number { "y" } else { "n" }`,
			wantExpanded: `"n"`,
		},
		{
			// The parameter also appears in the Extends operand, so each member is tested against a
			// pattern built from that member: `[string]` against `[[string]]` and `string` against
			// `[string]`, both of which fail. TypeScript reduces the same alias applied to the same
			// union to `"no"`.
			name: "MemberReachesTheExtendsOperand",
			src: `
				type X<T> = if T : [T] { "wrap" } else { "no" }
				type Result = X<[string] | string>
			`,
			wantExpanded: `"no"`,
		},
		{
			// Distribution and capture compose: each member matches the pattern on its own, so the
			// captures union.
			name: "DistributesOverCapture",
			src: `
				type Elem<T> = if T : [infer U] { U } else { boolean }
				type Result = Elem<[number] | [string]>
			`,
			wantExpanded: "number | string",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, ctx, errs := inferTypeNodes(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.wantExpanded, soltype.Print(expandAliasResidual(ctx, nodes["Result"])))
		})
	}
}

// A conditional whose Check never grounds keeps its `infer` clause symbolic, so the stored type
// renders the way the source wrote it: the Extends operand shows the `infer U` binder and the Then
// branch shows the bare reference `U`.
func TestInferCondInferStaysSymbolic(t *testing.T) {
	values, _, errs := inferSource(t, `fn f<T>(x: if T : [infer U] { U } else { boolean }) {}`)
	require.Empty(t, errs)
	require.Equal(t, "fn <T>(x: if T : [infer U] { U } else { boolean }) -> undefined", values["f"])
}

// constrain reduces a capturing conditional at the constraint site, so a value is checked against
// the type the capture resolved to. The cases instantiate an element-extracting alias and check a
// value against it, accepted when it matches the captured element type and rejected against that
// type by name when it does not.
func TestInferCondInferConstraint(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string // "" ⇒ expect no error
	}{
		{
			name: "CapturedTypeAccepted",
			src: `
				type Elem<T> = if T : [infer U] { U } else { boolean }
				val x: Elem<[number]> = 5
			`,
		},
		{
			name: "CapturedTypeRejected",
			src: `
				type Elem<T> = if T : [infer U] { U } else { boolean }
				val x: Elem<[number]> = "hi"
			`,
			wantErr: `cannot constrain "hi" <: number`,
		},
		{
			name: "NonMatchingArgumentTakesElse",
			src: `
				type Elem<T> = if T : [infer U] { U } else { boolean }
				val x: Elem<string> = true
			`,
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

// A capture is in scope for the Then branch only, the position TypeScript scopes it to, so the same
// name written in the Else branch is an unbound type reference.
func TestInferCondCaptureNotInScopeInElse(t *testing.T) {
	_, _, errs := inferSource(t, `type Result = if [number] : [infer U] { U } else { U }`)
	require.Len(t, errs, 1)
	require.IsType(t, &UnknownTypeError{}, errs[0])
	require.Equal(t, "cannot find type `U`", errs[0].Message())
}

// An alias whose Then branch re-instantiates itself with a capture terminates and resolves, one
// expansion per lap: `Deep<[[number]]>` captures `[number]`, reduces to `Deep<[number]>`, and that
// reduces to `number`, which the value is then checked against. This is the shape `Awaited<T>` takes,
// so it is what a later flattening operator rests on. Each lap shrinks its argument, so the alias
// guard is never what stops it — the Else branch is reached on its own.
func TestInferCondRecursiveCaptureAlias(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string // "" ⇒ expect no error
	}{
		{
			name: "InnermostTypeAccepted",
			src: `
				type Deep<T> = if T : [infer U] { Deep<U> } else { T }
				val x: Deep<[[number]]> = 5
			`,
		},
		{
			name: "InnermostTypeRejected",
			src: `
				type Deep<T> = if T : [infer U] { Deep<U> } else { T }
				val x: Deep<[[number]]> = "hi"
			`,
			wantErr: `cannot constrain "hi" <: number`,
		},
		{
			// A branch that re-wraps the capture reaches the same instantiation every lap, so it
			// never shrinks toward the Else branch. It terminates on constrain's cycle-detection
			// set instead: an alias operand is compared under an interned canonical representative,
			// so the repeated `5 <: Same<[number]>` state closes the cycle and the constraint is
			// accepted. The point of the case is termination, not the type the closed cycle admits.
			// The definition passes checkProductive, since the recursive reference sits in a
			// conditional branch.
			name: "SameSizeRecursionTerminates",
			src: `
				type Same<T> = if T : [infer U] { Same<[U]> } else { T }
				val x: Same<[number]> = 5
			`,
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

// A nested conditional that binds a name its enclosing conditional already captured shadows it: the
// inner Then branch reads the inner capture, and the inner binder is filled by the inner match
// rather than by the outer one. Without shadowing the outer `U` would replace both the inner binder
// and the inner reference, turning the inner pattern into `[number]`, which `[string]` fails to
// match, so the case would take the inner Else branch and reduce to `boolean`.
func TestInferCondNestedCaptureShadowsOuter(t *testing.T) {
	nodes, ctx, errs := inferTypeNodes(t, `
		type Result = if [number] : [infer U] {
			if [string] : [infer U] { U } else { boolean }
		} else { boolean }
	`)
	require.Empty(t, errs)
	require.Equal(t, "string", soltype.Print(expandAliasResidual(ctx, nodes["Result"])))
}

// A conditional whose Check names a type that does not resolve recovers that operand to a fresh var,
// and the recovery must not be read as a type parameter: the conditional is not distributive, so a
// union reaching the Check position later would decide as a whole. The annotation reports the
// unresolved name once and keeps the conditional shape.
func TestInferCondUnresolvedCheckIsNotDistributive(t *testing.T) {
	nodes, _, errs := inferTypeNodes(t, `type Result = if Bogus : string { number } else { boolean }`)
	require.Len(t, errs, 1)
	require.Equal(t, "cannot find type `Bogus`", errs[0].Message())
	cond, ok := nodes["Result"].(*soltype.CondType)
	require.True(t, ok)
	require.False(t, cond.Distribute)
}

// Two conditionals resolved separately from the same source denote the same type, so a value of one
// satisfies the other and `return k` is accepted. Each resolution declares its own identity for the
// `infer` name, so equality pairs the two declarations where their clauses meet rather than
// comparing the identities directly, the same way two functions' type parameters pair by position.
func TestInferCondSeparateResolutionsAreEqual(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f<T>(k: if T : [infer U] { U } else { boolean }) -> if T : [infer U] { U } else { boolean } {
			return k
		}
	`)
	require.Empty(t, errs)
	require.Equal(t,
		"fn <T>(k: if T : [infer U] { U } else { boolean }) -> if T : [infer U] { U } else { boolean }",
		values["f"])
}

// A conditional may sit inside any operand of another conditional, and each one decides its own
// branch once its own operands are ground. The cases place a nested conditional at each of the four
// positions, stack three of them, and run a type parameter into an inner Check through alias
// instantiation. Every expectation matches what TypeScript reduces the equivalent
// `C extends E ? T : E` chain to.
func TestInferCondNestedConditionals(t *testing.T) {
	tests := []struct {
		name         string
		src          string
		wantExpanded string
	}{
		{
			// The outer Then holds a conditional, so selecting Then reduces it in turn.
			name:         "NestedInThen",
			src:          `type Result = if number : number { if string : string { "a" } else { "b" } } else { "c" }`,
			wantExpanded: `"a"`,
		},
		{
			// The outer Else holds a conditional, whose own Check fails, so its Else is selected.
			name:         "NestedInElse",
			src:          `type Result = if string : number { "a" } else { if number : string { "b" } else { "c" } }`,
			wantExpanded: `"c"`,
		},
		{
			// The Check is a conditional, which reduces to `string` before the outer test runs.
			name:         "NestedInCheck",
			src:          `type Result = if (if number : number { string } else { boolean }) : string { "yes" } else { "no" }`,
			wantExpanded: `"yes"`,
		},
		{
			// The Extends is a conditional, which reduces to `"a"` before the outer test runs.
			name:         "NestedInExtends",
			src:          `type Result = if "a" : (if number : number { "a" } else { "b" }) { "hit" } else { "miss" }`,
			wantExpanded: `"hit"`,
		},
		{
			name:         "ThreeLevels",
			src:          `type Result = if number : number { if string : string { if boolean : boolean { "deep" } else { "x" } } else { "y" } } else { "z" }`,
			wantExpanded: `"deep"`,
		},
		{
			// Instantiating the alias substitutes the argument at both the outer and the inner Check,
			// so the inner conditional decides on the same argument the outer one did.
			name: "ArgumentReachesInnerCheck",
			src: `
				type Pick2<T> = if T : string { if T : "a" { "sa" } else { "s" } } else { "n" }
				type Result = Pick2<"a">
			`,
			wantExpanded: `"sa"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, ctx, errs := inferTypeNodes(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.wantExpanded, soltype.Print(expandAliasResidual(ctx, nodes["Result"])))
		})
	}
}

// A conditional alias may appear in another conditional's Check, so the outer test runs on what the
// inner alias reduced to. The Check is not decided by the evaluator directly — an alias reference is
// ground, so the `Check <: Extends` probe re-enters constrain, which expands the alias and reduces
// the conditional in its body. Each expectation matches TypeScript's reduction of the same chain.
func TestInferCondAliasComposition(t *testing.T) {
	// isStr reduces to "y" for a string argument and "n" otherwise; label tests isStr's result and
	// outer tests label's, so a reduction runs three aliases deep.
	const aliases = `
		type IsStr<T> = if T : string { "y" } else { "n" }
		type Label<T> = if IsStr<T> : "y" { "text" } else { "other" }
		type Outer<T> = if Label<T> : "text" { "outer-text" } else { "outer-other" }
	`
	tests := []struct {
		name         string
		src          string
		wantExpanded string
	}{
		{
			// IsStr<string> reduces to "y", which the outer conditional matches.
			name:         "InnerAliasSelectsThen",
			src:          aliases + `type Result = Label<string>`,
			wantExpanded: `"text"`,
		},
		{
			// IsStr<number> reduces to "n", so the outer conditional takes Else.
			name:         "InnerAliasSelectsElse",
			src:          aliases + `type Result = Label<number>`,
			wantExpanded: `"other"`,
		},
		{
			// Three aliases deep: Outer tests Label's result, which tested IsStr's.
			name:         "ThreeAliasesDeep",
			src:          aliases + `type Result = Outer<string>`,
			wantExpanded: `"outer-text"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, ctx, errs := inferTypeNodes(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.wantExpanded, soltype.Print(expandAliasResidual(ctx, nodes["Result"])))
		})
	}
}

// Distribution reaches every position the distributed type parameter stands at, nested conditionals
// and composed aliases included. A member selected at the outer conditional is the member the inner
// one decides on, and an alias whose own Check is a naked parameter distributes inside its own
// reduction. Each expectation matches TypeScript's reduction of the same chain.
//
// An alias that needs to distribute over a Check it cannot write nakedly can wrap its body in
// `if T : T { … } else { … }`, the TypeScript idiom for forcing distribution. That outer Check is a
// naked parameter, so it distributes, and each member then runs through the body on its own. Its
// Else branch is unreachable, since a member is always a subtype of itself, so the type written
// there never reaches the result.
func TestInferCondDistributionThroughNesting(t *testing.T) {
	const aliases = `
		type IsStr<T> = if T : string { "y" } else { "n" }
		type Label<T> = if IsStr<T> : "y" { "text" } else { "other" }
		type Outer<T> = if Label<T> : "text" { "outer-text" } else { "outer-other" }
	`
	tests := []struct {
		name         string
		src          string
		wantExpanded string
	}{
		{
			// The member reaches the nested conditional's Check, so `"a"` and `"b"` take the outer
			// Then and then split on the inner test, while `1` takes the outer Else.
			name: "MemberReachesNestedCheck",
			src: `
				type Wrap<T> = if T : string { if T : "a" { "exact" } else { "other" } } else { "num" }
				type Result = Wrap<"a" | "b" | 1>
			`,
			wantExpanded: `"exact" | "num" | "other"`,
		},
		{
			// Every member takes the outer Then, and the inner test still separates them.
			name: "MembersSplitOnInnerCheckAlone",
			src: `
				type Wrap<T> = if T : string { if T : "a" { "exact" } else { "other" } } else { "num" }
				type Result = Wrap<"a" | "b">
			`,
			wantExpanded: `"exact" | "other"`,
		},
		{
			// The member also reaches a nested conditional's Extends operand: `"a" <: "a"` holds for
			// the first member and `"a" <: "b"` fails for the second.
			name: "MemberReachesNestedExtends",
			src: `
				type W<T> = if T : string { if "a" : T { "isA" } else { "notA" } } else { "num" }
				type Result = W<"a" | "b">
			`,
			wantExpanded: `"isA" | "notA"`,
		},
		{
			// Label's Check is `IsStr<T>`, an alias reference rather than a naked parameter, so Label
			// does not distribute. IsStr does, reducing to `"y" | "n"`, which fails `<: "y"`.
			name:         "OuterAliasDoesNotDistributeButInnerDoes",
			src:          aliases + `type Result = Label<string | number>`,
			wantExpanded: `"other"`,
		},
		{
			// The same non-distributing composition one alias deeper.
			name:         "ThreeAliasesDeepWithUnion",
			src:          aliases + `type Result = Outer<string | number>`,
			wantExpanded: `"outer-other"`,
		},
		{
			// Wrapping the previous case's body in `if T : T` gives it a naked Check, so each member
			// runs the alias-check test on its own: `IsStr<string>` yields "y" and `IsStr<number>`
			// yields "n", selecting a different branch per member instead of one branch for the union.
			name: "ForcedDistributionOverAliasCheck",
			src: aliases + `
				type LabelF<T> = if T : T { if IsStr<T> : "y" { "text" } else { "other" } } else { "unreachable" }
				type Result = LabelF<string | number>
			`,
			wantExpanded: `"other" | "text"`,
		},
		{
			// The same idiom over a tuple-wrapped Check, which does not distribute on its own. Forcing
			// it turns the single `["a" | "b"]` the union yields into one tuple per member.
			name: "ForcedDistributionOverTupleWrappedCheck",
			src: `
				type WrapF<T> = if T : T { if [T] : [string] { [T] } else { boolean } } else { "unreachable" }
				type Result = WrapF<"a" | "b">
			`,
			wantExpanded: `["a"] | ["b"]`,
		},
		{
			// The unforced form of the case above, for contrast: the union decides as a whole and the
			// branch keeps it, so one tuple of the union comes back rather than a union of tuples.
			name: "UnforcedTupleWrappedCheckKeepsTheUnion",
			src: `
				type WrapN<T> = if [T] : [string] { [T] } else { boolean }
				type Result = WrapN<"a" | "b">
			`,
			wantExpanded: `["a" | "b"]`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, ctx, errs := inferTypeNodes(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.wantExpanded, soltype.Print(expandAliasResidual(ctx, nodes["Result"])))
		})
	}
}

// A distributed member reaches an alias reference written in a branch, so each member instantiates
// that alias with itself. `Branch<"a" | 1>` admits `"y"`, from `IsStr<"a">` on the string member, and
// `"num"` from the other member, which is what TypeScript reduces the same alias to. `IsStr`'s body
// is a conditional, so the branch reduction expands the reference and decides it, and a rejected
// value names the branch results rather than the alias that produced one of them.
func TestInferCondDistributionIntoBranchAlias(t *testing.T) {
	const aliases = `
		type IsStr<T> = if T : string { "y" } else { "n" }
		type Branch<T> = if T : string { IsStr<T> } else { "num" }
	`
	tests := []struct {
		name    string
		src     string
		wantErr string // "" ⇒ expect no error
	}{
		{
			name: "StringMemberBranchAccepted",
			src:  aliases + `val x: Branch<"a" | 1> = "y"`,
		},
		{
			name: "OtherMemberBranchAccepted",
			src:  aliases + `val x: Branch<"a" | 1> = "num"`,
		},
		{
			// "n" is what IsStr yields for a non-string argument, which no member produces here.
			name:    "UnreachableBranchRejected",
			src:     aliases + `val x: Branch<"a" | 1> = "n"`,
			wantErr: `cannot constrain "n" <: "num" | "y"`,
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

// The capture for an `infer` declaration is whatever the `Check <: Extends` constraint inferred for
// it, so the position it sits in decides how several matched types combine and what an unmatched one
// yields. Each expectation matches TypeScript's reduction of the equivalent conditional.
func TestInferCondCaptureFromConstraint(t *testing.T) {
	tests := []struct {
		name         string
		src          string
		wantExpanded string
	}{
		{
			// A covariant position collects the matched types as lower bounds, so two of them union.
			name:         "CovariantCandidatesUnion",
			src:          `type Result = if [number, string] : [infer U, infer U] { [U] } else { "no" }`,
			wantExpanded: `[number | string]`,
		},
		{
			// A function parameter is contravariant, so its candidates collect as upper bounds and
			// meet instead.
			name: "ContravariantCandidatesMeet",
			src: `
				type F = fn (x: {a: number}, y: {b: string}) -> boolean
				type P2<T> = if T : fn (x: infer P, y: infer P) -> boolean { [P] } else { "no" }
				type Result = P2<F>
			`,
			wantExpanded: `[{a: number} & {b: string}]`,
		},
		{
			// An optional property the Check does not carry leaves its capture unconstrained, so the
			// constraint holds and the capture is `unknown`.
			name:         "UnconstrainedCaptureIsUnknown",
			src:          `type Result = if {} : {a?: infer U, ...} { [U] } else { "no" }`,
			wantExpanded: `[unknown]`,
		},
		{
			// A union pattern is decided by constrain's union arm: `"a" <: number` fails, so the
			// remaining arm takes the capture.
			name:         "UnionPatternCaptures",
			src:          `type Result = if "a" : number | infer U { [U] } else { "no" }`,
			wantExpanded: `["a"]`,
		},
		{
			// An intersection Check is decomposed by constrain too, so a pattern reads a member off
			// whichever operand carries it.
			// The two operands are inexact so the intersection is inhabited. Exact records
			// over disjoint fields meet to `never`, which satisfies the Check vacuously and
			// leaves the capture unconstrained.
			name:         "IntersectionCheckCaptures",
			src:          `type Result = if {a: number, ...} & {b: string, ...} : {a: infer A, ...} { [A] } else { "no" }`,
			wantExpanded: `[number]`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, ctx, errs := inferTypeNodes(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.wantExpanded, soltype.Print(expandAliasResidual(ctx, nodes["Result"])))
		})
	}
}

// awaitedAlias is `Awaited<T>` written in Escalier, the recursive conditional that strips every layer
// of `Promise` off a type. Every case below prepends this one definition, since what they assert is
// that it reduces the way TypeScript's `Awaited<T>` does.
const awaitedAlias = `
	type Awaited<T> = if T : Promise<infer U> { Awaited<U> } else { T }
`

// `Awaited<T>` strips promises to any depth. Each lap captures the payload of one `Promise` and hands
// it back to the alias. The lap whose argument is not a promise selects Else and returns it, so the
// recursion ends on the first non-promise. Every expectation matches TypeScript's reduction of the
// same argument.
//
// Recursion is what sets these cases apart from a conditional whose branches name only written types.
// The Then branch names the alias being defined, so deciding the conditional once is not enough.
// reduceBranch expands that reference, decides the conditional in its body, and repeats until a lap
// selects Else.
func TestInferAwaited(t *testing.T) {
	tests := []struct {
		name         string
		src          string
		wantExpanded string
	}{
		{
			// One layer. The capture is the payload, and the next lap returns it unchanged.
			name:         "OneLayer",
			src:          `type Result = Awaited<Promise<number>>`,
			wantExpanded: "number",
		},
		{
			// A nested promise, the shape `await` does not flatten on its own. Each lap strips one
			// layer, so two laps reach the payload.
			name:         "TwoLayers",
			src:          `type Result = Awaited<Promise<Promise<string>>>`,
			wantExpanded: "string",
		},
		{
			// Depth is not fixed, so four layers reduce the same way one does.
			name:         "FourLayers",
			src:          `type Result = Awaited<Promise<Promise<Promise<Promise<number>>>>>`,
			wantExpanded: "number",
		},
		{
			// A non-promise argument selects Else on the first lap and comes back unchanged.
			name:         "NonPromise",
			src:          `type Result = Awaited<number>`,
			wantExpanded: "number",
		},
		{
			// A promise payload that is itself structural is returned whole.
			name:         "ObjectPayload",
			src:          `type Result = Awaited<Promise<{a: number}>>`,
			wantExpanded: "{a: number}",
		},
		{
			// The Check is a naked type parameter, so a union argument distributes and each member
			// unwraps on its own.
			name:         "UnionDistributes",
			src:          `type Result = Awaited<Promise<number> | string>`,
			wantExpanded: "number | string",
		},
		{
			// Each member recurses to its own depth before the results union.
			name:         "UnionOfPromisesAtDifferentDepths",
			src:          `type Result = Awaited<Promise<number> | Promise<Promise<string>>>`,
			wantExpanded: "number | string",
		},
		{
			// The argument is another operator's result rather than a written type. The alias
			// reference is ground, so the `Check <: Extends` probe expands it, and `Awaited` unwraps
			// the promise the function returns. `Awaited<ReturnType<F>>` is how a caller names the
			// value an async function resolves to.
			name: "OverReturnType",
			src: `
				type ReturnType<F> = if F : fn () -> infer R { R } else { never }
				type Fetch = fn () -> Promise<number>
				type Result = Awaited<ReturnType<Fetch>>
			`,
			wantExpanded: "number",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, ctx, errs := inferTypeNodes(t, awaitedAlias+tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.wantExpanded, soltype.Print(expandAliasResidual(ctx, nodes["Result"])))
		})
	}
}

// `Awaited<T>` over an unfilled type parameter stays symbolic, the same as any other conditional
// whose Check has not grounded. Reducing it yields the alias body with the recursive reference
// intact, so the recursion runs once an instantiation supplies the argument.
func TestInferAwaitedStaysSymbolic(t *testing.T) {
	nodes, ctx, errs := inferTypeNodes(t, awaitedAlias+`type Wrapper<T> = Awaited<T>`)
	require.Empty(t, errs)
	require.Equal(t, "Awaited<t3>", soltype.Print(nodes["Wrapper"]))
	require.Equal(t,
		"if t3 : Promise<infer U> { Awaited<U> } else { t3 }",
		soltype.Print(expandAliasResidual(ctx, nodes["Wrapper"])))
}

// constrain reduces an `Awaited<…>` annotation to check a value against it, while the stored type
// keeps the name the source wrote. A value of the unwrapped type is accepted and a mismatch is
// rejected against that unwrapped type, so the diagnostic names what the recursion arrived at
// rather than the annotation.
func TestInferAwaitedConstraint(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string // "" ⇒ expect no error
	}{
		{
			name: "UnwrappedValueAccepted",
			src:  `val x: Awaited<Promise<Promise<number>>> = 5`,
		},
		{
			name:    "MismatchNamesUnwrappedType",
			src:     `val x: Awaited<Promise<Promise<number>>> = "hi"`,
			wantErr: `cannot constrain "hi" <: number`,
		},
		{
			name: "ArgumentChecksThroughParameterType",
			src: `
				fn take(v: Awaited<Promise<string>>) -> number { return 1 }
				val r = take(5)
			`,
			wantErr: "cannot constrain 5 <: string",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, awaitedAlias+tt.src)
			if tt.wantErr == "" {
				require.Empty(t, errs)
				return
			}
			require.Len(t, errs, 1)
			require.Equal(t, tt.wantErr, errs[0].Message())
		})
	}
}

// A branch that names an alias expands only when that alias's body is itself an unreduced operator,
// which is what a recursive conditional needs to reach a value. A branch naming an alias whose body
// is ordinary structure keeps the name the source wrote, matching how every other reduction treats a
// named type. Both cases reduce the same conditional shape and differ only in what the branch alias
// stands for.
func TestInferCondBranchAliasExpandsOnlyOperators(t *testing.T) {
	tests := []struct {
		name         string
		src          string
		wantExpanded string
	}{
		{
			// The branch alias's body is a conditional, so it expands and decides.
			name: "OperatorBodyReduces",
			src: `
				type IsStr<T> = if T : string { "y" } else { "n" }
				type Pick<T> = if T : string { IsStr<T> } else { "num" }
				type Result = Pick<"a">
			`,
			wantExpanded: `"y"`,
		},
		{
			// The branch alias's body is an object, so the reference stands as written.
			name: "StructuralBodyKeepsTheName",
			src: `
				type List<T> = {head: T, tail: List<T>}
				type Pick<T> = if T : number { List<T> } else { boolean }
				type Result = Pick<number>
			`,
			wantExpanded: "List<number>",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, ctx, errs := inferTypeNodes(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.wantExpanded, soltype.Print(expandAliasResidual(ctx, nodes["Result"])))
		})
	}
}

// A recursive conditional whose branch never stops recursing terminates anyway, leaving the alias
// reference symbolic. `Awaited<T>` stops because a lap eventually selects Else. An alias whose Then
// branch always wins has no such lap, so what ends the walk is one of the guards expandAliasGuarded
// applies. A conditional guards its branches, so checkProductive accepts every alias written here and
// the stop has to come from reduction.
func TestInferAwaitedShapeWithoutBaseCaseTerminates(t *testing.T) {
	// `Loop` calls itself with the argument it was given, so the second lap repeats the first's
	// instantiation state and the active-state guard stops it there. The result is the reference the
	// source wrote.
	t.Run("RepeatingStateStopsAtTheRepeat", func(t *testing.T) {
		nodes, ctx, errs := inferTypeNodes(t, `
			type Loop<T> = if T : T { Loop<T> } else { T }
			type Result = Loop<number>
		`)
		require.Empty(t, errs)
		require.Equal(t, "Loop<number>", soltype.Print(expandAliasResidual(ctx, nodes["Result"])))
	})

	// `Grow` wraps its argument in a tuple every lap, so no instantiation state ever repeats and
	// maxExpandDepth is the guard that runs out. The nested case puts a second conditional between the
	// alias and its recursive reference, so the branch reduction reaches that reference through
	// another reduction rather than directly. Both must spend one expansion per lap. A branch that
	// expanded twice would keep growing past the exhausted budget, which is why each case asserts the
	// exact layer count rather than only that reduction finished.
	tests := []struct {
		name string
		body string
	}{
		{name: "DirectBranch", body: `if T : T { Grow<[T]> } else { T }`},
		{name: "BranchNestsAnotherConditional", body: `if T : T { if T : T { Grow<[T]> } else { T } } else { T }`},
	}
	for _, tt := range tests {
		t.Run(tt.name+"StopsOnTheDepthBudget", func(t *testing.T) {
			nodes, ctx, errs := inferTypeNodes(t, `
				type Grow<T> = `+tt.body+`
				type Result = Grow<number>
			`)
			require.Empty(t, errs)
			reduced := soltype.Print(expandAliasResidual(ctx, nodes["Result"]))
			require.True(t, strings.HasPrefix(reduced, "Grow<[["),
				"expected a truncated Grow reference, got %s", reduced)
			// Each lap adds one tuple layer and spends one unit of depth. The reference left behind is
			// the argument built for the lap the exhausted budget stopped, so it carries one layer more
			// than the budget allowed laps.
			require.Equal(t, maxExpandDepth+1, strings.Count(reduced, "["))
		})
	}
}

// A mapped type is stored unreduced, so an annotation prints the way the source wrote it, and
// reducing it — what constrain does to check a constraint against it — emits one field per key of
// the Keys union. Each case asserts both forms. The expectations match what TypeScript reduces the
// equivalent `{[K in Keys]: …}` to, except where noted.
func TestInferMappedTypeReduction(t *testing.T) {
	tests := []struct {
		name         string
		src          string
		wantSymbolic string
		wantExpanded string
	}{
		{
			// The identity map: every key of the operand keeps its own value type.
			name: "IdentityOverKeyof",
			src: `
				type Point = {x: number, y: string}
				type Result = {[K: keyof Point]: Point[K]}
			`,
			wantSymbolic: "{[K: keyof Point]: Point[K]}",
			wantExpanded: "{x: number, y: string}",
		},
		{
			// A value expression that never names the key gives every field the same type, the shape
			// `Record<K, V>` takes.
			name: "ValueIgnoresKey",
			src: `
				type Names = "a" | "b"
				type Result = {[K: Names]: boolean}
			`,
			wantSymbolic: "{[K: Names]: boolean}",
			wantExpanded: "{a: boolean, b: boolean}",
		},
		{
			// A number-literal key names the field its digits spell, the coercion JavaScript applies
			// when `{1: v}` stores under the key "1".
			name: "NumberLiteralKeys",
			src: `
				type Keys = "a" | 1
				type Result = {[K: Keys]: boolean}
			`,
			wantSymbolic: "{[K: Keys]: boolean}",
			wantExpanded: `{"1": boolean, a: boolean}`,
		},
		{
			// A tuple's keys are number literals, so a homomorphic map over one emits a field per
			// index. The value expression still indexes the tuple positionally.
			name: "TupleKeys",
			src: `
				type Pair = [number, string]
				type Result = {[K: keyof Pair]: Pair[K]}
			`,
			wantSymbolic: "{[K: keyof Pair]: Pair[K]}",
			wantExpanded: `{"0": number, "1": string}`,
		},
		{
			// A single-key operand emits a single-field object; `keyof` collapsed its union to the
			// lone literal, which unionMembers reads as one key.
			name: "SingleKey",
			src: `
				type One = {only: number}
				type Result = {[K: keyof One]: One[K]}
			`,
			wantSymbolic: "{[K: keyof One]: One[K]}",
			wantExpanded: "{only: number}",
		},
		{
			// An empty key set emits no fields. `keyof {}` is `never`, the union identity.
			name: "EmptyKeySet",
			src: `
				type Empty = {}
				type Result = {[K: keyof Empty]: Empty[K]}
			`,
			wantSymbolic: "{[K: keyof Empty]: Empty[K]}",
			wantExpanded: "{}",
		},
		{
			// `?` marks every emitted field optional, the shape `Partial<T>` takes.
			name: "AddOptional",
			src: `
				type Point = {x: number, y: string}
				type Result = {[K]?: Point[K] for K in keyof Point}
			`,
			wantSymbolic: "{[K: keyof Point]?: Point[K]}",
			wantExpanded: "{x?: number, y?: string}",
		},
		{
			// `readonly` marks every emitted field readonly, the shape `Readonly<T>` takes.
			name: "AddReadonly",
			src: `
				type Point = {x: number, y: string}
				type Result = {readonly [K: keyof Point]: Point[K]}
			`,
			wantSymbolic: "{readonly [K: keyof Point]: Point[K]}",
			wantExpanded: "{readonly x: number, readonly y: string}",
		},
		{
			// The `if C : E` filter drops a key that fails the test, narrowing the key set the way
			// `Pick<T, K>` does. Only "x" is a subtype of `"x"`.
			name: "FilterKeepsMatchingKeys",
			src: `
				type Point = {x: number, y: string}
				type Result = {[K: keyof Point]: Point[K] if K : "x"}
			`,
			wantSymbolic: `{[K: keyof Point]: Point[K] if K : "x"}`,
			wantExpanded: "{x: number}",
		},
		{
			// The filter reads the value at the key as well as the key itself, so a key whose value
			// type fails the test is dropped.
			name: "FilterOnValueType",
			src: `
				type Mixed = {n: number, s: string}
				type Result = {[K: keyof Mixed]: Mixed[K] if Mixed[K] : number}
			`,
			wantSymbolic: "{[K: keyof Mixed]: Mixed[K] if Mixed[K] : number}",
			wantExpanded: "{n: number}",
		},
		{
			// A filter no key satisfies drops every field.
			name: "FilterDropsEveryKey",
			src: `
				type Point = {x: number, y: string}
				type Result = {[K: keyof Point]: Point[K] if K : "z"}
			`,
			wantSymbolic: `{[K: keyof Point]: Point[K] if K : "z"}`,
			wantExpanded: "{}",
		},
		{
			// A key the bracketed expression remaps to `never` is dropped, the way `Omit<T, K>`
			// removes a key. The conditional in the brackets is branch selection, not a capture.
			name: "RemapToNeverDropsKey",
			src: `
				type Point = {x: number, y: string}
				type Result = {[if K : "x" { never } else { K }]: Point[K] for K in keyof Point}
			`,
			wantSymbolic: `{[if K : "x" { never } else { K }]: Point[K] for K in keyof Point}`,
			wantExpanded: "{y: string}",
		},
		{
			// A key remapped to another literal renames the field while the value still reads the
			// original key.
			name: "RemapRenamesField",
			src: `
				type Point = {x: number}
				type Result = {["renamed"]: Point[K] for K in keyof Point}
			`,
			wantSymbolic: `{["renamed"]: Point[K] for K in keyof Point}`,
			wantExpanded: "{renamed: number}",
		},
		{
			// Two keys remapped to one name merge into a single field whose type unions both keys'
			// values, so neither key's contribution is lost.
			name: "RemapCollidingKeysUnion",
			src: `
				type Point = {x: number, y: string}
				type Result = {["one"]: Point[K] for K in keyof Point}
			`,
			wantSymbolic: `{["one"]: Point[K] for K in keyof Point}`,
			wantExpanded: "{one: number | string}",
		},
		{
			// A key remapping may compute a name rather than naming one written in source, by
			// interpolating the key into a template literal and running it through a string
			// intrinsic. This is the `Getters<T>` shape, and it needs no mapped-type machinery of its
			// own: substituteMappedKey rewrites K to the key, the template literal and `Capitalize`
			// reduce it to a string literal, and remappedNames reads that literal as the field name.
			name: "RemapComputesNameFromKey",
			src: `
				type Src = {name: string, age: number}
				type Result = {[` + "`get${Capitalize<K>}`" + `]: Src[K] for K in keyof Src}
			`,
			wantSymbolic: "{[`get${Capitalize<K>}`]: Src[K] for K in keyof Src}",
			wantExpanded: "{getAge: number, getName: string}",
		},
		{
			// A remapping that reduces to a union of names emits one field per name, each carrying
			// the value the key it came from contributes.
			name: "RemapToUnionOfNames",
			src: `
				type Names = "a" | "b"
				type Src = {x: number}
				type Result = {[Names]: Src[K] for K in keyof Src}
			`,
			wantSymbolic: "{[Names]: Src[K] for K in keyof Src}",
			wantExpanded: "{a: number, b: number}",
		},
		{
			// The identity mapped type is the identity: with no modifier written, each emitted field
			// inherits the source member's `?` and `readonly` markers.
			name: "HomomorphicPreservesMarkers",
			src: `
				type Src = {a?: number, readonly b: string, c: boolean}
				type Result = {[K: keyof Src]: Src[K]}
			`,
			wantSymbolic: "{[K: keyof Src]: Src[K]}",
			wantExpanded: "{a?: number, readonly b: string, c: boolean}",
		},
		{
			// `-?` clears an inherited optional marker, which is what distinguishes it from writing
			// no modifier at all.
			name: "RemoveOptionalClearsInherited",
			src: `
				type Src = {a?: number, b: string}
				type Result = {[K: keyof Src]-?: Src[K]}
			`,
			wantSymbolic: "{[K: keyof Src]-?: Src[K]}",
			wantExpanded: "{a: number, b: string}",
		},
		{
			// `-readonly` clears an inherited readonly marker, the twin of the case above.
			name: "RemoveReadonlyClearsInherited",
			src: `
				type Src = {readonly a: number, b: string}
				type Result = {-readonly [K: keyof Src]: Src[K]}
			`,
			wantSymbolic: "{-readonly [K: keyof Src]: Src[K]}",
			wantExpanded: "{a: number, b: string}",
		},
		{
			// A mapped type over a bare key union has no source object to read a marker off, so its
			// fields are unmarked even though the value type is shared. This is the `Record` shape.
			name: "NonHomomorphicEmitsUnmarkedFields",
			src: `
				type Names = "a" | "b"
				type Result = {[K: Names]: boolean}
			`,
			wantSymbolic: "{[K: Names]: boolean}",
			wantExpanded: "{a: boolean, b: boolean}",
		},
		{
			// A mapped type nests: the outer key is in scope inside the inner one, so the inner value
			// reads it.
			name: "Nested",
			src: `
				type Point = {x: number}
				type Result = {[K: keyof Point]: {[J: keyof Point]: Point[K]}}
			`,
			wantSymbolic: "{[K: keyof Point]: {[J: keyof Point]: Point[K]}}",
			wantExpanded: "{x: {x: number}}",
		},
		{
			// An inexact operand has an inexact key set, so `keyof` yields an inexact key union and the
			// object built from it stays open too.
			name: "InexactOperandYieldsInexactObject",
			src: `
				type Point = {x: number, ...}
				type Result = {[K: keyof Point]: Point[K]}
			`,
			wantSymbolic: "{[K: keyof Point]: Point[K]}",
			wantExpanded: "{x: number, ...}",
		},
		{
			// The mapped type's own trailing `...` makes the result inexact even over an exact
			// operand.
			name: "InexactMarkerOnMappedType",
			src: `
				type Point = {x: number}
				type Result = {[K: keyof Point]: Point[K], ...}
			`,
			wantSymbolic: "{[K: keyof Point]: Point[K], ...}",
			wantExpanded: "{x: number, ...}",
		},
		{
			// `keyof` over a mapped type reduces the mapped type first, then projects the names of
			// the fields it emitted.
			name: "KeyofOverMappedType",
			src: `
				type Point = {x: number, y: string}
				type Result = keyof {[K: keyof Point]: Point[K]}
			`,
			wantSymbolic: "keyof {[K: keyof Point]: Point[K]}",
			wantExpanded: `"x" | "y"`,
		},
		{
			// An indexed access into a mapped type reduces the mapped type first, then reads one of
			// the fields it emitted.
			name: "IndexIntoMappedType",
			src: `
				type Point = {x: number, y: string}
				type Result = {[K: keyof Point]: Point[K]}["y"]
			`,
			wantSymbolic: `{[K: keyof Point]: Point[K]}["y"]`,
			wantExpanded: "string",
		},
		{
			// A spread of a mapped type merges the fields it emits into the enclosing object.
			name: "SpreadOfMappedType",
			src: `
				type Point = {x: number}
				type Result = {...{[K: keyof Point]: Point[K]}, y: string}
			`,
			wantSymbolic: "{...{[K: keyof Point]: Point[K]}, y: string}",
			wantExpanded: "{x: number, y: string}",
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

// A key remapping may compute the name with a template literal and a string intrinsic, and that
// composes with everything else an object's member list can hold. The mapped member is one member
// among others, so a renamed field merges with explicit siblings, spreads, and a second mapped
// member under the same source-order rule.
func TestInferMappedTypeTemplateRenameInObject(t *testing.T) {
	tests := []struct {
		name         string
		src          string
		wantExpanded string
	}{
		{
			// A computed rename composes with an ordinary sibling, the `Getters<T>` shape written as one
			// object rather than an intersection.
			name: "RenameAlongsideExplicitMember",
			src: `
				type Src = {name: string, age: number}
				type Result = {id: number, [` + "`get${Capitalize<K>}`" + `]: Src[K] for K in keyof Src}
			`,
			wantExpanded: "{id: number, getAge: number, getName: string}",
		},
		{
			// A computed name colliding with an explicit sibling overrides it, the rightmost-wins rule
			// every merged group follows.
			name: "RenameCollidesWithExplicitMember",
			src: `
				type Src = {x: number}
				type Result = {getX: string, [` + "`get${Capitalize<K>}`" + `]: Src[K] for K in keyof Src}
			`,
			wantExpanded: "{getX: number}",
		},
		{
			// Two mapped members may each rename over the same key set, which is how a getter/setter pair
			// is expressed.
			name: "TwoRenamingMembers",
			src: `
				type Src = {a: number}
				type Result = {[` + "`get${Capitalize<K>}`" + `]: Src[K] for K in keyof Src, [` + "`set${Capitalize<K>}`" + `]: Src[K] for K in keyof Src}
			`,
			wantExpanded: "{getA: number, setA: number}",
		},
		{
			// A renaming member merges after a spread's fields, in source order.
			name: "RenameComposesWithSpread",
			src: `
				type Base = {z: boolean}
				type Src = {a: number}
				type Result = {...Base, [` + "`get${Capitalize<K>}`" + `]: Src[K] for K in keyof Src}
			`,
			wantExpanded: "{z: boolean, getA: number}",
		},
		{
			// Renaming and filtering apply together: the filter reads the pre-rename key, so only the keys
			// that survive it are renamed.
			name: "RenameWithFilter",
			src: `
				type Src = {a: number, b: string}
				type Result = {id: boolean, [` + "`get${Capitalize<K>}`" + `]: Src[K] for K in keyof Src if Src[K] : number}
			`,
			wantExpanded: "{id: boolean, getA: number}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, ctx, errs := inferTypeNodes(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.wantExpanded, soltype.Print(expandAliasResidual(ctx, nodes["Result"])))
		})
	}
}

// The `if Check : Extends` filter tests two arbitrary type expressions, not just the key. Both
// operands resolve in the scope where K is bound and are reduced with the key substituted, so either
// side may name the key, the value at that key, a computed type over the key, or nothing at all.
// That makes value-based filtering — TypeScript's `PickByType` idiom — expressible without any
// filter machinery specific to it.
func TestInferMappedTypeFilterTestsArbitraryTypes(t *testing.T) {
	tests := []struct {
		name         string
		src          string
		wantExpanded string
	}{
		{
			// Keep the keys whose value is a number, testing the value rather than the key.
			name: "ByValueType",
			src: `
				type Src = {a: number, b: string, c: number}
				type Result = {[K]: Src[K] for K in keyof Src if Src[K] : number}
			`,
			wantExpanded: "{a: number, c: number}",
		},
		{
			// The same shape with the test inverted keeps the complementary keys.
			name: "ByValueTypeComplement",
			src: `
				type Src = {a: number, b: string, c: number}
				type Result = {[K]: Src[K] for K in keyof Src if Src[K] : string}
			`,
			wantExpanded: "{b: string}",
		},
		{
			// The check may reach into the value rather than comparing it whole.
			name: "ByNestedFieldOfValue",
			src: `
				type Src = {a: {kind: "x"}, b: {kind: "y"}}
				type Result = {[K]: Src[K] for K in keyof Src if Src[K]["kind"] : "x"}
			`,
			wantExpanded: `{a: {kind: "x"}}`,
		},
		{
			// The check may be a computed type over the key rather than the key itself.
			name: "ByComputedTypeOverKey",
			src: `
				type Src = {ax: number, bx: string}
				type Result = {[K]: Src[K] for K in keyof Src if ` + "`${K}`" + ` : "ax"}
			`,
			wantExpanded: "{ax: number}",
		},
		{
			// Neither operand need mention the key. A condition that fails drops every key, leaving
			// the empty object.
			name: "ConditionIndependentOfKey",
			src: `
				type Src = {a: number, b: string}
				type Result = {[K]: Src[K] for K in keyof Src if number : string}
			`,
			wantExpanded: "{}",
		},
		{
			// Value-based filtering works through a generic parameter, which is how a reusable
			// `PickByType<T, V>` alias would be written.
			name: "ByValueTypeThroughGeneric",
			src: `
				type NumbersOf<T> = {[K]: T[K] for K in keyof T if T[K] : number}
				type Result = NumbersOf<{a: number, b: string}>
			`,
			wantExpanded: "{a: number}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, ctx, errs := inferTypeNodes(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.wantExpanded, soltype.Print(expandAliasResidual(ctx, nodes["Result"])))
		})
	}
}

// A mapped member sits in its object's element list alongside whatever else the source wrote, so
// `{id: number, [K]: V for K in Keys}` is one object annotation. TypeScript has no such form and
// writes the intersection `{id: number} & {[K in Keys]: V}` instead. reduceObject merges the
// computed fields with their siblings in source order, the same merge a `...A` spread feeds, so a
// collision follows the rightmost-wins rule spreads already use.
func TestInferMappedMemberMixesWithOrdinaryMembers(t *testing.T) {
	tests := []struct {
		name         string
		src          string
		wantExpanded string
	}{
		{
			name: "ExplicitBeforeMapped",
			src: `
				type Keys = "a" | "b"
				type Result = {id: number, [K]: string for K in Keys}
			`,
			wantExpanded: "{id: number, a: string, b: string}",
		},
		{
			// Source order decides the member order, so a mapped member written first contributes
			// its fields first.
			name: "MappedBeforeExplicit",
			src: `
				type Keys = "a" | "b"
				type Result = {[K]: string for K in Keys, id: number}
			`,
			wantExpanded: "{a: string, b: string, id: number}",
		},
		{
			// A computed field colliding with an explicit one written earlier overrides it, the
			// rightmost-wins rule mergeSpreadOperands applies to every group it merges.
			name: "CollisionTakesRightmost",
			src: `
				type Keys = "a" | "id"
				type Result = {id: number, [K]: string for K in Keys}
			`,
			wantExpanded: "{id: string, a: string}",
		},
		{
			name: "ComposesWithSpread",
			src: `
				type Keys = "a" | "b"
				type Base = {z: boolean}
				type Result = {...Base, id: number, [K]: string for K in Keys}
			`,
			wantExpanded: "{z: boolean, id: number, a: string, b: string}",
		},
		{
			// Two mapped members in one object each contribute their own group.
			name: "TwoMappedMembers",
			src: `
				type K1 = "a" | "b"
				type K2 = "c"
				type Result = {[K]: string for K in K1, [K]: number for K in K2}
			`,
			wantExpanded: "{a: string, b: string, c: number}",
		},
		{
			// Two mapped members whose key sets overlap merge under the same rightmost-wins rule an
			// explicit collision follows, so the later member's value type wins on the shared key.
			name: "TwoMappedMembersCollide",
			src: `
				type K1 = "a" | "b"
				type K2 = "b" | "c"
				type Result = {[K]: string for K in K1, [K]: number for K in K2}
			`,
			wantExpanded: "{a: string, b: number, c: number}",
		},
		{
			// Each mapped member carries its own modifiers, so one may add `?` while its sibling adds
			// `readonly` without either leaking onto the other's fields.
			name: "TwoMappedMembersKeepOwnModifiers",
			src: `
				type K1 = "a"
				type K2 = "b"
				type Result = {[K]?: string for K in K1, readonly [K]: number for K in K2}
			`,
			wantExpanded: "{a?: string, readonly b: number}",
		},
		{
			// The homomorphic marker inheritance still applies to the computed fields, and leaves the
			// explicit sibling alone.
			name: "MarkersSurviveAlongsideExplicitMember",
			src: `
				type Src = {x?: number, readonly y: string}
				type Result = {id: boolean, [K]: Src[K] for K in keyof Src}
			`,
			wantExpanded: "{id: boolean, x?: number, readonly y: string}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, ctx, errs := inferTypeNodes(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.wantExpanded, soltype.Print(expandAliasResidual(ctx, nodes["Result"])))
		})
	}
}

// Two mapped members in one object each bind their own K. The two bindings draw distinct ids even
// when the source writes the same name, so equality pairs each member's binder with its counterpart
// at the same position rather than conflating the two. That makes the reflexive constraint succeed
// and a swapped pair fail, since member order is significant in an unreduced object.
func TestInferTwoMappedMembersBindIndependently(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f<T, U>(x: {[K]: T[K] for K in keyof T, [K]: U[K] for K in keyof U}) -> number { return 1 }
	`)
	require.Empty(t, errs)
	require.Equal(t,
		"fn <T, U>(x: {[K: keyof T]: T[K], [K: keyof U]: U[K]}) -> number",
		values["f"])

	_, _, errs = inferSource(t, `
		fn g<T, U>(x: {[K]: T[K] for K in keyof T, [K]: U[K] for K in keyof U}) -> {[K]: T[K] for K in keyof T, [K]: U[K] for K in keyof U} { return x }
	`)
	require.Empty(t, errs)

	_, _, errs = inferSource(t, `
		fn h<T, U>(x: {[K]: T[K] for K in keyof T, [K]: U[K] for K in keyof U}) -> {[K]: U[K] for K in keyof U, [K]: T[K] for K in keyof T} { return x }
	`)
	require.Len(t, errs, 1)
	require.Equal(t,
		"cannot constrain {[K: keyof t1]: t1[K], [K: keyof t2]: t2[K]} <: {[K: keyof t2]: t2[K], [K: keyof t1]: t1[K]}",
		errs[0].Message())
}

// A mixed object with an abstract key set stays symbolic and renders the way the source wrote it,
// and a value checked against a ground one must satisfy the explicit member and the computed ones
// alike.
func TestInferMappedMemberMixedConstraint(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f<T>(x: {id: number, [K]: T[K] for K in keyof T}) -> number { return 1 }
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn <T>(x: {id: number, [K: keyof T]: T[K]}) -> number", values["f"])

	_, _, errs = inferSource(t, `
		type Keys = "a" | "b"
		fn g(p: {id: number, [K]: string for K in Keys}) -> number { return 1 }
		val ok = g({id: 1, a: "x", b: "y"})
	`)
	require.Empty(t, errs)

	_, _, errs = inferSource(t, `
		type Keys = "a" | "b"
		fn g(p: {id: number, [K]: string for K in Keys}) -> number { return 1 }
		val bad = g({id: 1, a: "x"})
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "object is missing property: b", errs[0].Message())
}

// A residual object — one carrying a `...A` spread or a `[K]: V for K in Keys` member — reaches the
// property-level walks that peel owned-mut cells, strip borrows, and compare key sets. None of those
// has a settled property list to walk, so each treats the object as opaque. Asserting every member
// is a property instead panics, which is what these shapes pin.
func TestInferResidualObjectSurvivesPropertyWalks(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{"MappedIntoOwnedMutCell", "type P = {x: number}\nvar v: mut {[K]: P[K] for K in keyof P} = {x: 1}"},
		{"SpreadIntoOwnedMutCell", "fn f<T>(p: T) -> number { return 1 }\nvar v: mut {...T} = {x: 1}"},
		{"MappedBehindBorrow", "type P = {x: number}\nfn f(a: &{[K]: P[K] for K in keyof P}) -> number { return 1 }"},
		{"MappedInUnion", "type P = {x: number}\nfn f(a: {[K]: P[K] for K in keyof P} | number) -> number { return 1 }"},
		{"MappedReturnPosition", "type P = {x: number}\nfn f() -> {[K]: P[K] for K in keyof P} { return {x: 1} }"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The assertion is that inference completes at all. Each shape panicked before the walks
			// learned to skip a residual object, so reaching any conclusion here is the result.
			require.NotPanics(t, func() { inferSource(t, tt.src) })
		})
	}
}

// A rejected constraint between two inert mapped residuals names each side's modifiers and inexact
// marker. equalTypeWith reads those fields when deciding whether two residuals are equal, so a
// diagnostic that omitted one would print both sides identically and read as a type failing to
// satisfy itself.
func TestInferMappedTypeErrorNamesModifiers(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "AddOptional",
			src:  `fn f<T>(x: {[K]: T[K] for K in keyof T}) -> {[K]?: T[K] for K in keyof T} { return x }`,
			want: "cannot constrain {[K: keyof t1]: t1[K]} <: {[K: keyof t1]?: t1[K]}",
		},
		{
			name: "RemoveOptional",
			src:  `fn f<T>(x: {[K]: T[K] for K in keyof T}) -> {[K]-?: T[K] for K in keyof T} { return x }`,
			want: "cannot constrain {[K: keyof t1]: t1[K]} <: {[K: keyof t1]-?: t1[K]}",
		},
		{
			name: "AddReadonly",
			src:  `fn f<T>(x: {[K]: T[K] for K in keyof T}) -> {readonly [K]: T[K] for K in keyof T} { return x }`,
			want: "cannot constrain {[K: keyof t1]: t1[K]} <: {readonly [K: keyof t1]: t1[K]}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Len(t, errs, 1)
			require.Equal(t, tt.want, errs[0].Message())
		})
	}
}

// A homomorphic mapped type — one whose key set is written `keyof T` — carries the source member's
// `?` and `readonly` markers onto each field it emits, so the identity mapped type really is the
// identity and a marker survives composition. Dropping a marker here would be unsound rather than
// imprecise: it launders a readonly field into a writable one, and it makes a `Pick` parameter
// reject a value TypeScript accepts.
func TestInferMappedTypeMarkersSurviveComposition(t *testing.T) {
	tests := []struct {
		name         string
		src          string
		wantExpanded string
	}{
		{
			// The identity applied to `Partial<T>` must give `Partial<T>` back. Reducing to
			// `{a: number}` would silently make an optional field required.
			name: "IdentityOverPartial",
			src: `
				type Partial<T> = {[K]?: T[K] for K in keyof T}
				type Id<T> = {[K]: T[K] for K in keyof T}
				type Result = Id<Partial<{a: number}>>
			`,
			wantExpanded: "{a?: number}",
		},
		{
			// A readonly field stays readonly through the identity. Reducing to `{x: number}` would
			// hand out a writable view of a readonly field.
			name: "IdentityPreservesReadonly",
			src: `
				type Src = {readonly x: number, y?: string}
				type Id<T> = {[K]: T[K] for K in keyof T}
				type Result = Id<Src>
			`,
			wantExpanded: "{readonly x: number, y?: string}",
		},
		{
			// `Pick` keeps an optional member optional, so a `Pick<Src, "a">` parameter accepts a
			// value that omits `a`.
			name: "PickKeepsOptional",
			src: `
				type Pick<T, Ks> = {[K]: T[K] for K in keyof T if K : Ks}
				type Src = {a?: number, b: string}
				type Result = Pick<Src, "a">
			`,
			wantExpanded: "{a?: number}",
		},
		{
			// `Required<T>` is what clears the marker, and it must still do so.
			name: "RequiredClearsOptional",
			src: `
				type Required<T> = {[K]-?: T[K] for K in keyof T}
				type Result = Required<{a?: number, b: string}>
			`,
			wantExpanded: "{a: number, b: string}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, ctx, errs := inferTypeNodes(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.wantExpanded, soltype.Print(expandAliasResidual(ctx, nodes["Result"])))
		})
	}
}

// A generic alias whose body is a mapped type reduces per instantiation: the argument substituted
// for the type parameter grounds the Keys operand, so the alias reference reduces to the object for
// that argument. These are the shapes the TS utility types take, verified end to end in a later PR.
func TestInferMappedTypeGenericAlias(t *testing.T) {
	tests := []struct {
		name         string
		src          string
		wantExpanded string
	}{
		{
			// `Partial<T>`: every key optional.
			name: "Partial",
			src: `
				type Partial<T> = {[K]?: T[K] for K in keyof T}
				type Result = Partial<{a: number, b: string}>
			`,
			wantExpanded: "{a?: number, b?: string}",
		},
		{
			// `Readonly<T>`: every key readonly.
			name: "Readonly",
			src: `
				type Readonly<T> = {readonly [K]: T[K] for K in keyof T}
				type Result = Readonly<{a: number}>
			`,
			wantExpanded: "{readonly a: number}",
		},
		{
			// `Pick<T, K>`: the filter keeps the keys the second argument names.
			name: "Pick",
			src: `
				type Pick<T, Ks> = {[K]: T[K] for K in keyof T if K : Ks}
				type Result = Pick<{a: number, b: string, c: boolean}, "a" | "c">
			`,
			wantExpanded: "{a: number, c: boolean}",
		},
		{
			// `Omit<T, K>`: the bracketed expression remaps the named keys to `never`, dropping them.
			name: "Omit",
			src: `
				type Omit<T, Ks> = {[if K : Ks { never } else { K }]: T[K] for K in keyof T}
				type Result = Omit<{a: number, b: string, c: boolean}, "b">
			`,
			wantExpanded: "{a: number, c: boolean}",
		},
		{
			// `Record<Ks, V>` over a literal key union. The primitive-key form `Record<string, V>`
			// needs an index signature, which stays symbolic — see TestInferMappedTypeStaysSymbolic.
			name: "Record",
			src: `
				type Record<Ks, V> = {[K]: V for K in Ks}
				type Result = Record<"a" | "b", number>
			`,
			wantExpanded: "{a: number, b: number}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, ctx, errs := inferTypeNodes(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.wantExpanded, soltype.Print(expandAliasResidual(ctx, nodes["Result"])))
		})
	}
}

// A mapped type whose key set the evaluator cannot enumerate stays symbolic: reducing it returns
// the same operator rebuilt around its reduced operands, so it renders the way the source wrote it
// and reduces later once the operand grounds. Each case asserts the reduction is a no-op on the
// printed form.
func TestInferMappedTypeStaysSymbolic(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			// A type parameter has no ground key set, so the whole mapped type is inert. This is the
			// form every generic utility alias is written in.
			name: "TypeParameterOperand",
			src:  `fn f<T>(x: {[K]: T[K] for K in keyof T}) -> number { return 1 }`,
			want: "fn <T>(x: {[K: keyof T]: T[K]}) -> number",
		},
		{
			// An uncountable key set names infinitely many keys, so there is no field list to expand
			// the member into. The unexpanded member is itself the index signature, and it renders in
			// the `[K: Keys]?: V` shorthand whichever of the two spellings the source wrote.
			name: "IndexSignature",
			src:  `fn f(x: {[K: string]?: number}) -> number { return 1 }`,
			want: "fn (x: {[K: string]?: number}) -> number",
		},
		{
			name: "IndexSignatureWrittenLongForm",
			src:  `fn f(x: {[K]?: number for K in string}) -> number { return 1 }`,
			want: "fn (x: {[K: string]?: number}) -> number",
		},
		{
			// An annotation is stored unreduced, so a mapped type over a key set that does ground
			// still renders the way the source wrote it until a constraint reduces it. See
			// TestInferMappedTypeReduction for the fields this one emits.
			name: "GroundKeySetIsStoredUnreduced",
			src: `
				type Pair = [number, string]
				fn f(x: {[K: keyof Pair]: Pair[K]}) -> number { return 1 }
			`,
			want: "fn (x: {[K: keyof Pair]: Pair[K]}) -> number",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, values["f"])
		})
	}
}

// A mapped type over an uncountable key set is uninhabited unless it adds `?`, since no object
// carries a field at every key of an infinite set. Reduction rejects the required form and names the
// `?`-adding form as the fix. Like every other reduction diagnostic it fires where the type is
// reduced, which is the constraint site.
func TestInferRequiredUncountableKeys(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string // "" ⇒ expect no error
	}{
		{
			name:    "ShorthandNoMarker",
			src:     `val p: {[K: string]: number} = {a: 1}`,
			wantErr: "no object has a field at every key of string, so [K: string]: number is uninhabited; write [K: string]?: number instead",
		},
		{
			name:    "LongFormNoMarker",
			src:     `val p: {[K]: number for K in string} = {a: 1}`,
			wantErr: "no object has a field at every key of string, so [K: string]: number is uninhabited; write [K: string]?: number instead",
		},
		{
			// `-?` strips the marker, which is the required form again.
			name:    "RemovesMarker",
			src:     `val p: {[K: string]-?: number} = {a: 1}`,
			wantErr: "no object has a field at every key of string, so [K: string]-?: number is uninhabited; write [K: string]?: number instead",
		},
		{
			// A number key set is uncountable for the same reason a string one is.
			name:    "NumberKeys",
			src:     `val p: {[K: number]: boolean} = {a: true}`,
			wantErr: "no object has a field at every key of number, so [K: number]: boolean is uninhabited; write [K: number]?: boolean instead",
		},
		{
			// A union is uncountable when any member is. Union normalization orders the members, so
			// the rendered key set reads `number | string` whichever order the source wrote.
			name:    "UnionWithPrimitiveMember",
			src:     `val p: {[K: string | number]: boolean} = {a: true}`,
			wantErr: "no object has a field at every key of number | string, so [K: number | string]: boolean is uninhabited; write [K: number | string]?: boolean instead",
		},
		{
			// A `readonly` marker is orthogonal to `?` and carries into the suggested fix.
			name:    "ReadonlyCarriesIntoTheFix",
			src:     `val p: {readonly [K: string]: number} = {a: 1}`,
			wantErr: "no object has a field at every key of string, so readonly [K: string]: number is uninhabited; write readonly [K: string]?: number instead",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Len(t, errs, 1)
			require.IsType(t, &RequiredUncountableKeysError{}, errs[0])
			require.Equal(t, tt.wantErr, errs[0].Message())
		})
	}
}

// An indexed access reads an object through its index signature when the key names no declared
// member. Each case asserts the type the access reduces to, rather than checking a value against it:
// `1` satisfies both `number` and `number | undefined`, so a constraint would not pin which one the
// read produced.
func TestInferIndexAccessThroughIndexSignature(t *testing.T) {
	tests := []struct {
		name         string
		src          string
		wantExpanded string
	}{
		{
			name: "StringLiteralKeyReadsAsValueOrUndefined",
			src: `
				type Dict = {[K: string]?: number}
				type Result = Dict["anything"]
			`,
			wantExpanded: "number | undefined",
		},
		{
			// The key set itself is a legal key, and reads the same way.
			name: "PrimitiveKeyReadsAsValueOrUndefined",
			src: `
				type Dict = {[K: string]?: number}
				type Result = Dict[string]
			`,
			wantExpanded: "number | undefined",
		},
		{
			// A declared member wins over the signature and is always present, so no `undefined`.
			name: "DeclaredMemberReadsWithoutUndefined",
			src: `
				type Config = {name: string, [K: string]?: boolean}
				type Result = Config["name"]
			`,
			wantExpanded: "string",
		},
		{
			name: "UndeclaredKeyFallsToTheSignature",
			src: `
				type Config = {name: string, [K: string]?: boolean}
				type Result = Config["other"]
			`,
			wantExpanded: "boolean | undefined",
		},
		{
			// A key set narrower than the object's own keys needs no signature, since every key in
			// it is declared. This is the existing union-index distribution, and it adds no
			// `undefined` because every key it names is present.
			name: "KeySubsetNeedsNoSignature",
			src: `
				type Obj = {a: number, b: string}
				type Result = Obj["a" | "b"]
			`,
			wantExpanded: "number | string",
		},
		{
			// An object may carry several signatures over different key sets. The key picks which
			// one describes it, rather than the first one winning.
			name: "StringKeyPicksTheStringSignature",
			src: `
				type Two = {[K: string]?: number, [J: number]?: boolean}
				type Result = Two["a"]
			`,
			wantExpanded: "number | undefined",
		},
		{
			name: "NumberKeyPicksTheNumberSignature",
			src: `
				type Two = {[K: string]?: number, [J: number]?: boolean}
				type Result = Two[0]
			`,
			wantExpanded: "boolean | undefined",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, ctx, errs := inferTypeNodes(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.wantExpanded, soltype.Print(expandAliasResidual(ctx, nodes["Result"])))
		})
	}
}

// An indexed access the index signatures cannot describe is rejected. The diagnostic is minted
// during reduction, which is what a constraint runs, so each case checks a value against the access
// rather than reducing it directly.
func TestInferIndexAccessRejected(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string
	}{
		{
			// The key is not coerced to the key set's type, so a string key on a number signature
			// is rejected rather than read as its digits.
			name: "KeyOutsideTheKeySet",
			src: `
				type ByIndex = {[K: number]?: string}
				val v: ByIndex["a"] = "x"
			`,
			wantErr: `index signature of {[K: number]?: string} accepts a key of type number, not "a"`,
		},
		{
			// The message names every key set the access could have matched.
			name: "KeyOutsideEverySetNamesThemAll",
			src: `
				type Two = {[K: string]?: number, [J: number]?: boolean}
				val v: Two[true] = 1
			`,
			wantErr: `index signature of {[K: string]?: number, [J: number]?: boolean} accepts a key of type string or number, not true`,
		},
		{
			// Without a signature the object says nothing about keys it does not declare.
			name: "NoIndexSignature",
			src: `
				type Obj = {a: number}
				val v: Obj[string] = 1
			`,
			wantErr: `object {a: number} has no index signature to read a key of type string`,
		},
		{
			// The `undefined` arm is not cosmetic: a target that cannot hold it rejects.
			name: "UndefinedArmIsEnforced",
			src: `
				type Dict = {[K: string]?: number}
				fn f(x: Dict["anything"]) -> number { return x }
			`,
			wantErr: `cannot constrain undefined <: number`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Len(t, errs, 1)
			require.Equal(t, tt.wantErr, errs[0].Message())
		})
	}
}

// A dynamic key `recv[k]` is typed as the indexed access `Recv[Kt]`, so it resolves by the same
// rules the type-level `T[K]` does. A read off an index signature therefore agrees with the constant
// and dot forms, all three yielding `Value | undefined`.
func TestInferDynamicIndexRead(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantFn  string // "" ⇒ don't check the inferred signature
		wantErr string // "" ⇒ expect no error
	}{
		{
			name:   "DynamicKeyOffAnIndexSignature",
			src:    "type Dict = {[K: string]?: number}\nfn f(d: Dict, k: string) { return d[k] }",
			wantFn: "fn (d: Dict, k: string) -> number | undefined",
		},
		{
			// The constant-key and dot forms already resolved this way, and the dynamic form agrees.
			name:   "ConstantKeyAgreesWithTheDynamicForm",
			src:    "type Dict = {[K: string]?: number}\nfn f(d: Dict) { return d[\"a\"] }",
			wantFn: "fn (d: Dict) -> number | undefined",
		},
		{
			name:   "DotFormAgreesWithTheDynamicForm",
			src:    "type Dict = {[K: string]?: number}\nfn f(d: Dict) { return d.a }",
			wantFn: "fn (d: Dict) -> number | undefined",
		},
		{
			// A dynamic key cannot be shown to name the declared member, so it reads through the
			// signature rather than through `name`.
			name:   "DynamicKeyOnAMixedObjectReadsTheSignature",
			src:    "type Config = {name: string, [K: string]?: boolean}\nfn f(c: Config, k: string) { return c[k] }",
			wantFn: "fn (c: Config, k: string) -> boolean | undefined",
		},
		{
			// A key set narrower than the object's keys resolves without a signature, and carries no
			// `undefined` because every key in it is declared.
			name:   "KeySubsetOfADeclaredObject",
			src:    "fn f(o: {a: number, b: string}, k: \"a\" | \"b\") { return o[k] }",
			wantFn: `fn (o: {a: number, b: string}, k: "a" | "b") -> number | string`,
		},
		{
			name:    "NoIndexSignatureRejected",
			src:     "fn f(o: {a: number}, k: string) { return o[k] }",
			wantErr: `object {a: number} has no index signature to read a key of type string`,
		},
		{
			name:    "KeyOutsideTheKeySetRejected",
			src:     "type ByIndex = {[K: number]?: string}\nfn f(d: ByIndex, k: string) { return d[k] }",
			wantErr: `index signature of {[K: number]?: string} accepts a key of type number, not string`,
		},
		{
			// A receiver that has not grounded has no index signature to read yet. Resolving it needs
			// a receiver inferred from use, which this does not do.
			name:    "AbstractReceiverUnsupported",
			src:     "fn f<T>(o: T, k: string) { return o[k] }",
			wantErr: "Unsupported: IndexExpr",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			if tt.wantErr == "" {
				require.Empty(t, errs)
			} else {
				require.Len(t, errs, 1)
				require.Equal(t, tt.wantErr, errs[0].Message())
			}
			if tt.wantFn != "" {
				require.Equal(t, tt.wantFn, values["f"])
			}
		})
	}
}

// An index signature names no single key, so satisfying it means every key the source can carry
// holds a value of the signature's type. It also absorbs width, so a source with extra keys is not
// rejected for carrying them.
func TestInferIndexSignatureConstraint(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string // "" ⇒ expect no error
	}{
		{
			name: "EveryPropertyMatchesTheValueType",
			src:  `val p: {[K: string]?: number} = {a: 1, b: 2}`,
		},
		{
			name:    "WrongPropertyTypeRejected",
			src:     `val p: {[K: string]?: number} = {a: 1, b: "two"}`,
			wantErr: `cannot constrain "two" <: number`,
		},
		{
			name: "EmptyObjectAccepted",
			src:  `val p: {[K: string]?: number} = {}`,
		},
		{
			// The signature is written without a trailing `...`, but it absorbs every key, so no
			// property counts as excess against it.
			name: "ExtraPropertiesAbsorbed",
			src:  `val p: {[K: string]?: number} = {a: 1, b: 2, c: 3}`,
		},
		{
			// An index signature is a settled form, not a reduction still in progress, so an object
			// carrying one is ground. A conditional over it therefore selects a branch instead of
			// staying symbolic.
			name: "ConditionalOverAnIndexSignatureSelectsABranch",
			src: `
				type Dict = {[K: string]?: number}
				type Fits = if Dict : {a?: number} { "yes" } else { "no" }
				type Misses = if Dict : {a: number} { "yes" } else { "no" }
				val fits: Fits = "yes"
				val misses: Misses = "no"
			`,
		},
		{
			// A named property on the target states the type at its own key, so the signature covers
			// only the other keys.
			name: "NamedPropertyBesideTheSignature",
			src: `
				type Config = {name: string, [K: string]?: boolean}
				val c: Config = {name: "app", debug: true}
			`,
		},
		{
			name: "NamedPropertyBesideTheSignatureStillChecked",
			src: `
				type Config = {name: string, [K: string]?: boolean}
				val c: Config = {name: 1, debug: true}
			`,
			wantErr: `cannot constrain 1 <: string`,
		},
		{
			name: "OtherKeysStillCheckedAgainstTheSignature",
			src: `
				type Config = {name: string, [K: string]?: boolean}
				val c: Config = {name: "app", debug: 1}
			`,
			wantErr: `cannot constrain 1 <: boolean`,
		},
		{
			// A signature reached through a `mut` wrapper is writable, so the keys it covers are
			// invariant. A literal-typed source field would be widened by a write through the
			// signature, the same reason a `mut` property target pins its field.
			name: "MutableSignaturePinsASourceField",
			src: `
				fn f(d: mut {[K: string]?: number}) -> number { return 1 }
				declare fn g() -> mut {a: 1}
				val y = f(g())
			`,
			wantErr: `cannot constrain number <: 1`,
		},
		{
			// A readonly source member cannot fill a writable signature, matching the property rule.
			name: "ReadonlySourceFieldCannotFillAWritableSignature",
			src: `
				fn f(d: mut {[K: string]?: number}) -> number { return 1 }
				declare fn g() -> mut {readonly a: number}
				val y = f(g())
			`,
			wantErr: `readonly field a cannot satisfy a writable field requirement`,
		},
		{
			// Outside a `mut` wrapper the signature is read-only, so the key stays covariant.
			name: "ImmutableSignatureStaysCovariant",
			src: `
				fn f(d: {[K: string]?: number}) -> number { return 1 }
				declare fn g() -> {a: 1}
				val y = f(g())
			`,
		},
		{
			// A property only one signature's key set accepts is checked against that one. `debug`
			// is a string key, so the number signature never sees it and never demands `1 <: boolean`.
			name: "PropertyCheckedAgainstTheSignatureThatAcceptsIt",
			src: `
				type Two = {[K: string]?: number, [J: number]?: boolean}
				val v: Two = {debug: 1}
			`,
		},
		{
			// A property no signature accepts is still an excess property against an exact target.
			// A name is not coerced to a number, so `a` is outside a number-keyed signature's set.
			name: "PropertyNoSignatureAcceptsIsExtra",
			src: `
				type ByIndex = {[J: number]?: boolean}
				val v: ByIndex = {a: true}
			`,
			wantErr: `object has extra property: a`,
		},
		{
			name: "IndexSignatureIntoIndexSignature",
			src: `
				val p: {[K: string]?: number} = {a: 1}
				val q: {[K: string]?: number} = p
			`,
		},
		{
			// The value types are checked covariantly, so a wider one cannot fill a narrower target.
			name: "IndexSignatureValueTypeCovariant",
			src: `
				val p: {[K: string]?: number} = {a: 1}
				val q: {[K: string]?: string} = p
			`,
			wantErr: `cannot constrain number <: string`,
		},
		{
			// An index signature cannot guarantee any particular key is present, so it does not
			// satisfy a target that requires one.
			name: "DoesNotSatisfyARequiredProperty",
			src: `
				val p: {[K: string]?: number} = {a: 1}
				val q: {a: number} = p
			`,
			wantErr: `object property is optional but required: a`,
		},
		{
			// An optional target property tolerates the key being absent, so the signature fills it.
			name: "SatisfiesAnOptionalProperty",
			src: `
				val p: {[K: string]?: number} = {a: 1}
				val q: {a?: number} = p
			`,
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

// A mapped type whose key set has not grounded is expected to stay symbolic, so the uncountable-key
// rule must not reach it. A key set that is a bare type parameter grounds only at instantiation, and
// the parameter's own constraint does not make the key set uncountable on its own.
func TestInferUncountableKeysNotReportedWhileAbstract(t *testing.T) {
	tests := []struct {
		name   string
		src    string
		wantFn string
	}{
		{
			// The member must still be there unexpanded. Asserting only that no error fired would
			// pass just as well if the mapped type had wrongly reduced to some concrete object.
			name:   "KeySetIsTypeParameter",
			src:    `fn f<T>(x: {[K]: number for K in keyof T}) -> number { return 1 }`,
			wantFn: "fn <T>(x: {[K: keyof T]: number}) -> number",
		},
		{
			name:   "ShorthandOverTypeParameter",
			src:    `fn f<T>(x: {[K: keyof T]: number}) -> number { return 1 }`,
			wantFn: "fn <T>(x: {[K: keyof T]: number}) -> number",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.wantFn, values["f"])
		})
	}
}

// `keyof T` over a ground T is a countable key set even though it is written as an operator, so the
// required form over it is legal. It expands to the fields the keys name rather than reporting a
// RequiredUncountableKeysError, which the expanded form pins and an empty error list would not.
func TestInferGroundKeyofIsCountable(t *testing.T) {
	src := `
		type Point = {x: number, y: string}
		type Result = {[K: keyof Point]: Point[K]}
	`
	nodes, ctx, errs := inferTypeNodes(t, src)
	require.Empty(t, errs)
	require.Equal(t, "{x: number, y: string}", soltype.Print(expandAliasResidual(ctx, nodes["Result"])))
}

// constrain reduces a mapped type to check satisfaction, while the stored type keeps the form the
// source wrote. A value matching the emitted fields is accepted; one that does not is rejected
// against the reduced object, so the diagnostic names the field that failed.
func TestInferMappedTypeConstraint(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string // "" ⇒ expect no error
	}{
		{
			name: "MatchingValueAccepted",
			src: `
				type Point = {x: number, y: string}
				val p: {[K: keyof Point]: Point[K]} = {x: 1, y: "hi"}
			`,
		},
		{
			name: "WrongFieldTypeRejected",
			src: `
				type Point = {x: number, y: string}
				val p: {[K: keyof Point]: Point[K]} = {x: 1, y: 2}
			`,
			wantErr: `cannot constrain 2 <: string`,
		},
		{
			// An optional-marking mapped type accepts a value that omits a field.
			name: "OptionalFieldMayBeOmitted",
			src: `
				type Point = {x: number, y: string}
				val p: {[K]?: Point[K] for K in keyof Point} = {x: 1}
			`,
		},
		{
			// A field a filter dropped is not part of the reduced object, so supplying it is an
			// excess property.
			name: "FilteredFieldRejected",
			src: `
				type Point = {x: number, y: string}
				val p: {[K: keyof Point]: Point[K] if K : "x"} = {x: 1, y: "hi"}
			`,
			wantErr: `object has extra property: y`,
		},
		{
			// A member read goes through the reduced object, so a field the mapped type emits reads
			// at the value type its Value expression gave it.
			name: "FieldReadThroughMappedParam",
			src: `
				type Point = {x: number, y: string}
				fn f(p: {[K: keyof Point]: Point[K]}) -> number { return p.x }
				val n = f({x: 1, y: "hi"})
			`,
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

// A mapped type over a recursive alias terminates on the evaluator's active-state guard: the alias
// stays on the active path while its body reduces, so a field that re-references it is left as the
// unexpanded alias rather than unfolding forever. The reduction finishes and the recursive position
// keeps the alias name.
func TestInferMappedTypeRecursiveAliasTerminates(t *testing.T) {
	nodes, ctx, errs := inferTypeNodes(t, `
		type Rec<T> = {[K]: Rec<T> for K in keyof T}
		type Result = Rec<{a: number}>
	`)
	require.Empty(t, errs)
	require.Equal(t, "{a: Rec<{a: number}>}", soltype.Print(expandAliasResidual(ctx, nodes["Result"])))
}

// Two mapped types resolved separately from the same source compare equal even though each drew its
// own key binding id, so a signature that writes one in both a parameter and the return accepts
// `return x`. The reflexive `M <: M` succeeds inertly on the symbolic form, by structural equality
// under the key pairing rather than by reducing.
func TestInferMappedTypeSignatureRoundTrips(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f<T>(x: {[K]: T[K] for K in keyof T}) -> {[K]: T[K] for K in keyof T} { return x }
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn <T>(x: {[K: keyof T]: T[K]}) -> {[K: keyof T]: T[K]}", values["f"])
}

// `_` in a conditional's Extends operand is an `infer` clause with no name: the match fills it and
// the capture is dropped, so it matches any type at that position without naming it. Each case
// asserts what `Result` reduces to.
func TestInferWildcardInConditionalPattern(t *testing.T) {
	tests := []struct {
		name         string
		src          string
		wantSymbolic string
		wantExpanded string
	}{
		{
			// One `_` past the position being captured.
			name: "MatchesAnyElementBesideACapture",
			src: `
				type Second<T> = if T : [_, infer B] { B } else { never }
				type Result = Second<[number, string]>
			`,
			wantSymbolic: `Second<[number, string]>`,
			wantExpanded: "string",
		},
		{
			// Two `_` in one pattern are independent holes. If they shared a declaration the two
			// tuple positions would have to agree and this would take the Else branch.
			name: "TwoWildcardsAreIndependent",
			src: `
				type Both<T> = if T : [_, _] { true } else { false }
				type Result = Both<[number, string]>
			`,
			wantSymbolic: `Both<[number, string]>`,
			wantExpanded: "true",
		},
		{
			// A `_` constrains nothing, so the shape around it still decides the branch.
			name: "SurroundingShapeStillDecides",
			src: `
				type Second<T> = if T : [_, infer B] { B } else { never }
				type Result = Second<[number]>
			`,
			wantSymbolic: `Second<[number]>`,
			wantExpanded: "never",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, ctx, errs := inferTypeNodes(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.wantSymbolic, soltype.Print(nodes["Result"]))
			require.Equal(t, tt.wantExpanded, soltype.Print(expandAliasResidual(ctx, nodes["Result"])))
		})
	}
}

// A conditional whose Check stays abstract keeps its pattern stored, so the pattern is what the
// printer renders. An anonymous binder renders as the `_` the source wrote rather than as
// `infer _`, which is not writable — the parser requires an identifier after `infer`.
func TestInferWildcardPatternRoundTrips(t *testing.T) {
	src := `
		type Second<T> = if T : [_, infer B] { B } else { never }
		fn f<X>(p: Second<X>) -> Second<X> { return p }
	`
	nodes, ctx, errs := inferTypeNodes(t, src)
	require.Empty(t, errs)
	require.Equal(t, "if t0 : [_, infer B] { B } else { never }",
		soltype.Print(expandAliasResidual(ctx, nodes["Second"])))
}
