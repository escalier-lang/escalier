package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// interpolatable returns the type a template literal's interpolation is checked
// against: `string | number | boolean | bigint`.
func interpolatable() soltype.Type {
	return &soltype.UnionType{Types: []soltype.Type{
		prim(soltype.StrPrim),
		prim(soltype.NumPrim),
		prim(soltype.BoolPrim),
		prim(soltype.BigIntPrim),
	}}
}

// inferTemplateLit types a template literal. Each interpolation is constrained
// against interpolatable at its own node, and the literal is a `string`.
func (c *checker) inferTemplateLit(scope *Scope, lvl int, e *ast.TemplateLitExpr) soltype.Type {
	for _, expr := range e.Exprs {
		t := c.inferExpr(scope, lvl, expr)
		// A symbol throws when converted to a string, and an object or a function
		// renders as text no program means to print, so only the primitives that
		// print their value are accepted.
		c.constrain(expr, t, interpolatable())
	}
	t := prim(soltype.StrPrim)
	c.recordType(e, t)
	return t
}

// inferTaggedTemplateLit types a tagged template literal as the call it performs.
// The literal's quasis are its text segments, the parts between interpolations. The
// call passes the tag an array of the quasis followed by each interpolation, and
// this yields the call's type.
func (c *checker) inferTaggedTemplateLit(scope *Scope, lvl int, e *ast.TaggedTemplateLitExpr) soltype.Type {
	strs := make([]ast.Expr, len(e.Quasis))
	for i, q := range e.Quasis {
		strs[i] = ast.NewLitExpr(ast.NewString(q.Value, q.Span))
	}
	quasis := ast.NewArray(strs, e.Span())
	// At runtime the tag receives a TemplateStringsArray, which carries a `raw`
	// property `String.raw` reads. No expression infers that type, so the argument is
	// given it directly.
	var preset map[ast.Expr]soltype.Type
	if t, ok := c.templateStringsArray(); ok {
		preset = map[ast.Expr]soltype.Type{quasis: t}
	}
	args := append([]ast.Expr{quasis}, e.Exprs...)
	t := c.inferCallWithArgTypes(scope, lvl, ast.NewCall(e.Tag, args, false, e.Span()), preset)
	c.recordType(e, t)
	return t
}

// templateStringsArray returns the `TemplateStringsArray` type `std:string`
// declares, and false when the run has not loaded that package.
func (c *checker) templateStringsArray() (soltype.Type, bool) {
	ns, ok := c.ctx.packages.Lookup("std:string")
	if !ok || ns == nil {
		return nil, false
	}
	b, ok := ns.Types["TemplateStringsArray"]
	if !ok {
		return nil, false
	}
	return b.Type, true
}
