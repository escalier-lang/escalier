export const basics = `
let add = fn (a, b) => a + b
let sub = fn (a, b) => a - b
let foo = fn (f, x) => f(x) + x

// if-else are expressions and the last statement
// in a block is its value
let baz = if (true) {
  let x = 5
  let y = 10
  x + y
} else {
  10
}

type Point = {x: number, y: number}
let point: Point = {x: 5, y: 10}
`;

export const asyncAwait = `
let add = async fn (a, b) => await a() + await b()
`;

// TODO: infer JSX
export const jsxReact = `
// This is a placeholder until we can infer types from react.d.ts
type JSXElement = {}

type Props = {
    count: number,
}

// Adapted from https://rescript-lang.org/try
let Button = fn (props: Props) {
    let {count} = props
    let times = match (count) {
        1 => "once",
        2 => "twice",
        n => \`\${n} times\`
    }
    let msg = \`Click me \${times}\`

    return <button>{msg}</button>
}

// Props are type checked with extra props being allowed for now.
// In the future they won't be.
let button = <Button count={5} foo="bar" />
`;

export const fibonacci = `
// only self-recursive functions are supported, but support for
// mutual recursion will be added in the future
let fib = fn (n) => if (n == 0) {
  0
} else if (n == 1) {
  1
} else {
  fib(n - 1) + fib(n - 2)
}
`;

export const functionOverloading = `
declare let add: (fn (a: number, b: number) -> number) & (fn (a: string, b: string) -> string)

let num = add(5, 10)
let str = add("hello, ", "world")
`;

// export const ifLetElse = `
// declare let a: string | number

// // if-let is similar to TypeScript's type narrowing, but it
// // introduces a new binding for the narrowed type.
// let result = if (let x is number = a) {
//     x + 5
// } else if (let y is string = a) {
//     y
// } else {
//     true
// }
// `;

export const basicPatternMatching = `
declare let count: number

let result = match (count) {
    0 => "none",
    1 => "one",
    2 => "a couple",
    n if (n < 5) => "a few",
    _ => "many"
}
`;

export const disjointUnionPatternMatching = `
type Event = 
    {type: "mousedown", x: number, y: number}
  | {type: "keydown", key: string}

declare let event: Event

let result = match (event) {
    {type: "mousedown", x, y} => \`mousedown: (\${x}, \${y})\`,
    {type: "keydown", key} => \`keydown: \${key}\`
}
`;

export const standardLibrary = `
let message = "Hello, world!"
let length = message.length

let tuple = [1, 2, 3]
let squares = tuple.map(fn (x) => x * x)

let mut fruit: string[] = ["banana", "grapes", "apple", "pear"]
fruit.sort()
`;

export const utilityTypes = `
type Point3D = {x: number, y: number, z: number}
type Point2D = Pick<Point3D, "x" | "y">
let p: Point2D = {x: 5, y: 10}

type Obj = {a: number, b?: string, c: boolean, d?: string}
let mut obj: Obj = {a: 5, c: true}
obj.c = false

type PartialObj = Partial<Obj>
let p_obj: PartialObj = {b: "hello"}
let make_p_obj = fn (a, b, c, d) -> Partial<Obj> {
  return {a, b, c, d}
}

// type ReadonlyObj = Readonly<Obj>
// let ro_obj: ReadonlyObj = {a: 5, c: true}
// uncommenting the following line will cause an error
// ro_obj.c = false
`;

export const regexes = `
let regex_g = /(?<foo>foo)|(?<bar>bar)/g
let result_g = "foobarbaz".match(regex_g)

let regex = /(?<foo>foo)|(?<bar>bar)/
let result = "foobarbaz".match(regex)
if (regex.test("foo")) {
    // do something
}
`;
