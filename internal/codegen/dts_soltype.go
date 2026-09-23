package codegen

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/escalier-lang/escalier/internal/soltype"
)

// dts_soltype.go renders solver types as TypeScript declarations, the twin of
// the type_system-driven half of dts.go. The `FromSol` suffix marks the
// counterpart of a function there, and goes when internal/type_system does.
//
// A former TypeScript cannot express degrades rather than failing; each arm says
// how and why.

// solTypeAnnBuilder renders soltype values as .d.ts type annotations.
type solTypeAnnBuilder struct {
	// preludePrefix is the package key `std:prelude` is registered under, such as
	// `import:std:prelude`. It is how a reference to one of the four prelude types
	// below is told from a user's own type of the same name. Empty trims nothing.
	preludePrefix string
	// typeParamNames names each quantified type parameter. soltype binds one
	// through TypeParam.Var rather than by name, so a `T` in a signature body
	// arrives as the same pointer the binder holds.
	typeParamNames map[*soltype.TypeVarType]string
	// nextTypeParam is the index the next name for a retained variable comes from.
	nextTypeParam int
	// bindAt names, for each variable, the innermost signature holding every
	// occurrence of it, which is the one that may bind it. A variable whose
	// occurrences share no enclosing signature maps to nil and renders `unknown`.
	// render fills this; a nil map binds nothing.
	bindAt map[*soltype.TypeVarType]*soltype.FuncType
	// varOrder is every variable in the order the walk reached it, so a signature
	// renders its binders in a stable order.
	varOrder []*soltype.TypeVarType
	// companionPrefix seeds the names of the declarations a render mints, so two
	// bindings in one file cannot collide. The declaration walk passes the
	// binding's own local name.
	companionPrefix string
	// companions are the declarations this render minted, in the order they must
	// be emitted. A recursive type has no inline form, so it is named by one.
	companions []*TypeDecl
	// recursiveNames maps a knot's binder id to the name its companion declares,
	// so a reference back to the binder resolves to that name.
	recursiveNames map[int]string
}

// newSolTypeAnnBuilder returns a renderer for one declaration. typeParams are
// that declaration's own, since nothing inside its body binds them.
func newSolTypeAnnBuilder(preludePrefix, companionPrefix string, typeParams []*soltype.TypeParam) *solTypeAnnBuilder {
	b := &solTypeAnnBuilder{
		preludePrefix:   preludePrefix,
		typeParamNames:  nil,
		nextTypeParam:   0,
		bindAt:          nil,
		varOrder:        nil,
		companionPrefix: companionPrefix,
		companions:      nil,
		recursiveNames:  map[int]string{},
	}
	b.bindTypeParams(typeParams)
	return b
}

// companionDecls are the declarations this render minted. They carry what
// TypeScript has no inline form for, so each must be emitted before the
// declaration whose type references it.
func (b *solTypeAnnBuilder) companionDecls() []*TypeDecl {
	return b.companions
}

// bindTypeParams registers each parameter under its declared name. Nothing is
// ever unregistered: each binder mints its own variable, so two that both write
// `T` hold different pointers and neither shadows the other.
func (b *solTypeAnnBuilder) bindTypeParams(typeParams []*soltype.TypeParam) {
	for _, tp := range typeParams {
		if tp.Var == nil || tp.Name == "" {
			continue
		}
		if b.typeParamNames == nil {
			b.typeParamNames = map[*soltype.TypeVarType]string{}
		}
		b.typeParamNames[tp.Var] = tp.Name
	}
}

// render is the entry point for a whole type. Counting occurrences first is what
// lets a signature tell a variable it fully contains from one it shares.
func (b *solTypeAnnBuilder) render(t soltype.Type) TypeAnn {
	if b.typeParamNames == nil {
		b.typeParamNames = map[*soltype.TypeVarType]string{}
	}
	b.bindAt, b.varOrder = bindingSignatures(t)
	return b.typeAnn(t)
}

