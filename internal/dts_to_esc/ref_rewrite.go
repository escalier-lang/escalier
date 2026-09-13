package dts_to_esc

import (
	"fmt"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
)

// rewriteReadonlyTwinRefs walks every TypeAnn slot reachable from the
// module's top-level decls and rewrites references to the twin names
// so the emitted output matches Escalier's mut-modifier model:
//
//   - `TypeRefTypeAnn{Name: ReadonlyFoo, TypeArgs: …}` is renamed in
//     place to `Foo` (the readonly name has no place in the converter
//     output once references are spelled in Escalier's idiom).
//   - `TypeRefTypeAnn{Name: Foo, TypeArgs: …}` (where Foo is a twin's
//     mutable name) is wrapped in `MutableTypeAnn`, printing as
//     `mut Foo<…>`.
//
// Both pure renames at TypeRef sites and wraps at TypeAnn-slot sites
// happen in a single recursive pass. `*ast.TypeRefTypeAnn` slots
// (ClassDecl.Extends, .Implements, InterfaceDecl.Extends) can only
// participate in the rename: there is no place in those slots for a
// `MutableTypeAnn` wrapper, and in the pinned TS lib corpus no class
// or interface in a routed bucket extends a mutable twin directly.
//
// This pass runs after applyReadonlyTwinReceivers (which only touches
// method receivers) and before appendReadonlyAliases (so the
// synthesised `type ReadonlyFoo<…> = Foo<…>` alias's freshly built
// `Foo<…>` RHS is not mistakenly wrapped in `mut`).
func rewriteReadonlyTwinRefs(mod *StandaloneModule, twins []readonlyTwin) {
	if len(twins) == 0 {
		return
	}
	readonlyToMutable := make(map[string]string, len(twins))
	mutableSet := set.NewSet[string]()
	for _, t := range twins {
		readonlyToMutable[t.readonlyName] = t.mutableName
		mutableSet.Add(t.mutableName)
	}
	rw := &refRewriter{readonlyToMutable: readonlyToMutable, mutable: mutableSet}
	mod.Module.Namespaces.Scan(func(_ string, ns *ast.Namespace) bool {
		for _, decl := range ns.Decls {
			rw.rewriteDecl(decl)
		}
		return true
	})
}

// rewriteConsumedCtorRefs respells every reference to a constructor interface trio fusion
// consumed. `interface ArrayConstructor` is folded into `class Array`, so the name no longer
// denotes anything and `static readonly [Symbol.species]: ArrayConstructor` would not resolve.
// `typeof Array` names the same thing the interface did, the class value carrying the
// constructor and the statics.
//
// It walks the same slots rewriteReadonlyTwinRefs walks, through the same rewriter, since both
// rules replace a reference by name.
//
// A `*ast.TypeRefTypeAnn`-typed slot such as ClassDecl.Extends is not respelled, because the
// slot has no room for a `typeof`. Nothing reaches it: six constructor interfaces in
// lib.es5.d.ts do extend one, `RangeErrorConstructor extends ErrorConstructor` among them, but
// each is itself consumed and fuseTrio does not carry a consumed interface's Extends onto the
// class. An instance interface extending a constructor interface would dangle, and no
// declaration in the pinned lib set does.
func rewriteConsumedCtorRefs(mod *StandaloneModule, consumedCtor map[string]string) {
	if len(consumedCtor) == 0 {
		return
	}
	mod.Module.Namespaces.Scan(func(_ string, ns *ast.Namespace) bool {
		rewriteConsumedCtorRefsIn(ns.Decls, consumedCtor)
		return true
	})
}

// rewriteConsumedCtorRefsIn respells the references in one group of declarations against one
// mapping. A `declare namespace` detects its own trios, so its children are rewritten against
// that namespace's mapping alone, before they are flattened into the module. Two namespaces may
// each declare a `FooConstructor`, and the two name different trios, so the mappings are never
// merged.
func rewriteConsumedCtorRefsIn(decls []ast.Decl, consumedCtor map[string]string) {
	if len(consumedCtor) == 0 {
		return
	}
	rw := &refRewriter{consumedCtor: consumedCtor}
	for _, decl := range decls {
		rw.rewriteDecl(decl)
	}
}

