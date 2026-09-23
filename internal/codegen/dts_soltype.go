package codegen

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/escalier-lang/escalier/internal/soltype"
)

// dts_soltype.go renders solver types as TypeScript declarations. It is the twin
// of the type_system-driven half of dts.go, taking the same output from a
// different input. solTypeAnnBuilder.typeAnn is the entry point, standing where
// dts.go's buildTypeAnn stands. The `FromSol` suffix on the free functions here
// marks the soltype-driven counterpart of the type_system-driven one in dts.go.
// Deleting internal/type_system retires the originals, and the suffix goes with
// them.
//
// The port is a twin rather than a soltype-to-type_system bridge. A bridge would
// have to rebuild the representation the solver migration exists to retire, and
// it would keep type_system alive past the cleanup.
//
// A former TypeScript cannot express degrades rather than failing. Ownership,
// exactness, and what a call raises are erased. A complement becomes `unknown`
// and a recursive knot `any`. Each arm below says which it is and why.

// solTypeAnnBuilder renders soltype values as .d.ts type annotations.
type solTypeAnnBuilder struct {
	// preludePrefix is the package key `std:prelude` is registered under, such as
	// `import:std:prelude`. Four of its declarations take one more type parameter
	// than TypeScript's, and the renderer has to know which references are to
	// those declarations rather than to a user's own type of the same name. The
	// prefix is what tells them apart, since every registry key leads with the
	// key of the package that declared it. An empty value trims nothing.
	preludePrefix string
	// typeParamNames is the name each quantified type parameter renders under. A
	// soltype type parameter is an inference variable that a TypeParam binds
	// through its Var field, not a reference by name, so a `T` written in a
	// signature body arrives here as the same *soltype.TypeVarType pointer the
	// binder holds. This map is what turns that pointer back into the `T` the
	// source wrote.
	typeParamNames map[*soltype.TypeVarType]string
	// nextTypeParam is the index the next name for a retained variable is drawn
	// from, so two signatures in one rendered type never reuse a name.
	nextTypeParam int
	// varOccurrences counts how often each variable occurs in the whole type being
	// rendered, which is what decides whether a signature holds all of a variable's
	// occurrences and may therefore bind it. render fills it; a nil map binds
	// nothing, so a caller reaching typeAnn directly renders every retained
	// variable as `unknown`.
	varOccurrences map[*soltype.TypeVarType]int
}

// newSolTypeAnnBuilder returns a renderer for one declaration.
//
// preludePrefix is the package key `std:prelude` is registered under. The solver
// settles the prelude's `Promise` to a qualified name at the start of a run, and
// that name's prefix is this one. typeParams are the declaration's own type
// parameters. A generic class or alias declares them on the declaration, so
// nothing inside its body binds them and a use of one would otherwise reach the
// unresolved-variable fallback.
func newSolTypeAnnBuilder(preludePrefix string, typeParams []*soltype.TypeParam) *solTypeAnnBuilder {
	b := &solTypeAnnBuilder{
		preludePrefix:  preludePrefix,
		typeParamNames: nil,
		nextTypeParam:  0,
		varOccurrences: nil,
	}
	b.bindTypeParams(typeParams)
	return b
}

// bindTypeParams registers each parameter's inference variable under the name its
// binder declared, so a use of the parameter renders as that name. A parameter
// with no declared name is left unregistered and falls back to `unknown`.
//
// A binding is never removed. Each binder mints its own variable, so two binders
// that both write `T` hold different pointers and neither can shadow the other.
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

// render is the entry point for a whole type. It counts each variable's
// occurrences first, which is what lets a signature tell a variable it fully
// contains from one it shares with a sibling.
func (b *solTypeAnnBuilder) render(t soltype.Type) TypeAnn {
	if b.typeParamNames == nil {
		b.typeParamNames = map[*soltype.TypeVarType]string{}
	}
	b.varOccurrences = map[*soltype.TypeVarType]int{}
	collectRenderedVars(t, b.varOccurrences)
	return b.typeAnn(t)
}