func (b *solTypeAnnBuilder) typeAnn(t soltype.Type) TypeAnn {
	switch t := t.(type) {
	case *soltype.TypeVarType:
		if name, ok := b.typeParamNames[t]; ok {
			return NewRefTypeAnn(name, nil)
		}
		// Outside every signature in the rendered type, so there is nothing to hang
		// a `<T>` on. For a value binding such as `val xs = []` the element type is
		// genuinely undetermined, which `unknown` states.
		return NewUnknownTypeAnn(nil)
	case *soltype.SkolemType:
		// A rigid type parameter, rendered like a reference to it.
		return NewRefTypeAnn(t.Name, nil)
	case *soltype.PrimType:
		switch t.Prim {
		case soltype.NumPrim:
			return NewNumberTypeAnn(nil)
		case soltype.StrPrim:
			return NewStringTypeAnn(nil)
		case soltype.BoolPrim:
			return NewBooleanTypeAnn(nil)
		case soltype.SymPrim:
			return NewSymbolTypeAnn(nil)
		case soltype.BigIntPrim:
			return NewBigIntTypeAnn(nil)
		default:
			panic(fmt.Sprintf("typeAnn: unknown primitive %d", int(t.Prim)))
		}
	case *soltype.LitType:
		return NewLitTypeAnn(litToLitFromSol(t.Lit))
	case *soltype.NullType:
		return NewNullTypeAnn(nil)
	case *soltype.UndefinedType:
		return NewUndefinedTypeAnn(nil)
	case *soltype.NeverType:
		return NewNeverTypeAnn(nil)
	case *soltype.UnknownType:
		return NewUnknownTypeAnn(nil)
	case *soltype.ErrorType:
		// The recovery sentinel absorbs in both directions, as `any` does, so a
		// declaration built after a reported error cascades no second one.
		return NewAnyTypeAnn(nil)
	case *soltype.UniqueSymbolType:
		return NewUniqueSymbolTypeAnn(nil)
	case *soltype.SelfType:
		// `Self` is the receiver's class, which TypeScript spells `this`. No rewrite
		// pass is needed because soltype carries it as a kind of its own.
		return NewRefTypeAnn("this", nil)
	case *soltype.ClassType:
		return NewRefTypeAnn(refNameFromSol(t.Name), b.typeArgs(t.Name, t.TypeArgs))
	case *soltype.AliasType:
		return NewRefTypeAnn(refNameFromSol(t.Name), b.typeArgs(t.Name, t.TypeArgs))
	case *soltype.GeneratorType:
		// Both take three type arguments in TypeScript. Escalier's fourth, what
		// advancing the generator may raise, has no slot.
		// TODO(#385): Preserve the dropped raise type as TSDoc metadata.
		return NewRefTypeAnn(t.Name(), []TypeAnn{
			b.typeAnn(t.Yield),
			b.typeAnn(t.Ret),
			b.typeAnn(t.Next),
		})
	case *soltype.FuncType:
		fn := b.funcTypeAnn(t)
		return &fn
	case *soltype.TupleType:
		elems := make([]TypeAnn, len(t.Elems))
		for i, elem := range t.Elems {
			elems[i] = b.typeAnn(elem)
		}
		return NewTupleTypeAnn(elems)
	case *soltype.ObjectType:
		return b.objectTypeAnn(t)
	case *soltype.RefType:
		// Ownership and borrowing have no TypeScript form, so render the pointee.
		return b.typeAnn(t.Inner)
	case *soltype.UnionType:
		types := make([]TypeAnn, len(t.Types))
		for i, member := range t.Types {
			types[i] = b.typeAnn(member)
		}
		return NewUnionTypeAnn(types)
	case *soltype.IntersectionType:
		types := make([]TypeAnn, len(t.Types))
		for i, member := range t.Types {
			types[i] = b.typeAnn(member)
		}
		return NewIntersectionTypeAnn(types)
	case *soltype.KeyofType:
		return NewKeyOfTypeAnn(b.typeAnn(t.Operand))
	case *soltype.IndexType:
		return NewIndexTypeAnn(b.typeAnn(t.Target), b.typeAnn(t.Index))
	case *soltype.TypeofType:
		return NewTypeOfTypeAnn(convertQualIdentFromSol(t.Ident))
	case *soltype.CondType:
		return NewCondTypeAnn(
			b.typeAnn(t.Check),
			b.typeAnn(t.Extends),
			b.typeAnn(t.Then),
			b.typeAnn(t.Else),
		)
	case *soltype.InferType:
		// The declaring clause renders `infer U`, a reference to it the bare `U`.
		if t.Binder {
			return NewInferTypeAnn(t.Name)
		}
		return NewRefTypeAnn(t.Name, nil)
	case *soltype.MappedKeyType:
		// The `K` of `T[K]`.
		return NewRefTypeAnn(t.Name, nil)
	case *soltype.RestSpreadType:
		return &RestSpreadTypeAnn{
			Value:  b.typeAnn(t.Operand),
			span:   nil,
			source: nil,
		}
	case *soltype.TemplateLitType:
		types := make([]TypeAnn, len(t.Interps))
		for i, interp := range t.Interps {
			types[i] = b.typeAnn(interp)
		}
		quasis := make([]*Quasi, len(t.Quasis))
		for i, quasi := range t.Quasis {
			quasis[i] = &Quasi{Value: quasi, Span: nil}
		}
		return NewTemplateLitTypeAnn(quasis, types)
	case *soltype.StringIntrinsicType:
		// TypeScript ships these as generic aliases, so `Uppercase<T>` is a
		// reference under the operator's own name.
		return NewRefTypeAnn(t.Kind.String(), []TypeAnn{b.typeAnn(t.Operand)})
	case *soltype.ExactnessType:
		// These set and clear a trailing `...` marker TypeScript has no form for,
		// so the operand renders alone. M10 owns carrying exactness across.
		return b.typeAnn(t.Operand)
	case *soltype.RecursiveType:
		return b.recursiveTypeAnn(t)
	case *soltype.RecursiveVarType:
		if name, named := b.recursiveNames[t.ID]; named {
			return NewRefTypeAnn(name, nil)
		}
		// Only reachable for a knot whose companion could not be minted, where the
		// body rendered to something no interface can hold.
		return NewAnyTypeAnn(nil)
	case *soltype.NegationType:
		// TypeScript has no complement. `unknown` reduces the `A & ~B` a narrowed
		// match remainder produces to `A`, keeping the type it refines.
		return NewUnknownTypeAnn(nil)
	}
	panic(fmt.Sprintf("typeAnn: unhandled %T", t))
}

