package solver

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// TestInferTypeAliasBasic covers a non-generic `type` alias end to end. The type binding
// renders as its definition body, an annotated value that fits the aliased record
// type-checks and keeps the alias name on the value binding, and a primitive alias flows
// structurally.
func TestInferTypeAliasBasic(t *testing.T) {
	tests := []struct {
		name       string
		src        string
		wantValues map[string]string
		wantTypes  map[string]string
	}{
		{
			name:      "RecordAliasBinds",
			src:       `type Point = {x: number, y: number}`,
			wantTypes: map[string]string{"Point": "{x: number, y: number}"},
		},
		{
			name: "AnnotatedValueRendersUnderAliasName",
			src: `
				type Point = {x: number, y: number}
				val p: Point = {x: 1, y: 2}
			`,
			// The value binding keeps the alias name, while the type binding shows the body.
			wantValues: map[string]string{"p": "Point"},
			wantTypes:  map[string]string{"Point": "{x: number, y: number}"},
		},
		{
			name: "PrimitiveAliasAcceptsMatchingValue",
			src: `
				type Foo = number
				val x: Foo = 5
			`,
			wantValues: map[string]string{"x": "Foo"},
			wantTypes:  map[string]string{"Foo": "number"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, types, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
			for name, want := range tt.wantValues {
				require.Equal(t, want, values[name], "value binding %q", name)
			}
			for name, want := range tt.wantTypes {
				require.Equal(t, want, types[name], "type binding %q", name)
			}
		})
	}
}

// TestInferTypeAliasRejectsMissingField checks that an alias is transparent under
// subtyping. An object literal missing a field the aliased record requires is rejected
// against the expanded body, with the full missing-property message.
func TestInferTypeAliasRejectsMissingField(t *testing.T) {
	src := `
		type Point = {x: number, y: number}
		val p: Point = {x: 1}
	`
	_, _, errs := inferSource(t, src)
	require.Len(t, errs, 1)
	require.Equal(t, "object is missing property: y", errs[0].Message())
}

// TestInferTypeAliasRejectsMismatchedPrimitive checks that a primitive alias rejects a
// value of the wrong primitive, since the alias expands to its body at subtyping time.
func TestInferTypeAliasRejectsMismatchedPrimitive(t *testing.T) {
	src := `
		type Foo = number
		val x: Foo = "hi"
	`
	_, _, errs := inferSource(t, src)
	require.Len(t, errs, 1)
	require.Equal(t, `cannot constrain "hi" <: number`, errs[0].Message())
}

// TestInferTypeAliasMissingBodyDoesNotPanic guards the parser error-recovery case where
// `type Foo =` yields a TypeDecl with a nil TypeAnn. Inference runs despite parse errors
// in the real pipeline, so inferTypeDecl must bind the alias to a recovery type rather
// than route the nil annotation to reportUnsupported(nil), whose error has no span.
func TestInferTypeAliasMissingBodyDoesNotPanic(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	// Parse directly so the malformed source reaches inference; the standard harness
	// rejects parse errors, but the real compiler and LSP keep going on a partial AST.
	module, _ := parser.ParseLibFiles(ctx, []*ast.Source{
		{ID: 0, Path: "input.esc", Contents: `type Foo =`},
	})

	// Prove the malformed decl reaches inference: the parsed module must carry a Foo
	// TypeDecl with a nil TypeAnn, the exact shape inferTypeDecl must survive.
	var foo *ast.TypeDecl
	module.Namespaces.Scan(func(_ string, ns *ast.Namespace) bool {
		for _, d := range ns.Decls {
			if td, ok := d.(*ast.TypeDecl); ok && td.Name.Name == "Foo" {
				foo = td
			}
		}
		return true
	})
	require.NotNil(t, foo, "expected a Foo TypeDecl in the parsed module")
	require.Nil(t, foo.TypeAnn, "expected Foo's body to be nil after error recovery")

	// InferModule only collects diagnostics; the nil-Node crash surfaces when a caller
	// renders one, so exercise Span() and Message() on every returned error the way the
	// CLI and LSP formatters do.
	require.NotPanics(t, func() {
		_, _, errs := InferModule(module)
		for _, e := range errs {
			_ = e.Span()
			_ = e.Message()
		}
	})
}

