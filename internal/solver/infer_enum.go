package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/provenance"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// inferEnumDecl types an enum declaration (M5 D-Enum), porting the old checker's enum
// model to soltype. The old checker binds the enum name to BOTH a namespace and a type
// alias whose body is a union of the variant types. soltype has no type aliases yet
// (M7), so this binds the enum name as a namespace and binds the type directly to that
// union: `Color` resolves to `Color.RGB | Color.Hex`.
//
// TODO(M7): once type aliases land, bind the enum name to a proper alias whose body is
// this union, so a reference to the enum renders under its own name — `Color` rather
// than the expanded `Color.RGB | Color.Hex` — the way the old checker's TypeRefType
// does. The union built here becomes that alias's body.
//
// Each variant is a `final` ClassType named `Color.RGB` — a nominal token qualified by
// its enum, so two enums that share a variant name stay distinct. Each variant's
// constructor binds as a value under the enum namespace and returns the enum type, so
// `Color.Hex("#fff")` infers `Color.RGB | Color.Hex`, the union the enum names.
//
// It is driven from the SCC type-key path (module.go), which is ordered before the
// enum's value-key component, so the enum type binding and the constructor namespace
// exist for every later declaration that references the enum. The value key is a no-op
// skipped as handled.
//
// The enum type binding is defined BEFORE variant constructor parameters are resolved,
// so a self-recursive enum such as `enum Tree { Leaf, Node(left: Tree, right: Tree) }`
// resolves each variant's `Tree` parameter to the enum union under construction.
//
// Scope: the enum is the union of its variant tokens — the exhaustiveness substrate D2
// needs, which reads the union's members. A generic enum's variant arguments follow the
// shared class variance and are not specialized here.
func (c *checker) inferEnumDecl(scope *Scope, lvl int, decl *ast.EnumDecl, ns string) {
	// An enum-body type reference resolves against the enum's own namespace first, the
	// same qualified-first resolution a class body uses. Save and restore around the walk.
	prevNS := c.classNamespace
	c.classNamespace = ns
	defer func() { c.classNamespace = prevNS }()

	qname := qualifyEnumName(ns, decl)

	// Resolve the enum's type parameters into a child scope so a variant parameter and a
	// variant's own type arguments resolve the enum's T to one shared var, quantified at
	// the enum boundary and freshened per construction. A non-generic enum reuses scope.
	declScope := scope
	var typeParams []*soltype.TypeParam
	if len(decl.TypeParams) > 0 {
		declScope = scope.Child()
		typeParams = c.resolveTypeParams(declScope, lvl, decl.TypeParams)
	}
	typeArgs := typeParamVars(typeParams)

	// Phase 1: mint each variant's nominal token and register its ClassDef, so a variant
	// parameter referencing a sibling variant resolves before any constructor is built.
	variants := make([]*ast.EnumVariant, 0, len(decl.Elems))
	variantTypes := make([]soltype.Type, 0, len(decl.Elems))
	for _, elem := range decl.Elems {
		variant, ok := elem.(*ast.EnumVariant)
		if !ok {
			// Enum spreads (`...OtherEnum`) merge another enum's variants and are deferred;
			// the EnumDecl kind is supported, so report the spread as an unsupported feature.
			c.reportUnsupportedFeature(elem, "EnumSpread")
			continue
		}
		vname := qname + "." + variant.Name.Name
		vt := &soltype.ClassType{Name: vname, TypeArgs: typeArgs, Final: true, Variant: true}
		c.ctx.registerClass(vname, &ClassDef{
			TypeParams: typeParams,
			Variance:   make([]Variance, len(typeParams)),
			Body:       &soltype.ObjectType{},
			Static:     &soltype.ObjectType{},
			Level:      lvl - 1,
		})
		c.recordType(variant.Name, vt)
		variants = append(variants, variant)
		variantTypes = append(variantTypes, vt)
	}

	// The enum type is the union of its variants. Define it before resolving variant
	// parameters so a recursive variant resolves the enum name to this union.
	enumType := soltype.Type(&soltype.UnionType{Types: variantTypes})
	scope.defineType(qname, TypeBinding{
		Type:    enumType,
		Sources: []provenance.Provenance{&ast.NodeProvenance{Node: decl}},
	})
	c.recordType(decl.Name, enumType)

	// Phase 2: build each variant's constructor and bind it in the enum's namespace.
	nsValues := map[string]ValueBinding{}
	nsTypes := map[string]TypeBinding{}
	for i, variant := range variants {
		ctor := c.variantConstructor(declScope, lvl, variant, enumType)
		nsValues[variant.Name.Name] = ValueBinding{
			Schemes: []TypeScheme{c.generalize(ctor, lvl-1)},
			Sources: []provenance.Provenance{&ast.NodeProvenance{Node: variant}},
		}
		nsTypes[variant.Name.Name] = TypeBinding{
			Type:    variantTypes[i],
			Sources: []provenance.Provenance{&ast.NodeProvenance{Node: variant}},
		}
	}
	scope.defineNamespace(decl.Name.Name, &Namespace{
		Name:   qname,
		Values: nsValues,
		Types:  nsTypes,
		Nested: map[string]*Namespace{},
	})
}

// variantConstructor builds one enum variant's constructor: a function taking the
// variant's declared parameters and returning the ENUM type, so `Color.Hex("#fff")`
// infers the enum union `Color.RGB | Color.Hex`, not one variant — matching the old
// checker, where a constructor yields the enum and a match narrows it back to a variant.
// For a generic enum the return and the parameters share the enum's type-parameter vars;
// the caller generalizes the result so each construction freshens them, the same
// let-polymorphism a plain generic value uses.
func (c *checker) variantConstructor(scope *Scope, lvl int, variant *ast.EnumVariant, ret soltype.Type) *soltype.FuncType {
	params := make([]*soltype.FuncParam, len(variant.Params))
	for i, p := range variant.Params {
		name, ok := identPatName(p.Pattern)
		if !ok {
			name = "arg"
		}
		params[i] = &soltype.FuncParam{
			Pattern:  &soltype.IdentPat{Name: name},
			Type:     c.paramType(scope, p, lvl),
			Optional: p.Optional,
		}
	}
	return &soltype.FuncType{Params: params, Ret: ret}
}

// qualifyEnumName returns an enum's dep_graph-qualified name — the namespace joined to
// the local name with a dot, or the bare local name at the root namespace. It mirrors
// qualifyClassName so an enum's registry keys and type binding match the qualified key
// dep_graph forms.
func qualifyEnumName(ns string, decl *ast.EnumDecl) string {
	if ns == "" {
		return decl.Name.Name
	}
	return ns + "." + decl.Name.Name
}
