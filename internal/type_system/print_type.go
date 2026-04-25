package type_system

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// PrintConfig controls how types are serialized to strings.
// Zero-value fields use the default behavior (matching the original String() output).
type PrintConfig struct {
	// TypeVarStyle overrides the serialization of TypeVarType.
	// When nil, the default prints the bound value (e.g. "t5" or "t5:constraint").
	TypeVarStyle func(tv *TypeVarType) string

	// TypeRefStyle overrides the "head" portion of TypeRefType serialization
	// (the part before any <TypeArgs>). When nil, the default uses the
	// qualified name (e.g. "Array"). Type arguments are always printed by
	// PrintType using the same config.
	TypeRefStyle func(tr *TypeRefType) string
}

// Precedence levels for type operators, matching the Escalier parser.
// Higher values bind more tightly.
const (
	precFunc         = 2 // fn (...) -> T  — return type is greedy, needs parens in union/intersection
	precUnion        = 3 // A | B
	precIntersection = 4 // A & B
	precPrefix       = 5 // keyof T, mut T, ...T
	precAtom         = 6 // primary types, never need parens
)

// typePrec returns the precedence of a type for printing purposes.
// Types that resolve to a bound TypeVar use the precedence of the resolved type.
func typePrec(t Type) int {
	switch v := t.(type) {
	case *TypeVarType:
		if v.Instance != nil {
			return typePrec(resolveTypeVar(v))
		}
		return precAtom
	case *UnionType:
		return precUnion
	case *IntersectionType:
		return precIntersection
	case *FuncType:
		return precFunc
	case *KeyOfType, *MutabilityType, *RestSpreadType:
		return precPrefix
	default:
		return precAtom
	}
}

// PrintType converts a Type to its string representation using the given config.
// All recursive child-type printing uses the same config, so custom styles
// for TypeVarType and TypeRefType are applied consistently throughout.
// Parentheses are inserted where needed based on operator precedence.
func PrintType(t Type, config PrintConfig) string {
	return printTypeInner(t, config)
}

// printTypeMinPrec prints a child type, wrapping it in parentheses if its
// precedence is lower than the required minimum.
func printTypeMinPrec(t Type, config PrintConfig, minPrec int) string {
	result := printTypeInner(t, config)
	if typePrec(t) < minPrec {
		return "(" + result + ")"
	}
	return result
}

func printTypeInner(t Type, config PrintConfig) string {
	// pt prints a child with no precedence constraint (e.g. inside delimiters).
	pt := func(child Type) string {
		return printTypeInner(child, config)
	}

	switch v := t.(type) {
	case *TypeVarType:
		if config.TypeVarStyle != nil {
			return config.TypeVarStyle(v)
		}
		if v.Instance != nil {
			return printTypeInner(resolveTypeVar(v), config)
		}
		result := "t" + fmt.Sprint(v.ID)
		if v.Constraint != nil {
			result += fmt.Sprintf(":%s", pt(v.Constraint))
		}
		return result

	case *TypeRefType:
		var head string
		if config.TypeRefStyle != nil {
			head = config.TypeRefStyle(v)
		} else {
			head = QualIdentToString(v.Name)
		}
		if len(v.LifetimeArgs) > 0 || len(v.TypeArgs) > 0 {
			head += "<"
			first := true
			for _, lt := range v.LifetimeArgs {
				if !first {
					head += ", "
				}
				first = false
				head += printLifetime(lt)
			}
			for _, arg := range v.TypeArgs {
				if !first {
					head += ", "
				}
				first = false
				head += pt(arg)
			}
			head += ">"
		}
		if v.Lifetime != nil {
			return printLifetime(v.Lifetime) + " " + head
		}
		return head

	case *PrimType:
		return printPrimType(v)

	case *LitType:
		return printLitType(v)

	case *RegexType:
		return v.Regex.String()

	case *UniqueSymbolType:
		return "symbol" + fmt.Sprint(v.Value)

	case *UnknownType:
		return "unknown"

	case *NeverType:
		return "never"

	case *ErrorType:
		return "<error>"

	case *VoidType:
		return "void"

	case *AnyType:
		return "any"

	case *GlobalThisType:
		return "this"

	case *FuncType:
		return printFuncType(v, pt)

	case *ObjectType:
		return printObjectType(v, pt)

	case *TupleType:
		return printTupleType(v, pt)

	case *RestSpreadType:
		return "..." + printTypeMinPrec(v.Type, config, precPrefix)

	case *UnionType:
		return printUnionType(v, config)

	case *IntersectionType:
		return printIntersectionType(v, config)

	case *KeyOfType:
		return "keyof " + printTypeMinPrec(v.Type, config, precPrefix)

	case *TypeOfType:
		return "typeof " + QualIdentToString(v.Ident)

	case *IndexType:
		return printTypeMinPrec(v.Target, config, precAtom) + "[" + pt(v.Index) + "]"

	case *CondType:
		return "if " + pt(v.Check) + " : " + pt(v.Extends) + " { " + pt(v.Then) + " } else { " + pt(v.Else) + " }"

	case *InferType:
		return "infer " + v.Name

	case *MutabilityType:
		return printMutabilityType(v, config)

	case *WildcardType:
		return "_"

	case *ExtractorType:
		return printExtractorType(v, pt)

	case *TemplateLitType:
		return printTemplateLitType(v, pt)

	case *IntrinsicType:
		return v.Name

	case *NamespaceType:
		return printNamespaceType(v, pt)

	default:
		return fmt.Sprint(t)
	}
}