// recursiveTypeAnn names a μ-knot through a companion interface and returns a
// reference to it.
//
// TypeScript has no inline form for a recursive type, so `μX0.{next: X0}` would
// otherwise render one level of its unfolding with `any` where the recursion
// closes, and a reader of `{next: {next: any}}` could assign anything two steps
// in. An interface may name itself, so the knot emits as one.
//
// The binder is registered before the body renders, which is what lets a
// reference back to it resolve to the name.
func (b *solTypeAnnBuilder) recursiveTypeAnn(t *soltype.RecursiveType) TypeAnn {
	name := b.claimCompanionName()
	b.recursiveNames[t.Binder.ID] = name
	body := b.typeAnn(t.Body)

	object, isObject := body.(*ObjectTypeAnn)
	if !isObject {
		// An interface holds an object and nothing else, and a type alias naming
		// itself outside one is the error "Type alias circularly references
		// itself". A knot over a union has neither form, so it keeps the older
		// rendering: one level of the unfolding, with `any` at the binder.
		delete(b.recursiveNames, t.Binder.ID)
		return b.typeAnn(t.Body)
	}

	b.companions = append(b.companions, &TypeDecl{
		Name:       NewIdentifier(name, nil),
		TypeParams: nil,
		TypeAnn:    object,
		Interface:  true,
		declare:    false, // not exported, matching the `Self` companion in dts.go
		export:     false,
		span:       nil,
		source:     nil,
	})
	return NewRefTypeAnn(name, nil)
}

// claimCompanionName is the next name for a minted declaration, seeded by the
// prefix the caller gave so two bindings in one file cannot collide. It matches
// the shape of the `__<name>_self__` companion dts.go already emits.
func (b *solTypeAnnBuilder) claimCompanionName() string {
	return "__" + b.companionPrefix + "_rec" + strconv.Itoa(len(b.companions)) + "__"
}

// preludeTypeScriptArity is how many type arguments TypeScript's own declaration
// takes, for each `std:prelude` type whose Escalier declaration adds a trailing
// `E` for what the value may raise. Emitting that argument would hand the
// reference more than the TypeScript library declares, which the use site
// rejects. Only these four carry one; the prelude's iteration types declare what
// TypeScript does.
//
// TODO(#385): Preserve the dropped raise type as TSDoc metadata.
var preludeTypeScriptArity = map[string]int{
	"Promise":        1,
	"PromiseLike":    1,
	"Generator":      3,
	"AsyncGenerator": 3,
}

// typeArgs renders a reference's type arguments, dropping any TypeScript's own
// declaration does not take.
func (b *solTypeAnnBuilder) typeArgs(name string, args []soltype.Type) []TypeAnn {
	if arity, declared := b.typeScriptArity(name); declared && len(args) > arity {
		args = args[:arity]
	}
	typeArgs := make([]TypeAnn, len(args))
	for i, arg := range args {
		typeArgs[i] = b.typeAnn(arg)
	}
	return typeArgs
}

