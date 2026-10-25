//go:build spike

package hand

// lru-cache@11.5.3, lowered from src/index.ts as dist/esm/index.js.map carries
// it. The published JavaScript is minified, so the source map is the only
// readable form.
//
// The class keeps its TTL and size hooks, such as `#isStale` and
// `#updateItemAge`, in private fields that the constructor fills with closures.
// A call through one of them is a call through a property value.
// `metrics.publish` is node:diagnostics_channel, a package outside this one.

func init() {
	packages = append(packages, pkg{
		name:    "lru-cache",
		version: "11.5.3",
		funcs: []*fn{
			// get size() { return this.#size }
			impl("LRUCache.size").ret(Prop(V("this$"))),

			// #isBackgroundFetch(p)
			impl("LRUCache.#isBackgroundFetch", "p").
				builtin("h", "Object.prototype.hasOwnProperty", V("p"), Lit()).
				ret(Lit()),

			// #connect(p, n)
			impl("LRUCache.#connect", "p", "n").
				set(Prop(V("this$")), V("p")).
				set(Prop(V("this$")), V("n")).
				ret(Lit()),

			// #moveToTail(index)
			impl("LRUCache.#moveToTail", "index").
				set(V("this$"), Prop(Prop(V("this$")))).
				to("", "LRUCache.#connect", V("this$"), Prop(Prop(V("this$"))), Prop(Prop(V("this$")))).
				to("", "LRUCache.#connect", V("this$"), Prop(V("this$")), V("index")).
				set(V("this$"), V("index")).
				ret(Lit()),

			// has(k, hasOptions = {})
			impl("LRUCache.has", "k", "hasOptions").
				let("status", Prop(V("hasOptions"))).let("status", Alloc()).
				set(V("hasOptions"), V("status")).
				set(V("status"), Lit()).
				set(V("status"), V("k")).
				set(V("status"), V("this$")).
				to("result", "LRUCache.#has", V("this$"), V("k"), V("hasOptions")).
				unresolved("", "external-package:metrics.publish(status)", G("metrics"), V("status")).
				ret(V("result")),

			// #has(k, hasOptions = {})
			impl("LRUCache.#has", "k", "hasOptions").
				let("updateAgeOnHas", Prop(V("hasOptions"))).let("updateAgeOnHas", Prop(V("this$"))).
				let("status", Prop(V("hasOptions"))).
				builtin("index", "Map.prototype.get", Prop(V("this$")), V("k")).
				let("v", Prop(Prop(V("this$")))).
				to("bf", "LRUCache.#isBackgroundFetch", V("this$"), V("v")).
				ret(Lit()).
				unresolved("stale", "property-value:this.#isStale(index)", V("this$"), V("index")).
				unresolved("", "property-value:this.#updateItemAge(index)", V("this$"), V("index")).
				set(V("status"), Lit()).
				unresolved("", "property-value:this.#statusTTL(status, index)", V("this$"), V("status"), V("index")).
				ret(Lit()),

			// peek(k, peekOptions = {})
			impl("LRUCache.peek", "k", "peekOptions").
				unresolved("hs", "external-package:hasSubscribers()", Lit()).
				let("status", Prop(V("peekOptions"))).let("status", Alloc()).
				set(V("status"), Lit()).
				set(V("status"), V("k")).
				set(V("status"), V("this$")).
				set(V("peekOptions"), V("status")).
				to("result", "LRUCache.#peek", V("this$"), V("k"), V("peekOptions")).
				unresolved("", "external-package:metrics.publish(status)", G("metrics"), V("status")).
				ret(V("result")),

			// #peek(k, peekOptions)
			impl("LRUCache.#peek", "k", "peekOptions").
				let("status", Prop(V("peekOptions"))).
				let("allowStale", Prop(V("peekOptions"))).let("allowStale", Prop(V("this$"))).
				builtin("index", "Map.prototype.get", Prop(V("this$")), V("k")).
				unresolved("stale", "property-value:this.#isStale(index)", V("this$"), V("index")).
				set(V("status"), Lit()).
				ret(Lit()).
				let("v", Prop(Prop(V("this$")))).
				to("bf", "LRUCache.#isBackgroundFetch", V("this$"), V("v")).
				let("val", Prop(V("v"))).let("val", V("v")).
				set(V("status"), Lit()).
				set(V("status"), V("val")).
				ret(V("val")),

			// get(k, getOptions = {})
			impl("LRUCache.get", "k", "getOptions").
				let("status", Prop(V("getOptions"))).let("status", Alloc()).
				set(V("getOptions"), V("status")).
				set(V("status"), Lit()).
				set(V("status"), V("k")).
				set(V("status"), V("this$")).
				to("result", "LRUCache.#get", V("this$"), V("k"), V("getOptions")).
				set(V("status"), V("result")).
				unresolved("", "external-package:metrics.publish(status)", G("metrics"), V("status")).
				ret(V("result")),

			// #get(k, getOptions = {})
			impl("LRUCache.#get", "k", "getOptions").
				let("allowStale", Prop(V("getOptions"))).let("allowStale", Prop(V("this$"))).
				let("status", Prop(V("getOptions"))).
				builtin("index", "Map.prototype.get", Prop(V("this$")), V("k")).
				set(V("status"), Lit()).
				ret(Lit()).
				let("value", Prop(Prop(V("this$")))).
				to("fetching", "LRUCache.#isBackgroundFetch", V("this$"), V("value")).
				unresolved("", "property-value:this.#statusTTL(status, index)", V("this$"), V("status"), V("index")).
				unresolved("stale", "property-value:this.#isStale(index)", V("this$"), V("index")).
				to("", "LRUCache.#delete", V("this$"), V("k"), Lit()).
				set(V("status"), Lit()).
				ret(V("value")).
				ret(Prop(V("value"))).
				to("", "LRUCache.#moveToTail", V("this$"), V("index")).
				unresolved("", "property-value:this.#updateItemAge(index)", V("this$"), V("index")).
				let("r", Prop(V("value"))).let("r", V("value")).
				ret(V("r")),

			// #delete(k, reason)
			impl("LRUCache.#delete", "k", "reason").
				unresolved("", "external-package:metrics.publish({...})", G("metrics"), Alloc(V("k"), V("this$"))).
				builtin("index", "Map.prototype.get", Prop(V("this$")), V("k")).
				unresolved("", "host-api:clearTimeout(...)", Lit(), Prop(Prop(V("this$")))).
				set(Prop(V("this$")), Lit()).
				to("", "LRUCache.#clear", V("this$"), V("reason")).
				unresolved("", "property-value:this.#removeItemSize(index)", V("this$"), V("index")).
				let("v", Prop(Prop(V("this$")))).
				to("bf", "LRUCache.#isBackgroundFetch", V("this$"), V("v")).
				unresolved("", "property-value:v.__abortController.abort(...)", Prop(V("v")), Alloc()).
				unresolved("", "property-value:this.#dispose(v, k, reason)", V("v"), V("k"), V("reason")).
				builtin("", "Array.prototype.push", Prop(V("this$")), Alloc(V("v"), V("k"), V("reason"))).
				builtin("", "Map.prototype.delete", Prop(V("this$")), V("k")).
				set(Prop(V("this$")), Lit()).
				set(Prop(V("this$")), Lit()).
				set(V("this$"), Prop(Prop(V("this$")))).
				set(V("this$"), Lit()).
				builtin("", "Array.prototype.push", Prop(V("this$")), V("index")).
				builtin("task", "Array.prototype.shift", Prop(V("this$"))).
				unresolved("", "property-value:this.#disposeAfter(...task)", Prop(V("task"))).
				ret(Lit()),

			// #clear(reason) empties every list the cache holds and calls the
			// dispose hooks. It is lowered only as far as the writes it makes.
			impl("LRUCache.#clear", "reason").
				builtin("", "Map.prototype.clear", Prop(V("this$"))).
				builtin("", "Array.prototype.fill", Prop(V("this$")), Lit()).
				unresolved("", "property-value:this.#dispose(v, k, reason)", Lit(), Lit(), V("reason")).
				set(V("this$"), Lit()).
				ret(Lit()),

			// delete(k)
			impl("LRUCache.delete", "k").
				to("r", "LRUCache.#delete", V("this$"), V("k"), Lit()).
				ret(V("r")),
		},
		members: []member{
			{name: "LRUCache.size", impl: "LRUCache.size", receiver: true, params: []string{}, expect: "self:&"},
			{name: "LRUCache.has", impl: "LRUCache.has", receiver: true, params: []string{obj, obj}, expect: "self:&mut p0:& p1:&mut"},
			{name: "LRUCache.peek", impl: "LRUCache.peek", receiver: true, params: []string{obj, obj}, expect: "self:& p0:& p1:&mut"},
			{name: "LRUCache.get", impl: "LRUCache.get", receiver: true, params: []string{obj, obj}, expect: "self:&mut p0:& p1:&mut"},
			{name: "LRUCache.delete", impl: "LRUCache.delete", receiver: true, params: []string{obj}, expect: "self:&mut p0:&"},
		},
	})
}
