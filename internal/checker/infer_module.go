package checker

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dep_graph"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// Callers of this function should create a new scope when inferring a module.
// If it's inferring global declarations then it's okay to omit that step.
// TODO: Create separate InferModuleDepGraph and InferGlobalDepGraph functions?
func (c *Checker) InferDepGraph(ctx Context, depGraph *dep_graph.DepGraph) (errors []Error) {
	defer recoverTimeout(&errors)
	for _, component := range depGraph.Components {
		c.checkTimeout()
		declsErrors := c.InferComponent(ctx, depGraph, component)
		errors = slices.Concat(errors, declsErrors)
	}

	return errors
}

// GetNamespaceCtx returns a new Context with its namespace set to the namespace of
// the binding with the given key. If the namespace doesn't exist yet, it creates one.
// The namespace's Exported flag is set based on the ast.Namespace.Exported field
// from the module (if available).
func GetNamespaceCtx(
	ctx Context,
	depGraph *dep_graph.DepGraph,
	key dep_graph.BindingKey,
) Context {
	nsName := depGraph.GetNamespace(key)
	if nsName == "" {
		return ctx
	}
	ns := ctx.Scope.Namespace
	nsCtx := ctx
	qualifiedName := ""
	for part := range strings.SplitSeq(nsName, ".") {
		// Build the qualified namespace name for looking up export status
		if qualifiedName == "" {
			qualifiedName = part
		} else {
			qualifiedName = qualifiedName + "." + part
		}

		if _, ok := ns.GetNamespace(part); !ok {
			newNs := type_system.NewNamespace()
			// Check if this namespace is exported by looking up the ast.Namespace
			if ctx.Module != nil {
				if astNs, exists := ctx.Module.Namespaces.Get(qualifiedName); exists {
					newNs.Exported = astNs.Exported
				}
			}
			ns.SetNamespace(part, newNs)
		}
		ns, _ = ns.GetNamespace(part)
		nsCtx = nsCtx.WithNewScopeAndNamespace(ns)
	}
	return nsCtx
}

// GetDeclContext returns a Context for inferring a specific declaration.
// It uses the declaration's file scope for lookups (to resolve file-scoped imports),
// while ensuring declarations are written to the correct module namespace.
func GetDeclContext(
	ctx Context,
	depGraph *dep_graph.DepGraph,
	key dep_graph.BindingKey,
	decl ast.Decl,
) Context {
	// Get the base namespace context (for writing declarations)
	nsCtx := GetNamespaceCtx(ctx, depGraph, key)

	// If we have file scopes, use the file scope for this declaration's lookups
	if ctx.FileScopes != nil {
		sourceID := decl.Span().SourceID
		if fileScope, ok := ctx.FileScopes[sourceID]; ok {
			// Create a new scope that:
			// 1. Uses the module namespace (for writing declarations)
			// 2. Has the file scope as parent (for import resolution)
			declScope := &Scope{
				Parent:    fileScope,
				Namespace: nsCtx.Scope.Namespace,
			}
			return Context{
				Scope:                  declScope,
				IsAsync:                nsCtx.IsAsync,
				IsPatMatch:             nsCtx.IsPatMatch,
				AllowUndefinedTypeRefs: nsCtx.AllowUndefinedTypeRefs,
				TypeRefsToUpdate:       nsCtx.TypeRefsToUpdate,
				FileScopes:             ctx.FileScopes,
				Module:                 ctx.Module,
			}
		}
	}

	return nsCtx
}

// placeholderPriority returns the processing priority for a binding key in the placeholder phase.
// Lower numbers are processed first. This ensures correct ordering for cyclic
// dependencies like Symbol/SymbolConstructor.
func placeholderPriority(key dep_graph.BindingKey) int {
	name := key.Name()
	isType := key.IsTypeBinding()

	// Priority 0: *Constructor type bindings (e.g., SymbolConstructor)
	// These define properties like toPrimitive that other types reference
	if isType && strings.HasSuffix(name, "Constructor") {
		return 0
	}

	// Priority 1: Value bindings (e.g., Symbol value, variable declarations)
	// These create value bindings that can be used in computed keys.
	// Note: ClassDecl creates both type AND value bindings, so processing a class's
	// value binding will also define its type (via the processedDefinitions check).
	if !isType {
		return 1
	}

	// Priority 2: Other type bindings (e.g., Symbol type)
	// These may use computed keys that reference values, so they're processed last.
	return 2
}

// definitionPriority returns the processing priority for a binding key in the definition phase.
// VarDecl-only keys have lower priority (processed last) so that function/method return
// types are already inferred when processing VarDecl initializers.
func definitionPriority(depGraph *dep_graph.DepGraph, key dep_graph.BindingKey) int {
	decls := depGraph.GetDecls(key)
	for _, decl := range decls {
		if decl == nil {
			continue
		}
		if _, isVarDecl := decl.(*ast.VarDecl); !isVarDecl {
			return 0 // Non-VarDecl keys first
		}
	}
	if len(decls) > 0 {
		return 1 // VarDecl-only keys last
	}
	return 0
}

// sortKeysForPlaceholders sorts binding keys for the placeholder phase:
// 1. *Constructor type bindings (define properties like toPrimitive)
// 2. Value bindings (create value bindings referencing constructors)
// 3. Instance type bindings (can now resolve computed keys like [Symbol.toPrimitive])
func sortKeysForPlaceholders(keys []dep_graph.BindingKey) []dep_graph.BindingKey {
	sorted := make([]dep_graph.BindingKey, len(keys))
	copy(sorted, keys)

	slices.SortStableFunc(sorted, func(a, b dep_graph.BindingKey) int {
		return placeholderPriority(a) - placeholderPriority(b)
	})

	return sorted
}

// sortKeysForDefinitions sorts binding keys for the definition phase.
// VarDecl-only keys come last so function/method return types are already
// inferred when processing VarDecl initializers.
func sortKeysForDefinitions(depGraph *dep_graph.DepGraph, keys []dep_graph.BindingKey) []dep_graph.BindingKey {
	sorted := make([]dep_graph.BindingKey, len(keys))
	copy(sorted, keys)

	slices.SortStableFunc(sorted, func(a, b dep_graph.BindingKey) int {
		return definitionPriority(depGraph, a) - definitionPriority(depGraph, b)
	})

	return sorted
}