// --- helper functions ---

func printPrimType(t *PrimType) string {
	switch t.Prim {
	case BoolPrim:
		return "boolean"
	case NumPrim:
		return "number"
	case StrPrim:
		return "string"
	case BigIntPrim:
		return "bigint"
	case SymbolPrim:
		return "symbol"
	default:
		panic(fmt.Sprintf("unknown primitive type: %q", t.Prim))
	}
}

func printLitType(t *LitType) string {
	switch lit := t.Lit.(type) {
	case *StrLit:
		return strconv.Quote(lit.Value)
	case *NumLit:
		return strconv.FormatFloat(lit.Value, 'f', -1, 32)
	case *BoolLit:
		return strconv.FormatBool(lit.Value)
	case *BigIntLit:
		return lit.Value.String()
	case *NullLit:
		return "null"
	case *UndefinedLit:
		return "undefined"
	default:
		panic("unknown literal type")
	}
}

func printFuncParam(p *FuncParam, pt func(Type) string) string {
	if p.Pattern == nil {
		result := pt(p.Type)
		if p.Optional {
			result += "?"
		}
		return result
	}
	switch p.Pattern.(type) {
	case *TuplePat, *ObjectPat:
		return printPatternWithInlineTypes(p.Pattern, p.Type, pt)
	default:
		result := p.Pattern.String()
		if p.Optional {
			result += "?"
		}
		result += ": " + pt(p.Type)
		return result
	}
}

// printPatternWithInlineTypes prints a pattern with inline type annotations,
// using the provided printer function for all type-to-string conversions.
func printPatternWithInlineTypes(pattern Pat, paramType Type, pt func(Type) string) string {
	return printPatternWithInlineTypesContext(pattern, paramType, pt, "")
}

func printPatternWithInlineTypesContext(pattern Pat, paramType Type, pt func(Type) string, context string) string {
	paramType = resolveTypeVar(paramType)

	switch p := pattern.(type) {
	case *ObjectPat:
		if objType, ok := paramType.(*ObjectType); ok {
			propTypes := make(map[string]Type)
			propOptionals := make(map[string]bool)
			for _, elem := range objType.Elems {
				if propElem, ok := elem.(*PropertyElem); ok {
					propTypes[propElem.Name.String()] = propElem.Value
					propOptionals[propElem.Name.String()] = propElem.Optional
				}
			}

			var elems []string
			for _, elem := range p.Elems {
				switch e := elem.(type) {
				case *ObjKeyValuePat:
					isOpt := propOptionals[e.Key]
					colon := ": "
					if isOpt {
						colon = "?: "
					}
					if propType, exists := propTypes[e.Key]; exists {
						if _, ok := e.Value.(*IdentPat); ok {
							elems = append(elems, e.Key+colon+pt(propType))
						} else {
							valueStr := printPatternWithInlineTypesContext(e.Value, propType, pt, "object")
							elems = append(elems, e.Key+": "+valueStr)
						}
					} else {
						if _, ok := e.Value.(*IdentPat); ok {
							elems = append(elems, e.Key+colon+pt(paramType))
						} else {
							elems = append(elems, e.Key+": "+e.Value.String())
						}
					}
				case *ObjShorthandPat:
					isOpt := propOptionals[e.Key]
					colon := ": "
					if isOpt {
						colon = "?: "
					}
					if propType, exists := propTypes[e.Key]; exists {
						elems = append(elems, e.Key+colon+pt(propType))
					} else {
						elems = append(elems, e.Key+colon)
					}
				case *ObjRestPat:
					var restType Type
					for _, objElem := range objType.Elems {
						if rest, ok := objElem.(*RestSpreadElem); ok {
							restType = rest.Value
							break
						}
					}
					if restType != nil {
						innerStr := printPatternWithInlineTypesContext(e.Pattern, restType, pt, "object")
						elems = append(elems, "..."+innerStr)
					} else {
						elems = append(elems, e.String())
					}
				}
			}

			result := "{"
			for i, elem := range elems {
				if i > 0 {
					result += ", "
				}
				result += elem
			}
			result += "}"
			return result
		}
	case *TuplePat:
		if tupleType, ok := paramType.(*TupleType); ok {
			var elems []string
			for i, elem := range p.Elems {
				if i < len(tupleType.Elems) {
					elemStr := printPatternWithInlineTypesContext(elem, tupleType.Elems[i], pt, "tuple")
					elems = append(elems, elemStr)
				} else {
					elems = append(elems, elem.String())
				}
			}

			result := "["
			for i, elem := range elems {
				if i > 0 {
					result += ", "
				}
				result += elem
			}
			result += "]"
			return result
		}
	case *RestPat:
		if rest, ok := paramType.(*RestSpreadType); ok {
			innerStr := printPatternWithInlineTypesContext(p.Pattern, rest.Type, pt, context)
			return "..." + innerStr
		}
		return pattern.String()
	case *IdentPat:
		return p.Name + ": " + pt(paramType)
	}

	return pattern.String()
}

