import { InvokeCustomMatcherOrThrow } from "escalier/runtime";
class C {
  constructor(temp2, temp3) {
    const msg = temp2;
    const value = temp3;
    this.msg = msg;
    this.value = value;
  }
  static [Symbol.customMatcher](temp1) {
    const subject = temp1;
    return [subject.msg, subject.value];
  }
}
const subject = new C(undefined, 5);
const [temp4 = "hello", temp5] = InvokeCustomMatcherOrThrow(C, subject, undefined);
const x = "hello" = temp4;
const y = temp5;
//# sourceMappingURL=./index.js.map