func (c *Checker) InferComponent(
	ctx Context,
	depGraph *dep_graph.DepGraph,
	component []dep_graph.BindingKey,
) []Error {
	errors := []Error{}

	// Sort the component to ensure correct processing order for cyclic dependencies.
	// This ensures *Constructor types are processed before their instance types,
	// which is necessary for patterns like Symbol/SymbolConstructor where the
	// instance type uses computed keys that reference values defined in the constructor.
	sortedComponent := sortKeysForPlaceholders(component)

	// TODO:
	// - ensure there are no duplicate declarations in the module

	// We use ast.Decl as the key since each declaration needs its own state,
	// even when multiple declarations share the same binding key (overloads, interface merging)
	paramBindingsForDecl := make(map[ast.Decl]map[string]*type_system.Binding)
	declCtxMap := make(map[ast.Decl]Context)
	typeRefsToUpdate := make(map[dep_graph.BindingKey][]*type_system.TypeRefType)

	// Store individual function types for each declaration
	// This is needed for overloaded functions where the binding has IntersectionType
	// but we need the individual FuncType for each declaration
	funcTypeForDecl := make(map[ast.Decl]*type_system.FuncType)

	// Track method contexts per class declaration and body element index
	type classMethodCtxKey struct {
		decl      ast.Decl
		elemIndex int
	}
	methodCtxForElem := make(map[classMethodCtxKey]Context)

	// Track which class declarations have a single in-body ConstructorElem
	// whose body should be type-checked in the definition phase. The
	// stored FuncType is the "callable" signature used for overload
	// resolution (return type = Self instance type); the param bindings
	// were produced by inferFuncParams over the constructor's
	// `Fn.Params[1:]` (skipping the leading `mut self`).
	inBodyCtorForDecl := make(map[ast.Decl]*ast.ConstructorElem)
	ctorFuncTypeForDecl := make(map[ast.Decl]*type_system.FuncType)
	ctorParamBindingsForDecl := make(map[ast.Decl]map[string]*type_system.Binding)
	// Constructor-local context (carries ctor-level type params, e.g. U
	// in `constructor<U>(...)`) so the body-checking phase can resolve
	// names introduced only by the constructor signature.
	ctorCtxForDecl := make(map[ast.Decl]Context)

	// Track declarations that have been processed in the placeholder phase.
	// This is needed because classes and enums have both type and value binding keys,
	// and we don't want to process them twice.
	processedPlaceholders := make(map[ast.Decl]bool)

	// Infer placeholders
	for _, key := range sortedComponent {
		decls := depGraph.GetDecls(key)

		for _, decl := range decls {
			// Skip declarations that have already been processed.
			// This can happen for classes and enums which have both type and value binding keys.
			if processedPlaceholders[decl] {
				continue
			}
			processedPlaceholders[decl] = true

			// Get context for this specific declaration, including file scope for imports
			nsCtx := GetDeclContext(ctx, depGraph, key, decl)

			switch decl := decl.(type) {
			case *ast.FuncDecl:
				funcType, funcCtx, paramBindings, sigErrors := c.inferFuncSig(nsCtx, &decl.FuncSig, decl)
				paramBindingsForDecl[decl] = paramBindings
				errors = slices.Concat(errors, sigErrors)

				// Save the context for inferring the function body later
				declCtxMap[decl] = funcCtx

				// Store the individual function type for this declaration
				funcTypeForDecl[decl] = funcType

				// Functions can have multiple declarations.  This is to support function
				// overloading.  We only create a binding for the function if one doesn't
				// already exist.
				binding := nsCtx.Scope.GetValue(decl.Name.Name)
				if binding == nil {
					nsCtx.Scope.setValue(decl.Name.Name, &type_system.Binding{
						Source:     &ast.NodeProvenance{Node: decl},
						Type:       funcType,
						Assignable: false,
						Mutable:    false,
						Exported:   decl.Export(),
					})
				} else {
					// Merge with existing overload by creating a new intersection type
					// This ensures proper normalization and deduplication
					if it, ok := binding.Type.(*type_system.IntersectionType); ok {
						var allTypes []type_system.Type
						allTypes = append(allTypes, it.Types...)
						allTypes = append(allTypes, funcType)
						binding.Type = type_system.NewIntersectionType(nil, allTypes...)
					} else {
						// First overload, create new intersection
						binding.Type = type_system.NewIntersectionType(nil, binding.Type, funcType)
					}
				}

				// Track this declaration for codegen (for overload dispatch generation)
				// Use the fully qualified name if inside a namespace
				nsName := depGraph.GetNamespace(key)
				funcName := decl.Name.Name
				if nsName != "" {
					funcName = nsName + "." + funcName
				}
				c.OverloadDecls[funcName] = append(c.OverloadDecls[funcName], decl)
			case *ast.VarDecl:
				// For destructuring patterns, multiple binding keys share the same VarDecl.
				// If they end up in different components, we might process the same VarDecl
				// multiple times. Check if the pattern type was already set (which happens
				// when we infer the pattern in the placeholder phase).
				if decl.InferredType != nil {
					continue
				}

				patType, bindings, patErrors := c.inferPattern(ctx, decl.Pattern)
				errors = slices.Concat(errors, patErrors)

				// TODO: handle the situation where both decl.Init and decl.TypeAnn
				// are nil

				assignable := decl.Kind == ast.VarKind
				var names []string
				for name, binding := range bindings {
					binding.Exported = decl.Export()
					binding.Assignable = assignable
					nsCtx.Scope.setValue(name, binding)
					names = append(names, name)
				}

				if decl.TypeAnn != nil {
					// It's possible for a type annotation to contain type refs for a type alias
					// that hasn't been defined yet.  This can happen when there's a cyclic dependency
					// between a type alias (or interface) and a variable.  See `FileReader` in
					// lib.dom.d.ts for an example of this.  Most of the time we require that type
					// aliases be defined when inferring type annotations.  Setting `AllowUndefinedTypeRefs`
					// to `true` allows us to disable that check while `TypeRefsToUpdate` used to keep
					// track of which TypeRefTypes needs to be updated later once the type ref has been
					// defined.  See `inferTypeAnn` for more details.
					nsCtx.AllowUndefinedTypeRefs = true
					nsCtx.TypeRefsToUpdate = &Ref[[]*type_system.TypeRefType]{Value: []*type_system.TypeRefType{}}

					taType, taErrors := c.inferTypeAnn(nsCtx, decl.TypeAnn)

					// We need to be careful to reset `AllowUndefinedTypeRefs` so that we continue
					// to require type aliases to be defined when inferring TypeRefTypes in other situations.
					nsCtx.AllowUndefinedTypeRefs = false
					typeRefsToUpdate[key] = nsCtx.TypeRefsToUpdate.Value
					// We need to be careful to reset `TypeRefsToUpdate` so that we aren't accidentally
					// updating certain TypeRefTypes when we shouldn't be.
					nsCtx.TypeRefsToUpdate = &Ref[[]*type_system.TypeRefType]{Value: []*type_system.TypeRefType{}}

					errors = slices.Concat(errors, taErrors)

					unifyErrors := c.Unify(nsCtx, patType, taType)
					errors = slices.Concat(errors, unifyErrors)

					// Promote Mutable from a now-resolved MutType wrap.
					// Catches `val p: mut T = …` (annotation on VarDecl).
					updateBindingMutableFromType(bindings)
				}

				// This is used when inferring the definitions below
				decl.InferredType = patType
			case *ast.TypeDecl:
				// TODO: add new type aliases to ctx.Scope.Types as we go to handle
				// things like:
				// type Point = {x: number, y: number}
				// val p: Point = {x: 1, y: 2}
				typeParams := c.inferTypeParams(decl.TypeParams)

				typeAlias := &type_system.TypeAlias{
					Type:       c.FreshVar(&ast.NodeProvenance{Node: decl}),
					TypeParams: typeParams,
					Exported:   decl.Export(),
				}

				nsCtx.Scope.SetTypeAlias(decl.Name.Name, typeAlias)
			case *ast.ClassDecl:
				// Check if we've already processed this class from another binding key
				// (classes have both type and value keys that may be in different components)
				// Only check the current namespace, not parent scopes - we want to allow
				// local classes to shadow global types from prelude
				if _, exists := nsCtx.Scope.Namespace.Types[decl.Name.Name]; exists {
					// TODO(#295): Handle class declarations and interface declarations with the same name
					// Already processed from another component, skip
					continue
				}

				instanceType := c.FreshVar(&ast.NodeProvenance{Node: decl})

				typeParams := c.inferTypeParams(decl.TypeParams)

				typeAlias := &type_system.TypeAlias{
					Type:       instanceType,
					TypeParams: typeParams,
					Exported:   decl.Export(),
				}

				nsCtx.Scope.SetTypeAlias(decl.Name.Name, typeAlias)
				declCtx := nsCtx.WithNewScope()
				declCtxMap[decl] = declCtx

				for _, typeParam := range typeParams {
					var t type_system.Type = type_system.NewUnknownType(nil)
					if typeParam.Constraint != nil {
						t = typeParam.Constraint
					}
					declCtx.Scope.SetTypeAlias(typeParam.Name, &type_system.TypeAlias{
						Type:        t,
						TypeParams:  []*type_system.TypeParam{},
						IsTypeParam: true,
					})
				}

				// Phase 2: pre-walk the body to count in-body constructors.
				// At most one is allowed for now; mixing with primary-ctor
				// params is also rejected. If neither form is present we
				// synthesize a `ConstructorElem` from the instance fields and
				// prepend it to `decl.Body` so the rest of this phase, the
				// definition phase, and codegen can consume a uniform shape.
				inBodyCtors := []*ast.ConstructorElem{}
				for _, bodyElem := range decl.Body {
					if ctor, ok := bodyElem.(*ast.ConstructorElem); ok {
						inBodyCtors = append(inBodyCtors, ctor)
					}
				}

				if len(decl.Params) > 0 && len(inBodyCtors) > 0 {
					errors = append(errors, MixedConstructorFormsError{span: decl.Name.Span()})
				}

				for _, ctor := range inBodyCtors {
					if ctor.Private {
						errors = append(errors, PrivateConstructorNotYetSupportedError{span: ctor.Span()})
					}
				}

				if len(inBodyCtors) > 1 {
					// Report against the second (and later) constructors.
					for _, extra := range inBodyCtors[1:] {
						errors = append(errors, MultipleConstructorsNotYetSupportedError{span: extra.Span()})
					}
				}

				// Reject field-level defaults when an in-body constructor is
				// present. Only the explicit `= expr` form (`Default`) is
				// rejected here — the legacy `x: expr` shorthand
				// (`FieldElem.Value`) doubles as a typed-field syntax in
				// the current parser, so we leave it alone until Phase 4
				// retires it. Defaults remain valid under the synthesized-
				// constructor case (no in-body ctor).
				if len(inBodyCtors) > 0 {
					for _, bodyElem := range decl.Body {
						field, ok := bodyElem.(*ast.FieldElem)
						if !ok {
							continue
						}
						if field.Default == nil {
							continue
						}
						errors = append(errors, FieldDefaultNotAllowedError{
							FieldName: classFieldName(field.Name),
							span:      field.Span(),
						})
					}
				}

				// Synthesize a constructor when neither a primary-ctor head
				// nor an in-body constructor is present. Subclasses are
				// excluded — auto-synthesizing a constructor for an
				// `extends` class would silently skip the required
				// `super(...)` call. Until subclass-constructor semantics
				// land, require an explicit `constructor` block instead.
				if len(decl.Params) == 0 && len(inBodyCtors) == 0 {
					if decl.Extends != nil {
						errors = append(errors, SubclassConstructorRequiredError{
							span: decl.Name.Span(),
						})
					} else {
						synth, synthErrors := c.synthesizeConstructorElem(decl)
						errors = slices.Concat(errors, synthErrors)
						if synth != nil {
							// Prepend so the rest of the loop sees it like a
							// user-written constructor.
							decl.Body = append([]ast.ClassElem{synth}, decl.Body...)
							inBodyCtors = []*ast.ConstructorElem{synth}
						}
					}
				}

				objTypeElems := []type_system.ObjTypeElem{}
				staticElems := []type_system.ObjTypeElem{}
				instanceSymbolKeyMap := make(map[int]any)
				staticSymbolKeyMap := make(map[int]any)

				for i, elem := range decl.Body {
					switch elem := elem.(type) {
					case *ast.FieldElem:
						key, keyErrors := c.astKeyToTypeKey(declCtx, elem.Name)
						errors = slices.Concat(errors, keyErrors)
						if key == nil {
							continue
						}

						if key.Kind == type_system.SymObjTypeKeyKind {
							if _, ok := elem.Name.(*ast.ComputedKey); ok {
								expr := elem.Name.(*ast.ComputedKey).Expr
								if elem.Static {
									staticSymbolKeyMap[key.Sym] = expr
								} else {
									instanceSymbolKeyMap[key.Sym] = expr
								}
							}
						}

						if elem.Static {
							// Static fields go to the class object type
							tvar := c.FreshVar(nil)
							tvar.FromBinding = true
							propElem := type_system.NewPropertyElem(*key, tvar)
							propElem.Readonly = elem.Readonly
							staticElems = append(
								staticElems,
								propElem,
							)
						} else {
							// Instance fields go to the instance type
							tvar := c.FreshVar(nil)
							tvar.FromBinding = true
							propElem := type_system.NewPropertyElem(*key, tvar)
							propElem.Readonly = elem.Readonly
							objTypeElems = append(
								objTypeElems,
								propElem,
							)
						}
					case *ast.MethodElem:
						key, keyErrors := c.astKeyToTypeKey(declCtx, elem.Name)
						errors = slices.Concat(errors, keyErrors)
						methodType, methodCtx, _, sigErrors := c.inferFuncSig(declCtx, &elem.Fn.FuncSig, elem.Fn)
						errors = slices.Concat(errors, sigErrors)
						if key == nil {
							continue
						}

						if key.Kind == type_system.SymObjTypeKeyKind {
							if _, ok := elem.Name.(*ast.ComputedKey); ok {
								expr := elem.Name.(*ast.ComputedKey).Expr
								if elem.Static {
									staticSymbolKeyMap[key.Sym] = expr
								} else {
									instanceSymbolKeyMap[key.Sym] = expr
								}
							}
						}

						methodCtxForElem[classMethodCtxKey{decl: decl, elemIndex: i}] = methodCtx
						if elem.Static {
							// Static methods go to the class object type
							staticElems = append(
								staticElems,
								type_system.NewMethodElem(*key, methodType, nil), // static methods don't have self
							)
						} else {
							// Instance methods go to the instance type
							objTypeElems = append(
								objTypeElems,
								type_system.NewMethodElem(*key, methodType, elem.MutSelf),
							)
						}
					case *ast.GetterElem:
						key, keyErrors := c.astKeyToTypeKey(declCtx, elem.Name)
						errors = slices.Concat(errors, keyErrors)
						funcType, _, _, sigErrors := c.inferFuncSig(declCtx, &elem.Fn.FuncSig, elem.Fn)
						errors = slices.Concat(errors, sigErrors)
						if key == nil {
							continue
						}

						if key.Kind == type_system.SymObjTypeKeyKind {
							if _, ok := elem.Name.(*ast.ComputedKey); ok {
								expr := elem.Name.(*ast.ComputedKey).Expr
								if elem.Static {
									staticSymbolKeyMap[key.Sym] = expr
								} else {
									instanceSymbolKeyMap[key.Sym] = expr
								}
							}
						}

						if elem.Static {
							// Static getters go to the class object type
							staticElems = append(
								staticElems,
								type_system.NewGetterElem(*key, funcType),
							)
						} else {
							// Instance getters go to the instance type
							objTypeElems = append(
								objTypeElems,
								type_system.NewGetterElem(*key, funcType),
							)
						}
					case *ast.SetterElem:
						key, keyErrors := c.astKeyToTypeKey(declCtx, elem.Name)
						errors = slices.Concat(errors, keyErrors)
						funcType, _, _, sigErrors := c.inferFuncSig(declCtx, &elem.Fn.FuncSig, elem.Fn)
						errors = slices.Concat(errors, sigErrors)
						if key == nil {
							continue
						}

						if key.Kind == type_system.SymObjTypeKeyKind {
							if _, ok := elem.Name.(*ast.ComputedKey); ok {
								expr := elem.Name.(*ast.ComputedKey).Expr
								if elem.Static {
									staticSymbolKeyMap[key.Sym] = expr
								} else {
									instanceSymbolKeyMap[key.Sym] = expr
								}
							}
						}

						if elem.Static {
							// Static setters go to the class object type
							staticElems = append(
								staticElems,
								type_system.NewSetterElem(*key, funcType),
							)
						} else {
							// Instance setters go to the instance type
							objTypeElems = append(
								objTypeElems,
								type_system.NewSetterElem(*key, funcType),
							)
						}
					case *ast.ConstructorElem:
						// Constructors are not class-instance members and so
						// do not contribute to `objTypeElems`. The callable
						// signature is built separately below via
						// `inferConstructorSig` and attached as the
						// containing class type's `ConstructorElem`.
						_ = elem
					default:
						errors = append(errors, &UnimplementedError{
							message: fmt.Sprintf("Unsupported class element type: %T", elem),
							span:    elem.Span(),
						})
					}
				}

				provenance := &ast.NodeProvenance{Node: decl}
				objType := type_system.NewNominalObjectType(provenance, objTypeElems)
				objType.SymbolKeyMap = instanceSymbolKeyMap

				// Infer the Extends clause if present
				if decl.Extends != nil {
					extendsType, extendsErrors := c.inferTypeAnn(declCtx, decl.Extends)
					errors = slices.Concat(errors, extendsErrors)

					if extendsType != nil {
						// The extends type should be a TypeRefType
						if typeRef, ok := extendsType.(*type_system.TypeRefType); ok {
							objType.Extends = []*type_system.TypeRefType{typeRef}
						} else {
							// If it's not a TypeRefType, we still set it but wrap it if needed
							// This handles cases where the type might be pruned or indirect
							prunedType := type_system.Prune(extendsType)
							if typeRef, ok := prunedType.(*type_system.TypeRefType); ok {
								objType.Extends = []*type_system.TypeRefType{typeRef}
							}
						}
					}
				}

				// TODO: call c.bind() directly
				unifyErrors := c.Unify(ctx, instanceType, objType)
				errors = slices.Concat(errors, unifyErrors)

				typeArgs := make([]type_system.Type, len(typeParams))
				for i := range typeParams {
					typeArgs[i] = type_system.NewTypeRefType(nil, typeParams[i].Name, nil)
				}

				retType := type_system.NewTypeRefType(nil, decl.Name.Name, typeAlias, typeArgs...)

				// Decide where the constructor signature comes from. Phase 2:
				// when a single in-body `ConstructorElem` is present, use
				// `inferConstructorSig` (which strips `mut self`, fixes the
				// return type to Self, and layers class+ctor type params).
				// Otherwise fall back to the legacy primary-ctor head until
				// Phase 4 retires it. Multi-constructor support arrives in
				// Phase 5.
				var funcType *type_system.FuncType
				var paramBindings map[string]*type_system.Binding
				if len(inBodyCtors) > 0 {
					ctor := inBodyCtors[0]
					var sigErrors []Error
					var ctorCtx Context
					funcType, ctorCtx, paramBindings, sigErrors = c.inferConstructorSig(
						declCtx, ctor, typeParams, retType, provenance,
					)
					errors = slices.Concat(errors, sigErrors)
					ctorCtxForDecl[decl] = ctorCtx
				} else {
					params, legacyBindings, legacyParamErrors := c.inferFuncParams(declCtx, decl.Params)
					errors = slices.Concat(errors, legacyParamErrors)
					paramBindings = legacyBindings
					funcType = type_system.NewFuncType(
						provenance,
						typeParams,
						params,
						retType,
						type_system.NewNeverType(nil),
					)
				}
				if len(inBodyCtors) > 0 {
					// In-body constructor params are scoped to the
					// constructor body only — they must NOT leak into
					// method/getter/setter scopes (which see fields via
					// `self.<field>`). Stash them in the ctor-specific
					// maps. paramBindingsForDecl is populated from
					// `decl.Params` (the primary-ctor head) below so the
					// legacy shorthand-field path keeps working in mixed
					// (erroring) cases without nil-deref'ing.
					inBodyCtorForDecl[decl] = inBodyCtors[0]
					ctorFuncTypeForDecl[decl] = funcType
					ctorParamBindingsForDecl[decl] = paramBindings
					primaryParams, primaryBindings, primaryErrors := c.inferFuncParams(declCtx, decl.Params)
					_ = primaryParams
					errors = slices.Concat(errors, primaryErrors)
					paramBindingsForDecl[decl] = primaryBindings
				} else {
					paramBindingsForDecl[decl] = paramBindings
				}

				// Create an object type with a constructor element and static methods/properties
				constructorElem := &type_system.ConstructorElem{Fn: funcType}
				classObjTypeElems := []type_system.ObjTypeElem{constructorElem}
				classObjTypeElems = append(classObjTypeElems, staticElems...)

				classObjType := type_system.NewObjectType(provenance, classObjTypeElems)
				classObjType.SymbolKeyMap = staticSymbolKeyMap

				ctor := &type_system.Binding{
					Source:     &ast.NodeProvenance{Node: decl},
					Type:       classObjType,
					Assignable: false,
					Mutable:    false,
					Exported:   decl.Export(),
				}
				nsCtx.Scope.setValue(decl.Name.Name, ctor)

				// Infer lifetime parameters and default mutability for the
				// class. Runs once during the placeholder phase since the class
				// body's stored-field structure is purely syntactic.
				c.InferConstructorLifetimes(decl, typeAlias, funcType)
			case *ast.EnumDecl:
				// Check if we've already processed this enum from another binding key
				// (enums have both type and value keys that may be in different components)
				// Only check the current namespace, not parent scopes - we want to allow
				// local enums to shadow global types from prelude
				if _, exists := nsCtx.Scope.Namespace.Types[decl.Name.Name]; exists {
					// Already processed from another component, skip
					continue
				}

				// Create a new namespace for the enum
				ns := type_system.NewNamespace()
				nsCtx.Scope.setNamespace(decl.Name.Name, ns)

				// Create a fresh type variable for the enum
				enumType := c.FreshVar(&ast.NodeProvenance{Node: decl})

				// Infer type parameters
				typeParams := c.inferTypeParams(decl.TypeParams)

				// Create a type alias with placeholder type
				typeAlias := &type_system.TypeAlias{
					Type:       enumType,
					TypeParams: typeParams,
					Exported:   decl.Export(),
				}

				nsCtx.Scope.SetTypeAlias(decl.Name.Name, typeAlias)

				// Create a context for inferring enum variants
				declCtx := nsCtx.WithNewScope()
				declCtxMap[decl] = declCtx

				// Add each type param as a type alias in the declCtx so that
				// they can be referenced when inferring the enum variants
				for _, typeParam := range typeParams {
					var t type_system.Type = type_system.NewUnknownType(nil)
					if typeParam.Constraint != nil {
						t = typeParam.Constraint
					}
					declCtx.Scope.SetTypeAlias(typeParam.Name, &type_system.TypeAlias{
						Type:        t,
						TypeParams:  []*type_system.TypeParam{},
						IsTypeParam: true,
					})
				}
			case *ast.InterfaceDecl:
				// Similar to TypeDecl, but we need to handle interface merging
				typeParams := c.inferTypeParams(decl.TypeParams)

				// Check if an interface with this name already exists in the CURRENT namespace only.
				// We don't use GetTypeAlias here because it searches up the scope chain,
				// which would incorrectly try to merge package-level declarations with global ones.
				existingAlias := nsCtx.Scope.Namespace.Types[decl.Name.Name]
				if existingAlias == nil {
					// First declaration - create a fresh type variable for the interface
					interfaceType := c.FreshVar(&ast.NodeProvenance{Node: decl})

					typeAlias := &type_system.TypeAlias{
						Type:       interfaceType,
						TypeParams: typeParams,
						Exported:   decl.Export(),
					}

					// Directly set in the namespace to allow interface merging
					// We don't use SetTypeAlias since that would panic if it already exists
					nsCtx.Scope.Namespace.Types[decl.Name.Name] = typeAlias
				}
				// If it already exists, we'll merge during the definition phase
				// Type parameter validation happens in inferInterface
			}
		}
	}

	// Track declarations that have been processed in the definition phase.
	processedDefinitions := make(map[ast.Decl]bool)

	// Sort the component so VarDecl keys come last. This ensures function/method
	// return types are inferred before VarDecl initializers that may reference them.
	sortedForDefs := sortKeysForDefinitions(depGraph, sortedComponent)

	// Infer definitions - single pass with VarDecl processed last due to sorting
	for _, key := range sortedForDefs {
		decls := depGraph.GetDecls(key)

		for _, decl := range decls {
			if decl == nil {
				continue
			}

			// Skip declarations that have already been processed.
			// This can happen for classes and enums which have both type and value binding keys.
			if processedDefinitions[decl] {
				continue
			}
			processedDefinitions[decl] = true

			// Skip FuncDecl that use the `declare` keyword, since they are
			// already fully typed and don't have a body to infer.
			// However, TypeDecl, InterfaceDecl, and EnumDecl still need their types
			// to be inferred and unified with their placeholders.
			if decl.Declare() {
				switch d := decl.(type) {
				case *ast.FuncDecl:
					if ft, ok := funcTypeForDecl[d]; ok {
						c.resolveCallSites(ctx)
						GeneralizeFuncType(ft)
					}
					continue
				case *ast.VarDecl:
					continue
				}
			}

			// Get context for this specific declaration, including file scope for imports
			nsCtx := GetDeclContext(ctx, depGraph, key, decl)

			switch decl := decl.(type) {
			case *ast.FuncDecl:
				// We reuse the function type that was created for this specific declaration
				// For overloaded functions, the binding contains an IntersectionType,
				// but we need the individual FuncType for this particular overload
				funcType := funcTypeForDecl[decl]
				paramBindings := paramBindingsForDecl[decl]

				declCtx := declCtxMap[decl]

				if decl.Body != nil {
					// Allocate call-site maps so body inference and resolveCallSites share them.
					callSites := make(map[int][]*type_system.FuncType)
					callSiteTypeVars := make(map[int]*type_system.TypeVarType)
					declCtx.CallSites = &callSites
					declCtx.CallSiteTypeVars = &callSiteTypeVars

					inferErrors := c.inferFuncBodyWithFuncSigType(
						declCtx, funcType, paramBindings, decl.FuncSig.Params, decl.Body, decl.FuncSig.Async)
					errors = slices.Concat(errors, inferErrors)
				}

				// Resolve deferred call sites and generalize type variables into type parameters
				c.resolveCallSites(declCtx)
				GeneralizeFuncType(funcType)

			case *ast.VarDecl:
				// Skip if this VarDecl was processed in a previous component
				// (destructuring patterns create multiple binding keys sharing the same VarDecl)
				if decl.InferredType == nil {
					continue
				}

				// TODO: if there's a type annotation, unify the initializer with it
				// Skip if the init has already been inferred (to avoid re-unification errors
				// when multiple binding keys share the same VarDecl across different components)
				if decl.Init != nil && decl.Init.InferredType() == nil {
					initType, initErrors := c.inferExpr(nsCtx, decl.Init)
					errors = slices.Concat(errors, initErrors)
					if decl.TypeAnn != nil {
						taType := decl.TypeAnn.InferredType()
						unifyErrors := c.Unify(ctx, initType, taType)
						errors = slices.Concat(errors, unifyErrors)
					} else {
						patType := decl.InferredType
						unifyErrors := c.Unify(ctx, initType, patType)
						errors = slices.Concat(errors, unifyErrors)
					}
					// Generalize VarDecl bindings that resolve to FuncType.
					// This handles cases like `val I = S(K)(K)` where the
					// initializer is a call returning a FuncType with unresolved vars.
					prunedType := type_system.Prune(decl.InferredType)
					if funcType, ok := prunedType.(*type_system.FuncType); ok {
						c.resolveCallSites(nsCtx)
						GeneralizeFuncType(funcType)
					}
				}
			case *ast.TypeDecl:
				typeAlias, declErrors := c.inferTypeDecl(nsCtx, decl)
				errors = slices.Concat(errors, declErrors)

				// Unify the type alias' inferred type with its placeholder type
				existingTypeAlias := nsCtx.Scope.GetTypeAlias(decl.Name.Name)
				unifyErrors := c.Unify(nsCtx, existingTypeAlias.Type, typeAlias.Type)
				errors = slices.Concat(errors, unifyErrors)

				// Unify the type parameters
				typeParamErrors := c.unifyTypeParams(nsCtx, existingTypeAlias.TypeParams, typeAlias.TypeParams)
				errors = slices.Concat(errors, typeParamErrors)
			case *ast.InterfaceDecl:
				interfaceAlias, declErrors := c.inferInterface(nsCtx, decl)
				errors = slices.Concat(errors, declErrors)

				// Get the existing type alias from the CURRENT namespace only.
				// The placeholder phase should have created this in the current namespace.
				existingTypeAlias := nsCtx.Scope.Namespace.Types[decl.Name.Name]
				prunedType := type_system.Prune(existingTypeAlias.Type)

				// Check if the pruned type is already an ObjectType (from a previous interface)
				if existingObjType, ok := prunedType.(*type_system.ObjectType); ok && existingObjType.Interface {
					// Merge with existing interface
					if newObjType, ok := interfaceAlias.Type.(*type_system.ObjectType); ok {
						// Note: validation is done in inferInterface, no need to duplicate it here
						// Merge the elements
						existingObjType.Elems = append(existingObjType.Elems, newObjType.Elems...)
						// Keep the Interface flag true
						existingObjType.Interface = true

						// Unify the type parameters even for merged interfaces to ensure constraint compatibility
						typeParamErrors := c.unifyTypeParams(nsCtx, existingTypeAlias.TypeParams, interfaceAlias.TypeParams)
						errors = slices.Concat(errors, typeParamErrors)

						// The merged type is already in the scope via the binding, no need to update
						continue
					}
				}

				// First interface declaration or unification needed
				unifyErrors := c.Unify(nsCtx, existingTypeAlias.Type, interfaceAlias.Type)
				errors = slices.Concat(errors, unifyErrors)

				// Unify the type parameters
				typeParamErrors := c.unifyTypeParams(nsCtx, existingTypeAlias.TypeParams, interfaceAlias.TypeParams)
				errors = slices.Concat(errors, typeParamErrors)
			case *ast.EnumDecl:
				// Skip if this enum was processed in a previous component
				// (enums have both type and value keys that may be in different components)
				if _, ok := declCtxMap[decl]; !ok {
					continue
				}

				// Get the namespace and type alias created in the placeholder phase
				ns := nsCtx.Scope.getNamespace(decl.Name.Name)
				typeAlias := nsCtx.Scope.GetTypeAlias(decl.Name.Name)
				typeParams := typeAlias.TypeParams
				declCtx := declCtxMap[decl]

				typeArgs := make([]type_system.Type, len(typeParams))
				for i := range typeParams {
					typeArgs[i] = type_system.NewTypeRefType(nil, typeParams[i].Name, nil)
				}

				variantTypes := make([]type_system.Type, len(decl.Elems))

				for i, elem := range decl.Elems {
					switch elem := elem.(type) {
					case *ast.EnumVariant:
						instanceType := type_system.NewNominalObjectType(
							&ast.NodeProvenance{Node: elem}, []type_system.ObjTypeElem{})
						instanceTypeAlias := &type_system.TypeAlias{
							Type:       instanceType,
							TypeParams: typeParams,
							Exported:   decl.Export(),
						}
						ns.Types[elem.Name.Name] = instanceTypeAlias

						params, _, paramErrors := c.inferFuncParams(declCtx, elem.Params)
						errors = slices.Concat(errors, paramErrors)

						// Build the constructor function type
						// If the enum has type parameters, the constructor should be generic
						funcType := type_system.NewFuncType(
							&ast.NodeProvenance{Node: elem},
							typeParams,
							params,
							type_system.NewTypeRefType(nil, decl.Name.Name, typeAlias, typeArgs...),
							type_system.NewNeverType(nil),
						)
						constructorElem := &type_system.ConstructorElem{Fn: funcType}

						classObjTypeElems := []type_system.ObjTypeElem{constructorElem}

						// Build [Symbol.customMatcher](subject: C) -> [T] method
						symbol := ctx.Scope.GetValue("Symbol")
						symKey := PropertyKey{
							Name:     "customMatcher",
							OptChain: false,
							span:     DEFAULT_SPAN,
						}
						customMatcher, _ := c.getMemberType(ctx, symbol.Type, symKey, AccessRead)

						// Create the SymbolKeyMap for the object type
						symbolKeyMap := make(map[int]any)

						switch customMatcher := type_system.Prune(customMatcher).(type) {
						case *type_system.UniqueSymbolType:
							self := false
							subjectPat := &type_system.IdentPat{Name: "subject"}
							// The subject type should include type arguments if the enum is generic
							subjectType := type_system.NewTypeRefType(
								nil, elem.Name.Name, instanceTypeAlias, typeArgs...)
							paramTypes := make([]type_system.Type, len(elem.Params))
							for i, param := range elem.Params {
								t, _ := c.inferTypeAnn(declCtx, param.TypeAnn)
								paramTypes[i] = t
							}
							returnType := type_system.NewTupleType(nil, paramTypes...)

							methodElem := &type_system.MethodElem{
								Name: type_system.ObjTypeKey{
									Kind: type_system.SymObjTypeKeyKind,
									Sym:  customMatcher.Value,
								},
								Fn: type_system.NewFuncType(
									nil,
									typeParams,
									[]*type_system.FuncParam{{
										Pattern: subjectPat,
										Type:    subjectType,
									}},
									returnType,
									type_system.NewNeverType(nil),
								),
								MutSelf: &self,
							}
							classObjTypeElems = append(classObjTypeElems, methodElem)

							// Store the Symbol.customMatcher expression in the SymbolKeyMap
							symbolMemberExpr := ast.NewMember(
								ast.NewIdent("Symbol", DEFAULT_SPAN),
								ast.NewIdentifier("customMatcher", DEFAULT_SPAN),
								false,
								DEFAULT_SPAN,
							)
							symbolKeyMap[customMatcher.Value] = symbolMemberExpr
						default:
							panic("Symbol.customMatcher is not a unique symbol")
						}

						provenance := &ast.NodeProvenance{Node: elem}
						classObjType := type_system.NewObjectType(provenance, classObjTypeElems)
						classObjType.SymbolKeyMap = symbolKeyMap

						ctor := &type_system.Binding{
							Source:     provenance,
							Type:       classObjType,
							Assignable: false,
							Mutable:    false,
							Exported:   decl.Export(),
						}

						ns.Values[elem.Name.Name] = ctor

						variantName := &type_system.Member{
							Left:  type_system.NewIdent(decl.Name.Name),
							Right: type_system.NewIdent(elem.Name.Name),
						}

						variantTypes[i] = &type_system.TypeRefType{
							Name:      variantName,
							TypeArgs:  typeArgs,
							TypeAlias: instanceTypeAlias,
						}
					case *ast.EnumSpread:
						panic("TODO: infer enum spreads")
					}
				}

				// Build the union type and unify with the placeholder
				enumUnionType := type_system.NewUnionType(
					&ast.NodeProvenance{Node: decl}, variantTypes...)

				unifyErrors := c.Unify(nsCtx, typeAlias.Type, enumUnionType)
				errors = slices.Concat(errors, unifyErrors)

				// Unify the type parameters
				typeParamErrors := c.unifyTypeParams(nsCtx, typeAlias.TypeParams, typeParams)
				errors = slices.Concat(errors, typeParamErrors)
			case *ast.ClassDecl:
				// Skip if this class was processed in a previous component
				// (classes have both type and value keys that may be in different components)
				if _, ok := declCtxMap[decl]; !ok {
					continue
				}

				typeAlias := nsCtx.Scope.GetTypeAlias(decl.Name.Name)
				instanceType := type_system.Prune(typeAlias.Type).(*type_system.ObjectType)

				// Get the class binding to access static methods
				classBinding := nsCtx.Scope.GetValue(decl.Name.Name)
				classType := classBinding.Type.(*type_system.ObjectType)

				// We reuse the binding that was previous created for the function
				// declaration, so that we can unify the signature with the body's
				// inferred type.
				paramBindings := paramBindingsForDecl[decl]

				declCtx := declCtxMap[decl]
				bodyCtx := declCtx.WithNewScope()

				for name, binding := range paramBindings {
					bodyCtx.Scope.setValue(name, binding)
				}

				// Process each element in the class body
				for i, bodyElem := range decl.Body {
					switch bodyElem := bodyElem.(type) {
					case *ast.FieldElem:
						var prop *type_system.PropertyElem
						var isStatic bool = bodyElem.Static

						// Find the corresponding property in either instance or class type
						var targetType *type_system.ObjectType
						if isStatic {
							targetType = classType
						} else {
							targetType = instanceType
						}

						astKey, keyErrors := c.astKeyToTypeKey(bodyCtx, bodyElem.Name)
						errors = slices.Concat(errors, keyErrors)
						if astKey != nil {
							for _, elem := range targetType.Elems {
								if propElem, ok := elem.(*type_system.PropertyElem); ok {
									if propElem.Name == *astKey {
										prop = propElem
										break
									}
								}
							}
						}

						if prop != nil {
							if bodyElem.Type != nil {
								// TODO: handle type annotations
							} else {
								if bodyElem.Value != nil {
									initType, initErrors := c.inferExpr(bodyCtx, bodyElem.Value)
									errors = slices.Concat(errors, initErrors)

									unifyErrors := c.Unify(ctx, prop.Value, initType)
									errors = slices.Concat(errors, unifyErrors)
								} else {
									var binding *type_system.Binding
									switch name := bodyElem.Name.(type) {
									case *ast.IdentExpr:
										binding = bodyCtx.Scope.GetValue(name.Name)
									case *ast.StrLit:
										binding = bodyCtx.Scope.GetValue(name.Value)
									case *ast.NumLit:
										binding = bodyCtx.Scope.GetValue(strconv.FormatFloat(name.Value, 'f', -1, 64))
									case *ast.ComputedKey:
										panic("computed keys are not supported in shorthand field declarations")
									}

									unifyErrors := c.Unify(ctx, prop.Value, binding.Type)
									errors = slices.Concat(errors, unifyErrors)
								}
							}
						}

					case *ast.MethodElem:
						var methodType *type_system.MethodElem
						var isStatic bool = bodyElem.Static

						// Find the corresponding method in either instance or class type
						var targetType *type_system.ObjectType
						if isStatic {
							targetType = classType
						} else {
							targetType = instanceType
						}

						astKey, keyErrors := c.astKeyToTypeKey(bodyCtx, bodyElem.Name)
						errors = slices.Concat(errors, keyErrors)
						if astKey != nil {
							for _, elem := range targetType.Elems {
								if methodElem, ok := elem.(*type_system.MethodElem); ok {
									if methodElem.Name == *astKey {
										methodType = methodElem
										break
									}
								}
							}
						}

						if methodType != nil {
							paramBindings := make(map[string]*type_system.Binding)

							// For instance methods, add 'self' parameter and the
							// enclosing class's constructor params. Escalier
							// allows method bodies to reference constructor
							// parameters by name (the analysis in
							// InferConstructorLifetimes already relies on this);
							// these copies make the names resolvable during type
							// checking and the rename/liveness pre-pass. Method
							// params declared with the same name are added below
							// and overwrite the constructor binding, preserving
							// the shadowing semantics covered by
							// MethodBodyShadowedParamNotCaptured.
							if !isStatic {
								maps.Copy(paramBindings, paramBindingsForDecl[decl])

								// We use the name of the class as the type here to avoid
								// a RecursiveUnificationError.
								// TODO: handle generic classes
								isMutableSelf := methodType.MutSelf != nil && *methodType.MutSelf
								var t type_system.Type = type_system.NewTypeRefType(nil, decl.Name.Name, typeAlias)
								if isMutableSelf {
									t = type_system.NewMutType(nil, t)
								}

								paramBindings["self"] = &type_system.Binding{
									Source:     &ast.NodeProvenance{Node: bodyElem},
									Type:       t,
									Assignable: false,
									Mutable:    isMutableSelf,
								}
							}

							// For static methods, no 'self' parameter is added

							// `Mutable` is the OR of the AST pattern's `Mutable` flag
							// and a `MutType` wrap on `param.Type`. Either surface
							// form (`mut p: T` or `p: mut T`) means the binding
							// sees a mut value; checking both is defense-in-depth
							// in case one source is ever set without the other.
							// Mirrors the same OR'd computation in inferPattern.
							for paramIdx, param := range methodType.Fn.Params {
								if param.Pattern == nil {
									continue
								}
								_, typeIsMut := param.Type.(*type_system.MutType)
								patIsMut := false
								if paramIdx < len(bodyElem.Fn.Params) {
									if astIdent, ok := bodyElem.Fn.Params[paramIdx].Pattern.(*ast.IdentPat); ok {
										patIsMut = astIdent.Mutable
									}
								}
								paramBindings[param.Pattern.String()] = &type_system.Binding{
									Source:     &type_system.TypeProvenance{Type: param.Type},
									Type:       param.Type,
									Assignable: false,
									Mutable:    patIsMut || typeIsMut,
								}
							}

							methodCtx := methodCtxForElem[classMethodCtxKey{decl: decl, elemIndex: i}]
							bodyErrors := c.inferFuncBodyWithFuncSigType(methodCtx, methodType.Fn, paramBindings, bodyElem.Fn.Params, bodyElem.Fn.Body, false)
							errors = slices.Concat(errors, bodyErrors)
						}

					case *ast.GetterElem:
						var getterType *type_system.GetterElem
						var isStatic bool = bodyElem.Static

						// Find the corresponding getter in either instance or class type
						var targetType *type_system.ObjectType
						if isStatic {
							targetType = classType
						} else {
							targetType = instanceType
						}

						astKey, keyErrors := c.astKeyToTypeKey(bodyCtx, bodyElem.Name)
						errors = slices.Concat(errors, keyErrors)
						if astKey != nil {
							for _, elem := range targetType.Elems {
								if getterElem, ok := elem.(*type_system.GetterElem); ok {
									if getterElem.Name == *astKey {
										getterType = getterElem
										break
									}
								}
							}
						}

						if getterType != nil {
							paramBindings := make(map[string]*type_system.Binding)

							// For instance getters, add 'self' and the enclosing
							// class's constructor params (see the matching note
							// in the MethodElem case above for rationale).
							if !isStatic {
								maps.Copy(paramBindings, paramBindingsForDecl[decl])

								// We use the name of the class as the type here to avoid
								// a RecursiveUnificationError.
								// TODO: handle generic classes
								var t type_system.Type = type_system.NewTypeRefType(nil, decl.Name.Name, typeAlias)

								paramBindings["self"] = &type_system.Binding{
									Source:     &ast.NodeProvenance{Node: bodyElem},
									Type:       t,
									Assignable: false,
									Mutable:    false, // getters don't mutate self
								}
							}

							// For static getters, no 'self' parameter is added

							// TODO(#506): once the parser rejects extra getter params,
							// this loop becomes dead code and can be deleted. Today
							// the parser silently drops anything past `self`, so
							// `getterType.Fn.Params` is always empty in practice;
							// this loop is purely defensive.
							for _, param := range getterType.Fn.Params {
								if param.Pattern != nil {
									_, isMut := param.Type.(*type_system.MutType)
									paramBindings[param.Pattern.String()] = &type_system.Binding{
										Source:     &type_system.TypeProvenance{Type: param.Type},
										Type:       param.Type,
										Assignable: false,
										Mutable:    isMut,
									}
								}
							}

							if bodyElem.Fn.Body != nil {
								bodyErrors := c.inferFuncBodyWithFuncSigType(bodyCtx, getterType.Fn, paramBindings, bodyElem.Fn.Params, bodyElem.Fn.Body, false)
								errors = slices.Concat(errors, bodyErrors)
							}
						}

					case *ast.SetterElem:
						var setterType *type_system.SetterElem
						var isStatic bool = bodyElem.Static

						// Find the corresponding setter in either instance or class type
						var targetType *type_system.ObjectType
						if isStatic {
							targetType = classType
						} else {
							targetType = instanceType
						}

						astKey, keyErrors := c.astKeyToTypeKey(bodyCtx, bodyElem.Name)
						errors = slices.Concat(errors, keyErrors)
						if astKey != nil {
							for _, elem := range targetType.Elems {
								if setterElem, ok := elem.(*type_system.SetterElem); ok {
									if setterElem.Name == *astKey {
										setterType = setterElem
										break
									}
								}
							}
						}

						if setterType != nil {
							paramBindings := make(map[string]*type_system.Binding)

							// For instance setters, add 'self' and the enclosing
							// class's constructor params (see the matching note
							// in the MethodElem case above for rationale). The
							// setter's own value param is added below and
							// shadows any constructor binding sharing its name.
							if !isStatic {
								maps.Copy(paramBindings, paramBindingsForDecl[decl])

								// We use the name of the class as the type here to avoid
								// a RecursiveUnificationError.
								// TODO: handle generic classes
								var t type_system.Type = type_system.NewTypeRefType(nil, decl.Name.Name, typeAlias)
								// Setters typically need mutable self to modify the instance
								t = type_system.NewMutType(nil, t)

								paramBindings["self"] = &type_system.Binding{
									Source:     &ast.NodeProvenance{Node: bodyElem},
									Type:       t,
									Assignable: false,
									Mutable:    true, // setters may mutate self
								}
							}

							// For static setters, no 'self' parameter is added

							// TODO(#506): once the parser enforces "exactly one value
							// param", this loop can collapse into a single binding.
							// Today the parser accepts any number of value params on
							// a setter, so the loop blesses whatever was written.
							// `isMut` is derived from param.Type (not the AST pattern
							// flag) to catch both `mut p: T` and `p: mut T` uniformly
							// — see the MethodElem case above for the full rationale.
							for _, param := range setterType.Fn.Params {
								if param.Pattern != nil {
									_, isMut := param.Type.(*type_system.MutType)
									paramBindings[param.Pattern.String()] = &type_system.Binding{
										Source:     &type_system.TypeProvenance{Type: param.Type},
										Type:       param.Type,
										Assignable: false,
										Mutable:    isMut,
									}
								}
							}

							if bodyElem.Fn.Body != nil {
								bodyErrors := c.inferFuncBodyWithFuncSigType(bodyCtx, setterType.Fn, paramBindings, bodyElem.Fn.Params, bodyElem.Fn.Body, false)
								errors = slices.Concat(errors, bodyErrors)
							}
						}

					case *ast.ConstructorElem:
						// Phase 2: type-check the constructor body. We only
						// reach this branch for the (single) constructor that
						// won out in the placeholder phase — multiple-ctor
						// errors were already reported there. Definite-
						// assignment (Phase 3) is intentionally NOT enforced
						// here; this pass just resolves names, infers
						// expression types, and records throws.
						//
						// Phase 5 (multi-constructor support): when multiple
						// in-body constructors are allowed, this branch must
						// iterate over every `ConstructorElem` in `decl.Body`
						// rather than short-circuiting on `inBodyCtorForDecl`,
						// so each ctor's body gets type-checked independently.
						ctor := inBodyCtorForDecl[decl]
						if ctor != bodyElem {
							continue
						}
						ctorFuncType := ctorFuncTypeForDecl[decl]
						if ctorFuncType == nil {
							continue
						}

						ctorBindings := make(map[string]*type_system.Binding)
						maps.Copy(ctorBindings, ctorParamBindingsForDecl[decl])

						// `self` is always `mut Self` inside a constructor,
						// regardless of the class's default mutability — the
						// body needs to assign fields. This is invisible to
						// callers (the returned instance is still immutable
						// unless the caller binds it with `mut`). For generic
						// classes the self type carries the class's own
						// type parameters as type arguments, so the body
						// sees `self : mut Foo<T, U>` rather than the bare
						// alias.
						ctorTypeArgs := make([]type_system.Type, len(typeAlias.TypeParams))
						for i := range typeAlias.TypeParams {
							ctorTypeArgs[i] = type_system.NewTypeRefType(nil, typeAlias.TypeParams[i].Name, nil)
						}
						selfRefType := type_system.NewTypeRefType(nil, decl.Name.Name, typeAlias, ctorTypeArgs...)
						ctorBindings["self"] = &type_system.Binding{
							Source:     &ast.NodeProvenance{Node: bodyElem},
							Type:       type_system.NewMutType(nil, selfRefType),
							Assignable: false,
							Mutable:    true,
						}

						if bodyElem.Fn.Body == nil {
							continue
						}

						// `inferFuncBodyWithFuncSigType` unifies the body's
						// inferred return type with `funcSigType.Return`. The
						// constructor's *callable* return type is the class
						// instance (Self with type args), but the body has no
						// `return self` statement — its statements fall off
						// the end with Void. To avoid a spurious unification
						// failure we run the body checker against a temporary
						// FuncType whose Return is Void; the real
						// `ctorFuncType` (used for overload resolution) keeps
						// `Return = Self` via `retType`. Throws inferred by
						// the body are copied back.
						astCallableParams := bodyElem.Fn.Params
						if len(astCallableParams) > 0 {
							astCallableParams = astCallableParams[1:]
						}
						bodyFuncType := type_system.NewFuncType(
							ctorFuncType.Provenance(),
							ctorFuncType.TypeParams,
							ctorFuncType.Params,
							type_system.NewVoidType(nil),
							ctorFuncType.Throws,
						)
						// Constructor-local type params (e.g. `<U>` on the
						// `constructor` itself) live in `ctorCtxForDecl`,
						// not in `bodyCtx`. Use that scope when present
						// so the body can resolve them.
						ctorBodyCtx := bodyCtx
						if cc, ok := ctorCtxForDecl[decl]; ok {
							ctorBodyCtx = cc.WithNewScope()
						}
						bodyErrors := c.inferFuncBodyWithFuncSigType(
							ctorBodyCtx, bodyFuncType, ctorBindings,
							astCallableParams, bodyElem.Fn.Body, false,
						)
						errors = slices.Concat(errors, bodyErrors)
						ctorFuncType.Throws = bodyFuncType.Throws
					}
				}
			}
		}
	}

	// Phase 8.7: for SCCs of size > 1 (mutually recursive function
	// groups), iterate lifetime inference to a fixed point. Each pass
	// can pick up additional lifetimes for functions whose peers gained
	// lifetime info on the previous pass (visible through
	// determineCheckerAliasSource). The loop terminates when a full
	// pass over the component grows no function's LifetimeParams. The
	// re-run uses ReinferLifetimes (instead of InferLifetimes) so that
	// functions whose earlier pass already inferred SOME lifetimes can
	// still acquire additional ones via newly-resolved peer signatures.
	// User-explicit lifetimes (declared in the AST) are preserved by
	// skipping the reinfer entry point for those decls.
	//
	// `maxIterations` guards against pathological non-convergence. The
	// loop is monotonic (each pass can only *add* LifetimeParams, never
	// remove them) and the worst-case chain length within an SCC is
	// `len(component) - 1` forwarding hops, so that bound is sufficient
	// to reach a fixed point. If type-checking is ever flagged as slow
	// on large SCCs, profile before tightening this.
	if len(component) > 1 {
		maxIterations := len(component) - 1
		for range maxIterations {
			grew := false
			for _, key := range sortedForDefs {
				for _, decl := range depGraph.GetDecls(key) {
					fd, ok := decl.(*ast.FuncDecl)
					if !ok || fd.Body == nil {
						continue
					}
					ft, ok := funcTypeForDecl[fd]
					if !ok || ft == nil {
						continue
					}
					if len(fd.FuncSig.LifetimeParams) > 0 {
						continue
					}
					before := len(ft.LifetimeParams)
					c.ReinferLifetimes(fd.FuncSig.Params, fd.Body, ft, fd.FuncSig.Async)
					if len(ft.LifetimeParams) > before {
						grew = true
					}
				}
			}
			if !grew {
				break
			}
		}
	}

	// Resolve any type references that were deferred during type annotation inference
	// to allow for recursive definitions between type and variable declarations.
	for _, refs := range typeRefsToUpdate {
		for _, ref := range refs {
			// Get the file-specific context if available (for file-scoped imports)
			refCtx := ctx
			if ctx.FileScopes != nil {
				// Extract SourceID from the type ref's provenance
				if nodeProv, ok := ref.Provenance().(*ast.NodeProvenance); ok {
					if node := nodeProv.Node; node != nil {
						sourceID := node.Span().SourceID
						if fileScope, ok := ctx.FileScopes[sourceID]; ok {
							// Create a context with the file scope for proper import resolution
							refCtx = ctx.WithScope(&Scope{
								Parent:    fileScope,
								Namespace: ctx.Scope.Namespace,
							})
						}
					}
				} else {
					panic(fmt.Sprintf("Expected NodeProvenance for type reference, got %T", ref.Provenance()))
				}
			}
			ref.TypeAlias = resolveQualifiedTypeAlias(refCtx, ref.Name)

			// Generate an error if the type reference couldn't be resolved
			if ref.TypeAlias == nil {
				typeName := type_system.QualIdentToString(ref.Name)
				errors = append(errors, &UnknownTypeError{
					TypeName: typeName,
					TypeRef:  ref,
				})
			}
		}
	}

	return errors
}