// typeScriptArity reports how many type arguments TypeScript declares for this
// reference, and false when it declares what Escalier does.
//
// The name must be one the prelude registered, since a user's own `Promise` is a
// different type that keeps every argument. An empty prefix matches nothing,
// which trims no reference rather than trimming by bare name.
func (b *solTypeAnnBuilder) typeScriptArity(qualifiedName string) (int, bool) {
	if b.preludePrefix == "" {
		return 0, false
	}
	local, inPrelude := strings.CutPrefix(qualifiedName, b.preludePrefix+".")
	if !inPrelude {
		return 0, false
	}
	arity, declared := preludeTypeScriptArity[local]
	return arity, declared
}

// objectTypeAnn renders an object type.
//
// TypeScript requires a mapped type to be the SOLE member of its type literal,
// TS7061. Escalier has no such rule, so an object holding one beside anything
// else emits as an intersection: one literal per mapped member plus one for the
// rest. `{name: string, [K: keyof T]: T[K]}` emits
// `{[K in keyof T]: T[K]} & {name: string}`.
//
// An index signature is exempt. It is a settled mapped member here, and
// indexSignatureTypeAnn lowers it to `[key: string]: V`, which TypeScript allows
// beside ordinary members.
func (b *solTypeAnnBuilder) objectTypeAnn(t *soltype.ObjectType) TypeAnn {
	var mappedAnns []TypeAnn
	otherElems := make([]ObjTypeAnnElem, 0, len(t.Elems))
	for _, elem := range t.Elems {
		if mapped, ok := elem.(*soltype.MappedElem); ok && !soltype.MappedElemSettled(mapped) {
			mappedAnns = append(mappedAnns, NewObjectTypeAnn(b.objTypeAnnElems(mapped)))
			continue
		}
		otherElems = append(otherElems, b.objTypeAnnElems(elem)...)
	}

	// One mapped member on its own is already a valid literal.
	if len(mappedAnns) > 1 || (len(mappedAnns) == 1 && len(otherElems) > 0) {
		intersectionTypes := mappedAnns
		if len(otherElems) > 0 {
			intersectionTypes = append(intersectionTypes, NewObjectTypeAnn(otherElems))
		}
		return NewIntersectionTypeAnn(intersectionTypes)
	}
	if len(mappedAnns) == 1 {
		return mappedAnns[0]
	}
	return NewObjectTypeAnn(otherElems)
}

// objTypeAnnElems lowers one soltype ObjTypeElem to the elements it emits. A
// TypeScript overload set is one sibling declaration per arm, so a method,
// constructor, or call signature fans out; one with no arm emits nothing.
func (b *solTypeAnnBuilder) objTypeAnnElems(elem soltype.ObjTypeElem) []ObjTypeAnnElem {
	switch elem := elem.(type) {
	case *soltype.PropertyElem:
		return []ObjTypeAnnElem{&PropertyTypeAnn{
			Name:     buildTypeAnnObjKeyFromSol(elem.Name),
			Optional: elem.Optional,
			Readonly: elem.Readonly,
			Value:    b.typeAnn(elem.Type),
		}}
	case *soltype.MethodElem:
		out := make([]ObjTypeAnnElem, len(elem.Signatures))
		for i, fn := range elem.Signatures {
			out[i] = &MethodTypeAnn{
				Name:     buildTypeAnnObjKeyFromSol(elem.Name),
				Fn:       b.funcTypeAnn(fn),
				Optional: elem.Optional,
			}
		}
		return out
	case *soltype.ConstructorElem:
		out := make([]ObjTypeAnnElem, len(elem.Signatures))
		for i, fn := range elem.Signatures {
			out[i] = &ConstructorTypeAnn{Fn: b.funcTypeAnn(fn)}
		}
		return out
	case *soltype.CallableElem:
		out := make([]ObjTypeAnnElem, len(elem.Signatures))
		for i, fn := range elem.Signatures {
			out[i] = &CallableTypeAnn{Fn: b.funcTypeAnn(fn)}
		}
		return out
	case *soltype.GetterElem:
		// soltype carries the value a getter returns rather than a signature, so
		// the parameterless one TypeScript wants is built here.
		return []ObjTypeAnnElem{&GetterTypeAnn{
			Name: buildTypeAnnObjKeyFromSol(elem.Name),
			Fn: FuncTypeAnn{
				TypeParams: nil,
				Params:     nil,
				Return:     b.typeAnn(elem.Type),
				Throws:     nil,
				span:       nil,
				source:     nil,
			},
		}}
	case *soltype.SetterElem:
		// TypeScript forbids a return type on a setter, so the printer drops the
		// Return this fills in.
		return []ObjTypeAnnElem{&SetterTypeAnn{
			Name: buildTypeAnnObjKeyFromSol(elem.Name),
			Fn: FuncTypeAnn{
				TypeParams: nil,
				Params: []*Param{{
					Pattern:  NewIdentPat("value", nil, nil),
					Optional: false,
					TypeAnn:  b.typeAnn(elem.Param),
				}},
				Return: NewVoidTypeAnn(nil),
				Throws: nil,
				span:   nil,
				source: nil,
			},
		}}
	case *soltype.SpreadElem:
		return []ObjTypeAnnElem{&RestSpreadTypeAnn{
			Value:  b.typeAnn(elem.Type),
			span:   nil,
			source: nil,
		}}
	case *soltype.MappedElem:
		if soltype.MappedElemSettled(elem) {
			return []ObjTypeAnnElem{b.indexSignatureTypeAnn(elem)}
		}
		return []ObjTypeAnnElem{b.mappedTypeAnn(elem)}
	}
	panic(fmt.Sprintf("objTypeAnnElems: unhandled %T", elem))
}

