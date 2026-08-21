package soltype

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/escalier-lang/escalier/internal/set"
)

// Precedence levels for type operators, matching the Escalier parser (and
// type_system/print_type.go). Higher values bind more tightly. precPrefix covers the
// prefix forms — the `mut`/lifetime borrow prefix (RefType) and the `keyof` operator
// (KeyofType); the `...T` spread prefix lands with tuple/object spread types.
const (
	precFunc         = 2 // fn (...) -> T — return type is greedy, needs parens in union/intersection
	precUnion        = 3 // A | B
	precIntersection = 4 // A & B
	precPrefix       = 5 // mut T, 'a T, keyof T — a prefix binds looser than an atom
	precAtom         = 6 // primary types, never need parens
)

// typePrec returns the printing precedence of a coalesced M1 type.
func typePrec(t Type) int {
	switch t := t.(type) {
	case *FuncType:
		return precFunc
	case *UnionType:
		return precUnion
	case *IntersectionType:
		return precIntersection
	case *NegationType:
		// `¬T` leads with a prefix operator, so it binds like the `mut` borrow and `keyof`
		// forms. It is tighter than `|` and `&` and looser than an atom.
		return precPrefix
	case *RefType:
		return precPrefix
	case *KeyofType:
		return precPrefix
	case *IndexType:
		// `T[K]` is a postfix primary that binds tighter than any prefix, so it never needs
		// outer parens. `keyof T[K]` reads as `keyof (T[K])`, matching TypeScript.
		return precAtom
	case *TypeofType:
		return precPrefix
	case *CondType:
		// `if C : E { T } else { E }` is self-delimiting — the leading `if` and trailing `}` bound
		// it — so it binds like an atom and never needs outer parens, matching type_system's
		// CondType rendering.
		return precAtom
	case *InferType:
		// The `infer U` binder leads with a keyword, so it binds like the other prefixes. A
		// reference to that name renders as the bare `U`, an atom, and so does the anonymous
		// binder `_`, which leads with no keyword.
		if t.Binder && !t.IsWildcardInfer() {
			return precPrefix
		}
		return precAtom
	case *MappedKeyType:
		// A mapped type's key variable renders as its bare name, an atom.
		return precAtom
	case *RecursiveType:
		// A μ-knot is self-delimiting, because printType parenthesizes any body that is not itself
		// an atom, so it never needs outer parens. `μX0.{next: X0} | number` and
		// `μX0.(number | X0) | number` both read correctly with no wrapper.
		return precAtom
	case *RecursiveVarType:
		// A μ-knot's bound variable renders as its bare name, an atom.
		return precAtom
	case *RestSpreadType:
		// A `...P` spread element only appears inside a tuple's bracket list, which prints each
		// element unparenthesized, so its precedence is consulted only if it is rendered on its
		// own. The `...` prefix binds like the other prefixes.
		return precPrefix
	case *TemplateLitType:
		// A template literal is backtick-delimited, so like an object or a `Name<args>` reference
		// it is an atom that never needs outer parens.
		return precAtom
	case *StringIntrinsicType, *ExactnessType:
		// `Uppercase<T>` and `Exact<T>` render as a name with an argument list, the same atom shape
		// a class or alias reference takes, so neither needs outer parens.
		return precAtom
	default:
		// PrimType, LitType, TupleType, ObjectType, ClassType, AliasType, NullType,
		// UndefinedType, NeverType, UnknownType — atoms. ObjectType is brace-delimited, and ClassType,
		// ArrayType, and AliasType each render as a bare name or `Name<args>`, so none needs parens. A raw TypeVarType
		// appears only when printing an un-coalesced type, see printType; it is also an
		// atom rendered as `t{ID}`, so it lands here. A `mut 'a Point` borrow wraps the
		// ClassType in a RefType, which carries the looser precPrefix precedence.
		return precAtom
	}
}

// Print renders a coalesced Type as an Escalier type-annotation string.
//
// This is Delta #2 of m1-implementation-plan §2.2: a native soltype printer that
// shares NO code with type_system.PrintType but deliberately mirrors its surface
// forms so the two checkers' rendered types stay string-comparable in M7's
// differential harness. It renders the M1 coalesced type set only
// (PrimType/LitType/FuncType/TupleType/UndefinedType/NeverType/UnknownType/UnionType/
// IntersectionType). Print itself emits no <T0, ...> quantifier prefix — a
// monotype has no parameters to name; PrintAsScheme renders the generalized form.
//
// Print is distinct from solver's describe(): describe renders a RAW,
// uncoalesced type (t0, function, number) mid-constrain for error messages,
// whereas Print renders a COALESCED type as user-facing syntax. They look
// similar but operate at different stages and must not be merged (§2.2).
//
// Print's normal input is a coalesced type, but it also tolerates a raw,
// un-coalesced TypeVarType (rendering it as `t{ID}`) rather than panicking: the
// M2 walk records var-carrying types in its Info side table and coalesces only
// at binding boundaries, so a consumer may legitimately print an inner node's
// still-raw type (M2 plan §7).
func Print(t Type) string {
	return (&namedPrinter{}).printType(t)
}

// PrintQualified renders a type like Print but under the full dep_graph-qualified name of
// every ClassType and AliasType, so two nominal types sharing a local name in different
// namespaces render distinctly. Print strips the namespace for display, which suits
// user-facing output but collapses `A.Point` and `B.Point` to one string. The solver forms
// a collision-free canonical identity key from PrintQualified. It is not a surface form.
func PrintQualified(t Type) string {
	return (&namedPrinter{qualify: true}).printType(t)
}

// ElisionMark stands for the subtree PrintElided dropped. It is U+2026, distinct from the `...`
// that marks an inexact object or tuple, so a reader can tell an elided branch from an open one.
const ElisionMark = "…"

// PrintElided renders a type like Print but replaces every subtree nested deeper than maxDepth
// with ElisionMark, so `Grow<{a: {a: {a: number}}}>` at maxDepth 3 reads `Grow<{a: {a: …}}>`. The
// root sits at depth 0, so maxDepth 1 renders only the root's immediate children.
//
// A type reduction can build a type far larger than any the source wrote. Grounding one alias whose
// body spreads another twice, repeated over a few dozen such aliases, reduces to a type exponential
// in that count before the evaluator's budget stops it — see maxExpandKeyChars in internal/solver.
// Print renders that in full, which is right for an identity key but useless in a diagnostic, where
// it buries the one line a reader needs under tens of thousands of characters.
//
// maxDepth <= 0 elides nothing, so the zero value renders exactly as Print does.
func PrintElided(t Type, maxDepth int) string {
	return (&namedPrinter{maxDepth: maxDepth}).printType(t)
}

// PrintAsScheme renders a coalesced GENERALIZED type (M3): it collects the type's
// free variables into a <T0, T1, …> quantifier prefix and renders each as its
// assigned name. A type with no free variables renders exactly as Print would (no
// prefix), so PrintAsScheme is safe on a monotype. The prefix attaches to a
// function (`fn <T0>(…) -> …`, matching Escalier's generic-function surface
// syntax); a non-function body carrying free variables — not produced by M3's
// generalization, which only generalizes function values — falls back to a
// leading <…> group.
//
// Variables are named by first appearance in print order (params left to right,
// then return; tuple elements; record fields), so the same coalesced variable
// renders under one name everywhere it occurs.
//
// PrintAsScheme treats EVERY free variable as a quantified parameter — for a caller
// that trusts its input is a fully-generalized type. The solver's renderScheme
// uses PrintAsSchemeWith to restrict naming to the variables generalization
// actually quantified, so a stray variable is not disguised as a parameter.
//
// PrintAsScheme passes no lifetime bounds, so a quantified lifetime renders as a bare
// name with no outlives bound. A join lifetime then shows as `<'a, 'b, 'c>` with no
// `'a: 'c` linkage. To render those bounds a caller must supply them through
// PrintAsSchemeWith, which the solver's renderScheme does. Use renderScheme, not
// PrintAsScheme, to display a solver scheme that may carry borrow lifetimes.
func PrintAsScheme(t Type) string {
	return PrintAsSchemeWith(t, func(*TypeVarType) bool { return true }, nil, nil)
}

