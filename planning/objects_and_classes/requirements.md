# Objects & Classes

## Objects

Escalier object destructuring has the following capabilities:
- shorthand
- provide default values (useful when properties are optional)
- inline type annotations (this prevents having to provide a top-level type 
  annotation that repeats the same structure at the type-level)

You can combine these different capabilities in any combination.

```ts
// destructing
val {x, y} = p
val {x = 0, y = 0} = p
val {x: a, y : b} = p
val {x: a = 0, y: b = 0} = p
val {x::number, y::number} = p // val {x, y}: {x: number, y: number} = p
val {x::number = 0, y::number = 0} = p // val {x = 0, y = 0}: {x: number, y: number} = p
val {x: a:number, y: b:number} = p // val {x: a, y: b}: {x: number, y: number} = p
val {x: a:number = 0, y: b:number = 0} = p // val {x: a = 0, y: b = 0}: {x: number, y: number} = p

// example: destructuring + type annotation in function param
fn length({x::number, y::number}) {
    Math.sqrt(x*x + y*y)
}
```

Object literals have the following capabilities:
- shorthand
- inline type annotations (this can be useful for one-off objects)

```ts
// object literals
val p = {x, y}
val p = {x: 5, y: 10}
val p = {x::number, y::number} // val p = {x, y} as {x: number, y: number}
val p = {x: 5:number, y: 10:number} // val p = {x: 5, y: 10} as {x: number, y: number}
```

Objects can be defined over multiple lines, e.g.

```ts
val {
    x::number,
    y: b:number = 0,
} = p
val q = {
    x,
    y: b,
}
```


Objects can also have methods. Methods can be marked as `async` to indicate they return a promise and can use `await` inside their body. The first param of all methods is `self: Self`.
This can be shortened to just the identifier `self`.  `mut self` indicates that
properties on `self` can be modified.  The `self` parameter can also be 
destructured.  It does not make sense to destructure `mut self`.  It's also worth
noting that object literals with mutable methods are by definition mutable.


```ts
val p = {
    x: 5:number,
    y: 10:number,
    length1(self) -> number {
        return Math.sqrt(self.x * self.x + self.y * self.y)
    },
    length2({x, y}) -> number {
        return Math.sqrt(x*x + y*y)
    },
    reset(mut self) {
        self.x = 0
        self.y = 0
    },
    async fetchData(self, url: string) -> Promise<string> {
        const response = await fetch(url)
        return await response.text()
    },
}
```

You can use the `async` keyword before a method definition to make it asynchronous. Async methods always return a `Promise`.

NOTE: Return types on methods on not required since we can always infer them.
You still may want to include them for documentation purposes.


Compiling `p` as described above will produce the following JavaScript code (including an async method):

```js
const p = {
    x: 5,
    y: 10,
    length1() {
        return Math.sqrt(this.x * this.x + this.y * this.y);
    },
    length2() {
        const {x, y} = this;
        return Math.sqrt(x*x + y*y);
    },
    reset() {
        this.x = 0
        this.y = 0
    },
    async fetchData(url) {
        const response = await fetch(url);
        return await response.text();
    },
};
```

References to `self` inside method bodies are converted to `this`.  Destructuring
the `self` param becomes a statement inside th method body destructuring `this`.

Object literals can have getters and setters.

```ts
val obj = {
    value: 0:number,
    get foo(self) -> number {
        return self.vlaue
    },
    set bar(mut self, value: number) {
        self.value = value
    },
}
```

Object literals can have computed method and field names.

```ts
val key = "value";

val obj = {
    [key]: 0:number,
    [`get${key}`](self) -> number {
        return self[key]
    },
    [`set${key}`](self, value: number) {
        self[key] = value
    }
}
```

## Classes

Class declarations are very similar variable declarations where the initializer
is an object.  There are a couple of key differences:

Methods in classes can also be marked as `async` to indicate they are asynchronous and return a `Promise`. Both instance and static methods can be async.
- Classes have a primary constructor, e.g. `Point(x: number, y: number) { ... }`
  which is used to pass data to an instance when constructing it.
- If you need additional logic beyond initializing fields in the instance, add
  a static method which calls the primary constructor and then runs the additional
  initialization logic
- `Self` is a type alias that resolves to the class being defined, in this case
  that would be `Point` (this can make renaming classes and other kinds of
  refactoring easier)
- Instance methods can access parameters passed to the primary constructor;
  `static` methods cannot
- Instance methods can access the current instance via the `self` method param;
  `static` methods don't have access to `self`
- Static properties and methods are accessed as fields on the `Point` class
- Variables of type `Point` don't have access to methods that use `mut self` while
  variables of type `mut Point` will have access to all mehtods


```ts
class Point(x: number, y: number) {
    x: x
    y, // same as `y: y,`
    color: Color = [255, 0, 0],
    add({x, y}, other: Self) -> Self {
        return Self(x + other.x, y + other.y)
    },
    sub(self, other: Point) -> Point {
        return Self(self.x - other.x, self.y - other.y)
    },
    toString({x, y}) {
        return `(${x}, ${y})`
    },
    get length({x, y}) -> number {
        return Math.sqrt(x*x + y*y)
    },
    set color(mut self, color: Color) {
        self.color = color
    },
    async fetchData(self, url: string) -> Promise<string> {
        const response = await fetch(url)
        return await response.text()
    },
    static origin = Point(0, 0),
    static async randomAsync() -> Promise<Point> {
        const x = await getRandomNumber()
        const y = await getRandomNumber()
        return Point(x, y)
    },
    static random() {
        val p = Point(Math.random(), Math.random())
        console.log(`random p = ${p}`)
        return p
    },
}

val p = Point.random()
```


