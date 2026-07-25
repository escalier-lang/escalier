package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/dep_graph"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

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
	return newTypeEvaluator(ctx, set.NewSet[constraintKey]()).reduce(ty)
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
			// An inexact object carries an open key tail, so its key union is inexact: the known
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
			// keyof distributes over a union operand, so each member's keys union together.
			name: "Union",
			src: `
				type U = {a: number} | {b: number}
				type Result = keyof U
			`,
			wantSymbolic: "keyof U",
			wantExpanded: `"a" | "b"`,
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
			want: map[string]string{"h": "fn (k: keyof {x: number, y: string}) -> void"},
		},
		{
			name: "UnionOperand",
			src:  `fn g<T>(x: keyof (T | {a: number})) {}`,
			want: map[string]string{"g": "fn <T>(x: keyof (T | {a: number})) -> void"},
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
		require.Equal(t, "fn <T>(k: keyof keyof T) -> void", values["f"])
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

// Checking a value against `keyof` of an expanding recursive alias terminates instead of looping.
// The reduction is budget-truncated and leaves a `keyof A<…>` residual, so constrain does not
// recurse on it — re-expanding would grow the operand without bound — and the residual stays
// inert, conservatively rejecting the value. The point of the test is termination; the precise
// rejection is a consequence of the truncation, which CheckRegular will reject at definition time
// in a later milestone.
func TestInferKeyofExpandingAliasTerminates(t *testing.T) {
	_, _, errs := inferSource(t, `
		type A<T> = {x: T} | A<{y: T}>
		val k: keyof A<number> = "x"
	`)
	require.Len(t, errs, 1)
	require.IsType(t, &CannotConstrainError{}, errs[0])
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
			want: map[string]string{"h": `fn (k: {x: number, y: string}["x"]) -> void`},
		},
		{
			name: "KeyofIndex",
			src:  `fn g<T>(k: T[keyof T]) {}`,
			want: map[string]string{"g": "fn <T>(k: T[keyof T]) -> void"},
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

// Checking a value against a tuple spread of an expanding recursive alias terminates instead of
// looping. The reduction is budget-truncated and leaves a `[...A<…>, …]` residual, so constrain
// does not recurse on it — re-expanding would grow the operand without bound — and the residual
// stays inert, conservatively rejecting the value. The point is termination; the precise rejection
// is a consequence of the truncation, which CheckRegular will reject at definition time in a later
// milestone.
func TestInferTupleSpreadExpandingAliasTerminates(t *testing.T) {
	_, _, errs := inferSource(t, `
		type A<T> = [T, ...A<[T]>]
		val r: [...A<number>, boolean] = [1, 2]
	`)
	require.Len(t, errs, 1)
	require.IsType(t, &CannotConstrainError{}, errs[0])
	require.Equal(t, "3:36-3:42: cannot constrain tuple <: [...A<number>, boolean]", msgWithSpan(errs[0]))
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
				require.Equal(t, "fn <T>(k: "+tt.wantSymbolic+") -> void", values["f"])
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
			want: map[string]string{"f": "fn <T>(x: if T : number { string } else { boolean }) -> void"},
		},
		{
			name: "TypeParamExtends",
			src:  `fn g<T>(x: if number : T { string } else { boolean }) {}`,
			want: map[string]string{"g": "fn <T>(x: if number : T { string } else { boolean }) -> void"},
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

// A symbolic conditional whose Extends holds an `infer U` renders the binding under its `infer`
// prefix in a diagnostic. A type-parameter Check keeps the conditional symbolic — the `infer` matcher
// never runs and the branch is never expanded — so the rejected `return k` names the whole residual,
// and describe's InferType arm shows the Extends position as `Promise<infer U>`. describe is the raw
// mid-constrain renderer, so the Check shows as the raw var `t1` and the Then's `U` reference as the
// raw var `t2`, not the coalesced printer's names.
func TestInferCondInferResidualErrorMessage(t *testing.T) {
	_, _, errs := inferSource(t, `fn f<T>(k: if T : Promise<infer U> { U } else { boolean }) -> number { return k }`)
	require.Len(t, errs, 1)
	require.IsType(t, &CannotConstrainError{}, errs[0])
	require.Equal(t, "1:12-1:56: cannot constrain if t1 : Promise<infer U> { t2 } else { boolean } <: number", msgWithSpan(errs[0]))
}

// A conditional whose Extends holds an `infer U` reduces by matching the ground Check against the
// Extends pattern structurally, binding `U` to the matched position and substituting it into the
// selected branch. Each case names a ground Check and an Extends carrying an `infer`, and asserts
// the reduced branch. The cases cover the pattern shapes the matcher decomposes: a tuple element, a
// function parameter, a function return, a promise payload, an object property, two positions
// sharing one `infer`, and a Check whose shape does not match the pattern so the Else branch is
// taken.
func TestInferCondInferReduction(t *testing.T) {
	tests := []struct {
		name         string
		src          string
		wantExpanded string
	}{
		{
			// A tuple pattern binds `U` to the element at the `infer` position.
			name:         "TupleElement",
			src:          `type Result = if [number, string] : [infer U, string] { U } else { boolean }`,
			wantExpanded: "number",
		},
		{
			// A function pattern binds `U` to the parameter at the `infer` position.
			name:         "FunctionParameter",
			src:          `type Result = if fn (x: number) -> string : fn (x: infer U) -> string { U } else { boolean }`,
			wantExpanded: "number",
		},
		{
			// A function pattern binds `R` to the return at the `infer` position.
			name:         "FunctionReturn",
			src:          `type Result = if fn (x: number) -> string : fn (x: number) -> infer R { R } else { boolean }`,
			wantExpanded: "string",
		},
		{
			// A promise pattern binds `U` to the payload, the extraction Awaited<T> builds on.
			name:         "PromisePayload",
			src:          `type Result = if Promise<number> : Promise<infer U> { U } else { boolean }`,
			wantExpanded: "number",
		},
		{
			// An object pattern binds `U` to the named property's type.
			name:         "ObjectProperty",
			src:          `type Result = if {value: number} : {value: infer U} { U } else { boolean }`,
			wantExpanded: "number",
		},
		{
			// Two positions sharing one `infer U` capture the leftmost, so the branch reads the
			// first element's type.
			name:         "RepeatedInferName",
			src:          `type Result = if [number, string] : [infer U, infer U] { U } else { boolean }`,
			wantExpanded: "number",
		},
		{
			// A Check whose shape does not match the pattern takes the Else branch: a primitive is
			// not a tuple, so no `infer` binds.
			name:         "MismatchTakesElse",
			src:          `type Result = if number : [infer U, string] { U } else { boolean }`,
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

// constrain reduces an `infer` conditional to the selected branch to check satisfaction, expanding a
// generic alias's argument into the Check before matching. A value matching the captured type is
// accepted; a mismatch is rejected against that type, so the diagnostic names the branch the
// conditional resolved to. Each case defines a generic alias whose body extracts a type through
// `infer` and checks a value against an instantiation of it.
func TestInferCondInferConstraint(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string // "" ⇒ expect no error
	}{
		{
			name: "TupleFirstAccepted",
			src: `
				type First<T> = if T : [infer U, string] { U } else { boolean }
				val v: First<[number, string]> = 5
			`,
		},
		{
			name: "TupleFirstRejected",
			src: `
				type First<T> = if T : [infer U, string] { U } else { boolean }
				val v: First<[number, string]> = "hi"
			`,
			wantErr: `cannot constrain "hi" <: number`,
		},
		{
			name: "PromisePayloadAccepted",
			src: `
				type Payload<T> = if T : Promise<infer U> { U } else { boolean }
				val v: Payload<Promise<string>> = "hi"
			`,
		},
		{
			name: "ReturnTypeRejected",
			src: `
				type Ret<T> = if T : fn (x: number) -> infer R { R } else { boolean }
				val v: Ret<fn (x: number) -> string> = 5
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

// A conditional whose Check is a naked type parameter distributes over a union argument member-wise,
// matching TypeScript. Each member evaluates the conditional independently, so `Widen` maps every
// member of `"a" | "b"` through its own branch decision and unions the results. Without
// distribution the whole union `"a" | "b"` would fail the `<: "a"` probe and take the Else branch
// alone; distribution instead selects Then for `"a"` and Else for `"b"`, so the result is the union
// `number | boolean`. constrain accepts a value the distributed union covers and rejects one it does
// not.
func TestInferCondDistribution(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string // "" ⇒ expect no error
	}{
		{
			// number rides the "a" member's Then branch, present only because distribution evaluates
			// that member on its own.
			name: "MemberThenBranchAccepted",
			src: `
				type Widen<T> = if T : "a" { number } else { boolean }
				val v: Widen<"a" | "b"> = 5
			`,
		},
		{
			// boolean rides the "b" member's Else branch.
			name: "MemberElseBranchAccepted",
			src: `
				type Widen<T> = if T : "a" { number } else { boolean }
				val v: Widen<"a" | "b"> = true
			`,
		},
		{
			// A string is in neither branch of the distributed union number | boolean.
			name: "OutsideDistributedUnionRejected",
			src: `
				type Widen<T> = if T : "a" { number } else { boolean }
				val v: Widen<"a" | "b"> = "hi"
			`,
			wantErr: `cannot constrain "hi" <: number | boolean`,
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

// A distributive conditional reduces to the distributed union directly. `Widen<"a" | "b">` maps `"a"`
// to its Then branch `number` and `"b"` to its Else branch `boolean`, so the reduction is the union
// `number | boolean`. The alias reference expands its union argument into the naked Check before
// distributing.
func TestInferCondDistributionReduction(t *testing.T) {
	nodes, ctx, errs := inferTypeNodes(t, `
		type Widen<T> = if T : "a" { number } else { boolean }
		type Result = Widen<"a" | "b">
	`)
	require.Empty(t, errs)
	ref, ok := nodes["Result"].(*soltype.AliasType)
	require.True(t, ok)
	reduced := expandResidual(ctx, ctx.expandAlias(ref))
	require.Equal(t, "number | boolean", soltype.Print(reduced))
}

// A conditional is marked distributive only when its Check is written as a naked type parameter, so
// distribution fires for exactly the TypeScript-distributive conditionals. A bare `T` reference is
// distributive; a wildcard `_` Check and a Check whose reference does not resolve each reduce to a
// fresh variable but are not naked parameters, so neither is marked distributive.
func TestInferCondDistributeFlag(t *testing.T) {
	t.Run("NakedParameter", func(t *testing.T) {
		nodes, _, errs := inferTypeNodes(t, `type Choose<T> = if T : number { string } else { boolean }`)
		require.Empty(t, errs)
		cond, ok := nodes["Choose"].(*soltype.CondType)
		require.True(t, ok)
		require.True(t, cond.Distribute)
	})
	t.Run("WildcardCheckNotDistributive", func(t *testing.T) {
		nodes, _, errs := inferTypeNodes(t, `type Result = if _ : number { string } else { boolean }`)
		require.Empty(t, errs)
		cond, ok := nodes["Result"].(*soltype.CondType)
		require.True(t, ok)
		require.False(t, cond.Distribute)
	})
	t.Run("UnresolvedCheckNotDistributive", func(t *testing.T) {
		nodes, _, errs := inferTypeNodes(t, `type Result = if Bogus : number { string } else { boolean }`)
		// Bogus does not resolve, so the Check recovers to a fresh variable and the annotation
		// reports the unresolved reference.
		require.Len(t, errs, 1)
		cond, ok := nodes["Result"].(*soltype.CondType)
		require.True(t, ok)
		require.False(t, cond.Distribute)
	})
}

// An `infer` clause is legal only inside a conditional type's Extends operand. Outside it — here in a
// bare type alias — there is no conditional to bind the name, so the annotation reports one
// UnsupportedFeatureError and recovers.
func TestInferInferOutsideCondRejected(t *testing.T) {
	_, _, errs := inferSource(t, `type Result = infer U`)
	require.Len(t, errs, 1)
	require.IsType(t, &UnsupportedFeatureError{}, errs[0])
	require.Equal(t, "Unsupported: infer clause outside a conditional type's extends operand", errs[0].Message())
}

// An `infer` in a conditional's Then branch — not its Extends — is rejected: `infer` binds a capture
// only in the pattern position. The Then is resolved with the `infer`-legal context cleared, so it
// reports the same unsupported feature as an `infer` in a bare alias.
func TestInferInferInThenRejected(t *testing.T) {
	_, _, errs := inferSource(t, `type Result = if number : number { infer U } else { boolean }`)
	require.Len(t, errs, 1)
	require.IsType(t, &UnsupportedFeatureError{}, errs[0])
	require.Equal(t, "Unsupported: infer clause outside a conditional type's extends operand", errs[0].Message())
}

// Checking a value against a self-referential conditional alias terminates instead of looping. A
// conditional decides its branch by re-entering constrain to probe `Check <: Extends`, and constrain
// expands an alias Check back into the same conditional, so `type Bad = if Bad : number { … }` would
// recurse without bound if the probe started a fresh cycle-detection set. The probe reuses the
// caller's set, so the repeated `Bad <: number` state closes the cycle. The point of the test is
// termination; the branch the truncated cycle selects is a consequence, which CheckRegular will
// reject at definition time in a later milestone.
func TestInferCondRecursiveAliasTerminates(t *testing.T) {
	_, _, errs := inferSource(t, `
		type Bad = if Bad : number { number } else { string }
		val x: Bad = 5
	`)
	require.Empty(t, errs)
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
