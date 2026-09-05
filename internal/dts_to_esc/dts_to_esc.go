// Package dts_to_esc translates TypeScript `.d.ts` declarations into
// Escalier declaration ASTs. The translation runs from one AST to the
// other. Nothing here type-checks, and the only trees it produces are
// `ast` nodes.
//
// Classify decides each class member's receiver mutability from a
// seven-tier ladder. Two of those tiers read the runtime override store
// in internal/interop. This package reaches that store through the
// `OverrideLookup` interface instead of importing it, which is what
// keeps the store's type representation off the converter's import
// graph.
package dts_to_esc

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
// ast.Module plus the dotted runtime path of each declaration it holds.
//
// Each declaration carries its source JSDoc on the node itself, verbatim
// including the `/** ... */` delimiters, per the retention contract in §5.0.
// The printer emits it ahead of the declaration.
type StandaloneModule struct {
	Module *ast.Module
	// Paths maps each emitted declaration to its dotted runtime path — the
	// same string the `@js("...")` decorator carries, kept as a side map so
	// the declarations that erase at codegen have one too. `InterfaceDecl`
	// and `TypeDecl` carry no Decorators field by design (§3.3), yet the
	// ECMA-262 join still has to address the members an interface declares.
	Paths map[ast.Decl]string

	// KeyDrops lists every singleton member flattening skipped because
	// the member's key has no plain-name form.
	// ReportSingletonKeyDrops filters this against
	// AllowedSingletonKeyDrops and names what is left.
	KeyDrops []SingletonMember
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
//   - Lifts `declare global { ... }` blocks, converting what they hold
//     as if it had been written beside the block. Every other
//     declaration is converted too, whatever the file's scope. Only the
//     global tree cares which of them are actually global, and
//     PartitionLibWithOverlay decides that through globalStatements.
//   - Attaches an `@js("...")` decorator to every emitted top-level decl
//     per planning/builtins/implementation_plan.md §3.3.
//   - Forces `export` on every emitted decl.
//   - Emits everything into the root namespace (key ""); no nested
//     ast.Namespace entries are produced.
//   - Preserves the source's leading JSDoc on each top-level decl, kept on
//     the decl itself through ast.Documented. Trio fusion takes the doc from
//     the instance interface and drops the constructor interface's.
//   - Records each emitted decl's dotted runtime path (see
//     StandaloneModule.Paths).
//   - Records every singleton member it skipped because the member's
//     key has no plain-name form (see StandaloneModule.KeyDrops).
func ConvertToStandaloneModule(dtsModule *dts_parser.Module) (*StandaloneModule, error) {
	cctx := &convertCtx{}
	stmts := liftGlobals(dtsModule.Statements)
	trios := detectTrios(stmts)
	singletons := detectSingletons(stmts, trios)
	paths := make(map[ast.Decl]string)

	var decls []ast.Decl
	for _, stmt := range stmts {
		emitted, err := convertStandaloneStmt(cctx, stmt, trios, singletons, "")
		if err != nil {
			return nil, err
		}
		for _, dd := range emitted {
			decls = append(decls, dd.decl)
			dd.decl.SetDoc(dd.doc)
			if dd.path != "" {
				paths[dd.decl] = dd.path
			}
		}
	}

	var namespaces btree.Map[string, *ast.Namespace]
	namespaces.Set("", &ast.Namespace{Decls: decls})
	return &StandaloneModule{
		Module:   ast.NewModule(namespaces),
		Paths:    paths,
		KeyDrops: cctx.keyDrops,
	}, nil
}

// docDecl pairs a converted top-level declaration with the dotted runtime
// path it is reached by and the JSDoc string taken from its dts source
// statement. The JSDoc is empty when the source carried none.
type docDecl struct {
	doc  string
	path string
	decl ast.Decl
}

// RenderStandaloneModule prints a standalone-converter module with a
// blank line between top-level declarations. The plain
// printer.PrintModule separates decls by a single newline, which would
// make the converter's flattened output hard to read for humans. This
// helper is the human-facing form used by tools/dts_to_esc/ and the
// converter's snapshot tests.
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
// pattern. The three names and the var's type annotation are the whole
// rule: `Foo` and `FooConstructor` must both be declared, and the var
// named `Foo` must be a TypeReference to `FooConstructor`. Trios that
// fail any check pass through unchanged.
//
// What the constructor interface declares does not enter recognition.
// Its `new (...)` signatures become the class's constructors when it has
// any, and the class has none when it does not. Two shapes in the
// pinned lib set depend on that:
//
//   - `SymbolConstructor` and `BigIntConstructor` declare no `new` at
//     all, because the specification forbids constructing them.
//     `new Symbol()` throws a TypeError. A class with no constructor
//     is the shape that makes it unrepresentable rather than merely
//     discouraged.
//   - `ArrayConstructor` declares `new (arrayLength?: number): any[]`
//     and two more that return `T[]`. Shorthand is not a
//     TypeReference, so a rule reading the return type has to know
//     that `T[]` spells `Array<T>` before it can match the one name
//     whose mutation story the planning/interop_mutability/ workstream
//     is about. Reading the names spares it that.
//
// The shorthand itself needs no special handling past this point.
// convertTypeAnn turns `T[]` into `Array<T>` and `readonly T[]` into
// `ReadonlyArray<T>`, so every param and member type reaches the class
// already written the long way. Recognition was the only place the two
// spellings had to be told apart.
//
// One shape is held back, and for a reason that is about the class
// form rather than about recognition. A constructor interface with a
// call signature and no `new` describes something callable and not
// constructible, and fuseTrio has no class elem for a call signature,
// so the fused class could be neither called nor constructed.
// `SymbolConstructor` and `BigIntConstructor` are the two, and the
// guard comes out when #1412 gives a class somewhere to hold one.
//
// This matches tryFuseTrio in internal/interop/class_shapes.go, which
// has never gated on a construct signature.
//
// A name that a `declare class` already declares is left alone. That
// is a backstop rather than the right answer. TypeScript merges an
// interface into a same-named class and mergeDecls cannot, so the pair
// stays split whatever this does. Declining only keeps the converter
// from adding a second class beside the one the source spells out.
// #1430 covers the merge.
func detectTrios(stmts []dts_parser.Statement) *trioTable {
	t := &trioTable{
		byName:       make(map[string]*trioInfo),
		consumedCtor: set.NewSet[string](),
		consumedVar:  set.NewSet[string](),
	}

	interfaces := make(map[string]*dts_parser.InterfaceDecl)
	vars := make(map[string]*dts_parser.VarDecl)
	classes := set.NewSet[string]()
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *dts_parser.InterfaceDecl:
			interfaces[s.Name.Name] = s
		case *dts_parser.VarDecl:
			vars[s.Name.Name] = s
		case *dts_parser.ClassDecl:
			classes.Add(s.Name.Name)
		}
	}

	for name, inst := range interfaces {
		// A `declare class Foo` already declares the name as a class.
		// Fusing would emit a second one beside it. See #1430 for why
		// the pair stays split either way.
		if classes.Contains(name) {
			continue
		}
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
		// A constructor interface whose only callable form is a call
		// signature would lose it: fuseTrio has no class elem to put
		// one on, so the fused class could be neither called nor
		// constructed. `SymbolConstructor` and `BigIntConstructor` are
		// the two, and the specification forbids `new` on both, so the
		// call signature is the only way to make one. They stay
		// interfaces until #1412 gives a class somewhere to hold it.
		if hasCallSignature(ctor) && !hasConstructSignature(ctor) {
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

// hasCallSignature reports whether iface declares at least one bare
// `(...)` member, the form that makes `Symbol("x")` a call rather than
// a construction.
func hasCallSignature(iface *dts_parser.InterfaceDecl) bool {
	for _, m := range iface.Members {
		if _, ok := m.(*dts_parser.CallSignature); ok {
			return true
		}
	}
	return false
}

// hasConstructSignature reports whether iface declares at least one
// `new (...)` member.
func hasConstructSignature(iface *dts_parser.InterfaceDecl) bool {
	for _, m := range iface.Members {
		if _, ok := m.(*dts_parser.ConstructSignature); ok {
			return true
		}
	}
	return false
}

// namesWithConstructSignature returns every interface name that some
// top-level declaration gives a `new (...)` member. TypeScript merges
// repeated `interface Foo` declarations, so reading a single statement
// per name would miss a construct signature written on the second one.
//
// A signature inherited through `extends` is not counted. A heritage
// clause can name a type alias, as `IteratorConstructor` does, so
// following one takes name resolution this layer does not do. No
// interface the singleton path can reach carries one today.
func namesWithConstructSignature(stmts []dts_parser.Statement) set.Set[string] {
	names := set.NewSet[string]()
	for _, stmt := range stmts {
		iface, ok := stmt.(*dts_parser.InterfaceDecl)
		if !ok {
			continue
		}
		if hasConstructSignature(iface) {
			names.Add(iface.Name.Name)
		}
	}
	return names
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
	byName      map[string]*singletonInfo
	consumedVar set.Set[string]
}

// detectSingletons scans a module's top-level statements for the
// `interface Foo` + `declare var Foo: Foo` idiom. Recognition requires:
//
//   - A `declare var Foo: Foo` whose type annotation is a bare
//     TypeReference to the same name as the var.
//   - A matching top-level `interface Foo` declaration that is not
//     already consumed by trio detection and that declares no
//     `new (...)` member. Flattening turns each member into its own
//     top-level decl, and a construct signature has no such form, so
//     flattening one would drop it without a trace.
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
	constructible := namesWithConstructSignature(stmts)

	for name, iface := range interfaces {
		if trios.byName[name] != nil || trios.consumedCtor.Contains(name) || trios.consumedVar.Contains(name) {
			continue
		}
		if constructible.Contains(name) {
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
// MethodSignature → FuncDecl; PropertySignature → VarDecl. The
// remaining member kinds have no clean top-level lowering for a
// singleton and are skipped for the MVP: CallSignature,
// IndexSignature, GetterSignature, SetterSignature, ConstructSignature.
//
// A member whose key has no plain-name form is skipped too, because
// flattening needs a name for both the Escalier binding and the
// `@js(...)` path. The corpus shape is
// `interface Math { readonly [Symbol.toStringTag]: string }`. Every
// such skip is recorded on cctx under jsBase, whatever the member
// kind, which is what keeps a new one from passing unnoticed.
func flattenSingleton(cctx *convertCtx, info *singletonInfo, jsBase string) ([]docDecl, error) {
	var out []docDecl
	for _, m := range info.iface.Members {
		switch sig := m.(type) {
		case *dts_parser.GetterSignature:
			// An accessor has no top-level lowering whatever its key,
			// so it is skipped either way. Only the ones under a key
			// with no plain-name form belong in the report. A named
			// accessor is the separate gap listed above.
			if propertyKeyName(sig.Name) == "" {
				cctx.noteSingletonKeyDrop(jsBase, sig.Name)
			}
		case *dts_parser.SetterSignature:
			if propertyKeyName(sig.Name) == "" {
				cctx.noteSingletonKeyDrop(jsBase, sig.Name)
			}
		case *dts_parser.MethodSignature:
			member := propertyKeyName(sig.Name)
			if member == "" {
				cctx.noteSingletonKeyDrop(jsBase, sig.Name)
				continue
			}
			decl, err := singletonMethodToFuncDecl(sig)
			if err != nil {
				return nil, fmt.Errorf("flattening singleton method %s.%s: %w",
					info.iface.Name.Name, member, err)
			}
			path := jsBase + "." + member
			attachJSDecorator(decl, path)
			out = append(out, docDecl{doc: sig.Doc(), path: path, decl: decl})
		case *dts_parser.PropertySignature:
			member := propertyKeyName(sig.Name)
			if member == "" {
				cctx.noteSingletonKeyDrop(jsBase, sig.Name)
				continue
			}
			decl, err := singletonPropertyToVarDecl(sig)
			if err != nil {
				return nil, fmt.Errorf("flattening singleton property %s.%s: %w",
					info.iface.Name.Name, member, err)
			}
			path := jsBase + "." + member
			attachJSDecorator(decl, path)
			out = append(out, docDecl{doc: sig.Doc(), path: path, decl: decl})
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
		ret, err = convertReturnTypeAnn(m.ReturnType)
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
		nil,   // nil throws is equivalent to throws never (PR #384)
		nil,   // body
		true,  // export
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
		nil,  // init
		true, // export
		true, // declare
		span,
	), nil
}

// countTypeRefs returns the total number of TypeReference occurrences
// whose name resolves (via typeRefName) to `name` across every type
// annotation reachable from any statement in stmts. Used by
// detectSingletons to verify the candidate interface is referenced
// only by its companion var.
//
// TODO(#666): replace the hand-rolled walkers below with a dts_parser
// Visitor once that infrastructure lands. This function and its
// walk* helpers exist because dts_parser has no traversal abstraction
// yet — they should collapse to a small Visitor implementation.
func countTypeRefs(stmts []dts_parser.Statement, name string) int {
	count := 0
	visit := func(t dts_parser.TypeAnn) { count += countTypeRefsInTypeAnn(t, name) }
	for _, stmt := range stmts {
		walkStatementTypes(stmt, visit)
	}
	return count
}

// walkTypeParamTypes invokes visit on the constraint and default of
// each type parameter. `interface Foo<T extends Bar = Baz>` mentions
// Bar and Baz nowhere else, so a walk that skips these two slots
// reports both as referenced by nothing.
func walkTypeParamTypes(params []*dts_parser.TypeParam, visit func(dts_parser.TypeAnn)) {
	for _, tp := range params {
		if tp.Constraint != nil {
			visit(tp.Constraint)
		}
		if tp.Default != nil {
			visit(tp.Default)
		}
	}
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
		walkTypeParamTypes(s.TypeParams, visit)
		if s.TypeAnn != nil {
			visit(s.TypeAnn)
		}
	case *dts_parser.FuncDecl:
		walkTypeParamTypes(s.TypeParams, visit)
		for _, p := range s.Params {
			if p.Type != nil {
				visit(p.Type)
			}
		}
		if s.ReturnType != nil {
			visit(s.ReturnType)
		}
	case *dts_parser.InterfaceDecl:
		walkTypeParamTypes(s.TypeParams, visit)
		for _, ext := range s.Extends {
			visit(ext)
		}
		for _, m := range s.Members {
			walkInterfaceMemberTypes(m, visit)
		}
	case *dts_parser.ClassDecl:
		walkTypeParamTypes(s.TypeParams, visit)
		// A superclass and an implemented interface are references the
		// members repeat only by accident. `declare class Foo extends
		// Base implements Iface` names both nowhere else.
		if s.Extends != nil {
			visit(s.Extends)
		}
		for _, impl := range s.Implements {
			visit(impl)
		}
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
		walkTypeParamTypes(sig.TypeParams, visit)
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
		walkTypeParamTypes(sig.TypeParams, visit)
		for _, p := range sig.Params {
			if p.Type != nil {
				visit(p.Type)
			}
		}
		if sig.ReturnType != nil {
			visit(sig.ReturnType)
		}
	case *dts_parser.ConstructSignature:
		walkTypeParamTypes(sig.TypeParams, visit)
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
		walkTypeParamTypes(member.TypeParams, visit)
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

// walkTypeRefs invokes visit on every TypeReference reachable from t,
// including the references inside a reference's own type arguments.
//
// Coverage is best-effort, on the same terms as walkClassMemberTypes
// above. A node shape the switch misses makes a caller see fewer
// references than the source holds, never more.
func walkTypeRefs(t dts_parser.TypeAnn, visit func(*dts_parser.TypeReference)) {
	if t == nil {
		return
	}
	switch n := t.(type) {
	case *dts_parser.TypeReference:
		visit(n)
		for _, arg := range n.TypeArgs {
			walkTypeRefs(arg, visit)
		}
	case *dts_parser.ArrayType:
		walkTypeRefs(n.ElementType, visit)
	case *dts_parser.TupleType:
		for _, e := range n.Elements {
			walkTypeRefs(e.Type, visit)
		}
	case *dts_parser.UnionType:
		for _, sub := range n.Types {
			walkTypeRefs(sub, visit)
		}
	case *dts_parser.IntersectionType:
		for _, sub := range n.Types {
			walkTypeRefs(sub, visit)
		}
	case *dts_parser.FunctionType:
		walkTypeParamTypes(n.TypeParams, func(sub dts_parser.TypeAnn) {
			walkTypeRefs(sub, visit)
		})
		for _, p := range n.Params {
			walkTypeRefs(p.Type, visit)
		}
		walkTypeRefs(n.ReturnType, visit)
	case *dts_parser.ConstructorType:
		walkTypeParamTypes(n.TypeParams, func(sub dts_parser.TypeAnn) {
			walkTypeRefs(sub, visit)
		})
		for _, p := range n.Params {
			walkTypeRefs(p.Type, visit)
		}
		walkTypeRefs(n.ReturnType, visit)
	case *dts_parser.ObjectType:
		for _, m := range n.Members {
			walkInterfaceMemberTypes(m, func(sub dts_parser.TypeAnn) {
				walkTypeRefs(sub, visit)
			})
		}
	case *dts_parser.ParenthesizedType:
		walkTypeRefs(n.Type, visit)
	case *dts_parser.IndexedAccessType:
		walkTypeRefs(n.ObjectType, visit)
		walkTypeRefs(n.IndexType, visit)
	case *dts_parser.ConditionalType:
		walkTypeRefs(n.CheckType, visit)
		walkTypeRefs(n.ExtendsType, visit)
		walkTypeRefs(n.TrueType, visit)
		walkTypeRefs(n.FalseType, visit)
	case *dts_parser.MappedType:
		// The key source and the `as` remapping name types the value
		// slot does not. `{ [K in keyof Bag as Name<K>]: V }` mentions
		// Bag and Name only here.
		if n.TypeParam != nil {
			walkTypeRefs(n.TypeParam.Constraint, visit)
		}
		walkTypeRefs(n.AsClause, visit)
		walkTypeRefs(n.ValueType, visit)
	case *dts_parser.TemplateLiteralType:
		// A template literal's interpolations are the only place some
		// names appear. `type AutoFill = ...
		// ${OptionalPrefixToken<AutoFillSection>}...` in lib.dom.d.ts is
		// every reference AutoFillSection gets.
		for _, part := range n.Parts {
			if sub, ok := part.(*dts_parser.TemplateType); ok {
				walkTypeRefs(sub.Type, visit)
			}
		}
	case *dts_parser.InferType:
		// `infer U extends Bound` names Bound only in the constraint.
		if n.TypeParam != nil {
			walkTypeRefs(n.TypeParam.Constraint, visit)
		}
	case *dts_parser.KeyOfType:
		walkTypeRefs(n.Type, visit)
	case *dts_parser.TypePredicate:
		walkTypeRefs(n.Type, visit)
	case *dts_parser.RestType:
		walkTypeRefs(n.Type, visit)
	case *dts_parser.OptionalType:
		walkTypeRefs(n.Type, visit)
	}
}

// countTypeRefsInTypeAnn returns how many TypeReference nodes under t
// carry the bare name `name`.
func countTypeRefsInTypeAnn(t dts_parser.TypeAnn, name string) int {
	count := 0
	walkTypeRefs(t, func(ref *dts_parser.TypeReference) {
		if typeRefName(ref) == name {
			count++
		}
	})
	return count
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
			path := jsName(nsPath, s.Name.Name)
			attachJSDecorator(classDecl, path)
			// Trio class doc comes from the instance interface; the
			// constructor interface's doc (if any) is dropped — the
			// instance side is the one users see and document.
			return []docDecl{{doc: info.instance.Doc(), path: path, decl: classDecl}}, nil
		}
		if singletons != nil {
			if info, ok := singletons.byName[s.Name.Name]; ok {
				return flattenSingleton(cctx, info, jsName(nsPath, s.Name.Name))
			}
		}
		decl, err := convertInterfaceDecl(s)
		if err != nil {
			return nil, err
		}
		if decl == nil {
			return nil, nil
		}
		path := jsName(nsPath, s.Name.Name)
		attachJSDecorator(decl, path)
		decl.SetExport(true)
		return []docDecl{{doc: s.Doc(), path: path, decl: decl}}, nil

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
		path := jsName(nsPath, s.Name.Name)
		attachJSDecorator(decl, path)
		decl.SetExport(true)
		return []docDecl{{doc: s.Doc(), path: path, decl: decl}}, nil

	case *dts_parser.FuncDecl:
		decl, err := convertFuncDecl(s)
		if err != nil {
			return nil, err
		}
		path := jsName(nsPath, s.Name.Name)
		attachJSDecorator(decl, path)
		decl.SetExport(true)
		return []docDecl{{doc: s.Doc(), path: path, decl: decl}}, nil

	case *dts_parser.TypeDecl:
		decl, err := convertTypeDecl(s)
		if err != nil {
			return nil, err
		}
		if decl == nil {
			return nil, nil
		}
		path := jsName(nsPath, s.Name.Name)
		attachJSDecorator(decl, path)
		decl.SetExport(true)
		return []docDecl{{doc: s.Doc(), path: path, decl: decl}}, nil

	case *dts_parser.ClassDecl:
		decl, err := convertClassDecl(cctx, s)
		if err != nil {
			return nil, err
		}
		path := jsName(nsPath, s.Name.Name)
		attachJSDecorator(decl, path)
		decl.SetExport(true)
		return []docDecl{{doc: s.Doc(), path: path, decl: decl}}, nil

	case *dts_parser.EnumDecl, *dts_parser.ImportDecl,
		*dts_parser.NamedExportStmt, *dts_parser.ExportAllStmt,
		*dts_parser.ExportAsNamespaceStmt, *dts_parser.ExportAssignmentStmt,
		*dts_parser.ModuleDecl:
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

// attachJSDecorator stamps `@js("<arg>")` onto a decoratable decl. Per
// the §3.3 rule, decorators apply only to declarations that lower to a
// JS reference — `VarDecl`, `FuncDecl`, and `ClassDecl`. `TypeDecl` and
// `InterfaceDecl` erase at codegen and are excluded by design: they have
// no Decorators field and these branches are intentional no-ops (see the
// per-case comments below).
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
		// By design (§3.3): @js("...") only applies to decls that lower
		// to a JS reference. A type alias erases at codegen and has no
		// runtime form, so there is nothing for @js to point at — the
		// parser even rejects decorators on `declare type`. Attaching one
		// here would be meaningless, so this is an intentional no-op (and
		// AST.TypeDecl deliberately carries no Decorators field).
	case *ast.InterfaceDecl:
		// Likewise (§3.3): a bare interface that survives trio detection
		// is purely a type and erases at codegen, so @js has no runtime
		// reference to lower. Intentional no-op; InterfaceDecl carries no
		// Decorators field.
	}
}

// fuseTrio synthesises a ClassDecl from a matched trio. Instance members
// come from `info.instance` (always non-static); static members and the
// constructor come from `info.constructor`.
//
// Mapping from interface members to class elems:
//   - MethodSignature   → MethodElem (Static set per side; receiver from
//     ClassifyMethodByName on the instance
//     side, nil on the static side)
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
		elem, err := interfaceMemberToClassElem(m, className, false /*static*/)
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
		elem, err := interfaceMemberToClassElem(m, className, true /*static*/)
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

	// Escalier's `Promise` takes a raise parameter where the TypeScript
	// declaration has no slot for one. The TypeDecl and InterfaceDecl
	// paths add it in decl.go; a trio fuses into a class, so `Promise`
	// needs it here too. Without it the declaration reads `Promise<T>`
	// while every raised use passes two arguments.
	if RaiseParamDecls.Contains(className) {
		typeParams = addRaiseParamToClass(
			typeParams, body, convertSpan(info.instance.Span()))
	}

	return ast.NewClassDecl(
		ast.NewIdentifier(className, convertSpan(info.instance.Name.Span())),
		nil, // lifetime params
		typeParams,
		extends,
		nil, // implements
		body,
		true,  // export
		true,  // declare
		false, // final
		convertSpan(info.instance.Span()),
	), nil
}

// interfaceMemberToClassElem converts an interface member to a class elem,
// keying the static flag off the caller (instance side vs constructor side
// of the trio). Returns (nil, nil) for member kinds with no class-elem
// representation (CallSignature, IndexSignature).
//
// owner is the name of the class being fused, which the receiver
// classification reads for the owner-wide tiers. See ReceiverMutates.
func interfaceMemberToClassElem(
	member dts_parser.InterfaceMember,
	owner string,
	static bool,
) (ast.ClassElem, error) {
	doc := member.Doc()
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
			ret, err = convertReturnTypeAnn(m.ReturnType)
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
			receiver = &ast.MethodReceiver{
				Mut:   ReceiverMutates(owner, propertyKeyName(m.Name)),
				Span_: span,
			}
		}
		elem := &ast.MethodElem{
			Name:     name,
			Fn:       fn,
			Receiver: receiver,
			Static:   static,
			Span_:    span,
		}
		elem.SetDoc(doc)
		return elem, nil

	case *dts_parser.PropertySignature:
		typeAnn, err := convertTypeAnn(m.TypeAnn)
		if err != nil {
			return nil, fmt.Errorf("property %s: %w", propertyKeyName(m.Name), err)
		}
		name, err := convertPropertyKey(m.Name)
		if err != nil {
			return nil, err
		}
		elem := &ast.FieldElem{
			Name:     name,
			Type:     typeAnn,
			Static:   static,
			Readonly: m.Readonly,
			Optional: m.Optional,
			Span_:    convertSpan(m.Span()),
		}
		elem.SetDoc(doc)
		return elem, nil

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
		elem := &ast.GetterElem{
			Name:     name,
			Fn:       fn,
			Receiver: receiver,
			Static:   static,
			Span_:    span,
		}
		elem.SetDoc(doc)
		return elem, nil

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
		elem := &ast.SetterElem{
			Name:     name,
			Fn:       fn,
			Receiver: receiver,
			Static:   static,
			Span_:    span,
		}
		elem.SetDoc(doc)
		return elem, nil

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
	elem := &ast.ConstructorElem{
		Fn:       fn,
		Receiver: &ast.MethodReceiver{Mut: true, Span_: span},
		Span_:    span,
	}
	elem.SetDoc(cs.Doc())
	return elem, nil
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
