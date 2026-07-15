package solver

import (
	"fmt"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// ClassDef is the heavy per-class data the nominal token soltype.ClassType points
// at. inferClassDecl builds one per class declaration and registers it on the
// Context under the class's dep_graph-qualified name; member lookup reads the
// projected Body, and the nominal constrain rule (C1) reads Supers, Implements, and
// Variance.
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

	// Supers holds the resolved `extends` superclass — the declared nominal
	// subtype-graph edge. A class has at most one, so this holds zero or one element.
	// The rule that walks it transitively is C1; B1 only records it.
	Supers []*soltype.ClassType

	// Implements holds each resolved `implements` interface. `implements` is a
	// conformance-only assertion, so these are kept out of Supers: the nominal subtype
	// walk skips them and the structural conformance check reads them. Both the check
	// and the walk land in C1; B1 only records the targets.
	Implements []*soltype.ClassType

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

func (v Variance) String() string {
	switch v {
	case Covariant:
		return "covariant"
	case Contravariant:
		return "contravariant"
	case Bivariant:
		return "bivariant"
	default:
		return "invariant"
	}
}

// inferVariance measures each class type parameter's variance from how it occurs in the
// class body, then checks any declared `in`/`out`/`in out` modifier against the measured
// variance. It returns the measured variance to store on ClassDef.Variance, which the
// nominal constrain rule dispatches each argument position by. A declared modifier is
// checked, not trusted: a mismatch reports VarianceMismatchError and the measured
// variance is still stored, since soundness follows the body, not the annotation.
func (c *checker) inferVariance(def *ClassDef, decl *ast.ClassDecl) []Variance {
	variance := inferBodyVariance(def)
	for i, tp := range decl.TypeParams {
		if i >= len(variance) {
			break
		}
		declared, ok := modifierVariance(tp.Variance)
		if !ok {
			continue
		}
		// A phantom parameter imposes no constraint either way, so any modifier is sound
		// on it — accept the annotation and keep the measured Bivariant.
		if variance[i] == Bivariant {
			continue
		}
		if declared != variance[i] {
			c.report(&VarianceMismatchError{
				Name:     tp.Name,
				Declared: declared,
				Inferred: variance[i],
				Class:    decl.Name,
			})
		}
	}
	return variance
}

// modifierVariance maps a declared variance modifier to the Variance it asserts, or
// ok=false when no modifier was written. `out` is covariant, `in` contravariant, and
// `in out` invariant; there is no keyword for bivariant, so a phantom parameter is only
// ever left unannotated.
func modifierVariance(m ast.VarianceModifier) (Variance, bool) {
	switch m {
	case ast.VarianceOut:
		return Covariant, true
	case ast.VarianceIn:
		return Contravariant, true
	case ast.VarianceInOut:
		return Invariant, true
	default:
		return Invariant, false
	}
}

// inferBodyVariance computes each class type parameter's variance from the polarities its
// var occurs at in the class body, following algebraic subtyping's polarity threading:
// a parameter seen only in output positions is covariant, only in input positions
// contravariant, in both invariant, and in neither bivariant — a phantom parameter. The
// receiver `self` is excluded, since every method names the class in its receiver and
// counting that would force every parameter invariant. A parameter that reaches a `super`
// type argument is conservatively marked invariant, matching C1's pre-inference default:
// the polarity visitor treats a nested class's arguments covariantly regardless of that
// class's own variance, so precise variance through inheritance is deferred rather than
// measured unsoundly.
func inferBodyVariance(def *ClassDef) []Variance {
	variance := make([]Variance, len(def.TypeParams))
	if len(def.TypeParams) == 0 {
		return variance
	}
	v := &varianceVisitor{
		targets: make(map[*soltype.TypeVarType]int, len(def.TypeParams)),
		pos:     make([]bool, len(def.TypeParams)),
		neg:     make([]bool, len(def.TypeParams)),
	}
	for i, tp := range def.TypeParams {
		v.targets[tp.Var] = i
	}
	if def.Body != nil {
		for _, elem := range def.Body.Elems {
			soltype.AcceptObjElem(stripSelfReceiver(elem), v, soltype.Positive)
		}
	}
	// A parameter appearing in a `super` type argument is marked in both directions, so it
	// collapses to invariant — the sound conservative choice while inheritance variance is
	// not composed precisely. Walking each super once per polarity records both.
	for _, super := range def.Supers {
		super.Accept(v, soltype.Positive)
		super.Accept(v, soltype.Negative)
	}
	for i := range def.TypeParams {
		variance[i] = collapseVariance(v.pos[i], v.neg[i])
	}
	return variance
}

// collapseVariance turns a parameter's observed occurrence polarities into its variance:
// output-only is covariant, input-only contravariant, both invariant, neither bivariant.
func collapseVariance(pos, neg bool) Variance {
	switch {
	case pos && neg:
		return Invariant
	case pos:
		return Covariant
	case neg:
		return Contravariant
	default:
		return Bivariant
	}
}

// stripSelfReceiver returns a copy of a class-body member with its `self` receiver
// removed, so the variance walk does not count the receiver — a method's receiver names
// the class at its own type parameters, which would force every parameter invariant. A
// property carries no receiver and is returned unchanged.
func stripSelfReceiver(elem soltype.ObjTypeElem) soltype.ObjTypeElem {
	switch e := elem.(type) {
	case *soltype.MethodElem:
		sigs := make([]*soltype.FuncType, len(e.Signatures))
		for i, sig := range e.Signatures {
			bare := *sig
			bare.SelfParam = nil
			sigs[i] = &bare
		}
		return &soltype.MethodElem{Name: e.Name, Signatures: sigs, Static: e.Static}
	case *soltype.GetterElem:
		return &soltype.GetterElem{Name: e.Name, Type: e.Type}
	case *soltype.SetterElem:
		return &soltype.SetterElem{Name: e.Name, Param: e.Param}
	default:
		return elem
	}
}

// varianceVisitor is a read-only polarity-threading visitor that records, for a set of
// target type-parameter vars, the polarities each occurs at. It rewrites nothing — the
// polarity Accept threads is exactly the variance a parameter's occurrence contributes.
type varianceVisitor struct {
	targets map[*soltype.TypeVarType]int
	pos     []bool
	neg     []bool
}

func (v *varianceVisitor) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	if tv, ok := t.(*soltype.TypeVarType); ok {
		if i, found := v.targets[tv]; found {
			if pol == soltype.Positive {
				v.pos[i] = true
			} else {
				v.neg[i] = true
			}
		}
	}
	return soltype.EnterResult{}
}

