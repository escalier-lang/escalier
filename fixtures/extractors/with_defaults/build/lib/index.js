import { InvokeCustomMatcherOrThrow } from "@escalier/runtime";
export class C {
  constructor(temp2) {
    const msg = temp2;
    this.msg = msg;
  }
  static [Symbol.customMatcher](temp1) {
    const subject = temp1;
    return [subject.msg];
  }
}
export const subject = new C("hello");
export const [temp3 = "world"] = InvokeCustomMatcherOrThrow(C, subject, undefined);
export const msg = temp3;
//# sourceMappingURL=./index.js.map
