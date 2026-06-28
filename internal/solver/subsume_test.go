package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// --- M6 PR8: subsume inferred types at finalization ---

// An inferred union whose member a concrete sibling subsumes collapses to that
// sibling at generalization. Each case joins an if/else into a union, which
// combine builds Context-free, and the finalization pass subsumes it. Distinct
// literals that nothing subsumes are left intact.
func TestInferSubsumesInferredUnion(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "literal into primitive",
			src: `
				fn f(b: boolean, n: number) {
					return if b { 1 } else { n }
				}
			`,
			want: "fn (b: boolean, n: number) -> number",
		},
		{
			name: "several literals into primitive",
			src: `
				fn f(b: boolean, n: number) {
					return if b { 1 } else { if b { 2 } else { n } }
				}
			`,
			want: "fn (b: boolean, n: number) -> number",
		},
		{
			name: "string literal into string",
			src: `
				fn f(b: boolean, s: string) {
					return if b { "a" } else { s }
				}
			`,
			want: "fn (b: boolean, s: string) -> string",
		},
		{
			name: "union nested in a tuple",
			src: `
				fn f(b: boolean, n: number) {
					return [if b { 1 } else { n }]
				}
			`,
			want: "fn (b: boolean, n: number) -> [number]",
		},
		{
			name: "distinct literals are not subsumed",
			src: `
				fn f(b: boolean) {
					return if b { 1 } else { 2 }
				}
			`,
			want: "fn (b: boolean) -> 1 | 2",
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

// The finalized inferred type is equalType-equal to the annotated form, so caching
// and annotation round-trip agree. The body infers `1 | number`; the annotation
// resolves to `number`; after subsumption the two are equalType-equal.
func TestInferSubsumedTypeEqualsAnnotation(t *testing.T) {
	scope, _, errs := InferModule(parseModule(t, `
		fn inferred(b: boolean, n: number) {
			return if b { 1 } else { n }
		}
		val annotated: number = 0
	`))
	require.Empty(t, errs)

	inferredFn := schemeType(scope.values["inferred"].Schemes[0]).(*soltype.FuncType)
	annotated := schemeType(scope.values["annotated"].Schemes[0])
	require.True(t, equalType(annotated, inferredFn.Ret),
		"inferred return %s should equal annotated %s",
		soltype.Print(inferredFn.Ret), soltype.Print(annotated))
}

// Subsumption is display-only: the scheme's operative body keeps `1 | number`, so
// assignability is unchanged. A `number` sink accepts the value and a `string`
// sink rejects it, the rejection decomposing the union for-all exactly as it would
// against the un-subsumed form. Both sink keywords are six characters, so `r` sits
// at the same column and the rejection spans match.
func TestInferSubsumedTypePreservesAssignability(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "number sink accepts",
			src: `
				fn f(b: boolean, n: number) {
					val r = if b { 1 } else { n }
					val s: number = r
				}
			`,
			want: nil,
		},
		{
			name: "string sink rejects both union members",
			src: `
				fn f(b: boolean, n: number) {
					val r = if b { 1 } else { n }
					val s: string = r
				}
			`,
			want: []string{
				"4:22-4:23: cannot constrain 1 <: string",
				"4:22-4:23: cannot constrain number <: string",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Equal(t, tt.want, messagesWithSpan(errs))
		})
	}
}

// An inferred intersection collapses to its narrowest member: `{x, ...} & {x, y, ...}`
// reduces to `{x, y, ...}`. It has no source form that survives combine's fold, so it is built directly.
func TestSubsumeFinalIntersectionObjects(t *testing.T) {
	c := newChecker()
	in := newIntersection(nil, parseTypes(t, "{x: number, ...}", "{x: number, y: string, ...}"))
	require.IsType(t, &soltype.IntersectionType{}, in, "precondition: combine leaves both members")
	got := c.subsumeFinal(in)
	require.True(t, equalType(parseType(t, "{x: number, y: string, ...}"), got), "got %s", soltype.Print(got))
}

// A member that still carries a free type variable is left in place. The concrete
// gate skips it, so a scheme whose union is not yet ground is unchanged. The free
// var has no surface form parseType can author, so the union is built directly.
func TestSubsumeFinalLeavesFreeVar(t *testing.T) {
	c := newChecker()
	v := c.ctx.freshVar(0)
	got := c.subsumeFinal(newUnion(nil, []soltype.Type{parseType(t, "number"), v}, false))
	require.IsType(t, &soltype.UnionType{}, got, "got %s", soltype.Print(got))
	require.Len(t, got.(*soltype.UnionType).Types, 2)
}