func (v *varianceVisitor) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

// projectClassBody returns the instance member view of a class instance: the
// registered Body with each class type parameter replaced by the instance's
// corresponding type argument and each lifetime parameter by its lifetime argument.
// It returns ok=false when the class is unregistered so the caller can recover. The
// class-vs-object constrain rule reads the whole projected body; a single member access
// projects just that member through projectClassMember, so it pays only for the member
// it reads rather than rebuilding every member.
//
// The projected body's Inexact flag follows the instance's Final: a final class is
// exact, its member set closed, while a non-final class is inexact, since a subclass may
// widen it (exact-types §2.6). The returned ObjectType is always a fresh wrapper so the
// shared registry Body keeps its own flag.
func (c *Context) projectClassBody(ct *soltype.ClassType) (*soltype.ObjectType, bool) {
	def, ok := c.classDef(ct.Name)
	if !ok || def.Body == nil {
		return nil, false
	}
	if len(def.TypeParams) == 0 && len(def.LifetimeParams) == 0 {
		return &soltype.ObjectType{Elems: def.Body.Elems, Inexact: !ct.Final}, true
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
	// Accept returns def.Body's own ObjectType when the body holds none of the
	// substituted vars, so setting Inexact on obj directly would mutate the shared
	// registry Body. Wrap the projected elements in a fresh ObjectType and set exactness
	// on the copy, matching the non-generic path above.
	return &soltype.ObjectType{Elems: obj.Elems, Inexact: !ct.Final}, true
}

// classPair keys the nominal subtype walk's seen-set by the (sub, super) class NAMES,
// so a cyclic extends hierarchy terminates: the same name pair is never re-walked.
// This is coarser than constrain's type-keyed seen-set on purpose — the walk decides a
// relationship between nominal identities, and two instances of one class at different
// arguments share the identity the walk cares about.
type classPair struct{ sub, super string }

// constrainNominal decides sub <: super between two class instances. It succeeds when
// they name the same class, checking each type argument by the class's per-position
// variance, or when sub reaches super transitively through the declared extends graph.
// A (subName, supName) seen-set bounds the walk on a cyclic hierarchy. Until C2 infers
// real variance, every ClassDef.Variance entry is Invariant, so every argument is
// constrained in both directions — the conservative choice a sound rule falls back to.
func (c *Context) constrainNominal(sub, super *soltype.ClassType, seen set.Set[constraintKey]) []SolverError {
	return c.constrainNominalWalk(sub, super, seen, set.NewSet[classPair]())
}

func (c *Context) constrainNominalWalk(sub, super *soltype.ClassType, seen set.Set[constraintKey], walked set.Set[classPair]) []SolverError {
	key := classPair{sub.Name, super.Name}
	if walked.Contains(key) {
		return []SolverError{&CannotConstrainError{Sub: sub, Super: super}}
	}
	walked.Add(key)

	if sub.Name == super.Name {
		def, _ := c.classDef(sub.Name)
		var errs []SolverError
		n := min(len(sub.TypeArgs), len(super.TypeArgs))
		for i := range n {
			variance := Invariant
			if def != nil && i < len(def.Variance) {
				variance = def.Variance[i]
			}
			argSub, argSup := sub.TypeArgs[i], super.TypeArgs[i]
			switch variance {
			case Covariant:
				errs = append(errs, c.constrain(argSub, argSup, seen, false)...)
			case Contravariant:
				errs = append(errs, c.constrain(argSup, argSub, seen, false)...)
			case Bivariant:
				// A phantom parameter appears nowhere in the body, so its argument imposes
				// no constraint.
			default: // Invariant
				errs = append(errs, c.constrain(argSub, argSup, seen, false)...)
				errs = append(errs, c.constrain(argSup, argSub, seen, false)...)
			}
		}
		return errs
	}

	// Different names: sub <: super holds when any direct super of sub reaches super.
	// Substitute sub's arguments into each superclass type so a generic base is checked
	// at the instance's arguments, e.g. B<5> declared `extends A<T>` walks A<5>.
	if def, ok := c.classDef(sub.Name); ok {
		for _, superType := range def.Supers {
			s := substituteSuperArgs(def, sub, superType)
			if len(c.constrainNominalWalk(s, super, seen, walked)) == 0 {
				return nil
			}
		}
	}
	return []SolverError{&CannotConstrainError{Sub: sub, Super: super}}
}

// substituteSuperArgs rewrites a superclass type's references to sub's class type
// parameters to sub's actual arguments, so `class B<T> extends A<T>` checked at B<5>
// yields A<5>. A non-generic sub, whose superclass type holds no parameter vars, returns
// the superclass type unchanged.
func substituteSuperArgs(def *ClassDef, sub, superType *soltype.ClassType) *soltype.ClassType {
	if len(def.TypeParams) == 0 && len(def.LifetimeParams) == 0 {
		return superType
	}
	if ct, ok := superType.Accept(newClassSubst(def, sub), soltype.Positive).(*soltype.ClassType); ok {
		return ct
	}
	return superType
}

// projectedMember resolves a member access against a class instance by looking the
// member up on the class body — walking the declared `extends` chain for a member the
// class inherits rather than declares — and projecting just that member to the instance's
// arguments. It returns ok=false when the receiver is not a class instance — a plain
// object property, or a type variable — so the caller falls back to the structural
// field-requirement path. A class instance whose class and none of its ancestors declare
// the member reports the miss here.
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
	def, ok := c.ctx.classDef(ct.Name)
	if !ok || def.Body == nil {
		return pathResult{}, false
	}
	member, found := c.projectedClassMember(ct, name, set.NewSet[string]())
	if !found {
		// The miss is rare, so project the whole body here to render the diagnostic at
		// the instance's arguments rather than the declared type parameters.
		obj, _ := c.ctx.projectClassBody(ct)
		err := &MissingPropertyError{Sub: obj, Super: propReq(name, &soltype.UnknownType{}, false), Name: name}
		err.prov, err.site = c.prov, blame
		c.errs = append(c.errs, err)
		return pathResult{value: &soltype.ErrorType{}}, true
	}
	return c.memberValue(lvl, blame, member), true
}