func (b *solTypeAnnBuilder) typeAnn(t soltype.Type) TypeAnn {
	switch t := t.(type) {
	case *soltype.TypeVarType:
		// A quantified type parameter renders under the name its binder declared,
		// or under the one funcTypeAnn assigned it.
		if name, ok := b.typeParamNames[t]; ok {
			return NewRefTypeAnn(name, nil)
		}
		// A variable reaching here stands outside every function in the rendered
		// type, so there is no signature to hang a `<T>` on. Coalescing retains one
		// only where it occurs at both polarities, which for a value binding such
		// as `val xs = []` means its element type is genuinely undetermined.
		// `unknown` is the honest rendering of that.
		return NewUnknownTypeAnn(nil)
	case *soltype.SkolemType:
		// A skolem is a rigid type parameter held abstract while a term is checked.
		// It renders under its source name, the way a reference to that parameter does.
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
		// The error-recovery sentinel absorbs every constraint in both directions,
		// which is what `any` does in TypeScript. Emitting it keeps a declaration
		// built after a reported error from cascading a second one downstream.
		return NewAnyTypeAnn(nil)
	case *soltype.UniqueSymbolType:
		return NewUniqueSymbolTypeAnn(nil)
	case *soltype.SelfType:
		// `Self` denotes the receiver's class, which TypeScript spells `this`.
		// The rewrite lives here rather than in a separate pass because soltype
		// carries `Self` as a kind of its own. The renderer can tell a written
		// `Self` from the class it was declared in without inspecting a name.
		return NewRefTypeAnn("this", nil)
	case *soltype.ClassType:
		return NewRefTypeAnn(refNameFromSol(t.Name), b.typeArgs(t.Name, t.TypeArgs))
	case *soltype.AliasType:
		return NewRefTypeAnn(refNameFromSol(t.Name), b.typeArgs(t.Name, t.TypeArgs))
	case *soltype.GeneratorType:
		// TypeScript's `Generator` and `AsyncGenerator` take three type arguments.
		// Escalier tracks a fourth, what advancing the generator may raise, and
		// TypeScript has no slot for it.
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
		// Ownership and borrowing have no TypeScript form, so a reference renders
		// as the value it points at. This mirrors how the type_system twin renders
		// a MutType.
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
		// The declaring clause renders `infer U`. A reference to it from the Then
		// branch renders the bare `U`. soltype tells the two apart by the Binder
		// flag, where type_system carried the reference as a named type reference.
		if t.Binder {
			return NewInferTypeAnn(t.Name)
		}
		return NewRefTypeAnn(t.Name, nil)
	case *soltype.MappedKeyType:
		// A reference to the key a mapped type binds, the `K` of `T[K]`.
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
		// TypeScript ships the four string intrinsics as generic aliases, so
		// `Uppercase<T>` renders as a reference under the operator's own name.
		return NewRefTypeAnn(t.Kind.String(), []TypeAnn{b.typeAnn(t.Operand)})
	case *soltype.ExactnessType:
		// `Exact<T>` and `Inexact<T>` set and clear a trailing `...` marker, which
		// TypeScript has no form for. Rendering the operand erases the marker the
		// way every other exactness flag is erased here. M10 owns carrying it
		// across in JSDoc.
		return b.typeAnn(t.Operand)
	case *soltype.RecursiveType:
		// TypeScript names a recursive type through an interface, so a μ-knot has
		// no form as an inline annotation. Rendering one level of the unfolding and
		// standing `any` in at the binder keeps the shape a reader sees while
		// leaving the recursive position unchecked.
		return b.typeAnn(t.Body)
	case *soltype.RecursiveVarType:
		return NewAnyTypeAnn(nil)
	case *soltype.NegationType:
		// TypeScript has no complement type. A complement reaches emission inside
		// an intersection, as the `A & ~B` a narrowed match remainder produces, and
		// rendering the complement as `unknown` reduces that to `A`. The refinement
		// TypeScript cannot express is dropped and the type it refines survives.
		return NewUnknownTypeAnn(nil)
	}
	panic(fmt.Sprintf("typeAnn: unhandled %T", t))
}

// preludeTypeScriptArity is how many type arguments TypeScript's own declaration
// takes, for each `std:prelude` type whose Escalier declaration adds one.
//
// Escalier tracks what a value may raise as a trailing type parameter TypeScript
// has no slot for: the `E` of `Promise<T, E>` and `PromiseLike<T, E>`, and of
// `Generator<T, TReturn, TNext, E>` and `AsyncGenerator<T, TReturn, TNext, E>`.
// Emitting it would hand the reference more arguments than the TypeScript library
// declares, which the use site rejects.
//
// Only these four carry one. The prelude's `Iterator`, `AsyncIterator`,
// `Iterable`, and `AsyncIterable` each declare the parameters TypeScript does, so
// a reference to one renders with every argument it was given.
//
// TODO(#385): Preserve the dropped raise type as TSDoc metadata.
var preludeTypeScriptArity = map[string]int{
	"Promise":        1,
	"PromiseLike":    1,
	"Generator":      3,
	"AsyncGenerator": 3,
}

// typeArgs renders a nominal reference's type arguments, dropping the ones
// TypeScript's declaration of the same name does not take.
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