type refRewriter struct {
	readonlyToMutable map[string]string
	mutable           set.Set[string]
	// consumedCtor maps a fused constructor interface's name to the instance name it fused
	// into. Empty when the pass is not respelling those references.
	consumedCtor map[string]string
	// qualifiers maps a name another package declares to the binding that
	// package's import makes, so `Event` becomes `core.Event`. Empty when the
	// pass is not qualifying cross-package references.
	qualifiers map[string]string
	// substitutions replaces a reference by name with a whole type annotation,
	// which is how a vacuous type parameter's one occurrence becomes its
	// constraint. Empty when the pass is not substituting.
	substitutions map[string]ast.TypeAnn
	// elideVacuous drops a signature's type parameter that occurs once and only
	// among its parameters, rewriting that occurrence in place.
	elideVacuous bool
	// declaredNames is every name the tree declares. A qualified reference whose
	// head is among them resolves already and is left alone.
	declaredNames set.Set[string]
	// flattenedQualifiers maps the last segment of a qualified reference whose
	// head names nothing to the binding of the package declaring that segment.
	// Namespace flattening produces those: `Intl.LocalesArgument` survives with
	// `LocalesArgument` declared at the top level of `std:intl` and `Intl`
	// declared nowhere, so the head is replaced rather than prefixed.
	flattenedQualifiers map[string]string
}

// rewriteDecl dispatches over every Decl variant. The default panics
// so a newly-added Decl type cannot silently bypass the rewrite — see
// the same canary rationale on `classElemName` in partition_writer.go.
func (r *refRewriter) rewriteDecl(decl ast.Decl) {
	switch d := decl.(type) {
	case *ast.VarDecl:
		if d.TypeAnn != nil {
			d.TypeAnn = r.rewrite(d.TypeAnn)
		}
	case *ast.FuncDecl:
		r.rewriteFuncSig(&d.FuncSig)
	case *ast.TypeDecl:
		if d.TypeAnn != nil {
			d.TypeAnn = r.rewrite(d.TypeAnn)
		}
		r.rewriteTypeParams(d.TypeParams)
	case *ast.InterfaceDecl:
		r.rewriteTypeParams(d.TypeParams)
		for _, ext := range d.Extends {
			r.renameTypeRefInPlace(ext)
		}
		if d.TypeAnn != nil {
			r.rewriteObject(d.TypeAnn)
		}
	case *ast.ClassDecl:
		r.rewriteTypeParams(d.TypeParams)
		if d.Extends != nil {
			r.renameTypeRefInPlace(d.Extends)
		}
		for _, impl := range d.Implements {
			r.renameTypeRefInPlace(impl)
		}
		for _, elem := range d.Body {
			r.rewriteClassElem(elem)
		}
	case *ast.EnumDecl:
		r.rewriteTypeParams(d.TypeParams)
	case *ast.ExportAssignmentStmt:
		// `export = Name` carries only a value-side ident — nothing to rewrite.
	case *ast.NamespaceDecl:
		for _, inner := range d.Decls {
			r.rewriteDecl(inner)
		}
	case *ast.DeclareModuleDecl:
		for _, inner := range d.Decls {
			r.rewriteDecl(inner)
		}
	case *ast.DeclareGlobalDecl:
		for _, inner := range d.Decls {
			r.rewriteDecl(inner)
		}
	default:
		panic(fmt.Sprintf("refRewriter.rewriteDecl: unhandled decl type %T — extend this switch so the readonly-twin rewrite does not silently skip a new Decl variant", decl))
	}
}

func (r *refRewriter) rewriteFuncSig(sig *ast.FuncSig) {
	if r.elideVacuous {
		elideVacuousIn(sigParts{
			typeParams: &sig.TypeParams, params: sig.Params,
			ret: sig.Return, throws: sig.Throws,
		})
	}
	r.rewriteTypeParams(sig.TypeParams)
	for _, p := range sig.Params {
		if p.TypeAnn != nil {
			p.TypeAnn = r.rewrite(p.TypeAnn)
		}
	}
	if sig.Return != nil {
		sig.Return = r.rewrite(sig.Return)
	}
	if sig.Throws != nil {
		sig.Throws = r.rewrite(sig.Throws)
	}
}

