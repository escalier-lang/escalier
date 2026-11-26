import { InvokeCustomMatcherOrThrow } from "@escalier/runtime";
export const Boxed = {};
class Boxed__Of {
  constructor(temp1) {
    const value = temp1;
    this.value = value;
  }
  static [Symbol.customMatcher](subject) {
    return [subject.value];
  }
}
Boxed.Of = Boxed__Of;
export const Pair = {};
class Pair__Of {
  constructor(temp2) {
    const items = temp2;
    this.items = items;
  }
  static [Symbol.customMatcher](subject) {
    return [subject.items];
  }
}
Pair.Of = Pair__Of;
export const [temp3] = InvokeCustomMatcherOrThrow(Pair.Of, new Pair.Of([1, 2]), undefined);
export const [first, ] = temp3;
export function firstOf(temp4) {
  const [temp5] = InvokeCustomMatcherOrThrow(Pair.Of, temp4, undefined);
  const [a, ] = temp5;
  return a;
}
export const [temp6] = InvokeCustomMatcherOrThrow(Boxed.Of, new Boxed.Of({x: 3}), undefined);
export const {x} = temp6;
export const fromObject = x;
export const fromTuple = first;
//# sourceMappingURL=./index.js.map
