# Conditional Types

## Syntax

The syntax for conditional types uses the following pattern:
```
if `CheckType` : `ExtendsType` { `TrueType` } else { `FalseType` }
```

These are some examples of conditional types being used in type aliases in
Escalier:
```
type Exclude<T, U> = if T : U { never } else { T }
type Extract<T, U> = if T : U { T } else { never }
type Parameters<T : fn(...args: any) -> any> = if T : fn(...args: infer P) -> any ? P : never;
type ReturnType<T : fn(...args: any) -> any> = if T : fn(...args: any) -> infer R ? R : never;
```

These are the equivalent TypeScript types:
```ts
type Exclude<T, U> = T extends U ? never : T;
type Extract<T, U> = T extends U ? T : never;
type Parameters<T extends (...args: any) => any> = T extends (...args: infer P) => any ? P : never;
type ReturnType<T extends (...args: any) => any> = T extends (...args: any) => infer R ? R : never;
```

In Escalier, conditional types can be chained using `else if`:
```
type OmitThisParameter<T> = if unknown : ThisParameterType<T> { 
    T
} else if T : fn(...args: infer A) -> infer R {
    fn(...args: A) -> R
} else {
    T
}
```

## Semantics

Conditional types in Escalier have the same semantics as conditional types in
TypeScript.

### Basics

If the `CheckType` extends the `ExtendsType` then the conditional type evaluates to the `TrueType`, otherwise it evalutes to the `FalseType`.

```
type Flatten<T> = if T : Array<any> { T[number] } else { T }

// Extracts out the element type.
type Str = Flatten<Array<string>> // inferred as `string`

// Leaves the type alone.
type Num = Flatten<number> // inferred as `number`
```

### Distributive Conditional Types

When conditional types act on a generic type, they become distributive when given a union type.

```
type ToArray<Type> = if Type : any { Type[] } else { never }
```

If we plug a union type into ToArray, then the conditional type will be applied to each member of that union.
```
type ToArray<Type> = if Type : any { Type[] } else { never }
type StrArrOrNumArr = ToArray<string | number> // inferred as `string[] | number[]`
```

What happens here is that ToArray distributes on:
```
string | number
```
and maps over each member type of the union, to what is effectively:
```
ToArray<string> | ToArray<number>
```
which leaves us with:
```
Array<string> | Array<number>
```

### Inferring Within Conditional Types

Conditional types provide us with a way to infer from types we compare against in the true branch using the infer keyword. For example, we could have inferred the element type in Flatten instead of fetching it out “manually” with an indexed access type:
```
type Flatten<Type> = if Type : Array<infer Item> { Item } else { Type }
```

Here, we used the infer keyword to declaratively introduce a new generic type variable named Item instead of specifying how to retrieve the element type of Type within the true branch. This frees us from having to think about how to dig through and probing apart the structure of the types we’re interested in.

We can write some useful helper type aliases using the infer keyword. For example, for simple cases, we can extract the return type out from function types:
```
type GetReturnType<Type> = if Type : fn(...args: never[]) -> infer Return {
    Return
} else {
    never
}
 
type Num = GetReturnType<fn() -> number> // inferred as `number`
type Str = GetReturnType<fn (x: string) -> string> // inferred as `string`
type Bools = GetReturnType<fn (a: boolean, b: boolean) -> Array<boolean>> // inferred as `Array<boolean>`
```
