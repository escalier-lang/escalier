package solver

import (
	"fmt"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/stretchr/testify/require"
)

// firstBorrowFinder records the first borrow expression a walk reaches.
type firstBorrowFinder struct {
	ast.DefaultVisitor
	found *ast.BorrowExpr
}

func (v *firstBorrowFinder) EnterExpr(e ast.Expr) bool {
	if b, ok := e.(*ast.BorrowExpr); ok && v.found == nil {
		v.found = b
	}
	return v.found == nil
}

// TestIndexPlace covers the place exprPlace resolves for an index expression. The program's
// function `go` borrows the expression, and the test renders the place of the borrow's
// operand. An index whose type is one non-negative integer literal names its own element,
// and any other index names the whole container.
func TestIndexPlace(t *testing.T) {
	tests := []struct {
		name   string
		params string
		expr   string
		want   string
	}{
		{name: "LiteralIndex", params: "xs: Array<{x: number}>", expr: "xs[0]", want: "xs[0]"},
		{name: "IndexTypedAsALiteral", params: "xs: Array<{x: number}>, i: 0", expr: "xs[i]", want: "xs[0]"},
		{name: "IndexTypedAsNumber", params: "xs: Array<{x: number}>, i: number", expr: "xs[i]", want: "xs"},
		{name: "IndexTypedAsAUnionOfLiterals", params: "xs: Array<{x: number}>, i: 0 | 1", expr: "xs[i]", want: "xs"},
		{name: "NegativeLiteralIndex", params: "xs: Array<{x: number}>", expr: "xs[-1]", want: "xs"},
		{name: "FractionalLiteralIndex", params: "xs: Array<{x: number}>", expr: "xs[1.5]", want: "xs"},
		{name: "StringLiteralIndex", params: "o: {a: {x: number}}", expr: `o["a"]`, want: "o.a"},
		{name: "StringLiteralIndexThatIsNotAnIdentifier", params: `o: {"a.b": {x: number}}`, expr: `o["a.b"]`, want: `o["a.b"]`},
		{name: "NestedLiteralIndices", params: "xs: Array<{a: Array<{x: number}>}>", expr: "xs[0].a[1]", want: "xs[0].a[1]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := fmt.Sprintf("fn go(%s) -> number {\n\tval r = &%s\n\treturn 0\n}", tt.params, tt.expr)
			module := parseModule(t, src)
			res := InferModuleAgainstStdlib(module, benchTree, Options{})
			require.Empty(t, res.Errors)

			decl := findFuncDecl(t, module, "go")
			finder := &firstBorrowFinder{}
			decl.Body.Accept(finder)
			require.NotNil(t, finder.found)

			place, ok := res.checker.exprPlace(finder.found.Arg)
			require.True(t, ok)
			d := &bodyDataflow{table: res.Info.ownershipEventsOf(decl)}
			require.Equal(t, tt.want, d.placeName(place))
		})
	}
}

// findFuncDecl returns the top-level function declaration named name in module.
func findFuncDecl(t *testing.T, module *ast.Module, name string) *ast.FuncDecl {
	t.Helper()

	var decl *ast.FuncDecl
	module.Namespaces.Scan(func(_ string, ns *ast.Namespace) bool {
		for _, d := range ns.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Name != nil && fd.Name.Name == name {
				decl = fd
			}
		}
		return true
	})
	require.NotNil(t, decl)
	return decl
}