// TestInferTypeAliasShadowingPromiseRejectsArgs checks that a user `type Promise = …`
// alias is not silently reinterpreted as the built-in Promise: applying type arguments to
// the non-generic alias reports an arity mismatch against the user alias.
func TestInferTypeAliasShadowingPromiseRejectsArgs(t *testing.T) {
	src := `
		type Promise = number
		val p: Promise<string> = 5
	`
	_, _, errs := inferSource(t, src)
	require.Len(t, errs, 1)
	require.Equal(t, "type alias `Promise` expects 0 type arguments but got 1", errs[0].Message())
}

// TestExpandAliasUnregisteredReturnsError covers the defensive path in expandAlias: a
// reference whose name is not in the registry yields an ErrorType, which absorbs under
// subtyping rather than looping. inferTypeDecl registers before binding, so this never
// arises from source, but the guard keeps a stray reference from diverging.
func TestExpandAliasUnregisteredReturnsError(t *testing.T) {
	c := newChecker()
	got := c.ctx.expandAlias(&soltype.AliasType{Name: "Missing"})
	require.IsType(t, &soltype.ErrorType{}, got)
}

// TestDescribeAliasType renders an alias reference under its own name in a diagnostic,
// bare or with arguments, matching the printer's surface form.
func TestDescribeAliasType(t *testing.T) {
	require.Equal(t, "Point", describe(&soltype.AliasType{Name: "Point"}))
	require.Equal(t, "Box<number>", describe(&soltype.AliasType{Name: "Box", TypeArgs: []soltype.Type{numT()}}))
}

// TestEqualTypeAliasType compares two alias references: equal when they name the same
// alias with equal arguments, unequal on a different name, argument, or kind.
func TestEqualTypeAliasType(t *testing.T) {
	require.True(t, equalType(&soltype.AliasType{Name: "A"}, &soltype.AliasType{Name: "A"}))
	require.False(t, equalType(&soltype.AliasType{Name: "A"}, &soltype.AliasType{Name: "B"}))
	require.True(t, equalType(
		&soltype.AliasType{Name: "Box", TypeArgs: []soltype.Type{numT()}},
		&soltype.AliasType{Name: "Box", TypeArgs: []soltype.Type{numT()}},
	))
	require.False(t, equalType(
		&soltype.AliasType{Name: "Box", TypeArgs: []soltype.Type{numT()}},
		&soltype.AliasType{Name: "Box"},
	))
	require.False(t, equalType(&soltype.AliasType{Name: "Box"}, numT()))
}

// TestInferTypeAliasFlowsIntoStructuralTarget exercises the alias-on-the-sub-side path in
// constrain: an alias-typed value assigned to a structural target expands to its body and
// checks structurally.
func TestInferTypeAliasFlowsIntoStructuralTarget(t *testing.T) {
	src := `
		type Point = {x: number, y: number}
		val p: Point = {x: 1, y: 2}
		val o: {x: number, y: number} = p
	`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "Point", values["p"])
	require.Equal(t, "{x: number, y: number}", values["o"])
}

// TestInferTypeAliasNamespaced binds an alias declared in a namespace under its
// dep_graph-qualified name, so the registry key carries the namespace prefix.
func TestInferTypeAliasNamespaced(t *testing.T) {
	_, types, errs := inferSources(t, map[string]string{
		"geometry/types.esc": `type Coord = number`,
	})
	require.Empty(t, errs)
	require.Equal(t, "number", types["geometry.Coord"])
}

// TestInferGenericTypeAliasInstantiates covers a generic alias reference end to end: the
// annotated value renders under the alias name with its argument, and a value fitting the
// substituted body type-checks.
func TestInferGenericTypeAliasInstantiates(t *testing.T) {
	src := `
		type Box<T> = {value: T}
		val b: Box<number> = {value: 5}
	`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "Box<number>", values["b"])
}

// TestInferGenericTypeAliasStructuralSubtyping checks that two instances of one generic
// alias relate by expanding both to their substituted bodies and constraining structurally,
// so `Box<number>` flows into `Box<number | string>`.
func TestInferGenericTypeAliasStructuralSubtyping(t *testing.T) {
	src := `
		type Box<T> = {value: T}
		val b: Box<number> = {value: 5}
		val w: Box<number | string> = b
	`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "Box<number>", values["b"])
	require.Equal(t, "Box<number | string>", values["w"])
}

