package dts_to_esc

import (
	"github.com/escalier-lang/escalier/internal/ast"
)

// vacuous_type_params.go drops a signature's type parameter that occurs once,
// in an input position, and rewrites that occurrence to what the parameter was
// constrained to.
//
// A type parameter relates two positions. One occurring once relates nothing,
// so `<T>(value?: T) -> boolean` says exactly what `(value?: unknown) -> boolean`
// says, and the second says it without a reader having to check.
//
// A parameter occurring once in the RETURN is left alone. Nothing infers it, so
// it is equally vacuous, but rewriting it changes what a call yields rather than
// restating it. Sixteen of those come from TypeScript type predicates the
// converter degrades to `-> boolean`, and answering them means representing the
// predicate rather than erasing the parameter.

// elideVacuousTypeParams rewrites every signature in the module.
func elideVacuousTypeParams(mod *StandaloneModule) {
	rw := &refRewriter{elideVacuous: true}
	mod.Module.Namespaces.Scan(func(_ string, ns *ast.Namespace) bool {
		for _, decl := range ns.Decls {
			rw.rewriteDecl(decl)
		}
		return true
	})
}

// sigParts is the four slots a signature carries, in either of the two shapes
// the tree writes one in: `ast.FuncSig` for a declaration or class member, and
// `ast.FuncTypeAnn` for a member of an object type.
type sigParts struct {
	typeParams *[]*ast.TypeParam
	params     []*ast.Param
	ret        ast.TypeAnn
	throws     ast.TypeAnn
}

// elideVacuousIn drops each type parameter of one signature that occurs exactly
// once and only among the parameters.
func elideVacuousIn(sig sigParts) {
	if len(*sig.typeParams) == 0 {
		return
	}
	kept := make([]*ast.TypeParam, 0, len(*sig.typeParams))
	for _, tp := range *sig.typeParams {
		// A parameter with a default is a knob the declaration offers a caller,
		// so it stays even when the signature relates it to nothing. That is what
		// `Object.fromEntries<T = any>` is: its `T` occurs once only because the
		// conversion dropped the `{ [k: string]: T }` return, and the fix there is
		// to restore the return rather than to erase the parameter.
		if tp.Default != nil {
			kept = append(kept, tp)
			continue
		}
		inParams := 0
		for _, p := range sig.params {
			inParams += countTypeParamRefs(p.TypeAnn, tp.Name)
		}
		elsewhere := countTypeParamRefs(sig.ret, tp.Name) + countTypeParamRefs(sig.throws, tp.Name)
		// A default names a type of its own and does not count as a use, but a
		// constraint mentioning the parameter would make the rewrite circular.
		elsewhere += countTypeParamRefs(tp.Constraint, tp.Name)
		for _, other := range *sig.typeParams {
			if other != tp {
				elsewhere += countTypeParamRefs(other.Constraint, tp.Name)
				elsewhere += countTypeParamRefs(other.Default, tp.Name)
			}
		}
		if inParams != 1 || elsewhere != 0 {
			kept = append(kept, tp)
			continue
		}
		replacement := tp.Constraint
		if replacement == nil {
			replacement = ast.NewUnknownTypeAnn(ast.Span{})
		}
		sub := &refRewriter{substitutions: map[string]ast.TypeAnn{tp.Name: replacement}}
		for _, p := range sig.params {
			if p.TypeAnn != nil {
				p.TypeAnn = sub.rewrite(p.TypeAnn)
			}
		}
	}
	*sig.typeParams = kept
}

// countTypeRefs counts the references to name inside a type annotation.
func countTypeParamRefs(t ast.TypeAnn, name string) int {
	if t == nil {
		return 0
	}
	c := &typeRefCounter{name: name}
	t.Accept(c)
	return c.count
}

type typeRefCounter struct {
	ast.DefaultVisitor
	name  string
	count int
}

func (c *typeRefCounter) EnterTypeAnn(t ast.TypeAnn) bool {
	if ref, ok := t.(*ast.TypeRefTypeAnn); ok {
		if id, ok := ref.Name.(*ast.Ident); ok && id.Name == c.name {
			c.count++
		}
	}
	return true
}
