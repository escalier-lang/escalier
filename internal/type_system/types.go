//go:generate go run ../../tools/gen_types/gen_types.go

package type_system

import (
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"

	"github.com/escalier-lang/escalier/internal/provenance"
	. "github.com/escalier-lang/escalier/internal/provenance"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// QualIdent represents a qualified identifier (e.g., "Foo" or "Foo.Bar.Baz")
type QualIdent interface{ isQualIdent() }

func (*Ident) isQualIdent()  {}
func (*Member) isQualIdent() {}

// Ident represents a simple identifier
type Ident struct {
	Name string
}

// Member represents a member access in a qualified identifier
type Member struct {
	Left  QualIdent
	Right *Ident
}

// QualIdentToString converts a QualIdent to its string representation
func QualIdentToString(qi QualIdent) string {
	switch q := qi.(type) {
	case *Ident:
		return q.Name
	case *Member:
		left := QualIdentToString(q.Left)
		return left + "." + q.Right.Name
	default:
		return ""
	}
}

// NewIdent creates a new simple identifier
func NewIdent(name string) *Ident {
	return &Ident{Name: name}
}

//sumtype:decl
type Type interface {
	isType()
	Provenance() Provenance
	SetProvenance(Provenance)
	Accept(TypeVisitor) Type
	String() string
	Copy() Type // Return a shallow copy of the Type.
}

func (*TypeVarType) isType()      {}
func (*TypeRefType) isType()      {}
func (*PrimType) isType()         {}
func (*LitType) isType()          {}
func (*UniqueSymbolType) isType() {}
func (*UnknownType) isType()      {}
func (*NeverType) isType()        {}
func (*VoidType) isType()         {}
func (*AnyType) isType()          {}
func (*GlobalThisType) isType()   {}
func (*FuncType) isType()         {}
func (*ObjectType) isType()       {}
func (*TupleType) isType()        {}
func (*RestSpreadType) isType()   {}
func (*UnionType) isType()        {}
func (*IntersectionType) isType() {}
func (*KeyOfType) isType()        {}
func (*TypeOfType) isType()       {}
func (*IndexType) isType()        {}
func (*CondType) isType()         {}
func (*InferType) isType()        {}
func (*MutabilityType) isType()   {}
func (*WildcardType) isType()     {}
func (*ExtractorType) isType()    {}
func (*TemplateLitType) isType()  {}
func (*IntrinsicType) isType()    {}
func (*NamespaceType) isType()    {}
func (*RegexType) isType()        {}

func Prune(t Type) Type {
	switch t := t.(type) {
	case *TypeVarType:
		if t.Instance != nil {
			newInstance := Prune(t.Instance)
			t.Instance = newInstance
			return newInstance
		}
		return t
	default:
		return t
	}
}

type TypeVarType struct {
	ID          int
	Instance    Type
	Constraint  Type
	Default     Type
	FromBinding bool
	provenance  Provenance
}

func NewTypeVarType(provenance Provenance, id int) *TypeVarType {
	return &TypeVarType{
		ID:          id,
		Instance:    nil,
		Constraint:  nil,
		Default:     nil,
		FromBinding: false,
		provenance:  provenance,
	}
}

func (t *TypeVarType) Accept(v TypeVisitor) Type {
	prunedType := Prune(t)
	if prunedType != t {
		return prunedType.Accept(v) // Accept on the pruned type
	}

	if result := v.EnterType(prunedType); result != nil {
		t = result.(*TypeVarType)
	}
	if result := v.ExitType(prunedType); result != nil {
		return result
	}

	return t
}
func (t *TypeVarType) String() string {
	if t.Instance != nil {
		return Prune(t).String()
	}
	result := "t" + fmt.Sprint(t.ID)
	if t.Constraint != nil {
		result += fmt.Sprintf(":%s", t.Constraint.String())
	}
	return result
}

type TypeAlias struct {
	Type       Type
	TypeParams []*TypeParam
}

type TypeRefType struct {
	Name       QualIdent
	TypeArgs   []Type
	TypeAlias  *TypeAlias // optional, resolved type alias (definition)
	provenance Provenance
}

func NewTypeRefType(provenance Provenance, name string, typeAlias *TypeAlias, typeArgs ...Type) *TypeRefType {
	return &TypeRefType{
		Name:       NewIdent(name),
		TypeArgs:   typeArgs,
		TypeAlias:  typeAlias,
		provenance: nil,
	}
}

func NewTypeRefTypeFromQualIdent(provenance Provenance, name QualIdent, typeAlias *TypeAlias, typeArgs ...Type) *TypeRefType {
	return &TypeRefType{
		Name:       name,
		TypeArgs:   typeArgs,
		TypeAlias:  typeAlias,
		provenance: provenance,
	}
}
func (t *TypeRefType) Accept(v TypeVisitor) Type {
	if result := v.EnterType(t); result != nil {
		switch result := result.(type) {
		case *TypeRefType:
			t = result
		default:
			return result.Accept(v)
		}
	}

	changed := false
	newTypeArgs := make([]Type, len(t.TypeArgs))
	for i, arg := range t.TypeArgs {
		newArg := arg.Accept(v)
		if newArg != arg {
			changed = true
		}
		newTypeArgs[i] = newArg
	}

	var result Type = t
	if changed {
		result = NewTypeRefTypeFromQualIdent(t.provenance, t.Name, t.TypeAlias, newTypeArgs...)
	}

	if visitResult := v.ExitType(result); visitResult != nil {
		return visitResult
	}
	return result
}
func (t *TypeRefType) String() string {
	result := QualIdentToString(t.Name)
	if len(t.TypeArgs) > 0 {
		result += "<"
		for i, arg := range t.TypeArgs {
			if i > 0 {
				result += ", "
			}
			result += arg.String()
		}
		result += ">"
	}
	return result
}

type Prim string

const (
	BoolPrim   Prim = "boolean"
	NumPrim    Prim = "number"
	StrPrim    Prim = "string"
	BigIntPrim Prim = "bigint"
	SymbolPrim Prim = "symbol"
)

type PrimType struct {
	Prim       Prim
	provenance Provenance
}

func NewNumPrimType(provenance Provenance) *PrimType {
	return &PrimType{
		Prim:       NumPrim,
		provenance: provenance,
	}
}
func NewStrPrimType(provenance Provenance) *PrimType {
	return &PrimType{
		Prim:       StrPrim,
		provenance: provenance,
	}
}
func NewBoolPrimType(provenance Provenance) *PrimType {
	return &PrimType{
		Prim:       BoolPrim,
		provenance: provenance,
	}
}
func NewSymPrimType(provenance Provenance) *PrimType {
	return &PrimType{
		Prim:       SymbolPrim,
		provenance: provenance,
	}
}
func NewBigIntPrimType(provenance Provenance) *PrimType {
	return &PrimType{
		Prim:       BigIntPrim,
		provenance: provenance,
	}
}
func (t *PrimType) Accept(v TypeVisitor) Type {
	if result := v.EnterType(t); result != nil {
		t = result.(*PrimType)
	}
	if result := v.ExitType(t); result != nil {
		return result
	}
	return t
}
func (t *PrimType) String() string {
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
		panic("unknown primitive type")
	}
}

