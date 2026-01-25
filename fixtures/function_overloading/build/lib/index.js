export class Circle {
  constructor(temp1) {
    const radius = temp1;
    this.radius = radius;
  }
}
export class Point {
  constructor(temp2, temp3) {
    const x = temp2;
    const y = temp3;
    this.x = x;
    this.y = y;
  }
}
export function combine(param0, param1) {
  if (typeof param0 === "number" && typeof param1 === "number") {
    const a = param0;
    const b = param1;
    return "Numbers: " + a + b.toString();
  } else if (typeof param0 === "string" && typeof param1 === "string") {
    const a = param0;
    const b = param1;
    return "Strings: " + a + b;
  } else if (typeof param0 === "number" && typeof param1 === "string") {
    const a = param0;
    const b = param1;
    return "Mixed: " + a.toString() + b;
  } else throw new TypeError("No overload matches the provided arguments for function 'combine'");
}
export const c1 = combine(1, 2);
export const c2 = combine("hello", "world");
export const c3 = combine(42, "test");
export const circle = new Circle(5);
export function describe(param0) {
  if (param0 instanceof Point) {
    const shape = param0;
    return "Point at (" + shape.x.toString() + ", " + shape.y.toString() + ")";
  } else if (param0 instanceof Circle) {
    const shape = param0;
    return "Circle with radius " + shape.radius.toString();
  } else throw new TypeError("No overload matches the provided arguments for function 'describe'");
}
export const point = new Point(10, 20);
export const d1 = describe(point);
export const d2 = describe(circle);
export function dup(param0) {
  if (typeof param0 === "number") {
    const value = param0;
    return 2 * value;
  } else if (typeof param0 === "string") {
    const value = param0;
    return value + value;
  } else throw new TypeError("No overload matches the provided arguments for function 'dup'");
}
export function format(param0) {
  if (typeof param0 === "number") {
    const value = param0;
    return "Number: " + value.toString();
  } else if (typeof param0 === "string") {
    const value = param0;
    return "String: " + value;
  } else if (typeof param0 === "boolean") {
    const value = param0;
    if (value) {
      return "Boolean: true";
    } else {
      return "Boolean: false";
    }
  } else throw new TypeError("No overload matches the provided arguments for function 'format'");
}
export const f1 = format(42);
export const f2 = format("test");
export const f3 = format(true);
export async function fetchData(param0) {
  if (typeof param0 === "number") {
    const id = param0;
    return "Data for ID: " + id.toString();
  } else if (typeof param0 === "string") {
    const id = param0;
    return "Data for key: " + id;
  } else throw new TypeError("No overload matches the provided arguments for function 'fetchData'");
}
export function greet(param0, param1, param2) {
  if (typeof param0 === "string" && typeof param1 === "string" && typeof param2 === "string") {
    const title = param0;
    const firstName = param1;
    const lastName = param2;
    return "Hello, " + title + " " + firstName + " " + lastName + "!";
  } else if (typeof param0 === "string" && typeof param1 === "string") {
    const title = param0;
    const name = param1;
    return "Hello, " + title + " " + name + "!";
  } else if (typeof param0 === "string") {
    const name = param0;
    return "Hello, " + name + "!";
  } else throw new TypeError("No overload matches the provided arguments for function 'greet'");
}
export const g1 = greet("Alice");
export const g2 = greet("Dr.", "Bob");
export const g3 = greet("Prof.", "Carol", "Smith");
export const num = dup(5);
export function processPoint(param0) {
  if (param0 !== null && typeof param0 === "object" && "x" in param0 && typeof param0.x === "number" && "y" in param0 && typeof param0.y === "number" && "z" in param0 && typeof param0.z === "number") {
    const {x, y, z} = param0;
    return "3D Point: " + x.toString() + ", " + y.toString() + ", " + z.toString();
  } else if (param0 !== null && typeof param0 === "object" && "x" in param0 && typeof param0.x === "number" && "y" in param0 && typeof param0.y === "number") {
    const {x, y} = param0;
    return "2D Point: " + x.toString() + ", " + y.toString();
  } else throw new TypeError("No overload matches the provided arguments for function 'processPoint'");
}
export const p1 = processPoint({x: 1, y: 2});
export const p2 = processPoint({x: 1, y: 2, z: 3});
export const str = dup("hello");
export async function test() {
  const data1 = await fetchData(42);
  const data2 = await fetchData("user123");
}
//# sourceMappingURL=./index.js.map
