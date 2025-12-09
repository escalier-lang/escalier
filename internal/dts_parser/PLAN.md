## Plan: TypeScript .d.ts Parser Implementation

This plan outlines implementing a dedicated parser for TypeScript declaration files (`.d.ts`), building on Escalier's existing parser infrastructure while handling `.d.ts`-specific syntax like `declare`, `export`, interfaces, and ambient declarations.

### Steps

1. **Create `dts_parser` package structure** — Add `internal/dts_parser/dts_parser.go` with `DtsParser` struct that wraps the existing [`Lexer`](internal/parser/lexer.go) and reuses token types from [`internal/parser/tokens.go`](internal/parser/tokens.go). Include error tracking matching [`internal/parser/parser.go`](internal/parser/parser.go) patterns.

2. **Design TypeScript-specific AST** — Create `internal/dts_parser/ast.go` with AST nodes tailored for TypeScript `.d.ts` syntax. Key differences from Escalier's AST include: `interface` declarations (vs type aliases), function overload signatures, index signatures (`[key: string]: T`), `import`/`export` statements, ambient declarations (`declare`), module augmentation, and TypeScript-style type syntax (e.g., `Type[]` vs Escalier's `Array<Type>`). The AST should represent TypeScript semantics accurately without forcing them into Escalier's syntax model.

3. **Implement declaration parsing** — Parse `.d.ts` top-level constructs: `declare` statements, `export` declarations, `interface` definitions, type aliases, function/variable declarations, and `namespace`/`module` blocks. Build the TypeScript-specific AST structure with declarations organized by namespace.

4. **Implement type annotation parsing** — Parse TypeScript type syntax into the `.d.ts` AST: primitives, unions, intersections, object types, function types, conditional types, mapped types, template literal types, `keyof`, `typeof`, index access, tuple types, array types (`T[]`), and generics with constraints. Handle TypeScript-specific modifiers like `readonly`, optional (`?`), and parameter properties.

5. **Handle .d.ts-specific features** — Add support for function overload signatures (multiple function declarations with same name), index signatures in interfaces, interface extension/merging (`extends`, multiple declarations), ambient module declarations (`declare module "name"`), triple-slash directives (`/// <reference ...>`), and `export =` / `export as namespace` patterns.

6. **Create AST-to-Type System converter** — Implement `internal/dts_parser/converter.go` with functions to transform the TypeScript-specific AST into Escalier's [`type_system.Type`](internal/type_system/types.go) instances and register them in [`type_system.Namespace`](internal/type_system/namespace.go) structures. Handle semantic differences like converting TypeScript interfaces to Escalier object types, resolving overload signatures, and mapping TypeScript-specific constructs to equivalent Escalier representations.

7. **Implement comprehensive tests** — Add unit tests using `go-snaps` for snapshot testing following patterns in existing parser tests. Create test fixtures in `fixtures/dts_parser/` with sample `.d.ts` files covering primitives, complex types, generics, namespaces, interfaces, overloads, and module augmentation. Test the AST structure directly, and test the conversion to Escalier's type system. Include error cases for malformed declarations.

### Further Considerations

1. **Parser architecture choice** — Should this be a standalone `DtsParser` (cleaner separation) or extend the existing [`Parser`](internal/parser/parser.go) with a mode flag (maximum reuse)? Recommend standalone with component reuse to avoid complicating the existing parser.