func (r *refRewriter) rewriteTypeParams(tps []*ast.TypeParam) {
	for _, tp := range tps {
		if tp.Constraint != nil {
			tp.Constraint = r.rewrite(tp.Constraint)
		}
		if tp.Default != nil {
			tp.Default = r.rewrite(tp.Default)
		}
	}
}

func (r *refRewriter) rewriteClassElem(elem ast.ClassElem) {
	switch e := elem.(type) {
	case *ast.FieldElem:
		if e.Type != nil {
			e.Type = r.rewrite(e.Type)
		}
	case *ast.MethodElem:
		if e.Fn != nil {
			r.rewriteFuncSig(&e.Fn.FuncSig)
		}
	case *ast.GetterElem:
		if e.Fn != nil {
			r.rewriteFuncSig(&e.Fn.FuncSig)
		}
	case *ast.SetterElem:
		if e.Fn != nil {
			r.rewriteFuncSig(&e.Fn.FuncSig)
		}
	case *ast.ConstructorElem:
		if e.Fn != nil {
			r.rewriteFuncSig(&e.Fn.FuncSig)
		}
	case *ast.CallableElem:
		if e.Fn != nil {
			r.rewriteFuncSig(&e.Fn.FuncSig)
		}
	default:
		panic(fmt.Sprintf("refRewriter.rewriteClassElem: unhandled class-elem type %T — extend this switch so the readonly-twin rewrite does not silently skip a new ClassElem variant", elem))
	}
}

func (r *refRewriter) requalifyFlattenedRef(ref *ast.TypeRefTypeAnn) bool {
	member, ok := ref.Name.(*ast.Member)
	if !ok {
		return false
	}
	head, ok := member.Left.(*ast.Ident)
	if !ok {
		return false
	}
	if _, headResolves := r.qualifiers[head.Name]; headResolves {
		return false
	}
	if r.declaredNames.Contains(head.Name) {
		return false
	}
	qualifier, ok := r.flattenedQualifiers[member.Right.Name]
	if !ok {
		return false
	}
	head.Name = qualifier
	return true
}

func (r *refRewriter) qualifyRef(ref *ast.TypeRefTypeAnn, head *ast.Ident) bool {
	qualifier, ok := r.qualifiers[head.Name]
	if !ok {
		return false
	}
	ref.Name = &ast.Member{
		Left:  ast.NewIdentifier(qualifier, head.Span()),
		Right: ast.NewIdentifier(head.Name, head.Span()),
	}
	return true
}

// renameTypeRefInPlace handles the rewrite inside an `extends` or
// `implements` clause, whose AST slot holds a `*TypeRefTypeAnn` and
// nothing else. It renames `ReadonlyFoo` → `Foo` and cannot wrap a
// mutable twin in `MutableTypeAnn`, having no room for one. TypeArgs are
// still walked, so nested refs are rewritten.
//
// A mutable twin name here is left alone, naming the whole definition
// rather than the immutable view of it. A definition holds both `self`
// and `mut self` methods and extending it inherits all of them, so
// `interface RegExpMatchArray extends Array<string>` gets every `Array`
// member including `push`; whether a given instance may call it is
// settled where that instance is bound. Reading the bare name as the
// immutable view would drop the mutating half of the inherited surface.
//
// Eight declarations in the pinned lib set take this shape,
// `RegExpMatchArray`, `FontFaceSet`, and `HighlightRegistry` among
// them.
// requalifyFlattenedRef replaces the head of a qualified reference whose head
// names nothing with the binding of the package that declares its last
// segment, turning `Intl.LocalesArgument` into `intl.LocalesArgument`.
func (r *refRewriter) renameTypeRefInPlace(ref *ast.TypeRefTypeAnn) {
	for i, arg := range ref.TypeArgs {
		ref.TypeArgs[i] = r.rewrite(arg)
	}
	if r.requalifyFlattenedRef(ref) {
		return
	}
	id, ok := ref.Name.(*ast.Ident)
	if !ok {
		return
	}
	if mutableName, ok := r.readonlyToMutable[id.Name]; ok {
		id.Name = mutableName
		return
	}
	r.qualifyRef(ref, id)
}