// TestLiteralIndexConflicts covers the ownership diagnostics of places an index names. Two
// different literal indices name disjoint elements, so a borrow of one does not conflict
// with a write or a borrow of the other. The same literal index names one element twice. An
// index typed `number` names the whole container, which overlaps every element. A tuple
// pattern's element and an index read of the same position name one place in the dataflow
// pass. The current checks move or borrow a whole tuple for a tuple pattern, so they report
// the tuple itself. Each program runs with the dataflow flag off and on, and each run
// reports every diagnostic its want lists, with spans. wantDataflow is the flag-on result
// when it differs from want.
func TestLiteralIndexConflicts(t *testing.T) {
	cases := []struct {
		name         string
		src          string
		want         []string
		wantDataflow []string
	}{
		{
			name: "WriteToADifferentLiteralIndex",
			src: `
				fn go() -> number {
					val mut xs: Array<{x: number}> = [{x: 1}, {x: 2}]
					val r = &xs[0]
					xs[1].x = 3
					return r.x
				}
			`,
		},
		{
			name: "WriteToTheSameLiteralIndex",
			src: `
				fn go() -> number {
					val mut xs: Array<{x: number}> = [{x: 1}, {x: 2}]
					val r = &xs[0]
					xs[0].x = 3
					return r.x
				}
			`,
			want: []string{"5:6-5:13: cannot assign to 'xs[0].x' while it is borrowed as immutable"},
		},
		{
			name: "WriteThroughAnIndexTypedAsTheSameLiteral",
			src: `
				fn go(i: 0) -> number {
					val mut xs: Array<{x: number}> = [{x: 1}, {x: 2}]
					val r = &xs[0]
					xs[i].x = 3
					return r.x
				}
			`,
			want: []string{"5:6-5:13: cannot assign to 'xs[i].x' while it is borrowed as immutable"},
		},
		{
			name: "WriteThroughANumberIndex",
			src: `
				fn go(i: number) -> number {
					val mut xs: Array<{x: number}> = [{x: 1}, {x: 2}]
					val r = &xs[0]
					xs[i].x = 3
					return r.x
				}
			`,
			want: []string{"5:6-5:13: cannot assign to 'xs[i].x' while it is borrowed as immutable"},
		},
		{
			name: "BorrowsOfDifferentLiteralIndices",
			src: `
				declare fn readWrite(a: &{x: number}, b: &mut {x: number}) -> undefined
				fn go() {
					val mut xs: Array<{x: number}> = [{x: 1}, {x: 2}]
					readWrite(&xs[0], &mut xs[1])
				}
			`,
		},
		{
			name: "BorrowsOfTheSameLiteralIndex",
			src: `
				declare fn readWrite(a: &{x: number}, b: &mut {x: number}) -> undefined
				fn go() {
					val mut xs: Array<{x: number}> = [{x: 1}, {x: 2}]
					readWrite(&xs[0], &mut xs[0])
				}
			`,
			want: []string{"5:24-5:34: cannot borrow 'xs[0]' as mutable while it is borrowed as immutable"},
		},
		{
			name: "BorrowsOfALiteralIndexAndANumberIndex",
			src: `
				declare fn readWrite(a: &{x: number}, b: &mut {x: number}) -> undefined
				fn go(i: number) {
					val mut xs: Array<{x: number}> = [{x: 1}, {x: 2}]
					readWrite(&xs[0], &mut xs[i])
				}
			`,
			want: []string{"5:24-5:34: cannot borrow 'xs' as mutable while 'xs[0]' is borrowed as immutable"},
		},
		{
			name: "ReadsOfTwoLiteralIndices",
			src: `
				fn go() {
					val xs: Array<{x: number}> = [{x: 1}, {x: 2}]
					val a = xs[0]
					val b = xs[1]
				}
			`,
		},
		{
			name: "ReadsOfOneLiteralIndexTwice",
			src: `
				fn go() {
					val xs: Array<{x: number}> = [{x: 1}, {x: 2}]
					val a = xs[0]
					val b = xs[0]
				}
			`,
			want: []string{"5:14-5:19: use of moved value 'xs[0]'"},
		},
		{
			// The pattern moves t[0], which the read names again.
			name: "PatternElementThenIndexReadOfTheSameElement",
			src: `
				fn go() {
					val t: [{x: number}, {x: number}] = [{x: 1}, {x: 2}]
					val [a, _] = t
					val c = t[0]
				}
			`,
			want:         []string{"5:14-5:18: use of moved value 't'"},
			wantDataflow: []string{"5:14-5:18: use of moved value 't[0]'"},
		},
		{
			// The pattern moves t[0], and the read names t[1].
			name: "PatternElementThenIndexReadOfAnotherElement",
			src: `
				fn go() {
					val t: [{x: number}, {x: number}] = [{x: 1}, {x: 2}]
					val [a, _] = t
					val c = t[1]
				}
			`,
			want:         []string{"5:14-5:18: use of moved value 't'"},
			wantDataflow: []string{},
		},
		{
			// The pattern borrows t[0] into a, so a write to t[0].x conflicts with it.
			name: "PatternBorrowThenWriteThroughTheSameElement",
			src: `
				fn go() -> number {
					val mut t: [{x: number}, {x: number}] = [{x: 1}, {x: 2}]
					val [a, b] = &t
					t[0].x = 3
					return a.x
				}
			`,
			want:         []string{},
			wantDataflow: []string{"5:6-5:12: cannot assign to 't[0].x' while it is borrowed as immutable"},
		},
		{
			// The write to t[1] reinitializes one element of the rest, so t[2] stays moved.
			name: "WriteToOneElementOfAMovedRest",
			src: `
				declare fn mk() -> {x: number}
				declare fn sink(p: {x: number}) -> undefined
				fn go() {
					var mut t: [{x: number}, {x: number}, {x: number}] = [mk(), mk(), mk()]
					val [_, ...rest] = t
					t[1] = mk()
					sink(t[2])
				}
			`,
			want:         []string{"7:6-7:7: use of moved value 't'", "8:11-8:15: use of moved value 't'"},
			wantDataflow: []string{"8:11-8:15: use of moved value 't[1...]'"},
		},
		{
			// A write through a number index blames the index, which may name the moved element.
			name: "WriteThroughANumberIndexAfterAnElementMoved",
			src: `
				declare fn mk() -> {x: number}
				declare fn sink(p: {x: number}) -> undefined
				fn go(i: number) {
					val mut xs: Array<{x: number}> = [mk(), mk()]
					sink(xs[0])
					xs[i].x = 3
				}
			`,
			want: []string{"7:6-7:11: use of partially moved value 'xs'; field 'xs[0]' was moved out"},
		},
		{
			// A string-literal key that ends in `...` names a field, not a rest of elements.
			name: "FieldSpelledLikeARest",
			src: `
				declare fn sink(p: {x: number}) -> undefined
				fn go() {
					val o: {"1...": {x: number}, "2": {x: number}} = {"1...": {x: 1}, "2": {x: 2}}
					sink(o["1..."])
					sink(o["2"])
				}
			`,
		},
	}

	for _, tc := range cases {
		for _, dataflow := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/dataflow=%t", tc.name, dataflow), func(t *testing.T) {
				want := tc.want
				// The environment variable turns the pass on even when Options leaves it off.
				on := dataflow || ownershipDataflowFromEnv()
				if on && tc.wantDataflow != nil {
					want = tc.wantDataflow
				}
				res := InferModuleAgainstStdlib(parseModule(t, tc.src), benchTree, Options{OwnershipDataflow: dataflow})
				got := messagesWithSpan(t, res.Errors)
				if len(want) == 0 {
					require.Empty(t, got)
					return
				}
				require.Equal(t, want, got)
			})
		}
	}
}

