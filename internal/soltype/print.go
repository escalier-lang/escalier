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
// type_system/print_type.go). Higher values bind more tightly. M4 adds precPrefix
// for the `mut`/lifetime borrow prefix (RefType); type_system's other prefix forms
// (keyof, ...T) land with their later milestones.
const (
	precFunc         = 2 // fn (...) -> T — return type is greedy, needs parens in union/intersection
	precUnion        = 3 // A | B
	precIntersection = 4 // A & B
	precPrefix       = 5 // mut T, 'a T — a borrow prefix binds looser than an atom
	precAtom         = 6 // primary types, never need parens
)

// typePrec returns the printing precedence of a coalesced M1 type.
func typePrec(t Type) int {
	switch t.(type) {
	case *FuncType:
		return precFunc
	case *UnionType:
		return precUnion
	case *IntersectionType:
		return precIntersection
	case *RefType:
		return precPrefix
	default:
		// PrimType, LitType, TupleType, ObjectType, ClassType, Void, NeverType,
		// UnknownType — atoms. ObjectType is brace-delimited and ClassType renders as
		// a bare name or `Name<args>`, so neither needs parens. A raw TypeVarType
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
// (PrimType/LitType/FuncType/TupleType/Void/NeverType/UnknownType/UnionType/
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
	return PrintAsSchemeWith(t, func(*TypeVarType) bool { return true }, nil)
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
func PrintAsSchemeWith(t Type, isParam func(*TypeVarType) bool, ltBounds map[*LifetimeVar][]*LifetimeVar) string {
	names := map[*TypeVarType]string{}
	var labels []string
	for _, v := range freeTypeVars(t) {
		if !isParam(v) {
			continue // non-parameter free var → left unnamed → renders as t{ID}
		}
		name := typeParamName(len(labels))
		names[v] = name
		labels = append(labels, name)
	}
	// Borrow lifetimes left in the coalesced type by D4's coalesceLifetimes are all
	// nameable. A connect-nothing one was already elided; a param lifetime and a kept
	// join lifetime both survive here. Name each 'a, 'b, … in first-appearance order
	// and add it to the quantifier prefix after the type parameters.
	ltVars := freeLifetimeVars(t)
	ltNames := map[*LifetimeVar]string{}
	ltIndex := map[*LifetimeVar]int{}
	for i, lv := range ltVars {
		ltNames[lv] = lifetimeParamName(i)
		ltIndex[lv] = i
	}
	if len(labels) == 0 && len(ltVars) == 0 {
		// No quantified parameters: render as a plain (possibly raw-var) type, which
		// keeps a leaked variable visible as t{ID}.
		return Print(t)
	}
	p := &namedPrinter{names: names, ltNames: ltNames}
	if _, ok := t.(*ClassType); ok {
		// A class instance already displays its type parameters inline in its `<...>`
		// argument list, so it needs no separate quantifier prefix. A generalized
		// Map<K, V> renders as Map<T0, T1>, not <T0, T1> Map<T0, T1>. A ClassType's only
		// free-variable children are its arguments, so every quantified variable is
		// shown inline and none is lost by dropping the prefix.
		return p.printType(t)
	}
	ltLabels := make([]string, len(ltVars))
	for i, lv := range ltVars {
		ltLabels[i] = p.lifetimeBinder(lv, ltBounds[lv], ltIndex)
	}
	if ft, ok := t.(*FuncType); ok {
		// Merge the scheme's free variables, the function's OWN type parameters, and the
		// lifetimes into one ordered prefix, so a generic method that also captures a
		// scheme variable renders `fn <T0, U, 'a>(...)` rather than two adjacent groups.
		// printFuncBody omits the type-param prefix so the own parameters are not repeated.
		p.nameTypeParams(ft.TypeParams)
		binders := append([]string{}, labels...)
		binders = append(binders, p.typeParamBinders(ft.TypeParams)...)
		binders = append(binders, ltLabels...)
		return "fn <" + strings.Join(binders, ", ") + ">" + p.printFuncBody(ft)
	}
	prefix := "<" + strings.Join(append(labels, ltLabels...), ", ") + ">"
	return prefix + " " + p.printType(t)
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
	if r, ok := t.(*RefType); ok {
		c.add(r.Lt)
	}
	return EnterResult{}
}

func (c *ltVarCollector) ExitType(t Type, _ Polarity) Type { return t }

// add records a borrow's lifetime when it is a LifetimeVar, deduped by identity.
// 'static and an anonymous display lifetime carry no variable and are skipped.
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
				case *SetterElem:
					if e.SelfParam != nil {
						walk(e.SelfParam.Type)
					}
					walk(e.Param)
				}
			}
		case *ClassType:
			for _, a := range t.Args {
				walk(a)
			}
		case *PromiseType:
			walk(t.Inner)
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
	// `'l{ID}` debug form; D4's display-time coalescing populates it so a
	// param-originated lifetime renders under its quantified name.
	ltNames map[*LifetimeVar]string
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