// rewrite walks a TypeAnn, rewriting twin references in every
// reachable slot and returning the (possibly replaced) node.
func (r *refRewriter) rewrite(t ast.TypeAnn) ast.TypeAnn {
	if t == nil {
		return nil
	}
	switch tt := t.(type) {
	case *ast.TypeOfTypeAnn:
		// `typeof X` reaches a value another package declares, so its name is
		// qualified the way a type reference's is.
		if head, ok := tt.Value.(*ast.Ident); ok {
			if qualifier, ok := r.qualifiers[head.Name]; ok {
				tt.Value = &ast.Member{
					Left:  ast.NewIdentifier(qualifier, head.Span()),
					Right: ast.NewIdentifier(head.Name, head.Span()),
				}
			}
		}
		return tt
	case *ast.TypeRefTypeAnn:
		for i, arg := range tt.TypeArgs {
			tt.TypeArgs[i] = r.rewrite(arg)
		}
		if r.requalifyFlattenedRef(tt) {
			return tt
		}
		id, ok := tt.Name.(*ast.Ident)
		if !ok {
			return tt
		}
		if sub, ok := r.substitutions[id.Name]; ok {
			return sub
		}
		// The constructor interface is gone, fused into the class. `typeof Array` names what
		// `ArrayConstructor` named: the class value, carrying the constructor and the statics.
		//
		// Only the argument-less spelling is respelled. `typeof X` takes no arguments, so
		// rewriting `FooConstructor<number>` would drop the `number` and say something else.
		// detectTrios matches on the name alone, so a generic constructor interface does fuse;
		// none in the pinned lib set is referenced with arguments, and one left alone is a
		// visibly dangling name rather than a silently wrong type.
		if instance, ok := r.consumedCtor[id.Name]; ok && len(tt.TypeArgs) == 0 {
			return ast.NewTypeOfTypeAnn(ast.NewIdentifier(instance, tt.Span()), tt.Span())
		}
		if r.qualifyRef(tt, id) {
			return tt
		}
		if mutableName, ok := r.readonlyToMutable[id.Name]; ok {
			id.Name = mutableName
			return tt
		}
		if r.mutable.Contains(id.Name) {
			return ast.NewMutableTypeAnn(tt, tt.Span())
		}
		return tt
	case *ast.TupleTypeAnn:
		for i, e := range tt.Elems {
			tt.Elems[i] = r.rewrite(e)
		}
		return tt
	case *ast.UnionTypeAnn:
		for i, e := range tt.Types {
			tt.Types[i] = r.rewrite(e)
		}
		return tt
	case *ast.IntersectionTypeAnn:
		for i, e := range tt.Types {
			tt.Types[i] = r.rewrite(e)
		}
		return tt
	case *ast.FuncTypeAnn:
		if r.elideVacuous {
			elideVacuousIn(sigParts{
				typeParams: &tt.TypeParams, params: tt.Params,
				ret: tt.Return, throws: tt.Throws,
			})
		}
		r.rewriteTypeParams(tt.TypeParams)
		for _, p := range tt.Params {
			if p.TypeAnn != nil {
				p.TypeAnn = r.rewrite(p.TypeAnn)
			}
		}
		if tt.Return != nil {
			tt.Return = r.rewrite(tt.Return)
		}
		if tt.Throws != nil {
			tt.Throws = r.rewrite(tt.Throws)
		}
		return tt
	case *ast.KeyOfTypeAnn:
		tt.Type = r.rewrite(tt.Type)
		return tt
	case *ast.NegationTypeAnn:
		tt.Type = r.rewrite(tt.Type)
		return tt
	case *ast.IndexTypeAnn:
		tt.Target = r.rewrite(tt.Target)
		tt.Index = r.rewrite(tt.Index)
		return tt
	case *ast.CondTypeAnn:
		tt.Check = r.rewrite(tt.Check)
		tt.Extends = r.rewrite(tt.Extends)
		tt.Then = r.rewrite(tt.Then)
		tt.Else = r.rewrite(tt.Else)
		return tt
	case *ast.MatchTypeAnn:
		tt.Target = r.rewrite(tt.Target)
		for _, c := range tt.Cases {
			c.Extends = r.rewrite(c.Extends)
			c.Cons = r.rewrite(c.Cons)
		}
		return tt
	case *ast.TemplateLitTypeAnn:
		for i, e := range tt.TypeAnns {
			tt.TypeAnns[i] = r.rewrite(e)
		}
		return tt
	case *ast.ImportTypeAnn:
		for i, e := range tt.TypeArgs {
			tt.TypeArgs[i] = r.rewrite(e)
		}
		return tt
	case *ast.MutableTypeAnn:
		tt.Target = r.rewrite(tt.Target)
		return tt
	case *ast.RestSpreadTypeAnn:
		tt.Value = r.rewrite(tt.Value)
		return tt
	case *ast.ObjectTypeAnn:
		r.rewriteObject(tt)
		return tt
	// Leaf variants: no child TypeAnn slot to walk. `TypeOfTypeAnn` is
	// intentionally a leaf here — its `Value` is a value-side QualIdent
	// (e.g. `typeof Array`), not a type-level reference, so the twin
	// name table does not apply. Every other case below is a primitive
	// or unit-shaped node with no rewritable children.
	case *ast.LitTypeAnn,
		*ast.NumberTypeAnn,
		*ast.StringTypeAnn,
		*ast.BooleanTypeAnn,
		*ast.SymbolTypeAnn,
		*ast.UniqueSymbolTypeAnn,
		*ast.BigintTypeAnn,
		*ast.AnyTypeAnn,
		*ast.UnknownTypeAnn,
		*ast.NeverTypeAnn,
		*ast.InferTypeAnn,
		*ast.WildcardTypeAnn,
		*ast.IntrinsicTypeAnn,
		*ast.ErrorTypeAnn:
		return tt
	default:
		panic(fmt.Sprintf("refRewriter.rewrite: unhandled type-ann %T — extend this switch so the readonly-twin rewrite does not silently skip a new TypeAnn variant", t))
	}
}

