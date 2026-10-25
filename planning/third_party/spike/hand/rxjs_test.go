//go:build spike

package hand

// rxjs@7.8.2, lowered from the shipped src TypeScript. Subject methods run
// their bodies inside `errorContext(() => {...})`, so the closure is lowered
// into the method and errorContext itself calls its `cb` parameter.

func init() {
	packages = append(packages, pkg{
		name:    "rxjs",
		version: "7.8.2",
		funcs: []*fn{
			// errorContext(cb)
			impl("util/errorContext", "cb").
				let("context", Alloc()).
				call("", "cb").
				throw(Prop(V("context"))).
				call("", "cb").
				ret(Lit()),

			// Subject._throwIfClosed()
			impl("Subject._throwIfClosed").
				throw(Alloc()).
				ret(Lit()),

			// Subject.next(value)
			impl("Subject.next", "value").
				to("", "util/errorContext", Lit(), G("fn:next/closure")).
				to("", "Subject._throwIfClosed", V("this$")).
				builtin("copy", "Array.from", Lit(), Prop(V("this$"))).
				set(V("this$"), V("copy")).
				// for (const observer of this.currentObservers) observer.next(value)
				let("observer", Prop(Prop(V("this$")))).
				// observer is an Observer<T>, an interface whose next is a
				// function-typed property.
				unresolved("", "interface-dispatch:observer.next(value)", V("observer"), V("value")).
				ret(Lit()),

			// Subject.complete()
			impl("Subject.complete").
				to("", "util/errorContext", Lit(), G("fn:complete/closure")).
				to("", "Subject._throwIfClosed", V("this$")).
				set(V("this$"), Lit()).
				let("observers", Prop(V("this$"))).
				builtin("o", "Array.prototype.shift", V("observers")).
				unresolved("", "interface-dispatch:observers.shift()!.complete()", V("o")).
				ret(Lit()),

			// BehaviorSubject.getValue()
			impl("BehaviorSubject.getValue").
				let("hasError", Prop(V("this$"))).
				let("thrownError", Prop(V("this$"))).
				let("_value", Prop(V("this$"))).
				throw(V("thrownError")).
				to("", "Subject._throwIfClosed", V("this$")).
				ret(V("_value")),

			// BehaviorSubject.next(value): super.next((this._value = value))
			impl("BehaviorSubject.next", "value").
				set(V("this$"), V("value")).
				to("", "Subject.next", V("this$"), V("value")).
				ret(Lit()),

			// pipeFromArray(fns)
			impl("util/pipe#pipeFromArray", "fns").
				ret(G("fn:identity")).
				ret(Prop(V("fns"))).
				// function piped(input) { return fns.reduce((prev, fn) => fn(prev), input) }
				let("input", Unknown()).
				let("prev", Unknown()).let("cfn", Unknown()).
				unresolved("", "closure-param:fn(prev)", V("prev")).
				builtin("", "Array.prototype.reduce", V("fns"), G("fn:piped/closure"), V("input")).
				ret(G("fn:piped")),

			// Observable.pipe(...operations): pipeFromArray(operations)(this)
			impl("Observable.pipe", "operations").
				to("piped", "util/pipe#pipeFromArray", Lit(), V("operations")).
				unresolved("r", "local-value:pipeFromArray(operations)(this)", Lit(), V("this$")).
				ret(V("r")),

			// operate(init) returns `(source) => {...}`, which calls
			// source.lift with a function that calls init.
			impl("util/lift#operate", "init").
				let("source", Unknown()).
				unresolved("", "closure-param:source.lift(...)", V("source"), G("fn:operate/closure2")).
				let("liftedSource", Unknown()).
				call("", "init", V("liftedSource"), Unknown()).
				unresolved("", "untyped:this.error(err)", Unknown(), Unknown()).
				throw(Alloc()).
				ret(G("fn:operate/closure")),

			// map(project, thisArg): operate((source, subscriber) => {...})
			impl("operators/map#map", "project", "thisArg").
				let("source", Unknown()).let("subscriber", Unknown()).
				let("index", Lit()).
				let("value", Unknown()).
				unresolved("pv", "closure-param:project.call(thisArg, value, index++)", V("project"), V("thisArg"), V("value"), Lit()).
				unresolved("", "closure-param:subscriber.next(...)", V("subscriber"), V("pv")).
				unresolved("os", "not-lowered:createOperatorSubscriber(subscriber, ...)", Lit(), V("subscriber"), G("fn:map/closure2")).
				unresolved("", "closure-param:source.subscribe(...)", V("source"), V("os")).
				to("r", "util/lift#operate", Lit(), G("fn:map/closure")).
				ret(V("r")),
		},
		members: []member{
			{name: "Observable.pipe", impl: "Observable.pipe", receiver: true, params: []string{obj}, expect: "self:& p0:&"},
			{name: "map", impl: "operators/map#map", params: []string{cb, obj}, expect: "p0:& p1:&"},
			{name: "Subject.next", impl: "Subject.next", receiver: true, params: []string{obj}, expect: "self:&mut p0:&"},
			{name: "Subject.complete", impl: "Subject.complete", receiver: true, params: []string{}, expect: "self:&mut"},
			{name: "BehaviorSubject.getValue", impl: "BehaviorSubject.getValue", receiver: true, params: []string{}, expect: "self:&"},
			{name: "BehaviorSubject.next", impl: "BehaviorSubject.next", receiver: true, params: []string{obj}, expect: "self:&mut p0:&"},
		},
	})
}
