import { InvokeCustomMatcherOrThrow } from "@escalier/runtime";
export class Bag {
  constructor(temp1, temp2) {
    const item = temp1;
    const size = temp2;
    this.item = item;
    this.size = size;
  }
}
export class Box {
  constructor(temp3) {
    const value = temp3;
    this.value = value;
  }
}
export const MyOption = {};
class MyOption__Some {
  constructor(temp4) {
    const value = temp4;
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
  constructor(temp5, temp6) {
    const tag = temp5;
    const value = temp6;
    this.tag = tag;
    this.value = value;
  }
}
export const arrayFunctor = {map: function (temp7, temp8) {
  const fa = temp7;
  const f = temp8;
  return fa.map(function (temp9) {
    const a = temp9;
    return f(a);
  });
}};
export const bag = new Bag(1, 3);
export function sizeOf(temp10) {
  const fa = temp10;
  return fa.size;
}
export const bagSize = sizeOf(bag);
export function firstSlot(temp11) {
  const fa = temp11;
  return fa;
}
export const slots = [1, 2];
export const borrowedSlots = firstSlot(slots);
export const box = new Box(5);
export const boxFunctor = {map: function (temp12, temp13) {
  const fa = temp12;
  const f = temp13;
  return new Box(f(fa.value));
}};
export function double(temp14, temp15) {
  const functor = temp14;
  const fa = temp15;
  return functor.map(fa, function (temp16) {
    const n = temp16;
    return n * 2;
  });
}
export const xs = [1, 2, 3];
export const doubledArray = double(arrayFunctor, xs);
export const doubledBox = double(boxFunctor, box);
export const opt = new MyOption.Some(5);
export const optionFunctor = {map: function (temp17, temp18) {
  const fa = temp17;
  const f = temp18;
  let temp19;
  let temp20;
  temp20 = fa;
  if (temp20 instanceof MyOption.Some) {
    const [temp21] = InvokeCustomMatcherOrThrow(MyOption.Some, temp20, undefined);
    const value = temp21;
    temp19 = new MyOption.Some(f(value));
  } else if (temp20 instanceof MyOption.None) {
    const [] = InvokeCustomMatcherOrThrow(MyOption.None, temp20, undefined);
    temp19 = new MyOption.None();
  }
  return temp19;
}};
export const doubledOpt = double(optionFunctor, opt);
export const tagged = new Tagged("n", 5);
export const taggedFunctor = {map: function (temp22, temp23) {
  const fa = temp22;
  const f = temp23;
  return new Tagged(fa.tag, f(fa.value));
}};
export const doubledTagged = double(taggedFunctor, tagged);
export const five = new Box(5);
export function widen(temp24) {
  const fa = temp24;
  return fa;
}
export const widened = widen(five);
//# sourceMappingURL=./index.js.map
