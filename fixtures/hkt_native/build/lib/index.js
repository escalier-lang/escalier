import { InvokeCustomMatcherOrThrow } from "@escalier/runtime";
export class Box {
  constructor(temp1) {
    const value = temp1;
    this.value = value;
  }
}
export const MyOption = {};
class MyOption__Some {
  constructor(temp2) {
    const value = temp2;
    this.value = value;
  }
  static [Symbol.customMatcher](subject) {
    return [subject.value];
  }
}
MyOption.Some = MyOption__Some;
class MyOption__None {
  constructor() {
  }
  static [Symbol.customMatcher](subject) {
    return [];
  }
}
MyOption.None = MyOption__None;
export class Tagged {
  constructor(temp3, temp4) {
    const tag = temp3;
    const value = temp4;
    this.tag = tag;
    this.value = value;
  }
}
export const arrayFunctor = {map: function (temp5, temp6) {
  const fa = temp5;
  const f = temp6;
  return fa.map(function (temp7) {
    const a = temp7;
    return f(a);
  });
}};
export function firstSlot(temp8) {
  const fa = temp8;
  return fa;
}
export const slots = [1, 2];
export const borrowedSlots = firstSlot(slots);
export const box = new Box(5);
export const boxFunctor = {map: function (temp9, temp10) {
  const fa = temp9;
  const f = temp10;
  return new Box(f(fa.value));
}};
export function double(temp11, temp12) {
  const functor = temp11;
  const fa = temp12;
  return functor.map(fa, function (temp13) {
    const n = temp13;
    return n * 2;
  });
}
export const xs = [1, 2, 3];
export const doubledArray = double(arrayFunctor, xs);
export const doubledBox = double(boxFunctor, box);
export const opt = new MyOption.Some(5);
export const optionFunctor = {map: function (temp14, temp15) {
  const fa = temp14;
  const f = temp15;
  let temp16;
  let temp17;
  temp17 = fa;
  if (temp17 instanceof MyOption.Some) {
    const [temp18] = InvokeCustomMatcherOrThrow(MyOption.Some, temp17, undefined);
    const value = temp18;
    temp16 = new MyOption.Some(f(value));
  } else if (temp17 instanceof MyOption.None) {
    const [] = InvokeCustomMatcherOrThrow(MyOption.None, temp17, undefined);
    temp16 = new MyOption.None();
  }
  return temp16;
}};
export const doubledOpt = double(optionFunctor, opt);
export const tagged = new Tagged("n", 5);
export const taggedFunctor = {map: function (temp19, temp20) {
  const fa = temp19;
  const f = temp20;
  return new Tagged(fa.tag, f(fa.value));
}};
export const doubledTagged = double(taggedFunctor, tagged);
export const five = new Box(5);
export function widen(temp21) {
  const fa = temp21;
  return fa;
}
export const widened = widen(five);
//# sourceMappingURL=./index.js.map
