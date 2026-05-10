package interop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/escalier-lang/escalier/internal/set"
)

// memberKind distinguishes the kind of a class member for override lookup.
type memberKind int

const (
	kindMethod memberKind = iota
	kindGetter
	kindSetter
	kindField
)

// overrideKey identifies a single declaration for override lookup.
// Module is empty for the global scope; ClassName is empty for module-level
// functions. Kind distinguishes getters from setters when they share a name.
type overrideKey struct {
	Module    string
	ClassName string
	Member    string
	Kind      memberKind
}

// overrideEntry holds the mutability classification for a single declaration.
type overrideEntry struct {
	Mut bool // true = mutating receiver
}

// OverrideRegistry holds parsed overrides indexed by overrideKey.
// User overrides take priority over shipped overrides on lookup.
type OverrideRegistry struct {
	user        map[overrideKey]overrideEntry
	shipped     map[overrideKey]overrideEntry
	pureModules set.Set[string]
}

func newOverrideRegistry() *OverrideRegistry {
	return &OverrideRegistry{
		user:        make(map[overrideKey]overrideEntry),
		shipped:     make(map[overrideKey]overrideEntry),
		pureModules: set.NewSet[string](),
	}
}

// loadSource parses override source code and adds its entries to the registry.
// path is used only in error messages. isUser=true places entries in the user
// tier (resolution-order tier 3); isUser=false places them in the shipped tier
// (tier 4).
func (r *OverrideRegistry) loadSource(src, path string, isUser bool) error {
	entries, err := parseOverrideSource(src, path)
	if err != nil {
		return err
	}
	target := r.shipped
	if isUser {
		target = r.user
	}
	for k, v := range entries {
		target[k] = v
	}
	return nil
}

// loadFile reads an .esc file and adds its override entries to the registry.
func (r *OverrideRegistry) loadFile(path string, isUser bool) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return r.loadSource(string(data), path, isUser)
}

// loadDir recursively loads all .esc files under dir into the registry.
func (r *OverrideRegistry) loadDir(dir string, isUser bool) error {
	return filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && filepath.Ext(path) == ".esc" {
			return r.loadFile(path, isUser)
		}
		return nil
	})
}

// addPureModule marks an entire module as non-mutating at tier 4.
// Any lookup for a key whose Module matches returns non-mutating when no
// specific override exists.
func (r *OverrideRegistry) addPureModule(module string) {
	r.pureModules.Add(module)
}

// lookup finds the override entry for key, preferring user overrides over
// shipped overrides. Returns (entry, isUser, true) if found.
func (r *OverrideRegistry) lookup(key overrideKey) (overrideEntry, bool, bool) {
	if entry, ok := r.user[key]; ok {
		return entry, true, true
	}
	if entry, ok := r.shipped[key]; ok {
		return entry, false, true
	}
	if key.Module != "" && r.pureModules.Contains(key.Module) {
		return overrideEntry{Mut: false}, false, true
	}
	return overrideEntry{}, false, false
}

// parseOverrideSource parses Escalier source and extracts all override entries
// from `override declare global { ... }` and `override declare module "..." { ... }`
// blocks.
func parseOverrideSource(src, path string) (map[overrideKey]overrideEntry, error) {
	source := &ast.Source{Path: path, Contents: src}
	p := parser.NewParser(context.Background(), source)
	script, errs := p.ParseScript()
	if len(errs) > 0 {
		return nil, fmt.Errorf("parse error in %s: %v", path, errs[0])
	}

	result := make(map[overrideKey]overrideEntry)
	for _, stmt := range script.Stmts {
		ds, ok := stmt.(*ast.DeclStmt)
		if !ok {
			continue
		}
		switch d := ds.Decl.(type) {
		case *ast.DeclareGlobalDecl:
			if d.Override() {
				extractOverrideClassEntries(d.Decls, "", result)
			}
		case *ast.DeclareModuleDecl:
			if d.Override() {
				extractOverrideClassEntries(d.Decls, d.Name.Value, result)
			}
		}
	}
	return result, nil
}

// extractOverrideClassEntries walks decls and records override entries under module.
func extractOverrideClassEntries(decls []ast.Decl, module string, result map[overrideKey]overrideEntry) {
	for _, decl := range decls {
		switch d := decl.(type) {
		case *ast.ClassDecl:
			className := d.Name.Name
			for _, elem := range d.Body {
				name, kind, mut, ok := overrideClassElemEntry(elem)
				if !ok {
					continue
				}
				key := overrideKey{Module: module, ClassName: className, Member: name, Kind: kind}
				result[key] = overrideEntry{Mut: mut}
			}
		case *ast.FuncDecl:
			// Module-level function overrides are always non-mutating (pure).
			key := overrideKey{Module: module, ClassName: "", Member: d.Name.Name, Kind: kindMethod}
			result[key] = overrideEntry{Mut: false}
		}
	}
}

// overrideClassElemEntry extracts (memberName, kind, isMutating, ok) from a class element.
func overrideClassElemEntry(elem ast.ClassElem) (string, memberKind, bool, bool) {
	switch e := elem.(type) {
	case *ast.MethodElem:
		name, ok := overrideObjKeyName(e.Name)
		if !ok {
			return "", 0, false, false
		}
		mut := e.Receiver != nil && e.Receiver.Mut
		return name, kindMethod, mut, true
	case *ast.GetterElem:
		name, ok := overrideObjKeyName(e.Name)
		if !ok {
			return "", 0, false, false
		}
		return name, kindGetter, false, true
	case *ast.SetterElem:
		name, ok := overrideObjKeyName(e.Name)
		if !ok {
			return "", 0, false, false
		}
		return name, kindSetter, true, true
	case *ast.FieldElem:
		name, ok := overrideObjKeyName(e.Name)
		if !ok {
			return "", 0, false, false
		}
		return name, kindField, !e.Readonly, true
	}
	return "", 0, false, false
}

// overrideObjKeyName extracts a stable string name from an ObjKey.
// Computed keys of the form [Symbol.foo] are normalized to "symbol:foo".
// All other computed keys return ("", false).
func overrideObjKeyName(key ast.ObjKey) (string, bool) {
	switch k := key.(type) {
	case *ast.IdentExpr:
		return k.Name, true
	case *ast.StrLit:
		return k.Value, true
	case *ast.ComputedKey:
		member, ok := k.Expr.(*ast.MemberExpr)
		if !ok {
			return "", false
		}
		obj, ok := member.Object.(*ast.IdentExpr)
		if !ok || obj.Name != "Symbol" {
			return "", false
		}
		return "symbol:" + member.Prop.Name, true
	}
	return "", false
}