// PrintWithParams renders a type like Print, naming each variable in declared under the
// source name its declaration wrote.
//
// A class, alias, or enum keeps its type parameters in the checker's registry rather than
// in the type it binds, so the type carries the parameter's variable and nothing else.
// `class Node<T> {value: T}` binds the type Node<t0>, which Print renders with the raw
// `t{ID}` debug form. Passing the class's parameters here renders it Node<T>. A variable no
// entry of `declared` points at keeps the `t{ID}` form, the fallback Print applies to every
// variable.
func PrintWithParams(t Type, declared []*TypeParam) string {
	return PrintWithDeclaredParams(t, declared, nil)
}

// PrintWithDeclaredParams renders a type under the source names of a declaration's own
// type AND lifetime parameters. It is PrintWithParams extended to the lifetime sort, for a
// declaration that quantifies both.
//
// A borrow whose lifetime carries no name prints as a bare `&`, since an inferred borrow
// has no name worth showing. That rule also hides a lifetime the source did write. The
// body of `type Result<'a> = ¬(&'a Point)` holds the variable `'a` binds, and plain Print
// knows no name for it, so it renders `¬&Point`. A reader cannot tell from that which
// borrow is excluded. Passing the alias's LifetimeParams here renders `¬&'a Point`.
//
// A variable that no entry names keeps its fallback form. That is `t{ID}` for a type
// variable and a bare `&` for a borrow lifetime.
func PrintWithDeclaredParams(t Type, declared []*TypeParam, declaredLts []*LifetimeParam) string {
	p := &namedPrinter{}
	p.bindTypeParams(declared)
	p.nameLifetimeParams(declaredLts)
	return p.printType(t)
}

// PrintAsSchemeWith renders a generalized type, naming ONLY the free variables
// isParam accepts as quantified type parameters; any other free variable renders
// as the raw `t{ID}` debug form instead of being masked as a parameter. This
// preserves the leak anchor: a variable coalescing failed to inline (a captured
// var that escaped, a stray inference var) shows as `t{ID}` rather than a spurious
// `<Tn>` that would make a malformed signature look valid.
//
// ltBounds carries the transitively-reduced outlives relation among the type's named
// lifetime variables: ltBounds[lv] is the lifetimes lv outlives. A join lifetime
// bounded below by two borrows renders as `<'a: 'c, 'b: 'c, 'c>`, where 'a and 'b
// each carry the bound {'c}. A nil map draws no bounds, so a caller that does not
// solve lifetime bounds renders bare names.
//
// declared carries the source type parameters of the declaration t stands for, in
// declaration order. A class, alias, or enum keeps them in the checker's registry rather
// than in the type, so the caller must hand them over. Each one whose variable isParam
// accepts renders under its source name and leads the quantifier prefix in declaration
// order, ahead of the generated names. `class Pair<K, V>` whose constructor takes v before
// k therefore renders `<K, V> {new (v: V, k: K) -> Pair<K, V>}` rather than a binder list
// permuted by first appearance. Pass nil when there is no such declaration. That is every
// function, since a FuncType carries its own parameters and names them itself.
func PrintAsSchemeWith(
	t Type,
	isParam func(*TypeVarType) bool,
	ltBounds map[*LifetimeVar][]*LifetimeVar,
	declared []*TypeParam,
) string {
	p := &namedPrinter{}
	// A function's own type parameters claim their source names before anything else, so a
	// generated T0 skips a name the source itself wrote. `fn <T0>` beside a free scheme
	// variable renders `fn <T1, T0>`, with each binder naming one variable.
	if ft, ok := t.(*FuncType); ok {
		p.bindTypeParams(ft.TypeParams)
	}
	// The free variables the caller is willing to quantify, in first-appearance print order.
	// Anything isParam rejects is left unnamed and renders as t{ID}.
	var params []*TypeVarType
	for _, v := range freeTypeVars(t) {
		if isParam(v) {
			params = append(params, v)
		}
	}
	quantified := set.FromSlice(params)
	var labels []string
	for _, tp := range declared {
		if tp.Name == "" || !quantified.Contains(tp.Var) {
			continue
		}
		if _, bound := p.names[tp.Var]; bound {
			continue // one variable gets one binder, however many parameters point at it
		}
		labels = append(labels, p.bindTypeParam(tp.Var, tp.Name))
	}
	// Every remaining quantified variable takes a generated T0, T1, … in first-appearance
	// order, skipping any name a source parameter already claims.
	next := 0
	for _, v := range params {
		if _, bound := p.names[v]; bound {
			continue
		}
		name := typeParamName(next)
		for p.nameTaken(name) {
			next++
			name = typeParamName(next)
		}
		next++
		labels = append(labels, p.bindTypeParam(v, name))
	}
	// Borrow lifetimes left in the coalesced type by coalesceLifetimes are all
	// nameable. A connect-nothing one was already elided unless a complement encloses
	// it. Three kinds survive to here: a param lifetime, a kept join lifetime, and a
	// lifetime occurring under a complement.
	//
	// The third kind is named even when it reaches no parameter, so
	// `declare fn f<'a>() -> ¬(&'a mut {x: number})` renders
	// `fn <'a>() -> ¬&'a mut {x: number}`, while the same signature over a plain borrow
	// renders `fn () -> &mut {x: number}`. resolveLt carries the reason.
	//
	// Name each 'a, 'b, … in first-appearance order and add it to the quantifier prefix
	// after the type parameters.
	ltVars := freeLifetimeVars(t)
	ltNames := map[*LifetimeVar]string{}
	ltIndex := map[*LifetimeVar]int{}
	// A function's own lifetime parameters keep their source names in the prefix, and a
	// free lifetime gets a generated name from the same 'a, 'b, … alphabet, so a generated
	// name must skip any source name a parameter already claims. Without this a captured
	// 'a and a declared 'a would both render as 'a. The type-parameter loop above skips a
	// claimed name the same way.
	reserved := ownLifetimeParamNames(t)
	nextLt := 0
	for i, lv := range ltVars {
		name := lifetimeParamName(nextLt)
		for reserved.Contains(name) {
			nextLt++
			name = lifetimeParamName(nextLt)
		}
		nextLt++
		ltNames[lv] = name
		ltIndex[lv] = i
	}
	if len(labels) == 0 && len(ltVars) == 0 {
		// No quantified parameters: render as a plain (possibly raw-var) type, which
		// keeps a leaked variable visible as t{ID}.
		return Print(t)
	}
	p.ltNames = ltNames
	switch t.(type) {
	case *ClassType, *AliasType:
		// A class instance or alias reference already displays its parameters inline in its
		// `<...>` argument list, so it needs no separate quantifier prefix. A generalized
		// Map<K, V> renders as Map<T0, T1>, not <T0, T1> Map<T0, T1>, and Foo<'a> as Foo<'a>,
		// not <'a> Foo<'a>. Their only free-variable children are their arguments, so every
		// quantified variable is shown inline and none is lost by dropping the prefix.
		return p.printType(t)
	}
	ltLabels := make([]string, len(ltVars))
	for i, lv := range ltVars {
		ltLabels[i] = p.lifetimeBinder(lv, ltBounds[lv], ltIndex)
	}
	if ft, ok := t.(*FuncType); ok {
		// Merge the scheme's free variables, the function's OWN type and lifetime
		// parameters, and the free scheme lifetimes into one ordered prefix, so a generic
		// method that also captures a scheme variable renders `fn <T0, U, 'a>(...)` rather
		// than adjacent groups. A function's own lifetime params are excluded from ltLabels
		// by freeLifetimeVars, so they are named and rendered here from their declared
		// bounds. printFuncBody omits the prefix so the own parameters are not repeated. The
		// own TYPE parameters were bound at the top of this function, so typeParamBinders
		// reads their names back rather than binding them a second time.
		p.nameLifetimeParams(ft.LifetimeParams)
		binders := append([]string{}, labels...)
		binders = append(binders, p.typeParamBinders(ft.TypeParams)...)
		binders = append(binders, p.lifetimeParamBinders(ft.LifetimeParams)...)
		binders = append(binders, ltLabels...)
		return "fn <" + strings.Join(binders, ", ") + ">" + p.printFuncBody(ft)
	}
	prefix := "<" + strings.Join(append(labels, ltLabels...), ", ") + ">"
	// The prefix binds the WHOLE body, and a body joined by `|` or `&` needs parens to
	// say so. `<'a, 'b> &'a T | &'b T` reads as though the prefix covered the first
	// member alone, which would leave 'b bound by nothing. A body that is one atom or
	// one prefix form cannot be split by a following operator, so it needs none: the
	// class-constructor rendering stays `<T> {new (value: T) -> Node<T>}`.
	return prefix + " " + p.printTypeMinPrec(t, precPrefix)
}

