package dts_to_esc

import (
	"github.com/escalier-lang/escalier/internal/ast"
)

// vacuous_type_params.go drops a signature's type parameter that occurs once,
// in an input position, and rewrites that occurrence to what the parameter was
// constrained to.
//
// A type parameter relates two positions, so one occurring once relates
// nothing: `<T>(value?: T) -> boolean` says what `(value?: unknown) -> boolean`
// says, without a reader having to check.
//
// A parameter occurring once in the RETURN is left alone. It is equally
// vacuous, but rewriting it changes what a call yields rather than restating
// it. Sixteen come from type predicates the converter degrades to `-> boolean`,
// and answering those means representing the predicate.

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

// elideVacuousIn drops each type parameter of one signature whose only
// occurrence is a whole parameter's type.
//
// The whole-parameter restriction is what makes the rewrite safe. Replacing a
// whole parameter's type widens what the function accepts, so every call that
// type-checked still does. A NESTED occurrence does not widen:
//
//	fn each<T>(cb: fn (x: T) -> undefined)
//
// is contravariant there, so `fn each(cb: fn (x: unknown) -> undefined)` rejects
// the `fn (x: string) -> undefined` the generic form accepted. An occurrence
// inside a type argument is no safer, since the argument's variance decides.
//
// It also makes shadowing moot, since an inner `fn <T>` rebinding the name
// contributes an occurrence and any extra occurrence stops the elision.
func elideVacuousIn(sig sigParts) {
	if len(*sig.typeParams) == 0 {
		return
	}
	kept := make([]*ast.TypeParam, 0, len(*sig.typeParams))
	for _, tp := range *sig.typeParams {
		// A parameter with a default is a knob the declaration offers a caller, so
		// it stays. `Object.fromEntries<T = any>` occurs once only because the
		// conversion dropped the `{ [k: string]: T }` return that used it, and the
		// fix there is to restore the return.
		if tp.Default != nil {
			kept = append(kept, tp)
			continue
		}

		whole := 0     // parameters whose type is exactly this parameter
		elsewhere := 0 // every other occurrence, wherever it sits
		for _, p := range sig.params {
			if isBareRefTo(p.TypeAnn, tp.Name) {
				whole++
				continue
			}
			elsewhere += countTypeParamRefs(p.TypeAnn, tp.Name)
		}
		elsewhere += countTypeParamRefs(sig.ret, tp.Name)
		elsewhere += countTypeParamRefs(sig.throws, tp.Name)
		elsewhere += countTypeParamRefs(tp.Constraint, tp.Name)
		for _, other := range *sig.typeParams {
			if other != tp {
				elsewhere += countTypeParamRefs(other.Constraint, tp.Name)
				elsewhere += countTypeParamRefs(other.Default, tp.Name)
			}
		}
		if whole != 1 || elsewhere != 0 {
			kept = append(kept, tp)
			continue
		}

		replacement := tp.Constraint
		if replacement == nil {
			replacement = ast.NewUnknownTypeAnn(ast.Span{})
		}
		for _, p := range sig.params {
			if isBareRefTo(p.TypeAnn, tp.Name) {
				p.TypeAnn = replacement
			}
		}
	}
	*sig.typeParams = kept
}

// isBareRefTo reports whether t is exactly a reference to name, carrying no type
// arguments and wrapped in nothing.
func isBareRefTo(t ast.TypeAnn, name string) bool {
	ref, ok := t.(*ast.TypeRefTypeAnn)
	if !ok || len(ref.TypeArgs) > 0 {
		return false
	}
	id, ok := ref.Name.(*ast.Ident)
	return ok && id.Name == name
}

// countTypeParamRefs counts the references to a type parameter by name inside
// one type annotation.
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
