export const geo = {};
export const Color = {};
class Color__RGB {
  constructor(temp1, temp2, temp3) {
    const r = temp1;
    const g = temp2;
    const b = temp3;
    this.r = r;
    this.g = g;
    this.b = b;
  }
  static [Symbol.customMatcher](subject) {
    return [subject.r, subject.g, subject.b];
  }
}
Color.RGB = Color__RGB;
class Color__Hex {
  constructor(temp4) {
    const code = temp4;
    this.code = code;
  }
  static [Symbol.customMatcher](subject) {
    return [subject.code];
  }
}
Color.Hex = Color__Hex;
export class Holder {
  constructor(temp5) {
    const value = temp5;
    this.value = value;
  }
}
export class Other {
  constructor(temp6) {
    const tag = temp6;
    this.tag = tag;
  }
}
export class Shape {
  constructor(temp7) {
    const area = temp7;
    this.area = area;
  }
}
export function aliasOfAlias(param0) {
  if (arguments.length === 1 && param0 instanceof Shape) {
    const v = param0;
    return v.area;
  } else if (arguments.length === 1 && param0 instanceof Other) {
    const v = param0;
    return 2;
  } else throw new TypeError("No overload matches the provided arguments for function 'aliasOfAlias'");
}
export function aliasWithTypeArgs(param0) {
  if (arguments.length === 1 && param0 instanceof Holder) {
    const v = param0;
    return v.value;
  } else if (arguments.length === 1 && param0 instanceof Other) {
    const v = param0;
    return 4;
  } else throw new TypeError("No overload matches the provided arguments for function 'aliasWithTypeArgs'");
}
export function aliased(param0) {
  if (arguments.length === 1 && param0 instanceof Shape) {
    const v = param0;
    return v.area;
  } else if (arguments.length === 1 && param0 instanceof Other) {
    const v = param0;
    return 1;
  } else throw new TypeError("No overload matches the provided arguments for function 'aliased'");
}
export function direct(param0) {
  if (arguments.length === 1 && param0 instanceof Shape) {
    const v = param0;
    return v.area;
  } else if (arguments.length === 1 && param0 instanceof Other) {
    const v = param0;
    return 0;
  } else throw new TypeError("No overload matches the provided arguments for function 'direct'");
}
export function directNs(param0) {
  if (arguments.length === 1 && param0 instanceof geo__Circle) {
    const v = param0;
    return v.radius;
  } else if (arguments.length === 1 && param0 instanceof Other) {
    const v = param0;
    return 8;
  } else throw new TypeError("No overload matches the provided arguments for function 'directNs'");
}
export function genericAlias(param0) {
  if (arguments.length === 1 && param0 instanceof Holder) {
    const v = param0;
    return v.value;
  } else if (arguments.length === 1 && param0 instanceof Other) {
    const v = param0;
    return 3;
  } else throw new TypeError("No overload matches the provided arguments for function 'genericAlias'");
}
class geo__Circle {
  constructor(temp8) {
    const radius = temp8;
    this.radius = radius;
  }
}
function geo__describe(param0) {
  if (arguments.length === 1 && param0 instanceof geo__Circle) {
    const v = param0;
    return "circle";
  } else if (arguments.length === 1 && typeof param0 === "string") {
    const v = param0;
    return v;
  } else throw new TypeError("No overload matches the provided arguments for function 'describe'");
}
function geo__describeDirect(param0) {
  if (arguments.length === 1 && param0 instanceof geo__Circle) {
    const v = param0;
    return "circle";
  } else if (arguments.length === 1 && typeof param0 === "string") {
    const v = param0;
    return v;
  } else throw new TypeError("No overload matches the provided arguments for function 'describeDirect'");
}
export function namespacedAlias(param0) {
  if (arguments.length === 1 && param0 instanceof geo__Circle) {
    const v = param0;
    return v.radius;
  } else if (arguments.length === 1 && param0 instanceof Other) {
    const v = param0;
    return 5;
  } else throw new TypeError("No overload matches the provided arguments for function 'namespacedAlias'");
}
export function unionAlias(param0) {
  if (arguments.length === 1 && (param0 instanceof Shape || param0 instanceof Other)) {
    const v = param0;
    return 6;
  } else if (arguments.length === 1 && typeof param0 === "string") {
    const v = param0;
    return 7;
  } else throw new TypeError("No overload matches the provided arguments for function 'unionAlias'");
}
export function variantAlias(param0) {
  if (arguments.length === 1 && param0 instanceof Color__Hex) {
    const v = param0;
    return 9;
  } else if (arguments.length === 1 && param0 instanceof Other) {
    const v = param0;
    return 10;
  } else throw new TypeError("No overload matches the provided arguments for function 'variantAlias'");
}
export function variantDirect(param0) {
  if (arguments.length === 1 && param0 instanceof Color__Hex) {
    const v = param0;
    return 11;
  } else if (arguments.length === 1 && param0 instanceof Other) {
    const v = param0;
    return 12;
  } else throw new TypeError("No overload matches the provided arguments for function 'variantDirect'");
}
//# sourceMappingURL=./index.js.map