// lifetimeBinder renders one lifetime binder in the quantifier prefix: the bare name
// `'a`, or `'a: 'b & 'c` when lv outlives 'b and 'c. The bound lifetimes are ordered
// by first appearance via ltIndex, the same order the prefix names them, so the output
// is stable. A bound lifetime absent from ltIndex is skipped rather than rendered as an
// out-of-band name. The solver never produces such a bound, so this is only a guard.
func (p *namedPrinter) lifetimeBinder(lv *LifetimeVar, bounds []*LifetimeVar, ltIndex map[*LifetimeVar]int) string {
	name := p.ltNames[lv]
	if len(bounds) == 0 {
		return name
	}
	targets := make([]*LifetimeVar, 0, len(bounds))
	for _, b := range bounds {
		if _, ok := ltIndex[b]; ok {
			targets = append(targets, b)
		}
	}
	if len(targets) == 0 {
		return name
	}
	sort.Slice(targets, func(i, j int) bool { return ltIndex[targets[i]] < ltIndex[targets[j]] })
	parts := make([]string, len(targets))
	for i, b := range targets {
		parts[i] = p.ltNames[b]
	}
	return name + ": " + strings.Join(parts, " & ")
}

// typeParamName is the surface name for the i-th quantified type parameter: T0,
// T1, …, matching the planned `fn <T0>(x: T0) -> T0` rendering.
func typeParamName(i int) string {
	return "T" + strconv.Itoa(i)
}

// ownLifetimeParamNames returns the source names a function's own lifetime parameters
// claim in the quantifier prefix, so the free-lifetime naming can avoid colliding with
// them. It is non-empty only for a FuncType that declares lifetime parameters; every
// other type reserves nothing.
func ownLifetimeParamNames(t Type) set.Set[string] {
	names := set.NewSet[string]()
	if ft, ok := t.(*FuncType); ok {
		for _, lp := range ft.LifetimeParams {
			if lp.Name != "" {
				names.Add(lp.Name)
			}
		}
	}
	return names
}

// lifetimeParamName is the surface name for the i-th quantified lifetime parameter:
// 'a, 'b, …, 'z, 'aa, 'ab, … in Excel-style base-26, so a borrow renders as
// `fn <'a>(p: &'a mut {x}) -> &'a mut {x}`.
func lifetimeParamName(i int) string {
	var b []byte
	for {
		b = append([]byte{byte('a' + i%26)}, b...)
		i = i/26 - 1
		if i < 0 {
			break
		}
	}
	return "'" + string(b)
}

// freeLifetimeVars collects the LifetimeVars appearing in t in first-appearance
// print order, the lifetime-sort twin of freeTypeVars. It rides the shared Accept
// visitor rather than a hand-rolled walk, the same way simplify.go's varCollector
// collects type vars. Lifetimes are not Types, so Accept never visits a Lt slot
// itself. The collector reads it in EnterType when it reaches a RefType, before
// Accept descends into the borrow's inner. That preserves print order, because a
// borrow's own lifetime precedes any lifetime nested in its inner.
func freeLifetimeVars(t Type) []*LifetimeVar {
	c := &ltVarCollector{seen: set.NewSet[*LifetimeVar]()}
	t.Accept(c, Positive)
	return c.out
}

// ltVarCollector gathers LifetimeVars in Accept-traversal order. It rewrites nothing.
// EnterType returns the default descend result and ExitType returns the node
// unchanged, so Accept performs no allocation and the walk is a pure collection.
type ltVarCollector struct {
	out  []*LifetimeVar
	seen set.Set[*LifetimeVar]
}

func (c *ltVarCollector) EnterType(t Type, _ Polarity) EnterResult {
	switch t := t.(type) {
	case *RefType:
		c.add(t.Lt)
	case *ClassType:
		// A nominal instance's lifetime arguments and its own borrow lifetime are free
		// lifetimes reached here, so `Ref<'x, T>` collects 'x. They precede any lifetime
		// nested inside the type arguments, matching print order.
		for _, la := range t.LifetimeArgs {
			c.add(la)
		}
		c.add(t.Lt)
	case *AliasType:
		// An alias reference's lifetime arguments are free lifetimes reached here, so
		// `Foo<'a>` collects 'a. They precede any lifetime nested inside the type arguments,
		// matching print order. An alias has no own borrow lifetime. A borrow rides a RefType.
		for _, la := range t.LifetimeArgs {
			c.add(la)
		}
	case *FuncType:
		// A function's own lifetime parameters are bound, not free, so mark their
		// variables seen up front to exclude every use in the receiver, params, and
		// return. An outlives bound may reference an outer free lifetime, collected
		// after the binders are marked so the bound's own parameter lifetimes stay
		// excluded, mirroring freeTypeVars' treatment of a type parameter's constraint.
		// c.add skips lifetime if it's already in c.seen.
		for _, lp := range t.LifetimeParams {
			c.seen.Add(lp.Var)
		}
		for _, lp := range t.LifetimeParams {
			for _, b := range lp.Bounds {
				c.add(b)
			}
		}
	}
	return EnterResult{}
}

func (c *ltVarCollector) ExitType(t Type, _ Polarity) Type { return t }

// add records a lifetime when it is a LifetimeVar, deduped by identity. 'static and an
// anonymous display lifetime carry no variable and are skipped. A variable already
// marked seen, such as a function's own bound lifetime parameter, is skipped too.
func (c *ltVarCollector) add(lt Lifetime) {
	if lv, ok := lt.(*LifetimeVar); ok && !c.seen.Contains(lv) {
		c.seen.Add(lv)
		c.out = append(c.out, lv)
	}
}

