import { InvokeCustomMatcherOrThrow } from "@escalier/runtime";
export const MyOption = {};
class MyOption__Some {
  constructor(temp1) {
    const value = temp1;
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
export const arrayFunctor = {URI: "Array", map: function (temp2, temp3) {
  const fa = temp2;
  const f = temp3;
  return fa.map(function (temp4, temp5, temp6) {
    const a = temp4;
    const i = temp5;
    const arr = temp6;
    return f(a);
  });
}};
export function double(temp7, temp8) {
  const functor = temp7;
  const fa = temp8;
  return functor.map(fa, function (temp9) {
    const n = temp9;
    return n * 2;
  });
}
export const optionFunctor = {URI: "MyOption", map: function (temp10, temp11) {
  const fa = temp10;
  const f = temp11;
  let temp12;
  let temp13;
  temp13 = fa;
  if (temp13 instanceof MyOption.Some) {
    const [temp14] = InvokeCustomMatcherOrThrow(MyOption.Some, temp13, undefined);
    const value = temp14;
    temp12 = new MyOption.Some(f(value));
  } else if (temp13 instanceof MyOption.None) {
    const [] = InvokeCustomMatcherOrThrow(MyOption.None, temp13, undefined);
    temp12 = new MyOption.None();
  }
  return temp12;
}};
export const xs = [1, 2, 3];
export const viaInstance = arrayFunctor.map(xs, function (temp15) {
  const n = temp15;
  return n * 2;
});
export function widen(temp16) {
  const fa = temp16;
  return fa;
}
export const ys = double(arrayFunctor, xs);
export const zs = double(optionFunctor, new MyOption.Some(5));
//# sourceMappingURL=./index.js.map