// indexSignatureTypeAnn renders a settled mapped member as `[key: Keys]: Value`.
// The `?` such a member always carries is dropped: TypeScript writes an index
// signature without one and settles the question with
// `noUncheckedIndexedAccess`.
func (b *solTypeAnnBuilder) indexSignatureTypeAnn(elem *soltype.MappedElem) *IndexSignatureTypeAnn {
	keyName := indexSignatureKeyName(elem.Keys)
	if referencesMappedKey(elem.Value, elem.Key) {
		// `{[K: string]?: T[K]}` names its key in the value, so the conventional
		// name would leave `T[K]` pointing at nothing.
		keyName = elem.Key.Name
	}
	return &IndexSignatureTypeAnn{
		KeyName:  keyName,
		KeyType:  b.typeAnn(elem.Keys),
		Value:    b.typeAnn(elem.Value),
		Readonly: elem.Readonly == soltype.ModAdd,
	}
}

// indexSignatureKeyName is the key name TypeScript's own declarations use.
func indexSignatureKeyName(keys soltype.Type) string {
	prim, isPrim := keys.(*soltype.PrimType)
	if !isPrim {
		return "key"
	}
	switch prim.Prim {
	case soltype.NumPrim:
		return "index"
	case soltype.SymPrim:
		return "sym"
	case soltype.StrPrim, soltype.BoolPrim, soltype.BigIntPrim:
		// Only string, number, and symbol key sets settle, so the last two never
		// reach here.
		return "key"
	}
	return "key"
}

// referencesMappedKey reports whether t names the key a mapped member binds.
func referencesMappedKey(t soltype.Type, key *soltype.MappedKeyType) bool {
	if t == nil || key == nil {
		return false
	}
	visitor := &solMappedKeyVisitor{key: key, found: false}
	t.Accept(visitor, soltype.Positive)
	return visitor.found
}

type solMappedKeyVisitor struct {
	key   *soltype.MappedKeyType
	found bool
}

func (v *solMappedKeyVisitor) EnterType(t soltype.Type, _ soltype.Polarity) soltype.EnterResult {
	// Matched by id, so a nested member writing the same name stays separate.
	if ref, ok := t.(*soltype.MappedKeyType); ok && ref.ID == v.key.ID {
		v.found = true
		return soltype.EnterResult{Type: nil, SkipChildren: true}
	}
	return soltype.EnterResult{Type: nil, SkipChildren: false}
}

func (v *solMappedKeyVisitor) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type {
	return t
}

// mappedTypeAnn renders a mapped member as `[K in Keys]: V`. The
// `if Check : Extends` filter is dropped, matching the type_system and AST
// paths; lowering it to an `as` clause belongs with P4.3's goldens.
func (b *solTypeAnnBuilder) mappedTypeAnn(elem *soltype.MappedElem) *MappedTypeAnn {
	var nameTypeAnn TypeAnn
	if elem.Name != nil {
		nameTypeAnn = b.typeAnn(elem.Name)
	}
	return &MappedTypeAnn{
		TypeParam: &IndexParamTypeAnn{
			Name:       elem.Key.Name,
			Constraint: b.typeAnn(elem.Keys),
		},
		Name:     nameTypeAnn,
		Value:    b.typeAnn(elem.Value),
		Optional: mapMappedModifierFromSol(elem.Optional),
		ReadOnly: mapMappedModifierFromSol(elem.Readonly),
	}
}

