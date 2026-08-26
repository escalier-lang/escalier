package dts_to_esc

import (
	"sort"
	"strconv"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/ecma262"
	"github.com/escalier-lang/escalier/internal/set"
)

// CollectDeclarations returns the member surface mod declares, in the order
// the members are emitted.
//
// Each member is addressed by spelling the canonical spec key that names it
// and running that through ecma262.Normalize, so both sides of the join run
// the same rules. The owner comes from the dotted runtime path the converter
// records for every emitted declaration in StandaloneModule.Paths:
//
//   - `push` on the `Array` instance side spells "Array.prototype.push".
//   - `from` on the static side spells "Array.from".
//   - `max` on `Math`, which the language exposes as a namespace rather than
//     a constructor, spells "Math.max" and normalizes to a function with no
//     receiver.
//
// Overloads collapse. The type source spells an overload set as one member
// per signature, and a single spec algorithm sits behind all of them, so
// every signature under the same MemberRef lands in one Declaration. That is
// what lets a fact's position-keyed parts resolve per overload while its
// algorithm-level parts apply to all of them.
//
// A key that names no owner is reported in Declarations.Unkeyed rather than
// dropped, so the join accounts for everything the converter emits. The
// functions the global object holds land there, since `parseInt` has no
// owner to hang off.
//
// Fields, constructors, and index signatures are skipped. The join carries
// effect facts about calls, and the spec keys no algorithm for them.
func CollectDeclarations(mod *StandaloneModule) ecma262.Declarations {
	var (
		order   []ecma262.MemberRef
		unkeyed []string
	)
	sigs := make(map[ecma262.MemberRef][]ecma262.Signature)

	add := func(specKey string, sig ecma262.Signature) {
		ref, ok := ecma262.Normalize(specKey)
		if !ok {
			unkeyed = append(unkeyed, specKey)
			return
		}
		if _, seen := sigs[ref]; !seen {
			order = append(order, ref)
		}
		sigs[ref] = append(sigs[ref], sig)
	}

	statics := staticOwners(mod)
	mod.Module.Namespaces.Scan(func(_ string, ns *ast.Namespace) bool {
		for _, decl := range ns.Decls {
			path := mod.Paths[decl]
			if path == "" {
				continue
			}
			switch d := decl.(type) {
			case *ast.FuncDecl:
				// A namespace member is flattened to a top-level function
				// whose path is already the spec key that names it.
				add(path, signatureOf(d.Params))
			case *ast.ClassDecl:
				for _, elem := range d.Body {
					collectClassElem(path, elem, add)
				}
			case *ast.InterfaceDecl:
				// An interface bound to a `declare var` holds that binding's
				// statics; any other interface describes instances of itself.
				owner, static := statics[d.Name.Name]
				if !static {
					owner = path + ".prototype"
				}
				if d.TypeAnn == nil {
					continue
				}
				for _, elem := range d.TypeAnn.Elems {
					collectObjTypeElem(owner, elem, add)
				}
			}
		}
		return true
	})

	decls := ecma262.Declarations{
		Keyed:   make([]ecma262.Declaration, 0, len(order)),
		Unkeyed: unkeyed,
	}
	for _, ref := range order {
		decls.Keyed = append(decls.Keyed, ecma262.Declaration{Ref: ref, Signatures: sigs[ref]})
	}
	return decls
}

// staticOwners maps an interface name to the runtime path whose statics it
// describes. TypeScript spells a constructor as a `declare var` typed by a
// separate interface, so `declare var Array: ArrayConstructor` makes every
// member of `ArrayConstructor` a static of `Array`. detectTrios fuses that
// shape into one class where it can. This recovers the same link for the
// declarations where the fusion did not fire.
//
// The interface must describe a function object, which is what separates a
// constructor from an ordinary value the module happens to bind. `Math` is
// neither callable nor constructible, so `declare var Math: Math` does not
// turn its members into statics. The key is the bare interface name, matching
// how the flattened module resolves a type reference; two same-named
// interfaces from different namespaces already collide there, and the first
// one bound wins here.
func staticOwners(mod *StandaloneModule) map[string]string {
	ifaces := make(map[string]*ast.InterfaceDecl)
	mod.Module.Namespaces.Scan(func(_ string, ns *ast.Namespace) bool {
		for _, decl := range ns.Decls {
			if iface, ok := decl.(*ast.InterfaceDecl); ok {
				if _, taken := ifaces[iface.Name.Name]; !taken {
					ifaces[iface.Name.Name] = iface
				}
			}
		}
		return true
	})

	owners := make(map[string]string)
	mod.Module.Namespaces.Scan(func(_ string, ns *ast.Namespace) bool {
		for _, decl := range ns.Decls {
			v, ok := decl.(*ast.VarDecl)
			if !ok || mod.Paths[decl] == "" {
				continue
			}
			ref, ok := v.TypeAnn.(*ast.TypeRefTypeAnn)
			if !ok {
				continue
			}
			name, ok := ref.Name.(*ast.Ident)
			if !ok || !isFunctionObject(ifaces, name.Name, set.NewSet[string]()) {
				continue
			}
			if _, bound := owners[name.Name]; !bound {
				owners[name.Name] = mod.Paths[decl]
			}
		}
		return true
	})
	return owners
}

