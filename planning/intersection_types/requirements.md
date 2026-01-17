# Intersection Types

## Syntax

Intersection types in Escalier use the `&` operator to combine multiple types:

```
Type1 & Type2 & Type3
```

Examples:
```
type Person = {
    name: string,
    age: number
}

type Employee = {
    employeeId: number,
    department: string
}

type Manager = Person & Employee & {
    reports: Array<Employee>
}
```

This is the same syntax as TypeScript:
```ts
type Person = {
    name: string;
    age: number;
};

type Employee = {
    employeeId: number;
    department: string;
};

type Manager = Person & Employee & {
    reports: Array<Employee>;
};
```

## Semantics

Intersection types in Escalier have the same semantics as intersection types in TypeScript. An intersection type combines multiple types into one, requiring a value to have all properties from all constituent types.

### Basic Intersection

The intersection of two object types creates a new type that has all properties from both types:

```
type A = { x: number }
type B = { y: string }
type C = A & B // { x: number, y: string }

val obj: C = { x: 5, y: "hello" } // valid
```

### Intersection with Primitives

When intersecting incompatible primitive types, the result is `never`:

```
type Invalid = string & number // equivalent to never
type AlsoInvalid = string & boolean // equivalent to never
type AlsoNever = number & boolean // equivalent to never
```

The same primitive type intersected with itself is just that type:
```
type Same = string & string // string
type AlsoSame = number & number // number
```

### Intersection of Primitives with Object Types

Primitives can be intersected with object types, creating types that behave as both the primitive and the object type:

```
type StringWithLength = string & { length: number }
type NumberWithToFixed = number & { toFixed: fn(digits: number) -> string }

// The resulting type has properties from both
declare val str: StringWithLength
val len: number = str.length  // valid - from object type
val upper: string = str.toUpperCase()  // valid - from string primitive
```

The intersection type is a subtype of the primitive type:

```
type EnhancedString = string & { metadata: { source: string } }

declare val enhanced: EnhancedString
val plain: string = enhanced  // valid - EnhancedString is subtype of string
```

However, a plain primitive cannot be assigned to the intersection type:

```
type CustomNumber = number & { id: string }

declare val num: number
val custom: CustomNumber = num  // ERROR: number missing property 'id'
```

#### Branded Types

A common use case for primitive-object intersections is creating branded types for nominal typing:

```
type Email = string & { __brand: "email" }
type Currency = number & { __brand: "currency" }
type UserId = number & { __brand: "userId" }

// These types are structurally distinct even though they're based on strings or numbers
declare val email: Email
declare val str: string

val e: Email = email      // valid
val s: string = email     // valid (Email is subtype of string)
val x: Email = str        // ERROR: string is not assignable to Email
```

Branded types provide type safety while preserving compatibility with the underlying primitive:

```
type Celsius = number & { __brand: "celsius" }
type Fahrenheit = number & { __brand: "fahrenheit" }

fn toCelsius(f: Fahrenheit) -> Celsius {
    return ((f - 32) * 5 / 9) as Celsius
}

fn toFahrenheit(c: Celsius) -> Fahrenheit {
    return ((c * 9 / 5) + 32) as Fahrenheit
}

declare val temp: Celsius
val result = toFahrenheit(temp)  // valid
val invalid = toCelsius(temp)    // ERROR: Celsius is not assignable to Fahrenheit
```

The brand property (or any property in the object type) doesn't need to exist at runtime—it's only used for type checking:

```
fn createEmail(str: string) -> Email {
    // Validation logic here
    return str as Email  // Cast after validation
}

val userEmail: Email = createEmail("user@example.com")
console.log(userEmail)  // Just a string at runtime
```

#### Other Use Cases

Primitive-object intersections can also be used to add methods or metadata to primitives:

```
type RichNumber = number & {
    format: fn() -> string,
    currency: string
}

type ValidatedString = string & {
    isValid: boolean,
    validatedAt: number
}
```

### Intersection with Object Types

Object types can be intersected to combine their properties:

```
type Colorful = { color: string }
type Circle = { radius: number }
type ColorfulCircle = Colorful & Circle

val cc: ColorfulCircle = {
    color: "red",
    radius: 42
}
```

If the same property exists in multiple types with different types, the property type in the intersection is itself an intersection:

```
type A = { x: number, y: string }
type B = { x: string, z: boolean }
type C = A & B // { x: number & string, y: string, z: boolean }
                // which simplifies to { x: never, y: string, z: boolean }
```

If the same property exists with compatible types, the intersection resolves to the more specific type:

```
type A = { x: string | number }
type B = { x: number }
type C = A & B // { x: number }
```

### Intersection with Functions

Function types can be intersected to create overloaded function types:

```
type F1 = fn(x: string) -> number
type F2 = fn(x: number) -> string
type F3 = F1 & F2

// F3 is callable with either signature
declare val f: F3
val result1: number = f("hello")  // valid
val result2: string = f(42)       // valid
```

