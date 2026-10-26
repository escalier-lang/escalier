//go:build spike

package hand

// uuid@14.0.3, lowered from the published dist/*.js files with the types of
// their sibling `.d.ts` files. `crypto` is a host global from lib.dom, which
// has no ECMA-262 fact.

func init() {
	packages = append(packages, pkg{
		name:    "uuid",
		version: "14.0.3",
		funcs: []*fn{
			// rng(): crypto.getRandomValues(rnds8)
			impl("rng").
				unresolved("r", "host-api:crypto.getRandomValues(rnds8)", G("crypto"), G("rnds8")).
				ret(V("r")),

			// unsafeStringify(arr, offset = 0)
			impl("stringify#unsafeStringify", "arr", "offset").
				builtin("s", "String.prototype.toLowerCase", Lit()).
				ret(V("s")),

			// validate(uuid): typeof uuid === 'string' && REGEX.test(uuid)
			impl("validate", "uuid").
				builtin("t", "RegExp.prototype.test", G("REGEX"), V("uuid")).
				ret(Lit()),

			// stringify(arr, offset = 0)
			impl("stringify", "arr", "offset").
				to("uuid", "stringify#unsafeStringify", Lit(), V("arr"), V("offset")).
				to("ok", "validate", Lit(), V("uuid")).
				throw(Alloc()).
				ret(V("uuid")),

			// parse(uuid)
			impl("parse", "uuid").
				to("ok", "validate", Lit(), V("uuid")).
				throw(Alloc()).
				builtin("s", "String.prototype.slice", V("uuid"), Lit(), Lit()).
				builtin("n", "parseInt", Lit(), V("s"), Lit()).
				builtin("r", "TypedArray.of", Lit(), Lit()).
				ret(V("r")),

			// _v4(options, buf, offset)
			impl("v4#_v4", "options", "buf", "offset").
				let("options", V("options")).let("options", Alloc()).
				// options.random ?? options.rng?.() ?? rng()
				let("rnds", Prop(V("options"))).
				unresolved("r1", "property-value:options.rng()", V("options")).
				let("rnds", V("r1")).
				to("r2", "rng", Lit()).
				let("rnds", V("r2")).
				throw(Alloc()).
				set(V("rnds"), Lit()).
				set(V("rnds"), Lit()).
				throw(Alloc()).
				set(V("buf"), Prop(V("rnds"))).
				ret(V("buf")).
				to("s", "stringify#unsafeStringify", Lit(), V("rnds")).
				ret(V("s")),

			// v4(options, buf, offset)
			impl("v4", "options", "buf", "offset").
				unresolved("u", "host-api:crypto.randomUUID()", G("crypto")).
				ret(V("u")).
				to("r", "v4#_v4", Lit(), V("options"), V("buf"), V("offset")).
				ret(V("r")),

			// stringToBytes(str)
			impl("v35#stringToBytes", "str").
				builtin("e", "encodeURIComponent", Lit(), V("str")).
				// unescape is an Annex B function the ECMA-262 graph does not
				// carry.
				unresolved("u", "builtin-unkeyed:unescape(...)", Lit(), V("e")).
				let("str", V("u")).
				let("bytes", Alloc()).
				builtin("c", "String.prototype.charCodeAt", V("str"), Lit()).
				set(V("bytes"), V("c")).
				ret(V("bytes")),

			// sha1(bytes) and md5(bytes) read their input and return a
			// digest they allocate.
			impl("sha1", "bytes").ret(Alloc()),
			impl("md5", "bytes").ret(Alloc()),

			// v35(version, hash, value, namespace, buf, offset)
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
				call("h", "hash", Lit(), V("bytes")).
				let("bytes", V("h")).
				set(V("bytes"), Lit()).
				set(V("bytes"), Lit()).
				throw(Alloc()).
				set(V("buf"), Prop(V("bytes"))).
				ret(V("buf")).
				to("s", "stringify#unsafeStringify", Lit(), V("bytes")).
				ret(V("s")),

			// v5(value, namespace, buf, offset)
			impl("v5", "value", "namespace", "buf", "offset").
				to("r", "v35", Lit(), Lit(), V("impl:sha1"), V("value"), V("namespace"), V("buf"), V("offset")).
				ret(V("r")),
		},
		members: []member{
			{name: "v4", impl: "v4", params: []string{obj, obj, prim}, expect: "p0:&mut p1:&mut p2:prim"},
			{name: "v5", impl: "v5", params: []string{obj, obj, obj, prim}, expect: "p0:& p1:& p2:&mut p3:prim"},
			{name: "stringify", impl: "stringify", params: []string{obj, prim}, expect: "p0:& p1:prim"},
			{name: "validate", impl: "validate", params: []string{obj}, expect: "p0:&"},
			{name: "parse", impl: "parse", params: []string{prim}, expect: "p0:prim"},
		},
	})
}
