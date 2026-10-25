//go:build spike

package hand

// lodash-es@4.18.1, lowered from the published ESM files. The package ships no
// types of its own, so parameters are untyped inside the package and the
// member parameter kinds come from @types/lodash-es@4.17.12.
//
// `Array(n)` is lowered as an allocation, since a front end recognizes the
// builtin constructor whether or not `new` is written. `f.call(x, ...)` on a
// module-level alias of a builtin, such as `hasOwnProperty.call(value, key)`,
// is lowered as a call to that builtin with `x` as its receiver.

func init() {
	packages = append(packages, pkg{
		name:    "lodash-es",
		version: "4.18.1",
		funcs: []*fn{
			impl("isObjectLike", "value").ret(Lit()),
			impl("isObject", "value").ret(Lit()),
			impl("isLength", "value").ret(Lit()),
			impl("eq", "value", "other").ret(Lit()),

			// isIndex(value, length): reIsUint.test(value)
			impl("_isIndex", "value", "length").
				builtin("", "RegExp.prototype.test", G("reIsUint"), V("value")).
				ret(Lit()),

			// getRawTag(value) masks Symbol.toStringTag on the value itself
			// while it reads the raw tag, then restores it.
			impl("_getRawTag", "value").
				builtin("isOwn", "Object.prototype.hasOwnProperty", V("value"), Lit()).
				let("tag", Prop(V("value"))).
				set(V("value"), Lit()).
				builtin("result", "Object.prototype.toString", V("value")).
				set(V("value"), V("tag")).
				call("", "DeletePropertyOrThrow", V("value"), Lit()).
				ret(V("result")),

			impl("_objectToString", "value").
				builtin("r", "Object.prototype.toString", V("value")).
				ret(V("r")),

			// baseGetTag(value)
			impl("_baseGetTag", "value").
				builtin("o", "Object", Lit(), V("value")).
				to("a", "_getRawTag", Lit(), V("value")).
				to("b", "_objectToString", Lit(), V("value")).
				ret(Lit()),

			impl("isSymbol", "value").
				to("a", "isObjectLike", Lit(), V("value")).
				to("b", "_baseGetTag", Lit(), V("value")).
				ret(Lit()),

			impl("isFunction", "value").
				to("a", "isObject", Lit(), V("value")).
				to("tag", "_baseGetTag", Lit(), V("value")).
				ret(Lit()),

			impl("isArrayLike", "value").
				to("a", "isLength", Lit(), Prop(V("value"))).
				to("b", "isFunction", Lit(), V("value")).
				ret(Lit()),

			// isKey(value, object)
			impl("_isKey", "value", "object").
				builtin("a", "Array.isArray", Lit(), V("value")).
				to("s", "isSymbol", Lit(), V("value")).
				builtin("p", "RegExp.prototype.test", G("reIsPlainProp"), V("value")).
				builtin("d", "RegExp.prototype.test", G("reIsDeepProp"), V("value")).
				builtin("o", "Object", Lit(), V("object")).
				ret(Lit()),

			// arrayMap(array, iteratee)
			impl("_arrayMap", "array", "iteratee").
				let("result", Alloc()).
				call("x", "iteratee", Prop(V("array")), Lit(), V("array")).
				set(V("result"), V("x")).
				ret(V("result")),

			// baseToString(value)
			impl("_baseToString", "value").
				ret(V("value")).
				builtin("a", "Array.isArray", Lit(), V("value")).
				to("m", "_arrayMap", Lit(), V("value"), G("fn:_baseToString")).
				ret(Lit()).
				to("s", "isSymbol", Lit(), V("value")).
				builtin("t", "Symbol.prototype.toString", V("value")).
				ret(Lit()).
				ret(Lit()),

			impl("toString", "value").
				to("r", "_baseToString", Lit(), V("value")).
				ret(Lit()).ret(V("r")),

			// castPath(value, object)
			impl("_castPath", "value", "object").
				builtin("a", "Array.isArray", Lit(), V("value")).
				ret(V("value")).
				to("k", "_isKey", Lit(), V("value"), V("object")).
				to("s", "toString", Lit(), V("value")).
				// stringToPath is `memoizeCapped(function (string) {...})`, a
				// value computed at module load rather than a function the
				// package declares.
				unresolved("p", "package-value:stringToPath(toString(value))", Lit(), V("s")).
				ret(Alloc(V("value"))).
				ret(V("p")),

			// toKey(value)
			impl("_toKey", "value").
				to("s", "isSymbol", Lit(), V("value")).
				ret(V("value")).
				ret(Lit()),

			// baseGet(object, path)
			impl("_baseGet", "object", "path").
				to("p", "_castPath", Lit(), V("path"), V("object")).
				let("path", V("p")).
				to("k", "_toKey", Lit(), Prop(V("path"))).
				let("object", Prop(V("object"))).
				ret(V("object")).
				ret(Lit()),

			// get(object, path, defaultValue)
			impl("get", "object", "path", "defaultValue").
				to("result", "_baseGet", Lit(), V("object"), V("path")).
				ret(V("defaultValue")).
				ret(V("result")),

			// baseAssignValue(object, key, value)
			impl("_baseAssignValue", "object", "key", "value").
				// defineProperty is the result of an IIFE at module load.
				unresolved("", "package-value:defineProperty(object, key, {...})", Lit(), V("object"), V("key"), Alloc(V("value"))).
				set(V("object"), V("value")),

			// assignValue(object, key, value)
			impl("_assignValue", "object", "key", "value").
				let("objValue", Prop(V("object"))).
				builtin("own", "Object.prototype.hasOwnProperty", V("object"), V("key")).
				to("e", "eq", Lit(), V("objValue"), V("value")).
				to("", "_baseAssignValue", Lit(), V("object"), V("key"), V("value")).
				ret(Lit()),

			// baseSet(object, path, value, customizer)
			impl("_baseSet", "object", "path", "value", "customizer").
				to("o", "isObject", Lit(), V("object")).
				ret(V("object")).
				to("p", "_castPath", Lit(), V("path"), V("object")).
				let("path", V("p")).
				let("nested", V("object")).
				to("key", "_toKey", Lit(), Prop(V("path"))).
				let("newValue", V("value")).
				ret(V("object")).
				let("objValue", Prop(V("nested"))).
				call("c", "customizer", V("objValue"), V("key"), V("nested")).
				let("newValue", V("c")).
				to("io", "isObject", Lit(), V("objValue")).
				let("newValue", V("objValue")).
				to("ii", "_isIndex", Lit(), Prop(V("path"))).
				let("newValue", Alloc()).
				to("", "_assignValue", Lit(), V("nested"), V("key"), V("newValue")).
				let("nested", Prop(V("nested"))).
				ret(V("object")),

			impl("set", "object", "path", "value").
				ret(V("object")).
				to("r", "_baseSet", Lit(), V("object"), V("path"), V("value")).
				ret(V("r")),

			// toNumber(value)
			impl("toNumber", "value").
				ret(V("value")).
				to("s", "isSymbol", Lit(), V("value")).
				ret(Lit()).
				to("o", "isObject", Lit(), V("value")).
				// value.valueOf(), on a parameter the package does not type.
				unresolved("other", "untyped:value.valueOf()", V("value")).
				to("o2", "isObject", Lit(), V("other")).
				let("value", V("other")).
				let("value", Lit()).
				ret(V("value")).
				to("t", "_baseTrim", Lit(), V("value")).
				let("value", V("t")).
				builtin("b", "RegExp.prototype.test", G("reIsBinary"), V("value")).
				builtin("c", "RegExp.prototype.test", G("reIsOctal"), V("value")).
				builtin("sl", "String.prototype.slice", V("value"), Lit()).
				builtin("pi", "parseInt", Lit(), V("sl"), Lit()).
				builtin("h", "RegExp.prototype.test", G("reIsBadHex"), V("value")).
				ret(Lit()),

			// trimmedEndIndex(string)
			impl("_trimmedEndIndex", "string").
				builtin("ch", "String.prototype.charAt", V("string"), Lit()).
				builtin("w", "RegExp.prototype.test", G("reWhitespace"), V("ch")).
				ret(Lit()),

			// baseTrim(string)
			impl("_baseTrim", "string").
				to("e", "_trimmedEndIndex", Lit(), V("string")).
				builtin("s", "String.prototype.slice", V("string"), Lit(), Lit()).
				builtin("r", "String.prototype.replace", V("s"), G("reTrimStart"), Lit()).
				ret(V("r")).
				ret(V("string")),

			impl("toFinite", "value").
				ret(V("value")).ret(Lit()).
				to("n", "toNumber", Lit(), V("value")).
				let("value", V("n")).
				ret(Lit()).ret(V("value")).ret(Lit()),

			impl("toInteger", "value").
				to("result", "toFinite", Lit(), V("value")).
				ret(Lit()),

			// isIterateeCall(value, index, object)
			impl("_isIterateeCall", "value", "index", "object").
				to("o", "isObject", Lit(), V("object")).
				ret(Lit()).
				to("al", "isArrayLike", Lit(), V("object")).
				to("ii", "_isIndex", Lit(), V("index"), Prop(V("object"))).
				to("e", "eq", Lit(), Prop(V("object")), V("value")).
				ret(Lit()),

			// baseSlice(array, start, end)
			impl("_baseSlice", "array", "start", "end").
				let("result", Alloc()).
				set(V("result"), Prop(V("array"))).
				ret(V("result")),

			// chunk(array, size, guard)
			impl("chunk", "array", "size", "guard").
				to("it", "_isIterateeCall", Lit(), V("array"), V("size"), V("guard")).
				to("n", "toInteger", Lit(), V("size")).
				builtin("m", "Math.max", Lit(), V("n"), Lit()).
				ret(Alloc()).
				builtin("c", "Math.ceil", Lit(), Lit()).
				let("result", Alloc()).
				to("s", "_baseSlice", Lit(), V("array"), Lit(), Lit()).
				set(V("result"), V("s")).
				ret(V("result")),

			// copyArray(source, array)
			impl("_copyArray", "source", "array").
				let("array", Alloc()).
				set(V("array"), Prop(V("source"))).
				ret(V("array")),

			// baseUnary(func) returns `function (value) { return func(value) }`.
			impl("_baseUnary", "func").
				let("value", Unknown()).
				call("r", "func", V("value")).
				ret(G("fn:baseUnary/closure")),

			// basePullAll(array, values, iteratee, comparator)
			impl("_basePullAll", "array", "values", "iteratee", "comparator").
				let("indexOf", G("fn:baseIndexOfWith")).let("indexOf", G("fn:baseIndexOf")).
				let("seen", V("array")).
				to("cv", "_copyArray", Lit(), V("values")).
				let("values", V("cv")).
				to("u", "_baseUnary", Lit(), V("iteratee")).
				to("mapped", "_arrayMap", Lit(), V("array"), V("u")).
				let("seen", V("mapped")).
				let("value", Prop(V("values"))).
				call("computed", "iteratee", V("value")).
				let("computed", V("value")).
				// indexOf is a local holding one of two package functions.
				unresolved("fromIndex", "local-value:indexOf(seen, computed, fromIndex, comparator)", Lit(), V("seen"), V("computed"), Lit(), V("comparator")).
				builtin("", "Array.prototype.splice", V("seen"), Lit(), Lit()).
				builtin("", "Array.prototype.splice", V("array"), Lit(), Lit()).
				ret(V("array")),

			impl("pullAll", "array", "values").
				to("r", "_basePullAll", Lit(), V("array"), V("values")).
				let("res", V("r")).let("res", V("array")).
				ret(V("res")),

			// baseIteratee(value)
			impl("_baseIteratee", "value").
				ret(V("value")).
				ret(G("fn:identity")).
				builtin("a", "Array.isArray", Lit(), V("value")).
				// baseMatchesProperty, baseMatches and property build matcher
				// closures from the value. They are not lowered here, because
				// mapValues is already blocked before it reaches them.
				unresolved("m1", "not-lowered:baseMatchesProperty(value[0], value[1])", Lit(), Prop(V("value")), Prop(V("value"))).
				ret(V("m1")).
				unresolved("m2", "not-lowered:baseMatches(value)", Lit(), V("value")).
				ret(V("m2")).
				unresolved("m3", "not-lowered:property(value)", Lit(), V("value")).
				ret(V("m3")),

			// mapValues(object, iteratee)
			impl("mapValues", "object", "iteratee").
				let("result", Alloc()).
				to("it", "_baseIteratee", Lit(), V("iteratee")).
				let("iteratee", V("it")).
				// baseForOwn(object, function (value, key, object) {...}), with
				// the closure inlined. baseFor is `createBaseFor()`, a value
				// computed at module load.
				unresolved("", "package-value:baseFor(object, iteratee, keys)", Lit(), V("object"), G("fn:mapValues/closure"), G("fn:keys")).
				let("cvalue", Unknown()).let("ckey", Unknown()).let("cobject", Unknown()).
				call("x", "iteratee", V("cvalue"), V("ckey"), V("cobject")).
				to("", "_baseAssignValue", Lit(), V("result"), V("ckey"), V("x")).
				ret(V("result")),
		},
		members: []member{
			{name: "chunk", impl: "chunk", params: []string{obj, prim}, expect: "p0:& p1:prim"},
			{name: "get", impl: "get", params: []string{obj, obj, obj}, expect: "p0:& p1:&mut p2:&"},
			{name: "set", impl: "set", params: []string{obj, obj, obj}, expect: "p0:&mut p1:&mut p2:&"},
			{name: "pullAll", impl: "pullAll", params: []string{obj, obj}, expect: "p0:&mut p1:&"},
			{name: "mapValues", impl: "mapValues", params: []string{obj, cb}, expect: "p0:& p1:&"},
			{name: "isSymbol", impl: "isSymbol", params: []string{obj}, expect: "p0:&mut"},
		},
	})
}