func getPatternNames(pattern ast.Pat) []string {
	// Collect all identifiers that are bound by the pattern.
	// This mirrors the logic of BindingVisitor but returns a slice of names.
	namesSet := make(map[string]struct{})
	var collect func(ast.Pat)
	collect = func(pat ast.Pat) {
		switch p := pat.(type) {
		case *ast.IdentPat:
			namesSet[p.Name] = struct{}{}
		case *ast.ObjectPat:
			for _, elem := range p.Elems {
				switch e := elem.(type) {
				case *ast.ObjShorthandPat:
					namesSet[e.Key.Name] = struct{}{}
				case *ast.ObjKeyValuePat:
					collect(e.Value)
				case *ast.ObjRestPat:
					collect(e.Pattern)
				}
			}
		case *ast.TuplePat:
			for _, sub := range p.Elems {
				collect(sub)
			}
		case *ast.ExtractorPat:
			for _, arg := range p.Args {
				collect(arg)
			}
		case *ast.InstancePat:
			collect(p.Object)
		case *ast.RestPat:
			collect(p.Pattern)
			// WildcardPat, LitPat, etc. do not introduce bindings.
		}
	}
	collect(pattern)

	// Convert set to slice.
	names := make([]string, 0, len(namesSet))
	for n := range namesSet {
		names = append(names, n)
	}
	// Ensure deterministic order.
	// Sorting requires the sort package.
	// (Import added at top of file.)
	sort.Strings(names)
	return names
}

