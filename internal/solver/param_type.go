package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/printer"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// pendingParamType is one unannotated closure parameter waiting for checkParamTypes. v is the
// variable inferFunc minted for the parameter's type.
type pendingParamType struct {
	closure *ast.FuncExpr
	param   *ast.Param
	pat     ast.Pat
	v       *soltype.TypeVarType
}

// queueParamTypeCheck queues one unannotated parameter of the closure fe for checkParamTypes.
// A queued entry is rolled back with the probe that recorded it, as queueReturnCheck's is.
func (c *checker) queueParamTypeCheck(fe *ast.FuncExpr, p *ast.Param, pat ast.Pat, v *soltype.TypeVarType) {
	if probe := c.ctx.probe; probe != nil {
		queueLen := len(c.pendingParamTypes)
		probe.onRollback(func() { c.pendingParamTypes = c.pendingParamTypes[:queueLen] })
	}
	c.pendingParamTypes = append(c.pendingParamTypes, pendingParamType{closure: fe, param: p, pat: pat, v: v})
}

// checkParamTypes reports each queued closure parameter that no context gave a type, and clears
// the queue so a later group starts empty. It runs once every constraint of the component has
// run.
//
// A context types a parameter through the constraint that checks the closure against the
// function type it flows into. Parameters are contravariant, so that constraint makes the
// context's parameter type a lower bound of the parameter's variable. The body's uses of the
// parameter only add upper bounds, so a variable with no lower bound got its type from nowhere
// but the body, unless the check ran under a probe that rolled its bounds back.
func (c *checker) checkParamTypes() {
	pending := c.pendingParamTypes
	c.pendingParamTypes = nil
	// A closure is checked against its context in the component that infers it, so the
	// record of those checks is read here and then dropped.
	checked := c.ctx.checkedFuncs
	c.ctx.checkedFuncs = nil
	for _, p := range pending {
		if len(p.v.LowerBounds) > 0 {
			continue
		}
		// A check against a generic function type runs under a probe, which rolls the bound
		// it gave the parameter back, so the closure's own function type records the check.
		if ft, ok := c.info.TypeOf(p.closure).(*soltype.FuncType); ok && checked.Contains(ft) {
			continue
		}
		name, _ := printer.Print(p.pat, printer.DefaultOptions())
		err := &MissingParamTypeError{Param: p.param, Name: name}
		if shown := c.info.TypeOf(p.pat); shown != nil {
			if inferred := coalesce(shown, soltype.Negative); writable(inferred) {
				err.Suggestion = name + ": " + soltype.PrintAsScheme(inferred)
			}
		}
		c.report(err)
	}
}

// writable reports whether t is a type to suggest writing as a parameter's annotation. A type
// variable has no written form. `unknown` is what a variable the body left unconstrained
// coalesces to, and writing it would give up the type a caller passes, so it is not suggested.
func writable(t soltype.Type) bool {
	finder := &unwritableFinder{}
	t.Accept(finder, soltype.Positive)
	return !finder.found
}

// unwritableFinder records whether a type holds a type variable or `unknown`.
type unwritableFinder struct {
	found bool
}

func (v *unwritableFinder) EnterType(t soltype.Type, _ soltype.Polarity) soltype.EnterResult {
	switch t.(type) {
	case *soltype.TypeVarType, *soltype.UnknownType:
		v.found = true
	}
	return soltype.EnterResult{}
}

func (v *unwritableFinder) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

// MissingParamTypeError reports a closure parameter with no annotation that nothing the closure
// flows into gives a type. Param is the parameter, which the error blames. Name is its pattern as
// the source writes it. Suggestion is the annotation to write, or empty when the checker has no
// type it can write.
type MissingParamTypeError struct {
	Param      *ast.Param
	Name       string
	Suggestion string
}

func (*MissingParamTypeError) isSolverError()        {}
func (e *MissingParamTypeError) Span() ast.Span      { return e.Param.Span() }
func (e *MissingParamTypeError) Related() []ast.Span { return nil }
func (e *MissingParamTypeError) Message() string {
	msg := "parameter '" + e.Name + "' has no type, and nothing the closure flows into supplies one"
	if e.Suggestion == "" {
		return msg + "; write its type"
	}
	return msg + "; write `" + e.Suggestion + "`"
}