func printFuncType(t *FuncType, pt func(Type) string) string {
	result := "fn "
	if len(t.LifetimeParams) > 0 || len(t.TypeParams) > 0 {
		result += "<"
		first := true
		for _, lp := range t.LifetimeParams {
			if !first {
				result += ", "
			}
			first = false
			result += "'" + lp.Name
		}
		for _, param := range t.TypeParams {
			if !first {
				result += ", "
			}
			first = false
			result += param.Name
			if param.Constraint != nil {
				result += ": " + pt(param.Constraint)
			}
			if param.Default != nil {
				result += " = " + pt(param.Default)
			}
		}
		result += ">"
	}
	result += "("
	if len(t.Params) > 0 {
		for i, param := range t.Params {
			if i > 0 {
				result += ", "
			}
			result += printFuncParam(param, pt)
		}
	}
	result += ")"
	if t.Return != nil {
		result += " -> " + pt(t.Return)
	}
	if !IsNeverType(t.Throws) {
		result += " throws " + pt(t.Throws)
	}
	return result
}

func printObjectType(t *ObjectType, pt func(Type) string) string {
	result := "{"
	flatElems := collectFlatElems(t.Elems)
	if len(flatElems) > 0 {
		for i, elem := range flatElems {
			if i > 0 {
				result += ", "
			}
			switch elem := elem.(type) {
			case *CallableElem:
				result += printFuncType(elem.Fn, pt)
			case *ConstructorElem:
				result += "new " + printFuncType(elem.Fn, pt)
			case *MethodElem:
				result += elem.Name.String()
				if len(elem.Fn.TypeParams) > 0 {
					result += "<"
					for i, param := range elem.Fn.TypeParams {
						if i > 0 {
							result += ", "
						}
						result += param.Name
						if param.Constraint != nil {
							result += ": " + pt(param.Constraint)
						}
						if param.Default != nil {
							result += " = " + pt(param.Default)
						}
					}
					result += ">"
				}
				result += "("
				if elem.MutSelf != nil {
					if *elem.MutSelf {
						result += "mut "
					}
					result += "self"
				}
				if len(elem.Fn.Params) > 0 {
					for i, param := range elem.Fn.Params {
						if i > 0 || elem.MutSelf != nil {
							result += ", "
						}
						result += printFuncParam(param, pt)
					}
				}
				result += ")"
				if elem.Fn.Return != nil {
					result += " -> " + pt(elem.Fn.Return)
				}
				if !IsNeverType(elem.Fn.Throws) {
					result += " throws " + pt(elem.Fn.Throws)
				}
			case *GetterElem:
				result += "get " + elem.Name.String() + "(self) -> " + pt(elem.Fn.Return)
				if !IsNeverType(elem.Fn.Throws) {
					result += " throws " + pt(elem.Fn.Throws)
				}
			case *SetterElem:
				result += "set " + elem.Name.String() + "(mut self, "
				if len(elem.Fn.Params) > 0 {
					result += printFuncParam(elem.Fn.Params[0], pt)
				}
				result += ") -> undefined"
				if !IsNeverType(elem.Fn.Throws) {
					result += " throws " + pt(elem.Fn.Throws)
				}
			case *PropertyElem:
				if elem.Readonly {
					result += "readonly "
				}
				result += elem.Name.String()
				if elem.Optional {
					result += "?"
				}
				result += ": " + pt(elem.Value)
			case *MappedElem:
				result += "[" + elem.TypeParam.Name + " in " + pt(elem.TypeParam.Constraint) + "]"
				result += ": " + pt(elem.Value)
			case *IndexSignatureElem:
				if elem.Readonly {
					result += "readonly "
				}
				result += "[key: " + pt(elem.KeyType) + "]: " + pt(elem.Value)
			case *RestSpreadElem:
				result += "..." + pt(elem.Value)
			default:
				panic(fmt.Sprintf("unknown object type element: %#v\n", elem))
			}
		}
	}
	result += "}"
	if t.Lifetime != nil {
		return printLifetime(t.Lifetime) + " " + result
	}
	return result
}

