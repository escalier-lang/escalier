package solver

import (
	"fmt"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/provenance"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// Enum inference (M5 D-Enum) ports the old checker's enum model to soltype. The enum name
// binds to BOTH a namespace and a transparent type alias whose body is the union of the
// variant types. A reference to the enum renders under its own name, `Color` rather than
// the expanded `Color.RGB | Color.Hex`, the way the old checker's TypeRefType does. A
// consumer that reads the variant members, such as match exhaustiveness, first expands the
// alias to its union body through the shared expandAlias helper, the same unfold constrain
// performs on an alias sub or super.
//
// Each variant is a `final` ClassType named `Color.RGB` — a nominal handle qualified by
// its enum, so two enums that share a variant name stay distinct. Each variant's
// constructor binds as a value under the enum namespace and returns the enum's alias type,
// so `Color.Hex("#fff")` infers `Color`, the enum the alias names.
//
// Inference is two-phase, so a group of mutually-recursive enums resolves each other:
//
//   - preBindEnum mints every variant handle and binds the enum's alias TYPE, but does
//     NOT resolve variant parameters. Running it over every enum in a dep_graph type-key
//     component before any body means `enum A { X(b: B) }` / `enum B { Y(a: A) }` each
//     find the sibling's alias already bound. A self-recursive enum resolves through its
//     own alias the same way.
//   - inferEnumBody then resolves each variant's parameters, builds its constructor, and
//     binds the constructor namespace.
//
// Both run from the SCC type-key path (module.go), which is ordered before the enum's
// value-key component, so the enum type binding and the constructor namespace exist for
// every later declaration that references the enum. The value key is a no-op skipped as
// handled.
//
// Scope: the enum is the union of its variant handles — the exhaustiveness substrate D2
// needs, which reads the union's members. A generic enum's variant arguments follow the
// shared class variance and are not specialized here.

// enumShell carries an enum's pre-bound state from preBindEnum to inferEnumBody: the
// resolved type parameters, the minted variant handles, and the union those form. The
// body pass reuses this state so a variant constructor shares the exact type-parameter
// vars the union holds rather than minting a second, unrelated set.
type enumShell struct {
	decl  *ast.EnumDecl
	ns    string
	qname string
	lvl   int
	// scope is the scope the enum is declared in — where its type binding and namespace
	// live. declScope is scope, or a child holding a generic enum's type parameters, and
	// is where variant parameters resolve.
	scope        *Scope
	declScope    *Scope
	variants     []*ast.EnumVariant
	variantTypes []soltype.Type
	enumType     soltype.Type
}

// preBindEnum mints an enum's variant handles, registers their ClassDefs, and binds the
// enum name's TYPE to an alias whose body is the union of those handles — without resolving
// any variant parameter. It returns the shell inferEnumBody completes. Binding the alias up
// front is what lets a sibling enum, or the enum itself, resolve this name while its own
// body is still being walked.
func (c *checker) preBindEnum(scope *Scope, lvl int, decl *ast.EnumDecl, ns string) *enumShell {
	// An enum-body type reference resolves against the enum's own namespace first, the
	// same qualified-first resolution a class body uses. Save and restore around the walk.
	prevNS := c.classNamespace
	c.classNamespace = ns
	defer func() { c.classNamespace = prevNS }()

	// The enum's dep_graph-qualified name — the namespace joined to the local name, or the
	// bare local name at the root namespace — mirroring the key dep_graph forms and the
	// qualified names class registration uses.
	qname := decl.Name.Name
	if ns != "" {
		qname = ns + "." + decl.Name.Name
	}

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

	// Mint each variant's nominal handle and register its ClassDef, so a variant parameter
	// referencing a sibling variant resolves before any constructor is built.
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

	// Register the enum as a transparent alias whose body is the variant union, then bind
	// the enum name to an AliasType handle carrying the enum's own type-parameter vars. A
	// reference renders under the enum name, and a consumer reading the variants expands the
	// alias to this union body through expandAlias. The variant constructor returns the same
	// handle, so `Color.Hex("#fff")` infers `Color`.
	c.ctx.registerAlias(qname, &AliasDef{
		TypeParams: typeParams,
		Body:       &soltype.UnionType{Types: variantTypes},
		Level:      lvl - 1,
	})
	enumType := soltype.Type(&soltype.AliasType{Name: qname, TypeArgs: typeArgs})
	scope.defineType(qname, TypeBinding{
		Type:    enumType,
		Sources: []provenance.Provenance{&ast.NodeProvenance{Node: decl}},
	})
	c.recordType(decl.Name, enumType)

	return &enumShell{
		decl:         decl,
		ns:           ns,
		qname:        qname,
		lvl:          lvl,
		scope:        scope,
		declScope:    declScope,
		variants:     variants,
		variantTypes: variantTypes,
		enumType:     enumType,
	}
}

// inferEnumBody completes a pre-bound enum: it resolves each variant's parameters — which
// may name a sibling enum bound by preBindEnum — builds each variant's constructor, and
// binds the constructor namespace. It runs after every enum in the recursive group is
// pre-bound, so a cross-enum parameter reference resolves.
func (c *checker) inferEnumBody(sh *enumShell) {
	prevNS := c.classNamespace
	c.classNamespace = sh.ns
	defer func() { c.classNamespace = prevNS }()

	nsValues := map[string]ValueBinding{}
	nsTypes := map[string]TypeBinding{}
	for i, variant := range sh.variants {
		ctor := c.variantConstructor(sh.declScope, sh.lvl, variant, sh.enumType)
		nsValues[variant.Name.Name] = ValueBinding{
			Schemes: []TypeScheme{c.generalize(ctor, sh.lvl-1)},
			Sources: []provenance.Provenance{&ast.NodeProvenance{Node: variant}},
		}
		nsTypes[variant.Name.Name] = TypeBinding{
			Type:    sh.variantTypes[i],
			Sources: []provenance.Provenance{&ast.NodeProvenance{Node: variant}},
		}
	}
	sh.scope.defineNamespace(sh.decl.Name.Name, &Namespace{
		Name:   sh.qname,
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
		// A destructuring or wildcard parameter has no single name, so fall back to a
		// positional placeholder — distinct per position, mirroring memberSigStub — so
		// two unnamed params do not collide on one name.
		name, ok := identPatName(p.Pattern)
		if !ok {
			name = fmt.Sprintf("arg%d", i)
		}
		params[i] = &soltype.FuncParam{
			Pattern:  &soltype.IdentPat{Name: name},
			Type:     c.paramType(scope, p, lvl),
			Optional: p.Optional,
		}
	}
	return &soltype.FuncType{Params: params, Ret: ret}
}
