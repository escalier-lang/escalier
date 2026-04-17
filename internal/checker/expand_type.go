package checker

import (
	"fmt"
	"math"
	"slices"
	"unsafe"

	"maps"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/provenance"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// expandSeenKey identifies a specific instantiation of a type alias in a
// specific expansion context.
//
// TODO(#455): The insideKeyOf field may be unnecessary. The expandSeen
// visited set already detects cycles through TypeRefType's in-progress
// marker, which handles the nested-keyof recursion case that
// insideKeyOfTarget was designed to prevent. If insideKeyOfTarget is
// removed, this field can be removed too.
//
// Note: expandTypeRefsCount is intentionally excluded from the key. The
// expandTypeRefsCount == 0 check in ExitType returns nil before the cache
// lookup, so count=0 never consults or populates the cache. Within a single
// expansion pass the count only decreases (N → N-1 → … → 0), so a cached
// result can only be hit at the same or lower count — never at a higher count
// that would expect more expansion than what was cached.
type expandSeenKey struct {
	alias       unsafe.Pointer // TypeAlias pointer
	typeArgs    string         // typeArgKey(typeArgs)
	insideKeyOf bool           // TODO(#455): may be unnecessary
}

// expandSeen tracks type alias expansions in progress and caches completed results.
// A nil value means the expansion is in progress (re-encounter = cycle).
// A non-nil value is the cached expansion result (re-encounter = reuse).
type expandSeen map[expandSeenKey]type_system.Type

// memberCacheKey identifies a specific property on a specific instantiation of
// a generic type alias. Used by lazyMemberLookup to cache per-property
// substitution results (#461). The mode field separates getter-read results
// from setter-write results so they are never confused.
type memberCacheKey struct {
	alias    unsafe.Pointer // TypeAlias pointer
	typeArgs string         // typeArgKey(typeArgs)
	member   string         // property name
	mode     AccessMode     // read vs write (getter vs setter)
}

type memberCache map[memberCacheKey]type_system.Type

// typeVarDetector is a TypeVisitor that detects unresolved type variables.
// Only unresolved TypeVarTypes (Instance == nil) are flagged; resolved ones
// are pruned through by Accept and their instances are visited normally.
type typeVarDetector struct{ found bool }

func (d *typeVarDetector) EnterType(t type_system.Type) type_system.EnterResult {
	if tv, ok := t.(*type_system.TypeVarType); ok && tv.Instance == nil {
		d.found = true
		return type_system.EnterResult{SkipChildren: true}
	}
	return type_system.EnterResult{}
}
func (d *typeVarDetector) ExitType(t type_system.Type) type_system.Type { return t }

// containsTypeVar returns true if the type contains any unresolved type variables.
// Uses the Accept visitor to walk all nested types exhaustively.
func containsTypeVar(t type_system.Type) bool {
	d := &typeVarDetector{}
	t.Accept(d)
	return d.found
}

// isSymbolIndexKey returns true if the given MemberAccessKey is an IndexKey
// whose underlying type is a UniqueSymbolType (e.g. Symbol.iterator).
func isSymbolIndexKey(key MemberAccessKey) bool {
	indexKey, ok := key.(IndexKey)
	if !ok {
		return false
	}
	keyType := type_system.Prune(indexKey.Type)
	if mut, ok := keyType.(*type_system.MutabilityType); ok {
		keyType = mut.Type
	}
	_, isSymbol := keyType.(*type_system.UniqueSymbolType)
	return isSymbol
}

// canExpandTypeRef checks whether a TypeRefType can be expanded (i.e., it
// resolves to a non-nominal, non-self-referential type alias). Used as a
// predicate only — the actual expansion is done by ExpandType which creates
// fresh copies to prevent mutation of shared TypeAlias types.
func (c *Checker) canExpandTypeRef(ctx Context, t *type_system.TypeRefType) bool {
	typeAlias := t.TypeAlias
	if typeAlias == nil {
		typeAlias = resolveQualifiedTypeAlias(ctx, t.Name)
	}
	if typeAlias == nil {
		return false
	}

	// Don't expand type parameter placeholder aliases. These are marker
	// entries (IsTypeParam=true) created at type parameter creation sites;
	// expanding them would destroy the parameter's identity.
	if typeAlias.IsTypeParam {
		return false
	}

	expandedType := type_system.Prune(typeAlias.Type)

	// Don't expand nominal object types — nominal semantics are enforced
	// in the ObjectType vs ObjectType case in unifyMatched. Expanding here
	// would bypass nominal identity checks and can cause infinite loops
	// for self-referential class types.
	if obj, ok := expandedType.(*type_system.ObjectType); ok && obj.Nominal {
		return false
	}

	// Don't expand if following the alias chain leads back to the original
	// TypeRefType (direct or transitive cycle). Walk the chain of
	// TypeRefType → TypeAlias links with a visited set to detect cycles
	// like A→B→A.
	originName := type_system.QualIdentToString(t.Name)
	visited := map[string]bool{originName: true}
	cur := expandedType
	for {
		ref, ok := cur.(*type_system.TypeRefType)
		if !ok {
			break
		}
		refName := type_system.QualIdentToString(ref.Name)
		if visited[refName] {
			return false // cycle detected
		}
		visited[refName] = true
		// Follow the chain
		alias := ref.TypeAlias
		if alias == nil {
			alias = resolveQualifiedTypeAlias(ctx, ref.Name)
		}
		if alias == nil {
			break
		}
		cur = type_system.Prune(alias.Type)
	}

	return true
}

// TODO(#452): Extract a separate ExpandNonRefTypes helper for the count=0 case
// to make call sites self-documenting.
func (c *Checker) ExpandType(ctx Context, t type_system.Type, expandTypeRefsCount int) (type_system.Type, []Error) {
	return c.expandTypeWithConfig(ctx, t, expandTypeRefsCount, 0, make(expandSeen))
}

func (c *Checker) expandTypeWithConfig(ctx Context, t type_system.Type, expandTypeRefsCount int, insideKeyOfTarget int, seen expandSeen) (type_system.Type, []Error) {
	t = type_system.Prune(t)
	visitor := NewTypeExpansionVisitor(c, ctx, expandTypeRefsCount)
	visitor.insideKeyOfTarget = insideKeyOfTarget
	visitor.seen = seen

	result := t.Accept(visitor)
	return result, visitor.errors
}

// TypeExpansionVisitor implements TypeVisitor for expanding type references
type TypeExpansionVisitor struct {
	checker             *Checker
	ctx                 Context
	errors              []Error
	skipTypeRefsCount   int // if > 0, skip expanding TypeRefTypes
	expandTypeRefsCount int // if > 0, number of TypeRefTypes expanded, if -1 then unlimited
	insideKeyOfTarget   int // if > 0, we're expanding a keyof target, don't expand nested keyof
	seen                expandSeen
}

// NewTypeExpansionVisitor creates a new visitor for expanding type references
func NewTypeExpansionVisitor(checker *Checker, ctx Context, expandTypeRefsCount int) *TypeExpansionVisitor {
	return &TypeExpansionVisitor{
		checker:             checker,
		ctx:                 ctx,
		errors:              []Error{},
		skipTypeRefsCount:   0,
		expandTypeRefsCount: expandTypeRefsCount,
	}
}

// resolveTypeOfQualIdent resolves a qualified identifier to its type
// Handles both simple identifiers (p1) and member access (p1.x)
func (v *TypeExpansionVisitor) resolveTypeOfQualIdent(ident type_system.QualIdent, prov provenance.Provenance) type_system.Type {
	// Extract span from provenance if available
	span := ast.Span{}
	if prov != nil {
		if nodeProv, ok := prov.(*ast.NodeProvenance); ok {
			span = nodeProv.Node.Span()
		}
	}

	switch id := ident.(type) {
	case *type_system.Ident:
		// Simple identifier - look it up as a value or namespace
		if binding := v.ctx.Scope.GetValue(id.Name); binding != nil {
			return binding.Type
		} else if namespace := v.ctx.Scope.getNamespace(id.Name); namespace != nil {
			return type_system.NewNamespaceType(prov, namespace)
		} else {
			v.errors = append(v.errors, &UnknownIdentifierError{
				Ident: ast.NewIdent(id.Name, span),
				span:  span,
			})
			return type_system.NewNeverType(prov)
		}
	case *type_system.Member:
		// Member access - recursively resolve the left side, then access the property
		leftType := v.resolveTypeOfQualIdent(id.Left, prov)

		// Get the property type from the left type
		propKey := PropertyKey{
			Name:     id.Right.Name,
			OptChain: false,
			span:     span,
		}
		memberType, memberErrors := v.checker.getMemberType(v.ctx, leftType, propKey, AccessRead)
		v.errors = slices.Concat(v.errors, memberErrors)
		return memberType
	default:
		panic(fmt.Sprintf("Unknown QualIdent type: %T", ident))
	}
}

func (v *TypeExpansionVisitor) EnterType(t type_system.Type) type_system.EnterResult {
	switch t := t.(type) {
	case *type_system.FuncType:
		v.skipTypeRefsCount++ // don't expand type refs inside function types
	case *type_system.ObjectType:
		v.skipTypeRefsCount++ // don't expand type refs inside object types
	case *type_system.CondType:
		// We need to expand the CondType's extends type on entering so that
		// we can replace InferTypes in the extends type with fresh type variables
		// and then replace the corresponding TypeVarTypes in the alt and cons types
		// with those fresh type variables.  If we did this on exit, we wouldn't
		// be able to replace all the types in nested CondTypes.
		// TODO: Add a test case to ensure that infer type shadowing works and
		// fix the bug if it doesn't.

		inferSubs := v.checker.findInferTypes(t.Extends)
		groupSubs := v.checker.FindNamedGroups(t.Extends)
		extendsType := v.checker.replaceRegexGroupTypes(t.Extends, groupSubs)
		extendsType = v.checker.replaceInferTypes(extendsType, inferSubs)

		maps.Copy(inferSubs, groupSubs)

		return type_system.EnterResult{Type: type_system.NewCondType(
			t.Provenance(),
			t.Check,
			extendsType,
			SubstituteTypeParams(t.Then, inferSubs),
			// TODO: don't use substitutions for the Then type because the Checks
			// type didn't have any InferTypes in it, so we don't need to
			// replace them with fresh type variables.
			SubstituteTypeParams(t.Else, inferSubs),
		)}
	}

	return type_system.EnterResult{}
}

func (v *TypeExpansionVisitor) ExitType(t type_system.Type) type_system.Type {
	switch t := t.(type) {
	case *type_system.FuncType:
		v.skipTypeRefsCount--
	case *type_system.ObjectType:
		v.skipTypeRefsCount--
	case *type_system.CondType:
		errors := v.checker.Unify(v.ctx, t.Check, t.Extends)
		if len(errors) == 0 {
			return t.Then
		} else {
			return t.Else
		}
	case *type_system.UnionType:
		// filter out `never` types from the union
		var filteredTypes []type_system.Type
		for _, typ := range t.Types {
			if _, ok := typ.(*type_system.NeverType); !ok {
				filteredTypes = append(filteredTypes, typ)
			}
		}
		if len(filteredTypes) == len(t.Types) {
			return nil // No filtering needed, return nil to let Accept handle it
		}
		return type_system.NewUnionType(nil, filteredTypes...)
	case *type_system.IntersectionType:
		// First distribute intersection over any unions it contains
		// e.g., A & (B | C) becomes (A & B) | (A & C)
		distributed, changed := distributeIntersectionOverUnion(t)

		if !changed {
			// If no distribution occurred, re-normalize intersection after type expansion
			// Type expansion may reveal equivalent types or simplifications
			return v.checker.NormalizeIntersectionType(v.ctx, t)
		}

		// TODO: Consider moving this code into distributeIntersectionOverUnion
		// If the result of distribution is a union, recursively expand the union members
		if union, isUnion := distributed.(*type_system.UnionType); isUnion {
			// Recursively expand each member of the union to handle nested intersections
			expandedMembers := make([]type_system.Type, len(union.Types))
			for i, member := range union.Types {
				expanded, _ := v.checker.ExpandType(v.ctx, member, v.expandTypeRefsCount)
				expandedMembers[i] = expanded
			}

			// Filter out never types from the expanded union
			var filteredMembers []type_system.Type
			for _, member := range expandedMembers {
				if _, ok := member.(*type_system.NeverType); !ok {
					filteredMembers = append(filteredMembers, member)
				}
			}

			// If all members were filtered out, return never
			if len(filteredMembers) == 0 {
				return type_system.NewNeverType(nil)
			}

			// If only one member remains, return it directly
			if len(filteredMembers) == 1 {
				return filteredMembers[0]
			}

			return type_system.NewUnionType(nil, filteredMembers...)
		}

		return distributed
	case *type_system.KeyOfType:
		// TODO(#455): This guard may be redundant now that expandSeen detects
		// cycles via TypeRefType's in-progress marker. Evaluate removing it.
		if v.insideKeyOfTarget > 0 {
			return nil
		}

		// Expand keyof T by extracting the keys from the type T
		targetType := type_system.Prune(t.Type)

		// First, try to expand the target type
		// Pass insideKeyOfTarget+1 to prevent recursive keyof expansion during target expansion
		expandedTarget, _ := v.checker.expandTypeWithConfig(v.ctx, targetType, 1, v.insideKeyOfTarget+1, v.seen)
		expandedTarget = type_system.Prune(expandedTarget)

		// Unwrap MutabilityType so keyof sees the actual object type
		if mut, ok := expandedTarget.(*type_system.MutabilityType); ok {
			expandedTarget = mut.Type
		}

		switch target := expandedTarget.(type) {
		case *type_system.ObjectType:
			// Extract all keys from the object type (excluding methods)
			keys := []type_system.Type{}
			for _, elem := range target.Elems {
				switch e := elem.(type) {
				case *type_system.PropertyElem:
					// Add the property key as a literal type
					switch e.Name.Kind {
					case type_system.StrObjTypeKeyKind:
						keys = append(keys, type_system.NewStrLitType(nil, e.Name.Str))
					case type_system.NumObjTypeKeyKind:
						keys = append(keys, type_system.NewNumLitType(nil, e.Name.Num))
					case type_system.SymObjTypeKeyKind:
						keys = append(keys, type_system.NewUniqueSymbolType(nil, e.Name.Sym))
					}
				case *type_system.GetterElem:
					// Add getter names as string literal types
					switch e.Name.Kind {
					case type_system.StrObjTypeKeyKind:
						keys = append(keys, type_system.NewStrLitType(nil, e.Name.Str))
					case type_system.NumObjTypeKeyKind:
						keys = append(keys, type_system.NewNumLitType(nil, e.Name.Num))
					case type_system.SymObjTypeKeyKind:
						keys = append(keys, type_system.NewUniqueSymbolType(nil, e.Name.Sym))
					}
				case *type_system.SetterElem:
					// Add setter names as string literal types
					switch e.Name.Kind {
					case type_system.StrObjTypeKeyKind:
						keys = append(keys, type_system.NewStrLitType(nil, e.Name.Str))
					case type_system.NumObjTypeKeyKind:
						keys = append(keys, type_system.NewNumLitType(nil, e.Name.Num))
					case type_system.SymObjTypeKeyKind:
						keys = append(keys, type_system.NewUniqueSymbolType(nil, e.Name.Sym))
					}
				case *type_system.MappedElem:
					// For mapped types, return the constraint type
					// e.g., keyof {[K]: T[K] for K in Keys} -> Keys
					return e.TypeParam.Constraint
				case *type_system.RestSpreadElem:
					// For rest spread, recursively get keyof the spread type
					spreadKeys, _ := v.checker.ExpandType(v.ctx, type_system.NewKeyOfType(nil, e.Value), -1)
					keys = append(keys, spreadKeys)
				}
			}

			if len(keys) == 0 {
				return type_system.NewNeverType(nil)
			}
			if len(keys) == 1 {
				return keys[0]
			}
			return type_system.NewUnionType(nil, keys...)
		case *type_system.UnionType:
			// Distribute keyof over union types: keyof (A | B) = keyof A | keyof B
			keyofTypes := make([]type_system.Type, len(target.Types))
			for i, typ := range target.Types {
				keyofType := type_system.NewKeyOfType(nil, typ)
				expandedKeyof, _ := v.checker.ExpandType(v.ctx, keyofType, -1)
				keyofTypes[i] = expandedKeyof
			}
			return type_system.NewUnionType(nil, keyofTypes...)
		case *type_system.IntersectionType:
			// For intersection types, get the union of all keys
			// keyof (A & B) = keyof A | keyof B
			keyofTypes := make([]type_system.Type, len(target.Types))
			for i, typ := range target.Types {
				keyofType := type_system.NewKeyOfType(nil, typ)
				expandedKeyof, _ := v.checker.ExpandType(v.ctx, keyofType, -1)
				keyofTypes[i] = expandedKeyof
			}
			return type_system.NewUnionType(nil, keyofTypes...)
		case *type_system.TypeRefType:
			// Try to expand the type reference and get keyof that
			expandedRef, _ := v.checker.expandTypeRef(v.ctx, target)
			if expandedRef != target {
				keyofType := type_system.NewKeyOfType(nil, expandedRef)
				expandedKeyof, _ := v.checker.ExpandType(v.ctx, keyofType, -1)
				return expandedKeyof
			}
			// If we can't expand further, return the KeyOfType as-is
			return nil
		case *type_system.PrimType:
			// For primitive types, keyof returns never (TypeScript behavior)
			// because primitives don't have enumerable keys
			return type_system.NewNeverType(nil)
		case *type_system.TupleType:
			// For tuples, return the numeric indices as literal types plus "length"
			keys := []type_system.Type{type_system.NewStrLitType(nil, "length")}
			for i := range target.Elems {
				keys = append(keys, type_system.NewNumLitType(nil, float64(i)))
			}
			return type_system.NewUnionType(nil, keys...)
		case *type_system.NeverType:
			// keyof never = never
			return type_system.NewNeverType(nil)
		case *type_system.AnyType:
			// keyof any = string | number | symbol
			return type_system.NewUnionType(nil,
				type_system.NewStrPrimType(nil),
				type_system.NewNumPrimType(nil),
				type_system.NewSymPrimType(nil),
			)
		case *type_system.UnknownType:
			// keyof unknown = never
			return type_system.NewNeverType(nil)
		default:
			// For other types, return the KeyOfType as-is
			return nil
		}
	case *type_system.TypeRefType:
		// TODO: implement once TypeAliases have been marked as recursive.
		// `expandType` is eager so we can't expand recursive type aliases as it
		// would lead to infinite recursion.

		// Check if we've reached the maximum expansion depth
		if v.skipTypeRefsCount > 0 {
			// Return the type reference without expanding
			return nil
		}

		if v.expandTypeRefsCount == 0 {
			// Return the type reference without expanding
			return nil
		}

		// First, check if TypeAlias is already set on the TypeRefType
		typeAlias := t.TypeAlias
		if typeAlias == nil {
			typeAlias = resolveQualifiedTypeAlias(v.ctx, t.Name)
		}
		// TODO: Check if the qualifier is a type.  If it is, we can treat this
		// as a member access type.
		if typeAlias == nil {
			v.errors = append(v.errors, &UnknownTypeError{TypeName: type_system.QualIdentToString(t.Name), TypeRef: t})
			neverType := type_system.NewNeverType(nil)
			neverType.SetProvenance(&type_system.TypeProvenance{Type: t})
			return neverType
		}
		// Replace type params with type args if the type is generic
		expandedType := typeAlias.Type

		// Don't expand nominal object types
		if t, ok := expandedType.(*type_system.ObjectType); ok {
			if t.Nominal {
				return nil
			}
		}

		// Cycle detection: check if we're already expanding this alias+typeArgs.
		key := expandSeenKey{
			alias:       unsafe.Pointer(typeAlias),
			typeArgs:    typeArgKey(t.TypeArgs),
			insideKeyOf: v.insideKeyOfTarget > 0,
		}
		if cached, exists := v.seen[key]; exists {
			if cached == nil {
				// In progress — this is a cycle. Return unexpanded.
				return nil
			}
			// Completed — reuse the cached expansion.
			return cached
		}
		v.seen[key] = nil // mark as in progress

		// TODO:
		// - ensure that the number of type args matches the number of type params
		// - handle type params with defaults
		if len(typeAlias.TypeParams) > 0 && len(t.TypeArgs) > 0 {

			// TODO:
			// Handle case such as:
			// - type Foo<T> = boolean | T extends string ? T : number
			// - type Bar<T> = string & T extends string ? T : number
			// Do not perform distributions if the conditional type is the child
			// of any other type.
			switch prunedType := type_system.Prune(expandedType).(type) {
			case *type_system.CondType:
				substitutionSets, subSetErrors := v.checker.generateSubstitutionSets(v.ctx, typeAlias.TypeParams, t.TypeArgs)
				if len(subSetErrors) > 0 {
					v.errors = slices.Concat(v.errors, subSetErrors)
				}

				// If there are more than one substitution sets, distribute the
				// type arguments across the conditional type.
				if len(substitutionSets) > 1 {
					expandedTypes := make([]type_system.Type, len(substitutionSets))
					for i, substitutionSet := range substitutionSets {
						expandedTypes[i] = SubstituteTypeParams(prunedType, substitutionSet)
					}
					// Create a union type of all expanded types
					expandedType = type_system.NewUnionType(nil, expandedTypes...)
				} else {
					substitutions := createTypeParamSubstitutions(t.TypeArgs, typeAlias.TypeParams)
					expandedType = SubstituteTypeParams(prunedType, substitutions)
				}
			case *type_system.ObjectType:
				// Expand any MappedElem elements in the object type
				substitutions := createTypeParamSubstitutions(t.TypeArgs, typeAlias.TypeParams)
				objType := SubstituteTypeParams(prunedType, substitutions)
				expandedType = v.expandMappedElems(objType)
			default:
				substitutions := createTypeParamSubstitutions(t.TypeArgs, typeAlias.TypeParams)
				expandedType = SubstituteTypeParams(prunedType, substitutions)
			}
		} else if len(typeAlias.TypeParams) == 0 && len(t.TypeArgs) == 0 {
			// Expand MappedElems in ObjectTypes even if there are no type params/args
			// `{[P]: string for P in "foo" | "bar"}` should be expanded to
			// `{foo: string, bar: string}`
			if objType, ok := type_system.Prune(expandedType).(*type_system.ObjectType); ok {
				expandedType = v.expandMappedElems(objType)
			}
		}

		// Recursively expand the resolved type using the same visitor to maintain state
		// Propagate insideKeyOfTarget to prevent infinite recursion
		var result type_system.Type
		if v.expandTypeRefsCount == -1 {
			result, _ = v.checker.expandTypeWithConfig(v.ctx, expandedType, -1, v.insideKeyOfTarget, v.seen)
		} else {
			result, _ = v.checker.expandTypeWithConfig(v.ctx, expandedType, v.expandTypeRefsCount-1, v.insideKeyOfTarget, v.seen)
		}

		// Cache the expanded result for reuse
		v.seen[key] = result
		return result
	case *type_system.TemplateLitType:
		// Expand template literal types by generating all possible string combinations
		// from the cartesian product of the union types in the template
		return v.expandTemplateLitType(t)
	case *type_system.IndexType:
		// Expand index types by resolving the indexed property
		// e.g., {x: number, y: string}["x"] => number
		expandedTarget, _ := v.checker.ExpandType(v.ctx, t.Target, 1)
		expandedIndex, _ := v.checker.ExpandType(v.ctx, t.Index, 1)

		// Extract span from provenance if available
		span := ast.Span{}
		if t.Provenance() != nil {
			if nodeProv, ok := t.Provenance().(*ast.NodeProvenance); ok {
				span = nodeProv.Node.Span()
			}
		}

		// Use getMemberType to resolve the property access
		key := IndexKey{Type: expandedIndex, span: span}
		memberType, memberErrors := v.checker.getMemberType(v.ctx, expandedTarget, key, AccessRead)
		v.errors = slices.Concat(v.errors, memberErrors)
		return memberType
	case *type_system.TypeOfType:
		// Expand typeof by looking up the value and returning its type
		// Handle both simple identifiers (p1) and member access (p1.x)
		resultType := v.resolveTypeOfQualIdent(t.Ident, t.Provenance())
		return resultType
	}

	// For all other types, return nil to let Accept handle the traversal
	return nil
}

// getMemberType is a unified function for getting types from objects via property access or indexing.
// mode indicates whether the access is a read (rvalue) or write (lvalue), which affects
// getter/setter resolution: reads use getters, writes use setters.
func (c *Checker) getMemberType(ctx Context, objType type_system.Type, key MemberAccessKey, mode AccessMode) (type_system.Type, []Error) {
	errors := []Error{}

	objType = type_system.Prune(objType)

	// Fast path for TypeRefTypes: try O(1) per-member lazy lookup before
	// the expansion loop which would substitute the entire ObjectType (#461).
	// This handles both nominal types (which the expansion loop can't expand)
	// and non-nominal generic aliases (avoiding full expansion entirely).
	if tref, ok := objType.(*type_system.TypeRefType); ok {
		// Handle Array numeric index access (skip symbol keys like Symbol.iterator)
		if indexKey, ok := key.(IndexKey); ok && type_system.QualIdentToString(tref.Name) == "Array" && !isSymbolIndexKey(key) {
			unifyErrors := c.Unify(ctx, indexKey.Type, type_system.NewNumPrimType(nil))
			errors = slices.Concat(errors, unifyErrors)
			return tref.TypeArgs[0], errors
		}

		// Lazy substitution: find the property on the unsubstituted ObjectType
		// and substitute only that property's type.
		if propKey, ok := key.(PropertyKey); ok {
			if memberType, found := c.lazyMemberLookup(ctx, tref, propKey.Name, mode); found {
				return memberType, errors
			}
		}
	}

	// Check the cross-call expansion cache for TypeRefTypes with fully-concrete
	// type args. This avoids redundant ExpandType calls when the same concrete
	// TypeRefType is accessed multiple times (e.g. obj.x, obj.y on the same type). (#453)
	var cacheKey *expandSeenKey
	if tref, ok := objType.(*type_system.TypeRefType); ok && tref.TypeAlias != nil {
		concrete := true
		for _, arg := range tref.TypeArgs {
			if containsTypeVar(arg) {
				concrete = false
				break
			}
		}
		if concrete {
			// insideKeyOf is explicitly false: getMemberType never operates inside
			// a keyof context, so this cache only stores non-keyof expansions.
			// This avoids collisions with the per-pass expandSeen cache used by
			// expandTypeWithConfig, which keys on insideKeyOfTarget > 0.
			// See TODO(#455) on expandSeenKey for potential removal of insideKeyOf.
			k := expandSeenKey{
				alias:       unsafe.Pointer(tref.TypeAlias),
				typeArgs:    typeArgKey(tref.TypeArgs),
				insideKeyOf: false,
			}
			if cached, exists := c.expandCache[k]; exists {
				objType = cached
			} else {
				cacheKey = &k
			}
		}
	}

	// Repeatedly expand objType until it's either an ObjectType, NamespaceType,
	// IntersectionType, or can't be expanded any further
	for {
		// Check if we've reached a terminal type that we can directly get properties from
		// before attempting expansion (this avoids infinite recursion on NamespaceType
		// when globalThis points back to the global namespace)
		if _, ok := objType.(*type_system.ObjectType); ok {
			break
		}
		if _, ok := objType.(*type_system.NamespaceType); ok {
			break
		}
		if _, ok := objType.(*type_system.IntersectionType); ok {
			break
		}
		// Stop expanding TypeVarType so it reaches the switch below directly.
		// On the first property access a TypeVarType is constrained to an
		// open ObjectType (via t.Instance).  On subsequent accesses, Prune
		// follows t.Instance to the ObjectType, and getObjectAccess handles
		// adding new properties to open objects.
		if _, ok := objType.(*type_system.TypeVarType); ok {
			break
		}
		if _, ok := objType.(*type_system.MutabilityType); ok {
			break
		}

		expandedType, expandErrors := c.ExpandType(ctx, objType, 1)
		errors = slices.Concat(errors, expandErrors)

		// If expansion didn't change the type, we're done expanding
		if expandedType == objType {
			break
		}

		objType = expandedType
	}

	// Store the expanded result in the cross-call cache
	if cacheKey != nil {
		c.expandCache[*cacheKey] = objType
	}

	switch t := objType.(type) {
	case *type_system.MutabilityType:
		// For mutable types, get the access from the inner type
		return c.getMemberType(ctx, t.Type, key, mode)
	case *type_system.TypeRefType:
		// Fallback for TypeRefTypes that the lazy path couldn't handle
		// (index access, extends properties, symbol keys, non-ObjectType aliases).
		expandType, expandErrors := c.expandTypeRef(ctx, t)
		accessType, accessErrors := c.getMemberType(ctx, expandType, key, mode)

		errors = slices.Concat(errors, accessErrors, expandErrors)

		return accessType, errors
	case *type_system.TupleType:
		if indexKey, ok := key.(IndexKey); ok {
			var keyType type_system.Type = indexKey.Type
			if mut, ok := keyType.(*type_system.MutabilityType); ok && mut.Mutability == type_system.MutabilityUncertain {
				keyType = mut.Type
			}
			if indexLit, ok := keyType.(*type_system.LitType); ok {
				if numLit, ok := indexLit.Lit.(*type_system.NumLit); ok {
					index := int(numLit.Value)
					// Count only non-RestSpreadType elements for literal indexing
					fixedCount := 0
					for _, elem := range t.Elems {
						if _, isRest := elem.(*type_system.RestSpreadType); !isRest {
							fixedCount++
						}
					}
					if index < fixedCount {
						// Map index to actual position, skipping RestSpreadType
						pos := 0
						for _, elem := range t.Elems {
							if _, isRest := elem.(*type_system.RestSpreadType); isRest {
								continue
							}
							if pos == index {
								return elem, errors
							}
							pos++
						}
					}
					// Check if the tuple has a rest element
					for _, elem := range t.Elems {
						if rest, isRest := elem.(*type_system.RestSpreadType); isRest {
							// Index beyond fixed prefix — return the rest's element type
							restIndex := index - fixedCount
							return restElemType(rest, restIndex), errors
						}
					}
					errors = append(errors, &OutOfBoundsError{
						Index:  index,
						Length: fixedCount,
						span:   indexKey.Span(),
					})
					return type_system.NewNeverType(nil), errors
				}
			}
			errors = append(errors, &InvalidObjectKeyError{
				Key:  indexKey.Type,
				span: indexKey.Span(),
			})
			return type_system.NewNeverType(nil), errors
		}
		// If a property (method) is accessed on a tuple, delegate to Array<T>
		if _, ok := key.(PropertyKey); ok {
			elemUnion := tupleElemUnion(t)
			arrayRef := &type_system.TypeRefType{
				Name:     type_system.NewIdent("Array"),
				TypeArgs: []type_system.Type{elemUnion},
			}
			return c.getMemberType(ctx, arrayRef, key, mode)
		}
		// TupleType doesn't support other property access
		errors = append(errors, &ExpectedObjectError{Type: objType, span: key.Span()})
		return type_system.NewNeverType(nil), errors
	case *type_system.PrimType:
		// Delegate method calls on primitive types to their built‑in wrapper types
		var builtinName string
		switch t.Prim {
		case type_system.NumPrim:
			builtinName = "Number"
		case type_system.StrPrim:
			builtinName = "String"
		case type_system.BoolPrim:
			builtinName = "Boolean"
		default:
			builtinName = ""
		}
		if builtinName != "" {
			builtinRef := &type_system.TypeRefType{
				Name:     type_system.NewIdent(builtinName),
				TypeArgs: []type_system.Type{},
			}
			if _, ok := key.(PropertyKey); ok || isSymbolIndexKey(key) {
				return c.getMemberType(ctx, builtinRef, key, mode)
			}
		}
		// Not a supported primitive method call
		errors = append(errors, &ExpectedObjectError{Type: objType, span: key.Span()})
		return type_system.NewNeverType(nil), errors

	case *type_system.LitType:
		// Delegate literal method calls to the corresponding built‑in wrapper type
		var builtinName string
		switch t.Lit.(type) {
		case *type_system.NumLit:
			builtinName = "Number"
		case *type_system.StrLit:
			builtinName = "String"
		case *type_system.BoolLit:
			builtinName = "Boolean"
		default:
			builtinName = ""
		}
		if builtinName != "" {
			builtinRef := &type_system.TypeRefType{
				Name:     type_system.NewIdent(builtinName),
				TypeArgs: []type_system.Type{},
			}
			if _, ok := key.(PropertyKey); ok || isSymbolIndexKey(key) {
				return c.getMemberType(ctx, builtinRef, key, mode)
			}
		}
		// Fallback handling for other literals
		errors = append(errors, &ExpectedObjectError{Type: objType, span: key.Span()})
		return type_system.NewNeverType(nil), errors

	case *type_system.FuncType:
		// Delegate function method calls to the Function interface
		if _, ok := key.(PropertyKey); ok {
			functionRef := &type_system.TypeRefType{
				Name:     type_system.NewIdent("Function"),
				TypeArgs: []type_system.Type{},
			}
			return c.getMemberType(ctx, functionRef, key, mode)
		}
		// FuncType doesn't support index access
		errors = append(errors, &ExpectedObjectError{Type: objType, span: key.Span()})
		return type_system.NewNeverType(nil), errors

	case *type_system.ObjectType:
		return c.getObjectAccess(t, key, mode, errors)
	case *type_system.UnionType:
		return c.getUnionAccess(ctx, t, key, mode, errors)
	case *type_system.NamespaceType:
		if propKey, ok := key.(PropertyKey); ok {
			if value := t.Namespace.Values[propKey.Name]; value != nil {
				return value.Type, errors
			} else if namespace, ok := t.Namespace.GetNamespace(propKey.Name); ok {
				return type_system.NewNamespaceType(nil, namespace), errors
			} else {
				errors = append(errors, &UnknownPropertyError{
					ObjectType: objType,
					Property:   propKey.Name,
					span:       propKey.Span(),
				})
				return type_system.NewNeverType(nil), errors
			}
		}
		// NamespaceType doesn't support index access
		errors = append(errors, &ExpectedObjectError{Type: objType, span: key.Span()})
		return type_system.NewNeverType(nil), errors
	case *type_system.IntersectionType:
		return c.getIntersectionAccess(ctx, t, key, mode, errors)
	case *type_system.TypeVarType:
		// TODO(#389): Check t.Constraint before synthesizing an open object.
		// Constrained type variables (e.g. `<T: {name: string}>`) should resolve
		// properties from their constraint first. Currently this only works for
		// unannotated parameters where Constraint == nil.

		// If this TypeVar already has an ArrayConstraint, handle property access
		// by looking up the property on the Array type.
		if t.ArrayConstraint != nil {
			switch k := key.(type) {
			case PropertyKey:
				return c.getArrayConstraintPropertyAccess(ctx, t, k.Name, errors)
			case IndexKey:
				// If the index is a string literal, route to property access
				// instead of numeric index access.
				keyType := type_system.Prune(k.Type)
				if mut, ok := keyType.(*type_system.MutabilityType); ok {
					keyType = mut.Type
				}
				if litType, ok := keyType.(*type_system.LitType); ok {
					if strLit, ok := litType.Lit.(*type_system.StrLit); ok {
						return c.getArrayConstraintPropertyAccess(ctx, t, strLit.Value, errors)
					}
				}
				return c.getArrayConstraintIndexAccess(ctx, t, k, errors)
			default:
				errors = append(errors, &ExpectedObjectError{Type: objType, span: key.Span()})
				return type_system.NewNeverType(nil), errors
			}
		}

		switch k := key.(type) {
		case PropertyKey:
			// If this property is a method on Array (e.g. .push, .map, .filter),
			// create an ArrayConstraint instead of an open object. We only check
			// methods, not properties like .length which are ambiguous (also
			// exist on strings, etc.).
			if c.isArrayOnlyMethod(k.Name) {
				c.getOrCreateArrayConstraint(t)
				return c.getArrayConstraintPropertyAccess(ctx, t, k.Name, errors)
			}
			propTV, openObj := c.newOpenObjectWithProperty(k.Name, k)
			if k.OptChain {
				// Optional chaining: obj?.bar infers obj: {bar: T} | null | undefined.
				// Use the unwrapped ObjectType (not MutabilityType) since you can't
				// mutate through optional chaining — the object might be null/undefined.
				t.Instance = type_system.NewUnionType(nil, openObj.Type, type_system.NewNullType(nil), type_system.NewUndefinedType(nil))
				// The expression obj?.bar itself may produce undefined
				return type_system.NewUnionType(nil, propTV, type_system.NewUndefinedType(nil)), errors
			}
			t.Instance = openObj
			return propTV, errors
		case IndexKey:
			var keyType type_system.Type = k.Type
			if mut, ok := keyType.(*type_system.MutabilityType); ok && mut.Mutability == type_system.MutabilityUncertain {
				keyType = mut.Type
			}
			// String literal index key — treat like property access
			if indexLit, ok := keyType.(*type_system.LitType); ok {
				if strLit, ok := indexLit.Lit.(*type_system.StrLit); ok {
					propTV, openObj := c.newOpenObjectWithProperty(strLit.Value, k)
					t.Instance = openObj
					return propTV, errors
				}
			}
			// Numeric index key — create or update an ArrayConstraint instead of
			// immediately binding to Array<T>. This defers the tuple-vs-array
			// decision until closing time.
			constraint := c.getOrCreateArrayConstraint(t)
			if litIndex, ok := asNonNegativeIntLiteral(keyType); ok {
				// Record literal index with a fresh widenable type variable
				if _, exists := constraint.LiteralIndexes[litIndex]; !exists {
					elemTV := c.FreshVar(nil)
					elemTV.Widenable = true
					constraint.LiteralIndexes[litIndex] = elemTV
				}
				return constraint.LiteralIndexes[litIndex], errors
			}
			if isNumericType(keyType) {
				// Non-literal numeric type (e.g. number) — must be Array, not tuple
				constraint.HasNonLiteralIndex = true
				return constraint.ElemTypeVar, errors
			}
			// Non-literal string index — error
			errors = append(errors, &ExpectedObjectError{Type: objType, span: key.Span()})
			return type_system.NewNeverType(nil), errors
		default:
			errors = append(errors, &ExpectedObjectError{Type: objType, span: key.Span()})
			return type_system.NewNeverType(nil), errors
		}
	default:
		errors = append(errors, &ExpectedObjectError{Type: objType, span: key.Span()})
		return type_system.NewNeverType(nil), errors
	}
}

// resolveToObjectType attempts to resolve a type to an *ObjectType. It handles
// direct ObjectTypes, MutabilityType wrappers, and TypeRefTypes with aliases.
func resolveToObjectType(t type_system.Type) *type_system.ObjectType {
	resolved := type_system.Prune(t)
	if mut, ok := resolved.(*type_system.MutabilityType); ok {
		resolved = type_system.Prune(mut.Type)
	}
	if obj, ok := resolved.(*type_system.ObjectType); ok {
		return obj
	}
	if ref, ok := resolved.(*type_system.TypeRefType); ok {
		if ref.TypeAlias != nil {
			aliasType := ref.TypeAlias.Type
			if len(ref.TypeAlias.TypeParams) > 0 && len(ref.TypeArgs) > 0 {
				subs := createTypeParamSubstitutions(ref.TypeArgs, ref.TypeAlias.TypeParams)
				aliasType = SubstituteTypeParams(aliasType, subs)
			}
			return resolveToObjectType(aliasType)
		}
	}
	return nil
}

// lazyMemberLookup performs property lookup on a generic TypeRefType without
// substituting the entire ObjectType. It finds the property on the raw
// (unsubstituted) ObjectType and substitutes only that property's type.
// Results are cached per (TypeAlias, typeArgs, memberName) so repeated
// accesses to the same property are free after the first lookup.
// Returns (memberType, true) if the property was found, or (nil, false)
// to signal the caller should fall back to full expansion.
func (c *Checker) lazyMemberLookup(ctx Context, t *type_system.TypeRefType, name string, mode AccessMode) (type_system.Type, bool) {
	typeAlias := t.TypeAlias
	if typeAlias == nil {
		typeAlias = resolveQualifiedTypeAlias(ctx, t.Name)
	}
	if typeAlias == nil {
		return nil, false
	}

	aliasType := type_system.Prune(typeAlias.Type)
	objType, ok := aliasType.(*type_system.ObjectType)
	if !ok {
		return nil, false
	}

	// Check per-member cache first.
	cacheKey := memberCacheKey{
		alias:    unsafe.Pointer(typeAlias),
		typeArgs: typeArgKey(t.TypeArgs),
		member:   name,
		mode:     mode,
	}
	if cached, exists := c.memberCache[cacheKey]; exists {
		return cached, true
	}

	// Search the unsubstituted ObjectType for the property.
	targetKey := type_system.NewStrKey(name)
	var memberType type_system.Type
	for i := len(objType.Elems) - 1; i >= 0; i-- {
		switch elem := objType.Elems[i].(type) {
		case *type_system.PropertyElem:
			if elem.Name == targetKey {
				memberType = elem.Value
				if elem.Optional {
					memberType = type_system.NewUnionType(nil, memberType, type_system.NewUndefinedType(nil))
				}
			}
		case *type_system.MethodElem:
			if elem.Name == targetKey {
				memberType = elem.Fn
			}
		case *type_system.GetterElem:
			if elem.Name == targetKey && mode == AccessRead {
				memberType = elem.Fn.Return
			}
		case *type_system.SetterElem:
			if elem.Name == targetKey && mode == AccessWrite {
				memberType = elem.Fn.Params[0].Type
			}
		}
		if memberType != nil {
			break
		}
	}

	if memberType == nil {
		// Not found in direct Elems; fall back so the full expansion can
		// check Extends, RestSpreadElem, etc.
		return nil, false
	}

	// Substitute only this member's type instead of the whole ObjectType.
	if len(typeAlias.TypeParams) > 0 && len(t.TypeArgs) > 0 {
		substitutions := createTypeParamSubstitutions(t.TypeArgs, typeAlias.TypeParams)
		memberType = SubstituteTypeParams(memberType, substitutions)
	}

	c.memberCache[cacheKey] = memberType
	return memberType, true
}

// getObjectAccess handles property and index access on ObjectType.
// mode controls getter/setter resolution: AccessRead uses getters, AccessWrite uses setters.
func (c *Checker) getObjectAccess(objType *type_system.ObjectType, key MemberAccessKey, mode AccessMode, errors []Error) (type_system.Type, []Error) {
	switch k := key.(type) {
	case PropertyKey:
		// Search elements in reverse order so that later elements override
		// earlier ones. This respects JavaScript spread semantics where
		// {a: 1, ...{a: 2}} yields a=2 and {...{a: 1}, a: 2} yields a=2.
		targetKey := type_system.NewStrKey(k.Name)
		for i := len(objType.Elems) - 1; i >= 0; i-- {
			switch elem := objType.Elems[i].(type) {
			case *type_system.PropertyElem:
				if elem.Name == targetKey {
					propType := elem.Value
					if elem.Optional {
						propType = type_system.NewUnionType(nil, propType, type_system.NewUndefinedType(nil))
					}
					return propType, errors
				}
			case *type_system.MethodElem:
				if elem.Name == targetKey {
					return elem.Fn, errors
				}
			case *type_system.GetterElem:
				if elem.Name == targetKey && mode == AccessRead {
					return elem.Fn.Return, errors
				}
			case *type_system.SetterElem:
				if elem.Name == targetKey && mode == AccessWrite {
					return elem.Fn.Params[0].Type, errors
				}
			case *type_system.RestSpreadElem:
				if resolvedObj := resolveToObjectType(elem.Value); resolvedObj != nil {
					spreadType, spreadErrors := c.getObjectAccess(resolvedObj, key, mode, nil)
					if len(spreadErrors) == 0 {
						return spreadType, errors
					}
				}
			case *type_system.MappedElem:
				panic("MappedElems should have been expanded before property access")
			case *type_system.ConstructorElem:
			case *type_system.CallableElem:
				continue
			default:
				panic(fmt.Sprintf("Unknown object type element: %#v", elem))
			}
		}

		// Check the Extends field if property not found.
		//
		// TypeAlias is guaranteed to be non-nil here for valid code: the placeholder
		// phase in InferModule creates TypeAliases for all types before the definition
		// phase processes extends clauses via inferTypeAnn, which resolves TypeAlias
		// immediately. SubstituteTypeParams also preserves TypeAlias on copies. The
		// nil check below is a defensive guard for error-recovery paths where
		// inferTypeAnn couldn't resolve an unknown type name (already reported as
		// UnknownTypeError).
		for _, extendsTypeRef := range objType.Extends {
			extendsType := type_system.Type(extendsTypeRef)

			if typeRef, ok := type_system.Prune(extendsType).(*type_system.TypeRefType); ok {
				if typeRef.TypeAlias != nil {
					resolved := typeRef.TypeAlias.Type
					if len(typeRef.TypeAlias.TypeParams) > 0 && len(typeRef.TypeArgs) > 0 {
						subs := createTypeParamSubstitutions(typeRef.TypeArgs, typeRef.TypeAlias.TypeParams)
						resolved = SubstituteTypeParams(resolved, subs)
					}
					extendsType = type_system.Prune(resolved)
				}
			}

			if extendsObjType, ok := extendsType.(*type_system.ObjectType); ok {
				// Recursively check the extended type
				return c.getObjectAccess(extendsObjType, key, mode, errors)
			}

			// If the extended type cannot be resolved to an ObjectType,
			// report this instead of silently skipping it.
			errors = append(errors, &ExpectedObjectError{Type: extendsType})
		}

		// If the object is open, add the new property instead of reporting an error
		if objType.Open {
			return c.addPropertyToOpenObject(objType, k.Name, k), errors
		}

		errors = append(errors, &UnknownPropertyError{
			ObjectType: objType,
			Property:   k.Name,
			span:       k.Span(),
		})
		return type_system.NewUndefinedType(nil), errors
	case IndexKey:
		var keyType type_system.Type = k.Type
		if mut, ok := keyType.(*type_system.MutabilityType); ok && mut.Mutability == type_system.MutabilityUncertain {
			keyType = mut.Type
		}
		if indexLit, ok := keyType.(*type_system.LitType); ok {
			if strLit, ok := indexLit.Lit.(*type_system.StrLit); ok {
				// Search in reverse order for override semantics (same as PropertyKey).
				targetKey := type_system.NewStrKey(strLit.Value)
				for i := len(objType.Elems) - 1; i >= 0; i-- {
					switch elem := objType.Elems[i].(type) {
					case *type_system.PropertyElem:
						if elem.Name == targetKey {
							propType := elem.Value
							if elem.Optional {
								propType = type_system.NewUnionType(nil, propType, type_system.NewUndefinedType(nil))
							}
							return propType, errors
						}
					case *type_system.MethodElem:
						if elem.Name == targetKey {
							return elem.Fn, errors
						}
					case *type_system.GetterElem:
						if elem.Name == targetKey && mode == AccessRead {
							return elem.Fn.Return, errors
						}
					case *type_system.SetterElem:
						if elem.Name == targetKey && mode == AccessWrite {
							return elem.Fn.Params[0].Type, errors
						}
					case *type_system.RestSpreadElem:
						if resolvedObj := resolveToObjectType(elem.Value); resolvedObj != nil {
							spreadType, spreadErrors := c.getObjectAccess(resolvedObj, key, mode, nil)
							if len(spreadErrors) == 0 {
								return spreadType, errors
							}
						}
					case *type_system.MappedElem:
						panic("MappedElems should have been expanded before property access")
					case *type_system.ConstructorElem:
					case *type_system.CallableElem:
						continue
					default:
						panic(fmt.Sprintf("Unknown object type element: %#v", elem))
					}
				}
			}
		}
		// Handle unique symbol keys (e.g. Symbol.iterator).
		// Search in reverse order for override semantics, and check
		// RestSpreadElems so that symbol-keyed properties from spread
		// sources (e.g. spreading an Array which has Symbol.iterator)
		// are found.
		if symType, ok := keyType.(*type_system.UniqueSymbolType); ok {
			symKey := type_system.NewSymKey(symType.Value)
			for i := len(objType.Elems) - 1; i >= 0; i-- {
				switch elem := objType.Elems[i].(type) {
				case *type_system.PropertyElem:
					if elem.Name == symKey {
						propType := elem.Value
						if elem.Optional {
							propType = type_system.NewUnionType(nil, propType, type_system.NewUndefinedType(nil))
						}
						return propType, errors
					}
				case *type_system.MethodElem:
					if elem.Name == symKey {
						return elem.Fn, errors
					}
				case *type_system.GetterElem:
					if elem.Name == symKey && mode == AccessRead {
						return elem.Fn.Return, errors
					}
				case *type_system.SetterElem:
					if elem.Name == symKey && mode == AccessWrite {
						return elem.Fn.Params[0].Type, errors
					}
				case *type_system.RestSpreadElem:
					if resolvedObj := resolveToObjectType(elem.Value); resolvedObj != nil {
						symPropType, symPropErrors := c.getObjectAccess(resolvedObj, key, mode, nil)
						if len(symPropErrors) == 0 {
							return symPropType, errors
						}
					}
				case *type_system.MappedElem:
					panic("MappedElems should have been expanded before property access")
				case *type_system.ConstructorElem, *type_system.CallableElem:
					continue
				default:
					continue
				}
			}
		}

		// Check the Extends field if index key not found (same invariant as
		// the PropertyKey branch above — see comment there for why TypeAlias
		// is guaranteed non-nil for valid code).
		for _, extendsTypeRef := range objType.Extends {
			extendsType := type_system.Type(extendsTypeRef)
			if typeRef, ok := type_system.Prune(extendsType).(*type_system.TypeRefType); ok {
				if typeRef.TypeAlias != nil {
					resolved := typeRef.TypeAlias.Type
					if len(typeRef.TypeAlias.TypeParams) > 0 && len(typeRef.TypeArgs) > 0 {
						subs := createTypeParamSubstitutions(typeRef.TypeArgs, typeRef.TypeAlias.TypeParams)
						resolved = SubstituteTypeParams(resolved, subs)
					}
					extendsType = type_system.Prune(resolved)
				}
			}
			if extendsObjType, ok := extendsType.(*type_system.ObjectType); ok {
				// Recursively check the extended type
				return c.getObjectAccess(extendsObjType, key, mode, errors)
			}
		}

		// If the object is open and the key is a string literal, add the new property
		if objType.Open {
			if indexLit, ok := keyType.(*type_system.LitType); ok {
				if strLit, ok := indexLit.Lit.(*type_system.StrLit); ok {
					return c.addPropertyToOpenObject(objType, strLit.Value, k), errors
				}
			}
			// Numeric literal index — add a numeric-keyed property to the open object.
			// Objects can have both string and numeric keys.
			if litIndex, isNonNegInt := asNonNegativeIntLiteral(keyType); isNonNegInt {
				return c.addNumericPropertyToOpenObject(objType, float64(litIndex), k), errors
			}
		}

		errors = append(errors, &InvalidObjectKeyError{
			Key:  keyType,
			span: k.Span(),
		})
		return type_system.NewUndefinedType(nil), errors
	default:
		errors = append(errors, &ExpectedObjectError{Type: objType})
		return type_system.NewUndefinedType(nil), errors
	}
}

// newOpenObjectWithProperty creates a new open ObjectType with a single property
// and a rest-spread element, returning the property's type variable and the object.
// accessKey records the member access that triggered inference of this property.
func (c *Checker) newOpenObjectWithProperty(name string, accessKey MemberAccessKey) (*type_system.TypeVarType, *type_system.MutabilityType) {
	propTV := c.FreshVar(nil)
	propTV.Widenable = true
	rowTV := c.FreshVar(nil)
	prop := type_system.NewPropertyElem(type_system.NewStrKey(name), propTV)
	prop.Provenance = &MemberAccessKeyProvenance{Key: accessKey}
	openObj := type_system.NewObjectType(nil, []type_system.ObjTypeElem{
		prop,
		type_system.NewRestSpreadElem(rowTV),
	})
	openObj.Open = true
	return propTV, &type_system.MutabilityType{
		Type:       openObj,
		Mutability: type_system.MutabilityUncertain,
	}
}

// addPropertyToOpenObject appends a new widenable property to an existing open
// ObjectType and returns the property's type variable.
// accessKey records the member access that triggered inference of this property.
func (c *Checker) addPropertyToOpenObject(objType *type_system.ObjectType, name string, accessKey MemberAccessKey) *type_system.TypeVarType {
	propTV := c.FreshVar(nil)
	propTV.Widenable = true
	prop := type_system.NewPropertyElem(type_system.NewStrKey(name), propTV)
	prop.Provenance = &MemberAccessKeyProvenance{Key: accessKey}
	objType.Elems = append(objType.Elems, prop)
	return propTV
}

// addNumericPropertyToOpenObject appends a new widenable property with a numeric
// key to an existing open ObjectType and returns the property's type variable.
func (c *Checker) addNumericPropertyToOpenObject(objType *type_system.ObjectType, index float64, accessKey MemberAccessKey) *type_system.TypeVarType {
	propTV := c.FreshVar(nil)
	propTV.Widenable = true
	prop := type_system.NewPropertyElem(type_system.NewNumKey(index), propTV)
	prop.Provenance = &MemberAccessKeyProvenance{Key: accessKey}
	objType.Elems = append(objType.Elems, prop)
	return propTV
}

// markPropertyWritten finds a property by name on an open ObjectType and sets
// its Written flag. It handles both bare ObjectType and MutabilityType-wrapped
// ObjectType. Returns true if the property was found and marked.
func markPropertyWritten(prunedType type_system.Type, propName string) bool {
	var openObj *type_system.ObjectType
	switch t := prunedType.(type) {
	case *type_system.ObjectType:
		if t.Open {
			openObj = t
		}
	case *type_system.MutabilityType:
		if obj, ok := t.Type.(*type_system.ObjectType); ok && obj.Open {
			openObj = obj
		}
	}
	if openObj == nil {
		return false
	}
	for _, elem := range openObj.Elems {
		if propElem, ok := elem.(*type_system.PropertyElem); ok {
			if propElem.Name == type_system.NewStrKey(propName) {
				propElem.Written = true
				return true
			}
		}
	}
	return false
}

// isNumericType returns true if the type represents a numeric type.
// This includes the number primitive and numeric literal types that are not
// valid non-negative integer tuple indices (e.g. floats, negatives).
func isNumericType(t type_system.Type) bool {
	t = type_system.Prune(t)
	if numPrim, ok := t.(*type_system.PrimType); ok && numPrim.Prim == type_system.NumPrim {
		return true
	}
	if litType, ok := t.(*type_system.LitType); ok {
		if _, ok := litType.Lit.(*type_system.NumLit); ok {
			// If it's a valid non-negative int literal, it's handled by
			// asNonNegativeIntLiteral, not here. Only treat non-integer
			// or negative numeric literals as generic numeric types.
			if _, isIndex := asNonNegativeIntLiteral(t); !isIndex {
				return true
			}
		}
	}
	return false
}

// asNonNegativeIntLiteral extracts a non-negative integer from a numeric literal type.
// Returns the integer value and true if successful, or 0 and false otherwise.
// Rejects integers exceeding maxTupleIndex to prevent huge tuple allocations in
// resolveArrayConstraint.
// TODO(#402): Make maxTupleIndex configurable via checker options.
const maxTupleIndex = 20

func asNonNegativeIntLiteral(t type_system.Type) (int, bool) {
	t = type_system.Prune(t)
	if litType, ok := t.(*type_system.LitType); ok {
		if numLit, ok := litType.Lit.(*type_system.NumLit); ok {
			val := numLit.Value
			if val >= 0 && val == math.Floor(val) && int(val) <= maxTupleIndex {
				return int(val), true
			}
		}
	}
	return 0, false
}

// getOrCreateArrayConstraint returns the existing ArrayConstraint on a TypeVarType,
// or creates and attaches a new one with a fresh element type variable.
func (c *Checker) getOrCreateArrayConstraint(t *type_system.TypeVarType) *type_system.ArrayConstraint {
	if t.ArrayConstraint != nil {
		return t.ArrayConstraint
	}
	elemTV := c.FreshVar(nil)
	elemTV.Widenable = true
	t.ArrayConstraint = &type_system.ArrayConstraint{
		LiteralIndexes: make(map[int]type_system.Type),
		ElemTypeVar:    elemTV,
	}
	return t.ArrayConstraint
}

// getArrayConstraintPropertyAccess handles property/method access on a TypeVarType
// that already has an ArrayConstraint. It looks up the property on the Array type
// definition to determine if it's a read-only or mutating method.
func (c *Checker) getArrayConstraintPropertyAccess(ctx Context, t *type_system.TypeVarType, propName string, errors []Error) (type_system.Type, []Error) {
	constraint := t.ArrayConstraint

	// Special-case .length — always available on both tuples and arrays
	if propName == "length" {
		return type_system.NewNumPrimType(nil), errors
	}

	// Create a fresh elem var for this call site so that multiple calls
	// with different types don't conflict — they'll be unified into a
	// union during resolveArrayConstraint.
	arrayAlias := c.GlobalScope.Namespace.Types["Array"]
	if arrayAlias == nil {
		errors = append(errors, &ExpectedObjectError{Type: t, span: ast.Span{}})
		return type_system.NewNeverType(nil), errors
	}

	freshElem := c.FreshVar(nil)
	freshElem.Widenable = true
	constraint.MethodElemVars = append(constraint.MethodElemVars, freshElem)

	tempArrayType := type_system.NewTypeRefType(nil, "Array", arrayAlias, freshElem)
	resultType, accessErrors := c.getMemberType(ctx, tempArrayType, PropertyKey{Name: propName}, AccessRead)
	errors = slices.Concat(errors, accessErrors)

	// Classify the method as mutating or read-only.
	if c.isArrayMutatingMethod(propName) {
		constraint.HasMutatingMethod = true
	} else {
		constraint.HasReadOnlyMethod = true
	}

	return resultType, errors
}

// getArrayConstraintIndexAccess handles numeric index access on a TypeVarType
// that already has an ArrayConstraint.
func (c *Checker) getArrayConstraintIndexAccess(_ Context, t *type_system.TypeVarType, k IndexKey, errors []Error) (type_system.Type, []Error) {
	constraint := t.ArrayConstraint
	var keyType type_system.Type = k.Type
	if mut, ok := keyType.(*type_system.MutabilityType); ok && mut.Mutability == type_system.MutabilityUncertain {
		keyType = mut.Type
	}

	if litIndex, ok := asNonNegativeIntLiteral(keyType); ok {
		if _, exists := constraint.LiteralIndexes[litIndex]; !exists {
			elemTV := c.FreshVar(nil)
			elemTV.Widenable = true
			constraint.LiteralIndexes[litIndex] = elemTV
		}
		return constraint.LiteralIndexes[litIndex], errors
	}
	if isNumericType(keyType) {
		constraint.HasNonLiteralIndex = true
		return constraint.ElemTypeVar, errors
	}
	errors = append(errors, &ExpectedObjectError{Type: t, span: k.Span()})
	return type_system.NewNeverType(nil), errors
}

// isArrayOnlyMethod returns true if the given name is a method (not a property)
// on the Array type. This is used to decide whether accessing a property on a
// fresh TypeVar should create an ArrayConstraint. Only methods qualify because
// properties like .length are ambiguous (also exist on strings, etc.).
func (c *Checker) isArrayOnlyMethod(propName string) bool {
	arrayAlias := c.GlobalScope.Namespace.Types["Array"]
	if arrayAlias == nil {
		return false
	}
	arrayType := type_system.Prune(arrayAlias.Type)
	objType, ok := arrayType.(*type_system.ObjectType)
	if !ok {
		return false
	}
	key := type_system.NewStrKey(propName)
	for _, elem := range objType.Elems {
		if method, ok := elem.(*type_system.MethodElem); ok && method.Name == key {
			return true
		}
	}
	return false
}

// isArrayMutatingMethod returns true if the given method name is a mutating method
// on Array (i.e., it exists on mut Array but not on immutable Array).
func (c *Checker) isArrayMutatingMethod(methodName string) bool {
	arrayAlias := c.GlobalScope.Namespace.Types["Array"]
	if arrayAlias == nil {
		return false
	}
	arrayType := type_system.Prune(arrayAlias.Type)
	objType, ok := arrayType.(*type_system.ObjectType)
	if !ok {
		return false
	}
	for _, elem := range objType.Elems {
		if method, ok := elem.(*type_system.MethodElem); ok {
			if method.Name == type_system.NewStrKey(methodName) {
				if method.MutSelf != nil && *method.MutSelf {
					return true
				}
				return false
			}
		}
	}
	return false
}

// getUnionAccess handles property and index access on UnionType
func (c *Checker) getUnionAccess(ctx Context, unionType *type_system.UnionType, key MemberAccessKey, mode AccessMode, errors []Error) (type_system.Type, []Error) {
	propKey, isPropertyKey := key.(PropertyKey)

	definedElems := c.getDefinedElems(unionType)

	undefinedCount := len(unionType.Types) - len(definedElems)

	// If there are no defined elements (only null/undefined), we can't access properties
	if len(definedElems) == 0 {
		errors = append(errors, &ExpectedObjectError{Type: unionType, span: key.Span()})
		return type_system.NewUndefinedType(nil), errors
	}

	if len(definedElems) == 1 {
		if undefinedCount == 0 {
			return c.getMemberType(ctx, definedElems[0], key, mode)
		}

		if undefinedCount > 0 && isPropertyKey && !propKey.OptChain {
			errors = append(errors, &ExpectedObjectError{Type: unionType, span: key.Span()})
			return type_system.NewUndefinedType(nil), errors
		}

		pType, pErrors := c.getMemberType(ctx, definedElems[0], key, mode)
		errors = slices.Concat(errors, pErrors)
		// Only add undefined if the inner result doesn't already contain it
		// (e.g. from a nested optional chain on a TypeVarType).
		if !typeContainsUndefined(pType) {
			pType = type_system.NewUnionType(nil, pType, type_system.NewUndefinedType(nil))
		}
		return pType, errors
	}

	if len(definedElems) > 1 {
		// Get the member type from each element in the union and combine them
		memberTypes := []type_system.Type{}
		for _, elem := range definedElems {
			memberType, memberErrors := c.getMemberType(ctx, elem, key, mode)
			errors = slices.Concat(errors, memberErrors)
			memberTypes = append(memberTypes, memberType)
		}

		// If there are undefined elements and we're accessing without optional chaining,
		// we need to report an error
		if undefinedCount > 0 && isPropertyKey && !propKey.OptChain {
			errors = append(errors, &ExpectedObjectError{Type: unionType, span: key.Span()})
		}

		// Create a union of all member types
		resultType := type_system.NewUnionType(nil, memberTypes...)

		// If there are undefined elements, add undefined to the union
		// (unless the result already contains it from a nested optional chain).
		if undefinedCount > 0 && !typeContainsUndefined(resultType) {
			resultType = type_system.NewUnionType(nil, resultType, type_system.NewUndefinedType(nil))
		}

		return resultType, errors
	}

	return type_system.NewNeverType(nil), errors
}

// getIntersectionAccess handles property and index access on IntersectionType
func (c *Checker) getIntersectionAccess(ctx Context, intersectionType *type_system.IntersectionType, key MemberAccessKey, mode AccessMode, errors []Error) (type_system.Type, []Error) {
	// For an intersection A & B, member access should:
	// 1. If all parts are object types, merge their properties (the result is the intersection of matching property types)
	// 2. Otherwise, try to access from each part and use the first successful one

	// Separate object types from non-object types
	objectTypes := []*type_system.ObjectType{}
	funcTypes := []*type_system.FuncType{}

	for _, part := range intersectionType.Types {
		part = type_system.Prune(part)
		// Unwrap MutabilityType so inferred open objects (wrapped in mut?)
		// are classified as object parts of the intersection.
		if mut, ok := part.(*type_system.MutabilityType); ok {
			part = mut.Type
		}
		if objType, ok := part.(*type_system.ObjectType); ok {
			objectTypes = append(objectTypes, objType)
		} else if funcType, ok := part.(*type_system.FuncType); ok {
			funcTypes = append(funcTypes, funcType)
		}
	}

	// If all parts are object types, merge their properties
	if len(objectTypes) == len(intersectionType.Types) {
		memberTypes := []type_system.Type{}
		foundAny := false

		for _, objType := range objectTypes {
			memberType, memberErrors := c.getObjectAccess(objType, key, mode, nil)
			// Only include results from object types that have this property
			if len(memberErrors) == 0 {
				memberTypes = append(memberTypes, memberType)
				foundAny = true
			}
		}

		if !foundAny {
			// Property doesn't exist in any part of the intersection
			if propKey, ok := key.(PropertyKey); ok {
				errors = append(errors, &UnknownPropertyError{
					ObjectType: intersectionType,
					Property:   propKey.Name,
					span:       propKey.Span(),
				})
			} else {
				indexKey := key.(IndexKey)
				errors = append(errors, &InvalidObjectKeyError{
					Key:  indexKey.Type,
					span: indexKey.Span(),
				})
			}
			return type_system.NewNeverType(nil), errors
		}

		// The result type is the intersection of all matching property types
		// This ensures that the property type satisfies all parts of the intersection
		if len(memberTypes) == 1 {
			return memberTypes[0], errors
		}
		return type_system.NewIntersectionType(nil, memberTypes...), errors
	}

	// For mixed cases (e.g., branded primitives: string & {__brand: "email"}, or function & {metadata: string})
	// First, collect all properties from object type parts
	memberTypesFromObjects := []type_system.Type{}
	for _, objType := range objectTypes {
		memberType, memberErrors := c.getObjectAccess(objType, key, mode, nil)
		if len(memberErrors) == 0 {
			memberTypesFromObjects = append(memberTypesFromObjects, memberType)
		}
	}

	// If we found the property in multiple object types, intersect them
	if len(memberTypesFromObjects) > 1 {
		return type_system.NewIntersectionType(nil, memberTypesFromObjects...), errors
	}

	// If we found the property in exactly one object type, return it
	if len(memberTypesFromObjects) == 1 {
		return memberTypesFromObjects[0], errors
	}

	// If not found in object types, try other parts (primitives, functions, etc.)
	for _, part := range intersectionType.Types {
		part = type_system.Prune(part)
		// Skip object types (including MutabilityType-wrapped) since we already checked them
		unwrapped := part
		if mut, ok := unwrapped.(*type_system.MutabilityType); ok {
			unwrapped = mut.Type
		}
		if _, ok := unwrapped.(*type_system.ObjectType); ok {
			continue
		}
		memberType, memberErrors := c.getMemberType(ctx, part, key, mode)
		if len(memberErrors) == 0 {
			return memberType, errors
		}
	}

	// If no part has this property, report error
	if propKey, ok := key.(PropertyKey); ok {
		errors = append(errors, &UnknownPropertyError{
			ObjectType: intersectionType,
			Property:   propKey.Name,
			span:       propKey.Span(),
		})
	} else {
		indexKey := key.(IndexKey)
		errors = append(errors, &InvalidObjectKeyError{
			Key:  indexKey.Type,
			span: indexKey.Span(),
		})
	}
	return type_system.NewNeverType(nil), errors
}

// expandMappedElems expands MappedElem elements in an ObjectType into concrete properties.
// For example, {[P in "foo" | "bar"]: string} becomes {foo: string, bar: string}.
//
// TODO(#456): This function only handles finite, enumerable key sets (string/number
// literals, symbols). It panics when the constraint is a primitive type like `string`
// or `number` (e.g. Record<string, T> which expands to {[P in string]: T}). Supporting
// infinite key types requires adding an index signature representation to the type system.
func (v *TypeExpansionVisitor) expandMappedElems(objType *type_system.ObjectType) *type_system.ObjectType {
	// Check if there are any MappedElem elements to expand
	hasMappedElems := false
	for _, elem := range objType.Elems {
		if _, ok := elem.(*type_system.MappedElem); ok {
			hasMappedElems = true
			break
		}
	}

	if !hasMappedElems {
		return objType
	}

	// Expand mapped elements into concrete properties
	expandedElems := []type_system.ObjTypeElem{}
	for _, elem := range objType.Elems {
		if mappedElem, ok := elem.(*type_system.MappedElem); ok {
			// Expand the constraint to get all possible keys
			expandedConstraint, _ := v.checker.ExpandType(v.ctx, mappedElem.TypeParam.Constraint, -1)

			// Extract keys from the constraint
			var keys []type_system.Type
			switch constraint := type_system.Prune(expandedConstraint).(type) {
			case *type_system.UnionType:
				keys = constraint.Types
			default:
				keys = []type_system.Type{constraint}
			}

			// For each key in the constraint, create a property
			for _, keyType := range keys {
				keyType = type_system.Prune(keyType)

				// Substitute the type parameter with the key type
				keySubs := map[string]type_system.Type{
					mappedElem.TypeParam.Name: keyType,
				}

				// Apply filter if Check and Extends are present
				if mappedElem.Check != nil && mappedElem.Extends != nil {
					// Substitute the type parameter in both Check and Extends
					checkType := SubstituteTypeParams(mappedElem.Check, keySubs)
					extendsType := SubstituteTypeParams(mappedElem.Extends, keySubs)

					// Expand both types
					checkType, _ = v.checker.ExpandType(v.ctx, checkType, -1)
					extendsType, _ = v.checker.ExpandType(v.ctx, extendsType, -1)

					// Check if checkType extends extendsType
					errors := v.checker.Unify(v.ctx, checkType, extendsType)
					if len(errors) > 0 {
						// Skip this property if it doesn't satisfy the filter
						continue
					}
				}

				// Determine the property key
				var propKey type_system.ObjTypeKey
				if mappedElem.Name != nil {
					// If a Name is provided, use it to compute the property key
					// Example: {[`prefix_${K}`]: T[K] for K in keyof T}
					propKeyType := SubstituteTypeParams(mappedElem.Name, keySubs)

					// Expand the property key to resolve template literals
					propKeyType, _ = v.checker.ExpandType(v.ctx, propKeyType, -1)
					propKeyType = type_system.Prune(propKeyType)

					// Convert the expanded key type to an ObjTypeKey
					switch kt := propKeyType.(type) {
					case *type_system.LitType:
						switch lit := kt.Lit.(type) {
						case *type_system.StrLit:
							propKey = type_system.NewStrKey(lit.Value)
						case *type_system.NumLit:
							propKey = type_system.NewNumKey(lit.Value)
						default:
							// Skip non-string/number keys
							continue
						}
					case *type_system.UniqueSymbolType:
						propKey = type_system.NewSymKey(kt.Value)
					default:
						// Skip non-literal keys
						continue
					}
				} else {
					// Otherwise, use the constraint key directly
					switch kt := keyType.(type) {
					case *type_system.LitType:
						switch lit := kt.Lit.(type) {
						case *type_system.StrLit:
							propKey = type_system.NewStrKey(lit.Value)
						case *type_system.NumLit:
							propKey = type_system.NewNumKey(lit.Value)
						default:
							panic("Invalid property key type in mapped element")
						}
					case *type_system.UniqueSymbolType:
						propKey = type_system.NewSymKey(kt.Value)
					default:
						panic("Invalid property key type in mapped element")
					}
				}

				propValue := SubstituteTypeParams(mappedElem.Value, keySubs)

				// Expand the property value to resolve any index types such as `T[K]`
				// This is necessary to get the actual type including | undefined for optional properties
				propValue, _ = v.checker.ExpandType(v.ctx, propValue, 1)

				// Create property element
				propElem := &type_system.PropertyElem{
					Name:     propKey,
					Value:    propValue,
					Optional: false,
					Readonly: false,
				}

				// Handle optional modifier
				if mappedElem.Optional != nil {
					switch *mappedElem.Optional {
					case type_system.MMAdd:
						propElem.Optional = true
					case type_system.MMRemove:
						propElem.Optional = false
						// When removing the optional modifier, also remove undefined from the value type
						propElem.Value = removeUndefinedFromType(propElem.Value)
					}
				}

				// Handle readonly modifier
				if mappedElem.Readonly != nil {
					switch *mappedElem.Readonly {
					case type_system.MMAdd:
						propElem.Readonly = true
					case type_system.MMRemove:
						propElem.Readonly = false
					}
				}

				expandedElems = append(expandedElems, propElem)
			}
		} else {
			// Keep non-mapped elements as-is
			expandedElems = append(expandedElems, elem)
		}
	}

	return type_system.NewObjectType(objType.Provenance(), expandedElems)
}

func (c *Checker) expandTypeRef(ctx Context, t *type_system.TypeRefType) (type_system.Type, []Error) {
	// Resolve the type alias
	typeAlias := resolveQualifiedTypeAlias(ctx, t.Name)
	if typeAlias == nil {
		return type_system.NewNeverType(nil), []Error{&UnknownTypeError{TypeName: type_system.QualIdentToString(t.Name), TypeRef: t}}
	}

	// Expand the type alias
	expandedType := typeAlias.Type

	// Handle type parameter substitution if the type is generic
	if len(typeAlias.TypeParams) > 0 && len(t.TypeArgs) > 0 {
		// Check substitution cache to avoid redundant SubstituteTypeParams calls
		// for the same type alias + type args combination (#461).
		key := expandSeenKey{
			alias:       unsafe.Pointer(typeAlias),
			typeArgs:    typeArgKey(t.TypeArgs),
			insideKeyOf: false,
		}
		if cached, exists := c.substCache[key]; exists {
			return cached, []Error{}
		}
		substitutions := createTypeParamSubstitutions(t.TypeArgs, typeAlias.TypeParams)
		expandedType = SubstituteTypeParams(typeAlias.Type, substitutions)
		c.substCache[key] = expandedType
	}

	return expandedType, []Error{}
}

// expandTemplateLitType expands a template literal type by generating all possible
// string combinations from the cartesian product of union types in the template.
// Example: `${0 | 1},${0 | 1}` => "0,0" | "0,1" | "1,0" | "1,1"
func (v *TypeExpansionVisitor) expandTemplateLitType(t *type_system.TemplateLitType) type_system.Type {
	// Extract the members of each type in the template
	// If a type is a union, we get all its members
	// If it's a literal, we get just that literal
	typeOptions := make([][]type_system.Type, len(t.Types))

	for i, t := range t.Types {
		t = type_system.Prune(t)
		t, _ = v.checker.ExpandType(v.ctx, t, -1) // fully expand nested type refs

		if unionType, ok := t.(*type_system.UnionType); ok {
			typeOptions[i] = unionType.Types
		} else {
			typeOptions[i] = []type_system.Type{t}
		}
	}

	// Generate cartesian product of all type options
	combinations := v.cartesianProduct(typeOptions)

	// Convert each combination into a string literal type
	resultTypes := make([]type_system.Type, 0, len(combinations))

	for _, combo := range combinations {
		newQuasis := []*type_system.Quasi{}
		newTypes := []type_system.Type{}
		currentQuasi := ""

		for i, quasi := range t.Quasis {
			currentQuasi += quasi.Value

			if i < len(combo) {
				// Check if this is a literal type that should be concatenated to currentQuasi
				if litType, ok := combo[i].(*type_system.LitType); ok {
					switch lit := litType.Lit.(type) {
					case *type_system.StrLit:
						currentQuasi += lit.Value
					case *type_system.NumLit:
						currentQuasi += fmt.Sprintf("%v", lit.Value)
					case *type_system.BoolLit:
						currentQuasi += fmt.Sprintf("%v", lit.Value)
					case *type_system.BigIntLit:
						currentQuasi += lit.Value.String()
					default:
						// Other literal types: append currentQuasi and add the type
						newQuasis = append(newQuasis, &type_system.Quasi{Value: currentQuasi})
						currentQuasi = ""
						newTypes = append(newTypes, combo[i])
					}
				} else {
					// Non-literal types: append currentQuasi and add the type
					newQuasis = append(newQuasis, &type_system.Quasi{Value: currentQuasi})
					currentQuasi = ""
					newTypes = append(newTypes, combo[i])
				}
			}
		}

		// Append the final currentQuasi (this is the tail)
		newQuasis = append(newQuasis, &type_system.Quasi{Value: currentQuasi})

		// If we have no types (all were literals), convert to a string literal
		if len(newTypes) == 0 {
			resultTypes = append(resultTypes, type_system.NewStrLitType(t.Provenance(), newQuasis[0].Value))
		} else {
			// Otherwise, create a new template literal type
			newTemplateLitType := &type_system.TemplateLitType{
				Quasis: newQuasis,
				Types:  newTypes,
			}
			newTemplateLitType.SetProvenance(t.Provenance())
			resultTypes = append(resultTypes, newTemplateLitType)
		}
	}

	// Return a union of all possible string literals
	return type_system.NewUnionType(t.Provenance(), resultTypes...)
}

// NormalizeIntersectionType performs deep normalization of an intersection type
// after type inference and expansion. This handles cases that NewIntersectionType
// cannot handle because types haven't been fully resolved yet, such as:
// - Type aliases that resolve to the same underlying type
// - Type variables after substitution
// - Type references that point to the same concrete type
func (c *Checker) NormalizeIntersectionType(ctx Context, t *type_system.IntersectionType) type_system.Type {
	// Step 1: Prune and expand all types to resolve type variables and type aliases
	expanded := make([]type_system.Type, len(t.Types))
	for i, typ := range t.Types {
		// Prune to resolve type variables
		typ = type_system.Prune(typ)

		// Expand type aliases to their underlying types
		// Use depth 1 to expand one level of type aliases
		if _, ok := typ.(*type_system.TypeRefType); ok {
			expandedType, _ := c.ExpandType(ctx, typ, 1)
			expanded[i] = expandedType
		} else {
			expanded[i] = typ
		}
	}

	// Step 2: Use NewIntersectionType to apply basic normalization
	// This handles flattening, duplicates, never/any/unknown, primitives, mutability
	result := type_system.NewIntersectionType(t.Provenance(), expanded...)

	// Step 3: If still an intersection after normalization, check for further simplifications
	if inter, ok := result.(*type_system.IntersectionType); ok {
		// Check if all types in the intersection are ObjectTypes - if so, merge them
		allObjects := true
		for _, typ := range inter.Types {
			if _, ok := typ.(*type_system.ObjectType); !ok {
				allObjects = false
				break
			}
		}

		if allObjects && len(inter.Types) > 1 {
			// Merge all object types into a single object type
			return c.mergeObjectTypes(inter.Provenance(), inter.Types)
		}
	}

	return result
}

// mergeObjectTypes merges multiple object types into a single object type
// by combining their elements. When properties have the same name, their types
// are intersected (e.g., {x: string} & {x: number} becomes {x: string & number}).
func (c *Checker) mergeObjectTypes(prov provenance.Provenance, types []type_system.Type) type_system.Type {
	mergedElems := []type_system.ObjTypeElem{}
	propMap := make(map[string]int) // Maps property key to index in mergedElems

	// Iterate through object types and collect their elements
	for _, typ := range types {
		if obj, ok := typ.(*type_system.ObjectType); ok {
			for _, elem := range obj.Elems {
				if propElem, ok := elem.(*type_system.PropertyElem); ok {
					propKey := propElem.Name.String()

					if idx, exists := propMap[propKey]; exists {
						// Property already exists - create intersection of the types
						existingProp := mergedElems[idx].(*type_system.PropertyElem)
						intersectedType := type_system.NewIntersectionType(
							nil,
							existingProp.Value,
							propElem.Value,
						)

						// Update the property with the intersected type
						// Preserve other attributes from the first occurrence
						updatedProp := &type_system.PropertyElem{
							Name:     existingProp.Name,
							Value:    intersectedType,
							Optional: existingProp.Optional && propElem.Optional, // Both must be optional
							Readonly: existingProp.Readonly || propElem.Readonly, // Either readonly makes it readonly
						}
						mergedElems[idx] = updatedProp
					} else {
						// First occurrence of this property - add it
						propMap[propKey] = len(mergedElems)
						mergedElems = append(mergedElems, propElem)
					}
				} else {
					// For non-property elements (methods, getters, setters), just append
					// TODO: Handle method/getter/setter conflicts similarly
					mergedElems = append(mergedElems, elem)
				}
			}
		}
	}

	return type_system.NewObjectType(prov, mergedElems)
}

// cartesianProduct generates the cartesian product of multiple slices of types
func (v *TypeExpansionVisitor) cartesianProduct(sets [][]type_system.Type) [][]type_system.Type {
	if len(sets) == 0 {
		return [][]type_system.Type{{}}
	}

	// Start with combinations from the first set
	result := make([][]type_system.Type, 0)
	for _, item := range sets[0] {
		result = append(result, []type_system.Type{item})
	}

	// For each remaining set, combine with existing results
	for i := 1; i < len(sets); i++ {
		var newResult [][]type_system.Type
		for _, existing := range result {
			for _, item := range sets[i] {
				combination := make([]type_system.Type, len(existing)+1)
				copy(combination, existing)
				combination[len(existing)] = item
				newResult = append(newResult, combination)
			}
		}
		result = newResult
	}

	return result
}

// findInferTypes finds all InferType nodes in a type and replaces them with fresh type variables.
// Returns a mapping from infer names to the type variables that replaced them.
func (c *Checker) findInferTypes(t type_system.Type) map[string]type_system.Type {
	visitor := &InferTypeFinder{
		checker:   c,
		inferVars: make(map[string]type_system.Type),
	}
	t.Accept(visitor)
	return visitor.inferVars
}

// InferTypeFinder collects all InferType nodes and replaces them with fresh type variables
type InferTypeFinder struct {
	checker   *Checker
	inferVars map[string]type_system.Type
}

func (v *InferTypeFinder) EnterType(t type_system.Type) type_system.EnterResult {
	// No-op - just for traversal
	return type_system.EnterResult{}
}

func (v *InferTypeFinder) ExitType(t type_system.Type) type_system.Type {
	t = type_system.Prune(t)

	if inferType, ok := t.(*type_system.InferType); ok {
		if existingVar, exists := v.inferVars[inferType.Name]; exists {
			// Reuse existing type variable for same infer name
			return existingVar
		}
		// Create fresh type variable
		freshVar := v.checker.FreshVar(&type_system.TypeProvenance{Type: inferType})
		v.inferVars[inferType.Name] = freshVar
		return freshVar
	}

	// For all other types, return nil to let Accept handle the traversal
	return nil
}

// replaceInferTypes substitutes infer variables in a type with their inferred values from the mapping.
func (c *Checker) replaceInferTypes(t type_system.Type, inferMapping map[string]type_system.Type) type_system.Type {
	visitor := &InferTypeReplacer{
		inferMapping: inferMapping,
	}
	return t.Accept(visitor)
}

// InferTypeReplacer substitutes type variables that correspond to infer types
// with their actual inferred values
type InferTypeReplacer struct {
	inferMapping map[string]type_system.Type
}

func (v *InferTypeReplacer) EnterType(t type_system.Type) type_system.EnterResult {
	// No-op - just for traversal
	return type_system.EnterResult{}
}

func (v *InferTypeReplacer) ExitType(t type_system.Type) type_system.Type {
	t = type_system.Prune(t)

	// Check if this is an InferType that should be replaced
	if inferType, ok := t.(*type_system.InferType); ok {
		if typeVar, exists := v.inferMapping[inferType.Name]; exists {
			// Return the inferred type (what the type variable was unified with)
			return typeVar
		}
	}

	// For all other types, return nil to let Accept handle the traversal
	return nil
}

// restElemType extracts the element type from a RestSpreadType at the given
// offset within the rest. For a TupleType, if the index falls within bounds
// it returns the exact element type; otherwise it returns never. For Array<T>
// it returns T (the index doesn't matter since all elements have the same
// type). For unresolved types it returns the type as-is.
func restElemType(rest *type_system.RestSpreadType, index int) type_system.Type {
	resolved := type_system.Prune(rest.Type)
	switch r := resolved.(type) {
	case *type_system.TupleType:
		// Count fixed (non-rest) elements and find any nested rest spread.
		fixedCount := 0
		var nestedRest *type_system.RestSpreadType
		for _, elem := range r.Elems {
			if nr, ok := elem.(*type_system.RestSpreadType); ok {
				nestedRest = nr
			} else {
				fixedCount++
			}
		}
		if index < fixedCount {
			// Map index to actual position, skipping RestSpreadType
			pos := 0
			for _, elem := range r.Elems {
				if _, ok := elem.(*type_system.RestSpreadType); ok {
					continue
				}
				if pos == index {
					return elem
				}
				pos++
			}
		}
		if nestedRest != nil {
			return restElemType(nestedRest, index-fixedCount)
		}
		return type_system.NewNeverType(nil)
	case *type_system.TypeRefType:
		if type_system.QualIdentToString(r.Name) == "Array" && len(r.TypeArgs) == 1 {
			return r.TypeArgs[0]
		}
		return resolved
	default:
		return resolved
	}
}

// tupleElemUnion computes the union of all element types in a tuple,
// including the inner type of any RestSpreadType elements. For a
// RestSpreadType whose inner type is an Array<T>, T is included; for a
// TupleType, its elements are included; otherwise the rest type itself
// is included.
func tupleElemUnion(t *type_system.TupleType) type_system.Type {
	var types []type_system.Type
	for _, elem := range t.Elems {
		if rest, ok := elem.(*type_system.RestSpreadType); ok {
			resolved := type_system.Prune(rest.Type)
			switch r := resolved.(type) {
			case *type_system.TupleType:
				types = append(types, r.Elems...)
			case *type_system.TypeRefType:
				// If it's Array<T>, include T
				if type_system.QualIdentToString(r.Name) == "Array" && len(r.TypeArgs) == 1 {
					types = append(types, r.TypeArgs[0])
				} else {
					types = append(types, resolved)
				}
			default:
				types = append(types, resolved)
			}
		} else {
			types = append(types, elem)
		}
	}
	return type_system.NewUnionType(nil, types...)
}
