package interop

import (
	"fmt"
	"io"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dts_parser"
	"github.com/escalier-lang/escalier/internal/printer"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/tidwall/btree"
)

// StandaloneModule is the result of ConvertToStandaloneModule: an
// ast.Module plus a side map from each top-level decl to its source
// JSDoc string (verbatim including the `/** ... */` delimiters, per the
// retention contract in §5.0). Decls with no leading JSDoc are absent
// from Docs.
//
// The side map exists because the Escalier AST has no Doc field on
// declarations — adding one would touch the printer and every existing
// snapshot. The standalone renderer reads Docs directly and prints each
// comment ahead of its decl.
type StandaloneModule struct {
	Module *ast.Module
	Docs   map[ast.Decl]string
}

// ConvertToStandaloneModule converts a dts_parser.Module to a form
// shaped for emission as a standalone .esc file by tools/dts_to_esc/.
// Compared to ConvertModule (which preserves namespaces and is consumed by
// the checker prelude) this:
//
//   - Recognises the TS class-via-trio idiom at the dts level:
//     `interface Foo` + `interface FooConstructor` +
//     `declare var Foo: FooConstructor` collapses into one
//     `declare class Foo` (instance members from `Foo`, statics +
//     constructor from `FooConstructor`).
//   - Flattens `declare namespace Foo { ... }` blocks: each member becomes
//     a top-level declaration carrying `@js("Foo.member")`.
//   - Attaches an `@js("...")` decorator to every emitted top-level decl
//     per planning/builtins/implementation_plan.md §3.3.
//   - Forces `export` on every emitted decl.
//   - Emits everything into the root namespace (key ""); no nested
//     ast.Namespace entries are produced.
//   - Preserves the source's leading JSDoc on each top-level decl (see
//     StandaloneModule.Docs); trio fusion takes the doc from the
//     instance interface (the constructor interface's doc is dropped).
func ConvertToStandaloneModule(dtsModule *dts_parser.Module) (*StandaloneModule, error) {
	cctx := &convertCtx{}
	trios := detectTrios(dtsModule.Statements)
	singletons := detectSingletons(dtsModule.Statements, trios)
	docs := make(map[ast.Decl]string)

	var decls []ast.Decl
	for _, stmt := range dtsModule.Statements {
		emitted, err := convertStandaloneStmt(cctx, stmt, trios, singletons, "")
		if err != nil {
			return nil, err
		}
		for _, dd := range emitted {
			decls = append(decls, dd.decl)
			if dd.doc != "" {
				docs[dd.decl] = dd.doc
			}
		}
	}

	var namespaces btree.Map[string, *ast.Namespace]
	namespaces.Set("", &ast.Namespace{Decls: decls})
	return &StandaloneModule{
		Module: ast.NewModule(namespaces),
		Docs:   docs,
	}, nil
}

// docDecl pairs a converted top-level declaration with the JSDoc string
// taken from its dts source statement (empty when there was none).
type docDecl struct {
	doc  string
	decl ast.Decl
}

// RenderStandaloneModule prints a standalone-converter module with a
// blank line between top-level declarations, and the source JSDoc
// comment (when present) ahead of each decl. The plain
// printer.PrintModule separates decls by a single newline and does not
// emit doc comments at all, which would make the converter's flattened
// output hard to read for humans. This helper is the human-facing form
// used by tools/dts_to_esc/ and the converter's snapshot tests.
func RenderStandaloneModule(m *StandaloneModule) (string, error) {
	var sb strings.Builder
	if err := writeStandaloneModule(m, &sb); err != nil {
		return "", err
	}
	return sb.String(), nil
}

// WriteStandaloneModule is the io.Writer counterpart of
// RenderStandaloneModule.
func WriteStandaloneModule(m *StandaloneModule, w io.Writer) error {
	return writeStandaloneModule(m, w)
}

func writeStandaloneModule(m *StandaloneModule, w io.Writer) error {
	opts := printer.DefaultOptions()
	first := true
	var iterErr error
	m.Module.Namespaces.Scan(func(_ string, ns *ast.Namespace) bool {
		for _, decl := range ns.Decls {
			if !first {
				if _, err := io.WriteString(w, "\n\n"); err != nil {
					iterErr = err
					return false
				}
			}
			first = false
			if doc, ok := m.Docs[decl]; ok {
				// dts_parser hands us the comment verbatim with its
				// `/** ... */` delimiters and the source's original
				// column offset on continuation lines. Normalize so
				// continuation `*` lines align at column 1 of the
				// (top-level) destination instead of leaking the
				// source's indent.
				for _, line := range printer.NormalizeDocLines(doc) {
					if _, err := io.WriteString(w, line); err != nil {
						iterErr = err
						return false
					}
					if _, err := io.WriteString(w, "\n"); err != nil {
						iterErr = err
						return false
					}
				}
			}
			s, err := printer.Print(decl, opts)
			if err != nil {
				iterErr = err
				return false
			}
			if _, err := io.WriteString(w, s); err != nil {
				iterErr = err
				return false
			}
		}
		return true
	})
	if iterErr != nil {
		return iterErr
	}
	if !first {
		// Terminal newline so editors/diffs end cleanly.
		if _, err := io.WriteString(w, "\n"); err != nil {
			return err
		}
	}
	return nil
}