// projectedClassMember looks name up on ct's class body, then walks the declared
// `extends` chain when the class does not declare the member itself, so a member
// inherited from a superclass reads through a subclass instance. It returns the member
// projected to ct's arguments, or found=false when neither the class nor any ancestor
// declares it.
//
// Each superclass edge is first re-expressed at ct's arguments through
// substituteSuperArgs before the walk recurses into it, so `class Dog<T> extends
// Animal<T>` accessed at Dog<string> walks Animal<string>, and an inherited member typed
// `T` projects to `string`. visited holds the class names already on the current chain,
// bounding the walk on a cyclic hierarchy the same way constrainNominalWalk does.
func (c *checker) projectedClassMember(ct *soltype.ClassType, name string, visited set.Set[string]) (soltype.ObjTypeElem, bool) {
	def, ok := c.ctx.classDef(ct.Name)
	if !ok || def.Body == nil {
		return nil, false
	}
	// Member names are invariant under substitution, so look the member up on the
	// unprojected body and project only the one accessed, rather than rebuilding the
	// whole body per access.
	if member, found := def.Body.Member(name); found {
		return c.projectClassMember(def, ct, member), true
	}
	if visited.Contains(ct.Name) {
		return nil, false
	}
	visited.Add(ct.Name)
	for _, superType := range def.Supers {
		superInstance := substituteSuperArgs(def, ct, superType)
		if member, found := c.projectedClassMember(superInstance, name, visited); found {
			return member, true
		}
	}
	return nil, false
}

