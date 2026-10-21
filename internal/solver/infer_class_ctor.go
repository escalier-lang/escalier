package solver

import (
	"fmt"
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
// turns on whether the class declares a call signature as well. See inferClassBodies.
func (c *checker) walkConstructorBodies(
	scope *Scope,
	lvl int,
	self *soltype.ClassType,
	body *soltype.ObjectType,
	extends *ast.TypeRefTypeAnn,
	ctors []*ast.ConstructorElem,
) []*soltype.FuncType {
	out := make([]*soltype.FuncType, len(ctors))
	for i, ctor := range ctors {
		out[i] = c.walkConstructorBody(scope, lvl, self, body, extends, ctor)
	}
	return out
}

// synthesizeConstructor builds the implicit constructor of a class that declares no explicit
// one and no call signature: a function taking one parameter per required instance field, in
// declaration order, and returning the instance. An optional field is omitted, matching its
// omission from the required set.
//
// Each parameter takes the name implicitConstructorParamNames reads for its field, and a field
// it has no name for takes the field's own name. The second return pairs each parameter with
// the field it initializes.
func (c *checker) synthesizeConstructor(decl *ast.ClassDecl, self *soltype.ClassType, body *soltype.ObjectType) (*soltype.FuncType, []implicitParam) {
	paramNames := c.implicitConstructorParamNames(decl)
	var params []*soltype.FuncParam
	implicit := []implicitParam{}
	for _, e := range body.Elems {
		prop, ok := e.(*soltype.PropertyElem)
		if !ok || prop.Optional {
			continue
		}
		name, named := paramNames[prop.Name]
		if !named {
			name = prop.Name
		}
		params = append(params, &soltype.FuncParam{
			Pattern: &soltype.IdentPat{Name: name},
			Type:    prop.Type,
		})
		implicit = append(implicit, implicitParam{member: prop.Name, name: name})
	}
	return &soltype.FuncType{Params: params, Ret: self}, implicit
}

// implicitParam is one parameter of a synthesized constructor: the member name of the field
// its value initializes, and the parameter's name.
type implicitParam struct {
	member, name string
}

// synthesizeSubclassConstructor builds the implicit constructor of a subclass that declares
// no explicit one and no call signature. Its superclass's constructor must have been
// synthesized too, so each of its parameters initializes one field.
//
// The constructor takes the superclass constructor's parameters, read at the type arguments
// the `extends` clause writes, followed by one parameter per required field the subclass
// declares. A field the subclass redeclares from its superclass takes no parameter of its
// own. Its value arrives through the superclass's parameter for it, which takes the
// subclass's declared type, since the override check requires that type to fit the
// superclass's. It returns the instance.
//
// scope is the scope the class body reads names from, where the superclass's value binding
// is read, the same scope a `super(…)` call in a declared constructor reads it from.
//
// On success it records the parameters on the subclass's ClassDef, so a class extending this
// one can synthesize its own, and records in Info what emission needs to build the body. ok is
// false when no such constructor can be built. The reason is reported as a
// SubclassConstructorSynthesisError, and the caller recovers.
func (c *checker) synthesizeSubclassConstructor(lvl int, scope *Scope, decl *ast.ClassDecl, self *soltype.ClassType, body *soltype.ObjectType) (*soltype.FuncType, bool) {
	fail := func(reason string) (*soltype.FuncType, bool) {
		c.report(&SubclassConstructorSynthesisError{Decl: decl, Reason: reason})
		return nil, false
	}
	written := ast.QualIdentToString(decl.Extends.Name)
	def, ok := c.ctx.classDef(self.Name)
	if !ok || len(def.Supers) == 0 {
		return fail(fmt.Sprintf("`%s` in its `extends` clause names no class", written))
	}
	super := def.Supers[0]
	// Each of the superclass constructor's parameters has to initialize one field, which
	// only a synthesized constructor guarantees.
	superDef, ok := c.ctx.classDef(super.Name)
	if !ok || superDef.implicitCtor == nil {
		return fail(fmt.Sprintf("the constructor of `%s` is not synthesized", super.Name))
	}
	// A class declared in a package has a registry name that no value binding carries, such
	// as `import:geometry.Point`, so the name the `extends` clause writes is resolved when
	// the registry name misses.
	sigs, found := c.superConstructorSigs(lvl, scope, super.Name, decl.Extends.Name)
	if !found || len(sigs) != 1 || len(sigs[0].Params) != len(superDef.implicitCtor) {
		return fail(fmt.Sprintf("the constructor of `%s` is not known where it is declared", super.Name))
	}
	sig := sigs[0]

	// The superclass constructor binds the superclass's own parameters, one per argument
	// the `extends` clause writes. Reading each at its argument gives the parameters this
	// subclass forwards.
	nt, nl := min(len(super.TypeArgs), len(sig.TypeParams)), min(len(super.LifetimeArgs), len(sig.LifetimeParams))
	subst := newTypeSubst(sig.TypeParams[:nt], super.TypeArgs, sig.LifetimeParams[:nl], super.LifetimeArgs)
	superIndex := map[string]int{}
	superNames := make([]string, len(superDef.implicitCtor))
	params := make([]*soltype.FuncParam, len(sig.Params))
	for i, p := range superDef.implicitCtor {
		superIndex[p.member] = i
		superNames[i] = p.name
		params[i] = &soltype.FuncParam{Pattern: sig.Params[i].Pattern, Type: subst.apply(sig.Params[i].Type)}
	}

	inherited := set.NewSet[*ast.FieldElem]()
	var ownFields []*ast.FieldElem
	var ownMembers []string
	for _, elem := range decl.Body {
		field, ok := elem.(*ast.FieldElem)
		if !ok || !ast.TakesConstructorParam(field) {
			continue
		}
		member, named := c.inferredKeyName(field.Name)
		if i, redeclared := superIndex[member]; named && redeclared {
			inherited.Add(field)
			if prop, ok := body.Prop(member); ok {
				params[i].Type = prop.Type
			}
			continue
		}
		ownFields = append(ownFields, field)
		ownMembers = append(ownMembers, member)
	}

	_, fieldParams := ast.ImplicitSubclassConstructor(decl, superNames, inherited.Contains, nil)

	superNameSet := set.FromSlice(superNames)
	implicit := append([]implicitParam{}, superDef.implicitCtor...)
	for i := range ownFields {
		name, _ := identPatName(fieldParams[i].Pattern)
		if superNameSet.Contains(name) {
			return fail(fmt.Sprintf("a field of `%s` and a parameter of the constructor of `%s` are both named `%s`", self.Name, super.Name, name))
		}
		var t soltype.Type = &soltype.ErrorType{}
		if prop, ok := body.Prop(ownMembers[i]); ok {
			t = prop.Type
		}
		params = append(params, &soltype.FuncParam{Pattern: &soltype.IdentPat{Name: name}, Type: t})
		implicit = append(implicit, implicitParam{member: ownMembers[i], name: name})
	}

	def.implicitCtor = implicit
	c.info.setImplicitSubclassConstructor(decl, ImplicitSubclassConstructor{SuperParams: superNames, Inherited: inherited})
	return &soltype.FuncType{Params: params, Ret: self}, true
}

// implicitConstructorParamNames maps each required instance field's member name to the
// parameter ast.ImplicitConstructor binds its value under. A field whose key binds no name,
// such as `[sym]` or `"a-b"`, arrives under a generated `_field<N>`, and the map holds that
// name. The map is empty for a class ast.ImplicitConstructor builds no constructor for.
//
// It reads each computed key's member name from `Info`, so it must run after the class
// body's keys are inferred.
func (c *checker) implicitConstructorParamNames(decl *ast.ClassDecl) map[string]string {
	names := map[string]string{}
	synth := ast.ImplicitConstructor(decl, nil)
	if synth == nil {
		return names
	}
	// The first parameter is the `self` receiver. Each one after it belongs to the next
	// required instance field in declaration order.
	params := synth.Fn.Params[1:]
	i := 0
	for _, elem := range decl.Body {
		field, ok := elem.(*ast.FieldElem)
		if !ok || !ast.TakesConstructorParam(field) {
			continue
		}
		if i >= len(params) {
			break
		}
		param := params[i]
		i++
		paramName, ok := identPatName(param.Pattern)
		if !ok {
			continue
		}
		memberName, ok := c.inferredKeyName(field.Name)
		if !ok {
			continue
		}
		// A class that declares one member twice is reported elsewhere. The first
		// declaration's parameter name is the one kept.
		if _, named := names[memberName]; !named {
			names[memberName] = paramName
		}
	}
	return names
}

// walkConstructorBody walks an explicit constructor's body with `self` bound to the
// owned-mutable instance body, so `self.x = value` refines field x through the record
// write machinery, and returns a callable signature: the constructor's value
// parameters — the params after the leading `&mut self` — returning the instance.
//
// After the body is walked it runs definite-assignment analysis so a required field
// left unassigned on some path is a FieldNotInitializedError and a `self.f` read before
// its assignment is a ReadBeforeInitError.
func (c *checker) walkConstructorBody(scope *Scope, lvl int, self *soltype.ClassType, body *soltype.ObjectType, extends *ast.TypeRefTypeAnn, ctor *ast.ConstructorElem) *soltype.FuncType {
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
	if extends != nil {
		superCtx.superName = extends.Name
	}
	c.superCtx = superCtx
	// Collect the body's writes to the class's own readonly fields while it is walked. Only
	// definite assignment knows whether a write is the field's first on every path, so the
	// decision waits for checkConstructorInit.
	prevInit := c.ctorInit
	ctorInit := &ctorInitCtx{body: body}
	c.ctorInit = ctorInit
	// A constructor carries no type parameters of its own, since the class owns them,
	// so generic resolution stays off here, matching the method path.
	ft := c.inferFunc(ctorScope, lvl, bodySig, ctor.Fn.Body, ctor, false, true)
	c.superCtx = prevSuper
	c.ctorInit = prevInit
	c.checkSuperCalls(superCtx, ctor)
	// Definite assignment runs over the class's OWN fields. An inherited field is left out,
	// since the `super(…)` call delegates its initialization to the superclass constructor
	// rather than making the subclass re-assign every field its ancestors declare.
	c.checkConstructorInit(body, ctor, ctorInit)
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
//
// It also reports a ReadonlyFieldError for each write in init.writes that does not
// initialize its field. A write initializes the field when it is reachable code in the
// constructor's own body, outside any nested function, and the field is assigned on no
// path reaching it.
func (c *checker) checkConstructorInit(body *soltype.ObjectType, ctor *ast.ConstructorElem, init *ctorInitCtx) {
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
	// Each readonly field written in the body gets an id of its own, so the state before
	// each write says whether an earlier write reaches it. It is separate from the field's
	// fieldIDs entry because a `throw` gens every fieldIDs entry, and a throw nested in an
	// expression such as `b || throw e` would otherwise mark the field written on the path
	// that continues.
	readonlyIDs := map[string]liveness.VarID{}
	for _, w := range init.writes {
		if _, ok := readonlyIDs[w.field]; ok {
			continue
		}
		readonlyIDs[w.field] = next
		next++
	}
	if len(fieldIDs) == 0 && len(readonlyIDs) == 0 {
		return
	}

	cfg := liveness.BuildCFG(*ctor.Fn.Body)
	col := &initCollector{
		c:           c,
		fieldIDs:    fieldIDs,
		readonlyIDs: readonlyIDs,
		gens:        map[liveness.StmtRef]set.Set[liveness.VarID]{},
		writes:      map[*ast.BinaryExpr]liveness.StmtRef{},
		repeats:     set.NewSet[*ast.BinaryExpr](),
	}
	// The builder splits `self.f = if c { a } else { b }` into its branch blocks and keeps
	// the statement itself out of every block. It maps the statement to the entry of the
	// join block instead, and puts the target `self.f` in the block before the branch as a
	// statement of its own. decomposed holds each such assignment by its target.
	decomposed := map[ast.Expr]*ast.BinaryExpr{}
	decomposedRefs := map[*ast.BinaryExpr]liveness.StmtRef{}
	for stmt, ref := range liveness.BuildStmtToRef(cfg) {
		if ref.StmtIdx >= 0 {
			continue
		}
		es, ok := stmt.(*ast.ExprStmt)
		if !ok {
			continue
		}
		be, ok := es.Expr.(*ast.BinaryExpr)
		if !ok || be.Op != ast.Assign {
			continue
		}
		if _, ok := c.selfFieldName(be.Left); ok {
			decomposed[be.Left] = be
			decomposedRefs[be] = ref
		}
	}
	// Walk each CFG block's statements. The builder has already flattened control flow
	// into blocks and wrapped each branch condition as its own statement, so the per-
	// statement scan attributes every `self.f` write and read to the right program point
	// without re-deriving the branch structure.
	for _, block := range cfg.Blocks {
		for idx, stmt := range block.Stmts {
			col.currentRef = liveness.StmtRef{BlockID: block.ID, StmtIdx: idx}
			// The target of a decomposed assignment is written, not read, so only a
			// bracket key in it is scanned.
			if es, ok := stmt.(*ast.ExprStmt); ok {
				if _, isTarget := decomposed[es.Expr]; isTarget {
					if ix, isIndex := es.Expr.(*ast.IndexExpr); isIndex {
						ix.Index.Accept(col)
					}
					continue
				}
			}
			stmt.Accept(col)
		}
	}
	// A decomposed assignment writes its field at the join, after every branch has run.
	for be, ref := range decomposedRefs {
		name, _ := c.selfFieldName(be.Left)
		col.currentRef = ref
		col.write(be, name)
	}

	info := liveness.AnalyzeMoves(cfg, col.gens)

	// A readonly write the collector did not reach sits in a nested function, which may run
	// after construction, or in unreachable code. A write that follows another write to the
	// same field in one statement is a second write. Any other write the collector reached
	// initializes the field when no path into it has already assigned the field.
	for _, w := range init.writes {
		ref, reached := col.writes[w.node]
		if reached && !col.repeats.Contains(w.node) && info.StateBefore(ref, readonlyIDs[w.field]) == liveness.NotMoved {
			continue
		}
		c.report(&ReadonlyFieldError{Field: w.field, site: w.node.Left})
	}

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
	c           *checker
	currentRef  liveness.StmtRef
	fieldIDs    map[string]liveness.VarID
	readonlyIDs map[string]liveness.VarID
	gens        map[liveness.StmtRef]set.Set[liveness.VarID]
	reads       []initRead
	calls       []initCall
	// writes maps each `self.f = …` assignment the collector reached to its CFG point.
	writes map[*ast.BinaryExpr]liveness.StmtRef
	// repeats holds each readonly-field assignment that follows another write to the same
	// field within one statement, such as the second write in `[self.x = 1, self.x = 2]`.
	repeats set.Set[*ast.BinaryExpr]
}

// write records the assignment e to field at the current statement.
func (col *initCollector) write(e *ast.BinaryExpr, field string) {
	if id, ok := col.readonlyIDs[field]; ok && col.gens[col.currentRef] != nil && col.gens[col.currentRef].Contains(id) {
		col.repeats.Add(e)
	}
	col.gen(field)
	col.writes[e] = col.currentRef
}

// gen records that field is assigned at the current statement, under its fieldIDs entry
// and its readonlyIDs entry. A field absent from both is optional or unknown and is not
// tracked.
func (col *initCollector) gen(field string) {
	if id, ok := col.fieldIDs[field]; ok {
		col.genID(id)
	}
	if id, ok := col.readonlyIDs[field]; ok {
		col.genID(id)
	}
}

// genID records that the synthetic id is assigned at the current statement.
func (col *initCollector) genID(id liveness.VarID) {
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
				col.write(e, name)
				return false
			}
		}
	case *ast.ThrowExpr:
		// A throwing path never completes construction, so it is exempt: mark every
		// required field assigned so the throw does not lower the init state at the join.
		// The thrown value is still scanned for reads. readonlyIDs are left alone, since
		// they record only real writes.
		for _, id := range col.fieldIDs {
			col.genID(id)
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
// string literal, one number literal, or one unique symbol, so `self[k]` with
// `val k = "f"` names f.
//
// `ok` is false when the access names no field of `self`:
//   - `other.f`, whose object is not `self`;
//   - a deeper path like `self.a.b`, whose object is `self.a` rather than `self`;
//   - a bracket key naming no single field.
//
// It reads a key's type from `Info`, so it must run after the constructor body is walked.
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

// ctorInitCtx collects the writes a constructor body makes to the class's own readonly
// fields while the body is walked. body is the class's own instance body.
type ctorInitCtx struct {
	body   *soltype.ObjectType
	writes []readonlyInitWrite
}

// readonlyInitWrite is one write to a readonly field that ctorInitCtx deferred: the field
// name and the assignment.
type readonlyInitWrite struct {
	field string
	node  *ast.BinaryExpr
}

// deferReadonlyWrite records the assignment e as a candidate initializing write when object
// is the bare `self` identifier and the class's own body declares name as a readonly field.
// It reports whether it recorded one. A false result leaves the write to the caller to reject.
func (ctx *ctorInitCtx) deferReadonlyWrite(e *ast.BinaryExpr, object ast.Expr, name string) bool {
	if ctx == nil || !isSelfIdent(object) {
		return false
	}
	prop, ok := ctx.body.Prop(name)
	if !ok || !prop.Readonly {
		return false
	}
	ctx.writes = append(ctx.writes, readonlyInitWrite{field: name, node: e})
	return true
}
