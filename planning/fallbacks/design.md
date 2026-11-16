# Fallbacks for expressions that can raise

## Problem

`try`/`catch` is an expression, but handling one call with it takes five lines:

```esc
val x = try {
    thingThatCanThrow()
} catch {
    _ => valueOfLastResort
}
```

The common case wants none of what the block form offers. It has one expression
to guard, no arms to choose between, and one value to use instead. Rust code
reaches the same point with `result.unwrap_or(v)`, and Zig with `foo() catch v`.
Escalier has no short form, so code either pays for the block or moves the call
into a helper.

This document proposes an infix form that guards one expression and supplies a
fallback value, and shows how the same shape extends to type checks.

## Proposal

A `catch` written after an expression evaluates that expression. If it raises,
the result is the fallback on the right instead.

```esc
val port = Number.parseInt(env.PORT) catch 8080
val config = readConfig(userPath) catch readConfig(defaultPath) catch defaults
```

It is the block form with the braces and the catch-all arm removed. These two are
equivalent:

```esc
val x = a() catch b

val x = try { a() } catch { _ => b }
```

### Binding the caught value

The fallback can name the caught value with `binding =>`:

```esc
val n = parse(s) catch e => { log(e); 0 }
```

The binding has type `unknown`, the same as the caught value of a `try`
([06 Error Handling](../../docs/06_error_handling.md#the-caught-value-is-unknown)).
`ident =>` never starts an expression, because function expressions are written
`fn (…) { … }`, so the parser can tell the two right-hand forms apart with one
token of lookahead.

### Translating an error

A `throw` expression is a valid fallback, which gives a one-line way to convert
one error into another:

```esc
val cfg = JSON.parse(text) catch throw ConfigError(path)
```

## Semantics

### What the left operand guards

Everything evaluated by the left operand is guarded, exactly as if it were the
body of a `try` block. That includes every subexpression, not only the outermost
call:

```esc
val v = f(g(), h.prop) catch 0
// a raise from g(), from the getter for h.prop, or from f() yields 0
```

The raise sites the checker records today are the ones the left operand can
contain. Each one sends its known throws into the enclosing throws sink, and
`catch` replaces that sink for the left operand:

| Raise site | Example | Where the checker records it |
|---|---|---|
| A call, including an overloaded one and a constructor call | `parse(s)`, `Date(x)` | `applyCallShape`, `overload.go` |
| A getter read, including a read through destructuring | `obj.size`, `val {size} = obj` | `classes.go`, `pattern.go` |
| A setter write | `obj.size = 3` | `inferAccessorAssign` |
| An `await` of a promise with a rejection type | `await fetch(url)` | `inferAwait` |
| A `throw` expression | `throw E()` | `inferThrow` |
| Advancing an iterator | `yield* gen()` | `inferYield` through `constrainIterationRaise` |

`for … in` also advances an iterator, but it is a statement, so it can only reach
a `catch` through a function call or a `do` block. Spreading an iterable and
destructuring one with an array pattern do not appear as raise sites today. If
they are added later, `catch` picks them up with no change to this design.

### Type

The result type is the union of the left operand's type and the fallback's type:

```esc
declare fn parse(s: string) -> number throws ParseError
val n = parse(s) catch "invalid"
// n: number | "invalid"
```

### Known throws

The fallback is a catch-all, so every known throw of the left operand is
handled. None of them reach the enclosing function's clause:

```esc
declare fn parse(s: string) -> number throws ParseError
fn f(s: string) -> number { return parse(s) catch 0 }
// OK: f raises nothing
```

The fallback is evaluated in the enclosing throws sink, so what it raises does
reach the enclosing clause:

```esc
declare fn parse(s: string) -> number throws ParseError
declare fn parseLoose(s: string) -> number throws LooseError
fn f(s: string) throws _ { return parse(s) catch parseLoose(s) }
// inferred: fn (s: string) -> number throws LooseError
```

### Evaluation order and chaining

The fallback is evaluated only when the left operand raises. `catch` is
right-associative, so a chain reads as a list of attempts in order:

```esc
a() catch b() catch c
// groups as a() catch (b() catch c)
```

Left association would give the same result, since in both groupings `c` runs
exactly when `a()` and `b()` both raise. Right association lowers to a `try`
nested inside the outer `catch` clause rather than inside the outer `try` block,
which keeps each attempt's stack trace free of the earlier attempts' frames.

### Precedence

`catch` binds more loosely than every other binary operator, including `??` and
`||`. The left operand is therefore the whole expression before it, which is the
reading a `try` block gives:

```esc
x + f() catch 0          // (x + f()) catch 0
a() ?? b catch c         // (a() ?? b) catch c
a() catch b + 1          // a() catch (b + 1)
```

Assignment and `val` initializers take a `catch` expression as their right side,
so `y = f() catch 0` assigns the result of the whole `catch` expression.
`ast.Precedence` lists `Assign` at 10 today, so the parser change has to place
`catch` relative to how assignment is actually parsed, not relative to that
number.

Prefix operators bind tighter, so `await` and `!` sit inside the left operand:

```esc
await fetch(url) catch null   // (await fetch(url)) catch null
```

A `catch` on a new line continues the previous expression. A line cannot start
with `catch` otherwise, so this adds no ambiguity.

### Unawaited promises

A `catch` around an expression of type `Promise<T, E>` catches only what is
raised synchronously while the promise is created. The rejection is not caught.
This is almost always a missing `await`, so the checker warns when the left
operand's type is a promise whose `E` is not `never`:

```esc
val res = fetch(url) catch null
// WARNING: the left operand of `catch` is a promise, so its rejection is not
// caught; write `await fetch(url) catch null`
```

### Nothing to catch

A left operand that contains no raise site at all is reported, the way a `try`
with no catch arms is:

```esc
val n = 1 + 2 catch 0
// ERROR: the left operand of `catch` contains no call, `await`, accessor or
// `throw`, so it cannot raise; drop the `catch` and its fallback
```

A left operand that has raise sites whose known throws are all `never` is
accepted. The runtime can still raise something no signature named, and a
fallback for it is a reasonable thing to write.

### Ownership and liveness

On the fallback path the left operand may have stopped part way through. The
fallback sees the same state a `catch` arm sees after a `try` block: each move in
the left operand counts as possibly done, so the fallback cannot use a value the
left operand moved.

```esc
fn f(v: Buffer) -> Buffer {
    return consume(v) catch v
    // ERROR: `v` may have been moved by `consume(v)`
}
```

Whatever rule the liveness pass applies between a `try` block and its arms
applies here unchanged.

#### Raise edges

The ownership post-pass walks a control-flow graph that has an edge from each
known raise site. The [`defer` design][defer] checks its C6 rule on those edges,
since a raise that leaves a scope before a resource is scheduled for disposal
leaks it. A raise site in the left operand of `catch` has its edge to the
fallback's entry, not to the exit of the enclosing scope. So a guarded call
between an acquisition and its `defer` does not count as a leak path:

```esc
fn f(path: string, s: string) -> number throws IOError {
    val file = File.open(path)
    val n = parse(s) catch 0     // a raise here goes to the fallback, not out of f
    defer dispose(file)
    return n
}
```

#### Fallbacks that produce a resource

When both operands produce a value of a resource type, the C7 rule of the
[`defer` design][defer] carries a deferral along with the value into the binding.
Each operand is one path into that binding, so C6 requires both to agree on
whether the value arrives scheduled:

```esc
val file = do {
    val f = File.open(p)
    defer dispose(f)
    f.checkHeader()
    f
} catch File.open(q)
// ERROR: 'file' is scheduled for disposal on only some paths; write
// `defer dispose(…)` next to the acquisition in the fallback
```

Either both operands schedule their resource:

```esc
val file = do {
    val f = File.open(p)
    defer dispose(f)
    f.checkHeader()
    f
} catch do {
    val g = File.open(q)
    defer dispose(g)
    g
}
```

Or neither does, and one `defer` follows the binding:

```esc
val file = File.open(p) catch File.open(q)
defer dispose(file)
```

The second form only fits when nothing can raise between an acquisition and the
end of its operand. Above, `f.checkHeader()` can raise after `f` is acquired, so
leaving `f` unscheduled there would be a leak path.

### Inside a deferral

The C9 rule of the [`defer` design][defer] gives deferred code a `never` throws
sink, so a deferral cannot raise anything the checker knows of. A cleanup that
can fail is handled with a `catch`, usually the binding form, so the failure is
still recorded:

```esc
defer flush(&mut output) catch e => log.warn("flush failed", e)
```

`defer` takes an expression, and `catch` binds more loosely than every other
operator, so the whole `flush(…) catch …` is the deferred expression.

The fallback is part of the deferral too, so it is also checked against the
`never` sink. A fallback that translates the error is rejected there:

```esc
defer flush(&mut output) catch throw FlushError()
// ERROR: rejected by C9, since the fallback raises FlushError inside a deferral
```

C9's diagnostic suggests `catch`, so the binding form has to land no later than
`defer` does.

[defer]: https://github.com/escalier-lang/escalier/blob/claude/blissful-bell-aoxtyn/planning/resource_management/design.md

## Representation and lowering

### Keep a dedicated AST node

`catch` gets its own node rather than being rewritten into a `TryCatchExpr` by
the parser:

```go
type CatchExpr struct {
    Expr     Expr
    Binding  *IdentPat // nil when the fallback names no binding
    Fallback Expr
    span     Span
    commentSlots
}
```

A parser-level rewrite would cost three things:

1. The printer formats the AST back to source, so a rewrite would turn every
   `a() catch b` into the five-line block form the feature exists to avoid.
2. Diagnostics would describe a `try` block the user never wrote, and an error
   could not point at the left operand or the fallback by role.
3. The rewritten node needs a synthesized `_` arm with no source of its own,
   which has no honest span to give it.

### Checking

The solver checks a `CatchExpr` with the machinery `inferTryCatch` already uses.
That function installs a fresh throws sink over the enclosing one, infers the
block against it, and restores the enclosing sink before it checks the arms. The
step that installs and restores the sink becomes a helper both forms call.

For a `CatchExpr` the checker does the following:

1. Install a fresh throws sink and record its provenance against the
   `CatchExpr` node.
2. Infer `Expr` against that sink.
3. Restore the enclosing sink.
4. Bind `Binding`, if present, at `unknown`.
5. Infer `Fallback` against the enclosing sink.
6. Return the union of the two operand types.

Nothing is passed to `rethrowUnhandled`, since an unguarded catch-all leaves no
known throw uncovered.

### Codegen

The codegen builder already lowers a value-producing `try` into a temporary and a
`try` statement, passing the source node for each emitted node. `CatchExpr`
lowers the same way:

```esc
val x = parse(s) catch 0
```

```js
let $temp;
try {
  $temp = parse(s);
} catch {
  $temp = 0;
}
const x = $temp;
```

A chain nests in the `catch` clause:

```esc
val x = a() catch b() catch c
```

```js
let $temp;
try {
  $temp = a();
} catch {
  try {
    $temp = b();
  } catch {
    $temp = c;
  }
}
const x = $temp;
```

A binding becomes the `catch` parameter, renamed if it would collide.

A block with deferrals lowers to a `DisposableStack` held in a `using`
declaration (section 4.7 of the [`defer` design][defer]). When that block is the
left operand of `catch`, the stack sits inside the JavaScript `try` block. Its
deferrals therefore run before the fallback, which is the order section 4.9 of
the `defer` design specifies for a `try` block and its arms:

```esc
val n = do {
    val f = File.open(p)
    defer dispose(f)
    countLines(&f)
} catch 0
```

```js
let $temp;
try {
  using $stack = new DisposableStack();
  const f = File.open(p);
  $stack.defer(() => dispose(f));
  $temp = countLines(f);
} catch {
  $temp = 0;
}
const n = $temp;
```

This shows the native form. Targets without native `using` get the downlevel
helper the `defer` design describes.

When a resource moves out of a block into a binding, C7 registers its deferral
again on the destination block's stack, at the point of the move. In this
lowering the value reaches the binding through `$temp`, so codegen treats
`const file = $temp` as the move and registers the deferral there. The inner
block clears its own cancellation flag at the assignment to `$temp`. Both
operands reach the binding through `$temp`, so one registration covers both
paths.

### Source maps and provenance

Each emitted node carries a source node, and the mapping is chosen so a debugger
steps through the operands, not through the generated scaffolding:

| Emitted node | Source node |
|---|---|
| `let $temp`, the `try` statement, the `catch` clause | the `CatchExpr` |
| The assignment in the `try` block | `Expr` |
| The assignment in the `catch` clause | `Fallback` |
| The `catch` parameter | `Binding` |

The throws sink records its provenance against the `CatchExpr`, so a diagnostic
that traces a type back through the sink names the `catch` the user wrote.

The builder hoists a value-producing `try` out of its surrounding expression into
statements placed before it. A `catch` nested in a larger expression inherits
whatever ordering that hoisting gives. In `f(g(), a() catch 0)`, `g()` must still
run before `a()`, and a fixture has to assert this before the feature ships.

## Spelling

The proposal spells the operator `catch`. The alternatives considered:

| Spelling | Example | Assessment |
|---|---|---|
| `catch` | `a() catch b` | Reuses the keyword that already means "handle a raise". Zig uses the same form. Leaves room for `catch e => …` and `catch { arms }`. |
| `?!` | `a() ?! b` | Reads well and does not collide with an existing token. Its meaning has to be learned, and it has no natural place for a binding or arms. |
| `!?` | `a() !? b` | As `?!`. Close enough to `?!` that the two are easy to mix up. |
| `?` | `a() ? b` | Collides with `?.`, the optional call `?(`, the optional markers on parameters and properties, and the postfix `?` the planned `Result` type reserves. |
| `try … catch` without braces | `try a() catch b` | Marks the guarded region at both ends, but a chain reads as `try a() catch try b() catch c`. |
| `try?` then `??` | `try? a() ?? b` | Swift's form. It turns a raise into `undefined`, so it cannot tell a raise from a function that returns `undefined`. |

`catch` is recommended because the reader already knows what it means, and
because the binding and arm forms below extend it without new syntax. `?!` is
the best of the symbolic options if a symbol is preferred, and every section
above applies to it unchanged apart from the binding form.

## Extensions

### Selective arms

A `catch` followed by a brace-delimited arm list is the block form without its
`try`:

```esc
val n = parse(s) catch {
    ParseError{pos} => -pos,
    TimeoutError => 0,
}
```

Arms that leave known throws uncovered rethrow them, as in a `try`. This form is
not part of the first version. A brace after `catch` would otherwise be an
object literal or a `do`-less block, so the parser has to scan for `=>` to tell
them apart. `{a, b} => …` (an object pattern) and `{a, b}` (an object literal)
share a prefix, so the scan can be long. The first version rejects `{` directly
after `catch` and asks for parentheses or a `do` block.

### The same forms on `try` blocks

A handler is the part after `catch`. It comes in three forms:

| Form | After an expression | After a `try` block |
|---|---|---|
| Fallback | `a() catch 0` | `try { … } catch 0` |
| Binding | `a() catch e => report(e)` | `try { … } catch e => report(e)` |
| Arms | `a() catch { ParseError => 0 }` (extension) | `try { … } catch { ParseError => 0 }` (exists today) |

A `try` block accepts all three. The fallback and binding forms on a `try` block
are part of the first version, since the block form needs them as much as the
expression form does:

```esc
val cfg = try {
    val text = fs.readFileSync(path)
    JSON.parse(text)
} catch defaults
```

The forms mean the same thing in both positions:

- A fallback is a single unguarded `_` arm.
- A binding is a single unguarded arm whose pattern is that identifier, so
  `catch e => x` and `catch { e => x }` are equivalent.
- Arms follow the rules in
  [06 Error Handling](../../docs/06_error_handling.md#try--catch--finally),
  including the rethrow of known throws no arm covers.

After a `try` block, a `{` following `catch` always starts an arm list, as it
does today. A fallback that needs statements is written `catch do { … }`, and an
object literal fallback is written in parentheses:

```esc
val cfg = try { loadConfig(path) } catch ({port: 8080, host: "localhost"})
val cfg = loadConfig(path) catch ({port: 8080, host: "localhost"})
```

This keeps every existing `try` parsing as it does now, and it gives the
expression form the same rule once the arms extension lands.

Neither `catch` on an expression nor `catch` on a `try` block takes a `finally`
block. Cleanup in a `finally` sits at the end of the guarded block, which in a
long block is far from where the resource was acquired. The [`defer` design][defer]
removes `finally` and replaces it with a `defer` statement written next to the
acquisition (its section 4.9).

#### Representation

Both nodes hold the handler in one type, so the checker and codegen handle each
form in one place, and the printer reproduces the form the user wrote:

```go
type CatchHandler struct {
    Binding  *IdentPat   // set for the binding form
    Fallback Expr        // set for the fallback and binding forms
    Arms     []*MatchCase // set for the arms form
    span     Span
}

type TryCatchExpr struct {
    Try     Block
    Handler *CatchHandler
    // …
}

type CatchExpr struct {
    Expr    Expr
    Handler *CatchHandler
    // …
}
```

The checker and codegen read the fallback and binding forms as the single arm
each is equivalent to. They then reuse the arm code `TryCatchExpr` already has.

#### Whether `try` is still needed

With infix `catch` on any expression, `try { … } catch h` and `do { … } catch h`
mean the same thing. This proposal keeps `try` for two reasons:

1. It marks where the guarded region starts. In `do { … } catch h` a reader
   learns the block is guarded only on reaching its end, which for a long block
   is a screen away.
2. Every `try` written today keeps working, with no migration.

`do { … } catch h` is accepted, since it is ordinary infix `catch` on a `do`
expression, and the printer leaves it as written. Whether the checker should
suggest `try` for it is listed under the open questions.

### Type checks

Nullability already has its fallback, `??`. A failed type check has
`val x: T = u else { fallback }` and `if val`
([04 Pattern Matching](../../docs/04_pattern_matching.md)), but both are
statements or full conditional expressions. The expression-level counterpart
follows Kotlin's `as?`: a checked narrowing that produces `undefined` when the
check fails, so that `??` supplies the fallback.

```esc
fn port(u: unknown) -> number {
    return u as? number ?? 8080
}
```

`u as? T` has type `T | undefined` and lowers to the runtime test the pattern
`x: T` already produces. It accepts only the types a pattern can test at runtime:
primitives, classes, literal types, and object and tuple shapes built from them.
A `T` that itself admits `undefined` is rejected, since the result could no
longer tell a failed check from a value that passed it.

This keeps one fallback operator per failure channel:

| Failure | Detected by | Fallback |
|---|---|---|
| The value is `null` or `undefined` | the value | `??` |
| The value fails a type test | `as? T` | `??` |
| Evaluation raises | the throws sink | `catch` |

`as?` is a separate proposal. It is listed here to show that `catch` does not need
to grow a type-check mode.

### `Result`

The planned `Result<T, E>` represents failure as a value, not as a raise, so
`catch` does not apply to it. `catch` gives a one-line conversion in each
direction:

```esc
val r = Result.Ok(parse(s)) catch e => Result.Err(e)
```

## Editor cues

A call's name rarely says whether it raises, so a reader can't tell where a
`catch` belongs without checking each callee's signature. The checker already
reports an unhandled known throw in a function with no `throws` clause. The
gap is in a function that propagates with `throws _` or a declared clause,
where a raising call is legal and nothing on the line shows it. The language
server closes that gap without changing the language.

### A `throws` semantic token modifier

The server adds a `throws` modifier to the legend in
[cmd/lsp-server/semantic_tokens.go](../../cmd/lsp-server/semantic_tokens.go) and
sets it on every raise site whose known throws are not `never`. The token it
marks depends on the site:

| Raise site | Marked token |
|---|---|
| A call or constructor call | the callee's name, e.g. `parse` in `json.parse(s)` |
| A getter read or setter write | the property name, e.g. `size` in `obj.size` |
| An `await` | the `await` keyword |
| Advancing an iterator in `yield*` | the `yield` keyword |

A `throw` expression is not marked, since its keyword already says it raises.

The modifier is set whether or not the site sits inside a `catch` or `try`.
The style then reads as "this raises" everywhere, and a reader can see which
expressions a `catch` guards.

Monaco theme rules accept four font styles: `italic`, `bold`, `underline` and
`strikethrough`. The playground in
[playground/src/semantic-tokens.ts](../../playground/src/semantic-tokens.ts)
already underlines a `mutable` name and bolds a `reassignable` one, so `throws`
takes italic. A token can carry more than one modifier, and the playground's
`modifierTokenRules` already emits a combined rule for each set, so a `mut`
binding holding a raising getter reads as underlined italic. The VS Code
extension in [packages/vscode-escalier/](../../packages/vscode-escalier/) maps
the modifier to the same style.

### Hover

Hover on a marked token shows what that site raises, e.g.
`raises ParseError | RangeError`. This is the site's own known throws, not the
callee's full signature, so a getter on a union receiver shows the union of
what each member's getter raises.

### Quick fixes

The unhandled-throw diagnostic offers three code actions:

1. **Add a `catch` fallback.** It appends ` catch ?fallback` to the full
   expression of the statement that holds the raise site, which is what the
   left operand of `catch` covers given its precedence. `?fallback` is a typed
   hole ([planning/typed_holes/design.md](../typed_holes/design.md)), so the
   checker reports the type the fallback needs. Until typed holes land, the
   action inserts `catch undefined` and selects `undefined`.
2. **Wrap in `try`.** It wraps the statement in `try { … } catch ?fallback`.
3. **Declare `throws _`.** It adds `throws _` to the enclosing function, so the
   throws propagate to its callers.

The no-raise-site error offers one action, which removes the `catch` and its
fallback.

## Implementation plan

1. Parser. Lex `catch` after a complete expression as an infix keyword at the
   lowest binary precedence, right-associative, with the `ident =>` binding form.
   Add `CatchHandler` and `CatchExpr` to [internal/ast/](../../internal/ast/)
   with visitor support, and move `TryCatchExpr`'s arms into a `CatchHandler`.
   Parse the fallback and binding forms after a `try` block. Print both nodes
   in [internal/printer/](../../internal/printer/) with parentheses wherever
   precedence needs them. Snapshot tests cover chains, precedence
   against `??`, `||` and `+`, `await`, and assignment.
2. Solver. Factor the sink install and restore out of `inferTryCatch` and check
   `CatchExpr` with it. Add the no-raise-site error and the unawaited-promise
   warning. Tests in a new `internal/solver/infer_catch_expr_test.go` assert the
   result type, that known throws do not reach the enclosing clause, that the
   fallback's throws do, and that a move in the left operand blocks a use in the
   fallback. The binding form lands no later than the `defer` design's C9
   check, whose diagnostic suggests it. Once `defer` lands, add tests for the
   raise edges and resource-typed fallbacks in [Ownership and
   liveness](#ownership-and-liveness), and fixtures for a `do` block with
   deferrals as the left operand.
3. Codegen. Lower `CatchExpr` beside `TryCatchExpr` in
   [internal/codegen/builder.go](../../internal/codegen/builder.go) with the
   source-node mapping above. Fixtures cover a chain, a binding, a fallback that
   throws, the argument-order case `f(g(), a() catch 0)`, and the source map for
   each.
4. Editor. Add the `throws` modifier, hover text and quick fixes from
   [Editor cues](#editor-cues) to [cmd/lsp-server/](../../cmd/lsp-server/), and
   style the modifier in the playground and the VS Code extension. Tests in
   `cmd/lsp-server/semantic_tokens_test.go` cover each raise site in the table.
5. Docs. Add a section to
   [docs/06_error_handling.md](../../docs/06_error_handling.md) and replace the
   description of `try`/`catch` as heavy for a single call.

## Open questions

- Should the no-raise-site case be an error or a warning? A `try` with no arms is
  an error, but that `try` cannot do anything, whereas a needless fallback still
  runs correctly.
- When throwing is restricted to `Error` subclasses, the binding's type becomes
  `Error` for Escalier code. Should the binding follow the same setting that
  decides this for `try`?
- Should the checker suggest `try { … } catch h` when it sees
  `do { … } catch h`? The two compile to the same code, so a suggestion would
  only enforce the style that marks where the guarded region starts.
- Should `catch` on an expression require a `try` before it, as in
  `try parse(s) catch 0`? Swift requires `try` before every expression that
  calls a throwing function, and the marker would show where the guarded region
  starts in source as well as in an editor. It costs a word on every fallback,
  and a chain becomes `try a() catch try b() catch c`. An alternative is to
  require `try expr` only where the expression's throws propagate rather than
  being caught, which marks the sites the checker does not otherwise report.