// funcTypeToParams renders a signature's declared parameters. The `self`
// receiver is not among them, since TypeScript carries it implicitly.
func (b *solTypeAnnBuilder) funcTypeToParams(funcType *soltype.FuncType) []*Param {
	params := make([]*Param, len(funcType.Params))
	names := &paramNamer{n: 0}
	for i, param := range funcType.Params {
		pattern, named := patToPatFromSol(param.Pattern, names)
		if !named {
			pattern = NewIdentPat(names.next(), nil, nil)
		}
		if param.Rest {
			// soltype marks a rest parameter with a flag; TypeScript writes the
			// `...` on the binding itself.
			pattern = NewRestPat(pattern, nil)
		}
		params[i] = &Param{
			Pattern:  pattern,
			Optional: param.Optional,
			TypeAnn:  b.typeAnn(param.Type),
		}
	}
	return params
}

// paramNamer names the binding positions a signature's patterns leave unnamed,
// `arg0`, `arg1` and so on. One namer serves a whole signature, nested positions
// included, so no two names collide.
type paramNamer struct {
	n int
}

func (p *paramNamer) next() string {
	name := "arg" + strconv.Itoa(p.n)
	p.n++
	return name
}

// funcTypeAnn renders a signature. What it raises is dropped: TypeScript has no
// throws clause.
func (b *solTypeAnnBuilder) funcTypeAnn(funcType *soltype.FuncType) FuncTypeAnn {
	b.bindTypeParams(funcType.TypeParams)
	inferred := b.bindInferredTypeParams(funcType)

	typeParams := inferred
	if len(funcType.TypeParams) > 0 {
		typeParams = make([]*TypeParam, len(funcType.TypeParams), len(funcType.TypeParams)+len(inferred))
		for i, param := range funcType.TypeParams {
			var constraint TypeAnn
			if param.Constraint != nil {
				constraint = b.typeAnn(param.Constraint)
			}
			var defaultType TypeAnn
			if param.Default != nil {
				defaultType = b.typeAnn(param.Default)
			}
			typeParams[i] = &TypeParam{
				Name:       param.Name,
				Constraint: constraint,
				Default:    defaultType,
			}
		}
		typeParams = append(typeParams, inferred...)
	}

	return FuncTypeAnn{
		TypeParams: typeParams,
		Params:     b.funcTypeToParams(funcType),
		Return:     b.typeAnn(funcType.Ret),
		Throws:     nil,
		span:       nil,
		source:     nil,
	}
}

// bindInferredTypeParams names the variables let-generalization retained, which
// an un-annotated generic function declares none of. `fn f(x) { return x }`
// coalesces to `fn (x: t1) -> t1` with an empty TypeParams list, and rendering
// each `t1` alone would emit `(x: unknown) => unknown`, losing the link the
// variable carries. Naming it emits `<T0>(x: T0) => T0`.
//
// A variable binds here only when this signature holds every occurrence of it.
// One shared with a sibling, as in `{push: fn (v: t1) -> undefined, pop: fn () ->
// t1}`, belongs to a scope neither opens; binding it on the first would leave the
// second naming something TypeScript cannot see. Those stay `unknown`. See
// #1697.
func (b *solTypeAnnBuilder) bindInferredTypeParams(funcType *soltype.FuncType) []*TypeParam {
	var bind []*soltype.TypeVarType
	for _, v := range b.varOrder {
		if _, named := b.typeParamNames[v]; named {
			continue
		}
		if b.bindAt[v] != funcType {
			continue
		}
		bind = append(bind, v)
	}
	if len(bind) == 0 {
		return nil
	}

	// Name the whole batch before rendering any constraint, so a bound naming a
	// sibling of the batch reads as that sibling's name.
	for _, v := range bind {
		b.typeParamNames[v] = b.claimTypeParamName()
	}

	params := make([]*TypeParam, len(bind))
	for i, v := range bind {
		var constraint TypeAnn
		if len(v.UpperBounds) == 1 {
			// Several bounds would meet to an intersection, a shape the twin never
			// emitted and P4.3 has no golden for, so only a lone one renders.
			constraint = b.typeAnn(v.UpperBounds[0])
		}
		params[i] = &TypeParam{Name: b.typeParamNames[v], Constraint: constraint, Default: nil}
	}
	return params
}

// claimTypeParamName hands out the next `T0`, `T1` no binder in this render has
// taken, matching what soltype's scheme printer assigns.
func (b *solTypeAnnBuilder) claimTypeParamName() string {
	if b.typeParamNames == nil {
		b.typeParamNames = map[*soltype.TypeVarType]string{}
	}
	taken := make(map[string]bool, len(b.typeParamNames))
	for _, name := range b.typeParamNames {
		taken[name] = true
	}
	for i := b.nextTypeParam; ; i++ {
		name := "T" + strconv.Itoa(i)
		if !taken[name] {
			b.nextTypeParam = i + 1
			return name
		}
	}
}

