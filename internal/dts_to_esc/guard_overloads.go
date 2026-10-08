package dts_to_esc

import (
	"slices"

	"github.com/escalier-lang/escalier/internal/dts_parser"
)

// guard_overloads.go drops a method overload that exists only to narrow through a
// type-guard callback, such as the first `filter` of TypeScript's `Array<T>`:
//
//	filter<S extends T>(predicate: (value: T, index: number, array: T[]) => value is S): S[];
//	filter(predicate: (value: T, index: number, array: T[]) => unknown): T[];
//
// Escalier has no type predicates, so the converter lowers `value is S` to `boolean`.
// That leaves nothing tying `S` to the predicate, and the converted arm lets a caller
// pick any `S` below `T`, so `xs.filter<1>(fn (x) { return true })` would yield an
// `Array<1>`. The sibling arm covers every call the guard arm took. The guard arm's
// bound also names the declaration's `T` in an input position, which keeps `Array`
// from being covariant in `T`.
//
// TODO(#229): convert the guard once Escalier has type predicates, and stop dropping
// the arm.

// dropGuardOverloads removes every guard overload from the interfaces and classes the
// inputs declare, descending into namespaces, modules, and `declare global` blocks. It
// rewrites the parsed modules in place.
func dropGuardOverloads(inputs []LibInput) {
	for _, in := range inputs {
		in.Module.Statements = dropGuardOverloadsIn(in.Module.Statements)
	}
}

func dropGuardOverloadsIn(stmts []dts_parser.Statement) []dts_parser.Statement {
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *dts_parser.InterfaceDecl:
			s.Members = dropGuardMembers(s.Members, func(m dts_parser.InterfaceMember) (string, *dts_parser.MethodSignature) {
				sig, ok := m.(*dts_parser.MethodSignature)
				if !ok {
					return "", nil
				}
				return propertyKeyName(sig.Name), sig
			})
		case *dts_parser.ClassDecl:
			s.Members = dropGuardMembers(s.Members, func(m dts_parser.ClassMember) (string, *dts_parser.MethodSignature) {
				md, ok := m.(*dts_parser.MethodDecl)
				if !ok {
					return "", nil
				}
				return propertyKeyName(md.Name), &dts_parser.MethodSignature{
					Name: md.Name, TypeParams: md.TypeParams, Params: md.Params, ReturnType: md.ReturnType,
				}
			})
		case *dts_parser.NamespaceDecl:
			s.Statements = dropGuardOverloadsIn(s.Statements)
		case *dts_parser.ModuleDecl:
			s.Statements = dropGuardOverloadsIn(s.Statements)
		case *dts_parser.GlobalDecl:
			s.Statements = dropGuardOverloadsIn(s.Statements)
		}
	}
	return stmts
}

// dropGuardMembers returns members without its guard overloads. method returns a member's
// name and its signature, or a nil signature for a member that is not a method. A guard
// overload is dropped only when a plain overload of the same name covers it, as
// coversGuard decides, so every call the guard overload took still has an overload.
func dropGuardMembers[M any](members []M, method func(M) (string, *dts_parser.MethodSignature)) []M {
	plain := map[string][]*dts_parser.MethodSignature{}
	for _, m := range members {
		if name, sig := method(m); sig != nil && name != "" && !isGuardOverload(sig) {
			plain[name] = append(plain[name], sig)
		}
	}
	kept := members[:0:0]
	for _, m := range members {
		if name, sig := method(m); sig != nil && name != "" && isGuardOverload(sig) &&
			slices.ContainsFunc(plain[name], func(p *dts_parser.MethodSignature) bool { return coversGuard(p, sig) }) {
			continue
		}
		kept = append(kept, m)
	}
	return kept
}

// isGuardOverload reports whether sig takes a callback whose return is a type guard on one
// of sig's own type parameters, as `predicate: (value: T) => value is S` is on `S`.
func isGuardOverload(sig *dts_parser.MethodSignature) bool {
	for _, p := range sig.Params {
		if guardsOnOwnParam(sig, p.Type) {
			return true
		}
	}
	return false
}

// guardsOnOwnParam reports whether t is a type-guard callback on one of sig's own type
// parameters.
func guardsOnOwnParam(sig *dts_parser.MethodSignature, t dts_parser.TypeAnn) bool {
	for _, tp := range sig.TypeParams {
		if guardsOn(t, tp.Name.Name) {
			return true
		}
	}
	return false
}

// coversGuard reports whether plain takes the calls the guard overload guard takes, in the
// shape TypeScript pairs them:
//
//	filter<S extends T>(predicate: (value: T) => value is S): S[];
//	filter(predicate: (value: T) => unknown): T[];
//
// The two take the same number of parameters, and wherever guard takes a guard callback,
// plain takes a callback of the same arity. A callback returning `unknown` accepts any
// predicate, so plain then accepts every argument guard did.
func coversGuard(plain, guard *dts_parser.MethodSignature) bool {
	if len(plain.Params) != len(guard.Params) {
		return false
	}
	for i, gp := range guard.Params {
		if !guardsOnOwnParam(guard, gp.Type) {
			continue
		}
		gf, pf := funcType(gp.Type), funcType(plain.Params[i].Type)
		if gf == nil || pf == nil || len(gf.Params) != len(pf.Params) {
			return false
		}
	}
	return true
}

// funcType returns t as a function type, looking through parentheses, or nil when t is
// not one.
func funcType(t dts_parser.TypeAnn) *dts_parser.FunctionType {
	switch t := t.(type) {
	case *dts_parser.ParenthesizedType:
		return funcType(t.Type)
	case *dts_parser.FunctionType:
		return t
	}
	return nil
}

// guardsOn reports whether t is a function type, possibly parenthesized, whose return is
// a `value is X` guard with X naming name.
func guardsOn(t dts_parser.TypeAnn, name string) bool {
	f := funcType(t)
	if f == nil {
		return false
	}
	pred, ok := f.ReturnType.(*dts_parser.TypePredicate)
	return ok && !pred.Asserts && pred.Type != nil && countTypeRefsInTypeAnn(pred.Type, name) > 0
}
