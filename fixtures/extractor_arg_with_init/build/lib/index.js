import { InvokeCustomMatcherOrThrow } from "@escalier/runtime";
export class C {
  constructor(temp1, temp2) {
    const msg = temp1;
    const value = temp2;
    this.msg = msg;
    this.value = value;
  }
  static [Symbol.customMatcher](temp3) {
    const subject = temp3;
    return [subject.msg, subject.value];
  }
}
export const subject = new C(undefined, 5);
export const [temp4 = "hello", temp5] = InvokeCustomMatcherOrThrow(C, subject, undefined);
export const x = temp4;
export const y = temp5;
//# sourceMappingURL=./index.js.map
