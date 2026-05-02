import { InvokeCustomMatcherOrThrow } from "@escalier/runtime";
export class C {
  constructor(temp1) {
    const msg = temp1;
    this.msg = msg;
  }
  static [Symbol.customMatcher](temp2) {
    const subject = temp2;
    return [subject.msg];
  }
}
export const subject = new C("hello");
export const [temp3 = "world"] = InvokeCustomMatcherOrThrow(C, subject, undefined);
export const msg = temp3;
//# sourceMappingURL=./index.js.map
