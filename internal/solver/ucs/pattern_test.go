package ucs

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/stretchr/testify/require"
)

// readPattern renders what a pattern reads to: the tag its branch tests, then the binds
// it introduces wrapped around a stand-in leaf. A pattern that tests nothing renders
// `no test`, which is what a catch-all reads to.
//
// The binds nest as they come, so the rendering shows the one level shallowTest read.
// binder.wrap is what turns a nameless bind into a split of its own, and the tests that
// assert flattening go through Normalize.
func readPattern(p ast.Pat, target string) string {
	origin := At(OriginMatchArm, arm(span(45, 60)))
	scrutinee := NewRoot(ident(target), origin)
	test, binds := shallowTest(p, scrutinee, origin)

	tag := "no test"
	if test != nil {
		tag = testString(test)
	}
	var cont Norm = &BodyLeaf{Body: exprBody(num(0)), Origin: origin}
	for i := len(binds) - 1; i >= 0; i-- {
		cont = bindNode(binds[i], cont)
	}
	return tag + " => " + Print(cont, DefaultPrintOptions())
}

// Every pattern kind reads to one tag and a bind per leaf, each on a projection off the
// scrutinee. The cases are the worked examples in planning/ucs/implementation_plan.md,
// minus the flattening normalization wraps around them.
func TestShallowTestPatternKinds(t *testing.T) {
	tests := []struct {
		name    string
		pattern ast.Pat
		target  string
		want    string
	}{
		{
			name:    "wildcard",
			pattern: wildcardPat(),
			target:  "p",
			want:    "no test => leaf 0",
		},
		{
			name:    "identifier binds the scrutinee itself",
			pattern: identPat("all"),
			target:  "p",
			want:    "no test => bind all = p; leaf 0",
		},
		{
			name:    "literal",
			pattern: numPat(1),
			target:  "n",
			want:    "1 => leaf 0",
		},
		{
			name:    "object",
			pattern: objPat("x", "y"),
			target:  "p",
			want:    "{x, y} => bind x = p.x, y = p.y; leaf 0",
		},
		{
			name:    "object renaming a field",
			pattern: ast.NewObjectPat([]ast.ObjPatElem{keyValueElem("x", identPat("a"))}, ast.Span{}),
			target:  "p",
			want:    "{x} => bind a = p.x; leaf 0",
		},
		{
			name:    "object matching a field against a wildcard",
			pattern: ast.NewObjectPat([]ast.ObjPatElem{keyValueElem("x", wildcardPat())}, ast.Span{}),
			target:  "p",
			want:    "{x} => leaf 0",
		},
		{
			name: "object rest",
			pattern: ast.NewObjectPat(
				[]ast.ObjPatElem{shorthandElem("x"), objRestElem("rest")},
				ast.Span{},
			),
			target: "p",
			want:   `{x, ...} => bind x = p.x, rest = p \ {x}; leaf 0`,
		},
		{
			name:    "tuple",
			pattern: ast.NewTuplePat([]ast.Pat{identPat("a"), identPat("b")}, ast.Span{}),
			target:  "xs",
			want:    "[_, _] => bind a = xs.0, b = xs.1; leaf 0",
		},
		{
			name: "tuple rest",
			pattern: ast.NewTuplePat(
				[]ast.Pat{identPat("first"), ast.NewRestPat(identPat("rest"), ast.Span{})},
				ast.Span{},
			),
			target: "xs",
			want:   "[_, ...] => bind first = xs.0, rest = xs[1..]; leaf 0",
		},
		{
			name: "instance",
			pattern: ast.NewInstancePat(
				ast.NewIdentifier("Point", ast.Span{}),
				objPat("x", "y"),
				ast.Span{},
			),
			target: "p",
			want:   "Point => bind x = p.x, y = p.y; leaf 0",
		},
		{
			name: "extractor",
			pattern: ast.NewExtractorPat(
				ast.NewIdentifier("Ok", ast.Span{}),
				[]ast.Pat{identPat("v")},
				ast.Span{},
			),
			target: "r",
			want:   "Ok(_) => bind v = r.#0; leaf 0",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, readPattern(test.pattern, test.target))
		})
	}
}

// A sub-pattern that is not an identifier is kept whole on its projection, which is
// what leaves the nesting for the stage that flattens it. The read goes one level deep:
// the branch tests `Line` and nothing under it.
func TestShallowTestKeepsANestedPatternWhole(t *testing.T) {
	nested := ast.NewInstancePat(
		ast.NewIdentifier("Line", ast.Span{}),
		ast.NewObjectPat([]ast.ObjPatElem{keyValueElem("start", objPat("x", "y"))}, ast.Span{}),
		ast.Span{},
	)
	require.Equal(t,
		"Line => bind {x, y} = l.start; leaf 0",
		readPattern(nested, "l"),
	)

	// A literal sub-pattern is nesting too. The tag names the key and the literal it
	// has still to match rides the bind.
	literalField := ast.NewObjectPat([]ast.ObjPatElem{keyValueElem("x", numPat(1))}, ast.Span{})
	require.Equal(t, "{x} => bind 1 = p.x; leaf 0", readPattern(literalField, "p"))
}

