package solver

import (
	"slices"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// inferConstructor produces a class's constructor as a FuncType returning the
// instance. With an explicit constructor it walks the constructor body so field
// assignments refine the instance fields, then builds a callable signature from the
// value parameters. With none it synthesizes one from the instance fields, unless the
// class extends a superclass — a subclass must declare its own constructor to call
// `super`, so a missing one is reported and a field-based constructor is synthesized
// for recovery.
func (c *checker) inferConstructor(scope *Scope, lvl int, decl *ast.ClassDecl, self *soltype.ClassType, body *soltype.ObjectType, ctors []*ast.ConstructorElem) soltype.Type {
	if len(ctors) == 0 {
		if decl.Extends != nil {
			c.report(&SubclassConstructorRequiredError{Decl: decl})
		}
		return c.synthesizeConstructor(self, body)
	}
	return c.walkConstructorBody(scope, lvl, self, body, ctors[0])
}

// synthesizeConstructor builds the implicit constructor of a class with no explicit
// one: a function taking one parameter per required instance field, in declaration
// order, and returning the instance. An optional field is omitted, matching its
// omission from the required set.
func (c *checker) synthesizeConstructor(self *soltype.ClassType, body *soltype.ObjectType) soltype.Type {
	var params []*soltype.FuncParam
	for _, e := range body.Elems {
		prop, ok := e.(*soltype.PropertyElem)
		if !ok || prop.Optional {
			continue
		}
		params = append(params, &soltype.FuncParam{
			Pattern: &soltype.IdentPat{Name: prop.Name},
			Type:    prop.Type,
		})
	}
	return &soltype.FuncType{Params: params, Ret: self}
}

// walkConstructorBody walks an explicit constructor's body with `self` bound to the
// owned-mutable instance body, so `self.x = value` refines field x through the record
// write machinery, and returns a callable signature: the constructor's value
// parameters — the params after the leading `mut self` — returning the instance.
//
// After the body is walked it runs definite-assignment analysis so a required field
// left unassigned on some path is a FieldNotInitializedError and a `self.f` read before
// its assignment is a ReadBeforeInitError.
func (c *checker) walkConstructorBody(scope *Scope, lvl int, self *soltype.ClassType, body *soltype.ObjectType, ctor *ast.ConstructorElem) soltype.Type {
	// The parser materializes `mut self` as Fn.Params[0]; the callable signature is
	// the params after it.
	valueParams := ctor.Fn.Params
	if ctor.Receiver != nil && len(valueParams) > 0 {
		valueParams = valueParams[1:]
	}
	bodySig := ctor.Fn.FuncSig
	bodySig.Params = valueParams

	ctorScope := scope.Child()
	// A constructor's `self` is always owned-mutable so its body can assign fields,
	// regardless of the class's default mutability.
	c.bindSelf(ctorScope, &ast.MethodReceiver{Mut: true}, body)

	ft := c.inferFunc(ctorScope, lvl, bodySig, ctor.Fn.Body, ctor)
	c.checkConstructorInit(body, ctor)
	// A constructor returns a fresh instance, not the void its statement body falls off
	// to, so override the inferred return with the instance type.
	return &soltype.FuncType{Params: ft.Params, Ret: self, Inexact: ft.Inexact}
}

// checkConstructorInit runs definite-assignment analysis over an explicit
// constructor's body. It reports a FieldNotInitializedError for any required instance
// field left unassigned on some path that reaches a normal exit, and a
// ReadBeforeInitError for a `self.f` read on a path where f is not yet assigned.
//
// The analysis reuses the CFG and the forward move-state dataflow rather than a fresh
// tree traversal, so a `for` loop back edge inside the constructor is handled by the
// same machinery moves and borrows already rely on. Field assignment is modeled as a
// "move" of a synthetic per-field id: a field whose move-state is Moved at a point is
// assigned on every path reaching it, so a required field that is not Moved at the
// exit is uninitialized on some path, and a `self.f` read where f is not Moved reads it
// before initialization.
//
// A `throw` is exempt: it assigns every required field synthetically, so a path that
// throws before initializing the instance vacuously satisfies the requirement and does
// not pollute the join at the exit.
func (c *checker) checkConstructorInit(body *soltype.ObjectType, ctor *ast.ConstructorElem) {
	if ctor.Fn == nil || ctor.Fn.Body == nil {
		return
	}

	// Each required (non-optional) instance field gets a synthetic id the move-state
	// dataflow keys on. Optional fields are exempt: they may stay unset.
	fieldIDs := map[string]liveness.VarID{}
	next := liveness.VarID(1)
	for _, e := range body.Elems {
		prop, ok := e.(*soltype.PropertyElem)
		if !ok || prop.Optional {
			continue
		}
		fieldIDs[prop.Name] = next
		next++
	}
	if len(fieldIDs) == 0 {
		return
	}

	cfg := liveness.BuildCFG(*ctor.Fn.Body)
	col := &initCollector{
		fieldIDs: fieldIDs,
		gens:     map[liveness.StmtRef]set.Set[liveness.VarID]{},
	}
	// Walk each CFG block's statements. The builder has already flattened control flow
	// into blocks and wrapped each branch condition as its own statement, so the per-
	// statement scan attributes every `self.f` write and read to the right program point
	// without re-deriving the branch structure.
	for _, block := range cfg.Blocks {
		for idx, stmt := range block.Stmts {
			col.currentRef = liveness.StmtRef{BlockID: block.ID, StmtIdx: idx}
			stmt.Accept(col)
		}
	}

	info := liveness.AnalyzeMoves(cfg, col.gens)

	// A required field not Moved at the exit join is unassigned on some path reaching a
	// normal exit. StmtIdx -1 reads the exit block's entry state, the join over every
	// predecessor.
	exitRef := liveness.StmtRef{BlockID: cfg.Exit.ID, StmtIdx: -1}
	var missing []string
	for name, id := range fieldIDs {
		if info.StateBefore(exitRef, id) != liveness.Moved {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		c.report(&FieldNotInitializedError{FieldNames: missing, Ctor: ctor})
	}

	for _, r := range col.reads {
		if info.StateBefore(r.ref, fieldIDs[r.field]) != liveness.Moved {
			c.report(&ReadBeforeInitError{FieldName: r.field, Read: r.node})
		}
	}
}

// initRead is one `self.f` read the collector recorded: the field name, the CFG point
// of the read, and the node to blame.
type initRead struct {
	field string
	ref   liveness.StmtRef
	node  ast.Node
}

// initCollector walks a constructor body statement by statement, recording each
// `self.f` assignment as a "gen" of field f at the current program point and each
// `self.f` read as an initRead. currentRef is set by the driver before each statement,
// so a write or read nested in that statement's expression is attributed to it.
type initCollector struct {
	ast.DefaultVisitor
	currentRef liveness.StmtRef
	fieldIDs   map[string]liveness.VarID
	gens       map[liveness.StmtRef]set.Set[liveness.VarID]
	reads      []initRead
}

// gen records that field is assigned at the current statement. A field absent from
// fieldIDs is optional or unknown and is not tracked.
func (col *initCollector) gen(field string) {
	id, ok := col.fieldIDs[field]
	if !ok {
		return
	}
	at := col.gens[col.currentRef]
	if at == nil {
		at = set.NewSet[liveness.VarID]()
		col.gens[col.currentRef] = at
	}
	at.Add(id)
}

func (col *initCollector) EnterExpr(e ast.Expr) bool {
	switch e := e.(type) {
	case *ast.FuncExpr:
		// A nested closure has its own body and CFG. Its reads of `self.f` are not part
		// of the constructor's straight-line flow, so do not descend into it.
		return false
	case *ast.BinaryExpr:
		if e.Op == ast.Assign {
			if name, ok := selfFieldName(e.Left); ok {
				// The right side runs before the field is assigned, so scan it for reads
				// first — `self.x = self.x` reads x before init — then mark x assigned.
				// The write target itself is not a read, so the left side is not walked.
				e.Right.Accept(col)
				col.gen(name)
				return false
			}
		}
	case *ast.ThrowExpr:
		// A throwing path never completes construction, so it is exempt: mark every
		// required field assigned so the throw does not lower the init state at the join.
		// The thrown value is still scanned for reads.
		for name := range col.fieldIDs {
			col.gen(name)
		}
		if e.Arg != nil {
			e.Arg.Accept(col)
		}
		return false
	case *ast.MemberExpr:
		if name, ok := selfFieldName(e); ok {
			col.reads = append(col.reads, initRead{field: name, ref: col.currentRef, node: e})
			return false
		}
	}
	return true
}

// selfFieldName returns the field name of a `self.f` member access — the property of a
// non-optional-chain member whose object is the `self` identifier. ok is false for any
// other expression, including `self[k]`, `other.f`, and a deeper path like `self.a.b`
// whose object is `self.a` rather than `self`.
func selfFieldName(e ast.Expr) (string, bool) {
	m, ok := e.(*ast.MemberExpr)
	if !ok || m.OptChain || m.Prop == nil {
		return "", false
	}
	id, ok := m.Object.(*ast.IdentExpr)
	if !ok || id.Name != "self" {
		return "", false
	}
	return m.Prop.Name, true
}