// TestInferGenericTypeAliasRejectsMismatchedArgument checks that a generic alias is
// transparent under subtyping: a value whose field is the wrong type for the substituted
// body is rejected against the expanded structure.
func TestInferGenericTypeAliasRejectsMismatchedArgument(t *testing.T) {
	src := `
		type Box<T> = {value: T}
		val b: Box<number> = {value: "hi"}
	`
	_, _, errs := inferSource(t, src)
	require.Len(t, errs, 1)
	require.Equal(t, `cannot constrain "hi" <: number`, errs[0].Message())
}

// TestInferGenericTypeAliasFillsDefault checks that a trailing parameter with a default may
// be omitted: `Pair<number>` fills `U` from its `U = T` default, so the reference resolves
// as if `Pair<number, number>` were written and a matching tuple type-checks.
func TestInferGenericTypeAliasFillsDefault(t *testing.T) {
	src := `
		type Pair<T, U = T> = [T, U]
		val p: Pair<number> = [1, 2]
	`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "Pair<number, number>", values["p"])
}

// TestInferGenericTypeAliasDefaultConstrainsBody checks that the default-filled argument
// reaches the expanded body: with `U` defaulted to `T` = number, a tuple whose second
// element is a string is rejected.
func TestInferGenericTypeAliasDefaultConstrainsBody(t *testing.T) {
	src := `
		type Pair<T, U = T> = [T, U]
		val p: Pair<number> = [1, "hi"]
	`
	_, _, errs := inferSource(t, src)
	require.Len(t, errs, 1)
	require.Equal(t, `cannot constrain "hi" <: number`, errs[0].Message())
}

// TestInferRecursiveTypeAliasBinds covers a non-generic self-recursive alias. The type
// binding renders the self-reference under its own name at the knot rather than expanding
// forever, and a value inhabiting the type through the optional recursive field type-checks.
func TestInferRecursiveTypeAliasBinds(t *testing.T) {
	src := `
		type IntList = {head: number, tail?: IntList}
		val one: IntList = {head: 1}
		val two: IntList = {head: 1, tail: {head: 2}}
	`
	values, types, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "{head: number, tail?: IntList}", types["IntList"])
	require.Equal(t, "IntList", values["one"])
	require.Equal(t, "IntList", values["two"])
}

// TestInferRecursiveTypeAliasSubtypingSubject exercises a recursive alias as a subtyping
// subject: an alias-typed binding assigned to another binding of the same alias expands
// both sides and walks the recursive field, closing the cycle through the alias's own name.
func TestInferRecursiveTypeAliasSubtypingSubject(t *testing.T) {
	src := `
		type IntList = {head: number, tail?: IntList}
		val a: IntList = {head: 1}
		val b: IntList = a
	`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "IntList", values["a"])
	require.Equal(t, "IntList", values["b"])
}

// TestInferGenericRecursiveTypeAliasSubtypingSubject is the divergence case the canonical
// recursion guard exists for: a generic instance List<number> used as a subtyping subject.
// expandAlias substitutes the argument into a fresh node each unfold, so a pointer-identity
// guard would mint a new List<number> every lap and loop. Keying on the canonical identity
// formed from the alias and its arguments closes the cycle.
func TestInferGenericRecursiveTypeAliasSubtypingSubject(t *testing.T) {
	src := `
		type List<T> = {head: T, tail?: List<T>}
		val a: List<number> = {head: 1, tail: {head: 2}}
		val b: List<number> = a
	`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "List<number>", values["a"])
	require.Equal(t, "List<number>", values["b"])
}

// TestInferGenericRecursiveTypeAliasRejectsMismatch checks that a generic recursive alias
// stays transparent under subtyping: a nested value whose recursive field carries the wrong
// element type is rejected against the expanded body, with the full message.
func TestInferGenericRecursiveTypeAliasRejectsMismatch(t *testing.T) {
	src := `
		type List<T> = {head: T, tail?: List<T>}
		val a: List<number> = {head: 1, tail: {head: "hi"}}
	`
	_, _, errs := inferSource(t, src)
	require.Len(t, errs, 1)
	require.Equal(t, `cannot constrain "hi" <: number`, errs[0].Message())
}

