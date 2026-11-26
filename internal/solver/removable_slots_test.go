package solver

import (
	"fmt"
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// TestBlameContentsBorrowed asserts that ContentsBorrowedError blames the receiver of the
// call and relates the expression that made the live borrow of the contents.
func TestBlameContentsBorrowed(t *testing.T) {
	src := `type Flat = {x: number}
declare fn first<'a>(a: &'a mut Array<Flat>) -> &'a mut Flat
fn g(mut arr: Array<Flat>) -> undefined {
	val f = first(&mut arr)
	val popped = arr.pop()
	f.x = 1
}`
	res := InferModuleAgainstStdlib(parseModule(t, src), benchTree, Options{})
	requireBlame(t, src, res.Errors,
		"5:15-5:18: cannot borrow 'arr' as mutable for this call while a borrow of its contents is live",
		"arr", "first(&mut arr)")
}

// TestHoldsRemovableSlotOfBuiltTypes covers the kinds of type that no parameter annotation
// in TestContentsBorrowedByContainerType reaches the walk with. Each case builds the type
// directly and asserts what holdsRemovableSlot answers for it.
func TestHoldsRemovableSlotOfBuiltTypes(t *testing.T) {
	res := InferModuleAgainstStdlib(parseModule(t, ""), benchTree, Options{})
	c := res.checker

	num := &soltype.PrimType{Prim: soltype.NumPrim}
	flat := &soltype.ObjectType{Elems: []soltype.ObjTypeElem{&soltype.PropertyElem{Name: "x", Type: num}}}
	withIndexSignature := &soltype.ObjectType{Elems: []soltype.ObjTypeElem{&soltype.MappedElem{
		Key:   &soltype.MappedKeyType{ID: 1, Name: "k"},
		Keys:  &soltype.PrimType{Prim: soltype.StrPrim},
		Value: flat,
	}}}
	binder := &soltype.RecursiveVarType{}

	tests := map[string]struct {
		t    soltype.Type
		want bool
	}{
		// `unknown` may hold an array, which a callee can narrow to and pop from.
		"Unknown": {t: &soltype.UnknownType{}, want: true},
		// An alias with no registered body has no slots the walk can see.
		"UnresolvedAlias": {t: &soltype.AliasType{Name: "Missing"}, want: true},
		// `keyof T` is a type operator the walk does not reduce.
		"TypeOperator":   {t: &soltype.KeyofType{Operand: flat}, want: true},
		"IndexSignature": {t: withIndexSignature, want: true},
		"Spread":         {t: &soltype.ObjectType{Elems: []soltype.ObjTypeElem{&soltype.SpreadElem{Type: flat}}}, want: true},
		"Skolem":         {t: &soltype.SkolemType{ID: 1, Name: "T"}, want: true},
		// A type parameter's variable has no lower bound, but it stands for any type.
		"RigidVariable": {t: &soltype.TypeVarType{ID: 1, Rigid: &soltype.RigidParam{Name: "T"}}, want: true},
		// An inference variable reads as its lower bounds.
		"VariableBoundedByAnArray": {
			t:    &soltype.TypeVarType{ID: 2, LowerBounds: []soltype.Type{c.ctx.arrayOf(flat)}},
			want: true,
		},
		"VariableBoundedByAFixedShape": {
			t:    &soltype.TypeVarType{ID: 3, LowerBounds: []soltype.Type{flat}},
			want: false,
		},
		// A variable bounded above by `number` holds only numbers.
		"VariableOfNumbers": {t: &soltype.TypeVarType{ID: 4, UpperBounds: []soltype.Type{num}}, want: false},
		"IntersectionWithAnArray": {
			t:    &soltype.IntersectionType{Types: []soltype.Type{flat, c.ctx.arrayOf(flat)}},
			want: true,
		},
		"IntersectionOfFixedShapes": {t: &soltype.IntersectionType{Types: []soltype.Type{flat, flat}}, want: false},
		// `μX.{v: Flat, next: X | undefined}` holds only fixed slots.
		"RecursiveKnot": {
			t: &soltype.RecursiveType{Binder: binder, Body: &soltype.ObjectType{Elems: []soltype.ObjTypeElem{
				&soltype.PropertyElem{Name: "v", Type: flat},
				&soltype.PropertyElem{Name: "next", Type: newUnion(nil, []soltype.Type{binder, &soltype.UndefinedType{}})},
			}}},
			want: false,
		},
		"TypeofAFixedShape": {t: &soltype.TypeofType{Ident: "f", Ty: flat}, want: false},
		"Never":             {t: &soltype.NeverType{}, want: false},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.want, c.holdsRemovableSlot(tc.t))
		})
	}
}