// A field with a default is optional: `{x = 0}` binds even when the field is absent, so
// the test must not demand it. The two spellings of a default, on a shorthand element
// and on a renamed field's value, both mark the key optional.
//
// Optionality is all the rendering says about a default. The value a leaf takes when its
// field is absent stays on the leaf node the bind points at, which is why the assertions
// below read that node rather than the printed form.
func TestShallowTestMarksDefaultedFieldsOptional(t *testing.T) {
	shorthand := ast.NewObjShorthandPat(
		ast.NewIdentifier("x", ast.Span{}), false, nil, num(0), ast.Span{},
	)
	defaulted := ast.NewIdentPat("b", false, nil, num(1), ast.Span{})
	renamed := ast.NewObjKeyValuePat(ast.NewIdentifier("y", ast.Span{}), defaulted, ast.Span{})
	pattern := ast.NewObjectPat([]ast.ObjPatElem{shorthand, renamed}, ast.Span{})

	require.Equal(t,
		"{x?, y?} => bind x = p.x, b = p.y; leaf 0",
		readPattern(pattern, "p"),
	)

	origin := At(OriginMatchArm, arm(span(45, 70)))
	_, binds := shallowTest(pattern, NewRoot(ident("p"), origin), origin)
	require.Len(t, binds, 2)
	require.Same(t, shorthand, binds[0].elem)
	require.Same(t, defaulted, binds[1].pat)
}

// A rest anywhere but last names no suffix a projection can reach, so the test relaxes
// to the elements before it and nothing from the rest on binds. The same holds for an
// object rest that is not last. Both patterns are unsupported downstream, and the pass
// that lowers the surface is what rejects them.
func TestShallowTestMalformedRests(t *testing.T) {
	tuple := ast.NewTuplePat([]ast.Pat{
		identPat("first"),
		ast.NewRestPat(identPat("mid"), ast.Span{}),
		identPat("last"),
	}, ast.Span{})
	require.Equal(t, "[_, ...] => bind first = xs.0; leaf 0", readPattern(tuple, "xs"))

	object := ast.NewObjectPat(
		[]ast.ObjPatElem{objRestElem("rest"), shorthandElem("x")},
		ast.Span{},
	)
	require.Equal(t, "{x} => bind x = p.x; leaf 0", readPattern(object, "p"))
}

// A bare rest is meaningful only inside a tuple or an object. It is kept whole rather
// than dropped, so the solver's pattern walk still sees the names under it and reports
// the pattern itself.
func TestShallowTestKeepsABareRestWhole(t *testing.T) {
	require.Equal(t,
		"no test => bind ...rest = p; leaf 0",
		readPattern(ast.NewRestPat(identPat("rest"), ast.Span{}), "p"),
	)
}

// A shorthand element carries an annotation, a default, and a `mut` marker, none of
// which the bound name holds. The bind points at the element so the solver can read
// them when it binds the leaf.
func TestShallowTestKeepsShorthandElements(t *testing.T) {
	elem := ast.NewObjShorthandPat(
		ast.NewIdentifier("x", ast.Span{}), true, ast.NewNumberTypeAnn(ast.Span{}), nil, span(47, 60),
	)
	pattern := ast.NewObjectPat([]ast.ObjPatElem{elem}, ast.Span{})
	origin := At(OriginMatchArm, arm(span(45, 70)))

	// `{mut x: number}` reads to `{x} => bind x = p.x; leaf 0`. The rendering names the
	// key and the bound leaf and stops there, so the assertions below read the element
	// the bind points at, which is where the annotation and the `mut` marker live.
	_, binds := shallowTest(pattern, NewRoot(ident("p"), origin), origin)
	require.Len(t, binds, 1)
	require.Equal(t, "x", binds[0].name)
	require.Nil(t, binds[0].pat)
	require.Same(t, elem, binds[0].elem)
}

// Every projection hangs off the one scrutinee node the branch tests, so a consumer
// evaluates `f()` once and reads both fields off that one value.
func TestShallowTestSharesTheScrutinee(t *testing.T) {
	origin := At(OriginMatchArm, arm(span(45, 64)))
	scrutinee := NewRoot(ast.NewCall(ident("f"), []ast.Expr{}, false, ast.Span{}), origin)

	// `{x, y}` against `f()` reads to `{x, y} => bind x = f().x, y = f().y; leaf 0`. The
	// rendering spells the call out once per projection, which is what it would look
	// like if each projection re-derived it, so the assertions below read the pointers
	// rather than the path.
	_, binds := shallowTest(objPat("x", "y"), scrutinee, origin)
	require.Len(t, binds, 2)
	require.Same(t, scrutinee, binds[0].source.Parent)
	require.Same(t, scrutinee, binds[1].source.Parent)
	require.Equal(t, "f().x", binds[0].source.String())
	require.Equal(t, "f().y", binds[1].source.String())
}

// A bind and its projection point at the pattern leaf they came from rather than at the
// whole arm, so a message about one field blames the field the user wrote.
func TestShallowTestPointsAtThePatternLeaf(t *testing.T) {
	value := ast.NewIdentPat("a", false, nil, nil, span(52, 53))
	pattern := ast.NewObjectPat([]ast.ObjPatElem{ast.NewObjKeyValuePat(
		ast.NewIdentifier("x", ast.Span{}), value, ast.Span{},
	)}, ast.Span{})
	origin := At(OriginMatchArm, arm(span(45, 64)))

	// `{x: a}` reads to `{x} => bind a = p.x; leaf 0`. A span never renders, so the
	// assertions below read the node each part of the bind blames.
	_, binds := shallowTest(pattern, NewRoot(ident("p"), origin), origin)
	require.Len(t, binds, 1)
	require.Same(t, value, binds[0].pat)
	require.Same(t, value, binds[0].origin.Node)
	require.Same(t, value, binds[0].source.Origin.Node)
	require.Equal(t, OriginMatchArm, binds[0].source.Origin.Kind)
}
