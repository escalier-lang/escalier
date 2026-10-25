//go:build spike

package hand

// zod@4.6.7, lowered from the shipped src/v4 TypeScript. Schema classes are
// built at module load by `core.$constructor(name, initializer, methods)`, so
// a class such as `ZodOptional` is a value rather than a declaration, and
// `new ZodOptional(...)` has no constructor body to resolve. The methods in
// the third argument are the bodies behind `interface ZodType`'s method
// signatures. Matching the two takes knowledge of that pattern, which the
// lowering below supplies by hand.

func init() {
	packages = append(packages, pkg{
		name:    "zod",
		version: "4.6.7",
		funcs: []*fn{
			// util.normalizeParams(_params)
			impl("core/util#normalizeParams", "_params").
				let("params", V("_params")).
				ret(Alloc()).
				ret(Alloc()).
				throw(Alloc()).
				set(V("params"), Prop(V("params"))).
				call("", "DeletePropertyOrThrow", V("params"), Lit()).
				ret(Alloc(Prop(V("params")))).
				ret(V("params")),

			// snapshotChecks(def)
			impl("core/api#snapshotChecks", "def").
				set(V("def"), Alloc(Prop(Prop(V("def"))))).
				ret(V("def")),

			// _string(Class, params): new Class(snapshotChecks({...}))
			impl("core/api#_string", "Class", "params").
				to("np", "core/util#normalizeParams", Lit(), V("params")).
				to("def", "core/api#snapshotChecks", Lit(), Alloc(Prop(V("np")))).
				call("inst", "Class", V("def")).
				ret(V("inst")),

			// string(params): core._string(ZodString, params)
			impl("string", "params").
				to("r", "core/api#_string", Lit(), V("ZodString"), V("params")).
				ret(V("r")),

			// optional(innerType): new ZodOptional({ type, innerType })
			impl("optional", "innerType").
				unresolved("r", "package-value:new ZodOptional({...})", Lit(), Alloc(V("innerType"))).
				ret(V("r")),

			// ZodType methods
			impl("ZodType.optional").
				to("r", "optional", Lit(), V("this$")).
				ret(V("r")),

			impl("ZodType.brand").ret(V("this$")),

			// $ZodRegistry.add(schema, ..._meta)
			impl("core/registries#$ZodRegistry.add", "schema", "_meta").
				let("meta", Prop(V("_meta"))).
				builtin("", "WeakMap.prototype.set", Prop(V("this$")), V("schema"), V("meta")).
				builtin("", "Map.prototype.set", Prop(V("this$")), Prop(V("meta")), V("schema")).
				ret(V("this$")),

			// register(reg, meta): reg.add(this, meta); return this
			impl("ZodType.register", "reg", "meta").
				to("", "core/registries#$ZodRegistry.add", V("reg"), V("this$"), Alloc(V("meta"))).
				ret(V("this$")),

			// parse(data, params): parse.parse(this, data, params, {...})
			impl("ZodType.parse", "data", "params").
				// parse.parse is `_parse(errors.$ZodRealError)`, a function
				// built at module load.
				unresolved("r", "package-value:parse.parse(this, data, params, { callee: _parse })", Lit(), V("this$"), V("data"), V("params"), Alloc()).
				ret(V("r")),
		},
		members: []member{
			{name: "string", impl: "string", params: []string{obj}, expect: "p0:&mut"},
			{name: "optional", impl: "optional", params: []string{obj}, expect: "p0:&"},
			{name: "ZodType.optional", impl: "ZodType.optional", receiver: true, params: []string{}, expect: "self:&"},
			{name: "ZodType.brand", impl: "ZodType.brand", receiver: true, params: []string{}, expect: "self:&"},
			{name: "ZodType.register", impl: "ZodType.register", receiver: true, params: []string{obj, obj}, expect: "self:& p0:&mut p1:&"},
			{name: "ZodType.parse", impl: "ZodType.parse", receiver: true, params: []string{obj, obj}, expect: "self:& p0:& p1:&"},
		},
	})
}
