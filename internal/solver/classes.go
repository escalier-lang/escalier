package solver

import (
	"fmt"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// ClassDef is the heavy per-class data the nominal handle soltype.ClassType points
// at. inferClassDecl builds one per class declaration and registers it on the
// Context under the class's dep_graph-qualified name; member lookup reads the
// projected Body, and the nominal constrain rule (C1) reads Supers, Implements, and
// Variance.
// Keeping this data out of soltype.ClassType lets the handle stay a small, cheap-to-
// compare identity.
type ClassDef struct {
	// TypeParams are the class's own quantified type parameters in declaration order,
	// each carrying its constraint as its Var's upper bound and its resolved default.
	// nil for a non-generic class.
	TypeParams []*soltype.TypeParam

	// Arity is how many type arguments a reference may write, read off the declaration's
	// `<…>` clause when the class's identity is registered. TypeParams lands later, so a
	// reference resolved in between — a class body naming a sibling — still has a count.
	Arity typeParamArity

	// LifetimeParams are the class's quantified lifetime parameters (A3), the lifetime
	// twin of TypeParams. nil for a class that holds no borrowed data.
	LifetimeParams []*soltype.LifetimeParam

	// Variance records one entry per TypeParam as measured through an IMMUTABLE
	// reference to an instance, where every member is readable and none is writable. The
	// nominal constrain rule dispatches each argument position by it. B1 leaves every
	// entry Invariant, the conservative default; variance inference (C2) overwrites it.
	Variance []Variance

	// MutVariance is the same measurement taken through a MUTABLE reference, which also
	// admits a write to every non-`readonly` field. A parameter that reaches such a field
	// is Invariant here. Every other parameter carries the same entry it does in Variance.
	// The nominal constrain rule reads this vector when the constraint sits inside a
	// mutable borrow. It is nil until variance inference (C2) fills it, and a missing
	// entry falls back to Invariant.
	MutVariance []Variance

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

// varianceAt returns the variance measured for the i'th type parameter, read from
// MutVariance when the constraint sits inside a mutable borrow and from Variance
// otherwise. It yields Invariant whenever the chosen vector has no entry at i, which
// covers an unregistered class, a def whose vectors variance inference has not filled in
// yet, and an index past the parameter list. Invariant is the conservative default a sound
// constrain rule can always fall back to.
func (d *ClassDef) varianceAt(i int, mutCtx bool) Variance {
	if d == nil {
		return Invariant
	}
	vec := d.Variance
	if mutCtx {
		vec = d.MutVariance
	}
	if i >= len(vec) {
		return Invariant
	}
	return vec[i]
}

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
// variance. It returns the two vectors to store on ClassDef. immut is the immutable-view
// variance, stored as Variance, and mut the mutable-view variance, stored as MutVariance.
// The nominal constrain rule dispatches each argument position by whichever vector the
// constraint's reference mutability selects.
//
// A declared modifier is checked against the immutable view alone, the variance a reader
// of the class sees. It is checked, not trusted: a mismatch reports VarianceMismatchError
// and the measured variance is still stored, since soundness follows the body, not the
// annotation.
func (c *checker) inferVariance(def *ClassDef, decl *ast.ClassDecl) (immut, mut []Variance) {
	immut, mut = inferBodyVariance(def)
	for i, tp := range decl.TypeParams {
		if i >= len(immut) {
			break
		}
		declared, ok := modifierVariance(tp.Variance)
		if !ok {
			continue
		}
		// A phantom parameter imposes no constraint either way, so any modifier is sound
		// on it — accept the annotation and keep the measured Bivariant.
		if immut[i] == Bivariant {
			continue
		}
		if declared != immut[i] {
			c.report(&VarianceMismatchError{
				Name:     tp.Name,
				Declared: declared,
				Inferred: immut[i],
				Class:    decl.Name,
			})
		}
	}
	return immut, mut
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
//
// It returns two vectors, one per view a reference to an instance can offer. immut is the
// variance an immutable reference sees, where every member is readable and none is
// writable. mut is the variance a mutable reference sees, which additionally admits a
// write to every non-`readonly` field.
//
// The two vectors differ only at those writable fields. A field's read is an output
// position in both views, so a parameter reaching one is covariant in immut. Its write is
// an input position only mut has, so the same parameter is invariant in mut. Given `class
// Box<T> { value: T }`, immut measures T covariant while mut measures it invariant. That
// is what leaves `mut Box<number>` and `mut Box<number | string>` unrelated even though
// `Box<number> <: Box<number | string>` holds.
//
// Every other member position has one variance both views share, so it is walked once and
// folded into both. A method value parameter is contravariant whether or not the holder
// can mutate the instance, and a method return or getter is covariant either way. The same
// holds for a member only a mutable reference can reach, such as a setter or a `mut self`
// method. Reaching such a member is a call, and a call's argument and result carry the
// polarity its signature gives them regardless of what made the member reachable.
//
// Folding a mutability-gated member into immut as well is the conservative choice. An
// owned value can later be bound mutably, so a parameter that is inert only while the
// reference stays immutable is not safe to treat as a phantom.
func inferBodyVariance(def *ClassDef) (immut, mut []Variance) {
	immut = make([]Variance, len(def.TypeParams))
	mut = make([]Variance, len(def.TypeParams))
	if len(def.TypeParams) == 0 {
		return immut, mut
	}
	targets := make(map[*soltype.TypeVarType]int, len(def.TypeParams))
	for i, tp := range def.TypeParams {
		targets[tp.Var] = i
	}
	// shared records the occurrences both views agree on. write records the field-write
	// occurrences only a mutable reference adds.
	shared := newVarianceVisitor(targets, len(def.TypeParams))
	write := newVarianceVisitor(targets, len(def.TypeParams))
	if def.Body != nil {
		for _, elem := range def.Body.Elems {
			soltype.AcceptObjElem(stripSelfReceiver(elem), shared, soltype.Positive)
			if prop, ok := elem.(*soltype.PropertyElem); ok && !prop.Readonly {
				// Walking the same field again at Negative records the input position
				// `obj.f = …` occupies. A `readonly` field rejects that write, so it is
				// skipped here and contributes only its output position to both vectors.
				soltype.AcceptObjElem(prop, write, soltype.Negative)
			}
		}
	}
	// A parameter appearing in a `super` type argument is marked in both directions, so it
	// collapses to invariant — the sound conservative choice while inheritance variance is
	// not composed precisely. Walking each super once per polarity records both.
	for _, super := range def.Supers {
		super.Accept(shared, soltype.Positive)
		super.Accept(shared, soltype.Negative)
	}
	for i := range def.TypeParams {
		immut[i] = collapseVariance(shared.pos[i], shared.neg[i])
		mut[i] = collapseVariance(shared.pos[i] || write.pos[i], shared.neg[i] || write.neg[i])
	}
	return immut, mut
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
		return &soltype.GetterElem{Name: e.Name, Type: e.Type, Throws: e.Throws}
	case *soltype.SetterElem:
		return &soltype.SetterElem{Name: e.Name, Param: e.Param, Throws: e.Throws}
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

// newVarianceVisitor returns a visitor that records occurrences of the vars in targets,
// sized for n type parameters. The visitors measuring one class share a single targets
// map, since they index the same parameter list.
func newVarianceVisitor(targets map[*soltype.TypeVarType]int, n int) *varianceVisitor {
	return &varianceVisitor{targets: targets, pos: make([]bool, n), neg: make([]bool, n)}
}

func (v *varianceVisitor) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	// A `mut` borrow is a read-write window on its pointee, so a parameter reached
	// through one occupies an input position as well as an output position and comes out
	// invariant. RefType.Accept walks the pointee covariantly alone, so the write view is
	// recorded here through the helper the occurrence visitors share. This is what keeps
	// `readonly inner: mut Box<T>` from measuring T covariant: the field itself rejects
	// `h.inner = …`, but the borrow it holds still admits `h.inner.value = …`.
	if recordMutWriteView(v, t, pol) {
		return soltype.EnterResult{} // let Accept do the covariant read-view walk
	}
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

// projectClassBody returns the whole instance member view of a class instance: everything
// the class declares, followed by everything it inherits and does not shadow, each with the
// declaring class's type parameters replaced by the arguments that class is reached at. It
// returns ok=false when the class is unregistered so the caller can recover. A single member
// access goes through projectedClassMember instead, so it pays only for the member it reads.
//
// The projected body's Inexact flag follows the instance's Final: a final class is exact,
// its member set closed, while a non-final class is inexact, since a subclass may widen it
// (exact-types §2.6). The returned ObjectType is always a fresh wrapper so the shared
// registry Body keeps its own flag.
func (c *Context) projectClassBody(ct *soltype.ClassType) (*soltype.ObjectType, bool) {
	def, ok := c.classDef(ct.Name)
	if !ok || def.Body == nil {
		return nil, false
	}
	obj := c.withInherited(def, ct, &soltype.ObjectType{Elems: c.projectOwnElems(def, ct)})
	obj.Inexact = !ct.Final
	return obj, true
}

// withInherited returns own extended with the members ct inherits and does not shadow, or
// own itself when the chain contributes nothing. It is the shared tail of the two whole-body
// views, which differ only in what they hand it: projectClassBody passes the class's own
// members projected to an instance's arguments, and selfView passes them unprojected.
//
// The result aliases own whenever nothing is inherited, so a caller that goes on to set a
// field on it has to pass an object it owns.
func (c *Context) withInherited(def *ClassDef, ct *soltype.ClassType, own *soltype.ObjectType) *soltype.ObjectType {
	inherited := c.inheritedElems(def, ct)
	if len(inherited) == 0 {
		return own
	}
	elems := make([]soltype.ObjTypeElem, 0, len(own.Elems)+len(inherited))
	elems = append(elems, own.Elems...)
	elems = append(elems, inherited...)
	return &soltype.ObjectType{Elems: elems}
}

// projectOwnElems returns the members a class declares itself, projected to ct's arguments.
// A non-generic class needs no substitution, so the returned slice is then the registry's
// own. A caller that appends to the result has to copy it first.
func (c *Context) projectOwnElems(def *ClassDef, ct *soltype.ClassType) []soltype.ObjTypeElem {
	if len(def.TypeParams) == 0 && len(def.LifetimeParams) == 0 {
		return def.Body.Elems
	}
	projected := def.Body.Accept(newClassSubst(def, ct), soltype.Positive)
	obj, ok := projected.(*soltype.ObjectType)
	if !ok {
		// Substitution replaces only vars and lifetimes, so an ObjectType body always
		// projects to an ObjectType; a different kind means the substitution corrupted
		// the body. Fail loudly rather than return the unsubstituted body, matching the
		// AsProperty discipline.
		panic(fmt.Sprintf("projectOwnElems: %s projected to non-ObjectType %T", ct.Name, projected))
	}
	return obj.Elems
}

// inheritedElems returns the members an instance of ct carries through its `extends` chain
// and does not declare itself, each projected to the arguments its declaring class is
// reached at. A name ct declares shadows every same-named member above it, the same
// resolution projectedClassMember performs for a single access.
//
// Shadowing is by name rather than by member kind, so a subclass declaring only the getter
// of an inherited getter/setter pair shadows the setter too. That keeps this view agreeing
// with a single access, which stops at the first class carrying the name. Dropping a half
// the superclass view still reaches is what checkInheritedMembers reports.
func (c *Context) inheritedElems(def *ClassDef, ct *soltype.ClassType) []soltype.ObjTypeElem {
	if len(def.Supers) == 0 {
		return nil
	}
	taken := set.NewSet[string]()
	addElemNames(taken, def.Body)
	visited := set.NewSet[string]()
	visited.Add(ct.Name)
	return c.chainElems(def, ct, taken, visited)
}

// selfView returns the object `self` binds to inside a member or constructor body: the
// class's own members followed by the ones it inherits and does not shadow. A class with no
// superclass gets its own body back, so nothing is copied for the common case.
//
// The class's own members are shared by pointer, not projected. `self` is an instance at the
// class's own arguments, so a member naming `T` keeps `T` symbolic and resolves to the same
// variable the enclosing member sees. Sharing is also what lets a write such as `self.x = v`
// refine the very field variable the class body reads. An inherited member is projected
// instead, since the chain reaches it at whatever arguments the `extends` clause writes.
//
// Collecting the chain up front is what keeps this cheap. It runs twice per class
// declaration, once for the member bodies and once for the constructor, so a single walk
// covers every `self.x` in all of them. Walking per access would repeat it for each one, and
// a constructor alone usually touches most of the fields. The wider view does make each
// field lookup scan a few more elements, which costs less than repeating the walk.
func (c *Context) selfView(self *soltype.ClassType, body *soltype.ObjectType) *soltype.ObjectType {
	def, ok := c.classDef(self.Name)
	if !ok {
		return body
	}
	return c.withInherited(def, self, body)
}

// chainElems walks ct's superclass edges, collecting each member whose name taken does not
// already carry and marking that name taken for the classes further up. visited holds the
// class names already reached, bounding the walk on a cyclic hierarchy the way
// constrainNominalWalk does.
func (c *Context) chainElems(def *ClassDef, ct *soltype.ClassType, taken, visited set.Set[string]) []soltype.ObjTypeElem {
	var out []soltype.ObjTypeElem
	for _, superType := range def.Supers {
		super := substituteSuperArgs(def, ct, superType)
		if visited.Contains(super.Name) {
			continue
		}
		visited.Add(super.Name)
		superDef, ok := c.classDef(super.Name)
		if !ok || superDef.Body == nil {
			continue
		}
		for _, elem := range superDef.Body.Elems {
			if !taken.Contains(soltype.ObjElemName(elem)) {
				out = append(out, projectClassMember(superDef, super, elem))
			}
		}
		// Marked after the loop rather than inside it, so a class declaring both halves of an
		// accessor pair contributes both instead of shadowing its own second half.
		addElemNames(taken, superDef.Body)
		out = append(out, c.chainElems(superDef, super, taken, visited)...)
	}
	return out
}

// addElemNames adds the name of every member of obj to names.
func addElemNames(names set.Set[string], obj *soltype.ObjectType) {
	for _, elem := range obj.Elems {
		names.Add(soltype.ObjElemName(elem))
	}
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
// A (subName, supName) seen-set bounds the walk on a cyclic hierarchy.
//
// mutCtx is the deep-mut context flag the RefType gate sets, true when the constraint sits
// inside a mutable borrow. It selects which of the class's two variance vectors each
// argument is dispatched by, so a `mut` reference tightens only the parameters a write
// through that reference can reach rather than pinning every argument.
func (c *Context) constrainNominal(sub, super *soltype.ClassType, seen *seenPairs, mutCtx bool) []SolverError {
	return c.constrainNominalWalk(sub, super, seen, set.NewSet[classPair](), mutCtx)
}

func (c *Context) constrainNominalWalk(sub, super *soltype.ClassType, seen *seenPairs, walked set.Set[classPair], mutCtx bool) []SolverError {
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
			variance := def.varianceAt(i, mutCtx)
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
			// A candidate whose walk fails has its errors discarded so the next candidate can be
			// tried. It therefore walks over a clone of the seen-set, the discipline every arm
			// that swallows a failure follows. Nothing a discarded walk settled is read by a
			// later candidate, nor by the caller in the case where a later candidate accepts
			// and the walk reports no error.
			//
			// This walk is the one rejecting arm that opens no probe, so it restores
			// shallowestAssumed by hand where every other arm inherits the restore from
			// Probe.Discard.
			enclosingShallowest := c.shallowestAssumed
			if len(c.constrainNominalWalk(s, super, seen.Clone(), walked, mutCtx)) == 0 {
				// The accepting candidate is the derivation the caller keeps, so the goals it
				// closed assumptions on stay folded in.
				return nil
			}
			c.shallowestAssumed = enclosingShallowest // a rejected candidate informs nothing
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
	member, found := c.projectedClassMember(ct, name, (*soltype.ObjectType).ReadMember, set.NewSet[string]())
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

// objectMember resolves a read of a method, getter, or setter carried by a plain object type,
// the members an object type annotation declares. It is the structural twin of
// projectedMember, which does the same for a class instance.
//
// A PropertyElem deliberately does not resolve here, and neither does a miss. Both fall through
// to the structural `{name: fieldVar}` requirement in valueProp, which is where the
// read-after-write record, the borrow edges, the inexact tail, the union join, and
// MissingPropertyError already live. Only the member kinds that requirement cannot express are
// intercepted, so this adds a path rather than diverting one.
func (c *checker) objectMember(lvl int, blame ast.Node, name string, carrier soltype.Type) (pathResult, bool) {
	obj, ok := objectCarrier(carrier)
	if !ok {
		return pathResult{}, false
	}
	member, found := obj.ReadMember(name)
	if !found {
		return pathResult{}, false
	}
	if _, isProp := member.(*soltype.PropertyElem); isProp {
		return pathResult{}, false
	}
	return c.memberValue(lvl, blame, member), true
}

// objectCarrier reads the object type a receiver denotes: the type itself, or the single
// object among an unresolved var's lower bounds. It mirrors classCarrier and
// classValueCarrier, and like them it declines a var whose bounds disagree, since there is no
// one member list to read in that case.
func objectCarrier(t soltype.Type) (*soltype.ObjectType, bool) {
	switch t := t.(type) {
	case *soltype.ObjectType:
		return t, true
	case *soltype.TypeVarType:
		var found *soltype.ObjectType
		for _, lb := range t.LowerBounds {
			obj, ok := lb.(*soltype.ObjectType)
			if !ok {
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
//
// lookup selects which half of a getter/setter pair the access wants. A read passes
// ObjectType.ReadMember and a write passes ObjectType.WriteMember.
func (c *checker) projectedClassMember(ct *soltype.ClassType, name string, lookup memberLookup, visited set.Set[string]) (soltype.ObjTypeElem, bool) {
	def, ok := c.ctx.classDef(ct.Name)
	if !ok || def.Body == nil {
		return nil, false
	}
	// Member names are invariant under substitution, so look the member up on the
	// unprojected body and project only the one accessed, rather than rebuilding the
	// whole body per access.
	if member, found := lookup(def.Body, name); found {
		return projectClassMember(def, ct, member), true
	}
	if visited.Contains(ct.Name) {
		return nil, false
	}
	visited.Add(ct.Name)
	for _, superType := range def.Supers {
		superInstance := substituteSuperArgs(def, ct, superType)
		if member, found := c.projectedClassMember(superInstance, name, lookup, visited); found {
			return member, true
		}
	}
	return nil, false
}

// memberLookup selects one named member off a class body. The two implementations are
// ObjectType.ReadMember and ObjectType.WriteMember, which differ only in which half of a
// getter/setter pair they return.
type memberLookup func(*soltype.ObjectType, string) (soltype.ObjTypeElem, bool)

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
	member, found := obj.ReadMember(name)
	if !found {
		return pathResult{}, false
	}
	if _, isProp := member.(*soltype.PropertyElem); isProp {
		return pathResult{}, false
	}
	c.checkReceiverMut(blame, recv, memberSelfParam(member))
	return c.memberValue(lvl, blame, member), true
}

// projectClassMember rewrites one class-body member's type-parameter and
// lifetime-parameter vars to the arguments of one instance, the single-member analogue
// of projectClassBody. A non-generic class, whose body holds no such vars, returns the
// member unchanged. It runs the same typeSubst walk projectClassBody runs over the
// whole body, through the shared per-member entry point, so a member reads exactly as it
// would there.
func projectClassMember(def *ClassDef, ct *soltype.ClassType, member soltype.ObjTypeElem) soltype.ObjTypeElem {
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
		// Reading through a getter runs its body, so the read is an exceptional exit of
		// the enclosing body the way a call is. A method read is not. Reading `p.m` only
		// names the function, and its throws stays in the signature until it is called.
		c.raiseAccessorThrows(lvl, blame, m.ThrowsOrNever())
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

// writeAccessor resolves the accessor a field write `recv.prop = …` targets, across the
// same three receiver shapes valueProp intercepts for a read: a class instance, a class
// body reached through `self`, and a class value. The returned ok routes the write to
// inferAccessorAssign. Every other member kind returns ok=false, so a field write keeps the
// structural path, where the readonly check, the `written` record, and the borrow edges live.
func (c *checker) writeAccessor(name string, carrier soltype.Type) (soltype.ObjTypeElem, bool) {
	member, found := c.writeMember(name, carrier)
	if !found {
		return nil, false
	}
	// A setter is the member the write calls. A getter is routed here too, since it has no
	// setter to call and only inferAccessorAssign knows to report that. The structural path
	// matches a PropertyElem, finds none, and would blame a missing property instead.
	switch member.(type) {
	case *soltype.SetterElem, *soltype.GetterElem:
		return member, true
	}
	return nil, false
}

// writeMember looks name up on carrier's class instance, class body, or class value,
// whichever it resolves as, preferring the setter half of a getter/setter pair. It returns
// found=false for any other receiver.
func (c *checker) writeMember(name string, carrier soltype.Type) (soltype.ObjTypeElem, bool) {
	if ct, ok := classCarrier(carrier); ok {
		return c.projectedClassMember(ct, name, (*soltype.ObjectType).WriteMember, set.NewSet[string]())
	}
	if obj, ok := carrier.(*soltype.ObjectType); ok {
		return obj.WriteMember(name)
	}
	if obj, ok := classValueCarrier(carrier); ok {
		return obj.WriteMember(name)
	}
	return nil, false
}

// raiseAccessorThrows records that an accessor access raises `throws` into the enclosing
// body's throws sink, the same wiring inferCall gives a call. A body with no clause has a
// `never` sink, so a raising accessor is rejected at the access site rather than reading as
// non-throwing to the caller. A `never` throws is skipped outright, leaving the enclosing
// clause counting as unused; anything else, an unsolved variable included, marks the body
// as raising, matching how inferCall treats an unresolved callee.
func (c *checker) raiseAccessorThrows(lvl int, blame ast.Node, throws soltype.Type) {
	if isNeverType(throws) {
		return
	}
	c.markRaised()
	c.constrain(blame, throws, c.throwsSink(lvl))
}

// raiseUnionAccessorThrows records what reading `name` off a union receiver may raise.
// constrainUnionFieldRead joins the read's value across the union's members from inside
// constrain, which holds no throws sink, so this walks the same members to reach the getters
// that join reads through. Only a getter contributes; a property, method, or setter raises
// nothing on a read. It collects before it raises because the join abandons the whole read
// once any member carries no readable object, as `A | undefined` does, and then no getter runs.
func (c *checker) raiseUnionAccessorThrows(lvl int, blame ast.Node, name string, carrier soltype.Type) {
	u, isUnion := carrier.(*soltype.UnionType)
	if !isUnion {
		return
	}
	var throws []soltype.Type
	for _, member := range u.Types {
		obj, ok := c.ctx.readCarrierObject(soltype.CarrierOf(member))
		if !ok {
			return
		}
		elem, found := obj.ReadMember(name)
		if !found {
			continue
		}
		if getter, isGetter := elem.(*soltype.GetterElem); isGetter {
			throws = append(throws, getter.ThrowsOrNever())
		}
	}
	for _, t := range throws {
		c.raiseAccessorThrows(lvl, blame, t)
	}
}

// strippedMethodSig returns a method signature as a plain callable, its SelfParam
// dropped, since `p.m` binds the receiver and returns a function of the remaining
// parameters. The receiver's own ownership is checked separately at member access as a
// `receiver <: SelfParam` constraint.
func strippedMethodSig(sig *soltype.FuncType) *soltype.FuncType {
	return &soltype.FuncType{
		Params:         sig.Params,
		Ret:            sig.Ret,
		Throws:         sig.Throws,
		Inexact:        sig.Inexact,
		TypeParams:     sig.TypeParams,
		LifetimeParams: sig.LifetimeParams,
	}
}

// checkReceiverMut rejects a `mut self` member reached through a receiver that holds only
// a shared borrow to lend. It constrains the accessing receiver recv against the accessed
// member's own declared `self`, which the caller passes as self. The four pairings:
//
//   - plain `self` receiver → `mut self` member: rejected, a shared borrow has no mut to lend
//   - `mut self` receiver   → `mut self` member: ok
//   - `mut self` receiver   → plain `self` member: ok, mutable downgrades to shared
//   - plain `self` receiver → plain `self` member: ok
//
// recv is the un-stripped receiver, so it still carries the mutability the access has to
// lend. The receiver is rebuilt as `Self` in that mutability, so the diagnostic reads
// `immutable C <: mutable C`. A nil self, which a static member and a property both have,
// is a no-op, as is a receiver that is not a class instance.
func (c *checker) checkReceiverMut(blame ast.Node, recv soltype.Type, self *soltype.FuncParam) {
	if self == nil {
		return
	}
	inner := receiverClass(self.Type)
	if inner == nil {
		return
	}
	recvT := soltype.Type(inner)
	if lendsMut(recv) {
		recvT = soltype.NewRef(true, nil, inner)
	}
	c.constrain(blame, recvT, self.Type)
}

// lendsMut reports whether recv has mutable access to lend: a `mut` borrow directly, or a
// binding var whose lower bounds are all `mut` borrows. The look-through matches
// classCarrier's, since a `mut` borrow that reaches a receiver position through a call
// result or a branch join arrives as a variable with the borrow among its lower bounds
// rather than as a bare RefType. `g(c).x = 5`, where `g` returns `mut C`, is one such
// receiver.
//
// EVERY lower bound must be mutable, since each is a value the receiver may actually hold
// at run time. A join of `mut C` and `C` lends no mutable access, because the branch taken
// may be the immutable one. Reporting mutable off a single bound would accept a `mut self`
// setter write the structural field-write path rejects on the same receiver.
func lendsMut(recv soltype.Type) bool {
	switch recv := recv.(type) {
	case *soltype.RefType:
		return recv.Mut
	case *soltype.TypeVarType:
		sawBound := false
		for _, lb := range recv.LowerBounds {
			if lb == soltype.Type(recv) {
				// A vacuous `v <: v` self-edge constrains nothing, the same edge readCarrier
				// drops. It names no value the receiver holds, so it neither grants nor
				// withholds mutable access.
				continue
			}
			r, ok := lb.(*soltype.RefType)
			if !ok || !r.Mut {
				return false
			}
			sawBound = true
		}
		return sawBound
	}
	return false
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
	member, found := obj.ReadMember(name)
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

// typeSubst rewrites a generic body's type-parameter and lifetime-parameter vars to
// the arguments of one instance. It maps each TypeParam.Var to the instance's
// positional TypeArg and each LifetimeParam.Var to its positional LifetimeArg, so a
// generic member or alias body reads at the instance's arguments rather than the declared
// parameters.
type typeSubst struct {
	types     map[*soltype.TypeVarType]soltype.Type
	lifetimes map[*soltype.LifetimeVar]soltype.Lifetime
}

// newTypeSubst maps each type parameter's var to the positional type argument and each
// lifetime parameter's var to its positional lifetime argument. A class instance and an
// expanded generic alias both build their substitution through it.
func newTypeSubst(typeParams []*soltype.TypeParam, typeArgs []soltype.Type, lifetimeParams []*soltype.LifetimeParam, lifetimeArgs []soltype.Lifetime) *typeSubst {
	s := &typeSubst{
		types:     map[*soltype.TypeVarType]soltype.Type{},
		lifetimes: map[*soltype.LifetimeVar]soltype.Lifetime{},
	}
	for i, tp := range typeParams {
		if i < len(typeArgs) {
			s.types[tp.Var] = typeArgs[i]
		}
	}
	for i, lp := range lifetimeParams {
		if i < len(lifetimeArgs) {
			s.lifetimes[lp.Var] = lifetimeArgs[i]
		}
	}
	return s
}

// apply rewrites t through s, returning t unchanged when s is nil. A nil substitution is
// what a caller with nothing to rewrite passes, so the call site stays a plain expression
// rather than a branch.
func (s *typeSubst) apply(t soltype.Type) soltype.Type {
	if s == nil {
		return t
	}
	return t.Accept(s, soltype.Positive)
}

// newClassSubst builds the substitution for one class instance. ct is that instance's
// type, such as Box<5>, so its TypeArgs and LifetimeArgs are the concrete arguments each
// of def's parameter vars maps to.
func newClassSubst(def *ClassDef, ct *soltype.ClassType) *typeSubst {
	return newTypeSubst(def.TypeParams, ct.TypeArgs, def.LifetimeParams, ct.LifetimeArgs)
}

func (s *typeSubst) EnterType(t soltype.Type, _ soltype.Polarity) soltype.EnterResult {
	// A borrow's lifetime and a nested ClassType's or AliasType's lifetime arguments are a
	// separate sort Accept does not walk, so rewrite them here on the way down through the
	// shared lifetime-rewrite helpers and let Accept rebuild the type's children.
	switch t := t.(type) {
	case *soltype.RefType:
		return rewriteRefLifetime(t, s.lifetime(t.Lt))
	case *soltype.ClassType:
		return rewriteClassLifetimes(t, s.lifetime)
	case *soltype.AliasType:
		return rewriteAliasLifetimes(t, s.lifetime)
	case *soltype.TypeVarType:
		if rep, ok := s.types[t]; ok {
			return soltype.EnterResult{Type: rep, SkipChildren: true}
		}
	}
	return soltype.EnterResult{}
}

func (s *typeSubst) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

func (s *typeSubst) lifetime(lt soltype.Lifetime) soltype.Lifetime {
	lv, ok := lt.(*soltype.LifetimeVar)
	if !ok {
		return lt
	}
	if rep, ok := s.lifetimes[lv]; ok {
		return rep
	}
	return lt
}