// freeTypeVars collects the TypeVarTypes appearing in t in first-appearance print
// order. It does NOT descend into a variable's bound lists — a coalesced display
// type already carries the relevant structure inline (a retained variable's
// bounds are sibling union/intersection members), so the variable node is a leaf
// here.
func freeTypeVars(t Type) []*TypeVarType {
	var out []*TypeVarType
	seen := set.NewSet[*TypeVarType]()
	var walk func(Type)
	walk = func(t Type) {
		switch t := t.(type) {
		case *TypeVarType:
			if !seen.Contains(t) {
				seen.Add(t)
				out = append(out, t)
			}
		case *FuncType:
			// A function's own type parameters are bound, not free, so mark their
			// variables seen up front to exclude every use in the params, return,
			// constraints, and defaults. Their constraints and defaults may still
			// reference outer free variables, so walk those once the bound variables are
			// seen.
			for _, tp := range t.TypeParams {
				seen.Add(tp.Var)
			}
			for _, tp := range t.TypeParams {
				for _, b := range tp.Var.UpperBounds {
					walk(b)
				}
				if tp.Default != nil {
					walk(tp.Default)
				}
			}
			if t.SelfParam != nil {
				walk(t.SelfParam.Type)
			}
			for _, p := range t.Params {
				walk(p.Type)
			}
			walk(t.Ret)
			if t.Throws != nil {
				walk(t.Throws)
			}
		case *TupleType:
			for _, e := range t.Elems {
				walk(e)
			}
		case *ObjectType:
			for _, e := range t.Elems {
				switch e := e.(type) {
				case *PropertyElem:
					walk(e.Type)
				case *MethodElem:
					for _, sig := range e.Signatures {
						walk(sig)
					}
				case *GetterElem:
					if e.SelfParam != nil {
						walk(e.SelfParam.Type)
					}
					walk(e.Type)
					if e.Throws != nil {
						walk(e.Throws)
					}
				case *SetterElem:
					if e.SelfParam != nil {
						walk(e.SelfParam.Type)
					}
					walk(e.Param)
					if e.Throws != nil {
						walk(e.Throws)
					}
				case *ConstructorElem:
					walk(e.Fn)
				case *SpreadElem:
					walk(e.Type)
				case *MappedElem:
					walk(e.Keys)
					walk(e.Value)
					for _, operand := range MappedOptionalOperands(e) {
						walk(operand)
					}
				}
			}
		case *ClassType:
			for _, a := range t.TypeArgs {
				walk(a)
			}
		case *AliasType:
			for _, a := range t.TypeArgs {
				walk(a)
			}
		case *KeyofType:
			walk(t.Operand)
		case *IndexType:
			walk(t.Target)
			walk(t.Index)
		case *TypeofType:
			walk(t.Ty)
		case *CondType:
			walk(t.Check)
			walk(t.Extends)
			walk(t.Then)
			walk(t.Else)
		case *RestSpreadType:
			walk(t.Operand)
		case *TemplateLitType:
			for _, interp := range t.Interps {
				walk(interp)
			}
		case *StringIntrinsicType:
			walk(t.Operand)
		case *ExactnessType:
			walk(t.Operand)
		case *RecursiveType:
			walk(t.Body)
		case *PromiseType:
			walk(t.Inner)
			if t.Err != nil {
				walk(t.Err)
			}
		case *GeneratorType:
			walk(t.Yield)
			walk(t.Ret)
			walk(t.Next)
			if t.Throws != nil {
				walk(t.Throws)
			}
		case *ArrayType:
			walk(t.Elem)
		case *RefType:
			walk(t.Inner)
		case *UnionType:
			for _, m := range t.Types {
				walk(m)
			}
		case *IntersectionType:
			for _, m := range t.Types {
				walk(m)
			}
		case *NegationType:
			walk(t.Inner)
		}
	}
	walk(t)
	return out
}

// namedPrinter carries the optional retained-variable → quantifier-name map for a
// single render. names is nil for plain Print (a raw variable then renders as
// `t{ID}`) and populated by PrintAsScheme (a retained variable renders as `T{i}`).
type namedPrinter struct {
	names map[*TypeVarType]string
	// ltNames maps a retained lifetime variable to its surface name (`'a`, `'b`,
	// …). It is nil for plain Print, where a lifetime var renders as the raw
	// `'l{ID}` debug form. Display-time lifetime coalescing populates it so a
	// param-originated lifetime renders under its quantified name.
	ltNames map[*LifetimeVar]string
	// qualify renders a ClassType or AliasType under its full dep_graph-qualified name
	// instead of the namespace-stripped display name, so two nominal types sharing a local
	// name across namespaces stay distinct. PrintQualified sets it for identity-key use;
	// plain Print leaves it false so user-facing output stays unqualified.
	qualify bool
	// maxDepth bounds how deep printType descends before it renders ElisionMark in place of a
	// subtree. Zero, the value Print and PrintQualified leave, descends without limit. depth is
	// the nesting of the node being rendered, counted from 0 at the root.
	maxDepth int
	depth    int
	// claimed holds every surface name a type parameter in scope at this point renders
	// under, across both alphabets — a source name such as `K` and a generated `T0`. A new
	// binder consults it so two parameters in scope at once never share one name. A
	// signature releases its own parameters' names on the way out, so two sibling signatures
	// each written `<T>` both render `T`.
	claimed set.Set[string]
}

// nameTaken reports whether a type parameter in scope at this point already renders under
// name. Reading a nil set is safe, so plain Print needs no initialization.
func (p *namedPrinter) nameTaken(name string) bool {
	return p.claimed.Contains(name)
}

// bindTypeParam registers v under a surface name derived from base and returns that name.
// base is used as written when no parameter in scope holds it. When one does, a numeric
// suffix disambiguates. A method written `<T>` inside a class written `<T>` renders its own
// parameter as `T_2`, so the two read as different types everywhere in the method, which is
// the whole region where both are in scope.
func (p *namedPrinter) bindTypeParam(v *TypeVarType, base string) string {
	if p.names == nil {
		p.names = map[*TypeVarType]string{}
	}
	if p.claimed == nil {
		p.claimed = set.NewSet[string]()
	}
	name := base
	for n := 2; p.nameTaken(name); n++ {
		name = base + "_" + strconv.Itoa(n)
	}
	p.claimed.Add(name)
	p.names[v] = name
	return name
}

// bindTypeParams registers each type parameter's binding variable under its source name, so
// a use of the parameter renders as that name rather than the raw t{ID} debug form. A
// parameter with no source name is left unregistered and falls back to t{ID}.
//
// It returns the surface names it bound. A caller whose parameters go out of scope partway
// through the render hands them to releaseNames at that point; one whose parameters stay in
// scope for the whole render discards them.
func (p *namedPrinter) bindTypeParams(tps []*TypeParam) []string {
	if len(tps) == 0 {
		return nil
	}
	bound := make([]string, 0, len(tps))
	for _, tp := range tps {
		if tp.Name != "" {
			bound = append(bound, p.bindTypeParam(tp.Var, tp.Name))
		}
	}
	return bound
}

// releaseNames frees each name for a later binder to reuse. A signature's parameters are in
// scope only inside it, so releasing them on the way out is what lets two overload arms each
// written `<T>` both render `T`. A `<T>` nested inside an enclosing `<T>` is still renamed,
// because the enclosing binder holds its name across the whole nested signature.
//
// The variable-to-name bindings themselves are left in place, so a variable that escaped its
// binder still renders under the name its declaration gave it.
func (p *namedPrinter) releaseNames(names []string) {
	for _, name := range names {
		p.claimed.Remove(name)
	}
}

// printLifetime renders a lifetime in Escalier surface syntax: 'static for the
// bottom of the lattice, a retained variable's assigned name (`'a`) when ltNames
// carries one, else the raw `'l{ID}` debug form — the lifetime-sort twin of
// printType's TypeVarType arm, which falls back to `t{ID}` for an un-named var.
func (p *namedPrinter) printLifetime(lt Lifetime) string {
	switch lt := lt.(type) {
	case *StaticLifetime:
		return "'static"
	case *AnonLifetime:
		return ""
	case *LifetimeVar:
		if p.ltNames != nil {
			if name, ok := p.ltNames[lt]; ok {
				return name
			}
		}
		return "'l" + strconv.Itoa(lt.ID)
	}
	panic(fmt.Sprintf("printLifetime: unhandled %T", lt))
}

// borrowLifetimeName returns the lifetime to print after a borrow's leading `&`, or
// "" when the lifetime is inferred and carries no load-bearing name. A LifetimeVar
// renders its assigned quantifier name `'a` when ltNames carries one. An un-named var
// is an inferred borrow and prints as a bare `&` with no lifetime, matching the display
// rule that names a lifetime only when it is load-bearing. 'static is always shown. The
// `&` itself is emitted by the caller whenever Lt is set, so a borrow is always
// distinguishable from an owned value.
func (p *namedPrinter) borrowLifetimeName(lt Lifetime) string {
	switch lt := lt.(type) {
	case *LifetimeVar:
		if p.ltNames != nil {
			if name, ok := p.ltNames[lt]; ok {
				return name
			}
		}
		return ""
	case *StaticLifetime:
		return "'static"
	}
	return ""
}

