//go:generate go run ../../tools/gen_types/gen_types.go

package type_system

import (
	"fmt"
	"math/big"
	"regexp"
	"strconv"

	"github.com/escalier-lang/escalier/internal/provenance"
	. "github.com/escalier-lang/escalier/internal/provenance"
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
	Equals(Type) bool
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
func (*ErrorType) isType()        {}

func Prune(t Type) Type {
	tv, ok := t.(*TypeVarType)
	if !ok || tv.Instance == nil {
		return t
	}

	// Record the alias chain for Widenable TypeVars before path compression
	// destroys the links. The chain is used by the widening fallback in
	// unifyInner to update all aliased property TypeVars atomically.
	if _, isTV := tv.Instance.(*TypeVarType); isTV && tv.Widenable {
		recordInstanceChain(tv)
	}

	// Path compression: follow the Instance chain to the terminal type and
	// point every intermediate node directly at it. Stops before the terminal
	// to avoid a self-loop when the terminal is an unbound TypeVar.
	concrete := tv.Instance
	for {
		next, ok := concrete.(*TypeVarType)
		if !ok || next.Instance == nil {
			break
		}
		concrete = next.Instance
	}
	for cur := tv; cur != concrete; {
		next := cur.Instance
		cur.Instance = concrete
		nextTV, ok := next.(*TypeVarType)
		if !ok {
			break
		}
		cur = nextTV
	}
	return concrete
}

// recordInstanceChain walks the alias chain starting from tv and records all
// Widenable TypeVars in InstanceChain fields. Each node gets a suffix view of
// the same chain so that the widening fallback can update all aliases.
//
// The chain is rebuilt when Instance is a TypeVar even if InstanceChain is
// already set — the chain may have grown if a tail node was re-aliased to
// another TypeVar after the initial Prune.
func recordInstanceChain(tv *TypeVarType) {
	chain := []*TypeVarType{tv}
	current := tv.Instance
	for {
		next, ok := current.(*TypeVarType)
		if !ok {
			break
		}
		// Reuse a previously computed chain when it's still valid.
		// A cached chain becomes stale when bind() re-aliases the node
		// to another TypeVar after the chain was recorded — detected by
		// checking whether Instance is currently a TypeVar. If stale,
		// we continue walking to discover the new tail nodes.
		_, nextIsTV := next.Instance.(*TypeVarType)
		if next.InstanceChain != nil && !nextIsTV {
			chain = append(chain, next.InstanceChain...)
			break
		}
		chain = append(chain, next)
		if next.Instance == nil {
			break
		}
		current = next.Instance
	}
	for i, member := range chain {
		// Only set InstanceChain if the new suffix is longer, to
		// avoid truncating a prior chain.
		suffix := chain[i:]
		if len(suffix) > len(member.InstanceChain) {
			member.InstanceChain = suffix
		}
	}
}

// ArrayConstraint tracks numeric indexing patterns on a type variable before
// committing to tuple vs. array. It is stored on TypeVarType and resolved
// during closeOpenParams.
//
// This deferred approach is used instead of an Open field on TupleType (like
// ObjectType has) because the tuple-vs-array decision depends on the full set
// of access patterns: literal indexes produce tuples, while non-literal indexes
// or mutating methods (.push, .pop) force mut Array<T>, and read-only methods
// (.map, .filter) without literal indexes force Array<T>. An open TupleType
// couldn't represent "might actually be an Array or mut Array".
type ArrayConstraint struct {
	LiteralIndexes     map[int]Type   // index → element type variable
	HasNonLiteralIndex bool           // true if items[i] used with non-literal number type
	HasMutatingMethod  bool           // true if .push(), .pop(), etc. called
	HasReadOnlyMethod  bool           // true if .map(), .filter(), etc. called
	HasIndexAssignment bool           // true if items[i] = value used
	ElemTypeVar        Type           // fresh T for Array<T> (union accumulator)
	MethodElemVars     []*TypeVarType // per-call fresh elem vars from method calls (e.g. .push(), .unshift())
}

type TypeVarType struct {
	ID              int
	Instance        Type
	Constraint      Type
	Default         Type
	FromBinding     bool
	Widenable       bool             // true for type vars whose type is inferred from usage (e.g. property values on open objects)
	IsParam         bool             // true for type vars created for unannotated function parameters
	InstanceChain   []*TypeVarType   // populated by Prune when this TypeVar's Instance is another TypeVar (alias chain from bind); stores all TypeVars in the chain before path compression collapses it
	IsObjectRest    bool             // true for type vars created for object rest patterns (e.g. {a, ...rest})
	ArrayConstraint *ArrayConstraint // non-nil when numeric indexing has been observed; resolved during closing
	provenance      Provenance
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
	enter := v.EnterType(t)
	if enter.Type != nil {
		t = enter.Type.(*TypeVarType)
	}
	if enter.SkipChildren {
		if result := v.ExitType(t); result != nil {
			return result
		}
		return t
	}

	prunedType := Prune(t)
	if prunedType != t {
		return prunedType.Accept(v) // Accept on the pruned type
	}

	if result := v.ExitType(t); result != nil {
		return result
	}
	return t
}
func (t *TypeVarType) Equals(other Type) bool {
	if other, ok := other.(*TypeVarType); ok {
		if t.ID != other.ID {
			return false
		}
		if t.FromBinding != other.FromBinding {
			return false
		}
		if !equals(t.Instance, other.Instance) {
			return false
		}
		if !equals(t.Constraint, other.Constraint) {
			return false
		}
		if !equals(t.Default, other.Default) {
			return false
		}
		return true
	}
	return false
}

func (t *TypeVarType) String() string {
	return PrintType(t, PrintConfig{})
}

type TypeAlias struct {
	Type           Type
	TypeParams     []*TypeParam
	LifetimeParams []*LifetimeVar // e.g. ['a, 'b] for Pair<'a, 'b>
	Exported       bool
	IsTypeParam    bool // true for type parameter scope entries, not real aliases
}

type TypeRefType struct {
	Name         QualIdent
	TypeArgs     []Type
	TypeAlias    *TypeAlias // optional, resolved type alias (definition)
	Lifetime     Lifetime   // nil if no lifetime annotation (e.g. 'a Point)
	LifetimeArgs []Lifetime // lifetime arguments for constructed types (e.g. Container<'a>)
	provenance   Provenance
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
	enter := v.EnterType(t)
	if enter.Type != nil {
		switch r := enter.Type.(type) {
		case *TypeRefType:
			t = r
		default:
			if enter.SkipChildren {
				if visitResult := v.ExitType(r); visitResult != nil {
					return visitResult
				}
				return r
			}
			return r.Accept(v)
		}
	}
	if enter.SkipChildren {
		if visitResult := v.ExitType(t); visitResult != nil {
			return visitResult
		}
		return t
	}

	newTypeArgs, changed := CowAcceptTypes(t.TypeArgs, v)

	var result Type = t
	if changed {
		r := NewTypeRefTypeFromQualIdent(t.provenance, t.Name, t.TypeAlias, newTypeArgs...)
		r.Lifetime = t.Lifetime
		r.LifetimeArgs = t.LifetimeArgs
		result = r
	}

	if visitResult := v.ExitType(result); visitResult != nil {
		return visitResult
	}
	return result
}
func (t *TypeRefType) Equals(other Type) bool {
	if other, ok := other.(*TypeRefType); ok {
		if !qualIdentEquals(t.Name, other.Name) {
			return false
		}
		if len(t.TypeArgs) != len(other.TypeArgs) {
			return false
		}
		for i := range t.TypeArgs {
			if !equals(t.TypeArgs[i], other.TypeArgs[i]) {
				return false
			}
		}
		// TypeAlias comparison
		if (t.TypeAlias == nil) != (other.TypeAlias == nil) {
			return false
		}
		if t.TypeAlias != nil && other.TypeAlias != nil {
			if !equals(t.TypeAlias.Type, other.TypeAlias.Type) {
				return false
			}
			if len(t.TypeAlias.TypeParams) != len(other.TypeAlias.TypeParams) {
				return false
			}
			for i := range t.TypeAlias.TypeParams {
				if t.TypeAlias.TypeParams[i].Name != other.TypeAlias.TypeParams[i].Name {
					return false
				}
				if !equals(t.TypeAlias.TypeParams[i].Constraint, other.TypeAlias.TypeParams[i].Constraint) {
					return false
				}
				if !equals(t.TypeAlias.TypeParams[i].Default, other.TypeAlias.TypeParams[i].Default) {
					return false
				}
			}
			if len(t.TypeAlias.LifetimeParams) != len(other.TypeAlias.LifetimeParams) {
				return false
			}
		}
		if t.Lifetime != other.Lifetime {
			return false
		}
		if len(t.LifetimeArgs) != len(other.LifetimeArgs) {
			return false
		}
		for i := range t.LifetimeArgs {
			if t.LifetimeArgs[i] != other.LifetimeArgs[i] {
				return false
			}
		}
		return true
	}
	return false
}

func (t *TypeRefType) String() string {
	return PrintType(t, PrintConfig{})
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
	enter := v.EnterType(t)
	if enter.Type != nil {
		t = enter.Type.(*PrimType)
	}
	if result := v.ExitType(t); result != nil {
		return result
	}
	return t
}
func (t *PrimType) Equals(other Type) bool {
	if other, ok := other.(*PrimType); ok {
		return t.Prim == other.Prim
	}
	return false
}

func (t *PrimType) String() string {
	return PrintType(t, PrintConfig{})
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
	enter := v.EnterType(t)
	if enter.Type != nil {
		t = enter.Type.(*RegexType)
	}
	if result := v.ExitType(t); result != nil {
		return result
	}
	return t
}
func (t *RegexType) Equals(other Type) bool {
	if other, ok := other.(*RegexType); ok {
		if !regexEqual(t.Regex, other.Regex) {
			return false
		}
		if len(t.Groups) != len(other.Groups) {
			return false
		}
		for i := range t.Groups {
			if t.Groups[i] != other.Groups[i] {
				return false
			}
		}
		return true
	}
	return false
}

func (t *RegexType) String() string {
	return PrintType(t, PrintConfig{})
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
	enter := v.EnterType(t)
	if enter.Type != nil {
		t = enter.Type.(*LitType)
	}
	if result := v.ExitType(t); result != nil {
		return result
	}
	return t
}
func (t *LitType) Equals(other Type) bool {
	if other, ok := other.(*LitType); ok {
		return t.Lit.Equal(other.Lit)
	}
	return false
}

func (t *LitType) String() string {
	return PrintType(t, PrintConfig{})
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
	enter := v.EnterType(t)
	if enter.Type != nil {
		t = enter.Type.(*UniqueSymbolType)
	}
	if result := v.ExitType(t); result != nil {
		return result
	}
	return t
}
func (t *UniqueSymbolType) Equals(other Type) bool {
	if other, ok := other.(*UniqueSymbolType); ok {
		return t.Value == other.Value
	}
	return false
}

func (t *UniqueSymbolType) String() string {
	return PrintType(t, PrintConfig{})
}

type UnknownType struct {
	provenance Provenance
}

func (t *UnknownType) Accept(v TypeVisitor) Type {
	enter := v.EnterType(t)
	if enter.Type != nil {
		t = enter.Type.(*UnknownType)
	}
	if result := v.ExitType(t); result != nil {
		return result
	}
	return t
}

func NewUnknownType(provenance Provenance) *UnknownType { return &UnknownType{provenance: provenance} }
func (t *UnknownType) Equals(other Type) bool {
	_, ok := other.(*UnknownType)
	return ok
}

func (t *UnknownType) String() string {
	return PrintType(t, PrintConfig{})
}

type NeverType struct {
	provenance Provenance
}

func (t *NeverType) Accept(v TypeVisitor) Type {
	enter := v.EnterType(t)
	if enter.Type != nil {
		t = enter.Type.(*NeverType)
	}
	if result := v.ExitType(t); result != nil {
		return result
	}
	return t
}

func NewNeverType(provenance Provenance) *NeverType { return &NeverType{provenance: provenance} }
func (t *NeverType) Equals(other Type) bool {
	_, ok := other.(*NeverType)
	return ok
}

func (t *NeverType) String() string {
	return PrintType(t, PrintConfig{})
}

// IsNeverType reports whether t is nil or a *NeverType.
func IsNeverType(t Type) bool {
	if t == nil {
		return true
	}
	_, ok := t.(*NeverType)
	return ok
}

type ErrorType struct {
	provenance Provenance
}

func (t *ErrorType) Accept(v TypeVisitor) Type {
	enter := v.EnterType(t)
	if enter.Type != nil {
		t = enter.Type.(*ErrorType)
	}
	if result := v.ExitType(t); result != nil {
		return result
	}
	return t
}

func NewErrorType(provenance Provenance) *ErrorType { return &ErrorType{provenance: provenance} }
func (t *ErrorType) Equals(other Type) bool {
	_, ok := other.(*ErrorType)
	return ok
}

func (t *ErrorType) String() string {
	return PrintType(t, PrintConfig{})
}

type VoidType struct {
	provenance Provenance
}

func (t *VoidType) Accept(v TypeVisitor) Type {
	enter := v.EnterType(t)
	if enter.Type != nil {
		t = enter.Type.(*VoidType)
	}
	if result := v.ExitType(t); result != nil {
		return result
	}
	return t
}

func NewVoidType(provenance Provenance) *VoidType { return &VoidType{provenance: provenance} }
func (t *VoidType) Equals(other Type) bool {
	_, ok := other.(*VoidType)
	return ok
}

func (t *VoidType) String() string {
	return PrintType(t, PrintConfig{})
}

type AnyType struct {
	provenance Provenance
}

func (t *AnyType) Accept(v TypeVisitor) Type {
	enter := v.EnterType(t)
	if enter.Type != nil {
		t = enter.Type.(*AnyType)
	}
	if result := v.ExitType(t); result != nil {
		return result
	}
	return t
}

func NewAnyType(provenance Provenance) *AnyType { return &AnyType{provenance: provenance} }
func (t *AnyType) Equals(other Type) bool {
	_, ok := other.(*AnyType)
	return ok
}

func (t *AnyType) String() string {
	return PrintType(t, PrintConfig{})
}

type GlobalThisType struct {
	provenance Provenance
}

func (t *GlobalThisType) Accept(v TypeVisitor) Type {
	enter := v.EnterType(t)
	if enter.Type != nil {
		t = enter.Type.(*GlobalThisType)
	}
	if result := v.ExitType(t); result != nil {
		return result
	}
	return t
}
func (t *GlobalThisType) Equals(other Type) bool {
	_, ok := other.(*GlobalThisType)
	return ok
}

func (t *GlobalThisType) String() string {
	return PrintType(t, PrintConfig{})
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

func (p *FuncParam) String() string {
	return printFuncParam(p, func(t Type) string { return t.String() })
}

func NewFuncParam(pattern Pat, t Type) *FuncParam {
	return &FuncParam{
		Pattern:  pattern,
		Type:     t,
		Optional: false,
	}
}

type FuncType struct {
	LifetimeParams []*LifetimeVar // e.g. ['a, 'b]
	TypeParams     []*TypeParam
	Params         []*FuncParam
	Return         Type
	Throws         Type
	provenance     Provenance
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
	enter := v.EnterType(t)
	if enter.Type != nil {
		t = enter.Type.(*FuncType)
	}
	if enter.SkipChildren {
		if visitResult := v.ExitType(t); visitResult != nil {
			return visitResult
		}
		return t
	}

	changed := false
	var newParams []*FuncParam
	for i, param := range t.Params {
		newType := param.Type.Accept(v)
		if newType != param.Type {
			if newParams == nil {
				newParams = make([]*FuncParam, len(t.Params))
				copy(newParams[:i], t.Params[:i])
			}
			changed = true
			newParams[i] = &FuncParam{
				Pattern:  param.Pattern,
				Type:     newType,
				Optional: param.Optional,
			}
		} else if newParams != nil {
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
		params := t.Params
		if newParams != nil {
			params = newParams
		}
		r := NewFuncType(
			t.provenance,
			t.TypeParams,
			params,
			newReturn,
			newThrows,
		)
		r.LifetimeParams = t.LifetimeParams
		result = r
	}

	if visitResult := v.ExitType(result); visitResult != nil {
		return visitResult
	}
	return result
}

func (t *FuncType) Equals(other Type) bool {
	if other, ok := other.(*FuncType); ok {
		// Compare TypeParams
		if len(t.TypeParams) != len(other.TypeParams) {
			return false
		}
		for i := range t.TypeParams {
			if t.TypeParams[i].Name != other.TypeParams[i].Name {
				return false
			}
			if !equals(t.TypeParams[i].Constraint, other.TypeParams[i].Constraint) {
				return false
			}
			if !equals(t.TypeParams[i].Default, other.TypeParams[i].Default) {
				return false
			}
		}
		// Compare LifetimeParams
		if len(t.LifetimeParams) != len(other.LifetimeParams) {
			return false
		}
		// Compare Params
		if len(t.Params) != len(other.Params) {
			return false
		}
		for i := range t.Params {
			if !equals(t.Params[i].Type, other.Params[i].Type) {
				return false
			}
			if t.Params[i].Optional != other.Params[i].Optional {
				return false
			}
			// We ignore patterns when comparing function types since
			// they don't affect the type.
		}
		// Compare Return and Throws
		if !equals(t.Return, other.Return) {
			return false
		}
		if !equals(t.Throws, other.Throws) {
			return false
		}
		return true
	}
	return false
}

func (t *FuncType) String() string {
	return PrintType(t, PrintConfig{})
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
	Name       ObjTypeKey
	Optional   bool
	Readonly   bool
	Value      Type
	Written    bool                  // true when property is assigned to during inference
	Provenance provenance.Provenance // span of the property access that inferred this property (nil for declared properties)
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
type IndexSignatureElem struct {
	KeyType  Type // must be a PrimType (string, number, symbol)
	Value    Type
	Readonly bool
}

// NewIndexSignatureElem creates an IndexSignatureElem, panicking if keyType is
// not a *PrimType with Prim in {StrPrim, NumPrim, SymbolPrim}.
func NewIndexSignatureElem(keyType Type, value Type, readonly bool) *IndexSignatureElem {
	prim, ok := keyType.(*PrimType)
	if !ok {
		panic(fmt.Sprintf("IndexSignatureElem keyType must be a *PrimType, got %T", keyType))
	}
	switch prim.Prim {
	case StrPrim, NumPrim, SymbolPrim:
		// valid
	default:
		panic(fmt.Sprintf("IndexSignatureElem keyType must be string, number, or symbol, got %v", prim.Prim))
	}
	return &IndexSignatureElem{
		KeyType:  keyType,
		Value:    value,
		Readonly: readonly,
	}
}

type RestSpreadElem struct{ Value Type }

func NewRestSpreadElem(value Type) *RestSpreadElem {
	return &RestSpreadElem{
		Value: value,
	}
}

func (*CallableElem) isObjTypeElem()       {}
func (*ConstructorElem) isObjTypeElem()    {}
func (*MethodElem) isObjTypeElem()         {}
func (*GetterElem) isObjTypeElem()         {}
func (*SetterElem) isObjTypeElem()         {}
func (*PropertyElem) isObjTypeElem()       {}
func (*MappedElem) isObjTypeElem()         {}
func (*IndexSignatureElem) isObjTypeElem() {}
func (*RestSpreadElem) isObjTypeElem()     {}

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
		return &MethodElem{Name: m.Name, Fn: newFn, MutSelf: m.MutSelf}
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
			Name:       p.Name,
			Optional:   p.Optional,
			Readonly:   p.Readonly,
			Value:      newValue,
			Written:    p.Written,
			Provenance: p.Provenance,
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
func (idx *IndexSignatureElem) Accept(v TypeVisitor) ObjTypeElem {
	changed := false
	newKeyType := idx.KeyType.Accept(v)
	if newKeyType != idx.KeyType {
		changed = true
	}
	newValue := idx.Value.Accept(v)
	if newValue != idx.Value {
		changed = true
	}
	if changed {
		return NewIndexSignatureElem(newKeyType, newValue, idx.Readonly)
	}
	return idx
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
	Open         bool // true for inferred object types whose property set can grow during inference
	// MatchedUnionMembers records which union members a structural pattern matched
	// during pattern-matching unification. Nil outside of pattern matching. Used by
	// downstream passes (e.g. exhaustiveness checking).
	MatchedUnionMembers []Type
	Lifetime            Lifetime // nil if no lifetime annotation
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
	enter := v.EnterType(t)
	if enter.Type != nil {
		t = enter.Type.(*ObjectType)
	}
	if enter.SkipChildren {
		if visitResult := v.ExitType(t); visitResult != nil {
			return visitResult
		}
		return t
	}

	newElems, elemsChanged := CowAcceptElems(t.Elems, v)
	newExtends, extendsChanged := CowAcceptTypeRefs(t.Extends, v)
	newImplements, implementsChanged := CowAcceptTypeRefs(t.Implements, v)
	changed := elemsChanged || extendsChanged || implementsChanged

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
		result.Open = t.Open
		result.MatchedUnionMembers = t.MatchedUnionMembers
		result.Lifetime = t.Lifetime
	}

	if visitResult := v.ExitType(result); visitResult != nil {
		return visitResult
	}
	return result
}
func (t *ObjectType) Equals(other Type) bool {
	if other, ok := other.(*ObjectType); ok {
		if t.Nominal && other.Nominal {
			return t.ID == other.ID
		}
		if t.Exact != other.Exact {
			return false
		}
		if t.Immutable != other.Immutable {
			return false
		}
		if t.Mutable != other.Mutable {
			return false
		}
		if t.Nominal != other.Nominal {
			return false
		}
		if t.Interface != other.Interface {
			return false
		}
		if t.Lifetime != other.Lifetime {
			return false
		}
		// Compare Extends
		if len(t.Extends) != len(other.Extends) {
			return false
		}
		for i := range t.Extends {
			if !equals(t.Extends[i], other.Extends[i]) {
				return false
			}
		}
		// Compare Implements
		if len(t.Implements) != len(other.Implements) {
			return false
		}
		for i := range t.Implements {
			if !equals(t.Implements[i], other.Implements[i]) {
				return false
			}
		}
		// Compare Elems
		if len(t.Elems) != len(other.Elems) {
			return false
		}
		for i := range t.Elems {
			if !objTypeElemEquals(t.Elems[i], other.Elems[i]) {
				return false
			}
		}
		// Compare SymbolKeyMap
		if len(t.SymbolKeyMap) != len(other.SymbolKeyMap) {
			return false
		}
		for k, v := range t.SymbolKeyMap {
			if otherV, ok := other.SymbolKeyMap[k]; !ok || v != otherV {
				return false
			}
		}
		return true
	}
	return false
}

// resolveTypeVar follows a TypeVarType's Instance chain to its terminal type
// without performing path compression or recording instance chains. This is
// safe to call from side-effect-free contexts like String().
func resolveTypeVar(t Type) Type {
	for {
		tv, ok := t.(*TypeVarType)
		if !ok || tv.Instance == nil {
			return t
		}
		t = tv.Instance
	}
}

// collectFlatElems collects all displayable elements from an ObjectType,
// flattening any RestSpreadElem whose value resolves to an ObjectType by
// inlining its properties. RestSpreadElems that resolve to empty ObjectTypes
// are dropped. RestSpreadElems that resolve to non-ObjectTypes (e.g. unresolved
// TypeVars or TypeRefTypes) are kept as-is.
func collectFlatElems(elems []ObjTypeElem) []ObjTypeElem {
	var result []ObjTypeElem
	for _, elem := range elems {
		rest, ok := elem.(*RestSpreadElem)
		if !ok {
			result = append(result, elem)
			continue
		}
		resolved := resolveTypeVar(rest.Value)
		if obj, ok := resolved.(*ObjectType); ok {
			// Recursively flatten nested RestSpreadElems
			result = append(result, collectFlatElems(obj.Elems)...)
		} else {
			result = append(result, elem)
		}
	}
	return result
}

func (t *ObjectType) String() string {
	return PrintType(t, PrintConfig{})
}

type TupleType struct {
	Elems      []Type
	Lifetime   Lifetime // nil if no lifetime annotation
	provenance Provenance
}

func NewTupleType(provenance Provenance, elems ...Type) *TupleType {
	return &TupleType{
		Elems:      elems,
		provenance: provenance,
	}
}
func (t *TupleType) Accept(v TypeVisitor) Type {
	enter := v.EnterType(t)
	if enter.Type != nil {
		t = enter.Type.(*TupleType)
	}
	if enter.SkipChildren {
		if visitResult := v.ExitType(t); visitResult != nil {
			return visitResult
		}
		return t
	}

	newElems, changed := CowAcceptTypes(t.Elems, v)

	var result Type = t
	if changed {
		r := NewTupleType(t.provenance, newElems...)
		r.Lifetime = t.Lifetime
		result = r
	}

	if visitResult := v.ExitType(result); visitResult != nil {
		return visitResult
	}
	return result
}
func (t *TupleType) Equals(other Type) bool {
	if other, ok := other.(*TupleType); ok {
		if t.Lifetime != other.Lifetime {
			return false
		}
		if len(t.Elems) != len(other.Elems) {
			return false
		}
		for i := range t.Elems {
			if !equals(t.Elems[i], other.Elems[i]) {
				return false
			}
		}
		return true
	}
	return false
}

func (t *TupleType) String() string {
	return PrintType(t, PrintConfig{})
}

// collectFlatTupleElems collects all displayable elements from a TupleType,
// flattening any RestSpreadType whose inner type resolves to a TupleType by
// inlining its elements. RestSpreadTypes that resolve to non-TupleTypes (e.g.
// unresolved TypeVars or Array types) are kept as-is.
func collectFlatTupleElems(elems []Type) []Type {
	var result []Type
	for _, elem := range elems {
		rest, ok := elem.(*RestSpreadType)
		if !ok {
			result = append(result, elem)
			continue
		}
		resolved := resolveTypeVar(rest.Type)
		if tuple, ok := resolved.(*TupleType); ok {
			// Recursively flatten nested RestSpreadTypes
			result = append(result, collectFlatTupleElems(tuple.Elems)...)
		} else {
			result = append(result, elem)
		}
	}
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
	enter := v.EnterType(t)
	if enter.Type != nil {
		t = enter.Type.(*RestSpreadType)
	}
	if enter.SkipChildren {
		if visitResult := v.ExitType(t); visitResult != nil {
			return visitResult
		}
		return t
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
func (t *RestSpreadType) Equals(other Type) bool {
	if other, ok := other.(*RestSpreadType); ok {
		return equals(t.Type, other.Type)
	}
	return false
}

func (t *RestSpreadType) String() string {
	return PrintType(t, PrintConfig{})
}

type UnionType struct {
	Types      []Type
	provenance Provenance
}

// litPrimKind returns the Prim that a LitType corresponds to, or "" if the
// literal has no primitive supertype (e.g. null, undefined).
func litPrimKind(lit *LitType) Prim {
	switch lit.Lit.(type) {
	case *NumLit:
		return NumPrim
	case *StrLit:
		return StrPrim
	case *BoolLit:
		return BoolPrim
	case *BigIntLit:
		return BigIntPrim
	default:
		return ""
	}
}

// flattenUnionTypes recursively collects all non-Never leaf types from a
// slice of types, flattening any nested UnionTypes in order.
func flattenUnionTypes(dst []Type, types []Type) []Type {
	for _, t := range types {
		switch t := t.(type) {
		case *UnionType:
			dst = flattenUnionTypes(dst, t.Types)
		case *NeverType:
			// skip
		default:
			dst = append(dst, t)
		}
	}
	return dst
}

func NewUnionType(provenance Provenance, types ...Type) Type {
	// Recursively flatten nested unions and remove Never types.
	// Flattening ensures that primitives inside nested unions are visible
	// for the literal absorption pass below.
	filtered := flattenUnionTypes(nil, types)

	// Simplify literal types: if a primitive is present, remove all literal
	// types of the same kind (e.g. number absorbs 0). Also, true | false
	// collapses to boolean.
	primSet := make(map[Prim]bool)
	hasBoolTrue := false
	hasBoolFalse := false
	for _, t := range filtered {
		if prim, ok := t.(*PrimType); ok {
			primSet[prim.Prim] = true
		}
		if lit, ok := t.(*LitType); ok {
			if bl, ok := lit.Lit.(*BoolLit); ok {
				if bl.Value {
					hasBoolTrue = true
				} else {
					hasBoolFalse = true
				}
			}
		}
	}

	// If both true and false are present (and boolean isn't already), add boolean.
	addBoolean := hasBoolTrue && hasBoolFalse && !primSet[BoolPrim]
	if addBoolean {
		primSet[BoolPrim] = true
	}

	if len(primSet) > 0 {
		simplified := make([]Type, 0, len(filtered))
		for _, t := range filtered {
			if lit, ok := t.(*LitType); ok {
				if kind := litPrimKind(lit); kind != "" && primSet[kind] {
					continue // absorbed by the primitive
				}
			}
			simplified = append(simplified, t)
		}
		if addBoolean {
			simplified = append(simplified, NewBoolPrimType(provenance))
		}
		filtered = simplified
	}

	// Deduplicate structurally equal types.
	deduped := make([]Type, 0, len(filtered))
	for _, t := range filtered {
		isDup := false
		for _, existing := range deduped {
			if Equals(t, existing) {
				isDup = true
				break
			}
		}
		if !isDup {
			deduped = append(deduped, t)
		}
	}
	filtered = deduped

	if len(filtered) == 0 {
		return NewNeverType(nil)
	}
	if len(filtered) == 1 {
		return filtered[0]
	}
	return &UnionType{
		Types:      filtered,
		provenance: provenance,
	}
}
func (t *UnionType) Accept(v TypeVisitor) Type {
	enter := v.EnterType(t)
	if enter.Type != nil {
		t = enter.Type.(*UnionType)
	}
	if enter.SkipChildren {
		if visitResult := v.ExitType(t); visitResult != nil {
			return visitResult
		}
		return t
	}

	newTypes, changed := CowAcceptTypes(t.Types, v)

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
func (t *UnionType) Equals(other Type) bool {
	if other, ok := other.(*UnionType); ok {
		if len(t.Types) != len(other.Types) {
			return false
		}
		for i := range t.Types {
			if !equals(t.Types[i], other.Types[i]) {
				return false
			}
		}
		return true
	}
	return false
}

func (t *UnionType) String() string {
	return PrintType(t, PrintConfig{})
}

type IntersectionType struct {
	Types      []Type
	provenance Provenance
}

// NewIntersectionType creates a normalized intersection type from the given types.
// The normalization process ensures consistent representation and eliminates redundant types:
//
//   - Empty intersections return never
//   - Single-type intersections return that type directly
//   - Nested intersections are flattened: (A & B) & C becomes A & B & C
//   - Duplicate types are removed using structural equality
//   - any absorbs all other types: A & any becomes any
//   - never collapses the intersection: A & never becomes never
//   - unknown is the identity: A & unknown becomes A
//   - Conflicting primitives produce never: string & number becomes never
//   - For (mut T) & T, the immutable version T is preferred
//
// Note: This performs basic normalization only. Additional normalization may be
// needed after type inference when type aliases are resolved.
func NewIntersectionType(provenance Provenance, types ...Type) Type {
	if len(types) == 0 {
		return NewNeverType(nil)
	}
	if len(types) == 1 {
		return types[0]
	}

	// Flatten nested intersections
	flattened := []Type{}
	for _, t := range types {
		t = Prune(t)
		if inter, ok := t.(*IntersectionType); ok {
			flattened = append(flattened, inter.Types...)
		} else {
			flattened = append(flattened, t)
		}
	}

	// Normalize
	normalized := []Type{}
	hasAny := false
	hasNever := false
	primitiveTypes := make(map[Prim]*PrimType)

	for _, t := range flattened {
		t = Prune(t)

		// Check for any
		if _, ok := t.(*AnyType); ok {
			hasAny = true
			break
		}

		// Check for never
		if _, ok := t.(*NeverType); ok {
			hasNever = true
			continue // Don't add never to the list
		}

		// Remove unknown
		if _, ok := t.(*UnknownType); ok {
			continue
		}

		// Track primitive types to detect conflicts
		if prim, ok := t.(*PrimType); ok {
			if existing, exists := primitiveTypes[prim.Prim]; exists {
				// Same primitive, already added
				if existing.Prim == prim.Prim {
					continue
				}
			}
			// Check for conflicting primitives
			if len(primitiveTypes) > 0 {
				// Different primitive types exist
				hasConflict := false
				for existingPrim := range primitiveTypes {
					if existingPrim != prim.Prim {
						hasConflict = true
						break
					}
				}
				if hasConflict {
					// Conflicting primitives: string & number → never
					return NewNeverType(provenance)
				}
			}
			primitiveTypes[prim.Prim] = prim
		}

		// Remove duplicates using Equals
		alreadyExists := false
		for _, existing := range normalized {
			if existing.Equals(t) {
				alreadyExists = true
				break
			}
		}
		if alreadyExists {
			continue
		}

		normalized = append(normalized, t)
	}

	// Second pass: handle MutabilityType
	// If we have both (mut T) and T, keep only T
	finalNormalized := []Type{}
	for _, t := range normalized {
		if mut, ok := t.(*MutabilityType); ok {
			if mut.Mutability == MutabilityMutable {
				// Check if immutable version exists in normalized using Equals
				hasImmutable := false
				for _, other := range normalized {
					if other.Equals(mut.Type) {
						hasImmutable = true
						break
					}
				}
				if hasImmutable {
					// Skip the mutable version, keep immutable
					continue
				}
			}
		}
		finalNormalized = append(finalNormalized, t)
	}
	normalized = finalNormalized

	if hasAny {
		return NewAnyType(provenance)
	}

	if hasNever {
		return NewNeverType(provenance)
	}

	if len(normalized) == 0 {
		return NewNeverType(provenance)
	}

	if len(normalized) == 1 {
		return normalized[0]
	}

	return &IntersectionType{
		Types:      normalized,
		provenance: provenance,
	}
}

func (t *IntersectionType) Accept(v TypeVisitor) Type {
	enter := v.EnterType(t)
	if enter.Type != nil {
		t = enter.Type.(*IntersectionType)
	}
	if enter.SkipChildren {
		if visitResult := v.ExitType(t); visitResult != nil {
			return visitResult
		}
		return t
	}

	newTypes, changed := CowAcceptTypes(t.Types, v)

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
func (t *IntersectionType) Equals(other Type) bool {
	if other, ok := other.(*IntersectionType); ok {
		if len(t.Types) != len(other.Types) {
			return false
		}
		for i := range t.Types {
			if !equals(t.Types[i], other.Types[i]) {
				return false
			}
		}
		return true
	}
	return false
}

func (t *IntersectionType) String() string {
	return PrintType(t, PrintConfig{})
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
	enter := v.EnterType(t)
	if enter.Type != nil {
		t = enter.Type.(*KeyOfType)
	}
	if enter.SkipChildren {
		if visitResult := v.ExitType(t); visitResult != nil {
			return visitResult
		}
		return t
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
func (t *KeyOfType) Equals(other Type) bool {
	if other, ok := other.(*KeyOfType); ok {
		return equals(t.Type, other.Type)
	}
	return false
}

func (t *KeyOfType) String() string {
	return PrintType(t, PrintConfig{})
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
	enter := v.EnterType(t)
	if enter.Type != nil {
		t = enter.Type.(*TypeOfType)
	}

	if visitResult := v.ExitType(t); visitResult != nil {
		return visitResult
	}
	return t
}

func (t *TypeOfType) Equals(other Type) bool {
	if other, ok := other.(*TypeOfType); ok {
		return qualIdentEquals(t.Ident, other.Ident)
	}
	return false
}

func (t *TypeOfType) String() string {
	return PrintType(t, PrintConfig{})
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
	enter := v.EnterType(t)
	if enter.Type != nil {
		t = enter.Type.(*IndexType)
	}
	if enter.SkipChildren {
		if visitResult := v.ExitType(t); visitResult != nil {
			return visitResult
		}
		return t
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
func (t *IndexType) Equals(other Type) bool {
	if other, ok := other.(*IndexType); ok {
		return equals(t.Target, other.Target) && equals(t.Index, other.Index)
	}
	return false
}

func (t *IndexType) String() string {
	return PrintType(t, PrintConfig{})
}

type CondType struct {
	Check      Type
	Extends    Type
	Then       Type
	Else       Type
	provenance Provenance
}

func (t *CondType) Accept(v TypeVisitor) Type {
	enter := v.EnterType(t)
	if enter.Type != nil {
		t = enter.Type.(*CondType)
	}
	if enter.SkipChildren {
		if visitResult := v.ExitType(t); visitResult != nil {
			return visitResult
		}
		return t
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
func (t *CondType) Equals(other Type) bool {
	if other, ok := other.(*CondType); ok {
		return equals(t.Check, other.Check) &&
			equals(t.Extends, other.Extends) &&
			equals(t.Then, other.Then) &&
			equals(t.Else, other.Else)
	}
	return false
}

func (t *CondType) String() string {
	return PrintType(t, PrintConfig{})
}

type InferType struct {
	Name       string
	provenance Provenance
}

func (t *InferType) Accept(v TypeVisitor) Type {
	enter := v.EnterType(t)
	if enter.Type != nil {
		t = enter.Type.(*InferType)
	}
	if result := v.ExitType(t); result != nil {
		return result
	}
	return t
}
func (t *InferType) Equals(other Type) bool {
	if other, ok := other.(*InferType); ok {
		return t.Name == other.Name
	}
	return false
}

func (t *InferType) String() string {
	return PrintType(t, PrintConfig{})
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
	enter := v.EnterType(t)
	if enter.Type != nil {
		t = enter.Type.(*MutabilityType)
	}
	if enter.SkipChildren {
		if visitResult := v.ExitType(t); visitResult != nil {
			return visitResult
		}
		return t
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
func (t *MutabilityType) Equals(other Type) bool {
	if other, ok := other.(*MutabilityType); ok {
		return t.Mutability == other.Mutability && equals(t.Type, other.Type)
	}
	return false
}

func (t *MutabilityType) String() string {
	return PrintType(t, PrintConfig{})
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
	enter := v.EnterType(t)
	if enter.Type != nil {
		t = enter.Type.(*WildcardType)
	}
	if result := v.ExitType(t); result != nil {
		return result
	}
	return t
}
func (t *WildcardType) Equals(other Type) bool {
	_, ok := other.(*WildcardType)
	return ok
}

func (t *WildcardType) String() string {
	return PrintType(t, PrintConfig{})
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
	enter := v.EnterType(t)
	if enter.Type != nil {
		t = enter.Type.(*ExtractorType)
	}
	if enter.SkipChildren {
		if visitResult := v.ExitType(t); visitResult != nil {
			return visitResult
		}
		return t
	}

	newExtractor := t.Extractor.Accept(v)
	newArgs, argsChanged := CowAcceptTypes(t.Args, v)
	changed := newExtractor != t.Extractor || argsChanged

	var result Type = t
	if changed {
		result = NewExtractorType(t.provenance, newExtractor, newArgs...)
	}

	if visitResult := v.ExitType(result); visitResult != nil {
		return visitResult
	}
	return result
}
func (t *ExtractorType) Equals(other Type) bool {
	if other, ok := other.(*ExtractorType); ok {
		if !equals(t.Extractor, other.Extractor) {
			return false
		}
		if len(t.Args) != len(other.Args) {
			return false
		}
		for i := range t.Args {
			if !equals(t.Args[i], other.Args[i]) {
				return false
			}
		}
		return true
	}
	return false
}

func (t *ExtractorType) String() string {
	return PrintType(t, PrintConfig{})
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
	enter := v.EnterType(t)
	if enter.Type != nil {
		t = enter.Type.(*TemplateLitType)
	}
	if enter.SkipChildren {
		if visitResult := v.ExitType(t); visitResult != nil {
			return visitResult
		}
		return t
	}

	newTypes, changed := CowAcceptTypes(t.Types, v)

	var result Type = t
	if changed {
		result = NewTemplateLitType(t.provenance, t.Quasis, newTypes)
	}

	if visitResult := v.ExitType(result); visitResult != nil {
		return visitResult
	}
	return result
}
func (t *TemplateLitType) Equals(other Type) bool {
	if other, ok := other.(*TemplateLitType); ok {
		if len(t.Quasis) != len(other.Quasis) {
			return false
		}
		for i := range t.Quasis {
			if t.Quasis[i].Value != other.Quasis[i].Value {
				return false
			}
		}
		if len(t.Types) != len(other.Types) {
			return false
		}
		for i := range t.Types {
			if !equals(t.Types[i], other.Types[i]) {
				return false
			}
		}
		return true
	}
	return false
}

func (t *TemplateLitType) String() string {
	return PrintType(t, PrintConfig{})
}

type IntrinsicType struct {
	Name       string
	provenance Provenance
}

func (t *IntrinsicType) Accept(v TypeVisitor) Type {
	enter := v.EnterType(t)
	if enter.Type != nil {
		t = enter.Type.(*IntrinsicType)
	}
	if result := v.ExitType(t); result != nil {
		return result
	}
	return t
}
func (t *IntrinsicType) Equals(other Type) bool {
	if other, ok := other.(*IntrinsicType); ok {
		return t.Name == other.Name
	}
	return false
}

func (t *IntrinsicType) String() string {
	return PrintType(t, PrintConfig{})
}

// We want to model both `let x = 5` as well as `fn (x: number) => x`
type Binding struct {
	Source   provenance.Provenance // optional
	Type     Type
	Mutable  bool
	Exported bool
	VarID    int // liveness VarID assigned by the rename pass; 0 if unset
}

// This is similar to Scope, but instead of inheriting from a parent scope,
// the identifiers are fully qualified with their namespace (e.g. "foo.bar.baz").
// This makes it easier to build a dependency graph between declarations within
// the module.
type Namespace struct {
	Values     map[string]*Binding
	Types      map[string]*TypeAlias
	Namespaces map[string]*Namespace
	Exported   bool
}

func NewNamespace() *Namespace {
	return &Namespace{
		Values:     make(map[string]*Binding),
		Types:      make(map[string]*TypeAlias),
		Namespaces: make(map[string]*Namespace),
	}
}

// SetNamespace binds a sub-namespace to the given name in this namespace.
// Returns an error if the name conflicts with an existing type or value.
func (ns *Namespace) SetNamespace(name string, subNs *Namespace) error {
	if ns.Namespaces == nil {
		ns.Namespaces = make(map[string]*Namespace)
	}

	// Check for conflicts with existing sub-namespaces
	if _, exists := ns.Namespaces[name]; exists {
		return fmt.Errorf("cannot bind sub-namespace %q: conflicts with existing namespace", name)
	}

	// Check for conflicts with types
	if _, exists := ns.Types[name]; exists {
		return fmt.Errorf("cannot bind sub-namespace %q: conflicts with existing type", name)
	}
	// Check for conflicts with values
	if _, exists := ns.Values[name]; exists {
		return fmt.Errorf("cannot bind sub-namespace %q: conflicts with existing value", name)
	}

	ns.Namespaces[name] = subNs
	return nil
}

// GetNamespace returns the sub-namespace bound to the given name.
// Returns (namespace, true) if found, or (nil, false) if not found.
func (ns *Namespace) GetNamespace(name string) (*Namespace, bool) {
	if ns.Namespaces == nil {
		return nil, false
	}
	subNs, ok := ns.Namespaces[name]
	return subNs, ok
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
	enter := v.EnterType(t)
	if enter.Type != nil {
		t = enter.Type.(*NamespaceType)
	}
	if enter.SkipChildren {
		if visitResult := v.ExitType(t); visitResult != nil {
			return visitResult
		}
		return t
	}

	changed := false
	newValues := make(map[string]*Binding)
	for name, binding := range t.Namespace.Values {
		newType := binding.Type.Accept(v)
		if newType != binding.Type {
			changed = true
			newValues[name] = &Binding{
				Source:   binding.Source,
				Type:     newType,
				Mutable:  binding.Mutable,
				Exported: binding.Exported,
				VarID:    binding.VarID,
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
				Type:           newType,
				TypeParams:     typeAlias.TypeParams,
				LifetimeParams: typeAlias.LifetimeParams,
				Exported:       typeAlias.Exported,
				IsTypeParam:    typeAlias.IsTypeParam,
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
func (t *NamespaceType) Equals(other Type) bool {
	if other, ok := other.(*NamespaceType); ok {
		return namespaceEquals(t.Namespace, other.Namespace)
	}
	return false
}

func (t *NamespaceType) String() string {
	return PrintType(t, PrintConfig{})
}

// Helper functions for equality comparisons

func regexEqual(x, y *regexp.Regexp) bool {
	if x == nil || y == nil {
		return x == y
	}
	return x.String() == y.String()
}

func equals(t1, t2 Type) bool {
	if t1 == nil && t2 == nil {
		return true
	}
	if t1 == nil || t2 == nil {
		return false
	}
	return t1.Equals(t2)
}

func qualIdentEquals(q1, q2 QualIdent) bool {
	if q1 == nil && q2 == nil {
		return true
	}
	if q1 == nil || q2 == nil {
		return false
	}
	switch q1 := q1.(type) {
	case *Ident:
		if q2, ok := q2.(*Ident); ok {
			return q1.Name == q2.Name
		}
	case *Member:
		if q2, ok := q2.(*Member); ok {
			return qualIdentEquals(q1.Left, q2.Left) && q1.Right == q2.Right
		}
	}
	return false
}

func objTypeElemEquals(e1, e2 ObjTypeElem) bool {
	switch e1 := e1.(type) {
	case *CallableElem:
		if e2, ok := e2.(*CallableElem); ok {
			return equals(e1.Fn, e2.Fn)
		}
	case *ConstructorElem:
		if e2, ok := e2.(*ConstructorElem); ok {
			return equals(e1.Fn, e2.Fn)
		}
	case *MethodElem:
		if e2, ok := e2.(*MethodElem); ok {
			return e1.Name == e2.Name && e1.MutSelf == e2.MutSelf && equals(e1.Fn, e2.Fn)
		}
	case *GetterElem:
		if e2, ok := e2.(*GetterElem); ok {
			return e1.Name == e2.Name && equals(e1.Fn, e2.Fn)
		}
	case *SetterElem:
		if e2, ok := e2.(*SetterElem); ok {
			return e1.Name == e2.Name && equals(e1.Fn, e2.Fn)
		}
	case *PropertyElem:
		if e2, ok := e2.(*PropertyElem); ok {
			return e1.Name == e2.Name &&
				e1.Optional == e2.Optional &&
				e1.Readonly == e2.Readonly &&
				equals(e1.Value, e2.Value)
		}
	case *MappedElem:
		if e2, ok := e2.(*MappedElem); ok {
			return e1.TypeParam.Name == e2.TypeParam.Name &&
				equals(e1.TypeParam.Constraint, e2.TypeParam.Constraint) &&
				e1.Name == e2.Name &&
				equals(e1.Value, e2.Value) &&
				e1.Optional == e2.Optional &&
				e1.Readonly == e2.Readonly &&
				equals(e1.Check, e2.Check) &&
				equals(e1.Extends, e2.Extends)
		}
	case *RestSpreadElem:
		if e2, ok := e2.(*RestSpreadElem); ok {
			return equals(e1.Value, e2.Value)
		}
	case *IndexSignatureElem:
		if e2, ok := e2.(*IndexSignatureElem); ok {
			return e1.Readonly == e2.Readonly &&
				equals(e1.KeyType, e2.KeyType) &&
				equals(e1.Value, e2.Value)
		}
	}
	return false
}

func namespaceEquals(n1, n2 *Namespace) bool {
	if n1 == nil && n2 == nil {
		return true
	}
	if n1 == nil || n2 == nil {
		return false
	}
	// Compare Values
	if len(n1.Values) != len(n2.Values) {
		return false
	}
	// Only Mutable and Type participate in structural equality. VarID is
	// liveness metadata and Exported is a module-level concern — neither
	// affects the identity of the namespace's type structure.
	for k, v1 := range n1.Values {
		if v2, ok := n2.Values[k]; !ok {
			return false
		} else if v1.Mutable != v2.Mutable {
			return false
		} else if !equals(v1.Type, v2.Type) {
			return false
		}
	}
	// Compare Types
	if len(n1.Types) != len(n2.Types) {
		return false
	}
	for k, v1 := range n1.Types {
		if v2, ok := n2.Types[k]; !ok {
			return false
		} else {
			// Compare TypeAlias
			if (v1 == nil) != (v2 == nil) {
				return false
			}
			if v1 != nil && v2 != nil {
				if !equals(v1.Type, v2.Type) {
					return false
				}
				if len(v1.TypeParams) != len(v2.TypeParams) {
					return false
				}
				for i := range v1.TypeParams {
					if v1.TypeParams[i].Name != v2.TypeParams[i].Name {
						return false
					}
					if !equals(v1.TypeParams[i].Constraint, v2.TypeParams[i].Constraint) {
						return false
					}
					if !equals(v1.TypeParams[i].Default, v2.TypeParams[i].Default) {
						return false
					}
				}
				if len(v1.LifetimeParams) != len(v2.LifetimeParams) {
					return false
				}
			}
		}
	}
	// Compare Namespaces
	if len(n1.Namespaces) != len(n2.Namespaces) {
		return false
	}
	for k, v1 := range n1.Namespaces {
		if v2, ok := n2.Namespaces[k]; !ok || !namespaceEquals(v1, v2) {
			return false
		}
	}
	return true
}

// Equals is a convenience function that delegates to the Equals method
func Equals(t1 Type, t2 Type) bool {
	return equals(t1, t2)
}

// CowAcceptTypes applies a TypeVisitor to each element of a []Type slice.
// It is used inside Accept methods on composite types (UnionType, TupleType, etc.)
// to visit child type slices. It uses copy-on-write semantics so that no slice
// allocation occurs when the visitor leaves all elements unchanged — which is the
// common case during type walks. This matters because Accept is called recursively
// on every node in every type tree during inference, and most visits are no-op
// traversals. Returns the (possibly new) slice and whether any element changed.
//
// Note: unlike CowAcceptTypeRefs, this function does not check for nil elements.
// All []Type slices in the type system (TypeArgs, Elems, Types, Args) are
// constructed with non-nil entries, and the rest of the codebase (String, Equals,
// Accept methods) calls methods on elements without nil guards. If a nil element
// were present, it would panic here and in many other places.
func CowAcceptTypes(items []Type, v TypeVisitor) ([]Type, bool) {
	var result []Type
	for i, item := range items {
		newItem := item.Accept(v)
		if newItem != item {
			if result == nil {
				result = make([]Type, len(items))
				copy(result[:i], items[:i])
			}
		}
		if result != nil {
			result[i] = newItem
		}
	}
	if result != nil {
		return result, true
	}
	return items, false
}

// CowAcceptElems applies a TypeVisitor to each element of a []ObjTypeElem slice.
// It is used inside ObjectType.Accept to visit the Elems slice. Like CowAcceptTypes,
// it uses copy-on-write semantics to avoid allocating a new slice when no elements
// change.
func CowAcceptElems(items []ObjTypeElem, v TypeVisitor) ([]ObjTypeElem, bool) {
	var result []ObjTypeElem
	for i, item := range items {
		newItem := item.Accept(v)
		if newItem != item {
			if result == nil {
				result = make([]ObjTypeElem, len(items))
				copy(result[:i], items[:i])
			}
		}
		if result != nil {
			result[i] = newItem
		}
	}
	if result != nil {
		return result, true
	}
	return items, false
}

// CowAcceptTypeRefs applies a TypeVisitor to each element of a []*TypeRefType slice.
// It is used inside ObjectType.Accept to visit the Extends and Implements slices.
// Like CowAcceptTypes, it uses copy-on-write semantics to avoid allocating a new
// slice when no elements change. Nil elements are preserved as-is.
func CowAcceptTypeRefs(items []*TypeRefType, v TypeVisitor) ([]*TypeRefType, bool) {
	var result []*TypeRefType
	for i, item := range items {
		if item == nil {
			if result != nil {
				result[i] = nil
			}
			continue
		}
		newItem := item.Accept(v).(*TypeRefType)
		if newItem != item {
			if result == nil {
				result = make([]*TypeRefType, len(items))
				copy(result[:i], items[:i])
			}
		}
		if result != nil {
			result[i] = newItem
		}
	}
	if result != nil {
		return result, true
	}
	return items, false
}
