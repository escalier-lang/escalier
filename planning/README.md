# Planning

One folder per piece of design work. Two of them are halves of one problem and
are easy to confuse:

- **`web_api_types/`** — what a declaration in `internal/interop/data/web/`
  says. Augmenting the TypeScript `.d.ts` conversion with WebIDL from the web
  API specs, and hoisting the globals off the `*GlobalScope` classes.
- **`environments/`** — where a declaration exists. Which environments provide
  it, how a program declares which it targets, and what the compiler does when a
  reference escapes it. An environment is a browser global scope or a
  non-browser runtime such as Node or Deno.

They share one input. Section 5.2 of `web_api_types/design.md` emits the WebIDL
table, and `environments/` cannot start before it, because an `@env("window")`
is a claim about the other eight browser environments and only `[Exposed]` knows
it. It answers nothing about the runtimes, which is why their source is still
open.

Each folder's `design.md` is the whole of it: the problem, the requirements and
the plan.
