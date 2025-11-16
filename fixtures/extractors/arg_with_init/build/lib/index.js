import { InvokeCustomMatcherOrThrow } from "@escalier/runtime";
export class C {
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
export const subject = new C(undefined, 5);
export const [temp4 = "hello", temp5] = InvokeCustomMatcherOrThrow(C, subject, undefined);
export const x = temp4;
export const y = temp5;
//# sourceMappingURL=./index.js.map