// TestInferMutuallyRecursiveTypeAliases covers a mutual alias group: each body names the
// other, so both must be pre-bound before either body resolves. Both render under their own
// names, and an alias-typed binding assigned across the pair closes the cross-alias cycle.
func TestInferMutuallyRecursiveTypeAliases(t *testing.T) {
	src := `
		type Ping = {next?: Pong}
		type Pong = {next?: Ping}
		val a: Ping = {}
		val b: Ping = a
	`
	values, types, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "{next?: Pong}", types["Ping"])
	require.Equal(t, "{next?: Ping}", types["Pong"])
	require.Equal(t, "Ping", values["a"])
	require.Equal(t, "Ping", values["b"])
}

// TestInferGenericTypeAliasArityErrors covers the two out-of-range counts. Supplying more
// than the total parameter count and fewer than the required count each report a single
// AliasArityMismatchError, whose message states a range when a default makes a parameter
// optional and a single count when every parameter is required.
func TestInferGenericTypeAliasArityErrors(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "TooManyArgsWithDefault",
			src: `
				type Pair<T, U = T> = [T, U]
				val p: Pair<number, string, boolean> = [1, "a"]
			`,
			want: "type alias `Pair` expects between 1 and 2 type arguments but got 3",
		},
		{
			name: "TooFewRequiredWithDefault",
			src: `
				type Pair<T, U = T> = [T, U]
				val p: Pair = [1, 2]
			`,
			want: "type alias `Pair` expects between 1 and 2 type arguments but got 0",
		},
		{
			name: "TooFewAllRequired",
			src: `
				type Pair<T, U> = [T, U]
				val p: Pair<number> = [1, 2]
			`,
			want: "type alias `Pair` expects 2 type arguments but got 1",
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

// TestInferGenericTypeAliasParamBounds covers the bound a generic alias declares on a type
// parameter, `type Box<T: string>`. A reference supplying an argument outside the bound is
// rejected at the reference, and one inside it is accepted. A bound may name a sibling
// parameter, so the reference's own arguments are substituted into the bound before the
// comparison, and an argument filled from a parameter's default is checked the same way.
func TestInferGenericTypeAliasParamBounds(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "ArgumentOutsideBound",
			src: `
				type Box<T: string> = {v: T}
				val b: Box<number> = {v: 1}
			`,
			want: []string{"cannot constrain number <: string"},
		},
		{
			name: "ArgumentInsideBound",
			src: `
				type Box<T: string> = {v: T}
				val b: Box<"a"> = {v: "a"}
			`,
		},
		{
			name: "UnboundedParamAcceptsAnyArgument",
			src: `
				type Id<T> = T
				val a: Id<number> = 1
				val b: Id<string> = "hi"
			`,
		},
		{
			name: "IntersectionBound",
			src: `
				type Box<T: string & "a"> = {v: T}
				val b: Box<"b"> = {v: "b"}
			`,
			want: []string{`cannot constrain "b" <: "a"`},
		},
		{
			name: "SiblingBoundSatisfied",
			src: `
				type P<A, B: A> = [A, B]
				val p: P<number, 1> = [1, 1]
			`,
		},
		{
			name: "SiblingBoundViolated",
			src: `
				type P<A, B: A> = [A, B]
				val p: P<string, 1> = ["a", 1]
			`,
			want: []string{"cannot constrain 1 <: string"},
		},
		{
			name: "DefaultedArgumentChecked",
			src: `
				type Pair<T, U: string = T> = [T, U]
				val p: Pair<number> = [1, 1]
			`,
			want: []string{"cannot constrain number <: string"},
		},
		{
			name: "AliasBodyReferenceChecked",
			src: `
				type Box<T: string> = {v: T}
				type Outer = Box<number>
			`,
			want: []string{"cannot constrain number <: string"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			var msgs []string
			for _, e := range errs {
				msgs = append(msgs, e.Message())
			}
			require.Equal(t, tt.want, msgs)
		})
	}
}

// TestInferGenericTypeAliasParamBoundBlamesArgument checks that the mismatch points at the
// offending type argument in the reference rather than at the alias declaration. In
// `val b: Box<number>` the reported span covers `number`.
func TestInferGenericTypeAliasParamBoundBlamesArgument(t *testing.T) {
	src := "type Box<T: string> = {v: T}\nval b: Box<number> = {v: 1}"
	_, _, errs := inferSource(t, src)
	require.Len(t, errs, 1)
	require.Equal(t, "cannot constrain number <: string", errs[0].Message())
	span := errs[0].Span()
	require.Equal(t, 2, span.Start.Line)
	require.Equal(t, 12, span.Start.Column)
	require.Equal(t, 2, span.End.Line)
	require.Equal(t, 18, span.End.Column)
}