// trioInfo records a matched trio. `instance` is emitted as a synthesised
// ClassDecl when the converter walks its top-level position; `consumed`
// keeps track of the constructor interface and the `declare var` binding
// so the main pass skips them.
type trioInfo struct {
	instance    *dts_parser.InterfaceDecl
	constructor *dts_parser.InterfaceDecl
	binding     *dts_parser.VarDecl
}

// trioTable indexes trios by the instance type name. The constructor name
// and var name are recorded in `consumedCtor` / `consumedVar` (keyed by
// the same instance name) so the walk can skip them.
type trioTable struct {
	byName       map[string]*trioInfo
	consumedCtor set.Set[string] // ctor interface names
	consumedVar  set.Set[string] // var binding names
}

// detectTrios scans a module's top-level statements for the
// `interface Foo` + `interface FooConstructor` + `declare var Foo: FooConstructor`
// pattern. Recognition mirrors interop.tryFuseTrio at the dts layer:
// the var's TypeAnn must be a TypeReference to FooConstructor, and the
// constructor interface's `new (...)` signature(s) must return Foo.
// Trios that fail any check pass through unchanged.
func detectTrios(stmts []dts_parser.Statement) *trioTable {
	t := &trioTable{
		byName:       make(map[string]*trioInfo),
		consumedCtor: set.NewSet[string](),
		consumedVar:  set.NewSet[string](),
	}

	interfaces := make(map[string]*dts_parser.InterfaceDecl)
	vars := make(map[string]*dts_parser.VarDecl)
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *dts_parser.InterfaceDecl:
			interfaces[s.Name.Name] = s
		case *dts_parser.VarDecl:
			vars[s.Name.Name] = s
		}
	}

	for name, inst := range interfaces {
		ctorName := name + "Constructor"
		ctor, hasCtor := interfaces[ctorName]
		if !hasCtor {
			continue
		}
		v, hasVar := vars[name]
		if !hasVar {
			continue
		}
		// Var must be typed FooConstructor.
		ref, ok := v.TypeAnn.(*dts_parser.TypeReference)
		if !ok {
			continue
		}
		if typeRefName(ref) != ctorName {
			continue
		}
		// Constructor interface must carry at least one `new (...)` signature
		// whose return type is the instance name. (We allow other members
		// alongside it; bare-call signatures and prototype properties pass
		// through into static members or are skipped.)
		if !hasNewReturning(ctor, name) {
			continue
		}

		t.byName[name] = &trioInfo{
			instance:    inst,
			constructor: ctor,
			binding:     v,
		}
		t.consumedCtor.Add(ctorName)
		t.consumedVar.Add(name)
	}

	return t
}

// singletonInfo records an interface+var-singleton pair recognized at
// the module level: `interface Foo { ... }` + `declare var Foo: Foo`,
// where the interface name is not referenced as a type anywhere else
// in the module. The pair collapses to a flat list of top-level decls,
// each carrying `@js("Foo.<member>")` — the same emission shape as
// `declare namespace` flattening, because the runtime surface is a
// single object whose methods are bound to that object.
type singletonInfo struct {
	iface   *dts_parser.InterfaceDecl
	binding *dts_parser.VarDecl
}

// singletonTable indexes recognized singletons by the interface (and
// matching var) name. `consumedVar` exists for symmetry with
// `trioTable`; in the singleton idiom the var and interface share a
// name, so iterating by `byName` and skipping the var on match works
// equivalently. Kept as a Set for the same skip-on-walk pattern.
type singletonTable struct {
	byName       map[string]*singletonInfo
	consumedVar  set.Set[string]
}

