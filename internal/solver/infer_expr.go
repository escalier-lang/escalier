package solver

import (
	"fmt"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// inferLiteral types a literal expression as its singleton soltype.LitType and
// records it in Info. M1's soltype.Lit set is num/str/bool only; the remaining
// ast literal kinds (regex, bigint, null, undefined) fall through to the M2
// subset guard until later milestones extend soltype.Lit (§ soltype/type.go
// Prim/Lit note).
func (c *checker) inferLiteral(e *ast.LiteralExpr) soltype.Type {
	var lit soltype.Lit
	switch l := e.Lit.(type) {
	case *ast.NumLit:
		lit = &soltype.NumLit{Value: l.Value}
	case *ast.StrLit:
		lit = &soltype.StrLit{Value: l.Value}
	case *ast.BoolLit:
		lit = &soltype.BoolLit{Value: l.Value}
	default:
		return c.reportUnsupported(e.Lit)
	}
	t := &soltype.LitType{Lit: lit}
	c.recordType(e, t)
	c.recordProv(t, e, LiteralInference)
	return t
}

// inferIdent resolves a value-position identifier through the scope chain — the
// production form of the spike's *Var case crossed with design-notes §"The
// constraint-generating AST walk". M3 (PR1) slots in the instantiation hook M2
// left as a TODO: an ordinary binding instantiates its sole scheme at the current
// level, so a polymorphic let gives each use fresh variables (a MonoScheme
// instantiates to itself, preserving M2's behavior for the monomorphic bindings).
//
// An overloaded binding's value-position type is the intersection of its arms,
// resolved through the probe — that is PR6 and unreachable today (M2 rejects
// multi-FuncDecl names, so no overloaded binding is ever bound), so PR1 asserts
// the single-scheme invariant rather than branching on it.
//
// A namespace name in value position can only fail in M2: there is no legal
// namespace-member position yet (MemberExpr is value-only and there is no
// IndexExpr), so raising NamespaceUsedAsValueError on any namespace name is
// correct here. M4 moves that error to the value-position consumer once
// qualified Foo.bar access lands.
func (c *checker) inferIdent(scope *Scope, lvl int, e *ast.IdentExpr) soltype.Type {
	// Any binding still in scope has at least one scheme: inferComponent pre-binds
	// each group member to a fresh MonoScheme, and on failure deletes the binding
	// (scope.removeValue) rather than leaving it with an empty Schemes slice. So the
	// len > 0 check below should never fail in practice — but Schemes is a slice, not
	// a guaranteed-non-empty field, so we guard it anyway: a malformed empty binding
	// degrades to an unknown-identifier error here instead of panicking on Schemes[0].
	//
	// PR1 never builds an overloaded binding; the IsOverloaded value-position branch
	// (arm intersection) is PR6.
	if b, ok := scope.GetValue(e.Name); ok && len(b.Schemes) > 0 {
		t := c.instantiate(b.Schemes[0], lvl)
		c.recordType(e, t)
		return t
	}
	if _, ok := scope.GetNamespace(e.Name); ok {
		return c.report(&NamespaceUsedAsValueError{Ident: e})
	}
	return c.report(&UnknownIdentifierError{Ident: e})
}

// astKind returns a short surface name for any AST node — an expression,
// literal, declaration, or pattern — used in the M2 subset-guard error messages.
// It strips the leading "*ast." from the Go type name so e.g. *ast.BinaryExpr
// renders as "BinaryExpr". One helper serves every guard site (inferExpr,
// inferLiteral, inferDeclDef) so the format lives in a single place.
func astKind(n any) string {
	return strings.TrimPrefix(fmt.Sprintf("%T", n), "*ast.")
}

// inferFuncExpr types a function expression as a soltype.FuncType and records it
// in Info. It delegates to inferFunc, the shared core also used by inferFuncDecl
// (the plan's "reuse inferFuncExpr on the decl's sig+body", factored so neither
// side owns the other). M2 is monomorphic: any TypeParams on the signature are a
// generic function (M3) and are diagnosed as unsupported by inferFunc, not
// silently erased; an un-annotated param simply gets a fresh var, which
// coalesces to unknown/never at render time rather than a <T0> quantifier
// (generalization is M3).
func (c *checker) inferFuncExpr(scope *Scope, lvl int, e *ast.FuncExpr) soltype.Type {
	t := c.inferFunc(scope, lvl, e.FuncSig, e.Body, e)
	c.recordType(e, t)
	return t
}

// inferFunc is the shared function-typing core for FuncExpr and FuncDecl. It
// opens a child scope, binds each param (its annotation resolved to a soltype,
// or a fresh var when un-annotated), types the body block in that scope, and
// builds the n-ary soltype.FuncType. When the signature carries a return
// annotation the inferred body type is constrained against it and the annotated
// type becomes the function's return type; otherwise the body type is the
// return type directly. A bodyless (declare/ambient) function adopts its return
// annotation without constraining anything. node supplies the span stamped onto
// a return-annotation constraint failure.
func (c *checker) inferFunc(scope *Scope, lvl int, sig ast.FuncSig, body *ast.Block, node ast.Node) *soltype.FuncType {
	if len(sig.TypeParams) > 0 {
		// Generic functions (fn <T>(...)) need type schemes / generalization,
		// which are M3. The FuncExpr/FuncDecl kind itself is supported — it is the
		// type-param feature that is not — so diagnose it as an unsupported feature
		// (blaming the function node) rather than silently erasing the params, then
		// continue inferring monomorphically.
		c.reportUnsupportedFeature(node, "TypeParam")
	}
	fnScope := scope.Child()
	params := make([]*soltype.FuncParam, len(sig.Params))
	for i, p := range sig.Params {
		pt := c.paramType(p, lvl)
		name, ok := identPatName(p.Pattern)
		if !ok {
			// Destructuring params (TuplePat/ObjectPat) need record/tuple types —
			// they arrive in M4. M2 binds IdentPat only. A non-nil pattern blames
			// itself (its own narrower span); a pattern-less param (not reachable
			// from the real parser) blames the enclosing function, since Param.Span()
			// dereferences the nil pattern — honoring M2's "never a panic" guarantee.
			if p.Pattern != nil {
				c.reportUnsupported(p.Pattern)
			} else {
				c.reportUnsupported(node)
			}
			name = fmt.Sprintf("arg%d", i)
		} else if p.TypeAnn == nil {
			// An un-annotated param's type is the fresh var minted here, so a
			// param-type mismatch blames the param. Record against the pattern (an
			// ast.Node; *ast.Param is not) — for an IdentPat its span is the param's.
			// An annotated param's blame instead rides on its annotation, recorded
			// by resolveTypeAnn.
			c.recordProv(pt, p.Pattern, ParamBinding)
		}
		// A parameter binding never generalizes — its var is fixed for the body — so
		// it is a MonoScheme; instantiate returns pt unchanged at every use.
		fnScope.defineValue(name, ValueBinding{Schemes: []TypeScheme{monoScheme(pt)}})
		// An `x?` parameter (parsed onto ast.Param.Optional) lowers the function's
		// `required` count without dropping the param — carried onto the soltype so
		// the accept-set rule and the printer (x?: T) see it. KNOWN GAP (M6): the
		// in-body binding keeps the param's declared type (pt), NOT widened to
		// `pt | undefined`, so a body that reads an omitted optional sees it at the
		// narrower type. Widening needs undefined/unions (M6); M3 has neither.
		params[i] = &soltype.FuncParam{Pattern: &soltype.IdentPat{Name: name}, Type: pt, Optional: p.Optional}
	}

	var ret soltype.Type = &soltype.Void{}
	hasBody := body != nil
	if hasBody {
		ret = c.inferBlock(fnScope, lvl, body)
	}
	if sig.Return != nil {
		// Skip the check and adopt-the-annotation when the return annotation is
		// unsupported (ok=false): resolveTypeAnn already reported it and handed back
		// a `never` placeholder, so constraining the body `<: never` would cascade a
		// spurious error and adopting it would poison the return type. Keep the
		// inferred body type instead (error recovery).
		if annT, ok := c.resolveTypeAnn(sig.Return); ok {
			// Only check the inferred body against the declared return when there IS a
			// body. A bodyless (declare/ambient) function has no body to constrain, so
			// it simply adopts the annotation; constraining the synthetic Void return
			// would raise a spurious `void <: T` error.
			if hasBody {
				c.constrain(node, ret, annT) // body <: declared return
			}
			ret = annT
		} else if !hasBody {
			// Bodyless function with an unsupported return annotation: there is no
			// body to recover the return type from, and leaving the synthetic Void
			// would falsely signal "returns nothing" to callers. Fall back to
			// unknown (⊤) — the honest "couldn't resolve the declared return"
			// recovery. (A fresh var would coalesce to `never` in return position,
			// which is worse.)
			ret = &soltype.UnknownType{}
		}
	}
	// A bare function value is EXACT (accept-set [required, len(Params)]): it rejects
	// extra arguments. A trailing `...` in the signature (sig.Inexact) marks it
	// inexact — it tolerates extra args when used as a callback (#677 §4.1), accept
	// [required, ∞). Note exactness governs callback subtyping, not direct calls: an
	// inexact value still rejects extras at a visible call site (the inferCall lint).
	ft := &soltype.FuncType{Params: params, Ret: ret, Exact: !sig.Inexact}
	// Record the function's own type against its node so a function flowing into a
	// non-function requirement blames the function, and FuncArityMismatchError can
	// carry a "defined here" related span. (For a named callee this raw FuncType is
	// re-minted by coalescing at binding time, so the entry is exact for inline
	// callees; M3's FromInstantiation makes named-callee blame precise.)
	c.recordProv(ft, node, FuncInference)
	return ft
}

// paramType resolves a param's type: its annotation when present, else a fresh
// inference variable at the current level (the spike's "fresh var per param").
// An unsupported annotation (ok=false) already reported its own error; the param
// adopts a fresh var rather than the `never` placeholder so the body and any
// call site recover against an unconstrained variable instead of cascading
// `<: never` failures.
func (c *checker) paramType(p *ast.Param, lvl int) soltype.Type {
	if p.TypeAnn != nil {
		if t, ok := c.resolveTypeAnn(p.TypeAnn); ok {
			return t
		}
	}
	return c.freshAt(lvl)
}

// inferCall types a function application. It types the callee and each argument,
// allocates a fresh result var, and constrains callee <: fn(args) -> res — the
// production form of the spike's *App case. The result var picks up the callee's
// return type (covariantly) and renders as that once coalesced; an arity or
// argument mismatch surfaces as a constraint error stamped with the call's span.
//
// Error recovery: a call to a known function still yields that function's
// declared return type even when the arguments don't match, so a downstream
// expression sees the real return type rather than a poisoned `never`. constrain
// short-circuits its FuncType arity arm before propagating the return into res,
// so the return is wired through directly here. The callee is concrete either as a
// bare FuncType (an inline callee) OR as a var whose lower bound is a FuncType (a
// named/generalized callee, which inferIdent now resolves through instantiate — see
// concreteFunc); both recover, so recovery no longer regresses for named callees.
//
// PR4 adds two #677 pieces: an EXACT all-required call demand, and the extra-arg
// lint that rejects passing more arguments than a concrete callee declares.
func (c *checker) inferCall(scope *Scope, lvl int, e *ast.CallExpr) soltype.Type {
	callee := c.inferExpr(scope, lvl, e.Callee)
	args := make([]*soltype.FuncParam, len(e.Args))
	for i, a := range e.Args {
		args[i] = &soltype.FuncParam{Type: c.inferExpr(scope, lvl, a)}
	}
	res := c.freshAt(lvl)
	c.recordProv(res, e, Application)

	// Extra-arg lint (#677 §4.2.3): a DIRECT call rejects more arguments than the
	// callee declares — for exact AND inexact callees alike. This is a call-site
	// check the subtype lattice deliberately does NOT model: an inexact callee
	// tolerates extras as a *callback* (accept-set [required, ∞)), but supplying
	// extras to a call you can see is treated as a mistake. It fires only when the
	// callee is concrete; for a deferred (var) callee it is best-effort skipped while
	// "too few / required" still flows through the constraint below.
	fn, isConcrete := concreteFunc(callee)
	demand := args
	if isConcrete && len(args) > len(fn.Params) {
		// Hand the constraint only the arity-matched prefix so the EXACT synth's
		// accept-set gate does not ALSO report arity — the lint owns the single,
		// uniform too-many message; the constraint does pure type-flow.
		c.errs = append(c.errs, &TooManyArgsError{Call: e, Fn: fn})
		demand = args[:len(fn.Params)]
	}

	// EXACT + all-required call demand: accept(synth) = [N, N] (N = arg count), so
	// the constraint reads exactly "callee accepts being called with N args"
	// (required(callee) <= N <= upper(callee)). An INEXACT synth (the Go zero value)
	// would have accept [N, ∞), forcing upper(callee) = ∞ and rejecting every call to
	// an exact function — so Exact:true here is load-bearing, not decorative.
	callShape := &soltype.FuncType{Params: demand, Ret: res, Exact: true}
	// Record the synthesized call-shape against the CallExpr so FuncArityMismatchError
	// (the surviving "too few / required" path) resolves its blame to the call.
	c.recordProv(callShape, e, CallShape)
	c.constrain(e, callee, callShape)
	if isConcrete {
		c.constrain(e, fn.Ret, res)
	}
	c.recordType(e, res)
	return res
}

// concreteFunc resolves a callee to its concrete FuncType, used to recover a
// call's return type. The callee is either a FuncType directly (an inline callee)
// or a var whose first FuncType lower bound is the function (a named/generalized
// callee, since inferIdent returns instantiate(scheme) — a fresh var). Looking
// through the var matters because otherwise an arity-mismatched call to a named
// function would lose return recovery and yield `never`.
//
// ok=false means no concrete func was found (e.g. a deferred callee with no lower
// bound yet) — the caller skips return recovery. PR1 bindings have at most one
// func lower bound; overload sets (PR6) resolve through resolveOverload, not here.
func concreteFunc(t soltype.Type) (*soltype.FuncType, bool) {
	switch t := t.(type) {
	case *soltype.FuncType:
		return t, true
	case *soltype.TypeVarType:
		for _, lb := range t.LowerBounds {
			if fn, ok := lb.(*soltype.FuncType); ok {
				return fn, true
			}
		}
	}
	return nil, false
}

// inferTuple types a tuple literal as a soltype.TupleType of its element types
// and records it in Info. Elements are typed left-to-right in the current scope.
// A spread element ([...xs]) is an ArraySpreadExpr, which is not in the M2 walk,
// so inferExpr reports it as unsupported and contributes a never placeholder in
// its slot — the tuple is still built (error already accumulated), never a panic.
func (c *checker) inferTuple(scope *Scope, lvl int, e *ast.TupleExpr) soltype.Type {
	elems := make([]soltype.Type, len(e.Elems))
	for i, el := range e.Elems {
		elems[i] = c.inferExpr(scope, lvl, el)
	}
	t := &soltype.TupleType{Elems: elems}
	c.recordType(e, t)
	c.recordProv(t, e, TupleElem)
	return t
}

// inferObject types an object literal as a soltype.RecordType. M2 covers the
// basic case only: a `name: value` property with a static (identifier or string)
// key. The forms it does not cover each report an UnsupportedNodeError and are
// skipped rather than panicking (the deeper object system is M4):
//   - spreads ({...o}) and method/constructor elements,
//   - computed ({[k]: v}) and numeric ({0: v}) keys,
//   - shorthand ({x}, i.e. a property with no value).
//
// Usage-inference depth (e.g. inferring an open record from how a value is used)
// is explicitly M4; M2 builds the closed record the literal spells out.
//
// Duplicate keys follow JavaScript semantics: the last value wins, keeping the
// field at its first position ({a: 1, b: 2, a: 3} ⇒ {a: 3, b: 2}). This keeps
// field names unique, the invariant RecordType.Field / equalType rely on.
func (c *checker) inferObject(scope *Scope, lvl int, e *ast.ObjectExpr) soltype.Type {
	fields := make([]*soltype.RecordField, 0, len(e.Elems))
	pos := make(map[string]int, len(e.Elems)) // field name → index in fields, for last-wins dedup
	for _, elem := range e.Elems {
		prop, ok := elem.(*ast.PropertyExpr)
		if !ok {
			// ObjSpreadExpr, CallableExpr (method), ConstructorExpr — all M4.
			c.reportUnsupported(elem)
			continue
		}
		if prop.Value == nil {
			// Shorthand ({x}) needs the ident's binding folded in as the value — M4.
			c.reportUnsupported(prop)
			continue
		}
		name, ok := objKeyName(prop.Name)
		if !ok {
			// Computed/numeric keys carry no static field name — M4. Blame the key
			// itself (its own narrower span), not the whole property.
			c.reportUnsupported(prop.Name)
			continue
		}
		ft := c.inferExpr(scope, lvl, prop.Value)
		if i, dup := pos[name]; dup {
			fields[i] = &soltype.RecordField{Name: name, Type: ft} // last value wins, first position kept
			continue
		}
		pos[name] = len(fields)
		fields = append(fields, &soltype.RecordField{Name: name, Type: ft})
	}
	t := &soltype.RecordType{Fields: fields}
	c.recordType(e, t)
	c.recordProv(t, e, ObjectField)
	return t
}

// inferMember types a field read (recv.prop). It types the receiver, allocates a
// fresh result var, and constrains recv <: {prop: res} — the basic form from the
// plan's §3.2 table. The record <: record arm of constrain lowers res from the
// receiver's matching field (so res coalesces to that field's type); a receiver
// missing the field surfaces as a MissingPropertyError stamped with the member's
// span. Optional chaining (recv?.prop) needs union/undefined handling and is M6.
func (c *checker) inferMember(scope *Scope, lvl int, e *ast.MemberExpr) soltype.Type {
	if e.OptChain {
		// Optional chaining (recv?.prop) is wholesale unsupported in M2; report it
		// up front and do NOT descend into the receiver, so a single diagnostic
		// stands for the construct instead of cascading the receiver's errors. The
		// MemberExpr kind is supported — it is the optional-chain feature that is
		// not — so this is an UnsupportedFeatureError blaming the member.
		return c.reportUnsupportedFeature(e, "OptionalChain")
	}
	recv := c.inferExpr(scope, lvl, e.Object)
	if e.Prop == nil || e.Prop.Name == "" {
		// A malformed `recv.` with no valid property name: the parser already
		// reported the missing identifier, so constraining recv <: {"": res} here
		// would only layer a spurious "object is missing property: " on top. Yield
		// a never placeholder without reporting or constraining.
		t := soltype.Type(&soltype.NeverType{})
		c.recordType(e, t)
		return t
	}
	res := c.freshAt(lvl)
	// Record the fresh result var against the .prop IDENTIFIER (not the whole
	// MemberExpr), so a missing-property read blames the property (.foo), not the
	// receiver. The member-requirement record {prop: res} is deliberately NOT
	// recorded — MissingPropertyError blames this inner res var, so the record
	// would be a dead entry (§3.3).
	c.recordProv(res, e.Prop, MemberAccess)
	c.constrain(e, recv, &soltype.RecordType{Fields: []*soltype.RecordField{{Name: e.Prop.Name, Type: res}}})
	c.recordType(e, res)
	return res
}

// objKeyName reads the static field name of an object-literal key. M2 records
// have string field names, so an identifier label or a string-literal key maps
// to a field; numeric and computed keys do not (they need M4's wider object
// system) and return false so the caller can raise a structured error.
func objKeyName(k ast.ObjKey) (string, bool) {
	switch k := k.(type) {
	case *ast.IdentExpr:
		return k.Name, true
	case *ast.StrLit:
		return k.Value, true
	default:
		return "", false
	}
}

// identPatName reads the name of an IdentPat. M2 binds IdentPat-only patterns
// (mirroring M1's IdentPat-only FuncParam); the comma-ok form lets callers raise
// a structured error for the destructuring patterns deferred to M4.
func identPatName(pat ast.Pat) (string, bool) {
	if ip, ok := pat.(*ast.IdentPat); ok {
		return ip.Name, true
	}
	return "", false
}
