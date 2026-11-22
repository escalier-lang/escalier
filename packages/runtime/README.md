# @escalier/runtime

The runtime library for the [Escalier](https://github.com/escalier-lang/escalier)
programming language.

The JavaScript that the Escalier compiler emits imports this package, so a
project that runs compiled Escalier code needs it as a dependency:

```sh
npm install @escalier/runtime
```

It provides the helpers that compiled pattern matching calls, such as
`InvokeCustomMatcherOrThrow`, and a polyfill for `Symbol.customMatcher`. It
ships ESM and CommonJS builds with TypeScript declarations for each.