// printTypeMinPrec prints a child type, wrapping it in parentheses when its
// precedence is below the required minimum — mirrors type_system's helper of the
// same shape, so e.g. a function inside a union renders as
// `(fn () -> number) | string`.
func (p *namedPrinter) printTypeMinPrec(t Type, minPrec int) string {
	result := p.printType(t)
	if typePrec(t) < minPrec {
		return "(" + result + ")"
	}
	return result
}

// isPrintLeaf reports whether printType renders t without descending into another type. A leaf
// costs nothing to render in full, so PrintElided keeps it at the depth boundary rather than
// replacing a bare `number` with an ellipsis that tells the reader less than the type itself would.
//
// An InferType renders as a name in both forms, `infer U` at the binder and a bare `U` at a
// reference, and a TypeofType renders as the identifier it names rather than the value's type, so
// both are leaves. A RecursiveVarType renders as its binder's name, so it is one too. An alias or
// class reference is not, even though one with no type arguments renders as a bare name: its
// argument list is exactly what a diagnostic needs bounded.
func isPrintLeaf(t Type) bool {
	switch t.(type) {
	case *TypeVarType, *PrimType, *LitType, *NeverType, *UnknownType, *ErrorType,
		*NullType, *UndefinedType, *MappedKeyType, *InferType, *TypeofType,
		*RecursiveVarType:
		return true
	}
	return false
}

// printType renders a coalesced type. Under the lazy deep-mut form (PR 14) the
// stored type already matches the surface annotation the user wrote, so the
// printer needs no special elision pass — `mut {a: {x}}` is stored and printed
// verbatim.
func (p *namedPrinter) printType(t Type) string {
	if p.maxDepth > 0 {
		if p.depth >= p.maxDepth && !isPrintLeaf(t) {
			return ElisionMark
		}
		p.depth++
		defer func() { p.depth-- }()
	}
	switch t := t.(type) {
	case *TypeVarType:
		// A retained type parameter renders under its assigned name; otherwise a
		// raw, un-coalesced variable. Coalesced monotype output never contains one
		// (every variable is inlined to its bounds, m1-implementation-plan Delta #1),
		// but the M2 walk records raw, var-carrying types in Info and only coalesces
		// at binding boundaries — so a consumer printing an inner node's type
		// directly may hand Print a live variable. Render it as `t{ID}` (matching
		// solver's describe()) rather than panicking. See the M2 plan §7.
		if p.names != nil {
			if name, ok := p.names[t]; ok {
				return name
			}
		}
		return "t" + strconv.Itoa(t.ID)
	case *PrimType:
		return printPrim(t.Prim)
	case *LitType:
		return printLit(t.Lit)
	case *NeverType:
		return "never"
	case *UnknownType:
		return "unknown"
	case *ErrorType:
		return "error"
	case *NullType:
		return "null"
	case *UndefinedType:
		return "undefined"
	case *TupleType:
		elems := make([]string, 0, len(t.Elems)+1)
		for _, e := range t.Elems {
			elems = append(elems, p.printType(e))
		}
		if t.Inexact {
			elems = append(elems, "...")
		}
		return "[" + strings.Join(elems, ", ") + "]"
	case *RestSpreadType:
		// A `...P` spread element renders with a `...` prefix so `[...P, x]` round-trips. The
		// operand prints at precPrefix so a looser operand such as a union gets parenthesized under
		// the `...`. The enclosing TupleType arm renders each element through printType, so a spread
		// element reaches here in place.
		return "..." + p.printTypeMinPrec(t.Operand, precPrefix)
	case *TemplateLitType:
		// A template literal renders `q0${i0}q1${i1}…qn`, interleaving the fixed segments with
		// each interpolation, so `on${T}` round-trips. Quasis holds one more entry than
		// Interps. Each interpolation prints with no minimum since the `${…}` braces stop anything
		// from binding across it.
		var b strings.Builder
		b.WriteString("`")
		for i, quasi := range t.Quasis {
			b.WriteString(quasi)
			if i < len(t.Interps) {
				b.WriteString("${" + p.printType(t.Interps[i]) + "}")
			}
		}
		b.WriteString("`")
		return b.String()
	case *StringIntrinsicType:
		// `Uppercase<T>` and its three siblings render under the operator's name with the operand as
		// the sole argument, so `Capitalize<K>` round-trips. The operand prints with no minimum since
		// the `<…>` brackets stop anything from binding across it.
		return t.Kind.String() + "<" + p.printType(t.Operand) + ">"
	case *ExactnessType:
		// `Exact<T>` and `Inexact<T>` render under the operator's name with the operand as the sole
		// argument, so the annotation round-trips. The operand prints with no minimum since the
		// `<…>` brackets stop anything from binding across it.
		return t.Kind.String() + "<" + p.printType(t.Operand) + ">"
	case *ObjectType:
		elems := make([]string, 0, len(t.Elems)+1)
		for _, e := range t.Elems {
			elems = append(elems, p.printObjElem(e))
		}
		if t.Inexact {
			elems = append(elems, "...")
		}
		return "{" + strings.Join(elems, ", ") + "}"
	case *ClassType:
		// A ClassType renders under its bare display name, with a `<...>` argument list
		// when it has arguments: `Point`, `Box<number>`, `Ref<'x, number>`. Lifetime
		// arguments render first, then type arguments, so a class holding borrowed data
		// shows its lifetime before its element type. The qualified Name carries a
		// namespace prefix for registry keying, stripped here for display. Lt and the
		// `mut` borrow forms come from a RefType wrapper, not this arm.
		name := classDisplayName(t.Name)
		if t.Variant {
			// An enum variant renders qualified by its enum, `Color.RGB`, so variants of
			// two enums sharing a name stay distinct; a plain class strips to its bare name.
			name = enumVariantDisplayName(t.Name)
		}
		if p.qualify {
			name = t.Name // full qualified name for a collision-free identity key
		}
		if len(t.TypeArgs) == 0 && len(t.LifetimeArgs) == 0 {
			return name
		}
		parts := make([]string, 0, len(t.LifetimeArgs)+len(t.TypeArgs))
		for _, la := range t.LifetimeArgs {
			parts = append(parts, p.printLifetime(la))
		}
		for _, a := range t.TypeArgs {
			parts = append(parts, p.printType(a))
		}
		return name + "<" + strings.Join(parts, ", ") + ">"
	case *AliasType:
		// An alias reference renders under its own name, with a `<...>` argument list when
		// a generic reference supplies arguments: `Point`, `Box<number>`, `Foo<'a>`. Lifetime
		// arguments render first, then type arguments, matching the ClassType order. The
		// qualified Name carries a namespace prefix for registry keying, stripped here for
		// display, the same way a class name is. Rendering the name rather than the expanded
		// body is the point of the alias, so `val p: Point = {x: 1}` shows `Point`.
		name := classDisplayName(t.Name)
		if p.qualify {
			name = t.Name // full qualified name for a collision-free identity key
		}
		if len(t.TypeArgs) == 0 && len(t.LifetimeArgs) == 0 {
			return name
		}
		parts := make([]string, 0, len(t.LifetimeArgs)+len(t.TypeArgs))
		for _, la := range t.LifetimeArgs {
			parts = append(parts, p.printLifetime(la))
		}
		for _, a := range t.TypeArgs {
			parts = append(parts, p.printType(a))
		}
		return name + "<" + strings.Join(parts, ", ") + ">"
	case *FuncType:
		return "fn " + p.printFuncTail(t)
	case *KeyofType:
		// `keyof T`, mirroring type_system's KeyOfType rendering. The operand prints at
		// precPrefix so a looser operand such as a union gets parenthesized: `keyof (A | B)`.
		return "keyof " + p.printTypeMinPrec(t.Operand, precPrefix)
	case *IndexType:
		// `T[K]`. The target prints at precAtom so a looser target such as a union or a
		// `keyof` gets parenthesized: `(A | B)["x"]`, `(keyof T)[K]`. The index sits inside
		// the brackets, where nothing can bind across it, so it prints with no minimum.
		return p.printTypeMinPrec(t.Target, precAtom) + "[" + p.printType(t.Index) + "]"
	case *TypeofType:
		// `typeof x`, the value reference the source wrote. The resolved value type is not
		// rendered — the query stays symbolic, so `keyof typeof x` prints as written.
		return "typeof " + t.Ident
	case *CondType:
		// `if Check : Extends { Then } else { Else }`, the surface conditional-type syntax the
		// parser reads and the AST printer writes. Each operand prints with no minimum precedence:
		// the `:`, `{`, `}`, and `else` delimiters bound every position, so none can bind across a
		// neighbor and none needs parens.
		return "if " + p.printType(t.Check) + " : " + p.printType(t.Extends) +
			" { " + p.printType(t.Then) + " } else { " + p.printType(t.Else) + " }"
	case *InferType:
		// The binder renders `infer U`, the clause the source wrote in the Extends operand, and a
		// reference to that name renders as the bare `U`, so a stored conditional round-trips to
		// `if T : [infer U] { U } else { boolean }`.
		if t.IsWildcardInfer() {
			// An anonymous binder round-trips to the `_` the source wrote.
			return t.Name
		}
		if t.Binder {
			return "infer " + t.Name
		}
		return t.Name
	case *MappedKeyType:
		// A mapped type's key variable renders as the bare name the source bound it under, so a
		// stored mapped type round-trips to `{[K]: T[K] for K in keyof T}`.
		return t.Name
	case *RecursiveType:
		// `μX0.<body>`, the μ form for a recursive type. Escalier has no surface syntax for one, so
		// this is the standard notation rather than a mirror of a parser form.
		//
		// The body prints at precAtom, so it is parenthesized unless it is already self-delimiting.
		// A μ binder is greedy, meaning it extends to the end of the enclosing form, so a body that
		// could run on has to be bounded here or it would swallow whatever follows the knot:
		// `μX0.number | X0` beside a `| string` would read as one three-member union. An object,
		// tuple, or bare name carries its own delimiter and needs nothing, which is why the common
		// `μX0.{next: X0}` stays bare. Bounding the body here is also what makes the knot an atom,
		// so it never needs parens of its own at the use site.
		return "μ" + t.Binder.DisplayName() + "." + p.printTypeMinPrec(t.Body, precAtom)
	case *RecursiveVarType:
		// A reference to the enclosing knot's binder renders as that binder's bare name, so
		// `μX0.{next: X0}` names one binding twice.
		return t.DisplayName()
	case *PromiseType:
		// A promise that can reject renders its rejection type as a second argument,
		// `Promise<T, E>`. A promise that cannot reject resolves its Err to `never` and
		// renders the one-argument `Promise<T>`, the same suppression printThrowsClause
		// applies to a signature that raises nothing.
		if t.Rejects() {
			return "Promise<" + p.printType(t.Inner) + ", " + p.printType(t.Err) + ">"
		}
		return "Promise<" + p.printType(t.Inner) + ">"
	case *GeneratorType:
		// A generator that can raise renders its raise type as a fourth argument,
		// `Generator<Y, R, N, E>`, the same shape `Promise<T, E>` takes. One that cannot
		// resolves its Throws to `never` and renders three arguments.
		slots := p.printType(t.Yield) + ", " + p.printType(t.Ret) + ", " + p.printType(t.Next)
		if t.Raises() {
			slots += ", " + p.printType(t.Throws)
		}
		return t.Name() + "<" + slots + ">"
	case *ArrayType:
		return "Array<" + p.printType(t.Elem) + ">"
	case *RefType:
		// Ownership and the borrow `&` split on Lt. An owned value has Lt nil and
		// renders bare. NewRef collapses the owned-immutable cell, so a surviving owned
		// RefType is always owned-mutable and renders `mut {x}`. A borrow has Lt set and
		// leads with `&`, then the lifetime name when it is load-bearing, then `mut`.
		// The four forms are:
		//
		//	&{x}        &mut {x}        &'a {x}        &'a mut {x}
		//
		// The inner prints at precPrefix so a looser inner such as a union or function
		// gets parenthesized. Under the lazy deep-mut form (PR 14) the inner is the
		// bare shape the user wrote, so it prints verbatim with no elision pass.
		return p.refBorrowPrefix(t) + p.printTypeMinPrec(t.Inner, precPrefix)
	case *UnionType:
		parts := make([]string, 0, len(t.Types))
		for _, m := range t.Types {
			parts = append(parts, p.printTypeMinPrec(m, precUnion))
		}
		return strings.Join(parts, " | ")
	case *IntersectionType:
		parts := make([]string, len(t.Types))
		for i, m := range t.Types {
			parts[i] = p.printTypeMinPrec(m, precIntersection)
		}
		return strings.Join(parts, " & ")
	case *NegationType:
		// `¬T`, the standard set-theoretic notation, matching the `¬` surface syntax the parser
		// reads and the AST printer writes. The operand prints at precPrefix, so a union renders
		// `¬(A | B)`, an intersection `¬(A & B)`, a function `¬(fn () -> number)`, and an atom
		// stays bare as `¬number`.
		return "¬" + p.printTypeMinPrec(t.Inner, precPrefix)
	case *SkolemType:
		// A skolem renders under its source parameter name. It is transient to a
		// checking-mode pass and does not survive into a displayed type, so this arm is
		// reached only defensively.
		return t.Name
	}
	panic(fmt.Sprintf("printType: unhandled %T", t))
}

