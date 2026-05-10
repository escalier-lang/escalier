package interop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dts_parser"
	"github.com/escalier-lang/escalier/internal/parser"
)

// TestStdlibOverrideConsistency verifies that every override entry in
// internal/interop/stdlib/*.esc has a matching declaration in the TypeScript
// lib file the .esc is supposed to mirror. It checks both file location
// (the method must be declared in one of the mapped lib files) and signature
// (param arity, optional flags, types, and return type must match exactly).
//
// Overload policy: TS often has multiple overloads for a single method
// (Promise.then, Array.reduce, RegExp.exec, ...). The override passes if it
// matches at least one TS overload — "any-match" semantics.
//
// Type comparison policy: structural. TS-only named types like ArrayLike,
// IterableIterator, PromiseLike, WeakKey, RegExpExecArray are compared by
// name+arity since Escalier doesn't resolve them. The goal is consistency
// between .esc and .d.ts, not validity in Escalier.
//
// The test is gated on node_modules/typescript being present and skips
// otherwise. Run `npm install` to enable.
func TestStdlibOverrideConsistency(t *testing.T) {
	libDir, ok := tsLibPath()
	if !ok {
		t.Skip("typescript not installed; run `npm install` to enable consistency test")
	}

	// Sort for deterministic sub-test ordering.
	escFiles := make([]string, 0, len(escFileToLibs))
	for f := range escFileToLibs {
		escFiles = append(escFiles, f)
	}
	sort.Strings(escFiles)

	for _, escFile := range escFiles {
		libFiles := escFileToLibs[escFile]
		t.Run(escFile, func(t *testing.T) {
			escEntries, err := parseEscClassMembers(filepath.Join("stdlib", escFile))
			if err != nil {
				t.Fatalf("parse .esc: %v", err)
			}

			tsLookup := make(map[[3]string][]tsSig)
			for _, lib := range libFiles {
				if err := parseTSLibInto(filepath.Join(libDir, lib), tsLookup, lib); err != nil {
					t.Fatalf("parse %s: %v", lib, err)
				}
			}

			for _, e := range escEntries {
				key := [3]string{e.Class, e.Method, e.Kind}
				candidates, ok := tsLookup[key]
				if !ok {
					t.Errorf("%s:%d %s.%s (%s) not declared in any of %v",
						escFile, e.Line, e.Class, e.Method, e.Kind, libFiles)
					continue
				}
				var bestErrs []string
				bestErrCount := -1
				matched := false
				for _, c := range candidates {
					errs := compareEntry(e, c)
					if len(errs) == 0 {
						matched = true
						break
					}
					if bestErrCount < 0 || len(errs) < bestErrCount {
						bestErrs = errs
						bestErrCount = len(errs)
					}
				}
				if !matched {
					for _, msg := range bestErrs {
						t.Errorf("%s:%d %s.%s: %s", escFile, e.Line, e.Class, e.Method, msg)
					}
				}
			}
		})
	}
}

// escFileToLibs maps each stdlib .esc file to the set of TypeScript lib files
// whose declarations it overrides. Overrides for a class can span multiple
// lib files (TS interface merging); methods on the same class can therefore
// span multiple .esc files (Escalier override merging).
var escFileToLibs = map[string][]string{
	"es5.esc":             {"lib.es5.d.ts"},
	"es2015.esc":          {"lib.es5.d.ts", "lib.es2015.collection.d.ts", "lib.es2015.promise.d.ts", "lib.es2018.promise.d.ts"},
	"es2015.iterable.esc": {"lib.es2015.iterable.d.ts"},
	"es2016.esc":          {"lib.es2016.array.include.d.ts"},
	"es2021.esc":          {"lib.es2021.weakref.d.ts"},
	"dom.esc":             {"lib.dom.d.ts"},
	"dom.iterable.esc":    {"lib.dom.iterable.d.ts"},
}

