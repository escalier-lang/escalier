export class Box {
  constructor(temp1, temp2) {
    const x = temp1;
    const y = temp2;
    this.x = x;
    this.y = y;
  }
  grow(param0, param1) {
    if (arguments.length === 2 && typeof param0 === "number" && typeof param1 === "number") {
      const d = param0;
      const e = param1;
      return d + e;
    } else if (arguments.length === 1 && typeof param0 === "number") {
      const d = param0;
      return d;
    } else throw new TypeError("No overload matches the provided arguments for method 'grow'");
  }
  area() {
    return this.x * this.y;
  }
  static of(param0, param1) {
    if (arguments.length === 2 && typeof param0 === "number" && typeof param1 === "number") {
      const x = param0;
      const y = param1;
      return x + y;
    } else if (arguments.length === 1 && typeof param0 === "number") {
      const x = param0;
      return x;
    } else throw new TypeError("No overload matches the provided arguments for method 'of'");
  }
}
export function area(param0, param1) {
  if (arguments.length === 2 && typeof param0 === "number" && typeof param1 === "number") {
    const w = param0;
    const h = param1;
    return w * h;
  } else if (arguments.length === 1 && typeof param0 === "number") {
    const w = param0;
    return w * w;
  } else throw new TypeError("No overload matches the provided arguments for function 'area'");
}
export function total(param0, param1) {
  if (arguments.length === 2 && typeof param0 === "number" && typeof param1 === "number") {
    const a = param0;
    const b = param1;
    return a + b;
  } else if (Array.prototype.every.call(arguments, function (elem) {
    return typeof elem === "number";
  })) {
    const ns = Array.prototype.slice.call(arguments);
    return ns.length;
  } else throw new TypeError("No overload matches the provided arguments for function 'total'");
}
//# sourceMappingURL=./index.js.map