type RegexType struct {
	Regex      *regexp.Regexp
	Groups     map[string]Type // optional, used for named capture groups
	provenance Provenance
}

func NewRegexType(provenance Provenance, regex *regexp.Regexp, groups map[string]Type) *RegexType {
	return &RegexType{
		Regex:      regex,
		Groups:     groups,
		provenance: provenance,
	}
}
func NewRegexTypeWithPatternString(provenance Provenance, pattern string) (Type, error) {
	// parse the pattern as a regular expression

	pattern, err := convertJSRegexToGo(pattern)
	if err != nil {
		return NewNeverType(nil), fmt.Errorf("failed to convert regex: %v", err)
	}

	regex := regexp.MustCompile(pattern)

	groups := make(map[string]Type)
	if regex != nil {
		for _, name := range regex.SubexpNames()[1:] {
			if name != "" { // Skip unnamed groups
				groups[name] = NewStrPrimType(nil)
			}
		}
	}

	return NewRegexType(provenance, regex, groups), nil
}
func (t *RegexType) Accept(v TypeVisitor) Type {
	if result := v.EnterType(t); result != nil {
		t = result.(*RegexType)
	}
	if result := v.ExitType(t); result != nil {
		return result
	}
	return t
}
func (t *RegexType) String() string {
	return t.Regex.String()
}

type LitType struct {
	Lit        Lit
	provenance Provenance
}

func NewStrLitType(provenance Provenance, value string) *LitType {
	return &LitType{
		Lit:        &StrLit{Value: value},
		provenance: provenance,
	}
}
func NewNumLitType(provenance Provenance, value float64) *LitType {
	return &LitType{
		Lit:        &NumLit{Value: value},
		provenance: provenance,
	}
}
func NewBoolLitType(provenance Provenance, value bool) *LitType {
	return &LitType{
		Lit:        &BoolLit{Value: value},
		provenance: provenance,
	}
}
func NewNullType(provenance Provenance) *LitType {
	return &LitType{
		Lit:        &NullLit{},
		provenance: provenance,
	}
}
func NewUndefinedType(provenance Provenance) *LitType {
	return &LitType{
		Lit:        &UndefinedLit{},
		provenance: provenance,
	}
}
func NewBigIntLitType(provenance Provenance, value big.Int) *LitType {
	return &LitType{
		Lit:        &BigIntLit{Value: value},
		provenance: provenance,
	}
}

