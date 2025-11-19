import { InvokeCustomMatcherOrThrow } from "@escalier/runtime";
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
let temp5;
let temp6;
temp6 = color;
if (temp6 instanceof Color.RGB) {
  const [temp8, temp9, temp10] = InvokeCustomMatcherOrThrow(Color.RGB, temp6, undefined);
  const r = temp8;
  const g = temp9;
  const b = temp10;
  temp5 = r + g + b;
} else if (temp6 instanceof Color.Hex) {
  const [temp7] = InvokeCustomMatcherOrThrow(Color.Hex, temp6, undefined);
  const code = temp7;
  temp5 = code;
}
export const result = temp5;
//# sourceMappingURL=./index.js.map