// printMapped renders a mapped member in the surface syntax the parser reads and the AST printer
// writes. The enclosing object supplies the braces and any trailing `...`, the same way it does for
// an ordinary member.
//
// A member that does not remap its keys renders in the shorthand, `readonly [Key: Keys]?: Value`.
// The brackets are free to hold the key variable and its constraint, so nothing needs a trailing
// `for Key in Keys`. `{[K]: T[K] for K in keyof T}` therefore renders `{[K: keyof T]: T[K]}`, and
// its `?`-adding twin renders `{[K: keyof T]?: T[K]}`. An `if Check : Extends` filter trails the
// value in either form.
//
// A member that remaps its keys renders long, `readonly [Name]+?: Value for Key in Keys`, because
// the remapping expression occupies the brackets the shorthand needs for the constraint.
//
// Every operand prints with no minimum precedence: the brackets, the `:`, and the `for`, `in`, and
// `if` keywords bound each position, so none can bind across a neighbor.
func (p *namedPrinter) printMapped(t *MappedElem) string {
	out := ""
	switch t.Readonly {
	case ModAdd:
		out += "readonly "
	case ModRemove:
		out += "-readonly "
	case ModNone:
	}
	if MappedShorthandForm(t) {
		out += "[" + t.Key.Name + ": " + p.printType(t.Keys) + "]"
		out += ShorthandOptionalMarker(t.Optional)
	} else {
		out += "[" + p.printType(t.Name) + "]"
		switch t.Optional {
		case ModAdd:
			out += "+?"
		case ModRemove:
			out += "-?"
		case ModNone:
		}
	}
	out += ": " + p.printType(t.Value)
	if !MappedShorthandForm(t) {
		out += " for " + t.Key.Name + " in " + p.printType(t.Keys)
	}
	if t.Check != nil && t.Extends != nil {
		out += " if " + p.printType(t.Check) + " : " + p.printType(t.Extends)
	}
	return out
}

// ShorthandOptionalMarker renders a mapped member's `?` marker as the shorthand spells it, `?` where
// the long form writes `+?`. Exported so the solver's renderer shares it rather than copying it.
func ShorthandOptionalMarker(mod MappedModifier) string {
	switch mod {
	case ModAdd:
		return "?"
	case ModRemove:
		return "-?"
	case ModNone:
		return ""
	}
	panic(fmt.Sprintf("indexSigOptional: unhandled MappedModifier %v", mod))
}