func getDeclIdentifier(decl ast.Decl) string {
	switch d := decl.(type) {
	case *ast.FuncDecl:
		return d.Name.Name
	case *ast.VarDecl:
		names := getPatternNames(d.Pattern)
		return strings.Join(names, ",")
	case *ast.TypeDecl:
		return d.Name.Name
	case *ast.InterfaceDecl:
		return d.Name.Name
	case *ast.EnumDecl:
		return d.Name.Name
	default:
		return ""
	}
}

const DEBUG = false

// A module can contain declarations from mutliple source files.
// The order of the declarations doesn't matter because we compute the dependency
// graph and codegen will ensure that the declarations are emitted in the correct
// order.
// TODO: all interface declarations in a namespace to shadow previous ones.
func (c *Checker) InferModule(ctx Context, m *ast.Module) (errors []Error) {
	defer recoverTimeout(&errors)
	clear(c.expandCache) // Reset cross-call expansion cache for this inference pass
	clear(c.substCache)  // Reset substitution cache for this inference pass
	clear(c.memberCache) // Reset per-member substitution cache for this inference pass

	// Phase 1: Create file scopes and process imports for each file.
	// Import bindings are file-scoped (not visible to other files).
	fileScopes := make(map[int]*Scope)

	for _, file := range m.Files {
		// Create a file scope with the module scope as parent.
		// This allows file code to access:
		// - File-scoped imports (in fileScope.Namespace)
		// - Module-level declarations (via parent chain)
		// - Global types (via grandparent chain)
		fileNs := type_system.NewNamespace()
		fileScope := &Scope{
			Parent:    ctx.Scope, // Parent is module scope
			Namespace: fileNs,
		}
		fileScopes[file.SourceID] = fileScope

		// Process import statements for this file
		for _, importStmt := range file.Imports {
			fileCtx := ctx.WithScope(fileScope)
			importErrors := c.inferImport(fileCtx, importStmt)
			errors = append(errors, importErrors...)
		}
	}

	// Update context with file scopes and module reference
	ctx.FileScopes = fileScopes
	ctx.Module = m

	// Store file scopes on the checker so callers can access them
	c.FileScopes = fileScopes

	// Phase 1.5: Auto-load React types if JSX is detected
	// This allows JSX code to type-check without an explicit import of React types.
	if HasJSXSyntax(m) {
		var sourceDir string
		if len(m.Files) > 0 {
			sourceDir = filepath.Dir(m.Files[0].Path)
			loadErrors := c.LoadReactTypes(ctx, sourceDir)
			errors = append(errors, loadErrors...)

			// If there were errors loading React types, we can stop here since
			// JSX code won't type-check without them.
			if len(loadErrors) > 0 {
				return errors
			}
		}
	}

	// Phase 2: Build unified DepGraph for ALL declarations across all files.
	depGraph := dep_graph.BuildDepGraph(m)

	// print out all of the dependencies in depGraph for debugging
	if DEBUG {
		for _, key := range depGraph.AllBindings() {
			decls := depGraph.GetDecls(key)
			deps := depGraph.GetDeps(key)
			fmt.Fprintf(os.Stderr, "Binding: %s, Decls: [", key)
			for _, decl := range decls {
				fmt.Fprintf(os.Stderr, "%s, ", getDeclIdentifier(decl))
			}
			fmt.Fprintf(os.Stderr, "], Deps: [")
			iter := deps.Iter()
			for ok := iter.First(); ok; ok = iter.Next() {
				fmt.Fprintf(os.Stderr, "%s, ", iter.Key())
			}
			fmt.Fprintf(os.Stderr, "]\n")
		}
	}

	// Phase 3: Infer declarations using unified DepGraph.
	// Each declaration uses its file-specific scope for import resolution.
	declErrors := c.InferDepGraph(ctx, depGraph)
	errors = append(errors, declErrors...)

	// Phase 4: Process ExportAssignmentStmt declarations.
	// These are not in the dep_graph since they don't create bindings,
	// but they control what gets exported from the module.
	exportErrors := c.processModuleExportAssignments(ctx, m)
	errors = append(errors, exportErrors...)

	return errors
}

