//go:generate go run ../../tools/gen_types/gen_types.go

package type_system

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/escalier-lang/escalier/internal/provenance"
	. "github.com/escalier-lang/escalier/internal/provenance"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

//sumtype:decl
type Type interface {
	isType()
	Provenance() Provenance
	SetProvenance(Provenance)
	Equal(Type) bool
	Accept(TypeVisitor) Type
	String() string
	// WithProvenance returns a new Type with the given Provenance.
	// It's essentially a shallow copy of the Type with the new Provenance.
	WithProvenance(Provenance) Type
}

func (*TypeVarType) isType()      {}
func (*TypeRefType) isType()      {}
func (*PrimType) isType()         {}
func (*LitType) isType()          {}
func (*UniqueSymbolType) isType() {}
func (*UnknownType) isType()      {}
func (*NeverType) isType()        {}
func (*AnyType) isType()          {}
func (*GlobalThisType) isType()   {}
func (*FuncType) isType()         {}
func (*ObjectType) isType()       {}
func (*TupleType) isType()        {}
func (*RestSpreadType) isType()   {}
func (*UnionType) isType()        {}
func (*IntersectionType) isType() {}
func (*KeyOfType) isType()        {}
func (*IndexType) isType()        {}
func (*CondType) isType()         {}
func (*InferType) isType()        {}
func (*MutableType) isType()      {}
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
	ID         int
	Instance   Type
	provenance Provenance
}

func (t *TypeVarType) Equal(other Type) bool {
	if other, ok := other.(*TypeVarType); ok {
		return t.ID == other.ID
	}
	return false
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
	return "t" + fmt.Sprint(t.ID)
}

type TypeAlias struct {
	Type       Type
	TypeParams []*TypeParam
}

type TypeRefType struct {
	Name       string // TODO: Make this a qualified identifier
	TypeArgs   []Type
	TypeAlias  *TypeAlias // optional, resolved type alias (definition)
	provenance Provenance
}