The previous example will be compiled to the following JS code (including async methods):

```js
class Point {
    constructor(x, y) {
        this.x = x;
        this.y = y;
        this.color = [255, 0, 0];
    }

    add(other) {
        const {x, y} = this;
        return new Point(x + other.x, y + other.y);
    }

    sub(other) {
        return new Point(this.x - other.x, this.y - other.y);
    }

    toString() {
        return `(${this.x}, ${this.y})`;
    }

    get length()  {
        const {x, y} = this;
        return Math.sqrt(x*x + y*y);
    }

    set color(color) {
        this.color = color;
    }

    async fetchData(url) {
        const response = await fetch(url);
        return await response.text();
    }
    
    static origin = new Point(0, 0)

    static async randomAsync() {
        const x = await getRandomNumber();
        const y = await getRandomNumber();
        return new Point(x, y);
    }

    static random() {
        const p = new Point(Math.random(), Math.random())
        console.log(`random p = ${p}`)
        return p;
    }
}
```

Parameters passed to the primary constructor as accessible from instance methods
and in expressions used to initialize instance properties, they are not accesible
from static properties or static methods.

```ts
class User(name: string, age: number) {
    // Fields initialized from constructor parameters
    name,
    age,

    // Fields initialized with default values
    isActive: true,
    createdAt: Date(),
    role: "user",
    
    // ... method definitions
}
```


## Generic Classes

Classes in Escalier can be generic, allowing you to parameterize the class over one or more types. Type parameters are declared after the class name, before the primary constructor parameters. Both the primary constructor and methods on the class can be generic.

### Generic Class Example

```ts
class Box<T>(value: T) {
    value: value,
    get(self) -> T {
        return self.value
    },
    set(mut self, value: T) {
        self.value = value
    },
}

val intBox = Box(5)
val strBox = Box("hello")
```

### Generic Methods

Methods on a class can also be generic, even if the class itself is not. Type parameters for methods are declared after the method name and before the parameter list.

```ts
class Mapper<T>(value: T) {
    value: value,

    map<U>(self, fn: (T) -> U) -> Mapper<U> {
        return Mapper(fn(self.value))
    },
}

val numMapper = Mapper(42)
val strMapper = numMapper.map(x => x.toString())
```

### Generic Static Methods

Static methods can also be generic:

```ts
class Util {
    static identity<T>(x: T) -> T {
        return x
    }
}

val a = Util.identity(123) // a: number
val b = Util.identity("hi") // b: string
```

Type parameters can be constrained using the same syntax as elsewhere in Escalier:

```ts
class Pair<T: number, U: string>(first: T, second: U) {
    first: first,
    second: second,
}
```

You can also provide default type arguments:

```ts
class Response<T: any = string>(data: T) {
    data: data,
}

val r1 = Response<string>("ok")  // T = string
val r2 = Response<number>(42)    // T = number
```

Generic classes and methods enable powerful abstractions and type-safe code reuse in Escalier.

## Access Controls

Classes can use the `private` modifier to control visibility of members outside
of the class declaration.  Public methods can access `private` members.

```ts
class MyClass {
    private foo: "":string,
    bar: 0:number,
    private baz(self) {
        // ...
    },
    qux(self) {
        self.baz()
        console.log(self.foo)
    },
}
```

Parameters passed to the primary constructor are morally equivalent to private
members.  The main difference is that they're caught in the closure of all instance
methods and thus are accessed directly without having to go through `self`.

```ts
class MyClass(foo: string, bar: number) {
    private foo,
    bar,
    qux(self) {
        console.log(foo)
        console.log(bar)
        console.log(self.foo) // private member
        console.log(self.bar) // public member
    },
}

const myInstance = MyClass("hello", 5)
myInstance.foo // ERROR, foo is private
myInstance.bar // OKAY
```

This will be compiled to JavaScript in the following way:

```js
class MyClass {
    constructor(foo, bar) {
        this.#foo = foo
        this.bar = bar

        this.#__param_foo__
        this.#__param_bar__
    }
    
    qux(self) {
        console.log(this.#__param_foo__)
        console.log(this.#__param_bar__)
        console.log(this.#foo) // private member
        console.log(this.bar) // public member
    }
}

const myInstance = new MyClass("hello", 5)
myInstance.foo // ERROR, foo is private
myInstance.bar // OKAY
```

In certain situations we may want the primary constructor to be private.  As an
example we may want to require consumers of the class to use static factory 
methods on the class to construct instances.  This is useful if there's additional
logic that must be run as part of instance creation.

The example below shows a wrapper around 

```ts
import mysql from "mysql.promise"

class DBConnection private(conn: SQLConnection) {
    [Symbol.asyncDispose](self) -> Promise<void, Error> {
        return conn.end()
    },
    static create(host: string) -> Promise<DBConnection, Error> {
        val conn = await mysql.createConnection({host})
        return DBConnectino(conn)
    }
}

fn main() {
    use conn = DBConnection.create("example.com")

    // do stuff with `conn`

    // `conn[Symbol.asyncDispose]()` will automatically get called
}
```