// TestInferGenericTypeAliasParamBoundReportedOncePerReference checks that the bound is
// checked where a reference resolves, not where the alias expands. Each of the two bad
// references reports exactly once, and a recursive alias whose parameter carries a bound
// reports once for the whole reference rather than once per expansion lap.
func TestInferGenericTypeAliasParamBoundReportedOncePerReference(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "TwoBadReferences",
			src: `
				type Box<T: string> = {v: T}
				val a: Box<number> = {v: 1}
				val b: Box<boolean> = {v: true}
			`,
			want: []string{
				"cannot constrain number <: string",
				"cannot constrain boolean <: string",
			},
		},
		{
			name: "RecursiveAlias",
			src: `
				type List<T: string> = {head: T, tail?: List<T>}
				val l: List<number> = {head: 1}
			`,
			want: []string{"cannot constrain number <: string"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			var msgs []string
			for _, e := range errs {
				msgs = append(msgs, e.Message())
			}
			require.Equal(t, tt.want, msgs)
		})
	}
}

// TestTypeParamBoundEnforcedInEveryPosition is the regression guard for the three positions
// that already enforced a type parameter's bound before the alias position joined them: a
// generic function declaration, a generic function annotation, and a class reference. Each
// instantiates its parameter at a type the bound rejects and reports the same mismatch.
func TestTypeParamBoundEnforcedInEveryPosition(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "GenericFunctionDeclaration",
			src: `
				fn f<A: string>(a: A) -> A { return a }
				val x = f(5)
			`,
			want: "cannot constrain 5 <: string",
		},
		{
			name: "GenericFunctionAnnotation",
			src: `
				val g: fn<A: string>(a: A) -> A = fn (a) { return a }
				val x = g(5)
			`,
			want: "cannot constrain 5 <: string",
		},
		{
			name: "ClassReference",
			src: `
				class Box<T: string> { value: T }
				val b = Box(1)
			`,
			want: "cannot constrain 1 <: string",
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

// TestInferGenericTypeAliasBoundOnVariableArgument covers a type argument that is itself a
// type variable rather than a concrete type. The comparison is a live constraint, so the
// alias's bound lands on that variable and the diagnostic surfaces where the variable is
// instantiated, the same way the generic-function and class positions behave. Nothing is
// reported at the declaration that forwards the variable.
func TestInferGenericTypeAliasBoundOnVariableArgument(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "ForwardingDeclarationIsQuiet",
			src: `
				type Box<T: string> = {v: T}
				fn g<U>(u: U) -> Box<U> { return {v: u} }
			`,
		},
		{
			name: "InstantiationReportsThroughReturn",
			src: `
				type Box<T: string> = {v: T}
				fn g<U>(u: U) -> Box<U> { return {v: u} }
				val x = g(1)
			`,
			want: []string{"cannot constrain 1 <: string"},
		},
		{
			name: "InstantiationReportsThroughParameter",
			src: `
				type Box<T: string> = {v: T}
				fn f<U>(b: Box<U>) -> U { return b.v }
				val x = f({v: 1})
			`,
			want: []string{"cannot constrain 1 <: string"},
		},
		{
			name: "InstantiationReportsThroughClassField",
			src: `
				type Box<T: string> = {v: T}
				class C<U> { b: Box<U> }
				val c = C({v: 1})
			`,
			want: []string{"cannot constrain 1 <: string"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			var msgs []string
			for _, e := range errs {
				msgs = append(msgs, e.Message())
			}
			require.Equal(t, tt.want, msgs)
		})
	}
}

