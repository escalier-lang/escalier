//go:generate go run ../../tools/gen_types/gen_types.go

package type_system

import (
	"fmt"
	"strconv"

	. "github.com/escalier-lang/escalier/internal/provenance"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/moznion/go-optional"
)

//sumtype:decl
type Type interface {
	isType()
	Provenance() Provenance
	SetProvenance(Provenance)
	Equal(Type) bool
	Accept(TypeVisitor)
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
func (*WildcardType) isType()     {}
func (*ExtractorType) isType()    {}
func (*TemplateLitType) isType()  {}
func (*IntrinsicType) isType()    {}

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
func (t *TypeVarType) Accept(v TypeVisitor) {
	v.VisitType(Prune(t))
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
func (t *TypeRefType) Accept(v TypeVisitor) { v.VisitType(t) }
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
func (t *PrimType) Accept(v TypeVisitor) { v.VisitType(t) }
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
func (t *LitType) Accept(v TypeVisitor) { v.VisitType(t) }
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

func (t *UniqueSymbolType) Accept(v TypeVisitor) { v.VisitType(t) }
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

func NewUnknownType() *UnknownType          { return &UnknownType{provenance: nil} }
func (t *UnknownType) Accept(v TypeVisitor) { v.VisitType(t) }
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

func NewNeverType() *NeverType            { return &NeverType{provenance: nil} }
func (t *NeverType) Accept(v TypeVisitor) { v.VisitType(t) }
func (t *NeverType) Equal(other Type) bool {
	if _, ok := other.(*NeverType); ok {
		return true
	}
	return false
}
func (t *NeverType) String() string {
	return "never"
}

type GlobalThisType struct {
	provenance Provenance
}

func (t *GlobalThisType) Accept(v TypeVisitor) { v.VisitType(t) }
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
	Self       optional.Option[Type]
	Params     []*FuncParam
	Return     Type
	Throws     Type
	provenance Provenance
}

func (t *FuncType) Accept(v TypeVisitor) {
	for _, param := range t.Params {
		param.Type.Accept(v)
	}
	if t.Return != nil {
		t.Return.Accept(v)
	}
	if t.Throws != nil {
		t.Throws.Accept(v)
	}
	v.VisitType(t)
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
	Accept(TypeVisitor)
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

func (c *CallableElemType) Accept(v TypeVisitor) {
	c.Fn.Accept(v)
}
func (c *ConstructorElemType) Accept(v TypeVisitor) {
	c.Fn.Accept(v)
}
func (m *MethodElemType) Accept(v TypeVisitor) {
	m.Fn.Accept(v)
}
func (g *GetterElemType) Accept(v TypeVisitor) {
	g.Fn.Accept(v)
}
func (s *SetterElemType) Accept(v TypeVisitor) {
	s.Fn.Accept(v)
}
func (p *PropertyElemType) Accept(v TypeVisitor) {
	p.Value.Accept(v)
}
func (m *MappedElemType) Accept(v TypeVisitor) {
	m.TypeParam.Constraint.Accept(v)
	if m.name != nil {
		m.name.Accept(v)
	}
	m.Value.Accept(v)
}
func (r *RestSpreadElemType) Accept(v TypeVisitor) {
	r.Value.Accept(v)
}

type ObjectType struct {
	Elems      []ObjTypeElem
	Exact      bool // Can't be true if any of Interface, Implements, or Extends are true
	Immutable  bool // true for `#{...}`, false for `{...}`
	Mutable    bool // true for `mut {...}`, false for `{...}`
	Nomimal    bool // true for classes
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
		Nomimal:    false,
		Interface:  false,
		Extends:    nil,
		Implements: nil,
		provenance: nil,
	}
}

func (t *ObjectType) Accept(v TypeVisitor) {
	for _, elem := range t.Elems {
		elem.Accept(v)
	}
	for _, ext := range t.Extends {
		ext.Accept(v)
	}
	for _, impl := range t.Implements {
		impl.Accept(v)
	}
	v.VisitType(t)
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
				result += "call: " + elem.Fn.String()
			case *ConstructorElemType:
				result += "construct: " + elem.Fn.String()
			case *MethodElemType:
				result += elem.Name.String() + ": " + elem.Fn.String()
			case *GetterElemType:
				result += "get " + elem.Name.String() + ": " + elem.Fn.String()
			case *SetterElemType:
				result += "set " + elem.Name.String() + ": " + elem.Fn.String()
			case *PropertyElemType:
				result += elem.Name.String() + ": " + elem.Value.String()
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
func (t *TupleType) Accept(v TypeVisitor) {
	for _, elem := range t.Elems {
		elem.Accept(v)
	}
	v.VisitType(t)
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
func (t *RestSpreadType) Accept(v TypeVisitor) {
	v.VisitType(t)
	t.Type.Accept(v)
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
func (t *UnionType) Accept(v TypeVisitor) {
	for _, typ := range t.Types {
		typ.Accept(v)
	}
	v.VisitType(t)
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
func (t *IntersectionType) Accept(v TypeVisitor) {
	for _, typ := range t.Types {
		typ.Accept(v)
	}
	v.VisitType(t)
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

func (t *KeyOfType) Accept(v TypeVisitor) {
	t.Type.Accept(v)
	v.VisitType(t)
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

func (t *IndexType) Accept(v TypeVisitor) {
	t.Target.Accept(v)
	t.Index.Accept(v)
	v.VisitType(t)
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
	Cons       Type
	Alt        Type
	provenance Provenance
}

func (t *CondType) Accept(v TypeVisitor) {
	t.Check.Accept(v)
	t.Extends.Accept(v)
	t.Cons.Accept(v)
	t.Alt.Accept(v)
	v.VisitType(t)
}
func (t *CondType) Equal(other Type) bool {
	if other, ok := other.(*CondType); ok {
		// nolint: exhaustruct
		return cmp.Equal(t, other, cmpopts.IgnoreFields(CondType{}, "provenance"))
	}
	return false
}
func (t *CondType) String() string {
	return "if " + t.Check.String() + " : " + t.Extends.String() + " { " + t.Cons.String() + " } else { " + t.Alt.String() + " }"
}

type InferType struct {
	Name       string
	provenance Provenance
}

func (t *InferType) Accept(v TypeVisitor) { v.VisitType(t) }
func (t *InferType) Equal(other Type) bool {
	if other, ok := other.(*InferType); ok {
		return t.Name == other.Name
	}
	return false
}
func (t *InferType) String() string {
	return "infer " + t.Name
}

type WildcardType struct {
	provenance Provenance
}

func (t *WildcardType) Accept(v TypeVisitor) { v.VisitType(t) }
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
func (t *ExtractorType) Accept(v TypeVisitor) {
	t.Extractor.Accept(v)
	for _, arg := range t.Args {
		arg.Accept(v)
	}
	v.VisitType(t)
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

func (t *TemplateLitType) Accept(v TypeVisitor) {
	for _, typ := range t.Types {
		typ.Accept(v)
	}
	v.VisitType(t)
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

func (t *IntrinsicType) Accept(v TypeVisitor) { v.VisitType(t) }
func (t *IntrinsicType) Equal(other Type) bool {
	if other, ok := other.(*IntrinsicType); ok {
		return t.Name == other.Name
	}
	return false
}
func (t *IntrinsicType) String() string {
	return t.Name
}