func (r *refRewriter) rewriteObject(obj *ast.ObjectTypeAnn) {
	for _, elem := range obj.Elems {
		switch e := elem.(type) {
		case *ast.CallableTypeAnn:
			r.rewriteFnTypeAnn(e.Fn)
		case *ast.ConstructorTypeAnn:
			r.rewriteFnTypeAnn(e.Fn)
		case *ast.MethodTypeAnn:
			r.rewriteFnTypeAnn(e.Fn)
		case *ast.GetterTypeAnn:
			r.rewriteFnTypeAnn(e.Fn)
		case *ast.SetterTypeAnn:
			r.rewriteFnTypeAnn(e.Fn)
		case *ast.PropertyTypeAnn:
			if e.Value != nil {
				e.Value = r.rewrite(e.Value)
			}
		case *ast.MappedTypeAnn:
			if e.TypeParam != nil && e.TypeParam.Constraint != nil {
				e.TypeParam.Constraint = r.rewrite(e.TypeParam.Constraint)
			}
			if e.Name != nil {
				e.Name = r.rewrite(e.Name)
			}
			if e.Value != nil {
				e.Value = r.rewrite(e.Value)
			}
			if e.Check != nil {
				e.Check = r.rewrite(e.Check)
			}
			if e.Extends != nil {
				e.Extends = r.rewrite(e.Extends)
			}
		case *ast.RestSpreadTypeAnn:
			e.Value = r.rewrite(e.Value)
		default:
			panic(fmt.Sprintf("refRewriter.rewriteObject: unhandled object-type-ann elem %T — extend this switch so the readonly-twin rewrite does not silently skip a new ObjTypeAnnElem variant", elem))
		}
	}
}

func (r *refRewriter) rewriteFnTypeAnn(fn *ast.FuncTypeAnn) {
	if fn == nil {
		return
	}
	if r.elideVacuous {
		elideVacuousIn(sigParts{
			typeParams: &fn.TypeParams, params: fn.Params,
			ret: fn.Return, throws: fn.Throws,
		})
	}
	r.rewriteTypeParams(fn.TypeParams)
	for _, p := range fn.Params {
		if p.TypeAnn != nil {
			p.TypeAnn = r.rewrite(p.TypeAnn)
		}
	}
	if fn.Return != nil {
		fn.Return = r.rewrite(fn.Return)
	}
	if fn.Throws != nil {
		fn.Throws = r.rewrite(fn.Throws)
	}
}