// TestEscParseUndefinedShape is a tiny probe that documents how Escalier
// parses `T | undefined` so the comparator's `compareEscToTSPrim` handles
// the actual AST shape produced by the parser.
func TestEscParseUndefinedShape(t *testing.T) {
	src := `override declare global {
    declare class W<T> {
        deref(self) -> T | undefined,
    }
}
`
	p := parser.NewParser(context.Background(), &ast.Source{Path: "probe.esc", Contents: src})
	script, errs := p.ParseScript()
	if len(errs) > 0 {
		t.Fatalf("parse: %v", errs)
	}
	ds := script.Stmts[0].(*ast.DeclStmt)
	gd := ds.Decl.(*ast.DeclareGlobalDecl)
	cd := gd.Decls[0].(*ast.ClassDecl)
	m := cd.Body[0].(*ast.MethodElem)
	u, ok := m.Fn.Return.(*ast.UnionTypeAnn)
	if !ok {
		t.Fatalf("expected UnionTypeAnn return, got %T", m.Fn.Return)
	}
	for i, ty := range u.Types {
		t.Logf("union elem %d: %T %s", i, ty, typeStrEsc(ty))
	}
}

// tsLibPath walks up from CWD to locate node_modules/typescript/lib/.
func tsLibPath() (string, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return "", false
	}
	for {
		candidate := filepath.Join(dir, "node_modules", "typescript", "lib")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// escEntry represents a single override entry parsed from a .esc file.
// Kind is one of "method", "getter", "setter", "field".
type escEntry struct {
	Class    string
	Method   string
	Kind     string
	Line     int
	Method_  *ast.MethodElem // populated when Kind == "method"
	Getter_  *ast.GetterElem
	Setter_  *ast.SetterElem
	Field_   *ast.FieldElem
}

// tsSig represents a single declaration parsed from a TS lib file.
type tsSig struct {
	Class    string
	Method   string
	Kind     string
	SrcFile  string
	// Method/getter/setter
	Method_ *dts_parser.MethodSignature
	Getter_ *dts_parser.GetterSignature
	Setter_ *dts_parser.SetterSignature
	Field_  *dts_parser.PropertySignature
}

// parseEscClassMembers parses an Escalier .esc override file and returns one
// escEntry per class member declaration found inside any
// `override declare global { ... }` block.
func parseEscClassMembers(path string) ([]escEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	source := &ast.Source{Path: path, Contents: string(data)}
	p := parser.NewParser(context.Background(), source)
	script, errs := p.ParseScript()
	if len(errs) > 0 {
		return nil, fmt.Errorf("parse error: %v", errs[0])
	}

	var out []escEntry
	for _, stmt := range script.Stmts {
		ds, ok := stmt.(*ast.DeclStmt)
		if !ok {
			continue
		}
		gd, ok := ds.Decl.(*ast.DeclareGlobalDecl)
		if !ok {
			continue
		}
		for _, decl := range gd.Decls {
			cd, ok := decl.(*ast.ClassDecl)
			if !ok {
				continue
			}
			className := cd.Name.Name
			for _, elem := range cd.Body {
				switch e := elem.(type) {
				case *ast.MethodElem:
					if name, ok := objKeyName(e.Name); ok {
						out = append(out, escEntry{
							Class: className, Method: name, Kind: "method",
							Line: e.Span().Start.Line, Method_: e,
						})
					}
				case *ast.GetterElem:
					if name, ok := objKeyName(e.Name); ok {
						out = append(out, escEntry{
							Class: className, Method: name, Kind: "getter",
							Line: e.Span().Start.Line, Getter_: e,
						})
					}
				case *ast.SetterElem:
					if name, ok := objKeyName(e.Name); ok {
						out = append(out, escEntry{
							Class: className, Method: name, Kind: "setter",
							Line: e.Span().Start.Line, Setter_: e,
						})
					}
				case *ast.FieldElem:
					if name, ok := objKeyName(e.Name); ok {
						out = append(out, escEntry{
							Class: className, Method: name, Kind: "field",
							Line: e.Span().Start.Line, Field_: e,
						})
					}
				}
			}
		}
	}
	return out, nil
}

// objKeyName extracts the simple string name of an ast.ObjKey. Returns "",
// false for computed keys we can't handle here (Symbol.* etc.).
func objKeyName(k ast.ObjKey) (string, bool) {
	switch v := k.(type) {
	case *ast.IdentExpr:
		return v.Name, true
	case *ast.StrLit:
		return v.Value, true
	}
	return "", false
}

// parseTSLibInto parses a .d.ts file and accumulates method/getter/setter/property
// signatures from every interface declaration into the lookup map keyed by
// (class, member, kind). The same key may have multiple values when TS has
// overloads, augmentation, etc.
func parseTSLibInto(path string, lookup map[[3]string][]tsSig, srcFile string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	source := &ast.Source{Path: path, Contents: string(data)}
	p := dts_parser.NewDtsParser(source)
	module, errs := p.ParseModule()
	if len(errs) > 0 {
		return fmt.Errorf("parse error: %v", errs[0])
	}

	for _, stmt := range module.Statements {
		visitTSStatement(stmt, lookup, srcFile)
	}
	return nil
}

// visitTSStatement walks any interface declarations (top-level or nested in
// declare global / declare module / namespace) and collects their members.
func visitTSStatement(stmt dts_parser.Statement, lookup map[[3]string][]tsSig, srcFile string) {
	switch s := stmt.(type) {
	case *dts_parser.InterfaceDecl:
		collectInterfaceMembers(s, lookup, srcFile)
	case *dts_parser.GlobalDecl:
		for _, inner := range s.Statements {
			visitTSStatement(inner, lookup, srcFile)
		}
	case *dts_parser.ModuleDecl:
		for _, inner := range s.Statements {
			visitTSStatement(inner, lookup, srcFile)
		}
	case *dts_parser.NamespaceDecl:
		for _, inner := range s.Statements {
			visitTSStatement(inner, lookup, srcFile)
		}
	}
}

func collectInterfaceMembers(iface *dts_parser.InterfaceDecl, lookup map[[3]string][]tsSig, srcFile string) {
	className := iface.Name.Name
	for _, m := range iface.Members {
		switch v := m.(type) {
		case *dts_parser.MethodSignature:
			if name, ok := propertyKeyName(v.Name); ok {
				key := [3]string{className, name, "method"}
				lookup[key] = append(lookup[key], tsSig{
					Class: className, Method: name, Kind: "method",
					SrcFile: srcFile, Method_: v,
				})
			}
		case *dts_parser.GetterSignature:
			if name, ok := propertyKeyName(v.Name); ok {
				key := [3]string{className, name, "getter"}
				lookup[key] = append(lookup[key], tsSig{
					Class: className, Method: name, Kind: "getter",
					SrcFile: srcFile, Getter_: v,
				})
			}
		case *dts_parser.SetterSignature:
			if name, ok := propertyKeyName(v.Name); ok {
				key := [3]string{className, name, "setter"}
				lookup[key] = append(lookup[key], tsSig{
					Class: className, Method: name, Kind: "setter",
					SrcFile: srcFile, Setter_: v,
				})
			}
		case *dts_parser.PropertySignature:
			if name, ok := propertyKeyName(v.Name); ok {
				key := [3]string{className, name, "field"}
				lookup[key] = append(lookup[key], tsSig{
					Class: className, Method: name, Kind: "field",
					SrcFile: srcFile, Field_: v,
				})
			}
		}
	}
}

func propertyKeyName(k dts_parser.PropertyKey) (string, bool) {
	switch v := k.(type) {
	case *dts_parser.Ident:
		return v.Name, true
	case *dts_parser.StringLiteral:
		return v.Value, true
	}
	return "", false
}

// compareEntry returns a list of mismatch messages between the .esc entry
// and the TS signature. Empty list means a clean match.
func compareEntry(e escEntry, ts tsSig) []string {
	switch e.Kind {
	case "method":
		if ts.Method_ == nil {
			return []string{"kind mismatch: esc=method ts=" + ts.Kind}
		}
		return compareMethod(e.Method_, ts.Method_)
	case "getter":
		if ts.Getter_ == nil {
			return []string{"kind mismatch: esc=getter ts=" + ts.Kind}
		}
		return compareGetter(e.Getter_, ts.Getter_)
	case "setter":
		if ts.Setter_ == nil {
			return []string{"kind mismatch: esc=setter ts=" + ts.Kind}
		}
		return compareSetter(e.Setter_, ts.Setter_)
	case "field":
		if ts.Field_ == nil {
			return []string{"kind mismatch: esc=field ts=" + ts.Kind}
		}
		return compareField(e.Field_, ts.Field_)
	}
	return []string{"unknown kind: " + e.Kind}
}

func compareMethod(esc *ast.MethodElem, ts *dts_parser.MethodSignature) []string {
	escParams := esc.Fn.Params
	tsParams := ts.Params
	var errs []string
	errs = append(errs, compareParamLists(escParams, tsParams)...)
	errs = append(errs, compareReturn(esc.Fn.Return, ts.ReturnType)...)
	return errs
}

func compareGetter(esc *ast.GetterElem, ts *dts_parser.GetterSignature) []string {
	// Getters take no params; only return type matters.
	return compareReturn(esc.Fn.Return, ts.ReturnType)
}

func compareSetter(esc *ast.SetterElem, ts *dts_parser.SetterSignature) []string {
	// Setters take exactly one param; no return type to compare.
	if len(esc.Fn.Params) != 1 {
		return []string{fmt.Sprintf("setter must have exactly 1 param, esc has %d", len(esc.Fn.Params))}
	}
	tsParam := ts.Param
	return compareParam(esc.Fn.Params[0], tsParam, 0)
}

func compareField(esc *ast.FieldElem, ts *dts_parser.PropertySignature) []string {
	var errs []string
	if esc.Readonly != ts.Readonly {
		errs = append(errs, fmt.Sprintf("readonly mismatch: esc=%v ts=%v", esc.Readonly, ts.Readonly))
	}
	if err := compareTypes(esc.Type, ts.TypeAnn); err != nil {
		errs = append(errs, "type: "+err.Error())
	}
	return errs
}

func compareParamLists(escParams []*ast.Param, tsParams []*dts_parser.Param) []string {
	var errs []string
	if len(escParams) != len(tsParams) {
		return []string{fmt.Sprintf("param count: esc has %d, ts has %d", len(escParams), len(tsParams))}
	}
	for i := range escParams {
		errs = append(errs, compareParam(escParams[i], tsParams[i], i)...)
	}
	return errs
}

func compareParam(esc *ast.Param, ts *dts_parser.Param, idx int) []string {
	var errs []string
	if esc.Optional != ts.Optional {
		errs = append(errs, fmt.Sprintf("param %d optional: esc=%v ts=%v", idx, esc.Optional, ts.Optional))
	}
	if err := compareTypes(esc.TypeAnn, ts.Type); err != nil {
		errs = append(errs, fmt.Sprintf("param %d type: %s", idx, err))
	}
	return errs
}

func compareReturn(esc ast.TypeAnn, ts dts_parser.TypeAnn) []string {
	if err := compareTypes(esc, ts); err != nil {
		return []string{"return type: " + err.Error()}
	}
	return nil
}

// compareTypes does structural equality between Escalier and dts_parser type
// annotations. Returns nil on match, error describing the mismatch otherwise.
//
// Permissive cases (intentional):
//   - Generic type parameters compared by arity only (alpha-equivalence)
//   - TS-only named types like ArrayLike compared by name+arity
//   - LiteralType{null/undefined} on the TS side matches the corresponding
//     PrimitiveType variants
func compareTypes(esc ast.TypeAnn, ts dts_parser.TypeAnn) error {
	if esc == nil && ts == nil {
		return nil
	}
	if esc == nil || ts == nil {
		return fmt.Errorf("nil mismatch: esc=%v ts=%v", typeStrEsc(esc), typeStrTS(ts))
	}

	// Strip TS ParenthesizedType — semantic noise.
	if pt, ok := ts.(*dts_parser.ParenthesizedType); ok {
		return compareTypes(esc, pt.Type)
	}

	// Primitive matching.
	if prim, ok := ts.(*dts_parser.PrimitiveType); ok {
		return compareEscToTSPrim(esc, prim)
	}

	// LiteralType (e.g. `null`, `undefined`, string/number literals).
	if lit, ok := ts.(*dts_parser.LiteralType); ok {
		return compareEscToTSLit(esc, lit)
	}

	switch tsT := ts.(type) {
	case *dts_parser.TypeReference:
		// esc may be either a TypeRefTypeAnn or a primitive when the TS ref
		// resolves to a global like Array<T> ↔ T[]. We require an esc TypeRef
		// with matching name + arity.
		ref, ok := esc.(*ast.TypeRefTypeAnn)
		if !ok {
			return fmt.Errorf("expected type ref %s, got %s", typeStrTS(ts), typeStrEsc(esc))
		}
		if ast.QualIdentToString(ref.Name) != qualIdentToStringDts(tsT.Name) {
			return fmt.Errorf("ref name: esc=%s ts=%s", ast.QualIdentToString(ref.Name), qualIdentToStringDts(tsT.Name))
		}
		if len(ref.TypeArgs) != len(tsT.TypeArgs) {
			return fmt.Errorf("ref %s arity: esc=%d ts=%d", ast.QualIdentToString(ref.Name), len(ref.TypeArgs), len(tsT.TypeArgs))
		}
		for i := range ref.TypeArgs {
			if err := compareTypes(ref.TypeArgs[i], tsT.TypeArgs[i]); err != nil {
				return fmt.Errorf("ref %s arg %d: %s", ast.QualIdentToString(ref.Name), i, err)
			}
		}
		return nil

	case *dts_parser.UnionType:
		escU, ok := esc.(*ast.UnionTypeAnn)
		if !ok {
			return fmt.Errorf("expected union, got %s", typeStrEsc(esc))
		}
		return compareUnordered(escU.Types, tsT.Types, "union")

	case *dts_parser.IntersectionType:
		escI, ok := esc.(*ast.IntersectionTypeAnn)
		if !ok {
			return fmt.Errorf("expected intersection, got %s", typeStrEsc(esc))
		}
		return compareUnordered(escI.Types, tsT.Types, "intersection")

	case *dts_parser.FunctionType:
		escF, ok := esc.(*ast.FuncTypeAnn)
		if !ok {
			return fmt.Errorf("expected function type, got %s", typeStrEsc(esc))
		}
		if len(escF.TypeParams) != len(tsT.TypeParams) {
			return fmt.Errorf("fn type-param arity: esc=%d ts=%d", len(escF.TypeParams), len(tsT.TypeParams))
		}
		if len(escF.Params) != len(tsT.Params) {
			return fmt.Errorf("fn param count: esc=%d ts=%d", len(escF.Params), len(tsT.Params))
		}
		for i := range escF.Params {
			if escF.Params[i].Optional != tsT.Params[i].Optional {
				return fmt.Errorf("fn param %d optional: esc=%v ts=%v", i, escF.Params[i].Optional, tsT.Params[i].Optional)
			}
			if err := compareTypes(escF.Params[i].TypeAnn, tsT.Params[i].Type); err != nil {
				return fmt.Errorf("fn param %d: %s", i, err)
			}
		}
		if err := compareTypes(escF.Return, tsT.ReturnType); err != nil {
			return fmt.Errorf("fn return: %s", err)
		}
		return nil

	case *dts_parser.ArrayType:
		// TS T[] — esc usually a TypeRefTypeAnn{Name:"Array", TypeArgs:[T]}.
		ref, ok := esc.(*ast.TypeRefTypeAnn)
		if !ok || ast.QualIdentToString(ref.Name) != "Array" || len(ref.TypeArgs) != 1 {
			return fmt.Errorf("expected Array<T>, got %s", typeStrEsc(esc))
		}
		return compareTypes(ref.TypeArgs[0], tsT.ElementType)

	case *dts_parser.TupleType:
		escT, ok := esc.(*ast.TupleTypeAnn)
		if !ok {
			return fmt.Errorf("expected tuple, got %s", typeStrEsc(esc))
		}
		if len(escT.Elems) != len(tsT.Elements) {
			return fmt.Errorf("tuple arity: esc=%d ts=%d", len(escT.Elems), len(tsT.Elements))
		}
		for i := range escT.Elems {
			if err := compareTypes(escT.Elems[i], tsT.Elements[i].Type); err != nil {
				return fmt.Errorf("tuple elem %d: %s", i, err)
			}
		}
		return nil

	case *dts_parser.ThisType:
		// TS `this` return — accept if esc references the enclosing class name
		// (caller-decided). We can't enforce that here without context; accept
		// any type-ref or named primitive.
		if _, ok := esc.(*ast.TypeRefTypeAnn); ok {
			return nil
		}
		return fmt.Errorf("expected this-equivalent type ref, got %s", typeStrEsc(esc))
	}

	return fmt.Errorf("unsupported ts type %s vs esc %s", typeStrTS(ts), typeStrEsc(esc))
}

// compareEscToTSPrim handles all dts_parser.PrimitiveKind values.
func compareEscToTSPrim(esc ast.TypeAnn, prim *dts_parser.PrimitiveType) error {
	expect := primitiveName(prim.Kind)
	switch esc.(type) {
	case *ast.NumberTypeAnn:
		if expect == "number" {
			return nil
		}
	case *ast.StringTypeAnn:
		if expect == "string" {
			return nil
		}
	case *ast.BooleanTypeAnn:
		if expect == "boolean" {
			return nil
		}
	case *ast.VoidTypeAnn:
		if expect == "void" {
			return nil
		}
	case *ast.AnyTypeAnn:
		if expect == "any" {
			return nil
		}
	case *ast.UnknownTypeAnn:
		if expect == "unknown" {
			return nil
		}
	case *ast.NeverTypeAnn:
		if expect == "never" {
			return nil
		}
	case *ast.BigintTypeAnn:
		if expect == "bigint" {
			return nil
		}
	case *ast.SymbolTypeAnn:
		if expect == "symbol" {
			return nil
		}
	}
	// Escalier parses `null`/`undefined` as LitTypeAnn (NullLit/UndefinedLit).
	if lit, ok := esc.(*ast.LitTypeAnn); ok {
		switch lit.Lit.(type) {
		case *ast.NullLit:
			if expect == "null" {
				return nil
			}
		case *ast.UndefinedLit:
			if expect == "undefined" {
				return nil
			}
		}
	}
	// `object` (and other rare names) may surface as TypeRef.
	if ref, ok := esc.(*ast.TypeRefTypeAnn); ok && len(ref.TypeArgs) == 0 {
		if ast.QualIdentToString(ref.Name) == expect {
			return nil
		}
	}
	return fmt.Errorf("primitive: esc=%s ts=%s", typeStrEsc(esc), expect)
}

// compareEscToTSLit handles TS literal types (`null`, `undefined`, "literal",
// 42, true). Escalier's LitTypeAnn carries a Lit value; null/undefined come
// through as specific Lit kinds.
func compareEscToTSLit(esc ast.TypeAnn, lit *dts_parser.LiteralType) error {
	tsStr := tsLiteralStr(lit.Literal)
	if escLit, ok := esc.(*ast.LitTypeAnn); ok {
		if escLitStr(escLit) == tsStr {
			return nil
		}
		return fmt.Errorf("literal: esc=%s ts=%s", escLitStr(escLit), tsStr)
	}
	// `null` / `undefined` may be primitive on the esc side.
	switch tsStr {
	case "null":
		if _, ok := esc.(*ast.TypeRefTypeAnn); ok {
			if ast.QualIdentToString(esc.(*ast.TypeRefTypeAnn).Name) == "null" {
				return nil
			}
		}
	case "undefined":
		if _, ok := esc.(*ast.TypeRefTypeAnn); ok {
			if ast.QualIdentToString(esc.(*ast.TypeRefTypeAnn).Name) == "undefined" {
				return nil
			}
		}
	}
	return fmt.Errorf("expected literal %s, got %s", tsStr, typeStrEsc(esc))
}

func compareUnordered(escTypes []ast.TypeAnn, tsTypes []dts_parser.TypeAnn, kind string) error {
	if len(escTypes) != len(tsTypes) {
		return fmt.Errorf("%s arity: esc=%d ts=%d", kind, len(escTypes), len(tsTypes))
	}
	used := make([]bool, len(tsTypes))
	for i, e := range escTypes {
		matched := false
		for j, ts := range tsTypes {
			if used[j] {
				continue
			}
			if compareTypes(e, ts) == nil {
				used[j] = true
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%s elem %d (%s) not in ts", kind, i, typeStrEsc(e))
		}
	}
	return nil
}

// ---- string formatters for diagnostic messages ----

func primitiveName(k dts_parser.PrimitiveKind) string {
	switch k {
	case dts_parser.PrimAny:
		return "any"
	case dts_parser.PrimUnknown:
		return "unknown"
	case dts_parser.PrimVoid:
		return "void"
	case dts_parser.PrimNull:
		return "null"
	case dts_parser.PrimUndefined:
		return "undefined"
	case dts_parser.PrimNever:
		return "never"
	case dts_parser.PrimString:
		return "string"
	case dts_parser.PrimNumber:
		return "number"
	case dts_parser.PrimBoolean:
		return "boolean"
	case dts_parser.PrimBigInt:
		return "bigint"
	case dts_parser.PrimSymbol:
		return "symbol"
	case dts_parser.PrimUniqueSymbol:
		return "unique symbol"
	case dts_parser.PrimObject:
		return "object"
	}
	return "?"
}

func qualIdentToStringDts(qi dts_parser.QualIdent) string {
	switch v := qi.(type) {
	case *dts_parser.Ident:
		return v.Name
	case *dts_parser.Member:
		return qualIdentToStringDts(v.Left) + "." + v.Right.Name
	}
	return "?"
}

func typeStrTS(t dts_parser.TypeAnn) string {
	if t == nil {
		return "<nil>"
	}
	switch v := t.(type) {
	case *dts_parser.PrimitiveType:
		return primitiveName(v.Kind)
	case *dts_parser.LiteralType:
		return tsLiteralStr(v.Literal)
	case *dts_parser.TypeReference:
		s := qualIdentToStringDts(v.Name)
		if len(v.TypeArgs) > 0 {
			args := make([]string, len(v.TypeArgs))
			for i, a := range v.TypeArgs {
				args[i] = typeStrTS(a)
			}
			s += "<" + strings.Join(args, ", ") + ">"
		}
		return s
	case *dts_parser.UnionType:
		parts := make([]string, len(v.Types))
		for i, t := range v.Types {
			parts[i] = typeStrTS(t)
		}
		return strings.Join(parts, " | ")
	case *dts_parser.IntersectionType:
		parts := make([]string, len(v.Types))
		for i, t := range v.Types {
			parts[i] = typeStrTS(t)
		}
		return strings.Join(parts, " & ")
	case *dts_parser.FunctionType:
		params := make([]string, len(v.Params))
		for i, p := range v.Params {
			opt := ""
			if p.Optional {
				opt = "?"
			}
			params[i] = p.Name.Name + opt + ": " + typeStrTS(p.Type)
		}
		return "(" + strings.Join(params, ", ") + ") => " + typeStrTS(v.ReturnType)
	case *dts_parser.ArrayType:
		return typeStrTS(v.ElementType) + "[]"
	case *dts_parser.ParenthesizedType:
		return "(" + typeStrTS(v.Type) + ")"
	case *dts_parser.ThisType:
		return "this"
	}
	return fmt.Sprintf("<ts:%T>", t)
}

func typeStrEsc(t ast.TypeAnn) string {
	if t == nil {
		return "<nil>"
	}
	switch v := t.(type) {
	case *ast.NumberTypeAnn:
		return "number"
	case *ast.StringTypeAnn:
		return "string"
	case *ast.BooleanTypeAnn:
		return "boolean"
	case *ast.VoidTypeAnn:
		return "void"
	case *ast.AnyTypeAnn:
		return "any"
	case *ast.UnknownTypeAnn:
		return "unknown"
	case *ast.NeverTypeAnn:
		return "never"
	case *ast.BigintTypeAnn:
		return "bigint"
	case *ast.SymbolTypeAnn:
		return "symbol"
	case *ast.LitTypeAnn:
		return escLitStr(v)
	case *ast.TypeRefTypeAnn:
		s := ast.QualIdentToString(v.Name)
		if len(v.TypeArgs) > 0 {
			args := make([]string, len(v.TypeArgs))
			for i, a := range v.TypeArgs {
				args[i] = typeStrEsc(a)
			}
			s += "<" + strings.Join(args, ", ") + ">"
		}
		return s
	case *ast.UnionTypeAnn:
		parts := make([]string, len(v.Types))
		for i, t := range v.Types {
			parts[i] = typeStrEsc(t)
		}
		return strings.Join(parts, " | ")
	case *ast.IntersectionTypeAnn:
		parts := make([]string, len(v.Types))
		for i, t := range v.Types {
			parts[i] = typeStrEsc(t)
		}
		return strings.Join(parts, " & ")
	case *ast.FuncTypeAnn:
		params := make([]string, len(v.Params))
		for i, p := range v.Params {
			opt := ""
			if p.Optional {
				opt = "?"
			}
			name := paramName(p)
			params[i] = name + opt + ": " + typeStrEsc(p.TypeAnn)
		}
		return "fn(" + strings.Join(params, ", ") + ") -> " + typeStrEsc(v.Return)
	}
	return fmt.Sprintf("<esc:%T>", t)
}

func paramName(p *ast.Param) string {
	if ip, ok := p.Pattern.(*ast.IdentPat); ok {
		return ip.Name
	}
	return "?"
}

func escLitStr(l *ast.LitTypeAnn) string {
	if l == nil || l.Lit == nil {
		return "<nil-lit>"
	}
	switch v := l.Lit.(type) {
	case *ast.StrLit:
		return fmt.Sprintf("%q", v.Value)
	case *ast.NumLit:
		return fmt.Sprintf("%v", v.Value)
	case *ast.BoolLit:
		return fmt.Sprintf("%v", v.Value)
	case *ast.NullLit:
		return "null"
	case *ast.UndefinedLit:
		return "undefined"
	}
	return fmt.Sprintf("<lit:%T>", l.Lit)
}

func tsLiteralStr(l dts_parser.Literal) string {
	if l == nil {
		return "<nil>"
	}
	switch v := l.(type) {
	case *dts_parser.StringLiteral:
		return fmt.Sprintf("%q", v.Value)
	case *dts_parser.NumberLiteral:
		return fmt.Sprintf("%v", v.Value)
	case *dts_parser.BooleanLiteral:
		return fmt.Sprintf("%v", v.Value)
	case *dts_parser.BigIntLiteral:
		return v.Value + "n"
	}
	return "<unknown-lit>"
}