// classBodyMember resolves a method, getter, or setter read off a class-body ObjectType —
// the object `self` binds to inside a method or constructor body (M5 B3). It returns
// ok=false for a property read, so a field keeps the structural field-requirement path
// that threads the borrow and read-after-write rules a direct lookup would drop, and for a
// non-object receiver or a missing member, so an unknown member reports through that
// path's MissingPropertyError. Only a method, getter, or setter member — which only a
// class body carries — is intercepted, since the structural object arm reads only
// properties and panics on those kinds.
//
// Unlike projectedMember, this deliberately does NOT project the class's type parameters.
// `self` is an instance at the class's OWN arguments — the class-parameter vars themselves —
// so a member referencing `T` keeps `T` symbolic, and it is the same shared var the calling
// method resolves `T` to, since both members were walked in one class scope. Substituting,
// the way external access does for a concrete receiver like `Box<5>`, would be wrong here.
//
// A method whose return flows from a class type parameter — such as `read(self) { self.v }`
// on `class Box<T>` — resolves to that parameter because freezeClassBody coalesces the
// generic body while keeping the class's own type-parameter vars symbolic (B8), so `read`'s
// stored return reads as `T` rather than collapsing to `never`. A self call keeps `T` symbolic
// and an external call substitutes the instance's argument.
//
// Per-method type parameters — a method carrying its own `FuncType.TypeParams`, freshened per
// call by wrapping the resolved method in a scheme — remain future work: their inference
// depends on the generic-function machinery outside this milestone, so no method carries them
// yet and memberValue passes the field through unchanged.
func (c *checker) classBodyMember(lvl int, blame ast.Node, name string, recv, carrier soltype.Type) (pathResult, bool) {
	obj, ok := carrier.(*soltype.ObjectType)
	if !ok {
		return pathResult{}, false
	}
	member, found := obj.Member(name)
	if !found {
		return pathResult{}, false
	}
	if _, isProp := member.(*soltype.PropertyElem); isProp {
		return pathResult{}, false
	}
	c.checkMethodReceiver(blame, recv, member)
	return c.memberValue(lvl, blame, member), true
}