// printType renders a coalesced type. Under the lazy deep-mut form (PR 14) the
// stored type already matches the surface annotation the user wrote, so the
// printer needs no special elision pass — `mut {a: {x}}` is stored and printed
// verbatim.
func (p *namedPrinter) printType(t Type) string {
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
	case *Void:
		return "void"
	case *NullType:
		return "null"
	case *TupleType:
		elems := make([]string, 0, len(t.Elems)+1)
		for _, e := range t.Elems {
			elems = append(elems, p.printType(e))
		}
		if t.Inexact {
			elems = append(elems, "...")
		}
		return "[" + strings.Join(elems, ", ") + "]"
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
		// A ClassType renders under its bare display name, with a `<...>` type-argument
		// list when it has arguments: `Point`, `Box<number>`. The qualified Name carries
		// a namespace prefix for registry keying, stripped here for display. Lt and the
		// `mut` borrow forms come from a RefType wrapper, not this arm.
		name := classDisplayName(t.Name)
		if len(t.Args) == 0 {
			return name
		}
		args := make([]string, len(t.Args))
		for i, a := range t.Args {
			args[i] = p.printType(a)
		}
		return name + "<" + strings.Join(args, ", ") + ">"
	case *FuncType:
		return "fn " + p.printFuncTail(t)
	case *PromiseType:
		return "Promise<" + p.printType(t.Inner) + ">"
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
		// An inexact union renders a trailing `...` entry, so a union typed
		// `A | B | ...` round-trips to surface syntax. The inexact tuple,
		// object, and function arms render their flag the same way.
		parts := make([]string, 0, len(t.Types)+1)
		for _, m := range t.Types {
			parts = append(parts, p.printTypeMinPrec(m, precUnion))
		}
		if t.Inexact {
			parts = append(parts, "...")
		}
		return strings.Join(parts, " | ")
	case *IntersectionType:
		parts := make([]string, len(t.Types))
		for i, m := range t.Types {
			parts[i] = p.printTypeMinPrec(m, precIntersection)
		}
		return strings.Join(parts, " & ")
	}
	panic(fmt.Sprintf("printType: unhandled %T", t))
}

// printObjElem renders one object member in Escalier surface syntax. Each kind has
// its own form:
//
//   - a property renders `name: T` with the `readonly` and `?` markers;
//   - a method renders `name(params) -> ret` per overload arm, arms joined by "; "
//     so the arm boundary stays distinct from the outer ", " between members;
//   - a getter renders `get name(self) -> T`, or `get name() -> T` when static;
//   - a setter renders `set name(self, value: T)`, or `set name(value: T)` when static.
//
// A getter's or setter's self receiver renders through the same shorthand as a
// method's. It panics on an unknown element kind, matching AsProperty.
func (p *namedPrinter) printObjElem(e ObjTypeElem) string {
	switch e := e.(type) {
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
		return "get " + printObjectKeyName(e.Name) + "(" + recv + ") -> " + p.printType(e.Type)
	case *SetterElem:
		recv := ""
		if e.SelfParam != nil {
			recv = p.printSelfReceiver(e.SelfParam) + ", "
		}
		return "set " + printObjectKeyName(e.Name) + "(" + recv + "value: " + p.printType(e.Param) + ")"
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
	p.nameTypeParams(t.TypeParams)
	return p.printTypeParams(t.TypeParams) + p.printFuncBody(t)
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
	return "(" + strings.Join(ps, ", ") + ") -> " + p.printType(t.Ret)
}

// nameTypeParams registers each type parameter's binding variable under its source name
// in the printer's name map, so a use of the parameter inside the params, return,
// constraints, or defaults renders as that name rather than the raw t{ID} debug form. It
// allocates the map lazily, since plain Print starts with none. A parameter with no
// source name is left unregistered and falls back to t{ID}.
func (p *namedPrinter) nameTypeParams(tps []*TypeParam) {
	if len(tps) == 0 {
		return
	}
	if p.names == nil {
		p.names = map[*TypeVarType]string{}
	}
	for _, tp := range tps {
		if tp.Name != "" {
			p.names[tp.Var] = tp.Name
		}
	}
}

// typeParamBinders renders each type parameter as a binder string — `U`, `U: T` for a
// constraint, `U = D` for a default, or `U: T = D` for both — without the surrounding
// `<>`. The constraint is the parameter variable's upper bound. A variable with several
// upper bounds renders them joined by ` & `. nameTypeParams must run first so a binder
// that references another parameter renders under its name. Callers that build a
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

// printTypeParams wraps a function's own type-parameter binders in a `<...>` prefix,
// so a generic function renders `<U>`, `<U: T>`, `<U = D>`, or `<U: T = D>`. An empty
// slice renders "", so a monomorphic function shows no prefix. nameTypeParams must run
// first. See typeParamBinders for the per-parameter form.
func (p *namedPrinter) printTypeParams(tps []*TypeParam) string {
	if len(tps) == 0 {
		return ""
	}
	return "<" + strings.Join(p.typeParamBinders(tps), ", ") + ">"
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
	case *ObjectPat:
		parts := make([]string, len(p.Fields))
		for i, f := range p.Fields {
			// A shorthand `{x}` is a field whose value is the IdentPat `x`, so render
			// it as the bare key. Any other sub-pattern renders `name: subpat`.
			if ip, ok := f.Value.(*IdentPat); ok && ip.Name == f.Name {
				parts[i] = printObjectKeyName(f.Name)
				continue
			}
			s, ok := printPat(f.Value)
			if !ok {
				s = "_"
			}
			parts[i] = printObjectKeyName(f.Name) + ": " + s
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