// inferTypeParams infers type parameters from AST type parameters by creating
// fresh type variables for constraints and defaults.
//
// This helper is intended for module-level type declarations such as TypeDecl,
// ClassDecl, EnumDecl, and InterfaceDecl. It only mirrors the AST type parameter
// list into a corresponding slice of *type_system.TypeParam by allocating fresh
// type variables for any constraint and default types.
//
// Note that this function:
//   - does NOT add the inferred type parameters to any scope,
//   - does NOT perform any constraint checking or error reporting, and
//   - is NOT a replacement for inferFuncTypeParams, which is responsible for
//     function-level generic parameter handling and associated diagnostics.
func (c *Checker) inferTypeParams(astTypeParams []*ast.TypeParam) []*type_system.TypeParam {
	// Sort type parameters topologically for processing (so constraints can reference earlier params)
	sortedTypeParams := ast.SortTypeParamsTopologically(astTypeParams)

	// Create a map to store type params by name
	typeParamMap := make(map[string]*type_system.TypeParam)
	for _, typeParam := range sortedTypeParams {
		var constraintType type_system.Type
		var defaultType type_system.Type
		if typeParam.Constraint != nil {
			constraintType = c.FreshVar(&ast.NodeProvenance{Node: typeParam.Constraint})
		}
		if typeParam.Default != nil {
			defaultType = c.FreshVar(&ast.NodeProvenance{Node: typeParam.Default})
		}
		typeParamMap[typeParam.Name] = &type_system.TypeParam{
			Name:       typeParam.Name,
			Constraint: constraintType,
			Default:    defaultType,
		}
	}

	// Build result in DECLARATION order (not sorted order)
	// This is critical for correct substitution when the type is instantiated
	typeParams := make([]*type_system.TypeParam, len(astTypeParams))
	for i, astParam := range astTypeParams {
		typeParams[i] = typeParamMap[astParam.Name]
	}
	return typeParams
}