// printObjElem renders one object member in Escalier surface syntax. Each kind has
// its own form:
//
//   - a property renders `name: T` with the `readonly` and `?` markers;
//   - a method renders `name(params) -> ret` per overload arm, arms joined by "; "
//     so the arm boundary stays distinct from the outer ", " between members;
//   - a getter renders `get name(self) -> T`, or `get name() -> T` when static;
//   - a setter renders `set name(self, value: T)`, or `set name(value: T)` when static;
//   - a constructor renders `new (params) -> ret`, the unnamed call signature of a
//     class value.
//
// A getter's or setter's self receiver renders through the same shorthand as a
// method's. It panics on an unknown element kind, matching AsProperty.
func (p *namedPrinter) printObjElem(e ObjTypeElem) string {
	switch e := e.(type) {
	case *MappedElem:
		return p.printMapped(e)
	case *PropertyElem:
		opt := ""
		if e.Optional {
			opt = "?"
		}
		ro := ""
		if e.Readonly {
			ro = "readonly "
		}
		return ro + printObjectKeyName(e.Name) + opt + ": " + p.printType(e.Type)
	case *MethodElem:
		arms := make([]string, len(e.Signatures))
		for i, sig := range e.Signatures {
			arms[i] = printObjectKeyName(e.Name) + p.printFuncTail(sig)
		}
		return strings.Join(arms, "; ")
	case *GetterElem:
		recv := ""
		if e.SelfParam != nil {
			recv = p.printSelfReceiver(e.SelfParam)
		}
		clause := p.printThrowsClause(e.ThrowsOrNever())
		if clause == "" {
			return "get " + printObjectKeyName(e.Name) + "(" + recv + ") -> " + p.printType(e.Type)
		}
		// `-> R` is greedy, so a function-typed return is parenthesized once a clause
		// follows it, the same bound printFuncBody puts on a signature's return.
		return "get " + printObjectKeyName(e.Name) + "(" + recv + ") -> " +
			p.printTypeMinPrec(e.Type, precUnion) + clause
	case *SetterElem:
		recv := ""
		if e.SelfParam != nil {
			recv = p.printSelfReceiver(e.SelfParam) + ", "
		}
		return "set " + printObjectKeyName(e.Name) + "(" + recv + "value: " + p.printType(e.Param) + ")" +
			p.printThrowsClause(e.ThrowsOrNever())
	case *ConstructorElem:
		// A class value's constructor renders as the unnamed call signature
		// `new (params) -> ret`.
		return "new " + p.printFuncTail(e.Fn)
	case *SpreadElem:
		// A `...A` spread renders inline among the object's fields, so `{...A, x: T}` round-trips
		// to the source. The operand prints at precPrefix, so a looser one such as a union gets
		// parenthesized.
		return "..." + p.printTypeMinPrec(e.Type, precPrefix)
	}
	panic(fmt.Sprintf("printObjElem: unhandled ObjTypeElem %T", e))
}

// classDisplayName strips the dep_graph namespace prefix off a qualified class
// name for display, so "Geometry.Point" renders as "Point". A bare name with no
// dot is returned unchanged.
func classDisplayName(qname string) string {
	if i := strings.LastIndex(qname, "."); i >= 0 {
		return qname[i+1:]
	}
	return qname
}

// enumVariantDisplayName renders an enum variant's qualified name as `Enum.Variant` —
// the last two dot-components — stripping any dep_graph namespace prefix while keeping
// the enum qualifier. A root-namespace `Color.RGB` and a namespaced `Geo.Color.RGB`
// both render `Color.RGB`. A name with fewer than two components is returned unchanged.
func enumVariantDisplayName(qname string) string {
	last := strings.LastIndex(qname, ".")
	if last < 0 {
		return qname
	}
	if prev := strings.LastIndex(qname[:last], "."); prev >= 0 {
		return qname[prev+1:]
	}
	return qname
}

// refBorrowPrefix renders the ownership and borrow prefix of a RefType: "" for an
// owned-immutable cell, "mut " for owned-mutable, "&" or "&'a " for an immutable
// borrow, and "&mut " or "&'a mut " for a mutable borrow. The RefType arm and the
// method self-receiver share it so a borrow renders the same in both places.
func (p *namedPrinter) refBorrowPrefix(t *RefType) string {
	prefix := ""
	if t.Lt != nil {
		prefix = "&"
		if name := p.borrowLifetimeName(t.Lt); name != "" {
			prefix += name + " "
		}
	}
	if t.Mut {
		prefix += "mut "
	}
	return prefix
}

// printSelfReceiver renders a method's receiver as the Rust-style shorthand, reading
// it back from the desugared receiver type. An owned receiver `Self` renders `self`.
// The `mut Self`, `&Self`, and `&mut Self` receivers render `mut self`, `&self`, and
// `&mut self` through the shared borrow prefix, so a named borrow lifetime renders
// `&'a self`.
func (p *namedPrinter) printSelfReceiver(sp *FuncParam) string {
	if ref, ok := sp.Type.(*RefType); ok {
		return p.refBorrowPrefix(ref) + "self"
	}
	return "self"
}

// printFuncTail renders the "(params) -> ret" portion of a function, without the
// leading "fn" keyword. Kept as a separate helper so PrintAsScheme can compose it
// with a <...> quantifier prefix without byte-slicing the "fn " back off.
//
// A method's self receiver renders first as its shorthand, so an instance method
// reads `(self, x: T) -> R` or `(mut self) -> R`. PR4 markers follow: an optional
// parameter renders as `x?: T`, and an INEXACT function renders a trailing `...`
// entry (`fn (x: T, ...) -> R`) so the exactness it carries round-trips to surface
// syntax. An exact function with no receiver renders with no marker.
func (p *namedPrinter) printFuncTail(t *FuncType) string {
	// The signature's own type parameters are in scope only inside it, so their names are
	// released on the way out and a sibling signature is free to reuse them.
	scoped := p.bindTypeParams(t.TypeParams)
	defer p.releaseNames(scoped)
	p.nameLifetimeParams(t.LifetimeParams)
	binders := append(p.typeParamBinders(t.TypeParams), p.lifetimeParamBinders(t.LifetimeParams)...)
	prefix := ""
	if len(binders) > 0 {
		prefix = "<" + strings.Join(binders, ", ") + ">"
	}
	return prefix + p.printFuncBody(t)
}

// printFuncBody renders the "(receiver, params) -> ret" portion with NO quantifier
// prefix, so a caller that emits its own combined prefix — PrintAsSchemeWith merging
// scheme-bound variables with the function's own type parameters — does not render the
// type parameters twice. The body may reference the type parameters, so a caller must
// register their names with nameTypeParams first.
func (p *namedPrinter) printFuncBody(t *FuncType) string {
	ps := make([]string, 0, len(t.Params)+2)
	if t.SelfParam != nil {
		ps = append(ps, p.printSelfReceiver(t.SelfParam))
	}
	for i, param := range t.Params {
		rest := ""
		if param.Rest {
			rest = "..." // a typed rest param renders `...xs: T`
		}
		opt := ""
		if param.Optional {
			opt = "?"
		}
		ps = append(ps, rest+paramName(param, i)+opt+": "+p.printType(param.Type))
	}
	if t.Inexact {
		ps = append(ps, "...")
	}
	// A `throws T` clause renders after the return type, matching the surface syntax.
	clause := p.printThrowsClause(t.ThrowsOrNever())
	if clause == "" {
		return "(" + strings.Join(ps, ", ") + ") -> " + p.printType(t.Ret)
	}
	// `-> R` is greedy, so a function-typed return is parenthesized once a clause follows
	// it — `fn () -> (fn () -> number) throws string` — or the clause re-reads as the inner
	// function's. precUnion bounds a function type and nothing else, precFunc being the
	// only precedence below it.
	return "(" + strings.Join(ps, ", ") + ") -> " + p.printTypeMinPrec(t.Ret, precUnion) + clause
}

// printThrowsClause renders a signature's ` throws T` suffix, or the empty string when
// there is nothing to raise. A member that raises nothing resolves to `never` and renders
// no clause, so `fn () -> number` and `get x(self) -> number` stay the common forms. That
// covers a coalesced throws variable nothing reached as well as a signature minted with no
// clause at all. The clause needs no minimum precedence: it is last, so nothing can bind
// across its right edge.
func (p *namedPrinter) printThrowsClause(throws Type) string {
	if isNever(throws) {
		return ""
	}
	return " throws " + p.printType(throws)
}