// detectSingletons scans a module's top-level statements for the
// `interface Foo` + `declare var Foo: Foo` idiom. Recognition requires:
//
//   - A `declare var Foo: Foo` whose type annotation is a bare
//     TypeReference to the same name as the var.
//   - A matching top-level `interface Foo` declaration that is not
//     already consumed by trio detection.
//   - No other TypeReference to `Foo` anywhere else in the module
//     (the candidate var's own type contributes the only legal
//     reference). Self-references inside the interface body, references
//     from sibling decls, or a second `declare var X: Foo` all
//     disqualify the pair — those mean `Foo` is a shared shape, not a
//     singleton's structure.
//
// Trios take priority: a name already routed through `trios.byName` or
// `trios.consumedCtor`/`trios.consumedVar` is skipped here.
func detectSingletons(stmts []dts_parser.Statement, trios *trioTable) *singletonTable {
	t := &singletonTable{
		byName:      make(map[string]*singletonInfo),
		consumedVar: set.NewSet[string](),
	}

	interfaces := make(map[string]*dts_parser.InterfaceDecl)
	vars := make(map[string]*dts_parser.VarDecl)
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *dts_parser.InterfaceDecl:
			interfaces[s.Name.Name] = s
		case *dts_parser.VarDecl:
			vars[s.Name.Name] = s
		}
	}

	for name, iface := range interfaces {
		if trios.byName[name] != nil || trios.consumedCtor.Contains(name) || trios.consumedVar.Contains(name) {
			continue
		}
		v, hasVar := vars[name]
		if !hasVar {
			continue
		}
		ref, ok := v.TypeAnn.(*dts_parser.TypeReference)
		if !ok {
			continue
		}
		if typeRefName(ref) != name {
			continue
		}
		// The var's own type annotation legitimately references the
		// interface name once. Any further reference (sibling decl,
		// self-reference inside the interface body) means the interface
		// is participating as a shared type, not just as the singleton's
		// structure.
		if countTypeRefs(stmts, name) > 1 {
			continue
		}

		t.byName[name] = &singletonInfo{iface: iface, binding: v}
		t.consumedVar.Add(name)
	}

	return t
}

// flattenSingleton emits a top-level decl for each member of the
// singleton interface, each decorated with `@js("<jsBase>.<member>")`.
// MethodSignature → FuncDecl; PropertySignature → VarDecl. Other member
// kinds (CallSignature, IndexSignature, GetterSignature, SetterSignature,
// ConstructSignature) have no clean top-level lowering for a singleton
// and are skipped silently for the MVP.
func flattenSingleton(info *singletonInfo, jsBase string) ([]docDecl, error) {
	var out []docDecl
	for _, m := range info.iface.Members {
		switch sig := m.(type) {
		case *dts_parser.MethodSignature:
			decl, err := singletonMethodToFuncDecl(sig)
			if err != nil {
				return nil, fmt.Errorf("flattening singleton method %s.%s: %w",
					info.iface.Name.Name, propertyKeyName(sig.Name), err)
			}
			memberJS := jsBase + "." + propertyKeyName(sig.Name)
			attachJSDecorator(decl, memberJS)
			decl.SetExport(true)
			out = append(out, docDecl{doc: sig.Doc, decl: decl})
		case *dts_parser.PropertySignature:
			decl, err := singletonPropertyToVarDecl(sig)
			if err != nil {
				return nil, fmt.Errorf("flattening singleton property %s.%s: %w",
					info.iface.Name.Name, propertyKeyName(sig.Name), err)
			}
			memberJS := jsBase + "." + propertyKeyName(sig.Name)
			attachJSDecorator(decl, memberJS)
			decl.SetExport(true)
			out = append(out, docDecl{doc: sig.Doc, decl: decl})
		}
	}
	return out, nil
}

// singletonMethodToFuncDecl converts an interface MethodSignature to a
// top-level FuncDecl suitable for emission alongside an `@js(...)`
// decorator. Mirrors convertFuncDecl's output shape but starts from a
// signature (no body) and a bare PropertyKey name.
func singletonMethodToFuncDecl(m *dts_parser.MethodSignature) (*ast.FuncDecl, error) {
	typeParams, err := convertTypeParams(m.TypeParams)
	if err != nil {
		return nil, fmt.Errorf("type params: %w", err)
	}
	params, err := convertParams(m.Params)
	if err != nil {
		return nil, fmt.Errorf("params: %w", err)
	}
	var ret ast.TypeAnn
	if m.ReturnType != nil {
		ret, err = convertTypeAnn(m.ReturnType)
		if err != nil {
			return nil, fmt.Errorf("return: %w", err)
		}
	}
	span := convertSpan(m.Span())
	name := propertyKeyName(m.Name)
	if name == "" {
		return nil, fmt.Errorf("unsupported singleton method key %T", m.Name)
	}
	return ast.NewFuncDecl(
		ast.NewIdentifier(name, span),
		nil, // lifetime params
		typeParams,
		params,
		ret,
		nil, // nil throws is equivalent to throws never (PR #384)
		nil, // body
		false, // export — caller sets
		true,  // declare
		false, // async
		span,
	), nil
}

// singletonPropertyToVarDecl converts an interface PropertySignature to
// a top-level VarDecl. Readonly is preserved; the optional `?` form is
// dropped (top-level singletons in TS are always present).
func singletonPropertyToVarDecl(p *dts_parser.PropertySignature) (*ast.VarDecl, error) {
	typeAnn, err := convertTypeAnn(p.TypeAnn)
	if err != nil {
		return nil, err
	}
	span := convertSpan(p.Span())
	name := propertyKeyName(p.Name)
	if name == "" {
		return nil, fmt.Errorf("unsupported singleton property key %T", p.Name)
	}
	kind := ast.VarKind
	if p.Readonly {
		kind = ast.ValKind
	}
	return ast.NewVarDecl(
		kind,
		ast.NewIdentPat(name, false, nil, nil, span),
		typeAnn,
		nil,   // init
		false, // export — caller sets
		true,  // declare
		span,
	), nil
}