// isFunctionObject reports whether the interface named `name` describes a
// callable or constructible object, following `extends` through the module's
// own interfaces. `SymbolConstructor` is callable without being
// constructible, so both signature forms count. `seen` guards against a
// cyclic extends chain.
func isFunctionObject(ifaces map[string]*ast.InterfaceDecl, name string, seen set.Set[string]) bool {
	iface, ok := ifaces[name]
	if !ok || seen.Contains(name) {
		return false
	}
	seen.Add(name)
	if iface.TypeAnn != nil {
		for _, elem := range iface.TypeAnn.Elems {
			switch elem.(type) {
			case *ast.ConstructorTypeAnn, *ast.CallableTypeAnn:
				return true
			}
		}
	}
	for _, base := range iface.Extends {
		if ident, ok := base.Name.(*ast.Ident); ok && isFunctionObject(ifaces, ident.Name, seen) {
			return true
		}
	}
	return false
}

// collectClassElem addresses one class member under the class's runtime path.
// A member on the static side of a fused trio is a static of the constructor;
// a member on the instance side is reached through the prototype.
func collectClassElem(path string, elem ast.ClassElem, add func(string, ecma262.Signature)) {
	var (
		name   ast.ObjKey
		params []*ast.Param
		static bool
		prefix string
	)
	switch e := elem.(type) {
	case *ast.MethodElem:
		name, static = e.Name, e.Static
		params = funcExprParams(e.Fn)
	case *ast.GetterElem:
		name, static, prefix = e.Name, e.Static, "get "
		params = funcExprParams(e.Fn)
	case *ast.SetterElem:
		name, static, prefix = e.Name, e.Static, "set "
		params = funcExprParams(e.Fn)
	default:
		return
	}
	owner := path
	if !static {
		owner += ".prototype"
	}
	add(specKey(prefix, owner, name), signatureOf(params))
}

// collectObjTypeElem addresses one member of an emitted interface. `owner` is
// the path the member hangs off, already carrying the `.prototype` segment
// when the interface describes instances.
func collectObjTypeElem(owner string, elem ast.ObjTypeAnnElem, add func(string, ecma262.Signature)) {
	var (
		name   ast.ObjKey
		fn     *ast.FuncTypeAnn
		prefix string
	)
	switch e := elem.(type) {
	case *ast.MethodTypeAnn:
		name, fn = e.Name, e.Fn
	case *ast.GetterTypeAnn:
		name, fn, prefix = e.Name, e.Fn, "get "
	case *ast.SetterTypeAnn:
		name, fn, prefix = e.Name, e.Fn, "set "
	default:
		return
	}
	var params []*ast.Param
	if fn != nil {
		params = fn.Params
	}
	add(specKey(prefix, owner, name), signatureOf(params))
}

// specKey spells the canonical spec key for one member of owner. A
// symbol-keyed member takes the spec's bracket form, so `[Symbol.iterator]`
// on `Array.prototype` spells "Array.prototype [ @@iterator ]". prefix is
// "get " or "set " for an accessor and empty otherwise.
//
// A member the spec keys no algorithm by — a numeric key, or a computed key
// that is not a well-known symbol access — still gets a key here, spelled the
// way source spells it. ecma262.Normalize refuses it, which lands it in
// Declarations.Unkeyed instead of dropping it unreported.
func specKey(prefix, owner string, name ast.ObjKey) string {
	switch k := name.(type) {
	case *ast.IdentExpr:
		return prefix + owner + "." + k.Name
	case *ast.StrLit:
		return prefix + owner + "." + k.Value
	case *ast.NumLit:
		return prefix + owner + "." + strconv.FormatFloat(k.Value, 'g', -1, 64)
	case *ast.ComputedKey:
		if symbol, found := strings.CutPrefix(astExprDottedName(k.Expr), "Symbol."); found && symbol != "" {
			return prefix + owner + " [ @@" + symbol + " ]"
		}
	}
	return prefix + owner + ".[computed]"
}

// funcExprParams returns a function expression's declared parameters, or nil
// when the member carries no function at all.
func funcExprParams(fn *ast.FuncExpr) []*ast.Param {
	if fn == nil {
		return nil
	}
	return fn.Params
}

// signatureOf reads the declared parameter shape the join needs. A leading
// `this` parameter is a TypeScript receiver annotation rather than an
// argument, so dropping it keeps the remaining positions aligned with the
// spec algorithm's own parameter numbering.
func signatureOf(params []*ast.Param) ecma262.Signature {
	if len(params) > 0 {
		if ident, ok := params[0].Pattern.(*ast.IdentPat); ok && ident.Name == "this" {
			params = params[1:]
		}
	}
	sig := ecma262.Signature{Params: len(params)}
	if len(params) > 0 {
		_, sig.Rest = params[len(params)-1].Pattern.(*ast.RestPat)
	}
	return sig
}

// StdDeclarations collects the declarations of the `std:*` packages across a
// converted partition, in sorted package order. The `web:*` and `node:*`
// packages are left out because their members are outside ECMA-262 by
// construction. No fact addresses one, and reporting them all as gaps would
// bury the `std:*` members the join is measured on.
func StdDeclarations(mods map[string]*StandaloneModule) ecma262.Declarations {
	uris := make([]string, 0, len(mods))
	for uri := range mods {
		if strings.HasPrefix(uri, "std:") {
			uris = append(uris, uri)
		}
	}
	sort.Strings(uris)

	var all ecma262.Declarations
	for _, uri := range uris {
		decls := CollectDeclarations(mods[uri])
		all.Keyed = append(all.Keyed, decls.Keyed...)
		all.Unkeyed = append(all.Unkeyed, decls.Unkeyed...)
	}
	return all
}