// isNever reports whether t is the `never` type.
func isNever(t Type) bool {
	_, never := t.(*NeverType)
	return never
}

// typeParamBinders renders each type parameter as a binder string — `U`, `U: T` for a
// constraint, `U = D` for a default, or `U: T = D` for both — without the surrounding
// `<>`. The constraint is the parameter variable's upper bound. A variable with several
// upper bounds renders them joined by ` & `. The parameters must be bound first, through
// bindTypeParams. Each binder then renders under its own name, and a binder whose constraint
// or default names a sibling parameter renders that name too. Callers that build a
// combined quantifier prefix, such as PrintAsSchemeWith, join these with the scheme's
// free variables and lifetimes into one list.
func (p *namedPrinter) typeParamBinders(tps []*TypeParam) []string {
	binders := make([]string, len(tps))
	for i, tp := range tps {
		s := p.printType(tp.Var) // the registered source name, else t{ID}
		if bounds := tp.Var.UpperBounds; len(bounds) > 0 {
			rendered := make([]string, len(bounds))
			for j, b := range bounds {
				rendered[j] = p.printType(b)
			}
			s += ": " + strings.Join(rendered, " & ")
		}
		if tp.Default != nil {
			s += " = " + p.printType(tp.Default)
		}
		binders[i] = s
	}
	return binders
}

// nameLifetimeParams registers each lifetime parameter's variable under its source name
// in the printer's ltNames map, so a use of the parameter inside the receiver, params,
// return, or another parameter's bound renders as that name rather than the raw `'l{ID}`
// debug form. It allocates the map lazily, since plain Print starts with none. It is the
// lifetime-sort twin of nameTypeParams.
func (p *namedPrinter) nameLifetimeParams(lps []*LifetimeParam) {
	if len(lps) == 0 {
		return
	}
	if p.ltNames == nil {
		p.ltNames = map[*LifetimeVar]string{}
	}
	for _, lp := range lps {
		if lp.Name != "" {
			p.ltNames[lp.Var] = lp.Name
		}
	}
}

// lifetimeParamBinders renders each lifetime parameter as a binder string — `'a`, or
// `'b: 'a` for an outlives bound and `'b: 'a & 'c` for several — without the surrounding
// `<>`. The bound is the parameter's declared outlives list. nameLifetimeParams must run
// first so a binder that references another parameter renders under its name. It is the
// lifetime-sort twin of typeParamBinders, joined into the same combined quantifier
// prefix so a method renders `fn <U, 'a>(...)`.
func (p *namedPrinter) lifetimeParamBinders(lps []*LifetimeParam) []string {
	binders := make([]string, len(lps))
	for i, lp := range lps {
		s := p.printLifetime(lp.Var) // the registered source name, else 'l{ID}
		if len(lp.Bounds) > 0 {
			rendered := make([]string, len(lp.Bounds))
			for j, b := range lp.Bounds {
				rendered[j] = p.printLifetime(b)
			}
			s += ": " + strings.Join(rendered, " & ")
		}
		binders[i] = s
	}
	return binders
}

// paramName renders p.Pattern. M1's only Pat concrete is IdentPat; a nil or
// otherwise-unknown pattern falls back to a positional name ("arg0", "arg1",
// ...). M2's destructuring Pat concretes add their own arms here. The optional
// `?` marker is appended by printFuncTail, not here, so callers that only want
// the bare name (none today) stay unaffected.
func paramName(p *FuncParam, i int) string {
	if s, ok := printPat(p.Pattern); ok {
		return s
	}
	return "arg" + strconv.Itoa(i)
}

// printPat renders a parameter pattern in Escalier surface syntax (M4 E1). A
// pattern carries only sub-patterns and literal values, never a Type, so it
// renders without the namedPrinter's type context. ok=false for a nil or unknown
// pattern, so paramName falls back to a positional name.
func printPat(pat Pat) (string, bool) {
	switch p := pat.(type) {
	case *IdentPat:
		return p.Name, true
	case *WildcardPat:
		return "_", true
	case *LitPat:
		return printLit(p.Lit), true
	case *NullPat:
		return "null", true
	case *UndefinedPat:
		return "undefined", true
	case *TuplePat:
		parts := make([]string, len(p.Elems))
		for i, e := range p.Elems {
			s, ok := printPat(e)
			if !ok {
				s = "_"
			}
			parts[i] = s
		}
		return "[" + strings.Join(parts, ", ") + "]", true
	case *RestPat:
		s, ok := printPat(p.Pattern)
		if !ok {
			s = "_"
		}
		return "..." + s, true
	case *ObjectPat:
		parts := make([]string, 0, len(p.Fields)+1)
		for _, f := range p.Fields {
			// A shorthand `{x}` is a field whose value is the IdentPat `x`, so render
			// it as the bare key. Any other sub-pattern renders `name: subpat`.
			if ip, ok := f.Value.(*IdentPat); ok && ip.Name == f.Name {
				parts = append(parts, printObjectKeyName(f.Name))
				continue
			}
			s, ok := printPat(f.Value)
			if !ok {
				s = "_"
			}
			parts = append(parts, printObjectKeyName(f.Name)+": "+s)
		}
		// A rest renders after the named fields, the only position the source may
		// write it at.
		if p.Rest != nil {
			s, ok := printPat(p.Rest)
			if !ok {
				s = "_"
			}
			parts = append(parts, "..."+s)
		}
		return "{" + strings.Join(parts, ", ") + "}", true
	case *ExtractorPat:
		parts := make([]string, len(p.Args))
		for i, a := range p.Args {
			s, ok := printPat(a)
			if !ok {
				s = "_"
			}
			parts[i] = s
		}
		return p.Name + "(" + strings.Join(parts, ", ") + ")", true
	case *InstancePat:
		obj, ok := printPat(p.Object)
		if !ok {
			obj = "{}"
		}
		return p.ClassName + " " + obj, true
	}
	return "", false
}

// printObjectKeyName renders an object property name as Escalier surface syntax:
// a bare label when the name is a valid identifier, otherwise a quoted string
// key (e.g. "a-b", a key that came from a string-literal property). This keeps
// the rendered object parseable; an unquoted "a-b" would corrupt the type.
func printObjectKeyName(name string) string {
	if isIdent(name) {
		return name
	}
	return strconv.Quote(name)
}

// isIdent reports whether name is a valid Escalier identifier: non-empty, with a
// leading letter or underscore and letter/underscore/digit runes thereafter.
func isIdent(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if i == 0 {
			if !unicode.IsLetter(r) && r != '_' {
				return false
			}
			continue
		}
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			return false
		}
	}
	return true
}

// printPrim maps a Prim to its Escalier surface name — mirrors
// type_system/print_type.go's printPrimType.
func printPrim(p Prim) string {
	switch p {
	case NumPrim:
		return "number"
	case StrPrim:
		return "string"
	case BoolPrim:
		return "boolean"
	}
	panic(fmt.Sprintf("printPrim: unhandled Prim %d", p))
}

// printLit renders a literal value in Escalier surface syntax.
func printLit(lit Lit) string {
	switch lit := lit.(type) {
	case *StrLit:
		return strconv.Quote(lit.Value)
	case *NumLit:
		// 64-bit precision, matching solver's describe() (see its comment):
		// NumLit.Value is a float64, so bitSize 32 would round-trip through
		// float32 and misrender values beyond float32's range/mantissa.
		// type_system's printer still uses bitSize 32 here — a latent bug noted
		// in describe — so this is the one surface form where Print is
		// deliberately more correct than the renderer it otherwise mirrors.
		return strconv.FormatFloat(lit.Value, 'f', -1, 64)
	case *BoolLit:
		return strconv.FormatBool(lit.Value)
	}
	panic(fmt.Sprintf("printLit: unhandled Lit %T", lit))
}