// projectClassMember rewrites one class-body member's type-parameter and
// lifetime-parameter vars to the arguments of one instance, the single-member analogue
// of projectClassBody. A non-generic class, whose body holds no such vars, returns the
// member unchanged. It runs the same classSubst walk projectClassBody runs over the
// whole body, through the shared per-member entry point, so a member reads exactly as it
// would there.
func (c *checker) projectClassMember(def *ClassDef, ct *soltype.ClassType, member soltype.ObjTypeElem) soltype.ObjTypeElem {
	if len(def.TypeParams) == 0 && len(def.LifetimeParams) == 0 {
		return member
	}
	return soltype.AcceptObjElem(member, newClassSubst(def, ct), soltype.Positive)
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
// access and is reported.
//
// An overloaded method carries more than one signature in its MethodElem. Its member value
// is the IntersectionType of those arms, each with its SelfParam stripped. A direct call
// `p.m(args)` resolves one arm through resolveOverload at the call site in inferCall, and a
// read of an overloaded method as a value carries the intersection the way a let-bound
// overloaded function does.
func (c *checker) memberValue(lvl int, blame ast.Node, member soltype.ObjTypeElem) pathResult {
	var out soltype.Type
	switch m := member.(type) {
	case *soltype.PropertyElem:
		out = m.Type
	case *soltype.GetterElem:
		out = m.Type
	case *soltype.MethodElem:
		switch len(m.Signatures) {
		case 0:
			out = &soltype.ErrorType{}
		case 1:
			out = strippedMethodSig(m.Signatures[0])
		default:
			arms := make([]soltype.Type, len(m.Signatures))
			for i, sig := range m.Signatures {
				arms[i] = strippedMethodSig(sig)
			}
			out = &soltype.IntersectionType{Types: arms}
		}
	case *soltype.SetterElem:
		out = c.report(&WriteOnlyPropertyError{Name: m.Name, Site: blame})
	default:
		out = &soltype.ErrorType{}
	}
	c.recordType(blame, out)
	return pathResult{value: out}
}

// strippedMethodSig returns a method signature as a plain callable, its SelfParam
// dropped, since `p.m` binds the receiver and returns a function of the remaining
// parameters. The receiver's own ownership is checked separately at member access as a
// `receiver <: SelfParam` constraint.
func strippedMethodSig(sig *soltype.FuncType) *soltype.FuncType {
	return &soltype.FuncType{
		Params:         sig.Params,
		Ret:            sig.Ret,
		Inexact:        sig.Inexact,
		TypeParams:     sig.TypeParams,
		LifetimeParams: sig.LifetimeParams,
	}
}

// checkMethodReceiver rejects a `mut self` member reached from a plain-`self` body, which
// holds only a shared borrow to lend. It fires only for inside-the-body access, where recv
// (the un-stripped `self` binding) carries the caller's mutability; an external call's
// mutability is a place property this check does not see. The receiver is rebuilt as the
// member's own `Self` carried in that mutability so the diagnostic names the class on both
// sides. A no-op for a static member, a property, or a non-class receiver.
func (c *checker) checkMethodReceiver(blame ast.Node, recv soltype.Type, member soltype.ObjTypeElem) {
	self := memberSelfParam(member)
	if self == nil {
		return
	}
	inner := receiverClass(self.Type)
	if inner == nil {
		return
	}
	recvT := soltype.Type(inner)
	if r, ok := recv.(*soltype.RefType); ok && r.Mut {
		recvT = soltype.NewRef(true, nil, inner)
	}
	c.constrain(blame, recvT, self.Type)
}

// memberSelfParam returns the `self` receiver of a readable member, a method or getter, or
// nil otherwise. A method reads its first arm's receiver, representative because
// buildMemberSigs rejects arms that disagree on receiver mutability. A setter is excluded,
// since reading one is already a write-only error.
func memberSelfParam(member soltype.ObjTypeElem) *soltype.FuncParam {
	switch m := member.(type) {
	case *soltype.MethodElem:
		if len(m.Signatures) > 0 {
			return m.Signatures[0].SelfParam
		}
	case *soltype.GetterElem:
		return m.SelfParam
	}
	return nil
}

// receiverClass returns the class instance a `self` receiver type names — the ClassType
// directly for a plain `self`, or the ClassType inside the borrow for a `mut self` / `&self`
// receiver. It returns nil when the receiver is not a class instance.
func receiverClass(t soltype.Type) soltype.RefInner {
	switch t := t.(type) {
	case *soltype.ClassType:
		return t
	case *soltype.RefType:
		if ct, ok := t.Inner.(*soltype.ClassType); ok {
			return ct
		}
	}
	return nil
}

// classValueMember resolves a static member read off a class value, such as
// `Point.origin`, by looking the member up on the value object and producing its type via
// memberValue. It returns ok=false when the receiver is not a class value or carries no
// such member, leaving both cases to the structural field-requirement path.
func (c *checker) classValueMember(lvl int, blame ast.Node, name string, carrier soltype.Type) (pathResult, bool) {
	obj, ok := classValueCarrier(carrier)
	if !ok {
		return pathResult{}, false
	}
	member, found := obj.Member(name)
	if !found {
		return pathResult{}, false
	}
	return c.memberValue(lvl, blame, member), true
}

// classValueCarrier resolves a receiver to the class-value object it reads as: an object
// carrying a ConstructorElem directly, or a binding var whose lower bounds carry one, the
// same look-through classCarrier uses for an instance. A var with two different class-value
// lower bounds is ambiguous and left to the structural path.
func classValueCarrier(t soltype.Type) (*soltype.ObjectType, bool) {
	switch t := t.(type) {
	case *soltype.ObjectType:
		if _, ok := t.Constructor(); ok {
			return t, true
		}
	case *soltype.TypeVarType:
		var found *soltype.ObjectType
		for _, lb := range t.LowerBounds {
			obj, ok := lb.(*soltype.ObjectType)
			if !ok {
				continue
			}
			if _, hasCtor := obj.Constructor(); !hasCtor {
				continue
			}
			if found != nil && !equalType(found, obj) {
				return nil, false
			}
			found = obj
		}
		if found != nil {
			return found, true
		}
	}
	return nil, false
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

// newClassSubst builds the substitution for one class instance. ct is that instance's
// type, such as Box<5>, so its TypeArgs and LifetimeArgs are the concrete arguments each
// of def's parameter vars maps to.
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