2. **AST design principles** — Should the TypeScript AST be a minimal representation (just enough to convert to Escalier's type system) or a complete representation (preserving all TypeScript syntactic details)? A minimal AST simplifies conversion but may lose information needed for error messages or future features. A complete AST is more complex but more flexible.

3. **Import resolution** — Should the parser resolve triple-slash directives and module imports, or defer this to a separate module resolution phase? Consider integration with the existing [`dep_graph`](internal/dep_graph/) package for dependency tracking.

4. **Type syntax differences** — How should we handle TypeScript array syntax (`T[]`) vs Escalier's generic syntax (`Array<T>`)? Should the parser normalize these during parsing or preserve them in the AST for the converter to handle? Similar questions apply to tuple rest elements, optional/readonly modifiers, and other syntactic differences.

## Parser Implementation Breakdown

To avoid response length limits and make the implementation more manageable, the parser should be built incrementally in the following stages:

### Phase 1: Foundation & Basic Types (parser_base.go)
- **Parser structure**: Create `DtsParser` struct with lexer, error tracking, and state management
- **Helper functions**: `expect()`, `consume()`, `peek()`, `reportError()`, parsing utilities
- **Primitive types**: Parse `string`, `number`, `boolean`, `any`, `unknown`, `void`, `null`, `undefined`, `never`, `symbol`, `bigint`, `object`
- **Literal types**: Parse string, number, boolean, and bigint literals
- **Basic identifiers**: Parse simple type references and qualified names (e.g., `Foo.Bar`)

### Phase 2: Simple Compound Types (parser_compound.go)
- **Array types**: `T[]` syntax
- **Tuple types**: `[T1, T2, ...]` with optional/rest elements and labels
- **Union types**: `T1 | T2 | ...`
- **Intersection types**: `T1 & T2 & ...`
- **Parenthesized types**: `(T)`
- **Type references**: With type arguments `Foo<T, U>`

### Phase 3: Function & Constructor Types (parser_functions.go)
- **Function types**: `(params) => ReturnType`
- **Constructor types**: `new (params) => T`
- **Parameters**: With optional (`?`), rest (`...`), and type annotations
- **Type parameters**: Generic syntax `<T extends U = Default>`
- **Type predicates**: `arg is Type` and `asserts` predicates

### Phase 4: Object & Interface Types (parser_objects.go)
- **Object type literals**: `{ prop: Type; method(): void }`
- **Property signatures**: With optional (`?`), readonly modifiers
- **Method signatures**: Including generic methods
- **Call signatures**: `(params): ReturnType`
- **Constructor signatures**: `new (params): T`
- **Index signatures**: `[key: string]: Type`
- **Getter/setter signatures**: `get prop(): T`, `set prop(value: T)`

### Phase 5: Advanced Type Operators (parser_advanced.go)
- **Indexed access**: `T[K]`
- **Conditional types**: `T extends U ? X : Y`
- **Infer types**: `infer T`
- **Mapped types**: `{ [K in T]: U }` with optional/readonly modifiers
- **Template literal types**: `` `${T}...` ``
- **keyof operator**: `keyof T`
- **typeof operator**: `typeof expr`
- **Import types**: `import("module").Type`
- **Rest/optional types**: `...T`, `T?`

### Phase 6: Declarations (parser_declarations.go)
- **Variable declarations**: `declare var/let/const name: Type`
- **Function declarations**: `declare function name<T>(params): ReturnType`
- **Type aliases**: `type Name<T> = Type`
- **Interface declarations**: `interface Name<T> extends Base { ... }`
- **Enum declarations**: `enum Name { ... }` and `const enum`
- **Class declarations**: `declare class Name<T> { ... }` with members

### Phase 7: Namespaces & Modules (parser_modules.go)
- **Namespace declarations**: `namespace Name { ... }` and `module Name { ... }`
- **Ambient module declarations**: `declare module "name" { ... }`
- **Import declarations**: `import { ... } from "module"` with type-only imports
- **Export declarations**: `export { ... }`, `export * from "module"`, etc.
- **Export assignments**: `export = Name`, `export as namespace Name`

### Phase 8: Class Members (parser_classes.go)
- **Constructor declarations**: With parameter properties
- **Method declarations**: With modifiers (public, private, protected, static, abstract, async)
- **Property declarations**: With modifiers (readonly, optional, static)
- **Getter/setter declarations**: `get/set` with modifiers
- **Index signatures in classes**: `[key: string]: Type`
- **Abstract members**: `abstract` methods and properties

### Implementation Strategy

1. **Start with Phase 1**: Build the foundation and get basic types working
2. **Test incrementally**: Add tests for each phase before moving to the next
3. **Reuse lexer**: Use the existing `internal/parser/Lexer` and token types
4. **Follow patterns**: Match the structure and conventions in `internal/parser/`
5. **Handle errors gracefully**: Report clear errors and recover to continue parsing
6. **Add as needed**: Some phases may need tokens added to `internal/parser/token.go` (e.g., `Interface`, `Implements`, `Extends`, `Abstract`, etc.)

Each phase builds on the previous one, making the implementation tractable and testable at every step.