// bindingSignatures returns, for each type variable, the innermost signature
// holding every occurrence of it, and every variable in the order the walk
// reached it.
//
// That signature is the one that may bind the variable. The outermost holding
// all occurrences would be wrong: in `fn () -> {put: fn (v: t1) -> t1}` the
// outer signature does hold t1, but binding it there hands the choice to
// whoever calls the outer function, when `put` alone is what is polymorphic.
// A variable whose occurrences share no enclosing signature, as the two
// siblings of `{push: fn (v: t1) -> undefined, pop: fn () -> t1}` do, maps to
// nil and renders `unknown`.
//
// Only the positions the renderer emits are walked. A variable reachable only
// through a `self` receiver, a throws clause, or another variable's bounds
// would otherwise earn a binder appearing nowhere in the signature.
func bindingSignatures(t soltype.Type) (map[*soltype.TypeVarType]*soltype.FuncType, []*soltype.TypeVarType) {
	v := &solVarScopeVisitor{
		stack:    nil,
		enclosed: map[*soltype.TypeVarType][]*soltype.FuncType{},
		order:    nil,
	}
	t.Accept(v, soltype.Positive)

	at := make(map[*soltype.TypeVarType]*soltype.FuncType, len(v.order))
	for _, tv := range v.order {
		if shared := v.enclosed[tv]; len(shared) > 0 {
			at[tv] = shared[len(shared)-1]
		}
	}
	return at, v.order
}

// solVarScopeVisitor records the signatures enclosing every occurrence of each
// variable, narrowing to the ones common to all of them.
type solVarScopeVisitor struct {
	stack    []*soltype.FuncType
	enclosed map[*soltype.TypeVarType][]*soltype.FuncType
	order    []*soltype.TypeVarType
}

func (v *solVarScopeVisitor) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	switch t := t.(type) {
	case *soltype.TypeVarType:
		if t == nil {
			return soltype.EnterResult{Type: nil, SkipChildren: true}
		}
		if seen, ok := v.enclosed[t]; ok {
			v.enclosed[t] = commonPrefix(seen, v.stack)
		} else {
			v.order = append(v.order, t)
			v.enclosed[t] = append([]*soltype.FuncType(nil), v.stack...)
		}
	case *soltype.FuncType:
		// Walk the emitted positions by hand and skip the rest. Polarity is threaded
		// the way Accept would, though nothing here reads it.
		v.stack = append(v.stack, t)
		for _, param := range t.Params {
			if param.Type != nil {
				param.Type.Accept(v, pol.Flip())
			}
		}
		if t.Ret != nil {
			t.Ret.Accept(v, pol)
		}
		v.stack = v.stack[:len(v.stack)-1]
		return soltype.EnterResult{Type: nil, SkipChildren: true}
	}
	return soltype.EnterResult{Type: nil, SkipChildren: false}
}

func (v *solVarScopeVisitor) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type {
	return t
}

// commonPrefix truncates a to the signatures it shares with b from the outside
// in. a is the walk's own copy, so truncating it aliases nothing.
func commonPrefix(a, b []*soltype.FuncType) []*soltype.FuncType {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return a[:i]
		}
	}
	return a[:n]
}

// buildTypeAnnObjKeyFromSol renders a member name as an object key. soltype
// stores one keyed off a well-known symbol under a reserved `@@name` spelling,
// which renders back as `[Symbol.name]`. Every other name is a string literal,
// which the printer emits bare when it is a valid identifier.
func buildTypeAnnObjKeyFromSol(name string) ObjKey {
	if sym, isSymbol := soltype.SymbolOfMemberName(name); isSymbol {
		return NewComputedKey(
			NewMemberExpr(NewIdentExpr("Symbol", "", nil), NewIdentifier(sym, nil), false, nil),
			nil,
		)
	}
	return NewStrLit(name, nil)
}

// convertQualIdentFromSol splits a dotted reference such as `p.inner`, which
// soltype carries as one string, into the identifier a `typeof` holds.
func convertQualIdentFromSol(ident string) QualIdent {
	parts := strings.Split(ident, ".")
	var qual QualIdent = NewIdent(parts[0], Span{})
	for _, part := range parts[1:] {
		qual = &Member{Left: qual, Right: NewIdent(part, Span{})}
	}
	return qual
}

func mapMappedModifierFromSol(mod soltype.MappedModifier) *MappedModifier {
	switch mod {
	case soltype.ModNone:
		return nil
	case soltype.ModAdd:
		result := MMAdd
		return &result
	case soltype.ModRemove:
		result := MMRemove
		return &result
	}
	panic(fmt.Sprintf("mapMappedModifierFromSol: unhandled MappedModifier %d", int(mod)))
}