// countTypeRefs returns the total number of TypeReference occurrences
// whose name resolves (via typeRefName) to `name` across every type
// annotation reachable from any statement in stmts. Used by
// detectSingletons to verify the candidate interface is referenced
// only by its companion var.
func countTypeRefs(stmts []dts_parser.Statement, name string) int {
	count := 0
	visit := func(t dts_parser.TypeAnn) { count += countTypeRefsInTypeAnn(t, name) }
	for _, stmt := range stmts {
		walkStatementTypes(stmt, visit)
	}
	return count
}

// walkStatementTypes invokes visit on every top-level TypeAnn carried
// by stmt. For composite statements (InterfaceDecl, ClassDecl,
// NamespaceDecl) it descends into member type annotations too.
func walkStatementTypes(stmt dts_parser.Statement, visit func(dts_parser.TypeAnn)) {
	switch s := stmt.(type) {
	case *dts_parser.VarDecl:
		if s.TypeAnn != nil {
			visit(s.TypeAnn)
		}
	case *dts_parser.TypeDecl:
		if s.TypeAnn != nil {
			visit(s.TypeAnn)
		}
	case *dts_parser.FuncDecl:
		for _, p := range s.Params {
			if p.Type != nil {
				visit(p.Type)
			}
		}
		if s.ReturnType != nil {
			visit(s.ReturnType)
		}
	case *dts_parser.InterfaceDecl:
		for _, ext := range s.Extends {
			visit(ext)
		}
		for _, m := range s.Members {
			walkInterfaceMemberTypes(m, visit)
		}
	case *dts_parser.ClassDecl:
		for _, m := range s.Members {
			walkClassMemberTypes(m, visit)
		}
	case *dts_parser.NamespaceDecl:
		for _, child := range s.Statements {
			walkStatementTypes(child, visit)
		}
	}
}

// walkInterfaceMemberTypes invokes visit on every TypeAnn reachable
// from an interface member.
func walkInterfaceMemberTypes(m dts_parser.InterfaceMember, visit func(dts_parser.TypeAnn)) {
	switch sig := m.(type) {
	case *dts_parser.PropertySignature:
		if sig.TypeAnn != nil {
			visit(sig.TypeAnn)
		}
	case *dts_parser.MethodSignature:
		for _, p := range sig.Params {
			if p.Type != nil {
				visit(p.Type)
			}
		}
		if sig.ReturnType != nil {
			visit(sig.ReturnType)
		}
	case *dts_parser.GetterSignature:
		if sig.ReturnType != nil {
			visit(sig.ReturnType)
		}
	case *dts_parser.SetterSignature:
		if sig.Param != nil && sig.Param.Type != nil {
			visit(sig.Param.Type)
		}
	case *dts_parser.CallSignature:
		for _, p := range sig.Params {
			if p.Type != nil {
				visit(p.Type)
			}
		}
		if sig.ReturnType != nil {
			visit(sig.ReturnType)
		}
	case *dts_parser.ConstructSignature:
		for _, p := range sig.Params {
			if p.Type != nil {
				visit(p.Type)
			}
		}
		if sig.ReturnType != nil {
			visit(sig.ReturnType)
		}
	case *dts_parser.IndexSignature:
		if sig.KeyType != nil {
			visit(sig.KeyType)
		}
		if sig.ValueType != nil {
			visit(sig.ValueType)
		}
	}
}

// walkClassMemberTypes invokes visit on every TypeAnn reachable from a
// class member. Coverage is best-effort — the singleton detector only
// uses this for the "is the interface name referenced elsewhere?"
// check, so missing a rare member shape just biases toward emitting a
// regular interface (the safe direction).
func walkClassMemberTypes(m dts_parser.ClassMember, visit func(dts_parser.TypeAnn)) {
	switch member := m.(type) {
	case *dts_parser.PropertyDecl:
		if member.TypeAnn != nil {
			visit(member.TypeAnn)
		}
	case *dts_parser.MethodDecl:
		for _, p := range member.Params {
			if p.Type != nil {
				visit(p.Type)
			}
		}
		if member.ReturnType != nil {
			visit(member.ReturnType)
		}
	case *dts_parser.ConstructorDecl:
		for _, p := range member.Params {
			if p.Type != nil {
				visit(p.Type)
			}
		}
	}
}