// TestInferGenericTypeAliasBoundOnSiblingAliasArgument covers a type argument naming an
// alias in the same dep_graph component, whose body is still nil while the reference
// resolves. The comparison is queued and replayed once every body in the component is
// filled, so the finished body is what the bound sees. Checking against the half-built
// sibling instead would expand it to an error type, which absorbs and reports nothing.
func TestInferGenericTypeAliasBoundOnSiblingAliasArgument(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "SelfRecursiveArgument",
			src: `
				type Box<T: string> = {v: T}
				type A = {b: Box<A>}
			`,
			want: []string{"cannot constrain object <: string"},
		},
		{
			name: "MutuallyRecursiveArgument",
			src: `
				type Box<T: string> = {v: T}
				type A = {b: Box<B>}
				type B = A
			`,
			want: []string{"cannot constrain object <: string"},
		},
		{
			name: "NonRecursiveSiblingArgument",
			src: `
				type Box<T: string> = {v: T}
				type B = {x: number}
				type A = {b: Box<B>}
			`,
			want: []string{"cannot constrain object <: string"},
		},
		{
			name: "SatisfiedSiblingArgument",
			src: `
				type Box<T: string> = {v: T}
				type S = "a"
				type A = {b: Box<S>}
			`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			var msgs []string
			for _, e := range errs {
				msgs = append(msgs, e.Message())
			}
			require.Equal(t, tt.want, msgs)
		})
	}
}

// TestInferGenericTypeAliasBoundUnderConditional guards the case that rules out reading a
// parameter's inferred upper bounds instead of its declared constraint. Resolving the then
// branch of `if T : string` constrains X's own T against Box's bound, which leaves string on
// that variable even though X declares T unbounded. `X<number>` selects the else branch and
// is number, so reading the variable's bounds would reject a well-typed program.
func TestInferGenericTypeAliasBoundUnderConditional(t *testing.T) {
	src := `
		type Box<T: string> = {v: T}
		type X<T> = if T : string { Box<T> } else { number }
		val a: X<number> = 1
		val b: X<"a"> = {v: "a"}
	`
	_, _, errs := inferSource(t, src)
	require.Empty(t, errs)
}

// TestInferGenericTypeAliasRequiredParamAfterDefault covers a required parameter written after a
// defaulted one. Filling T from its default would leave U with no argument, so the accepted
// count runs up to and past the last parameter with no default and `Pair<string>` is rejected
// rather than silently binding U to a fresh var.
func TestInferGenericTypeAliasRequiredParamAfterDefault(t *testing.T) {
	_, _, errs := inferSource(t, `
		type Pair<T = number, U> = [T, U]
		val p: Pair<string> = ["a", 1]
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "type alias `Pair` expects 2 type arguments but got 1", errs[0].Message())
}

// TestInferGenericTypeAliasSurplusTypeArgStillResolved checks that a type argument past the last
// declared parameter is still resolved even though it is dropped from the instance, so an error
// inside it is reported rather than swallowed by the arity diagnostic.
func TestInferGenericTypeAliasSurplusTypeArgStillResolved(t *testing.T) {
	_, _, errs := inferSource(t, `
		type Id = number
		val a: Id<DoesNotExist> = 1
	`)
	var msgs []string
	for _, e := range errs {
		msgs = append(msgs, e.Message())
	}
	require.Equal(t, []string{
		"type alias `Id` expects 0 type arguments but got 1",
		"Unsupported: TypeRefTypeAnn",
	}, msgs)
}

// TestInferTypeParamDefaultOutsideBoundReportedOnce checks that a default outside its bound is
// reported at the declaration and not again at each reference. The declaration-site check in
// resolveTypeParams already compared the two, so a reference that omits the argument skips the
// use-site comparison when substitution moved neither the bound nor the default. A reference
// whose arguments do move one of them is still checked, since only the reference knows what the
// comparison is between.
func TestInferTypeParamDefaultOutsideBoundReportedOnce(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "NoUseSite",
			src:  `type Box<T: string = number> = {v: T}`,
			want: []string{"cannot constrain number <: string"},
		},
		{
			name: "TwoUseSitesOmitTheArgument",
			src: `
				type Box<T: string = number> = {v: T}
				val a: Box = {v: 1}
				val b: Box = {v: 2}
			`,
			want: []string{"cannot constrain number <: string"},
		},
		{
			name: "BoundNamesSiblingSoUseSiteStillChecks",
			src: `
				type P<A, B: A = number> = [A, B]
				val p: P<string> = ["a", 1]
			`,
			want: []string{"cannot constrain number <: string"},
		},
		{
			name: "DefaultNamesSiblingSoUseSiteStillChecks",
			src: `
				type Pair<T, U: string = T> = [T, U]
				val p: Pair<number> = [1, 1]
			`,
			want: []string{"cannot constrain number <: string"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			var msgs []string
			for _, e := range errs {
				msgs = append(msgs, e.Message())
			}
			require.Equal(t, tt.want, msgs)
		})
	}
}