// unifyTypeParams unifies the placeholder type parameters (with FreshVar constraints/defaults)
// with the fully inferred type parameters (with resolved constraint/default types).
func (c *Checker) unifyTypeParams(
	ctx Context,
	existingTypeParams []*type_system.TypeParam,
	inferredTypeParams []*type_system.TypeParam,
) []Error {
	errors := []Error{}

	for i, existingTypeParam := range existingTypeParams {
		if i >= len(inferredTypeParams) {
			break // Safety check in case of mismatched lengths
		}
		inferredTypeParam := inferredTypeParams[i]

		if existingTypeParam.Constraint != nil && inferredTypeParam.Constraint != nil {
			constraintErrors := c.Unify(ctx, existingTypeParam.Constraint, inferredTypeParam.Constraint)
			errors = slices.Concat(errors, constraintErrors)
		}
		if existingTypeParam.Default != nil && inferredTypeParam.Default != nil {
			defaultErrors := c.Unify(ctx, existingTypeParam.Default, inferredTypeParam.Default)
			errors = slices.Concat(errors, defaultErrors)
		}
	}

	return errors
}

// processModuleExportAssignments iterates over all namespaces in the module
// and processes any ExportAssignmentStmt declarations.
func (c *Checker) processModuleExportAssignments(ctx Context, m *ast.Module) []Error {
	var errors []Error

	// Iterate over all namespaces in the module
	iter := m.Namespaces.Iter()
	for ok := iter.First(); ok; ok = iter.Next() {
		nsName := iter.Key()
		ns := iter.Value()

		// Get the corresponding type_system.Namespace from the scope
		// Handle dot-qualified names by traversing nested namespaces
		var tsNs *type_system.Namespace
		if nsName == "" {
			// Root namespace
			tsNs = ctx.Scope.Namespace
		} else {
			// Traverse nested namespaces for dot-qualified names (e.g., "Foo.Bar.Baz")
			tsNs = ctx.Scope.Namespace
			for part := range strings.SplitSeq(nsName, ".") {
				var ok bool
				tsNs, ok = tsNs.GetNamespace(part)
				if !ok {
					tsNs = nil
					break
				}
			}
		}

		if tsNs == nil {
			continue
		}

		// Create a context for this namespace
		nsCtx := ctx.WithNewScopeAndNamespace(tsNs)

		// Process ExportAssignmentStmt declarations in this namespace
		for _, decl := range ns.Decls {
			if exportAssign, ok := decl.(*ast.ExportAssignmentStmt); ok {
				if err := c.processExportAssignment(exportAssign, nsCtx); err != nil {
					errors = append(errors, err)
				}
			}
		}
	}

	return errors
}