func printTupleType(t *TupleType, pt func(Type) string) string {
	result := "["
	flatElems := collectFlatTupleElems(t.Elems)
	for i, elem := range flatElems {
		if i > 0 {
			result += ", "
		}
		result += pt(elem)
	}
	result += "]"
	if t.Lifetime != nil {
		return printLifetime(t.Lifetime) + " " + result
	}
	return result
}

func printUnionType(t *UnionType, config PrintConfig) string {
	result := ""
	if len(t.Types) > 0 {
		for i, typ := range t.Types {
			if i > 0 {
				result += " | "
			}
			// Union children must have at least union precedence;
			// intersections and atoms print bare, but a nested union
			// (which can't occur after normalization) would need parens.
			result += printTypeMinPrec(typ, config, precUnion)
		}
	}
	return result
}

func printIntersectionType(t *IntersectionType, config PrintConfig) string {
	result := ""
	if len(t.Types) > 0 {
		for i, typ := range t.Types {
			if i > 0 {
				result += " & "
			}
			// Intersection children must have at least intersection
			// precedence; a union child gets wrapped in parens.
			result += printTypeMinPrec(typ, config, precIntersection)
		}
	}
	return result
}

func printMutabilityType(t *MutabilityType, config PrintConfig) string {
	switch t.Mutability {
	case MutabilityUncertain:
		return "mut? " + printTypeMinPrec(t.Type, config, precPrefix)
	case MutabilityMutable:
		return "mut " + printTypeMinPrec(t.Type, config, precPrefix)
	default:
		panic(fmt.Sprintf("unexpected mutability value: %q", t.Mutability))
	}
}

func printExtractorType(t *ExtractorType, pt func(Type) string) string {
	result := pt(t.Extractor)
	if len(t.Args) > 0 {
		result += "("
		for i, arg := range t.Args {
			if i > 0 {
				result += ", "
			}
			result += pt(arg)
		}
		result += ")"
	}
	return result
}

func printTemplateLitType(t *TemplateLitType, pt func(Type) string) string {
	result := "`"
	for i, quasi := range t.Quasis {
		result += quasi.Value
		if i < len(t.Types) {
			result += "${" + pt(t.Types[i]) + "}"
		}
	}
	result += "`"
	return result
}

func printNamespaceType(t *NamespaceType, pt func(Type) string) string {
	var builder strings.Builder
	builder.WriteString("namespace {")
	if len(t.Namespace.Values) > 0 {
		valueNames := make([]string, 0, len(t.Namespace.Values))
		for name := range t.Namespace.Values {
			valueNames = append(valueNames, name)
		}
		sort.Strings(valueNames)
		for _, name := range valueNames {
			builder.WriteString(name)
			builder.WriteString(": ")
			builder.WriteString(pt(t.Namespace.Values[name].Type))
			builder.WriteString(", ")
		}
	}
	if len(t.Namespace.Types) > 0 {
		typeNames := make([]string, 0, len(t.Namespace.Types))
		for name := range t.Namespace.Types {
			typeNames = append(typeNames, name)
		}
		sort.Strings(typeNames)
		for _, name := range typeNames {
			builder.WriteString(name)
			builder.WriteString(": ")
			builder.WriteString(pt(t.Namespace.Types[name].Type))
			builder.WriteString(", ")
		}
	}
	builder.WriteString("}")
	return builder.String()
}

// printLifetime renders a Lifetime value for inclusion in a type's string
// form. LifetimeVar prints as 'name (or 't<id> for unnamed vars), a
// resolved LifetimeValue prints its identity name (or 'static when
// IsStatic), and a LifetimeUnion prints as ('a | 'b).
func printLifetime(lt Lifetime) string {
	switch v := PruneLifetime(lt).(type) {
	case *LifetimeVar:
		if v.Name != "" {
			return "'" + v.Name
		}
		return "'t" + strconv.Itoa(v.ID)
	case *LifetimeValue:
		if v.IsStatic {
			return "'static"
		}
		if v.Name != "" {
			return "'" + v.Name
		}
		return "'v" + strconv.Itoa(v.ID)
	case *LifetimeUnion:
		parts := make([]string, len(v.Lifetimes))
		for i, m := range v.Lifetimes {
			parts[i] = printLifetime(m)
		}
		return "(" + strings.Join(parts, " | ") + ")"
	}
	return "<lifetime?>"
}
