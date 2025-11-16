import { InvokeCustomMatcherOrThrow } from "@escalier/runtime";
class C {
  constructor(temp2) {
    const msg = temp2;
    this.msg = msg;
  }
  static [Symbol.customMatcher](temp1) {
    const subject = temp1;
    return [subject.msg];
  }
}
const subject = new C("hello");
const [temp3] = InvokeCustomMatcherOrThrow(C, subject, undefined);
const msg = temp3;
//# sourceMappingURL=./index.js.map