// countTypeRefsInTypeAnn recursively walks a TypeAnn and returns the
// number of TypeReference nodes whose bare name equals `name`.
func countTypeRefsInTypeAnn(t dts_parser.TypeAnn, name string) int {
	if t == nil {
		return 0
	}
	switch n := t.(type) {
	case *dts_parser.TypeReference:
		count := 0
		if typeRefName(n) == name {
			count++
		}
		for _, arg := range n.TypeArgs {
			count += countTypeRefsInTypeAnn(arg, name)
		}
		return count
	case *dts_parser.ArrayType:
		return countTypeRefsInTypeAnn(n.ElementType, name)
	case *dts_parser.TupleType:
		count := 0
		for _, e := range n.Elements {
			count += countTypeRefsInTypeAnn(e.Type, name)
		}
		return count
	case *dts_parser.UnionType:
		count := 0
		for _, sub := range n.Types {
			count += countTypeRefsInTypeAnn(sub, name)
		}
		return count
	case *dts_parser.IntersectionType:
		count := 0
		for _, sub := range n.Types {
			count += countTypeRefsInTypeAnn(sub, name)
		}
		return count
	case *dts_parser.FunctionType:
		count := 0
		for _, p := range n.Params {
			count += countTypeRefsInTypeAnn(p.Type, name)
		}
		count += countTypeRefsInTypeAnn(n.ReturnType, name)
		return count
	case *dts_parser.ConstructorType:
		count := 0
		for _, p := range n.Params {
			count += countTypeRefsInTypeAnn(p.Type, name)
		}
		count += countTypeRefsInTypeAnn(n.ReturnType, name)
		return count
	case *dts_parser.ObjectType:
		count := 0
		for _, m := range n.Members {
			walkInterfaceMemberTypes(m, func(sub dts_parser.TypeAnn) {
				count += countTypeRefsInTypeAnn(sub, name)
			})
		}
		return count
	case *dts_parser.ParenthesizedType:
		return countTypeRefsInTypeAnn(n.Type, name)
	case *dts_parser.IndexedAccessType:
		return countTypeRefsInTypeAnn(n.ObjectType, name) +
			countTypeRefsInTypeAnn(n.IndexType, name)
	case *dts_parser.ConditionalType:
		return countTypeRefsInTypeAnn(n.CheckType, name) +
			countTypeRefsInTypeAnn(n.ExtendsType, name) +
			countTypeRefsInTypeAnn(n.TrueType, name) +
			countTypeRefsInTypeAnn(n.FalseType, name)
	case *dts_parser.MappedType:
		return countTypeRefsInTypeAnn(n.ValueType, name)
	case *dts_parser.KeyOfType:
		return countTypeRefsInTypeAnn(n.Type, name)
	case *dts_parser.TypePredicate:
		return countTypeRefsInTypeAnn(n.Type, name)
	case *dts_parser.RestType:
		return countTypeRefsInTypeAnn(n.Type, name)
	case *dts_parser.OptionalType:
		return countTypeRefsInTypeAnn(n.Type, name)
	}
	return 0
}

// typeRefName returns the bare identifier of a TypeReference's name, or
// "" for qualified refs (e.g. `Foo.Bar`). Trio detection uses this to
// match a binding's type against an interface declared at the same
// scope; qualified names refer to a different declaration and must not
// be matched on the trailing segment alone.
func typeRefName(ref *dts_parser.TypeReference) string {
	if id, ok := ref.Name.(*dts_parser.Ident); ok {
		return id.Name
	}
	return ""
}

// hasNewReturning reports whether ctor has at least one ConstructSignature
// whose return type names instanceName.
func hasNewReturning(ctor *dts_parser.InterfaceDecl, instanceName string) bool {
	for _, m := range ctor.Members {
		cs, ok := m.(*dts_parser.ConstructSignature)
		if !ok {
			continue
		}
		ref, ok := cs.ReturnType.(*dts_parser.TypeReference)
		if !ok {
			continue
		}
		if typeRefName(ref) == instanceName {
			return true
		}
	}
	return false
}

