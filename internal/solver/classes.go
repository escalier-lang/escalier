package solver

import (
	"fmt"
	"maps"
	"slices"

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

	// CovariantInputs has one entry per TypeParam. An entry is true when a method with an
	// immutable receiver takes the parameter as input and Variance still measures it
	// covariant. Through that covariance an instance can be read at a wider argument, so the
	// override check also reads such a method at the widest instance widestInstance returns.
	// It is nil until variance inference fills it, and a missing entry reads as false.
	CovariantInputs []bool

	// StorageVariance is the immutable-view variance measured from the class's own fields
	// and the members an immutable reference reaches other than its methods, before a
	// parameter reaching an `extends` argument is marked invariant. A parameter it measures
	// Covariant or Bivariant is one the class holds no consumer of that a method with an
	// immutable receiver can reach. It is nil until variance inference fills it.
	StorageVariance []Variance

	// varianceMeasured is set once the class body is inferred and its variance measured, even
	// when that measurement is provisional.
	varianceMeasured bool

	// varianceProvisional marks the variance vectors as a stand-in. The measurement read a
	// class whose own variance was not final yet, so every entry is Invariant and
	// CovariantInputs is all false. settleVariance measures again.
	varianceProvisional bool

	// varianceSelf is the class's qualified name and varianceDecl its declaration, which
	// measureVariance reads each time it measures.
	varianceSelf string
	varianceDecl *ast.ClassDecl

	// varianceTriedAt is the Context's measuredClasses count when the class was last
	// measured, so settleVariance measures again only once another class has settled.
	varianceTriedAt int

	// varianceSettling is set while settleVariance measures the class, so a class reached
	// again through its own measurement reads as not settled.
	varianceSettling bool

	// varianceMismatches holds the declared modifiers the latest final measurement found
	// looser than the body.
	varianceMismatches []*VarianceMismatchError

	// varianceWaitsOn names the classes a provisional measurement read whose variance was
	// not settled. settleVariance follows it to find a group of classes that wait on each
	// other.
	varianceWaitsOn []string

	// varianceUnsettled marks a class in a group whose measurement never stopped changing.
	// It keeps its provisional Invariant vectors and is not measured again.
	varianceUnsettled bool

	// HasSelf marks a body carrying a `Self` in some member signature, so the substitution
	// that resolves `Self` at the receiver's class runs only for a class that wrote one. It is
	// set when the body is registered and read by projectSelf.
	HasSelf bool

	// Supers holds the resolved `extends` superclass — the declared nominal
	// subtype-graph edge. A class has at most one, so this holds zero or one element.
	// The rule that walks it transitively is C1; B1 only records it.
	Supers []*soltype.ClassType

	// Implements holds each resolved `implements` interface. `implements` is a
	// conformance-only assertion, so these are kept out of Supers: the nominal subtype
	// walk skips them and the structural conformance check reads them. Both the check
	// and the walk land in C1; B1 only records the targets.
	Implements []*soltype.ClassType

	// EdgesPending marks a def the SCC pre-pass registered as a bare identity, before the
	// declaration's `extends` and `implements` clauses resolved. Supers and Implements are
	// empty on such a def for want of reading, not for want of a declaration, so a reader
	// concluding a class has no superclass must check this first. inferClassDecl clears it.
	EdgesPending bool

	// Body is the instance member view a class projects: one element per field,
	// method, getter, and setter. Member access and the class-vs-object constrain
	// rule read it.
	Body *soltype.ObjectType

	// Impls holds the implementation of each instance method whose overload set has one,
	// as ast.ImplementationArm finds it. Each MethodElem carries the one implementation
	// signature. Body holds only the bodiless signatures a caller sees, so the
	// implementation is reached only by the checks that read the method the runtime runs.
	// It is nil for a class with no such method.
	Impls *soltype.ObjectType

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

// measuredVariance holds what variance inference measures for one class, one entry per type
// parameter in each vector.
type measuredVariance struct {
	// immut is stored as ClassDef.Variance and mut as ClassDef.MutVariance.
	immut, mut []Variance
	// covariantInputs is stored as ClassDef.CovariantInputs.
	covariantInputs []bool
	// storage is stored as ClassDef.StorageVariance.
	storage []Variance
	// incomplete reports that the measurement read a class whose variance is not settled.
	// The vectors are then nil, and waitsOn names each such class.
	incomplete bool
	waitsOn    []string
}

// measureVariance measures def's type parameters' variance from its body, settles it
// against any declared modifier, and stores the vectors on def. It reads def.varianceSelf
// and def.varianceDecl, which must be set first.
//
// A measurement that reads a class whose variance is not final stores Invariant throughout
// and marks def provisional. Reading that class as Invariant instead would not be
// conservative: an Invariant class in a non-mutating method's parameter adds an output
// position, which sets that method's inputs aside. A final measurement bumps
// measuredClasses, so a provisional class waiting on it measures again.
func (c *Context) measureVariance(def *ClassDef) {
	m := inferBodyVariance(def, def.varianceSelf, c)
	def.varianceTriedAt = c.measuredClasses
	if m.incomplete {
		n := len(def.TypeParams)
		def.Variance, def.MutVariance = uniformVariance(n, Invariant), uniformVariance(n, Invariant)
		def.CovariantInputs, def.StorageVariance = make([]bool, n), uniformVariance(n, Invariant)
		def.varianceProvisional = true
		def.varianceMismatches = nil
		def.varianceWaitsOn = m.waitsOn
		return
	}
	c.storeFinalVariance(def, m)
}

// storeFinalVariance settles m against def's declared modifiers and stores it on def as
// final.
func (c *Context) storeFinalVariance(def *ClassDef, m measuredVariance) {
	m, def.varianceMismatches = applyDeclaredVariance(m, def.varianceDecl)
	def.Variance, def.MutVariance = m.immut, m.mut
	def.CovariantInputs, def.StorageVariance = m.covariantInputs, m.storage
	def.varianceProvisional = false
	def.varianceWaitsOn = nil
	c.measuredClasses++
}

// settleVariance measures a provisional def again once another class has settled since its
// last measurement. It does nothing for a nil def, a final one, or one being measured.
//
// When def stays provisional, it may sit in a group of classes that each wait on another
// in the group, as `class A<T> { b: B<T> }` and `class B<T> { a: A<T> }` do. None of them
// settles alone, so settleVarianceGroup measures the group together.
func (c *Context) settleVariance(def *ClassDef) {
	if def == nil || !def.varianceProvisional || def.varianceSettling || def.varianceUnsettled ||
		def.varianceTriedAt == c.measuredClasses {
		return
	}
	def.varianceSettling = true
	c.measureVariance(def)
	def.varianceSettling = false
	if def.varianceProvisional {
		c.settleVarianceGroup(def)
	}
}

// settleVarianceGroup measures def together with every class it waits on, directly or
// through another, when each of them has its body inferred and is provisional. The group's
// variance starts at Bivariant, and each member is measured against the rest of the group's
// current vectors until no vector changes. That is the least variance consistent with every
// member's body. The group stays provisional when a member waits on a class whose body is
// not inferred yet, when a member waits on a class outside the group that does not settle,
// or when the vectors keep changing.
func (c *Context) settleVarianceGroup(def *ClassDef) {
	group := map[string]*ClassDef{}
	pending := []*ClassDef{def}
	for len(pending) > 0 {
		member := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if _, seen := group[member.varianceSelf]; seen {
			continue
		}
		group[member.varianceSelf] = member
		for _, name := range member.varianceWaitsOn {
			next, ok := c.classes[name]
			if !ok || !next.varianceMeasured {
				return
			}
			if next.varianceProvisional {
				pending = append(pending, next)
			}
		}
	}
	// A pass reads the vectors earlier members stored in the same pass, so the members are
	// measured in a fixed order. Otherwise map order could change whether a group settles.
	names := slices.Sorted(maps.Keys(group))
	env := &groupVarianceEnv{ctx: c, assumed: map[string]*ClassDef{}}
	params := 0
	for _, name := range names {
		member := group[name]
		n := len(member.TypeParams)
		params += n
		env.assumed[name] = &ClassDef{Variance: uniformVariance(n, Bivariant), MutVariance: uniformVariance(n, Bivariant)}
		member.varianceSettling = true
	}
	defer func() {
		for _, member := range group {
			member.varianceSettling = false
		}
	}()
	measured := map[string]measuredVariance{}
	for range 4*params + 1 {
		changed := false
		for _, name := range names {
			member := group[name]
			m := inferBodyVariance(member, name, env)
			if m.incomplete {
				return
			}
			// A member's declared modifier is what the rest of the group reads it at, as a
			// settled class outside the group is read at its stored variance.
			m, _ = applyDeclaredVariance(m, member.varianceDecl)
			measured[name] = m
			assumed := env.assumed[name]
			if !slices.Equal(m.immut, assumed.Variance) || !slices.Equal(m.mut, assumed.MutVariance) {
				env.assumed[name] = &ClassDef{Variance: m.immut, MutVariance: m.mut}
				changed = true
			}
		}
		if !changed {
			for _, name := range names {
				c.storeFinalVariance(group[name], measured[name])
			}
			return
		}
	}
	// The vectors never settled. Each member keeps Invariant throughout, which is sound, and
	// is not measured again.
	for _, member := range group {
		member.varianceUnsettled = true
	}
}

// groupVarianceEnv is the varianceEnv settleVarianceGroup measures a group through. A class
// in the group reads at the vectors assumed for it, and any other class through ctx.
type groupVarianceEnv struct {
	ctx     *Context
	assumed map[string]*ClassDef
}

func (env *groupVarianceEnv) settledClassDef(name string) (*ClassDef, bool) {
	if def, ok := env.assumed[name]; ok {
		return def, true
	}
	return env.ctx.settledClassDef(name)
}

func (env *groupVarianceEnv) aliasBody(ref *soltype.AliasType) (soltype.Type, bool) {
	return env.ctx.aliasBody(ref)
}

// applyDeclaredVariance settles a measurement against the `in`/`out`/`in out` modifiers
// decl writes. It returns the vectors to store and one VarianceMismatchError per modifier
// looser than the measured variance.
//
// A modifier may be stricter than the measured variance, and the declared variance is then
// what is stored. So `in out T` on a parameter the body reads covariantly makes the class
// invariant in T, which is how an author opts out of a widening the body would allow. A
// looser modifier leaves the measured variance stored, since soundness follows the body.
//
// A modifier speaks for the immutable view. Each other vector stores whichever of its own
// measurement and the modifier is stricter.
func applyDeclaredVariance(m measuredVariance, decl *ast.ClassDecl) (measuredVariance, []*VarianceMismatchError) {
	var mismatches []*VarianceMismatchError
	if decl == nil {
		return m, nil
	}
	for i, tp := range decl.TypeParams {
		if i >= len(m.immut) {
			break
		}
		declared, ok := modifierVariance(tp.Variance)
		if !ok {
			continue
		}
		if joinVariance(declared, m.immut[i]) != declared {
			mismatches = append(mismatches, &VarianceMismatchError{
				Name:     tp.Name,
				Declared: declared,
				Inferred: m.immut[i],
				Class:    decl.Name,
			})
			continue
		}
		m.immut[i] = declared
		m.mut[i] = joinVariance(m.mut[i], declared)
		m.covariantInputs[i] = m.covariantInputs[i] && declared == Covariant
	}
	return m, mismatches
}

// joinVariance returns the least variance at least as strict as both a and b. Bivariant
// imposes nothing and Invariant everything, and covariance joined with contravariance is
// Invariant.
func joinVariance(a, b Variance) Variance {
	switch {
	case a == b, b == Bivariant:
		return a
	case a == Bivariant:
		return b
	default:
		return Invariant
	}
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
// type argument is conservatively marked invariant, since the variance an `extends` edge
// composes with is not measured.
//
// selfName is the class's qualified name, and env returns the classes and alias bodies the
// measurement reads from outside the class. A class instance nested in a member contributes
// each of its arguments at the variance that class measures for it, so `readonly sink:
// Sink<T>` passes T on at Sink's variance rather than as an output. A class whose variance
// env does not return as settled makes the whole measurement incomplete, and the result
// carries no vectors. An alias reference is expanded and its body walked. A reference an
// alias makes to itself adds nothing the walk has not already recorded, so it is skipped.
//
// The class's own references, `Self` among them, read at the variance being solved for.
// The measurement starts from Bivariant and repeats until the vectors stop changing, which
// is the least variance consistent with the body. A body that never settles measures
// Invariant throughout.
//
// It returns two vectors, one per view a reference to an instance can offer. immut is the
// variance an immutable reference sees, which reaches only the members such a reference
// can use. mut is the variance a mutable reference sees, which additionally admits a write
// to every non-`readonly` field and every member that demands a mutable receiver.
//
// A member an immutable reference cannot reach says nothing about the immutable view, so
// it is walked into mut alone. Three members demand a mutable receiver: a write to a
// non-`readonly` field, a setter, and a `&mut self` method. An overloaded method is split
// per signature, since one arm taking `&mut self` says nothing about the arms that do not.
//
// `Array<T>` is what this buys. Its `at(&self, index) -> T | undefined` puts T in an output
// position and its `push(&mut self, item: T)` puts T in an input position. Folding both into
// immut would measure T invariant and leave `Array<1> <: Array<number>` rejected. Reading
// `push` only where it can be called measures T covariant in immut and invariant in mut, so
// the widening holds for an immutable array while `mut Array<1>` and `mut Array<number>` stay
// unrelated.
//
// A field is the same shape of split. Its read is an output position in both views, its
// write an input position only mut has, so `class Box<T> { value: T }` measures T covariant
// in immut and invariant in mut. A mutable reference also reaches into what a field holds,
// so a class instance in a field reads at that class's mutable-view variance in mut. That is
// what keeps `readonly items: Array<T>` invariant in mut, since `b.items.push(x)` type-checks
// through a `mut` reference to the holder.
//
// A method that leaves its receiver immutable has input positions that count only for a
// parameter the views give no output position. Such a method cannot store a value it takes
// into the instance. A `T` passed in can leave only through that method's own outputs, and
// the caller reads those at the type it holds the instance at. Storage that could keep the
// value is a field or a mutating member, and those are measured in full. So once a
// parameter has an output position, a non-mutating method's inputs leave it covariant. A
// parameter with no output position keeps the contravariance its inputs give it, so
// `accept(&self, x: T)` alone still measures `T` contravariant. A `declare class` has no
// body, so the measurement trusts its declaration to list its storage as fields and
// mutating members.
//
// `Array<T>` needs this as well. `includes(&self, searchElement: T)` and
// `with(&self, index: number, value: T)` each put T in an input position. Neither method
// mutates the array, and `at` gives T an output position, so T stays covariant in immut.
//
// The method body this relies on is generic in T, and a subclass override at a fixed
// argument is not. covariantInputs marks each parameter covariant only because its inputs
// were set aside, and checkOverriddenName holds an override to the method read at the
// widest instance widestInstance computes. storage is the immutable-view variance of the
// fields and the members an immutable reference reaches other than methods, before a
// parameter reaching a `super` argument is marked invariant. That check reads it for a
// subclass passing its own parameter on. A mutating member is left out of it, since the
// method the check is about takes an immutable receiver and cannot call one.
//
// Every other member position has one variance both views share, so it is walked once and
// folded into both. A method return or getter is covariant whether or not the holder can
// mutate the instance.
func inferBodyVariance(def *ClassDef, selfName string, env varianceEnv) measuredVariance {
	n := len(def.TypeParams)
	assumed := measuredVariance{immut: uniformVariance(n, Bivariant), mut: uniformVariance(n, Bivariant)}
	// A pass can lower an entry as well as raise it, since a new output position sets a
	// non-mutating method's inputs aside, so the passes are bounded rather than trusted to
	// converge. Each entry has four values, which bounds a sequence that keeps moving.
	for range 4*n + 1 {
		m, readSelf := measureBodyVariance(def, selfName, env, assumed)
		// A body that never names its own class does not read assumed, so one pass is final.
		if m.incomplete || !readSelf || slices.Equal(m.immut, assumed.immut) && slices.Equal(m.mut, assumed.mut) {
			return m
		}
		assumed = m
	}
	return measuredVariance{
		immut:           uniformVariance(n, Invariant),
		mut:             uniformVariance(n, Invariant),
		covariantInputs: make([]bool, n),
		storage:         uniformVariance(n, Invariant),
	}
}

// varianceEnv is what variance inference reads from outside the class it measures.
type varianceEnv interface {
	// settledClassDef returns a class whose variance is final, or ok=false for one that is
	// not registered, not measured yet, or provisional.
	settledClassDef(name string) (*ClassDef, bool)
	// aliasBody returns the body an alias reference stands for, or ok=false for an alias
	// that is not registered or has no body yet.
	aliasBody(ref *soltype.AliasType) (soltype.Type, bool)
}

// uniformVariance returns a vector of n entries, each v.
func uniformVariance(n int, v Variance) []Variance {
	return slices.Repeat([]Variance{v}, n)
}

// measureBodyVariance is one pass of inferBodyVariance. It reads the class's own references
// at assumed, the vectors the previous pass measured. readSelf reports whether the body
// holds such a reference, which is the only way assumed reaches the result.
func measureBodyVariance(def *ClassDef, selfName string, env varianceEnv, assumed measuredVariance) (m measuredVariance, readSelf bool) {
	n := len(def.TypeParams)
	m = measuredVariance{
		immut:           make([]Variance, n),
		mut:             make([]Variance, n),
		covariantInputs: make([]bool, n),
		storage:         make([]Variance, n),
	}
	if n == 0 {
		return m, false
	}
	targets := make(map[*soltype.TypeVarType]int, n)
	for i, tp := range def.TypeParams {
		targets[tp.Var] = i
	}
	var waitsOn []string
	argVariance := func(ct *soltype.ClassType, mutable bool) []Variance {
		if ct.Name == selfName {
			readSelf = true
			if mutable {
				return assumed.mut
			}
			return assumed.immut
		}
		nested, ok := env.settledClassDef(ct.Name)
		if !ok {
			if !slices.Contains(waitsOn, ct.Name) {
				waitsOn = append(waitsOn, ct.Name)
			}
			return nil
		}
		if mutable {
			return nested.MutVariance
		}
		return nested.Variance
	}
	newVisitor := func(mutable bool) *varianceVisitor {
		return &varianceVisitor{
			targets:     targets,
			pos:         make([]bool, n),
			neg:         make([]bool, n),
			mutable:     mutable,
			argVariance: argVariance,
			aliasBody:   env.aliasBody,
			aliasesSeen: set.NewSet[string](),
		}
	}
	// Each visitor records one group of occurrences:
	//   - fieldRef: a field read through an immutable reference.
	//   - mutFieldRef: a field read and written through a mutable reference.
	//   - otherRef: every other member an immutable reference reaches, except the
	//     non-mutating methods.
	//   - readerRef: the non-mutating methods, whose input positions count only for a
	//     parameter with no output position.
	//   - mutOnlyRef: the members only a mutable reference reaches.
	//   - superRef: the `extends` arguments.
	fieldRef := newVisitor(false)
	mutFieldRef := newVisitor(true)
	otherRef := newVisitor(false)
	readerRef := newVisitor(false)
	mutOnlyRef := newVisitor(false)
	superRef := newVisitor(false)
	if def.Body != nil {
		for _, elem := range def.Body.Elems {
			if prop, ok := elem.(*soltype.PropertyElem); ok {
				soltype.AcceptObjElem(prop, fieldRef, soltype.Positive)
				soltype.AcceptObjElem(prop, mutFieldRef, soltype.Positive)
				if !prop.Readonly {
					// Walking the same field again at Negative records the input position
					// `obj.f = …` occupies. A `readonly` field rejects that write.
					soltype.AcceptObjElem(prop, mutFieldRef, soltype.Negative)
				}
				continue
			}
			immutPart, mutOnlyPart := splitByReceiverMut(elem)
			if method, ok := immutPart.(*soltype.MethodElem); ok {
				soltype.AcceptObjElem(method, readerRef, soltype.Positive)
			} else if immutPart != nil {
				soltype.AcceptObjElem(immutPart, otherRef, soltype.Positive)
			}
			if mutOnlyPart != nil {
				soltype.AcceptObjElem(mutOnlyPart, mutOnlyRef, soltype.Positive)
			}
		}
	}
	// A parameter appearing in a `super` type argument is marked in both directions, so it
	// collapses to invariant.
	for _, super := range def.Supers {
		for _, arg := range super.TypeArgs {
			arg.Accept(superRef, soltype.Positive)
			arg.Accept(superRef, soltype.Negative)
		}
	}
	for i := range n {
		ownPos := fieldRef.pos[i] || otherRef.pos[i] || readerRef.pos[i]
		readerNeg := readerRef.neg[i] && !ownPos
		ownNeg := fieldRef.neg[i] || otherRef.neg[i] || readerNeg
		m.storage[i] = collapseVariance(fieldRef.pos[i] || otherRef.pos[i], fieldRef.neg[i] || otherRef.neg[i])
		m.immut[i] = collapseVariance(ownPos || superRef.pos[i], ownNeg || superRef.neg[i])
		m.mut[i] = collapseVariance(
			mutFieldRef.pos[i] || otherRef.pos[i] || readerRef.pos[i] || mutOnlyRef.pos[i] || superRef.pos[i],
			mutFieldRef.neg[i] || otherRef.neg[i] || readerNeg || mutOnlyRef.neg[i] || superRef.neg[i],
		)
		m.covariantInputs[i] = readerRef.neg[i] && m.immut[i] == Covariant
	}
	if len(waitsOn) > 0 {
		return measuredVariance{incomplete: true, waitsOn: waitsOn}, readSelf
	}
	return m, readSelf
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

// splitByReceiverMut divides a class member into the part an immutable reference can
// reach and the part only a mutable one can, each returned with its `self` receiver
// stripped for the variance walk. Either half is nil when the member contributes nothing
// to that view.
//
// A setter is a write, so only a mutable reference reaches it. A method is split per
// signature, since `find(&self, …)` and `push(&mut self, …)` on one name demand different
// receivers and an overload set may hold both. Every other member is readable through
// either view and goes to the immutable half whole.
func splitByReceiverMut(elem soltype.ObjTypeElem) (immutPart, mutOnlyPart soltype.ObjTypeElem) {
	switch e := elem.(type) {
	case *soltype.SetterElem:
		return nil, stripSelfReceiver(e)
	case *soltype.GetterElem:
		if mutReceiver(e.SelfParam) {
			return nil, stripSelfReceiver(e)
		}
		return stripSelfReceiver(e), nil
	case *soltype.MethodElem:
		var immutSigs, mutOnlySigs []*soltype.FuncType
		for _, sig := range e.Signatures {
			bare := *sig
			bare.SelfParam = nil
			if mutReceiver(sig.SelfParam) {
				mutOnlySigs = append(mutOnlySigs, &bare)
				continue
			}
			immutSigs = append(immutSigs, &bare)
		}
		if len(immutSigs) > 0 {
			immutPart = &soltype.MethodElem{Name: e.Name, Signatures: immutSigs, Static: e.Static}
		}
		if len(mutOnlySigs) > 0 {
			mutOnlyPart = &soltype.MethodElem{Name: e.Name, Signatures: mutOnlySigs, Static: e.Static}
		}
		return immutPart, mutOnlyPart
	default:
		return stripSelfReceiver(elem), nil
	}
}

// mutReceiver reports whether a member's `self` receiver demands mutable access, which is
// what makes the member unreachable through an immutable reference. Both `&mut self` and `mut
// self` are a mutable RefType over the class. A `&self` or `self` receiver is not, and a nil
// receiver is a static member or a plain function.
func mutReceiver(self *soltype.FuncParam) bool {
	if self == nil {
		return false
	}
	ref, isRef := self.Type.(*soltype.RefType)
	return isRef && ref.Mut
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
	// mutable marks a walk through something a mutable reference can change, so a class
	// instance reached here reads at its mutable-view variance.
	mutable bool
	// argVariance returns the variance a class instance's arguments are read at, from the
	// immutable or the mutable view. A missing entry reads as Invariant.
	argVariance func(ct *soltype.ClassType, mutable bool) []Variance
	// aliasBody returns the body an alias reference stands for, or ok=false when it has none
	// to read.
	aliasBody func(ref *soltype.AliasType) (soltype.Type, bool)
	// aliasesSeen holds a key for each alias reference already walked, made of the reference
	// printed with qualified names, the polarity, and the view. Walking one again records nothing new, so a
	// reference an alias makes to itself and a repeated reference are both skipped.
	aliasesSeen set.Set[string]
	// aliasDepth counts the alias bodies the walk is inside. A recursive alias whose
	// arguments grow at each level never repeats a key, and this bounds it.
	aliasDepth int
}

// maxAliasDepth bounds how many alias bodies a variance walk enters inside one another.
const maxAliasDepth = 32

// withMutable returns a visitor recording into the same vectors with mutable set to m.
func (v *varianceVisitor) withMutable(m bool) *varianceVisitor {
	copied := *v
	copied.mutable = m
	return &copied
}

func (v *varianceVisitor) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	switch t := t.(type) {
	case *soltype.RefType:
		if !t.Mut {
			// Nothing writes through an immutable borrow, so its pointee reads as written
			// even where a mutable reference reached the borrow.
			if v.mutable {
				t.Accept(v.withMutable(false), pol)
				return soltype.EnterResult{SkipChildren: true}
			}
			return soltype.EnterResult{}
		}
		// A `mut` borrow is a read-write window on its pointee, so a parameter reached
		// through one occupies an input position as well as an output position and comes
		// out invariant. This is what keeps `readonly inner: mut Box<T>` from measuring T
		// covariant: the field itself rejects `h.inner = …`, but the borrow it holds still
		// admits `h.inner.value = …`.
		inner := v.withMutable(true)
		t.Inner.Accept(inner, pol)
		t.Inner.Accept(inner, pol.Flip())
		return soltype.EnterResult{SkipChildren: true}
	case *soltype.FuncType:
		// A function value cannot be changed through a mutable reference to its holder, so
		// its signature reads as written.
		if v.mutable {
			t.Accept(v.withMutable(false), pol)
			return soltype.EnterResult{SkipChildren: true}
		}
	case *soltype.AliasType:
		// An alias stands for its body, so the body is what decides the polarity each
		// argument lands at. A reference with no body to read, or one past maxAliasDepth,
		// has its arguments walked in both directions.
		key := fmt.Sprintf("%s|%v|%t", soltype.PrintQualified(t), pol, v.mutable)
		if v.aliasesSeen.Contains(key) {
			return soltype.EnterResult{SkipChildren: true}
		}
		body, ok := v.aliasBody(t)
		if !ok || v.aliasDepth >= maxAliasDepth {
			for _, arg := range t.TypeArgs {
				arg.Accept(v, pol)
				arg.Accept(v, pol.Flip())
			}
			return soltype.EnterResult{SkipChildren: true}
		}
		v.aliasesSeen.Add(key)
		inside := *v
		inside.aliasDepth++
		body.Accept(&inside, pol)
		return soltype.EnterResult{SkipChildren: true}
	case *soltype.ClassType:
		vec := v.argVariance(t, v.mutable)
		for i, arg := range t.TypeArgs {
			argVar := Invariant
			if i < len(vec) {
				argVar = vec[i]
			}
			switch argVar {
			case Covariant:
				arg.Accept(v, pol)
			case Contravariant:
				arg.Accept(v, pol.Flip())
			case Invariant:
				arg.Accept(v, pol)
				arg.Accept(v, pol.Flip())
			}
		}
		return soltype.EnterResult{SkipChildren: true}
	case *soltype.TypeVarType:
		if i, found := v.targets[t]; found {
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
	if def.HasSelf {
		// A member the receiver's own class declares resolves `Self` at that class, which for
		// an own member is the receiver's class already. The class substitution below still
		// runs for a generic class, so the two compose.
		own := make([]soltype.ObjTypeElem, len(def.Body.Elems))
		for i, elem := range def.Body.Elems {
			own[i] = projectClassMember(def, ct, projectSelf(def, ct, elem))
		}
		return own
	}
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
	// ct is both the class whose chain is walked and the receiver `Self` resolves at, since a
	// member access enters the chain at the receiver's own class.
	return c.chainElems(def, ct, ct, taken, visited)
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
func (c *Context) chainElems(def *ClassDef, ct, recv *soltype.ClassType, taken, visited set.Set[string]) []soltype.ObjTypeElem {
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
				// The class substitution takes the SUPERCLASS instance, since the member was
				// declared against the superclass's own parameters and the `extends` clause
				// says what they are here. The `Self` substitution takes recv, the class the
				// receiver belongs to, which is what makes an inherited `-> Self` yield the
				// subclass rather than the class that declared the member.
				out = append(out, projectClassMember(superDef, super, projectSelf(superDef, recv, elem)))
			}
		}
		// Marked after the loop rather than inside it, so a class declaring both halves of an
		// accessor pair contributes both instead of shadowing its own second half.
		addElemNames(taken, superDef.Body)
		out = append(out, c.chainElems(superDef, super, recv, taken, visited)...)
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
		c.settleVariance(def)
		var errs []SolverError
		// Relate the lifetime arguments as equal regions. A class records no per-parameter
		// variance for the lifetime sort, and a parameter may sit in a position of either
		// variance inside the body, so each argument is invariant: `Holder<'x>` reaches
		// `Holder<'static>` only when the two lifetimes outlive each other. Constraining one
		// direction alone would let a caller launder a short region into a long one through
		// the nominal name wherever the parameter is used mutably. Measuring per-parameter
		// lifetime variance the way def.Variance measures the type sort would relax this.
		for i := range min(len(sub.LifetimeArgs), len(super.LifetimeArgs)) {
			c.constrainLt(sub.LifetimeArgs[i], super.LifetimeArgs[i])
			c.constrainLt(super.LifetimeArgs[i], sub.LifetimeArgs[i])
		}
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

// --- The nominal meet ---

// glbClass is the greatest lower bound of two class tags, the nominal meet LhsNf
// reads through Base. It settles three ways.
//
//  1. Two tags of one class meet position by position, through meetClassArgs.
//  2. Two tags the declared `extends` graph orders meet to the lower one, since
//     every instance of that one already carries the other's tag.
//  3. Two tags neither of which reaches the other meet to `never`, dropping the
//     conjunct. This is the fast path from caveat 1 in
//     planning/ml_struct/02-caveats-and-mitigations.md: an intersection of
//     unrelated classes is settled here rather than in structural work. It is sound
//     because a class declares at most one superclass, so such a pair has no common
//     subclass.
//
// ok is false when none of the three applies, which keeps both tags as atoms and
// loses nothing, since a two-atom list already denotes the meet exactly.
//
// It reads the registry and records nothing, unlike constrainNominal, which decides
// the same graph by emitting bounds.
func (c *Context) glbClass(a, b *soltype.ClassType) (soltype.Type, bool) {
	if a.Name == b.Name {
		if met, ok := c.meetClassArgs(a, b); ok {
			return met, true
		}
		return nil, false
	}
	aUp, aReaches, aSettled := c.ancestorInstance(a, b.Name)
	if aReaches && c.instanceBelow(aUp, b) {
		return a, true
	}
	bUp, bReaches, bSettled := c.ancestorInstance(b, a.Name)
	if bReaches && c.instanceBelow(bUp, a) {
		return b, true
	}
	if !aReaches && !bReaches && aSettled && bSettled {
		return &soltype.NeverType{}, true
	}
	// Either one class reaches the other while the instance below carries an argument
	// the one above rules out, or a walk ran into a graph still being built. Meeting
	// `Wrapper<string>` with `Reader<number>` under `class Wrapper<T> extends Reader<T>`
	// is the first: `Wrapper<never>` is below both, so the pair is not disjoint either.
	return nil, false
}

// nominalSubtype reports whether the class instance sub is below super, following
// the declared `extends` graph and checking sub's arguments at super's class. It is
// the pure twin of constrainNominal, which decides the same relation by emitting
// bounds.
//
// It always dispatches arguments by the mutable-view variance, so it rejects pairs
// constrainNominal accepts outside a mutable borrow, such as `Box<5> <: Box<number>`
// for a Box covariant to a reader and invariant to a writer. That direction is the
// safe one, since a rejection only leaves two atoms unfused.
func (c *Context) nominalSubtype(sub, super *soltype.ClassType) bool {
	up, reaches, _ := c.ancestorInstance(sub, super.Name)
	return reaches && c.instanceBelow(up, super)
}

// instanceBelow reports whether up is below super, where both name ONE class and up
// is what ancestorInstance projected. Meeting the two leaves up unchanged exactly
// when up is the lower, since a type met with something above it is itself.
func (c *Context) instanceBelow(up, super *soltype.ClassType) bool {
	met, ok := c.meetClassArgs(up, super)
	return ok && equalType(met, up)
}

// meetClassArgs meets two instances of ONE class, position by position, dispatching
// each by the variance the registry records for it. ok is false when a position
// admits no exact meet, which keeps the two tags separate.
//
// The vector read is the MUTABLE view, the stricter of the two. A normal form
// carries no borrow around the tag to say whether a write can reach a position, so
// the meet assumes one can, which pins a writable position to matching arguments.
//
// The exactness and enum-variant flags must agree, since both are read off the
// declaration. Fusing two tags whose exactness differs is left to #1064.
func (c *Context) meetClassArgs(a, b *soltype.ClassType) (*soltype.ClassType, bool) {
	if a.Final != b.Final || a.Variant != b.Variant || len(a.TypeArgs) != len(b.TypeArgs) {
		return nil, false
	}
	if !sameLifetimeSlice(a.LifetimeArgs, b.LifetimeArgs, &alphaCtx{}) {
		return nil, false
	}
	// varianceAt reads Invariant off a nil def, so two tags of an unregistered class
	// fuse only on matching arguments.
	def, _ := c.classDef(a.Name)
	c.settleVariance(def)
	args := make([]soltype.Type, len(a.TypeArgs))
	same := true
	for i := range a.TypeArgs {
		argA, argB := a.TypeArgs[i], b.TypeArgs[i]
		switch def.varianceAt(i, true) {
		case Covariant:
			// An instance below both reads the position at both types, so at their meet.
			args[i] = c.meetTypes(argA, argB)
		case Contravariant:
			// The dual. Such an instance accepts a write of either type, so of their join.
			args[i] = c.joinTypes(argA, argB)
		case Bivariant:
			// A phantom parameter says nothing about the instances, so either argument does.
			args[i] = argA
		default: // Invariant
			if !equalType(argA, argB) {
				return nil, false
			}
			args[i] = argA
		}
		same = same && equalType(args[i], argA)
	}
	if same {
		// Every position met to a's own argument, so a IS the meet.
		return a, true
	}
	return &soltype.ClassType{
		Name:         a.Name,
		TypeArgs:     args,
		Defaults:     a.Defaults,
		LifetimeArgs: a.LifetimeArgs,
		Lt:           a.Lt,
		Final:        a.Final,
		Variant:      a.Variant,
	}, true
}

// ancestorInstance walks the declared `extends` graph up from ct, looking for the
// class named `name`. It returns three things.
//
//   - up is the instance of that class ct denotes, with ct's arguments substituted
//     into each edge, so `class B<T> extends A<T>` asked for A at B<5> yields A<5>.
//   - reaches says whether the class was found, which is when up is meaningful.
//   - settled says whether the walk read a finished graph. It is false when the walk
//     stopped at an unregistered class or at one whose edges are unresolved, so a
//     false reaches must not be taken for "cannot reach".
//
// The edges are `extends` alone, the same ones constrainNominalWalk follows, so the
// meet decides the solver's subtype relation rather than a wider one. An
// `implements` clause is a conformance assertion the walk skips. The walk is keyed
// by class name, so a cyclic chain terminates.
func (c *Context) ancestorInstance(ct *soltype.ClassType, name string) (up *soltype.ClassType, reaches, settled bool) {
	return c.ancestorInstanceWalk(ct, name, set.NewSet[string]())
}

func (c *Context) ancestorInstanceWalk(ct *soltype.ClassType, name string, walked set.Set[string]) (*soltype.ClassType, bool, bool) {
	if ct.Name == name {
		return ct, true, true
	}
	if walked.Contains(ct.Name) {
		// An enclosing call is walking this class already, so its answer covers these edges.
		return nil, false, true
	}
	walked.Add(ct.Name)
	def, ok := c.classDef(ct.Name)
	if !ok || def.EdgesPending {
		return nil, false, false
	}
	// One walked set is shared across the walk rather than kept per path. Supers holds at
	// most one edge, so a class is reachable by a single path and no branch can shut
	// another out of one. Several nominal edges would need the walk recorded per path.
	settled := true
	for _, superType := range def.Supers {
		found, reaches, superSettled := c.ancestorInstanceWalk(substituteSuperArgs(def, ct, superType), name, walked)
		if reaches {
			return found, true, true
		}
		settled = settled && superSettled
	}
	return nil, false, settled
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
func (c *checker) projectedMember(lvl int, blame ast.Node, name string, recv, carrier soltype.Type) (pathResult, bool) {
	ct, ok := c.classCarrier(carrier)
	if !ok {
		return pathResult{}, false
	}
	def, ok := c.ctx.classDef(ct.Name)
	if !ok || def.Body == nil {
		return pathResult{}, false
	}
	member, found := c.projectedClassMember(ct, ct, name, (*soltype.ObjectType).ReadMember, set.NewSet[string]())
	// A field declines here, the way objectMember and classBodyMember decline one, so a
	// class field read takes the structural path below in valueProp. That path is where
	// the read-through-borrow rule lives, and reading a field's type straight off the
	// projected body skips it: the field would come back bare and a write through a `mut`
	// receiver would be rejected (#617). A method, getter, or setter still resolves here,
	// since the structural requirement cannot express those.
	if _, isProp := member.(*soltype.PropertyElem); found && isProp {
		return pathResult{}, false
	}
	if !found {
		// The miss is rare, so project the whole body here to render the diagnostic at
		// the instance's arguments rather than the declared type parameters.
		obj, _ := c.ctx.projectClassBody(ct)
		err := &MissingPropertyError{Sub: obj, Super: propReq(name, &soltype.UnknownType{}, false), Name: name}
		err.prov, err.site = c.prov, blame
		c.errs = append(c.errs, err)
		return pathResult{value: &soltype.ErrorType{}}, true
	}
	// A member declaring `&mut self` needs mutable access to the instance, on an instance
	// reached from outside the class as much as on the `self` classBodyMember serves. Both
	// call the same check, so `c.bump()` and `self.bump()` answer the same way for the same
	// receiver.
	c.checkReceiverMut(blame, name, recv, memberSelfParam(member))
	// Each access from outside the class gets its own copy of the method's lifetimes, the
	// way a call to a free function instantiates the function's scheme.
	return c.memberValue(lvl, blame, c.instantiateMethodLifetimes(lvl, blame, recv, member)), true
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
	obj, ok := c.objectCarrier(carrier)
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

// memberCarrier peels a receiver to the shape a member lookup dispatches on, expanding a
// transparent alias and unwrapping a `typeof v`. Both repeat, since either may name the other.
// A type that is neither is returned unchanged.
//
// Every lookup below asserts a kind, and neither a handle nor a residual is one. Without this
// a receiver annotated `C` declines where the same object written inline resolves, and the read
// falls through to the structural `{name: fieldVar}` requirement. That requirement is a
// PropertyElem, so a method or getter under the name is reported as missing rather than read.
//
// A borrow an expansion uncovers is peeled, so `type M = mut {m(&self) -> number}` reads the way
// the inline spelling does. The walk is bounded rather than guarded by the names it has
// expanded, because `type Id<T> = T` over `Id<Id<X>>` reaches one name twice at different
// arguments and has to keep going. Running out returns the handle, which declines the lookup.
func (c *checker) memberCarrier(t soltype.Type) soltype.Type {
	for range maxExpandDepth {
		switch cur := t.(type) {
		case *soltype.AliasType:
			t = peelBorrows(c.ctx.expandAlias(cur))
		case *soltype.TypeofType:
			t = peelBorrows(cur.Ty)
		default:
			return t
		}
	}
	return t
}

// objectCarrier reads the object type a receiver denotes: the type itself, or the single
// object among an unresolved var's lower bounds. It mirrors classCarrier and
// classValueCarrier, and like them it declines a var whose bounds disagree, since there is no
// one member list to read in that case.
//
// Each candidate goes through memberCarrier first, so a receiver written as an alias or a
// `typeof` reads as the object it stands for. The peel is inside the pick rather than around
// the call because a receiver reaches here as a variable carrying the alias among its lower
// bounds, not as the alias itself.
func (c *checker) objectCarrier(t soltype.Type) (*soltype.ObjectType, bool) {
	return soleLowerBound(t, func(x soltype.Type) (*soltype.ObjectType, bool) {
		obj, ok := c.memberCarrier(x).(*soltype.ObjectType)
		return obj, ok
	})
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
func (c *checker) projectedClassMember(ct, recv *soltype.ClassType, name string, lookup memberLookup, visited set.Set[string]) (soltype.ObjTypeElem, bool) {
	def, ok := c.ctx.classDef(ct.Name)
	if !ok || def.Body == nil {
		return nil, false
	}
	// Member names are invariant under substitution, so look the member up on the
	// unprojected body and project only the one accessed, rather than rebuilding the
	// whole body per access.
	if member, found := lookup(def.Body, name); found {
		// The class substitution takes ct, the class the member was declared against as the
		// walk reaches it. The `Self` substitution takes recv, the class the ACCESS was made
		// through, which stays fixed as the walk climbs so an inherited `-> Self` yields the
		// receiver's class rather than the declaring one.
		return projectClassMember(def, ct, projectSelf(def, recv, member)), true
	}
	if visited.Contains(ct.Name) {
		return nil, false
	}
	visited.Add(ct.Name)
	for _, superType := range def.Supers {
		superInstance := substituteSuperArgs(def, ct, superType)
		if member, found := c.projectedClassMember(superInstance, recv, name, lookup, visited); found {
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
// A method whose return flows from a class type parameter — such as `read(&self) { self.v }`
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
	obj, ok := c.memberCarrier(carrier).(*soltype.ObjectType)
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
	c.checkReceiverMut(blame, name, recv, memberSelfParam(member))
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

// projectSelf resolves every `Self` in a member at recv, the class the RECEIVER belongs to.
// That is what makes `Self` polymorphic: a member declared `me(&self) -> Self` on A and reached
// through a `B extends A` yields B, the way TypeScript's `this` type does.
//
// recv is threaded down from the access rather than taken from the declaring class, which is the
// one thing the substitution cannot read off def. projectClassMember rewrites an inherited
// member for the arguments the `extends` clause writes, so it holds the SUPERCLASS instance
// there; `Self` needs the subclass instead, so the two substitutions take different classes and
// stay separate.
//
// def.HasSelf keeps the walk off every class that wrote no `Self`, which is nearly all of them.
func projectSelf(def *ClassDef, recv *soltype.ClassType, member soltype.ObjTypeElem) soltype.ObjTypeElem {
	if !def.HasSelf || recv == nil {
		return member
	}
	return soltype.AcceptObjElem(member, &selfSubst{recv: recv}, soltype.Positive)
}

// selfSubst replaces every `Self` with the receiver's own instance. It substitutes the whole
// node rather than descending into it, so the declaring class a `Self` carries is dropped along
// with it — that class is what `Self` means only until a receiver is known.
type selfSubst struct{ recv *soltype.ClassType }

func (s *selfSubst) EnterType(t soltype.Type, _ soltype.Polarity) soltype.EnterResult {
	if _, ok := t.(*soltype.SelfType); ok {
		return soltype.EnterResult{Type: s.recv, SkipChildren: true}
	}
	return soltype.EnterResult{}
}

func (s *selfSubst) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

// soleLowerBound resolves t to the single value pick accepts: t itself, or the one accepted
// value among an unresolved variable's lower bounds. A class instance and an object both
// flow through the bound graph as a variable carrying the concrete type among its bounds
// rather than as a bare type, so a lookup off a binding has to read through one.
//
// A variable whose accepted bounds disagree resolves to nothing, since there is no one
// value to read. That covers a join of two classes and a join of the same class at
// different arguments. A bound pick rejects is skipped rather than failing the walk, which
// is what a lookup wants: a member is read off whichever bound carries it.
//
// A vacuous `v <: v` self-edge names no value the variable holds and is skipped, the same
// edge readCarrier drops.
func soleLowerBound[T soltype.Type](t soltype.Type, pick func(soltype.Type) (T, bool)) (T, bool) {
	if got, ok := pick(t); ok {
		return got, true
	}
	var zero T
	v, isVar := t.(*soltype.TypeVarType)
	if !isVar {
		return zero, false
	}
	found, seen := zero, false
	for _, lb := range v.LowerBounds {
		if lb == soltype.Type(v) {
			continue
		}
		got, ok := pick(lb)
		if !ok {
			continue
		}
		if seen && !equalType(found, got) {
			return zero, false
		}
		found, seen = got, true
	}
	return found, seen
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
// Each candidate goes through memberCarrier first, for the reason objectCarrier gives.
func (c *checker) classCarrier(t soltype.Type) (*soltype.ClassType, bool) {
	return soleLowerBound(t, func(x soltype.Type) (*soltype.ClassType, bool) {
		ct, ok := c.memberCarrier(x).(*soltype.ClassType)
		return ct, ok
	})
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
			c.recordMethodSelfParam(blame, m.Signatures[0].SelfParam)
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
	if ct, ok := c.classCarrier(carrier); ok {
		return c.projectedClassMember(ct, ct, name, (*soltype.ObjectType).WriteMember, set.NewSet[string]())
	}
	if obj, ok := c.memberCarrier(carrier).(*soltype.ObjectType); ok {
		return obj.WriteMember(name)
	}
	if obj, ok := c.classValueCarrier(carrier); ok {
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

// recordMethodSelfParam notes the receiver of the method a member access reads, so a rule at
// the call site can reach the type strippedMethodSig drops. An overloaded method records
// nothing: its arms may declare different receivers, and which arm a call resolves is decided
// later, in resolveOverload.
func (c *checker) recordMethodSelfParam(blame ast.Node, self *soltype.FuncParam) {
	if self == nil {
		return
	}
	if c.methodSelfParams == nil {
		c.methodSelfParams = map[ast.Node]*soltype.FuncParam{}
	}
	c.methodSelfParams[blame] = self
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

// checkReceiverMut rejects a member reached through a receiver that cannot give it the access
// its `self` declares. It constrains the accessing receiver recv against the accessed member's
// own declared `self`, which the caller passes as self.
//
// A member needs mutable access for `&mut self` and `mut self`. The four pairings:
//
//   - immutable receiver → mutable member: rejected, there is no mut to lend
//   - mutable receiver   → mutable member: ok
//   - mutable receiver   → immutable member: ok, mutable downgrades to shared
//   - immutable receiver → immutable member: ok
//
// The receiver is rebuilt as `Self` in the mutability it lends, so the diagnostic reads
// `immutable C <: mutable C`.
//
// A consuming `self` or `mut self` member moves the instance, so the receiver must also own
// it. An owned receiver is moved at the access, through consumeReceiver. A borrowed receiver
// has nothing to move, so it is reported as a ConsumingReceiverBorrowedError naming the
// member. A `mut self` member still asks for a mutable receiver. Passing a class instance to
// an owned parameter does not move it in the caller, so an immutable instance may still be
// shared, and only a mutable binding vouches that nothing else reads it.
//
// recv is the un-stripped receiver, so it still carries the access it has to lend. A nil
// self, which a static member and a property both have, is a no-op, as is a receiver that is
// not a class instance.
func (c *checker) checkReceiverMut(blame ast.Node, name string, recv soltype.Type, self *soltype.FuncParam) {
	if self == nil {
		return
	}
	inner := receiverClass(self.Type)
	if inner == nil {
		return
	}
	if !isBorrowType(self.Type) {
		if len(heldBorrows(recv)) > 0 {
			c.report(&ConsumingReceiverBorrowedError{Name: name, Site: blame})
			return
		}
		c.consumeReceiver(blame)
	}
	recvT := soltype.Type(inner)
	if lendsMut(recv) {
		recvT = soltype.NewRef(true, nil, inner)
	}
	c.constrain(blame, recvT, self.Type)
}

// heldBorrows returns every borrow recv may hold, or nil when it holds none. It looks
// through a binding var to its lower bounds the way lendsMut does, so `val q = p` over a
// borrowed p still reads as a borrow at `q.finish()`. A join such as
// `val v = if k { a } else { b }` over two borrowed parameters holds both, since the branch
// taken at run time may be either one.
func heldBorrows(recv soltype.Type) []*soltype.RefType {
	switch recv := recv.(type) {
	case *soltype.RefType:
		if recv.Lt != nil {
			return []*soltype.RefType{recv}
		}
	case *soltype.TypeVarType:
		var out []*soltype.RefType
		for _, lb := range recv.LowerBounds {
			if r, ok := lb.(*soltype.RefType); ok && r.Lt != nil {
				out = append(out, r)
			}
		}
		return out
	}
	return nil
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
// may be the immutable one. Reporting mutable off a single bound would accept a `&mut self`
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
// directly for a `&self`, or the ClassType inside the borrow for a `&mut self` / `&self`
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
	obj, ok := c.classValueCarrier(carrier)
	if !ok {
		return pathResult{}, false
	}
	member, found := obj.ReadMember(name)
	if !found {
		return pathResult{}, false
	}
	// A static method's lifetimes are instantiated per access the way an instance method's
	// are. It has no receiver to relate.
	return c.memberValue(lvl, blame, c.instantiateMethodLifetimes(lvl, blame, nil, member)), true
}

// classValueCarrier resolves a receiver to the class-value object it reads as: an object
// carrying a ConstructorElem directly, or a binding var whose lower bounds carry one, the
// same look-through classCarrier uses for an instance. A var with two different class-value
// lower bounds is ambiguous and left to the structural path.
// Each candidate goes through memberCarrier first, for the reason objectCarrier gives.
func (c *checker) classValueCarrier(t soltype.Type) (*soltype.ObjectType, bool) {
	return soleLowerBound(t, func(x soltype.Type) (*soltype.ObjectType, bool) {
		obj, ok := c.memberCarrier(x).(*soltype.ObjectType)
		if !ok {
			return nil, false
		}
		// Either unnamed callable member marks the object as a class value. A class
		// declaring a call signature and no constructor carries only the former, which is
		// the shape `Symbol` has, and its statics are read the same way.
		if _, hasCtor := obj.Constructor(); hasCtor {
			return obj, true
		}
		_, hasCall := obj.Callable()
		return obj, hasCall
	})
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