func (t *LitType) Accept(v TypeVisitor) Type {
	if result := v.EnterType(t); result != nil {
		t = result.(*LitType)
	}
	if result := v.ExitType(t); result != nil {
		return result
	}
	return t
}
func (t *LitType) String() string {
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

type UniqueSymbolType struct {
	Value      int
	provenance Provenance
}

func NewUniqueSymbolType(provenance Provenance, value int) *UniqueSymbolType {
	return &UniqueSymbolType{
		Value:      value,
		provenance: provenance,
	}
}

func (t *UniqueSymbolType) Accept(v TypeVisitor) Type {
	if result := v.EnterType(t); result != nil {
		t = result.(*UniqueSymbolType)
	}
	if result := v.ExitType(t); result != nil {
		return result
	}
	return t
}
func (t *UniqueSymbolType) String() string {
	return "symbol" + fmt.Sprint(t.Value)
}

type UnknownType struct {
	provenance Provenance
}

func (t *UnknownType) Accept(v TypeVisitor) Type {
	if result := v.EnterType(t); result != nil {
		t = result.(*UnknownType)
	}
	if result := v.ExitType(t); result != nil {
		return result
	}
	return t
}

func NewUnknownType(provenance Provenance) *UnknownType { return &UnknownType{provenance: provenance} }
func (t *UnknownType) String() string {
	return "unknown"
}

type NeverType struct {
	provenance Provenance
}

func (t *NeverType) Accept(v TypeVisitor) Type {
	if result := v.EnterType(t); result != nil {
		t = result.(*NeverType)
	}
	if result := v.ExitType(t); result != nil {
		return result
	}
	return t
}

func NewNeverType(provenance Provenance) *NeverType { return &NeverType{provenance: provenance} }
func (t *NeverType) String() string {
	return "never"
}

type VoidType struct {
	provenance Provenance
}

func (t *VoidType) Accept(v TypeVisitor) Type {
	if result := v.EnterType(t); result != nil {
		t = result.(*VoidType)
	}
	if result := v.ExitType(t); result != nil {
		return result
	}
	return t
}

func NewVoidType(provenance Provenance) *VoidType { return &VoidType{provenance: provenance} }
func (t *VoidType) String() string {
	return "void"
}

type AnyType struct {
	provenance Provenance
}

func (t *AnyType) Accept(v TypeVisitor) Type {
	if result := v.EnterType(t); result != nil {
		t = result.(*AnyType)
	}
	if result := v.ExitType(t); result != nil {
		return result
	}
	return t
}

func NewAnyType(provenance Provenance) *AnyType { return &AnyType{provenance: provenance} }
func (t *AnyType) String() string {
	return "any"
}

type GlobalThisType struct {
	provenance Provenance
}

func (t *GlobalThisType) Accept(v TypeVisitor) Type {
	if result := v.EnterType(t); result != nil {
		t = result.(*GlobalThisType)
	}
	if result := v.ExitType(t); result != nil {
		return result
	}
	return t
}
func (t *GlobalThisType) String() string {
	return "this"
}

type TypeParam struct {
	Name       string
	Constraint Type
	Default    Type
}

func NewTypeParam(name string) *TypeParam {
	return &TypeParam{
		Name:       name,
		Constraint: nil,
		Default:    nil,
	}
}

func NewTypeParamWithDefault(name string, default_ Type) *TypeParam {
	return &TypeParam{
		Name:       name,
		Constraint: nil,
		Default:    default_,
	}
}

type FuncParam struct {
	Pattern  Pat
	Type     Type
	Optional bool
}

func NewFuncParam(pattern Pat, t Type) *FuncParam {
	return &FuncParam{
		Pattern:  pattern,
		Type:     t,
		Optional: false,
	}
}

type FuncType struct {
	TypeParams []*TypeParam
	Params     []*FuncParam
	Return     Type
	Throws     Type
	provenance Provenance
}

func NewFuncType(provenance Provenance, typeParams []*TypeParam, params []*FuncParam, returnType Type, throws Type) *FuncType {
	return &FuncType{
		TypeParams: typeParams,
		Params:     params,
		Return:     returnType,
		Throws:     throws,
		provenance: provenance,
	}
}
func (t *FuncType) Accept(v TypeVisitor) Type {
	if result := v.EnterType(t); result != nil {
		t = result.(*FuncType)
	}

	changed := false
	newParams := make([]*FuncParam, len(t.Params))
	for i, param := range t.Params {
		newType := param.Type.Accept(v)
		if newType != param.Type {
			changed = true
			newParams[i] = &FuncParam{
				Pattern:  param.Pattern,
				Type:     newType,
				Optional: param.Optional,
			}
		} else {
			newParams[i] = param
		}
	}

	var newReturn Type
	if t.Return != nil {
		newReturn = t.Return.Accept(v)
		if newReturn != t.Return {
			changed = true
		}
	}

	var newThrows Type
	if t.Throws != nil {
		newThrows = t.Throws.Accept(v)
		if newThrows != t.Throws {
			changed = true
		}
	}

	var result Type = t
	if changed {
		result = NewFuncType(
			t.provenance,
			t.TypeParams,
			newParams,
			newReturn,
			newThrows,
		)
	}

	if visitResult := v.ExitType(result); visitResult != nil {
		return visitResult
	}
	return result
}

// patternStringWithInlineTypes prints a pattern with inline type annotations for object and tuple patterns
func patternStringWithInlineTypes(pattern Pat, paramType Type) string {
	return patternStringWithInlineTypesContext(pattern, paramType, "")
}

// patternStringWithInlineTypesContext prints a pattern with inline type annotations
// context can be "tuple" or "object" to control the separator used for IdentPat
func patternStringWithInlineTypesContext(pattern Pat, paramType Type, context string) string {
	// Prune the type to resolve any type variables
	paramType = Prune(paramType)

	switch p := pattern.(type) {
	case *ObjectPat:
		if objType, ok := paramType.(*ObjectType); ok {
			// Create a map of property names to their types for quick lookup
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
							elems = append(elems, e.Key+colon+propType.String())
						} else {
							valueStr := patternStringWithInlineTypesContext(e.Value, propType, "object")
							elems = append(elems, e.Key+": "+valueStr)
						}
					} else {
						if _, ok := e.Value.(*IdentPat); ok {
							elems = append(elems, e.Key+colon+paramType.String())
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
						elems = append(elems, e.Key+colon+propType.String())
					} else {
						elems = append(elems, e.Key+colon)
					}
				case *ObjRestPat:
					elems = append(elems, e.String())
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
					// Recursively apply inline types to tuple elements
					elemStr := patternStringWithInlineTypesContext(elem, tupleType.Elems[i], "tuple")
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
	case *IdentPat:
		return p.Name + ": " + paramType.String()
	}

	// For other pattern types or when types don't match, fall back to default
	return pattern.String()
}

func (t *FuncType) String() string {
	result := "fn "
	if len(t.TypeParams) > 0 {
		result += "<"
		for i, param := range t.TypeParams {
			if i > 0 {
				result += ", "
			}
			result += param.Name
			if param.Constraint != nil {
				result += ": " + param.Constraint.String()
			}
			if param.Default != nil {
				result += " = " + param.Default.String()
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
			switch param.Pattern.(type) {
			case *TuplePat, *ObjectPat:
				// Use inline type annotations for object and tuple patterns
				result += patternStringWithInlineTypes(param.Pattern, param.Type)
			default:
				result += param.Pattern.String() + ": " + param.Type.String()
			}
		}
	}
	result += ")"
	if t.Return != nil {
		result += " -> " + t.Return.String()
	}
	if t.Throws != nil {
		result += " throws " + t.Throws.String()
	}
	return result
}

type ObjTypeKeyKind int

const (
	StrObjTypeKeyKind ObjTypeKeyKind = iota
	NumObjTypeKeyKind
	SymObjTypeKeyKind
)

type ObjTypeKey struct {
	Kind ObjTypeKeyKind // this is an "enum"
	Str  string
	Num  float64
	Sym  int
}

func NewStrKey(str string) ObjTypeKey {
	return ObjTypeKey{
		Kind: StrObjTypeKeyKind,
		Str:  str,
		Num:  0,
		Sym:  0,
	}
}
func NewNumKey(num float64) ObjTypeKey {
	return ObjTypeKey{
		Kind: NumObjTypeKeyKind,
		Str:  "",
		Num:  num,
		Sym:  0,
	}
}
func NewSymKey(sym int) ObjTypeKey {
	return ObjTypeKey{
		Kind: SymObjTypeKeyKind,
		Str:  "",
		Num:  0,
		Sym:  sym,
	}
}
func (s *ObjTypeKey) String() string {
	switch s.Kind {
	case StrObjTypeKeyKind:
		return s.Str
	case NumObjTypeKeyKind:
		return strconv.FormatFloat(s.Num, 'f', -1, 32)
	case SymObjTypeKeyKind:
		return "symbol" + fmt.Sprint(s.Sym)
	default:
		panic("unknown object type key kind")
	}
}

type ObjTypeElem interface {
	isObjTypeElem()
	Accept(TypeVisitor) ObjTypeElem
}

type CallableElem struct{ Fn *FuncType }
type ConstructorElem struct{ Fn *FuncType }
type MethodElem struct {
	Name    ObjTypeKey
	Fn      *FuncType
	MutSelf *bool // nil = unknown, true = mut self, false = self
}
type GetterElem struct {
	Name ObjTypeKey
	Fn   *FuncType
}
type SetterElem struct {
	Name ObjTypeKey
	Fn   *FuncType
}
type PropertyElem struct {
	Name     ObjTypeKey
	Optional bool
	Readonly bool
	Value    Type
}

func NewMethodElem(name ObjTypeKey, fn *FuncType, mutSelf *bool) *MethodElem {
	return &MethodElem{
		Name:    name,
		Fn:      fn,
		MutSelf: mutSelf,
	}
}
func NewGetterElem(name ObjTypeKey, fn *FuncType) *GetterElem {
	return &GetterElem{
		Name: name,
		Fn:   fn,
	}
}
func NewSetterElem(name ObjTypeKey, fn *FuncType) *SetterElem {
	return &SetterElem{
		Name: name,
		Fn:   fn,
	}
}
func NewPropertyElem(name ObjTypeKey, value Type) *PropertyElem {
	return &PropertyElem{
		Name:     name,
		Optional: false,
		Readonly: false,
		Value:    value,
	}
}

type MappedModifier string

const (
	MMAdd    MappedModifier = "add"
	MMRemove MappedModifier = "remove"
)

type MappedElem struct {
	TypeParam *IndexParam
	Name      Type // optional
	Value     Type
	Optional  *MappedModifier // TODO: replace with `?`, `!`, or nothing
	Readonly  *MappedModifier
	Check     Type // optional - the type to check
	Extends   Type // optional - the type it should extend
}
type IndexParam struct {
	Name       string
	Constraint Type
}
type RestSpreadElem struct{ Value Type }

func NewRestSpreadElem(value Type) *RestSpreadElem {
	return &RestSpreadElem{
		Value: value,
	}
}

func (*CallableElem) isObjTypeElem()    {}
func (*ConstructorElem) isObjTypeElem() {}
func (*MethodElem) isObjTypeElem()      {}
func (*GetterElem) isObjTypeElem()      {}
func (*SetterElem) isObjTypeElem()      {}
func (*PropertyElem) isObjTypeElem()    {}
func (*MappedElem) isObjTypeElem()      {}
func (*RestSpreadElem) isObjTypeElem()  {}

func (c *CallableElem) Accept(v TypeVisitor) ObjTypeElem {
	newFn := c.Fn.Accept(v).(*FuncType)
	if newFn != c.Fn {
		return &CallableElem{Fn: newFn}
	}
	return c
}
func (c *ConstructorElem) Accept(v TypeVisitor) ObjTypeElem {
	newFn := c.Fn.Accept(v).(*FuncType)
	if newFn != c.Fn {
		return &ConstructorElem{Fn: newFn}
	}
	return c
}
func (m *MethodElem) Accept(v TypeVisitor) ObjTypeElem {
	newFn := m.Fn.Accept(v).(*FuncType)
	if newFn != m.Fn {
		return &MethodElem{Name: m.Name, Fn: newFn}
	}
	return m
}
func (g *GetterElem) Accept(v TypeVisitor) ObjTypeElem {
	newFn := g.Fn.Accept(v).(*FuncType)
	if newFn != g.Fn {
		return &GetterElem{Name: g.Name, Fn: newFn}
	}
	return g
}
func (s *SetterElem) Accept(v TypeVisitor) ObjTypeElem {
	newFn := s.Fn.Accept(v).(*FuncType)
	if newFn != s.Fn {
		return &SetterElem{Name: s.Name, Fn: newFn}
	}
	return s
}
func (p *PropertyElem) Accept(v TypeVisitor) ObjTypeElem {
	newValue := p.Value.Accept(v)
	if newValue != p.Value {
		return &PropertyElem{
			Name:     p.Name,
			Optional: p.Optional,
			Readonly: p.Readonly,
			Value:    newValue,
		}
	}
	return p
}
func (m *MappedElem) Accept(v TypeVisitor) ObjTypeElem {
	changed := false
	newConstraint := m.TypeParam.Constraint.Accept(v)
	if newConstraint != m.TypeParam.Constraint {
		changed = true
	}

	var newName Type
	if m.Name != nil {
		newName = m.Name.Accept(v)
		if newName != m.Name {
			changed = true
		}
	}

	newValue := m.Value.Accept(v)
	if newValue != m.Value {
		changed = true
	}

	var newCheck Type
	if m.Check != nil {
		newCheck = m.Check.Accept(v)
		if newCheck != m.Check {
			changed = true
		}
	}

	var newExtends Type
	if m.Extends != nil {
		newExtends = m.Extends.Accept(v)
		if newExtends != m.Extends {
			changed = true
		}
	}

	if changed {
		newTypeParam := &IndexParam{
			Name:       m.TypeParam.Name,
			Constraint: newConstraint,
		}
		return &MappedElem{
			TypeParam: newTypeParam,
			Name:      newName,
			Value:     newValue,
			Optional:  m.Optional,
			Readonly:  m.Readonly,
			Check:     newCheck,
			Extends:   newExtends,
		}
	}
	return m
}
func (r *RestSpreadElem) Accept(v TypeVisitor) ObjTypeElem {
	newValue := r.Value.Accept(v)
	if newValue != r.Value {
		return &RestSpreadElem{Value: newValue}
	}
	return r
}

type ObjectType struct {
	ID         int
	Elems      []ObjTypeElem
	Exact      bool // Can't be true if any of Interface, Implements, or Extends are true
	Immutable  bool // true for `#{...}`, false for `{...}`
	Mutable    bool // true for `mut {...}`, false for `{...}`
	Nominal    bool // true for classes
	Interface  bool
	Extends    []*TypeRefType
	Implements []*TypeRefType
	// NOTE: the value type is ast.Expr, but we can't use that here because it
	// would cause a cycle between type_system and ast packages.
	// Maps symbols used as keys to the ast.Expr that was used as the computed
	// key.
	SymbolKeyMap map[int]any
	// TODO: support multiple provenance entries for different elements so that
	// we can work back from an element to the interface decl that defined it.
	provenance Provenance
}

var idCounter int = 0

// TODO: add different constructors for different types of object types
func NewObjectType(provenance Provenance, elems []ObjTypeElem) *ObjectType {
	return &ObjectType{
		ID:           0,
		Elems:        elems,
		Exact:        false,
		Immutable:    false,
		Mutable:      false,
		Nominal:      false,
		Interface:    false,
		Extends:      nil,
		Implements:   nil,
		SymbolKeyMap: nil,
		provenance:   provenance,
	}
}
func NewNominalObjectType(provenance Provenance, elems []ObjTypeElem) *ObjectType {
	idCounter++
	return &ObjectType{
		ID:           idCounter,
		Elems:        elems,
		Exact:        false,
		Immutable:    false,
		Mutable:      false,
		Nominal:      true,
		Interface:    false,
		Extends:      nil,
		Implements:   nil,
		SymbolKeyMap: nil,
		provenance:   provenance,
	}
}

func (t *ObjectType) Accept(v TypeVisitor) Type {
	if result := v.EnterType(t); result != nil {
		t = result.(*ObjectType)
	}

	changed := false
	newElems := make([]ObjTypeElem, len(t.Elems))
	for i, elem := range t.Elems {
		newElem := elem.Accept(v)
		if newElem != elem {
			changed = true
		}
		newElems[i] = newElem
	}

	newExtends := make([]*TypeRefType, len(t.Extends))
	for i, ext := range t.Extends {
		newExt := ext.Accept(v).(*TypeRefType)
		if newExt != ext {
			changed = true
		}
		newExtends[i] = newExt
	}

	newImplements := make([]*TypeRefType, len(t.Implements))
	for i, impl := range t.Implements {
		newImpl := impl.Accept(v).(*TypeRefType)
		if newImpl != impl {
			changed = true
		}
		newImplements[i] = newImpl
	}

	var result *ObjectType = t
	if changed {
		result = NewObjectType(t.provenance, newElems)
		result.ID = t.ID
		result.Exact = t.Exact
		result.Immutable = t.Immutable
		result.Mutable = t.Mutable
		result.Nominal = t.Nominal
		result.Interface = t.Interface
		result.Extends = newExtends
		result.Implements = newImplements
		result.SymbolKeyMap = t.SymbolKeyMap
	}

	if visitResult := v.ExitType(result); visitResult != nil {
		return visitResult
	}
	return result
}
func (t *ObjectType) String() string {
	result := "{"
	if len(t.Elems) > 0 {
		for i, elem := range t.Elems {
			if i > 0 {
				result += ", "
			}
			switch elem := elem.(type) {
			case *CallableElem:
				result += elem.Fn.String()
			case *ConstructorElem:
				result += "new " + elem.Fn.String()
			case *MethodElem:
				// TODO: update this to include `self` parameter
				result += elem.Name.String()
				if len(elem.Fn.TypeParams) > 0 {
					result += "<"
					for i, param := range elem.Fn.TypeParams {
						if i > 0 {
							result += ", "
						}
						result += param.Name
						if param.Constraint != nil {
							result += ": " + param.Constraint.String()
						}
						if param.Default != nil {
							result += " = " + param.Default.String()
						}
					}
					result += ">"
				}
				result += "("
				if len(elem.Fn.Params) > 0 {
					for i, param := range elem.Fn.Params {
						if i > 0 {
							result += ", "
						}
						switch param.Pattern.(type) {
						case *TuplePat, *ObjectPat:
							// Use inline type annotations for object and tuple patterns
							result += patternStringWithInlineTypes(param.Pattern, param.Type)
						default:
							result += param.Pattern.String() + ": " + param.Type.String()
						}
					}
				}
				result += ")"
				if elem.Fn.Return != nil {
					result += " -> " + elem.Fn.Return.String()
				}
				if elem.Fn.Throws != nil {
					result += " throws " + elem.Fn.Throws.String()
				}
			case *GetterElem:
				result += "get " + elem.Name.String() + "() -> " + elem.Fn.Return.String()
				if elem.Fn.Throws != nil {
					result += " throws " + elem.Fn.Throws.String()
				}
			case *SetterElem:
				result += "set " + elem.Name.String() + "("
				result += elem.Fn.Params[0].Pattern.String() + ": " + elem.Fn.Params[0].Type.String()
				result += ") -> undefined"
				if elem.Fn.Throws != nil {
					result += " throws " + elem.Fn.Throws.String()
				}
			case *PropertyElem:
				result += elem.Name.String()
				if elem.Optional {
					result += "?"
				}
				result += ": " + elem.Value.String()
			case *MappedElem:
				// TODO: handle renaming
				// TODO: handle optional and readonly
				result += "[" + elem.TypeParam.Name + " in " + elem.TypeParam.Constraint.String() + "]"
				result += ": " + elem.Value.String()
			case *RestSpreadElem:
				result += "..." + elem.Value.String()
			default:
				panic(fmt.Sprintf("unknown object type element: %#v\n", elem))
			}
		}
	}
	result += "}"
	return result
}

type TupleType struct {
	Elems      []Type
	provenance Provenance
}

func NewTupleType(provenance Provenance, elems ...Type) *TupleType {
	return &TupleType{
		Elems:      elems,
		provenance: provenance,
	}
}
func (t *TupleType) Accept(v TypeVisitor) Type {
	if result := v.EnterType(t); result != nil {
		t = result.(*TupleType)
	}

	changed := false
	newElems := make([]Type, len(t.Elems))
	for i, elem := range t.Elems {
		newElem := elem.Accept(v)
		if newElem != elem {
			changed = true
		}
		newElems[i] = newElem
	}

	var result Type = t
	if changed {
		result = NewTupleType(t.provenance, newElems...)
	}

	if visitResult := v.ExitType(result); visitResult != nil {
		return visitResult
	}
	return result
}
func (t *TupleType) String() string {
	result := "["
	if len(t.Elems) > 0 {
		for i, elem := range t.Elems {
			if i > 0 {
				result += ", "
			}
			result += elem.String()
		}
	}
	result += "]"
	return result
}

type RestSpreadType struct {
	Type       Type
	provenance Provenance
}

func NewRestSpreadType(provenance Provenance, typ Type) *RestSpreadType {
	return &RestSpreadType{
		Type:       typ,
		provenance: provenance,
	}
}
func (t *RestSpreadType) Accept(v TypeVisitor) Type {
	if result := v.EnterType(t); result != nil {
		t = result.(*RestSpreadType)
	}

	newType := t.Type.Accept(v)
	var result Type = t
	if newType != t.Type {
		result = NewRestSpreadType(t.provenance, newType)
	}

	if visitResult := v.ExitType(result); visitResult != nil {
		return visitResult
	}
	return result
}
func (t *RestSpreadType) String() string {
	return "..." + t.Type.String()
}

type UnionType struct {
	Types      []Type
	provenance Provenance
}

func NewUnionType(provenance Provenance, types ...Type) Type {
	if len(types) == 0 {
		return NewNeverType(nil)
	}
	if len(types) == 1 {
		return types[0]
	}
	return &UnionType{
		Types:      types,
		provenance: provenance,
	}
}
func (t *UnionType) Accept(v TypeVisitor) Type {
	if result := v.EnterType(t); result != nil {
		t = result.(*UnionType)
	}

	changed := false
	newTypes := make([]Type, len(t.Types))
	for i, typ := range t.Types {
		newType := typ.Accept(v)
		if newType != typ {
			changed = true
		}
		newTypes[i] = newType
	}

	var result Type = t
	if changed {
		result = NewUnionType(t.provenance, newTypes...)
	}

	if visitResult := v.ExitType(result); visitResult != nil {
		return visitResult
	}
	return result
}

// TODO: handle precedence when printing
func (t *UnionType) String() string {
	result := ""
	if len(t.Types) > 0 {
		for i, typ := range t.Types {
			if i > 0 {
				result += " | "
			}
			result += typ.String()
		}
	}
	return result
}

type IntersectionType struct {
	Types      []Type
	provenance Provenance
}

func NewIntersectionType(provenance Provenance, types ...Type) Type {
	if len(types) == 0 {
		return NewNeverType(nil)
	}
	if len(types) == 1 {
		return types[0]
	}
	return &IntersectionType{
		Types:      types,
		provenance: provenance,
	}
}
func (t *IntersectionType) Accept(v TypeVisitor) Type {
	if result := v.EnterType(t); result != nil {
		t = result.(*IntersectionType)
	}

	changed := false
	newTypes := make([]Type, len(t.Types))
	for i, typ := range t.Types {
		newType := typ.Accept(v)
		if newType != typ {
			changed = true
		}
		newTypes[i] = newType
	}

	var result Type = t
	if changed {
		result = NewIntersectionType(t.provenance, newTypes...)
	}

	if visitResult := v.ExitType(result); visitResult != nil {
		return visitResult
	}
	return result
}

// TODO: handle precedence when printing
func (t *IntersectionType) String() string {
	result := ""
	if len(t.Types) > 0 {
		for i, typ := range t.Types {
			if i > 0 {
				result += " & "
			}
			result += typ.String()
		}
	}
	return result
}

type KeyOfType struct {
	Type       Type
	provenance Provenance
}

func NewKeyOfType(provenance Provenance, typ Type) *KeyOfType {
	return &KeyOfType{
		Type:       typ,
		provenance: provenance,
	}
}
func (t *KeyOfType) Accept(v TypeVisitor) Type {
	if result := v.EnterType(t); result != nil {
		t = result.(*KeyOfType)
	}

	newType := t.Type.Accept(v)
	var result Type = t
	if newType != t.Type {
		result = NewKeyOfType(t.provenance, newType)
	}

	if visitResult := v.ExitType(result); visitResult != nil {
		return visitResult
	}
	return result
}

// TODO: handle precedence when printing
func (t *KeyOfType) String() string {
	return "keyof " + t.Type.String()
}

type TypeOfType struct {
	Ident      QualIdent
	provenance Provenance
}

func NewTypeOfType(provenance Provenance, ident QualIdent) *TypeOfType {
	return &TypeOfType{
		Ident:      ident,
		provenance: provenance,
	}
}

func (t *TypeOfType) Accept(v TypeVisitor) Type {
	if result := v.EnterType(t); result != nil {
		t = result.(*TypeOfType)
	}

	if visitResult := v.ExitType(t); visitResult != nil {
		return visitResult
	}
	return t
}

func (t *TypeOfType) String() string {
	return "typeof " + QualIdentToString(t.Ident)
}

type IndexType struct {
	Target     Type
	Index      Type
	provenance Provenance
}

func NewIndexType(provenance Provenance, target Type, index Type) *IndexType {
	return &IndexType{
		Target:     target,
		Index:      index,
		provenance: provenance,
	}
}
func (t *IndexType) Accept(v TypeVisitor) Type {
	if result := v.EnterType(t); result != nil {
		t = result.(*IndexType)
	}

	newTarget := t.Target.Accept(v)
	newIndex := t.Index.Accept(v)
	var result Type = t
	if newTarget != t.Target || newIndex != t.Index {
		result = NewIndexType(t.provenance, newTarget, newIndex)
	}

	if visitResult := v.ExitType(result); visitResult != nil {
		return visitResult
	}
	return result
}
func (t *IndexType) String() string {
	return t.Target.String() + "[" + t.Index.String() + "]"
}

type CondType struct {
	Check      Type
	Extends    Type
	Then       Type
	Else       Type
	provenance Provenance
}

func (t *CondType) Accept(v TypeVisitor) Type {
	if result := v.EnterType(t); result != nil {
		t = result.(*CondType)
	}

	newCheck := t.Check.Accept(v)
	newExtends := t.Extends.Accept(v)
	newCons := t.Then.Accept(v)
	newAlt := t.Else.Accept(v)
	var result Type = t
	if newCheck != t.Check || newExtends != t.Extends || newCons != t.Then || newAlt != t.Else {
		result = NewCondType(
			t.provenance,
			newCheck,
			newExtends,
			newCons,
			newAlt,
		)
	}

	if visitResult := v.ExitType(result); visitResult != nil {
		return visitResult
	}
	return result
}
func NewCondType(provenance Provenance, check Type, extends Type, cons Type, alt Type) *CondType {
	return &CondType{
		Check:      check,
		Extends:    extends,
		Then:       cons,
		Else:       alt,
		provenance: provenance,
	}
}
func (t *CondType) String() string {
	return "if " + t.Check.String() + " : " + t.Extends.String() + " { " + t.Then.String() + " } else { " + t.Else.String() + " }"
}

type InferType struct {
	Name       string
	provenance Provenance
}

func (t *InferType) Accept(v TypeVisitor) Type {
	if result := v.EnterType(t); result != nil {
		t = result.(*InferType)
	}
	if result := v.ExitType(t); result != nil {
		return result
	}
	return t
}
func (t *InferType) String() string {
	return "infer " + t.Name
}

func NewInferType(provenance Provenance, name string) *InferType {
	return &InferType{
		Name:       name,
		provenance: provenance,
	}
}

type Mutability string

const (
	MutabilityMutable   Mutability = "!"
	MutabilityUncertain Mutability = "?"
)

type MutabilityType struct {
	Type       Type
	Mutability Mutability
	provenance Provenance
}

func (t *MutabilityType) Accept(v TypeVisitor) Type {
	if result := v.EnterType(t); result != nil {
		t = result.(*MutabilityType)
	}

	newType := t.Type.Accept(v)
	var result Type = t
	if newType != t.Type {
		result = &MutabilityType{
			Type:       newType,
			Mutability: t.Mutability,
			provenance: t.provenance,
		}
	}

	if visitResult := v.ExitType(result); visitResult != nil {
		return visitResult
	}
	return result
}
func (t *MutabilityType) String() string {
	switch t.Mutability {
	case MutabilityUncertain:
		return "mut? " + t.Type.String()
	case MutabilityMutable:
		return "mut " + t.Type.String()
	default:
		panic(fmt.Sprintf("unexpected mutability value: %q", t.Mutability))
	}
}

func NewMutableType(provenance Provenance, t Type) *MutabilityType {
	return &MutabilityType{
		Type:       t,
		Mutability: MutabilityMutable,
		provenance: provenance,
	}
}

type WildcardType struct {
	provenance Provenance
}

func NewWildcardType(provenance Provenance) *WildcardType {
	return &WildcardType{
		provenance: provenance,
	}
}
func (t *WildcardType) Accept(v TypeVisitor) Type {
	if result := v.EnterType(t); result != nil {
		t = result.(*WildcardType)
	}
	if result := v.ExitType(t); result != nil {
		return result
	}
	return t
}
func (t *WildcardType) String() string {
	return "_"
}

type ExtractorType struct {
	Extractor  Type
	Args       []Type
	provenance Provenance
}

func NewExtractorType(provenance Provenance, extractor Type, args ...Type) *ExtractorType {
	return &ExtractorType{
		Extractor:  extractor,
		Args:       args,
		provenance: provenance,
	}
}
func (t *ExtractorType) Accept(v TypeVisitor) Type {
	if result := v.EnterType(t); result != nil {
		t = result.(*ExtractorType)
	}

	newExtractor := t.Extractor.Accept(v)
	changed := newExtractor != t.Extractor
	newArgs := make([]Type, len(t.Args))
	for i, arg := range t.Args {
		newArg := arg.Accept(v)
		if newArg != arg {
			changed = true
		}
		newArgs[i] = newArg
	}

	var result Type = t
	if changed {
		result = NewExtractorType(t.provenance, newExtractor, newArgs...)
	}

	if visitResult := v.ExitType(result); visitResult != nil {
		return visitResult
	}
	return result
}
func (t *ExtractorType) String() string {
	result := t.Extractor.String()
	if len(t.Args) > 0 {
		result += "("
		for i, arg := range t.Args {
			if i > 0 {
				result += ", "
			}
			result += arg.String()
		}
		result += ")"
	}
	return result
}

type Quasi struct {
	Value string
}

type TemplateLitType struct {
	Quasis     []*Quasi
	Types      []Type
	provenance Provenance
}

func NewTemplateLitType(provenance Provenance, quasis []*Quasi, types []Type) *TemplateLitType {
	return &TemplateLitType{
		Quasis:     quasis,
		Types:      types,
		provenance: provenance,
	}
}
func (t *TemplateLitType) Accept(v TypeVisitor) Type {
	if result := v.EnterType(t); result != nil {
		t = result.(*TemplateLitType)
	}

	changed := false
	newTypes := make([]Type, len(t.Types))
	for i, typ := range t.Types {
		newType := typ.Accept(v)
		if newType != typ {
			changed = true
		}
		newTypes[i] = newType
	}

	var result Type = t
	if changed {
		result = NewTemplateLitType(t.provenance, t.Quasis, newTypes)
	}

	if visitResult := v.ExitType(result); visitResult != nil {
		return visitResult
	}
	return result
}
func (t *TemplateLitType) String() string {
	result := "`"
	for i, quasi := range t.Quasis {
		result += quasi.Value
		// Add the interpolated type if there is one at this position
		if i < len(t.Types) {
			result += "${" + t.Types[i].String() + "}"
		}
	}
	result += "`"
	return result
}

type IntrinsicType struct {
	Name       string
	provenance Provenance
}

func (t *IntrinsicType) Accept(v TypeVisitor) Type {
	if result := v.EnterType(t); result != nil {
		t = result.(*IntrinsicType)
	}
	if result := v.ExitType(t); result != nil {
		return result
	}
	return t
}
func (t *IntrinsicType) String() string {
	return t.Name
}

// We want to model both `let x = 5` as well as `fn (x: number) => x`
type Binding struct {
	Source  provenance.Provenance // optional
	Type    Type
	Mutable bool
}

// This is similar to Scope, but instead of inheriting from a parent scope,
// the identifiers are fully qualified with their namespace (e.g. "foo.bar.baz").
// This makes it easier to build a dependency graph between declarations within
// the module.
type Namespace struct {
	Values     map[string]*Binding
	Types      map[string]*TypeAlias
	Namespaces map[string]*Namespace
}

func NewNamespace() *Namespace {
	return &Namespace{
		Values:     make(map[string]*Binding),
		Types:      make(map[string]*TypeAlias),
		Namespaces: make(map[string]*Namespace),
	}
}

type NamespaceType struct {
	Namespace  *Namespace
	provenance Provenance
}

func NewNamespaceType(provenance Provenance, ns *Namespace) *NamespaceType {
	return &NamespaceType{
		Namespace:  ns,
		provenance: provenance,
	}
}
func (t *NamespaceType) Accept(v TypeVisitor) Type {
	if result := v.EnterType(t); result != nil {
		t = result.(*NamespaceType)
	}

	changed := false
	newValues := make(map[string]*Binding)
	for name, binding := range t.Namespace.Values {
		newType := binding.Type.Accept(v)
		if newType != binding.Type {
			changed = true
			newValues[name] = &Binding{
				Source:  binding.Source,
				Type:    newType,
				Mutable: binding.Mutable,
			}
		} else {
			newValues[name] = binding
		}
	}

	newTypes := make(map[string]*TypeAlias)
	for name, typeAlias := range t.Namespace.Types {
		newType := typeAlias.Type.Accept(v)
		if newType != typeAlias.Type {
			changed = true
			newTypes[name] = &TypeAlias{
				Type:       newType,
				TypeParams: typeAlias.TypeParams,
			}
		} else {
			newTypes[name] = typeAlias
		}
	}

	var result Type = t
	if changed {
		newNamespace := &Namespace{
			Values:     newValues,
			Types:      newTypes,
			Namespaces: t.Namespace.Namespaces, // Note: not recursing into nested namespaces for simplicity
		}
		result = NewNamespaceType(t.provenance, newNamespace)
	}

	if visitResult := v.ExitType(result); visitResult != nil {
		return visitResult
	}
	return result
}
func (t *NamespaceType) String() string {
	var builder strings.Builder
	builder.WriteString("namespace {")
	if len(t.Namespace.Values) > 0 {
		for name, binding := range t.Namespace.Values {
			builder.WriteString(name)
			builder.WriteString(": ")
			builder.WriteString(binding.Type.String())
			builder.WriteString(", ")
		}
	}
	if len(t.Namespace.Types) > 0 {
		for name, typeAlias := range t.Namespace.Types {
			builder.WriteString(name)
			builder.WriteString(": ")
			builder.WriteString(typeAlias.Type.String())
			builder.WriteString(", ")
		}
	}
	builder.WriteString("}")
	return builder.String()
}

func regexEqual(x, y *regexp.Regexp) bool {
	if x == nil || y == nil {
		return x == y
	}
	return x.String() == y.String()
}

func Equals(t1 Type, t2 Type) bool {
	return cmp.Equal(t1, t2,
		cmpopts.IgnoreUnexported(
			// nolint:exhaustruct
			TypeVarType{}, TypeRefType{}, PrimType{}, RegexType{}, regexp.Regexp{}, LitType{},
			// nolint:exhaustruct
			UniqueSymbolType{}, UnknownType{}, NeverType{}, AnyType{}, GlobalThisType{},
			// nolint:exhaustruct
			FuncType{}, ObjectType{}, TupleType{}, RestSpreadType{}, UnionType{},
			// nolint:exhaustruct
			IntersectionType{}, KeyOfType{}, IndexType{}, CondType{}, InferType{},
			// nolint:exhaustruct
			MutabilityType{}, WildcardType{}, ExtractorType{}, TemplateLitType{},
			// nolint:exhaustruct
			IntrinsicType{}, NamespaceType{}, MappedElem{}, Ident{}, Member{},
		),
		cmp.Comparer(regexEqual),
	)
}