### Intersection with Union Types

Intersection types distribute over union types:

```
type A = { a: string }
type B = { b: number }
type C = { c: boolean }

type Result = A & (B | C)
// Distributes to: (A & B) | (A & C)
// Which is: { a: string, b: number } | { a: string, c: boolean }
```

### Intersection with Interfaces

Interfaces can be intersected just like type aliases:

```
interface Named {
    name: string
}

interface Aged {
    age: number
}

type Person = Named & Aged

val person: Person = {
    name: "Alice",
    age: 30
}
```

### Intersection with Generic Types

Generic types can be intersected:

```
type WithTimestamp<T> = T & { timestamp: number }

type User = { name: string, email: string }
type TimestampedUser = WithTimestamp<User>
// { name: string, email: string, timestamp: number }
```

### Intersection Order

The order of types in an intersection does not matter:

```
type A = { x: number }
type B = { y: string }

type C1 = A & B // { x: number, y: string }
type C2 = B & A // { x: number, y: string }
// C1 and C2 are equivalent
```

### Intersection with `never` and `unknown`

- `never & T` is always `never` for any type `T`
- `unknown & T` is always `T` for any type `T`

```
type N = never & string    // never
type U = unknown & number  // number
```

### Intersection with `any`

- `any & T` is always `any` for any type `T`

```
type A = any & string  // any
```

### Nested Intersections

Intersections can be nested and will be flattened:

```
type A = { x: number }
type B = { y: string }
type C = { z: boolean }

type D = (A & B) & C // Same as A & B & C
// { x: number, y: string, z: boolean }
```

### Intersection with Readonly Properties

When intersecting types with readonly properties:

```
type A = { readonly x: number }
type B = { x: number }
type C = A & B // { readonly x: number }

// The more restrictive modifier (readonly) wins
```

### Intersection with Mutable Object Types

Object types (interfaces, classes, etc.) can be marked as mutable. When intersecting mutable and immutable object types:

```
type A = mut { x: number }
type B = { x: number }
type C = A & B // { x: number }

// (mut T) & T is equivalent to T
// The immutable type wins
```

### Intersection with Optional Properties

When intersecting types with optional properties:

```
type A = { x?: number }
type B = { x: number }
type C = A & B // { x: number }

// The required property wins over optional
```

If both are optional:
```
type A = { x?: number }
type B = { x?: string }
type C = A & B // { x?: number & string } which is { x?: never }
```

## Type Checking Rules

### Assignment

A value of type `A & B` can be assigned to a variable of type `A` or type `B`:

```
type AB = { x: number } & { y: string }
type A = { x: number }
type B = { y: string }

val ab: AB = { x: 1, y: "hello" }
val a: A = ab  // valid
val b: B = ab  // valid
```

### Subtyping

`A & B` is a subtype of both `A` and `B`:

```
fn requiresA(a: { x: number }) -> undefined { }
fn requiresB(b: { y: string }) -> undefined { }

val ab: { x: number } & { y: string } = { x: 1, y: "hello" }
requiresA(ab)  // valid
requiresB(ab)  // valid
```

### Type Narrowing

Type guards work with intersection types:

```
type A = { kind: "a", x: number }
type B = { kind: "b", y: string }
type C = { z: boolean }

fn process(value: (A | B) & C) -> undefined {
    if value.kind == "a" {
        // value is narrowed to A & C
        console.log(value.x, value.z)
    } else {
        // value is narrowed to B & C
        console.log(value.y, value.z)
    }
}
```

## Implementation Notes

### Subtyping Rules

The following rules govern subtyping relationships with intersection types:

#### Basic Intersection Subtyping

An intersection type `A & B` is a subtype of both `A` and `B`:
- `A & B <: A`
- `A & B <: B`

```
type AB = { x: number } & { y: string }
type A = { x: number }
type B = { y: string }

// AB is assignable to both A and B
declare val ab: AB
val a: A = ab  // valid
val b: B = ab  // valid
```

#### Subtyping from Constituent Types

If a type `T` is a subtype of all types in an intersection, then `T` is a subtype of the intersection:
- If `T <: A` and `T <: B`, then `T <: A & B`

```
type AB = { x: number } & { y: string }
type ABC = { x: number, y: string, z: boolean }

// ABC has all properties of A and B, so ABC <: AB
declare val abc: ABC
val ab: AB = abc  // valid
```

#### Intersection with Multiple Types

For an intersection with multiple types `A & B & C`:
- `A & B & C <: A`
- `A & B & C <: B`
- `A & B & C <: C`
- `A & B & C <: A & B`
- `A & B & C <: B & C`
- `A & B & C <: A & C`

```
type ABC = { x: number } & { y: string } & { z: boolean }
type AB = { x: number } & { y: string }

declare val abc: ABC
val ab: AB = abc  // valid - ABC is more specific than AB
```

