package solver

import (
	"fmt"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// prelude_classes.go settles which classes the checker's own rules mean by `Array`
// and `Promise`, and reads their type arguments back.
//
// A rule such as "a rest parameter absorbs its arguments into an array", or
// "awaiting a value yields what its promise carries", has to reach the class
// whether or not the file under inference imports one. The prelude package declares
// both, and preludeScope binds that package's exports once per run, so each name is
// read off that scope rather than through the scope chain a file writes into.

const (
	arrayClassName   = "Array"
	promiseClassName = "Promise"
)

// resolvePreludeClasses records the qualified name of each class the rules single out,
// reporting one the run's tree does not declare well enough to use.
//
// The rules that read them run deep inside constraint solving, where a lookup that
// could load a package would be unsafe. Settling the names once at the start of the
// run keeps those rules to a string comparison, and they answer the same inside a
// speculation trial as outside one.
//
// A tree that declares neither is a broken standard library rather than a
// configuration to degrade into. Reporting it once here is what keeps every rule that
// reads a name from having to answer for its absence, and what keeps a program from
// being told its `await` is wrong when the real fault is upstream of its file.
func (c *checker) resolvePreludeClasses() {
	c.ctx.arrayClass = c.preludeClass(arrayClassName, arrayTypeParams)
	c.ctx.promiseClass = c.preludeClass(promiseClassName, promiseTypeParams)
}

// The type-parameter count each class has to declare for the rules that read it to
// mean anything. `Array<T>` gives the element a rest parameter absorbs into, and
// `Promise<T, E>` the value an `await` yields beside what it may reject with.
const (
	arrayTypeParams   = 1
	promiseTypeParams = 2
)

// preludeClass returns the qualified name the prelude package declares name as a class
// under, reporting and answering "" when it declares no such class or declares one the
// rules cannot read.
//
// The lookup reads the prelude scope's own bindings rather than walking to its parent,
// so it answers what the prelude package declared rather than what an enclosing scope
// seeded under the same name.
func (c *checker) preludeClass(name string, params int) string {
	b, found := c.preludeScope().ownType(name)
	if !found {
		c.report(&MissingPreludeClassError{Name: name, Params: params, Fault: preludeClassAbsent})
		return ""
	}
	cls, isClass := b.Type.(*soltype.ClassType)
	if !isClass {
		c.report(&MissingPreludeClassError{Name: name, Params: params, Fault: preludeClassNotAClass})
		return ""
	}
	def, registered := c.ctx.classDef(cls.Name)
	if !registered {
		c.report(&MissingPreludeClassError{Name: name, Params: params, Fault: preludeClassAbsent})
		return ""
	}
	if len(def.TypeParams) != params {
		c.report(&MissingPreludeClassError{
			Name: name, Params: params, Fault: preludeClassWrongArity, Got: len(def.TypeParams),
		})
		return ""
	}
	return cls.Name
}

// arrayElem returns the element type of t when t is an instance of the `Array`
// the run settled on, and false otherwise. It lives on Context rather than on the
// checker because the subtyping core does, and that is where the rest-parameter
// rules read it.
func (c *Context) arrayElem(t soltype.Type) (soltype.Type, bool) {
	args, ok := c.instanceArgs(t, c.arrayClass, 1)
	if !ok {
		return nil, false
	}
	return args[0], true
}

// promiseParts returns what an instance of the `Promise` the run settled on resolves
// to and what it may reject with, and false when t is not one.
//
// A promise that cannot reject carries `never` in the rejection slot rather than
// nothing, so a caller compares one canonical value and never a nil.
func (c *Context) promiseParts(t soltype.Type) (inner, errT soltype.Type, ok bool) {
	args, held := c.instanceArgs(t, c.promiseClass, 2)
	if !held {
		return nil, nil, false
	}
	return args[0], args[1], true
}

// promiseOf returns an instance of the `Promise` the run settled on. A nil errT stands
// for the `never` a promise that cannot reject carries, so a caller with no rejection to
// name passes nothing.
//
// The instance carries the declaration's own parameter defaults, so what the printer
// elides is what a reference omitting the argument resolves to. A
// `Promise<T, E = unknown>` renders its rejection slot instead of hiding a `never` the
// declaration would not have filled.
//
// A run whose tree declares no usable `Promise` has already reported it, so this hands
// back the recovery sentinel rather than a second diagnostic. ErrorType absorbs in both
// directions inside constrain, so nothing downstream cascades on it.
func (c *Context) promiseOf(inner, errT soltype.Type) soltype.Type {
	if c.promiseClass == "" {
		return &soltype.ErrorType{}
	}
	def, declared := c.classDef(c.promiseClass)
	if !declared || len(def.TypeParams) != promiseTypeParams {
		return &soltype.ErrorType{}
	}
	if errT == nil {
		errT = &soltype.NeverType{}
	}
	return &soltype.ClassType{
		Name:     c.promiseClass,
		TypeArgs: []soltype.Type{inner, errT},
		Defaults: paramDefaults(def.TypeParams),
	}
}

// instanceArgs returns t's type arguments when t is an instance of the class named
// className carrying want of them, and false otherwise. An empty className is a run
// that resolved no such class, where nothing in play is one.
func (c *Context) instanceArgs(t soltype.Type, className string, want int) ([]soltype.Type, bool) {
	cls, isClass := t.(*soltype.ClassType)
	if !isClass || className == "" || cls.Name != className {
		return nil, false
	}
	if len(cls.TypeArgs) != want {
		return nil, false
	}
	return cls.TypeArgs, true
}

// preludeClassFault says how a class the rules name falls short of what they read.
type preludeClassFault int

const (
	// preludeClassAbsent marks a name the standard library does not declare, or one
	// whose class declaration did not resolve.
	preludeClassAbsent preludeClassFault = iota
	// preludeClassNotAClass marks a name bound to something other than a class.
	preludeClassNotAClass
	// preludeClassWrongArity marks a class declaring a different type-parameter count
	// than the rules read.
	preludeClassWrongArity
)

// MissingPreludeClassError reports a class the checker's own rules name that the run's
// standard library does not declare well enough to use. `Array` is what a rest parameter
// absorbs its arguments into and what a `for`-`in` reads its element off; `Promise` is
// what an `async fn` returns and an `await` unwraps. A tree declaring neither is broken
// rather than minimal, so the run says so once instead of every rule that reads a name
// answering for its absence.
//
// It carries no span. The fault is in the standard library the run was pointed at, not
// at any offset in the file under inference, and blaming a line there would send a
// reader to code that is not wrong.
type MissingPreludeClassError struct {
	// Name is the class as the rules write it, `Array` or `Promise`.
	Name string
	// Params is the type-parameter count the rules require of it.
	Params int
	// Fault says which way the declaration falls short.
	Fault preludeClassFault
	// Got is the count the class declared. It is read only for preludeClassWrongArity.
	Got int
}

func (e *MissingPreludeClassError) Message() string {
	switch e.Fault {
	case preludeClassNotAClass:
		return fmt.Sprintf(
			"the standard library binds `%s` to something other than a class, "+
				"so the checker cannot read it", e.Name)
	case preludeClassWrongArity:
		return fmt.Sprintf(
			"the standard library declares `%s` with %d type parameter(s), "+
				"but the checker reads %d", e.Name, e.Got, e.Params)
	default:
		return fmt.Sprintf(
			"the standard library declares no class `%s`, which the checker needs", e.Name)
	}
}
func (e *MissingPreludeClassError) Span() ast.Span      { return ast.Span{} }
func (e *MissingPreludeClassError) Related() []ast.Span { return nil }
func (e *MissingPreludeClassError) isSolverError()      {}
