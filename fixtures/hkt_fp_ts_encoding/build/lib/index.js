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
  return fa.map(function (temp4) {
    const a = temp4;
    return f(a);
  });
}};
export function double(temp5, temp6) {
  const functor = temp5;
  const fa = temp6;
  return functor.map(fa, function (temp7) {
    const n = temp7;
    return n * 2;
  });
}
export const optionFunctor = {URI: "MyOption", map: function (temp8, temp9) {
  const fa = temp8;
  const f = temp9;
  let temp10;
  let temp11;
  temp11 = fa;
  if (temp11 instanceof MyOption.Some) {
    const [temp12] = InvokeCustomMatcherOrThrow(MyOption.Some, temp11, undefined);
    const value = temp12;
    temp10 = new MyOption.Some(f(value));
  } else if (temp11 instanceof MyOption.None) {
    const [] = InvokeCustomMatcherOrThrow(MyOption.None, temp11, undefined);
    temp10 = new MyOption.None();
  }
  return temp10;
}};
export const xs = [1, 2, 3];
export const viaInstance = arrayFunctor.map(xs, function (temp13) {
  const n = temp13;
  return n * 2;
});
export const ys = double(arrayFunctor, xs);
export const zs = double(optionFunctor, new MyOption.Some(5));
//# sourceMappingURL=./index.js.map