// convertStandaloneStmt converts a single top-level statement, handling
// trio fusion, namespace flattening, and decorator decoration. `nsPath`
// is the qualified namespace path (e.g. "JSON") when called recursively
// for namespace flattening; empty at the module root.
//
// Returns zero or more decls — namespace flattening expands to N decls;
// `consumed` trio sides return zero; everything else returns one.
func convertStandaloneStmt(
	cctx *convertCtx,
	stmt dts_parser.Statement,
	trios *trioTable,
	singletons *singletonTable,
	nsPath string,
) ([]docDecl, error) {
	switch s := stmt.(type) {
	case *dts_parser.NamespaceDecl:
		// `declare namespace Foo { ... }` → flatten its members.
		// Members carry `@js("<qualified>.<member>")` and keep their
		// own JSDoc (the namespace's own JSDoc is dropped — there is
		// no decl to anchor it to once the wrapper is flattened away).
		qual := qualifiedName(nsPath, s.Name.Name)
		var out []docDecl
		innerTrios := detectTrios(s.Statements)
		innerSingletons := detectSingletons(s.Statements, innerTrios)
		for _, child := range s.Statements {
			children, err := convertStandaloneStmt(cctx, child, innerTrios, innerSingletons, qual)
			if err != nil {
				return nil, fmt.Errorf("flattening namespace %s: %w", qual, err)
			}
			out = append(out, children...)
		}
		return out, nil

	case *dts_parser.InterfaceDecl:
		if trios.consumedCtor.Contains(s.Name.Name) {
			return nil, nil
		}
		if info, ok := trios.byName[s.Name.Name]; ok {
			classDecl, err := fuseTrio(info)
			if err != nil {
				return nil, fmt.Errorf("fusing trio for %s: %w", s.Name.Name, err)
			}
			attachJSDecorator(classDecl, jsName(nsPath, s.Name.Name))
			classDecl.SetExport(true)
			// Trio class doc comes from the instance interface; the
			// constructor interface's doc (if any) is dropped — the
			// instance side is the one users see and document.
			return []docDecl{{doc: info.instance.Doc, decl: classDecl}}, nil
		}
		if singletons != nil {
			if info, ok := singletons.byName[s.Name.Name]; ok {
				return flattenSingleton(info, jsName(nsPath, s.Name.Name))
			}
		}
		decl, err := convertInterfaceDecl(s)
		if err != nil {
			return nil, err
		}
		if decl == nil {
			return nil, nil
		}
		attachJSDecorator(decl, jsName(nsPath, s.Name.Name))
		decl.SetExport(true)
		return []docDecl{{doc: s.Doc, decl: decl}}, nil

	case *dts_parser.VarDecl:
		if trios.consumedVar.Contains(s.Name.Name) {
			return nil, nil
		}
		if singletons != nil && singletons.consumedVar.Contains(s.Name.Name) {
			return nil, nil
		}
		decl, err := convertVarDecl(s)
		if err != nil {
			return nil, err
		}
		attachJSDecorator(decl, jsName(nsPath, s.Name.Name))
		decl.SetExport(true)
		return []docDecl{{doc: s.Doc, decl: decl}}, nil

	case *dts_parser.FuncDecl:
		decl, err := convertFuncDecl(s)
		if err != nil {
			return nil, err
		}
		attachJSDecorator(decl, jsName(nsPath, s.Name.Name))
		decl.SetExport(true)
		return []docDecl{{doc: s.Doc, decl: decl}}, nil

	case *dts_parser.TypeDecl:
		decl, err := convertTypeDecl(s)
		if err != nil {
			return nil, err
		}
		if decl == nil {
			return nil, nil
		}
		attachJSDecorator(decl, jsName(nsPath, s.Name.Name))
		decl.SetExport(true)
		return []docDecl{{doc: s.Doc, decl: decl}}, nil

	case *dts_parser.ClassDecl:
		decl, err := convertClassDecl(cctx, s)
		if err != nil {
			return nil, err
		}
		attachJSDecorator(decl, jsName(nsPath, s.Name.Name))
		decl.SetExport(true)
		return []docDecl{{doc: s.Doc, decl: decl}}, nil

	case *dts_parser.EnumDecl, *dts_parser.ImportDecl,
		*dts_parser.NamedExportStmt, *dts_parser.ExportAllStmt,
		*dts_parser.ExportAsNamespaceStmt, *dts_parser.ExportAssignmentStmt,
		*dts_parser.ModuleDecl, *dts_parser.GlobalDecl:
		// Skip MVP-out-of-scope statements silently. §6 will tighten
		// the unmapped-symbol fail-safe; for the MVP we just drop.
		return nil, nil

	default:
		return nil, fmt.Errorf("unsupported top-level statement: %T", stmt)
	}
}

// jsName builds the `@js("...")` argument for a decl named `name` inside
// the dotted namespace path `nsPath`. Root-level decls produce the bare
// name; nested decls produce "<ns>.<name>".
func jsName(nsPath, name string) string {
	if nsPath == "" {
		return name
	}
	return nsPath + "." + name
}

// attachJSDecorator stamps `@js("<arg>")` onto a decoratable decl. Only
// the decl kinds with a Decorators field are handled; other kinds are
// no-ops (matching the §3.3 rule that decorators apply to declarations
// that lower to a JS reference — type aliases and similar are silently
// dropped if they have no field).
func attachJSDecorator(decl ast.Decl, arg string) {
	dec := &ast.Decorator{
		Name: ast.NewIdentifier("js", ast.Span{}),
		Args: []ast.Expr{ast.NewLitExpr(ast.NewString(arg, ast.Span{}))},
	}
	switch d := decl.(type) {
	case *ast.VarDecl:
		d.Decorators = append(d.Decorators, dec)
	case *ast.FuncDecl:
		d.Decorators = append(d.Decorators, dec)
	case *ast.ClassDecl:
		d.Decorators = append(d.Decorators, dec)
	case *ast.TypeDecl:
		// TODO(#664): TypeDecl has no Decorators field; this is a silent
		// no-op. Decide whether type aliases (and InterfaceDecls below)
		// should carry @js("...") or stay unmarked by design, and
		// either widen the AST or codify the exclusion in §3.3.
	case *ast.InterfaceDecl:
		// TODO(#664): see TypeDecl above.
	}
}

