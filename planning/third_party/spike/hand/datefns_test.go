//go:build spike

package hand

// date-fns@4.4.0, lowered from the published ESM files with the types of the
// sibling `.d.ts` files. Every member reaches `toDate` and `constructFrom`.

func init() {
	packages = append(packages, pkg{
		name:    "date-fns",
		version: "4.4.0",
		funcs: []*fn{
			// constructFrom(date, value)
			impl("constructFrom", "date", "value").
				// if (typeof date === "function") return date(value);
				call("r1", "date", V("value")).ret(V("r1")).
				// return date[constructFromSymbol](value);
				unresolved("r2", "dynamic:date[constructFromSymbol](value)", V("date"), V("value")).ret(V("r2")).
				// return new date.constructor(value);
				unresolved("r3", "dynamic:new date.constructor(value)", V("value")).ret(V("r3")).
				// return new Date(value);
				ret(Alloc()),

			// toDate(argument, context)
			impl("toDate", "argument", "context").
				// constructFrom(context || argument, argument)
				let("c", V("context")).let("c", V("argument")).
				to("r", "constructFrom", Lit(), V("c"), V("argument")).
				ret(V("r")),

			// addDays(date, amount, options)
			impl("addDays", "date", "amount", "options").
				to("_date", "toDate", Lit(), V("date"), Prop(V("options"))).
				builtin("", "isNaN", Lit(), V("amount")).
				let("c", Prop(V("options"))).let("c", V("date")).
				to("r", "constructFrom", Lit(), V("c"), Lit()).ret(V("r")).
				ret(V("_date")).
				builtin("d", "Date.prototype.getDate", V("_date")).
				builtin("", "Date.prototype.setDate", V("_date"), Lit()).
				ret(V("_date")),

			// startOfDay(date, options)
			impl("startOfDay", "date", "options").
				to("_date", "toDate", Lit(), V("date"), Prop(V("options"))).
				builtin("", "Date.prototype.setHours", V("_date"), Lit(), Lit(), Lit(), Lit()).
				ret(V("_date")),

			// isAfter(date, dateToCompare)
			impl("isAfter", "date", "dateToCompare").
				to("a", "toDate", Lit(), V("date")).
				to("b", "toDate", Lit(), V("dateToCompare")).
				ret(Lit()),

			// getYear(date, options)
			impl("getYear", "date", "options").
				to("d", "toDate", Lit(), V("date"), Prop(V("options"))).
				builtin("y", "Date.prototype.getFullYear", V("d")).
				ret(V("y")),

			// max(dates, options)
			impl("max", "dates", "options").
				let("context", Prop(V("options"))).
				// dates.forEach((date) => { ... }), with the closure inlined.
				let("date", Unknown()).
				builtin("bound", "Function.prototype.bind", G("fn:constructFrom"), Lit(), V("date")).
				let("context", V("bound")).
				to("date_", "toDate", Lit(), V("date"), V("context")).
				let("result", V("date_")).
				builtin("", "Array.prototype.forEach", V("dates"), G("fn:max/closure")).
				let("res", V("result")).let("res", Lit()).
				to("r", "constructFrom", Lit(), V("context"), V("res")).
				ret(V("r")),

			// normalizeDates(context, ...dates)
			impl("_lib/normalizeDates", "context", "dates").
				// dates.find((date) => typeof date === "object")
				let("date", Unknown()).
				builtin("found", "Array.prototype.find", V("dates"), G("fn:normalizeDates/closure")).
				let("c", V("context")).let("c", V("found")).
				builtin("normalize", "Function.prototype.bind", G("fn:constructFrom"), Lit(), V("c")).
				// dates.map(normalize) calls the bound constructFrom, which the
				// three IR additions do not resolve.
				unresolved("e", "bound-function:normalize(date)", Prop(V("dates"))).
				builtin("r", "Array.prototype.map", V("dates"), V("normalize")).
				ret(V("r")),

			// closestIndexTo(dateToCompare, dates)
			impl("closestIndexTo", "dateToCompare", "dates").
				to("t", "toDate", Lit(), V("dateToCompare")).
				builtin("", "isNaN", Lit(), Lit()).
				ret(Lit()).
				// dates.forEach((date, index) => { ... })
				let("date", Unknown()).
				to("date_", "toDate", Lit(), V("date")).
				builtin("", "isNaN", Lit(), Lit()).
				builtin("", "Math.abs", Lit(), Lit()).
				builtin("", "Array.prototype.forEach", V("dates"), G("fn:closestIndexTo/closure")).
				ret(Lit()),

			// closestTo(dateToCompare, dates, options)
			impl("closestTo", "dateToCompare", "dates", "options").
				to("n", "_lib/normalizeDates", Lit(), Prop(V("options")), V("dateToCompare"), Prop(V("dates"))).
				let("dateToCompare_", Prop(V("n"))).
				let("dates_", Alloc(Prop(V("n")))).
				to("index", "closestIndexTo", Lit(), V("dateToCompare_"), V("dates_")).
				builtin("", "isNaN", Lit(), V("index")).
				to("r", "constructFrom", Lit(), V("dateToCompare_"), Lit()).ret(V("r")).
				ret(Prop(V("dates_"))).
				ret(Lit()),
		},
		members: []member{
			{name: "addDays", impl: "addDays", params: []string{obj, prim, obj}, expect: "p0:& p1:prim p2:&"},
			{name: "startOfDay", impl: "startOfDay", params: []string{obj, obj}, expect: "p0:& p1:&"},
			{name: "isAfter", impl: "isAfter", params: []string{obj, obj}, expect: "p0:& p1:&"},
			{name: "getYear", impl: "getYear", params: []string{obj, obj}, expect: "p0:& p1:&"},
			{name: "max", impl: "max", params: []string{obj, obj}, expect: "p0:& p1:&"},
			{name: "closestTo", impl: "closestTo", params: []string{obj, obj, obj}, expect: "p0:& p1:& p2:&"},
		},
	})
}
