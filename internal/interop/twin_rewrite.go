package interop

import (
	"fmt"

	"github.com/escalier-lang/escalier/internal/ast"
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
	mutableSet := make(map[string]struct{}, len(twins))
	for _, t := range twins {
		readonlyToMutable[t.readonlyName] = t.mutableName
		mutableSet[t.mutableName] = struct{}{}
	}
	rw := &twinRewriter{readonlyToMutable: readonlyToMutable, mutable: mutableSet}
	mod.Module.Namespaces.Scan(func(_ string, ns *ast.Namespace) bool {
		for _, decl := range ns.Decls {
			rw.rewriteDecl(decl)
		}
		return true
	})
}

type twinRewriter struct {
	readonlyToMutable map[string]string
	mutable           map[string]struct{}
}

// rewriteDecl dispatches over every Decl variant. The default panics
// so a newly-added Decl type cannot silently bypass the rewrite — see
// the same canary rationale on `classElemName` in partition_writer.go.
func (r *twinRewriter) rewriteDecl(decl ast.Decl) {
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
		panic(fmt.Sprintf("twinRewriter.rewriteDecl: unhandled decl type %T — extend this switch so the readonly-twin rewrite does not silently skip a new Decl variant", decl))
	}
}

func (r *twinRewriter) rewriteFuncSig(sig *ast.FuncSig) {
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

func (r *twinRewriter) rewriteTypeParams(tps []*ast.TypeParam) {
	for _, tp := range tps {
		if tp.Constraint != nil {
			tp.Constraint = r.rewrite(tp.Constraint)
		}
		if tp.Default != nil {
			tp.Default = r.rewrite(tp.Default)
		}
	}
}

func (r *twinRewriter) rewriteClassElem(elem ast.ClassElem) {
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
	default:
		panic(fmt.Sprintf("twinRewriter.rewriteClassElem: unhandled class-elem type %T — extend this switch so the readonly-twin rewrite does not silently skip a new ClassElem variant", elem))
	}
}

// renameTypeRefInPlace handles the subset of the rewrite that fits a
// `*TypeRefTypeAnn`-only slot (Extends, Implements). It only renames
// `ReadonlyFoo` → `Foo`; it cannot wrap a mutable twin in
// `MutableTypeAnn` because the slot is typed `*TypeRefTypeAnn`. The
// TypeArgs are still walked recursively so nested refs are rewritten.
//
// If a mutable twin name *does* show up in one of these slots we panic
// rather than emit silently-wrong output: the pinned TS lib corpus
// never has a class extending or implementing `Array<T>` / `Map<K,V>` /
// `Set<T>` directly, so the canary fires only on a real corpus change
// that this rewrite cannot honour in place.
func (r *twinRewriter) renameTypeRefInPlace(ref *ast.TypeRefTypeAnn) {
	for i, arg := range ref.TypeArgs {
		ref.TypeArgs[i] = r.rewrite(arg)
	}
	id, ok := ref.Name.(*ast.Ident)
	if !ok {
		return
	}
	if mutableName, ok := r.readonlyToMutable[id.Name]; ok {
		id.Name = mutableName
		return
	}
	if _, ok := r.mutable[id.Name]; ok {
		panic(fmt.Sprintf("twinRewriter.renameTypeRefInPlace: mutable twin %q appears in an Extends/Implements slot, which cannot carry a `mut` wrapper — the readonly-twin rewrite assumed no class or interface in the pinned TS lib extends a mutable twin directly", id.Name))
	}
}

// rewrite walks a TypeAnn, rewriting twin references in every
// reachable slot and returning the (possibly replaced) node.
func (r *twinRewriter) rewrite(t ast.TypeAnn) ast.TypeAnn {
	if t == nil {
		return nil
	}
	switch tt := t.(type) {
	case *ast.TypeRefTypeAnn:
		for i, arg := range tt.TypeArgs {
			tt.TypeArgs[i] = r.rewrite(arg)
		}
		id, ok := tt.Name.(*ast.Ident)
		if !ok {
			return tt
		}
		if mutableName, ok := r.readonlyToMutable[id.Name]; ok {
			id.Name = mutableName
			return tt
		}
		if _, ok := r.mutable[id.Name]; ok {
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
		*ast.VoidTypeAnn,
		*ast.TypeOfTypeAnn,
		*ast.InferTypeAnn,
		*ast.WildcardTypeAnn,
		*ast.IntrinsicTypeAnn,
		*ast.ErrorTypeAnn:
		return tt
	default:
		panic(fmt.Sprintf("twinRewriter.rewrite: unhandled type-ann %T — extend this switch so the readonly-twin rewrite does not silently skip a new TypeAnn variant", t))
	}
}

func (r *twinRewriter) rewriteObject(obj *ast.ObjectTypeAnn) {
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
			panic(fmt.Sprintf("twinRewriter.rewriteObject: unhandled object-type-ann elem %T — extend this switch so the readonly-twin rewrite does not silently skip a new ObjTypeAnnElem variant", elem))
		}
	}
}

func (r *twinRewriter) rewriteFnTypeAnn(fn *ast.FuncTypeAnn) {
	if fn == nil {
		return
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
