// Adds `Symbol.customMatcher`, the well-known symbol from the TC39 pattern
// matching proposal, to TypeScript's `SymbolConstructor`. polyfills.ts
// defines the symbol at runtime. The published declarations import this
// file, so code that uses the package's types sees the symbol too.
declare global {
    interface SymbolConstructor {
        readonly customMatcher: unique symbol;
    }
}

export {};
