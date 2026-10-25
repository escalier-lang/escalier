//go:build spike

package hand

// express@5.3.0, lowered from lib/*.js. The package is CommonJS with methods
// assigned onto the `app`, `req` and `res` prototype objects. Inside the
// package, parameters take their JSDoc types, which TypeScript reads in plain
// JavaScript. The member parameter kinds come from @types/express@5.0.6.
//
// `this` inside those methods is the object the method was called on. Its
// own methods resolve, as `this.set` does, but a property that only exists at
// runtime, such as `this.app` or the `http.ServerResponse` methods `res`
// inherits, does not.

func init() {
	packages = append(packages, pkg{
		name:    "express",
		version: "5.3.0",
		funcs: []*fn{
			// utils.compileETag(val), and its siblings for the other settings,
			// return one of the package's own functions or val itself.
			impl("utils#compileETag", "val").
				ret(V("val")).
				throw(Alloc()).
				ret(Prop(G("exports"))),

			// app.set(setting, val)
			impl("app.set", "setting", "val").
				ret(Prop(Prop(V("this$")))).
				unresolved("", "external-package:debug('set \"%s\" to %o', setting, val)", Lit(), Lit(), V("setting"), V("val")).
				set(Prop(V("this$")), V("val")).
				to("e", "utils#compileETag", Lit(), V("val")).
				to("", "app.set", V("this$"), Lit(), V("e")).
				builtin("", "Object.defineProperty", Lit(), Prop(V("this$")), Lit(), Alloc()).
				ret(V("this$")),

			// app.enabled(setting): Boolean(this.set(setting))
			impl("app.enabled", "setting").
				to("s", "app.set", V("this$"), V("setting")).
				ret(Lit()),

			// app.use(fn, ...fns)
			impl("app.use", "fn").
				let("arg", V("fn")).
				builtin("", "Array.isArray", Lit(), V("arg")).
				let("arg", Prop(V("arg"))).
				let("path", V("fn")).let("path", Lit()).
				// flatten.call(slice.call(arguments, offset), Infinity)
				builtin("sl", "Array.prototype.slice", Alloc(V("fn")), Lit()).
				builtin("fns", "Array.prototype.flat", V("sl"), Lit()).
				throw(Alloc()).
				// this.router is a getter installed with Object.defineProperty
				// in app.init, so nothing declares it.
				let("router", Prop(V("this$"))).
				// fns.forEach(function (fn) { ... }, this), with the closure
				// inlined. Its own `this` is the forEach thisArg.
				let("cfn", Unknown()).
				unresolved("", "untyped:router.use(path, fn)", V("router"), V("path"), V("cfn")).
				unresolved("", "external-package:debug('.use app under %s', path)", Lit(), V("path")).
				set(V("cfn"), V("path")).
				set(V("cfn"), Unknown()).
				unresolved("", "untyped:router.use(path, mounted_app)", V("router"), V("path"), G("fn:mounted_app")).
				unresolved("", "untyped:fn.emit('mount', this)", V("cfn"), Unknown()).
				builtin("", "Array.prototype.forEach", V("fns"), G("fn:use/closure"), V("this$")).
				ret(V("this$")),

			// res.status(code)
			impl("res.status", "code").
				builtin("i", "Number.isInteger", Lit(), V("code")).
				builtin("j", "JSON.stringify", Lit(), V("code")).
				throw(Alloc()).
				throw(Alloc()).
				set(V("this$"), V("code")).
				ret(V("this$")),

			// res.set(field, val)
			impl("res.set", "field", "val").
				builtin("", "Array.isArray", Lit(), V("val")).
				builtin("m", "Array.prototype.map", V("val"), G("fn:String")).
				let("value", V("m")).let("value", Lit()).
				builtin("lc", "String.prototype.toLowerCase", V("field")).
				throw(Alloc()).
				unresolved("ct", "external-package:mime.contentType(value)", G("mime"), V("value")).
				let("value", V("ct")).
				// this.setHeader comes from http.ServerResponse, which res
				// inherits at runtime.
				unresolved("", "untyped:this.setHeader(field, value)", V("this$"), V("field"), V("value")).
				// for (var key in field) this.set(key, field[key])
				to("", "res.set", V("this$"), Lit(), Prop(V("field"))).
				ret(V("this$")),

			// req.get(name)
			impl("req.get", "name").
				throw(Alloc()).
				builtin("lc", "String.prototype.toLowerCase", V("name")).
				ret(Prop(Prop(V("this$")))).
				ret(Prop(Prop(V("this$")))),

			// res.json(obj)
			impl("res.json", "obj").
				let("app", Prop(V("this$"))).
				unresolved("escape", "untyped:app.get('json escape')", V("app"), Lit()).
				unresolved("replacer", "untyped:app.get('json replacer')", V("app"), Lit()).
				unresolved("spaces", "untyped:app.get('json spaces')", V("app"), Lit()).
				unresolved("body", "not-lowered:stringify(obj, replacer, spaces, escape)", Lit(), V("obj"), V("replacer"), V("spaces"), V("escape")).
				unresolved("ct", "untyped:this.get('Content-Type')", V("this$"), Lit()).
				to("", "res.set", V("this$"), Lit(), Lit()).
				unresolved("r", "not-lowered:this.send(body)", V("this$"), V("body")).
				ret(V("r")),
		},
		members: []member{
			{name: "app.set", impl: "app.set", receiver: true, params: []string{prim, obj}, expect: "self:&mut p0:prim p1:&"},
			{name: "app.enabled", impl: "app.enabled", receiver: true, params: []string{prim}, expect: "self:& p0:prim"},
			{name: "app.use", impl: "app.use", receiver: true, params: []string{obj}, expect: "self:&mut p0:&mut"},
			{name: "res.status", impl: "res.status", receiver: true, params: []string{prim}, expect: "self:&mut p0:prim"},
			{name: "res.set", impl: "res.set", receiver: true, params: []string{obj, obj}, expect: "self:&mut p0:& p1:&"},
			{name: "res.json", impl: "res.json", receiver: true, params: []string{obj}, expect: "self:&mut p0:&"},
			{name: "req.get", impl: "req.get", receiver: true, params: []string{prim}, expect: "self:& p0:prim"},
		},
	})
}
