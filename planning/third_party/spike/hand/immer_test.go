//go:build spike

package hand

// immer@11.2.9, lowered from the shipped src TypeScript. `produce` is the
// arrow function in the `produce` field of `class Immer`, exported as
// `immer.produce` off a module-level instance. The helpers in
// src/utils/common.ts are `let` bindings holding arrow functions, or aliases of
// builtins such as `getPrototypeOf = O.getPrototypeOf`, which resolve to the
// builtin.

func init() {
	packages = append(packages, pkg{
		name:    "immer",
		version: "11.2.9",
		funcs: []*fn{
			impl("isObjectish", "target").ret(Lit()),

			// die(error, ...args) formats a message from a module-level table
			// whose entries are strings or functions.
			impl("die", "error", "args").
				let("e", Prop(V("errors"))).
				to("f", "isFunction", Lit(), V("e")).
				unresolved("msg", "property-value:e.apply(null, args)", V("e"), Lit(), Prop(V("args"))).
				throw(Alloc()).
				throw(Alloc()),
			impl("isDraft", "value").ret(Lit()),
			impl("isMap", "target").ret(Lit()),
			impl("isSet", "target").ret(Lit()),
			impl("isFunction", "target").ret(Lit()),

			// isPlainObject(value)
			impl("isPlainObject", "value").
				to("o", "isObjectish", Lit(), V("value")).
				builtin("proto", "Object.getPrototypeOf", Lit(), V("value")).
				builtin("h", "Object.prototype.hasOwnProperty", V("proto"), Lit()).
				let("Ctor", Prop(V("proto"))).
				to("f", "isFunction", Lit(), V("Ctor")).
				builtin("cs", "WeakMap.prototype.get", V("cachedCtorStrings"), V("Ctor")).
				builtin("s", "Function.prototype.toString", V("Ctor")).
				builtin("", "WeakMap.prototype.set", V("cachedCtorStrings"), V("Ctor"), V("s")).
				ret(Lit()),

			// isDraftable(value)
			impl("isDraftable", "value").
				to("a", "isPlainObject", Lit(), V("value")).
				builtin("b", "Array.isArray", Lit(), V("value")).
				to("c", "isMap", Lit(), V("value")).
				to("d", "isSet", Lit(), V("value")).
				ret(Lit()),

			// getArchtype(thing)
			impl("getArchtype", "thing").
				let("state", Prop(V("thing"))).
				builtin("a", "Array.isArray", Lit(), V("thing")).
				to("m", "isMap", Lit(), V("thing")).
				to("s", "isSet", Lit(), V("thing")).
				ret(Prop(V("state"))).
				ret(Lit()),

			// isFrozen(obj)
			impl("isFrozen", "obj").
				to("o", "isObjectish", Lit(), V("obj")).
				builtin("f", "Object.isFrozen", Lit(), V("obj")).
				ret(Lit()),

			// each(obj, iter, strict = true)
			impl("each", "obj", "iter", "strict").
				to("t", "getArchtype", Lit(), V("obj")).
				builtin("k1", "Reflect.ownKeys", Lit(), V("obj")).
				builtin("k2", "Object.keys", Lit(), V("obj")).
				let("keys", V("k1")).let("keys", V("k2")).
				// keys.forEach(key => { iter(key, obj[key], obj) })
				let("key", Unknown()).
				call("", "iter", V("key"), Prop(V("obj")), V("obj")).
				builtin("", "Array.prototype.forEach", V("keys"), V("fn:each/closure")).
				// obj.forEach((entry, index) => iter(index, entry, obj)), where
				// obj is `any`.
				unresolved("", "untyped:obj.forEach(...)", V("obj"), V("fn:each/closure2")).
				ret(Lit()),

			// freeze(obj, deep = false)
			impl("freeze", "obj", "deep").
				to("a", "isFrozen", Lit(), V("obj")).
				to("b", "isDraft", Lit(), V("obj")).
				to("c", "isDraftable", Lit(), V("obj")).
				ret(V("obj")).
				to("t", "getArchtype", Lit(), V("obj")).
				builtin("", "Object.defineProperties", Lit(), V("obj"), Alloc(V("dontMutateMethodOverride"))).
				builtin("", "Object.freeze", Lit(), V("obj")).
				// each(obj, (_key, value) => { freeze(value, true) }, false)
				let("value", Unknown()).
				to("", "freeze", Lit(), V("value"), Lit()).
				to("", "each", Lit(), V("obj"), V("fn:freeze/closure"), Lit()).
				ret(V("obj")),

			// original(value)
			impl("original", "value").
				to("d", "isDraft", Lit(), V("value")).
				to("", "die", Lit(), Lit(), Alloc(V("value"))).
				ret(Prop(Prop(V("value")))),

			// Immer.produce = (base, recipe, patchListener) => {...}. The
			// curried branch returns a closure and is lowered as an allocation.
			impl("Immer.produce", "base", "recipe", "patchListener").
				to("fb", "isFunction", Lit(), V("base")).
				ret(V("fn:curriedProduce")).
				to("", "die", Lit(), Lit(), Alloc()).
				to("dr", "isDraftable", Lit(), V("base")).
				// enterScope, createProxy and processResult build the draft
				// and the result. They are not lowered, because the recipe
				// callback below already blocks the member.
				unresolved("scope", "not-lowered:enterScope(this)", Lit(), V("this$")).
				unresolved("proxy", "not-lowered:createProxy(scope, base, undefined)", Lit(), V("scope"), V("base"), Lit()).
				call("result", "recipe", V("proxy")).
				unresolved("", "not-lowered:usePatchesInScope(scope, patchListener)", Lit(), V("scope"), V("patchListener")).
				unresolved("pr", "not-lowered:processResult(result, scope)", Lit(), V("result"), V("scope")).
				ret(V("pr")).
				to("oi", "isObjectish", Lit(), V("base")).
				call("result2", "recipe", V("base")).
				let("r", V("result2")).let("r", V("base")).let("r", Lit()).
				to("", "freeze", Lit(), V("r"), Lit()).
				call("", "patchListener", Alloc(), Alloc()).
				ret(V("r")),
		},
		members: []member{
			{name: "produce", impl: "Immer.produce", params: []string{obj, cb, cb}, expect: "p0:&mut p1:& p2:&"},
			{name: "freeze", impl: "freeze", params: []string{obj, prim}, expect: "p0:&mut p1:prim"},
			{name: "original", impl: "original", params: []string{obj}, expect: "p0:&"},
			{name: "isDraft", impl: "isDraft", params: []string{obj}, expect: "p0:&"},
			{name: "isDraftable", impl: "isDraftable", params: []string{obj}, expect: "p0:&"},
		},
	})
}
