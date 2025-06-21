# 03 Classes

## Syntax

```ts
class Vector implements Arithmetic {
    x: number
    y: number
    color: Color

    constructor(self, id: string, x: number, y: number) {
        super(id)
        // can call methods from the superclass on `self` now
        self.x = x
        self.y = y
        self.color = {r: 255, b: 0, g: 0}
        // can call methods from `Vector` on `self` now
    }

    constructor(self, id: string) {
        self(0, 0) // calls another constructor
        // can call methods from `Vector` on `self` now
    }

    get length(self) {
        return Math.sqrt(self.x * self.x + self.y * self.y)
    }

    set color(self, c: Color) {
        self.color = c
    }

    // inferred as `fn(other: Vector) -> Vector`
    add(self, other: Vector) {
        return Vector(self.x + other.x, self.y + other.y)
    }

    static origin() {
        return Vector(0, 0)
    }
}
```

All of the same modifiers that can be used with functions can also be used with
methods.

Classes can be generic as can individual methods.  

How are constructors compiled to JS.
```ts
// input.esc
type Color = {r: number, g: number, b: number}

class Vector implements Arithmetic {
    x: number
    y: number
    color: Color

    constructor(self, id: string, x: number, y: number) {
        super(id)
        // can call methods from the superclass on `self` now
        self.x = x
        self.y = y
        self.color = {r: 255, b: 0, g: 0}
        // can call methods from `Vector` on `self` now
    }

    constructor(self, id: string) {
        self(id, 0, 0) // calls another constructor
        // can call methods from `Vector` on `self` now
    }
}

// output.js
class Vector {
    constructor(...args) {
        if (args.length >= 3) {
            super(args[0]);
            this.x = args[1];
            this.y = args[2];
            this.color = {r: 255, b: 0, g: 0};
        } else if (args.length >= 1) {
            super(args[0]);
            this.x = 0;
            this.y = 0;
            this.color = {r: 255, b: 0, g: 0};
        }
    }
}
```

Overloaded constructors is totally a post-MVP feature.

QUESTIONS:
- are classes nominal or structural?