func NewTypeRefType(name string, typeAlias *TypeAlias, typeArgs ...Type) *TypeRefType {
	return &TypeRefType{
		Name:       name,
		TypeArgs:   typeArgs,
		TypeAlias:  typeAlias,
		provenance: nil,
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
		result = &TypeRefType{
			Name:       t.Name,
			TypeArgs:   newTypeArgs,
			TypeAlias:  t.TypeAlias,
			provenance: t.provenance,
		}
	}

	if visitResult := v.ExitType(result); visitResult != nil {
		return visitResult
	}
	return result
}
func (t *TypeRefType) Equal(other Type) bool {
	if other, ok := other.(*TypeRefType); ok {
		// nolint: exhaustruct
		return cmp.Equal(t, other, cmpopts.IgnoreFields(TypeRefType{}, "provenance"))
	}
	return false
}
func (t *TypeRefType) String() string {
	result := t.Name
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

func NewNumType() *PrimType {
	return &PrimType{
		Prim:       NumPrim,
		provenance: nil,
	}
}
func NewStrType() *PrimType {
	return &PrimType{
		Prim:       StrPrim,
		provenance: nil,
	}
}
func NewBoolType() *PrimType {
	return &PrimType{
		Prim:       BoolPrim,
		provenance: nil,
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
func (t *PrimType) Equal(other Type) bool {
	if other, ok := other.(*PrimType); ok {
		return t.Prim == other.Prim
	}
	return false
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

func NewRegexType(pattern string) (Type, error) {
	// parse the pattern as a regular expression

	pattern, err := convertJSRegexToGo(pattern)
	if err != nil {
		return NewNeverType(), fmt.Errorf("failed to convert regex: %v", err)
	}

	regex := regexp.MustCompile(pattern)

	groups := make(map[string]Type)
	if regex != nil {
		for _, name := range regex.SubexpNames()[1:] {
			if name != "" { // Skip unnamed groups
				groups[name] = NewStrType()
			}
		}
	}

	return &RegexType{
		Regex:      regex,
		Groups:     groups,
		provenance: nil,
	}, nil
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
func (t *RegexType) Equal(other Type) bool {
	if other, ok := other.(*RegexType); ok {
		// Compare the regex patterns as strings since regexp.Regexp doesn't have value equality
		return t.Regex.String() == other.Regex.String()
	}
	return false
}
func (t *RegexType) String() string {
	return t.Regex.String()
}

type LitType struct {
	Lit        Lit
	provenance Provenance
}

func NewLitType(lit Lit) *LitType {
	return &LitType{
		Lit:        lit,
		provenance: nil,
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
func (t *LitType) Equal(other Type) bool {
	if other, ok := other.(*LitType); ok {
		return t.Lit.Equal(other.Lit)
	}
	return false
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

func (t *UniqueSymbolType) Accept(v TypeVisitor) Type {
	if result := v.EnterType(t); result != nil {
		t = result.(*UniqueSymbolType)
	}
	if result := v.ExitType(t); result != nil {
		return result
	}
	return t
}
func (t *UniqueSymbolType) Equal(other Type) bool {
	if other, ok := other.(*UniqueSymbolType); ok {
		return t.Value == other.Value
	}
	return false
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

func NewUnknownType() *UnknownType { return &UnknownType{provenance: nil} }
func (t *UnknownType) Equal(other Type) bool {
	if _, ok := other.(*UnknownType); ok {
		return true
	}
	return false
}
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

func NewNeverType() *NeverType { return &NeverType{provenance: nil} }
func (t *NeverType) Equal(other Type) bool {
	if _, ok := other.(*NeverType); ok {
		return true
	}
	return false
}
func (t *NeverType) String() string {
	return "never"
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

func NewAnyType() *AnyType { return &AnyType{provenance: nil} }
func (t *AnyType) Equal(other Type) bool {
	if _, ok := other.(*AnyType); ok {
		return true
	}
	return false
}
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
func (t *GlobalThisType) Equal(other Type) bool {
	if _, ok := other.(*GlobalThisType); ok {
		return true
	}
	return false
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
	Self       Type // optional, used for methods only
	Params     []*FuncParam
	Return     Type
	Throws     Type
	provenance Provenance
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
		result = &FuncType{
			TypeParams: t.TypeParams,
			Self:       t.Self,
			Params:     newParams,
			Return:     newReturn,
			Throws:     newThrows,
			provenance: t.provenance,
		}
	}

	if visitResult := v.ExitType(result); visitResult != nil {
		return visitResult
	}
	return result
}
func (t *FuncType) Equal(other Type) bool {
	if other, ok := other.(*FuncType); ok {
		// nolint: exhaustruct
		return cmp.Equal(t, other, cmpopts.IgnoreFields(FuncType{}, "provenance"))
	}
	return false
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
			result += param.Pattern.String() + ": " + param.Type.String()
		}
	}
	result += ")"
	if t.Return != nil {
		result += " -> " + t.Return.String()
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

type CallableElemType struct{ Fn *FuncType }
type ConstructorElemType struct{ Fn *FuncType }
type MethodElemType struct {
	Name ObjTypeKey
	Fn   *FuncType
}
type GetterElemType struct {
	Name ObjTypeKey
	Fn   *FuncType
}
type SetterElemType struct {
	Name ObjTypeKey
	Fn   *FuncType
}
type PropertyElemType struct {
	Name     ObjTypeKey
	Optional bool
	Readonly bool
	Value    Type
}

func NewPropertyElemType(name ObjTypeKey, value Type) *PropertyElemType {
	return &PropertyElemType{
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

type MappedElemType struct {
	TypeParam *IndexParamType
	// TODO: rename this so that we can differentiate between this and the
	// Name() method thats common to all ObjTypeElems.
	name     Type // optional
	Value    Type
	Optional *MappedModifier // TODO: replace with `?`, `!`, or nothing
	ReadOnly *MappedModifier
}
type IndexParamType struct {
	Name       string
	Constraint Type
}
type RestSpreadElemType struct{ Value Type }

func NewRestSpreadElemType(value Type) *RestSpreadElemType {
	return &RestSpreadElemType{
		Value: value,
	}
}

func (*CallableElemType) isObjTypeElem()    {}
func (*ConstructorElemType) isObjTypeElem() {}
func (*MethodElemType) isObjTypeElem()      {}
func (*GetterElemType) isObjTypeElem()      {}
func (*SetterElemType) isObjTypeElem()      {}
func (*PropertyElemType) isObjTypeElem()    {}
func (*MappedElemType) isObjTypeElem()      {}
func (*RestSpreadElemType) isObjTypeElem()  {}

func (c *CallableElemType) Accept(v TypeVisitor) ObjTypeElem {
	newFn := c.Fn.Accept(v).(*FuncType)
	if newFn != c.Fn {
		return &CallableElemType{Fn: newFn}
	}
	return c
}
func (c *ConstructorElemType) Accept(v TypeVisitor) ObjTypeElem {
	newFn := c.Fn.Accept(v).(*FuncType)
	if newFn != c.Fn {
		return &ConstructorElemType{Fn: newFn}
	}
	return c
}
func (m *MethodElemType) Accept(v TypeVisitor) ObjTypeElem {
	newFn := m.Fn.Accept(v).(*FuncType)
	if newFn != m.Fn {
		return &MethodElemType{Name: m.Name, Fn: newFn}
	}
	return m
}
func (g *GetterElemType) Accept(v TypeVisitor) ObjTypeElem {
	newFn := g.Fn.Accept(v).(*FuncType)
	if newFn != g.Fn {
		return &GetterElemType{Name: g.Name, Fn: newFn}
	}
	return g
}
func (s *SetterElemType) Accept(v TypeVisitor) ObjTypeElem {
	newFn := s.Fn.Accept(v).(*FuncType)
	if newFn != s.Fn {
		return &SetterElemType{Name: s.Name, Fn: newFn}
	}
	return s
}
func (p *PropertyElemType) Accept(v TypeVisitor) ObjTypeElem {
	newValue := p.Value.Accept(v)
	if newValue != p.Value {
		return &PropertyElemType{
			Name:     p.Name,
			Optional: p.Optional,
			Readonly: p.Readonly,
			Value:    newValue,
		}
	}
	return p
}
func (m *MappedElemType) Accept(v TypeVisitor) ObjTypeElem {
	changed := false
	newConstraint := m.TypeParam.Constraint.Accept(v)
	if newConstraint != m.TypeParam.Constraint {
		changed = true
	}

	var newName Type
	if m.name != nil {
		newName = m.name.Accept(v)
		if newName != m.name {
			changed = true
		}
	}

	newValue := m.Value.Accept(v)
	if newValue != m.Value {
		changed = true
	}

	if changed {
		newTypeParam := &IndexParamType{
			Name:       m.TypeParam.Name,
			Constraint: newConstraint,
		}
		return &MappedElemType{
			TypeParam: newTypeParam,
			name:      newName,
			Value:     newValue,
			Optional:  m.Optional,
			ReadOnly:  m.ReadOnly,
		}
	}
	return m
}
func (r *RestSpreadElemType) Accept(v TypeVisitor) ObjTypeElem {
	newValue := r.Value.Accept(v)
	if newValue != r.Value {
		return &RestSpreadElemType{Value: newValue}
	}
	return r
}

type ObjectType struct {
	Elems      []ObjTypeElem
	Exact      bool // Can't be true if any of Interface, Implements, or Extends are true
	Immutable  bool // true for `#{...}`, false for `{...}`
	Mutable    bool // true for `mut {...}`, false for `{...}`
	Nominal    bool // true for classes
	Interface  bool
	Extends    []*TypeRefType
	Implements []*TypeRefType
	provenance Provenance // TODO: use optional.Option for this
}

// TODO: add different constructors for different types of object types
func NewObjectType(elems []ObjTypeElem) *ObjectType {
	return &ObjectType{
		Elems:      elems,
		Exact:      false,
		Immutable:  false,
		Mutable:    false,
		Nominal:    false,
		Interface:  false,
		Extends:    nil,
		Implements: nil,
		provenance: nil,
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

	var result Type = t
	if changed {
		result = &ObjectType{
			Elems:      newElems,
			Exact:      t.Exact,
			Immutable:  t.Immutable,
			Mutable:    t.Mutable,
			Nominal:    t.Nominal,
			Interface:  t.Interface,
			Extends:    newExtends,
			Implements: newImplements,
			provenance: t.provenance,
		}
	}

	if visitResult := v.ExitType(result); visitResult != nil {
		return visitResult
	}
	return result
}
func (t *ObjectType) Equal(other Type) bool {
	if other, ok := other.(*ObjectType); ok {
		// nolint: exhaustruct
		return cmp.Equal(t, other, cmpopts.IgnoreFields(ObjectType{}, "provenance"))
	}
	return false
}
func (t *ObjectType) String() string {
	result := "{"
	if len(t.Elems) > 0 {
		for i, elem := range t.Elems {
			if i > 0 {
				result += ", "
			}
			switch elem := elem.(type) {
			case *CallableElemType:
				result += elem.Fn.String()
			case *ConstructorElemType:
				result += "new " + elem.Fn.String()
			case *MethodElemType:
				result += elem.Name.String() + ": " + elem.Fn.String()
			case *GetterElemType:
				result += "get " + elem.Name.String() + ": " + elem.Fn.String()
			case *SetterElemType:
				result += "set " + elem.Name.String() + ": " + elem.Fn.String()
			case *PropertyElemType:
				result += elem.Name.String()
				if elem.Optional {
					result += "?"
				}
				result += ": " + elem.Value.String()
			case *MappedElemType:
				// TODO: handle renaming
				// TODO: handle optional and readonly
				result += "[" + elem.TypeParam.Name + " in " + elem.TypeParam.Constraint.String() + "]"
				result += ": " + elem.Value.String()
			case *RestSpreadElemType:
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

func NewTupleType(elems ...Type) *TupleType {
	return &TupleType{
		Elems:      elems,
		provenance: nil,
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
		result = &TupleType{
			Elems:      newElems,
			provenance: t.provenance,
		}
	}

	if visitResult := v.ExitType(result); visitResult != nil {
		return visitResult
	}
	return result
}
func (t *TupleType) Equal(other Type) bool {
	if other, ok := other.(*TupleType); ok {
		// nolint: exhaustruct
		return cmp.Equal(t, other, cmpopts.IgnoreFields(TupleType{}, "provenance"))
	}
	return false
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

func NewRestSpreadType(typ Type) *RestSpreadType {
	return &RestSpreadType{
		Type:       typ,
		provenance: nil,
	}
}
func (t *RestSpreadType) Accept(v TypeVisitor) Type {
	if result := v.EnterType(t); result != nil {
		t = result.(*RestSpreadType)
	}

	newType := t.Type.Accept(v)
	var result Type = t
	if newType != t.Type {
		result = &RestSpreadType{
			Type:       newType,
			provenance: t.provenance,
		}
	}

	if visitResult := v.ExitType(result); visitResult != nil {
		return visitResult
	}
	return result
}
func (t *RestSpreadType) Equal(other Type) bool {
	if other, ok := other.(*RestSpreadType); ok {
		return t.Type.Equal(other.Type)
	}
	return false
}
func (t *RestSpreadType) String() string {
	return "..." + t.Type.String()
}

type UnionType struct {
	Types      []Type
	provenance Provenance
}

func NewUnionType(types ...Type) Type {
	if len(types) == 0 {
		return NewNeverType()
	}
	if len(types) == 1 {
		return types[0]
	}
	return &UnionType{
		Types:      types,
		provenance: nil,
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
		result = &UnionType{
			Types:      newTypes,
			provenance: t.provenance,
		}
	}

	if visitResult := v.ExitType(result); visitResult != nil {
		return visitResult
	}
	return result
}
func (t *UnionType) Equal(other Type) bool {
	if other, ok := other.(*UnionType); ok {
		// nolint: exhaustruct
		return cmp.Equal(t, other, cmpopts.IgnoreFields(UnionType{}, "provenance"))
	}
	return false
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

func NewIntersectionType(types ...Type) *IntersectionType {
	return &IntersectionType{
		Types:      types,
		provenance: nil,
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
		result = &IntersectionType{
			Types:      newTypes,
			provenance: t.provenance,
		}
	}

	if visitResult := v.ExitType(result); visitResult != nil {
		return visitResult
	}
	return result
}
func (t *IntersectionType) Equal(other Type) bool {
	if other, ok := other.(*IntersectionType); ok {
		// nolint: exhaustruct
		return cmp.Equal(t, other, cmpopts.IgnoreFields(IntersectionType{}, "provenance"))
	}
	return false
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

func (t *KeyOfType) Accept(v TypeVisitor) Type {
	if result := v.EnterType(t); result != nil {
		t = result.(*KeyOfType)
	}

	newType := t.Type.Accept(v)
	var result Type = t
	if newType != t.Type {
		result = &KeyOfType{
			Type:       newType,
			provenance: t.provenance,
		}
	}

	if visitResult := v.ExitType(result); visitResult != nil {
		return visitResult
	}
	return result
}
func (t *KeyOfType) Equal(other Type) bool {
	if other, ok := other.(*KeyOfType); ok {
		return t.Type.Equal(other.Type)
	}
	return false
}

// TODO: handle precedence when printing
func (t *KeyOfType) String() string {
	return "keyof " + t.Type.String()
}

type IndexType struct {
	Target     Type
	Index      Type
	provenance Provenance
}

func (t *IndexType) Accept(v TypeVisitor) Type {
	if result := v.EnterType(t); result != nil {
		t = result.(*IndexType)
	}

	newTarget := t.Target.Accept(v)
	newIndex := t.Index.Accept(v)
	var result Type = t
	if newTarget != t.Target || newIndex != t.Index {
		result = &IndexType{
			Target:     newTarget,
			Index:      newIndex,
			provenance: t.provenance,
		}
	}

	if visitResult := v.ExitType(result); visitResult != nil {
		return visitResult
	}
	return result
}
func (t *IndexType) Equal(other Type) bool {
	if other, ok := other.(*IndexType); ok {
		// nolint: exhaustruct
		return cmp.Equal(t, other, cmpopts.IgnoreFields(IndexType{}, "provenance"))
	}
	return false
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
		result = &CondType{
			Check:      newCheck,
			Extends:    newExtends,
			Then:       newCons,
			Else:       newAlt,
			provenance: t.provenance,
		}
	}

	if visitResult := v.ExitType(result); visitResult != nil {
		return visitResult
	}
	return result
}
func (t *CondType) Equal(other Type) bool {
	if other, ok := other.(*CondType); ok {
		// nolint: exhaustruct
		return cmp.Equal(t, other, cmpopts.IgnoreFields(CondType{}, "provenance"))
	}
	return false
}
func NewCondType(check Type, extends Type, cons Type, alt Type) *CondType {
	return &CondType{
		Check:      check,
		Extends:    extends,
		Then:       cons,
		Else:       alt,
		provenance: nil,
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
func (t *InferType) Equal(other Type) bool {
	if other, ok := other.(*InferType); ok {
		return t.Name == other.Name
	}
	return false
}
func (t *InferType) String() string {
	return "infer " + t.Name
}

func NewInferType(name string) *InferType {
	return &InferType{
		Name:       name,
		provenance: nil,
	}
}

type MutableType struct {
	Type       Type
	provenance Provenance
}

func (t *MutableType) Accept(v TypeVisitor) Type {
	if result := v.EnterType(t); result != nil {
		t = result.(*MutableType)
	}

	newType := t.Type.Accept(v)
	var result Type = t
	if newType != t.Type {
		result = &MutableType{
			Type:       newType,
			provenance: t.provenance,
		}
	}

	if visitResult := v.ExitType(result); visitResult != nil {
		return visitResult
	}
	return result
}
func (t *MutableType) Equal(other Type) bool {
	if other, ok := other.(*MutableType); ok {
		return t.Type.Equal(other.Type)
	}
	return false
}
func (t *MutableType) String() string {
	return "mut " + t.Type.String()
}

func NewMutableType(typ Type) *MutableType {
	return &MutableType{
		Type:       typ,
		provenance: nil,
	}
}

type WildcardType struct {
	provenance Provenance
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
func (t *WildcardType) Equal(other Type) bool {
	if _, ok := other.(*WildcardType); ok {
		return true
	}
	return false
}
func (t *WildcardType) String() string {
	return "_"
}

type ExtractorType struct {
	Extractor  Type
	Args       []Type
	provenance Provenance
}

func NewExtractorType(extractor Type, args ...Type) *ExtractorType {
	return &ExtractorType{
		Extractor:  extractor,
		Args:       args,
		provenance: nil,
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
		result = &ExtractorType{
			Extractor:  newExtractor,
			Args:       newArgs,
			provenance: t.provenance,
		}
	}

	if visitResult := v.ExitType(result); visitResult != nil {
		return visitResult
	}
	return result
}
func (t *ExtractorType) Equal(other Type) bool {
	if other, ok := other.(*ExtractorType); ok {
		// nolint: exhaustruct
		return cmp.Equal(t, other, cmpopts.IgnoreFields(ExtractorType{}, "provenance"))
	}
	return false
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
		result = &TemplateLitType{
			Quasis:     t.Quasis,
			Types:      newTypes,
			provenance: t.provenance,
		}
	}

	if visitResult := v.ExitType(result); visitResult != nil {
		return visitResult
	}
	return result
}
func (t *TemplateLitType) Equal(other Type) bool {
	if other, ok := other.(*TemplateLitType); ok {
		// nolint: exhaustruct
		return cmp.Equal(t, other, cmpopts.IgnoreFields(TemplateLitType{}, "provenance"))
	}
	return false
}
func (t *TemplateLitType) String() string {
	result := "`"
	if len(t.Quasis) > 0 {
		for i, quasi := range t.Quasis {
			if i > 0 {
				result += "${" + quasi.Value + "}"
			}
			result += quasi.Value
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
func (t *IntrinsicType) Equal(other Type) bool {
	if other, ok := other.(*IntrinsicType); ok {
		return t.Name == other.Name
	}
	return false
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
		result = &NamespaceType{
			Namespace:  newNamespace,
			provenance: t.provenance,
		}
	}

	if visitResult := v.ExitType(result); visitResult != nil {
		return visitResult
	}
	return result
}
func (t *NamespaceType) Equal(other Type) bool {
	if other, ok := other.(*NamespaceType); ok {
		// nolint: exhaustruct
		return cmp.Equal(t, other, cmpopts.IgnoreFields(NamespaceType{}, "provenance"))
	}
	return false
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
