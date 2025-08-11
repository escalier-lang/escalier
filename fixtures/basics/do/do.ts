// Basic do-expression that returns a value
export const sum = (() => {
    const a = 5;
    const b = 10;
    return a + b;
})();

// Do-expression with multiple statements
export const message = (() => {
    const greeting = "Hello";
    const name = "World";
    return greeting + ", " + name + "!";
})();

// Do-expression that returns undefined (no final expression)
export const sideEffect = (() => {
    const x = 42;
    console.log("Side effect executed");
    return undefined;
})();

// Nested do-expressions
export const nested = (() => {
    const outer = (() => {
        const inner = 10;
        return inner * 2;
    })();
    return outer + 5;
})();

// Do-expression with conditional logic
export const conditional = (() => {
    const value = 15;
    return value > 10 ? (() => {
        return "large";
    })() : "small";
})();

// Empty do-expression (should return undefined)
export const empty = (() => {
    return undefined;
})();
