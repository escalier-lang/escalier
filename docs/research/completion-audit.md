# Completion audit

This report lists where the LSP server's code completion works today, where it returns nothing or the wrong list, and how to split the fixes into pull requests. It covers `textDocumentCompletion` and `moduleCompletion` in [cmd/lsp-server/completion.go](../../cmd/lsp-server/completion.go) as of `d75b2fc` (#2712).

## Method

A temporary test, not committed, opened a source file through `integrationServer` and requested completion at a marked cursor. For each position it recorded the node `findNodeAndParent` found, that node's ancestors, which branch of the handler answered, and the items returned. Each of about 170 snippets ran once as `bin/main.esc` and once as `lib/index.esc`. The snippets cover declarations, expressions, types, patterns, imports, classes, enums, interfaces, generics, JSX, object literals, destructuring, call arguments, strings, template literals, comments, `match`, `if val`, `try`/`catch`, `for ... in`, `async`/`await`, generators and `declare` forms, each in a complete and a half-typed form.

A second pass loaded each project under `playground/examples/` and requested completion at the end of every identifier in its `lib/` and `bin/` files. An identifier counts as a hit when the list contains it. Keywords and declaration names are expected misses, so the totals below split the positions by node.

| Position in the examples | Hits | Misses |
|---|---|---|
| Identifier read, not the object of a member expression | 258 | 22 |
| Identifier that is the object of a member expression, as `console` in `console.log` | 0 | 159 |
| Member name after a dot | 158 | 9 |
| Variant name after a dot in a pattern | 32 | 0 |
| Named type reference | 100 | 25 |
| Primitive type such as `number`, `string` or `boolean` | 0 | 156 |

The TypeScript column in each table is what TypeScript's language server offers in the equivalent JavaScript or TypeScript position.

## How the handler chooses a list

Both entry points find the deepest node whose span contains the cursor, then try these in order:

1. `shouldSuppressCompletions` returns an empty list when the cursor is inside the pattern of a `val` or `var` declaration.
2. `emptyIndexKeyCompletions` answers inside the empty brackets of `p[]`.
3. `extractorNameCompletions` answers after the dot of `Enum.` in a pattern.
4. A node of `nil` gets every name in scope, with no filter.
5. A switch on the node handles `MemberExpr`, `IdentExpr`, `ErrorTypeAnn` and `TypeRefTypeAnn`.
6. Any other node gets every name in scope, filtered by the word before the cursor.

Steps 4 and 6 return the same list: local bindings, then the scope's types and namespaces, then the builtins. This report calls it the scope list. Because it is sorted by label and cut to 100 items, a user sees it as an alphabetical run of builtins from `AbortController` onward.

## Contexts handled today

