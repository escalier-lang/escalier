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
//# sourceMappingURL=./index.js.map
