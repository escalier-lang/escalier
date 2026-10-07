package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// memberImpls holds the implementation signature of each method overload set in a class
// declaration that has one, split by side the way the class body and its static view are.
type memberImpls struct {
	instance *soltype.ObjectType
	static   *soltype.ObjectType
}

// implementationArms returns the implementation arm of every method overload set decl
// declares, as ast.ImplementationArm picks it. The sets group by name and by `static`, the
// grouping buildMemberSigs merges arms under.
func implementationArms(decl *ast.ClassDecl) set.Set[*ast.MethodElem] {
	type setKey struct {
		name   string
		static bool
	}
	var order []setKey
	sets := map[setKey][]*ast.MethodElem{}
	for _, elem := range decl.Body {
		m, ok := elem.(*ast.MethodElem)
		if !ok {
			continue
		}
		name, ok := objKeyName(m.Name)
		if !ok {
			continue
		}
		key := setKey{name: name, static: m.Static}
		if _, seen := sets[key]; !seen {
			order = append(order, key)
		}
		sets[key] = append(sets[key], m)
	}
	impls := set.NewSet[*ast.MethodElem]()
	for _, key := range order {
		if impl := ast.ImplementationArm(sets[key]); impl != nil {
			impls.Add(impl)
		}
	}
	return impls
}

// checkImplementations reports each implementation that cannot stand in for one of its
// set's bodiless signatures. A call checked against a signature runs the implementation, so
// the implementation has to accept every argument the signature does and return only what
// it promises. It also reports an implementation taking a different `self` receiver than
// the signatures, which every arm of one set has to share.
//
// body and static are the frozen member views and impls the frozen implementations, so each
// signature is compared at the type a caller reads.
func (c *checker) checkImplementations(def *ClassDef, decl *ast.ClassDecl, body, static *soltype.ObjectType, impls memberImpls) {
	if len(impls.instance.Elems) == 0 && len(impls.static.Elems) == 0 {
		return
	}
	rigid := c.ctx.skolemizeClassParams(def)
	implArms := implementationArms(decl)
	for _, member := range decl.Body {
		elem, ok := member.(*ast.MethodElem)
		if !ok || !implArms.Contains(elem) {
			continue
		}
		name, _ := objKeyName(elem.Name)
		impl := methodNamed(targetBody(impls.instance, impls.static, elem.Static), name)
		sigs := methodNamed(targetBody(body, static, elem.Static), name)
		if impl == nil || sigs == nil {
			continue
		}
		implSig := impl.Signatures[0]
		first := sigs.Signatures[0].SelfParam
		if selfParamMut(implSig.SelfParam) != selfParamMut(first) ||
			selfParamConsumes(implSig.SelfParam) != selfParamConsumes(first) {
			c.report(&MethodOverloadReceiverMismatchError{Name: name, Elem: elem})
			continue
		}
		implType := rigid.apply(callableView(implSig))
		for _, sig := range sigs.Signatures {
			sigType := rigid.apply(callableView(sig))
			// The trial rolls its bounds back, so the comparison records nothing on the
			// signatures the class registered.
			if hasHardError(c.ctx.trialUnderProbe(implType, sigType)) {
				c.report(&IncompatibleImplementationError{
					Member:        name,
					Class:         decl.Name.Name,
					ImplType:      implType,
					SignatureType: sigType,
					Node:          elem,
				})
				break
			}
		}
	}
}

// methodNamed returns the method obj declares under name, or nil when it declares none.
func methodNamed(obj *soltype.ObjectType, name string) *soltype.MethodElem {
	for _, elem := range obj.Elems {
		if m, ok := elem.(*soltype.MethodElem); ok && m.Name == name {
			return m
		}
	}
	return nil
}
