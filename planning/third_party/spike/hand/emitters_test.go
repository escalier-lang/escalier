//go:build spike

package hand

// Two event emitters with opposite shapes. mitt keeps its state in a variable
// its methods capture, and eventemitter3 keeps it in properties of `this`.

func init() {
	// mitt@3.0.1, lowered from src/index.ts as dist/mitt.mjs.map carries it.
	// Each method is a closure over `all`. Lowered on its own, `all` is a name
	// the method does not bind, so it reads as Unknown.
	packages = append(packages, pkg{
		name:    "mitt",
		version: "3.0.1",
		funcs: []*fn{
			// mitt(all)
			impl("mitt", "all").
				let("all", V("all")).let("all", Alloc()).
				ret(Alloc(V("all"), G("fn:on"), G("fn:off"), G("fn:emit"))),

			// on(type, handler)
			impl("on", "type", "handler").
				builtin("handlers", "Map.prototype.get", V("all"), V("type")).
				builtin("", "Array.prototype.push", V("handlers"), V("handler")).
				builtin("", "Map.prototype.set", V("all"), V("type"), Alloc(V("handler"))).
				ret(Lit()),

			// off(type, handler)
			impl("off", "type", "handler").
				builtin("handlers", "Map.prototype.get", V("all"), V("type")).
				builtin("i", "Array.prototype.indexOf", V("handlers"), V("handler")).
				builtin("", "Array.prototype.splice", V("handlers"), Lit(), Lit()).
				builtin("", "Map.prototype.set", V("all"), V("type"), Alloc()).
				ret(Lit()),

			// emit(type, evt)
			impl("emit", "type", "evt").
				builtin("handlers", "Map.prototype.get", V("all"), V("type")).
				builtin("copy", "Array.prototype.slice", V("handlers")).
				// .map((handler) => { handler(evt) }), with the closure inlined.
				let("handler", Unknown()).
				unresolved("", "closure-param:handler(evt)", V("evt")).
				builtin("", "Array.prototype.map", V("copy"), G("fn:emit/closure")).
				builtin("handlers2", "Map.prototype.get", V("all"), Lit()).
				builtin("copy2", "Array.prototype.slice", V("handlers2")).
				unresolved("", "closure-param:handler(type, evt)", V("type"), V("evt")).
				builtin("", "Array.prototype.map", V("copy2"), G("fn:emit/closure2")).
				ret(Lit()),
		},
		members: []member{
			{name: "mitt", impl: "mitt", params: []string{obj}, expect: "p0:&"},
			{name: "Emitter.on", impl: "on", receiver: true, params: []string{prim, cb}, expect: "self:&mut p0:prim p1:&"},
			{name: "Emitter.off", impl: "off", receiver: true, params: []string{prim, cb}, expect: "self:&mut p0:prim p1:&"},
			{name: "Emitter.emit", impl: "emit", receiver: true, params: []string{prim, obj}, expect: "self:& p0:prim p1:&"},
		},
	})

	// eventemitter3@5.0.4, lowered from index.js with the types of the sibling
	// index.d.ts. The package is ES5 code with methods on
	// `EventEmitter.prototype`.
	packages = append(packages, pkg{
		name:    "eventemitter3",
		version: "5.0.4",
		funcs: []*fn{
			// function EE(fn, context, once)
			impl("EE", "fn", "context", "once").
				set(V("this$"), V("fn")).
				set(V("this$"), V("context")).
				set(V("this$"), Lit()).
				ret(Lit()),

			// addListener(emitter, event, fn, context, once)
			impl("addListener", "emitter", "event", "fn", "context", "once").
				throw(Alloc()).
				let("ctx", V("context")).let("ctx", V("emitter")).
				let("listener", Alloc()).
				to("", "EE", V("listener"), V("fn"), V("ctx"), V("once")).
				// emitter._events[evt] = listener, emitter._eventsCount++
				set(Prop(V("emitter")), V("listener")).
				set(V("emitter"), Lit()).
				// emitter._events[evt].push(listener)
				builtin("", "Array.prototype.push", Prop(Prop(V("emitter"))), V("listener")).
				// emitter._events[evt] = [emitter._events[evt], listener]
				set(Prop(V("emitter")), Alloc(Prop(Prop(V("emitter"))), V("listener"))).
				ret(V("emitter")),

			// clearEvent(emitter, evt)
			impl("clearEvent", "emitter", "evt").
				set(V("emitter"), Lit()).
				set(V("emitter"), Alloc()).
				call("", "DeletePropertyOrThrow", Prop(V("emitter")), Lit()).
				ret(Lit()),

			// EventEmitter.prototype.eventNames()
			impl("EventEmitter.eventNames").
				let("names", Alloc()).
				ret(V("names")).
				let("events", Prop(V("this$"))).
				builtin("h", "Object.prototype.hasOwnProperty", V("events"), Lit()).
				builtin("s", "String.prototype.slice", Lit(), Lit()).
				builtin("", "Array.prototype.push", V("names"), V("s")).
				builtin("syms", "Object.getOwnPropertySymbols", Lit(), V("events")).
				builtin("c", "Array.prototype.concat", V("names"), V("syms")).
				ret(V("c")).
				ret(V("names")),

			// EventEmitter.prototype.listenerCount(event)
			impl("EventEmitter.listenerCount", "event").
				let("listeners", Prop(V("this$"))).
				ret(Lit()),

			// EventEmitter.prototype.emit(event, ...args)
			impl("EventEmitter.emit", "event", "a1").
				ret(Lit()).
				let("listeners", Prop(V("this$"))).
				to("", "EventEmitter.removeListener", V("this$"), V("event"), Prop(V("listeners")), Lit(), Lit()).
				// listeners.fn.call(listeners.context, a1, ...), a function the
				// emitter read out of its own state.
				unresolved("", "property-value:listeners.fn.call(listeners.context, a1)", Prop(V("listeners")), V("a1")).
				let("args", Alloc()).
				set(V("args"), V("a1")).
				unresolved("", "property-value:listeners.fn.apply(listeners.context, args)", Prop(V("listeners")), V("args")).
				to("", "EventEmitter.removeListener", V("this$"), V("event"), Prop(Prop(V("listeners"))), Lit(), Lit()).
				unresolved("", "property-value:listeners[i].fn.call(listeners[i].context, a1)", Prop(Prop(V("listeners"))), V("a1")).
				ret(Lit()),

			// EventEmitter.prototype.on(event, fn, context)
			impl("EventEmitter.on", "event", "fn", "context").
				to("r", "addListener", Lit(), V("this$"), V("event"), V("fn"), V("context"), Lit()).
				ret(V("r")),

			// EventEmitter.prototype.once(event, fn, context)
			impl("EventEmitter.once", "event", "fn", "context").
				to("r", "addListener", Lit(), V("this$"), V("event"), V("fn"), V("context"), Lit()).
				ret(V("r")),

			// EventEmitter.prototype.removeListener(event, fn, context, once)
			impl("EventEmitter.removeListener", "event", "fn", "context", "once").
				ret(V("this$")).
				to("", "clearEvent", Lit(), V("this$"), Lit()).
				ret(V("this$")).
				let("listeners", Prop(V("this$"))).
				let("events", Alloc()).
				builtin("", "Array.prototype.push", V("events"), Prop(V("listeners"))).
				let("e", Prop(V("events"))).let("e", V("events")).
				set(Prop(V("this$")), V("e")).
				ret(V("this$")),
		},
		members: []member{
			{name: "EventEmitter.on", impl: "EventEmitter.on", receiver: true, params: []string{prim, cb, obj}, expect: "self:&mut p0:prim p1:& p2:&"},
			{name: "EventEmitter.once", impl: "EventEmitter.once", receiver: true, params: []string{prim, cb, obj}, expect: "self:&mut p0:prim p1:& p2:&"},
			{name: "EventEmitter.emit", impl: "EventEmitter.emit", receiver: true, params: []string{prim, obj}, expect: "self:&mut p0:prim p1:&"},
			{name: "EventEmitter.removeListener", impl: "EventEmitter.removeListener", receiver: true, params: []string{prim, cb, obj, prim}, expect: "self:&mut p0:prim p1:& p2:& p3:prim"},
			{name: "EventEmitter.eventNames", impl: "EventEmitter.eventNames", receiver: true, params: []string{}, expect: "self:&"},
			{name: "EventEmitter.listenerCount", impl: "EventEmitter.listenerCount", receiver: true, params: []string{prim}, expect: "self:& p0:prim"},
		},
	})
}
