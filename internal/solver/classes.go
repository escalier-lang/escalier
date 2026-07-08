package solver

import (
	"fmt"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// ClassDef is the heavy per-class data the nominal token soltype.ClassType points
// at. inferClassDecl builds one per class declaration and registers it on the
// Context under the class's dep_graph-qualified name; member lookup reads the
// projected Body, and the nominal constrain rule (C1) reads Supers and Variance.
// Keeping this data out of soltype.ClassType lets the token stay a small, cheap-to-
// compare identity.
type ClassDef struct {
	// TypeParams are the class's own quantified type parameters in declaration order,
	// each carrying its constraint as its Var's upper bound and its resolved default.
	// nil for a non-generic class.
	TypeParams []*soltype.TypeParam
	// LifetimeParams are the class's quantified lifetime parameters (A3), the lifetime
	// twin of TypeParams. nil for a class that holds no borrowed data.
	LifetimeParams []*soltype.LifetimeParam
	// Variance records one entry per TypeParam, dispatched per position by the nominal
	// constrain rule. B1 leaves every entry Invariant, the conservative default;
	// variance inference (C2) overwrites it.
	Variance []Variance
	// Supers holds the resolved `extends` superclass followed by each resolved
	// `implements` interface — the declared subtype-graph edges. The rule that walks
	// them transitively is C1; B1 only records them.
	Supers []*soltype.ClassType
	// Body is the instance member view a class projects: one element per field,
	// method, getter, and setter. Member access and the class-vs-object constrain
	// rule read it.
	Body *soltype.ObjectType
	// Static is the constructor-plus-static-member view — the value side of the dual
	// binding. B1 stores static members here for later phases; the callable
	// constructor itself is the value binding's FuncType.
	Static *soltype.ObjectType
	// Level is the class binding's generalize level. A generic method's own type
	// parameters live deeper than this, so member access wraps a resolved method in a
	// scheme quantified at this level and instantiates it per access.
	Level int
}

// Variance is a type parameter's variance — how the subtype relation on a class
// instance depends on that parameter's argument.
type Variance int

const (
	// Invariant requires the argument to match in both directions. It is the default
	// until inference runs, the conservative choice a sound constrain rule can always
	// fall back to.
	Invariant Variance = iota
	// Covariant lets a subtype argument make a subtype instance, as a read-only field
	// of that type would.
	Covariant
	// Contravariant flips the direction, as a write-only or parameter position would.
	Contravariant
	// Bivariant imposes no constraint — a phantom parameter that appears nowhere in
	// the body.
	Bivariant
)

// projectClassBody returns the instance member view of a class instance: the
// registered Body with each class type parameter replaced by the instance's
// corresponding type argument and each lifetime parameter by its lifetime argument.
// A non-generic instance, or one whose class is unregistered, projects the stored
// Body unchanged. It is the single path member lookup and the class-vs-object
// constrain rule use to read a class instance structurally.
func (c *checker) projectClassBody(ct *soltype.ClassType) (*soltype.ObjectType, bool) {
	def, ok := c.ctx.classDef(ct.Name)
	if !ok || def.Body == nil {
		return nil, false
	}
	if len(def.TypeParams) == 0 && len(def.LifetimeParams) == 0 {
		return def.Body, true
	}
	subst := newClassSubst(def, ct)
	projected := def.Body.Accept(subst, soltype.Positive)
	obj, ok := projected.(*soltype.ObjectType)
	if !ok {
		// Substitution replaces only vars and lifetimes, so an ObjectType body always
		// projects to an ObjectType; a different kind means the substitution corrupted
		// the body. Fail loudly rather than return the unsubstituted body, matching the
		// AsProperty discipline.
		panic(fmt.Sprintf("projectClassBody: %s projected to non-ObjectType %T", ct.Name, projected))
	}
	return obj, true
}

// projectedMember resolves a member access against a class instance or against a
// class body's method/getter member by direct lookup through the projected body,
// returning ok=false when the receiver is neither — a plain object property, or a type
// variable — so the caller falls back to the structural field-requirement path. A
// class instance whose class has no such member reports the miss here.
//
// Only a class receiver is intercepted. A plain object keeps the structural
// field-requirement path, which threads the read-through-borrow and read-after-write
// rules a direct lookup would drop; a method or getter member reaches valueProp only
// through a class instance, since class bodies are the only source of those elements.
func (c *checker) projectedMember(lvl int, blame ast.Node, name string, carrier soltype.Type) (pathResult, bool) {
	ct, ok := classCarrier(carrier)
	if !ok {
		return pathResult{}, false
	}
	obj, ok := c.projectClassBody(ct)
	if !ok {
		return pathResult{}, false
	}
	member, found := obj.Member(name)
	if !found {
		err := &MissingPropertyError{Sub: obj, Super: propReq(name, &soltype.UnknownType{}, false), Name: name}
		err.prov, err.site = c.prov, blame
		c.errs = append(c.errs, err)
		return pathResult{value: &soltype.ErrorType{}}, true
	}
	return c.memberValue(lvl, blame, member), true
}

// classCarrier resolves a receiver to the class instance it reads as: a ClassType
// directly, or a type variable whose lower bounds carry one — the same look-through
// resolveFunc uses to find a concrete callee behind a binding var, since a class
// instance flows through the bound graph as a variable with a ClassType lower bound
// rather than a bare ClassType.
//
// It resolves only an unambiguous class: a variable whose lower bounds carry two
// different instantiations is not resolved, so member access falls to the structural
// path rather than silently projecting whichever appears first. This covers a join of
// distinct classes such as `Foo(…)` and `Bar(…)`, and a join of the same class at
// different arguments such as `Box(1)` and `Box("s")`, whose members differ by
// argument. Member access on such a union rides the nominal-vs-structural rule in C1.
func classCarrier(t soltype.Type) (*soltype.ClassType, bool) {
	switch t := t.(type) {
	case *soltype.ClassType:
		return t, true
	case *soltype.TypeVarType:
		var found *soltype.ClassType
		for _, lb := range t.LowerBounds {
			ct, ok := lb.(*soltype.ClassType)
			if !ok {
				continue
			}
			if found != nil && !equalType(found, ct) {
				return nil, false
			}
			found = ct
		}
		if found != nil {
			return found, true
		}
	}
	return nil, false
}

// memberValue produces the value a member access yields: a property's or getter's
// type directly, or a method's callable signature with the receiver applied — the
// signature with its SelfParam stripped, since `p.m` binds the receiver and returns a
// function of the remaining parameters. Reading a setter-only member is a write-only
// access and is reported. An overloaded method resolves its arm at the call site (E1);
// B1 hands back the first signature.
func (c *checker) memberValue(lvl int, blame ast.Node, member soltype.ObjTypeElem) pathResult {
	var out soltype.Type
	switch m := member.(type) {
	case *soltype.PropertyElem:
		out = m.Type
	case *soltype.GetterElem:
		out = m.Type
	case *soltype.MethodElem:
		if len(m.Signatures) == 0 {
			out = &soltype.ErrorType{}
			break
		}
		sig := m.Signatures[0]
		out = &soltype.FuncType{
			Params:         sig.Params,
			Ret:            sig.Ret,
			Inexact:        sig.Inexact,
			TypeParams:     sig.TypeParams,
			LifetimeParams: sig.LifetimeParams,
		}
	case *soltype.SetterElem:
		out = c.report(&WriteOnlyPropertyError{Name: m.Name, Site: blame})
	default:
		out = &soltype.ErrorType{}
	}
	c.recordType(blame, out)
	return pathResult{value: out}
}

// classSubst rewrites a class body's type-parameter and lifetime-parameter vars to
// the arguments of one instance. It maps each TypeParam.Var to the instance's
// positional TypeArg and each LifetimeParam.Var to its positional LifetimeArg, so a
// generic member's type reads at the instance's arguments rather than the declared
// parameters.
type classSubst struct {
	types     map[*soltype.TypeVarType]soltype.Type
	lifetimes map[*soltype.LifetimeVar]soltype.Lifetime
}

func newClassSubst(def *ClassDef, ct *soltype.ClassType) *classSubst {
	s := &classSubst{
		types:     map[*soltype.TypeVarType]soltype.Type{},
		lifetimes: map[*soltype.LifetimeVar]soltype.Lifetime{},
	}
	for i, tp := range def.TypeParams {
		if i < len(ct.TypeArgs) {
			s.types[tp.Var] = ct.TypeArgs[i]
		}
	}
	for i, lp := range def.LifetimeParams {
		if i < len(ct.LifetimeArgs) {
			s.lifetimes[lp.Var] = ct.LifetimeArgs[i]
		}
	}
	return s
}

func (s *classSubst) EnterType(t soltype.Type, _ soltype.Polarity) soltype.EnterResult {
	// A borrow's lifetime and a nested ClassType's lifetime arguments are a separate
	// sort Accept does not walk, so rewrite them here on the way down through the
	// shared lifetime-rewrite helpers and let Accept rebuild the type's children.
	switch t := t.(type) {
	case *soltype.RefType:
		return rewriteRefLifetime(t, s.lifetime(t.Lt))
	case *soltype.ClassType:
		return rewriteClassLifetimes(t, s.lifetime)
	case *soltype.TypeVarType:
		if rep, ok := s.types[t]; ok {
			return soltype.EnterResult{Type: rep, SkipChildren: true}
		}
	}
	return soltype.EnterResult{}
}

func (s *classSubst) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

func (s *classSubst) lifetime(lt soltype.Lifetime) soltype.Lifetime {
	lv, ok := lt.(*soltype.LifetimeVar)
	if !ok {
		return lt
	}
	if rep, ok := s.lifetimes[lv]; ok {
		return rep
	}
	return lt
}
