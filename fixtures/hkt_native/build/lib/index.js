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
export const box = new Box(5);
export const boxFunctor = {map: function (temp8, temp9) {
  const fa = temp8;
  const f = temp9;
  return new Box(f(fa.value));
}};
export function double(temp10, temp11) {
  const functor = temp10;
  const fa = temp11;
  return functor.map(fa, function (temp12) {
    const n = temp12;
    return n * 2;
  });
}
export const xs = [1, 2, 3];
export const doubledArray = double(arrayFunctor, xs);
export const doubledBox = double(boxFunctor, box);
export const opt = new MyOption.Some(5);
export const optionFunctor = {map: function (temp13, temp14) {
  const fa = temp13;
  const f = temp14;
  let temp15;
  let temp16;
  temp16 = fa;
  if (temp16 instanceof MyOption.Some) {
    const [temp17] = InvokeCustomMatcherOrThrow(MyOption.Some, temp16, undefined);
    const value = temp17;
    temp15 = new MyOption.Some(f(value));
  } else if (temp16 instanceof MyOption.None) {
    const [] = InvokeCustomMatcherOrThrow(MyOption.None, temp16, undefined);
    temp15 = new MyOption.None();
  }
  return temp15;
}};
export const doubledOpt = double(optionFunctor, opt);
export const tagged = new Tagged("n", 5);
export const taggedFunctor = {map: function (temp18, temp19) {
  const fa = temp18;
  const f = temp19;
  return new Tagged(fa.tag, f(fa.value));
}};
export const doubledTagged = double(taggedFunctor, tagged);
export const five = new Box(5);
export function widen(temp20) {
  const fa = temp20;
  return fa;
}
export const widened = widen(five);
//# sourceMappingURL=./index.js.map