// typeScriptArity reports how many type arguments TypeScript's declaration of
// this reference takes, and false for a reference TypeScript declares with the
// parameters Escalier does.
//
// The name has to be one the prelude registered. A user's own `Promise` is a
// different type that keeps every argument it declared, and its registry key
// leads with the key of the package that declared it rather than the prelude's.
// An empty preludePrefix matches nothing, so a caller that has not settled the
// prelude trims no reference rather than trimming by bare name.
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

// objectTypeAnn renders an object type, splitting it into an intersection where
// TypeScript's sole-member rule for mapped types demands one.
func (b *solTypeAnnBuilder) objectTypeAnn(t *soltype.ObjectType) TypeAnn {
	// TypeScript requires a mapped type to be the SOLE member of its type literal. A literal
	// holding one alongside anything else is the error "A mapped type may not declare properties
	// or methods", TS7061. So an object carrying a mapped member together with any other member
	// is emitted as an intersection: one literal per mapped member, plus one holding the rest.
	//
	// Escalier has no such restriction, so `{name: string, [K: keyof T]: T[K]}` is a single
	// object here and emits `{[K in keyof T]: T[K]} & {name: string}`.
	//
	// An index signature is exempt. soltype stores one as a settled mapped member, which
	// indexSignatureTypeAnn lowers to `[key: string]: V` rather than to a mapped type, and
	// TypeScript allows that beside ordinary members.
	var mappedAnns []TypeAnn
	otherElems := make([]ObjTypeAnnElem, 0, len(t.Elems))
	for _, elem := range t.Elems {
		if mapped, ok := elem.(*soltype.MappedElem); ok && !soltype.MappedElemSettled(mapped) {
			// Each mapped element gets its own object type in the intersection.
			mappedAnns = append(mappedAnns, NewObjectTypeAnn(b.objTypeAnnElems(mapped)))
			continue
		}
		// An overloaded method or call signature contributes one element per arm.
		otherElems = append(otherElems, b.objTypeAnnElems(elem)...)
	}

	// One mapped member on its own already is a valid literal, so it needs no intersection.
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

// objTypeAnnElems lowers a single soltype ObjTypeElem to the .d.ts
// ObjTypeAnnElems it emits.
//
// A method, constructor, or call signature holding several overload arms fans out
// to one element per arm, because a TypeScript overload set is one sibling
// declaration per arm and a single ObjTypeAnnElem cannot carry several. One
// holding no arm emits nothing, since it describes no callable. Every other
// element kind emits exactly one.
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
		// A getter takes no parameters in TypeScript. soltype carries the value it
		// returns directly rather than as a signature, so the signature is built here.
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
		// A setter takes the written value as its one parameter. TypeScript forbids
		// a return type on a setter, so the printer drops the Return this fills in.
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
// Escalier writes an index signature as a mapped member over an uncountable key
// set, which is the shape MappedElemSettled recognizes.
//
// The `?` such a member always carries has no place in the emitted form.
// TypeScript writes an index signature without one and expresses the same fact
// through `noUncheckedIndexedAccess`, which decides at the reader's side whether a
// miss shows up as `undefined`.
func (b *solTypeAnnBuilder) indexSignatureTypeAnn(elem *soltype.MappedElem) *IndexSignatureTypeAnn {
	keyName := indexSignatureKeyName(elem.Keys)
	if referencesMappedKey(elem.Value, elem.Key) {
		// `{[K: string]?: T[K]}` names its key in the value it computes, so
		// renaming the key to the conventional one would leave `T[K]` pointing at
		// nothing. The member's own name is what the source wrote and is already a
		// valid identifier.
		keyName = elem.Key.Name
	}
	return &IndexSignatureTypeAnn{
		KeyName:  keyName,
		KeyType:  b.typeAnn(elem.Keys),
		Value:    b.typeAnn(elem.Value),
		Readonly: elem.Readonly == soltype.ModAdd,
	}
}

// indexSignatureKeyName is the name TypeScript's own declarations give an index
// signature's key, chosen by what the key set holds.
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
		// A string key set takes the default name. A boolean or bigint one names no
		// index signature at all, since UncountableKeys admits only string, number,
		// and symbol, so a member over either is never settled.
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
	// A nested mapped member writing the same name draws its own id, which is what
	// keeps its binding separate from this one.
	if ref, ok := t.(*soltype.MappedKeyType); ok && ref.ID == v.key.ID {
		v.found = true
		return soltype.EnterResult{Type: nil, SkipChildren: true}
	}
	return soltype.EnterResult{Type: nil, SkipChildren: false}
}

func (v *solMappedKeyVisitor) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type {
	return t
}

