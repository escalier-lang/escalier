package soltype

import (
	"fmt"
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
		// PrimType, LitType, TupleType, ObjectType, Void, NeverType, UnknownType —
		// atoms (ObjectType is brace-delimited, so it never needs parens). A
		// raw TypeVarType (which appears only when printing an un-coalesced type,
		// see printType) is also an atom (rendered as `t{ID}`), so it lands here.
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
func PrintAsScheme(t Type) string {
	return PrintAsSchemeWith(t, func(*TypeVarType) bool { return true })
}

// PrintAsSchemeWith renders a generalized type, naming ONLY the free variables
// isParam accepts as quantified type parameters; any other free variable renders
// as the raw `t{ID}` debug form instead of being masked as a parameter. This
// preserves the leak anchor: a variable coalescing failed to inline (a captured
// var that escaped, a stray inference var) shows as `t{ID}` rather than a spurious
// `<Tn>` that would make a malformed signature look valid.
func PrintAsSchemeWith(t Type, isParam func(*TypeVarType) bool) string {
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
	// nameable param lifetimes. A connect-nothing one was already elided, and a join
	// expanded to a union of these. Name each 'a, 'b, … in first-appearance order and
	// add it to the quantifier prefix after the type parameters.
	ltNames := map[*LifetimeVar]string{}
	var ltLabels []string
	for _, lv := range freeLifetimeVars(t) {
		name := lifetimeParamName(len(ltLabels))
		ltNames[lv] = name
		ltLabels = append(ltLabels, name)
	}
	if len(labels) == 0 && len(ltLabels) == 0 {
		// No quantified parameters: render as a plain (possibly raw-var) type, which
		// keeps a leaked variable visible as t{ID}.
		return Print(t)
	}
	p := &namedPrinter{names: names, ltNames: ltNames}
	prefix := "<" + strings.Join(append(labels, ltLabels...), ", ") + ">"
	if ft, ok := t.(*FuncType); ok {
		return "fn " + prefix + p.printFuncTail(ft)
	}
	return prefix + " " + p.printType(t)
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

// add records a borrow's lifetime: a LifetimeVar directly, or each LifetimeVar
// member of a LifetimeUnion, deduped by identity.
func (c *ltVarCollector) add(lt Lifetime) {
	switch lt := lt.(type) {
	case *LifetimeVar:
		if !c.seen.Contains(lt) {
			c.seen.Add(lt)
			c.out = append(c.out, lt)
		}
	case *LifetimeUnion:
		for _, m := range lt.Lifetimes {
			if mv, ok := m.(*LifetimeVar); ok && !c.seen.Contains(mv) {
				c.seen.Add(mv)
				c.out = append(c.out, mv)
			}
		}
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
				walk(AsProperty(e).Type)
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
	case *LifetimeVar:
		if p.ltNames != nil {
			if name, ok := p.ltNames[lt]; ok {
				return name
			}
		}
		return "'l" + strconv.Itoa(lt.ID)
	case *LifetimeUnion:
		// The union form a join lifetime coalesces to. It is the union of the param
		// lifetimes it reaches, parenthesized so the `mut`/borrow prefix binds the
		// whole union, giving `mut ('a | 'b) {…}` rather than `mut 'a | 'b {…}`.
		parts := make([]string, len(lt.Lifetimes))
		for i, m := range lt.Lifetimes {
			parts[i] = p.printLifetime(m)
		}
		return "(" + strings.Join(parts, " | ") + ")"
	}
	panic(fmt.Sprintf("printLifetime: unhandled %T", lt))
}

// borrowLifetimeName returns the lifetime to print after a borrow's leading `&`, or
// "" when the lifetime is inferred and carries no load-bearing name. A LifetimeVar
// renders its assigned quantifier name `'a` when ltNames carries one. An un-named var
// is an inferred borrow and prints as a bare `&` with no lifetime, matching the display
// rule that names a lifetime only when it is load-bearing. 'static and a coalesced
// lifetime union are always shown. The `&` itself is emitted by the caller whenever Lt
// is set, so a borrow is always distinguishable from an owned value.
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
	case *LifetimeUnion:
		return p.printLifetime(lt)
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
			prop := AsProperty(e)
			opt := ""
			if prop.Optional {
				opt = "?"
			}
			ro := ""
			if prop.Readonly {
				ro = "readonly "
			}
			elems = append(elems, ro+printObjectKeyName(prop.Name)+opt+": "+p.printType(prop.Type))
		}
		if t.Inexact {
			elems = append(elems, "...")
		}
		return "{" + strings.Join(elems, ", ") + "}"
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
		// gets parenthesized.
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
		return prefix + p.printTypeMinPrec(t.Inner, precPrefix)
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

// printFuncTail renders the "(params) -> ret" portion of a function, without the
// leading "fn" keyword. Kept as a separate helper so PrintAsScheme can compose it
// with a <...> quantifier prefix without byte-slicing the "fn " back off.
//
// PR4 markers: an optional parameter renders as `x?: T`, and an INEXACT function
// renders a trailing `...` entry (`fn (x: T, ...) -> R`) so the exactness it
// carries round-trips to surface syntax. An exact function (the common case)
// renders with no marker.
func (p *namedPrinter) printFuncTail(t *FuncType) string {
	ps := make([]string, 0, len(t.Params)+1)
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