// processExportAssignment handles "export = identifier" patterns from TypeScript interop.
// If the identifier refers to a namespace, all exported members are re-exported.
// For everything else (functions, objects, primitives), a default export is created.
// Returns an error if the identifier cannot be resolved.
func (c *Checker) processExportAssignment(stmt *ast.ExportAssignmentStmt, ctx Context) Error {
	name := stmt.Name.Name

	// Check if it's a namespace (from declare namespace Foo)
	if ns := ctx.Scope.getNamespace(name); ns != nil {
		// Re-export all exported members of the namespace
		for memberName, binding := range ns.Values {
			if binding.Exported {
				ctx.Scope.setValue(memberName, binding)
			}
		}
		for typeName, typeAlias := range ns.Types {
			if typeAlias.Exported {
				ctx.Scope.SetTypeAlias(typeName, typeAlias)
			}
		}
		return nil
	}

	// For everything else, look up the value binding and create default export
	binding := ctx.Scope.GetValue(name)
	if binding == nil {
		return UnresolvedExportAssignmentError{
			Name: name,
			span: stmt.Name.Span(),
		}
	}

	// Create default export
	ctx.Scope.setValue("default", &type_system.Binding{
		Source:     &ast.NodeProvenance{Node: stmt},
		Type:       binding.Type,
		Assignable: binding.Assignable,
		Mutable:    binding.Mutable,
		Exported:   true,
	})
	return nil
}