// fuseTrio synthesises a ClassDecl from a matched trio. Instance members
// come from `info.instance` (always non-static); static members and the
// constructor come from `info.constructor`.
//
// Mapping from interface members to class elems:
//   - MethodSignature   → MethodElem (Static set per side; receiver from
//                                     ClassifyMethodByName on the instance
//                                     side, nil on the static side)
//   - PropertySignature → FieldElem
//   - GetterSignature   → GetterElem
//   - SetterSignature   → SetterElem
//   - ConstructSignature (static side only) → ConstructorElem
//   - CallSignature (static side: bare-call form like `Boolean(x)`) and
//     IndexSignature are skipped for the MVP — they have no direct class-
//     elem mapping. §6 may revisit (e.g. lower the bare-call form into a
//     static factory).
func fuseTrio(info *trioInfo) (*ast.ClassDecl, error) {
	className := info.instance.Name.Name
	typeParams, err := convertTypeParams(info.instance.TypeParams)
	if err != nil {
		return nil, fmt.Errorf("converting type parameters: %w", err)
	}

	var body []ast.ClassElem

	for _, m := range info.instance.Members {
		elem, err := interfaceMemberToClassElem(m, false /*static*/)
		if err != nil {
			return nil, err
		}
		if elem != nil {
			body = append(body, elem)
		}
	}

	for _, m := range info.constructor.Members {
		if cs, ok := m.(*dts_parser.ConstructSignature); ok {
			ctor, err := constructSignatureToCtorElem(cs)
			if err != nil {
				return nil, err
			}
			body = append(body, ctor)
			continue
		}
		elem, err := interfaceMemberToClassElem(m, true /*static*/)
		if err != nil {
			return nil, err
		}
		if elem != nil {
			body = append(body, elem)
		}
	}

	var extends *ast.TypeRefTypeAnn
	if len(info.instance.Extends) > 0 {
		// For the MVP we take only the first extends — Escalier's
		// ClassDecl carries a single Extends (`*TypeRefTypeAnn`). TS
		// interfaces can extend multiple bases; §6 handles the wider
		// surface (likely by routing extras through `implements`).
		conv, err := convertTypeAnn(info.instance.Extends[0])
		if err != nil {
			return nil, fmt.Errorf("converting extends: %w", err)
		}
		ref, ok := conv.(*ast.TypeRefTypeAnn)
		if !ok {
			return nil, fmt.Errorf("trio %s: extends is not a type ref", className)
		}
		extends = ref
	}

	return ast.NewClassDecl(
		ast.NewIdentifier(className, convertSpan(info.instance.Name.Span())),
		nil, // lifetime params
		typeParams,
		extends,
		nil, // implements
		body,
		false, // export — caller sets
		true,  // declare
		convertSpan(info.instance.Span()),
	), nil
}