// TestHoldsRemovableNonValueSlotOfBuiltTypes covers the element-type condition that
// holdsRemovableNonValueSlot adds to holdsRemovableSlot. Each case builds the type directly
// and asserts what both predicates answer for it.
func TestHoldsRemovableNonValueSlotOfBuiltTypes(t *testing.T) {
	res := InferModuleAgainstStdlib(parseModule(t, ""), benchTree, Options{})
	c := res.checker

	num := &soltype.PrimType{Prim: soltype.NumPrim}
	str := &soltype.PrimType{Prim: soltype.StrPrim}
	flat := &soltype.ObjectType{Elems: []soltype.ObjTypeElem{&soltype.PropertyElem{Name: "x", Type: num}}}
	field := func(name string, t soltype.Type) *soltype.ObjectType {
		return &soltype.ObjectType{Elems: []soltype.ObjTypeElem{&soltype.PropertyElem{Name: name, Type: t}}}
	}

	tests := map[string]struct {
		t           soltype.Type
		want        bool
		wantAnySlot bool
	}{
		"ArrayOfNumbers":       {t: c.ctx.arrayOf(num), want: false, wantAnySlot: true},
		"ArrayOfStrings":       {t: c.ctx.arrayOf(str), want: false, wantAnySlot: true},
		"ArrayOfObjects":       {t: c.ctx.arrayOf(flat), want: true, wantAnySlot: true},
		"ArrayOfArrays":        {t: c.ctx.arrayOf(c.ctx.arrayOf(num)), want: true, wantAnySlot: true},
		"FieldOfArrayOfNumber": {t: field("items", c.ctx.arrayOf(num)), want: false, wantAnySlot: true},
		"FieldOfArrayOfObject": {t: field("items", c.ctx.arrayOf(flat)), want: true, wantAnySlot: true},
		// A type parameter stands for any type, including an object.
		"ArrayOfATypeParameter": {
			t:           c.ctx.arrayOf(&soltype.TypeVarType{ID: 1, Rigid: &soltype.RigidParam{Name: "T"}}),
			want:        true,
			wantAnySlot: true,
		},
		// The second `Array` carries different type arguments from the first, so reaching
		// the class again still checks its elements.
		"FieldsOfBothArrayKinds": {
			t: &soltype.ObjectType{Elems: []soltype.ObjTypeElem{
				&soltype.PropertyElem{Name: "a", Type: c.ctx.arrayOf(num)},
				&soltype.PropertyElem{Name: "b", Type: c.ctx.arrayOf(flat)},
			}},
			want:        true,
			wantAnySlot: true,
		},
		"Unknown":    {t: &soltype.UnknownType{}, want: true, wantAnySlot: true},
		"FixedShape": {t: field("inner", flat), want: false, wantAnySlot: false},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.want, c.holdsRemovableNonValueSlot(tc.t))
			require.Equal(t, tc.wantAnySlot, c.holdsRemovableSlot(tc.t))
		})
	}
}

