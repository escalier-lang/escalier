import { InvokeCustomMatcherOrThrow } from "@escalier/runtime";
const MyEnum = {};
export class MyEnum__Color {
  constructor(temp2, temp3, temp4) {
    const r = temp2;
    const g = temp3;
    const b = temp4;
    this.r = r;
    this.g = g;
    this.b = b;
  }
  static [Symbol.customMatcher](temp1) {
    const subject = temp1;
    return [subject.r, subject.g, subject.b];
  }
}
MyEnum.Color = MyEnum__Color;
export class MyEnum__MyEvent {
  constructor(temp6) {
    const kind = temp6;
    this.kind = kind;
  }
  static [Symbol.customMatcher](temp5) {
    const subject = temp5;
    return [subject.kind];
  }
}
MyEnum.MyEvent = MyEnum__MyEvent;
let temp7;
let temp8;
temp8 = obj;
if (temp8 instanceof MyEnum.Color && temp8 != null && "r" in temp8 && "g" in temp8 && "b" in temp8) {
  const {r, g, b: blue = 0} = temp8;
  temp7 = r + g + blue;
} else if (temp8 instanceof MyEnum.MyEvent && temp8 != null && "kind" in temp8) {
  const {kind = "default"} = temp8;
  temp7 = kind;
}
export const result1 = temp7;
let temp9;
let temp10;
temp10 = obj;
if (temp10 instanceof MyEnum.Color) {
  const [temp12, temp13, temp14 = 0] = InvokeCustomMatcherOrThrow(MyEnum.Color, temp10, undefined);
  const r = temp12;
  const g = temp13;
  const blue = temp14;
  temp9 = r + g + blue;
} else if (temp10 instanceof MyEnum.MyEvent) {
  const [temp11 = "default"] = InvokeCustomMatcherOrThrow(MyEnum.MyEvent, temp10, undefined);
  const kind = temp11;
  temp9 = kind;
}
export const result2 = temp9;
//# sourceMappingURL=./index.js.map