| Context | Example | Code path | Notes |
|---|---|---|---|
| Member after a dot | `point.\|`, `point.x\|` | switch `*ast.MemberExpr` → `memberCompletions` | Reads the receiver's display type. Works on literals, arrays, tuples, call results, `(await p).`, `(&p).`, chained members and template placeholders. |
| Optional chain | `p?.\|` on `{a: number} \| undefined` | switch `*ast.MemberExpr` | `null` and `undefined` variants are skipped. |
| Union and intersection receivers | `u.\|` on `A \| B` | `unionCompletions`, `intersectionCompletions` | A union offers every member, and its detail gains `\| undefined` when a variant lacks it. |
| Mutability of the receiver | `self.\|` in a `&self` method | `completionsFromSolObject` with `info.LendsMut` | Hides `&mut self` methods and setters unless the receiver lends mutable access. |
| `self` members in a method | `return self.\|` | switch `*ast.MemberExpr` | Fields, getters and methods of the class. |
| Type parameter with a bound | `x.\|` where `x: T` and `T: {price: number}` | switch `*ast.MemberExpr` | Members of the bound. |
| Namespace members in value position | `Math.\|`, `intl.\|`, `m.\|` after `import "std:math" as m` | `namespaceAt` → `completionsFromNamespace` | Values and nested namespaces. A class appears as a constructor. Types are left out (#2675). |
| Enum variants in value position | `Color.\|`, `Color.Re\|` | `completionsFromNamespace` | Variants appear with the constructor kind. |
| Class statics | `Foo.\|` | switch `*ast.MemberExpr` | Static fields and methods. |
| Namespace members in type position | `val c: intl.\|` | switch `*ast.TypeRefTypeAnn` with `*ast.Member` → `qualifiedTypeCompletions` | Types and nested namespaces only. |
| Enum variants in a pattern | `Color.\|` and `Color.Re\|` in a `match` arm or `if val` | `extractorNameCompletions` | Constructors and nested namespaces only (#2712). |
| Keys inside empty index brackets | `p[\|]`, unclosed `p[` | `emptyIndexKeyCompletions` | Each key quoted, as `"x"`. Skipped for arrays, tuples and primitives (#2700). |
| Keys inside an empty indexed access type | `Point[\|]` | switch `*ast.ErrorTypeAnn` → `indexKeyCompletions` | Quoted keys with property signatures (#2701). |
| Named type reference with a prefix | `val p: Po\|`, `fn f(x: Po\|)`, `Array<Po\|>`, `extends Ani\|`, `T: Po\|` | switch `*ast.TypeRefTypeAnn` → `typeCompletionsFromScope` | Types and namespaces only. |
| Identifier read with a prefix | `poi\|` in an initializer, argument, operand, spread, array element, `yield`, `match` subject, `try` body or template placeholder | switch `*ast.IdentExpr` → `completionsFromScope` | Filtered by prefix. |
| Block-scoped bindings | parameters, `for ... in` bindings, `match` and `catch` bindings from identifier, object and tuple patterns, `if val` bindings, `do` and nested block locals | `completionsFromScope`, `completionsFromModuleScope` | A binding is offered only after its declaration in a block. |
| Module declarations across files | `hel\|` in one `lib/` file for a function in another | `collectModuleDeclBindings` | Top-level declarations are visible in any order, as the dependency graph allows. |
| File-scoped imports in `lib/` | `mat\|` after `import "std:math"` | `collectFileImportBindings` | Only in the file that imports. |
| New binding names | `val poi\|`, `val {x\|} = p`, `val [a\|] = t` | `shouldSuppressCompletions` | Returns an empty list. |

`bin/` and `lib/` give the same result in every row above.

## Missing or broken contexts

The cursor is written `|`. "Scope list" is the list described in [How the handler chooses a list](#how-the-handler-chooses-a-list).

### Identifiers, scope and values

| # | Context | Snippet | Now | TypeScript | Proposed |
|---|---|---|---|---|---|
| G1 | Identifier that is the object of a member expression | `val q = poi\|.x` | Nothing. `MemberExpr.Prop` is an `*ast.Ident`, so an `IdentExpr` whose parent is a `MemberExpr` is always its object, yet the `IdentExpr` case treats it as the property and lists `poi`'s members filtered by `poi`. | `point` | The scope list filtered by the prefix. |
| G2 | Locals at an empty prefix | `val zzLocal = 1`<br>`val q = \|` | The first 100 builtins in label order. `zzLocal` and every other lowercase local are cut, because uppercase labels sort first. | Locals first, then globals, ordered by `sortText` | Set `SortText` so that locals, then module names, then builtins come first, and cut after sorting. |
| G3 | Node is `nil` | `declare val p: Po\|`, `// poi\|`, a top-level `expo\|` in `lib/` | The scope list with no prefix filter, 100 items. | Filtered by the word before the cursor | Apply `wordAtCursor` on the `nil` path, as the `default` case does. |
| G4 | Prefix filter uses the whole word | `point.x\|b` with keys `xa` and `xb` | Only `xb`. The `MemberExpr` and `IdentExpr` cases filter by the node's full name, where the `default` case uses the text before the cursor. | `xa`, `xb`, filtered by the client on `x` | Filter by the text before the cursor in every case. |
| G5 | The binding being typed | `match e { Http\| }` | `Http` with type `HttpError \| string`, beside `HttpError`. The half-typed pattern binds `Http`, and the scope walk collects it. | `HttpError` only | Skip a binding whose span contains the cursor. |
| G6 | Bindings from extractor and instance patterns | `E.A(amount) => amo\|`<br>`HttpError{status} => sta\|`<br>`Error{message} => mess\|` in `catch` | Nothing for `amount` and `status`. `collectPatternBindings` handles identifier, object and tuple patterns but not `ExtractorPat` or `InstancePat`. 18 misses in the examples. | The bound name | Recurse into the arguments of `ExtractorPat` and the object pattern of `InstancePat`. |
| G7 | `self` as an identifier | `return sel\|` in a method | Nothing | `this` is a keyword in the list | Offer `self` inside a method, with its type as the detail. |
| G8 | Type-only names in value position | `type Point = {x: number}`<br>`val q = Poi\|` | `Point` beside `pointVal`. `collectScopeTypeBindings` adds types and namespaces to the value list. | Only values, classes, enums and namespaces | Leave out names that are only types. |
| G9 | Keywords | `retu\|` in a function body, `va\|` at a statement start, `i\|`, `true` | No keyword appears anywhere. `retu\|` offers `ReturnType`. | Keywords that fit the position, such as `return`, `if`, `const`, `true`, `await` in an async function | Offer statement keywords at a statement start, expression keywords such as `if`, `match`, `fn`, `await`, `true` and `null` in expression position, and declaration keywords at the top level. |
| G10 | Comments | `// poi\|`, `/* poi\| */` | The scope list, unfiltered (G3) | Nothing | Return an empty list inside a comment. `Script.Comments` and `Module.Comments` hold the spans. |
| G11 | Plain string and template text | `val s = "poi\|"`, `` `poi\|` `` | `point`. The `default` case filters the scope list by the word before the cursor. | Nothing unless the string has a literal type (G15) | Return an empty list inside string and template text, except where G15 applies. |
| G12 | After a numeric literal's dot | `val q = 1.\|` | The scope list | Nothing, since `1.` is a number | Return an empty list. |

### Types

| # | Context | Snippet | Now | TypeScript | Proposed |
|---|---|---|---|---|---|
| T1 | Empty type slot | `val p: \|`<br>`fn f(x: \|) {}`<br>`fn f() -> \| {}`<br>`type U = Point \| \|`<br>`type K = keyof \|`<br>`type F = fn (x: \|) -> number`<br>`interface B extends \| {}`<br>`class C implements \| {}` | The scope list, values included. The parser records no type node at the cursor, so the walk stops at the enclosing declaration. | Types, namespaces and type keywords | Offer the type list. Needs the parser to place an `ErrorTypeAnn` at the cursor, as it already does for `type O = {a: \|}`. |
| T2 | Empty object type value | `type O = {a: \|}` | Nothing. The cursor is on an `ErrorTypeAnn` whose parent is not an `IndexTypeAnn`. | Types | Offer the type list for any `ErrorTypeAnn` outside index brackets. |
| T3 | Primitive and keyword types | `val p: num\|`, `str\|`, `boo\|`, `unknown`, `never`, `null` | `Number`, `NumberFormatOptions`, `String`, `Boolean`. The lower-case keywords never appear. 156 misses in the examples. | `number`, `string`, `boolean`, `unknown`, `never`, `null`, `undefined`, `symbol`, `bigint`, `object`, `keyof`, `typeof` | Add the type keywords to every type list. |
| T4 | Type parameters | `fn f<Tee>(x: Te\|)`<br>`type Box<Elem> = {value: Ele\|}`<br>`class Box<Elem> { value: Ele\| }`<br>`enum R<Val> { Ok(value: Va\|) }` | `TemplateStringsArray` and other builtins, or nothing. `typeCompletionsFromScope` reads only the module or script scope. 25 misses in the examples. | The type parameter, kind `TypeParameter` | Collect the type parameters of each enclosing declaration, function type and method. |
| T5 | Type variables bound inside a type | `{[Key]: T[Ke\|] for Key in keyof T}`<br>`if T : Array<infer Elem> { Ele\| }` | Nothing | `K` from `in keyof T`, and `Elem` from `infer` in the true branch | Collect the mapped type's key variable inside its body, and `infer` variables inside the conditional's true branch. |
| T6 | `typeof` operand | `type C = typeof con\|` | `config` mixed with types such as `ConcatArray` and `Console` | Values only | Offer the value list. |
| T7 | Indexed access with a quoted key | `type X = Point["\|"]` | The scope list | `x`, `y` | Offer the keys inside the string, as `indexKeyCompletions` does for `Point[\|]`, with a text edit that replaces the string's contents. |
| T8 | `declare val` annotation | `declare val p: Po\|`, `declare val p: Point\|` | The walk finds no node, so the unfiltered scope list (G3) | `Point` | Find the type annotation of a `declare val`. `declare fn` parameters already work. |

### Members

| # | Context | Snippet | Now | TypeScript | Proposed |
|---|---|---|---|---|---|
| M1 | Mapped type receiver | `type Nums<T> = {[K]: T[K] for K in keyof T if T[K] : number}`<br>`declare val n: Nums<S>`<br>`n.\|` | Nothing. Also seen in `fancy-types/bin/main.esc` on `levels.` and `getters.` | The mapped keys | Expand the mapped type to an object before `info.MemberObject` reads it. |
| M2 | `super` | `return super.\|` in a subclass method | Nothing | The base class's members | Read the members of the base class's instance type. |
| M3 | Unannotated parameter | `fn f(p) { val a = p.lat`<br>`return p.\| }` | Nothing. `p`'s type is an open type variable. Also seen in `exact-types/lib/index.esc`. | `any` gives nothing, but an inferred object gives its members | Offer the properties the body has read from `p` so far, from the type variable's recorded bound. |
| M4 | Callback parameter in a half-typed body | `xs.map(fn (x, ...) { return x.\| })` with `xs: Array<{abc: number}>` | Nothing. `x` reads as `never`, while the complete body `return x.abc` checks. | `abc` | Check the callback with the error expression so `x` keeps its contextual type. |
| M5 | Optional property insert text | `p.\|` on `{abc?: number}` | Label `abc?` with no `InsertText`, so accepting it writes `abc?` | Label `abc?`, inserts `abc` | Set `InsertText` to the bare name. |
| M6 | Variant instance | `val v = E.A(1)`<br>`v.\|` with `enum E { A(amount: number) }` | Nothing | Not applicable. A TypeScript enum member offers `number` members. | Offer the variant's fields, if the type of `E.A(1)` records them. |

### Contextual values

| # | Context | Snippet | Now | TypeScript | Proposed |
|---|---|---|---|---|---|
| C1 | Object literal keys from the expected type | `val p: Point = {\|}`<br>`val p: Point = {x: 1, \|}`<br>`f({\|})` | The scope list | The keys not yet written | Offer the expected type's keys that the literal has not written, kind `Property`, with property signatures as the detail. |
| C2 | Partial object literal key | `val p: Point = {x\|}` with keys `xa` and `y` | Nothing. `{x}` parses as shorthand and the `default` case filters the scope list. | `xa` | Offer the expected type's keys, then the scope names a shorthand could read. |
| C3 | String literal union argument | `fn f(mode: "read" \| "write")`<br>`f("\|")` | The scope list | `read`, `write` | Offer the string members of the expected type, replacing the string's contents. |
| C4 | String literal in an assignment or comparison | `val m: Mode = "\|"`<br>`m == "\|"` | The scope list | The literal members | Same as C3, with the other operand's type for a comparison. |
| C5 | Empty argument | `add(\|)`, `add(1, \|)` | The scope list (G2) | The scope list, locals first | Fixed by G2. Ranking names whose type fits the parameter can follow. |
| C6 | Quoted key in an index expression | `p["x\|"]` | Nothing | `xa` | Offer the keys inside the string, extending `emptyIndexKeyCompletions`. |

### Patterns

| # | Context | Snippet | Now | TypeScript | Proposed |
|---|---|---|---|---|---|
| P1 | Destructuring keys in a declaration | `val {\|} = point`, `val {x\|} = point` | Empty, by `shouldSuppressCompletions` | The keys of `point`'s type | Offer the keys of the initializer's type in key position of an object pattern. Keep suppression for the name after a `:` and for identifier and tuple patterns. |
| P2 | Destructuring keys in a parameter | `fn f({\|}: Point) {}` | The scope list | The keys of `Point` | Offer the keys of the annotation's type. |
| P3 | Object pattern keys in a `match` arm | `match v { {\|} => 1 }` | The scope list | Not applicable | Offer the keys of the subject's type. |
| P4 | Instance pattern fields | `match e { HttpError{\|} => 1 }` | The scope list | Not applicable | Offer the class's fields. |
| P5 | Empty `match` arm | `match c { \| }` with `c: Color` | The scope list | In a `switch`, `case \|` offers the scope list | Offer the subject's variants as `Color.Red`, its literal members, and the class names in its union. |
| P6 | Literal pattern | `match u { "\|" => 1 }` with `u: "px" \| "cm"` | The scope list | `px`, `cm` in `case "\|"` | Offer the subject's string members not yet covered. |

### Imports, JSX and declarations

| # | Context | Snippet | Now | TypeScript | Proposed |
|---|---|---|---|---|---|
| I1 | Import path | `import "\|"`, `import "std:\|"`, `import "std:ma\|"` | The scope list, or `Map`, `MapIterator` and `Math` filtered by `ma` | Module specifiers | Offer the `std:` and `web:` packages. Add `"` and `:` as trigger characters. |
| J1 | JSX attribute names | `<Comp \| />`, `<Comp ti\| />` | The scope list, or nothing | `title` from the component's props | Offer the props type's keys not yet written. Needs the parser to keep a half-typed attribute. |
| J2 | JSX intrinsic attributes | `<div cl\| />` | Builtin types starting with `Class` | `className` and the other DOM attributes | Offer the attributes of the intrinsic element's type. |
| J3 | JSX tag name | `<Co\|` | Values and types mixed: `Comp`, `Console`, `CollatorOptions` | Components and intrinsic tags | Offer values that can be called as a component, and intrinsic tag names in lower case. Add `<` as a trigger character. |
| J4 | JSX text | `<div>poi\|</div>` | `point` | Nothing | Return an empty list in JSX text. |
| D1 | Class body member | `class Foo { x: number, \| }` | The scope list | Keywords, and base class members to override | Offer `get`, `set`, `static`, `constructor` and overridable base members. |

## Result quality

| Aspect | Now | TypeScript | Proposed |
|---|---|---|---|
| Sort order | Sorted by label only. No `SortText`. A list over 100 items is cut alphabetically and marked incomplete. The TODO on `sortAndLimit` names this. | Locals, then members, then globals, then keywords. Exact case matches rank first. | Set `SortText` by scope depth and kind, as in G2. |
| Kind of type aliases | `Struct`, as `Point` | A `type` kind, distinct from classes and interfaces | Use `Interface` for an alias to an object type, and `TypeParameter` for the names T4 adds. |
| Kind of enum variants | `Constructor` | `EnumMember` | Use `EnumMember`. |
| Detail of properties | `(property) x: number` from `propertyDetail` | `(property) x: number` | Unchanged. |
| Detail of methods | `fn () -> number`, without the name | `(method) Foo.bar(): number` | `(method) bar() -> number`, matching `propertyDetail`. |
| Detail of namespace values | `number` for `Math.PI`, `{new () -> Color}` for a variant | `(property) Math.PI: number`, `(enum member) Color.Red = 0` | `(property) PI: number` for a value, and the variant's constructor signature for a variant. |
| Detail of locals | The type alone, such as `{x: 1, y: 2}` | `const point: {x: number; y: number}` | `val point: {x: 1, y: 2}`, with `var` for a mutable binding. |
| Detail of classes | `Foo` | `class Foo` | `class Foo`. |
| Detail of builtins | Empty until `completionItem/resolve`. Resolve then returns a new item with only `Label`, `Kind` and `Detail`, dropping `FilterText`, `Data` and any future `InsertText`. | Filled on resolve | Copy the request item and set `Detail` on the copy. |
| Prefix filter | Case-insensitive prefix | Fuzzy, with camel-case humps | Keep the prefix filter, since clients filter again, but filter by the text before the cursor (G4). |
| Trigger characters | `.` | `.`, `"`, `'`, `` ` ``, `/`, `@`, `<`, `#`, space | Add `"`, `:` and `/` with I1, and `<` with J3. |
| `bin/` against `lib/` | Same lists, except that `lib/` drops top-level expression statements. `poi\|` or `f(a\|)` at the top of a `lib/` file finds no node and returns the unfiltered scope list. | Not applicable | Covered by G3. |

## Work plan

Each item is one pull request, in priority order. Sizes are rough: S is under a day, M is one to three days, L is more.

1. **Scope list ordering and filtering (S).** G1, G2, G3, G4, G5 and M5. Fix the `IdentExpr` case so it lists the scope when its parent is a `MemberExpr`. Add `SortText`, filter by the text before the cursor on every path, skip the binding under the cursor, and set `InsertText` on optional properties. This raises the 0 of 159 hits on `console.log`-style objects and makes locals visible at an empty prefix. No parser change.
2. **Missing bindings (S).** G6 and G7. Collect bindings from extractor and instance patterns, and offer `self` in methods. No parser change.
3. **Suppression in comments, strings and JSX text (S).** G10, G11, G12 and J4. Read comment spans from `Script.Comments` and `Module.Comments`, and string spans from the literal nodes. No parser change.
4. **Type position (M).** T1 to T8 and G8. Add type keywords, collect type parameters and type variables from the enclosing declarations, keep types out of value lists, list values after `typeof`, and find `declare val` annotations. T1 needs parser recovery: an empty type slot after `:`, `->`, `|`, `&`, `keyof`, `extends` and `implements` must produce an `ErrorTypeAnn` at the cursor, as `{a: |}` does.
5. **Keywords (M).** G9 and D1. Decide the keyword set for statement, expression, declaration and class-body positions. A statement start with nothing typed finds no node today, so this needs the node's enclosing block from the ancestor chain, not new parser recovery.
6. **Contextual object keys and destructuring keys (L).** C1, C2, P1, P2, P3 and P4. Needs a way to read the expected type at the cursor. The checker does not record one today, so this PR adds it to `solver.Info` for object literals, arguments and pattern targets. P1 also narrows `shouldSuppressCompletions` to the name positions of a pattern.
7. **Contextual string literals and `match` arms (M, after 6).** C3, C4, C6, T7, P5 and P6. Reuses the expected type from item 6. Needs a text edit that replaces the string's contents.
8. **Member completion on more receiver types (M).** M1, M2, M3, M4 and M6. M1 and M2 are in the server and the solver's `MemberObject`. M4 needs the checker to keep a callback parameter's contextual type when its body has an error, which is the same kind of recovery #2712 needed for a half-typed pattern.
9. **Import paths (M).** I1. List the `std:` and `web:` packages, and add trigger characters.
10. **JSX (L).** J1, J2 and J3. Needs parser recovery to keep a half-typed attribute name and an open tag, and intrinsic element types for J2.
11. **Detail and kind polish (S).** The rows of [Result quality](#result-quality) not covered above, and the resolve handler. Coordinate the detail formats with the hover work in #2704 so that hover and completion render a member the same way.
