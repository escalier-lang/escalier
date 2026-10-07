package dts_to_esc

import (
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
// name and its signature, or a nil signature for a member that is not a method. An arm is
// dropped only when another method of the same name remains, so a declaration never loses
// the member whole.
func dropGuardMembers[M any](members []M, method func(M) (string, *dts_parser.MethodSignature)) []M {
	plain := map[string]int{}
	for _, m := range members {
		if name, sig := method(m); sig != nil && name != "" && !isGuardOverload(sig) {
			plain[name]++
		}
	}
	kept := members[:0:0]
	for _, m := range members {
		if name, sig := method(m); sig != nil && name != "" && plain[name] > 0 && isGuardOverload(sig) {
			continue
		}
		kept = append(kept, m)
	}
	return kept
}

// isGuardOverload reports whether sig takes a callback whose return is a type guard on one
// of sig's own type parameters, as `predicate: (value: T) => value is S` is on `S`.
func isGuardOverload(sig *dts_parser.MethodSignature) bool {
	for _, tp := range sig.TypeParams {
		for _, p := range sig.Params {
			if guardsOn(p.Type, tp.Name.Name) {
				return true
			}
		}
	}
	return false
}

// guardsOn reports whether t is a function type, possibly parenthesized, whose return is
// a `value is X` guard with X naming name.
func guardsOn(t dts_parser.TypeAnn, name string) bool {
	switch t := t.(type) {
	case *dts_parser.ParenthesizedType:
		return guardsOn(t.Type, name)
	case *dts_parser.FunctionType:
		pred, ok := t.ReturnType.(*dts_parser.TypePredicate)
		return ok && !pred.Asserts && pred.Type != nil && countTypeRefsInTypeAnn(pred.Type, name) > 0
	}
	return false
}
