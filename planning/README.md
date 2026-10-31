# Planning

One folder per piece of design work. Two of them are halves of one problem and
are easy to confuse:

- **`web_api_types/`** — what a declaration in `internal/interop/data/web/`
  says. Augmenting the TypeScript `.d.ts` conversion with WebIDL from the web
  API specs, and hoisting the globals off the `*GlobalScope` classes.
- **`web_environments/`** — where a declaration exists. Which global scopes the
  platform gives it, how a program declares which scope it targets, and what the
  compiler does when a reference escapes it.

They share one input. `web_api_types/` Stage 1 emits the WebIDL table, and
`web_environments/` cannot start before it, because an `@env("window")` is a
claim about the other eight environments and only `[Exposed]` knows it.

They were one folder until the split. If a cross-reference looks wrong, the
other folder is where it went.