#### Intersection Type Subtyping

Between two intersection types, subtyping follows from constituent subtyping:
- If `A & B <: C & D`, then for each type in `C & D`, `A & B` must be a subtype of it
- Equivalently: `A & B` must be a subtype of both `C` and `D`

```
type AB = { x: number } & { y: string }
type A = { x: number }
type B = { y: string }

// AB <: A & B is true (they're equivalent)
// AB <: A is true
// AB <: B is true
```

More complex example:
```
type ABC = { x: number } & { y: string } & { z: boolean }
type AB = { x: number } & { y: string }
type AC = { x: number } & { z: boolean }

// ABC <: AB is true (ABC is subtype of both A and B)
// ABC <: AC is true (ABC is subtype of both A and C)
// AB <: AC is false (AB is not subtype of C)
// AC <: AB is false (AC is not subtype of B)
```

#### Object Type Subtyping

When comparing intersection types with object types:
- `A & B <: {x: T}` if the merged properties of `A & B` make it a subtype of `{x: T}`

```
type A = { x: number, y: string }
type B = { z: boolean }
type AB = A & B  // { x: number, y: string, z: boolean }

type X = { x: number }

// AB <: X because AB has property x with type number
declare val ab: AB
val x: X = ab  // valid
```

#### Primitive Intersection Subtyping

For primitive-object intersections:
- `P & O <: P` where `P` is a primitive type and `O` is an object type
- `P <: P & O` is false (primitive doesn't have object properties)

```
type EnhancedString = string & { metadata: string }

declare val enhanced: EnhancedString
val str: string = enhanced  // valid - EnhancedString <: string

declare val plain: string
val bad: EnhancedString = plain  // ERROR - string is not <: EnhancedString
```

#### Union Distribution

Intersection types distribute over union types for subtyping:
- `(A | B) & C <: (A & C) | (B & C)`

```
type A = { a: string }
type B = { b: number }
type C = { c: boolean }

type Result = (A | B) & C
// This is equivalent to (A & C) | (B & C)
```

#### Function Intersection Subtyping

For function intersections, the intersection type is a subtype of each function type:
- `(fn(x: A) -> B) & (fn(x: C) -> D) <: fn(x: A) -> B`
- `(fn(x: A) -> B) & (fn(x: C) -> D) <: fn(x: C) -> D`

```
type F1 = fn(x: string) -> number
type F2 = fn(x: number) -> string
type Both = F1 & F2

declare val both: Both
val f1: F1 = both  // valid
val f2: F2 = both  // valid
```

#### Never and Unknown

Special cases with `never` and `unknown`:
- `never <: A & B` for any types `A` and `B`
- `A & B <: unknown` for any types `A` and `B`
- `never & T` is equivalent to `never`, and `never <: anything`
- `unknown & T` is equivalent to `T`

### Normalization

The type checker should normalize intersection types by:
1. Flattening nested intersections: `(A & B) & C` → `A & B & C`
2. Removing duplicates: `A & A` → `A`
3. Simplifying with `never`: `A & never` → `never`
4. Simplifying with `unknown`: `A & unknown` → `A`
5. Simplifying with `any`: `A & any` → `any`
6. Merging object types when possible

### Circularity

Circular intersection types should be detected and handled:

```
type Circular = { next: Circular } & { value: number }
// Should be allowed and represent: { next: Circular, value: number }
```

### Error Messages

When a value doesn't match an intersection type, error messages should clearly indicate which constituent type(s) are not satisfied:

```
val obj: { x: number } & { y: string } = { x: 5 }
// Error: Object is missing property 'y' required by intersection type
```

## Interoperability with TypeScript

Intersection types in Escalier should be compiled to TypeScript intersection types using the `&` operator. The generated `.d.ts` files should preserve intersection types as-is:

```
// Escalier
type Person = { name: string } & { age: number }

// Generated .d.ts
type Person = { name: string } & { age: number };
```

## Examples

### Mixins

Intersection types are useful for implementing mixin patterns:

```
type Constructor<T> = fn() -> T

fn Timestamped<T>(Base: Constructor<T>) -> Constructor<T & { timestamp: number }> {
    return fn() {
        return { ...Base(), timestamp: Date.now() }
    }
}
```

### API Responses

Intersection types can model API responses with common fields:

```
type BaseResponse = {
    status: number,
    timestamp: number
}

type UserResponse = BaseResponse & {
    data: { id: number, name: string }
}

type ErrorResponse = BaseResponse & {
    error: string
}
```

### Configuration Objects

Intersection types can combine configuration options:

```
type BaseConfig = {
    debug: boolean,
    verbose: boolean
}

type ServerConfig = BaseConfig & {
    port: number,
    host: string
}

type ClientConfig = BaseConfig & {
    timeout: number,
    retries: number
}
```
