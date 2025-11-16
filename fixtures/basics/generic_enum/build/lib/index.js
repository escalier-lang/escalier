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
let temp2;
let temp3;
temp3 = option;
if (temp3 instanceof MyOption.Some) {
  const [temp4] = InvokeCustomMatcherOrThrow(MyOption.Some, temp3, undefined);
  const value = temp4;
  temp2 = value;
} else if (temp3 instanceof MyOption.None) {
  const [] = InvokeCustomMatcherOrThrow(MyOption.None, temp3, undefined);
  temp2 = 0;
}
export const result = temp2;
//# sourceMappingURL=./index.js.map
