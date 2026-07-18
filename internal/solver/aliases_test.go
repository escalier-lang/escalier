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
// guard would mint a new List<number> every lap and loop; keying on the canonical
// (alias, args) identity closes the cycle.
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
