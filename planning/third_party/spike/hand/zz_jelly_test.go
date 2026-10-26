//go:build spike

package hand

// The lowerings below replace a call the hand lowering left unresolved with the
// targets the Jelly prototype in planning/third_party/spike/jelly/ found for
// it. Each target is lowered by hand from the package source. A target that
// fans out into code this check does not lower stays a not-lowered call, so a
// resolved site never reads as settled because its target was left out.
//
// The file is named so that its init runs after every package's own, since it
// attaches to the packages they register.

func init() {
	jelly := map[string][]*fn{
		"date-fns": {
			// normalizeDates: dates.map(normalize), where normalize is
			// constructFrom bound to the context. Jelly resolves the call
			// to constructFrom.
			impl("_lib/normalizeDates", "context", "dates").
				let("date", Unknown()).
				builtin("found", "Array.prototype.find", V("dates"), Alloc()).
				let("c", V("context")).let("c", V("found")).
				builtin("normalize", "Function.prototype.bind", V("impl:constructFrom"), Lit(), V("c")).
				to("e", "constructFrom", Lit(), V("c"), Prop(V("dates"))).
				builtin("r", "Array.prototype.map", V("dates"), V("normalize")).
				ret(V("r")),
		},

		"immer": {
			// die(error, ...args): e.apply(null, args) resolves to the
			// message functions in the errors table. Each formats a
			// string from its argument.
			impl("utils/errors#message", "thing").ret(Lit()),
			impl("die", "error", "args").
				let("e", Prop(G("errors"))).
				to("f", "isFunction", Lit(), V("e")).
				to("msg", "utils/errors#message", Lit(), Prop(V("args"))).
				throw(Alloc()).
				throw(Alloc()),

			// each(obj, iter, strict): obj.forEach resolves to the native
			// forEach of a Map, a Set or an array, called with the
			// closure below.
			impl("each", "obj", "iter", "strict").
				to("t", "getArchtype", Lit(), V("obj")).
				builtin("k1", "Reflect.ownKeys", Lit(), V("obj")).
				builtin("k2", "Object.keys", Lit(), V("obj")).
				let("keys", V("k1")).let("keys", V("k2")).
				let("key", Unknown()).
				call("", "iter", Lit(), V("key"), Prop(V("obj")), V("obj")).
				builtin("", "Array.prototype.forEach", V("keys"), Alloc()).
				let("entry", Unknown()).let("index", Unknown()).
				call("", "iter", Lit(), V("index"), V("entry"), V("obj")).
				builtin("", "Array.prototype.forEach", V("obj"), Alloc()).
				ret(Lit()),
		},

		"uuid": {
			// v35(version, hash, value, namespace, buf, offset): hash(bytes)
			// resolves to sha1 and md5, the only functions any caller
			// passes as hash.
			impl("v35", "version", "hash", "value", "namespace", "buf", "offset").
				to("vb", "v35#stringToBytes", Lit(), V("value")).
				let("valueBytes", V("vb")).let("valueBytes", V("value")).
				to("nb", "parse", Lit(), V("namespace")).
				let("namespaceBytes", V("nb")).let("namespaceBytes", V("namespace")).
				to("np", "parse", Lit(), V("namespace")).
				let("namespace", V("np")).
				throw(Alloc()).
				let("bytes", Alloc()).
				builtin("", "TypedArray.prototype.set", V("bytes"), V("namespaceBytes")).
				builtin("", "TypedArray.prototype.set", V("bytes"), V("valueBytes"), Lit()).
				to("h1", "sha1", Lit(), V("bytes")).
				to("h2", "md5", Lit(), V("bytes")).
				let("h", V("h1")).let("h", V("h2")).
				let("bytes", V("h")).
				set(V("bytes"), Lit()).
				set(V("bytes"), Lit()).
				throw(Alloc()).
				set(V("buf"), Prop(V("bytes"))).
				ret(V("buf")).
				to("s", "stringify#unsafeStringify", Lit(), V("bytes")).
				ret(V("s")),
		},

		"lodash-es": {
			// MapCache, the cache memoize keeps. Each method reads or
			// writes the cache's own data.
			impl("_MapCache.has", "key").ret(Lit()),
			impl("_MapCache.get", "key").ret(Prop(Prop(V("this$")))),
			impl("_MapCache.set", "key", "value").
				set(Prop(V("this$")), V("value")).
				set(V("this$"), Lit()).
				ret(V("this$")),
			impl("_MapCache.clear").
				set(V("this$"), Alloc()).
				ret(Lit()),

			// memoizeCapped's resolver: clears the cache once it is full.
			impl("_memoizeCapped#resolver", "key").
				to("", "_MapCache.clear", G("stringToPath.cache")).
				ret(V("key")),

			// stringToPath's own function: splits a path string into its
			// segments.
			impl("_stringToPath#closure", "string").
				let("result", Alloc()).
				builtin("c", "String.prototype.charCodeAt", V("string"), Lit()).
				builtin("", "Array.prototype.push", V("result"), Lit()).
				builtin("", "String.prototype.replace", V("string"), G("rePropName"), Alloc()).
				builtin("", "Array.prototype.push", V("result"), Lit()).
				ret(V("result")),

			// memoize's memoized wrapper. stringToPath is built at module
			// load, so the wrapper and its cache are module-level.
			impl("memoize#memoized", "args").
				to("key", "_memoizeCapped#resolver", Lit(), Prop(V("args"))).
				let("cache", Prop(G("stringToPath"))).
				to("h", "_MapCache.has", V("cache"), V("key")).
				to("g", "_MapCache.get", V("cache"), V("key")).
				ret(V("g")).
				to("result", "_stringToPath#closure", Lit(), Prop(V("args"))).
				to("c2", "_MapCache.set", V("cache"), V("key"), V("result")).
				set(G("stringToPath"), V("c2")).
				ret(V("result")),

			// castPath(value, object), with stringToPath resolved to the
			// memoized wrapper.
			impl("_castPath", "value", "object").
				builtin("a", "Array.isArray", Lit(), V("value")).
				ret(V("value")).
				to("k", "_isKey", Lit(), V("value"), V("object")).
				to("s", "toString", Lit(), V("value")).
				to("p", "memoize#memoized", G("stringToPath"), Alloc(V("s"))).
				ret(Alloc(V("value"))).
				ret(V("p")),

			// baseAssignValue(object, key, value): defineProperty is the
			// native Object.defineProperty getNative returned.
			impl("_baseAssignValue", "object", "key", "value").
				builtin("", "Object.defineProperty", Lit(), V("object"), V("key"), Alloc(V("value"))).
				set(V("object"), V("value")),

			// The closure createBaseFor returns, which baseFor holds.
			impl("_createBaseFor#closure", "object", "iteratee", "keysFunc").
				builtin("iterable", "Object", Lit(), V("object")).
				call("props", "keysFunc", Lit(), V("object")).
				call("r", "iteratee", Lit(), Prop(V("iterable")), Lit(), V("iterable")).
				ret(V("object")),

			// mapValues(object, iteratee), with baseFor resolved.
			impl("mapValues", "object", "iteratee").
				let("result", Alloc()).
				to("it", "_baseIteratee", Lit(), V("iteratee")).
				let("iteratee", V("it")).
				to("", "_createBaseFor#closure", Lit(), V("object"), Alloc(), V("impl:keys")).
				let("cvalue", Unknown()).let("ckey", Unknown()).let("cobject", Unknown()).
				call("x", "iteratee", Lit(), V("cvalue"), V("ckey"), V("cobject")).
				to("", "_baseAssignValue", Lit(), V("result"), V("ckey"), V("x")).
				ret(V("result")),

			// baseIndexOf and baseIndexOfWith, the two functions basePullAll's
			// indexOf holds.
			impl("_strictIndexOf", "array", "value", "fromIndex").ret(Lit()),
			impl("_baseIsNaN", "value").ret(Lit()),
			impl("_baseFindIndex", "array", "predicate", "fromIndex", "fromRight").
				call("r", "predicate", Lit(), Prop(V("array")), Lit(), V("array")).
				ret(Lit()),
			impl("_baseIndexOf", "array", "value", "fromIndex").
				to("a", "_strictIndexOf", Lit(), V("array"), V("value"), V("fromIndex")).
				to("b", "_baseFindIndex", Lit(), V("array"), V("impl:_baseIsNaN"), V("fromIndex")).
				ret(Lit()),
			impl("_baseIndexOfWith", "array", "value", "fromIndex", "comparator").
				call("r", "comparator", Lit(), Prop(V("array")), V("value")).
				ret(Lit()),

			// basePullAll, with indexOf resolved to both candidates.
			impl("_basePullAll", "array", "values", "iteratee", "comparator").
				let("seen", V("array")).
				to("cv", "_copyArray", Lit(), V("values")).
				let("values", V("cv")).
				to("u", "_baseUnary", Lit(), V("iteratee")).
				to("mapped", "_arrayMap", Lit(), V("array"), V("u")).
				let("seen", V("mapped")).
				let("value", Prop(V("values"))).
				call("computed", "iteratee", Lit(), V("value")).
				let("computed", V("value")).
				to("f1", "_baseIndexOf", Lit(), V("seen"), V("computed"), Lit()).
				to("f2", "_baseIndexOfWith", Lit(), V("seen"), V("computed"), Lit(), V("comparator")).
				builtin("", "Array.prototype.splice", V("seen"), Lit(), Lit()).
				builtin("", "Array.prototype.splice", V("array"), Lit(), Lit()).
				ret(V("array")),
		},

		"lru-cache": {
			// The closures #initializeTTLTracking stores in the TTL fields,
			// and the field defaults. Each closure captures the `starts`
			// and `ttls` arrays and the instance, which it reaches as its
			// lexical `this`. Lowered on its own, a captured name is one the
			// closure does not bind.
			impl("LRUCache#getNow").
				unresolved("n", "external-package:this.#perf.now()", V("self")).
				unresolved("t", "host-api:setTimeout(() => (cachedNow = 0), ttlResolution)", Lit()).
				unresolved("", "host-api:t.unref()", V("t")).
				ret(V("n")),
			impl("LRUCache#isStale", "index").
				let("s", Prop(V("starts"))).
				let("t", Prop(V("ttls"))).
				to("now", "LRUCache#getNow", Lit()).
				ret(Lit()),
			impl("LRUCache#isStaleDefault", "index").ret(Lit()),
			impl("LRUCache#setPurgeTimer", "index", "ttl").
				unresolved("", "host-api:clearTimeout(purgeTimers[index])", Lit(), Prop(V("purgeTimers"))).
				set(V("purgeTimers"), Lit()).
				unresolved("t", "host-api:setTimeout(...)", Lit(), Alloc(), Lit()).
				set(V("purgeTimers"), V("t")).
				ret(Lit()),
			impl("LRUCache#updateItemAge", "index").
				unresolved("n", "external-package:this.#perf.now()", V("self")).
				set(V("starts"), V("n")).
				to("", "LRUCache#setPurgeTimer", Lit(), V("index"), Prop(V("ttls"))).
				ret(Lit()),
			impl("LRUCache#updateItemAgeDefault", "index").ret(Lit()),
			impl("LRUCache#statusTTL", "status", "index").
				let("ttl", Prop(V("ttls"))).
				let("start", Prop(V("starts"))).
				set(V("status"), V("ttl")).
				set(V("status"), V("start")).
				to("now", "LRUCache#getNow", Lit()).
				set(V("status"), V("now")).
				set(V("status"), Lit()).
				ret(Lit()),
			impl("LRUCache#statusTTLDefault", "status", "index").ret(Lit()),
			impl("LRUCache#removeItemSize", "index").
				set(V("self"), Prop(V("sizes"))).
				set(V("sizes"), Lit()).
				ret(Lit()),
			impl("LRUCache#removeItemSizeDefault", "index").ret(Lit()),

			// #clear(reason), with its dispose call resolved to the
			// caller's option.
			impl("LRUCache.#clear", "reason").
				builtin("", "Map.prototype.clear", Prop(V("this$"))).
				builtin("", "Array.prototype.fill", Prop(V("this$")), Lit()).
				unresolved("", "closure-param:this.#dispose(v, k, reason), the caller's dispose option", Lit(), Lit(), V("reason")).
				set(V("this$"), Lit()).
				ret(Lit()),

			// #has, #peek, #get and #delete with the four fields resolved.
			impl("LRUCache.#has", "k", "hasOptions").
				let("updateAgeOnHas", Prop(V("hasOptions"))).let("updateAgeOnHas", Prop(V("this$"))).
				let("status", Prop(V("hasOptions"))).
				builtin("index", "Map.prototype.get", Prop(V("this$")), V("k")).
				let("v", Prop(Prop(V("this$")))).
				to("bf", "LRUCache.#isBackgroundFetch", V("this$"), V("v")).
				ret(Lit()).
				to("s1", "LRUCache#isStale", Lit(), V("index")).
				to("s2", "LRUCache#isStaleDefault", Lit(), V("index")).
				to("", "LRUCache#updateItemAge", Lit(), V("index")).
				to("", "LRUCache#updateItemAgeDefault", Lit(), V("index")).
				set(V("status"), Lit()).
				to("", "LRUCache#statusTTL", Lit(), V("status"), V("index")).
				to("", "LRUCache#statusTTLDefault", Lit(), V("status"), V("index")).
				ret(Lit()),
			impl("LRUCache.#peek", "k", "peekOptions").
				let("status", Prop(V("peekOptions"))).
				let("allowStale", Prop(V("peekOptions"))).let("allowStale", Prop(V("this$"))).
				builtin("index", "Map.prototype.get", Prop(V("this$")), V("k")).
				to("s1", "LRUCache#isStale", Lit(), V("index")).
				to("s2", "LRUCache#isStaleDefault", Lit(), V("index")).
				set(V("status"), Lit()).
				ret(Lit()).
				let("v", Prop(Prop(V("this$")))).
				to("bf", "LRUCache.#isBackgroundFetch", V("this$"), V("v")).
				let("val", Prop(V("v"))).let("val", V("v")).
				set(V("status"), Lit()).
				set(V("status"), V("val")).
				ret(V("val")),
			impl("LRUCache.#get", "k", "getOptions").
				let("allowStale", Prop(V("getOptions"))).let("allowStale", Prop(V("this$"))).
				let("status", Prop(V("getOptions"))).
				builtin("index", "Map.prototype.get", Prop(V("this$")), V("k")).
				set(V("status"), Lit()).
				ret(Lit()).
				let("value", Prop(Prop(V("this$")))).
				to("fetching", "LRUCache.#isBackgroundFetch", V("this$"), V("value")).
				to("", "LRUCache#statusTTL", Lit(), V("status"), V("index")).
				to("", "LRUCache#statusTTLDefault", Lit(), V("status"), V("index")).
				to("s1", "LRUCache#isStale", Lit(), V("index")).
				to("s2", "LRUCache#isStaleDefault", Lit(), V("index")).
				to("", "LRUCache.#delete", V("this$"), V("k"), Lit()).
				set(V("status"), Lit()).
				ret(V("value")).
				ret(Prop(V("value"))).
				to("", "LRUCache.#moveToTail", V("this$"), V("index")).
				to("", "LRUCache#updateItemAge", Lit(), V("index")).
				to("", "LRUCache#updateItemAgeDefault", Lit(), V("index")).
				let("r", Prop(V("value"))).let("r", V("value")).
				ret(V("r")),
			impl("LRUCache.#delete", "k", "reason").
				unresolved("", "external-package:metrics.publish({...})", G("metrics"), Alloc(V("k"), V("this$"))).
				builtin("index", "Map.prototype.get", Prop(V("this$")), V("k")).
				unresolved("", "host-api:clearTimeout(...)", Lit(), Prop(Prop(V("this$")))).
				set(Prop(V("this$")), Lit()).
				to("", "LRUCache.#clear", V("this$"), V("reason")).
				to("", "LRUCache#removeItemSize", Lit(), V("index")).
				to("", "LRUCache#removeItemSizeDefault", Lit(), V("index")).
				let("v", Prop(Prop(V("this$")))).
				to("bf", "LRUCache.#isBackgroundFetch", V("this$"), V("v")).
				unresolved("", "host-api:v.__abortController.abort(...)", Prop(V("v")), Alloc()).
				// #dispose and #disposeAfter hold the dispose and
				// disposeAfter options, which the caller supplies.
				unresolved("", "closure-param:this.#dispose(v, k, reason), the caller's dispose option", V("v"), V("k"), V("reason")).
				builtin("", "Array.prototype.push", Prop(V("this$")), Alloc(V("v"), V("k"), V("reason"))).
				builtin("", "Map.prototype.delete", Prop(V("this$")), V("k")).
				set(Prop(V("this$")), Lit()).
				set(Prop(V("this$")), Lit()).
				set(V("this$"), Prop(Prop(V("this$")))).
				set(V("this$"), Lit()).
				builtin("", "Array.prototype.push", Prop(V("this$")), V("index")).
				builtin("task", "Array.prototype.shift", Prop(V("this$"))).
				unresolved("", "closure-param:this.#disposeAfter(...task), the caller's disposeAfter option", Prop(V("task"))).
				ret(Lit()),
		},

		"rxjs": {
			// errorContext(cb): every cb its callers pass is a closure they
			// create, so the call runs a body already lowered into the
			// caller.
			impl("util/errorContext", "cb").
				let("context", Alloc()).
				throw(Prop(V("context"))).
				ret(Lit()),

			// Subject.next(value): observer.next resolves to the next
			// methods of seven Subject and Subscriber classes. Each of
			// those dispatches again through its destination, which this
			// check does not lower.
			impl("Subject.next", "value").
				to("", "util/errorContext", Lit(), Alloc()).
				to("", "Subject._throwIfClosed", V("this$")).
				builtin("copy", "Array.from", Lit(), Prop(V("this$"))).
				set(V("this$"), V("copy")).
				let("observer", Prop(Prop(V("this$")))).
				to("", "Subject.next", V("observer"), V("value")).
				unresolved("", "not-lowered:Subscriber.next and its destination's next", V("observer"), V("value")).
				ret(Lit()),

			// Subject.complete(): observers.shift().complete() resolves to
			// four complete methods, which again dispatch further.
			impl("Subject.complete").
				to("", "util/errorContext", Lit(), Alloc()).
				to("", "Subject._throwIfClosed", V("this$")).
				set(V("this$"), Lit()).
				let("observers", Prop(V("this$"))).
				builtin("o", "Array.prototype.shift", V("observers")).
				to("", "Subject.complete", V("o")).
				unresolved("", "not-lowered:Subscriber.complete and its destination's complete", V("o")).
				ret(Lit()),

			// Observable.pipe(...operations): pipeFromArray(operations)
			// returns a function that Jelly resolves to piped, identity,
			// the operate closure, or take's. piped calls each operator,
			// and the operate closure calls source.lift, neither lowered
			// here.
			impl("Observable.pipe", "operations").
				to("piped", "util/pipe#pipeFromArray", Lit(), V("operations")).
				unresolved("r", "not-lowered:piped(this), operate's closure and take's", Lit(), V("this$")).
				ret(V("r")),
		},

		"zod": {
			// The class body core.$constructor builds. It runs the shared
			// init, which calls the initializer the class was made with,
			// installs the class's methods on a prototype it reads off the
			// instance, runs deferred closures, and calls a postprocessor
			// read off globalThis. Jelly gives the initializer call 10
			// candidates, none lowered here.
			impl("core#constructor", "def").
				set(Prop(V("this$")), V("def")).
				builtin("", "Set.prototype.add", Prop(Prop(V("this$"))), Lit()).
				unresolved("", "not-lowered:initializer(inst, def), 10 candidates", Lit(), V("this$"), V("def")).
				builtin("proto", "Object.getPrototypeOf", Lit(), V("this$")).
				set(V("proto"), Lit()).
				set(V("this$"), Unknown()).
				unresolved("", "property-value:fn() for each deferred closure", Lit()).
				unresolved("", "external-package:globalThis.__zod_globalConfig.postProcessor(inst)", Lit(), V("this$")).
				ret(V("this$")),

			// optional(innerType): new ZodOptional({...}) resolves to the
			// class body above.
			impl("optional", "innerType").
				let("inst", Alloc()).
				to("", "core#constructor", V("inst"), Alloc(V("innerType"))).
				ret(V("inst")),

			// _string(Class, params): new Class(...) resolves to the same
			// class body.
			impl("core/api#_string", "Class", "params").
				to("np", "core/util#normalizeParams", Lit(), V("params")).
				to("def", "core/api#snapshotChecks", Lit(), Alloc(Prop(V("np")))).
				let("inst", Alloc()).
				to("", "core#constructor", V("inst"), V("def")).
				ret(V("inst")),

			// parse(data, params): parse.parse resolves to _parse's inner
			// function, which calls schema._zod.run. Jelly gives that call
			// 50 targets, none lowered here.
			impl("ZodType.parse", "data", "params").
				unresolved("r", "not-lowered:schema._zod.run(...), 50 candidates", V("this$"), V("data"), V("params")).
				ret(V("r")),
		},
	}

	for i := range packages {
		packages[i].jelly = jelly[packages[i].name]
	}
}
