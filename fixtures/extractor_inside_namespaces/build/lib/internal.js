import { InvokeCustomMatcherOrThrow } from "@escalier/runtime";
export const MyEnum = {};
class MyEnum__Color {
  constructor(temp1, temp2, temp3) {
    const r = temp1;
    const g = temp2;
    const b = temp3;
    this.r = r;
    this.g = g;
    this.b = b;
  }
  static [Symbol.customMatcher](temp4) {
    const subject = temp4;
    return [subject.r, subject.g, subject.b];
  }
}
MyEnum.Color = MyEnum__Color;
class MyEnum__MyEvent {
  constructor(temp5) {
    const kind = temp5;
    this.kind = kind;
  }
  static [Symbol.customMatcher](temp6) {
    const subject = temp6;
    return [subject.kind];
  }
}
MyEnum.MyEvent = MyEnum__MyEvent;
let temp7;
let temp8;
temp8 = obj;
if (temp8 instanceof MyEnum__Color && temp8 != null && "r" in temp8 && "g" in temp8 && "b" in temp8) {
  const {r, g, b: blue = 0} = temp8;
  temp7 = r + g + blue;
} else if (temp8 instanceof MyEnum__MyEvent && temp8 != null && "kind" in temp8) {
  const {kind = "default"} = temp8;
  temp7 = kind;
}
export const result1 = temp7;
let temp9;
let temp10;
temp10 = obj;
if (temp10 instanceof MyEnum__Color) {
  const [temp12, temp13, temp14 = 0] = InvokeCustomMatcherOrThrow(MyEnum__Color, temp10, undefined);
  const r = temp12;
  const g = temp13;
  const blue = temp14;
  temp9 = r + g + blue;
} else if (temp10 instanceof MyEnum__MyEvent) {
  const [temp11 = "default"] = InvokeCustomMatcherOrThrow(MyEnum__MyEvent, temp10, undefined);
  const kind = temp11;
  temp9 = kind;
}
export const result2 = temp9;
//# sourceMappingURL=./internal.js.map
