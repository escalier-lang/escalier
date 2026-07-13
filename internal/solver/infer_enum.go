package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/provenance"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// inferEnumDecl types an enum declaration (M5 D-Enum), porting the old checker's
// nominal enum model to soltype. An enum name binds as a TYPE to a nominal ClassType,
// so a value of the enum renders `Color`. Each variant is a `final` ClassType named
// `Color.RGB` whose Supers name the enum, and each variant's constructor binds as a
// VALUE under a namespace named for the enum. A constructor returns the enum type, so
// `Color.Hex("#fff")` infers `Color`; the variant type surfaces when a match narrows
// the enum back to one case (D2).
//
// Qualifying every variant with its enum keeps two enums that share a variant name
// distinct — `Color.RGB` and `Pixel.RGB` are separate nominal tokens.
//
// It is driven from the SCC type-key path (module.go), which is ordered before the
// enum's value-key component, so the enum type binding and the constructor namespace
// exist for every later declaration that references the enum. The value key is a no-op
// skipped as handled.
//
// The enum type binding is defined BEFORE variant constructor parameters are resolved,
// so a self-recursive enum such as `enum Tree { Leaf, Node(left: Tree, right: Tree) }`
// resolves each variant's `Tree` parameter to the enum under construction.
//
// Scope: the enum is the nominal identity plus its variant subtypes, enough for the
// exhaustiveness substrate D2 needs. Per-variant variance on a generic enum's arguments
// falls out of the shared class variance inference and is not specialized here.
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

	// The enum's own nominal token. It is non-final, since its variants are subtypes that
	// extend it, and it carries the enum's type arguments so `MyOption<number>` resolves.
	enumType := &soltype.ClassType{Name: qname, TypeArgs: typeArgs}
	c.ctx.registerClass(qname, &ClassDef{
		TypeParams: typeParams,
		Variance:   make([]Variance, len(typeParams)),
		Body:       &soltype.ObjectType{},
		Static:     &soltype.ObjectType{},
		Level:      lvl - 1,
	})
	scope.defineType(qname, TypeBinding{
		Type:    enumType,
		Sources: []provenance.Provenance{&ast.NodeProvenance{Node: decl}},
	})
	c.recordType(decl.Name, enumType)

	// Phase 1: mint each variant's nominal token and register its ClassDef, extending the
	// enum, so a variant parameter referencing a sibling variant resolves before any
	// constructor is built.
	variants := make([]*ast.EnumVariant, 0, len(decl.Elems))
	variantTypes := make([]*soltype.ClassType, 0, len(decl.Elems))
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
			Supers:     []*soltype.ClassType{enumType},
			Body:       &soltype.ObjectType{},
			Static:     &soltype.ObjectType{},
			Level:      lvl - 1,
		})
		c.recordType(variant.Name, vt)
		variants = append(variants, variant)
		variantTypes = append(variantTypes, vt)
	}
	if def, ok := c.ctx.classDef(qname); ok {
		def.Variants = variantTypes
	}

	// Phase 2: build each variant's constructor and bind it in the enum's namespace.
	nsValues := map[string]ValueBinding{}
	nsTypes := map[string]TypeBinding{}
	for i, variant := range variants {
		vt := variantTypes[i]
		ctor := c.variantConstructor(declScope, lvl, variant, enumType)
		nsValues[variant.Name.Name] = ValueBinding{
			Schemes: []TypeScheme{c.generalize(ctor, lvl-1)},
			Sources: []provenance.Provenance{&ast.NodeProvenance{Node: variant}},
		}
		nsTypes[variant.Name.Name] = TypeBinding{
			Type:    vt,
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
// infers `Color`, not the narrower `Color.Hex` — matching the old checker, where a
// constructor call yields the enum and a match narrows it back to a variant. For a
// generic enum the return `MyOption<T>` and the parameters share the enum's
// type-parameter vars; the caller generalizes the result so each construction freshens
// them, the same let-polymorphism a plain generic value uses.
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
