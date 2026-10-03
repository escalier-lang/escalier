package solver

import (
	"slices"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// walkConstructorBodies produces a FuncType per declared constructor, in source order. Each
// has its body walked so field assignments refine the instance fields, then a callable
// signature built from its value parameters. A class declaring none yields none.
//
// What a class with no declared constructor gets instead is the caller's decision, since it
// turns on whether the class declares a call signature as well. See inferClassDecl.
func (c *checker) walkConstructorBodies(
	scope *Scope,
	lvl int,
	self *soltype.ClassType,
	body *soltype.ObjectType,
	ctors []*ast.ConstructorElem,
) []*soltype.FuncType {
	out := make([]*soltype.FuncType, len(ctors))
	for i, ctor := range ctors {
		out[i] = c.walkConstructorBody(scope, lvl, self, body, ctor)
	}
	return out
}

// synthesizeConstructor builds the implicit constructor of a class that declares no explicit
// one and no call signature: a function taking one parameter per required instance field, in
// declaration order, and returning the instance. An optional field is omitted, matching its
// omission from the required set.
func (c *checker) synthesizeConstructor(self *soltype.ClassType, body *soltype.ObjectType) *soltype.FuncType {
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
// parameters — the params after the leading `&mut self` — returning the instance.
//
// After the body is walked it runs definite-assignment analysis so a required field
// left unassigned on some path is a FieldNotInitializedError and a `self.f` read before
// its assignment is a ReadBeforeInitError.
func (c *checker) walkConstructorBody(scope *Scope, lvl int, self *soltype.ClassType, body *soltype.ObjectType, ctor *ast.ConstructorElem) *soltype.FuncType {
	// The parser materializes `&mut self` as Fn.Params[0]; the callable signature is
	// the params after it.
	valueParams := ctor.Fn.Params
	if ctor.Receiver != nil && len(valueParams) > 0 {
		valueParams = valueParams[1:]
	}
	bodySig := ctor.Fn.FuncSig
	bodySig.Params = valueParams

	ctorScope := scope.Child()
	// A constructor's `self` is always a mutable borrow so its body can assign fields,
	// regardless of the class's default mutability. The body hands the instance back to the
	// caller, so it may not consume it, and a borrow keeps a consuming method out of reach.
	// It binds the `self` view, so a subclass constructor can assign a field it inherits.
	ctorRecv := &ast.MethodReceiver{Mut: true}
	c.bindSelf(ctorScope, ctorRecv, c.ctx.freshLifetime(lvl), self, c.ctx.selfView(self, body))
	// The liveness pre-pass defines `self` as one of the body's parameters, so places rooted
	// at the instance under construction are tracked like places rooted at a parameter.
	c.memberReceiver = ctorRecv

	// Collect the body's `super(…)` calls while it is walked, so the rules about the body as
	// a whole can be checked once every call is known. A class with no superclass still gets
	// a context, so a stray `super(…)` there is reported against the missing edge rather than
	// as a call outside a constructor.
	prevSuper := c.superCtx
	// scope, not ctorScope: the superclass's value binding is read from where the subclass is
	// declared, so a constructor parameter cannot shadow it.
	superCtx := &superCtx{declScope: scope}
	if def, ok := c.ctx.classDef(self.Name); ok && len(def.Supers) > 0 {
		superCtx.super = def.Supers[0]
	}
	c.superCtx = superCtx
	// A constructor carries no type parameters of its own, since the class owns them,
	// so generic resolution stays off here, matching the method path.
	ft := c.inferFunc(ctorScope, lvl, bodySig, ctor.Fn.Body, ctor, false)
	c.superCtx = prevSuper
	c.checkSuperCalls(superCtx, ctor)
	// Definite assignment runs over the class's OWN fields. An inherited field is left out,
	// since the `super(…)` call delegates its initialization to the superclass constructor
	// rather than making the subclass re-assign every field its ancestors declare.
	c.checkConstructorInit(body, ctor)
	// A constructor returns a fresh instance, not the `undefined` its statement body falls
	// off to, so override the inferred return with the instance type.
	return &soltype.FuncType{Params: ft.Params, Ret: self, Throws: ft.Throws, Inexact: ft.Inexact}
}

// checkConstructorInit runs definite-assignment analysis over an explicit
// constructor's body. It reports a FieldNotInitializedError for any required instance
// field left unassigned on some path that reaches a normal exit, a ReadBeforeInitError
// for a `self.f` read on a path where f is not yet assigned, and a
// MethodCallBeforeInitError for a `self.method(...)` call on a path where some required
// field is not yet assigned, since the callee may read any field.
//
// The analysis reuses the CFG and the forward move-state dataflow rather than a fresh
// tree traversal, so a `for` loop back edge inside the constructor is handled by the
// same machinery moves and borrows already rely on. Field assignment is modeled as a
// "move" of a synthetic per-field id: a field whose move-state is Moved at a point is
// assigned on every path reaching it, so a required field that is not Moved at the
// exit is uninitialized on some path, a `self.f` read where f is not Moved reads it
// before initialization, and a `self.method(...)` call where some field is not Moved
// happens before the instance is fully built.
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
		c:        c,
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

	// A method call on `self` may read any field, so it requires every required field
	// assigned at the call site. Report the ones still unassigned on some reaching path.
	for _, call := range col.calls {
		var callMissing []string
		for name, id := range fieldIDs {
			if info.StateBefore(call.ref, id) != liveness.Moved {
				callMissing = append(callMissing, name)
			}
		}
		if len(callMissing) > 0 {
			slices.Sort(callMissing)
			c.report(&MethodCallBeforeInitError{MissingFields: callMissing, Call: call.node})
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

// initCall is one `self.method(...)` call the collector recorded: the CFG point of the
// call and the node to blame.
type initCall struct {
	ref  liveness.StmtRef
	node ast.Node
}

// initCollector walks a constructor body statement by statement, recording each
// `self.f` assignment as a "gen" of field f at the current program point, each `self.f`
// read as an initRead, and each `self.method(...)` call as an initCall. currentRef is
// set by the driver before each statement, so a write, read, or call nested in that
// statement's expression is attributed to it.
type initCollector struct {
	ast.DefaultVisitor
	c          *checker
	currentRef liveness.StmtRef
	fieldIDs   map[string]liveness.VarID
	gens       map[liveness.StmtRef]set.Set[liveness.VarID]
	reads      []initRead
	calls      []initCall
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
			if name, ok := col.c.selfFieldName(e.Left); ok {
				// The right side runs before the field is assigned, so scan it for reads
				// first — `self.x = self.x` reads x before init — then mark x assigned.
				// The write target itself is not a read, so only a bracket key is walked.
				if ix, isIndex := e.Left.(*ast.IndexExpr); isIndex {
					ix.Index.Accept(col)
				}
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
	case *ast.CallExpr:
		// A call whose callee is `self.x(...)` needs every required field initialized,
		// since the callee may read any of them. Record the call site and scan the
		// arguments for reads, but not the `self.x` callee — it is a method reference,
		// not a field read.
		if _, ok := col.c.selfFieldName(e.Callee); ok {
			col.calls = append(col.calls, initCall{ref: col.currentRef, node: e})
			for _, arg := range e.Args {
				arg.Accept(col)
			}
			return false
		}
	case *ast.MemberExpr, *ast.IndexExpr:
		if name, ok := col.c.selfFieldName(e); ok {
			// Only a required field is tracked. A read of a method, getter, or optional
			// field names no required field, so it is not a read-before-init.
			if _, tracked := col.fieldIDs[name]; tracked {
				col.reads = append(col.reads, initRead{field: name, ref: col.currentRef, node: e})
			}
			if ix, isIndex := e.(*ast.IndexExpr); isIndex {
				ix.Index.Accept(col)
			}
			return false
		}
	}
	return true
}

// selfFieldName returns the field name a member access off the `self` identifier
// names. The dot form `self.f` names f. The bracket form `self[k]` names the field its
// key names: a string literal, a well-known symbol, or a key whose inferred type is one
// string or number literal, so `self[k]` with `val k = "f"` names f. ok is false for
// `other.f`, whose object is not `self`, for a deeper path like `self.a.b`, whose
// object is `self.a` rather than `self`, and for a bracket key naming no single field.
//
// It reads a key's type from Info, so it must run after the constructor body is walked.
func (c *checker) selfFieldName(e ast.Expr) (string, bool) {
	switch e := e.(type) {
	case *ast.MemberExpr:
		if e.OptChain || e.Prop == nil || !isSelfIdent(e.Object) {
			return "", false
		}
		return e.Prop.Name, true
	case *ast.IndexExpr:
		if e.OptChain || !isSelfIdent(e.Object) {
			return "", false
		}
		if name, ok := constStringKey(e.Index); ok {
			return name, true
		}
		if name, ok := wellKnownSymbolMember(e.Index); ok {
			return name, true
		}
		return c.literalKeyName(e.Index)
	}
	return "", false
}

// isSelfIdent reports whether e is the bare `self` identifier.
func isSelfIdent(e ast.Expr) bool {
	id, ok := e.(*ast.IdentExpr)
	return ok && id.Name == "self"
}