// interfaceMemberToClassElem converts an interface member to a class elem,
// keying the static flag off the caller (instance side vs constructor side
// of the trio). Returns (nil, nil) for member kinds with no class-elem
// representation (CallSignature, IndexSignature).
func interfaceMemberToClassElem(
	member dts_parser.InterfaceMember,
	static bool,
) (ast.ClassElem, error) {
	doc := interfaceMemberDoc(member)
	switch m := member.(type) {
	case *dts_parser.MethodSignature:
		typeParams, err := convertTypeParams(m.TypeParams)
		if err != nil {
			return nil, fmt.Errorf("method %s: type params: %w", propertyKeyName(m.Name), err)
		}
		params, err := convertParams(m.Params)
		if err != nil {
			return nil, fmt.Errorf("method %s: params: %w", propertyKeyName(m.Name), err)
		}
		var ret ast.TypeAnn
		if m.ReturnType != nil {
			ret, err = convertTypeAnn(m.ReturnType)
			if err != nil {
				return nil, fmt.Errorf("method %s: return: %w", propertyKeyName(m.Name), err)
			}
		}
		span := convertSpan(m.Span())
		fn := ast.NewFuncExpr(nil, typeParams, params, ret, nil, false, nil, span)
		name, err := convertPropertyKey(m.Name)
		if err != nil {
			return nil, err
		}
		var receiver *ast.MethodReceiver
		if !static {
			mut, ok := ClassifyMethodByName(propertyKeyName(m.Name))
			if !ok {
				// Tier 7 default in Classify is mutating; mirror that
				// so the synthesised receiver matches what classifyMember
				// would have produced for a real MethodDecl that hit no
				// name-based tier.
				mut = true
			}
			receiver = &ast.MethodReceiver{Mut: mut, Span_: span}
		}
		return &ast.MethodElem{
			Name:     name,
			Fn:       fn,
			Receiver: receiver,
			Static:   static,
			Doc:      doc,
			Span_:    span,
		}, nil

	case *dts_parser.PropertySignature:
		typeAnn, err := convertTypeAnn(m.TypeAnn)
		if err != nil {
			return nil, fmt.Errorf("property %s: %w", propertyKeyName(m.Name), err)
		}
		name, err := convertPropertyKey(m.Name)
		if err != nil {
			return nil, err
		}
		return &ast.FieldElem{
			Name:     name,
			Type:     typeAnn,
			Static:   static,
			Readonly: m.Readonly,
			Optional: m.Optional,
			Doc:      doc,
			Span_:    convertSpan(m.Span()),
		}, nil

	case *dts_parser.GetterSignature:
		ret, err := convertTypeAnn(m.ReturnType)
		if err != nil {
			return nil, err
		}
		span := convertSpan(m.Span())
		fn := ast.NewFuncExpr(nil, nil, []*ast.Param{}, ret, nil, false, nil, span)
		name, err := convertPropertyKey(m.Name)
		if err != nil {
			return nil, err
		}
		var receiver *ast.MethodReceiver
		if !static {
			receiver = &ast.MethodReceiver{Mut: false, Span_: span}
		}
		return &ast.GetterElem{
			Name:     name,
			Fn:       fn,
			Receiver: receiver,
			Static:   static,
			Doc:      doc,
			Span_:    span,
		}, nil

	case *dts_parser.SetterSignature:
		param, err := convertParam(m.Param)
		if err != nil {
			return nil, err
		}
		span := convertSpan(m.Span())
		ret := ast.NewLitTypeAnn(ast.NewUndefined(span), span)
		fn := ast.NewFuncExpr(nil, nil, []*ast.Param{param}, ret, nil, false, nil, span)
		name, err := convertPropertyKey(m.Name)
		if err != nil {
			return nil, err
		}
		var receiver *ast.MethodReceiver
		if !static {
			receiver = &ast.MethodReceiver{Mut: true, Span_: span}
		}
		return &ast.SetterElem{
			Name:     name,
			Fn:       fn,
			Receiver: receiver,
			Static:   static,
			Doc:      doc,
			Span_:    span,
		}, nil

	case *dts_parser.CallSignature, *dts_parser.IndexSignature, *dts_parser.ConstructSignature:
		// Skip — no direct class-elem mapping in the MVP. ConstructSignature
		// is handled by the caller for the static side.
		return nil, nil

	default:
		return nil, fmt.Errorf("unsupported interface member in trio fusion: %T", member)
	}
}

// constructSignatureToCtorElem builds a ConstructorElem from the trio's
// `new (...)` signature. The synthesised `mut self` matches the receiver
// shape that convertClassDecl produces for a real ConstructorDecl.
func constructSignatureToCtorElem(cs *dts_parser.ConstructSignature) (*ast.ConstructorElem, error) {
	params, err := convertParams(cs.Params)
	if err != nil {
		return nil, fmt.Errorf("constructor params: %w", err)
	}
	span := convertSpan(cs.Span())
	selfPat := ast.NewIdentPat("self", true, nil, nil, span)
	selfParam := &ast.Param{Pattern: selfPat, TypeAnn: nil, Optional: false}
	allParams := append([]*ast.Param{selfParam}, params...)
	fn := ast.NewFuncExpr(nil, nil, allParams, nil, nil, false, nil, span)
	return &ast.ConstructorElem{
		Fn:       fn,
		Receiver: &ast.MethodReceiver{Mut: true, Span_: span},
		Doc:      cs.Doc,
		Span_:    span,
	}, nil
}

// interfaceMemberDoc returns the leading JSDoc string retained on an
// interface member by the dts_parser (verbatim, with `/** ... */`
// delimiters), or "" if none was present.
func interfaceMemberDoc(member dts_parser.InterfaceMember) string {
	switch m := member.(type) {
	case *dts_parser.CallSignature:
		return m.Doc
	case *dts_parser.ConstructSignature:
		return m.Doc
	case *dts_parser.MethodSignature:
		return m.Doc
	case *dts_parser.PropertySignature:
		return m.Doc
	case *dts_parser.GetterSignature:
		return m.Doc
	case *dts_parser.SetterSignature:
		return m.Doc
	case *dts_parser.IndexSignature:
		return m.Doc
	}
	return ""
}

// propertyKeyName extracts the textual name from a dts PropertyKey,
// best-effort for diagnostics and ClassifyMethodByName lookup. Returns
// "" for keys with no plain-name form (computed keys, etc.).
func propertyKeyName(pk dts_parser.PropertyKey) string {
	switch k := pk.(type) {
	case *dts_parser.Ident:
		return k.Name
	case *dts_parser.StringLiteral:
		return k.Value
	}
	return ""
}