// mappedTypeAnn renders a mapped member as `[K in Keys]: V`.
//
// The `if Check : Extends` filter is dropped, matching what the type_system and
// AST paths emit. P4.3 reconciles the goldens and is where lowering it to a `as`
// clause belongs.
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

// funcTypeToParams renders a signature's declared parameters. The implicit
// `self` receiver is not among them. TypeScript methods carry their receiver
// implicitly, so an instance method's receiver has no slot in the emitted
// signature.
func (b *solTypeAnnBuilder) funcTypeToParams(funcType *soltype.FuncType) []*Param {
	params := make([]*Param, len(funcType.Params))
	names := &paramNamer{n: 0}
	for i, param := range funcType.Params {
		pattern, named := patToPatFromSol(param.Pattern, names)
		if !named {
			pattern = NewIdentPat(names.next(), nil, nil)
		}
		if param.Rest {
			// soltype marks a rest parameter with a flag beside an ordinary
			// pattern. TypeScript writes the `...` on the binding itself.
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

// paramNamer hands out names for the binding positions a signature's patterns
// leave unnamed, `arg0`, `arg1` and so on, the same recovery soltype's own printer
// makes. One namer serves a whole signature, including the positions nested inside
// a destructuring pattern, so no two names it hands out collide.
type paramNamer struct {
	n int
}

func (p *paramNamer) next() string {
	name := "arg" + strconv.Itoa(p.n)
	p.n++
	return name
}

// funcTypeAnn renders a signature. What the call may raise is dropped, because
// TypeScript has no throws clause.
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

// bindInferredTypeParams names the variables let-generalization retained in this
// signature and returns a type parameter for each.
//
// An un-annotated generic function declares nothing: inference quantifies it, and
// coalescing keeps a variable symbolic where it occurs at both polarities so the
// link between its uses survives. `fn f(x) { return x }` coalesces to
// `fn (x: t1) -> t1` with an empty TypeParams list, and rendering each `t1`
// independently would emit `(x: unknown) => unknown`, losing the very link the
// variable was retained to carry. Naming it emits `<T0>(x: T0) => T0`.
//
// A variable binds here only when this signature holds every occurrence of it in
// the whole rendered type. One shared with a sibling signature belongs to a scope
// neither of them opens, as the `t1` of `{push: fn (v: t1) -> undefined, pop: fn
// () -> t1}` does; binding it on the first would leave the second referencing a
// name TypeScript cannot see. Such a variable stays unnamed and renders `unknown`,
// which is lossy where an interface-level parameter would be exact, but is a
// declaration TypeScript accepts. The enclosing declaration is what could carry
// one, so P4.2 owns that.
func (b *solTypeAnnBuilder) bindInferredTypeParams(funcType *soltype.FuncType) []*TypeParam {
	local := map[*soltype.TypeVarType]int{}
	collectRenderedVars(funcType, local)

	var bind []*soltype.TypeVarType
	for _, v := range orderRenderedVars(funcType) {
		if _, named := b.typeParamNames[v]; named {
			continue
		}
		if local[v] != b.varOccurrences[v] {
			continue
		}
		bind = append(bind, v)
	}
	if len(bind) == 0 {
		return nil
	}

	// Name every variable in the batch before rendering any constraint, so a bound
	// naming a sibling of the same batch reads as that sibling's name.
	for _, v := range bind {
		b.typeParamNames[v] = b.claimTypeParamName()
	}

	params := make([]*TypeParam, len(bind))
	for i, v := range bind {
		var constraint TypeAnn
		if len(v.UpperBounds) == 1 {
			// A single upper bound renders as the `extends` clause it stands for.
			// Several would meet to an intersection, a shape the twin never emitted
			// and P4.3 has no golden for, so they are left off.
			constraint = b.typeAnn(v.UpperBounds[0])
		}
		params[i] = &TypeParam{Name: b.typeParamNames[v], Constraint: constraint, Default: nil}
	}
	return params
}

// claimTypeParamName hands out the next name no binder in this render has taken,
// `T0`, `T1` and so on, matching what soltype's own scheme printer assigns.
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

// collectRenderedVars counts each type variable's occurrences in t, and
// orderRenderedVars lists them in the order the walk first reaches one.
//
// Both count only the positions the renderer emits. A signature's `self` receiver
// and what it raises have no TypeScript form, so a variable reachable only through
// one of them would otherwise earn a binder that appears nowhere in the rendered
// signature. A variable's own bounds are a side graph rather than tree children,
// and the walk leaves them alone for the same reason: nothing reads a bound unless
// the variable it belongs to is itself named.
func collectRenderedVars(t soltype.Type, into map[*soltype.TypeVarType]int) {
	t.Accept(&solRenderedVarVisitor{counts: into}, soltype.Positive)
}

func orderRenderedVars(t soltype.Type) []*soltype.TypeVarType {
	visitor := &solRenderedVarVisitor{counts: map[*soltype.TypeVarType]int{}}
	t.Accept(visitor, soltype.Positive)
	return visitor.order
}

type solRenderedVarVisitor struct {
	counts map[*soltype.TypeVarType]int
	order  []*soltype.TypeVarType
}

func (v *solRenderedVarVisitor) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	switch t := t.(type) {
	case *soltype.TypeVarType:
		if t == nil {
			return soltype.EnterResult{Type: nil, SkipChildren: true}
		}
		if v.counts[t] == 0 {
			v.order = append(v.order, t)
		}
		v.counts[t]++
	case *soltype.FuncType:
		// Walk the emitted positions by hand and skip the rest. The polarity is
		// threaded the way Accept would, though nothing here reads it: a variable
		// counts the same whichever side of an arrow it stands on.
		for _, param := range t.Params {
			if param.Type != nil {
				param.Type.Accept(v, pol.Flip())
			}
		}
		if t.Ret != nil {
			t.Ret.Accept(v, pol)
		}
		return soltype.EnterResult{Type: nil, SkipChildren: true}
	}
	return soltype.EnterResult{Type: nil, SkipChildren: false}
}

func (v *solRenderedVarVisitor) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type {
	return t
}

// buildTypeAnnObjKeyFromSol renders a member name as a .d.ts object key. soltype
// stores a member keyed off a well-known symbol under a reserved `@@name`
// spelling, which renders back as the computed key `[Symbol.name]`. Every other
// name renders as a string literal, which the printer emits bare when it is a
// valid identifier.
func buildTypeAnnObjKeyFromSol(name string) ObjKey {
	if sym, isSymbol := soltype.SymbolOfMemberName(name); isSymbol {
		return NewComputedKey(
			NewMemberExpr(NewIdentExpr("Symbol", "", nil), NewIdentifier(sym, nil), false, nil),
			nil,
		)
	}
	return NewStrLit(name, nil)
}

// convertQualIdentFromSol splits a dotted value reference such as `p.inner` into
// the qualified identifier a `typeof` annotation holds. soltype carries the
// reference as one string, since it stays free of the AST.
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

// patToPatFromSol renders a parameter pattern, reporting false when the pattern
// binds no name TypeScript can write down.
//
// A wildcard, a literal, and a constructor or class pattern each match a value
// without naming it, and the solver leaves a pattern it has no counterpart for
// nil. TypeScript needs a name at every binding position, so the caller draws one
// from names rather than emitting a signature with an empty slot.
//
// A sub-pattern inside a tuple, an object, or a rest element is named here, since
// only this walk reaches it.
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
		// A rest element binds the tail, which is a fact about the pattern rather
		// than about the sub-pattern it binds through. Dropping the `...` for an
		// unnamed sub-pattern would turn `[a, ..._]` into `[a, arg0]`, binding one
		// element where the source bound every remaining one.
		sub, named := patToPatFromSol(pat.Pattern, names)
		if !named {
			sub = NewIdentPat(names.next(), nil, nil)
		}
		return NewRestPat(sub, nil), true
	case *soltype.InstancePat:
		// A class-instance pattern binds through its object part, and the class it
		// names is already carried by the parameter's own type. `Point {x}: Point`
		// binds the same name TypeScript's `{x}: Point` does.
		if pat.Object == nil {
			return nil, false
		}
		return patToPatFromSol(pat.Object, names)
	}
	// A wildcard, a literal, `null`, `undefined`, a nil pattern, and an extractor
	// all land here. None binds a name TypeScript can write: the first five bind
	// nothing at all, and an extractor binds its arguments positionally behind a
	// constructor, which no TypeScript binding form takes apart.
	return nil, false
}

// refNameFromSol is the name a nominal reference renders under.
//
// A soltype class or alias carries the key its declaration is registered under. A
// declaration from another package leads with an `import:<uri>.` prefix, which
// TypeScript cannot write, so the prefix is dropped and the namespace path behind
// it kept. `import:std:array.Foo` renders `Foo`, and a same-module `Geometry.Point`
// renders unchanged, which is also how an enum variant keeps its enum's name.
//
// The prefix holds no unescaped dot, because the solver escapes the dots in the
// URI when it builds the key, so the first dot after the prefix is where the
// namespace path starts.
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
// does renders as an interface, because TypeScript's `this` type is only legal
// inside one.
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
