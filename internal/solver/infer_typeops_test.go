package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/dep_graph"
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
	return newTypeEvaluator(ctx).reduce(ty)
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
			// A class projects its instance body, the same key set an object yields.
			name: "Class",
			src: `
				class Point {
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