// TestPathPrefixRelated covers which field paths under one root overlap. An element segment
// such as `1` overlaps a rest segment such as `1...` that starts at or before it.
func TestPathPrefixRelated(t *testing.T) {
	seg := func(name string) placeSeg { return placeSeg{kind: namedSeg, name: name} }
	rest := func(name string) placeSeg { return placeSeg{kind: restSeg, name: name} }
	tests := []struct {
		name string
		a, b []placeSeg
		want bool
	}{
		{name: "SameElement", a: []placeSeg{seg("0")}, b: []placeSeg{seg("0")}, want: true},
		{name: "DifferentElements", a: []placeSeg{seg("0")}, b: []placeSeg{seg("1")}, want: false},
		{name: "ContainerAndElement", a: nil, b: []placeSeg{seg("1")}, want: true},
		{name: "ElementAndFieldUnderIt", a: []placeSeg{seg("0")}, b: []placeSeg{seg("0"), seg("x")}, want: true},
		{name: "RestAndAnElementItTakes", a: []placeSeg{rest("1...")}, b: []placeSeg{seg("2")}, want: true},
		{name: "RestAndTheElementItStartsAt", a: []placeSeg{seg("1")}, b: []placeSeg{rest("1...")}, want: true},
		{name: "RestAndAnElementBeforeIt", a: []placeSeg{rest("1...")}, b: []placeSeg{seg("0")}, want: false},
		{name: "TwoRests", a: []placeSeg{rest("2...")}, b: []placeSeg{rest("1...")}, want: true},
		{name: "RestAndAField", a: []placeSeg{rest("1...")}, b: []placeSeg{seg("x")}, want: false},
		// A string-literal index such as `o["1..."]` names a field, not a rest.
		{name: "FieldSpelledLikeARest", a: []placeSeg{seg("1...")}, b: []placeSeg{seg("2")}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, pathPrefixRelated(tt.a, tt.b))
		})
	}
}
