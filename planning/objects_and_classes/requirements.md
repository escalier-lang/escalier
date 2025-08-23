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
val = {
    x::number,
    y: b:number = 0,
} = p
val q = {
    x,
    y: b,
}
```

Objects can also have methods.  The first param of all methods is `self: Self`.
This can be shortened to just the identifier `self`.  `mut self` indicates that
properties on `self` can be modified.  The `self` parameter can also be 
destructured.  It does not make sense to destructure `mut self`.

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
}
```

NOTE: Return types on methods on not required since we can always infer them.
You still may want to include them for documentation purposes.

Compiling `p` as described above will produce the following JavaScript code:

```js
const p = {
    x,
    y,
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
};
```

References to `self` inside method bodies are converted to `this`.  Destructuring
the `self` param becomes a statement inside th method body destructuring `this`.

NOTE: Objects literals can have getters and setters.

## Classes

Class declarations are very similar variable declarations where the initializer
is an object.  There are a couple of key differences:
- Classes have a primary constructor, e.g. `Point(x: number, y: number) { ... }`
  which is used to pass data to an instance when constructing it.
- `Self` is a type alias that resolves to the class being defined, in this case
  that would be `Point` (this can make renaming classes and other kinds of
  refactoring easier)
- Instance methods can access parameters passed to the primary constructor;
  `static` methods cannot
- Instance methods can access the current instance via the `self` method param;
  `static` methods don't have access to `self`
- Static properties and methods are accessed as fields on the `Point` class

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
    set color(mut self, c: Color) {
        self.color = c
    },
    static origin = Point(0, 0),
    static random() {
        val p = Point(Math.random(), Math.random())
        console.log(`random p = ${p}`)
        return p
    },
}

val p = Point.random()
```

The previous example will be compiled to the following JS code:

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
        return Self(this.x - other.x, this.y - other.y);
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
    
    static origin = new Point(0, 0)
    
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