// TestContentsBorrowedByContainerType covers which container types let a callee remove the
// value a live borrow reaches. Each case fills one template. The template borrows from `c`,
// passes `&mut c` to touchC while the borrow is live, then writes through the borrow.
//
// A case borrows a call result, `firstC(&mut c)`, unless it sets `borrow`. The checker cannot
// see which slot a call result reaches, so it decides from the type of `c`. A case that sets
// `borrow` to a written path such as `&mut c.inner` exercises the per-link check instead.
func TestContentsBorrowedByContainerType(t *testing.T) {
	const tmpl = `
		type Flat = {x: number}
		%s
		declare fn firstC<'a%s>(c: &'a mut %s) -> &'a mut Flat
		declare fn touchC%s(c: &mut %s) -> undefined

		fn g%s(mut c: %s) -> undefined {
			val f = %s
			touchC(&mut c)
			f.x = 1
		}
	`
	rejected := []string{"9:11-9:17: cannot borrow 'c' as mutable for this call while a borrow of its contents is live"}

	tests := map[string]struct {
		// types declares what `ty` names, on one line so every case keeps the same spans.
		types string
		// ty is the type of `c`.
		ty string
		// tparams is the type parameter list of each function, without its angle brackets.
		tparams string
		// borrow is the expression f binds. It defaults to `firstC(&mut c)`.
		borrow string
		want   []string
	}{
		// A declared class's internals are unknown, so its methods may remove anything.
		"DeclaredClass": {
			types: `declare class D { inner: Flat }`,
			ty:    "D",
			want:  rejected,
		},
		"DeclaredClassField": {
			types:  `declare class D { inner: Flat }`,
			ty:     "D",
			borrow: "&mut c.inner",
			want:   rejected,
		},
		// An enum's variants are declared by the enum, not as classes with checked bodies.
		"EnumVariant": {
			types: `enum E { A(Flat), B(Flat) } type EB = {e: E}`,
			ty:    "EB",
			want:  rejected,
		},
		// Sub declares only fields, but it inherits Base's unknown internals.
		"ClassExtendingADeclaredClass": {
			types: `declare class Base { n: number, constructor(&mut self) } class Sub extends Base { constructor(&mut self) { super() } }`,
			ty:    "Sub",
			want:  rejected,
		},
		// take can hand inner to a new owner.
		"ObjectWithAMutSelfMethod": {
			types: `type M = {inner: Flat, take(&mut self) -> Flat}`,
			ty:    "M",
			want:  rejected,
		},
		"FieldOfAnObjectWithAMutSelfMethod": {
			types:  `type M = {inner: Flat, take(&mut self) -> Flat}`,
			ty:     "M",
			borrow: "&mut c.inner",
			want:   rejected,
		},
		"ObjectWithASetter": {
			types: `type S = {inner: Flat, set v(&mut self, x: number)}`,
			ty:    "S",
			want:  rejected,
		},
		// A callee can delete an entry of an index signature.
		"IndexSignature": {
			types: `type I = {[k: string]: Flat}`,
			ty:    "I",
			want:  rejected,
		},
		// An inexact tuple may hold elements its type does not list.
		"InexactTuple": {
			ty:   "[Flat, ...]",
			want: rejected,
		},
		// A rest element is an array, so its elements can be removed.
		"TupleWithARestElement": {
			ty:   "[Flat, ...Array<Flat>]",
			want: rejected,
		},
		"SlotOfATupleRestElement": {
			ty:     "[Flat, ...Array<Flat>]",
			borrow: "&mut c[1]",
			want:   rejected,
		},
		// The slot before the rest element is fixed.
		"SlotBeforeATupleRestElementOk": {
			ty:     "[Flat, ...Array<Flat>]",
			borrow: "&mut c[0]",
			want:   nil,
		},
		// The walk cannot see what a type parameter holds.
		"TypeParameter": {
			types:   `type W<T> = {v: T, inner: Flat}`,
			ty:      "W<T>",
			tparams: "T",
			want:    rejected,
		},
		// A field read off a type parameter is not a slot the walk can see.
		"FieldOfATypeParameter": {
			ty:      "{t: T}",
			tparams: "T: {inner: Flat}",
			borrow:  "&mut c.t.inner",
			want:    rejected,
		},
		// The walk stops once it finds items, and skips inner.
		"ArrayFieldBeforeAFixedField": {
			types: `type B = {items: Array<Flat>, inner: Flat}`,
			ty:    "B",
			want:  rejected,
		},
		// A mutable borrow inside the container lets a callee pop through it.
		"MutableBorrowOfAnArray": {
			types: `type H = {r: &mut Array<Flat>}`,
			ty:    "H",
			want:  rejected,
		},
		// Each member of the union is a fixed shape.
		"UnionOfFixedShapesOk": {
			types: `type U = {u: {inner: Flat} | {inner: Flat, n: number}}`,
			ty:    "U",
			want:  nil,
		},
		"FieldOfAUnionOfFixedShapesOk": {
			types:  `type U = {u: {inner: Flat} | {inner: Flat, n: number}}`,
			ty:     "U",
			borrow: "&mut c.u.inner",
			want:   nil,
		},
		// The walk follows a mutable borrow and finds only a Flat.
		"MutableBorrowOfAFixedShapeOk": {
			types: `type H = {r: &mut Flat}`,
			ty:    "H",
			want:  nil,
		},
		// Nothing can be removed through an immutable borrow, so the walk stops there.
		"ImmutableBorrowOfAnArrayOk": {
			types: `type H = {r: &Array<Flat>}`,
			ty:    "H",
			want:  nil,
		},
		// An object type annotation records no receiver on its getters and methods, so the
		// walk cannot tell `&self` from `&mut self`, and a getter may take `&mut self`.
		"GetterWrittenWithAnImmutableReceiver": {
			types: `type G = {inner: Flat, get v(&self) -> number}`,
			ty:    "G",
			want:  rejected,
		},
		"FieldOfAnObjectWithAGetter": {
			types:  `type G = {inner: Flat, get v(&self) -> number}`,
			ty:     "G",
			borrow: "&mut c.inner",
			want:   rejected,
		},
		"InterfaceMethodWrittenWithAnImmutableReceiver": {
			types: `interface P { inner: Flat, peek(&self) -> number }`,
			ty:    "P",
			want:  rejected,
		},
		"ObjectTypeMethodWrittenWithAnImmutableReceiver": {
			types: `type P = {inner: Flat, peek(&self) -> number}`,
			ty:    "P",
			want:  rejected,
		},
		// A call signature and a `new` signature act on no instance.
		"CallAndNewSignaturesOk": {
			types: `type K = {inner: Flat, (n: number) -> string, new (n: number) -> string}`,
			ty:    "K",
			want:  nil,
		},
		"FieldOfAnObjectWithCallSignaturesOk": {
			types:  `type K = {inner: Flat, (n: number) -> string, new (n: number) -> string}`,
			ty:     "K",
			borrow: "&mut c.inner",
			want:   nil,
		},
		// Every class in Sub's chain is declared in Escalier, so its slots are its fields.
		"ClassExtendingAnEscalierClassOk": {
			types: `class A { inner: Flat, constructor(&mut self) { self.inner = {x: 0} } } class Sub extends A { constructor(&mut self) { super() } }`,
			ty:    "Sub",
			want:  nil,
		},
		// A function field is not a slot a callee can remove from.
		"FunctionFieldOk": {
			types: `type F = {inner: Flat, cb: fn () -> number}`,
			ty:    "F",
			want:  nil,
		},
		// The walk reaches Node again through next and does not expand it twice.
		"RecursiveClassOk": {
			types: `class Node { v: Flat, next: Node | undefined }`,
			ty:    "Node",
			want:  nil,
		},
		"RecursiveAliasOk": {
			types: `type L = {v: Flat, next: L | undefined}`,
			ty:    "L",
			want:  nil,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			borrow := tc.borrow
			if borrow == "" {
				borrow = "firstC(&mut c)"
			}
			firstParams, otherParams := "", ""
			if tc.tparams != "" {
				firstParams, otherParams = ", "+tc.tparams, "<"+tc.tparams+">"
			}
			src := fmt.Sprintf(tmpl, tc.types, firstParams, tc.ty, otherParams, tc.ty, otherParams, tc.ty, borrow)
			_, errs := inferAgainstTreeWithSpans(t, src)
			require.Equal(t, tc.want, errs)
		})
	}
}