// validateInterfaceMerge checks that when merging interface declarations,
// properties with the same name have compatible (identical) types as required by TypeScript.
// Methods are allowed to have different signatures (method overloading).
func (c *Checker) validateInterfaceMerge(
	ctx Context,
	existingInterface *type_system.ObjectType,
	newInterface *type_system.ObjectType,
	decl *ast.InterfaceDecl,
) []Error {
	errors := []Error{}

	// Build a map of property names to their types from the existing interface
	// Track whether each name is a method (allows overloading) or a property (must be identical)
	existingProps := make(map[type_system.ObjTypeKey]type_system.Type)
	existingMethods := make(map[type_system.ObjTypeKey]bool)
	for _, elem := range existingInterface.Elems {
		switch elem := elem.(type) {
		case *type_system.PropertyElem:
			existingProps[elem.Name] = elem.Value
		case *type_system.MethodElem:
			existingProps[elem.Name] = elem.Fn
			existingMethods[elem.Name] = true
		case *type_system.GetterElem:
			existingProps[elem.Name] = elem.Fn.Return
		case *type_system.SetterElem:
			existingProps[elem.Name] = elem.Fn.Params[0].Type
		}
	}

	// Check each property in the new interface against the existing interface
	for _, elem := range newInterface.Elems {
		var name type_system.ObjTypeKey
		var newType type_system.Type
		isMethod := false

		switch elem := elem.(type) {
		case *type_system.PropertyElem:
			name = elem.Name
			newType = elem.Value
		case *type_system.MethodElem:
			name = elem.Name
			newType = elem.Fn
			isMethod = true
		case *type_system.GetterElem:
			name = elem.Name
			newType = elem.Fn.Return
		case *type_system.SetterElem:
			name = elem.Name
			newType = elem.Fn.Params[0].Type
		default:
			continue
		}

		// If a property with this name already exists, check type compatibility
		if existingType, exists := existingProps[name]; exists {
			// TypeScript allows method overloading - methods with different signatures
			// are allowed and become overloads. Only check compatibility for non-methods.
			if isMethod && existingMethods[name] {
				// Both are methods - allow different signatures (method overloading)
				continue
			}

			// Properties with the same name must have identical types
			unifyErrors := c.Unify(ctx, newType, existingType)
			if len(unifyErrors) > 0 {
				// Add a more specific error for interface merging
				errors = append(errors, &InterfaceMergeError{
					InterfaceName: decl.Name.Name,
					PropertyName:  name.String(),
					ExistingType:  existingType,
					NewType:       newType,
					span:          decl.Name.Span(),
				})
			}
		}
	}

	return errors
}