func litToLitFromSol(lit soltype.Lit) Lit {
	switch lit := lit.(type) {
	case *soltype.BoolLit:
		return NewBoolLit(lit.Value, nil)
	case *soltype.NumLit:
		return NewNumLit(lit.Value, nil)
	case *soltype.StrLit:
		return NewStrLit(lit.Value, nil)
	}
	panic(fmt.Sprintf("litToLitFromSol: unhandled %T", lit))
}

// patToPatFromSol renders a parameter pattern, reporting false when it binds no
// name TypeScript can write. The caller then draws one from names, since
// TypeScript needs a name at every binding position. A sub-pattern is named here
// instead, because only this walk reaches it.
func patToPatFromSol(pat soltype.Pat, names *paramNamer) (Pat, bool) {
	switch pat := pat.(type) {
	case *soltype.IdentPat:
		return NewIdentPat(pat.Name, nil, nil), true
	case *soltype.TuplePat:
		elems := make([]Pat, len(pat.Elems))
		for i, elem := range pat.Elems {
			sub, named := patToPatFromSol(elem, names)
			if !named {
				sub = NewIdentPat(names.next(), nil, nil)
			}
			elems[i] = sub
		}
		return NewTuplePat(elems, nil), true
	case *soltype.ObjectPat:
		elems := make([]ObjPatElem, 0, len(pat.Fields)+1)
		for _, field := range pat.Fields {
			value, named := patToPatFromSol(field.Value, names)
			if !named {
				value = NewIdentPat(names.next(), nil, nil)
			}
			elems = append(elems, NewObjKeyValuePat(field.Name, value, nil, nil))
		}
		if pat.Rest != nil {
			rest, named := patToPatFromSol(pat.Rest, names)
			if !named {
				rest = NewIdentPat(names.next(), nil, nil)
			}
			elems = append(elems, NewObjRestPat(rest, nil))
		}
		return NewObjectPat(elems, nil), true
	case *soltype.RestPat:
		// Binding the tail is a fact about the element, not the sub-pattern it binds
		// through: dropping the `...` would turn `[a, ..._]` into `[a, arg0]`,
		// binding one element where the source bound every remaining one.
		sub, named := patToPatFromSol(pat.Pattern, names)
		if !named {
			sub = NewIdentPat(names.next(), nil, nil)
		}
		return NewRestPat(sub, nil), true
	case *soltype.InstancePat:
		// It binds through its object part, and the class it names is already on the
		// parameter's type, so `Point {x}: Point` emits `{x}: Point`.
		if pat.Object == nil {
			return nil, false
		}
		return patToPatFromSol(pat.Object, names)
	}
	// A wildcard, a literal, `null`, `undefined`, and a nil pattern bind nothing.
	// An extractor binds positionally behind a constructor, which no TypeScript
	// binding form takes apart.
	return nil, false
}

// refNameFromSol is the name a nominal reference renders under. A class or alias
// carries its registry key, which for another package's declaration leads with an
// `import:<uri>.` prefix TypeScript cannot write. Dropping it keeps the namespace
// path: `import:std:array.Foo` renders `Foo`, and `Geometry.Point` is unchanged.
//
// The solver escapes the dots inside the URI, so the first dot after the prefix
// is where the path starts.
func refNameFromSol(qualifiedName string) string {
	// The marker the solver writes at the head of a package's registry keys; see
	// packageKeyHead in internal/solver/infer_class.go.
	const packageKeyHead = "import:"
	if !strings.HasPrefix(qualifiedName, packageKeyHead) {
		return qualifiedName
	}
	if dot := strings.Index(qualifiedName, "."); dot != -1 {
		return qualifiedName[dot+1:]
	}
	return qualifiedName
}

// containsSelfTypeFromSol reports whether t mentions `Self`. A binding whose type
// does renders as an interface, since `this` is only legal inside one.
func containsSelfTypeFromSol(t soltype.Type) bool {
	visitor := &solSelfTypeVisitor{found: false}
	t.Accept(visitor, soltype.Positive)
	return visitor.found
}

type solSelfTypeVisitor struct {
	found bool
}

func (v *solSelfTypeVisitor) EnterType(t soltype.Type, _ soltype.Polarity) soltype.EnterResult {
	if _, ok := t.(*soltype.SelfType); ok {
		v.found = true
		return soltype.EnterResult{Type: nil, SkipChildren: true}
	}
	return soltype.EnterResult{Type: nil, SkipChildren: false}
}

func (v *solSelfTypeVisitor) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type {
	return t
}
